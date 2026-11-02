package derp

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/derp/artifact"
	"github.com/darkraise/darkpanel/internal/modules/tailnet"
)

const srIP = "203.0.113.10"

var srPin = "sha256-raw:" + strings.Repeat("ab", 32)

// srServe makes host id answer derp.server.status with s.
func srServe(h *dpHost, id string, s ServerStatus) {
	h.agent(id).Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return s, nil }
}

// srFail makes host id's derp.server.status answer err.
func srFail(h *dpHost, id string, err error) {
	h.agent(id).Reads["derp.server.status/v1"] = func(json.RawMessage) (any, error) { return nil, err }
}

// srNotAfter is the certificate expiry of the world's ip server: far enough for health ok.
func srNotAfter(h *dpHost) time.Time { return h.clock.Now().Add(200 * 24 * time.Hour) }

// srWorld is a controller with three hosts: h1 (alpha) runs an ip-mode server with a certificate, h2 (bravo) a
// letsencrypt server whose unit is stopped, h3 (charlie) none. No tailnet account is connected.
func srWorld(t *testing.T) *dpHost {
	t.Helper()
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	h.addHost("h2", "bravo", false)
	h.addHost("h3", "charlie", false)
	notAfter := srNotAfter(h)
	srServe(h, "h1", ServerStatus{Installed: true, Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", UnitActive: true, CertName: srPin, CertNotAfter: &notAfter})
	srServe(h, "h2", ServerStatus{Installed: true, Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1"})
	srServe(h, "h3", ServerStatus{})
	return h
}

func srDeps(h *dpHost) ext.Deps {
	env := h.ctlEnv("")
	return ext.Deps{DB: env.DB, Sealer: env.Sealer, Clock: env.Clock, Log: slog.New(slog.DiscardHandler), Hosts: env.Agents}
}

func srPass(t *testing.T, h *dpHost) {
	t.Helper()
	if err := h.dp().checkServers(context.Background(), srDeps(h), h.clock.Now()); err != nil {
		t.Fatalf("checkServers: %v", err)
	}
}

func srPut(t *testing.T, h *dpHost, r serverRecord) {
	t.Helper()
	if err := upsertServerRecord(context.Background(), h.ctlDB, r); err != nil {
		t.Fatal(err)
	}
}

func srLoad(t *testing.T, h *dpHost, id string) (serverRecord, bool) {
	t.Helper()
	r, ok, err := loadServerRecord(context.Background(), h.ctlDB.Read(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r, ok
}

func srSame(a, b serverRecord) bool {
	return a.HostID == b.HostID && a.Mode == b.Mode && a.HostName == b.HostName && a.RegionID == b.RegionID && a.RegionCode == b.RegionCode &&
		a.DerperVersion == b.DerperVersion && a.CertName == b.CertName && a.CertNotAfter.Equal(b.CertNotAfter) && a.UnitActive == b.UnitActive &&
		a.CheckedAt.Equal(b.CheckedAt) && a.Error == b.Error
}

// srList runs derp.server.list as the controller's read handler does and decodes its answer.
func srList(t *testing.T, h *dpHost) (Servers, string) {
	t.Helper()
	raw, err := h.dp().serverList().Handler.Read(context.Background(), h.ctlEnv(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var out Servers
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("value %s: %v", raw, err)
	}
	return out, string(raw)
}

func srWaitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestSrDeclarations(t *testing.T) {
	d := newDPHost(t).dp()
	def := d.serverList()
	if def.Spec.Name != "derp.server.list" || def.Spec.Version != 1 || def.Spec.Class != core.ClassRead || def.Spec.Side != core.SideController ||
		def.Spec.Summary == "" || len(def.Spec.LockKeys) != 0 || def.Spec.Internal || def.Spec.Available != nil || def.Handler.Read == nil {
		t.Errorf("serverList spec = %+v, want a version 1 controller read with a summary, no lock, not internal and no availability check", def.Spec)
	}
	if def.InputType != reflect.TypeFor[ServerListInput]() || def.OutputType != reflect.TypeFor[Servers]() {
		t.Errorf("serverList types = %v and %v, want ServerListInput and Servers", def.InputType, def.OutputType)
	}
	routes, services, err := (module{d: d}).Controller(ext.Deps{})
	if err != nil || len(routes) != 0 || len(services) != 1 || services[0].Name != "derp.servers" || services[0].Run == nil {
		t.Errorf("Controller = %d routes, %+v services, %v; want no route and the service derp.servers", len(routes), services, err)
	}
	if _, ok := Module().(ext.Controller); !ok {
		t.Error("Module() does not implement ext.Controller, so the server would never start the service")
	}
}

func TestSrServerHealth(t *testing.T) {
	now := time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)
	base := serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, UnitActive: true, CheckedAt: now}
	with := func(f func(r *serverRecord)) serverRecord {
		r := base
		f(&r)
		return r
	}
	tests := []struct {
		name    string
		rec     serverRecord
		health  string
		reasons []string
	}{
		{"nothing wrong", base, "ok", []string{}},
		{"the unit is stopped", with(func(r *serverRecord) { r.UnitActive = false }), "broken", []string{"derper is not running"}},
		{"the certificate expired a second ago", with(func(r *serverRecord) { r.CertNotAfter = now.Add(-time.Second) }), "broken",
			[]string{"the certificate expired on 2026-11-02"}},
		{"the certificate expires now", with(func(r *serverRecord) { r.CertNotAfter = now }), "degraded", []string{"the certificate expires on 2026-11-02"}},
		{"the certificate expires in 30 days", with(func(r *serverRecord) { r.CertNotAfter = now.Add(30 * 24 * time.Hour) }), "degraded",
			[]string{"the certificate expires on 2026-12-02"}},
		{"the certificate expires in 30 days and a second", with(func(r *serverRecord) { r.CertNotAfter = now.Add(30*24*time.Hour + time.Second) }), "ok", []string{}},
		{"checked ten minutes ago", with(func(r *serverRecord) { r.CheckedAt = now.Add(-10 * time.Minute) }), "ok", []string{}},
		{"checked ten minutes and a second ago", with(func(r *serverRecord) { r.CheckedAt = now.Add(-10*time.Minute - time.Second) }), "degraded",
			[]string{"the last check was at 2026-11-02T11:49:59Z"}},
		{"the last read failed", with(func(r *serverRecord) { r.Error = "tailscaled does not answer on its socket" }), "degraded",
			[]string{"the host could not be read: tailscaled does not answer on its socket"}},
		{"every condition at once", with(func(r *serverRecord) {
			r.UnitActive, r.CertNotAfter, r.CheckedAt, r.Error = false, now.Add(24*time.Hour), now.Add(-11*time.Minute), "tailscaled does not answer on its socket"
		}), "broken", []string{
			"derper is not running",
			"the certificate expires on 2026-11-03",
			"the last check was at 2026-11-02T11:49:00Z",
			"the host could not be read: tailscaled does not answer on its socket",
		}},
	}
	for _, tt := range tests {
		health, reasons := serverHealth(tt.rec, now)
		if health != tt.health || !reflect.DeepEqual(reasons, tt.reasons) {
			t.Errorf("%s: health %q, reasons %#v; want %q, %#v", tt.name, health, reasons, tt.health, tt.reasons)
		}
	}
}

// TestSrPassStoresInstalledServers: an installed server is stored with its status fields and the pass time, a host
// without a server loses its row, and so does a row whose host no longer exists; the status is read without a
// whois list.
func TestSrPassStoresInstalledServers(t *testing.T) {
	h := srWorld(t)
	now := h.clock.Now()
	for _, id := range []string{"h3", "h9"} {
		srPut(t, h, serverRecord{HostID: id, Mode: "ip", HostName: "203.0.113.99", DerperVersion: "v1.100.0", UnitActive: true, CheckedAt: now.Add(-time.Hour)})
	}
	srPass(t, h)
	want := map[string]serverRecord{
		"h1": {HostID: "h1", Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", CertName: srPin, CertNotAfter: srNotAfter(h), UnitActive: true, CheckedAt: now},
		"h2": {HostID: "h2", Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1", CheckedAt: now},
	}
	for id, w := range want {
		if got, ok := srLoad(t, h, id); !ok || !srSame(got, w) {
			t.Errorf("record of %s = %+v (found %v), want %+v", id, got, ok, w)
		}
	}
	for _, id := range []string{"h3", "h9"} {
		if _, ok := srLoad(t, h, id); ok {
			t.Errorf("the record of %s survived, want it deleted", id)
		}
	}
	for _, id := range []string{"h1", "h2", "h3"} {
		if calls := h.agent(id).Calls; !reflect.DeepEqual(calls, []string{"derp.server.status/v1 {}"}) {
			t.Errorf("calls to %s = %q, want one derp.server.status without a whois list", id, calls)
		}
	}
}

// TestSrPassRegion: with a connected tailnet a server's region is the policy's custom region holding a node of its
// host name (ignoring case); without one it is 0; a policy that cannot be read or parsed leaves the stored region.
func TestSrPassRegion(t *testing.T) {
	const withRegion = `{"derpMap": {"Regions": {"900": {"RegionID": 900, "RegionCode": "eu-derp", "RegionName": "EU",
		"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "DERP.Example.com"}]}}}}`
	tests := []struct {
		name      string
		connected bool
		policy    tailnet.Policy
		policyErr error
		id        int
		code      string
	}{
		{"a connected tailnet publishes the region", true, tailnet.Policy{Text: withRegion}, nil, 900, "eu-derp"},
		{"no tailnet means no region", false, tailnet.Policy{Text: withRegion}, nil, 0, ""},
		{"an unreadable policy keeps the stored region", true, tailnet.Policy{}, errors.New("provider down"), 901, "old"},
		{"an unparsable policy keeps the stored region", true, tailnet.Policy{Text: "{"}, nil, 901, "old"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := srWorld(t)
			h.tailnet, h.policy, h.policyErr = tt.connected, tt.policy, tt.policyErr
			srPut(t, h, serverRecord{HostID: "h2", Mode: "letsencrypt", HostName: "derp.example.com", RegionID: 901, RegionCode: "old",
				DerperVersion: "v1.100.0", UnitActive: true, CheckedAt: h.clock.Now().Add(-time.Minute)})
			srPass(t, h)
			got, ok := srLoad(t, h, "h2")
			if !ok || got.RegionID != tt.id || got.RegionCode != tt.code || got.DerperVersion != "v1.104.1" {
				t.Errorf("record of h2 = %+v (found %v), want region %d %q and the fresh version", got, ok, tt.id, tt.code)
			}
			if one, _ := srLoad(t, h, "h1"); one.RegionID != 0 || one.RegionCode != "" {
				t.Errorf("record of h1 has region %d %q, want none", one.RegionID, one.RegionCode)
			}
		})
	}
}

// TestSrPassKeepsTheRegionWhenTheTailnetCheckFails: a failed connected check is no pass error and no reason to
// forget the stored region.
func TestSrPassKeepsTheRegionWhenTheTailnetCheckFails(t *testing.T) {
	h := srWorld(t)
	srPut(t, h, serverRecord{HostID: "h2", Mode: "letsencrypt", HostName: "derp.example.com", RegionID: 901, RegionCode: "old",
		DerperVersion: "v1.100.0", UnitActive: true, CheckedAt: h.clock.Now().Add(-time.Minute)})
	d := h.dp()
	d.d.connected = func(context.Context, *store.DB) (bool, error) { return false, errors.New("database busy") }
	if err := d.checkServers(context.Background(), srDeps(h), h.clock.Now()); err != nil {
		t.Fatalf("checkServers: %v", err)
	}
	if got, ok := srLoad(t, h, "h2"); !ok || got.RegionID != 901 || got.RegionCode != "old" || got.DerperVersion != "v1.104.1" {
		t.Errorf("record of h2 = %+v (found %v), want the stored region 901 old and the fresh version", got, ok)
	}
}

// srLogs is a slog.Handler that keeps every record it is given.
type srLogs struct{ records []slog.Record }

func (l *srLogs) Enabled(context.Context, slog.Level) bool { return true }

func (l *srLogs) Handle(_ context.Context, r slog.Record) error {
	l.records = append(l.records, r.Clone())
	return nil
}

func (l *srLogs) WithAttrs([]slog.Attr) slog.Handler { return l }

func (l *srLogs) WithGroup(string) slog.Handler { return l }

// TestSrPassWarnsWhenThePolicyReadFails: a failed policy read keeps the stored region and logs one warning that
// does not carry the provider's error text.
func TestSrPassWarnsWhenThePolicyReadFails(t *testing.T) {
	const secret = "provider down: token tskey-api-secret"
	h := srWorld(t)
	h.tailnet, h.policyErr = true, errors.New(secret)
	srPut(t, h, serverRecord{HostID: "h2", Mode: "letsencrypt", HostName: "derp.example.com", RegionID: 901, RegionCode: "old",
		DerperVersion: "v1.100.0", UnitActive: true, CheckedAt: h.clock.Now().Add(-time.Minute)})
	logs := &srLogs{}
	deps := srDeps(h)
	deps.Log = slog.New(logs)
	if err := h.dp().checkServers(context.Background(), deps, h.clock.Now()); err != nil {
		t.Fatalf("checkServers: %v", err)
	}
	if got, ok := srLoad(t, h, "h2"); !ok || got.RegionID != 901 || got.RegionCode != "old" {
		t.Errorf("record of h2 = %+v (found %v), want the stored region 901 old", got, ok)
	}
	warnings := 0
	for _, r := range logs.records {
		if r.Level != slog.LevelWarn {
			continue
		}
		warnings++
		text := r.Message
		r.Attrs(func(a slog.Attr) bool {
			text += " " + a.String()
			return true
		})
		if strings.Contains(text, "tskey") || strings.Contains(text, "provider down") {
			t.Errorf("warning %q carries the provider's error text", text)
		}
	}
	if warnings != 1 {
		t.Errorf("%d warnings, want 1; records %+v", warnings, logs.records)
	}
}

// TestSrPassReadsThePolicyAtMostOnce: one policy read serves every server of a pass, and a pass that found no
// server reads none, so a tailnet without DERP costs the provider no call.
func TestSrPassReadsThePolicyAtMostOnce(t *testing.T) {
	const text = `{"derpMap": {"Regions": {"900": {"RegionID": 900, "RegionCode": "eu-derp",
		"Nodes": [{"Name": "900a", "RegionID": 900, "HostName": "derp.example.com"}]}}}}`
	for _, tt := range []struct {
		name  string
		setup func(h *dpHost)
		reads int
	}{
		{"two servers", func(*dpHost) {}, 1},
		{"no server", func(h *dpHost) {
			srServe(h, "h1", ServerStatus{})
			srServe(h, "h2", ServerStatus{})
		}, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := srWorld(t)
			h.tailnet = true
			tt.setup(h)
			reads := 0
			d := h.dp()
			d.d.policy = func(context.Context, core.Env) (tailnet.Policy, error) {
				reads++
				return tailnet.Policy{Text: text}, nil
			}
			if err := d.checkServers(context.Background(), srDeps(h), h.clock.Now()); err != nil {
				t.Fatalf("checkServers: %v", err)
			}
			if reads != tt.reads {
				t.Errorf("%d policy reads, want %d", reads, tt.reads)
			}
		})
	}
}

// TestSrPassKeepsAServerItCannotRead: a failed read keeps the row's fields and its check time and only sets the
// client-safe error; a host with no row stays without one; the next good read clears the error.
func TestSrPassKeepsAServerItCannotRead(t *testing.T) {
	h := srWorld(t)
	old := h.clock.Now().Add(-3 * time.Minute)
	stored := serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, RegionID: 900, RegionCode: "eu-derp", DerperVersion: "v1.100.0",
		CertName: srPin, CertNotAfter: srNotAfter(h), UnitActive: true, CheckedAt: old}
	srPut(t, h, stored)
	const detail = "tailscaled does not answer on its socket"
	srFail(h, "h1", errcode.New(errcode.TailscaleUnavailable, detail))
	srFail(h, "h2", errors.New("connection refused"))
	srPass(t, h)
	stored.Error = detail
	if got, ok := srLoad(t, h, "h1"); !ok || !srSame(got, stored) {
		t.Errorf("record of h1 = %+v (found %v), want the stored fields with error %q", got, ok, detail)
	}
	if _, ok := srLoad(t, h, "h2"); ok {
		t.Error("a host that never had a row got one from a failed read")
	}
	srFail(h, "h1", errors.New("connection refused"))
	srPass(t, h)
	if got, _ := srLoad(t, h, "h1"); got.Error != "the agent did not answer" || !got.CheckedAt.Equal(old) {
		t.Errorf("after a plain failure error %q, checked at %v; want %q and %v", got.Error, got.CheckedAt, "the agent did not answer", old)
	}
	notAfter := srNotAfter(h)
	srServe(h, "h1", ServerStatus{Installed: true, Mode: "ip", HostName: srIP, DerperVersion: "v1.104.1", UnitActive: true, CertName: srPin, CertNotAfter: &notAfter})
	srPass(t, h)
	if got, _ := srLoad(t, h, "h1"); got.Error != "" || !got.CheckedAt.Equal(h.clock.Now()) || got.DerperVersion != "v1.104.1" {
		t.Errorf("after a good read %+v, want no error, the pass time and the fresh version", got)
	}
}

// TestSrPassReadsAsTheServiceActor: the status reads carry the service's own admin principal.
func TestSrPassReadsAsTheServiceActor(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	var principals []core.Principal
	var refs []core.ActionRef
	agent := &coretest.FakeAgent{ReadFunc: func(_ context.Context, p core.Principal, a core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
		principals = append(principals, p)
		refs = append(refs, a)
		return json.Marshal(ServerStatus{})
	}}
	deps := srDeps(h)
	deps.Hosts = coretest.MapHosts{"h1": agent}
	if err := h.dp().checkServers(context.Background(), deps, h.clock.Now()); err != nil {
		t.Fatal(err)
	}
	want := core.Principal{Actor: "system:derp.servers", Scope: core.ScopeAdmin}
	if len(principals) != 1 || principals[0] != want || refs[0] != (core.ActionRef{Name: "derp.server.status", Version: 1}) {
		t.Errorf("reads as %+v of %+v, want one derp.server.status/v1 as %+v", principals, refs, want)
	}
}

// TestSrACancelledPassWritesNothing: a context that ends during a read ends the pass with its error before any
// row changes, and the service treats that as a normal stop.
func TestSrACancelledPassWritesNothing(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	stored := serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, DerperVersion: "v1.100.0", UnitActive: true, CheckedAt: h.clock.Now().Add(-time.Minute)}
	srPut(t, h, stored)
	cancelling := func(cancel context.CancelFunc) ext.Deps {
		agent := &coretest.FakeAgent{ReadFunc: func(ctx context.Context, _ core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
			cancel()
			return nil, ctx.Err()
		}}
		deps := srDeps(h)
		deps.Hosts = coretest.MapHosts{"h1": agent}
		return deps
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := h.dp().checkServers(ctx, cancelling(cancel), h.clock.Now()); !errors.Is(err, context.Canceled) {
		t.Errorf("checkServers = %v, want context.Canceled", err)
	}
	if got, ok := srLoad(t, h, "h1"); !ok || !srSame(got, stored) {
		t.Errorf("record of h1 = %+v (found %v), want it unchanged", got, ok)
	}

	ctx, cancel = context.WithCancel(context.Background())
	defer cancel()
	_, services, err := (module{d: h.dp()}).Controller(cancelling(cancel))
	if err != nil || len(services) != 1 {
		t.Fatalf("Controller = %+v, %v; want the one service", services, err)
	}
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v after its context ended during the first pass, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if n := h.clock.Pending(); n != 0 {
		t.Errorf("%d timers armed after the service ended, want none", n)
	}
}

// TestSrListReadsTheRecords: the read joins the rows with the host names, orders them by host id, leaves out a row
// without a host and names the controller's derper version.
func TestSrListReadsTheRecords(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	h.addHost("h2", "bravo", false)
	now := h.clock.Now()
	notAfter := now.Add(20 * 24 * time.Hour)
	srPut(t, h, serverRecord{HostID: "h2", Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1", CheckedAt: now})
	srPut(t, h, serverRecord{HostID: "h9", Mode: "ip", HostName: "203.0.113.98", DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: now})
	srPut(t, h, serverRecord{HostID: "h1", Mode: "ip", HostName: srIP, RegionID: 900, RegionCode: "eu-derp", DerperVersion: "v1.104.1", CertName: srPin,
		CertNotAfter: notAfter, UnitActive: true, CheckedAt: now})
	got, _ := srList(t, h)
	if got.AvailableVersion != artifact.DerperVersion || len(got.Servers) != 2 {
		t.Fatalf("servers %+v, want two and available version %s", got, artifact.DerperVersion)
	}
	one, two := got.Servers[0], got.Servers[1]
	if one.HostID != "h1" || one.Host != "alpha" || one.Mode != "ip" || one.HostName != srIP || one.RegionID != 900 || one.RegionCode != "eu-derp" ||
		one.DerperVersion != "v1.104.1" || one.CertName != srPin || one.CertNotAfter == nil || !one.CertNotAfter.Equal(notAfter) || !one.UnitActive ||
		one.Health != "degraded" || !reflect.DeepEqual(one.Reasons, []string{"the certificate expires on " + notAfter.UTC().Format("2006-01-02")}) ||
		!one.CheckedAt.Equal(now) || one.Error != "" {
		t.Errorf("first server %+v, want h1 (alpha) degraded by its certificate", one)
	}
	if two.HostID != "h2" || two.Host != "bravo" || two.CertNotAfter != nil || two.Health != "broken" || !reflect.DeepEqual(two.Reasons, []string{"derper is not running"}) {
		t.Errorf("second server %+v, want h2 (bravo) broken by its stopped unit and no certificate expiry", two)
	}
}

// TestSrListHealthIsComputedAtRead: a row the service stopped refreshing turns degraded as the clock moves, without
// a new pass.
func TestSrListHealthIsComputedAtRead(t *testing.T) {
	h := newDPHost(t)
	h.addHost("h1", "alpha", false)
	srPut(t, h, serverRecord{HostID: "h1", Mode: "letsencrypt", HostName: "derp.example.com", DerperVersion: "v1.104.1", UnitActive: true, CheckedAt: h.clock.Now()})
	if got, _ := srList(t, h); len(got.Servers) != 1 || got.Servers[0].Health != "ok" {
		t.Fatalf("servers %+v, want one ok server", got.Servers)
	}
	h.clock.Advance(11 * time.Minute)
	got, _ := srList(t, h)
	if len(got.Servers) != 1 || got.Servers[0].Health != "degraded" ||
		!reflect.DeepEqual(got.Servers[0].Reasons, []string{"the last check was at 2026-11-02T12:00:00Z"}) {
		t.Errorf("servers %+v, want one degraded server whose last check was at 12:00:00", got.Servers)
	}
}

func TestSrListWithoutRecords(t *testing.T) {
	_, raw := srList(t, newDPHost(t))
	if !strings.Contains(raw, `"servers":[]`) {
		t.Errorf("value %s, want an empty servers list and not null", raw)
	}
}

// TestSrServiceChecksEveryMinute: a pass runs at start and the next one a service period later, not before, and
// the service ends without error when its context does.
func TestSrServiceChecksEveryMinute(t *testing.T) {
	h := srWorld(t)
	_, services, err := (module{d: h.dp()}).Controller(srDeps(h))
	if err != nil || len(services) != 1 {
		t.Fatalf("Controller = %+v, %v; want the one service", services, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()

	srWaitUntil(t, "the first pass and the armed timer", func() bool { return h.clock.Pending() == 1 })
	if _, ok := srLoad(t, h, "h1"); !ok {
		t.Fatal("the pass at start stored no record for h1")
	}
	if err := deleteServerRecord(context.Background(), h.ctlDB, "h1"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(servicePeriod - time.Second)
	if _, ok := srLoad(t, h, "h1"); ok {
		t.Error("a pass ran before the service period ended")
	}
	if n := h.clock.Pending(); n != 1 {
		t.Errorf("%d timers armed before the period ended, want the one", n)
	}
	h.clock.Advance(time.Second)
	srWaitUntil(t, "the second pass", func() bool { _, ok := srLoad(t, h, "h1"); return ok })
	srWaitUntil(t, "the timer armed again after the second pass", func() bool { return h.clock.Pending() == 1 })

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run returned %v after its context ended, want nil", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
}

// TestSrServiceEndsOnAPassError: a pass that fails for a reason other than the context ending ends the service with
// that error, so the supervisor restarts it, and leaves no timer armed.
func TestSrServiceEndsOnAPassError(t *testing.T) {
	h := srWorld(t)
	ctx := context.Background()
	err := h.ctlDB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DROP TABLE derp_servers`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	_, services, err := (module{d: h.dp()}).Controller(srDeps(h))
	if err != nil || len(services) != 1 {
		t.Fatalf("Controller = %+v, %v; want the one service", services, err)
	}
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()
	select {
	case err := <-done:
		if err == nil {
			t.Error("Run returned nil after a failed pass, want the error")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after a failed pass")
	}
	if n := h.clock.Pending(); n != 0 {
		t.Errorf("%d timers armed after the service failed, want none", n)
	}
}

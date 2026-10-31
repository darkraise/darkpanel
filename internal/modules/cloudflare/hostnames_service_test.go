package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

const hsActor = "system:cloudflare.hostnames"

type hsFixture struct {
	t      *testing.T
	h      *cfHost
	srv    *fake.Server
	c      cf
	api    *api
	deps   ext.Deps
	audit  *coretest.MemAudit
	tunnel tunnelRow
	a, b   hostnameRow
}

// newHSFixture adds a.example.com and b.example.com to one tunnel through the
// action, then marks both rows unchecked so the first pass changes them.
func newHSFixture(t *testing.T) *hsFixture {
	t.Helper()
	h, srv := newCFHost(t), fake.New(t)
	a, b := haAdd(t, h, srv, "a.example.com"), haAdd(t, h, srv, "b.example.com")
	c := cfWith(srv)
	client, err := c.newAPI(fake.AccountID, []byte(fake.Token), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	tunnels, err := listTunnels(context.Background(), h.db.Read())
	if err != nil || len(tunnels) != 1 {
		t.Fatalf("tunnels = %+v, %v; want the one tunnel haAdd made", tunnels, err)
	}
	audit := &coretest.MemAudit{}
	f := &hsFixture{
		t: t, h: h, srv: srv, c: c, api: client, audit: audit, tunnel: tunnels[0], a: a, b: b,
		deps: ext.Deps{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.DiscardHandler), Audit: audit},
	}
	f.setHealth("a.example.com", "unknown")
	f.setHealth("b.example.com", "unknown")
	return f
}

func (f *hsFixture) setHealth(hostname, health string) {
	f.t.Helper()
	ctx := context.Background()
	err := f.h.db.Write(ctx, func(tx *sql.Tx) error { return setHostnameHealth(ctx, tx, hostname, health, f.h.clock.Now()) })
	if err != nil {
		f.t.Fatal(err)
	}
}

func (f *hsFixture) health(hostname string) string {
	f.t.Helper()
	row, ok, err := loadHostname(context.Background(), f.h.db.Read(), hostname)
	if err != nil || !ok {
		f.t.Fatalf("hostname %s: found %t, error %v", hostname, ok, err)
	}
	return row.Health
}

func (f *hsFixture) loadTunnel() tunnelRow {
	f.t.Helper()
	row, ok, err := loadTunnel(context.Background(), f.h.db.Read(), f.tunnel.ID)
	if err != nil || !ok {
		f.t.Fatalf("tunnel: found %t, error %v", ok, err)
	}
	return row
}

// pass runs one check at the clock's time and requires that it sent nothing
// but reads.
func (f *hsFixture) pass() {
	f.t.Helper()
	before := len(f.srv.Cloud().Requests())
	if err := f.c.checkHostnames(context.Background(), f.deps, f.h.clock.Now()); err != nil {
		f.t.Fatalf("checkHostnames: %v", err)
	}
	for _, r := range f.srv.Cloud().Requests()[before:] {
		if r.Method != http.MethodGet {
			f.t.Errorf("the check sent %s %s, want reads only", r.Method, r.Path)
		}
	}
}

// requests counts the requests the fake has received.
func (f *hsFixture) requests() int { return len(f.srv.Cloud().Requests()) }

// audits are the check rows recorded, newest first.
func (f *hsFixture) audits() []core.AuditEntry {
	f.t.Helper()
	got, _, err := f.audit.List(context.Background(), core.AuditQuery{Action: "cloudflare.hostnames"})
	if err != nil {
		f.t.Fatal(err)
	}
	for _, e := range got {
		if e.Actor != hsActor || e.Phase != "check" {
			f.t.Errorf("audit row %+v, want actor %s and phase check", e, hsActor)
		}
	}
	return got
}

// path is the fake's request path for suffix.
func (f *hsFixture) path(suffix string) string {
	f.t.Helper()
	if _, err := f.api.Zones(context.Background()); err != nil {
		f.t.Fatal(err)
	}
	reqs := f.srv.Cloud().Requests()
	prefix, ok := strings.CutSuffix(reqs[len(reqs)-1].Path, "/zones")
	if !ok {
		f.t.Fatalf("the zones request went to %q", reqs[len(reqs)-1].Path)
	}
	return prefix + suffix
}

func (f *hsFixture) waitHealth(hostname, want string) {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.health(hostname) != want {
		if time.Now().After(deadline) {
			f.t.Fatalf("health of %s is %q, want %q", hostname, f.health(hostname), want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestHSCheckRecordsHealthAndAuditsEachChange(t *testing.T) {
	f := newHSFixture(t)
	f.pass()
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if got := f.health(host); got != "ok" {
			t.Errorf("health of %s = %q, want ok", host, got)
		}
	}
	row := f.loadTunnel()
	if row.Drift || !row.CheckedAt.Equal(f.h.clock.Now()) {
		t.Errorf("tunnel = %+v, want no drift and the check time %v", row, f.h.clock.Now())
	}
	first := f.audits()
	if len(first) != 2 {
		t.Fatalf("audit rows after the first pass = %+v, want one per hostname whose health changed", first)
	}
	var in struct{ Hostname, From, To string }
	if err := json.Unmarshal(first[0].Input, &in); err != nil || in.From != "unknown" || in.To != "ok" || !strings.HasSuffix(in.Hostname, ".example.com") {
		t.Errorf("audit input %s (%v), want a hostname going from unknown to ok", first[0].Input, err)
	}
	f.h.clock.Advance(5 * time.Minute)
	f.pass()
	if got := f.audits(); len(got) != 2 {
		t.Errorf("a pass that changed nothing left %d audit rows, want the same two", len(got))
	}
	if got := f.loadTunnel(); !got.CheckedAt.Equal(f.h.clock.Now()) {
		t.Errorf("checked at %v, want the second pass's time %v", got.CheckedAt, f.h.clock.Now())
	}
}

func TestHSCheckReportsAMissingRule(t *testing.T) {
	f := newHSFixture(t)
	ctx := context.Background()
	cfg, _ := ingress.Remove(mustConfig(t, f.api, f.tunnel.ID), "a.example.com")
	if err := f.api.PutConfiguration(ctx, f.tunnel.ID, cfg); err != nil {
		t.Fatal(err)
	}
	f.pass()
	if a, b := f.health("a.example.com"), f.health("b.example.com"); a != "rule_missing" || b != "ok" {
		t.Errorf("health = %q and %q, want rule_missing and ok", a, b)
	}
	if !f.loadTunnel().Drift {
		t.Error("removing a rule at the provider was not reported as drift")
	}
}

func TestHSCheckReportsDriftFromAnotherClient(t *testing.T) {
	f := newHSFixture(t)
	ctx := context.Background()
	cfg, _, err := ingress.Add(mustConfig(t, f.api, f.tunnel.ID), ingress.Rule{Hostname: "other.example.com", Service: "http://10.0.0.9:80"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.api.PutConfiguration(ctx, f.tunnel.ID, cfg); err != nil {
		t.Fatal(err)
	}
	f.pass()
	if !f.loadTunnel().Drift || f.health("a.example.com") != "ok" {
		t.Errorf("tunnel %+v, health %q; want drift and the hostnames still ok", f.loadTunnel(), f.health("a.example.com"))
	}
	var drift int
	for _, e := range f.audits() {
		if strings.Contains(string(e.Input), `"drift":true`) {
			drift++
		}
	}
	if drift != 1 {
		t.Errorf("audit rows reporting the drift = %d, want 1", drift)
	}
}

func TestHSCheckReportsAMissingRecord(t *testing.T) {
	f := newHSFixture(t)
	if err := f.api.DeleteDNSRecord(context.Background(), f.a.ZoneID, f.a.RecordID); err != nil {
		t.Fatal(err)
	}
	f.pass()
	if a, b := f.health("a.example.com"), f.health("b.example.com"); a != "record_missing" || b != "ok" {
		t.Errorf("health = %q and %q, want record_missing and ok", a, b)
	}
	if f.loadTunnel().Drift {
		t.Error("a missing DNS record was reported as configuration drift")
	}
}

func TestHSCheckReportsAChangedRecordContent(t *testing.T) {
	f := newHSFixture(t)
	ctx := context.Background()
	// The provider cannot edit a record in place through the client, so the
	// fixture row is pointed at a record that holds other content.
	other, err := f.api.CreateDNSRecord(ctx, f.a.ZoneID, DNSRecord{Type: "CNAME", Name: "c.example.com", Content: "elsewhere.example.net", Proxied: true,
		Comment: "darkpanel:" + f.a.ChangeID})
	if err != nil {
		t.Fatal(err)
	}
	err = f.h.db.Write(ctx, func(tx *sql.Tx) error {
		return setHostnamePhase(ctx, tx, "a.example.com", "active", f.a.ZoneID, other.ID, f.h.clock.Now())
	})
	if err != nil {
		t.Fatal(err)
	}
	f.pass()
	if a, b := f.health("a.example.com"), f.health("b.example.com"); a != "record_changed" || b != "ok" {
		t.Errorf("health = %q and %q, want record_changed and ok", a, b)
	}
}

func TestHSCheckMarksAnUnreadableProviderUnknown(t *testing.T) {
	f := newHSFixture(t)
	cfg, _, err := ingress.Add(mustConfig(t, f.api, f.tunnel.ID), ingress.Rule{Hostname: "other.example.com", Service: "http://10.0.0.9:80"})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.api.PutConfiguration(context.Background(), f.tunnel.ID, cfg); err != nil {
		t.Fatal(err)
	}
	f.pass()
	audits := len(f.audits())
	f.srv.Cloud().Fail(http.MethodGet, f.path("/accounts/"+fake.AccountID+"/cfd_tunnel/"+f.tunnel.ID+"/configurations"),
		cloudfake.Fault{Status: http.StatusInternalServerError, Times: 8})
	f.h.clock.Advance(5 * time.Minute)
	f.pass()
	for _, host := range []string{"a.example.com", "b.example.com"} {
		if got := f.health(host); got != "unknown" {
			t.Errorf("health of %s = %q, want unknown", host, got)
		}
	}
	if !f.loadTunnel().Drift {
		t.Error("an unreadable configuration cleared the drift flag, want it left as stored")
	}
	if got := len(f.audits()); got != audits+2 {
		t.Errorf("audit rows = %d, want %d: one per hostname that became unknown", got, audits+2)
	}
}

func TestHSCheckLeavesATunnelWithAnOperationInFlight(t *testing.T) {
	f := newHSFixture(t)
	ctx := context.Background()
	busy := hostnameRow{Hostname: "busy.example.com", TunnelID: f.tunnel.ID, Service: "http://10.0.0.9:80", ZoneID: f.a.ZoneID,
		Phase: "adding_record", ChangeID: "01HBUSY", Health: "unknown", UpdatedAt: f.h.clock.Now()}
	if err := f.h.db.Write(ctx, func(tx *sql.Tx) error { return insertHostname(ctx, tx, busy) }); err != nil {
		t.Fatal(err)
	}
	before := f.requests()
	f.pass()
	if n := f.requests() - before; n != 0 {
		t.Errorf("the check sent %d requests for a tunnel with a hostname mid-phase, want none", n)
	}
	if f.health("a.example.com") != "unknown" || f.health("busy.example.com") != "unknown" {
		t.Error("the check changed a health while an operation was in flight")
	}
}

func TestHSCheckWithoutAnAccountDoesNothing(t *testing.T) {
	h := newCFHost(t)
	deps := ext.Deps{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Audit: &coretest.MemAudit{}}
	if err := (cf{}).checkHostnames(context.Background(), deps, h.clock.Now()); err != nil {
		t.Errorf("checkHostnames without an account = %v, want nil", err)
	}
}

func TestHSServiceChecksEveryFiveMinutes(t *testing.T) {
	f := newHSFixture(t)
	routes, services, err := (module{c: f.c}).Controller(f.deps)
	if err != nil || len(routes) != 0 || len(services) != 1 || services[0].Name != "cloudflare.hostnames" {
		t.Fatalf("Controller = %d routes, %+v services, %v; want no route and the service cloudflare.hostnames", len(routes), services, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	done := make(chan error, 1)
	go func() { done <- services[0].Run(ctx) }()

	f.waitHealth("a.example.com", "ok")
	f.setHealth("a.example.com", "unknown")
	deadline := time.Now().Add(10 * time.Second)
	for f.h.clock.Pending() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("pending timers = %d, want the service waiting on one", f.h.clock.Pending())
		}
		time.Sleep(5 * time.Millisecond)
	}
	f.h.clock.Advance(4 * time.Minute)
	if got := f.health("a.example.com"); got != "unknown" {
		t.Errorf("health after 4 minutes = %q, want no pass yet", got)
	}
	f.h.clock.Advance(time.Minute)
	f.waitHealth("a.example.com", "ok")

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

func mustConfig(t *testing.T, a *api, tunnelID string) ingress.Config {
	t.Helper()
	cfg, err := a.Configuration(context.Background(), tunnelID)
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

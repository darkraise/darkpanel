package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/controller/cloud/cloudfake"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/fake"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

const (
	haTunnelName = "darkpanel-ha"
	haService    = "http://10.0.0.5:8080"
	haZoneName   = "example.com"
)

var haAdmin = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "203.0.113.7"}

// haModule serves the hostname add action under the cloudflare module id, so
// the engine tests do not depend on the module's Manifest.
type haModule struct{ c cf }

func (m haModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.c.hostnameAdd()}}
}

func haInputWith(tunnel, hostname, service string) string {
	return fmt.Sprintf(`{"tunnel":%q,"hostname":%q,"service":%q}`, tunnel, hostname, service)
}

func haInput(hostname string) string { return haInputWith(haTunnelName, hostname, haService) }

// haPrepare makes sure the host has a connected account, the zone example.com
// and the tunnel darkpanel-ha, on the fake and in the database, and returns
// what a test needs to reach them. Calling it twice changes nothing.
func haPrepare(t *testing.T, h *cfHost, srv *fake.Server) (c cf, a *api, zoneID, tunnelID string) {
	t.Helper()
	ctx := context.Background()
	c = cfWith(srv)
	if _, ok, err := loadAccount(ctx, h.db.Read()); err != nil {
		t.Fatal(err)
	} else if !ok {
		acConnect(t, h, srv)
	}
	a, err := c.newAPI(fake.AccountID, []byte(fake.Token), h.clock)
	if err != nil {
		t.Fatal(err)
	}
	zones, err := a.Zones(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, z := range zones {
		if z.Name == haZoneName {
			zoneID = z.ID
		}
	}
	if zoneID == "" {
		zoneID = srv.AddZone(haZoneName)
	}
	row, ok, err := loadTunnel(ctx, h.db.Read(), haTunnelName)
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		return c, a, zoneID, row.ID
	}
	tunnelID = srv.AddTunnel(haTunnelName)
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		return insertTunnel(ctx, tx, tunnelRow{ID: tunnelID, Name: haTunnelName, ChangeID: "01HATUNNEL", CreatedAt: h.clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, a, zoneID, tunnelID
}

// haEngine is a controller engine over the host's database serving the
// hostname add action. The caller closes it.
func haEngine(t *testing.T, h *cfHost, c cf) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(haModule{c})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base:   core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eng.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	return eng
}

// haApplyPlan plans and applies the hostname add action with input.
func haApplyPlan(t *testing.T, eng *lifecycle.Engine, input string) core.ChangeView {
	t.Helper()
	ctx := context.Background()
	pv, err := eng.Plan(ctx, haAdmin, core.ActionRef{Name: "cloudflare.hostname.add", Version: 1}, json.RawMessage(input), core.PlanOptions{})
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	cv, err := eng.Apply(ctx, haAdmin, pv.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	return cv
}

// haAdd adds an active hostname to the tunnel darkpanel-ha through the
// action, preparing the account, zone and tunnel first, and returns its row.
// It builds an engine of its own and closes it before it returns.
func haAdd(t *testing.T, h *cfHost, srv *fake.Server, hostname string) hostnameRow { //nolint:unused // shared helper of the other hostname test files
	t.Helper()
	c, _, _, _ := haPrepare(t, h, srv)
	eng := haEngine(t, h, c)
	defer eng.Close()
	if cv := haApplyPlan(t, eng, haInput(hostname)); cv.State != core.StateCompleted {
		t.Fatalf("adding %s ended %s: %+v", hostname, cv.State, cv.Error)
	}
	row, ok, err := loadHostname(context.Background(), h.db.Read(), hostname)
	if err != nil || !ok {
		t.Fatalf("hostname %s: found %t, error %v", hostname, ok, err)
	}
	return row
}

type haFixture struct {
	t                *testing.T
	h                *cfHost
	srv              *fake.Server
	c                cf
	api              *api
	eng              *lifecycle.Engine
	zoneID, tunnelID string
}

func newHAFixture(t *testing.T) *haFixture {
	t.Helper()
	h, srv := newCFHost(t), fake.New(t)
	c, a, zoneID, tunnelID := haPrepare(t, h, srv)
	eng := haEngine(t, h, c)
	t.Cleanup(eng.Close)
	return &haFixture{t: t, h: h, srv: srv, c: c, api: a, eng: eng, zoneID: zoneID, tunnelID: tunnelID}
}

func (f *haFixture) plan(input string, call *core.CallContext) (core.PlanView, error) {
	return f.eng.Plan(context.Background(), haAdmin, core.ActionRef{Name: "cloudflare.hostname.add", Version: 1},
		json.RawMessage(input), core.PlanOptions{Call: call})
}

func (f *haFixture) apply(input string) core.ChangeView {
	f.t.Helper()
	return haApplyPlan(f.t, f.eng, input)
}

// settle waits for the change to reach want.
func (f *haFixture) settle(id, want string) core.ChangeView {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		cv, err := f.eng.Change(context.Background(), id)
		if err != nil {
			f.t.Fatal(err)
		}
		if cv.State == want {
			return cv
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is %s, want %s: %+v", id, cv.State, want, cv.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (f *haFixture) config() ingress.Config {
	f.t.Helper()
	cfg, err := f.api.Configuration(context.Background(), f.tunnelID)
	if err != nil {
		f.t.Fatal(err)
	}
	return cfg
}

// putRule adds a rule to the tunnel's configuration at the provider, as
// another client would.
func (f *haFixture) putRule(hostname, service string) {
	f.t.Helper()
	cfg, _, err := ingress.Add(f.config(), ingress.Rule{Hostname: hostname, Service: service})
	if err != nil {
		f.t.Fatal(err)
	}
	if err := f.api.PutConfiguration(context.Background(), f.tunnelID, cfg); err != nil {
		f.t.Fatal(err)
	}
}

func (f *haFixture) records(name string) []DNSRecord {
	f.t.Helper()
	rs, err := f.api.DNSRecords(context.Background(), f.zoneID, name, "")
	if err != nil {
		f.t.Fatal(err)
	}
	return rs
}

func (f *haFixture) row(hostname string) (hostnameRow, bool) {
	f.t.Helper()
	row, ok, err := loadHostname(context.Background(), f.h.db.Read(), hostname)
	if err != nil {
		f.t.Fatal(err)
	}
	return row, ok
}

func (f *haFixture) tunnel() tunnelRow {
	f.t.Helper()
	row, ok, err := loadTunnel(context.Background(), f.h.db.Read(), f.tunnelID)
	if err != nil || !ok {
		f.t.Fatalf("tunnel row: found %t, error %v", ok, err)
	}
	return row
}

func (f *haFixture) effects(id string) []core.EffectView {
	f.t.Helper()
	effs, err := f.eng.Effects(context.Background(), id)
	if err != nil {
		f.t.Fatal(err)
	}
	return effs
}

// writes names the methods of the writes the fake received, in order.
func (f *haFixture) writes() []string {
	var out []string
	for _, r := range f.srv.Cloud().Requests() {
		if r.Method != http.MethodGet {
			out = append(out, r.Method)
		}
	}
	return out
}

// path is the fake's request path for suffix, found from the path of a
// request the client sent for zones.
func (f *haFixture) path(suffix string) string {
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

func haDiffText(diff []core.DiffLine) string {
	var b strings.Builder
	for _, l := range diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

func TestHAAddWritesTheRuleBeforeTheCatchAllAndTheRecord(t *testing.T) {
	f := newHAFixture(t)
	input := haInput("app.example.com")
	pv, err := f.plan(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	diff := haDiffText(pv.Diff)
	for _, want := range []string{"app.example.com", haService, f.tunnelID + ".cfargotunnel.com", "not detected"} {
		if !strings.Contains(diff, want) {
			t.Errorf("the plan's diff lacks %q:\n%s", want, diff)
		}
	}
	if w := f.writes(); len(w) != 0 {
		t.Errorf("planning sent the writes %v, want none", w)
	}
	cv, err := f.eng.Apply(context.Background(), haAdmin, pv.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v; want a completed change", cv, err)
	}
	var out HostnameView
	if err := json.Unmarshal(cv.Output, &out); err != nil {
		t.Fatal(err)
	}
	recs := f.records("app.example.com")
	if len(recs) != 1 {
		t.Fatalf("records for app.example.com = %+v, want exactly one", recs)
	}
	r := recs[0]
	if r.Type != "CNAME" || r.Content != f.tunnelID+".cfargotunnel.com" || !r.Proxied || r.Comment != "darkpanel:"+cv.ID {
		t.Errorf("record = %+v, want a proxied CNAME to the tunnel commented darkpanel:%s", r, cv.ID)
	}
	if out.Hostname != "app.example.com" || out.Tunnel != f.tunnelID || out.Service != haService || out.Phase != "active" ||
		out.Health != "ok" || out.RecordID != r.ID {
		t.Errorf("output = %+v, want the active hostname with record %s", out, r.ID)
	}
	cfg := f.config()
	rule, ok := ingress.Lookup(cfg, "app.example.com")
	if !ok || rule.Service != haService {
		t.Errorf("rule = %+v (found %t), want service %s", rule, ok, haService)
	}
	if last := cfg.Ingress[len(cfg.Ingress)-1]; last.Hostname != "" || last.Service != ingress.CatchAll {
		t.Errorf("the last rule is %+v, want the catch-all %s", last, ingress.CatchAll)
	}
	row, found := f.row("app.example.com")
	if !found || row.Phase != "active" || row.ChangeID != cv.ID || row.RecordID != r.ID || row.ZoneID != f.zoneID || row.Service != haService {
		t.Errorf("row = %+v (found %t), want the active row of change %s", row, found, cv.ID)
	}
	if got := f.tunnel().ConfigHash; got != ingress.Hash(cfg) {
		t.Errorf("stored configuration hash = %q, want the hash of the configuration read back, %q", got, ingress.Hash(cfg))
	}
	effs := f.effects(cv.ID)
	if len(effs) != 2 {
		t.Fatalf("effects = %+v, want two", effs)
	}
	if e := effs[0]; e.Resource != "cloudflare.ingress:"+f.tunnelID+"/app.example.com" || e.Kind != core.EffectModify || e.State != core.EffectMade || e.Local {
		t.Errorf("effect 1 = %+v, want the made, non-local ingress modification", e)
	}
	if e := effs[1]; e.Resource != "cloudflare.dnsrecord:"+f.zoneID+"/"+r.ID || e.Kind != core.EffectCreate || e.State != core.EffectMade || e.Local {
		t.Errorf("effect 2 = %+v, want the made, non-local DNS record creation", e)
	}
	if w := strings.Join(f.writes(), " "); w != "PUT POST" {
		t.Errorf("writes = %q, want one PUT of the configuration and one POST of the record", w)
	}
}

func TestHAAddCarriesUnrelatedRulesOver(t *testing.T) {
	f := newHAFixture(t)
	var cfg ingress.Config
	const raw = `{"ingress":[{"hostname":"keep.example.com","service":"http://10.0.0.9:80","originRequest":{"connectTimeout":"30s"}},{"service":"http_status:404"}],"warp-routing":{"enabled":true}}`
	if err := json.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatal(err)
	}
	if err := f.api.PutConfiguration(context.Background(), f.tunnelID, cfg); err != nil {
		t.Fatal(err)
	}
	f.apply(haInput("app.example.com"))
	got := f.config()
	kept, ok := ingress.Lookup(got, "keep.example.com")
	b, _ := json.Marshal(kept)
	if !ok || kept.Service != "http://10.0.0.9:80" || !strings.Contains(string(b), `"connectTimeout":"30s"`) {
		t.Errorf("rule keep.example.com = %s (found %t), want it unchanged with its origin settings", b, ok)
	}
	all, _ := json.Marshal(got)
	if !strings.Contains(string(all), `"warp-routing":{"enabled":true}`) {
		t.Errorf("configuration %s lost its top-level warp-routing member", all)
	}
	if len(got.Ingress) != 3 {
		t.Errorf("ingress = %+v, want keep, the new rule and the catch-all", got.Ingress)
	}
}

func TestHAAddRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *haFixture)
		input func(f *haFixture) string
		want  errcode.Code
	}{
		{"unknown tunnel", nil, func(*haFixture) string { return haInputWith("darkpanel-nope", "app.example.com", haService) }, errcode.NotFound},
		{"uppercase hostname", nil, func(*haFixture) string { return haInputWith(haTunnelName, "App.example.com", haService) }, errcode.InvalidInput},
		{"hostname with one label", nil, func(*haFixture) string { return haInputWith(haTunnelName, "single", haService) }, errcode.InvalidInput},
		{"wildcard hostname", nil, func(*haFixture) string { return haInputWith(haTunnelName, "*.example.com", haService) }, errcode.InvalidInput},
		{"leading hyphen", nil, func(*haFixture) string { return haInputWith(haTunnelName, "-a.example.com", haService) }, errcode.InvalidInput},
		{"unsupported scheme", nil, func(*haFixture) string { return haInputWith(haTunnelName, "app.example.com", "ftp://10.0.0.5:21") }, errcode.InvalidInput},
		{"service without a port", nil, func(*haFixture) string { return haInputWith(haTunnelName, "app.example.com", "http://10.0.0.5") }, errcode.InvalidInput},
		{"service with a path", nil, func(*haFixture) string { return haInputWith(haTunnelName, "app.example.com", "http://10.0.0.5:80/x") }, errcode.InvalidInput},
		{"status out of range", nil, func(*haFixture) string { return haInputWith(haTunnelName, "app.example.com", "http_status:99") }, errcode.InvalidInput},
		{"hostname outside every zone", nil, func(*haFixture) string { return haInputWith(haTunnelName, "app.example.org", haService) }, errcode.InvalidInput},
		{"hostname already managed", func(f *haFixture) { f.apply(haInput("dup.example.com")) },
			func(*haFixture) string { return haInput("dup.example.com") }, errcode.InvalidInput},
		{"dns record exists", func(f *haFixture) {
			_, err := f.api.CreateDNSRecord(context.Background(), f.zoneID, DNSRecord{Type: "CNAME", Name: "taken.example.com", Content: "x.example.net"})
			if err != nil {
				f.t.Fatal(err)
			}
		}, func(*haFixture) string { return haInput("taken.example.com") }, errcode.InvalidInput},
		{"rule exists", func(f *haFixture) { f.putRule("ruled.example.com", "http://10.0.0.9:80") },
			func(*haFixture) string { return haInput("ruled.example.com") }, errcode.InvalidInput},
		{"configuration drift", func(f *haFixture) {
			err := f.h.db.Write(context.Background(), func(tx *sql.Tx) error { return setTunnelHash(context.Background(), tx, f.tunnelID, "stale") })
			if err != nil {
				f.t.Fatal(err)
			}
		}, func(*haFixture) string { return haInput("app.example.com") }, errcode.CloudConflict},
		{"panel gateway service", nil, func(*haFixture) string {
			return haInputWith(haTunnelName, "app.example.com", "http://172.31.255.1:8080")
		}, errcode.CloudflareReserved},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHAFixture(t)
			if c.setup != nil {
				c.setup(f)
			}
			before := len(f.writes())
			_, err := f.plan(c.input(f), nil)
			if err == nil || errcode.From(err).Code != c.want {
				t.Errorf("plan error = %v, want code %s", err, c.want)
			}
			if n := len(f.writes()); n != before {
				t.Errorf("the refused plan sent %d writes", n-before)
			}
		})
	}
}

func TestHAReservedServiceNeedsTheExposureParent(t *testing.T) {
	f := newHAFixture(t)
	input := haInputWith(haTunnelName, "panel.example.com", "https://172.31.255.1:8443")
	call := &core.CallContext{ParentChangeID: "01HPARENT", ParentAction: "panel.exposure.add", StepID: "hostname"}
	if _, err := f.plan(input, call); err != nil {
		t.Errorf("plan as a step of panel.exposure.add: %v, want it accepted", err)
	}
	for name, call := range map[string]*core.CallContext{
		"no parent":           nil,
		"another parent":      {ParentChangeID: "01HPARENT", ParentAction: "panel.exposure.remove", StepID: "hostname"},
		"an unrelated parent": {ParentChangeID: "01HPARENT", ParentAction: "cloudflare.expose.container", StepID: "hostname"},
	} {
		if _, err := f.plan(input, call); err == nil || errcode.From(err).Code != errcode.CloudflareReserved {
			t.Errorf("%s: plan error = %v, want %s", name, err, errcode.CloudflareReserved)
		}
	}
}

func TestHAReserved(t *testing.T) {
	for service, want := range map[string]bool{
		"http://172.31.255.1:8080": true, "tcp://172.31.255.254:22": true, "https://172.31.255.7": true,
		"http://172.31.254.1:80": false, "http://172.31.256.1:80": false, "http://panel.internal:80": false,
		"http_status:404": false, "ssh://[::1]:22": false, "": false,
	} {
		if got := reserved(service); got != want {
			t.Errorf("reserved(%q) = %t, want %t", service, got, want)
		}
	}
}

func TestHACheckDrift(t *testing.T) {
	f := newHAFixture(t)
	ctx := context.Background()
	f.putRule("a.example.com", "http://10.0.0.9:80")
	cfg := f.config()
	for _, c := range []struct {
		name, hash string
		conflict   bool
	}{{"none stored", "", false}, {"matching hash", ingress.Hash(cfg), false}, {"another hash", "stale", true}} {
		got, err := f.c.checkDrift(ctx, f.api, tunnelRow{ID: f.tunnelID, Name: haTunnelName, ConfigHash: c.hash})
		switch {
		case c.conflict && (err == nil || errcode.From(err).Code != errcode.CloudConflict || !strings.Contains(err.Error(), haTunnelName)):
			t.Errorf("%s: error %v, want cloud.conflict naming the tunnel", c.name, err)
		case !c.conflict && (err != nil || ingress.Hash(got) != ingress.Hash(cfg)):
			t.Errorf("%s: %v, hash %q; want the configuration read", c.name, err, ingress.Hash(got))
		}
	}
}

func TestHAPlanStateHashCoversTheConfiguration(t *testing.T) {
	f := newHAFixture(t)
	input := haInput("app.example.com")
	pv, err := f.plan(input, nil)
	if err != nil {
		t.Fatal(err)
	}
	f.putRule("other.example.com", "http://10.0.0.9:80")
	_, err = f.eng.Apply(context.Background(), haAdmin, pv.PlanID, core.ApplyOptions{})
	if err == nil || errcode.From(err).Code != errcode.PlanStateChanged {
		t.Errorf("apply of a plan made before the configuration changed: %v, want %s", err, errcode.PlanStateChanged)
	}
	if w := f.writes(); len(w) != 1 {
		t.Errorf("writes = %v, want only the test's own PUT", w)
	}
}

func TestHAUndoRemovesTheRecordAndTheRule(t *testing.T) {
	f := newHAFixture(t)
	cv := f.apply(haInput("app.example.com"))
	if _, err := f.eng.Undo(context.Background(), haAdmin, cv.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.settle(cv.ID, core.StateReverted)
	if recs := f.records("app.example.com"); len(recs) != 0 {
		t.Errorf("records after the undo = %+v, want none", recs)
	}
	cfg := f.config()
	if _, ok := ingress.Lookup(cfg, "app.example.com"); ok {
		t.Error("the rule is still in the configuration after the undo")
	}
	if last := cfg.Ingress[len(cfg.Ingress)-1]; last.Service != ingress.CatchAll {
		t.Errorf("the last rule is %+v, want the catch-all", last)
	}
	if _, ok := f.row("app.example.com"); ok {
		t.Error("the hostname row is still stored after the undo")
	}
	if got := f.tunnel().ConfigHash; got != ingress.Hash(cfg) {
		t.Errorf("stored hash = %q, want the hash of the configuration after the undo", got)
	}
	for _, e := range f.effects(cv.ID) {
		if e.State != core.EffectUndone {
			t.Errorf("effect %d is %s, want undone", e.Seq, e.State)
		}
	}
}

func TestHAUndoRefusesWhenTheRecordIsGone(t *testing.T) {
	f := newHAFixture(t)
	cv := f.apply(haInput("app.example.com"))
	rec := f.records("app.example.com")[0]
	if err := f.api.DeleteDNSRecord(context.Background(), f.zoneID, rec.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Undo(context.Background(), haAdmin, cv.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.settle(cv.ID, core.StateNeedsRepair)
	if _, ok := ingress.Lookup(f.config(), "app.example.com"); !ok {
		t.Error("the undo removed the rule although the record's effect was in conflict")
	}
	effs := f.effects(cv.ID)
	if effs[0].State != core.EffectMade || effs[1].State != core.EffectUnresolved || effs[1].Reason != core.ReasonConflict {
		t.Errorf("effects = %+v, want the rule made and the record unresolved with reason conflict", effs)
	}
}

func TestHALiveDNSRefusalRollsTheRuleBack(t *testing.T) {
	f := newHAFixture(t)
	f.srv.Cloud().Fail(http.MethodPost, f.path("/zones/"+f.zoneID+"/dns_records"),
		cloudfake.Fault{Status: http.StatusBadRequest, Body: `{"success":false,"errors":[{"code":9005,"message":"invalid"}],"result":null}`})
	cv := f.apply(haInput("app.example.com"))
	cv = f.settle(cv.ID, core.StateReverted)
	if _, ok := ingress.Lookup(f.config(), "app.example.com"); ok {
		t.Error("the rule is still in the configuration after the recovery handler rolled the change back")
	}
	if _, ok := f.row("app.example.com"); ok {
		t.Error("the hostname row is still stored after the rollback")
	}
	if recs := f.records("app.example.com"); len(recs) != 0 {
		t.Errorf("records = %+v, want none", recs)
	}
	if got := f.tunnel().ConfigHash; got != ingress.Hash(f.config()) {
		t.Errorf("stored hash = %q, want the hash of the configuration after the rollback", got)
	}
	effs := f.effects(cv.ID)
	if len(effs) != 2 || effs[0].State != core.EffectMade || effs[1].State != core.EffectSkipped {
		t.Errorf("effects = %+v, want the rule made and the refused record skipped", effs)
	}
}

func TestHAUncertainRecordCreateNeedsRepair(t *testing.T) {
	f := newHAFixture(t)
	f.srv.Cloud().Fail(http.MethodPost, f.path("/zones/"+f.zoneID+"/dns_records"), cloudfake.Fault{LoseResponse: true})
	cv := f.apply(haInput("app.example.com"))
	cv = f.settle(cv.ID, core.StateNeedsRepair)
	effs := f.effects(cv.ID)
	if len(effs) != 2 || effs[1].State != core.EffectUnresolved || effs[1].Reason != core.ReasonUncertain ||
		!strings.HasPrefix(effs[1].Resource, "cloudflare.dnsrecord:?darkpanel:") {
		t.Fatalf("effects = %+v, want the record creation unresolved as uncertain and still unnamed", effs)
	}
	res, err := f.eng.Observe(context.Background(), haAdmin, cv.ID, 2)
	recs := f.records("app.example.com")
	if err != nil || len(recs) != 1 {
		t.Fatalf("observe error %v, records %+v; want the committed record found", err, recs)
	}
	if res.Observation.Matches != 1 || res.Observation.Resource != "cloudflare.dnsrecord:"+f.zoneID+"/"+recs[0].ID {
		t.Errorf("observation = %+v, want one match naming record %s", res.Observation, recs[0].ID)
	}
	if row, ok := f.row("app.example.com"); !ok || row.Phase != "adding_record" {
		t.Errorf("row = %+v (found %t), want it left in adding_record", row, ok)
	}
	for _, w := range f.writes() {
		if w == http.MethodDelete {
			t.Error("a DELETE was sent while the effect was unresolved")
		}
	}
}

// haInsertAdding stores the tunnel's hash and a hostname row in phase
// adding_record for changeID, as an add leaves them after its rule write.
func haInsertAdding(t *testing.T, f *haFixture, host, changeID, hash string) {
	t.Helper()
	ctx := context.Background()
	row := hostnameRow{Hostname: host, TunnelID: f.tunnelID, Service: haService, ZoneID: f.zoneID, Phase: "adding_record", ChangeID: changeID,
		Health: "unknown", UpdatedAt: f.h.clock.Now()}
	err := f.h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := setTunnelHash(ctx, tx, f.tunnelID, hash); err != nil {
			return err
		}
		return insertHostname(ctx, tx, row)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// haMadeRule journals the change's ingress effect for host as made, with the
// rule as the configuration now holds it.
func haMadeRule(t *testing.T, f *haFixture, env core.Env, host string) {
	t.Helper()
	ctx := context.Background()
	e := core.Effect{Kind: core.EffectModify, Resource: "cloudflare.ingress:" + f.tunnelID + "/" + host, CommitWait: cloud.CommitWait,
		Before: core.StateDoc{State: json.RawMessage(`{"rule":null}`)}}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		t.Fatal(err)
	}
	rule, _ := ingress.Lookup(f.config(), host)
	b, _ := json.Marshal(rule)
	if err := env.Effects.Made(ctx, seq, e.Resource, core.StateDoc{State: json.RawMessage(`{"rule":` + string(b) + `}`)}); err != nil {
		t.Fatal(err)
	}
}

func TestHARecoverRollsBackItsOwnRuleWhoseHashWasNotStored(t *testing.T) {
	const host, changeID = "lag.example.com", "01HRECLAG"
	for _, foreign := range []bool{false, true} {
		t.Run(fmt.Sprintf("foreign rule %t", foreign), func(t *testing.T) {
			f := newHAFixture(t)
			f.putRule("keep.example.com", "http://10.0.0.9:80")
			haInsertAdding(t, f, host, changeID, ingress.Hash(f.config()))
			f.putRule(host, haService)
			if foreign {
				f.putRule("other.example.com", "http://10.0.0.8:80")
			}
			env := f.h.env(changeID)
			haMadeRule(t, f, env, host)
			out, err := hostnameHandler{c: f.c}.Recover(context.Background(), env)
			cfg := f.config()
			_, hasRule := ingress.Lookup(cfg, host)
			_, hasRow := f.row(host)
			if foreign {
				if err == nil || errcode.From(err).Code != errcode.CloudConflict || !hasRule || !hasRow {
					t.Errorf("Recover = %+v, %v; rule %t, row %t; want cloud.conflict with the rule and the row kept", out, err, hasRule, hasRow)
				}
				return
			}
			if err != nil || out.State != core.RecoveryRolledBack || hasRule || hasRow {
				t.Fatalf("Recover = %+v, %v; rule %t, row %t; want rolled back with the rule and the row gone", out, err, hasRule, hasRow)
			}
			if _, ok := ingress.Lookup(cfg, "keep.example.com"); !ok {
				t.Error("the rollback dropped an unrelated rule")
			}
			if got := f.tunnel().ConfigHash; got != ingress.Hash(cfg) {
				t.Errorf("stored hash = %q, want the hash of the configuration after the rollback", got)
			}
		})
	}
}

func TestHARecoverCountsAGoneTunnelAsRemoved(t *testing.T) {
	const host, changeID = "gone.example.com", "01HRECGONE"
	f := newHAFixture(t)
	haInsertAdding(t, f, host, changeID, "")
	f.putRule(host, haService)
	env := f.h.env(changeID)
	haMadeRule(t, f, env, host)
	f.srv.Cloud().Fail(http.MethodGet, f.path("/accounts/"+fake.AccountID+"/cfd_tunnel/"+f.tunnelID+"/configurations"),
		cloudfake.Fault{Status: http.StatusNotFound, Body: `{"success":false,"errors":[{"code":1003,"message":"tunnel not found"}],"result":null}`})
	before := len(f.writes())
	out, err := hostnameHandler{c: f.c}.Recover(context.Background(), env)
	if err != nil || out.State != core.RecoveryRolledBack {
		t.Fatalf("Recover = %+v, %v; want rolled back: a tunnel gone at Cloudflare took its rule with it", out, err)
	}
	if _, ok := f.row(host); ok {
		t.Error("the hostname row is still stored after the rollback")
	}
	if n := len(f.writes()) - before; n != 0 {
		t.Errorf("Recover sent %d writes, want none", n)
	}
	if got := f.tunnel().ConfigHash; got != "" {
		t.Errorf("stored hash = %q, want it left empty", got)
	}
}

func TestHARecover(t *testing.T) {
	const host, changeID = "rec.example.com", "01HRECOVER"
	cases := []struct {
		name                        string
		phase                       string // "" stores no row
		rule, record                bool   // present at the provider
		ingressEffect, recordEffect string // "", made or skipped
		want                        string
		wantRow, wantRule, wantRec  bool
		wantWrites                  int
	}{
		{"no row", "", false, false, "", "", core.RecoveryFailed, false, false, false, 0},
		{"rule write refused", "adding_rule", false, false, "skipped", "", core.RecoveryFailed, false, false, false, 0},
		{"rule made, record never tried", "adding_record", true, false, "made", "", core.RecoveryRolledBack, false, false, false, 1},
		{"record refused", "adding_record", true, false, "made", "skipped", core.RecoveryRolledBack, false, false, false, 1},
		{"both made, row behind", "adding_record", true, true, "made", "made", core.RecoveryCompleted, true, true, true, 0},
		{"already active", "active", true, true, "made", "made", core.RecoveryCompleted, true, true, true, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newHAFixture(t)
			ctx := context.Background()
			if c.phase != "" {
				row := hostnameRow{Hostname: host, TunnelID: f.tunnelID, Service: haService, ZoneID: f.zoneID, Phase: c.phase, ChangeID: changeID,
					Health: "unknown", UpdatedAt: f.h.clock.Now()}
				if err := f.h.db.Write(ctx, func(tx *sql.Tx) error { return insertHostname(ctx, tx, row) }); err != nil {
					t.Fatal(err)
				}
			}
			if c.rule {
				f.putRule(host, haService)
			}
			var rec DNSRecord
			if c.record {
				var err error
				rec, err = f.api.CreateDNSRecord(ctx, f.zoneID, DNSRecord{Type: "CNAME", Name: host, Content: f.tunnelID + ".cfargotunnel.com", Proxied: true, Comment: "darkpanel:" + changeID})
				if err != nil {
					t.Fatal(err)
				}
			}
			env := f.h.env(changeID)
			if c.ingressEffect != "" {
				e := core.Effect{Kind: core.EffectModify, Resource: "cloudflare.ingress:" + f.tunnelID + "/" + host, CommitWait: cloud.CommitWait,
					Before: core.StateDoc{State: json.RawMessage(`{"rule":null}`)}}
				seq, err := env.Effects.Intend(ctx, e, nil)
				if err != nil {
					t.Fatal(err)
				}
				if c.ingressEffect == "skipped" {
					err = env.Effects.Skipped(ctx, seq)
				} else {
					rule, _ := ingress.Lookup(f.config(), host)
					b, _ := json.Marshal(rule)
					err = env.Effects.Made(ctx, seq, e.Resource, core.StateDoc{State: json.RawMessage(`{"rule":` + string(b) + `}`)})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			if c.recordEffect != "" {
				e := core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.dnsrecord:?darkpanel:" + changeID, CommitWait: cloud.CommitWait,
					Recovery: &core.RecoveryID{Kind: "marker", Value: "darkpanel:" + changeID}, Before: core.StateDoc{State: json.RawMessage(`null`)}}
				seq, err := env.Effects.Intend(ctx, e, nil)
				if err != nil {
					t.Fatal(err)
				}
				if c.recordEffect == "skipped" {
					err = env.Effects.Skipped(ctx, seq)
				} else {
					err = env.Effects.Made(ctx, seq, "cloudflare.dnsrecord:"+f.zoneID+"/"+rec.ID, core.StateDoc{State: json.RawMessage(`{"name":"` + host + `"}`)})
				}
				if err != nil {
					t.Fatal(err)
				}
			}
			before := len(f.writes())
			out, err := hostnameHandler{c: f.c}.Recover(ctx, env)
			if err != nil || out.State != c.want {
				t.Fatalf("Recover = %+v, %v; want state %s", out, err, c.want)
			}
			row, found := f.row(host)
			_, hasRule := ingress.Lookup(f.config(), host)
			hasRec := len(f.records(host)) > 0
			if found != c.wantRow || hasRule != c.wantRule || hasRec != c.wantRec {
				t.Errorf("row %t, rule %t, record %t; want %t, %t, %t", found, hasRule, hasRec, c.wantRow, c.wantRule, c.wantRec)
			}
			if n := len(f.writes()) - before; n != c.wantWrites {
				t.Errorf("Recover sent %d writes, want %d", n, c.wantWrites)
			}
			if c.want == core.RecoveryCompleted {
				var view HostnameView
				if err := json.Unmarshal(out.Output, &view); err != nil || view.Phase != "active" || view.RecordID != rec.ID || row.Phase != "active" || row.RecordID != rec.ID {
					t.Errorf("output %s (%v), row %+v; want the active hostname with record %s", out.Output, err, row, rec.ID)
				}
			}
		})
	}
}

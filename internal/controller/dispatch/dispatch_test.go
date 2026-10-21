package dispatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var alice = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeAdmin, Source: "192.0.2.7"}

// config is the dispatcher's configuration over the harness, with a real
// controller engine journaling to journal.
func (h *harness) config(journal core.Journal) Config {
	h.t.Helper()
	log := slog.New(slog.DiscardHandler)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: h.reg, Validator: schema.NewValidator(), Journal: journal,
		Redact: schema.Redact,
		Base:   core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: log, Agents: h.hosts},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(eng.Close)
	return Config{Registry: h.reg, Engine: eng, Hosts: h.hosts, DB: h.db, Clock: h.clock, Log: log}
}

func newDispatcher(t *testing.T) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarness(t)
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

func TestNewRequiresItsConfig(t *testing.T) {
	h := newHarness(t)
	breaks := map[string]func(*Config){
		"Registry": func(c *Config) { c.Registry = nil },
		"Engine":   func(c *Config) { c.Engine = nil },
		"Hosts":    func(c *Config) { c.Hosts = nil },
		"DB":       func(c *Config) { c.DB = nil },
		"Clock":    func(c *Config) { c.Clock = nil },
		"Log":      func(c *Config) { c.Log = nil },
	}
	for name, brk := range breaks {
		c := h.config(&coretest.MemAudit{})
		brk(&c)
		if _, err := New(c); err == nil || !strings.Contains(err.Error(), "Config."+name) {
			t.Errorf("%s: New = %v, want an error naming Config.%s", name, err, name)
		}
	}
}

func TestReadRoutesBySide(t *testing.T) {
	_, d := newDispatcher(t)
	ctx := context.Background()
	in := json.RawMessage(`{"name":"a"}`)
	out, err := d.Read(ctx, alice, testHost, refGet, in)
	if err != nil || string(out) != `{"actor":"user:alice","value":""}` {
		t.Errorf("agent read = %s, %v", out, err)
	}
	if _, err := d.Read(ctx, alice, "", refCtlGet, in); err != nil {
		t.Errorf("controller read: %v", err)
	}
	cases := []struct {
		name, host string
		ref        core.ActionRef
		code       errcode.Code
	}{
		{"controller action with a host", testHost, refCtlGet, errcode.ActionUnknown},
		{"agent action without a host", "", refGet, errcode.ActionUnknown},
		{"unknown action", testHost, core.ActionRef{Name: "kv.item.nope", Version: 1}, errcode.ActionUnknown},
		{"unknown version", testHost, core.ActionRef{Name: refGet.Name, Version: 2}, errcode.ActionUnknown},
		{"unknown host", "h9", refGet, errcode.NotFound},
		{"failed migration", testHost, core.ActionRef{Name: "bad.item.get", Version: 1}, errcode.ModuleUnavailable},
		{"disabled", testHost, core.ActionRef{Name: "off.item.get", Version: 1}, errcode.ModuleUnavailable},
		{"failed preflight", testHost, core.ActionRef{Name: "flaky.item.get", Version: 1}, errcode.ModuleUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Read(ctx, alice, c.host, c.ref, in)
			_ = wantCode(t, err, c.code)
		})
	}
}

func TestResource(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	out, err := d.Resource(ctx, alice, testHost, "kv.status")
	if err != nil || string(out) != `{"count":0,"token":"[redacted]"}` {
		t.Errorf("kv.status = %s, %v", out, err)
	}
	_, err = d.Resource(ctx, alice, testHost, "kv.nope")
	_ = wantCode(t, err, errcode.NotFound)
	_, err = d.Resource(ctx, alice, "h9", "kv.status")
	_ = wantCode(t, err, errcode.NotFound)
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err = d.Resource(ctx, alice, testHost, "kv.status")
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module kv is unavailable on host h1: disabled" {
		t.Errorf("detail = %q", e.Detail)
	}
}

func TestPlanRemembersWhereAndWhat(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentPlan, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	ctlPlan, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"x"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if agentPlan.HostID != testHost || ctlPlan.HostID != "" {
		t.Errorf("plan hosts %q and %q, want h1 and none", agentPlan.HostID, ctlPlan.HostID)
	}
	type row struct {
		host, action string
		version      int
		expires      string
		applied      int
	}
	want := map[string]row{
		agentPlan.PlanID: {testHost, refSet.Name, 1, agentPlan.ExpiresAt.UTC().Format(timeFormat), 0},
		ctlPlan.PlanID:   {"", refCtlSet.Name, 1, ctlPlan.ExpiresAt.UTC().Format(timeFormat), 0},
	}
	for id, w := range want {
		var got row
		if err := h.db.Read().QueryRow(
			"SELECT host_id, action, version, expires_at, applied FROM core_plan_index WHERE plan_id = ?", id).
			Scan(&got.host, &got.action, &got.version, &got.expires, &got.applied); err != nil || got != w {
			t.Errorf("index of %s = %+v, %v; want %+v", id, got, err, w)
		}
	}
	// An unapplied plan outlives its expiry by PlanIndexRetention, then the
	// next plan deletes it; an applied one is kept.
	if _, err := h.db.Writer().Exec("UPDATE core_plan_index SET applied = 1 WHERE plan_id = ?", ctlPlan.PlanID); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(lifecycle.PlanTTL + PlanIndexRetention - time.Second)
	if _, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"y"}`), core.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := h.count("SELECT count(*) FROM core_plan_index"); n != 3 {
		t.Errorf("index holds %d plans, want 3 inside the retention", n)
	}
	h.clock.Advance(time.Second)
	if _, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"z"}`), core.PlanOptions{}); err != nil {
		t.Fatal(err)
	}
	if n := h.count("SELECT count(*) FROM core_plan_index WHERE plan_id = ?", agentPlan.PlanID); n != 0 {
		t.Error("the unapplied plan past its retention is still indexed")
	}
	if n := h.count("SELECT count(*) FROM core_plan_index"); n != 3 {
		t.Errorf("index holds %d plans, want the applied plan and the two new ones", n)
	}
}

func TestPlanRefusesAnUnusableModule(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	if _, err := h.hosts.Agent(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET available = 0, reason = 'kv: broken' WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if n := h.count("SELECT count(*) FROM core_plan_index"); e.Detail != "module kv is unavailable on host h1: kv: broken" || n != 0 {
		t.Errorf("detail %q, %d plans indexed", e.Detail, n)
	}
}

func TestPlanChecksTheAgentsAnswer(t *testing.T) {
	h, d := newDispatcher(t)
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		PlanFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage, core.PlanOptions) (core.PlanView, error) {
			return core.PlanView{PlanID: "p1", HostID: testHost, Action: "kv.item.get", Version: 1}, nil
		},
	})
	_, err := d.Plan(context.Background(), alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if n := h.count("SELECT count(*) FROM core_plan_index"); err == nil ||
		!strings.Contains(err.Error(), "answered with a plan for kv.item.get/v1 on h1") || n != 0 {
		t.Errorf("Plan = %v (%d plans indexed), want the mismatch refused", err, n)
	}
}

func TestPlanForwardsItsOptionsToTheAgent(t *testing.T) {
	h, d := newDispatcher(t)
	var seen core.PlanOptions
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		PlanFunc: func(ctx context.Context, p core.Principal, a core.ActionRef, in json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
			seen = opts
			return h.rt.Plan(ctx, p, a, in, opts)
		},
	})
	call := &core.CallContext{ParentChangeID: "c-parent", ParentAction: "kv.stack.deploy", StepID: "set"}
	_, err := d.Plan(context.Background(), alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{Call: call})
	if err != nil {
		t.Fatal(err)
	}
	if seen.Call != call {
		t.Errorf("the agent saw options %+v, want the call %+v", seen, call)
	}
}

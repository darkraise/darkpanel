package dispatch

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/schemas"
)

var refUpdateStatus = core.ActionRef{Name: "core.update.status", Version: 1}

// newWindowHarness is a dispatcher of a controller at version whose registry
// also has an agent-side core.update.status, as the assembled controller's
// does.
func newWindowHarness(t *testing.T, version string) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarness(t)
	status := core.DefineRead(core.ActionSpec{Name: refUpdateStatus.Name, Version: 1, Side: core.SideAgent}, echo{})
	var err error
	if h.reg, err = core.NewRegistry(append(modules(), coreactions.Module(coreactions.Deps{Extra: []core.ActionDef{status}}))...); err != nil {
		t.Fatal(err)
	}
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: h.hosts.cfg.Log})
	cfg := h.config(&coretest.MemAudit{})
	cfg.Version = version
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

// agentAt gives h1 a new session whose agent is the runtime reporting
// version and protocol; it answers core.update.status itself, which the
// fixture runtime does not have, and reports that action's schema hash.
func (h *harness) agentAt(version string, protocol int) {
	h.t.Helper()
	def, _ := h.reg.Action(refUpdateStatus)
	hash, err := schemas.ActionHash(def)
	if err != nil {
		h.t.Fatal(err)
	}
	h.clients.Add(testHost, &coretest.FakeAgent{
		HelloFunc: func(ctx context.Context) (core.AgentHello, error) {
			hello, err := h.rt.Hello(ctx)
			hello.BinaryVersion, hello.ProtocolVersion = version, protocol
			hello.SchemaHashes = maps.Clone(hello.SchemaHashes)
			hello.SchemaHashes[schemas.HashKey(refUpdateStatus)] = hash
			return hello, err
		},
		ReadFunc: func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			if a == refUpdateStatus {
				return json.RawMessage(`{"transactions":[]}`), nil
			}
			return h.rt.Read(ctx, p, a, input)
		},
		ResourceFunc: h.rt.Resource,
		PlanFunc:     h.rt.Plan,
		ApplyFunc:    h.rt.Apply,
	})
}

func TestVersionWindow(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		controller, agent string
		protocol          int
		supported         bool
	}{
		{"1.3.0", "1.3.0", 1, true},
		{"1.3.0", "1.3.4", 1, true},
		{"1.3.0", "1.2.9", 1, true},
		{"1.3.0", "1.1.0", 1, false},
		{"1.3.0", "1.4.0", 1, false},
		{"1.3.0", "2.3.0", 1, false},
		{"1.3.0", "1.3.0", 2, false},
		{"1.3.0", "dev", 1, false},
		{"dev", "dev", 1, true},
		{"", "0.1.0", 1, true},
	} {
		h, d := newWindowHarness(t, tc.controller)
		h.agentAt(tc.agent, tc.protocol)
		name := tc.agent + " under " + tc.controller
		calls := map[string]func() error{
			"read": func() error {
				_, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`))
				return err
			},
			"resource": func() error { _, err := d.Resource(ctx, alice, testHost, "kv.status"); return err },
			"plan": func() error {
				_, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
				return err
			},
		}
		for call, run := range calls {
			err := run()
			switch {
			case tc.supported && err != nil:
				t.Errorf("%s: %s = %v, want it dispatched", name, call, err)
			case !tc.supported:
				e := wantCode(t, err, errcode.HostVersionUnsupported)
				if !strings.Contains(e.Detail, "host h1 runs agent "+tc.agent) {
					t.Errorf("%s: %s detail %q does not name the agent's version", name, call, e.Detail)
				}
			}
		}
		if out, err := d.Read(ctx, alice, testHost, refUpdateStatus, json.RawMessage(`{"name":"x"}`)); err != nil ||
			string(out) != `{"transactions":[]}` {
			t.Errorf("%s: core.update.status = %s, %v; want it dispatched in every case", name, out, err)
		}
	}
}

// TestApplyChecksTheWindowAgain: an agent replaced by one outside the window
// after the plan cannot apply it.
func TestApplyChecksTheWindowAgain(t *testing.T) {
	ctx := context.Background()
	h, d := newWindowHarness(t, "1.3.0")
	h.agentAt("1.3.0", 1)
	pv := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	h.agentAt("1.4.0", 1)
	_, err := d.Apply(ctx, alice, testHost, refSet, pv.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.HostVersionUnsupported)
	h.agentAt("1.2.0", 1)
	if v, err := d.Apply(ctx, alice, testHost, refSet, pv.PlanID, core.ApplyOptions{}); err != nil || v.State != core.StatePending {
		t.Errorf("Apply from a previous-minor agent = %+v, %v; want the pending change", v, err)
	}
}

// windowOps gives h1 a new session whose agent reports version, answers its
// change operations for view as opsAgent does and confirms view.
func (h *harness) windowOps(version string, view core.ChangeView) *opsAgent {
	h.t.Helper()
	h.agentAt(version, 1)
	c, _, err := h.clients.Client(context.Background(), testHost)
	if err != nil {
		h.t.Fatal(err)
	}
	agent := &opsAgent{AgentClient: c, view: view}
	c.(*coretest.FakeAgent).ConfirmFunc = func(_ context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
		agent.record("confirm %s by %s", changeID, p.Actor)
		return view, nil
	}
	h.clients.Add(testHost, agent)
	return agent
}

func TestChangeOpsCheckTheWindow(t *testing.T) {
	ctx := context.Background()
	h, d := newWindowHarness(t, "1.3.0")
	h.agentAt("1.3.0", 1)
	kv := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})
	update := core.ChangeView{ID: "01UPDATECHANGE", HostID: testHost, Action: "core.update.apply", Version: 1}
	if _, err := h.db.Writer().Exec(`INSERT INTO core_change_index (change_id, host_id, action, version, created_at)
		VALUES (?, ?, ?, 1, '2026-10-17T12:00:00.000000000Z')`, update.ID, testHost, update.Action); err != nil {
		t.Fatal(err)
	}
	ops := []struct {
		name string
		run  func(id string) error
	}{
		{"undo", func(id string) error { _, err := d.Undo(ctx, alice, id); return err }},
		{"repair", func(id string) error { _, err := d.Repair(ctx, alice, id, core.RepairRequest{}); return err }},
		{"repair with input", func(id string) error {
			_, err := d.Repair(ctx, alice, id, core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)})
			return err
		}},
		{"resolve", func(id string) error {
			_, err := d.Resolve(ctx, alice, id, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
			return err
		}},
	}
	for _, tc := range []struct {
		name, agent string
		view        core.ChangeView
		refused     bool
	}{
		{"a module change inside the window", "1.3.0", kv, false},
		{"a module change outside the window", "1.4.0", kv, true},
		{"a core.update change outside the window", "1.4.0", update, false},
	} {
		for _, op := range ops {
			agent := h.windowOps(tc.agent, tc.view)
			err := op.run(tc.view.ID)
			calls := agent.recorded()
			if !tc.refused {
				if err != nil || len(calls) != 1 {
					t.Errorf("%s: %s = %v, agent calls %q; want it dispatched", tc.name, op.name, err, calls)
				}
				continue
			}
			e := wantCode(t, err, errcode.HostVersionUnsupported)
			if !strings.Contains(e.Detail, "host h1 runs agent 1.4.0") {
				t.Errorf("%s: %s detail %q does not name the agent's version", tc.name, op.name, e.Detail)
			}
			if len(calls) != 0 {
				t.Errorf("%s: %s reached the agent: %q", tc.name, op.name, calls)
			}
		}
	}
}

// TestChangeOpsSkipEnablement: undo and resolve of a change of a disabled
// module still reach the agent, and release and confirm still reach an agent
// outside the support window.
func TestChangeOpsSkipEnablement(t *testing.T) {
	ctx := context.Background()
	h, d := newWindowHarness(t, "1.3.0")
	h.agentAt("1.3.0", 1)
	kv := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})

	agent := h.windowOps("1.3.0", kv)
	if _, err := h.db.Writer().Exec(
		"UPDATE core_module_state SET enabled = 0 WHERE host_id = ? AND module_id = 'kv'", testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Undo(ctx, alice, kv.ID); err != nil {
		t.Errorf("Undo of a disabled module's change = %v, want it dispatched", err)
	}
	if _, err := d.Resolve(ctx, alice, kv.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept}); err != nil {
		t.Errorf("Resolve of a disabled module's change = %v, want it dispatched", err)
	}
	if got := agent.recorded(); len(got) != 2 {
		t.Errorf("agent calls %q, want the undo and the resolve", got)
	}

	agent = h.windowOps("1.4.0", kv)
	if _, err := d.Release(ctx, alice, kv.ID, "host rebuilt"); err != nil {
		t.Errorf("Release outside the window = %v, want it dispatched", err)
	}
	if _, err := d.Confirm(ctx, alice, kv.ID); err != nil {
		t.Errorf("Confirm outside the window = %v, want it dispatched", err)
	}
	want := []string{"release " + kv.ID + " by user:alice: host rebuilt, call <nil>", "confirm " + kv.ID + " by user:alice"}
	if got := agent.recorded(); !slices.Equal(got, want) {
		t.Errorf("agent calls\n got %q\nwant %q", got, want)
	}
}

// TestChangeActionWithoutAnIndexRow: a change missing from core_change_index
// takes its action from the agent's view, which must be of that change.
func TestChangeActionWithoutAnIndexRow(t *testing.T) {
	ctx := context.Background()
	h, d := newWindowHarness(t, "1.3.0")
	h.agentAt("1.3.0", 1)
	kv := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})
	if _, err := h.db.Writer().Exec("DELETE FROM core_change_index WHERE change_id = ?", kv.ID); err != nil {
		t.Fatal(err)
	}
	other := kv
	other.ID = "01OTHER"
	for _, tc := range []struct {
		name   string
		answer core.ChangeView
		want   string // "" expects an error
	}{
		{"the change", kv, refSet.Name},
		{"another change", other, ""},
	} {
		agent := &coretest.FakeAgent{ChangeFunc: func(context.Context, string) (core.ChangeView, error) { return tc.answer, nil }}
		action, err := d.changeAction(ctx, testHost, kv.ID, agent)
		switch {
		case tc.want == "" && err == nil:
			t.Errorf("%s: changeAction = %q, want an error", tc.name, action)
		case tc.want != "" && (err != nil || action != tc.want):
			t.Errorf("%s: changeAction = %q, %v; want %q", tc.name, action, err, tc.want)
		}
	}
}

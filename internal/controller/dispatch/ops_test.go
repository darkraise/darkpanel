package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// opsAgent is h1's agent with its change operations recorded and answered
// for view; Repair answers plan when it is set, else view undoing.
type opsAgent struct {
	core.AgentClient
	view core.ChangeView
	plan *core.PlanView

	mu    sync.Mutex
	calls []string
}

func (a *opsAgent) record(format string, args ...any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.calls = append(a.calls, fmt.Sprintf(format, args...))
}

func (a *opsAgent) recorded() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	return slices.Clone(a.calls)
}

func (a *opsAgent) Undo(_ context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error) {
	a.record("undo %s by %s, call %v", changeID, p.Actor, call)
	v := a.view
	v.State = core.StateUndoing
	return v, nil
}

func (a *opsAgent) Repair(_ context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	a.record("repair %s by %s, input %s, step %q as %q", changeID, p.Actor, string(req.Input), req.Step, req.As)
	if a.plan != nil {
		pl := *a.plan
		return core.RepairResult{Plan: &pl}, nil
	}
	v := a.view
	v.State = core.StateUndoing
	return core.RepairResult{Change: &v}, nil
}

func (a *opsAgent) Release(_ context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	a.record("release %s by %s: %s, call %v", changeID, p.Actor, reason, call)
	v := a.view
	v.State = core.StateAbandoned
	return core.ReleaseResult{Change: v, Abandoned: []core.EffectView{{ChangeID: changeID, Seq: 1, State: core.EffectAbandoned}}}, nil
}

func (a *opsAgent) Effects(_ context.Context, _ core.Principal, changeID string) ([]core.EffectView, error) {
	a.record("effects %s", changeID)
	return []core.EffectView{{ChangeID: changeID, Seq: 1, State: core.EffectMade}}, nil
}

func (a *opsAgent) Observe(_ context.Context, _ core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	a.record("observe %s %d", changeID, seq)
	return core.ObserveResult{Effect: core.EffectView{ChangeID: changeID, Seq: seq}, Allowed: []string{core.ResolutionAccept}}, nil
}

func (a *opsAgent) Resolve(_ context.Context, _ core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	a.record("resolve %s %d %s", changeID, seq, req.Resolution)
	return core.EffectView{ChangeID: changeID, Seq: seq, State: core.EffectAccepted}, nil
}

// opsComposite writes a controller-side composite id in state, whose step
// db's child change is childID.
func opsComposite(t *testing.T, h *harness, id, childID, state string) {
	t.Helper()
	const at = "2026-10-17T12:00:00.000000000Z"
	for _, s := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
			confirmation, created_at, expires_at) VALUES (?, '', 'cfx.deploy.add', 1, 'user:alice', '', '', '[]', '[]', '', ?, ?)`,
			[]any{"P" + id, at, at}},
		{`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
			created_at, updated_at) VALUES (?, ?, '', 'cfx.deploy.add', 1, 'composite', '', 'user:alice', ?, '{}', ?, ?)`,
			[]any{id, "P" + id, state, at, at}},
		{`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, input_template, state,
			child_change_id, updated_at) VALUES (?, 'db', 0, ?, 'kv.item.set/v1', '{}', 'applied', ?, ?)`,
			[]any{id, testHost, childID, at}},
	} {
		if _, err := h.db.Writer().Exec(s.query, s.args...); err != nil {
			t.Fatal(err)
		}
	}
}

func TestChangeOpsRouteBySide(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentChange := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})
	ctlChange := h.applyCtl(d, "x")
	agent := &opsAgent{AgentClient: h.rt, view: agentChange}
	h.clients.Add(testHost, agent)
	calls := &ingestCalls{}
	d.cfg.Ingest = calls
	id := agentChange.ID

	if v, err := d.Undo(ctx, alice, id); err != nil || v.ID != id || v.State != core.StateUndoing {
		t.Errorf("Undo = %+v, %v; want the agent's undoing change", v, err)
	}
	if rel, err := d.Release(ctx, alice, id, "host rebuilt"); err != nil || rel.Change.State != core.StateAbandoned || len(rel.Abandoned) != 1 {
		t.Errorf("Release = %+v, %v", rel, err)
	}
	if es, err := d.Effects(ctx, alice, id); err != nil || len(es) != 1 || es[0].ChangeID != id {
		t.Errorf("Effects = %+v, %v", es, err)
	}
	if o, err := d.Observe(ctx, alice, id, 1); err != nil || o.Effect.Seq != 1 {
		t.Errorf("Observe = %+v, %v", o, err)
	}
	if e, err := d.Resolve(ctx, alice, id, 1, core.ResolveRequest{Resolution: core.ResolutionAccept}); err != nil ||
		e.State != core.EffectAccepted {
		t.Errorf("Resolve = %+v, %v", e, err)
	}
	if steps, err := d.Steps(ctx, alice, id); err != nil || steps == nil || len(steps) != 0 {
		t.Errorf("Steps of an agent change = %#v, %v; want []", steps, err)
	}
	want := []string{
		"undo " + id + " by user:alice, call <nil>",
		"release " + id + " by user:alice: host rebuilt, call <nil>",
		"effects " + id,
		"observe " + id + " 1",
		"resolve " + id + " 1 accept",
	}
	if got := agent.recorded(); !slices.Equal(got, want) {
		t.Errorf("agent calls\n got %q\nwant %q", got, want)
	}
	if want := []string{testHost, testHost, testHost}; !slices.Equal(calls.hosts, want) {
		t.Errorf("ingested %v, want h1 after undo, release and resolve", calls.hosts)
	}

	// The controller's own change never reaches an agent.
	if es, err := d.Effects(ctx, alice, ctlChange.ID); err != nil || len(es) != 0 {
		t.Errorf("Effects of a controller change = %+v, %v; want none", es, err)
	}
	if steps, err := d.Steps(ctx, alice, ctlChange.ID); err != nil || len(steps) != 0 {
		t.Errorf("Steps of a controller change = %+v, %v; want none", steps, err)
	}
	if _, err := d.Undo(ctx, alice, ctlChange.ID); err == nil {
		t.Error("Undo of ctl.note.set, which has no undo handler, succeeded")
	}
	if got := agent.recorded(); len(got) != len(want) {
		t.Errorf("a controller change reached the agent: %q", got[len(want):])
	}
	_, err := d.Effects(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func TestRepairRelaysAPlan(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	target := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})
	// A plan the agent made itself, as its repair route does, so the
	// dispatcher has not indexed it.
	pl, err := h.rt.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"b","value":"2"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	agent := &opsAgent{AgentClient: h.rt, view: target, plan: &pl}
	h.clients.Add(testHost, agent)

	res, err := d.Repair(ctx, alice, target.ID, core.RepairRequest{Input: json.RawMessage(`{"outcome":"keep"}`)})
	if err != nil || res.Change != nil || res.Plan == nil || res.Plan.PlanID != pl.PlanID {
		t.Fatalf("Repair = %+v, %v; want the agent's plan", res, err)
	}
	var host, action, repairs string
	var version int
	if err := h.db.Read().QueryRow("SELECT host_id, action, version, repairs FROM core_plan_index WHERE plan_id = ?", pl.PlanID).
		Scan(&host, &action, &version, &repairs); err != nil {
		t.Fatalf("plan index row: %v", err)
	}
	if host != testHost || action != refSet.Name || version != 1 || repairs != target.ID {
		t.Errorf("plan index row = %s %s %d repairs %s", host, action, version, repairs)
	}
	if v, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{}); err != nil || v.State != core.StatePending {
		t.Errorf("Apply of the relayed plan = %+v, %v; want pending", v, err)
	}

	agent.plan = nil
	res, err = d.Repair(ctx, alice, target.ID, core.RepairRequest{})
	if err != nil || res.Plan != nil || res.Change == nil || res.Change.State != core.StateUndoing {
		t.Errorf("Repair = %+v, %v; want the agent's undoing change", res, err)
	}
	want := []string{
		`repair ` + target.ID + ` by user:alice, input {"outcome":"keep"}, step "" as ""`,
		`repair ` + target.ID + ` by user:alice, input , step "" as ""`,
	}
	if got := agent.recorded(); !slices.Equal(got, want) {
		t.Errorf("agent calls\n got %q\nwant %q", got, want)
	}
}

func TestReleasingCompositeRefusesChildRepairs(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	child := h.applyAgent(d, "a", core.ApplyOptions{ConfirmNow: true})
	agent := &opsAgent{AgentClient: h.rt, view: child}
	h.clients.Add(testHost, agent)
	opsComposite(t, h, "01COMPOSITE", child.ID, core.StateReleasing)

	_, err := d.Repair(ctx, alice, child.ID, core.RepairRequest{})
	if e := wantCode(t, err, errcode.ChangeBusy); !strings.Contains(e.Detail, "01COMPOSITE") {
		t.Errorf("detail %q does not name the composite", e.Detail)
	}
	// A repair plan of the child indexed before the release began.
	if _, err := h.db.Writer().Exec(`INSERT INTO core_plan_index (plan_id, host_id, action, version, expires_at, repairs)
		VALUES ('01REPAIRPLAN', ?, ?, 1, '2026-10-17T12:10:00.000000000Z', ?)`, testHost, refSet.Name, child.ID); err != nil {
		t.Fatal(err)
	}
	_, err = d.Apply(ctx, alice, testHost, refSet, "01REPAIRPLAN", core.ApplyOptions{})
	_ = wantCode(t, err, errcode.ChangeBusy)
	if n := h.count("SELECT applied FROM core_plan_index WHERE plan_id = '01REPAIRPLAN'"); n != 0 {
		t.Errorf("a refused repair plan was marked applied")
	}
	if got := agent.recorded(); len(got) != 0 {
		t.Errorf("a refused repair reached the agent: %q", got)
	}

	if _, err := h.db.Writer().Exec("UPDATE core_changes SET state = ? WHERE id = '01COMPOSITE'", core.StateAbandoned); err != nil {
		t.Fatal(err)
	}
	if res, err := d.Repair(ctx, alice, child.ID, core.RepairRequest{}); err != nil || res.Change == nil {
		t.Errorf("Repair once the composite is abandoned = %+v, %v; want the agent's answer", res, err)
	}
}

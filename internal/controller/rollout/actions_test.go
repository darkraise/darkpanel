package rollout

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// admin is the operator who submits and cancels rollouts.
var admin = core.Principal{Actor: "user:admin", UserID: "admin", Scope: core.ScopeAdmin}

// actionModule is a core module holding only the given actions.
type actionModule struct{ defs []core.ActionDef }

func (m actionModule) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: m.defs, DefaultEnabled: true}
}

// newEngine returns a controller engine over db serving the rollout actions
// of a controller running version.
func newEngine(t *testing.T, db *store.DB, clock core.Clock, version string) *lifecycle.Engine {
	t.Helper()
	reg, err := core.NewRegistry(actionModule{Actions(ActionsConfig{Version: version})})
	if err != nil {
		t.Fatal(err)
	}
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: &coretest.MemJournal{},
		Redact: schema.Redact,
		Base:   core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.DiscardHandler)},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	return eng
}

// planApply plans name/v1 with input and applies the plan.
func planApply(eng *lifecycle.Engine, name, input, confirmation string) (core.ChangeView, error) {
	ctx := context.Background()
	plan, err := eng.Plan(ctx, admin, core.ActionRef{Name: name, Version: 1}, json.RawMessage(input))
	if err != nil {
		return core.ChangeView{}, err
	}
	return eng.Apply(ctx, admin, plan.PlanID, core.ApplyOptions{Confirmation: confirmation})
}

// outputOf decodes a change's output.
func outputOf[T any](t *testing.T, v core.ChangeView) T {
	t.Helper()
	var out T
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// mustLoad reads rollout id.
func mustLoad(t *testing.T, db *store.DB, id string) record {
	t.Helper()
	r, err := load(context.Background(), db.Read(), id)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

// hostStates lists a rollout's host states in position order as
// "<host>=<state>".
func hostStates(r record) []string {
	var out []string
	for _, h := range r.Hosts {
		out = append(out, h.HostID+"="+h.State)
	}
	return out
}

func TestRolloutActionsRegister(t *testing.T) {
	defs := Actions(ActionsConfig{Version: "1.2.0"})
	reg, err := core.NewRegistry(actionModule{defs})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]struct {
		class         core.Class
		compensatedBy string
	}{
		ActionRollout:       {core.ClassCompensable, ActionRolloutCancel},
		ActionRolloutCancel: {core.ClassIrreversible, ""},
		ActionRollouts:      {core.ClassRead, ""},
	}
	if len(reg.Actions()) != len(want) {
		t.Fatalf("registered %d actions, want %d", len(reg.Actions()), len(want))
	}
	for _, def := range reg.Actions() {
		w, ok := want[def.Spec.Name]
		switch {
		case !ok:
			t.Errorf("unexpected action %s", def.Ref())
		case def.Spec.Version != 1 || def.Spec.Side != core.SideController || def.Spec.Class != w.class ||
			def.Spec.CompensatedBy != w.compensatedBy || len(def.Spec.LockKeys) != 0 || def.Spec.BreakGlass:
			t.Errorf("%s spec = %+v", def.Ref(), def.Spec)
		}
		if _, err := schema.ActionSnapshot(def); err != nil {
			t.Errorf("%s snapshot: %v", def.Ref(), err)
		}
	}
	for _, def := range defs {
		var err error
		if def.Spec.Class == core.ClassRead {
			_, err = def.Handler.Read(context.Background(), core.Env{}, json.RawMessage(`{}`))
		} else {
			_, err = def.Handler.Plan(context.Background(), core.Env{}, json.RawMessage(`{}`))
		}
		if err == nil || errcode.From(err).Code != errcode.Internal {
			t.Errorf("%s without a database: error = %v, want an internal error", def.Ref(), err)
		}
	}
}

func TestRolloutAction(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "A", false, "1.2.0")
	addHost(t, db, "OWN", true, "1.2.0")
	clock := coretest.NewFakeClock(testStart)
	eng := newEngine(t, db, clock, "1.2.0")
	plan, err := eng.Plan(context.Background(), admin, core.ActionRef{Name: ActionRollout, Version: 1},
		json.RawMessage(`{"hosts":["A","OWN"],"version":"1.3.0","channel":"stable"}`))
	if err != nil {
		t.Fatal(err)
	}
	var diff strings.Builder
	for _, l := range plan.Diff {
		diff.WriteString(l.Op + " " + l.Text + "\n")
	}
	if !strings.Contains(diff.String(), "+ darkpanel 1.3.0 (stable) on 2 hosts, one at a time: OWN, A") {
		t.Errorf("diff = %q", diff.String())
	}
	v, err := eng.Apply(context.Background(), admin, plan.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateCompleted {
		t.Fatalf("change = %+v, want completed", v)
	}
	r := outputOf[core.Rollout](t, v)
	if r.State != core.RolloutRunning || r.Version != "1.3.0" || !slices.Equal(hostOrder(r), []string{"OWN", "A"}) {
		t.Errorf("rollout = %+v", r)
	}
	if rec := mustLoad(t, db, r.ID); rec.ChangeID != v.ID {
		t.Errorf("change id = %q, want %q", rec.ChangeID, v.ID)
	}
	if v.CompensatedBy != ActionRolloutCancel {
		t.Errorf("compensated by %q", v.CompensatedBy)
	}
	def := actionDef(t, Actions(ActionsConfig{Version: "1.2.0"}), ActionRollout)
	env := core.Env{DB: db, Clock: clock, ChangeID: v.ID}
	in := json.RawMessage(`{"hosts":["A","OWN"],"version":"1.3.0","channel":"stable"}`)
	if err := def.Handler.Verify(context.Background(), env, in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("recovery Verify = %v, want the rollout found by its change", err)
	}
	env.ChangeID = "OTHER"
	if err := def.Handler.Verify(context.Background(), env, in, json.RawMessage(`{}`)); err == nil {
		t.Error("recovery Verify found a rollout for another change")
	}
}

// actionDef returns the action of defs named name.
func actionDef(t *testing.T, defs []core.ActionDef, name string) core.ActionDef {
	t.Helper()
	i := slices.IndexFunc(defs, func(d core.ActionDef) bool { return d.Spec.Name == name })
	if i < 0 {
		t.Fatalf("no action %s", name)
	}
	return defs[i]
}

func TestRolloutActionRefusals(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "A", false, "1.2.0")
	addHost(t, db, "OWN", true, "1.2.0")
	eng := newEngine(t, db, coretest.NewFakeClock(testStart), "1.2.0")
	_, err := planApply(eng, ActionRollout, `{"hosts":["A"],"version":"1.3.0","channel":"stable"}`, "")
	wantCode(t, err, errcode.InvalidInput)

	ctx := context.Background()
	ref := core.ActionRef{Name: ActionRollout, Version: 1}
	first, err := eng.Plan(ctx, admin, ref, json.RawMessage(`{"hosts":["OWN"],"version":"1.3.0","channel":"stable"}`))
	if err != nil {
		t.Fatal(err)
	}
	second, err := eng.Plan(ctx, admin, ref, json.RawMessage(`{"hosts":["A"],"version":"1.2.0","channel":"stable"}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := eng.Apply(ctx, admin, first.PlanID, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	_, err = eng.Apply(ctx, admin, second.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.RolloutInProgress)
	_, err = eng.Plan(ctx, admin, ref, json.RawMessage(`{"hosts":["A"],"version":"1.2.0","channel":"stable"}`))
	wantCode(t, err, errcode.RolloutInProgress)
}

func TestRolloutCancel(t *testing.T) {
	tests := []struct {
		name        string
		setup       []string // statements run on rollout ? with hosts OWN, A, B
		force       bool
		state       string
		outcome     string
		hosts       []string
		updatingErr string // the error code of host OWN, if any
	}{
		{
			name:  "before any host started",
			state: core.RolloutCancelled, outcome: core.RolloutCancelled,
			hosts: []string{"OWN=skipped", "A=skipped", "B=skipped"},
		},
		{
			name:  "a host updating without a plan",
			setup: []string{"UPDATE core_rollout_hosts SET state = 'updating' WHERE rollout_id = ? AND host_id = 'OWN'"},
			state: core.RolloutCancelled, outcome: core.RolloutCancelled,
			hosts: []string{"OWN=skipped", "A=skipped", "B=skipped"},
		},
		{
			name: "a host updating with a plan",
			setup: []string{
				"UPDATE core_rollout_hosts SET state = 'committed' WHERE rollout_id = ? AND host_id = 'OWN'",
				"UPDATE core_rollout_hosts SET state = 'updating', plan_id = 'P1' WHERE rollout_id = ? AND host_id = 'A'",
			},
			state: core.RolloutDraining, outcome: core.RolloutCancelled,
			hosts: []string{"OWN=committed", "A=updating", "B=skipped"},
		},
		{
			name: "with force while a host updates",
			setup: []string{"UPDATE core_rollout_hosts SET state = 'updating', plan_id = 'P1', operation_id = 'OP1' " +
				"WHERE rollout_id = ? AND host_id = 'OWN'"},
			force: true,
			state: core.RolloutCancelled, outcome: core.RolloutCancelled,
			hosts:       []string{"OWN=failed", "A=skipped", "B=skipped"},
			updatingErr: string(errcode.UpdateInterrupted),
		},
		{
			name: "a draining rollout",
			setup: []string{
				"UPDATE core_rollout_hosts SET state = 'updating', plan_id = 'P1' WHERE rollout_id = ? AND host_id = 'OWN'",
				"UPDATE core_rollout_hosts SET state = 'skipped' WHERE rollout_id = ? AND host_id <> 'OWN'",
				"UPDATE core_rollouts SET state = 'draining', outcome = 'failed' WHERE id = ?",
			},
			state: core.RolloutDraining, outcome: core.RolloutCancelled,
			hosts: []string{"OWN=updating", "A=skipped", "B=skipped"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := openDB(t)
			addHost(t, db, "OWN", true, "1.2.0")
			addHost(t, db, "A", false, "1.2.0")
			addHost(t, db, "B", false, "1.2.0")
			r := mustCreate(t, db, "1.3.0", "1.2.0", "A", "B", "OWN")
			for _, s := range tt.setup {
				exec(t, db, s, r.ID)
			}
			eng := newEngine(t, db, coretest.NewFakeClock(testStart), "1.2.0")
			input := `{"rolloutId":"` + r.ID + `","force":` + map[bool]string{true: "true", false: "false"}[tt.force] + `}`
			_, err := planApply(eng, ActionRolloutCancel, input, "")
			wantCode(t, err, errcode.PlanConfirmationNeeded)
			v, err := planApply(eng, ActionRolloutCancel, input, r.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != core.StateCompleted {
				t.Fatalf("change = %+v, want completed", v)
			}
			if out := outputOf[core.Rollout](t, v); out.State != tt.state {
				t.Errorf("output state = %s, want %s", out.State, tt.state)
			}
			rec := mustLoad(t, db, r.ID)
			if rec.State != tt.state || rec.Outcome != tt.outcome || rec.Error != nil {
				t.Errorf("rollout = %s outcome %q error %v, want %s outcome %q", rec.State, rec.Outcome, rec.Error, tt.state, tt.outcome)
			}
			if got := hostStates(rec); !slices.Equal(got, tt.hosts) {
				t.Errorf("hosts = %v, want %v", got, tt.hosts)
			}
			var code string
			if e := rec.Hosts[0].Error; e != nil {
				code = e.Code
			}
			if code != tt.updatingErr {
				t.Errorf("host OWN error code = %q, want %q", code, tt.updatingErr)
			}
		})
	}
}

func TestRolloutCancelRefusals(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "OWN", true, "1.2.0")
	r := mustCreate(t, db, "1.3.0", "1.2.0", "OWN")
	exec(t, db, "UPDATE core_rollouts SET state = 'succeeded' WHERE id = ?", r.ID)
	eng := newEngine(t, db, coretest.NewFakeClock(testStart), "1.2.0")
	_, err := planApply(eng, ActionRolloutCancel, `{"rolloutId":"`+r.ID+`","force":false}`, r.ID)
	wantCode(t, err, errcode.InvalidInput)
	_, err = planApply(eng, ActionRolloutCancel, `{"rolloutId":"MISSING","force":true}`, "MISSING")
	wantCode(t, err, errcode.NotFound)
}

func TestRolloutsAction(t *testing.T) {
	db := openDB(t)
	addHost(t, db, "OWN", true, "1.2.0")
	clock := coretest.NewFakeClock(testStart)
	eng := newEngine(t, db, clock, "1.2.0")
	var ids []string
	for _, version := range []string{"1.2.1", "1.2.2", "1.2.3"} {
		v, err := planApply(eng, ActionRollout, `{"hosts":["OWN"],"version":"`+version+`","channel":"stable"}`, "")
		if err != nil {
			t.Fatal(err)
		}
		id := outputOf[core.Rollout](t, v).ID
		ids = append(ids, id)
		exec(t, db, "UPDATE core_rollouts SET state = 'succeeded' WHERE id = ?", id)
		clock.Advance(1)
	}
	read := func(input string) []string {
		t.Helper()
		out, err := eng.Read(context.Background(), admin, core.ActionRef{Name: ActionRollouts, Version: 1}, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		var l core.RolloutList
		if err := json.Unmarshal(out, &l); err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, r := range l.Rollouts {
			if len(r.Hosts) != 1 || r.Hosts[0].HostID != "OWN" {
				t.Errorf("rollout %s hosts = %+v", r.ID, r.Hosts)
			}
			got = append(got, r.ID)
		}
		return got
	}
	if got := read(`{}`); !slices.Equal(got, []string{ids[2], ids[1], ids[0]}) {
		t.Errorf("rollouts = %v, want newest first %v", got, ids)
	}
	if got := read(`{"limit":2}`); !slices.Equal(got, []string{ids[2], ids[1]}) {
		t.Errorf("rollouts with limit 2 = %v", got)
	}
	exec(t, db, "DELETE FROM core_rollout_hosts")
	exec(t, db, "DELETE FROM core_rollouts")
	out, err := eng.Read(context.Background(), admin, core.ActionRef{Name: ActionRollouts, Version: 1}, json.RawMessage(`{}`))
	if err != nil || string(out) != `{"rollouts":[]}` {
		t.Errorf("empty list = %s, %v", out, err)
	}
}

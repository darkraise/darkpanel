package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var cfxBob = core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeAdmin, Source: "test"}

// awaitAgent waits, without moving any clock, until the agent's change id
// is in state.
func (f *cfxFixture) awaitAgent(id, state string) {
	f.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for f.agentChange(id).State != state {
		if time.Now().After(deadline) {
			f.t.Fatalf("agent change %s is %s, want %s", id, f.agentChange(id).State, state)
		}
		time.Sleep(time.Millisecond)
	}
}

// conflicted runs cfx.deploy.add with step c failing after an outside change
// took key cst while c was being planned, so the unwind's undo of b is
// refused with core.undo_conflict. It returns the composite and the outside
// change.
func conflicted(t *testing.T, f *cfxFixture) (core.ChangeView, core.ChangeView) {
	t.Helper()
	var outside core.ChangeView
	var once sync.Once
	f.host.setHooks(func(name string) {
		if name != "web-c" {
			return
		}
		once.Do(func() {
			ctx := context.Background()
			pl, err := f.agent.Plan(ctx, cfxRoot, refCstAdd, json.RawMessage(`{"name":"other","value":"9"}`), core.PlanOptions{})
			if err != nil {
				t.Error(err)
				return
			}
			if outside, err = f.agent.Apply(ctx, cfxRoot, pl.PlanID, core.ApplyOptions{}); err != nil {
				t.Error(err)
			}
		})
	}, nil)
	f.host.failNext("web-c", 1)
	v := f.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	done := f.await(v.ID, core.StateNeedsRepair, core.StateReverted, core.StateFailed, core.StateCompleted)
	f.host.setHooks(nil, nil)
	if done.State != core.StateNeedsRepair || done.Error == nil || done.Error.Cause != string(errcode.UndoConflict) {
		t.Fatalf("composite = %s (%+v), want needs_repair caused by core.undo_conflict", done.State, done.Error)
	}
	if outside.State != core.StateCompleted {
		t.Fatalf("outside change = %+v, want completed", outside)
	}
	return v, outside
}

// stuckForward runs cfx.bad.drop with step a adding z1 and step b adding z2
// through a remote call that fails, so b's child needs repair with an
// unresolved effect and the composite needs repair at b.
func stuckForward(t *testing.T, f *cfxFixture) (core.ChangeView, stepRow) {
	t.Helper()
	f.bad.set(
		cstStep("a", refCstPut, `{"name":"z1","value":"1"}`),
		cstStep("b", refCstPut, `{"name":"z2","value":"2","remote":true}`, "a"),
	)
	f.host.failNext("z2", 1)
	v := f.applyC(refCfxDrop, `{"name":"z"}`, core.ApplyOptions{})
	if done := f.await(v.ID, core.StateNeedsRepair, core.StateCompleted, core.StateReverted); done.State != core.StateNeedsRepair {
		t.Fatalf("composite = %s (%+v), want needs_repair", done.State, done.Error)
	}
	b := f.stepsOf(v.ID)["b"]
	if child := f.agentChange(b.ChildID); child.State != core.StateNeedsRepair {
		t.Fatalf("child of b = %s, want needs_repair", child.State)
	}
	return v, b
}

// abandonChild releases step s's child as its parent's runner would.
func abandonChild(t *testing.T, f *cfxFixture, id string, s stepRow) {
	t.Helper()
	call := &core.CallContext{ParentChangeID: id, StepID: s.ID}
	if _, err := f.agent.Release(context.Background(), recoveryPrincipal, s.ChildID, "test", call); err != nil {
		t.Fatal(err)
	}
}

func repairC(f *cfxFixture, p core.Principal, id string, req core.RepairRequest) (core.RepairResult, error) {
	return f.ctrl.Repair(context.Background(), p, id, req)
}

func TestReverseCompositeRepairResumesTheUnwind(t *testing.T) {
	f := newCfxFixture(t)
	v, outside := conflicted(t, f)
	if _, err := f.agent.Undo(context.Background(), cfxRoot, outside.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.awaitAgent(outside.ID, core.StateReverted)
	r, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{})
	if err != nil || r.Change == nil || r.Change.State != core.StateUndoing || r.Plan != nil {
		t.Fatalf("Repair = %+v, %v; want the composite undoing", r, err)
	}
	done := f.await(v.ID, core.StateReverted, core.StateNeedsRepair)
	if done.State != core.StateReverted || done.Error == nil || done.Error.Code != string(errcode.ChildFailed) {
		t.Fatalf("composite = %s (%+v), want reverted with core.child_failed", done.State, done.Error)
	}
	for _, name := range []string{"web-a", "web-b"} {
		if _, ok := f.host.item(name); ok {
			t.Errorf("item %s is still there", name)
		}
	}
}

func TestReverseCompositeStepDecisions(t *testing.T) {
	f := newCfxFixture(t)
	v, _ := conflicted(t, f)
	r, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{Step: "b", As: "reverted"})
	if err != nil || r.Change == nil || r.Change.State != core.StateNeedsRepair {
		t.Fatalf("decide b = %+v, %v; want the composite still needs_repair", r, err)
	}
	if s := f.stepsOf(v.ID)["b"]; s.State != core.StepUndone {
		t.Fatalf("step b = %s, want undone", s.State)
	}
	if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{}); err != nil {
		t.Fatal(err)
	}
	f.await(v.ID, core.StateNeedsRepair)
	if s := f.stepsOf(v.ID)["a"]; s.Err == nil || s.Err.Code != string(errcode.UndoConflict) {
		t.Fatalf("step a error = %+v, want core.undo_conflict", s.Err)
	}
	if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{Step: "a", As: "reverted"}); err != nil {
		t.Fatal(err)
	}
	if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{}); err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateReverted, core.StateNeedsRepair); done.State != core.StateReverted {
		t.Fatalf("composite = %s (%+v), want reverted", done.State, done.Error)
	}
	for _, name := range []string{"web-a", "web-b"} {
		if _, ok := f.host.item(name); !ok {
			t.Errorf("item %s is gone; the operator took responsibility for it", name)
		}
	}
	var decisions int
	for _, e := range f.ctrl.cfg.Journal.(*coretest.MemJournal).Entries() {
		if e.ChangeID == v.ID && e.Phase == phaseResolve && strings.Contains(string(e.Input), `"as":"reverted"`) {
			decisions++
		}
	}
	if decisions != 2 {
		t.Errorf("journaled decisions = %d, want 2", decisions)
	}
}

func TestForwardCompositeRepairMakesANewAttempt(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	f.host.failNext("web-b", 1)
	v := f.applyC(refCfxRemove, `{"name":"web"}`, core.ApplyOptions{})
	f.await(v.ID, core.StateNeedsRepair)
	first := f.stepsOf(v.ID)["y"].ChildID
	r, err := repairC(f, cfxBob, v.ID, core.RepairRequest{})
	if err != nil || r.Change == nil || r.Change.State != core.StateApplying {
		t.Fatalf("Repair = %+v, %v; want the composite applying", r, err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	y := f.stepsOf(v.ID)["y"]
	if y.Attempt != 2 || len(y.Earlier) != 1 || y.Earlier[0] != first || y.ChildID == first {
		t.Errorf("step y = attempt %d, earlier %v, child %s; want attempt 2 after child %s", y.Attempt, y.Earlier, y.ChildID, first)
	}
	if child := f.agentChange(y.ChildID); child.Actor != cfxBob.Actor || child.ParentChangeID != v.ID {
		t.Errorf("new child actor %q parent %q, want %s and %s", child.Actor, child.ParentChangeID, cfxBob.Actor, v.ID)
	}
	if _, ok := f.host.item("web-b"); ok {
		t.Error("item web-b is still there")
	}
}

func TestForwardCompositeRepairAdvancesAKeptChild(t *testing.T) {
	f := newCfxFixture(t)
	f.host.setItem("web-a", "1")
	f.host.setItem("web-b", "2")
	f.host.failNext("web-b", 1)
	v := f.applyC(refCfxRemove, `{"name":"web"}`, core.ApplyOptions{})
	f.await(v.ID, core.StateNeedsRepair)
	y := f.stepsOf(v.ID)["y"]
	// No fixture action repairs a forward step's child as kept: cst.item.add
	// is repaired by its undo and cst.item.del never needs repair. So the
	// host and the child are left as a re-apply repair of the child leaves
	// them.
	f.host.mu.Lock()
	delete(f.host.items, "web-b")
	f.host.mu.Unlock()
	if _, err := f.agentDB.Writer().Exec("UPDATE core_changes SET state = ?, repair_outcome = ? WHERE id = ?",
		core.StateRepaired, core.RepairKept, y.ChildID); err != nil {
		t.Fatal(err)
	}
	if child := f.agentChange(y.ChildID); child.State != core.StateRepaired || child.RepairOutcome != core.RepairKept {
		t.Fatalf("child of y = %s with outcome %q, want repaired and kept", child.State, child.RepairOutcome)
	}
	r, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{})
	if err != nil || r.Change == nil || r.Change.State != core.StateApplying {
		t.Fatalf("Repair = %+v, %v; want the composite applying", r, err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	if s := f.stepsOf(v.ID)["y"]; s.State != core.StepApplied || s.ChildID != y.ChildID || s.Attempt != y.Attempt {
		t.Errorf("step y = %s child %s attempt %d, want applied with child %s at attempt %d",
			s.State, s.ChildID, s.Attempt, y.ChildID, y.Attempt)
	}
	if _, ok := f.host.item("web-b"); ok {
		t.Error("item web-b is back")
	}
}

func TestForwardCompositeRepairTakesTheRepairOutput(t *testing.T) {
	f := newCfxFixture(t)
	ctx := context.Background()
	v, b := stuckForward(t, f)
	f.agentClock.Advance(time.Hour)
	if _, err := f.agent.Resolve(ctx, cfxRoot, b.ChildID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept}); err != nil {
		t.Fatal(err)
	}
	res, err := f.agent.Repair(ctx, cfxRoot, b.ChildID, core.RepairRequest{})
	if err != nil || res.Plan == nil {
		t.Fatalf("child Repair = %+v, %v; want a re-apply plan", res, err)
	}
	p, err := f.agent.Apply(ctx, cfxRoot, res.Plan.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	f.awaitAgent(p.ID, core.StateCompleted)
	if child := f.agentChange(b.ChildID); child.State != core.StateRepaired || child.RepairOutcome != core.RepairKept {
		t.Fatalf("child of b = %s with outcome %q, want repaired and kept", child.State, child.RepairOutcome)
	}
	if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{}); err != nil {
		t.Fatal(err)
	}
	done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair)
	if done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	var out cfxOut
	if err := json.Unmarshal(done.Output, &out); err != nil {
		t.Fatal(err)
	}
	if got := string(out.Steps["b"]); got != `{"name":"z2","value":"2"}` {
		t.Errorf("output of step b = %s, want the repair change's output", got)
	}
}

func TestCompositeIntentsAdmittedBeforeTheBarrierAreRefused(t *testing.T) {
	ctx := context.Background()
	// Each call passed admit before the barrier was set.
	barrier := func(f *cfxFixture) {
		t.Helper()
		if _, err := f.ctrlDB.Writer().Exec("INSERT INTO core_meta (key, value) VALUES (?, '1')", MetaUninstalling); err != nil {
			t.Fatal(err)
		}
	}

	a := newCfxFixture(t)
	pl, err := a.planC(refCfxAdd, `{"name":"web"}`)
	if err != nil {
		t.Fatal(err)
	}
	barrier(a)
	_, err = a.ctrl.applyComposite(ctx, cfxRoot, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.Uninstalling)
	var n int
	if err := a.ctrlDB.Read().QueryRow("SELECT count(*) FROM core_changes WHERE plan_id = ?", pl.PlanID).Scan(&n); err != nil || n != 0 {
		t.Errorf("changes of the refused plan = %d (%v), want 0", n, err)
	}

	r := newCfxFixture(t)
	v, _ := stuckForward(t, r)
	barrier(r)
	err = r.ctrl.claimComposite(ctx, v.ID, core.StateApplying, func(*sql.Tx) error { return nil })
	wantCode(t, err, errcode.Uninstalling)
	if got, _ := r.ctrl.Change(ctx, v.ID); got.State != core.StateNeedsRepair || got.Owner != "" {
		t.Errorf("composite = %s owned by %q, want needs_repair and unowned", got.State, got.Owner)
	}
}

func TestForwardCompositeStepDecisions(t *testing.T) {
	t.Run("applied counts the step done", func(t *testing.T) {
		f := newCfxFixture(t)
		v, b := stuckForward(t, f)
		_, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{})
		if wantCode(t, err, errcode.NeedsRepair); !strings.Contains(err.Error(), b.ChildID) {
			t.Errorf("refusal %v does not name the child %s", err, b.ChildID)
		}
		abandonChild(t, f, v.ID, b)
		_, err = repairC(f, cfxRoot, v.ID, core.RepairRequest{})
		wantCode(t, err, errcode.NeedsRepair)
		if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{Step: "b", As: "applied"}); err != nil {
			t.Fatal(err)
		}
		if s := f.stepsOf(v.ID)["b"]; s.State != core.StepApplied {
			t.Fatalf("step b = %s, want applied", s.State)
		}
		if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{}); err != nil {
			t.Fatal(err)
		}
		if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateCompleted {
			t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
		}
	})
	t.Run("reverted makes the next repair a new attempt", func(t *testing.T) {
		f := newCfxFixture(t)
		v, b := stuckForward(t, f)
		abandonChild(t, f, v.ID, b)
		if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{Step: "b", As: "reverted"}); err != nil {
			t.Fatal(err)
		}
		if s := f.stepsOf(v.ID)["b"]; s.State != core.StepPending || s.ChildID != "" || len(s.Earlier) != 1 {
			t.Fatalf("step b = %s child %q earlier %v, want pending with the abandoned child kept as earlier", s.State, s.ChildID, s.Earlier)
		}
		if _, err := repairC(f, cfxRoot, v.ID, core.RepairRequest{}); err != nil {
			t.Fatal(err)
		}
		if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair); done.State != core.StateCompleted {
			t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
		}
		if v, ok := f.host.item("z2"); !ok || v != "2" {
			t.Errorf("item z2 = %q (%v), want the new attempt's 2", v, ok)
		}
	})
}

func TestCompositeRepairRefusals(t *testing.T) {
	f := newCfxFixture(t)
	v, b := stuckForward(t, f)
	cases := []struct {
		name string
		req  core.RepairRequest
		code errcode.Code
	}{
		{"an input", core.RepairRequest{Input: json.RawMessage(`{}`)}, errcode.InvalidInput},
		{"an unknown decision", core.RepairRequest{Step: "b", As: "maybe"}, errcode.InvalidInput},
		{"a decision without a step", core.RepairRequest{As: "applied"}, errcode.InvalidInput},
		{"an unknown step", core.RepairRequest{Step: "nope", As: "applied"}, errcode.InvalidInput},
		{"a step that is not at the cursor", core.RepairRequest{Step: "a", As: "applied"}, errcode.InvalidInput},
		{"a child that is neither kept nor abandoned", core.RepairRequest{Step: "b", As: "applied"}, errcode.InvalidInput},
	}
	for _, c := range cases {
		_, err := repairC(f, cfxRoot, v.ID, c.req)
		if got := errcode.From(err).Code; got != c.code {
			t.Errorf("%s: Repair = %v, want %s", c.name, err, c.code)
		}
	}
	if child := f.agentChange(b.ChildID); child.State != core.StateNeedsRepair {
		t.Errorf("a refused repair changed the child to %s", child.State)
	}

	g := newCfxFixture(t)
	gv, _ := awaitingGate(t, g)
	_, err := repairC(g, cfxRoot, gv.ID, core.RepairRequest{Step: "g", As: "reverted"})
	wantCode(t, err, errcode.ChangeBusy)

	r := newCfxFixture(t)
	r.bad.set(cstStep("a", refCstAdd, `{"name":"z","value":"1","remote":true}`))
	r.host.failNext("z", 1)
	rv := r.applyC(refCfxBad, `{"name":"z"}`, core.ApplyOptions{})
	r.await(rv.ID, core.StateNeedsRepair)
	_, err = repairC(r, cfxRoot, rv.ID, core.RepairRequest{Step: "a", As: "applied"})
	wantCode(t, err, errcode.InvalidInput)
}

func TestCompositeReleaseWithAnUnreachableAgent(t *testing.T) {
	f := newCfxFixture(t)
	v, b := stuckForward(t, f)
	f.kids.setUnreachable(true)
	r, err := f.ctrl.Release(context.Background(), cfxRoot, v.ID, "give up", nil)
	if err != nil || r.Change.State != core.StateReleasing || len(r.Abandoned) != 0 {
		t.Fatalf("Release = %+v, %v; want releasing with no effects of its own", r, err)
	}
	for range 3 {
		f.ctrlClock.Advance(ChildPoll)
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := f.ctrl.Change(context.Background(), v.ID); got.State != core.StateReleasing {
		t.Fatalf("composite = %s while the agent is unreachable, want releasing", got.State)
	}
	f.kids.setUnreachable(false)
	done := f.await(v.ID, core.StateAbandoned)
	if done.Owner != "" || f.lockOwner("composite:cfx") != "" {
		t.Errorf("abandoned composite owned by %q holding its lock %q", done.Owner, f.lockOwner("composite:cfx"))
	}
	if child := f.agentChange(b.ChildID); child.State != core.StateAbandoned {
		t.Errorf("child of b = %s, want abandoned", child.State)
	}
	for id, s := range f.stepsOf(v.ID) {
		if s.State != core.StepReleased {
			t.Errorf("step %s = %s, want released", id, s.State)
		}
	}
}

func TestCompositeReleaseWaitsForAChildRepair(t *testing.T) {
	f := newCfxFixture(t)
	v, b := stuckForward(t, f)
	if _, err := f.agentDB.Writer().Exec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerRepair, b.ChildID); err != nil {
		t.Fatal(err)
	}
	if _, err := f.ctrl.Release(context.Background(), cfxRoot, v.ID, "give up", nil); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		f.ctrlClock.Advance(ChildPoll)
		time.Sleep(5 * time.Millisecond)
	}
	if got, _ := f.ctrl.Change(context.Background(), v.ID); got.State != core.StateReleasing {
		t.Fatalf("composite = %s while its child's repair runs, want releasing", got.State)
	}
	if child := f.agentChange(b.ChildID); child.State != core.StateNeedsRepair {
		t.Fatalf("child of b = %s while its repair runs, want needs_repair", child.State)
	}
	if _, err := f.agentDB.Writer().Exec("UPDATE core_changes SET recovery_owner = '' WHERE id = ?", b.ChildID); err != nil {
		t.Fatal(err)
	}
	f.await(v.ID, core.StateAbandoned)
	if child := f.agentChange(b.ChildID); child.State != core.StateAbandoned {
		t.Errorf("child of b = %s, want abandoned", child.State)
	}
}

func TestCompositeReleaseRefusalsAndRepeat(t *testing.T) {
	f := newCfxFixture(t)
	v, _ := stuckForward(t, f)
	ctx := context.Background()
	for _, reason := range []string{"", strings.Repeat("x", 501)} {
		_, err := f.ctrl.Release(ctx, cfxRoot, v.ID, reason, nil)
		wantCode(t, err, errcode.InvalidInput)
	}
	if _, err := f.ctrl.Release(ctx, cfxRoot, v.ID, "give up", nil); err != nil {
		t.Fatal(err)
	}
	f.await(v.ID, core.StateAbandoned)
	r, err := f.ctrl.Release(ctx, cfxRoot, v.ID, "again", nil)
	if err != nil || r.Change.State != core.StateAbandoned || len(r.Abandoned) != 0 {
		t.Errorf("repeated Release = %+v, %v; want the abandoned composite", r, err)
	}

	done := newCfxFixture(t)
	dv := done.applyC(refCfxAdd, `{"name":"web"}`, core.ApplyOptions{})
	done.await(dv.ID, core.StateCompleted)
	_, err = done.ctrl.Release(ctx, cfxRoot, dv.ID, "give up", nil)
	wantCode(t, err, errcode.ChangeNotPending)
}

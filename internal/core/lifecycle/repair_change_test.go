package lifecycle

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// repairPlan asks for a repair of target with input and returns the plan.
func (r *rfxFixture) repairPlan(target, input string) core.PlanView {
	r.t.Helper()
	req := core.RepairRequest{}
	if input != "" {
		req.Input = json.RawMessage(input)
	}
	res, err := r.eng.Repair(context.Background(), alice, target, req)
	if err != nil || res.Plan == nil {
		r.t.Fatalf("Repair = %+v, %v; want a plan", res, err)
	}
	return *res.Plan
}

func (r *rfxFixture) lockOwner(key string) string {
	r.t.Helper()
	var owner string
	if err := r.db.Read().QueryRow("SELECT owner FROM core_locks WHERE key = ?", key).Scan(&owner); err != nil {
		return ""
	}
	return owner
}

func TestRepairChangeRepairsItsTarget(t *testing.T) {
	cases := []struct {
		name, input, outcome string
	}{
		{"restored", `{}`, core.RepairRestored},
		{"kept", `{"outcome":"keep"}`, core.RepairKept},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRfx(t)
			ctx := context.Background()
			target := r.revertibleTarget()
			p := r.apply(alice, r.repairPlan(target.ID, tc.input).PlanID, core.ApplyOptions{})
			if p.State != core.StateCompleted {
				t.Fatalf("repair change = %s, want completed", p.State)
			}
			v, err := r.eng.Change(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != core.StateRepaired || v.RepairOutcome != tc.outcome || v.RepairedBy != p.ID || v.Owner != "" {
				t.Errorf("target = %s outcome %q repaired by %q owned by %q; want repaired %s by %s, unowned",
					v.State, v.RepairOutcome, v.RepairedBy, v.Owner, tc.outcome, p.ID)
			}
			if n := r.count("SELECT count(*) FROM core_locks"); n != 0 {
				t.Errorf("locks = %d, want none: they are released with the repair change", n)
			}
			for _, call := range []string{"Apply", "Verify"} {
				if want := "repair " + call + " repairOf=" + target.ID; !r.rh.sawCall(want) {
					t.Errorf("calls = %v, want %q", r.rh.calls, want)
				}
			}
		})
	}
}

func TestRepairChangeOfAConvergingChangeKeeps(t *testing.T) {
	r := newRfx(t)
	target := r.convergeTarget(`{"name":"a","value":"1"}`)
	p := r.apply(alice, r.repairPlan(target.ID, "").PlanID, core.ApplyOptions{})
	if p.State != core.StateCompleted {
		t.Fatalf("repair change = %s, want completed", p.State)
	}
	v, err := r.eng.Change(context.Background(), target.ID)
	if err != nil || v.State != core.StateRepaired || v.RepairOutcome != core.RepairKept {
		t.Fatalf("target = %+v, %v; want repaired with outcome kept", v, err)
	}
}

func TestRepairChangeHoldsTheTargetsLocks(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.revertibleTarget()
	pl := r.repairPlan(target.ID, "")
	entered, release := make(chan struct{}, 1), make(chan struct{})
	r.rh.mu.Lock()
	r.rh.onApply = func() {
		entered <- struct{}{}
		<-release
	}
	r.rh.mu.Unlock()
	done := make(chan error, 1)
	go func() {
		_, err := r.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
		done <- err
	}()
	select {
	case <-entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the repair change never applied")
	}
	var pid string
	if err := r.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", pl.PlanID).Scan(&pid); err != nil {
		t.Fatal(err)
	}
	v, err := r.eng.Change(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.Owner != core.OwnerRepair || v.RepairedBy != pid || v.State != core.StateNeedsRepair {
		t.Errorf("target while repairing = %s owned by %q repaired by %q; want needs_repair owned by repair, by %s",
			v.State, v.Owner, v.RepairedBy, pid)
	}
	if owner := r.lockOwner(rfxKey); owner != pid {
		t.Errorf("lock %s is held by %q, want the repair change %s", rfxKey, owner, pid)
	}
	_, err = r.eng.Repair(ctx, alice, pid, core.RepairRequest{})
	wantRefusal(t, err, errcode.ChangeBusy, target.ID)
	_, err = r.eng.Release(ctx, bob, target.ID, "given up", nil)
	wantRefusal(t, err, errcode.ChangeBusy, "owned by repair")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if s := r.state(target.ID); s != core.StateRepaired {
		t.Errorf("target = %s, want repaired", s)
	}
}

func TestFailedRepairChangeReturnsTheLocks(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.revertibleTarget()
	p := r.apply(alice, r.repairPlan(target.ID, `{"fail":true}`).PlanID, core.ApplyOptions{})
	if p.State != core.StateFailed {
		t.Fatalf("repair change = %s, want failed", p.State)
	}
	v, err := r.eng.Change(ctx, target.ID)
	if err != nil || v.State != core.StateNeedsRepair || v.Owner != "" {
		t.Fatalf("target = %s owned by %q (%v), want needs_repair and unowned", v.State, v.Owner, err)
	}
	if owner := r.lockOwner(rfxKey); owner != target.ID {
		t.Errorf("lock %s is held by %q, want it back with the target %s", rfxKey, owner, target.ID)
	}
	again := r.apply(alice, r.repairPlan(target.ID, "").PlanID, core.ApplyOptions{})
	if again.State != core.StateCompleted || r.state(target.ID) != core.StateRepaired {
		t.Errorf("a second repair = %s, target %s; want completed and repaired", again.State, r.state(target.ID))
	}
}

func TestRepairChangeLeftInDoubtIsRecordedFailed(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.revertibleTarget()
	first := r.repairPlan(target.ID, `{"failRemote":true}`)
	second := r.repairPlan(target.ID, "")

	p := r.apply(alice, first.PlanID, core.ApplyOptions{})
	if p.State != core.StateFailed || p.Error == nil || p.Error.Code != string(errcode.NeedsRepair) {
		t.Fatalf("repair change = %s with error %+v, want failed with core.needs_repair", p.State, p.Error)
	}
	if v, _ := r.eng.Change(ctx, target.ID); v.State != core.StateNeedsRepair || v.Owner != "" {
		t.Errorf("target = %s owned by %q, want needs_repair and unowned", v.State, v.Owner)
	}
	if owner := r.lockOwner(rfxKey); owner != target.ID {
		t.Errorf("lock %s is held by %q, want the target %s", rfxKey, owner, target.ID)
	}
	effs, err := r.eng.Effects(ctx, target.ID)
	if err != nil || len(effs) != 1 || effs[0].ChangeID != p.ID || effs[0].State != core.EffectUnresolved {
		t.Fatalf("effects of the target = %+v, %v; want the repair change's unresolved effect", effs, err)
	}

	_, err = r.eng.Apply(ctx, alice, second.PlanID, core.ApplyOptions{})
	wantRefusal(t, err, errcode.NeedsRepair, "resolve its effects first")
	_, err = r.eng.Repair(ctx, alice, p.ID, core.RepairRequest{})
	wantRefusal(t, err, errcode.ChangeBusy, target.ID)
	_, err = r.eng.Release(ctx, bob, p.ID, "given up", nil)
	wantRefusal(t, err, errcode.ChangeBusy, target.ID)

	res, err := r.eng.Release(ctx, bob, target.ID, "given up", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Abandoned) != 1 || res.Abandoned[0].ChangeID != p.ID {
		t.Errorf("abandoned = %+v, want the repair change's effect", res.Abandoned)
	}
}

func TestRepairChangeIsRefusedWhenItsTargetMoved(t *testing.T) {
	cases := []struct {
		name  string
		move  func(r *rfxFixture, target string)
		code  errcode.Code
		part  string
		state string
	}{
		{"the target is owned", func(r *rfxFixture, target string) {
			r.sqlExec("UPDATE core_changes SET recovery_owner = ? WHERE id = ?", core.OwnerResolve, target)
		}, errcode.ChangeBusy, "owned by resolve", core.StateNeedsRepair},
		{"the target was released", func(r *rfxFixture, target string) {
			r.sqlExec("UPDATE core_changes SET state = ? WHERE id = ?", core.StateAbandoned, target)
			r.sqlExec("DELETE FROM core_locks")
		}, errcode.ChangeBusy, "abandoned", core.StateAbandoned},
		{"the target's recovery handler has not finished", func(r *rfxFixture, target string) {
			r.sqlExec("UPDATE core_changes SET recovery_retry = 1 WHERE id = ?", target)
		}, errcode.NeedsRepair, "recovery handler has not finished", core.StateNeedsRepair},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRfx(t)
			target := r.revertibleTarget()
			pl := r.repairPlan(target.ID, "")
			tc.move(r, target.ID)
			_, err := r.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
			wantRefusal(t, err, tc.code, tc.part)
			if n := r.count("SELECT count(*) FROM core_changes WHERE plan_id = ?", pl.PlanID); n != 0 {
				t.Errorf("a refused repair plan made %d changes", n)
			}
			if s := r.state(target.ID); s != tc.state {
				t.Errorf("target = %s, want %s", s, tc.state)
			}
		})
	}
}

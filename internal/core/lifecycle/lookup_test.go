package lifecycle

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// planOf reads the plan id of a change.
func (f *fixture) planOf(changeID string) string {
	f.t.Helper()
	var planID string
	if err := f.db.Read().QueryRow("SELECT plan_id FROM core_changes WHERE id = ?", changeID).Scan(&planID); err != nil {
		f.t.Fatal(err)
	}
	return planID
}

func TestLookupOfAPlanWithoutAChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	lk, err := f.eng.Lookup(ctx, pl.PlanID, false)
	if err != nil || !reflect.DeepEqual(lk, core.PlanLookup{Status: core.LookupAbsent}) {
		t.Errorf("lookup = %+v, %v; want absent", lk, err)
	}
	_, err = f.eng.Lookup(ctx, "NOPE", false)
	wantCode(t, err, errcode.PlanNotFound)
	_, err = f.eng.Retire(ctx, "NOPE")
	wantCode(t, err, errcode.PlanNotFound)
}

func TestLookupOfAChange(t *testing.T) {
	add := `{"name":"a","value":"1"}`
	cases := []struct {
		name  string
		setup func(f *fixture) core.ChangeView
		want  func(v core.ChangeView) core.PlanLookup
	}{
		{"completed", func(f *fixture) core.ChangeView {
			return f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupTerminal, ChangeID: v.ID, State: core.StateCompleted,
				Output: []byte(`{"previous":"","token":"[redacted]"}`)}
		}},
		{"pending revertible", func(f *fixture) core.ChangeView {
			return f.pending(add)
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupTerminal, ChangeID: v.ID, State: core.StatePending,
				Deadline: v.RevertDeadline, Output: []byte(`{"previous":"","token":"[redacted]"}`)}
		}},
		{"owned by the engine", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec("UPDATE core_changes SET state = ?, recovery_owner = ?, output_json = NULL WHERE id = ?",
				core.StateApplying, core.OwnerEngine, v.ID)
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupRunning, ChangeID: v.ID, State: core.StateApplying, Owner: core.OwnerEngine}
		}},
		{"owned by an undo", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec("UPDATE core_changes SET state = ?, recovery_owner = ?, output_json = NULL WHERE id = ?",
				core.StateUndoing, core.OwnerUndo, v.ID)
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupRunning, ChangeID: v.ID, State: core.StateUndoing, Owner: core.OwnerUndo}
		}},
		{"owned by a repair change", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec("UPDATE core_changes SET state = ?, recovery_owner = ?, output_json = NULL WHERE id = ?",
				core.StateNeedsRepair, core.OwnerRepair, v.ID)
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupRunning, ChangeID: v.ID, State: core.StateNeedsRepair, Owner: core.OwnerRepair}
		}},
		{"owned by a recovery handler", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec("UPDATE core_changes SET state = ?, recovery_owner = ?, output_json = NULL WHERE id = ?",
				core.StateRecovering, core.OwnerModule, v.ID)
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupRecovering, ChangeID: v.ID, State: core.StateRecovering, Owner: core.OwnerModule}
		}},
		{"in needs_repair with effects", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec(`UPDATE core_changes SET state = ?, output_json = NULL, error_code = ?, error_message = 'in doubt',
				error_cause = 'cloud.uncertain_outcome' WHERE id = ?`, core.StateNeedsRepair, string(errcode.NeedsRepair), v.ID)
			f.putEffect(v.ID, 1, core.EffectMade, "")
			f.putEffect(v.ID, 2, core.EffectUndoing, "")
			f.putEffect(v.ID, 3, core.EffectUnresolved, core.ReasonUncertain)
			f.putEffect(v.ID, 4, core.EffectSkipped, "")
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupTerminal, ChangeID: v.ID, State: core.StateNeedsRepair,
				Made: 2, Unresolved: 1, Error: &core.ChangeError{
					Code: string(errcode.NeedsRepair), Message: "in doubt", Cause: "cloud.uncertain_outcome"}}
		}},
		{"repaired", func(f *fixture) core.ChangeView {
			v := f.apply(alice, f.plan(alice, refAdd, add).PlanID, core.ApplyOptions{})
			f.sqlExec("UPDATE core_changes SET state = ?, repair_outcome = ?, output_json = NULL WHERE id = ?",
				core.StateRepaired, core.RepairRestored, v.ID)
			return v
		}, func(v core.ChangeView) core.PlanLookup {
			return core.PlanLookup{Status: core.LookupTerminal, ChangeID: v.ID, State: core.StateRepaired,
				RepairOutcome: core.RepairRestored}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			v := tc.setup(f)
			lk, err := f.eng.Lookup(context.Background(), f.planOf(v.ID), false)
			if err != nil {
				t.Fatal(err)
			}
			if want := tc.want(v); !reflect.DeepEqual(lk, want) {
				t.Errorf("lookup = %+v\nwant %+v", lk, want)
			}
		})
	}
}

func TestRetireThenApply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	lk, err := f.eng.Retire(ctx, pl.PlanID)
	if err != nil || lk.Status != core.LookupRetired {
		t.Fatalf("retire = %+v, %v; want retired", lk, err)
	}
	_, err = f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
	wantRefusal(t, err, errcode.PlanNotFound, "retired")
	if lk, err = f.eng.Lookup(ctx, pl.PlanID, false); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("lookup = %+v, %v; want retired", lk, err)
	}
	if lk, err = f.eng.Retire(ctx, pl.PlanID); err != nil || lk.Status != core.LookupRetired {
		t.Errorf("a repeated retire = %+v, %v; want retired", lk, err)
	}
	if n := f.count("SELECT count(*) FROM core_changes WHERE plan_id = ?", pl.PlanID); n != 0 {
		t.Errorf("a retired plan made %d changes", n)
	}
}

func TestRetireOfAnAppliedPlanAnswersItsLookup(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	lk, err := f.eng.Retire(ctx, pl.PlanID)
	if err != nil || lk.Status != core.LookupTerminal || lk.ChangeID != v.ID {
		t.Fatalf("retire = %+v, %v; want the change's lookup", lk, err)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ? AND retired_at IS NOT NULL", pl.PlanID); n != 0 {
		t.Error("retire marked a plan that has a change")
	}
}

func TestRetireRacesApply(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	for i := range 100 {
		pl := f.plan(alice, refAdd, fmt.Sprintf(`{"name":"n%d","value":"1"}`, i))
		start := make(chan struct{})
		var wg sync.WaitGroup
		var applied core.ChangeView
		var retired core.PlanLookup
		var applyErr, retireErr error
		wg.Add(2)
		go func() {
			defer wg.Done()
			<-start
			applied, applyErr = f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
		}()
		go func() {
			defer wg.Done()
			<-start
			retired, retireErr = f.eng.Retire(ctx, pl.PlanID)
		}()
		close(start)
		wg.Wait()
		if retireErr != nil {
			t.Fatalf("round %d: retire: %v", i, retireErr)
		}
		lk, err := f.eng.Lookup(ctx, pl.PlanID, false)
		if err != nil {
			t.Fatalf("round %d: lookup: %v", i, err)
		}
		changes := f.count("SELECT count(*) FROM core_changes WHERE plan_id = ?", pl.PlanID)
		if applyErr != nil {
			wantCode(t, applyErr, errcode.PlanNotFound)
			if retired.Status != core.LookupRetired || lk.Status != core.LookupRetired || changes != 0 {
				t.Fatalf("round %d: apply refused, retire %+v, lookup %+v, %d changes; want retired and none",
					i, retired, lk, changes)
			}
			continue
		}
		if retired.Status == core.LookupRetired || retired.ChangeID != applied.ID || lk.ChangeID != applied.ID ||
			lk.Status != core.LookupTerminal || changes != 1 {
			t.Fatalf("round %d: applied %s, retire %+v, lookup %+v, %d changes; want the change in every answer",
				i, applied.ID, retired, lk, changes)
		}
	}
}

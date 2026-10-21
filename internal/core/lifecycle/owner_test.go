package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestAnApplyOwnsItsChangeUntilItsOutcome(t *testing.T) {
	f := newJFixture(t, jfxOptions{})
	pl := f.plan(alice, refJModify, `{"name":"a","value":"1","local":true,"hangApply":true}`)
	go func() {
		if _, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{}); err != nil {
			t.Error(err)
		}
	}()
	f.store.waitEntered(t, "a")
	id := f.changeOf(pl.PlanID)
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateApplying || v.Owner != core.OwnerEngine {
		t.Errorf("running change = %s owned by %q, want applying owned by engine", v.State, v.Owner)
	}
	f.store.open("a")
	if done := f.waitState(id, core.StateCompleted); done.Owner != "" {
		t.Errorf("completed change owned by %q, want unowned", done.Owner)
	}
	failed := f.apply(alice, f.plan(alice, refJModify, `{"name":"b","value":"1","local":true,"failBeforeCall":true}`).PlanID, core.ApplyOptions{})
	if failed.State != core.StateFailed || failed.Owner != "" {
		t.Errorf("failed change = %s owned by %q, want failed and unowned", failed.State, failed.Owner)
	}
}

func TestClaimIsOneConditionalUpdate(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	claimUndo := func(id string) error {
		return f.db.Write(ctx, func(tx *sql.Tx) error {
			return claim(ctx, tx, f.clock.Now(), id, "", core.OwnerUndo, core.StateCompleted, core.StateUndoing)
		})
	}
	if err := claimUndo(v.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.State != core.StateUndoing || got.Owner != core.OwnerUndo {
		t.Errorf("claimed change = %s owned by %q, want undoing owned by undo", got.State, got.Owner)
	}
	err = claimUndo(v.ID)
	wantCode(t, err, errcode.ChangeBusy)
	if !strings.Contains(err.Error(), "is undoing and owned by undo") {
		t.Errorf("second claim error = %v, want the state and the owner named", err)
	}
	wantCode(t, claimUndo("01NOPE"), errcode.ChangeNotFound)
}

func TestTheViewCarriesTheLifecycleColumns(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if _, err := f.db.Writer().Exec(
		`UPDATE core_changes SET parent_change_id = 'comp1', recovery_owner = 'module', repaired_by = 'rep1',
		 repair_outcome = 'kept', override_json = '{"overwritten":"a"}', error_code = 'core.needs_repair',
		 error_message = 'effect 1 is unresolved', error_cause = 'change.revert_failed' WHERE id = ?`, v.ID); err != nil {
		t.Fatal(err)
	}
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ParentChangeID != "comp1" || got.Owner != core.OwnerModule || got.RepairedBy != "rep1" ||
		got.RepairOutcome != core.RepairKept || string(got.Override) != `{"overwritten":"a"}` || got.Error == nil ||
		*got.Error != (core.ChangeError{Code: "core.needs_repair", Message: "effect 1 is unresolved", Cause: "change.revert_failed"}) {
		t.Errorf("view = %+v (error %+v)", got, got.Error)
	}
	listed, err := f.eng.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(listed) != 1 || listed[0].Owner != core.OwnerModule {
		t.Errorf("listing = %+v, %v; want the same columns", listed, err)
	}
}

func TestTransitionWritesTheLifecycleColumns(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	retry := true
	got, err := f.eng.transition(ctx, v.ID, transition{
		from: core.StateCompleted, to: core.StateNeedsRepair, owner: newOwner(core.OwnerModule), keepLocks: true,
		err: &core.ChangeError{Code: string(errcode.NeedsRepair), Message: "m"}, cause: string(errcode.ChangeRevertFailed),
		override: json.RawMessage(`{"o":1}`), retry: &retry, repairOutcome: core.RepairRestored,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.State != core.StateNeedsRepair || got.Owner != core.OwnerModule || got.Error.Cause != string(errcode.ChangeRevertFailed) ||
		string(got.Override) != `{"o":1}` || got.RepairOutcome != core.RepairRestored ||
		f.count("SELECT recovery_retry FROM core_changes WHERE id = ?", v.ID) != 1 {
		t.Errorf("view = %+v (error %+v)", got, got.Error)
	}
	got, err = f.eng.transition(ctx, v.ID, transition{
		from: core.StateNeedsRepair, to: core.StateFailed, owner: newOwner(""),
		err: &core.ChangeError{Code: string(errcode.Internal), Message: "m2"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Owner != "" || got.Error.Cause != "" || got.RepairOutcome != core.RepairRestored {
		t.Errorf("view = %+v (error %+v); want the owner and the old cause cleared, the outcome kept", got, got.Error)
	}
	if _, err := f.eng.transition(ctx, v.ID, transition{from: core.StateFailed, to: core.StateFailed, release: true, keepLocks: true}); err == nil {
		t.Error("a transition that both releases and keeps its locks was accepted")
	}
}

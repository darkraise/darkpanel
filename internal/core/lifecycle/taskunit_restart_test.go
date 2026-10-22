package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// taskJournal records what an apply unit's Task does for item name: the
// item set to value, journaled as one made local modify effect.
func (f *taskFixture) taskJournal(ref core.ActionRef, id, name, value string) {
	f.t.Helper()
	ctx := context.Background()
	def, ok := f.reg.Action(ref)
	if !ok {
		f.t.Fatalf("%s is not registered", ref)
	}
	env := f.eng.changeEnv(alice, def, id, core.CallContext{})
	after := rcvDoc(value)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: "tsk.item:" + name, Local: true, InverseLocal: true,
		Before: rcvDoc(f.tsk.get(name)), After: &after,
	}, nil)
	if err != nil {
		f.t.Fatal(err)
	}
	f.tsk.set(name, value)
	if err := env.Effects.Made(ctx, seq, "tsk.item:"+name, after); err != nil {
		f.t.Fatal(err)
	}
}

// taskCompleted applies tsk.pkg.install for item a as the apply unit would
// run it and waits until the change is completed.
func (f *taskFixture) taskCompleted() core.ChangeView {
	f.t.Helper()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
		f.taskJournal(refTskInstall, v.ID, "a", "1")
	})
	f.taskPoll(func() {
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
		f.units.set(name, false)
	})
	return f.taskUntil(v.ID, core.StateCompleted)
}

func TestTaskApplyOutlivesARestartAndItsDeadline(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	f.taskArmed()
	f.restartTask()
	if err := f.eng.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	c, err := f.eng.Change(ctx, v.ID)
	if err != nil || c.State != core.StateRecovering || c.Owner != core.OwnerModule {
		t.Fatalf("after the restart: %+v, %v; want recovering, owned by module", c, err)
	}
	f.taskArmed()
	f.clock.Advance(tskTimeout + TaskPoll)
	f.taskArmed()
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateRecovering ||
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
		t.Errorf("a unit active past its deadline: %+v, %v; want the change recovering and locked", c, err)
	}
	f.taskPoll(func() {
		f.tsk.set("a", "1")
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
		f.units.set(name, false)
	})
	done := f.taskUntil(v.ID, core.StateCompleted)
	if done.Owner != "" || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 0 {
		t.Errorf("completed change = %+v, want unowned and unlocked", done)
	}
	if got := f.units.startedUnits(); !reflect.DeepEqual(got, []string{name}) {
		t.Errorf("started units = %v, want only %s: a running row is never started again", got, name)
	}
}

func TestTaskUndoRetriesAfterACancelledAttempt(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskCompleted()
	undo1, undo2 := TaskUnitName(v.ID, core.TaskOpUndo, 1), TaskUnitName(v.ID, core.TaskOpUndo, 2)

	f.units.mu.Lock()
	f.units.startErr = errors.New("the bus connection dropped")
	f.units.mu.Unlock()
	u, err := f.eng.Undo(ctx, alice, v.ID, nil)
	if err != nil || u.State != core.StateUndoing {
		t.Fatalf("first Undo = %+v, %v; want undoing", u, err)
	}
	if got := f.taskRowState(v.ID, core.TaskOpUndo, 1); got != taskRowStarting {
		t.Fatalf("undo row = %s, want starting", got)
	}
	after := f.taskUntil(v.ID, core.StateNeedsRepair)
	if got := f.taskRowState(v.ID, core.TaskOpUndo, 1); got != taskRowCancelled || after.Owner != "" ||
		f.tsk.get("a") != "1" || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
		t.Fatalf("after the fenced undo: row %s, change %+v, item a %q; want cancelled, needs_repair, unowned, item kept, locked",
			got, after, f.tsk.get("a"))
	}

	f.units.mu.Lock()
	f.units.startErr = nil
	f.units.mu.Unlock()
	u, err = f.eng.Undo(ctx, alice, v.ID, nil)
	if err != nil || u.State != core.StateUndoing {
		t.Fatalf("second Undo = %+v, %v; want undoing", u, err)
	}
	f.taskPoll(func() {
		f.units.set(undo2, true)
		f.taskRowTo(v.ID, core.TaskOpUndo, 2, taskRowRunning, "", "", "")
	})
	f.taskArmed()
	f.restartTask()
	if err := f.eng.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateUndoing || c.Owner != core.OwnerUndo {
		t.Fatalf("after the restart: %+v, %v; want undoing, owned by undo", c, err)
	}
	def, _ := f.reg.Action(refTskInstall)
	f.taskPoll(func() {
		// What the undo unit's process does: the undo run, from the journal.
		if err := f.eng.undoRun(ctx, f.eng.changeEnv(alice, def, v.ID, core.CallContext{}), def, v.ID); err != nil {
			t.Errorf("undoRun: %v", err)
		}
		f.taskRowTo(v.ID, core.TaskOpUndo, 2, taskRowSucceeded, "", "", "")
		f.units.set(undo2, false)
	})
	done := f.taskUntil(v.ID, core.StateReverted)
	if done.Owner != "" || f.tsk.get("a") != "" || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 0 {
		t.Errorf("after the second undo: %+v, item a %q; want reverted, unowned, unlocked, item a empty", done, f.tsk.get("a"))
	}
	want := []string{TaskUnitName(v.ID, core.TaskOpApply, 1), undo1, undo2}
	if got := f.units.startedUnits(); !reflect.DeepEqual(got, want) {
		t.Errorf("started units = %v, want %v", got, want)
	}
}

func TestTaskUndoRefusedWhileTheLastUndoUnitRuns(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskCompleted()
	undo1 := TaskUnitName(v.ID, core.TaskOpUndo, 1)
	f.units.mu.Lock()
	f.units.startErr = errors.New("the bus connection dropped")
	f.units.mu.Unlock()
	if _, err := f.eng.Undo(ctx, alice, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	f.taskUntil(v.ID, core.StateNeedsRepair)
	// The unit of the cancelled attempt starts after all; its process finds
	// the row fenced and exits, but until then the unit is active.
	f.units.set(undo1, true)
	_, err := f.eng.Undo(ctx, alice, v.ID, nil)
	wantCode(t, err, errcode.ChangeBusy)
	if n := f.count("SELECT count(*) FROM core_task_runs WHERE change_id = ? AND op = ?", v.ID, core.TaskOpUndo); n != 1 {
		t.Errorf("undo rows = %d, want 1", n)
	}
}

func TestTaskRecoverRefusedWhileTheLastRecoverUnitRuns(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskPin, `{"name":"a","value":"1"}`)
	apply1, recover1 := TaskUnitName(v.ID, core.TaskOpApply, 1), TaskUnitName(v.ID, core.TaskOpRecover, 1)
	f.taskPoll(func() {
		f.units.set(apply1, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	f.taskPoll(func() {
		f.units.mu.Lock()
		f.units.startErr = errors.New("the bus connection dropped")
		f.units.mu.Unlock()
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowFailed, "", string(errcode.Unavailable), "the package mirror is unreachable")
		f.units.set(apply1, false)
	})
	after := f.taskUntil(v.ID, core.StateNeedsRepair)
	if got := f.taskRowState(v.ID, core.TaskOpRecover, 1); got != taskRowCancelled || after.Owner != "" {
		t.Fatalf("after the fenced recover: row %s, change %+v; want cancelled, needs_repair, unowned", got, after)
	}
	// The unit of the cancelled attempt starts after all; its process finds
	// the row fenced and exits, but until then the unit is active.
	f.units.set(recover1, true)
	_, err := f.eng.Repair(ctx, alice, v.ID, core.RepairRequest{})
	wantCode(t, err, errcode.ChangeBusy)
	if n := f.count("SELECT count(*) FROM core_task_runs WHERE change_id = ? AND op = ?", v.ID, core.TaskOpRecover); n != 1 {
		t.Errorf("recover rows after Repair = %d, want 1", n)
	}
	f.restartTask()
	if err := f.eng.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateNeedsRepair || c.Owner != "" {
		t.Errorf("after the restart: %+v, %v; want needs_repair, unowned", c, err)
	}
	if n := f.count("SELECT count(*) FROM core_task_runs WHERE change_id = ? AND op = ?", v.ID, core.TaskOpRecover); n != 1 {
		t.Errorf("recover rows after Recover = %d, want 1", n)
	}
}

func TestTaskRecoverAttemptAfterAFailedApply(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	f.tsk.mu.Lock()
	f.tsk.recover = core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"value":"1","token":"tok-a"}`)}
	f.tsk.mu.Unlock()
	v := f.taskApplyStart(refTskPin, `{"name":"a","value":"1"}`)
	apply1, recover1 := TaskUnitName(v.ID, core.TaskOpApply, 1), TaskUnitName(v.ID, core.TaskOpRecover, 1)
	f.taskPoll(func() {
		f.units.set(apply1, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
		f.taskJournal(refTskPin, v.ID, "a", "1")
	})
	f.taskPoll(func() {
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowFailed, "", string(errcode.Unavailable), "the package mirror is unreachable")
		f.units.set(apply1, false)
	})
	deadline := time.Now().Add(10 * time.Second)
	for len(f.units.startedUnits()) < 2 {
		if time.Now().After(deadline) {
			t.Fatal("no recover unit was started after the apply failed")
		}
		time.Sleep(time.Millisecond)
	}
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateRecovering || c.Owner != core.OwnerModule ||
		f.taskRowState(v.ID, core.TaskOpRecover, 1) != taskRowStarting {
		t.Fatalf("after the failed apply: %+v, %v; want recovering, owned by module, a starting recover row", c, err)
	}
	f.taskPoll(func() {
		f.units.set(recover1, true)
		f.taskRowTo(v.ID, core.TaskOpRecover, 1, taskRowRunning, "", "", "")
	})
	outcome, err := json.Marshal(core.RecoveryOutcome{State: core.RecoveryCompleted, Output: json.RawMessage(`{"value":"1","token":"tok-a"}`)})
	if err != nil {
		t.Fatal(err)
	}
	f.taskPoll(func() {
		f.taskRowTo(v.ID, core.TaskOpRecover, 1, taskRowSucceeded, string(outcome), "", "")
		f.units.set(recover1, false)
	})
	done := f.taskUntil(v.ID, core.StateCompleted)
	if done.Owner != "" || string(done.Output) != `{"token":"[redacted]","value":"1"}` {
		t.Errorf("completed change = %+v (output %s), want unowned with the recorded output redacted", done, done.Output)
	}
	f.tsk.mu.Lock()
	recovers := f.tsk.recovers
	f.tsk.mu.Unlock()
	if recovers != 0 {
		t.Errorf("the recovery handler ran %d times in the agent, want never: it runs in its unit", recovers)
	}
	if got := f.units.startedUnits(); !reflect.DeepEqual(got, []string{apply1, recover1}) {
		t.Errorf("started units = %v, want %v", got, []string{apply1, recover1})
	}
}

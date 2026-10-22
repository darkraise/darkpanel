package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (f *fixture) uninstalling() int {
	f.t.Helper()
	return f.count("SELECT count(*) FROM core_meta WHERE key = ?", MetaUninstalling)
}

func TestBeginUninstallRefusesWhileWorkIsLive(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	err := BeginUninstall(ctx, f.db, false)
	wantCode(t, err, errcode.ChangeBusy)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "change "+v.ID+" is owned by engine") ||
		!strings.Contains(msg, "task unit "+name+" is running") {
		t.Errorf("refusal = %q, want it to name the owned change and the running unit", msg)
	}
	if n := f.uninstalling(); n != 0 {
		t.Errorf("a refused BeginUninstall left %d barrier rows", n)
	}
	for range 2 {
		if err := BeginUninstall(ctx, f.db, true); err != nil {
			t.Fatalf("BeginUninstall with stopTasks = %v, want nil", err)
		}
	}
	if n := f.uninstalling(); n != 1 {
		t.Errorf("barrier rows = %d, want 1", n)
	}
}

func TestUninstallingGatesTheEngine(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	input := json.RawMessage(`{"name":"c","value":"1"}`)
	spare := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	done := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err := BeginUninstall(ctx, f.db, false); err != nil {
		t.Fatalf("BeginUninstall with nothing running = %v, want nil", err)
	}
	gated := []struct {
		name string
		call func() error
	}{
		{"Plan", func() error { _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); return err }},
		{"Apply", func() error { _, err := f.eng.Apply(ctx, alice, spare.PlanID, core.ApplyOptions{}); return err }},
		{"Undo", func() error { _, err := f.eng.Undo(ctx, alice, done.ID, nil); return err }},
		{"Repair", func() error { _, err := f.eng.Repair(ctx, alice, done.ID, core.RepairRequest{}); return err }},
		{"Resolve", func() error {
			_, err := f.eng.Resolve(ctx, alice, done.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
			return err
		}},
	}
	for _, g := range gated {
		t.Run(g.name, func(t *testing.T) { wantCode(t, g.call(), errcode.Uninstalling) })
	}
	_, err := f.eng.Release(ctx, alice, done.ID, "the operator gave up", nil)
	var coded *errcode.Error
	if errors.As(err, &coded) && coded.Code == errcode.Uninstalling {
		t.Error("Release answered core.uninstalling; the barrier never refuses a release")
	}
	for range 2 {
		if err := AbortUninstall(ctx, f.db); err != nil {
			t.Fatalf("AbortUninstall = %v", err)
		}
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, input, core.PlanOptions{}); err != nil {
		t.Errorf("Plan after the abort = %v, want it admitted", err)
	}
}

func TestRunTaskFenceRefusesWhileUninstalling(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.RunTask(ctx, v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatalf("RunTask while uninstalling = %v, want nil", err)
	}
	f.tsk.mu.Lock()
	ran := len(f.tsk.envs)
	f.tsk.mu.Unlock()
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowStarting || ran != 0 {
		t.Errorf("row %s, Task ran %d times; want the row starting and no Task", got, ran)
	}
	if err := AbortUninstall(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if err := f.eng.RunTask(ctx, v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatal(err)
	}
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowSucceeded {
		t.Errorf("row after the abort = %s, want succeeded", got)
	}
}

func TestRecoverSkipsClaimsWhileUninstalling(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	// What a process that died during this apply leaves.
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	wantCode(t, BeginUninstall(ctx, f.db, false), errcode.ChangeBusy)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	f.restart()
	log := f.logged()
	f.recoverAll()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		t.Errorf("after recovery while uninstalling: %+v, %v; want the change left as it was", got, err)
	}
	if out := log.String(); !strings.Contains(out, "recovery claim skipped") || !strings.Contains(out, v.ID) {
		t.Errorf("log = %q, want the skipped claim of %s", out, v.ID)
	}
}

func TestAbortUninstallRecoversWhatStartupSkipped(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	// What a process that died during this apply leaves.
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	err := BeginUninstall(ctx, f.db, false)
	wantCode(t, err, errcode.ChangeBusy)
	if msg := errcode.From(err).Detail; !strings.Contains(msg, "change "+v.ID+" is owned by engine") ||
		!strings.Contains(msg, "a stopped process") {
		t.Errorf("refusal = %q, want it to name the stale owner of %s", msg, v.ID)
	}
	if err := f.eng.AbortUninstall(ctx); err != nil {
		t.Fatalf("AbortUninstall on the live engine = %v", err)
	}
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("after the abort: %+v, %v; want interrupted and unowned without another restart", got, err)
	}
	if n := f.uninstalling(); n != 0 {
		t.Errorf("barrier rows after the abort = %d, want 0", n)
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{}); err != nil {
		t.Errorf("Plan after the abort = %v, want it admitted", err)
	}
	if err := f.eng.AbortUninstall(ctx); err != nil {
		t.Errorf("a second AbortUninstall = %v, want nil", err)
	}
}

// skippedAtStart leaves a change owned by a stopped process, sets the
// barrier as uninstall --stop-tasks does, and restarts, so start-up recovery
// skips the change. It returns the change's id.
func (f *fixture) skippedAtStart() string {
	f.t.Helper()
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	f.recoverExec("UPDATE core_changes SET state = ?, recovery_owner = ? WHERE id = ?",
		core.StateApplying, core.OwnerEngine, v.ID)
	if err := BeginUninstall(ctx, f.db, true); err != nil {
		f.t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	got, err := f.eng.Change(ctx, v.ID)
	if err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		f.t.Fatalf("after start-up recovery under the barrier: %+v, %v; want the change left as it was", got, err)
	}
	return v.ID
}

func TestEngineNoticesTheBarrierClearedOnItsPoll(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.skippedAtStart()
	f.clock.Advance(uninstallPoll)
	if got, err := f.eng.Change(ctx, id); err != nil || got.State != core.StateApplying || got.Owner != core.OwnerEngine {
		t.Fatalf("after a poll with the barrier set: %+v, %v; want the change left as it was", got, err)
	}
	// darkpanel uninstall --abort clears the row with the package function,
	// which cannot reach the engine.
	if err := AbortUninstall(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(uninstallPoll)
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, id)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("a poll after the abort: %+v, %v; want interrupted and unowned without a restart", got, err)
	}
	f.eng.uninstallMu.Lock()
	left := len(f.eng.uninstallSkipped)
	f.eng.uninstallMu.Unlock()
	f.eng.timerMu.Lock()
	armed := f.eng.uninstallTimer != nil
	f.eng.timerMu.Unlock()
	if left != 0 || armed {
		t.Errorf("after the recovery: %d skipped ids recorded, poll armed %v; want none and no poll", left, armed)
	}
}

func TestAdmissionNoticesTheBarrierCleared(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	id := f.skippedAtStart()
	if err := AbortUninstall(ctx, f.db); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Plan(ctx, alice, refAdd, json.RawMessage(`{"name":"b","value":"1"}`), core.PlanOptions{}); err != nil {
		t.Fatalf("Plan after the abort = %v, want it admitted", err)
	}
	f.eng.recoveryPass.Wait()
	got, err := f.eng.Change(ctx, id)
	if err != nil || got.State != core.StateInterrupted || got.Owner != "" {
		t.Errorf("after the first admitted call: %+v, %v; want interrupted and unowned before any poll", got, err)
	}
}

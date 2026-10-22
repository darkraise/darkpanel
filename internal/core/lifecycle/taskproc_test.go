package lifecycle

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// taskProcess makes every unit the engine starts run the agent task
// process in this test: the unit is active while RunTask runs and inactive
// once it returns, as systemd shows a transient service.
func (f *taskFixture) taskProcess() {
	f.units.mu.Lock()
	defer f.units.mu.Unlock()
	f.units.onStart = func(name string, svc core.TransientService) {
		f.units.set(name, true)
		args := svc.ExecStart
		go func() {
			defer f.units.set(name, false)
			attempt, err := strconv.Atoi(args[5])
			if err == nil {
				err = f.eng.RunTask(context.Background(), args[3], args[4], attempt)
			}
			if err != nil {
				f.t.Errorf("RunTask %v: %v", args[3:], err)
			}
		}()
	}
}

func (f *taskFixture) taskPID(id, op string, attempt int) int {
	f.t.Helper()
	return f.count("SELECT coalesce(pid, 0) FROM core_task_runs WHERE change_id = ? AND op = ? AND attempt = ?", id, op, attempt)
}

func TestRunTaskAppliesAndUndoes(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	f.taskProcess()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	done := f.taskUntil(v.ID, core.StateCompleted)
	if f.tsk.get("a") != "1" || string(done.Output) != `{"token":"[redacted]","value":"1"}` {
		t.Errorf("after the apply unit: item a %q, output %s; want 1 and the task's output redacted", f.tsk.get("a"), done.Output)
	}
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowSucceeded || f.taskPID(v.ID, core.TaskOpApply, 1) != os.Getpid() {
		t.Errorf("apply row = %s with PID %d, want succeeded with this process's PID", got, f.taskPID(v.ID, core.TaskOpApply, 1))
	}
	f.tsk.mu.Lock()
	env := f.tsk.envs[0]
	f.tsk.mu.Unlock()
	if env.ChangeID != v.ID || env.Principal.Actor != alice.Actor || env.Principal.Scope != core.ScopeAdmin ||
		env.HostID != testHost || env.Agents != nil || env.Effects == nil {
		t.Errorf("Task's Env = change %q, principal %+v, host %q, agents %v, effects %v; want the change, %s at admin, %s, no agents, a journal",
			env.ChangeID, env.Principal, env.HostID, env.Agents, env.Effects, alice.Actor, testHost)
	}
	effs, err := f.eng.Effects(ctx, v.ID)
	if err != nil || len(effs) != 1 || effs[0].State != core.EffectMade {
		t.Fatalf("effects = %+v, %v; want one made effect", effs, err)
	}
	u, err := f.eng.Undo(ctx, alice, v.ID, nil)
	if err != nil || u.State != core.StateUndoing {
		t.Fatalf("Undo = %+v, %v; want undoing", u, err)
	}
	f.taskUntil(v.ID, core.StateReverted)
	if f.tsk.get("a") != "" || f.taskRowState(v.ID, core.TaskOpUndo, 1) != taskRowSucceeded {
		t.Errorf("after the undo unit: item a %q, undo row %s; want empty and succeeded",
			f.tsk.get("a"), f.taskRowState(v.ID, core.TaskOpUndo, 1))
	}
}

func TestRunTaskRecordsAFailureAndRecovers(t *testing.T) {
	f := newTaskFixture(t)
	f.taskProcess()
	f.tsk.mu.Lock()
	f.tsk.recover = core.RecoveryOutcome{State: core.RecoveryRolledBack}
	f.tsk.mu.Unlock()
	v := f.taskApplyStart(refTskPin, `{"name":"a","value":"1","fail":true}`)
	done := f.taskUntil(v.ID, core.StateReverted)
	if done.Owner != "" {
		t.Errorf("reverted change owned by %q, want unowned", done.Owner)
	}
	var code, msg string
	if err := f.db.Read().QueryRow(`SELECT error_code, error_message FROM core_task_runs
		WHERE change_id = ? AND op = ? AND attempt = 1`, v.ID, core.TaskOpApply).Scan(&code, &msg); err != nil {
		t.Fatal(err)
	}
	if code != string(errcode.Unavailable) || msg != "the package mirror is unreachable" {
		t.Errorf("failed apply row = %s %q, want unavailable and the task's message", code, msg)
	}
	f.tsk.mu.Lock()
	recovers := f.tsk.recovers
	f.tsk.mu.Unlock()
	if recovers != 1 || f.taskRowState(v.ID, core.TaskOpRecover, 1) != taskRowSucceeded {
		t.Errorf("Recover ran %d times, recover row %s; want once in the recover unit, succeeded",
			recovers, f.taskRowState(v.ID, core.TaskOpRecover, 1))
	}
}

func TestRunTaskFence(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	f.recoverExec("UPDATE core_task_runs SET state = ? WHERE change_id = ?", taskRowCancelled, v.ID)
	if err := f.eng.RunTask(ctx, v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatalf("RunTask on a cancelled row = %v, want nil", err)
	}
	f.tsk.mu.Lock()
	ran := len(f.tsk.envs)
	f.tsk.mu.Unlock()
	if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowCancelled || ran != 0 {
		t.Errorf("row %s, Task ran %d times; want the cancelled row untouched and no Task", got, ran)
	}
	// The follower sees the row cancelled and fails the change, which frees
	// lock tsk for the next one.
	f.taskUntil(v.ID, core.StateFailed)

	w := f.taskApplyStart(refTskInstall, `{"name":"b","value":"1"}`)
	for range 2 {
		if err := f.eng.RunTask(ctx, w.ID, core.TaskOpApply, 1); err != nil {
			t.Fatalf("RunTask = %v", err)
		}
	}
	f.tsk.mu.Lock()
	ran = len(f.tsk.envs)
	f.tsk.mu.Unlock()
	if got := f.taskRowState(w.ID, core.TaskOpApply, 1); got != taskRowSucceeded || ran != 1 {
		t.Errorf("row %s, Task ran %d times; want succeeded by the first run alone", got, ran)
	}
	if s := f.state(w.ID); s != core.StateApplying {
		t.Errorf("change = %s, want applying: the task process never records the change", s)
	}
}

func TestRunTaskSealsItsOutput(t *testing.T) {
	f := newTaskFixture(t)
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	if err := f.eng.RunTask(context.Background(), v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatal(err)
	}
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT output_ct FROM core_task_runs WHERE change_id = ? AND op = ? AND attempt = 1",
		v.ID, core.TaskOpApply).Scan(&ct); err != nil {
		t.Fatal(err)
	}
	raw, err := coretest.Sealer{}.Open(taskOutputAD(v.ID, core.TaskOpApply, 1), ct)
	if err != nil {
		t.Fatalf("open with the row's associated data: %v", err)
	}
	var out tskOut
	if err := json.Unmarshal(raw, &out); err != nil || out != (tskOut{Value: "1", Token: "tok-a"}) {
		t.Errorf("sealed output = %s, %v; want the unredacted output", raw, err)
	}
	if _, err := (coretest.Sealer{}).Open(taskOutputAD(v.ID, core.TaskOpApply, 2), ct); err == nil {
		t.Error("the output opened with another attempt's associated data")
	}
}

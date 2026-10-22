package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Task unit polling (1.3c): the follower reads the row and the unit every
// TaskPoll, and fences a starting row whose unit stayed absent or inactive
// over two observations FenceGap apart.
const (
	TaskPoll = 2 * time.Second
	FenceGap = 10 * time.Second
)

// States of a core_task_runs row.
const (
	taskRowStarting  = "starting"
	taskRowRunning   = "running"
	taskRowSucceeded = "succeeded"
	taskRowFailed    = "failed"
	taskRowCancelled = "cancelled"
)

// TaskUnitName is the transient unit of one attempt of a task change's op;
// a task unit has no other name.
func TaskUnitName(changeID, op string, attempt int) string {
	return fmt.Sprintf("darkpanel-task-%s-%s-%d.service", changeID, op, attempt)
}

// taskOutputAD is the associated data of a task run's sealed output
// (ADR 0005).
func taskOutputAD(changeID, op string, attempt int) string {
	return fmt.Sprintf("core_task_runs.output_ct.%s.%s.%d", changeID, op, attempt)
}

// taskEnd is how one attempt ended: its row's state, its opened output when
// it succeeded, and the error any other end carries.
type taskEnd struct {
	Row    string
	Output json.RawMessage
	Err    *core.ChangeError
}

func taskCancelled() taskEnd {
	return taskEnd{Row: taskRowCancelled, Err: &core.ChangeError{
		Code: string(errcode.Unavailable), Message: "the task unit did not start",
	}}
}

type taskRow struct {
	state, code, message string
	output               []byte
	started              time.Time
}

// startTask inserts the starting row of op's next attempt, inside the
// transaction that claims the change for it. A new attempt follows only a
// terminal row, and a row is never reset; the caller has seen the previous
// attempt's unit inactive.
func (e *Engine) startTask(ctx context.Context, tx *sql.Tx, changeID, op string) (attempt int, err error) {
	var last int
	var state string
	err = tx.QueryRowContext(ctx,
		"SELECT attempt, state FROM core_task_runs WHERE change_id = ? AND op = ? ORDER BY attempt DESC LIMIT 1",
		changeID, op).Scan(&last, &state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return 0, err
	case state == taskRowStarting || state == taskRowRunning:
		return 0, errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("task unit %s has not finished", TaskUnitName(changeID, op, last)))
	}
	attempt = last + 1
	_, err = tx.ExecContext(ctx,
		"INSERT INTO core_task_runs (change_id, op, attempt, state, started_at) VALUES (?, ?, ?, ?, ?)",
		changeID, op, attempt, taskRowStarting, formatTime(e.cfg.Base.Clock.Now()))
	return attempt, err
}

// taskAttempt starts an attempt's unit while its row is still starting, then
// follows the attempt to its end. Starting it again after a restart is
// safe: the row's fence lets at most one process run the attempt.
func (e *Engine) taskAttempt(ctx context.Context, def core.ActionDef, changeID, op string, attempt int) (taskEnd, error) {
	if e.cfg.Units == nil {
		return taskEnd{}, fmt.Errorf("lifecycle: %s runs in a task unit, and this engine has no unit client", def.Ref())
	}
	r, err := e.readTaskRow(ctx, changeID, op, attempt)
	if err != nil {
		return taskEnd{}, err
	}
	if r.state == taskRowStarting {
		e.launchTask(ctx, def, changeID, op, attempt)
	}
	return e.followTask(ctx, changeID, op, attempt)
}

// launchTask starts the unit of a starting attempt. A start call that
// errors proves nothing, so the error is only logged: the start fence
// decides whether the attempt ever ran.
func (e *Engine) launchTask(ctx context.Context, def core.ActionDef, changeID, op string, attempt int) {
	name := TaskUnitName(changeID, op, attempt)
	err := e.cfg.Units.StartTransientService(ctx, name, core.TransientService{
		Description: fmt.Sprintf("Darkpanel %s %s of change %s", def.Ref(), op, changeID),
		ExecStart:   []string{e.cfg.TaskBinary, "agent", "task", changeID, op, strconv.Itoa(attempt)},
		RuntimeMax:  e.applyTimeout(def),
		CollectMode: "inactive-or-failed",
	})
	if err != nil {
		e.cfg.Base.Log.Warn("lifecycle: start task unit", "unit", name, "error", err)
	}
}

// followTask follows one attempt until 1.3c's sequence can decide it: the
// row records a result and the unit is inactive, the row is cancelled, or
// a running row's unit is inactive, so the process died; that row is then
// recorded failed, so a later attempt may follow it. A starting row whose
// unit stays absent or inactive with no queued job for FenceGap is fenced
// cancelled; when the fence matches no row, the process claimed the attempt
// in between, and the follower keeps waiting as for a running row. It acts
// only on what it read, and only Close stops it.
func (e *Engine) followTask(ctx context.Context, changeID, op string, attempt int) (taskEnd, error) {
	ctx = context.WithoutCancel(ctx)
	name := TaskUnitName(changeID, op, attempt)
	timeout, err := e.taskTimeout(ctx, changeID)
	if err != nil {
		return taskEnd{}, err
	}
	var gone time.Time
	for {
		r, err := e.readTaskRow(ctx, changeID, op, attempt)
		if err != nil {
			return taskEnd{}, err
		}
		inactive := false
		if st, err := e.cfg.Units.State(ctx, name); err != nil {
			e.cfg.Base.Log.Warn("lifecycle: read task unit", "unit", name, "error", err)
		} else {
			inactive = taskInactive(st)
		}
		now := e.cfg.Base.Clock.Now()
		switch {
		case r.state == taskRowCancelled:
			return taskCancelled(), nil
		case r.state == taskRowStarting && !inactive:
			gone = time.Time{}
		case r.state == taskRowStarting && gone.IsZero():
			gone = now
		case r.state == taskRowStarting && now.Sub(gone) >= FenceGap:
			fenced, err := e.taskRowUpdate(ctx,
				"UPDATE core_task_runs SET state = ?, finished_at = ? WHERE change_id = ? AND op = ? AND attempt = ? AND state = ?",
				taskRowCancelled, formatTime(now), changeID, op, attempt, taskRowStarting)
			if err != nil {
				return taskEnd{}, err
			}
			if fenced {
				return taskCancelled(), nil
			}
			gone = time.Time{}
		case r.state == taskRowStarting || !inactive:
		case r.state == taskRowSucceeded:
			var out []byte
			if len(r.output) > 0 {
				if out, err = e.cfg.Base.Sealer.Open(taskOutputAD(changeID, op, attempt), r.output); err != nil {
					return taskEnd{}, fmt.Errorf("lifecycle: open the output of %s: %w", name, err)
				}
			}
			return taskEnd{Row: taskRowSucceeded, Output: out}, nil
		case r.state == taskRowFailed:
			return taskEnd{Row: taskRowFailed, Err: &core.ChangeError{Code: r.code, Message: r.message}}, nil
		case r.state == taskRowRunning:
			ce := &core.ChangeError{Code: string(errcode.Internal), Message: "the task ended without recording a result"}
			if !now.Before(r.started.Add(timeout)) {
				ce = &core.ChangeError{
					Code: string(errcode.DeadlineExceeded), Message: fmt.Sprintf("the task did not finish within %s", timeout),
				}
			}
			dead, err := e.taskRowUpdate(ctx,
				`UPDATE core_task_runs SET state = ?, error_code = ?, error_message = ?, finished_at = ?
				 WHERE change_id = ? AND op = ? AND attempt = ? AND state = ?`,
				taskRowFailed, ce.Code, ce.Message, formatTime(now), changeID, op, attempt, taskRowRunning)
			if err != nil {
				return taskEnd{}, err
			}
			if dead {
				return taskEnd{Row: taskRowRunning, Err: ce}, nil
			}
		}
		if err := e.taskSleep(); err != nil {
			return taskEnd{}, err
		}
	}
}

// taskInactive is 1.3c's inactive: the unit is inactive, failed or not
// found (collected), with no job queued for it.
func taskInactive(st core.UnitState) bool {
	return st.PendingJob == "" &&
		(st.LoadState == "not-found" || st.ActiveState == "inactive" || st.ActiveState == "failed")
}

// taskSleep waits one TaskPoll on the engine's clock. Only Close ends the
// wait early.
func (e *Engine) taskSleep() error {
	done := make(chan struct{})
	t := e.cfg.Base.Clock.AfterFunc(TaskPoll, func() { close(done) })
	select {
	case <-done:
		return nil
	case <-e.runCtx.Done():
		t.Stop()
		return fmt.Errorf("lifecycle: stopped following a task unit: %w", e.runCtx.Err())
	}
}

func (e *Engine) readTaskRow(ctx context.Context, changeID, op string, attempt int) (taskRow, error) {
	var r taskRow
	var started string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT state, output_ct, error_code, error_message, started_at FROM core_task_runs
		 WHERE change_id = ? AND op = ? AND attempt = ?`, changeID, op, attempt).
		Scan(&r.state, &r.output, &r.code, &r.message, &started)
	if err != nil {
		return r, fmt.Errorf("lifecycle: read task run %s: %w", TaskUnitName(changeID, op, attempt), err)
	}
	r.started, err = parseTime(started)
	return r, err
}

// taskRowUpdate runs one conditional update of a task run and reports
// whether it matched the row.
func (e *Engine) taskRowUpdate(ctx context.Context, query string, args ...any) (bool, error) {
	var n int64
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, query, args...)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	if err != nil {
		return false, fmt.Errorf("lifecycle: update task run: %w", err)
	}
	return n == 1, nil
}

// taskTimeout is the ApplyTimeout of a change's action, which bounds each
// of its unit's attempts.
func (e *Engine) taskTimeout(ctx context.Context, changeID string) (time.Duration, error) {
	var ref core.ActionRef
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version FROM core_changes WHERE id = ?", changeID).Scan(&ref.Name, &ref.Version); err != nil {
		return 0, fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	}
	def, err := e.action(ref)
	if err != nil {
		return 0, err
	}
	return e.applyTimeout(def), nil
}

// taskInput opens a change's sealed plan input, which is kept while the
// change is unfinished (1.2, sealed input).
func (e *Engine) taskInput(ctx context.Context, changeID string) (json.RawMessage, error) {
	var planID string
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT p.id, p.input_ct FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?", changeID).
		Scan(&planID, &ct); err != nil {
		return nil, fmt.Errorf("lifecycle: read the plan input of %s: %w", changeID, err)
	}
	if ct == nil {
		return nil, fmt.Errorf("lifecycle: the plan input of %s is gone", changeID)
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(planID), ct)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: open the plan input of %s: %w", changeID, err)
	}
	return input, nil
}

// taskApply is the apply of a task action after its intent: attempt 1 of
// op apply runs in a unit, and the engine decides the change once the
// attempt has ended. The engine closing stops the follower and leaves the
// change owned; the next start re-claims it.
func (e *Engine) taskApply(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	end, err := e.taskAttempt(ctx, it.def, it.id, core.TaskOpApply, 1)
	if err != nil {
		return core.ChangeView{}, err
	}
	return e.taskApplyEnd(ctx, p, it.def, it.id, end)
}

// taskApplyEnd decides a task change from its apply attempt's end (1.3c,
// one sequence, steps 3 and 4): a success runs Verify in the agent and
// completes the change; any other end, and a failed Verify, settles the
// change's intended effects and follows 1.2's failure table, which runs
// the recovery handler of an action that has one.
func (e *Engine) taskApplyEnd(ctx context.Context, p core.Principal, def core.ActionDef, changeID string, end taskEnd) (core.ChangeView, error) {
	ctx = context.WithoutCancel(ctx)
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	env := e.changeEnv(p, def, changeID, call)
	timeout := e.applyTimeout(def)
	var cause error
	if end.Row == taskRowSucceeded {
		cause = e.taskVerify(def, env, changeID, end.Output)
		if cause == nil {
			state, err := e.recovererState(ctx, changeID)
			if err != nil {
				return core.ChangeView{}, err
			}
			tr := transition{
				from: state, to: core.StateCompleted, release: true, owner: recovererOwner(""),
				phase: phaseApply, result: resultOK, actor: p.Actor, source: p.Source,
			}
			if len(end.Output) > 0 {
				tr.output, tr.private = e.cfg.Redact(def.OutputType, end.Output), end.Output
			}
			return e.transition(ctx, changeID, tr)
		}
	} else {
		cause = errcode.New(errcode.Code(end.Err.Code), end.Err.Message)
	}
	sctx, cancel := context.WithTimeout(ctx, timeout)
	err = e.settle(sctx, env, def, changeID)
	cancel()
	if err != nil {
		return core.ChangeView{}, err
	}
	return e.failSettled(ctx, p, changeID, def, cause)
}

// taskVerify runs the action's Verify in the agent on the task's output,
// under the action's ApplyTimeout.
func (e *Engine) taskVerify(def core.ActionDef, env core.Env, changeID string, out json.RawMessage) error {
	ctx, cancel := context.WithTimeout(e.runCtx, e.applyTimeout(def))
	defer cancel()
	input, err := e.taskInput(ctx, changeID)
	if err != nil {
		return err
	}
	return e.protect(def.Ref(), "Verify", func() error { return def.Handler.Verify(ctx, env, input, out) })
}

// taskNewest is the attempt a re-claimed task change resumes: the newest
// undo attempt of an undoing change, else the newest recover attempt, else
// apply's.
func (e *Engine) taskNewest(ctx context.Context, changeID, state string) (op string, attempt int, err error) {
	ops := []string{core.TaskOpRecover, core.TaskOpApply}
	if state == core.StateUndoing {
		ops = []string{core.TaskOpUndo}
	}
	for _, op := range ops {
		var n sql.NullInt64
		if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
			"SELECT max(attempt) FROM core_task_runs WHERE change_id = ? AND op = ?", changeID, op).Scan(&n); err != nil {
			return "", 0, fmt.Errorf("lifecycle: read the task runs of %s: %w", changeID, err)
		}
		if n.Valid {
			return op, int(n.Int64), nil
		}
	}
	return "", 0, fmt.Errorf("lifecycle: change %s has no task run to resume", changeID)
}

// taskUndoCheck refuses the undo of a task change while this engine has no
// unit client, or while the unit of the change's last undo attempt is not
// inactive: a new attempt follows only an inactive unit (1.3c, results
// table). Changes of other actions pass.
func (e *Engine) taskUndoCheck(ctx context.Context, changeID string) error {
	f, err := loadFacts(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return err
	}
	def, err := e.action(f.ref())
	if err != nil || !def.Spec.Task {
		return nil
	}
	return e.taskLastInactive(ctx, changeID, core.TaskOpUndo)
}

// taskLastInactive refuses a new op attempt of a task change while this
// engine has no unit client, or while the unit of the change's newest op
// attempt is not inactive (1.3c, results table). A change with no op
// attempt passes.
func (e *Engine) taskLastInactive(ctx context.Context, changeID, op string) error {
	if e.cfg.Units == nil {
		return errcode.New(errcode.Internal, fmt.Sprintf("change %s runs in a task unit, and this engine has no unit client", changeID))
	}
	var n sql.NullInt64
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT max(attempt) FROM core_task_runs WHERE change_id = ? AND op = ?", changeID, op).Scan(&n); err != nil {
		return fmt.Errorf("lifecycle: read the %s runs of %s: %w", op, changeID, err)
	}
	if !n.Valid {
		return nil
	}
	name := TaskUnitName(changeID, op, int(n.Int64))
	st, err := e.cfg.Units.State(ctx, name)
	if err != nil {
		return fmt.Errorf("lifecycle: read task unit %s: %w", name, err)
	}
	if !taskInactive(st) {
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("task unit %s is still active", name))
	}
	return nil
}

// taskUndo runs a claimed undo of a task change in a unit: the newest undo
// attempt's unit runs the undo run from the journal, and once it has ended
// the engine records the undo settle. Any error before the attempt ends,
// the engine closing included, leaves the change undoing and owned, since
// its unit may still be running (1.3c, liveness gates); the next start
// resumes it.
func (e *Engine) taskUndo(ctx context.Context, p core.Principal, def core.ActionDef, changeID string) (core.ChangeView, error) {
	_, attempt, err := e.taskNewest(ctx, changeID, core.StateUndoing)
	if err != nil {
		return core.ChangeView{}, err
	}
	if _, err := e.taskAttempt(ctx, def, changeID, core.TaskOpUndo, attempt); err != nil {
		return core.ChangeView{}, err
	}
	sctx, cancel := e.cleanup()
	defer cancel()
	return e.undoSettle(sctx, p, changeID)
}

// taskRecover runs a task change's module recovery handler in its newest
// recover attempt's unit (1.3b, ownership). It answers the outcome the
// unit recorded, or, as herr, the error any other end carries. Any error
// before the attempt ends, the engine closing included, is stop: the
// change then stays owned, since its unit may still be running (1.3c,
// liveness gates), and the next start resumes it.
func (e *Engine) taskRecover(ctx context.Context, def core.ActionDef, changeID string) (out core.RecoveryOutcome, herr, stop error) {
	var n sql.NullInt64
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT max(attempt) FROM core_task_runs WHERE change_id = ? AND op = ?", changeID, core.TaskOpRecover).Scan(&n); err != nil {
		return out, nil, fmt.Errorf("lifecycle: read the recover runs of %s: %w", changeID, err)
	}
	if !n.Valid {
		return out, nil, fmt.Errorf("lifecycle: change %s has no recover attempt", changeID)
	}
	end, err := e.taskAttempt(ctx, def, changeID, core.TaskOpRecover, int(n.Int64))
	if err != nil {
		return out, nil, err
	}
	if end.Row != taskRowSucceeded {
		return out, errcode.New(errcode.Code(end.Err.Code), end.Err.Message), nil
	}
	if err := json.Unmarshal(end.Output, &out); err != nil {
		return out, fmt.Errorf("lifecycle: decode the recovery outcome of %s: %w", changeID, err), nil
	}
	return out, nil, nil
}

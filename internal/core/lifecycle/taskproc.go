package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/darkraise/darkpanel/internal/core"
)

// RunTask is the agent task process (1.3c, the agent task subcommand): it
// runs one attempt of a task change's op inside the attempt's transient
// unit. Its first act is the conditional update that moves exactly its row
// from starting to running; when that matches no row, the attempt was
// fenced or already ran, and RunTask does nothing. It records the op's
// result in the row and never changes the change: the agent decides the
// change once the unit is inactive.
func (e *Engine) RunTask(ctx context.Context, changeID, op string, attempt int) error {
	claimed, err := e.taskClaim(ctx, changeID, op, attempt)
	if err != nil {
		return err
	}
	if !claimed {
		e.cfg.Base.Log.Info("lifecycle: task attempt not run: its row is not starting, or the host is being uninstalled",
			"unit", TaskUnitName(changeID, op, attempt))
		return nil
	}
	ref, out, runErr := e.taskOp(ctx, changeID, op)
	return e.taskResult(changeID, op, attempt, ref, out, runErr)
}

// taskClaim is the start fence of the agent task process. It also refuses
// while the host is being uninstalled.
func (e *Engine) taskClaim(ctx context.Context, changeID, op string, attempt int) (bool, error) {
	return e.taskRowUpdate(ctx,
		`UPDATE core_task_runs SET state = ?, pid = ? WHERE change_id = ? AND op = ? AND attempt = ? AND state = ?
		 AND NOT EXISTS (SELECT 1 FROM core_meta WHERE key = ?)`,
		taskRowRunning, os.Getpid(), changeID, op, attempt, taskRowStarting, MetaUninstalling)
}

// taskOp runs op of the change under its action's ApplyTimeout with 1.3c's
// Env: the change's actor at scope admin, its host, its plan's call, and
// no host directory.
func (e *Engine) taskOp(ctx context.Context, changeID, op string) (core.ActionRef, json.RawMessage, error) {
	var ref core.ActionRef
	var actor, hostID string
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version, actor, host_id FROM core_changes WHERE id = ?", changeID).
		Scan(&ref.Name, &ref.Version, &actor, &hostID); err != nil {
		return ref, nil, fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	}
	def, err := e.action(ref)
	if err != nil {
		return ref, nil, err
	}
	if !def.Spec.Task {
		return ref, nil, fmt.Errorf("lifecycle: %s is not a task action", ref)
	}
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return ref, nil, err
	}
	env := e.changeEnv(core.Principal{Actor: actor, Scope: core.ScopeAdmin}, def, changeID, call)
	env.HostID, env.Agents = hostID, nil
	ctx, cancel := context.WithTimeout(ctx, e.applyTimeout(def))
	defer cancel()
	switch op {
	case core.TaskOpApply:
		input, err := e.taskInput(ctx, changeID)
		if err != nil {
			return ref, nil, err
		}
		var out json.RawMessage
		err = e.protect(ref, "Task", func() error {
			var err error
			out, err = def.Handler.Task(ctx, env, input)
			return err
		})
		return ref, out, err
	case core.TaskOpUndo:
		return ref, nil, e.undoRun(ctx, env, def, changeID)
	case core.TaskOpRecover:
		if def.Handler.Recover == nil {
			return ref, nil, fmt.Errorf("lifecycle: %s has no recovery handler", ref)
		}
		var ro core.RecoveryOutcome
		err := e.protect(ref, "Recover", func() error {
			var err error
			ro, err = def.Handler.Recover(ctx, env)
			return err
		})
		if err != nil {
			return ref, nil, err
		}
		out, err := json.Marshal(ro)
		return ref, out, err
	}
	return ref, nil, fmt.Errorf("lifecycle: unknown task op %q", op)
}

// taskResult records the op's result in its running row: succeeded with
// the output sealed, or failed with the error's code and client-safe
// message. It writes on a context of its own, so an op that ran out of time
// still records what happened.
func (e *Engine) taskResult(changeID, op string, attempt int, ref core.ActionRef, out json.RawMessage, runErr error) error {
	ctx, cancel := e.cleanup()
	defer cancel()
	now := formatTime(e.cfg.Base.Clock.Now())
	if runErr != nil {
		ce := e.changeError(ref, runErr)
		_, err := e.taskRowUpdate(ctx,
			`UPDATE core_task_runs SET state = ?, error_code = ?, error_message = ?, finished_at = ?
			 WHERE change_id = ? AND op = ? AND attempt = ? AND state = ?`,
			taskRowFailed, ce.Code, ce.Message, now, changeID, op, attempt, taskRowRunning)
		return err
	}
	var ct any
	if len(out) > 0 {
		sealed, err := e.cfg.Base.Sealer.Seal(taskOutputAD(changeID, op, attempt), out)
		if err != nil {
			return fmt.Errorf("lifecycle: seal the output of %s: %w", TaskUnitName(changeID, op, attempt), err)
		}
		ct = sealed
	}
	_, err := e.taskRowUpdate(ctx,
		`UPDATE core_task_runs SET state = ?, output_ct = ?, finished_at = ?
		 WHERE change_id = ? AND op = ? AND attempt = ? AND state = ?`,
		taskRowSucceeded, ct, now, changeID, op, attempt, taskRowRunning)
	return err
}

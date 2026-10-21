package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRecovery is the actor of what startup recovery does.
const ActorRecovery = "system:recovery"

var recoveryPrincipal = core.Principal{Actor: ActorRecovery, Scope: core.ScopeAdmin}

// Recover settles every change a previous run left unfinished and releases
// the locks of finished changes (ADR 0004 startup recovery). Run it once,
// before the engine serves requests. Every unconfirmed revertible change is
// reverted from its snapshot whatever its deadline; a compensable or
// irreversible change cut off during apply is marked interrupted with what its
// verify reports, and is never retried or compensated. Recover first seeds the
// engine's ids from the stored ones, so new ids sort after them even when the
// clock stepped back across the restart.
func (e *Engine) Recover(ctx context.Context) error {
	if err := e.seedIDs(ctx); err != nil {
		return err
	}
	type unfinished struct{ id, class, state string }
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
		"SELECT id, class, state FROM core_changes WHERE state IN (?, ?) ORDER BY id",
		core.StateApplying, core.StatePending)
	if err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}
	var todo []unfinished
	for rows.Next() {
		var u unfinished
		if err := rows.Scan(&u.id, &u.class, &u.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
		}
		todo = append(todo, u)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}

	var errs []error
	for _, u := range todo {
		// A restart ends in-process ownership (spec 1.3), so every recorded
		// outcome clears the owner the previous process left.
		tr := transition{
			from: u.state, to: core.StateReverted, release: true, owner: newOwner(""),
			phase: phaseRecover, result: resultOK, actor: ActorRecovery,
		}
		if core.Class(u.class) == core.ClassRevertible {
			tr.giveBack = true
			if _, err := e.runRevert(ctx, recoveryPrincipal, u.id); err != nil {
				e.cfg.Base.Log.Error("lifecycle: recovery revert", "change", u.id, "error", err)
				tr.to, tr.err, tr.result, tr.giveBack = core.StateFailed, revertFailed(), resultError, false
			}
		} else {
			tr.to, tr.phase, tr.result = core.StateInterrupted, phaseInterrupt, resultError
			tr.err = &core.ChangeError{
				Code:    string(errcode.Internal),
				Message: "interrupted by a restart during apply; " + e.verifyStored(ctx, u.id),
			}
		}
		if _, err := e.transition(ctx, u.id, tr); err != nil {
			errs = append(errs, err)
		}
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"DELETE FROM core_locks WHERE owner IN (SELECT id FROM core_changes WHERE state NOT IN (?, ?))",
			core.StateApplying, core.StatePending)
		return err
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("lifecycle: release finished locks: %w", err))
	}
	return errors.Join(errs...)
}

// verifyStored runs the action's Verify against the plan's stored input, so
// the operator learns whether the interrupted mutation took effect.
func (e *Engine) verifyStored(ctx context.Context, changeID string) string {
	var action, planID string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT c.action, c.version, p.id, p.input_ct FROM core_changes c
		 JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?`, changeID).
		Scan(&action, &version, &planID, &ct); err != nil {
		return "verify not run: the stored input is unreadable"
	}
	def, ok := e.cfg.Registry.Action(core.ActionRef{Name: action, Version: version})
	if !ok {
		return "verify not run: the action is not registered"
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(planID), ct)
	if err != nil {
		return "verify not run: the stored input does not open"
	}
	env := e.changeEnv(recoveryPrincipal, def, changeID, core.CallContext{})
	return e.verifyRecovery(ctx, def, env, input, json.RawMessage("null"))
}

// Purge deletes expired plans that never became a change. A plan with a change
// is kept, so a repeated apply still returns that change (spec section 3).
func (e *Engine) Purge(ctx context.Context) error {
	// An apply that loaded a plan before it expired inserts its change later;
	// deleting the plan in between would fail that insert's foreign key.
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	now := formatTime(e.cfg.Base.Clock.Now())
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM core_plans WHERE expires_at <= ?
			 AND NOT EXISTS (SELECT 1 FROM core_changes WHERE plan_id = core_plans.id)`, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: purge plans: %w", err)
	}
	return nil
}

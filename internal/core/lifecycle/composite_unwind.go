package lifecycle

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// unwindFrom starts unwinding composite id from step ordinal down (1.3
// Unwind). The start check comes first: a child that cannot be undone
// cleanly stops the composite in needs_repair before anything is undone, and
// a composite with nothing to undo is failed. Otherwise the direction turns
// to unwind and the composite is undoing, in one transaction.
func (e *Engine) unwindFrom(ctx context.Context, id string, ordinal int) error {
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return err
	}
	ordinal = min(ordinal, len(steps)-1)
	block, why, todo, err := e.unwindBlocker(ctx, c, steps, ordinal)
	switch {
	case err != nil:
		return err
	case block != nil:
		return e.compositeNeedsRepair(ctx, c, *block, why)
	case !todo:
		if _, err := e.transition(ctx, id, transition{
			from: c.State, to: core.StateFailed, owner: newOwner(""), release: true,
			phase: phaseApply, result: resultError, actor: c.Actor,
		}); err != nil {
			return err
		}
		return errCompositeDone
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE core_composites SET direction = ?, cursor = ? WHERE change_id = ?",
			dirUnwind, ordinal, id); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, "UPDATE core_changes SET state = ?, updated_at = ? WHERE id = ? AND state = ?",
			core.StateUndoing, formatTime(e.cfg.Base.Clock.Now()), id, c.State)
		if err != nil {
			return err
		}
		if n, err := res.RowsAffected(); err != nil || n != 1 {
			if err == nil {
				err = errcode.New(errcode.ChangeNotPending, fmt.Sprintf("composite %s is no longer %s", id, c.State))
			}
			return err
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("lifecycle: start the unwind of %s: %w", id, err)
	}
	return nil
}

// unwindBlocker is the start check over the steps from ordinal down: it
// looks up every applied step's child and returns the first step whose child
// has an unresolved effect, is in needs_repair, kept or abandoned, or is
// owned, with why. todo reports whether any child has something to undo. An
// absent plan is retired.
func (e *Engine) unwindBlocker(ctx context.Context, c compositeRow, steps []stepRow, ordinal int) (*stepRow, *core.ChangeError, bool, error) {
	kids, err := e.kids()
	if err != nil {
		return nil, nil, false, err
	}
	todo := false
	for i := ordinal; i >= 0; i-- {
		s := steps[i]
		if s.PlanID == "" || e.isReadStep(s) || s.State == core.StepUndone {
			continue
		}
		lk, err := lookupChild(ctx, kids, s)
		if err == nil && lk.Status == core.LookupAbsent {
			lk, err = retireChild(ctx, kids, s)
		}
		if err != nil {
			return nil, nil, false, e.childUnreachable(ctx, c, s, err)
		}
		switch {
		case lk.Status == core.LookupRetired, lk.State == core.StateReverted:
			continue
		case lk.Unresolved > 0, lk.Owner != "", lk.State == core.StateNeedsRepair,
			lk.State == core.StateKept, lk.State == core.StateAbandoned:
			return &steps[i], stepFailure(s, fmt.Sprintf("child %s cannot be undone cleanly: it is %s, owned by %q, with %d unresolved effects",
				lk.ChangeID, lk.State, lk.Owner, lk.Unresolved)), false, nil
		case (lk.State == core.StateFailed || lk.State == core.StateInterrupted) && lk.Made == 0:
			continue
		}
		todo = true
	}
	return nil, nil, todo, nil
}

// unwindStep is one runner pass in the direction unwind, from the cursor
// backwards: a read step is skipped, a step never applied counts undone, and
// any other step is marked undoing before its child is looked up.
func (e *Engine) unwindStep(ctx context.Context, c compositeRow, steps []stepRow) error {
	if c.Cursor < 0 {
		_, err := e.transition(ctx, c.ID, transition{
			from: c.State, to: core.StateReverted, owner: newOwner(""), release: true,
			phase: phaseApply, result: resultError, actor: c.Actor,
		})
		if err != nil {
			return err
		}
		return errCompositeDone
	}
	s := steps[c.Cursor]
	prev := c.Cursor - 1
	switch {
	case e.isReadStep(s), s.State == core.StepUndone:
		return e.moveCursor(ctx, c.ID, prev)
	case s.State == core.StepPending, s.State == core.StepPlanning:
		return e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{State: core.StepUndone, Cursor: &prev})
	case s.State == core.StepUndoing:
		kids, err := e.kids()
		if err != nil {
			return err
		}
		lk, err := lookupChild(ctx, kids, s)
		if err != nil {
			return e.childUnreachable(ctx, c, s, err)
		}
		return e.observeChild(ctx, c.ID, lk)
	}
	return e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{State: core.StepUndoing})
}

func (e *Engine) moveCursor(ctx context.Context, id string, to int) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE core_composites SET cursor = ? WHERE change_id = ?", to, id)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: move the cursor of %s: %w", id, err)
	}
	return nil
}

// unwindObserve is 1.3's table for the direction unwind.
func (e *Engine) unwindObserve(ctx context.Context, c compositeRow, s stepRow, lk core.PlanLookup) error {
	prev := s.Ordinal - 1
	undone := func() error {
		return e.setStep(ctx, c.ID, s.ID, []string{core.StepUndoing}, stepUpdate{State: core.StepUndone, Cursor: &prev})
	}
	switch {
	case lk.Status == core.LookupRetired:
		return undone()
	case lk.Status == core.LookupRunning, lk.Status == core.LookupRecovering:
		return waitChild(ChildPoll)
	case lk.Unresolved > 0:
		return e.compositeNeedsRepair(ctx, c, s,
			stepFailure(s, fmt.Sprintf("child %s has %d unresolved effects", lk.ChangeID, lk.Unresolved)))
	}
	switch lk.State {
	case core.StateCompleted:
		return e.sendUndo(ctx, c, s)
	case core.StateFailed, core.StateInterrupted:
		if lk.Made == 0 {
			return undone()
		}
		return e.sendUndo(ctx, c, s)
	case core.StatePending:
		return e.sendRevert(ctx, c, s, recoveryPrincipal)
	case core.StateReverted:
		return undone()
	case core.StateConfirmed:
		return e.confirmationWon(ctx, c, s)
	}
	return e.compositeNeedsRepair(ctx, c, s, childFailure(s, lk))
}

// sendUndo undoes step s's child through the undo route as system:recovery
// with the composite as its parent. A refusal saying the child cannot be
// undone stops the unwind; anything else is settled by the next lookup.
func (e *Engine) sendUndo(ctx context.Context, c compositeRow, s stepRow) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	v, err := kids.Undo(ctx, recoveryPrincipal, s.Host, s.ChildID, e.stepCall(c, s))
	switch code := errcode.From(err).Code; {
	case err == nil && v.State == core.StateReverted:
		prev := s.Ordinal - 1
		return e.setStep(ctx, c.ID, s.ID, []string{core.StepUndoing}, stepUpdate{State: core.StepUndone, Cursor: &prev})
	case err == nil:
		return waitChild(ChildPoll)
	case code == errcode.UndoConflict, code == errcode.LockHeld, code == errcode.NeedsRepair:
		return e.compositeNeedsRepair(ctx, c, s, &core.ChangeError{Code: string(code),
			Message: fmt.Sprintf("step %s: the undo of child %s was refused: %s", s.ID, s.ChildID, errcode.From(err).Detail)})
	case code == errcode.HostOffline:
		return e.childUnreachable(ctx, c, s, err)
	}
	return waitChild(ChildPoll)
}

// sendRevert reverts the pending child of the revertible last step; the
// unwind waits for the lookup to show it reverted.
func (e *Engine) sendRevert(ctx context.Context, c compositeRow, s stepRow, p core.Principal) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	v, err := kids.Revert(ctx, p, s.ChildID)
	switch {
	case err == nil && v.State == core.StateReverted:
		prev := s.Ordinal - 1
		return e.setStep(ctx, c.ID, s.ID, []string{core.StepUndoing}, stepUpdate{State: core.StepUndone, Cursor: &prev})
	case errcode.From(err).Code == errcode.HostOffline:
		return e.childUnreachable(ctx, c, s, err)
	}
	return waitChild(ChildPoll)
}

// confirmationWon ends an unwind whose revertible child was confirmed first:
// the step is confirmed, the direction is forward again past the last step,
// and the stored cause is cleared, so the next pass completes the composite.
func (e *Engine) confirmationWon(ctx context.Context, c compositeRow, s stepRow) error {
	now := formatTime(e.cfg.Base.Clock.Now())
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE core_composite_steps SET state = ?, updated_at = ? WHERE change_id = ? AND step_id = ?",
			core.StepConfirmed, now, c.ID, s.ID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE core_composites SET direction = ?, cursor = ? WHERE change_id = ?",
			dirForward, s.Ordinal+1, c.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET state = ?, error_code = NULL, error_message = NULL, error_cause = NULL, updated_at = ?
			 WHERE id = ?`, core.StateApplying, now, c.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: record the confirmation of %s: %w", c.ID, err)
	}
	e.publishStep(c.ID, s.ID, core.StepConfirmed, s.ChildID)
	return nil
}

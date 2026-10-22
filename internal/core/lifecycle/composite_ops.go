package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// noStepsDeadline is the steps deadline of a repaired composite: repair has
// none (1.3 Deadlines).
const noStepsDeadline = "9999-12-31T23:59:59.000000000Z"

// The values of RepairRequest.As.
const (
	decideReverted = "reverted"
	decideApplied  = "applied"
)

// compositeUnowned refuses an owned composite (core.change_busy) and one not
// in needs_repair (change.not_pending).
func compositeUnowned(c compositeRow) error {
	switch {
	case c.Owner != "":
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("composite %s is owned by %s", c.ID, c.Owner))
	case c.State != core.StateNeedsRepair:
		return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("composite %s is %s", c.ID, c.State))
	}
	return nil
}

// claimComposite claims needs_repair composite id with owner composite in
// state to, and runs more in the same transaction.
func (e *Engine) claimComposite(ctx context.Context, id, to string, more func(tx *sql.Tx) error) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := uninstallingErr(ctx, tx); err != nil {
			return err
		}
		if err := claim(ctx, tx, e.cfg.Base.Clock.Now(), id, "", core.OwnerComposite, core.StateNeedsRepair, to); err != nil {
			return err
		}
		return more(tx)
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: claim composite %s: %w", id, err)
	}
	return nil
}

// repairComposite is the repair of a composite (1.3 Repair of a
// composite), claimed with owner composite. A reverse composite resumes its
// unwind from the cursor, after the same start check, and never applies
// anything. A forward composite continues from the cursor step: a child that
// took effect advances it, a child that did not makes a new attempt planned
// by p, and a child in doubt is refused. The runner continues in the
// background.
func (e *Engine) repairComposite(ctx context.Context, p core.Principal, id string, req core.RepairRequest) (core.RepairResult, error) {
	ctx = context.WithoutCancel(ctx)
	if len(req.Input) > 0 {
		return core.RepairResult{}, errcode.New(errcode.InvalidInput, "a composite's repair takes no input")
	}
	if req.Step != "" || req.As != "" {
		return e.decideStep(ctx, p, id, req.Step, req.As)
	}
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return core.RepairResult{}, err
	}
	if err := compositeUnowned(c); err != nil {
		return core.RepairResult{}, err
	}
	def, err := e.compositeDef(c)
	if err != nil {
		return core.RepairResult{}, err
	}
	if def.Spec.Unwind == core.UnwindReverse {
		err = e.repairReverse(ctx, c, steps)
	} else {
		err = e.repairForward(ctx, c, steps)
	}
	switch {
	case errors.Is(err, errCompositeDone):
	case err != nil:
		return core.RepairResult{}, err
	default:
		run := &compositeRun{P: p}
		e.goRun(func() { e.runComposite(id, run) })
	}
	v, err := e.Change(ctx, id)
	if err != nil {
		return core.RepairResult{}, err
	}
	return core.RepairResult{Change: &v}, nil
}

func (e *Engine) repairReverse(ctx context.Context, c compositeRow, steps []stepRow) error {
	cursor := min(c.Cursor, len(steps)-1)
	block, why, _, err := e.unwindBlocker(ctx, c, steps, cursor)
	switch {
	case err != nil:
		return err
	case block != nil:
		return errcode.New(errcode.NeedsRepair, why.Message+"; repair the child or decide the step first")
	}
	return e.claimComposite(ctx, c.ID, core.StateUndoing, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "UPDATE core_composites SET direction = ?, cursor = ? WHERE change_id = ?",
			dirUnwind, cursor, c.ID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET error_code = CASE WHEN error_cause IN (?, ?) THEN error_cause ELSE ? END,
			 error_cause = NULL WHERE id = ?`,
			string(errcode.ChildFailed), string(errcode.DeadlineExceeded), string(errcode.ChildFailed), c.ID)
		return err
	})
}

func (e *Engine) repairForward(ctx context.Context, c compositeRow, steps []stepRow) error {
	claimForward := func() error {
		return e.claimComposite(ctx, c.ID, core.StateApplying, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE core_composites SET steps_deadline = ? WHERE change_id = ?",
				noStepsDeadline, c.ID); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx,
				"UPDATE core_changes SET error_code = NULL, error_message = NULL, error_cause = NULL WHERE id = ?", c.ID)
			return err
		})
	}
	if c.Cursor >= len(steps) {
		return claimForward()
	}
	s := steps[c.Cursor]
	if s.State == core.StepPending || s.State == core.StepPlanning {
		return claimForward()
	}
	kids, err := e.kids()
	if err != nil {
		return err
	}
	lk, err := lookupChild(ctx, kids, s)
	if err == nil && lk.Status == core.LookupAbsent {
		lk, err = retireChild(ctx, kids, s)
	}
	if err != nil {
		return err
	}
	advance := false
	switch {
	case lk.Status == core.LookupRunning, lk.Status == core.LookupRecovering:
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("child %s of step %s is still running", lk.ChangeID, s.ID))
	case lk.Status == core.LookupRetired:
	case lk.Unresolved > 0:
		return errcode.New(errcode.NeedsRepair, fmt.Sprintf(
			"child %s of step %s has %d unresolved effects; repair it first, or decide the step", lk.ChangeID, s.ID, lk.Unresolved))
	case lk.State == core.StateCompleted, lk.State == core.StateConfirmed,
		lk.State == core.StateRepaired && lk.RepairOutcome == core.RepairKept:
		advance = true
	case lk.State == core.StateReverted, lk.State == core.StateRepaired,
		lk.State == core.StateFailed, lk.State == core.StateInterrupted:
	default:
		return errcode.New(errcode.NeedsRepair, fmt.Sprintf(
			"child %s of step %s is %s; repair it first, or decide the step", lk.ChangeID, s.ID, lk.State))
	}
	if advance {
		return claimForward()
	}
	earlier := slices.Clone(s.Earlier)
	if child := compFirst(lk.ChangeID, s.ChildID); child != "" {
		earlier = append(earlier, child)
	}
	none := ""
	if err := e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{
		State: core.StepPending, PlanID: &none, ChildID: &none, Earlier: nonNil(earlier), ClearErr: true,
	}); err != nil {
		return err
	}
	return claimForward()
}

// compFirst returns the first of a and b that is not empty.
func compFirst(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// decideStep records an operator's decision on the cursor step of a
// composite in needs_repair (1.3 Step decision): reverted counts the step as
// undone (reverse) or not done (forward: the next repair makes a new
// attempt); applied counts it as done (forward only). It is allowed for a
// child kept or abandoned, and, while a reverse composite unwinds, for a
// child repaired or whose undo was refused with core.undo_conflict. The
// decision is journaled in the transaction that records it.
func (e *Engine) decideStep(ctx context.Context, p core.Principal, id, stepID, as string) (core.RepairResult, error) {
	invalid := func(format string, args ...any) (core.RepairResult, error) {
		return core.RepairResult{}, errcode.New(errcode.InvalidInput, fmt.Sprintf(format, args...))
	}
	if stepID == "" || (as != decideReverted && as != decideApplied) {
		return invalid("a step decision names a step and as reverted or applied")
	}
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return core.RepairResult{}, err
	}
	if err := compositeUnowned(c); err != nil {
		return core.RepairResult{}, err
	}
	i := slices.IndexFunc(steps, func(s stepRow) bool { return s.ID == stepID })
	switch {
	case i < 0:
		return invalid("composite %s has no step %s", id, stepID)
	case i != c.Cursor:
		return invalid("only the step at the cursor can be decided, not step %s", stepID)
	}
	s := steps[i]
	def, err := e.compositeDef(c)
	if err != nil {
		return core.RepairResult{}, err
	}
	reverse := def.Spec.Unwind == core.UnwindReverse
	if reverse && as == decideApplied {
		return invalid("applied is for forward composites only")
	}
	var lk core.PlanLookup
	if s.PlanID != "" {
		kids, err := e.kids()
		if err != nil {
			return core.RepairResult{}, err
		}
		if lk, err = lookupChild(ctx, kids, s); err != nil {
			return core.RepairResult{}, err
		}
	}
	terminal := lk.Status == core.LookupTerminal
	eligible := terminal && (lk.State == core.StateKept || lk.State == core.StateAbandoned)
	if reverse && c.Direction == dirUnwind {
		eligible = eligible || terminal && lk.State == core.StateRepaired ||
			s.Err != nil && s.Err.Code == string(errcode.UndoConflict)
	}
	if !eligible {
		return invalid("step %s can be decided only for a child kept or abandoned, or, while unwinding, repaired "+
			"or refused with core.undo_conflict; its child %s is %s", stepID, lk.ChangeID, lk.State)
	}
	state, cursor, clearChild, earlier := core.StepApplied, s.Ordinal+1, false, slices.Clone(s.Earlier)
	direction := c.Direction
	switch {
	case reverse:
		state, cursor, direction = core.StepUndone, s.Ordinal-1, dirUnwind
	case as == decideReverted:
		state, cursor, clearChild = core.StepPending, s.Ordinal, true
		if s.ChildID != "" {
			earlier = append(earlier, s.ChildID)
		}
	}
	earlierJSON, err := json.Marshal(nonNil(earlier))
	if err != nil {
		return core.RepairResult{}, err
	}
	input, err := json.Marshal(map[string]string{"step": stepID, "as": as})
	if err != nil {
		return core.RepairResult{}, err
	}
	now := e.cfg.Base.Clock.Now()
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var st, owner string
		if err := tx.QueryRowContext(ctx, "SELECT state, recovery_owner FROM core_changes WHERE id = ?", id).
			Scan(&st, &owner); err != nil {
			return err
		}
		if st != core.StateNeedsRepair || owner != "" {
			return errcode.New(errcode.ChangeBusy, fmt.Sprintf("composite %s is %s, owned by %q", id, st, owner))
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_composite_steps SET state = ?, updated_at = ?, earlier_child_change_ids = ?,
			 child_plan_id = CASE WHEN ? THEN NULL ELSE child_plan_id END,
			 child_change_id = CASE WHEN ? THEN NULL ELSE child_change_id END,
			 error_code = NULL, error_message = NULL
			 WHERE change_id = ? AND step_id = ?`,
			state, formatTime(now), string(earlierJSON), clearChild, clearChild, id, stepID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE core_composites SET cursor = ?, direction = ? WHERE change_id = ?",
			cursor, direction, id); err != nil {
			return err
		}
		return e.cfg.Journal.Append(ctx, tx, core.JournalEntry{
			At: now, Actor: p.Actor, Action: c.Action, Version: c.Version, Phase: phaseResolve, ChangeID: id,
			Input: input, Result: resultOK, Source: p.Source,
		})
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return core.RepairResult{}, err
	case err != nil:
		return core.RepairResult{}, fmt.Errorf("lifecycle: record the decision on step %s of %s: %w", stepID, id, err)
	}
	child := s.ChildID
	if clearChild {
		child = ""
	}
	e.publishStep(id, stepID, state, child)
	v, err := e.Change(ctx, id)
	if err != nil {
		return core.RepairResult{}, err
	}
	return core.RepairResult{Change: &v}, nil
}

// releaseComposite gives up a composite in needs_repair (1.3 Release): it
// goes releasing with owner composite, keeping its composite lock, and the
// runner releases its children in needs_repair. A repeated release of an
// abandoned composite answers it.
func (e *Engine) releaseComposite(ctx context.Context, p core.Principal, id, reason string) (core.ReleaseResult, error) {
	ctx = context.WithoutCancel(ctx)
	if n := utf8.RuneCountInString(reason); n < 1 || n > 500 {
		return core.ReleaseResult{}, errcode.New(errcode.InvalidInput, "a release reason is 1 to 500 characters")
	}
	unlock := e.compositeLock(id)
	defer unlock()
	c, _, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	if c.State != core.StateAbandoned {
		if err := compositeUnowned(c); err != nil {
			return core.ReleaseResult{}, err
		}
		input, err := json.Marshal(map[string]string{"reason": reason})
		if err != nil {
			return core.ReleaseResult{}, err
		}
		now := e.cfg.Base.Clock.Now()
		err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
			if err := claim(ctx, tx, now, id, "", core.OwnerComposite, core.StateNeedsRepair, core.StateReleasing); err != nil {
				return err
			}
			return e.cfg.Journal.Append(ctx, tx, core.JournalEntry{
				At: now, Actor: p.Actor, Action: c.Action, Version: c.Version, Phase: phaseRelease, ChangeID: id,
				Input: input, Result: resultOK, Source: p.Source,
			})
		})
		var coded *errcode.Error
		switch {
		case errors.As(err, &coded):
			return core.ReleaseResult{}, err
		case err != nil:
			return core.ReleaseResult{}, fmt.Errorf("lifecycle: release composite %s: %w", id, err)
		}
		e.goRun(func() { e.runComposite(id, nil) })
	}
	v, err := e.Change(ctx, id)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	return core.ReleaseResult{Change: v, Abandoned: []core.EffectView{}}, nil
}

// releaseStep is one runner pass of a releasing composite: every child in
// needs_repair is released as system:recovery with the composite as its
// parent. A child that cannot answer now (its agent unreachable, an owner
// such as a running repair, a live task unit) is retried at the next poll.
// Once every child is settled, the step rows are released and the composite
// is abandoned, releasing its lock.
func (e *Engine) releaseStep(ctx context.Context, c compositeRow, steps []stepRow) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	pending := false
	for _, s := range steps {
		if s.PlanID == "" || e.isReadStep(s) || s.State == core.StepReleased {
			continue
		}
		lk, err := lookupChild(ctx, kids, s)
		if err == nil && lk.Status == core.LookupAbsent {
			lk, err = retireChild(ctx, kids, s)
		}
		switch {
		case err != nil:
			e.cfg.Base.Log.Debug("lifecycle: release: look a child up", "change", c.ID, "step", s.ID, "error", err)
			pending = true
			continue
		case lk.Status == core.LookupRetired:
			continue
		case lk.Status != core.LookupTerminal:
			pending = true
			continue
		case lk.State != core.StateNeedsRepair:
			continue
		}
		r, err := kids.Release(ctx, recoveryPrincipal, s.Host, lk.ChangeID, "released with composite "+c.ID, e.stepCall(c, s))
		if err != nil || r.Change.State != core.StateAbandoned {
			e.cfg.Base.Log.Debug("lifecycle: release a child", "change", c.ID, "step", s.ID, "error", err)
			pending = true
		}
	}
	if pending {
		return waitChild(ChildPoll)
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE core_composite_steps SET state = ?, updated_at = ? WHERE change_id = ?",
			core.StepReleased, formatTime(e.cfg.Base.Clock.Now()), c.ID)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: release the steps of %s: %w", c.ID, err)
	}
	for _, s := range steps {
		e.publishStep(c.ID, s.ID, core.StepReleased, s.ChildID)
	}
	if _, err := e.transition(ctx, c.ID, transition{
		from: core.StateReleasing, to: core.StateAbandoned, owner: newOwner(""), release: true,
		phase: phaseRelease, result: resultOK, actor: ActorRecovery,
	}); err != nil {
		return err
	}
	return errCompositeDone
}

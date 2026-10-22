package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const phaseUndo = "undo"

// changeFacts is what the operator routes (undo, resolve, release and
// repair) check before they act on a change.
type changeFacts struct {
	id, planID, action, state, owner string
	parent                           string // the composite this change is a step of; "" for a top-level change
	repairs                          string // the change this repair change repairs; "" otherwise
	class                            core.Class
	version                          int
	retry                            bool
}

func (f changeFacts) ref() core.ActionRef { return core.ActionRef{Name: f.action, Version: f.version} }

// loadFacts reads a change together with its plan's repairs column. A
// missing change is change.not_found.
func loadFacts(ctx context.Context, q queryer, changeID string) (changeFacts, error) {
	f := changeFacts{id: changeID}
	var class string
	var parent, repairs sql.NullString
	err := q.QueryRowContext(ctx,
		`SELECT c.plan_id, c.action, c.version, c.class, c.state, c.recovery_owner, c.parent_change_id,
		 c.recovery_retry, p.repairs
		 FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?`, changeID).
		Scan(&f.planID, &f.action, &f.version, &class, &f.state, &f.owner, &parent, &f.retry, &repairs)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return f, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return f, fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	}
	f.class, f.parent, f.repairs = core.Class(class), parent.String, repairs.String
	return f, nil
}

// unresolvedCount counts the unresolved effects of a change and, with
// repairs set, those of its failed repair changes too.
func unresolvedCount(ctx context.Context, q queryer, changeID string, repairs bool) (int, error) {
	query := "SELECT count(*) FROM core_effects WHERE state = ? AND (change_id = ?"
	args := []any{string(core.EffectUnresolved), changeID}
	if repairs {
		query += ` OR change_id IN (SELECT c.id FROM core_changes c JOIN core_plans p ON p.id = c.plan_id
		 WHERE p.repairs = ? AND c.state = ?)`
		args = append(args, changeID, core.StateFailed)
	}
	query += ")"
	var n int
	if err := q.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("lifecycle: count unresolved effects of %s: %w", changeID, err)
	}
	return n, nil
}

// journalChange appends a journal entry for a change inside tx. The entry
// carries input when it is set, and the change's stored redacted input
// otherwise.
func (e *Engine) journalChange(ctx context.Context, tx *sql.Tx, now time.Time, changeID string, p core.Principal,
	phase, result, errCode string, input json.RawMessage) error {
	var action, stored string
	var version int
	if err := tx.QueryRowContext(ctx, "SELECT action, version, input_json FROM core_changes WHERE id = ?", changeID).
		Scan(&action, &version, &stored); err != nil {
		return fmt.Errorf("lifecycle: read change %s for the journal: %w", changeID, err)
	}
	if input == nil {
		input = json.RawMessage(stored)
	}
	return e.cfg.Journal.Append(ctx, tx, core.JournalEntry{
		At: now, Actor: p.Actor, Action: action, Version: version, Phase: phase, ChangeID: changeID,
		Input: input, Result: result, ErrorCode: errCode, Source: p.Source,
	})
}

// observeCall runs the action's Observe for one effect under the action's
// ApplyTimeout.
func (e *Engine) observeCall(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) (core.Observation, error) {
	if def.Handler.Observe == nil {
		return core.Observation{}, fmt.Errorf("lifecycle: %s has no Observe handler", def.Ref())
	}
	ctx, cancel := context.WithTimeout(ctx, e.applyTimeout(def))
	defer cancel()
	var obs core.Observation
	err := e.protect(def.Ref(), "Observe", func() error {
		var err error
		obs, err = def.Handler.Observe(ctx, env, eff)
		return err
	})
	return obs, err
}

// inverseCall runs the action's Inverse for one effect under the action's
// ApplyTimeout.
func (e *Engine) inverseCall(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) (core.InverseResult, error) {
	ctx, cancel := context.WithTimeout(ctx, e.applyTimeout(def))
	defer cancel()
	var res core.InverseResult
	err := e.protect(def.Ref(), "Inverse", func() error {
		var err error
		res, err = def.Handler.Inverse(ctx, env, eff)
		return err
	})
	return res, err
}

// Undo undoes a compensable change from its effects journal, newest effect
// first (spec 1.2, Undo of a change). It answers the change in undoing as
// soon as the claim is committed and runs the undo on its own goroutine; a
// change already reverted is answered as it is. call is set only when a
// composite undoes its own step.
func (e *Engine) Undo(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error) {
	if err := e.admit(ctx); err != nil {
		return core.ChangeView{}, err
	}
	if err := e.taskUndoCheck(ctx, changeID); err != nil {
		return core.ChangeView{}, err
	}
	e.applyMu.Lock()
	v, def, started, err := e.claimUndo(ctx, p, changeID, call)
	e.applyMu.Unlock()
	if err != nil || !started {
		return v, err
	}
	e.goRun(func() { e.finishUndo(p, def, changeID) })
	return v, nil
}

// claimUndo is the claim of spec 1.2: the checks, the locks without a new
// epoch (a needs_repair change still holds them), state undoing with owner
// undo, and the journal entry, in one transaction. started is false when
// the change was already reverted.
func (e *Engine) claimUndo(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, core.ActionDef, bool, error) {
	var v core.ChangeView
	var def core.ActionDef
	started := false
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := uninstallingErr(ctx, tx); err != nil {
			return err
		}
		f, err := loadFacts(ctx, tx, changeID)
		if err != nil {
			return err
		}
		if def, err = e.action(f.ref()); err != nil {
			return err
		}
		if err := undoRefusal(ctx, tx, f, def, call); err != nil {
			return err
		}
		if f.state != core.StateReverted {
			key, err := windowOpen(ctx, tx, changeID)
			if err != nil {
				return err
			}
			if key != "" {
				return errcode.New(errcode.UndoConflict, fmt.Sprintf("lock %s was taken by a later change", key))
			}
			if _, err := takeLocks(ctx, tx, changeID, def.Spec.LockKeys, false); err != nil {
				return err
			}
			now := e.cfg.Base.Clock.Now()
			if err := claim(ctx, tx, now, changeID, "", core.OwnerUndo, f.state, core.StateUndoing); err != nil {
				return err
			}
			if def.Spec.Task {
				if _, err := e.startTask(ctx, tx, changeID, core.TaskOpUndo); err != nil {
					return err
				}
			}
			if err := e.journalChange(ctx, tx, now, changeID, p, phaseUndo, resultOK, "", nil); err != nil {
				return err
			}
			started = true
		}
		v, err = scanChange(tx.QueryRowContext(ctx, "SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID))
		return err
	})
	var coded *errcode.Error
	if err != nil && !errors.As(err, &coded) {
		err = fmt.Errorf("lifecycle: undo %s: %w", changeID, err)
	}
	return v, def, started && err == nil, err
}

// undoRefusal applies the eligibility rules of spec 1.2 in order. It
// answers nil for a change that may be undone and for one already
// reverted.
func undoRefusal(ctx context.Context, q queryer, f changeFacts, def core.ActionDef, call *core.CallContext) error {
	n, err := unresolvedCount(ctx, q, f.id, false)
	if err != nil {
		return err
	}
	switch {
	case n > 0:
		return errcode.New(errcode.NeedsRepair,
			fmt.Sprintf("change %s has an unresolved effect: resolve its effects first", f.id))
	case f.retry:
		return errcode.New(errcode.NeedsRepair,
			fmt.Sprintf("change %s: repair it: its recovery handler has not finished", f.id))
	case f.owner != "":
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is owned by %s", f.id, f.owner))
	case f.parent != "" && f.state != core.StateNeedsRepair && (call == nil || call.ParentChangeID != f.parent):
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s is a step of composite %s; undo or repair the composite", f.id, f.parent))
	case f.repairs != "":
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s repairs change %s; repair or release change %s", f.id, f.repairs, f.repairs))
	case f.class != core.ClassCompensable || def.Handler.Inverse == nil:
		return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s has no undo handler", f.id))
	}
	switch f.state {
	case core.StateReverted, core.StateCompleted, core.StateFailed, core.StateInterrupted, core.StateNeedsRepair:
		return nil
	}
	return errcode.New(errcode.ChangeNotPending, fmt.Sprintf(
		"change %s is %s; only a completed, failed, interrupted or needs_repair change is undone", f.id, f.state))
}

// finishUndo runs a claimed undo and records its outcome. A shutdown that
// cancels the run leaves the change undoing, and the next start resumes
// it from the journal.
func (e *Engine) finishUndo(p core.Principal, def core.ActionDef, changeID string) {
	if def.Spec.Task {
		if _, err := e.taskUndo(e.runCtx, p, def, changeID); err != nil {
			e.cfg.Base.Log.Error("lifecycle: task undo", "change", changeID, "error", err)
		}
		return
	}
	ctx := e.runCtx
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: undo: read the call context", "change", changeID, "error", err)
		return
	}
	runErr := e.undoRun(ctx, e.changeEnv(p, def, changeID, call), def, changeID)
	if ctx.Err() != nil {
		e.cfg.Base.Log.Info("lifecycle: undo stopped by shutdown; the next start resumes it", "change", changeID)
		return
	}
	if runErr != nil {
		e.cfg.Base.Log.Error("lifecycle: undo run", "change", changeID, "error", runErr)
	}
	sctx, cancel := e.cleanup()
	defer cancel()
	if _, err := e.undoSettle(sctx, p, changeID); err != nil {
		e.cfg.Base.Log.Error("lifecycle: undo settle", "change", changeID, "error", err)
	}
}

// undoRun is the run of spec 1.2: newest effect first, it stops at the
// first effect that does not end undone or skipped, so an earlier effect
// is never undone while a later one is in doubt or kept. It writes effect
// states only, each in its own committed transaction before the act it
// announces.
func (e *Engine) undoRun(ctx context.Context, env core.Env, def core.ActionDef, changeID string) error {
	effs, err := loadEffects(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return fmt.Errorf("lifecycle: read effects of %s: %w", changeID, err)
	}
	slices.SortFunc(effs, func(a, b core.Effect) int { return b.Seq - a.Seq })
	for _, eff := range effs {
		done, err := e.undoEffect(ctx, env, def, eff)
		if err != nil || !done {
			return err
		}
	}
	return nil
}

// undoEffect undoes one effect and reports whether it ended undone or
// skipped. An accepted, unresolved or abandoned effect stops the run.
func (e *Engine) undoEffect(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) (bool, error) {
	switch eff.State {
	case core.EffectUndone, core.EffectSkipped:
		return true, nil
	case core.EffectMade:
		return e.runInverse(ctx, env, def, eff)
	case core.EffectUndoing:
		next, err := e.resumeInverse(ctx, env, def, eff)
		if err != nil {
			return false, err
		}
		if err := e.writeEffect(ctx, eff, core.EffectUndoing, next); err != nil {
			return false, err
		}
		switch next.State {
		case core.EffectUndone:
			return true, nil
		case core.EffectMade:
			eff.State = core.EffectMade
			return e.runInverse(ctx, env, def, eff)
		}
	}
	return false, nil
}

// resumeInverse decides an effect a cut-off undo left undoing. Only an
// InverseLocal inverse can be judged by observing it: a remote one may
// still commit.
func (e *Engine) resumeInverse(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) (effectUpdate, error) {
	uncertain := effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonUncertain}
	if !eff.InverseLocal {
		return uncertain, nil
	}
	obs, err := e.observeCall(ctx, env, def, eff)
	if err != nil {
		if ctx.Err() != nil {
			return effectUpdate{}, ctx.Err()
		}
		return uncertain, nil
	}
	cur := obs.Current
	switch {
	case eff.UndoTo != nil && stateEqual(cur, *eff.UndoTo):
		return effectUpdate{State: core.EffectUndone}, nil
	// State only, as in runInverse: an earlier undo on the object gave it a new Version.
	case eff.Post != nil && stateEqual(cur, *eff.Post):
		return effectUpdate{State: core.EffectMade}, nil
	}
	return effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonAmbiguous, Current: &cur}, nil
}

// runInverse undoes one made effect and reports whether it ended undone.
func (e *Engine) runInverse(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) (bool, error) {
	obs, err := e.observeCall(ctx, env, def, eff)
	if err != nil {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}
		// Nothing is written yet: the effect stays made, the change goes to
		// needs_repair, and the operator can run the undo again.
		e.cfg.Base.Log.Warn("lifecycle: undo could not observe an effect",
			"change", eff.ChangeID, "seq", eff.Seq, "error", err)
		return false, nil
	}
	// State only: undoing a later change on the same object gives it a new
	// Version, which no inverse restores, so Version can never match Post.
	if eff.Post == nil || !stateEqual(obs.Current, *eff.Post) {
		cur := obs.Current
		return false, e.writeEffect(ctx, eff, core.EffectMade,
			effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &cur})
	}
	to := undoTo(eff)
	u := effectUpdate{State: core.EffectUndoing, UndoTo: &to}
	if !eff.InverseLocal {
		by := e.cfg.Base.Clock.Now().Add(eff.CommitWait)
		u.CommitBy = &by
	}
	if err := e.writeEffect(ctx, eff, core.EffectMade, u); err != nil {
		return false, err
	}
	eff.State, eff.UndoTo = core.EffectUndoing, &to
	res, invErr := e.inverseCall(ctx, env, def, eff)
	if ctx.Err() != nil {
		return false, ctx.Err()
	}
	if invErr != nil {
		e.cfg.Base.Log.Warn("lifecycle: an inverse failed", "change", eff.ChangeID, "seq", eff.Seq, "error", invErr)
	}
	next, err := e.afterInverse(ctx, env, def, eff, res, invErr)
	if err != nil {
		return false, err
	}
	return next.State == core.EffectUndone, e.writeEffect(ctx, eff, core.EffectUndoing, next)
}

// afterInverse maps an inverse's result to the effect's next state (the
// undoing rows of spec 1.2's effect state table). It errs only when the
// run is being shut down.
func (e *Engine) afterInverse(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect,
	res core.InverseResult, invErr error) (effectUpdate, error) {
	uncertain := effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonUncertain}
	switch {
	case invErr != nil && core.IsDefinite(invErr):
		return effectUpdate{State: core.EffectMade}, nil
	case invErr != nil && !eff.InverseLocal:
		return uncertain, nil
	}
	if res.Resource != "" {
		eff.UndoResource = res.Resource
	}
	next := uncertain
	obs, err := e.observeCall(ctx, env, def, eff)
	switch {
	case err != nil && ctx.Err() != nil:
		return effectUpdate{}, ctx.Err()
	case err != nil:
	case invErr == nil && stateEqual(obs.Current, *eff.UndoTo):
		next = effectUpdate{State: core.EffectUndone}
	case invErr == nil:
		cur := obs.Current
		next = effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonConflict, Current: &cur}
	// State only, as in runInverse: an earlier undo on the object gave it a new Version.
	case eff.Post != nil && stateEqual(obs.Current, *eff.Post):
		next = effectUpdate{State: core.EffectMade}
	default:
		cur := obs.Current
		next = effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonAmbiguous, Current: &cur}
	}
	if res.Resource != "" {
		r := res.Resource
		next.UndoResource = &r
	}
	return next, nil
}

// writeEffect commits one effect transition from the state from.
func (e *Engine) writeEffect(ctx context.Context, eff core.Effect, from core.EffectState, u effectUpdate) error {
	return e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		return setEffect(ctx, tx, e.cfg.Base.Clock.Now(), eff.ChangeID, eff.Seq, []core.EffectState{from}, u)
	})
}

// undoSettle records an undo run's outcome in one transaction and clears
// the owner (spec 1.2, Settle): reverted when every effect is undone or
// skipped, kept when the run stopped at an accepted effect after undoing
// every later one, and needs_repair, with the locks kept, otherwise.
func (e *Engine) undoSettle(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	effs, err := loadEffects(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.ChangeView{}, fmt.Errorf("lifecycle: read effects of %s: %w", changeID, err)
	}
	none := ""
	tr := transition{
		from: core.StateUndoing, to: core.StateReverted, release: true, giveBack: true, owner: &none,
		phase: phaseUndo, result: resultOK, actor: p.Actor, source: p.Source,
	}
	stop := undoStop(effs)
	switch {
	case stop == nil:
	case stop.State == core.EffectAccepted:
		tr.to, tr.giveBack = core.StateKept, false
	default:
		tr.to, tr.release, tr.giveBack, tr.keepLocks, tr.result = core.StateNeedsRepair, false, false, true, resultError
		tr.err = &core.ChangeError{Code: string(errcode.NeedsRepair), Message: undoStopMessage(effs, *stop)}
	}
	return e.transition(ctx, changeID, tr)
}

// undoStop is the newest effect that is neither undone nor skipped, or nil
// when the undo left none.
func undoStop(effs []core.Effect) *core.Effect {
	var stop *core.Effect
	for i := range effs {
		s := effs[i].State
		if s != core.EffectUndone && s != core.EffectSkipped && (stop == nil || effs[i].Seq > stop.Seq) {
			stop = &effs[i]
		}
	}
	return stop
}

// undoStopMessage names where an undo stopped and every effect left
// unresolved.
func undoStopMessage(effs []core.Effect, stop core.Effect) string {
	msg := fmt.Sprintf("the undo stopped at effect %d (%s), which is %s", stop.Seq, stop.Resource, stop.State)
	var unresolved []string
	for _, eff := range effs {
		if eff.State == core.EffectUnresolved {
			unresolved = append(unresolved, fmt.Sprintf("%d %s (%s)", eff.Seq, eff.Resource, eff.Reason))
		}
	}
	if len(unresolved) > 0 {
		msg += "; unresolved effects: " + strings.Join(unresolved, ", ")
	}
	return msg
}

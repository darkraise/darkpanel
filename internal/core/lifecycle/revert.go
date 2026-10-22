package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRevertTimer is the actor of a revert that a confirm timer triggers.
const ActorRevertTimer = "system:revert-timer"

// WatchdogExitCode is the status the process exits with when a revertible
// change is still applying at its revert deadline (spec 1.2): the unit's
// Restart=on-failure starts it again and startup recovery reverts the change.
const WatchdogExitCode = 70

// The journal phase of the revert watchdog and the result of a revert that
// overwrote unresolved effects (spec 1.2).
const (
	phaseWatchdog  = "revert-watchdog"
	resultOverride = "override"
)

var timerPrincipal = core.Principal{Actor: ActorRevertTimer, Scope: core.ScopeAdmin}

// armedTimer is a change's revert timer. Its callback compares the pointer
// with the map entry, so a timer that was stopped or replaced while its
// callback waited for timerMu does nothing.
type armedTimer struct{ core.Timer }

// Confirm keeps a pending revertible change and releases its locks. Any
// principal may confirm; the caller authorizes it. Confirm and revert race in
// one transaction each, and the one that commits second gets
// change.not_pending. On a composite it confirms the revertible child (1.3
// Confirmation).
func (e *Engine) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	composite, err := e.isComposite(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if composite {
		return e.confirmComposite(ctx, p, changeID)
	}
	v, err := e.transition(ctx, changeID, transition{
		from: core.StatePending, to: core.StateConfirmed, release: true,
		phase: phaseConfirm, result: resultOK, actor: p.Actor, source: p.Source,
	})
	if err != nil {
		return v, err
	}
	e.stopTimer(changeID)
	return v, nil
}

// Revert reverts a pending revertible change from its snapshot. Any principal
// may revert; the caller authorizes it. On a composite it reverts the
// revertible child and unwinds the steps before it.
func (e *Engine) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	composite, err := e.isComposite(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if composite {
		return e.revertComposite(ctx, p, changeID)
	}
	return e.revertPending(context.WithoutCancel(ctx), p, changeID)
}

// revertPending claims a pending change for the engine, moving it back to
// applying, so a racing confirm loses and a crash during the revert is
// recovered from the snapshot at the next start; then it reverts and records
// the outcome. The claim and the stop of the change's timer happen under
// timerMu, so the timer never mistakes this revert for a hung apply.
func (e *Engine) revertPending(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	e.timerMu.Lock()
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		return claim(ctx, tx, e.cfg.Base.Clock.Now(), changeID, "", core.OwnerEngine, core.StatePending, core.StateApplying)
	})
	if err == nil {
		e.stopTimerLocked(changeID)
	}
	e.timerMu.Unlock()
	if err != nil {
		return core.ChangeView{}, e.notPending(ctx, changeID, err)
	}
	def, err := e.changeDef(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	hctx, cancel := e.cleanupCall(def)
	out, revertErr := e.runRevert(hctx, p, changeID)
	cancel()
	// The recording transaction has a bound of its own (spec 1.3,
	// deadlines); an operator's or the timer's ctx carries none.
	tctx, cancelTx := context.WithTimeout(context.WithoutCancel(ctx), CleanupTxTimeout)
	defer cancelTx()
	return e.recordRevert(tctx, p, changeID, out, revertErr, transition{
		from: core.StateApplying, to: core.StateReverted, owner: newOwner(""), release: true, giveBack: true,
		phase: phaseRevert, result: resultOK, actor: p.Actor, source: p.Source,
	})
}

// notPending turns a failed claim of a change that is no longer pending into
// change.not_pending, as confirm and revert have always answered.
func (e *Engine) notPending(ctx context.Context, changeID string, err error) error {
	if errcode.From(err).Code != errcode.ChangeBusy {
		return err
	}
	if v, readErr := e.Change(ctx, changeID); readErr == nil && v.State != core.StatePending {
		return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", changeID, v.State))
	}
	return err
}

// changeDef returns the registered action of a stored change.
func (e *Engine) changeDef(ctx context.Context, changeID string) (core.ActionDef, error) {
	var action string
	var version int
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version FROM core_changes WHERE id = ?", changeID).Scan(&action, &version); err != nil {
		return core.ActionDef{}, fmt.Errorf("lifecycle: read action of %s: %w", changeID, err)
	}
	ref := core.ActionRef{Name: action, Version: version}
	def, ok := e.cfg.Registry.Action(ref)
	if !ok {
		return core.ActionDef{}, fmt.Errorf("lifecycle: action %s is not registered", ref)
	}
	return def, nil
}

// recordRevert records a revert in one transaction (spec 1.2, failure of a
// revertible change): it applies out.Effects, then maps the outcome through
// the four-row table. tr is the transition of a revert that restored
// everything (state reverted, locks released, epochs given back); every
// other outcome becomes needs_repair, keeps the locks and clears the owner.
func (e *Engine) recordRevert(ctx context.Context, p core.Principal, changeID string, out core.RevertOutcome, err error, tr transition) (core.ChangeView, error) {
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: revert", "change", changeID, "actor", p.Actor, "error", err)
	}
	var v core.ChangeView
	werr := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		now := e.cfg.Base.Clock.Now()
		if err == nil {
			for _, o := range out.Effects {
				if err := e.revertEffect(ctx, tx, now, changeID, o); err != nil {
					return err
				}
			}
		}
		effects, lerr := loadEffects(ctx, tx, changeID)
		if lerr != nil {
			return lerr
		}
		var left []string
		for _, eff := range effects {
			if !nothingLeft([]core.Effect{eff}) {
				left = append(left, fmt.Sprintf("%d %s (%s)", eff.Seq, eff.Resource, effectStatus(eff)))
			}
		}
		repair := func(msg, cause string) {
			// The failed revert of a failed apply is journaled as a revert,
			// and the apply's error, which the repair error replaces, stays in
			// the message: nothing else records why the apply failed.
			if tr.phase == phaseApply {
				tr.phase = phaseRevert
			}
			if tr.err != nil {
				msg += fmt.Sprintf("; the apply had failed first (%s: %s)", tr.err.Code, tr.err.Message)
			}
			tr.to, tr.release, tr.keepLocks, tr.giveBack, tr.result = core.StateNeedsRepair, false, true, false, resultError
			tr.err, tr.cause = &core.ChangeError{Code: string(errcode.NeedsRepair), Message: msg}, cause
		}
		switch {
		case err != nil:
			repair("the revert failed; the host may still be in the applied state", string(errcode.ChangeRevertFailed))
		case out.Override:
			repair("the revert restored the snapshot over unresolved effects to keep a protected flow open: "+
				strings.Join(left, ", ")+"; resolve them", "")
			tr.result, tr.override = resultOverride, out.Detail
			if len(tr.override) == 0 {
				tr.override = json.RawMessage("{}")
			}
		case len(left) > 0:
			repair("the revert left effects in place: "+strings.Join(left, ", "), string(errcode.ChangeRevertFailed))
		}
		var terr error
		v, terr = e.transitionTx(ctx, tx, changeID, tr)
		return terr
	})
	var coded *errcode.Error
	switch {
	case errors.As(werr, &coded):
		return v, werr
	case werr != nil:
		return v, fmt.Errorf("lifecycle: record the revert of %s: %w", changeID, werr)
	}
	return v, nil
}

// effectStatus is an effect's state, with its reason when unresolved.
func effectStatus(eff core.Effect) string {
	if eff.Reason != "" {
		return string(eff.State) + "/" + eff.Reason
	}
	return string(eff.State)
}

// revertEffect applies one EffectOutcome of a revert inside tx. A restored
// effect passes through undoing, so the journal keeps the state it was
// restored to. An outcome naming an effect the revert could not act on, one
// already finished, is a handler bug: it is logged and ignored.
func (e *Engine) revertEffect(ctx context.Context, tx *sql.Tx, now time.Time, changeID string, o core.EffectOutcome) error {
	var eff *core.Effect
	effects, err := loadEffects(ctx, tx, changeID)
	if err != nil {
		return err
	}
	for i := range effects {
		if effects[i].Seq == o.Seq {
			eff = &effects[i]
		}
	}
	switch {
	case eff == nil || effectTerminal(eff.State):
		e.cfg.Base.Log.Error("lifecycle: a revert reported an effect it cannot have acted on", "change", changeID, "effect", o.Seq)
		return nil
	case o.State == core.EffectUndone && eff.State == core.EffectMade:
		to := undoTo(*eff)
		if err := setEffect(ctx, tx, now, changeID, o.Seq, []core.EffectState{core.EffectMade},
			effectUpdate{State: core.EffectUndoing, UndoTo: &to}); err != nil {
			return err
		}
		return setEffect(ctx, tx, now, changeID, o.Seq, []core.EffectState{core.EffectUndoing}, effectUpdate{State: core.EffectUndone})
	case o.State == core.EffectUnresolved && eff.State == core.EffectUnresolved:
		cur, err := nullableJSON(o.Current)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, "UPDATE core_effects SET current_json = coalesce(?, current_json), updated_at = ? WHERE change_id = ? AND seq = ?",
			cur, formatTime(now), changeID, o.Seq)
		return err
	case o.State == core.EffectUnresolved:
		reason := o.Reason
		if reason != core.ReasonConflict {
			reason = core.ReasonUncertain
		}
		return setEffect(ctx, tx, now, changeID, o.Seq, []core.EffectState{core.EffectMade, core.EffectIntended},
			effectUpdate{State: core.EffectUnresolved, Reason: reason, Current: o.Current})
	}
	e.cfg.Base.Log.Error("lifecycle: a revert reported an effect state it cannot set", "change", changeID,
		"effect", o.Seq, "state", o.State, "from", eff.State)
	return nil
}

// armTimer arms changeID's revert timer to fire after d (ADR 0004). Apply
// arms it in the intent's transaction's wake, so it also fires while the
// change is still applying.
func (e *Engine) armTimer(changeID string, d time.Duration) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.closed {
		return
	}
	armed := &armedTimer{}
	armed.Timer = e.cfg.Base.Clock.AfterFunc(d, func() { e.timerFired(changeID, armed) })
	e.timers[changeID] = armed
}

// revertTimerRetry is how long a revert timer that could not read its
// change's state waits before it reads it again.
const revertTimerRetry = 10 * time.Second

// timerFired reverts a change still pending at its deadline. A change still
// applying has a handler that has not returned and cannot be reverted around,
// so the watchdog ends the process instead (spec 1.2). The state is read
// under timerMu, which revertPending holds while it claims a change, so an
// operator's revert in progress is never taken for a hung apply. A state
// that cannot be read is read again after revertTimerRetry.
func (e *Engine) timerFired(changeID string, armed *armedTimer) {
	e.timerMu.Lock()
	if e.closed || e.timers[changeID] != armed {
		e.timerMu.Unlock()
		return
	}
	var state string
	err := e.cfg.Base.DB.Read().QueryRow("SELECT state FROM core_changes WHERE id = ?", changeID).Scan(&state)
	if err != nil {
		retry := &armedTimer{}
		retry.Timer = e.cfg.Base.Clock.AfterFunc(revertTimerRetry, func() { e.timerFired(changeID, retry) })
		e.timers[changeID] = retry
		e.timerMu.Unlock()
		e.cfg.Base.Log.Error("lifecycle: revert timer: read the change; retrying", "change", changeID,
			"retry", revertTimerRetry, "error", err)
		return
	}
	delete(e.timers, changeID)
	if state == core.StatePending {
		e.inflight.Add(1)
	}
	e.timerMu.Unlock()
	switch {
	case state == core.StatePending:
		defer e.inflight.Done()
		_, err := e.revertPending(context.Background(), timerPrincipal, changeID)
		var coded *errcode.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code != errcode.ChangeNotPending) {
			e.cfg.Base.Log.Error("lifecycle: revert timer", "change", changeID, "error", err)
		}
	case state == core.StateApplying:
		e.watchdog(changeID)
	}
}

// watchdog journals that changeID was still applying at its revert deadline
// and exits with WatchdogExitCode through Config.Exit.
func (e *Engine) watchdog(changeID string) {
	e.cfg.Base.Log.Error("lifecycle: revert watchdog: a revertible change is still applying at its revert deadline; "+
		"exiting so that startup recovery reverts it", "change", changeID)
	ctx, cancel := e.cleanup()
	defer cancel()
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var action, input string
		var version int
		if err := tx.QueryRowContext(ctx, "SELECT action, version, input_json FROM core_changes WHERE id = ?", changeID).
			Scan(&action, &version, &input); err != nil {
			return err
		}
		return e.cfg.Journal.Append(ctx, tx, core.JournalEntry{
			At: e.cfg.Base.Clock.Now(), Actor: ActorRevertTimer, Action: action, Version: version, Phase: phaseWatchdog,
			ChangeID: changeID, Input: json.RawMessage(input), Result: resultError, ErrorCode: string(errcode.DeadlineExceeded),
		})
	})
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: revert watchdog journal", "change", changeID, "error", err)
	}
	exit := e.cfg.Exit
	if exit == nil {
		exit = os.Exit
	}
	exit(WatchdogExitCode)
}

func (e *Engine) stopTimer(changeID string) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	e.stopTimerLocked(changeID)
}

func (e *Engine) stopTimerLocked(changeID string) {
	if t, ok := e.timers[changeID]; ok {
		t.Stop()
		delete(e.timers, changeID)
	}
}

package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRevertTimer is the actor of a revert that a confirm timer triggers.
const ActorRevertTimer = "system:revert-timer"

var timerPrincipal = core.Principal{Actor: ActorRevertTimer, Scope: core.ScopeAdmin}

// Confirm keeps a pending revertible change and releases its locks. Any
// principal may confirm; the caller authorizes it. Confirm and revert race in
// one transaction each, and the one that commits second gets
// change.not_pending.
func (e *Engine) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
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
// may revert; the caller authorizes it.
func (e *Engine) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return e.revertPending(context.WithoutCancel(ctx), p, changeID)
}

// revertPending claims a pending change by moving it back to applying, so a
// racing confirm loses and a crash during the revert is recovered from the
// snapshot at the next start; then it reverts and records the outcome.
func (e *Engine) revertPending(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if v, err := e.transition(ctx, changeID, transition{from: core.StatePending, to: core.StateApplying}); err != nil {
		return v, err
	}
	e.stopTimer(changeID)
	tr := transition{
		from: core.StateApplying, to: core.StateReverted, release: true,
		phase: phaseRevert, result: resultOK, actor: p.Actor, source: p.Source,
	}
	if err := e.runRevert(ctx, p, changeID); err != nil {
		e.cfg.Base.Log.Error("lifecycle: revert", "change", changeID, "error", err)
		tr.to, tr.err, tr.result = core.StateFailed, revertFailed(), resultError
	}
	return e.transition(ctx, changeID, tr)
}

// armTimer reverts the pending change after d unless it is confirmed or
// reverted first. Deadlines run on the clock's monotonic timers (ADR 0004).
func (e *Engine) armTimer(changeID string, d time.Duration) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.closed {
		return
	}
	e.timers[changeID] = e.cfg.Base.Clock.AfterFunc(d, func() {
		e.timerMu.Lock()
		if e.closed {
			e.timerMu.Unlock()
			return
		}
		delete(e.timers, changeID)
		e.inflight.Add(1)
		e.timerMu.Unlock()
		defer e.inflight.Done()
		_, err := e.revertPending(context.Background(), timerPrincipal, changeID)
		var coded *errcode.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code != errcode.ChangeNotPending) {
			e.cfg.Base.Log.Error("lifecycle: revert timer", "change", changeID, "error", err)
		}
	})
}

func (e *Engine) stopTimer(changeID string) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if t, ok := e.timers[changeID]; ok {
		t.Stop()
		delete(e.timers, changeID)
	}
}

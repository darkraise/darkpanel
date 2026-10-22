package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// failSettled records the failure of a compensable or irreversible change
// whose intended effects are settled, by 1.2's failure table. With a module
// recovery handler and no unresolved effect, the change is claimed
// recovering by owner module and the handler runs at once, its locks kept;
// with an unresolved effect the change goes needs_repair with
// recovery_retry set, so repair runs the handler once the operator has
// resolved them. The caller owns the change as engine, or as module for a
// task change that startup recovery re-claimed.
func (e *Engine) failSettled(ctx context.Context, p core.Principal, changeID string, def core.ActionDef, cause error) (core.ChangeView, error) {
	if def.Handler.Recover != nil {
		n, err := unresolvedCount(ctx, e.cfg.Base.DB.Read(), changeID, false)
		if err != nil {
			return core.ChangeView{}, err
		}
		if n == 0 {
			if err := e.recovererClaim(ctx, p, def, changeID, e.changeError(def.Ref(), cause)); err != nil {
				return core.ChangeView{}, err
			}
			return e.runRecoverer(ctx, changeID)
		}
	}
	state, err := e.recovererState(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	tr, err := e.failure(ctx, changeID, def, cause, false)
	if err != nil {
		return core.ChangeView{}, err
	}
	tr.from, tr.owner = state, recovererOwner("")
	tr.phase, tr.result, tr.actor, tr.source = phaseApply, resultError, p.Actor, p.Source
	if def.Handler.Recover != nil && tr.to == core.StateNeedsRepair {
		tr.retry = recovererFlag(true)
	}
	return e.transition(ctx, changeID, tr)
}

// recovererClaim moves a change the caller owns, as engine or as module,
// to state recovering with owner module, in one conditional update. cause,
// when set, is the live failure that led here: it is journaled as the
// apply's error and kept as the change's error cause, which the handler's
// outcome reports. A task action's handler runs in a recover attempt's
// unit, whose starting row this transaction inserts.
func (e *Engine) recovererClaim(ctx context.Context, p core.Principal, def core.ActionDef, changeID string, cause *core.ChangeError) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var state, owner string
		err := tx.QueryRowContext(ctx, "SELECT state, recovery_owner FROM core_changes WHERE id = ?", changeID).
			Scan(&state, &owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
		case err != nil:
			return err
		case owner != core.OwnerEngine && owner != core.OwnerModule:
			return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is owned by %q", changeID, owner))
		}
		now := e.cfg.Base.Clock.Now()
		if err := claim(ctx, tx, now, changeID, owner, core.OwnerModule, state, core.StateRecovering); err != nil {
			return err
		}
		if def.Spec.Task {
			if _, err := e.startTask(ctx, tx, changeID, core.TaskOpRecover); err != nil {
				return err
			}
		}
		if cause == nil {
			return nil
		}
		if _, err := tx.ExecContext(ctx, "UPDATE core_changes SET error_cause = ? WHERE id = ?", cause.Code, changeID); err != nil {
			return err
		}
		return e.journalChange(ctx, tx, now, changeID, p, phaseApply, resultError, cause.Code, nil)
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: claim %s for its recovery handler: %w", changeID, err)
	}
	return nil
}

// runRecoverer runs the module recovery handler of a change claimed
// recovering by owner module (1.3b), as the change's lock owner, then
// settles any effect the handler left intended and records its outcome
// with the owner cleared in one transaction. The handler's context ends at
// the action's ApplyTimeout, but the engine waits for it to return, so
// ownership never lapses.
func (e *Engine) runRecoverer(ctx context.Context, changeID string) (core.ChangeView, error) {
	var ref core.ActionRef
	var actor, state, owner, cause string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT action, version, actor, state, recovery_owner, coalesce(error_cause, '')
		 FROM core_changes WHERE id = ?`, changeID).Scan(&ref.Name, &ref.Version, &actor, &state, &owner, &cause)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.ChangeView{}, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return core.ChangeView{}, fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	case state != core.StateRecovering || owner != core.OwnerModule:
		return core.ChangeView{}, errcode.New(errcode.ChangeBusy, fmt.Sprintf(
			"change %s is %s and owned by %q; a recovery handler runs only on a change claimed recovering by module",
			changeID, state, owner))
	}
	def, err := e.action(ref)
	if err != nil {
		return core.ChangeView{}, err
	}
	if def.Handler.Recover == nil {
		return core.ChangeView{}, fmt.Errorf("lifecycle: %s has no recovery handler", ref)
	}
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	env := e.changeEnv(core.Principal{Actor: actor, Scope: core.ScopeAdmin}, def, changeID, call)
	var out core.RecoveryOutcome
	var herr error
	if def.Spec.Task {
		var stop error
		if out, herr, stop = e.taskRecover(ctx, def, changeID); stop != nil {
			return core.ChangeView{}, stop
		}
	} else {
		out, herr = e.recovererCall(def, env)
	}
	sctx, scancel := context.WithTimeout(context.WithoutCancel(e.runCtx), e.applyTimeout(def))
	if err := e.settle(sctx, env, def, changeID); err != nil && herr == nil {
		herr = err
	}
	scancel()
	rctx, rcancel := e.cleanup()
	defer rcancel()
	effs, err := loadEffects(rctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.ChangeView{}, fmt.Errorf("lifecycle: read the effects of %s: %w", changeID, err)
	}
	return e.transition(rctx, changeID, e.recovererOutcome(def, cause, out, herr, effs))
}

// recovererCall runs the handler in this process under the action's
// ApplyTimeout.
func (e *Engine) recovererCall(def core.ActionDef, env core.Env) (core.RecoveryOutcome, error) {
	ctx, cancel := context.WithTimeout(e.runCtx, e.applyTimeout(def))
	defer cancel()
	var out core.RecoveryOutcome
	err := e.protect(def.Ref(), "Recover", func() error {
		var err error
		out, err = def.Handler.Recover(ctx, env)
		return err
	})
	return out, err
}

// recovererOutcome maps a handler's outcome to the transition that records
// it (1.3b, Outcome). cause is the code of the failure that led to the
// handler, "" when a restart cut the change off.
func (e *Engine) recovererOutcome(def core.ActionDef, cause string, out core.RecoveryOutcome, herr error, effs []core.Effect) transition {
	if cause == "" {
		cause = string(errcode.Internal)
	}
	tr := transition{
		from: core.StateRecovering, owner: recovererOwner(""), retry: recovererFlag(false),
		phase: phaseRecover, result: resultError, actor: ActorRecovery,
	}
	needsRepair := func(cause, msg string) {
		tr.to, tr.keepLocks, tr.cause = core.StateNeedsRepair, true, cause
		tr.err = &core.ChangeError{Code: string(errcode.NeedsRepair), Message: msg}
	}
	unresolved := recovererUnresolved(effs)
	switch {
	case herr != nil:
		needsRepair(string(errcode.From(herr).Code),
			"the recovery handler failed: "+e.changeError(def.Ref(), herr).Message+"; repair runs it again")
		tr.retry = recovererFlag(true)
	case unresolved != "":
		needsRepair(cause, recovererReason(out.Reason, "the recovery handler finished")+"; unresolved effects: "+unresolved)
	case out.State == core.RecoveryCompleted:
		tr.to, tr.release, tr.result = core.StateCompleted, true, resultOK
		if len(out.Output) > 0 {
			tr.output, tr.private = e.cfg.Redact(def.OutputType, out.Output), out.Output
		}
	case out.State == core.RecoveryFailed:
		tr.to, tr.release, tr.giveBack = core.StateFailed, true, recovererNothingLeft(effs)
		tr.err = &core.ChangeError{Code: cause, Message: recovererReason(out.Reason, "the recovery handler found the change failed")}
	case out.State == core.RecoveryRolledBack:
		tr.to, tr.release, tr.giveBack, tr.result = core.StateReverted, true, true, resultOK
	case out.State == core.RecoveryNeedsRepair:
		needsRepair(cause, recovererReason(out.Reason, "the recovery handler needs the operator"))
	default:
		needsRepair(string(errcode.Internal),
			fmt.Sprintf("the recovery handler returned the unknown state %q; repair runs it again", out.State))
		tr.retry = recovererFlag(true)
	}
	return tr
}

// recovererRepair is the repair row of a change whose recovery handler has
// not finished (recovery_retry set, 1.3's repair table): under applyMu it
// claims the change recovering by owner module and runs the handler again
// in the background, answering the claimed change. It answers nil when
// recovery_retry is not set, so the next row of the table decides.
func (e *Engine) recovererRepair(ctx context.Context, changeID string) (*core.RepairResult, error) {
	f, err := loadFacts(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return nil, err
	}
	if def, err := e.action(f.ref()); f.retry && err == nil && def.Spec.Task {
		if err := e.taskLastInactive(ctx, changeID, core.TaskOpRecover); err != nil {
			return nil, err
		}
	}
	retry := false
	e.applyMu.Lock()
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := uninstallingErr(ctx, tx); err != nil {
			return err
		}
		f, err := loadFacts(ctx, tx, changeID)
		if err != nil {
			return err
		}
		if retry = f.retry; !retry {
			return nil
		}
		if err := claim(ctx, tx, e.cfg.Base.Clock.Now(), changeID, "", core.OwnerModule,
			core.StateNeedsRepair, core.StateRecovering); err != nil {
			return err
		}
		def, err := e.action(f.ref())
		if err != nil || !def.Spec.Task {
			return err
		}
		if e.cfg.Units == nil {
			return errcode.New(errcode.Internal, fmt.Sprintf("%s runs in a task unit, and this engine has no unit client", def.Ref()))
		}
		_, err = e.startTask(ctx, tx, changeID, core.TaskOpRecover)
		return err
	})
	e.applyMu.Unlock()
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return nil, err
	case err != nil:
		return nil, fmt.Errorf("lifecycle: repair %s: %w", changeID, err)
	case !retry:
		return nil, nil
	}
	v, err := e.Change(ctx, changeID)
	if err != nil {
		return nil, err
	}
	e.goRun(func() {
		if _, err := e.runRecoverer(e.runCtx, changeID); err != nil {
			e.cfg.Base.Log.Error("lifecycle: recovery handler run by repair", "change", changeID, "error", err)
		}
	})
	return &core.RepairResult{Change: &v}, nil
}

// recovererState reads a change's state.
func (e *Engine) recovererState(ctx context.Context, changeID string) (string, error) {
	var state string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx, "SELECT state FROM core_changes WHERE id = ?", changeID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return "", fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	}
	return state, nil
}

// recovererUnresolved names a change's unresolved effects by seq and
// resource, or is "" when there are none.
func recovererUnresolved(effs []core.Effect) string {
	var names []string
	for _, eff := range effs {
		if eff.State == core.EffectUnresolved {
			names = append(names, fmt.Sprintf("%d %s", eff.Seq, eff.Resource))
		}
	}
	return strings.Join(names, ", ")
}

// recovererNothingLeft reports whether no effect is made or unresolved, so
// the change gives its lock epochs back (1.2, Window).
func recovererNothingLeft(effs []core.Effect) bool {
	for _, eff := range effs {
		if eff.State == core.EffectMade || eff.State == core.EffectUnresolved {
			return false
		}
	}
	return true
}

func recovererReason(reason, fallback string) string {
	if reason == "" {
		return fallback
	}
	return reason
}

func recovererOwner(owner string) *string { return &owner }

func recovererFlag(b bool) *bool { return &b }

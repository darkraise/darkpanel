package lifecycle

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// settle decides every effect of changeID still intended after its handler
// returned an error or was cut off (spec 1.2, settle): a call that is not
// Local may still commit, so its effect is unresolved; a Local call is over,
// so an observation tells whether it was made. Each decision is its own
// committed write, bounded by CleanupTxTimeout rather than ctx: the
// observations may spend ctx's budget, and a decision must still be recorded.
func (e *Engine) settle(ctx context.Context, env core.Env, def core.ActionDef, changeID string) error {
	effects, err := loadEffects(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return err
	}
	for _, eff := range effects {
		if eff.State != core.EffectIntended {
			continue
		}
		u := e.settled(ctx, env, def, eff)
		wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), CleanupTxTimeout)
		err := e.cfg.Base.DB.Write(wctx, func(tx *sql.Tx) error {
			return setEffect(wctx, tx, e.cfg.Base.Clock.Now(), changeID, eff.Seq, []core.EffectState{core.EffectIntended}, u)
		})
		cancel()
		if err != nil {
			return fmt.Errorf("lifecycle: settle effect %d of change %s: %w", eff.Seq, changeID, err)
		}
	}
	return nil
}

// settled is settle's decision for one intended effect.
func (e *Engine) settled(ctx context.Context, env core.Env, def core.ActionDef, eff core.Effect) effectUpdate {
	uncertain := effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonUncertain}
	if !eff.Local || def.Handler.Observe == nil {
		return uncertain
	}
	var obs core.Observation
	err := e.protect(def.Ref(), "Observe", func() error {
		var err error
		obs, err = def.Handler.Observe(ctx, env, eff)
		return err
	})
	if err != nil {
		e.cfg.Base.Log.Warn("lifecycle: settle observation failed", "change", eff.ChangeID, "effect", eff.Seq, "error", err)
		return uncertain
	}
	cur := obs.Current
	ambiguous := effectUpdate{State: core.EffectUnresolved, Reason: core.ReasonAmbiguous, Current: &cur}
	switch {
	case eff.Kind == core.EffectCreate && obs.Matches == 0:
		return effectUpdate{State: core.EffectSkipped}
	case eff.Kind == core.EffectCreate:
		if obs.Matches != 1 || obs.Resource == "" || (eff.After != nil && !stateEqual(cur, *eff.After)) {
			return ambiguous
		}
		return effectUpdate{State: core.EffectMade, Resource: &obs.Resource, Post: &cur}
	case docEqual(cur, eff.Before):
		return effectUpdate{State: core.EffectSkipped}
	case eff.After != nil && stateEqual(cur, *eff.After):
		return effectUpdate{State: core.EffectMade, Post: &cur}
	}
	return ambiguous
}

// failure is spec 1.2's failure table for a compensable or irreversible
// change whose Apply or Verify failed with cause, read once its effects are
// settled: with an effect unresolved the change needs repair and keeps its
// locks; otherwise it failed (interrupted when cutOff) and releases them,
// giving its epochs back when nothing is left. The owner is cleared either
// way. The caller sets the journal fields.
func (e *Engine) failure(ctx context.Context, changeID string, def core.ActionDef, cause error, cutOff bool) (transition, error) {
	effects, err := loadEffects(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return transition{}, err
	}
	failed := e.changeError(def.Ref(), cause)
	var unresolved []string
	for _, eff := range effects {
		if eff.State == core.EffectUnresolved {
			unresolved = append(unresolved, fmt.Sprintf("%d %s (%s)", eff.Seq, eff.Resource, eff.Reason))
		}
	}
	if len(unresolved) > 0 {
		return transition{
			from: core.StateApplying, to: core.StateNeedsRepair, owner: newOwner(""), keepLocks: true,
			err: &core.ChangeError{
				Code:    string(errcode.NeedsRepair),
				Message: failed.Message + "; unresolved effects: " + strings.Join(unresolved, ", "),
			},
			cause: failed.Code,
		}, nil
	}
	to := core.StateFailed
	if cutOff {
		to = core.StateInterrupted
	}
	return transition{
		from: core.StateApplying, to: to, owner: newOwner(""), release: true, giveBack: nothingLeft(effects), err: failed,
	}, nil
}

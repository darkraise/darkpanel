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

// Recover settles every change a previous process left unfinished (1.3,
// change recovery at start). It seeds the engine's ids, claims each
// unfinished change in a transaction of its own, voiding the owners the
// previous process left, sweeps the locks of finished changes, and runs
// the recovery pass over the claims. Plan, Apply, Undo, Repair, Release and
// Resolve answer core.recovering until the pass has started. Run it once,
// before the engine serves requests.
func (e *Engine) Recover(ctx context.Context) error {
	e.recovering.Store(true)
	if err := e.seedIDs(ctx); err != nil {
		return err
	}
	claims, claimErr := e.recoverClaims(ctx)
	sweepErr := e.recoverSweep(ctx)
	passErr := e.recoverPass(ctx, claims)
	e.recovering.Store(false)
	return errors.Join(claimErr, sweepErr, passErr)
}

// recoverKind is the work startup recovery claimed a change for.
type recoverKind string

const (
	recoverRevert recoverKind = "revert" // a revertible change: settled and reverted
	recoverSettle recoverKind = "settle" // a cut-off apply or recovery handler: settled, then the failure table
	recoverUndo   recoverKind = "undo"   // an undo run, resumed
	recoverRetry  recoverKind = "retry"  // a recovery handler that has not finished, run again
	recoverTask   recoverKind = "task"   // a task change, which follows its unit sequence
)

// recoverClaim is one change startup recovery claimed.
type recoverClaim struct {
	id     string
	def    core.ActionDef
	kind   recoverKind
	state  string // the state the claim left
	repair bool   // the change's plan repairs another change
}

type recoverCandidate struct {
	id, action, class, state, owner string
	version                         int
	retry, repair, unresolved       bool
}

// recoverClaims claims every unfinished change, one transaction each. An
// owner the previous process left is void: a claim replaces it, and an
// owned change nothing claims is released from it, except a target a
// repair change still runs under (owner repair) and a composite, which
// composite recovery claims.
func (e *Engine) recoverClaims(ctx context.Context) ([]recoverClaim, error) {
	cands, err := e.recoverCandidates(ctx)
	if err != nil {
		return nil, err
	}
	var claims []recoverClaim
	var errs []error
	for _, c := range cands {
		cl, err := e.recoverClaimOne(ctx, c)
		switch {
		case err != nil:
			errs = append(errs, err)
		case cl != nil:
			claims = append(claims, *cl)
		}
	}
	return claims, errors.Join(errs...)
}

func (e *Engine) recoverCandidates(ctx context.Context) ([]recoverCandidate, error) {
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
		`SELECT c.id, c.action, c.version, c.class, c.state, c.recovery_owner, c.recovery_retry = 1,
		 coalesce(p.repairs, '') <> '',
		 EXISTS (SELECT 1 FROM core_effects f WHERE f.change_id = c.id AND f.state = ?)
		 FROM core_changes c LEFT JOIN core_plans p ON p.id = c.plan_id
		 WHERE c.state IN (?, ?, ?, ?, ?) OR c.recovery_owner <> ''
		 ORDER BY c.id`,
		string(core.EffectUnresolved), core.StateApplying, core.StatePending, core.StateRecovering,
		core.StateUndoing, core.StateNeedsRepair)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}
	var out []recoverCandidate
	for rows.Next() {
		var c recoverCandidate
		if err := rows.Scan(&c.id, &c.action, &c.version, &c.class, &c.state, &c.owner,
			&c.retry, &c.repair, &c.unresolved); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("lifecycle: find unfinished changes: %w", err)
		}
		out = append(out, c)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}
	return out, nil
}

// recoverDecide is the claim of 1.3's change recovery at start for c, or
// ok false when nothing claims it. A pending revertible change is claimed
// back to applying, so a confirm arriving before its revert loses.
func recoverDecide(c recoverCandidate, def core.ActionDef) (kind recoverKind, owner, to string, ok bool) {
	unfinished := c.state == core.StateApplying || c.state == core.StateRecovering
	switch {
	case def.Spec.Task && unfinished:
		return recoverTask, core.OwnerModule, core.StateRecovering, true
	case def.Spec.Class == core.ClassRevertible && (c.state == core.StateApplying || c.state == core.StatePending):
		return recoverRevert, core.OwnerEngine, core.StateApplying, true
	case c.state == core.StateApplying && c.repair,
		(def.Spec.Class == core.ClassCompensable || def.Spec.Class == core.ClassIrreversible) && unfinished:
		return recoverSettle, core.OwnerEngine, c.state, true
	case c.state == core.StateUndoing:
		return recoverUndo, core.OwnerUndo, core.StateUndoing, true
	case c.state == core.StateNeedsRepair && c.retry && !c.unresolved:
		return recoverRetry, core.OwnerModule, core.StateRecovering, true
	}
	return "", "", "", false
}

// recoverClaimOne claims one change in a transaction of its own, or voids
// the owner a previous process left on it. It skips composites, and a
// change whose action is no longer registered, which no handler can settle.
func (e *Engine) recoverClaimOne(ctx context.Context, c recoverCandidate) (*recoverClaim, error) {
	if core.Class(c.class) == core.ClassComposite {
		return nil, nil
	}
	ref := core.ActionRef{Name: c.action, Version: c.version}
	def, err := e.action(ref)
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: recovery skips a change of an unregistered action",
			"change", c.id, "action", ref.String())
		return nil, nil
	}
	kind, owner, to, ok := recoverDecide(c, def)
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		now := e.cfg.Base.Clock.Now()
		if ok {
			return claim(ctx, tx, now, c.id, c.owner, owner, c.state, to)
		}
		if c.owner == "" || c.owner == core.OwnerRepair {
			return nil
		}
		_, err := tx.ExecContext(ctx,
			"UPDATE core_changes SET recovery_owner = '', updated_at = ? WHERE id = ? AND recovery_owner = ?",
			formatTime(now), c.id, c.owner)
		return err
	})
	if err != nil {
		return nil, fmt.Errorf("lifecycle: claim change %s at start: %w", c.id, err)
	}
	if !ok {
		return nil, nil
	}
	return &recoverClaim{id: c.id, def: def, kind: kind, state: to, repair: c.repair}, nil
}

// recoverSweep deletes the locks of every change that is in none of the
// states that keep their locks across a restart and that no mechanism owns
// (1.3, locks across restarts). A reservation's owner is no change id, so
// the sweep never touches one.
func (e *Engine) recoverSweep(ctx context.Context) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM core_locks WHERE owner IN (SELECT id FROM core_changes
			 WHERE recovery_owner = '' AND state NOT IN (?, ?, ?, ?, ?, ?, ?, ?))`,
			core.StateApplying, core.StatePending, core.StateAwaitingConfirmation, core.StateUndoing,
			core.StateNeedsRepair, core.StateRecovering, core.StateReleasing, core.StateQuarantined)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: release finished locks: %w", err)
	}
	return nil
}

// recoverPass runs the claimed work one change after another: the revert of
// a revertible change and the interrupt record of a cut-off apply. Every
// other claim keeps its owner until the background pass takes it over.
func (e *Engine) recoverPass(ctx context.Context, claims []recoverClaim) error {
	var errs []error
	for _, c := range claims {
		if err := e.recoverOne(ctx, c); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

func (e *Engine) recoverOne(ctx context.Context, c recoverClaim) error {
	switch {
	case c.kind == recoverRevert:
		return e.recoverRevert(ctx, c)
	case c.kind == recoverSettle && c.state == core.StateApplying:
		msg := "interrupted by a restart during apply; " + e.verifyStored(ctx, c.id)
		_, err := e.transition(ctx, c.id, transition{
			from: core.StateApplying, to: core.StateInterrupted, release: true, owner: recovererOwner(""),
			phase: phaseInterrupt, result: resultError, actor: ActorRecovery,
			err: &core.ChangeError{Code: string(errcode.Internal), Message: msg},
		})
		return err
	}
	return nil
}

// recoverRevert settles a claimed revertible change, reverts it from its
// snapshot and records the outcome by 1.2's revert table (revertAtStart).
func (e *Engine) recoverRevert(ctx context.Context, c recoverClaim) error {
	_, err := e.revertAtStart(ctx, c.id, transition{
		from: core.StateApplying, to: core.StateReverted, release: true, giveBack: true, owner: recovererOwner(""),
		phase: phaseRecover, result: resultOK, actor: ActorRecovery,
	})
	return err
}

// revertAtStart settles a revertible change cut off by the restart, reverts
// it from its snapshot and records the outcome through recordRevert; tr is
// the transition of a complete revert.
func (e *Engine) revertAtStart(ctx context.Context, changeID string, tr transition) (core.ChangeView, error) {
	def, err := e.changeDef(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	hctx, cancel := e.cleanupCall(def)
	defer cancel()
	if err := e.settle(hctx, e.changeEnv(recoveryPrincipal, def, changeID, call), def, changeID); err != nil {
		return core.ChangeView{}, err
	}
	out, revertErr := e.runRevert(hctx, recoveryPrincipal, changeID)
	// Recorded even when Recover's ctx ends after the revert: the host is
	// restored, and an unrecorded revert would run again at the next start.
	tctx, cancelTx := context.WithTimeout(context.WithoutCancel(ctx), CleanupTxTimeout)
	defer cancelTx()
	return e.recordRevert(tctx, recoveryPrincipal, changeID, out, revertErr, tr)
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
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return "verify not run: the stored call context is unreadable"
	}
	env := e.changeEnv(recoveryPrincipal, def, changeID, call)
	return e.verifyRecovery(ctx, def, env, input, json.RawMessage("null"))
}

// Purge deletes expired plans that never became a change, clears private
// outputs no composite acknowledged within PrivateOutputTTL, and clears the
// sealed secret of every made effect whose undo window has closed (spec
// 1.2, 1.3); a step whose composite is terminal is cleared by AckPrivate's
// final acknowledgement instead. It runs every 10 minutes, more often than
// the hourly purge the spec requires. A plan with a change is kept, so a
// repeated apply still returns that change (spec section 3).
func (e *Engine) Purge(ctx context.Context) error {
	// An apply that loaded a plan before it expired inserts its change later;
	// deleting the plan in between would fail that insert's foreign key.
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	now := e.cfg.Base.Clock.Now()
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM core_plans WHERE expires_at <= ?
			 AND NOT EXISTS (SELECT 1 FROM core_changes WHERE plan_id = core_plans.id)`, formatTime(now)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET private_output_ct = NULL, private_output_at = NULL
			 WHERE private_output_at IS NOT NULL AND private_output_at <= ?`,
			formatTime(now.Add(-PrivateOutputTTL))); err != nil {
			return err
		}
		return purgeEffectSecrets(ctx, tx)
	})
	if err != nil {
		return fmt.Errorf("lifecycle: purge plans: %w", err)
	}
	return nil
}

// purgeEffectSecrets clears the sealed secret of every made effect whose
// change can no longer be undone because a later change took one of its
// lock keys (spec 1.2). A secret exists only for an inverse, so it has no
// use once the undo window has closed.
func purgeEffectSecrets(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx,
		"SELECT DISTINCT change_id FROM core_effects WHERE state = ? AND secret_ct IS NOT NULL", string(core.EffectMade))
	if err != nil {
		return err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	for _, id := range ids {
		key, err := windowOpen(ctx, tx, id)
		if err != nil {
			return err
		}
		if key == "" {
			continue
		}
		if _, err := tx.ExecContext(ctx, "UPDATE core_effects SET secret_ct = NULL WHERE change_id = ? AND state = ?",
			id, string(core.EffectMade)); err != nil {
			return err
		}
	}
	return nil
}

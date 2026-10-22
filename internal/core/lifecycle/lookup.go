package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Lookup answers what became of a plan, for a composite that lost an
// answer (spec 1.3, Lookup and retire). It never writes. A plan without a
// change is absent, or retired once Retire marked it; a plan with a change
// is running, recovering or terminal by the change's owner.
func (e *Engine) Lookup(ctx context.Context, planID string, _ bool) (core.PlanLookup, error) {
	return e.lookup(ctx, e.cfg.Base.DB.Read(), planID)
}

// Retire marks a plan that has no change retired, so a later apply of it
// is refused with plan.not_found, and answers retired; a plan with a
// change answers its lookup. It holds applyMu, so it never interleaves with
// an apply's checks and intent.
func (e *Engine) Retire(ctx context.Context, planID string) (core.PlanLookup, error) {
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	var lk core.PlanLookup
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if lk, err = e.lookup(ctx, tx, planID); err != nil || lk.Status != core.LookupAbsent {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE core_plans SET retired_at = ? WHERE id = ?",
			formatTime(e.cfg.Base.Clock.Now()), planID); err != nil {
			return err
		}
		lk = core.PlanLookup{Status: core.LookupRetired}
		return nil
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return core.PlanLookup{}, err
	case err != nil:
		return core.PlanLookup{}, fmt.Errorf("lifecycle: retire plan %s: %w", planID, err)
	}
	return lk, nil
}

func (e *Engine) lookup(ctx context.Context, q queryer, planID string) (core.PlanLookup, error) {
	var retired sql.NullString
	err := q.QueryRowContext(ctx, "SELECT retired_at FROM core_plans WHERE id = ?", planID).Scan(&retired)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return core.PlanLookup{}, errcode.New(errcode.PlanNotFound, fmt.Sprintf("no plan %s", planID))
	case err != nil:
		return core.PlanLookup{}, fmt.Errorf("lifecycle: read plan %s: %w", planID, err)
	}
	v, err := scanChange(q.QueryRowContext(ctx, "SELECT "+changeColumns+" FROM core_changes WHERE plan_id = ?", planID))
	switch {
	case errors.Is(err, sql.ErrNoRows) && retired.Valid:
		return core.PlanLookup{Status: core.LookupRetired}, nil
	case errors.Is(err, sql.ErrNoRows):
		return core.PlanLookup{Status: core.LookupAbsent}, nil
	case err != nil:
		return core.PlanLookup{}, fmt.Errorf("lifecycle: read the change of plan %s: %w", planID, err)
	}
	lk := core.PlanLookup{
		ChangeID: v.ID, State: v.State, Owner: v.Owner, RepairOutcome: v.RepairOutcome, Output: v.Output, Error: v.Error,
	}
	switch v.Owner {
	case "":
		lk.Status = core.LookupTerminal
	case core.OwnerModule:
		lk.Status = core.LookupRecovering
	default:
		lk.Status = core.LookupRunning
	}
	if v.State == core.StatePending && v.Class == core.ClassRevertible {
		lk.Deadline = v.RevertDeadline
	}
	if err := q.QueryRowContext(ctx,
		`SELECT count(*) FILTER (WHERE state IN (?, ?)), count(*) FILTER (WHERE state = ?)
		 FROM core_effects WHERE change_id = ?`,
		string(core.EffectMade), string(core.EffectUndoing), string(core.EffectUnresolved), v.ID).
		Scan(&lk.Made, &lk.Unresolved); err != nil {
		return core.PlanLookup{}, fmt.Errorf("lifecycle: count effects of %s: %w", v.ID, err)
	}
	return lk, nil
}

// refuseRetired answers plan.not_found for a retired plan: retire promised
// the composite that the plan never becomes a change.
func refuseRetired(ctx context.Context, q queryer, planID string) error {
	var retired sql.NullString
	if err := q.QueryRowContext(ctx, "SELECT retired_at FROM core_plans WHERE id = ?", planID).Scan(&retired); err != nil {
		return fmt.Errorf("lifecycle: read plan %s: %w", planID, err)
	}
	if retired.Valid {
		return errcode.New(errcode.PlanNotFound, fmt.Sprintf("plan %s was retired", planID))
	}
	return nil
}

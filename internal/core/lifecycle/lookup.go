package lifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// PrivateOutputTTL is how long a step's unacknowledged private output is
// kept (spec 1.3, Child results, including secrets).
const PrivateOutputTTL = 24 * time.Hour

// privateOutputAD is the associated data of a change's sealed private
// output (ADR 0005).
func privateOutputAD(changeID string) string { return "core_changes.private_output_ct." + changeID }

// Lookup answers what became of a plan, for a composite that lost an
// answer (spec 1.3, Lookup and retire). It never writes. A plan without a
// change is absent, or retired once Retire marked it; a plan with a change
// is running, recovering or terminal by the change's owner. With private
// set, the answer carries the change's unredacted output while it is
// stored.
func (e *Engine) Lookup(ctx context.Context, planID string, private bool) (core.PlanLookup, error) {
	return e.lookup(ctx, e.cfg.Base.DB.Read(), planID, private)
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
		if lk, err = e.lookup(ctx, tx, planID, false); err != nil || lk.Status != core.LookupAbsent {
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

func (e *Engine) lookup(ctx context.Context, q queryer, planID string, private bool) (core.PlanLookup, error) {
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
	if private {
		var ct []byte
		if err := q.QueryRowContext(ctx, "SELECT private_output_ct FROM core_changes WHERE id = ?", v.ID).
			Scan(&ct); err != nil {
			return core.PlanLookup{}, fmt.Errorf("lifecycle: read the private output of %s: %w", v.ID, err)
		}
		if ct != nil {
			raw, err := e.cfg.Base.Sealer.Open(privateOutputAD(v.ID), ct)
			if err != nil {
				return core.PlanLookup{}, fmt.Errorf("lifecycle: open the private output of %s: %w", v.ID, err)
			}
			lk.Private = raw
		}
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

// storePrivate keeps a step's unredacted output sealed when redaction hid
// something in it, so its composite can take the secret once, from the
// apply answer or a private lookup (spec 1.3, Child results, including
// secrets). It runs in the transaction that records the change's result; a
// top-level change, and an output without secret fields, store nothing.
func (e *Engine) storePrivate(ctx context.Context, tx *sql.Tx, now time.Time, changeID string, redacted, raw json.RawMessage) error {
	if len(raw) == 0 {
		return nil
	}
	var parent sql.NullString
	if err := tx.QueryRowContext(ctx, "SELECT parent_change_id FROM core_changes WHERE id = ?", changeID).
		Scan(&parent); err != nil {
		return err
	}
	if parent.String == "" {
		return nil
	}
	a, err := canonicalJSON(redacted)
	if err != nil {
		return fmt.Errorf("lifecycle: compare the output of %s: %w", changeID, err)
	}
	b, err := canonicalJSON(raw)
	if err != nil {
		return fmt.Errorf("lifecycle: compare the output of %s: %w", changeID, err)
	}
	if bytes.Equal(a, b) {
		return nil
	}
	ct, err := e.cfg.Base.Sealer.Seal(privateOutputAD(changeID), raw)
	if err != nil {
		return fmt.Errorf("lifecycle: seal the private output of %s: %w", changeID, err)
	}
	_, err = tx.ExecContext(ctx, "UPDATE core_changes SET private_output_ct = ?, private_output_at = ? WHERE id = ?",
		ct, formatTime(now), changeID)
	return err
}

// AckPrivate clears a change's private output once its composite has
// sealed it into the step log. With final set the change's composite is
// terminal, so the step can no longer be undone, and the sealed secrets of
// its made effects are cleared in the same transaction (spec 1.2). A change
// that is no step keeps them: only its own undo window ends its undo. It is
// idempotent.
func (e *Engine) AckPrivate(ctx context.Context, changeID string, final bool) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"UPDATE core_changes SET private_output_ct = NULL, private_output_at = NULL WHERE id = ?", changeID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
		}
		if !final {
			return nil
		}
		_, err = tx.ExecContext(ctx,
			`UPDATE core_effects SET secret_ct = NULL WHERE change_id = ? AND state = ?
			 AND EXISTS (SELECT 1 FROM core_changes WHERE id = ? AND coalesce(parent_change_id, '') <> '')`,
			changeID, string(core.EffectMade), changeID)
		return err
	})
	var coded *errcode.Error
	if err != nil && !errors.As(err, &coded) {
		return fmt.Errorf("lifecycle: acknowledge the private output of %s: %w", changeID, err)
	}
	return err
}

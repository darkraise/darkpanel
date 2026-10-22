package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const phaseRelease = "release"

// maxReasonLength is the longest release reason, in characters (spec 1.3,
// Release).
const maxReasonLength = 500

// releaseEntry is the journal input of a release.
type releaseEntry struct {
	Reason string `json:"reason"`
}

// Release gives up a change in needs_repair (spec 1.3, Release). In one
// transaction it marks every non-terminal effect of the change and of its
// failed repair changes abandoned, moves the change to abandoned, releases
// its locks and journals the reason, and it answers the abandoned effects.
// A repeated release of an abandoned change answers it with none. A step
// of a composite is released only by that composite, as system:recovery
// with call naming it.
func (e *Engine) Release(ctx context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	if err := e.admitRecovery(); err != nil {
		return core.ReleaseResult{}, err
	}
	composite, err := e.isComposite(ctx, changeID)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	if composite {
		return e.releaseComposite(ctx, p, changeID, reason)
	}
	res := core.ReleaseResult{Abandoned: []core.EffectView{}}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		f, err := loadFacts(ctx, tx, changeID)
		if err != nil {
			return err
		}
		if err := e.releaseRefusal(ctx, tx, p, f, reason, call); err != nil {
			return err
		}
		switch f.state {
		case core.StateAbandoned:
		case core.StateNeedsRepair:
			if res.Abandoned, err = e.abandon(ctx, tx, p, f, reason); err != nil {
				return err
			}
		default:
			return errcode.New(errcode.ChangeNotPending,
				fmt.Sprintf("change %s is %s; only a change in needs_repair is released", changeID, f.state))
		}
		res.Change, err = scanChange(tx.QueryRowContext(ctx,
			"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID))
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return core.ReleaseResult{}, err
	case err != nil:
		return core.ReleaseResult{}, fmt.Errorf("lifecycle: release %s: %w", changeID, err)
	}
	return res, nil
}

// releaseRefusal applies the refusals of spec 1.3's Release in order.
func (e *Engine) releaseRefusal(ctx context.Context, q queryer, p core.Principal, f changeFacts, reason string,
	call *core.CallContext) error {
	switch {
	case f.repairs != "":
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s repairs change %s; release change %s", f.id, f.repairs, f.repairs))
	case f.parent != "" && (call == nil || call.ParentChangeID != f.parent || p.Actor != ActorRecovery):
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s is a step of composite %s; release the composite", f.id, f.parent))
	case f.owner != "":
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is owned by %s", f.id, f.owner))
	}
	var repairID, repairOwner string
	err := q.QueryRowContext(ctx,
		`SELECT c.id, c.recovery_owner FROM core_changes c JOIN core_plans p ON p.id = c.plan_id
		 WHERE p.repairs = ? AND c.recovery_owner <> '' ORDER BY c.id LIMIT 1`, f.id).Scan(&repairID, &repairOwner)
	switch {
	case err == nil:
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("repair change %s of change %s is owned by %s", repairID, f.id, repairOwner))
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("lifecycle: read the repair changes of %s: %w", f.id, err)
	}
	// Only the agent runs task units; the controller has no results table.
	if e.cfg.Side == core.SideAgent {
		var live int
		if err := q.QueryRowContext(ctx,
			"SELECT count(*) FROM core_task_runs WHERE change_id = ? AND state IN ('starting', 'running')", f.id).
			Scan(&live); err != nil {
			return fmt.Errorf("lifecycle: read the task units of %s: %w", f.id, err)
		}
		if live > 0 {
			return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s has a task unit still running", f.id))
		}
	}
	if n := utf8.RuneCountInString(reason); n < 1 || n > maxReasonLength {
		return &errcode.Error{
			Code: errcode.InvalidInput, Detail: "a release reason is 1 to 500 characters",
			Fields: []errcode.FieldError{{Path: "/reason", Message: "must be 1 to 500 characters"}},
		}
	}
	return nil
}

// abandon gives up a needs_repair change inside tx and returns the effects
// it abandoned.
func (e *Engine) abandon(ctx context.Context, tx *sql.Tx, p core.Principal, f changeFacts, reason string) ([]core.EffectView, error) {
	now := e.cfg.Base.Clock.Now()
	failed, err := repairChanges(ctx, tx, f.id, core.StateFailed)
	if err != nil {
		return nil, err
	}
	abandoned := []core.EffectView{}
	for _, id := range append([]string{f.id}, failed...) {
		effs, err := loadEffects(ctx, tx, id)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: read effects of %s: %w", id, err)
		}
		for _, eff := range effs {
			if !abandonable(eff.State) {
				continue
			}
			if err := setEffect(ctx, tx, now, id, eff.Seq, []core.EffectState{eff.State},
				effectUpdate{State: core.EffectAbandoned}); err != nil {
				return nil, err
			}
			eff.State = core.EffectAbandoned
			abandoned = append(abandoned, effectView(eff))
		}
	}
	res, err := tx.ExecContext(ctx,
		"UPDATE core_changes SET state = ?, updated_at = ? WHERE id = ? AND state = ? AND recovery_owner = ''",
		core.StateAbandoned, formatTime(now), f.id, core.StateNeedsRepair)
	if err != nil {
		return nil, err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return nil, err
	}
	if n != 1 {
		return nil, fmt.Errorf("lifecycle: change %s changed during its release", f.id)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", f.id); err != nil {
		return nil, err
	}
	// An abandoned change is terminal, so its plan keeps only the hash (spec
	// 1.2, Sealed input).
	if _, err := tx.ExecContext(ctx, "UPDATE core_plans SET input_ct = NULL WHERE id = ?", f.planID); err != nil {
		return nil, err
	}
	input, err := json.Marshal(releaseEntry{Reason: reason})
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode the release reason: %w", err)
	}
	if err := e.journalChange(ctx, tx, now, f.id, p, phaseRelease, resultOK, "", input); err != nil {
		return nil, err
	}
	return abandoned, nil
}

// abandonable reports whether a release abandons an effect in state s: every
// state but the terminal ones (spec 1.2).
func abandonable(s core.EffectState) bool {
	return !effectTerminal(s)
}

// repairChanges lists the ids of target's repair changes, oldest first; a
// non-empty state lists only those in that state.
func repairChanges(ctx context.Context, q queryer, target, state string) ([]string, error) {
	query := "SELECT c.id FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE p.repairs = ?"
	args := []any{target}
	if state != "" {
		query += " AND c.state = ?"
		args = append(args, state)
	}
	query += " ORDER BY c.id"
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: list the repair changes of %s: %w", target, err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("lifecycle: list the repair changes of %s: %w", target, err)
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// Effects lists a change's effects, followed by those of its repair
// changes; each view carries its own change id.
func (e *Engine) Effects(ctx context.Context, changeID string) ([]core.EffectView, error) {
	q := e.cfg.Base.DB.Read()
	if _, err := loadFacts(ctx, q, changeID); err != nil {
		return nil, err
	}
	repairs, err := repairChanges(ctx, q, changeID, "")
	if err != nil {
		return nil, err
	}
	out := []core.EffectView{}
	for _, id := range append([]string{changeID}, repairs...) {
		effs, err := loadEffects(ctx, q, id)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: read effects of %s: %w", id, err)
		}
		for _, eff := range effs {
			out = append(out, effectView(eff))
		}
	}
	return out, nil
}

// Steps lists a composite's step log in step order. Any other change, and
// every change on the agent, has none.
func (e *Engine) Steps(ctx context.Context, changeID string) ([]core.StepView, error) {
	q := e.cfg.Base.DB.Read()
	f, err := loadFacts(ctx, q, changeID)
	if err != nil {
		return nil, err
	}
	out := []core.StepView{}
	if e.cfg.Side != core.SideController || f.class != core.ClassComposite {
		return out, nil
	}
	rows, err := q.QueryContext(ctx,
		`SELECT step_id, ordinal, host_id, action, state, attempt, child_change_id, earlier_child_change_ids,
		 child_deadline, error_code, error_message
		 FROM core_composite_steps WHERE change_id = ? ORDER BY ordinal`, changeID)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: read steps of %s: %w", changeID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		v, err := scanStepView(rows)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: read steps of %s: %w", changeID, err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// scanStepView reads the columns Steps selects into a StepView, which
// never carries inputs or outputs.
func scanStepView(s scanner) (core.StepView, error) {
	var v core.StepView
	var action, earlier string
	var child, deadline, code, msg sql.NullString
	if err := s.Scan(&v.ID, &v.Ordinal, &v.Host, &action, &v.State, &v.Attempt, &child, &earlier,
		&deadline, &code, &msg); err != nil {
		return v, err
	}
	if err := v.Action.UnmarshalText([]byte(action)); err != nil {
		return v, err
	}
	if err := json.Unmarshal([]byte(earlier), &v.EarlierChildChangeIDs); err != nil {
		return v, fmt.Errorf("earlier child change ids of step %s: %w", v.ID, err)
	}
	v.EarlierChildChangeIDs = nonNil(v.EarlierChildChangeIDs)
	v.ChildChangeID = child.String
	if deadline.Valid {
		t, err := parseTime(deadline.String)
		if err != nil {
			return v, err
		}
		v.ChildDeadline = &t
	}
	if code.Valid {
		v.Error = &core.ChangeError{Code: code.String, Message: msg.String}
	}
	return v, nil
}

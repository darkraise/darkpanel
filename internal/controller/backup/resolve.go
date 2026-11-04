package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

const resolveTimeLayout = "2006-01-02T15:04:05.000000000Z"

const (
	resolveDone   = "done"
	resolveNever  = "never"
	resolveRepair = "repair"

	resolveNeverApplied = "restore: never applied"
)

// resolveStep is a composite step a done or never outcome is decided from.
type resolveStep struct {
	id, hostID, plan string
	agent            core.AgentClient
	absent           bool // the agent answered absent, so a never outcome retires the plan
}

// resolveHandler settles one quarantined record with the outcome the operator
// chose.
func (b *Backup) resolveHandler(deps ext.Deps) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		var req ResolveRequest
		if err := decodeBody(w, r, &req); err != nil ||
			(req.Outcome != resolveDone && req.Outcome != resolveNever && req.Outcome != resolveRepair) {
			writeProblem(w, deps.Log, errcode.New(errcode.InvalidInput, "the outcome must be done, never or repair"))
			return
		}
		id := r.PathValue("record")

		rec, err := resolveFind(ctx, deps.DB.Read(), id)
		if err == nil {
			err = resolveCheck(ctx, deps, rec, req.Outcome)
		}
		if err == nil {
			err = deps.DB.Write(ctx, func(tx *sql.Tx) error {
				return resolveCommit(ctx, tx, rec, req.Outcome, deps.Clock.Now().UTC().Format(resolveTimeLayout))
			})
		}
		if err != nil {
			writeProblem(w, deps.Log, err)
			return
		}
		if req.Outcome != resolveRepair {
			rec.Locks = []string{}
		}
		recordAudit(context.WithoutCancel(r.Context()), deps, "controller.quarantine.resolve", "resolve", id, "")
		writeJSON(w, deps.Log, http.StatusOK, rec)
	})
}

func resolveNotQuarantined() error {
	return errcode.New(errcode.PlanStateChanged, "the record is no longer quarantined")
}

// resolveFind returns the quarantined record id.
func resolveFind(ctx context.Context, q queryer, id string) (Record, error) {
	var state string
	err := q.QueryRowContext(ctx, `SELECT state FROM core_changes WHERE id = ?`, id).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		err = q.QueryRowContext(ctx, `SELECT state FROM core_rollouts WHERE id = ?`, id).Scan(&state)
	}
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return Record{}, errcode.New(errcode.NotFound, "no such record")
	case err != nil:
		return Record{}, fmt.Errorf("backup: read the state of %s: %w", id, err)
	case state != core.StateQuarantined:
		return Record{}, resolveNotQuarantined()
	}
	rec, ok, err := recordByID(ctx, q, id)
	switch {
	case err != nil:
		return Record{}, err
	case !ok:
		return Record{}, resolveNotQuarantined()
	}
	return rec, nil
}

// resolveCheck refuses an outcome the record's kind does not admit and, for a
// composite resolved done or never, decides the outcome from its steps and
// retires the child plans a never outcome leaves behind.
func resolveCheck(ctx context.Context, deps ext.Deps, rec Record, outcome string) error {
	switch rec.Kind {
	case "rollout":
		if outcome != resolveNever {
			return errcode.New(errcode.InvalidInput, "a rollout resolves never; start a new rollout after release")
		}
		return nil
	case "composite":
		if outcome == resolveRepair {
			return nil
		}
		return resolveComposite(ctx, deps, rec.ID, outcome)
	default:
		if outcome != resolveRepair {
			return errcode.New(errcode.InvalidInput, "a controller-side change is reconciled by repair: resolve it repair and run darkpanel change repair "+
				rec.ID+" after release")
		}
		return nil
	}
}

func resolveComposite(ctx context.Context, deps ext.Deps, id, outcome string) error {
	db := deps.DB.Read()
	var direction string
	if err := db.QueryRowContext(ctx, `SELECT direction FROM core_composites WHERE change_id = ?`, id).Scan(&direction); err != nil {
		return fmt.Errorf("backup: read the direction of composite %s: %w", id, err)
	}
	rows, err := db.QueryContext(ctx,
		`SELECT step_id, host_id, state, child_plan_id FROM core_composite_steps WHERE change_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return fmt.Errorf("backup: read the steps of composite %s: %w", id, err)
	}
	var (
		looked     []*resolveStep
		notApplied int // steps that never ran; a done outcome refuses any
	)
	_, err = quarantineScanRows(rows, nil, func(s quarantineScanner) (struct{}, error) {
		var (
			step  resolveStep
			state string
			plan  sql.NullString
		)
		if err := s.Scan(&step.id, &step.hostID, &state, &plan); err != nil {
			return struct{}{}, err
		}
		step.plan = plan.String
		switch {
		case state == core.StepRead:
		case step.plan == "":
			notApplied++
		default:
			looked = append(looked, &step)
		}
		return struct{}{}, nil
	})
	if err != nil {
		return fmt.Errorf("backup: read the steps of composite %s: %w", id, err)
	}

	for _, step := range looked {
		if step.hostID == "" {
			return errcode.New(errcode.InvalidInput, "step "+step.id+
				" runs on the controller and no agent can show whether it ran; resolve the composite repair")
		}
	}
	if outcome == resolveDone && direction == "unwind" {
		return errcode.New(errcode.InvalidInput,
			"a composite that is unwinding cannot be resolved done; resolve it never or repair")
	}

	applied, neither := 0, 0
	for _, step := range looked {
		if step.agent, err = deps.Hosts.Agent(ctx, step.hostID); err != nil {
			return err
		}
		lk, err := step.agent.Lookup(ctx, step.plan, false)
		if err != nil {
			return err
		}
		switch {
		case lk.Status == core.LookupRunning || lk.Status == core.LookupRecovering:
			return errcode.New(errcode.ChangeBusy, "step "+step.id+" is still running on host "+step.hostID+
				"; resolve the composite after the agent finishes")
		case resolveApplied(lk):
			applied++
		case resolveNotApplied(lk):
			notApplied++
			step.absent = lk.Status == core.LookupAbsent
		default:
			neither++
		}
	}
	if neither > 0 || (outcome == resolveDone && notApplied > 0) || (outcome == resolveNever && applied > 0) {
		return errcode.New(errcode.InvalidInput, "the composite is partly applied; resolve it repair")
	}

	if outcome != resolveNever {
		return nil
	}
	for _, step := range looked {
		if !step.absent {
			continue
		}
		lk, err := step.agent.Retire(ctx, step.plan)
		if err != nil {
			return err
		}
		if lk.Status != core.LookupRetired && lk.Status != core.LookupAbsent {
			return errcode.New(errcode.PlanStateChanged, "a child plan of step "+step.id+" changed while it was being retired")
		}
	}
	return nil
}

func resolveApplied(lk core.PlanLookup) bool {
	if lk.Status != core.LookupTerminal {
		return false
	}
	switch lk.State {
	case core.StateConfirmed, core.StateCompleted, core.StateRepaired, core.StateKept:
		return true
	}
	return false
}

func resolveNotApplied(lk core.PlanLookup) bool {
	switch lk.Status {
	case core.LookupAbsent, core.LookupRetired:
		return true
	case core.LookupTerminal:
		switch lk.State {
		case core.StateReverted, core.StateFailed, core.StateInterrupted, core.StateAbandoned:
			return lk.Made == 0
		}
	}
	return false
}

// resolveCommit moves the record out of quarantine; quarantined_from is kept
// so the record can still be answered with the state it was quarantined from.
func resolveCommit(ctx context.Context, tx *sql.Tx, rec Record, outcome, now string) error {
	table := "core_changes"
	if rec.Kind == "rollout" {
		table = "core_rollouts"
	}
	var state string
	err := tx.QueryRowContext(ctx, `SELECT state FROM `+table+` WHERE id = ?`, rec.ID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return resolveNotQuarantined()
	case err != nil:
		return fmt.Errorf("backup: read the state of %s: %w", rec.ID, err)
	case state != core.StateQuarantined:
		return resolveNotQuarantined()
	}

	var (
		query string
		args  []any
	)
	switch {
	case outcome == resolveRepair:
		query = `UPDATE core_changes SET state = ?, error_code = ?, error_message = ?, recovery_owner = '', updated_at = ?
			WHERE id = ? AND state = 'quarantined'`
		args = []any{core.StateNeedsRepair, "core.needs_repair", "restore: quarantined; repair it after release", now, rec.ID}
	case rec.Kind == "rollout":
		query = `UPDATE core_rollouts SET state = ?, error_message = ?, updated_at = ? WHERE id = ? AND state = 'quarantined'`
		args = []any{core.RolloutCancelled, resolveNeverApplied, now, rec.ID}
	case outcome == resolveDone:
		query = `UPDATE core_changes SET state = ?, error_code = NULL, error_message = NULL, error_cause = NULL,
			recovery_owner = '', updated_at = ? WHERE id = ? AND state = 'quarantined'`
		args = []any{core.StateCompleted, now, rec.ID}
	default:
		query = `UPDATE core_changes SET state = ?, error_code = NULL, error_message = ?, error_cause = NULL,
			recovery_owner = '', updated_at = ? WHERE id = ? AND state = 'quarantined'`
		args = []any{core.StateAbandoned, resolveNeverApplied, now, rec.ID}
	}
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("backup: resolve %s: %w", rec.ID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("backup: resolve %s: %w", rec.ID, err)
	}
	if n != 1 {
		return resolveNotQuarantined()
	}

	if outcome == resolveRepair {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_locks WHERE owner = ?`, rec.ID); err != nil {
		return fmt.Errorf("backup: release the locks of %s: %w", rec.ID, err)
	}
	return nil
}

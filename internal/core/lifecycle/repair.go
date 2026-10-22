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

// Repair repairs a change in needs_repair by the first row of spec 1.3's
// repair table that applies: an unresolved effect refuses it, an undo
// handler undoes the change, a revertible change gets a plan of its
// RepairedBy action, and a converging change without an undo handler gets
// a plan that applies it again with its stored input. A plan is applied by
// the client through the ordinary apply route.
func (e *Engine) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	if err := e.admit(ctx); err != nil {
		return core.RepairResult{}, err
	}
	composite, err := e.isComposite(ctx, changeID)
	if err != nil {
		return core.RepairResult{}, err
	}
	if composite {
		return e.repairComposite(ctx, p, changeID, req)
	}
	q := e.cfg.Base.DB.Read()
	f, err := loadFacts(ctx, q, changeID)
	if err != nil {
		return core.RepairResult{}, err
	}
	if err := repairRequestRefusal(f, req); err != nil {
		return core.RepairResult{}, err
	}
	def, err := e.action(f.ref())
	if err != nil {
		return core.RepairResult{}, err
	}
	switch {
	case f.repairs != "":
		return core.RepairResult{}, errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s repairs change %s; repair change %s instead", changeID, f.repairs, f.repairs))
	case f.owner != "":
		return core.RepairResult{}, errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is owned by %s", changeID, f.owner))
	case f.state != core.StateNeedsRepair:
		return core.RepairResult{}, errcode.New(errcode.ChangeNotPending,
			fmt.Sprintf("change %s is %s; only a change in needs_repair is repaired", changeID, f.state))
	}
	n, err := unresolvedCount(ctx, q, changeID, true)
	if err != nil {
		return core.RepairResult{}, err
	}
	if n > 0 {
		return core.RepairResult{}, errcode.New(errcode.NeedsRepair,
			fmt.Sprintf("change %s has an unresolved effect: resolve its effects first", changeID))
	}
	if f.retry {
		if err := noRepairInput(req); err != nil {
			return core.RepairResult{}, err
		}
	}
	if res, err := e.recovererRepair(ctx, changeID); err != nil || res != nil {
		if err != nil {
			return core.RepairResult{}, err
		}
		return *res, nil
	}
	h, s := def.Handler, def.Spec
	switch {
	case s.Class == core.ClassCompensable && h.Inverse != nil:
		if err := noRepairInput(req); err != nil {
			return core.RepairResult{}, err
		}
		v, err := e.Undo(ctx, p, changeID, nil)
		if err != nil {
			return core.RepairResult{}, err
		}
		return core.RepairResult{Change: &v}, nil
	case s.Class == core.ClassRevertible:
		rdef, ok := repairedByDef(e.cfg.Registry, e.cfg.Side, s.RepairedBy)
		if !ok {
			return core.RepairResult{}, fmt.Errorf("lifecycle: %s names RepairedBy %q, which is not registered", def.Ref(), s.RepairedBy)
		}
		input := req.Input
		if len(bytes.TrimSpace(input)) == 0 {
			input = json.RawMessage(`{}`)
		}
		v, err := e.planRepair(ctx, p, rdef, input, changeID)
		if err != nil {
			return core.RepairResult{}, err
		}
		return core.RepairResult{Plan: &v}, nil
	case s.Class == core.ClassCompensable && s.Converges && h.Inverse == nil && !s.Task && h.Recover == nil:
		if err := noRepairInput(req); err != nil {
			return core.RepairResult{}, err
		}
		input, err := e.storedRepairInput(ctx, q, f)
		if err != nil {
			return core.RepairResult{}, err
		}
		v, err := e.planRepair(ctx, p, def, input, changeID)
		if err != nil {
			return core.RepairResult{}, err
		}
		return core.RepairResult{Plan: &v}, nil
	}
	return core.RepairResult{}, errcode.New(errcode.ChangeNotPending,
		fmt.Sprintf("change %s has no repair; release it", changeID))
}

// repairRequestRefusal checks a repair request's shape: a step decision is
// for a composite, and an input is an object that never names repairs,
// which only the repair route sets.
func repairRequestRefusal(f changeFacts, req core.RepairRequest) error {
	if f.class != core.ClassComposite && (req.Step != "" || req.As != "") {
		return &errcode.Error{
			Code: errcode.InvalidInput, Detail: "a step decision repairs only a composite",
			Fields: []errcode.FieldError{{Path: "/step", Message: "only a composite has steps"}},
		}
	}
	if len(bytes.TrimSpace(req.Input)) == 0 {
		return nil
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(req.Input, &m); err != nil || m == nil {
		return &errcode.Error{
			Code: errcode.InvalidInput, Detail: "a repair input is a JSON object",
			Fields: []errcode.FieldError{{Path: "/input", Message: "must be an object"}},
		}
	}
	if _, ok := m["repairs"]; ok {
		return &errcode.Error{
			Code: errcode.InvalidInput, Detail: "a repair input never carries repairs",
			Fields: []errcode.FieldError{{Path: "/input/repairs", Message: "is set only by the repair route"}},
		}
	}
	return nil
}

// noRepairInput refuses an input for a repair that takes none: an undo, or
// a re-apply that uses the change's stored input.
func noRepairInput(req core.RepairRequest) error {
	if len(bytes.TrimSpace(req.Input)) == 0 {
		return nil
	}
	return &errcode.Error{
		Code: errcode.InvalidInput, Detail: "this change is repaired without input",
		Fields: []errcode.FieldError{{Path: "/input", Message: "must be absent"}},
	}
}

// repairedByDef returns the newest registered version of the action name
// on side.
func repairedByDef(reg *core.Registry, side core.Side, name string) (core.ActionDef, bool) {
	var best core.ActionDef
	found := false
	for _, a := range reg.Actions() {
		if a.Spec.Name == name && a.Spec.Side == side && (!found || a.Spec.Version > best.Spec.Version) {
			best, found = a, true
		}
	}
	return best, found
}

// checkRepairedBy refuses a registry in which a RepairedBy action of this
// side does not accept the input {}, which a repair request without input
// plans it with.
func checkRepairedBy(cfg Config) error {
	for _, a := range cfg.Registry.Actions() {
		if a.Spec.Side != cfg.Side || a.Spec.RepairedBy == "" {
			continue
		}
		rdef, ok := repairedByDef(cfg.Registry, cfg.Side, a.Spec.RepairedBy)
		if !ok {
			return fmt.Errorf("lifecycle: %s names RepairedBy %s, which is not registered on this side", a.Ref(), a.Spec.RepairedBy)
		}
		if err := cfg.Validator.ValidateInput(rdef, json.RawMessage(`{}`)); err != nil {
			return fmt.Errorf("lifecycle: %s, the RepairedBy action of %s, refuses the input {}: %w", rdef.Ref(), a.Ref(), err)
		}
	}
	return nil
}

// storedRepairInput opens the input a change's plan was made with; it is
// kept until the change is terminal (spec 1.2, Sealed input).
func (e *Engine) storedRepairInput(ctx context.Context, q queryer, f changeFacts) (json.RawMessage, error) {
	var ct []byte
	if err := q.QueryRowContext(ctx, "SELECT input_ct FROM core_plans WHERE id = ?", f.planID).Scan(&ct); err != nil {
		return nil, fmt.Errorf("lifecycle: read the input of change %s: %w", f.id, err)
	}
	if ct == nil {
		return nil, fmt.Errorf("lifecycle: the sealed input of change %s is gone", f.id)
	}
	in, err := e.cfg.Base.Sealer.Open(planInputAD(f.planID), ct)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: open the input of change %s: %w", f.id, err)
	}
	return in, nil
}

// planRepair is Plan for a repair change of the change repairs (spec 1.3,
// Repair of a single change). The plan records repairs in
// core_plans.repairs, from which the engine derives Env.Call.RepairOf for
// every later call of the repair change. Only the repair route makes such a
// plan, so it carries no parent's call context.
func (e *Engine) planRepair(ctx context.Context, p core.Principal, def core.ActionDef, input json.RawMessage, repairs string) (core.PlanView, error) {
	canonical, err := e.canonical(def, input)
	if err != nil {
		return core.PlanView{}, err
	}
	env := e.env(p)
	env.Call = core.CallContext{RepairOf: repairs}
	return e.makePlan(ctx, p, def, canonical, env, nil, repairs)
}

// planRepairs returns the change a plan repairs, or "" for an ordinary
// plan.
func planRepairs(ctx context.Context, q queryer, planID string) (string, error) {
	var repairs sql.NullString
	err := q.QueryRowContext(ctx, "SELECT repairs FROM core_plans WHERE id = ?", planID).Scan(&repairs)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("lifecycle: read plan %s: %w", planID, err)
	}
	return repairs.String, nil
}

// repairTargetReady is the re-check of spec 1.3, Repair change P of R: R
// is needs_repair, unowned and without recovery_retry, and neither R nor a
// failed repair change of R has an unresolved effect. A second plan made
// before an earlier attempt failed is caught here.
func repairTargetReady(ctx context.Context, q queryer, target string) error {
	f, err := loadFacts(ctx, q, target)
	if err != nil {
		return err
	}
	switch {
	case f.owner != "":
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is owned by %s", target, f.owner))
	case f.state != core.StateNeedsRepair:
		return errcode.New(errcode.ChangeBusy,
			fmt.Sprintf("change %s is %s; this repair plan no longer applies", target, f.state))
	case f.retry:
		return errcode.New(errcode.NeedsRepair,
			fmt.Sprintf("change %s: repair it: its recovery handler has not finished", target))
	}
	n, err := unresolvedCount(ctx, q, target, true)
	if err != nil {
		return err
	}
	if n > 0 {
		return errcode.New(errcode.NeedsRepair,
			fmt.Sprintf("change %s has an unresolved effect: resolve its effects first", target))
	}
	return nil
}

// takeRepairTarget runs in the intent transaction of the repair change p.
// It re-checks the target, gives it owner repair and repaired_by p, and
// moves every lock row of the target to p, so the keys are never free in
// between; p's own takeLocks then finds its keys held by p and increments
// their epochs.
func takeRepairTarget(ctx context.Context, tx *sql.Tx, now time.Time, target, p string) error {
	if err := repairTargetReady(ctx, tx, target); err != nil {
		return err
	}
	if err := claim(ctx, tx, now, target, "", core.OwnerRepair, core.StateNeedsRepair, core.StateNeedsRepair); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE core_changes SET repaired_by = ? WHERE id = ?", p, target); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, "UPDATE core_locks SET owner = ? WHERE owner = ?", p, target)
	return err
}

// repairTransition applies the table of spec 1.3, Repair change P of R,
// when changeID is a repair change whose transition tr ends its run. It
// runs inside tr's transaction before the change's row is written: it
// marks the target repaired when P completed or was confirmed, gives the
// locks back to the target when P failed or was reverted, and records as
// failed an outcome that would leave P in needs_repair, because a repair
// change is never repaired itself.
func (e *Engine) repairTransition(ctx context.Context, tx *sql.Tx, now time.Time, changeID string, tr *transition) error {
	var target, action, output sql.NullString
	if err := tx.QueryRowContext(ctx,
		`SELECT p.repairs, c.action, c.output_json FROM core_changes c JOIN core_plans p ON p.id = c.plan_id
		 WHERE c.id = ?`, changeID).Scan(&target, &action, &output); err != nil {
		return err
	}
	if target.String == "" {
		return nil
	}
	switch tr.to {
	case core.StateCompleted, core.StateConfirmed:
		out := tr.output
		if len(out) == 0 {
			out = json.RawMessage(output.String)
		}
		outcome, err := e.repairOutcomeOf(ctx, tx, changeID, action.String, target.String, out)
		if err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET state = ?, repair_outcome = ?, recovery_owner = '', updated_at = ?
			 WHERE id = ? AND state = ? AND recovery_owner = ?`,
			core.StateRepaired, outcome, formatTime(now), target.String, core.StateNeedsRepair, core.OwnerRepair)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("lifecycle: change %s is no longer held by its repair change %s", target.String, changeID)
		}
		// A repaired change is terminal, so its plan keeps only the hash.
		_, err = tx.ExecContext(ctx,
			"UPDATE core_plans SET input_ct = NULL WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
			target.String)
		return err
	case core.StateNeedsRepair:
		tr.to, tr.release, tr.keepLocks, tr.giveBack = core.StateFailed, true, false, false
		return returnRepairTarget(ctx, tx, now, changeID, target.String)
	case core.StateFailed, core.StateReverted, core.StateInterrupted:
		return returnRepairTarget(ctx, tx, now, changeID, target.String)
	}
	return nil
}

// returnRepairTarget moves the locks of the repair change p back to its
// target and ends the target's repair ownership.
func returnRepairTarget(ctx context.Context, tx *sql.Tx, now time.Time, p, target string) error {
	if _, err := tx.ExecContext(ctx, "UPDATE core_locks SET owner = ? WHERE owner = ?", target, p); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx,
		"UPDATE core_changes SET recovery_owner = '', updated_at = ? WHERE id = ? AND recovery_owner = ?",
		formatTime(now), target, core.OwnerRepair)
	return err
}

// repairOutcomeOf is the repairOutcome a completed repair change reports
// relative to its target: a re-apply of the target's own action leaves the
// host in the target's intended state, so it is always kept; a RepairedBy
// action reports it in its output field repairOutcome. A missing or unknown
// value is stored as NULL and logged.
func (e *Engine) repairOutcomeOf(ctx context.Context, tx *sql.Tx, p, pAction, target string, out json.RawMessage) (any, error) {
	var rAction string
	if err := tx.QueryRowContext(ctx, "SELECT action FROM core_changes WHERE id = ?", target).Scan(&rAction); err != nil {
		return nil, err
	}
	if pAction == rAction {
		return core.RepairKept, nil
	}
	var o struct {
		RepairOutcome string `json:"repairOutcome"`
	}
	if json.Unmarshal(out, &o) == nil && (o.RepairOutcome == core.RepairRestored || o.RepairOutcome == core.RepairKept) {
		return o.RepairOutcome, nil
	}
	e.cfg.Base.Log.Warn("lifecycle: a repair change reported no repairOutcome", "change", p, "target", target)
	return nil, nil
}

package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"

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

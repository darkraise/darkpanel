package lifecycle

import (
	"encoding/json"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// guardOverrides returns the overrides of a guarded action's canonical input,
// refusing an entry that is not an Overridable guard of the action with
// request.invalid_input (spec 1.5). An action without Guards has none.
func guardOverrides(def core.ActionDef, input json.RawMessage) ([]string, error) {
	if len(def.Spec.Guards) == 0 {
		return nil, nil
	}
	var in struct {
		Overrides []string `json:"overrides"`
	}
	if err := json.Unmarshal(input, &in); err != nil {
		return nil, fmt.Errorf("lifecycle: read the overrides of %s: %w", def.Ref(), err)
	}
	var fields []errcode.FieldError
	for i, id := range in.Overrides {
		if !slices.ContainsFunc(def.Spec.Guards, func(g core.GuardSpec) bool { return g.ID == id && g.Overridable }) {
			fields = append(fields, errcode.FieldError{
				Path: fmt.Sprintf("/overrides/%d", i), Message: fmt.Sprintf("%s has no overridable guard %q", def.Ref(), id),
			})
		}
	}
	if len(fields) > 0 {
		return nil, &errcode.Error{Code: errcode.InvalidInput, Detail: "overrides names a guard that cannot be overridden", Fields: fields}
	}
	return in.Overrides, nil
}

// checkGuards checks the guards a guarded action's Plan reported against its
// declaration (spec 1.5). An undeclared id, or a guard reported overridden
// that is not Overridable, not in overrides or not passing, is the module's
// mistake and fails with internal. It sets each guard's Overridable from its
// GuardSpec and adds one diff line per overridden guard, so the plan, the
// diff and the audit record the override. An action without Guards is left
// as it is.
func checkGuards(def core.ActionDef, overrides []string, out *core.PlanOutcome) error {
	if len(def.Spec.Guards) == 0 {
		return nil
	}
	for i := range out.Guards {
		g := &out.Guards[i]
		at := slices.IndexFunc(def.Spec.Guards, func(s core.GuardSpec) bool { return s.ID == g.ID })
		if at < 0 {
			return fmt.Errorf("lifecycle: %s reported guard %s, which it does not declare", def.Ref(), g.ID)
		}
		g.Overridable = def.Spec.Guards[at].Overridable
		if !g.Overridden {
			continue
		}
		switch {
		case !g.Overridable:
			return fmt.Errorf("lifecycle: %s reported guard %s overridden, which is not overridable", def.Ref(), g.ID)
		case !slices.Contains(overrides, g.ID):
			return fmt.Errorf("lifecycle: %s reported guard %s overridden, which the input does not override", def.Ref(), g.ID)
		case !g.OK:
			return fmt.Errorf("lifecycle: %s reported guard %s overridden and failing", def.Ref(), g.ID)
		}
		out.Diff = append(out.Diff, core.DiffLine{Op: " ", Text: "guard " + g.ID + " overridden: " + g.Message})
	}
	return nil
}

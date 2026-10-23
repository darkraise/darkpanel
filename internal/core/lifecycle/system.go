package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// systemKey marks the context of ApplySystem's apply, so that begin records
// core_changes.system = 1 without a parameter every other apply would pass as
// false.
type systemKey struct{}

// ApplySystem plans ref with input as p, refuses a plan with a failing guard
// with plan.guard_failed ("guard <id> failed: <message>"), and applies it with
// ConfirmNow, recording core_changes.system = 1 (spec 1.4). Background tasks
// apply through it: they have no operator to read a plan or confirm a change,
// so an irreversible action, whose apply needs an operator's confirmation
// phrase, is refused before it is planned.
func (e *Engine) ApplySystem(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (core.ChangeView, error) {
	def, err := e.action(ref)
	if err != nil {
		return core.ChangeView{}, err
	}
	if def.Spec.Class == core.ClassIrreversible {
		return core.ChangeView{}, fmt.Errorf("lifecycle: %s is irreversible, and a system change has no operator to confirm it", ref)
	}
	pv, err := e.Plan(ctx, p, ref, input, core.PlanOptions{})
	if err != nil {
		return core.ChangeView{}, err
	}
	for _, g := range pv.Guards {
		if !g.OK {
			return core.ChangeView{}, errcode.New(errcode.PlanGuardFailed, fmt.Sprintf("guard %s failed: %s", g.ID, g.Message))
		}
	}
	return e.Apply(context.WithValue(ctx, systemKey{}, true), p, pv.PlanID, core.ApplyOptions{ConfirmNow: true})
}

// isSystem reports whether ctx is an apply of ApplySystem.
func isSystem(ctx context.Context) bool {
	system, _ := ctx.Value(systemKey{}).(bool)
	return system
}

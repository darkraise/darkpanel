package schema

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fixtureEmpty is an input with no fields.
type fixtureEmpty struct{}

type fixtureStatus struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// fixtureHandler implements every handler interface and does nothing; the
// schema package never calls handlers.
type fixtureHandler[In, Out any] struct{}

func (fixtureHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (fixtureHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (fixtureHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) {
	return nil, nil
}

func (fixtureHandler[In, Out]) Revert(context.Context, core.Env, []byte) error { return nil }

// fixtureModule has one action per class, a controller-side break-glass
// action and a resource.
type fixtureModule struct{}

func (fixtureModule) Manifest() core.Manifest {
	h := fixtureHandler[fixtureInput, fixtureOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Version: 1, Side: core.SideAgent, LockKeys: []string{"demo"}}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Version: 1, Class: core.ClassCompensable, Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.purge", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true},
				fixtureHandler[fixtureEmpty, fixtureOutput]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (fixtureStatus, error) {
				return fixtureStatus{}, nil
			}),
		},
	}
}

// fixtureRegistry returns a registry holding only fixtureModule.
func fixtureRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(fixtureModule{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// fixtureAction returns one action of fixtureModule.
func fixtureAction(t *testing.T, name string) core.ActionDef {
	t.Helper()
	def, ok := fixtureRegistry(t).Action(core.ActionRef{Name: name, Version: 1})
	if !ok {
		t.Fatalf("fixture action %s missing", name)
	}
	return def
}

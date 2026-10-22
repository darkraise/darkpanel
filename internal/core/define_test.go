package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type defineIn struct {
	Name string `json:"name"`
}

type defineOut struct {
	Done string `json:"done"`
}

// defineMutator is a Mutator and nothing more.
type defineMutator struct{}

func (defineMutator) Plan(_ context.Context, _ Env, in defineIn) (PlanOutcome, error) {
	return PlanOutcome{StateHash: "plan " + in.Name}, nil
}

func (defineMutator) Apply(_ context.Context, _ Env, in defineIn) (defineOut, error) {
	return defineOut{"apply " + in.Name}, nil
}

func (defineMutator) Verify(_ context.Context, _ Env, in defineIn, out defineOut) error {
	if out.Done != "apply "+in.Name && out.Done != "task "+in.Name {
		return errors.New("not done")
	}
	return nil
}

func (defineMutator) Snapshot(_ context.Context, _ Env, in defineIn) ([]byte, error) {
	return []byte(in.Name), nil
}

func (defineMutator) Revert(context.Context, Env, []byte) (RevertOutcome, error) {
	return RevertOutcome{}, nil
}

func (defineMutator) Task(_ context.Context, env Env, in defineIn) (defineOut, error) {
	return defineOut{"task " + in.Name + " of " + env.ChangeID}, nil
}

// defineHandlers adds every optional lifecycle handler to defineMutator; each
// answer carries what it was given, so a test sees it passed through.
type defineHandlers struct{ defineMutator }

func (defineHandlers) Observe(_ context.Context, env Env, e Effect) (Observation, error) {
	return Observation{Resource: e.Resource + "@" + env.ChangeID, Matches: e.Seq}, nil
}

func (defineHandlers) Resolved(_ context.Context, _ *sql.Tx, e Effect, resolution string) error {
	return errors.New(resolution + " " + e.Resource)
}

func (defineHandlers) Inverse(_ context.Context, _ Env, e Effect) (InverseResult, error) {
	return InverseResult{Resource: "undo " + e.Resource}, nil
}

func (defineHandlers) Recover(_ context.Context, env Env) (RecoveryOutcome, error) {
	return RecoveryOutcome{State: RecoveryRolledBack, Reason: env.ChangeID}, nil
}

type defineComposer struct{}

func (defineComposer) Steps(_ context.Context, _ Env, in defineIn) ([]Step, error) {
	return []Step{{ID: "one", Action: ActionRef{"other.item.add", 1}, Input: json.RawMessage(`{"name":"` + in.Name + `"}`)}}, nil
}

func (defineComposer) Output(_ context.Context, _ Env, in defineIn, steps map[string]json.RawMessage) (defineOut, error) {
	return defineOut{in.Name + " " + string(steps["one"])}, nil
}

func TestDefineFindsLifecycleHandlers(t *testing.T) {
	spec := ActionSpec{Name: "demo.item.add", Version: 1, Class: ClassCompensable, Side: SideAgent, LockKeys: []string{"demo"}}
	tests := []struct {
		name  string
		def   ActionDef
		found bool
	}{
		{"mutating without handlers", DefineMutating[defineIn, defineOut](spec, defineMutator{}), false},
		{"mutating with handlers", DefineMutating[defineIn, defineOut](spec, defineHandlers{}), true},
		{"revertible without handlers", DefineRevertible[defineIn, defineOut](spec, defineMutator{}), false},
		{"revertible with handlers", DefineRevertible[defineIn, defineOut](spec, defineHandlers{}), true},
		{"task without handlers", DefineTask[defineIn, defineOut](spec, defineMutator{}), false},
		{"task with handlers", DefineTask[defineIn, defineOut](spec, defineHandlers{}), true},
	}
	ctx := context.Background()
	env := Env{ChangeID: "c1"}
	e := Effect{ChangeID: "c1", Seq: 3, Kind: EffectModify, Resource: "demo.item:a"}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := tt.def.Handler
			if !tt.found {
				if h.Observe != nil || h.Resolved != nil || h.Inverse != nil || h.Recover != nil {
					t.Fatalf("handlers found on a handler without them: %+v", h)
				}
				return
			}
			if h.Observe == nil || h.Resolved == nil || h.Inverse == nil || h.Recover == nil {
				t.Fatalf("a handler was not found: %+v", h)
			}
			if obs, err := h.Observe(ctx, env, e); err != nil || obs.Resource != "demo.item:a@c1" || obs.Matches != 3 {
				t.Errorf("Observe = %+v, %v", obs, err)
			}
			if err := h.Resolved(ctx, nil, e, ResolutionAccept); err == nil || err.Error() != "accept demo.item:a" {
				t.Errorf("Resolved = %v", err)
			}
			if res, err := h.Inverse(ctx, env, e); err != nil || res.Resource != "undo demo.item:a" {
				t.Errorf("Inverse = %+v, %v", res, err)
			}
			if out, err := h.Recover(ctx, env); err != nil || out.State != RecoveryRolledBack || out.Reason != "c1" {
				t.Errorf("Recover = %+v, %v", out, err)
			}
		})
	}
}

func TestDefineComposite(t *testing.T) {
	def := DefineComposite[defineIn, defineOut](ActionSpec{
		Name: "demo.stack.deploy", Version: 2, Side: SideAgent, LockKeys: []string{"demo"}, Unwind: UnwindReverse,
	}, defineComposer{})
	s, h := def.Spec, def.Handler
	if s.Class != ClassComposite || s.Side != SideController || s.Unwind != UnwindReverse ||
		!reflect.DeepEqual(s.LockKeys, []string{"composite:demo"}) {
		t.Errorf("spec = %+v, want a controller-side composite holding composite:demo", s)
	}
	if h.Plan != nil || h.Apply != nil || h.Verify != nil || h.Read != nil || h.Task != nil {
		t.Errorf("a composite has a mutator handler: %+v", h)
	}
	ctx := context.Background()
	steps, err := h.Steps(ctx, Env{}, json.RawMessage(`{"name":"web"}`))
	if err != nil || len(steps) != 1 || steps[0].ID != "one" || string(steps[0].Input) != `{"name":"web"}` {
		t.Errorf("Steps = %+v, %v", steps, err)
	}
	out, err := h.Output(ctx, Env{}, json.RawMessage(`{"name":"web"}`), map[string]json.RawMessage{"one": json.RawMessage(`{"id":1}`)})
	if err != nil || string(out) != `{"done":"web {\"id\":1}"}` {
		t.Errorf("Output = %s, %v", out, err)
	}
}

func TestDefineTask(t *testing.T) {
	spec := ActionSpec{Name: "demo.package.install", Version: 1, Class: ClassIrreversible, Side: SideAgent,
		LockKeys: []string{"demo"}, ApplyTimeout: 15 * time.Minute}
	def := DefineTask[defineIn, defineOut](spec, defineMutator{})
	s, h := def.Spec, def.Handler
	if !s.Task || !s.LongRunning || s.Class != ClassIrreversible || s.ApplyTimeout != 15*time.Minute {
		t.Errorf("spec = %+v, want a long-running irreversible task action", s)
	}
	if h.Apply != nil || h.Task == nil || h.Plan == nil || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
		t.Fatalf("handler = %+v, want Plan, Task and Verify only", h)
	}
	ctx := context.Background()
	out, err := h.Task(ctx, Env{ChangeID: "c7"}, json.RawMessage(`{"name":"x"}`))
	if err != nil || string(out) != `{"done":"task x of c7"}` {
		t.Errorf("Task = %s, %v", out, err)
	}
	if plan, err := h.Plan(ctx, Env{}, json.RawMessage(`{"name":"x"}`)); err != nil || plan.StateHash != "plan x" {
		t.Errorf("Plan = %+v, %v", plan, err)
	}
	if err := h.Verify(ctx, Env{}, json.RawMessage(`{"name":"x"}`), json.RawMessage(`{"done":"task x"}`)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestDefinedHandlersRefuseInputOfAnotherType(t *testing.T) {
	ctx := context.Background()
	bad := json.RawMessage(`{"name":"x","extra":true}`)
	composite := DefineComposite[defineIn, defineOut](ActionSpec{Name: "demo.stack.deploy", Version: 1, Unwind: UnwindForward}, defineComposer{}).Handler
	task := DefineTask[defineIn, defineOut](ActionSpec{Name: "demo.package.install", Version: 1, Class: ClassCompensable}, defineMutator{}).Handler
	calls := map[string]func() error{
		"composite steps":  func() error { _, err := composite.Steps(ctx, Env{}, bad); return err },
		"composite output": func() error { _, err := composite.Output(ctx, Env{}, bad, nil); return err },
		"task":             func() error { _, err := task.Task(ctx, Env{}, bad); return err },
		"task plan":        func() error { _, err := task.Plan(ctx, Env{}, bad); return err },
		"task verify":      func() error { return task.Verify(ctx, Env{}, bad, json.RawMessage(`{}`)) },
	}
	for name, call := range calls {
		var coded *errcode.Error
		if err := call(); !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
			t.Errorf("%s: err = %v, want %s", name, err, errcode.InvalidInput)
		}
	}
}

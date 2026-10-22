package lifecycle

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// The test module "mu" records which unit client each of its handler calls
// got. muUnits tags a client with the module ModuleUnits made it for.

var (
	muRefSet = core.ActionRef{Name: "mu.item.set", Version: 1} // compensable with an undo handler
	muRefGet = core.ActionRef{Name: "mu.item.get", Version: 1}
)

// muUnits is a core.Units the tests compare and never call.
type muUnits struct {
	core.Units
	module string
}

func muModuleUnits(module string) core.Units { return muUnits{module: module} }

type muHost struct {
	mu    sync.Mutex
	value string
	saw   []string // "<call>=<module of Env.Units>", or "<call>=nil" without a client
}

func (h *muHost) see(call string, env core.Env) {
	tag := "nil"
	if env.Units != nil {
		tag = env.Units.(muUnits).module
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.saw = append(h.saw, call+"="+tag)
}

func (h *muHost) seen() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return slices.Clone(h.saw)
}

func (h *muHost) get() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.value
}

func (h *muHost) set(v string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.value = v
}

type muValue struct {
	Value string `json:"value"`
}

func muDoc(v string) core.StateDoc {
	b, err := json.Marshal(muValue{Value: v})
	if err != nil {
		panic(err)
	}
	return core.StateDoc{State: b}
}

type muHandler struct{ h *muHost }

func (x muHandler) Plan(_ context.Context, env core.Env, in muValue) (core.PlanOutcome, error) {
	x.h.see("Plan", env)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Value}}, StateHash: x.h.get()}, nil
}

func (x muHandler) Apply(ctx context.Context, env core.Env, in muValue) (muValue, error) {
	x.h.see("Apply", env)
	after := muDoc(in.Value)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: "mu.item:x", Local: true, InverseLocal: true, Before: muDoc(x.h.get()), After: &after,
	}, nil)
	if err != nil {
		return muValue{}, err
	}
	x.h.set(in.Value)
	return in, env.Effects.Made(ctx, seq, "mu.item:x", after)
}

func (x muHandler) Verify(_ context.Context, env core.Env, _ muValue, _ muValue) error {
	x.h.see("Verify", env)
	return nil
}

func (x muHandler) Observe(_ context.Context, env core.Env, _ core.Effect) (core.Observation, error) {
	x.h.see("Observe", env)
	return core.Observation{Current: muDoc(x.h.get()), Resource: "mu.item:x", Matches: 1, Exclusive: true}, nil
}

func (x muHandler) Inverse(_ context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	x.h.see("Inverse", env)
	var v muValue
	if err := json.Unmarshal(e.UndoTo.State, &v); err != nil {
		return core.InverseResult{}, err
	}
	x.h.set(v.Value)
	return core.InverseResult{}, nil
}

type muGetHandler struct{ h *muHost }

func (x muGetHandler) Read(_ context.Context, env core.Env, _ muValue) (muValue, error) {
	x.h.see("Read", env)
	return muValue{Value: x.h.get()}, nil
}

type muModule struct{ h *muHost }

func (m muModule) Manifest() core.Manifest {
	return core.Manifest{ID: "mu", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: muRefSet.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{"mu"},
		}, muHandler(m)),
		core.DefineRead(core.ActionSpec{Name: muRefGet.Name, Version: 1, Side: core.SideAgent}, muGetHandler(m)),
	}}
}

// muRun plans, applies and undoes one mu change and reads mu once, so every
// handler call of the module runs.
func muRun(t *testing.T, f *fixture) {
	t.Helper()
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, muRefSet, `{"value":"a"}`).PlanID, core.ApplyOptions{})
	if v.State != core.StateCompleted {
		t.Fatalf("change = %s, want completed", v.State)
	}
	if _, err := f.eng.Read(ctx, alice, muRefGet, json.RawMessage(`{"value":""}`)); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Undo(ctx, alice, v.ID, nil); err != nil {
		t.Fatal(err)
	}
	if v = f.settled(v.ID); v.State != core.StateReverted {
		t.Fatalf("undone change = %s, want reverted", v.State)
	}
}

// muWant fails unless every call in saw carries tag and saw holds each of
// the handler calls a change, a read and an undo make.
func muWant(t *testing.T, saw []string, tag string) {
	t.Helper()
	calls := map[string]bool{}
	for _, s := range saw {
		call, got, _ := strings.Cut(s, "=")
		if got != tag {
			t.Errorf("%s saw units %s, want %s", call, got, tag)
		}
		calls[call] = true
	}
	for _, call := range []string{"Plan", "Apply", "Verify", "Read", "Observe", "Inverse"} {
		if !calls[call] {
			t.Errorf("no %s call was seen; calls %v", call, saw)
		}
	}
}

func TestHandlersGetTheirModuleUnits(t *testing.T) {
	h := &muHost{}
	f := newFixtureWith(t, muModule{h})
	f.moduleUnits = muModuleUnits
	f.restart()
	muRun(t, f)
	muWant(t, h.seen(), "mu")
}

func TestNilModuleUnits(t *testing.T) {
	h := &muHost{}
	f := newFixtureWith(t, muModule{h})
	f.eng.Close()
	cfg := f.config()
	cfg.Base.Units = muUnits{module: "base"}
	eng, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	f.eng = eng
	t.Cleanup(eng.Close)
	muRun(t, f)
	muWant(t, h.seen(), "nil")
}

func TestTaskProcessGetsModuleUnits(t *testing.T) {
	f := newTaskFixture(t)
	f.moduleUnits = muModuleUnits
	f.restartTask()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	if err := f.eng.RunTask(context.Background(), v.ID, core.TaskOpApply, 1); err != nil {
		t.Fatal(err)
	}
	f.tsk.mu.Lock()
	defer f.tsk.mu.Unlock()
	if len(f.tsk.envs) != 1 || f.tsk.envs[0].Units != (muUnits{module: "tsk"}) {
		t.Errorf("Task envs = %+v, want one with the tsk module's units", f.tsk.envs)
	}
}

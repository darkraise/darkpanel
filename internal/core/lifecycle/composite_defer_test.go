package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

var (
	refTdfSet    = core.ActionRef{Name: "tdf.gate.set", Version: 1}    // revertible; its plan refuses until the cst item it names exists
	refTdfRepair = core.ActionRef{Name: "tdf.gate.repair", Version: 1} // tdf.gate.set's repair action
)

// tdfHost is the state of tdf.gate.set: cst is the fixture's host, set once the fixture exists.
type tdfHost struct {
	mu     sync.Mutex
	cst    *cstHost
	refuse bool
	set    bool
}

type tdfIn struct {
	After string `json:"after"` // the cst item that must exist before the gate can be planned
}

type tdfOut struct {
	Set bool `json:"set"`
}

type tdfGateHandler struct{ h *tdfHost }

func (x tdfGateHandler) Plan(_ context.Context, _ core.Env, in tdfIn) (core.PlanOutcome, error) {
	x.h.mu.Lock()
	refuse := x.h.refuse
	x.h.mu.Unlock()
	if _, ok := x.h.cst.item(in.After); !ok || refuse {
		return core.PlanOutcome{}, errors.New("tdf: " + in.After + " is not there yet")
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "gate"}}, StateHash: "gate"}, nil
}

func (x tdfGateHandler) Apply(context.Context, core.Env, tdfIn) (tdfOut, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.set = true
	return tdfOut{Set: true}, nil
}

func (tdfGateHandler) Verify(context.Context, core.Env, tdfIn, tdfOut) error { return nil }

func (tdfGateHandler) Snapshot(context.Context, core.Env, tdfIn) ([]byte, error) {
	return json.Marshal(false)
}

func (x tdfGateHandler) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.set = false
	return core.RevertOutcome{}, nil
}

type tdfModule struct{ h *tdfHost }

func (m tdfModule) Manifest() core.Manifest {
	return core.Manifest{ID: "tdf", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{
			Name: refTdfSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"tdf-gate"}, RepairedBy: refTdfRepair.Name,
		}, tdfGateHandler(m)),
		core.DefineMutating(core.ActionSpec{
			Name: refTdfRepair.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"tdf-gate"}, ForwardOnly: true, Converges: true,
		}, cstFix{}),
	}}
}

// newDeferFixture runs cfx.bad.run as a → b, where a is cst.item.add and b, the revertible last step,
// is tdf.gate.set with DeferPlan: b's plan refuses until a's item exists.
func newDeferFixture(t *testing.T, refuse bool) (*cfxFixture, *tdfHost) {
	t.Helper()
	h := &tdfHost{refuse: refuse}
	f := newCfxFixture(t, tdfModule{h})
	h.cst = f.host
	b := core.Step{ID: "b", Host: testHost, Action: refTdfSet, Input: json.RawMessage(`{"after":"dfr-a"}`), After: []string{"a"}, DeferPlan: true}
	f.bad.set(cstStep("a", refCstAdd, `{"name":"dfr-a","value":"1"}`), b)
	return f, h
}

func TestDeferredStepIsPlannedAfterItsPredecessor(t *testing.T) {
	f, h := newDeferFixture(t, false)
	if _, err := f.planC(refCfxBad, `{"name":"x"}`); err != nil {
		t.Fatalf("plan with a deferred step failed: %v", err)
	}
	ch := f.applyC(refCfxBad, `{"name":"x"}`, core.ApplyOptions{})
	f.await(ch.ID, core.StateAwaitingConfirmation)
	h.mu.Lock()
	set := h.set
	h.mu.Unlock()
	if _, ok := f.host.item("dfr-a"); !ok || !set || f.stepsOf(ch.ID)["b"].State != core.StepApplied {
		t.Fatalf("b not applied after a: set=%v steps=%+v", set, f.stepsOf(ch.ID))
	}
}

func TestDeferredStepRefusalUndoesPredecessors(t *testing.T) {
	f, h := newDeferFixture(t, true)
	if _, err := f.planC(refCfxBad, `{"name":"x"}`); err != nil {
		t.Fatal(err)
	}
	ch := f.applyC(refCfxBad, `{"name":"x"}`, core.ApplyOptions{})
	f.await(ch.ID, core.StateReverted)
	h.mu.Lock()
	set := h.set
	h.mu.Unlock()
	if _, ok := f.host.item("dfr-a"); ok || set {
		t.Fatalf("a not undone after b's plan refused at its cursor: set=%v", set)
	}
}

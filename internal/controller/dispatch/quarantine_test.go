package dispatch

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// qgGate is the tests' Config.Quarantine: it refuses while on.
type qgGate struct {
	on    atomic.Bool
	calls atomic.Int32
}

func (g *qgGate) check(context.Context) error {
	g.calls.Add(1)
	if g.on.Load() {
		return errcode.New(errcode.Quarantined, "the test controller is quarantined")
	}
	return nil
}

func qgDispatcher(t *testing.T) (*harness, *Dispatcher, *qgGate) {
	t.Helper()
	h := newHarness(t)
	g := &qgGate{}
	cfg := h.config(&coretest.MemAudit{})
	cfg.Quarantine = g.check
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, g
}

func TestQuarantineRefusesMutations(t *testing.T) {
	h, d, g := qgDispatcher(t)
	ctx := context.Background()
	confirmable := h.applyAgent(d, "a", core.ApplyOptions{})
	done := h.applyCtl(d, "x")
	agentPlan := h.plan(d, testHost, refSet, `{"name":"c","value":"1"}`)
	ctlPlan := h.plan(d, "", refCtlSet, `{"name":"n","value":"y"}`)

	g.on.Store(true)
	_, err := d.Apply(ctx, alice, testHost, refSet, agentPlan.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Apply(ctx, alice, "", refCtlSet, ctlPlan.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Undo(ctx, alice, done.ID)
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Repair(ctx, alice, done.ID, core.RepairRequest{})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Release(ctx, alice, done.ID, "given up")
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Resolve(ctx, alice, done.ID, 1, core.ResolveRequest{Resolution: core.ResolutionAccept})
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Children().Undo(ctx, alice, "", done.ID, nil)
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Children().Release(ctx, alice, testHost, confirmable.ID, "given up", nil)
	_ = wantCode(t, err, errcode.Quarantined)

	// Plans, reads, confirm and revert stay admitted.
	h.plan(d, testHost, refSet, `{"name":"d","value":"1"}`)
	if _, err := d.Read(ctx, alice, testHost, refGet, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("read while quarantined: %v", err)
	}
	if v, err := d.Confirm(ctx, alice, confirmable.ID); err != nil || v.State != core.StateConfirmed {
		t.Errorf("confirm while quarantined = %+v, %v; want confirmed", v, err)
	}
	// kv.item.set holds its lock while pending, so the change to revert is
	// applied once the first is confirmed.
	g.on.Store(false)
	revertible := h.applyAgent(d, "b", core.ApplyOptions{})
	g.on.Store(true)
	if v, err := d.Revert(ctx, alice, revertible.ID); err != nil || v.State != core.StateReverted {
		t.Errorf("revert while quarantined = %+v, %v; want reverted", v, err)
	}

	// A refused apply left its plan unapplied, so it applies once released.
	g.on.Store(false)
	v, err := d.Apply(ctx, alice, testHost, refSet, agentPlan.PlanID, core.ApplyOptions{})
	if err != nil || v.State != core.StatePending {
		t.Errorf("apply after the quarantine = %+v, %v; want pending", v, err)
	}
}

// TestQuarantineGateRunsFirst: a quarantined controller refuses before it
// looks the change up, so an unknown change is refused the same way.
func TestQuarantineGateRunsFirst(t *testing.T) {
	_, d, g := qgDispatcher(t)
	ctx := context.Background()
	g.on.Store(true)
	_, err := d.Undo(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.Quarantined)
	_, err = d.Apply(ctx, alice, testHost, refSet, "01UNKNOWN", core.ApplyOptions{})
	_ = wantCode(t, err, errcode.Quarantined)
	if n := g.calls.Load(); n != 2 {
		t.Errorf("gate calls = %d, want 2", n)
	}
}

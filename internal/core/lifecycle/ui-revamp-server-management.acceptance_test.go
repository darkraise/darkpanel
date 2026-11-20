package lifecycle

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Acceptance tests of the ui-revamp-server-management spec, change engine.
// The q module has the shape section 4 gives system.docker.install: a
// controller-side check read, a compensable engine install, and the firewall
// integration as the revertible last step whose plan refuses while the
// engine is absent.

type qFlagIn struct {
	FirewallIntegration bool `json:"firewallIntegration"`
}

// qCheck refuses when the flag differs from the host's firewall state.
type qCheck struct{ hostFirewallOn *atomic.Bool }

func (c qCheck) Read(_ context.Context, _ core.Env, in qFlagIn) (xEmpty, error) {
	if in.FirewallIntegration != c.hostFirewallOn.Load() {
		return xEmpty{}, errcode.New(errcode.InvalidInput, "the firewall integration flag differs from the host")
	}
	return xEmpty{}, nil
}

type qInstall struct{}

func (qInstall) Steps(_ context.Context, _ core.Env, in qFlagIn) ([]core.Step, error) {
	steps := []core.Step{
		{ID: "check", Action: core.ActionRef{Name: "q.docker.check", Version: 1}, Input: json.RawMessage(`{"firewallIntegration":` + map[bool]string{true: "true", false: "false"}[in.FirewallIntegration] + `}`)},
		{ID: "engine", Host: testHost, Action: core.ActionRef{Name: "q.docker.engine", Version: 1}, Input: json.RawMessage(`{}`), After: []string{"check"}},
	}
	if in.FirewallIntegration {
		steps = append(steps, core.Step{
			ID: "firewall", Host: testHost, Action: core.ActionRef{Name: "q.docker.fw", Version: 1}, Input: json.RawMessage(`{}`),
			After: []string{"engine"}, DeferPlan: true,
		})
	}
	return steps, nil
}

func (qInstall) Output(context.Context, core.Env, qFlagIn, map[string]json.RawMessage) (xEmpty, error) {
	return xEmpty{}, nil
}

type qModule struct{ installed, undone, fwOn *atomic.Bool }

func (m qModule) Manifest() core.Manifest {
	return core.Manifest{ID: "q", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "q.docker.check", Version: 1, Side: core.SideController}, qCheck{m.fwOn}),
		core.DefineMutating(core.ActionSpec{
			Name: "q.docker.engine", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{"q-apt"}, Converges: true,
		}, xEngine{installed: m.installed, undone: m.undone}),
		core.DefineRevertible(core.ActionSpec{
			Name: "q.docker.fw", Version: 1, Side: core.SideAgent, LockKeys: []string{"q-fw"}, RepairedBy: "q.docker.fwfix",
		}, xFirewall{installed: m.installed}),
		core.DefineMutating(core.ActionSpec{
			Name: "q.docker.fwfix", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"q-fw"}, ForwardOnly: true, Converges: true,
		}, cstFix{}),
		core.DefineComposite[qFlagIn, xEmpty](core.ActionSpec{Name: "q.docker.install", Version: 1, Unwind: core.UnwindReverse}, qInstall{}),
	}}
}

var refQInstall = core.ActionRef{Name: "q.docker.install", Version: 1}

func TestA42_AnUnconfirmedFirewallStepRollsTheWholeInstallBack(t *testing.T) {
	var installed, undone, fwOn atomic.Bool
	fwOn.Store(true)
	f := newCfxFixture(t, qModule{&installed, &undone, &fwOn})
	ch := f.applyC(refQInstall, `{"firewallIntegration":true}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	f.await(ch.ID, core.StateAwaitingConfirmation)
	if !installed.Load() {
		t.Fatal("the engine was not installed before the firewall confirmation")
	}
	f.agentClock.Advance(15*time.Minute + time.Second)
	f.await(ch.ID, core.StateReverted)
	if installed.Load() || !undone.Load() {
		t.Fatalf("the engine install was not undone after the confirmation window ran out: installed=%v undone=%v", installed.Load(), undone.Load())
	}
}

func TestA42_ARevertedFirewallStepRollsTheWholeInstallBack(t *testing.T) {
	var installed, undone, fwOn atomic.Bool
	fwOn.Store(true)
	f := newCfxFixture(t, qModule{&installed, &undone, &fwOn})
	ch := f.applyC(refQInstall, `{"firewallIntegration":true}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	f.await(ch.ID, core.StateAwaitingConfirmation)
	f.revertC(ch.ID)
	f.await(ch.ID, core.StateReverted)
	if installed.Load() || !undone.Load() {
		t.Fatalf("revert did not undo the engine install: installed=%v undone=%v", installed.Load(), undone.Load())
	}
}

func TestA57_AFlagThatDiffersFromTheHostFailsAtTheCheckStepAndChangesNothing(t *testing.T) {
	var installed, undone, fwOn atomic.Bool // the host's firewall integration is off
	f := newCfxFixture(t, qModule{&installed, &undone, &fwOn})
	if _, err := f.planC(refQInstall, `{"firewallIntegration":true}`); err != nil {
		t.Fatalf("the plan must not run the read step; it was refused: %v", err)
	}
	ch := f.applyC(refQInstall, `{"firewallIntegration":true}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	done := f.await(ch.ID, core.StateFailed, core.StateReverted)
	if done.State != core.StateFailed {
		t.Fatalf("state = %s, want failed at the check step", done.State)
	}
	if installed.Load() || undone.Load() {
		t.Fatalf("the engine step ran: installed=%v undone=%v", installed.Load(), undone.Load())
	}
	if st := f.stepsOf(ch.ID)["check"].State; st == core.StepApplied {
		t.Fatalf("check step state = %s, want it failed", st)
	}
}

func TestA57_TheFirewallStepIsPlannedOnlyAfterTheEngineStepApplied(t *testing.T) {
	var installed, undone, fwOn atomic.Bool
	fwOn.Store(true)
	f := newCfxFixture(t, qModule{&installed, &undone, &fwOn})
	// The firewall step's own plan refuses while the engine is absent, so a
	// plan that accepted the composite did not plan it.
	if _, err := f.planC(refQInstall, `{"firewallIntegration":true}`); err != nil {
		t.Fatalf("composite plan: %v", err)
	}
	ch := f.applyC(refQInstall, `{"firewallIntegration":true}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	f.await(ch.ID, core.StateAwaitingConfirmation)
	steps := f.stepsOf(ch.ID)
	if steps["engine"].State != core.StepApplied || steps["firewall"].State != core.StepApplied || !installed.Load() {
		t.Fatalf("steps = %+v, installed = %v", steps, installed.Load())
	}
}

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

// The x module mirrors system.docker.install's shape: a controller-side check, an agent compensable engine
// install, and the firewall integration as the revertible last step whose plan refuses until the engine is
// installed, so it must defer its plan.

type xFlagIn struct {
	FirewallIntegration bool `json:"firewallIntegration"`
}

type xEmpty struct{}

type xCheck struct{}

func (xCheck) Read(context.Context, core.Env, xFlagIn) (xEmpty, error) { return xEmpty{}, nil }

// xEngine installs by setting installed and journals the installation, so the engine can undo it.
type xEngine struct{ installed, undone *atomic.Bool }

func (xEngine) Plan(context.Context, core.Env, xEmpty) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "engine"}}, StateHash: "engine"}, nil
}

func (x xEngine) Apply(ctx context.Context, env core.Env, _ xEmpty) (xEmpty, error) {
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: "x.engine:?engine", Recovery: &core.RecoveryID{Kind: "name", Value: "engine"},
		Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")}, After: &core.StateDoc{State: json.RawMessage("true")},
	}, nil)
	if err != nil {
		return xEmpty{}, err
	}
	x.installed.Store(true)
	return xEmpty{}, env.Effects.Made(ctx, seq, "x.engine:engine", core.StateDoc{State: json.RawMessage("true")})
}

func (xEngine) Verify(context.Context, core.Env, xEmpty, xEmpty) error { return nil }

func (x xEngine) Observe(context.Context, core.Env, core.Effect) (core.Observation, error) {
	if !x.installed.Load() {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
	}
	return core.Observation{Current: core.StateDoc{State: json.RawMessage("true")}, Resource: "x.engine:engine", Matches: 1, Exclusive: true}, nil
}

func (x xEngine) Inverse(context.Context, core.Env, core.Effect) (core.InverseResult, error) {
	x.installed.Store(false)
	x.undone.Store(true)
	return core.InverseResult{}, nil
}

// xFirewall is the integration: its plan asks for the engine, as firewall.docker.enable asks the Docker daemon.
type xFirewall struct{ installed *atomic.Bool }

func (x xFirewall) Plan(context.Context, core.Env, xEmpty) (core.PlanOutcome, error) {
	if !x.installed.Load() {
		return core.PlanOutcome{}, errcode.New(errcode.FirewallUnsupportedIntegration, "docker absent")
	}
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: "integration"}}, StateHash: "fw"}, nil
}

func (xFirewall) Apply(context.Context, core.Env, xEmpty) (xEmpty, error) { return xEmpty{}, nil }
func (xFirewall) Verify(context.Context, core.Env, xEmpty, xEmpty) error  { return nil }
func (xFirewall) Snapshot(context.Context, core.Env, xEmpty) ([]byte, error) {
	return json.Marshal(false)
}
func (xFirewall) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

type xInstall struct{}

func (xInstall) Steps(_ context.Context, _ core.Env, in xFlagIn) ([]core.Step, error) {
	check, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	steps := []core.Step{
		{ID: "check", Action: core.ActionRef{Name: "x.docker.check", Version: 1}, Input: check},
		{ID: "engine", Host: testHost, Action: core.ActionRef{Name: "x.docker.engine", Version: 1}, Input: json.RawMessage(`{}`), After: []string{"check"}},
	}
	if in.FirewallIntegration {
		steps = append(steps, core.Step{
			ID: "firewall", Host: testHost, Action: core.ActionRef{Name: "x.docker.fw", Version: 1}, Input: json.RawMessage(`{}`),
			After: []string{"engine"}, DeferPlan: true,
		})
	}
	return steps, nil
}

func (xInstall) Output(context.Context, core.Env, xFlagIn, map[string]json.RawMessage) (xEmpty, error) {
	return xEmpty{}, nil
}

type xModule struct{ installed, undone *atomic.Bool }

func (m xModule) Manifest() core.Manifest {
	return core.Manifest{ID: "x", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: "x.docker.check", Version: 1, Side: core.SideController}, xCheck{}),
		core.DefineMutating(core.ActionSpec{
			Name: "x.docker.engine", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{"x-apt"}, Converges: true,
		}, xEngine(m)),
		core.DefineRevertible(core.ActionSpec{
			Name: "x.docker.fw", Version: 1, Side: core.SideAgent, LockKeys: []string{"x-fw"}, RepairedBy: "x.docker.fwfix",
		}, xFirewall{installed: m.installed}),
		core.DefineMutating(core.ActionSpec{
			Name: "x.docker.fwfix", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"x-fw"}, ForwardOnly: true, Converges: true,
		}, cstFix{}),
		core.DefineComposite[xFlagIn, xEmpty](core.ActionSpec{Name: "x.docker.install", Version: 1, Unwind: core.UnwindReverse}, xInstall{}),
	}}
}

// dockerShapeModule is the x module over installed and undone.
func dockerShapeModule(installed, undone *atomic.Bool) core.Module {
	return xModule{installed: installed, undone: undone}
}

func TestDockerInstallShapeDefersAndUndoes(t *testing.T) {
	var installed, undone atomic.Bool
	f := newCfxFixture(t, dockerShapeModule(&installed, &undone))
	install := core.ActionRef{Name: "x.docker.install", Version: 1}
	if _, err := f.planC(install, `{"firewallIntegration":true}`); err != nil {
		t.Fatalf("A57: plan refused although fw is deferred: %v", err)
	}
	ch := f.applyC(install, `{"firewallIntegration":true}`, core.ApplyOptions{ConfirmTimeout: 15 * time.Minute})
	f.await(ch.ID, core.StateAwaitingConfirmation)
	if !installed.Load() {
		t.Fatal("engine step did not apply before fw")
	}
	f.revertC(ch.ID)
	f.await(ch.ID, core.StateReverted)
	if !undone.Load() || installed.Load() {
		t.Fatal("A42: engine step not undone after the revert")
	}
}

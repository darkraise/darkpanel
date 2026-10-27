package lifecycle

import (
	"bytes"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// The test module "srx" has a revertible action that repairs itself, as
// firewall.state.repair does (owner ruling R1), and a revertible action it
// repairs. A plain apply outputs {"applied":"yes"}; an apply as a repair
// change outputs the repairOutcome its input asks for.

type srxIn struct {
	Outcome string `json:"outcome,omitempty"` // "keep", "restore", or "none" for no repairOutcome
}

type srxOut struct {
	Applied       string `json:"applied,omitempty"`
	RepairOutcome string `json:"repairOutcome,omitempty"`
}

type srxHandler struct{ h *rfxHost }

func (x srxHandler) Plan(context.Context, core.Env, srxIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{StateHash: x.h.hash()}, nil
}

func (x srxHandler) Apply(_ context.Context, env core.Env, in srxIn) (srxOut, error) {
	switch {
	case env.Call.RepairOf == "":
		return srxOut{Applied: "yes"}, nil
	case in.Outcome == "keep":
		return srxOut{RepairOutcome: core.RepairKept}, nil
	case in.Outcome == "none":
		return srxOut{}, nil
	}
	return srxOut{RepairOutcome: core.RepairRestored}, nil
}

func (srxHandler) Verify(context.Context, core.Env, srxIn, srxOut) error { return nil }

func (srxHandler) Snapshot(context.Context, core.Env, srxIn) ([]byte, error) {
	return []byte("{}"), nil
}

func (srxHandler) Revert(context.Context, core.Env, []byte) (core.RevertOutcome, error) {
	return core.RevertOutcome{}, nil
}

type srxModule struct{ h *rfxHost }

func (m srxModule) Manifest() core.Manifest {
	keys := []string{"srx"}
	return core.Manifest{ID: "srx", Version: 1, Actions: []core.ActionDef{
		core.DefineRevertible(core.ActionSpec{Name: "srx.state.repair", Version: 1, Side: core.SideAgent, LockKeys: keys,
			RepairedBy: "srx.state.repair"}, srxHandler(m)),
		core.DefineRevertible(core.ActionSpec{Name: "srx.rule.set", Version: 1, Side: core.SideAgent, LockKeys: keys,
			RepairedBy: "srx.state.repair"}, srxHandler(m)),
	}}
}

// newSrx returns an agent engine whose registry holds only the srx module
// and whose log is written to the returned buffer.
func newSrx(t *testing.T) (*rfxFixture, *bytes.Buffer) {
	t.Helper()
	h := &rfxHost{values: map[string]string{}}
	reg, err := core.NewRegistry(srxModule{h})
	if err != nil {
		t.Fatal(err)
	}
	logs := &bytes.Buffer{}
	f := &fixture{t: t, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC))}
	f.open()
	f.eng.cfg.Base.Log = slog.New(slog.NewTextHandler(logs, nil))
	return &rfxFixture{fixture: f, rh: h}, logs
}

// srxTarget applies action, confirms it and leaves it in needs_repair
// holding the srx lock, as a change whose revert failed is.
func (r *rfxFixture) srxTarget(action string) core.ChangeView {
	r.t.Helper()
	v := r.apply(alice, r.plan(alice, core.ActionRef{Name: action, Version: 1}, `{}`).PlanID, core.ApplyOptions{})
	if _, err := r.eng.Confirm(context.Background(), alice, v.ID); err != nil {
		r.t.Fatal(err)
	}
	r.setNeedsRepair(v.ID, "srx")
	return v
}

// TestRevertibleRepairReportsItsOutcome: a revertible target is repaired by
// its RepairedBy action, also when that is the target's own action, so the
// outcome comes from the repair's output and the target keeps its own
// output.
func TestRevertibleRepairReportsItsOutcome(t *testing.T) {
	for _, tc := range []struct {
		action, input, outcome string
	}{
		{"srx.rule.set", `{"outcome":"restore"}`, core.RepairRestored},
		{"srx.state.repair", `{"outcome":"restore"}`, core.RepairRestored},
		{"srx.state.repair", `{"outcome":"keep"}`, core.RepairKept},
		{"srx.state.repair", `{"outcome":"none"}`, ""},
	} {
		t.Run(tc.action+" "+tc.input, func(t *testing.T) {
			r, logs := newSrx(t)
			ctx := context.Background()
			target := r.srxTarget(tc.action)
			p := r.apply(alice, r.repairPlan(target.ID, tc.input).PlanID, core.ApplyOptions{})
			if p.State != core.StatePending || p.RevertDeadline == nil {
				t.Fatalf("repair change = %s, deadline %v; want pending with a revert deadline", p.State, p.RevertDeadline)
			}
			if _, err := r.eng.Confirm(ctx, alice, p.ID); err != nil {
				t.Fatal(err)
			}
			v, err := r.eng.Change(ctx, target.ID)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != core.StateRepaired || v.RepairOutcome != tc.outcome || v.RepairedBy != p.ID {
				t.Errorf("target = %s, outcome %q, repaired by %q; want repaired, outcome %q, by %s",
					v.State, v.RepairOutcome, v.RepairedBy, tc.outcome, p.ID)
			}
			if string(v.Output) != `{"applied":"yes"}` {
				t.Errorf("target output = %s, want its own {\"applied\":\"yes\"}", v.Output)
			}
			if warned := strings.Contains(logs.String(), "reported no repairOutcome"); warned != (tc.outcome == "") {
				t.Errorf("warning logged %v, want %v:\n%s", warned, tc.outcome == "", logs)
			}
		})
	}
}

// TestRevertibleRepairRevertedReturnsTheLocks: a reverted revertible repair
// change gives the srx lock back to its target, which stays in
// needs_repair.
func TestRevertibleRepairRevertedReturnsTheLocks(t *testing.T) {
	r, _ := newSrx(t)
	ctx := context.Background()
	target := r.srxTarget("srx.state.repair")
	p := r.apply(alice, r.repairPlan(target.ID, "").PlanID, core.ApplyOptions{})
	if _, err := r.eng.Revert(ctx, alice, p.ID); err != nil {
		t.Fatal(err)
	}
	v, err := r.eng.Change(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateNeedsRepair || v.Owner != "" || r.lockOwner("srx") != target.ID {
		t.Errorf("target = %s, owner %q, lock held by %q; want needs_repair, unowned, holding srx",
			v.State, v.Owner, r.lockOwner("srx"))
	}
}

// TestConvergingRepairKeepsAndCopiesTheOutput: a compensable target repaired
// by applying its own action again is kept, and takes the repair's output.
func TestConvergingRepairKeepsAndCopiesTheOutput(t *testing.T) {
	r := newRfx(t)
	ctx := context.Background()
	target := r.convergeTarget(`{"name":"a","value":"2"}`)
	p := r.apply(alice, r.repairPlan(target.ID, "").PlanID, core.ApplyOptions{})
	v, err := r.eng.Change(ctx, target.ID)
	if err != nil {
		t.Fatal(err)
	}
	if v.RepairOutcome != core.RepairKept || string(v.Output) != string(p.Output) {
		t.Errorf("target outcome %q, output %s; want kept with the repair's output %s", v.RepairOutcome, v.Output, p.Output)
	}
}

package firewall

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

type hkInput struct {
	core.Guarded
	Fail bool `json:"fail,omitempty"`
}

// hkMutation adds pnRule, or runs build when it is set.
func hkMutation(f fw, build buildFunc) mutation[hkInput] {
	return mutation[hkInput]{
		f:       f,
		guarded: func(in hkInput) core.Guarded { return in.Guarded },
		build: func(context.Context, core.Env, hkInput) buildFunc {
			if build != nil {
				return build
			}
			return func(fwstate.Config, fwstate.Loaded) (change, error) {
				op, err := addRuleOp(pnRule)
				return change{ops: []ufwOp{op}}, err
			}
		},
		check: func(_ context.Context, _ core.Env, in hkInput, _ fwstate.Config) error {
			if in.Fail {
				return errors.New("firewall: the rule is missing")
			}
			return nil
		},
		finish: func(_ context.Context, _ core.Env, _ hkInput, out *ChangeOutput) error {
			out.Rules = []string{planRuleID}
			return nil
		},
	}
}

func hkEffects(h *fakeHost) []string {
	var out []string
	for _, e := range h.journal.effects {
		out = append(out, e.Resource+" "+string(e.State))
	}
	return out
}

// TestMutationDefinition: every mutating action is revertible on the agent
// under the firewall lock, repaired by firewall.state.repair, with the
// overridable lockout guard, and the registry accepts it.
func TestMutationDefinition(t *testing.T) {
	_, f, _ := pnHost(t)
	def := defineMutation(core.ActionSpec{Name: "firewall.test.add", Summary: "Add a test rule.", Converges: true}, hkMutation(f, nil))
	repair := defineMutation(core.ActionSpec{Name: repairAction, Summary: "Repair."}, hkMutation(f, nil))
	want := core.ActionSpec{Name: "firewall.test.add", Summary: "Add a test rule.", Version: 1, Class: core.ClassRevertible,
		Side: core.SideAgent, LockKeys: []string{"firewall"}, Converges: true, RepairedBy: "firewall.state.repair",
		ApplyTimeout: 45 * time.Second, Guards: []core.GuardSpec{{ID: "firewall.lockout", Overridable: true}}}
	if !reflect.DeepEqual(def.Spec, want) {
		t.Errorf("spec %+v, want %+v", def.Spec, want)
	}
	if def.Handler.Observe == nil || def.Handler.Snapshot == nil || def.Handler.Revert == nil {
		t.Error("the handler lacks Observe, Snapshot or Revert")
	}
	if _, err := core.NewRegistry(fwTestModule{defs: []core.ActionDef{def, repair}}); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// fwTestModule is a firewall module holding only the given actions.
type fwTestModule struct{ defs []core.ActionDef }

func (m fwTestModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs, DefaultEnabled: true}
}

// TestMutationLifecycle: snapshot, apply as journaled effects with the
// reload of an active UFW, post-apply hashes, the action's output, verify,
// and a revert that restores the snapshot whole.
func TestMutationLifecycle(t *testing.T) {
	ctx := context.Background()
	h, f, _ := pnHost(t)
	m := hkMutation(f, nil)
	gc := &core.GuardContext{Complete: true}
	in := hkInput{Guarded: core.Guarded{Guard: gc}}
	env := h.env()

	raw, err := m.Snapshot(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	snap, err := getSnapshot(ctx, h.db, string(raw))
	if err != nil || snap.State.Guard == nil || !snap.State.Guard.Complete || snap.State.Image != nil {
		t.Fatalf("snapshot %+v (%v), want the plan's guard context and no before-image", snap.State, err)
	}
	before := h.state()
	out, err := m.Apply(ctx, env, in)
	if err != nil {
		t.Fatal(err)
	}
	if out.Snapshot != string(raw) || !reflect.DeepEqual(out.Rules, []string{planRuleID}) {
		t.Errorf("output %+v, want the snapshot and the finish's fields", out)
	}
	if got, want := hkEffects(h), []string{"firewall.rule:" + planRuleID + " made", "firewall.state:enabled made"}; !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
	if !strings.HasSuffix(h.calls[len(h.calls)-1], "/usr/sbin/ufw reload") {
		t.Errorf("calls %v, want a reload last", h.calls)
	}
	if snap, _ = getSnapshot(ctx, h.db, string(raw)); snap.ChangeID != "c1" || snap.PostHashes == nil {
		t.Errorf("snapshot change %q post hashes %v, want attached with post hashes", snap.ChangeID, snap.PostHashes)
	}
	if err := m.Verify(ctx, env, in, out); err != nil {
		t.Errorf("Verify: %v", err)
	}
	rev, err := m.Revert(ctx, env, raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(rev.Effects) != 2 || rev.Effects[0].State != core.EffectUndone || rev.Effects[1].State != core.EffectUndone || rev.Override {
		t.Errorf("revert %+v, want both effects undone", rev)
	}
	if !reflect.DeepEqual(h.state(), before) {
		t.Errorf("state after revert %+v, want %+v", h.state(), before)
	}
	obs, err := m.Observe(ctx, env, h.journal.effects[0])
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("Observe = %s, %v; want the rule absent", obs.Current.State, err)
	}
}

// TestMutationVerify: the action's own check fails verify first; then a
// loaded state that differs from the prediction is a prediction mismatch.
func TestMutationVerify(t *testing.T) {
	ctx := context.Background()
	h, f, _ := pnHost(t)
	m := hkMutation(f, nil)
	if err := m.Verify(ctx, h.env(), hkInput{Fail: true}, ChangeOutput{}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Errorf("Verify = %v, want the action's check", err)
	}
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V6, Table: "filter", Chain: "ufw6-user-input", OnlyA: []string{"-j ACCEPT"}}}
	err := m.Verify(ctx, h.env(), hkInput{}, ChangeOutput{})
	if e := errcode.From(err); e.Code != errcode.FirewallPredictionMismatch || !strings.Contains(e.Detail, "v6 filter/ufw6-user-input") {
		t.Errorf("Verify = %v, want a prediction mismatch naming the chain", err)
	}
}

// TestMutationPlanAndFailure: Plan is the shared pipeline with the input's
// guard and manual flag; a ufw failure fails Apply and leaves its effect
// for the engine.
func TestMutationPlanAndFailure(t *testing.T) {
	ctx := context.Background()
	h, f, stub := pnHost(t)
	m := hkMutation(f, nil)
	stub.guard = core.Guard{OK: false, Message: "the change would cut ssh"}
	out, err := m.Plan(ctx, h.env(), hkInput{Guarded: core.Guarded{Overrides: []string{guard.GuardID}}})
	if err != nil || !out.Guards[0].Overridden || len(out.Diff) != 1 {
		t.Errorf("plan %+v (%v), want the change with the guard overridden", out, err)
	}
	h.ufw[strings.Join(model.AddArgs(pnRule), " ")] = func(*fhState) int { return 1 }
	if _, err := m.Snapshot(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(ctx, h.env(), hkInput{}); err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Errorf("Apply = %v, want ufw's failure", err)
	}
	if got := hkEffects(h); !reflect.DeepEqual(got, []string{"firewall.rule:?" + planRuleID + " skipped"}) {
		t.Errorf("effects %v, want the rule skipped", got)
	}
}

// TestMutationRestore: a restore writes each differing file once (ufw.conf
// with the current ENABLED while the state changes), then the transition,
// then each differing sysctl value of an interface that still exists.
func TestMutationRestore(t *testing.T) {
	ctx := context.Background()
	h, f, _ := pnHost(t)
	h.ufw["--force enable"] = func(s *fhState) int { s.Enabled = true; return 0 }
	h.ufw["disable"] = func(s *fhState) int { s.Enabled = false; return 0 }
	h.loaded = fwstate.Loaded{Variant: "nft", V4: fhParse(t, tgStopped), V6: fhParse(t, tgStopped)}

	cfg := h.config()
	var entries []core.RestoreEntry
	for _, p := range fwstate.StateFiles {
		entries = append(entries, cfg.Files[p])
	}
	snap := snapshot{ID: "01J00000000000000000000008", Entries: entries, State: snapshotState{Enabled: true,
		Sysctl: map[string]string{"net/ipv4/ip_forward": "1", "net/ipv4/conf/eth0/rp_filter": "1", "net/ipv4/conf/eth7/rp_filter": "0"}}}
	s := h.state()
	s.Enabled, s.Rules4 = false, s.Rules4[:1]
	h.setState(s)

	m := hkMutation(f, func(fwstate.Config, fwstate.Loaded) (change, error) { return change{restore: &snap}, nil })
	if _, err := m.Snapshot(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	if _, err := m.Apply(ctx, h.env(), hkInput{}); err != nil {
		t.Fatal(err)
	}
	want := []string{"firewall.file:/etc/ufw/user.rules made", "firewall.state:enabled made", "firewall.sysctl:net/ipv4/ip_forward made"}
	if got := hkEffects(h); !reflect.DeepEqual(got, want) {
		t.Errorf("effects %v, want %v", got, want)
	}
	var enabled bool
	if err := json.Unmarshal(h.journal.effects[1].Before.State, &enabled); err != nil || enabled {
		t.Errorf("the enable's before-state is %s, want UFW disabled", h.journal.effects[1].Before.State)
	}
	if got := h.state(); !got.Enabled || len(got.Rules4) != 2 || h.sysctl["net/ipv4/ip_forward"] != "1" {
		t.Errorf("state %+v, ip_forward %s; want the snapshot's", got, h.sysctl["net/ipv4/ip_forward"])
	}
}

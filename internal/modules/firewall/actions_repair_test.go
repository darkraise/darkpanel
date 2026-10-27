package firewall

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// rpRepaired is the change the repair tests repair.
const rpRepaired = "01J000000000000000000000R1"

// rpRun plans, snapshots and applies firewall.state.repair under env, then
// clears the host's differences (the reload repaired them) and verifies.
func rpRun(t *testing.T, h *fakeHost, f fw, env core.Env, raw string) (core.PlanOutcome, ChangeOutput) {
	t.Helper()
	ctx := context.Background()
	def := f.stateRepair()
	if err := schema.NewValidator().ValidateInput(def, json.RawMessage(raw)); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	planEnv := env
	planEnv.ChangeID = ""
	plan, err := def.Handler.Plan(ctx, planEnv, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := def.Handler.Snapshot(ctx, env, json.RawMessage(raw)); err != nil {
		t.Fatal(err)
	}
	res, err := def.Handler.Apply(ctx, env, json.RawMessage(raw))
	if err != nil {
		t.Fatalf("apply: %v", err)
	}
	h.diffs = nil
	if err := def.Handler.Verify(ctx, env, json.RawMessage(raw), res); err != nil {
		t.Fatalf("verify: %v", err)
	}
	var out ChangeOutput
	if err := json.Unmarshal(res, &out); err != nil {
		t.Fatal(err)
	}
	return plan, out
}

// rpTarget gives the repair tests a repaired change whose snapshot is the
// host now, pinned, and returns its id.
func rpTarget(t *testing.T, h *fakeHost, f fw) string {
	t.Helper()
	id := snCapture(t, h, f)
	if err := attachSnapshot(context.Background(), h.db, id, rpRepaired); err != nil {
		t.Fatal(err)
	}
	if err := setPinned(context.Background(), h.db, id, true); err != nil {
		t.Fatal(err)
	}
	return id
}

// TestRepairDirect: run on its own, the repair reloads UFW from its
// configuration on a host out of sync in UFW's chains, its guard runs and
// its snapshot holds the loaded-state before-image.
func TestRepairDirect(t *testing.T) {
	h, f, env := ruHost(t)
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-j ACCEPT"}}}
	plan, out := rpRun(t, h, f, env, `{}`)
	if len(plan.Guards) != 1 || !plan.Guards[0].OK || !strings.HasPrefix(acDiff(plan)[0], "  out of sync: v4 filter/ufw-user-input") {
		t.Errorf("plan %+v, want the differing chain and a passing guard", plan)
	}
	if out.RepairOutcome != "" {
		t.Errorf("output %+v, want no repair outcome", out)
	}
	snap, err := getSnapshot(context.Background(), h.db, out.Snapshot)
	if err != nil || snap.State.Image == nil {
		t.Errorf("snapshot %+v (%v), want a before-image", snap.State, err)
	}
	if got := hkEffects(h); !reflect.DeepEqual(got, []string{"firewall.state:enabled made"}) {
		t.Errorf("effects %v, want the reload", got)
	}
	_, err = f.stateRepair().Handler.Plan(context.Background(), env, json.RawMessage(`{"outcome":"keep"}`))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/outcome" {
		t.Errorf("an outcome without a repaired change = %v, want an invalid input at /outcome", err)
	}
	if def := f.stateRepair(); def.Spec.RepairedBy != "firewall.state.repair" || def.Spec.Guards[0].ID != guard.GuardID {
		t.Errorf("spec %+v", def.Spec)
	}
}

// TestRepairRestore: repairing a change, outcome restore (the default)
// returns the host to the change's snapshot, reports restored and unpins
// the snapshot.
func TestRepairRestore(t *testing.T) {
	h, f, env := ruHost(t)
	id := rpTarget(t, h, f)
	s := h.state()
	s.Rules4, s.Rules6 = s.Rules4[1:], nil
	h.setState(s)
	env.Call.RepairOf = rpRepaired
	plan, out := rpRun(t, h, f, env, `{}`)
	if len(plan.Diff) != 1 || !strings.Contains(plan.Diff[0].Text, fhTag) || plan.Diff[0].Op != "+" {
		t.Errorf("diff %q, want the rule the restore brings back", acDiff(plan))
	}
	if out.RepairOutcome != "restored" || len(findRules(h.config(), fhTag)) != 1 {
		t.Errorf("output %+v, want restored with the rule back", out)
	}
	if snap, _ := getSnapshot(context.Background(), h.db, id); snap.Pinned {
		t.Error("the repaired change's snapshot is still pinned")
	}
}

// TestRepairKeep: outcome keep reloads UFW from the files the change left,
// only while they still hash as it recorded them, and reports kept.
func TestRepairKeep(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	id := rpTarget(t, h, f)
	env.Call.RepairOf = rpRepaired
	_, err := f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(`{"outcome":"keep"}`))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || !strings.Contains(e.Detail, "no post-apply state") {
		t.Errorf("keep without post-apply hashes = %v, want an invalid input", err)
	}
	if err := setPostHashes(ctx, h.db, id, fileHashes(h.config().Files)); err != nil {
		t.Fatal(err)
	}
	_, out := rpRun(t, h, f, env, `{"outcome":"keep"}`)
	if out.RepairOutcome != "kept" {
		t.Errorf("output %+v, want kept", out)
	}

	s := h.state()
	s.LogLevel = "full"
	h.setState(s)
	_, err = f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(`{"outcome":"keep"}`))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || !strings.Contains(e.Detail, "/etc/ufw/ufw.conf") {
		t.Errorf("keep with a changed file = %v, want an invalid input naming it", err)
	}
	err = f.stateRepair().Handler.Verify(ctx, env, json.RawMessage(`{"outcome":"keep"}`), json.RawMessage(`{}`))
	if errcode.From(err).Code != errcode.FirewallPredictionMismatch {
		t.Errorf("verify keep with a changed file = %v, want a prediction mismatch", err)
	}
	env.Call.RepairOf = "01J000000000000000000000R9"
	if _, err := f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(`{}`)); err == nil {
		t.Error("repairing a change without a snapshot plans")
	}
}

// TestRepairRestoreManual: repairing a change with outcome restore brings
// back a manual rule the snapshot holds and the host lacks only with
// manual: true, as firewall.snapshot.restore does, and then reports
// restored with the rule back.
func TestRepairRestoreManual(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	rpTarget(t, h, f)
	manual := model.Merge(h.config().Rules4, nil)[1].ID
	s := h.state()
	s.Rules4 = s.Rules4[:1]
	h.setState(s)
	env.Call.RepairOf = rpRepaired

	_, err := f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(`{}`))
	if e := errcode.From(err); e.Code != errcode.FirewallManualRequired || !strings.Contains(e.Detail, manual) {
		t.Fatalf("plan without manual = %v, want manual required naming %s", err, manual)
	}
	plan, out := rpRun(t, h, f, env, `{"manual":true}`)
	if len(plan.Diff) != 1 || plan.Diff[0].Op != "+" || !strings.Contains(plan.Diff[0].Text, "rule "+manual+" (manual)") {
		t.Errorf("diff %q, want the manual rule the restore brings back", acDiff(plan))
	}
	if out.RepairOutcome != "restored" || len(model.Merge(h.config().Rules4, nil)) != 2 {
		t.Errorf("output %+v with %d rules, want restored with the manual rule back", out, len(model.Merge(h.config().Rules4, nil)))
	}
}

// TestRepairRefusesOutcome: an outcome other than restore and keep is
// refused at /outcome, whether or not a change is repaired.
func TestRepairRefusesOutcome(t *testing.T) {
	ctx := context.Background()
	h, f, env := ruHost(t)
	id := rpTarget(t, h, f)
	if err := setPostHashes(ctx, h.db, id, fileHashes(h.config().Files)); err != nil {
		t.Fatal(err)
	}
	for _, repairOf := range []string{"", rpRepaired} {
		env.Call.RepairOf = repairOf
		_, err := f.stateRepair().Handler.Plan(ctx, env, json.RawMessage(`{"outcome":"discard"}`))
		if e := errcode.From(err); e.Code != errcode.InvalidInput || len(e.Fields) != 1 || e.Fields[0].Path != "/outcome" {
			t.Errorf("repair of %q with outcome discard = %v, want an invalid input at /outcome", repairOf, err)
		}
	}
}

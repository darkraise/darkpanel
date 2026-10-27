package firewall

import (
	"context"
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/modules/firewall/guard"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
)

// pnRule is the rule the plan tests add.
var pnRule = model.Rule{ID: planRuleID, Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP,
	From: model.Any, To: model.Any, ToPort: "80", Family: model.FamilyBoth}

// pnHost is a fake host whose ufw adds pnRule.
func pnHost(t *testing.T) (*fakeHost, fw, *fwStub) {
	t.Helper()
	h := newFakeHost(t)
	h.ufw[strings.Join(model.AddArgs(pnRule), " ")] = func(s *fhState) int {
		s.Rules4 = append(s.Rules4, "### tuple ### allow tcp 80 0.0.0.0/0 any 0.0.0.0/0 in comment="+hex.EncodeToString([]byte(model.Tag(planRuleID, ""))))
		return 0
	}
	h.ufw["reload"] = func(*fhState) int { return 0 }
	f, stub := newFW(h)
	return h, f, stub
}

type pnInput struct {
	core.Guarded
	Port string `json:"port"`
}

func pnAddRule(t *testing.T) func(fwstate.Config, fwstate.Loaded) (change, error) {
	t.Helper()
	return func(fwstate.Config, fwstate.Loaded) (change, error) {
		op, err := addRuleOp(pnRule)
		return change{ops: []ufwOp{op}}, err
	}
}

func pnReq(t *testing.T, g core.Guarded) planReq {
	return planReq{input: pnInput{Guarded: g, Port: "80"}, guarded: g, build: pnAddRule(t)}
}

// TestPlanPasses: the plan shows the change, runs the lockout guard on the
// predicted state with the plan's and the apply's guard contexts and the
// real evaluator, and hashes the input and the state it read.
func TestPlanPasses(t *testing.T) {
	ctx := context.Background()
	h, f, stub := pnHost(t)
	gc := &core.GuardContext{Complete: true, Client: &core.Peer{Port: 50000}}
	refresh := &core.GuardContext{Complete: true}
	env := h.env()
	env.Call = core.CallContext{GuardRefresh: refresh, Retired: []string{"exp1"}}
	out, err := f.plan(ctx, env, pnReq(t, core.Guarded{Guard: gc}))
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.DiffLine{{Op: "+", Text: "rule new: allow in proto tcp from 0.0.0.0/0 to 0.0.0.0/0 port 80"}}; !reflect.DeepEqual(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if len(out.Guards) != 1 || !out.Guards[0].OK || out.Guards[0].ID != guard.GuardID || out.Guards[0].Overridden {
		t.Errorf("guards %+v, want one passing lockout guard", out.Guards)
	}
	if len(stub.inputs) != 1 {
		t.Fatalf("guard checked %d times, want 1", len(stub.inputs))
	}
	in := stub.inputs[0]
	if in.Plan != gc || in.Refresh != refresh || !reflect.DeepEqual(in.Retired, []string{"exp1"}) {
		t.Errorf("guard input %+v, want the plan's guard, the apply's refresh and the retired exposures", in)
	}
	wrSameFunc(t, "Eval", in.Eval, eval.Evaluate)
	if stub.states[0].Sysctl == nil {
		t.Error("the guard ran on a state without sysctl values")
	}
	if out.StateHash == "" {
		t.Fatal("no state hash")
	}
	again, err := f.plan(ctx, env, pnReq(t, core.Guarded{Guard: gc}))
	if err != nil || again.StateHash != out.StateHash {
		t.Errorf("a second plan hashes %q (%v), want %q", again.StateHash, err, out.StateHash)
	}
	other, err := f.plan(ctx, env, planReq{input: pnInput{Port: "81"}, build: pnAddRule(t)})
	if err != nil || other.StateHash == out.StateHash {
		t.Errorf("another input hashes the same (%v)", err)
	}
	h.sysctl["net/ipv4/ip_forward"] = "1"
	if moved, err := f.plan(ctx, env, pnReq(t, core.Guarded{Guard: gc})); err != nil || moved.StateHash == out.StateHash {
		t.Errorf("a changed sysctl value hashes the same (%v)", err)
	}
	h.sysctl["net/ipv4/ip_forward"] = "0"
	h.loaded.V4 = fhParse(t, strings.Replace(fhSave, "--dport 22", "--dport 2222", 1))
	if moved, err := f.plan(ctx, env, pnReq(t, core.Guarded{Guard: gc})); err != nil || moved.StateHash == out.StateHash {
		t.Errorf("a changed loaded rule hashes the same (%v)", err)
	}
}

// TestPlanGuardOverrides: a failing guard fails the plan's guard; listed in
// overrides it passes as overridden with its message; a guard that cannot
// run fails the plan.
func TestPlanGuardOverrides(t *testing.T) {
	ctx := context.Background()
	h, f, stub := pnHost(t)
	cut := "the change would cut ssh from 203.0.113.7 to 192.0.2.1:22 on eth0: drop (filter/INPUT policy DROP)"
	stub.guard = core.Guard{OK: false, Message: cut}
	out, err := f.plan(ctx, h.env(), pnReq(t, core.Guarded{}))
	if err != nil {
		t.Fatal(err)
	}
	if g := out.Guards[0]; g.OK || g.Overridden || g.Message != cut {
		t.Errorf("guard %+v, want failing with the flow", g)
	}
	out, err = f.plan(ctx, h.env(), pnReq(t, core.Guarded{Overrides: []string{guard.GuardID}}))
	if err != nil {
		t.Fatal(err)
	}
	if g := out.Guards[0]; !g.OK || !g.Overridden || g.Message != cut {
		t.Errorf("guard %+v, want overridden with the flow", g)
	}
	stub.err = errors.New("firewall: read the socket table: netstat: no socket tables under the proc root")
	if _, err := f.plan(ctx, h.env(), pnReq(t, core.Guarded{Overrides: []string{guard.GuardID}})); !errors.Is(err, stub.err) {
		t.Errorf("plan = %v, want the guard's error", err)
	}
}

// TestPlanRefusals: an unsupported configuration is refused whatever the
// action; a manual rule change needs manual: true.
func TestPlanRefusals(t *testing.T) {
	ctx := context.Background()
	h, f, _ := pnHost(t)
	h.loaded.Unsupported = []string{"nft table inet dpforeign: base chain \"ruled\" on hook \"input\" has 2 rule(s) and policy accept"}
	_, err := f.plan(ctx, h.env(), pnReq(t, core.Guarded{Overrides: []string{guard.GuardID}}))
	if e := errcode.From(err); e.Code != errcode.FirewallUnsupportedConfiguration || !strings.Contains(e.Detail, "dpforeign") {
		t.Errorf("plan = %v, want an unsupported configuration naming the reason", err)
	}

	h, f, _ = pnHost(t)
	s := h.state()
	deleteManual := func(fwstate.Config, fwstate.Loaded) (change, error) {
		return change{ops: []ufwOp{{Kind: core.EffectDelete, Resource: ruleResource("manual"), Args: []string{"delete", "allow", "3000"}}}}, nil
	}
	h.ufw["delete allow 3000"] = func(s *fhState) int { s.Rules4 = s.Rules4[:1]; return 0 }
	_, err = f.plan(ctx, h.env(), planReq{input: 1, build: deleteManual})
	if e := errcode.From(err); e.Code != errcode.FirewallManualRequired || !strings.Contains(e.Detail, "manual-") {
		t.Errorf("plan = %v, want manual required naming the rule", err)
	}
	out, err := f.plan(ctx, h.env(), planReq{input: 1, manual: true, build: deleteManual})
	if err != nil || len(out.Diff) != 1 || !strings.Contains(out.Diff[0].Text, "(manual)") || out.Diff[0].Op != "-" {
		t.Errorf("plan = %+v, %v; want the manual rule's removal", out.Diff, err)
	}
	if h.state().Rules4[1] != s.Rules4[1] {
		t.Error("the plan changed the host")
	}

	h, f, _ = pnHost(t)
	boom := errors.New("firewall: rule 01J00000000000000000000009 not found")
	if _, err := f.plan(ctx, h.env(), planReq{input: 1, build: func(fwstate.Config, fwstate.Loaded) (change, error) { return change{}, boom }}); !errors.Is(err, boom) {
		t.Errorf("plan = %v, want the action's error", err)
	}
}

// TestPlanReplacingAdd: an add that ufw would turn into a replacement of a
// rule differing only in action, log type or comment is refused at plan
// time, as Apply would refuse it.
func TestPlanReplacingAdd(t *testing.T) {
	ctx := context.Background()
	h, f, stub := pnHost(t)
	s := h.state()
	s.Rules4, s.Rules6 = []string{strings.Replace(fhRule22v4, "allow tcp 22", "deny tcp 80", 1), fhRule3000v4}, nil
	h.setState(s)
	h.ufw[strings.Join(model.AddArgs(pnRule), " ")] = func(s *fhState) int {
		s.Rules4[0] = "### tuple ### allow tcp 80 0.0.0.0/0 any 0.0.0.0/0 in comment=" + hex.EncodeToString([]byte(model.Tag(planRuleID, "")))
		return 0
	}
	_, err := f.plan(ctx, h.env(), pnReq(t, core.Guarded{Overrides: []string{guard.GuardID}}))
	if e := errcode.From(err); e.Code != errcode.InvalidInput || !strings.Contains(e.Detail, "would replace an existing rule") {
		t.Errorf("plan = %v, want the replacement refused", err)
	}
	if len(stub.inputs) != 0 {
		t.Errorf("guard checked %d times, want none", len(stub.inputs))
	}
}

// TestPlanOutOfSync: an out-of-sync host shows the differing chains and
// fails the guard as a hard refusal that overrides cannot pass, except for
// a repair whose difference is confined to UFW's and Darkpanel's chains,
// whose guard runs as usual.
func TestPlanOutOfSync(t *testing.T) {
	ctx := context.Background()
	h, f, stub := pnHost(t)
	h.diffs = []fwstate.ChainDiff{{Family: fwstate.V4, Table: "filter", Chain: "ufw-user-input", OnlyB: []string{"-p tcp -m tcp --dport 22 -j ACCEPT"}}}
	out, err := f.plan(ctx, h.env(), pnReq(t, core.Guarded{Overrides: []string{guard.GuardID}}))
	if err != nil {
		t.Fatal(err)
	}
	last := out.Diff[len(out.Diff)-1]
	if last.Op != " " || last.Text != "out of sync: v4 filter/ufw-user-input: 1 rule only loaded" {
		t.Errorf("diff %+v, want the differing chain last", out.Diff)
	}
	if g := out.Guards[0]; g.OK || g.Overridden || !strings.Contains(g.Message, "out of sync") || len(stub.inputs) != 0 {
		t.Errorf("guard %+v (checked %d times), want a hard refusal without a check", g, len(stub.inputs))
	}

	repair := planReq{input: 2, guarded: core.Guarded{Overrides: []string{guard.GuardID}},
		build: func(fwstate.Config, fwstate.Loaded) (change, error) { return change{repair: true}, nil }}
	stub.guard = core.Guard{OK: false, Message: "the change would cut agent link: drop"}
	out, err = f.plan(ctx, h.env(), repair)
	if err != nil {
		t.Fatal(err)
	}
	if g := out.Guards[0]; !g.OK || !g.Overridden || len(stub.inputs) != 1 {
		t.Errorf("repair guard %+v (checked %d times), want the guard run and overridden", g, len(stub.inputs))
	}

	h.diffs = append(h.diffs, fwstate.ChainDiff{Family: fwstate.V4, Table: "filter", Chain: "fw-foreign", OnlyB: []string{"-j DROP"}})
	out, err = f.plan(ctx, h.env(), repair)
	if err != nil {
		t.Fatal(err)
	}
	if g := out.Guards[0]; g.OK || g.Overridden || !strings.Contains(g.Message, "fw-foreign") || len(stub.inputs) != 1 {
		t.Errorf("repair guard %+v, want a hard refusal naming the foreign chain", g)
	}
}

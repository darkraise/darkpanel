package cli

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fwrbRestoreInput is the input of the fixture's firewall.snapshot.restore.
type fwrbRestoreInput struct {
	core.Guarded
	ID     string `json:"id" jsonschema_description:"Snapshot id."`
	Manual bool   `json:"manual,omitempty" jsonschema_description:"Allow adding or removing manual rules."`
}

// fwrbModule declares the firewall names rollback uses: the revertible
// firewall.snapshot.restore with its overridable lockout guard and the
// repair action it names.
type fwrbModule struct{}

func (fwrbModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "firewall", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRevertible(core.ActionSpec{
				Name: firewallRestoreAction, Version: 1, Side: core.SideAgent, Summary: "Restore a firewall snapshot.",
				LockKeys: []string{"firewall"}, RepairedBy: "firewall.state.repair",
				Guards: []core.GuardSpec{{ID: "firewall.lockout", Overridable: true}},
			}, fixtureHandler[fwrbRestoreInput, demoRule]{}),
			core.DefineMutating(core.ActionSpec{
				Name: "firewall.state.repair", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent,
				Summary: "Repair the firewall.", LockKeys: []string{"firewall"},
			}, fixtureHandler[demoNoInput, demoRule]{}),
		},
	}
}

func fwrbRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{}), fwrbModule{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// fwrbView is a firewall change whose output names the snapshot s<id>
// taken before it, as every mutating firewall action's output does.
func fwrbView(id, action, actor, state string) core.ChangeView {
	return core.ChangeView{ID: id, HostID: "h1", Action: action, Actor: actor, Version: 1, Class: core.ClassRevertible, State: state,
		Output: json.RawMessage(`{"snapshot":"s` + strings.TrimPrefix(id, "c") + `"}`)}
}

// fwrbAgent is a fake agent holding changes, newest first; it records the
// listings and every call that changes something.
func fwrbAgent(rec *agentCalls, changes []core.ChangeView) *coretest.FakeAgent {
	return &coretest.FakeAgent{
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			rec.add("changes before=%q limit=%d", f.Before, f.Limit)
			start := 0
			if f.Before != "" {
				start = slices.IndexFunc(changes, func(v core.ChangeView) bool { return v.ID == f.Before }) + 1
			}
			return slices.Clone(changes[start:min(start+f.Limit, len(changes))]), nil
		},
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			for _, v := range changes {
				if v.ID == id {
					return v, nil
				}
			}
			return core.ChangeView{}, errcode.New(errcode.ChangeNotFound, "no change "+id)
		},
		RevertFunc: func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
			rec.add("revert %s", id)
			return fwrbView(id, "firewall.rule.add", "user:u1", core.StateReverted), nil
		},
		PlanFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
			rec.add("plan %s/v%d %s", a.Name, a.Version, input)
			var in fwrbRestoreInput
			_ = json.Unmarshal(input, &in)
			guard := core.Guard{ID: "firewall.lockout", OK: true, Message: "every protected flow passes", Overridable: true}
			if in.ID == "s3" && len(in.Overrides) == 0 {
				guard.OK, guard.Message = false, "ssh from 203.0.113.7 to 192.0.2.1:22 on eth0 is dropped"
			}
			return core.PlanView{PlanID: "p1", HostID: "h1", Action: a.Name, Version: a.Version,
				Diff: []core.DiffLine{{Op: "-", Text: "allow in tcp 8080"}}, Guards: []core.Guard{guard}}, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			rec.add("apply %s confirmNow=%t", planID, opts.ConfirmNow)
			return fwrbView("c9", firewallRestoreAction, "user:u1", core.StateConfirmed), nil
		},
	}
}

func TestFirewallRollbackRevertsTheNewestPendingChange(t *testing.T) {
	changes := []core.ChangeView{
		fwrbView("c5", "firewall.containers.apply", "system:firewall.reconcile", core.StatePending),
		fwrbView("c4", "docker.container.stop", "user:u1", core.StateCompleted),
		{ID: "c3s", Action: "firewall.rules.apply", Actor: "user:u1", State: core.StatePending, ParentChangeID: "c3p"},
		fwrbView("c2", "firewall.rule.add", "user:u1", core.StatePending),
		fwrbView("c1", "firewall.rule.add", "user:u1", core.StateConfirmed),
	}
	rec := &agentCalls{}
	cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, changes))}
	out, errOut, code := cli.run(t, "firewall", "rollback", "--local", "--yes")
	if code != ExitOK || !strings.Contains(out, "Change c2 (firewall.rule.add) is pending: it will be reverted now.\n") ||
		!strings.Contains(out, "Change c2: reverted\n") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
	if got, want := rec.list(), []string{`changes before="" limit=100`, "revert c2"}; !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

func TestFirewallRollbackAsksBeforeReverting(t *testing.T) {
	rec := &agentCalls{}
	changes := []core.ChangeView{fwrbView("c2", "firewall.rule.add", "user:u1", core.StatePending)}
	cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, changes))}
	_, errOut, code := cli.run(t, "firewall", "rollback", "--local")
	if code != ExitRefused || !strings.Contains(errOut, "pass --yes") {
		t.Errorf("exit %d, err %q; want a refusal asking for --yes", code, errOut)
	}
	cli.terminal, cli.stdin = true, "y\n"
	if _, errOut, code = cli.run(t, "firewall", "rollback", "--local"); code != ExitOK {
		t.Errorf("answered y: exit %d, err %q", code, errOut)
	}
	want := []string{`changes before="" limit=100`, `changes before="" limit=100`, "revert c2"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q: one revert, after the answer", got, want)
	}
}

func TestFirewallRollbackRestoresAConfirmedChange(t *testing.T) {
	changes := []core.ChangeView{
		fwrbView("c2", "firewall.rule.add", "user:u1", core.StateReverted),
		fwrbView("c1", "firewall.rule.add", "user:u1", core.StateConfirmed),
	}
	for _, tc := range []struct {
		name  string
		extra []string
		input string
	}{
		{"plain", nil, `{"id":"s1"}`},
		{"manual and an override", []string{"--manual", "--override", "firewall.lockout"},
			`{"id":"s1","manual":true,"overrides":["firewall.lockout"]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &agentCalls{}
			cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, changes))}
			out, errOut, code := cli.run(t, slices.Concat([]string{"firewall", "rollback", "--change", "c1", "--local", "--yes"}, tc.extra)...)
			if code != ExitOK || !strings.Contains(out, "snapshot s1, taken before it, will be restored and confirmed") ||
				!strings.Contains(out, "Plan for firewall.snapshot.restore/v1 on host h1:\n  - allow in tcp 8080\n") ||
				!strings.Contains(out, "Change c9: confirmed\n") {
				t.Errorf("exit %d, out %q, err %q", code, out, errOut)
			}
			want := []string{"plan firewall.snapshot.restore/v1 " + tc.input, "apply p1 confirmNow=true"}
			if got := rec.list(); !slices.Equal(got, want) {
				t.Errorf("the agent saw %q, want %q", got, want)
			}
		})
	}
}

func TestFirewallRollbackPrintsJSON(t *testing.T) {
	changes := []core.ChangeView{fwrbView("c1", "firewall.rule.add", "user:u1", core.StateConfirmed)}
	cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(&agentCalls{}, changes))}
	out, errOut, code := cli.run(t, "firewall", "rollback", "--local", "--yes", "--json")
	var ch changeWire
	if code != ExitOK || json.Unmarshal([]byte(out), &ch) != nil || ch.ID != "c9" || ch.State != core.StateConfirmed ||
		!strings.Contains(errOut, "Plan for firewall.snapshot.restore/v1") {
		t.Errorf("exit %d, out %q, err %q; want the change's JSON alone on standard output", code, out, errOut)
	}
}

func TestFirewallRollbackPagesThroughOlderChanges(t *testing.T) {
	saved := firewallChangePage
	firewallChangePage = 2
	t.Cleanup(func() { firewallChangePage = saved })
	changes := []core.ChangeView{
		fwrbView("c5", "docker.container.stop", "user:u1", core.StateCompleted),
		fwrbView("c4", "core.token.create", "user:u1", core.StateCompleted),
		fwrbView("c3", "docker.container.start", "user:u1", core.StateCompleted),
		fwrbView("c2", "firewall.rule.delete", "user:u1", core.StatePending),
	}
	rec := &agentCalls{}
	cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, changes))}
	if out, errOut, code := cli.run(t, "firewall", "rollback", "--local", "--yes"); code != ExitOK {
		t.Fatalf("exit %d, out %q, err %q", code, out, errOut)
	}
	want := []string{`changes before="" limit=2`, `changes before="c4" limit=2`, "revert c2"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

func TestFirewallRollbackRefusals(t *testing.T) {
	confirmed := fwrbView("c1", "firewall.rule.add", "user:u1", core.StateConfirmed)
	noSnapshot := confirmed
	noSnapshot.Output = json.RawMessage(`{}`)
	for _, tc := range []struct {
		name    string
		changes []core.ChangeView
		args    []string
		code    int
		errPart string
		calls   []string
	}{
		{name: "without --local", args: []string{"firewall", "rollback", "--yes"}, code: ExitUsage,
			errPart: "darkpanel firewall rollback is a break-glass command: run it as root on the host with --local"},
		{name: "no firewall change", changes: []core.ChangeView{fwrbView("c1", "docker.container.stop", "user:u1", core.StateCompleted)},
			args: []string{"firewall", "rollback", "--local", "--yes"}, code: ExitRefused,
			errPart: "this host has no firewall change to roll back", calls: []string{`changes before="" limit=100`}},
		{name: "the newest change is reverted", changes: []core.ChangeView{fwrbView("c2", "firewall.rule.add", "user:u1", core.StateReverted), confirmed},
			args: []string{"firewall", "rollback", "--local", "--yes"}, code: ExitRefused,
			errPart: "change c2 (firewall.rule.add) is reverted: only a pending or a confirmed firewall change can be rolled back",
			calls:   []string{`changes before="" limit=100`}},
		{name: "a named change of another module", changes: []core.ChangeView{fwrbView("c7", "docker.container.stop", "user:u1", core.StateCompleted)},
			args: []string{"firewall", "rollback", "--change", "c7", "--local", "--yes"}, code: ExitRefused,
			errPart: "change c7 is docker.container.stop, not a firewall change"},
		{name: "an unknown change", args: []string{"firewall", "rollback", "--change", "c8", "--local", "--yes"}, code: ExitError,
			errPart: "no change c8 (change.not_found)"},
		{name: "no snapshot named by the change", changes: []core.ChangeView{noSnapshot},
			args: []string{"firewall", "rollback", "--local", "--yes"}, code: ExitRefused,
			errPart: "change c1 names no snapshot taken before it", calls: []string{`changes before="" limit=100`}},
		{name: "a failing guard", changes: []core.ChangeView{fwrbView("c3", "firewall.rule.add", "user:u1", core.StateConfirmed)},
			args: []string{"firewall", "rollback", "--local", "--yes"}, code: ExitRefused,
			errPart: "guard firewall.lockout failed: ssh from 203.0.113.7 to 192.0.2.1:22 on eth0 is dropped; nothing was applied",
			calls:   []string{`changes before="" limit=100`, `plan firewall.snapshot.restore/v1 {"id":"s3"}`}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := &agentCalls{}
			cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, tc.changes))}
			out, errOut, code := cli.run(t, tc.args...)
			if code != tc.code || !strings.Contains(errOut, tc.errPart) {
				t.Errorf("exit %d, out %q, err %q; want exit %d and an error containing %q", code, out, errOut, tc.code, tc.errPart)
			}
			if got := rec.list(); !slices.Equal(got, tc.calls) {
				t.Errorf("the agent saw %q, want %q", got, tc.calls)
			}
		})
	}
}

func TestFirewallRollbackNeedsTheRestoreAction(t *testing.T) {
	changes := []core.ChangeView{fwrbView("c1", "firewall.rule.add", "user:u1", core.StateConfirmed)}
	cli := cliEnv{agentSocket: agentSocket(t, fwrbAgent(&agentCalls{}, changes))}
	_, errOut, code := cli.run(t, "firewall", "rollback", "--local", "--yes")
	if code != ExitError || !strings.Contains(errOut, "this darkpanel has no firewall.snapshot.restore action") {
		t.Errorf("exit %d, err %q", code, errOut)
	}
}

// The break-glass snapshot restore is the generated command, which --local
// sends to the agent socket and --confirm confirms in the same run.
func TestFirewallSnapshotRestoreBreakGlass(t *testing.T) {
	rec := &agentCalls{}
	cli := cliEnv{reg: fwrbRegistry(t), agentSocket: agentSocket(t, fwrbAgent(rec, nil))}
	out, errOut, code := cli.run(t, "firewall", "snapshot", "restore", "--id", "s1", "--local", "--yes", "--confirm")
	if code != ExitOK || !strings.Contains(out, "Change c9: confirmed\n") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
	want := []string{`plan firewall.snapshot.restore/v1 {"id":"s1"}`, "apply p1 confirmNow=true"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

package modules_test

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

// spec returns the action's spec; a missing action is reported and answers a
// spec that fails every expectation of the callers.
func spec(t *testing.T, name string) core.ActionSpec {
	t.Helper()
	for _, m := range modules.All() {
		for _, a := range m.Manifest().Actions {
			if a.Spec.Name == name {
				return a.Spec
			}
		}
	}
	t.Errorf("no action named %s is registered", name)
	return core.ActionSpec{Name: name, MinScope: "missing"}
}

func manifest(t *testing.T, id string) core.Manifest {
	t.Helper()
	for _, m := range modules.All() {
		if m.Manifest().ID == id {
			return m.Manifest()
		}
	}
	t.Fatalf("no module %s", id)
	return core.Manifest{}
}

func TestA1_ScopesOfSectionZero(t *testing.T) {
	read := []string{
		"services.unit.list", "services.unit.get", "services.journal.query",
		"users.user.list", "users.key.list", "users.sshd.get",
		"fail2ban.status.get", "fail2ban.ban.list",
		"tuning.sysctl.get", "tuning.swap.get",
	}
	operate := []string{"services.unit.start", "services.unit.stop", "services.unit.restart", "services.unit.reload", "fail2ban.ban.remove"}
	admin := []string{
		"services.unit.enable", "services.unit.disable",
		"users.user.create", "users.user.lock", "users.user.unlock", "users.user.delete",
		// The spec's users.user.password.* and users.user.sudo.* are
		// registered as users.password.* and users.sudo.* (plan Header
		// amendment A32: action names have three parts).
		"users.password.set", "users.password.remove", "users.sudo.add", "users.sudo.remove",
		"users.key.add", "users.key.remove", "users.sshd.apply", "users.sshd.repair",
		"fail2ban.package.install", "fail2ban.package.remove", "fail2ban.settings.apply", "fail2ban.ban.add",
		"tuning.sysctl.apply", "tuning.sysctl.repair", "tuning.swap.create", "tuning.swap.resize", "tuning.swap.remove",
		"jobs.job.create", "jobs.job.update", "jobs.job.delete", "jobs.job.enable", "jobs.job.disable", "jobs.job.run", "jobs.schedule.check",
	}
	for _, n := range read {
		if got := core.RequiredScope(spec(t, n)); got != core.ScopeRead {
			t.Errorf("%s needs %q; want read", n, got)
		}
	}
	for _, n := range operate {
		if got := core.RequiredScope(spec(t, n)); got != core.ScopeOperate {
			t.Errorf("%s needs %q; want operate", n, got)
		}
	}
	for _, n := range admin {
		if got := core.RequiredScope(spec(t, n)); got != core.ScopeAdmin {
			t.Errorf("%s needs %q; want admin", n, got)
		}
	}
	// A viewer holds read, an operator operate: neither passes an admin action, a viewer passes no operate one.
	for _, n := range admin {
		need := core.RequiredScope(spec(t, n))
		if core.ScopeOperate.Allows(need) || core.ScopeRead.Allows(need) {
			t.Errorf("%s is open to an operator or a viewer", n)
		}
	}
	for _, n := range operate {
		if core.ScopeRead.Allows(core.RequiredScope(spec(t, n))) {
			t.Errorf("%s is open to a viewer", n)
		}
	}
}

func TestA3_EveryJobsActionIsAdminAndTheConsoleHasNoViewerReach(t *testing.T) {
	for _, a := range manifest(t, "jobs").Actions {
		n := a.Spec.Name
		if n == "jobs.job.list" {
			if core.RequiredScope(a.Spec) != core.ScopeRead {
				t.Errorf("jobs.job.list needs %q; a viewer reads it without commands", core.RequiredScope(a.Spec))
			}
			continue
		}
		if core.RequiredScope(a.Spec) != core.ScopeAdmin {
			t.Errorf("%s needs %q; every jobs action needs admin", n, core.RequiredScope(a.Spec))
		}
	}
	for _, a := range manifest(t, "console").Actions {
		if core.RequiredScope(a.Spec) != core.ScopeAdmin {
			t.Errorf("%s needs %q; everything in console needs admin, reads included", a.Spec.Name, core.RequiredScope(a.Spec))
		}
	}
}

func TestA101_RootGrantingUsersActionsNeedStepUp(t *testing.T) {
	// users.password.set and users.sudo.add are the spec's
	// users.user.password.set and users.user.sudo.add (plan amendment A32).
	for _, n := range []string{"users.user.create", "users.key.add", "users.password.set", "users.sudo.add"} {
		if !spec(t, n).StepUp {
			t.Errorf("%s does not require step-up", n)
		}
	}
	for _, n := range []string{"jobs.job.create", "jobs.job.update"} {
		if !spec(t, n).StepUp {
			t.Errorf("%s does not require step-up", n)
		}
	}
}

func TestA61_ListingAndStatNeedNoStepUp(t *testing.T) {
	for _, m := range modules.All() {
		for _, a := range m.Manifest().Actions {
			if a.Spec.StepUp && !strings.HasPrefix(a.Spec.Name, "users.") && !strings.HasPrefix(a.Spec.Name, "jobs.") {
				t.Errorf("%s requires step-up outside the users and jobs actions of spec 1.1", a.Spec.Name)
			}
		}
	}
}

func TestA56_NoSysctlKeyIsDeclaredTwice(t *testing.T) {
	if _, err := core.NewRegistry(modules.All()...); err != nil {
		t.Fatalf("the compiled-in modules do not register: %v", err)
	}
	seen := map[string]string{}
	for _, m := range modules.All() {
		mf := m.Manifest()
		for _, p := range mf.OwnedPaths {
			if p.Kind != core.OwnedSysctl {
				continue
			}
			if other, dup := seen[p.Path]; dup {
				t.Errorf("sysctl key %s is declared by %s and %s", p.Path, other, mf.ID)
			}
			seen[p.Path] = mf.ID
		}
	}
}

func TestA56_RegistryRefusesADuplicateSysctlDeclaration(t *testing.T) {
	mk := func(id string) core.Module {
		return fakeModule{core.Manifest{ID: id, Version: 1, OwnedPaths: []core.OwnedPath{
			{Path: "vm/swappiness", Kind: core.OwnedSysctl, Pattern: `^[0-9]+$`}}}}
	}
	if _, err := core.NewRegistry(mk("alpha"), mk("beta")); err == nil {
		t.Error("two modules declaring vm/swappiness register")
	}
}

type fakeModule struct{ m core.Manifest }

func (f fakeModule) Manifest() core.Manifest { return f.m }

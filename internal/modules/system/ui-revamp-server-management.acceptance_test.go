package system

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// Acceptance tests of the ui-revamp-server-management spec, system module.
// Expectations are the spec's literals; the sysHost fakes stand in for apt,
// dpkg, logind and Docker.

var qaDockerFive = []string{"docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"}

// qaSim builds an apt simulation: install lines, removal lines and the
// summary line that agrees with them.
func qaSim(installs []string, removes []string, upgraded int) string {
	var b strings.Builder
	for i, n := range installs {
		if i < upgraded {
			fmt.Fprintf(&b, "Inst %s [1.0] (2.0 Ubuntu:24.04/noble-updates [amd64])\n", n)
		} else {
			fmt.Fprintf(&b, "Inst %s (2.0 Ubuntu:24.04/noble-updates [amd64])\n", n)
		}
	}
	for _, n := range removes {
		fmt.Fprintf(&b, "Remv %s [1.0]\n", n)
	}
	fmt.Fprintf(&b, "%d upgraded, %d newly installed, %d to remove and 0 not upgraded.\n", upgraded, len(installs)-upgraded, len(removes))
	return b.String()
}

func qaHasMutatingApt(calls []string) bool {
	return slices.ContainsFunc(calls, func(v string) bool {
		return strings.HasPrefix(v, "/usr/bin/apt-get") && strings.Contains(v, " -y ")
	})
}

func TestA14_SystemModuleAvailableOnAllFiveReleases(t *testing.T) {
	releases := []struct{ id, codename string }{
		{"ubuntu", "jammy"}, {"ubuntu", "noble"}, {"ubuntu", "resolute"}, {"debian", "bookworm"}, {"debian", "trixie"},
	}
	for _, r := range releases {
		for _, arch := range []string{"amd64", "arm64"} {
			t.Run(r.id+"-"+r.codename+"-"+arch, func(t *testing.T) {
				h := newSysHost(t)
				h.osRelease(r.id, r.codename, arch)
				h.installed("bash") // no docker, no unattended-upgrades
				m := h.mod().Manifest()
				if m.ID != "system" || !m.DefaultEnabled {
					t.Fatalf("manifest id=%q defaultEnabled=%v", m.ID, m.DefaultEnabled)
				}
				if len(m.Preflight) == 0 {
					t.Fatal("no preflight")
				}
				for _, c := range m.Preflight {
					if err := c.Run(context.Background(), h.env()); err != nil {
						t.Fatalf("preflight %s: %v", c.ID, err)
					}
				}
			})
		}
	}
}

func qaUpdates(t *testing.T, h *sysHost, sim string) struct {
	Upgradable []struct {
		Name, From, To, Origin string
		Security               bool
	}
} {
	t.Helper()
	h.updatesReadable()
	h.run(aptGet+" "+strings.Join(platform.SystemAptRead("dist-upgrade", nil, nil), " "), sim, 0)
	h.run(aptGet+" "+strings.Join(platform.SystemAptRead("dist-upgrade", []string{"-o", "APT::Get::Always-Include-Phased-Updates=true"}, nil), " "), sim, 0)
	var got struct {
		Upgradable []struct {
			Name, From, To, Origin string
			Security               bool
		}
	}
	if err := json.Unmarshal([]byte(h.readResource(t, "updates")), &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func TestA15_UpdatesListsVersionsAndMarksSecuritySuites(t *testing.T) {
	cases := []struct {
		name, sim string
		security  bool
		to        string
	}{
		{"ubuntu security", "Inst openssl [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-security [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", true, "3.0.2-0ubuntu1.18"},
		{"ubuntu updates and security", "Inst openssl [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-updates, Ubuntu:22.04/jammy-security [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", true, "3.0.2-0ubuntu1.18"},
		{"debian security", "Inst openssl [3.0.15-1~deb12u1] (3.0.17-1~deb12u2 Debian-Security:12/oldstable-security [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", true, "3.0.17-1~deb12u2"},
		{"ubuntu updates only", "Inst openssl [3.0.2-0ubuntu1.15] (3.0.2-0ubuntu1.18 Ubuntu:22.04/jammy-updates [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", false, "3.0.2-0ubuntu1.18"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newSysHost(t)
			got := qaUpdates(t, h, c.sim)
			if len(got.Upgradable) != 1 || got.Upgradable[0].Name != "openssl" {
				t.Fatalf("upgradable = %+v", got.Upgradable)
			}
			u := got.Upgradable[0]
			if u.To != c.to || u.Security != c.security || !strings.HasPrefix(u.From, "3.0.") {
				t.Fatalf("entry = %+v, want candidate %s security %v", u, c.to, c.security)
			}
		})
	}
}

func TestA17_UpgradeAllIsAFullUpgradeWithARemovalGroup(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "oldlib")
	sim := qaSim([]string{"curl"}, []string{"oldlib"}, 1)
	h.run(planVec("dist-upgrade", nil), sim, 0)
	h.run(resimVec("dist-upgrade", nil), sim, 0)
	h.runChanges(runVec("dist-upgrade", nil), "", 0, "bash", "curl")
	plan := h.plan(t, "system.packages.upgrade", `{"all":true}`)
	if !slices.Contains(h.calls(), planVec("dist-upgrade", nil)) {
		t.Fatalf("upgrade all did not simulate dist-upgrade; ran %v", h.calls())
	}
	var removal string
	for _, l := range plan.Diff {
		if strings.Contains(l.Text, "oldlib") {
			removal = l.Op + " " + l.Text
		}
	}
	if !strings.Contains(strings.ToLower(removal), "remov") {
		t.Fatalf("no removal line for oldlib in:\n%s", plan.Text())
	}
	if _, err := h.applyTask("system.packages.upgrade", `{"all":true}`); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.calls(), runVec("dist-upgrade", nil)) {
		t.Fatalf("the real run was not dist-upgrade; ran %v", h.calls())
	}
}

func TestA17_UpgradeAllRefusesAProtectedRemovalNamingIt(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "openssh-server")
	h.run(planVec("dist-upgrade", nil), qaSim([]string{"curl"}, []string{"openssh-server"}, 1), 0)
	_, err := h.planErr("system.packages.upgrade", `{"all":true}`)
	if err == nil || !strings.Contains(err.Error(), "openssh-server") {
		t.Fatalf("err = %v, want a refusal naming openssh-server", err)
	}
}

func TestA18_InstallJqAndUndoRemovesExactlyWhatItAdded(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\nlibjq1\nlibonig5\n", 0)
	sim := "Inst libonig5 (6.9.9-1 Ubuntu:24.04/noble [amd64])\nInst libjq1 (1.7.1-3 Ubuntu:24.04/noble [amd64])\nInst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\n0 upgraded, 3 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install", []string{"jq"}), sim, 0)
	h.run(resimVec("install", []string{"jq"}), sim, 0)
	h.runChanges(runVec("install", []string{"jq"}), "", 0, "bash", "jq", "libjq1", "libonig5")
	if _, err := h.applyTask("system.packages.install", `{"names":["jq"]}`); err != nil {
		t.Fatal(err)
	}
	set := []string{"jq", "libjq1", "libonig5"}
	rm := qaSim(nil, set, 0)
	h.run(planVec("remove", set), rm, 0)
	h.run(resimVec("remove", set), rm, 0)
	h.runChanges(runVec("remove", set), "", 0, "bash")
	if err := h.undo("system.packages.install"); err != nil {
		t.Fatal(err)
	}
	var removes []string
	for _, c := range h.calls() {
		if strings.HasPrefix(c, "/usr/bin/apt-get") && strings.Contains(c, " -y ") && strings.Contains(c, " remove ") {
			removes = append(removes, c)
		}
	}
	if len(removes) != 1 || removes[0] != runVec("remove", set) {
		t.Fatalf("undo ran %v, want exactly %s", removes, runVec("remove", set))
	}
}

func TestA40_InstallUndoRefusedWhenALaterPackageDependsOnIt(t *testing.T) {
	h := newSysHost(t)
	h.recordedInstall([]string{"jq", "libjq1"})
	h.installed("bash", "jq", "libjq1", "jq-extras")
	bigger := qaSim(nil, []string{"jq-extras", "jq", "libjq1"}, 0)
	h.run(planVec("remove", []string{"jq", "libjq1"}), bigger, 0)
	h.run(resimVec("remove", []string{"jq", "libjq1"}), bigger, 0)
	h.runChanges(runVec("remove", []string{"jq", "libjq1"}), "", 0, "bash")
	err := h.undo("system.packages.install")
	if err == nil || !strings.Contains(err.Error(), "jq-extras") {
		t.Fatalf("err = %v, want a refusal naming jq-extras", err)
	}
	if qaHasMutatingApt(h.calls()) {
		t.Fatalf("a mutating apt run happened: %v", h.calls())
	}
}

func TestA19_InstallShowsRemovalsAndAppliesAProtectedOneIsRefused(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "mawk")
	h.run("/usr/bin/apt-cache pkgnames", "jq\nmawk\n", 0)
	sim := qaSim([]string{"jq"}, []string{"mawk"}, 0)
	h.run(planVec("install", []string{"jq"}), sim, 0)
	h.run(resimVec("install", []string{"jq"}), sim, 0)
	h.runChanges(runVec("install", []string{"jq"}), "", 0, "bash", "jq")
	plan := h.plan(t, "system.packages.install", `{"names":["jq"]}`)
	text := strings.ToLower(plan.Text())
	if !strings.Contains(text, "mawk") || !strings.Contains(text, "remov") || !strings.Contains(text, "reinstall") {
		t.Fatalf("plan lacks the removal group or the undo note:\n%s", plan.Text())
	}
	if _, err := h.applyTask("system.packages.install", `{"names":["jq"]}`); err != nil {
		t.Fatalf("an install with removals did not apply: %v", err)
	}

	p := newSysHost(t)
	p.installed("bash", "sudo")
	p.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	p.run(planVec("install", []string{"jq"}), qaSim([]string{"jq"}, []string{"sudo"}, 0), 0)
	if _, err := p.planErr("system.packages.install", `{"names":["jq"]}`); err == nil || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("err = %v, want a refusal naming sudo", err)
	}
}

// protectedCases are section 4's protected packages, each with the host
// state that makes it protected.
func qaProtected() []struct {
	name  string
	setup func(h *sysHost)
} {
	plain := func(*sysHost) {}
	cases := []struct {
		name  string
		setup func(h *sysHost)
	}{}
	for _, n := range []string{"apt", "dpkg", "systemd", "systemd-sysv", "dbus", "openssh-server", "sudo", "ufw", "tailscale", "netplan.io", "systemd-resolved", "ifupdown", "grub-pc", "grub-efi-amd64-bin", "shim-signed", "docker-ce", "docker-ce-cli", "containerd.io", "docker-buildx-plugin", "docker-compose-plugin"} {
		cases = append(cases, struct {
			name  string
			setup func(h *sysHost)
		}{n, plain})
	}
	for _, n := range []string{"iptables", "nftables"} {
		cases = append(cases, struct {
			name  string
			setup func(h *sysHost)
		}{n, func(h *sysHost) { h.firewallEnabled(true) }})
	}
	cases = append(cases,
		struct {
			name  string
			setup func(h *sysHost)
		}{"linux-image-6.8.0-45-generic", func(h *sysHost) { h.unameR("6.8.0-45-generic") }},
		struct {
			name  string
			setup func(h *sysHost)
		}{"linux-modules-6.8.0-45-generic", func(h *sysHost) { h.unameR("6.8.0-45-generic") }},
		struct {
			name  string
			setup func(h *sysHost)
		}{"coreutils", func(h *sysHost) { h.dpkgFields(map[string]string{"coreutils": "Essential: yes"}) }},
		struct {
			name  string
			setup func(h *sysHost)
		}{"libc6", func(h *sysHost) { h.dpkgFields(map[string]string{"libc6": "Priority: required"}) }},
	)
	return cases
}

func TestA20_EveryProtectedPackageIsRefusedByEveryPackageChange(t *testing.T) {
	for _, c := range qaProtected() {
		t.Run(c.name, func(t *testing.T) {
			check := func(label string, h *sysHost, action, input string) {
				t.Helper()
				_, err := h.planErr(action, input)
				if err == nil || !strings.Contains(err.Error(), c.name) {
					t.Errorf("%s: err = %v, want a refusal naming %s", label, err, c.name)
				}
			}
			mk := func() *sysHost {
				h := newSysHost(t)
				h.osRelease("ubuntu", "noble", "amd64")
				h.installed("bash", "libfoo", c.name)
				c.setup(h)
				h.firewallEnabled(c.name == "iptables" || c.name == "nftables")
				h.run("/usr/bin/apt-cache pkgnames", "libfoo\n", 0)
				return h
			}
			rm := qaSim(nil, []string{c.name}, 0)
			h := mk()
			h.run(planVec("remove", []string{"libfoo"}), rm, 0)
			check("remove", h, "system.packages.remove", `{"names":["libfoo"]}`)
			h = mk()
			h.run(planVec("install", []string{"libfoo"}), qaSim([]string{"libfoo"}, []string{c.name}, 0), 0)
			check("install", h, "system.packages.install", `{"names":["libfoo"]}`)
			h = mk()
			h.run(planVec("install-only-upgrade", []string{"libfoo"}), qaSim([]string{"libfoo"}, []string{c.name}, 1), 0)
			check("upgrade selected", h, "system.packages.upgrade", `{"names":["libfoo"]}`)
			h = mk()
			h.run(planVec("dist-upgrade", nil), qaSim([]string{"libfoo"}, []string{c.name}, 1), 0)
			check("upgrade all", h, "system.packages.upgrade", `{"all":true}`)
			h = mk()
			h.installed(append([]string{"bash", "libfoo", c.name}, qaDockerFive...)...)
			c.setup(h)
			h.run(planVec("install-only-upgrade", qaDockerFive), qaSim([]string{"docker-ce"}, []string{c.name}, 1), 0)
			check("docker update", h, "system.docker.engine-update", `{}`)
		})
	}
}

func TestA21_NamesOutsideThePatternAreRefusedBeforeAnyProgramRuns(t *testing.T) {
	bad := []string{"-o", "--help", "j", "Jq", "jq;rm", "jq rm", "jq$(id)", "+jq", ".jq", strings.Repeat("a", 65), ""}
	for _, name := range bad {
		for _, c := range []struct{ action, input string }{
			{"system.packages.install", fmt.Sprintf(`{"names":[%q]}`, name)},
			{"system.packages.remove", fmt.Sprintf(`{"names":[%q]}`, name)},
			{"system.packages.remove", fmt.Sprintf(`{"names":[%q],"purge":true}`, name)},
			{"system.packages.upgrade", fmt.Sprintf(`{"names":[%q]}`, name)},
		} {
			h := newSysHost(t)
			h.installed("bash", "jq")
			if _, err := h.planErr(c.action, c.input); err == nil {
				t.Errorf("%s %s: accepted", c.action, c.input)
			}
			if got := h.calls(); len(got) != 0 {
				t.Errorf("%s %s: ran %v before refusing", c.action, c.input, got)
			}
		}
		h := newSysHost(t)
		if _, err := h.callReadErr("system.packages.search", fmt.Sprintf(`{"term":%q}`, name)); err == nil {
			t.Errorf("search term %q accepted", name)
		}
		if got := h.calls(); len(got) != 0 {
			t.Errorf("search term %q: ran %v", name, got)
		}
	}
}

func TestA21_NamesMustBeExactPackagesBeforeAptRuns(t *testing.T) {
	for _, name := range []string{"jq-", "jq+", "j.q"} {
		h := newSysHost(t)
		h.installed("bash", "jq")
		h.run("/usr/bin/apt-cache pkgnames", "jq\nlibjq1\n", 0)
		for _, c := range []struct{ action, input string }{
			{"system.packages.install", fmt.Sprintf(`{"names":[%q]}`, name)},
			{"system.packages.remove", fmt.Sprintf(`{"names":[%q]}`, name)},
			{"system.packages.remove", fmt.Sprintf(`{"names":[%q],"purge":true}`, name)},
			{"system.packages.upgrade", fmt.Sprintf(`{"names":[%q]}`, name)},
		} {
			if _, err := h.planErr(c.action, c.input); err == nil {
				t.Errorf("%s %s: accepted a name that is not an exact package", c.action, c.input)
			}
		}
		for _, call := range h.calls() {
			if strings.HasPrefix(call, "/usr/bin/apt-get") {
				t.Errorf("%q: apt-get ran: %s", name, call)
			}
		}
	}
}

func TestA22_AptBusyAtPlanAndAtApplyChangesNothing(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	busy := "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 485 (apt-get)\nE: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?\n"
	h.run(planVec("install", []string{"jq"}), busy, 100)
	h.run(aptGet+" "+strings.Join(platform.SystemAptCheck(), " "), busy, 100)
	_, err := h.planErr("system.packages.install", `{"names":["jq"]}`)
	if errcode.From(err).Code != "system.apt_busy" {
		t.Fatalf("plan err = %v, want system.apt_busy", err)
	}
	if !strings.Contains(planVec("install", []string{"jq"}), "DPkg::Lock::Timeout=10") {
		t.Fatalf("the plan simulation waits for the lock for other than 10 s: %s", planVec("install", []string{"jq"}))
	}
	if v := runVec("install", []string{"jq"}); !strings.Contains(v, "DPkg::Lock::Timeout=120") {
		t.Fatalf("the real run waits for the lock for other than 120 s: %s", v)
	}

	// The real run finds the lock held past the timeout.
	a := newSysHost(t)
	a.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	sim := qaSim([]string{"jq"}, nil, 0)
	a.run(planVec("install", []string{"jq"}), sim, 0)
	a.run(resimVec("install", []string{"jq"}), sim, 0)
	a.run(runVec("install", []string{"jq"}), "Waiting for cache lock: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 485 (apt-get)...\nE: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?\n", 100)
	_, err = a.applyTask("system.packages.install", `{"names":["jq"]}`)
	if errcode.From(err).Code != "system.apt_busy" {
		t.Fatalf("apply err = %v, want system.apt_busy", err)
	}
	if strings.Contains(a.readResource(t, "packages"), `"name":"jq"`) {
		t.Fatal("jq is installed after a refused run")
	}

	// The updates read does not wait behind apt.
	r := newSysHost(t)
	r.updatesReadable()
	_ = r.readResource(t, "updates")
	for _, c := range r.calls() {
		if strings.HasPrefix(c, "/usr/bin/apt-get") && !strings.Contains(c, "Debug::NoLocking=true") {
			t.Errorf("the updates read runs a locking apt-get: %s", c)
		}
	}
}

func TestA23_RebootResource(t *testing.T) {
	t.Run("reboot-required file", func(t *testing.T) {
		h := newSysHost(t)
		h.file("/run/reboot-required", []byte("*** System restart required ***\n"))
		h.file("/run/reboot-required.pkgs", []byte("libc6\nlinux-image-6.8.0-50-generic\n"))
		var got struct {
			Required bool
			Packages []string
		}
		if err := json.Unmarshal([]byte(h.readResource(t, "reboot")), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Required || !slices.Contains(got.Packages, "libc6") {
			t.Fatalf("reboot = %+v", got)
		}
	})
	t.Run("ubuntu older running kernel", func(t *testing.T) {
		h := newSysHost(t)
		h.unameR("6.8.0-45-generic")
		h.installed("bash", "linux-image-6.8.0-45-generic=6.8.0-45.45", "linux-image-6.8.0-50-generic=6.8.0-50.51")
		var got struct{ Required bool }
		if err := json.Unmarshal([]byte(h.readResource(t, "reboot")), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Required {
			t.Fatal("an older running kernel was not reported")
		}
	})
	t.Run("ubuntu newest kernel running", func(t *testing.T) {
		h := newSysHost(t)
		h.unameR("6.8.0-50-generic")
		h.installed("bash", "linux-image-6.8.0-45-generic=6.8.0-45.45", "linux-image-6.8.0-50-generic=6.8.0-50.51")
		var got struct{ Required bool }
		if err := json.Unmarshal([]byte(h.readResource(t, "reboot")), &got); err != nil {
			t.Fatal(err)
		}
		if got.Required {
			t.Fatal("a reboot was reported with the newest kernel running")
		}
	})
	t.Run("debian running version from /proc/version", func(t *testing.T) {
		h := newSysHost(t)
		h.osRelease("debian", "bookworm", "amd64")
		h.unameR("6.1.0-18-amd64")
		h.file("/proc/version", []byte("Linux version 6.1.0-18-amd64 (debian-kernel@lists.debian.org) (gcc-12 (Debian 12.2.0-14) 12.2.0) #1 SMP PREEMPT_DYNAMIC Debian 6.1.76-1 (2024-02-01)\n"))
		h.installed("bash", "linux-image-6.1.0-18-amd64=6.1.82-1")
		var got struct{ Required bool }
		if err := json.Unmarshal([]byte(h.readResource(t, "reboot")), &got); err != nil {
			t.Fatal(err)
		}
		if !got.Required {
			t.Fatal("debian: a newer installed kernel package version was not reported")
		}
	})
}

func TestA24_RebootSchedulesTenToThirtySecondsAhead(t *testing.T) {
	h := newSysHost(t)
	h.file("/proc/uptime", []byte("3600.00 7000.00\n"))
	out, err := h.applyTask("system.host.reboot", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	d := h.reboot.scheduledAt.Sub(h.now())
	if d < 10*time.Second || d > 30*time.Second {
		t.Fatalf("reboot scheduled %v after the change records success, want 10 to 30 s", d)
	}
	var got struct{ ScheduledAt, BootTimeBefore string }
	if err := json.Unmarshal([]byte(out), &got); err != nil || got.ScheduledAt == "" || got.BootTimeBefore == "" {
		t.Fatalf("output = %s, %v", out, err)
	}
}

func TestA41_RebootRefusalsNameTheChangeAndLogindsReasonFailsIt(t *testing.T) {
	for _, state := range []string{core.StateApplying, core.StateAwaitingConfirmation} {
		h := newSysHost(t)
		h.pendingChange("change-123", "system.packages.install", state)
		_, err := h.planErr("system.host.reboot", `{}`)
		if err == nil || !strings.Contains(err.Error(), "change-123") {
			t.Errorf("%s: err = %v, want a refusal naming the change", state, err)
		}
	}
	h := newSysHost(t)
	h.file("/proc/uptime", []byte("3600.00 7000.00\n"))
	h.reboot.err = &platform.LogindError{Reason: "Operation inhibited by Xyzzy backup"}
	_, err := h.applyTask("system.host.reboot", `{}`)
	if err == nil || !strings.Contains(err.Error(), "Operation inhibited by Xyzzy backup") {
		t.Fatalf("err = %v, want logind's reason", err)
	}
}

func TestA25_AutoUpdatesWritesTheManagedFileAndUndoRestoresIt(t *testing.T) {
	const path = "/etc/apt/apt.conf.d/52darkpanel-auto-upgrades"
	for _, on := range []bool{true, false} {
		t.Run(fmt.Sprintf("enabled=%v", on), func(t *testing.T) {
			h := newSysHost(t)
			h.installed("bash", "unattended-upgrades")
			prior := "// prior\nAPT::Periodic::Update-Package-Lists \"0\";\n"
			h.file(path, []byte(prior))
			v := "0"
			if on {
				v = "1"
			}
			h.run("/usr/bin/apt-config dump", fmt.Sprintf("APT::Periodic::Update-Package-Lists \"%s\";\nAPT::Periodic::Unattended-Upgrade \"%s\";\n", v, v), 0)
			if _, err := h.applyTask("system.autoupdates.set", fmt.Sprintf(`{"enabled":%v}`, on)); err != nil {
				t.Fatal(err)
			}
			got := string(h.written(path))
			for _, key := range []string{"APT::Periodic::Update-Package-Lists", "APT::Periodic::Unattended-Upgrade"} {
				if !regexp.MustCompile(regexp.QuoteMeta(key) + `\s+"` + v + `"`).MatchString(got) {
					t.Fatalf("file lacks %s %q:\n%s", key, v, got)
				}
			}
			if err := h.undo("system.autoupdates.set"); err != nil {
				t.Fatal(err)
			}
			b, err := h.readFile(path)
			if err != nil || string(b) != prior {
				t.Fatalf("undo left %q, %v; want the prior file %q", b, err, prior)
			}
		})
	}
}

func TestA53_AutoUpdatesRefusedWhenUnattendedUpgradesIsAbsent(t *testing.T) {
	h := newSysHost(t)
	_, err := h.planErr("system.autoupdates.set", `{"enabled":true}`)
	if err == nil || !strings.Contains(err.Error(), "Install unattended-upgrades first") {
		t.Fatalf("plan err = %v", err)
	}
	_, err = h.applyTask("system.autoupdates.set", `{"enabled":true}`)
	if err == nil || h.exists("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades") {
		t.Fatalf("apply err = %v, the file exists = %v", err, h.exists("/etc/apt/apt.conf.d/52darkpanel-auto-upgrades"))
	}
}

func TestA28_DockerInstallRefusedWhileConflictingPackagesAreInstalled(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("ubuntu", "noble", "amd64")
	h.installed("bash", "docker.io", "podman-docker")
	for _, step := range []string{"plan", "apply"} {
		var err error
		if step == "plan" {
			_, err = h.planErr("system.docker.engine-install", `{}`)
		} else {
			_, err = h.applyTask("system.docker.engine-install", `{}`)
		}
		if err == nil || !strings.Contains(err.Error(), "docker.io") || !strings.Contains(err.Error(), "podman-docker") {
			t.Fatalf("%s: err = %v, want both conflicting packages named", step, err)
		}
	}
	if w := h.writtenPaths(); len(w) != 0 || qaHasMutatingApt(h.calls()) {
		t.Fatalf("a refused install changed something: wrote %v, ran %v", w, h.calls())
	}
}

// qaOtherKeyring is a well-formed OpenPGP public-key packet whose
// fingerprint is not Docker's.
func qaOtherKeyring() []byte {
	body := []byte{0x04, 0, 0, 0, 1, 0x01, 0x00, 0x09, 0x01, 0xAB, 0x00, 0x11, 0x01, 0x00, 0x01}
	// Tag 6 with a two-byte length: the body's 15 bytes.
	return append([]byte{0x99, 0x00, 0x0F}, body...)
}

func TestA29_InstallRefusedBeforeWritingWhenTheKeyringFingerprintDiffers(t *testing.T) {
	other, err := platform.KeyringFingerprints(qaOtherKeyring())
	if err != nil || len(other) != 1 || other[0] == "9DC858229FC7DD38854AE2D88D81803C0EBFCD88" {
		t.Fatalf("the stand-in keyring is not a different key: %v, %v", other, err)
	}
	for name, ring := range map[string][]byte{"a different key": qaOtherKeyring(), "not a keyring": []byte("this is not the pinned keyring")} {
		t.Run(name, func(t *testing.T) {
			h := newSysHost(t)
			h.osRelease("ubuntu", "noble", "amd64")
			h.keyringOverride(ring)
			h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
			sim := "Inst docker-ce (5:27.3.1-1 Docker:noble [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n"
			h.run(resimVec("install", qaDockerFive), sim, 0)
			h.runChanges(runVec("install", qaDockerFive), "", 0, append([]string{"bash"}, qaDockerFive...)...)
			if _, err := h.applyTask("system.docker.engine-install", `{}`); err == nil {
				t.Fatal("an install with a keyring of another fingerprint was not refused")
			}
			if w := h.writtenPaths(); len(w) != 0 {
				t.Fatalf("wrote %v before the keyring check", w)
			}
			if qaHasMutatingApt(h.calls()) {
				t.Fatalf("apt ran: %v", h.calls())
			}
		})
	}
}

func TestA30_EngineUpdateUpgradesTheFivePackagesAndNamesRestartingContainers(t *testing.T) {
	h := newSysHost(t)
	h.installed(append([]string{"bash"}, qaDockerFive...)...)
	h.dockerDaemon(true)
	h.dockerCounts(3, 0, 0, nil)
	sim := "Inst docker-ce [5:26.0.0-1] (5:27.3.1-1 Docker:noble [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install-only-upgrade", qaDockerFive), sim, 0)
	h.run(resimVec("install-only-upgrade", qaDockerFive), sim, 0)
	h.runChanges(runVec("install-only-upgrade", qaDockerFive), "", 0, append([]string{"bash"}, qaDockerFive...)...)
	name := "system.docker.engine.update"
	if _, err := h.action(name); err != nil {
		// Section 4 names it .engine.update, which the registry's three-segment
		// action name pattern cannot hold; the module registers .engine-update.
		t.Logf("note: %v; using system.docker.engine-update", err)
		name = "system.docker.engine-update"
	}
	plan := h.plan(t, name, `{}`)
	if !regexp.MustCompile(`3 running containers? will restart`).MatchString(plan.Text()) {
		t.Fatalf("plan does not name the 3 running containers that restart:\n%s", plan.Text())
	}
	if _, err := h.applyTask(name, `{}`); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.calls(), runVec("install-only-upgrade", qaDockerFive)) {
		t.Fatalf("the five packages were not upgraded: %v", h.calls())
	}
}

func TestA31_DockerUninstallAndPurge(t *testing.T) {
	setup := func(t *testing.T) *sysHost {
		h := newSysHost(t)
		h.installed(append([]string{"bash"}, qaDockerFive...)...)
		h.dockerDaemon(true)
		h.dockerCounts(3, 5, 2, nil)
		sorted := slices.Sorted(slices.Values(qaDockerFive))
		rm := qaSim(nil, sorted, 0)
		h.run(planVec("remove", sorted), rm, 0)
		h.run(resimVec("remove", sorted), rm, 0)
		h.runChanges(runVec("remove", sorted), "", 0, "bash")
		return h
	}
	t.Run("without purge keeps the data", func(t *testing.T) {
		h := setup(t)
		if _, err := h.planErr("system.docker.uninstall", `{}`); err != nil {
			t.Fatal(err)
		}
		if _, err := h.applyTask("system.docker.uninstall", `{}`); err != nil {
			t.Fatal(err)
		}
		if len(h.purge.purged) != 0 {
			t.Fatalf("purged %v without the purge box", h.purge.purged)
		}
	})
	t.Run("with purge names the counts and removes exactly the two directories", func(t *testing.T) {
		h := setup(t)
		p := h.plan(t, "system.docker.uninstall", `{"purge":true}`)
		if p.Phrase != "purge docker on "+h.hostName() {
			t.Fatalf("phrase = %q", p.Phrase)
		}
		text := p.Text() + p.Phrase
		for _, re := range []string{`3 containers?`, `5 images?`, `2 volumes?`} {
			if !regexp.MustCompile(re).MatchString(text) {
				t.Errorf("plan does not name %q:\n%s", re, text)
			}
		}
		if _, err := h.applyTask("system.docker.uninstall", `{"purge":true}`); err != nil {
			t.Fatal(err)
		}
		got := slices.Clone(h.purge.purged)
		slices.Sort(got)
		if !slices.Equal(got, []string{"/var/lib/containerd", "/var/lib/docker"}) {
			t.Fatalf("purged %v, want exactly /var/lib/docker and /var/lib/containerd", got)
		}
	})
	t.Run("a symlink or mount point is refused at plan", func(t *testing.T) {
		h := setup(t)
		h.purge.checkErr = platform.ErrPurgeSymlink
		if _, err := h.planErr("system.docker.uninstall", `{"purge":true}`); err == nil {
			t.Fatal("the plan accepted a purge of a symlinked path")
		}
		h.purge.checkErr = platform.ErrPurgeMount
		if _, err := h.planErr("system.docker.uninstall", `{"purge":true}`); err == nil {
			t.Fatal("the plan accepted a purge of a mount point")
		}
	})
	t.Run("daemon not answering refuses the purge and allows a plain uninstall", func(t *testing.T) {
		h := setup(t)
		h.dockerDaemon(false)
		if _, err := h.planErr("system.docker.uninstall", `{"purge":true}`); err == nil {
			t.Fatal("a purge was planned while the daemon does not answer")
		}
		p, err := h.planErr("system.docker.uninstall", `{}`)
		if err != nil {
			t.Fatalf("an uninstall without purge was refused: %v", err)
		}
		if !strings.Contains(strings.ToLower(p.Text()), "could not run") {
			t.Fatalf("plan does not say the labelled-container check could not run:\n%s", p.Text())
		}
	})
}

func TestA44_DockerUninstallRefusals(t *testing.T) {
	base := func(t *testing.T) *sysHost {
		h := newSysHost(t)
		h.installed(append([]string{"bash"}, qaDockerFive...)...)
		h.dockerDaemon(true)
		h.dockerCounts(2, 0, 0, nil)
		sorted := slices.Sorted(slices.Values(qaDockerFive))
		h.run(planVec("remove", sorted), qaSim(nil, sorted, 0), 0)
		return h
	}
	t.Run("firewall integration on", func(t *testing.T) {
		h := base(t)
		h.firewallDockerIntegration(true)
		_, err := h.planErr("system.docker.uninstall", `{}`)
		if err == nil || !strings.Contains(err.Error(), "firewall.docker.disable") {
			t.Fatalf("err = %v, want a refusal naming firewall.docker.disable", err)
		}
	})
	t.Run("firewall change awaiting confirmation", func(t *testing.T) {
		h := base(t)
		h.pendingChange("fw-change-7", "firewall.rules.apply", core.StateAwaitingConfirmation)
		_, err := h.planErr("system.docker.uninstall", `{}`)
		if err == nil || !strings.Contains(err.Error(), "fw-change-7") {
			t.Fatalf("err = %v, want a refusal naming the change", err)
		}
	})
	t.Run("labelled containers", func(t *testing.T) {
		h := base(t)
		h.dockerCounts(2, 0, 0, map[string]string{"npm-proxy": "npm", "derp-1": "derp"})
		_, err := h.planErr("system.docker.uninstall", `{}`)
		if err == nil || !strings.Contains(err.Error(), "npm-proxy") || !strings.Contains(err.Error(), "derp-1") {
			t.Fatalf("err = %v, want both containers named", err)
		}
	})
	t.Run("refusal changes nothing", func(t *testing.T) {
		h := base(t)
		h.firewallDockerIntegration(true)
		_, _ = h.applyTask("system.docker.uninstall", `{}`)
		if qaHasMutatingApt(h.calls()) || len(h.purge.purged) != 0 {
			t.Fatalf("a refused uninstall changed something: %v", h.calls())
		}
	})
}

func TestA45_DriftIsPlanStateChangedAndTheTaskResimulationIsPlanStale(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\nhtop\n", 0)
	one := qaSim([]string{"jq"}, nil, 0)
	h.run(planVec("install", []string{"jq"}), one, 0)
	p := h.plan(t, "system.packages.install", `{"names":["jq"]}`)
	h.run(planVec("install", []string{"jq"}), qaSim([]string{"jq", "htop"}, nil, 0), 0)
	h.run(resimVec("install", []string{"jq"}), qaSim([]string{"jq", "htop"}, nil, 0), 0)
	h.runChanges(runVec("install", []string{"jq"}), "", 0, "bash", "jq", "htop")
	_, err := h.applyPlan(p)
	if errcode.From(err).Code != "plan.state_changed" {
		t.Fatalf("err = %v, want plan.state_changed", err)
	}
	if qaHasMutatingApt(h.calls()) {
		t.Fatalf("a drifted plan changed something: %v", h.calls())
	}

	s := newSysHost(t)
	s.run("/usr/bin/apt-cache pkgnames", "jq\nhtop\n", 0)
	s.run(planVec("install", []string{"jq"}), one, 0)
	s.run(resimVec("install", []string{"jq"}), qaSim([]string{"jq", "htop"}, nil, 0), 0)
	_, err = s.applyTask("system.packages.install", `{"names":["jq"]}`)
	if errcode.From(err).Code != "system.plan_stale" {
		t.Fatalf("err = %v, want system.plan_stale", err)
	}
	if slices.Contains(s.calls(), runVec("install", []string{"jq"})) {
		t.Fatal("the real run happened after a differing re-simulation")
	}
}

func TestA47_FailedAptRunsCarryTheirCodeThePackageAndTheLast40Lines(t *testing.T) {
	var lines []string
	for i := 1; i <= 60; i++ {
		lines = append(lines, fmt.Sprintf("progress line %d", i))
	}
	filler := strings.Join(lines, "\n")
	cases := []struct {
		code  errcode.Code
		cause string
	}{
		{"system.apt_busy", "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 485 (apt-get)"},
		{"system.repo_signature", "W: GPG error: https://repo.example stable InRelease: The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 0123456789ABCDEF\nE: The repository 'https://repo.example stable InRelease' is not signed."},
		{"system.disk_full", "dpkg: error processing archive /var/cache/apt/archives/jq_1.7_amd64.deb (--unpack):\n cannot copy extracted data for './usr/bin/jq' to '/usr/bin/jq.dpkg-new': failed to write (No space left on device)"},
		{"system.broken_packages", "E: Unable to correct problems, you have held broken packages."},
		{"system.dpkg_interrupted", "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem."},
	}
	for _, c := range cases {
		t.Run(string(c.code), func(t *testing.T) {
			h := newSysHost(t)
			h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
			sim := qaSim([]string{"jq"}, nil, 0)
			h.run(planVec("install", []string{"jq"}), sim, 0)
			h.run(resimVec("install", []string{"jq"}), sim, 0)
			out := filler + "\ndpkg: error processing package jq (--configure):\n" + c.cause + "\n"
			h.run(runVec("install", []string{"jq"}), out, 100)
			_, err := h.applyTask("system.packages.install", `{"names":["jq"]}`)
			e := errcode.From(err)
			if e.Code != c.code {
				t.Fatalf("code = %s (%v), want %s", e.Code, err, c.code)
			}
			full := err.Error() + " " + e.Detail
			if !strings.Contains(full, "jq") {
				t.Errorf("the error does not name the failing package: %s", full)
			}
			if !strings.Contains(full, "progress line 60") {
				t.Errorf("the error does not carry the last lines: %s", full)
			}
			if strings.Contains(full, "progress line 1\n") || strings.Contains(full, "progress line 10\n") {
				t.Errorf("the error carries more than the last 40 lines")
			}
		})
	}
}

func TestA52_SearchTermIsALiteralSubstringNeverAPattern(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "a.out-tools\nabc\nlibva.2\nzlib1g\n", 0)
	policy := "a.out-tools:\n  Installed: (none)\n  Candidate: 1.0\nlibva.2:\n  Installed: (none)\n  Candidate: 2.1\n"
	h.run("/usr/bin/apt-cache policy -- a.out-tools libva.2", policy, 0)
	h.run("/usr/bin/apt-cache policy -- a.out-tools abc libva.2", policy+"abc:\n  Installed: (none)\n  Candidate: 1.0\n", 0)
	out := h.callRead(t, "system.packages.search", `{"term":"a."}`)
	var got []struct{ Name string }
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, g := range got {
		names = append(names, g.Name)
	}
	slices.Sort(names)
	if !slices.Equal(names, []string{"a.out-tools", "libva.2"}) {
		t.Fatalf("search a. = %v, want only names containing the literal a.", names)
	}
	for _, c := range h.calls() {
		if strings.Contains(c, "a.") && strings.Contains(c, "pkgnames") {
			t.Errorf("the term reached apt-cache: %s", c)
		}
	}
}

func TestA55_SimulationParserCountsPurgAndRefusesUnknownLinesAndCountMismatch(t *testing.T) {
	t.Run("a Purg line is a removal and a protected purge is refused", func(t *testing.T) {
		h := newSysHost(t)
		h.installed("bash", "libpam-foo", "sudo")
		h.run(planVec("purge", []string{"libpam-foo"}), "Purg libpam-foo [1.0]\nPurg sudo [1.9.16]\n0 upgraded, 0 newly installed, 2 to remove and 0 not upgraded.\n", 0)
		_, err := h.planErr("system.packages.remove", `{"names":["libpam-foo"],"purge":true}`)
		if err == nil || !strings.Contains(err.Error(), "sudo") {
			t.Fatalf("err = %v, want a refusal naming sudo", err)
		}
	})
	t.Run("an unknown action line refuses the plan", func(t *testing.T) {
		h := newSysHost(t)
		h.installed("bash", "jq")
		h.run(planVec("remove", []string{"jq"}), "Xyzz jq [1.7]\nRemv jq [1.7]\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
		if _, err := h.planErr("system.packages.remove", `{"names":["jq"]}`); err == nil {
			t.Fatal("a plan with an unknown action line was accepted")
		}
	})
	t.Run("counts that disagree with the summary refuse the plan", func(t *testing.T) {
		h := newSysHost(t)
		h.installed("bash", "jq")
		h.run(planVec("remove", []string{"jq"}), "Remv jq [1.7]\n0 upgraded, 0 newly installed, 2 to remove and 0 not upgraded.\n", 0)
		if _, err := h.planErr("system.packages.remove", `{"names":["jq"]}`); err == nil {
			t.Fatal("a plan whose counts disagree with the summary line was accepted")
		}
	})
}

func TestA56_RefreshWithAFailingSourceCompletesAndNamesIt(t *testing.T) {
	h := newSysHost(t)
	upd := "/usr/bin/apt-get " + strings.Join(platform.SystemAptUpdate(""), " ")
	out := "Hit:1 http://archive.ubuntu.com/ubuntu noble InRelease\nErr:2 http://127.0.0.1:9/debian nowhere InRelease\n  Could not connect to 127.0.0.1:9 (127.0.0.1). - connect (111: Connection refused)\nReading package lists...\nW: Failed to fetch http://127.0.0.1:9/debian/dists/nowhere/InRelease  Could not connect to 127.0.0.1:9 (127.0.0.1). - connect (111: Connection refused)\nW: Some index files failed to download. They have been ignored, or old ones used instead.\n"
	h.run(strings.TrimSpace(upd), out, 0)
	res, err := h.applyTask("system.index.refresh", `{}`)
	if err != nil {
		t.Fatalf("a refresh with a failing source failed: %v", err)
	}
	if !strings.Contains(res, "127.0.0.1:9") {
		t.Fatalf("the refresh output does not name the failed source: %s", res)
	}
	h.updatesReadable()
	if got := h.readResource(t, "updates"); !strings.Contains(got, "127.0.0.1:9") {
		t.Fatalf("the updates resource does not name the failed source: %s", got)
	}
}

func TestA46_UpdatesReportsDpkgInterruptedAndRepairIsOffered(t *testing.T) {
	h := newSysHost(t)
	h.updatesReadable()
	h.run("/usr/bin/dpkg --audit", "The following packages are only half configured, probably due to problems\nconfiguring them the first time.  The configuration should be retried using\ndpkg --configure <package> or the configure menu option in dselect:\n curl  command line tool\n", 0)
	if got := h.readResource(t, "updates"); !strings.Contains(got, `"dpkgInterrupted":true`) {
		t.Fatalf("updates = %s", got)
	}
	p, err := h.planErr("system.packages.repair", `{}`)
	if err != nil || !strings.Contains(p.Text(), "curl") {
		t.Fatalf("repair plan = %v, %v", p.Text(), err)
	}
}

func TestA29_TheEmbeddedKeyringHoldsExactlyThePinnedFingerprint(t *testing.T) {
	b, err := os.ReadFile("docker_keyring.gpg")
	if err != nil {
		t.Fatal(err)
	}
	fps, err := platform.KeyringFingerprints(b)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(fps, "9DC858229FC7DD38854AE2D88D81803C0EBFCD88") {
		t.Fatalf("the embedded keyring's fingerprints are %v, want 9DC858229FC7DD38854AE2D88D81803C0EBFCD88", fps)
	}
}

func TestA26_DockerInstallWritesTheRepositoryWithThePinnedKeyInstallsTheFivePackagesAndRechecksDocker(t *testing.T) {
	h := newSysHost(t)
	h.osRelease("ubuntu", "noble", "amd64")
	h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), "Hit:1 https://download.docker.com/linux/ubuntu noble InRelease\n", 0)
	sim := "Inst containerd.io (1.7.24-1 Docker:noble [amd64])\nInst docker-buildx-plugin (0.17.1 Docker:noble [amd64])\nInst docker-ce-cli (5:27.3.1 Docker:noble [amd64])\nInst docker-ce (5:27.3.1 Docker:noble [amd64])\nInst docker-compose-plugin (2.29.7 Docker:noble [amd64])\n0 upgraded, 5 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(resimVec("install", qaDockerFive), sim, 0)
	h.runChanges(runVec("install", qaDockerFive), "", 0, append([]string{"bash"}, qaDockerFive...)...)
	out, err := h.applyTask("system.docker.engine-install", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range qaDockerFive {
		if !strings.Contains(out, `"`+p+`"`) {
			t.Errorf("the change output does not list %s: %s", p, out)
		}
	}
	list := string(h.written("/etc/apt/sources.list.d/docker.list"))
	if !strings.Contains(list, "https://download.docker.com/linux/ubuntu noble stable") || !strings.Contains(list, "signed-by=/usr/share/keyrings/docker-archive-keyring.gpg") {
		t.Errorf("docker.list = %q", list)
	}
	fps, err := platform.KeyringFingerprints(h.written("/usr/share/keyrings/docker-archive-keyring.gpg"))
	if err != nil || !slices.Contains(fps, "9DC858229FC7DD38854AE2D88D81803C0EBFCD88") {
		t.Errorf("the keyring written holds %v (%v)", fps, err)
	}
	// The module's availability is read again after each of these actions, so
	// the Docker module reads available without an agent restart.
	for _, name := range []string{"system.docker.install", "system.docker.uninstall", "system.docker.engine-update", "system.packages.install", "system.packages.remove", "system.packages.upgrade", "system.packages.repair"} {
		a, err := h.action(name)
		if err != nil {
			t.Errorf("%v", err)
			continue
		}
		if !slices.Equal(a.Spec.Rechecks, []string{"docker"}) {
			t.Errorf("%s rechecks %v, want [docker]", name, a.Spec.Rechecks)
		}
	}
}

func TestA27_DockerInstallWithTheFirewallFlagEndsWithTheFirewallDockerIntegration(t *testing.T) {
	c := dockerInstallComposer{}
	steps, err := c.Steps(context.Background(), core.Env{}, dockerInstallIn{HostID: "h1", FirewallIntegration: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(steps) < 2 || steps[len(steps)-1].Action.Name != "firewall.docker.enable" {
		t.Fatalf("steps = %+v, want the install to end with firewall.docker.enable", steps)
	}
	steps, _ = c.Steps(context.Background(), core.Env{}, dockerInstallIn{HostID: "h1", FirewallIntegration: false})
	for _, s := range steps {
		if strings.HasPrefix(s.Action.Name, "firewall.") {
			t.Errorf("a firewall step is planned without the flag: %+v", s)
		}
	}
}

func TestA56_DockersOwnRefreshFailsTheStepEvenWhenAptExitsZero(t *testing.T) {
	for name, out := range map[string]string{
		"fetch failure":     "Err:1 https://download.docker.com/linux/ubuntu noble InRelease\n  Could not connect to download.docker.com:443\nW: Failed to fetch https://download.docker.com/linux/ubuntu/dists/noble/InRelease\nW: Some index files failed to download.\n",
		"signature failure": "Get:1 https://download.docker.com/linux/ubuntu noble InRelease\nW: GPG error: https://download.docker.com/linux/ubuntu noble InRelease: The following signatures couldn't be verified because the public key is not available: NO_PUBKEY 7EA0A9C3F273FCD8\nE: The repository 'https://download.docker.com/linux/ubuntu noble InRelease' is not signed.\n",
	} {
		t.Run(name, func(t *testing.T) {
			h := newSysHost(t)
			h.osRelease("ubuntu", "noble", "amd64")
			h.run(aptGet+" "+strings.Join(platform.SystemAptUpdate("/etc/apt/sources.list.d/docker.list"), " "), out, 0)
			if _, err := h.applyTask("system.docker.engine-install", `{}`); err == nil {
				t.Fatal("the install went on after Docker's own source failed to refresh")
			}
			if qaHasMutatingApt(h.calls()) {
				t.Fatalf("packages were installed: %v", h.calls())
			}
		})
	}
}

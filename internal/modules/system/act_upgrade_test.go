package system

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

func TestUpgradeAllPlansAFullUpgrade(t *testing.T) {
	h := newSysHost(t)
	h.run(planVec("dist-upgrade", nil), "Inst curl [1] (2 Ubuntu:24.04/noble-updates [amd64])\nInst docker-ce [5:27.0] (5:27.3 Docker:noble [amd64])\nRemv oldlib [1.0]\n2 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	h.dockerDaemon(true)
	h.dockerCounts(4, 0, 0, nil)
	plan := h.plan(t, "system.packages.upgrade", `{"all":true}`)
	for _, want := range []string{"curl", "docker-ce", "Remove: oldlib", "4 running containers will restart"} {
		if !strings.Contains(plan.Text(), want) {
			t.Errorf("plan lacks %q:\n%s", want, plan.Text())
		}
	}
	if plan.Phrase != h.hostName() {
		t.Errorf("phrase = %q", plan.Phrase)
	}
}

func TestUpgradeRefusesAProtectedRemoval(t *testing.T) {
	h := newSysHost(t)
	h.run(planVec("install-only-upgrade", []string{"curl"}), "Inst curl [1] (2 x [amd64])\nRemv openssh-server [1:9.6]\n1 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	h.installed("curl", "openssh-server")
	_, err := h.planErr("system.packages.upgrade", `{"names":["curl"]}`)
	if errcode.From(err).Code != "system.protected_package" || !strings.Contains(err.Error(), "openssh-server") {
		t.Fatalf("err = %v", err)
	}
}

func TestUpgradeTaskRefusesAStaleSet(t *testing.T) {
	h := newSysHost(t)
	h.run(planVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.run(resimVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\nInst zlib1g [1] (2 x [amd64])\n2 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	_, err := h.applyTask("system.packages.upgrade", `{"all":true}`)
	if errcode.From(err).Code != "system.plan_stale" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(h.calls(), runVec("dist-upgrade", nil)) {
		t.Fatal("the real run happened after a stale re-simulation")
	}
}

func TestPlanRefusesWithinTenSecondsWhenAptIsBusy(t *testing.T) {
	probe := "/usr/bin/apt-get " + strings.Join(platform.SystemAptCheck(), " ")
	if probe != "/usr/bin/apt-get -q -o DPkg::Lock::Timeout=10 check" {
		t.Fatalf("lock probe = %q", probe)
	}
	for _, c := range []struct{ out, code string }{
		{"E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 7 (unattended-upgr)\nE: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?", "system.apt_busy"},
		{"E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem.", "system.dpkg_interrupted"},
	} {
		h := newSysHost(t)
		h.run(probe, c.out, 100)
		_, err := h.planErr("system.packages.upgrade", `{"all":true}`)
		if string(errcode.From(err).Code) != c.code {
			t.Fatalf("%s: err = %v", c.code, err)
		}
		if slices.Contains(h.calls(), planVec("dist-upgrade", nil)) {
			t.Fatalf("%s: simulated after a failed lock probe", c.code)
		}
	}
}

func TestEveryEndOfAnAptTaskDropsTheUpdatesCache(t *testing.T) {
	h := newSysHost(t)
	h.run(planVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.run(resimVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.run(runVec("dist-upgrade", nil), "E: Sub-process /usr/bin/dpkg returned an error code (1)", 100)
	h.run("/usr/bin/apt-get "+strings.Join(platform.SystemAptUpdate(""), " "), "Hit:1 http://archive.ubuntu.com noble InRelease\n", 0)
	h.updatesReadable()
	for _, act := range []struct{ name, input string }{{"system.packages.upgrade", `{"all":true}`}, {"system.index.refresh", `{}`}} {
		_ = h.readResource(t, "updates")
		n := len(h.calls())
		_, _ = h.applyTask(act.name, act.input)
		m := len(h.calls())
		_ = h.readResource(t, "updates")
		if len(h.calls()) == m {
			t.Errorf("%s: updates served from cache after the task ended (calls %d→%d)", act.name, n, m)
		}
	}
}

func TestDriftBetweenPlanAndApplyIsStateChanged(t *testing.T) {
	h := newSysHost(t)
	h.run(planVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	p := h.plan(t, "system.packages.upgrade", `{"all":true}`)
	h.run(planVec("dist-upgrade", nil), "Inst curl [1] (2 x [amd64])\nInst zlib1g [1] (2 x [amd64])\n2 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	if _, err := h.applyPlan(p); errcode.From(err).Code != "plan.state_changed" {
		t.Fatalf("A45: err = %v", err)
	}
}

func TestRefreshRecordsFailedSources(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-get "+strings.Join(platform.SystemAptUpdate(""), " "), "Hit:1 http://archive.ubuntu.com noble InRelease\nErr:2 https://bad.example stable InRelease\n  Could not resolve 'bad.example'\nW: Failed to fetch https://bad.example/dists/stable/InRelease\n", 0)
	out, err := h.applyTask("system.index.refresh", `{}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, `"failedSources":["https://bad.example stable"]`) {
		t.Fatalf("output = %s", out)
	}
	if string(h.written("/var/lib/darkpanel/system/failed-sources")) != "https://bad.example stable\n" {
		t.Fatal("failed sources not recorded for the updates read")
	}
}

// Additions to the plan's cases.

func TestFailedSourcesOfEveryRelease(t *testing.T) {
	for _, rel := range fixtureReleases {
		b, err := os.ReadFile(filepath.Join("testdata", rel, "update-bad-source.txt")) //nolint:gosec // G304: a committed fixture under testdata
		if err != nil {
			t.Fatal(err)
		}
		if got := failedSourcesOf(b); !slices.Equal(got, []string{"http://127.0.0.1:9/debian nowhere"}) {
			t.Errorf("%s: failed sources = %q", rel, got)
		}
	}
	got := failedSourcesOf([]byte("Err:5 http://x.example/ubuntu noble/main amd64 Packages\nErr:6 http://x.example/ubuntu noble/universe amd64 Packages\nErr:7 https://flat.example ./ InRelease\n"))
	if !slices.Equal(got, []string{"http://x.example/ubuntu noble", "https://flat.example ./"}) {
		t.Errorf("failed sources = %q", got)
	}
}

func TestUpgradeTaskRunsTheSelectedSetAndReportsIt(t *testing.T) {
	h := newSysHost(t)
	h.installed("curl", "bash")
	sim := "Inst curl [1.0-1] (2.0 x [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install-only-upgrade", []string{"curl"}), sim, 0)
	h.run(resimVec("install-only-upgrade", []string{"curl"}), sim, 0)
	h.runChanges(runVec("install-only-upgrade", []string{"curl"}), "", 0, "bash", "curl=2.0")
	out, err := h.applyTask("system.packages.upgrade", `{"names":["curl"]}`)
	if err != nil {
		t.Fatal(err)
	}
	if out != `{"installed":[],"upgraded":["curl"],"removed":[]}` {
		t.Fatalf("output = %s", out)
	}
}

func TestUpgradeRefusesAnInputThatIsNotOneOfAllOrNames(t *testing.T) {
	h := newSysHost(t)
	for _, in := range []string{`{}`, `{"all":true,"names":["curl"]}`} {
		if _, err := h.planErr("system.packages.upgrade", in); errcode.From(err).Code != errcode.InvalidInput {
			t.Errorf("%s: err = %v", in, err)
		}
	}
	if _, err := h.planErr("system.packages.upgrade", `{"names":["-o"]}`); errcode.From(err).Code != "system.name_invalid" {
		t.Errorf("an option-shaped name: err = %v", err)
	}
}

func TestPlanSetExemptsTheNamedProtectedPackages(t *testing.T) {
	h := newSysHost(t)
	h.installed("docker-ce", "openssh-server")
	h.run(planVec("remove", []string{"docker-ce"}), "Remv docker-ce [5:27.0]\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	if _, _, err := h.mod().planSet(t.Context(), h.env(), "remove", []string{"docker-ce"}, nil); errcode.From(err).Code != "system.protected_package" {
		t.Fatalf("without the exemption: err = %v", err)
	}
	out, _, err := h.mod().planSet(t.Context(), h.env(), "remove", []string{"docker-ce"}, dockerPackages)
	if err != nil || out.Confirmation != "" || !strings.Contains(PlanView{PlanOutcome: out}.Text(), "- Remove: docker-ce 5:27.0") {
		t.Fatalf("with the exemption: %+v, %v", out, err)
	}
}

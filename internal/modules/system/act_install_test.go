package system

import (
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestInstallShowsRemovalsAndUndoDoesNotReinstallThem(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\nmawk\n", 0)
	h.run(planVec("install", []string{"jq"}), "Inst libjq1 (1.7 x [amd64])\nInst jq (1.7 x [amd64])\nRemv mawk [1.3]\n0 upgraded, 2 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	plan := h.plan(t, "system.packages.install", `{"names":["jq"]}`)
	if !strings.Contains(plan.Text(), "Remove: mawk") || !strings.Contains(plan.Text(), "Undo does not reinstall") || plan.Phrase != "" {
		t.Fatalf("plan:\n%s phrase=%q", plan.Text(), plan.Phrase)
	}
}

func TestInstallUndoRefusesWhenSomethingNowDependsOnIt(t *testing.T) {
	h := newSysHost(t)
	h.recordedInstall([]string{"jq", "libjq1"})
	h.installed("jq", "libjq1", "jq-extras")
	h.run(planVec("remove", []string{"jq", "libjq1"}), "Remv jq-extras [1]\nRemv jq [1.7]\nRemv libjq1 [1.7]\n0 upgraded, 0 newly installed, 3 to remove and 0 not upgraded.\n", 0)
	err := h.undo("system.packages.install")
	if errcode.From(err).Code != "system.removal_refused" || !strings.Contains(err.Error(), "jq-extras") {
		t.Fatalf("err = %v", err)
	}
	if slices.ContainsFunc(h.calls(), func(v string) bool { return strings.Contains(v, " remove ") && !strings.Contains(v, " -s ") }) {
		t.Fatal("undo removed something")
	}
}

func TestRemoveRefusesAProtectedPackageReachedThroughPurg(t *testing.T) {
	h := newSysHost(t)
	h.installed("libpam-foo", "sudo")
	h.run(planVec("purge", []string{"libpam-foo"}), "Purg libpam-foo [1]\nPurg sudo [1.9]\n0 upgraded, 0 newly installed, 2 to remove and 0 not upgraded.\n", 0)
	_, err := h.planErr("system.packages.remove", `{"names":["libpam-foo"],"purge":true}`)
	if errcode.From(err).Code != "system.protected_package" || !strings.Contains(err.Error(), "sudo") {
		t.Fatalf("err = %v", err)
	}
}

func TestRepairPlansFromAuditAndStopsOnAProtectedRemoval(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/dpkg --audit", "The following packages are only half configured:\n curl\n", 0)
	plan := h.plan(t, "system.packages.repair", `{}`)
	if !strings.Contains(plan.Text(), "curl") || slices.ContainsFunc(h.calls(), func(v string) bool { return strings.Contains(v, "-f install") }) {
		t.Fatalf("repair plan simulated -f install or misses curl:\n%s", plan.Text())
	}
	h.run("/usr/bin/dpkg --force-confdef --force-confold --configure -a", "", 0)
	h.run(resimVec("fix-install", nil), "Remv openssh-server [1:9.6]\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	h.installed("openssh-server")
	_, err := h.applyTask("system.packages.repair", `{}`)
	if errcode.From(err).Code != "system.protected_package" {
		t.Fatalf("err = %v", err)
	}
	if slices.Contains(h.calls(), runVec("fix-install", nil)) {
		t.Fatal("ran -f install after a protected removal")
	}
}

// Additions to the plan's cases.

func TestInstallJournalsTheSetAndUndoRemovesExactlyIt(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\nlibjq1\n", 0)
	sim := "Inst libjq1 (1.7 x [amd64])\nInst jq (1.7 x [amd64])\n0 upgraded, 2 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install", []string{"jq"}), sim, 0)
	h.run(resimVec("install", []string{"jq"}), sim, 0)
	h.runChanges(runVec("install", []string{"jq"}), "", 0, "bash", "jq=1.7", "libjq1=1.7")
	out, err := h.applyTask("system.packages.install", `{"names":["jq"]}`)
	if err != nil || out != `{"installed":["jq","libjq1"],"upgraded":[],"removed":[]}` {
		t.Fatalf("install = %s, %v", out, err)
	}
	effects := h.journal.of(h.last["system.packages.install"])
	if len(effects) != 1 || effects[0].State != core.EffectMade || effects[0].Resource != "system.packages:jq,libjq1" {
		t.Fatalf("effects = %+v", effects)
	}
	rm := "Remv jq [1.7]\nRemv libjq1 [1.7]\n0 upgraded, 0 newly installed, 2 to remove and 0 not upgraded.\n"
	h.run(planVec("remove", []string{"jq", "libjq1"}), rm, 0)
	h.run(resimVec("remove", []string{"jq", "libjq1"}), rm, 0)
	h.runChanges(runVec("remove", []string{"jq", "libjq1"}), "", 0, "bash")
	if err := h.undo("system.packages.install"); err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(h.calls(), runVec("remove", []string{"jq", "libjq1"})) {
		t.Fatal("undo did not remove the installed set")
	}
}

func TestInstallUndoSkipsRecordedPackagesAlreadyGone(t *testing.T) {
	h := newSysHost(t)
	h.recordedInstall([]string{"jq", "libjq1"})
	h.installed("libjq1")
	rm := "Remv libjq1 [1.7]\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n"
	h.run(planVec("remove", []string{"libjq1"}), rm, 0)
	h.run(resimVec("remove", []string{"libjq1"}), rm, 0)
	h.runChanges(runVec("remove", []string{"libjq1"}), "", 0, "bash")
	if err := h.undo("system.packages.install"); err != nil {
		t.Fatal(err)
	}
	h.installed("bash")
	if err := h.undo("system.packages.install"); err != nil {
		t.Fatalf("undo with nothing left: %v", err)
	}
}

func TestInstallStaleSetSkipsItsEffect(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	h.run(planVec("install", []string{"jq"}), "Inst jq (1.7 x [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.run(resimVec("install", []string{"jq"}), "Inst jq (1.8 x [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	if _, err := h.applyTask("system.packages.install", `{"names":["jq"]}`); errcode.From(err).Code != errcode.SystemPlanStale || !core.IsDefinite(err) {
		t.Fatalf("err = %v", err)
	}
	if effects := h.journal.of("c1"); len(effects) != 1 || effects[0].State != core.EffectSkipped {
		t.Fatalf("effects = %+v", effects)
	}
}

func TestRemovePurgePlanAsksForTheHostName(t *testing.T) {
	h := newSysHost(t)
	h.installed("libpam-foo")
	h.run(planVec("purge", []string{"libpam-foo"}), "Purg libpam-foo [1]\n0 upgraded, 0 newly installed, 1 to remove and 0 not upgraded.\n", 0)
	p := h.plan(t, "system.packages.remove", `{"names":["libpam-foo"],"purge":true}`)
	if p.Phrase != h.hostName() || !strings.Contains(p.Text(), "- Remove: libpam-foo 1, and purge its configuration") {
		t.Fatalf("plan:\n%s phrase=%q", p.Text(), p.Phrase)
	}
	if _, err := h.planErr("system.packages.remove", `{"names":["jq"]}`); errcode.From(err).Code != errcode.SystemNameInvalid {
		t.Fatalf("a package not installed: err = %v", err)
	}
}

func TestRepairRunsTheFixWhenNothingProtectedGoes(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/dpkg --force-confdef --force-confold --configure -a", "", 0)
	h.run(resimVec("fix-install", nil), "Inst libfoo (1 x [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	h.runChanges(runVec("fix-install", nil), "", 0, "bash", "libfoo")
	out, err := h.applyTask("system.packages.repair", `{}`)
	if err != nil || out != `{"installed":["libfoo"],"upgraded":[],"removed":[]}` {
		t.Fatalf("repair = %s, %v", out, err)
	}
	p := h.plan(t, "system.packages.repair", `{}`)
	if p.Phrase != h.hostName() || !strings.Contains(p.Text(), "Dependencies are fixed afterwards") {
		t.Fatalf("plan:\n%s phrase=%q", p.Text(), p.Phrase)
	}
}

func TestInstallEffectNamesAnotherArchitecturesPackageByName(t *testing.T) {
	h := newSysHost(t)
	h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	sim := "Inst libfoo:i386 (1 x [i386])\nInst jq (1.7 x [amd64])\n0 upgraded, 2 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install", []string{"jq"}), sim, 0)
	h.run(resimVec("install", []string{"jq"}), sim, 0)
	h.run(runVec("install", []string{"jq"}), "E: Sub-process /usr/bin/dpkg returned an error code (1)", 100)
	if _, err := h.applyTask("system.packages.install", `{"names":["jq"]}`); err == nil {
		t.Fatal("a failed run succeeded")
	}
	effects := h.journal.of("c1")
	if len(effects) != 1 || effects[0].Resource != "system.packages:jq,libfoo" {
		t.Fatalf("effects = %+v", effects)
	}
	a, err := h.action("system.packages.install")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := a.Handler.Observe(t.Context(), h.env(), effects[0]); err != nil {
		t.Fatalf("observe the intended effect: %v", err)
	}
}

func TestInstallLeavesAnotherArchitecturesCopyOfAnInstalledPackageOutOfItsUndo(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "libfoo")
	h.run("/usr/bin/apt-cache pkgnames", "jq\n", 0)
	sim := "Inst libfoo:i386 (1 x [i386])\nInst jq (1.7 x [amd64])\n0 upgraded, 2 newly installed, 0 to remove and 0 not upgraded.\n"
	h.run(planVec("install", []string{"jq"}), sim, 0)
	h.run(resimVec("install", []string{"jq"}), sim, 0)
	// A failed run leaves the effect intended with the planned set, which the engine settles and may undo.
	h.run(runVec("install", []string{"jq"}), "E: Sub-process /usr/bin/dpkg returned an error code (1)", 100)
	if _, err := h.applyTask("system.packages.install", `{"names":["jq"]}`); err == nil {
		t.Fatal("a failed run succeeded")
	}
	if effects := h.journal.of("c1"); len(effects) != 1 || effects[0].Resource != "system.packages:jq" {
		t.Fatalf("effects = %+v", effects)
	}
}

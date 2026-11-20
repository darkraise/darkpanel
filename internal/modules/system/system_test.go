package system

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform"
)

// preflight runs the manifest's checks; "" when the module is available.
func preflight(h *sysHost) string {
	for _, c := range h.mod().Manifest().Preflight {
		if err := c.Run(context.Background(), h.env()); err != nil {
			return err.Error()
		}
	}
	return ""
}

func TestModuleIsAvailableOnEverySupportedRelease(t *testing.T) {
	for _, osr := range []struct{ id, codename string }{{"ubuntu", "jammy"}, {"ubuntu", "noble"}, {"ubuntu", "resolute"}, {"debian", "bookworm"}, {"debian", "trixie"}} {
		for _, arch := range []string{"amd64", "arm64"} {
			h := newSysHost(t)
			h.osRelease(osr.id, osr.codename, arch) // no docker, no unattended-upgrades
			if reason := preflight(h); reason != "" {
				t.Errorf("%s %s %s: unavailable: %s", osr.id, osr.codename, arch, reason)
			}
		}
	}
	for _, bad := range []struct{ id, codename, arch string }{{"ubuntu", "focal", "amd64"}, {"fedora", "41", "amd64"}, {"debian", "trixie", "riscv64"}} {
		h := newSysHost(t)
		h.osRelease(bad.id, bad.codename, bad.arch)
		if preflight(h) == "" {
			t.Errorf("%+v: available", bad)
		}
	}
	m := Module().Manifest()
	if !m.DefaultEnabled || m.ID != ID || ID != "system" {
		t.Errorf("manifest %s DefaultEnabled=%v, want system enabled by default", m.ID, m.DefaultEnabled)
	}
}

func TestAptRunMapsFailures(t *testing.T) {
	h := newSysHost(t)
	vec := "/usr/bin/apt-get " + strings.Join(platform.SystemAptRun("install", []string{"jq"}), " ")
	h.run(vec, "E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 42", 100)
	_, _, err := aptRun(context.Background(), h.env(), "install", []string{"jq"})
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != "system.apt_busy" {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(ce.Detail, "held by process 42") {
		t.Fatalf("detail lacks apt's last lines: %q", ce.Detail)
	}
}

func TestAptRunReportsTheInstalledDifference(t *testing.T) {
	h := newSysHost(t)
	h.installed("bash", "curl", "oldlib")
	h.runChanges(runVec("install", []string{"jq"}), "Setting up jq ...\n", 0, "bash", "curl=2.0-1", "jq", "libjq1")
	applied, out, err := aptRun(context.Background(), h.env(), "install", []string{"jq"})
	if err != nil || !strings.Contains(string(out), "Setting up jq") {
		t.Fatalf("aptRun: %v, %q", err, out)
	}
	if !slices.Equal(applied.Installed, []string{"jq", "libjq1"}) || !slices.Equal(applied.Upgraded, []string{"curl"}) || !slices.Equal(applied.Removed, []string{"oldlib"}) {
		t.Fatalf("applied = %+v", applied)
	}
}

func TestAptPlanProbesTheLockFirst(t *testing.T) {
	sim := "Inst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\n0 upgraded, 1 newly installed, 0 to remove and 0 not upgraded.\n"
	h := newSysHost(t)
	h.run(planVec("install", []string{"jq"}), sim, 0)
	got, err := aptPlan(context.Background(), h.env(), "install", []string{"jq"}, false)
	if err != nil || !slices.Equal(got.Installs(), []string{"jq"}) {
		t.Fatalf("aptPlan = %+v, %v", got, err)
	}
	probe := "/usr/bin/apt-get " + strings.Join(platform.SystemAptCheck(), " ")
	if calls := h.calls(); len(calls) != 2 || calls[0] != probe {
		t.Fatalf("calls = %q, want the probe then the simulation", calls)
	}

	h = newSysHost(t)
	h.run(probe, "E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem.\n", 100)
	_, err = aptPlan(context.Background(), h.env(), "install", []string{"jq"}, false)
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != errcode.SystemDpkgInterrupted || len(h.calls()) != 1 {
		t.Fatalf("interrupted dpkg: err = %v, calls %q; want system.dpkg_interrupted and no simulation", err, h.calls())
	}
}

func TestAptSimulationsParseOrRefuse(t *testing.T) {
	h := newSysHost(t)
	h.run(resimVec("fix-install", nil), "Frob x\n0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n", 0)
	_, err := aptResim(context.Background(), h.env(), "fix-install", nil)
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != errcode.SystemSimUnparsed || !errors.Is(err, ErrSimUnknownLine) {
		t.Fatalf("resim: err = %v", err)
	}
	h.updatesReadable()
	if sim, err := aptRead(context.Background(), h.env(), "dist-upgrade", nil, nil); err != nil || len(sim.Ops) != 0 {
		t.Fatalf("read: %+v, %v", sim, err)
	}
	if slices.Contains(h.calls(), "/usr/bin/apt-get "+strings.Join(platform.SystemAptCheck(), " ")) {
		t.Error("a resimulation or a read ran the lock probe")
	}
}

package system

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestParseSimFixtures(t *testing.T) {
	for _, rel := range fixtureReleases {
		for _, name := range []string{"install", "remove", "purge", "dist-upgrade", "fix-install", "summary-only"} {
			b, _ := os.ReadFile(filepath.Join("testdata", rel, name+".txt")) //nolint:gosec // G304: a committed fixture under testdata
			sim, err := ParseSim(b)
			if err != nil {
				t.Errorf("%s/%s: %v", rel, name, err)
				continue
			}
			if name == "purge" && len(sim.Removals()) == 0 {
				t.Errorf("%s/purge: Purg lines not counted as removals", rel)
			}
		}
	}
}

func TestParseSimRefusals(t *testing.T) {
	cases := map[string]error{
		"Inst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\nFrob x\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n": ErrSimUnknownLine,
		"Inst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\n0 upgraded, 2 newly installed, 0 to remove and 0 not upgraded.\n":         ErrSimCountMismatch,
		"Inst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\n":                                                                         ErrSimCountMismatch,
	}
	for in, want := range cases {
		if _, err := ParseSim([]byte(in)); !errors.Is(err, want) {
			t.Errorf("%q: err = %v, want %v", in, err, want)
		}
	}
	sim, err := ParseSim([]byte("Inst libjq1 [1.6-2] (1.7.1-3 Ubuntu:24.04/noble [amd64])\nInst jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\nRemv oldpkg [2.0]\nPurg gone [1.0]\nConf jq (1.7.1-3 Ubuntu:24.04/noble [amd64])\n1 upgraded, 1 newly installed, 2 to remove and 0 not upgraded.\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(sim.Upgrades(), []string{"libjq1"}) || !slices.Equal(sim.Installs(), []string{"jq"}) || !slices.Equal(sim.Removals(), []string{"gone", "oldpkg"}) {
		t.Fatalf("sim = %+v", sim)
	}
}

func TestClassifyApt(t *testing.T) {
	cases := map[string]errcode.Code{
		"E: Could not get lock /var/lib/dpkg/lock-frontend. It is held by process 42 (apt-get)":        "system.apt_busy",
		"E: The repository 'https://x stable InRelease' is not signed.":                                "system.repo_signature",
		"W: GPG error: https://x stable InRelease: The following signatures couldn't be verified":      "system.repo_signature",
		"E: You don't have enough free space in /var/cache/apt/archives/.":                             "system.disk_full",
		"E: Unable to correct problems, you have held broken packages.":                                "system.broken_packages",
		"E: dpkg was interrupted, you must manually run 'dpkg --configure -a' to correct the problem.": "system.dpkg_interrupted",
	}
	for out, want := range cases {
		if got := classifyApt([]byte(out)); got != want {
			t.Errorf("%q → %s, want %s", out, got, want)
		}
	}
	lines := strings.Repeat("noise\n", 60) + "Errors were encountered while processing:\n foo-bar\n"
	pkg, tail := failureDetail([]byte(lines))
	if pkg != "foo-bar" || len(tail) != 40 || tail[39] != " foo-bar" {
		t.Fatalf("pkg=%q len=%d last=%q", pkg, len(tail), tail[len(tail)-1])
	}
}

func TestClassifyAptFixtures(t *testing.T) {
	for _, rel := range fixtureReleases {
		for name, want := range map[string]errcode.Code{"lock-held": "system.apt_busy", "dpkg-interrupted": "system.dpkg_interrupted"} {
			b, err := os.ReadFile(filepath.Join("testdata", rel, name+".txt")) //nolint:gosec // G304: a committed fixture under testdata
			if err != nil {
				t.Fatal(err)
			}
			if got := classifyApt(b); got != want {
				t.Errorf("%s/%s → %s, want %s", rel, name, got, want)
			}
		}
	}
	for out, want := range map[string]errcode.Code{
		"E: Unable to acquire the dpkg frontend lock (/var/lib/dpkg/lock-frontend), is another process using it?": "system.apt_busy",
		"E: The following signatures were invalid: EXPKEYSIG 1234":                                                "system.repo_signature",
		"W: GPG error: https://x stable InRelease: NO_PUBKEY 7EA0A9C3F273FCD8":                                    "system.repo_signature",
		"dpkg: error processing archive x.deb: No space left on device":                                           "system.disk_full",
		"The following packages have unmet dependencies:\n jq : Depends: libjq1 but it is not installable":        "system.broken_packages",
		"E: Sub-process /usr/bin/dpkg returned an error code (1)":                                                 "system.apt_failed",
		"": "system.apt_failed",
	} {
		if got := classifyApt([]byte(out)); got != want {
			t.Errorf("%q → %s, want %s", out, got, want)
		}
	}
}

func TestParseSimRepeatedSummary(t *testing.T) {
	two := "0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n0 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"
	if _, err := ParseSim([]byte(two)); !errors.Is(err, ErrSimCountMismatch) {
		t.Errorf("two summaries: err = %v", err)
	}
	b, _ := os.ReadFile(filepath.Join("testdata", "debian-13", "install.txt"))
	sim, err := ParseSim(b)
	if err != nil || !slices.Equal(sim.Installs(), []string{"jq", "libjq1", "libonig5"}) || sim.Installed != 3 || len(sim.Upgrades()) != 0 {
		t.Errorf("debian-13 install: %+v, %v", sim, err)
	}
}

func TestFailureDetailEPackage(t *testing.T) {
	pkg, tail := failureDetail([]byte("Reading package lists...\nE: Unable to locate package nosuchpkg\n"))
	if pkg != "nosuchpkg" || len(tail) != 2 || !strings.HasPrefix(tail[1], "E: ") {
		t.Errorf("pkg=%q tail=%q", pkg, tail)
	}
}

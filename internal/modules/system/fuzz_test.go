package system

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fixtureSeeds adds every captured apt and dpkg output under testdata.
func fixtureSeeds(f *testing.F) {
	f.Helper()
	err := filepath.WalkDir("testdata", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(path) //nolint:gosec // G304: a fixture under testdata
		if err != nil {
			return err
		}
		f.Add(b)
		return nil
	})
	if err != nil {
		f.Fatal(err)
	}
}

// FuzzParseSim checks that an accepted simulation's action lines are known
// kinds with a name, and agree with its summary counts.
func FuzzParseSim(f *testing.F) {
	fixtureSeeds(f)
	f.Add([]byte("Inst a [1] (2 Ubuntu:22.04/jammy-security [amd64])\n1 upgraded, 0 newly installed, 0 to remove and 0 not upgraded.\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		sim, err := ParseSim(in)
		if err != nil {
			return
		}
		for _, op := range sim.Ops {
			if !slices.Contains([]string{"Inst", "Conf", "Remv", "Purg"}, op.Kind) || op.Name == "" || strings.Contains(op.Name, " ") {
				t.Errorf("ParseSim(%q) accepted op %+v", in, op)
			}
		}
		if len(sim.Upgrades()) != sim.Upgraded || len(sim.Installs()) != sim.Installed || len(sim.Removals()) != sim.Removed {
			t.Errorf("ParseSim(%q) = %+v, whose ops disagree with its counts", in, sim)
		}
	})
}

// FuzzAptOutput checks the readers of apt-get update and failed-run output:
// each failed source once as "<uri> <suite>", at most 40 tail lines, a known
// code, and a strict refresh that refuses exactly the output with an Err:
// line or a W:/E: line about a fetch or signature.
func FuzzAptOutput(f *testing.F) {
	fixtureSeeds(f)
	f.Add([]byte("Err:1 http://a b/c Release\nW: Failed to fetch x\n"))
	f.Add([]byte("Err:2 https://download.docker.com/linux/ubuntu jammy InRelease\n"))
	f.Add([]byte("Errors were encountered while processing:\n pkg\n"))
	known := []errcode.Code{errcode.SystemAptBusy, errcode.SystemDpkgInterrupted, errcode.SystemDiskFull,
		errcode.SystemRepoSignature, errcode.SystemBrokenPackages, errcode.SystemAptFailed}
	f.Fuzz(func(t *testing.T, in []byte) {
		failed := failedSourcesOf(in)
		for i, s := range failed {
			if len(strings.Fields(s)) != 2 || slices.Contains(failed[:i], s) {
				t.Errorf("failedSourcesOf(%q) = %q", in, failed)
			}
		}
		pkg, tail := failureDetail(in)
		if len(tail) > 40 || strings.Contains(pkg, "\n") {
			t.Errorf("failureDetail(%q) = %q, %d lines", in, pkg, len(tail))
		}
		if c := classifyApt(in); !slices.Contains(known, c) {
			t.Errorf("classifyApt(%q) = %q", in, c)
		}
		bad := false
		for line := range strings.Lines(string(in)) {
			l := strings.TrimRight(line, "\n")
			bad = bad || strings.HasPrefix(l, "Err:") || ((strings.HasPrefix(l, "W:") || strings.HasPrefix(l, "E:")) && fetchOrSignature(l))
		}
		if err := strictRefresh(in, nil); (err != nil) != bad {
			t.Errorf("strictRefresh(%q, nil) = %v, want a refusal %v", in, err, bad)
		}
	})
}

// FuzzDebVersionCompare checks that the Debian version order is a total
// order on the inputs: reflexive, antisymmetric, and -1, 0 or 1.
func FuzzDebVersionCompare(f *testing.F) {
	f.Add("1:2.3-4", "2.3-4")
	f.Add("1.0~rc1", "1.0")
	f.Add("5.15.0-91.101", "5.15.0-100.110")
	f.Fuzz(func(t *testing.T, a, b string) {
		c := debVersionCompare(a, b)
		if c < -1 || c > 1 || debVersionCompare(b, a) != -c || debVersionCompare(a, a) != 0 {
			t.Errorf("debVersionCompare(%q, %q) = %d, reversed %d", a, b, c, debVersionCompare(b, a))
		}
		_, rest, ok := strings.Cut(a, ":")
		if !ok {
			rest = a
		}
		joined := ""
		if _, up, rev := splitDebVersion(a); strings.Contains(rest, "-") {
			joined = up + "-" + rev
			if strings.Contains(rev, "-") {
				t.Errorf("splitDebVersion(%q) revision %q holds a hyphen", a, rev)
			}
		} else {
			joined = up + rev
		}
		if joined != rest {
			t.Errorf("splitDebVersion(%q) does not rejoin to %q", a, rest)
		}
	})
}

// FuzzParseDpkgQuery checks that an accepted listing has one package per line.
func FuzzParseDpkgQuery(f *testing.F) {
	f.Add([]byte("bash\t5.1-6\tamd64\tinstall ok installed\tyes\trequired\tGNU Bourne Again SHell\n"))
	f.Add([]byte("a\tb\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		pkgs, err := parseDpkgQuery(in)
		if err != nil {
			return
		}
		lines := 0
		for range strings.Lines(string(in)) {
			lines++
		}
		if len(pkgs) != lines {
			t.Errorf("parseDpkgQuery(%q) = %d packages, want %d", in, len(pkgs), lines)
		}
	})
}

// FuzzParseAptPolicy checks that every section is named and "(none)" never
// survives as a version.
func FuzzParseAptPolicy(f *testing.F) {
	fixtureSeeds(f)
	f.Add([]byte("docker.io:\n  Installed: (none)\n  Candidate: 24.0.7-0ubuntu2\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		for name, e := range parseAptPolicy(in) {
			if name == "" || e.installed == "(none)" || e.candidate == "(none)" {
				t.Errorf("parseAptPolicy(%q)[%q] = %+v", in, name, e)
			}
		}
	})
}

// FuzzHostFiles checks the readers of the host files the module reads.
func FuzzHostFiles(f *testing.F) {
	fixtureSeeds(f)
	f.Add([]byte("ID=debian\nVERSION_CODENAME=\"bookworm\"\n"))
	f.Add([]byte("122610.97 1469212.45\n"))
	f.Add([]byte("Linux version 6.1.0-18-amd64 (gcc-12 (Debian 12.2.0-14) 12.2.0) #1 SMP Debian 6.1.76-1 (2024-02-01)\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		info, err := readOSRelease(func(string) ([]byte, error) { return in, nil })
		if err != nil || strings.ContainsAny(info.ID+info.Codename, "\n") {
			t.Errorf("readOSRelease(%q) = %+v, %v", in, info, err)
		}
		if s, err := parseUptime(in); err == nil && s < 0 {
			t.Errorf("parseUptime(%q) = %d", in, s)
		}
		if p := parseRebootPkgs(in); !slices.IsSorted(p) || len(slices.Compact(slices.Clone(p))) != len(p) || slices.Contains(p, "") {
			t.Errorf("parseRebootPkgs(%q) = %q", in, p)
		}
		if v := debianRunningKernel(in); v != "" && (!strings.Contains(string(in), "Debian "+v) || strings.ContainsAny(v, " \t\n")) {
			t.Errorf("debianRunningKernel(%q) = %q", in, v)
		}
		if mentionsDockerRepo(in) && !strings.Contains(string(in), dockerRepoHost) {
			t.Errorf("mentionsDockerRepo(%q) = true", in)
		}
	})
}

// FuzzCommandLists checks the readers of the line-per-item command outputs.
func FuzzCommandLists(f *testing.F) {
	f.Add([]byte("docker-ce\nlinux-image-generic\n"))
	f.Add([]byte("NEEDRESTART-VER: 3.6\nNEEDRESTART-SVC: ssh.service\nNEEDRESTART-SVC: cron.service\n"))
	f.Add([]byte("APT::Periodic::Unattended-Upgrade \"1\";\n"))
	f.Add([]byte("The following packages are only half configured:\n bash\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		for _, n := range parseShowhold(in) {
			if !namePattern.MatchString(n) {
				t.Errorf("parseShowhold(%q) accepted %q", in, n)
			}
		}
		if s := parseNeedrestart(in); !slices.IsSorted(s) || len(slices.Compact(slices.Clone(s))) != len(s) || slices.Contains(s, "") {
			t.Errorf("parseNeedrestart(%q) = %q", in, s)
		}
		for n := range parsePkgnames(in) {
			if n == "" || n != strings.TrimSpace(n) {
				t.Errorf("parsePkgnames(%q) has %q", in, n)
			}
		}
		for k := range parseAptConfigDump(in) {
			if strings.ContainsAny(k, " \n") {
				t.Errorf("parseAptConfigDump(%q) has key %q", in, k)
			}
		}
		for _, l := range auditLines(in) {
			if l == "" || l != strings.TrimSpace(l) {
				t.Errorf("auditLines(%q) has %q", in, l)
			}
		}
	})
}

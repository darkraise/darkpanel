package docs_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// pinTestBinary compiles this package's tests once per test, for running the
// pin test against a copy of the docs.
func pinTestBinary(t *testing.T, root string) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "docs.test")
	cmd := exec.Command("go", "test", "-c", "-o", bin, "./internal/docs") //nolint:gosec // G204: compiles this package into a temporary path
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go test -c: %v\n%s", err, out)
	}
	return bin
}

// runPinTest runs TestReadmeInstallNamesNewestRelease inside the copy at
// repo, which holds go.mod, CHANGELOG.md, README.md and docs/user/install.md.
func runPinTest(t *testing.T, bin, repo string) (string, error) {
	t.Helper()
	dir := filepath.Join(repo, "internal", "docs")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(bin, "-test.run", "^TestReadmeInstallNamesNewestRelease$", "-test.count=1")
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// pinnedRelease is the release the docs' install commands name today.
func pinnedRelease(t *testing.T) string {
	t.Helper()
	pins := rmPinned.FindAllStringSubmatch(dcRead(t, "README.md")+dcRead(t, "docs/user/install.md"), -1)
	if len(pins) == 0 {
		t.Skip("the docs pin no release, so there is no pin to move")
	}
	return pins[0][1]
}

func writeDocCopy(t *testing.T, root, newest string) string {
	t.Helper()
	repo := t.TempDir()
	for _, rel := range []string{"go.mod", "README.md", "docs/user/install.md"} {
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(rel))) //nolint:gosec // G304: a path under the repository root
		if err != nil {
			t.Fatal(err)
		}
		dst := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, raw, 0o600); err != nil { //nolint:gosec // G703: a path under the test's temporary directory
			t.Fatal(err)
		}
	}
	changelog := "# Changelog\n\n## " + newest + " - 2026-11-18\n\n### Fixes\n\n- repair a thing\n"
	if err := os.WriteFile(filepath.Join(repo, "CHANGELOG.md"), []byte(changelog), 0o600); err != nil {
		t.Fatal(err)
	}
	return repo
}

func readCopy(t *testing.T, repo, rel string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(repo, filepath.FromSlash(rel))) //nolint:gosec // G304: a path under a temporary copy
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func pinDocs(t *testing.T, root, repo, version string) {
	t.Helper()
	cmd := exec.Command("bash", filepath.Join(root, "release", "pin-docs.sh"), version) //nolint:gosec // G204: the repository's own script and a test version
	cmd.Env = append(os.Environ(), "DOCS_ROOT="+repo)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("pin-docs.sh %s: %v\n%s", version, err, out)
	}
}

func TestA5_ReleasePrepMovesThePinsOfAPreRelease(t *testing.T) {
	root := dcRoot(t)
	bin := pinTestBinary(t, root)
	old := pinnedRelease(t)

	for _, next := range []string{"7.0.0-rc.3", "7.0.0-rc.10", "7.1.0-beta.1"} {
		t.Run(next, func(t *testing.T) {
			repo := writeDocCopy(t, root, next)
			beforeReadme := readCopy(t, repo, "README.md")
			beforeInstall := readCopy(t, repo, "docs/user/install.md")
			if out, err := runPinTest(t, bin, repo); err == nil {
				t.Fatalf("the pin test passes before the pins move, so the check is vacuous:\n%s", out)
			}

			pinDocs(t, root, repo, next)
			readme := readCopy(t, repo, "README.md")
			install := readCopy(t, repo, "docs/user/install.md")

			for name, text := range map[string]string{"README.md": readme, "docs/user/install.md": install} {
				if strings.Contains(text, old) {
					t.Errorf("%s still names %s", name, old)
				}
				if !strings.Contains(text, "releases/download/v"+next+"/install.sh") {
					t.Errorf("%s does not install v%s from its release", name, next)
				}
			}
			if !strings.Contains(install, "--version "+next+" ") {
				t.Errorf("docs/user/install.md does not pass --version %s", next)
			}
			if !strings.Contains(install, "--version X.Y.Z") {
				t.Errorf("the generic --version X.Y.Z text of docs/user/install.md was changed")
			}
			if strings.Count(beforeInstall, "--version X.Y.Z") != strings.Count(install, "--version X.Y.Z") {
				t.Errorf("the number of --version X.Y.Z mentions changed")
			}
			if strings.Count(beforeReadme, "\n") != strings.Count(readme, "\n") || strings.Count(beforeInstall, "\n") != strings.Count(install, "\n") {
				t.Errorf("pinning changed the number of lines")
			}
			if out, err := runPinTest(t, bin, repo); err != nil {
				t.Errorf("the pin test fails after the pins move: %v\n%s", err, out)
			}
		})
	}
}

func TestA5_ReleasePrepLeavesTheDocsAloneForAStableVersion(t *testing.T) {
	root := dcRoot(t)
	for _, next := range []string{"1.0.0", "1.2.4", "2.0.0"} {
		t.Run(next, func(t *testing.T) {
			repo := writeDocCopy(t, root, next)
			beforeReadme := readCopy(t, repo, "README.md")
			beforeInstall := readCopy(t, repo, "docs/user/install.md")
			pinDocs(t, root, repo, next)
			if got := readCopy(t, repo, "README.md"); got != beforeReadme {
				t.Errorf("README.md changed for stable %s", next)
			}
			if got := readCopy(t, repo, "docs/user/install.md"); got != beforeInstall {
				t.Errorf("docs/user/install.md changed for stable %s", next)
			}
		})
	}
}

func TestA5_MakeReleasePrepMovesThePinsBeforeFreezingSnapshots(t *testing.T) {
	makeBin, err := exec.LookPath("make")
	if err != nil {
		t.Skip("make is not installed")
	}
	cmd := exec.Command(makeBin, "-n", "release-prep", "VERSION=7.0.0-rc.3") //nolint:gosec // G204: make from PATH, a dry run of the repository's Makefile
	cmd.Dir = dcRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("make -n release-prep: %v\n%s", err, out)
	}
	plan := string(out)
	pin := strings.Index(plan, "release/pin-docs.sh 7.0.0-rc.3")
	freeze := strings.Index(plan, "release/snapshots.sh copy v7.0.0-rc.3")
	if pin < 0 || freeze < 0 {
		t.Fatalf("release-prep does not run both the pin move and the snapshot copy:\n%s", plan)
	}
	if pin > freeze {
		t.Errorf("release-prep freezes the snapshots before it validates the pins:\n%s", plan)
	}
}

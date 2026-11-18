package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const dedupeChangelog = "# Changelog\n\nAll notable changes to Darkpanel.\n\n" +
	"## 1.0.0-rc.3 - 2026-11-18\n\n### Fixes\n\n- **install:** quote the token path\n"

// TestA7_CheckPrepAcceptsAReleaseWithoutSnapshots drives the built
// check-prep command: a release that changed no schema writes no snapshot
// directory, so its path list holds the changelog alone or with the pins.
func TestA7_CheckPrepAcceptsAReleaseWithoutSnapshots(t *testing.T) {
	dir := t.TempDir()
	bin := filepath.Join(dir, "releasetool")
	build := exec.Command("go", "build", "-o", bin, ".") //nolint:gosec // G204: builds this package into a temporary path
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	changelog := filepath.Join(dir, "CHANGELOG.md")
	if err := os.WriteFile(changelog, []byte(dedupeChangelog), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name, paths string
		want        bool
	}{
		{"changelog alone", "CHANGELOG.md\n", true},
		{"changelog and the two docs pins", "CHANGELOG.md\nREADME.md\ndocs/user/install.md\n", true},
		{"changelog and one changed snapshot", "CHANGELOG.md\nschemas/released/v1.0.0-rc.3/core.host.list/v1.json\n", true},
		{"docs pins without the changelog", "README.md\ndocs/user/install.md\n", false},
		{"changelog and a source file", "CHANGELOG.md\ninternal/core/x.go\n", false},
	} {
		cmd := exec.Command(bin, "check-prep", "1.0.0-rc.3", changelog) //nolint:gosec // G204: the binary built above
		cmd.Stdin = strings.NewReader(c.paths)
		var out bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &out
		err := cmd.Run()
		if c.want && err != nil {
			t.Errorf("%s: check-prep refused: %v\n%s", c.name, err, out.String())
		}
		if !c.want && err == nil {
			t.Errorf("%s: check-prep accepted it", c.name)
		}
	}
}

// jobBlock returns the text of the top-level job named job in a workflow
// file: from its "  <job>:" line to the next two-space-indented key.
func jobBlock(t *testing.T, file, job string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", ".github", "workflows", file)) //nolint:gosec // G304: a workflow file of this repository
	if err != nil {
		t.Fatal(err)
	}
	start := regexp.MustCompile(`(?m)^  ` + regexp.QuoteMeta(job) + `:\s*$`).FindIndex(data)
	if start == nil {
		t.Fatalf("%s has no job %s", file, job)
	}
	rest := string(data[start[1]:])
	if next := regexp.MustCompile(`(?m)^  [A-Za-z0-9_-]+:\s*$`).FindStringIndex(rest); next != nil {
		rest = rest[:next[0]]
	}
	return rest
}

func TestA11_WorkflowsRunRetainedAgainstThePreviousTag(t *testing.T) {
	for _, c := range []struct{ file, job string }{
		{"ci.yml", "release-scripts"},
		{"release.yml", "verify"},
	} {
		block := jobBlock(t, c.file, c.job)
		if !strings.Contains(block, "fetch-depth: 0") {
			t.Errorf("%s job %s does not check out with fetch-depth: 0", c.file, c.job)
		}
		if !strings.Contains(block, "bash release/snapshots.sh retained") {
			t.Errorf("%s job %s does not run snapshots.sh retained", c.file, c.job)
		}
		if !strings.Contains(block, `git describe --tags --abbrev=0 --match 'v*' "$GITHUB_SHA^"`) {
			t.Errorf("%s job %s does not take the previous tag of the first parent as the reference", c.file, c.job)
		}
	}
}

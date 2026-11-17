package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
)

const (
	arWorkflowYML = "../../.github/workflows/auto-release.yml"
	arHarnessYML  = "../../.github/workflows/harness.yml"
	arHarnessIf   = "    if: ${{ !(github.event_name == 'push' && startsWith(github.event.head_commit.message, 'chore(release): prepare ') && github.event.head_commit.committer.email == 'release-bot@darkpanel.invalid') }}\n"
)

var arPinned = regexp.MustCompile(`^\s*(?:-\s+)?uses:\s+[^@\s]+@[0-9a-f]{40}\s+#\s+v\S+$`)

// arCheck holds the security properties of auto-release.yml: the bot key and
// its environment only in the push job, hosted runners, a read-only token
// set once, SHA-pinned actions, and ignored runs kept out of the shared
// concurrency group.
func arCheck(yml string) error {
	prep, push, ok := strings.Cut(yml, "\n  push:\n")
	if !ok || !strings.Contains(prep, "\n  prepare:\n") {
		return errors.New("want a prepare job followed by a push job")
	}
	if strings.Contains(prep, "RELEASE_BOT_SSH_KEY") || strings.Contains(prep, "environment:") {
		return errors.New("the bot key or an environment appears outside the push job")
	}
	if !strings.Contains(push, "${{ secrets.RELEASE_BOT_SSH_KEY }}") || !strings.Contains(push, "    environment: auto-release\n") {
		return errors.New("the push job lacks the bot key or environment auto-release")
	}
	if strings.Count(yml, "runs-on:") != 2 || strings.Count(yml, "    runs-on: ubuntu-24.04\n") != 2 {
		return errors.New("want exactly two jobs, both on ubuntu-24.04")
	}
	if strings.Count(yml, "permissions:") != 1 || !strings.Contains(yml, "\npermissions:\n  contents: read\n  actions: read\n\n") {
		return errors.New("want one workflow-level permissions block of contents: read, actions: read")
	}
	for _, line := range strings.Split(yml, "\n") {
		if strings.Contains(line, "uses:") && !arPinned.MatchString(line) {
			return fmt.Errorf("action not pinned by commit SHA: %s", strings.TrimSpace(line))
		}
	}
	if !strings.Contains(yml, "format('auto-release-ignored-{0}', github.run_id)") {
		return errors.New("ignored runs must use a concurrency group of their own")
	}
	if !strings.Contains(yml, "tagged=$(git tag --points-at HEAD 'v*')") {
		return errors.New("the version step must end quietly on an already tagged HEAD")
	}
	return nil
}

func TestArWorkflow(t *testing.T) {
	b, err := os.ReadFile(arWorkflowYML)
	if err != nil {
		t.Fatal(err)
	}
	yml := string(b)
	if err := arCheck(yml); err != nil {
		t.Fatal(err)
	}
	for name, bad := range map[string]string{
		"the key in prepare": strings.Replace(yml, "\n  push:\n", "\n      - env:\n          K: ${{ secrets.RELEASE_BOT_SSH_KEY }}\n  push:\n", 1),
		"a tag-pinned action": strings.Replace(yml, "actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1", "actions/checkout@v7", 1),
		"a write token":       strings.Replace(yml, "  contents: read\n  actions: read\n", "  contents: write\n  actions: read\n", 1),
		"a latest runner":     strings.Replace(yml, "runs-on: ubuntu-24.04", "runs-on: ubuntu-latest", 1),
		"no environment":      strings.Replace(yml, "    environment: auto-release\n", "", 1),
		"one shared group":    strings.Replace(yml, "auto-release-ignored-{0}", "auto-release-{0}", 1),
		"no tagged-HEAD exit": strings.Replace(yml, "tagged=$(git tag --points-at HEAD 'v*')", "tagged=", 1),
	} {
		if bad == yml {
			t.Fatalf("test setup: %s changed nothing", name)
		}
		if err := arCheck(bad); err == nil {
			t.Errorf("arCheck passed a workflow with %s", name)
		}
	}
}

func TestArHarnessSkipsBotReleaseCommits(t *testing.T) {
	b, err := os.ReadFile(arHarnessYML)
	if err != nil {
		t.Fatal(err)
	}
	yml := string(b)
	start := strings.Index(yml, "\n  build:\n")
	if start < 0 {
		t.Fatal("harness.yml has no build job")
	}
	end := strings.Index(yml[start+1:], "\n  vm:\n")
	if end < 0 {
		t.Fatal("harness.yml has no vm job after build")
	}
	if !strings.Contains(yml[start:start+1+end], arHarnessIf) {
		t.Errorf("the harness build job does not carry %q", arHarnessIf)
	}
}

package main_test

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// Acceptance tests of the automatic release spec, driven through the built
// releasetool binary and the files the release tooling reads.

type cliResult struct {
	code           int
	stdout, stderr string
}

func buildReleasetool(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "releasetool")
	out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput() //nolint:gosec // G204: builds this package into a temporary path
	if err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

func runCLI(t *testing.T, bin, stdin string, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(bin, args...) //nolint:gosec // G204: the binary this test built
	cmd.Stdin = strings.NewReader(stdin)
	var so, se bytes.Buffer
	cmd.Stdout, cmd.Stderr = &so, &se
	err := cmd.Run()
	code := 0
	if err != nil {
		var ee *exec.ExitError
		if !errors.As(err, &ee) {
			t.Fatalf("running %v: %v", args, err)
		}
		code = ee.ExitCode()
	}
	return cliResult{code: code, stdout: so.String(), stderr: se.String()}
}

// commits joins messages the way `git log -z --format=%B` writes them: each
// message ends in a newline and a NUL.
func commits(msgs ...string) string {
	var b strings.Builder
	for _, m := range msgs {
		b.WriteString(m + "\n\x00")
	}
	return b.String()
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the working directory")
		}
		dir = parent
	}
}

func nextVersionCases(t *testing.T, bin string, cases []struct {
	name, current, stdin, want string
}) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := runCLI(t, bin, c.stdin, "next-version", c.current)
			if r.code != 0 {
				t.Fatalf("next-version %s exited %d: %s", c.current, r.code, r.stderr)
			}
			if got := strings.TrimSpace(r.stdout); got != c.want {
				t.Errorf("next-version %s printed %q, want %q", c.current, got, c.want)
			}
		})
	}
}

func TestA1_PreReleaseCounterIncrements(t *testing.T) {
	bin := buildReleasetool(t)
	nextVersionCases(t, bin, []struct{ name, current, stdin, want string }{
		{"rc feat", "1.0.0-rc.2", commits("feat: add a thing"), "1.0.0-rc.3"},
		{"rc fix", "1.0.0-rc.2", commits("fix: repair a thing"), "1.0.0-rc.3"},
		{"rc fix with scope", "1.0.0-rc.2", commits("fix(web): repair a thing"), "1.0.0-rc.3"},
		{"beta feat", "1.3.0-beta.1", commits("feat: add a thing"), "1.3.0-beta.2"},
		{"beta fix", "1.3.0-beta.1", commits("fix: repair a thing"), "1.3.0-beta.2"},
		{"counter is numeric", "1.0.0-rc.9", commits("fix: repair a thing"), "1.0.0-rc.10"},
		{"breaking stays on the line", "1.0.0-rc.2", commits("feat!: drop a thing"), "1.0.0-rc.3"},
	})
}

func TestA2_StableVersionBumpFollowsCommitTypes(t *testing.T) {
	bin := buildReleasetool(t)
	nextVersionCases(t, bin, []struct{ name, current, stdin, want string }{
		{"type bang", "1.2.3", commits("feat!: drop a thing"), "2.0.0"},
		{"scoped bang", "1.2.3", commits("fix(api)!: drop a thing"), "2.0.0"},
		{"footer", "1.2.3", commits("fix: change a thing\n\nBREAKING CHANGE: the old flag is gone"), "2.0.0"},
		{"footer on a feat", "1.2.3", commits("feat: add a thing\n\nBREAKING CHANGE: the old flag is gone"), "2.0.0"},
		{"footer on a skipped type", "1.2.3", commits("docs: describe a thing\n\nBREAKING CHANGE: the old flag is gone"), "2.0.0"},
		{"bang on a skipped type", "1.2.3", commits("chore!: drop a thing"), "2.0.0"},
		{"breaking beats feat and fix", "1.2.3", commits("feat: a", "fix: b", "refactor!: c"), "2.0.0"},
		{"major from 1.9.9", "1.9.9", commits("fix!: a"), "2.0.0"},
		{"feat", "1.2.3", commits("feat: add a thing"), "1.3.0"},
		{"feat beats fix", "1.2.3", commits("fix: a", "feat(web): b", "perf: c"), "1.3.0"},
		{"fix", "1.2.3", commits("fix: repair a thing"), "1.2.4"},
		{"perf", "1.2.3", commits("perf: speed up a thing"), "1.2.4"},
		{"refactor", "1.2.3", commits("refactor: tidy a thing"), "1.2.4"},
		{"revert", "1.2.3", commits("revert: undo a thing"), "1.2.4"},
		{"fix beside docs", "1.2.3", commits("docs: a", "fix: b", "chore: c"), "1.2.4"},
		{"messages are separate", "1.2.3", commits("fix: a", "docs: b\n\nplain body text"), "1.2.4"},
	})
}

func TestA3_NothingShipsPrintsNothing(t *testing.T) {
	bin := buildReleasetool(t)
	skipped := []string{
		"docs: update a guide", "test: cover a thing", "chore: bump a tool",
		"ci: change a workflow", "style: format a file", "build: change the Makefile",
		"docs(readme): scoped docs", "chore(deps): scoped chore",
	}
	for _, current := range []string{"1.2.3", "1.0.0-rc.2"} {
		for _, msg := range skipped {
			t.Run(current+" "+msg, func(t *testing.T) {
				r := runCLI(t, bin, commits(msg), "next-version", current)
				if r.code != 0 || r.stdout != "" {
					t.Errorf("next-version %s on %q: exit %d, stdout %q, stderr %q; want exit 0 and no output", current, msg, r.code, r.stdout, r.stderr)
				}
			})
		}
		t.Run(current+" all skipped types together", func(t *testing.T) {
			r := runCLI(t, bin, commits(skipped...), "next-version", current)
			if r.code != 0 || r.stdout != "" {
				t.Errorf("exit %d, stdout %q; want exit 0 and no output", r.code, r.stdout)
			}
		})
		t.Run(current+" no commits", func(t *testing.T) {
			r := runCLI(t, bin, "", "next-version", current)
			if r.code != 0 || r.stdout != "" {
				t.Errorf("exit %d, stdout %q; want exit 0 and no output", r.code, r.stdout)
			}
		})
	}
	t.Run("a shipping commit among them still releases", func(t *testing.T) {
		r := runCLI(t, bin, commits(append(append([]string{}, skipped...), "fix: repair a thing")...), "next-version", "1.2.3")
		if got := strings.TrimSpace(r.stdout); got != "1.2.4" {
			t.Errorf("printed %q, want 1.2.4", got)
		}
	})
}

func TestA4_NonConventionalCommitsAreIgnoredAndRevertCounts(t *testing.T) {
	bin := buildReleasetool(t)
	nextVersionCases(t, bin, []struct{ name, current, stdin, want string }{
		{"plain subject", "1.2.3", commits("Update stuff"), ""},
		{"merge subject", "1.2.3", commits("Merge branch 'x' into main"), ""},
		{"unknown word", "1.2.3", commits("WIP"), ""},
		{"capitalised type", "1.2.3", commits("Fix: a thing"), ""},
		{"plain subject in a pre-release", "1.0.0-rc.2", commits("Update stuff"), ""},
		{"git revert subject", "1.2.3", commits("Revert \"feat: add a thing\"\n\nThis reverts commit 0123456789abcdef."), "1.2.4"},
		{"git revert subject of a plain subject", "1.2.3", commits("Revert \"Update stuff\""), "1.2.4"},
		{"git revert in a pre-release", "1.0.0-rc.2", commits("Revert \"fix: a thing\""), "1.0.0-rc.3"},
		{"ignored beside a fix", "1.2.3", commits("Update stuff", "fix: a"), "1.2.4"},
	})
}

func TestA4_RevertSubjectAppearsInTheNotes(t *testing.T) {
	cliff := os.Getenv("GIT_CLIFF")
	if cliff == "" {
		p, err := exec.LookPath("git-cliff")
		if err != nil {
			t.Skip("git-cliff is neither in GIT_CLIFF nor on PATH")
		}
		cliff = p
	}
	root := repoRoot(t)
	repo := t.TempDir()
	env := append(os.Environ(), "GIT_AUTHOR_NAME=a", "GIT_AUTHOR_EMAIL=a@example.invalid",
		"GIT_COMMITTER_NAME=a", "GIT_COMMITTER_EMAIL=a@example.invalid",
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...) //nolint:gosec // G204: git with the test's own arguments
		cmd.Dir = repo
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q", "-b", "main")
	for _, m := range []string{"feat: add widgets", "Revert \"fix: break gadgets\"", "Update stuff", "docs: explain widgets",
		"chore!: drop the old config", "ci: move the job\n\nBREAKING CHANGE: the runner label changed"} {
		git("commit", "-q", "--allow-empty", "-m", m)
	}
	cmd := exec.Command(cliff, "--config", filepath.Join(root, "cliff.toml"), "--repository", repo, "--tag", "v9.9.9") //nolint:gosec // G204: git-cliff from GIT_CLIFF or PATH on a scratch repository
	cmd.Env = env
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git-cliff: %v", err)
	}
	notes := string(out)
	if !strings.Contains(notes, "fix: break gadgets") {
		t.Errorf("the notes do not hold the reverted subject; notes:\n%s", notes)
	}
	if !strings.Contains(notes, "add widgets") {
		t.Errorf("the notes lose an ordinary feat; notes:\n%s", notes)
	}
	if strings.Contains(notes, "Update stuff") {
		t.Errorf("the notes hold a non-conventional commit; notes:\n%s", notes)
	}
	for _, want := range []string{"### Breaking changes", "drop the old config", "move the job"} {
		if !strings.Contains(notes, want) {
			t.Errorf("the notes lose a breaking commit of a skipped type (%q); notes:\n%s", want, notes)
		}
	}
}

func gateRun(name, path, event, status, conclusion, branch, sha string) string {
	concl := "null"
	if conclusion != "" {
		concl = fmt.Sprintf("%q", conclusion)
	}
	return fmt.Sprintf(`{"id":1,"name":%q,"path":%q,"event":%q,"status":%q,"conclusion":%s,"head_branch":%q,"head_sha":%q,"run_attempt":1}`,
		name, path, event, status, concl, branch, sha)
}

func gateInput(runs ...string) string {
	return fmt.Sprintf(`{"total_count":%d,"workflow_runs":[%s]}`, len(runs), strings.Join(runs, ","))
}

const gateTip = "0123456789abcdef0123456789abcdef01234567"

func ciRun(event, status, conclusion, branch, sha string) string {
	return gateRun("CI", ".github/workflows/ci.yml", event, status, conclusion, branch, sha)
}

func harnessRun(event, status, conclusion, branch, sha string) string {
	return gateRun("Harness", ".github/workflows/harness.yml", event, status, conclusion, branch, sha)
}

func TestA7_GateReleasesOnlyWhenCIAndHarnessSucceeded(t *testing.T) {
	bin := buildReleasetool(t)
	exit := func(t *testing.T, stdin string, want int) {
		t.Helper()
		r := runCLI(t, bin, stdin, "gate", gateTip)
		if r.code != want {
			t.Errorf("gate exited %d, want %d; stdout %q, stderr %q", r.code, want, r.stdout, r.stderr)
		}
	}
	okCI := ciRun("push", "completed", "success", "main", gateTip)
	okHarness := harnessRun("push", "completed", "success", "main", gateTip)

	t.Run("CI push and Harness push succeeded", func(t *testing.T) { exit(t, gateInput(okCI, okHarness), 0) })
	t.Run("Harness schedule counts", func(t *testing.T) {
		exit(t, gateInput(okCI, harnessRun("schedule", "completed", "success", "main", gateTip)), 0)
	})
	t.Run("Harness workflow_dispatch counts", func(t *testing.T) {
		exit(t, gateInput(okCI, harnessRun("workflow_dispatch", "completed", "success", "main", gateTip)), 0)
	})
	t.Run("run order does not matter", func(t *testing.T) { exit(t, gateInput(okHarness, okCI), 0) })
	t.Run("runs for other tips are ignored", func(t *testing.T) {
		other := "ffffffffffffffffffffffffffffffffffffffff"
		exit(t, gateInput(ciRun("push", "completed", "failure", "main", other), okCI, okHarness), 0)
	})

	t.Run("no runs at all", func(t *testing.T) { exit(t, gateInput(), 3) })
	t.Run("Harness missing", func(t *testing.T) { exit(t, gateInput(okCI), 3) })
	t.Run("CI missing", func(t *testing.T) { exit(t, gateInput(okHarness), 3) })
	t.Run("only runs for another tip", func(t *testing.T) {
		other := "ffffffffffffffffffffffffffffffffffffffff"
		exit(t, gateInput(ciRun("push", "completed", "success", "main", other), harnessRun("push", "completed", "success", "main", other)), 3)
	})
	t.Run("CI succeeded on another branch", func(t *testing.T) {
		exit(t, gateInput(ciRun("push", "completed", "success", "feature", gateTip), okHarness), 3)
	})
	t.Run("Harness succeeded on another branch", func(t *testing.T) {
		exit(t, gateInput(okCI, harnessRun("push", "completed", "success", "feature", gateTip)), 3)
	})
	t.Run("CI from a pull request does not count", func(t *testing.T) {
		exit(t, gateInput(ciRun("pull_request", "completed", "success", "main", gateTip), okHarness), 3)
	})
	t.Run("CI from a schedule does not count", func(t *testing.T) {
		exit(t, gateInput(ciRun("schedule", "completed", "success", "main", gateTip), okHarness), 3)
	})
	t.Run("Harness from a pull request does not count", func(t *testing.T) {
		exit(t, gateInput(okCI, harnessRun("pull_request", "completed", "success", "main", gateTip)), 3)
	})
	for _, status := range []string{"queued", "in_progress", "waiting", "requested", "pending"} {
		t.Run("CI "+status, func(t *testing.T) { exit(t, gateInput(ciRun("push", status, "", "main", gateTip), okHarness), 3) })
		t.Run("Harness "+status, func(t *testing.T) { exit(t, gateInput(okCI, harnessRun("push", status, "", "main", gateTip)), 3) })
	}
	for _, conclusion := range []string{"failure", "skipped", "cancelled", "neutral", "timed_out", "action_required", "stale"} {
		t.Run("CI "+conclusion, func(t *testing.T) {
			exit(t, gateInput(ciRun("push", "completed", conclusion, "main", gateTip), okHarness), 3)
		})
		t.Run("Harness "+conclusion, func(t *testing.T) {
			exit(t, gateInput(okCI, harnessRun("push", "completed", conclusion, "main", gateTip)), 3)
		})
		t.Run("both "+conclusion, func(t *testing.T) {
			exit(t, gateInput(ciRun("push", "completed", conclusion, "main", gateTip), harnessRun("schedule", "completed", conclusion, "main", gateTip)), 3)
		})
	}

	for name, stdin := range map[string]string{
		"empty input":        "",
		"not json":           "this is not json",
		"truncated json":     `{"workflow_runs":[{"name":"CI"`,
		"array, not object":  `[]`,
		"runs not a list":    `{"workflow_runs":"none"}`,
		"run not an object":  `{"workflow_runs":[1,2]}`,
		"status not a text":  `{"workflow_runs":[{"status":5,"head_sha":"` + gateTip + `"}]}`,
		"trailing text only": `}`,
	} {
		t.Run("malformed: "+name, func(t *testing.T) {
			r := runCLI(t, bin, stdin, "gate", gateTip)
			if r.code == 0 || r.code == 3 {
				t.Errorf("gate exited %d on malformed input, want non-zero other than 3; stderr %q", r.code, r.stderr)
			}
		})
	}
}

func TestA8_CurrentIsTheHighestTagAndMustBePublished(t *testing.T) {
	bin := buildReleasetool(t)
	dir := t.TempDir()
	tagsFile := func(tags ...string) string {
		p := filepath.Join(dir, fmt.Sprintf("tags-%d", len(tags)))
		if err := os.WriteFile(p, []byte(strings.Join(tags, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	releases := func(published ...string) string {
		var items []string
		for _, tag := range published {
			items = append(items, fmt.Sprintf(`{"tagName":%q,"isDraft":false,"isPrerelease":%t}`, tag, strings.Contains(tag, "-")))
		}
		return "[" + strings.Join(items, ",") + "]"
	}

	t.Run("prints the highest version", func(t *testing.T) {
		tags := tagsFile("v1.0.0-beta.1", "index", "v1.0.0-rc.1", "v1.0.0-rc.2", "v0.9.9")
		r := runCLI(t, bin, releases("v1.0.0-beta.1", "v1.0.0-rc.1", "v1.0.0-rc.2", "index"), "current", tags)
		if r.code != 0 || strings.TrimSpace(r.stdout) != "1.0.0-rc.2" {
			t.Errorf("exit %d, stdout %q, stderr %q; want 1.0.0-rc.2", r.code, r.stdout, r.stderr)
		}
	})
	t.Run("numeric identifiers compare as numbers", func(t *testing.T) {
		tags := tagsFile("v1.0.0-rc.2", "v1.0.0-rc.10", "v1.0.0-rc.9")
		r := runCLI(t, bin, releases("v1.0.0-rc.2", "v1.0.0-rc.9", "v1.0.0-rc.10"), "current", tags)
		if strings.TrimSpace(r.stdout) != "1.0.0-rc.10" {
			t.Errorf("printed %q, want 1.0.0-rc.10", r.stdout)
		}
	})
	t.Run("a stable version beats its pre-releases and minors compare as numbers", func(t *testing.T) {
		tags := tagsFile("v1.9.0", "v1.10.0-rc.1", "v1.10.0", "v1.10.0-rc.2")
		r := runCLI(t, bin, releases("v1.9.0", "v1.10.0-rc.1", "v1.10.0", "v1.10.0-rc.2"), "current", tags)
		if strings.TrimSpace(r.stdout) != "1.10.0" {
			t.Errorf("printed %q, want 1.10.0", r.stdout)
		}
	})
	t.Run("the index tag is not a version", func(t *testing.T) {
		tags := tagsFile("index", "v1.0.0-rc.2", "latest")
		r := runCLI(t, bin, releases("v1.0.0-rc.2", "index"), "current", tags)
		if r.code != 0 || strings.TrimSpace(r.stdout) != "1.0.0-rc.2" {
			t.Errorf("exit %d, stdout %q; want 1.0.0-rc.2", r.code, r.stdout)
		}
	})
	t.Run("a lower unpublished tag does not matter", func(t *testing.T) {
		tags := tagsFile("v1.0.0-rc.1", "v1.0.0-rc.2")
		r := runCLI(t, bin, releases("v1.0.0-rc.2"), "current", tags)
		if r.code != 0 || strings.TrimSpace(r.stdout) != "1.0.0-rc.2" {
			t.Errorf("exit %d, stdout %q; want 1.0.0-rc.2", r.code, r.stdout)
		}
	})
	t.Run("the highest tag has no release", func(t *testing.T) {
		tags := tagsFile("v1.0.0-rc.1", "v1.0.0-rc.2", "v1.0.0-rc.3")
		r := runCLI(t, bin, releases("v1.0.0-rc.1", "v1.0.0-rc.2"), "current", tags)
		if r.code == 0 || r.code == 3 || r.stdout != "" {
			t.Errorf("exit %d, stdout %q; want a failure that prints no version", r.code, r.stdout)
		}
		if !strings.Contains(r.stderr, "v1.0.0-rc.3") {
			t.Errorf("stderr %q does not name the tag v1.0.0-rc.3", r.stderr)
		}
	})
	t.Run("the highest tag has only a draft release", func(t *testing.T) {
		tags := tagsFile("v1.0.0-rc.2", "v1.0.0-rc.3")
		list := `[{"tagName":"v1.0.0-rc.2","isDraft":false},{"tagName":"v1.0.0-rc.3","isDraft":true}]`
		r := runCLI(t, bin, list, "current", tags)
		if r.code == 0 || r.stdout != "" {
			t.Errorf("exit %d, stdout %q; want a failure", r.code, r.stdout)
		}
		if !strings.Contains(r.stderr, "v1.0.0-rc.3") {
			t.Errorf("stderr %q does not name the tag v1.0.0-rc.3", r.stderr)
		}
	})
	t.Run("an empty release list", func(t *testing.T) {
		tags := tagsFile("v1.0.0-rc.2")
		r := runCLI(t, bin, "[]", "current", tags)
		if r.code == 0 || !strings.Contains(r.stderr, "v1.0.0-rc.2") {
			t.Errorf("exit %d, stderr %q; want a failure naming v1.0.0-rc.2", r.code, r.stderr)
		}
	})
	t.Run("no version tag", func(t *testing.T) {
		tags := tagsFile("index", "latest")
		r := runCLI(t, bin, releases("index"), "current", tags)
		if r.code == 0 || r.stdout != "" {
			t.Errorf("exit %d, stdout %q; want a failure", r.code, r.stdout)
		}
	})
}

const changelogHead = "# Changelog\n\nAll notable changes to Darkpanel. Versions follow docs/versioning.md.\n\n"

func TestA15_CheckPrepRefusesAnythingReleasePrepDoesNotWrite(t *testing.T) {
	bin := buildReleasetool(t)
	dir := t.TempDir()
	changelog := func(body string) string {
		p := filepath.Join(dir, "CHANGELOG.md")
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil { //nolint:gosec // G703: a path under the test's temporary directory
			t.Fatal(err)
		}
		return p
	}
	good := changelog(changelogHead + "## 1.0.0-rc.3 - 2026-11-18\n\n### Fixes\n\n- **api:** repair a thing\n\n## 1.0.0-rc.2 - 2026-11-17\n\n### Features\n\n- guide an install\n")
	allowed := "CHANGELOG.md\nREADME.md\ndocs/user/install.md\nschemas/released/v1.0.0-rc.3/kv.item.get/v1.json\nschemas/released/v1.0.0-rc.3/kv.item.set/v1.json\n"

	pass := func(t *testing.T, version, file, paths string) {
		t.Helper()
		r := runCLI(t, bin, paths, "check-prep", version, file)
		if r.code != 0 {
			t.Errorf("check-prep exited %d, want 0; stderr %q", r.code, r.stderr)
		}
	}
	fail := func(t *testing.T, version, file, paths string) {
		t.Helper()
		r := runCLI(t, bin, paths, "check-prep", version, file)
		if r.code == 0 {
			t.Errorf("check-prep exited 0, want a failure")
		}
	}

	t.Run("the four allowed kinds of path pass", func(t *testing.T) { pass(t, "1.0.0-rc.3", good, allowed) })
	t.Run("only the changelog and snapshots", func(t *testing.T) {
		pass(t, "1.0.0-rc.3", good, "CHANGELOG.md\nschemas/released/v1.0.0-rc.3/a.b/v1.json\n")
	})
	for _, p := range []string{
		"scripts/auto-run", "Makefile", "release/auto-push.sh", ".github/workflows/release.yml",
		"release/allowed_signers", "internal/core/registry.go", "go.mod", "docs/user/other.md",
		"docs/user/install.md.bak", "CHANGELOG.md.orig", "README.md/extra", "docs/README.md",
		"schemas/released/v1.0.0-rc.2/kv.item.get/v1.json",
		"schemas/released/v1.0.0-rc.30/kv.item.get/v1.json",
		"schemas/released/v1.0.0-rc.3-extra/kv.item.get/v1.json",
		"schemas/released/v1.0.0/kv.item.get/v1.json",
		"schemas/other/v1.0.0-rc.3/kv.item.get/v1.json",
		"other/schemas/released/v1.0.0-rc.3/kv.item.get/v1.json",
		"web/src/main.ts",
	} {
		t.Run("outside path "+p, func(t *testing.T) { fail(t, "1.0.0-rc.3", good, allowed+p+"\n") })
		t.Run("outside path alone "+p, func(t *testing.T) { fail(t, "1.0.0-rc.3", good, "CHANGELOG.md\n"+p+"\n") })
	}
	t.Run("changelog opens with another version", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.2 - 2026-11-17\n\n### Features\n\n- guide an install\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("changelog opens with Unreleased", func(t *testing.T) {
		f := changelog(changelogHead + "## Unreleased\n\n### Fixes\n\n- repair a thing\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("changelog heading has no date", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.3\n\n### Fixes\n\n- repair a thing\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("changelog heading has a malformed date", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.3 - yesterday\n\n### Fixes\n\n- repair a thing\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("changelog heading names a longer version", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.30 - 2026-11-18\n\n### Fixes\n\n- repair a thing\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("first section has no entry", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.3 - 2026-11-18\n\n## 1.0.0-rc.2 - 2026-11-17\n\n### Features\n\n- guide an install\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("first section has only group headings", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.0.0-rc.3 - 2026-11-18\n\n### Fixes\n\n## 1.0.0-rc.2 - 2026-11-17\n\n- an entry of the older section\n")
		fail(t, "1.0.0-rc.3", f, allowed)
	})
	t.Run("changelog has no section at all", func(t *testing.T) {
		fail(t, "1.0.0-rc.3", changelog(changelogHead), allowed)
	})
	t.Run("changelog file is missing", func(t *testing.T) {
		fail(t, "1.0.0-rc.3", filepath.Join(dir, "absent.md"), allowed)
	})
	t.Run("stable version heading passes", func(t *testing.T) {
		f := changelog(changelogHead + "## 1.2.4 - 2026-11-18\n\n### Fixes\n\n- repair a thing\n")
		pass(t, "1.2.4", f, "CHANGELOG.md\nschemas/released/v1.2.4/a.b/v1.json\n")
	})
}

type workflowJob struct {
	name  string
	lines []string
}

// splitWorkflow returns the lines before `jobs:` and each job's lines.
func splitWorkflow(text string) (preamble []string, jobs []workflowJob) {
	header := regexp.MustCompile(`^  ([A-Za-z0-9_-]+):\s*$`)
	inJobs := false
	for _, line := range strings.Split(text, "\n") {
		switch {
		case !inJobs && line == "jobs:":
			inJobs = true
		case !inJobs:
			preamble = append(preamble, line)
		default:
			if m := header.FindStringSubmatch(line); m != nil {
				jobs = append(jobs, workflowJob{name: m[1]})
			} else if len(jobs) > 0 {
				jobs[len(jobs)-1].lines = append(jobs[len(jobs)-1].lines, line)
			}
		}
	}
	return preamble, jobs
}

// permissionBlock returns the key: value pairs under a `permissions:` line
// of the given indentation, and whether there is one.
func permissionBlock(lines []string, indent string) (map[string]string, bool) {
	perms := map[string]string{}
	found := false
	for i := 0; i < len(lines); i++ {
		if lines[i] != indent+"permissions:" {
			continue
		}
		found = true
		for _, l := range lines[i+1:] {
			rest, ok := strings.CutPrefix(l, indent+"  ")
			if !ok || strings.TrimSpace(rest) == "" || strings.HasPrefix(strings.TrimSpace(rest), "#") {
				break
			}
			k, v, _ := strings.Cut(rest, ":")
			perms[strings.TrimSpace(k)] = strings.TrimSpace(strings.SplitN(v, "#", 2)[0])
		}
	}
	return perms, found
}

// workflowProblems lists how the text of a workflow breaks the security
// rules the spec sets for auto-release.yml.
func workflowProblems(text string) []string {
	var problems []string
	preamble, jobs := splitWorkflow(text)
	names := []string{}
	for _, j := range jobs {
		names = append(names, j.name)
	}
	if len(jobs) != 2 || !contains(names, "prepare") || !contains(names, "push") {
		problems = append(problems, fmt.Sprintf("jobs are %v, want prepare and push", names))
	}

	const secret = "RELEASE_BOT_SSH_KEY" //nolint:gosec // G101: the secret's name, not its value
	if strings.Contains(strings.Join(preamble, "\n"), secret) {
		problems = append(problems, secret+" is referenced outside the jobs")
	}
	for _, j := range jobs {
		refs := strings.Count(strings.Join(j.lines, "\n"), secret)
		switch {
		case j.name == "push" && refs == 0:
			problems = append(problems, "push does not use "+secret)
		case j.name != "push" && refs > 0:
			problems = append(problems, fmt.Sprintf("job %s references %s", j.name, secret))
		}
		runsOn := ""
		for _, l := range j.lines {
			if v, ok := strings.CutPrefix(l, "    runs-on:"); ok {
				runsOn = strings.TrimSpace(v)
			}
		}
		if runsOn != "ubuntu-24.04" {
			problems = append(problems, fmt.Sprintf("job %s runs on %q, want ubuntu-24.04", j.name, runsOn))
		}
		if perms, ok := permissionBlock(j.lines, "    "); ok && !samePerms(perms) {
			problems = append(problems, fmt.Sprintf("job %s sets permissions %v", j.name, perms))
		}
	}

	if perms, ok := permissionBlock(preamble, ""); !ok || !samePerms(perms) {
		problems = append(problems, fmt.Sprintf("workflow permissions are %v, want contents: read and actions: read", perms))
	}

	pinned := regexp.MustCompile(`^[A-Za-z0-9_.-]+/[A-Za-z0-9_./-]+@[0-9a-f]{40}$`)
	uses := regexp.MustCompile(`(?m)^\s*(?:-\s+)?uses:\s*(\S+)`)
	all := regexp.MustCompile(`#.*`).ReplaceAllString(text, "")
	found := uses.FindAllStringSubmatch(all, -1)
	if len(found) == 0 {
		problems = append(problems, "no action is used")
	}
	for _, m := range found {
		if !pinned.MatchString(m[1]) {
			problems = append(problems, fmt.Sprintf("action %s is not pinned by a 40-hex commit SHA", m[1]))
		}
	}
	return problems
}

func samePerms(p map[string]string) bool {
	return len(p) == 2 && p["contents"] == "read" && p["actions"] == "read"
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestA14_AutoReleaseWorkflowKeepsTheKeyInThePushJob(t *testing.T) {
	path := filepath.Join(repoRoot(t), ".github", "workflows", "auto-release.yml")
	raw, err := os.ReadFile(path) //nolint:gosec // G304: the workflow of this repository
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, p := range workflowProblems(text) {
		t.Error(p)
	}

	// The checker must see each violation it exists for.
	mutants := map[string]func(string) string{
		"key in the prepare job": func(s string) string {
			return strings.Replace(s, "    runs-on: ubuntu-24.04\n    outputs:", "    runs-on: ubuntu-24.04\n    env:\n      K: ${{ secrets.RELEASE_BOT_SSH_KEY }}\n    outputs:", 1)
		},
		"key at workflow level": func(s string) string {
			return strings.Replace(s, "env:\n  GOTOOLCHAIN: local", "env:\n  GOTOOLCHAIN: local\n  K: ${{ secrets.RELEASE_BOT_SSH_KEY }}", 1)
		},
		"push job on a floating runner": func(s string) string {
			return strings.Replace(s, "  push:\n    needs: prepare\n    if: needs.prepare.outputs.release == 'true'\n    runs-on: ubuntu-24.04", "  push:\n    needs: prepare\n    if: needs.prepare.outputs.release == 'true'\n    runs-on: ubuntu-latest", 1)
		},
		"prepare job on another runner": func(s string) string { return strings.Replace(s, "runs-on: ubuntu-24.04", "runs-on: ubuntu-22.04", 1) },
		"contents write":                func(s string) string { return strings.Replace(s, "contents: read", "contents: write", 1) },
		"actions write":                 func(s string) string { return strings.Replace(s, "actions: read", "actions: write", 1) },
		"extra permission": func(s string) string {
			return strings.Replace(s, "actions: read", "actions: read\n  id-token: write", 1)
		},
		"an action pinned by tag": func(s string) string {
			return regexp.MustCompile(`actions/checkout@[0-9a-f]{40}`).ReplaceAllString(s, "actions/checkout@v4")
		},
		"an action pinned by short sha": func(s string) string {
			return regexp.MustCompile(`actions/setup-go@[0-9a-f]{40}`).ReplaceAllString(s, "actions/setup-go@b7ad1da")
		},
		"an action on a branch": func(s string) string {
			return regexp.MustCompile(`actions/upload-artifact@[0-9a-f]{40}`).ReplaceAllString(s, "actions/upload-artifact@main")
		},
		"an action without a pin": func(s string) string {
			return regexp.MustCompile(`actions/download-artifact@[0-9a-f]{40}`).ReplaceAllString(s, "actions/download-artifact")
		},
	}
	for name, mutate := range mutants {
		t.Run("checker detects "+name, func(t *testing.T) {
			m := mutate(text)
			if m == text {
				t.Fatal("the mutation did not change the workflow text; the fixture is stale")
			}
			if len(workflowProblems(m)) == 0 {
				t.Error("the checker accepts the mutated workflow")
			}
		})
	}
}

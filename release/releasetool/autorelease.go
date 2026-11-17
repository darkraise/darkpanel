package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"slices"
	"strings"
)

// errSkip is the gate's verdict that this tip is not released now. It is no
// failure: the workflow ends quietly and the next qualifying run decides.
var errSkip = errors.New("not released")

func exitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case errors.Is(err, errSkip):
		return 3
	}
	return 1
}

// workflowRun is the part of a runs-API workflow run the gate reads.
type workflowRun struct {
	Path       string `json:"path"`
	HeadSHA    string `json:"head_sha"`
	HeadBranch string `json:"head_branch"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

// gate decides whether tip, the head of main, may be released: CI from the
// push and the harness from the push, the nightly or a manual run must both
// have succeeded on it. A pull-request CI run tests a merge commit, and a run
// on another branch tests other code, so neither counts.
func gate(tip string, stdin io.Reader, stdout io.Writer) error {
	var page struct {
		Runs []workflowRun `json:"workflow_runs"`
	}
	if err := json.NewDecoder(stdin).Decode(&page); err != nil {
		return fmt.Errorf("workflow runs: %w", err)
	}
	for _, w := range []struct {
		name, path string
		events     []string
	}{
		{"CI", ".github/workflows/ci.yml", []string{"push"}},
		{"Harness", ".github/workflows/harness.yml", []string{"push", "schedule", "workflow_dispatch"}},
	} {
		var runs []workflowRun
		for _, r := range page.Runs {
			if r.Path == w.path && r.HeadSHA == tip && r.HeadBranch == "main" && slices.Contains(w.events, r.Event) {
				runs = append(runs, r)
			}
		}
		level, state := gateState(runs)
		if level == "" {
			continue
		}
		if _, err := fmt.Fprintf(stdout, "::%s::%s on %s: %s\n", level, w.name, tip, state); err != nil {
			return err
		}
		return errSkip
	}
	return nil
}

// gateState returns "" when one of runs succeeded, and otherwise the
// annotation level and the state to report: a cancelled run warns, since a
// person may have to start it again, while a run still going, a failure or
// no run at all is only noted.
func gateState(runs []workflowRun) (level, state string) {
	if len(runs) == 0 {
		return "notice", "no run"
	}
	var unfinished, cancelled bool
	for _, r := range runs {
		switch {
		case r.Status == "completed" && r.Conclusion == "success":
			return "", ""
		case r.Status != "completed":
			unfinished = true
			state = r.Status
		case r.Conclusion == "cancelled":
			cancelled = true
		}
	}
	switch {
	case cancelled:
		return "warning", "cancelled"
	case unfinished:
		return "notice", state
	}
	return "notice", runs[0].Conclusion
}

// current returns the highest v<semver> tag of the tags file, which must
// already be a published release: releasing on top of a tag that never
// published would leave a gap in the changelog and the index.
func current(tagsFile string, stdin io.Reader) (string, error) {
	data, err := os.ReadFile(tagsFile) //nolint:gosec // G304: the tags file named on the command line
	if err != nil {
		return "", err
	}
	var best *version
	for line := range strings.SplitSeq(string(data), "\n") {
		v, err := parseTag(strings.TrimSpace(line))
		if err != nil {
			continue
		}
		if best == nil || compare(v, *best) > 0 {
			best = &v
		}
	}
	if best == nil {
		return "", errors.New("no v<version> tag")
	}
	list, err := readList(stdin)
	if err != nil {
		return "", err
	}
	for _, r := range list {
		if r.TagName == "v"+best.text && !r.IsDraft {
			return best.text, nil
		}
	}
	return "", fmt.Errorf("tag v%s has no published release", best.text)
}

// checkPrep refuses a prepared release change that touches more than
// release-prep writes, or whose changelog does not open with an entry for
// version: the release commit is pushed without review, so it must hold
// nothing else.
func checkPrep(v version, changelogPath string, stdin io.Reader) error {
	paths, err := io.ReadAll(stdin)
	if err != nil {
		return err
	}
	snapshots := "schemas/released/v" + v.text + "/"
	var sawChangelog bool
	for p := range strings.SplitSeq(string(paths), "\n") {
		p = strings.TrimSpace(p)
		switch {
		case p == "":
		case p == "CHANGELOG.md":
			sawChangelog = true
		case p == "README.md", p == "docs/user/install.md", strings.HasPrefix(p, snapshots):
		default:
			return fmt.Errorf("the release change touches %s, which release-prep does not write", p)
		}
	}
	if !sawChangelog {
		return errors.New("the release change does not touch CHANGELOG.md")
	}
	text, err := os.ReadFile(changelogPath) //nolint:gosec // G304: the changelog named on the command line
	if err != nil {
		return err
	}
	heading := regexp.MustCompile(`^## ` + regexp.QuoteMeta(v.text) + ` - \d{4}-\d{2}-\d{2}$`)
	inSection := false
	for line := range strings.SplitSeq(string(text), "\n") {
		if strings.HasPrefix(line, "## ") {
			if inSection {
				break
			}
			if !heading.MatchString(line) {
				return fmt.Errorf("%s: first version heading is %q, want \"## %s - YYYY-MM-DD\"", changelogPath, line, v.text)
			}
			inSection = true
			continue
		}
		if inSection && strings.HasPrefix(line, "- ") {
			return nil
		}
	}
	if !inSection {
		return fmt.Errorf("%s: no version heading, want \"## %s - YYYY-MM-DD\"", changelogPath, v.text)
	}
	return fmt.Errorf("%s: section %s has no entry", changelogPath, v.text)
}

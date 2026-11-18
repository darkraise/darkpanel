package main

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const gateTip = "1111111111111111111111111111111111111111"

// gateRun is one workflow run as the runs API lists it; an empty conclusion
// is JSON null.
func gateRun(file, sha, branch, event, status, conclusion string) string {
	c := "null"
	if conclusion != "" {
		c = fmt.Sprintf("%q", conclusion)
	}
	return fmt.Sprintf(`{"path":".github/workflows/%s","head_sha":%q,"head_branch":%q,"event":%q,"status":%q,"conclusion":%s}`,
		file, sha, branch, event, status, c)
}

func gateInput(runs ...string) string {
	return fmt.Sprintf(`{"total_count":%d,"workflow_runs":[%s]}`, len(runs), strings.Join(runs, ","))
}

func TestGate(t *testing.T) {
	ciOK := gateRun("ci.yml", gateTip, "main", "push", "completed", "success")
	hOK := gateRun("harness.yml", gateTip, "main", "push", "completed", "success")
	type gateCase struct {
		name, in string
		code     int
		prefix   string
	}
	cases := []gateCase{
		{"both succeeded", gateInput(ciOK, hOK), 0, ""},
		{"nightly harness", gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "schedule", "completed", "success")), 0, ""},
		{"manual harness", gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "workflow_dispatch", "completed", "success")), 0, ""},
		{"failed push then passing nightly", gateInput(ciOK,
			gateRun("harness.yml", gateTip, "main", "push", "completed", "failure"),
			gateRun("harness.yml", gateTip, "main", "schedule", "completed", "success")), 0, ""},
		{"no harness run", gateInput(ciOK), 3, "::notice::"},
		{"no runs", gateInput(), 3, "::notice::"},
		{"harness running", gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "push", "in_progress", "")), 3, "::notice::"},
		{"harness queued", gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "push", "queued", "")), 3, "::notice::"},
		{"ci from a pull request only", gateInput(gateRun("ci.yml", gateTip, "main", "pull_request", "completed", "success"), hOK), 3, "::notice::"},
		{"ci by hand is no push", gateInput(gateRun("ci.yml", gateTip, "main", "workflow_dispatch", "completed", "success"), hOK), 3, "::notice::"},
		{"harness on another branch", gateInput(ciOK, gateRun("harness.yml", gateTip, "feature", "push", "completed", "success")), 3, "::notice::"},
		{"ci for another commit", gateInput(gateRun("ci.yml", strings.Repeat("2", 40), "main", "push", "completed", "success"), hOK), 3, "::notice::"},
		{"another workflow does not count", gateInput(ciOK, gateRun("fuzz.yml", gateTip, "main", "push", "completed", "success")), 3, "::notice::"},
		{"cancelled warns", gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "push", "completed", "cancelled")), 3, "::warning::"},
		{"ci failed", gateInput(gateRun("ci.yml", gateTip, "main", "push", "completed", "failure"), hOK), 3, "::notice::"},
		{"ci running", gateInput(gateRun("ci.yml", gateTip, "main", "push", "in_progress", ""), hOK), 3, "::notice::"},
		{"ci cancelled warns", gateInput(gateRun("ci.yml", gateTip, "main", "push", "completed", "cancelled"), hOK), 3, "::warning::"},
	}
	for _, concl := range []string{"failure", "skipped", "neutral", "timed_out", "action_required", "stale"} {
		cases = append(cases, gateCase{"harness " + concl, gateInput(ciOK, gateRun("harness.yml", gateTip, "main", "push", "completed", concl)), 3, "::notice::"})
	}
	for _, c := range cases {
		var out bytes.Buffer
		err := run([]string{"gate", gateTip}, strings.NewReader(c.in), &out)
		if got := exitCode(err); got != c.code {
			t.Errorf("%s: exit %d (%v), want %d", c.name, got, err, c.code)
		}
		if c.prefix != "" && !strings.HasPrefix(out.String(), c.prefix) {
			t.Errorf("%s: printed %q, want a line starting %q", c.name, out.String(), c.prefix)
		}
	}
	for name, args := range map[string][]string{"malformed": {"gate", gateTip}, "no tip": {"gate"}} {
		err := run(args, strings.NewReader("{"), &bytes.Buffer{})
		if got := exitCode(err); got != 1 {
			t.Errorf("%s: exit %d (%v), want 1", name, got, err)
		}
	}
	if exitCode(nil) != 0 {
		t.Error("exitCode(nil) != 0")
	}
}

func TestCurrent(t *testing.T) {
	tags := filepath.Join(t.TempDir(), "tags")
	const all = `[{"tagName":"index","isDraft":false},{"tagName":"v1.0.0-beta.1","isDraft":false},{"tagName":"v1.0.0-rc.1","isDraft":false},{"tagName":"v1.0.0-rc.2","isDraft":false}]`
	for _, c := range []struct{ name, tags, list, want, errWant string }{
		{"newest published", "index\nv1.0.0-beta.1\nv1.0.0-rc.2\nv1.0.0-rc.1\nprobe-1\n", all, "1.0.0-rc.2", ""},
		{"stable above its candidates", "v1.0.0-rc.2\nv1.0.0\n", `[{"tagName":"v1.0.0","isDraft":false}]`, "1.0.0", ""},
		{"rc.10 above rc.9", "v1.0.0-rc.9\nv1.0.0-rc.10\n", `[{"tagName":"v1.0.0-rc.10","isDraft":false}]`, "1.0.0-rc.10", ""},
		{"newest unpublished", "v1.0.0-rc.2\nv1.0.0-rc.3\n", all, "", "tag v1.0.0-rc.3 has no published release"},
		{"newest a draft", "v1.0.0-rc.2\n", `[{"tagName":"v1.0.0-rc.2","isDraft":true}]`, "", "tag v1.0.0-rc.2 has no published release"},
		{"no version tag", "index\n", all, "", "no v<version> tag"},
	} {
		if err := os.WriteFile(tags, []byte(c.tags), 0o600); err != nil {
			t.Fatal(err)
		}
		var out bytes.Buffer
		err := run([]string{"current", tags}, strings.NewReader(c.list), &out)
		if c.errWant != "" {
			if err == nil || !strings.Contains(err.Error(), c.errWant) {
				t.Errorf("%s: current = %v, want an error containing %q", c.name, err, c.errWant)
			}
			continue
		}
		if err != nil || strings.TrimSpace(out.String()) != c.want {
			t.Errorf("%s: current = %q, %v; want %q", c.name, out.String(), err, c.want)
		}
	}
}

func TestCheckPrep(t *testing.T) {
	changelog := filepath.Join(t.TempDir(), "CHANGELOG.md")
	const good = "# Changelog\n\nAll notable changes to Darkpanel. Versions follow docs/versioning.md.\n\n" +
		"## 1.0.0-rc.3 - 2026-11-18\n\n### Fixes\n\n- **install:** quote the token path\n" +
		"## 1.0.0-rc.2 - 2026-11-17\n\n### Features\n\n- **install:** guide an install run with no options\n"
	const paths = "CHANGELOG.md\nREADME.md\ndocs/user/install.md\nschemas/released/v1.0.0-rc.3/core.host.list/v1.json\n"
	for _, c := range []struct{ name, changelog, paths, errWant string }{
		{"prepared", good, paths, ""},
		{"changelog and snapshot only", good, "CHANGELOG.md\nschemas/released/v1.0.0-rc.3/core.host.list/v1.json\n", ""},
		{"changelog only", good, "CHANGELOG.md\n", ""},
		{"changelog and docs pins", good, "CHANGELOG.md\nREADME.md\ndocs/user/install.md\n", ""},
		{"code changed", good, paths + "internal/core/x.go\n", "internal/core/x.go"},
		{"older snapshot changed", good, paths + "schemas/released/v1.0.0-rc.2/core.host.list/v1.json\n", "schemas/released/v1.0.0-rc.2/core.host.list/v1.json"},
		{"current schema changed", good, paths + "schemas/core.host.list/v1.json\n", "schemas/core.host.list/v1.json"},
		{"changelog untouched", good, "README.md\n", "CHANGELOG.md"},
		{"other version first", strings.Replace(good, "## 1.0.0-rc.3 - 2026-11-18", "## 1.0.0-rc.4 - 2026-11-18", 1), paths, "## 1.0.0-rc.3 - "},
		{"empty section", strings.Replace(good, "### Fixes\n\n- **install:** quote the token path\n", "", 1), paths, "has no entry"},
	} {
		if err := os.WriteFile(changelog, []byte(c.changelog), 0o600); err != nil {
			t.Fatal(err)
		}
		err := run([]string{"check-prep", "1.0.0-rc.3", changelog}, strings.NewReader(c.paths), &bytes.Buffer{})
		if c.errWant == "" {
			if err != nil {
				t.Errorf("%s: check-prep = %v, want success", c.name, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.errWant) {
			t.Errorf("%s: check-prep = %v, want an error containing %q", c.name, err, c.errWant)
		}
	}
}

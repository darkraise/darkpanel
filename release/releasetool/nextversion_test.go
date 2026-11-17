package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestNextVersion(t *testing.T) {
	for _, c := range []struct {
		name, current string
		commits       []string
		want          string
	}{
		{"rc after a feat", "1.0.0-rc.2", []string{"feat(install): guide a run\n"}, "1.0.0-rc.3"},
		{"rc after a fix", "1.0.0-rc.2", []string{"fix: quote the path\n"}, "1.0.0-rc.3"},
		{"beta stays a pre-release when breaking", "1.3.0-beta.1", []string{"feat!: drop v1\n"}, "1.3.0-beta.2"},
		{"rc.9 to rc.10", "1.0.0-rc.9", []string{"perf: cache hashes\n"}, "1.0.0-rc.10"},
		{"bang is major", "1.4.2", []string{"fix: a\n", "refactor(api)!: rename the field\n"}, "2.0.0"},
		{"footer is major", "1.4.2", []string{"fix: a\n\nBREAKING CHANGE: the field is gone\n"}, "2.0.0"},
		{"hyphenated footer is major", "1.4.2", []string{"fix: a\n\nBREAKING-CHANGE: the field is gone\n"}, "2.0.0"},
		{"breaking chore ships as major", "1.4.2", []string{"chore!: drop the old config\n"}, "2.0.0"},
		{"feat is minor", "1.4.2", []string{"fix: a\n", "feat: b\n"}, "1.5.0"},
		{"fix is patch", "1.4.2", []string{"docs: a\n", "fix: b\n"}, "1.4.3"},
		{"perf is patch", "1.4.2", []string{"perf: a\n"}, "1.4.3"},
		{"refactor is patch", "1.4.2", []string{"refactor(core): a\n"}, "1.4.3"},
		{"revert is patch", "1.4.2", []string{"revert: feat: b\n"}, "1.4.3"},
		{"git revert subject is a revert", "1.4.2", []string{"Revert \"feat: b\"\n\nThis reverts commit 0123abc.\n"}, "1.4.3"},
		{"breaking text in a subject is no footer", "1.4.2", []string{"docs: explain BREAKING CHANGE: footers\n", "fix: c\n"}, "1.4.3"},
		{"nothing ships", "1.4.2", []string{"docs: a\n", "test: b\n", "chore(deps): c\n", "ci: d\n", "style: e\n", "build: f\n"}, ""},
		{"non-conventional is ignored", "1.4.2", []string{"Update stuff\n", "docs: a\n"}, ""},
		{"non-conventional with a breaking footer is ignored", "1.4.2", []string{"Update stuff\n\nBREAKING CHANGE: x\n"}, ""},
		{"git revert with a breaking footer is major", "1.4.2", []string{"Revert \"feat: b\"\n\nBREAKING CHANGE: x\n"}, "2.0.0"},
		{"no commits", "1.0.0-rc.2", nil, ""},
		{"no numeric identifier but nothing ships", "1.0.0-rc", []string{"docs: a\n"}, ""},
	} {
		in := strings.Join(c.commits, "\x00")
		if len(c.commits) > 0 {
			in += "\x00"
		}
		var out bytes.Buffer
		if err := run([]string{"next-version", c.current}, strings.NewReader(in), &out); err != nil {
			t.Errorf("%s: next-version %s: %v", c.name, c.current, err)
			continue
		}
		if got := strings.TrimSpace(out.String()); got != c.want {
			t.Errorf("%s: next-version %s = %q, want %q", c.name, c.current, got, c.want)
		}
	}
}

func TestNextVersionRefuses(t *testing.T) {
	for _, current := range []string{"1.0.0-rc", "v1.0.0", "1.0"} {
		if err := run([]string{"next-version", current}, strings.NewReader("fix: a\n\x00"), &bytes.Buffer{}); err == nil {
			t.Errorf("next-version %s succeeded, want an error", current)
		}
	}
	if err := run([]string{"next-version"}, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
		t.Errorf("next-version with no version = %v, want the usage error", err)
	}
}

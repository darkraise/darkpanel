package docs_test

import (
	"regexp"
	"strings"
	"testing"
)

var (
	rmToolchain = regexp.MustCompile(`(?m)^toolchain go(\S+)$`)
	rmPnpm      = regexp.MustCompile(`"packageManager":\s*"pnpm@([^"]+)"`)
	rmNode      = regexp.MustCompile(`"node":\s*">=(\d+)"`)
	rmChangelog = regexp.MustCompile(`(?m)^## (\S+) - \d{4}-\d{2}-\d{2}$`)
	rmPinned    = regexp.MustCompile(`github\.com/Darkraise/darkpanel/releases/download/v([^/\s]+)/`)
	rmVersion   = regexp.MustCompile(`--version (\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?)\b`)
)

func rmFirst(t *testing.T, rel string, re *regexp.Regexp) string {
	t.Helper()
	m := re.FindStringSubmatch(dcRead(t, rel))
	if m == nil {
		t.Fatalf("%s has no match for %s", rel, re)
	}
	return m[1]
}

func TestReadmeLinksResolve(t *testing.T) {
	for _, rel := range []string{"README.md", "CONTRIBUTING.md", "SECURITY.md", "CODE_OF_CONDUCT.md"} {
		dcLinks(t, rel)
	}
}

func TestReadmePrerequisitesMatchToolchain(t *testing.T) {
	readme := dcRead(t, "README.md")
	want := []string{
		"Go " + rmFirst(t, "go.mod", rmToolchain),
		"Node " + rmFirst(t, "web/package.json", rmNode),
		"pnpm " + rmFirst(t, "web/package.json", rmPnpm),
	}
	for _, name := range dcMissing(readme, want) {
		t.Errorf("README.md does not state the prerequisite %q that the build requires", name)
	}
	contributing := dcRead(t, "CONTRIBUTING.md")
	for _, name := range dcMissing(contributing, want) {
		t.Errorf("CONTRIBUTING.md does not state the prerequisite %q that the build requires", name)
	}
}

// The install command in the README and the install guide names a release
// tag while no stable release exists, so it goes stale with each release
// candidate unless a test holds it to the changelog's newest version.
func TestReadmeInstallNamesNewestRelease(t *testing.T) {
	newest := rmFirst(t, "CHANGELOG.md", rmChangelog)
	for _, file := range []string{"README.md", "docs/user/install.md"} {
		text := dcRead(t, file)
		pins := append(rmPinned.FindAllStringSubmatch(text, -1), rmVersion.FindAllStringSubmatch(text, -1)...)
		if file == "README.md" && len(pins) == 0 && !strings.Contains(text, "/releases/latest/download/install.sh") {
			t.Fatal("README.md has no install command")
		}
		for _, m := range pins {
			if m[1] != newest {
				t.Errorf("%s installs v%s, but the newest release in CHANGELOG.md is %s", file, m[1], newest)
			}
		}
	}
}

// TestReadmeNamesM11Modules holds the README's feature list to naming the
// host administration modules and linking their guides.
func TestReadmeNamesM11Modules(t *testing.T) {
	readme := dcRead(t, "README.md")
	start := strings.Index(readme, "## Features\n")
	end := strings.Index(readme, "\n## Status")
	if start < 0 || end < start {
		t.Fatal("README.md has no Features section before Status")
	}
	features := strings.Join(strings.Fields(readme[start:end]), " ")
	want := []string{
		"services", "journal", "users", "SSH", "fail2ban", "terminal", "files", "scheduled jobs", "swap", "kernel",
	}
	for _, id := range []string{"services", "users", "fail2ban", "console", "jobs", "tuning"} {
		want = append(want, "(docs/user/modules/"+id+".md)")
	}
	for _, name := range dcMissing(features, want) {
		t.Errorf("README.md's feature list does not name %q", name)
	}
}

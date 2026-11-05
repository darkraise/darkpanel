//go:build harness

package harness

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/test/harness/release"
)

const upgUnitCommit = "3c5e837d976666d556d0fa1939b7636c67be2a64"

func TestUpgUnitParseSources(t *testing.T) {
	other := strings.Repeat("ab", 20)
	ok := func(lines ...string) string { return strings.Join(lines, "\n") + "\n" }
	baseline := "1.0.0 " + upgUnitCommit
	for _, tc := range []struct {
		name, text string
		want       []upgSource
	}{
		{"the baseline", ok(baseline), []upgSource{{"1.0.0", upgUnitCommit}}},
		{"comments and blank lines", ok("# sources", "", baseline, "   ", "  # indented comment"), []upgSource{{"1.0.0", upgUnitCommit}}},
		{"ascending sources", ok(baseline, "1.0.1 "+other, "1.0.2-rc.1 "+upgUnitCommit),
			[]upgSource{{"1.0.0", upgUnitCommit}, {"1.0.1", other}, {"1.0.2-rc.1", upgUnitCommit}}},
		{"no final newline", baseline, []upgSource{{"1.0.0", upgUnitCommit}}},
		{"windows line ends", "1.0.0 " + upgUnitCommit + "\r\n", []upgSource{{"1.0.0", upgUnitCommit}}},
	} {
		got, err := upgParseSources(tc.text)
		if err != nil || !slices.Equal(got, tc.want) {
			t.Errorf("%s: upgParseSources = %v, %v; want %v", tc.name, got, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, text, want string }{
		{"empty", "", "lists no source"},
		{"comments only", "# nothing\n\n", "lists no source"},
		{"one field", "1.0.0\n", `line 1: want <version> <commit>, got "1.0.0"`},
		{"three fields", "1.0.0 " + upgUnitCommit + " extra\n", "line 1: want <version> <commit>"},
		{"a v prefix", "v1.0.0 " + upgUnitCommit + "\n", "line 1: update: "},
		{"build metadata", "1.0.0+build " + upgUnitCommit + "\n", "line 1: update: "},
		{"not a version", "latest " + upgUnitCommit + "\n", "line 1: update: "},
		{"short commit", "1.0.0 3c5e837\n", `line 1: commit "3c5e837" is not 40 lower-case hexadecimal characters`},
		{"upper-case commit", "1.0.0 " + strings.ToUpper(upgUnitCommit) + "\n", "is not 40 lower-case hexadecimal characters"},
		{"a pre-1.0 source", "0.3.0 " + upgUnitCommit + "\n", "version 0.3.0 is not of major 1"},
		{"a 2.x source", "2.0.0 " + upgUnitCommit + "\n", "version 2.0.0 is not of major 1"},
		{"the build under test", "1.0.0 " + upgUnitCommit + "\n1.1.0 " + other + "\n", "line 2: version 1.1.0 is not older than the build under test, 1.1.0"},
		{"above the build under test", "1.0.0 " + upgUnitCommit + "\n1.2.0 " + other + "\n", "version 1.2.0 is not older"},
		{"first is not the baseline", "1.0.1 " + upgUnitCommit + "\n", "the first source is 1.0.1, want the baseline 1.0.0"},
		{"listed twice", "1.0.0 " + upgUnitCommit + "\n1.0.0 " + other + "\n", "line 2: version 1.0.0 is listed twice"},
		{"descending", "1.0.0 " + upgUnitCommit + "\n1.0.2 " + other + "\n1.0.1 " + upgUnitCommit + "\n", "line 3: version 1.0.1 is not above 1.0.2"},
		{"a pre-release below its release", "1.0.0 " + upgUnitCommit + "\n1.0.1 " + other + "\n1.0.1-rc.1 " + upgUnitCommit + "\n", "line 3: version 1.0.1-rc.1 is not above 1.0.1"},
	} {
		got, err := upgParseSources(tc.text)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: upgParseSources = %v, %v; want an error naming %q", tc.name, got, err, tc.want)
		}
	}
}

// TestUpgUnitSourcesFile checks the committed sources file: it parses, and
// its sources fit TestUpgradeMatrix's deadline.
func TestUpgUnitSourcesFile(t *testing.T) {
	sources, err := upgLoadSources(upgSourcesFile)
	if err != nil {
		t.Fatal(err)
	}
	if sources[0].Version != "1.0.0" {
		t.Errorf("the first source is %s, want 1.0.0", sources[0].Version)
	}
	if time.Duration(len(sources))*upgSourceDeadline > upgDeadline {
		t.Errorf("%d sources of %v each do not fit the %v deadline: raise upgDeadline, the workflow step's -timeout and the job's timeout-minutes together, or split the matrix",
			len(sources), upgSourceDeadline, upgDeadline)
	}
	if _, err := upgLoadSources(filepath.Join(t.TempDir(), "missing.txt")); err == nil {
		t.Error("upgLoadSources of a missing file succeeded")
	}
}

func TestUpgUnitReleases(t *testing.T) {
	src := upgSource{Version: "1.0.0", Commit: upgUnitCommit}
	got := upgReleases("/m2", src)
	want := []release.Version{
		{Version: "1.0.0", Channel: "stable", Binary: "/m2/darkpanel-1.0.0"},
		{Version: "1.1.0", Channel: "stable", Binary: "/m2/darkpanel-1.1.0"},
		{Version: "1.1.1", Channel: "stable", Binary: "/m2/darkpanel-1.1.1"},
	}
	if !slices.Equal(got, want) {
		t.Errorf("upgReleases = %+v, want %+v", got, want)
	}
	if names := upgBuildNames(src); !slices.Equal(names, []string{"darkpanel-1.0.0", "darkpanel-1.1.0", "darkpanel-1.1.1"}) {
		t.Errorf("upgBuildNames = %v", names)
	}
}

func TestUpgUnitCheckBuilds(t *testing.T) {
	dir := t.TempDir()
	sources := []upgSource{{"1.0.0", upgUnitCommit}}
	err := upgCheckBuilds(dir, sources)
	if err == nil || !strings.Contains(err.Error(), "needs darkpanel-1.0.0 in "+dir) {
		t.Fatalf("upgCheckBuilds of an empty directory = %v, want it to name darkpanel-1.0.0", err)
	}
	for _, name := range []string{"darkpanel-1.0.0", "darkpanel-1.1.0"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("binary"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := upgCheckBuilds(dir, sources); err == nil || !strings.Contains(err.Error(), "needs darkpanel-1.1.1") {
		t.Fatalf("upgCheckBuilds without the broken twin = %v, want it to name darkpanel-1.1.1", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "darkpanel-1.1.1"), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := upgCheckBuilds(dir, sources); err != nil {
		t.Errorf("upgCheckBuilds with every build = %v", err)
	}
}

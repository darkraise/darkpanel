package schemas

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

func TestA5_GuardHelperReportsSameMajorDuplicates(t *testing.T) {
	one := &fstest.MapFile{Data: []byte("{\"x\":1}\n")}
	for _, c := range []struct {
		name string
		fsys fstest.MapFS
	}{
		{"same major, two minors", fstest.MapFS{
			"released/v1.2.3/core.host.list/v1.json": one,
			"released/v1.4.0/core.host.list/v1.json": one,
		}},
		{"pre-release pair", fstest.MapFS{
			"released/v1.0.0-rc.1/core.host.list/v1.json": one,
			"released/v1.0.0-rc.2/core.host.list/v1.json": one,
		}},
		{"three tags, one pair", fstest.MapFS{
			"released/v2.0.0/core.host.list/v1.json": one,
			"released/v2.1.0/core.host.list/v1.json": {Data: []byte("{\"x\":2}\n")},
			"released/v2.2.0/core.host.list/v1.json": one,
		}},
	} {
		got, err := releasedDuplicates(c.fsys)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if len(got) == 0 {
			t.Errorf("%s: the duplicate is not reported", c.name)
			continue
		}
		if !strings.Contains(strings.Join(got, "\n"), "core.host.list/v1.json") {
			t.Errorf("%s: the report %q does not name the duplicated file", c.name, got)
		}
	}
}

func TestA5_GuardHelperAcceptsACrossMajorCopy(t *testing.T) {
	one := &fstest.MapFile{Data: []byte("{\"x\":1}\n")}
	got, err := releasedDuplicates(fstest.MapFS{
		"released/v1.2.3/core.host.list/v1.json":  one,
		"released/v10.0.0/core.host.list/v1.json": one,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("a copy in v10.0.0 of a v1.2.3 file is reported: %q", got)
	}
}

func TestA5_GuardHelperAcceptsTheEmbeddedTree(t *testing.T) {
	got, err := releasedDuplicates(FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("schemas.FS holds duplicates: %q", got)
	}
}

// treeFiles returns, from git's tree for ref, one "<path without tag> <blob
// id>" entry per file under a tag directory of schemas/released, and the
// number of files in each tag directory.
func treeFiles(t *testing.T, root, ref string) ([]string, map[string]int) {
	t.Helper()
	cmd := exec.Command("git", "-C", root, "ls-tree", "-r", ref, "--", "schemas/released") //nolint:gosec // G204: git with a fixed argument list
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("git ls-tree -r %s: %v", ref, err)
	}
	var pairs []string
	perTag := map[string]int{}
	for line := range strings.SplitSeq(strings.TrimSpace(string(out)), "\n") {
		meta, path, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		rest := strings.TrimPrefix(path, "schemas/released/")
		tag, inner, ok := strings.Cut(rest, "/")
		if !ok || !tagDir.MatchString(tag) {
			continue
		}
		fields := strings.Fields(meta)
		pairs = append(pairs, inner+" "+fields[2])
		perTag[tag]++
	}
	slices.Sort(pairs)
	return slices.Compact(pairs), perTag
}

func TestA6_MigratedTreeHoldsEveryReleasedPairOfRC3(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	if err := exec.Command("git", "-C", root, "rev-parse", "--verify", "--quiet", "v1.0.0-rc.3^{commit}").Run(); err != nil { //nolint:gosec // G204: git with a fixed argument list
		t.Fatalf("tag v1.0.0-rc.3 is not in this clone: %v", err)
	}
	before, _ := treeFiles(t, root, "v1.0.0-rc.3")
	after, perTag := treeFiles(t, root, "HEAD")
	if len(before) == 0 {
		t.Fatal("v1.0.0-rc.3 holds no released snapshots")
	}
	for _, p := range before {
		if _, found := slices.BinarySearch(after, p); !found {
			t.Errorf("lost: %s", p)
		}
	}
	for _, p := range after {
		if _, found := slices.BinarySearch(before, p); !found {
			t.Errorf("added: %s", p)
		}
	}
	if got := perTag["v1.0.0-beta.1"]; got != 169 {
		t.Errorf("v1.0.0-beta.1 holds %d files, want 169", got)
	}
	if got := perTag["v1.0.0-rc.1"]; got != 4 {
		t.Errorf("v1.0.0-rc.1 holds %d files, want 4", got)
	}
	for _, tag := range []string{"v1.0.0-rc.2", "v1.0.0-rc.3"} {
		if _, err := os.Stat(filepath.Join(root, "schemas", "released", tag)); err == nil {
			t.Errorf("schemas/released/%s still exists", tag)
		}
		if perTag[tag] != 0 {
			t.Errorf("HEAD's tree holds %d files under %s", perTag[tag], tag)
		}
	}
}

func TestA6_CompatibilityAndNegotiationTestsPass(t *testing.T) {
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ pkg, run string }{
		{"./internal/modules/", "^TestReleasedSnapshotsCompatible$"},
		{"./internal/controller/dispatch/", "Negotiat|ABrokenReleasedSnapshot"},
	} {
		cmd := exec.Command("go", "test", "-count=1", "-timeout", "5m", "-run", c.run, c.pkg) //nolint:gosec // G204: go test with fixed package and pattern
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("go test -run %s %s: %v\n%s", c.run, c.pkg, err, out)
		}
	}
}

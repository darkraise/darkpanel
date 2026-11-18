package schemas

import (
	"bytes"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// tagDir matches the name of a tag directory directly under released/.
var tagDir = regexp.MustCompile(`^v[0-9]+\..+$`)

// releasedDuplicates returns, sorted, every file released/<tag dir>/<path>
// that has a byte-identical file at the same <path> in another tag directory
// of the same major; nil when there is none.
func releasedDuplicates(fsys fs.FS) ([]string, error) {
	type file struct {
		name string
		data []byte
	}
	groups := map[string][]file{}
	err := fs.WalkDir(fsys, "released", func(name string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		tag, path, ok := strings.Cut(strings.TrimPrefix(name, "released/"), "/")
		if !ok || !tagDir.MatchString(tag) {
			return nil
		}
		data, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		major, _, _ := strings.Cut(tag, ".")
		key := major + "/" + path
		groups[key] = append(groups[key], file{name, data})
		return nil
	})
	if err != nil {
		return nil, err
	}
	var dups []string
	for _, group := range groups {
		for i, a := range group {
			for j, b := range group {
				if i != j && bytes.Equal(a.data, b.data) {
					dups = append(dups, a.name)
					break
				}
			}
		}
	}
	slices.Sort(dups)
	return dups, nil
}

func TestReleasedDuplicates(t *testing.T) {
	one := &fstest.MapFile{Data: []byte("{\"input\":1}\n")}
	two := &fstest.MapFile{Data: []byte("{\"input\":2}\n")}
	for _, c := range []struct {
		name string
		fsys fstest.MapFS
		want []string
	}{
		{"same major", fstest.MapFS{
			"released/v1.2.3/a.b/v1.json": one,
			"released/v1.3.0/a.b/v1.json": one,
		}, []string{"released/v1.2.3/a.b/v1.json", "released/v1.3.0/a.b/v1.json"}},
		{"pre-release pair", fstest.MapFS{
			"released/v1.0.0-rc.1/a.b/v1.json": one,
			"released/v1.0.0-rc.2/a.b/v1.json": one,
		}, []string{"released/v1.0.0-rc.1/a.b/v1.json", "released/v1.0.0-rc.2/a.b/v1.json"}},
		{"other major", fstest.MapFS{
			"released/v1.2.3/a.b/v1.json":  one,
			"released/v10.0.0/a.b/v1.json": one,
		}, nil},
		{"changed content", fstest.MapFS{
			"released/v1.2.3/a.b/v1.json": one,
			"released/v1.3.0/a.b/v1.json": two,
		}, nil},
		{"other path", fstest.MapFS{
			"released/v1.2.3/a.b/v1.json": one,
			"released/v1.3.0/a.b/v2.json": one,
		}, nil},
		{"not tag directories", fstest.MapFS{
			"released/README.md":            one,
			"released/v1backup/a.b/v1.json": one,
			"released/notes/a.b/v1.json":    one,
			"released/v1.2.3/a.b/v1.json":   one,
		}, nil},
	} {
		got, err := releasedDuplicates(c.fsys)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if !slices.Equal(got, c.want) {
			t.Errorf("%s: releasedDuplicates = %q, want %q", c.name, got, c.want)
		}
	}
	if got, err := releasedDuplicates(fstest.MapFS{}); err == nil {
		t.Errorf("no released directory: releasedDuplicates = %q, want an error", got)
	}
}

// Each released snapshot is stored once per major (ADR 0002).
func TestReleasedSnapshotsStoredOnce(t *testing.T) {
	got, err := releasedDuplicates(FS)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("released snapshots stored more than once in a major: %q", got)
	}
}

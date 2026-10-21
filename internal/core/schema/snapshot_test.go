package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestActionSnapshot(t *testing.T) {
	def := fixtureAction(t, "demo.item.add")
	b, err := ActionSnapshot(def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "{\n  \"name\": \"demo.item.add\",\n") || !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("snapshot is not two-space indented with a final newline:\n%s", b)
	}
	var got struct {
		Name       string          `json:"name"`
		Version    int             `json:"version"`
		Class      string          `json:"class"`
		Side       string          `json:"side"`
		BreakGlass bool            `json:"breakGlass"`
		Input      json.RawMessage `json:"input"`
		Output     json.RawMessage `json:"output"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "demo.item.add" || got.Version != 1 || got.Class != "revertible" || got.Side != "agent" || got.BreakGlass {
		t.Errorf("header = %+v", got)
	}
	in, err := Input(def.InputType)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Output(def.OutputType)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, got.Input, in) || !jsonEqual(t, got.Output, out) {
		t.Error("snapshot schemas differ from Input and Output")
	}
	if violations, err := Compare(b, b); err != nil || len(violations) != 0 {
		t.Errorf("Compare(snapshot, snapshot) = %q, %v; want none", violations, err)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y bytes.Buffer
	if err := json.Compact(&x, a); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&y, b); err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func TestResourceSnapshot(t *testing.T) {
	res, ok := fixtureRegistry(t).Resource("demo.status")
	if !ok {
		t.Fatal("fixture resource missing")
	}
	b, err := ResourceSnapshot(res)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, []string{"name", "output"}) {
		t.Errorf("keys = %v, want [name output]", keys)
	}
	if string(got["name"]) != `"demo.status"` {
		t.Errorf("name = %s", got["name"])
	}
}

func TestSnapshots(t *testing.T) {
	files, err := Snapshots(fixtureRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"demo.item.add/v1.json", "demo.item.get/v1.json", "demo.item.purge/v1.json",
		"demo.item.remove/v1.json", "demo.item.repair/v1.json", "demo.secret.reset/v1.json", "demo.status/v1.json",
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

func TestWriteSnapshots(t *testing.T) {
	dir := t.TempDir()
	stale := map[string]string{
		"demo.item.old/v1.json":         "{}\n",
		"released/v0.1.0/x.y.z/v1.json": "{}\n",
		"README.md":                     "kept\n",
	}
	for name, content := range stale {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Snapshots(fixtureRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second run changes nothing
		if err := WriteSnapshots(dir, files); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a path under the test's temporary directory
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v, content equal %t", name, err, bytes.Equal(got, want))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.item.old")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale snapshot directory: %v, want it removed", err)
	}
	for _, kept := range []string{"released/v0.1.0/x.y.z/v1.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s: %v, want it kept", kept, err)
		}
	}
}

func TestWriteSnapshotsCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schemas")
	if err := WriteSnapshots(dir, map[string][]byte{}); err != nil {
		t.Fatalf("empty registry: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("snapshot directory: %v", err)
	}
}

func TestWriteSnapshotsRefusesSymlinks(t *testing.T) {
	for name, link := range map[string]struct{ at, target string }{
		"directory": {"demo.item.add", "released/v0.1.0/demo.item.add"},
		"file":      {"demo.item.add/v1.json", "../released/v0.1.0/demo.item.add/v1.json"},
	} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			frozen := filepath.Join(dir, "released", "v0.1.0", "demo.item.add", "v1.json")
			if err := os.MkdirAll(filepath.Dir(frozen), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(frozen, []byte("{}\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			at := filepath.Join(dir, link.at)
			if err := os.MkdirAll(filepath.Dir(at), 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(link.target, at); err != nil {
				t.Fatal(err)
			}
			files := map[string][]byte{"demo.item.add/v1.json": []byte("{\"changed\": true}\n")}
			if err := WriteSnapshots(dir, files); err == nil {
				t.Error("WriteSnapshots wrote through a symlink")
			}
			got, err := os.ReadFile(frozen) //nolint:gosec // G304: a path under the test's temporary directory
			if err != nil || string(got) != "{}\n" {
				t.Errorf("released snapshot = %q, %v; want it unchanged", got, err)
			}
		})
	}
}

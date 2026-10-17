package schema

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

type actionSnapshot struct {
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Class      core.Class      `json:"class"`
	Side       core.Side       `json:"side"`
	BreakGlass bool            `json:"breakGlass"`
	Input      json.RawMessage `json:"input"`
	Output     json.RawMessage `json:"output"`
}

type resourceSnapshot struct {
	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`
}

// ActionSnapshot returns the committed form of one action version: its
// declaration and both schemas, indented with two spaces, ending in a newline.
func ActionSnapshot(def core.ActionDef) ([]byte, error) {
	in, err := Input(def.InputType)
	if err != nil {
		return nil, err
	}
	out, err := Output(def.OutputType)
	if err != nil {
		return nil, err
	}
	s := def.Spec
	return indented(actionSnapshot{s.Name, s.Version, s.Class, s.Side, s.BreakGlass, in, out})
}

// ResourceSnapshot returns the committed form of a resource: its name and
// output schema.
func ResourceSnapshot(def core.ResourceDef) ([]byte, error) {
	out, err := Output(def.OutputType)
	if err != nil {
		return nil, err
	}
	return indented(resourceSnapshot{def.Name, out})
}

func indented(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("schema: snapshot: %w", err)
	}
	return append(b, '\n'), nil
}

// Snapshots returns every snapshot of reg keyed by its path under the
// snapshot directory: "<action>/v<n>.json" and "<resource>/v1.json".
func Snapshots(reg *core.Registry) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, def := range reg.Actions() {
		b, err := ActionSnapshot(def)
		if err != nil {
			return nil, err
		}
		files[fmt.Sprintf("%s/v%d.json", def.Spec.Name, def.Spec.Version)] = b
	}
	for _, def := range reg.Resources() {
		b, err := ResourceSnapshot(def)
		if err != nil {
			return nil, err
		}
		files[def.Name+"/v1.json"] = b
	}
	return files, nil
}

// WriteSnapshots writes files under dir, creating it when missing, and deletes
// every other .json file there, and directories left empty, except under
// dir/released, which holds the frozen snapshots of released tags (ADR 0002).
// Every operation stays inside dir, even through a symlink.
func WriteSnapshots(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	defer func() { _ = root.Close() }()
	for name, b := range files {
		if err := root.MkdirAll(path.Dir(name), 0o750); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		if err := root.WriteFile(name, b, 0o600); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	var dirs []string
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case name == "released":
			return fs.SkipDir
		case d.IsDir():
			if name != "." {
				dirs = append(dirs, name)
			}
			return nil
		case path.Ext(name) != ".json":
			return nil
		}
		if _, ok := files[name]; !ok {
			return root.Remove(name)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("schema: prune %s: %w", dir, err)
	}
	// Deepest first, so a parent is empty once its children are gone.
	slices.Sort(dirs)
	slices.Reverse(dirs)
	for _, d := range dirs {
		entries, err := fs.ReadDir(root.FS(), d)
		if err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		if len(entries) == 0 {
			if err := root.Remove(d); err != nil {
				return fmt.Errorf("schema: %w", err)
			}
		}
	}
	return nil
}

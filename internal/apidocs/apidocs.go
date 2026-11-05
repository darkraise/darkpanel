// Package apidocs renders the API reference under docs/api from the registry
// and the hand-written routes. Output is byte-for-byte deterministic, so a
// test can compare it with the committed files.
package apidocs

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// Generate renders the OpenAPI document of reg and routes and the Markdown
// reference built from it, keyed by slash path relative to docs/api.
func Generate(reg *core.Registry, routes []schema.Route) (map[string][]byte, error) {
	raw, err := schema.OpenAPI(reg, routes)
	if err != nil {
		return nil, fmt.Errorf("apidocs: %w", err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, raw, "", "  "); err != nil {
		return nil, fmt.Errorf("apidocs: %w", err)
	}
	indented.WriteByte('\n')
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("apidocs: %w", err)
	}
	r := newRenderer(doc)

	files := map[string][]byte{"openapi.json": indented.Bytes()}
	var modules []string
	for _, m := range reg.Modules() {
		page, err := r.modulePage(m)
		if err != nil {
			return nil, err
		}
		if page == "" {
			continue
		}
		files[m.ID+".md"] = []byte(page)
		modules = append(modules, m.ID)
	}
	index, err := r.index(modules)
	if err != nil {
		return nil, err
	}
	files["README.md"] = []byte(index)
	return files, nil
}

// Write creates dir, removes every .md and .json file directly in it that
// files does not name, and writes each of files by its slash path. It leaves
// subdirectories and files with another extension alone, and running it
// twice gives the same directory.
func Write(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("apidocs: %w", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return fmt.Errorf("apidocs: %w", err)
	}
	for _, e := range entries {
		if e.IsDir() || !slices.Contains([]string{".md", ".json"}, path.Ext(e.Name())) {
			continue
		}
		if _, keep := files[e.Name()]; keep {
			continue
		}
		if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("apidocs: %w", err)
		}
	}
	for name, content := range files {
		//nolint:gosec // G306: documentation files are world-readable
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(name)), content, 0o644); err != nil {
			return fmt.Errorf("apidocs: %w", err)
		}
	}
	return nil
}

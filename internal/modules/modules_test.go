package modules_test

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

// snapshotDir is the committed schemas/ directory at the repository root.
var snapshotDir = filepath.Join("..", "..", "schemas")

// releasedDir holds the frozen snapshots of released tags (ADR 0002).
var releasedDir = filepath.Join(snapshotDir, "released")

func built(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

func snapshots(t *testing.T, reg *core.Registry) map[string][]byte {
	t.Helper()
	files, err := schema.Snapshots(reg)
	if err != nil {
		t.Fatalf("schema.Snapshots: %v", err)
	}
	return files
}

// TestAllRegistersHostinfo is register row 1: hostinfo is compiled in by
// its one line in All, and the registry serves its resource.
func TestAllRegistersHostinfo(t *testing.T) {
	var ids []string
	for _, m := range modules.All() {
		ids = append(ids, m.Manifest().ID)
	}
	if !slices.Contains(ids, "hostinfo") {
		t.Fatalf("modules.All() = %v, want hostinfo among them", ids)
	}
	if _, ok := built(t).Resource("hostinfo.summary"); !ok {
		t.Error("the registry has no hostinfo.summary resource")
	}
}

// TestSnapshotsCurrent regenerates every snapshot in memory and fails when a
// committed file under schemas/ differs, is missing, or belongs to no
// registered action or resource (spec section 7). make schemas fixes all
// three.
func TestSnapshotsCurrent(t *testing.T) {
	files := snapshots(t, built(t))
	for _, name := range slices.Sorted(maps.Keys(files)) {
		got, err := os.ReadFile(filepath.Join(snapshotDir, filepath.FromSlash(name))) //nolint:gosec // G304: a snapshot path under the committed schemas/ directory
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.Errorf("schemas/%s is missing; run make schemas", name)
		case err != nil:
			t.Errorf("schemas/%s: %v", name, err)
		case !bytes.Equal(got, files[name]):
			t.Errorf("schemas/%s is out of date; run make schemas", name)
		}
	}
	err := filepath.WalkDir(snapshotDir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path == releasedDir:
			return fs.SkipDir
		case d.IsDir() || filepath.Ext(path) != ".json":
			return nil
		}
		rel, err := filepath.Rel(snapshotDir, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			t.Errorf("schemas/%s belongs to no registered action or resource; run make schemas", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk schemas/: %v", err)
	}
}

// TestReleasedSnapshotsCompatible runs the compatibility check of ADR 0002
// for every snapshot under schemas/released/<tag>/; until the first release
// there is none.
func TestReleasedSnapshotsCompatible(t *testing.T) {
	if err := schema.CheckReleased(releasedDir, snapshots(t, built(t))); err != nil {
		t.Errorf("released snapshots: %v", err)
	}
}

// TestServerRegistryHasTheSameShape shows that the registry the server
// builds with its services has the actions, resources and snapshots of
// registry.Build(), which the CLI, the schema generator and these tests use.
func TestServerRegistryHasTheSameShape(t *testing.T) {
	server, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Auth: new(auth.Service), Audit: &coretest.MemAudit{}, Modules: modules.All(),
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
	}))
	if err != nil {
		t.Fatalf("registry.BuildWith: %v", err)
	}
	want, got := snapshots(t, built(t)), snapshots(t, server)
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("server registry snapshots %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Errorf("server registry snapshot %s differs from registry.Build()'s", name)
		}
	}
}

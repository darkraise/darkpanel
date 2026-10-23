package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// rstFiles returns a writer for module "rst", which declares <dir>/user.rules
// as kind restore and <dir>/app.conf as kind file.
func rstFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	dir := t.TempDir()
	f, own := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"rst": {
		{Path: filepath.Join(dir, "user.rules"), Kind: core.OwnedRestore},
		{Path: filepath.Join(dir, "app.conf"), Kind: core.OwnedFile},
	}}})
	return f, own, dir
}

func TestRestoreWrites(t *testing.T) {
	f, own, dir := rstFiles(t)
	ctx := context.Background()
	path := filepath.Join(dir, "user.rules")
	uid, gid := os.Geteuid(), os.Getegid()
	entry := core.RestoreEntry{Path: path, Present: true, Mode: 0o640, UID: uid, GID: gid, Data: []byte("x")}
	if err := f.Restore(ctx, "rst", entry); err != nil {
		t.Fatalf("Restore (create): %v", err)
	}
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.Restore(ctx, "rst", entry); err != nil {
		t.Fatalf("Restore (replace): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if got := readFile(t, path); got != "x" || info.Mode().Perm() != 0o640 || int(st.Uid) != uid || int(st.Gid) != gid {
		t.Errorf("restored %q mode %o owner %d:%d, want x, 640, %d:%d", got, info.Mode().Perm(), st.Uid, st.Gid, uid, gid)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"user.rules"}) {
		t.Errorf("directory holds %v, want only user.rules (no temporary file left)", names)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestRestoreRemoves(t *testing.T) {
	f, _, dir := rstFiles(t)
	ctx := context.Background()
	path := filepath.Join(dir, "user.rules")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	entry := core.RestoreEntry{Path: path}
	if err := f.Restore(ctx, "rst", entry); err != nil {
		t.Fatalf("Restore (remove): %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present: %v", err)
	}
	if err := f.Restore(ctx, "rst", entry); err != nil {
		t.Errorf("Restore (remove) of a missing file: %v", err)
	}
}

func TestRestoreRefuses(t *testing.T) {
	cases := []struct {
		name    string
		present bool
		path    string                         // relative to the test directory
		setup   func(t *testing.T, dir string) // prepares the host
		check   func(t *testing.T, dir string) // asserts nothing changed
		want    string                         // in the error
	}{
		{name: "kind file", present: true, path: "app.conf", want: "does not declare"},
		{name: "undeclared", present: true, path: "other.rules", want: "does not declare"},
		{name: "symlink written", present: true, path: "user.rules", setup: rstSymlink, check: rstVictimKept, want: "is a symlink"},
		{name: "symlink removed", path: "user.rules", setup: rstSymlink, check: rstVictimKept, want: "is not a regular file"},
		{name: "directory written", present: true, path: "user.rules", setup: rstDirectory, want: "is not a regular file"},
		{name: "directory removed", path: "user.rules", setup: rstDirectory, want: "is not a regular file"},
		{name: "parent of another user", present: true, path: "user.rules", setup: func(t *testing.T, dir string) {
			if os.Geteuid() != 0 {
				t.Skip("needs root to own a directory by another user")
			}
			if err := os.Chown(dir, 12345, 12345); err != nil {
				t.Fatal(err)
			}
		}, want: "owned by uid 12345"},
		{name: "parent writable by others", present: true, path: "user.rules", setup: func(t *testing.T, dir string) {
			if err := os.Chmod(dir, 0o707); err != nil { //nolint:gosec // G302: the unsafe mode under test
				t.Fatal(err)
			}
		}, want: "writable by group or others"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, own, dir := rstFiles(t)
			if c.setup != nil {
				c.setup(t, dir)
			}
			entry := core.RestoreEntry{Path: filepath.Join(dir, c.path), Present: c.present, Mode: 0o600,
				UID: os.Geteuid(), GID: os.Getegid(), Data: []byte("x")}
			if err := f.Restore(context.Background(), "rst", entry); err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Restore = %v, want a refusal containing %q", err, c.want)
			}
			if c.check != nil {
				c.check(t, dir)
			}
			if _, err := os.Lstat(filepath.Join(dir, c.path)); c.setup == nil && !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a refused Restore created %s", c.path)
			}
			if got, _ := own.List(context.Background()); len(got) != 0 {
				t.Errorf("ownership = %+v, want nothing recorded", got)
			}
		})
	}
}

// rstSymlink puts a symlink at user.rules pointing at victim.
func rstSymlink(t *testing.T, dir string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "victim"), []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(dir, "victim"), filepath.Join(dir, "user.rules")); err != nil {
		t.Fatal(err)
	}
}

// rstVictimKept checks that the symlink and its target are unchanged.
func rstVictimKept(t *testing.T, dir string) {
	t.Helper()
	if got := readFile(t, filepath.Join(dir, "victim")); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
	if info, err := os.Lstat(filepath.Join(dir, "user.rules")); err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("the symlink was replaced or removed: %v", err)
	}
}

// rstDirectory puts a directory at user.rules.
func rstDirectory(t *testing.T, dir string) {
	t.Helper()
	if err := os.Mkdir(filepath.Join(dir, "user.rules"), 0o700); err != nil {
		t.Fatal(err)
	}
}

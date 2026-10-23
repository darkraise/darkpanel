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

// mgdFiles returns a writer for module "mgd", which declares <root>/tokens as
// a managed-dir with the default pattern and <root>/debs with the pattern
// ^[0-9]+\.deb$, and <root>/app.conf as a file.
func mgdFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	root := t.TempDir()
	f, own := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"mgd": {
		{Path: filepath.Join(root, "tokens"), Kind: core.OwnedManagedDir},
		{Path: filepath.Join(root, "debs"), Kind: core.OwnedManagedDir, Pattern: `^[0-9]+\.deb$`},
		{Path: filepath.Join(root, "app.conf"), Kind: core.OwnedFile},
	}}})
	return f, own, root
}

func TestWriteManagedCreatesDir(t *testing.T) {
	f, own, root := mgdFiles(t)
	ctx := context.Background()
	dir := filepath.Join(root, "tokens")
	own.onRecord = func(e core.OwnedEntry) {
		if _, err := os.Lstat(e.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists when it is recorded (%v); it must be recorded first", e.Path, err)
		}
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if err := f.WriteManaged(ctx, "mgd", dir, "abc.token", []byte("secret"), 0o600, uid, gid); err != nil {
		t.Fatalf("WriteManaged: %v", err)
	}
	own.onRecord = nil
	info, err := os.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if !info.IsDir() || info.Mode().Perm() != 0o700 || int(st.Uid) != os.Geteuid() {
		t.Errorf("directory mode %v owner %d, want a 0700 directory owned by %d", info.Mode(), st.Uid, os.Geteuid())
	}
	file := filepath.Join(dir, "abc.token")
	finfo, err := os.Stat(file)
	if err != nil {
		t.Fatal(err)
	}
	fst := finfo.Sys().(*syscall.Stat_t)
	if readFile(t, file) != "secret" || finfo.Mode().Perm() != 0o600 || int(fst.Uid) != uid || int(fst.Gid) != gid {
		t.Errorf("file mode %o owner %d:%d, want 600 and %d:%d", finfo.Mode().Perm(), fst.Uid, fst.Gid, uid, gid)
	}
	if err := f.WriteManaged(ctx, "mgd", dir, "abc.token", []byte("rotated"), 0o600, uid, gid); err != nil {
		t.Fatalf("WriteManaged (replace): %v", err)
	}
	if got := readFile(t, file); got != "rotated" {
		t.Errorf("file = %q after the second write", got)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"abc.token"}) {
		t.Errorf("directory holds %v, want only abc.token", names)
	}
	want := []core.OwnedEntry{{Path: dir, Module: "mgd", Kind: core.OwnedManagedDir, CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
}

func TestManagedNames(t *testing.T) {
	cases := []struct {
		dir, name string
		ok        bool
	}{
		{"tokens", "abc.token", true},
		{"tokens", "0-a_b.c", true},
		{"tokens", ".hidden", false},
		{"tokens", "A.token", false},
		{"tokens", "a/b", false},
		{"tokens", "..", false},
		{"tokens", strings.Repeat("a", 65), false},
		{"debs", "1.deb", true},
		{"debs", "a.deb", false},
	}
	for _, c := range cases {
		t.Run(c.dir+"/"+c.name, func(t *testing.T) {
			f, _, root := mgdFiles(t)
			dir := filepath.Join(root, c.dir)
			err := f.WriteManaged(context.Background(), "mgd", dir, c.name, []byte("x"), 0o600, os.Geteuid(), os.Getegid())
			if c.ok != (err == nil) {
				t.Fatalf("WriteManaged(%s) = %v, want ok %v", c.name, err, c.ok)
			}
			if rerr := f.RemoveManaged(context.Background(), "mgd", dir, c.name); c.ok != (rerr == nil) {
				t.Errorf("RemoveManaged(%s) = %v, want ok %v", c.name, rerr, c.ok)
			}
			if !c.ok {
				if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a refused name created %s", dir)
				}
			}
		})
	}
}

func TestRemoveManaged(t *testing.T) {
	f, own, root := mgdFiles(t)
	ctx := context.Background()
	dir := filepath.Join(root, "tokens")
	if err := f.RemoveManaged(ctx, "mgd", dir, "a.token"); err != nil {
		t.Errorf("RemoveManaged before the directory exists: %v", err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	for _, name := range []string{"a.token", "b.token"} {
		if err := f.WriteManaged(ctx, "mgd", dir, name, []byte(name), 0o600, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.RemoveManaged(ctx, "mgd", dir, "a.token"); err != nil {
		t.Fatalf("RemoveManaged: %v", err)
	}
	if err := f.RemoveManaged(ctx, "mgd", dir, "a.token"); err != nil {
		t.Errorf("RemoveManaged of a missing name: %v", err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "c.token")); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveManaged(ctx, "mgd", dir, "c.token"); err != nil {
		t.Fatalf("RemoveManaged of a symlink: %v", err)
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "d.token"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveManaged(ctx, "mgd", dir, "d.token"); err == nil {
		t.Error("RemoveManaged of a subdirectory succeeded")
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"b.token", "d.token"}) {
		t.Errorf("directory holds %v, want b.token and d.token", names)
	}
	if got, _ := own.List(ctx); len(got) != 1 {
		t.Errorf("ownership = %+v, want the directory still recorded", got)
	}
}

func TestManagedRefusals(t *testing.T) {
	cases := []struct {
		name  string
		dir   string // relative to the test directory
		setup func(t *testing.T, root string, own *fakeOwnership)
		want  string // in the error
	}{
		{name: "undeclared directory", dir: "other", want: "does not declare"},
		{name: "kind file", dir: "app.conf", want: "does not declare"},
		{name: "directory is a symlink", dir: "tokens", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", filepath.Join(root, "tokens")); err != nil {
				t.Fatal(err)
			}
			mgdRecord(t, own, filepath.Join(root, "tokens"))
		}, want: "symbolic link"},
		{name: "directory not recorded", dir: "tokens", setup: func(t *testing.T, root string, _ *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "tokens"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "is not Darkpanel's"},
		{name: "directory open to others", dir: "tokens", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "tokens"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(filepath.Join(root, "tokens"), 0o750); err != nil { //nolint:gosec // G302: the unsafe mode under test
				t.Fatal(err)
			}
			mgdRecord(t, own, filepath.Join(root, "tokens"))
		}, want: "has mode 750"},
		{name: "directory of another user", dir: "tokens", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if os.Geteuid() != 0 {
				t.Skip("needs root to own a directory by another user")
			}
			if err := os.Mkdir(filepath.Join(root, "tokens"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(filepath.Join(root, "tokens"), 12345, 12345); err != nil {
				t.Fatal(err)
			}
			mgdRecord(t, own, filepath.Join(root, "tokens"))
		}, want: "owned by uid 12345"},
		{name: "parent writable by others", dir: "tokens", setup: func(t *testing.T, root string, _ *fakeOwnership) {
			if err := os.Chmod(root, 0o707); err != nil { //nolint:gosec // G302: the unsafe mode under test
				t.Fatal(err)
			}
		}, want: "writable by group or others"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, own, root := mgdFiles(t)
			if c.setup != nil {
				c.setup(t, root, own)
			}
			dir := filepath.Join(root, c.dir)
			err := f.WriteManaged(context.Background(), "mgd", dir, "a.token", []byte("x"), 0o600, os.Geteuid(), os.Getegid())
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Errorf("WriteManaged = %v, want a refusal containing %q", err, c.want)
			}
			err = f.RemoveManaged(context.Background(), "mgd", dir, "a.token")
			if c.setup != nil && (err == nil || !strings.Contains(err.Error(), c.want)) {
				t.Errorf("RemoveManaged = %v, want a refusal containing %q", err, c.want)
			}
			for _, d := range []string{"real", "tokens"} {
				if _, err := os.Lstat(filepath.Join(root, d, "a.token")); !errors.Is(err, os.ErrNotExist) {
					t.Errorf("a refused write created %s/a.token", d)
				}
			}
		})
	}
}

// mgdRecord records dir as module mgd's managed directory.
func mgdRecord(t *testing.T, own *fakeOwnership, dir string) {
	t.Helper()
	if err := own.Record(context.Background(), core.OwnedEntry{Path: dir, Module: "mgd", Kind: core.OwnedManagedDir, CreatedAt: filesNow}); err != nil {
		t.Fatal(err)
	}
}

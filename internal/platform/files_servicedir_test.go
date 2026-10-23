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

// svdFiles returns a writer for module "svd", which declares <root>/derp as a
// service-dir of user "svd-user" with the subdirectory certs, <root>/ghost as
// a service-dir of user "ghost-user", and <root>/plain as a managed-dir.
// svd-user resolves to uid:gid; ghost-user does not exist.
func svdFiles(t *testing.T, uid, gid int) (*Files, *fakeOwnership, string) {
	t.Helper()
	root := t.TempDir()
	f, own := fpFiles(t, FilesConfig{
		Owned: map[string][]core.OwnedPath{"svd": {
			{Path: filepath.Join(root, "derp"), Kind: core.OwnedServiceDir, User: "svd-user", Subdirs: []string{"certs"}},
			{Path: filepath.Join(root, "ghost"), Kind: core.OwnedServiceDir, User: "ghost-user"},
			{Path: filepath.Join(root, "plain"), Kind: core.OwnedManagedDir},
		}},
		LookupUser: func(name string) (int, int, error) {
			if name != "svd-user" {
				return 0, 0, errors.New("unknown user")
			}
			return uid, gid, nil
		},
	})
	return f, own, root
}

// svdOwner returns the mode and owner of path, not following a symlink.
func svdOwner(t *testing.T, path string) (os.FileMode, int, int) {
	t.Helper()
	info, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	return info.Mode(), int(st.Uid), int(st.Gid)
}

// svdNeedRoot skips a test that must own files by another user.
func svdNeedRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("needs root to own files by another user")
	}
}

func TestEnsureServiceDir(t *testing.T) {
	uid, gid := os.Geteuid(), os.Getegid()
	f, own, root := svdFiles(t, uid, gid)
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	own.onRecord = func(e core.OwnedEntry) {
		if _, err := os.Lstat(e.Path); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s exists when it is recorded (%v); it must be recorded first", e.Path, err)
		}
	}
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatalf("EnsureServiceDir: %v", err)
	}
	own.onRecord = nil
	for _, p := range []string{dir, filepath.Join(dir, "certs")} {
		if mode, u, g := svdOwner(t, p); !mode.IsDir() || mode.Perm() != 0o700 || u != uid || g != gid {
			t.Errorf("%s: mode %v owner %d:%d, want a 0700 directory owned by %d:%d", p, mode, u, g, uid, gid)
		}
	}
	want := []core.OwnedEntry{{Path: dir, Module: "svd", Kind: core.OwnedServiceDir, CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
	if err := os.WriteFile(filepath.Join(dir, "derp.key"), []byte("k"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Errorf("EnsureServiceDir on the compliant directory: %v", err)
	}
	if err := os.Remove(filepath.Join(dir, "certs")); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Errorf("EnsureServiceDir with certs missing: %v", err)
	}
	if mode, _, _ := svdOwner(t, filepath.Join(dir, "certs")); !mode.IsDir() {
		t.Error("certs was not created again")
	}
}

func TestEnsureServiceDirHandsOverRootOwned(t *testing.T) {
	svdNeedRoot(t)
	f, own, root := svdFiles(t, 12345, 12346)
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	if err := os.Mkdir(dir, 0o755); err != nil { //nolint:gosec // G301: a mode the hand-over must correct
		t.Fatal(err)
	}
	if err := own.Record(ctx, core.OwnedEntry{Path: dir, Module: "svd", Kind: core.OwnedServiceDir, CreatedAt: filesNow}); err != nil {
		t.Fatal(err)
	}
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatalf("EnsureServiceDir: %v", err)
	}
	for _, p := range []string{dir, filepath.Join(dir, "certs")} {
		if mode, u, g := svdOwner(t, p); mode.Perm() != 0o700 || u != 12345 || g != 12346 {
			t.Errorf("%s: mode %o owner %d:%d, want 700 and 12345:12346", p, mode.Perm(), u, g)
		}
	}
}

func TestEnsureServiceDirRefuses(t *testing.T) {
	cases := []struct {
		name  string
		root  bool // needs root
		dir   string
		setup func(t *testing.T, root string, own *fakeOwnership)
		want  string
	}{
		{name: "undeclared", dir: "other", want: "does not declare"},
		{name: "kind managed-dir", dir: "plain", want: "does not declare"},
		{name: "unknown user", dir: "ghost", want: "look up user ghost-user"},
		{name: "pre-existing", dir: "derp", setup: func(t *testing.T, root string, _ *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "derp"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "is not Darkpanel's"},
		{name: "symlinked directory", dir: "derp", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", filepath.Join(root, "derp")); err != nil {
				t.Fatal(err)
			}
			svdRecord(t, own, filepath.Join(root, "derp"))
		}, want: "symbolic link"},
		{name: "parent of another user", root: true, dir: "derp", setup: func(t *testing.T, root string, _ *fakeOwnership) {
			if err := os.Chown(root, 12345, 12345); err != nil {
				t.Fatal(err)
			}
		}, want: "owned by uid 12345"},
		{name: "directory of another user", root: true, dir: "derp", setup: func(t *testing.T, root string, own *fakeOwnership) {
			svdMkdirOwned(t, filepath.Join(root, "derp"), 12345)
			svdRecord(t, own, filepath.Join(root, "derp"))
		}, want: "owned by uid 12345"},
		{name: "subdirectory of another user", root: true, dir: "derp", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "derp"), 0o700); err != nil {
				t.Fatal(err)
			}
			svdMkdirOwned(t, filepath.Join(root, "derp", "certs"), 12345)
			svdRecord(t, own, filepath.Join(root, "derp"))
		}, want: "owned by uid 12345"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.root {
				svdNeedRoot(t)
			}
			f, own, root := svdFiles(t, os.Geteuid(), os.Getegid())
			if c.setup != nil {
				c.setup(t, root, own)
			}
			err := f.EnsureServiceDir(context.Background(), "svd", filepath.Join(root, c.dir))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("EnsureServiceDir = %v, want a refusal containing %q", err, c.want)
			}
			if _, err := os.Lstat(filepath.Join(root, "real", "certs")); !errors.Is(err, os.ErrNotExist) {
				t.Error("a refused EnsureServiceDir created certs through the symlink")
			}
		})
	}
}

func TestServiceDirRenameRemove(t *testing.T) {
	uid, gid := os.Geteuid(), os.Getegid()
	f, _, root := svdFiles(t, uid, gid)
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"a.crt", "certs/a.crt", "2001:db8::1.crt"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, c := range [][2]string{{"a.crt", "b.crt"}, {"certs/a.crt", "certs/b.crt"}, {"2001:db8::1.crt", "2001:db8::2.crt"}} {
		if err := f.RenameIn(ctx, "svd", dir, c[0], c[1]); err != nil {
			t.Fatalf("RenameIn(%s, %s): %v", c[0], c[1], err)
		}
		if got := readFile(t, filepath.Join(dir, c[1])); got != c[0] {
			t.Errorf("%s holds %q, want the renamed %s", c[1], got, c[0])
		}
	}
	for _, rel := range []string{"b.crt", "certs/b.crt", "2001:db8::2.crt", "missing.crt", "certs/missing.crt"} {
		if err := f.RemoveIn(ctx, "svd", dir, rel); err != nil {
			t.Errorf("RemoveIn(%s): %v", rel, err)
		}
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"certs"}) {
		t.Errorf("directory holds %v, want only certs", names)
	}
	if names := dirNames(t, filepath.Join(dir, "certs")); len(names) != 0 {
		t.Errorf("certs holds %v, want nothing", names)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "certs", "l.crt")); err != nil {
		t.Fatal(err)
	}
	if err := f.RenameIn(ctx, "svd", dir, "certs/l.crt", "certs/m.crt"); err == nil {
		t.Error("RenameIn of a symlink succeeded")
	}
	if err := f.RemoveIn(ctx, "svd", dir, "certs/l.crt"); err != nil {
		t.Errorf("RemoveIn of a symlink: %v", err)
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
	if names := dirNames(t, filepath.Join(dir, "certs")); len(names) != 0 {
		t.Errorf("certs holds %v after the symlink was removed", names)
	}
}

func TestServiceDirNamesRefused(t *testing.T) {
	f, _, root := svdFiles(t, os.Geteuid(), os.Getegid())
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.crt"), []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"../x", "other/a.crt", "certs/../a.crt", ".hidden", "certs/sub/a.crt", ""} {
		if err := f.RenameIn(ctx, "svd", dir, "a.crt", rel); err == nil {
			t.Errorf("RenameIn(a.crt, %q) succeeded", rel)
		}
		if err := f.RemoveIn(ctx, "svd", dir, rel); err == nil {
			t.Errorf("RemoveIn(%q) succeeded", rel)
		}
	}
	if err := f.RenameIn(ctx, "svd", dir, "a.crt", "certs/a.crt"); err == nil {
		t.Error("RenameIn across directories succeeded")
	}
	if got := readFile(t, filepath.Join(dir, "a.crt")); got != "a" {
		t.Errorf("a.crt = %q after refusals", got)
	}
}

func TestServiceDirFileOfAnotherUser(t *testing.T) {
	svdNeedRoot(t)
	f, _, root := svdFiles(t, os.Geteuid(), os.Getegid())
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "certs", "a.crt")
	if err := os.WriteFile(path, []byte("a"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, 12345, 12345); err != nil {
		t.Fatal(err)
	}
	if err := f.RenameIn(ctx, "svd", dir, "certs/a.crt", "certs/b.crt"); err == nil || !strings.Contains(err.Error(), "owned by uid 12345") {
		t.Errorf("RenameIn = %v, want a refusal naming the owner", err)
	}
	if err := f.RemoveIn(ctx, "svd", dir, "certs/a.crt"); err == nil || !strings.Contains(err.Error(), "owned by uid 12345") {
		t.Errorf("RemoveIn = %v, want a refusal naming the owner", err)
	}
	if got := readFile(t, path); got != "a" {
		t.Errorf("the file of another user changed to %q", got)
	}
}

func TestRemoveServiceDir(t *testing.T) {
	f, own, root := svdFiles(t, os.Geteuid(), os.Getegid())
	ctx := context.Background()
	dir := filepath.Join(root, "derp")
	if err := f.RemoveServiceDir(ctx, "svd", dir); err != nil {
		t.Errorf("RemoveServiceDir of a missing directory: %v", err)
	}
	if err := f.EnsureServiceDir(ctx, "svd", dir); err != nil {
		t.Fatal(err)
	}
	victim := filepath.Join(root, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, rel := range []string{"derp.key", "certs/a.crt"} {
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(rel), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, rel := range []string{"link", "certs/link"} {
		if err := os.Symlink(victim, filepath.Join(dir, rel)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "extra"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "certs", "deeper"), 0o700); err != nil {
		t.Fatal(err)
	}
	err := f.RemoveServiceDir(ctx, "svd", dir)
	if err == nil || !strings.Contains(err.Error(), "extra") || !strings.Contains(err.Error(), "certs/deeper") {
		t.Fatalf("RemoveServiceDir = %v, want a refusal naming extra and certs/deeper", err)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"certs", "derp.key", "extra", "link"}) {
		t.Fatalf("a refused removal changed the directory to %v", names)
	}
	for _, d := range []string{filepath.Join(dir, "extra"), filepath.Join(dir, "certs", "deeper")} {
		if err := os.Remove(d); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.RemoveServiceDir(ctx, "svd", dir); err != nil {
		t.Fatalf("RemoveServiceDir: %v", err)
	}
	if _, err := os.Lstat(dir); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("directory still present: %v", err)
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want the record forgotten", got)
	}
	if err := f.RemoveServiceDir(ctx, "svd", dir); err != nil {
		t.Errorf("RemoveServiceDir again: %v", err)
	}
}

func TestRemoveServiceDirRefuses(t *testing.T) {
	cases := []struct {
		name  string
		dir   string
		setup func(t *testing.T, root string, own *fakeOwnership)
		want  string
	}{
		{name: "undeclared", dir: "other", want: "does not declare"},
		{name: "not recorded", dir: "derp", setup: func(t *testing.T, root string, _ *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "derp"), 0o700); err != nil {
				t.Fatal(err)
			}
		}, want: "is not Darkpanel's"},
		{name: "symlinked directory", dir: "derp", setup: func(t *testing.T, root string, own *fakeOwnership) {
			if err := os.Mkdir(filepath.Join(root, "real"), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("real", filepath.Join(root, "derp")); err != nil {
				t.Fatal(err)
			}
			svdRecord(t, own, filepath.Join(root, "derp"))
		}, want: "symbolic link"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f, own, root := svdFiles(t, os.Geteuid(), os.Getegid())
			if c.setup != nil {
				c.setup(t, root, own)
			}
			err := f.RemoveServiceDir(context.Background(), "svd", filepath.Join(root, c.dir))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("RemoveServiceDir = %v, want a refusal containing %q", err, c.want)
			}
			if _, err := os.Lstat(filepath.Join(root, "derp")); c.setup != nil && err != nil {
				t.Errorf("a refused removal removed derp: %v", err)
			}
		})
	}
}

// svdRecord records dir as module svd's service directory.
func svdRecord(t *testing.T, own *fakeOwnership, dir string) {
	t.Helper()
	if err := own.Record(context.Background(), core.OwnedEntry{Path: dir, Module: "svd", Kind: core.OwnedServiceDir, CreatedAt: filesNow}); err != nil {
		t.Fatal(err)
	}
}

// svdMkdirOwned makes the directory path with mode 0700, owned by uid.
func svdMkdirOwned(t *testing.T, path string, uid int) {
	t.Helper()
	if err := os.Mkdir(path, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(path, uid, uid); err != nil {
		t.Fatal(err)
	}
}

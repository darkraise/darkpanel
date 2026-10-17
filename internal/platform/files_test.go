package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"golang.org/x/sys/unix"
)

// fakeOwnership is a core.Ownership in memory.
type fakeOwnership struct {
	mu      sync.Mutex
	entries []core.OwnedEntry
}

func (o *fakeOwnership) Record(_ context.Context, e core.OwnedEntry) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.ContainsFunc(o.entries, func(x core.OwnedEntry) bool { return x.Path == e.Path && x.BlockID == e.BlockID }) {
		o.entries = append(o.entries, e)
	}
	return nil
}

func (o *fakeOwnership) Forget(_ context.Context, module, path, blockID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = slices.DeleteFunc(o.entries, func(x core.OwnedEntry) bool {
		return x.Module == module && x.Path == path && x.BlockID == blockID
	})
	return nil
}

func (o *fakeOwnership) List(context.Context) ([]core.OwnedEntry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.entries), nil
}

var filesNow = time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)

// newTestFiles returns a writer in TestMode for module "demo", which owns
// <dir>/app.conf as a file, block "names" of <dir>/hosts and block "rules" of
// <dir>/rules.conf with comment prefix "//".
func newTestFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	dir := t.TempDir()
	own := &fakeOwnership{}
	f := NewFiles(FilesConfig{
		Owned: map[string][]core.OwnedPath{"demo": {
			{Path: filepath.Join(dir, "app.conf"), Kind: "file"},
			{Path: filepath.Join(dir, "hosts"), Kind: "block", BlockIDs: []string{"names"}},
			{Path: filepath.Join(dir, "rules.conf"), Kind: "block", BlockIDs: []string{"rules"}, CommentPrefix: "//"},
		}},
		Ownership: own,
		Clock:     coretest.NewFakeClock(filesNow),
		TestMode:  true,
	})
	return f, own, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test's temporary path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteFileCreatesAndRecords(t *testing.T) {
	f, own, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := f.WriteFile(context.Background(), "demo", path, []byte("a=1\n"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := readFile(t, path); got != "a=1\n" {
		t.Errorf("content = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o640 {
		t.Errorf("mode = %o, want 640", mode)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"app.conf"}) {
		t.Errorf("directory holds %v, want only app.conf (no temporary file left)", names)
	}
	want := []core.OwnedEntry{{Path: path, Module: "demo", Kind: "file", CreatedAt: filesNow}}
	if got, _ := own.List(context.Background()); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
}

func TestWriteFileKeepsModeAndOwner(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o604); err != nil { //nolint:gosec // G302: an unusual mode the write must keep
		t.Fatal(err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 12345, 12346
		if err := os.Chown(path, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.WriteFile(context.Background(), "demo", path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0o604 || int(st.Uid) != uid || int(st.Gid) != gid {
		t.Errorf("mode %o owner %d:%d, want 604 and %d:%d", info.Mode().Perm(), st.Uid, st.Gid, uid, gid)
	}
	if got := readFile(t, path); got != "new\n" {
		t.Errorf("content = %q", got)
	}
}

func TestUndeclaredWritesRefused(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	app, hosts := filepath.Join(dir, "app.conf"), filepath.Join(dir, "hosts")
	attempts := map[string]error{
		"other path":        f.WriteFile(ctx, "demo", filepath.Join(dir, "other.conf"), []byte("x"), 0o644),
		"other module":      f.WriteFile(ctx, "other", app, []byte("x"), 0o644),
		"unclean path":      f.WriteFile(ctx, "demo", dir+"/./app.conf", []byte("x"), 0o644),
		"file as block":     f.ReplaceBlock(ctx, "demo", app, "names", []byte("x")),
		"block as file":     f.WriteFile(ctx, "demo", hosts, []byte("x"), 0o644),
		"undeclared block":  f.ReplaceBlock(ctx, "demo", hosts, "other", []byte("x")),
		"remove undeclared": f.Remove(ctx, "demo", hosts),
	}
	for name, err := range attempts {
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", name)
		}
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Errorf("directory holds %v, want nothing written", names)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestSymlinkedParentRefused(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "link", "app.conf")
	f := NewFiles(FilesConfig{
		Owned:     map[string][]core.OwnedPath{"demo": {{Path: path, Kind: "file"}}},
		Ownership: &fakeOwnership{}, Clock: coretest.NewFakeClock(filesNow), TestMode: true,
	})
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile through a symlinked parent succeeded")
	}
	if names := dirNames(t, realDir); len(names) != 0 {
		t.Errorf("real directory holds %v, want nothing", names)
	}
}

func TestSymlinkTargetRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "app.conf")); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(context.Background(), "demo", filepath.Join(dir, "app.conf"), []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a symlink succeeded")
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
}

func TestNonRegularTargetRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a FIFO succeeded")
	}
}

func TestUnsafeParentRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	for _, mode := range []os.FileMode{0o770, 0o707} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
			t.Errorf("parent mode %o: WriteFile succeeded, want a refusal", mode)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its search bit
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, 12345, 12345); err != nil {
			t.Fatal(err)
		}
		if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
			t.Error("parent owned by another user: WriteFile succeeded, want a refusal")
		}
	}
}

func TestProductionModeRequiresRootParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: every temporary directory is owned by root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.conf")
	f := NewFiles(FilesConfig{
		Owned:     map[string][]core.OwnedPath{"demo": {{Path: path, Kind: "file"}}},
		Ownership: &fakeOwnership{}, Clock: coretest.NewFakeClock(filesNow),
	})
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile into a directory not owned by root succeeded")
	}
}

func TestBlocks(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	hosts := filepath.Join(dir, "hosts")
	if err := f.ReplaceBlock(ctx, "demo", hosts, "names", []byte("10.0.0.1 a\n")); err != nil {
		t.Fatalf("ReplaceBlock (create): %v", err)
	}
	info, err := os.Stat(hosts)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("new block file mode = %o, want 644", info.Mode().Perm())
	}
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"+readFile(t, hosts)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.ReplaceBlock(ctx, "demo", hosts, "names", []byte("10.0.0.2 b\n")); err != nil {
		t.Fatalf("ReplaceBlock (replace): %v", err)
	}
	want := "127.0.0.1 localhost\n# BEGIN DARKPANEL demo/names\n10.0.0.2 b\n# END DARKPANEL demo/names\n"
	if got := readFile(t, hosts); got != want {
		t.Errorf("hosts = %q, want %q", got, want)
	}
	wantOwn := []core.OwnedEntry{{Path: hosts, Module: "demo", Kind: "block", BlockID: "names", CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, wantOwn) {
		t.Errorf("ownership = %+v, want %+v", got, wantOwn)
	}
	if err := f.RemoveBlock(ctx, "demo", hosts, "names"); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}
	if got := readFile(t, hosts); got != "127.0.0.1 localhost\n" {
		t.Errorf("after RemoveBlock: %q", got)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership after RemoveBlock = %+v, want none", got)
	}
	if err := f.RemoveBlock(ctx, "demo", filepath.Join(dir, "rules.conf"), "rules"); err != nil {
		t.Errorf("RemoveBlock of a missing file: %v", err)
	}
}

func TestBlockCommentPrefix(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "rules.conf")
	if err := f.ReplaceBlock(context.Background(), "demo", path, "rules", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), "// BEGIN DARKPANEL demo/rules\nx\n// END DARKPANEL demo/rules\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestInvalidMarkersLeaveFileUnchanged(t *testing.T) {
	f, own, dir := newTestFiles(t)
	hosts := filepath.Join(dir, "hosts")
	bad := "# BEGIN DARKPANEL demo/names\nx\n"
	if err := os.WriteFile(hosts, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.ReplaceBlock(context.Background(), "demo", hosts, "names", []byte("y\n"))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.FilesMarkersBad {
		t.Errorf("err = %v, want %s", err, errcode.FilesMarkersBad)
	}
	if got := readFile(t, hosts); got != bad {
		t.Errorf("file changed to %q", got)
	}
	if got, _ := own.List(context.Background()); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestRemove(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	path := filepath.Join(dir, "app.conf")
	if err := f.WriteFile(ctx, "demo", path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "demo", path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present: %v", err)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want none", got)
	}
	if err := f.Remove(ctx, "demo", path); err != nil {
		t.Errorf("Remove of a missing file: %v", err)
	}
}

func TestOwnedPaths(t *testing.T) {
	a := core.OwnedPath{Path: "/etc/a", Kind: "file"}
	got := OwnedPaths([]core.Manifest{{ID: "one", OwnedPaths: []core.OwnedPath{a}}, {ID: "two"}})
	if want := map[string][]core.OwnedPath{"one": {a}}; !reflect.DeepEqual(got, want) {
		t.Errorf("OwnedPaths = %+v, want %+v", got, want)
	}
}

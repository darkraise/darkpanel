package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"golang.org/x/sys/unix"
)

// fakeOwnership is a core.Ownership in memory. onRecord, when set, sees each
// entry before Record stores it, so a test can check what the host looked like
// at that moment.
type fakeOwnership struct {
	mu       sync.Mutex
	entries  []core.OwnedEntry
	onRecord func(e core.OwnedEntry)
}

func (o *fakeOwnership) Record(_ context.Context, e core.OwnedEntry) error {
	if o.onRecord != nil {
		o.onRecord(e)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	recorded := slices.ContainsFunc(o.entries, func(x core.OwnedEntry) bool {
		return x.Path == e.Path && x.Module == e.Module && x.BlockID == e.BlockID
	})
	if !recorded {
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

func (o *fakeOwnership) Owns(_ context.Context, module, path string) (bool, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.ContainsFunc(o.entries, func(x core.OwnedEntry) bool { return x.Module == module && x.Path == path }), nil
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

// fpFiles returns a writer in TestMode over cfg with a new in-memory
// ownership store, which it also returns, and the fixed clock unless cfg sets
// one.
func fpFiles(t *testing.T, cfg FilesConfig) (*Files, *fakeOwnership) {
	t.Helper()
	own := &fakeOwnership{}
	cfg.Ownership = own
	if cfg.Clock == nil {
		cfg.Clock = coretest.NewFakeClock(filesNow)
	}
	cfg.TestMode = true
	return NewFiles(cfg), own
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

// Directories and users are uninstall's to remove: Files neither writes nor
// removes them, and holds no lock for them.
func TestOtherKindsIgnored(t *testing.T) {
	dir := t.TempDir()
	own := &fakeOwnership{}
	data := filepath.Join(dir, "data")
	f := NewFiles(FilesConfig{
		Owned: map[string][]core.OwnedPath{"demo": {
			{Path: data, Kind: core.OwnedDir},
			{Path: "demo-user", Kind: core.OwnedUser},
		}},
		Ownership: own,
		Clock:     coretest.NewFakeClock(filesNow),
		TestMode:  true,
	})
	ctx := context.Background()
	attempts := map[string]error{
		"write a directory":  f.WriteFile(ctx, "demo", data, []byte("x"), 0o644),
		"remove a directory": f.Remove(ctx, "demo", data),
		"write a user":       f.WriteFile(ctx, "demo", "demo-user", []byte("x"), 0o644),
	}
	for name, err := range attempts {
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", name)
		}
	}
	if len(f.locks) != 0 {
		t.Errorf("Files holds %d path locks, want none", len(f.locks))
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

func TestConcurrentBlocksInOneFile(t *testing.T) {
	dir := t.TempDir()
	hosts := filepath.Join(dir, "hosts")
	decl := []core.OwnedPath{{Path: hosts, Kind: "block", BlockIDs: []string{"names"}}}
	own := &fakeOwnership{}
	f := NewFiles(FilesConfig{
		Owned:     map[string][]core.OwnedPath{"demo": decl, "other": decl},
		Ownership: own, Clock: coretest.NewFakeClock(filesNow), TestMode: true,
	})
	ctx := context.Background()
	modules := []string{"demo", "other"}
	for round := range 30 {
		errs := make([]error, len(modules))
		var wg sync.WaitGroup
		for i, module := range modules {
			wg.Go(func() {
				errs[i] = f.ReplaceBlock(ctx, module, hosts, "names", fmt.Appendf(nil, "%s %d\n", module, round))
			})
		}
		wg.Wait()
		if err := errors.Join(errs...); err != nil {
			t.Fatalf("round %d: %v", round, err)
		}
		got := readFile(t, hosts)
		for _, module := range modules {
			if !strings.Contains(got, fmt.Sprintf("%s %d\n", module, round)) {
				t.Fatalf("round %d: %s's block lost: %q", round, module, got)
			}
		}
	}
	entries, err := own.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var recorded []string
	for _, e := range entries {
		recorded = append(recorded, e.Module)
	}
	slices.Sort(recorded)
	if !slices.Equal(recorded, modules) {
		t.Errorf("ownership modules = %v, want %v", recorded, modules)
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

func TestRemoveWithParentGone(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	file, hosts := filepath.Join(gone, "app.conf"), filepath.Join(gone, "hosts")
	own := &fakeOwnership{}
	f := NewFiles(FilesConfig{
		Owned: map[string][]core.OwnedPath{"demo": {
			{Path: file, Kind: "file"},
			{Path: hosts, Kind: "block", BlockIDs: []string{"names"}},
		}},
		Ownership: own, Clock: coretest.NewFakeClock(filesNow), TestMode: true,
	})
	ctx := context.Background()
	if err := os.Mkdir(gone, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "demo", file, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.ReplaceBlock(ctx, "demo", hosts, "names", []byte("y\n")); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(gone); err != nil {
		t.Fatal(err)
	}
	// A symlink in place of the directory is refused, not taken for missing.
	if err := os.Symlink(t.TempDir(), gone); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "demo", file); err == nil {
		t.Error("Remove through a symlinked parent succeeded")
	}
	if err := f.RemoveBlock(ctx, "demo", hosts, "names"); err == nil {
		t.Error("RemoveBlock through a symlinked parent succeeded")
	}
	if got, _ := own.List(ctx); len(got) != 2 {
		t.Fatalf("ownership after refusals = %+v, want both entries kept", got)
	}
	if err := os.Remove(gone); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "demo", file); err != nil {
		t.Errorf("Remove with the parent gone: %v", err)
	}
	if err := f.RemoveBlock(ctx, "demo", hosts, "names"); err != nil {
		t.Errorf("RemoveBlock with the parent gone: %v", err)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want none", got)
	}
}

func TestOwnedPaths(t *testing.T) {
	a := core.OwnedPath{Path: "/etc/a", Kind: "file"}
	got := OwnedPaths([]core.Manifest{{ID: "one", OwnedPaths: []core.OwnedPath{a}}, {ID: "two"}})
	if want := map[string][]core.OwnedPath{"one": {a}}; !reflect.DeepEqual(got, want) {
		t.Errorf("OwnedPaths = %+v, want %+v", got, want)
	}
}

func TestFilesOwns(t *testing.T) {
	f, own := fpFiles(t, FilesConfig{})
	ctx := context.Background()
	if err := own.Record(ctx, core.OwnedEntry{Path: "/etc/fp.conf", Module: "fp", Kind: core.OwnedFile, CreatedAt: filesNow}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		module, path string
		want         bool
	}{
		{"fp", "/etc/fp.conf", true},
		{"other", "/etc/fp.conf", false},
		{"fp", "/etc/other.conf", false},
	}
	for _, c := range cases {
		if got, err := f.Owns(ctx, c.module, c.path); err != nil || got != c.want {
			t.Errorf("Owns(%s, %s) = %v, %v; want %v", c.module, c.path, got, err, c.want)
		}
	}
}

func TestWriteFileUnitKind(t *testing.T) {
	dir := t.TempDir()
	unit, pre, conf := filepath.Join(dir, "darkpanel-fp.service"), filepath.Join(dir, "darkpanel-pre.service"), filepath.Join(dir, "fp.conf")
	f, own := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"fp": {
		{Path: unit, Kind: core.OwnedUnit},
		{Path: pre, Kind: core.OwnedUnit},
		{Path: conf, Kind: core.OwnedFile},
	}}})
	ctx := context.Background()
	if err := f.WriteFile(ctx, "fp", unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatalf("WriteFile(unit): %v", err)
	}
	if got := readFile(t, unit); got != "[Unit]\n" {
		t.Errorf("unit content = %q", got)
	}
	info, err := os.Stat(unit)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("unit mode = %o, want 644", info.Mode().Perm())
	}
	if err := f.WriteFile(ctx, "fp", unit, []byte("[Unit]\nDescription=x\n"), 0o644); err != nil {
		t.Errorf("rewriting the recorded unit: %v", err)
	}
	if err := f.WriteFile(ctx, "fp", conf, []byte("a=1\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(file): %v", err)
	}
	if err := os.WriteFile(pre, []byte("[Unit]\nDescription=not ours\n"), 0o644); err != nil { //nolint:gosec // G306: a unit file's usual mode
		t.Fatal(err)
	}
	err = f.WriteFile(ctx, "fp", pre, []byte("x"), 0o644)
	if err == nil || !strings.Contains(err.Error(), pre) {
		t.Errorf("WriteFile over a unit file Darkpanel did not write = %v, want a refusal naming it", err)
	}
	if got := readFile(t, pre); got != "[Unit]\nDescription=not ours\n" {
		t.Errorf("the pre-existing unit file changed to %q", got)
	}
	if err := f.WriteFile(ctx, "fp", filepath.Join(dir, "darkpanel-other.service"), []byte("x"), 0o644); err == nil {
		t.Error("WriteFile of an undeclared unit succeeded")
	}
	want := []core.OwnedEntry{
		{Path: unit, Module: "fp", Kind: core.OwnedUnit, CreatedAt: filesNow},
		{Path: conf, Module: "fp", Kind: core.OwnedFile, CreatedAt: filesNow},
	}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
}

func TestRemoveUnitKind(t *testing.T) {
	dir := t.TempDir()
	unit := filepath.Join(dir, "darkpanel-fp.service")
	f, own := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"fp": {{Path: unit, Kind: core.OwnedUnit}}}})
	ctx := context.Background()
	if err := f.WriteFile(ctx, "fp", unit, []byte("[Unit]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "fp", unit); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(unit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("unit file still present: %v", err)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want none", got)
	}
	if err := f.Remove(ctx, "fp", unit); err != nil {
		t.Errorf("Remove of a missing unit file: %v", err)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, unit); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "fp", unit); err != nil {
		t.Fatalf("Remove of a symlink: %v", err)
	}
	if _, err := os.Lstat(unit); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("symlink still present: %v", err)
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
}

func TestRename(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	bin, next, prev, other := filepath.Join(dir, "derper"), filepath.Join(dir, "derper.new"), filepath.Join(dir, "derper.prev"), filepath.Join(sub, "derper")
	undeclared := filepath.Join(dir, "undeclared")
	f, own := fpFiles(t, FilesConfig{Owned: map[string][]core.OwnedPath{"fp": {
		{Path: bin, Kind: core.OwnedFile}, {Path: next, Kind: core.OwnedFile},
		{Path: prev, Kind: core.OwnedFile}, {Path: other, Kind: core.OwnedFile},
	}}})
	ctx := context.Background()
	if err := f.WriteFile(ctx, "fp", bin, []byte("old"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(ctx, "fp", next, []byte("new"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(undeclared, []byte("u"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(undeclared, prev); err != nil {
		t.Fatal(err)
	}
	refused := []struct {
		name, from, to, names string
	}{
		{"another directory", bin, other, other},
		{"undeclared source", undeclared, bin, undeclared},
		{"undeclared target", bin, undeclared, undeclared},
		{"symlinked source", prev, bin, prev},
		{"onto itself", bin, bin, bin},
	}
	for _, c := range refused {
		err := f.Rename(ctx, "fp", c.from, c.to)
		if err == nil || !strings.Contains(err.Error(), c.names) {
			t.Errorf("%s: Rename = %v, want a refusal naming %s", c.name, err, c.names)
		}
	}
	if err := os.Remove(prev); err != nil {
		t.Fatal(err)
	}
	if err := f.Rename(ctx, "fp", prev, bin); err == nil || !strings.Contains(err.Error(), prev) {
		t.Errorf("missing source: Rename = %v, want a refusal naming %s", err, prev)
	}
	if got := readFile(t, bin); got != "old" {
		t.Fatalf("a refused rename changed %s to %q", bin, got)
	}
	if err := f.Rename(ctx, "fp", next, bin); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if got := readFile(t, bin); got != "new" {
		t.Errorf("%s = %q, want the renamed content", bin, got)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"derper", "sub", "undeclared"}) {
		t.Errorf("directory holds %v", names)
	}
	want := []core.OwnedEntry{{Path: bin, Module: "fp", Kind: core.OwnedFile, CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v (the target recorded, the source forgotten)", got, want)
	}
}

func TestWriteFileAt(t *testing.T) {
	dir := t.TempDir()
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 12345, 12346
	}
	path := filepath.Join(dir, "a")
	if err := writeFileAt(fd, "a", path, []byte("data"), 0o640, uid, gid); err != nil {
		t.Fatalf("writeFileAt: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if readFile(t, path) != "data" || info.Mode().Perm() != 0o640 || int(st.Uid) != uid || int(st.Gid) != gid {
		t.Errorf("wrote %q mode %o owner %d:%d, want data, 640, %d:%d", readFile(t, path), info.Mode().Perm(), st.Uid, st.Gid, uid, gid)
	}
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAt(fd, "link", filepath.Join(dir, "link"), []byte("x"), 0o600, uid, gid); err == nil {
		t.Error("writeFileAt over a symlink succeeded")
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeFileAt(fd, "sub", filepath.Join(dir, "sub"), []byte("x"), 0o600, uid, gid); err == nil {
		t.Error("writeFileAt over a directory succeeded")
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"a", "link", "sub", "victim"}) {
		t.Errorf("directory holds %v, want no temporary file left", names)
	}
}

func TestOpenBeneath(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "real"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "real", "g"), []byte("g"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("real", filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("g", filepath.Join(dir, "real", "h")); err != nil {
		t.Fatal(err)
	}
	fd, err := unix.Open(dir, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	cases := []struct {
		name string
		want error // nil: the open succeeds
	}{
		{"real/g", nil},
		{"link/g", unix.ELOOP},
		{"real/h", unix.ELOOP},
		{"../" + filepath.Base(dir), unix.EXDEV},
		{"/etc", unix.EXDEV},
		{"real/missing", unix.ENOENT},
	}
	for _, c := range cases {
		got, err := openBeneath(fd, c.name, unix.O_RDONLY, 0)
		if err == nil {
			_ = unix.Close(got)
		}
		if !errors.Is(err, c.want) {
			t.Errorf("openBeneath(%s) = %v, want %v", c.name, err, c.want)
		}
	}
}

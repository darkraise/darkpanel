package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func duWriteFile(t *testing.T, ops Ops, path, data string) {
	t.Helper()
	f, err := ops.Create(path, 0o600)
	if err != nil {
		t.Fatalf("Create %s: %v", path, err)
	}
	if _, err := f.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func duRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDuOSOpsCreate(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	p := filepath.Join(dir, "staged")
	duWriteFile(t, ops, p, "hello")
	st, err := os.Stat(p)
	if err != nil || st.Mode() != 0o600 || duRead(t, p) != "hello" {
		t.Fatalf("created file: %v, %v, %q; want mode 0600 holding hello", st, err, duRead(t, p))
	}

	if f, err := ops.Create(p, 0o600); !errors.Is(err, fs.ErrExist) || f != nil {
		t.Errorf("Create over an existing file = %v, %v; want fs.ErrExist and no file", f, err)
	}
	if duRead(t, p) != "hello" {
		t.Error("a refused Create changed the existing file")
	}

	target := filepath.Join(dir, "victim")
	for name, setup := range map[string]func(link string){
		"a dangling link":  func(link string) { _ = os.Symlink(target, link) },
		"a link to a file": func(link string) { duWriteFile(t, ops, target, "keep"); _ = os.Symlink(target, link) },
	} {
		link := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		setup(link)
		if f, err := ops.Create(link, 0o600); err == nil {
			_ = f.Close()
			t.Errorf("Create through %s succeeded", name)
		}
	}
	if _, err := os.Stat(target); err == nil && duRead(t, target) != "keep" {
		t.Error("Create wrote through a symbolic link")
	}

	if f, err := ops.Create(filepath.Join(dir, "missing", "file"), 0o600); !errors.Is(err, fs.ErrNotExist) || f != nil {
		t.Errorf("Create in a missing directory = %v, %v; want fs.ErrNotExist", f, err)
	}
}

func TestDuOSOpsRenameAndRemove(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	duWriteFile(t, ops, a, "A")
	duWriteFile(t, ops, b, "B")
	if err := ops.Rename(a, b); err != nil || duRead(t, b) != "A" {
		t.Fatalf("Rename over an existing name = %v, target holds %q; want it replaced by A", err, duRead(t, b))
	}
	if _, err := os.Lstat(a); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the source still exists after Rename: %v", err)
	}
	if err := ops.Rename(a, b); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename of a missing source = %v, want fs.ErrNotExist", err)
	}

	if err := ops.Remove(b); err != nil {
		t.Errorf("Remove of a file = %v", err)
	}
	if err := ops.Remove(b); err != nil {
		t.Errorf("Remove of a missing path = %v, want nil", err)
	}
	keep := filepath.Join(dir, "keep")
	duWriteFile(t, ops, keep, "kept")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(keep, link); err != nil {
		t.Fatal(err)
	}
	if err := ops.Remove(link); err != nil || duRead(t, keep) != "kept" {
		t.Errorf("Remove of a link = %v; the target must stay", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link survived Remove: %v", err)
	}
	full := filepath.Join(dir, "full")
	if err := os.Mkdir(full, 0o700); err != nil {
		t.Fatal(err)
	}
	duWriteFile(t, ops, filepath.Join(full, "x"), "x")
	if err := ops.Remove(full); err == nil {
		t.Error("Remove deleted a directory that holds files")
	}
}

func TestDuOSOpsDirectoryAndMetadata(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	if err := ops.SyncDir(dir); err != nil {
		t.Errorf("SyncDir of a directory = %v", err)
	}
	if err := ops.SyncDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SyncDir of a missing directory = %v, want fs.ErrNotExist", err)
	}

	p := filepath.Join(dir, "f")
	duWriteFile(t, ops, p, "x")
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}

	for _, tt := range []struct {
		name, path string
		want       bool
	}{
		{"a file", p, true}, {"a directory", dir, true}, {"a dangling link", dangling, true}, {"a missing path", filepath.Join(dir, "nope"), false},
	} {
		if got, err := ops.Exists(tt.path); err != nil || got != tt.want {
			t.Errorf("Exists(%s) = %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}

// duScript makes one call of each kind in a fixed order and stops at the
// first error: Create, Write, Sync, Close, Rename, Exists, Secure, SyncDir,
// Remove.
func duScript(ops Ops, dir string) error {
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	f, err := ops.Create(a, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ops.Rename(a, b); err != nil {
		return err
	}
	if _, err := ops.Exists(b); err != nil {
		return err
	}
	if err := ops.Secure(b, os.Getuid(), os.Getgid(), 0o640); err != nil {
		return err
	}
	if err := ops.SyncDir(dir); err != nil {
		return err
	}
	return ops.Remove(b)
}

const duScriptCalls = 9

// duState lists dir as sorted "name=content:mode" words.
func duState(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e.Name()+"="+duRead(t, filepath.Join(dir, e.Name()))+":"+info.Mode().Perm().String())
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestDuCrashOpsCountsWithoutCrashing(t *testing.T) {
	for _, at := range []int{0, duScriptCalls + 1, 1 << 30} {
		dir := t.TempDir()
		c := newCrashOps(OSOps(), at, true)
		if err := duScript(c, dir); err != nil {
			t.Fatalf("at %d: the script failed: %v", at, err)
		}
		if c.Calls() != duScriptCalls || c.Crashed() {
			t.Errorf("at %d: Calls = %d, Crashed = %v; want %d and false (every Ops and File method counts)", at, c.Calls(), c.Crashed(), duScriptCalls)
		}
		if s := duState(t, dir); s != "" {
			t.Errorf("at %d: the script left %q", at, s)
		}
	}
}

func TestDuCrashOpsCountsFilesOfSeveralCreates(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 0, true)
	f1, err := c.Create(filepath.Join(dir, "1"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := c.Create(filepath.Join(dir, "2"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []File{f1, f2} {
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if c.Calls() != 8 {
		t.Errorf("Calls = %d, want 8: two Creates and three File calls on each file", c.Calls())
	}
}

func TestDuCrashOpsErrorsOfTheInnerOpsPass(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 0, true)
	if err := c.Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "to")); !errors.Is(err, fs.ErrNotExist) || errors.Is(err, errCrash) {
		t.Errorf("Rename of a missing file = %v, want the inner fs.ErrNotExist", err)
	}
	if c.Calls() != 1 || c.Crashed() {
		t.Errorf("Calls = %d, Crashed = %v; a failing call still counts and is no crash", c.Calls(), c.Crashed())
	}
}

// TestDuCrashOpsEveryCall crashes the script on each of its calls, before
// and after, and checks what the disk holds: a call that crashed before ran
// has no effect, one that crashed after ran has its full effect.
func TestDuCrashOpsEveryCall(t *testing.T) {
	type outcome struct{ before, after string }
	want := map[int]outcome{
		1: {"", "a=:-rw-------"},
		2: {"a=:-rw-------", "a=data:-rw-------"},
		3: {"a=data:-rw-------", "a=data:-rw-------"},
		4: {"a=data:-rw-------", "a=data:-rw-------"},
		5: {"a=data:-rw-------", "b=data:-rw-------"},
		6: {"b=data:-rw-------", "b=data:-rw-------"},
		7: {"b=data:-rw-------", "b=data:-rw-r-----"},
		8: {"b=data:-rw-r-----", "b=data:-rw-r-----"},
		9: {"b=data:-rw-r-----", ""},
	}
	for k := 1; k <= duScriptCalls; k++ {
		for _, before := range []bool{true, false} {
			dir := t.TempDir()
			c := newCrashOps(OSOps(), k, before)
			err := duScript(c, dir)
			if !errors.Is(err, errCrash) || !c.Crashed() || c.Calls() != k {
				t.Errorf("call %d before=%v: err %v, Crashed %v, Calls %d; want errCrash, true, %d", k, before, err, c.Crashed(), c.Calls(), k)
			}
			w := want[k].after
			if before {
				w = want[k].before
			}
			if got := duState(t, dir); got != w {
				t.Errorf("call %d before=%v: the directory holds %q, want %q", k, before, got, w)
			}
		}
	}
}

// TestDuCrashOpsDeadAfterCrash: after the crash nothing runs, whatever the
// method, and the count stays where it stopped.
func TestDuCrashOpsDeadAfterCrash(t *testing.T) {
	dir := t.TempDir()
	a, b, later := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "later")
	c := newCrashOps(OSOps(), 3, false)
	f, err := c.Create(a, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); !errors.Is(err, errCrash) {
		t.Fatalf("the crashing Sync = %v, want errCrash", err)
	}
	if _, err := f.Write([]byte("after")); !errors.Is(err, errCrash) {
		t.Errorf("Write after the crash = %v, want errCrash", err)
	}
	if err := f.Sync(); !errors.Is(err, errCrash) {
		t.Errorf("Sync after the crash = %v, want errCrash", err)
	}
	if err := f.Close(); !errors.Is(err, errCrash) {
		t.Errorf("Close after the crash = %v, want errCrash", err)
	}
	if g, err := c.Create(later, 0o600); !errors.Is(err, errCrash) || g != nil {
		t.Errorf("Create after the crash = %v, %v; want no file and errCrash", g, err)
	}
	if err := c.Rename(a, b); !errors.Is(err, errCrash) {
		t.Errorf("Rename after the crash = %v, want errCrash", err)
	}
	if err := c.Remove(a); !errors.Is(err, errCrash) {
		t.Errorf("Remove after the crash = %v, want errCrash", err)
	}
	if err := c.SyncDir(dir); !errors.Is(err, errCrash) {
		t.Errorf("SyncDir after the crash = %v, want errCrash", err)
	}
	if err := c.Secure(a, os.Getuid(), os.Getgid(), 0o644); !errors.Is(err, errCrash) {
		t.Errorf("Secure after the crash = %v, want errCrash", err)
	}
	if ok, err := c.Exists(a); !errors.Is(err, errCrash) || ok {
		t.Errorf("Exists after the crash = %v, %v; want false and errCrash", ok, err)
	}
	if got := duState(t, dir); got != "a=before:-rw-------" {
		t.Errorf("the directory holds %q after the crash, want only a holding what was written before it", got)
	}
	if c.Calls() != 3 || !c.Crashed() {
		t.Errorf("Calls = %d, Crashed = %v; want 3 and true", c.Calls(), c.Crashed())
	}
}

// TestDuCrashOpsCrashingCreateGivesNoFile: a Create that crashes after it
// ran leaves the empty file on disk and hands the caller nothing.
func TestDuCrashOpsCrashingCreateGivesNoFile(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 1, false)
	f, err := c.Create(filepath.Join(dir, "a"), 0o600)
	if f != nil || !errors.Is(err, errCrash) {
		t.Errorf("Create = %v, %v; want no file and errCrash", f, err)
	}
	if got := duState(t, dir); got != "a=:-rw-------" {
		t.Errorf("the directory holds %q, want the empty file", got)
	}
}

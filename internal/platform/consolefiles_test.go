package platform

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const consoleBinary = "/usr/local/bin/darkpanel"

// newConsoleRoot builds a temporary root holding a file x in every
// protected directory, the binary, and /srv with a file, a symlinked
// directory into /etc/darkpanel and a symlinked file.
func newConsoleRoot(t *testing.T) (string, *ConsoleFiles) {
	t.Helper()
	root := t.TempDir()
	for _, d := range []string{"proc", "sys", "dev", "run", "boot/efi", "etc/darkpanel", "var/lib/darkpanel", "usr/local/bin", "srv", "tmp"} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, d, "x"), []byte("keep "+d), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for name, data := range map[string]string{"usr/local/bin/darkpanel": "binary", "srv/target.txt": "target", "tmp/y": "y"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink("../etc/darkpanel", filepath.Join(root, "srv", "link")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(root, "srv", "file-link")); err != nil {
		t.Fatal(err)
	}
	return root, NewConsoleFiles(ConsoleFilesConfig{Root: root, Binary: consoleBinary, FreeSpace: func(int) (int64, error) { return 1 << 40, nil }})
}

// snapshot records every path below root with its mode and content.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		entry := info.Mode().String()
		if info.Mode().IsRegular() {
			b, err := os.ReadFile(p) //nolint:gosec // G304: a path below the test's temporary root
			if err != nil {
				return err
			}
			entry += " " + string(b)
		}
		out[p] = entry
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func TestProtectedPaths(t *testing.T) {
	ctx := context.Background()
	write := func(p string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { _, err := f.WriteText(ctx, p, []byte("new"), ""); return err }
	}
	overwrite := func(p, old string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { _, err := f.WriteText(ctx, p, []byte("new"), sha(old)); return err }
	}
	upload := func(p string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { _, err := f.Upload(ctx, p, strings.NewReader("new"), 3); return err }
	}
	rename := func(from, to string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { return f.Rename(ctx, from, to) }
	}
	del := func(p string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { return f.Delete(ctx, p, p) }
	}
	read := func(p string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error { _, _, err := f.ReadText(ctx, p); return err }
	}
	download := func(p string) func(*ConsoleFiles) error {
		return func(f *ConsoleFiles) error {
			rc, _, err := f.Open(ctx, p)
			if err == nil {
				_ = rc.Close()
			}
			return err
		}
	}
	tests := []struct {
		name string
		op   func(*ConsoleFiles) error
	}{
		{"write /proc", write("/proc/new")},
		{"write /sys", overwrite("/sys/x", "keep sys")},
		{"write /dev", write("/dev/new")},
		{"write /run", overwrite("/run/x", "keep run")},
		{"write /boot/efi", write("/boot/efi/new")},
		{"write /etc/darkpanel", overwrite("/etc/darkpanel/x", "keep etc/darkpanel")},
		{"write /var/lib/darkpanel", write("/var/lib/darkpanel/new")},
		{"write the binary", overwrite(consoleBinary, "binary")},
		{"upload /etc/darkpanel", upload("/etc/darkpanel/new")},
		{"upload the binary", upload(consoleBinary)},
		{"mkdir /sys", func(f *ConsoleFiles) error { return f.Mkdir(ctx, "/sys/new") }},
		{"chmod /etc/darkpanel", func(f *ConsoleFiles) error { return f.Chmod(ctx, "/etc/darkpanel/x", 0o600) }},
		{"chown /run", func(f *ConsoleFiles) error { return f.Chown(ctx, "/run/x", 1000, 1000) }},
		{"rename out of /etc/darkpanel", rename("/etc/darkpanel/x", "/tmp/x")},
		{"rename into /proc", rename("/tmp/y", "/proc/y")},
		{"rename the binary", rename(consoleBinary, "/tmp/darkpanel")},
		{"delete in /var/lib/darkpanel", del("/var/lib/darkpanel/x")},
		{"delete in /dev", del("/dev/x")},
		{"delete the binary", del(consoleBinary)},
		{"delete /boot/efi", del("/boot/efi")},
		{"rename /", rename("/", "/tmp/root")},
		{"rename /etc", rename("/etc", "/tmp/etc")},
		{"rename /var", rename("/var", "/tmp/var")},
		{"rename /var/lib", rename("/var/lib", "/tmp/lib")},
		{"rename /boot", rename("/boot", "/tmp/boot")},
		{"rename /usr/local/bin", rename("/usr/local/bin", "/tmp/bin")},
		{"delete /", del("/")},
		{"delete /etc", del("/etc")},
		{"delete /var", del("/var")},
		{"delete /var/lib", del("/var/lib")},
		{"read /proc", read("/proc/x")},
		{"read /sys", read("/sys/x")},
		{"read /dev", read("/dev/x")},
		{"read /etc/darkpanel", read("/etc/darkpanel/x")},
		{"read /var/lib/darkpanel", read("/var/lib/darkpanel/x")},
		{"download /proc", download("/proc/x")},
		{"download /sys", download("/sys/x")},
		{"download /dev", download("/dev/x")},
		{"download /etc/darkpanel", download("/etc/darkpanel/x")},
		{"download /var/lib/darkpanel", download("/var/lib/darkpanel/x")},
		{"list /etc/darkpanel", func(f *ConsoleFiles) error { _, err := f.List(ctx, "/etc/darkpanel", ""); return err }},
		{"read through a symlinked directory", read("/srv/link/x")},
		{"download through a symlinked directory", download("/srv/link/x")},
		{"write through a symlinked directory", overwrite("/srv/link/x", "keep etc/darkpanel")},
		{"upload through a symlinked directory", upload("/srv/link/new")},
		{"write a symlink", overwrite("/srv/file-link", "target")},
		{"upload over a symlink", upload("/srv/file-link")},
		{"chmod a symlink", func(f *ConsoleFiles) error { return f.Chmod(ctx, "/srv/file-link", 0o600) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, f := newConsoleRoot(t)
			before := snapshot(t, root)
			err := tt.op(f)
			if err == nil {
				t.Fatal("accepted")
			}
			if code := errcode.From(err).Code; code != errcode.InvalidInput {
				t.Errorf("error %v (code %s), want request.invalid_input", err, code)
			}
			after := snapshot(t, root)
			for p, v := range before {
				if after[p] != v {
					t.Errorf("%s changed: %q, was %q", p, after[p], v)
				}
			}
			for p := range after {
				if _, ok := before[p]; !ok {
					t.Errorf("%s appeared", p)
				}
			}
		})
	}
	t.Run("the same operations elsewhere", func(t *testing.T) {
		root, f := newConsoleRoot(t)
		if _, err := f.WriteText(ctx, "/srv/x", []byte("new"), sha("keep srv")); err != nil {
			t.Fatal(err)
		}
		if _, err := f.Upload(ctx, "/srv/up", strings.NewReader("abc"), 3); err != nil {
			t.Fatal(err)
		}
		if err := f.Rename(ctx, "/tmp/y", "/srv/y"); err != nil {
			t.Fatal(err)
		}
		if err := f.Delete(ctx, "/tmp", "/tmp"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := f.ReadText(ctx, "/srv/target.txt"); err != nil {
			t.Fatal(err)
		}
		if b, _ := os.ReadFile(filepath.Join(root, "srv", "x")); string(b) != "new" { //nolint:gosec // G304: a path below the test's temporary root
			t.Errorf("/srv/x = %q", b)
		}
		if _, err := os.Stat(filepath.Join(root, "tmp")); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("/tmp after its delete: %v", err)
		}
	})
	t.Run("a listing marks what is refused", func(t *testing.T) {
		_, f := newConsoleRoot(t)
		page, err := f.List(ctx, "/", "")
		if err != nil {
			t.Fatal(err)
		}
		got := map[string]string{}
		for _, e := range page.Entries {
			got[e.Name] = e.Protected
		}
		if got["proc"] == "" || got["etc"] == "" || got["srv"] != "" {
			t.Errorf("protected reasons %q; want proc and etc refused, srv not", got)
		}
		if root, err := f.Stat(ctx, "/"); err != nil || root.Protected == "" {
			t.Errorf("stat of /: protected %q, %v; want a reason", root.Protected, err)
		}
		_, err = f.WriteText(ctx, "/srv/file-link", []byte("new"), sha("target"))
		if err == nil || !strings.Contains(err.Error(), "symbolic link") {
			t.Errorf("a write to a symlink: %v; want it named a symbolic link", err)
		}
		page, err = f.List(ctx, "/run", "")
		if err != nil || page.Protected == "" {
			t.Errorf("listing /run: protected %q, %v; want a reason", page.Protected, err)
		}
	})
}

func TestWriteKeepsModeOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("giving a file to uid 1000 needs root")
	}
	ctx := context.Background()
	root, f := newConsoleRoot(t)
	target := filepath.Join(root, "srv", "owned")
	for i, write := range []func() error{
		func() error { _, err := f.WriteText(ctx, "/srv/owned", []byte("edited"), sha("old")); return err },
		func() error { _, err := f.Upload(ctx, "/srv/owned", strings.NewReader("uploaded"), 8); return err },
	} {
		if err := os.WriteFile(target, []byte("old"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(target, 0o640); err != nil { //nolint:gosec // G302: the mode the write must keep
			t.Fatal(err)
		}
		if err := os.Chown(target, 1000, 1000); err != nil {
			t.Fatal(err)
		}
		if err := write(); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		info, err := f.Stat(ctx, "/srv/owned")
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode != "0640" || info.UID != 1000 || info.GID != 1000 {
			t.Errorf("write %d: mode %s, owner %d:%d; want 0640 and 1000:1000", i, info.Mode, info.UID, info.GID)
		}
	}
	if _, err := f.WriteText(ctx, "/srv/new", []byte("n"), ""); err != nil {
		t.Fatal(err)
	}
	if info, _ := f.Stat(ctx, "/srv/new"); info.Mode != "0644" || info.UID != 0 {
		t.Errorf("a new file: mode %s, uid %d; want 0644 and root", info.Mode, info.UID)
	}
}

// cutReader gives n bytes, then fails, as a dropped connection does.
type cutReader struct{ n int }

func (r *cutReader) Read(p []byte) (int, error) {
	if r.n == 0 {
		return 0, io.ErrUnexpectedEOF
	}
	m := min(len(p), r.n)
	r.n -= m
	return m, nil
}

func temps(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), TempPrefix) {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestTempCleanup(t *testing.T) {
	ctx := context.Background()
	root, f := newConsoleRoot(t)
	srv := filepath.Join(root, "srv")
	var recorded []string
	rctx := core.WithTempRecord(ctx, func(p string) error {
		if _, err := os.Stat(filepath.Join(root, p)); err != nil {
			t.Errorf("the recorded temporary file %s: %v", p, err)
		}
		if got := temps(t, srv); len(got) != 1 {
			t.Errorf("at the record the directory holds %v", got)
		} else if b, _ := os.ReadFile(filepath.Join(srv, got[0])); len(b) != 0 { //nolint:gosec // G304: a path below the test's temporary root
			t.Errorf("the temporary file held %d bytes before its record", len(b))
		}
		recorded = append(recorded, p)
		return nil
	})
	if _, err := f.Upload(rctx, "/srv/cut", &cutReader{n: 100 << 10}, 1<<20); err == nil {
		t.Fatal("an upload cut off mid-body succeeded")
	}
	cctx, cancel := context.WithCancel(rctx)
	body := io.MultiReader(strings.NewReader(strings.Repeat("a", TransferChunk)), readerFunc(func([]byte) (int, error) {
		cancel()
		return 0, nil
	}), strings.NewReader("rest"))
	if _, err := f.Upload(cctx, "/srv/cancelled", body, TransferChunk+4); !errors.Is(err, context.Canceled) {
		t.Fatalf("a cancelled upload: %v, want context.Canceled", err)
	}
	failing := core.WithTempRecord(ctx, func(string) error { return errors.New("database gone") })
	if _, err := f.WriteText(failing, "/srv/x", []byte("new"), sha("keep srv")); err == nil {
		t.Fatal("a write whose temporary file could not be recorded succeeded")
	}
	if got := temps(t, srv); len(got) != 0 {
		t.Errorf("temporary files left: %v", got)
	}
	if len(recorded) != 2 {
		t.Errorf("recorded %v, want both uploads' temporary files", recorded)
	}
	for _, name := range []string{"cut", "cancelled"} {
		if _, err := os.Stat(filepath.Join(srv, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("/srv/%s exists after its failed upload: %v", name, err)
		}
	}
	if b, _ := os.ReadFile(filepath.Join(srv, "x")); string(b) != "keep srv" { //nolint:gosec // G304: a path below the test's temporary root
		t.Errorf("/srv/x = %q after the failed write", b)
	}

	t.Run("a recorded stale file is removed", func(t *testing.T) {
		stale := TempPrefix + "0123456789abcdef"
		if err := os.WriteFile(filepath.Join(srv, stale), []byte("half"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := f.RemoveTemp(ctx, "/srv/"+stale); err != nil {
			t.Fatal(err)
		}
		if got := temps(t, srv); len(got) != 0 {
			t.Errorf("left %v", got)
		}
		if err := f.RemoveTemp(ctx, "/srv/"+stale); err != nil {
			t.Errorf("removing it again: %v", err)
		}
	})
	t.Run("nothing else is removed", func(t *testing.T) {
		link := TempPrefix + "fedcba9876543210"
		if err := os.Symlink("target.txt", filepath.Join(srv, link)); err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"/srv/target.txt", "/srv/" + link, "/etc/darkpanel/x", "/srv/link/" + TempPrefix + "0123456789abcdef"} {
			if err := f.RemoveTemp(ctx, p); err == nil {
				t.Errorf("RemoveTemp(%s) succeeded", p)
			}
		}
		if _, err := os.Lstat(filepath.Join(srv, link)); err != nil {
			t.Errorf("the symlink was removed: %v", err)
		}
		if b, _ := os.ReadFile(filepath.Join(srv, "target.txt")); !bytes.Equal(b, []byte("target")) { //nolint:gosec // G304: a path below the test's temporary root
			t.Errorf("target.txt = %q", b)
		}
	})
}

type readerFunc func([]byte) (int, error)

func (f readerFunc) Read(p []byte) (int, error) { return f(p) }

package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// w10brFile writes a regular file and gives it mode 0644, so that a call
// which sets 0600 shows in the mode.
func w10brFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("content"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o644); err != nil { //nolint:gosec // G302: a mode other than 0600 shows whether a call changed it
		t.Fatal(err)
	}
}

func w10brMode(t *testing.T, path string) fs.FileMode {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return st.Mode().Perm()
}

func TestW10BrOSOpsSecure(t *testing.T) {
	ops := OSOps()
	uid, gid := os.Getuid(), os.Getgid()

	t.Run("a regular file gets the mode", func(t *testing.T) {
		p := filepath.Join(t.TempDir(), "f")
		w10brFile(t, p)
		if err := ops.Secure(p, uid, gid, 0o600); err != nil {
			t.Fatalf("Secure = %v", err)
		}
		if got := w10brMode(t, p); got != 0o600 {
			t.Errorf("mode after Secure = %v, want 0600", got)
		}
	})

	t.Run("a missing path", func(t *testing.T) {
		err := ops.Secure(filepath.Join(t.TempDir(), "missing"), uid, gid, 0o600)
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Secure of a missing path = %v, want fs.ErrNotExist", err)
		}
	})

	// A refused path must not change the mode of the file behind it, and a
	// named pipe must be refused without blocking.
	refusals := []struct {
		name  string
		plant func(t *testing.T, dir string) (path, untouched string)
	}{
		{"a symbolic link to a file", func(t *testing.T, dir string) (string, string) {
			target, link := filepath.Join(dir, "target"), filepath.Join(dir, "link")
			w10brFile(t, target)
			if err := os.Symlink(target, link); err != nil {
				t.Fatal(err)
			}
			return link, target
		}},
		{"a dangling symbolic link", func(t *testing.T, dir string) (string, string) {
			link := filepath.Join(dir, "link")
			if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
				t.Fatal(err)
			}
			return link, ""
		}},
		{"a file with a second hard link", func(t *testing.T, dir string) (string, string) {
			first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
			w10brFile(t, first)
			if err := os.Link(first, second); err != nil {
				t.Fatal(err)
			}
			return second, first
		}},
		{"a named pipe", func(t *testing.T, dir string) (string, string) {
			fifo := filepath.Join(dir, "pipe")
			if err := syscall.Mkfifo(fifo, 0o600); err != nil {
				t.Fatal(err)
			}
			return fifo, ""
		}},
		{"a directory", func(t *testing.T, dir string) (string, string) {
			sub := filepath.Join(dir, "sub")
			if err := os.Mkdir(sub, 0o755); err != nil { //nolint:gosec // G301: a mode other than 0700 shows whether a call changed it
				t.Fatal(err)
			}
			return sub, sub
		}},
	}
	for _, tc := range refusals {
		t.Run("refuses "+tc.name, func(t *testing.T) {
			path, untouched := tc.plant(t, t.TempDir())
			var before fs.FileMode
			if untouched != "" {
				before = w10brMode(t, untouched)
			}
			done := make(chan error, 1)
			go func() { done <- ops.Secure(path, uid, gid, 0o600) }()
			select {
			case err := <-done:
				if err == nil || !strings.Contains(err.Error(), "is not a single regular file") {
					t.Errorf("Secure = %v, want an error saying the path is not a single regular file", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("Secure did not return within 10 seconds")
			}
			if untouched != "" {
				if after := w10brMode(t, untouched); after != before {
					t.Errorf("the mode behind the refused path changed from %v to %v", before, after)
				}
			}
		})
	}
}

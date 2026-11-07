package platform

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

// v1r15Dir is a temporary directory whose own path holds no symlink, because ReadRegularFile refuses one anywhere.
func v1r15Dir(t *testing.T) string {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestV1r15ReadRegularFile(t *testing.T) {
	const secret = "placeholder-secret-content"
	const content = "certificate bytes"
	dir := v1r15Dir(t)
	put := func(path, data string) string {
		t.Helper()
		if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	regular := put(filepath.Join(dir, "regular"), content)
	outside := put(filepath.Join(dir, "outside"), secret)
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	put(filepath.Join(realDir, "crt"), secret)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(outside, link); err != nil {
		t.Fatal(err)
	}
	dirLink := filepath.Join(dir, "dirlink")
	if err := os.Symlink(realDir, dirLink); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		path     string
		limit    int64
		want     string
		wantErr  string
		notExist bool
	}{
		{name: "a regular file", path: regular, limit: 1024, want: content},
		{name: "exactly the limit", path: regular, limit: int64(len(content)), want: content},
		{name: "one byte over the limit", path: regular, limit: int64(len(content)) - 1, wantErr: "larger than"},
		{name: "a symlink to a file", path: link, limit: 1024, wantErr: "symbolic link"},
		{name: "a symlinked directory in the path", path: filepath.Join(dirLink, "crt"), limit: 1024, wantErr: "symbolic link"},
		{name: "a directory", path: realDir, limit: 1024, wantErr: "not a regular file"},
		{name: "a FIFO", path: fifo, limit: 1024, wantErr: "not a regular file"},
		{name: "a missing file", path: filepath.Join(dir, "missing"), limit: 1024, notExist: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			type result struct {
				data []byte
				err  error
			}
			done := make(chan result, 1)
			go func() {
				data, err := ReadRegularFile(tc.path, tc.limit)
				done <- result{data, err}
			}()
			var got result
			select {
			case got = <-done:
			case <-time.After(5 * time.Second):
				t.Fatal("ReadRegularFile blocked")
			}
			switch {
			case tc.notExist:
				if !errors.Is(got.err, fs.ErrNotExist) {
					t.Errorf("error %v, want one that satisfies fs.ErrNotExist", got.err)
				}
			case tc.wantErr != "":
				if got.err == nil || !strings.Contains(got.err.Error(), tc.wantErr) {
					t.Errorf("error %v, want one containing %q", got.err, tc.wantErr)
				}
			case got.err != nil || string(got.data) != tc.want:
				t.Errorf("got %q, %v; want %q", got.data, got.err, tc.want)
			}
			if got.err != nil {
				if len(got.data) != 0 {
					t.Errorf("a failed read returned %d bytes", len(got.data))
				}
				if strings.Contains(got.err.Error(), secret) {
					t.Errorf("error %v carries file content", got.err)
				}
			}
		})
	}
}

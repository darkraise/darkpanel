package platform

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func TestW11c05ReadRegularFileBeneath(t *testing.T) {
	root := t.TempDir()
	certs := filepath.Join(root, "disk", "certs")
	if err := os.MkdirAll(certs, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(certs, "a.crt"), []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "disk"), filepath.Join(root, "linked")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(certs, "a.crt"), filepath.Join(certs, "planted")); err != nil {
		t.Fatal(err)
	}
	through := filepath.Join(root, "linked", "certs")

	got, err := ReadRegularFileBeneath(through, "a.crt", 64)
	if err != nil || string(got) != "cert" {
		t.Fatalf("a file through a host link above the directory = %q, %v", got, err)
	}
	if _, err := ReadRegularFileBeneath(through, "planted", 64); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a link inside the directory = %v, want a refusal naming the link", err)
	}
	if _, err := ReadRegularFileBeneath(through, "missing", 64); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file = %v, want fs.ErrNotExist", err)
	}
	if _, err := ReadRegularFileBeneath(through, "../certs/a.crt", 64); err == nil || !strings.Contains(err.Error(), "is outside its directory") {
		t.Errorf("a name climbing out of the directory = %v, want a refusal naming it as outside", err)
	}
	if err := syscall.Mkfifo(filepath.Join(certs, "fifo"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadRegularFileBeneath(through, "fifo", 64); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Errorf("a planted FIFO = %v, want a refusal without blocking", err)
	}
	if _, err := ReadRegularFileBeneath(through, "a.crt", 2); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Errorf("a file over the limit = %v, want a size refusal", err)
	}
}

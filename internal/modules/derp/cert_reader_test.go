package derp

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestW11c05CertFileReaderWiring(t *testing.T) {
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
	dir := filepath.Join(root, "linked", "certs")
	read := certFileReader(dir)

	if got, err := read(dir + "/a.crt"); err != nil || string(got) != "cert" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if _, err := read(dir + "/planted"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a planted link = %v, want a refusal naming the link", err)
	}
	if _, err := read(dir + "/missing"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("a missing file = %v, want fs.ErrNotExist to reach the caller", err)
	}
	if _, err := read(filepath.Join(root, "disk", "certs", "a.crt")); err == nil {
		t.Error("a path outside the directory was read")
	}
	if err := os.WriteFile(filepath.Join(root, "disk", "derper.key"), []byte("key"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := read(dir + "/../derper.key"); err == nil || !strings.Contains(err.Error(), "is not under") {
		t.Errorf("a name climbing to the parent = %q, %v, want it refused as not under the directory", got, err)
	}
}

func TestW11c05CertFileReaderRefusesALinkAtTheDirectory(t *testing.T) {
	root := t.TempDir()
	elsewhere := filepath.Join(root, "elsewhere")
	if err := os.Mkdir(elsewhere, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(elsewhere, "a.crt"), []byte("cert"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "certs")
	if err := os.Symlink(elsewhere, dir); err != nil {
		t.Fatal(err)
	}
	if got, err := certFileReader(dir)(dir + "/a.crt"); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Errorf("a link at the directory = %q, %v, want a refusal naming the link", got, err)
	}
}

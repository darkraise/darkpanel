package secret

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeKeyFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOrCreateKeyCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("key length = %d, want %d", len(key), KeySize)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key mode = %o, want 600", mode)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := KeyPrefix + base64.StdEncoding.EncodeToString(key) + "\n"; string(raw) != want {
		t.Errorf("key file = %q, want %q", raw, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("directory entries = %v, %v; want only the key (no temporary file left)", entries, err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(again, key) {
		t.Errorf("second call = %x, %v; want the same key", again, err)
	}
}

func TestLoadOrCreateKeyKeepsExistingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.key")
	want := bytes.Repeat([]byte{7}, KeySize)
	writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(want)+"\n", 0o600)
	got, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("LoadOrCreateKey = %x, %v; want the existing key", got, err)
	}
}

func TestLoadOrCreateKeyConcurrentCallersAgree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.key")
	keys := make([][]byte, 8)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() { keys[i], errs[i] = LoadOrCreateKey(path) })
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil || !bytes.Equal(keys[i], keys[0]) {
			t.Errorf("caller %d = %x, %v; want %x", i, keys[i], errs[i], keys[0])
		}
	}
}

func TestLoadOrCreateKeyRefusesOpenMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660} {
		path := filepath.Join(t.TempDir(), "controller.key")
		writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", mode)
		if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Errorf("mode %o: err = %v, want a mode error", mode, err)
		}
	}
}

func TestLoadOrCreateKeyRefusesBadContent(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, KeySize))
	for _, content := range []string{
		"",
		good + "\n",
		"darkpanel-key-v2:" + good + "\n",
		KeyPrefix + "not base64!\n",
		KeyPrefix + base64.StdEncoding.EncodeToString(make([]byte, 16)) + "\n",
		KeyPrefix + good + "\n" + KeyPrefix + good + "\n",
		KeyPrefix + good + "\n\n",
	} {
		path := filepath.Join(t.TempDir(), "controller.key")
		writeKeyFile(t, path, content, 0o600)
		if _, err := LoadOrCreateKey(path); err == nil {
			t.Errorf("content %q accepted", content)
		}
	}
}

func TestLoadOrCreateKeyRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.key")
	writeKeyFile(t, target, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", 0o600)
	link := filepath.Join(dir, "controller.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(link); err == nil {
		t.Error("a symlinked key file was accepted")
	}
}

func TestLoadOrCreateKeyRefusesForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing a file's owner needs root")
	}
	path := filepath.Join(t.TempDir(), "controller.key")
	writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", 0o600)
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("err = %v, want an owner error", err)
	}
}

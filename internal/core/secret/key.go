// Package secret holds the role keys, the sealer that encrypts secrets at rest
// and the redaction of secret fields (ADR 0005, spec section 5).
package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// KeyPrefix starts every key file; the version lets the format change later.
const KeyPrefix = "darkpanel-key-v1:"

// KeySize is the length of a role key in bytes.
const KeySize = 32

// maxKeyFile bounds what is read from a key file; a valid one is 62 bytes.
const maxKeyFile = 1024

// LoadOrCreateKey returns the role key at path, creating it first when the
// file does not exist. Creation never replaces an existing key: the new key is
// written to a temporary file and hard-linked to path, so a concurrent creator
// or an existing file wins and is loaded instead. Loading refuses a symlink, a
// file group or other can access, a file another user owns and any content
// other than one KeyPrefix line holding KeySize bytes in standard base64.
func LoadOrCreateKey(path string) ([]byte, error) {
	_, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := createKey(path); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("secret: key %s: %w", path, err)
	}
	return loadKey(path)
}

func createKey(path string) (err error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("secret: generate key: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".darkpanel-key-*") // created with mode 0600
	if err != nil {
		return fmt.Errorf("secret: create key: %w", err)
	}
	defer func() {
		if rmErr := os.Remove(tmp.Name()); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("secret: remove temporary key: %w", rmErr))
		}
	}()
	_, err = tmp.WriteString(KeyPrefix + base64.StdEncoding.EncodeToString(key) + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("secret: write key: %w", err)
	}
	// link(2) fails with EEXIST instead of replacing, unlike rename(2): an
	// existing key, or one a concurrent creator just linked, is kept.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("secret: install key: %w", err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the key directory comes from paths.Paths, never from a request
	if err != nil {
		return fmt.Errorf("secret: open key directory: %w", err)
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("secret: sync key directory: %w", err)
	}
	return nil
}

func loadKey(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0) //nolint:gosec // G304: the key path comes from paths.Paths, never from a request
	if err != nil {
		return nil, fmt.Errorf("secret: open key %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("secret: stat key %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret: key %s is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("secret: key %s has mode %o; group and other must have no access (chmod 600)", path, perm)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("secret: key %s has another owner than uid %d", path, os.Geteuid())
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFile))
	if err != nil {
		return nil, fmt.Errorf("secret: read key %s: %w", path, err)
	}
	line, ok := bytes.CutSuffix(raw, []byte("\n"))
	encoded, hasPrefix := bytes.CutPrefix(line, []byte(KeyPrefix))
	if !ok || !hasPrefix || bytes.ContainsAny(encoded, "\r\n") {
		return nil, fmt.Errorf("secret: key %s: want one line starting with %s", path, KeyPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil || len(key) != KeySize {
		// The decoder's error would quote key material, so it is not reported.
		return nil, fmt.Errorf("secret: key %s: want %d bytes in standard base64 after %s", path, KeySize, KeyPrefix)
	}
	return key, nil
}

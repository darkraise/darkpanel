//go:build harness

// Package release publishes signed test releases for the harness in the
// production layout (spec section 9): <dir>/index/releases.json and, per
// version, <dir>/v<version>/ with the tarball, checksums.txt and its
// signature. The scanner VM serves the directory as the release base URL of
// the harnesskeys build and of install.sh --base-url.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update"
)

// Version is one release to publish.
type Version struct {
	Version string // a semantic version, such as 0.3.1
	Channel string // stable or beta
	Binary  string // a linux/amd64 darkpanel built for Version
}

// LoadKey reads the unencrypted minisign secret key at path, such as the
// committed harness key internal/update/trustedkeys/testdata/harness.key.
// PrivateKeyFromFile only decrypts, so it refuses an unencrypted key;
// UnmarshalText reads one and, unlike the C minisign tool, does not check the
// key's checksum, which the harness key leaves all zero.
func LoadKey(path string) (minisign.PrivateKey, error) {
	text, err := os.ReadFile(path) //nolint:gosec // G304: the key file the harness names
	if err != nil {
		return minisign.PrivateKey{}, fmt.Errorf("release: %w", err)
	}
	var key minisign.PrivateKey
	if err := key.UnmarshalText(text); err != nil {
		return minisign.PrivateKey{}, fmt.Errorf("release: %s: %w", path, err)
	}
	return key, nil
}

// Publish writes versions under dir, signed with key, and an index listing
// them in order. LICENSE and README.md come from the repository root, the
// nearest directory above the working directory that holds go.mod, so the
// harness tests and this package's tests, which run in different package
// directories, package the same files.
func Publish(dir string, key minisign.PrivateKey, versions []Version) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE")) //nolint:gosec // G304: a repository file
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md")) //nolint:gosec // G304: a repository file
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	index := update.Index{FormatVersion: 1, Releases: []update.Release{}}
	seen := map[string]bool{}
	for _, v := range versions {
		if _, err := update.ParseVersion(v.Version); err != nil {
			return fmt.Errorf("release: %w", err)
		}
		if v.Channel != "stable" && v.Channel != "beta" {
			return fmt.Errorf("release: %s: channel %q is not stable or beta", v.Version, v.Channel)
		}
		if seen[v.Version] {
			return fmt.Errorf("release: version %s is listed twice", v.Version)
		}
		seen[v.Version] = true
		if err := publishVersion(dir, key, v, license, readme); err != nil {
			return err
		}
		index.Releases = append(index.Releases, update.Release{Version: v.Version, Channel: v.Channel})
	}
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("release: index: %w", err)
	}
	return writeFile(filepath.Join(dir, "index", "releases.json"), append(data, '\n'))
}

// publishVersion writes v<version>/ as release/package.sh and release/sign.sh
// write it: the tarball, checksums.txt in sha256sum's text form, and one
// hashed-mode signature with the trusted comment the verifiers require.
func publishVersion(dir string, key minisign.PrivateKey, v Version, license, readme []byte) error {
	binary, err := os.ReadFile(v.Binary)
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	tarball, err := tarGz([]file{
		{"darkpanel", 0o755, binary},
		{"LICENSE", 0o644, license},
		{"README.md", 0o644, readme},
	})
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	vdir := filepath.Join(dir, "v"+v.Version)
	name := update.TarballName(v.Version, "amd64")
	if err := writeFile(filepath.Join(vdir, name), tarball); err != nil {
		return err
	}
	checksums := fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(tarball), name)
	if err := writeFile(filepath.Join(vdir, "checksums.txt"), checksums); err != nil {
		return err
	}
	sig, err := sign(key, checksums, fmt.Sprintf("version=%s channel=%s purpose=checksums", v.Version, v.Channel))
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	return writeFile(filepath.Join(vdir, "checksums.txt."+fmt.Sprintf("%016X", key.ID())+".minisig"), sig)
}

// sign signs message in minisign's hashed mode, the only mode the updater
// and install.sh accept. The package-level minisign.SignWithComments signs in
// the legacy mode, so the message goes through a Reader, which signs its
// BLAKE2b-512 digest.
func sign(key minisign.PrivateKey, message []byte, trustedComment string) ([]byte, error) {
	r := minisign.NewReader(bytes.NewReader(message))
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, err
	}
	return r.SignWithComments(key, trustedComment, "signature from minisign secret key"), nil
}

type file struct {
	name string
	mode int64
	data []byte
}

// tarGz builds the archive as package.sh does: entries in the given order,
// owner 0:0 by number, fixed modes and mtime, and gzip without a name or
// timestamp.
func tarGz(files []file) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg, Name: f.name, Mode: f.mode, Size: int64(len(f.data)),
			ModTime: time.Unix(0, 0), Format: tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFile writes data to path, creating its directory. Release files are
// public, and the web server that serves them may run as any user, so they
// are world-readable.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: published release files
		return fmt.Errorf("release: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // G306: published release files
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// repoRoot returns the nearest directory at or above the working directory
// that holds go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("release: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("release: no go.mod at or above the working directory; run from inside the repository")
		}
		dir = parent
	}
}

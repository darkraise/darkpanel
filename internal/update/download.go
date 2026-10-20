package update

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// Limits on what a release download reads, far above a real release's sizes.
const (
	maxChecksumsBytes = 64 << 10
	maxSignatureBytes = 4 << 10
	maxTarballBytes   = 256 << 20
	maxBinaryBytes    = 256 << 20
	// maxArchiveBytes bounds the decompressed archive, so a small tarball
	// cannot expand without limit.
	maxArchiveBytes = 512 << 20
)

var (
	archName     = regexp.MustCompile(`^[a-z0-9]+$`)
	checksumHex  = regexp.MustCompile(`^[0-9a-f]{64}$`)
	errNotServed = errors.New("not published")
)

// TarballName is the release tarball of version for arch (F-C16).
func TarballName(version, arch string) string {
	return fmt.Sprintf("darkpanel_%s_linux_%s.tar.gz", version, arch)
}

// Download fetches rel for arch from <baseURL>/v<version>/ into dir, which
// must be empty: checksums.txt, one signature file per trusted key (a missing
// one is skipped), and the tarball. It verifies the signature against
// rel.Channel, the channel of the selected index entry, and the tarball
// against its checksums.txt line, then extracts the archive root's darkpanel
// to dir/darkpanel with mode 0755 and returns that path. Every URL is built
// here from baseURL, so a caller can name a version but never a location.
func Download(ctx context.Context, client *http.Client, baseURL string, keys []trustedkeys.Key, rel Release, arch, dir string) (string, error) {
	if _, err := ParseVersion(rel.Version); err != nil {
		return "", err
	}
	if !archName.MatchString(arch) {
		return "", fmt.Errorf("update: %q is not an architecture", arch)
	}
	base := strings.TrimSuffix(baseURL, "/") + "/v" + rel.Version + "/"
	checksums, err := fetchBytes(ctx, client, base+"checksums.txt", maxChecksumsBytes)
	if errors.Is(err, errNotServed) {
		return "", errcode.Wrap(errcode.UpdateNotFound, err, fmt.Sprintf("release %s is not published", rel.Version))
	}
	if err != nil {
		return "", err
	}
	sigs := map[string][]byte{}
	for _, k := range keys {
		if sig, err := fetchBytes(ctx, client, base+"checksums.txt."+k.ID+".minisig", maxSignatureBytes); err == nil {
			sigs[k.ID] = sig
		}
	}
	// A cancelled download must not be reported as a bad signature.
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := VerifyChecksums(checksums, sigs, keys, rel.Version, rel.Channel); err != nil {
		return "", err
	}
	name := TarballName(rel.Version, arch)
	want, err := checksumFor(checksums, name)
	if err != nil {
		return "", err
	}
	tarball := filepath.Join(dir, name)
	got, err := fetchFile(ctx, client, base+name, tarball, maxTarballBytes)
	if errors.Is(err, errNotServed) {
		return "", errcode.Wrap(errcode.UpdateNotFound, err, fmt.Sprintf("release %s has no %s", rel.Version, name))
	}
	if err != nil {
		return "", err
	}
	if got != want {
		return "", errcode.New(errcode.UpdateSignatureInvalid, fmt.Sprintf("%s does not match the signed checksums.txt", name))
	}
	binary := filepath.Join(dir, "darkpanel")
	if err := extractBinary(tarball, binary); err != nil {
		return "", errors.Join(err, removeIfExists(binary))
	}
	return binary, nil
}

// checksumFor returns the hash checksums.txt lists for name, in sha256sum's
// text ("  ") or binary (" *") form.
func checksumFor(checksums []byte, name string) (string, error) {
	var found []string
	for _, line := range strings.Split(string(checksums), "\n") {
		if len(line) < 67 || !checksumHex.MatchString(line[:64]) {
			continue
		}
		if sep := line[64:66]; (sep == "  " || sep == " *") && line[66:] == name {
			found = append(found, line[:64])
		}
	}
	switch len(found) {
	case 0:
		return "", errcode.New(errcode.UpdateNotFound, fmt.Sprintf("checksums.txt lists no %s", name))
	case 1:
		return found[0], nil
	}
	return "", fmt.Errorf("update: checksums.txt lists %s %d times", name, len(found))
}

// fetch opens url; an answer of 404 is errNotServed.
func fetch(ctx context.Context, client *http.Client, url string) (io.ReadCloser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
		return resp.Body, nil
	case http.StatusNotFound:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("update: download %s: %w", url, errNotServed)
	}
	_ = resp.Body.Close()
	return nil, fmt.Errorf("update: download %s: answered %s", url, resp.Status)
}

func fetchBytes(ctx context.Context, client *http.Client, url string, limit int64) ([]byte, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	data, err := io.ReadAll(io.LimitReader(body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("update: download %s: %w", url, err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("update: download %s: over %d bytes", url, limit)
	}
	return data, nil
}

// fetchFile downloads url into a new file at path and returns its hash.
func fetchFile(ctx context.Context, client *http.Client, url, path string, limit int64) (string, error) {
	body, err := fetch(ctx, client, url)
	if err != nil {
		return "", err
	}
	defer func() { _ = body.Close() }()
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: path is under the transaction's download directory
	if err != nil {
		return "", fmt.Errorf("update: download %s: %w", url, err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(f, h), io.LimitReader(body, limit+1))
	if err == nil && n > limit {
		err = fmt.Errorf("over %d bytes", limit)
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return "", fmt.Errorf("update: download %s: %w", url, err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractBinary writes the regular file darkpanel at the archive's root to
// dst with mode 0755. Any other entry is skipped, so a name like
// ../darkpanel or sub/darkpanel never reaches the disk; a second darkpanel,
// or one that is not a regular file, refuses the archive.
func extractBinary(tarball, dst string) error {
	in, err := os.Open(tarball) //nolint:gosec // G304: the tarball Download just wrote
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	defer func() { _ = in.Close() }()
	gz, err := gzip.NewReader(in)
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	tr := tar.NewReader(io.LimitReader(gz, maxArchiveBytes))
	found := false
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return fmt.Errorf("update: extract: %w", err)
		}
		if hdr.Name != "darkpanel" && hdr.Name != "./darkpanel" {
			continue
		}
		switch {
		case found:
			return errors.New("update: extract: the archive holds darkpanel twice")
		case hdr.Typeflag != tar.TypeReg:
			return errors.New("update: extract: darkpanel in the archive is not a regular file")
		case hdr.Size > maxBinaryBytes:
			return fmt.Errorf("update: extract: darkpanel is over %d bytes", maxBinaryBytes)
		}
		if err := writeExtracted(dst, tr, hdr.Size); err != nil {
			return err
		}
		found = true
	}
	if !found {
		return errors.New("update: extract: the archive holds no darkpanel at its root")
	}
	return nil
}

func writeExtracted(dst string, r io.Reader, size int64) error {
	f, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o755) //nolint:gosec // G302,G304: an executable in the transaction's download directory
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	_, err = io.CopyN(f, r, size)
	if err == nil {
		err = f.Chmod(0o755) //nolint:gosec // G302: the binary must be executable
	}
	if err == nil {
		err = f.Sync()
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("update: extract: %w", err)
	}
	return nil
}

// trustedComment is the exact trusted comment of a release's checksums.txt
// signature (F-C16). It binds the signature to one version and channel, so a
// replayed older checksums.txt or a beta signature cannot pass for another
// release.
func trustedComment(version, channel string) string {
	return fmt.Sprintf("version=%s channel=%s purpose=checksums", version, channel)
}

// VerifyChecksums accepts checksums when one key of keys has a signature in
// sigs, keyed by trustedkeys.Key.ID, that verifies in minisign's hashed mode
// (algorithm ED) with the trusted comment for version and channel. A missing,
// malformed or failing signature is one unsuccessful candidate, so a key
// rotation's dual-signed releases verify under either key (ADR 0007).
// Legacy Ed signatures are refused: the release workflow signs in hashed
// mode only, and install.sh verifies only that mode.
func VerifyChecksums(checksums []byte, sigs map[string][]byte, keys []trustedkeys.Key, version, channel string) error {
	want := trustedComment(version, channel)
	for _, k := range keys {
		raw, ok := sigs[k.ID]
		if !ok {
			continue
		}
		var pub minisign.PublicKey
		if err := pub.UnmarshalText([]byte(k.PublicKey)); err != nil {
			continue
		}
		var sig minisign.Signature
		if err := sig.UnmarshalText(raw); err != nil {
			continue
		}
		if sig.Algorithm != minisign.HashEdDSA || sig.TrustedComment != want {
			continue
		}
		if minisign.Verify(pub, checksums, raw) {
			return nil
		}
	}
	return errcode.New(errcode.UpdateSignatureInvalid,
		fmt.Sprintf("no trusted key signed checksums.txt of version %s for the %s channel", version, channel))
}

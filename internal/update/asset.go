package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

var assetName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// DownloadAsset fetches asset name of rel from <baseURL>/v<version>/:
// checksums.txt and its signatures, verified exactly as Download verifies
// them (VerifyChecksums against rel.Channel), then name, read up to limit
// bytes, whose SHA-256 must match its checksums.txt line. name matches
// ^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$ (else an error "update: <name> is not an
// asset name"). An unserved checksums.txt or asset is update.not_found; a
// hash mismatch is update.signature_invalid ("<name> does not match the
// signed checksums.txt"); an asset larger than limit is an error naming
// limit. Every URL is built here from baseURL.
func DownloadAsset(ctx context.Context, client *http.Client, baseURL string, keys []trustedkeys.Key, rel Release, name string, limit int64) ([]byte, error) {
	if _, err := ParseVersion(rel.Version); err != nil {
		return nil, err
	}
	if !assetName.MatchString(name) {
		return nil, fmt.Errorf("update: %q is not an asset name", name)
	}
	base := strings.TrimSuffix(baseURL, "/") + "/v" + rel.Version + "/"
	checksums, err := verifiedChecksums(ctx, client, base, keys, rel)
	if err != nil {
		return nil, err
	}
	want, err := checksumFor(checksums, name)
	if err != nil {
		return nil, err
	}
	data, err := fetchBytes(ctx, client, base+name, limit)
	if errors.Is(err, errNotServed) {
		return nil, errcode.Wrap(errcode.UpdateNotFound, err, fmt.Sprintf("release %s has no %s", rel.Version, name))
	}
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(data)
	if hex.EncodeToString(sum[:]) != want {
		return nil, errcode.New(errcode.UpdateSignatureInvalid, fmt.Sprintf("%s does not match the signed checksums.txt", name))
	}
	return data, nil
}

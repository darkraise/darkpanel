package artifact

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"slices"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const (
	maxManifestBytes = 64 << 10
	maxBinaryBytes   = 128 << 20
)

// Source is where Fetch reads a release.
type Source struct {
	Client   *http.Client
	BaseURL  string                                              // trustedkeys.BaseURL() in production
	Keys     []trustedkeys.Key                                   // trustedkeys.Keys() in production
	Releases func(ctx context.Context) ([]update.Release, error) // (*update.IndexSource).Releases in production
}

// Fetch returns derper version want for arch from the release of Darkpanel version dpVersion: it finds dpVersion in
// Releases (absent: update.not_found, "darkpanel <dpVersion> is not a published release; a DERP server needs a
// released darkpanel"), reads ManifestName with update.DownloadAsset (at most 64 KiB), checks the manifest's
// darkpanelVersion equals dpVersion (else update.signature_invalid) and its derperVersion equals want (else
// update.not_found, "release <dpVersion> carries derper <v>, not <want>"), reads BinaryName(want, arch) with
// update.DownloadAsset (at most 128 MiB) and checks its SHA-256 against the manifest's artifacts[arch] (else
// update.signature_invalid). sha is the 64 lower-case hex digits of the binary.
func Fetch(ctx context.Context, src Source, dpVersion, want, arch string) (bin []byte, sha string, err error) {
	if !slices.Contains(Arches, arch) {
		return nil, "", fmt.Errorf("artifact: no derper build for architecture %q", arch)
	}
	releases, err := src.Releases(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("artifact: read the release index: %w", err)
	}
	i := slices.IndexFunc(releases, func(r update.Release) bool { return r.Version == dpVersion })
	if i < 0 {
		return nil, "", errcode.New(errcode.UpdateNotFound,
			fmt.Sprintf("darkpanel %s is not a published release; a DERP server needs a released darkpanel", dpVersion))
	}
	rel := releases[i]

	raw, err := update.DownloadAsset(ctx, src.Client, src.BaseURL, src.Keys, rel, ManifestName, maxManifestBytes)
	if err != nil {
		return nil, "", err
	}
	m, err := ParseManifest(raw)
	if err != nil {
		return nil, "", fmt.Errorf("artifact: release %s: %w", dpVersion, err)
	}
	if m.DarkpanelVersion != dpVersion {
		return nil, "", errcode.New(errcode.UpdateSignatureInvalid,
			fmt.Sprintf("%s of release %s names darkpanel %s", ManifestName, dpVersion, m.DarkpanelVersion))
	}
	if m.DerperVersion != want {
		return nil, "", errcode.New(errcode.UpdateNotFound,
			fmt.Sprintf("release %s carries derper %s, not %s", dpVersion, m.DerperVersion, want))
	}

	name := BinaryName(want, arch)
	data, err := update.DownloadAsset(ctx, src.Client, src.BaseURL, src.Keys, rel, name, maxBinaryBytes)
	if err != nil {
		return nil, "", err
	}
	// The signed checksums.txt vouches for the binary and for derper.json separately; the manifest must also agree
	// with the binary, so a release whose two files disagree is refused.
	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	if digest != m.Artifacts[arch] {
		return nil, "", errcode.New(errcode.UpdateSignatureInvalid, fmt.Sprintf("%s does not match %s", name, ManifestName))
	}
	return data, digest, nil
}

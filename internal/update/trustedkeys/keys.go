// Package trustedkeys lists the minisign public keys a release must be signed
// with (ADR 0007) and where releases are published. It holds data only: the
// updater and the install script generator read the same list, so the two can
// never trust different keys.
package trustedkeys

import "slices"

// Key is one trusted minisign public key.
type Key struct {
	// ID is the minisign key id as 16 upper-case hexadecimal digits,
	// zero-padded: the little-endian id bytes of PublicKey read as a uint64.
	// Release signature files are named checksums.txt.<ID>.minisig.
	ID string
	// PublicKey is the base64 minisign public key line, starting "RW".
	PublicKey string
}

// Keys returns the keys this build trusts. Any one valid signature from one
// of them accepts a release, so a key rotation signs with both keys for at
// least two minor releases (ADR 0007).
func Keys() []Key { return slices.Clone(keys) }

// TestBuild reports whether this build trusts the harness test key instead
// of the production keys; a release must never be such a build.
func TestBuild() bool { return testBuild }

// BaseURL is the URL under which the release index (index/releases.json) and
// each version's files (v<version>/) are published.
func BaseURL() string { return baseURL }

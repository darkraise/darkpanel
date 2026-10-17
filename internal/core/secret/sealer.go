package secret

import (
	"bytes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// CSRFLabel is the Derive label of the CSRF token key (spec section 9).
const CSRFLabel = "darkpanel csrf v1"

const (
	sealVersion = 1
	keyIDSize   = 8
	headerSize  = 1 + keyIDSize
)

// Sealer encrypts secrets at rest with XChaCha20-Poly1305 (ADR 0005). A
// ciphertext is the version byte 1, an 8-byte key id (the first 8 bytes of
// SHA-256 of the key), a random 24-byte nonce and the sealed data. The
// associated data binds it to its place, "<table>.<column>.<row id>".
type Sealer struct {
	key   []byte
	keyID [keyIDSize]byte
	aead  cipher.AEAD
}

// NewSealer returns a sealer for a KeySize-byte role key; it keeps its own
// copy of the key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secret: key is %d bytes, want %d", len(key), KeySize)
	}
	k := bytes.Clone(key)
	aead, err := chacha20poly1305.NewX(k)
	if err != nil {
		return nil, fmt.Errorf("secret: sealer: %w", err)
	}
	s := &Sealer{key: k, aead: aead}
	sum := sha256.Sum256(k)
	copy(s.keyID[:], sum[:keyIDSize])
	return s, nil
}

// Seal encrypts plaintext bound to the associated data ad.
func (s *Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
	out := make([]byte, headerSize+chacha20poly1305.NonceSizeX, headerSize+chacha20poly1305.NonceSizeX+len(plaintext)+s.aead.Overhead())
	out[0] = sealVersion
	copy(out[1:headerSize], s.keyID[:])
	nonce := out[headerSize:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return s.aead.Seal(out, nonce, plaintext, []byte(ad)), nil
}

// Open decrypts a ciphertext that Seal produced with the same key and ad. It
// refuses an unknown version, another key's id and any modified byte.
func (s *Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < headerSize+chacha20poly1305.NonceSizeX+s.aead.Overhead() {
		return nil, errors.New("secret: ciphertext too short")
	}
	if ciphertext[0] != sealVersion {
		return nil, fmt.Errorf("secret: ciphertext version %d, want %d", ciphertext[0], sealVersion)
	}
	if !bytes.Equal(ciphertext[1:headerSize], s.keyID[:]) {
		return nil, errors.New("secret: ciphertext was sealed with another key")
	}
	nonce := ciphertext[headerSize : headerSize+chacha20poly1305.NonceSizeX]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext[headerSize+chacha20poly1305.NonceSizeX:], []byte(ad))
	if err != nil {
		return nil, fmt.Errorf("secret: open: %w", err)
	}
	return plaintext, nil
}

// Derive returns a 32-byte subkey for label: HKDF-SHA256 over the role key
// with no salt and the label as info. Each use takes its own label, such as
// CSRFLabel, so no subkey equals another or the role key.
func (s *Sealer) Derive(label string) []byte {
	sub, err := hkdf.Key(sha256.New, s.key, nil, label, 32)
	if err != nil {
		// hkdf.Key fails only for an output longer than 255 hash lengths or
		// under FIPS 140 enforcement; neither applies to 32 bytes of SHA-256.
		panic(fmt.Sprintf("secret: derive %q: %v", label, err))
	}
	return sub
}

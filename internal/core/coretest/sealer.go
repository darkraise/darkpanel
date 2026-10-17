package coretest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

var sealerKey = sha256.Sum256([]byte("darkpanel coretest sealer"))

// Sealer is a core.Sealer using AES-256-GCM under a fixed key. Like the
// production sealer it binds the associated data and detects tampering; its
// key and ciphertext layout are not the production ones. Tests only.
type Sealer struct{}

func (Sealer) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(sealerKey[:])
	if err != nil {
		return nil, fmt.Errorf("coretest sealer: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext bound to ad.
func (s Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("coretest sealer: nonce: %w", err)
	}
	return a.Seal(nonce, nonce, plaintext, []byte(ad)), nil
}

// Open decrypts ciphertext sealed with the same ad.
func (s Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < a.NonceSize() {
		return nil, errors.New("coretest sealer: ciphertext too short")
	}
	n := a.NonceSize()
	out, err := a.Open(nil, ciphertext[:n], ciphertext[n:], []byte(ad))
	if err != nil {
		return nil, fmt.Errorf("coretest sealer: open: %w", err)
	}
	return out, nil
}

// Derive returns HMAC-SHA256 of label under the fixed key.
func (Sealer) Derive(label string) []byte {
	m := hmac.New(sha256.New, sealerKey[:])
	m.Write([]byte(label))
	return m.Sum(nil)
}

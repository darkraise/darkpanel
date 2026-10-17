package coretest

import (
	"bytes"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestSealer(t *testing.T) {
	var s core.Sealer = Sealer{}
	secret := []byte("totp-secret")
	ct, err := s.Seal("core_users.totp_secret_ct.01A", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, secret) {
		t.Error("ciphertext contains the plaintext")
	}
	got, err := s.Open("core_users.totp_secret_ct.01A", ct)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open("core_users.totp_secret_ct.01B", ct); err == nil {
		t.Error("Open with other associated data succeeded")
	}
	ct[len(ct)-1] ^= 1
	if _, err := s.Open("core_users.totp_secret_ct.01A", ct); err == nil {
		t.Error("Open of tampered ciphertext succeeded")
	}
	if _, err := s.Open("x", []byte{1}); err == nil {
		t.Error("Open of a short ciphertext succeeded")
	}
	a, b := s.Derive("darkpanel csrf v1"), s.Derive("other")
	if len(a) != 32 || bytes.Equal(a, b) || !bytes.Equal(a, s.Derive("darkpanel csrf v1")) {
		t.Errorf("Derive: want 32 bytes, stable per label and distinct across labels")
	}
}

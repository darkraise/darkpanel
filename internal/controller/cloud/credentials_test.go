package cloud

import (
	"bytes"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

func crSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	s, err := secret.NewSealer(bytes.Repeat([]byte{9}, secret.KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestCredentialRoundTrip(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, []byte("client-secret")) {
		t.Fatal("the ciphertext contains the secret")
	}
	pt, err := OpenCredential(s, "tailnet_accounts", "secret_ct", "acc1", ct)
	if err != nil || string(pt) != "client-secret" {
		t.Fatalf("open = %q, %v", pt, err)
	}
}

func TestCredentialIsBoundToItsRow(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	for _, place := range [][3]string{
		{"tailnet_accounts", "secret_ct", "acc2"},
		{"tailnet_accounts", "token_ct", "acc1"},
		{"cloudflare_accounts", "secret_ct", "acc1"},
	} {
		if _, err := OpenCredential(s, place[0], place[1], place[2], ct); err == nil {
			t.Errorf("opened at %v, a place it was not sealed for", place)
		}
	}
}

func TestCredentialNeedsItsPlace(t *testing.T) {
	s := crSealer(t)
	if _, err := SealCredential(s, "tailnet_accounts", "", "acc1", []byte("x")); err == nil {
		t.Fatal("sealed without a column")
	}
	if _, err := OpenCredential(s, "", "secret_ct", "acc1", []byte("x")); err == nil {
		t.Fatal("opened without a table")
	}
}

// TestW10PlOpenRefusesDamagedCiphertext shows that a ciphertext with a
// flipped bit or cut short does not open at the place it was sealed for, and
// that the refusal names the place but never the secret.
func TestW10PlOpenRefusesDamagedCiphertext(t *testing.T) {
	s := crSealer(t)
	ct, err := SealCredential(s, "tailnet_accounts", "secret_ct", "acc1", []byte("client-secret"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		b := bytes.Clone(ct)
		b[i] ^= 0x01
		return b
	}
	tests := []struct {
		name string
		ct   []byte
	}{
		{"a flipped bit in the sealed data", flip(len(ct) - 1)},
		{"a flipped bit in the nonce", flip(1 + 8 + 3)},
		{"the last byte cut off", ct[:len(ct)-1]},
		{"the authentication tag cut off", ct[:len(ct)-16]},
		{"only the header", ct[:9]},
		{"nothing", nil},
	}
	const place = "cloud: open tailnet_accounts.secret_ct.acc1: "
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pt, err := OpenCredential(s, "tailnet_accounts", "secret_ct", "acc1", tt.ct)
			if err == nil {
				t.Fatalf("opened %q from a damaged ciphertext", pt)
			}
			if len(pt) != 0 {
				t.Errorf("returned %q with an error", pt)
			}
			if !strings.HasPrefix(err.Error(), place) || strings.Contains(err.Error(), "client-secret") {
				t.Errorf("error %q, want it to start with %q and not carry the secret", err, place)
			}
		})
	}
}

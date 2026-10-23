package cloud

import (
	"bytes"
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

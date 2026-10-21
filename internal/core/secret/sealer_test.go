package secret

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.Sealer = (*Sealer)(nil)

func newTestSealer(t *testing.T, fill byte) *Sealer {
	t.Helper()
	s, err := NewSealer(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestSealerRoundTrip(t *testing.T) {
	s := newTestSealer(t, 1)
	const ad = "core_users.totp_secret_ct.01JABCDEF"
	plaintext := []byte("JBSWY3DPEHPK3PXP")
	ct, err := s.Seal(ad, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	keyID := sha256.Sum256(bytes.Repeat([]byte{1}, KeySize))
	if ct[0] != 1 || !bytes.Equal(ct[1:9], keyID[:8]) {
		t.Errorf("header = %x, want version 1 and key id %x", ct[:9], keyID[:8])
	}
	if want := 1 + 8 + 24 + len(plaintext) + 16; len(ct) != want {
		t.Errorf("ciphertext length = %d, want %d", len(ct), want)
	}
	if bytes.Contains(ct, plaintext) {
		t.Error("ciphertext contains the plaintext")
	}
	got, err := s.Open(ad, ct)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Errorf("Open = %q, %v; want %q", got, err, plaintext)
	}
	again, err := s.Seal(ad, plaintext)
	if err != nil || bytes.Equal(again, ct) {
		t.Errorf("second Seal = %x, %v; want a different nonce", again, err)
	}
}

func TestSealerRefuses(t *testing.T) {
	s := newTestSealer(t, 1)
	const ad = "core_plans.input_ct.01JPLAN"
	ct, err := s.Seal(ad, []byte("input"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		c := bytes.Clone(ct)
		c[i] ^= 1
		return c
	}
	cases := map[string]struct {
		sealer *Sealer
		ad     string
		ct     []byte
	}{
		"other associated data": {s, "core_plans.input_ct.01JOTHER", ct},
		"other key":             {newTestSealer(t, 2), ad, ct},
		"unknown version":       {s, ad, flip(0)},
		"changed key id":        {s, ad, flip(1)},
		"changed nonce":         {s, ad, flip(9)},
		"changed data":          {s, ad, flip(len(ct) - 1)},
		"short":                 {s, ad, ct[:1+8+24+15]},
		"empty":                 {s, ad, nil},
	}
	for name, c := range cases {
		if _, err := c.sealer.Open(c.ad, c.ct); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

// TestSealerOpenChecksTheTag reaches the AEAD check with the wrong key: a
// sealer of key 2 that carries key 1's id passes the key id check.
func TestSealerOpenChecksTheTag(t *testing.T) {
	ct, err := newTestSealer(t, 1).Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	other := newTestSealer(t, 2)
	other.keyID = newTestSealer(t, 1).keyID
	if _, err := other.Open("ad", ct); err == nil || !strings.HasPrefix(err.Error(), "secret: open:") {
		t.Errorf("Open with another key under the same id = %v, want a secret: open error", err)
	}
}

func TestUninitialisedSealerRefuses(t *testing.T) {
	ct, err := newTestSealer(t, 1).Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Sealer{"nil": nil, "zero value": {}} {
		if _, err := s.Seal("ad", []byte("x")); err == nil || err.Error() != "secret: sealer not initialised" {
			t.Errorf("%s: Seal = %v, want secret: sealer not initialised", name, err)
		}
		if _, err := s.Open("ad", ct); err == nil || err.Error() != "secret: sealer not initialised" {
			t.Errorf("%s: Open = %v, want secret: sealer not initialised", name, err)
		}
	}
}

func TestUninitialisedSealerDeriveFailsLoudly(t *testing.T) {
	for name, s := range map[string]*Sealer{"nil": nil, "zero value": {}} {
		func() {
			defer func() {
				if p := recover(); fmt.Sprint(p) != "secret: sealer not initialised" {
					t.Errorf("%s: Derive panicked with %v, want secret: sealer not initialised", name, p)
				}
			}()
			s.Derive(CSRFLabel)
		}()
	}
}

func TestNewSealerRefusesKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewSealer(make([]byte, n)); err == nil {
			t.Errorf("NewSealer accepted a %d-byte key", n)
		}
	}
}

func TestSealerCopiesKey(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := s.Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	if _, err := s.Open("ad", ct); err != nil {
		t.Errorf("Open after the caller cleared its key: %v", err)
	}
}

func TestDerive(t *testing.T) {
	s := newTestSealer(t, 1)
	a := s.Derive(CSRFLabel)
	if len(a) != 32 {
		t.Fatalf("subkey length = %d, want 32", len(a))
	}
	if !bytes.Equal(a, s.Derive(CSRFLabel)) {
		t.Error("Derive is not deterministic")
	}
	if bytes.Equal(a, s.Derive("darkpanel other v1")) {
		t.Error("two labels derived the same subkey")
	}
	if bytes.Equal(a, newTestSealer(t, 2).Derive(CSRFLabel)) {
		t.Error("two keys derived the same subkey")
	}
	if bytes.Equal(a, bytes.Repeat([]byte{1}, KeySize)) {
		t.Error("Derive returned the role key")
	}
}

// TestDeriveIsHKDF pins the construction of spec section 5, HKDF-SHA256 with
// no salt and the label as info, computed here from RFC 5869's definition.
func TestDeriveIsHKDF(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	extract := hmac.New(sha256.New, make([]byte, sha256.Size))
	extract.Write(key)
	expand := hmac.New(sha256.New, extract.Sum(nil))
	expand.Write([]byte(CSRFLabel))
	expand.Write([]byte{1})
	if got, want := newTestSealer(t, 1).Derive(CSRFLabel), expand.Sum(nil); !bytes.Equal(got, want) {
		t.Errorf("Derive = %x, want %x", got, want)
	}
}

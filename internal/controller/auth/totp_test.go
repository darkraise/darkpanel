package auth

import (
	"testing"
	"time"
)

// rfcKey is the SHA-1 seed of RFC 6238 appendix B.
var rfcKey = []byte("12345678901234567890")

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	// The RFC's eight-digit values, truncated to their last six digits: the
	// code is the same dynamic binary code modulo 10^6.
	for _, tt := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		if got := totpCode(rfcKey, totpStep(time.Unix(tt.unix, 0))); got != tt.want {
			t.Errorf("code at %d = %s, want %s", tt.unix, got, tt.want)
		}
	}
}

func TestTOTPCodeFromEncodedSecret(t *testing.T) {
	secret := encodeTOTPSecret(rfcKey)
	if secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" { //nolint:gosec // G101: the RFC 6238 test seed, not a credential
		t.Fatalf("encoded secret = %s, want unpadded base32", secret)
	}
	got, err := TOTPCode(secret, time.Unix(59, 0))
	if err != nil || got != "287082" {
		t.Fatalf("TOTPCode = %q, %v; want 287082", got, err)
	}
	if _, err := TOTPCode("not base32!", time.Unix(59, 0)); err == nil {
		t.Error("TOTPCode accepted an invalid secret")
	}
}

func TestNewTOTPSecret(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	if len(a) != TOTPSecretSize || len(b) != TOTPSecretSize {
		t.Fatalf("secret sizes %d and %d, want %d", len(a), len(b), TOTPSecretSize)
	}
	if string(a) == string(b) {
		t.Error("two secrets are equal")
	}
}

func TestMatchTOTP(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := totpStep(now)
	code := func(s int64) string { return totpCode(rfcKey, s) }
	for _, tt := range []struct {
		name     string
		code     string
		lastStep int64
		want     int64
		ok       bool
	}{
		{"current step", code(step), 0, step, true},
		{"previous step", code(step - 1), 0, step - 1, true},
		{"next step", code(step + 1), 0, step + 1, true},
		{"two steps old", code(step - 2), 0, 0, false},
		{"two steps ahead", code(step + 2), 0, 0, false},
		{"replay of the last accepted step", code(step), step, 0, false},
		{"step before the last accepted", code(step - 1), step, 0, false},
		{"step after the last accepted", code(step + 1), step, step + 1, true},
		{"five digits", code(step)[1:], 0, 0, false},
		{"seven digits", code(step) + "0", 0, 0, false},
		{"not digits", "12345a", 0, 0, false},
		{"empty", "", 0, 0, false},
	} {
		got, ok := matchTOTP(rfcKey, tt.code, now, tt.lastStep)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: matchTOTP = %d, %v; want %d, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}

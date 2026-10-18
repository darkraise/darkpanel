package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 TOTP with SHA-1 is what authenticator apps implement (spec section 8)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"time"
)

// TOTP parameters (spec section 8, RFC 6238).
const (
	TOTPSecretSize = 20
	TOTPPeriod     = 30 * time.Second
	totpDigits     = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() []byte {
	s := make([]byte, TOTPSecretSize)
	_, _ = rand.Read(s)
	return s
}

// encodeTOTPSecret renders key as unpadded base32, the form enrollment URIs
// and authenticator apps take.
func encodeTOTPSecret(key []byte) string { return b32.EncodeToString(key) }

func totpStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod/time.Second) }

func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // G115: steps of real times are positive
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, bin%1000000)
}

// TOTPCode returns the code for the unpadded base32 secret at t, as an
// authenticator app shows it.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: TOTP secret: %w", err)
	}
	return totpCode(key, totpStep(t)), nil
}

// matchTOTP checks code against the steps around now, one either side, and
// returns the matching step. Steps at or before lastStep are never accepted,
// so a code cannot be replayed.
func matchTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	cur := totpStep(now)
	for s := cur - 1; s <= cur+1; s++ {
		if s <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}

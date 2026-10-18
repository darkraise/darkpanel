// Package auth is the controller's identity: users with argon2id passwords
// and TOTP, sessions with CSRF tokens, login rate limits and scoped API tokens
// (spec sections 8 and 9).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Params are argon2id parameters. Memory is in KiB.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     int
	KeyLen      uint32
}

// DefaultParams are the production parameters (spec section 8). Stored hashes
// carry their own parameters, so changing these affects only new hashes.
var DefaultParams = Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// MaxConcurrentHashes bounds the argon2id hashes running at once; each holds
// Params.Memory, so the bound is what keeps a login flood from exhausting
// memory.
const MaxConcurrentHashes = 2

// Password length bounds, in characters.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var b64 = base64.RawStdEncoding

type hasher struct {
	params  Params
	sem     chan struct{}
	dummy   string
	compute func(password, salt []byte, p Params) []byte
}

func argon2id(password, salt []byte, p Params) []byte {
	return argon2.IDKey(password, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)
}

// newHasher returns a hasher for p. It hashes a random password once, without
// waiting for a slot, to make the dummy hash that verifyDummy checks against.
func newHasher(p Params) *hasher {
	h := &hasher{params: p, sem: make(chan struct{}, MaxConcurrentHashes), compute: argon2id}
	pw := make([]byte, 32)
	_, _ = rand.Read(pw)
	h.dummy = h.encode(pw)
	return h
}

func (h *hasher) encode(password []byte) string {
	salt := make([]byte, h.params.SaltLen)
	_, _ = rand.Read(salt)
	key := h.compute(password, salt, h.params)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		h.params.Memory, h.params.Iterations, h.params.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// acquire waits for a hashing slot or for ctx to end.
func (h *hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *hasher) release() { <-h.sem }

// hash returns password's PHC string:
// $argon2id$v=19$m=<KiB>,t=<iterations>,p=<parallelism>$<salt>$<key>, salt and
// key in unpadded standard base64.
func (h *hasher) hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return h.encode([]byte(password)), nil
}

// verify reports whether password matches phc, using the parameters stored in
// phc. A malformed phc is an error.
func (h *hasher) verify(ctx context.Context, phc, password string) (bool, error) {
	if _, _, _, err := parsePHC(phc); err != nil {
		return false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	return h.verifyHeld(phc, password)
}

// verifyHeld is verify for a caller that already holds a slot.
func (h *hasher) verifyHeld(phc, password string) (bool, error) {
	p, salt, want, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	got := h.compute([]byte(password), salt, p)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// verifyDummy runs one verification against a hash no password matches, so a
// login for an unknown username costs what a real one costs.
func (h *hasher) verifyDummy(ctx context.Context, password string) error {
	_, err := h.verify(ctx, h.dummy, password)
	return err
}

// verifyDummyHeld is verifyDummy for a caller that already holds a slot.
func (h *hasher) verifyDummyHeld(password string) error {
	_, err := h.verifyHeld(h.dummy, password)
	return err
}

func parsePHC(phc string) (Params, []byte, []byte, error) {
	bad := errors.New("auth: malformed password hash")
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return Params{}, nil, nil, bad
	}
	var p Params
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism)
	if err != nil || p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 ||
		fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Iterations, p.Parallelism) != parts[3] {
		return Params{}, nil, nil, bad
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, bad
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, bad
	}
	p.SaltLen = len(salt)
	p.KeyLen = uint32(len(key)) //nolint:gosec // G115: a decoded hash field is far below 4 GiB
	return p, salt, key, nil
}

// checkUsername refuses a username that does not match spec section 8's
// pattern, with a field error at /username.
func checkUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid username",
			Fields: []errcode.FieldError{{Path: "/username", Message: "must match ^[a-z0-9][a-z0-9._-]{0,63}$"}}}
	}
	return nil
}

// checkPassword refuses a password outside the length bounds, with a field
// error at /password that never quotes the value.
func checkPassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid password",
			Fields: []errcode.FieldError{{Path: "/password", Message: fmt.Sprintf("must be %d to %d characters", MinPasswordLength, MaxPasswordLength)}}}
	}
	return nil
}

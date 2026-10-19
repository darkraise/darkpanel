package transport

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TokenPrefix starts every enrollment token; the digit is the format version.
const TokenPrefix = "dpe1."

// secretSize is the length of a token's secret in bytes: 128 bits, so a
// plain SHA-256 is enough to store it.
const secretSize = 16

var fingerprintPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

// Token is an enrollment token's content. It tells the agent where to dial
// and which root and controller identity to accept before it sends
// anything, and gives the controller the host and the secret to check.
type Token struct {
	Address       string `json:"a"` // host:port the agent dials
	Fingerprint   string `json:"f"` // Fingerprint of the agent root
	ControllerSAN string `json:"s"` // ControllerURI of the controller id
	HostID        string `json:"h"`
	Secret        string `json:"t"` // 16 random bytes, base64url without padding
}

// String is the token as the operator copies it.
func (t Token) String() string {
	// A struct of strings always encodes.
	raw, _ := json.Marshal(t) //nolint:gosec // G117: the token exists to carry its secret
	return TokenPrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// SecretHash is the SHA-256 of the decoded secret, the value
// core_enroll_tokens.hash stores.
func (t Token) SecretHash() ([]byte, error) {
	secret, err := base64.RawURLEncoding.Strict().DecodeString(t.Secret)
	if err != nil || len(secret) != secretSize {
		return nil, malformed()
	}
	sum := sha256.Sum256(secret)
	return sum[:], nil
}

// ParseToken reads a token the operator pasted. Every refusal is the same
// enroll.token_invalid, so a probe learns nothing about which part failed.
func ParseToken(s string) (Token, error) {
	body, ok := strings.CutPrefix(strings.TrimSpace(s), TokenPrefix)
	if !ok {
		return Token{}, malformed()
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil {
		return Token{}, malformed()
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || len(members) != 5 {
		return Token{}, malformed()
	}
	values := make(map[string]string, len(members))
	for _, key := range []string{"a", "f", "s", "h", "t"} {
		m, ok := members[key]
		if !ok {
			return Token{}, malformed()
		}
		// A null member decodes without error and leaves v empty.
		var v string
		if err := json.Unmarshal(m, &v); err != nil || v == "" {
			return Token{}, malformed()
		}
		values[key] = v
	}
	t := Token{Address: values["a"], Fingerprint: values["f"], ControllerSAN: values["s"], HostID: values["h"], Secret: values["t"]}
	controllerID, isController := strings.CutPrefix(t.ControllerSAN, controllerURIPrefix)
	if _, _, err := net.SplitHostPort(t.Address); err != nil ||
		!fingerprintPattern.MatchString(t.Fingerprint) ||
		!isController || !idPattern.MatchString(controllerID) ||
		!idPattern.MatchString(t.HostID) {
		return Token{}, malformed()
	}
	if _, err := t.SecretHash(); err != nil {
		return Token{}, err
	}
	return t, nil
}

func malformed() error {
	return errcode.New(errcode.EnrollTokenInvalid, "the enrollment token is malformed")
}

// EnrollRequest is the body of POST EnrollPath.
type EnrollRequest struct {
	Token string `json:"token"`
	CSR   string `json:"csr"` // PEM "CERTIFICATE REQUEST"
}

// EnrollResponse answers EnrollRequest.
type EnrollResponse struct {
	Certificate string `json:"certificate"` // PEM
	CA          string `json:"ca"`          // PEM of the agent root
}

// RenewRequest is the body of POST RenewPath.
type RenewRequest struct {
	CSR string `json:"csr"`
}

// RenewResponse answers RenewRequest.
type RenewResponse struct {
	Certificate string `json:"certificate"`
}

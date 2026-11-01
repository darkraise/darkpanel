package transport

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/netip"
	"regexp"
	"strconv"
	"strings"
	"unicode"

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

// BundlePrefix starts a version-2 bootstrap bundle; "dpb" and the digit are the format and its version.
const BundlePrefix = "dpb2."

var bundleVersionPattern = regexp.MustCompile(`^dpb([0-9]+)\.`)

// The Tailscale address ranges; this package cannot import the tailscale module's link package.
var (
	tailnetIPv4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetIPv6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Bundle is a bootstrap bundle: an enrollment token plus what a host needs to join the tailnet first (spec 4.2).
// JSON members: {"v": 2, "token": <Token.String()>, "tailnetKey": <auth key>, "controllerTailnetAddr": <host:port>}.
type Bundle struct {
	Token                 Token
	TailnetKey            string // a Tailscale auth key, "tskey-auth-..."; never empty
	ControllerTailnetAddr string // host:port inside 100.64.0.0/10 or fd7a:115c:a1e0::/48 (IPv6 in brackets)
}

type bundleJSON struct {
	V                     int    `json:"v"`
	Token                 string `json:"token"`
	TailnetKey            string `json:"tailnetKey"`
	ControllerTailnetAddr string `json:"controllerTailnetAddr"`
}

// String is the bundle as the operator copies it: BundlePrefix + base64url (no padding) of the JSON above.
func (b Bundle) String() string {
	// A struct of strings and an int always encodes.
	raw, _ := json.Marshal(bundleJSON{
		V:                     2,
		Token:                 b.Token.String(),
		TailnetKey:            b.TailnetKey,
		ControllerTailnetAddr: b.ControllerTailnetAddr,
	})
	return BundlePrefix + base64.RawURLEncoding.EncodeToString(raw)
}

// ParseBootstrap reads what the operator pasted: a plain token (TokenPrefix) gives (token, nil, nil);
// a BundlePrefix bundle gives (bundle.Token, &bundle, nil). "dpb<digits>." with a version other than 2 is refused
// with enroll.token_invalid and the detail "bootstrap bundle version <n> is not supported by this darkpanel";
// every other malformed input is refused with ParseToken's own error (enroll.token_invalid, "the enrollment token is malformed").
// A bundle must hold exactly the four members, v == 2, a token ParseToken accepts, a non-empty tailnetKey without
// whitespace, and a controllerTailnetAddr that net.SplitHostPort accepts, whose host is a Tailscale address and
// whose port is 1-65535.
func ParseBootstrap(s string) (Token, *Bundle, error) {
	s = strings.TrimSpace(s)
	switch {
	case strings.HasPrefix(s, TokenPrefix):
		t, err := ParseToken(s)
		return t, nil, err
	case strings.HasPrefix(s, BundlePrefix):
		b, ok := parseBundle(strings.TrimPrefix(s, BundlePrefix))
		if !ok {
			return Token{}, nil, malformed()
		}
		return b.Token, &b, nil
	}
	if m := bundleVersionPattern.FindStringSubmatch(s); m != nil {
		return Token{}, nil, errcode.New(errcode.EnrollTokenInvalid,
			"bootstrap bundle version "+m[1]+" is not supported by this darkpanel")
	}
	return Token{}, nil, malformed()
}

// parseBundle reads a version-2 bundle's body; it reports no reason, so no refusal can echo the input.
func parseBundle(body string) (Bundle, bool) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(body)
	if err != nil {
		return Bundle{}, false
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || len(members) != 4 || !distinctMembers(raw) {
		return Bundle{}, false
	}
	var version int
	if m, ok := members["v"]; !ok || json.Unmarshal(m, &version) != nil || version != 2 {
		return Bundle{}, false
	}
	values := make(map[string]string, 3)
	for _, key := range []string{"token", "tailnetKey", "controllerTailnetAddr"} {
		m, ok := members[key]
		if !ok {
			return Bundle{}, false
		}
		// A null member decodes without error and leaves v empty, which the checks below refuse.
		var v string
		if err := json.Unmarshal(m, &v); err != nil {
			return Bundle{}, false
		}
		values[key] = v
	}
	tok, err := ParseToken(values["token"])
	if err != nil {
		return Bundle{}, false
	}
	key := values["tailnetKey"]
	if key == "" || strings.IndexFunc(key, unicode.IsSpace) >= 0 || !isTailnetHostPort(values["controllerTailnetAddr"]) {
		return Bundle{}, false
	}
	return Bundle{Token: tok, TailnetKey: key, ControllerTailnetAddr: values["controllerTailnetAddr"]}, true
}

// distinctMembers reports whether raw, a JSON object, names each member once: json.Unmarshal keeps the last of two
// equal names, so a duplicate would pass the member count.
func distinctMembers(raw []byte) bool {
	dec := json.NewDecoder(bytes.NewReader(raw))
	if t, err := dec.Token(); err != nil || t != json.Delim('{') {
		return false
	}
	seen := map[string]bool{}
	for dec.More() {
		t, err := dec.Token()
		name, ok := t.(string)
		if err != nil || !ok || seen[name] {
			return false
		}
		seen[name] = true
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return false
		}
	}
	return true
}

// isTailnetHostPort reports whether s is an IP literal inside a Tailscale range with a port from 1 to 65535.
func isTailnetHostPort(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return false
	}
	addr, err := netip.ParseAddr(host)
	// Unmap drops the zone of an IPv4-mapped address, so the zone is refused before it.
	if err != nil || addr.Zone() != "" {
		return false
	}
	addr = addr.Unmap()
	if !tailnetIPv4.Contains(addr) && !tailnetIPv6.Contains(addr) {
		return false
	}
	n, err := strconv.ParseUint(port, 10, 16)
	return err == nil && n != 0
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

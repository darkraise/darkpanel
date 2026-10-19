package transport

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"math/big"
	"net"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// namedCert returns a self-signed certificate carrying the given URI, DNS
// and IP names.
func namedCert(t *testing.T, uris, dns []string, ips []net.IP) *x509.Certificate {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     dns,
		IPAddresses:  ips,
	}
	for _, s := range uris {
		u, err := url.Parse(s)
		if err != nil {
			t.Fatal(err)
		}
		tmpl.URIs = append(tmpl.URIs, u)
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestURIs(t *testing.T) {
	if got := HostURI("01HOST"); got != "darkpanel://host/01HOST" {
		t.Errorf("HostURI = %q", got)
	}
	if got := ControllerURI("01CTRL"); got != "darkpanel://controller/01CTRL" {
		t.Errorf("ControllerURI = %q", got)
	}
}

func TestHostIDOf(t *testing.T) {
	long := strings.Repeat("A", 64)
	tests := []struct {
		name   string
		uris   []string
		dns    []string
		ips    []net.IP
		wantID string // empty: refused
	}{
		{"host", []string{"darkpanel://host/01JABCDEF"}, nil, nil, "01JABCDEF"},
		{"64 characters", []string{"darkpanel://host/" + long}, nil, nil, long},
		{"65 characters", []string{"darkpanel://host/" + long + "A"}, nil, nil, ""},
		{"no URI", nil, nil, nil, ""},
		{"two URIs", []string{"darkpanel://host/A1", "darkpanel://host/B2"}, nil, nil, ""},
		{"controller URI", []string{"darkpanel://controller/A1"}, nil, nil, ""},
		{"empty id", []string{"darkpanel://host/"}, nil, nil, ""},
		{"nested path", []string{"darkpanel://host/A1/B2"}, nil, nil, ""},
		{"punctuation", []string{"darkpanel://host/A-1"}, nil, nil, ""},
		{"query", []string{"darkpanel://host/A1?x=1"}, nil, nil, ""},
		{"fragment", []string{"darkpanel://host/A1#x"}, nil, nil, ""},
		{"user info", []string{"darkpanel://u@host/A1"}, nil, nil, ""},
		{"port", []string{"darkpanel://host:1/A1"}, nil, nil, ""},
		{"other scheme", []string{"https://host/A1"}, nil, nil, ""},
		{"escaped path", []string{"darkpanel://host/A%31"}, nil, nil, ""},
		{"DNS name too", []string{"darkpanel://host/A1"}, []string{"agent.example"}, nil, ""},
		{"IP address too", []string{"darkpanel://host/A1"}, nil, []net.IP{net.IPv4(127, 0, 0, 1)}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			id, err := HostIDOf(namedCert(t, tt.uris, tt.dns, tt.ips))
			switch {
			case tt.wantID == "" && err == nil:
				t.Fatalf("HostIDOf = %q, want an error", id)
			case tt.wantID != "" && (err != nil || id != tt.wantID):
				t.Fatalf("HostIDOf = %q, %v; want %q", id, err, tt.wantID)
			}
		})
	}
}

func TestSerialAndFingerprint(t *testing.T) {
	serial, _ := new(big.Int).SetString("00ab01", 16)
	if got := SerialText(serial); got != "ab01" {
		t.Errorf("SerialText = %q, want ab01", got)
	}
	cert := namedCert(t, nil, nil, nil)
	sum := sha256.Sum256(cert.Raw)
	if got := Fingerprint(cert); got != hex.EncodeToString(sum[:]) {
		t.Errorf("Fingerprint = %q", got)
	}
}

// validToken is a well-formed token with a 16-byte secret.
func validToken() Token {
	return Token{
		Address:       "controller.example:7443",
		Fingerprint:   strings.Repeat("ab", 32),
		ControllerSAN: ControllerURI("01JCONTROLLER"),
		HostID:        "01JHOST",
		Secret:        base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef")),
	}
}

// encode wraps a JSON text as a token.
func encode(json string) string {
	return TokenPrefix + base64.RawURLEncoding.EncodeToString([]byte(json))
}

func TestTokenRoundTrip(t *testing.T) {
	want := validToken()
	text := want.String()
	if !strings.HasPrefix(text, "dpe1.") || strings.ContainsAny(text, "=+/") {
		t.Fatalf("String = %q, want dpe1. and base64url without padding", text)
	}
	got, err := ParseToken("  " + text + "\n")
	if err != nil || got != want {
		t.Fatalf("ParseToken = %+v, %v; want %+v", got, err, want)
	}
	hash, err := got.SecretHash()
	sum := sha256.Sum256([]byte("0123456789abcdef"))
	if err != nil || string(hash) != string(sum[:]) {
		t.Fatalf("SecretHash = %x, %v; want %x", hash, err, sum)
	}
}

func TestParseTokenRefuses(t *testing.T) {
	f := strings.Repeat("ab", 32)
	secret := base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
	member := func(a, f, s, h, t string) string {
		return `{"a":"` + a + `","f":"` + f + `","s":"` + s + `","h":"` + h + `","t":"` + t + `"}`
	}
	ctrl := ControllerURI("01JC")
	valid := validToken().String()
	tests := []struct{ name, text string }{
		{"empty", ""},
		{"wrong prefix", "dpe2." + strings.TrimPrefix(valid, TokenPrefix)},
		{"no prefix", strings.TrimPrefix(valid, TokenPrefix)},
		{"padded base64", valid + "=="},
		{"standard base64", TokenPrefix + base64.StdEncoding.EncodeToString([]byte(member("c:1", f, ctrl, "H1", secret)))},
		{"not JSON", encode("not json")},
		{"JSON array", encode(`["a"]`)},
		{"trailing JSON", encode(member("c:1", f, ctrl, "H1", secret) + "{}")},
		{"missing member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1"}`)},
		{"extra member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1","t":"` + secret + `","x":"y"}`)},
		{"number member", encode(`{"a":"c:1","f":"` + f + `","s":"` + ctrl + `","h":"H1","t":7}`)},
		{"null member", encode(`{"a":null,"f":"` + f + `","s":"` + ctrl + `","h":"H1","t":"` + secret + `"}`)},
		{"empty address", encode(member("", f, ctrl, "H1", secret))},
		{"address without port", encode(member("controller", f, ctrl, "H1", secret))},
		{"upper-case fingerprint", encode(member("c:1", strings.ToUpper(f), ctrl, "H1", secret))},
		{"short fingerprint", encode(member("c:1", f[:62], ctrl, "H1", secret))},
		{"host SAN", encode(member("c:1", f, HostURI("H1"), "H1", secret))},
		{"bare controller SAN", encode(member("c:1", f, ControllerURI(""), "H1", secret))},
		{"bad host id", encode(member("c:1", f, ctrl, "H-1", secret))},
		{"short secret", encode(member("c:1", f, ctrl, "H1", base64.RawURLEncoding.EncodeToString([]byte("short"))))},
		{"padded secret", encode(member("c:1", f, ctrl, "H1", secret+"=="))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tok, err := ParseToken(tt.text)
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseToken = %+v, %v; want %s", tok, err, errcode.EnrollTokenInvalid)
			}
		})
	}
}

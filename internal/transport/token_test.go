package transport

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const bdKey = "tskey-auth-kBdTest1CNTRL-0123456789abcdefghij"

const bdMalformed = "the enrollment token is malformed"

// bdBundle is a well-formed bundle whose token dials the controller's tailnet address.
func bdBundle() Bundle {
	tok := validToken()
	tok.Address = "100.64.0.7:7443"
	return Bundle{Token: tok, TailnetKey: bdKey, ControllerTailnetAddr: "100.64.0.7:7443"}
}

// bdEncode wraps a JSON text as a version-2 bundle.
func bdEncode(js string) string {
	return BundlePrefix + base64.RawURLEncoding.EncodeToString([]byte(js))
}

// bdJSON builds a bundle's JSON text from raw member values, so a case can put any JSON type in any member.
func bdJSON(v, token, key, addr string) string {
	return `{"v":` + v + `,"token":` + token + `,"tailnetKey":` + key + `,"controllerTailnetAddr":` + addr + `}`
}

// bdQuote is s as a JSON string.
func bdQuote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// bdRefused fails the test unless ParseBootstrap refused with enroll.token_invalid and the given detail, returning
// nothing and leaking nothing of the bundle.
func bdRefused(t *testing.T, input, detail string) {
	t.Helper()
	tok, b, err := ParseBootstrap(input)
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid || coded.Detail != detail {
		t.Fatalf("ParseBootstrap(%q) = %+v, %+v, %v; want %s %q", input, tok, b, err, errcode.EnrollTokenInvalid, detail)
	}
	if tok != (Token{}) || b != nil {
		t.Fatalf("ParseBootstrap(%q) returned %+v, %+v with an error", input, tok, b)
	}
	if strings.Contains(err.Error(), bdKey) {
		t.Fatalf("the error %q carries the tailnet key", err)
	}
}

func TestBdBundleRoundTrip(t *testing.T) {
	want := bdBundle()
	text := want.String()
	if !strings.HasPrefix(text, "dpb2.") || strings.ContainsAny(text, "=+/") {
		t.Fatalf("String = %q, want dpb2. and base64url without padding", text)
	}
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimPrefix(text, BundlePrefix))
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]any
	if err := json.Unmarshal(raw, &members); err != nil {
		t.Fatal(err)
	}
	if len(members) != 4 || members["v"] != float64(2) || members["token"] != want.Token.String() ||
		members["tailnetKey"] != want.TailnetKey || members["controllerTailnetAddr"] != want.ControllerTailnetAddr {
		t.Fatalf("bundle JSON = %s", raw)
	}
	tok, got, err := ParseBootstrap("  " + text + "\n")
	if err != nil || got == nil || *got != want || tok != want.Token {
		t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v", tok, got, err, want)
	}
}

func TestBdParseBootstrapPlainToken(t *testing.T) {
	want := validToken()
	tok, b, err := ParseBootstrap(" " + want.String() + "\n")
	if err != nil || b != nil || tok != want {
		t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v, nil, nil", tok, b, err, want)
	}
}

func TestBdParseBootstrapAcceptsTailnetAddresses(t *testing.T) {
	for _, addr := range []string{
		"100.64.0.0:7443",
		"100.127.255.255:1",
		"100.100.100.100:65535",
		"[fd7a:115c:a1e0::1]:7443",
		"[fd7a:115c:a1e0:ffff:ffff:ffff:ffff:ffff]:7443",
	} {
		t.Run(addr, func(t *testing.T) {
			want := bdBundle()
			want.ControllerTailnetAddr = addr
			tok, got, err := ParseBootstrap(want.String())
			if err != nil || got == nil || *got != want || tok != want.Token {
				t.Fatalf("ParseBootstrap = %+v, %+v, %v; want %+v", tok, got, err, want)
			}
		})
	}
}

func TestBdParseBootstrapRefuses(t *testing.T) {
	tok := bdQuote(validToken().String())
	key := bdQuote(bdKey)
	addr := bdQuote("100.64.0.7:7443")
	good := bdJSON("2", tok, key, addr)
	withRawAddr := func(a string) string { return bdEncode(bdJSON("2", tok, key, a)) }
	withAddr := func(a string) string { return withRawAddr(bdQuote(a)) }
	withKey := func(k string) string { return bdEncode(bdJSON("2", tok, k, addr)) }
	withToken := func(k string) string { return bdEncode(bdJSON("2", k, key, addr)) }
	withVersion := func(v string) string { return bdEncode(bdJSON(v, tok, key, addr)) }
	padded := BundlePrefix + base64.URLEncoding.EncodeToString([]byte(good))
	if !strings.HasSuffix(padded, "=") {
		t.Fatal("the padded case needs a JSON text whose length is not a multiple of three")
	}

	tests := []struct{ name, text string }{
		{"empty", ""},
		{"blank", "  \n"},
		{"prefix only", BundlePrefix},
		{"upper-case prefix", "DPB2." + strings.TrimPrefix(bdEncode(good), BundlePrefix)},
		{"no dot", "dpb2" + strings.TrimPrefix(bdEncode(good), BundlePrefix)},
		{"padded base64", padded},
		{"not base64", BundlePrefix + "!!!"},
		{"not JSON", bdEncode("not json")},
		{"JSON array", bdEncode(`["a"]`)},
		{"trailing JSON", bdEncode(good + "{}")},
		{"missing tailnetKey", bdEncode(`{"v":2,"token":` + tok + `,"controllerTailnetAddr":` + addr + `}`)},
		{"extra member", bdEncode(strings.TrimSuffix(good, "}") + `,"x":1}`)},
		{"member v is 3", withVersion("3")},
		{"member v is a string", withVersion(`"2"`)},
		{"member v is null", withVersion("null")},
		{"member v is 2.5", withVersion("2.5")},
		{"member v is 2.0", withVersion("2.0")},
		{"token is a number", withToken("7")},
		{"token is null", withToken("null")},
		{"token is malformed", withToken(bdQuote("dpe1.AAAA"))},
		{"token is a bundle", withToken(bdQuote(bdBundle().String()))},
		{"token without its prefix", withToken(bdQuote(strings.TrimPrefix(validToken().String(), TokenPrefix)))},
		{"key is empty", withKey(bdQuote(""))},
		{"key is null", withKey("null")},
		{"key is a number", withKey("7")},
		{"key has a space", withKey(bdQuote("tskey-auth-a b"))},
		{"key has a newline", withKey(bdQuote("tskey-auth-a\nb"))},
		{"key has a tab", withKey(bdQuote("tskey-auth-a\tb"))},
		{"address is empty", withAddr("")},
		{"address is null", withRawAddr("null")},
		{"address is a number", withRawAddr("7")},
		{"address has no port", withAddr("100.64.0.7")},
		{"port is empty", withAddr("100.64.0.7:")},
		{"port is zero", withAddr("100.64.0.7:0")},
		{"port is 65536", withAddr("100.64.0.7:65536")},
		{"port is a name", withAddr("100.64.0.7:http")},
		{"port is signed", withAddr("100.64.0.7:+80")},
		{"host is a name", withAddr("controller.example:7443")},
		{"public IPv4", withAddr("203.0.113.5:7443")},
		{"loopback IPv4", withAddr("127.0.0.1:7443")},
		{"just below 100.64.0.0/10", withAddr("100.63.255.255:7443")},
		{"just above 100.64.0.0/10", withAddr("100.128.0.0:7443")},
		{"IPv6 without brackets", withAddr("fd7a:115c:a1e0::1:7443")},
		{"IPv6 outside fd7a:115c:a1e0::/48", withAddr("[fd7a:115c:a1e1::1]:7443")},
		{"IPv6 loopback", withAddr("[::1]:7443")},
		{"IPv6 with a zone", withAddr("[fd7a:115c:a1e0::1%eth0]:7443")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) { bdRefused(t, tt.text, bdMalformed) })
	}
}

func TestBdParseBootstrapRefusesOtherVersions(t *testing.T) {
	body := base64.RawURLEncoding.EncodeToString([]byte(bdJSON("3", bdQuote(validToken().String()), bdQuote(bdKey), bdQuote("100.64.0.7:7443"))))
	tests := []struct{ prefix, version, body string }{
		{"dpb1.", "1", body},
		{"dpb3.", "3", body},
		{"dpb3.", "3", "not base64 at all"},
		{"dpb3.", "3", ""},
		{"dpb10.", "10", body},
		{"dpb02.", "02", body},
		{"dpb0.", "0", body},
		{"dpb99999999999999999999.", "99999999999999999999", body},
	}
	for _, tt := range tests {
		t.Run(tt.prefix+tt.body[:min(len(tt.body), 4)], func(t *testing.T) {
			want := "bootstrap bundle version " + tt.version + " is not supported by this darkpanel"
			bdRefused(t, tt.prefix+tt.body, want)
			bdRefused(t, "  "+tt.prefix+tt.body+"\n", want)
		})
	}
	for _, text := range []string{"dpb.", "dpb.x", "dpbx.x", "dpb3", "dpb-3.x", "dpe2.x", "dpb 3.x"} {
		t.Run(text, func(t *testing.T) { bdRefused(t, text, bdMalformed) })
	}
}

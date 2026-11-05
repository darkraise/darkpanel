package transport

import (
	"errors"
	"net"
	"net/netip"
	"strings"
	"testing"
	"unicode"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// FuzzParseBootstrap feeds arbitrary input to ParseBootstrap: it must never panic, every refusal is
// enroll.token_invalid, a plain token is what ParseToken reads, and an accepted bundle holds a key without
// whitespace and a Tailscale controller address and round-trips through String and ParseBootstrap.
func FuzzParseBootstrap(f *testing.F) {
	bundle := bdBundle()
	v3 := strings.Replace(bdEncode(bdJSON("3", bdQuote(bundle.Token.String()), bdQuote(bdKey), bdQuote(bundle.ControllerTailnetAddr))), BundlePrefix, "dpb3.", 1)
	f.Add(validToken().String())
	f.Add(bundle.String())
	f.Add(v3)
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		tok, b, err := ParseBootstrap(in)
		if err != nil {
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseBootstrap(%q) error = %v, want enroll.token_invalid", in, err)
			}
			return
		}
		if b == nil {
			again, err := ParseToken(in)
			if err != nil || again != tok {
				t.Fatalf("ParseBootstrap(%q) accepted a plain token that ParseToken reads as %+v, %v", in, again, err)
			}
			return
		}
		if tok != b.Token {
			t.Fatalf("ParseBootstrap(%q) returned token %+v beside bundle token %+v", in, tok, b.Token)
		}
		if b.TailnetKey == "" || strings.IndexFunc(b.TailnetKey, unicode.IsSpace) >= 0 {
			t.Fatalf("accepted tailnet key %q", b.TailnetKey)
		}
		host, _, err := net.SplitHostPort(b.ControllerTailnetAddr)
		addr, perr := netip.ParseAddr(host)
		inTailnet := netip.MustParsePrefix("100.64.0.0/10").Contains(addr.Unmap()) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(addr.Unmap())
		if err != nil || perr != nil || !inTailnet {
			t.Fatalf("accepted controller address %q", b.ControllerTailnetAddr)
		}
		tok2, b2, err := ParseBootstrap(b.String())
		if err != nil || b2 == nil || *b2 != *b || tok2 != tok {
			t.Fatalf("round trip of %+v gave %+v, %+v, %v", *b, tok2, b2, err)
		}
	})
}

func fz9Head(s string) string {
	if len(s) > 64 {
		return s[:64]
	}
	return s
}

// FuzzParseToken feeds arbitrary input to ParseToken: every refusal is enroll.token_invalid, and an accepted
// token prints with String and parses back to itself.
func FuzzParseToken(f *testing.F) {
	tok := validToken()
	f.Add(tok.String())
	f.Add("  " + tok.String() + "\n")
	f.Add(bdBundle().String())
	f.Add(TokenPrefix + "e30")
	f.Add(encode(`{"a":"controller.example:7443"}`))
	f.Add(encode(`{"a":null,"f":"x","s":"y","h":"z","t":"w"}`))
	f.Add(encode(`{"a":"a<b>:1","f":"` + strings.Repeat("ab", 32) + `","s":"` + ControllerURI("01JCONTROLLER") + `","h":"01JHOST","t":"MDEyMzQ1Njc4OWFiY2RlZg"}`))
	f.Add("")
	f.Fuzz(func(t *testing.T, in string) {
		got, err := ParseToken(in)
		if err != nil {
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.EnrollTokenInvalid {
				t.Fatalf("ParseToken(%q) error = %v, want enroll.token_invalid", fz9Head(in), err)
			}
			return
		}
		again, err := ParseToken(got.String())
		if err != nil || again != got {
			t.Fatalf("ParseToken(%q) = %+v, which prints as %q and parses as %+v, %v", fz9Head(in), got, got.String(), again, err)
		}
	})
}

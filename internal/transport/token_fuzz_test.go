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

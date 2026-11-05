package proxy

import (
	"errors"
	"net/netip"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fz8Head returns at most the first 64 bytes of s, for a failure message.
func fz8Head(s string) string { return s[:min(len(s), 64)] }

// FuzzCheckUpstream feeds an arbitrary forward host, a port and a calling
// action to CheckUpstream, parseNginxIPv4 and parseInetAton: none panics, a
// refusal is npm.reserved, and a host that nginx reads as a loopback or
// unspecified address, or is a loopback name, is refused unless the caller is
// npm.admin.expose. nginx tries its own parser first and getaddrinfo's
// inet_aton form only when that fails, so the reading that counts is the first
// one that succeeds; a host the later reading takes for loopback is left to the
// first. With nothing reserved and a port other than NPM's admin port, the
// loopback rule is the only one that refuses. A canonical dotted quad is read
// alike by both parsers and by netip.ParseAddr (R74).
func FuzzCheckUpstream(f *testing.F) {
	for _, h := range []string{
		"127.0.0.1", "127.1", "0x7f.1", "2130706433", "0127.0.0.1", "0177.0.0.1", "127...1", "localhost", "LocalHost.",
		"::1", "[::1]", "::ffff:127.0.0.1", "0.0.0.0", "172.18.0.2", "172.31.255.", "darkpanel-npm", "web.1", "10.1",
		"example.com", "", "[", "1.2.3.4",
	} {
		f.Add(h, uint16(81), uint8(0))
		f.Add(h, uint16(8080), uint8(1))
	}
	f.Fuzz(func(t *testing.T, host string, port uint16, which uint8) {
		parent := []string{"", panelOwner, adminOwner, "other.action"}[int(which)%4]
		name := normaliseHost(host)
		addr, isAddr := upstreamAddr(host, name)
		loopback := !isAddr && slices.Contains(loopbackNames, name) || isAddr && (addr.IsLoopback() || addr.IsUnspecified())
		if a, err := netip.ParseAddr(name); err == nil && (a.Unmap().IsLoopback() || a.Unmap().IsUnspecified()) && !loopback {
			t.Fatalf("upstreamAddr(%q) does not read the canonical loopback address it holds", fz8Head(host))
		}
		for _, r := range []Reserved{{}, pxReserved()} {
			err := CheckUpstream(r, host, int(port), parent)
			if err != nil {
				var ce *errcode.Error
				if !errors.As(err, &ce) || ce.Code != errcode.NPMReserved {
					t.Fatalf("CheckUpstream(%q, %d, %q) = %v", fz8Head(host), port, parent, err)
				}
			}
			if loopback && parent != adminOwner && err == nil {
				t.Fatalf("CheckUpstream(%q, %d, %q) lets a loopback host through", fz8Head(host), port, parent)
			}
		}
		if port != npmAdminPort {
			if err := CheckUpstream(Reserved{}, host, int(port), parent); (err != nil) != (loopback && parent != adminOwner) {
				t.Fatalf("CheckUpstream(%q, %d, %q) with nothing reserved = %v, loopback %v", fz8Head(host), port, parent, err, loopback)
			}
		}
		if want, err := netip.ParseAddr(host); err == nil && want.Is4() {
			if got, ok := parseNginxIPv4(host); !ok || got != want {
				t.Fatalf("parseNginxIPv4(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(host), got, ok, want)
			}
			if got, ok := parseInetAton(host); !ok || got != want {
				t.Fatalf("parseInetAton(%q) = %v, %v; netip.ParseAddr reads %v", fz8Head(host), got, ok, want)
			}
		}
	})
}

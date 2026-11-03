package proxy

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func pxReserved() Reserved {
	return Reserved{
		NPMAddrs:     []netip.Addr{netip.MustParseAddr("172.18.0.2"), netip.MustParseAddr("fd00:18::2")},
		NPMNames:     []string{"4fa6e0f0c678", "npm"},
		PanelNetwork: netip.MustParsePrefix("172.31.255.0/29"),
		PanelGateway: netip.MustParseAddr("172.31.255.1"),
	}
}

func TestPxCheckUpstream(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	for _, tc := range []struct {
		name   string
		host   string
		port   int
		parent string
		refuse bool
	}{
		{"loopback address on the admin port", "127.0.0.1", 81, "", true},
		{"loopback address on the admin port from its owner", "127.0.0.1", 81, admin, false},
		{"loopback address from the other owner", "127.0.0.1", 81, panel, true},
		{"loopback address on any port", "127.0.0.1", 8080, "", true},
		{"another address of 127.0.0.0/8", "127.1.2.3", 9000, "", true},
		{"a shortened loopback address", "127.1", 81, "", true},
		{"a decimal loopback address", "2130706433", 8080, "", true},
		{"a hex and decimal loopback address", "0x7f.1", 81, "", true},
		{"a loopback address with a leading zero, which nginx reads as decimal", "0127.0.0.1", 81, "", true},
		{"empty parts, which nginx reads as zero", "127...1", 81, "", true},
		{"a trailing dot, which nginx reads as a zero last part", "172.31.255.", 9443, "", true},
		{"a leading zero nginx reads as decimal 177", "0177.0.0.1", 81, "", false},
		{"a shortened loopback address from its owner", "127.1", 81, admin, false},
		{"a shortened NPM address on the admin port", "172.18.2", 81, "", true},
		{"the panel gateway as one decimal number", "2887778049", 9443, "", true},
		{"a shortened unrelated address", "10.1", 81, "", false},
		{"a name whose last label is a number", "web.1", 81, "", false},
		{"localhost", "localhost", 81, "", true},
		{"localhost in capitals with a trailing dot", "LocalHost.", 3000, "", true},
		{"localhost from its owner", "localhost", 81, admin, false},
		{"the IPv6 loopback", "::1", 81, "", true},
		{"the bracketed IPv6 loopback", "[::1]", 81, "", true},
		{"the IPv4-mapped loopback", "::ffff:127.0.0.1", 81, "", true},
		{"the unspecified address, which Linux connects to the local host", "0.0.0.0", 81, "", true},
		{"the IPv6 unspecified address", "::", 81, "", true},
		{"an NPM address on the admin port", "172.18.0.2", 81, "", true},
		{"an NPM address on the admin port from its owner", "172.18.0.2", 81, admin, false},
		{"an NPM IPv6 address on the admin port", "fd00:18::2", 81, "", true},
		{"an NPM address on another port", "172.18.0.2", 80, "", false},
		{"the NPM container's name on the admin port", "darkpanel-npm", 81, "", true},
		{"the NPM container's name on another port", "darkpanel-npm", 8080, "", false},
		{"the hosts file's ip6-localhost on any port", "ip6-localhost", 8080, "", true},
		{"the hosts file's ip6-loopback in capitals with a trailing dot", "IP6-Loopback.", 81, "", true},
		{"ip6-localhost from its owner", "ip6-localhost", 81, admin, false},
		{"the NPM container's short id on the admin port", "4fa6e0f0c678", 81, "", true},
		{"an NPM alias in capitals on the admin port", "NPM", 81, "", true},
		{"an NPM name on the admin port from its owner", "4fa6e0f0c678", 81, admin, false},
		{"an NPM alias on another port", "npm", 8080, "", false},
		{"an address inside the panel network", "172.31.255.2", 9443, "", true},
		{"an address inside the panel network from its owner", "172.31.255.2", 9443, panel, false},
		{"an address inside the panel network from the admin exposure", "172.31.255.2", 9443, admin, true},
		{"the panel gateway", "172.31.255.1", 9443, "", true},
		{"the panel gateway from its owner", "172.31.255.1", 9443, panel, false},
		{"an address just outside the panel network", "172.31.255.8", 9443, "", false},
		{"a container name", "web", 8080, "", false},
		{"a public name from the panel exposure", "app.example.com", 443, panel, false},
		{"an unrelated private address on the admin port", "10.0.0.5", 81, "", false},
		{"a container name on the admin port", "web", 81, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckUpstream(pxReserved(), tc.host, tc.port, tc.parent)
			if !tc.refuse {
				if err != nil {
					t.Fatalf("CheckUpstream(%q, %d, %q) = %v, want nil", tc.host, tc.port, tc.parent, err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.NPMReserved || e.Detail == "" {
				t.Fatalf("CheckUpstream(%q, %d, %q) = %v, want npm.reserved with a detail", tc.host, tc.port, tc.parent, err)
			}
			if !strings.Contains(e.Detail, tc.host) {
				t.Errorf("detail %q does not name the upstream %q", e.Detail, tc.host)
			}
		})
	}
}

func TestPxCheckUpstreamWithNothingKnownRefusesOnlyTheLoopback(t *testing.T) {
	if err := CheckUpstream(Reserved{}, "172.31.255.2", 9443, ""); err != nil {
		t.Errorf("an empty Reserved refused a private address: %v", err)
	}
	if err := CheckUpstream(Reserved{}, "127.0.0.1", 81, ""); errcode.From(err).Code != errcode.NPMReserved {
		t.Errorf("an empty Reserved let the loopback through: %v", err)
	}
}

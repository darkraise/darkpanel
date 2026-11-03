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

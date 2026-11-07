package proxy

import (
	"net"
	"net/netip"
	"strconv"
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
		{"an NPM address on another port", "172.18.0.2", 80, "", true},
		{"the NPM container's name on the admin port", "darkpanel-npm", 81, "", true},
		{"the NPM container's name on another port", "darkpanel-npm", 8080, "", true},
		{"the hosts file's ip6-localhost on any port", "ip6-localhost", 8080, "", true},
		{"the hosts file's ip6-loopback in capitals with a trailing dot", "IP6-Loopback.", 81, "", true},
		{"ip6-localhost from its owner", "ip6-localhost", 81, admin, false},
		{"the NPM container's short id on the admin port", "4fa6e0f0c678", 81, "", true},
		{"an NPM alias in capitals on the admin port", "NPM", 81, "", true},
		{"an NPM name on the admin port from its owner", "4fa6e0f0c678", 81, admin, false},
		{"an NPM alias on another port", "npm", 8080, "", true},
		{"the NPM container's name with its network on the admin port", "darkpanel-npm.darkpanel-npm", 81, "", true},
		{"the NPM container's short id with a network on the admin port", "4fa6e0f0c678.darkpanel-npm", 81, "", true},
		{"an NPM alias with a dotted network in capitals and a trailing dot", "NPM.apps.internal.", 81, "", true},
		{"the NPM container's name with a network from its owner", "darkpanel-npm.darkpanel-npm", 81, admin, false},
		{"the NPM container's name with a network on another port", "darkpanel-npm.darkpanel-npm", 8080, "", true},
		{"a container name with a network on the admin port", "web.darkpanel-npm", 81, "", false},
		{"a name that only ends with an NPM name", "web.npm", 81, "", false},
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

func TestPxCheckUpstreamWithNothingKnownRefusesTheLoopbackAndNPMsName(t *testing.T) {
	if err := CheckUpstream(Reserved{}, "172.31.255.2", 9443, ""); err != nil {
		t.Errorf("an empty Reserved refused a private address: %v", err)
	}
	if err := CheckUpstream(Reserved{}, "127.0.0.1", 81, ""); errcode.From(err).Code != errcode.NPMReserved {
		t.Errorf("an empty Reserved let the loopback through: %v", err)
	}
	for _, port := range []int{80, 3000, 8080} {
		if err := CheckUpstream(Reserved{}, "darkpanel-npm", port, ""); errcode.From(err).Code != errcode.NPMReserved {
			t.Errorf("an empty Reserved let NPM's container name through on port %d: %v", port, err)
		}
	}
}

func TestV1r08ReservesEveryPortOfNPMsNamesAndAddresses(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	ports := []int{22, 80, 81, 443, 3000, 8080, 65535}

	t.Run("NPM's own identities are refused on every port", func(t *testing.T) {
		for _, host := range []string{
			"172.18.0.2", "fd00:18::2", "[fd00:18::2]", "172.18.2", "darkpanel-npm", "4fa6e0f0c678", "NPM",
			"NPM.apps.internal.", "darkpanel-npm.darkpanel-npm",
		} {
			for _, port := range ports {
				for _, parent := range []string{"", panel, "other.action"} {
					err := CheckUpstream(pxReserved(), host, port, parent)
					e := errcode.From(err)
					if e.Code != errcode.NPMReserved {
						t.Errorf("CheckUpstream(%q, %d, %q) = %v, want npm.reserved", host, port, parent, err)
						continue
					}
					want := net.JoinHostPort(strings.Trim(host, "[]"), strconv.Itoa(port))
					if !strings.Contains(e.Detail, want) {
						t.Errorf("detail %q does not name the upstream %s", e.Detail, want)
					}
				}
			}
		}
	})

	t.Run("the admin exposure's own composite may reach NPM on any port", func(t *testing.T) {
		for _, host := range []string{"172.18.0.2", "darkpanel-npm", "npm", "127.0.0.1"} {
			for _, port := range ports {
				if err := CheckUpstream(pxReserved(), host, port, admin); err != nil {
					t.Errorf("CheckUpstream(%q, %d, %q) = %v, want nil", host, port, admin, err)
				}
			}
		}
	})

	t.Run("other upstreams stay allowed on every port", func(t *testing.T) {
		for _, host := range []string{"web", "web.npm", "darkpanel-npm-data", "npm2", "10.0.0.5", "172.18.0.3", "app.example.com"} {
			for _, port := range ports {
				if err := CheckUpstream(pxReserved(), host, port, ""); err != nil {
					t.Errorf("CheckUpstream(%q, %d, %q) = %v, want nil", host, port, "", err)
				}
			}
		}
	})
}

func TestV1r09CheckResolved(t *testing.T) {
	const admin, panel = "npm.admin.expose", "panel.exposure.add"
	addrs := func(s ...string) []netip.Addr {
		out := make([]netip.Addr, len(s))
		for i, a := range s {
			out[i] = netip.MustParseAddr(a)
		}
		return out
	}
	const host = "web.example.net"
	for _, tc := range []struct {
		name    string
		answers []netip.Addr
		port    int
		parent  string
		show    string // the offending answer, as the detail must show it; "" when nothing is refused
	}{
		{"a public answer", addrs("203.0.113.7"), 8080, "", ""},
		{"no answer", nil, 8080, "", ""},
		{"the loopback", addrs("127.0.0.1"), 8080, "", "127.0.0.1"},
		{"the loopback from the admin exposure", addrs("127.0.0.1"), 81, admin, ""},
		{"the IPv6 loopback", addrs("::1"), 8080, "", "::1"},
		{"an IPv4-mapped loopback", addrs("::ffff:127.0.0.1"), 8080, "", "127.0.0.1"},
		{"the unspecified address", addrs("0.0.0.0"), 8080, "", "0.0.0.0"},
		{"an NPM address on its API port", addrs("172.18.0.2"), 3000, "", "172.18.0.2"},
		{"an NPM IPv6 address", addrs("fd00:18::2"), 3000, "", "fd00:18::2"},
		{"the panel gateway", addrs("172.31.255.1"), 9443, "", "172.31.255.1"},
		{"the panel gateway from its owner", addrs("172.31.255.1"), 9443, panel, ""},
		{"a panel network address from the admin exposure", addrs("172.31.255.2"), 9443, admin, "172.31.255.2"},
		{"a reserved second answer", addrs("203.0.113.7", "127.0.0.1"), 8080, "", "127.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := CheckResolved(pxReserved(), host, tc.answers, tc.port, tc.parent)
			if tc.show == "" {
				if err != nil {
					t.Fatalf("CheckResolved = %v, want nil", err)
				}
				return
			}
			e := errcode.From(err)
			if e.Code != errcode.NPMReserved {
				t.Fatalf("CheckResolved = %v, want npm.reserved", err)
			}
			if want := "forward host " + host + " resolves to " + tc.show + ": "; !strings.HasPrefix(e.Detail, want) {
				t.Errorf("detail %q does not start with %q", e.Detail, want)
			}
		})
	}
}

func TestV1r09LiteralAddr(t *testing.T) {
	for _, tc := range []struct {
		host string
		want string // "" when nginx reads the host as a name
	}{
		{"10.0.0.5", "10.0.0.5"},
		{"127.1", "127.0.0.1"},
		{"0x7f.1", "127.0.0.1"},
		{"2130706433", "127.0.0.1"},
		{"[::1]", "::1"},
		{"::ffff:10.0.0.5", "10.0.0.5"},
		{"web", ""},
		{"web.1", ""},
		{"app.example.com", ""},
		{"LocalHost.", ""},
	} {
		got, ok := LiteralAddr(tc.host)
		if tc.want == "" {
			if ok {
				t.Errorf("LiteralAddr(%q) = %v, true; want a name", tc.host, got)
			}
			continue
		}
		if !ok || got != netip.MustParseAddr(tc.want) {
			t.Errorf("LiteralAddr(%q) = %v, %v; want %s", tc.host, got, ok, tc.want)
		}
	}
}

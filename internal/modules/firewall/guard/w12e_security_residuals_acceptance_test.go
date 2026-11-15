package guard

import (
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

const (
	qaEth0LL    = "fe80::a00:27ff:fe4e:66a1"
	qaPanelLL   = "fe80::42:acff:fe1e:1"
	qaHostIP    = "172.30.0.1" // the bridge gateway: an address of this host, the client Docker's userland proxy shows
	qaVisibleIP = "10.0.0.50"  // a client no address of this host equals
	qaDrop6443  = "-A INPUT -p tcp -m tcp --dport 443 -j DROP"
)

// qaLinkLocalHost is a host whose only non-loopback IPv6 addresses are link-local: eth0 and the dp-panel bridge each
// hold one, and an IPv6 default route exists only when defaultRoute is set, as router advertisements would add it.
func qaLinkLocalHost(defaultRoute bool) v1r21Fib {
	ifaces := ckIfaces()
	for i := range ifaces {
		switch ifaces[i].Name {
		case "eth0":
			ifaces[i].Addrs = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix(qaEth0LL + "/64")}
		case "dp-panel":
			ifaces[i].Addrs = append(ifaces[i].Addrs, netip.MustParsePrefix(qaPanelLL+"/64"))
		}
	}
	table := append(v1r21HostRoutes(),
		v1r21Route("unicast", "fe80::/64", "eth0", qaEth0LL, 254),
		v1r21Route("unicast", "fe80::/64", "dp-panel", qaPanelLL, 254),
		v1r21Route("local", "::1/128", "lo", "::1", 255),
		v1r21Route("local", qaEth0LL+"/128", "eth0", qaEth0LL, 255),
		v1r21Route("local", qaPanelLL+"/128", "dp-panel", qaPanelLL, 255),
	)
	if defaultRoute {
		table = append(table, v1r21Route("unicast", "::/0", "eth0", qaEth0LL, 254))
	}
	return v1r21Fib{&gHost{ifaces: ifaces, table: table}}
}

// qaFilter6 is the IPv6 filter table with the given INPUT policy and rules.
func qaFilter6(inputPolicy string, rules ...string) string {
	return "*filter\n:INPUT " + inputPolicy + " [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" +
		strings.Join(rules, "\n") + "\nCOMMIT\n"
}

// qaChange runs the guard of a request through the NPM exposure exp1 on [::]:443 made by client, for the change from
// an ACCEPT-all host to the given IPv4 and IPv6 filter tables; extra names the interfaces of h beyond docker0 and
// tailscale0 whose rp_filter the guard reads.
func qaChange(t *testing.T, h Host, client, v4 string, v6 string, extra ...string) (ok bool, message string) {
	t.Helper()
	before := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table())
	st := v1r21State(t, w11b09Nat+v4, v6)
	g := w12bCheck(t, h, &before, st, "[::]:443", client, "", extra...)
	return g.OK, g.Message
}

// TestA7_ADropOfIPv6443OnALinkLocalOnlyHostIsRefusedForAHiddenClient: on a host whose only non-loopback IPv6
// addresses are link-local and that has no IPv6 default route, dropping IPv6 TCP 443 is refused for a request through an
// NPM exposure with a [::]:443 binding whose client is a host address, and the refusal names the exposure and the
// blocked range.
func TestA7_ADropOfIPv6443OnALinkLocalOnlyHostIsRefusedForAHiddenClient(t *testing.T) {
	ok, msg := qaChange(t, qaLinkLocalHost(false), qaHostIP, v1r21Table(), v1r21Table(qaDrop6443))
	if ok {
		t.Fatal("the guard passed a change that drops IPv6 TCP 443 for a hidden client of the exposure")
	}
	if !strings.Contains(msg, "exposure exp1") {
		t.Errorf("refusal %q does not name the exposure exp1", msg)
	}
	if !w12eNamesIPv6Range(msg) {
		t.Errorf("refusal %q names no blocked IPv6 range", msg)
	}
}

// TestA8_ChangesThatBlockNoOneOnALinkLocalOnlyHostPass: on that host a change that touches no IPv6 rule passes and
// the change of A7 made by a visible client passes.
func TestA8_ChangesThatBlockNoOneOnALinkLocalOnlyHostPass(t *testing.T) {
	h := qaLinkLocalHost(false)
	if ok, msg := qaChange(t, h, qaHostIP, v1r21Table("-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT"), v1r21Table()); !ok {
		t.Errorf("a change that touches no IPv6 rule was refused: %s", msg)
	}
	if ok, msg := qaChange(t, h, qaVisibleIP, v1r21Table(), v1r21Table(qaDrop6443)); !ok {
		t.Errorf("the drop of IPv6 TCP 443 was refused for a visible client: %s", msg)
	}
}

// TestA8_LinkLocalVethsAddNoProbesOnADualStackHost: on a dual-stack host with docker0 and two veths up, giving those
// three interfaces fe80:: addresses leaves the number of route lookups of an unrelated change as it was.
func TestA8_LinkLocalVethsAddNoProbesOnADualStackHost(t *testing.T) {
	lookups := func(linkLocal bool) int {
		ifaces := ckIfaces()
		for i, name := range []string{"docker0", "veth1", "veth2"} {
			var addrs []netip.Prefix
			if linkLocal {
				addrs = []netip.Prefix{netip.MustParsePrefix("fe80::" + string(rune('1'+i)) + "/64")}
			}
			ifaces = append(ifaces, netstat.Interface{Index: 30 + i, Name: name, Up: true, Running: true, Addrs: addrs})
		}
		h := v1r21Fib{&gHost{ifaces: ifaces, table: w12bRoutes()}}
		ok, msg := qaChange(t, h, qaVisibleIP, v1r21Table(), v1r21Table("-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT"), "veth1", "veth2")
		if !ok {
			t.Fatalf("an unrelated change was refused: %s", msg)
		}
		return len(h.lookups)
	}
	without, with := lookups(false), lookups(true)
	if without == 0 {
		t.Fatal("the unrelated change made no route lookup, so the count cannot show an added probe")
	}
	if with != without {
		t.Errorf("%d route lookups with link-local veths, %d without; want the same", with, without)
	}
}

// TestA9_HardeningIPv6OnALinkLocalOnlyHostIsRefusedForAHiddenClient: on an IPv4-only host a change that sets the IPv6
// INPUT policy to DROP, or turns IPv6 off in UFW (whose plain IPv6 table drops what its loopback rule does not
// accept), is refused for a request through an NPM exposure whose client is a host address.
func TestA9_HardeningIPv6OnALinkLocalOnlyHostIsRefusedForAHiddenClient(t *testing.T) {
	for _, tc := range []struct{ name, v6 string }{
		{"the IPv6 INPUT policy set to DROP", qaFilter6("DROP")},
		{"IPv6 turned off in UFW", qaFilter6("DROP", "-A INPUT -i lo -j ACCEPT")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ok, msg := qaChange(t, qaLinkLocalHost(false), qaHostIP, v1r21Table(), tc.v6)
			if ok {
				t.Fatal("the guard passed the change for a hidden client of the exposure")
			}
			if !strings.Contains(msg, "exposure exp1") {
				t.Errorf("refusal %q does not name the exposure exp1", msg)
			}
		})
	}
}

// TestA9_OnALinkLocalOnlyHostWithAnIPv6DefaultRouteTheRefusalReachesEveryClient: with an IPv6 default route learned from
// router advertisements the same changes are refused for a visible client as well.
func TestA9_OnALinkLocalOnlyHostWithAnIPv6DefaultRouteTheRefusalReachesEveryClient(t *testing.T) {
	for _, tc := range []struct{ name, v6 string }{
		{"a drop of IPv6 TCP 443", v1r21Table(qaDrop6443)},
		{"the IPv6 INPUT policy set to DROP", qaFilter6("DROP")},
		{"IPv6 turned off in UFW", qaFilter6("DROP", "-A INPUT -i lo -j ACCEPT")},
	} {
		for _, client := range []string{qaVisibleIP, qaHostIP} {
			t.Run(tc.name+" by "+client, func(t *testing.T) {
				ok, msg := qaChange(t, qaLinkLocalHost(true), client, v1r21Table(), tc.v6)
				if ok {
					t.Fatalf("the guard passed the change for client %s", client)
				}
				if !strings.Contains(msg, "exposure exp1") {
					t.Errorf("refusal %q does not name the exposure exp1", msg)
				}
			})
		}
	}
}

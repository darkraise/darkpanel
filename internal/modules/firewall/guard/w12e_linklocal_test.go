package guard

import (
	"errors"
	"net/netip"
	"regexp"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// w12eCaptured are the route lookups of the evidence note's Route capture: destination, source ("" for none), and the
// answer.
var w12eCaptured = []struct {
	dst, src    string
	noRoute     bool
	iface, pref string
}{
	{"2001:db8::1", "", true, "", ""},
	{"2001:db8::1", w12eEth0LL, true, "", ""},
	{"2001:db8::1", w12eDocker0LL, true, "", ""},
	{"2001:db8::1", "::1", true, "", ""},
	{"2606:4700::1111", "", true, "", ""},
	{"2606:4700::1111", w12eEth0LL, true, "", ""},
	{"2606:4700::1111", w12eDocker0LL, true, "", ""},
	{"2606:4700::1111", "::1", true, "", ""},
	{"fe80::1", "", false, "eth0", w12eEth0LL},
	{"fe80::1", w12eEth0LL, false, "eth0", w12eEth0LL},
}

// w12eRange is how a refusal names a blocked cell: the span of its addresses, "from <from>-<to> to".
var w12eRange = regexp.MustCompile(`from ([^ -]+)-([^ ]+) to`)

// w12eNamesIPv6Range reports whether msg names a blocked cell whose two ends are IPv6 addresses in order.
func w12eNamesIPv6Range(msg string) bool {
	for _, m := range w12eRange.FindAllStringSubmatch(msg, -1) {
		from, err1 := netip.ParseAddr(m[1])
		to, err2 := netip.ParseAddr(m[2])
		if err1 == nil && err2 == nil && from.Is6() && !from.Is4In6() && to.Is6() && !to.Is4In6() && from.Compare(to) <= 0 {
			return true
		}
	}
	return false
}

func TestW12eNamesIPv6Range(t *testing.T) {
	for _, tc := range []struct {
		msg  string
		want bool
	}{
		{"cut exposure exp1 hidden client from fe80::-fe80::10ed:bdff:fe67:f4d0 to [fe80::1]:443 on eth0", true},
		{"cut exposure exp1 from 10.0.0.0-10.255.255.255 to 10.0.0.1:443 on eth0", false},
		{"cut exposure exp1 from ab:-:cd to [fe80::1]:443", false},
		{"cut exposure exp1 from :-: to [fe80::1]:443", false},
		{"cut exposure exp1 from fe80::9-fe80::1 to [fe80::1]:443", false},
		{"cut exposure exp1 from ::ffff:10.0.0.1-::ffff:10.0.0.9 to [fe80::1]:443", false},
		{"cut exposure exp1 to [fe80::1]:443 on eth0", false},
	} {
		if got := w12eNamesIPv6Range(tc.msg); got != tc.want {
			t.Errorf("w12eNamesIPv6Range(%q) = %v, want %v", tc.msg, got, tc.want)
		}
	}
}

// TestW12eFixtureMatchesCapture: the link-local fixture answers every captured lookup as the host did.
func TestW12eFixtureMatchesCapture(t *testing.T) {
	h := w12eLinkLocalHost(false)
	for _, c := range w12eCaptured {
		q := netstat.RouteQuery{Dst: netip.MustParseAddr(c.dst)}
		if c.src != "" {
			q.Src = netip.MustParseAddr(c.src)
		}
		r, err := h.RouteGet(q)
		switch {
		case c.noRoute && !errors.Is(err, netstat.ErrNoRoute):
			t.Errorf("%s from %q: %+v %v, want no route", c.dst, c.src, r, err)
		case !c.noRoute && (err != nil || r.OutIface != c.iface || r.PrefSrc != netip.MustParseAddr(c.pref)):
			t.Errorf("%s from %q: %+v %v, want dev %s src %s", c.dst, c.src, r, err, c.iface, c.pref)
		}
	}
}

const (
	w12eEth0LL    = "fe80::be24:11ff:fedb:56d4"
	w12eDocker0LL = "fe80::10ed:bdff:fe67:f4d1"
	w12eDrop6443  = "-A INPUT -p tcp -m tcp --dport 443 -j DROP"
	w12eDropAll6  = "-A INPUT -j DROP"
)

// w12eLinkLocalHost is ckIfaces as captured on an IPv4-only Docker host: eth0 and the panel bridge hold only
// link-local IPv6 addresses, and no IPv6 route leaves the host unless defaultRoute adds one learned from router
// advertisements.
func w12eLinkLocalHost(defaultRoute bool) v1r21Fib {
	ifaces := ckIfaces()
	for i := range ifaces {
		switch ifaces[i].Name {
		case "eth0":
			ifaces[i].Addrs = []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix(w12eEth0LL + "/64")}
		case "dp-panel":
			ifaces[i].Addrs = append(ifaces[i].Addrs, netip.MustParsePrefix(w12eDocker0LL+"/64"))
		}
	}
	table := append(v1r21HostRoutes(),
		v1r21Route("unicast", "fe80::/64", "eth0", w12eEth0LL, 254),
		v1r21Route("unicast", "fe80::/64", "dp-panel", w12eDocker0LL, 254),
		v1r21Route("local", "::1/128", "lo", "::1", 255),
		v1r21Route("local", w12eEth0LL+"/128", "eth0", w12eEth0LL, 255),
		v1r21Route("local", w12eDocker0LL+"/128", "dp-panel", w12eDocker0LL, 255),
	)
	if defaultRoute {
		table = append(table, v1r21Route("unicast", "::/0", "eth0", w12eEth0LL, 254))
	}
	return v1r21Fib{&gHost{ifaces: ifaces, table: table}}
}

func TestW12eLinkLocalOnlyHostGuardsIPv6443(t *testing.T) {
	for _, tc := range []struct {
		name         string
		defaultRoute bool
		client       string
		v4, v6       []string
		want         string // "" when the change must pass
	}{
		{"a hidden client and a drop of IPv6 443", false, "172.30.0.1", nil, []string{w12eDrop6443}, "exposure exp1"},
		{"a hidden client and an IPv6 INPUT that drops everything", false, "172.30.0.1", nil, []string{w12eDropAll6}, "exposure exp1"},
		{"a hidden client and a change that touches no IPv6 rule", false, "172.30.0.1", []string{w12bOtherPort}, nil, ""},
		{"a visible client and a drop of IPv6 443", false, "10.0.0.50", nil, []string{w12eDrop6443}, ""},
		{"a visible client and a drop of IPv6 443 with a default route", true, "10.0.0.50", nil, []string{w12eDrop6443}, "exposure exp1"},
		{"a visible client and an IPv6 INPUT that drops everything with a default route", true, "10.0.0.50", nil, []string{w12eDropAll6}, "exposure exp1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := w12eLinkLocalHost(tc.defaultRoute)
			before := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table())
			st := v1r21State(t, w11b09Nat+v1r21Table(tc.v4...), v1r21Table(tc.v6...))
			g := w12bCheck(t, h, &before, st, "[::]:443", tc.client, "")
			switch {
			case tc.want == "" && !g.OK:
				t.Errorf("guard = %+v, want OK", g)
			case tc.want != "" && (g.OK || !strings.Contains(g.Message, tc.want)):
				t.Errorf("guard = %+v, want a refusal containing %q", g, tc.want)
			case tc.want != "" && !w12eNamesIPv6Range(g.Message):
				t.Errorf("refusal %q names no blocked IPv6 range", g.Message)
			}
		})
	}
}

// TestW12eLinkLocalAddressesAddNoProbes: on a dual-stack host with docker0 and two veths up, their link-local
// addresses leave the number of route lookups of an unrelated IPv6 change as it is without them. Both runs hold the
// three interfaces, because each up interface is an ingress candidate for the IPv4 cells whatever its addresses.
func TestW12eLinkLocalAddressesAddNoProbes(t *testing.T) {
	lookups := func(linkLocal bool) int {
		ifaces := ckIfaces()
		for i, name := range []string{"docker0", "veth1", "veth2"} {
			var addrs []netip.Prefix
			if linkLocal {
				addrs = []netip.Prefix{netip.MustParsePrefix("fe80::" + string(rune('a'+i)) + "/64")}
			}
			ifaces = append(ifaces, netstat.Interface{Index: 20 + i, Name: name, Up: true, Running: true, Addrs: addrs})
		}
		h := v1r21Fib{&gHost{ifaces: ifaces, table: w12bRoutes()}}
		before := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table())
		st := v1r21State(t, w11b09Nat+v1r21Table(), v1r21Table(w12bOtherPort))
		if g := w12bCheck(t, h, &before, st, "[::]:443", "10.0.0.50", "", "veth1", "veth2"); !g.OK {
			t.Fatalf("guard = %+v, want OK", g)
		}
		return len(h.lookups)
	}
	without, with := lookups(false), lookups(true)
	if without == 0 {
		t.Fatal("the IPv6 change made no route lookup, so the count cannot show an added probe")
	}
	if with != without {
		t.Errorf("%d route lookups with link-local veths, %d without; want the same", with, without)
	}
}

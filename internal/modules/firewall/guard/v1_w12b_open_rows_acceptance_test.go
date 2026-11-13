package guard

import (
	"context"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// a27Hosts are the layouts the acceptance tests run the guard on.
type a27Layout struct {
	ifaces []netstat.Interface
	routes []netstat.Route
	strict bool
}

func a27Prefixes(ps ...string) []netip.Prefix {
	out := make([]netip.Prefix, len(ps))
	for i, p := range ps {
		out[i] = netip.MustParsePrefix(p)
	}
	return out
}

// a27Busy is a host with Docker bridges, a tailnet interface, ULA and
// link-local addresses, and a v6 default route.
func a27Busy(strict bool) a27Layout {
	ifaces := []netstat.Interface{
		{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: a27Prefixes("127.0.0.1/8", "::1/128")},
		{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: a27Prefixes("192.0.2.1/24", "2001:db8::1/64", "fd00:aa::1/64", "fe80::1/64")},
		{Index: 3, Name: "eth1", Up: true, Running: true, Addrs: a27Prefixes("10.0.0.1/24")},
		{Index: 6, Name: "dp-panel", Up: true, Running: true, Addrs: a27Prefixes("172.30.0.1/16")},
		{Index: 7, Name: "docker0", Up: true, Running: true, Addrs: a27Prefixes("172.17.0.1/16")},
		{Index: 8, Name: "br-0a1b2c", Up: true, Running: true, Addrs: a27Prefixes("172.18.0.1/16")},
		{Index: 9, Name: "tailscale0", Up: true, Running: true, Addrs: a27Prefixes("100.101.102.103/32", "fd7a:115c:a1e0::1/128", "fe80::2/64")},
	}
	routes := []netstat.Route{
		v1r21Route("unicast", "0.0.0.0/0", "eth0", "192.0.2.1", 254),
		v1r21Route("unicast", "192.0.2.0/24", "eth0", "192.0.2.1", 254),
		v1r21Route("unicast", "10.0.0.0/24", "eth1", "10.0.0.1", 254),
		v1r21Route("unicast", "172.30.0.0/16", "dp-panel", "172.30.0.1", 254),
		v1r21Route("unicast", "172.17.0.0/16", "docker0", "172.17.0.1", 254),
		v1r21Route("unicast", "172.18.0.0/16", "br-0a1b2c", "172.18.0.1", 254),
		v1r21Route("unicast", "100.64.0.0/10", "tailscale0", "100.101.102.103", 52),
		v1r21Route("local", "127.0.0.0/8", "lo", "127.0.0.1", 255),
		v1r21Route("local", "192.0.2.1/32", "eth0", "192.0.2.1", 255),
		v1r21Route("local", "10.0.0.1/32", "eth1", "10.0.0.1", 255),
		v1r21Route("local", "172.30.0.1/32", "dp-panel", "172.30.0.1", 255),
		v1r21Route("local", "172.17.0.1/32", "docker0", "172.17.0.1", 255),
		v1r21Route("local", "172.18.0.1/32", "br-0a1b2c", "172.18.0.1", 255),
		v1r21Route("local", "100.101.102.103/32", "tailscale0", "100.101.102.103", 255),
		v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254),
		v1r21Route("unicast", "2001:db8::/64", "eth0", "2001:db8::1", 254),
		v1r21Route("unicast", "fd00:aa::/64", "eth0", "fd00:aa::1", 254),
		v1r21Route("unicast", "fd7a:115c:a1e0::/48", "tailscale0", "fd7a:115c:a1e0::1", 52),
		v1r21Route("local", "::1/128", "lo", "::1", 255),
		v1r21Route("local", "2001:db8::1/128", "eth0", "2001:db8::1", 255),
		v1r21Route("local", "fd00:aa::1/128", "eth0", "fd00:aa::1", 255),
		v1r21Route("local", "fd7a:115c:a1e0::1/128", "tailscale0", "fd7a:115c:a1e0::1", 255),
	}
	return a27Layout{ifaces: ifaces, routes: routes, strict: strict}
}

// a27Plain is the small host the other tests use: eth0 with a public IPv4 and
// IPv6 address, eth1, the panel bridge and loopback.
func a27Plain() a27Layout {
	return a27Layout{ifaces: ckIfaces(), routes: append(v1r21HostRoutes(), v1r21Route("unicast", "::/0", "eth0", "2001:db8::1", 254))}
}

const (
	a27Nat = "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 172.30.0.2:443\nCOMMIT\n"
)

// a27Plan is a request through the NPM exposure exp1, whose public binding is
// bind, naming client as the client.
func a27Plan(bind, client string) *core.GuardContext {
	g := &core.GuardContext{Complete: true, Via: &core.ProxyPath{
		ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: []netip.AddrPort{netip.MustParseAddrPort(bind)},
	}}
	if client != "" {
		g.Client = &core.Peer{Addr: netip.MustParseAddr(client)}
	}
	return g
}

// a27State is a loaded state with the DNAT of 443 to the proxy and the filter
// rules of each family.
func a27State(t *testing.T, v4, v6 []string) fwstate.Loaded {
	t.Helper()
	return v1r21State(t, a27Nat+v1r21Table(v4...), v1r21Table(v6...))
}

// a27Check runs the guard on layout, with before as the state the change
// starts from (nil: a guard that has no before-state).
func a27Check(t *testing.T, l a27Layout, before *fwstate.Loaded, st fwstate.Loaded, plan *core.GuardContext) (core.Guard, []FlowResult) {
	t.Helper()
	env, files, _ := ckEnv()
	ckRunning(env)
	if l.strict {
		files.sysctl["net/ipv4/conf/all/rp_filter"] = "1"
		for _, i := range l.ifaces {
			files.sysctl["net/ipv4/conf/"+i.Name+"/rp_filter"] = "1"
		}
	} else {
		for _, i := range l.ifaces {
			files.sysctl["net/ipv4/conf/"+i.Name+"/rp_filter"] = "0"
		}
	}
	h := v1r21Fib{&gHost{ifaces: l.ifaces, table: l.routes}}
	g, results, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Before: before, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g, results
}

func a27Refused(t *testing.T, g core.Guard, want ...string) {
	t.Helper()
	if g.OK {
		t.Errorf("guard passed (%q), want a refusal containing %q", g.Message, want)
		return
	}
	for _, w := range want {
		if !strings.Contains(g.Message, w) {
			t.Errorf("refusal %q does not contain %q", g.Message, w)
		}
	}
}

func a27Hidden(results []FlowResult) int {
	n := 0
	for _, r := range results {
		if strings.Contains(r.Flow, "hidden client") {
			n++
		}
	}
	return n
}

const (
	a27Drop4 = "-A FORWARD -s %s -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -j DROP"
	a27Drop6 = "-A INPUT -s %s -p tcp -m tcp --dport 443 -j DROP"
)

func a27DropFrom(tmpl, src string) string { return strings.Replace(tmpl, "%s", src, 1) }

// hostClients are clients a request through NPM names when it came through
// Docker's userland proxy or from the host itself.
var a27HostClients = []string{"172.30.0.1", "127.0.0.1", "192.0.2.1"}

func TestA1_HostClientChecksEveryUnicastSourceOfTheBinding(t *testing.T) {
	for _, client := range a27HostClients {
		for _, tc := range []struct {
			name, bind string
			v4, v6     []string
			wantRange  string
		}{
			{"a ULA source on an IPv6 binding", "[::]:443", nil, []string{a27DropFrom(a27Drop6, "fd00:d0::/64")}, "fd00:d0::"},
			{"a link-local source on an IPv6 binding", "[::]:443", nil, []string{a27DropFrom(a27Drop6, "fe80::/10")}, "fe80::"},
			{"a tailnet source on an IPv4 binding", "0.0.0.0:443", []string{a27DropFrom(a27Drop4, "100.100.0.0/16")}, nil, "100.100.0.0"},
			{"a private source on an IPv4 binding", "0.0.0.0:443", []string{a27DropFrom(a27Drop4, "10.77.0.0/16")}, nil, "10.77.0.0"},
			{"a link-local source on an IPv4 binding", "0.0.0.0:443", []string{a27DropFrom(a27Drop4, "169.254.0.0/16")}, nil, "169.254.0.0"},
		} {
			t.Run(client+" "+tc.name, func(t *testing.T) {
				g, _ := a27Check(t, a27Plain(), nil, a27State(t, tc.v4, tc.v6), a27Plan(tc.bind, client))
				a27Refused(t, g, "exposure exp1", tc.wantRange)
			})
		}
	}
}

func TestA1_ANewBlockOfAnotherPortRefusesNothing(t *testing.T) {
	for _, client := range a27HostClients {
		g, _ := a27Check(t, a27Plain(), nil,
			a27State(t, []string{"-A FORWARD -s 10.77.0.0/16 -d 172.30.0.2/32 -p tcp -m tcp --dport 8443 -j DROP"},
				[]string{"-A INPUT -s fd00:d0::/64 -p tcp -m tcp --dport 8443 -j DROP"}), a27Plan("[::]:443", client))
		if !g.OK {
			t.Errorf("client %s: guard refused (%q), want a pass: no source is blocked on 443", client, g.Message)
		}
	}
}

func TestA1_DNATBindingHiddenClientAndPublicLegAgreeOnAPublicSource(t *testing.T) {
	blocked := []string{a27DropFrom(a27Drop4, "8.8.8.0/24")}
	pub, _ := a27Check(t, a27Plain(), nil, a27State(t, blocked, nil), a27Plan("0.0.0.0:443", ""))
	a27Refused(t, pub, "exposure exp1 public from 8.8.8.0-8.8.8.255")
	for _, client := range a27HostClients {
		both, results := a27Check(t, a27Plain(), nil, a27State(t, blocked, nil), a27Plan("0.0.0.0:443", client))
		a27Refused(t, both, "exposure exp1 public from 8.8.8.0-8.8.8.255", "exposure exp1 hidden client from 8.8.8.0-8.8.8.255")
		if a27Hidden(results) == 0 {
			t.Errorf("client %s: no hidden-client flow was checked", client)
		}
	}
	for _, client := range []string{"", "172.30.0.1"} {
		g, _ := a27Check(t, a27Plain(), nil, a27State(t, nil, nil), a27Plan("0.0.0.0:443", client))
		if !g.OK {
			t.Errorf("client %q: guard refused (%q) with nothing blocked", client, g.Message)
		}
	}
}

func TestA2_ARangeBlockedBeforeTheChangeIsNotRefused(t *testing.T) {
	for _, client := range a27HostClients {
		blocked4 := []string{a27DropFrom(a27Drop4, "100.100.0.0/16")}
		blocked6 := []string{a27DropFrom(a27Drop6, "fd00:d0::/64")}
		for _, bind := range []string{"[::]:443", "0.0.0.0:443"} {
			t.Run(client+" "+bind, func(t *testing.T) {
				before := a27State(t, blocked4, blocked6)
				// The same state before and after: nothing is newly blocked.
				g, _ := a27Check(t, a27Plain(), &before, a27State(t, blocked4, blocked6), a27Plan(bind, client))
				if !g.OK {
					t.Errorf("guard refused (%q), want a pass: the rules before the change blocked the same sources", g.Message)
				}
				// A range the change newly blocks is still refused, naming only it.
				after := a27State(t, append(append([]string{}, blocked4...), a27DropFrom(a27Drop4, "10.77.0.0/16")),
					append(append([]string{}, blocked6...), a27DropFrom(a27Drop6, "fd00:e0::/64")))
				g, _ = a27Check(t, a27Plain(), &before, after, a27Plan(bind, client))
				want := "10.77.0.0"
				if bind == "[::]:443" {
					want = "fd00:e0::"
				}
				a27Refused(t, g, want)
				for _, old := range []string{"100.100.0.0", "fd00:d0::"} {
					if strings.Contains(g.Message, old) {
						t.Errorf("refusal %q names %s, which the rules before the change already blocked", g.Message, old)
					}
				}
			})
		}
	}
}

func TestA3_AnIPv4BindingIsCheckedFromEveryIPv4UnicastSource(t *testing.T) {
	for _, src := range []string{"192.168.50.0/24", "10.200.0.0/16", "100.64.0.0/10", "198.51.100.0/24", "172.20.0.0/14"} {
		g, _ := a27Check(t, a27Plain(), nil, a27State(t, []string{a27DropFrom(a27Drop4, src)}, nil), a27Plan("0.0.0.0:443", "172.30.0.1"))
		a27Refused(t, g, "exposure exp1", strings.TrimSuffix(strings.Split(src, "/")[0], ".0"))
	}
}

func TestA4_AClientThatIsNotAHostAddressGetsNoHiddenClientFlow(t *testing.T) {
	for _, tc := range []struct {
		name, bind, client string
		v4, v6             []string
		refused            string // "" when the change must pass
	}{
		{"an unrelated private range on an IPv4 binding", "0.0.0.0:443", "10.0.0.50", []string{a27DropFrom(a27Drop4, "10.77.0.0/16")}, nil, ""},
		{"an unrelated tailnet range on an IPv4 binding", "0.0.0.0:443", "198.51.100.77", []string{a27DropFrom(a27Drop4, "100.100.0.0/16")}, nil, ""},
		{"an unrelated ULA range on an IPv6 binding", "[::]:443", "fd00:cc::5", nil, []string{a27DropFrom(a27Drop6, "fd00:d0::/64")}, ""},
		{"the client itself", "0.0.0.0:443", "10.0.0.50", []string{a27DropFrom(a27Drop4, "10.0.0.50/32")}, nil, "exposure exp1 client from 10.0.0.50"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g, results := a27Check(t, a27Plain(), nil, a27State(t, tc.v4, tc.v6), a27Plan(tc.bind, tc.client))
			if n := a27Hidden(results); n != 0 {
				t.Errorf("%d hidden-client flows were checked for client %s", n, tc.client)
			}
			if tc.refused == "" {
				if !g.OK {
					t.Errorf("guard refused (%q), want a pass", g.Message)
				}
				return
			}
			a27Refused(t, g, tc.refused)
		})
	}
}

func TestA27_AChangeThatBlocksNoSourceOnTheBindingPassesForAHostClient(t *testing.T) {
	// Kept rules whose source is one of the host's own addresses: a packet
	// from such an address arrives only through loopback, so the rule blocks
	// no source of the binding.
	keep4 := []string{
		a27DropFrom(a27Drop4, "100.101.102.103/32"),
		a27DropFrom(a27Drop4, "172.17.0.1/32"),
		a27DropFrom(a27Drop4, "192.0.2.1/32"),
	}
	keep6 := []string{
		a27DropFrom(a27Drop6, "fd7a:115c:a1e0::1/128"),
		a27DropFrom(a27Drop6, "fd00:aa::1/128"),
		a27DropFrom(a27Drop6, "fe80::1/128"),
	}
	for _, strict := range []bool{true, false} {
		for _, client := range []string{"172.30.0.1", "172.17.0.1", "127.0.0.1", "100.101.102.103"} {
			for _, bind := range []string{"0.0.0.0:443", "[::]:443"} {
				name := client + " " + bind
				if strict {
					name += " strict"
				}
				t.Run(name, func(t *testing.T) {
					l := a27Busy(strict)
					plan := a27Plan(bind, client)
					base, _ := a27Check(t, l, nil, a27State(t, nil, nil), a27Plan(bind, ""))
					if !base.OK {
						t.Fatalf("the layout is wrong: without a client the guard refuses %q", base.Message)
					}
					before := a27State(t, keep4, keep6)
					for _, from := range []*fwstate.Loaded{nil, &before} {
						g, results := a27Check(t, l, from, a27State(t, keep4, keep6), plan)
						if !g.OK {
							t.Errorf("before-state %v: guard refused (%q), want a pass", from != nil, g.Message)
						}
						if a27Hidden(results) == 0 {
							t.Errorf("before-state %v: no hidden-client flow was checked, so the pass proves nothing", from != nil)
						}
					}
					g, _ := a27Check(t, l, nil, a27State(t, nil, nil), plan)
					if !g.OK {
						t.Errorf("an unchanged accepting ruleset: guard refused (%q), want a pass", g.Message)
					}
				})
			}
		}
	}
}

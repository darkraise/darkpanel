package guard

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/eval"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// v1r21Table is save output with ACCEPT policies and the given rules.
func v1r21Table(rules ...string) string {
	return "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n" + strings.Join(rules, "\n") + "\nCOMMIT\n"
}

// v1r21State is a checked state of the parsed IPv4 and IPv6 save output.
func v1r21State(t *testing.T, v4, v6 string) fwstate.Loaded {
	t.Helper()
	parse := func(text string) *netfilter.Ruleset {
		rs, err := netfilter.Parse(strings.NewReader(text))
		if err != nil {
			t.Fatal(err)
		}
		return rs
	}
	return fwstate.Loaded{Variant: "nft", V4: parse(v4), V6: parse(v6)}
}

// v1r21Check runs the guard with the real evaluator.
func v1r21Check(t *testing.T, h *gHost, st fwstate.Loaded, plan *core.GuardContext) core.Guard {
	t.Helper()
	return v1r21CheckFrom(t, h, nil, st, plan)
}

// v1r21CheckFrom runs the guard with the real evaluator on st, with before
// as the loaded state the change starts from.
func v1r21CheckFrom(t *testing.T, h *gHost, before *fwstate.Loaded, st fwstate.Loaded, plan *core.GuardContext) core.Guard {
	t.Helper()
	env, _, _ := ckEnv()
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Before: before, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func v1r21Tunnel() *core.GuardContext {
	return &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel",
		ProxyAddr: netip.MustParseAddr("172.30.0.3"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}
}

// v1r21Public is the guard context of an NPM exposure whose public
// binding is 192.0.2.1:443.
func v1r21Public() *core.GuardContext {
	return &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm",
		ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"),
		Public: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:443")}}}
}

func TestV1r21TunnelFlowNamesTheFirstBlockedCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 198.41.192.0/24 -j DROP", "-A FORWARD -d 104.16.0.0/13 -j DROP"), v1r21Table())
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"1.0.0.0", "104.16.0.0", "104.24.0.0", "198.41.192.0", "198.41.193.0"} {
		h.routes[rtQuery(rep, "172.30.0.3", "dp-panel")] = rtVia("eth0", "")
	}
	g := v1r21Check(t, h, st, v1r21Tunnel())
	if g.OK {
		t.Fatalf("guard = %+v, want a refusal: the connector's edge ranges are dropped while the documentation address is allowed", g)
	}
	const first, later = "104.16.0.0-104.23.255.255", "198.41.192.0-198.41.192.255"
	for _, want := range []string{
		"exposure exp2 tunnel udp from 172.30.0.3 to " + first + ":7844 on dp-panel out eth0: drop",
		"exposure exp2 tunnel from 172.30.0.3 to " + first + ":7844 on dp-panel out eth0: drop",
		"exposure exp2 tunnel from 172.30.0.3 to " + first + ":443 on dp-panel out eth0: drop",
	} {
		if !strings.Contains(g.Message, want) {
			t.Errorf("message %q does not contain %q", g.Message, want)
		}
	}
	if strings.Contains(g.Message, later) {
		t.Errorf("message %q names %s, which is not the first blocked cell", g.Message, later)
	}
}

func TestV1r21TunnelFlowIsEvaluatedInEveryCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 198.41.192.0/24 -j ACCEPT"), v1r21Table())
	reps := []string{"1.0.0.0", "198.41.192.0", "198.41.193.0"}
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range reps {
		h.routes[rtQuery(rep, "172.30.0.3", "dp-panel")] = rtVia("eth0", "")
	}
	if g := v1r21Check(t, h, st, v1r21Tunnel()); !g.OK {
		t.Fatalf("guard = %+v, want OK when every cell passes", g)
	}
	for _, rep := range reps {
		if q := rtQuery(rep, "172.30.0.3", "dp-panel"); !slices.Contains(h.lookups, q) {
			t.Errorf("no route was looked up for the cell starting at %s: lookups %v", rep, h.lookups)
		}
	}
}

func TestV1r21PublicFlowChecksEverySourceCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A INPUT -s 8.8.8.0/24 -p tcp -j DROP"), v1r21Table())
	g := v1r21Check(t, &gHost{ifaces: ckIfaces()}, st, v1r21Public())
	want := "exposure exp1 public from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}

func TestV1r21TailscaleFlowChecksEveryIPv6Cell(t *testing.T) {
	st := v1r21State(t, v1r21Table(), v1r21Table("-A OUTPUT -d 2606:4700::/32 -j DROP"))
	h := &gHost{ifaces: gIfaces(), sockets: []netstat.Socket{
		flSock(netstat.ProtoUDP, netstat.StateClose, "[::]:41641", "", "tailscaled", 300),
	}, routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"2000::", "2606:4700::", "2606:4701::"} {
		h.routes[gxQueryMark(rtQuery(rep, "", ""), 0x80000)] = rtVia("eth0", "2001:db8::1")
	}
	g := v1r21Check(t, h, st, nil)
	want := "tailscale udp from 2001:db8::1 to [2606:4700::-2606:4700:ffff:ffff:ffff:ffff:ffff:ffff]:41641 out eth0: drop"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}

func TestV1r21SourceBlockedBeforeTheChangeDoesNotRefuseIt(t *testing.T) {
	const ban = "-A INPUT -s 8.8.8.0/24 -p tcp -j DROP"
	before := v1r21State(t, v1r21Table(ban), v1r21Table())
	after := v1r21State(t, v1r21Table(ban, "-A INPUT -p tcp -m tcp --dport 2222 -j ACCEPT"), v1r21Table())
	if g := v1r21CheckFrom(t, &gHost{ifaces: ckIfaces()}, &before, after, v1r21Public()); !g.OK {
		t.Errorf("guard = %+v, want OK: the change does not block the source the loaded rules already drop", g)
	}
	const want = "exposure exp1 public from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop"
	if g := v1r21Check(t, &gHost{ifaces: ckIfaces()}, after, v1r21Public()); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard without a before-state = %+v, want a refusal containing %q", g, want)
	}
}

func TestV1r21ChangeThatBlocksASourceCellIsRefused(t *testing.T) {
	for _, tt := range []struct {
		name, before string // the IPv4 rule loaded before the change; "" reads no ruleset
		after        []string
		want, not    string
	}{
		{"a new ban", "-A INPUT -s 8.8.8.0/24 -p tcp -j DROP",
			[]string{"-A INPUT -s 8.8.8.0/24 -p tcp -j DROP", "-A INPUT -s 9.9.9.0/24 -p tcp -j DROP"},
			"exposure exp1 public from 9.9.9.0-9.9.9.255 to 192.0.2.1:443 on eth0: drop", "8.8.8.0-8.8.8.255"},
		// Cut by the new rules alone, 8.8.0.0/16 would be one cell whose
		// lowest address was already dropped.
		{"a ban widened past the old one", "-A INPUT -s 8.8.0.0/24 -p tcp -j DROP",
			[]string{"-A INPUT -s 8.8.0.0/16 -p tcp -j DROP"},
			"exposure exp1 public from 8.8.1.0-8.8.255.255 to 192.0.2.1:443 on eth0: drop", "8.8.0.0-8.8.0.255"},
		{"a source the before-state cannot decide", "",
			[]string{"-A INPUT -s 8.8.8.0/24 -p tcp -j DROP"},
			"exposure exp1 public from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := fwstate.Loaded{Variant: "nft"}
			if tt.before != "" {
				before = v1r21State(t, v1r21Table(tt.before), v1r21Table())
			}
			g := v1r21CheckFrom(t, &gHost{ifaces: ckIfaces()}, &before, v1r21State(t, v1r21Table(tt.after...), v1r21Table()), v1r21Public())
			if g.OK || !strings.Contains(g.Message, tt.want) {
				t.Errorf("guard = %+v, want a refusal containing %q", g, tt.want)
			}
			if tt.not != "" && strings.Contains(g.Message, tt.not) {
				t.Errorf("message %q names %s, which the loaded rules already drop", g.Message, tt.not)
			}
		})
	}
}

func TestV1r21WildDestinationIgnoresTheBeforeState(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 104.16.0.0/13 -j DROP"), v1r21Table())
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"1.0.0.0", "104.16.0.0", "104.24.0.0"} {
		h.routes[rtQuery(rep, "172.30.0.3", "dp-panel")] = rtVia("eth0", "")
	}
	g := v1r21CheckFrom(t, h, &st, st, v1r21Tunnel())
	const want = "exposure exp2 tunnel from 172.30.0.3 to 104.16.0.0-104.23.255.255:443 on dp-panel out eth0: drop"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q: a flow to any internet address needs every cell", g, want)
	}
}

// v1r21Bans are n rules "-A <chain> -s <addr>/32<match> -j DROP" for public
// addresses no two of which are adjacent, so each one would cut two cells.
func v1r21Bans(n int, chain, match string) []string {
	rules := make([]string, n)
	a := netip.MustParseAddr("45.0.0.0")
	for i := range rules {
		rules[i] = "-A " + chain + " -s " + a.String() + "/32" + match + " -j DROP"
		a = a.Next().Next()
	}
	return rules
}

// v1r21Jail is save output with the rules of a fail2ban chain f2b-sshd that
// INPUT jumps to for the ports of dports, and nat rules if any.
func v1r21Jail(dports string, jail []string, nat ...string) string {
	s := "*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:f2b-sshd - [0:0]\n" +
		"-A INPUT -p tcp -m multiport --dports " + dports + " -j f2b-sshd\n" +
		strings.Join(jail, "\n") + "\n-A f2b-sshd -j RETURN\nCOMMIT\n"
	if len(nat) > 0 {
		s += "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
			strings.Join(nat, "\n") + "\nCOMMIT\n"
	}
	return s
}

// v1r21AnyRoute answers every route lookup through eth0 from 192.0.2.1.
type v1r21AnyRoute struct {
	*gHost
	n int
}

func (h *v1r21AnyRoute) RouteGet(netstat.RouteQuery) (netstat.Route, error) {
	h.n++
	return rtVia("eth0", "192.0.2.1"), nil
}

// v1r21Listener checks the IPv4 save output v4 for a host whose controller
// listens on 0.0.0.0:443 and that has three interfaces up, and returns the
// guard with the number of route lookups.
func v1r21Listener(t *testing.T, v4 string) (core.Guard, int) {
	t.Helper()
	h := &v1r21AnyRoute{gHost: &gHost{ifaces: ckIfaces(), sockets: []netstat.Socket{
		flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:443", "", "darkpanel-contr", 4242),
	}}}
	env, _, _ := ckEnv()
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, v1r21State(t, v4, v1r21Table()), Input{Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g, h.n
}

func TestV1r21BansOnAnotherPortCutNoCell(t *testing.T) {
	const bans = 5000
	for _, tt := range []struct{ name, v4 string }{
		{"rules of INPUT", v1r21Table(v1r21Bans(bans, "INPUT", " -p tcp -m tcp --dport 22")...)},
		{"a chain only port 22 jumps to", v1r21Jail("22", v1r21Bans(bans, "f2b-sshd", ""))},
	} {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			g, lookups := v1r21Listener(t, tt.v4)
			t.Logf("%d bans: Check took %s with %d route lookups", bans, time.Since(start), lookups)
			if !g.OK {
				t.Fatalf("guard = %+v, want OK: no ban matches port 443", g)
			}
			// The controller listener's flow looks up the route back once per cell.
			if lookups != 1 {
				t.Errorf("%d route lookups, want 1: bans that cannot match port 443 cut cells", lookups)
			}
		})
	}
}

func TestV1r21OnlyRulesThatCanMatchCutCells(t *testing.T) {
	const ban = "-A INPUT -s 8.8.8.8/32 -p tcp -m tcp --dport "
	jail := []string{"-A f2b-sshd -s 8.8.8.8/32 -j REJECT --reject-with icmp-port-unreachable"}
	for _, tt := range []struct {
		name, v4 string
		blocked  bool
	}{
		{"a ban on another port", v1r21Table(ban + "22 -j DROP"), false},
		{"a ban on another protocol", v1r21Table("-A INPUT -s 8.8.8.8/32 -p udp -j DROP"), false},
		{"a ban on the flow's port", v1r21Table(ban + "443 -j DROP"), true},
		{"a ban on every port but another", v1r21Table("-A INPUT -s 8.8.8.8/32 -p tcp -m tcp ! --dport 22 -j DROP"), true},
		{"a ban on a port range", v1r21Table("-A INPUT -s 8.8.8.8/32 -p tcp -m multiport --dports 80,400:500 -j DROP"), true},
		{"a chain only another port jumps to", v1r21Jail("22", jail), false},
		{"a chain the flow's port jumps to", v1r21Jail("22,443", jail), true},
		{"a ban on the port DNAT rewrites to", v1r21Jail("22", []string{ban + "8443 -j DROP"},
			"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 192.0.2.1:8443"), true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			g, _ := v1r21Listener(t, tt.v4)
			const want = "controller listener from 8.8.8.8-8.8.8.8 to 192.0.2.1:"
			if got := !g.OK && strings.Contains(g.Message, want); got != tt.blocked {
				t.Errorf("guard = %+v, want a refusal containing %q: %v", g, want, tt.blocked)
			}
		})
	}
}

// v1r21Bindings is IPv4 save output for NPM published on 0.0.0.0:443, with
// one binding allowance per host address in allowed.
func v1r21Bindings(allowed ...string) string {
	s := "*nat\n:PREROUTING ACCEPT [0:0]\n:INPUT ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:POSTROUTING ACCEPT [0:0]\n" +
		"-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 172.30.0.2:443\nCOMMIT\n" +
		"*filter\n:INPUT ACCEPT [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n:darkpanel-docker-bindings - [0:0]\n" +
		"-A FORWARD -m conntrack --ctstate DNAT -j darkpanel-docker-bindings\n"
	for _, a := range allowed {
		s += "-A darkpanel-docker-bindings -d 172.30.0.2/32 -p tcp -m tcp --dport 443 -m conntrack --ctorigdst " + a + " --ctorigdstport 443 -j ACCEPT\n"
	}
	return s + "-A darkpanel-docker-bindings -j DROP\nCOMMIT\n"
}

// v1r21TwoAddrs is a host whose eth0 holds 10.0.0.10/24 and 10.0.0.11/24.
// It routes 172.30.0.0/16 out of dp-panel and every other address out of
// eth0 with the preferred source 10.0.0.10.
type v1r21TwoAddrs struct{ *gHost }

func (h v1r21TwoAddrs) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	if netip.MustParsePrefix("172.30.0.0/16").Contains(q.Dst) {
		return rtVia("dp-panel", "172.30.0.1"), nil
	}
	return rtVia("eth0", "10.0.0.10"), nil
}

func TestV1r21WildcardBindIsCheckedOnEveryAddress(t *testing.T) {
	check := func(before, st fwstate.Loaded) core.Guard {
		t.Helper()
		h := v1r21TwoAddrs{&gHost{ifaces: []netstat.Interface{
			{Index: 1, Name: "lo", Up: true, Running: true, Loopback: true, Addrs: []netip.Prefix{netip.MustParsePrefix("127.0.0.1/8")}},
			{Index: 2, Name: "eth0", Up: true, Running: true, Addrs: []netip.Prefix{
				netip.MustParsePrefix("10.0.0.10/24"), netip.MustParsePrefix("10.0.0.11/24")}},
			{Index: 6, Name: "dp-panel", Up: true, Running: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/16")}},
		}}}
		plan := &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm",
			ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"),
			Public: []netip.AddrPort{netip.MustParseAddrPort("0.0.0.0:443")}}}
		env, _, _ := ckEnv()
		ckRunning(env)
		g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Before: &before, Eval: eval.Evaluate})
		if err != nil {
			t.Fatal(err)
		}
		return g
	}
	both := v1r21State(t, v1r21Bindings("10.0.0.10", "10.0.0.11"), v1r21Table())
	const want = "exposure exp1 public from 1.0.0.0-9.255.255.255 to 10.0.0.11:443 on eth0: drop"
	if g := check(both, v1r21State(t, v1r21Bindings("10.0.0.10"), v1r21Table())); g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q: the allowance of an address the wildcard bind takes is gone", g, want)
	}
	if g := check(both, both); !g.OK {
		t.Errorf("guard = %+v, want OK with both allowances kept", g)
	}
}

// v1r21Edge routes 198.41.192.0/24 out of eth1 and every other address out
// of eth0.
type v1r21Edge struct{ *gHost }

func (h v1r21Edge) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	if netip.MustParsePrefix("198.41.192.0/24").Contains(q.Dst) {
		return rtVia("eth1", ""), nil
	}
	return rtVia("eth0", ""), nil
}

// v1r21CheckHost runs the guard with the real evaluator on st against h.
func v1r21CheckHost(t *testing.T, h Host, st fwstate.Loaded, plan *core.GuardContext) core.Guard {
	t.Helper()
	env, _, _ := ckEnv()
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestV1r21RoutesCutInternetCells(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -s 172.30.0.3/32 -i dp-panel -o eth1 -j DROP"), v1r21Table())
	def := netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix("0.0.0.0/0"), Gateway: netip.MustParseAddr("192.0.2.254"), OutIface: "eth0"}
	edge := netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix("198.41.192.0/24"), Gateway: netip.MustParseAddr("10.0.0.254"), OutIface: "eth1"}
	g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def, edge}}}, st, v1r21Tunnel())
	for _, want := range []string{"198.41.192.0-198.41.192.255", "out eth1: drop"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard = %+v, want a refusal containing %q: the connector's edge routes out of eth1, where the rule drops it", g, want)
		}
	}
	if g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def}}}, st, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: without the /24 route no cell routes out of eth1", g)
	}
}

func TestV1r21HostThatCannotListRoutes(t *testing.T) {
	g := v1r21CheckHost(t, struct{ Host }{&gHost{ifaces: ckIfaces()}}, v1r21State(t, v1r21Table(), v1r21Table()), v1r21Tunnel())
	const want = "exposure exp2 tunnel: unknown (the host cannot list routes)"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}

func TestV1r21ManyRoutesStayCheap(t *testing.T) {
	const n = 500
	table := []netstat.Route{{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix("0.0.0.0/0"), OutIface: "eth0"}}
	a := netip.MustParseAddr("45.0.0.0")
	for range n {
		table = append(table, netstat.Route{Type: "unicast", Table: 254, Dst: netip.PrefixFrom(a, 24), OutIface: "eth0"})
		for range 512 {
			a = a.Next()
		}
	}
	h := &v1r21AnyRoute{gHost: &gHost{ifaces: ckIfaces(), table: table, sockets: []netstat.Socket{
		flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:443", "", "darkpanel-contr", 4242),
	}}}
	st := v1r21State(t, v1r21Table(v1r21Bans(1000, "INPUT", " -p tcp -m tcp --dport 22")...), v1r21Table())
	start := time.Now()
	g := v1r21CheckHost(t, h, st, v1r21Tunnel())
	t.Logf("%d routes: Check took %s with %d route lookups", n, time.Since(start), h.n)
	if !g.OK {
		t.Fatalf("guard = %+v, want OK: no rule blocks a cell", g)
	}
	// The controller listener's flow alone looks up the route back once per
	// cell, and each route adds two cells.
	if h.n < 2*n+1 {
		t.Errorf("%d route lookups, want at least %d: the routes did not cut cells", h.n, 2*n+1)
	}
}

// v1r21Fib answers each route lookup from its route dump the way the
// kernel does: the local table first, then the longest matching prefix of
// the other tables. A local route answers out of lo from the address
// itself, a blackhole route fails with EINVAL, and any other route that is
// not unicast has no route.
type v1r21Fib struct{ *gHost }

func (h v1r21Fib) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	h.lookups = append(h.lookups, q)
	dst := q.Dst.Unmap()
	var best *netstat.Route
	for _, local := range []bool{true, false} {
		for i := range h.table {
			r := &h.table[i]
			if (r.Table == 255) == local && r.Dst.Contains(dst) && (best == nil || r.Dst.Bits() > best.Dst.Bits()) {
				best = r
			}
		}
		if best != nil {
			break
		}
	}
	if best == nil {
		return netstat.Route{}, netstat.ErrNoRoute
	}
	answer := *best
	answer.Dst = netip.PrefixFrom(dst, dst.BitLen())
	switch best.Type {
	case "unicast":
		return answer, nil
	case "local":
		answer.PrefSrc, answer.OutIface = dst, "lo"
		return answer, nil
	case "blackhole":
		return netstat.Route{}, errors.New("netstat: route lookup: invalid argument")
	}
	return netstat.Route{}, fmt.Errorf("%w to %s: %s", netstat.ErrNoRoute, dst, best.Type)
}

// v1r21Route is a route of a dump: "<typ> <dst> dev <iface> src <src>
// table <table>", with "-" for no preferred source.
func v1r21Route(typ, dst, iface, src string, table uint32) netstat.Route {
	r := netstat.Route{Type: typ, Table: table, Dst: netip.MustParsePrefix(dst), OutIface: iface}
	if src != "-" {
		r.PrefSrc = netip.MustParseAddr(src)
	}
	return r
}

// v1r21HostRoutes are the main and local table routes of a host with
// ckIfaces whose default route leaves eth0.
func v1r21HostRoutes() []netstat.Route {
	return []netstat.Route{
		v1r21Route("unicast", "0.0.0.0/0", "eth0", "192.0.2.1", 254),
		v1r21Route("unicast", "192.0.2.0/24", "eth0", "192.0.2.1", 254),
		v1r21Route("unicast", "10.0.0.0/24", "eth1", "10.0.0.1", 254),
		v1r21Route("unicast", "172.30.0.0/16", "dp-panel", "172.30.0.1", 254),
		v1r21Route("local", "127.0.0.0/8", "lo", "127.0.0.1", 255),
		v1r21Route("local", "192.0.2.1/32", "eth0", "192.0.2.1", 255),
		v1r21Route("local", "10.0.0.1/32", "eth1", "10.0.0.1", 255),
		v1r21Route("local", "172.30.0.1/32", "dp-panel", "172.30.0.1", 255),
	}
}

func TestV1r21LocalRoutesCutNoCell(t *testing.T) {
	// The host's own public address, 45.33.0.9 on eth0, as on a typical
	// VPS. UFW denies incoming traffic by default; the proxy's listener is
	// allowed.
	ifaces := ckIfaces()
	ifaces[1].Addrs = append(ifaces[1].Addrs, netip.MustParsePrefix("45.33.0.9/24"))
	table := append(v1r21HostRoutes(),
		v1r21Route("unicast", "45.33.0.0/24", "eth0", "45.33.0.9", 254),
		v1r21Route("local", "45.33.0.9/32", "eth0", "45.33.0.9", 255),
		v1r21Route("broadcast", "45.33.0.255/32", "eth0", "45.33.0.9", 255),
	)
	st := v1r21State(t, "*filter\n:INPUT DROP [0:0]\n:FORWARD ACCEPT [0:0]\n:OUTPUT ACCEPT [0:0]\n"+
		"-A INPUT -s 172.30.0.3/32 -d 172.30.0.1/32 -i dp-panel -p tcp -m tcp --dport 8080 -j ACCEPT\n"+
		"-A FORWARD -s 172.30.0.3/32 -i dp-panel -o eth1 -j DROP\nCOMMIT\n", v1r21Table())
	if g := v1r21CheckHost(t, v1r21Fib{&gHost{ifaces: ifaces, table: table}}, st, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: the host's own address is no internet cell the connector reaches", g)
	}
	edge := v1r21Route("unicast", "198.41.192.0/24", "eth1", "10.0.0.1", 254)
	g := v1r21CheckHost(t, v1r21Fib{&gHost{ifaces: ifaces, table: append(table, edge)}}, st, v1r21Tunnel())
	for _, want := range []string{"198.41.192.0-198.41.192.255", "out eth1: drop"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard = %+v, want a refusal containing %q: the connector's edge routes out of eth1, where the rule drops it", g, want)
		}
	}
}

func TestV1r21RoutesWithoutAPathCutNoCell(t *testing.T) {
	// A ban by fail2ban's route action, and a blackholed range.
	table := append(v1r21HostRoutes(),
		v1r21Route("unreachable", "45.1.2.3/32", "", "-", 254),
		v1r21Route("blackhole", "45.9.0.0/16", "", "-", 254),
	)
	check := func(v4 string) core.Guard {
		t.Helper()
		h := v1r21Fib{&gHost{ifaces: ckIfaces(), table: table, sockets: []netstat.Socket{
			flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:443", "", "darkpanel-contr", 4242),
		}}}
		return v1r21CheckHost(t, h, v1r21State(t, v4, v1r21Table()), v1r21Tunnel())
	}
	if g := check(v1r21Table()); !g.OK {
		t.Errorf("guard = %+v, want OK: nothing blocks the listener or the connector", g)
	}
	for _, tt := range []struct{ rule, want string }{
		{"-A INPUT -s 8.8.8.0/24 -p tcp -j DROP", "controller listener from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop"},
		{"-A FORWARD -d 104.16.0.0/13 -j DROP", "exposure exp2 tunnel from 172.30.0.3 to 104.16.0.0-104.23.255.255:443 on dp-panel out eth0: drop"},
	} {
		if g := check(v1r21Table(tt.rule)); g.OK || !strings.Contains(g.Message, tt.want) {
			t.Errorf("guard with %q = %+v, want a refusal containing %q", tt.rule, g, tt.want)
		}
	}
}

// v1r21Throw answers lookups as a host whose first routing table sends every
// address out of eth0 from 192.0.2.1 and throws thrown on to the tables after
// it. With later, one of them routes thrown out of eth1 from 10.0.0.1;
// without, none routes it. With from, a source rule routes replies from that
// address to thrown out of eth0. A non-nil fail answers every lookup in
// thrown.
type v1r21Throw struct {
	*gHost
	thrown netip.Prefix
	later  bool
	from   netip.Addr
	fail   error
}

func (h v1r21Throw) RouteGet(q netstat.RouteQuery) (netstat.Route, error) {
	switch {
	case !h.thrown.Contains(q.Dst):
		return rtVia("eth0", "192.0.2.1"), nil
	case h.fail != nil:
		return netstat.Route{}, h.fail
	case h.from.IsValid() && q.Src == h.from:
		return rtVia("eth0", h.from.String()), nil
	case h.later:
		return rtVia("eth1", "10.0.0.1"), nil
	}
	return netstat.Route{}, fmt.Errorf("%w to %s: network is unreachable", netstat.ErrNoRoute, q.Dst)
}

func TestV1r21ThrowRoutesCutCells(t *testing.T) {
	// Tailscale's table 52 throws ranges on to the tables after it.
	thrown := netip.MustParsePrefix("45.33.0.0/16")
	st := v1r21State(t, v1r21Table("-A FORWARD -s 172.30.0.3/32 -i dp-panel -o eth1 -j DROP"), v1r21Table())
	throw := v1r21Route("throw", thrown.String(), "", "-", 52)
	g := v1r21CheckHost(t, v1r21Throw{gHost: &gHost{ifaces: ckIfaces(), table: append(v1r21HostRoutes(), throw)}, thrown: thrown, later: true}, st, v1r21Tunnel())
	for _, want := range []string{"45.33.0.0-45.33.255.255", "out eth1: drop"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard = %+v, want a refusal containing %q: the later table routes the thrown range out of eth1, where the rule drops it", g, want)
		}
	}
	if g := v1r21CheckHost(t, v1r21Throw{gHost: &gHost{ifaces: ckIfaces(), table: v1r21HostRoutes()}, thrown: thrown, later: true}, st, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: without the throw route no cell routes out of eth1", g)
	}
}

func TestV1r21ThrowCellWithoutARouteBackHasNothingToProtect(t *testing.T) {
	thrown := netip.MustParsePrefix("45.33.0.0/16")
	table := append(v1r21HostRoutes(), v1r21Route("throw", thrown.String(), "", "-", 52))
	check := func(later bool, st fwstate.Loaded, plan *core.GuardContext) core.Guard {
		t.Helper()
		h := v1r21Throw{gHost: &gHost{ifaces: ckIfaces(), table: table, sockets: []netstat.Socket{
			flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:443", "", "darkpanel-contr", 4242),
		}}, thrown: thrown, later: later}
		return v1r21CheckHost(t, h, st, plan)
	}
	open := v1r21State(t, v1r21Table(), v1r21Table())
	if g := check(false, open, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: no table routes back to the thrown range, and nothing blocks the rest", g)
	}
	strict := v1r21State(t, v1r21Table(), v1r21Table())
	strict.Sysctl = map[string]string{"net/ipv4/conf/all/rp_filter": "1"}
	if g := check(false, strict, v1r21Public()); !g.OK {
		t.Errorf("guard under strict rp_filter = %+v, want OK: no table routes back to the thrown range, and nothing blocks the rest", g)
	}
	for _, tt := range []struct {
		later      bool
		rule, want string
	}{
		{false, "-A INPUT -s 8.8.8.0/24 -p tcp -j DROP", "controller listener from 8.8.8.0-8.8.8.255 to 192.0.2.1:443 on eth0: drop"},
		// The later table answers the thrown range from eth1's address.
		{true, "-A INPUT -d 10.0.0.1/32 -p tcp -j DROP", "controller listener from 45.33.0.0-45.33.255.255 to 10.0.0.1:443 on eth0: drop"},
	} {
		if g := check(tt.later, v1r21State(t, v1r21Table(tt.rule), v1r21Table()), v1r21Tunnel()); g.OK || !strings.Contains(g.Message, tt.want) {
			t.Errorf("guard with %q = %+v, want a refusal containing %q", tt.rule, g, tt.want)
		}
	}
}

// v1r21ThrowListener checks st under the tunnel plan on h, set up as a host
// whose table 52 throws 45.33.0.0/16 and whose controller listens on
// 0.0.0.0:443.
func v1r21ThrowListener(t *testing.T, h v1r21Throw, st fwstate.Loaded) core.Guard {
	t.Helper()
	h.thrown = netip.MustParsePrefix("45.33.0.0/16")
	h.gHost = &gHost{ifaces: ckIfaces(), table: append(v1r21HostRoutes(), v1r21Route("throw", h.thrown.String(), "", "-", 52)),
		sockets: []netstat.Socket{flSock(netstat.ProtoTCP, netstat.StateListen, "0.0.0.0:443", "", "darkpanel-contr", 4242)}}
	return v1r21CheckHost(t, h, st, v1r21Tunnel())
}

func TestV1r21ThrowCellWithASourceRouteBackIsChecked(t *testing.T) {
	const rule = "-A INPUT -s 45.33.0.0/16 -p tcp --dport 443 -j DROP"
	st := v1r21State(t, v1r21Table(rule), v1r21Table())
	g := v1r21ThrowListener(t, v1r21Throw{from: netip.MustParseAddr("192.0.2.1")}, st)
	for _, want := range []string{"controller listener from 45.33.0.0-45.33.255.255", "cannot be resolved"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard with %q = %+v, want a refusal containing %q: replies from 192.0.2.1 route back to the range the rule drops", rule, g, want)
		}
	}
	if g := v1r21ThrowListener(t, v1r21Throw{}, st); !g.OK {
		t.Errorf("guard with %q and no source rule = %+v, want OK: no address of this host routes back to the thrown range", rule, g)
	}
}

func TestV1r21ThrowCellWhoseLookupFailsIsUnknown(t *testing.T) {
	// The tunnel's cell in the range is unknown as well, so only the
	// listener's own reason shows that its cell was not skipped.
	const want = "controller listener from 45.33.0.0-45.33.255.255 to 0.0.0.0:443: unknown (the route back to 45.33.0.0 cannot be resolved"
	g := v1r21ThrowListener(t, v1r21Throw{fail: errors.New("netstat: invalid argument")}, v1r21State(t, v1r21Table(), v1r21Table()))
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q: only a missing route skips a cell", g, want)
	}
}

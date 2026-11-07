package guard

import (
	"context"
	"net/netip"
	"slices"
	"strings"
	"testing"

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
	env, _, _ := ckEnv()
	ckRunning(env)
	g, _, err := Checker{Host: h}.Check(context.Background(), env, st, Input{Plan: plan, Eval: eval.Evaluate})
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func v1r21Tunnel() *core.GuardContext {
	return &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel",
		ProxyAddr: netip.MustParseAddr("172.30.0.3"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}}
}

func TestV1r21TunnelFlowNamesTheFirstBlockedCell(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -d 198.41.192.0/24 -j DROP", "-A FORWARD -d 104.16.0.0/13 -j DROP"), v1r21Table())
	h := &gHost{ifaces: ckIfaces(), routes: map[netstat.RouteQuery]netstat.Route{}}
	for _, rep := range []string{"0.0.0.1", "104.16.0.0", "104.24.0.0", "198.41.192.0", "198.41.193.0"} {
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
	reps := []string{"0.0.0.1", "198.41.192.0", "198.41.193.0"}
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
	plan := &core.GuardContext{Complete: true, Via: &core.ProxyPath{ExposureID: "exp1", Kind: "npm",
		ProxyAddr: netip.MustParseAddr("172.30.0.2"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"),
		Public: []netip.AddrPort{netip.MustParseAddrPort("192.0.2.1:443")}}}
	g := v1r21Check(t, &gHost{ifaces: ckIfaces()}, st, plan)
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

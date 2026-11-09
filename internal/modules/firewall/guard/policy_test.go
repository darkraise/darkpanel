package guard

import (
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func w11b08Rule(fam netstat.Family, r netstat.Rule) []netstat.Rule {
	r.Family, r.Priority, r.Table, r.Action = fam, 100, 200, unix.FR_ACT_TO_TBL
	return []netstat.Rule{r}
}

// w11b08Selected is q with the selectors of a tcp packet between the ports.
func w11b08Selected(q netstat.RouteQuery, proto uint8, sport, dport uint16) netstat.RouteQuery {
	q.Proto, q.SrcPort, q.DstPort = proto, sport, dport
	return q
}

func TestW11b08ProbeLookupsCarrySelectorsOnlyUnderAPolicyRule(t *testing.T) {
	link := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	tunnel := flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "198.51.100.1", 7844, "udp"))
	linkQ := rtQuery("198.51.100.9", "192.0.2.1", "")
	tunnelQ := rtQuery("198.51.100.1", "172.30.0.3", "dp-panel")
	for _, tt := range []struct {
		name  string
		fl    flow
		rules []netstat.Rule
		want  netstat.RouteQuery
	}{
		{"no policy rule", flow{label: "l", pkt: link}, nil, linkQ},
		{"a mark rule selects by no protocol or port", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{Mark: true}), linkQ},
		{"a rule of the other family", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv6, netstat.Rule{Proto: 6}), linkQ},
		{"a protocol rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a source port rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{SrcPort: true}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a destination port rule", flow{label: "l", pkt: link}, w11b08Rule(netstat.IPv4, netstat.Rule{DstPort: true}), w11b08Selected(linkQ, 6, 40100, 7443)},
		{"a forwarded flow", flow{label: "t", pkt: tunnel, standIn: true}, w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 17}), w11b08Selected(tunnelQ, 17, 0, 7844)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &gHost{ifaces: gIfaces(), routes: map[netstat.RouteQuery]netstat.Route{tt.want: rtVia("eth0", "192.0.2.1")}, rules: tt.rules}
			r := newRouter(h, rtFacts(0, 0, 0), fwstate.Loaded{})
			r.probes(tt.fl)
			if !reflect.DeepEqual(h.lookups, []netstat.RouteQuery{tt.want}) {
				t.Errorf("lookups = %+v, want %+v", h.lookups, tt.want)
			}
		})
	}
}

func TestW11b08RouteCarriesSelectors(t *testing.T) {
	proto := w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6})
	fwd := flOn("eth0", flPkt("input", "198.51.100.1", 0, "172.17.0.2", 80, "tcp"))
	out := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	for _, tt := range []struct {
		name string
		p    fwstate.Packet
		want netstat.RouteQuery
	}{
		{"forwarded", fwd, w11b08Selected(rtQuery("172.17.0.2", "198.51.100.1", "eth0"), 6, 0, 80)},
		{"outbound", out, w11b08Selected(rtQuery("198.51.100.9", "192.0.2.1", ""), 6, 40100, 7443)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			h := &gHost{routes: map[netstat.RouteQuery]netstat.Route{tt.want: rtVia("eth1", "")}, rules: proto}
			if _, oif, err := newRouter(h, rtFacts(0, 0, 0), fwstate.Loaded{}).route(tt.p); err != nil || oif != "eth1" {
				t.Errorf("route = %q, %v; want eth1", oif, err)
			}
		})
	}
}

func TestW11b08ReplyLookupsCarryTheFlowsPortAsSource(t *testing.T) {
	proto := w11b08Rule(netstat.IPv4, netstat.Rule{Proto: 6})
	src, from, proxy := netip.MustParseAddr("45.33.0.0"), netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("172.30.0.2")
	fl := flow{pkt: flPkt("input", "45.33.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc, standIn: true, fwdReplies: []netip.Addr{proxy}}
	h := &gHost{ifaces: gIfaces(), rules: proto}
	if !newRouter(h, facts{ifaces: gIfaces()}, fwstate.Loaded{}).noReply(fl, src, 0, from) {
		t.Fatal("noReply = false, want true: no lookup has a route")
	}
	want := []netstat.RouteQuery{
		w11b08Selected(rtQuery("45.33.0.0", "192.0.2.1", ""), 6, 443, 0),
		w11b08Selected(rtQuery("45.33.0.0", "172.30.0.2", "dp-panel"), 6, 443, 0),
	}
	if !reflect.DeepEqual(h.lookups, want) {
		t.Errorf("lookups = %+v, want %+v", h.lookups, want)
	}

	h = &gHost{ifaces: gIfaces(), rules: proto}
	fl.fwdReplies = nil
	newRouter(h, facts{ifaces: gIfaces()}, fwstate.Loaded{}).noReplyFrom(fl, src, 0)
	if len(h.lookups) == 0 || slices.ContainsFunc(h.lookups, func(q netstat.RouteQuery) bool { return q.Proto != 6 || q.SrcPort != 443 || q.DstPort != 0 }) {
		t.Errorf("lookups = %+v, want every one with protocol 6 and source port 443", h.lookups)
	}

	panel := flPkt("input", "203.0.113.7", 51000, "0.0.0.0", 443, "tcp")
	back := w11b08Selected(rtQuery("203.0.113.7", "", ""), 6, 443, 0)
	strict := []netstat.RouteQuery{back,
		w11b08Selected(rtQuery("203.0.113.7", "192.0.2.1", ""), 6, 443, 0),
		w11b08Selected(rtQuery("203.0.113.7", "10.0.0.1", ""), 6, 443, 0)}
	h = &gHost{ifaces: gIfaces(), rules: proto, routes: map[netstat.RouteQuery]netstat.Route{
		strict[0]: rtVia("eth0", "192.0.2.1"), strict[1]: rtVia("eth0", "192.0.2.1"), strict[2]: rtVia("eth0", "192.0.2.1")}}
	newRouter(h, rtFacts(1, 0, 0), fwstate.Loaded{}).probes(flow{label: "panel", pkt: panel})
	if !reflect.DeepEqual(h.lookups, strict) {
		t.Errorf("route-back lookups = %+v, want %+v", h.lookups, strict)
	}
}

func TestW11b08PolicyPrefixesJoinTheRouteCuts(t *testing.T) {
	ifaces := ckIfaces()
	ifaces[1].Addrs = append(ifaces[1].Addrs, netip.MustParsePrefix("45.33.0.9/24"))
	rule := func(fam netstat.Family, src, dst string) netstat.Rule {
		r := netstat.Rule{Family: fam, Priority: 100, Table: 200, Action: unix.FR_ACT_TO_TBL}
		if src != "" {
			r.Src = netip.MustParsePrefix(src)
		}
		if dst != "" {
			r.Dst = netip.MustParsePrefix(dst)
		}
		return r
	}
	h := &gHost{ifaces: ifaces, rules: []netstat.Rule{
		rule(netstat.IPv4, "8.8.8.0/24", ""),
		rule(netstat.IPv4, "", "9.9.9.9/32"),
		rule(netstat.IPv4, "45.33.0.9/32", ""),
		rule(netstat.IPv4, "45.33.0.0/24", ""),
		rule(netstat.IPv6, "", "2606:4700::/32"),
	}}
	r := newRouter(h, facts{ifaces: ifaces}, fwstate.Loaded{})
	var got []netip.Prefix
	for _, cut := range r.routeCuts(false).rs.Table("routes").Chain("routes").Rules {
		got = append(got, cut.Dest.Prefix)
	}
	want := []netip.Prefix{netip.MustParsePrefix("8.8.8.0/24"), netip.MustParsePrefix("9.9.9.9/32"), netip.MustParsePrefix("45.33.0.0/24")}
	if !slices.Equal(got, want) {
		t.Errorf("cuts = %v, want %v: a host's own single address turns the flow into input", got, want)
	}
}

func TestW11b08PolicyRuleCutsTheCellItSends(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A FORWARD -s 172.30.0.3/32 -i dp-panel -o eth1 -j DROP"), v1r21Table())
	def := netstat.Route{Type: "unicast", Table: 254, Dst: netip.MustParsePrefix("0.0.0.0/0"), OutIface: "eth0"}
	edge := w11b08Rule(netstat.IPv4, netstat.Rule{Dst: netip.MustParsePrefix("198.41.192.0/24")})
	g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def}, rules: edge}}, st, v1r21Tunnel())
	for _, want := range []string{"198.41.192.0-198.41.192.255", "out eth1: drop"} {
		if g.OK || !strings.Contains(g.Message, want) {
			t.Errorf("guard = %+v, want a refusal containing %q: the policy rule sends the range out of eth1", g, want)
		}
	}
	if g := v1r21CheckHost(t, v1r21Edge{&gHost{ifaces: ckIfaces(), table: []netstat.Route{def}}}, st, v1r21Tunnel()); !g.OK {
		t.Errorf("guard = %+v, want OK: without the policy rule no cell routes out of eth1", g)
	}
}

// w11b08RulesFail is a host whose rule dump fails.
type w11b08RulesFail struct{ *gHost }

func (w11b08RulesFail) Rules(netstat.Family) ([]netstat.Rule, error) {
	return nil, errors.New("netlink: dump interrupted")
}

func TestW11b08HostWhosePolicyRulesCannotBeRead(t *testing.T) {
	g := v1r21CheckHost(t, w11b08RulesFail{&gHost{ifaces: ckIfaces()}}, v1r21State(t, v1r21Table(), v1r21Table()), v1r21Tunnel())
	const want = "exposure exp2 tunnel: unknown (the policy rules cannot be read: netlink: dump interrupted)"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
}

package guard

import (
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

func TestW11b06OverCellLimit(t *testing.T) {
	for _, tt := range []struct {
		cells, rules, limit int
		over                bool
	}{
		{65536, 0, 0, false},
		{65537, 0, 65536, true},
		{1000, 9999, 0, false},
		{1000, 10000, 10000000, true},
		{65536, 152, 10000000, true},
		{65537, 152, 65536, true},
	} {
		if limit, over := overCellLimit(tt.cells, tt.rules); limit != tt.limit || over != tt.over {
			t.Errorf("overCellLimit(%d, %d) = %d, %v; want %d, %v", tt.cells, tt.rules, limit, over, tt.limit, tt.over)
		}
	}
}

func TestW11b06TooManyCellsAreUnknown(t *testing.T) {
	g, lookups := v1r21Listener(t, v1r21Table(v1r21Bans(2300, "INPUT", " -p tcp -m tcp --dport 443")...))
	const want = "controller listener: unknown (the rules and routes split the internet into 4601 ranges for this flow; " +
		"checking them against 2300 rules is more than the guard evaluates (limit 10000000))"
	if g.OK || !strings.Contains(g.Message, want) {
		t.Errorf("guard = %+v, want a refusal containing %q", g, want)
	}
	if lookups != 0 {
		t.Errorf("%d route lookups, want none: a flow over the limit is not evaluated", lookups)
	}
}

func TestW11b06LocalRouteInMainCutsCells(t *testing.T) {
	st := v1r21State(t, v1r21Table("-A INPUT -s 172.30.0.3/32 -i dp-panel -p tcp -m tcp --dport 443 -j DROP"), v1r21Table())
	check := func(table []netstat.Route) (msg string, ok bool) {
		t.Helper()
		g := v1r21CheckHost(t, v1r21Fib{&gHost{ifaces: ckIfaces(), table: table}}, st, v1r21Tunnel())
		return g.Message, g.OK
	}
	const want = "exposure exp2 tunnel from 172.30.0.3 to 45.0.0.0-45.255.255.255:443 on dp-panel: drop"
	if msg, ok := check(append(v1r21HostRoutes(), v1r21Route("local", "45.0.0.0/8", "lo", "-", 254))); ok || !strings.Contains(msg, want) {
		t.Errorf("guard: %v %q, want a refusal containing %q: the range is delivered locally, where the rule drops it", ok, msg, want)
	}
	if msg, ok := check(v1r21HostRoutes()); !ok {
		t.Errorf("guard: %q, want OK: without the local route no cell is delivered locally", msg)
	}
}

func TestW11b06CuttersKeepEveryRuleUnderAnUnmodelledRewrite(t *testing.T) {
	const ban = "-A INPUT -s 8.8.8.8/32 -p tcp -m tcp --dport 8443 -j DROP"
	for _, tt := range []struct {
		name, nat string
		kept      bool
	}{
		{"REDIRECT", "-A PREROUTING -p tcp -m tcp --dport 443 -j REDIRECT --to-ports 8443", true},
		{"a DNAT to another port", "-A PREROUTING -p tcp -m tcp --dport 443 -j DNAT --to-destination 192.0.2.1:9443", false},
		{"MASQUERADE", "-A POSTROUTING -j MASQUERADE", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			st := v1r21State(t, v1r21Jail("22", []string{ban}, tt.nat), v1r21Table())
			rules := cutters(st.V4, false, "tcp", 443).Table("filter").Chain("INPUT").Rules
			got := slices.ContainsFunc(rules, func(r netfilter.Rule) bool {
				return r.Source.Prefix == netip.MustParsePrefix("8.8.8.8/32")
			})
			if got != tt.kept {
				t.Errorf("the ban on 8443 kept = %v, want %v, in %+v", got, tt.kept, rules)
			}
		})
	}
}

func TestW11b06LongestRouteDecidesACell(t *testing.T) {
	type mark struct{ noPath, blackhole bool }
	const wide, narrow = "45.9.0.0/16", "45.9.1.0/24"
	route := func(typ, dst string) netstat.Route { return v1r21Route(typ, dst, "", "-", 254) }
	for _, tt := range []struct {
		name         string
		routes       []netstat.Route
		rest, inside mark // the /16 outside the /24, and the /24
	}{
		{"a unicast route inside a blackhole", []netstat.Route{route("blackhole", wide), route("unicast", narrow)}, mark{true, true}, mark{false, false}},
		{"a unicast route inside an unreachable route", []netstat.Route{route("unreachable", wide), route("unicast", narrow)}, mark{true, false}, mark{false, false}},
		{"a local route inside a blackhole", []netstat.Route{route("blackhole", wide), route("local", narrow)}, mark{true, true}, mark{false, false}},
		{"an unreachable route inside a blackhole", []netstat.Route{route("blackhole", wide), route("unreachable", narrow)}, mark{true, true}, mark{true, false}},
		{"a blackhole inside a unicast route", []netstat.Route{route("unicast", wide), route("blackhole", narrow)}, mark{false, false}, mark{true, true}},
		{"a blackhole inside an unreachable route", []netstat.Route{route("unreachable", wide), route("blackhole", narrow)}, mark{true, false}, mark{true, true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := newRouter(&gHost{table: tt.routes}, facts{ifaces: ckIfaces()}, fwstate.Loaded{})
			fl := flow{label: "x", pkt: flPkt("input", "0.0.0.0", 0, "192.0.2.1", 443, "tcp"), wild: wildSrc}
			got := map[string]mark{}
			for _, cf := range r.cellFlows(fl, nil) {
				got[cf.pkt.Src.String()] = mark{cf.standIn, cf.blackhole}
			}
			for rep, want := range map[string]mark{"45.9.0.0": tt.rest, "45.9.1.0": tt.inside, "45.9.2.0": tt.rest} {
				if got[rep] != want {
					t.Errorf("cell at %s = %+v, want %+v", rep, got[rep], want)
				}
			}
		})
	}
}

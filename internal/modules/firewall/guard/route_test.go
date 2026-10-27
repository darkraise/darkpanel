package guard

import (
	"errors"
	"fmt"
	"net/netip"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// rtQuery is a route lookup; src and iif may be empty.
func rtQuery(dst, src, iif string) netstat.RouteQuery {
	q := netstat.RouteQuery{Dst: netip.MustParseAddr(dst), IIF: iif}
	if src != "" {
		q.Src = netip.MustParseAddr(src)
	}
	return q
}

// rtVia is a unicast route through iface with a preferred source.
func rtVia(iface, prefSrc string) netstat.Route {
	r := netstat.Route{Type: "unicast", OutIface: iface}
	if prefSrc != "" {
		r.PrefSrc = netip.MustParseAddr(prefSrc)
	}
	return r
}

// rtFacts are the facts of gIfaces with the given live rp_filter values for
// all, eth0 and eth1.
func rtFacts(all, eth0, eth1 int) facts {
	return facts{ifaces: gIfaces(), rpFilter: map[string]int{"all": all, "eth0": eth0, "eth1": eth1}}
}

// rtProbe is a probe of a packet that was evaluated.
func rtProbe(desc string, p fwstate.Packet, in, out string) probe {
	p.InIface, p.OutIface = in, out
	return probe{desc: desc, pkt: p}
}

func TestProbes(t *testing.T) {
	ssh4 := flPkt("input", "203.0.113.7", 50000, "192.0.2.1", 22, "tcp")
	ssh6 := flPkt("input", "2001:db8:9::9", 50004, "2001:db8::1", 22, "tcp")
	panel := flPkt("input", "203.0.113.7", 51000, "0.0.0.0", 443, "tcp")
	panelResolved := flPkt("input", "203.0.113.7", 51000, "192.0.2.1", 443, "tcp")
	link := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	tsUDP := flPkt("output", "0.0.0.0", 41641, "198.51.100.1", 41641, "udp")
	tsUDPResolved := flPkt("output", "192.0.2.1", 41641, "198.51.100.1", 41641, "udp")
	tunnel := flOn("dp-panel", flPkt("forward", "172.30.0.3", 49152, "198.51.100.1", 7844, "udp"))
	proxy := flOn("dp-panel", flPkt("input", "172.30.0.2", 49152, "172.30.0.1", 8080, "tcp"))
	sshDesc := "ssh from 203.0.113.7 to 192.0.2.1:22"
	deadline := fmt.Errorf("netstat: route lookup: %w", os.ErrDeadlineExceeded)
	for _, tc := range []struct {
		name      string
		fl        flow
		f         facts
		sysctl    map[string]string
		routes    map[netstat.RouteQuery]netstat.Route
		routeErrs map[netstat.RouteQuery]error
		want      []probe
		lookups   []netstat.RouteQuery
	}{
		{
			name: "every up non-loopback interface",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(0, 0, 0),
			want: []probe{rtProbe(sshDesc+" on eth0", ssh4, "eth0", ""), rtProbe(sshDesc+" on eth1", ssh4, "eth1", "")},
		},
		{
			name: "strict rp_filter keeps only the interface of the route back",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(1, 0, 0),
			routes:  map[netstat.RouteQuery]netstat.Route{rtQuery("203.0.113.7", "192.0.2.1", ""): rtVia("eth0", "192.0.2.1")},
			want:    []probe{rtProbe(sshDesc+" on eth0", ssh4, "eth0", "")},
			lookups: []netstat.RouteQuery{rtQuery("203.0.113.7", "192.0.2.1", "")},
		},
		{
			name: "loose rp_filter excludes nothing",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(0, 2, 2),
			want: []probe{rtProbe(sshDesc+" on eth0", ssh4, "eth0", ""), rtProbe(sshDesc+" on eth1", ssh4, "eth1", "")},
		},
		{
			name: "the checked state's rp_filter wins over the live value",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(0, 0, 0),
			sysctl: map[string]string{"net/ipv4/conf/eth1/rp_filter": "1"},
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("203.0.113.7", "192.0.2.1", ""): rtVia("eth0", "192.0.2.1")},
			want:   []probe{rtProbe(sshDesc+" on eth0", ssh4, "eth0", "")},
		},
		{
			name: "a non-numeric rp_filter in the checked state is unknown",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(0, 0, 0),
			sysctl: map[string]string{"net/ipv4/conf/all/rp_filter": "strict"},
			want:   []probe{{desc: sshDesc, unknown: `net/ipv4/conf/all/rp_filter is "strict" in the checked state, not a number`}},
		},
		{
			name: "IPv6 has no rp_filter",
			fl:   flow{label: "ssh", pkt: ssh6}, f: rtFacts(1, 1, 1),
			want: []probe{
				rtProbe("ssh from 2001:db8:9::9 to [2001:db8::1]:22 on eth0", ssh6, "eth0", ""),
				rtProbe("ssh from 2001:db8:9::9 to [2001:db8::1]:22 on eth1", ssh6, "eth1", ""),
			},
		},
		{
			name: "an unresolvable route back under strict rp_filter is unknown",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(1, 0, 0),
			routeErrs: map[netstat.RouteQuery]error{rtQuery("203.0.113.7", "192.0.2.1", ""): deadline},
			want: []probe{{desc: sshDesc,
				unknown: "the route back to 203.0.113.7 cannot be resolved: netstat: route lookup: i/o timeout"}},
		},
		{
			name: "no eligible interface is unknown",
			fl:   flow{label: "ssh", pkt: ssh4}, f: rtFacts(1, 0, 0),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("203.0.113.7", "192.0.2.1", ""): rtVia("wg0", "10.8.0.1")},
			want:   []probe{{desc: sshDesc, unknown: "no interface can receive the flow"}},
		},
		{
			name: "an unspecified destination is the source the host answers from",
			fl:   flow{label: "panel", pkt: panel}, f: rtFacts(0, 0, 0),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("203.0.113.7", "", ""): rtVia("eth0", "192.0.2.1")},
			want: []probe{
				rtProbe("panel from 203.0.113.7 to 192.0.2.1:443 on eth0", panelResolved, "eth0", ""),
				rtProbe("panel from 203.0.113.7 to 192.0.2.1:443 on eth1", panelResolved, "eth1", ""),
			},
		},
		{
			name: "an unspecified destination without a route back is unknown",
			fl:   flow{label: "panel", pkt: panel}, f: rtFacts(0, 0, 0),
			want: []probe{{desc: "panel from 203.0.113.7 to 0.0.0.0:443",
				unknown: "the route back to 203.0.113.7 cannot be resolved: netstat: no route"}},
		},
		{
			name: "a fixed in-interface is the only one",
			fl:   flow{label: "exposure exp1 proxy", pkt: proxy}, f: rtFacts(1, 1, 1),
			want: []probe{rtProbe("exposure exp1 proxy from 172.30.0.2 to 172.30.0.1:8080 on dp-panel", proxy, "dp-panel", "")},
		},
		{
			name: "outbound from a known source",
			fl:   flow{label: "controller link", pkt: link}, f: rtFacts(0, 0, 0),
			routes:  map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.9", "192.0.2.1", ""): rtVia("eth0", "192.0.2.1")},
			want:    []probe{rtProbe("controller link from 192.0.2.1 to 198.51.100.9:7443 out eth0", link, "", "eth0")},
			lookups: []netstat.RouteQuery{rtQuery("198.51.100.9", "192.0.2.1", "")},
		},
		{
			name: "outbound from the source the route chooses",
			fl:   flow{label: "tailscale", pkt: tsUDP, standIn: true}, f: rtFacts(0, 0, 0),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.1", "", ""): rtVia("eth0", "192.0.2.1")},
			want:   []probe{rtProbe("tailscale udp from 192.0.2.1 to 198.51.100.1:41641 out eth0", tsUDPResolved, "", "eth0")},
		},
		{
			name: "a stand-in destination without a route has nothing to protect",
			fl:   flow{label: "tailscale", pkt: tsUDP, standIn: true}, f: rtFacts(0, 0, 0),
		},
		{
			name: "a stand-in destination whose lookup fails otherwise is unknown",
			fl:   flow{label: "tailscale", pkt: tsUDP, standIn: true}, f: rtFacts(0, 0, 0),
			routeErrs: map[netstat.RouteQuery]error{rtQuery("198.51.100.1", "", ""): deadline},
			want: []probe{{desc: "tailscale udp from 0.0.0.0 to 198.51.100.1:41641",
				unknown: "the route to 198.51.100.1 cannot be resolved: netstat: route lookup: i/o timeout"}},
		},
		{
			name: "a real destination without a route is unknown",
			fl:   flow{label: "controller link", pkt: link}, f: rtFacts(0, 0, 0),
			want: []probe{{desc: "controller link from 192.0.2.1 to 198.51.100.9:7443",
				unknown: "the route to 198.51.100.9 cannot be resolved: netstat: no route"}},
		},
		{
			name: "a multipath route is unknown",
			fl:   flow{label: "controller link", pkt: link}, f: rtFacts(0, 0, 0),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.9", "192.0.2.1", ""): rtVia("", "192.0.2.1")},
			want: []probe{{desc: "controller link from 192.0.2.1 to 198.51.100.9:7443",
				unknown: "the route to 198.51.100.9 has no single out-interface"}},
		},
		{
			name: "forwarded flow",
			fl:   flow{label: "exposure exp2 tunnel", pkt: tunnel, standIn: true}, f: rtFacts(0, 0, 0),
			routes: map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.1", "172.30.0.3", "dp-panel"): rtVia("eth0", "")},
			want:   []probe{rtProbe("exposure exp2 tunnel udp from 172.30.0.3 to 198.51.100.1:7844 on dp-panel out eth0", tunnel, "dp-panel", "eth0")},
		},
		{
			name: "forwarded flow with forwarding off",
			fl:   flow{label: "exposure exp2 tunnel", pkt: tunnel, standIn: true}, f: rtFacts(0, 0, 0),
			sysctl: map[string]string{"net/ipv4/ip_forward": "0"},
			want: []probe{{desc: "exposure exp2 tunnel udp from 172.30.0.3 to 198.51.100.1:7844 on dp-panel",
				unknown: "forwarding is off (net/ipv4/ip_forward = 0)"}},
		},
		{
			name: "a flow that is already unknown",
			fl:   flow{label: "controller topology", unknown: "the controller's topology is incomplete"},
			want: []probe{{desc: "controller topology", unknown: "the controller's topology is incomplete"}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &gHost{ifaces: gIfaces(), routes: tc.routes, routeErrs: tc.routeErrs}
			r := newRouter(h, tc.f, fwstate.Loaded{Sysctl: tc.sysctl})
			got := r.probes(tc.fl)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("probes =\n%+v\nwant\n%+v", got, tc.want)
			}
			if tc.lookups != nil && !reflect.DeepEqual(h.lookups, tc.lookups) {
				t.Errorf("lookups = %+v, want %+v", h.lookups, tc.lookups)
			}
		})
	}
}

func TestProbesTailscaleSource(t *testing.T) {
	ts := func(up bool) netstat.Interface {
		return netstat.Interface{Index: 5, Name: "tailscale0", Up: up, Running: up, Addrs: []netip.Prefix{netip.MustParsePrefix("100.101.1.1/32")}}
	}
	withTS := func(up bool) facts {
		f := rtFacts(1, 0, 0)
		f.ifaces = append(f.ifaces, ts(up))
		f.rpFilter["tailscale0"] = 0
		return f
	}
	tsSSH4 := flPkt("input", "100.101.1.2", 50000, "100.101.1.1", 22, "tcp")
	tsSSH6 := flPkt("input", "fd7a:115c:a1e0::5", 50001, "2001:db8::1", 22, "tcp")
	ssh4 := flPkt("input", "203.0.113.7", 50000, "192.0.2.1", 22, "tcp")
	for _, tc := range []struct {
		name string
		p    fwstate.Packet
		f    facts
		want []string
	}{
		{"an IPv4 tailnet source only on tailscale0", tsSSH4, withTS(true), []string{"tailscale0"}},
		{"an IPv6 tailnet source only on tailscale0", tsSSH6, withTS(true), []string{"tailscale0"}},
		{"tailscale0 down keeps the normal rule", tsSSH4, withTS(false), []string{"eth0"}},
		{"no tailscale0 keeps the normal rule", tsSSH4, rtFacts(1, 0, 0), []string{"eth0"}},
		{"another source is not limited to tailscale0", ssh4, withTS(true), []string{"eth0"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &gHost{routes: map[netstat.RouteQuery]netstat.Route{
				rtQuery("100.101.1.2", "100.101.1.1", ""): rtVia("eth0", ""),
				rtQuery("100.101.1.2", "", ""):            rtVia("eth0", ""),
				rtQuery("203.0.113.7", "192.0.2.1", ""):   rtVia("eth0", ""),
			}}
			var got []string
			for _, pr := range newRouter(h, tc.f, fwstate.Loaded{}).probes(flow{label: "ssh", pkt: tc.p}) {
				if pr.unknown != "" {
					t.Fatalf("unknown probe: %s", pr.unknown)
				}
				got = append(got, pr.pkt.InIface)
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("in-interfaces = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestRouterRoute(t *testing.T) {
	fwd := flOn("eth0", flPkt("input", "198.51.100.1", 49152, "172.17.0.2", 80, "tcp"))
	out := flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")
	toDocker := rtQuery("172.17.0.2", "198.51.100.1", "eth0")
	for _, tc := range []struct {
		name      string
		p         fwstate.Packet
		sysctl    map[string]string
		routes    map[netstat.RouteQuery]netstat.Route
		routeErrs map[netstat.RouteQuery]error
		path, oif string
		err       string
	}{
		{name: "a host address", p: flOn("eth0", flPkt("input", "198.51.100.1", 49152, "10.0.0.1", 80, "tcp")), path: "input"},
		{name: "a loopback address", p: flOn("eth0", flPkt("input", "198.51.100.1", 49152, "127.0.0.2", 80, "tcp")), path: "input"},
		{name: "forwarded", p: fwd, routes: map[netstat.RouteQuery]netstat.Route{toDocker: rtVia("docker0", "")}, path: "forward", oif: "docker0"},
		{name: "a local route", p: fwd, routes: map[netstat.RouteQuery]netstat.Route{toDocker: {Type: "local", OutIface: "lo"}}, path: "input"},
		{name: "forwarding off", p: fwd, sysctl: map[string]string{"net/ipv4/ip_forward": "0"}, err: "firewall: forwarding is off (net/ipv4/ip_forward = 0)"},
		{name: "no route", p: fwd, err: "firewall: route to 172.17.0.2 from 198.51.100.1 on eth0: netstat: no route"},
		{name: "multipath", p: fwd, routes: map[netstat.RouteQuery]netstat.Route{toDocker: rtVia("", "")}, err: "firewall: the route to 172.17.0.2 has no single out-interface"},
		{name: "outbound", p: out, routes: map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.9", "192.0.2.1", ""): rtVia("eth1", "")}, path: "output", oif: "eth1"},
		{name: "outbound without a route", p: out, err: "firewall: route to 198.51.100.9: netstat: no route"},
		{name: "outbound multipath", p: out, routes: map[netstat.RouteQuery]netstat.Route{rtQuery("198.51.100.9", "192.0.2.1", ""): rtVia("", "")},
			err: "firewall: the route to 198.51.100.9 has no single out-interface"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := newRouter(&gHost{routes: tc.routes, routeErrs: tc.routeErrs}, rtFacts(0, 0, 0), fwstate.Loaded{Sysctl: tc.sysctl})
			path, oif, err := r.route(tc.p)
			if tc.err != "" {
				if err == nil || err.Error() != tc.err {
					t.Fatalf("route error = %v, want %q", err, tc.err)
				}
				if strings.Contains(tc.err, "no route") && !errors.Is(err, netstat.ErrNoRoute) {
					t.Errorf("error %v does not wrap ErrNoRoute", err)
				}
				return
			}
			if err != nil || path != tc.path || oif != tc.oif {
				t.Errorf("route = %q, %q, %v; want %q, %q", path, oif, err, tc.path, tc.oif)
			}
		})
	}
}

func TestDescribe(t *testing.T) {
	p := flPkt("input", "2001:db8:9::9", 50004, "2001:db8::1", 22, "tcp")
	p.InIface = "eth0"
	if got, want := describe("ssh", p), "ssh from 2001:db8:9::9 to [2001:db8::1]:22 on eth0"; got != want {
		t.Errorf("describe = %q, want %q", got, want)
	}
	q := flPkt("forward", "172.30.0.3", 49152, "198.51.100.1", 7844, "udp")
	q.InIface, q.OutIface = "dp-panel", "eth0"
	if got, want := describe("exposure x tunnel", q), "exposure x tunnel udp from 172.30.0.3 to 198.51.100.1:7844 on dp-panel out eth0"; got != want {
		t.Errorf("describe = %q, want %q", got, want)
	}
}

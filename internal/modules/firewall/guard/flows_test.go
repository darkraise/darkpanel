package guard

import (
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// flPkt is the expected packet of a new connection.
func flPkt(path, src string, sport uint16, dst string, dport uint16, proto string) fwstate.Packet {
	s, d := netip.MustParseAddr(src), netip.MustParseAddr(dst)
	fam := fwstate.V4
	if s.Is6() {
		fam = fwstate.V6
	}
	return fwstate.Packet{Family: fam, Path: path, Src: s, SrcPort: sport, Dst: d, DstPort: dport, Proto: proto, CtState: "NEW"}
}

// flOn is flPkt with a fixed in-interface.
func flOn(iface string, p fwstate.Packet) fwstate.Packet {
	p.InIface = iface
	return p
}

// flSock is a socket of the table held by one process.
func flSock(proto netstat.Proto, state netstat.State, local, remote, comm string, pid int) netstat.Socket {
	s := netstat.Socket{Proto: proto, Family: netstat.IPv4, State: state, Local: netip.MustParseAddrPort(local)}
	if remote != "" {
		s.Remote = netip.MustParseAddrPort(remote)
	}
	if s.Local.Addr().Is6() {
		s.Family = netstat.IPv6
	}
	if comm != "" {
		s.Processes = []netstat.Process{{PID: pid, Comm: comm}}
	}
	return s
}

// gxMark is p with the packet mark set.
func gxMark(p fwstate.Packet, mark uint32) fwstate.Packet {
	p.Mark = mark
	return p
}

func flAddrPorts(s ...string) []netip.AddrPort {
	out := make([]netip.AddrPort, len(s))
	for i, a := range s {
		out[i] = netip.MustParseAddrPort(a)
	}
	return out
}

func TestBuildFlows(t *testing.T) {
	const tcp, udp = netstat.ProtoTCP, netstat.ProtoUDP
	const est, listen, closed = netstat.StateEstablished, netstat.StateListen, netstat.StateClose
	client4 := &core.Peer{Addr: netip.MustParseAddr("203.0.113.7"), Port: 51000}
	client6 := &core.Peer{Addr: netip.MustParseAddr("2001:db8:5::7"), Port: 52000}
	panelListen := flAddrPorts("0.0.0.0:443", "[::]:8443", "192.0.2.1:9443")
	npm := &core.ProxyPath{ExposureID: "exp1", Kind: "npm", ProxyAddr: netip.MustParseAddr("172.30.0.2"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080"), Public: flAddrPorts("192.0.2.1:443", "[2001:db8::1]:443", "203.0.113.80:443", "[2001:db8:77::80]:443")}
	tunnel := &core.ProxyPath{ExposureID: "exp2", Kind: "tunnel", ProxyAddr: netip.MustParseAddr("172.30.0.3"),
		ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}
	for _, tc := range []struct {
		name string
		in   Input
		f    facts
		want []flow
	}{
		{
			name: "nothing to protect",
			f:    facts{ifaces: gIfaces()},
		},
		{
			name: "ssh sessions",
			f: facts{ifaces: gIfaces(), sockets: []netstat.Socket{
				flSock(tcp, listen, "0.0.0.0:22", "", "sshd", 10),
				flSock(tcp, est, "192.0.2.1:22", "203.0.113.7:50000", "sshd-session", 11),
				flSock(tcp, est, "[::ffff:192.0.2.1]:22", "[::ffff:203.0.113.8]:50001", "sshd", 12),
				flSock(tcp, est, "192.0.2.1:41000", "198.51.100.20:80", "sshd-session", 11),
				flSock(tcp, est, "127.0.0.1:22", "127.0.0.1:50002", "sshd-session", 13),
				flSock(tcp, est, "192.0.2.1:22", "192.0.2.1:50003", "sshd-session", 14),
				flSock(tcp, listen, "[::]:2222", "", "systemd", 1),
				flSock(tcp, est, "[2001:db8::1]:2222", "[2001:db8:9::9]:50004", "sshd-auth", 15),
				flSock(tcp, est, "192.0.2.1:22", "203.0.113.9:50005", "nginx", 16),
			}},
			want: []flow{
				{label: "ssh", pkt: flPkt("input", "203.0.113.7", 0, "192.0.2.1", 22, "tcp")},
				{label: "ssh", pkt: flPkt("input", "203.0.113.8", 0, "192.0.2.1", 22, "tcp")},
				{label: "ssh", pkt: flPkt("input", "2001:db8:9::9", 0, "2001:db8::1", 2222, "tcp")},
			},
		},
		{
			name: "panel clients with a local controller",
			in: Input{
				Plan:    &core.GuardContext{Client: client4, PanelListen: panelListen, Complete: true},
				Refresh: &core.GuardContext{Client: client6, PanelListen: panelListen, Complete: true},
			},
			f: facts{ifaces: gIfaces(), controller: 4242},
			want: []flow{
				{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "0.0.0.0", 443, "tcp")},
				{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "0.0.0.0", 8443, "tcp")},
				{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "192.0.2.1", 9443, "tcp")},
				{label: "panel", pkt: flPkt("input", "2001:db8:5::7", 0, "::", 8443, "tcp")},
			},
		},
		{
			name: "panel listener on a host address means the controller runs here",
			in:   Input{Plan: &core.GuardContext{Client: client4, PanelListen: flAddrPorts("192.0.2.1:443", "198.51.100.80:443"), Complete: true}},
			f:    facts{ifaces: gIfaces()},
			want: []flow{{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "192.0.2.1", 443, "tcp")}},
		},
		{
			name: "no controller on this host",
			in: Input{Plan: &core.GuardContext{Client: client4, Via: npm, PanelListen: flAddrPorts("198.51.100.80:443"),
				AgentListen: flAddrPorts("198.51.100.80:7443"), AgentPeers: []netip.Addr{netip.MustParseAddr("192.0.2.77")}, Complete: true}},
			f: facts{ifaces: gIfaces()},
		},
		{
			name: "a client that came through an exposure is not a direct panel flow",
			in:   Input{Plan: &core.GuardContext{Client: client4, Via: npm, PanelListen: panelListen, Complete: true}},
			f:    facts{ifaces: gIfaces(), controller: 4242},
			want: []flow{
				{label: "exposure exp1 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 8080, "tcp"))},
				{label: "exposure exp1 public", pkt: flPkt("input", "198.51.100.1", 0, "192.0.2.1", 443, "tcp")},
				{label: "exposure exp1 public", pkt: flPkt("input", "2001:db8:ffff::1", 0, "2001:db8::1", 443, "tcp")},
				{label: "exposure exp1 public", pkt: flPkt("input", "198.51.100.1", 0, "0.0.0.0", 443, "tcp")},
				{label: "exposure exp1 public", pkt: flPkt("input", "2001:db8:ffff::1", 0, "::", 443, "tcp")},
			},
		},
		{
			name: "tunnel exposure",
			in:   Input{Plan: &core.GuardContext{Via: tunnel, Complete: true}},
			f:    facts{ifaces: gIfaces(), controller: 4242},
			want: []flow{
				{label: "exposure exp2 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.3", 0, "172.30.0.1", 8080, "tcp"))},
				{label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "198.51.100.1", 7844, "udp")), standIn: true},
				{label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "198.51.100.1", 7844, "tcp")), standIn: true},
				{label: "exposure exp2 tunnel", pkt: flOn("dp-panel", flPkt("forward", "172.30.0.3", 0, "198.51.100.1", 443, "tcp")), standIn: true},
			},
		},
		{
			name: "retired exposure",
			in:   Input{Plan: &core.GuardContext{Via: tunnel, Complete: true}, Refresh: &core.GuardContext{Via: npm, Complete: true}, Retired: []string{"exp2", "exp1"}},
			f:    facts{ifaces: gIfaces(), controller: 4242},
		},
		{
			name: "malformed exposures",
			in: Input{
				Plan:    &core.GuardContext{Via: &core.ProxyPath{ExposureID: "exp3", Kind: "npm"}, Complete: true},
				Refresh: &core.GuardContext{Via: &core.ProxyPath{ExposureID: "exp4", Kind: "carrier-pigeon", ProxyAddr: netip.MustParseAddr("172.30.0.4"), ListenerAddr: netip.MustParseAddrPort("172.30.0.1:8080")}, Complete: true},
			},
			f: facts{ifaces: gIfaces(), controller: 4242},
			want: []flow{
				{label: "exposure exp3", unknown: "the exposure has no proxy or listener address"},
				{label: "exposure exp4 proxy", pkt: flOn("dp-panel", flPkt("input", "172.30.0.4", 0, "172.30.0.1", 8080, "tcp"))},
				{label: "exposure exp4", unknown: "the exposure kind carrier-pigeon is not npm or tunnel"},
			},
		},
		{
			name: "agent links",
			in: Input{Plan: &core.GuardContext{AgentListen: flAddrPorts("0.0.0.0:7443", "[::]:7444"), Complete: true,
				AgentPeers: []netip.Addr{netip.MustParseAddr("198.51.100.7"), netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("2001:db8:7::7")}}},
			f: facts{ifaces: gIfaces(), controller: 4242},
			want: []flow{
				{label: "agent link", pkt: flPkt("input", "198.51.100.7", 0, "0.0.0.0", 7443, "tcp")},
				{label: "agent link", pkt: flPkt("input", "198.51.100.7", 0, "0.0.0.0", 7444, "tcp")},
				{label: "agent link", pkt: flPkt("input", "2001:db8:7::7", 0, "::", 7444, "tcp")},
			},
		},
		{
			name: "local controller sessions from live facts",
			f: facts{ifaces: gIfaces(), controller: 4242, sockets: []netstat.Socket{
				flSock(tcp, listen, "[::]:443", "", "darkpanel-contr", 4242),
				flSock(tcp, est, "[::ffff:192.0.2.1]:443", "[::ffff:203.0.113.9]:40000", "darkpanel-contr", 4242),
				flSock(tcp, est, "192.0.2.1:40001", "198.51.100.50:443", "darkpanel-contr", 4242),
				flSock(tcp, est, "[::1]:443", "[::1]:40002", "darkpanel-contr", 4242),
			}},
			want: []flow{
				{label: "controller session", pkt: flPkt("input", "203.0.113.9", 0, "192.0.2.1", 443, "tcp")},
				{label: "controller listener", pkt: flPkt("input", "198.51.100.1", 0, "0.0.0.0", 443, "tcp")},
				{label: "controller listener", pkt: flPkt("input", "2001:db8:ffff::1", 0, "::", 443, "tcp")},
			},
		},
		{
			name: "the local controller's listening sockets",
			f: facts{ifaces: ckIfaces(), controller: 4242, sockets: []netstat.Socket{
				flSock(tcp, listen, "0.0.0.0:7443", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "192.0.2.1:9443", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "[2001:db8::1]:9444", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "172.30.0.1:9445", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "198.51.100.80:9446", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "127.0.0.1:9447", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "[::1]:9448", "", "darkpanel-contr", 4242),
				flSock(udp, closed, "0.0.0.0:9449", "", "darkpanel-contr", 4242),
				flSock(tcp, listen, "0.0.0.0:9450", "", "nginx", 500),
				flSock(tcp, listen, "0.0.0.0:7443", "", "darkpanel-contr", 4242),
			}},
			want: []flow{
				{label: "controller listener", pkt: flPkt("input", "198.51.100.1", 0, "0.0.0.0", 7443, "tcp")},
				{label: "controller listener", pkt: flPkt("input", "198.51.100.1", 0, "192.0.2.1", 9443, "tcp")},
				{label: "controller listener", pkt: flPkt("input", "2001:db8:ffff::1", 0, "2001:db8::1", 9444, "tcp")},
				{label: "controller listener", pkt: flOn("dp-panel", flPkt("input", "172.30.0.2", 0, "172.30.0.1", 9445, "tcp"))},
			},
		},
		{
			name: "a runtime listener on a bridge address without a proxy address",
			f: facts{controller: 4242, sockets: []netstat.Socket{flSock(tcp, listen, "172.30.0.1:9445", "", "darkpanel-contr", 4242)},
				ifaces: append(gIfaces(), netstat.Interface{Index: 6, Name: "dp-panel", Up: true, Addrs: []netip.Prefix{netip.MustParsePrefix("172.30.0.1/32")}})},
		},
		{
			name: "listening sockets without a controller process",
			f: facts{ifaces: gIfaces(), sockets: []netstat.Socket{
				flSock(tcp, listen, "0.0.0.0:7443", "", "darkpanel-contr", 4242),
			}},
		},
		{
			name: "the agent's outbound sessions",
			f: facts{ifaces: gIfaces(), pid: 99, sockets: []netstat.Socket{
				flSock(tcp, est, "192.0.2.1:40100", "198.51.100.9:7443", "darkpanel-agent", 99),
				flSock(tcp, est, "127.0.0.1:40101", "127.0.0.1:7443", "darkpanel-agent", 99),
				flSock(tcp, listen, "0.0.0.0:9100", "", "darkpanel-agent", 99),
				flSock(tcp, est, "192.0.2.1:9100", "198.51.100.10:40102", "darkpanel-agent", 99),
				flSock(tcp, est, "192.0.2.1:40103", "198.51.100.11:443", "curl", 500),
			}},
			want: []flow{{label: "controller link", pkt: flPkt("output", "192.0.2.1", 40100, "198.51.100.9", 7443, "tcp")}},
		},
		{
			name: "tailscaled",
			f: facts{ifaces: gIfaces(), sockets: []netstat.Socket{
				flSock(udp, closed, "0.0.0.0:41641", "", "tailscaled", 300),
				flSock(udp, closed, "[::]:41641", "", "tailscaled", 300),
				flSock(udp, closed, "127.0.0.1:41000", "", "tailscaled", 300),
				flSock(udp, closed, "0.0.0.0:0", "", "tailscaled", 300),
				flSock(tcp, est, "100.64.0.1:41002", "100.64.0.2:443", "tailscaled", 300),
			}},
			want: []flow{
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 41641, "198.51.100.1", 41641, "udp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "0.0.0.0", 0, "198.51.100.1", 443, "tcp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "::", 41641, "2001:db8:ffff::1", 41641, "udp"), 0x80000), standIn: true},
				{label: "tailscale", pkt: gxMark(flPkt("output", "::", 0, "2001:db8:ffff::1", 443, "tcp"), 0x80000), standIn: true},
			},
		},
		{
			name: "incomplete topology once for both contexts",
			in:   Input{Plan: &core.GuardContext{}, Refresh: &core.GuardContext{}},
			f:    facts{ifaces: gIfaces()},
			want: []flow{{label: "controller topology", unknown: "the controller's topology is incomplete"}},
		},
		{
			name: "plan and refresh merge without duplicates",
			in: Input{
				Plan:    &core.GuardContext{Client: client4, PanelListen: flAddrPorts("192.0.2.1:443"), Complete: true},
				Refresh: &core.GuardContext{Client: client4, PanelListen: flAddrPorts("192.0.2.1:443", "10.0.0.1:443"), Complete: true},
			},
			f: facts{ifaces: gIfaces()},
			want: []flow{
				{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "192.0.2.1", 443, "tcp")},
				{label: "panel", pkt: flPkt("input", "203.0.113.7", 0, "10.0.0.1", 443, "tcp")},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := buildFlows(tc.in, tc.f)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("buildFlows =\n%+v\nwant\n%+v", got, tc.want)
			}
		})
	}
}

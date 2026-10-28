package guard

import (
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// Input is what a guard check protects besides the host's live facts.
type Input struct {
	Plan    *core.GuardContext // the plan input's guard (nil for break-glass and system plans)
	Refresh *core.GuardContext // env.Call.GuardRefresh at apply; nil at plan
	Retired []string           // env.Call.Retired
	Eval    func(rs *netfilter.Ruleset, p fwstate.Packet, route fwstate.Router) fwstate.Result
}

// panelBridge is the Docker bridge of the panel network (spec 8).
const panelBridge = "dp-panel"

// unknownPort is the source port of a connection that does not exist yet:
// the evaluator treats a source-port match against it as undecidable.
const unknownPort uint16 = 0

// The documentation addresses that stand in for "any internet address".
var (
	internet4 = netip.MustParseAddr("198.51.100.1")
	internet6 = netip.MustParseAddr("2001:db8:ffff::1")
)

// sshComms are the process names that hold an OpenSSH session's socket:
// OpenSSH before 9.8 keeps it in sshd, later releases in sshd-session, and
// 10.0 authenticates in sshd-auth.
var sshComms = []string{"sshd", "sshd-session", "sshd-auth"}

// tailscaled is the process name of the Tailscale daemon.
const tailscaled = "tailscaled"

// tailscaleMark is the bypass mark tailscaled puts on its own sockets, so
// policy routing sends its traffic around the tailnet's routes.
const tailscaleMark uint32 = 0x80000

// flow is one protected flow before routing: pkt has no in- or
// out-interface yet unless the flow fixes its in-interface. A flow with a
// non-empty unknown is not evaluated and fails with that reason.
type flow struct {
	label   string
	pkt     fwstate.Packet
	standIn bool // pkt.Dst stands in for any destination: with no route of its family there is nothing to protect
	unknown string
}

// flowSet collects flows in order without duplicates. Two flows with the
// same packet are one flow, whichever label came first.
type flowSet struct {
	flows []flow
	seen  map[flowKey]bool
}

type flowKey struct {
	pkt            fwstate.Packet
	label, unknown string // set only for a flow that is not evaluated
}

func (s *flowSet) add(f flow) {
	k := flowKey{pkt: f.pkt}
	if f.unknown != "" {
		k = flowKey{label: f.label, unknown: f.unknown}
	}
	if s.seen[k] {
		return
	}
	s.seen[k] = true
	s.flows = append(s.flows, f)
}

// buildFlows returns every protected flow of spec 2.3 for in and f, in the
// spec's order, with Plan and Refresh merged.
func buildFlows(in Input, f facts) []flow {
	s := &flowSet{seen: map[flowKey]bool{}}
	local := localAddrs(f.ifaces)
	var ctxs []*core.GuardContext
	for _, g := range []*core.GuardContext{in.Plan, in.Refresh} {
		if g != nil {
			ctxs = append(ctxs, g)
			if !g.Complete {
				s.add(flow{label: "controller topology", unknown: "the controller's topology is incomplete"})
			}
		}
	}
	sshFlows(s, f, local)
	if f.controller != 0 || listensHere(ctxs, local) {
		panelFlows(s, ctxs, local)
		viaFlows(s, ctxs, in.Retired, local)
		agentLinkFlows(s, ctxs, local)
	}
	controllerSessionFlows(s, f, local)
	agentOutboundFlows(s, f, local)
	tailscaleFlows(s, f)
	return s.flows
}

// localAddrs are the addresses of every interface of the host.
func localAddrs(ifaces []netstat.Interface) map[netip.Addr]bool {
	out := map[netip.Addr]bool{}
	for _, i := range ifaces {
		for _, p := range i.Addrs {
			out[p.Addr().Unmap()] = true
		}
	}
	return out
}

// fromHere reports whether a packet from a reaches this host only through
// the loopback interface, which no flow is checked on.
func fromHere(a netip.Addr, local map[netip.Addr]bool) bool {
	a = a.Unmap()
	return a.IsLoopback() || local[a]
}

// listensHere reports whether a guard context names a listener on one of
// this host's addresses, so the controller runs here.
func listensHere(ctxs []*core.GuardContext, local map[netip.Addr]bool) bool {
	for _, g := range ctxs {
		for _, l := range slices.Concat(g.PanelListen, g.AgentListen) {
			if local[l.Addr().Unmap()] {
				return true
			}
		}
	}
	return false
}

// newPacket is a new TCP or UDP connection from src to dst on path.
func newPacket(path string, src netip.Addr, sport uint16, dst netip.Addr, dport uint16, proto string) fwstate.Packet {
	src, dst = src.Unmap(), dst.Unmap()
	fam := fwstate.V6
	if src.Is4() {
		fam = fwstate.V4
	}
	return fwstate.Packet{Family: fam, Path: path, Src: src, SrcPort: sport, Dst: dst, DstPort: dport, Proto: proto, CtState: "NEW"}
}

// listenFor is the destination a client at src reaches a listener on l
// through, or false when the listener does not take src's family or is an
// address of another host. An unspecified IPv6 listener is dual-stack, as
// Go's listeners are; the unspecified destination stays for routing to
// resolve.
func listenFor(src, l netip.Addr, local map[netip.Addr]bool) (netip.Addr, bool) {
	src, l = src.Unmap(), l.Unmap()
	switch {
	case !l.IsUnspecified() && !local[l]:
		return netip.Addr{}, false
	case l.IsUnspecified() && l.Is6() && src.Is4():
		return netip.IPv4Unspecified(), true
	case l.Is4() == src.Is4():
		return l, true
	}
	return netip.Addr{}, false
}

// heldBy reports whether a process named one of comms, or with pid, holds s.
func heldBy(s netstat.Socket, pid int, comms ...string) bool {
	for _, p := range s.Processes {
		if (pid != 0 && p.PID == pid) || slices.Contains(comms, p.Comm) {
			return true
		}
	}
	return false
}

// inbound reports whether the established TCP socket s was accepted on a
// listening socket of this host, whichever process holds that listener
// (systemd holds sshd's under socket activation).
func inbound(socks []netstat.Socket, s netstat.Socket) bool {
	for _, l := range socks {
		if l.Proto == netstat.ProtoTCP && l.State == netstat.StateListen && l.Local.Port() == s.Local.Port() &&
			(l.Local.Addr().IsUnspecified() || l.Local.Addr().Unmap() == s.Local.Addr().Unmap()) {
			return true
		}
	}
	return false
}

// established lists the established TCP sockets of the table.
func established(socks []netstat.Socket) []netstat.Socket {
	var out []netstat.Socket
	for _, s := range socks {
		if s.Proto == netstat.ProtoTCP && s.State == netstat.StateEstablished {
			out = append(out, s)
		}
	}
	return out
}

// sshFlows is flow 1: a new connection from each SSH session's peer to its
// local address and port.
func sshFlows(s *flowSet, f facts, local map[netip.Addr]bool) {
	for _, so := range established(f.sockets) {
		if !heldBy(so, 0, sshComms...) || !inbound(f.sockets, so) || fromHere(so.Remote.Addr(), local) {
			continue
		}
		s.add(flow{label: "ssh", pkt: newPacket("input", so.Remote.Addr(), unknownPort, so.Local.Addr(), so.Local.Port(), "tcp")})
	}
}

// panelFlows is flow 2: each client that reached the controller directly,
// to every panel listener.
func panelFlows(s *flowSet, ctxs []*core.GuardContext, local map[netip.Addr]bool) {
	var listen []netip.AddrPort
	for _, g := range ctxs {
		listen = append(listen, g.PanelListen...)
	}
	for _, g := range ctxs {
		if g.Via != nil || g.Client == nil || !g.Client.Addr.IsValid() || fromHere(g.Client.Addr, local) {
			continue
		}
		for _, l := range listen {
			if dst, ok := listenFor(g.Client.Addr, l.Addr(), local); ok {
				s.add(flow{label: "panel", pkt: newPacket("input", g.Client.Addr, unknownPort, dst, l.Port(), "tcp")})
			}
		}
	}
}

// viaFlows is flow 3: the exposure a request came through, unless a
// composite has retired it. A public address that is not one of this
// host's arrives through upstream address translation (a cloud's 1:1 NAT),
// so its packets reach the host's own address and the destination is left
// unspecified for routing to resolve.
func viaFlows(s *flowSet, ctxs []*core.GuardContext, retired []string, local map[netip.Addr]bool) {
	for _, g := range ctxs {
		v := g.Via
		if v == nil || slices.Contains(retired, v.ExposureID) {
			continue
		}
		label := "exposure " + v.ExposureID
		if !v.ProxyAddr.IsValid() || !v.ListenerAddr.IsValid() {
			s.add(flow{label: label, unknown: "the exposure has no proxy or listener address"})
			continue
		}
		proxy := newPacket("input", v.ProxyAddr, unknownPort, v.ListenerAddr.Addr(), v.ListenerAddr.Port(), "tcp")
		proxy.InIface = panelBridge
		s.add(flow{label: label + " proxy", pkt: proxy})
		switch v.Kind {
		case "npm":
			for _, pub := range v.Public {
				dst := pub.Addr().Unmap()
				switch {
				case dst.IsUnspecified() || local[dst]:
				case dst.Is4():
					dst = netip.IPv4Unspecified()
				default:
					dst = netip.IPv6Unspecified()
				}
				s.add(flow{label: label + " public", pkt: newPacket("input", internetFor(pub.Addr()), unknownPort, dst, pub.Port(), "tcp")})
			}
		case "tunnel":
			for _, out := range []struct {
				proto string
				port  uint16
			}{{"udp", 7844}, {"tcp", 7844}, {"tcp", 443}} {
				p := newPacket("forward", v.ProxyAddr, unknownPort, internetFor(v.ProxyAddr), out.port, out.proto)
				p.InIface = panelBridge
				s.add(flow{label: label + " tunnel", pkt: p, standIn: true})
			}
		default:
			s.add(flow{label: label, unknown: "the exposure kind " + v.Kind + " is not npm or tunnel"})
		}
	}
}

// internetFor is the stand-in internet address of a's family.
func internetFor(a netip.Addr) netip.Addr {
	if a.Unmap().Is4() {
		return internet4
	}
	return internet6
}

// agentLinkFlows is flow 4 from the guard context: every live agent's
// address to every agent listener.
func agentLinkFlows(s *flowSet, ctxs []*core.GuardContext, local map[netip.Addr]bool) {
	var peers []netip.Addr
	var listen []netip.AddrPort
	for _, g := range ctxs {
		peers = append(peers, g.AgentPeers...)
		listen = append(listen, g.AgentListen...)
	}
	for _, p := range peers {
		if !p.IsValid() || fromHere(p, local) {
			continue
		}
		for _, l := range listen {
			if dst, ok := listenFor(p, l.Addr(), local); ok {
				s.add(flow{label: "agent link", pkt: newPacket("input", p, unknownPort, dst, l.Port(), "tcp")})
			}
		}
	}
}

// controllerSessionFlows is flow 4 from live facts: each established
// session a local controller accepted, from its peer to its local address
// and port. Without a guard context these are the panel and agent links a
// --local change must keep.
func controllerSessionFlows(s *flowSet, f facts, local map[netip.Addr]bool) {
	if f.controller == 0 {
		return
	}
	for _, so := range established(f.sockets) {
		if !heldBy(so, f.controller) || !inbound(f.sockets, so) || fromHere(so.Remote.Addr(), local) {
			continue
		}
		s.add(flow{label: "controller session", pkt: newPacket("input", so.Remote.Addr(), unknownPort, so.Local.Addr(), so.Local.Port(), "tcp")})
	}
}

// agentOutboundFlows is flow 5: every outbound TCP session of the agent
// itself, which includes its link to the controller.
func agentOutboundFlows(s *flowSet, f facts, local map[netip.Addr]bool) {
	for _, so := range established(f.sockets) {
		if !heldBy(so, f.pid) || inbound(f.sockets, so) || fromHere(so.Remote.Addr(), local) {
			continue
		}
		s.add(flow{label: "controller link", pkt: newPacket("output", so.Local.Addr(), so.Local.Port(), so.Remote.Addr(), so.Remote.Port(), "tcp")})
	}
}

// tailscaleFlows is flow 6: when tailscaled runs, its WireGuard UDP port to
// any destination and TCP 443 to any destination (DERP and the control
// plane). The source address is left for routing to choose.
func tailscaleFlows(s *flowSet, f facts) {
	running := false
	var ports []uint16
	for _, so := range f.sockets {
		if !heldBy(so, 0, tailscaled) {
			continue
		}
		running = true
		if so.Proto == netstat.ProtoUDP && so.Listening() && so.Local.Port() != 0 && !so.Local.Addr().IsLoopback() && !slices.Contains(ports, so.Local.Port()) {
			ports = append(ports, so.Local.Port())
		}
	}
	if !running {
		return
	}
	slices.Sort(ports)
	for _, src := range []netip.Addr{netip.IPv4Unspecified(), netip.IPv6Unspecified()} {
		dst := internetFor(src)
		for _, port := range ports {
			p := newPacket("output", src, port, dst, 41641, "udp")
			p.Mark = tailscaleMark
			s.add(flow{label: "tailscale", pkt: p, standIn: true})
		}
		p := newPacket("output", src, unknownPort, dst, 443, "tcp")
		p.Mark = tailscaleMark
		s.add(flow{label: "tailscale", pkt: p, standIn: true})
	}
}

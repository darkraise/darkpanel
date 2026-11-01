package tailscale

import (
	"context"
	"errors"
	"fmt"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/tailscale/link"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
	"github.com/darkraise/darkpanel/internal/platform/tslocal"
)

var (
	// linkInternet4 and linkInternet6 are documentation addresses that stand for any internet address.
	linkInternet4 = netip.MustParseAddr("198.51.100.1")
	linkInternet6 = netip.MustParseAddr("2001:db8:ffff::1")
	// linkTailnet4 and linkTailnet6 stand for any tailnet peer.
	linkTailnet4 = netip.MustParseAddr("100.100.100.100")
	linkTailnet6 = netip.MustParseAddr("fd7a:115c:a1e0::1")
)

// linkSSHComms are the processes that hold an OpenSSH session's socket: sshd before 9.8, sshd-session from 9.8,
// and sshd-auth while it authenticates from 10.0. A process can set its own name, so a name match can only add a
// protected flow, never remove one.
var linkSSHComms = []string{"sshd", "sshd-session", "sshd-auth"}

// linkFacts are the host facts the link guard reads before it builds flows.
type linkFacts struct {
	sockets       []netstat.Socket
	host          map[netip.Addr]bool // every address of every interface, unmapped
	agentPID      int
	controllerPID int // 0 when no controller runs on this host
}

// linkRaw is a protected flow before its route is looked up.
type linkRaw struct {
	label    string
	inbound  bool
	local    netip.Addr
	remote   netip.Addr
	request  bool
	unknown  string
	tailnet  bool // a listener bound to a Tailscale address, reached only through link.Interface
	internet bool // remote is a stand-in internet address
}

// currentLinkPrefs reads the node's preferences as link.Prefs.
func (t ts) currentLinkPrefs(ctx context.Context) (link.Prefs, *tslocal.Prefs, error) {
	p, err := t.d.local().Prefs(ctx)
	if err != nil {
		return link.Prefs{}, nil, fmt.Errorf("tailscale: read the preferences: %w", err)
	}
	lp := link.Prefs{
		WantRunning: p.WantRunning, AcceptRoutes: p.RouteAll, ShieldsUp: p.ShieldsUp, RunSSH: p.RunSSH,
		ExitNode: p.ExitNodeID,
	}
	if lp.ExitNode == "" && p.ExitNodeIP.IsValid() {
		lp.ExitNode = p.ExitNodeIP.String()
	}
	for _, r := range p.AdvertiseRoutes {
		if r.Bits() == 0 {
			lp.AdvertiseExitNode = true
			continue
		}
		lp.AdvertiseRoutes = append(lp.AdvertiseRoutes, r)
	}
	return lp, p, nil
}

// linkGuard gathers this host's protected flows (spec 2.3: SSH sessions, the agent's own session to the
// controller, a local controller's sessions and listeners, and the guard context's panel client, agent peers and
// listeners) through t.d.link and env.Units' controllerUnit state, classifies each with a route lookup, marks the
// request flows, and returns link.Check's guard for c, honouring in.Overrides (an overridden failing guard reports
// OK true, Overridden true). in.Guard is the plan's guard context; env.Call.GuardRefresh is added at apply.
func (t ts) linkGuard(ctx context.Context, env core.Env, in core.Guarded, cur link.Prefs, c link.Change) (core.Guard, error) {
	facts, err := t.linkReadFacts(ctx, env)
	if err != nil {
		return core.Guard{}, err
	}
	flows, err := linkClassify(t.d.link, linkDedupe(linkRawFlows(facts, in.Guard, env.Call.GuardRefresh)))
	if err != nil {
		return core.Guard{}, err
	}
	g := link.Check(cur, c, flows)
	if !g.OK && slices.Contains(in.Overrides, link.GuardID) {
		g.OK, g.Overridden = true, true
	}
	return g, nil
}

// linkReadFacts reads the socket table, the host's addresses, the agent's process id and the local controller's
// process id. A guard must never pass on facts it could not read, so every failure is returned.
func (t ts) linkReadFacts(ctx context.Context, env core.Env) (linkFacts, error) {
	if env.Units == nil {
		return linkFacts{}, errors.New("tailscale: the link guard needs the unit client")
	}
	socks, err := t.d.link.Sockets()
	if err != nil {
		return linkFacts{}, fmt.Errorf("tailscale: read the socket table: %w", err)
	}
	ifaces, err := t.d.link.Interfaces()
	if err != nil {
		return linkFacts{}, fmt.Errorf("tailscale: read the interfaces: %w", err)
	}
	host := map[netip.Addr]bool{}
	for _, ifc := range ifaces {
		for _, p := range ifc.Addrs {
			host[p.Addr().Unmap()] = true
		}
	}
	st, err := env.Units.State(ctx, controllerUnit)
	if err != nil {
		return linkFacts{}, fmt.Errorf("tailscale: read the state of %s: %w", controllerUnit, err)
	}
	f := linkFacts{sockets: socks, host: host, agentPID: t.d.link.PID()}
	if st.LoadState != "not-found" && st.ActiveState != "inactive" {
		f.controllerPID = int(st.MainPID)
	}
	return f, nil
}

// linkRawFlows lists the protected flows of f and the guard contexts plan and refresh (either may be nil), before
// routing. A flow whose remote address is loopback or one of the host's own never leaves the host and is left out.
func linkRawFlows(f linkFacts, plan, refresh *core.GuardContext) []linkRaw {
	var ctxs []*core.GuardContext
	for _, g := range []*core.GuardContext{plan, refresh} {
		if g != nil {
			ctxs = append(ctxs, g)
		}
	}
	request := plan
	if request == nil {
		request = refresh
	}
	here := f.controllerPID != 0
	for _, g := range ctxs {
		for _, l := range slices.Concat(g.PanelListen, g.AgentListen) {
			if f.host[l.Addr().Unmap()] {
				here = true
			}
		}
	}

	var raw []linkRaw
	add := func(r linkRaw) {
		if r.remote.IsValid() && (r.remote.IsLoopback() || f.host[r.remote]) {
			return
		}
		raw = append(raw, r)
	}
	established := func(s netstat.Socket) bool {
		return s.Proto == netstat.ProtoTCP && s.State == netstat.StateEstablished
	}
	addrs := func(s netstat.Socket) (local, remote netip.Addr) {
		return s.Local.Addr().Unmap(), s.Remote.Addr().Unmap()
	}

	for _, g := range ctxs {
		if !g.Complete {
			add(linkRaw{label: "controller link", unknown: "the controller's topology is incomplete"})
		}
	}

	for _, s := range f.sockets {
		if established(s) && linkInbound(f.sockets, s) && linkHeldBy(s, 0, linkSSHComms...) {
			local, remote := addrs(s)
			add(linkRaw{label: "ssh", inbound: true, local: local, remote: remote, request: request == nil})
		}
	}

	if here {
		for _, g := range ctxs {
			isRequest := g == request
			switch {
			case g.Via != nil:
				// The context's Client is then the proxy's address, so it adds no flow of its own.
				if g.Via.ListenerAddr.Addr().IsValid() && g.Via.ProxyAddr.IsValid() {
					add(linkRaw{label: "panel", inbound: true, local: g.Via.ListenerAddr.Addr().Unmap(),
						remote: g.Via.ProxyAddr.Unmap(), request: isRequest})
				}
			case g.Client != nil && g.Client.Addr.IsValid():
				client := g.Client.Addr.Unmap()
				for _, l := range g.PanelListen {
					if dst, ok := linkListenFor(client, l.Addr(), f.host); ok {
						add(linkRaw{label: "panel", inbound: true, local: dst, remote: client, request: isRequest})
					}
				}
			}
			for _, peer := range g.AgentPeers {
				peer = peer.Unmap()
				for _, l := range g.AgentListen {
					if dst, ok := linkListenFor(peer, l.Addr(), f.host); ok {
						add(linkRaw{label: "agent peer", inbound: true, local: dst, remote: peer})
					}
				}
			}
		}
	}

	if f.controllerPID != 0 {
		for _, s := range f.sockets {
			if established(s) && linkHeldBy(s, f.controllerPID) && linkInbound(f.sockets, s) {
				local, remote := addrs(s)
				add(linkRaw{label: "controller session", inbound: true, local: local, remote: remote})
			}
		}
		for _, s := range f.sockets {
			if s.Proto != netstat.ProtoTCP || s.State != netstat.StateListen || !linkHeldBy(s, f.controllerPID) {
				continue
			}
			l := s.Local.Addr().Unmap()
			if l.IsLoopback() {
				continue
			}
			if link.IsTailscaleAddr(l) {
				add(linkRaw{label: "controller link", inbound: true, local: l, remote: linkTailnetOf(l), tailnet: true})
				continue
			}
			for _, remote := range []netip.Addr{linkInternet4, linkInternet6} {
				if dst, ok := linkListenFor(remote, l, f.host); ok {
					add(linkRaw{label: "controller link", inbound: true, local: dst, remote: remote, internet: true})
				}
			}
		}
	}

	// With the controller elsewhere, the request arrives over the agent's own session.
	agentRequest := request != nil && !here
	for _, s := range f.sockets {
		if established(s) && linkHeldBy(s, f.agentPID) && !linkInbound(f.sockets, s) {
			local, remote := addrs(s)
			add(linkRaw{label: "agent link", local: local, remote: remote, request: agentRequest})
		}
	}
	return raw
}

// linkDedupe merges flows with the same direction, local and remote address: the first label wins and the request
// mark is the OR of both. Unknown flows are merged by label and reason.
func linkDedupe(raw []linkRaw) []linkRaw {
	type flowKey struct {
		inbound       bool
		local, remote netip.Addr
	}
	type unknownKey struct{ label, reason string }
	flows := map[flowKey]int{}
	unknowns := map[unknownKey]bool{}
	out := make([]linkRaw, 0, len(raw))
	for _, r := range raw {
		if r.unknown != "" {
			k := unknownKey{r.label, r.unknown}
			if !unknowns[k] {
				unknowns[k] = true
				out = append(out, r)
			}
			continue
		}
		k := flowKey{r.inbound, r.local, r.remote}
		if i, ok := flows[k]; ok {
			out[i].request = out[i].request || r.request
			continue
		}
		flows[k] = len(out)
		out = append(out, r)
	}
	return out
}

// linkClassify turns raw flows into link.Flows: each takes the interface the kernel routes its remote address
// through, and Default when that interface is link.Interface and the internet at large leaves through it too. The
// lookup answers a host route, so a default route is recognized by probing the stand-in internet address from the
// same local address. Exit-node traffic is then Default and a subnet route is not; a host with both is Default,
// which can only add a failure the operator may override.
func linkClassify(host linkHost, raw []linkRaw) ([]link.Flow, error) {
	routes := linkRoutes{host: host, seen: map[[2]netip.Addr]linkRoute{}}
	flows := make([]link.Flow, 0, len(raw))
	for _, r := range raw {
		f := link.Flow{Label: r.label, Inbound: r.inbound, Local: r.local, Remote: r.remote, Request: r.request, Unknown: r.unknown}
		switch {
		case r.unknown != "":
		case r.tailnet:
			f.OutIface = link.Interface
		default:
			route, err := routes.get(r.local, r.remote)
			switch {
			case errors.Is(err, netstat.ErrNoRoute) && r.internet:
				// A host without a route to the internet has nothing to protect there.
				continue
			case errors.Is(err, netstat.ErrNoRoute):
				f.Unknown = fmt.Sprintf("no route to %s", r.remote)
			case err != nil:
				return nil, fmt.Errorf("tailscale: look up the route to %s: %w", r.remote, err)
			case route.OutIface == "":
				f.Unknown = fmt.Sprintf("the route to %s has no single output interface", r.remote)
			default:
				f.OutIface = route.OutIface
				if f.OutIface == link.Interface && !link.IsTailscaleAddr(r.remote) {
					probe, err := routes.get(r.local, linkInternetOf(r.remote))
					switch {
					case errors.Is(err, netstat.ErrNoRoute):
					case err != nil:
						return nil, fmt.Errorf("tailscale: look up the default route from %s: %w", r.local, err)
					default:
						f.Default = probe.OutIface == link.Interface
					}
				}
			}
		}
		flows = append(flows, f)
	}
	return flows, nil
}

// linkRoute is the answer to one route lookup.
type linkRoute struct {
	route netstat.Route
	err   error
}

// linkRoutes looks up routes, each (source, destination) pair once per guard evaluation: a default-route probe
// from one local address is shared by every flow from it, and a stand-in internet flow is its own probe.
type linkRoutes struct {
	host linkHost
	seen map[[2]netip.Addr]linkRoute
}

// get answers the route from local to remote; the query carries a source only when local is specified.
func (r linkRoutes) get(local, remote netip.Addr) (netstat.Route, error) {
	var src netip.Addr
	if !local.IsUnspecified() {
		src = local
	}
	k := [2]netip.Addr{src, remote}
	if a, ok := r.seen[k]; ok {
		return a.route, a.err
	}
	route, err := r.host.RouteGet(netstat.RouteQuery{Dst: remote, Src: src})
	r.seen[k] = linkRoute{route: route, err: err}
	return route, err
}

// linkListenFor is the local address at which a listener on l takes a client from client, and whether it takes
// it at all: a listener on a specified address that is not one of host's takes nothing; an unspecified IPv6
// listener is dual-stack, taking an IPv4 client at 0.0.0.0 and an IPv6 client at ::; any other listener takes a
// client of its own family at its own address.
func linkListenFor(client, l netip.Addr, host map[netip.Addr]bool) (netip.Addr, bool) {
	client, l = client.Unmap(), l.Unmap()
	switch {
	case !client.IsValid():
		return netip.Addr{}, false
	case !l.IsUnspecified() && !host[l]:
		return netip.Addr{}, false
	case l.IsUnspecified() && l.Is6() && client.Is4():
		return netip.IPv4Unspecified(), true
	case l.Is4() == client.Is4():
		return l, true
	}
	return netip.Addr{}, false
}

// linkHeldBy reports whether the process with pid (when not 0) or a process named one of comms holds s.
func linkHeldBy(s netstat.Socket, pid int, comms ...string) bool {
	for _, p := range s.Processes {
		if (pid != 0 && p.PID == pid) || slices.Contains(comms, p.Comm) {
			return true
		}
	}
	return false
}

// linkInbound reports whether the established TCP socket s was accepted on a listening socket of this host,
// whichever process holds that listener (systemd holds sshd's under socket activation).
func linkInbound(socks []netstat.Socket, s netstat.Socket) bool {
	for _, l := range socks {
		if l.Proto == netstat.ProtoTCP && l.State == netstat.StateListen && l.Local.Port() == s.Local.Port() &&
			(l.Local.Addr().IsUnspecified() || l.Local.Addr().Unmap() == s.Local.Addr().Unmap()) {
			return true
		}
	}
	return false
}

// linkInternetOf is the stand-in internet address of a's family.
func linkInternetOf(a netip.Addr) netip.Addr {
	if a.Unmap().Is4() {
		return linkInternet4
	}
	return linkInternet6
}

// linkTailnetOf is the stand-in tailnet peer of a's family.
func linkTailnetOf(a netip.Addr) netip.Addr {
	if a.Unmap().Is4() {
		return linkTailnet4
	}
	return linkTailnet6
}

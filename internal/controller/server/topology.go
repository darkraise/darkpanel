package server

import (
	"net/netip"
	"sync"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// topology is the dispatcher's Topology. The listeners start after the
// dispatcher exists, and startup recovery may already call the dispatcher, so
// each source is registered under mu once it exists; until then it adds
// nothing and the topology reads as incomplete. Once the server shuts down
// it reads as incomplete again, because the listeners are going away.
type topology struct {
	mu      sync.Mutex
	closed  bool
	web     func() []listen.Bound
	runtime func() ([]netip.AddrPort, []string, int)
	agent   func() []listen.Bound
	peers   func() ([]netip.Addr, []string)
}

// close marks the topology incomplete for good.
func (t *topology) close() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.closed = true
}

func (t *topology) setWeb(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.web = f
}

// setRuntime registers the runtime listeners' source: their bound addresses,
// the addresses it could not read, and the number of open listeners that are
// not bound yet. The source is registered once the listeners have started,
// because before that it cannot tell which listeners are open.
func (t *topology) setRuntime(f func() ([]netip.AddrPort, []string, int)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runtime = f
}

func (t *topology) setAgent(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agent = f
}

func (t *topology) setPeers(f func() ([]netip.Addr, []string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = f
}

// setNoAgents registers empty agent listeners and peers, for a controller
// whose agent runs embedded: it serves no agent listener and has no agent
// sessions, so their lists are complete when empty.
func (t *topology) setNoAgents() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agent = func() []listen.Bound { return nil }
	t.peers = func() ([]netip.Addr, []string) { return nil, nil }
}

// read is the topology now. It is complete once all four sources have
// registered, no open runtime listener waits for a bind and the server has
// not begun to shut down; what the runtime listeners and the session
// registry could not read is dropped from it after what the bound listeners
// drop.
func (t *topology) read() dispatch.Topology {
	t.mu.Lock()
	closed, web, runtime, agent, peers := t.closed, t.web, t.runtime, t.agent, t.peers
	t.mu.Unlock()
	var webBound, agentBound []listen.Bound
	var runtimeAddrs []netip.AddrPort
	var agentPeers []netip.Addr
	var runtimeDropped, peersDropped []string
	var runtimeUnbound int
	if web != nil {
		webBound = web()
	}
	if runtime != nil {
		runtimeAddrs, runtimeDropped, runtimeUnbound = runtime()
	}
	if agent != nil {
		agentBound = agent()
	}
	if peers != nil {
		agentPeers, peersDropped = peers()
	}
	out := topologyOf(webBound, runtimeAddrs, agentBound, agentPeers)
	out.Dropped = append(append(out.Dropped, runtimeDropped...), peersDropped...)
	out.Complete = !closed && web != nil && runtime != nil && agent != nil && peers != nil && runtimeUnbound == 0
	return out
}

// topologyOf is the topology of the bound web listeners, the runtime
// listeners, the bound agent listeners and the live agents' addresses. The
// panel is reached through the web and the runtime listeners alike (spec
// 1.5). A listener without an IP address, such as a Unix socket, is left
// out; any other address that cannot be read is listed in Dropped.
func topologyOf(web []listen.Bound, runtime []netip.AddrPort, agent []listen.Bound, peers []netip.Addr) dispatch.Topology {
	panel, dropped := boundAddrs(web)
	for _, ap := range runtime {
		panel = append(panel, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	agentAddrs, agentDropped := boundAddrs(agent)
	return dispatch.Topology{PanelListen: panel, AgentListen: agentAddrs, AgentPeers: peers,
		Dropped: append(dropped, agentDropped...)}
}

// boundAddrs returns the IP addresses of bound and, as dropped, every
// address that is not one. A Unix socket is neither: no firewall rule can
// cut it, so it is left out without being dropped.
func boundAddrs(bound []listen.Bound) (out []netip.AddrPort, dropped []string) {
	for _, b := range bound {
		if b.Addr.Network() == "unix" {
			continue
		}
		ap, err := netip.ParseAddrPort(b.Addr.String())
		if err != nil {
			dropped = append(dropped, b.Addr.String())
			continue
		}
		out = append(out, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	return out, dropped
}

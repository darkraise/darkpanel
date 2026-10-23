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
// nothing.
type topology struct {
	mu      sync.Mutex
	web     func() []listen.Bound
	runtime func() []netip.AddrPort
	agent   func() []listen.Bound
	peers   func() []netip.Addr
}

func (t *topology) setWeb(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.web = f
}

func (t *topology) setRuntime(f func() []netip.AddrPort) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.runtime = f
}

func (t *topology) setAgent(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.agent = f
}

func (t *topology) setPeers(f func() []netip.Addr) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.peers = f
}

// read is the topology now.
func (t *topology) read() dispatch.Topology {
	t.mu.Lock()
	web, runtime, agent, peers := t.web, t.runtime, t.agent, t.peers
	t.mu.Unlock()
	var webBound, agentBound []listen.Bound
	var runtimeAddrs []netip.AddrPort
	var agentPeers []netip.Addr
	if web != nil {
		webBound = web()
	}
	if runtime != nil {
		runtimeAddrs = runtime()
	}
	if agent != nil {
		agentBound = agent()
	}
	if peers != nil {
		agentPeers = peers()
	}
	return topologyOf(webBound, runtimeAddrs, agentBound, agentPeers)
}

// topologyOf is the topology of the bound web listeners, the runtime
// listeners, the bound agent listeners and the live agents' addresses. The
// panel is reached through the web and the runtime listeners alike (spec
// 1.5). A listener without an IP address, such as a Unix socket, is left out.
func topologyOf(web []listen.Bound, runtime []netip.AddrPort, agent []listen.Bound, peers []netip.Addr) dispatch.Topology {
	panel := boundAddrs(web)
	for _, ap := range runtime {
		panel = append(panel, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
	}
	return dispatch.Topology{PanelListen: panel, AgentListen: boundAddrs(agent), AgentPeers: peers}
}

func boundAddrs(bound []listen.Bound) []netip.AddrPort {
	var out []netip.AddrPort
	for _, b := range bound {
		if ap, err := netip.ParseAddrPort(b.Addr.String()); err == nil {
			out = append(out, netip.AddrPortFrom(ap.Addr().Unmap(), ap.Port()))
		}
	}
	return out
}

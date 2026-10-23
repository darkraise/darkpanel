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
	mu    sync.Mutex
	web   func() []listen.Bound
	agent func() []listen.Bound
	peers func() []netip.Addr
}

func (t *topology) setWeb(f func() []listen.Bound) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.web = f
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
	web, agent, peers := t.web, t.agent, t.peers
	t.mu.Unlock()
	var webBound, agentBound []listen.Bound
	var agentPeers []netip.Addr
	if web != nil {
		webBound = web()
	}
	if agent != nil {
		agentBound = agent()
	}
	if peers != nil {
		agentPeers = peers()
	}
	return topologyOf(webBound, agentBound, agentPeers)
}

// topologyOf is the topology of the bound web and agent listeners and the
// live agents' addresses. A listener without an IP address, such as a Unix
// socket, is left out.
func topologyOf(web, agent []listen.Bound, peers []netip.Addr) dispatch.Topology {
	return dispatch.Topology{PanelListen: boundAddrs(web), AgentListen: boundAddrs(agent), AgentPeers: peers}
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

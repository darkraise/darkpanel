package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// Topology is the controller's side of a guard context (spec 1.5): its web
// and agent listeners and its live agents' addresses.
type Topology struct {
	PanelListen []netip.AddrPort
	AgentListen []netip.AddrPort
	AgentPeers  []netip.Addr
}

// guardContext is the guard context of a call now: the client peer the API
// stored in ctx, the exposure path of a request that came through a runtime
// listener, and the configured topology. A composite's children run on the
// engine's context and so carry neither. Every address loses its
// IPv6 zone: a zone names an interface of the controller, so it means nothing
// on the agent's host and two addresses that differ only by zone must compare
// equal. The topology's slices are copied, never changed.
func (d *Dispatcher) guardContext(ctx context.Context) core.GuardContext {
	var g core.GuardContext
	if d.cfg.Topology != nil {
		t := d.cfg.Topology()
		g.PanelListen = withoutPortZones(t.PanelListen)
		g.AgentListen = withoutPortZones(t.AgentListen)
		g.AgentPeers = withoutZones(t.AgentPeers)
	}
	if p, ok := core.ClientPeer(ctx); ok {
		p.Addr = p.Addr.WithZone("")
		g.Client = &p
	}
	if px, ok := core.ProxiedFrom(ctx); ok {
		path := px.Path
		path.ProxyAddr = path.ProxyAddr.WithZone("")
		path.ListenerAddr = netip.AddrPortFrom(path.ListenerAddr.Addr().WithZone(""), path.ListenerAddr.Port())
		path.Public = withoutPortZones(path.Public)
		g.Via = &path
	}
	return g
}

func withoutZones(addrs []netip.Addr) []netip.Addr {
	if addrs == nil {
		return nil
	}
	out := make([]netip.Addr, len(addrs))
	for i, a := range addrs {
		out[i] = a.WithZone("")
	}
	return out
}

func withoutPortZones(addrs []netip.AddrPort) []netip.AddrPort {
	if addrs == nil {
		return nil
	}
	out := make([]netip.AddrPort, len(addrs))
	for i, a := range addrs {
		out[i] = netip.AddrPortFrom(a.Addr().WithZone(""), a.Port())
	}
	return out
}

// withGuard returns input with its top-level guard member set to g. Members
// whose name matches guard ignoring case are dropped first, because
// encoding/json would decode any of them as the guard. An input that is not
// a JSON object is returned as it is, for the validator to refuse.
func withGuard(input json.RawMessage, g core.GuardContext) (json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(input, &obj); err != nil || obj == nil {
		return input, nil
	}
	for key := range obj {
		if strings.EqualFold(key, "guard") {
			delete(obj, key)
		}
	}
	raw, err := json.Marshal(g)
	if err != nil {
		return nil, fmt.Errorf("dispatch: encode the guard context: %w", err)
	}
	obj["guard"] = raw
	out, err := json.Marshal(obj)
	if err != nil {
		return nil, fmt.Errorf("dispatch: encode the input: %w", err)
	}
	return out, nil
}

// withGuardRefresh returns opts whose call context carries the guard context
// now, on a copy, so the caller's call context is never changed.
func (d *Dispatcher) withGuardRefresh(ctx context.Context, opts core.ApplyOptions) core.ApplyOptions {
	var call core.CallContext
	if opts.Call != nil {
		call = *opts.Call
	}
	g := d.guardContext(ctx)
	call.GuardRefresh = &g
	opts.Call = &call
	return opts
}

package core

import (
	"context"
	"net/netip"
)

type clientPeerKey struct{}

// WithClientPeer returns ctx carrying the address the controller's transport
// saw a request come from, which the dispatcher puts in the guard context
// (spec 1.5).
func WithClientPeer(ctx context.Context, p Peer) context.Context {
	return context.WithValue(ctx, clientPeerKey{}, p)
}

// ClientPeer returns the peer WithClientPeer stored in ctx.
func ClientPeer(ctx context.Context) (Peer, bool) {
	p, ok := ctx.Value(clientPeerKey{}).(Peer)
	return p, ok
}

// Proxied describes a request that reached the controller through a
// runtime listener (1.10). Path.ExposureID is the listener's
// ListenerSpec.ChangeID, Path.Kind is "npm" for ClientHeaderNPM and "tunnel"
// for ClientHeaderTunnel, Path.ProxyAddr is the allowed peer and
// Path.ListenerAddr the listener's address; Path.Public is left empty.
type Proxied struct {
	ListenerID string
	Client     netip.Addr     // the address the listener's client header named, unmapped
	Transport  netip.AddrPort // the proxy's TCP peer
	PublicHost string         // the exposure's public host name
	Path       ProxyPath
	Revoked    <-chan struct{} // closed once the listener is revoked
}

type proxiedKey struct{}

// WithProxied returns ctx carrying p.
func WithProxied(ctx context.Context, p Proxied) context.Context {
	return context.WithValue(ctx, proxiedKey{}, p)
}

// ProxiedFrom returns the Proxied WithProxied stored in ctx.
func ProxiedFrom(ctx context.Context) (Proxied, bool) {
	p, ok := ctx.Value(proxiedKey{}).(Proxied)
	return p, ok
}

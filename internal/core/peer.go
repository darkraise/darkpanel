package core

import "context"

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

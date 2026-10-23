package core

import (
	"context"
	"net/netip"
)

// The states of a runtime listener (core_listeners.state).
const (
	ListenerOpen    = "open"
	ListenerRevoked = "revoked"
	ListenerClosed  = "closed"
)

// The client headers a runtime listener may read (1.10).
const (
	ClientHeaderNPM    = "X-Darkpanel-Client"
	ClientHeaderTunnel = "Cf-Connecting-IP"
)

// Listeners opens and closes the controller's runtime listeners (1.10)
// without a restart. Env carries it for controller-side actions only.
type Listeners interface {
	// Open records a core_listeners row in state open, then binds it.
	Open(ctx context.Context, spec ListenerSpec) (id string, err error)
	// Revoke commits state revoked first, then stops accepting and closes
	// tracked connections and streams (1.10). Revoking a revoked listener succeeds.
	Revoke(ctx context.Context, id string) error
	// Close moves a revoked listener to closed.
	Close(ctx context.Context, id string) error
	Get(ctx context.Context, id string) (ListenerInfo, error)
}

// ListenerSpec is what a runtime listener serves and whom it trusts.
type ListenerSpec struct {
	Addr         netip.AddrPort
	AllowedPeer  netip.Addr
	ClientHeader string // "X-Darkpanel-Client" or "Cf-Connecting-IP"
	PublicHost   string // accepted by the CSRF origin check
	ChangeID     string
}

// ListenerInfo is one core_listeners row.
type ListenerInfo struct {
	ID    string
	Spec  ListenerSpec
	State string // open | revoked | closed
}

package core

import "net/netip"

// GuardSpec declares one guard an action's plan may report.
type GuardSpec struct {
	ID          string // "firewall.lockout"; a plan reporting an undeclared id fails with internal
	Overridable bool   // only the lockout guards of the firewall and Tailscale modules
}

// Guarded is embedded in the input type of every action with Guards. The
// controller fills Guard; the API and the agent socket refuse an input that
// carries it.
type Guarded struct {
	Guard     *GuardContext `json:"guard,omitempty"`     // filled by the controller
	Overrides []string      `json:"overrides,omitempty"` // ids of Overridable guards
}

// GuardContext is the controller's topology a guard protects: the flows
// that must survive a change.
type GuardContext struct {
	Client      *Peer            `json:"client,omitempty"`      // the browser or CLI as the controller's transport saw it
	Via         *ProxyPath       `json:"via,omitempty"`         // set when the request came through an exposure
	PanelListen []netip.AddrPort `json:"panelListen,omitempty"` // the controller's web listeners
	AgentListen []netip.AddrPort `json:"agentListen,omitempty"` // the controller's agent listeners
	AgentPeers  []netip.Addr     `json:"agentPeers,omitempty"`  // live agents' source addresses
}

// Peer is one end of a connection.
type Peer struct {
	Addr netip.Addr `json:"addr"`
	Port uint16     `json:"port"`
}

// ProxyPath is the exposure a request came through.
type ProxyPath struct {
	ExposureID   string           `json:"exposureId"`   // the panel exposure; what a retirement names
	Kind         string           `json:"kind"`         // npm | tunnel
	ProxyAddr    netip.Addr       `json:"proxyAddr"`    // the proxy container's address on the panel network
	ListenerAddr netip.AddrPort   `json:"listenerAddr"` // the runtime listener
	Public       []netip.AddrPort `json:"public"`
}

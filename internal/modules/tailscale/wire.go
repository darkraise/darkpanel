package tailscale

import (
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Status is the node's state, the output of the tailscale.status resource.
type Status struct {
	Version        string      `json:"version" jsonschema_description:"The tailscaled version."`
	BackendState   string      `json:"backendState" jsonschema_description:"The backend state: NoState, NeedsLogin, NeedsMachineAuth, Stopped, Starting or Running."`
	Self           *Node       `json:"self" jsonschema:"nullable" jsonschema_description:"This node; null while logged out."`
	Tailnet        string      `json:"tailnet" jsonschema_description:"The tailnet's name; empty when the node belongs to none."`
	MagicDNSSuffix string      `json:"magicDNSSuffix" jsonschema_description:"The tailnet's MagicDNS suffix."`
	PreferredDERP  string      `json:"preferredDerp" jsonschema_description:"The region code of the node's home DERP; empty when none."`
	RunSSH         bool        `json:"runSSH" jsonschema_description:"Whether the node runs Tailscale SSH."`
	Prefs          PrefsFields `json:"prefs" jsonschema_description:"The node's current preferences, every field set."`
	Peers          []Peer      `json:"peers" jsonschema_description:"The tailnet's other nodes, sorted by DNS name, then id."`
	Health         []string    `json:"health" jsonschema_description:"The health warnings tailscaled reports."`
}

// Node is one node of the tailnet.
type Node struct {
	ID        string   `json:"id" jsonschema_description:"The node's stable id."`
	HostName  string   `json:"hostName" jsonschema_description:"The node's host name."`
	DNSName   string   `json:"dnsName" jsonschema_description:"The node's MagicDNS name, without the trailing dot."`
	OS        string   `json:"os" jsonschema_description:"The node's operating system."`
	Addresses []string `json:"addresses" jsonschema_description:"The node's Tailscale addresses, IPv4 first."`
	Tags      []string `json:"tags" jsonschema_description:"The node's ACL tags."`
}

// Peer is another node of the tailnet as this node sees it.
type Peer struct {
	Node
	Online         bool       `json:"online" jsonschema_description:"Whether the peer is connected to the coordination server."`
	Connection     string     `json:"connection" jsonschema_description:"How this node reaches the peer: direct, derp, peer-relay or none."`
	Endpoint       string     `json:"endpoint" jsonschema_description:"The UDP endpoint in use for a direct connection; empty otherwise."`
	Region         string     `json:"region" jsonschema_description:"The DERP region code for a DERP connection; empty otherwise."`
	ExitNode       bool       `json:"exitNode" jsonschema_description:"Whether the peer is this node's exit node."`
	ExitNodeOption bool       `json:"exitNodeOption" jsonschema_description:"Whether the peer offers itself as an exit node."`
	LastHandshake  *time.Time `json:"lastHandshake" jsonschema:"nullable" jsonschema_description:"The last WireGuard handshake with the peer; null when there was none."`
}

// Latency is the node's latency to each DERP region, the output of the tailscale.latency resource.
type Latency struct {
	Regions []RegionLatency `json:"regions" jsonschema_description:"Each DERP region, sorted by region id."`
}

// RegionLatency is the latency to one DERP region.
type RegionLatency struct {
	RegionID int         `json:"regionId" jsonschema_description:"The DERP region id."`
	Code     string      `json:"code" jsonschema_description:"The DERP region code."`
	Name     string      `json:"name" jsonschema_description:"The DERP region name."`
	STUN     Measurement `json:"stun" jsonschema_description:"The STUN latency to the region."`
	HTTPS    Measurement `json:"https" jsonschema_description:"The DERP HTTPS probe latency to the region."`
}

// Measurement is one latency measurement: the median of its probes, or the reason it is unavailable.
type Measurement struct {
	Available bool    `json:"available" jsonschema_description:"Whether a latency was measured."`
	MedianMs  float64 `json:"medianMs" jsonschema_description:"The median round trip in milliseconds; 0 when unavailable."`
	Reason    string  `json:"reason" jsonschema_description:"Why no latency was measured; empty when available."`
}

// PrefsFields is a masked set of the node's preferences: a nil field is not set and stays unchanged.
type PrefsFields struct {
	WantRunning       *bool     `json:"wantRunning,omitempty" jsonschema:"nullable" jsonschema_description:"Whether the node should be connected."`
	AdvertiseRoutes   *[]string `json:"advertiseRoutes,omitempty" jsonschema:"nullable" jsonschema_description:"The subnet routes the node advertises, as prefixes; never 0.0.0.0/0 or ::/0."`
	ExitNode          *string   `json:"exitNode,omitempty" jsonschema:"nullable" jsonschema_description:"The exit node in use, by stable id or Tailscale address; empty clears it."`
	AdvertiseExitNode *bool     `json:"advertiseExitNode,omitempty" jsonschema:"nullable" jsonschema_description:"Whether the node offers itself as an exit node."`
	AcceptRoutes      *bool     `json:"acceptRoutes,omitempty" jsonschema:"nullable" jsonschema_description:"Whether the node accepts the subnet routes other nodes advertise."`
	ShieldsUp         *bool     `json:"shieldsUp,omitempty" jsonschema:"nullable" jsonschema_description:"Whether the node refuses every incoming connection."`
	RunSSH            *bool     `json:"runSSH,omitempty" jsonschema:"nullable" jsonschema_description:"Whether the node runs Tailscale SSH."`
}

// PrefsSetInput is the input of tailscale.prefs.set.
type PrefsSetInput struct {
	core.Guarded
	PrefsFields
}

// PrefsResult is the output of tailscale.prefs.set.
type PrefsResult struct {
	Prefs PrefsFields `json:"prefs" jsonschema_description:"The values now, for exactly the fields the input set."`
}

// PrefsRepairInput is the input of tailscale.prefs.repair.
type PrefsRepairInput struct {
	core.Guarded
	Outcome string `json:"outcome,omitempty" jsonschema_description:"restore (the default) puts back the preferences from before the repaired change; keep applies the values the change set."`
}

// PrefsRepaired is the output of tailscale.prefs.repair.
type PrefsRepaired struct {
	RepairOutcome string      `json:"repairOutcome" jsonschema_description:"What the repair did: restored or kept."`
	Prefs         PrefsFields `json:"prefs" jsonschema_description:"The repaired preferences."`
}

// LoginInput is the input of tailscale.node.login.
type LoginInput struct {
	core.Guarded
	AuthKey       string   `json:"authKey" secret:"true" jsonschema_description:"The Tailscale auth key to log in with."`
	Hostname      string   `json:"hostname,omitempty" jsonschema_description:"The host name the node takes in the tailnet."`
	AdvertiseTags []string `json:"advertiseTags,omitempty" jsonschema_description:"The ACL tags the node advertises, each tag:<name>."`
	Relogin       bool     `json:"relogin,omitempty" jsonschema_description:"Whether to log in again when the node is logged in already."`
}

// LoginResult is the output of tailscale.node.login.
type LoginResult struct {
	BackendState string   `json:"backendState" jsonschema_description:"The backend state after the login."`
	DNSName      string   `json:"dnsName" jsonschema_description:"The node's MagicDNS name, without the trailing dot."`
	Addresses    []string `json:"addresses" jsonschema_description:"The node's Tailscale addresses, IPv4 first."`
}

// LogoutInput is the input of tailscale.node.logout.
type LogoutInput struct{ core.Guarded }

// LogoutResult is the output of tailscale.node.logout.
type LogoutResult struct {
	BackendState string `json:"backendState" jsonschema_description:"The backend state after the logout."`
}

// PackageInstallInput is the input of tailscale.package.install.
type PackageInstallInput struct{}

// PackageInstalled is the output of tailscale.package.install.
type PackageInstalled struct {
	Version string `json:"version" jsonschema_description:"The installed tailscale version."`
}

// PackageUpdateInput is the input of tailscale.package.update.
type PackageUpdateInput struct{}

// PackageUpdated is the output of tailscale.package.update.
type PackageUpdated struct {
	From string `json:"from" jsonschema_description:"The tailscale version before the update."`
	To   string `json:"to" jsonschema_description:"The tailscale version after the update."`
}

// PinInput is the input of tailscale.package.pin.
type PinInput struct {
	Version string `json:"version" jsonschema:"pattern=^[0-9][0-9A-Za-z.+~-]{0\\,63}$" jsonschema_description:"The tailscale version to install and hold."`
}

// Pinned is the output of tailscale.package.pin.
type Pinned struct {
	Version      string `json:"version" jsonschema_description:"The tailscale version now installed and held."`
	PriorVersion string `json:"priorVersion" jsonschema_description:"The tailscale version before the pin."`
	PriorHeld    bool   `json:"priorHeld" jsonschema_description:"Whether the package was held before the pin."`
}

// UnpinInput is the input of tailscale.package.unpin, which reverses the newest pin in phase done.
type UnpinInput struct{}

// Unpinned is the output of tailscale.package.unpin.
type Unpinned struct {
	Version string `json:"version" jsonschema_description:"The installed tailscale version, unchanged."`
	Held    bool   `json:"held" jsonschema_description:"Whether the package is held now: the pin's prior hold state."`
}

// CertGetInput is the input of tailscale.cert.get.
type CertGetInput struct{}

// CertPair is the node's TLS certificate for its MagicDNS name, the output of tailscale.cert.get.
type CertPair struct {
	Name     string    `json:"name" jsonschema_description:"The node's MagicDNS name, without the trailing dot."`
	CertPEM  string    `json:"certPem" jsonschema_description:"The certificate chain, PEM-encoded."`
	KeyPEM   string    `json:"keyPem" secret:"true" jsonschema_description:"The certificate's private key, PEM-encoded."`
	NotAfter time.Time `json:"notAfter" jsonschema_description:"When the certificate expires."`
}

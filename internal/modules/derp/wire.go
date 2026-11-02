package derp

import (
	"time"

	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

// ServerInstallInput is the input of derp.server.install.
type ServerInstallInput struct {
	Mode          string `json:"mode" jsonschema:"enum=letsencrypt,enum=ip" jsonschema_description:"How derper gets its certificate: letsencrypt or ip."`
	HostName      string `json:"hostName" jsonschema:"maxLength=253" jsonschema_description:"The server's DNS name, or its public IP literal in ip mode."`
	VerifyClients *bool  `json:"verifyClients,omitempty" jsonschema:"nullable" jsonschema_description:"Whether derper admits only nodes of this tailnet; absent means true."`
}

// ServerInstalled is the output of derp.server.install.
type ServerInstalled struct {
	DerperVersion string     `json:"derperVersion" jsonschema_description:"The derper version installed."`
	CertName      string     `json:"certName" jsonschema_description:"The certificate pin in ip mode; empty for letsencrypt."`
	CertNotAfter  *time.Time `json:"certNotAfter" jsonschema:"nullable" jsonschema_description:"When the ip certificate expires; null for letsencrypt."`
}

// ServerUninstallInput is the input of derp.server.uninstall.
type ServerUninstallInput struct{}

// ServerUninstalled is the output of derp.server.uninstall.
type ServerUninstalled struct {
	Removed []string `json:"removed" jsonschema_description:"The effect resources removed, in order; empty when nothing was installed."`
}

// BinaryUpdateInput is the input of derp.binary.update.
type BinaryUpdateInput struct {
	Version string `json:"version" jsonschema:"pattern=^v[0-9]+\\.[0-9]+\\.[0-9]+$" jsonschema_description:"The derper version to install."`
}

// BinaryUpdated is the output of derp.binary.update.
type BinaryUpdated struct {
	From string `json:"from" jsonschema_description:"The derper version replaced."`
	To   string `json:"to" jsonschema_description:"The derper version now installed."`
}

// CertRenewInput is the input of derp.cert.renew.
type CertRenewInput struct{}

// CertRenewed is the output of derp.cert.renew.
type CertRenewed struct {
	CertName      string    `json:"certName" jsonschema_description:"The new certificate's pin."`
	CertNotAfter  time.Time `json:"certNotAfter" jsonschema_description:"When the new certificate expires."`
	PriorCertName string    `json:"priorCertName" jsonschema_description:"The replaced certificate's pin."`
}

// ServerStatusInput is the input of derp.server.status.
type ServerStatusInput struct {
	Whois []string `json:"whois,omitempty" jsonschema:"maxItems=256" jsonschema_description:"IP literals to look up on this host's tailscaled."`
}

// ServerStatus is the output of derp.server.status: the DERP server of one host and its node.
type ServerStatus struct {
	Installed        bool         `json:"installed" jsonschema_description:"Whether a DERP server is installed on the host."`
	Mode             string       `json:"mode" jsonschema_description:"The server's certificate mode; empty when not installed."`
	HostName         string       `json:"hostName" jsonschema_description:"The server's DNS name or public IP literal."`
	VerifyClients    bool         `json:"verifyClients" jsonschema_description:"Whether derper admits only nodes of this tailnet."`
	DerperVersion    string       `json:"derperVersion" jsonschema_description:"The derper version installed."`
	UnitActive       bool         `json:"unitActive" jsonschema_description:"Whether derper's unit is active."`
	CertName         string       `json:"certName" jsonschema_description:"The certificate pin in ip mode; empty for letsencrypt."`
	CertNotAfter     *time.Time   `json:"certNotAfter" jsonschema:"nullable" jsonschema_description:"When the ip certificate expires; null for letsencrypt."`
	AvailableVersion string       `json:"availableVersion" jsonschema_description:"The derper version this agent's release carries."`
	LinkViaTailnet   bool         `json:"linkViaTailnet" jsonschema_description:"Whether the agent reaches its controller over a Tailscale address."`
	OnTailnet        bool         `json:"onTailnet" jsonschema_description:"Whether tailscaled answers with BackendState Running."`
	Tags             []string     `json:"tags" jsonschema_description:"The node's tags, sorted; empty when none."`
	Addresses        []string     `json:"addresses" jsonschema_description:"The node's Tailscale addresses, IPv4 first."`
	Visible          []Visibility `json:"visible" jsonschema_description:"One entry per looked-up address, in input order."`
}

// Visibility is whether this host's tailscaled knows the node holding one address.
type Visibility struct {
	Address string `json:"address" jsonschema_description:"The address looked up."`
	Visible bool   `json:"visible" jsonschema_description:"Whether a node holds the address."`
	Node    string `json:"node" jsonschema_description:"The node's MagicDNS name without the trailing dot; empty when not visible."`
}

// NodeProbeInput is the input of derp.node.probe: the DERP server this agent probes.
type NodeProbeInput struct {
	HostName string `json:"hostName" jsonschema:"maxLength=253" jsonschema_description:"The server's DNS name or public IP literal."`
	Address  string `json:"address,omitempty" jsonschema_description:"The address to connect to; absent resolves hostName."`
	CertName string `json:"certName,omitempty" jsonschema_description:"The certificate pin in ip mode; absent verifies the chain."`
	RegionID int    `json:"regionId,omitempty" jsonschema:"minimum=900,maximum=999" jsonschema_description:"The server's region id, 900 to 999; absent skips the region diagnostics."`
}

// NodeProbe is the output of derp.node.probe.
type NodeProbe struct {
	TLS    ProbeResult        `json:"tls" jsonschema_description:"The TLS probe of /derp/probe."`
	STUN   ProbeResult        `json:"stun" jsonschema_description:"The STUN probe."`
	Region *RegionDiagnostics `json:"region" jsonschema:"nullable" jsonschema_description:"The local API's region diagnostics; null when no region was given."`
}

// ProbeResult is one probe's outcome.
type ProbeResult struct {
	OK        bool    `json:"ok" jsonschema_description:"Whether the probe succeeded."`
	LatencyMs float64 `json:"latencyMs" jsonschema_description:"The round trip in milliseconds; 0 when not OK."`
	Error     string  `json:"error" jsonschema_description:"Why the probe failed; empty when OK."`
}

// RegionDiagnostics is the local API's DERP-region debug report: connectivity diagnostics only (spec 4.3).
type RegionDiagnostics struct {
	Info     []string `json:"info" jsonschema_description:"The report's informational lines."`
	Warnings []string `json:"warnings" jsonschema_description:"The report's warnings."`
	Errors   []string `json:"errors" jsonschema_description:"The report's errors."`
}

// ServerProbeInput is the input of derp.server.probe.
type ServerProbeInput struct {
	HostID string   `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the DERP server."`
	Agents []string `json:"agents,omitempty" jsonschema:"maxItems=8" jsonschema_description:"Ids of the hosts that probe it; absent picks the other hosts."`
}

// ServerProbe is the output of derp.server.probe.
type ServerProbe struct {
	HostID     string        `json:"hostId" jsonschema_description:"Id of the host running the DERP server."`
	RegionID   int           `json:"regionId" jsonschema_description:"The server's region id; 0 when the region is not published."`
	RegionCode string        `json:"regionCode" jsonschema_description:"The server's region code; empty when the region is not published."`
	Server     ServerStatus  `json:"server" jsonschema_description:"The DERP host's status."`
	Sources    []ProbeSource `json:"sources" jsonschema_description:"The probes: the controller first, then the agents in input order."`
	Nodes      []NodeUse     `json:"nodes" jsonschema_description:"The tailnet's nodes and how they use the region."`
	Ready      bool          `json:"ready" jsonschema_description:"Whether the probes back publishing the region."`
	Warnings   []string      `json:"warnings" jsonschema_description:"Warnings about the probes."`
}

// ProbeSource is the probe from one place.
type ProbeSource struct {
	Source string             `json:"source" jsonschema_description:"controller, or the id of the probing host."`
	Name   string             `json:"name" jsonschema_description:"controller, or the name of the probing host."`
	TLS    ProbeResult        `json:"tls" jsonschema_description:"The TLS probe of /derp/probe."`
	STUN   ProbeResult        `json:"stun" jsonschema_description:"The STUN probe."`
	Region *RegionDiagnostics `json:"region" jsonschema:"nullable" jsonschema_description:"The source's region diagnostics; null when none were read."`
	Error  string             `json:"error" jsonschema_description:"Why the agent read failed; empty when it succeeded."`
}

// NodeUse is how one node of the tailnet uses the region.
type NodeUse struct {
	Name      string `json:"name" jsonschema_description:"The device name, or the host name when the tailnet is not connected."`
	Address   string `json:"address" jsonschema_description:"The node's Tailscale address."`
	HostID    string `json:"hostId" jsonschema_description:"Id of the Darkpanel host of the node; empty when it is none."`
	Visible   bool   `json:"visible" jsonschema_description:"Whether the DERP host's tailscaled knows the node."`
	Preferred string `json:"preferred" jsonschema_description:"The node's home region code; empty when unknown."`
	Observed  bool   `json:"observed" jsonschema_description:"Whether a peer of the node is relayed through this region now."`
}

// ServerListInput is the input of derp.server.list.
type ServerListInput struct{}

// Servers is the output of derp.server.list.
type Servers struct {
	Servers          []ServerView `json:"servers" jsonschema_description:"The known DERP servers, by host id."`
	AvailableVersion string       `json:"availableVersion" jsonschema_description:"The derper version this release carries."`
}

// ServerView is one known DERP server with its health.
type ServerView struct {
	HostID        string     `json:"hostId" jsonschema_description:"Id of the host running the server."`
	Host          string     `json:"host" jsonschema_description:"The host's name."`
	Mode          string     `json:"mode" jsonschema_description:"The server's certificate mode: letsencrypt or ip."`
	HostName      string     `json:"hostName" jsonschema_description:"The server's DNS name or public IP literal."`
	RegionID      int        `json:"regionId" jsonschema_description:"The server's region id; 0 when the region is not published."`
	RegionCode    string     `json:"regionCode" jsonschema_description:"The server's region code; empty when the region is not published."`
	DerperVersion string     `json:"derperVersion" jsonschema_description:"The derper version installed."`
	CertName      string     `json:"certName" jsonschema_description:"The certificate pin in ip mode; empty for letsencrypt."`
	CertNotAfter  *time.Time `json:"certNotAfter" jsonschema:"nullable" jsonschema_description:"When the ip certificate expires; null for letsencrypt."`
	UnitActive    bool       `json:"unitActive" jsonschema_description:"Whether derper's unit is active."`
	Health        string     `json:"health" jsonschema_description:"The server's health: ok, degraded or broken."`
	Reasons       []string   `json:"reasons" jsonschema_description:"One sentence per health reason; empty when ok."`
	CheckedAt     time.Time  `json:"checkedAt" jsonschema_description:"When the server was last checked."`
	Error         string     `json:"error" jsonschema_description:"Why the last check failed; empty when it succeeded."`
}

// RegionEditsInput is the input of derp.region.edits.
type RegionEditsInput struct {
	Op                 string `json:"op" jsonschema:"enum=set,enum=remove,enum=rotate" jsonschema_description:"The region change: set, remove or rotate."`
	HostID             string `json:"hostId,omitempty" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server; set and rotate."`
	RegionID           int    `json:"regionId" jsonschema:"minimum=900,maximum=999" jsonschema_description:"The region id, 900 to 999."`
	RegionCode         string `json:"regionCode,omitempty" jsonschema:"pattern=^[a-z][a-z0-9-]{0\\,31}$" jsonschema_description:"The region code; set."`
	RegionName         string `json:"regionName,omitempty" jsonschema:"maxLength=64" jsonschema_description:"The region name; set."`
	OmitDefaultRegions bool   `json:"omitDefaultRegions,omitempty" jsonschema_description:"Whether to drop Tailscale's default regions; set."`
	Acknowledge        string `json:"acknowledge,omitempty" jsonschema_description:"The typed acknowledgement of omitting the default regions; set."`
	CertName           string `json:"certName,omitempty" jsonschema_description:"The new certificate pin; rotate."`
}

// RegionEdits is the output of derp.region.edits.
type RegionEdits struct {
	Edits    []policy.Edit `json:"edits" jsonschema_description:"The policy edits; empty when the policy already holds the result."`
	Warnings []string      `json:"warnings" jsonschema_description:"Warnings about the change."`
}

// FallbackCheckInput is the input of derp.fallback.check.
type FallbackCheckInput struct {
	HostID   string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server."`
	RegionID int    `json:"regionId" jsonschema:"minimum=900,maximum=999" jsonschema_description:"The server's region id, 900 to 999."`
}

// FallbackCheck is the output of derp.fallback.check.
type FallbackCheck struct {
	Path string `json:"path" jsonschema_description:"The DERP host's other path: link, default-regions or region:<id>."`
}

// DeployInput is the input of derp.server.deploy.
type DeployInput struct {
	HostID        string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host to run the server on."`
	Mode          string `json:"mode" jsonschema:"enum=letsencrypt,enum=ip" jsonschema_description:"How derper gets its certificate: letsencrypt or ip."`
	HostName      string `json:"hostName" jsonschema:"maxLength=253" jsonschema_description:"The server's DNS name, or its public IP literal in ip mode."`
	VerifyClients *bool  `json:"verifyClients,omitempty" jsonschema:"nullable" jsonschema_description:"Whether derper admits only nodes of this tailnet; absent means true."`
}

// Deployed is the output of derp.server.deploy.
type Deployed struct {
	DerperVersion string     `json:"derperVersion" jsonschema_description:"The derper version installed."`
	CertName      string     `json:"certName" jsonschema_description:"The certificate pin in ip mode; empty for letsencrypt."`
	CertNotAfter  *time.Time `json:"certNotAfter" jsonschema:"nullable" jsonschema_description:"When the ip certificate expires; null for letsencrypt."`
}

// UpgradeInput is the input of derp.server.upgrade.
type UpgradeInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server."`
}

// Upgraded is the output of derp.server.upgrade.
type Upgraded struct {
	From string `json:"from" jsonschema_description:"The derper version replaced."`
	To   string `json:"to" jsonschema_description:"The derper version now installed."`
}

// CertRotateInput is the input of derp.cert.rotate.
type CertRotateInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server."`
}

// CertRotated is the output of derp.cert.rotate.
type CertRotated struct {
	CertName     string    `json:"certName" jsonschema_description:"The new certificate's pin."`
	CertNotAfter time.Time `json:"certNotAfter" jsonschema_description:"When the new certificate expires."`
}

// RegionSetInput is the input of derp.region.set.
type RegionSetInput struct {
	HostID             string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server."`
	RegionID           int    `json:"regionId" jsonschema:"minimum=900,maximum=999" jsonschema_description:"The region id, 900 to 999."`
	RegionCode         string `json:"regionCode" jsonschema:"pattern=^[a-z][a-z0-9-]{0\\,31}$" jsonschema_description:"The region code."`
	RegionName         string `json:"regionName" jsonschema:"maxLength=64" jsonschema_description:"The region name."`
	OmitDefaultRegions bool   `json:"omitDefaultRegions,omitempty" jsonschema_description:"Whether to drop Tailscale's default regions."`
	Acknowledge        string `json:"acknowledge,omitempty" jsonschema_description:"The typed acknowledgement of omitting the default regions."`
}

// RegionRemoveInput is the input of derp.region.remove.
type RegionRemoveInput struct {
	RegionID int `json:"regionId" jsonschema:"minimum=900,maximum=999" jsonschema_description:"The region id, 900 to 999."`
}

// ServerRemoveInput is the input of derp.server.remove.
type ServerRemoveInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host running the server."`
}

// RegionChanged is the output of a composite that changes the DERP region in the tailnet policy.
type RegionChanged struct {
	ETag string `json:"etag" jsonschema_description:"The policy's ETag after the change; empty when nothing changed."`
}

// ServerRemoved is the output of derp.server.remove.
type ServerRemoved struct {
	Removed []string `json:"removed" jsonschema_description:"The effect resources removed, in order."`
}

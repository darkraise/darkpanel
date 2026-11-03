package cloudflare

import "net/netip"

// ConnectorRunInput is the input of cloudflare.connector.run.
type ConnectorRunInput struct {
	Tunnel string `json:"tunnel"`              // the tunnel id: a lower-case UUID
	Token  string `json:"token" secret:"true"` // the tunnel token from cloudflare.tunnel.token
}

// Connector is one connector on a host.
type Connector struct {
	Tunnel      string `json:"tunnel"`
	Container   string `json:"container"` // darkpanel-cloudflared-<tunnel>
	ContainerID string `json:"containerId"`
	State       string `json:"state"` // the Engine's container state; "missing" when the container is gone
}

// ConnectorStopInput is the input of cloudflare.connector.stop.
type ConnectorStopInput struct {
	Tunnel string `json:"tunnel"`
}

// ConnectorStopped is the output of cloudflare.connector.stop.
type ConnectorStopped struct {
	Tunnel  string `json:"tunnel"`
	Removed bool   `json:"removed"` // false when nothing was left to remove
}

// ConnectorListInput is the input of cloudflare.connector.list.
type ConnectorListInput struct{}

// ConnectorList is the output of cloudflare.connector.list, ordered by tunnel.
type ConnectorList struct {
	Connectors []Connector `json:"connectors"`
}

// NetworkAttachInput is the input of cloudflare.network.attach.
type NetworkAttachInput struct {
	Tunnel   string     `json:"tunnel"`
	Network  string     `json:"network"`
	Claimant string     `json:"claimant"`
	Target   string     `json:"target,omitempty"` // a container that must be on Network
	Address  netip.Addr `json:"address,omitzero"` // a fixed address for the connector on Network
}

// NetworkAttachment is the output of cloudflare.network.attach and cloudflare.network.detach.
type NetworkAttachment struct {
	Tunnel      string `json:"tunnel"`
	Network     string `json:"network"`
	Claimant    string `json:"claimant"`
	Preexisting bool   `json:"preexisting"`
	Attached    bool   `json:"attached"` // whether the connector is on Network after the change
}

// NetworkDetachInput is the input of cloudflare.network.detach; Network "" releases every claim of Claimant on the
// tunnel's connector.
type NetworkDetachInput struct {
	Tunnel   string `json:"tunnel"`
	Network  string `json:"network"`
	Claimant string `json:"claimant"`
}

// ConnectorDeployInput is the input of cloudflare.connector.deploy; Tunnel "" creates a tunnel.
type ConnectorDeployInput struct {
	HostID string `json:"hostId"`
	Tunnel string `json:"tunnel,omitempty"` // id or name of a tunnel Darkpanel manages
}

// ConnectorDeployOutput is the output of cloudflare.connector.deploy.
type ConnectorDeployOutput struct {
	Tunnel    string    `json:"tunnel"`  // the tunnel id
	Created   bool      `json:"created"` // whether the composite created the tunnel
	Connector Connector `json:"connector"`
}

// ExposeInput is the input of cloudflare.expose.container.
type ExposeInput struct {
	HostID    string `json:"hostId"`
	Tunnel    string `json:"tunnel"` // id or name
	Container string `json:"container"`
	Port      int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
	Network   string `json:"network"` // a user-defined bridge network the container is on
	Hostname  string `json:"hostname"`
}

// ExposeOutput is the output of cloudflare.expose.container.
type ExposeOutput struct {
	Hostname string `json:"hostname"`
	Service  string `json:"service"`
	Network  string `json:"network"`
}

// ExposeRemoveInput is the input of cloudflare.expose.remove.
type ExposeRemoveInput struct {
	HostID   string `json:"hostId"`
	Hostname string `json:"hostname"`
}

// ExposeRemoveOutput is the output of cloudflare.expose.remove.
type ExposeRemoveOutput struct {
	Hostname string `json:"hostname"`
}

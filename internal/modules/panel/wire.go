package panel

import (
	"fmt"
	"net/netip"
	"regexp"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The names and values of the panel exposure: its network, the routes, the exposure states and the actions.
const (
	// NetworkName is the bridge network between the proxy and the panel's listener.
	NetworkName = "darkpanel-panel"
	// BridgeName is the host interface of NetworkName.
	BridgeName = "dp-panel"
	// BridgeOption is the network option that names the bridge interface.
	BridgeOption = "com.docker.network.bridge.name"
	// DefaultPort is the listener port of an exposure that names none.
	DefaultPort = 9443
	// Claimant is the claimant of the npm and cloudflare network attachments the panel makes.
	Claimant = "panel"
	// ConnectorPrefix is the name of a tunnel's connector container without the tunnel id.
	ConnectorPrefix = "darkpanel-cloudflared-"

	// ViaNPM routes the exposure through Nginx Proxy Manager.
	ViaNPM = "npm"
	// ViaTunnel routes the exposure through a Cloudflare tunnel.
	ViaTunnel = "tunnel"

	// StateOpen is an exposure whose listener is open.
	StateOpen = "open"
	// StateRevoked is an exposure whose listener is revoked.
	StateRevoked = "revoked"
	// StateNeedsRepair is an exposure an operator has to repair.
	StateNeedsRepair = "needs_repair"

	// ActionAdd exposes the panel.
	ActionAdd = "panel.exposure.add"
	// ActionRemove removes the panel's exposure.
	ActionRemove = "panel.exposure.remove"
	// ActionStatus reads the panel's exposure.
	ActionStatus = "panel.exposure.status"
	// ActionNetCreate creates the panel network on the agent's host.
	ActionNetCreate = "panel.network.create"
	// ActionNetRemove removes the panel network from the agent's host.
	ActionNetRemove = "panel.network.remove"
	// ActionConnCheck checks that a tunnel's connector runs on the agent's host.
	ActionConnCheck = "panel.connector.check"
	// ActionOpen opens the panel's listener.
	ActionOpen = "panel.listener.open"
	// ActionRevoke revokes the panel's listener.
	ActionRevoke = "panel.listener.revoke"
)

// PanelRange is the range the panel network's /29 is taken from.
var PanelRange = netip.MustParsePrefix("172.31.255.0/24")

// ExposeInput is the input of panel.exposure.add.
type ExposeInput struct {
	Via      string `json:"via" jsonschema:"enum=npm,enum=tunnel"`
	Hostname string `json:"hostname" jsonschema:"maxLength=253"`                 // lowercase DNS name
	HostID   string `json:"hostId"`                                              // must be the controller's host
	Tunnel   string `json:"tunnel,omitempty"`                                    // required for via tunnel, refused for via npm; lowercase UUID
	Port     int    `json:"port,omitempty" jsonschema:"minimum=0,maximum=65535"` // 0 means DefaultPort; else 1024-65535
}

// ExposeOutput is the output of panel.exposure.add.
type ExposeOutput struct {
	ExposureID string       `json:"exposureId"`
	Via        string       `json:"via"`
	Hostname   string       `json:"hostname"`
	HostID     string       `json:"hostId"`
	Tunnel     string       `json:"tunnel,omitempty"`
	Subnet     netip.Prefix `json:"subnet"`
	Gateway    netip.Addr   `json:"gateway"`
	Proxy      netip.Addr   `json:"proxy"`
	Port       int          `json:"port"`
}

// RemoveInput is the input of panel.exposure.remove; at most one live exposure exists.
type RemoveInput struct{}

// RemoveOutput is the output of panel.exposure.remove.
type RemoveOutput struct {
	ExposureID string `json:"exposureId"`
	Hostname   string `json:"hostname"`
}

// NetworkCreateInput is the input of panel.network.create.
type NetworkCreateInput struct{}

// NetworkView is the panel network: the output of panel.network.create.
type NetworkView struct {
	Network string       `json:"network"` // NetworkName
	ID      string       `json:"id"`      // the Engine's network id
	Subnet  netip.Prefix `json:"subnet"`
	Gateway netip.Addr   `json:"gateway"`
	Proxy   netip.Addr   `json:"proxy"`
}

// NetworkRemoveInput is the input of panel.network.remove.
type NetworkRemoveInput struct{}

// NetworkRemoveOutput is the output of panel.network.remove.
type NetworkRemoveOutput struct {
	Network string `json:"network"`
	Removed bool   `json:"removed"` // false when it was already absent
}

// ConnectorCheckInput is the input of panel.connector.check.
type ConnectorCheckInput struct {
	Tunnel string `json:"tunnel"`
}

// ConnectorCheckOutput is the output of panel.connector.check.
type ConnectorCheckOutput struct {
	Container string `json:"container"` // ConnectorPrefix + tunnel
	State     string `json:"state"`     // the Engine's container state, "running" on success
}

// ListenerOpenInput is the input of panel.listener.open.
type ListenerOpenInput struct {
	Via      string       `json:"via" jsonschema:"enum=npm,enum=tunnel"`
	Hostname string       `json:"hostname"`
	HostID   string       `json:"hostId"`
	Tunnel   string       `json:"tunnel,omitempty"`
	Subnet   netip.Prefix `json:"subnet"`
	Gateway  netip.Addr   `json:"gateway"`
	Proxy    netip.Addr   `json:"proxy"`
	Port     int          `json:"port"`
}

// ListenerOpenOutput is the output of panel.listener.open.
type ListenerOpenOutput struct {
	ExposureID string         `json:"exposureId"`
	ListenerID string         `json:"listenerId"`
	RuleID     string         `json:"ruleId"`
	Address    netip.AddrPort `json:"address"`
}

// ListenerRevokeInput is the input of panel.listener.revoke.
type ListenerRevokeInput struct{}

// ListenerRevokeOutput is the output of panel.listener.revoke.
type ListenerRevokeOutput struct {
	ExposureID string `json:"exposureId"`
	ListenerID string `json:"listenerId"`
	RuleID     string `json:"ruleId"`
	Revoked    bool   `json:"revoked"` // false when the listener was already revoked or closed
}

// StatusInput is the input of panel.exposure.status.
type StatusInput struct{}

// StatusOutput is the output of panel.exposure.status.
type StatusOutput struct {
	Exposed  bool          `json:"exposed"`
	Exposure *ExposureView `json:"exposure,omitempty" jsonschema:"nullable"` // the live exposure, nil when none
}

// ExposureView is an exposure as the API shows it.
type ExposureView struct {
	ID         string       `json:"id"`
	Via        string       `json:"via"`
	Hostname   string       `json:"hostname"`
	HostID     string       `json:"hostId"`
	Tunnel     string       `json:"tunnel,omitempty"`
	Subnet     netip.Prefix `json:"subnet"`
	Gateway    netip.Addr   `json:"gateway"`
	Proxy      netip.Addr   `json:"proxy"`
	Port       int          `json:"port"`
	State      string       `json:"state" jsonschema:"enum=open,enum=revoked,enum=needs_repair"`
	ListenerID string       `json:"listenerId"`
	CreatedAt  time.Time    `json:"createdAt"`
}

var (
	// validateLabel is one label of an exposure's hostname, as npm.proxyhost.add accepts it.
	validateLabel = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)
	// validateTunnel is a tunnel id: a lowercase UUID.
	validateTunnel = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// ValidateExpose refuses an exposure input of the wrong shape with request.invalid_input naming every bad field.
func ValidateExpose(in ExposeInput) error {
	var fields []errcode.FieldError
	add := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	if in.Via != ViaNPM && in.Via != ViaTunnel {
		add("/via", fmt.Sprintf("via must be %q or %q", ViaNPM, ViaTunnel))
	}
	if p := validateHostname(in.Hostname); p != "" {
		add("/hostname", p)
	}
	if in.HostID == "" {
		add("/hostId", "hostId must not be empty")
	}
	switch {
	case in.Via == ViaTunnel && !validateTunnel.MatchString(in.Tunnel):
		add("/tunnel", "tunnel must be a lowercase UUID for via tunnel")
	case in.Via == ViaNPM && in.Tunnel != "":
		add("/tunnel", "tunnel must be empty for via npm")
	}
	if in.Port != 0 && (in.Port < 1024 || in.Port > 65535) {
		add("/port", "port must be 0 or from 1024 to 65535")
	}
	if len(fields) == 0 {
		return nil
	}
	msgs := make([]string, len(fields))
	for i, f := range fields {
		msgs[i] = f.Message
	}
	return &errcode.Error{Code: errcode.InvalidInput, Detail: strings.Join(msgs, "; "), Fields: fields}
}

// validateHostname says what is wrong with an exposure's hostname, or "" when it is acceptable. The rules are the
// ones npm.proxyhost.add applies to a domain, without its wildcard form, so every child of the composite accepts it.
func validateHostname(h string) string {
	if h != strings.ToLower(h) {
		return fmt.Sprintf("hostname %q must be lowercase", h)
	}
	if len(h) < 1 || len(h) > 253 {
		return "hostname must have 1 to 253 bytes"
	}
	labels := strings.Split(h, ".")
	if len(labels) < 2 {
		return fmt.Sprintf("hostname %q must have at least two labels", h)
	}
	for _, l := range labels {
		if !validateLabel.MatchString(l) {
			return fmt.Sprintf("hostname %q must be dot-separated labels of letters, digits and hyphens", h)
		}
	}
	if c := labels[len(labels)-1][0]; c < 'a' || c > 'z' {
		return fmt.Sprintf("the last label of hostname %q must start with a letter", h)
	}
	return ""
}

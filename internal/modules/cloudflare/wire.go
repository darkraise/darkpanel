package cloudflare

import (
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
)

// ConnectInput is the input of cloudflare.account.connect.
type ConnectInput struct {
	AccountID string `json:"accountId"`
	APIToken  string `json:"apiToken" secret:"true"`
}

// AccountView is the connected Cloudflare account.
type AccountView struct {
	AccountID    string             `json:"accountId"`
	Capabilities []cloud.Capability `json:"capabilities"`
	ConnectedAt  time.Time          `json:"connectedAt"`
}

// DisconnectInput is the input of cloudflare.account.disconnect.
type DisconnectInput struct {
	AbandonCleanup bool `json:"abandonCleanup,omitempty"`
}

// Disconnected is the output of cloudflare.account.disconnect: the cleanup it abandoned.
type Disconnected struct {
	Abandoned []string `json:"abandoned"`
}

// AccountGetInput is the input of cloudflare.account.get.
type AccountGetInput struct{}

// AccountStatus is the output of cloudflare.account.get.
type AccountStatus struct {
	Connected bool         `json:"connected"`
	Account   *AccountView `json:"account,omitempty"`
}

// TunnelCreateInput is the input of cloudflare.tunnel.create.
type TunnelCreateInput struct{}

// TunnelInput names one tunnel by id or name.
type TunnelInput struct {
	Tunnel string `json:"tunnel"` // id or name
}

// TunnelRef identifies one tunnel.
type TunnelRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// TunnelListInput is the input of cloudflare.tunnel.list.
type TunnelListInput struct{}

// ConnectionView is one connection of a tunnel's connector.
type ConnectionView struct {
	ID       string    `json:"id"`
	Colo     string    `json:"colo"`
	OriginIP string    `json:"originIp"`
	OpenedAt time.Time `json:"openedAt"`
	Version  string    `json:"version"`
}

// TunnelView is one tunnel as cloudflare.tunnel.list shows it.
type TunnelView struct {
	ID          string           `json:"id"`
	Name        string           `json:"name"`
	Status      string           `json:"status"` // the provider's; "unknown" when the read failed
	Connections []ConnectionView `json:"connections"`
	Drift       bool             `json:"drift"`
	Hostnames   int              `json:"hostnames"`
	CreatedAt   time.Time        `json:"createdAt"`
}

// TunnelList is the output of cloudflare.tunnel.list.
type TunnelList struct {
	Tunnels []TunnelView `json:"tunnels"`
}

// TunnelToken is the output of cloudflare.tunnel.token.
type TunnelToken struct {
	Token string `json:"token" secret:"true"`
}

// HostnameAddInput is the input of cloudflare.hostname.add.
type HostnameAddInput struct {
	Tunnel      string `json:"tunnel"`
	Hostname    string `json:"hostname"`
	Service     string `json:"service"` // http(s)://host:port, tcp://, ssh://, http_status:<code>
	NoTLSVerify bool   `json:"noTLSVerify,omitempty"`
}

// HostnameInput names one public hostname.
type HostnameInput struct {
	Hostname string `json:"hostname"`
}

// HostnameView is one public hostname.
type HostnameView struct {
	Hostname  string     `json:"hostname"`
	Tunnel    string     `json:"tunnel"` // id
	Service   string     `json:"service"`
	RecordID  string     `json:"recordId,omitempty"`
	Phase     string     `json:"phase"` // a stored cloudflare_hostnames phase; "removed" only in hostname.remove's output
	Health    string     `json:"health"`
	CheckedAt *time.Time `json:"checkedAt,omitempty"`
}

// HostnameListInput is the input of cloudflare.hostname.list.
type HostnameListInput struct{}

// HostnameList is the output of cloudflare.hostname.list.
type HostnameList struct {
	Hostnames []HostnameView `json:"hostnames"`
}

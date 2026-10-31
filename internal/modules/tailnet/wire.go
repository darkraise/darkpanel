package tailnet

import (
	"time"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/modules/tailnet/policy"
)

// ConnectInput is the input of tailnet.account.connect.
type ConnectInput struct {
	Tailnet      string   `json:"tailnet"`
	ClientID     string   `json:"clientId"`
	ClientSecret string   `json:"clientSecret" secret:"true"`
	TagsAllowed  []string `json:"tagsAllowed"` // at least one, each "tag:<name>"
}

// AccountView is the connected tailnet account.
type AccountView struct {
	Tailnet      string             `json:"tailnet"`
	ClientID     string             `json:"clientId"`
	TagsAllowed  []string           `json:"tagsAllowed"`
	Capabilities []cloud.Capability `json:"capabilities"`
	ConnectedAt  time.Time          `json:"connectedAt"`
}

// DisconnectInput is the input of tailnet.account.disconnect.
type DisconnectInput struct {
	AbandonCleanup bool `json:"abandonCleanup,omitempty"`
}

// Disconnected is the output of tailnet.account.disconnect: the cleanup it abandoned.
type Disconnected struct {
	Abandoned []string `json:"abandoned"`
}

// AccountGetInput is the input of tailnet.account.get.
type AccountGetInput struct{}

// AccountStatus is the output of tailnet.account.get.
type AccountStatus struct {
	Connected bool         `json:"connected"`
	Account   *AccountView `json:"account,omitempty" jsonschema:"nullable"`
}

// DeviceListInput is the input of tailnet.device.list.
type DeviceListInput struct{}

// Device is one device of the tailnet.
type Device struct {
	ID                string   `json:"id"` // the provider's nodeId
	Name              string   `json:"name"`
	Hostname          string   `json:"hostname"`
	OS                string   `json:"os"`
	User              string   `json:"user"`
	Addresses         []string `json:"addresses"`
	Tags              []string `json:"tags"`
	Authorized        bool     `json:"authorized"`
	LastSeen          string   `json:"lastSeen,omitempty"`
	Expires           string   `json:"expires,omitempty"`
	KeyExpiryDisabled bool     `json:"keyExpiryDisabled"`
	AdvertisedRoutes  []string `json:"advertisedRoutes"`
	EnabledRoutes     []string `json:"enabledRoutes"`
	ExitNode          bool     `json:"exitNode"`        // advertises 0.0.0.0/0 or ::/0
	ExitNodeEnabled   bool     `json:"exitNodeEnabled"` // both approved
}

// DeviceList is the output of tailnet.device.list.
type DeviceList struct {
	Devices []Device `json:"devices"`
}

// DeviceInput names one device.
type DeviceInput struct {
	Device string `json:"device"`
}

// DeviceRef identifies a device an irreversible action acted on.
type DeviceRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// SetRoutesInput is the input of tailnet.device.set-routes.
type SetRoutesInput struct {
	Device string   `json:"device"`
	Routes []string `json:"routes"` // the full approved list; an exit node is 0.0.0.0/0 and ::/0
}

// DeviceRoutes are the routes a device advertises and those approved.
type DeviceRoutes struct {
	Advertised []string `json:"advertisedRoutes"`
	Enabled    []string `json:"enabledRoutes"`
}

// AuthKeyInput is the input of tailnet.authkey.create.
type AuthKeyInput struct {
	Tags          []string `json:"tags"` // each within tagsAllowed
	Reusable      bool     `json:"reusable,omitempty"`
	Ephemeral     bool     `json:"ephemeral,omitempty"`
	Preauthorized bool     `json:"preauthorized,omitempty"`
	ExpirySeconds int      `json:"expirySeconds,omitempty"` // 1–7776000, default 86400
}

// CreatedKey is a created auth key, its secret included.
type CreatedKey struct {
	ID          string    `json:"id"`
	Key         string    `json:"key" secret:"true"`
	Expires     time.Time `json:"expires"`
	Description string    `json:"description"`
}

// AuthKeyListInput is the input of tailnet.authkey.list.
type AuthKeyListInput struct{}

// AuthKeyRow is one auth key the module requested.
type AuthKeyRow struct {
	Description string    `json:"description"`
	Kind        string    `json:"kind"`
	OwnerID     string    `json:"ownerId"`
	KeyID       string    `json:"keyId,omitempty"`
	State       string    `json:"state"`
	RequestedAt time.Time `json:"requestedAt"`
	Expires     time.Time `json:"expires"`
}

// AuthKeyList is the output of tailnet.authkey.list.
type AuthKeyList struct {
	Keys []AuthKeyRow `json:"keys"`
}

// PolicyGetInput is the input of tailnet.policy.get.
type PolicyGetInput struct{}

// Policy is the tailnet policy file and its ETag.
type Policy struct {
	Text string `json:"text"`
	ETag string `json:"etag"`
}

// PolicySetInput is the input of tailnet.policy.set.
type PolicySetInput struct {
	Text string `json:"text"`
}

// PolicyPatchInput is the input of tailnet.policy.patch.
type PolicyPatchInput struct {
	Edits []policy.Edit `json:"edits"`
}

// PolicyWritten is the ETag of a written policy file.
type PolicyWritten struct {
	ETag string `json:"etag"`
}

// KeyRequest is an auth key to create.
type KeyRequest struct {
	Description   string
	Tags          []string
	Reusable      bool
	Ephemeral     bool
	Preauthorized bool
	ExpirySeconds int
}

// KeyInfo is an auth key as the provider describes it, without its secret.
type KeyInfo struct {
	ID          string
	Description string
	Created     time.Time
	Expires     time.Time
	Revoked     bool // the provider's revoked is set or invalid is true (a deleted or expired key)
}

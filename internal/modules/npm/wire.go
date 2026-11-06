package npm

import "net/netip"

// DeployInput is the input of npm.instance.deploy. AcmeEmail becomes the NPM administrator's email, which NPM also
// gives Let's Encrypt as the ACME contact.
type DeployInput struct {
	AcmeEmail string `json:"acmeEmail" jsonschema:"minLength=3,maxLength=254"`
}

// Deployed is the output of npm.instance.deploy.
type Deployed struct {
	ContainerID string `json:"containerId"`
	Image       string `json:"image"`
	AdminEmail  string `json:"adminEmail"`
}

// InstanceStatus is the output of npm.instance.status.
type InstanceStatus struct {
	Deployed         bool           `json:"deployed"`
	ContainerID      string         `json:"containerId"`
	Image            string         `json:"image"`       // the image the container runs
	PinnedImage      string         `json:"pinnedImage"` // the image this release pins
	Running          bool           `json:"running"`
	APIReady         bool           `json:"apiReady"`
	Public           string         `json:"public" jsonschema:"enum=no,enum=confirmed"`
	AdminEmail       string         `json:"adminEmail"`
	AdminExposure    *AdminExposure `json:"adminExposure" jsonschema:"nullable"` // nil when the admin UI is not exposed
	UpgradeAvailable bool           `json:"upgradeAvailable"`
}

// AdminExposure is the exposure of NPM's own admin UI through one of its proxy hosts.
type AdminExposure struct {
	Hostname      string `json:"hostname"`
	ProxyHostID   int    `json:"proxyHostId"`
	CertificateID int    `json:"certificateId"`
}

// ConfirmPublicInput is the input of npm.instance.confirm-public.
type ConfirmPublicInput struct{}

// PublicMark is the output of npm.instance.confirm-public.
type PublicMark struct {
	Public string `json:"public" jsonschema:"enum=no,enum=confirmed"`
}

// RemoveInput is the input of npm.instance.remove.
type RemoveInput struct{}

// Removed is the output of npm.instance.remove.
type Removed struct {
	ContainerID string `json:"containerId"`
}

// AttachInput is the input of npm.network.attach. Target is a container the claimant needs to reach, and Address an
// optional static IPv4 address for the NPM container on the network.
type AttachInput struct {
	Network  string     `json:"network"`
	Claimant string     `json:"claimant"`
	Target   string     `json:"target,omitempty"`
	Address  netip.Addr `json:"address,omitzero"`
}

// Attachment is the output of npm.network.attach.
type Attachment struct {
	Network     string `json:"network"`
	Claimant    string `json:"claimant"`
	Preexisting bool   `json:"preexisting"`
	Attached    bool   `json:"attached"`
}

// DetachInput is the input of npm.network.detach. Network "" detaches every claim of Claimant.
type DetachInput struct {
	Network  string `json:"network"`
	Claimant string `json:"claimant"`
}

// ProxyHostInput is the input of npm.proxyhost.add.
type ProxyHostInput struct {
	Domains       []string `json:"domains"`
	ForwardScheme string   `json:"forwardScheme" jsonschema:"enum=http,enum=https"`
	ForwardHost   string   `json:"forwardHost"`
	ForwardPort   int      `json:"forwardPort" jsonschema:"minimum=1,maximum=65535"`
	CertificateID int      `json:"certificateId,omitempty"`
	AccessListID  int      `json:"accessListId,omitempty"`
	SSLForced     bool     `json:"sslForced,omitempty"`
	HSTS          bool     `json:"hsts,omitempty"`
	Websockets    bool     `json:"websockets,omitempty"`
}

// ProxyHostUpdateInput is the input of npm.proxyhost.update: the proxy host's id and its new settings.
type ProxyHostUpdateInput struct {
	ID int `json:"id"`
	ProxyHostInput
}

// ProxyHostRemoveInput is the input of npm.proxyhost.remove; it names the proxy host by exactly one of ID and Domain.
type ProxyHostRemoveInput struct {
	ID     int    `json:"id,omitempty"`
	Domain string `json:"domain,omitempty"`
}

// ProxyHostView is one proxy host as the module shows it. Reserved is the action whose exposure owns it, or "";
// Unclaimed marks a proxy host whose domain set a skipped creation watched.
type ProxyHostView struct {
	ID            int      `json:"id"`
	Domains       []string `json:"domains"`
	ForwardScheme string   `json:"forwardScheme"`
	ForwardHost   string   `json:"forwardHost"`
	ForwardPort   int      `json:"forwardPort"`
	CertificateID int      `json:"certificateId"`
	AccessListID  int      `json:"accessListId"`
	SSLForced     bool     `json:"sslForced"`
	HSTS          bool     `json:"hsts"`
	Websockets    bool     `json:"websockets"`
	Reserved      string   `json:"reserved"`
	Unclaimed     bool     `json:"unclaimed"`
}

// AccessClientInput is one client rule of an access list: an IP address or CIDR prefix allowed or denied.
type AccessClientInput struct {
	Address   string `json:"address"`
	Directive string `json:"directive" jsonschema:"enum=allow,enum=deny"`
}

// AccessListInput is the input of npm.accesslist.add.
type AccessListInput struct {
	SatisfyAny bool                `json:"satisfyAny,omitempty"`
	Clients    []AccessClientInput `json:"clients"`
}

// AccessListRemoveInput is the input of npm.accesslist.remove.
type AccessListRemoveInput struct {
	ID int `json:"id"`
}

// AccessListView is one access list as the module shows it; Reserved and Unclaimed are as in ProxyHostView.
type AccessListView struct {
	ID         int                 `json:"id"`
	Name       string              `json:"name"`
	SatisfyAny bool                `json:"satisfyAny"`
	Clients    []AccessClientInput `json:"clients"`
	Reserved   string              `json:"reserved"`
	Unclaimed  bool                `json:"unclaimed"`
}

// CertificateInput is the input of npm.certificate.request.
type CertificateInput struct {
	Domains []string `json:"domains"`
}

// CertificateRemoveInput is the input of npm.certificate.remove.
type CertificateRemoveInput struct {
	ID int `json:"id"`
}

// CertificateView is one certificate as the module shows it; Reserved and Unclaimed are as in ProxyHostView.
type CertificateView struct {
	ID        int      `json:"id"`
	Name      string   `json:"name"`
	Domains   []string `json:"domains"`
	ExpiresOn string   `json:"expiresOn"`
	Reserved  string   `json:"reserved"`
	Unclaimed bool     `json:"unclaimed"`
}

// ProxyHostList is the output of npm.proxyhost.list.
type ProxyHostList struct {
	Items []ProxyHostView `json:"items"`
}

// AccessListList is the output of npm.accesslist.list.
type AccessListList struct {
	Items []AccessListView `json:"items"`
}

// CertificateList is the output of npm.certificate.list.
type CertificateList struct {
	Items []CertificateView `json:"items"`
}

// RevealInput is the input of npm.admin.reveal-password.
type RevealInput struct{}

// AdminPassword is the output of npm.admin.reveal-password.
type AdminPassword struct {
	Email    string `json:"email"`
	Password string `json:"password" secret:"true"`
}

// UpgradeInput is the input of npm.instance.upgrade.
type UpgradeInput struct{}

// Upgraded is the output of npm.instance.upgrade.
type Upgraded struct {
	FromImage string `json:"fromImage"`
	ToImage   string `json:"toImage"`
}

// CheckInput is the input of npm.instance.check: the host and whether the Docker integration is expected to be on.
type CheckInput struct {
	HostID      string `json:"hostId" jsonschema:"minLength=1"`
	Integration bool   `json:"integration"`
}

// CheckResult is the output of npm.instance.check.
type CheckResult struct {
	Integration bool `json:"integration"`
}

// PublicInput is the input of npm.public.check.
type PublicInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1"`
}

// PublicResult is the output of npm.public.check; Via says what makes the instance public.
type PublicResult struct {
	Public bool   `json:"public"`
	Via    string `json:"via" jsonschema:"enum=mark,enum=ports"`
}

// HostInstance is the NPM instance status of one host.
type HostInstance struct {
	HostID   string         `json:"hostId"`
	HostName string         `json:"hostName"`
	Status   InstanceStatus `json:"status"`
}

// InstancesOutput is the output of npm.instance.list.
type InstancesOutput struct {
	Instances []HostInstance `json:"instances"`
}

// DeployPublicInput is the input of npm.instance.deploy-public.
type DeployPublicInput struct {
	HostID      string `json:"hostId" jsonschema:"minLength=1"`
	AcmeEmail   string `json:"acmeEmail" jsonschema:"minLength=3,maxLength=254"`
	Integration bool   `json:"integration"`
}

// ExposeInput is the input of npm.expose.container: the container's port to serve on Domains, reached over Network.
type ExposeInput struct {
	HostID       string   `json:"hostId" jsonschema:"minLength=1"`
	Container    string   `json:"container"`
	Port         int      `json:"port" jsonschema:"minimum=1,maximum=65535"`
	Network      string   `json:"network"`
	Domains      []string `json:"domains" jsonschema:"minItems=1"`
	Websockets   bool     `json:"websockets,omitempty"`
	AccessListID int      `json:"accessListId,omitempty"`
}

// ExposeRemoveInput is the input of npm.expose.remove.
type ExposeRemoveInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1"`
	Domain string `json:"domain"`
}

// AdminExposeInput is the input of npm.admin.expose.
type AdminExposeInput struct {
	HostID       string `json:"hostId" jsonschema:"minLength=1"`
	Hostname     string `json:"hostname"`
	AccessListID int    `json:"accessListId,omitempty"`
}

// AdminUnexposeInput is the input of npm.admin.unexpose.
type AdminUnexposeInput struct {
	HostID   string `json:"hostId" jsonschema:"minLength=1"`
	Hostname string `json:"hostname"`
}

// DeployPublicOutput is the output of npm.instance.deploy-public.
type DeployPublicOutput struct {
	ContainerID string `json:"containerId"`
	PortsOpened bool   `json:"portsOpened"`
}

// ExposeOutput is the output of npm.expose.container.
type ExposeOutput struct {
	ProxyHostID   int `json:"proxyHostId"`
	CertificateID int `json:"certificateId"`
}

// ExposeRemoveOutput is the output of npm.expose.remove.
type ExposeRemoveOutput struct {
	Domain string `json:"domain"`
}

// AdminExposeOutput is the output of npm.admin.expose.
type AdminExposeOutput struct {
	ProxyHostID   int `json:"proxyHostId"`
	CertificateID int `json:"certificateId"`
}

// AdminUnexposeOutput is the output of npm.admin.unexpose.
type AdminUnexposeOutput struct {
	Hostname string `json:"hostname"`
}

// StatusInput is the input of npm.instance.status.
type StatusInput struct{}

// ProxyHostListInput is the input of npm.proxyhost.list.
type ProxyHostListInput struct{}

// AccessListListInput is the input of npm.accesslist.list.
type AccessListListInput struct{}

// CertificateListInput is the input of npm.certificate.list.
type CertificateListInput struct{}

// InstanceListInput is the input of npm.instance.list.
type InstanceListInput struct{}

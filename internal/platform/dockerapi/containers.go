package dockerapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
	"strconv"
	"time"
)

// ContainerSummary is one entry of GET /containers/json.
type ContainerSummary struct {
	ID              string `json:"Id"`
	Names           []string
	Image           string
	ImageID         string
	Command         string
	Created         int64 // Unix seconds
	State           string
	Status          string
	Ports           []PortSummary
	Labels          map[string]string
	NetworkSettings *SummaryNetworkSettings
	Mounts          []MountPoint
}

// PortSummary is a published or exposed port of a container summary. IP is
// "" for a port that is exposed but not published.
type PortSummary struct {
	IP          string
	PrivatePort uint16
	PublicPort  uint16
	Type        string
}

// SummaryNetworkSettings is a container summary's networks.
type SummaryNetworkSettings struct {
	Networks map[string]*EndpointSettings
}

// ContainerJSON is GET /containers/{id}/json.
type ContainerJSON struct {
	ID              string `json:"Id"`
	Created         string // RFC 3339 with nanoseconds
	Name            string
	Image           string // the image ID
	RestartCount    int
	State           ContainerState
	Config          ContainerConfig
	HostConfig      HostConfig
	NetworkSettings NetworkSettings
	Mounts          []MountPoint
}

// ContainerState is a container's run state. StartedAt and FinishedAt are
// RFC 3339 with nanoseconds; a container that never ran has the zero time.
type ContainerState struct {
	Status     string
	Running    bool
	Paused     bool
	Restarting bool
	OOMKilled  bool
	Dead       bool
	Pid        int
	ExitCode   int
	Error      string
	StartedAt  string
	FinishedAt string
}

// ContainerConfig is the portable configuration of a container.
type ContainerConfig struct {
	Hostname     string              `json:",omitempty"`
	User         string              `json:",omitempty"`
	Env          []string            `json:",omitempty"`
	Cmd          []string            `json:",omitempty"`
	Entrypoint   []string            `json:",omitempty"`
	Image        string              `json:",omitempty"`
	WorkingDir   string              `json:",omitempty"`
	Labels       map[string]string   `json:",omitempty"`
	ExposedPorts map[string]struct{} `json:",omitempty"`
	Tty          bool                `json:",omitempty"`
}

// HostConfig is the host-dependent configuration of a container. Ports are
// keyed "<port>/<proto>", for example "80/tcp".
type HostConfig struct {
	Binds          []string                 `json:",omitempty"`
	NetworkMode    string                   `json:",omitempty"`
	PortBindings   map[string][]PortBinding `json:",omitempty"`
	RestartPolicy  RestartPolicy            `json:",omitzero"`
	Mounts         []Mount                  `json:",omitempty"`
	ReadonlyRootfs bool                     `json:",omitempty"`
	Privileged     bool                     `json:",omitempty"`
	CapAdd         []string                 `json:",omitempty"`
	CapDrop        []string                 `json:",omitempty"`
	SecurityOpt    []string                 `json:",omitempty"`
}

// PortBinding is one host side of a published port. HostIP is "" or an
// unspecified address for every host address.
type PortBinding struct {
	HostIP   string `json:"HostIp"`
	HostPort string
}

// RestartPolicy is a container's restart policy ("no", "always",
// "unless-stopped", "on-failure").
type RestartPolicy struct {
	Name              string
	MaximumRetryCount int `json:",omitempty"`
}

// Mount is a mount requested at creation.
type Mount struct {
	Type     string // bind | volume | tmpfs
	Source   string `json:",omitempty"`
	Target   string
	ReadOnly bool `json:",omitempty"`
}

// MountPoint is a mount as a container reports it.
type MountPoint struct {
	Type        string
	Name        string
	Source      string
	Destination string
	RW          bool
}

// NetworkSettings is a container's published ports and network endpoints.
type NetworkSettings struct {
	Ports    map[string][]PortBinding
	Networks map[string]*EndpointSettings
}

// EndpointSettings is a container's endpoint on one network, as inspect
// reports it and as create and connect requests carry it.
type EndpointSettings struct {
	IPAMConfig          *EndpointIPAMConfig `json:",omitempty"`
	Aliases             []string            `json:",omitempty"`
	NetworkID           string              `json:",omitempty"`
	EndpointID          string              `json:",omitempty"`
	Gateway             netip.Addr          `json:",omitzero"`
	IPAddress           netip.Addr          `json:",omitzero"`
	IPPrefixLen         int                 `json:",omitempty"`
	IPv6Gateway         netip.Addr          `json:",omitzero"`
	GlobalIPv6Address   netip.Addr          `json:",omitzero"`
	GlobalIPv6PrefixLen int                 `json:",omitempty"`
	MacAddress          string              `json:",omitempty"`
	DNSNames            []string            `json:",omitempty"`
}

// EndpointIPAMConfig is an endpoint's static addresses.
type EndpointIPAMConfig struct {
	IPv4Address netip.Addr `json:",omitzero"`
	IPv6Address netip.Addr `json:",omitzero"`
}

// ContainerCreate is a POST /containers/create request; Name is the
// container's name, sent in the query.
type ContainerCreate struct {
	Name string `json:"-"`
	ContainerConfig
	HostConfig       *HostConfig       `json:",omitempty"`
	NetworkingConfig *NetworkingConfig `json:",omitempty"`
}

// NetworkingConfig names the networks a container is created on.
type NetworkingConfig struct {
	EndpointsConfig map[string]*EndpointSettings
}

// ListContainersOptions selects the containers GET /containers/json lists.
type ListContainersOptions struct {
	All     bool // include stopped containers
	Filters Filters
}

// ListContainers lists containers.
func (c *Client) ListContainers(ctx context.Context, opts ListContainersOptions) ([]ContainerSummary, error) {
	q := url.Values{}
	if opts.All {
		q.Set("all", "1")
	}
	if err := opts.Filters.set(q); err != nil {
		return nil, err
	}
	var out []ContainerSummary
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/containers/json", query: q}, &out)
	return out, err
}

// InspectContainer inspects a container by ID or name.
func (c *Client) InspectContainer(ctx context.Context, id string) (ContainerJSON, error) {
	var out ContainerJSON
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/containers/" + url.PathEscape(id) + "/json"}, &out)
	return out, err
}

// CreateContainer creates a container and returns its ID.
func (c *Client) CreateContainer(ctx context.Context, cfg ContainerCreate) (string, error) {
	q := url.Values{}
	if cfg.Name != "" {
		q.Set("name", cfg.Name)
	}
	var out struct {
		ID string `json:"Id"`
	}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/create", query: q, body: cfg}, &out)
	return out.ID, err
}

// StartContainer starts a container. It reports false when the container
// was already running (the daemon's 304).
func (c *Client) StartContainer(ctx context.Context, id string) (bool, error) {
	code, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/start"}, nil)
	return err == nil && code != http.StatusNotModified, err
}

// StopContainer stops a container, killing it after timeout. It reports
// false when the container was already stopped (the daemon's 304).
func (c *Client) StopContainer(ctx context.Context, id string, timeout time.Duration) (bool, error) {
	q := url.Values{"t": {strconv.Itoa(int(timeout / time.Second))}}
	code, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/stop", query: q}, nil)
	return err == nil && code != http.StatusNotModified, err
}

// RestartContainer restarts a container, killing it after timeout if it
// does not stop.
func (c *Client) RestartContainer(ctx context.Context, id string, timeout time.Duration) error {
	q := url.Values{"t": {strconv.Itoa(int(timeout / time.Second))}}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/containers/" + url.PathEscape(id) + "/restart", query: q}, nil)
	return err
}

// RemoveContainerOptions modifies DELETE /containers/{id}.
type RemoveContainerOptions struct {
	Force   bool // kill a running container first
	Volumes bool // also remove its anonymous volumes
}

// RemoveContainer removes a container.
func (c *Client) RemoveContainer(ctx context.Context, id string, opts RemoveContainerOptions) error {
	q := url.Values{"force": {strconv.FormatBool(opts.Force)}, "v": {strconv.FormatBool(opts.Volumes)}}
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/containers/" + url.PathEscape(id), query: q}, nil)
	return err
}

package dockerapi

import (
	"context"
	"net/http"
	"net/netip"
	"net/url"
)

// Network is a Docker network. Containers is filled by InspectNetwork only;
// the list endpoint leaves it empty.
type Network struct {
	Name    string
	ID      string `json:"Id"`
	Created string
	Scope   string
	Driver  string
	// EnableIPv4 is nil below API 1.47, which does not report it; such a
	// daemon cannot create an IPv6-only network, so nil means IPv4 is on.
	EnableIPv4 *bool
	EnableIPv6 bool
	IPAM       IPAM
	Internal   bool
	Attachable bool
	Containers map[string]NetworkContainer
	Options    map[string]string
	Labels     map[string]string
}

// IPAM is a network's address management.
type IPAM struct {
	Driver string       `json:",omitempty"`
	Config []IPAMConfig `json:",omitempty"`
}

// IPAMConfig is one subnet of a network.
type IPAMConfig struct {
	Subnet  netip.Prefix `json:",omitzero"`
	IPRange netip.Prefix `json:",omitzero"`
	Gateway netip.Addr   `json:",omitzero"`
}

// NetworkContainer is a container's endpoint as a network reports it; the
// addresses carry the subnet's prefix length.
type NetworkContainer struct {
	Name        string
	EndpointID  string
	MacAddress  string
	IPv4Address netip.Prefix
	IPv6Address netip.Prefix
}

// NetworkCreate is a POST /networks/create request.
type NetworkCreate struct {
	Name       string
	Driver     string            `json:",omitempty"`
	Internal   bool              `json:",omitempty"`
	Attachable bool              `json:",omitempty"`
	EnableIPv6 *bool             `json:",omitempty"`
	IPAM       *IPAM             `json:",omitempty"`
	Options    map[string]string `json:",omitempty"`
	Labels     map[string]string `json:",omitempty"`
}

// ListNetworks lists networks.
func (c *Client) ListNetworks(ctx context.Context, filters Filters) ([]Network, error) {
	q := url.Values{}
	if err := filters.set(q); err != nil {
		return nil, err
	}
	var out []Network
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/networks", query: q}, &out)
	return out, err
}

// InspectNetwork inspects a network by ID or name, with its containers.
func (c *Client) InspectNetwork(ctx context.Context, id string) (Network, error) {
	if err := requireID("network", id); err != nil {
		return Network{}, err
	}
	var out Network
	_, err := c.call(ctx, request{method: http.MethodGet, path: "/networks/" + url.PathEscape(id)}, &out)
	return out, err
}

// CreateNetwork creates a network and returns its ID.
func (c *Client) CreateNetwork(ctx context.Context, n NetworkCreate) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/create", body: n}, &out)
	return out.ID, err
}

// RemoveNetwork removes a network; the daemon refuses one with endpoints.
func (c *Client) RemoveNetwork(ctx context.Context, id string) error {
	if err := requireID("network", id); err != nil {
		return err
	}
	_, err := c.call(ctx, request{method: http.MethodDelete, path: "/networks/" + url.PathEscape(id)}, nil)
	return err
}

// ConnectNetwork attaches a container to a network; endpoint may be nil or
// carry aliases and static addresses.
func (c *Client) ConnectNetwork(ctx context.Context, network, container string, endpoint *EndpointSettings) error {
	if err := requireID("network", network); err != nil {
		return err
	}
	if err := requireID("container", container); err != nil {
		return err
	}
	body := struct {
		Container      string
		EndpointConfig *EndpointSettings `json:",omitempty"`
	}{container, endpoint}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/" + url.PathEscape(network) + "/connect", body: body}, nil)
	return err
}

// DisconnectNetwork detaches a container from a network.
func (c *Client) DisconnectNetwork(ctx context.Context, network, container string, force bool) error {
	if err := requireID("network", network); err != nil {
		return err
	}
	if err := requireID("container", container); err != nil {
		return err
	}
	body := struct {
		Container string
		Force     bool
	}{container, force}
	_, err := c.call(ctx, request{method: http.MethodPost, path: "/networks/" + url.PathEscape(network) + "/disconnect", body: body}, nil)
	return err
}

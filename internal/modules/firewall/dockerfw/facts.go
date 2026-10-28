package dockerfw

import "net/netip"

// Endpoint is a container's attachment to one Docker network.
type Endpoint struct {
	Network      string     // network name
	Bridge       string     // the network's bridge interface
	Addr4, Addr6 netip.Addr // zero when absent
}

// Published is one port a container publishes on the host.
type Published struct {
	HostAddr      netip.Addr // as Docker reports it; 0.0.0.0 or :: for every address
	HostPort      uint16
	ContainerPort uint16
	Proto         string
}

// Container is a running container as the Docker integration sees it.
type Container struct {
	ID, Name, Project, Service string
	Ports                      []Published
	Endpoints                  []Endpoint
}

// Network is a Docker network. Bridge holds the value of its
// com.docker.network.bridge.name option, empty when unset.
type Network struct {
	ID, Name, Driver, Bridge     string
	Subnets                      []netip.Prefix
	IPv6                         bool
	GatewayModeV4, GatewayModeV6 string // com.docker.network.bridge.gateway_mode_ipv4|ipv6, "" when unset
}

// Facts is the Docker state the integration realizes its policy against.
type Facts struct {
	Containers []Container // running containers only, sorted by name
	Networks   []Network   // sorted by name
}

// Bridge returns the name of the network's bridge interface.
func Bridge(n Network) string {
	if n.Bridge != "" {
		return n.Bridge
	}
	if n.Name == "bridge" {
		return "docker0"
	}
	return "br-" + n.ID[:min(len(n.ID), 12)]
}

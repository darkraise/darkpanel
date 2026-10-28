package firewall

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/netip"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netfilter"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

// fakeDocker is a test daemon: it answers from its fields and records
// each call in calls ("Info", "Version", "ListContainers",
// "InspectContainer <id>", "ListNetworks", "Events"). When err is set,
// every call is recorded and then fails with it.
type fakeDocker struct {
	info       dockerapi.SystemInfo
	version    dockerapi.VersionInfo
	containers []dockerapi.ContainerJSON
	networks   []dockerapi.Network
	events     chan dockerapi.Event // every stream reads this channel
	err        error

	mu    sync.Mutex // guards calls
	calls []string
}

// newFakeDocker returns Docker 29.9.1 with the iptables driver, no
// containers and the default bridge network.
func newFakeDocker() *fakeDocker {
	return &fakeDocker{
		info: dockerapi.SystemInfo{ServerVersion: "29.9.1", FirewallBackend: &dockerapi.FirewallBackend{Driver: "iptables"}},
		version: dockerapi.VersionInfo{Version: "29.9.1", APIVersion: "1.52", MinAPIVersion: "1.44",
			Os: "linux", Arch: "amd64"},
		networks: []dockerapi.Network{fdNetwork("bridge", "docker0", "172.17.0.0/16")},
		events:   make(chan dockerapi.Event, 16),
	}
}

func (d *fakeDocker) record(call string) error {
	d.mu.Lock()
	d.calls = append(d.calls, call)
	d.mu.Unlock()
	return d.err
}

func (d *fakeDocker) Info(context.Context) (dockerapi.SystemInfo, error) {
	if err := d.record("Info"); err != nil {
		return dockerapi.SystemInfo{}, err
	}
	return d.info, nil
}

func (d *fakeDocker) Version(context.Context) (dockerapi.VersionInfo, error) {
	if err := d.record("Version"); err != nil {
		return dockerapi.VersionInfo{}, err
	}
	return d.version, nil
}

// ListContainers summarizes the running containers, or every container
// when o.All is set; it ignores o.Filters.
func (d *fakeDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	if err := d.record("ListContainers"); err != nil {
		return nil, err
	}
	var out []dockerapi.ContainerSummary
	for _, c := range d.containers {
		if !c.State.Running && !o.All {
			continue
		}
		state := c.State.Status
		if state == "" {
			state = "exited"
			if c.State.Running {
				state = "running"
			}
		}
		out = append(out, dockerapi.ContainerSummary{
			ID: c.ID, Names: []string{c.Name}, Image: c.Config.Image, Labels: c.Config.Labels, State: state,
		})
	}
	return out, nil
}

// InspectContainer finds a container by id, by name or by name without
// Docker's leading slash, and answers the daemon's 404 otherwise.
func (d *fakeDocker) InspectContainer(_ context.Context, id string) (dockerapi.ContainerJSON, error) {
	if err := d.record("InspectContainer " + id); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	for _, c := range d.containers {
		if c.ID == id || c.Name == id || c.Name == "/"+id {
			return c, nil
		}
	}
	return dockerapi.ContainerJSON{}, &dockerapi.APIError{StatusCode: 404, Message: "no such container: " + id}
}

// ListNetworks returns every network; it ignores the filters.
func (d *fakeDocker) ListNetworks(context.Context, dockerapi.Filters) ([]dockerapi.Network, error) {
	if err := d.record("ListNetworks"); err != nil {
		return nil, err
	}
	return slices.Clone(d.networks), nil
}

func (d *fakeDocker) Events(ctx context.Context, _ dockerapi.EventsOptions) (dockerEvents, error) {
	if err := d.record("Events"); err != nil {
		return nil, err
	}
	return &fdEvents{ctx: ctx, events: d.events, done: make(chan struct{})}, nil
}

// fdEvents is one event stream over fakeDocker.events.
type fdEvents struct {
	ctx    context.Context
	events <-chan dockerapi.Event
	done   chan struct{}
	once   sync.Once
}

// Next returns the next event, io.EOF once the stream or the events
// channel is closed, or the context's error once it ends. A closed stream
// or ended context is checked first, so an event still buffered in the
// channel is never delivered after either.
func (s *fdEvents) Next() (dockerapi.Event, error) {
	select {
	case <-s.done:
		return dockerapi.Event{}, io.EOF
	default:
	}
	if err := s.ctx.Err(); err != nil {
		return dockerapi.Event{}, err
	}
	select {
	case <-s.done:
		return dockerapi.Event{}, io.EOF
	case <-s.ctx.Done():
		return dockerapi.Event{}, s.ctx.Err()
	case ev, ok := <-s.events:
		if !ok {
			return dockerapi.Event{}, io.EOF
		}
		return ev, nil
	}
}

func (s *fdEvents) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// fdHexID is the lower-case hex SHA-256 of seed: a 64-digit Docker id.
func fdHexID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// fdContainer returns the running container name with no endpoints and
// empty label, port and network maps, after applying opts in order.
func fdContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	c := dockerapi.ContainerJSON{
		ID:     fdHexID("container:" + name),
		Name:   "/" + name,
		State:  dockerapi.ContainerState{Status: "running", Running: true},
		Config: dockerapi.ContainerConfig{Image: "busybox", Labels: map[string]string{}},
		NetworkSettings: dockerapi.NetworkSettings{
			Ports:    map[string][]dockerapi.PortBinding{},
			Networks: map[string]*dockerapi.EndpointSettings{},
		},
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// fdNetwork returns the local bridge-driver network name over subnets. An
// empty bridge leaves the bridge option unset, so the reader names the
// bridge br-<id> as Docker does.
func fdNetwork(name, bridge string, subnets ...string) dockerapi.Network {
	n := dockerapi.Network{
		ID: fdHexID("network:" + name), Name: name, Scope: "local", Driver: "bridge",
		Options: map[string]string{}, Labels: map[string]string{},
	}
	if bridge != "" {
		n.Options["com.docker.network.bridge.name"] = bridge
	}
	for _, s := range subnets {
		p := netip.MustParsePrefix(s)
		n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: p, Gateway: p.Masked().Addr().Next()})
		if p.Addr().Is6() {
			n.EnableIPv6 = true
		}
	}
	return n
}

// fdDeps are h's deps with d as the daemon (none when d is nil) and the
// other Docker-facing reads served by h's hooks.
func fdDeps(h *fakeHost, d *fakeDocker) deps {
	dd := h.deps()
	if d != nil {
		dd.docker = d
	}
	dd.readVariant = func(_ context.Context, _ core.Env, f fwstate.Family, variant string, tables []string) (*netfilter.Ruleset, error) {
		if h.onReadVariant == nil {
			return nil, nil
		}
		return h.onReadVariant(f, variant, tables)
	}
	dd.sockets = func() ([]netstat.Socket, error) {
		if h.onSockets == nil {
			return nil, nil
		}
		return h.onSockets()
	}
	dd.interfaces = func() ([]netstat.Interface, error) {
		if h.onInterfaces == nil {
			return nil, nil
		}
		return h.onInterfaces()
	}
	dd.routeGet = func(q netstat.RouteQuery) (netstat.Route, error) {
		if h.onRouteGet == nil {
			return netstat.Route{}, netstat.ErrNoRoute
		}
		return h.onRouteGet(q)
	}
	dd.shieldsUp = func(context.Context) (bool, bool, error) {
		if h.onShieldsUp == nil {
			return false, false, nil
		}
		return h.onShieldsUp()
	}
	return dd
}

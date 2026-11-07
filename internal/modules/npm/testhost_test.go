package npm

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/npm/fake"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/modules/npm/proxy"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
	"github.com/darkraise/darkpanel/internal/platform/netstat"
)

var (
	_ core.Runner        = (*thHost)(nil)
	_ core.FileWriter    = (*thHost)(nil)
	_ core.EffectJournal = (*thJournal)(nil)
	_ dockerAPI          = (*thDocker)(nil)
	_ dockerAPI          = dockerClient{}
	_ npmAPI             = (*npmapi.Client)(nil)
)

// thStart is the time the test host's clock starts at.
var thStart = time.Date(2026, 11, 2, 12, 0, 0, 0, time.UTC)

// thPrincipal is the principal every test host call runs as.
var thPrincipal = core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin}

var errTHUnused = errors.New("fake: not used by the npm module")

// thID returns a 64-digit hex id derived from seed.
func thID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

// thRandomBytes is what the host's random dep returns: the bytes 1, 2, ..., n.
func thRandomBytes(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i + 1)
	}
	return b
}

func thAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

// thContainer builds a stopped container named name; the options change it.
func thContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	c := dockerapi.ContainerJSON{
		ID:      thID("container:" + name),
		Name:    "/" + name,
		Created: "2026-10-01T00:00:00Z",
		Image:   "sha256:" + thID("image:"+name),
		State:   dockerapi.ContainerState{Status: "exited", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  dockerapi.ContainerConfig{Image: name + ":latest"},
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// thRunning marks the container running.
func thRunning(c *dockerapi.ContainerJSON) {
	c.State.Status = "running"
	c.State.Running = true
	c.State.Pid = 1
	c.State.StartedAt = "2026-10-01T00:00:01Z"
}

// thLabelled sets the container's labels.
func thLabelled(m map[string]string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Labels = maps.Clone(m) }
}

// thImage sets the image reference the container was created from.
func thImage(ref string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Image = ref }
}

// thEndpoint attaches the container to the network name with the IPv4 address ip; the endpoint is keyed by the network's name.
func thEndpoint(network, ip string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		netID := thID("network:" + network)
		c.NetworkSettings.Networks[network] = &dockerapi.EndpointSettings{
			NetworkID:  netID,
			EndpointID: thID("endpoint:" + c.ID + "@" + netID),
			IPAddress:  netip.MustParseAddr(ip),
		}
	}
}

// thMounted adds a volume mount to the container.
func thMounted(volume, destination string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: "volume", Name: volume, Destination: destination, RW: true})
	}
}

// thPublish publishes ports given as "[ip:]host:container/proto".
func thPublish(specs ...string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		if c.HostConfig.PortBindings == nil {
			c.HostConfig.PortBindings = map[string][]dockerapi.PortBinding{}
		}
		if c.NetworkSettings.Ports == nil {
			c.NetworkSettings.Ports = map[string][]dockerapi.PortBinding{}
		}
		for _, spec := range specs {
			addr, proto, ok := strings.Cut(spec, "/")
			if !ok {
				panic(fmt.Sprintf("thPublish: %q has no protocol", spec))
			}
			parts := strings.Split(addr, ":")
			ip := "0.0.0.0"
			if len(parts) == 3 {
				ip, parts = parts[0], parts[1:]
			}
			if len(parts) != 2 {
				panic(fmt.Sprintf("thPublish: %q is not [ip:]host:container/proto", spec))
			}
			key := parts[1] + "/" + proto
			b := dockerapi.PortBinding{HostIP: ip, HostPort: parts[0]}
			c.HostConfig.PortBindings[key] = append(c.HostConfig.PortBindings[key], b)
			c.NetworkSettings.Ports[key] = append(c.NetworkSettings.Ports[key], b)
		}
	}
}

// thNetwork builds a bridge network named name; the options change it.
func thNetwork(name string, opts ...func(*dockerapi.Network)) dockerapi.Network {
	n := dockerapi.Network{
		ID:      thID("network:" + name),
		Name:    name,
		Created: "2026-10-01T00:00:00Z",
		Scope:   "local",
		Driver:  "bridge",
	}
	for _, o := range opts {
		o(&n)
	}
	return n
}

// thDriver sets the network's driver.
func thDriver(driver string) func(*dockerapi.Network) {
	return func(n *dockerapi.Network) { n.Driver = driver }
}

// thNetLabels sets the network's labels.
func thNetLabels(m map[string]string) func(*dockerapi.Network) {
	return func(n *dockerapi.Network) { n.Labels = maps.Clone(m) }
}

// thSubnet gives the network one IPv4 subnet and its gateway.
func thSubnet(subnet, gateway string) func(*dockerapi.Network) {
	return func(n *dockerapi.Network) {
		n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix(subnet), Gateway: netip.MustParseAddr(gateway)})
	}
}

// thInternal marks the network internal.
func thInternal(n *dockerapi.Network) { n.Internal = true }

func thCloneEndpoint(e *dockerapi.EndpointSettings) *dockerapi.EndpointSettings {
	if e == nil {
		return nil
	}
	c := *e
	c.Aliases = slices.Clone(e.Aliases)
	c.DNSNames = slices.Clone(e.DNSNames)
	if e.IPAMConfig != nil {
		v := *e.IPAMConfig
		c.IPAMConfig = &v
	}
	return &c
}

func thClonePorts(m map[string][]dockerapi.PortBinding) map[string][]dockerapi.PortBinding {
	if m == nil {
		return nil
	}
	out := make(map[string][]dockerapi.PortBinding, len(m))
	for k, b := range m {
		out[k] = slices.Clone(b)
	}
	return out
}

// thCloneContainer copies c so that no map, slice or endpoint pointer is shared with it.
func thCloneContainer(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Labels = maps.Clone(c.Config.Labels)
	c.Config.ExposedPorts = maps.Clone(c.Config.ExposedPorts)
	c.HostConfig.Binds = slices.Clone(c.HostConfig.Binds)
	c.HostConfig.PortBindings = thClonePorts(c.HostConfig.PortBindings)
	c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
	c.HostConfig.CapAdd = slices.Clone(c.HostConfig.CapAdd)
	c.HostConfig.CapDrop = slices.Clone(c.HostConfig.CapDrop)
	c.HostConfig.SecurityOpt = slices.Clone(c.HostConfig.SecurityOpt)
	c.NetworkSettings.Ports = thClonePorts(c.NetworkSettings.Ports)
	if c.NetworkSettings.Networks != nil {
		nets := make(map[string]*dockerapi.EndpointSettings, len(c.NetworkSettings.Networks))
		for k, e := range c.NetworkSettings.Networks {
			nets[k] = thCloneEndpoint(e)
		}
		c.NetworkSettings.Networks = nets
	}
	c.Mounts = slices.Clone(c.Mounts)
	return c
}

func thCloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	n.Containers = maps.Clone(n.Containers)
	return n
}

// thMatch reports whether an object with these labels and name passes the Engine filters the fake supports: label (every
// value must match), name (substring of any value) and status (any value).
func thMatch(f dockerapi.Filters, labels map[string]string, name, status string) bool {
	for _, want := range f["label"] {
		k, v, hasValue := strings.Cut(want, "=")
		got, ok := labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	if names := f["name"]; len(names) > 0 && !slices.ContainsFunc(names, func(s string) bool { return strings.Contains(name, s) }) {
		return false
	}
	if statuses := f["status"]; len(statuses) > 0 && !slices.Contains(statuses, status) {
		return false
	}
	return true
}

// thPull is the progress stream of a fake pull.
type thPull struct {
	msgs []dockerapi.PullMessage
	pos  int
}

func (p *thPull) Next() (dockerapi.PullMessage, error) {
	if p.pos >= len(p.msgs) {
		return dockerapi.PullMessage{}, io.EOF
	}
	m := p.msgs[p.pos]
	p.pos++
	return m, m.Err()
}

func (p *thPull) Close() error { return nil }

// thDocker is an in-memory Docker Engine.
type thDocker struct {
	mu         sync.Mutex
	clock      core.Clock
	containers map[string]dockerapi.ContainerJSON // by id
	networks   map[string]dockerapi.Network       // by id
	volumes    map[string]dockerapi.Volume        // by name
	images     map[string]bool
	archives   map[string][]byte // "<container id>|<path>"
	faults     map[string][]error
	called     []string
	created    int
	version    string
}

func newTHDocker(clock core.Clock) *thDocker {
	return &thDocker{
		clock:      clock,
		containers: map[string]dockerapi.ContainerJSON{},
		networks:   map[string]dockerapi.Network{},
		volumes:    map[string]dockerapi.Volume{},
		images:     map[string]bool{},
		archives:   map[string][]byte{},
		faults:     map[string][]error{},
		version:    "29.9.1",
	}
}

// Fail queues err as the answer of the next call of the method op; the call changes nothing.
func (d *thDocker) Fail(op string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults[op] = append(d.faults[op], err)
}

// calls returns the names of the methods called, in order; a failed call is recorded too.
func (d *thDocker) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.called)
}

// enter records a call and returns the failure queued for it; the caller holds d.mu.
func (d *thDocker) enter(op string) error {
	d.called = append(d.called, op)
	if q := d.faults[op]; len(q) > 0 {
		d.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func (d *thDocker) now() string { return d.clock.Now().UTC().Format(time.RFC3339Nano) }

func (d *thDocker) putContainer(c dockerapi.ContainerJSON) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers[c.ID] = thCloneContainer(c)
}

func (d *thDocker) putNetwork(n dockerapi.Network) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks[n.ID] = thCloneNetwork(n)
}

func (d *thDocker) putVolume(v dockerapi.Volume) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v.Labels = maps.Clone(v.Labels)
	d.volumes[v.Name] = v
}

func (d *thDocker) putImage(ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[ref] = true
}

func (d *thDocker) putArchive(containerID, path string, data []byte) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.archives[containerID+"|"+path] = bytes.Clone(data)
}

func (d *thDocker) container(idOrName string) (dockerapi.ContainerJSON, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, false
	}
	return thCloneContainer(d.containers[id]), true
}

func (d *thDocker) network(idOrName string) (dockerapi.Network, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, false
	}
	return thCloneNetwork(d.networks[id]), true
}

func (d *thDocker) volume(name string) (dockerapi.Volume, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	v, ok := d.volumes[name]
	return v, ok
}

// archive returns the archive CopyToContainer or putArchive stored for path in the container.
func (d *thDocker) archive(containerID, path string) ([]byte, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	b, ok := d.archives[containerID+"|"+path]
	return bytes.Clone(b), ok
}

func (d *thDocker) containerID(idOrName string) (string, bool) {
	if _, ok := d.containers[idOrName]; ok {
		return idOrName, true
	}
	for id, c := range d.containers {
		if c.Name == idOrName || strings.TrimPrefix(c.Name, "/") == idOrName {
			return id, true
		}
	}
	return "", false
}

func (d *thDocker) networkID(idOrName string) (string, bool) {
	if _, ok := d.networks[idOrName]; ok {
		return idOrName, true
	}
	for id, n := range d.networks {
		if n.Name == idOrName {
			return id, true
		}
	}
	return "", false
}

func (d *thDocker) noContainer(id string) error {
	return thAPIError(http.StatusNotFound, "No such container: %s", id)
}
func (d *thDocker) noNetwork(id string) error {
	return thAPIError(http.StatusNotFound, "network %s not found", id)
}

func (d *thDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListContainers"); err != nil {
		return nil, err
	}
	var out []dockerapi.ContainerSummary
	for _, c := range d.containers {
		name := strings.TrimPrefix(c.Name, "/")
		if (!o.All && !c.State.Running) || !thMatch(o.Filters, c.Config.Labels, name, c.State.Status) {
			continue
		}
		cc := thCloneContainer(c)
		out = append(out, dockerapi.ContainerSummary{
			ID: c.ID, Names: []string{c.Name}, Image: c.Config.Image, ImageID: c.Image, State: c.State.Status, Status: c.State.Status,
			Labels: cc.Config.Labels, NetworkSettings: &dockerapi.SummaryNetworkSettings{Networks: cc.NetworkSettings.Networks}, Mounts: cc.Mounts,
		})
	}
	slices.SortFunc(out, func(a, b dockerapi.ContainerSummary) int { return strings.Compare(a.Names[0], b.Names[0]) })
	return out, nil
}

func (d *thDocker) InspectContainer(_ context.Context, idOrName string) (dockerapi.ContainerJSON, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, d.noContainer(idOrName)
	}
	return thCloneContainer(d.containers[id]), nil
}

func (d *thDocker) CreateContainer(_ context.Context, cfg dockerapi.ContainerCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateContainer"); err != nil {
		return "", err
	}
	if cfg.Name != "" {
		if _, taken := d.containerID(cfg.Name); taken {
			return "", thAPIError(http.StatusConflict, "Conflict. The container name %q is already in use", "/"+cfg.Name)
		}
	}
	if !d.images[cfg.Image] {
		return "", thAPIError(http.StatusNotFound, "No such image: %s", cfg.Image)
	}
	d.created++
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("th-container-%d", d.created)
	}
	c := dockerapi.ContainerJSON{
		ID:      thID(fmt.Sprintf("container:%s#%d", name, d.created)),
		Name:    "/" + name,
		Created: d.now(),
		Image:   "sha256:" + thID("image:"+cfg.Image),
		State:   dockerapi.ContainerState{Status: "created", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  cfg.ContainerConfig,
	}
	if cfg.HostConfig != nil {
		c.HostConfig = *cfg.HostConfig
	}
	c.NetworkSettings.Ports = thClonePorts(c.HostConfig.PortBindings)
	attach := map[string]*dockerapi.EndpointSettings{}
	if cfg.NetworkingConfig != nil {
		for k, e := range cfg.NetworkingConfig.EndpointsConfig {
			attach[k] = e
		}
	}
	if mode := c.HostConfig.NetworkMode; mode != "" && !slices.Contains([]string{"default", "bridge", "host", "none"}, mode) {
		if _, ok := attach[mode]; !ok {
			attach[mode] = nil
		}
	}
	for netName, ep := range attach {
		id, ok := d.networkID(netName)
		if !ok {
			return "", d.noNetwork(netName)
		}
		n := d.networks[id]
		e := thCloneEndpoint(ep)
		if e == nil {
			e = &dockerapi.EndpointSettings{}
		}
		e.NetworkID, e.EndpointID = n.ID, thID("endpoint:"+c.ID+"@"+n.ID)
		if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
			e.IPAddress = e.IPAMConfig.IPv4Address
		}
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		c.NetworkSettings.Networks[n.Name] = e
	}
	for _, m := range c.HostConfig.Mounts {
		if m.Type != "volume" {
			continue
		}
		if _, ok := d.volumes[m.Source]; !ok {
			d.volumes[m.Source] = dockerapi.Volume{Name: m.Source, Driver: "local", Mountpoint: "/var/lib/docker/volumes/" + m.Source + "/_data", CreatedAt: d.now(), Scope: "local"}
		}
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: "volume", Name: m.Source, Destination: m.Target, RW: !m.ReadOnly})
	}
	d.containers[c.ID] = thCloneContainer(c)
	return c.ID, nil
}

func (d *thDocker) StartContainer(_ context.Context, id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("StartContainer"); err != nil {
		return false, err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return false, d.noContainer(id)
	}
	c := d.containers[cid]
	if c.State.Running {
		return false, nil
	}
	c.State.Status, c.State.Running, c.State.Pid, c.State.StartedAt = "running", true, 1, d.now()
	d.containers[cid] = c
	return true, nil
}

func (d *thDocker) StopContainer(_ context.Context, id string, _ time.Duration) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("StopContainer"); err != nil {
		return false, err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return false, d.noContainer(id)
	}
	c := d.containers[cid]
	if !c.State.Running {
		return false, nil
	}
	c.State.Status, c.State.Running, c.State.Pid, c.State.FinishedAt = "exited", false, 0, d.now()
	d.containers[cid] = c
	return true, nil
}

func (d *thDocker) RemoveContainer(_ context.Context, id string, o dockerapi.RemoveContainerOptions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveContainer"); err != nil {
		return err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return d.noContainer(id)
	}
	if d.containers[cid].State.Running && !o.Force {
		return thAPIError(http.StatusConflict, "cannot remove container %q: container is running: stop the container before removing or force remove", id)
	}
	delete(d.containers, cid)
	for k := range d.archives {
		if strings.HasPrefix(k, cid+"|") {
			delete(d.archives, k)
		}
	}
	return nil
}

func (d *thDocker) ListNetworks(_ context.Context, f dockerapi.Filters) ([]dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListNetworks"); err != nil {
		return nil, err
	}
	var out []dockerapi.Network
	for _, n := range d.networks {
		if thMatch(f, n.Labels, n.Name, "") {
			n = thCloneNetwork(n)
			n.Containers = nil
			out = append(out, n)
		}
	}
	slices.SortFunc(out, func(a, b dockerapi.Network) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

// attached returns the endpoints of containers on network n, keyed by container id.
func (d *thDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			out[c.ID] = dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
		}
	}
	return out
}

func (d *thDocker) InspectNetwork(_ context.Context, idOrName string) (dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, d.noNetwork(idOrName)
	}
	n := thCloneNetwork(d.networks[id])
	n.Containers = d.attached(n)
	return n, nil
}

func (d *thDocker) CreateNetwork(_ context.Context, nc dockerapi.NetworkCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateNetwork"); err != nil {
		return "", err
	}
	if _, taken := d.networkID(nc.Name); taken {
		return "", thAPIError(http.StatusConflict, "network with name %s already exists", nc.Name)
	}
	n := dockerapi.Network{
		ID: thID("network:" + nc.Name), Name: nc.Name, Created: d.now(), Scope: "local", Driver: nc.Driver, Internal: nc.Internal,
		Attachable: nc.Attachable, Options: maps.Clone(nc.Options), Labels: maps.Clone(nc.Labels),
	}
	if n.Driver == "" {
		n.Driver = "bridge"
	}
	if nc.EnableIPv6 != nil {
		n.EnableIPv6 = *nc.EnableIPv6
	}
	if nc.IPAM != nil {
		n.IPAM = *nc.IPAM
		n.IPAM.Config = slices.Clone(nc.IPAM.Config)
	}
	d.networks[n.ID] = n
	return n.ID, nil
}

func (d *thDocker) RemoveNetwork(_ context.Context, idOrName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveNetwork"); err != nil {
		return err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return d.noNetwork(idOrName)
	}
	if len(d.attached(d.networks[id])) > 0 {
		return thAPIError(http.StatusForbidden, "error while removing network: network %s has active endpoints", idOrName)
	}
	delete(d.networks, id)
	return nil
}

func (d *thDocker) ConnectNetwork(_ context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ConnectNetwork"); err != nil {
		return err
	}
	nid, ok := d.networkID(network)
	if !ok {
		return d.noNetwork(network)
	}
	cid, ok := d.containerID(container)
	if !ok {
		return d.noContainer(container)
	}
	n, c := d.networks[nid], d.containers[cid]
	if c.NetworkSettings.Networks[n.Name] != nil {
		return thAPIError(http.StatusForbidden, "endpoint with name %s already exists in network %s", strings.TrimPrefix(c.Name, "/"), n.Name)
	}
	e := thCloneEndpoint(ep)
	if e == nil {
		e = &dockerapi.EndpointSettings{}
	}
	e.NetworkID, e.EndpointID = n.ID, thID("endpoint:"+c.ID+"@"+n.ID)
	if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
		e.IPAddress = e.IPAMConfig.IPv4Address
	}
	if c.NetworkSettings.Networks == nil {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
	}
	c.NetworkSettings.Networks[n.Name] = e
	d.containers[cid] = c
	return nil
}

func (d *thDocker) DisconnectNetwork(_ context.Context, network, container string, _ bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("DisconnectNetwork"); err != nil {
		return err
	}
	nid, ok := d.networkID(network)
	if !ok {
		return d.noNetwork(network)
	}
	cid, ok := d.containerID(container)
	if !ok {
		return d.noContainer(container)
	}
	n, c := d.networks[nid], d.containers[cid]
	if c.NetworkSettings.Networks[n.Name] == nil {
		return thAPIError(http.StatusForbidden, "container %s is not connected to network %s", container, n.Name)
	}
	delete(c.NetworkSettings.Networks, n.Name)
	d.containers[cid] = c
	return nil
}

func (d *thDocker) ListVolumes(_ context.Context, f dockerapi.Filters) ([]dockerapi.Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListVolumes"); err != nil {
		return nil, err
	}
	var out []dockerapi.Volume
	for _, v := range d.volumes {
		if thMatch(f, v.Labels, v.Name, "") {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b dockerapi.Volume) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (d *thDocker) InspectVolume(_ context.Context, name string) (dockerapi.Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectVolume"); err != nil {
		return dockerapi.Volume{}, err
	}
	v, ok := d.volumes[name]
	if !ok {
		return dockerapi.Volume{}, thAPIError(http.StatusNotFound, "get %s: no such volume", name)
	}
	return v, nil
}

func (d *thDocker) CreateVolume(_ context.Context, vc dockerapi.VolumeCreate) (dockerapi.Volume, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateVolume"); err != nil {
		return dockerapi.Volume{}, err
	}
	if v, ok := d.volumes[vc.Name]; ok {
		return v, nil
	}
	v := dockerapi.Volume{
		Name: vc.Name, Driver: "local", Mountpoint: "/var/lib/docker/volumes/" + vc.Name + "/_data", CreatedAt: d.now(), Scope: "local",
		Labels: maps.Clone(vc.Labels),
	}
	if vc.Driver != "" {
		v.Driver = vc.Driver
	}
	d.volumes[v.Name] = v
	return v, nil
}

func (d *thDocker) RemoveVolume(_ context.Context, name string, _ bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveVolume"); err != nil {
		return err
	}
	if _, ok := d.volumes[name]; !ok {
		return thAPIError(http.StatusNotFound, "get %s: no such volume", name)
	}
	for _, c := range d.containers {
		if slices.ContainsFunc(c.Mounts, func(m dockerapi.MountPoint) bool { return m.Name == name }) {
			return thAPIError(http.StatusConflict, "remove %s: volume is in use - [%s]", name, c.ID)
		}
	}
	delete(d.volumes, name)
	return nil
}

func (d *thDocker) InspectImage(_ context.Context, ref string) (dockerapi.ImageInspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectImage"); err != nil {
		return dockerapi.ImageInspect{}, err
	}
	if !d.images[ref] {
		return dockerapi.ImageInspect{}, thAPIError(http.StatusNotFound, "No such image: %s", ref)
	}
	img := dockerapi.ImageInspect{ID: "sha256:" + thID("image:"+ref), Created: d.now(), Architecture: "amd64", Os: "linux", Config: &dockerapi.ContainerConfig{}}
	if strings.Contains(ref, "@") {
		img.RepoDigests = []string{ref}
	} else {
		img.RepoTags = []string{ref}
	}
	return img, nil
}

func (d *thDocker) PullImage(_ context.Context, ref string) (pullStream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("PullImage"); err != nil {
		return nil, err
	}
	d.images[ref] = true
	return &thPull{msgs: []dockerapi.PullMessage{{Status: "Pulling from " + ref}, {Status: "Status: Downloaded newer image for " + ref}}}, nil
}

// thTar returns a tar archive holding one regular file dir/name with the content.
func thTar(dir, name, content string) []byte {
	var buf bytes.Buffer
	w := tar.NewWriter(&buf)
	if err := w.WriteHeader(&tar.Header{Name: path.Join(dir, name), Mode: 0o644, Size: int64(len(content)), Typeflag: tar.TypeReg}); err != nil {
		panic(err)
	}
	if _, err := w.Write([]byte(content)); err != nil {
		panic(err)
	}
	if err := w.Close(); err != nil {
		panic(err)
	}
	return buf.Bytes()
}

// CopyFromContainer returns the archive stored for the path, or when there is none a tar holding one file marker in the
// path's base directory whose content names the container and the path. It works on a stopped container.
func (d *thDocker) CopyFromContainer(_ context.Context, id, p string) (io.ReadCloser, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CopyFromContainer"); err != nil {
		return nil, err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return nil, d.noContainer(id)
	}
	data, ok := d.archives[cid+"|"+p]
	if !ok {
		data = thTar(path.Base(p), "marker", cid+"|"+p)
	}
	return io.NopCloser(bytes.NewReader(bytes.Clone(data))), nil
}

func (d *thDocker) CopyToContainer(_ context.Context, id, p string, archive io.Reader) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CopyToContainer"); err != nil {
		return err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return d.noContainer(id)
	}
	data, err := io.ReadAll(archive)
	if err != nil {
		return err
	}
	d.archives[cid+"|"+p] = data
	return nil
}

func (d *thDocker) Info(context.Context) (dockerapi.SystemInfo, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("Info"); err != nil {
		return dockerapi.SystemInfo{}, err
	}
	return dockerapi.SystemInfo{ID: "th", ServerVersion: d.version, Driver: "overlay2"}, nil
}

// thJournal is an in-memory core.EffectJournal.
type thJournal struct {
	mu      sync.Mutex
	change  string
	list    []core.Effect
	secrets map[int][]byte
}

// effects returns a copy of the journaled effects.
func (j *thJournal) effects() []core.Effect {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.list)
}

func (j *thJournal) Intend(_ context.Context, e core.Effect, secret []byte) (int, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	e.Seq = len(j.list) + 1
	e.State = core.EffectIntended
	e.ChangeID = j.change
	j.list = append(j.list, e)
	if secret != nil {
		if j.secrets == nil {
			j.secrets = map[int][]byte{}
		}
		j.secrets[e.Seq] = bytes.Clone(secret)
	}
	return e.Seq, nil
}

// intended returns the index of the effect seq when it is still intended.
func (j *thJournal) intended(seq int) (int, error) {
	if seq < 1 || seq > len(j.list) {
		return 0, fmt.Errorf("fake: no effect %d", seq)
	}
	if st := j.list[seq-1].State; st != core.EffectIntended {
		return 0, fmt.Errorf("fake: effect %d is %s, not intended", seq, st)
	}
	return seq - 1, nil
}

func (j *thJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectMade
	j.list[i].Resource = resource
	j.list[i].Post = &post
	return nil
}

func (j *thJournal) Skipped(_ context.Context, seq int) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	i, err := j.intended(seq)
	if err != nil {
		return err
	}
	j.list[i].State = core.EffectSkipped
	return nil
}

func (j *thJournal) List(context.Context) ([]core.Effect, error) { return j.effects(), nil }

func (j *thJournal) Secret(_ context.Context, seq int) ([]byte, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if seq < 1 || seq > len(j.list) {
		return nil, fmt.Errorf("fake: no effect %d", seq)
	}
	return bytes.Clone(j.secrets[seq]), nil
}

// thInstance is what deployed seeds.
type thInstance struct{ ContainerID, Email, Password string }

// thHost is the fake host an npm test runs on: a fake Docker Engine, a fake NPM server, a file system, the agent
// database with the module's tables, and a controller database.
type thHost struct {
	t       *testing.T
	docker  *thDocker
	npm     *fake.Server
	db      *store.DB
	ctlDB   *store.DB
	journal *thJournal
	clock   *coretest.FakeClock
	files   map[string][]byte
	managed map[string][]byte
	agents  map[string]*coretest.FakeAgent
	changes int
}

// newTHHost returns a host with a Docker 29.9.1 engine that holds nothing, an NPM that has no administrator, an agent
// database with the core and npm migrations, a controller database with the core migrations, and a clock at thStart.
func newTHHost(t *testing.T) *thHost {
	t.Helper()
	dir := t.TempDir()
	clock := coretest.NewFakeClock(thStart)
	return &thHost{
		t:       t,
		docker:  newTHDocker(clock),
		npm:     fake.New(t),
		db:      thOpenDB(t, dir, store.SideAgent, store.MigrationSet{Module: ID, FS: agentMigrations()}),
		ctlDB:   thOpenDB(t, dir, store.SideController),
		journal: &thJournal{},
		clock:   clock,
		files:   map[string][]byte{},
		managed: map[string][]byte{},
		agents:  map[string]*coretest.FakeAgent{},
	}
}

// thOpenDB opens the database of side in dir with the core migrations of side and the module sets.
func thOpenDB(t *testing.T, dir, side string, sets ...store.MigrationSet) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(dir, side+".db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: side, BinaryVersion: "test", BackupRoot: filepath.Join(dir, side+"-backups"), Keep: 5, Now: thStart,
	}, coreSet, sets)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("migrations failed: %+v", res.Failed)
	}
	return db
}

// nm returns an nm whose deps all point at the host's fakes: Docker 29.9.1 with raw protection loaded, no socket
// bound, the bytes 1..n as random, a panel with nothing reserved, and every name resolving to 203.0.113.7.
func (h *thHost) nm() nm {
	hc := &http.Client{Timeout: 10 * time.Second}
	newClient := func() *npmapi.Client { return npmapi.New(h.npm.URL(), hc) }
	return nm{d: deps{
		docker:   h.docker,
		api:      apiVia(newClient),
		anon:     func() npmAPI { return newClient() },
		info:     h.docker.Info,
		raw:      func(context.Context, core.Env) (bool, error) { return true, nil },
		random:   func(n int) ([]byte, error) { return thRandomBytes(n), nil },
		readFile: h.readFile,
		readDir:  h.readDir,
		panel:    func(context.Context, core.Env) (proxy.Reserved, error) { return proxy.Reserved{}, nil },
		sockets:  func() ([]netstat.Socket, error) { return nil, nil },
		resolve: func(context.Context, string) ([]netip.Addr, error) {
			return []netip.Addr{netip.MustParseAddr("203.0.113.7")}, nil
		},
	}}
}

func (h *thHost) readFile(name string) ([]byte, error) {
	b, ok := h.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return bytes.Clone(b), nil
}

// thEntry is the fs.DirEntry of a file in the host's files map.
type thEntry struct{ name string }

func (e thEntry) Name() string               { return e.name }
func (e thEntry) IsDir() bool                { return false }
func (e thEntry) Type() fs.FileMode          { return 0 }
func (e thEntry) Info() (fs.FileInfo, error) { return nil, errTHUnused }

// readDir lists the files directly under dir, by name.
func (h *thHost) readDir(dir string) ([]os.DirEntry, error) {
	var out []os.DirEntry
	for name := range h.files {
		if rest, ok := strings.CutPrefix(name, dir+"/"); ok && !strings.Contains(rest, "/") {
			out = append(out, thEntry{rest})
		}
	}
	if len(out) == 0 {
		return nil, &fs.PathError{Op: "open", Path: dir, Err: fs.ErrNotExist}
	}
	slices.SortFunc(out, func(a, b os.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return out, nil
}

// env returns the agent-side environment of a call of the change; an empty changeID is a plan or a read, which has no
// effects journal.
func (h *thHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: thPrincipal,
		DB:        h.db,
		Exec:      h,
		Files:     h,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.mu.Lock()
		h.journal.change = changeID
		h.journal.mu.Unlock()
		env.Effects = h.journal
	}
	return env
}

// ctlEnv returns the controller-side environment of a call of the change. Its Agents answer through the host's agents.
func (h *thHost) ctlEnv(changeID string) core.Env {
	hosts := coretest.MapHosts{}
	for id, a := range h.agents {
		hosts[id] = a
	}
	env := core.Env{
		ChangeID:  changeID,
		Principal: thPrincipal,
		DB:        h.ctlDB,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Agents:    hosts,
	}
	if changeID != "" {
		h.journal.mu.Lock()
		h.journal.change = changeID
		h.journal.mu.Unlock()
		env.Effects = h.journal
	}
	return env
}

// child returns the agent-side environment of a call that is a step of the composite parentAction.
func (h *thHost) child(changeID, parentAction string) core.Env {
	env := h.env(changeID)
	env.Call = core.CallContext{ParentAction: parentAction}
	return env
}

// addChange adds an agent change row of action in state applying, with its plan.
func (h *thHost) addChange(id, action string) {
	h.t.Helper()
	now := h.clock.Now()
	h.changes++
	planID := fmt.Sprintf("p%d-%s", h.changes, id)
	err := h.db.Write(context.Background(), func(tx *sql.Tx) error {
		if _, err := tx.Exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json,
			guards_json, confirmation, created_at, expires_at) VALUES (?, 'h1', ?, 1, ?, '', '', '[]', '[]', '', ?, ?)`,
			planID, action, thPrincipal.Actor, storeTime(now), storeTime(now.Add(time.Hour))); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			input_json, created_at, updated_at) VALUES (?, ?, 'h1', ?, 1, 'compensable', '', ?, 'applying', '{}', ?, ?)`,
			id, planID, action, thPrincipal.Actor, storeTime(now), storeTime(now))
		return err
	})
	if err != nil {
		h.t.Fatal(err)
	}
}

// deployed seeds a running instance: the network, both volumes and the container labelled darkpanel.module=npm, the
// container created from Image and attached to the network, an administrator in the fake NPM, and the instance row
// with the password sealed.
func (h *thHost) deployed() thInstance {
	h.t.Helper()
	inst := thInstance{ContainerID: thID("container:" + containerName), Email: "ops@darkpanel.dev", Password: "th-admin-password-0123456789abcdefghijk"}
	labels := map[string]string{core.LabelModule: ID}
	h.docker.putImage(Image)
	h.docker.putNetwork(thNetwork(networkName, thNetLabels(labels)))
	for _, v := range []string{dataVolume, certVolume} {
		h.docker.putVolume(dockerapi.Volume{Name: v, Driver: "local", Mountpoint: "/var/lib/docker/volumes/" + v + "/_data", CreatedAt: "2026-10-01T00:00:00Z", Scope: "local", Labels: labels})
	}
	h.docker.putContainer(thContainer(containerName, thRunning, thLabelled(labels), thImage(Image), thEndpoint(networkName, "172.20.0.2"),
		thMounted(dataVolume, "/data"), thMounted(certVolume, "/etc/letsencrypt"), thPublish("80:80/tcp", "127.0.0.1:81:81/tcp", "443:443/tcp")))
	h.npm.SeedAdmin(inst.Email, inst.Password)
	sealed, err := h.env("").Sealer.Seal("npm_instances.password_ct.1", []byte(inst.Password))
	if err != nil {
		h.t.Fatal(err)
	}
	err = h.db.Write(context.Background(), func(tx *sql.Tx) error {
		return saveInstance(context.Background(), tx, instanceRow{
			ContainerID: inst.ContainerID, Image: Image, AdminEmail: inst.Email, PasswordCT: sealed, Public: "no",
			CreatedAt: thStart, UpdatedAt: thStart,
		})
	})
	if err != nil {
		h.t.Fatal(err)
	}
	return inst
}

var thManagedName = regexp.MustCompile(ownedPaths()[0].Pattern)

// WriteManaged stores the file at dir/name in both the managed and the files maps, and refuses what the module's owned
// path would: another module, another directory or a name outside the pattern.
func (h *thHost) WriteManaged(_ context.Context, module, dir, name string, data []byte, _ fs.FileMode, _, _ int) error {
	if err := thCheckManaged(module, dir, name); err != nil {
		return err
	}
	h.managed[dir+"/"+name] = bytes.Clone(data)
	h.files[dir+"/"+name] = bytes.Clone(data)
	return nil
}

// RemoveManaged removes the file at dir/name from both maps.
func (h *thHost) RemoveManaged(_ context.Context, module, dir, name string) error {
	if err := thCheckManaged(module, dir, name); err != nil {
		return err
	}
	delete(h.managed, dir+"/"+name)
	delete(h.files, dir+"/"+name)
	return nil
}

func thCheckManaged(module, dir, name string) error {
	switch {
	case module != ID:
		return fmt.Errorf("fake: module %q is not %q", module, ID)
	case dir != backupDir:
		return fmt.Errorf("fake: %s is not an owned managed directory", dir)
	case !thManagedName.MatchString(name):
		return fmt.Errorf("fake: %q does not match the owned name pattern", name)
	}
	return nil
}

// Run refuses every program: the module runs none.
func (h *thHost) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	return core.CmdResult{}, fmt.Errorf("fake: the npm module runs no program, got %s", cmd.Path)
}

func (h *thHost) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errTHUnused
}
func (h *thHost) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errTHUnused
}
func (h *thHost) RemoveBlock(context.Context, string, string, string) error      { return errTHUnused }
func (h *thHost) Remove(context.Context, string, string) error                   { return errTHUnused }
func (h *thHost) Restore(context.Context, string, core.RestoreEntry) error       { return errTHUnused }
func (h *thHost) ReadSysctl(context.Context, string, string) (string, error)     { return "", errTHUnused }
func (h *thHost) WriteSysctl(context.Context, string, string, string) error      { return errTHUnused }
func (h *thHost) Rename(context.Context, string, string, string) error           { return errTHUnused }
func (h *thHost) EnsureServiceDir(context.Context, string, string) error         { return errTHUnused }
func (h *thHost) RenameIn(context.Context, string, string, string, string) error { return errTHUnused }
func (h *thHost) RemoveIn(context.Context, string, string, string) error         { return errTHUnused }
func (h *thHost) RemoveServiceDir(context.Context, string, string) error         { return errTHUnused }
func (h *thHost) Owns(context.Context, string, string) (bool, error)             { return false, errTHUnused }

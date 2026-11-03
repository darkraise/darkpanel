package cloudflare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
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
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var (
	_ connectorDocker = (*agDocker)(nil)
	_ connectorDocker = engineClient{}
	_ core.FileWriter = (*agFiles)(nil)
)

// agStart is the time the agent test host's clock starts at.
var agStart = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// agOperator is the principal every agent test host call runs as.
var agOperator = core.Principal{Actor: "user:u1", Scope: core.ScopeOperate}

// agManagedName is the file-name pattern the module declares for its token directory.
var agManagedName = regexp.MustCompile(ownedPaths()[0].Pattern)

// agID returns a 64-digit hex id derived from seed.
func agID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func agAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

// agContainer builds a stopped container named name; the options change it.
func agContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	c := dockerapi.ContainerJSON{
		ID:      agID("container:" + name),
		Name:    "/" + name,
		Created: "2026-10-01T00:00:00Z",
		Image:   "sha256:" + agID("image:"+name),
		State:   dockerapi.ContainerState{Status: "exited", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  dockerapi.ContainerConfig{Image: name + ":latest"},
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// agRunning marks the container running.
func agRunning(c *dockerapi.ContainerJSON) {
	c.State.Status = "running"
	c.State.Running = true
	c.State.Pid = 1
	c.State.StartedAt = "2026-10-01T00:00:01Z"
}

// agLabelled sets the container's labels.
func agLabelled(m map[string]string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Labels = maps.Clone(m) }
}

// agOnNetwork attaches the container to the network name with the IPv4 address ip; the endpoint is keyed by the
// network's name.
func agOnNetwork(network, ip string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		netID := agID("network:" + network)
		c.NetworkSettings.Networks[network] = &dockerapi.EndpointSettings{
			NetworkID:  netID,
			EndpointID: agID("endpoint:" + c.ID + "@" + netID),
			IPAddress:  netip.MustParseAddr(ip),
		}
	}
}

// agNetwork builds a network named name that uses the driver.
func agNetwork(name, driver string) dockerapi.Network {
	return dockerapi.Network{ID: agID("network:" + name), Name: name, Created: "2026-10-01T00:00:00Z", Scope: "local", Driver: driver}
}

// agToken builds a well-formed tunnel token for the tunnel: the standard base64 of the JSON object
// {"a":<account tag>,"t":<tunnel id>,"s":<base64 secret>}.
func agToken(tunnel string) string {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	doc := fmt.Sprintf(`{"a":%q,"t":%q,"s":%q}`, "0123456789abcdef0123456789abcdef", tunnel, secret)
	return base64.StdEncoding.EncodeToString([]byte(doc))
}

func agCloneEndpoint(e *dockerapi.EndpointSettings) *dockerapi.EndpointSettings {
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

// agCloneContainer copies c so that no map, slice or endpoint pointer is shared with it.
func agCloneContainer(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Labels = maps.Clone(c.Config.Labels)
	c.HostConfig.Binds = slices.Clone(c.HostConfig.Binds)
	c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
	if c.NetworkSettings.Networks != nil {
		nets := make(map[string]*dockerapi.EndpointSettings, len(c.NetworkSettings.Networks))
		for k, e := range c.NetworkSettings.Networks {
			nets[k] = agCloneEndpoint(e)
		}
		c.NetworkSettings.Networks = nets
	}
	c.Mounts = slices.Clone(c.Mounts)
	return c
}

func agCloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	n.Containers = maps.Clone(n.Containers)
	return n
}

// agMatch reports whether an object with these labels and name passes the Engine filters the fake supports: label
// (every value must match), name (substring of any value) and status (any value).
func agMatch(f dockerapi.Filters, labels map[string]string, name, status string) bool {
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

// agPull is the progress stream of a fake pull.
type agPull struct {
	msgs []dockerapi.PullMessage
	pos  int
}

func (p *agPull) Next() (dockerapi.PullMessage, error) {
	if p.pos >= len(p.msgs) {
		return dockerapi.PullMessage{}, io.EOF
	}
	m := p.msgs[p.pos]
	p.pos++
	return m, m.Err()
}

func (p *agPull) Close() error { return nil }

// agDocker is an in-memory Docker Engine holding the three predefined networks (bridge, host and none) and no
// container or image.
type agDocker struct {
	mu         sync.Mutex
	clock      core.Clock
	containers map[string]dockerapi.ContainerJSON // by id
	networks   map[string]dockerapi.Network       // by id
	images     map[string]bool
	faults     map[string][]error
	called     []string
	created    int
}

func newAGDocker(clock core.Clock) *agDocker {
	d := &agDocker{
		clock:      clock,
		containers: map[string]dockerapi.ContainerJSON{},
		networks:   map[string]dockerapi.Network{},
		images:     map[string]bool{},
		faults:     map[string][]error{},
	}
	for name, driver := range map[string]string{"bridge": "bridge", "host": "host", "none": "null"} {
		n := agNetwork(name, driver)
		d.networks[n.ID] = n
	}
	return d
}

// Fail queues err as the answer of the next call of the method op; the call changes nothing.
func (d *agDocker) Fail(op string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults[op] = append(d.faults[op], err)
}

// calls returns the names of the methods called, in order; a failed call is recorded too.
func (d *agDocker) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.called)
}

// enter records a call and returns the failure queued for it; the caller holds d.mu.
func (d *agDocker) enter(op string) error {
	d.called = append(d.called, op)
	if q := d.faults[op]; len(q) > 0 {
		d.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func (d *agDocker) now() string { return d.clock.Now().UTC().Format(time.RFC3339Nano) }

func (d *agDocker) putContainer(c dockerapi.ContainerJSON) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers[c.ID] = agCloneContainer(c)
}

func (d *agDocker) putNetwork(n dockerapi.Network) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks[n.ID] = agCloneNetwork(n)
}

func (d *agDocker) putImage(ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[ref] = true
}

// hasImage reports whether the Engine holds the image.
func (d *agDocker) hasImage(ref string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[ref]
}

// container returns the container with the id or name.
func (d *agDocker) container(idOrName string) (dockerapi.ContainerJSON, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, false
	}
	return agCloneContainer(d.containers[id]), true
}

// network returns the network with the id or name.
func (d *agDocker) network(idOrName string) (dockerapi.Network, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, false
	}
	return agCloneNetwork(d.networks[id]), true
}

func (d *agDocker) containerID(idOrName string) (string, bool) {
	if _, ok := d.containers[idOrName]; ok {
		return idOrName, true
	}
	for id, c := range d.containers {
		if strings.TrimPrefix(c.Name, "/") == strings.TrimPrefix(idOrName, "/") {
			return id, true
		}
	}
	return "", false
}

func (d *agDocker) networkID(idOrName string) (string, bool) {
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

func (d *agDocker) noContainer(id string) error {
	return agAPIError(http.StatusNotFound, "No such container: %s", id)
}

func (d *agDocker) noNetwork(id string) error {
	return agAPIError(http.StatusNotFound, "network %s not found", id)
}

func (d *agDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListContainers"); err != nil {
		return nil, err
	}
	var out []dockerapi.ContainerSummary
	for _, c := range d.containers {
		name := strings.TrimPrefix(c.Name, "/")
		if (!o.All && !c.State.Running) || !agMatch(o.Filters, c.Config.Labels, name, c.State.Status) {
			continue
		}
		cc := agCloneContainer(c)
		out = append(out, dockerapi.ContainerSummary{
			ID: c.ID, Names: []string{c.Name}, Image: c.Config.Image, ImageID: c.Image, State: c.State.Status, Status: c.State.Status,
			Labels: cc.Config.Labels, NetworkSettings: &dockerapi.SummaryNetworkSettings{Networks: cc.NetworkSettings.Networks}, Mounts: cc.Mounts,
		})
	}
	slices.SortFunc(out, func(a, b dockerapi.ContainerSummary) int { return strings.Compare(a.Names[0], b.Names[0]) })
	return out, nil
}

func (d *agDocker) InspectContainer(_ context.Context, idOrName string) (dockerapi.ContainerJSON, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, d.noContainer(idOrName)
	}
	return agCloneContainer(d.containers[id]), nil
}

// CreateContainer creates a stopped container. Without an endpoint in the request it joins the network the
// HostConfig names, or the default bridge network when it names none. A mount or a "src:dst[:ro]" bind is reported
// as a MountPoint.
func (d *agDocker) CreateContainer(_ context.Context, cfg dockerapi.ContainerCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateContainer"); err != nil {
		return "", err
	}
	if cfg.Name != "" {
		if _, taken := d.containerID(cfg.Name); taken {
			return "", agAPIError(http.StatusConflict, "Conflict. The container name %q is already in use", "/"+cfg.Name)
		}
	}
	if !d.images[cfg.Image] {
		return "", agAPIError(http.StatusNotFound, "No such image: %s", cfg.Image)
	}
	d.created++
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("ag-container-%d", d.created)
	}
	c := dockerapi.ContainerJSON{
		ID:      agID(fmt.Sprintf("container:%s#%d", name, d.created)),
		Name:    "/" + name,
		Created: d.now(),
		Image:   "sha256:" + agID("image:"+cfg.Image),
		State:   dockerapi.ContainerState{Status: "created", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  cfg.ContainerConfig,
	}
	if cfg.HostConfig != nil {
		c.HostConfig = *cfg.HostConfig
	}
	attach := map[string]*dockerapi.EndpointSettings{}
	if cfg.NetworkingConfig != nil {
		maps.Copy(attach, cfg.NetworkingConfig.EndpointsConfig)
	}
	if len(attach) == 0 {
		mode := c.HostConfig.NetworkMode
		if mode == "" || mode == "default" {
			mode = "bridge"
		}
		attach[mode] = nil
	}
	for netName, ep := range attach {
		id, ok := d.networkID(netName)
		if !ok {
			return "", d.noNetwork(netName)
		}
		n := d.networks[id]
		e := agCloneEndpoint(ep)
		if e == nil {
			e = &dockerapi.EndpointSettings{}
		}
		e.NetworkID, e.EndpointID = n.ID, agID("endpoint:"+c.ID+"@"+n.ID)
		if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
			e.IPAddress = e.IPAMConfig.IPv4Address
		}
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		c.NetworkSettings.Networks[n.Name] = e
	}
	for _, m := range c.HostConfig.Mounts {
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: m.Type, Source: m.Source, Destination: m.Target, RW: !m.ReadOnly})
	}
	for _, b := range c.HostConfig.Binds {
		parts := strings.Split(b, ":")
		if len(parts) < 2 {
			return "", agAPIError(http.StatusBadRequest, "invalid bind %q", b)
		}
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: "bind", Source: parts[0], Destination: parts[1], RW: !slices.Contains(parts[2:], "ro")})
	}
	d.containers[c.ID] = agCloneContainer(c)
	return c.ID, nil
}

func (d *agDocker) StartContainer(_ context.Context, id string) (bool, error) {
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

func (d *agDocker) StopContainer(_ context.Context, id string, _ time.Duration) (bool, error) {
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

func (d *agDocker) RemoveContainer(_ context.Context, id string, o dockerapi.RemoveContainerOptions) error {
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
		return agAPIError(http.StatusConflict, "cannot remove container %q: container is running: stop the container before removing or force remove", id)
	}
	delete(d.containers, cid)
	return nil
}

// attached returns the endpoints of containers on network n, keyed by container id.
func (d *agDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			out[c.ID] = dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
		}
	}
	return out
}

func (d *agDocker) InspectNetwork(_ context.Context, idOrName string) (dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, d.noNetwork(idOrName)
	}
	n := agCloneNetwork(d.networks[id])
	n.Containers = d.attached(n)
	return n, nil
}

func (d *agDocker) ConnectNetwork(_ context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
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
		return agAPIError(http.StatusForbidden, "endpoint with name %s already exists in network %s", strings.TrimPrefix(c.Name, "/"), n.Name)
	}
	e := agCloneEndpoint(ep)
	if e == nil {
		e = &dockerapi.EndpointSettings{}
	}
	e.NetworkID, e.EndpointID = n.ID, agID("endpoint:"+c.ID+"@"+n.ID)
	if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
		e.IPAddress = e.IPAMConfig.IPv4Address
	}
	if e.IPAMConfig != nil && e.IPAMConfig.IPv6Address.IsValid() {
		e.GlobalIPv6Address = e.IPAMConfig.IPv6Address
	}
	if c.NetworkSettings.Networks == nil {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
	}
	c.NetworkSettings.Networks[n.Name] = e
	d.containers[cid] = c
	return nil
}

func (d *agDocker) DisconnectNetwork(_ context.Context, network, container string, _ bool) error {
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
		return agAPIError(http.StatusForbidden, "container %s is not connected to network %s", container, n.Name)
	}
	delete(c.NetworkSettings.Networks, n.Name)
	d.containers[cid] = c
	return nil
}

func (d *agDocker) InspectImage(_ context.Context, ref string) (dockerapi.ImageInspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectImage"); err != nil {
		return dockerapi.ImageInspect{}, err
	}
	if !d.images[ref] {
		return dockerapi.ImageInspect{}, agAPIError(http.StatusNotFound, "No such image: %s", ref)
	}
	img := dockerapi.ImageInspect{ID: "sha256:" + agID("image:"+ref), Created: d.now(), Architecture: "amd64", Os: "linux", Config: &dockerapi.ContainerConfig{}}
	if strings.Contains(ref, "@") {
		img.RepoDigests = []string{ref}
	} else {
		img.RepoTags = []string{ref}
	}
	return img, nil
}

func (d *agDocker) PullImage(_ context.Context, ref string) (pullStream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("PullImage"); err != nil {
		return nil, err
	}
	d.images[ref] = true
	return &agPull{msgs: []dockerapi.PullMessage{{Status: "Pulling from " + ref}, {Status: "Status: Downloaded newer image for " + ref}}}, nil
}

// agFile is a file the module wrote through WriteManaged.
type agFile struct {
	Data     []byte
	Mode     fs.FileMode
	UID, GID int
}

// agFiles is an in-memory core.FileWriter that accepts only the module's own token directory and file names, as the
// platform's would, and records every call. The module uses no other method.
type agFiles struct {
	mu     sync.Mutex
	files  map[string]agFile
	called []string
	faults map[string][]error
}

func newAGFiles() *agFiles { return &agFiles{files: map[string]agFile{}, faults: map[string][]error{}} }

// Fail queues err as the answer of the next call of the method op, WriteManaged or RemoveManaged; the call changes
// nothing.
func (f *agFiles) Fail(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults[op] = append(f.faults[op], err)
}

// file returns the file stored at dir/name.
func (f *agFiles) file(dir, name string) (agFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[dir+"/"+name]
	file.Data = bytes.Clone(file.Data)
	return file, ok
}

// calls returns the calls made, in order, as "WriteManaged <dir>/<name>" and "RemoveManaged <dir>/<name>".
func (f *agFiles) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.called)
}

// enter records a call, checks the arguments as the platform does and returns the failure queued for it; the caller
// holds f.mu.
func (f *agFiles) enter(op, module, dir, name string) error {
	f.called = append(f.called, op+" "+dir+"/"+name)
	switch {
	case module != ID:
		return fmt.Errorf("fake: module %q is not %q", module, ID)
	case dir != tokenDir:
		return fmt.Errorf("fake: %s is not an owned managed directory", dir)
	case name == "." || name == ".." || filepath.Base(name) != name || !agManagedName.MatchString(name):
		return fmt.Errorf("fake: %q does not match the owned name pattern", name)
	}
	if q := f.faults[op]; len(q) > 0 {
		f.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func (f *agFiles) WriteManaged(_ context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("WriteManaged", module, dir, name); err != nil {
		return err
	}
	f.files[dir+"/"+name] = agFile{Data: bytes.Clone(data), Mode: mode, UID: uid, GID: gid}
	return nil
}

// RemoveManaged removes the file; an absent one is not an error, as on the platform.
func (f *agFiles) RemoveManaged(_ context.Context, module, dir, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveManaged", module, dir, name); err != nil {
		return err
	}
	delete(f.files, dir+"/"+name)
	return nil
}

var errAGUnused = errors.New("fake: not used by the cloudflare module")

func (f *agFiles) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errAGUnused
}
func (f *agFiles) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errAGUnused
}
func (f *agFiles) RemoveBlock(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) Remove(context.Context, string, string) error              { return errAGUnused }
func (f *agFiles) Restore(context.Context, string, core.RestoreEntry) error  { return errAGUnused }
func (f *agFiles) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errAGUnused
}
func (f *agFiles) WriteSysctl(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) Rename(context.Context, string, string, string) error      { return errAGUnused }
func (f *agFiles) EnsureServiceDir(context.Context, string, string) error    { return errAGUnused }
func (f *agFiles) RenameIn(context.Context, string, string, string, string) error {
	return errAGUnused
}
func (f *agFiles) RemoveIn(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) RemoveServiceDir(context.Context, string, string) error { return errAGUnused }
func (f *agFiles) Owns(context.Context, string, string) (bool, error)     { return false, errAGUnused }

// agHost is the fake host an agent-side Cloudflare test runs on: an agent database with the core and the module's
// migrations, a manual clock, an effects journal, a file writer and an in-memory Docker Engine.
type agHost struct {
	t       *testing.T
	db      *store.DB
	clock   *coretest.FakeClock
	journal *cfJournal
	files   *agFiles
	docker  *agDocker
}

// newAGHost returns a host whose Engine holds only the predefined networks, whose token directory is empty and whose
// clock reads agStart.
func newAGHost(t *testing.T) *agHost {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	clock := coretest.NewFakeClock(agStart)
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: agStart,
	}, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("cloudflare agent migrations failed: %v", res.Failed[0].Err)
	}
	return &agHost{t: t, db: db, clock: clock, journal: &cfJournal{}, files: newAGFiles(), docker: newAGDocker(clock)}
}

// cf returns a cf whose agent dependencies are the host's Engine.
func (h *agHost) cf() cf { return cf{a: agentDeps{docker: h.docker}} }

// env returns the agent-side environment of a call of the change; an empty changeID is a plan or a read, which has no
// effects journal.
func (h *agHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: agOperator,
		DB:        h.db,
		Files:     h.files,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.changeID = changeID
		env.Effects = h.journal
	}
	return env
}

// TestAgHostEnvAndJournal: env carries the host's database, files, sealer and clock, gives the journal only to a
// change, and the journal records the effects of the change.
func TestAgHostEnvAndJournal(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	if env := h.env(""); env.Effects != nil || env.DB != h.db || env.Files != h.files || env.Sealer == nil || env.Clock == nil || env.ChangeID != "" ||
		env.Principal.Scope != core.ScopeOperate {
		t.Errorf("env(\"\") = %+v", env)
	}
	env := h.env("c1")
	if env.Effects == nil || env.ChangeID != "c1" {
		t.Fatalf("env(c1) = %+v", env)
	}
	seq, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.connector:t"}, nil)
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := env.Effects.Made(ctx, seq, "cloudflare.connector:t", core.StateDoc{State: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if got := h.journal.effects(); len(got) != 1 || got[0].State != core.EffectMade || got[0].ChangeID != "c1" {
		t.Errorf("effects = %+v", got)
	}
	if cf := h.cf(); cf.a.docker != connectorDocker(h.docker) {
		t.Error("cf().a.docker is not the host's Engine")
	}
	if got := agToken(agTunnel); got == "" {
		t.Error("agToken is empty")
	}
}

// TestAgTokenIsTheDocumentedToken: agToken is the standard base64 of {"a":<account tag>,"t":<tunnel id>,"s":<base64
// secret>}.
func TestAgTokenIsTheDocumentedToken(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(agToken(agTunnel))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ A, T, S string }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	secret, err := base64.StdEncoding.DecodeString(doc.S)
	if doc.T != agTunnel || len(doc.A) != 32 || err != nil || len(secret) != 32 {
		t.Errorf("token document %+v (secret %d bytes, %v), want the tunnel, a 32-character account tag and a 32-byte secret", doc, len(secret), err)
	}
	if agToken("00000000-0000-0000-0000-000000000000") == agToken(agTunnel) {
		t.Error("two tunnels share a token")
	}
}

// TestAgFilesRecordWritesAndRefuseForeignPaths: a write is stored with its mode and owner and read back as a copy,
// a removal of an absent file succeeds, and another module, directory or name is refused as the platform would.
func TestAgFilesRecordWritesAndRefuseForeignPaths(t *testing.T) {
	ctx := context.Background()
	f := newAGFiles()
	name := tokenFileName(agTunnel)
	data := []byte("secret")
	if err := f.WriteManaged(ctx, ID, tokenDir, name, data, 0o400, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	got, ok := f.file(tokenDir, name)
	if !ok || string(got.Data) != "secret" || got.Mode != 0o400 || got.UID != 65532 || got.GID != 65532 {
		t.Fatalf("file = %+v, %t; want the bytes written, mode 0400 and owner 65532:65532", got, ok)
	}
	got.Data[0] = 'Y'
	if again, _ := f.file(tokenDir, name); string(again.Data) != "secret" {
		t.Error("file returned the stored bytes, not a copy")
	}
	for _, c := range []struct{ module, dir, name string }{
		{"npm", tokenDir, name}, {ID, "/var/lib/darkpanel/agent/npm-backups", name}, {ID, tokenDir, "x.token"}, {ID, tokenDir, "../" + name},
	} {
		if err := f.WriteManaged(ctx, c.module, c.dir, c.name, nil, 0o400, 0, 0); err == nil {
			t.Errorf("WriteManaged(%q, %q, %q) was accepted", c.module, c.dir, c.name)
		}
		if err := f.RemoveManaged(ctx, c.module, c.dir, c.name); err == nil {
			t.Errorf("RemoveManaged(%q, %q, %q) was accepted", c.module, c.dir, c.name)
		}
	}
	if err := f.WriteFile(ctx, ID, "/etc/x", nil, 0o600); err == nil {
		t.Error("WriteFile was accepted")
	}
	f.Fail("RemoveManaged", errors.New("disk gone"))
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err == nil || err.Error() != "disk gone" {
		t.Errorf("RemoveManaged with a queued failure = %v", err)
	}
	if _, ok := f.file(tokenDir, name); !ok {
		t.Error("a failed removal removed the file")
	}
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err != nil {
		t.Errorf("removing an absent file: %v, want nil", err)
	}
	if _, ok := f.file(tokenDir, name); ok {
		t.Error("the file remains after its removal")
	}
	want := "WriteManaged " + tokenDir + "/" + name
	if calls := f.calls(); calls[0] != want || calls[len(calls)-1] != "RemoveManaged "+tokenDir+"/"+name {
		t.Errorf("calls = %v", calls)
	}
}

// TestAgDockerCreatesInspectsAndConnects: the Engine fake keeps what a create asked for, starts a container on the
// default bridge network, lists by label, connects and disconnects as Docker does and fails a call on request.
func TestAgDockerCreatesInspectsAndConnects(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	d := h.docker
	const image = "cloudflare/cloudflared:test@sha256:" + "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	if _, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{Name: "c1", ContainerConfig: dockerapi.ContainerConfig{Image: image}}); !dockerapi.IsNotFound(err) {
		t.Fatalf("create from an absent image: %v, want not found", err)
	}
	if _, err := d.InspectImage(ctx, image); !dockerapi.IsNotFound(err) {
		t.Fatalf("InspectImage of an absent image: %v, want not found", err)
	}
	if d.hasImage(image) {
		t.Fatal("the Engine holds an image before any pull")
	}
	stream, err := d.PullImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := stream.Next(); err != nil {
			break
		}
	}
	_ = stream.Close()
	if _, err := d.InspectImage(ctx, image); err != nil || !d.hasImage(image) {
		t.Fatalf("InspectImage after the pull: %v", err)
	}
	id, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{
		Name: "darkpanel-cloudflared-t",
		ContainerConfig: dockerapi.ContainerConfig{
			Image: image, Cmd: []string{"tunnel", "run"}, Labels: map[string]string{core.LabelModule: ID},
		},
		HostConfig: &dockerapi.HostConfig{
			RestartPolicy: dockerapi.RestartPolicy{Name: "unless-stopped"},
			Mounts:        []dockerapi.Mount{{Type: "bind", Source: "/host/file", Target: "/run/darkpanel/token", ReadOnly: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{Name: "darkpanel-cloudflared-t", ContainerConfig: dockerapi.ContainerConfig{Image: image}}); err == nil {
		t.Error("a second container with the same name was created")
	}
	c, err := d.InspectContainer(ctx, "darkpanel-cloudflared-t")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != id || c.State.Status != "created" || c.Config.Image != image || c.HostConfig.RestartPolicy.Name != "unless-stopped" ||
		!slices.Equal(c.Config.Cmd, []string{"tunnel", "run"}) || len(c.Mounts) != 1 ||
		c.Mounts[0] != (dockerapi.MountPoint{Type: "bind", Source: "/host/file", Destination: "/run/darkpanel/token", RW: false}) {
		t.Errorf("created container %+v", c)
	}
	if c.NetworkSettings.Networks["bridge"] == nil || len(c.NetworkSettings.Networks) != 1 {
		t.Errorf("networks %v, want the default bridge only", c.NetworkSettings.Networks)
	}
	if changed, err := d.StartContainer(ctx, id); err != nil || !changed {
		t.Fatalf("StartContainer = %t, %v", changed, err)
	}
	if changed, err := d.StartContainer(ctx, id); err != nil || changed {
		t.Errorf("a second StartContainer = %t, %v; want unchanged", changed, err)
	}
	list, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"label": {core.LabelModule + "=" + ID}}})
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].State != "running" {
		t.Errorf("ListContainers by label = %+v, %v", list, err)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err == nil {
		t.Error("a running container was removed without force")
	}

	d.putNetwork(agNetwork("web", "bridge"))
	d.putImage("app:latest")
	d.putContainer(agContainer("app", agRunning, agLabelled(map[string]string{"k": "v"}), agOnNetwork("web", "172.20.0.5")))
	app, ok := d.container("app")
	if !ok || !app.State.Running || app.Config.Labels["k"] != "v" || app.NetworkSettings.Networks["web"].IPAddress != netip.MustParseAddr("172.20.0.5") {
		t.Fatalf("seeded container %+v, %t", app, ok)
	}
	ep := &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("172.20.0.9")}}
	if err := d.ConnectNetwork(ctx, "web", id, ep); err != nil {
		t.Fatal(err)
	}
	if err := d.ConnectNetwork(ctx, "web", id, nil); err == nil {
		t.Error("a container was connected to a network twice")
	}
	c, _ = d.container(id)
	if got := c.NetworkSettings.Networks["web"]; got == nil || got.IPAddress != netip.MustParseAddr("172.20.0.9") {
		t.Errorf("web endpoint %+v, want the fixed address", got)
	}
	n, err := d.InspectNetwork(ctx, "web")
	if err != nil || len(n.Containers) != 2 || n.Containers[id].Name != "darkpanel-cloudflared-t" || n.Containers[app.ID].Name != "app" {
		t.Errorf("InspectNetwork web = %+v, %v; want the connector and app attached", n.Containers, err)
	}
	if err := d.DisconnectNetwork(ctx, "web", id, false); err != nil {
		t.Fatal(err)
	}
	if err := d.DisconnectNetwork(ctx, "web", id, false); err == nil {
		t.Error("a container was disconnected from a network it is not on")
	}
	if _, err := d.InspectNetwork(ctx, "nowhere"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectNetwork of an unknown network: %v, want not found", err)
	}
	for name, driver := range map[string]string{"bridge": "bridge", "host": "host", "none": "null"} {
		if n, ok := d.network(name); !ok || n.Driver != driver {
			t.Errorf("predefined network %s = %+v, %t; want driver %s", name, n, ok, driver)
		}
	}

	before := len(d.calls())
	d.Fail("StopContainer", errors.New("daemon busy"))
	if _, err := d.StopContainer(ctx, id, time.Second); err == nil || err.Error() != "daemon busy" {
		t.Errorf("StopContainer with a queued failure = %v", err)
	}
	if c, _ := d.container(id); !c.State.Running {
		t.Error("a failed stop stopped the container")
	}
	if changed, err := d.StopContainer(ctx, id, time.Second); err != nil || !changed {
		t.Errorf("StopContainer = %t, %v", changed, err)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InspectContainer(ctx, id); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectContainer after the removal: %v, want not found", err)
	}
	if got := d.calls()[before:]; !slices.Equal(got, []string{"StopContainer", "StopContainer", "RemoveContainer", "InspectContainer"}) {
		t.Errorf("calls since the failure = %v", got)
	}
}

package docker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var errDKUnused = errors.New("fake: not used by the Docker module")

func dkAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

func dkID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func dkContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	c := dockerapi.ContainerJSON{
		ID:      dkID(name),
		Name:    "/" + name,
		Created: "2026-10-01T00:00:00Z",
		Image:   "sha256:" + dkID("image:"+name),
		State:   dockerapi.ContainerState{Status: "exited", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  dockerapi.ContainerConfig{Image: name + ":latest"},
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

func dkRunning(c *dockerapi.ContainerJSON) {
	c.State.Status = "running"
	c.State.Running = true
	c.State.Pid = 1
	c.State.StartedAt = "2026-10-01T00:00:01Z"
}

func dkLabels(m map[string]string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Labels = maps.Clone(m) }
}

// dkPorts publishes ports given as "[ip:]host:container/proto".
func dkPorts(specs ...string) func(*dockerapi.ContainerJSON) {
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
				panic(fmt.Sprintf("dkPorts: %q has no protocol", spec))
			}
			parts := strings.Split(addr, ":")
			ip := "0.0.0.0"
			if len(parts) == 3 {
				ip, parts = parts[0], parts[1:]
			}
			if len(parts) != 2 {
				panic(fmt.Sprintf("dkPorts: %q is not [ip:]host:container/proto", spec))
			}
			key := parts[1] + "/" + proto
			b := dockerapi.PortBinding{HostIP: ip, HostPort: parts[0]}
			c.HostConfig.PortBindings[key] = append(c.HostConfig.PortBindings[key], b)
			c.NetworkSettings.Ports[key] = append(c.NetworkSettings.Ports[key], b)
		}
	}
}

// dkOnNetwork attaches the container to a network; the endpoint is keyed by
// the network's id.
func dkOnNetwork(networkID string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		c.NetworkSettings.Networks[networkID] = &dockerapi.EndpointSettings{
			NetworkID:  networkID,
			EndpointID: dkID("endpoint:" + c.ID + "@" + networkID),
		}
	}
}

func dkNetwork(name string, opts ...func(*dockerapi.Network)) dockerapi.Network {
	n := dockerapi.Network{
		ID:      dkID("network:" + name),
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

func dkVolume(name string, labels map[string]string) dockerapi.Volume {
	return dockerapi.Volume{
		Name:       name,
		Driver:     "local",
		Mountpoint: "/var/lib/docker/volumes/" + name + "/_data",
		CreatedAt:  "2026-10-01T00:00:00Z",
		Scope:      "local",
		Labels:     maps.Clone(labels),
	}
}

// dkImage builds an image summary; a ref that starts with "<none>" is a
// dangling image whose id derives from the whole ref.
func dkImage(ref string, size int64) dockerapi.ImageSummary {
	i := dockerapi.ImageSummary{
		ID:         "sha256:" + dkID("image:"+ref),
		Created:    1790812800,
		Size:       size,
		Containers: -1,
		SharedSize: -1,
	}
	if !strings.HasPrefix(ref, "<none>") {
		i.RepoTags = []string{ref}
	}
	return i
}

func dkCloneEndpoint(e *dockerapi.EndpointSettings) *dockerapi.EndpointSettings {
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

func dkCloneEndpoints(m map[string]*dockerapi.EndpointSettings) map[string]*dockerapi.EndpointSettings {
	if m == nil {
		return nil
	}
	out := make(map[string]*dockerapi.EndpointSettings, len(m))
	for k, e := range m {
		out[k] = dkCloneEndpoint(e)
	}
	return out
}

func dkClonePorts(m map[string][]dockerapi.PortBinding) map[string][]dockerapi.PortBinding {
	if m == nil {
		return nil
	}
	out := make(map[string][]dockerapi.PortBinding, len(m))
	for k, b := range m {
		out[k] = slices.Clone(b)
	}
	return out
}

// dkCloneContainer copies c so that no map, slice or endpoint pointer is
// shared with it.
func dkCloneContainer(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Labels = maps.Clone(c.Config.Labels)
	c.Config.ExposedPorts = maps.Clone(c.Config.ExposedPorts)
	c.HostConfig.Binds = slices.Clone(c.HostConfig.Binds)
	c.HostConfig.PortBindings = dkClonePorts(c.HostConfig.PortBindings)
	c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
	c.HostConfig.CapAdd = slices.Clone(c.HostConfig.CapAdd)
	c.HostConfig.CapDrop = slices.Clone(c.HostConfig.CapDrop)
	c.HostConfig.SecurityOpt = slices.Clone(c.HostConfig.SecurityOpt)
	c.NetworkSettings.Ports = dkClonePorts(c.NetworkSettings.Ports)
	c.NetworkSettings.Networks = dkCloneEndpoints(c.NetworkSettings.Networks)
	c.Mounts = slices.Clone(c.Mounts)
	return c
}

// dkMatchFilters reports whether an object with these labels and name passes
// f. A key other than label and name is an error, so a test never passes by
// silently ignoring a filter.
func dkMatchFilters(f dockerapi.Filters, labels map[string]string, name string) (bool, error) {
	for _, key := range slices.Sorted(maps.Keys(f)) {
		if key != "label" && key != "name" {
			return false, fmt.Errorf("fake: unsupported filter %q", key)
		}
	}
	for _, l := range f["label"] {
		k, v, withValue := strings.Cut(l, "=")
		got, ok := labels[k]
		if !ok || (withValue && got != v) {
			return false, nil
		}
	}
	for _, n := range f["name"] {
		if !strings.Contains(name, n) {
			return false, nil
		}
	}
	return true, nil
}

// dkStream yields items and then, when it follows, waits until it is closed
// or its context ends.
type dkStream[T any] struct {
	ctx    context.Context
	mu     sync.Mutex
	items  []T
	follow bool
	done   chan struct{}
	once   sync.Once
}

func newDKStream[T any](ctx context.Context, items []T, follow bool) *dkStream[T] {
	return &dkStream[T]{ctx: ctx, items: items, follow: follow, done: make(chan struct{})}
}

func (s *dkStream[T]) Next() (T, error) {
	var zero T
	select {
	case <-s.done:
		return zero, io.EOF
	default:
	}
	s.mu.Lock()
	if len(s.items) > 0 {
		it := s.items[0]
		s.items = s.items[1:]
		s.mu.Unlock()
		return it, nil
	}
	s.mu.Unlock()
	if !s.follow {
		return zero, io.EOF
	}
	select {
	case <-s.done:
		return zero, io.EOF
	case <-s.ctx.Done():
		return zero, s.ctx.Err()
	}
}

func (s *dkStream[T]) Close() error {
	s.once.Do(func() { close(s.done) })
	return nil
}

// dkPullStream yields scripted pull messages; the image exists once the
// stream has ended without a failure.
type dkPullStream struct {
	mu     sync.Mutex
	msgs   []dockerapi.PullMessage
	failed bool
	closed bool
	finish func()
}

func (s *dkPullStream) Next() (dockerapi.PullMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return dockerapi.PullMessage{}, io.EOF
	}
	if len(s.msgs) == 0 {
		if !s.failed && s.finish != nil {
			s.finish()
		}
		s.finish = nil
		return dockerapi.PullMessage{}, io.EOF
	}
	m := s.msgs[0]
	s.msgs = s.msgs[1:]
	if err := m.Err(); err != nil {
		s.failed = true
		return m, err
	}
	return m, nil
}

func (s *dkPullStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}

// fakeDocker is an in-memory Docker daemon.
type fakeDocker struct {
	mu         sync.Mutex
	clock      *coretest.FakeClock
	containers map[string]dockerapi.ContainerJSON // by id
	images     map[string]dockerapi.ImageSummary  // by id
	networks   map[string]dockerapi.Network       // by id
	volumes    map[string]dockerapi.Volume        // by name
	failures   map[string][]error
	called     []string
	counter    int

	pulls map[string][]dockerapi.PullMessage
	logs  map[string][]dockerapi.LogFrame
	stats map[string][]dockerapi.Stats
}

var _ dockerAPI = (*fakeDocker)(nil)

func newFakeDocker(clock *coretest.FakeClock) *fakeDocker {
	return &fakeDocker{
		clock:      clock,
		containers: map[string]dockerapi.ContainerJSON{},
		images:     map[string]dockerapi.ImageSummary{},
		networks:   map[string]dockerapi.Network{},
		volumes:    map[string]dockerapi.Volume{},
		failures:   map[string][]error{},
		pulls:      map[string][]dockerapi.PullMessage{},
		logs:       map[string][]dockerapi.LogFrame{},
		stats:      map[string][]dockerapi.Stats{},
	}
}

func (f *fakeDocker) now() string { return f.clock.Now().UTC().Format(time.RFC3339Nano) }

func (f *fakeDocker) put(c dockerapi.ContainerJSON) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.containers[c.ID] = dkCloneContainer(c)
}

func (f *fakeDocker) putImage(i dockerapi.ImageSummary) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.images[i.ID] = f.cloneImage(i)
}

func (f *fakeDocker) putNetwork(n dockerapi.Network) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.networks[n.ID] = f.cloneNetwork(n)
}

func (f *fakeDocker) putVolume(v dockerapi.Volume) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.volumes[v.Name] = f.cloneVolume(v)
}

// container finds a container by id or by name and returns a copy.
func (f *fakeDocker) container(id string) (dockerapi.ContainerJSON, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	key, ok := f.findContainer(id)
	if !ok {
		return dockerapi.ContainerJSON{}, false
	}
	return dkCloneContainer(f.containers[key]), true
}

// fail queues err for the next call of the named dockerAPI method.
func (f *fakeDocker) fail(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.failures[method] = append(f.failures[method], err)
}

// calls returns the names of the methods called so far, failed calls included.
func (f *fakeDocker) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.called)
}

// enter records a call and returns the failure queued for it. The caller
// holds f.mu.
func (f *fakeDocker) enter(method string) error {
	f.called = append(f.called, method)
	q := f.failures[method]
	if len(q) == 0 {
		return nil
	}
	f.failures[method] = q[1:]
	return q[0]
}

func (f *fakeDocker) findContainer(ref string) (string, bool) {
	if _, ok := f.containers[ref]; ok {
		return ref, true
	}
	name := strings.TrimPrefix(ref, "/")
	for _, id := range slices.Sorted(maps.Keys(f.containers)) {
		if strings.TrimPrefix(f.containers[id].Name, "/") == name {
			return id, true
		}
	}
	return "", false
}

func (f *fakeDocker) findNetwork(ref string) (string, bool) {
	if _, ok := f.networks[ref]; ok {
		return ref, true
	}
	for _, id := range slices.Sorted(maps.Keys(f.networks)) {
		if f.networks[id].Name == ref {
			return id, true
		}
	}
	return "", false
}

func (f *fakeDocker) findImage(ref string) (string, bool) {
	for _, id := range slices.Sorted(maps.Keys(f.images)) {
		i := f.images[id]
		if id == ref || id == "sha256:"+ref || slices.Contains(i.RepoTags, ref) ||
			slices.Contains(i.RepoDigests, ref) || slices.Contains(i.RepoTags, ref+":latest") {
			return id, true
		}
	}
	return "", false
}

func (f *fakeDocker) cloneImage(i dockerapi.ImageSummary) dockerapi.ImageSummary {
	i.RepoTags = slices.Clone(i.RepoTags)
	i.RepoDigests = slices.Clone(i.RepoDigests)
	i.Labels = maps.Clone(i.Labels)
	return i
}

func (f *fakeDocker) cloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.Containers = nil
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	if n.EnableIPv4 != nil {
		v := *n.EnableIPv4
		n.EnableIPv4 = &v
	}
	return n
}

func (f *fakeDocker) cloneVolume(v dockerapi.Volume) dockerapi.Volume {
	v.Labels = maps.Clone(v.Labels)
	v.Options = maps.Clone(v.Options)
	return v
}

// endpointOn returns the key of the container's endpoint on the network.
func (f *fakeDocker) endpointOn(c dockerapi.ContainerJSON, networkID string) (string, bool) {
	for key, ep := range c.NetworkSettings.Networks {
		if ep != nil && ep.NetworkID == networkID {
			return key, true
		}
	}
	return "", false
}

func (f *fakeDocker) imageInUse(imageID string) bool {
	for _, c := range f.containers {
		if c.Image == imageID {
			return true
		}
	}
	return false
}

func (f *fakeDocker) Version(context.Context) (dockerapi.VersionInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Version"); err != nil {
		return dockerapi.VersionInfo{}, err
	}
	return dockerapi.VersionInfo{Version: "29.9.1", APIVersion: "1.52", MinAPIVersion: "1.44", Os: "linux", Arch: "amd64"}, nil
}

func (f *fakeDocker) Info(context.Context) (dockerapi.SystemInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Info"); err != nil {
		return dockerapi.SystemInfo{}, err
	}
	info := dockerapi.SystemInfo{ServerVersion: "29.9.1", Containers: len(f.containers), Images: len(f.images)}
	for _, c := range f.containers {
		if c.State.Running {
			info.ContainersRunning++
		}
	}
	return info, nil
}

func (f *fakeDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListContainers"); err != nil {
		return nil, err
	}
	if _, err := dkMatchFilters(o.Filters, nil, ""); err != nil {
		return nil, err
	}
	var cs []dockerapi.ContainerJSON
	for _, c := range f.containers {
		if !o.All && !c.State.Running {
			continue
		}
		if ok, _ := dkMatchFilters(o.Filters, c.Config.Labels, strings.TrimPrefix(c.Name, "/")); ok {
			cs = append(cs, dkCloneContainer(c))
		}
	}
	slices.SortFunc(cs, func(a, b dockerapi.ContainerJSON) int { return strings.Compare(a.Name, b.Name) })
	out := make([]dockerapi.ContainerSummary, 0, len(cs))
	for _, c := range cs {
		s := dockerapi.ContainerSummary{
			ID:              c.ID,
			Names:           []string{c.Name},
			Image:           c.Config.Image,
			ImageID:         c.Image,
			State:           c.State.Status,
			Status:          c.State.Status,
			Labels:          c.Config.Labels,
			NetworkSettings: &dockerapi.SummaryNetworkSettings{Networks: c.NetworkSettings.Networks},
			Mounts:          c.Mounts,
		}
		if t, err := time.Parse(time.RFC3339Nano, c.Created); err == nil {
			s.Created = t.Unix()
		}
		for _, key := range slices.Sorted(maps.Keys(c.NetworkSettings.Ports)) {
			port, proto, _ := strings.Cut(key, "/")
			private, _ := strconv.ParseUint(port, 10, 16)
			for _, b := range c.NetworkSettings.Ports[key] {
				public, _ := strconv.ParseUint(b.HostPort, 10, 16)
				s.Ports = append(s.Ports, dockerapi.PortSummary{IP: b.HostIP, PrivatePort: uint16(private), PublicPort: uint16(public), Type: proto})
			}
		}
		out = append(out, s)
	}
	return out, nil
}

func (f *fakeDocker) InspectContainer(_ context.Context, id string) (dockerapi.ContainerJSON, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return dockerapi.ContainerJSON{}, dkAPIError(404, "No such container: %s", id)
	}
	return dkCloneContainer(f.containers[key]), nil
}

func (f *fakeDocker) StartContainer(_ context.Context, id string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("StartContainer"); err != nil {
		return false, err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return false, dkAPIError(404, "No such container: %s", id)
	}
	c := f.containers[key]
	if c.State.Running {
		return false, nil
	}
	c.State.Running, c.State.Status, c.State.StartedAt = true, "running", f.now()
	f.containers[key] = c
	return true, nil
}

func (f *fakeDocker) StopContainer(_ context.Context, id string, _ time.Duration) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("StopContainer"); err != nil {
		return false, err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return false, dkAPIError(404, "No such container: %s", id)
	}
	c := f.containers[key]
	if !c.State.Running {
		return false, nil
	}
	c.State.Running, c.State.Status, c.State.FinishedAt = false, "exited", f.now()
	f.containers[key] = c
	return true, nil
}

func (f *fakeDocker) RestartContainer(_ context.Context, id string, _ time.Duration) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RestartContainer"); err != nil {
		return err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return dkAPIError(404, "No such container: %s", id)
	}
	c := f.containers[key]
	c.State.Running, c.State.Status, c.State.StartedAt = true, "running", f.now()
	f.containers[key] = c
	return nil
}

func (f *fakeDocker) RemoveContainer(_ context.Context, id string, o dockerapi.RemoveContainerOptions) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveContainer"); err != nil {
		return err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return dkAPIError(404, "No such container: %s", id)
	}
	if f.containers[key].State.Running && !o.Force {
		return dkAPIError(409, "cannot remove container %s: container is running", id)
	}
	delete(f.containers, key)
	return nil
}

func (f *fakeDocker) ListImages(_ context.Context, o dockerapi.ListImagesOptions) ([]dockerapi.ImageSummary, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListImages"); err != nil {
		return nil, err
	}
	for _, key := range slices.Sorted(maps.Keys(o.Filters)) {
		if key != "dangling" {
			return nil, fmt.Errorf("fake: unsupported filter %q", key)
		}
	}
	var out []dockerapi.ImageSummary
	for _, id := range slices.Sorted(maps.Keys(f.images)) {
		i := f.images[id]
		if vals := o.Filters["dangling"]; len(vals) > 0 && (len(i.RepoTags) == 0) != (vals[0] == "true") {
			continue
		}
		out = append(out, f.cloneImage(i))
	}
	return out, nil
}

func (f *fakeDocker) InspectImage(_ context.Context, ref string) (dockerapi.ImageInspect, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("InspectImage"); err != nil {
		return dockerapi.ImageInspect{}, err
	}
	id, ok := f.findImage(ref)
	if !ok {
		return dockerapi.ImageInspect{}, dkAPIError(404, "No such image: %s", ref)
	}
	i := f.images[id]
	return dockerapi.ImageInspect{
		ID:          i.ID,
		RepoTags:    slices.Clone(i.RepoTags),
		RepoDigests: slices.Clone(i.RepoDigests),
		Created:     time.Unix(i.Created, 0).UTC().Format(time.RFC3339Nano),
		Size:        i.Size,
	}, nil
}

func (f *fakeDocker) PullImage(_ context.Context, ref string) (pullStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("PullImage"); err != nil {
		return nil, err
	}
	msgs, ok := f.pulls[ref]
	if !ok {
		return nil, dkAPIError(404, "pull access denied for %s, repository does not exist", ref)
	}
	tag := ref
	if !strings.Contains(ref, "@") && !strings.Contains(ref[strings.LastIndex(ref, "/")+1:], ":") {
		tag = ref + ":latest"
	}
	return &dkPullStream{msgs: slices.Clone(msgs), finish: func() {
		f.mu.Lock()
		defer f.mu.Unlock()
		for _, i := range f.images {
			if slices.Contains(i.RepoTags, tag) {
				return
			}
		}
		img := dkImage(tag, 1<<20)
		f.images[img.ID] = img
	}}, nil
}

func (f *fakeDocker) RemoveImage(_ context.Context, ref string, o dockerapi.RemoveImageOptions) ([]dockerapi.ImageDeleteItem, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveImage"); err != nil {
		return nil, err
	}
	id, ok := f.findImage(ref)
	if !ok {
		return nil, dkAPIError(404, "No such image: %s", ref)
	}
	i := f.images[id]
	if !o.Force && f.imageInUse(id) {
		return nil, dkAPIError(409, "conflict: unable to delete %s: image is being used by a container", ref)
	}
	if len(i.RepoTags) > 1 {
		for n, tag := range i.RepoTags {
			if tag == ref || tag == ref+":latest" {
				i.RepoTags = slices.Delete(slices.Clone(i.RepoTags), n, n+1)
				f.images[id] = i
				return []dockerapi.ImageDeleteItem{{Untagged: ref}}, nil
			}
		}
	}
	var items []dockerapi.ImageDeleteItem
	for _, tag := range i.RepoTags {
		items = append(items, dockerapi.ImageDeleteItem{Untagged: tag})
	}
	items = append(items, dockerapi.ImageDeleteItem{Deleted: id})
	delete(f.images, id)
	return items, nil
}

func (f *fakeDocker) ListNetworks(_ context.Context, filters dockerapi.Filters) ([]dockerapi.Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListNetworks"); err != nil {
		return nil, err
	}
	if _, err := dkMatchFilters(filters, nil, ""); err != nil {
		return nil, err
	}
	var out []dockerapi.Network
	for _, n := range f.networks {
		if ok, _ := dkMatchFilters(filters, n.Labels, n.Name); ok {
			out = append(out, f.cloneNetwork(n))
		}
	}
	slices.SortFunc(out, func(a, b dockerapi.Network) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (f *fakeDocker) InspectNetwork(_ context.Context, id string) (dockerapi.Network, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	key, ok := f.findNetwork(id)
	if !ok {
		return dockerapi.Network{}, dkAPIError(404, "network %s not found", id)
	}
	n := f.cloneNetwork(f.networks[key])
	for _, c := range f.containers {
		epKey, attached := f.endpointOn(c, key)
		if !attached {
			continue
		}
		if n.Containers == nil {
			n.Containers = map[string]dockerapi.NetworkContainer{}
		}
		n.Containers[c.ID] = dockerapi.NetworkContainer{
			Name:       strings.TrimPrefix(c.Name, "/"),
			EndpointID: c.NetworkSettings.Networks[epKey].EndpointID,
		}
	}
	return n, nil
}

func (f *fakeDocker) CreateNetwork(_ context.Context, req dockerapi.NetworkCreate) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreateNetwork"); err != nil {
		return "", err
	}
	for _, n := range f.networks {
		if n.Name == req.Name {
			return "", dkAPIError(409, "network with name %s already exists", req.Name)
		}
	}
	f.counter++
	n := dockerapi.Network{
		ID:         dkID(fmt.Sprintf("network:%s#%d", req.Name, f.counter)),
		Name:       req.Name,
		Created:    f.now(),
		Scope:      "local",
		Driver:     req.Driver,
		EnableIPv6: req.EnableIPv6 != nil && *req.EnableIPv6,
		Internal:   req.Internal,
		Attachable: req.Attachable,
		Options:    req.Options,
		Labels:     req.Labels,
	}
	if n.Driver == "" {
		n.Driver = "bridge"
	}
	if req.IPAM != nil {
		n.IPAM = *req.IPAM
	}
	f.networks[n.ID] = f.cloneNetwork(n)
	return n.ID, nil
}

func (f *fakeDocker) RemoveNetwork(_ context.Context, id string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveNetwork"); err != nil {
		return err
	}
	key, ok := f.findNetwork(id)
	if !ok {
		return dkAPIError(404, "network %s not found", id)
	}
	for _, c := range f.containers {
		if _, attached := f.endpointOn(c, key); attached {
			return dkAPIError(409, "error while removing network: network %s has active endpoints", id)
		}
	}
	delete(f.networks, key)
	return nil
}

func (f *fakeDocker) ConnectNetwork(_ context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ConnectNetwork"); err != nil {
		return err
	}
	netID, ok := f.findNetwork(network)
	if !ok {
		return dkAPIError(404, "network %s not found", network)
	}
	key, ok := f.findContainer(container)
	if !ok {
		return dkAPIError(404, "No such container: %s", container)
	}
	c := f.containers[key]
	if _, attached := f.endpointOn(c, netID); attached {
		return dkAPIError(403, "container %s is already connected to network %s", container, network)
	}
	f.counter++
	e := dockerapi.EndpointSettings{NetworkID: netID, EndpointID: dkID(fmt.Sprintf("endpoint:%d", f.counter))}
	if ep != nil {
		e.Aliases = slices.Clone(ep.Aliases)
		e.IPAMConfig = dkCloneEndpoint(ep).IPAMConfig
	}
	if c.NetworkSettings.Networks == nil {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
	}
	c.NetworkSettings.Networks[f.networks[netID].Name] = &e
	f.containers[key] = c
	return nil
}

func (f *fakeDocker) DisconnectNetwork(_ context.Context, network, container string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("DisconnectNetwork"); err != nil {
		return err
	}
	netID, ok := f.findNetwork(network)
	if !ok {
		return dkAPIError(404, "network %s not found", network)
	}
	key, ok := f.findContainer(container)
	if !ok {
		return dkAPIError(404, "No such container: %s", container)
	}
	c := f.containers[key]
	epKey, attached := f.endpointOn(c, netID)
	if !attached {
		return dkAPIError(404, "container %s is not connected to network %s", container, network)
	}
	delete(c.NetworkSettings.Networks, epKey)
	f.containers[key] = c
	return nil
}

func (f *fakeDocker) ListVolumes(_ context.Context, filters dockerapi.Filters) ([]dockerapi.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("ListVolumes"); err != nil {
		return nil, err
	}
	if _, err := dkMatchFilters(filters, nil, ""); err != nil {
		return nil, err
	}
	var out []dockerapi.Volume
	for _, name := range slices.Sorted(maps.Keys(f.volumes)) {
		v := f.volumes[name]
		if ok, _ := dkMatchFilters(filters, v.Labels, v.Name); ok {
			out = append(out, f.cloneVolume(v))
		}
	}
	return out, nil
}

func (f *fakeDocker) InspectVolume(_ context.Context, name string) (dockerapi.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("InspectVolume"); err != nil {
		return dockerapi.Volume{}, err
	}
	v, ok := f.volumes[name]
	if !ok {
		return dockerapi.Volume{}, dkAPIError(404, "get %s: no such volume", name)
	}
	return f.cloneVolume(v), nil
}

func (f *fakeDocker) CreateVolume(_ context.Context, req dockerapi.VolumeCreate) (dockerapi.Volume, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("CreateVolume"); err != nil {
		return dockerapi.Volume{}, err
	}
	if v, ok := f.volumes[req.Name]; ok {
		return f.cloneVolume(v), nil
	}
	v := dkVolume(req.Name, req.Labels)
	v.CreatedAt = f.now()
	if req.Driver != "" {
		v.Driver = req.Driver
	}
	f.volumes[v.Name] = v
	return f.cloneVolume(v), nil
}

func (f *fakeDocker) RemoveVolume(_ context.Context, name string, _ bool) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveVolume"); err != nil {
		return err
	}
	if _, ok := f.volumes[name]; !ok {
		return dkAPIError(404, "get %s: no such volume", name)
	}
	for _, c := range f.containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" && m.Name == name {
				return dkAPIError(409, "remove %s: volume is in use", name)
			}
		}
	}
	delete(f.volumes, name)
	return nil
}

func (f *fakeDocker) Logs(ctx context.Context, id string, o dockerapi.LogsOptions) (logStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Logs"); err != nil {
		return nil, err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return nil, dkAPIError(404, "No such container: %s", id)
	}
	var frames []dockerapi.LogFrame
	for _, fr := range f.logs[key] {
		if (fr.Stream == "stderr" && o.Stderr) || (fr.Stream != "stderr" && o.Stdout) {
			frames = append(frames, dockerapi.LogFrame{Stream: fr.Stream, Data: bytes.Clone(fr.Data)})
		}
	}
	if n, err := strconv.Atoi(o.Tail); err == nil && n >= 0 && n < len(frames) {
		frames = frames[len(frames)-n:]
	}
	return newDKStream(ctx, frames, o.Follow), nil
}

func (f *fakeDocker) Stats(ctx context.Context, id string, stream bool) (statsStream, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("Stats"); err != nil {
		return nil, err
	}
	key, ok := f.findContainer(id)
	if !ok {
		return nil, dkAPIError(404, "No such container: %s", id)
	}
	samples := slices.Clone(f.stats[key])
	if !stream && len(samples) > 1 {
		samples = samples[:1]
	}
	return newDKStream(ctx, samples, stream), nil
}

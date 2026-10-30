package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// rsDocker answers the calls the four list resources make from its fields,
// so these tests do not depend on how fakeDocker renders summaries. Stats
// answers one sample per container id.
type rsDocker struct {
	dockerAPI
	containers []dockerapi.ContainerSummary
	images     []dockerapi.ImageSummary
	networks   []dockerapi.Network // Containers is returned by InspectNetwork only
	volumes    []dockerapi.Volume
	stats      map[string]dockerapi.Stats
	statsErr   map[string]error // by container id
	inspectErr map[string]error // by network id
	failList   map[string]error // by method name

	mu       sync.Mutex
	listed   []bool // the All option of each ListContainers call
	statsFor []string
}

func (d *rsDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.mu.Lock()
	d.listed = append(d.listed, o.All)
	d.mu.Unlock()
	if err := d.failList["ListContainers"]; err != nil {
		return nil, err
	}
	return slices.Clone(d.containers), nil
}

func (d *rsDocker) ListImages(context.Context, dockerapi.ListImagesOptions) ([]dockerapi.ImageSummary, error) {
	if err := d.failList["ListImages"]; err != nil {
		return nil, err
	}
	return slices.Clone(d.images), nil
}

func (d *rsDocker) ListNetworks(context.Context, dockerapi.Filters) ([]dockerapi.Network, error) {
	if err := d.failList["ListNetworks"]; err != nil {
		return nil, err
	}
	out := slices.Clone(d.networks)
	for i := range out {
		out[i].Containers = nil
	}
	return out, nil
}

func (d *rsDocker) InspectNetwork(_ context.Context, id string) (dockerapi.Network, error) {
	if err := d.inspectErr[id]; err != nil {
		return dockerapi.Network{}, err
	}
	for _, n := range d.networks {
		if n.ID == id {
			return n, nil
		}
	}
	return dockerapi.Network{}, &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"}
}

func (d *rsDocker) ListVolumes(context.Context, dockerapi.Filters) ([]dockerapi.Volume, error) {
	if err := d.failList["ListVolumes"]; err != nil {
		return nil, err
	}
	return slices.Clone(d.volumes), nil
}

func (d *rsDocker) Stats(_ context.Context, id string, stream bool) (statsStream, error) {
	d.mu.Lock()
	d.statsFor = append(d.statsFor, fmt.Sprintf("%s stream=%t", id, stream))
	d.mu.Unlock()
	if err := d.statsErr[id]; err != nil {
		return nil, err
	}
	return &rsStream{s: d.stats[id]}, nil
}

type rsStream struct {
	s      dockerapi.Stats
	closed bool
}

func (r *rsStream) Next() (dockerapi.Stats, error) {
	if r.closed {
		return dockerapi.Stats{}, io.EOF
	}
	return r.s, nil
}

func (r *rsStream) Close() error { r.closed = true; return nil }

func rsRead(t *testing.T, d *rsDocker, def func(dk) core.ResourceDef, out any) (json.RawMessage, error) {
	t.Helper()
	h := newDKHost(t)
	k := h.dk()
	k.d.docker = d
	raw, err := def(k).Read(t.Context(), h.env(""))
	if err == nil && out != nil {
		if uerr := json.Unmarshal(raw, out); uerr != nil {
			t.Fatalf("decode %s: %v", raw, uerr)
		}
	}
	return raw, err
}

func rsSummary(id, name, imageID, state string, labels map[string]string) dockerapi.ContainerSummary {
	return dockerapi.ContainerSummary{ID: id, Names: []string{"/" + name}, Image: "img-" + name, ImageID: imageID,
		State: state, Status: state + " status", Created: 1_760_000_000, Labels: labels}
}

// rsSample is a stats sample with a CPU delta of 1e9 against a system delta
// of 10e9 on 4 CPUs (40 %), and 100 MiB of memory of which 20 MiB is
// inactive file cache.
func rsSample() dockerapi.Stats {
	return dockerapi.Stats{
		CPUStats:    dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 2_000_000_000}, SystemCPUUsage: 20_000_000_000, OnlineCPUs: 4},
		PreCPUStats: dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: 1_000_000_000}, SystemCPUUsage: 10_000_000_000, OnlineCPUs: 4},
		MemoryStats: dockerapi.MemoryStats{Usage: 100 << 20, Limit: 1 << 30, Stats: map[string]uint64{"inactive_file": 20 << 20}},
	}
}

func TestRsResourceNames(t *testing.T) {
	k := newDKHost(t).dk()
	for name, def := range map[string]core.ResourceDef{
		"docker.containers": k.containersResource(), "docker.images": k.imagesResource(),
		"docker.networks": k.networksResource(), "docker.volumes": k.volumesResource(),
	} {
		if def.Name != name || def.Read == nil || def.OutputType == nil {
			t.Errorf("resource %q: Name %q, Read nil %t, OutputType nil %t", name, def.Name, def.Read == nil, def.OutputType == nil)
		}
	}
}

func TestRsContainers(t *testing.T) {
	d := &rsDocker{
		containers: []dockerapi.ContainerSummary{
			func() dockerapi.ContainerSummary {
				c := rsSummary("id-web", "web", "sha256:aaa", "running", map[string]string{
					"com.docker.compose.project": "shop", "com.docker.compose.service": "front", "darkpanel.module": "cloudflare"})
				c.Ports = []dockerapi.PortSummary{
					{IP: "::", PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
					{PrivatePort: 443, Type: "tcp"},
					{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Type: "tcp"},
				}
				return c
			}(),
			func() dockerapi.ContainerSummary {
				c := rsSummary("id-db", "db", "sha256:bbb", "exited", nil)
				c.Names = []string{"/app/db", "/db"} // a legacy link name first
				return c
			}(),
			rsSummary("id-cache", "cache", "sha256:aaa", "running", nil),
		},
		stats:    map[string]dockerapi.Stats{"id-web": rsSample()},
		statsErr: map[string]error{"id-cache": errors.New("stats failed")},
	}
	var got ContainerList
	if _, err := rsRead(t, d, dk.containersResource, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.listed, []bool{true}) {
		t.Errorf("ListContainers All options = %v, want one call with All", d.listed)
	}
	slices.Sort(d.statsFor)
	if want := []string{"id-cache stream=false", "id-web stream=false"}; !slices.Equal(d.statsFor, want) {
		t.Errorf("stats reads = %v, want %v (running containers only, one sample each)", d.statsFor, want)
	}
	created := time.Unix(1_760_000_000, 0).UTC()
	want := []Container{
		{ID: "id-cache", Name: "cache", Image: "img-cache", ImageID: "sha256:aaa", State: "running", Status: "running status",
			Created: created, Ports: []Port{}},
		{ID: "id-db", Name: "db", Image: "img-db", ImageID: "sha256:bbb", State: "exited", Status: "exited status",
			Created: created, Ports: []Port{}},
		{ID: "id-web", Name: "web", Image: "img-web", ImageID: "sha256:aaa", State: "running", Status: "running status",
			Created: created,
			Ports: []Port{
				{IP: "0.0.0.0", PrivatePort: 80, PublicPort: 8080, Proto: "tcp"},
				{IP: "::", PrivatePort: 80, PublicPort: 8080, Proto: "tcp"},
				{PrivatePort: 443, Proto: "tcp"},
			},
			Labels:  map[string]string{"com.docker.compose.project": "shop", "com.docker.compose.service": "front", "darkpanel.module": "cloudflare"},
			Project: "shop", Service: "front", Owner: "cloudflare",
			Usage: &Usage{CPUPercent: 40, MemUsage: 80 << 20, MemLimit: 1 << 30}},
	}
	if !reflect.DeepEqual(got.Containers, want) {
		t.Errorf("containers\n got %+v\nwant %+v", got.Containers, want)
	}
}

func TestRsContainersEncodeEmptyListsAsArrays(t *testing.T) {
	d := &rsDocker{containers: []dockerapi.ContainerSummary{rsSummary("id-solo", "solo", "sha256:aaa", "exited", nil)}}
	raw, err := rsRead(t, d, dk.containersResource, nil)
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Containers []map[string]json.RawMessage `json:"containers"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || len(doc.Containers) != 1 {
		t.Fatalf("decode %s: %v", raw, err)
	}
	c := doc.Containers[0]
	if string(c["ports"]) != "[]" {
		t.Errorf("ports = %s, want []", c["ports"])
	}
	for _, absent := range []string{"labels", "usage", "project", "service", "owner"} {
		if _, ok := c[absent]; ok {
			t.Errorf("member %q present for a container without it", absent)
		}
	}
	empty, err := rsRead(t, &rsDocker{}, dk.containersResource, nil)
	if err != nil || string(empty) != `{"containers":[]}` {
		t.Errorf("no containers: %s, %v; want {\"containers\":[]}", empty, err)
	}
}

func TestRsUsageOf(t *testing.T) {
	cpu := func(total, pre, system, preSystem uint64, online uint32) dockerapi.Stats {
		return dockerapi.Stats{
			CPUStats:    dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: total}, SystemCPUUsage: system, OnlineCPUs: online},
			PreCPUStats: dockerapi.CPUStats{CPUUsage: dockerapi.CPUUsage{TotalUsage: pre}, SystemCPUUsage: preSystem},
		}
	}
	mem := func(usage uint64, stats map[string]uint64) dockerapi.Stats {
		return dockerapi.Stats{MemoryStats: dockerapi.MemoryStats{Usage: usage, Limit: 1 << 30, Stats: stats}}
	}
	for _, tc := range []struct {
		name string
		in   dockerapi.Stats
		want Usage
	}{
		{"cpu delta ratio times online cpus", cpu(2e9, 1e9, 20e9, 10e9, 4), Usage{CPUPercent: 40}},
		{"zero online cpus count as one", cpu(2e9, 1e9, 20e9, 10e9, 0), Usage{CPUPercent: 10}},
		{"system counter unchanged", cpu(2e9, 1e9, 10e9, 10e9, 4), Usage{}},
		{"cpu counter went backwards", cpu(1e9, 2e9, 20e9, 10e9, 4), Usage{}},
		{"inactive file is subtracted", mem(100<<20, map[string]uint64{"inactive_file": 20 << 20}), Usage{MemUsage: 80 << 20, MemLimit: 1 << 30}},
		{"inactive file above usage is ignored", mem(10<<20, map[string]uint64{"inactive_file": 20 << 20}), Usage{MemUsage: 10 << 20, MemLimit: 1 << 30}},
		{"no inactive file", mem(10<<20, nil), Usage{MemUsage: 10 << 20, MemLimit: 1 << 30}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := usageOf(tc.in)
			if math.Abs(got.CPUPercent-tc.want.CPUPercent) > 1e-9 || got.MemUsage != tc.want.MemUsage || got.MemLimit != tc.want.MemLimit {
				t.Errorf("usageOf = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestRsImages(t *testing.T) {
	d := &rsDocker{
		containers: []dockerapi.ContainerSummary{
			rsSummary("id-web", "web", "sha256:aaa", "running", nil),
			rsSummary("id-cache", "cache", "sha256:aaa", "exited", nil),
		},
		images: []dockerapi.ImageSummary{
			{ID: "sha256:ccc", RepoDigests: []string{"busybox@sha256:x"}, Created: 1_760_000_000, Size: 3},
			{ID: "sha256:bbb", RepoTags: []string{"<none>:<none>"}, RepoDigests: []string{"<none>@<none>"}, Created: 1_760_000_000, Size: 2},
			{ID: "sha256:aaa", RepoTags: []string{"nginx:latest", "nginx:1.27"}, RepoDigests: []string{"nginx@sha256:d2", "nginx@sha256:d1"},
				Created: 1_760_000_000, Size: 100},
			{ID: "sha256:ddd", RepoTags: []string{"alpine:3"}, Created: 1_760_000_000, Size: 7},
		},
	}
	var got ImageList
	if _, err := rsRead(t, d, dk.imagesResource, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.listed, []bool{true}) {
		t.Errorf("ListContainers All options = %v, want one call with All", d.listed)
	}
	created := time.Unix(1_760_000_000, 0).UTC()
	want := []Image{
		{ID: "sha256:ddd", Tags: []string{"alpine:3"}, Digests: []string{}, Size: 7, Created: created, Containers: []string{}},
		{ID: "sha256:aaa", Tags: []string{"nginx:1.27", "nginx:latest"}, Digests: []string{"nginx@sha256:d1", "nginx@sha256:d2"},
			Size: 100, Created: created, Containers: []string{"cache", "web"}},
		{ID: "sha256:bbb", Tags: []string{}, Digests: []string{}, Size: 2, Created: created, Containers: []string{}, Dangling: true},
		{ID: "sha256:ccc", Tags: []string{}, Digests: []string{"busybox@sha256:x"}, Size: 3, Created: created, Containers: []string{}},
	}
	if !reflect.DeepEqual(got.Images, want) {
		t.Errorf("images\n got %+v\nwant %+v", got.Images, want)
	}
}

func TestRsNetworks(t *testing.T) {
	d := &rsDocker{
		networks: []dockerapi.Network{
			{ID: "net-b", Name: "bridge", Driver: "bridge", Scope: "local", Created: "2026-10-01T00:00:00.123456789Z",
				IPAM: dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("172.17.0.0/16")}}}},
			{ID: "net-a", Name: "app_default", Driver: "bridge", Scope: "local", Internal: true, EnableIPv6: true,
				Created: "2026-10-02T00:00:00Z", Labels: map[string]string{"darkpanel.module": "panel"},
				IPAM:       dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("fd00::/64")}, {Subnet: netip.MustParsePrefix("172.18.0.0/16")}}},
				Containers: map[string]dockerapi.NetworkContainer{"c2": {Name: "web"}, "c1": {Name: "cache"}}},
			{ID: "net-gone", Name: "removed_meanwhile"},
		},
		inspectErr: map[string]error{"net-gone": &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"}},
	}
	var got NetworkList
	if _, err := rsRead(t, d, dk.networksResource, &got); err != nil {
		t.Fatal(err)
	}
	want := []Network{
		{ID: "net-a", Name: "app_default", Driver: "bridge", Scope: "local", Internal: true, IPv6: true,
			Subnets: []string{"172.18.0.0/16", "fd00::/64"}, Containers: []string{"cache", "web"}, Owner: "panel",
			Created: time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)},
		{ID: "net-b", Name: "bridge", Driver: "bridge", Scope: "local", Subnets: []string{"172.17.0.0/16"}, Containers: []string{},
			Created: time.Date(2026, 10, 1, 0, 0, 0, 123456789, time.UTC)},
	}
	if !reflect.DeepEqual(got.Networks, want) {
		t.Errorf("networks\n got %+v\nwant %+v", got.Networks, want)
	}

	boom := errors.New("boom")
	d.inspectErr["net-b"] = boom
	if _, err := rsRead(t, d, dk.networksResource, nil); !errors.Is(err, boom) {
		t.Errorf("inspect failure other than 404: err = %v, want it to wrap the engine error", err)
	}
}

func TestRsVolumes(t *testing.T) {
	d := &rsDocker{
		containers: func() []dockerapi.ContainerSummary {
			web := rsSummary("id-web", "web", "sha256:aaa", "running", nil)
			web.Mounts = []dockerapi.MountPoint{{Type: "volume", Name: "data", Destination: "/data"}}
			cache := rsSummary("id-cache", "cache", "sha256:aaa", "exited", nil)
			cache.Mounts = []dockerapi.MountPoint{{Type: "volume", Name: "data"}, {Type: "bind", Source: "/srv/data", Destination: "/srv"}}
			return []dockerapi.ContainerSummary{web, cache}
		}(),
		volumes: []dockerapi.Volume{
			{Name: "unused", Driver: "local", Mountpoint: "/var/lib/docker/volumes/unused/_data", CreatedAt: "2026-09-02T00:00:00Z"},
			{Name: "data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/data/_data", CreatedAt: "2026-09-01T00:00:00Z",
				Labels: map[string]string{"darkpanel.module": "npm"}},
		},
	}
	var got VolumeList
	if _, err := rsRead(t, d, dk.volumesResource, &got); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(d.listed, []bool{true}) {
		t.Errorf("ListContainers All options = %v, want one call with All", d.listed)
	}
	want := []Volume{
		{Name: "data", Driver: "local", Mountpoint: "/var/lib/docker/volumes/data/_data", Created: time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC),
			Labels: map[string]string{"darkpanel.module": "npm"}, Containers: []string{"cache", "web"}, Owner: "npm"},
		{Name: "unused", Driver: "local", Mountpoint: "/var/lib/docker/volumes/unused/_data", Created: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
			Containers: []string{}},
	}
	if !reflect.DeepEqual(got.Volumes, want) {
		t.Errorf("volumes\n got %+v\nwant %+v", got.Volumes, want)
	}
}

func TestRsListFailures(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		method string
		def    func(dk) core.ResourceDef
	}{
		{"ListContainers", dk.containersResource}, {"ListImages", dk.imagesResource},
		{"ListNetworks", dk.networksResource}, {"ListVolumes", dk.volumesResource},
	} {
		t.Run(tc.method, func(t *testing.T) {
			_, err := rsRead(t, &rsDocker{failList: map[string]error{tc.method: fmt.Errorf("get: %w", dockerapi.ErrUnavailable)}}, tc.def, nil)
			if e := errcode.From(err); e == nil || e.Code != errcode.DockerEngineUnavailable {
				t.Errorf("unreachable daemon: err = %v, want code %s", err, errcode.DockerEngineUnavailable)
			}
			_, err = rsRead(t, &rsDocker{failList: map[string]error{tc.method: boom}}, tc.def, nil)
			if !errors.Is(err, boom) {
				t.Errorf("other failure: err = %v, want it to wrap the engine error", err)
			}
		})
	}
}

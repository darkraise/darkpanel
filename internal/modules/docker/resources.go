package docker

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	resSampleTimeout     = 5 * time.Second
	resSampleConcurrency = 8
	resComposeProject    = "com.docker.compose.project"
	resComposeService    = "com.docker.compose.service"
)

func (k dk) containersResource() core.ResourceDef {
	return core.DefineResource(ID+".containers", k.containers)
}

func (k dk) imagesResource() core.ResourceDef {
	return core.DefineResource(ID+".images", k.images)
}

func (k dk) networksResource() core.ResourceDef {
	return core.DefineResource(ID+".networks", k.networks)
}

func (k dk) volumesResource() core.ResourceDef {
	return core.DefineResource(ID+".volumes", k.volumes)
}

func (k dk) containers(ctx context.Context, _ core.Env) (ContainerList, error) {
	summaries, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return ContainerList{}, resError("list containers", err)
	}
	out := make([]Container, 0, len(summaries))
	for _, s := range summaries {
		ports := make([]Port, 0, len(s.Ports))
		for _, p := range s.Ports {
			ports = append(ports, Port{IP: p.IP, PrivatePort: p.PrivatePort, PublicPort: p.PublicPort, Proto: p.Type})
		}
		slices.SortFunc(ports, func(a, b Port) int {
			return cmp.Or(cmp.Compare(a.PrivatePort, b.PrivatePort), cmp.Compare(a.PublicPort, b.PublicPort),
				strings.Compare(a.Proto, b.Proto), strings.Compare(a.IP, b.IP))
		})
		out = append(out, Container{
			ID: s.ID, Name: resContainerName(s.Names), Image: s.Image, ImageID: s.ImageID,
			State: s.State, Status: s.Status, Created: time.Unix(s.Created, 0).UTC(), Ports: ports,
			Labels: resLabels(s.Labels), Project: s.Labels[resComposeProject], Service: s.Labels[resComposeService],
			Owner: s.Labels[core.LabelModule],
		})
	}
	slices.SortFunc(out, func(a, b Container) int { return strings.Compare(a.Name, b.Name) })

	sem := make(chan struct{}, resSampleConcurrency)
	var wg sync.WaitGroup
	for i := range out {
		if out[i].State != "running" {
			continue
		}
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			out[i].Usage = k.resSample(ctx, out[i].ID)
		})
	}
	wg.Wait()
	return ContainerList{Containers: out}, nil
}

// resSample reads one stats sample; a container that stops or cannot be read
// meanwhile just has no usage.
func (k dk) resSample(ctx context.Context, id string) *Usage {
	ctx, cancel := context.WithTimeout(ctx, resSampleTimeout)
	defer cancel()
	stream, err := k.d.docker.Stats(ctx, id, false)
	if err != nil {
		return nil
	}
	defer func() { _ = stream.Close() }()
	sample, err := stream.Next()
	if err != nil {
		return nil
	}
	u := usageOf(sample)
	return &u
}

// usageOf renders a sample the way docker stats does: the page cache the
// kernel can drop (inactive_file) is not counted as memory use.
func usageOf(s dockerapi.Stats) Usage {
	u := Usage{MemUsage: s.MemoryStats.Usage, MemLimit: s.MemoryStats.Limit}
	if inactive, ok := s.MemoryStats.Stats["inactive_file"]; ok && inactive < u.MemUsage {
		u.MemUsage -= inactive
	}
	cur, pre := s.CPUStats, s.PreCPUStats
	if cur.CPUUsage.TotalUsage > pre.CPUUsage.TotalUsage && cur.SystemCPUUsage > pre.SystemCPUUsage {
		cpuDelta := float64(cur.CPUUsage.TotalUsage - pre.CPUUsage.TotalUsage)
		systemDelta := float64(cur.SystemCPUUsage - pre.SystemCPUUsage)
		u.CPUPercent = cpuDelta / systemDelta * float64(max(cur.OnlineCPUs, 1)) * 100
	}
	return u
}

func (k dk) images(ctx context.Context, _ core.Env) (ImageList, error) {
	summaries, err := k.d.docker.ListImages(ctx, dockerapi.ListImagesOptions{})
	if err != nil {
		return ImageList{}, resError("list images", err)
	}
	containers, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return ImageList{}, resError("list containers", err)
	}
	usedBy := map[string][]string{}
	for _, c := range containers {
		usedBy[c.ImageID] = append(usedBy[c.ImageID], resContainerName(c.Names))
	}
	out := make([]Image, 0, len(summaries))
	for _, s := range summaries {
		tags := resSet(resWithout(s.RepoTags, "<none>:<none>"))
		digests := resSet(resWithout(s.RepoDigests, "<none>@<none>"))
		out = append(out, Image{
			ID: s.ID, Tags: tags, Digests: digests, Size: s.Size, Created: time.Unix(s.Created, 0).UTC(),
			Containers: resSet(usedBy[s.ID]), Dangling: len(tags) == 0 && len(digests) == 0,
		})
	}
	slices.SortFunc(out, func(a, b Image) int {
		if (len(a.Tags) == 0) != (len(b.Tags) == 0) {
			if len(a.Tags) == 0 {
				return 1
			}
			return -1
		}
		if len(a.Tags) > 0 {
			if c := strings.Compare(a.Tags[0], b.Tags[0]); c != 0 {
				return c
			}
		}
		return strings.Compare(a.ID, b.ID)
	})
	return ImageList{Images: out}, nil
}

func (k dk) networks(ctx context.Context, _ core.Env) (NetworkList, error) {
	listed, err := k.d.docker.ListNetworks(ctx, nil)
	if err != nil {
		return NetworkList{}, resError("list networks", err)
	}
	out := make([]Network, 0, len(listed))
	for _, l := range listed {
		n, err := k.d.docker.InspectNetwork(ctx, l.ID)
		if dockerapi.IsNotFound(err) {
			continue
		}
		if err != nil {
			return NetworkList{}, resError("inspect network", err)
		}
		subnets := []string{}
		for _, c := range n.IPAM.Config {
			if c.Subnet.IsValid() {
				subnets = append(subnets, c.Subnet.String())
			}
		}
		names := make([]string, 0, len(n.Containers))
		for _, c := range n.Containers {
			names = append(names, c.Name)
		}
		slices.Sort(subnets)
		slices.Sort(names)
		out = append(out, Network{
			ID: n.ID, Name: n.Name, Driver: n.Driver, Scope: n.Scope, Internal: n.Internal, IPv6: n.EnableIPv6,
			Subnets: subnets, Containers: names, Owner: n.Labels[core.LabelModule], Created: resTime(n.Created),
		})
	}
	slices.SortFunc(out, func(a, b Network) int { return strings.Compare(a.Name, b.Name) })
	return NetworkList{Networks: out}, nil
}

func (k dk) volumes(ctx context.Context, _ core.Env) (VolumeList, error) {
	listed, err := k.d.docker.ListVolumes(ctx, nil)
	if err != nil {
		return VolumeList{}, resError("list volumes", err)
	}
	containers, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return VolumeList{}, resError("list containers", err)
	}
	usedBy := map[string][]string{}
	for _, c := range containers {
		for _, m := range c.Mounts {
			if m.Type == "volume" {
				usedBy[m.Name] = append(usedBy[m.Name], resContainerName(c.Names))
			}
		}
	}
	out := make([]Volume, 0, len(listed))
	for _, v := range listed {
		out = append(out, Volume{
			Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Created: resTime(v.CreatedAt),
			Labels: resLabels(v.Labels), Containers: resSet(usedBy[v.Name]), Owner: v.Labels[core.LabelModule],
		})
	}
	slices.SortFunc(out, func(a, b Volume) int { return strings.Compare(a.Name, b.Name) })
	return VolumeList{Volumes: out}, nil
}

// resError maps an Engine failure of a list call.
func resError(call string, err error) error {
	if errors.Is(err, dockerapi.ErrUnavailable) {
		return errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine cannot be reached")
	}
	return fmt.Errorf("docker: %s: %w", call, err)
}

// resContainerName prefers the container's own name over the "/other/alias"
// names Docker lists for legacy links.
func resContainerName(names []string) string {
	for _, n := range names {
		if !strings.Contains(strings.TrimPrefix(n, "/"), "/") {
			return strings.TrimPrefix(n, "/")
		}
	}
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func resLabels(labels map[string]string) map[string]string {
	if len(labels) == 0 {
		return nil
	}
	return labels
}

func resWithout(values []string, placeholder string) []string {
	out := make([]string, 0, len(values))
	for _, v := range values {
		if v != placeholder {
			out = append(out, v)
		}
	}
	return out
}

// resSet returns a sorted copy without duplicates; it is never nil, so an
// empty set encodes as [].
func resSet(values []string) []string {
	out := slices.Clone(values)
	if out == nil {
		out = []string{}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func resTime(s string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

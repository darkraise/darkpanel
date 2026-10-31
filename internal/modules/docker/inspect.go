package docker

import (
	"cmp"
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) containerInspect() core.ActionDef {
	return core.DefineRead[ContainerInput, ContainerDetail](core.ActionSpec{
		Name: ID + ".container.inspect", Summary: "Show a container's configuration and state.", Version: 1,
		Side: core.SideAgent,
	}, containerInspectHandler{k: k})
}

// containerInspectHandler reads one container. Its answer never carries an
// environment value: those are served only by reveal-env, which is audited.
type containerInspectHandler struct{ k dk }

func (h containerInspectHandler) Read(ctx context.Context, _ core.Env, in ContainerInput) (ContainerDetail, error) {
	if in.Container == "" {
		return ContainerDetail{}, inspectInvalid("/container", "container is required")
	}
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return ContainerDetail{}, engineError("container", in.Container, err)
	}
	return containerDetailOf(c)
}

func (k dk) containerRevealEnv() core.ActionDef {
	return core.DefineReveal[ContainerInput, RevealedEnv](core.ActionSpec{
		Name: ID + ".container.reveal-env", Summary: "Reveal a container's environment values.", Version: 1,
		Side: core.SideAgent, MinScope: core.ScopeAdmin,
	}, func(ctx context.Context, env core.Env, in ContainerInput) (core.Revealed[RevealedEnv], error) {
		if in.Container == "" {
			return core.Revealed[RevealedEnv]{}, inspectInvalid("/container", "container is required")
		}
		c, err := k.d.docker.InspectContainer(ctx, in.Container)
		if err != nil {
			return core.Revealed[RevealedEnv]{}, engineError("container", in.Container, err)
		}
		name := containerName(c)
		// A module's container holds the secrets that module manages, which
		// it alone may hand out.
		if err := refuseOwned("container", name, c.Config.Labels); err != nil {
			return core.Revealed[RevealedEnv]{}, err
		}
		return core.Revealed[RevealedEnv]{
			Identity: "environment of container " + name + ", host " + env.HostID,
			Value:    RevealedEnv{Env: envVars(c.Config.Env, false)},
		}, nil
	})
}

// inspectInvalid refuses an input member at path.
func inspectInvalid(path, message string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: message,
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// envVars splits "NAME=value" entries at their first "="; an entry without
// one is a name with an empty value. With redact, every value is the mask.
func envVars(entries []string, redact bool) []EnvVar {
	vars := make([]EnvVar, 0, len(entries))
	for _, e := range entries {
		name, value, _ := strings.Cut(e, "=")
		if redact {
			value = logfilter.Mask
		}
		vars = append(vars, EnvVar{Name: name, Value: value})
	}
	return vars
}

// containerDetailOf renders an inspected container with its environment
// values redacted.
func containerDetailOf(c dockerapi.ContainerJSON) (ContainerDetail, error) {
	created, err := time.Parse(time.RFC3339Nano, c.Created)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: created: %w", c.ID, err)
	}
	started, err := dstate.Instant(c.State.StartedAt)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: started: %w", c.ID, err)
	}
	finished, err := dstate.Instant(c.State.FinishedAt)
	if err != nil {
		return ContainerDetail{}, fmt.Errorf("docker: container %s: finished: %w", c.ID, err)
	}
	name := containerName(c)
	d := ContainerDetail{
		ID: c.ID, Name: name, Image: c.Config.Image, ImageID: c.Image, Created: created.UTC(),
		State: c.State.Status, Running: c.State.Running, StartedAt: started, FinishedAt: finished,
		ExitCode: c.State.ExitCode, RestartPolicy: c.HostConfig.RestartPolicy.Name,
		Cmd: c.Config.Cmd, Entrypoint: c.Config.Entrypoint, WorkingDir: c.Config.WorkingDir, User: c.Config.User,
		Env: envVars(c.Config.Env, true), Labels: c.Config.Labels,
		Ports: []Port{}, Mounts: make([]Mount, 0, len(c.Mounts)), Networks: []EndpointView{},
		Project: c.Config.Labels["com.docker.compose.project"], Service: c.Config.Labels["com.docker.compose.service"],
		Owner: ownerOf(c.Config.Labels),
	}
	for key, bindings := range c.NetworkSettings.Ports {
		port, proto, ok := strings.Cut(key, "/")
		private, err := strconv.ParseUint(port, 10, 16)
		if !ok || err != nil {
			continue
		}
		for _, b := range bindings {
			public, err := strconv.ParseUint(b.HostPort, 10, 16)
			if err != nil {
				continue
			}
			d.Ports = append(d.Ports, Port{IP: b.HostIP, PrivatePort: uint16(private), PublicPort: uint16(public), Proto: proto})
		}
	}
	slices.SortFunc(d.Ports, func(a, b Port) int {
		return cmp.Or(cmp.Compare(a.PrivatePort, b.PrivatePort), cmp.Compare(a.Proto, b.Proto),
			cmp.Compare(a.PublicPort, b.PublicPort), cmp.Compare(a.IP, b.IP))
	})
	for _, m := range c.Mounts {
		d.Mounts = append(d.Mounts, Mount{Type: m.Type, Name: m.Name, Source: m.Source, Destination: m.Destination, RW: m.RW})
	}
	for network, ep := range c.NetworkSettings.Networks {
		if ep == nil {
			continue
		}
		v := EndpointView{Network: network, NetworkID: ep.NetworkID, Container: name, EndpointID: ep.EndpointID, Aliases: ep.Aliases}
		if ep.IPAddress.IsValid() {
			v.IPv4 = ep.IPAddress.String()
		}
		if ep.GlobalIPv6Address.IsValid() {
			v.IPv6 = ep.GlobalIPv6Address.String()
		}
		d.Networks = append(d.Networks, v)
	}
	slices.SortFunc(d.Networks, func(a, b EndpointView) int { return cmp.Compare(a.Network, b.Network) })
	return d, nil
}

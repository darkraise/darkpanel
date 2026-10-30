package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// phaseOf is the form an effect's resource is compared in: the state an undo
// restores while the effect is being or was undone, the recorded post-state
// otherwise.
func phaseOf(e core.Effect) dstate.Phase {
	if e.State == core.EffectUndoing || e.State == core.EffectUndone {
		return dstate.Restored
	}
	return dstate.Recorded
}

// nullState is the state of an object that does not exist.
func nullState() core.StateDoc { return core.StateDoc{State: json.RawMessage("null")} }

// named is the observation of an object its identity names.
func named(state json.RawMessage) core.Observation {
	return core.Observation{Current: core.StateDoc{State: state}, Matches: 1, Exclusive: true}
}

// observe is every docker handler's Observer. It only reads the Docker Engine.
func (k dk) observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	noun, identity, ok := strings.Cut(e.Resource, ":")
	if !ok {
		return core.Observation{}, fmt.Errorf("docker: %q is not a docker resource", e.Resource)
	}
	name, creation := strings.CutPrefix(identity, "?")
	if name == "" {
		return core.Observation{}, fmt.Errorf("docker: %q names no object", e.Resource)
	}
	switch noun {
	case nounContainer:
		if creation {
			return core.Observation{}, notCreated(e.Resource)
		}
		return k.observeContainer(ctx, name, phaseOf(e))
	case nounEndpoint:
		if creation {
			return core.Observation{}, notCreated(e.Resource)
		}
		containerID, networkID, ok := strings.Cut(name, "@")
		if !ok || containerID == "" || networkID == "" {
			return core.Observation{}, fmt.Errorf("docker: %q is not <container id>@<network id>", e.Resource)
		}
		return k.observeEndpoint(ctx, containerID, networkID, phaseOf(e))
	case nounNetwork:
		if creation {
			return k.findNetworks(ctx, name)
		}
		return k.observeNetwork(ctx, name)
	case nounVolume:
		return k.observeVolume(ctx, name, creation)
	default:
		return core.Observation{}, fmt.Errorf("docker: %q is not a docker resource", e.Resource)
	}
}

// notCreated refuses a creation of an object no docker effect creates.
func notCreated(resource string) error {
	return fmt.Errorf("docker: %q is not an object docker creates", resource)
}

func (k dk) observeContainer(ctx context.Context, id string, phase dstate.Phase) (core.Observation, error) {
	c, err := k.d.docker.InspectContainer(ctx, id)
	if dockerapi.IsNotFound(err) {
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, engineError("container", id, err)
	}
	doc, err := dstate.ContainerRun(c, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("docker: render container %s: %w", id, err)
	}
	return named(doc), nil
}

// observeEndpoint reads the endpoint from the container, whose Networks map
// Docker keys by network name rather than id.
func (k dk) observeEndpoint(ctx context.Context, containerID, networkID string, phase dstate.Phase) (core.Observation, error) {
	c, err := k.d.docker.InspectContainer(ctx, containerID)
	if err != nil && !dockerapi.IsNotFound(err) {
		return core.Observation{}, engineError("container", containerID, err)
	}
	var ep *dockerapi.EndpointSettings
	if err == nil {
		for _, s := range c.NetworkSettings.Networks {
			if s != nil && s.NetworkID == networkID {
				ep = s
				break
			}
		}
	}
	doc, err := dstate.Endpoint(containerID, networkID, ep, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("docker: render endpoint %s@%s: %w", containerID, networkID, err)
	}
	return named(doc), nil
}

// networkState inspects a network, because only the inspect answer lists the
// containers that use it; found is false when the network is gone.
func (k dk) networkState(ctx context.Context, id string) (doc json.RawMessage, found bool, err error) {
	n, err := k.d.docker.InspectNetwork(ctx, id)
	if dockerapi.IsNotFound(err) {
		return nullState().State, false, nil
	}
	if err != nil {
		return nil, false, engineError("network", id, err)
	}
	doc, err = dstate.NetworkCreated(&n)
	if err != nil {
		return nil, false, fmt.Errorf("docker: render network %s: %w", id, err)
	}
	return doc, true, nil
}

func (k dk) observeNetwork(ctx context.Context, id string) (core.Observation, error) {
	doc, _, err := k.networkState(ctx, id)
	if err != nil {
		return core.Observation{}, err
	}
	return named(doc), nil
}

// findNetworks finds a creation's network by its exact name. Network names
// are not unique, so a single match is not exclusive.
func (k dk) findNetworks(ctx context.Context, name string) (core.Observation, error) {
	list, err := k.d.docker.ListNetworks(ctx, dockerapi.Filters{"name": {name}})
	if err != nil {
		return core.Observation{}, engineError("network", name, err)
	}
	var found []core.Candidate
	for _, n := range list {
		// Docker's name filter also matches networks whose name only contains name.
		if n.Name != name {
			continue
		}
		doc, ok, err := k.networkState(ctx, n.ID)
		if err != nil {
			return core.Observation{}, err
		}
		if ok {
			found = append(found, core.Candidate{Resource: networkResource(n.ID), State: core.StateDoc{State: doc}})
		}
	}
	slices.SortFunc(found, func(a, b core.Candidate) int { return strings.Compare(a.Resource, b.Resource) })
	switch len(found) {
	case 0:
		return core.Observation{Current: nullState()}, nil
	case 1:
		return core.Observation{Current: found[0].State, Resource: found[0].Resource, Matches: 1}, nil
	default:
		return core.Observation{Current: nullState(), Matches: len(found), Candidates: found}, nil
	}
}

// observeVolume reads a volume by its name, which Docker keeps unique, so a
// creation found by name is exclusive too.
func (k dk) observeVolume(ctx context.Context, name string, creation bool) (core.Observation, error) {
	v, err := k.d.docker.InspectVolume(ctx, name)
	if dockerapi.IsNotFound(err) {
		if creation {
			return core.Observation{Current: nullState()}, nil
		}
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, engineError("volume", name, err)
	}
	containers, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return core.Observation{}, engineError("volume", name, err)
	}
	unused := true
	for _, c := range containers {
		if slices.ContainsFunc(c.Mounts, func(m dockerapi.MountPoint) bool { return m.Name == v.Name }) {
			unused = false
			break
		}
	}
	doc, err := dstate.VolumeCreated(&v, unused)
	if err != nil {
		return core.Observation{}, fmt.Errorf("docker: render volume %s: %w", name, err)
	}
	obs := named(doc)
	if creation {
		obs.Resource = volumeResource(name)
	}
	return obs, nil
}

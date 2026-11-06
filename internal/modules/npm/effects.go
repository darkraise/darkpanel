package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// The resource nouns of the npm effects. A resource is "<noun>:<identity>"; a creation not yet named is
// "<noun>:?<recovery value>".
const (
	nounNetwork     = "npm.network"
	nounVolume      = "npm.volume"
	nounContainer   = "npm.container"
	nounEndpoint    = "npm.endpoint"
	nounProxyHost   = "npm.proxyhost"
	nounAccessList  = "npm.accesslist"
	nounCertificate = "npm.certificate"
	nounInstance    = "npm.instance"
	nounClaim       = "npm.claim"
)

// resourceOf is the resource of the object of noun that identity names.
func resourceOf(noun, identity string) string { return noun + ":" + identity }

// identityOf returns the part of a resource after its noun.
func identityOf(resource string) string {
	_, identity, _ := strings.Cut(resource, ":")
	return identity
}

// npmEffect is the effect of one Engine or NPM call. A creation expects the null state and is recovered by the name its
// "?" identity carries. No npm call ends with the process that makes it, and its post-state is not known before the
// call, so the effect is not Local and has no After.
func npmEffect(kind core.EffectKind, resource string, before json.RawMessage, wait time.Duration) core.Effect {
	e := core.Effect{
		Kind:       kind,
		Resource:   resource,
		CommitWait: wait,
		Before:     core.StateDoc{State: before},
	}
	if kind == core.EffectCreate {
		// The engine refuses a creation whose Before is not the null state.
		e.Before.State = json.RawMessage("null")
		if name, ok := strings.CutPrefix(identityOf(resource), "?"); ok {
			e.Recovery = &core.RecoveryID{Kind: "name", Value: name}
		}
	}
	return e
}

// refused reports whether err is an answer that proves the call committed nothing: a 4xx of the Engine or of NPM.
func refused(err error) bool {
	var answer *dockerapi.APIError
	if errors.As(err, &answer) && answer.StatusCode >= http.StatusBadRequest && answer.StatusCode < http.StatusInternalServerError {
		return true
	}
	return npmapi.IsClientError(err)
}

// run journals one call: it records e as intended, makes the call, and records it made with the state observe reads
// of the resource do names (or of e.Resource when do names none). A refusal (refused, or an error do marked Definite
// because a precondition failed before anything changed) skips the effect and returns a Definite error; any other
// failure leaves the effect intended for the engine to settle as uncertain.
func (n nm) run(ctx context.Context, env core.Env, e core.Effect, observe func(context.Context, core.Env, core.Effect) (core.Observation, error),
	do func(ctx context.Context) (resource string, err error)) error {
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return fmt.Errorf("npm: journal %s: %w", e.Resource, err)
	}
	resource, err := do(ctx)
	if err != nil {
		if !refused(err) && !core.IsDefinite(err) {
			return err
		}
		if skipErr := env.Effects.Skipped(ctx, seq); skipErr != nil {
			return errors.Join(err, fmt.Errorf("npm: journal %s: %w", e.Resource, skipErr))
		}
		return core.Definite(err)
	}
	if resource == "" {
		resource = e.Resource
	}
	if strings.HasPrefix(identityOf(resource), "?") {
		return fmt.Errorf("npm: the call that made %s did not name its object", resource)
	}
	made := e
	made.Resource = resource
	made.State = core.EffectIntended
	obs, err := observe(ctx, env, made)
	if err != nil {
		return fmt.Errorf("npm: %s is unsettled: %w", resource, err)
	}
	if err := env.Effects.Made(ctx, seq, resource, obs.Current); err != nil {
		return fmt.Errorf("npm: journal %s: %w", resource, err)
	}
	return nil
}

// nullState is the state of an object that does not exist.
func nullState() core.StateDoc { return core.StateDoc{State: json.RawMessage("null")} }

// named is the observation of an object its identity names.
func named(state json.RawMessage) core.Observation {
	return core.Observation{Current: core.StateDoc{State: state}, Matches: 1, Exclusive: true}
}

// phaseOf is the form an Engine object is compared in. A creation is always compared in the restorable form: the
// module starts a container it created, and the recorded form of the creation would then no longer match. A
// modification is compared in the recorded form until it is being or was undone.
func phaseOf(e core.Effect) dstate.Phase {
	if e.Kind == core.EffectCreate || e.State == core.EffectUndoing || e.State == core.EffectUndone {
		return dstate.Restored
	}
	return dstate.Recorded
}

// found is the observation of a creation whose recovery identity matched candidates: the null state when none did,
// the one match as the creation's object, or every match for the operator.
func found(candidates []core.Candidate) core.Observation {
	candidates = slices.Clone(candidates)
	slices.SortFunc(candidates, func(a, b core.Candidate) int { return strings.Compare(a.Resource, b.Resource) })
	switch len(candidates) {
	case 0:
		return core.Observation{Current: nullState()}
	case 1:
		return core.Observation{Current: candidates[0].State, Resource: candidates[0].Resource, Matches: 1, Exclusive: true}
	default:
		return core.Observation{Current: nullState(), Matches: len(candidates), Candidates: candidates}
	}
}

// creationOf splits a resource into its noun and the name its identity carries; creation is true for a "?" identity.
func creationOf(resource string) (noun, name string, creation bool, err error) {
	noun, identity, ok := strings.Cut(resource, ":")
	if !ok {
		return "", "", false, fmt.Errorf("npm: %q is not an npm resource", resource)
	}
	name, creation = strings.CutPrefix(identity, "?")
	if name == "" {
		return "", "", false, fmt.Errorf("npm: %q names no object", resource)
	}
	return noun, name, creation, nil
}

// observeEngine is the Observer of the module's Engine objects: networks, volumes, the container and its endpoints.
// It only reads the Engine.
func (n nm) observeEngine(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	noun, name, creation, err := creationOf(e.Resource)
	if err != nil {
		return core.Observation{}, err
	}
	switch noun {
	case nounNetwork:
		return n.observeNetwork(ctx, name, creation)
	case nounVolume:
		return n.observeVolume(ctx, name, creation)
	case nounContainer:
		return n.observeContainer(ctx, name, creation, phaseOf(e))
	case nounEndpoint:
		return n.observeEndpoint(ctx, name, creation, phaseOf(e))
	default:
		return core.Observation{}, fmt.Errorf("npm: %q is not an Engine object of the module", e.Resource)
	}
}

// networkDoc renders a network as InspectNetwork reports it, because only the inspect answer lists the containers
// that use it; ok is false when the network does not exist.
func (n nm) networkDoc(ctx context.Context, id string) (doc json.RawMessage, ok bool, err error) {
	net, err := n.d.docker.InspectNetwork(ctx, id)
	if dockerapi.IsNotFound(err) {
		return nullState().State, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("npm: inspect network %s: %w", id, engineCoded(err))
	}
	doc, err = dstate.NetworkCreated(&net)
	if err != nil {
		return nil, false, fmt.Errorf("npm: render network %s: %w", id, err)
	}
	return doc, true, nil
}

// observeNetwork reads a network by its id, or a creation's network by its exact name.
func (n nm) observeNetwork(ctx context.Context, name string, creation bool) (core.Observation, error) {
	if !creation {
		doc, _, err := n.networkDoc(ctx, name)
		if err != nil {
			return core.Observation{}, err
		}
		return named(doc), nil
	}
	list, err := n.d.docker.ListNetworks(ctx, dockerapi.Filters{"name": {name}})
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: list networks named %s: %w", name, engineCoded(err))
	}
	var candidates []core.Candidate
	for _, net := range list {
		// Docker's name filter also matches networks whose name only contains name.
		if net.Name != name {
			continue
		}
		doc, ok, err := n.networkDoc(ctx, net.ID)
		if err != nil {
			return core.Observation{}, err
		}
		if ok {
			candidates = append(candidates, core.Candidate{Resource: resourceOf(nounNetwork, net.ID), State: core.StateDoc{State: doc}})
		}
	}
	return found(candidates), nil
}

// observeVolume reads a volume by its name, which Docker keeps unique. The Engine does not say which containers use
// a volume, so it is unused when no container, running or not, mounts it.
func (n nm) observeVolume(ctx context.Context, name string, creation bool) (core.Observation, error) {
	v, err := n.d.docker.InspectVolume(ctx, name)
	if dockerapi.IsNotFound(err) {
		if creation {
			return found(nil), nil
		}
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: inspect volume %s: %w", name, engineCoded(err))
	}
	containers, err := n.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: list the containers using volume %s: %w", name, engineCoded(err))
	}
	unused := !slices.ContainsFunc(containers, func(c dockerapi.ContainerSummary) bool {
		return slices.ContainsFunc(c.Mounts, func(m dockerapi.MountPoint) bool { return m.Name == v.Name })
	})
	doc, err := dstate.VolumeCreated(&v, unused)
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: render volume %s: %w", name, err)
	}
	obs := named(doc)
	if creation {
		obs.Resource = resourceOf(nounVolume, v.Name)
	}
	return obs, nil
}

// observeContainer reads a container by its id, or a creation's container by its name, which Docker keeps unique.
func (n nm) observeContainer(ctx context.Context, name string, creation bool, phase dstate.Phase) (core.Observation, error) {
	c, err := n.d.docker.InspectContainer(ctx, name)
	if dockerapi.IsNotFound(err) {
		if creation {
			return found(nil), nil
		}
		return named(nullState().State), nil
	}
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: inspect container %s: %w", name, engineCoded(err))
	}
	doc, err := dstate.ContainerRun(c, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: render container %s: %w", name, err)
	}
	obs := named(doc)
	if creation {
		obs.Resource = resourceOf(nounContainer, c.ID)
	}
	return obs, nil
}

// observeEndpoint reads the NPM container's attachment to the network of that name; Docker keys a container's
// endpoints by network name. A missing container or attachment is the null state.
func (n nm) observeEndpoint(ctx context.Context, network string, creation bool, phase dstate.Phase) (core.Observation, error) {
	c, err := n.d.docker.InspectContainer(ctx, containerName)
	var ep *dockerapi.EndpointSettings
	switch {
	case dockerapi.IsNotFound(err):
	case err != nil:
		return core.Observation{}, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	default:
		ep = c.NetworkSettings.Networks[network]
	}
	if ep == nil {
		if creation {
			return found(nil), nil
		}
		return named(nullState().State), nil
	}
	doc, err := dstate.Endpoint(containerName, network, ep, phase)
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: render endpoint %s@%s: %w", containerName, network, err)
	}
	obs := named(doc)
	if creation {
		obs.Resource = resourceOf(nounEndpoint, network)
	}
	return obs, nil
}

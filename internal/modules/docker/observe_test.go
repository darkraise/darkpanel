package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// efAPI wraps a dockerAPI and replaces the answers a test needs to control;
// a nil hook delegates to the wrapped API.
type efAPI struct {
	dockerAPI
	containers func() []dockerapi.ContainerSummary
	networks   func() []dockerapi.Network
	extra      map[string]dockerapi.Network // networks InspectNetwork also finds
	inspectErr error                        // InspectContainer, InspectNetwork and InspectVolume fail with it
}

func (a efAPI) ListContainers(ctx context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	if a.containers != nil {
		return a.containers(), nil
	}
	return a.dockerAPI.ListContainers(ctx, o)
}

func (a efAPI) ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error) {
	if a.networks != nil {
		return a.networks(), nil
	}
	return a.dockerAPI.ListNetworks(ctx, f)
}

func (a efAPI) InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error) {
	if n, ok := a.extra[id]; ok {
		return n, nil
	}
	return a.dockerAPI.InspectNetwork(ctx, id)
}

func (a efAPI) InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error) {
	if a.inspectErr != nil {
		return dockerapi.ContainerJSON{}, a.inspectErr
	}
	return a.dockerAPI.InspectContainer(ctx, id)
}

// efState decodes a StateDoc's State so documents compare as JSON values.
func efState(t *testing.T, raw json.RawMessage) any {
	t.Helper()
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("state %q: %v", raw, err)
	}
	return v
}

// efSame fails unless got and want are the same JSON value.
func efSame(t *testing.T, what string, got, want json.RawMessage) {
	t.Helper()
	if !reflect.DeepEqual(efState(t, got), efState(t, want)) {
		t.Errorf("%s = %s, want %s", what, got, want)
	}
}

// efMust unwraps a dstate document; an error is a bug in the test's input.
func efMust(doc json.RawMessage, err error) json.RawMessage {
	if err != nil {
		panic(err)
	}
	return doc
}

func efEffectOf(resource string, state core.EffectState) core.Effect {
	return core.Effect{Kind: core.EffectModify, Resource: resource, State: state}
}

// TestEFPhaseOf: an effect that is being or was undone is compared in the
// Restored form; every other state in the Recorded form.
func TestEFPhaseOf(t *testing.T) {
	for state, want := range map[core.EffectState]dstate.Phase{
		core.EffectIntended: dstate.Recorded, core.EffectMade: dstate.Recorded, core.EffectSkipped: dstate.Recorded,
		core.EffectUnresolved: dstate.Recorded, core.EffectAccepted: dstate.Recorded, core.EffectAbandoned: dstate.Recorded,
		core.EffectUndoing: dstate.Restored, core.EffectUndone: dstate.Restored,
	} {
		if got := phaseOf(core.Effect{State: state}); got != want {
			t.Errorf("phaseOf(%s) = %v, want %v", state, got, want)
		}
	}
}

// TestEFObserveContainer: a container is rendered with dstate in the phase
// of the effect, and a container that is gone is the null state.
func TestEFObserveContainer(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	c := dkContainer("web", dkRunning)
	h.docker.put(c)
	k := h.dk()
	for _, tc := range []struct {
		state core.EffectState
		phase dstate.Phase
	}{
		{core.EffectMade, dstate.Recorded}, {core.EffectIntended, dstate.Recorded},
		{core.EffectUndoing, dstate.Restored}, {core.EffectUndone, dstate.Restored},
	} {
		obs, err := k.observe(ctx, h.env(""), efEffectOf(containerResource(c.ID), tc.state))
		if err != nil {
			t.Fatal(err)
		}
		efSame(t, string(tc.state), obs.Current.State, efMust(dstate.ContainerRun(c, tc.phase)))
		if obs.Matches != 1 || !obs.Exclusive {
			t.Errorf("%s: matches %d, exclusive %v; want 1, true", tc.state, obs.Matches, obs.Exclusive)
		}
	}
	rec, _ := dstate.ContainerRun(c, dstate.Recorded)
	res, _ := dstate.ContainerRun(c, dstate.Restored)
	if reflect.DeepEqual(efState(t, rec), efState(t, res)) {
		t.Fatal("the Recorded and Restored documents of a running container are equal; the test cannot tell the phases apart")
	}

	gone, err := k.observe(ctx, h.env(""), efEffectOf(containerResource(dkID("gone")), core.EffectMade))
	if err != nil || string(gone.Current.State) != "null" || gone.Matches != 1 {
		t.Errorf("a missing container: %+v, %v; want the null state", gone, err)
	}
}

// TestEFObserveEndpoint: an endpoint is present when the container is on
// the network, absent when it is not or when the container is gone.
func TestEFObserveEndpoint(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	n := dkNetwork("web")
	other := dkNetwork("other")
	c := dkContainer("app", dkRunning)
	ep := &dockerapi.EndpointSettings{NetworkID: n.ID, EndpointID: "ep-1", Aliases: []string{"app"}}
	c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{"web": ep}
	h.docker.put(c)
	k := h.dk()

	for _, tc := range []struct {
		name         string
		container    string
		network      string
		state        core.EffectState
		ep           *dockerapi.EndpointSettings
		phase        dstate.Phase
		wantPresent  bool
		wantEndpoint string
	}{
		{"present, recorded", c.ID, n.ID, core.EffectMade, ep, dstate.Recorded, true, "ep-1"},
		{"present, restored", c.ID, n.ID, core.EffectUndone, ep, dstate.Restored, true, ""},
		{"other network", c.ID, other.ID, core.EffectMade, nil, dstate.Recorded, false, ""},
		{"container gone", dkID("gone"), n.ID, core.EffectMade, nil, dstate.Recorded, false, ""},
	} {
		obs, err := k.observe(ctx, h.env(""), efEffectOf(endpointResource(tc.container, tc.network), tc.state))
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		efSame(t, tc.name, obs.Current.State, efMust(dstate.Endpoint(tc.container, tc.network, tc.ep, tc.phase)))
		if got := strings.Contains(string(obs.Current.State), `"present":true`); got != tc.wantPresent {
			t.Errorf("%s: present = %v, want %v (%s)", tc.name, got, tc.wantPresent, obs.Current.State)
		}
	}
}

// TestEFObserveNetwork: a network named by id is rendered with dstate, one
// that is gone is the null state, and a creation not yet named is found by
// its exact name.
func TestEFObserveNetwork(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	web, web2 := dkNetwork("web"), dkNetwork("web2")
	busy := dkNetwork("web")
	busy.ID = dkID("web again")
	busy.Containers = map[string]dockerapi.NetworkContainer{dkID("app"): {Name: "app"}}
	h.docker.putNetwork(web)
	h.docker.putNetwork(web2)
	k := h.dk()
	creation := core.Effect{Kind: core.EffectCreate, Resource: networkResource("?web"), State: core.EffectIntended,
		Recovery: &core.RecoveryID{Kind: "name", Value: "web"}}

	obs, err := k.observe(ctx, h.env(""), efEffectOf(networkResource(web.ID), core.EffectMade))
	if err != nil {
		t.Fatal(err)
	}
	efSame(t, "by id", obs.Current.State, efMust(dstate.NetworkCreated(&web)))

	obs, err = k.observe(ctx, h.env(""), efEffectOf(networkResource(dkID("gone")), core.EffectMade))
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("a missing network: %+v, %v", obs, err)
	}

	obs, err = k.observe(ctx, h.env(""), creation)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Matches != 1 || obs.Resource != networkResource(web.ID) {
		t.Errorf("by name: matches %d, resource %q; want 1, %q (web2 must not match)", obs.Matches, obs.Resource, networkResource(web.ID))
	}
	efSame(t, "by name", obs.Current.State, efMust(dstate.NetworkCreated(&web)))

	none := core.Effect{Kind: core.EffectCreate, Resource: networkResource("?absent"), State: core.EffectIntended,
		Recovery: &core.RecoveryID{Kind: "name", Value: "absent"}}
	if obs, err = k.observe(ctx, h.env(""), none); err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("no match: %+v, %v; want 0 matches and the null state", obs, err)
	}

	k.d.docker = efAPI{dockerAPI: h.docker, extra: map[string]dockerapi.Network{busy.ID: busy},
		networks: func() []dockerapi.Network { return []dockerapi.Network{web, busy} }}
	obs, err = k.observe(ctx, h.env(""), creation)
	if err != nil || obs.Matches != 2 || len(obs.Candidates) != 2 || string(obs.Current.State) != "null" {
		t.Fatalf("two matches: %+v, %v", obs, err)
	}
	want := []string{networkResource(web.ID), networkResource(busy.ID)}
	if want[0] > want[1] {
		want[0], want[1] = want[1], want[0]
	}
	if obs.Candidates[0].Resource != want[0] || obs.Candidates[1].Resource != want[1] {
		t.Errorf("candidates %q, %q; want %q in order", obs.Candidates[0].Resource, obs.Candidates[1].Resource, want)
	}
	for _, c := range obs.Candidates {
		if c.Resource == networkResource(busy.ID) && !strings.Contains(string(c.State.State), `"unused":false`) {
			t.Errorf("the candidate with a container is not marked used: %s", c.State.State)
		}
	}
}

// TestEFObserveNetworkByNameIsExact: Docker's name filter matches substrings,
// so the observation keeps only networks whose name is the whole name even
// when the daemon returns more.
func TestEFObserveNetworkByNameIsExact(t *testing.T) {
	h := newDKHost(t)
	web := dkNetwork("web")
	prefix := dkNetwork("web-extra")
	h.docker.putNetwork(web)
	h.docker.putNetwork(prefix)
	k := h.dk()
	k.d.docker = efAPI{dockerAPI: h.docker, networks: func() []dockerapi.Network { return []dockerapi.Network{prefix, web} }}
	creation := core.Effect{Kind: core.EffectCreate, Resource: networkResource("?web"), State: core.EffectIntended,
		Recovery: &core.RecoveryID{Kind: "name", Value: "web"}}
	obs, err := k.observe(context.Background(), h.env(""), creation)
	if err != nil || obs.Matches != 1 || obs.Resource != networkResource(web.ID) {
		t.Errorf("observation = %+v, %v; want the one network web", obs, err)
	}
}

// TestEFObserveVolume: a volume is rendered with whether any container,
// running or not, mounts it; a creation is found by its name.
func TestEFObserveVolume(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	v := dkVolume("data", map[string]string{"team": "a"})
	h.docker.putVolume(v)
	var mounts []dockerapi.ContainerSummary
	k := h.dk()
	k.d.docker = efAPI{dockerAPI: h.docker, containers: func() []dockerapi.ContainerSummary { return mounts }}

	obs, err := k.observe(ctx, h.env(""), efEffectOf(volumeResource("data"), core.EffectMade))
	if err != nil {
		t.Fatal(err)
	}
	efSame(t, "unused", obs.Current.State, efMust(dstate.VolumeCreated(&v, true)))

	mounts = []dockerapi.ContainerSummary{
		{Names: []string{"/other"}, Mounts: []dockerapi.MountPoint{{Type: "volume", Name: "elsewhere"}}},
		{Names: []string{"/app"}, State: "exited", Mounts: []dockerapi.MountPoint{{Type: "volume", Name: "data"}}},
	}
	obs, err = k.observe(ctx, h.env(""), efEffectOf(volumeResource("data"), core.EffectMade))
	if err != nil {
		t.Fatal(err)
	}
	efSame(t, "used", obs.Current.State, efMust(dstate.VolumeCreated(&v, false)))

	creation := core.Effect{Kind: core.EffectCreate, Resource: volumeResource("?data"), State: core.EffectIntended,
		Recovery: &core.RecoveryID{Kind: "name", Value: "data"}}
	obs, err = k.observe(ctx, h.env(""), creation)
	if err != nil || obs.Matches != 1 || obs.Resource != volumeResource("data") {
		t.Errorf("creation by name: %+v, %v", obs, err)
	}
	creation.Resource, creation.Recovery.Value = volumeResource("?absent"), "absent"
	obs, err = k.observe(ctx, h.env(""), creation)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("absent creation: %+v, %v; want 0 matches and the null state", obs, err)
	}
	gone, err := k.observe(ctx, h.env(""), efEffectOf(volumeResource("absent"), core.EffectUndone))
	if err != nil || string(gone.Current.State) != "null" {
		t.Errorf("a missing volume: %+v, %v", gone, err)
	}
}

// TestEFObserveRefuses: a resource that is not a docker object is an error
// that starts with the package name, and a daemon that cannot be reached is
// docker.engine_unavailable.
func TestEFObserveRefuses(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	k := h.dk()
	for _, resource := range []string{"docker.thing:x", "docker.container", "docker.container:", "docker.container:?", "docker.container:?web", "docker.endpoint:?c1@n1", "firewall.rule:x",
		"docker.endpoint:c1", "docker.endpoint:@n1", "docker.endpoint:c1@"} {
		_, err := k.observe(ctx, h.env(""), efEffectOf(resource, core.EffectMade))
		if err == nil || !strings.HasPrefix(err.Error(), "docker: ") {
			t.Errorf("observe(%q) error = %v, want one starting with docker:", resource, err)
		}
	}
	k.d.docker = efAPI{dockerAPI: h.docker, inspectErr: fmt.Errorf("dockerapi: GET: %w", dockerapi.ErrUnavailable)}
	_, err := k.observe(ctx, h.env(""), efEffectOf(containerResource(dkID("web")), core.EffectMade))
	if coded := errcode.From(err); coded.Code != errcode.DockerEngineUnavailable {
		t.Errorf("a daemon that is down: code %q, want docker.engine_unavailable (%v)", coded.Code, err)
	}
}

package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/npm/npmapi"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func sdDoc(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("%s: %v", raw, err)
	}
	return m
}

func sdCode(t *testing.T, err error) errcode.Code {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		t.Fatalf("error %v carries no code", err)
	}
	return coded.Code
}

func TestSdResourceAndIdentity(t *testing.T) {
	tests := []struct{ noun, identity, resource string }{
		{nounNetwork, "?darkpanel-npm", "npm.network:?darkpanel-npm"},
		{nounNetwork, "abc", "npm.network:abc"},
		{nounProxyHost, "?a.example.org,b.example.org", "npm.proxyhost:?a.example.org,b.example.org"},
		{nounClaim, "net|expose:a.example.org", "npm.claim:net|expose:a.example.org"},
	}
	for _, tc := range tests {
		if got := resourceOf(tc.noun, tc.identity); got != tc.resource {
			t.Errorf("resourceOf(%q, %q) = %q, want %q", tc.noun, tc.identity, got, tc.resource)
		}
		if got := identityOf(tc.resource); got != tc.identity {
			t.Errorf("identityOf(%q) = %q, want %q", tc.resource, got, tc.identity)
		}
	}
	nouns := map[string]string{
		nounNetwork: "npm.network", nounVolume: "npm.volume", nounContainer: "npm.container", nounEndpoint: "npm.endpoint",
		nounProxyHost: "npm.proxyhost", nounAccessList: "npm.accesslist", nounCertificate: "npm.certificate",
		nounInstance: "npm.instance", nounClaim: "npm.claim",
	}
	for got, want := range nouns {
		if got != want {
			t.Errorf("noun %q, want %q", got, want)
		}
	}
}

func TestSdNpmEffect(t *testing.T) {
	create := npmEffect(core.EffectCreate, "npm.proxyhost:?a.example.org,b.example.org", json.RawMessage(`{"ignored":true}`), certCommitWait)
	want := core.Effect{
		Kind: core.EffectCreate, Resource: "npm.proxyhost:?a.example.org,b.example.org", CommitWait: certCommitWait,
		Recovery: &core.RecoveryID{Kind: "name", Value: "a.example.org,b.example.org"},
		Before:   core.StateDoc{State: json.RawMessage("null")},
	}
	if !reflect.DeepEqual(create, want) {
		t.Errorf("creation %+v, want %+v", create, want)
	}
	modify := npmEffect(core.EffectModify, "npm.container:abc", json.RawMessage(`{"running":false}`), commitWait)
	want = core.Effect{
		Kind: core.EffectModify, Resource: "npm.container:abc", CommitWait: commitWait,
		Before: core.StateDoc{State: json.RawMessage(`{"running":false}`)},
	}
	if !reflect.DeepEqual(modify, want) {
		t.Errorf("modification %+v, want %+v", modify, want)
	}
	if create.Local || modify.Local || create.After != nil || modify.After != nil {
		t.Error("an npm effect is not local and has no After")
	}
}

func TestSdRun(t *testing.T) {
	dockerErr := func(code int) error { return &dockerapi.APIError{StatusCode: code, Message: "no"} }
	npmErr := func(code int) error { return &npmapi.APIError{Status: code, Message: "no"} }
	creation := npmEffect(core.EffectCreate, resourceOf(nounNetwork, "?darkpanel-npm"), nil, commitWait)
	modify := npmEffect(core.EffectModify, resourceOf(nounContainer, "abc"), json.RawMessage(`{"running":false}`), commitWait)
	boom := errors.New("connection reset")
	wrapped := npmErr(422)
	tests := []struct {
		name     string
		effect   core.Effect
		resource string // what do names
		cause    error
		definite bool
		state    core.EffectState
		made     string
	}{
		{"a creation that names its object", creation, resourceOf(nounNetwork, "abc"), nil, false, core.EffectMade, "npm.network:abc"},
		{"a modification that names nothing", modify, "", nil, false, core.EffectMade, "npm.container:abc"},
		{"a creation that still names no object", creation, resourceOf(nounNetwork, "?x"), nil, false, core.EffectIntended, ""},
		{"a 409 from the Engine", creation, "", dockerErr(409), true, core.EffectSkipped, ""},
		{"a 404 from the Engine", modify, "", dockerErr(404), true, core.EffectSkipped, ""},
		{"a 500 from the Engine", creation, "", dockerErr(500), false, core.EffectIntended, ""},
		{"a 400 from NPM", creation, "", npmErr(400), true, core.EffectSkipped, ""},
		{"a wrapped 422 from NPM", creation, "", fmt.Errorf("create: %w", wrapped), true, core.EffectSkipped, ""},
		{"a 502 from NPM", creation, "", npmErr(502), false, core.EffectIntended, ""},
		{"a transport error", creation, "", boom, false, core.EffectIntended, ""},
		{"a refusal the callee marked Definite", modify, "", core.Definite(errors.New("npm: no instance")), true, core.EffectSkipped, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newTHHost(t)
			var observed []string
			observe := func(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
				observed = append(observed, e.Resource+" "+string(e.State))
				return named(json.RawMessage(`{"seen":"` + e.Resource + `"}`)), nil
			}
			do := func(context.Context) (string, error) { return tc.resource, tc.cause }
			err := h.nm().run(context.Background(), h.env("c1"), tc.effect, observe, do)
			effects := h.journal.effects()
			if len(effects) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(effects))
			}
			got := effects[0]
			if got.State != tc.state || got.Resource != sdFirstOf(tc.made, tc.effect.Resource) {
				t.Errorf("effect %s %s, want %s %s", got.Resource, got.State, sdFirstOf(tc.made, tc.effect.Resource), tc.state)
			}
			switch tc.state {
			case core.EffectMade:
				if err != nil {
					t.Fatal(err)
				}
				if got.Post == nil || string(got.Post.State) != `{"seen":"`+tc.made+`"}` {
					t.Errorf("post-state %+v, want what observe read from %s", got.Post, tc.made)
				}
				if !reflect.DeepEqual(observed, []string{tc.made + " intended"}) {
					t.Errorf("observe was called with %v", observed)
				}
			default:
				if err == nil {
					t.Fatal("run succeeded")
				}
				if core.IsDefinite(err) != tc.definite {
					t.Errorf("Definite = %v, want %v (error %v)", core.IsDefinite(err), tc.definite, err)
				}
				if tc.cause != nil && !errors.Is(err, tc.cause) {
					t.Errorf("error %v lost its cause %v", err, tc.cause)
				}
			}
		})
	}
}

func sdFirstOf(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func TestSdObserveEngine(t *testing.T) {
	ctx := context.Background()
	labels := map[string]string{core.LabelModule: ID}
	observe := func(t *testing.T, h *thHost, resource string, state core.EffectState) core.Observation {
		t.Helper()
		obs, err := h.nm().observeEngine(ctx, h.env("c1"), core.Effect{Resource: resource, State: state})
		if err != nil {
			t.Fatalf("observe %s: %v", resource, err)
		}
		return obs
	}
	absent := func(t *testing.T, obs core.Observation, matches int) {
		t.Helper()
		if obs.Matches != matches || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, want %d matches and the null state", obs, matches)
		}
	}
	netID, ctrID := thID("network:darkpanel-npm"), thID("container:darkpanel-npm")

	t.Run("a network created by name", func(t *testing.T) {
		h := newTHHost(t)
		absent(t, observe(t, h, "npm.network:?darkpanel-npm", core.EffectIntended), 0)
		h.docker.putNetwork(thNetwork("darkpanel-npm-extra"))
		absent(t, observe(t, h, "npm.network:?darkpanel-npm", core.EffectIntended), 0)
		h.docker.putNetwork(thNetwork("darkpanel-npm", thNetLabels(labels)))
		obs := observe(t, h, "npm.network:?darkpanel-npm", core.EffectIntended)
		doc := sdDoc(t, obs.Current.State)
		if obs.Matches != 1 || obs.Resource != "npm.network:"+netID || !obs.Exclusive || doc["id"] != netID || doc["unused"] != true {
			t.Errorf("observation %+v (state %v), want one exclusive match, unused", obs, doc)
		}
	})
	t.Run("a network by id", func(t *testing.T) {
		h := newTHHost(t)
		absent(t, observe(t, h, "npm.network:"+netID, core.EffectMade), 1)
		h.docker.putNetwork(thNetwork("darkpanel-npm"))
		if doc := sdDoc(t, observe(t, h, "npm.network:"+netID, core.EffectMade).Current.State); doc["unused"] != true {
			t.Errorf("state %v, want unused", doc)
		}
		h.docker.putContainer(thContainer("web", thEndpoint("darkpanel-npm", "172.20.0.3")))
		if doc := sdDoc(t, observe(t, h, "npm.network:"+netID, core.EffectMade).Current.State); doc["unused"] != false {
			t.Errorf("state %v, want a network in use", doc)
		}
	})
	t.Run("a volume", func(t *testing.T) {
		h := newTHHost(t)
		absent(t, observe(t, h, "npm.volume:?darkpanel-npm-data", core.EffectIntended), 0)
		absent(t, observe(t, h, "npm.volume:darkpanel-npm-data", core.EffectMade), 1)
		h.docker.putVolume(dockerapi.Volume{Name: dataVolume, Labels: labels})
		obs := observe(t, h, "npm.volume:?darkpanel-npm-data", core.EffectIntended)
		if obs.Matches != 1 || obs.Resource != "npm.volume:darkpanel-npm-data" || !obs.Exclusive || sdDoc(t, obs.Current.State)["unused"] != true {
			t.Errorf("observation %+v, want one exclusive, unused match", obs)
		}
		h.docker.putContainer(thContainer("darkpanel-npm", thMounted(dataVolume, "/data")))
		if doc := sdDoc(t, observe(t, h, "npm.volume:darkpanel-npm-data", core.EffectMade).Current.State); doc["unused"] != false {
			t.Errorf("state %v, want a volume in use", doc)
		}
	})
	t.Run("a container", func(t *testing.T) {
		h := newTHHost(t)
		absent(t, observe(t, h, "npm.container:?darkpanel-npm", core.EffectIntended), 0)
		absent(t, observe(t, h, "npm.container:"+ctrID, core.EffectMade), 1)
		h.docker.putContainer(thContainer("darkpanel-npm", thRunning))
		obs := observe(t, h, "npm.container:?darkpanel-npm", core.EffectIntended)
		doc := sdDoc(t, obs.Current.State)
		if obs.Matches != 1 || obs.Resource != "npm.container:"+ctrID || !obs.Exclusive || doc["id"] != ctrID || doc["running"] != true {
			t.Errorf("observation %+v (state %v), want one exclusive, running match", obs, doc)
		}
		byID := observe(t, h, "npm.container:"+ctrID, core.EffectMade)
		if _, ok := sdDoc(t, byID.Current.State)["startedAt"]; !ok || byID.Matches != 1 {
			t.Errorf("recorded state %s, want a startedAt", byID.Current.State)
		}
		for _, st := range []core.EffectState{core.EffectUndoing, core.EffectUndone} {
			if _, ok := sdDoc(t, observe(t, h, "npm.container:"+ctrID, st).Current.State)["startedAt"]; ok {
				t.Errorf("state of an %s effect holds a startedAt, want the restorable form", st)
			}
		}
		made, err := h.nm().observeEngine(ctx, h.env("c1"), core.Effect{Kind: core.EffectCreate, Resource: "npm.container:" + ctrID, State: core.EffectMade})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := sdDoc(t, made.Current.State)["startedAt"]; ok {
			t.Errorf("state of a made creation %s holds a startedAt, want the restorable form", made.Current.State)
		}
	})
	t.Run("an endpoint", func(t *testing.T) {
		h := newTHHost(t)
		absent(t, observe(t, h, "npm.endpoint:?app", core.EffectIntended), 0)
		h.docker.putContainer(thContainer("darkpanel-npm", thRunning))
		absent(t, observe(t, h, "npm.endpoint:?app", core.EffectIntended), 0)
		absent(t, observe(t, h, "npm.endpoint:app", core.EffectMade), 1)
		h.docker.putContainer(thContainer("darkpanel-npm", thRunning, thEndpoint("app", "172.20.0.2")))
		obs := observe(t, h, "npm.endpoint:?app", core.EffectIntended)
		doc := sdDoc(t, obs.Current.State)
		if obs.Matches != 1 || obs.Resource != "npm.endpoint:app" || !obs.Exclusive || doc["present"] != true || doc["container"] != "darkpanel-npm" || doc["network"] != "app" {
			t.Errorf("observation %+v (state %v), want the container's attachment to app", obs, doc)
		}
		byName := observe(t, h, "npm.endpoint:app", core.EffectMade)
		if byName.Matches != 1 || sdDoc(t, byName.Current.State)["present"] != true {
			t.Errorf("observation by identity %+v", byName)
		}
	})
	t.Run("what is not an Engine object", func(t *testing.T) {
		h := newTHHost(t)
		for _, r := range []string{"npm.proxyhost:7", "npm.network:", "npm.network:?", "npm", "docker.network:abc"} {
			if _, err := h.nm().observeEngine(ctx, h.env("c1"), core.Effect{Resource: r}); err == nil {
				t.Errorf("observeEngine(%q) succeeded", r)
			}
		}
	})
	t.Run("an Engine that is not reachable", func(t *testing.T) {
		h := newTHHost(t)
		h.docker.Fail("InspectNetwork", dockerapi.ErrUnavailable)
		_, err := h.nm().observeEngine(ctx, h.env("c1"), core.Effect{Resource: "npm.network:" + netID})
		if err == nil || sdCode(t, err) != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want %s", err, errcode.DockerEngineUnavailable)
		}
	})
}

// TestSdRunSettlesThroughObserve: run records the post-state the Engine reports after the call, so the next observation of the
// object equals it.
func TestSdRunSettlesThroughObserve(t *testing.T) {
	h := newTHHost(t)
	n, ctx := h.nm(), context.Background()
	e := npmEffect(core.EffectCreate, resourceOf(nounVolume, "?"+dataVolume), nil, commitWait)
	err := n.run(ctx, h.env("c1"), e, n.observeEngine, func(ctx context.Context) (string, error) {
		v, err := n.d.docker.CreateVolume(ctx, dockerapi.VolumeCreate{Name: dataVolume, Labels: map[string]string{core.LabelModule: ID}})
		return resourceOf(nounVolume, v.Name), err
	})
	if err != nil {
		t.Fatal(err)
	}
	made := h.journal.effects()[0]
	obs, err := n.observeEngine(ctx, h.env("c1"), made)
	if err != nil || made.State != core.EffectMade || made.Resource != "npm.volume:darkpanel-npm-data" || made.Post == nil ||
		!reflect.DeepEqual(sdDoc(t, made.Post.State), sdDoc(t, obs.Current.State)) {
		t.Errorf("made effect %+v and observation %+v (error %v) disagree", made, obs, err)
	}
	if made.CommitWait != 2*time.Minute || made.Local {
		t.Errorf("effect commit wait %v, local %v; want 2m and not local", made.CommitWait, made.Local)
	}
}

// TestW10NsFoundLeavesTheCallersSliceAlone: found orders the candidates it reports, not the slice the caller passed.
func TestW10NsFoundLeavesTheCallersSliceAlone(t *testing.T) {
	second := core.Candidate{Resource: "npm.proxyhost:2"}
	first := core.Candidate{Resource: "npm.proxyhost:1"}
	candidates := []core.Candidate{second, first}
	obs := found(candidates)
	resources := func(cs []core.Candidate) []string {
		out := make([]string, len(cs))
		for i, c := range cs {
			out[i] = c.Resource
		}
		return out
	}
	if got := resources(candidates); !slices.Equal(got, []string{"npm.proxyhost:2", "npm.proxyhost:1"}) {
		t.Errorf("the caller's slice is now %v, want it in the order it was passed", got)
	}
	if got := resources(obs.Candidates); obs.Matches != 2 || !slices.Equal(got, []string{"npm.proxyhost:1", "npm.proxyhost:2"}) {
		t.Errorf("observation %+v, want two matches reported in resource order", obs)
	}
}

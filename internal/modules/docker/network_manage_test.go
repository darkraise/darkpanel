package docker

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// obRecorder passes every call to the wrapped client and records what
// CreateNetwork and CreateVolume were asked for.
type obRecorder struct {
	dockerAPI
	networks []dockerapi.NetworkCreate
	volumes  []dockerapi.VolumeCreate
}

func (r *obRecorder) CreateNetwork(ctx context.Context, n dockerapi.NetworkCreate) (string, error) {
	r.networks = append(r.networks, n)
	return r.dockerAPI.CreateNetwork(ctx, n)
}

func (r *obRecorder) CreateVolume(ctx context.Context, v dockerapi.VolumeCreate) (dockerapi.Volume, error) {
	r.volumes = append(r.volumes, v)
	return r.dockerAPI.CreateVolume(ctx, v)
}

// obModule registers actions, so the registry's own checks run on them.
type obModule struct{ defs []core.ActionDef }

func (m obModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

// obRefused asserts err is a definite refusal with the given code and returns it.
func obRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// obHasField reports whether a refusal names the field path.
func obHasField(e *errcode.Error, path string) bool {
	return slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == path })
}

// obCheckInputs validates each input against def's schema: field "" means
// accepted, otherwise the refusal names that field.
func obCheckInputs(t *testing.T, def core.ActionDef, inputs map[string]string) {
	t.Helper()
	v := schema.NewValidator()
	for input, field := range inputs {
		err := v.ValidateInput(def, json.RawMessage(input))
		var coded *errcode.Error
		switch {
		case field == "" && err != nil:
			t.Errorf("%s input %s: %v, want it accepted", def.Spec.Name, input, err)
		case field != "" && (!errors.As(err, &coded) || !obHasField(coded, field)):
			t.Errorf("%s input %s: %v, want a refusal naming %s", def.Spec.Name, input, err, field)
		}
	}
}

// obShared is a network that containers a and b are attached to.
func obShared(h *dkHost) dockerapi.Network {
	n := dkNetwork("shared")
	n.Containers = map[string]dockerapi.NetworkContainer{dkID("b"): {Name: "b"}, dkID("a"): {Name: "a"}}
	h.docker.putNetwork(n)
	h.docker.put(dkContainer("a", dkOnNetwork(n.ID)))
	h.docker.put(dkContainer("b", dkOnNetwork(n.ID)))
	return n
}

func TestObNetworkCreatePlan(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		input string
		field string // the field a refusal names; "" when the plan is accepted
		diff  []core.DiffLine
	}{
		{name: "defaults", input: `{"name":"web-net"}`, diff: []core.DiffLine{
			{Op: "+", Text: "network web-net"}, {Op: " ", Text: "driver bridge"},
		}},
		{name: "everything", input: `{"name":"app","internal":true,"attachable":true,"ipv6":true,"subnet":"10.9.0.0/24","gateway":"10.9.0.1","labels":{"team":"a","env":"x"}}`,
			diff: []core.DiffLine{
				{Op: "+", Text: "network app"}, {Op: " ", Text: "driver bridge"}, {Op: " ", Text: "internal"}, {Op: " ", Text: "attachable"},
				{Op: " ", Text: "IPv6 enabled"}, {Op: " ", Text: "subnet 10.9.0.0/24"}, {Op: " ", Text: "gateway 10.9.0.1"},
				{Op: " ", Text: "label env=x"}, {Op: " ", Text: "label team=a"},
			}},
		{name: "empty name", input: `{"name":""}`, field: "/name"},
		{name: "bad name", input: `{"name":"-x"}`, field: "/name"},
		{name: "name taken", input: `{"name":"taken"}`, field: "/name"},
		{name: "reserved label", input: `{"name":"a","labels":{"darkpanel.module":"npm"}}`, field: "/labels"},
		{name: "bad driver", input: `{"name":"a","driver":"Bad Driver"}`, field: "/driver"},
		{name: "not a prefix", input: `{"name":"a","subnet":"nope"}`, field: "/subnet"},
		{name: "host bits set", input: `{"name":"a","subnet":"10.9.0.5/24"}`, field: "/subnet"},
		{name: "IPv6 subnet without ipv6", input: `{"name":"a","subnet":"2001:db8::/64"}`, field: "/subnet"},
		{name: "gateway without subnet", input: `{"name":"a","gateway":"10.9.0.1"}`, field: "/gateway"},
		{name: "gateway outside the subnet", input: `{"name":"a","subnet":"10.9.0.0/24","gateway":"10.8.0.1"}`, field: "/gateway"},
		{name: "gateway not an address", input: `{"name":"a","subnet":"10.9.0.0/24","gateway":"gw"}`, field: "/gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.putNetwork(dkNetwork("taken"))
			out, err := h.dk().networkCreate().Handler.Plan(ctx, h.env(""), json.RawMessage(tt.input))
			if tt.field != "" {
				if coded := obRefused(t, err, errcode.InvalidInput); !obHasField(coded, tt.field) {
					t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tt.diff) {
				t.Errorf("diff %v, want %v", out.Diff, tt.diff)
			}
			if out.StateHash == "" || out.Confirmation != "" {
				t.Errorf("hash %q confirmation %q, want a hash and no confirmation", out.StateHash, out.Confirmation)
			}
			if slices.Contains(h.docker.calls(), "CreateNetwork") {
				t.Error("a plan created the network")
			}
		})
	}
}

func TestObNetworkCreateStateHash(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	def := h.dk().networkCreate()
	plan := func(input string) string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first := plan(`{"name":"a"}`)
	if again := plan(`{"name":"a"}`); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	for _, other := range []string{`{"name":"b"}`, `{"name":"a","internal":true}`, `{"name":"a","labels":{"k":"v"}}`} {
		if plan(other) == first {
			t.Errorf("input %s hashes like the plain one", other)
		}
	}
}

func TestObNetworkCreateApply(t *testing.T) {
	ctx := context.Background()
	t.Run("creates and journals", func(t *testing.T) {
		h := newDKHost(t)
		k := h.dk()
		rec := &obRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		raw, err := k.networkCreate().Handler.Apply(ctx, h.env("c1"),
			json.RawMessage(`{"name":"app","internal":true,"attachable":true,"ipv6":true,"subnet":"10.9.0.0/24","gateway":"10.9.0.1","labels":{"team":"a"}}`))
		if err != nil {
			t.Fatal(err)
		}
		yes := true
		want := dockerapi.NetworkCreate{Name: "app", Driver: "bridge", Internal: true, Attachable: true, EnableIPv6: &yes,
			IPAM:   &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("10.9.0.0/24"), Gateway: netip.MustParseAddr("10.9.0.1")}}},
			Labels: map[string]string{"team": "a"}}
		if len(rec.networks) != 1 || !reflect.DeepEqual(rec.networks[0], want) {
			t.Fatalf("CreateNetwork requests %+v, want %+v", rec.networks, want)
		}
		made, err := h.docker.InspectNetwork(ctx, "app")
		if err != nil {
			t.Fatal(err)
		}
		var out Created
		if err := json.Unmarshal(raw, &out); err != nil || out != (Created{ID: made.ID, Name: "app"}) {
			t.Errorf("output %s (%v), want id %s name app", raw, err, made.ID)
		}
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		wantPost, err := dstate.NetworkCreated(&made)
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind != core.EffectCreate || e.Resource != networkResource(made.ID) || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait {
			t.Errorf("effect %+v, want a made non-local create of %s with commit wait %s", e, networkResource(made.ID), commitWait)
		}
		if e.Recovery == nil || *e.Recovery != (core.RecoveryID{Kind: "name", Value: "app"}) {
			t.Errorf("recovery %+v, want the name app", e.Recovery)
		}
		if string(e.Before.State) != "null" || e.Post == nil || string(e.Post.State) != string(wantPost) {
			t.Errorf("before %s post %+v, want null and %s", e.Before.State, e.Post, wantPost)
		}
	})
	t.Run("a name taken since the plan", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putNetwork(dkNetwork("app"))
		_, err := h.dk().networkCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"app"}`))
		_ = obRefused(t, err, errcode.InvalidInput)
		if len(h.journal.effects()) != 0 {
			t.Error("a refused apply journaled an effect")
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.fail("CreateNetwork", &dockerapi.APIError{StatusCode: 400, Message: "invalid subnet"})
		_, err := h.dk().networkCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"app"}`))
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		effects := h.journal.effects()
		if len(effects) != 1 || effects[0].State != core.EffectSkipped || effects[0].Resource != "docker.network:?app" {
			t.Errorf("effects %+v, want one skipped creation of docker.network:?app", effects)
		}
	})
	t.Run("the daemon fails", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.fail("CreateNetwork", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := h.dk().networkCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"app"}`))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("effects %+v, want one left intended", effects)
		}
	})
}

// TestObNetworkCreateUndo: the inverse removes the network while it is
// unused, leaves it and refuses when a container joined, and succeeds
// when it is already gone; observation then reads absent.
func TestObNetworkCreateUndo(t *testing.T) {
	ctx := context.Background()
	apply := func(t *testing.T, h *dkHost) (core.ActionDef, core.Effect) {
		t.Helper()
		def := h.dk().networkCreate()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"app"}`)); err != nil {
			t.Fatal(err)
		}
		return def, h.journal.effects()[0]
	}
	t.Run("unused", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
			t.Fatal(err)
		}
		if _, err := h.docker.InspectNetwork(ctx, "app"); !dockerapi.IsNotFound(err) {
			t.Errorf("the network is still there: %v", err)
		}
		e.State = core.EffectUndone
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %s (%v), want null", obs.Current.State, err)
		}
	})
	t.Run("a container joined", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		n, _ := h.docker.InspectNetwork(ctx, "app")
		h.docker.put(dkContainer("web", dkOnNetwork(n.ID)))
		_, err := def.Handler.Inverse(ctx, h.env("c1"), e)
		if coded := obRefused(t, err, errcode.DockerInUse); !strings.Contains(coded.Detail, "web") {
			t.Errorf("detail %q does not name web", coded.Detail)
		}
		if _, err := h.docker.InspectNetwork(ctx, "app"); err != nil {
			t.Errorf("the network was removed: %v", err)
		}
	})
	t.Run("already gone", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		n, _ := h.docker.InspectNetwork(ctx, "app")
		if err := h.docker.RemoveNetwork(ctx, n.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
			t.Errorf("inverse of an absent network: %v", err)
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		h.docker.fail("RemoveNetwork", &dockerapi.APIError{StatusCode: 409, Message: "in use"})
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("a resource that is not a created network", func(t *testing.T) {
		h := newDKHost(t)
		def := h.dk().networkCreate()
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), core.Effect{Resource: "docker.network:?app"}); err == nil {
			t.Error("inverse of an unnamed creation succeeded")
		}
	})
}

func TestObNetworkCreateVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	def := h.dk().networkCreate()
	in := json.RawMessage(`{"name":"app"}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{"id":"","name":""}`)); err == nil {
		t.Error("verify passed with no network")
	}
	raw, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, raw); err != nil {
		t.Errorf("verify with the output: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without an output finds the network by name: %v", err)
	}
}

func TestObNetworkRemovePlan(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	old := dkNetwork("old")
	h.docker.putNetwork(old)
	owned := dkNetwork("npm-net")
	owned.Labels = map[string]string{core.LabelModule: "npm"}
	h.docker.putNetwork(owned)
	bridge := dkNetwork("bridge")
	h.docker.putNetwork(bridge)
	shared := obShared(h)
	def := h.dk().networkRemove()
	plan := func(id string) (core.PlanOutcome, error) {
		return def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{"network":"`+id+`"}`))
	}

	out, err := plan(old.ID)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "network old (" + old.ID[:12] + ")"}, {Op: " ", Text: "driver bridge"}}
	if !slices.Equal(out.Diff, wantDiff) || out.Confirmation != "old" || out.StateHash == "" {
		t.Errorf("plan %+v, want diff %v, confirmation old and a hash", out, wantDiff)
	}
	if _, err := plan(shared.ID); err == nil {
		t.Error("a network in use was planned")
	} else if coded := obRefused(t, err, errcode.DockerInUse); !strings.Contains(coded.Detail, "a, b") {
		t.Errorf("detail %q does not name the containers a, b in order", coded.Detail)
	}
	_, err = plan(owned.ID)
	_ = obRefused(t, err, errcode.DockerOwned)
	_, err = plan(bridge.ID)
	if coded := obRefused(t, err, errcode.InvalidInput); !obHasField(coded, "/network") {
		t.Errorf("fields %v, want one at /network", coded.Fields)
	}
	_, err = plan("ghost")
	_ = obRefused(t, err, errcode.NotFound)
	_, err = plan("")
	if coded := obRefused(t, err, errcode.InvalidInput); !obHasField(coded, "/network") {
		t.Errorf("fields %v, want one at /network", coded.Fields)
	}
	if slices.Contains(h.docker.calls(), "RemoveNetwork") {
		t.Error("a plan removed a network")
	}
	// A container that joins changes the document the hash covers.
	other := dkNetwork("other")
	h.docker.putNetwork(other)
	first, _ := plan(other.ID)
	other.Labels = map[string]string{"k": "v"}
	h.docker.putNetwork(other)
	if second, _ := plan(other.ID); second.StateHash == first.StateHash {
		t.Error("the hash did not change with the network's labels")
	}
}

func TestObNetworkRemoveApply(t *testing.T) {
	ctx := context.Background()
	input := func(n dockerapi.Network) json.RawMessage { return json.RawMessage(`{"network":"` + n.ID + `"}`) }
	t.Run("removes and journals", func(t *testing.T) {
		h := newDKHost(t)
		old := dkNetwork("old")
		h.docker.putNetwork(old)
		raw, err := h.dk().networkRemove().Handler.Apply(ctx, h.env("c1"), input(old))
		if err != nil {
			t.Fatal(err)
		}
		var out Removed
		if err := json.Unmarshal(raw, &out); err != nil || out != (Removed{ID: old.ID, Name: "old"}) {
			t.Errorf("output %s (%v), want id %s name old", raw, err, old.ID)
		}
		if _, err := h.docker.InspectNetwork(ctx, old.ID); !dockerapi.IsNotFound(err) {
			t.Errorf("the network is still there: %v", err)
		}
		wantBefore, _ := dstate.NetworkCreated(&old)
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		if e.Kind != core.EffectDelete || e.Resource != networkResource(old.ID) || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait ||
			string(e.Before.State) != string(wantBefore) || e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("effect %+v, want a made non-local delete from %s to null", e, wantBefore)
		}
	})
	t.Run("a container joined since the plan", func(t *testing.T) {
		h := newDKHost(t)
		shared := obShared(h)
		_, err := h.dk().networkRemove().Handler.Apply(ctx, h.env("c1"), input(shared))
		_ = obRefused(t, err, errcode.DockerInUse)
		if len(h.journal.effects()) != 0 || slices.Contains(h.docker.calls(), "RemoveNetwork") {
			t.Error("a refused apply journaled an effect or called RemoveNetwork")
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		old := dkNetwork("old")
		h.docker.putNetwork(old)
		h.docker.fail("RemoveNetwork", &dockerapi.APIError{StatusCode: 403, Message: "forbidden"})
		_, err := h.dk().networkRemove().Handler.Apply(ctx, h.env("c1"), input(old))
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
	})
	t.Run("the daemon fails, then settle observes", func(t *testing.T) {
		h := newDKHost(t)
		old := dkNetwork("old")
		h.docker.putNetwork(old)
		def := h.dk().networkRemove()
		h.docker.fail("RemoveNetwork", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		if _, err := def.Handler.Apply(ctx, h.env("c1"), input(old)); err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		e := h.journal.effects()[0]
		if e.State != core.EffectIntended {
			t.Fatalf("effect state %s, want intended", e.State)
		}
		if obs, err := def.Handler.Observe(ctx, h.env("c1"), e); err != nil || string(obs.Current.State) != string(e.Before.State) {
			t.Errorf("observation %s (%v), want the before-state %s", obs.Current.State, err, e.Before.State)
		}
		if err := h.docker.RemoveNetwork(ctx, old.ID); err != nil {
			t.Fatal(err)
		}
		if obs, err := def.Handler.Observe(ctx, h.env("c1"), e); err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %s (%v), want null", obs.Current.State, err)
		}
	})
}

func TestObNetworkRemoveVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	old := dkNetwork("old")
	h.docker.putNetwork(old)
	def := h.dk().networkRemove()
	in := json.RawMessage(`{"network":"` + old.ID + `"}`)
	out := json.RawMessage(`{"id":"` + old.ID + `","name":"old"}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the network still there")
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after the removal: %v", err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err != nil {
		t.Errorf("verify without an output: %v", err)
	}
}

func TestObNetworkDefinitions(t *testing.T) {
	h := newDKHost(t)
	k := h.dk()
	create, remove := k.networkCreate(), k.networkRemove()
	if s := create.Spec; s.Name != "docker.network.create" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) || s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("create spec %+v", s)
	}
	if create.Handler.Inverse == nil || create.Handler.Observe == nil {
		t.Error("create has no undo handler or observer")
	}
	if s := remove.Spec; s.Name != "docker.network.remove" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) {
		t.Errorf("remove spec %+v", s)
	}
	if remove.Handler.Inverse != nil || remove.Handler.Observe == nil {
		t.Error("remove has an undo handler or no observer")
	}
	if _, err := core.NewRegistry(obModule{[]core.ActionDef{create, remove, k.volumeCreate(), k.volumeRemove()}}); err != nil {
		t.Errorf("the registry refuses the actions: %v", err)
	}
	obCheckInputs(t, create, map[string]string{
		`{"name":"a"}`: "", `{"name":"a","driver":"bridge","internal":true,"attachable":true,"ipv6":true,"subnet":"10.0.0.0/8","gateway":"10.0.0.1","labels":{"k":"v"}}`: "",
		`{}`: "/name", `{"name":"a","x":1}`: "/x", `{"name":"a","internal":"yes"}`: "/internal", `{"name":"a","labels":{"k":1}}`: "/labels/k",
	})
	obCheckInputs(t, remove, map[string]string{`{"network":"a"}`: "", `{}`: "/network", `{"network":"a","x":1}`: "/x"})
}

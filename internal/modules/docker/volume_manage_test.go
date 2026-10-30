package docker

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// obMounts makes a container mount the named volumes.
func obMounts(names ...string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		for _, n := range names {
			c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: "volume", Name: n, Destination: "/" + n, RW: true})
		}
	}
}

func TestObVolumeCreatePlan(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name  string
		input string
		field string // the field a refusal names; "" when the plan is accepted
		diff  []core.DiffLine
	}{
		{name: "defaults", input: `{"name":"data"}`, diff: []core.DiffLine{{Op: "+", Text: "volume data"}, {Op: " ", Text: "driver local"}}},
		{name: "labels", input: `{"name":"data","driver":"local","labels":{"team":"a","env":"x"}}`, diff: []core.DiffLine{
			{Op: "+", Text: "volume data"}, {Op: " ", Text: "driver local"}, {Op: " ", Text: "label env=x"}, {Op: " ", Text: "label team=a"},
		}},
		{name: "empty name", input: `{"name":""}`, field: "/name"},
		{name: "bad name", input: `{"name":"a/b"}`, field: "/name"},
		{name: "name taken", input: `{"name":"taken"}`, field: "/name"},
		{name: "reserved label", input: `{"name":"data","labels":{"darkpanel.module":"npm"}}`, field: "/labels"},
		{name: "bad driver", input: `{"name":"data","driver":"Bad Driver"}`, field: "/driver"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.putVolume(dkVolume("taken", nil))
			out, err := h.dk().volumeCreate().Handler.Plan(ctx, h.env(""), json.RawMessage(tt.input))
			if tt.field != "" {
				if coded := obRefused(t, err, errcode.InvalidInput); !obHasField(coded, tt.field) {
					t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tt.diff) || out.StateHash == "" || out.Confirmation != "" {
				t.Errorf("plan %+v, want diff %v, a hash and no confirmation", out, tt.diff)
			}
			if slices.Contains(h.docker.calls(), "CreateVolume") {
				t.Error("a plan created the volume")
			}
		})
	}
}

func TestObVolumeCreateApply(t *testing.T) {
	ctx := context.Background()
	t.Run("creates and journals", func(t *testing.T) {
		h := newDKHost(t)
		k := h.dk()
		rec := &obRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		raw, err := k.volumeCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data","labels":{"team":"a"}}`))
		if err != nil {
			t.Fatal(err)
		}
		if want := []dockerapi.VolumeCreate{{Name: "data", Driver: "local", Labels: map[string]string{"team": "a"}}}; !reflect.DeepEqual(rec.volumes, want) {
			t.Errorf("CreateVolume requests %+v, want %+v", rec.volumes, want)
		}
		var out Created
		if err := json.Unmarshal(raw, &out); err != nil || out != (Created{ID: "data", Name: "data"}) {
			t.Errorf("output %s (%v), want id data name data", raw, err)
		}
		made, _ := h.docker.InspectVolume(ctx, "data")
		wantPost, _ := dstate.VolumeCreated(&made, true)
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		if e.Kind != core.EffectCreate || e.Resource != volumeResource("data") || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait {
			t.Errorf("effect %+v, want a made non-local create of %s with commit wait %s", e, volumeResource("data"), commitWait)
		}
		if e.Recovery == nil || *e.Recovery != (core.RecoveryID{Kind: "name", Value: "data"}) {
			t.Errorf("recovery %+v, want the name data", e.Recovery)
		}
		if string(e.Before.State) != "null" || e.Post == nil || string(e.Post.State) != string(wantPost) {
			t.Errorf("before %s post %+v, want null and %s", e.Before.State, e.Post, wantPost)
		}
	})
	t.Run("a name taken since the plan", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("data", nil))
		_, err := h.dk().volumeCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data"}`))
		_ = obRefused(t, err, errcode.InvalidInput)
		if len(h.journal.effects()) != 0 || slices.Contains(h.docker.calls(), "CreateVolume") {
			t.Error("a refused apply journaled an effect or called CreateVolume")
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.fail("CreateVolume", &dockerapi.APIError{StatusCode: 400, Message: "no such driver"})
		_, err := h.dk().volumeCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data"}`))
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped || effects[0].Resource != "docker.volume:?data" {
			t.Errorf("effects %+v, want one skipped creation of docker.volume:?data", effects)
		}
	})
	t.Run("the daemon fails", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.fail("CreateVolume", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := h.dk().volumeCreate().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data"}`))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("effects %+v, want one left intended", effects)
		}
	})
}

func TestObVolumeCreateUndo(t *testing.T) {
	ctx := context.Background()
	apply := func(t *testing.T, h *dkHost) (core.ActionDef, core.Effect) {
		t.Helper()
		def := h.dk().volumeCreate()
		if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"name":"data"}`)); err != nil {
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
		if _, err := h.docker.InspectVolume(ctx, "data"); !dockerapi.IsNotFound(err) {
			t.Errorf("the volume is still there: %v", err)
		}
		e.State = core.EffectUndone
		if obs, err := def.Handler.Observe(ctx, h.env("c1"), e); err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %s (%v), want null", obs.Current.State, err)
		}
	})
	t.Run("a container mounted it", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		h.docker.put(dkContainer("web", obMounts("data")))
		_, err := def.Handler.Inverse(ctx, h.env("c1"), e)
		if coded := obRefused(t, err, errcode.DockerInUse); !strings.Contains(coded.Detail, "web") {
			t.Errorf("detail %q does not name web", coded.Detail)
		}
		if _, err := h.docker.InspectVolume(ctx, "data"); err != nil {
			t.Errorf("the volume was removed: %v", err)
		}
	})
	t.Run("already gone", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		if err := h.docker.RemoveVolume(ctx, "data", false); err != nil {
			t.Fatal(err)
		}
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err != nil {
			t.Errorf("inverse of an absent volume: %v", err)
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		def, e := apply(t, h)
		h.docker.fail("RemoveVolume", &dockerapi.APIError{StatusCode: 409, Message: "in use"})
		if _, err := def.Handler.Inverse(ctx, h.env("c1"), e); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("a resource that is not a created volume", func(t *testing.T) {
		h := newDKHost(t)
		if _, err := h.dk().volumeCreate().Handler.Inverse(ctx, h.env("c1"), core.Effect{Resource: "docker.volume:?data"}); err == nil {
			t.Error("inverse of an unnamed creation succeeded")
		}
	})
}

func TestObVolumeCreateVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	def := h.dk().volumeCreate()
	in := json.RawMessage(`{"name":"data"}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, json.RawMessage(`{}`)); err == nil {
		t.Error("verify passed with no volume")
	}
	raw, err := def.Handler.Apply(ctx, h.env("c1"), in)
	if err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, raw); err != nil {
		t.Errorf("verify: %v", err)
	}
}

func TestObVolumeRemovePlan(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.putVolume(dkVolume("old", nil))
	h.docker.putVolume(dkVolume("npm-data", map[string]string{core.LabelModule: "npm"}))
	h.docker.putVolume(dkVolume("busy", nil))
	h.docker.put(dkContainer("b", obMounts("busy")))
	h.docker.put(dkContainer("a", obMounts("busy", "old2")))
	h.docker.put(dkContainer("c", obMounts("other")))
	def := h.dk().volumeRemove()
	plan := func(name string) (core.PlanOutcome, error) {
		return def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{"volume":"`+name+`"}`))
	}

	out, err := plan("old")
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "volume old"}, {Op: " ", Text: "the data in the volume is deleted and cannot be restored"}}
	if !slices.Equal(out.Diff, wantDiff) || out.Confirmation != "old" || out.StateHash == "" {
		t.Errorf("plan %+v, want diff %v, confirmation old and a hash", out, wantDiff)
	}
	_, err = plan("busy")
	if coded := obRefused(t, err, errcode.DockerInUse); !strings.Contains(coded.Detail, "a, b") {
		t.Errorf("detail %q does not name the containers a, b in order", coded.Detail)
	}
	_, err = plan("npm-data")
	_ = obRefused(t, err, errcode.DockerOwned)
	_, err = plan("ghost")
	_ = obRefused(t, err, errcode.NotFound)
	_, err = plan("")
	if coded := obRefused(t, err, errcode.InvalidInput); !obHasField(coded, "/volume") {
		t.Errorf("fields %v, want one at /volume", coded.Fields)
	}
	if slices.Contains(h.docker.calls(), "RemoveVolume") {
		t.Error("a plan removed a volume")
	}
	first, _ := plan("old")
	h.docker.putVolume(dkVolume("old", map[string]string{"k": "v"}))
	if second, _ := plan("old"); second.StateHash == first.StateHash {
		t.Error("the hash did not change with the volume's labels")
	}
}

func TestObVolumeRemoveApply(t *testing.T) {
	ctx := context.Background()
	in := json.RawMessage(`{"volume":"old"}`)
	t.Run("removes and journals", func(t *testing.T) {
		h := newDKHost(t)
		old := dkVolume("old", nil)
		h.docker.putVolume(old)
		raw, err := h.dk().volumeRemove().Handler.Apply(ctx, h.env("c1"), in)
		if err != nil {
			t.Fatal(err)
		}
		var out Removed
		if err := json.Unmarshal(raw, &out); err != nil || out != (Removed{ID: "old", Name: "old"}) {
			t.Errorf("output %s (%v), want id old name old", raw, err)
		}
		if _, err := h.docker.InspectVolume(ctx, "old"); !dockerapi.IsNotFound(err) {
			t.Errorf("the volume is still there: %v", err)
		}
		wantBefore, _ := dstate.VolumeCreated(&old, true)
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		if e.Kind != core.EffectDelete || e.Resource != volumeResource("old") || e.State != core.EffectMade || e.Local || e.CommitWait != commitWait ||
			string(e.Before.State) != string(wantBefore) || e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("effect %+v, want a made non-local delete from %s to null", e, wantBefore)
		}
	})
	t.Run("a container mounted it since the plan", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("old", nil))
		h.docker.put(dkContainer("web", obMounts("old")))
		_, err := h.dk().volumeRemove().Handler.Apply(ctx, h.env("c1"), in)
		_ = obRefused(t, err, errcode.DockerInUse)
		if len(h.journal.effects()) != 0 || slices.Contains(h.docker.calls(), "RemoveVolume") {
			t.Error("a refused apply journaled an effect or called RemoveVolume")
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("old", nil))
		h.docker.fail("RemoveVolume", &dockerapi.APIError{StatusCode: 409, Message: "in use"})
		_, err := h.dk().volumeRemove().Handler.Apply(ctx, h.env("c1"), in)
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
	})
	t.Run("the daemon fails, then settle observes", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putVolume(dkVolume("old", nil))
		def := h.dk().volumeRemove()
		h.docker.fail("RemoveVolume", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		e := h.journal.effects()[0]
		if e.State != core.EffectIntended {
			t.Fatalf("effect state %s, want intended", e.State)
		}
		if obs, err := def.Handler.Observe(ctx, h.env("c1"), e); err != nil || string(obs.Current.State) != string(e.Before.State) {
			t.Errorf("observation %s (%v), want the before-state %s", obs.Current.State, err, e.Before.State)
		}
		if err := h.docker.RemoveVolume(ctx, "old", false); err != nil {
			t.Fatal(err)
		}
		if obs, err := def.Handler.Observe(ctx, h.env("c1"), e); err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %s (%v), want null", obs.Current.State, err)
		}
	})
}

func TestObVolumeRemoveVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.putVolume(dkVolume("old", nil))
	def := h.dk().volumeRemove()
	in, out := json.RawMessage(`{"volume":"old"}`), json.RawMessage(`{"id":"old","name":"old"}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the volume still there")
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after the removal: %v", err)
	}
}

func TestObVolumeDefinitions(t *testing.T) {
	h := newDKHost(t)
	k := h.dk()
	create, remove := k.volumeCreate(), k.volumeRemove()
	if s := create.Spec; s.Name != "docker.volume.create" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) || s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("create spec %+v", s)
	}
	if create.Handler.Inverse == nil || create.Handler.Observe == nil {
		t.Error("create has no undo handler or observer")
	}
	if s := remove.Spec; s.Name != "docker.volume.remove" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) {
		t.Errorf("remove spec %+v", s)
	}
	if remove.Handler.Inverse != nil || remove.Handler.Observe == nil {
		t.Error("remove has an undo handler or no observer")
	}
	obCheckInputs(t, create, map[string]string{
		`{"name":"a"}`: "", `{"name":"a","driver":"local","labels":{"k":"v"}}`: "",
		`{}`: "/name", `{"name":"a","x":1}`: "/x", `{"name":"a","labels":"k"}`: "/labels",
	})
	obCheckInputs(t, remove, map[string]string{`{"volume":"a"}`: "", `{}`: "/volume", `{"volume":"a","x":1}`: "/x"})
}

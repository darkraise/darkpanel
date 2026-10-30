package docker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// rmRecorder passes every call to the wrapped client and records the options
// of RemoveContainer.
type rmRecorder struct {
	dockerAPI
	opts []dockerapi.RemoveContainerOptions
}

func (r *rmRecorder) RemoveContainer(ctx context.Context, id string, o dockerapi.RemoveContainerOptions) error {
	r.opts = append(r.opts, o)
	return r.dockerAPI.RemoveContainer(ctx, id, o)
}

// rmModule registers one action, so the registry's own checks run on it.
type rmModule struct{ def core.ActionDef }

func (m rmModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: []core.ActionDef{m.def}}
}

// rmRefused asserts err is a definite refusal with the given code and returns it.
func rmRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
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

func TestRMPlan(t *testing.T) {
	ctx := context.Background()
	id12 := dkID("web")[:12]
	owned := dkLabels(map[string]string{core.LabelModule: "npm"})
	tests := []struct {
		name    string
		put     []func(*dockerapi.ContainerJSON)
		input   string
		code    errcode.Code // "" when the plan is accepted
		field   string       // the field a refusal names
		diff    []core.DiffLine
		confirm string
	}{
		{name: "stopped by name", input: `{"container":"web"}`, confirm: "web", diff: []core.DiffLine{
			{Op: "-", Text: "container web (" + id12 + ")"},
			{Op: " ", Text: "its writable layer is deleted and cannot be restored"},
		}},
		{name: "stopped by id", input: `{"container":"` + dkID("web") + `"}`, confirm: "web", diff: []core.DiffLine{
			{Op: "-", Text: "container web (" + id12 + ")"},
			{Op: " ", Text: "its writable layer is deleted and cannot be restored"},
		}},
		{name: "running with force and volumes", put: []func(*dockerapi.ContainerJSON){dkRunning},
			input: `{"container":"web","force":true,"volumes":true}`, confirm: "web", diff: []core.DiffLine{
				{Op: "-", Text: "container web (" + id12 + ")"},
				{Op: " ", Text: "its writable layer is deleted and cannot be restored"},
				{Op: " ", Text: "the running container is killed first"},
				{Op: " ", Text: "its anonymous volumes are deleted"},
			}},
		{name: "running without force", put: []func(*dockerapi.ContainerJSON){dkRunning},
			input: `{"container":"web"}`, code: errcode.InvalidInput, field: "/force"},
		{name: "empty name", input: `{"container":""}`, code: errcode.InvalidInput, field: "/container"},
		{name: "absent", input: `{"container":"ghost"}`, code: errcode.NotFound},
		{name: "owned", put: []func(*dockerapi.ContainerJSON){owned}, input: `{"container":"web"}`, code: errcode.DockerOwned},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.put(dkContainer("web", tt.put...))
			def := h.dk().containerRemove()
			out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(tt.input))
			if tt.code != "" {
				coded := rmRefused(t, err, tt.code)
				if tt.field != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
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
			if out.Confirmation != tt.confirm {
				t.Errorf("confirmation %q, want %q", out.Confirmation, tt.confirm)
			}
			if slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Error("a plan removed the container")
			}
		})
	}
}

// TestRMPlanStateHash: the hash is stable while the container is, and changes
// when the container starts, so a plan made before a start is refused.
func TestRMPlanStateHash(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.put(dkContainer("web"))
	h.docker.put(dkContainer("db"))
	def := h.dk().containerRemove()
	plan := func(name string) string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{"container":"`+name+`","force":true}`))
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan %s: hash %q, error %v", name, out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan("web")
	if again := plan("web"); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	if other := plan("db"); other == first {
		t.Error("two containers hash alike")
	}
	if _, err := h.docker.StartContainer(ctx, "web"); err != nil {
		t.Fatal(err)
	}
	if started := plan("web"); started == first {
		t.Error("the hash did not change when the container started")
	}
}

func TestRMApply(t *testing.T) {
	ctx := context.Background()
	t.Run("stopped container", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.put(dkContainer("web"))
		before, _ := h.docker.container("web")
		def := h.dk().containerRemove()
		raw, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`))
		if err != nil {
			t.Fatal(err)
		}
		var out Removed
		if err := json.Unmarshal(raw, &out); err != nil || out != (Removed{ID: before.ID, Name: "web"}) {
			t.Errorf("output %s (%v), want id %s name web", raw, err, before.ID)
		}
		if _, ok := h.docker.container("web"); ok {
			t.Error("the container is still there")
		}
		effects := h.journal.effects()
		if len(effects) != 1 {
			t.Fatalf("%d effects, want 1", len(effects))
		}
		e := effects[0]
		wantBefore, err := dstate.ContainerRun(before, dstate.Recorded)
		if err != nil {
			t.Fatal(err)
		}
		if e.Kind != core.EffectDelete || e.Resource != containerResource(before.ID) || e.State != core.EffectMade ||
			e.Local || e.CommitWait != commitWait {
			t.Errorf("effect %+v, want a made non-local delete of %s with commit wait %s", e, containerResource(before.ID), commitWait)
		}
		if string(e.Before.State) != string(wantBefore) {
			t.Errorf("before %s, want %s", e.Before.State, wantBefore)
		}
		if e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("post %+v, want the absent state null", e.Post)
		}
	})
	t.Run("running container with force and volumes", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.put(dkContainer("web", dkRunning))
		k := h.dk()
		rec := &rmRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		if _, err := k.containerRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web","force":true,"volumes":true}`)); err != nil {
			t.Fatal(err)
		}
		if want := []dockerapi.RemoveContainerOptions{{Force: true, Volumes: true}}; !slices.Equal(rec.opts, want) {
			t.Errorf("remove options %v, want %v", rec.opts, want)
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.put(dkContainer("web"))
		h.docker.fail("RemoveContainer", &dockerapi.APIError{StatusCode: 409, Message: "removal in progress"})
		_, err := h.dk().containerRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`))
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a definite refusal", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped", effects)
		}
		if _, ok := h.docker.container("web"); !ok {
			t.Error("the container is gone")
		}
	})
	t.Run("the daemon fails", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.put(dkContainer("web"))
		h.docker.fail("RemoveContainer", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := h.dk().containerRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`))
		if err == nil || core.IsDefinite(err) {
			t.Fatalf("error %v, want an uncertain failure", err)
		}
		if effects := h.journal.effects(); len(effects) != 1 || effects[0].State != core.EffectIntended {
			t.Errorf("effects %+v, want one left intended", effects)
		}
	})
	t.Run("refused again at apply", func(t *testing.T) {
		for name, tt := range map[string]struct {
			opts []func(*dockerapi.ContainerJSON)
			code errcode.Code
		}{
			"owned":   {[]func(*dockerapi.ContainerJSON){dkLabels(map[string]string{core.LabelModule: "npm"})}, errcode.DockerOwned},
			"running": {[]func(*dockerapi.ContainerJSON){dkRunning}, errcode.InvalidInput},
		} {
			h := newDKHost(t)
			h.docker.put(dkContainer("web", tt.opts...))
			_, err := h.dk().containerRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`))
			_ = rmRefused(t, err, tt.code)
			if len(h.journal.effects()) != 0 || slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Errorf("%s: apply journaled an effect or called RemoveContainer", name)
			}
		}
	})
}

func TestRMVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.put(dkContainer("web"))
	def := h.dk().containerRemove()
	id := dkID("web")
	present := def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`), json.RawMessage(`{"id":"`+id+`","name":"web"}`))
	if present == nil {
		t.Error("verify passed with the container still there")
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`)); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`), json.RawMessage(`{"id":"`+id+`","name":"web"}`)); err != nil {
		t.Errorf("verify after the removal: %v", err)
	}
	h.docker.put(dkContainer("web"))
	if err := def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`), json.RawMessage(`{}`)); err == nil {
		t.Error("verify without an output passed with a container of that name present")
	}
}

// TestRMObserve: a settle finds the container's recorded state while it is
// there and the absent state once it is removed.
func TestRMObserve(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.put(dkContainer("web"))
	def := h.dk().containerRemove()
	h.docker.fail("RemoveContainer", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
	if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"container":"web"}`)); err == nil {
		t.Fatal("apply succeeded")
	}
	e := h.journal.effects()[0]
	obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
	if err != nil || string(obs.Current.State) != string(e.Before.State) {
		t.Errorf("observation %s (%v), want the journaled before-state %s", obs.Current.State, err, e.Before.State)
	}
	if _, err := def.Handler.Apply(ctx, h.env("c2"), json.RawMessage(`{"container":"web"}`)); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, h.env("c2"), e)
	if err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation %s (%v), want null", obs.Current.State, err)
	}
}

func TestRMDefinition(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().containerRemove()
	s := def.Spec
	if s.Name != "docker.container.remove" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse != nil || def.Handler.Observe == nil {
		t.Errorf("inverse %v observe %v, want no inverse and an observer", def.Handler.Inverse != nil, def.Handler.Observe != nil)
	}
	if _, err := core.NewRegistry(rmModule{def}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	v := schema.NewValidator()
	for input, field := range map[string]string{
		`{"container":"web"}`:                             "",
		`{"container":"web","force":true,"volumes":true}`: "",
		`{}`:                                "/container",
		`{"container":"web","x":1}`:         "/x",
		`{"container":"web","force":"yes"}`: "/force",
	} {
		err := v.ValidateInput(def, json.RawMessage(input))
		var coded *errcode.Error
		switch {
		case field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", input, err)
		case field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })):
			t.Errorf("input %s: %v, want a refusal naming %s", input, err, field)
		}
	}
}

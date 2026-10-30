package docker

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// imRecorder passes every call to the wrapped client and records the
// arguments of RemoveImage; with fixed set it answers the given items.
type imRecorder struct {
	dockerAPI
	refs  []string
	opts  []dockerapi.RemoveImageOptions
	fixed []dockerapi.ImageDeleteItem
	set   bool
}

func (r *imRecorder) RemoveImage(ctx context.Context, ref string, o dockerapi.RemoveImageOptions) ([]dockerapi.ImageDeleteItem, error) {
	r.refs = append(r.refs, ref)
	r.opts = append(r.opts, o)
	items, err := r.dockerAPI.RemoveImage(ctx, ref, o)
	if r.set {
		return r.fixed, err
	}
	return items, err
}

// imModule registers actions, so the registry's own checks run on them.
type imModule struct{ defs []core.ActionDef }

func (m imModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

// imUses makes a container run the image with the given id.
func imUses(id string, running bool) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		c.Image = id
		c.State.Running = running
		c.State.Status = "exited"
		if running {
			c.State.Status = "running"
		}
	}
}

// imRefused asserts err is a definite refusal with the given code and returns it.
func imRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
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

func TestImRemovePlan(t *testing.T) {
	ctx := context.Background()
	img := dkImage("nginx:1.27", 100)
	tests := []struct {
		name    string
		users   []func(*dockerapi.ContainerJSON)
		input   string
		code    errcode.Code // "" when the plan is accepted
		field   string
		detail  string // text the refusal's detail holds
		extra   []core.DiffLine
		confirm string
	}{
		{name: "unused", input: `{"image":"nginx:1.27"}`, confirm: "nginx:1.27"},
		{name: "stopped container uses it", users: []func(*dockerapi.ContainerJSON){imUses(img.ID, false)},
			input: `{"image":"nginx:1.27"}`, code: errcode.DockerInUse, detail: "web"},
		{name: "stopped container uses it, forced", users: []func(*dockerapi.ContainerJSON){imUses(img.ID, false)},
			input: `{"image":"nginx:1.27","force":true}`, confirm: "nginx:1.27",
			extra: []core.DiffLine{{Op: " ", Text: "stopped containers keep a reference to the deleted image: web"}}},
		{name: "running container uses it, forced", users: []func(*dockerapi.ContainerJSON){imUses(img.ID, true)},
			input: `{"image":"nginx:1.27","force":true}`, code: errcode.DockerInUse, detail: "web"},
		{name: "by id", input: `{"image":"` + img.ID + `"}`, confirm: img.ID},
		{name: "absent", input: `{"image":"ghost:1"}`, code: errcode.NotFound},
		{name: "empty", input: `{"image":""}`, code: errcode.InvalidInput, field: "/image"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newDKHost(t)
			h.docker.putImage(img)
			if tt.users != nil {
				h.docker.put(dkContainer("web", tt.users...))
			}
			def := h.dk().imageRemove()
			out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(tt.input))
			if tt.code != "" {
				coded := imRefused(t, err, tt.code)
				if tt.detail != "" && !strings.Contains(coded.Detail, tt.detail) {
					t.Errorf("detail %q does not name %q", coded.Detail, tt.detail)
				}
				if tt.field != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.field }) {
					t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			want := append([]core.DiffLine{
				{Op: "-", Text: "image " + tt.confirm},
				{Op: " ", Text: "id " + img.ID},
				{Op: " ", Text: "size 100 bytes"},
			}, tt.extra...)
			if !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if out.Confirmation != tt.confirm {
				t.Errorf("confirmation %q, want %q", out.Confirmation, tt.confirm)
			}
		})
	}
}

// TestImRemoveStateHash: the hash is stable while the image and the
// containers that use it are, and changes when a container starts using it.
func TestImRemoveStateHash(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	img := dkImage("nginx:1.27", 100)
	h.docker.putImage(img)
	def := h.dk().imageRemove()
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{"image":"nginx:1.27","force":true}`))
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	h.docker.put(dkContainer("web", imUses(img.ID, false)))
	if used := plan(); used == first {
		t.Error("the hash did not change when a container began to use the image")
	}
}

func TestImRemoveApply(t *testing.T) {
	ctx := context.Background()
	t.Run("removes and reports", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putImage(dkImage("nginx:1.27", 100))
		k := h.dk()
		rec := &imRecorder{dockerAPI: k.d.docker, set: true, fixed: []dockerapi.ImageDeleteItem{{Untagged: "nginx:1.27"}, {Deleted: "sha256:aaa"}, {Deleted: "sha256:bbb"}}}
		k.d.docker = rec
		raw, err := k.imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27"}`))
		if err != nil {
			t.Fatal(err)
		}
		if want := `{"untagged":["nginx:1.27"],"deleted":["sha256:aaa","sha256:bbb"]}`; string(raw) != want {
			t.Errorf("output %s, want %s", raw, want)
		}
		if !slices.Equal(rec.refs, []string{"nginx:1.27"}) || !slices.Equal(rec.opts, []dockerapi.RemoveImageOptions{{}}) {
			t.Errorf("RemoveImage called with %v %v, want the reference as given and no options", rec.refs, rec.opts)
		}
		if len(h.journal.effects()) != 0 {
			t.Error("an image removal journaled an effect; spec 3 has no comparator for images")
		}
	})
	t.Run("an empty answer reports empty lists", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putImage(dkImage("nginx:1.27", 100))
		k := h.dk()
		k.d.docker = &imRecorder{dockerAPI: k.d.docker, set: true, fixed: []dockerapi.ImageDeleteItem{}}
		raw, err := k.imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27","force":true}`))
		if want := `{"untagged":[],"deleted":[]}`; err != nil || string(raw) != want {
			t.Errorf("output %s (%v), want %s", raw, err, want)
		}
	})
	t.Run("force is passed on", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putImage(dkImage("nginx:1.27", 100))
		k := h.dk()
		rec := &imRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		if _, err := k.imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27","force":true}`)); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(rec.opts, []dockerapi.RemoveImageOptions{{Force: true}}) {
			t.Errorf("options %v, want force", rec.opts)
		}
	})
	t.Run("the daemon refuses", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putImage(dkImage("nginx:1.27", 100))
		h.docker.fail("RemoveImage", &dockerapi.APIError{StatusCode: 409, Message: "conflict"})
		_, err := h.dk().imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27"}`))
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("the daemon fails", func(t *testing.T) {
		h := newDKHost(t)
		h.docker.putImage(dkImage("nginx:1.27", 100))
		h.docker.fail("RemoveImage", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		_, err := h.dk().imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27"}`))
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want an uncertain failure", err)
		}
	})
	t.Run("refused again at apply", func(t *testing.T) {
		h := newDKHost(t)
		img := dkImage("nginx:1.27", 100)
		h.docker.putImage(img)
		h.docker.put(dkContainer("web", imUses(img.ID, true)))
		k := h.dk()
		rec := &imRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		_, err := k.imageRemove().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{"image":"nginx:1.27","force":true}`))
		_ = imRefused(t, err, errcode.DockerInUse)
		if len(rec.refs) != 0 {
			t.Error("RemoveImage was called")
		}
	})
}

func TestImRemoveVerify(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.putImage(dkImage("nginx:1.27", 100))
	def := h.dk().imageRemove()
	in, out := json.RawMessage(`{"image":"nginx:1.27"}`), json.RawMessage(`{"untagged":[],"deleted":[]}`)
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err == nil {
		t.Error("verify passed with the image still there")
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), in); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), in, out); err != nil {
		t.Errorf("verify after the removal: %v", err)
	}
}

func TestImRemoveDefinition(t *testing.T) {
	h := newDKHost(t)
	def := h.dk().imageRemove()
	s := def.Spec
	if s.Name != "docker.image.remove" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) {
		t.Errorf("spec %+v", s)
	}
	if _, err := core.NewRegistry(imModule{[]core.ActionDef{def}}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	imCheckInputs(t, def, map[string]string{
		`{"image":"nginx"}`: "", `{"image":"nginx","force":true}`: "", `{}`: "/image", `{"image":"nginx","x":1}`: "/x",
	})
}

// imCheckInputs validates each input against def's schema: field "" means
// accepted, otherwise the refusal names that field.
func imCheckInputs(t *testing.T, def core.ActionDef, inputs map[string]string) {
	t.Helper()
	v := schema.NewValidator()
	for input, field := range inputs {
		err := v.ValidateInput(def, json.RawMessage(input))
		var coded *errcode.Error
		switch {
		case field == "" && err != nil:
			t.Errorf("%s input %s: %v, want it accepted", def.Spec.Name, input, err)
		case field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })):
			t.Errorf("%s input %s: %v, want a refusal naming %s", def.Spec.Name, input, err, field)
		}
	}
}

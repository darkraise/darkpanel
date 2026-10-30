package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// imPruneHost holds two dangling images (one sharing 100 of its 300 bytes),
// a tagged image, and a dangling image a container uses. dangling lists the
// two prunable images in the order the plan shows them: by id.
type imPruneHost struct {
	*dkHost
	dangling []dockerapi.ImageSummary
	used     dockerapi.ImageSummary
}

func imPruneSetup(t *testing.T) *imPruneHost {
	t.Helper()
	h := &imPruneHost{dkHost: newDKHost(t)}
	old := dkImage("old", 300)
	old.RepoTags, old.SharedSize = nil, 100
	older := dkImage("older", 50)
	older.RepoTags = nil
	tagged := dkImage("tagged:1", 999)
	h.used = dkImage("used", 70)
	h.used.RepoTags = nil
	h.dangling = []dockerapi.ImageSummary{old, older}
	slices.SortFunc(h.dangling, func(a, b dockerapi.ImageSummary) int { return strings.Compare(a.ID, b.ID) })
	// The daemon lists the larger id first, so the plan has to sort.
	for _, i := range []dockerapi.ImageSummary{h.dangling[1], tagged, h.used, h.dangling[0]} {
		h.docker.putImage(i)
	}
	h.docker.put(dkContainer("web", imUses(h.used.ID, false)))
	return h
}

func imSize(i dockerapi.ImageSummary) int64 { return i.Size - max(i.SharedSize, 0) }

func TestImPrunePlan(t *testing.T) {
	ctx := context.Background()
	h := imPruneSetup(t)
	out, err := h.dk().imagePrune().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	var want []core.DiffLine
	for _, i := range h.dangling {
		want = append(want, core.DiffLine{Op: "-", Text: fmt.Sprintf("image %s (%d bytes)", i.ID, imSize(i))})
	}
	want = append(want, core.DiffLine{Op: " ", Text: "reclaims 250 bytes from 2 images"})
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %v, want %v", out.Diff, want)
	}
	if out.Confirmation != "prune" || out.StateHash == "" {
		t.Errorf("confirmation %q hash %q, want prune and a hash", out.Confirmation, out.StateHash)
	}
	if slices.Contains(h.docker.calls(), "RemoveImage") {
		t.Error("a plan removed an image")
	}
}

func TestImPruneNothingToRemove(t *testing.T) {
	ctx := context.Background()
	h := newDKHost(t)
	h.docker.putImage(dkImage("tagged:1", 10))
	out, err := h.dk().imagePrune().Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
	if want := []core.DiffLine{{Op: " ", Text: "no unused dangling images to remove"}}; err != nil || !slices.Equal(out.Diff, want) {
		t.Errorf("diff %v (%v), want %v", out.Diff, err, want)
	}
}

// TestImPruneStateHash: the hash changes when the list to remove changes.
func TestImPruneStateHash(t *testing.T) {
	ctx := context.Background()
	h := imPruneSetup(t)
	def := h.dk().imagePrune()
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(ctx, h.env(""), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	h.docker.put(dkContainer("api", imUses(h.dangling[0].ID, false)))
	if changed := plan(); changed == first {
		t.Error("the hash did not change when a container began to use a listed image")
	}
}

func TestImPruneApply(t *testing.T) {
	ctx := context.Background()
	t.Run("removes exactly the planned list", func(t *testing.T) {
		h := imPruneSetup(t)
		k := h.dk()
		rec := &imRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		raw, err := k.imagePrune().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var ids []string
		var want Pruned
		for _, i := range h.dangling {
			ids = append(ids, i.ID)
			want.Images = append(want.Images, PrunedImage{ID: i.ID, Tags: []string{}, Size: imSize(i)})
		}
		want.Reclaimed = 250
		wantJSON, _ := json.Marshal(want)
		if string(raw) != string(wantJSON) {
			t.Errorf("output %s, want %s", raw, wantJSON)
		}
		if !slices.Equal(rec.refs, ids) {
			t.Errorf("removed %v, want %v", rec.refs, ids)
		}
		left, _ := h.docker.ListImages(ctx, dockerapi.ListImagesOptions{All: true})
		if len(left) != 2 {
			t.Errorf("%d images left, want the tagged one and the one a container uses", len(left))
		}
		if len(h.journal.effects()) != 0 {
			t.Error("a prune journaled an effect; spec 3 has no comparator for images")
		}
	})
	t.Run("an image that vanished is skipped", func(t *testing.T) {
		h := imPruneSetup(t)
		h.docker.fail("RemoveImage", &dockerapi.APIError{StatusCode: 404, Message: "no such image"})
		raw, err := h.dk().imagePrune().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var out Pruned
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Images) != 1 || out.Images[0].ID != h.dangling[1].ID || out.Reclaimed != imSize(h.dangling[1]) {
			t.Errorf("output %s (%v), want only the second image", raw, err)
		}
	})
	t.Run("the daemon fails", func(t *testing.T) {
		h := imPruneSetup(t)
		h.docker.fail("RemoveImage", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		k := h.dk()
		rec := &imRecorder{dockerAPI: k.d.docker}
		k.d.docker = rec
		_, err := k.imagePrune().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want an uncertain failure", err)
		}
		if len(rec.refs) != 1 {
			t.Errorf("%d RemoveImage calls, want the run to stop after the first", len(rec.refs))
		}
	})
	t.Run("a refusal before any removal is definite", func(t *testing.T) {
		h := imPruneSetup(t)
		h.docker.fail("RemoveImage", &dockerapi.APIError{StatusCode: 409, Message: "conflict"})
		_, err := h.dk().imagePrune().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite refusal", err)
		}
	})
	t.Run("a refusal after a removal is uncertain", func(t *testing.T) {
		h := imPruneSetup(t)
		k := h.dk()
		k.d.docker = &imRefuseAfter{dockerAPI: k.d.docker, after: 1, err: &dockerapi.APIError{StatusCode: 409, Message: "conflict"}}
		_, err := k.imagePrune().Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`))
		if err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want an uncertain failure", err)
		}
		if _, ierr := h.docker.InspectImage(ctx, h.dangling[0].ID); !dockerapi.IsNotFound(ierr) {
			t.Errorf("first image: %v, want it removed", ierr)
		}
	})
}

// imRefuseAfter passes the first after RemoveImage calls to the wrapped
// client and answers err to every later one.
type imRefuseAfter struct {
	dockerAPI
	after int
	err   error
}

func (r *imRefuseAfter) RemoveImage(ctx context.Context, ref string, o dockerapi.RemoveImageOptions) ([]dockerapi.ImageDeleteItem, error) {
	if r.after == 0 {
		return nil, r.err
	}
	r.after--
	return r.dockerAPI.RemoveImage(ctx, ref, o)
}

func TestImPruneVerify(t *testing.T) {
	ctx := context.Background()
	h := imPruneSetup(t)
	def := h.dk().imagePrune()
	out, _ := json.Marshal(Pruned{Images: []PrunedImage{{ID: h.dangling[0].ID, Tags: []string{}, Size: 1}}, Reclaimed: 1})
	if err := def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), out); err == nil {
		t.Error("verify passed with a pruned image still there")
	}
	if _, err := def.Handler.Apply(ctx, h.env("c1"), json.RawMessage(`{}`)); err != nil {
		t.Fatal(err)
	}
	if err := def.Handler.Verify(ctx, h.env("c1"), json.RawMessage(`{}`), out); err != nil {
		t.Errorf("verify after the prune: %v", err)
	}
}

func TestImPruneDefinition(t *testing.T) {
	h := newDKHost(t)
	k := h.dk()
	def := k.imagePrune()
	s := def.Spec
	if s.Name != "docker.image.prune" || s.Version != 1 || s.Class != core.ClassIrreversible || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"docker"}) {
		t.Errorf("spec %+v", s)
	}
	if _, err := core.NewRegistry(imModule{[]core.ActionDef{def, k.imageRemove()}}); err != nil {
		t.Errorf("the registry refuses the actions: %v", err)
	}
	imCheckInputs(t, def, map[string]string{`{}`: "", `{"x":1}`: "/x"})
}

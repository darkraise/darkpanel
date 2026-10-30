package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) imagePrune() core.ActionDef {
	return core.DefineMutating[ImagePruneInput, Pruned](core.ActionSpec{
		Name: ID + ".image.prune", Summary: "Remove the dangling images no container uses.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey},
	}, imagePruneHandler{k: k})
}

// imagePruneSize is the bytes removing i alone reclaims. The daemon sends a
// SharedSize of -1 when it did not compute it.
func imagePruneSize(i dockerapi.ImageSummary) int64 {
	if i.SharedSize > 0 {
		return i.Size - i.SharedSize
	}
	return i.Size
}

// imagePruneHandler removes the dangling images no container uses, one by
// one, so the list it removes is the list the plan showed. Removal cannot be
// undone, so it has no Inverse, and no comparator covers images, so it
// journals no effect.
type imagePruneHandler struct{ k dk }

// list returns the prunable images sorted by id.
func (h imagePruneHandler) list(ctx context.Context) ([]dockerapi.ImageSummary, error) {
	images, err := h.k.d.docker.ListImages(ctx, dockerapi.ListImagesOptions{
		SharedSize: true, Filters: dockerapi.Filters{"dangling": {"true"}},
	})
	if err != nil {
		return nil, engineError("images", "", err)
	}
	cs, err := h.k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return nil, engineError("containers", "", err)
	}
	used := make(map[string]bool, len(cs))
	for _, c := range cs {
		used[c.ImageID] = true
	}
	var out []dockerapi.ImageSummary
	for _, i := range images {
		// The tag check does not trust the dangling filter, so a daemon that
		// ignores it cannot make the prune remove a tagged image.
		if used[i.ID] || len(resWithout(i.RepoTags, "<none>:<none>")) > 0 {
			continue
		}
		out = append(out, i)
	}
	slices.SortFunc(out, func(a, b dockerapi.ImageSummary) int { return strings.Compare(a.ID, b.ID) })
	return out, nil
}

func (h imagePruneHandler) Plan(ctx context.Context, _ core.Env, _ ImagePruneInput) (core.PlanOutcome, error) {
	images, err := h.list(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	var state strings.Builder
	var total int64
	for _, i := range images {
		size := imagePruneSize(i)
		total += size
		diff = append(diff, core.DiffLine{Op: "-", Text: fmt.Sprintf("image %s (%d bytes)", i.ID, size)})
		fmt.Fprintf(&state, "%s %d\n", i.ID, size)
	}
	if len(images) == 0 {
		diff = []core.DiffLine{{Op: " ", Text: "no unused dangling images to remove"}}
	} else {
		diff = append(diff, core.DiffLine{Op: " ", Text: fmt.Sprintf("reclaims %d bytes from %d images", total, len(images))})
	}
	sum := sha256.Sum256([]byte(state.String()))
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:]), Confirmation: "prune"}, nil
}

// Apply stops at the first failure other than not found; the images removed
// before it stay removed.
func (h imagePruneHandler) Apply(ctx context.Context, _ core.Env, _ ImagePruneInput) (Pruned, error) {
	images, err := h.list(ctx)
	if err != nil {
		return Pruned{}, err
	}
	out := Pruned{Images: []PrunedImage{}}
	for _, i := range images {
		if _, err := h.k.d.docker.RemoveImage(ctx, i.ID, dockerapi.RemoveImageOptions{}); err != nil {
			if dockerapi.IsNotFound(err) {
				continue
			}
			return Pruned{}, imageCallError("image", i.ID, err)
		}
		size := imagePruneSize(i)
		out.Images = append(out.Images, PrunedImage{ID: i.ID, Tags: resWithout(i.RepoTags, "<none>:<none>"), Size: size})
		out.Reclaimed += size
	}
	return out, nil
}

// Verify checks the images out names; recovery passes the zero output, which
// names none.
func (h imagePruneHandler) Verify(ctx context.Context, _ core.Env, _ ImagePruneInput, out Pruned) error {
	for _, i := range out.Images {
		_, err := h.k.d.docker.InspectImage(ctx, i.ID)
		switch {
		case err == nil:
			return fmt.Errorf("docker: image %s still exists", i.ID)
		case dockerapi.IsNotFound(err):
		default:
			return engineError("image", i.ID, err)
		}
	}
	return nil
}

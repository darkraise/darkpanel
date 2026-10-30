package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) imageRemove() core.ActionDef {
	return core.DefineMutating[ImageRemoveInput, RemovedImage](core.ActionSpec{
		Name: ID + ".image.remove", Summary: "Remove an image or one of its tags.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideAgent, LockKeys: []string{lockKey},
	}, imageRemoveHandler{k: k})
}

// imageCallError maps the error of a call that removes an image. A 4xx
// answer means the daemon refused before changing anything, so it is
// Definite; any other failure leaves the call uncertain.
func imageCallError(kind, name string, err error) error {
	if err == nil {
		return nil
	}
	mapped := engineError(kind, name, err)
	var refused *dockerapi.APIError
	if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
		return core.Definite(mapped)
	}
	return mapped
}

// imageUsers lists the containers that run one image.
type imageUsers struct {
	running []string // names, sorted
	stopped []string // names, sorted
	ids     []string // ids of all of them, sorted
}

func (k dk) imageUsersOf(ctx context.Context, imageID string) (imageUsers, error) {
	cs, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return imageUsers{}, engineError("image", imageID, err)
	}
	var u imageUsers
	for _, c := range cs {
		if c.ImageID != imageID {
			continue
		}
		u.ids = append(u.ids, c.ID)
		if c.State == "running" {
			u.running = append(u.running, resContainerName(c.Names))
		} else {
			u.stopped = append(u.stopped, resContainerName(c.Names))
		}
	}
	slices.Sort(u.running)
	slices.Sort(u.stopped)
	slices.Sort(u.ids)
	return u, nil
}

// imageRemoveHandler deletes one image, or untags it when the reference is
// one of several tags. Removal cannot be undone, so it has no Inverse, and
// no comparator covers images, so it journals no effect.
type imageRemoveHandler struct{ k dk }

// target reads the image in names and the containers that use it, and
// refuses what the removal must not touch. Nothing has changed when it
// fails, so every failure is Definite.
func (h imageRemoveHandler) target(ctx context.Context, in ImageRemoveInput) (dockerapi.ImageInspect, imageUsers, error) {
	if in.Image == "" {
		msg := "image is required"
		return dockerapi.ImageInspect{}, imageUsers{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/image", Message: msg}}})
	}
	img, err := h.k.d.docker.InspectImage(ctx, in.Image)
	if err != nil {
		return dockerapi.ImageInspect{}, imageUsers{}, core.Definite(engineError("image", in.Image, err))
	}
	users, err := h.k.imageUsersOf(ctx, img.ID)
	if err != nil {
		return dockerapi.ImageInspect{}, imageUsers{}, core.Definite(err)
	}
	// The daemon refuses to remove an image a running container uses even
	// with force, so the refusal does not depend on in.Force.
	if len(users.running) > 0 {
		return dockerapi.ImageInspect{}, imageUsers{}, core.Definite(&errcode.Error{Code: errcode.DockerInUse,
			Detail: "image " + in.Image + " is used by running containers: " + strings.Join(users.running, ", ")})
	}
	if len(users.stopped) > 0 && !in.Force {
		return dockerapi.ImageInspect{}, imageUsers{}, core.Definite(&errcode.Error{Code: errcode.DockerInUse,
			Detail: "image " + in.Image + " is used by stopped containers: " + strings.Join(users.stopped, ", ") +
				"; set force to remove it anyway"})
	}
	return img, users, nil
}

func (h imageRemoveHandler) Plan(ctx context.Context, _ core.Env, in ImageRemoveInput) (core.PlanOutcome, error) {
	img, users, err := h.target(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := json.Marshal(struct {
		ID         string   `json:"id"`
		Tags       []string `json:"tags"`
		Containers []string `json:"containers"`
	}{ID: img.ID, Tags: slices.Sorted(slices.Values(img.RepoTags)), Containers: users.ids})
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render image %s: %w", img.ID, err)
	}
	sum := sha256.Sum256(doc)
	diff := []core.DiffLine{
		{Op: "-", Text: "image " + in.Image},
		{Op: " ", Text: "id " + img.ID},
		{Op: " ", Text: "size " + strconv.FormatInt(img.Size, 10) + " bytes"},
	}
	if len(users.stopped) > 0 {
		diff = append(diff, core.DiffLine{Op: " ",
			Text: "stopped containers keep a reference to the deleted image: " + strings.Join(users.stopped, ", ")})
	}
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:]), Confirmation: in.Image}, nil
}

// Apply removes by the reference as given, so a reference that is one of
// several tags only untags the image.
func (h imageRemoveHandler) Apply(ctx context.Context, _ core.Env, in ImageRemoveInput) (RemovedImage, error) {
	if _, _, err := h.target(ctx, in); err != nil {
		return RemovedImage{}, err
	}
	items, err := h.k.d.docker.RemoveImage(ctx, in.Image, dockerapi.RemoveImageOptions{Force: in.Force})
	if err != nil {
		return RemovedImage{}, imageCallError("image", in.Image, err)
	}
	out := RemovedImage{Untagged: []string{}, Deleted: []string{}}
	for _, item := range items {
		if item.Untagged != "" {
			out.Untagged = append(out.Untagged, item.Untagged)
		}
		if item.Deleted != "" {
			out.Deleted = append(out.Deleted, item.Deleted)
		}
	}
	return out, nil
}

func (h imageRemoveHandler) Verify(ctx context.Context, _ core.Env, in ImageRemoveInput, _ RemovedImage) error {
	_, err := h.k.d.docker.InspectImage(ctx, in.Image)
	switch {
	case err == nil:
		return fmt.Errorf("docker: image %s still exists", in.Image)
	case dockerapi.IsNotFound(err):
		return nil
	default:
		return engineError("image", in.Image, err)
	}
}

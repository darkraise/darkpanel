package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) containerRemove() core.ActionDef {
	return core.DefineMutating[ContainerRemoveInput, Removed](core.ActionSpec{
		Name: ID + ".container.remove", Summary: "Remove a container.", Version: 1, Class: core.ClassIrreversible,
		Side: core.SideAgent, LockKeys: []string{lockKey},
	}, containerRemoveHandler{k: k})
}

// containerRemoveHandler deletes one container. Removal cannot be undone, so
// it has no Inverse.
type containerRemoveHandler struct{ k dk }

// target reads the container in names and refuses what removal must not
// touch. Nothing has changed when it fails, so every failure is Definite.
func (h containerRemoveHandler) target(ctx context.Context, in ContainerRemoveInput) (dockerapi.ContainerJSON, error) {
	if in.Container == "" {
		msg := "container is required"
		return dockerapi.ContainerJSON{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/container", Message: msg}}})
	}
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return dockerapi.ContainerJSON{}, core.Definite(engineError("container", in.Container, err))
	}
	name := containerName(c)
	if err := refuseOwned("container", name, c.Config.Labels); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	if c.State.Running && !in.Force {
		msg := "container " + name + " is running; stop it first or set force"
		return dockerapi.ContainerJSON{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/force", Message: msg}}})
	}
	return c, nil
}

func (h containerRemoveHandler) Plan(ctx context.Context, _ core.Env, in ContainerRemoveInput) (core.PlanOutcome, error) {
	c, err := h.target(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := dstate.ContainerRun(c, dstate.Recorded)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
	}
	sum := sha256.Sum256(doc)
	name := containerName(c)
	diff := []core.DiffLine{
		{Op: "-", Text: "container " + name + " (" + c.ID[:min(len(c.ID), 12)] + ")"},
		{Op: " ", Text: "its writable layer is deleted and cannot be restored"},
	}
	if c.State.Running {
		diff = append(diff, core.DiffLine{Op: " ", Text: "the running container is killed first"})
	}
	if in.Volumes {
		diff = append(diff, core.DiffLine{Op: " ", Text: "its anonymous volumes are deleted"})
	}
	return core.PlanOutcome{Diff: diff, StateHash: hex.EncodeToString(sum[:]), Confirmation: name}, nil
}

// Apply removes the container by the id it found, so a container created
// under the same name since the plan is left alone.
func (h containerRemoveHandler) Apply(ctx context.Context, env core.Env, in ContainerRemoveInput) (Removed, error) {
	c, err := h.target(ctx, in)
	if err != nil {
		return Removed{}, err
	}
	before, err := dstate.ContainerRun(c, dstate.Recorded)
	if err != nil {
		return Removed{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
	}
	resource := containerResource(c.ID)
	err = h.k.run(ctx, env, dockerEffect(core.EffectDelete, resource, before), func(ctx context.Context) (string, error) {
		err := h.k.d.docker.RemoveContainer(ctx, c.ID, dockerapi.RemoveContainerOptions{Force: in.Force, Volumes: in.Volumes})
		return resource, engineError("container", in.Container, err)
	})
	if err != nil {
		return Removed{}, err
	}
	return Removed{ID: c.ID, Name: containerName(c)}, nil
}

// Verify checks by the removed id, or by in when recovery passes the zero
// output.
func (h containerRemoveHandler) Verify(ctx context.Context, _ core.Env, in ContainerRemoveInput, out Removed) error {
	id := out.ID
	if id == "" {
		id = in.Container
	}
	_, err := h.k.d.docker.InspectContainer(ctx, id)
	switch {
	case err == nil:
		return fmt.Errorf("docker: container %s still exists", id)
	case dockerapi.IsNotFound(err):
		return nil
	default:
		return engineError("container", id, err)
	}
}

func (h containerRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

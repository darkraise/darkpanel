package docker

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// containerOp is the run-state change one of the three container actions makes.
type containerOp int

const (
	opContainerStart containerOp = iota
	opContainerStop
	opContainerRestart
)

func (k dk) containerStart() core.ActionDef {
	return k.containerRunAction(ID+".container.start", "Start a container.", opContainerStart)
}

func (k dk) containerStop() core.ActionDef {
	return k.containerRunAction(ID+".container.stop", "Stop a container.", opContainerStop)
}

func (k dk) containerRestart() core.ActionDef {
	return k.containerRunAction(ID+".container.restart", "Restart a container.", opContainerRestart)
}

func (k dk) containerRunAction(name, summary string, op containerOp) core.ActionDef {
	return core.DefineMutating[ContainerInput, ContainerState](core.ActionSpec{
		Name: name, Summary: summary, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
		LockKeys: []string{lockKey},
	}, runHandler{k: k, op: op})
}

// runHandler starts, stops or restarts one container. Its undo restores the
// running flag the container had before, except that a restart is not
// reversed: its undo only starts the container again if it is stopped.
type runHandler struct {
	k  dk
	op containerOp
}

// inspect reads the container in names and refuses one another module owns.
// Nothing has changed when it fails, so every failure is Definite.
func (h runHandler) inspect(ctx context.Context, in ContainerInput) (dockerapi.ContainerJSON, error) {
	if in.Container == "" {
		msg := "container is required"
		return dockerapi.ContainerJSON{}, core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: msg,
			Fields: []errcode.FieldError{{Path: "/container", Message: msg}}})
	}
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return dockerapi.ContainerJSON{}, core.Definite(engineError("container", in.Container, err))
	}
	if err := refuseOwned("container", containerName(c), c.Config.Labels); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	return c, nil
}

// containerName is the container's name without Docker's leading "/".
func containerName(c dockerapi.ContainerJSON) string { return strings.TrimPrefix(c.Name, "/") }

func (h runHandler) Plan(ctx context.Context, _ core.Env, in ContainerInput) (core.PlanOutcome, error) {
	c, err := h.inspect(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := dstate.ContainerRun(c, dstate.Restored)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
	}
	sum := sha256.Sum256(doc)
	diff := []core.DiffLine{{Op: " ", Text: "container " + containerName(c) + " (" + c.ID[:min(len(c.ID), 12)] + ")"}}
	return core.PlanOutcome{Diff: append(diff, h.runDiff(c.State.Running)...), StateHash: hex.EncodeToString(sum[:])}, nil
}

func (h runHandler) runDiff(running bool) []core.DiffLine {
	change := func(from, to string) []core.DiffLine {
		return []core.DiffLine{{Op: "-", Text: "state: " + from}, {Op: "+", Text: "state: " + to}}
	}
	switch {
	case h.op == opContainerStop && running:
		return change("running", "stopped")
	case h.op == opContainerStop:
		return []core.DiffLine{{Op: " ", Text: "state: stopped (already stopped)"}}
	case !running:
		return change("stopped", "running")
	case h.op == opContainerRestart:
		return []core.DiffLine{{Op: " ", Text: "state: running"},
			{Op: "+", Text: fmt.Sprintf("restart: stop with a %d s timeout, then start", int(stopTimeout.Seconds()))}}
	default:
		return []core.DiffLine{{Op: " ", Text: "state: running (already running)"}}
	}
}

func (h runHandler) Apply(ctx context.Context, env core.Env, in ContainerInput) (ContainerState, error) {
	c, err := h.inspect(ctx, in)
	if err != nil {
		return ContainerState{}, err
	}
	before, err := dstate.ContainerRunning(c.ID, c.Created, c.State.Running || h.op == opContainerRestart)
	if err != nil {
		return ContainerState{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
	}
	resource := containerResource(c.ID)
	err = h.k.run(ctx, env, dockerEffect(core.EffectModify, resource, before), func(ctx context.Context) (string, error) {
		var err error
		switch h.op {
		case opContainerStart:
			_, err = h.k.d.docker.StartContainer(ctx, c.ID)
		case opContainerStop:
			_, err = h.k.d.docker.StopContainer(ctx, c.ID, stopTimeout)
		default:
			err = h.k.d.docker.RestartContainer(ctx, c.ID, stopTimeout)
		}
		return resource, err
	})
	if err != nil {
		return ContainerState{}, engineError("container", in.Container, err)
	}
	after, err := h.k.d.docker.InspectContainer(ctx, c.ID)
	if err != nil {
		return ContainerState{}, engineError("container", in.Container, err)
	}
	return ContainerState{ID: after.ID, Name: containerName(after), Running: after.State.Running}, nil
}

// Verify checks the container from in alone, because recovery passes the
// zero output.
func (h runHandler) Verify(ctx context.Context, _ core.Env, in ContainerInput, _ ContainerState) error {
	c, err := h.k.d.docker.InspectContainer(ctx, in.Container)
	if err != nil {
		return engineError("container", in.Container, err)
	}
	if want := h.op != opContainerStop; c.State.Running != want {
		return fmt.Errorf("docker: container %s is %s, want %s", in.Container, runWord(c.State.Running), runWord(want))
	}
	return nil
}

func runWord(running bool) string {
	if running {
		return "running"
	}
	return "stopped"
}

func (h runHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse leaves the container in the running state its effect's Before
// names and touches nothing else. A 4xx answer, not found included, means the
// daemon changed nothing, so it is Definite; any other failure leaves the
// undo uncertain.
func (h runHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	id, ok := strings.CutPrefix(e.Resource, containerResource(""))
	if !ok || id == "" {
		return core.InverseResult{}, fmt.Errorf("docker: %q is not a container", e.Resource)
	}
	var before struct {
		Running *bool `json:"running"`
	}
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, fmt.Errorf("docker: read the state container %s is restored to: %w", id, err)
	}
	if before.Running == nil {
		return core.InverseResult{}, fmt.Errorf("docker: container %s has no running flag to restore", id)
	}
	var err error
	if *before.Running {
		_, err = h.k.d.docker.StartContainer(ctx, id)
	} else {
		_, err = h.k.d.docker.StopContainer(ctx, id, stopTimeout)
	}
	if err == nil {
		return core.InverseResult{}, nil
	}
	var refused *dockerapi.APIError
	if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
		return core.InverseResult{}, core.Definite(engineError("container", id, err))
	}
	return core.InverseResult{}, engineError("container", id, err)
}

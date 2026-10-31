package docker

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func (k dk) composeUp() core.ActionDef {
	return core.DefineMutating[ComposeInput, ComposeResult](composeSpec(ID+".compose.up",
		"Create or recreate the containers of a Compose project.", core.ClassIrreversible, 15*time.Minute),
		composeVerb{k: k, word: "up", args: []string{"up", "-d", "--no-build"}})
}

func (k dk) composeDown() core.ActionDef {
	return core.DefineMutating[ComposeInput, ComposeResult](composeSpec(ID+".compose.down",
		"Stop and remove the containers of a Compose project.", core.ClassIrreversible, 10*time.Minute),
		composeVerb{k: k, word: "down", args: []string{"down"}})
}

func (k dk) composePull() core.ActionDef {
	return core.DefineMutating[ComposeInput, ComposeResult](composeSpec(ID+".compose.pull",
		"Pull the images of a Compose project.", core.ClassCompensable, 30*time.Minute),
		composeVerbUndo{composeVerb{k: k, word: "pull", args: []string{"pull"}}})
}

func (k dk) composeRestart() core.ActionDef {
	return core.DefineMutating[ComposeInput, ComposeResult](composeSpec(ID+".compose.restart",
		"Restart the containers of a Compose project.", core.ClassCompensable, 10*time.Minute),
		composeVerbUndo{composeVerb{k: k, word: "restart", args: []string{"restart"}}})
}

func composeSpec(name, summary string, class core.Class, timeout time.Duration) core.ActionSpec {
	return core.ActionSpec{
		Name: name, Summary: summary, Version: 1, Class: class, Side: core.SideAgent,
		LockKeys: []string{lockKey}, MinScope: core.ScopeAdmin, LongRunning: true, ApplyTimeout: timeout,
	}
}

// composeVerb runs one Compose verb over a checked project. It has no undo
// handler, so it serves the irreversible verbs.
type composeVerb struct {
	k    dk
	word string   // up, down, pull or restart
	args []string // the verb's arguments to composeArgs
}

// composeVerbUndo is a composeVerb with the undo of pull and restart.
type composeVerbUndo struct {
	composeVerb
}

func (v composeVerb) Plan(ctx context.Context, env core.Env, in ComposeInput) (core.PlanOutcome, error) {
	r, err := v.k.prepareCompose(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	cs, err := v.k.composeContainersOf(ctx, r.Project.Name)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := planCompose(r, strings.Join(v.args, " "))
	switch v.word {
	case "up":
		diff = append(diff, core.DiffLine{Op: " ", Text: "containers are created or recreated; a recreated container loses its writable layer"})
	case "down":
		for _, c := range cs {
			diff = append(diff, core.DiffLine{Op: "-", Text: "remove container " + containerName(c) + " and its writable layer"})
		}
	case "pull":
		diff = append(diff, core.DiffLine{Op: " ", Text: "images are pulled; a tag that moved is not reverted by an undo"})
	case "restart":
		// Compose restart also starts stopped containers, which the undo leaves running.
		for _, c := range cs {
			if c.State.Running {
				diff = append(diff, core.DiffLine{Op: " ", Text: "restart container " + containerName(c)})
			} else {
				diff = append(diff, core.DiffLine{Op: " ", Text: "start container " + containerName(c) + ", which is stopped; an undo does not stop it again"})
			}
		}
		if len(cs) == 0 {
			diff = append(diff, core.DiffLine{Op: " ", Text: "the project has no container"})
		}
	}
	out := core.PlanOutcome{Diff: diff, StateHash: composeStateHash(r, cs)}
	if v.word == "up" || v.word == "down" {
		out.Confirmation = r.Project.Name
	}
	return out, nil
}

func (v composeVerb) Apply(ctx context.Context, env core.Env, in ComposeInput) (ComposeResult, error) {
	r, err := v.k.prepareCompose(ctx, env, in)
	if err != nil {
		return ComposeResult{}, err
	}
	name := r.Project.Name
	if err := env.Files.WriteManaged(ctx, "docker", dockerConfigDir, "config.json", []byte("{}\n"), 0o600, 0, 0); err != nil {
		return ComposeResult{}, fmt.Errorf("docker: write %s/config.json: %w", dockerConfigDir, err)
	}

	type intended struct {
		seq int
		id  string
	}
	var effects []intended
	if v.word == "restart" {
		cs, err := v.k.composeContainersOf(ctx, name)
		if err != nil {
			return ComposeResult{}, err
		}
		for _, c := range cs {
			if !c.State.Running {
				continue
			}
			before, err := dstate.ContainerRunning(c.ID, c.Created, true)
			if err != nil {
				return ComposeResult{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
			}
			seq, err := env.Effects.Intend(ctx, dockerEffect(core.EffectModify, containerResource(c.ID), before), nil)
			if err != nil {
				return ComposeResult{}, fmt.Errorf("docker: journal %s: %w", containerResource(c.ID), err)
			}
			effects = append(effects, intended{seq: seq, id: c.ID})
		}
	}

	res, err := env.Exec.Run(ctx, core.Cmd{Path: composeDockerPath, Args: composeArgs(r.Project, r.Profiles, v.args...)})
	if err != nil {
		return ComposeResult{}, fmt.Errorf("docker: compose %s: %w", v.word, err)
	}
	if res.ExitCode != 0 {
		// Not Definite: Compose may have created, stopped or removed
		// containers before it failed, and restart's intended effects stay
		// for the engine to settle as uncertain.
		return ComposeResult{}, composeFailure(v.word, res)
	}

	for _, e := range effects {
		c, err := v.k.d.docker.InspectContainer(ctx, e.id)
		if err != nil {
			return ComposeResult{}, engineError("container", e.id, err)
		}
		post, err := dstate.ContainerRun(c, dstate.Recorded)
		if err != nil {
			return ComposeResult{}, fmt.Errorf("docker: render container %s: %w", c.ID, err)
		}
		if err := env.Effects.Made(ctx, e.seq, containerResource(e.id), core.StateDoc{State: post}); err != nil {
			return ComposeResult{}, fmt.Errorf("docker: journal %s: %w", containerResource(e.id), err)
		}
	}

	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return setProjectRun(ctx, tx, name, r.Profiles, r.Hash, env.Clock.Now())
	})
	if err != nil {
		return ComposeResult{}, fmt.Errorf("docker: compose %s: %w", v.word, err)
	}

	cs, err := v.k.composeContainersOf(ctx, name)
	if err != nil {
		return ComposeResult{}, err
	}
	out := ComposeResult{Project: name, Containers: make([]ContainerState, 0, len(cs))}
	for _, c := range cs {
		out.Containers = append(out.Containers, ContainerState{ID: c.ID, Name: containerName(c), Running: c.State.Running})
	}
	return out, nil
}

// Verify checks the project from in alone, because recovery passes the zero
// output.
func (v composeVerb) Verify(ctx context.Context, _ core.Env, in ComposeInput, _ ComposeResult) error {
	if v.word != "up" && v.word != "down" {
		return nil
	}
	cs, err := v.k.composeContainersOf(ctx, in.Project)
	if err != nil {
		return err
	}
	switch {
	case v.word == "up" && len(cs) == 0:
		return fmt.Errorf("docker: project %s has no container after up", in.Project)
	case v.word == "down" && len(cs) > 0:
		return fmt.Errorf("docker: project %s still has %d containers after down", in.Project, len(cs))
	}
	return nil
}

func (v composeVerbUndo) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return v.k.observe(ctx, env, e)
}

// Inverse of a pull changes nothing: a pulled tag is not reverted. Inverse
// of a restart starts the effect's container again when it is stopped and
// touches no other container. A 4xx answer, not found included, means the
// daemon changed nothing, so it is Definite; any other failure leaves the
// undo uncertain.
func (v composeVerbUndo) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	if v.word == "pull" {
		return core.InverseResult{}, nil
	}
	id, ok := strings.CutPrefix(e.Resource, containerResource(""))
	if !ok || id == "" {
		return core.InverseResult{}, fmt.Errorf("docker: %q is not a container", e.Resource)
	}
	failed := func(err error) (core.InverseResult, error) {
		var refused *dockerapi.APIError
		if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
			return core.InverseResult{}, core.Definite(engineError("container", id, err))
		}
		return core.InverseResult{}, engineError("container", id, err)
	}
	c, err := v.k.d.docker.InspectContainer(ctx, id)
	if err != nil {
		return failed(err)
	}
	if c.State.Running {
		return core.InverseResult{}, nil
	}
	if _, err := v.k.d.docker.StartContainer(ctx, id); err != nil {
		return failed(err)
	}
	return core.InverseResult{}, nil
}

// composeContainersOf returns the containers of project name, sorted by name.
func (k dk) composeContainersOf(ctx context.Context, name string) ([]dockerapi.ContainerJSON, error) {
	list, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{
		All:     true,
		Filters: dockerapi.Filters{"label": {projectLabelName + "=" + name}},
	})
	if err != nil {
		return nil, engineError("container", name, err)
	}
	var cs []dockerapi.ContainerJSON
	for _, s := range list {
		c, err := k.d.docker.InspectContainer(ctx, s.ID)
		if err != nil {
			return nil, engineError("container", s.ID, err)
		}
		// The daemon's filter is not relied on.
		if c.Config.Labels[projectLabelName] == name {
			cs = append(cs, c)
		}
	}
	slices.SortFunc(cs, func(a, b dockerapi.ContainerJSON) int { return strings.Compare(containerName(a), containerName(b)) })
	return cs, nil
}

// composeStateHash is the hex SHA-256 of what a run acts on: the project, the
// hash of its files, its profiles and the running flag of each container.
func composeStateHash(r composeRun, cs []dockerapi.ContainerJSON) string {
	lines := []string{"project=" + r.Project.Name, "hash=" + r.Hash, "profiles=" + strings.Join(r.Profiles, ",")}
	for _, c := range cs {
		lines = append(lines, "container="+c.ID+":"+strconv.FormatBool(c.State.Running))
	}
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// composeFailure is the error of a Compose run that exited non-zero: the
// verb, the exit status and the last non-empty stderr line.
func composeFailure(word string, res core.CmdResult) error {
	detail := "compose " + word + " exited with status " + strconv.Itoa(res.ExitCode)
	if line := composeLastLine(res.Stderr); line != "" {
		detail += ": " + line
	}
	return errcode.New(errcode.DockerComposeUnavailable, detail)
}

package panel

import (
	"context"
	"fmt"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// networkRemoveAction declares panel.network.remove: it removes the panel network, which panel.network.create
// compensates. A network already gone counts as removed.
func networkRemoveAction(d agentDeps) core.ActionDef {
	return core.DefineMutating[NetworkRemoveInput, NetworkRemoveOutput](core.ActionSpec{
		Name:          ActionNetRemove,
		Summary:       "Remove the panel network between the proxy and the panel's listener.",
		Version:       1,
		Class:         core.ClassCompensable,
		Side:          core.SideAgent,
		LockKeys:      []string{networkLockKey},
		Converges:     true,
		CompensatedBy: ActionNetCreate,
		MinScope:      core.ScopeAdmin,
	}, networkRemoveHandler{d: d})
}

// networkRemoveHandler is the handler of panel.network.remove.
type networkRemoveHandler struct{ d agentDeps }

// networkFind reads the panel network; ok is false when it does not exist. A network of that name the panel module did
// not label is refused, so Darkpanel never removes a network it did not make.
func networkFind(ctx context.Context, docker panelDocker) (n dockerapi.Network, ok bool, err error) {
	n, err = docker.InspectNetwork(ctx, NetworkName)
	if dockerapi.IsNotFound(err) {
		return dockerapi.Network{}, false, nil
	}
	if err != nil {
		return dockerapi.Network{}, false, networkReadFailed("inspect network "+NetworkName, err)
	}
	if n.Labels[core.LabelModule] != ID {
		return dockerapi.Network{}, false, core.Definite(errcode.New(errcode.DockerOwned, fmt.Sprintf(
			"the network %s is not labelled %s=%s, so Darkpanel will not remove it", NetworkName, core.LabelModule, ID)))
	}
	return n, true, nil
}

// Plan reports the removal; the hash covers the network's id and the containers attached to it.
func (h networkRemoveHandler) Plan(ctx context.Context, _ core.Env, _ NetworkRemoveInput) (core.PlanOutcome, error) {
	docker, err := networkClient(h.d)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	n, ok, err := networkFind(ctx, docker)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	containers := []string{}
	for _, c := range n.Containers {
		containers = append(containers, c.Name)
	}
	slices.Sort(containers)
	hash, err := networkHash(struct {
		ID         string   `json:"id"`
		Containers []string `json:"containers"`
	}{ID: n.ID, Containers: containers})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{}
	if ok {
		diff = append(diff, core.DiffLine{Op: "-", Text: "network " + NetworkName})
	}
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: hash}, nil
}

// Apply journals the deletion with the network's state, removes the network by its id and records it made.
func (h networkRemoveHandler) Apply(ctx context.Context, env core.Env, _ NetworkRemoveInput) (NetworkRemoveOutput, error) {
	docker, err := networkClient(h.d)
	if err != nil {
		return NetworkRemoveOutput{}, err
	}
	n, ok, err := networkFind(ctx, docker)
	if err != nil {
		return NetworkRemoveOutput{}, err
	}
	if !ok {
		return NetworkRemoveOutput{Network: NetworkName}, nil
	}
	before, err := dstate.NetworkCreated(&n)
	if err != nil {
		return NetworkRemoveOutput{}, fmt.Errorf("panel: render network %s: %w", n.ID, err)
	}
	resource := networkResource(n.ID)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind:       core.EffectDelete,
		Resource:   resource,
		CommitWait: networkCommitWait,
		Before:     core.StateDoc{State: before},
	}, nil)
	if err != nil {
		return NetworkRemoveOutput{}, fmt.Errorf("panel: journal %s: %w", resource, err)
	}
	if err := docker.RemoveNetwork(ctx, n.ID); err != nil && !dockerapi.IsNotFound(err) {
		return NetworkRemoveOutput{}, networkFailed(ctx, env, seq, resource,
			fmt.Errorf("panel: remove network %s: %w", NetworkName, networkEngineCoded(err)))
	}
	if err := env.Effects.Made(ctx, seq, resource, networkNull()); err != nil {
		return NetworkRemoveOutput{}, fmt.Errorf("panel: journal %s: %w", resource, err)
	}
	return NetworkRemoveOutput{Network: NetworkName, Removed: true}, nil
}

// Verify passes once the network is gone.
func (h networkRemoveHandler) Verify(ctx context.Context, _ core.Env, _ NetworkRemoveInput, _ NetworkRemoveOutput) error {
	docker, err := networkClient(h.d)
	if err != nil {
		return err
	}
	_, err = docker.InspectNetwork(ctx, NetworkName)
	switch {
	case dockerapi.IsNotFound(err):
		return nil
	case err != nil:
		return fmt.Errorf("panel: inspect network %s: %w", NetworkName, networkEngineCoded(err))
	default:
		return fmt.Errorf("panel: network %s still exists", NetworkName)
	}
}

// Observe reads the network of one effect.
func (h networkRemoveHandler) Observe(ctx context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	return networkObserve(ctx, h.d, e)
}

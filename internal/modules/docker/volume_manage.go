package docker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// manageVolumeUsers names the containers, running or not, that mount the
// named volume.
func (k dk) manageVolumeUsers(ctx context.Context, name string) ([]string, error) {
	containers, err := k.d.docker.ListContainers(ctx, dockerapi.ListContainersOptions{All: true})
	if err != nil {
		return nil, engineError("volume", name, err)
	}
	var users []string
	for _, c := range containers {
		if slices.ContainsFunc(c.Mounts, func(m dockerapi.MountPoint) bool { return m.Type == "volume" && m.Name == name }) {
			users = append(users, resContainerName(c.Names))
		}
	}
	slices.Sort(users)
	return users, nil
}

func (k dk) volumeCreate() core.ActionDef {
	return core.DefineMutating[VolumeCreateInput, Created](core.ActionSpec{
		Name: ID + ".volume.create", Summary: "Create a volume.", Version: 1, Class: core.ClassCompensable,
		Side: core.SideAgent, LockKeys: []string{lockKey},
	}, volumeCreateHandler{k: k})
}

// volumeCreateHandler creates one volume. Its undo removes the volume while
// no container mounts it.
type volumeCreateHandler struct{ k dk }

// request builds the create request and its diff, or refuses the input.
// Nothing has changed when it fails, so every failure is Definite.
func (h volumeCreateHandler) request(ctx context.Context, in VolumeCreateInput) (dockerapi.VolumeCreate, []core.DiffLine, error) {
	var none dockerapi.VolumeCreate
	if !manageNamePattern.MatchString(in.Name) {
		return none, nil, manageInvalid("/name", "volume name must start with a letter or digit and hold at most 128 letters, digits, '_', '.' or '-'")
	}
	if err := manageLabels(in.Labels); err != nil {
		return none, nil, err
	}
	driver := in.Driver
	if driver == "" {
		driver = "local"
	}
	if !manageDriverPattern.MatchString(driver) {
		return none, nil, manageInvalid("/driver", "driver "+driver+" is not a valid driver name")
	}
	// The daemon's create answers an existing volume unchanged, and an undo
	// would then remove a volume this change did not create.
	_, err := h.k.d.docker.InspectVolume(ctx, in.Name)
	switch {
	case err == nil:
		return none, nil, manageInvalid("/name", "volume "+in.Name+" already exists")
	case !dockerapi.IsNotFound(err):
		return none, nil, core.Definite(engineError("volume", in.Name, err))
	}
	req := dockerapi.VolumeCreate{Name: in.Name, Driver: driver, Labels: in.Labels}
	diff := []core.DiffLine{{Op: "+", Text: "volume " + in.Name}, {Op: " ", Text: "driver " + driver}}
	return req, append(diff, manageLabelLines(in.Labels)...), nil
}

func (h volumeCreateHandler) Plan(ctx context.Context, _ core.Env, in VolumeCreateInput) (core.PlanOutcome, error) {
	req, diff, err := h.request(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	hash, err := manageHash(req)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h volumeCreateHandler) Apply(ctx context.Context, env core.Env, in VolumeCreateInput) (Created, error) {
	req, _, err := h.request(ctx, in)
	if err != nil {
		return Created{}, err
	}
	e := dockerEffect(core.EffectCreate, volumeResource("?"+in.Name), json.RawMessage("null"))
	e.Recovery = &core.RecoveryID{Kind: "name", Value: in.Name}
	err = h.k.run(ctx, env, e, func(ctx context.Context) (string, error) {
		v, err := h.k.d.docker.CreateVolume(ctx, req)
		if err != nil {
			return "", engineError("volume", in.Name, err)
		}
		return volumeResource(v.Name), nil
	})
	if err != nil {
		return Created{}, err
	}
	return Created{ID: in.Name, Name: in.Name}, nil
}

// Verify checks the volume from in alone, because recovery passes the zero
// output and a volume's name is its identity.
func (h volumeCreateHandler) Verify(ctx context.Context, _ core.Env, in VolumeCreateInput, _ Created) error {
	if _, err := h.k.d.docker.InspectVolume(ctx, in.Name); err != nil {
		return engineError("volume", in.Name, err)
	}
	return nil
}

func (h volumeCreateHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

// Inverse removes the created volume, never forced. A volume already gone is
// the state the undo wants; one a container mounted since is left alone and
// refused.
func (h volumeCreateHandler) Inverse(ctx context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	name, ok := strings.CutPrefix(e.Resource, volumeResource(""))
	if !ok || name == "" || strings.HasPrefix(name, "?") {
		return core.InverseResult{}, fmt.Errorf("docker: %q is not a created volume", e.Resource)
	}
	_, err := h.k.d.docker.InspectVolume(ctx, name)
	if dockerapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	}
	if err != nil {
		return core.InverseResult{}, core.Definite(engineError("volume", name, err))
	}
	users, err := h.k.manageVolumeUsers(ctx, name)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if len(users) > 0 {
		return core.InverseResult{}, core.Definite(errcode.New(errcode.DockerInUse,
			"volume "+name+" is used by containers: "+strings.Join(users, ", ")))
	}
	err = h.k.d.docker.RemoveVolume(ctx, name, false)
	if err == nil || dockerapi.IsNotFound(err) {
		return core.InverseResult{}, nil
	}
	var refused *dockerapi.APIError
	if errors.As(err, &refused) && refused.StatusCode >= http.StatusBadRequest && refused.StatusCode < http.StatusInternalServerError {
		return core.InverseResult{}, core.Definite(engineError("volume", name, err))
	}
	return core.InverseResult{}, engineError("volume", name, err)
}

func (k dk) volumeRemove() core.ActionDef {
	return core.DefineMutating[VolumeInput, Removed](core.ActionSpec{
		Name: ID + ".volume.remove", Summary: "Remove a volume and the data in it.", Version: 1, Class: core.ClassIrreversible,
		Side: core.SideAgent, LockKeys: []string{lockKey},
	}, volumeRemoveHandler{k: k})
}

// volumeRemoveHandler deletes one volume and its data. Removal cannot be
// undone, so it has no Inverse.
type volumeRemoveHandler struct{ k dk }

// target reads the volume in names and refuses what removal must not touch.
// Nothing has changed when it fails, so every failure is Definite.
func (h volumeRemoveHandler) target(ctx context.Context, in VolumeInput) (dockerapi.Volume, error) {
	if in.Volume == "" {
		return dockerapi.Volume{}, manageInvalid("/volume", "volume is required")
	}
	v, err := h.k.d.docker.InspectVolume(ctx, in.Volume)
	if err != nil {
		return dockerapi.Volume{}, core.Definite(engineError("volume", in.Volume, err))
	}
	if err := refuseOwned("volume", v.Name, v.Labels); err != nil {
		return dockerapi.Volume{}, err
	}
	users, err := h.k.manageVolumeUsers(ctx, v.Name)
	if err != nil {
		return dockerapi.Volume{}, core.Definite(err)
	}
	if len(users) > 0 {
		return dockerapi.Volume{}, core.Definite(errcode.New(errcode.DockerInUse,
			"volume "+v.Name+" is used by containers: "+strings.Join(users, ", ")))
	}
	return v, nil
}

func (h volumeRemoveHandler) Plan(ctx context.Context, _ core.Env, in VolumeInput) (core.PlanOutcome, error) {
	v, err := h.target(ctx, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	doc, err := dstate.VolumeCreated(&v, true)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("docker: render volume %s: %w", v.Name, err)
	}
	hash, err := manageHash(doc)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: "volume " + v.Name},
		{Op: " ", Text: "the data in the volume is deleted and cannot be restored"},
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash, Confirmation: v.Name}, nil
}

// Apply never forces the removal: a forced removal ignores a missing volume
// and hides a conflict.
func (h volumeRemoveHandler) Apply(ctx context.Context, env core.Env, in VolumeInput) (Removed, error) {
	v, err := h.target(ctx, in)
	if err != nil {
		return Removed{}, err
	}
	before, err := dstate.VolumeCreated(&v, true)
	if err != nil {
		return Removed{}, fmt.Errorf("docker: render volume %s: %w", v.Name, err)
	}
	resource := volumeResource(v.Name)
	err = h.k.run(ctx, env, dockerEffect(core.EffectDelete, resource, before), func(ctx context.Context) (string, error) {
		return resource, engineError("volume", v.Name, h.k.d.docker.RemoveVolume(ctx, v.Name, false))
	})
	if err != nil {
		return Removed{}, err
	}
	return Removed{ID: v.Name, Name: v.Name}, nil
}

func (h volumeRemoveHandler) Verify(ctx context.Context, _ core.Env, in VolumeInput, _ Removed) error {
	_, err := h.k.d.docker.InspectVolume(ctx, in.Volume)
	switch {
	case err == nil:
		return fmt.Errorf("docker: volume %s still exists", in.Volume)
	case dockerapi.IsNotFound(err):
		return nil
	default:
		return engineError("volume", in.Volume, err)
	}
}

func (h volumeRemoveHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	return h.k.observe(ctx, env, e)
}

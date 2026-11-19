package system

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// RemoveInput is the input of system.packages.remove.
type RemoveInput struct {
	Names []string `json:"names" jsonschema_description:"The installed packages to remove."`
	Purge bool     `json:"purge,omitempty" jsonschema_description:"Also delete their configuration files."`
}

func (in RemoveInput) vector() (string, error) {
	if len(in.Names) == 0 {
		return "", errcode.New(errcode.InvalidInput, "list the packages to remove")
	}
	if in.Purge {
		return "purge", nil
	}
	return "remove", nil
}

func (m module) removeAction() core.ActionDef {
	return core.DefineTask[RemoveInput, Applied](core.ActionSpec{
		Name: ID + ".packages.remove", Version: 1,
		Summary:      "Remove named packages, optionally with their configuration files.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 30 * time.Minute,
		Rechecks:     []string{"docker"},
	}, removeHandler{m: m})
}

type removeHandler struct{ m module }

func (h removeHandler) Plan(ctx context.Context, env core.Env, in RemoveInput) (core.PlanOutcome, error) {
	verb, err := in.vector()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out, _, err := h.m.planSet(ctx, env, verb, in.Names, nil)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if out.Confirmation, err = h.m.hostname(); err != nil {
		return core.PlanOutcome{}, err
	}
	return out, nil
}

func (h removeHandler) Task(ctx context.Context, env core.Env, in RemoveInput) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	verb, err := in.vector()
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	planned, err := h.m.simulateSet(ctx, env, verb, in.Names, nil)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	return runSet(ctx, env, verb, in.Names, planned)
}

// Verify checks the named packages are gone, and the reported sets.
func (h removeHandler) Verify(ctx context.Context, env core.Env, in RemoveInput, out Applied) error {
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	for _, n := range in.Names {
		if _, ok := versions[n]; ok {
			return fmt.Errorf("system: %s is still installed", n)
		}
	}
	return verifyApplied(ctx, env, out)
}

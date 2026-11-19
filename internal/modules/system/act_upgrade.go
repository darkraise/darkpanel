package system

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// aptLockKey is held by every action that runs apt.
const aptLockKey = ID + ".apt"

// UpgradeInput is the input of system.packages.upgrade: every package, or the
// named installed ones.
type UpgradeInput struct {
	All   bool     `json:"all,omitempty" jsonschema_description:"Upgrade every package, as apt-get dist-upgrade does; it may install new dependencies and remove packages."`
	Names []string `json:"names,omitempty" jsonschema_description:"Upgrade only these installed packages; empty when all is set."`
}

// vector is the apt verb and names of the input.
func (in UpgradeInput) vector() (string, []string, error) {
	switch {
	case in.All && len(in.Names) > 0:
		return "", nil, errcode.New(errcode.InvalidInput, "set all or list names, not both")
	case in.All:
		return "dist-upgrade", nil, nil
	case len(in.Names) == 0:
		return "", nil, errcode.New(errcode.InvalidInput, "set all or list the packages to upgrade")
	}
	return "install-only-upgrade", in.Names, nil
}

func (m module) upgradeAction() core.ActionDef {
	return core.DefineTask[UpgradeInput, Applied](core.ActionSpec{
		Name: ID + ".packages.upgrade", Version: 1,
		Summary:      "Upgrade every package, or the named installed packages.",
		Class:        core.ClassIrreversible,
		Side:         core.SideAgent,
		LockKeys:     []string{aptLockKey},
		ApplyTimeout: 60 * time.Minute,
		Rechecks:     []string{"docker"},
	}, upgradeHandler{m: m})
}

type upgradeHandler struct{ m module }

func (h upgradeHandler) Plan(ctx context.Context, env core.Env, in UpgradeInput) (core.PlanOutcome, error) {
	verb, names, err := in.vector()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	out, _, err := h.m.planSet(ctx, env, verb, names, nil)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if out.Confirmation, err = h.m.hostname(); err != nil {
		return core.PlanOutcome{}, err
	}
	return out, nil
}

func (h upgradeHandler) Task(ctx context.Context, env core.Env, in UpgradeInput) (Applied, error) {
	defer invalidateUpdates(env.HostID)
	verb, names, err := in.vector()
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	planned, err := h.m.simulateSet(ctx, env, verb, names, nil)
	if err != nil {
		return Applied{}, core.Definite(err)
	}
	return runSet(ctx, env, verb, names, planned)
}

// Verify checks that what the run reports upgraded or installed is installed
// and what it removed is not; recovery passes the zero output, which checks
// nothing.
func (h upgradeHandler) Verify(ctx context.Context, env core.Env, _ UpgradeInput, out Applied) error {
	return verifyApplied(ctx, env, out)
}

func verifyApplied(ctx context.Context, env core.Env, out Applied) error {
	if len(out.Installed)+len(out.Upgraded)+len(out.Removed) == 0 {
		return nil
	}
	versions, err := installedVersions(ctx, env)
	if err != nil {
		return err
	}
	for _, list := range [][]string{out.Installed, out.Upgraded} {
		for _, n := range list {
			if _, ok := versions[n]; !ok {
				return fmt.Errorf("system: %s is not installed after the change", n)
			}
		}
	}
	for _, n := range out.Removed {
		if _, ok := versions[n]; ok {
			return fmt.Errorf("system: %s is still installed after the change", n)
		}
	}
	return nil
}

package npm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// Inverse undoes the upgrade of change e.ChangeID through the same rollback a failed upgrade runs. It refuses, having
// changed nothing, an upgrade that never finished, and a finished one whose archives cannot restore the original or
// whose original, recreated, could take a forwarded address.
func (h instanceUpgradeHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if e.Resource != instanceUpgradeResource() {
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: %q is not a resource npm.instance.upgrade changes", e.Resource))
	}
	u, ok, err := loadUpgrade(ctx, env.DB.Read(), e.ChangeID)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if !ok {
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: no upgrade is recorded for change %s", e.ChangeID))
	}
	switch u.Phase {
	case "rolled_back":
		return core.InverseResult{}, nil
	case "done":
		// The rollback removes the upgraded container before it restores, so the archives are checked first.
		if err := h.checkArchives(u); err != nil {
			return core.InverseResult{}, core.Definite(err)
		}
		// The image is made available before the check, so a caller who removes the unused original image cannot
		// stretch the time between the check and the restore by a pull.
		if err := h.pull(ctx, u.Config.Create.Image); err != nil {
			return core.InverseResult{}, core.Definite(err)
		}
		if err := h.undoForwardCheck(ctx, env, u); err != nil {
			return core.InverseResult{}, err
		}
		if _, err := h.rollback(ctx, env, u); err != nil {
			return core.InverseResult{}, err
		}
		return core.InverseResult{}, nil
	default:
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: the upgrade of change %s is in phase %q, which has nothing to undo",
			e.ChangeID, u.Phase))
	}
}

// undoForwardCheck refuses, Definite, the undo of the finished upgrade u while the original, recreated from u's
// configuration, could take a forwarded address; the addresses of the container named darkpanel-npm count as free,
// because the restore removes it. Unlike a failed upgrade's rollback, a requested undo may refuse: NPM keeps running
// the upgraded container. Only the upgraded container, the one the instance row names, serves NPM's API, so only then
// are the targets read through it, and a custom location whose static forward name has no answer refused, as the
// upgrade refuses it. Otherwise an earlier undo was cut off after the restore removed that container, and the targets
// come from the list u records; a row with no list is not checked here, since the verification after the restored
// original starts reads them through its API.
func (h instanceUpgradeHandler) undoForwardCheck(ctx context.Context, env core.Env, u upgradeRow) error {
	c, err := h.n.d.docker.InspectContainer(ctx, containerName)
	var freeID string
	switch {
	case err == nil:
		freeID = c.ID
	case !dockerapi.IsNotFound(err):
		return core.Definite(fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err)))
	}
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return core.Definite(err)
	}
	if freeID != "" && ok && freeID == row.ContainerID {
		targets, us, err := h.checkedTargets(ctx, env)
		if err != nil {
			return err
		}
		if err := h.forwardCheckConfig(ctx, env, u.Config, targets, freeID); err != nil {
			return err
		}
		return unansweredRefusal(us)
	}
	if u.Config.Targets == nil {
		return nil
	}
	targets := make([]forwardTarget, 0, len(*u.Config.Targets))
	for _, r := range *u.Config.Targets {
		targets = append(targets, r.target())
	}
	return h.forwardCheckConfig(ctx, env, u.Config, targets, freeID)
}

// checkArchives runs the checks the restore makes of u's archives, their count, volumes and checksums, without
// keeping their contents.
func (h instanceUpgradeHandler) checkArchives(u upgradeRow) error {
	_, err := h.restoreArchives(u)
	return err
}

// Recover decides by the transaction row alone, because the engine calls it only once the apply unit has ended. The
// rollback it runs from recreating or waiting records the upgrade's effect as skipped; the engine settles every other
// outcome through Observe after it returns.
func (h instanceUpgradeHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	u, ok, err := loadUpgrade(ctx, env.DB.Read(), env.ChangeID)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	if !ok {
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	}
	switch u.Phase {
	case "rolled_back":
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	case "done":
		out, err := json.Marshal(Upgraded{FromImage: u.FromImage, ToImage: u.ToImage})
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryCompleted, Output: out}, nil
	case "failed":
		// A failed row means the rollback itself failed; it is not repeated without the operator.
		return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
			Reason: "the upgrade of Nginx Proxy Manager to " + u.ToImage + " failed: " + u.Result}, nil
	case "recreating", "waiting":
		// The rollback runs even when the container named darkpanel-npm already runs the original image, because it
		// may still be the replacement.
		if _, err := h.rollback(ctx, env, u); err != nil {
			return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
				Reason: "the rollback of the upgrade to " + u.ToImage + " failed: " + err.Error()}, nil
		}
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	case "recorded", "stopped", "archiving", "archived":
		return h.recoverOriginal(ctx, env, u)
	default:
		return core.RecoveryOutcome{}, fmt.Errorf("npm: upgrade %s is in the unknown phase %q", u.ChangeID, u.Phase)
	}
}

// recoverOriginal ends a transaction that never removed the original container: it drops the archives a cut-off
// archiving may have left half written and starts the original again. The phase is written only once the original
// runs, so a failed start leaves the row as it was.
func (h instanceUpgradeHandler) recoverOriginal(ctx context.Context, env core.Env, u upgradeRow) (core.RecoveryOutcome, error) {
	if u.Phase == "archiving" {
		if err := h.dropPartialArchives(ctx, env, env.ChangeID); err != nil {
			return core.RecoveryOutcome{}, err
		}
	}
	docker := h.n.d.docker
	c, err := docker.InspectContainer(ctx, containerName)
	switch {
	case dockerapi.IsNotFound(err):
		return core.RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: "the original container " + containerName + " is missing"}, nil
	case err != nil:
		return core.RecoveryOutcome{}, fmt.Errorf("npm: inspect container %s: %w", containerName, engineCoded(err))
	}
	if !c.State.Running {
		if _, err := docker.StartContainer(ctx, c.ID); err != nil {
			return core.RecoveryOutcome{}, fmt.Errorf("npm: start container %s: %w", containerName, engineCoded(err))
		}
	}
	// An original that already runs is verified too: the cut-off run may have started it and not got further.
	held, err := h.verifyStart(ctx, env, u, c.ID)
	var unverified unverifiedStart
	if errors.As(err, &unverified) {
		return core.RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: err.Error()}, nil
	}
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	if err := setUpgradePhase(ctx, env.DB, env.ChangeID, "rolled_back", startedResult(held), nil, env.Clock.Now()); err != nil {
		return core.RecoveryOutcome{}, err
	}
	return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
}

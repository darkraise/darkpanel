package npm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// Inverse undoes the upgrade of change e.ChangeID through the same rollback a failed upgrade runs. It refuses, having
// changed nothing, an upgrade that never finished, and a finished one whose archives cannot restore the original.
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
		if err := h.rollback(ctx, env, u); err != nil {
			return core.InverseResult{}, err
		}
		return core.InverseResult{}, nil
	default:
		return core.InverseResult{}, core.Definite(fmt.Errorf("npm: the upgrade of change %s is in phase %q, which has nothing to undo",
			e.ChangeID, u.Phase))
	}
}

// checkArchives reads every archive of u and checks it against its recorded checksum.
func (h instanceUpgradeHandler) checkArchives(u upgradeRow) error {
	for _, a := range u.Archives {
		data, err := h.n.d.readFile(backupDir + "/" + a.File)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return errcode.New(errcode.NotFound, "the backup archive "+a.File+" of the upgrade no longer exists")
		case err != nil:
			return err
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != a.SHA256 {
			return fmt.Errorf("npm: archive %s does not match its recorded checksum", a.File)
		}
	}
	return nil
}

// Recover decides by the transaction row alone, because the engine calls it only once the apply unit has ended. It
// journals nothing: the engine settles the upgrade's effect through Observe after it returns.
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
		if err := h.rollback(ctx, env, u); err != nil {
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
		for _, v := range instanceUpgradeVolumes {
			name := archiveName(env.ChangeID, v.volume)
			if err := env.Files.RemoveManaged(ctx, ID, backupDir, name); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return core.RecoveryOutcome{}, fmt.Errorf("npm: remove the archive %s: %w", name, err)
			}
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
	if err := setUpgradePhase(ctx, env.DB, env.ChangeID, "rolled_back", "", u.Archives, env.Clock.Now()); err != nil {
		return core.RecoveryOutcome{}, err
	}
	return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
}

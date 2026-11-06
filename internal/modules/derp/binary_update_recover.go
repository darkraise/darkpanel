package derp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/derp/server"
)

// binaryUpdateRecoverTarget refuses an effect on any resource other than derper's binary.
func binaryUpdateRecoverTarget(e core.Effect) error {
	if e.Resource != binaryEffectResource {
		return fmt.Errorf("derp: %q is not a resource derp.binary.update changes", e.Resource)
	}
	return nil
}

// Observe reads the version the server is recorded at and the hash of the binary on disk, the members the update's
// effect records. No recorded server, or no binary, is the state null.
func (h binaryUpdateHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if err := binaryUpdateRecoverTarget(e); err != nil {
		return core.Observation{}, err
	}
	absent := core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Matches: 1, Exclusive: true}
	row, ok, err := loadLocalServer(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok {
		return absent, nil
	}
	// fileSHA256 answers "" only for a missing file.
	sha, err := h.fileSHA256(server.BinaryPath)
	if err != nil {
		return core.Observation{}, err
	}
	if sha == "" {
		return absent, nil
	}
	return core.Observation{Current: binaryDoc(row.DerperVersion, sha), Matches: 1, Exclusive: true}, nil
}

// Inverse undoes the update of change e.ChangeID through the same rollback a failed check runs. It refuses, having
// changed nothing, only an update that never swapped the binary in; a failed update runs the rollback again.
func (h binaryUpdateHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if err := binaryUpdateRecoverTarget(e); err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	u, ok, err := loadUpdate(ctx, env.DB.Read(), e.ChangeID)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if !ok {
		return core.InverseResult{}, core.Definite(fmt.Errorf("derp: no binary update is recorded for change %s", e.ChangeID))
	}
	switch u.Phase {
	case "rolled_back":
		return core.InverseResult{}, nil
	case "done", "rolling_back", "failed":
		if err := h.rollback(ctx, env, u); err != nil {
			return core.InverseResult{}, err
		}
		return core.InverseResult{}, nil
	default:
		return core.InverseResult{}, core.Definite(fmt.Errorf("derp: the binary update of change %s is in phase %q, which has nothing to undo",
			e.ChangeID, u.Phase))
	}
}

// Recover decides by the update row's phase alone, because the engine calls it only once the apply unit has ended.
// It journals nothing: the engine settles the update's effect through Observe after it returns.
func (h binaryUpdateHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	u, ok, err := loadUpdate(ctx, env.DB.Read(), env.ChangeID)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	if !ok || u.Phase == "staged" {
		if err := binaryUpdateRecoverUnstage(ctx, env, h.d); err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	}
	switch u.Phase {
	case "rolled_back":
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	case "done":
		out, err := json.Marshal(BinaryUpdated{From: u.FromVersion, To: u.ToVersion})
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryCompleted, Output: out}, nil
	case "failed":
		return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
			Reason: "the update of derper to " + u.ToVersion + " failed: " + u.Result}, nil
	case "kept", "swapped", "restarted", "rolling_back":
		if err := h.rollback(ctx, env, u); err != nil {
			return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
				Reason: "the rollback of the update to " + u.ToVersion + " failed: " + err.Error()}, nil
		}
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	default:
		return core.RecoveryOutcome{}, fmt.Errorf("derp: binary update %s is in the unknown phase %q", u.ChangeID, u.Phase)
	}
}

// binaryUpdateRecoverUnstage removes a derper.new an update staged and never swapped in.
func binaryUpdateRecoverUnstage(ctx context.Context, env core.Env, d dp) error {
	_, err := d.d.readFile(server.BinaryNewPath)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return fmt.Errorf("derp: read %s: %w", server.BinaryNewPath, err)
	}
	if err := env.Files.Remove(ctx, ID, server.BinaryNewPath); err != nil {
		return fmt.Errorf("derp: remove %s: %w", server.BinaryNewPath, err)
	}
	return nil
}

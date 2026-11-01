package tailscale

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// packagePinRecoverLastStepSQL reads the step a pin or its rollback took last.
const packagePinRecoverLastStepSQL = "SELECT step, result FROM tailscale_pin_steps WHERE change_id = ? ORDER BY seq DESC LIMIT 1"

// packagePinRecoverTarget refuses an effect on any resource other than the tailscale package.
func packagePinRecoverTarget(e core.Effect) error {
	if e.Resource != pinTxResource {
		return fmt.Errorf("tailscale: %q is not a resource tailscale.package.pin changes", e.Resource)
	}
	return nil
}

// Observe reads the installed version and the hold, the members the pin's effect records.
func (h packagePinHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if err := packagePinRecoverTarget(e); err != nil {
		return core.Observation{}, err
	}
	version, err := h.t.installedVersion(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	held, err := h.t.held(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: txPackageDoc(version, held), Matches: 1, Exclusive: true}, nil
}

// Inverse undoes the pin of change e.ChangeID through the same rollback a failed check runs.
func (h packagePinHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if err := packagePinRecoverTarget(e); err != nil {
		return core.InverseResult{}, err
	}
	p, ok, err := loadPin(ctx, env.DB.Read(), e.ChangeID)
	if err != nil {
		return core.InverseResult{}, err
	}
	if !ok {
		return core.InverseResult{}, fmt.Errorf("tailscale: no pin is recorded for change %s", e.ChangeID)
	}
	if err := h.rollback(ctx, env, p); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

// Recover decides by the pin row's phase alone, because the engine calls it only once the apply unit has ended. It
// journals nothing: the engine settles the pin's effect through Observe after it returns.
func (h packagePinHandler) Recover(ctx context.Context, env core.Env) (core.RecoveryOutcome, error) {
	p, ok, err := loadPin(ctx, env.DB.Read(), env.ChangeID)
	if err != nil {
		return core.RecoveryOutcome{}, err
	}
	if !ok {
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	}
	switch p.Phase {
	case "recorded", "rolled_back":
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	case "done":
		out, err := json.Marshal(Pinned{Version: p.Version, PriorVersion: p.PriorVersion, PriorHeld: p.PriorHeld})
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryCompleted, Output: out}, nil
	case "failed":
		reason, err := packagePinRecoverFailure(ctx, env, p)
		if err != nil {
			return core.RecoveryOutcome{}, err
		}
		return core.RecoveryOutcome{State: core.RecoveryNeedsRepair, Reason: reason}, nil
	case "unheld", "staged", "installed", "held", "checking", "rolling_back":
		if err := h.rollback(ctx, env, p); err != nil {
			return core.RecoveryOutcome{State: core.RecoveryNeedsRepair,
				Reason: "the rollback of the pin of " + p.Version + " failed: " + err.Error()}, nil
		}
		return core.RecoveryOutcome{State: core.RecoveryRolledBack}, nil
	default:
		return core.RecoveryOutcome{}, fmt.Errorf("tailscale: pin %s is in the unknown phase %q", p.ChangeID, p.Phase)
	}
}

// packagePinRecoverFailure is the reason a failed pin needs repair: the step it or its rollback took last.
func packagePinRecoverFailure(ctx context.Context, env core.Env, p pinRow) (string, error) {
	var step, result string
	err := env.DB.Read().QueryRowContext(ctx, packagePinRecoverLastStepSQL, p.ChangeID).Scan(&step, &result)
	if errors.Is(err, sql.ErrNoRows) {
		return "the pin of " + p.Version + " failed", nil
	}
	if err != nil {
		return "", fmt.Errorf("tailscale: read the last step of pin %s: %w", p.ChangeID, err)
	}
	return "the pin of " + p.Version + " failed; last step " + step + ": " + result, nil
}

package tailscale

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// packageUnpinResource is the one effect of an unpin: the tailscale package's hold.
const packageUnpinResource = "tailscale.package:tailscale"

// packageUnpinBySQL finds the pin a change unpinned.
const packageUnpinBySQL = "SELECT change_id, prior_held FROM tailscale_pins WHERE unpinned_by = ?"

// packageUnpin is tailscale.package.unpin: it releases the active pin, restoring the hold state the pin replaced and
// leaving the installed version alone. With no active pin it changes nothing.
func (t ts) packageUnpin() core.ActionDef {
	return core.DefineTask[UnpinInput, Unpinned](core.ActionSpec{
		Name: "tailscale.package.unpin", Summary: "Release the pin of the tailscale package and restore the hold state it replaced.",
		Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{lockKey},
		Converges: true, ApplyTimeout: 5 * time.Minute,
		Available: t.available,
	}, packageUnpinHandler{t: t})
}

// packageUnpinHandler is package.unpin's handler. Which pin it releases is read from the agent database at plan and
// again in the task, so a pin that appears or is released in between changes the plan's state hash.
type packageUnpinHandler struct{ t ts }

// packageUnpinHoldDoc is the state of the tailscale package's hold.
func packageUnpinHoldDoc(held bool) core.StateDoc {
	b, _ := json.Marshal(struct {
		Held bool `json:"held"`
	}{held})
	return core.StateDoc{State: b}
}

// packageUnpinHoldText describes the hold state held.
func packageUnpinHoldText(held bool) string {
	if held {
		return "tailscale is held"
	}
	return "tailscale is not held"
}

// packageUnpinHash is the hex SHA-256 of state.
func packageUnpinHash(state string) string {
	sum := sha256.Sum256([]byte(state))
	return hex.EncodeToString(sum[:])
}

// packageUnpinHost reads the installed version and the hold state.
func (h packageUnpinHandler) packageUnpinHost(ctx context.Context, env core.Env) (installed string, held bool, err error) {
	if installed, err = h.t.installedVersion(ctx, env); err != nil {
		return "", false, err
	}
	if held, err = h.t.held(ctx, env); err != nil {
		return "", false, err
	}
	return installed, held, nil
}

// packageUnpinBy returns the pin change changeID unpinned; ok is false when it unpinned none.
func packageUnpinBy(ctx context.Context, env core.Env, changeID string) (pinID string, priorHeld, ok bool, err error) {
	err = env.DB.Read().QueryRowContext(ctx, packageUnpinBySQL, changeID).Scan(&pinID, &priorHeld)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, false, nil
	}
	if err != nil {
		return "", false, false, fmt.Errorf("tailscale: read the pin change %s unpinned: %w", changeID, err)
	}
	return pinID, priorHeld, true, nil
}

func (h packageUnpinHandler) Plan(ctx context.Context, env core.Env, _ UnpinInput) (core.PlanOutcome, error) {
	p, ok, err := activePin(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if !ok {
		return core.PlanOutcome{StateHash: packageUnpinHash("no active pin")}, nil
	}
	installed, held, err := h.packageUnpinHost(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	want := p.PriorHeld
	diff := []core.DiffLine{{Op: " ", Text: "release the pin of change " + p.ChangeID + ": tailscale " + installed + " stays installed"}}
	if held == want {
		diff = append(diff, core.DiffLine{Op: " ", Text: packageUnpinHoldText(held) + " (unchanged)"})
	} else {
		diff = append(diff, core.DiffLine{Op: "-", Text: packageUnpinHoldText(held)}, core.DiffLine{Op: "+", Text: packageUnpinHoldText(want)})
	}
	return core.PlanOutcome{Diff: diff, StateHash: packageUnpinHash(p.ChangeID + "\n" + installed + "\n" + strconv.FormatBool(held))}, nil
}

// Task restores the active pin's prior hold state and then marks the pin unpinned, so a crash in between leaves
// the hold right and the pin active. The engine does not run Task again for this change; a new unpin change
// finishes the pin without another apt-mark, and still journals Before {"held":true}.
func (h packageUnpinHandler) Task(ctx context.Context, env core.Env, _ UnpinInput) (Unpinned, error) {
	p, ok, err := activePin(ctx, env.DB.Read())
	if err != nil {
		return Unpinned{}, err
	}
	installed, held, err := h.packageUnpinHost(ctx, env)
	if err != nil {
		return Unpinned{}, err
	}
	if !ok {
		return Unpinned{Version: installed, Held: held}, nil
	}
	after := packageUnpinHoldDoc(p.PriorHeld)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectModify, Resource: packageUnpinResource, Local: true, InverseLocal: true,
		Before: packageUnpinHoldDoc(true), After: &after,
	}, nil)
	if err != nil {
		return Unpinned{}, err
	}
	// A failed apt-mark leaves the effect intended; the engine settles it through Observe.
	if held != p.PriorHeld {
		verb := "unhold"
		if p.PriorHeld {
			verb = "hold"
		}
		if _, err := run(ctx, env, txAptMark, verb, "tailscale"); err != nil {
			return Unpinned{}, err
		}
	}
	if err := setPinUnpinned(ctx, env.DB, p.ChangeID, env.ChangeID, env.Clock.Now()); err != nil {
		return Unpinned{}, err
	}
	if err := env.Effects.Made(ctx, seq, packageUnpinResource, after); err != nil {
		return Unpinned{}, err
	}
	return Unpinned{Version: installed, Held: p.PriorHeld}, nil
}

// Verify checks the host from the input alone, as recovery calls it with the zero output: the hold state is the
// prior one of the pin the change unpinned, if any.
func (h packageUnpinHandler) Verify(ctx context.Context, env core.Env, _ UnpinInput, _ Unpinned) error {
	_, want, ok, err := packageUnpinBy(ctx, env, env.ChangeID)
	if err != nil || !ok {
		return err
	}
	held, err := h.t.held(ctx, env)
	if err != nil {
		return err
	}
	if held != want {
		return fmt.Errorf("tailscale: the package is %s after the unpin, want %s", packageUnpinHeldWord(held), packageUnpinHeldWord(want))
	}
	return nil
}

// packageUnpinHeldWord names the hold state held.
func packageUnpinHeldWord(held bool) string {
	if held {
		return "held"
	}
	return "not held"
}

// packageUnpinTarget refuses an effect on any resource other than the package's hold.
func packageUnpinTarget(e core.Effect) error {
	if e.Resource != packageUnpinResource {
		return fmt.Errorf("tailscale: %q is not a resource tailscale.package.unpin changes", e.Resource)
	}
	return nil
}

func (h packageUnpinHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if err := packageUnpinTarget(e); err != nil {
		return core.Observation{}, err
	}
	held, err := h.t.held(ctx, env)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: packageUnpinHoldDoc(held), Matches: 1, Exclusive: true}, nil
}

// Inverse holds the package again and only then makes the pin the change unpinned active again, so a failed
// apt-mark leaves the pin marked unpinned.
func (h packageUnpinHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	if err := packageUnpinTarget(e); err != nil {
		return core.InverseResult{}, err
	}
	held, err := h.t.held(ctx, env)
	if err != nil {
		return core.InverseResult{}, err
	}
	if !held {
		if _, err := run(ctx, env, txAptMark, "hold", "tailscale"); err != nil {
			return core.InverseResult{}, err
		}
	}
	pinID, _, ok, err := packageUnpinBy(ctx, env, e.ChangeID)
	if err != nil || !ok {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, setPinUnpinned(ctx, env.DB, pinID, "", env.Clock.Now())
}

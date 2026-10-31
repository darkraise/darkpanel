package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/core"
)

func (t tn) deviceAuthorize() core.ActionDef {
	return core.DefineMutating[DeviceInput, Device](core.ActionSpec{
		Name: ID + ".device.authorize", Summary: "Authorize a device to join the tailnet.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"tailnet"},
	}, deviceAuthorizeHandler{t: t})
}

// deviceAuthorizeHandler authorizes one device; its undo restores the
// authorization the device had before.
type deviceAuthorizeHandler struct{ t tn }

func authorizedState(authorized bool) json.RawMessage {
	return json.RawMessage(`{"authorized":` + strconv.FormatBool(authorized) + `}`)
}

func (h deviceAuthorizeHandler) Plan(ctx context.Context, env core.Env, in DeviceInput) (core.PlanOutcome, error) {
	if err := requireDevice(in.Device); err != nil {
		return core.PlanOutcome{}, err
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	hash := d.ID + " authorized=" + strconv.FormatBool(d.Authorized)
	if d.Authorized {
		return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: hash}, nil
	}
	diff := []core.DiffLine{
		deviceContext(d),
		{Op: "-", Text: "authorized: false"},
		{Op: "+", Text: "authorized: true"},
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h deviceAuthorizeHandler) Apply(ctx context.Context, env core.Env, in DeviceInput) (Device, error) {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return Device{}, err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return Device{}, err
	}
	if d.Authorized {
		return deviceView(d), nil
	}
	resource := deviceResourcePrefix + d.ID
	e := cloudEffect(core.EffectModify, resource, authorizedState(false))
	e.After = &core.StateDoc{State: authorizedState(true)}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return Device{}, err
	}
	if err := a.Authorize(ctx, d.ID, true); err != nil {
		// Only a definite refusal proves nothing committed; any other error
		// stays intended for the engine to settle.
		if core.IsDefinite(err) {
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return Device{}, errors.Join(err, fmt.Errorf("tailnet: journal %s: %w", resource, serr))
			}
		}
		return Device{}, err
	}
	if err := env.Effects.Made(ctx, seq, resource, core.StateDoc{State: authorizedState(true)}); err != nil {
		return Device{}, err
	}
	d.Authorized = true
	return deviceView(d), nil
}

func (h deviceAuthorizeHandler) Verify(ctx context.Context, env core.Env, in DeviceInput, _ Device) error {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return err
	}
	if !d.Authorized {
		return fmt.Errorf("tailnet: device %s is not authorized", in.Device)
	}
	return nil
}

// Observe reads the authorization of the device the effect names; a device
// that no longer exists observes null.
func (h deviceAuthorizeHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := effectDevice(e, deviceResourcePrefix)
	if err != nil {
		return core.Observation{}, err
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	d, err := a.Device(ctx, id)
	return observedDevice(e, d, err, func(d Device) (json.RawMessage, error) { return authorizedState(d.Authorized), nil })
}

// Inverse sets the device's authorization back to the state the undo
// restores.
func (h deviceAuthorizeHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := effectDevice(e, deviceResourcePrefix)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if e.UndoTo == nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: effect on %s has no state to undo to", e.Resource))
	}
	var to struct {
		Authorized bool `json:"authorized"`
	}
	if err := json.Unmarshal(e.UndoTo.State, &to); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: decode the undo state of %s: %w", e.Resource, err))
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	return core.InverseResult{}, a.Authorize(ctx, id, to.Authorized)
}

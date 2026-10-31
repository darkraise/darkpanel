package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (t tn) deviceSetRoutes() core.ActionDef {
	return core.DefineMutating[SetRoutesInput, DeviceRoutes](core.ActionSpec{
		Name: ID + ".device.set-routes", Summary: "Set the routes a device is approved to serve.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"tailnet"},
	}, deviceSetRoutesHandler{t: t})
}

// deviceSetRoutesHandler replaces a device's approved routes; its undo
// restores the list approved before.
type deviceSetRoutesHandler struct{ t tn }

// parseRoutes is routes in canonical prefix form, sorted, de-duplicated and
// never nil.
func parseRoutes(in []string) ([]string, error) {
	out := make([]string, 0, len(in))
	for _, r := range in {
		p, err := netip.ParsePrefix(r)
		if err != nil {
			return nil, fmt.Errorf("tailnet: route %q is not an IP prefix: %w", r, err)
		}
		out = append(out, p.String())
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

func routesState(enabled []string) (json.RawMessage, error) {
	b, err := json.Marshal(struct {
		EnabledRoutes []string `json:"enabledRoutes"`
	}{orEmpty(enabled)})
	if err != nil {
		return nil, fmt.Errorf("tailnet: encode the routes state: %w", err)
	}
	return b, nil
}

func routesInvalid(detail string) error {
	return core.Definite(&errcode.Error{
		Code: errcode.InvalidInput, Detail: detail,
		Fields: []errcode.FieldError{{Path: "/routes", Message: detail}},
	})
}

// wantedRoutes parses the input's routes, refusing a malformed one at the
// input's /routes field.
func wantedRoutes(in SetRoutesInput) ([]string, error) {
	wanted, err := parseRoutes(in.Routes)
	if err != nil {
		return nil, routesInvalid(err.Error())
	}
	return wanted, nil
}

// requireAdvertised refuses a wanted route the device does not advertise.
func requireAdvertised(wanted []string, d Device) error {
	for _, r := range wanted {
		if !slices.Contains(d.AdvertisedRoutes, r) {
			return routesInvalid(fmt.Sprintf("device %s does not advertise route %s", d.ID, r))
		}
	}
	return nil
}

func (h deviceSetRoutesHandler) Plan(ctx context.Context, env core.Env, in SetRoutesInput) (core.PlanOutcome, error) {
	if err := requireDevice(in.Device); err != nil {
		return core.PlanOutcome{}, err
	}
	wanted, err := wantedRoutes(in)
	if err != nil {
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
	d = deviceView(d)
	if err := requireAdvertised(wanted, d); err != nil {
		return core.PlanOutcome{}, err
	}
	hash := d.ID + " enabled=" + strings.Join(d.EnabledRoutes, ",")
	if slices.Equal(wanted, d.EnabledRoutes) {
		return core.PlanOutcome{Diff: []core.DiffLine{}, StateHash: hash}, nil
	}
	diff := []core.DiffLine{deviceContext(d)}
	for _, r := range d.EnabledRoutes {
		if !slices.Contains(wanted, r) {
			diff = append(diff, core.DiffLine{Op: "-", Text: "route " + r})
		}
	}
	for _, r := range wanted {
		if !slices.Contains(d.EnabledRoutes, r) {
			diff = append(diff, core.DiffLine{Op: "+", Text: "route " + r})
		}
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h deviceSetRoutesHandler) Apply(ctx context.Context, env core.Env, in SetRoutesInput) (DeviceRoutes, error) {
	wanted, err := wantedRoutes(in)
	if err != nil {
		return DeviceRoutes{}, err
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return DeviceRoutes{}, err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return DeviceRoutes{}, err
	}
	d = deviceView(d)
	if err := requireAdvertised(wanted, d); err != nil {
		return DeviceRoutes{}, err
	}
	if slices.Equal(wanted, d.EnabledRoutes) {
		return DeviceRoutes{Advertised: d.AdvertisedRoutes, Enabled: d.EnabledRoutes}, nil
	}
	before, err := routesState(d.EnabledRoutes)
	if err != nil {
		return DeviceRoutes{}, err
	}
	after, err := routesState(wanted)
	if err != nil {
		return DeviceRoutes{}, err
	}
	resource := routesResourcePrefix + d.ID
	e := cloudEffect(core.EffectModify, resource, before)
	e.After = &core.StateDoc{State: after}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return DeviceRoutes{}, err
	}
	routes, err := a.SetRoutes(ctx, d.ID, wanted)
	if err != nil {
		// Only a definite refusal proves nothing committed; any other error
		// stays intended for the engine to settle.
		if core.IsDefinite(err) {
			if serr := env.Effects.Skipped(ctx, seq); serr != nil {
				return DeviceRoutes{}, errors.Join(err, fmt.Errorf("tailnet: journal %s: %w", resource, serr))
			}
		}
		return DeviceRoutes{}, err
	}
	if err := env.Effects.Made(ctx, seq, resource, core.StateDoc{State: after}); err != nil {
		return DeviceRoutes{}, err
	}
	return DeviceRoutes{Advertised: sortedRoutes(routes.Advertised), Enabled: sortedRoutes(routes.Enabled)}, nil
}

func (h deviceSetRoutesHandler) Verify(ctx context.Context, env core.Env, in SetRoutesInput, _ DeviceRoutes) error {
	wanted, err := parseRoutes(in.Routes)
	if err != nil {
		return err
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	d, err := a.Device(ctx, in.Device)
	if err != nil {
		return err
	}
	if enabled := sortedRoutes(d.EnabledRoutes); !slices.Equal(enabled, wanted) {
		return fmt.Errorf("tailnet: device %s approves routes %v, want %v", in.Device, enabled, wanted)
	}
	return nil
}

// Observe reads the approved routes of the device the effect names; a
// device that no longer exists observes null.
func (h deviceSetRoutesHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := effectDevice(e, routesResourcePrefix)
	if err != nil {
		return core.Observation{}, err
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.Observation{}, err
	}
	d, err := a.Device(ctx, id)
	return observedDevice(e, d, err, func(d Device) (json.RawMessage, error) { return routesState(sortedRoutes(d.EnabledRoutes)) })
}

// Inverse approves again the routes the undo restores.
func (h deviceSetRoutesHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := effectDevice(e, routesResourcePrefix)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	if e.UndoTo == nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: effect on %s has no state to undo to", e.Resource))
	}
	var to struct {
		EnabledRoutes []string `json:"enabledRoutes"`
	}
	if err := json.Unmarshal(e.UndoTo.State, &to); err != nil {
		return core.InverseResult{}, core.Definite(fmt.Errorf("tailnet: decode the undo state of %s: %w", e.Resource, err))
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.InverseResult{}, core.Definite(err)
	}
	_, err = a.SetRoutes(ctx, id, to.EnabledRoutes)
	return core.InverseResult{}, err
}

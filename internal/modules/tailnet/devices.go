package tailnet

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	deviceResourcePrefix = "tailnet.device:"
	routesResourcePrefix = "tailnet.routes:"
)

// deviceView is the form of a device every action answers: lists never
// nil, routes sorted and de-duplicated, and the exit-node flags derived
// from them.
func deviceView(d Device) Device {
	d.Addresses = orEmpty(d.Addresses)
	d.Tags = orEmpty(d.Tags)
	d.AdvertisedRoutes = sortedRoutes(d.AdvertisedRoutes)
	d.EnabledRoutes = sortedRoutes(d.EnabledRoutes)
	d.ExitNode = slices.Contains(d.AdvertisedRoutes, exitRouteV4) || slices.Contains(d.AdvertisedRoutes, exitRouteV6)
	d.ExitNodeEnabled = slices.Contains(d.EnabledRoutes, exitRouteV4) && slices.Contains(d.EnabledRoutes, exitRouteV6)
	return d
}

// sortedRoutes is a sorted, de-duplicated, never nil copy of routes.
func sortedRoutes(routes []string) []string {
	return orEmpty(slices.Compact(slices.Sorted(slices.Values(routes))))
}

// requireDevice refuses an empty device id at the input's /device field.
func requireDevice(id string) error {
	if id != "" {
		return nil
	}
	return core.Definite(&errcode.Error{
		Code: errcode.InvalidInput, Detail: "a device id is required",
		Fields: []errcode.FieldError{{Path: "/device", Message: "the device's node id is required"}},
	})
}

// deviceContext is the diff's context line naming the device.
func deviceContext(d Device) core.DiffLine {
	return core.DiffLine{Op: " ", Text: "device " + d.Name + " (" + d.ID + ")"}
}

// effectDevice is the node id an effect's resource names after prefix.
func effectDevice(e core.Effect, prefix string) (string, error) {
	id, ok := strings.CutPrefix(e.Resource, prefix)
	if !ok || id == "" {
		return "", fmt.Errorf("tailnet: %q does not name a device", e.Resource)
	}
	return id, nil
}

// observedDevice is the observation of a device-level effect: null once the
// device is gone, else the state state renders from the device.
func observedDevice(e core.Effect, d Device, err error, state func(Device) (json.RawMessage, error)) (core.Observation, error) {
	obs := core.Observation{Resource: e.Resource, Matches: 1, Exclusive: true}
	switch {
	case err != nil && errcode.From(err).Code == errcode.NotFound:
		obs.Current = core.StateDoc{State: json.RawMessage("null")}
		return obs, nil
	case err != nil:
		return core.Observation{}, err
	}
	s, err := state(d)
	if err != nil {
		return core.Observation{}, err
	}
	obs.Current = core.StateDoc{State: s}
	return obs, nil
}

func (t tn) deviceList() core.ActionDef {
	return core.DefineRead[DeviceListInput, DeviceList](core.ActionSpec{
		Name: ID + ".device.list", Summary: "List the tailnet's devices.", Version: 1, Side: core.SideController,
	}, deviceListHandler{t: t})
}

// deviceListHandler lists the connected tailnet's devices by name.
type deviceListHandler struct{ t tn }

func (h deviceListHandler) Read(ctx context.Context, env core.Env, _ DeviceListInput) (DeviceList, error) {
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return DeviceList{}, err
	}
	devices, err := a.Devices(ctx)
	if err != nil {
		return DeviceList{}, err
	}
	out := make([]Device, 0, len(devices))
	for _, d := range devices {
		out = append(out, deviceView(d))
	}
	slices.SortFunc(out, func(x, y Device) int { return cmp.Or(cmp.Compare(x.Name, y.Name), cmp.Compare(x.ID, y.ID)) })
	return DeviceList{Devices: out}, nil
}

package coreactions

import (
	"context"
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TailnetDeviceList is the output of core.tailnetdevice.list/v1 and
// core.user.list-tailnet-devices/v1.
type TailnetDeviceList struct {
	Devices []auth.TailnetDevice `json:"devices" jsonschema_description:"The user's remembered Tailscale devices, most recently used first. Continue as from one of them asks no code."`
}

// TailnetDeviceInput is the input of core.tailnetdevice.forget/v1.
type TailnetDeviceInput struct {
	StableID string `json:"stableId" jsonschema:"minLength=1" jsonschema_description:"Tailscale stable node id of one of the calling user's remembered devices."`
}

// UserTailnetDeviceInput is the input of core.user.forget-tailnet-device/v1.
type UserTailnetDeviceInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name."`
	StableID string `json:"stableId" jsonschema:"minLength=1" jsonschema_description:"Tailscale stable node id of one of the user's remembered devices."`
}

func (m module) tailnetDeviceActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, TailnetDeviceList](core.ActionSpec{
			Name: "core.tailnetdevice.list", Version: 1, Side: core.SideController,
			HostData: core.HostDataNone,
			Summary:  "List the calling user's remembered Tailscale devices.",
		}, ownDeviceList{m}),
		core.DefineMutating[TailnetDeviceInput, auth.TailnetDevice](core.ActionSpec{
			Name: "core.tailnetdevice.forget", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Forget one of the calling user's remembered Tailscale devices, so its next Continue as asks the code; the confirmation phrase is the stable id.",
		}, ownDeviceForget{m}),
		core.DefineRead[UsernameInput, TailnetDeviceList](core.ActionSpec{
			Name: "core.user.list-tailnet-devices", Version: 1, Side: core.SideController,
			MinScope: core.ScopeAdmin, HostData: core.HostDataNone,
			Summary: "List a user's remembered Tailscale devices.",
		}, userDeviceList{m}),
		core.DefineMutating[UserTailnetDeviceInput, auth.TailnetDevice](core.ActionSpec{
			Name: "core.user.forget-tailnet-device", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			MinScope: core.ScopeAdmin,
			Summary:  "Forget one of a user's remembered Tailscale devices, so its next Continue as asks the code; the confirmation phrase is the stable id.",
		}, userDeviceForget{m}),
	}
}

// deviceOwner is the service and the username of the principal's user, by
// tokenManager's rule when manage is set and tokenOwner's otherwise.
func (m module) deviceOwner(ctx context.Context, p core.Principal, manage bool) (*auth.Service, string, error) {
	owner := m.tokenOwner
	if manage {
		owner = func(p core.Principal) (*auth.Service, string, error) { return m.tokenManager(p, "") }
	}
	svc, userID, err := owner(p)
	if err != nil {
		return nil, "", err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return nil, "", err
	}
	for _, u := range users {
		if u.ID == userID {
			return svc, u.Username, nil
		}
	}
	return nil, "", errcode.New(errcode.NotFound, "the calling user is not a user")
}

func listDevices(ctx context.Context, svc *auth.Service, username string) (TailnetDeviceList, error) {
	devices, err := svc.ListTailnetDevices(ctx, username)
	if err != nil {
		return TailnetDeviceList{}, err
	}
	return TailnetDeviceList{Devices: devices}, nil
}

func planForget(ctx context.Context, svc *auth.Service, username, stableID string) (core.PlanOutcome, error) {
	devices, err := svc.ListTailnetDevices(ctx, username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	for _, d := range devices {
		if d.StableID != stableID {
			continue
		}
		name := d.NodeName
		if name == "" {
			name = "Unnamed device"
		}
		return core.PlanOutcome{
			Diff: []core.DiffLine{
				{Op: "-", Text: fmt.Sprintf("remembered device %s (%s) of user %s: its next Continue as asks the code", name, d.StableID, username)},
				{Op: " ", Text: "sessions it opened stay signed in"},
			},
			Guards:       []core.Guard{},
			StateHash:    stateHash(username, d.StableID, formatTime(d.RememberedAt)),
			Confirmation: d.StableID,
		}, nil
	}
	return core.PlanOutcome{}, errcode.New(errcode.NotFound, "device not found")
}

func verifyForgotten(ctx context.Context, svc *auth.Service, username, stableID string) error {
	ok, err := svc.TailnetDeviceExists(ctx, username, stableID)
	if err != nil {
		return err
	}
	if ok {
		return fmt.Errorf("coreactions: device %s of user %s is still remembered", stableID, username)
	}
	return nil
}

type ownDeviceList struct{ m module }

func (h ownDeviceList) Read(ctx context.Context, env core.Env, _ NoInput) (TailnetDeviceList, error) {
	svc, username, err := h.m.deviceOwner(ctx, env.Principal, false)
	if err != nil {
		return TailnetDeviceList{}, err
	}
	return listDevices(ctx, svc, username)
}

type ownDeviceForget struct{ m module }

func (h ownDeviceForget) Plan(ctx context.Context, env core.Env, in TailnetDeviceInput) (core.PlanOutcome, error) {
	svc, username, err := h.m.deviceOwner(ctx, env.Principal, true)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return planForget(ctx, svc, username, in.StableID)
}

func (h ownDeviceForget) Apply(ctx context.Context, env core.Env, in TailnetDeviceInput) (auth.TailnetDevice, error) {
	svc, username, err := h.m.deviceOwner(ctx, env.Principal, true)
	if err != nil {
		return auth.TailnetDevice{}, err
	}
	return svc.ForgetTailnetDevice(ctx, username, in.StableID)
}

// Verify reads the device from in. Recovery's principal has no user, so an
// interrupted forget is reported as not verified.
func (h ownDeviceForget) Verify(ctx context.Context, env core.Env, in TailnetDeviceInput, _ auth.TailnetDevice) error {
	svc, username, err := h.m.deviceOwner(ctx, env.Principal, false)
	if err != nil {
		return err
	}
	return verifyForgotten(ctx, svc, username, in.StableID)
}

type userDeviceList struct{ m module }

func (h userDeviceList) Read(ctx context.Context, _ core.Env, in UsernameInput) (TailnetDeviceList, error) {
	svc, err := h.m.authService()
	if err != nil {
		return TailnetDeviceList{}, err
	}
	return listDevices(ctx, svc, in.Username)
}

type userDeviceForget struct{ m module }

func (h userDeviceForget) Plan(ctx context.Context, _ core.Env, in UserTailnetDeviceInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return planForget(ctx, svc, in.Username, in.StableID)
}

func (h userDeviceForget) Apply(ctx context.Context, _ core.Env, in UserTailnetDeviceInput) (auth.TailnetDevice, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.TailnetDevice{}, err
	}
	return svc.ForgetTailnetDevice(ctx, in.Username, in.StableID)
}

func (h userDeviceForget) Verify(ctx context.Context, _ core.Env, in UserTailnetDeviceInput, _ auth.TailnetDevice) error {
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	return verifyForgotten(ctx, svc, in.Username, in.StableID)
}

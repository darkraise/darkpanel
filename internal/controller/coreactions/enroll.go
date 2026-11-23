package coreactions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// UserActivateInput is the input of core.user.activate/v1.
type UserActivateInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The pending user's name."`
	Code     string `json:"code" secret:"true" jsonschema:"pattern=^[0-9]{6}$" jsonschema_description:"The six-digit code the authenticator shows for the user's new TOTP secret."`
}

// UserCreateInput is the input of core.user.create/v1.
type UserCreateInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name: lower-case letters, digits, dot, underscore and hyphen, starting with a letter or digit, at most 64 characters."`
	Password string `json:"password" secret:"true" jsonschema:"minLength=12,maxLength=128" jsonschema_description:"The password, 12 to 128 characters."`
	Role     string `json:"role,omitempty" jsonschema:"enum=admin,enum=member" jsonschema_description:"admin, the default, has every scope on every host; member starts with no grants."`
}

// role is the role the input asks for, admin when it names none.
func (in UserCreateInput) role() string {
	if in.Role == "" {
		return auth.RoleAdmin
	}
	return in.Role
}

func (m module) enrollActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[UserCreateInput, Enrollment](core.ActionSpec{
			Name: "core.user.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController, BreakGlass: true,
			CompensatedBy: "core.user.delete",
			Summary:       "Create a pending user, an admin unless the role says member, and return its TOTP secret, shown only once.",
		}, userCreate{m}),
		core.DefineMutating[UserActivateInput, auth.User](core.ActionSpec{
			Name: "core.user.activate", Version: 1, Class: core.ClassCompensable, Side: core.SideController, BreakGlass: true,
			CompensatedBy: "core.user.reset-totp",
			Summary:       "Activate a pending user with a code from its authenticator, confirming its TOTP enrollment.",
		}, userActivate{m}),
	}
}

type userCreate struct{ m module }

func (h userCreate) Plan(ctx context.Context, _ core.Env, in UserCreateInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	taken := false
	for _, u := range users {
		taken = taken || u.Username == in.Username
	}
	guard := core.Guard{ID: "username-free", OK: !taken, Message: "no user is named " + in.Username}
	if taken {
		guard.Message = "a user named " + in.Username + " already exists"
	}
	diff := []core.DiffLine{{Op: "+", Text: fmt.Sprintf("user %s: pending %s, with a new TOTP secret shown once", in.Username, in.role())}}
	if in.role() == auth.RoleMember {
		diff = append(diff, core.DiffLine{Op: " ", Text: "no grants: the member sees no host until it is given some"})
	}
	diff = append(diff,
		core.DiffLine{Op: " ", Text: "core.user.activate with a code from the secret makes the user active"},
		core.DiffLine{Op: " ", Text: "compensated by core.user.delete"})
	return core.PlanOutcome{
		Diff:      diff,
		Guards:    []core.Guard{guard},
		StateHash: stateHash(in.Username, strconv.FormatBool(taken)),
	}, nil
}

func (h userCreate) Apply(ctx context.Context, _ core.Env, in UserCreateInput) (Enrollment, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Enrollment{}, err
	}
	u, secret, err := svc.CreateUserWithRole(ctx, in.Username, in.Password, in.role())
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{User: u, TOTPSecret: secret}, nil
}

func (h userCreate) Verify(ctx context.Context, _ core.Env, in UserCreateInput, _ Enrollment) error {
	return wantUserState(ctx, h.m, in.Username, "")
}

type userActivate struct{ m module }

// Plan cannot check the code: checking it needs the sealed secret and would
// spend the code's step, so a wrong code fails the apply instead.
func (h userActivate) Plan(ctx context.Context, _ core.Env, in UserActivateInput) (core.PlanOutcome, error) {
	_, u, _, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	pending := u.State == auth.StatePending
	guard := core.Guard{ID: "user-pending", OK: pending, Message: u.Username + " is pending"}
	if !pending {
		guard.Message = u.Username + " is already " + u.State
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: %s", u.Username, u.State)},
			{Op: "+", Text: fmt.Sprintf("user %s: active, if the code matches its authenticator", u.Username)},
			{Op: " ", Text: "compensated by core.user.reset-totp"},
		},
		Guards:    []core.Guard{guard},
		StateHash: stateHash(u.ID, u.State),
	}, nil
}

func (h userActivate) Apply(ctx context.Context, _ core.Env, in UserActivateInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.ActivateUser(ctx, in.Username, in.Code)
}

func (h userActivate) Verify(ctx context.Context, _ core.Env, in UserActivateInput, _ auth.User) error {
	return wantUserState(ctx, h.m, in.Username, auth.StateActive)
}

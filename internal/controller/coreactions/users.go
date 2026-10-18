package coreactions

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// UserList is the output of core.user.list/v1.
type UserList struct {
	Users []auth.User `json:"users" jsonschema_description:"Every user, sorted by username."`
}

// UsernameInput is the input of core.user.reset-totp/v1 and core.user.delete/v1.
type UsernameInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name."`
}

// UserPasswordInput is the input of core.user.create/v1 and
// core.user.reset-password/v1.
type UserPasswordInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name: lower-case letters, digits, dot, underscore and hyphen, starting with a letter or digit, at most 64 characters."`
	Password string `json:"password" secret:"true" jsonschema:"minLength=12,maxLength=128" jsonschema_description:"The password, 12 to 128 characters."`
}

// Enrollment is the output of core.user.create/v1 and core.user.reset-totp/v1:
// the user and its new TOTP secret, which only the apply that made it returns.
type Enrollment struct {
	User       auth.User `json:"user" jsonschema_description:"The user, pending until core.user.activate confirms a code."`
	TOTPSecret string    `json:"totpSecret" secret:"true" jsonschema_description:"The TOTP secret in unpadded base32, for an authenticator app; shown only once."`
}

func (m module) userActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, UserList](core.ActionSpec{
			Name: "core.user.list", Version: 1, Side: core.SideController, BreakGlass: true,
			Summary: "List the users; served only on the controller socket.",
		}, userList{m}),
		core.DefineMutating[UsernameInput, Enrollment](core.ActionSpec{
			Name: "core.user.reset-totp", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Give a user a new TOTP secret and return it to pending, ending its sessions; the confirmation phrase is the username.",
		}, userResetTOTP{m}),
		core.DefineMutating[UserPasswordInput, auth.User](core.ActionSpec{
			Name: "core.user.reset-password", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Set a user's password, end its sessions and revoke its API tokens; the confirmation phrase is the username.",
		}, userResetPassword{m}),
		core.DefineMutating[UsernameInput, Empty](core.ActionSpec{
			Name: "core.user.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Delete a user with its sessions and API tokens, refusing the last active admin; the confirmation phrase is the username.",
		}, userDelete{m}),
	}
}

// userState returns username and whether it is the last active admin, the
// one user core.user.delete refuses.
func (m module) userState(ctx context.Context, username string) (*auth.Service, auth.User, bool, error) {
	svc, err := m.authService()
	if err != nil {
		return nil, auth.User{}, false, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return nil, auth.User{}, false, err
	}
	var u auth.User
	admins := 0
	for _, each := range users {
		if each.Username == username {
			u = each
		}
		if each.State == auth.StateActive && each.Role == auth.RoleAdmin {
			admins++
		}
	}
	if u.ID == "" {
		return nil, auth.User{}, false, errcode.New(errcode.NotFound, fmt.Sprintf("no user %s", username))
	}
	last := u.State == auth.StateActive && u.Role == auth.RoleAdmin && admins == 1
	return svc, u, last, nil
}

type userList struct{ m module }

func (h userList) Read(ctx context.Context, _ core.Env, _ NoInput) (UserList, error) {
	svc, err := h.m.authService()
	if err != nil {
		return UserList{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return UserList{}, err
	}
	return UserList{Users: users}, nil
}

type userResetTOTP struct{ m module }

func (h userResetTOTP) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	_, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: fmt.Sprintf("user %s: %s, with its enrolled authenticator", u.Username, u.State)},
		{Op: "+", Text: fmt.Sprintf("user %s: pending, with a new TOTP secret shown once; its sessions end and its API tokens stay", u.Username)},
		{Op: " ", Text: "core.user.activate with a code from the new secret makes the user active again"},
	}
	if last {
		diff = append(diff, core.DiffLine{Op: " ", Text: u.Username + " is the last active admin: until it is activated again, no one can log in"})
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(u.ID, u.State, strconv.FormatBool(last)),
		Confirmation: u.Username,
	}, nil
}

func (h userResetTOTP) Apply(ctx context.Context, _ core.Env, in UsernameInput) (Enrollment, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Enrollment{}, err
	}
	u, secret, err := svc.ResetTOTP(ctx, in.Username)
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{User: u, TOTPSecret: secret}, nil
}

func (h userResetTOTP) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ Enrollment) error {
	return wantUserState(ctx, h.m, in.Username, auth.StatePending)
}

// wantUserState fails unless username exists and, when state is not empty,
// is in that state.
func wantUserState(ctx context.Context, m module, username, state string) error {
	svc, err := m.authService()
	if err != nil {
		return err
	}
	u, err := svc.GetUser(ctx, username)
	if err != nil {
		return err
	}
	if state != "" && u.State != state {
		return fmt.Errorf("coreactions: user %s is %s, want %s", username, u.State, state)
	}
	return nil
}

type userResetPassword struct{ m module }

func (h userResetPassword) Plan(ctx context.Context, _ core.Env, in UserPasswordInput) (core.PlanOutcome, error) {
	_, u, _, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: current password", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: new password; its sessions end and its API tokens are revoked", u.Username)},
		},
		Guards: []core.Guard{},
		// The diff names no token, so tokens made or revoked after the plan
		// change nothing it shows: the apply revokes whatever is live then.
		StateHash:    stateHash(u.ID),
		Confirmation: u.Username,
	}, nil
}

func (h userResetPassword) Apply(ctx context.Context, _ core.Env, in UserPasswordInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.ResetPassword(ctx, in.Username, in.Password)
}

// Verify can check only that the user still exists: the service compares a
// password only during a login.
func (h userResetPassword) Verify(ctx context.Context, _ core.Env, in UserPasswordInput, _ auth.User) error {
	return wantUserState(ctx, h.m, in.Username, "")
}

type userDelete struct{ m module }

func (h userDelete) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	_, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guard := core.Guard{ID: "not-last-admin", OK: !last, Message: u.Username + " is not the last active admin"}
	if last {
		guard.Message = u.Username + " is the last active admin; create and activate another admin first"
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s (%s, role %s), with its sessions and API tokens", u.Username, u.State, u.Role)},
		},
		Guards:       []core.Guard{guard},
		StateHash:    stateHash(u.ID, u.State, strconv.FormatBool(last)),
		Confirmation: u.Username,
	}, nil
}

func (h userDelete) Apply(ctx context.Context, _ core.Env, in UsernameInput) (Empty, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Empty{}, err
	}
	return Empty{}, svc.DeleteUser(ctx, in.Username)
}

func (h userDelete) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ Empty) error {
	err := wantUserState(ctx, h.m, in.Username, "")
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded) && coded.Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("coreactions: user %s still exists", in.Username)
}

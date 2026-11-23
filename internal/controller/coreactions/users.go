package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"time"

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
			HostData: core.HostDataFleet,
			Summary:  "List the users; served only on the controller socket.",
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

// activeAdmin reports whether u is an admin that can sign in: a disabled
// admin does not count toward the last active admin.
func activeAdmin(u auth.User) bool {
	return u.State == auth.StateActive && u.Role == auth.RoleAdmin && !u.Disabled
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
		if activeAdmin(each) {
			admins++
		}
	}
	if u.ID == "" {
		return nil, auth.User{}, false, errcode.New(errcode.NotFound, fmt.Sprintf("no user %s", username))
	}
	last := activeAdmin(u) && admins == 1
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

// InviteInput is the input of core.user.invite/v1.
type InviteInput struct {
	Username     string       `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The new user's name: lower-case letters, digits, dot, underscore and hyphen, starting with a letter or digit, at most 64 characters."`
	Role         string       `json:"role" jsonschema:"enum=admin,enum=member" jsonschema_description:"admin has every scope on every host; member has only what its grants give."`
	Grants       []auth.Grant `json:"grants,omitempty" jsonschema_description:"A member's grants, one per target; none for an admin."`
	Email        string       `json:"email,omitempty" jsonschema:"maxLength=254" jsonschema_description:"The email an OIDC sign-in matches; optional."`
	TailnetLogin string       `json:"tailnetLogin,omitempty" jsonschema:"maxLength=254" jsonschema_description:"The tailnet login a Tailscale sign-in matches; optional."`
}

// Invitation is the output of core.user.invite/v1 and
// core.user.renew-invite/v1: the user and its enrollment link's token, which
// only the apply that made it returns.
type Invitation struct {
	User      auth.User `json:"user" jsonschema_description:"The user, pending until it uses its enrollment link."`
	Token     string    `json:"token" secret:"true" jsonschema_description:"The enrollment link's token; the link is /enroll#<token> on the panel's origin. Shown only once."`
	ExpiresAt time.Time `json:"expiresAt" jsonschema_description:"When the link stops working, 72 hours after it was made."`
}

func (m module) inviteActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[InviteInput, Invitation](core.ActionSpec{
			Name: "core.user.invite", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.cancel-invite",
			Summary: "Invite a user: a pending user with no usable credentials and a one-time enrollment link valid 72 hours.",
		}, userInvite{m}),
		core.DefineMutating[UsernameInput, Invitation](core.ActionSpec{
			Name: "core.user.renew-invite", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.renew-invite",
			Summary: "Give an invited user that never enrolled a new enrollment link; the previous link stops working.",
		}, userInviteRenew{m}),
		core.DefineMutating[UsernameInput, auth.User](core.ActionSpec{
			Name: "core.user.cancel-invite", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.invite",
			Summary: "Remove an invited user that never enrolled, with its grants and its enrollment link; returns the removed user.",
		}, userInviteCancel{m}),
		core.DefineMutating[UsernameInput, auth.User](core.ActionSpec{
			Name: "core.user.disable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.enable",
			Summary: "Disable a user: its sessions, tokens and sign-ins are refused until core.user.enable; ends an unused enrollment link.",
		}, userSetDisabled{m: m, disabled: true}),
		core.DefineMutating[UsernameInput, auth.User](core.ActionSpec{
			Name: "core.user.enable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.disable",
			Summary: "Enable a disabled user, restoring its sessions and tokens.",
		}, userSetDisabled{m: m, disabled: false}),
	}
}

// grantQuerier reads the host groups grants name.
type grantQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func grantRefusal(path, message string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// checkGrants checks a member's grants: each target is "*" or an existing
// host group id, named once, and each role viewer or operator. It returns
// the name of each group a grant targets. Every action that stores grants
// checks them here.
func checkGrants(ctx context.Context, q grantQuerier, grants []auth.Grant) (map[string]string, error) {
	names := map[string]string{}
	seen := map[string]bool{}
	for i, g := range grants {
		at := fmt.Sprintf("/grants/%d", i)
		if g.Role != auth.GrantViewer && g.Role != auth.GrantOperator {
			return nil, grantRefusal(at+"/role", "must be viewer or operator")
		}
		if seen[g.Target] {
			return nil, grantRefusal(at+"/target", "names a target another grant names")
		}
		seen[g.Target] = true
		if g.Target == "*" {
			continue
		}
		var name string
		err := q.QueryRowContext(ctx, `SELECT name FROM core_host_groups WHERE id = ?`, g.Target).Scan(&name)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, grantRefusal(at+"/target", "is not * or an existing host group")
		}
		if err != nil {
			return nil, fmt.Errorf("coreactions: check grants: %w", err)
		}
		names[g.Target] = name
	}
	return names, nil
}

// grantText renders a grant for a diff line.
func grantText(g auth.Grant, names map[string]string) string {
	if g.Target == "*" {
		return g.Role + " on every host"
	}
	return fmt.Sprintf("%s on group %s", g.Role, names[g.Target])
}

type userInvite struct{ m module }

func (h userInvite) Plan(ctx context.Context, env core.Env, in InviteInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if in.Role == auth.RoleAdmin && len(in.Grants) > 0 {
		return core.PlanOutcome{}, grantRefusal("/grants", "an admin has every host; grants are for members")
	}
	names, err := checkGrants(ctx, env.DB.Read(), in.Grants)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	_, err = svc.GetUser(ctx, in.Username)
	taken := err == nil
	if err != nil && errcode.From(err).Code != errcode.NotFound {
		return core.PlanOutcome{}, err
	}
	guard := core.Guard{ID: "username-free", OK: !taken, Message: "no user is named " + in.Username}
	if taken {
		guard.Message = "a user named " + in.Username + " already exists"
	}
	diff := []core.DiffLine{{Op: "+", Text: fmt.Sprintf("user %s: pending %s, no usable password until its enrollment link is used",
		in.Username, in.Role)}}
	state := []string{in.Username, strconv.FormatBool(taken)}
	for _, g := range in.Grants {
		diff = append(diff, core.DiffLine{Op: "+", Text: "grant: " + grantText(g, names)})
		state = append(state, g.Target, names[g.Target])
	}
	if in.Role == auth.RoleMember && len(in.Grants) == 0 {
		diff = append(diff, core.DiffLine{Op: " ", Text: "no grants: the member sees no host until it is given some"})
	}
	diff = append(diff,
		core.DiffLine{Op: "+", Text: "an enrollment link, shown once and valid 72 hours"},
		core.DiffLine{Op: " ", Text: "compensated by core.user.cancel-invite"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{guard}, StateHash: stateHash(state...)}, nil
}

func (h userInvite) Apply(ctx context.Context, env core.Env, in InviteInput) (Invitation, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Invitation{}, err
	}
	if _, err := checkGrants(ctx, env.DB.Read(), in.Grants); err != nil {
		return Invitation{}, err
	}
	inv, err := svc.Invite(ctx, auth.InviteRequest{Username: in.Username, Role: in.Role, Grants: in.Grants,
		Email: in.Email, TailnetLogin: in.TailnetLogin})
	if err != nil {
		return Invitation{}, err
	}
	return Invitation{User: inv.User, Token: inv.Token, ExpiresAt: inv.ExpiresAt}, nil
}

func (h userInvite) Verify(ctx context.Context, _ core.Env, in InviteInput, _ Invitation) error {
	return wantUserState(ctx, h.m, in.Username, auth.StatePending)
}

type userInviteRenew struct{ m module }

func (h userInviteRenew) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	u, err := svc.Invited(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: its current enrollment link", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: a new enrollment link, shown once and valid 72 hours", u.Username)},
		},
		Guards:    []core.Guard{},
		StateHash: stateHash(u.ID, u.State),
	}, nil
}

func (h userInviteRenew) Apply(ctx context.Context, _ core.Env, in UsernameInput) (Invitation, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Invitation{}, err
	}
	inv, err := svc.RenewInvite(ctx, in.Username)
	if err != nil {
		return Invitation{}, err
	}
	return Invitation{User: inv.User, Token: inv.Token, ExpiresAt: inv.ExpiresAt}, nil
}

func (h userInviteRenew) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ Invitation) error {
	return wantUserState(ctx, h.m, in.Username, auth.StatePending)
}

type userInviteCancel struct{ m module }

func (h userInviteCancel) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	u, err := svc.Invited(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s (invited %s, never enrolled), with its grants and enrollment link", u.Username, u.Role)},
			{Op: " ", Text: "compensated by core.user.invite"},
		},
		Guards:    []core.Guard{},
		StateHash: stateHash(u.ID, u.State),
	}, nil
}

func (h userInviteCancel) Apply(ctx context.Context, _ core.Env, in UsernameInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.CancelInvite(ctx, in.Username)
}

func (h userInviteCancel) Verify(ctx context.Context, env core.Env, in UsernameInput, _ auth.User) error {
	return userDelete(h).Verify(ctx, env, in, Empty{})
}

type userSetDisabled struct {
	m        module
	disabled bool
}

func (h userSetDisabled) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	_, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guards := []core.Guard{}
	var diff []core.DiffLine
	switch {
	case u.Disabled == h.disabled:
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("user %s is already %s; nothing changes", u.Username, disabledWord(h.disabled))}}
	case h.disabled:
		guard := core.Guard{ID: "not-last-admin", OK: !last, Message: u.Username + " is not the last active admin"}
		if last {
			guard.Message = u.Username + " is the last active admin; activate another admin first"
		}
		guards = append(guards, guard)
		diff = []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: enabled", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: disabled; its sessions, tokens and sign-ins are refused, and an unused enrollment link ends", u.Username)},
			{Op: " ", Text: "compensated by core.user.enable"},
		}
	default:
		diff = []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: disabled", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: enabled; its sessions and tokens work again", u.Username)},
			{Op: " ", Text: "compensated by core.user.disable"},
		}
	}
	return core.PlanOutcome{
		Diff: diff, Guards: guards,
		StateHash: stateHash(u.ID, u.State, strconv.FormatBool(u.Disabled), strconv.FormatBool(last)),
	}, nil
}

func disabledWord(disabled bool) string {
	if disabled {
		return "disabled"
	}
	return "enabled"
}

func (h userSetDisabled) Apply(ctx context.Context, _ core.Env, in UsernameInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.SetDisabled(ctx, in.Username, h.disabled)
}

func (h userSetDisabled) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ auth.User) error {
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	u, err := svc.GetUser(ctx, in.Username)
	if err != nil {
		return err
	}
	if u.Disabled != h.disabled {
		return fmt.Errorf("coreactions: user %s is not %s", in.Username, disabledWord(h.disabled))
	}
	return nil
}

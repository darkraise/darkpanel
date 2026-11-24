package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
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

// UserPasswordInput is the input of core.user.reset-password/v1.
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
	if last {
		return core.PlanOutcome{}, auth.LastAdminError(u.Username)
	}
	guard := core.Guard{ID: "not-last-admin", OK: true, Message: u.Username + " is not the last active admin"}
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
			Name: "core.user.invite", Version: 1, Class: core.ClassCompensable, Side: core.SideController, TypedConfirmation: true,
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
		return core.PlanOutcome{}, fieldRefusal("/grants", "an admin has every host; grants are for members")
	}
	names, err := auth.CheckGrants(ctx, env.DB.Read(), in.Grants)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := svc.CheckNewIdentity(ctx, in.Email, in.TailnetLogin); err != nil {
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
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{guard}, StateHash: stateHash(state...), Confirmation: in.Username}, nil
}

func (h userInvite) Apply(ctx context.Context, _ core.Env, in InviteInput) (Invitation, error) {
	svc, err := h.m.authService()
	if err != nil {
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
	u, err := svc.LinkHolder(ctx, in.Username)
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
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	_, err = svc.LinkHolder(ctx, in.Username)
	return err
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
	var diff []core.DiffLine
	switch {
	case u.Disabled == h.disabled:
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("user %s is already %s; nothing changes", u.Username, disabledWord(h.disabled))}}
	case h.disabled && last:
		return core.PlanOutcome{}, auth.LastAdminError(u.Username)
	case h.disabled:
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
		Diff: diff, Guards: []core.Guard{},
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

// fieldRefusal is a request.invalid_input refusal naming one field.
func fieldRefusal(path, message string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// SetRoleInput is the input of core.user.set-role/v1.
type SetRoleInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name."`
	Role     string `json:"role" jsonschema:"enum=admin,enum=member" jsonschema_description:"admin has every scope on every host; member has only what its grants give."`
}

// SetGrantsInput is the input of core.user.set-grants/v1.
type SetGrantsInput struct {
	Username string       `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The member's name."`
	Grants   []auth.Grant `json:"grants" jsonschema_description:"The member's grants, one per target, replacing every grant it has; empty leaves it no host."`
}

// IdentityInput is the input of core.user.set-identity/v1.
type IdentityInput struct {
	Username     string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name."`
	Email        string `json:"email" jsonschema:"maxLength=254" jsonschema_description:"The email an OIDC sign-in matches; empty clears it."`
	TailnetLogin string `json:"tailnetLogin" jsonschema:"maxLength=254" jsonschema_description:"The tailnet login a Tailscale sign-in matches; empty clears it."`
}

// Directory is the output of core.user.directory/v1.
type Directory struct {
	Users []auth.DirectoryUser `json:"users" jsonschema_description:"Every user, sorted by username."`
}

func (m module) roleActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[SetRoleInput, auth.DirectoryUser](core.ActionSpec{
			Name: "core.user.set-role", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.set-role",
			Summary: "Make a user an admin or a member, refusing to demote the last active admin; ends an unused enrollment link.",
		}, userSetRole{m}),
		core.DefineMutating[SetGrantsInput, auth.DirectoryUser](core.ActionSpec{
			Name: "core.user.set-grants", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.set-grants",
			Summary: "Replace a member's grants: viewer or operator on every host or on a host group.",
		}, userSetGrants{m}),
		core.DefineMutating[IdentityInput, auth.DirectoryUser](core.ActionSpec{
			Name: "core.user.set-identity", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			MinScope: core.ScopeAdmin, CompensatedBy: "core.user.set-identity",
			Summary: "Set the email and tailnet login a user's OIDC and Tailscale sign-ins match; a changed one ends that method's link.",
		}, userSetIdentity{m}),
		core.DefineRead[NoInput, Directory](core.ActionSpec{
			Name: "core.user.directory", Version: 1, Side: core.SideController, HostData: core.HostDataFleet,
			Summary: "List every user with its role, grants, sign-in identities and sign-in methods.",
		}, userDirectory{m}),
	}
}

// grantNames returns the name of each group grants target; a group that no
// longer exists is named by its id.
func grantNames(ctx context.Context, q *sql.DB, grants []auth.Grant) (map[string]string, error) {
	names := map[string]string{}
	for _, g := range grants {
		if g.Target == "*" {
			continue
		}
		var name string
		err := q.QueryRowContext(ctx, `SELECT name FROM core_host_groups WHERE id = ?`, g.Target).Scan(&name)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			name = g.Target
		case err != nil:
			return nil, fmt.Errorf("coreactions: read group names: %w", err)
		}
		names[g.Target] = name
	}
	return names, nil
}

// grantLines renders grants as diff lines with op, and their state for a
// plan's hash.
func grantLines(op string, grants []auth.Grant, names map[string]string) ([]core.DiffLine, []string) {
	var diff []core.DiffLine
	var state []string
	for _, g := range grants {
		diff = append(diff, core.DiffLine{Op: op, Text: "grant: " + grantText(g, names)})
		state = append(state, g.Target, g.Role, names[g.Target])
	}
	return diff, state
}

func sortedGrants(grants []auth.Grant) []auth.Grant {
	out := slices.Clone(grants)
	slices.SortFunc(out, func(a, b auth.Grant) int { return strings.Compare(a.Target, b.Target) })
	return out
}

type userSetRole struct{ m module }

func (h userSetRole) Plan(ctx context.Context, env core.Env, in SetRoleInput) (core.PlanOutcome, error) {
	svc, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	p, err := svc.Profile(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	names, err := grantNames(ctx, env.DB.Read(), p.Grants)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	grantDiff, grantState := grantLines(" ", p.Grants, names)
	state := append([]string{u.ID, u.Role, u.State, strconv.FormatBool(last)}, grantState...)
	if u.Role == in.Role {
		return core.PlanOutcome{
			Diff:   []core.DiffLine{{Op: " ", Text: fmt.Sprintf("user %s is already %s; nothing changes", u.Username, roleWord(u.Role))}},
			Guards: []core.Guard{}, StateHash: stateHash(state...),
		}, nil
	}
	if last {
		return core.PlanOutcome{}, auth.LastAdminError(u.Username)
	}
	diff := []core.DiffLine{{Op: "-", Text: fmt.Sprintf("user %s: %s", u.Username, u.Role)}}
	if in.Role == auth.RoleMember {
		diff = append(diff, core.DiffLine{Op: "+", Text: fmt.Sprintf("user %s: member, with only the access its grants give", u.Username)})
		if len(p.Grants) == 0 {
			grantDiff = []core.DiffLine{{Op: " ", Text: "no grants: the member sees no host until it is given some"}}
		}
	} else {
		diff = append(diff, core.DiffLine{Op: "+", Text: fmt.Sprintf("user %s: admin, with every scope on every host", u.Username)})
		if len(p.Grants) > 0 {
			grantDiff = append([]core.DiffLine{{Op: " ", Text: "its grants stay stored and apply again if it is made a member"}}, grantDiff...)
		}
	}
	diff = append(diff, grantDiff...)
	if _, err := svc.LinkHolder(ctx, in.Username); err == nil {
		diff = append(diff, core.DiffLine{Op: " ", Text: "its enrollment link ends; core.user.renew-invite gives a new one"})
		state = append(state, "invited")
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.user.set-role with role " + u.Role})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(state...)}, nil
}

func roleWord(role string) string {
	if role == auth.RoleAdmin {
		return "an admin"
	}
	return "a member"
}

func (h userSetRole) Apply(ctx context.Context, _ core.Env, in SetRoleInput) (auth.DirectoryUser, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.DirectoryUser{}, err
	}
	return svc.SetRole(ctx, in.Username, in.Role)
}

func (h userSetRole) Verify(ctx context.Context, _ core.Env, in SetRoleInput, _ auth.DirectoryUser) error {
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	u, err := svc.GetUser(ctx, in.Username)
	if err != nil {
		return err
	}
	if u.Role != in.Role {
		return fmt.Errorf("coreactions: user %s is %s, want %s", in.Username, u.Role, in.Role)
	}
	return nil
}

type userSetGrants struct{ m module }

func (h userSetGrants) Plan(ctx context.Context, env core.Env, in SetGrantsInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	p, err := svc.Profile(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if p.Role != auth.RoleMember {
		return core.PlanOutcome{}, fieldRefusal("/grants", "an admin has every host; grants are for members")
	}
	names, err := auth.CheckGrants(ctx, env.DB.Read(), in.Grants)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	oldNames, err := grantNames(ctx, env.DB.Read(), p.Grants)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	want := sortedGrants(in.Grants)
	var gone, added []auth.Grant
	for _, g := range p.Grants {
		if !slices.Contains(want, g) {
			gone = append(gone, g)
		}
	}
	for _, g := range want {
		if !slices.Contains(p.Grants, g) {
			added = append(added, g)
		}
	}
	goneDiff, oldState := grantLines("-", gone, oldNames)
	addedDiff, newState := grantLines("+", added, names)
	_, keptState := grantLines(" ", p.Grants, oldNames)
	diff := append(goneDiff, addedDiff...)
	switch {
	case len(diff) == 0:
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("user %s's grants are unchanged; nothing changes", p.Username)}}
	case len(want) == 0:
		diff = append(diff, core.DiffLine{Op: " ", Text: "no grants: the member sees no host until it is given some"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.user.set-grants with the previous grants"})
	state := append([]string{p.ID, p.Role}, keptState...)
	state = append(append(state, oldState...), newState...)
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(state...)}, nil
}

func (h userSetGrants) Apply(ctx context.Context, _ core.Env, in SetGrantsInput) (auth.DirectoryUser, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.DirectoryUser{}, err
	}
	return svc.SetGrants(ctx, in.Username, in.Grants)
}

func (h userSetGrants) Verify(ctx context.Context, _ core.Env, in SetGrantsInput, _ auth.DirectoryUser) error {
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	p, err := svc.Profile(ctx, in.Username)
	if err != nil {
		return err
	}
	if want := sortedGrants(in.Grants); !slices.Equal(p.Grants, want) {
		return fmt.Errorf("coreactions: user %s has grants %v, want %v", in.Username, p.Grants, want)
	}
	return nil
}

type userSetIdentity struct{ m module }

func identityWord(v string) string {
	if v == "" {
		return "none"
	}
	return v
}

func (h userSetIdentity) Plan(ctx context.Context, _ core.Env, in IdentityInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	p, err := svc.Profile(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := svc.CheckIdentity(ctx, in.Username, in.Email, in.TailnetLogin); err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	for _, f := range []struct {
		label, old, new, method, link string
		pinned                        bool
	}{
		{"email", p.Email, in.Email, "OIDC", "email", slices.Contains(p.SignIn, auth.SignInOIDC)},
		{"tailnet login", p.TailnetLogin, in.TailnetLogin, "Tailscale", "tailnet login", slices.Contains(p.SignIn, auth.SignInTailscale)},
	} {
		if f.old == f.new {
			continue
		}
		diff = append(diff,
			core.DiffLine{Op: "-", Text: fmt.Sprintf("user %s: %s %s", p.Username, f.label, identityWord(f.old))},
			core.DiffLine{Op: "+", Text: fmt.Sprintf("user %s: %s %s", p.Username, f.label, identityWord(f.new))})
		if f.pinned && !strings.EqualFold(f.old, f.new) {
			diff = append(diff, core.DiffLine{Op: " ", Text: fmt.Sprintf("its %s link ends; the next %s sign-in links the account by the new %s",
				f.method, f.method, f.link)})
		}
	}
	if len(diff) == 0 {
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("user %s's email and tailnet login are unchanged; nothing changes", p.Username)}}
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.user.set-identity with the previous email and tailnet login"})
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash: stateHash(append([]string{p.ID, p.Email, p.TailnetLogin}, p.SignIn...)...),
	}, nil
}

func (h userSetIdentity) Apply(ctx context.Context, _ core.Env, in IdentityInput) (auth.DirectoryUser, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.DirectoryUser{}, err
	}
	return svc.SetIdentity(ctx, in.Username, in.Email, in.TailnetLogin)
}

func (h userSetIdentity) Verify(ctx context.Context, _ core.Env, in IdentityInput, _ auth.DirectoryUser) error {
	svc, err := h.m.authService()
	if err != nil {
		return err
	}
	p, err := svc.Profile(ctx, in.Username)
	if err != nil {
		return err
	}
	if p.Email != in.Email || p.TailnetLogin != in.TailnetLogin {
		return fmt.Errorf("coreactions: user %s has email %q and tailnet login %q, want %q and %q",
			in.Username, p.Email, p.TailnetLogin, in.Email, in.TailnetLogin)
	}
	return nil
}

type userDirectory struct{ m module }

// Read refuses a principal without fleet access itself, as the API does,
// since the directory names every user's sign-in identities.
func (h userDirectory) Read(ctx context.Context, env core.Env, _ NoInput) (Directory, error) {
	if !env.Principal.HostAccess().Fleet {
		return Directory{}, errcode.New(errcode.Forbidden, "this request needs access to the whole fleet")
	}
	svc, err := h.m.authService()
	if err != nil {
		return Directory{}, err
	}
	users, err := svc.Directory(ctx)
	if err != nil {
		return Directory{}, err
	}
	return Directory{Users: users}, nil
}

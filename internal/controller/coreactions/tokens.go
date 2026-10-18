package coreactions

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TokenList is the output of core.token.list/v1.
type TokenList struct {
	Tokens []auth.TokenInfo `json:"tokens" jsonschema_description:"The calling user's tokens, newest first; revoked and expired tokens stay listed until they are purged."`
}

// TokenCreateInput is the input of core.token.create/v1.
type TokenCreateInput struct {
	Name          string     `json:"name" jsonschema:"minLength=1,maxLength=64" jsonschema_description:"A name to recognise the token by, 1 to 64 characters."`
	Scope         core.Scope `json:"scope" jsonschema:"enum=read,enum=operate,enum=admin" jsonschema_description:"What the token may do: read, operate or admin."`
	ExpiresInDays int        `json:"expiresInDays,omitempty" jsonschema:"minimum=1,maximum=365" jsonschema_description:"Days until the token expires, 1 to 365; 90 when omitted."`
}

// NewToken is the output of core.token.create/v1: the new token's description
// and the token itself, which only the apply that created it returns.
type NewToken struct {
	auth.TokenInfo
	Token string `json:"token" secret:"true" jsonschema_description:"The token, sent as Authorization: Bearer <token>; shown only once."`
}

// TokenRevokeInput is the input of core.token.revoke/v1.
type TokenRevokeInput struct {
	ID string `json:"id" jsonschema:"minLength=1" jsonschema_description:"Id of one of the calling user's tokens."`
}

func (m module) tokenActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, TokenList](core.ActionSpec{
			Name: "core.token.list", Version: 1, Side: core.SideController,
			Summary: "List the calling user's API tokens.",
		}, tokenList{m}),
		core.DefineMutating[TokenCreateInput, NewToken](core.ActionSpec{
			Name: "core.token.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: "core.token.revoke",
			Summary:       "Create an API token for the calling user; the token is shown only once.",
		}, tokenCreate{m}),
		core.DefineMutating[TokenRevokeInput, auth.TokenInfo](core.ActionSpec{
			Name: "core.token.revoke", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Revoke one of the calling user's API tokens; the confirmation phrase is the token id.",
		}, tokenRevoke{m}),
	}
}

func (m module) authService() (*auth.Service, error) {
	if m.d.Auth == nil {
		return nil, errUnset("Deps.Auth")
	}
	return m.d.Auth, nil
}

// tokenOwner returns the service and the calling user's id. Tokens belong to
// users, so a principal without one (a local or system actor) is refused.
func (m module) tokenOwner(p core.Principal) (*auth.Service, string, error) {
	svc, err := m.authService()
	if err != nil {
		return nil, "", err
	}
	if p.UserID == "" {
		return nil, "", errcode.New(errcode.Forbidden, "token actions need a user's session or token")
	}
	return svc, p.UserID, nil
}

type tokenList struct{ m module }

func (h tokenList) Read(ctx context.Context, env core.Env, _ NoInput) (TokenList, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return TokenList{}, err
	}
	tokens, err := svc.ListTokens(ctx, userID)
	if err != nil {
		return TokenList{}, err
	}
	return TokenList{Tokens: tokens}, nil
}

type tokenCreate struct{ m module }

func (h tokenCreate) Plan(ctx context.Context, env core.Env, in TokenCreateInput) (core.PlanOutcome, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var owner auth.User
	for _, u := range users {
		if u.ID == userID {
			owner = u
		}
	}
	if owner.State != auth.StateActive {
		return core.PlanOutcome{}, errcode.New(errcode.NotFound, "the calling user is not an active user")
	}
	days := in.ExpiresInDays
	if days == 0 {
		days = auth.DefaultTokenDays
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("token %q with scope %s for user %s, expiring in %d days", in.Name, in.Scope, owner.Username, days)},
			{Op: " ", Text: "compensated by core.token.revoke with the new token's id"},
		},
		Guards:    []core.Guard{},
		StateHash: stateHash(owner.ID, owner.State),
	}, nil
}

func (h tokenCreate) Apply(ctx context.Context, env core.Env, in TokenCreateInput) (NewToken, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return NewToken{}, err
	}
	info, token, err := svc.CreateToken(ctx, userID, in.Name, in.Scope, in.ExpiresInDays)
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{TokenInfo: info, Token: token}, nil
}

// Verify finds the new token by the id in out. Recovery passes no output and
// a principal without a user, so an interrupted create is reported as not
// verified; the operator lists the tokens and revokes an unexpected one.
func (h tokenCreate) Verify(ctx context.Context, env core.Env, _ TokenCreateInput, out NewToken) error {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return err
	}
	info, err := svc.GetToken(ctx, userID, out.ID)
	if err != nil {
		return err
	}
	if info.RevokedAt != nil {
		return fmt.Errorf("coreactions: token %s is revoked", info.ID)
	}
	return nil
}

type tokenRevoke struct{ m module }

func (h tokenRevoke) Plan(ctx context.Context, env core.Env, in TokenRevokeInput) (core.PlanOutcome, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	info, err := svc.GetToken(ctx, userID, in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("token %s (%q, scope %s)", info.ID, info.Name, info.Scope)
	state := "active"
	var diff []core.DiffLine
	if info.RevokedAt != nil {
		state = formatTime(*info.RevokedAt)
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("%s: already revoked at %s; nothing changes",
			subject, info.RevokedAt.UTC().Format(time.RFC3339))}}
	} else {
		diff = []core.DiffLine{
			{Op: "-", Text: subject + ": active"},
			{Op: "+", Text: subject + ": revoked; a request that sends it gets auth.required"},
		}
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(userID, info.ID, state),
		Confirmation: info.ID,
	}, nil
}

func (h tokenRevoke) Apply(ctx context.Context, env core.Env, in TokenRevokeInput) (auth.TokenInfo, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return auth.TokenInfo{}, err
	}
	return svc.RevokeToken(ctx, userID, in.ID)
}

// Verify reads the token from in. Recovery's principal has no user, so an
// interrupted revoke is reported as not verified.
func (h tokenRevoke) Verify(ctx context.Context, env core.Env, in TokenRevokeInput, _ auth.TokenInfo) error {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return err
	}
	info, err := svc.GetToken(ctx, userID, in.ID)
	if err != nil {
		return err
	}
	if info.RevokedAt == nil {
		return fmt.Errorf("coreactions: token %s is not revoked", info.ID)
	}
	return nil
}

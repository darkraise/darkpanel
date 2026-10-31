package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var accountIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)

const maxTokenBytes = 512

func (c cf) accountConnect() core.ActionDef {
	return core.DefineMutating[ConnectInput, AccountView](core.ActionSpec{
		Name: ID + ".account.connect", Summary: "Connect the Cloudflare account with an API token.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"cloudflare"},
	}, accountConnectHandler{c: c})
}

// accountConnectHandler stores the one account of the controller. Its one
// effect is the local account row, which the undo deletes once no provider
// cleanup is pending.
type accountConnectHandler struct{ c cf }

// checkConnect refuses an invalid connect input with one field error per
// problem. No message quotes an input value, since the token is one.
func checkConnect(in ConnectInput) error {
	var fields []errcode.FieldError
	if !accountIDPattern.MatchString(in.AccountID) {
		fields = append(fields, errcode.FieldError{Path: "/accountId", Message: "the account id is 32 lowercase hexadecimal digits"})
	}
	switch {
	case strings.TrimSpace(in.APIToken) == "":
		fields = append(fields, errcode.FieldError{Path: "/apiToken", Message: "the API token is required"})
	case len(in.APIToken) > maxTokenBytes:
		fields = append(fields, errcode.FieldError{Path: "/apiToken", Message: fmt.Sprintf("the API token is at most %d bytes", maxTokenBytes)})
	case strings.ContainsFunc(in.APIToken, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }):
		fields = append(fields, errcode.FieldError{Path: "/apiToken", Message: "the API token holds no space or control character"})
	}
	if len(fields) > 0 {
		return core.Definite(&errcode.Error{Code: errcode.InvalidInput, Detail: "the connect input is invalid", Fields: fields})
	}
	return nil
}

func accountExists() error {
	return core.Definite(errcode.New(errcode.InvalidInput, "a Cloudflare account is already connected"))
}

// verifyAndProbe checks that Cloudflare accepts the token and probes its
// read capabilities. It only reads from Cloudflare.
func (h accountConnectHandler) verifyAndProbe(ctx context.Context, env core.Env, in ConnectInput) ([]cloud.Capability, error) {
	a, err := h.c.newAPI(in.AccountID, []byte(in.APIToken), env.Clock)
	if err != nil {
		return nil, err
	}
	if err := a.VerifyToken(ctx); err != nil {
		return nil, err
	}
	return probeCapabilities(ctx, a), nil
}

func (h accountConnectHandler) Plan(ctx context.Context, env core.Env, in ConnectInput) (core.PlanOutcome, error) {
	if err := checkConnect(in); err != nil {
		return core.PlanOutcome{}, err
	}
	_, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if ok {
		return core.PlanOutcome{}, accountExists()
	}
	caps, err := h.verifyAndProbe(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "+", Text: "cloudflare account " + in.AccountID}}
	for _, c := range caps {
		text := "capability " + c.Name + ": " + c.State
		if c.Detail != "" {
			text += " (" + c.Detail + ")"
		}
		diff = append(diff, core.DiffLine{Op: "+", Text: text})
	}
	return core.PlanOutcome{Diff: diff, StateHash: "no account"}, nil
}

func (h accountConnectHandler) Apply(ctx context.Context, env core.Env, in ConnectInput) (AccountView, error) {
	if err := checkConnect(in); err != nil {
		return AccountView{}, err
	}
	caps, err := h.verifyAndProbe(ctx, env, in)
	if err != nil {
		return AccountView{}, err
	}
	ct, err := cloud.SealCredential(env.Sealer, accountSealTable, accountSealColumn, env.ChangeID, []byte(in.APIToken))
	if err != nil {
		return AccountView{}, fmt.Errorf("cloudflare: seal the API token: %w", err)
	}
	state := accountState(in.AccountID)
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: accountResourcePrefix + "?" + env.ChangeID,
		Recovery: &core.RecoveryID{Kind: "name", Value: env.ChangeID}, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")}, After: &core.StateDoc{State: state},
	}, nil)
	if err != nil {
		return AccountView{}, err
	}
	a := account{ID: env.ChangeID, AccountID: in.AccountID, TokenCT: ct, Capabilities: caps, ConnectedAt: env.Clock.Now().UTC()}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, ok, err := loadAccount(ctx, tx)
		if err != nil {
			return err
		}
		if ok {
			return accountExists()
		}
		return insertAccount(ctx, tx, a)
	})
	if err != nil {
		if serr := env.Effects.Skipped(ctx, seq); serr != nil {
			return AccountView{}, errors.Join(err, serr)
		}
		return AccountView{}, err
	}
	if err := env.Effects.Made(ctx, seq, accountResourcePrefix+env.ChangeID, core.StateDoc{State: state}); err != nil {
		return AccountView{}, err
	}
	return accountView(a), nil
}

func (h accountConnectHandler) Verify(ctx context.Context, env core.Env, in ConnectInput, _ AccountView) error {
	a, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok || a.AccountID != in.AccountID {
		return errors.New("cloudflare: the connected account is not stored")
	}
	return nil
}

// accountRowID is the id of the account row an effect names: the identity
// after the resource's colon, which is the connecting change id whether or
// not the creation was named yet.
func accountRowID(e core.Effect) (string, error) {
	id, ok := strings.CutPrefix(e.Resource, accountResourcePrefix)
	id = strings.TrimPrefix(id, "?")
	if !ok || id == "" {
		return "", fmt.Errorf("cloudflare: %q is not a Cloudflare account", e.Resource)
	}
	return id, nil
}

// Observe reads the account row the effect names. The row's id tells it
// apart from any other, so the observation is exclusive; the same reading
// serves an effect that is undoing or undone, whose undo leaves no row.
func (h accountConnectHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, err := accountRowID(e)
	if err != nil {
		return core.Observation{}, err
	}
	a, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok || a.ID != id {
		return core.Observation{Current: core.StateDoc{State: json.RawMessage("null")}, Exclusive: true}, nil
	}
	return core.Observation{
		Current: core.StateDoc{State: accountState(a.AccountID)}, Resource: accountResourcePrefix + a.ID, Matches: 1, Exclusive: true,
	}, nil
}

// Inverse deletes the account row unless provider cleanup is pending: the
// pending tunnels, rules and records can only be cleaned up with the stored
// token, so the refusal keeps the effect made.
func (h accountConnectHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, err := accountRowID(e)
	if err != nil {
		return core.InverseResult{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		pending, err := cleanupPending(ctx, tx)
		if err != nil {
			return err
		}
		if len(pending) > 0 {
			return core.Definite(errcode.New(errcode.CloudCleanupPending, cleanupDetail(pending)))
		}
		return deleteAccount(ctx, tx, id)
	})
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	authkeyResourcePrefix = "tailnet.key:"
	authkeyDefaultExpiry  = 86400
	authkeyMaxExpiry      = 7776000 // 90 days
)

func (t tn) authkeyCreate() core.ActionDef {
	return core.DefineMutating[AuthKeyInput, CreatedKey](core.ActionSpec{
		Name: ID + ".authkey.create", Summary: "Create a tagged auth key for devices to join the tailnet.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{ID},
	}, authkeyHandler{t: t})
}

// authkeyHandler creates one auth key named darkpanel-<change id>, recorded
// in tailnet_keys before the request so a sweep can find it whatever the
// answer; its undo deletes the key.
type authkeyHandler struct{ t tn }

// normalizeAuthKey refuses an auth key input the account cannot create and
// answers it with its tags sorted and de-duplicated and its expiry defaulted.
func normalizeAuthKey(in AuthKeyInput, allowed []string) (AuthKeyInput, error) {
	var fields []errcode.FieldError
	bad := func(path, msg string) { fields = append(fields, errcode.FieldError{Path: path, Message: msg}) }
	if len(in.Tags) == 0 {
		bad("/tags", "at least one tag is required")
	}
	for _, tag := range in.Tags {
		switch {
		case !accountTagPattern.MatchString(tag):
			bad("/tags", fmt.Sprintf("tag %q is not \"tag:\" followed by a letter, then letters, digits or '-'", tag))
		case !slices.Contains(allowed, tag):
			bad("/tags", fmt.Sprintf("tag %q is not among the tags the OAuth client may assign", tag))
		}
	}
	switch {
	case in.ExpirySeconds == 0:
		in.ExpirySeconds = authkeyDefaultExpiry
	case in.ExpirySeconds < 0 || in.ExpirySeconds > authkeyMaxExpiry:
		bad("/expirySeconds", fmt.Sprintf("expiry %d seconds is outside 1 to %d", in.ExpirySeconds, authkeyMaxExpiry))
	}
	if len(fields) > 0 {
		return AuthKeyInput{}, &errcode.Error{Code: errcode.InvalidInput, Detail: "the auth key input is invalid", Fields: fields}
	}
	in.Tags = slices.Compact(slices.Sorted(slices.Values(in.Tags)))
	return in, nil
}

func authkeyExpiry(seconds int) time.Duration { return time.Duration(seconds) * time.Second }

func (h authkeyHandler) Plan(ctx context.Context, env core.Env, in AuthKeyInput) (core.PlanOutcome, error) {
	_, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	in, err = normalizeAuthKey(in, acct.TagsAllowed)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "+", Text: fmt.Sprintf("auth key with tags %s; reusable %t, ephemeral %t, preauthorized %t; expires after %s",
			strings.Join(in.Tags, ", "), in.Reusable, in.Ephemeral, in.Preauthorized, authkeyExpiry(in.ExpirySeconds))},
		{Op: "+", Text: "description darkpanel-<change id>"},
		{Op: "+", Text: "the key is shown once, in the change's output"},
		{Op: "+", Text: "the key is recorded in tailnet_keys before it is requested"},
	}
	normalized, err := json.Marshal(in)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailnet: encode the auth key input: %w", err)
	}
	return core.PlanOutcome{Diff: diff, StateHash: accountHash(acct.ID, string(normalized))}, nil
}

func (h authkeyHandler) Apply(ctx context.Context, env core.Env, in AuthKeyInput) (CreatedKey, error) {
	a, acct, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return CreatedKey{}, core.Definite(err)
	}
	in, err = normalizeAuthKey(in, acct.TagsAllowed)
	if err != nil {
		return CreatedKey{}, core.Definite(err)
	}
	description := "darkpanel-" + env.ChangeID
	now := env.Clock.Now().UTC()
	row := keyRow{
		Description: description, Kind: "change", OwnerID: env.ChangeID, State: "requested",
		RequestedAt: now, UpdatedAt: now, ExpirySeconds: in.ExpirySeconds, Tags: in.Tags,
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return insertKey(ctx, tx, row) }); err != nil {
		return CreatedKey{}, err
	}
	// The outcome is recorded even when ctx ended during the call that
	// preceded it.
	record := context.WithoutCancel(ctx)
	setRow := func(state, keyID string) error {
		return env.DB.Write(record, func(tx *sql.Tx) error {
			return setKeyState(record, tx, description, state, keyID, env.Clock.Now().UTC())
		})
	}

	e := cloudEffect(core.EffectCreate, authkeyResourcePrefix+"?"+description, nil)
	e.Recovery = &core.RecoveryID{Kind: "marker", Value: description}
	e.Before = core.StateDoc{State: json.RawMessage("null")}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		// Nothing was sent, so the row names no key.
		if rerr := setRow("deleted", ""); rerr != nil {
			return CreatedKey{}, errors.Join(err, rerr)
		}
		return CreatedKey{}, err
	}

	ck, err := a.CreateKey(ctx, KeyRequest{
		Description: description, Tags: in.Tags, Reusable: in.Reusable, Ephemeral: in.Ephemeral,
		Preauthorized: in.Preauthorized, ExpirySeconds: in.ExpirySeconds,
	})
	if err != nil {
		if core.IsDefinite(err) {
			if serr := env.Effects.Skipped(record, seq); serr != nil {
				return CreatedKey{}, errors.Join(err, fmt.Errorf("tailnet: journal %s: %w", e.Resource, serr))
			}
			if rerr := setRow("deleted", ""); rerr != nil {
				return CreatedKey{}, errors.Join(err, rerr)
			}
			return CreatedKey{}, err
		}
		// The key may exist: the effect stays intended for the engine to
		// settle, and the row waits for the sweep or the resolution.
		if rerr := setRow("uncertain", ""); rerr != nil {
			return CreatedKey{}, errors.Join(err, rerr)
		}
		return CreatedKey{}, err
	}
	// The row is written before the journal: if either write fails the
	// effect stays intended, and its resolution repairs the row.
	if err := setRow("live", ck.ID); err != nil {
		return CreatedKey{}, err
	}
	post := core.StateDoc{State: authkeyState(ck.ID, description)}
	if err := env.Effects.Made(record, seq, authkeyResourcePrefix+ck.ID, post); err != nil {
		return CreatedKey{}, err
	}
	return CreatedKey{ID: ck.ID, Key: ck.Key, Expires: ck.Expires, Description: description}, nil
}

func (h authkeyHandler) Verify(ctx context.Context, env core.Env, _ AuthKeyInput, out CreatedKey) error {
	if out.ID == "" {
		return nil
	}
	a, _, err := h.t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return err
	}
	k, err := a.Key(ctx, out.ID)
	if err != nil {
		return err
	}
	if k.Revoked {
		return fmt.Errorf("tailnet: auth key %s is revoked", out.ID)
	}
	return nil
}

func (t tn) authkeyList() core.ActionDef {
	return core.DefineRead[AuthKeyListInput, AuthKeyList](core.ActionSpec{
		Name: ID + ".authkey.list", Summary: "List the auth keys Darkpanel requested.", Version: 1, Side: core.SideController,
	}, authkeyLister{})
}

// authkeyLister reads the tailnet_keys rows; it calls no provider and needs
// no account.
type authkeyLister struct{}

func (authkeyLister) Read(ctx context.Context, env core.Env, _ AuthKeyListInput) (AuthKeyList, error) {
	rows, err := listKeys(ctx, env.DB.Read())
	if err != nil {
		return AuthKeyList{}, err
	}
	out := make([]AuthKeyRow, 0, len(rows))
	for _, r := range rows {
		out = append(out, AuthKeyRow{
			Description: r.Description, Kind: r.Kind, OwnerID: r.OwnerID, KeyID: r.KeyID, State: r.State,
			RequestedAt: r.RequestedAt, Expires: r.RequestedAt.Add(authkeyExpiry(r.ExpirySeconds)),
		})
	}
	return AuthKeyList{Keys: out}, nil
}

// authkeyState is the state document of a live auth key.
func authkeyState(id, description string) json.RawMessage {
	// A struct of two strings always marshals.
	b, _ := json.Marshal(struct {
		ID          string `json:"id"`
		Description string `json:"description"`
	}{id, description})
	return b
}

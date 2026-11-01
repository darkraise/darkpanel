package tailnet

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const (
	enrollKeyPrefix = "darkpanel-enroll-"
	// enrollKeySeconds is also the enrollment token's life, so the key
	// expires with the token it travels beside.
	enrollKeySeconds = 3600
)

// enrollHostID is a host id the key sweep recognises in a description: a
// ULID.
var enrollHostID = regexp.MustCompile(`^[0-9A-HJKMNP-TV-Z]{26}$`)

// PlanJoin checks that the connected account can create req's enroll key,
// writing nothing and calling no provider.
func (m module) PlanJoin(ctx context.Context, env core.Env, req core.JoinRequest) ([]core.DiffLine, string, error) {
	return m.t.planJoin(ctx, env, req)
}

// PrepareJoin creates the one-use enroll key of req.HostID and answers its
// secret.
func (m module) PrepareJoin(ctx context.Context, env core.Env, req core.JoinRequest) (string, error) {
	return m.t.prepareJoin(ctx, env, req)
}

// enrollKey loads the account and answers req's tags sorted and
// de-duplicated, refusing tags the account cannot assign.
func (t tn) enrollKey(ctx context.Context, env core.Env, req core.JoinRequest) (*api, account, []string, error) {
	a, acct, err := t.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return nil, account{}, nil, err
	}
	in, err := normalizeAuthKey(AuthKeyInput{Tags: req.Tags, Preauthorized: true, ExpirySeconds: enrollKeySeconds}, acct.TagsAllowed)
	if err != nil {
		return nil, account{}, nil, err
	}
	return a, acct, in.Tags, nil
}

func (t tn) planJoin(ctx context.Context, env core.Env, req core.JoinRequest) ([]core.DiffLine, string, error) {
	_, acct, tags, err := t.enrollKey(ctx, env, req)
	if err != nil {
		return nil, "", err
	}
	host := req.HostID
	if host == "" {
		host = "<host id>"
	}
	diff := []core.DiffLine{
		{Op: "+", Text: fmt.Sprintf("tailnet auth key with tags %s; one use, preauthorized, not ephemeral; expires after %s",
			strings.Join(tags, ", "), authkeyExpiry(enrollKeySeconds))},
		{Op: "+", Text: "description " + enrollKeyPrefix + host},
		{Op: "+", Text: "the key is recorded in tailnet_keys before it is requested"},
	}
	return diff, accountHash(acct.ID, strings.Join(tags, ",")), nil
}

func (t tn) prepareJoin(ctx context.Context, env core.Env, req core.JoinRequest) (string, error) {
	if !enrollHostID.MatchString(req.HostID) {
		return "", core.Definite(fmt.Errorf("tailnet: enroll key host id %q is not a ULID", req.HostID))
	}
	a, _, tags, err := t.enrollKey(ctx, env, req)
	if err != nil {
		return "", core.Definite(err)
	}
	description := enrollKeyPrefix + req.HostID
	now := env.Clock.Now().UTC()
	row := keyRow{
		Description: description, Kind: "enroll", OwnerID: req.HostID, State: "requested",
		RequestedAt: now, UpdatedAt: now, ExpirySeconds: enrollKeySeconds, Tags: tags,
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return insertKey(ctx, tx, row) }); err != nil {
		return "", err
	}
	// The outcome is recorded even when ctx ended during the call that
	// preceded it.
	record := context.WithoutCancel(ctx)
	setRow := func(state, keyID string) error {
		return env.DB.Write(record, func(tx *sql.Tx) error {
			return settleKey(record, tx, description, state, keyID, env.Clock.Now().UTC())
		})
	}

	ck, err := a.CreateKey(ctx, KeyRequest{
		Description: description, Tags: tags, Reusable: false, Ephemeral: false,
		Preauthorized: true, ExpirySeconds: enrollKeySeconds,
	})
	if err == nil && (ck.ID == "" || ck.Key == "") {
		err = errcode.New(errcode.CloudUncertainOutcome, "Tailscale answered a created auth key without its id or secret; the key may still exist")
	}
	if err != nil {
		// A refusal created nothing; any other failure may have, and the
		// sweep deletes that key by its description.
		state := "uncertain"
		if core.IsDefinite(err) {
			state = "deleted"
		}
		if rerr := setRow(state, ""); rerr != nil {
			return "", errors.Join(err, rerr)
		}
		return "", err
	}
	if err := setRow("live", ck.ID); err != nil {
		return "", err
	}
	return ck.Key, nil
}

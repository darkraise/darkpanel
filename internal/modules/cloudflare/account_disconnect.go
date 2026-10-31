package cloudflare

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (c cf) accountDisconnect() core.ActionDef {
	return core.DefineMutating[DisconnectInput, Disconnected](core.ActionSpec{
		Name: ID + ".account.disconnect", Summary: "Disconnect the Cloudflare account and delete the stored token.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideController, LockKeys: []string{"cloudflare"},
	}, accountDisconnectHandler{})
}

// accountDisconnectHandler deletes the account row. It leaves the tunnel and
// hostname rows alone, so a later reconnect finds what is left to clean up.
type accountDisconnectHandler struct{}

func accountNotConnected() error {
	return core.Definite(errcode.New(errcode.CloudNotConnected, "no Cloudflare account is connected"))
}

func cleanupRefusal(pending []string) error {
	return core.Definite(errcode.New(errcode.CloudCleanupPending, cleanupDetail(pending)))
}

// disconnectHash is the hex SHA-256 of the account id and the pending items,
// each ended by a newline.
func disconnectHash(accountID string, pending []string) string {
	h := sha256.New()
	for _, l := range append([]string{accountID}, pending...) {
		h.Write([]byte(l))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (accountDisconnectHandler) Plan(ctx context.Context, env core.Env, in DisconnectInput) (core.PlanOutcome, error) {
	var (
		a       account
		pending []string
	)
	// cleanupPending reads through a transaction; this one writes nothing.
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		var (
			ok  bool
			err error
		)
		if a, ok, err = loadAccount(ctx, tx); err != nil {
			return err
		}
		if !ok {
			return accountNotConnected()
		}
		pending, err = cleanupPending(ctx, tx)
		return err
	})
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if len(pending) > 0 && !in.AbandonCleanup {
		return core.PlanOutcome{}, cleanupRefusal(pending)
	}
	diff := []core.DiffLine{{Op: "-", Text: "cloudflare account " + a.AccountID}}
	if in.AbandonCleanup {
		for _, p := range pending {
			diff = append(diff, core.DiffLine{Op: "-", Text: "abandon: " + p})
		}
	}
	return core.PlanOutcome{Diff: diff, StateHash: disconnectHash(a.AccountID, pending), Confirmation: a.AccountID}, nil
}

func (accountDisconnectHandler) Apply(ctx context.Context, env core.Env, in DisconnectInput) (Disconnected, error) {
	abandoned := []string{}
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		a, ok, err := loadAccount(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return accountNotConnected()
		}
		pending, err := cleanupPending(ctx, tx)
		if err != nil {
			return err
		}
		if len(pending) > 0 && !in.AbandonCleanup {
			return cleanupRefusal(pending)
		}
		if in.AbandonCleanup && len(pending) > 0 {
			abandoned = pending
		}
		return deleteAccount(ctx, tx, a.ID)
	})
	if err != nil {
		return Disconnected{}, err
	}
	return Disconnected{Abandoned: abandoned}, nil
}

func (accountDisconnectHandler) Verify(ctx context.Context, env core.Env, _ DisconnectInput, _ Disconnected) error {
	_, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if ok {
		return errors.New("cloudflare: an account is still stored")
	}
	return nil
}

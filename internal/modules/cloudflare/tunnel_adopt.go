package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare/ingress"
)

// adoptNoun is the resource noun of the stored configuration hash of a
// tunnel row, followed by the tunnel id.
const adoptNoun = "cloudflare.tunnelhash:"

// adoptDoc is the state document of a tunnel row's stored configuration
// hash and drift mark.
func adoptDoc(hash string, drift bool) json.RawMessage {
	// Marshalling a struct of a string and a boolean cannot fail.
	b, _ := json.Marshal(struct {
		Hash  string `json:"hash"`
		Drift bool   `json:"drift"`
	}{hash, drift})
	return b
}

// tunnelAdopt declares cloudflare.tunnel.adopt, which accepts a managed
// tunnel's current configuration at Cloudflare as the one Darkpanel expects.
func (c cf) tunnelAdopt() core.ActionDef {
	return core.DefineMutating[TunnelInput, TunnelRef](core.ActionSpec{
		Name: ID + ".tunnel.adopt", Summary: "Accept a Cloudflare tunnel's current configuration as expected.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{ID},
	}, tunnelAdopter{c})
}

// tunnelAdopter stores the hash of a tunnel's configuration and clears its
// drift mark. It only reads Cloudflare; its undo restores the previous hash
// and drift mark.
type tunnelAdopter struct{ c cf }

// inspect finds the managed tunnel the input names, by id or name, and
// hashes its configuration at Cloudflare.
func (h tunnelAdopter) inspect(ctx context.Context, env core.Env, in TunnelInput) (tunnelRow, string, error) {
	if in.Tunnel == "" {
		return tunnelRow{}, "", core.Definite(&errcode.Error{
			Code: errcode.InvalidInput, Detail: "the adopt input is invalid",
			Fields: []errcode.FieldError{{Path: "/tunnel", Message: "the tunnel id or name is required"}},
		})
	}
	row, found, err := loadTunnel(ctx, env.DB.Read(), in.Tunnel)
	if err != nil {
		return tunnelRow{}, "", err
	}
	if !found {
		return tunnelRow{}, "", core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))
	}
	a, _, err := h.c.connected(ctx, env.DB, env.Sealer, env.Clock)
	if err != nil {
		return tunnelRow{}, "", err
	}
	cfg, err := a.Configuration(ctx, row.ID)
	if err != nil {
		return tunnelRow{}, "", err
	}
	return row, ingress.Hash(cfg), nil
}

// Plan shows the stored configuration hash the adoption replaces, or that
// it already matches. It writes nothing.
func (h tunnelAdopter) Plan(ctx context.Context, env core.Env, in TunnelInput) (core.PlanOutcome, error) {
	row, hash, err := h.inspect(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	line := func(op, hash string) core.DiffLine {
		return core.DiffLine{Op: op, Text: "tunnel " + row.Name + ": stored configuration hash " + hash}
	}
	var diff []core.DiffLine
	switch row.ConfigHash {
	case hash:
		same := line(" ", hash)
		same.Text += " already matches Cloudflare"
		diff = []core.DiffLine{same}
	case "":
		diff = []core.DiffLine{line("+", hash)}
	default:
		diff = []core.DiffLine{line("-", row.ConfigHash), line("+", hash)}
	}
	return core.PlanOutcome{Diff: diff, StateHash: hash + ":" + row.ConfigHash}, nil
}

// Apply stores the configuration's hash and clears the drift mark, journaled
// as one local effect. A database error leaves the effect intended, for the
// engine to settle by observing the row.
func (h tunnelAdopter) Apply(ctx context.Context, env core.Env, in TunnelInput) (TunnelRef, error) {
	row, hash, err := h.inspect(ctx, env, in)
	if err != nil {
		return TunnelRef{}, err
	}
	after := core.StateDoc{State: adoptDoc(hash, false)}
	e := core.Effect{
		Kind: core.EffectModify, Resource: adoptNoun + row.ID, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: adoptDoc(row.ConfigHash, row.Drift)}, After: &after,
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return TunnelRef{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := setTunnelHash(ctx, tx, row.ID, hash); err != nil {
			return err
		}
		return setTunnelCheck(ctx, tx, row.ID, false, env.Clock.Now())
	})
	if err != nil {
		return TunnelRef{}, err
	}
	if err := env.Effects.Made(ctx, seq, e.Resource, after); err != nil {
		return TunnelRef{}, err
	}
	return TunnelRef{ID: row.ID, Name: row.Name}, nil
}

// Verify passes when the first made effect still observes its post-state.
// It only reads, so startup recovery can call it.
func (h tunnelAdopter) Verify(ctx context.Context, env core.Env, _ TunnelInput, _ TunnelRef) error {
	effs, err := env.Effects.List(ctx)
	if err != nil {
		return err
	}
	for _, e := range effs {
		if e.State != core.EffectMade {
			continue
		}
		obs, err := h.Observe(ctx, env, e)
		if err != nil {
			return err
		}
		if e.Post == nil || !bytes.Equal(obs.Current.State, e.Post.State) {
			return fmt.Errorf("cloudflare: %s is not in the state the adoption left", e.Resource)
		}
		return nil
	}
	return errors.New("cloudflare: the adoption made no change")
}

// Observe reads the stored hash and drift mark of the tunnel row the
// effect names; the state is null when the row is gone.
func (h tunnelAdopter) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	id, ok := strings.CutPrefix(e.Resource, adoptNoun)
	if !ok || id == "" {
		return core.Observation{}, fmt.Errorf("cloudflare: %q is not a tunnel hash effect", e.Resource)
	}
	row, found, err := loadTunnel(ctx, env.DB.Read(), id)
	if err != nil {
		return core.Observation{}, err
	}
	state := json.RawMessage("null")
	// loadTunnel also matches a name; only the row with this id is the
	// effect's.
	if found && row.ID == id {
		state = adoptDoc(row.ConfigHash, row.Drift)
	}
	return core.Observation{Current: core.StateDoc{State: state}, Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

// Inverse restores the hash and drift mark the effect's before-state
// records. The row's presence is checked in the same transaction as the
// restore, so a concurrent delete cannot slip between them.
func (h tunnelAdopter) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	id, ok := strings.CutPrefix(e.Resource, adoptNoun)
	if !ok || id == "" {
		return core.InverseResult{}, fmt.Errorf("cloudflare: %q is not a tunnel hash effect", e.Resource)
	}
	var before struct {
		Hash  string `json:"hash"`
		Drift bool   `json:"drift"`
	}
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, fmt.Errorf("cloudflare: read the state before %s: %w", e.Resource, err)
	}
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, found, err := loadTunnel(ctx, tx, id)
		if err != nil {
			return err
		}
		if !found || row.ID != id {
			return core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+id))
		}
		if err := setTunnelHash(ctx, tx, id, before.Hash); err != nil {
			return err
		}
		return setTunnelCheck(ctx, tx, id, before.Drift, env.Clock.Now())
	})
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

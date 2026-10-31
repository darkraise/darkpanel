package tailnet

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/cloud"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func (t tn) accountConnect() core.ActionDef {
	return core.DefineMutating[ConnectInput, AccountView](core.ActionSpec{
		Name: ID + ".account.connect", Summary: "Connect the tailnet with an OAuth client.", Version: 1,
		Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"tailnet"},
	}, accountConnectHandler{t: t})
}

// accountConnectHandler stores the one OAuth client of the controller. Its
// one effect is the local account row, which the undo deletes once no
// provider cleanup is pending.
type accountConnectHandler struct{ t tn }

func accountConflict() error {
	return core.Definite(errcode.New(errcode.CloudConflict, "a tailnet is already connected: disconnect it first"))
}

// refuseSecondAccount refuses a connect while an account row exists.
func refuseSecondAccount(ctx context.Context, q queryer) error {
	_, ok, err := loadAccount(ctx, q)
	switch {
	case err != nil:
		return err
	case ok:
		return accountConflict()
	}
	return nil
}

func (h accountConnectHandler) Plan(ctx context.Context, env core.Env, in ConnectInput) (core.PlanOutcome, error) {
	in, err := checkConnect(in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := refuseSecondAccount(ctx, env.DB.Read()); err != nil {
		return core.PlanOutcome{}, err
	}
	caps, err := h.t.probeAccount(ctx, env, in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "+", Text: "connect tailnet " + in.Tailnet + " with OAuth client " + in.ClientID},
		{Op: "+", Text: "tags the client may assign: " + strings.Join(in.TagsAllowed, ", ")},
	}
	for _, c := range caps {
		text := "capability " + c.Name + ": " + c.State
		if c.Detail != "" {
			text += " (" + c.Detail + ")"
		}
		diff = append(diff, core.DiffLine{Op: " ", Text: text})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "undo deletes the stored credentials once no provider cleanup is pending"})
	// The capabilities stay out of the hash: a second probe can answer
	// differently without the connection changing.
	hash := accountHash("tailnet.account", in.Tailnet, in.ClientID, strings.Join(in.TagsAllowed, ","), "absent")
	return core.PlanOutcome{Diff: diff, StateHash: hash}, nil
}

func (h accountConnectHandler) Apply(ctx context.Context, env core.Env, in ConnectInput) (AccountView, error) {
	in, err := checkConnect(in)
	if err != nil {
		return AccountView{}, err
	}
	if err := refuseSecondAccount(ctx, env.DB.Read()); err != nil {
		return AccountView{}, err
	}
	caps, err := h.t.probeAccount(ctx, env, in)
	if err != nil {
		return AccountView{}, err
	}
	ct, err := cloud.SealCredential(env.Sealer, accountSecretTable, accountSecretColumn, env.ChangeID, []byte(in.ClientSecret))
	if err != nil {
		return AccountView{}, fmt.Errorf("tailnet: seal the client secret: %w", err)
	}
	state, err := accountState(env.ChangeID, in.Tailnet, in.ClientID, in.TagsAllowed)
	if err != nil {
		return AccountView{}, err
	}
	a := account{
		ID: env.ChangeID, Tailnet: in.Tailnet, ClientID: in.ClientID, SecretCT: ct,
		TagsAllowed: in.TagsAllowed, Capabilities: caps, ConnectedAt: env.Clock.Now().UTC(),
	}
	seq, err := env.Effects.Intend(ctx, core.Effect{
		Kind: core.EffectCreate, Resource: accountResource("?" + env.ChangeID),
		Recovery: &core.RecoveryID{Kind: "name", Value: env.ChangeID}, Local: true, InverseLocal: true,
		Before: core.StateDoc{State: json.RawMessage("null")}, After: &core.StateDoc{State: state},
	}, nil)
	if err != nil {
		return AccountView{}, err
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := refuseSecondAccount(ctx, tx); err != nil {
			return err
		}
		return insertAccount(ctx, tx, a)
	})
	if err != nil {
		return AccountView{}, err
	}
	if err := env.Effects.Made(ctx, seq, accountResource(env.ChangeID), core.StateDoc{State: state}); err != nil {
		return AccountView{}, err
	}
	return accountView(a), nil
}

func (h accountConnectHandler) Verify(ctx context.Context, env core.Env, _ ConnectInput, _ AccountView) error {
	a, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok || a.ID != env.ChangeID {
		return fmt.Errorf("tailnet: change %s stored no account", env.ChangeID)
	}
	return nil
}

// accountRowID is the id of the account row an effect names: the recovery
// value while the creation is unnamed, else the resource's identity.
func accountRowID(e core.Effect) (string, error) {
	id, ok := strings.CutPrefix(e.Resource, accountResourcePrefix)
	if ok && strings.HasPrefix(id, "?") && e.Recovery != nil {
		id = e.Recovery.Value
	}
	if !ok || id == "" || strings.HasPrefix(id, "?") {
		return "", fmt.Errorf("tailnet: %q is not a tailnet account", e.Resource)
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
	state, err := accountState(a.ID, a.Tailnet, a.ClientID, a.TagsAllowed)
	if err != nil {
		return core.Observation{}, err
	}
	return core.Observation{Current: core.StateDoc{State: state}, Resource: accountResource(a.ID), Matches: 1, Exclusive: true}, nil
}

// Inverse deletes the account row unless provider cleanup is pending: the
// pending keys and effects can only be cleaned up with the stored
// credentials, so the refusal keeps the effect made.
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
			return core.Definite(errcode.New(errcode.CloudCleanupPending, accountCleanupDetail(pending)))
		}
		return deleteAccount(ctx, tx, id)
	})
	if err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

func (t tn) accountDisconnect() core.ActionDef {
	return core.DefineMutating[DisconnectInput, Disconnected](core.ActionSpec{
		Name: ID + ".account.disconnect", Summary: "Disconnect the tailnet and delete the stored credentials.", Version: 1,
		Class: core.ClassIrreversible, Side: core.SideController, LockKeys: []string{"tailnet"},
	}, accountDisconnectHandler{t: t})
}

// accountDisconnectHandler deletes the account row. It leaves the tailnet
// and the tailnet_keys rows alone, so a later reconnect finds the keys to
// clean up.
type accountDisconnectHandler struct{ t tn }

func accountNotConnected() error {
	return core.Definite(errcode.New(errcode.CloudNotConnected, "no tailnet is connected"))
}

func accountCleanupRefusal(pending []string) error {
	return core.Definite(errcode.New(errcode.CloudCleanupPending,
		accountCleanupDetail(pending)+"; wait for it to finish or disconnect with abandonCleanup"))
}

func (h accountDisconnectHandler) Plan(ctx context.Context, env core.Env, in DisconnectInput) (core.PlanOutcome, error) {
	tx, err := env.DB.Read().BeginTx(ctx, nil)
	if err != nil {
		return core.PlanOutcome{}, fmt.Errorf("tailnet: begin a read: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	a, ok, err := loadAccount(ctx, tx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if !ok {
		return core.PlanOutcome{}, accountNotConnected()
	}
	pending, err := cleanupPending(ctx, tx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if len(pending) > 0 && !in.AbandonCleanup {
		return core.PlanOutcome{}, accountCleanupRefusal(pending)
	}
	diff := []core.DiffLine{
		{Op: "-", Text: "disconnect tailnet " + a.Tailnet + " (OAuth client " + a.ClientID + "): the stored credentials are deleted"},
		{Op: " ", Text: "keys and devices in the tailnet stay as they are"},
	}
	if in.AbandonCleanup {
		for _, l := range pending {
			diff = append(diff, core.DiffLine{Op: "-", Text: "abandons pending cleanup: " + l})
		}
	}
	hash := accountHash(append([]string{a.ID, strconv.FormatBool(in.AbandonCleanup)}, pending...)...)
	return core.PlanOutcome{Diff: diff, StateHash: hash, Confirmation: a.Tailnet}, nil
}

func (h accountDisconnectHandler) Apply(ctx context.Context, env core.Env, in DisconnectInput) (Disconnected, error) {
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
			return accountCleanupRefusal(pending)
		}
		if in.AbandonCleanup {
			abandoned = pending
		}
		return deleteAccount(ctx, tx, a.ID)
	})
	if err != nil {
		return Disconnected{}, err
	}
	return Disconnected{Abandoned: abandoned}, nil
}

func (h accountDisconnectHandler) Verify(ctx context.Context, env core.Env, _ DisconnectInput, _ Disconnected) error {
	_, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if ok {
		return errors.New("tailnet: an account is still stored")
	}
	return nil
}

func (t tn) accountGet() core.ActionDef {
	return core.DefineRead[AccountGetInput, AccountStatus](core.ActionSpec{
		Name: ID + ".account.get", Summary: "Show the connected tailnet account.", Version: 1, Side: core.SideController,
	}, accountGetHandler{t: t})
}

// accountGetHandler reads the stored account; it calls no provider.
type accountGetHandler struct{ t tn }

func (h accountGetHandler) Read(ctx context.Context, env core.Env, _ AccountGetInput) (AccountStatus, error) {
	a, ok, err := loadAccount(ctx, env.DB.Read())
	if err != nil {
		return AccountStatus{}, err
	}
	if !ok {
		return AccountStatus{Connected: false}, nil
	}
	v := accountView(a)
	return AccountStatus{Connected: true, Account: &v}, nil
}

package npm

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// instanceConfirmPublic is npm.instance.confirm-public: the operator's statement that ports 80 and 443 of this host
// are reachable from the internet. It marks the instance row and calls neither the Engine nor NPM; its undo clears
// the mark.
func (n nm) instanceConfirmPublic() core.ActionDef {
	return core.DefineMutating[ConfirmPublicInput, PublicMark](core.ActionSpec{
		Name:     "npm.instance.confirm-public",
		Summary:  "Record that ports 80 and 443 of this host are reachable from the internet.",
		Version:  1,
		Class:    core.ClassCompensable,
		Side:     core.SideAgent,
		LockKeys: []string{lockKey},
	}, confirmPublicHandler{n: n})
}

// confirmPublicHandler is the handler of npm.instance.confirm-public. The instance row is the authority on the mark,
// so its one effect is Local both ways and the engine settles an interrupted one by observing the row.
type confirmPublicHandler struct{ n nm }

// precheck returns the instance row, refusing a host with no instance or one already marked.
func (h confirmPublicHandler) precheck(ctx context.Context, env core.Env) (instanceRow, error) {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return instanceRow{}, err
	}
	if !ok {
		return instanceRow{}, core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
	}
	if row.Public == "confirmed" {
		return instanceRow{}, core.Definite(errcode.New(errcode.InvalidInput, "this host is already confirmed public"))
	}
	return row, nil
}

// Plan shows the mark changing. The hash covers the instance, so a plan made for one instance does not apply to
// another that replaced it.
func (h confirmPublicHandler) Plan(ctx context.Context, env core.Env, _ ConfirmPublicInput) (core.PlanOutcome, error) {
	row, err := h.precheck(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	sum := sha256.Sum256([]byte(row.ContainerID + "\n" + row.Public))
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "-", Text: "public: no"}, {Op: "+", Text: "public: confirmed"}},
		StateHash: hex.EncodeToString(sum[:]),
	}, nil
}

// Apply marks the instance public.
func (h confirmPublicHandler) Apply(ctx context.Context, env core.Env, _ ConfirmPublicInput) (PublicMark, error) {
	if _, err := h.precheck(ctx, env); err != nil {
		return PublicMark{}, err
	}
	before, err := json.Marshal(PublicMark{Public: "no"})
	if err != nil {
		return PublicMark{}, fmt.Errorf("npm: render the public mark: %w", err)
	}
	after, err := json.Marshal(PublicMark{Public: "confirmed"})
	if err != nil {
		return PublicMark{}, fmt.Errorf("npm: render the public mark: %w", err)
	}
	e := npmEffect(core.EffectModify, resourceOf(nounInstance, "public"), before, 0)
	e.Local, e.InverseLocal = true, true
	e.After = &core.StateDoc{State: after}
	err = h.n.run(ctx, env, e, h.Observe, func(ctx context.Context) (string, error) {
		return "", h.setMark(ctx, env, "confirmed")
	})
	if err != nil {
		return PublicMark{}, err
	}
	return PublicMark{Public: "confirmed"}, nil
}

// setMark writes mark to the instance row and changes no other column but its update time. No row is a Definite
// not_found that wrote nothing.
func (h confirmPublicHandler) setMark(ctx context.Context, env core.Env, mark string) error {
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		row, ok, err := loadInstance(ctx, tx)
		if err != nil {
			return err
		}
		if !ok {
			return core.Definite(errcode.New(errcode.NotFound, "no Nginx Proxy Manager instance is deployed on this host"))
		}
		row.Public, row.UpdatedAt = mark, env.Clock.Now()
		return saveInstance(ctx, tx, row)
	})
}

// Verify checks from the host alone that the instance is marked public.
func (h confirmPublicHandler) Verify(ctx context.Context, env core.Env, _ ConfirmPublicInput, _ PublicMark) error {
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return err
	}
	if !ok {
		return errors.New("npm: no Nginx Proxy Manager instance is recorded on this host")
	}
	if row.Public != "confirmed" {
		return fmt.Errorf("npm: the instance's public mark is %q, not confirmed", row.Public)
	}
	return nil
}

// Observe reads the mark from the instance row; no row is the null state.
func (h confirmPublicHandler) Observe(ctx context.Context, env core.Env, e core.Effect) (core.Observation, error) {
	if e.Resource != resourceOf(nounInstance, "public") {
		return core.Observation{}, fmt.Errorf("npm: %q is not the public mark", e.Resource)
	}
	row, ok, err := loadInstance(ctx, env.DB.Read())
	if err != nil {
		return core.Observation{}, err
	}
	if !ok {
		return named(nullState().State), nil
	}
	doc, err := json.Marshal(PublicMark{Public: row.Public})
	if err != nil {
		return core.Observation{}, fmt.Errorf("npm: render the public mark: %w", err)
	}
	return named(doc), nil
}

// Inverse restores the mark the effect found. A journal that holds no mark is refused before anything is written;
// restoring a mark already in place is harmless.
func (h confirmPublicHandler) Inverse(ctx context.Context, env core.Env, e core.Effect) (core.InverseResult, error) {
	var before PublicMark
	if err := json.Unmarshal(e.Before.State, &before); err != nil || (before.Public != "no" && before.Public != "confirmed") {
		return core.InverseResult{}, core.Definite(errcode.New(errcode.InvalidInput, "the journaled public mark is neither no nor confirmed"))
	}
	if err := h.setMark(ctx, env, before.Public); err != nil {
		return core.InverseResult{}, err
	}
	return core.InverseResult{}, nil
}

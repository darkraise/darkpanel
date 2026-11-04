package panel

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// listenerRevokeAction declares panel.listener.revoke: it revokes and closes the live exposure's listener and marks
// the exposure revoked. It is the first step of panel.exposure.remove and, run on its own, the break-glass command.
func listenerRevokeAction() core.ActionDef {
	return core.DefineMutating[ListenerRevokeInput, ListenerRevokeOutput](core.ActionSpec{
		Name:        ActionRevoke,
		Summary:     "Revoke the exposed panel's runtime listener, then close it.",
		Version:     1,
		Class:       core.ClassCompensable,
		Side:        core.SideController,
		LockKeys:    []string{"panel"},
		ForwardOnly: true,
		Converges:   true,
		BreakGlass:  true,
		MinScope:    core.ScopeAdmin,
	}, listenerRevokeHandler{})
}

// listenerRevokeHandler is the handler of panel.listener.revoke. It journals no effect: a run that is cut off is run
// again, and every run ends in the same state.
type listenerRevokeHandler struct{}

// listenerLive returns the live exposure and whether its listener is open.
func listenerLive(ctx context.Context, env core.Env) (row exposureRow, open bool, err error) {
	row, ok, err := liveExposure(ctx, env.DB.Read())
	if err != nil {
		return exposureRow{}, false, err
	}
	if !ok {
		return exposureRow{}, false, core.Definite(errcode.New(errcode.PanelNotExposed, "the panel is not exposed"))
	}
	info, err := env.Listeners.Get(ctx, row.ListenerID)
	if err != nil {
		return exposureRow{}, false, fmt.Errorf("panel: read the listener %s: %w", row.ListenerID, err)
	}
	return row, info.State == core.ListenerOpen, nil
}

// Plan shows what the revoke changes. A revoked exposure behind a listener that is no longer open plans nothing,
// which is how a removal runs after the break-glass command.
func (listenerRevokeHandler) Plan(ctx context.Context, env core.Env, _ ListenerRevokeInput) (core.PlanOutcome, error) {
	row, open, err := listenerLive(ctx, env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var diff []core.DiffLine
	if open {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: "runtime listener " + row.ListenerID + ": open"},
			core.DiffLine{Op: "+", Text: "runtime listener " + row.ListenerID + ": revoked, then closed"})
	}
	if row.State != StateRevoked {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: "exposure " + row.Hostname + ": " + row.State},
			core.DiffLine{Op: "+", Text: "exposure " + row.Hostname + ": " + StateRevoked})
	}
	return core.PlanOutcome{Diff: diff, StateHash: listenerHash(row.ID + "|" + row.State + "|" + row.ListenerID)}, nil
}

// Apply revokes and closes the listener on every run, so a listener an interrupted run revoked but did not close is
// closed now, then marks the exposure revoked by the removal that called it, if any.
func (listenerRevokeHandler) Apply(ctx context.Context, env core.Env, _ ListenerRevokeInput) (ListenerRevokeOutput, error) {
	row, open, err := listenerLive(ctx, env)
	if err != nil {
		return ListenerRevokeOutput{}, err
	}
	if err := env.Listeners.Revoke(ctx, row.ListenerID); err != nil {
		return ListenerRevokeOutput{}, fmt.Errorf("panel: revoke the listener %s: %w", row.ListenerID, err)
	}
	if err := env.Listeners.Close(ctx, row.ListenerID); err != nil {
		return ListenerRevokeOutput{}, fmt.Errorf("panel: close the listener %s: %w", row.ListenerID, err)
	}
	if err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		return setExposureState(ctx, tx, row.ID, StateRevoked, env.Call.ParentChangeID, env.Clock.Now())
	}); err != nil {
		return ListenerRevokeOutput{}, err
	}
	return ListenerRevokeOutput{ExposureID: row.ID, ListenerID: row.ListenerID, RuleID: row.RuleID, Revoked: open}, nil
}

// Verify passes once the live exposure is revoked and its listener is no longer open. It ignores the output.
func (listenerRevokeHandler) Verify(ctx context.Context, env core.Env, _ ListenerRevokeInput, _ ListenerRevokeOutput) error {
	row, open, err := listenerLive(ctx, env)
	if err != nil {
		return err
	}
	if row.State != StateRevoked {
		return fmt.Errorf("panel: exposure %s is %s, not revoked", row.ID, row.State)
	}
	if open {
		return fmt.Errorf("panel: the listener %s is still open", row.ListenerID)
	}
	return nil
}

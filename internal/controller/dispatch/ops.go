package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var _ core.DispatcherChangeOps = (*Dispatcher)(nil)

// changeSide finds where changeID lives: on the controller engine, with a
// nil client, or on the agent of hostID that core_change_index names or that
// holds it.
func (d *Dispatcher) changeSide(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	_, err := d.cfg.Engine.Change(ctx, changeID)
	switch {
	case err == nil:
		return "", nil, nil
	case !isChangeNotFound(err):
		return "", nil, err
	}
	return d.owner(ctx, changeID)
}

// changeSideInWindow is changeSide for undo, repair and resolve: an agent
// change is refused with host.version_unsupported while its agent is outside
// the support window, unless its action is a core.update action, as plan and
// apply are. Module enablement is not checked: disabling a module must not
// strand a pending or failed change.
func (d *Dispatcher) changeSideInWindow(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	hostID, c, err := d.changeSide(ctx, changeID)
	if err != nil || c == nil {
		return hostID, c, err
	}
	action, err := d.changeAction(ctx, hostID, changeID, c)
	if err != nil {
		return "", nil, err
	}
	if err := d.inWindow(hostID, action); err != nil {
		return "", nil, err
	}
	return hostID, c, nil
}

// changeAction is the action of hostID's change changeID: core_change_index's
// row, else the action of the change as the agent reports it, because the
// index write after an apply may have failed.
func (d *Dispatcher) changeAction(ctx context.Context, hostID, changeID string, c core.AgentClient) (string, error) {
	var action string
	err := d.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT action FROM core_change_index WHERE change_id = ? AND host_id = ?", changeID, hostID).Scan(&action)
	switch {
	case err == nil:
		return action, nil
	case !errors.Is(err, sql.ErrNoRows):
		return "", fmt.Errorf("dispatch: read change index: %w", err)
	}
	v, err := c.Change(ctx, changeID)
	if err != nil {
		return "", err
	}
	if v, err = checkChange(v, hostID, changeID); err != nil {
		return "", err
	}
	return v.Action, nil
}

// Undo undoes a change where it lives, then ingests its host's journal. The
// operator's undo carries no call context; only a composite's unwind does.
func (d *Dispatcher) Undo(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.ChangeView{}, err
	}
	hostID, c, err := d.changeSideInWindow(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if c == nil {
		return d.cfg.Engine.Undo(ctx, p, changeID, nil)
	}
	v, err := c.Undo(ctx, p, changeID, nil)
	if err != nil {
		return v, err
	}
	d.ingest(ctx, hostID)
	return checkChange(v, hostID, changeID)
}

// Repair repairs a change where it lives (spec 1.3). A plan it answers is
// indexed with the change it repairs, so the client applies it through the
// ordinary apply route. A step of a composite being released is refused:
// the release decides that step.
func (d *Dispatcher) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.RepairResult{}, err
	}
	if err := d.refuseReleasingParent(ctx, changeID); err != nil {
		return core.RepairResult{}, err
	}
	hostID, c, err := d.changeSideInWindow(ctx, changeID)
	if err != nil {
		return core.RepairResult{}, err
	}
	var res core.RepairResult
	if c == nil {
		res, err = d.cfg.Engine.Repair(ctx, p, changeID, req)
	} else {
		res, err = c.Repair(ctx, p, changeID, req)
	}
	if err != nil {
		return core.RepairResult{}, err
	}
	switch {
	case res.Change != nil && res.Plan == nil:
		if c == nil {
			return res, nil
		}
		d.ingest(ctx, hostID)
		v, err := checkChange(*res.Change, hostID, changeID)
		if err != nil {
			return core.RepairResult{}, err
		}
		res.Change = &v
		return res, nil
	case res.Plan != nil && res.Change == nil:
		if res.Plan.HostID != hostID {
			return core.RepairResult{}, fmt.Errorf("dispatch: repair of change %s on %q answered with a plan on %q",
				changeID, hostID, res.Plan.HostID)
		}
		if err := d.rememberRepair(ctx, hostID, *res.Plan, changeID); err != nil {
			return core.RepairResult{}, err
		}
		return res, nil
	}
	return core.RepairResult{}, fmt.Errorf("dispatch: repair of change %s answered neither a change nor a plan, or both", changeID)
}

// Release gives up a change where it lives, then ingests its host's journal.
func (d *Dispatcher) Release(ctx context.Context, p core.Principal, changeID, reason string) (core.ReleaseResult, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.ReleaseResult{}, err
	}
	hostID, c, err := d.changeSide(ctx, changeID)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	if c == nil {
		return d.cfg.Engine.Release(ctx, p, changeID, reason, nil)
	}
	res, err := c.Release(ctx, p, changeID, reason, nil)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	d.ingest(ctx, hostID)
	if res.Change, err = checkChange(res.Change, hostID, changeID); err != nil {
		return core.ReleaseResult{}, err
	}
	return res, nil
}

// Effects lists a change's effects where it lives.
func (d *Dispatcher) Effects(ctx context.Context, p core.Principal, changeID string) ([]core.EffectView, error) {
	_, c, err := d.changeSide(ctx, changeID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return d.cfg.Engine.Effects(ctx, changeID)
	}
	return c.Effects(ctx, p, changeID)
}

// Observe reads one effect's resource where its change lives.
func (d *Dispatcher) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	_, c, err := d.changeSide(ctx, changeID)
	if err != nil {
		return core.ObserveResult{}, err
	}
	if c == nil {
		return d.cfg.Engine.Observe(ctx, p, changeID, seq)
	}
	return c.Observe(ctx, p, changeID, seq)
}

// Resolve resolves an effect where its change lives, then ingests its
// host's journal.
func (d *Dispatcher) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	if err := d.admitMutation(ctx); err != nil {
		return core.EffectView{}, err
	}
	hostID, c, err := d.changeSideInWindow(ctx, changeID)
	if err != nil {
		return core.EffectView{}, err
	}
	if c == nil {
		return d.cfg.Engine.Resolve(ctx, p, changeID, seq, req)
	}
	v, err := c.Resolve(ctx, p, changeID, seq, req)
	if err != nil {
		return v, err
	}
	d.ingest(ctx, hostID)
	return v, nil
}

// Steps lists a composite's steps. Composites run on the controller, so an
// agent change has none.
func (d *Dispatcher) Steps(ctx context.Context, _ core.Principal, changeID string) ([]core.StepView, error) {
	_, c, err := d.changeSide(ctx, changeID)
	if err != nil {
		return nil, err
	}
	if c == nil {
		return d.cfg.Engine.Steps(ctx, changeID)
	}
	return []core.StepView{}, nil
}

// refuseReleasingParent answers core.change_busy when changeID is the child
// of a composite being released.
func (d *Dispatcher) refuseReleasingParent(ctx context.Context, changeID string) error {
	var parent string
	err := d.cfg.DB.Read().QueryRowContext(ctx, `SELECT s.change_id FROM core_composite_steps s
		JOIN core_changes c ON c.id = s.change_id
		WHERE s.child_change_id = ? AND c.state = ? LIMIT 1`, changeID, core.StateReleasing).Scan(&parent)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("dispatch: read composite steps: %w", err)
	}
	return errcode.New(errcode.ChangeBusy,
		fmt.Sprintf("change %s is a step of composite %s, which is being released", changeID, parent))
}

// refuseReleasingRepair refuses the apply of a repair plan whose target is a
// step of a composite being released. planFor has checked the plan's row.
func (d *Dispatcher) refuseReleasingRepair(ctx context.Context, planID string) error {
	var repairs sql.NullString
	if err := d.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT repairs FROM core_plan_index WHERE plan_id = ?", planID).Scan(&repairs); err != nil {
		return fmt.Errorf("dispatch: read plan index: %w", err)
	}
	if !repairs.Valid {
		return nil
	}
	return d.refuseReleasingParent(ctx, repairs.String)
}

// rememberRepair indexes a repair plan as remember indexes any plan, with the
// change it repairs.
func (d *Dispatcher) rememberRepair(ctx context.Context, hostID string, v core.PlanView, target string) error {
	return d.remember(ctx, hostID, core.ActionRef{Name: v.Action, Version: v.Version}, v, target)
}

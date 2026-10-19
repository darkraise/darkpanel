package dispatch

import (
	"context"
	"database/sql"
	"fmt"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Apply applies a plan this dispatcher made for exactly hostID and a. A plan
// id core_plan_index does not hold, and one made for another host, action or
// version, are plan.not_found (register row 69, obligation 1): the caller
// authorized a, so a plan of another action must not run. An agent apply also
// needs a's module to be usable on hostID; its change is recorded in
// core_change_index and the host's journal is ingested.
func (d *Dispatcher) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if err := d.planFor(ctx, planID, hostID, a); err != nil {
		return core.ChangeView{}, err
	}
	apply := func() (core.ChangeView, error) { return d.cfg.Engine.Apply(ctx, p, planID, opts) }
	if def.Spec.Side == core.SideAgent {
		c, err := d.agent(ctx, hostID, a.Name)
		if err != nil {
			return core.ChangeView{}, err
		}
		apply = func() (core.ChangeView, error) { return c.Apply(ctx, p, planID, opts) }
	}
	// Marked before the call: the change may be committed even when this
	// process stops before the call returns, and its plan must stay indexed
	// so a repeated apply returns that change.
	// Applies of one plan run one at a time: otherwise a failed apply that
	// claimed the flag could clear it after a concurrent apply of the same
	// plan committed (register row 90).
	v, err := func() (core.ChangeView, error) {
		unlock, err := d.plans.lock(ctx, planID)
		if err != nil {
			return core.ChangeView{}, err
		}
		defer unlock()
		claimed, err := d.markApplied(ctx, planID)
		if err != nil {
			return core.ChangeView{}, err
		}
		v, err := apply()
		if err != nil && v.ID == "" && claimed && noChange(err) {
			if err := d.unmarkApplied(context.WithoutCancel(ctx), planID); err != nil {
				d.cfg.Log.Error("dispatch: unmark plan", "plan", planID, "error", err)
			}
		}
		return v, err
	}()
	if v.ID != "" && def.Spec.Side == core.SideAgent {
		d.index(ctx, hostID, v)
		d.ingest(ctx, hostID)
	}
	return v, err
}

// planFor refuses planID unless core_plan_index holds it for hostID and a.
func (d *Dispatcher) planFor(ctx context.Context, planID, hostID string, a core.ActionRef) error {
	var found bool
	err := d.cfg.DB.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM core_plan_index
		 WHERE plan_id = ? AND host_id = ? AND action = ? AND version = ?)`,
		planID, hostID, a.Name, a.Version).Scan(&found)
	switch {
	case err != nil:
		return fmt.Errorf("dispatch: read plan index: %w", err)
	case found:
		return nil
	}
	detail := fmt.Sprintf("no plan %s for %s", planID, a)
	if hostID != "" {
		detail += " on host " + hostID
	}
	return errcode.New(errcode.PlanNotFound, detail)
}

// markApplied sets the applied flag of planID and reports whether this call
// set it. Only that call may clear it again: a refused repeat of a plan that
// already has a change, such as another principal's, must leave it set. An
// applied plan is kept in core_plan_index; an unapplied one is deleted after
// its retention.
func (d *Dispatcher) markApplied(ctx context.Context, planID string) (bool, error) {
	var claimed bool
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "UPDATE core_plan_index SET applied = 1 WHERE plan_id = ? AND applied = 0", planID)
		if err != nil {
			return err
		}
		n, err := res.RowsAffected()
		claimed = n == 1
		return err
	})
	if err != nil {
		return false, fmt.Errorf("dispatch: mark plan %s: %w", planID, err)
	}
	return claimed, nil
}

// noChange reports whether err shows that the apply recorded no change. A lost
// reply or an internal error may follow a committed change, and
// plan.principal_mismatch is checked before the plan's change is looked up, so
// another principal's concurrent apply may hold one.
func noChange(err error) bool {
	c := errcode.From(err).Code
	return c != errcode.Internal && c != errcode.PlanPrincipalMismatch
}

// unmarkApplied clears the applied flag that markApplied set.
func (d *Dispatcher) unmarkApplied(ctx context.Context, planID string) error {
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE core_plan_index SET applied = 0 WHERE plan_id = ?", planID)
		return err
	})
	if err != nil {
		return fmt.Errorf("dispatch: unmark plan %s: %w", planID, err)
	}
	return nil
}

// index records the host of an agent change, so Change, Confirm and Revert
// find it. A failure is logged, not returned: the change exists and its
// apply response may carry a secret output shown only once.
func (d *Dispatcher) index(ctx context.Context, hostID string, v core.ChangeView) {
	ctx = context.WithoutCancel(ctx)
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_change_index (change_id, host_id, action, version, created_at)
			 VALUES (?, ?, ?, ?, ?) ON CONFLICT (change_id) DO NOTHING`,
			v.ID, hostID, v.Action, v.Version, v.CreatedAt.UTC().Format(timeFormat))
		return err
	})
	if err != nil {
		d.cfg.Log.Error("dispatch: index change", "change", v.ID, "host", hostID, "error", err)
	}
}

// ingest copies hostID's journal into the audit log now; the ingester's loop
// retries what fails here.
func (d *Dispatcher) ingest(ctx context.Context, hostID string) {
	if d.cfg.Ingest == nil {
		return
	}
	if err := d.cfg.Ingest.IngestHost(context.WithoutCancel(ctx), hostID); err != nil {
		d.cfg.Log.Warn("dispatch: journal ingestion", "host", hostID, "error", err)
	}
}

// planLocks are per-plan locks of this process. One process is enough: the
// controller lock keeps the controller to one instance.
type planLocks struct {
	mu   sync.Mutex
	held map[string]*planLock
}

type planLock struct {
	slot  chan struct{} // holds a value while the plan is locked
	users int           // holders and waiters; guarded by planLocks.mu
}

// lock locks planID, waiting until it is free or ctx ends, and returns the
// function that unlocks it.
func (l *planLocks) lock(ctx context.Context, planID string) (func(), error) {
	l.mu.Lock()
	if l.held == nil {
		l.held = make(map[string]*planLock)
	}
	pl := l.held[planID]
	if pl == nil {
		pl = &planLock{slot: make(chan struct{}, 1)}
		l.held[planID] = pl
	}
	pl.users++
	l.mu.Unlock()
	release := func() {
		l.mu.Lock()
		if pl.users--; pl.users == 0 {
			delete(l.held, planID)
		}
		l.mu.Unlock()
	}
	select {
	case pl.slot <- struct{}{}:
		return func() {
			<-pl.slot
			release()
		}, nil
	case <-ctx.Done():
		release()
		return nil, ctx.Err()
	}
}

package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/update"
)

const (
	// PollInterval is how often the driver advances the active rollout and
	// polls the updating host's operation.
	PollInterval = 5 * time.Second
	// HostTimeout is how long the driver follows one host's update operation
	// before the rollout drains.
	HostTimeout = 10 * time.Minute
	// callTimeout bounds one dispatcher call.
	callTimeout = 30 * time.Second
	// passSteps bounds the transitions of one pass; a pass that needs more
	// continues at the next tick.
	passSteps = 16
)

// driverPrincipal is the driver's principal on every dispatcher call
// (ADR 0012: system:rollout).
var driverPrincipal = core.Principal{Actor: core.ActorRollout, Scope: core.ScopeAdmin}

var (
	applyRef  = core.ActionRef{Name: update.ActionApply, Version: 1}
	statusRef = core.ActionRef{Name: update.ActionStatus, Version: 1}
)

// Dispatcher is what the driver calls on the controller's dispatcher;
// *dispatch.Dispatcher implements it.
type Dispatcher interface {
	Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error)
	Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
}

// DriverConfig wires a Driver.
type DriverConfig struct {
	DB         *store.DB // the controller database
	Dispatcher Dispatcher
	Clock      core.Clock
	Log        *slog.Logger // nil discards
	// Suspended reports whether automatic changes are suspended (restore
	// quarantine, spec 10): a pass that finds it true does nothing, and an
	// error counts as true. nil is never suspended.
	Suspended func(ctx context.Context) (bool, error)
}

// Driver drives the active rollout from its rows, one host at a time (spec
// section 8, Rollout driver). Every transition is one controller write
// transaction that first checks the rollout is still as the driver read it,
// so a cancel committed in between wins and the driver reads it again. A
// restarted controller resumes from the rows.
type Driver struct {
	cfg    DriverConfig
	ctx    context.Context
	cancel context.CancelFunc

	// mu is held for a whole pass, so passes never overlap and Stop waits
	// for a running one.
	mu      sync.Mutex
	timer   core.Timer
	started bool
	stopped bool
	// stuckWarned is when follow last warned of a draining rollout whose
	// host's update outlived HostTimeout; guarded by mu.
	stuckWarned time.Time
}

// NewDriver returns a driver for cfg; DB, Dispatcher and Clock are required.
// Call Start to drive rollouts.
func NewDriver(cfg DriverConfig) (*Driver, error) {
	switch {
	case cfg.DB == nil:
		return nil, errors.New("rollout: DriverConfig.DB is required")
	case cfg.Dispatcher == nil:
		return nil, errors.New("rollout: DriverConfig.Dispatcher is required")
	case cfg.Clock == nil:
		return nil, errors.New("rollout: DriverConfig.Clock is required")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Driver{cfg: cfg, ctx: ctx, cancel: cancel}, nil
}

// Start drives the active rollout every PollInterval until Stop. A second
// Start, or a Start after Stop, does nothing.
func (d *Driver) Start() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started || d.stopped {
		return
	}
	d.started = true
	d.timer = d.cfg.Clock.AfterFunc(PollInterval, d.tick)
}

// Stop ends the loop: it cancels the calls of a running pass and waits for
// the pass to end. The rollout stays in its rows for the next start.
func (d *Driver) Stop() {
	d.cancel()
	d.mu.Lock()
	defer d.mu.Unlock()
	d.stopped = true
	if d.timer != nil {
		d.timer.Stop()
	}
}

func (d *Driver) tick() {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.stopped {
		return
	}
	if !suspended(d.ctx, d.cfg.Suspended, d.cfg.Log, "driver") {
		if err := d.pass(d.ctx); err != nil {
			d.cfg.Log.Warn("rollout: drive the active rollout", "error", err)
		}
	}
	d.timer = d.cfg.Clock.AfterFunc(PollInterval, d.tick)
}

// pass advances the active rollout until it has to wait.
func (d *Driver) pass(ctx context.Context) error {
	for range passSteps {
		r, ok, err := active(ctx, d.cfg.DB.Read())
		if err != nil || !ok {
			return err
		}
		more, err := d.step(ctx, r)
		if err != nil || !more {
			return err
		}
	}
	return nil
}

// step makes one transition of r and reports whether another may follow at
// once.
func (d *Driver) step(ctx context.Context, r record) (bool, error) {
	h := r.updating()
	switch {
	case h == nil && r.State == core.RolloutDraining:
		return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
			return setRollout(ctx, tx, r.ID, r.Outcome, r.Outcome, r.Error, now)
		})
	case h == nil:
		for _, w := range r.Hosts {
			if w.State == core.RolloutHostWaiting {
				return d.write(ctx, r, func(tx *sql.Tx, _ time.Time) error {
					return setRolloutHost(ctx, tx, r.ID, w.HostID, core.RolloutHostUpdating, nil)
				})
			}
		}
		return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
			return setRollout(ctx, tx, r.ID, core.RolloutSucceeded, "", nil, now)
		})
	case h.OperationID == nil:
		return d.handOff(ctx, r, h)
	default:
		return d.follow(ctx, r, h)
	}
}

// write runs fn in one controller write transaction when rollout r is still
// as the driver read it, and stamps the rollout's updated_at. It reports
// true, so the pass reads the rollout again at once, whether or not fn ran:
// a cancel committed since the read skips fn, and the next read sees it.
func (d *Driver) write(ctx context.Context, r record, fn func(tx *sql.Tx, now time.Time) error) (bool, error) {
	now := d.cfg.Clock.Now().UTC()
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var state, outcome, updated string
		if err := tx.QueryRowContext(ctx,
			"SELECT state, coalesce(outcome, ''), updated_at FROM core_rollouts WHERE id = ?", r.ID).
			Scan(&state, &outcome, &updated); err != nil {
			return fmt.Errorf("rollout: read rollout %s: %w", r.ID, err)
		}
		if state != r.State || outcome != r.Outcome || updated != r.UpdatedAt.UTC().Format(timeLayout) {
			return nil
		}
		if err := fn(tx, now); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE core_rollouts SET updated_at = ? WHERE id = ?", now.Format(timeLayout), r.ID)
		return err
	})
	if err != nil {
		return false, err
	}
	return true, nil
}

// notYet reports whether a dispatcher error only means the host cannot
// answer now: it is offline, another apply of the same plan is running, or
// the call failed in transport or in this process (an error that carries no
// *errcode.Error). errcode.From reports an uncoded error as internal too, but
// an agent that answers internal has answered, and would answer the same
// forever, so a coded internal fails the host.
func notYet(err error) bool {
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		return true
	}
	return coded.Code == errcode.HostOffline || coded.Code == errcode.LockHeld
}

// detailOf is the error a host records for a dispatcher refusal.
func detailOf(err error) *core.ErrorDetail {
	e := errcode.From(err)
	msg := e.Detail
	if msg == "" {
		msg = e.Code.Title()
	}
	return &core.ErrorDetail{Code: string(e.Code), Message: msg}
}

// handOff plans core.update.apply on h and applies the plan (spec section 8,
// Rollout driver, steps 1 and 2): the plan id is stored before the apply, so
// after a lost answer or a restart the same plan is applied again and the
// engine returns its one change. A change's update operation is stored for
// follow.
//
// apply_sent_at is committed between storing the plan and the first apply.
// A draining rollout applies only a plan that was already sent, to learn its
// outcome, and skips a host it never sent one, so a cancel that commits
// before the marker starts no update.
func (d *Driver) handOff(ctx context.Context, r record, h *hostRecord) (bool, error) {
	if r.State == core.RolloutDraining && !handedOff(h) {
		return d.endHost(ctx, r, h, core.RolloutHostSkipped, nil)
	}
	gone, err := d.unenrolled(ctx, h.HostID)
	if err != nil {
		return false, err
	}
	if gone != nil {
		return d.failHost(ctx, r, h, gone)
	}
	if h.PlanID == nil {
		input, err := json.Marshal(core.UpdateApplyInput{Version: r.Version, Channel: r.Channel})
		if err != nil {
			return false, fmt.Errorf("rollout: encode the apply input: %w", err)
		}
		cctx, cancel := context.WithTimeout(ctx, callTimeout)
		plan, err := d.cfg.Dispatcher.Plan(cctx, driverPrincipal, h.HostID, applyRef, input, core.PlanOptions{})
		cancel()
		if err != nil {
			return d.refused(ctx, r, h, err)
		}
		return d.write(ctx, r, func(tx *sql.Tx, _ time.Time) error {
			return d.setPlan(ctx, tx, r.ID, h.HostID, plan.PlanID)
		})
	}
	if h.ApplySentAt.IsZero() {
		return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
			_, err := tx.ExecContext(ctx,
				"UPDATE core_rollout_hosts SET apply_sent_at = ? WHERE rollout_id = ? AND host_id = ?",
				now.Format(timeLayout), r.ID, h.HostID)
			if err != nil {
				return fmt.Errorf("rollout: mark the apply of host %s: %w", h.HostID, err)
			}
			return nil
		})
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	v, err := d.cfg.Dispatcher.Apply(cctx, driverPrincipal, h.HostID, applyRef, *h.PlanID, core.ApplyOptions{})
	cancel()
	if err != nil {
		switch errcode.From(err).Code {
		case errcode.PlanExpired, errcode.PlanNotFound, errcode.PlanStateChanged:
			// No change was made of this plan, so nothing is running on
			// the host: plan again, or, while draining, stop here.
			return d.write(ctx, r, func(tx *sql.Tx, _ time.Time) error {
				return d.setPlan(ctx, tx, r.ID, h.HostID, "")
			})
		}
		return d.refused(ctx, r, h, err)
	}
	switch v.State {
	case core.StateCompleted:
		var tr core.UpdateTransaction
		if err := json.Unmarshal(v.Output, &tr); err != nil || tr.OperationID == "" {
			return d.resolveChange(ctx, r, h, v)
		}
		return d.storeOperation(ctx, r, h, tr.OperationID)
	case core.StateFailed, core.StateInterrupted:
		return d.resolveChange(ctx, r, h, v)
	default:
		return false, nil
	}
}

// resolveChange finds the update operation a change started through
// core.update.status with its change id: none means the change failed
// before any handoff, so the host fails; otherwise the operation is
// followed.
func (d *Driver) resolveChange(ctx context.Context, r record, h *hostRecord, v core.ChangeView) (bool, error) {
	st, err := d.status(ctx, h.HostID, core.UpdateStatusInput{ChangeID: v.ID})
	if err != nil {
		return d.refused(ctx, r, h, err)
	}
	if len(st.Transactions) > 0 {
		return d.storeOperation(ctx, r, h, st.Transactions[0].OperationID)
	}
	e := &core.ErrorDetail{Code: string(errcode.Internal), Message: fmt.Sprintf("change %s is %s", v.ID, v.State)}
	if v.Error != nil {
		e = &core.ErrorDetail{Code: v.Error.Code, Message: v.Error.Message}
	}
	return d.failHost(ctx, r, h, e)
}

// refused handles a dispatcher error: the host fails unless the error only
// means it cannot answer now.
func (d *Driver) refused(ctx context.Context, r record, h *hostRecord, err error) (bool, error) {
	if notYet(err) {
		d.cfg.Log.Debug("rollout: host not ready", "rollout", r.ID, "host", h.HostID, "error", err)
		return false, nil
	}
	return d.failHost(ctx, r, h, detailOf(err))
}

// unenrolled returns the error a host fails with when it was revoked or
// deleted since the rollout was created, and nil while it is enrolled. Such
// a host never answers again, so waiting for it would hold this rollout, and
// with it every later one, without a limit.
func (d *Driver) unenrolled(ctx context.Context, hostID string) (*core.ErrorDetail, error) {
	var state string
	err := d.cfg.DB.Read().QueryRowContext(ctx, "SELECT state FROM core_hosts WHERE id = ?", hostID).Scan(&state)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return &core.ErrorDetail{Code: string(errcode.NotFound), Message: "the host was deleted"}, nil
	case err != nil:
		return nil, fmt.Errorf("rollout: read host %s: %w", hostID, err)
	case state != core.HostEnrolled:
		return &core.ErrorDetail{Code: string(errcode.InvalidInput),
			Message: fmt.Sprintf("the host is %s; only an enrolled host can be updated", state)}, nil
	}
	return nil, nil
}

// setPlan stores or, with an empty planID, clears the plan of a host; either
// way the new plan has not been sent.
func (d *Driver) setPlan(ctx context.Context, tx *sql.Tx, id, hostID, planID string) error {
	var plan any
	if planID != "" {
		plan = planID
	}
	_, err := tx.ExecContext(ctx,
		"UPDATE core_rollout_hosts SET plan_id = ?, apply_sent_at = NULL WHERE rollout_id = ? AND host_id = ?",
		plan, id, hostID)
	if err != nil {
		return fmt.Errorf("rollout: store the plan of host %s: %w", hostID, err)
	}
	return nil
}

// storeOperation stores the update operation of h; the ten-minute limit
// runs from now.
func (d *Driver) storeOperation(ctx context.Context, r record, h *hostRecord, op string) (bool, error) {
	return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
		_, err := tx.ExecContext(ctx,
			"UPDATE core_rollout_hosts SET operation_id = ?, followed_at = ? WHERE rollout_id = ? AND host_id = ?",
			op, now.Format(timeLayout), r.ID, h.HostID)
		if err != nil {
			return fmt.Errorf("rollout: store the operation of host %s: %w", h.HostID, err)
		}
		return nil
	})
}

// status reads core.update.status on hostID.
func (d *Driver) status(ctx context.Context, hostID string, in core.UpdateStatusInput) (core.UpdateStatus, error) {
	input, err := json.Marshal(in)
	if err != nil {
		return core.UpdateStatus{}, fmt.Errorf("rollout: encode the status input: %w", err)
	}
	cctx, cancel := context.WithTimeout(ctx, callTimeout)
	defer cancel()
	out, err := d.cfg.Dispatcher.Read(cctx, driverPrincipal, hostID, statusRef, input)
	if err != nil {
		return core.UpdateStatus{}, err
	}
	var st core.UpdateStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return core.UpdateStatus{}, fmt.Errorf("rollout: decode the update status of host %s: %w", hostID, err)
	}
	return st, nil
}

// follow polls h's update operation (spec section 8, Rollout driver, step
// 3): the host commits when the newest record is committed at the
// rollout's version, and fails when any record failed or rolled back. Any
// error of the status read is "not yet", a refusal included: the operation
// may still be running, and the host fails only once it is over. While a
// running rollout's host is not done HostTimeout after its operation was
// stored, the rollout drains; a draining one keeps waiting, with a warning.
func (d *Driver) follow(ctx context.Context, r record, h *hostRecord) (bool, error) {
	st, err := d.status(ctx, h.HostID, core.UpdateStatusInput{OperationID: *h.OperationID})
	if err == nil {
		done, failure := operationResult(st.Transactions, r.Version)
		switch {
		case failure != nil:
			return d.failHost(ctx, r, h, failure)
		case done:
			return d.endHost(ctx, r, h, core.RolloutHostCommitted, nil)
		}
	} else {
		d.cfg.Log.Debug("rollout: operation status unavailable", "rollout", r.ID, "host", h.HostID, "error", err)
	}
	now := d.cfg.Clock.Now()
	if now.Sub(h.FollowedAt) < HostTimeout {
		return false, nil
	}
	if r.State != core.RolloutRunning {
		// A draining rollout waits for its host without a limit and, while
		// it waits, refuses every new rollout and holds the update windows,
		// so say so above debug level, at most once per HostTimeout.
		if now.Sub(d.stuckWarned) >= HostTimeout {
			d.stuckWarned = now
			d.cfg.Log.Warn("rollout: a draining rollout is still waiting for its host's update; "+
				"core.update.rollout-cancel with force ends it if the host never comes back",
				"rollout", r.ID, "host", h.HostID, "operation", *h.OperationID, "since", h.FollowedAt)
		}
		return false, nil
	}
	e := &core.ErrorDetail{Code: string(errcode.UpdateInterrupted),
		Message: fmt.Sprintf("host %s: its update did not finish within %s", h.HostID, HostTimeout)}
	return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
		if err := skipWaiting(ctx, tx, r.ID); err != nil {
			return err
		}
		return setRollout(ctx, tx, r.ID, core.RolloutDraining, core.RolloutFailed, e, now)
	})
}

// operationResult reads an operation's records, newest first: failure is
// set when any record failed or rolled back, and done when the newest
// record committed the rollout's version. A committed intermediate step of
// a stepped update is neither.
func operationResult(trs []core.UpdateTransaction, version string) (done bool, failure *core.ErrorDetail) {
	for _, tr := range trs {
		if tr.State == core.UpdateFailed || tr.State == core.UpdateRolledBack {
			if tr.Error != nil {
				return false, tr.Error
			}
			return false, &core.ErrorDetail{Code: string(errcode.UpdateInterrupted),
				Message: fmt.Sprintf("update transaction %s is %s", tr.ID, tr.State)}
		}
	}
	return len(trs) > 0 && trs[0].State == core.UpdateCommitted && sameVersion(trs[0].ToVersion, version), nil
}

// sameVersion reports whether two versions are equal by semantic version
// precedence; a version that does not parse equals only the same string.
func sameVersion(a, b string) bool {
	if a == b {
		return true
	}
	va, errA := update.ParseVersion(a)
	vb, errB := update.ParseVersion(b)
	return errA == nil && errB == nil && va.Compare(vb) == 0
}

// failHost records h failed with e. A running rollout fails with it at once
// and skips its waiting hosts; a draining one ends in its outcome.
func (d *Driver) failHost(ctx context.Context, r record, h *hostRecord, e *core.ErrorDetail) (bool, error) {
	return d.endHost(ctx, r, h, core.RolloutHostFailed, e)
}

// endHost records the end of h's turn. A draining rollout then ends in its
// outcome; a running one fails when the host failed and otherwise moves on
// to its next host.
func (d *Driver) endHost(ctx context.Context, r record, h *hostRecord, state string, e *core.ErrorDetail) (bool, error) {
	return d.write(ctx, r, func(tx *sql.Tx, now time.Time) error {
		if err := setRolloutHost(ctx, tx, r.ID, h.HostID, state, e); err != nil {
			return err
		}
		switch {
		case r.State == core.RolloutDraining:
			if err := skipWaiting(ctx, tx, r.ID); err != nil {
				return err
			}
			return setRollout(ctx, tx, r.ID, r.Outcome, r.Outcome, r.Error, now)
		case state == core.RolloutHostFailed:
			if err := skipWaiting(ctx, tx, r.ID); err != nil {
				return err
			}
			re := &core.ErrorDetail{Code: e.Code, Message: fmt.Sprintf("host %s: %s", h.HostID, e.Message)}
			return setRollout(ctx, tx, r.ID, core.RolloutFailed, core.RolloutFailed, re, now)
		}
		return nil
	})
}

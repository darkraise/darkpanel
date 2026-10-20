package rollout

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The core module's controller-side rollout actions (spec section 8, Fleet
// rollout).
const (
	ActionRollout       = "core.update.rollout"
	ActionRolloutCancel = "core.update.rollout-cancel"
	ActionRollouts      = "core.update.rollouts"
)

// DefaultListLimit is how many rollouts core.update.rollouts returns when
// its input names no limit.
const DefaultListLimit = 20

// ActionsConfig is what the rollout actions need beyond their Env.
type ActionsConfig struct {
	// Version is the controller's buildinfo.Version: a rollout to a newer
	// version must list the controller's own host.
	Version string
}

// Actions returns core.update.rollout/v1, core.update.rollout-cancel/v1 and
// core.update.rollouts/v1, for coreactions.Deps.Extra. They run on the
// controller engine over its Env.DB and Env.Clock. The agent process
// registers them too but never runs them; a call without a database fails
// with an internal error.
func Actions(cfg ActionsConfig) []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[core.RolloutInput, core.Rollout](core.ActionSpec{
			Name: ActionRollout, Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: ActionRolloutCancel,
			Summary: "Update the listed hosts to a release one host at a time, the controller's own host first; " +
				"the rollout stops at the first failure.",
		}, rolloutAction{cfg}),
		core.DefineMutating[core.RolloutCancelInput, core.Rollout](core.ActionSpec{
			Name: ActionRolloutCancel, Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Stop a rollout: no further host is updated; the confirmation phrase is the rollout id.",
		}, cancelAction{}),
		core.DefineRead[core.RolloutsInput, core.RolloutList](core.ActionSpec{
			Name: ActionRollouts, Version: 1, Side: core.SideController,
			Summary: "List rollouts with their hosts, newest first.",
		}, listAction{}),
	}
}

// dbOf is the controller database of a rollout action's Env.
func dbOf(env core.Env) (*store.DB, error) {
	if env.DB == nil {
		return nil, errors.New("rollout: rollout actions run on the controller and need its database")
	}
	return env.DB, nil
}

// stateHash is a plan's state hash over the parts that decide what an apply
// would do.
func stateHash(parts ...string) string {
	sum := sha256.Sum256([]byte(strings.Join(parts, "\x00")))
	return hex.EncodeToString(sum[:])
}

// errForced is the error of a host whose update a forced cancel stopped
// following.
var errForced = &core.ErrorDetail{
	Code:    string(errcode.UpdateInterrupted),
	Message: "the rollout was cancelled with force while this host was updating; its update was not followed to the end",
}

// updating returns r's host whose turn it is, or nil.
func (r record) updating() *hostRecord {
	for i := range r.Hosts {
		if r.Hosts[i].State == core.RolloutHostUpdating {
			return &r.Hosts[i]
		}
	}
	return nil
}

// handedOff reports whether h was given its update: from the moment the
// driver commits to sending its apply, the host may be applying the plan, so
// stopping the rollout waits for the host's outcome. A stored plan that was
// never sent starts nothing on the host.
func handedOff(h *hostRecord) bool {
	return !h.ApplySentAt.IsZero() || h.OperationID != nil
}

// setRollout sets rollout id's state, outcome and error; an empty outcome and
// a nil error are stored as NULL.
func setRollout(ctx context.Context, tx *sql.Tx, id, state, outcome string, e *core.ErrorDetail, now time.Time) error {
	var out, code, message any
	if outcome != "" {
		out = outcome
	}
	if e != nil {
		code, message = e.Code, e.Message
	}
	_, err := tx.ExecContext(ctx,
		"UPDATE core_rollouts SET state = ?, outcome = ?, error_code = ?, error_message = ?, updated_at = ? WHERE id = ?",
		state, out, code, message, now.UTC().Format(timeLayout), id)
	if err != nil {
		return fmt.Errorf("rollout: set rollout %s %s: %w", id, state, err)
	}
	return nil
}

// setRolloutHost sets the state and error of one host of rollout id.
func setRolloutHost(ctx context.Context, tx *sql.Tx, id, hostID, state string, e *core.ErrorDetail) error {
	var code, message any
	if e != nil {
		code, message = e.Code, e.Message
	}
	_, err := tx.ExecContext(ctx,
		"UPDATE core_rollout_hosts SET state = ?, error_code = ?, error_message = ? WHERE rollout_id = ? AND host_id = ?",
		state, code, message, id, hostID)
	if err != nil {
		return fmt.Errorf("rollout: set host %s of %s %s: %w", hostID, id, state, err)
	}
	return nil
}

// skipWaiting marks every host of rollout id that is still waiting skipped:
// the rollout stopped before it reached them.
func skipWaiting(ctx context.Context, tx *sql.Tx, id string) error {
	_, err := tx.ExecContext(ctx, "UPDATE core_rollout_hosts SET state = ? WHERE rollout_id = ? AND state = ?",
		core.RolloutHostSkipped, id, core.RolloutHostWaiting)
	if err != nil {
		return fmt.Errorf("rollout: skip the waiting hosts of %s: %w", id, err)
	}
	return nil
}

type rolloutAction struct{ cfg ActionsConfig }

func (h rolloutAction) Plan(ctx context.Context, env core.Env, in core.RolloutInput) (core.PlanOutcome, error) {
	db, err := dbOf(env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	hosts, err := check(ctx, db.Read(), in, h.cfg.Version)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("darkpanel %s (%s) on %d hosts, one at a time: %s",
				in.Version, in.Channel, len(hosts), strings.Join(hosts, ", "))},
			{Op: " ", Text: "the rollout stops at the first host whose update fails"},
			{Op: " ", Text: "compensated by core.update.rollout-cancel with the new rollout's id"},
		},
		Guards:    []core.Guard{},
		StateHash: stateHash(append([]string{h.cfg.Version, in.Version, in.Channel}, hosts...)...),
	}, nil
}

func (h rolloutAction) Apply(ctx context.Context, env core.Env, in core.RolloutInput) (core.Rollout, error) {
	db, err := dbOf(env)
	if err != nil {
		return core.Rollout{}, err
	}
	var out core.Rollout
	err = db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = Create(ctx, tx, CreateParams{
			Input: in, ControllerVersion: h.cfg.Version, ChangeID: env.ChangeID, Now: env.Clock.Now(),
		})
		return err
	})
	return out, err
}

// Verify finds the rollout by the id in out, or, for the Verify that
// startup recovery runs without an output, by the change that created it.
func (h rolloutAction) Verify(ctx context.Context, env core.Env, _ core.RolloutInput, out core.Rollout) error {
	db, err := dbOf(env)
	if err != nil {
		return err
	}
	if out.ID != "" {
		_, err := load(ctx, db.Read(), out.ID)
		return err
	}
	if env.ChangeID == "" {
		return errors.New("rollout: verify without a change or a rollout")
	}
	var id string
	err = db.Read().QueryRowContext(ctx, "SELECT id FROM core_rollouts WHERE change_id = ?", env.ChangeID).Scan(&id)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("rollout: change %s created no rollout", env.ChangeID)
	}
	return err
}

type cancelAction struct{}

// cancellable refuses a rollout that already ended.
func cancellable(r record) error {
	if r.State != core.RolloutRunning && r.State != core.RolloutDraining {
		return errcode.New(errcode.InvalidInput, fmt.Sprintf("rollout %s is already %s", r.ID, r.State))
	}
	return nil
}

// cancelsNow reports whether cancelling r ends it at once: with force, or
// when no host has been handed its update.
func cancelsNow(r record, force bool) bool {
	h := r.updating()
	return force || h == nil || !handedOff(h)
}

func (cancelAction) Plan(ctx context.Context, env core.Env, in core.RolloutCancelInput) (core.PlanOutcome, error) {
	db, err := dbOf(env)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	r, err := load(ctx, db.Read(), in.RolloutID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	if err := cancellable(r); err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{{Op: "-", Text: fmt.Sprintf("rollout %s: %s", r.ID, r.State)}}
	cur, hostID := r.updating(), ""
	if cur != nil {
		hostID = cur.HostID
	}
	switch {
	case !cancelsNow(r, in.Force):
		diff = append(diff, core.DiffLine{Op: "+", Text: fmt.Sprintf(
			"rollout %s: draining; no further host is updated, and it is cancelled once host %s's update ends",
			r.ID, hostID)})
	case cur != nil && handedOff(cur):
		diff = append(diff,
			core.DiffLine{Op: "+", Text: fmt.Sprintf("rollout %s: cancelled; hosts not yet updated are skipped", r.ID)},
			core.DiffLine{Op: "+", Text: fmt.Sprintf("host %s: failed; its update is no longer followed", hostID)})
	default:
		diff = append(diff,
			core.DiffLine{Op: "+", Text: fmt.Sprintf("rollout %s: cancelled; hosts not yet updated are skipped", r.ID)})
	}
	planID, sent := "", ""
	if cur != nil && cur.PlanID != nil {
		planID = *cur.PlanID
	}
	if cur != nil && !cur.ApplySentAt.IsZero() {
		sent = cur.ApplySentAt.UTC().Format(timeLayout)
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(r.ID, r.State, r.Outcome, hostID, planID, sent),
		Confirmation: r.ID,
	}, nil
}

func (cancelAction) Apply(ctx context.Context, env core.Env, in core.RolloutCancelInput) (core.Rollout, error) {
	db, err := dbOf(env)
	if err != nil {
		return core.Rollout{}, err
	}
	var out core.Rollout
	err = db.Write(ctx, func(tx *sql.Tx) error {
		r, err := load(ctx, tx, in.RolloutID)
		if err != nil {
			return err
		}
		if err := cancellable(r); err != nil {
			return err
		}
		if err := cancel(ctx, tx, r, in.Force, env.Clock.Now()); err != nil {
			return err
		}
		if r, err = load(ctx, tx, in.RolloutID); err != nil {
			return err
		}
		out = r.view()
		return nil
	})
	return out, err
}

// cancel stops r inside tx. A rollout whose host was handed its update
// drains: dispatching stops and the driver marks it cancelled once that
// update ends. With force, or when no host was handed an update, it is
// cancelled at once; a forced cancel no longer follows the updating host,
// which fails.
func cancel(ctx context.Context, tx *sql.Tx, r record, force bool, now time.Time) error {
	if err := skipWaiting(ctx, tx, r.ID); err != nil {
		return err
	}
	if !cancelsNow(r, force) {
		return setRollout(ctx, tx, r.ID, core.RolloutDraining, core.RolloutCancelled, r.Error, now)
	}
	if h := r.updating(); h != nil {
		state, e := core.RolloutHostSkipped, (*core.ErrorDetail)(nil)
		if handedOff(h) {
			state, e = core.RolloutHostFailed, errForced
		}
		if err := setRolloutHost(ctx, tx, r.ID, h.HostID, state, e); err != nil {
			return err
		}
	}
	return setRollout(ctx, tx, r.ID, core.RolloutCancelled, core.RolloutCancelled, r.Error, now)
}

// Verify checks that the rollout is cancelled or draining towards
// cancelled.
func (cancelAction) Verify(ctx context.Context, env core.Env, in core.RolloutCancelInput, _ core.Rollout) error {
	db, err := dbOf(env)
	if err != nil {
		return err
	}
	r, err := load(ctx, db.Read(), in.RolloutID)
	if err != nil {
		return err
	}
	if r.State == core.RolloutCancelled || (r.State == core.RolloutDraining && r.Outcome == core.RolloutCancelled) {
		return nil
	}
	return fmt.Errorf("rollout: rollout %s is %s after its cancel", r.ID, r.State)
}

type listAction struct{}

func (listAction) Read(ctx context.Context, env core.Env, in core.RolloutsInput) (core.RolloutList, error) {
	db, err := dbOf(env)
	if err != nil {
		return core.RolloutList{}, err
	}
	limit := in.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	records, err := list(ctx, db.Read(), limit)
	if err != nil {
		return core.RolloutList{}, err
	}
	out := core.RolloutList{Rollouts: make([]core.Rollout, 0, len(records))}
	for _, r := range records {
		out.Rollouts = append(out.Rollouts, r.view())
	}
	return out, nil
}

// list reads the newest limit rollouts with their hosts, newest first.
func list(ctx context.Context, q querier, limit int) ([]record, error) {
	rows, err := q.QueryContext(ctx,
		"SELECT "+rolloutColumns+" FROM core_rollouts ORDER BY created_at DESC, id DESC LIMIT ?", limit)
	if err != nil {
		return nil, fmt.Errorf("rollout: list rollouts: %w", err)
	}
	var out []record
	for rows.Next() {
		r, err := scanRollout(rows)
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("rollout: list rollouts: %w", err)
		}
		out = append(out, r)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("rollout: list rollouts: %w", err)
	}
	for i := range out {
		if err := loadHosts(ctx, q, &out[i]); err != nil {
			return nil, err
		}
	}
	return out, nil
}

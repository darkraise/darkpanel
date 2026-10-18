package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Confirm timeouts of revertible changes (spec section 3).
const (
	DefaultConfirmTimeout = 60 * time.Second
	MinConfirmTimeout     = 10 * time.Second
	MaxConfirmTimeout     = 15 * time.Minute
)

// snapshotAD is the associated data of a change's sealed snapshot (ADR 0005).
func snapshotAD(changeID string) string { return "core_changes.snapshot_ct." + changeID }

func revertFailed() *core.ChangeError {
	return &core.ChangeError{
		Code:    string(errcode.ChangeRevertFailed),
		Message: "the revert failed; the host may still be in the applied state",
	}
}

type planRecord struct {
	id, action, actor, stateHash, confirmation string
	version                                    int
	inputCT                                    []byte
	guards                                     []core.Guard
	expiresAt                                  time.Time
}

// intent is a change whose intent is persisted; its mutation comes next.
type intent struct {
	id         string
	def        core.ActionDef
	input      json.RawMessage
	deadline   time.Time // revertible only
	confirmNow bool
}

// Apply executes a stored plan with ADR 0004's ordering. A plan that already
// has a change returns that change's current view whatever its state; it never
// acts twice. The returned Output is the handler's output as is; the stored
// change, every later view and the journal carry it redacted.
func (e *Engine) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	e.applyMu.Lock()
	it, existing, err := e.begin(ctx, p, planID, opts)
	e.applyMu.Unlock()
	switch {
	case err != nil:
		return core.ChangeView{}, err
	case existing != nil:
		return *existing, nil
	}
	// The intent is persisted: a cancelled request must not stop the change
	// half way, so the rest runs until its result is recorded.
	return e.run(context.WithoutCancel(ctx), p, it)
}

// begin runs apply's checks and persists the intent (ADR 0004 steps 1 and 2).
// It runs under applyMu.
func (e *Engine) begin(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (*intent, *core.ChangeView, error) {
	db := e.cfg.Base.DB
	pl, err := e.loadPlan(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	if p.Actor != pl.actor {
		return nil, nil, errcode.New(errcode.PlanPrincipalMismatch, "the plan was made by another principal")
	}
	existing, err := scanChange(db.Read().QueryRowContext(ctx,
		"SELECT "+changeColumns+" FROM core_changes WHERE plan_id = ?", planID))
	switch {
	case err == nil:
		return nil, &existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, nil, fmt.Errorf("lifecycle: read change of plan %s: %w", planID, err)
	}
	now := e.cfg.Base.Clock.Now()
	if !now.Before(pl.expiresAt) {
		return nil, nil, errcode.New(errcode.PlanExpired, "the plan expired; plan again")
	}
	def, err := e.action(core.ActionRef{Name: pl.action, Version: pl.version})
	if err != nil {
		return nil, nil, err
	}
	timeout, err := confirmTimeout(def.Spec.Class, opts.ConfirmTimeout)
	if err != nil {
		return nil, nil, err
	}
	if def.Spec.Class == core.ClassIrreversible && opts.Confirmation != pl.confirmation {
		return nil, nil, errcode.New(errcode.PlanConfirmationNeeded,
			fmt.Sprintf("type the confirmation phrase %q to apply", pl.confirmation))
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(pl.id), pl.inputCT)
	if err != nil {
		return nil, nil, fmt.Errorf("lifecycle: open plan input: %w", err)
	}
	// Checked before recheck and the snapshot: a plan made while another change
	// held the lock describes the host with that change applied, and if the
	// holder were reverted after the snapshot, reverting this change would
	// re-apply it. Only applies take locks and they hold applyMu, so a key
	// found free here stays free until takeLocks.
	if err := locksFree(ctx, db.Read(), def.Spec.LockKeys); err != nil {
		return nil, nil, err
	}
	env := e.env(p)
	if err := e.recheck(ctx, env, def, pl, input); err != nil {
		return nil, nil, err
	}

	it := &intent{id: e.newID(), def: def, input: input, confirmNow: opts.ConfirmNow}
	var snapshot []byte
	var deadline any
	if def.Spec.Class == core.ClassRevertible {
		var raw []byte
		err := e.protect(def.Ref(), "Snapshot", func() error {
			var err error
			raw, err = def.Handler.Snapshot(ctx, env, input)
			return err
		})
		if err != nil {
			return nil, nil, err
		}
		if snapshot, err = e.cfg.Base.Sealer.Seal(snapshotAD(it.id), raw); err != nil {
			return nil, nil, fmt.Errorf("lifecycle: seal snapshot: %w", err)
		}
		it.deadline = now.Add(timeout)
		deadline = formatTime(it.deadline)
	}
	created := formatTime(now)
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if err := takeLocks(ctx, tx, it.id, def.Spec.LockKeys); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 input_json, snapshot_ct, revert_deadline, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.id, pl.id, e.cfg.Base.HostID, def.Spec.Name, def.Spec.Version, string(def.Spec.Class),
			def.Spec.CompensatedBy, p.Actor, core.StateApplying, string(e.cfg.Redact(def.InputType, input)),
			snapshot, deadline, created, created)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return nil, nil, err
	case err != nil:
		return nil, nil, fmt.Errorf("lifecycle: persist intent: %w", err)
	}
	return it, nil, nil
}

func (e *Engine) loadPlan(ctx context.Context, planID string) (planRecord, error) {
	pl := planRecord{id: planID}
	var guards, expires string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT action, version, actor, input_ct, state_hash, guards_json, confirmation, expires_at
		 FROM core_plans WHERE id = ?`, planID).
		Scan(&pl.action, &pl.version, &pl.actor, &pl.inputCT, &pl.stateHash, &guards, &pl.confirmation, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return pl, errcode.New(errcode.PlanNotFound, fmt.Sprintf("no plan %s", planID))
	}
	if err != nil {
		return pl, fmt.Errorf("lifecycle: read plan: %w", err)
	}
	if err := json.Unmarshal([]byte(guards), &pl.guards); err != nil {
		return pl, fmt.Errorf("lifecycle: read plan guards: %w", err)
	}
	pl.expiresAt, err = parseTime(expires)
	return pl, err
}

func confirmTimeout(class core.Class, d time.Duration) (time.Duration, error) {
	switch {
	case class != core.ClassRevertible:
		return 0, nil
	case d == 0:
		return DefaultConfirmTimeout, nil
	case d < MinConfirmTimeout || d > MaxConfirmTimeout:
		return 0, &errcode.Error{
			Code: errcode.InvalidInput, Detail: "the confirm timeout must be between 10 seconds and 15 minutes",
			Fields: []errcode.FieldError{{Path: "/confirmTimeoutSeconds", Message: "must be between 10 and 900"}},
		}
	}
	return d, nil
}

// recheck is apply's step 1: the handler plans again from the stored input,
// and the apply is refused when the state hash changed or a guard that passed
// now fails (plan.state_changed), or a guard failed (plan.guard_failed).
func (e *Engine) recheck(ctx context.Context, env core.Env, def core.ActionDef, pl planRecord, input json.RawMessage) error {
	var out core.PlanOutcome
	err := e.protect(def.Ref(), "Plan", func() error {
		var err error
		out, err = def.Handler.Plan(ctx, env, input)
		return err
	})
	if err != nil {
		return err
	}
	if out.StateHash != pl.stateHash {
		return errcode.New(errcode.PlanStateChanged, "the state changed since the plan was made; plan again")
	}
	passed := map[string]bool{}
	for _, g := range pl.guards {
		passed[g.ID] = g.OK
	}
	for _, g := range out.Guards {
		switch {
		case g.OK:
		case passed[g.ID]:
			return errcode.New(errcode.PlanStateChanged,
				fmt.Sprintf("guard %s passed when the plan was made and fails now; plan again", g.ID))
		default:
			return errcode.New(errcode.PlanGuardFailed, fmt.Sprintf("guard %s failed: %s", g.ID, g.Message))
		}
	}
	return nil
}

// locksFree fails with lock.held when a change holds one of keys.
func locksFree(ctx context.Context, db *sql.DB, keys []string) error {
	for _, key := range keys {
		var held string
		err := db.QueryRowContext(ctx, "SELECT owner FROM core_locks WHERE key = ?", key).Scan(&held)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return fmt.Errorf("lifecycle: read lock %s: %w", key, err)
		default:
			return errcode.New(errcode.LockHeld, fmt.Sprintf("lock %s is held by change %s", key, held))
		}
	}
	return nil
}

// takeLocks gives every key to owner, or fails with lock.held when another
// owner has one. A key listed twice is taken once.
func takeLocks(ctx context.Context, tx *sql.Tx, owner string, keys []string) error {
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO core_locks (key, owner) VALUES (?, ?) ON CONFLICT (key) DO NOTHING", key, owner); err != nil {
			return err
		}
		var held string
		if err := tx.QueryRowContext(ctx, "SELECT owner FROM core_locks WHERE key = ?", key).Scan(&held); err != nil {
			return err
		}
		if held != owner {
			return errcode.New(errcode.LockHeld, fmt.Sprintf("lock %s is held by change %s", key, held))
		}
	}
	return nil
}

// run mutates, verifies and records the result (ADR 0004 steps 3 and 4).
func (e *Engine) run(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	env := e.env(p)
	h := it.def.Handler
	var out json.RawMessage
	err := e.protect(it.def.Ref(), "Apply", func() error {
		var err error
		out, err = h.Apply(ctx, env, it.input)
		return err
	})
	if err == nil {
		err = e.verify(ctx, it.def, env, it.input, out)
	}
	if err != nil {
		return e.failApply(ctx, p, it, err)
	}
	revertible := it.def.Spec.Class == core.ClassRevertible
	to := core.StateCompleted
	if revertible {
		to = core.StatePending
	}
	v, err := e.transition(ctx, it.id, transition{
		from: core.StateApplying, to: to, output: e.cfg.Redact(it.def.OutputType, out),
		release: !revertible, phase: phaseApply, result: resultOK, actor: p.Actor, source: p.Source,
	})
	if err != nil {
		return v, err
	}
	if revertible {
		e.armTimer(it.id, it.deadline.Sub(e.cfg.Base.Clock.Now()))
		if it.confirmNow {
			if v, err = e.Confirm(ctx, p, it.id); err != nil {
				return v, err
			}
		}
	}
	v.Output = out
	return v, nil
}

// failApply records a failed mutation or verify; a revertible change is
// reverted from its snapshot first.
func (e *Engine) failApply(ctx context.Context, p core.Principal, it *intent, cause error) (core.ChangeView, error) {
	tr := transition{
		from: core.StateApplying, to: core.StateFailed, err: e.changeError(it.def.Ref(), cause),
		release: true, phase: phaseApply, result: resultError, actor: p.Actor, source: p.Source,
	}
	if it.def.Spec.Class == core.ClassRevertible {
		if err := e.runRevert(ctx, p, it.id); err != nil {
			e.cfg.Base.Log.Error("lifecycle: revert after a failed apply", "change", it.id, "error", err, "cause", cause)
			tr.err, tr.phase = revertFailed(), phaseRevert
		} else {
			tr.to = core.StateReverted
		}
	}
	return e.transition(ctx, it.id, tr)
}

// changeError is what a change's error shows: the code and the client-safe
// detail of an errcode error, and only the title of an internal one, whose
// cause is logged instead (a panic was logged with its stack already).
func (e *Engine) changeError(ref core.ActionRef, err error) *core.ChangeError {
	c := errcode.From(err)
	msg := c.Detail
	if c.Code == errcode.Internal {
		var pe *panicError
		if !errors.As(err, &pe) {
			e.cfg.Base.Log.Error("lifecycle: action failed", "action", ref.String(), "error", err)
		}
		msg = ""
	}
	if msg == "" {
		msg = c.Code.Title()
	}
	return &core.ChangeError{Code: string(c.Code), Message: msg}
}

// runRevert runs the action's Revert with the change's stored snapshot.
func (e *Engine) runRevert(ctx context.Context, p core.Principal, changeID string) error {
	var action string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version, snapshot_ct FROM core_changes WHERE id = ?", changeID).
		Scan(&action, &version, &ct); err != nil {
		return fmt.Errorf("lifecycle: read snapshot of %s: %w", changeID, err)
	}
	ref := core.ActionRef{Name: action, Version: version}
	def, ok := e.cfg.Registry.Action(ref)
	if !ok {
		return fmt.Errorf("lifecycle: action %s is not registered", ref)
	}
	snapshot, err := e.cfg.Base.Sealer.Open(snapshotAD(changeID), ct)
	if err != nil {
		return fmt.Errorf("lifecycle: open snapshot of %s: %w", changeID, err)
	}
	return e.protect(ref, "Revert", func() error { return def.Handler.Revert(ctx, e.env(p), snapshot) })
}

// transition is one state change of a change.
type transition struct {
	from, to string
	output   json.RawMessage   // stored when set
	err      *core.ChangeError // stored when set
	release  bool              // release the change's locks
	phase    string            // journal phase; empty journals nothing
	result   string
	actor    string
	source   string // the acting principal's Source; empty for system actors
}

// transition commits a state change together with its journal entry in one
// transaction (spec section 3). A change that is not in state tr.from answers
// change.not_pending, so of two racing transitions the first to commit wins.
func (e *Engine) transition(ctx context.Context, changeID string, tr transition) (core.ChangeView, error) {
	var v core.ChangeView
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var state, input string
		err := tx.QueryRowContext(ctx, "SELECT state, input_json FROM core_changes WHERE id = ?", changeID).Scan(&state, &input)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
		case err != nil:
			return err
		case state != tr.from:
			return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", changeID, state))
		}
		now := e.cfg.Base.Clock.Now()
		var code, msg any
		if tr.err != nil {
			code, msg = tr.err.Code, tr.err.Message
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET state = ?, output_json = coalesce(?, output_json),
			 error_code = coalesce(?, error_code), error_message = coalesce(?, error_message), updated_at = ?
			 WHERE id = ?`,
			tr.to, nullJSON(tr.output), code, msg, formatTime(now), changeID); err != nil {
			return err
		}
		if tr.release {
			if _, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", changeID); err != nil {
				return err
			}
		}
		if tr.to != core.StateApplying {
			// Once a plan has a result only its hash is kept (spec section 3).
			if _, err := tx.ExecContext(ctx,
				"UPDATE core_plans SET input_ct = NULL WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
				changeID); err != nil {
				return err
			}
		}
		if v, err = scanChange(tx.QueryRowContext(ctx,
			"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID)); err != nil {
			return err
		}
		if tr.phase == "" {
			return nil
		}
		entry := core.JournalEntry{
			At: now, Actor: tr.actor, Action: v.Action, Version: v.Version, Phase: tr.phase,
			ChangeID: changeID, Input: json.RawMessage(input), Result: tr.result, Source: tr.source,
		}
		if tr.err != nil {
			entry.ErrorCode = tr.err.Code
		}
		return e.cfg.Journal.Append(ctx, tx, entry)
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return v, err
	case err != nil:
		return v, fmt.Errorf("lifecycle: record change %s: %w", changeID, err)
	}
	return v, nil
}

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

type planRecord struct {
	id, action, actor, stateHash, confirmation string
	version                                    int
	inputCT                                    []byte
	guards                                     []core.Guard
	expiresAt                                  time.Time
	call                                       core.CallContext
}

// intent is a change whose intent is persisted; its mutation comes next.
type intent struct {
	id         string
	def        core.ActionDef
	input      json.RawMessage
	deadline   time.Time // revertible only
	confirmNow bool
	call       core.CallContext // the plan's
}

// Apply executes a stored plan with ADR 0004's ordering. A plan that already
// has a change returns that change's current view whatever its state; it never
// acts twice. The returned Output is the handler's output as is; the stored
// change, every later view and the journal carry it redacted.
//
// Once the intent is persisted the change runs on the engine's run context,
// which only Close cancels, so a cancelled request never stops it half way
// (spec 1.3, deadlines). A LongRunning action answers the applying change at
// once; any other answers its result, or the applying change when the run
// has not finished within Config.SyncAnswer (spec 1.3, asynchronous apply).
func (e *Engine) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if err := e.admit(ctx); err != nil {
		return core.ChangeView{}, err
	}
	if def, ok := e.planDef(ctx, planID); ok {
		if def.Spec.Class == core.ClassComposite {
			return e.applyComposite(ctx, p, planID, opts)
		}
		if err := e.compositeGate(def.Ref()); err != nil {
			return core.ChangeView{}, err
		}
	}
	e.applyMu.Lock()
	it, existing, err := e.begin(ctx, p, planID, opts)
	e.applyMu.Unlock()
	switch {
	case err != nil:
		return core.ChangeView{}, err
	case existing != nil:
		return *existing, nil
	}
	ctx = context.WithoutCancel(ctx)
	accepted, acceptedErr := e.Change(ctx, it.id)
	type result struct {
		v   core.ChangeView
		err error
	}
	results := make(chan result, 1)
	ended := make(chan struct{})
	answer := make(chan struct{})
	timer := e.cfg.Base.Clock.AfterFunc(e.cfg.SyncAnswer, func() { close(answer) })
	defer timer.Stop()
	e.goRun(func() {
		defer close(ended)
		v, err := e.run(e.runCtx, p, it)
		results <- result{v, err}
	})
	if it.def.Spec.LongRunning {
		return accepted, acceptedErr
	}
	select {
	case r := <-results:
		return r.v, r.err
	case <-ended:
		// The run stopped without a result, as only a stopped goroutine
		// does; the change stays applying until the next start recovers it.
		select {
		case r := <-results:
			return r.v, r.err
		default:
		}
	case <-answer:
	}
	return e.Change(ctx, it.id)
}

// begin runs apply's checks and persists the intent (ADR 0004 steps 1 and 2).
// It runs under applyMu.
func (e *Engine) begin(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (*intent, *core.ChangeView, error) {
	db := e.cfg.Base.DB
	pl, err := e.loadPlan(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	if err := refuseRetired(ctx, db.Read(), planID); err != nil {
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
	if def.Spec.Task && e.cfg.Units == nil {
		return nil, nil, errcode.New(errcode.Internal,
			fmt.Sprintf("%s runs in a task unit, and this engine has no unit client", def.Ref()))
	}
	timeout, err := confirmTimeout(def.Spec.Class, opts.ConfirmTimeout)
	if err != nil {
		return nil, nil, err
	}
	var call core.CallContext
	if opts.Call != nil {
		call = *opts.Call
	}
	if call.ParentChangeID != pl.call.ParentChangeID || call.StepID != pl.call.StepID {
		return nil, nil, errcode.New(errcode.PlanStateChanged,
			"the apply names another parent change or step than the plan; plan again")
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
	repairs, err := planRepairs(ctx, db.Read(), planID)
	if err != nil {
		return nil, nil, err
	}
	if repairs == "" {
		if err := locksFree(ctx, db.Read(), def.Spec.LockKeys); err != nil {
			return nil, nil, err
		}
	} else if err := repairTargetReady(ctx, db.Read(), repairs); err != nil {
		// A repair plan's keys are held by its target, which hands them over
		// in the intent transaction.
		return nil, nil, err
	}
	env := e.env(p)
	env.Call = pl.call
	if err := e.recheck(ctx, env, def, pl, input); err != nil {
		return nil, nil, err
	}

	it := &intent{id: e.newID(e.cfg.Base.Clock.Now()), def: def, input: input, confirmNow: opts.ConfirmNow, call: pl.call}
	var parent any
	if pl.call.ParentChangeID != "" {
		parent = pl.call.ParentChangeID
	}
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
		if repairs != "" {
			if err := takeRepairTarget(ctx, tx, now, repairs, it.id); err != nil {
				return err
			}
		}
		epochs, err := takeLocks(ctx, tx, it.id, def.Spec.LockKeys, true)
		if err != nil {
			return err
		}
		recorded, err := json.Marshal(epochs)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 recovery_owner, input_json, snapshot_ct, revert_deadline, lock_epochs, parent_change_id, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.id, pl.id, e.cfg.Base.HostID, def.Spec.Name, def.Spec.Version, string(def.Spec.Class),
			def.Spec.CompensatedBy, p.Actor, core.StateApplying, core.OwnerEngine, string(e.cfg.Redact(def.InputType, input)),
			snapshot, deadline, string(recorded), parent, created, created)
		if err != nil || !def.Spec.Task {
			return err
		}
		_, err = e.startTask(ctx, tx, it.id, core.TaskOpApply)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return nil, nil, err
	case err != nil:
		return nil, nil, fmt.Errorf("lifecycle: persist intent: %w", err)
	}
	if def.Spec.Class == core.ClassRevertible {
		// Armed now, not after Verify: a hung Apply or Verify must still be
		// reverted on time (spec 1.2, the revert watchdog).
		e.armTimer(it.id, it.deadline.Sub(now))
	}
	return it, nil, nil
}

func (e *Engine) loadPlan(ctx context.Context, planID string) (planRecord, error) {
	pl := planRecord{id: planID}
	var guards, expires string
	var call, repairs sql.NullString
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT action, version, actor, input_ct, state_hash, guards_json, confirmation, expires_at, call, repairs
		 FROM core_plans WHERE id = ?`, planID).
		Scan(&pl.action, &pl.version, &pl.actor, &pl.inputCT, &pl.stateHash, &guards, &pl.confirmation, &expires, &call, &repairs)
	if errors.Is(err, sql.ErrNoRows) {
		return pl, errcode.New(errcode.PlanNotFound, fmt.Sprintf("no plan %s", planID))
	}
	if err != nil {
		return pl, fmt.Errorf("lifecycle: read plan: %w", err)
	}
	if err := json.Unmarshal([]byte(guards), &pl.guards); err != nil {
		return pl, fmt.Errorf("lifecycle: read plan guards: %w", err)
	}
	if pl.call, err = decodeCall(call, repairs); err != nil {
		return pl, err
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
// and checkReplan compares the outcome with the plan.
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
	return checkReplan(pl, out)
}

// checkReplan is the comparison of apply's step 1: the apply is refused when
// the state hash changed or a guard that passed now fails
// (plan.state_changed), or a guard failed (plan.guard_failed).
func checkReplan(pl planRecord, out core.PlanOutcome) error {
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

// locksFree fails with lock.held when a change holds one of keys, or an
// update operation holds every lock.
func locksFree(ctx context.Context, db *sql.DB, keys []string) error {
	if len(keys) > 0 {
		if err := notReserved(ctx, db, "", keys[0]); err != nil {
			return err
		}
	}
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

// run mutates, verifies and records the result (ADR 0004 steps 3 and 4).
// Apply and Verify share the action's deadline and are never abandoned: a
// handler that ignores ctx keeps its change applying until it returns.
func (e *Engine) run(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	if it.def.Spec.Task {
		return e.taskApply(ctx, p, it)
	}
	env := e.changeEnv(p, it.def, it.id, it.call)
	h, ref, timeout := it.def.Handler, it.def.Ref(), e.applyTimeout(it.def)
	dctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var out json.RawMessage
	err := e.protect(ref, "Apply", func() error {
		var err error
		out, err = h.Apply(dctx, env, it.input)
		return err
	})
	if err == nil {
		err = e.protect(ref, "Verify", func() error { return h.Verify(dctx, env, it.input, out) })
	}
	if err != nil && errors.Is(dctx.Err(), context.DeadlineExceeded) {
		err = errcode.Wrap(errcode.DeadlineExceeded, err, fmt.Sprintf("%s did not finish within %s", ref, timeout))
	}
	ctx, cancelTx := e.cleanup()
	defer cancelTx()
	if err != nil {
		return e.failApply(ctx, p, it, err)
	}
	revertible := it.def.Spec.Class == core.ClassRevertible
	to := core.StateCompleted
	if revertible {
		to = core.StatePending
	}
	v, err := e.transition(ctx, it.id, transition{
		from: core.StateApplying, to: to, output: e.cfg.Redact(it.def.OutputType, out), private: out, owner: newOwner(""),
		release: !revertible, phase: phaseApply, result: resultOK, actor: p.Actor, source: p.Source,
	})
	if err != nil {
		return v, err
	}
	if revertible {
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
// reverted from its snapshot first, and a compensable or irreversible change
// settles its effects and follows the failure table.
func (e *Engine) failApply(ctx context.Context, p core.Principal, it *intent, cause error) (core.ChangeView, error) {
	hctx, cancel := e.cleanupCall(it.def)
	defer cancel()
	if it.def.Spec.Class == core.ClassRevertible {
		e.stopTimer(it.id)
	}
	if err := e.settle(hctx, e.changeEnv(p, it.def, it.id, it.call), it.def, it.id); err != nil {
		return core.ChangeView{}, err
	}
	if it.def.Spec.Class == core.ClassRevertible {
		out, err := e.runRevert(hctx, p, it.id)
		// The settle and revert calls may have spent ctx's budget; the
		// recording transaction gets its own.
		tctx, cancelTx := context.WithTimeout(context.WithoutCancel(ctx), CleanupTxTimeout)
		defer cancelTx()
		return e.recordRevert(tctx, p, it.id, out, err, transition{
			from: core.StateApplying, to: core.StateReverted, err: e.changeError(it.def.Ref(), cause), owner: newOwner(""),
			release: true, giveBack: true, phase: phaseApply, result: resultError, actor: p.Actor, source: p.Source,
		})
	}
	tctx, cancelTx := context.WithTimeout(context.WithoutCancel(ctx), CleanupTxTimeout)
	defer cancelTx()
	return e.failSettled(tctx, p, it.id, it.def, cause)
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

// runRevert runs the action's Revert with the change's stored snapshot and
// returns what the revert reports it did.
func (e *Engine) runRevert(ctx context.Context, p core.Principal, changeID string) (core.RevertOutcome, error) {
	var action string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version, snapshot_ct FROM core_changes WHERE id = ?", changeID).
		Scan(&action, &version, &ct); err != nil {
		return core.RevertOutcome{}, fmt.Errorf("lifecycle: read snapshot of %s: %w", changeID, err)
	}
	ref := core.ActionRef{Name: action, Version: version}
	def, ok := e.cfg.Registry.Action(ref)
	if !ok {
		return core.RevertOutcome{}, fmt.Errorf("lifecycle: action %s is not registered", ref)
	}
	snapshot, err := e.cfg.Base.Sealer.Open(snapshotAD(changeID), ct)
	if err != nil {
		return core.RevertOutcome{}, fmt.Errorf("lifecycle: open snapshot of %s: %w", changeID, err)
	}
	call, err := changeCall(ctx, e.cfg.Base.DB.Read(), changeID)
	if err != nil {
		return core.RevertOutcome{}, err
	}
	env := e.changeEnv(p, def, changeID, call)
	var out core.RevertOutcome
	err = e.protect(ref, "Revert", func() error {
		var err error
		out, err = def.Handler.Revert(ctx, env, snapshot)
		return err
	})
	return out, err
}

// sealedInputDone reports whether a change in state no longer needs its
// plan's sealed input.
func sealedInputDone(state string) bool {
	switch state {
	case core.StateCompleted, core.StateConfirmed, core.StateReverted, core.StateFailed, core.StateInterrupted,
		core.StateKept, core.StateRepaired, core.StateAbandoned:
		return true
	}
	return false
}

// claim is the one conditional update of spec 1.3, ownership: changeID moves
// from fromState owned by fromOwner ("" is unowned) to toState owned by
// toOwner. When no row matches it answers core.change_busy naming the change's
// state and owner, or change.not_found.
func claim(ctx context.Context, tx *sql.Tx, now time.Time, changeID, fromOwner, toOwner, fromState, toState string) error {
	res, err := tx.ExecContext(ctx,
		`UPDATE core_changes SET recovery_owner = ?, state = ?, updated_at = ?
		 WHERE id = ? AND recovery_owner = ? AND state = ?`,
		toOwner, toState, formatTime(now), changeID, fromOwner, fromState)
	if err != nil {
		return fmt.Errorf("lifecycle: claim change %s: %w", changeID, err)
	}
	if n, err := res.RowsAffected(); err != nil || n == 1 {
		return err
	}
	var state, owner string
	err = tx.QueryRowContext(ctx, "SELECT state, recovery_owner FROM core_changes WHERE id = ?", changeID).Scan(&state, &owner)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return fmt.Errorf("lifecycle: claim change %s: %w", changeID, err)
	case owner == "":
		return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is %s", changeID, state))
	}
	return errcode.New(errcode.ChangeBusy, fmt.Sprintf("change %s is %s and owned by %s", changeID, state, owner))
}

// newOwner is a transition's owner: owner, or "" to clear it.
func newOwner(owner string) *string { return &owner }

// transition is one state change of a change.
type transition struct {
	from, to      string
	output        json.RawMessage   // stored when set
	private       json.RawMessage   // the unredacted output, sealed as a step's private output when redaction hid something
	err           *core.ChangeError // stored when set, with cause
	cause         string            // the stored error's ChangeError.Cause
	owner         *string           // the new recovery_owner; nil keeps it
	release       bool              // release the change's locks
	keepLocks     bool              // the change keeps its locks (needs_repair); contradicts release
	giveBack      bool              // give the change's lock epochs back (giveBackEpochs)
	override      json.RawMessage   // stored in override_json when set
	retry         *bool             // the new recovery_retry; nil keeps it
	repairOutcome string            // stored when set
	phase         string            // journal phase; empty journals nothing
	result        string
	actor         string
	source        string // the acting principal's Source; empty for system actors
}

// transition commits a state change together with its journal entry in one
// transaction (spec section 3). A change that is not in state tr.from answers
// change.not_pending, so of two racing transitions the first to commit wins.
func (e *Engine) transition(ctx context.Context, changeID string, tr transition) (core.ChangeView, error) {
	var v core.ChangeView
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		v, err = e.transitionTx(ctx, tx, changeID, tr)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return v, err
	case err != nil:
		return v, fmt.Errorf("lifecycle: record change %s: %w", changeID, err)
	}
	// The children are told after the commit, outside the transaction,
	// because telling them is a network call per child.
	if v.Class == core.ClassComposite && sealedInputDone(v.State) {
		e.goRun(func() { e.compositeFinal(changeID) })
	}
	return v, nil
}

// transitionTx is transition inside tx, for a caller that records more in
// the same transaction.
func (e *Engine) transitionTx(ctx context.Context, tx *sql.Tx, changeID string, tr transition) (core.ChangeView, error) {
	var v core.ChangeView
	if tr.release && tr.keepLocks {
		return v, fmt.Errorf("lifecycle: change %s: a transition both releases and keeps its locks", changeID)
	}
	var state, input string
	err := tx.QueryRowContext(ctx, "SELECT state, input_json FROM core_changes WHERE id = ?", changeID).Scan(&state, &input)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return v, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return v, err
	case state != tr.from:
		return v, errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", changeID, state))
	}
	now := e.cfg.Base.Clock.Now()
	if err := e.repairTransition(ctx, tx, now, changeID, &tr); err != nil {
		return v, err
	}
	var code, msg, cause, owner, retry, outcome any
	if tr.err != nil {
		code, msg = tr.err.Code, tr.err.Message
		if tr.cause != "" {
			cause = tr.cause
		}
	}
	if tr.owner != nil {
		owner = *tr.owner
	}
	if tr.retry != nil {
		retry = *tr.retry
	}
	if tr.repairOutcome != "" {
		outcome = tr.repairOutcome
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE core_changes SET state = ?, output_json = coalesce(?, output_json),
		 error_code = coalesce(?, error_code), error_message = coalesce(?, error_message),
		 error_cause = CASE WHEN ? THEN ? ELSE error_cause END,
		 recovery_owner = coalesce(?, recovery_owner), override_json = coalesce(?, override_json),
		 recovery_retry = coalesce(?, recovery_retry), repair_outcome = coalesce(?, repair_outcome),
		 updated_at = ? WHERE id = ?`,
		tr.to, nullJSON(tr.output), code, msg, tr.err != nil, cause, owner, nullJSON(tr.override), retry, outcome,
		formatTime(now), changeID); err != nil {
		return v, err
	}
	if err := e.storePrivate(ctx, tx, now, changeID, tr.output, tr.private); err != nil {
		return v, err
	}
	if tr.release {
		if _, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", changeID); err != nil {
			return v, err
		}
	}
	if tr.giveBack {
		if err := giveBackEpochs(ctx, tx, changeID); err != nil {
			return v, err
		}
	}
	if sealedInputDone(tr.to) {
		// Once a change is finished only its plan's hash is kept; until
		// then recovery handlers and task units read the input (spec 1.2).
		if _, err := tx.ExecContext(ctx,
			"UPDATE core_plans SET input_ct = NULL WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
			changeID); err != nil {
			return v, err
		}
	}
	if v, err = scanChange(tx.QueryRowContext(ctx,
		"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID)); err != nil {
		return v, err
	}
	if tr.phase == "" {
		return v, nil
	}
	entry := core.JournalEntry{
		At: now, Actor: tr.actor, Action: v.Action, Version: v.Version, Phase: tr.phase,
		ChangeID: changeID, Input: json.RawMessage(input), Result: tr.result, Source: tr.source,
	}
	if tr.err != nil {
		entry.ErrorCode = tr.err.Code
	}
	return v, e.cfg.Journal.Append(ctx, tx, entry)
}

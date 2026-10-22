package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ChildPoll is how often a composite looks up a child it waits for (1.3
// Child watcher).
const ChildPoll = 15 * time.Second

// compositeFailLimit is how many runner passes in a row may fail with an
// error the runner has no rule for before the composite needs repair.
const compositeFailLimit = 10

// EventCompositeStep is the event type of every committed composite step
// state (1.6).
const EventCompositeStep = "composite.step"

// StepEvent is the data of a composite.step event.
type StepEvent struct {
	ChangeID      string `json:"changeId"`
	StepID        string `json:"stepId"`
	State         string `json:"state"`
	ChildChangeID string `json:"childChangeId"`
}

// The directions of core_composites.
const (
	dirForward = "forward"
	dirUnwind  = "unwind"
)

func stepInputAD(id, stepID string) string {
	return "core_composite_steps.input_ct." + id + "." + stepID
}

func stepOutputAD(id, stepID string) string {
	return "core_composite_steps.output_ct." + id + "." + stepID
}

// compositeRun is what a live runner has and recovery does not: the
// principal that plans and applies the children, and the options of the
// composite's apply, which reach its revertible step. A nil *compositeRun is
// recovery, which never plans a step.
type compositeRun struct {
	P    core.Principal
	Opts core.ApplyOptions
}

// compositeRow is a composite's change row with its core_composites row.
type compositeRow struct {
	ID, Action, PlanID, Actor, State, Owner string
	Version                                 int
	Direction                               string
	Cursor                                  int
	StepsDeadline                           time.Time
}

// stepRow is one row of core_composite_steps.
type stepRow struct {
	ID              string
	Ordinal         int
	Host            string
	Action          core.ActionRef
	After           []string
	Template        json.RawMessage
	State           string
	Attempt         int
	PlanID, ChildID string
	Earlier         []string
	InputCT         []byte
	OutputCT        []byte
	ChildDeadline   *time.Time
	Err             *core.ChangeError
}

// loadComposite reads composite id and its steps in ordinal order. A change
// that is not a composite is change.not_found.
func loadComposite(ctx context.Context, q queryer, id string) (compositeRow, []stepRow, error) {
	c := compositeRow{ID: id}
	var deadline string
	err := q.QueryRowContext(ctx,
		`SELECT ch.action, ch.version, ch.plan_id, ch.actor, ch.state, ch.recovery_owner,
		 co.direction, co.cursor, co.steps_deadline
		 FROM core_changes ch JOIN core_composites co ON co.change_id = ch.id WHERE ch.id = ?`, id).
		Scan(&c.Action, &c.Version, &c.PlanID, &c.Actor, &c.State, &c.Owner, &c.Direction, &c.Cursor, &deadline)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return c, nil, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no composite %s", id))
	case err != nil:
		return c, nil, fmt.Errorf("lifecycle: read composite %s: %w", id, err)
	}
	if c.StepsDeadline, err = parseTime(deadline); err != nil {
		return c, nil, err
	}
	rows, err := q.QueryContext(ctx,
		`SELECT step_id, ordinal, host_id, action, after_json, input_template, state, attempt, child_plan_id,
		 child_change_id, earlier_child_change_ids, input_ct, output_ct, child_deadline, error_code, error_message
		 FROM core_composite_steps WHERE change_id = ? ORDER BY ordinal`, id)
	if err != nil {
		return c, nil, fmt.Errorf("lifecycle: read the steps of %s: %w", id, err)
	}
	defer func() { _ = rows.Close() }()
	var steps []stepRow
	for rows.Next() {
		var s stepRow
		var action, after, tmpl, earlier string
		var planID, childID, childDeadline, code, msg sql.NullString
		if err := rows.Scan(&s.ID, &s.Ordinal, &s.Host, &action, &after, &tmpl, &s.State, &s.Attempt, &planID,
			&childID, &earlier, &s.InputCT, &s.OutputCT, &childDeadline, &code, &msg); err != nil {
			return c, nil, fmt.Errorf("lifecycle: read the steps of %s: %w", id, err)
		}
		if err := s.Action.UnmarshalText([]byte(action)); err != nil {
			return c, nil, fmt.Errorf("lifecycle: step %s of %s: %w", s.ID, id, err)
		}
		if err := json.Unmarshal([]byte(after), &s.After); err != nil {
			return c, nil, fmt.Errorf("lifecycle: step %s of %s: after: %w", s.ID, id, err)
		}
		if err := json.Unmarshal([]byte(earlier), &s.Earlier); err != nil {
			return c, nil, fmt.Errorf("lifecycle: step %s of %s: earlier children: %w", s.ID, id, err)
		}
		s.Template, s.PlanID, s.ChildID = json.RawMessage(tmpl), planID.String, childID.String
		if childDeadline.Valid {
			t, err := parseTime(childDeadline.String)
			if err != nil {
				return c, nil, err
			}
			s.ChildDeadline = &t
		}
		if code.Valid {
			s.Err = &core.ChangeError{Code: code.String, Message: msg.String}
		}
		steps = append(steps, s)
	}
	return c, steps, rows.Err()
}

// stepUpdate is one committed change of a step row. State is always written;
// every other field is kept when nil, zero or empty. A PlanID or ChildID
// pointing at "" clears that column. Cursor and Direction move the
// core_composites row in the same transaction.
type stepUpdate struct {
	State         string
	Attempt       int
	PlanID        *string
	ChildID       *string
	Earlier       []string
	InputCT       []byte
	OutputCT      []byte
	ChildDeadline *time.Time
	Err           *core.ChangeError
	ClearErr      bool
	Cursor        *int
	Direction     string
}

func stepCursor(n int) *int { return &n }

// optText is a nullable column update: set reports whether to write, and
// value is NULL for "".
func optText(p *string) (set bool, value any) {
	switch {
	case p == nil:
		return false, nil
	case *p == "":
		return true, nil
	}
	return true, *p
}

func optBlob(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}

// setStep commits u for step stepID of composite id when the step is in one
// of from, and then publishes the step's state; each step boundary is its
// own committed write, made before the act it announces. A step in another
// state is change.not_pending: another mechanism moved it first.
func (e *Engine) setStep(ctx context.Context, id, stepID string, from []string, u stepUpdate) error {
	var child string
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var state string
		var stored sql.NullString
		err := tx.QueryRowContext(ctx,
			"SELECT state, child_change_id FROM core_composite_steps WHERE change_id = ? AND step_id = ?", id, stepID).
			Scan(&state, &stored)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("composite %s has no step %s", id, stepID))
		case err != nil:
			return err
		case !slices.Contains(from, state):
			return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("step %s of %s is %s", stepID, id, state))
		}
		child = stored.String
		if u.ChildID != nil {
			child = *u.ChildID
		}
		var attempt, earlier, deadline, code, msg any
		if u.Attempt > 0 {
			attempt = u.Attempt
		}
		if u.Earlier != nil {
			b, err := json.Marshal(u.Earlier)
			if err != nil {
				return err
			}
			earlier = string(b)
		}
		if u.ChildDeadline != nil {
			deadline = formatTime(*u.ChildDeadline)
		}
		if u.Err != nil {
			code, msg = u.Err.Code, u.Err.Message
		}
		planSet, planID := optText(u.PlanID)
		childSet, childID := optText(u.ChildID)
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_composite_steps SET state = ?, updated_at = ?,
			 attempt = coalesce(?, attempt),
			 child_plan_id = CASE WHEN ? THEN ? ELSE child_plan_id END,
			 child_change_id = CASE WHEN ? THEN ? ELSE child_change_id END,
			 earlier_child_change_ids = coalesce(?, earlier_child_change_ids),
			 input_ct = coalesce(?, input_ct),
			 output_ct = coalesce(?, output_ct),
			 child_deadline = coalesce(?, child_deadline),
			 error_code = CASE WHEN ? THEN NULL ELSE coalesce(?, error_code) END,
			 error_message = CASE WHEN ? THEN NULL ELSE coalesce(?, error_message) END
			 WHERE change_id = ? AND step_id = ?`,
			u.State, formatTime(e.cfg.Base.Clock.Now()), attempt, planSet, planID, childSet, childID, earlier,
			optBlob(u.InputCT), optBlob(u.OutputCT), deadline, u.ClearErr, code, u.ClearErr, msg, id, stepID); err != nil {
			return err
		}
		if u.Cursor == nil && u.Direction == "" {
			return nil
		}
		var cursor, direction any
		if u.Cursor != nil {
			cursor = *u.Cursor
		}
		if u.Direction != "" {
			direction = u.Direction
		}
		_, err = tx.ExecContext(ctx,
			"UPDATE core_composites SET cursor = coalesce(?, cursor), direction = coalesce(?, direction) WHERE change_id = ?",
			cursor, direction, id)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: record step %s of %s: %w", stepID, id, err)
	}
	e.publishStep(id, stepID, u.State, child)
	return nil
}

func (e *Engine) publishStep(id, stepID, state, childID string) {
	if e.cfg.Publish == nil {
		return
	}
	e.cfg.Publish(EventCompositeStep, StepEvent{ChangeID: id, StepID: stepID, State: state, ChildChangeID: childID})
}

// compLock serializes the acts on one composite. refs counts its holders
// and waiters, so the entry is dropped once the last one is done and the map
// does not grow with every composite ever run.
type compLock struct {
	mu   sync.Mutex
	refs int
}

// compWaker is the wake channel of a composite's runner. runners counts the
// runners of the composite, so the entry is dropped when the last one ends.
type compWaker struct {
	ch      chan struct{}
	runners int
}

// compositeLock serializes everything that acts on composite id in this
// process: the runner's passes, confirm, revert, repair and release.
func (e *Engine) compositeLock(id string) func() {
	e.compMu.Lock()
	if e.compLocks == nil {
		e.compLocks = map[string]*compLock{}
	}
	l, ok := e.compLocks[id]
	if !ok {
		l = &compLock{}
		e.compLocks[id] = l
	}
	l.refs++
	e.compMu.Unlock()
	l.mu.Lock()
	return func() {
		l.mu.Unlock()
		e.compMu.Lock()
		defer e.compMu.Unlock()
		if l.refs--; l.refs == 0 {
			delete(e.compLocks, id)
		}
	}
}

// compositeWaker registers a runner of composite id. It returns the channel
// the runner sleeps on and the function that unregisters the runner.
func (e *Engine) compositeWaker(id string) (chan struct{}, func()) {
	e.compMu.Lock()
	defer e.compMu.Unlock()
	if e.compWake == nil {
		e.compWake = map[string]*compWaker{}
	}
	w, ok := e.compWake[id]
	if !ok {
		w = &compWaker{ch: make(chan struct{}, 1)}
		e.compWake[id] = w
	}
	w.runners++
	return w.ch, func() {
		e.compMu.Lock()
		defer e.compMu.Unlock()
		if w.runners--; w.runners == 0 {
			delete(e.compWake, id)
		}
	}
}

// wakeComposite ends the current wait of composite id's runner, so it looks
// its child up now. A composite without a runner has nothing to wake.
func (e *Engine) wakeComposite(id string) { //nolint:unused // for the routes that see a waited-for child end
	e.compMu.Lock()
	w := e.compWake[id]
	e.compMu.Unlock()
	if w == nil {
		return
	}
	select {
	case w.ch <- struct{}{}:
	default:
	}
}

// compositeSleep waits d on the engine's clock, or until wake receives or
// ctx ends.
func (e *Engine) compositeSleep(ctx context.Context, wake chan struct{}, d time.Duration) {
	timer := e.cfg.Base.Clock.AfterFunc(d, func() {
		select {
		case wake <- struct{}{}:
		default:
		}
	})
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-wake:
	}
}

// compositeWait ends a runner pass that waits for a child; the runner sleeps
// d before its next pass.
type compositeWait struct{ d time.Duration }

func (w *compositeWait) Error() string { return "lifecycle: the composite waits for a child" }

func waitChild(d time.Duration) error { return &compositeWait{d: d} }

// errCompositeDone ends the runner: the composite is terminal, needs repair,
// or another mechanism owns it.
var errCompositeDone = errors.New("lifecycle: the composite runner is done")

// runComposite runs composite id on the engine's run context, one pass at a
// time, until it is terminal or needs repair or the engine closes; Close
// leaves it as persisted, for RecoverComposites. run is nil at recovery.
func (e *Engine) runComposite(id string, run *compositeRun) {
	ctx := e.runCtx
	wake, done := e.compositeWaker(id)
	defer done()
	failures := 0
	for ctx.Err() == nil {
		err := e.compositeStep(ctx, id, run)
		var wait *compositeWait
		switch {
		case err == nil:
			failures = 0
		case errors.Is(err, errCompositeDone):
			return
		case errors.As(err, &wait):
			failures = 0
			e.compositeSleep(ctx, wake, wait.d)
		default:
			e.cfg.Base.Log.Error("lifecycle: composite runner", "change", id, "error", err)
			if failures++; failures >= compositeFailLimit && ctx.Err() == nil {
				err := e.compositeGiveUp(ctx, id, failures)
				if errors.Is(err, errCompositeDone) {
					return
				}
				e.cfg.Base.Log.Error("lifecycle: composite runner: record needs_repair", "change", id, "error", err)
			}
			e.compositeSleep(ctx, wake, ChildPoll)
		}
	}
}

// compositeGiveUp stops a runner whose passes keep failing: the composite
// goes needs_repair at its cursor step, so an operator can repair or
// release it. The errors themselves are only logged.
func (e *Engine) compositeGiveUp(ctx context.Context, id string, failures int) error {
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return err
	}
	if c.Owner != core.OwnerComposite {
		return errCompositeDone
	}
	s := steps[max(0, min(c.Cursor, len(steps)-1))]
	return e.compositeNeedsRepair(ctx, c, s, &core.ChangeError{Code: string(errcode.Internal), Message: fmt.Sprintf(
		"step %s: the composite runner failed %d times in a row; the controller log has each error", s.ID, failures)})
}

// compositeStep is one pass of the runner, under the composite's lock.
func (e *Engine) compositeStep(ctx context.Context, id string, run *compositeRun) error {
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return err
	}
	if c.Owner != core.OwnerComposite {
		return errCompositeDone
	}
	if c.State == core.StateReleasing {
		return e.releaseStep(ctx, c, steps)
	}
	if c.State == core.StateRecovering {
		return e.probeChild(ctx, c, steps)
	}
	if c.Direction == dirUnwind {
		return e.unwindStep(ctx, c, steps)
	}
	return e.forwardStep(ctx, c, steps, run)
}

func (e *Engine) forwardStep(ctx context.Context, c compositeRow, steps []stepRow, run *compositeRun) error {
	if c.Cursor >= len(steps) {
		return e.completeComposite(ctx, c, steps, run)
	}
	s := steps[c.Cursor]
	switch s.State {
	case core.StepPending, core.StepPlanning, core.StepPlanned:
		if s.Err != nil {
			def, err := e.compositeDef(c)
			if err != nil {
				return err
			}
			if def.Spec.Unwind == core.UnwindReverse {
				// The unwind was decided when this error was recorded; its start
				// check waited for a host, so resume it rather than retry the step.
				return e.unwindFrom(ctx, c.ID, s.Ordinal-1)
			}
		}
		switch {
		case run == nil:
			return e.betweenSteps(ctx, c, s)
		case s.State == core.StepPending && !e.cfg.Base.Clock.Now().Before(c.StepsDeadline):
			return e.stepFailed(ctx, c, s, s.Ordinal-1, &core.ChangeError{Code: string(errcode.DeadlineExceeded),
				Message: fmt.Sprintf("the steps deadline passed before step %s (%s) started", s.ID, s.Action)})
		case e.isReadStep(s):
			return e.readStep(ctx, c, steps, s, run)
		}
		return e.startStep(ctx, c, steps, s, run)
	case core.StepApplying, core.StepConfirming, core.StepApplied:
		kids, err := e.kids()
		if err != nil {
			return err
		}
		lk, err := lookupChild(ctx, kids, s)
		if err != nil {
			return e.childUnreachable(ctx, c, s, err)
		}
		return e.observeChild(ctx, c.ID, lk)
	}
	return fmt.Errorf("lifecycle: composite %s: step %s is %s at the cursor", c.ID, s.ID, s.State)
}

func (e *Engine) stepCall(c compositeRow, s stepRow) *core.CallContext {
	return &core.CallContext{ParentChangeID: c.ID, ParentAction: c.Action, StepID: s.ID}
}

// startStep plans and applies step s (1.3 Step log and cursor): planning is
// committed before the plan request, planned with the plan id once it
// answered, and applying before the apply request, so only a recorded plan
// id is ever applied. The next pass looks the child up.
func (e *Engine) startStep(ctx context.Context, c compositeRow, steps []stepRow, s stepRow, run *compositeRun) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	def, ok := e.cfg.Registry.Action(s.Action)
	if !ok {
		return fmt.Errorf("lifecycle: composite %s: step %s: action %s is not registered", c.ID, s.ID, s.Action)
	}
	call := e.stepCall(c, s)
	if s.State == core.StepPending {
		if err := e.setStep(ctx, c.ID, s.ID, []string{core.StepPending}, stepUpdate{State: core.StepPlanning}); err != nil {
			return err
		}
		s.State = core.StepPlanning
	}
	if s.State == core.StepPlanning {
		input, err := e.stepInput(c, steps, s)
		if err == nil {
			err = e.cfg.Validator.ValidateInput(def, input)
		}
		if err != nil {
			return e.stepNotApplied(ctx, c, s, err)
		}
		sealed, err := e.cfg.Base.Sealer.Seal(stepInputAD(c.ID, s.ID), input)
		if err != nil {
			return fmt.Errorf("lifecycle: seal the input of step %s of %s: %w", s.ID, c.ID, err)
		}
		v, err := kids.Plan(ctx, run.P, s.Host, s.Action, input, core.PlanOptions{Call: call})
		if err != nil {
			return e.stepNotApplied(ctx, c, s, err)
		}
		if err := e.setStep(ctx, c.ID, s.ID, []string{core.StepPlanning},
			stepUpdate{State: core.StepPlanned, PlanID: &v.PlanID, InputCT: sealed}); err != nil {
			return err
		}
		s.State, s.PlanID = core.StepPlanned, v.PlanID
	}
	opts := core.ApplyOptions{Call: call}
	if def.Spec.Class == core.ClassRevertible {
		opts.ConfirmTimeout, opts.ConfirmNow = run.Opts.ConfirmTimeout, run.Opts.ConfirmNow
	}
	deadline := e.cfg.Base.Clock.Now().Add(e.applyTimeout(def))
	if err := e.setStep(ctx, c.ID, s.ID, []string{core.StepPlanned},
		stepUpdate{State: core.StepApplying, Attempt: s.Attempt + 1, ChildDeadline: &deadline}); err != nil {
		return err
	}
	v, applyErr := kids.Apply(ctx, run.P, s.Host, s.Action, s.PlanID, opts)
	u := stepUpdate{State: core.StepApplying}
	switch {
	case applyErr != nil:
		// The answer may be lost after the child committed; the lookup decides.
		u.Err = e.stepError(s, applyErr)
	case v.ID != "":
		u.ChildID = &v.ID
		if len(v.Output) > 0 && !compositeSecretType(def.OutputType) {
			sealed, err := e.cfg.Base.Sealer.Seal(stepOutputAD(c.ID, s.ID), v.Output)
			if err != nil {
				return fmt.Errorf("lifecycle: seal the output of step %s of %s: %w", s.ID, c.ID, err)
			}
			u.OutputCT = sealed
		}
	}
	return e.setStep(ctx, c.ID, s.ID, []string{core.StepApplying}, u)
}

// stepInput resolves s's template against the sealed unredacted outputs of
// the steps it references (1.3 References).
func (e *Engine) stepInput(c compositeRow, steps []stepRow, s stepRow) (json.RawMessage, error) {
	refs, err := stepRefs(s.Template)
	if err != nil {
		return nil, err
	}
	outputs := map[string]json.RawMessage{}
	for _, r := range refs {
		i := slices.IndexFunc(steps, func(o stepRow) bool { return o.ID == r })
		if i < 0 || len(steps[i].OutputCT) == 0 {
			return nil, errcode.New(errcode.ChildFailed, fmt.Sprintf("step %s has no output for step %s", r, s.ID))
		}
		raw, err := e.cfg.Base.Sealer.Open(stepOutputAD(c.ID, r), steps[i].OutputCT)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: open the output of step %s of %s: %w", r, c.ID, err)
		}
		outputs[r] = raw
	}
	return resolveStepRefs(s.Template, outputs)
}

// readStep runs a read step when the cursor reaches it (1.3 Read steps): no
// plan, no change; the result is sealed into the step row.
func (e *Engine) readStep(ctx context.Context, c compositeRow, steps []stepRow, s stepRow, run *compositeRun) error {
	kids, err := e.kids()
	if err != nil {
		return err
	}
	input, err := e.stepInput(c, steps, s)
	if err != nil {
		return e.stepFailed(ctx, c, s, s.Ordinal-1, e.stepError(s, err))
	}
	out, err := kids.Read(ctx, run.P, s.Host, s.Action, input)
	switch {
	case errcode.From(err).Code == errcode.HostOffline:
		return e.childUnreachable(ctx, c, s, err)
	case err != nil:
		return e.stepFailed(ctx, c, s, s.Ordinal-1, e.stepError(s, err))
	}
	sealed, err := e.cfg.Base.Sealer.Seal(stepOutputAD(c.ID, s.ID), out)
	if err != nil {
		return fmt.Errorf("lifecycle: seal the result of step %s of %s: %w", s.ID, c.ID, err)
	}
	return e.setStep(ctx, c.ID, s.ID, []string{core.StepPending},
		stepUpdate{State: core.StepRead, OutputCT: sealed, Cursor: stepCursor(s.Ordinal + 1)})
}

// stepNotApplied ends a step that failed before its apply was sent: it is
// pending again with the error, and the composite moves on as for a child
// that was never applied.
func (e *Engine) stepNotApplied(ctx context.Context, c compositeRow, s stepRow, cause error) error {
	cerr := e.stepError(s, cause)
	if err := e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{State: core.StepPending, Err: cerr}); err != nil {
		return err
	}
	s.State = core.StepPending
	return e.stepFailed(ctx, c, s, s.Ordinal-1, cerr)
}

// stepFailed records cause on step s and moves the composite on by 1.3's
// tables: a reverse composite unwinds from ordinal down (failed when nothing
// there needs undoing), a forward composite needs repair at s.
func (e *Engine) stepFailed(ctx context.Context, c compositeRow, s stepRow, ordinal int, cause *core.ChangeError) error {
	def, err := e.compositeDef(c)
	if err != nil {
		return err
	}
	if def.Spec.Unwind == core.UnwindForward {
		return e.compositeNeedsRepair(ctx, c, s, cause)
	}
	if err := e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{State: s.State, Err: cause}); err != nil {
		return err
	}
	if err := e.compositeCause(ctx, c.ID, cause); err != nil {
		return err
	}
	return e.unwindFrom(ctx, c.ID, ordinal)
}

// stepFailure is a composite's error naming step s.
func stepFailure(s stepRow, why string) *core.ChangeError {
	where := "on host " + s.Host
	if s.Host == "" {
		where = "on the controller"
	}
	return &core.ChangeError{Code: string(errcode.ChildFailed), Message: fmt.Sprintf("step %s (%s %s): %s", s.ID, s.Action, where, why)}
}

func (e *Engine) stepError(s stepRow, err error) *core.ChangeError {
	ce := e.changeError(s.Action, err)
	return stepFailure(s, ce.Code+": "+ce.Message)
}

func childFailure(s stepRow, lk core.PlanLookup) *core.ChangeError {
	why := "child " + lk.ChangeID + " is " + lk.State
	if lk.Error != nil {
		why += ": " + lk.Error.Code + ": " + lk.Error.Message
	}
	return stepFailure(s, why)
}

// compositeCause records why the composite is unwinding; the terminal
// transition keeps it.
func (e *Engine) compositeCause(ctx context.Context, id string, cause *core.ChangeError) error {
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "UPDATE core_changes SET error_code = ?, error_message = ?, updated_at = ? WHERE id = ?",
			cause.Code, cause.Message, formatTime(e.cfg.Base.Clock.Now()), id)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: record the cause of composite %s: %w", id, err)
	}
	return nil
}

// compositeNeedsRepair stops composite c at step s: needs_repair with
// cause, owner cleared, its composite lock kept; only an operator's repair
// or step decision moves it on.
func (e *Engine) compositeNeedsRepair(ctx context.Context, c compositeRow, s stepRow, cause *core.ChangeError) error {
	if err := e.setStep(ctx, c.ID, s.ID, []string{s.State}, stepUpdate{State: s.State, Err: cause}); err != nil {
		return err
	}
	_, err := e.transition(ctx, c.ID, transition{
		from: c.State, to: core.StateNeedsRepair, owner: newOwner(""), keepLocks: true,
		err:   &core.ChangeError{Code: string(errcode.NeedsRepair), Message: cause.Message, Cause: cause.Code},
		cause: cause.Code, phase: phaseApply, result: resultError, actor: c.Actor,
	})
	if err != nil {
		return err
	}
	return errCompositeDone
}

func (e *Engine) compositeDef(c compositeRow) (core.ActionDef, error) {
	return e.action(core.ActionRef{Name: c.Action, Version: c.Version})
}

func (e *Engine) isReadStep(s stepRow) bool {
	def, ok := e.cfg.Registry.Action(s.Action)
	return ok && def.Spec.Class == core.ClassRead
}

// lookupChild looks step s's child up. A plan its side no longer holds was
// purged unapplied, since a plan with a change is kept, so it reads as
// retired.
func lookupChild(ctx context.Context, kids Children, s stepRow) (core.PlanLookup, error) {
	lk, err := kids.Lookup(ctx, s.Host, s.PlanID, false)
	if errcode.From(err).Code == errcode.PlanNotFound {
		return core.PlanLookup{Status: core.LookupRetired}, nil
	}
	return lk, err
}

func retireChild(ctx context.Context, kids Children, s stepRow) (core.PlanLookup, error) {
	lk, err := kids.Retire(ctx, s.Host, s.PlanID)
	if errcode.From(err).Code == errcode.PlanNotFound {
		return core.PlanLookup{Status: core.LookupRetired}, nil
	}
	return lk, err
}

// childUnreachable is what a failed child call means for the runner (1.3
// Child watcher). An unreachable host is waited for until the step's child
// deadline (if any) plus UnreachableGrace; then the composite is recovering
// and polls every UnreachablePoll. A releasing composite only retries, so it
// stays releasing. Any other error, and any error met on a composite the
// runner does not own (a repair's checks), is returned.
func (e *Engine) childUnreachable(ctx context.Context, c compositeRow, s stepRow, err error) error {
	if errcode.From(err).Code != errcode.HostOffline || c.Owner != core.OwnerComposite {
		return err
	}
	switch {
	case c.State == core.StateRecovering:
		return waitChild(UnreachablePoll)
	case c.State == core.StateReleasing:
		return waitChild(ChildPoll)
	case s.ChildDeadline != nil && e.cfg.Base.Clock.Now().Before(s.ChildDeadline.Add(UnreachableGrace)):
		return waitChild(ChildPoll)
	}
	if _, terr := e.transition(ctx, c.ID, transition{from: c.State, to: core.StateRecovering}); terr != nil {
		return terr
	}
	return waitChild(UnreachablePoll)
}

// observeChild applies 1.3's transition tables, chosen by the persisted
// direction, to lk, a lookup of the cursor step's child. An absent plan is
// retired first, and the retire's answer is read in its place.
func (e *Engine) observeChild(ctx context.Context, id string, lk core.PlanLookup) error {
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return err
	}
	if c.Cursor < 0 || c.Cursor >= len(steps) {
		return fmt.Errorf("lifecycle: composite %s has no step at cursor %d", id, c.Cursor)
	}
	s := steps[c.Cursor]
	if lk.Status == core.LookupAbsent {
		kids, err := e.kids()
		if err != nil {
			return err
		}
		if lk, err = retireChild(ctx, kids, s); err != nil {
			return e.childUnreachable(ctx, c, s, err)
		}
		if lk.Status == core.LookupAbsent {
			return waitChild(ChildPoll)
		}
	}
	if lk.ChangeID != "" && lk.ChangeID != s.ChildID {
		if err := e.setStep(ctx, id, s.ID, []string{s.State}, stepUpdate{State: s.State, ChildID: &lk.ChangeID}); err != nil {
			return err
		}
		s.ChildID = lk.ChangeID
	}
	if c.Direction == dirUnwind {
		return e.unwindObserve(ctx, c, s, lk)
	}
	return e.forwardObserve(ctx, c, s, lk)
}

// forwardObserve is 1.3's table for the direction forward.
func (e *Engine) forwardObserve(ctx context.Context, c compositeRow, s stepRow, lk core.PlanLookup) error {
	switch {
	case lk.Status == core.LookupRunning, lk.Status == core.LookupRecovering:
		return waitChild(ChildPoll)
	case lk.Status == core.LookupRetired:
		why := "its plan was retired without a change"
		if s.Err != nil {
			why += " after " + s.Err.Message
		}
		return e.stepFailed(ctx, c, s, s.Ordinal-1, stepFailure(s, why))
	case lk.Unresolved > 0:
		return e.compositeNeedsRepair(ctx, c, s,
			stepFailure(s, fmt.Sprintf("child %s has %d unresolved effects", lk.ChangeID, lk.Unresolved)))
	}
	switch {
	case lk.State == core.StateCompleted, lk.State == core.StateConfirmed,
		lk.State == core.StateRepaired && lk.RepairOutcome == core.RepairKept:
		return e.stepDone(ctx, c, s, lk)
	case lk.State == core.StatePending:
		return e.stepAwaiting(ctx, c, s, lk)
	case lk.State == core.StateReverted, lk.State == core.StateRepaired:
		return e.stepFailed(ctx, c, s, s.Ordinal-1, childFailure(s, lk))
	case lk.State == core.StateFailed, lk.State == core.StateInterrupted:
		return e.stepFailed(ctx, c, s, s.Ordinal, childFailure(s, lk))
	}
	return e.compositeNeedsRepair(ctx, c, s, childFailure(s, lk))
}

// stepDone makes step s final and moves the cursor past it. When the apply
// answer did not carry the child's output, it is taken from the lookup or,
// for an output type with secret fields, from the private output (1.3 Child
// results); a private output gone before it was sealed is output-lost, and
// the composite never continues with a redacted value. The private output is
// acknowledged once sealed. An error a lost apply answer left on the step is
// cleared.
func (e *Engine) stepDone(ctx context.Context, c compositeRow, s stepRow, lk core.PlanLookup) error {
	sdef, ok := e.cfg.Registry.Action(s.Action)
	if !ok {
		return fmt.Errorf("lifecycle: composite %s: step %s: action %s is not registered", c.ID, s.ID, s.Action)
	}
	kids, err := e.kids()
	if err != nil {
		return err
	}
	secret := compositeSecretType(sdef.OutputType)
	u := stepUpdate{State: core.StepApplied, Cursor: stepCursor(s.Ordinal + 1), ClearErr: true}
	if lk.State == core.StateConfirmed {
		u.State = core.StepConfirmed
	}
	if len(s.OutputCT) == 0 {
		raw := lk.Output
		if secret {
			priv, err := kids.Lookup(ctx, s.Host, s.PlanID, true)
			if err != nil {
				return e.childUnreachable(ctx, c, s, err)
			}
			if len(priv.Private) == 0 {
				return e.stepFailed(ctx, c, s, s.Ordinal,
					stepFailure(s, fmt.Sprintf("the output of child %s was lost before it was sealed", lk.ChangeID)))
			}
			raw = priv.Private
		}
		if len(raw) == 0 {
			raw = json.RawMessage("null")
		}
		sealed, err := e.cfg.Base.Sealer.Seal(stepOutputAD(c.ID, s.ID), raw)
		if err != nil {
			return fmt.Errorf("lifecycle: seal the output of step %s of %s: %w", s.ID, c.ID, err)
		}
		u.OutputCT = sealed
	}
	if err := e.setStep(ctx, c.ID, s.ID, []string{core.StepApplying, core.StepConfirming, core.StepApplied}, u); err != nil {
		return err
	}
	if secret {
		// An unacknowledged private output is cleared after PrivateOutputTTL
		// anyway, so a failed acknowledgement is only logged.
		if err := kids.AckPrivate(ctx, s.Host, s.ChildID, false); err != nil {
			e.cfg.Base.Log.Warn("lifecycle: acknowledge a child's private output",
				"change", c.ID, "step", s.ID, "child", s.ChildID, "error", err)
		}
	}
	return nil
}

// stepAwaiting is the revertible last step's child pending: the step is
// applied and the composite awaits confirmation (1.3 Confirmation), while
// the runner keeps looking the child up. A step confirming had its confirm
// sent, and the answer was lost or the confirm never arrived: it is sent
// again.
func (e *Engine) stepAwaiting(ctx context.Context, c compositeRow, s stepRow, lk core.PlanLookup) error {
	if s.State == core.StepConfirming {
		kids, err := e.kids()
		if err != nil {
			return err
		}
		_, err = kids.Confirm(ctx, recoveryPrincipal, s.ChildID)
		switch {
		case err == nil:
			return nil
		case errcode.From(err).Code == errcode.HostOffline:
			return e.childUnreachable(ctx, c, s, err)
		}
		return waitChild(ChildPoll)
	}
	if s.State == core.StepApplying {
		if err := e.setStep(ctx, c.ID, s.ID, []string{core.StepApplying},
			stepUpdate{State: core.StepApplied, ChildDeadline: lk.Deadline}); err != nil {
			return err
		}
	}
	if c.State != core.StateAwaitingConfirmation {
		if _, err := e.transition(ctx, c.ID, transition{from: c.State, to: core.StateAwaitingConfirmation}); err != nil {
			return err
		}
	}
	return waitChild(ChildPoll)
}

// betweenSteps is recovery at a cursor step whose apply was never sent (1.3
// Between steps): a planned plan is retired first, and the step counts as
// never applied.
func (e *Engine) betweenSteps(ctx context.Context, c compositeRow, s stepRow) error {
	if s.State == core.StepPlanned {
		kids, err := e.kids()
		if err != nil {
			return err
		}
		lk, err := retireChild(ctx, kids, s)
		if err != nil {
			return e.childUnreachable(ctx, c, s, err)
		}
		if lk.Status != core.LookupRetired {
			return e.setStep(ctx, c.ID, s.ID, []string{core.StepPlanned}, stepUpdate{State: core.StepApplying})
		}
	}
	return e.stepFailed(ctx, c, s, s.Ordinal-1, stepFailure(s, "the controller restarted before the step was applied"))
}

// completeComposite builds the output from every non-read step's redacted
// output and records the composite completed, releasing its lock.
func (e *Engine) completeComposite(ctx context.Context, c compositeRow, steps []stepRow, run *compositeRun) error {
	def, err := e.compositeDef(c)
	if err != nil {
		return err
	}
	outputs := map[string]json.RawMessage{}
	for _, s := range steps {
		sdef, ok := e.cfg.Registry.Action(s.Action)
		if !ok {
			return fmt.Errorf("lifecycle: composite %s: step %s: action %s is not registered", c.ID, s.ID, s.Action)
		}
		if sdef.Spec.Class == core.ClassRead {
			continue
		}
		raw := json.RawMessage("null")
		if len(s.OutputCT) > 0 {
			if raw, err = e.cfg.Base.Sealer.Open(stepOutputAD(c.ID, s.ID), s.OutputCT); err != nil {
				return fmt.Errorf("lifecycle: open the output of step %s of %s: %w", s.ID, c.ID, err)
			}
		}
		outputs[s.ID] = e.cfg.Redact(sdef.OutputType, raw)
	}
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx, "SELECT input_ct FROM core_plans WHERE id = ?", c.PlanID).
		Scan(&ct); err != nil {
		return fmt.Errorf("lifecycle: read the input of composite %s: %w", c.ID, err)
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(c.PlanID), ct)
	if err != nil {
		return fmt.Errorf("lifecycle: open the input of composite %s: %w", c.ID, err)
	}
	p := recoveryPrincipal
	if run != nil {
		p = run.P
	}
	env := e.changeEnv(p, def, c.ID, core.CallContext{})
	var out json.RawMessage
	err = e.protect(def.Ref(), "Output", func() error {
		var err error
		out, err = def.Handler.Output(ctx, env, input, outputs)
		return err
	})
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: composite output", "change", c.ID, "error", err)
		return e.compositeNeedsRepair(ctx, c, steps[len(steps)-1],
			&core.ChangeError{Code: string(errcode.Internal), Message: "the composite's output could not be built"})
	}
	_, err = e.transition(ctx, c.ID, transition{
		from: c.State, to: core.StateCompleted, output: e.cfg.Redact(def.OutputType, out), owner: newOwner(""),
		release: true, phase: phaseApply, result: resultOK, actor: c.Actor,
	})
	if err != nil {
		return err
	}
	return errCompositeDone
}

// planDef is the registered action of a stored plan; false when the plan or
// its action is unknown, which Apply then reports.
func (e *Engine) planDef(ctx context.Context, planID string) (core.ActionDef, bool) {
	var name string
	var version int
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx, "SELECT action, version FROM core_plans WHERE id = ?", planID).
		Scan(&name, &version); err != nil {
		return core.ActionDef{}, false
	}
	return e.cfg.Registry.Action(core.ActionRef{Name: name, Version: version})
}

// applyComposite is Apply for a composite (1.3 Step log and cursor). Its
// checks and recheck run without applyMu; applyMu is held only while the
// checks a concurrent apply or retire could change are repeated and the
// intent writes the change (owner composite, its composite lock), every step
// row and the cursor in one transaction. The runner continues on the
// engine's run context, so the answer is the applying change.
func (e *Engine) applyComposite(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	pl, def, input, out, steps, existing, err := e.prepareComposite(ctx, p, planID)
	var id string
	if err == nil && existing == nil {
		e.applyMu.Lock()
		id, existing, err = e.commitComposite(ctx, p, pl, def, input, out, steps)
		e.applyMu.Unlock()
	}
	switch {
	case err != nil:
		return core.ChangeView{}, err
	case existing != nil:
		return *existing, nil
	}
	v, err := e.Change(ctx, id)
	if err != nil {
		return core.ChangeView{}, err
	}
	run := &compositeRun{P: p, Opts: opts}
	e.goRun(func() { e.runComposite(id, run) })
	return v, nil
}

// existingChange is the change of plan planID, or nil when it has none.
func existingChange(ctx context.Context, q queryer, planID string) (*core.ChangeView, error) {
	v, err := scanChange(q.QueryRowContext(ctx, "SELECT "+changeColumns+" FROM core_changes WHERE plan_id = ?", planID))
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("lifecycle: read change of plan %s: %w", planID, err)
	}
	return &v, nil
}

// prepareComposite runs a composite apply's checks and rechecks its plan by
// planning it again. It runs without applyMu, so the recheck's child plans do
// not stall the controller's other applies, lookups and retires; the stored
// plan and input cannot change, and commitComposite repeats under applyMu the
// checks that can. A plan that already has a change answers that change.
func (e *Engine) prepareComposite(ctx context.Context, p core.Principal, planID string) (
	planRecord, core.ActionDef, json.RawMessage, core.PlanOutcome, []plannedStep, *core.ChangeView, error,
) {
	pl, err := e.loadPlan(ctx, planID)
	if err != nil {
		return planRecord{}, core.ActionDef{}, nil, core.PlanOutcome{}, nil, nil, err
	}
	if p.Actor != pl.actor {
		return pl, core.ActionDef{}, nil, core.PlanOutcome{}, nil, nil,
			errcode.New(errcode.PlanPrincipalMismatch, "the plan was made by another principal")
	}
	existing, err := existingChange(ctx, e.cfg.Base.DB.Read(), planID)
	if err != nil || existing != nil {
		return pl, core.ActionDef{}, nil, core.PlanOutcome{}, nil, existing, err
	}
	if !e.cfg.Base.Clock.Now().Before(pl.expiresAt) {
		return pl, core.ActionDef{}, nil, core.PlanOutcome{}, nil, nil,
			errcode.New(errcode.PlanExpired, "the plan expired; plan again")
	}
	def, err := e.action(core.ActionRef{Name: pl.action, Version: pl.version})
	if err != nil {
		return pl, core.ActionDef{}, nil, core.PlanOutcome{}, nil, nil, err
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(pl.id), pl.inputCT)
	if err != nil {
		return pl, def, nil, core.PlanOutcome{}, nil, nil, fmt.Errorf("lifecycle: open plan input: %w", err)
	}
	out, steps, err := e.compositePlan(ctx, p, def, input)
	return pl, def, input, out, steps, nil, err
}

// commitComposite persists a prepared composite apply's intent. It runs
// under applyMu and first repeats the checks that a concurrent apply or
// retire of the plan, a lock taken meanwhile, or the time spent on the
// recheck could have changed.
func (e *Engine) commitComposite(ctx context.Context, p core.Principal, pl planRecord, def core.ActionDef,
	input json.RawMessage, out core.PlanOutcome, steps []plannedStep,
) (string, *core.ChangeView, error) {
	db := e.cfg.Base.DB
	existing, err := existingChange(ctx, db.Read(), pl.id)
	if err != nil || existing != nil {
		return "", existing, err
	}
	if err := refuseRetired(ctx, db.Read(), pl.id); err != nil {
		return "", nil, err
	}
	now := e.cfg.Base.Clock.Now()
	if !now.Before(pl.expiresAt) {
		return "", nil, errcode.New(errcode.PlanExpired, "the plan expired; plan again")
	}
	if err := locksFree(ctx, db.Read(), def.Spec.LockKeys); err != nil {
		return "", nil, err
	}
	if err := checkReplan(pl, out); err != nil {
		return "", nil, err
	}
	id := e.newID(now)
	created := formatTime(now)
	deadline := formatTime(now.Add(e.compositeTimeout(steps)))
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if err := uninstallingErr(ctx, tx); err != nil {
			return err
		}
		epochs, err := takeLocks(ctx, tx, id, def.Spec.LockKeys, true)
		if err != nil {
			return err
		}
		ej, err := json.Marshal(epochs)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 input_json, recovery_owner, lock_epochs, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, '', ?, ?, ?, ?, ?, ?, ?)`,
			id, pl.id, e.cfg.Base.HostID, def.Spec.Name, def.Spec.Version, string(def.Spec.Class), p.Actor,
			core.StateApplying, string(e.cfg.Redact(def.InputType, input)), core.OwnerComposite, string(ej),
			created, created); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO core_composites (change_id, direction, cursor, steps_deadline) VALUES (?, ?, 0, ?)",
			id, dirForward, deadline); err != nil {
			return err
		}
		for _, s := range steps {
			after, err := json.Marshal(nonNil(s.Step.After))
			if err != nil {
				return err
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, after_json,
				 input_template, state, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				id, s.Step.ID, s.Ordinal, s.Step.Host, s.Def.Ref().String(), string(after), string(s.Template),
				core.StepPending, created); err != nil {
				return err
			}
		}
		return nil
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return "", nil, err
	case err != nil:
		return "", nil, fmt.Errorf("lifecycle: persist the composite intent: %w", err)
	}
	return id, nil, nil
}

// isComposite reports whether changeID is a composite of this engine; an
// unknown change is not, and its own path answers change.not_found.
func (e *Engine) isComposite(ctx context.Context, changeID string) (bool, error) {
	var class string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx, "SELECT class FROM core_changes WHERE id = ?", changeID).Scan(&class)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	case err != nil:
		return false, fmt.Errorf("lifecycle: read change %s: %w", changeID, err)
	}
	return core.Class(class) == core.ClassComposite, nil
}

// compositeSecretType reports whether values of t carry a field tagged
// secret:"true" at any depth.
func compositeSecretType(t reflect.Type) bool {
	return compositeSecretIn(t, map[reflect.Type]bool{})
}

func compositeSecretIn(t reflect.Type, seen map[reflect.Type]bool) bool {
	for t != nil && (t.Kind() == reflect.Pointer || t.Kind() == reflect.Slice || t.Kind() == reflect.Array || t.Kind() == reflect.Map) {
		t = t.Elem()
	}
	if t == nil || t.Kind() != reflect.Struct || seen[t] {
		return false
	}
	seen[t] = true
	for i := range t.NumField() {
		f := t.Field(i)
		if f.Tag.Get("secret") == "true" || compositeSecretIn(f.Type, seen) {
			return true
		}
	}
	return false
}

// confirmComposite confirms a composite awaiting confirmation through its
// revertible child (1.3 Confirmation): the step is confirming, committed,
// before the confirm is sent, and the lookup, never a deadline, settles an
// answer that was lost.
func (e *Engine) confirmComposite(ctx context.Context, p core.Principal, id string) (core.ChangeView, error) {
	ctx = context.WithoutCancel(ctx)
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return core.ChangeView{}, err
	}
	if c.State != core.StateAwaitingConfirmation || c.Cursor < 0 || c.Cursor >= len(steps) {
		return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", id, c.State))
	}
	s := steps[c.Cursor]
	if s.State == core.StepApplied {
		if err := e.setStep(ctx, id, s.ID, []string{core.StepApplied}, stepUpdate{State: core.StepConfirming}); err != nil {
			return core.ChangeView{}, err
		}
	}
	kids, err := e.kids()
	if err != nil {
		return core.ChangeView{}, err
	}
	if _, err := kids.Confirm(ctx, p, s.ChildID); err != nil {
		e.cfg.Base.Log.Warn("lifecycle: confirm a composite's child", "change", id, "child", s.ChildID, "error", err)
	}
	if err := e.settleComposite(ctx, id, &compositeRun{P: p}); err != nil {
		return core.ChangeView{}, err
	}
	e.wakeComposite(id)
	return e.Change(ctx, id)
}

// settleComposite looks the cursor step's child up and applies the tables
// once, completing the composite when its last step became final. A child
// that cannot be looked up now is left to the runner.
func (e *Engine) settleComposite(ctx context.Context, id string, run *compositeRun) error {
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return err
	}
	kids, err := e.kids()
	if err != nil {
		return err
	}
	lk, err := lookupChild(ctx, kids, steps[c.Cursor])
	if err != nil {
		return nil
	}
	err = e.observeChild(ctx, id, lk)
	var wait *compositeWait
	switch {
	case errors.As(err, &wait), errors.Is(err, errCompositeDone):
		return nil
	case err != nil:
		return err
	}
	if c, steps, err = loadComposite(ctx, e.cfg.Base.DB.Read(), id); err != nil {
		return err
	}
	if c.Owner == core.OwnerComposite && c.Direction == dirForward && c.Cursor >= len(steps) {
		if err := e.completeComposite(ctx, c, steps, run); err != nil && !errors.Is(err, errCompositeDone) {
			return err
		}
	}
	return nil
}

// revertComposite reverts a composite awaiting confirmation (1.3 States):
// its unwind starts at the revertible step, whose child p reverts now; the
// runner undoes the steps before it.
func (e *Engine) revertComposite(ctx context.Context, p core.Principal, id string) (core.ChangeView, error) {
	ctx = context.WithoutCancel(ctx)
	unlock := e.compositeLock(id)
	defer unlock()
	c, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		return core.ChangeView{}, err
	}
	if c.State != core.StateAwaitingConfirmation || c.Cursor < 0 || c.Cursor >= len(steps) {
		return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", id, c.State))
	}
	s := steps[c.Cursor]
	if err := e.compositeCause(ctx, id, stepFailure(s, "reverted by "+p.Actor)); err != nil {
		return core.ChangeView{}, err
	}
	var wait *compositeWait
	if err := e.unwindFrom(ctx, id, s.Ordinal); err != nil && !errors.Is(err, errCompositeDone) && !errors.As(err, &wait) {
		return core.ChangeView{}, err
	}
	if c, steps, err = loadComposite(ctx, e.cfg.Base.DB.Read(), id); err != nil {
		return core.ChangeView{}, err
	}
	if c.State == core.StateUndoing && c.Cursor == s.Ordinal {
		s = steps[c.Cursor]
		if err := e.setStep(ctx, id, s.ID, []string{s.State}, stepUpdate{State: core.StepUndoing}); err != nil {
			return core.ChangeView{}, err
		}
		switch err := e.sendRevert(ctx, c, s, p); {
		case err == nil, errors.Is(err, errCompositeDone), errors.As(err, &wait):
		case errors.Is(err, errChildRevert):
			// The unwind is recorded; the runner retries the revert.
			e.cfg.Base.Log.Warn("lifecycle: composite revert of a child refused", "change", id, "error", err)
		default:
			return core.ChangeView{}, err
		}
	}
	e.wakeComposite(id)
	return e.Change(ctx, id)
}

// compositeFinal tells the executing side of every child of composite id,
// now terminal, that its composite is terminal: a final AckPrivate, after
// which the child's made effects drop their sealed secrets (spec 1.2). A
// child that cannot be told keeps its secrets until its undo window closes
// and Purge clears them, so a failure is only logged.
func (e *Engine) compositeFinal(id string) {
	ctx, cancel := context.WithTimeout(e.runCtx, CleanupTxTimeout)
	defer cancel()
	kids, err := e.kids()
	if err != nil {
		e.cfg.Base.Log.Warn("lifecycle: tell a terminal composite's children", "change", id, "error", err)
		return
	}
	_, steps, err := loadComposite(ctx, e.cfg.Base.DB.Read(), id)
	if err != nil {
		e.cfg.Base.Log.Warn("lifecycle: tell a terminal composite's children", "change", id, "error", err)
		return
	}
	for _, s := range steps {
		for _, child := range append([]string{s.ChildID}, s.Earlier...) {
			if child == "" {
				continue
			}
			if err := kids.AckPrivate(ctx, s.Host, child, true); err != nil {
				e.cfg.Base.Log.Warn("lifecycle: tell a child its composite is terminal",
					"change", id, "step", s.ID, "child", child, "error", err)
			}
		}
	}
}

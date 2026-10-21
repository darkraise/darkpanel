package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// PlanTTL is how long a plan can be applied after it is made (ADR 0004).
const PlanTTL = 10 * time.Minute

// Change listings return defaultChangeLimit changes unless asked for fewer,
// and never more than maxChangeLimit.
const (
	defaultChangeLimit = 100
	maxChangeLimit     = 500
)

// Config wires an engine to one side. Base.DB, Base.Sealer, Base.Clock and
// Base.Log are the engine's own; Base.HostID is the agent's host id, empty on
// the controller. Every handler call receives Base with Principal set to the
// caller.
type Config struct {
	Side      core.Side
	Registry  *core.Registry
	Validator core.Validator
	Journal   core.Journal
	// Redact returns raw, a JSON value of type t, with every secret field
	// replaced by "[redacted]".
	Redact func(t reflect.Type, raw json.RawMessage) json.RawMessage
	Base   core.Env
	// SyncAnswer is how long Apply waits for a synchronous action before it
	// answers the applying change; zero means SyncAnswer.
	SyncAnswer time.Duration
}

// Deadlines of spec 1.3.
const (
	// DefaultApplyTimeout is the budget of an action that declares no
	// ApplyTimeout, shared by its Apply and Verify.
	DefaultApplyTimeout = 30 * time.Second
	// SyncAnswer is how long Apply waits for a synchronous action. It stays
	// below the API servers' 60 s write timeout, so the answer always fits.
	SyncAnswer = 50 * time.Second
	// CleanupTxTimeout bounds a recording transaction that runs after the
	// action's own deadline.
	CleanupTxTimeout = 30 * time.Second
	// RecoveryVerifyBound is how long startup recovery waits for a Verify.
	RecoveryVerifyBound = 30 * time.Second
)

// Engine runs the change lifecycle of one side over its database. Its methods
// are safe for concurrent use.
type Engine struct {
	cfg Config

	// runCtx is the context of every in-process apply; Close cancels it, so
	// only shutdown cancels a running handler, never a client's request.
	runCtx  context.Context
	stopRun context.CancelFunc
	runs    sync.WaitGroup // goRun goroutines

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
	idMs    uint64 // no id is stamped below this millisecond

	// applyMu is held from apply's checks until its intent is persisted, so no
	// other apply takes a lock or the same plan in between and Purge deletes no
	// plan.
	applyMu sync.Mutex

	timerMu  sync.Mutex
	timers   map[string]core.Timer // armed revert timers by change id
	closed   bool                  // Close ran: no timer is armed and goRun runs nothing in the background
	inflight sync.WaitGroup        // timer reverts that are running
}

// New returns an engine for cfg. Run Recover before serving requests.
func New(cfg Config) (*Engine, error) {
	if cfg.Side != core.SideController && cfg.Side != core.SideAgent {
		return nil, fmt.Errorf("lifecycle: unknown side %q", cfg.Side)
	}
	required := []struct {
		name string
		set  bool
	}{
		{"Registry", cfg.Registry != nil},
		{"Validator", cfg.Validator != nil},
		{"Journal", cfg.Journal != nil},
		{"Redact", cfg.Redact != nil},
		{"Base.DB", cfg.Base.DB != nil},
		{"Base.Sealer", cfg.Base.Sealer != nil},
		{"Base.Clock", cfg.Base.Clock != nil},
		{"Base.Log", cfg.Base.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return nil, fmt.Errorf("lifecycle: Config.%s is required", r.name)
		}
	}
	if cfg.SyncAnswer == 0 {
		cfg.SyncAnswer = SyncAnswer
	}
	runCtx, stopRun := context.WithCancel(context.Background())
	return &Engine{
		cfg: cfg, runCtx: runCtx, stopRun: stopRun,
		entropy: ulid.Monotonic(rand.Reader, 0), timers: map[string]core.Timer{},
	}, nil
}

// Close stops every armed revert timer and arms no new one. A change whose
// timer was stopped stays pending until the next start's Recover reverts it.
// Close then cancels every in-process apply, as process shutdown does, and
// waits for each to record its result and for a running timer revert.
func (e *Engine) Close() {
	e.timerMu.Lock()
	e.closed = true
	for id, t := range e.timers {
		t.Stop()
		delete(e.timers, id)
	}
	// Unlock before waiting: a running revert takes timerMu in stopTimer.
	e.timerMu.Unlock()
	e.stopRun()
	e.inflight.Wait()
	e.runs.Wait()
}

// goRun runs f on a goroutine that Close waits for. After Close it runs f on
// the caller's goroutine instead, where f finds the run context cancelled.
func (e *Engine) goRun(f func()) {
	e.timerMu.Lock()
	if e.closed {
		e.timerMu.Unlock()
		f()
		return
	}
	e.runs.Add(1)
	e.timerMu.Unlock()
	go func() {
		defer e.runs.Done()
		f()
	}()
}

// applyTimeout is def's budget for Apply and Verify together.
func (e *Engine) applyTimeout(def core.ActionDef) time.Duration {
	if def.Spec.ApplyTimeout > 0 {
		return def.Spec.ApplyTimeout
	}
	return DefaultApplyTimeout
}

// cleanup is the context of a recording transaction after a handler call: it
// outlives the run context's cancellation, bounded by CleanupTxTimeout.
func (e *Engine) cleanup() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(e.runCtx), CleanupTxTimeout)
}

// cleanupCall is the context of a handler call made after the apply ended,
// such as a revert: it outlives the run context's cancellation, bounded by
// def's ApplyTimeout.
func (e *Engine) cleanupCall(def core.ActionDef) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(e.runCtx), e.applyTimeout(def))
}

// newID returns a ULID stamped with now, or with the newest id's millisecond
// when the clock has stepped back since. Ids made by one engine, and after
// Recover's seed by every engine on the database, sort in creation order,
// which Changes pages by.
func (e *Engine) newID(now time.Time) string {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	e.idMs = max(e.idMs, ulid.Timestamp(now))
	return ulid.MustNew(e.idMs, e.entropy).String()
}

// seedIDs makes every id newID returns sort after each stored plan and change
// id, even when the clock stepped back across a restart: the next id is
// stamped at least one millisecond after the newest stored one. Ids that are
// not ULIDs (26 characters) are ignored; the engine never made them.
func (e *Engine) seedIDs(ctx context.Context) error {
	var newest sql.NullString
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT max(id) FROM (
		 SELECT max(id) AS id FROM core_plans WHERE length(id) = 26
		 UNION ALL SELECT max(id) FROM core_changes WHERE length(id) = 26)`).Scan(&newest)
	if err != nil {
		return fmt.Errorf("lifecycle: read the newest id: %w", err)
	}
	if !newest.Valid {
		return nil
	}
	if id, err := ulid.ParseStrict(newest.String); err == nil {
		e.idMu.Lock()
		defer e.idMu.Unlock()
		e.idMs = max(e.idMs, id.Time()+1)
	}
	return nil
}

func (e *Engine) env(p core.Principal) core.Env {
	env := e.cfg.Base
	env.Principal = p
	return env
}

// action returns a registered action of this engine's side.
func (e *Engine) action(ref core.ActionRef) (core.ActionDef, error) {
	def, ok := e.cfg.Registry.Action(ref)
	if !ok || def.Spec.Side != e.cfg.Side {
		return core.ActionDef{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", ref))
	}
	return def, nil
}

// Read runs a read action and returns its output with secret fields redacted:
// a secret output is returned only by the apply that created it (spec
// section 5). Reads are not journaled; they change nothing.
func (e *Engine) Read(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	def, err := e.action(ref)
	if err != nil {
		return nil, err
	}
	if def.Spec.Class != core.ClassRead {
		return nil, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is not a read action", ref))
	}
	if err := e.cfg.Validator.ValidateInput(def, input); err != nil {
		return nil, err
	}
	var out json.RawMessage
	err = e.protect(ref, "Read", func() error {
		var err error
		out, err = def.Handler.Read(ctx, e.env(p), input)
		return err
	})
	if err != nil {
		return nil, err
	}
	return e.cfg.Redact(def.OutputType, out), nil
}

// Plan validates input, runs the handler's Plan and stores the plan with its
// canonical input sealed (spec section 3). The plan expires after PlanTTL.
func (e *Engine) Plan(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage, _ core.PlanOptions) (core.PlanView, error) {
	def, err := e.action(ref)
	if err != nil {
		return core.PlanView{}, err
	}
	if def.Spec.Class == core.ClassRead {
		return core.PlanView{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is a read action and has no plan", ref))
	}
	canonical, err := e.canonical(def, input)
	if err != nil {
		return core.PlanView{}, err
	}
	var out core.PlanOutcome
	err = e.protect(ref, "Plan", func() error {
		var err error
		out, err = def.Handler.Plan(ctx, e.env(p), canonical)
		return err
	})
	if err != nil {
		return core.PlanView{}, err
	}
	if def.Spec.Class != core.ClassIrreversible {
		out.Confirmation = ""
	} else if out.Confirmation == "" {
		return core.PlanView{}, fmt.Errorf("lifecycle: %s returned no confirmation phrase", ref)
	}
	// One reading: the id may be stamped later than now, but expiry and
	// created_at stay on the clock.
	now := e.cfg.Base.Clock.Now()
	v := core.PlanView{
		PlanID: e.newID(now), HostID: e.cfg.Base.HostID, Action: def.Spec.Name, Version: def.Spec.Version,
		InputHash: e.inputHash(def, canonical), Confirmation: out.Confirmation,
		Diff: nonNil(out.Diff), Guards: nonNil(out.Guards), ExpiresAt: now.Add(PlanTTL),
	}
	sealed, err := e.cfg.Base.Sealer.Seal(planInputAD(v.PlanID), canonical)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: seal plan input: %w", err)
	}
	diff, err := json.Marshal(v.Diff)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: encode diff: %w", err)
	}
	guards, err := json.Marshal(v.Guards)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: encode guards: %w", err)
	}
	created := formatTime(now)
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_plans (id, host_id, action, version, actor, input_ct, input_hash, state_hash,
			 diff_json, guards_json, confirmation, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.PlanID, v.HostID, v.Action, v.Version, p.Actor, sealed, v.InputHash, out.StateHash,
			string(diff), string(guards), v.Confirmation, created, formatTime(v.ExpiresAt))
		return err
	})
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: store plan: %w", err)
	}
	return v, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// canonical validates raw and re-encodes it from the typed input, so equal
// inputs have equal bytes whatever their field order.
func (e *Engine) canonical(def core.ActionDef, raw json.RawMessage) (json.RawMessage, error) {
	if err := e.cfg.Validator.ValidateInput(def, raw); err != nil {
		return nil, err
	}
	v := reflect.New(def.InputType)
	if err := json.Unmarshal(raw, v.Interface()); err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "input does not match the action's input type")
	}
	b, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode input: %w", err)
	}
	return b, nil
}

// inputHash is SHA-256 over the canonical input with its secret fields
// redacted, so the hash the API and audit expose reveals nothing about a
// secret (spec section 3).
func (e *Engine) inputHash(def core.ActionDef, canonical json.RawMessage) string {
	sum := sha256.Sum256(e.cfg.Redact(def.InputType, canonical))
	return hex.EncodeToString(sum[:])
}

// planInputAD is the associated data of a plan's sealed input (ADR 0005).
func planInputAD(planID string) string { return "core_plans.input_ct." + planID }

const changeColumns = `id, host_id, action, version, class, compensated_by, actor, state,
	revert_deadline, output_json, error_code, error_message, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanChange(s scanner) (core.ChangeView, error) {
	var v core.ChangeView
	var class, created, updated string
	var deadline, output, code, msg sql.NullString
	if err := s.Scan(&v.ID, &v.HostID, &v.Action, &v.Version, &class, &v.CompensatedBy, &v.Actor, &v.State,
		&deadline, &output, &code, &msg, &created, &updated); err != nil {
		return v, err
	}
	v.Class = core.Class(class)
	var err error
	if v.CreatedAt, err = parseTime(created); err != nil {
		return v, err
	}
	if v.UpdatedAt, err = parseTime(updated); err != nil {
		return v, err
	}
	if deadline.Valid {
		t, err := parseTime(deadline.String)
		if err != nil {
			return v, err
		}
		v.RevertDeadline = &t
	}
	if output.Valid {
		v.Output = json.RawMessage(output.String)
	}
	if code.Valid {
		v.Error = &core.ChangeError{Code: code.String, Message: msg.String}
	}
	return v, nil
}

// Change returns one change; secret output fields are redacted.
func (e *Engine) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	v, err := scanChange(e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID))
	if errors.Is(err, sql.ErrNoRows) {
		return v, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	}
	if err != nil {
		return v, fmt.Errorf("lifecycle: read change: %w", err)
	}
	return v, nil
}

// Changes lists changes newest first. Before is a change id: only older
// changes are listed. A Limit of 0 or less means 100; at most 500 are listed.
func (e *Engine) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	query := "SELECT " + changeColumns + " FROM core_changes WHERE 1 = 1"
	var args []any
	if f.State != "" {
		query += " AND state = ?"
		args = append(args, f.State)
	}
	if f.HostID != "" {
		query += " AND host_id = ?"
		args = append(args, f.HostID)
	}
	if f.Before != "" {
		query += " AND id < ?"
		args = append(args, f.Before)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultChangeLimit
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, min(limit, maxChangeLimit))
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: list changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []core.ChangeView{}
	for rows.Next() {
		v, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: list changes: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

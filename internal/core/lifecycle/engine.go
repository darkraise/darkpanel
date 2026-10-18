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
}

// Engine runs the change lifecycle of one side over its database. Its methods
// are safe for concurrent use.
type Engine struct {
	cfg           Config
	verifyTimeout time.Duration // VerifyTimeout; tests shorten it

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy

	// applyMu is held from apply's checks until its intent is persisted, so no
	// other apply takes a lock or the same plan in between and Purge deletes no
	// plan.
	applyMu sync.Mutex

	timerMu  sync.Mutex
	timers   map[string]core.Timer // armed revert timers by change id
	closed   bool
	inflight sync.WaitGroup // timer reverts that are running
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
	return &Engine{
		cfg: cfg, verifyTimeout: VerifyTimeout,
		entropy: ulid.Monotonic(rand.Reader, 0), timers: map[string]core.Timer{},
	}, nil
}

// Close stops every armed revert timer and arms no new one. A change whose
// timer was stopped stays pending until the next start's Recover reverts it.
// Close also waits for a timer revert that is already running to finish.
func (e *Engine) Close() {
	e.timerMu.Lock()
	e.closed = true
	for id, t := range e.timers {
		t.Stop()
		delete(e.timers, id)
	}
	// Unlock before waiting: a running revert takes timerMu in stopTimer.
	e.timerMu.Unlock()
	e.inflight.Wait()
}

// newID returns a ULID stamped with the engine's clock; ids made by one
// engine sort in creation order.
func (e *Engine) newID() string {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(e.cfg.Base.Clock.Now()), e.entropy).String()
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
func (e *Engine) Plan(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (core.PlanView, error) {
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
	v := core.PlanView{
		PlanID: e.newID(), HostID: e.cfg.Base.HostID, Action: def.Spec.Name, Version: def.Spec.Version,
		InputHash: e.inputHash(def, canonical), Confirmation: out.Confirmation,
		Diff: nonNil(out.Diff), Guards: nonNil(out.Guards), ExpiresAt: e.cfg.Base.Clock.Now().Add(PlanTTL),
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
	created := formatTime(e.cfg.Base.Clock.Now())
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

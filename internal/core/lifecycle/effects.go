package lifecycle

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// queryer is a *sql.DB or a *sql.Tx.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// effectResourcePattern is "<module>.<noun>:<identity>" (spec 1.1 and 1.2).
var effectResourcePattern = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*:.+$`)

// effectNext lists, per state, the states an effect may move to (spec 1.2,
// effect states). Every non-terminal state may also become abandoned.
var effectNext = map[core.EffectState][]core.EffectState{
	core.EffectIntended:   {core.EffectMade, core.EffectSkipped, core.EffectUnresolved, core.EffectAbandoned},
	core.EffectMade:       {core.EffectUnresolved, core.EffectUndoing, core.EffectAbandoned},
	core.EffectUndoing:    {core.EffectUndone, core.EffectMade, core.EffectUnresolved, core.EffectAbandoned},
	core.EffectUnresolved: {core.EffectAccepted, core.EffectMade, core.EffectSkipped, core.EffectAbandoned},
}

// effectTerminal reports whether s ends an effect.
func effectTerminal(s core.EffectState) bool {
	switch s {
	case core.EffectSkipped, core.EffectUndone, core.EffectAccepted, core.EffectAbandoned:
		return true
	}
	return false
}

// effectSecretAD is the associated data of an effect's sealed secret (ADR 0005).
func effectSecretAD(changeID string, seq int) string {
	return fmt.Sprintf("core_effects.secret_ct.%s.%d", changeID, seq)
}

// changeEnv is the Env of every handler call of a change: the engine's base,
// the caller, the change and its call context, and the change's effects
// journal.
func (e *Engine) changeEnv(p core.Principal, def core.ActionDef, changeID string, call core.CallContext) core.Env {
	env := e.env(p)
	env.ChangeID = changeID
	env.Call = call
	env.Effects = &effectJournal{e: e, def: def, changeID: changeID}
	return env
}

// effectJournal is core.EffectJournal over core_effects for one change. Each
// method is one committed write, so a journaled effect is durable before the
// call it announces is made.
type effectJournal struct {
	e        *Engine
	def      core.ActionDef
	changeID string
}

// Intend records eff as intended. The engine assigns ChangeID, Seq, State
// and CommitBy and ignores what the handler set there and in the fields only
// the engine writes.
func (j *effectJournal) Intend(ctx context.Context, eff core.Effect, secret []byte) (int, error) {
	if j.def.Handler.Observe == nil {
		return 0, fmt.Errorf("lifecycle: %s journals an effect but has no Observer", j.def.Ref())
	}
	if err := checkIntended(eff); err != nil {
		return 0, fmt.Errorf("lifecycle: %s: %w", j.def.Ref(), err)
	}
	recovery, err := nullableJSON(eff.Recovery)
	if err != nil {
		return 0, err
	}
	inverseRecovery, err := nullableJSON(eff.InverseRecovery)
	if err != nil {
		return 0, err
	}
	before, err := json.Marshal(eff.Before)
	if err != nil {
		return 0, fmt.Errorf("lifecycle: encode effect before: %w", err)
	}
	after, err := nullableJSON(eff.After)
	if err != nil {
		return 0, err
	}
	now := j.e.cfg.Base.Clock.Now()
	var commitBy any
	if !eff.Local {
		commitBy = formatTime(now.Add(eff.CommitWait))
	}
	var seq int
	err = j.e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			"SELECT coalesce(max(seq), 0) + 1 FROM core_effects WHERE change_id = ?", j.changeID).Scan(&seq); err != nil {
			return err
		}
		var sealed []byte
		if len(secret) > 0 {
			var err error
			if sealed, err = j.e.cfg.Base.Sealer.Seal(effectSecretAD(j.changeID, seq), secret); err != nil {
				return fmt.Errorf("seal effect secret: %w", err)
			}
		}
		created := formatTime(now)
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_effects (change_id, seq, kind, resource, recovery_json, local, inverse_local,
			 commit_wait_ns, commit_by, inverse_recovery_json, before_json, after_json, state, secret_ct,
			 created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			j.changeID, seq, string(eff.Kind), eff.Resource, recovery, eff.Local, eff.InverseLocal,
			int64(eff.CommitWait), commitBy, inverseRecovery, string(before), after,
			string(core.EffectIntended), sealed, created, created)
		return err
	})
	if err != nil {
		return 0, fmt.Errorf("lifecycle: intend effect of change %s: %w", j.changeID, err)
	}
	return seq, nil
}

// checkIntended validates what a handler declares at Intend.
func checkIntended(eff core.Effect) error {
	switch eff.Kind {
	case core.EffectCreate, core.EffectModify, core.EffectDelete:
	default:
		return fmt.Errorf("effect kind %q, want create, modify or delete", eff.Kind)
	}
	if !effectResourcePattern.MatchString(eff.Resource) {
		return fmt.Errorf("effect resource %q, want <module>.<noun>:<identity>", eff.Resource)
	}
	noun, identity, _ := strings.Cut(eff.Resource, ":")
	switch {
	case eff.Kind == core.EffectCreate && eff.Recovery == nil:
		return errors.New("a creation declares its Recovery identity")
	case eff.Kind != core.EffectCreate && eff.Recovery != nil:
		return errors.New("only a creation declares a Recovery identity")
	case eff.Kind == core.EffectCreate && eff.Resource != noun+":?"+eff.Recovery.Value:
		return fmt.Errorf("a creation's resource is %s:?<recovery value>, not %s", noun, eff.Resource)
	case eff.Kind != core.EffectCreate && strings.HasPrefix(identity, "?"):
		return fmt.Errorf("resource %s names an object not yet created", eff.Resource)
	case eff.CommitWait < 0:
		return errors.New("a negative CommitWait")
	}
	for _, id := range []*core.RecoveryID{eff.Recovery, eff.InverseRecovery} {
		if id != nil && (!slices.Contains([]string{"name", "marker", "idempotency"}, id.Kind) || id.Value == "") {
			return fmt.Errorf("recovery identity %+v, want kind name, marker or idempotency and a value", *id)
		}
	}
	if err := checkDoc("Before", &eff.Before); err != nil {
		return err
	}
	if eff.Kind == core.EffectCreate && !bytes.Equal(bytes.TrimSpace(eff.Before.State), []byte("null")) {
		return errors.New(`a creation's Before is {"state": null}`)
	}
	return checkDoc("After", eff.After)
}

// checkDoc refuses a StateDoc whose State is missing or either part is not
// JSON. A nil doc passes.
func checkDoc(name string, d *core.StateDoc) error {
	switch {
	case d == nil:
		return nil
	case !json.Valid(d.State):
		return fmt.Errorf("effect %s.State is not a JSON value", name)
	case len(d.Version) > 0 && !json.Valid(d.Version):
		return fmt.Errorf("effect %s.Version is not a JSON value", name)
	}
	return nil
}

// nullableJSON encodes v, storing a nil pointer as NULL.
func nullableJSON[T any](v *T) (any, error) {
	if v == nil {
		return nil, nil
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode %T: %w", v, err)
	}
	return string(b), nil
}

// Made records that the call of effect seq succeeded: resource names the
// object it acted on and post is its state now.
func (j *effectJournal) Made(ctx context.Context, seq int, resource string, post core.StateDoc) error {
	if _, identity, _ := strings.Cut(resource, ":"); !effectResourcePattern.MatchString(resource) || strings.HasPrefix(identity, "?") {
		return fmt.Errorf("lifecycle: %s: made resource %q, want <module>.<noun>:<identity>", j.def.Ref(), resource)
	}
	if err := checkDoc("Post", &post); err != nil {
		return fmt.Errorf("lifecycle: %s: %w", j.def.Ref(), err)
	}
	return j.write(ctx, seq, effectUpdate{State: core.EffectMade, Resource: &resource, Post: &post})
}

// Skipped records that the call of effect seq was not made or committed
// nothing.
func (j *effectJournal) Skipped(ctx context.Context, seq int) error {
	return j.write(ctx, seq, effectUpdate{State: core.EffectSkipped})
}

func (j *effectJournal) write(ctx context.Context, seq int, u effectUpdate) error {
	err := j.e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		return setEffect(ctx, tx, j.e.cfg.Base.Clock.Now(), j.changeID, seq, []core.EffectState{core.EffectIntended}, u)
	})
	if err != nil {
		return fmt.Errorf("lifecycle: record effect %d of change %s: %w", seq, j.changeID, err)
	}
	return nil
}

// List returns the change's effects in seq order.
func (j *effectJournal) List(ctx context.Context) ([]core.Effect, error) {
	return loadEffects(ctx, j.e.cfg.Base.DB.Read(), j.changeID)
}

// Secret opens the sealed secret of effect seq. An effect without one, or
// whose secret was cleared, answers an error.
func (j *effectJournal) Secret(ctx context.Context, seq int) ([]byte, error) {
	var ct []byte
	err := j.e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT secret_ct FROM core_effects WHERE change_id = ? AND seq = ?", j.changeID, seq).Scan(&ct)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("lifecycle: change %s has no effect %d", j.changeID, seq)
	case err != nil:
		return nil, fmt.Errorf("lifecycle: read secret of effect %d of change %s: %w", seq, j.changeID, err)
	case ct == nil:
		return nil, fmt.Errorf("lifecycle: effect %d of change %s holds no secret", seq, j.changeID)
	}
	secret, err := j.e.cfg.Base.Sealer.Open(effectSecretAD(j.changeID, seq), ct)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: open secret of effect %d of change %s: %w", seq, j.changeID, err)
	}
	return secret, nil
}

// effectUpdate is one engine-written transition of an effect. Nil pointers
// and empty strings keep the stored value, except Reason, which is always
// written: only an unresolved effect has one.
type effectUpdate struct {
	State                  core.EffectState
	Reason                 string
	Resource               *string
	Post, UndoTo, Current  *core.StateDoc
	CommitBy               *time.Time
	UndoResource           *string
	Resolution, ResolvedBy string
	ResolvedAt             *time.Time
}

// setEffect writes an engine transition inside tx and refuses one 1.2's table
// does not allow; a terminal state clears secret_ct. The effect must be in
// one of from.
func setEffect(ctx context.Context, tx *sql.Tx, now time.Time, changeID string, seq int, from []core.EffectState, u effectUpdate) error {
	var cur string
	err := tx.QueryRowContext(ctx, "SELECT state FROM core_effects WHERE change_id = ? AND seq = ?", changeID, seq).Scan(&cur)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("lifecycle: change %s has no effect %d", changeID, seq)
	case err != nil:
		return err
	}
	state := core.EffectState(cur)
	switch {
	case !slices.Contains(from, state) || !slices.Contains(effectNext[state], u.State):
		return fmt.Errorf("lifecycle: effect %d of change %s cannot move from %s to %s", seq, changeID, state, u.State)
	case (u.State == core.EffectUnresolved) != (u.Reason != ""):
		return fmt.Errorf("lifecycle: effect %d of change %s: only an unresolved effect has a reason", seq, changeID)
	}
	docs := make([]any, 3)
	for i, d := range []*core.StateDoc{u.Post, u.UndoTo, u.Current} {
		if docs[i], err = nullableJSON(d); err != nil {
			return err
		}
	}
	var commitBy, resolvedAt, resolution, resolvedBy any
	if u.CommitBy != nil {
		commitBy = formatTime(*u.CommitBy)
	}
	if u.ResolvedAt != nil {
		resolvedAt = formatTime(*u.ResolvedAt)
	}
	if u.Resolution != "" {
		resolution, resolvedBy = u.Resolution, u.ResolvedBy
	}
	_, err = tx.ExecContext(ctx,
		`UPDATE core_effects SET state = ?, reason = ?, resource = coalesce(?, resource),
		 post_json = coalesce(?, post_json), undo_to_json = coalesce(?, undo_to_json),
		 current_json = coalesce(?, current_json), commit_by = coalesce(?, commit_by),
		 undo_resource = coalesce(?, undo_resource), resolution = coalesce(?, resolution),
		 resolved_by = coalesce(?, resolved_by), resolved_at = coalesce(?, resolved_at),
		 secret_ct = CASE WHEN ? THEN NULL ELSE secret_ct END, updated_at = ?
		 WHERE change_id = ? AND seq = ?`,
		string(u.State), u.Reason, u.Resource, docs[0], docs[1], docs[2], commitBy, u.UndoResource,
		resolution, resolvedBy, resolvedAt, effectTerminal(u.State), formatTime(now), changeID, seq)
	return err
}

const effectColumns = `change_id, seq, kind, resource, recovery_json, local, inverse_local, commit_wait_ns,
	commit_by, inverse_recovery_json, undo_resource, before_json, after_json, post_json, undo_to_json,
	current_json, state, reason, resolution, resolved_by, resolved_at`

// loadEffects returns a change's effects in seq order.
func loadEffects(ctx context.Context, q queryer, changeID string) ([]core.Effect, error) {
	rows, err := q.QueryContext(ctx, "SELECT "+effectColumns+" FROM core_effects WHERE change_id = ? ORDER BY seq", changeID)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: list effects of change %s: %w", changeID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []core.Effect{}
	for rows.Next() {
		eff, err := scanEffect(rows)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: list effects of change %s: %w", changeID, err)
		}
		out = append(out, eff)
	}
	return out, rows.Err()
}

func scanEffect(s scanner) (core.Effect, error) {
	var eff core.Effect
	var kind, state, before string
	var commitWait int64
	var recovery, commitBy, inverseRecovery, after, post, undoTo, current, resolvedAt sql.NullString
	if err := s.Scan(&eff.ChangeID, &eff.Seq, &kind, &eff.Resource, &recovery, &eff.Local, &eff.InverseLocal,
		&commitWait, &commitBy, &inverseRecovery, &eff.UndoResource, &before, &after, &post, &undoTo,
		&current, &state, &eff.Reason, &eff.Resolution, &eff.ResolvedBy, &resolvedAt); err != nil {
		return eff, err
	}
	eff.Kind, eff.State, eff.CommitWait = core.EffectKind(kind), core.EffectState(state), time.Duration(commitWait)
	if err := json.Unmarshal([]byte(before), &eff.Before); err != nil {
		return eff, err
	}
	for _, f := range []struct {
		raw sql.NullString
		dst any
	}{{recovery, &eff.Recovery}, {inverseRecovery, &eff.InverseRecovery}, {after, &eff.After},
		{post, &eff.Post}, {undoTo, &eff.UndoTo}, {current, &eff.Current}} {
		if !f.raw.Valid {
			continue
		}
		if err := json.Unmarshal([]byte(f.raw.String), f.dst); err != nil {
			return eff, err
		}
	}
	for _, f := range []struct {
		raw sql.NullString
		dst *time.Time
	}{{commitBy, &eff.CommitBy}, {resolvedAt, &eff.ResolvedAt}} {
		if !f.raw.Valid {
			continue
		}
		t, err := parseTime(f.raw.String)
		if err != nil {
			return eff, err
		}
		*f.dst = t
	}
	return eff, nil
}

// canonicalJSON re-encodes raw with object keys sorted, no insignificant
// whitespace and each number in one exact form, so two documents that mean
// the same compare equal as bytes. Numbers are not decoded through float64:
// a revision counter or nanosecond time above 2^53 would lose its low digits
// and two different versions would compare equal. Empty input is null.
func canonicalJSON(raw json.RawMessage) ([]byte, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return []byte("null"), nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, fmt.Errorf("lifecycle: canonical JSON: %w", err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, errors.New("lifecycle: canonical JSON: data after the value")
	}
	v, err := canonicalNumbers(v)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: canonical JSON: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: canonical JSON: %w", err)
	}
	return b, nil
}

// canonicalNumbers rewrites every number in v as the shortest decimal of its
// 512-bit value, so 1, 1.0 and 1e0 meet and integers stay exact.
func canonicalNumbers(v any) (any, error) {
	switch t := v.(type) {
	case json.Number:
		f, _, err := big.ParseFloat(t.String(), 10, 512, big.ToNearestEven)
		if err != nil {
			return nil, err
		}
		return json.Number(f.Text('g', -1)), nil
	case []any:
		for i := range t {
			n, err := canonicalNumbers(t[i])
			if err != nil {
				return nil, err
			}
			t[i] = n
		}
	case map[string]any:
		for k := range t {
			n, err := canonicalNumbers(t[k])
			if err != nil {
				return nil, err
			}
			t[k] = n
		}
	}
	return v, nil
}

// jsonEqual compares two JSON values canonically; a value that is not JSON
// equals nothing.
func jsonEqual(a, b json.RawMessage) bool {
	ca, errA := canonicalJSON(a)
	cb, errB := canonicalJSON(b)
	return errA == nil && errB == nil && bytes.Equal(ca, cb)
}

// docEqual is "Equal" of spec 1.2: State and Version equal.
func docEqual(a, b core.StateDoc) bool {
	return jsonEqual(a.State, b.State) && jsonEqual(a.Version, b.Version)
}

// stateEqual is "State-equal" of spec 1.2: State equal.
func stateEqual(a, b core.StateDoc) bool { return jsonEqual(a.State, b.State) }

// undoTo is the state an inverse must leave: Before with Version cleared, and
// for a creation the absence of the object.
func undoTo(eff core.Effect) core.StateDoc {
	if eff.Kind == core.EffectCreate {
		return core.StateDoc{State: json.RawMessage("null")}
	}
	return core.StateDoc{State: eff.Before.State}
}

// effectView is an effect as callers see it, never its secret.
func effectView(eff core.Effect) core.EffectView {
	v := core.EffectView{
		ChangeID: eff.ChangeID, Seq: eff.Seq, Kind: eff.Kind, Resource: eff.Resource, Local: eff.Local,
		InverseLocal: eff.InverseLocal, State: eff.State, Reason: eff.Reason, Before: eff.Before, After: eff.After,
		Post: eff.Post, UndoTo: eff.UndoTo, UndoResource: eff.UndoResource, Current: eff.Current,
		Resolution: eff.Resolution, ResolvedBy: eff.ResolvedBy,
	}
	if !eff.CommitBy.IsZero() {
		t := eff.CommitBy
		v.CommitBy = &t
	}
	if !eff.ResolvedAt.IsZero() {
		t := eff.ResolvedAt
		v.ResolvedAt = &t
	}
	return v
}

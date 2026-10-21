// Package audit is the controller's audit log over core_audit (spec section
// 11): the controller engine's journal, the login and logout records, the
// ingestion of agent journals and the listing behind core.audit.list.
package audit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// List returns DefaultListLimit entries unless asked for fewer, and never
// more than MaxListLimit.
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// correctly as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Store is the core.Audit of the controller database. Its methods are safe
// for concurrent use.
type Store struct {
	db    *store.DB
	clock core.Clock

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
}

var _ core.Audit = (*Store)(nil)

// New returns the audit log of the migrated controller database db. clock
// stamps entries that arrive without a time.
func New(db *store.DB, clock core.Clock) *Store {
	return &Store{db: db, clock: clock, entropy: ulid.Monotonic(rand.Reader, 0)}
}

// newID returns a ULID stamped with at, so ids sort like the events.
func (s *Store) newID(at time.Time) (string, error) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	id, err := ulid.New(ulid.Timestamp(at), s.entropy)
	if err != nil {
		return "", fmt.Errorf("audit: new id: %w", err)
	}
	return id.String(), nil
}

// insert writes e of journal epoch epoch inside tx. Empty strings, a zero
// seq and an empty input are stored as NULL; with skipDuplicate an agent row
// already stored is skipped.
func (s *Store) insert(ctx context.Context, tx *sql.Tx, e core.AuditEntry, epoch string, skipDuplicate bool) error {
	if e.At.IsZero() {
		e.At = s.clock.Now()
	}
	if e.ID == "" {
		id, err := s.newID(e.At)
		if err != nil {
			return err
		}
		e.ID = id
	}
	query := `INSERT INTO core_audit (id, seq, epoch, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if skipDuplicate {
		query += " ON CONFLICT (host_id, epoch, seq) DO NOTHING"
	}
	var seq any
	if e.Seq != 0 {
		seq = e.Seq
	}
	var input any
	if len(e.Input) > 0 {
		input = string(e.Input)
	}
	_, err := tx.ExecContext(ctx, query, e.ID, seq, epoch, e.At.UTC().Format(timeFormat), e.Actor, null(e.HostID),
		e.Action, e.Version, e.Phase, null(e.ChangeID), input, e.Result, null(e.ErrorCode), null(e.Source))
	if err != nil {
		return fmt.Errorf("audit: insert entry: %w", err)
	}
	return nil
}

// null stores an empty string as NULL.
func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// fromJournal is the audit row of a journal entry of hostID ("" for the
// controller).
func fromJournal(hostID string, e core.JournalEntry) core.AuditEntry {
	return core.AuditEntry{
		Actor: e.Actor, HostID: hostID, Action: e.Action, Phase: e.Phase, ChangeID: e.ChangeID,
		Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source, At: e.At, Version: e.Version, Seq: e.Seq,
		Input: e.Input,
	}
}

// Append records a controller-side change event inside the change's
// transaction, as the controller engine's core.Journal. The input is stored
// as given: the engine redacted it.
func (s *Store) Append(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	e.Seq = 0
	return s.insert(ctx, tx, fromJournal("", e), "", false)
}

// Record stores a login or logout (or any event without a change), keeping a
// supplied ID; an entry without one gets a ULID.
func (s *Store) Record(ctx context.Context, e core.AuditEntry) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error { return s.insert(ctx, tx, e, "", false) })
}

// Ingest stores agent journal entries of hostID in the empty journal epoch,
// as IngestEpoch does.
func (s *Store) Ingest(ctx context.Context, hostID string, entries []core.JournalEntry) error {
	return s.IngestEpoch(ctx, hostID, "", entries)
}

// IngestEpoch stores agent journal entries of hostID's journal epoch epoch in
// one transaction, skipping any host, epoch and sequence already stored.
func (s *Store) IngestEpoch(ctx context.Context, hostID, epoch string, entries []core.JournalEntry) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error { return s.ingest(ctx, tx, hostID, epoch, entries) })
}

func (s *Store) ingest(ctx context.Context, tx *sql.Tx, hostID, epoch string, entries []core.JournalEntry) error {
	for _, e := range entries {
		// insert stores a zero sequence as NULL, which the (host_id, epoch,
		// seq) index never deduplicates.
		if e.Seq <= 0 {
			return fmt.Errorf("audit: a journal entry of host %s has sequence %d; sequences start at 1", hostID, e.Seq)
		}
		if err := s.insert(ctx, tx, fromJournal(hostID, e), epoch, true); err != nil {
			return err
		}
	}
	return nil
}

// List returns entries newest first, filtered by the query's exact-match
// fields and by Before (ids below it). A Limit of 0 or less means
// DefaultListLimit, and at most MaxListLimit are returned. nextBefore is the
// last returned id when older matching entries remain, else empty.
func (s *Store) List(ctx context.Context, q core.AuditQuery) ([]core.AuditEntry, string, error) {
	query := `SELECT id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result,
		error_code, source FROM core_audit WHERE 1 = 1`
	var args []any
	for _, f := range []struct{ column, value string }{
		{"actor", q.Actor}, {"host_id", q.HostID}, {"action", q.Action},
	} {
		if f.value != "" {
			query += " AND " + f.column + " = ?"
			args = append(args, f.value)
		}
	}
	if q.Before != "" {
		query += " AND id < ?"
		args = append(args, q.Before)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("audit: list: %w", err)
	}
	out := []core.AuditEntry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, "", fmt.Errorf("audit: list: %w", err)
	}
	if len(out) > limit {
		out = out[:limit]
		return out, out[limit-1].ID, nil
	}
	return out, "", nil
}

func scan(rows *sql.Rows) (core.AuditEntry, error) {
	var e core.AuditEntry
	var seq sql.NullInt64
	var at string
	var host, change, input, code, source sql.NullString
	if err := rows.Scan(&e.ID, &seq, &at, &e.Actor, &host, &e.Action, &e.Version, &e.Phase, &change, &input,
		&e.Result, &code, &source); err != nil {
		return e, fmt.Errorf("audit: list: %w", err)
	}
	t, err := time.Parse(timeFormat, at)
	if err != nil {
		return e, fmt.Errorf("audit: stored time %q: %w", at, err)
	}
	e.At, e.Seq, e.HostID, e.ChangeID = t, seq.Int64, host.String, change.String
	e.ErrorCode, e.Source = code.String, source.String
	if input.Valid {
		e.Input = json.RawMessage(input.String)
	}
	return e, nil
}

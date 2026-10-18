// Package lifecycle is the change lifecycle engine of ADR 0004 (spec section
// 3): plans, apply, confirm, revert, recovery and the agent journal. Both
// sides use it, the agent runtime for agent actions and the controller for
// controller-side actions, each over its own database.
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
	"github.com/darkraise/darkpanel/internal/core/store"
)

// JournalCap is the most unacknowledged entries the agent journal keeps
// (spec section 11).
const JournalCap = 10000

// Journal phases and results (spec section 3).
const (
	phaseApply     = "apply"
	phaseConfirm   = "confirm"
	phaseRevert    = "revert"
	phaseRecover   = "recover"
	phaseInterrupt = "interrupt"
	resultOK       = "ok"
	resultError    = "error"
)

const actorJournal = "system:journal"

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// correctly as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("lifecycle: stored time %q: %w", s, err)
	}
	return t, nil
}

// nullJSON stores an empty document as NULL.
func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// AgentJournal is the agent's core.Journal over core_journal. The controller
// reads it with Entries and acknowledges what it stored with Ack (ADR 0003).
type AgentJournal struct {
	db  *store.DB
	cap int
}

// NewAgentJournal returns the journal of the agent database db.
func NewAgentJournal(db *store.DB) *AgentJournal {
	return &AgentJournal{db: db, cap: JournalCap}
}

type overflowInput struct {
	Dropped int `json:"dropped"`
}

// Append writes e inside the change's transaction. When the journal is full,
// the oldest entries are dropped and a single overflow entry, replacing any
// earlier one, counts every entry dropped since the controller last
// acknowledged one.
func (j *AgentJournal) Append(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM core_journal").Scan(&n); err != nil {
		return fmt.Errorf("lifecycle: count journal: %w", err)
	}
	if n >= j.cap {
		if err := j.overflow(ctx, tx, n, e.At); err != nil {
			return err
		}
	}
	return insertEntry(ctx, tx, e)
}

// overflow makes room for the new entry and an overflow entry; n is the
// current number of entries.
func (j *AgentJournal) overflow(ctx context.Context, tx *sql.Tx, n int, at time.Time) error {
	var dropped overflowInput
	var seq int64
	var input string
	err := tx.QueryRowContext(ctx,
		"SELECT seq, input_json FROM core_journal WHERE error_code = ? ORDER BY seq DESC LIMIT 1",
		string(errcode.JournalOverflow)).Scan(&seq, &input)
	switch {
	case err == nil:
		if err := json.Unmarshal([]byte(input), &dropped); err != nil {
			return fmt.Errorf("lifecycle: read overflow entry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM core_journal WHERE seq = ?", seq); err != nil {
			return fmt.Errorf("lifecycle: replace overflow entry: %w", err)
		}
		n--
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("lifecycle: read overflow entry: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		"DELETE FROM core_journal WHERE seq IN (SELECT seq FROM core_journal ORDER BY seq LIMIT ?)", n-j.cap+2)
	if err != nil {
		return fmt.Errorf("lifecycle: drop journal entries: %w", err)
	}
	k, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("lifecycle: drop journal entries: %w", err)
	}
	dropped.Dropped += int(k)
	raw, err := json.Marshal(dropped)
	if err != nil {
		return fmt.Errorf("lifecycle: overflow entry: %w", err)
	}
	return insertEntry(ctx, tx, core.JournalEntry{
		At: at, Actor: actorJournal, Phase: phaseInterrupt, Input: raw,
		Result: resultError, ErrorCode: string(errcode.JournalOverflow),
	})
}

func insertEntry(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO core_journal (at, actor, action, version, phase, change_id, input_json, result, error_code, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(e.At), e.Actor, e.Action, e.Version, e.Phase, e.ChangeID, nullJSON(e.Input), e.Result, e.ErrorCode,
		e.Source)
	if err != nil {
		return fmt.Errorf("lifecycle: append journal: %w", err)
	}
	return nil
}

// Entries returns up to limit entries after afterSeq in sequence order; a
// limit of 0 or less means 1000.
func (j *AgentJournal) Entries(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := j.db.Read().QueryContext(ctx,
		`SELECT seq, at, actor, action, version, phase, change_id, input_json, result, error_code, source
		 FROM core_journal WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: read journal: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.JournalEntry
	for rows.Next() {
		var e core.JournalEntry
		var at string
		var input sql.NullString
		if err := rows.Scan(&e.Seq, &at, &e.Actor, &e.Action, &e.Version, &e.Phase, &e.ChangeID,
			&input, &e.Result, &e.ErrorCode, &e.Source); err != nil {
			return nil, fmt.Errorf("lifecycle: read journal: %w", err)
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if input.Valid {
			e.Input = json.RawMessage(input.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ack deletes every entry up to and including seq, once the controller has
// stored them.
func (j *AgentJournal) Ack(ctx context.Context, seq int64) error {
	return j.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM core_journal WHERE seq <= ?", seq); err != nil {
			return fmt.Errorf("lifecycle: ack journal: %w", err)
		}
		return nil
	})
}

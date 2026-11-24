package notify

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// cursorInterval is how often the cursor reads core_changes.
	cursorInterval = 5 * time.Second
	// cursorOverlap is how far before its mark the cursor reads again: a
	// writer stamps updated_at before its transaction commits, so a row can
	// become visible with a time the cursor has already passed (the change
	// watcher's overlap, internal/controller/events/watch.go).
	cursorOverlap = 5 * time.Second
)

// changeCursor emits change.failed and change.auto_reverted for the
// controller's own changes. It reads core_changes past its stored mark,
// with the overlap, and advances the mark in the transaction that stores
// the events; a change's key is stored once, so a row read again emits
// nothing.
type changeCursor struct {
	db    *store.DB
	clock core.Clock
	log   *slog.Logger
	n     *notifier
}

func newChangeCursor(db *store.DB, clock core.Clock, log *slog.Logger, n *notifier) *changeCursor {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &changeCursor{db: db, clock: clock, log: log, n: n}
}

func (c *changeCursor) service() ext.Service {
	return ext.Service{Name: ID + ".changes", Run: c.Run}
}

// Run passes every cursorInterval until ctx ends. A failed pass is logged
// and the next one reads from the same mark.
func (c *changeCursor) Run(ctx context.Context) error {
	for {
		if err := c.pass(ctx); err != nil && ctx.Err() == nil {
			c.log.Warn("notify: read the controller's changes", "error", err)
		}
		fired := make(chan struct{})
		t := c.clock.AfterFunc(cursorInterval, func() { close(fired) })
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-fired:
		}
	}
}

type changeSeen struct {
	id, host, action, state, code string
}

// pass emits the events of the changes stamped after the mark less the
// overlap and moves the mark to the newest change, in one transaction.
func (c *changeCursor) pass(ctx context.Context) error {
	return c.db.Write(ctx, func(tx *sql.Tx) error {
		var mark, newest string
		if err := tx.QueryRowContext(ctx, `SELECT updated_at FROM notify_cursor WHERE id = 1`).Scan(&mark); err != nil {
			return fmt.Errorf("notify: read the change cursor: %w", err)
		}
		if err := tx.QueryRowContext(ctx, `SELECT coalesce(max(updated_at), '') FROM core_changes`).Scan(&newest); err != nil {
			return fmt.Errorf("notify: read the newest change: %w", err)
		}
		from := ""
		if mark != "" {
			at, err := time.Parse(timeFormat, mark)
			if err != nil {
				return fmt.Errorf("notify: the change cursor %q: %w", mark, err)
			}
			from = at.Add(-cursorOverlap).Format(timeFormat)
		}
		seen, err := endedChanges(ctx, tx, from)
		if err != nil {
			return err
		}
		for _, ch := range seen {
			kind, err := changeKind(ctx, tx, ch)
			if err != nil {
				return err
			}
			if kind == "" {
				continue
			}
			if err := c.n.Emit(ctx, tx, core.NotifyEvent{Kind: kind, HostID: ch.host, DedupeKey: kind + ":" + ch.id,
				Facts: &core.NotifyFacts{ChangeID: ch.id, Action: ch.action, State: ch.state, ErrorCode: ch.code}}); err != nil {
				return err
			}
		}
		if newest > mark {
			if _, err := tx.ExecContext(ctx, `UPDATE notify_cursor SET updated_at = ? WHERE id = 1`, newest); err != nil {
				return fmt.Errorf("notify: advance the change cursor: %w", err)
			}
		}
		return nil
	})
}

// endedChanges reads the failed and reverted changes stamped after from.
func endedChanges(ctx context.Context, tx *sql.Tx, from string) ([]changeSeen, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id, host_id, action, state, coalesce(error_code, '') FROM core_changes
		WHERE updated_at > ? AND state IN (?, ?) ORDER BY updated_at, id`, from, core.StateFailed, core.StateReverted)
	if err != nil {
		return nil, fmt.Errorf("notify: read the ended changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []changeSeen
	for rows.Next() {
		var ch changeSeen
		if err := rows.Scan(&ch.id, &ch.host, &ch.action, &ch.state, &ch.code); err != nil {
			return nil, fmt.Errorf("notify: read the ended changes: %w", err)
		}
		out = append(out, ch)
	}
	return out, rows.Err()
}

// changeKind is the event a controller change's end is: change.failed for
// a failed change or one reverted after its apply failed, and
// change.auto_reverted for one the revert timer or the revert watchdog
// reverted (its journal says which); "" for any other.
func changeKind(ctx context.Context, tx *sql.Tx, ch changeSeen) (string, error) {
	if ch.state == core.StateFailed || ch.code != "" {
		return core.NotifyChangeFailed, nil
	}
	var auto int
	err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_audit WHERE change_id = ?
		AND (phase = 'revert' AND actor = ? OR phase = ?)`, ch.id, lifecycle.ActorRevertTimer, lifecycle.PhaseRevertWatchdog).Scan(&auto)
	if err != nil {
		return "", fmt.Errorf("notify: read the journal of change %s: %w", ch.id, err)
	}
	if auto > 0 {
		return core.NotifyChangeAutoReverted, nil
	}
	return "", nil
}

package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// NonTerminal are the change states restore quarantines (spec 1.3 "Locks across restarts" without quarantined).
var NonTerminal = []string{core.StateApplying, core.StatePending, core.StateAwaitingConfirmation,
	core.StateUndoing, core.StateNeedsRepair, core.StateRecovering, core.StateReleasing}

// markStampLayout is the lifecycle time format the controller writes to
// updated_at columns.
const markStampLayout = "2006-01-02T15:04:05.000000000Z"

// Mark quarantines db in one transaction (spec 10 Marking): it writes the
// marker, moves every unfinished change and rollout to quarantined, closes
// open listeners and drops every lock a quarantined change does not own.
// The locks of quarantined records stay so that a new change on one of
// their keys fails with lock.held naming the record, until the operator
// resolves it.
func Mark(ctx context.Context, db *store.DB, m quarantine.Marker) error {
	raw, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("backup: mark encode marker: %w", err)
	}
	at := m.RestoredAt.UTC().Format(markStampLayout)
	return db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
			quarantine.MetaKey, string(raw)); err != nil {
			return fmt.Errorf("backup: mark core_meta %s: %w", quarantine.MetaKey, err)
		}

		args := []any{core.StateQuarantined, at}
		for _, s := range NonTerminal {
			args = append(args, s)
		}
		in := strings.TrimSuffix(strings.Repeat("?, ", len(NonTerminal)), ", ")
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET quarantined_from = state, state = ?, recovery_owner = '', updated_at = ? WHERE state IN (`+in+`)`, //nolint:gosec // G202: in holds only ? placeholders; the states are bound
			args...); err != nil {
			return fmt.Errorf("backup: mark core_changes: %w", err)
		}

		var taskRuns int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'core_task_runs'`).Scan(&taskRuns); err != nil {
			return fmt.Errorf("backup: mark look up core_task_runs: %w", err)
		}
		if taskRuns > 0 {
			if _, err := tx.ExecContext(ctx, `DELETE FROM core_task_runs`); err != nil {
				return fmt.Errorf("backup: mark core_task_runs: %w", err)
			}
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE core_rollouts SET quarantined_from = state, state = ?, updated_at = ? WHERE state IN (?, ?)`,
			core.RolloutQuarantined, at, core.RolloutRunning, core.RolloutDraining); err != nil {
			return fmt.Errorf("backup: mark core_rollouts: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`UPDATE core_listeners SET state = ?, updated_at = ? WHERE state = ?`,
			core.ListenerClosed, at, core.ListenerOpen); err != nil {
			return fmt.Errorf("backup: mark core_listeners: %w", err)
		}

		if _, err := tx.ExecContext(ctx,
			`DELETE FROM core_locks WHERE owner NOT IN (SELECT id FROM core_changes WHERE state = ?)`,
			core.StateQuarantined); err != nil {
			return fmt.Errorf("backup: mark core_locks: %w", err)
		}
		return nil
	})
}

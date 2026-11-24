package notify

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
)

// restore marks every pending delivery failed with reason restored when the
// restore-quarantine marker names a restore the outbox has not handled yet,
// and stores that restore's transaction in notify_state, in one
// transaction. The backup cannot tell which deliveries were sent after it
// was taken, so none of them is sent again; the marks emit nothing.
//
// It runs when the module's contribution is built, while the marker is
// still set: the service runner holds the sender until the quarantine is
// released, and the release deletes the marker.
func (o *outbox) restore(ctx context.Context) error {
	m, ok, err := quarantine.Read(ctx, o.cfg.DB)
	if err != nil || !ok {
		return err
	}
	now := o.cfg.Clock.Now().UTC().Format(timeFormat)
	return o.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		var handled sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT restored_tx FROM notify_state WHERE id = 1`).Scan(&handled); err != nil {
			return fmt.Errorf("notify: read notify_state: %w", err)
		}
		if handled.Valid && handled.String == m.Tx {
			return nil
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notify_deliveries SET state = 'failed', next_at = '', last_error = ?, updated_at = ?
			WHERE state = 'pending'`, reasonRestored, now); err != nil {
			return fmt.Errorf("notify: mark the pending deliveries restored: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE notify_state SET restored_tx = ? WHERE id = 1`, m.Tx); err != nil {
			return fmt.Errorf("notify: store the handled restore: %w", err)
		}
		return nil
	})
}

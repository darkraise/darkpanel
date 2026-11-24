package notify

import (
	"context"
	"database/sql"
	"time"
)

const (
	// retention is how long events, and with them their deliveries, are kept.
	retention = 30 * 24 * time.Hour
	// purgeEvery is how often the sender purges.
	purgeEvery = time.Hour
)

// purgeIfDue deletes the events older than retention, their deliveries by
// cascade, at most once per purgeEvery.
func (o *outbox) purgeIfDue(ctx context.Context) {
	now := o.cfg.Clock.Now().UTC()
	if !o.lastPurge.IsZero() && now.Sub(o.lastPurge) < purgeEvery {
		return
	}
	err := o.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM notify_events WHERE created_at < ?`, now.Add(-retention).Format(timeFormat))
		return err
	})
	if err != nil {
		if ctx.Err() == nil {
			o.cfg.Log.Warn("notify: purge old events", "error", err)
		}
		return
	}
	o.lastPurge = now
}

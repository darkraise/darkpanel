package controller

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	// rollupInterval is how often the rollup runs.
	rollupInterval = time.Hour
	// rollupRedo is how many hours before a host's newest hour row the
	// rollup averages again, so minutes backfilled after an hour ended
	// still reach its row.
	rollupRedo = 3 * time.Hour
	// keepMinutes and keepHours are how long the rows stay.
	keepMinutes = 7 * 24 * time.Hour
	keepHours   = 90 * 24 * time.Hour
)

// rollup averages each completed hour into the hour tables and deletes
// expired rows, at start and then every rollupInterval.
type rollup struct {
	db    *store.DB
	clock core.Clock
	log   *slog.Logger
}

func newRollup(db *store.DB, clock core.Clock, log *slog.Logger) *rollup {
	if clock == nil {
		clock = core.SystemClock{}
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &rollup{db: db, clock: clock, log: log}
}

// Run passes until ctx ends; a failed pass is logged and retried at the
// next interval.
func (r *rollup) Run(ctx context.Context) error {
	for {
		if err := r.pass(ctx); err != nil && ctx.Err() == nil {
			r.log.Warn("metrics: roll up the hours", "error", err)
		}
		fired := make(chan struct{})
		t := r.clock.AfterFunc(rollupInterval, func() { close(fired) })
		select {
		case <-ctx.Done():
			t.Stop()
			return nil
		case <-fired:
		}
	}
}

// The averages of one hour's minutes, by host and hour; each statement
// takes the host, the first minute to read and the end of the last
// completed hour.
const (
	hourExpr      = `substr(ts, 1, 13) || ':00:00Z'`
	rollupMinutes = `INSERT OR REPLACE INTO metrics_hour (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
		SELECT host_id, ` + hourExpr + `, avg(cpu), CAST(round(avg(mem_used)) AS INTEGER), CAST(round(avg(mem_total)) AS INTEGER),
		CAST(round(avg(swap_used)) AS INTEGER), CAST(round(avg(swap_total)) AS INTEGER), avg(load1), avg(load5), avg(load15)
		FROM metrics_minute WHERE host_id = ? AND ts >= ? AND ts < ? GROUP BY host_id, ` + hourExpr
	rollupMinuteFS = `INSERT OR REPLACE INTO metrics_hour_fs (host_id, ts, mount, used, total)
		SELECT host_id, ` + hourExpr + `, mount, CAST(round(avg(used)) AS INTEGER), CAST(round(avg(total)) AS INTEGER)
		FROM metrics_minute_fs WHERE host_id = ? AND ts >= ? AND ts < ? GROUP BY host_id, ` + hourExpr + `, mount`
	rollupMinuteNet = `INSERT OR REPLACE INTO metrics_hour_net (host_id, ts, iface, rx_bps, tx_bps)
		SELECT host_id, ` + hourExpr + `, iface, avg(rx_bps), avg(tx_bps)
		FROM metrics_minute_net WHERE host_id = ? AND ts >= ? AND ts < ? GROUP BY host_id, ` + hourExpr + `, iface`
)

// pass averages, for each host with minute rows, every completed hour from
// rollupRedo before its newest hour row (every hour when it has none), and
// then deletes minute rows older than keepMinutes and hour rows older than
// keepHours, in one transaction.
func (r *rollup) pass(ctx context.Context) error {
	now := r.clock.Now().UTC()
	end := now.Truncate(time.Hour).Format(tsFormat)
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `SELECT m.host_id, coalesce((SELECT max(h.ts) FROM metrics_hour h WHERE h.host_id = m.host_id), '')
			FROM (SELECT DISTINCT host_id FROM metrics_minute) m ORDER BY m.host_id`)
		if err != nil {
			return fmt.Errorf("metrics: read the hosts to roll up: %w", err)
		}
		from := map[string]string{}
		var hosts []string
		for rows.Next() {
			var host, newest string
			if err := rows.Scan(&host, &newest); err != nil {
				_ = rows.Close()
				return fmt.Errorf("metrics: read the hosts to roll up: %w", err)
			}
			if newest != "" {
				at, err := time.Parse(tsFormat, newest)
				if err != nil {
					_ = rows.Close()
					return fmt.Errorf("metrics: the hour %q of host %s: %w", newest, host, err)
				}
				newest = at.Add(-rollupRedo).Format(tsFormat)
			}
			hosts = append(hosts, host)
			from[host] = newest
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("metrics: read the hosts to roll up: %w", err)
		}
		for _, host := range hosts {
			for _, q := range []string{rollupMinutes, rollupMinuteFS, rollupMinuteNet} {
				if _, err := tx.ExecContext(ctx, q, host, from[host], end); err != nil {
					return fmt.Errorf("metrics: roll up the hours of host %s: %w", host, err)
				}
			}
		}
		minuteCut, hourCut := now.Add(-keepMinutes).Format(tsFormat), now.Add(-keepHours).Format(tsFormat)
		for _, d := range []struct{ q, cut string }{
			{`DELETE FROM metrics_minute WHERE ts < ?`, minuteCut},
			{`DELETE FROM metrics_minute_fs WHERE ts < ?`, minuteCut},
			{`DELETE FROM metrics_minute_net WHERE ts < ?`, minuteCut},
			{`DELETE FROM metrics_hour WHERE ts < ?`, hourCut},
			{`DELETE FROM metrics_hour_fs WHERE ts < ?`, hourCut},
			{`DELETE FROM metrics_hour_net WHERE ts < ?`, hourCut},
		} {
			if _, err := tx.ExecContext(ctx, d.q, d.cut); err != nil {
				return fmt.Errorf("metrics: delete expired rows: %w", err)
			}
		}
		return nil
	})
}

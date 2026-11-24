// Package controller is the metrics module's controller half (M10 spec
// section 5.2): the poller that copies each host's minute averages into
// controller.db, the hourly rollup and retention, and metrics.history.get.
// It must not import internal/modules/metrics, which imports it, so it
// decodes the agent's samples into its own types.
package controller

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"
)

// moduleID is the metrics module's id.
const moduleID = "metrics"

// tsFormat is how the tables store a minute or an hour.
const tsFormat = "2006-01-02T15:04:05Z"

// The bounds on what one answer may store: an agent answers at most 60
// minutes, 16 filesystems and 16 interfaces each.
const (
	maxAnswerMinutes = 60
	maxEntries       = 16
	maxMountLen      = 4096
	maxIfaceLen      = 64
	maxCPUs          = 1 << 14
)

// Point is one minute's or one hour's averages, as metrics.samples.get
// answers them and metrics.history.get returns them.
type Point struct {
	TS        time.Time `json:"ts" jsonschema_description:"The start of the minute or the hour."`
	CPU       float64   `json:"cpu" jsonschema_description:"CPU busy percent across all cores."`
	MemUsed   uint64    `json:"memUsed"`
	MemTotal  uint64    `json:"memTotal"`
	SwapUsed  uint64    `json:"swapUsed"`
	SwapTotal uint64    `json:"swapTotal"`
	Load1     float64   `json:"load1"`
	Load5     float64   `json:"load5"`
	Load15    float64   `json:"load15"`
	FS        []FSUsage `json:"fs"`
	Net       []NetRate `json:"net"`
}

// FSUsage is one mounted filesystem's use in bytes.
type FSUsage struct {
	Mount string `json:"mount"`
	Used  uint64 `json:"used"`
	Total uint64 `json:"total"`
}

// NetRate is one interface's receive and transmit bytes per second.
type NetRate struct {
	Iface string  `json:"iface"`
	RxBps float64 `json:"rxBps"`
	TxBps float64 `json:"txBps"`
}

// answer is the output of metrics.samples.get.
type answer struct {
	Now     time.Time `json:"now"`
	Samples []Point   `json:"samples"`
	CPUs    int       `json:"cpus"`
}

func nonNegative(vs ...float64) bool {
	for _, v := range vs {
		if math.IsNaN(v) || math.IsInf(v, 0) || v < 0 {
			return false
		}
	}
	return true
}

func fitsInteger(vs ...uint64) bool {
	for _, v := range vs {
		if v > math.MaxInt64 {
			return false
		}
	}
	return true
}

// storable is the minutes of an agent's answer the poller stores: whole
// minutes that ended by now (by the controller's clock) and are at most two
// hours old, with figures in range; a filesystem or interface beyond the
// first 16, or with a name out of bounds, is left out. Agents are not
// trusted to keep their own bounds. bad counts the minutes left out for a
// misaligned time or a figure out of range, which no later poll refetches.
func storable(samples []Point, now time.Time) (out []Point, bad int) {
	for _, s := range samples[:min(len(samples), maxAnswerMinutes)] {
		ts := s.TS.UTC()
		if ts.Add(time.Minute).After(now) || ts.Before(now.Add(-2*time.Hour)) {
			continue
		}
		if !ts.Equal(ts.Truncate(time.Minute)) || !nonNegative(s.CPU, s.Load1, s.Load5, s.Load15) || s.CPU > 100 ||
			!fitsInteger(s.MemUsed, s.MemTotal, s.SwapUsed, s.SwapTotal) {
			bad++
			continue
		}
		p := s
		p.TS, p.FS, p.Net = ts, nil, nil
		for _, f := range s.FS {
			if len(p.FS) < maxEntries && strings.HasPrefix(f.Mount, "/") && len(f.Mount) <= maxMountLen && fitsInteger(f.Used, f.Total) {
				p.FS = append(p.FS, f)
			}
		}
		for _, n := range s.Net {
			if len(p.Net) < maxEntries && n.Iface != "" && len(n.Iface) <= maxIfaceLen && nonNegative(n.RxBps, n.TxBps) {
				p.Net = append(p.Net, n)
			}
		}
		out = append(out, p)
	}
	return out, bad
}

// storeMinutes writes points as hostID's minute rows; a minute already
// stored is kept.
func storeMinutes(ctx context.Context, tx *sql.Tx, hostID string, points []Point) error {
	for _, p := range points {
		ts := p.TS.Format(tsFormat)
		if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metrics_minute
			(host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, hostID, ts, p.CPU, int64(p.MemUsed), int64(p.MemTotal), //nolint:gosec // G115: storable bounds every value to int64
			int64(p.SwapUsed), int64(p.SwapTotal), p.Load1, p.Load5, p.Load15); err != nil { //nolint:gosec // G115: as above
			return fmt.Errorf("metrics: store a minute of host %s: %w", hostID, err)
		}
		for _, f := range p.FS {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metrics_minute_fs (host_id, ts, mount, used, total)
				VALUES (?, ?, ?, ?, ?)`, hostID, ts, f.Mount, int64(f.Used), int64(f.Total)); err != nil { //nolint:gosec // G115: as above
				return fmt.Errorf("metrics: store a minute of host %s: %w", hostID, err)
			}
		}
		for _, n := range p.Net {
			if _, err := tx.ExecContext(ctx, `INSERT OR IGNORE INTO metrics_minute_net (host_id, ts, iface, rx_bps, tx_bps)
				VALUES (?, ?, ?, ?, ?)`, hostID, ts, n.Iface, n.RxBps, n.TxBps); err != nil {
				return fmt.Errorf("metrics: store a minute of host %s: %w", hostID, err)
			}
		}
	}
	return nil
}

// lastMinute is hostID's newest stored minute, or "".
func lastMinute(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, hostID string) (string, error) {
	var last string
	err := q.QueryRowContext(ctx, `SELECT coalesce(max(ts), '') FROM metrics_minute WHERE host_id = ?`, hostID).Scan(&last)
	if err != nil {
		return "", fmt.Errorf("metrics: read the last minute of host %s: %w", hostID, err)
	}
	return last, nil
}

// recordOK marks hostID's poll at now successful, with the agent's skew
// and its logical CPUs; a count out of range is stored as unknown.
func recordOK(ctx context.Context, tx *sql.Tx, hostID string, now time.Time, skew int64, cpus int) error {
	var n sql.NullInt64
	if cpus > 0 && cpus <= maxCPUs {
		n = sql.NullInt64{Int64: int64(cpus), Valid: true}
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO metrics_poll_status (host_id, last_ok, last_error_code, last_error_at, clock_skew_s, cpus)
		VALUES (?, ?, NULL, NULL, ?, ?) ON CONFLICT (host_id) DO UPDATE SET last_ok = excluded.last_ok,
		last_error_code = NULL, last_error_at = NULL, clock_skew_s = excluded.clock_skew_s, cpus = excluded.cpus`,
		hostID, now.Format(tsFormat), skew, n)
	if err != nil {
		return fmt.Errorf("metrics: record the poll of host %s: %w", hostID, err)
	}
	return nil
}

// recordFailure marks hostID's poll at now failed with code; skew, when
// not nil, replaces the stored skew.
func recordFailure(ctx context.Context, tx *sql.Tx, hostID, code string, now time.Time, skew *int64) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO metrics_poll_status (host_id, last_ok, last_error_code, last_error_at, clock_skew_s)
		VALUES (?, NULL, ?, ?, ?) ON CONFLICT (host_id) DO UPDATE SET last_error_code = excluded.last_error_code,
		last_error_at = excluded.last_error_at, clock_skew_s = coalesce(excluded.clock_skew_s, clock_skew_s)`,
		hostID, code, now.Format(tsFormat), skew)
	if err != nil {
		return fmt.Errorf("metrics: record the poll of host %s: %w", hostID, err)
	}
	return nil
}

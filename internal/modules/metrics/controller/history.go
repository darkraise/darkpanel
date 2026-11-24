package controller

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActionHistory is the controller-side read of a host's stored metrics.
const ActionHistory = moduleID + ".history.get"

// The ranges metrics.history.get serves and how far back each reaches.
var ranges = map[string]time.Duration{
	"1h": time.Hour, "24h": 24 * time.Hour, "7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour, "90d": 90 * 24 * time.Hour,
}

// minuteRanges are the ranges served from the minute tables; the others
// come from the hour tables.
var minuteRanges = map[string]bool{"1h": true, "24h": true, "7d": true}

// HistoryInput is the input of metrics.history.get.
type HistoryInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host whose metrics to read."`
	Range  string `json:"range,omitempty" jsonschema:"enum=1h,enum=24h,enum=7d,enum=30d,enum=90d" jsonschema_description:"How far back to read; 1h when left out. Up to 7d the points are minutes, beyond that hours."`
}

// History is the output of metrics.history.get.
type History struct {
	HostID     string      `json:"hostId"`
	Range      string      `json:"range"`
	Resolution string      `json:"resolution" jsonschema:"enum=1m,enum=1h" jsonschema_description:"1m for minute points, 1h for hour points."`
	Points     []Point     `json:"points" jsonschema_description:"Oldest first."`
	Status     *PollStatus `json:"status" jsonschema:"nullable" jsonschema_description:"The host's last poll; null before the first."`
}

// PollStatus is a host's last poll, so a reader can say why points stopped.
type PollStatus struct {
	LastOK        *time.Time `json:"lastOk" jsonschema:"nullable"`
	LastErrorCode *string    `json:"lastErrorCode" jsonschema:"nullable" jsonschema_description:"The error code of the last poll while it failed: an error code, metrics.clock_skew or metrics.bad_answer."`
	LastErrorAt   *time.Time `json:"lastErrorAt" jsonschema:"nullable"`
	ClockSkewS    *int64     `json:"clockSkewS" jsonschema:"nullable" jsonschema_description:"The agent clock less the controller's, in seconds, at the last answer."`
}

func historyAction() core.ActionDef {
	return core.DefineRead[HistoryInput, History](core.ActionSpec{
		Name:     ActionHistory,
		Summary:  "Return a host's stored metrics over a range, with its last poll.",
		Version:  1,
		Side:     core.SideController,
		HostData: core.HostDataInput, HostPointers: []string{"/hostId"},
	}, historyHandler{})
}

type historyHandler struct{}

// The queries of one resolution, over its three tables.
type seriesQueries struct{ main, fs, net string }

var (
	minuteQueries = seriesQueries{
		main: `SELECT ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15 FROM metrics_minute WHERE host_id = ? AND ts > ? ORDER BY ts`,
		fs:   `SELECT ts, mount, used, total FROM metrics_minute_fs WHERE host_id = ? AND ts > ? ORDER BY ts, mount`,
		net:  `SELECT ts, iface, rx_bps, tx_bps FROM metrics_minute_net WHERE host_id = ? AND ts > ? ORDER BY ts, iface`,
	}
	hourQueries = seriesQueries{
		main: `SELECT ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15 FROM metrics_hour WHERE host_id = ? AND ts > ? ORDER BY ts`,
		fs:   `SELECT ts, mount, used, total FROM metrics_hour_fs WHERE host_id = ? AND ts > ? ORDER BY ts, mount`,
		net:  `SELECT ts, iface, rx_bps, tx_bps FROM metrics_hour_net WHERE host_id = ? AND ts > ? ORDER BY ts, iface`,
	}
)

func (historyHandler) Read(ctx context.Context, env core.Env, in HistoryInput) (History, error) {
	if in.Range == "" {
		in.Range = "1h"
	}
	span, ok := ranges[in.Range]
	if !ok {
		return History{}, errcode.New(errcode.InvalidInput, "range must be 1h, 24h, 7d, 30d or 90d")
	}
	db := env.DB.Read()
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE id = ?`, in.HostID).Scan(&exists); err != nil {
		return History{}, fmt.Errorf("metrics: read host %s: %w", in.HostID, err)
	}
	if exists == 0 {
		return History{}, errcode.New(errcode.NotFound, "no host "+in.HostID)
	}
	out := History{HostID: in.HostID, Range: in.Range, Resolution: "1h", Points: []Point{}}
	qs := hourQueries
	if minuteRanges[in.Range] {
		out.Resolution, qs = "1m", minuteQueries
	}
	from := env.Clock.Now().UTC().Add(-span).Format(tsFormat)
	var err error
	if out.Points, err = readSeries(ctx, db, qs, in.HostID, from); err != nil {
		return History{}, err
	}
	if out.Status, err = readStatus(ctx, db, in.HostID); err != nil {
		return History{}, err
	}
	return out, nil
}

// readSeries reads hostID's points after from, with their filesystems and
// interfaces.
func readSeries(ctx context.Context, db *sql.DB, qs seriesQueries, hostID, from string) ([]Point, error) {
	points := []Point{}
	at := map[string]int{}
	rows, err := db.QueryContext(ctx, qs.main, hostID, from)
	if err != nil {
		return nil, fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
	}
	for rows.Next() {
		var ts string
		var p Point
		var mu, mt, su, st int64
		if err := rows.Scan(&ts, &p.CPU, &mu, &mt, &su, &st, &p.Load1, &p.Load5, &p.Load15); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
		}
		if p.TS, err = time.Parse(tsFormat, ts); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("metrics: the stored time %q of host %s: %w", ts, hostID, err)
		}
		p.MemUsed, p.MemTotal, p.SwapUsed, p.SwapTotal = uint64(mu), uint64(mt), uint64(su), uint64(st) //nolint:gosec // G115: stored from uint64 values within int64
		p.FS, p.Net = []FSUsage{}, []NetRate{}
		at[ts] = len(points)
		points = append(points, p)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
	}
	err = eachRow(ctx, db, qs.fs, hostID, from, func(rows *sql.Rows) error {
		var ts string
		var f FSUsage
		var used, total int64
		if err := rows.Scan(&ts, &f.Mount, &used, &total); err != nil {
			return err
		}
		if i, ok := at[ts]; ok {
			f.Used, f.Total = uint64(used), uint64(total) //nolint:gosec // G115: as above
			points[i].FS = append(points[i].FS, f)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	err = eachRow(ctx, db, qs.net, hostID, from, func(rows *sql.Rows) error {
		var ts string
		var n NetRate
		if err := rows.Scan(&ts, &n.Iface, &n.RxBps, &n.TxBps); err != nil {
			return err
		}
		if i, ok := at[ts]; ok {
			points[i].Net = append(points[i].Net, n)
		}
		return nil
	})
	return points, err
}

func eachRow(ctx context.Context, db *sql.DB, q, hostID, from string, scan func(*sql.Rows) error) error {
	rows, err := db.QueryContext(ctx, q, hostID, from)
	if err != nil {
		return fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := scan(rows); err != nil {
			return fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("metrics: read the history of host %s: %w", hostID, err)
	}
	return nil
}

// readStatus is hostID's poll status, nil before its first poll.
func readStatus(ctx context.Context, db *sql.DB, hostID string) (*PollStatus, error) {
	var lastOK, code, errAt sql.NullString
	var skew sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT last_ok, last_error_code, last_error_at, clock_skew_s FROM metrics_poll_status WHERE host_id = ?`,
		hostID).Scan(&lastOK, &code, &errAt, &skew)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil //nolint:nilnil // no poll yet is not an error
	}
	if err != nil {
		return nil, fmt.Errorf("metrics: read the poll status of host %s: %w", hostID, err)
	}
	st := &PollStatus{}
	parse := func(s sql.NullString) *time.Time {
		if !s.Valid {
			return nil
		}
		t, err := time.Parse(tsFormat, s.String)
		if err != nil {
			return nil
		}
		return &t
	}
	st.LastOK, st.LastErrorAt = parse(lastOK), parse(errAt)
	if code.Valid {
		st.LastErrorCode = &code.String
	}
	if skew.Valid {
		st.ClockSkewS = &skew.Int64
	}
	return st, nil
}

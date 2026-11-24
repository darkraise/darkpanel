package controller

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 11, 24, 12, 0, 30, 0, time.UTC)

// testDB is a controller database with the core migrations and the
// module's (read from disk: this package cannot import the module).
func testDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	sets := []store.MigrationSet{{Module: moduleID, FS: os.DirFS("../migrations/controller")}}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}, coreSet, sets)
	if err != nil || len(res.Failed) != 0 {
		t.Fatalf("migrate: %+v, %v", res, err)
	}
	return db
}

func exec(t *testing.T, db *store.DB, q string, args ...any) {
	t.Helper()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(q, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
}

func count(t *testing.T, db *store.DB, q string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(q, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", q, err)
	}
	return n
}

func addHost(t *testing.T, db *store.DB, id, state string) {
	t.Helper()
	exec(t, db, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, 0, ?, ?)`,
		id, "name-"+id, t0.Format(time.RFC3339Nano), state)
}

// fakeAgent answers metrics.samples.get like an agent whose ring holds the
// complete minutes from start, at most the last hour, on a clock skew off
// the controller's.
type fakeAgent struct {
	start       time.Time
	skew        time.Duration
	ignoreSince bool  // answer every minute held, as an overlapping poll would
	err         error // fail every read
	extra       []Point
}

func pointAt(ts time.Time) Point {
	v := float64(ts.Minute())
	return Point{TS: ts, CPU: v, MemUsed: uint64(1000 + v), MemTotal: 4000, SwapTotal: 100, Load1: v / 10, Load5: 1, Load15: 2,
		FS: []FSUsage{{Mount: "/", Used: 10, Total: 100}}, Net: []NetRate{{Iface: "eth0", RxBps: v, TxBps: 1}}}
}

func (a *fakeAgent) answer(now time.Time, in json.RawMessage) (json.RawMessage, error) {
	if a.err != nil {
		return nil, a.err
	}
	var req struct {
		Since string `json:"since"`
	}
	if err := json.Unmarshal(in, &req); err != nil {
		return nil, err
	}
	agentNow := now.Add(a.skew)
	from := a.start
	if held := agentNow.Truncate(time.Minute).Add(-time.Hour); from.Before(held) {
		from = held
	}
	var since time.Time
	if req.Since != "" && !a.ignoreSince {
		var err error
		if since, err = time.Parse(time.RFC3339, req.Since); err != nil {
			return nil, errcode.New(errcode.InvalidInput, "since")
		}
	}
	out := answer{Now: agentNow, Samples: append([]Point{}, a.extra...)}
	for m := from; !m.Add(time.Minute).After(agentNow) && len(out.Samples) < 60; m = m.Add(time.Minute) {
		if since.IsZero() || m.After(since) {
			out.Samples = append(out.Samples, pointAt(m))
		}
	}
	return json.Marshal(out)
}

type pollEnv struct {
	db       *store.DB
	clock    *coretest.FakeClock
	mu       sync.Mutex
	agents   map[string]*fakeAgent
	reads    map[string][]string // each host's inputs, in order
	internal int
	logs     *bytes.Buffer
	p        *poller
	disp     *coretest.FakeDispatcher
}

func newPollEnv(t *testing.T) *pollEnv {
	t.Helper()
	e := &pollEnv{db: testDB(t), clock: coretest.NewFakeClock(t0), agents: map[string]*fakeAgent{}, reads: map[string][]string{},
		logs: &bytes.Buffer{}}
	e.disp = &coretest.FakeDispatcher{
		ReadFunc: func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if a != samplesRef || p.Actor != "system:metrics.poll" {
				t.Errorf("the poller read %s as %s", a, p.Actor)
			}
			e.reads[hostID] = append(e.reads[hostID], string(input))
			ag, ok := e.agents[hostID]
			if !ok {
				return nil, errcode.New(errcode.HostOffline, "host "+hostID+" is offline")
			}
			return ag.answer(e.clock.Now(), input)
		},
		ReadInternalFunc: func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.internal++
			return nil, errcode.New(errcode.ActionUnknown, "no internal read")
		},
	}
	e.p = newPoller(e.db, e.disp, e.clock, slog.New(slog.NewTextHandler(e.logs, nil)))
	return e
}

func (e *pollEnv) pass(t *testing.T) {
	t.Helper()
	if err := e.p.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func (e *pollEnv) readsOf(host string) []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.reads[host]...)
}

func (e *pollEnv) logLines(host string) int {
	n := 0
	for _, line := range strings.Split(e.logs.String(), "\n") {
		if strings.Contains(line, "polling the host") && strings.Contains(line, "host="+host) {
			n++
		}
	}
	return n
}

type statusRow struct {
	lastOK, code, errAt sql.NullString
	skew                sql.NullInt64
}

func status(t *testing.T, db *store.DB, host string) (statusRow, bool) {
	t.Helper()
	var s statusRow
	err := db.Read().QueryRow(`SELECT last_ok, last_error_code, last_error_at, clock_skew_s FROM metrics_poll_status WHERE host_id = ?`,
		host).Scan(&s.lastOK, &s.code, &s.errAt, &s.skew)
	if err == sql.ErrNoRows {
		return s, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return s, true
}

func minutes(t *testing.T, db *store.DB, host string) int {
	return count(t, db, `SELECT count(*) FROM metrics_minute WHERE host_id = ?`, host)
}

func TestPollAndBackfill(t *testing.T) {
	e := newPollEnv(t)
	for _, h := range []struct{ id, state string }{{"h1", "enrolled"}, {"h2", "enrolled"}, {"h3", "enrolled"}, {"h4", "revoked"}, {"h5", "enrolled"}, {"h6", "enrolled"}} {
		addHost(t, e.db, h.id, h.state)
	}
	exec(t, e.db, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at) VALUES ('h2', 'metrics', 0, 1, '', ?)`,
		t0.Format(time.RFC3339Nano))
	agentStart := time.Date(2026, 11, 24, 11, 30, 0, 0, time.UTC)
	e.agents["h1"] = &fakeAgent{start: agentStart}
	e.agents["h2"] = &fakeAgent{start: agentStart}
	e.agents["h4"] = &fakeAgent{start: agentStart}
	e.agents["h5"] = &fakeAgent{start: agentStart, skew: 3 * time.Minute}
	e.agents["h6"] = &fakeAgent{start: agentStart, skew: -3 * time.Minute}
	// h3 has no agent: it is offline.

	e.pass(t)
	if got := e.readsOf("h1"); len(got) != 1 || got[0] != `{}` {
		t.Fatalf("h1 was read with %v, want once with no since", got)
	}
	if n := minutes(t, e.db, "h1"); n != 30 {
		t.Fatalf("after the first poll h1 has %d minutes, want 30 (11:30 to 11:59)", n)
	}
	for _, h := range []string{"h2", "h4"} {
		if got := e.readsOf(h); len(got) != 0 {
			t.Errorf("%s (disabled or revoked) was read: %v", h, got)
		}
	}
	if _, ok := status(t, e.db, "h3"); ok || minutes(t, e.db, "h3") != 0 {
		t.Error("the offline h3 has a poll status or minutes")
	}
	if s, ok := status(t, e.db, "h1"); !ok || s.lastOK.String != "2026-11-24T12:00:30Z" || s.code.Valid || s.skew.Int64 != 0 {
		t.Errorf("h1 status = %+v, want ok at 12:00:30 with no skew", s)
	}

	e.clock.Advance(time.Minute)
	e.pass(t)
	if got := e.readsOf("h1"); got[1] != `{"since":"2026-11-24T11:59:00Z"}` {
		t.Errorf("the second poll passed %s, want the last stored minute", got[1])
	}
	if n := minutes(t, e.db, "h1"); n != 31 {
		t.Errorf("after the second poll h1 has %d minutes, want 31", n)
	}

	t.Run("a 40-minute outage backfills", func(t *testing.T) {
		e.clock.Advance(40 * time.Minute) // 12:41:30, no poll in between
		e.pass(t)
		if n := count(t, e.db, `SELECT count(*) FROM metrics_minute WHERE host_id = 'h1' AND ts > '2026-11-24T12:00:00Z'
			AND ts <= '2026-11-24T12:40:00Z'`); n != 40 {
			t.Errorf("after the outage %d of the 40 missing minutes are stored", n)
		}
		if n := count(t, e.db, `SELECT count(*) FROM metrics_minute_fs WHERE host_id = 'h1'`); n != 71 {
			t.Errorf("h1 has %d filesystem rows, want 71", n)
		}
	})
	t.Run("an overlapping answer stores each minute once", func(t *testing.T) {
		e.agents["h1"].ignoreSince = true
		e.clock.Advance(time.Minute)
		e.pass(t)
		if n := minutes(t, e.db, "h1"); n != 72 {
			t.Errorf("after an overlapping answer h1 has %d minutes, want 72", n)
		}
		e.agents["h1"].ignoreSince = false
	})
	t.Run("minutes out of bounds are not stored", func(t *testing.T) {
		now := e.clock.Now().Truncate(time.Minute)
		bad := pointAt(now.Add(-time.Hour - 30*time.Second)) // not a whole minute
		hot := pointAt(now.Add(-90 * time.Minute))
		hot.CPU = 150
		future := pointAt(now.Add(5 * time.Minute))
		e.agents["h1"].extra = []Point{bad, hot, future}
		e.clock.Advance(time.Minute)
		e.pass(t)
		e.agents["h1"].extra = nil
		if n := count(t, e.db, `SELECT count(*) FROM metrics_minute WHERE host_id = 'h1' AND (cpu > 100 OR ts > ? OR substr(ts, 18) != '00Z')`,
			now.Format(tsFormat)); n != 0 {
			t.Errorf("%d out-of-bounds minutes were stored", n)
		}
	})
	t.Run("failures set the status and log once per change", func(t *testing.T) {
		before := minutes(t, e.db, "h1")
		ok, _ := status(t, e.db, "h1")
		e.agents["h1"].err = errcode.New(errcode.ModuleUnavailable, "the module is unavailable: cannot read /proc/stat")
		for range 2 {
			e.clock.Advance(time.Minute)
			e.pass(t)
		}
		s, _ := status(t, e.db, "h1")
		if s.code.String != string(errcode.ModuleUnavailable) || s.errAt.String != e.clock.Now().Format(tsFormat) || s.lastOK != ok.lastOK {
			t.Errorf("after two failed polls h1 status = %+v, want module.unavailable now with last_ok kept", s)
		}
		if n := e.logLines("h1"); n != 1 {
			t.Errorf("two failed polls logged %d lines, want 1", n)
		}
		e.agents["h1"].err = nil
		e.clock.Advance(time.Minute)
		e.pass(t)
		if s, _ := status(t, e.db, "h1"); s.code.Valid || s.errAt.Valid {
			t.Errorf("after a good poll h1 status = %+v, want the error cleared", s)
		}
		if n := e.logLines("h1"); n != 2 {
			t.Errorf("failing and recovering logged %d lines, want 2", n)
		}
		if minutes(t, e.db, "h1") <= before {
			t.Error("the recovering poll stored no minutes")
		}
		if strings.Contains(e.logs.String(), "/proc/stat") {
			t.Error("the log carries the agent's error text")
		}
	})
	t.Run("a skewed agent clock is a failure", func(t *testing.T) {
		s, _ := status(t, e.db, "h5")
		if s.code.String != CodeClockSkew || s.skew.Int64 != 180 || s.lastOK.Valid {
			t.Errorf("h5 status = %+v, want metrics.clock_skew with 180 s", s)
		}
		if n := minutes(t, e.db, "h5"); n != 0 {
			t.Errorf("h5 has %d minutes stored from a skewed clock", n)
		}
		if n := e.logLines("h5"); n != 1 {
			t.Errorf("h5's repeated skew logged %d lines, want 1", n)
		}
		if s, _ := status(t, e.db, "h6"); s.code.String != CodeClockSkew || s.skew.Int64 != -180 || minutes(t, e.db, "h6") != 0 {
			t.Errorf("h6, 3 minutes behind: status %+v with %d minutes, want metrics.clock_skew at -180 s and none", s, minutes(t, e.db, "h6"))
		}
	})
	t.Run("Run polls once a minute", func(t *testing.T) {
		f := newPollEnv(t)
		addHost(t, f.db, "h1", "enrolled")
		f.agents["h1"] = &fakeAgent{start: agentStart}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error)
		go func() { done <- f.p.Run(ctx) }()
		armed := func() {
			deadline := time.Now().Add(5 * time.Second)
			for f.clock.Pending() == 0 && time.Now().Before(deadline) {
				time.Sleep(time.Millisecond)
			}
		}
		armed() // the first pass ran and the next waits a minute
		if n := len(f.readsOf("h1")); n != 1 {
			t.Fatalf("Run polled %d times at start, want 1", n)
		}
		f.clock.Advance(59 * time.Second)
		time.Sleep(20 * time.Millisecond)
		if n := len(f.readsOf("h1")); n != 1 {
			t.Errorf("after 59 s there were %d polls, want 1", n)
		}
		f.clock.Advance(time.Second)
		deadline := time.Now().Add(5 * time.Second)
		for len(f.readsOf("h1")) < 2 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if n := len(f.readsOf("h1")); n != 2 {
			t.Errorf("after a minute there were %d polls, want 2", n)
		}
		cancel()
		if err := <-done; err != nil {
			t.Errorf("Run returned %v", err)
		}
	})
}

// minuteRow writes one minute of host at ts with every figure v.
func minuteRow(t *testing.T, db *store.DB, host string, ts time.Time, v float64) {
	t.Helper()
	s := ts.UTC().Format(tsFormat)
	exec(t, db, `INSERT OR REPLACE INTO metrics_minute (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
		VALUES (?, ?, ?, ?, 4000, 0, 0, ?, ?, ?)`, host, s, v, int64(v*10), v, v, v)
	exec(t, db, `INSERT OR REPLACE INTO metrics_minute_fs (host_id, ts, mount, used, total) VALUES (?, ?, '/', ?, 100)`, host, s, int64(v))
	exec(t, db, `INSERT OR REPLACE INTO metrics_minute_net (host_id, ts, iface, rx_bps, tx_bps) VALUES (?, ?, 'eth0', ?, 1)`, host, s, v)
}

func hourRow(t *testing.T, db *store.DB, host string, ts time.Time) {
	t.Helper()
	exec(t, db, `INSERT INTO metrics_hour (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15)
		VALUES (?, ?, 1, 1, 1, 0, 0, 0, 0, 0)`, host, ts.UTC().Format(tsFormat))
}

func TestRollupAndRetention(t *testing.T) {
	db := testDB(t)
	addHost(t, db, "h1", "enrolled")
	now := time.Date(2026, 11, 24, 12, 5, 0, 0, time.UTC)
	clock := coretest.NewFakeClock(now)
	ten := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for i := range 60 {
		minuteRow(t, db, "h1", ten.Add(time.Duration(i)*time.Minute), float64(i))
	}
	for i := range 30 {
		minuteRow(t, db, "h1", ten.Add(time.Hour+time.Duration(i)*time.Minute), 100)
	}
	for i := range 5 {
		minuteRow(t, db, "h1", ten.Add(2*time.Hour+time.Duration(i)*time.Minute), 7)
	}
	week, quarter := 7*24*time.Hour, 90*24*time.Hour
	minuteRow(t, db, "h1", now.Add(-week-time.Minute), 1)
	minuteRow(t, db, "h1", now.Add(-week+time.Hour), 1)
	hourRow(t, db, "h1", now.Add(-quarter-time.Hour))
	hourRow(t, db, "h1", now.Add(-quarter+24*time.Hour))
	r := newRollup(db, clock, nil)
	if err := r.pass(context.Background()); err != nil {
		t.Fatal(err)
	}
	hour := func(ts time.Time) (cpu float64, memUsed int64, fsUsed int64, rx float64, ok bool) {
		t.Helper()
		s := ts.Format(tsFormat)
		err := db.Read().QueryRow(`SELECT h.cpu, h.mem_used, f.used, n.rx_bps FROM metrics_hour h
			JOIN metrics_hour_fs f ON f.host_id = h.host_id AND f.ts = h.ts AND f.mount = '/'
			JOIN metrics_hour_net n ON n.host_id = h.host_id AND n.ts = h.ts AND n.iface = 'eth0'
			WHERE h.host_id = 'h1' AND h.ts = ?`, s).Scan(&cpu, &memUsed, &fsUsed, &rx)
		if err == sql.ErrNoRows {
			return 0, 0, 0, 0, false
		}
		if err != nil {
			t.Fatal(err)
		}
		return cpu, memUsed, fsUsed, rx, true
	}
	if cpu, mem, fs, rx, ok := hour(ten); !ok || cpu != 29.5 || mem != 295 || fs != 30 || rx != 29.5 {
		t.Errorf("hour 10:00 = %v %v %v %v (%t), want the averages 29.5, 295, 30, 29.5", cpu, mem, fs, rx, ok)
	}
	if cpu, _, _, _, ok := hour(ten.Add(time.Hour)); !ok || cpu != 100 {
		t.Errorf("hour 11:00 cpu = %v (%t), want 100", cpu, ok)
	}
	if _, _, _, _, ok := hour(ten.Add(2 * time.Hour)); ok {
		t.Error("the hour 12:00, not yet complete, was rolled up")
	}
	if n := count(t, db, `SELECT count(*) FROM metrics_minute WHERE ts < ?`, now.Add(-week).Format(tsFormat)); n != 0 {
		t.Errorf("%d minute rows older than 7 days remain", n)
	}
	for _, table := range []string{"metrics_minute_fs", "metrics_minute_net"} {
		if n := count(t, db, `SELECT count(*) FROM `+table+` WHERE ts < ?`, now.Add(-week).Format(tsFormat)); n != 0 {
			t.Errorf("%d %s rows older than 7 days remain", n, table)
		}
	}
	if n := count(t, db, `SELECT count(*) FROM metrics_minute WHERE ts = ?`, now.Add(-week+time.Hour).Format(tsFormat)); n != 1 {
		t.Error("a minute row younger than 7 days was deleted")
	}
	if n := count(t, db, `SELECT count(*) FROM metrics_hour WHERE ts < ?`, now.Add(-quarter).Format(tsFormat)); n != 0 {
		t.Errorf("%d hour rows older than 90 days remain", n)
	}
	if n := count(t, db, `SELECT count(*) FROM metrics_hour WHERE ts = ?`, now.Add(-quarter+24*time.Hour).Format(tsFormat)); n != 1 {
		t.Error("an hour row younger than 90 days was deleted")
	}

	t.Run("minutes backfilled after the hour ended reach its row", func(t *testing.T) {
		for i := 30; i < 60; i++ {
			minuteRow(t, db, "h1", ten.Add(time.Hour+time.Duration(i)*time.Minute), 0)
		}
		clock.Advance(time.Hour)
		if err := r.pass(context.Background()); err != nil {
			t.Fatal(err)
		}
		if cpu, _, _, _, _ := hour(ten.Add(time.Hour)); cpu != 50 {
			t.Errorf("hour 11:00 cpu = %v after the backfill, want 50", cpu)
		}
		if _, _, _, _, ok := hour(ten.Add(2 * time.Hour)); !ok {
			t.Error("the hour 12:00, now complete, was not rolled up")
		}
	})
}

func TestHistoryResolution(t *testing.T) {
	db := testDB(t)
	addHost(t, db, "h1", "enrolled")
	now := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	env := core.Env{DB: db, Clock: coretest.NewFakeClock(now)}
	for _, ago := range []time.Duration{30 * time.Minute, 23 * time.Hour, 6*24*time.Hour + 23*time.Hour, 7*24*time.Hour + time.Hour} {
		minuteRow(t, db, "h1", now.Add(-ago), 5)
	}
	for _, ago := range []time.Duration{29 * 24 * time.Hour, 89 * 24 * time.Hour, 91 * 24 * time.Hour} {
		hourRow(t, db, "h1", now.Add(-ago))
	}
	exec(t, db, `INSERT INTO metrics_poll_status (host_id, last_ok, last_error_code, last_error_at, clock_skew_s)
		VALUES ('h1', '2026-11-24T11:58:00Z', 'module.unavailable', '2026-11-24T11:59:00Z', 3)`)
	read := func(rng string) History {
		t.Helper()
		out, err := historyHandler{}.Read(context.Background(), env, HistoryInput{HostID: "h1", Range: rng})
		if err != nil {
			t.Fatalf("range %q: %v", rng, err)
		}
		return out
	}
	for _, c := range []struct {
		rng, resolution string
		points          int
	}{{"", "1m", 1}, {"1h", "1m", 1}, {"24h", "1m", 2}, {"7d", "1m", 3}, {"30d", "1h", 1}, {"90d", "1h", 2}} {
		out := read(c.rng)
		if out.Resolution != c.resolution || len(out.Points) != c.points {
			t.Errorf("range %q: resolution %s with %d points, want %s with %d", c.rng, out.Resolution, len(out.Points), c.resolution, c.points)
		}
		for i := 1; i < len(out.Points); i++ {
			if !out.Points[i].TS.After(out.Points[i-1].TS) {
				t.Errorf("range %q: points not oldest first", c.rng)
			}
		}
	}
	day := read("24h")
	p := day.Points[1]
	if !p.TS.Equal(now.Add(-30*time.Minute)) || p.CPU != 5 || p.MemUsed != 50 || len(p.FS) != 1 || p.FS[0].Used != 5 ||
		len(p.Net) != 1 || p.Net[0].RxBps != 5 {
		t.Errorf("the newest 24h point = %+v", p)
	}
	st := day.Status
	if st == nil || st.LastOK == nil || !st.LastOK.Equal(now.Add(-2*time.Minute)) || st.LastErrorCode == nil ||
		*st.LastErrorCode != "module.unavailable" || st.ClockSkewS == nil || *st.ClockSkewS != 3 {
		t.Errorf("status = %+v", st)
	}
	addHost(t, db, "h2", "enrolled")
	if out, err := (historyHandler{}).Read(context.Background(), env, HistoryInput{HostID: "h2", Range: "1h"}); err != nil ||
		out.Status != nil || out.Points == nil || len(out.Points) != 0 {
		t.Errorf("a host never polled: %+v, %v; want no points and a null status", out, err)
	}
	if _, err := (historyHandler{}).Read(context.Background(), env, HistoryInput{HostID: "nope", Range: "1h"}); err == nil ||
		errcode.From(err).Code != errcode.NotFound {
		t.Errorf("an unknown host: %v, want %s", err, errcode.NotFound)
	}
	if _, err := (historyHandler{}).Read(context.Background(), env, HistoryInput{HostID: "h1", Range: "2y"}); err == nil ||
		errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("range 2y: %v, want %s", err, errcode.InvalidInput)
	}
	// A viewer outside the host gets not found from the API's host check,
	// which reads these fields (internal/controller/api leak test).
	spec := historyAction().Spec
	if spec.Side != core.SideController || spec.Class != core.ClassRead || spec.Internal ||
		spec.HostData != core.HostDataInput || len(spec.HostPointers) != 1 || spec.HostPointers[0] != "/hostId" {
		t.Errorf("metrics.history.get spec = %+v, want a controller read naming its host at /hostId", spec)
	}
}

func TestPollingWritesNoAudit(t *testing.T) {
	e := newPollEnv(t)
	addHost(t, e.db, "h1", "enrolled")
	e.agents["h1"] = &fakeAgent{start: t0.Add(-30 * time.Minute)}
	for range 60 {
		e.pass(t)
		e.clock.Advance(time.Minute)
	}
	if n := len(e.readsOf("h1")); n != 60 {
		t.Fatalf("60 passes made %d reads", n)
	}
	if e.internal != 0 {
		t.Errorf("polling made %d internal reads, which journal every call", e.internal)
	}
	if n := count(t, e.db, `SELECT count(*) FROM core_audit`); n != 0 {
		t.Errorf("60 polls wrote %d audit rows", n)
	}
}

package metrics_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/metrics"
)

// Acceptance tests of the M10 foundations spec for the metrics module. The
// sampler is driven through its background task over /proc fixtures; the
// controller half through its contributed services over a real controller
// database and a fake agent.

const qaTS = "2006-01-02T15:04:05Z"

// ---- A26: the sampler ------------------------------------------------------

// qaProc is a mutable /proc.
type qaProc struct{ fstest.MapFS }

func (p qaProc) set(cpuBusy, cpuIdle int, netRx, netTx int64, extraIfaces int) {
	p.MapFS["proc/stat"] = &fstest.MapFile{Data: []byte(fmt.Sprintf("cpu  %d 0 0 %d 0 0 0 0 0 0\ncpu0 %d 0 0 %d 0 0 0 0 0 0\ncpu1 0 0 0 0 0 0 0 0 0 0\nintr 1\n", cpuBusy, cpuIdle, cpuBusy, cpuIdle))}
	var b strings.Builder
	b.WriteString("Inter-|   Receive                                                |  Transmit\n face |bytes    packets errs drop fifo frame compressed multicast|bytes    packets errs drop fifo colls carrier compressed\n")
	fmt.Fprintf(&b, "    lo: 99999 1 0 0 0 0 0 0 99999 1 0 0 0 0 0 0\n")
	fmt.Fprintf(&b, "  eth0: %d 1 0 0 0 0 0 0 %d 1 0 0 0 0 0 0\n", netRx, netTx)
	for i := 0; i < extraIfaces; i++ {
		fmt.Fprintf(&b, "  dum%02d: %d 1 0 0 0 0 0 0 %d 1 0 0 0 0 0 0\n", i, netRx, netTx)
	}
	p.MapFS["proc/net/dev"] = &fstest.MapFile{Data: []byte(b.String())}
}

func newQAProc() qaProc {
	p := qaProc{fstest.MapFS{
		"proc/meminfo": {Data: []byte("MemTotal: 8000000 kB\nMemFree: 1000000 kB\nMemAvailable: 6000000 kB\nBuffers: 0 kB\nSwapTotal: 1000000 kB\nSwapFree: 750000 kB\n")},
		"proc/loadavg": {Data: []byte("0.50 0.25 0.10 1/100 123\n")},
	}}
	var m strings.Builder
	m.WriteString("sysfs /sys sysfs rw 0 0\nproc /proc proc rw 0 0\ntmpfs /run tmpfs rw 0 0\n10.0.0.1:/x /nfs nfs rw 0 0\n")
	for i := 1; i <= 20; i++ {
		fmt.Fprintf(&m, "/dev/sd%c /m%02d ext4 rw 0 0\n", 'a'+i, i)
	}
	p.MapFS["proc/self/mounts"] = &fstest.MapFile{Data: []byte(m.String())}
	return p
}

func TestA26_sampler_returns_minute_averages(t *testing.T) {
	proc := newQAProc()
	statfs := func(string) (uint64, uint64, error) { return 1 << 30, 4 << 30, nil }
	mod := metrics.New(metrics.Source{FS: proc, Statfs: statfs})
	tasks := mod.(core.Background).BackgroundTasks()
	if len(tasks) != 1 || tasks[0].Every != 15*time.Second {
		t.Fatalf("background tasks %+v, want one sampler every 15 seconds", tasks)
	}
	var samples core.ActionDef
	for _, a := range mod.Manifest().Actions {
		if a.Spec.Name == "metrics.samples.get" {
			samples = a
		}
	}
	if samples.Handler.Read == nil || samples.Spec.Side != core.SideAgent {
		t.Fatalf("metrics.samples.get is not an agent-side read: %+v", samples.Spec)
	}
	clock := coretest.NewFakeClock(time.Date(2026, 11, 24, 11, 59, 50, 0, time.UTC))
	env := core.Env{Clock: clock}
	pass := func(cpuBusy, cpuIdle int, rx, tx int64) {
		t.Helper()
		proc.set(cpuBusy, cpuIdle, rx, tx, 20)
		if err := tasks[0].Run(context.Background(), core.TaskContext{Env: env}); err != nil {
			t.Fatal(err)
		}
	}
	pass(0, 0, 0, 0) // primes the counters
	busy, idle := 0, 0
	var rx, tx int64
	// Minute 12:00 is 20 % busy, 15 s apart, 1 000 B/s in and 500 B/s out; 12:01 is 40 % busy.
	for i := 0; i < 8; i++ {
		clock.Advance(15 * time.Second)
		pct := 20
		if clock.Now().After(time.Date(2026, 11, 24, 12, 1, 0, 0, time.UTC)) {
			pct = 40
		}
		busy += pct * 10
		idle += (100 - pct) * 10
		rx += 15000
		tx += 7500
		pass(busy, idle, rx, tx)
	}
	clock.Advance(15 * time.Second)
	pass(busy+400, idle+600, rx+15000, tx+7500)
	read := func(since string) metrics.SamplesOutput {
		t.Helper()
		in := json.RawMessage(`{}`)
		if since != "" {
			in = json.RawMessage(fmt.Sprintf(`{"since":%q}`, since))
		}
		raw, err := samples.Handler.Read(context.Background(), env, in)
		if err != nil {
			t.Fatal(err)
		}
		var out metrics.SamplesOutput
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := read("")
	if len(out.Samples) < 2 {
		t.Fatalf("metrics.samples.get returned %d minutes, want the two complete ones: %+v", len(out.Samples), out.Samples)
	}
	m0, m1 := out.Samples[0], out.Samples[1]
	if !m0.TS.Equal(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)) || !m1.TS.Equal(time.Date(2026, 11, 24, 12, 1, 0, 0, time.UTC)) {
		t.Errorf("minutes start at %s and %s, want 12:00 and 12:01, oldest first", m0.TS, m1.TS)
	}
	if out.CPUs != 2 {
		t.Errorf("cpus = %d, want 2", out.CPUs)
	}
	if m0.CPU < 19.99 || m0.CPU > 20.01 || m1.CPU < 39.99 || m1.CPU > 40.01 {
		t.Errorf("cpu averages %v and %v, want 20 and 40", m0.CPU, m1.CPU)
	}
	if m0.MemUsed != 2_048_000_000 || m0.MemTotal != 8_192_000_000 {
		t.Errorf("memory %d of %d, want 2048000000 of 8192000000 (total less available)", m0.MemUsed, m0.MemTotal)
	}
	if m0.SwapUsed != 256_000_000 || m0.SwapTotal != 1_024_000_000 {
		t.Errorf("swap %d of %d, want 256000000 of 1024000000", m0.SwapUsed, m0.SwapTotal)
	}
	if m0.Load1 != 0.5 || m0.Load5 != 0.25 || m0.Load15 != 0.1 {
		t.Errorf("load %v %v %v, want 0.5 0.25 0.1", m0.Load1, m0.Load5, m0.Load15)
	}
	if len(m0.FS) != 16 {
		t.Fatalf("%d mounts, want the first 16 of 20 local ones", len(m0.FS))
	}
	for i, f := range m0.FS {
		if want := fmt.Sprintf("/m%02d", i+1); f.Mount != want || f.Used != 1<<30 || f.Total != 4<<30 {
			t.Errorf("mount %d is %+v, want %s using 1 GiB of 4 GiB", i, f, want)
		}
	}
	if len(m0.Net) != 16 {
		t.Fatalf("%d interfaces, want 16 (lo left out, 21 others)", len(m0.Net))
	}
	for _, n := range m0.Net {
		if n.Iface == "lo" {
			t.Errorf("loopback in the interfaces")
		}
		if n.RxBps < 999.9 || n.RxBps > 1000.1 || n.TxBps < 499.9 || n.TxBps > 500.1 {
			t.Errorf("interface %s rate %v/%v B/s, want 1000 in and 500 out", n.Iface, n.RxBps, n.TxBps)
		}
	}
	if after := read("2026-11-24T12:00:00Z"); len(after.Samples) != 1 || !after.Samples[0].TS.Equal(m1.TS) {
		t.Errorf("since 12:00:00 returned %+v, want only the 12:01 minute", after.Samples)
	}
}

// ---- the controller half ---------------------------------------------------

// qaAgent is a host's agent: it has a sample for every complete minute value
// gives one for, and answers metrics.samples.get as the spec describes.
type qaAgent struct {
	clock  *coretest.FakeClock
	reads  atomic.Int32
	mu     sync.Mutex
	value  map[string]func(minute time.Time) (metrics.Sample, bool)
	offset time.Duration
}

func (a *qaAgent) Read(_ context.Context, _ core.Principal, hostID string, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if ref.Name != "metrics.samples.get" {
		return nil, errcode.New(errcode.ActionUnknown, ref.Name)
	}
	a.reads.Add(1)
	var in struct{ Since string }
	_ = json.Unmarshal([]byte(strings.ToLower(string(input))), &in)
	var in2 struct {
		Since string `json:"since"`
	}
	_ = json.Unmarshal(input, &in2)
	now := a.clock.Now().UTC().Add(a.offset)
	var since time.Time
	if in2.Since != "" {
		since, _ = time.Parse(time.RFC3339, in2.Since)
	}
	a.mu.Lock()
	f := a.value[hostID]
	a.mu.Unlock()
	var out metrics.SamplesOutput
	out.Now, out.CPUs, out.Samples = now, 2, []metrics.Sample{}
	if f != nil {
		first := now.Truncate(time.Minute).Add(-time.Hour)
		for m := first; !m.Add(time.Minute).After(now) && len(out.Samples) < 60; m = m.Add(time.Minute) {
			if !m.After(since) {
				continue
			}
			if s, ok := f(m); ok {
				s.TS = m
				out.Samples = append(out.Samples, s)
			}
		}
	}
	return json.Marshal(out)
}

func (a *qaAgent) ReadInternal(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
	return nil, errcode.New(errcode.Forbidden, "no internal reads")
}

func (a *qaAgent) Plan(context.Context, core.Principal, string, core.ActionRef, json.RawMessage, core.PlanOptions) (core.PlanView, error) {
	return core.PlanView{}, errcode.New(errcode.Forbidden, "no plans")
}

func (a *qaAgent) Apply(context.Context, core.Principal, string, core.ActionRef, string, core.ApplyOptions) (core.ChangeView, error) {
	return core.ChangeView{}, errcode.New(errcode.Forbidden, "no applies")
}

// qaNotifier records the events emitted.
type qaNotifier struct {
	mu     sync.Mutex
	events []core.NotifyEvent
}

func (n *qaNotifier) Emit(_ context.Context, _ *sql.Tx, e core.NotifyEvent) error {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.events = append(n.events, e)
	return nil
}

func (n *qaNotifier) kinds(host string) []string {
	n.mu.Lock()
	defer n.mu.Unlock()
	var out []string
	for _, e := range n.events {
		if host == "" || e.HostID == host {
			out = append(out, e.Kind)
		}
	}
	return out
}

// qaSessions says which hosts have a live agent session.
type qaSessions struct {
	mu      sync.Mutex
	offline map[string]bool
}

func (s *qaSessions) Agent(_ context.Context, host string) (core.AgentClient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.offline[host] {
		return nil, errcode.New(errcode.HostOffline, "offline")
	}
	return nil, nil
}

func (s *qaSessions) set(host string, off bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.offline[host] = off
}

type qaCtl struct {
	t        *testing.T
	db       *store.DB
	clock    *coretest.FakeClock
	agent    *qaAgent
	notifier *qaNotifier
	sessions *qaSessions
	mod      core.Module
	hosts    []string
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	expected int32
}

func qaNewCtl(t *testing.T, start time.Time, hosts ...string) *qaCtl {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	mod := metrics.New(metrics.Source{FS: fstest.MapFS{}, Statfs: func(string) (uint64, uint64, error) { return 0, 0, nil }})
	core1, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: start}
	if _, err := store.Migrate(ctx, db, opts, core1, []store.MigrationSet{{Module: "metrics", FS: mod.Manifest().Migrations[core.SideController]}}); err != nil {
		t.Fatal(err)
	}
	c := &qaCtl{t: t, db: db, clock: coretest.NewFakeClock(start), notifier: &qaNotifier{}, sessions: &qaSessions{offline: map[string]bool{}}, mod: mod, hosts: hosts}
	c.agent = &qaAgent{clock: c.clock, value: map[string]func(time.Time) (metrics.Sample, bool){}}
	for _, h := range hosts {
		if _, err := db.Writer().Exec(`INSERT INTO core_hosts (id, name, is_local, state, created_at) VALUES (?, ?, 0, 'enrolled', '2026-11-01T00:00:00.000000000Z')`, h, h); err != nil {
			t.Fatal(err)
		}
	}
	return c
}

// start runs the module's services, as the server does, and waits until the
// first pass has run.
func (c *qaCtl) start() {
	c.t.Helper()
	deps := ext.Deps{DB: c.db, Clock: c.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Hosts: c.sessions, Dispatch: c.agent, Notifier: c.notifier}
	_, services, err := c.mod.(ext.Controller).Controller(deps)
	if err != nil || len(services) == 0 {
		c.t.Fatalf("Controller: %v, %d services", err, len(services))
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.cancel = cancel
	for _, s := range services {
		c.wg.Add(1)
		go func() {
			defer c.wg.Done()
			_ = s.Run(ctx)
		}()
	}
	c.expected += int32(min(len(c.hosts), 1<<20)) //nolint:gosec // G115: a handful of hosts
	c.idle(len(services))
}

func (c *qaCtl) stop() {
	c.cancel()
	c.wg.Wait()
}

// idle waits until every host was polled as often as expected and the
// services wait for their timers again.
func (c *qaCtl) idle(timers int) {
	c.t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	stable := 0
	for time.Now().Before(deadline) {
		if c.agent.reads.Load() == c.expected && c.clock.Pending() == timers {
			if stable++; stable >= 3 {
				return
			}
		} else {
			stable = 0
		}
		time.Sleep(15 * time.Millisecond)
	}
	c.t.Fatalf("services not idle: %d reads of %d, %d timers of %d", c.agent.reads.Load(), c.expected, c.clock.Pending(), timers)
}

// step moves the clock one minute and waits for the poll that follows.
func (c *qaCtl) step(timers int) {
	c.t.Helper()
	c.expected += int32(min(len(c.hosts), 1<<20)) //nolint:gosec // G115: a handful of hosts
	c.clock.Advance(time.Minute)
	c.idle(timers)
}

func (c *qaCtl) minutes(host string) []time.Time {
	rows, err := c.db.Read().Query(`SELECT ts FROM metrics_minute WHERE host_id = ? ORDER BY ts`, host)
	if err != nil {
		c.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var out []time.Time
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			c.t.Fatal(err)
		}
		at, err := time.Parse(qaTS, s)
		if err != nil {
			c.t.Fatal(err)
		}
		out = append(out, at)
	}
	return out
}

// qaSample is a sample of a healthy host with the disk at diskPct percent.
func qaSample(diskPct int) metrics.Sample {
	return metrics.Sample{CPU: 10, MemUsed: 1 << 30, MemTotal: 4 << 30, SwapTotal: 1, Load1: 0.1,
		FS: []metrics.FSUsage{{Mount: "/", Used: uint64(max(diskPct, 0)), Total: 100}}, Net: []metrics.NetRate{}}
}

func TestA27_outage_is_backfilled(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 30, 0, time.UTC)
	c := qaNewCtl(t, start, "h1")
	c.agent.value["h1"] = func(time.Time) (metrics.Sample, bool) { return qaSample(10), true }
	c.start()
	for i := 0; i < 10; i++ {
		c.step(2)
	}
	before := c.minutes("h1")
	c.stop()
	newestBefore := before[len(before)-1]
	c.clock.Advance(40 * time.Minute) // the outage
	c.start()
	got := c.minutes("h1")
	wantNewest := c.clock.Now().UTC().Truncate(time.Minute).Add(-time.Minute)
	if last := got[len(got)-1]; !last.Equal(wantNewest) {
		t.Fatalf("newest stored minute %s, want %s", last, wantNewest)
	}
	// Every minute from the first stored to the newest, none missing.
	for i, at := range got {
		if want := got[0].Add(time.Duration(i) * time.Minute); !at.Equal(want) {
			t.Fatalf("stored minutes have a gap before %s (row %d, want %s)", at, i, want)
		}
	}
	missing := 0
	for m := newestBefore.Add(time.Minute); !m.After(wantNewest); m = m.Add(time.Minute) {
		missing++
	}
	if missing < 40 || len(got) < len(before)+40 {
		t.Errorf("%d minutes stored after the outage, %d before; want the %d missing minutes filled", len(got), len(before), missing)
	}
	c.stop()
}

func TestA57_backfill_fires_no_stale_alert(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 30, 0, time.UTC)
	c := qaNewCtl(t, start, "burst", "gap", "steady")
	outageEnd := start.Add(13*time.Minute + 40*time.Minute) // after 13 live minutes and the outage
	endMinute := outageEnd.Truncate(time.Minute)
	live := start.Add(13 * time.Minute).Truncate(time.Minute)
	// burst: a high disk for 16 minutes in the middle of the outage, ended before its newest minute.
	c.agent.value["burst"] = func(m time.Time) (metrics.Sample, bool) {
		if !m.Before(live.Add(5*time.Minute)) && m.Before(live.Add(21*time.Minute)) {
			return qaSample(95), true
		}
		return qaSample(40), true
	}
	// gap: high from the end of the live minutes, but one minute inside the last 10 is missing.
	c.agent.value["gap"] = func(m time.Time) (metrics.Sample, bool) {
		if m.Equal(endMinute.Add(-5 * time.Minute)) {
			return metrics.Sample{}, false
		}
		if m.Before(live) {
			return qaSample(40), true
		}
		return qaSample(95), true
	}
	// steady: high for the whole outage and after; the control that proves evaluation ran.
	c.agent.value["steady"] = func(m time.Time) (metrics.Sample, bool) {
		if m.Before(live) {
			return qaSample(40), true
		}
		return qaSample(95), true
	}
	c.start()
	for i := 0; i < 13; i++ {
		c.step(2)
	}
	if k := c.notifier.kinds(""); len(k) != 0 {
		t.Fatalf("events during the live minutes: %v, want none", k)
	}
	c.stop()
	c.clock.Advance(40 * time.Minute)
	c.start()
	c.step(2)
	if got := c.notifier.kinds("burst"); len(got) != 0 {
		t.Errorf("backfilling a burst that ended before the newest minute emitted %v, want no alert", got)
	}
	if got := c.notifier.kinds("gap"); len(got) != 0 {
		t.Errorf("a window with a missing minute emitted %v, want no alert", got)
	}
	if got := c.notifier.kinds("steady"); strings.Join(got, ",") != "alert.firing" {
		t.Errorf("a host high through its whole newest window emitted %v, want one alert.firing", got)
	}
	c.stop()
}

func TestA30_alert_fires_once_and_resolves(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 30, 0, time.UTC)
	c := qaNewCtl(t, start, "h1")
	high := 0
	c.agent.value["h1"] = func(m time.Time) (metrics.Sample, bool) {
		// Minutes 12:00 to 12:14 are high (15 of them); before and after are low.
		if !m.Before(time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)) && m.Before(time.Date(2026, 11, 24, 12, 15, 0, 0, time.UTC)) {
			return qaSample(95), true
		}
		return qaSample(50), true
	}
	_ = high
	c.start()
	// The rule needs 10 minutes: the newest complete minute is 12:00 after the first step.
	count := func() (f, r int) {
		for _, k := range c.notifier.kinds("h1") {
			switch k {
			case "alert.firing":
				f++
			case "alert.resolved":
				r++
			}
		}
		return
	}
	for i := 1; i <= 9; i++ {
		c.step(2)
		if f, _ := count(); f != 0 {
			t.Fatalf("after %d high minutes the rule already fired %d times, want it to need 10", i, f)
		}
	}
	c.step(2) // the tenth high minute
	if f, r := count(); f != 1 || r != 0 {
		t.Fatalf("after 10 high minutes: %d firing, %d resolved, want 1 and 0", f, r)
	}
	for i := 0; i < 5; i++ { // minutes 11 to 15 of the condition: the newest complete minute is still high at 12:14
		c.step(2)
	}
	if f, r := count(); f != 1 || r != 0 {
		t.Errorf("while it holds: %d firing, %d resolved, want it to fire once only", f, r)
	}
	c.step(2) // one low minute (12:15): not yet resolved
	if _, r := count(); r != 0 {
		t.Errorf("one minute below the threshold resolved the alert")
	}
	c.step(2) // second low minute
	if f, r := count(); f != 1 || r != 1 {
		t.Errorf("after 2 minutes without the condition: %d firing, %d resolved, want 1 and 1", f, r)
	}
	c.step(2)
	if f, r := count(); f != 1 || r != 1 {
		t.Errorf("a later evaluation changed the counts to %d and %d", f, r)
	}
	c.stop()
}

func TestA31_restart_raises_no_offline_alert(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 0, 30, 0, time.UTC)
	c := qaNewCtl(t, start, "back", "stays")
	for _, h := range c.hosts {
		c.agent.value[h] = func(time.Time) (metrics.Sample, bool) { return qaSample(10), true }
	}
	// The controller has just started: no host has its session yet.
	c.sessions.set("back", true)
	c.sessions.set("stays", true)
	c.start()
	c.step(2)
	c.step(2)
	c.sessions.set("back", false) // reconnects within the 5 minutes of "Host offline"
	for i := 0; i < 4; i++ {
		c.step(2)
	}
	if got := c.notifier.kinds("back"); len(got) != 0 {
		t.Errorf("a host that reconnected within the rule's duration raised %v", got)
	}
	// A host that stays offline does fire once the duration has passed.
	for i := 0; i < 4; i++ {
		c.step(2)
	}
	if got := c.notifier.kinds("stays"); strings.Join(got, ",") != "alert.firing" {
		t.Errorf("a host offline for longer than the rule's duration raised %v, want one alert.firing", got)
	}
	c.stop()
}

func TestA28_retention_and_hourly_rollup(t *testing.T) {
	start := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	c := qaNewCtl(t, start, "h1")
	c.agent.value["h1"] = nil // the agent has nothing: only the stored rows matter
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := c.db.Writer().Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	insertMinute := func(at time.Time, cpu float64) {
		ts := at.Format(qaTS)
		exec(`INSERT INTO metrics_minute (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15) VALUES ('h1', ?, ?, 100, 200, 0, 0, 1, 1, 1)`, ts, cpu)
		exec(`INSERT INTO metrics_minute_fs (host_id, ts, mount, used, total) VALUES ('h1', ?, '/', 50, 100)`, ts)
		exec(`INSERT INTO metrics_minute_net (host_id, ts, iface, rx_bps, tx_bps) VALUES ('h1', ?, 'eth0', 10, 20)`, ts)
	}
	insertHour := func(at time.Time) {
		ts := at.Format(qaTS)
		exec(`INSERT INTO metrics_hour (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15) VALUES ('h1', ?, 1, 1, 1, 0, 0, 1, 1, 1)`, ts)
		exec(`INSERT INTO metrics_hour_fs (host_id, ts, mount, used, total) VALUES ('h1', ?, '/', 1, 2)`, ts)
		exec(`INSERT INTO metrics_hour_net (host_id, ts, iface, rx_bps, tx_bps) VALUES ('h1', ?, 'eth0', 1, 2)`, ts)
	}
	old := start.Add(-7*24*time.Hour - time.Minute).Truncate(time.Minute)
	keep := start.Add(-7*24*time.Hour + time.Hour).Truncate(time.Minute)
	insertMinute(old, 1)
	insertMinute(keep, 1)
	oldHour := start.Add(-90*24*time.Hour - time.Hour).Truncate(time.Hour)
	keepHour := start.Add(-89 * 24 * time.Hour).Truncate(time.Hour)
	insertHour(oldHour)
	insertHour(keepHour)
	// A completed hour, 11:00, holds 60 minutes alternating 10 % and 30 % CPU.
	for i := 0; i < 60; i++ {
		cpu := 10.0
		if i%2 == 1 {
			cpu = 30
		}
		insertMinute(time.Date(2026, 11, 24, 11, i, 0, 0, time.UTC), cpu)
	}
	c.start()
	count := func(table string) (n int) {
		_ = c.db.Read().QueryRow(`SELECT count(*) FROM `+table+` WHERE host_id = 'h1' AND ts = ?`, "").Scan(&n)
		return
	}
	_ = count
	has := func(table string, at time.Time) bool {
		var n int
		if err := c.db.Read().QueryRow(`SELECT count(*) FROM `+table+` WHERE ts = ?`, at.Format(qaTS)).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n > 0
	}
	for _, tb := range []string{"metrics_minute", "metrics_minute_fs", "metrics_minute_net"} {
		if has(tb, old) {
			t.Errorf("%s still holds a row older than 7 days", tb)
		}
		if !has(tb, keep) {
			t.Errorf("%s lost a row younger than 7 days", tb)
		}
	}
	for _, tb := range []string{"metrics_hour", "metrics_hour_fs", "metrics_hour_net"} {
		if has(tb, oldHour) {
			t.Errorf("%s still holds a row older than 90 days", tb)
		}
		if !has(tb, keepHour) {
			t.Errorf("%s lost a row younger than 90 days", tb)
		}
	}
	var cpu, mu float64
	var memTotal int64
	if err := c.db.Read().QueryRow(`SELECT cpu, mem_used, mem_total FROM metrics_hour WHERE host_id = 'h1' AND ts = '2026-11-24T11:00:00Z'`).Scan(&cpu, &mu, &memTotal); err != nil {
		t.Fatalf("the completed 11:00 hour is not in the hour table: %v", err)
	}
	if cpu < 19.999 || cpu > 20.001 || mu != 100 || memTotal != 200 {
		t.Errorf("the 11:00 hour averages cpu %v, mem %v of %v, want 20, 100 of 200", cpu, mu, memTotal)
	}
	var fs, net int
	_ = c.db.Read().QueryRow(`SELECT count(*) FROM metrics_hour_fs WHERE ts = '2026-11-24T11:00:00Z'`).Scan(&fs)
	_ = c.db.Read().QueryRow(`SELECT count(*) FROM metrics_hour_net WHERE ts = '2026-11-24T11:00:00Z'`).Scan(&net)
	if fs != 1 || net != 1 {
		t.Errorf("the 11:00 hour has %d filesystem and %d interface rows, want 1 and 1", fs, net)
	}
	// The hour in progress (12:00) is not averaged yet.
	if has("metrics_hour", time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)) {
		t.Errorf("the hour in progress was averaged")
	}
	c.stop()
}

func TestA29_history_resolution(t *testing.T) {
	now := time.Date(2026, 11, 24, 12, 30, 0, 0, time.UTC)
	c := qaNewCtl(t, now, "h1")
	for i := 1; i <= 90; i++ { // 90 minutes of minute rows
		at := now.Add(-time.Duration(i) * time.Minute).Truncate(time.Minute)
		if _, err := c.db.Writer().Exec(`INSERT INTO metrics_minute (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15) VALUES ('h1', ?, 5, 1, 2, 0, 0, 0, 0, 0)`, at.Format(qaTS)); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 24*40; i++ { // 40 days of hour rows
		at := now.Add(-time.Duration(i) * time.Hour).Truncate(time.Hour)
		if _, err := c.db.Writer().Exec(`INSERT INTO metrics_hour (host_id, ts, cpu, mem_used, mem_total, swap_used, swap_total, load1, load5, load15) VALUES ('h1', ?, 7, 1, 2, 0, 0, 0, 0, 0)`, at.Format(qaTS)); err != nil {
			t.Fatal(err)
		}
	}
	var history core.ActionDef
	for _, a := range c.mod.Manifest().Actions {
		if a.Spec.Name == "metrics.history.get" {
			history = a
		}
	}
	env := core.Env{DB: c.db, Clock: c.clock, Principal: core.Principal{Access: core.Access{Admin: true, Fleet: true}}}
	get := func(rng string) (res string, points []time.Time) {
		t.Helper()
		raw, err := history.Handler.Read(context.Background(), env, json.RawMessage(fmt.Sprintf(`{"hostId":"h1","range":%q}`, rng)))
		if err != nil {
			t.Fatalf("range %s: %v", rng, err)
		}
		var out struct {
			Resolution string
			Points     []struct{ TS time.Time }
		}
		if err := json.Unmarshal(raw, &out); err != nil {
			t.Fatal(err)
		}
		for _, p := range out.Points {
			points = append(points, p.TS)
		}
		return out.Resolution, points
	}
	for _, rng := range []string{"1h", "24h", "7d"} {
		res, pts := get(rng)
		if res != "1m" {
			t.Errorf("range %s resolution %q, want 1m", rng, res)
		}
		for i := 1; i < len(pts); i++ {
			if pts[i].Sub(pts[i-1]) != time.Minute {
				t.Errorf("range %s: points %s and %s are not 1 minute apart", rng, pts[i-1], pts[i])
				break
			}
		}
	}
	if _, pts := get("1h"); len(pts) != 60 && len(pts) != 59 {
		t.Errorf("range 1h returned %d points, want 60 minutes", len(pts))
	}
	if _, pts := get("24h"); len(pts) != 90 {
		t.Errorf("range 24h returned %d points, want the 90 stored minutes", len(pts))
	}
	for _, rng := range []string{"30d", "90d"} {
		res, pts := get(rng)
		if res != "1h" {
			t.Errorf("range %s resolution %q, want 1h", rng, res)
		}
		for i := 1; i < len(pts); i++ {
			if pts[i].Sub(pts[i-1]) != time.Hour {
				t.Errorf("range %s: points %s and %s are not 1 hour apart", rng, pts[i-1], pts[i])
				break
			}
		}
		if len(pts) == 0 {
			t.Errorf("range %s returned no hourly points", rng)
		}
	}
	if _, pts := get("30d"); len(pts) < 24*30-1 || len(pts) > 24*30 {
		t.Errorf("range 30d returned %d points, want about %d hourly ones", len(pts), 24*30)
	}
}

func TestA32_default_rules_exist(t *testing.T) {
	c := qaNewCtl(t, time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC))
	rows, err := c.db.Read().Query(`SELECT name, metric, comparator, threshold, duration_min, severity, target, enabled FROM metrics_alert_rules ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type rule struct {
		name, metric, cmp, severity, target string
		threshold                           float64
		duration, enabled                   int
	}
	var got []rule
	for rows.Next() {
		var r rule
		if err := rows.Scan(&r.name, &r.metric, &r.cmp, &r.threshold, &r.duration, &r.severity, &r.target, &r.enabled); err != nil {
			t.Fatal(err)
		}
		got = append(got, r)
	}
	want := []rule{
		{"Disk almost full", "disk", ">", "warning", "*", 90, 10, 1},
		{"Host offline", "offline", ">", "critical", "*", 0, 5, 1},
	}
	if len(got) != len(want) {
		t.Fatalf("rules after first start: %+v, want exactly the two defaults", got)
	}
	for i := range want {
		if got[i].name != want[i].name || got[i].metric != want[i].metric || got[i].severity != want[i].severity || got[i].target != want[i].target ||
			got[i].duration != want[i].duration || got[i].enabled != 1 || (want[i].metric == "disk" && (got[i].threshold != 90 || got[i].cmp != ">")) {
			t.Errorf("default rule %d is %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestA33_every_parser_has_a_fuzz_target_and_a_row(t *testing.T) {
	root := "../../.."
	parse, err := os.ReadFile("parse.go")
	if err != nil {
		t.Fatal(err)
	}
	parsers := regexp.MustCompile(`(?m)^func (parse[A-Za-z0-9]+)\(`).FindAllStringSubmatch(string(parse), -1)
	if len(parsers) < 5 {
		t.Fatalf("found %d parsers in parse.go, want at least the five of /proc", len(parsers))
	}
	doc, err := os.ReadFile(filepath.Join(root, "docs/security/parsers.md"))
	if err != nil {
		t.Fatal(err)
	}
	fuzz, err := os.ReadFile("fuzz_test.go")
	if err != nil {
		t.Fatal(err)
	}
	for _, p := range parsers {
		name := "metrics." + p[1]
		var row string
		for _, line := range strings.Split(string(doc), "\n") {
			if strings.Contains(line, "`"+name+"`") {
				row = line
			}
		}
		if row == "" {
			t.Errorf("docs/security/parsers.md has no row for %s", name)
			continue
		}
		target := regexp.MustCompile("`(Fuzz[A-Za-z0-9]+)`").FindStringSubmatch(row)
		if target == nil || !strings.Contains(string(fuzz), "func "+target[1]+"(") {
			t.Errorf("the row of %s names fuzz target %v, which fuzz_test.go does not define", name, target)
		}
	}
	cmd := exec.Command("bash", "scripts/fuzz.sh", "--check")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Errorf("scripts/fuzz.sh --check failed: %v\n%s", err, out)
	}
	_ = sort.Strings
}

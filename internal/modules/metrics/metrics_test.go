package metrics

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

var t0 = time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixture under testdata
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// hostFS is a host whose /proc files a test swaps between passes.
func hostFS(t *testing.T, stat, netdev string) fstest.MapFS {
	return fstest.MapFS{
		"proc/stat":        {Data: fixture(t, stat)},
		"proc/meminfo":     {Data: fixture(t, "meminfo")},
		"proc/loadavg":     {Data: fixture(t, "loadavg")},
		"proc/net/dev":     {Data: fixture(t, netdev)},
		"proc/self/mounts": {Data: fixture(t, "mounts")},
	}
}

// fakeStatfs gives each mount point used 1 GB of total 4 GB.
func fakeStatfs(string) (uint64, uint64, error) { return 1 << 30, 4 << 30, nil }

func TestSampleFromFixtures(t *testing.T) {
	src := Source{FS: hostFS(t, "stat-0", "netdev-0"), Statfs: fakeStatfs}
	m := New(src).(module)
	clock := coretest.NewFakeClock(t0)
	tc := core.TaskContext{Env: core.Env{Clock: clock}}
	if err := m.s.pass(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	if got := m.s.ring.snapshot(); len(got) != 0 {
		t.Fatalf("the first pass stored %d samples, want none (it only records the counters)", len(got))
	}
	m.s.src.FS = hostFS(t, "stat-1", "netdev-1")
	clock.Advance(15 * time.Second)
	if err := m.s.pass(context.Background(), tc); err != nil {
		t.Fatal(err)
	}
	got := m.s.ring.snapshot()
	if len(got) != 1 {
		t.Fatalf("after two passes the ring holds %d samples, want 1", len(got))
	}
	s := got[0]
	if !s.TS.Equal(t0.Add(15 * time.Second)) {
		t.Errorf("ts = %s, want %s", s.TS, t0.Add(15*time.Second))
	}
	if s.CPU != 25.0 {
		t.Errorf("cpu = %v, want 25.0 (1,200 ticks, 900 idle or iowait; guest time not counted again)", s.CPU)
	}
	if out, err := (samplesHandler{r: m.s.ring}).Read(context.Background(), tc.Env, SamplesInput{}); err != nil || out.CPUs != 2 {
		t.Errorf("metrics.samples.get answered cpus %d (%v), want 2 (the cpu0 and cpu1 lines)", out.CPUs, err)
	}
	t.Run("cpu count stops after the cpu lines", func(t *testing.T) {
		stat := "cpu  1 0 1 8 0 0 0 0\ncpu0 1 0 1 8 0 0 0 0\ncpu1 0 0 0 0 0 0 0 0\ncpu12 0 0 0 0 0 0 0 0\n" +
			"ctxt 1\ncpu7 0 0 0 0\nintr " + strings.Repeat("0 ", 70000) + "\n"
		got, err := parseProcStat([]byte(stat))
		if err != nil {
			t.Fatal(err)
		}
		if got.cpus != 3 || got.total != 10 || got.idle != 8 {
			t.Errorf("parseProcStat = %+v, want 3 cpus, 10 ticks, 8 idle", got)
		}
	})
	for _, c := range []struct {
		name      string
		got, want uint64
	}{
		{"memUsed", s.MemUsed, 2_048_000_000}, {"memTotal", s.MemTotal, 8_192_000_000},
		{"swapUsed", s.SwapUsed, 256_000_000}, {"swapTotal", s.SwapTotal, 1_024_000_000},
	} {
		if c.got != c.want {
			t.Errorf("%s = %d, want %d", c.name, c.got, c.want)
		}
	}
	if s.Load1 != 0.5 || s.Load5 != 0.25 || s.Load15 != 0.1 {
		t.Errorf("load = %v %v %v, want 0.5 0.25 0.1", s.Load1, s.Load5, s.Load15)
	}
	wantMounts := []string{"/"}
	for k := 1; k <= 15; k++ {
		wantMounts = append(wantMounts, "/data"+string(rune('0'+k/10))+string(rune('0'+k%10)))
	}
	var mounts []string
	for _, f := range s.FS {
		mounts = append(mounts, f.Mount)
		if f.Used != 1<<30 || f.Total != 4<<30 {
			t.Errorf("fs %s = %d of %d, want the statfs figures", f.Mount, f.Used, f.Total)
		}
	}
	if !slices.Equal(mounts, wantMounts) {
		t.Errorf("mounts = %v, want %v", mounts, wantMounts)
	}
	if len(s.Net) != 16 {
		t.Fatalf("net has %d interfaces, want 16", len(s.Net))
	}
	for _, r := range s.Net {
		if r.Iface == "lo" {
			t.Error("net includes lo")
		}
	}
	if eth := s.Net[0]; eth.Iface != "eth0" || eth.RxBps != 100_000 || eth.TxBps != 10_000 {
		t.Errorf("net[0] = %+v, want eth0 at 100000 rx and 10000 tx bytes per second", eth)
	}
	if last := s.Net[15]; last.Iface != "veth15" || last.RxBps != 0 {
		t.Errorf("net[15] = %+v, want veth15 at 0 (the first 16 by name)", last)
	}

	t.Run("escaped mount point", func(t *testing.T) {
		got, err := parseMounts([]byte("/dev/sdc1 /mnt/my\\040disk xfs rw 0 0\n/dev/sdd1 /boot/efi vfat rw 0 0\n"))
		if err != nil || !slices.Equal(got, []string{"/boot/efi", "/mnt/my disk"}) {
			t.Errorf("parseMounts = %q, %v", got, err)
		}
	})
	t.Run("only local filesystem types", func(t *testing.T) {
		got, err := parseMounts([]byte("tmpfs /a tmpfs rw 0 0\nproc /b proc rw 0 0\noverlay /c overlay rw 0 0\n/dev/x /d ext3 rw 0 0\n/dev/y /e btrfs rw 0 0\ntank /f zfs rw 0 0\n"))
		if err != nil || !slices.Equal(got, []string{"/d", "/e", "/f"}) {
			t.Errorf("parseMounts = %q, %v, want /d /e /f", got, err)
		}
	})
	t.Run("a mount statfs cannot measure is left out", func(t *testing.T) {
		m := New(Source{FS: hostFS(t, "stat-0", "netdev-0"), Statfs: func(p string) (uint64, uint64, error) {
			if p == "/data01" {
				return 0, 0, errors.New("stale")
			}
			return fakeStatfs(p)
		}}).(module)
		clock := coretest.NewFakeClock(t0)
		tc := core.TaskContext{Env: core.Env{Clock: clock}}
		_ = m.s.pass(context.Background(), tc)
		m.s.src.FS = hostFS(t, "stat-1", "netdev-1")
		clock.Advance(15 * time.Second)
		if err := m.s.pass(context.Background(), tc); err != nil {
			t.Fatal(err)
		}
		got := map[string]FSUsage{}
		for _, f := range m.s.ring.snapshot()[0].FS {
			if f.Mount == "/data01" {
				t.Error("the unmeasurable /data01 is in the sample")
			}
			got[f.Mount] = f
		}
		// The other mounts stay, with their figures.
		for _, mount := range []string{"/", "/data02"} {
			if f, ok := got[mount]; !ok || f.Used != 1<<30 || f.Total != 4<<30 {
				t.Errorf("mount %s = %+v (present %v), want used 1 GiB of 4 GiB", mount, f, ok)
			}
		}
	})
	t.Run("a counter that went back gives no rate", func(t *testing.T) {
		m := New(Source{FS: hostFS(t, "stat-1", "netdev-1"), Statfs: fakeStatfs}).(module)
		clock := coretest.NewFakeClock(t0)
		tc := core.TaskContext{Env: core.Env{Clock: clock}}
		_ = m.s.pass(context.Background(), tc)
		m.s.src.FS = hostFS(t, "stat-0", "netdev-0")
		clock.Advance(15 * time.Second)
		if err := m.s.pass(context.Background(), tc); err != nil {
			t.Fatal(err)
		}
		if got := m.s.ring.snapshot(); len(got) != 0 {
			t.Errorf("counters that went back stored %d samples, want none", len(got))
		}
		cur, _ := parseNetDev(fixture(t, "netdev-1"))
		prev, _ := parseNetDev(fixture(t, "netdev-0"))
		for _, r := range netRates(cur, prev, 15) {
			if r.Iface == "eth0" {
				t.Errorf("eth0's counters went back, yet it has the rate %+v", r)
			}
		}
	})
}

// sampleAt is a sample at t whose figures are all v.
func sampleAt(at time.Time, v float64) Sample {
	return Sample{TS: at, CPU: v, MemUsed: uint64(v), MemTotal: 1000, Load1: v,
		FS: []FSUsage{{Mount: "/", Used: uint64(v), Total: 100}}, Net: []NetRate{{Iface: "eth0", RxBps: v, TxBps: 2 * v}}}
}

func TestRingAndMinuteAverages(t *testing.T) {
	r := newRing()
	for i := range 300 {
		r.add(sampleAt(t0.Add(time.Duration(i)*15*time.Second), float64(i)))
	}
	held := r.snapshot()
	if len(held) != 240 {
		t.Fatalf("the ring holds %d samples, want 240", len(held))
	}
	if !held[0].TS.Equal(t0.Add(15*time.Minute)) || !held[239].TS.Equal(t0.Add(75*time.Minute-15*time.Second)) {
		t.Errorf("the ring holds %s to %s, want the last 240", held[0].TS, held[239].TS)
	}

	clock := coretest.NewFakeClock(t0.Add(75*time.Minute - 10*time.Second)) // 13:14:50: minute 13:14 is not over
	h := samplesHandler{r: r}
	read := func(since string) []Sample {
		t.Helper()
		out, err := h.Read(context.Background(), core.Env{Clock: clock}, SamplesInput{Since: since})
		if err != nil {
			t.Fatal(err)
		}
		if !out.Now.Equal(clock.Now()) {
			t.Errorf("now = %s, want the agent clock %s", out.Now, clock.Now())
		}
		return out.Samples
	}
	all := read("")
	if len(all) != 59 {
		t.Fatalf("got %d minutes, want 59 (12:15 to 13:13; 13:14 is not complete)", len(all))
	}
	first := all[0]
	if !first.TS.Equal(t0.Add(15*time.Minute)) || first.CPU != 61.5 || first.MemUsed != 62 || first.Load1 != 61.5 {
		t.Errorf("first minute = %+v, want 12:15 averaging samples 60 to 63 (61.5)", first)
	}
	if len(first.FS) != 1 || first.FS[0].Used != 62 || first.FS[0].Total != 100 {
		t.Errorf("first minute fs = %+v", first.FS)
	}
	if len(first.Net) != 1 || first.Net[0].RxBps != 61.5 || first.Net[0].TxBps != 123 {
		t.Errorf("first minute net = %+v", first.Net)
	}
	for i := 1; i < len(all); i++ {
		if !all[i].TS.After(all[i-1].TS) {
			t.Fatalf("minutes not oldest first at %d", i)
		}
	}
	clock.Advance(10 * time.Second)
	if got := read(""); len(got) != 60 || !got[59].TS.Equal(t0.Add(74*time.Minute)) {
		t.Errorf("at 13:15:00 got %d minutes, want 60 ending 13:14", len(got))
	}
	since := read(t0.Add(30 * time.Minute).Format(time.RFC3339))
	if len(since) != 44 || !since[0].TS.Equal(t0.Add(31*time.Minute)) {
		t.Errorf("since 12:30 got %d minutes from %v, want 44 from 12:31", len(since), since[0].TS)
	}
	if got := read(t0.Add(2 * time.Hour).Format(time.RFC3339)); got == nil || len(got) != 0 {
		t.Errorf("since a future time got %v, want an empty list", got)
	}
	_, err := h.Read(context.Background(), core.Env{Clock: clock}, SamplesInput{Since: "yesterday"})
	if err == nil || errcode.From(err).Code != errcode.InvalidInput {
		t.Errorf("since yesterday: %v, want %s", err, errcode.InvalidInput)
	}

	t.Run("at most 60", func(t *testing.T) {
		r := newRing()
		for i := range 300 { // every 20 s: 240 samples span 80 minutes
			r.add(sampleAt(t0.Add(time.Duration(i)*20*time.Second), float64(i)))
		}
		got := minuteAverages(r.snapshot(), time.Time{}, t0.Add(2*time.Hour))
		if len(got) != 60 || !got[0].TS.Equal(t0.Add(20*time.Minute)) || !got[59].TS.Equal(t0.Add(79*time.Minute)) {
			t.Errorf("got %d minutes, want the oldest 60, 12:20 to 13:19", len(got))
		}
	})
}

// countingFS counts reads of proc/stat, the sampler's first file.
type countingFS struct {
	fstest.MapFS
	reads *atomic.Int64
}

func (c countingFS) Open(name string) (fs.File, error) {
	if name == "proc/stat" {
		c.reads.Add(1)
	}
	return c.MapFS.Open(name)
}

// ReadFile hides MapFS's, which fs.ReadFile would call instead of Open.
func (c countingFS) ReadFile(name string) ([]byte, error) {
	if name == "proc/stat" {
		c.reads.Add(1)
	}
	return c.MapFS.ReadFile(name)
}

// agentHost opens a real agent runtime over a new database, serving the
// module over fixtures; flag, when not nil, is stored as
// module_enabled.metrics before the runtime opens.
func agentHost(t *testing.T, flag *string) (*agentruntime.Runtime, *store.DB, *atomic.Int64) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideAgent, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}, coreSet, nil); err != nil {
		t.Fatal(err)
	}
	if flag != nil {
		if err := db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, agentruntime.MetaModuleEnabledPrefix+ID, *flag)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	reads := &atomic.Int64{}
	reg, err := core.NewRegistry(New(Source{FS: countingFS{MapFS: hostFS(t, "stat-0", "netdev-0"), reads: reads}, Statfs: fakeStatfs}))
	if err != nil {
		t.Fatal(err)
	}
	rt, err := agentruntime.Open(ctx, agentruntime.Config{
		DB: db, Sealer: coretest.Sealer{}, Clock: coretest.NewFakeClock(t0), Log: slog.New(slog.DiscardHandler),
		Registry: reg, HostID: "h1", BinaryVersion: "test", BackupDir: filepath.Join(dir, "backups"),
		Exec: platform.NewRunner(nil), FilesTestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	t.Cleanup(func() { once.Do(rt.Close) })
	return rt, db, reads
}

func TestEnabledOnExistingHosts(t *testing.T) {
	_, _, reads := agentHost(t, nil)
	deadline := time.Now().Add(5 * time.Second)
	for reads.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if reads.Load() == 0 {
		t.Error("with no module_enabled.metrics key the sampler did not run")
	}

	off := "0"
	_, _, reads = agentHost(t, &off)
	time.Sleep(200 * time.Millisecond)
	if n := reads.Load(); n != 0 {
		t.Errorf("with module_enabled.metrics 0 the sampler read /proc/stat %d times", n)
	}
}

func TestSamplesReadIsNotJournaled(t *testing.T) {
	rt, db, _ := agentHost(t, nil)
	ctx := context.Background()
	count := func() int {
		var n int
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_journal`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := count()
	p := core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeRead}
	for range 60 {
		raw, err := rt.Read(ctx, p, core.ActionRef{Name: ActionSamples, Version: 1}, json.RawMessage(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		var out SamplesOutput
		if err := json.Unmarshal(raw, &out); err != nil || out.Samples == nil {
			t.Fatalf("metrics.samples.get answered %s (%v)", raw, err)
		}
	}
	if after := count(); after != before {
		t.Errorf("60 reads of metrics.samples.get wrote %d journal entries, want none", after-before)
	}
}

// TestControllerHalfIsWired checks that the module, as the registry and the
// server's contributions see it, declares the controller migrations and
// metrics.history.get and contributes the poller and the rollup.
func TestControllerHalfIsWired(t *testing.T) {
	m := Module()
	man := m.Manifest()
	if man.Migrations[core.SideController] == nil {
		t.Error("the manifest declares no controller migrations")
	}
	if _, err := fs.Stat(man.Migrations[core.SideController], "0001_metrics.sql"); err != nil {
		t.Errorf("the controller migrations lack 0001_metrics.sql: %v", err)
	}
	var names []string
	for _, def := range man.Actions {
		names = append(names, def.Spec.Name)
	}
	if !slices.Contains(names, "metrics.history.get") || !slices.Contains(names, ActionSamples) {
		t.Errorf("the manifest declares %v", names)
	}
	ctl, ok := m.(ext.Controller)
	if !ok {
		t.Fatal("the module has no controller contribution")
	}
	db, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	_, svcs, err := ctl.Controller(ext.Deps{DB: db, Dispatch: &coretest.FakeDispatcher{}, Clock: coretest.NewFakeClock(t0)})
	if err != nil {
		t.Fatal(err)
	}
	var svcNames []string
	for _, s := range svcs {
		svcNames = append(svcNames, s.Name)
	}
	if !slices.Equal(svcNames, []string{"metrics.poll", "metrics.rollup"}) {
		t.Errorf("the module contributes %v, want the poller and the rollup", svcNames)
	}
	if _, svcs, _ := ctl.Controller(ext.Deps{}); len(svcs) != 0 {
		t.Errorf("with no database the module contributes %d services", len(svcs))
	}
}

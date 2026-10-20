package rollout

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/update"
)

var (
	_ dispatch.ClientSource = (*fakeClients)(nil)
	_ ReleaseSource         = (*update.IndexSource)(nil)
)

// fakeClients tells which hosts have a live session.
type fakeClients struct {
	mu     sync.Mutex
	online map[string]bool
}

func (c *fakeClients) Client(_ context.Context, hostID string) (core.AgentClient, uint64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.online[hostID] {
		return nil, 0, offline(hostID)
	}
	return &coretest.FakeAgent{}, 1, nil
}

func (c *fakeClients) set(hostID string, online bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.online[hostID] = online
}

// fakeIndex is the release index.
type fakeIndex struct {
	mu       sync.Mutex
	releases []update.Release
	err      error
}

func (x *fakeIndex) Releases(context.Context) ([]update.Release, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	return slices.Clone(x.releases), x.err
}

// schedEnv is a controller with hosts OWN (its own), A and B on 1.2.0, all
// online, and an index publishing 1.2.0 and 1.3.0 stable and 1.4.0-beta.1.
// Its clock starts at testStart, Monday 12:00 UTC.
type schedEnv struct {
	db      *store.DB
	clock   *coretest.FakeClock
	clients *fakeClients
	index   *fakeIndex
	audit   *coretest.MemJournal
}

func newSchedEnv(t *testing.T) *schedEnv {
	t.Helper()
	e := &schedEnv{
		db: openDB(t), clock: coretest.NewFakeClock(testStart),
		clients: &fakeClients{online: map[string]bool{"OWN": true, "A": true, "B": true}},
		index: &fakeIndex{releases: []update.Release{
			{Version: "1.2.0", Channel: core.ChannelStable},
			{Version: "1.3.0", Channel: core.ChannelStable},
			{Version: "1.4.0-beta.1", Channel: core.ChannelBeta},
		}},
		audit: &coretest.MemJournal{},
	}
	addHost(t, e.db, "OWN", true, "1.2.0")
	addHost(t, e.db, "A", false, "1.2.0")
	addHost(t, e.db, "B", false, "1.2.0")
	return e
}

// schedule gives host a weekly window.
func (e *schedEnv) schedule(t *testing.T, host, channel, days, hhmm string) {
	t.Helper()
	exec(t, e.db, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, ?, ?, ?)
		ON CONFLICT (host_id) DO UPDATE SET channel = excluded.channel, days = excluded.days, time_utc = excluded.time_utc`,
		host, channel, days, hhmm)
}

// start starts a scheduler of a controller running version, stopped when
// the test ends.
func (e *schedEnv) start(t *testing.T, version string) *Scheduler {
	t.Helper()
	s, err := NewScheduler(SchedulerConfig{
		DB: e.db, Clients: e.clients, Index: e.index, Audit: e.audit, Clock: e.clock, Version: version,
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Stop)
	return s
}

// minutes runs n scheduler passes.
func (e *schedEnv) minutes(n int) {
	for range n {
		e.clock.Advance(ScheduleInterval)
	}
}

// claim reads a host's last_window and last_rollout_id.
func (e *schedEnv) claim(t *testing.T, host string) (window, rolloutID string) {
	t.Helper()
	var w, r sql.NullString
	if err := e.db.Read().QueryRow("SELECT last_window, last_rollout_id FROM core_host_update WHERE host_id = ?", host).
		Scan(&w, &r); err != nil {
		t.Fatal(err)
	}
	return w.String, r.String
}

// rollouts lists every rollout, newest first.
func (e *schedEnv) rollouts(t *testing.T) []record {
	t.Helper()
	rs, err := list(context.Background(), e.db.Read(), 100)
	if err != nil {
		t.Fatal(err)
	}
	return rs
}

// finishAll marks every rollout succeeded.
func (e *schedEnv) finishAll(t *testing.T) {
	t.Helper()
	exec(t, e.db, "UPDATE core_rollouts SET state = 'succeeded' WHERE state IN ('running', 'draining')")
}

func TestOpenWindow(t *testing.T) {
	monday := testStart // 2026-10-19 12:00 UTC
	tests := []struct {
		name      string
		now       time.Time
		days, at  string
		window    string
		wantFound bool
	}{
		{"before it opens", monday, "mon", "12:01", "", false},
		{"as it opens", monday, "mon", "12:00", "2026-10-19T12:00", true},
		{"inside", monday.Add(119 * time.Minute), "mon,thu", "12:00", "2026-10-19T12:00", true},
		{"as it closes", monday.Add(2 * time.Hour), "mon", "12:00", "", false},
		{"another day", monday, "tue,sun", "12:00", "", false},
		{"across midnight", time.Date(2026, 10, 20, 0, 45, 0, 0, time.UTC), "mon", "23:30", "2026-10-19T23:30", true},
		{"after midnight on the next listed day", time.Date(2026, 10, 20, 0, 45, 0, 0, time.UTC), "mon,tue", "00:30", "2026-10-20T00:30", true},
		{"a day not listed after midnight", time.Date(2026, 10, 20, 0, 45, 0, 0, time.UTC), "tue", "23:30", "", false},
		{"sunday", time.Date(2026, 10, 25, 6, 0, 0, 0, time.UTC), "sun", "05:00", "2026-10-25T05:00", true},
		{"a malformed time", monday, "mon", "1200", "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			w, ok := openWindow(tt.now, tt.days, tt.at)
			if w != tt.window || ok != tt.wantFound {
				t.Errorf("openWindow = %q, %v; want %q, %v", w, ok, tt.window, tt.wantFound)
			}
		})
	}
}

func TestSchedulerClaimsTheOwnHostAheadOfTheController(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "OWN", core.ChannelStable, "mon,thu", "12:00")
	e.start(t, "1.2.0")
	e.minutes(1)
	rs := e.rollouts(t)
	if len(rs) != 1 {
		t.Fatalf("%d rollouts, want 1", len(rs))
	}
	r := rs[0]
	if r.Version != "1.3.0" || r.Channel != core.ChannelStable || r.State != core.RolloutRunning || r.ChangeID != "" ||
		!slices.Equal(hostStates(r), []string{"OWN=waiting"}) {
		t.Errorf("rollout = %+v", r)
	}
	if w, id := e.claim(t, "OWN"); w != "2026-10-19T12:00" || id != r.ID {
		t.Errorf("claim = %q, %q; want the window and rollout %s", w, id, r.ID)
	}
	entries := e.audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("audit entries = %+v, want one", entries)
	}
	a := entries[0]
	var in core.RolloutInput
	if err := json.Unmarshal(a.Input, &in); err != nil {
		t.Fatal(err)
	}
	if a.Actor != core.ActorUpdateSchedule || a.Action != ActionRollout || a.Version != 1 || a.Phase != "apply" ||
		a.Result != "ok" || a.ChangeID != "" || !slices.Equal(in.Hosts, []string{"OWN"}) || in.Version != "1.3.0" {
		t.Errorf("audit entry = %+v, input %+v", a, in)
	}
	e.finishAll(t)
	e.minutes(30)
	if n := len(e.rollouts(t)); n != 1 {
		t.Errorf("%d rollouts after the window was claimed, want 1", n)
	}
}

func TestSchedulerHoldsAnAgentHostBehindTheController(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "A", core.ChannelStable, "mon", "12:00")
	s := e.start(t, "1.2.0")
	e.minutes(5)
	if n := len(e.rollouts(t)); n != 0 {
		t.Fatalf("%d rollouts while the controller is older than the target, want 0", n)
	}
	if w, _ := e.claim(t, "A"); w != "" {
		t.Fatalf("last window = %q, want none claimed", w)
	}
	s.Stop()
	e.start(t, "1.3.0")
	e.minutes(1)
	rs := e.rollouts(t)
	if len(rs) != 1 || !slices.Equal(hostStates(rs[0]), []string{"A=waiting"}) || rs[0].Version != "1.3.0" {
		t.Fatalf("rollouts = %+v, want one for A once the controller runs 1.3.0", rs)
	}
}

func TestSchedulerRetryAndSkipRules(t *testing.T) {
	t.Run("offline host", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		e.clients.set("OWN", false)
		e.start(t, "1.2.0")
		e.minutes(3)
		if n := len(e.rollouts(t)); n != 0 {
			t.Fatalf("%d rollouts for an offline host", n)
		}
		e.clients.set("OWN", true)
		e.minutes(1)
		if n := len(e.rollouts(t)); n != 1 {
			t.Fatalf("%d rollouts once the host is back in its window, want 1", n)
		}
	})
	t.Run("active rollout", func(t *testing.T) {
		e := newSchedEnv(t)
		mustCreate(t, e.db, "1.2.0", "1.2.0", "B")
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		e.start(t, "1.2.0")
		e.minutes(3)
		if n := len(e.rollouts(t)); n != 1 {
			t.Fatalf("%d rollouts while another is active, want only that one", n)
		}
		e.finishAll(t)
		e.minutes(1)
		if rs := e.rollouts(t); len(rs) != 2 || rs[0].Hosts[0].HostID != "OWN" {
			t.Fatalf("rollouts = %+v, want the window claimed once the other ended", rs)
		}
	})
	t.Run("one claim per pass", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "B", core.ChannelStable, "mon", "12:00")
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		e.start(t, "1.3.0")
		e.minutes(1)
		rs := e.rollouts(t)
		if len(rs) != 1 || rs[0].Hosts[0].HostID != "OWN" {
			t.Fatalf("rollouts = %+v, want only the own host's", rs)
		}
		e.finishAll(t)
		e.minutes(1)
		if rs := e.rollouts(t); len(rs) != 2 || rs[0].Hosts[0].HostID != "B" {
			t.Fatalf("rollouts = %+v, want B's next", rs)
		}
	})
	t.Run("nothing newer", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		exec(t, e.db, "UPDATE core_hosts SET agent_version = '1.3.0' WHERE id = 'OWN'")
		e.start(t, "1.3.0")
		e.minutes(3)
		if n := len(e.rollouts(t)); n != 0 {
			t.Fatalf("%d rollouts without a newer release", n)
		}
	})
	t.Run("beta channel", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelBeta, "mon", "12:00")
		e.start(t, "1.2.0")
		e.minutes(1)
		if rs := e.rollouts(t); len(rs) != 1 || rs[0].Version != "1.4.0-beta.1" || rs[0].Channel != core.ChannelBeta {
			t.Fatalf("rollouts = %+v, want the newest beta", rs)
		}
	})
	t.Run("unknown agent version", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		e.schedule(t, "A", core.ChannelStable, "mon", "12:00")
		exec(t, e.db, "UPDATE core_hosts SET agent_version = 'dev' WHERE id = 'OWN'")
		exec(t, e.db, "UPDATE core_hosts SET agent_version = NULL WHERE id = 'A'")
		e.start(t, "1.3.0")
		e.minutes(3)
		if n := len(e.rollouts(t)); n != 0 {
			t.Fatalf("%d rollouts for hosts of unknown versions", n)
		}
	})
	t.Run("missed window", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelStable, "mon", "09:00")
		s := e.start(t, "1.2.0")
		e.minutes(60)
		if n := len(e.rollouts(t)); n != 0 {
			t.Fatalf("%d rollouts for a window that closed at 11:00", n)
		}
		s.Stop()
		e.clock.Advance(7*24*time.Hour - 4*time.Hour - time.Minute)
		e.start(t, "1.2.0")
		e.minutes(1)
		if rs := e.rollouts(t); len(rs) != 1 {
			t.Fatalf("rollouts = %+v, want next week's window claimed", rs)
		}
		if w, _ := e.claim(t, "OWN"); w != "2026-10-26T09:00" {
			t.Errorf("last window = %q", w)
		}
	})
	t.Run("index unavailable", func(t *testing.T) {
		e := newSchedEnv(t)
		e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
		e.index.err = errors.New("index: connection refused")
		e.start(t, "1.2.0")
		e.minutes(2)
		if n := len(e.rollouts(t)); n != 0 {
			t.Fatalf("%d rollouts without an index", n)
		}
		e.index.mu.Lock()
		e.index.err = nil
		e.index.mu.Unlock()
		e.minutes(1)
		if n := len(e.rollouts(t)); n != 1 {
			t.Fatalf("%d rollouts once the index answers, want 1", n)
		}
	})
}

func TestSchedulerClaimIsAtomic(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
	e.audit.Err = errors.New("audit: disk full")
	e.start(t, "1.2.0")
	e.minutes(2)
	if n := len(e.rollouts(t)); n != 0 {
		t.Fatalf("%d rollouts after a failed audit entry, want the claim rolled back", n)
	}
	if w, id := e.claim(t, "OWN"); w != "" || id != "" {
		t.Fatalf("claim = %q, %q after a failed audit entry", w, id)
	}
	e.audit.Err = nil
	e.minutes(1)
	rs := e.rollouts(t)
	if w, id := e.claim(t, "OWN"); len(rs) != 1 || w != "2026-10-19T12:00" || id != rs[0].ID {
		t.Fatalf("claim = %q, %q, rollouts %d", w, id, len(rs))
	}
}

func TestNewSchedulerRequiresItsDependencies(t *testing.T) {
	e := newSchedEnv(t)
	full := SchedulerConfig{DB: e.db, Clients: e.clients, Index: e.index, Audit: e.audit, Clock: e.clock, Version: "1.2.0"}
	for _, drop := range []func(*SchedulerConfig){
		func(c *SchedulerConfig) { c.DB = nil },
		func(c *SchedulerConfig) { c.Clients = nil },
		func(c *SchedulerConfig) { c.Index = nil },
		func(c *SchedulerConfig) { c.Audit = nil },
		func(c *SchedulerConfig) { c.Clock = nil },
		func(c *SchedulerConfig) { c.Version = "" },
	} {
		cfg := full
		drop(&cfg)
		if _, err := NewScheduler(cfg); err == nil {
			t.Errorf("NewScheduler(%+v) succeeded", cfg)
		}
	}
}

func TestSchedulerStop(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
	s := e.start(t, "1.2.0")
	s.Start()
	if n := e.clock.Pending(); n != 1 {
		t.Fatalf("pending timers = %d after two Starts, want 1", n)
	}
	s.Stop()
	if n := e.clock.Pending(); n != 0 {
		t.Fatalf("pending timers = %d after Stop, want 0", n)
	}
	e.minutes(3)
	if n := len(e.rollouts(t)); n != 0 {
		t.Fatalf("%d rollouts after Stop", n)
	}
}

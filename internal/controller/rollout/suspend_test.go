package rollout

import (
	"context"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// susSwitch is a Suspended function the test flips; err, when set, is what
// every call answers.
type susSwitch struct {
	on  atomic.Bool
	err atomic.Pointer[error]
}

func (s *susSwitch) suspended(context.Context) (bool, error) {
	if err := s.err.Load(); err != nil {
		return false, *err
	}
	return s.on.Load(), nil
}

func (s *susSwitch) fail(err error) { s.err.Store(&err) }

func (s *susSwitch) clear() { s.err.Store(nil) }

func TestDriverSkipsPassesWhileSuspended(t *testing.T) {
	e := newDriverEnv(t)
	r := mustCreate(t, e.db, "1.3.0", "1.2.0", "OWN")
	var sw susSwitch
	sw.on.Store(true)
	var logs lockedBuffer
	d, err := NewDriver(DriverConfig{DB: e.db, Dispatcher: e.fleet, Clock: e.clock, Suspended: sw.suspended,
		Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn}))})
	if err != nil {
		t.Fatal(err)
	}
	d.Start()
	t.Cleanup(d.Stop)

	e.passes(3)
	e.want(t, r.ID, core.RolloutRunning, "OWN=waiting")
	if calls := e.fleet.callsOf("OWN"); len(calls) != 0 {
		t.Fatalf("calls while suspended: %v, want none", calls)
	}

	sw.fail(errors.New("the quarantine flag cannot be read"))
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=waiting")
	if n := logs.count("the pass is skipped"); n != 1 {
		t.Fatalf("%d skip warnings after a failed read, want 1:\n%s", n, logs.String())
	}

	sw.clear()
	sw.on.Store(false)
	e.passes(1)
	e.want(t, r.ID, core.RolloutRunning, "OWN=updating")
}

func TestSchedulerSkipsPassesWhileSuspended(t *testing.T) {
	e := newSchedEnv(t)
	e.schedule(t, "OWN", core.ChannelStable, "mon", "12:00")
	var sw susSwitch
	sw.on.Store(true)
	var logs lockedBuffer
	s, err := NewScheduler(SchedulerConfig{
		DB: e.db, Clients: e.clients, Index: e.index, Audit: e.audit, Clock: e.clock, Version: "1.2.0",
		Suspended: sw.suspended, Log: slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelWarn})),
	})
	if err != nil {
		t.Fatal(err)
	}
	s.Start()
	t.Cleanup(s.Stop)

	e.minutes(3)
	if rs := e.rollouts(t); len(rs) != 0 {
		t.Fatalf("rollouts while suspended: %+v, want none", rs)
	}

	sw.fail(errors.New("the quarantine flag cannot be read"))
	e.minutes(1)
	if rs := e.rollouts(t); len(rs) != 0 {
		t.Fatalf("rollouts after a failed read: %+v, want none", rs)
	}
	if n := logs.count("the pass is skipped"); n != 1 {
		t.Fatalf("%d skip warnings after a failed read, want 1:\n%s", n, logs.String())
	}

	sw.clear()
	sw.on.Store(false)
	e.minutes(1)
	if rs := e.rollouts(t); len(rs) != 1 {
		t.Fatalf("%d rollouts once resumed, want 1", len(rs))
	}
}

package server

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
)

var errSvBoom = errors.New("the service failed")

// svService is a scripted controller service: each Run reports when it
// started on runs, then returns what the test sends on results, panics with
// what it sends on panics, or returns at shutdown, once hold is closed when
// hold is set.
type svService struct {
	clock   core.Clock
	runs    chan time.Time
	results chan error
	panics  chan any
	hold    chan struct{}
}

func newSvService(clock core.Clock) *svService {
	return &svService{clock: clock, runs: make(chan time.Time, 16), results: make(chan error), panics: make(chan any)}
}

func (s *svService) run(ctx context.Context) error {
	s.runs <- s.clock.Now()
	select {
	case err := <-s.results:
		return err
	case v := <-s.panics:
		panic(v)
	case <-ctx.Done():
		if s.hold != nil {
			<-s.hold
		}
		return ctx.Err()
	}
}

func (s *svService) service(name string) ext.Service { return ext.Service{Name: name, Run: s.run} }

// svNextRun waits for the service's next Run and returns when it started.
func svNextRun(t *testing.T, s *svService) time.Time {
	t.Helper()
	select {
	case at := <-s.runs:
		return at
	case <-time.After(5 * time.Second):
		t.Fatal("the service did not run")
		return time.Time{}
	}
}

// svNoRun fails when the service runs within a short real-time wait.
func svNoRun(t *testing.T, s *svService) {
	t.Helper()
	select {
	case at := <-s.runs:
		t.Fatalf("the service ran at %v, want no run", at)
	case <-time.After(50 * time.Millisecond):
	}
}

// svWaitPending waits until the clock has n armed timers.
func svWaitPending(t *testing.T, clock *coretest.FakeClock, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for clock.Pending() != n {
		if time.Now().After(deadline) {
			t.Fatalf("pending timers = %d, want %d", clock.Pending(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

// svFailAndWait makes the running Run fail and waits for its restart delay
// to be armed.
func svFailAndWait(t *testing.T, s *svService, clock *coretest.FakeClock) {
	t.Helper()
	s.results <- errSvBoom
	svWaitPending(t, clock, 1)
}

func TestServiceRunnerBacksOff(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.backoff")}, Clock: clock})
	t.Cleanup(r.stop)
	at := svNextRun(t, svc)
	if !at.Equal(t0) {
		t.Fatalf("first run at %v, want %v", at, t0)
	}
	for _, d := range []time.Duration{
		time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second, 32 * time.Second,
		64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute,
	} {
		svFailAndWait(t, svc, clock)
		clock.Advance(d - time.Nanosecond)
		if n := clock.Pending(); n != 1 {
			t.Fatalf("the service restarted before its delay of %v", d)
		}
		clock.Advance(time.Nanosecond)
		got := svNextRun(t, svc)
		if want := at.Add(d); !got.Equal(want) {
			t.Fatalf("restart at %v, want %v (a delay of %v)", got, want, d)
		}
		at = got
	}
}

func TestServiceRunnerResetsTheDelayAfterALongRun(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.reset")}, Clock: clock})
	t.Cleanup(r.stop)
	svNextRun(t, svc)
	for _, d := range []time.Duration{time.Second, 2 * time.Second} {
		svFailAndWait(t, svc, clock)
		clock.Advance(d)
		svNextRun(t, svc)
	}
	// This run lasts five minutes, so the next delay starts over at 1 s.
	clock.Advance(5 * time.Minute)
	svFailAndWait(t, svc, clock)
	clock.Advance(time.Second - time.Nanosecond)
	if n := clock.Pending(); n != 1 {
		t.Fatal("the service restarted before 1 s")
	}
	clock.Advance(time.Nanosecond)
	svNextRun(t, svc)
}

func TestServiceRunnerRestartsAnEarlyReturnAndAPanic(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	var logs lockedBuffer
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{svc.service("test.early")}, Clock: clock, Log: slog.New(slog.NewTextHandler(&logs, nil)),
	})
	t.Cleanup(r.stop)
	svNextRun(t, svc)

	svc.results <- nil
	svWaitPending(t, clock, 1)
	clock.Advance(time.Second)
	svNextRun(t, svc)
	if !strings.Contains(logs.String(), "returned before shutdown") {
		t.Errorf("the log does not report the early return:\n%s", logs.String())
	}

	svc.panics <- "a secret the service held"
	svWaitPending(t, clock, 1)
	clock.Advance(2 * time.Second)
	svNextRun(t, svc)
	out := logs.String()
	if !strings.Contains(out, "panicked with a string") || strings.Contains(out, "a secret the service held") {
		t.Errorf("the log reports the panic as %q, want its type only", out)
	}
}

func TestServiceRunnerWaitsWhileSuspended(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	var suspended, unreadable atomic.Bool
	suspended.Store(true)
	var logs lockedBuffer
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{svc.service("test.suspended")}, Clock: clock, Log: slog.New(slog.NewTextHandler(&logs, nil)),
		Suspended: func(context.Context) (bool, error) {
			if unreadable.Load() {
				return false, errors.New("the flag cannot be read")
			}
			return suspended.Load(), nil
		},
	})
	t.Cleanup(r.stop)

	svWaitPending(t, clock, 1)
	svNoRun(t, svc)
	clock.Advance(serviceDefaultPoll)
	svWaitPending(t, clock, 1)
	svNoRun(t, svc)

	unreadable.Store(true)
	clock.Advance(serviceDefaultPoll)
	svWaitPending(t, clock, 1)
	svNoRun(t, svc)
	if !strings.Contains(logs.String(), "cannot tell whether controller services are suspended") {
		t.Errorf("the log does not report the failed read:\n%s", logs.String())
	}

	unreadable.Store(false)
	suspended.Store(false)
	clock.Advance(serviceDefaultPoll)
	if at := svNextRun(t, svc); !at.Equal(t0.Add(3 * serviceDefaultPoll)) {
		t.Errorf("first run at %v, want %v", at, t0.Add(3*serviceDefaultPoll))
	}
}

func TestServiceRunnerStopWaitsForRun(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	svc := newSvService(clock)
	svc.hold = make(chan struct{})
	r := startServiceRunner(serviceRunnerConfig{Services: []ext.Service{svc.service("test.stop")}, Clock: clock})
	svNextRun(t, svc)
	stopped := make(chan struct{})
	go func() {
		r.stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("stop returned while Run was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(svc.hold)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("stop did not return after Run returned")
	}
}

// TestServiceRunnerStopsWhileWaiting: stop ends a suspended wait and a
// restart delay at once, leaving no timer armed and no service started.
func TestServiceRunnerStopsWhileWaiting(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	suspendedSvc, failedSvc := newSvService(clock), newSvService(clock)
	r := startServiceRunner(serviceRunnerConfig{
		Services: []ext.Service{suspendedSvc.service("test.waiting")}, Clock: clock,
		Suspended: func(context.Context) (bool, error) { return true, nil },
	})
	svWaitPending(t, clock, 1)
	r.stop()
	svNoRun(t, suspendedSvc)
	if n := clock.Pending(); n != 0 {
		t.Errorf("pending timers after stop = %d, want 0", n)
	}

	r = startServiceRunner(serviceRunnerConfig{Services: []ext.Service{failedSvc.service("test.failed")}, Clock: clock})
	svNextRun(t, failedSvc)
	svFailAndWait(t, failedSvc, clock)
	r.stop()
	if n := clock.Pending(); n != 0 {
		t.Errorf("pending timers after stop = %d, want 0", n)
	}
	clock.Advance(time.Minute)
	svNoRun(t, failedSvc)
}

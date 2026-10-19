package transport

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakePinger answers pings from a script: each entry is one ping's result,
// and pings past the end succeed. A blocking fake never answers.
type fakePinger struct {
	mu       sync.Mutex
	script   []error
	pings    int
	block    bool
	done     chan struct{}
	closeOne sync.Once
}

func newFakePinger(script ...error) *fakePinger {
	return &fakePinger{script: script, done: make(chan struct{})}
}

func (f *fakePinger) Ping() (time.Duration, error) {
	f.mu.Lock()
	f.pings++
	block := f.block
	var err error
	if len(f.script) > 0 {
		err, f.script = f.script[0], f.script[1:]
	}
	f.mu.Unlock()
	if block {
		<-f.done
		return 0, errors.New("closed")
	}
	return time.Millisecond, err
}

func (f *fakePinger) Done() <-chan struct{} { return f.done }

func (f *fakePinger) Close() error {
	f.closeOne.Do(func() { close(f.done) })
	return nil
}

func (f *fakePinger) closed() bool {
	select {
	case <-f.done:
		return true
	default:
		return false
	}
}

func (f *fakePinger) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pings
}

// runHeartbeat runs heartbeat on p and returns its result, failing the test
// when it runs longer than five seconds.
func runHeartbeat(ctx context.Context, t *testing.T, p pinger, cfg HeartbeatConfig) error {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- heartbeat(ctx, p, cfg) }()
	select {
	case err := <-done:
		return err
	case <-time.After(5 * time.Second):
		t.Fatal("the heartbeat did not return")
		return nil
	}
}

func TestHeartbeatDefaults(t *testing.T) {
	got := HeartbeatConfig{}.withDefaults()
	if got.Interval != 15*time.Second || got.Timeout != 10*time.Second || got.Failures != 3 {
		t.Fatalf("defaults = %+v", got)
	}
	set := HeartbeatConfig{Interval: time.Second, Timeout: 2 * time.Second, Failures: 5}.withDefaults()
	if set.Interval != time.Second || set.Timeout != 2*time.Second || set.Failures != 5 {
		t.Fatalf("explicit values replaced: %+v", set)
	}
}

func TestHeartbeatSuccessResetsCount(t *testing.T) {
	fail := errors.New("no answer")
	p := newFakePinger(fail, fail, nil, fail, fail, nil, fail, fail, fail)
	var alive atomic.Int32
	cfg := HeartbeatConfig{Interval: time.Millisecond, Timeout: time.Second, OnAlive: func() { alive.Add(1) }}
	err := runHeartbeat(context.Background(), t, p, cfg)
	if err == nil || !errors.Is(err, fail) {
		t.Fatalf("heartbeat = %v, want the third consecutive failure", err)
	}
	if n := p.count(); n != 9 {
		t.Errorf("pings = %d, want 9: two failures followed by a success must not close", n)
	}
	if n := alive.Load(); n != 2 {
		t.Errorf("OnAlive calls = %d, want 2", n)
	}
	if !p.closed() {
		t.Error("the session is still open after three failed pings")
	}
}

func TestHeartbeatTimesOut(t *testing.T) {
	p := newFakePinger()
	p.block = true
	err := runHeartbeat(context.Background(), t, p, HeartbeatConfig{Interval: time.Millisecond, Timeout: 5 * time.Millisecond, Failures: 2})
	if err == nil || !p.closed() {
		t.Fatalf("heartbeat = %v, closed %v; want unanswered pings to close the session", err, p.closed())
	}
}

func TestHeartbeatStops(t *testing.T) {
	t.Run("context ends", func(t *testing.T) {
		p := newFakePinger()
		ctx, cancel := context.WithCancel(context.Background())
		var alive atomic.Int32
		cfg := HeartbeatConfig{Interval: time.Millisecond, OnAlive: func() {
			if alive.Add(1) == 3 {
				cancel()
			}
		}}
		if err := runHeartbeat(ctx, t, p, cfg); err != nil {
			t.Fatalf("heartbeat = %v, want nil", err)
		}
		if p.closed() {
			t.Error("the heartbeat closed the session when its context ended")
		}
	})
	t.Run("session closes", func(t *testing.T) {
		p := newFakePinger()
		if err := p.Close(); err != nil {
			t.Fatal(err)
		}
		if err := runHeartbeat(context.Background(), t, p, HeartbeatConfig{Interval: time.Millisecond}); err != nil {
			t.Fatalf("heartbeat = %v, want nil", err)
		}
	})
}

func TestHeartbeatOverSession(t *testing.T) {
	t.Run("answered", func(t *testing.T) {
		ctrl, _ := sessionPair(t)
		ctx, cancel := context.WithCancel(context.Background())
		var alive atomic.Int32
		cfg := HeartbeatConfig{Interval: 5 * time.Millisecond, OnAlive: func() {
			if alive.Add(1) == 3 {
				cancel()
			}
		}}
		done := make(chan error, 1)
		go func() { done <- Heartbeat(ctx, ctrl, cfg) }()
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Heartbeat = %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Fatal("no three answered pings within five seconds")
		}
	})
	t.Run("unanswered", func(t *testing.T) {
		// Nothing reads the far end, so no ping is ever answered.
		near, far := net.Pipe()
		t.Cleanup(func() { _ = far.Close() })
		ctrl, err := ControllerSession(near)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() {
			done <- Heartbeat(context.Background(), ctrl, HeartbeatConfig{Interval: 5 * time.Millisecond, Timeout: 20 * time.Millisecond})
		}()
		select {
		case err := <-done:
			if err == nil {
				t.Fatal("Heartbeat = nil, want an error")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("unanswered pings did not end the heartbeat")
		}
		select {
		case <-ctrl.Done():
		default:
			t.Fatal("the session is still open")
		}
	})
}

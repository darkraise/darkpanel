package update

import (
	"context"
	"errors"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// healthAnswers returns the n-th answer on the n-th call and repeats the last
// one; it fails a probe called without a deadline.
type healthAnswers[T any] struct {
	mu      sync.Mutex
	calls   int
	answers []healthAnswer[T]
}

type healthAnswer[T any] struct {
	value T
	err   error
}

func (h *healthAnswers[T]) probe(ctx context.Context) (T, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	var zero T
	if _, ok := ctx.Deadline(); !ok {
		return zero, errors.New("probe called without a deadline")
	}
	a := h.answers[min(h.calls, len(h.answers)-1)]
	h.calls++
	return a.value, a.err
}

func healthCtrl(version, status string) healthAnswer[ControllerHealth] {
	return healthAnswer[ControllerHealth]{value: ControllerHealth{Status: status, Version: version}}
}

func healthCtrlErr(err error) healthAnswer[ControllerHealth] {
	return healthAnswer[ControllerHealth]{err: err}
}

func healthHello(version string, connected *bool) healthAnswer[core.AgentHello] {
	return healthAnswer[core.AgentHello]{value: core.AgentHello{BinaryVersion: version, ControllerConnected: connected}}
}

func healthAgentErr(err error) healthAnswer[core.AgentHello] {
	return healthAnswer[core.AgentHello]{err: err}
}

// runHealthWait runs Wait with a deadline HealthDeadline ahead and advances
// the fake clock one HealthInterval whenever Wait sleeps. It returns how many
// times the clock had to move and Wait's error.
func runHealthWait(t *testing.T, p Probes) (int, error) {
	t.Helper()
	clock := coretest.NewFakeClock(time.Date(2026, 10, 19, 12, 0, 0, 0, time.UTC))
	deadline := clock.Now().Add(HealthDeadline)
	done := make(chan error, 1)
	go func() { done <- p.Wait(context.Background(), clock, "1.2.0", "1.3.0", deadline, nil) }()
	advances := 0
	for {
		select {
		case err := <-done:
			return advances, err
		case <-time.After(time.Millisecond):
		}
		if clock.Pending() > 0 {
			clock.Advance(HealthInterval)
			advances++
		}
		if advances > 100 {
			t.Fatal("Wait did not stop after the deadline")
		}
	}
}

func TestProbesWait(t *testing.T) {
	yes, no := true, false
	deadlineAdvances := int(HealthDeadline / HealthInterval)
	tests := []struct {
		name       string
		controller []healthAnswer[ControllerHealth] // nil: the host has no controller
		agent      []healthAnswer[core.AgentHello]
		wantErr    string
		advances   int
	}{
		{"both pass at once", []healthAnswer[ControllerHealth]{healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 0},
		{"controller refused, then old, then new", []healthAnswer[ControllerHealth]{
			healthCtrlErr(errors.New("connection refused")), healthCtrl("1.2.0", "ok"), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 2},
		{"controller timeout is not yet", []healthAnswer[ControllerHealth]{
			healthCtrlErr(context.DeadlineExceeded), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 1},
		{"new controller degraded fails at once", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "ok"), healthCtrl("1.3.0", "degraded")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, `status "degraded"`, 1},
		{"old controller degraded is not yet", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "degraded"), healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "", 1},
		{"controller never moves", []healthAnswer[ControllerHealth]{healthCtrl("1.2.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &yes)}, "deadline", deadlineAdvances},
		{"agent refused, then old, then new", nil, []healthAnswer[core.AgentHello]{
			healthAgentErr(errors.New("connection refused")), healthHello("1.2.0", &yes), healthHello("1.3.0", &yes)}, "", 2},
		{"agent not connected to the controller yet", nil, []healthAnswer[core.AgentHello]{
			healthHello("1.3.0", &no), healthHello("1.3.0", &yes)}, "", 1},
		{"unenrolled agent passes on its version", nil, []healthAnswer[core.AgentHello]{healthHello("1.3.0", nil)}, "", 0},
		{"agent never connects", []healthAnswer[ControllerHealth]{healthCtrl("1.3.0", "ok")},
			[]healthAnswer[core.AgentHello]{healthHello("1.3.0", &no)}, "deadline", deadlineAdvances},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var p Probes
			if tt.controller != nil {
				p.Controller = (&healthAnswers[ControllerHealth]{answers: tt.controller}).probe
			}
			p.Agent = (&healthAnswers[core.AgentHello]{answers: tt.agent}).probe
			advances, err := runHealthWait(t, p)
			switch {
			case tt.wantErr == "" && err != nil:
				t.Fatalf("Wait = %v, want nil", err)
			case tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)):
				t.Fatalf("Wait = %v, want an error mentioning %q", err, tt.wantErr)
			}
			if advances != tt.advances {
				t.Errorf("the clock moved %d times, want %d", advances, tt.advances)
			}
		})
	}
}

func TestProbesWaitStops(t *testing.T) {
	clock := coretest.NewFakeClock(time.Unix(0, 0))
	if err := (Probes{}).Wait(context.Background(), clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), nil); err == nil {
		t.Error("Wait without an agent probe passed")
	}
	p := Probes{Agent: (&healthAnswers[core.AgentHello]{answers: []healthAnswer[core.AgentHello]{healthHello("1.2.0", nil)}}).probe}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- p.Wait(ctx, clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), nil) }()
	for clock.Pending() == 0 {
		time.Sleep(time.Millisecond)
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Errorf("Wait after cancel = %v, want context.Canceled", err)
	}
}

func TestProbesWaitStopsWhenMoved(t *testing.T) {
	clock := coretest.NewFakeClock(time.Unix(0, 0))
	agent := &healthAnswers[core.AgentHello]{answers: []healthAnswer[core.AgentHello]{healthHello("1.2.0", nil)}}
	p := Probes{Agent: agent.probe}
	asked := 0
	moved := func() bool {
		asked++
		return asked > 1
	}
	done := make(chan error, 1)
	go func() {
		done <- p.Wait(context.Background(), clock, "1.2.0", "1.3.0", clock.Now().Add(time.Minute), moved)
	}()
	for clock.Pending() == 0 {
		time.Sleep(time.Millisecond)
	}
	clock.Advance(HealthInterval)
	if err := <-done; !errors.Is(err, errMoved) {
		t.Errorf("Wait once the record moved = %v, want errMoved", err)
	}
	if asked != 2 {
		t.Errorf("moved was asked %d times, want 2: before each round of probes", asked)
	}
}

func TestControllerProbe(t *testing.T) {
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "controller.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	status := http.StatusOK
	var mu sync.Mutex
	srv := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.Method != http.MethodGet || r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"status":"ok","version":"1.3.0","commit":"abc","schemaVersion":5}`))
	})}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	probe := ControllerProbe(socket)
	got, err := probe(ctx)
	if err != nil || got != (ControllerHealth{Status: "ok", Version: "1.3.0"}) {
		t.Fatalf("probe = %+v, %v", got, err)
	}
	mu.Lock()
	status = http.StatusServiceUnavailable
	mu.Unlock()
	if _, err := probe(ctx); err == nil {
		t.Error("a 503 answer was accepted")
	}
	if _, err := ControllerProbe(filepath.Join(dir, "missing.sock"))(ctx); err == nil {
		t.Error("a missing socket was accepted")
	}
}

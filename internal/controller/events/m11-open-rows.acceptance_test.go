package events_test

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/events"
	"github.com/darkraise/darkpanel/internal/core"
)

type qaRechecker struct {
	mu      sync.Mutex
	calls   []string
	called  chan struct{}
	release chan struct{}
}

func (r *qaRechecker) RecheckModules(_ context.Context, host string, modules []string) error {
	r.mu.Lock()
	r.calls = append(r.calls, host+":"+strings.Join(modules, ","))
	r.mu.Unlock()
	r.called <- struct{}{}
	<-r.release
	return nil
}

func (r *qaRechecker) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.calls)
}

// The agent journals agent.policy.reload naming the modules a reload changed;
// the controller rechecks exactly those modules of that host, off the thread
// that ingests the entries.
func TestA32_ControllerRechecksTheModulesAPolicyReloadChanged(t *testing.T) {
	rc := &qaRechecker{called: make(chan struct{}, 4), release: make(chan struct{})}
	defer close(rc.release)
	on := events.PolicyReloads(t.Context(), func(string) { t.Error("repush although the recheck had no failure yet") }, nil, events.WithRechecker(rc))

	returned := make(chan struct{})
	go func() {
		on("h7", []core.JournalEntry{
			{Action: "console.terminal.open", Phase: "apply", Input: []byte(`{"modules":["console"]}`)},
			{Action: "agent.policy.reload", Phase: "apply", Result: "ok", Input: []byte(`{"modules":["console","jobs"]}`)},
		})
		close(returned)
	}()
	select {
	case <-returned:
	case <-time.After(5 * time.Second):
		t.Fatal("the ingestion callback waited for the recheck to finish")
	}
	select {
	case <-rc.called:
	case <-time.After(5 * time.Second):
		t.Fatal("the policy reload entry caused no recheck")
	}
	if got, want := rc.snapshot(), []string{"h7:console,jobs"}; !slices.Equal(got, want) {
		t.Errorf("rechecks %q, want %q", got, want)
	}
}

func TestA32_EntriesThatAreNoPolicyReloadRecheckNothing(t *testing.T) {
	rc := &qaRechecker{called: make(chan struct{}, 4), release: make(chan struct{})}
	defer close(rc.release)
	on := events.PolicyReloads(t.Context(), func(string) { t.Error("repush") }, nil, events.WithRechecker(rc))
	on("h7", []core.JournalEntry{
		{Action: "services.unit.start", Phase: "apply", Input: []byte(`{"modules":["console"]}`)},
		{Action: "agent.policy.reload", Phase: "apply", Input: []byte(`{"modules":[]}`)},
	})
	select {
	case <-rc.called:
		t.Errorf("a recheck ran for entries naming no policy reload of any module: %q", rc.snapshot())
	case <-time.After(300 * time.Millisecond):
	}
}

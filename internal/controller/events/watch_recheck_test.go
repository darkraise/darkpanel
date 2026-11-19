package events

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

type recordRechecker struct {
	mu    sync.Mutex
	calls []string
	got   chan struct{}
}

func (r *recordRechecker) RecheckModules(_ context.Context, host string, modules []string) error {
	r.mu.Lock()
	r.calls = append(r.calls, host+":"+strings.Join(modules, ","))
	r.mu.Unlock()
	r.got <- struct{}{}
	return nil
}

// settled waits for one recheck, or a short while when none is wanted, and returns the calls so far.
func (r *recordRechecker) settled(want bool) []string {
	wait := 200 * time.Millisecond
	if want {
		wait = 5 * time.Second
	}
	select {
	case <-r.got:
	case <-time.After(wait):
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.calls...)
}

type wrcIn struct{}
type wrcOut struct{}
type wrcHandler struct{}

func (wrcHandler) Plan(context.Context, core.Env, wrcIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}
func (wrcHandler) Apply(context.Context, core.Env, wrcIn) (wrcOut, error) { return wrcOut{}, nil }
func (wrcHandler) Verify(context.Context, core.Env, wrcIn, wrcOut) error  { return nil }

// wrcModule declares tre.thing.compose (controller-side) and tre.thing.task (agent-side), both rechecking docker.
type wrcModule struct{}

func (wrcModule) Manifest() core.Manifest {
	spec := func(name string, side core.Side) core.ActionSpec {
		return core.ActionSpec{Name: name, Version: 1, Class: core.ClassIrreversible, Side: side, Rechecks: []string{"docker"}}
	}
	return core.Manifest{ID: "tre", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating[wrcIn, wrcOut](spec("tre.thing.compose", core.SideController), wrcHandler{}),
		core.DefineMutating[wrcIn, wrcOut](spec("tre.thing.task", core.SideAgent), wrcHandler{}),
	}}
}

// stubDocker is a module with the id docker and no actions, so Rechecks can name it.
type stubDocker struct{}

func (stubDocker) Manifest() core.Manifest { return core.Manifest{ID: "docker", Version: 1} }

type watchFixture struct {
	*pubEnv
	reg  *core.Registry
	rec  *recordRechecker
	opts []Option
}

func newWatchFixture(t *testing.T, rec *recordRechecker) *watchFixture {
	t.Helper()
	reg, err := core.NewRegistry(wrcModule{}, stubDocker{})
	if err != nil {
		t.Fatal(err)
	}
	f := &watchFixture{pubEnv: newPubEnv(t), reg: reg, rec: rec}
	f.opts = []Option{WithRegistry(reg), WithRechecker(rec)}
	return f
}

func hideAll(core.ChangeView) bool { return false }

// firstPoll starts the watchers, whose first poll only records.
func (f *watchFixture) firstPoll() {
	stop := StartWatchers(f.db, f.clock, f.bus, hideAll, nil, f.opts...)
	f.t.Cleanup(stop)
}

func (f *watchFixture) poll() { f.clock.Advance(PollInterval) }

// ctrlChange inserts a controller-side change of action with one composite step on host.
func (f *watchFixture) ctrlChange(id, action, host, state string) {
	at := f.clock.Now()
	f.exec(`INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, '', ?, 1, 'user:u1', 'h', 's', '[]', '[]', '', ?, ?)`,
		"P"+id, action, pubAt(at), pubAt(at.Add(10*time.Minute)))
	f.exec(`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		input_json, created_at, updated_at) VALUES (?, ?, '', ?, 1, 'irreversible', '', 'user:u1', ?, '{}', ?, ?)`,
		id, "P"+id, action, state, pubAt(at), pubAt(at))
	f.exec(`INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, input_template, state, updated_at)
		VALUES (?, 's1', 0, ?, 'tre.thing.task/v1', '{}', 'applied', ?)`, id, host, pubAt(at))
}

// agentChange feeds one ingested change of action on host to AgentChanges.
func (f *watchFixture) agentChange(host, id, action, state string) {
	read := func(context.Context, string, string) (core.ChangeView, error) {
		return core.ChangeView{ID: id, HostID: host, Action: action, Version: 1, State: state}, nil
	}
	AgentChanges(f.bus, read, hideAll, nil, f.opts...)(host, []core.JournalEntry{{ChangeID: id, Input: json.RawMessage(`{}`)}})
}

func TestWatcherRechecksOnTerminalStates(t *testing.T) {
	states := map[string]bool{
		core.StateCompleted: true, core.StateConfirmed: true, core.StateReverted: true, core.StateFailed: true,
		core.StateNeedsRepair: true, core.StateApplying: false, core.StateAwaitingConfirmation: false,
	}
	for state, want := range states {
		for _, kind := range []string{"controller-composite", "agent-task"} {
			t.Run(kind+"/"+state, func(t *testing.T) {
				rec := &recordRechecker{got: make(chan struct{}, 4)}
				f := newWatchFixture(t, rec)
				f.firstPoll()
				switch kind {
				case "controller-composite":
					f.ctrlChange("c1", "tre.thing.compose", "h1", state)
					f.poll()
				case "agent-task":
					f.agentChange("h1", "a1", "tre.thing.task", state)
				}
				calls := rec.settled(want)
				if got := len(calls) == 1 && calls[0] == "h1:docker"; got != want {
					t.Fatalf("calls = %v, want recheck=%v", calls, want)
				}
			})
		}
	}
}

func TestWatcherFirstPollOnlyRecords(t *testing.T) {
	rec := &recordRechecker{got: make(chan struct{}, 4)}
	f := newWatchFixture(t, rec)
	f.ctrlChange("c1", "tre.thing.compose", "h1", core.StateCompleted)
	f.firstPoll()
	f.poll()
	if calls := rec.settled(false); len(calls) != 0 {
		t.Fatalf("a change that ended before the watchers started was rechecked: %v", calls)
	}
}

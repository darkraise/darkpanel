package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// bgClock is a fake clock that counts the timers armed for each duration,
// so a test can wait until a task has scheduled its next pass before it
// advances the clock.
type bgClock struct {
	*coretest.FakeClock
	mu    sync.Mutex
	armed map[time.Duration]int
	ch    chan struct{} // closed and replaced whenever a timer is armed
}

func (c *bgClock) AfterFunc(d time.Duration, f func()) core.Timer {
	tm := c.FakeClock.AfterFunc(d, f)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.armed[d]++
	close(c.ch)
	c.ch = make(chan struct{})
	return tm
}

// awaitArmed waits until n timers of d have been armed in all.
func (c *bgClock) awaitArmed(t *testing.T, d time.Duration, n int) {
	t.Helper()
	guard := time.After(10 * time.Second)
	for {
		c.mu.Lock()
		got, ch := c.armed[d], c.ch
		c.mu.Unlock()
		if got >= n {
			return
		}
		select {
		case <-ch:
		case <-guard:
			t.Fatalf("%d timers of %s armed after 10 s, want %d", got, d, n)
		}
	}
}

func (c *bgClock) armedFor(d time.Duration) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.armed[d]
}

// bgRun is one pass as Run saw it start.
type bgRun struct {
	task string
	at   time.Time
}

// bgHost is what the bg modules' checks, tasks and actions look at and record.
type bgHost struct {
	clock  core.Clock
	passes chan bgRun  // every pass, as it starts
	wakes  chan func() // the wake of every Watch that stays up

	mu            sync.Mutex
	down          map[string]bool // modules whose preflight fails
	fail          bool            // Run fails
	block         bool            // Run waits until its context ends
	gate          chan struct{}   // when set, Run waits until it closes
	watchErrs     int             // Watch calls still to fail at once
	runsReturned  int
	watchReturned int
	tc            core.TaskContext // the last pass's
	applied       []string
}

func bgNewHost() *bgHost {
	return &bgHost{passes: make(chan bgRun, 64), wakes: make(chan func(), 16), down: map[string]bool{}}
}

func (h *bgHost) set(f func(h *bgHost)) {
	h.mu.Lock()
	defer h.mu.Unlock()
	f(h)
}

func (h *bgHost) counts() (runs, watches int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.runsReturned, h.watchReturned
}

func (h *bgHost) run(task string) func(context.Context, core.TaskContext) error {
	return func(ctx context.Context, tc core.TaskContext) error {
		h.mu.Lock()
		h.tc = tc
		fail, block, gate := h.fail, h.block, h.gate
		h.mu.Unlock()
		defer h.set(func(h *bgHost) { h.runsReturned++ })
		h.passes <- bgRun{task, h.clock.Now()}
		if gate != nil {
			select {
			case <-gate:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		if block {
			<-ctx.Done()
			return ctx.Err()
		}
		if fail {
			return errors.New("bg: the pass failed")
		}
		return nil
	}
}

func (h *bgHost) watch(ctx context.Context, _ core.Env, wake func()) error {
	defer h.set(func(h *bgHost) { h.watchReturned++ })
	h.mu.Lock()
	fail := h.watchErrs > 0
	if fail {
		h.watchErrs--
	}
	h.mu.Unlock()
	if fail {
		return errors.New("bg: the event stream broke")
	}
	h.wakes <- wake
	<-ctx.Done()
	return ctx.Err()
}

// pass waits for the next pass.
func (h *bgHost) pass(t *testing.T) bgRun {
	t.Helper()
	select {
	case p := <-h.passes:
		return p
	case <-time.After(10 * time.Second):
		t.Fatal("no pass within 10 s")
		return bgRun{}
	}
}

// noPass fails when a pass has started that the test has not taken.
func (h *bgHost) noPass(t *testing.T) {
	t.Helper()
	select {
	case p := <-h.passes:
		t.Fatalf("unexpected pass %+v", p)
	default:
	}
}

type bgItemIn struct {
	Name string `json:"name"`
}

// bgItemSet records each apply with its version and actor.
type bgItemSet struct {
	h       *bgHost
	version int
}

func (bgItemSet) Plan(_ context.Context, _ core.Env, in bgItemIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name}}, StateHash: "bg"}, nil
}

func (x bgItemSet) Apply(_ context.Context, env core.Env, in bgItemIn) (bgItemIn, error) {
	x.h.set(func(h *bgHost) {
		h.applied = append(h.applied, fmt.Sprintf("v%d %s by %s", x.version, in.Name, env.Principal.Actor))
	})
	return in, nil
}

func (bgItemSet) Verify(context.Context, core.Env, bgItemIn, bgItemIn) error { return nil }

// bgModule declares <id>.item.set versions 1 and 2, a preflight check that
// fails while h.down[id] is set and, unless idle, the task <id>.sync.
type bgModule struct {
	id    string
	off   bool // DefaultEnabled false
	idle  bool // no background task
	watch bool // the task has a Watch
	every time.Duration
	h     *bgHost
}

func (m bgModule) Manifest() core.Manifest {
	set := func(version int) core.ActionDef {
		return core.DefineMutating(core.ActionSpec{
			Name: m.id + ".item.set", Version: version, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{m.id}, ForwardOnly: true, Converges: true,
		}, bgItemSet{m.h, version})
	}
	return core.Manifest{
		ID: m.id, Version: 1, DefaultEnabled: !m.off, Actions: []core.ActionDef{set(1), set(2)},
		Preflight: []core.PreflightCheck{{ID: "ready", Run: func(context.Context, core.Env) error {
			m.h.mu.Lock()
			defer m.h.mu.Unlock()
			if m.h.down[m.id] {
				return fmt.Errorf("%s: the host is not ready", m.id)
			}
			return nil
		}}},
	}
}

func (m bgModule) BackgroundTasks() []core.BackgroundTask {
	if m.idle {
		return nil
	}
	task := core.BackgroundTask{ID: m.id + ".sync", Every: m.every, LockKeys: []string{m.id}, Run: m.h.run(m.id + ".sync")}
	if m.watch {
		task.Watch = m.h.watch
	}
	return []core.BackgroundTask{task}
}

// bgOpen opens a runtime of mods alone on a bgClock; h reads that clock.
func bgOpen(t *testing.T, h *bgHost, mods ...core.Module) (*fixture, *bgClock) {
	t.Helper()
	ctx := context.Background()
	reg, err := core.NewRegistry(mods...)
	if err != nil {
		t.Fatal(err)
	}
	clock := &bgClock{FakeClock: coretest.NewFakeClock(t0), armed: map[time.Duration]int{}, ch: make(chan struct{})}
	h.clock = clock
	f := &fixture{t: t, clock: clock.FakeClock, reg: reg, dir: t.TempDir()}
	if f.db, err = store.Open(ctx, filepath.Join(f.dir, "agent.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.db.Close() })
	cfg := f.config()
	cfg.Clock = clock
	if f.rt, err = Open(ctx, cfg); err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(f.rt.Close)
	return f, clock
}

func TestBackgroundStartsEnabledTasks(t *testing.T) {
	h := bgNewHost()
	h.down["bgdown"] = true
	_, clock := bgOpen(t, h, bgModule{id: "bg", every: 10 * time.Second, h: h},
		bgModule{id: "bgoff", off: true, every: 10 * time.Second, h: h},
		bgModule{id: "bgdown", every: 10 * time.Second, h: h})
	if p := h.pass(t); p.task != "bg.sync" || !p.at.Equal(t0) {
		t.Errorf("first pass = %+v, want bg.sync at the start", p)
	}
	// Open started only bg's task, which has now scheduled its next pass.
	clock.awaitArmed(t, 10*time.Second, 1)
	h.noPass(t)
	if n := clock.armedFor(10 * time.Second); n != 1 {
		t.Errorf("%d tasks scheduled a pass, want bg.sync alone", n)
	}
}

func TestBackgroundEvery(t *testing.T) {
	h := bgNewHost()
	gate := make(chan struct{})
	h.gate = gate
	_, clock := bgOpen(t, h, bgModule{id: "bg", every: 10 * time.Second, h: h})
	h.pass(t)
	// The first pass takes 3 s.
	clock.Advance(3 * time.Second)
	close(gate)
	clock.awaitArmed(t, 10*time.Second, 1)
	clock.Advance(7 * time.Second)
	h.noPass(t)
	clock.Advance(3 * time.Second)
	if p := h.pass(t); !p.at.Equal(t0.Add(13 * time.Second)) {
		t.Errorf("second pass at %s, want 10 s after the first ended (%s)", p.at, t0.Add(13*time.Second))
	}
}

func TestBackgroundWakeCoalesces(t *testing.T) {
	h := bgNewHost()
	gate := make(chan struct{})
	h.gate = gate
	_, clock := bgOpen(t, h, bgModule{id: "bg", every: 10 * time.Second, watch: true, h: h})
	h.pass(t)
	var wake func()
	select {
	case wake = <-h.wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not start")
	}
	wake()
	wake()
	wake()
	close(gate)
	h.pass(t)
	// The further pass ended and scheduled the next one; nothing else ran.
	clock.awaitArmed(t, 10*time.Second, 2)
	h.noPass(t)
}

func TestBackgroundSkipsHeldLock(t *testing.T) {
	for _, key := range []string{"bg", lifecycle.ReserveAll} {
		t.Run(key, func(t *testing.T) {
			h := bgNewHost()
			f, clock := bgOpen(t, h, bgModule{id: "bg", every: 10 * time.Second, h: h})
			h.pass(t)
			clock.awaitArmed(t, 10*time.Second, 1)
			if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES (?, '01HOLDER')", key); err != nil {
				t.Fatal(err)
			}
			clock.Advance(10 * time.Second)
			clock.awaitArmed(t, 10*time.Second, 2)
			h.noPass(t)
			if n := clock.armedFor(TaskBackoffMin); n != 0 {
				t.Errorf("the skipped pass backed off %d times, want a skip without error", n)
			}
			if _, err := f.db.Writer().Exec("DELETE FROM core_locks WHERE key = ?", key); err != nil {
				t.Fatal(err)
			}
			clock.Advance(10 * time.Second)
			if p := h.pass(t); !p.at.Equal(t0.Add(20 * time.Second)) {
				t.Errorf("pass at %s, want the tick after the lock went (%s)", p.at, t0.Add(20*time.Second))
			}
		})
	}
}

func TestBackgroundBackoff(t *testing.T) {
	h := bgNewHost()
	h.fail = true
	_, clock := bgOpen(t, h, bgModule{id: "bg", every: 10 * time.Second, h: h})
	h.pass(t)
	backoffs := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second,
		32 * time.Second, 64 * time.Second, 128 * time.Second, 256 * time.Second, 5 * time.Minute, 5 * time.Minute}
	armed := map[time.Duration]int{}
	for i, d := range backoffs {
		armed[d]++
		clock.awaitArmed(t, d, armed[d])
		if i == len(backoffs)-1 {
			h.set(func(h *bgHost) { h.fail = false })
		}
		clock.Advance(d)
		h.pass(t)
	}
	// The successful pass is back on the Every schedule, and the next
	// failure starts the backoff again at 1 s.
	clock.awaitArmed(t, 10*time.Second, 1)
	h.set(func(h *bgHost) { h.fail = true })
	clock.Advance(10 * time.Second)
	h.pass(t)
	clock.awaitArmed(t, time.Second, 2)
}

func TestBackgroundWatchRestart(t *testing.T) {
	h := bgNewHost()
	h.watchErrs = 1
	_, clock := bgOpen(t, h, bgModule{id: "bg", watch: true, h: h})
	h.pass(t)
	clock.awaitArmed(t, TaskBackoffMin, 1)
	clock.Advance(TaskBackoffMin)
	var wake func()
	select {
	case wake = <-h.wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not restart")
	}
	// The restart woke the task once; the watch's own wake runs another pass.
	h.pass(t)
	wake()
	h.pass(t)
}

func TestBackgroundWatchBackoff(t *testing.T) {
	h := bgNewHost()
	h.watchErrs = 3
	_, clock := bgOpen(t, h, bgModule{id: "bg", watch: true, h: h})
	h.pass(t)
	// The pass after each restart succeeds, and the watch's backoff still
	// grows.
	for _, d := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second} {
		clock.awaitArmed(t, d, 1)
		clock.Advance(d)
		h.pass(t)
	}
	select {
	case <-h.wakes:
	case <-time.After(10 * time.Second):
		t.Fatal("the watch did not restart")
	}
	if n := clock.armedFor(time.Second); n != 1 {
		t.Errorf("%d timers of 1 s armed, want the first restart's alone", n)
	}
}

func TestDisableStopsTasks(t *testing.T) {
	h := bgNewHost()
	h.block = true
	f, _ := bgOpen(t, h, bgModule{id: "bg", watch: true, h: h})
	ctx := context.Background()
	h.pass(t)
	<-h.wakes
	if err := f.rt.SetModuleEnabled(ctx, "bg", false); err != nil {
		t.Fatal(err)
	}
	if runs, watches := h.counts(); runs != 1 || watches != 1 {
		t.Errorf("after the disable %d Runs and %d Watches returned, want 1 and 1", runs, watches)
	}
	h.set(func(h *bgHost) { h.block = false })
	if err := f.rt.SetModuleEnabled(ctx, "bg", true); err != nil {
		t.Fatal(err)
	}
	h.pass(t)
	<-h.wakes

	h.set(func(h *bgHost) { h.down["bg"] = true })
	if st, err := f.rt.Preflight(ctx, "bg"); err != nil || st.Available {
		t.Fatalf("Preflight = %+v, %v; want bg unavailable", st, err)
	}
	if _, watches := h.counts(); watches != 2 {
		t.Errorf("after the failed preflight %d Watches returned, want 2", watches)
	}
	h.set(func(h *bgHost) { h.down["bg"] = false })
	if st, err := f.rt.Preflight(ctx, "bg"); err != nil || !st.Available {
		t.Fatalf("Preflight = %+v, %v; want bg available", st, err)
	}
	h.pass(t)
}

func TestCloseWaitsForTasks(t *testing.T) {
	h := bgNewHost()
	h.block = true
	f, _ := bgOpen(t, h, bgModule{id: "bg", watch: true, h: h})
	h.pass(t)
	<-h.wakes
	f.rt.Close()
	if runs, watches := h.counts(); runs != 1 || watches != 1 {
		t.Errorf("after Close %d Runs and %d Watches returned, want 1 and 1", runs, watches)
	}
}

func TestTaskContextApply(t *testing.T) {
	h := bgNewHost()
	f, _ := bgOpen(t, h, bgModule{id: "bg", h: h}, bgModule{id: "bgo", idle: true, h: h})
	ctx := context.Background()
	h.pass(t)
	h.mu.Lock()
	tc := h.tc
	h.mu.Unlock()
	if want := (core.Principal{Actor: "system:bg.sync", Scope: core.ScopeAdmin}); tc.Env.Principal != want {
		t.Errorf("task principal = %+v, want %+v", tc.Env.Principal, want)
	}

	v, err := tc.Apply(ctx, "bg.item.set", bgItemIn{Name: "a"})
	if err != nil || v.State != core.StateCompleted || v.Version != 2 || v.Actor != "system:bg.sync" {
		t.Fatalf("Apply = %+v, %v; want version 2 completed by system:bg.sync", v, err)
	}
	if n := f.count("SELECT count(*) FROM core_changes WHERE id = ? AND system = 1", v.ID); n != 1 {
		t.Error("the change is not marked system")
	}
	h.mu.Lock()
	applied := slices.Clone(h.applied)
	h.mu.Unlock()
	if want := []string{"v2 a by system:bg.sync"}; !reflect.DeepEqual(applied, want) {
		t.Errorf("applies = %q, want %q", applied, want)
	}

	_, err = tc.Apply(ctx, "bgo.item.set", bgItemIn{Name: "b"})
	if err == nil || !strings.Contains(err.Error(), "background task bg.sync may apply only actions of module bg, not bgo.item.set") {
		t.Errorf("Apply of another module's action = %v, want the module refusal", err)
	}
	_ = wantCode(t, func() error { _, err := tc.Apply(ctx, "bg.item.nope", bgItemIn{}); return err }(), errcode.ActionUnknown)
	_, err = tc.Apply(ctx, "bg.item.set", json.RawMessage(`not json`))
	if err == nil || !strings.Contains(err.Error(), "background task bg.sync: encode the input of bg.item.set") {
		t.Errorf("Apply of an input that does not encode = %v, want the encoding refusal", err)
	}
	if n := f.count("SELECT count(*) FROM core_changes"); n != 1 {
		t.Errorf("%d changes, want only the first", n)
	}
}

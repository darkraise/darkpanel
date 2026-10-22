package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The fixture module "tsk" has task actions over a map of named items.
// tsk.pkg.install has an undo handler and tsk.pkg.pin a recovery handler.
// Task journals one local modify effect and, with fail, fails after
// recording it made.

var (
	refTskInstall = core.ActionRef{Name: "tsk.pkg.install", Version: 1}
	refTskPin     = core.ActionRef{Name: "tsk.pkg.pin", Version: 1}
)

const (
	tskTimeout = 5 * time.Minute
	tskBinary  = "/usr/local/bin/darkpanel"
)

type tskHost struct {
	mu       sync.Mutex
	items    map[string]string
	envs     []core.Env // the Env of every Task call
	recover  core.RecoveryOutcome
	recovers int
	verify   func(ctx context.Context) error // runs first in Verify when set
}

func (h *tskHost) get(name string) string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.items[name]
}

func (h *tskHost) set(name, value string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.items[name] = value
}

type tskIn struct {
	Name  string `json:"name"`
	Value string `json:"value"`
	Fail  bool   `json:"fail,omitempty"`
}

type tskOut struct {
	Value string `json:"value"`
	Token string `json:"token" secret:"true"`
}

type tskBase struct{ h *tskHost }

func (x tskBase) Plan(_ context.Context, _ core.Env, in tskIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{
		Diff:      []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}},
		Guards:    []core.Guard{},
		StateHash: in.Name + "=" + x.h.get(in.Name),
	}, nil
}

func (x tskBase) Task(ctx context.Context, env core.Env, in tskIn) (tskOut, error) {
	x.h.mu.Lock()
	x.h.envs = append(x.h.envs, env)
	x.h.mu.Unlock()
	after := rcvDoc(in.Value)
	e := core.Effect{
		Kind: core.EffectModify, Resource: "tsk.item:" + in.Name, Local: true, InverseLocal: true,
		Before: rcvDoc(x.h.get(in.Name)), After: &after,
	}
	seq, err := env.Effects.Intend(ctx, e, nil)
	if err != nil {
		return tskOut{}, err
	}
	x.h.set(in.Name, in.Value)
	if err := env.Effects.Made(ctx, seq, e.Resource, after); err != nil {
		return tskOut{}, err
	}
	if in.Fail {
		return tskOut{}, errcode.New(errcode.Unavailable, "the package mirror is unreachable")
	}
	return tskOut{Value: in.Value, Token: "tok-" + in.Name}, nil
}

func (x tskBase) Verify(ctx context.Context, _ core.Env, in tskIn, _ tskOut) error {
	x.h.mu.Lock()
	hook := x.h.verify
	x.h.mu.Unlock()
	if hook != nil {
		if err := hook(ctx); err != nil {
			return err
		}
	}
	if got := x.h.get(in.Name); got != in.Value {
		return errcode.New(errcode.Internal, "item "+in.Name+" is "+got)
	}
	return nil
}

func (x tskBase) Observe(_ context.Context, _ core.Env, e core.Effect) (core.Observation, error) {
	name := strings.TrimPrefix(e.Resource, "tsk.item:")
	return core.Observation{Current: rcvDoc(x.h.get(name)), Resource: e.Resource, Matches: 1, Exclusive: true}, nil
}

type tskInstall struct{ tskBase }

func (x tskInstall) Inverse(_ context.Context, _ core.Env, e core.Effect) (core.InverseResult, error) {
	var before struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(e.Before.State, &before); err != nil {
		return core.InverseResult{}, err
	}
	x.h.set(strings.TrimPrefix(e.Resource, "tsk.item:"), before.Value)
	return core.InverseResult{}, nil
}

type tskPin struct{ tskBase }

func (x tskPin) Recover(context.Context, core.Env) (core.RecoveryOutcome, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.recovers++
	return x.h.recover, nil
}

type tskModule struct{ host *tskHost }

func (m tskModule) Manifest() core.Manifest {
	b := tskBase{m.host}
	return core.Manifest{ID: "tsk", Version: 1, Actions: []core.ActionDef{
		core.DefineTask(core.ActionSpec{
			Name: refTskInstall.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"tsk"}, ApplyTimeout: tskTimeout,
		}, tskInstall{b}),
		core.DefineTask(core.ActionSpec{
			Name: refTskPin.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"tsk"}, ApplyTimeout: tskTimeout, ForwardOnly: true, Converges: true,
		}, tskPin{b}),
	}}
}

// taskUnits is a core.Units whose unit states the test scripts. A unit it
// was never told about is not-found.
type taskUnits struct {
	mu       sync.Mutex
	states   map[string]core.UnitState
	started  []string
	services map[string]core.TransientService
	startErr error
	// onStart runs after a start call is recorded; onState at the start of
	// each State call, with the call's number from 1.
	onStart func(name string, svc core.TransientService)
	onState func(n int)
	polls   int
}

func newTaskUnits() *taskUnits {
	return &taskUnits{states: map[string]core.UnitState{}, services: map[string]core.TransientService{}}
}

var errTaskUnitsUnused = errors.New("lifecycle test: the engine called a unit operation it never needs")

func (u *taskUnits) StartTransientService(_ context.Context, name string, svc core.TransientService) error {
	u.mu.Lock()
	u.started = append(u.started, name)
	u.services[name] = svc
	on, err := u.onStart, u.startErr
	u.mu.Unlock()
	if on != nil {
		on(name, svc)
	}
	return err
}

func (u *taskUnits) StartTransientTimer(context.Context, string, core.TransientTimer) error {
	return errTaskUnitsUnused
}
func (u *taskUnits) Start(context.Context, string) error                { return errTaskUnitsUnused }
func (u *taskUnits) Stop(context.Context, string) error                 { return errTaskUnitsUnused }
func (u *taskUnits) Restart(context.Context, string) error              { return errTaskUnitsUnused }
func (u *taskUnits) Kill(context.Context, string, syscall.Signal) error { return errTaskUnitsUnused }
func (u *taskUnits) ResetFailed(context.Context, string) error          { return errTaskUnitsUnused }
func (u *taskUnits) Enable(context.Context, ...string) error            { return errTaskUnitsUnused }
func (u *taskUnits) Disable(context.Context, ...string) error           { return errTaskUnitsUnused }
func (u *taskUnits) Reload(context.Context) error                       { return errTaskUnitsUnused }

func (u *taskUnits) State(_ context.Context, name string) (core.UnitState, error) {
	u.mu.Lock()
	u.polls++
	n, on := u.polls, u.onState
	u.mu.Unlock()
	if on != nil {
		on(n)
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if st, ok := u.states[name]; ok {
		return st, nil
	}
	return core.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
}

func (u *taskUnits) set(name string, active bool) {
	st := core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}
	if active {
		st.ActiveState, st.SubState = "active", "running"
	}
	u.setState(name, st)
}

func (u *taskUnits) setState(name string, st core.UnitState) {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.states[name] = st
}

func (u *taskUnits) startedUnits() []string {
	u.mu.Lock()
	defer u.mu.Unlock()
	return append([]string(nil), u.started...)
}

func (u *taskUnits) service(name string) core.TransientService {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.services[name]
}

type taskFixture struct {
	*fixture
	units *taskUnits
	tsk   *tskHost
}

func newTaskFixture(t *testing.T) *taskFixture {
	t.Helper()
	h := &fixHost{items: map[string]string{}}
	th := &tskHost{items: map[string]string{}}
	reg, err := core.NewRegistry(fixModule{h}, tskModule{th})
	if err != nil {
		t.Fatal(err)
	}
	f := &taskFixture{
		fixture: &fixture{
			t: t, host: h, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
			clock: coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)),
		},
		units: newTaskUnits(), tsk: th,
	}
	f.openTask()
	return f
}

// openTask opens the database and an engine with the fake units, as a
// starting agent does.
func (f *taskFixture) openTask() {
	f.t.Helper()
	f.db = openDBAt(f.t, f.path, store.SideAgent)
	f.journal = NewAgentJournal(f.db)
	cfg := f.config()
	cfg.Units, cfg.TaskBinary = f.units, tskBinary
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
}

// restartTask closes the engine and the database and opens both again; the
// fake units keep their state, as systemd does across an agent restart.
func (f *taskFixture) restartTask() { //nolint:unused // shared helper for the restart tests of task units in this package
	f.t.Helper()
	f.eng.Close()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.openTask()
}

// taskArmed waits until the task follower has finished a poll and armed
// the timer of its next one.
func (f *taskFixture) taskArmed() {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for f.clock.Pending() == 0 {
		if time.Now().After(deadline) {
			f.t.Fatal("the task follower armed no poll")
		}
		time.Sleep(time.Millisecond)
	}
}

// taskPoll waits for the follower's next poll timer, runs between, and
// advances the clock by TaskPoll, so that poll sees what between did.
func (f *taskFixture) taskPoll(between func()) {
	f.t.Helper()
	f.taskArmed()
	if between != nil {
		between()
	}
	f.clock.Advance(TaskPoll)
}

// taskUntil advances the clock poll by poll until change id is in state
// want, and returns its view.
func (f *taskFixture) taskUntil(id, want string) core.ChangeView {
	f.t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		v, err := f.eng.Change(context.Background(), id)
		if err != nil {
			f.t.Fatal(err)
		}
		if v.State == want {
			return v
		}
		if time.Now().After(deadline) {
			f.t.Fatalf("change %s is %s (owner %q), want %s", id, v.State, v.Owner, want)
		}
		if f.clock.Pending() > 0 {
			f.clock.Advance(TaskPoll)
		} else {
			time.Sleep(time.Millisecond)
		}
	}
}

// taskRowTo writes the row of (id, op, attempt) as the agent task process
// would: running with a PID, or a result with out sealed.
func (f *taskFixture) taskRowTo(id, op string, attempt int, state, out, code, msg string) {
	f.t.Helper()
	var ct, finished any
	if out != "" {
		sealed, err := coretest.Sealer{}.Seal(taskOutputAD(id, op, attempt), []byte(out))
		if err != nil {
			f.t.Fatal(err)
		}
		ct = sealed
	}
	if state != taskRowRunning {
		finished = formatTime(f.clock.Now())
	}
	f.recoverExec(`UPDATE core_task_runs SET state = ?, output_ct = ?, error_code = ?, error_message = ?, pid = 4242,
		finished_at = ? WHERE change_id = ? AND op = ? AND attempt = ?`,
		state, ct, code, msg, finished, id, op, attempt)
}

func (f *taskFixture) taskRowState(id, op string, attempt int) string {
	f.t.Helper()
	var state string
	if err := f.db.Read().QueryRow("SELECT state FROM core_task_runs WHERE change_id = ? AND op = ? AND attempt = ?",
		id, op, attempt).Scan(&state); err != nil {
		f.t.Fatal(err)
	}
	return state
}

// taskApplyStart plans and applies ref as alice and returns the change,
// which a task action answers applying.
func (f *taskFixture) taskApplyStart(ref core.ActionRef, input string) core.ChangeView {
	f.t.Helper()
	v := f.apply(alice, f.plan(alice, ref, input).PlanID, core.ApplyOptions{})
	if v.State != core.StateApplying {
		f.t.Fatalf("Apply answered %s, want applying", v.State)
	}
	return v
}

func TestTaskUnitName(t *testing.T) {
	if got := TaskUnitName("01J", core.TaskOpUndo, 2); got != "darkpanel-task-01J-undo-2.service" {
		t.Errorf("TaskUnitName = %q", got)
	}
}

func TestTaskApplySucceeds(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	if f.taskRowState(v.ID, core.TaskOpApply, 1) != taskRowStarting {
		t.Fatal("the intent inserted no starting row")
	}
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskArmed()
	svc := f.units.service(name)
	wantExec := []string{tskBinary, "agent", "task", v.ID, core.TaskOpApply, "1"}
	if !reflect.DeepEqual(svc.ExecStart, wantExec) || svc.RuntimeMax != tskTimeout ||
		svc.CollectMode != "inactive-or-failed" || svc.OnFailure != nil || svc.Description == "" {
		t.Errorf("unit %s = %+v, want ExecStart %v, RuntimeMax %v, CollectMode inactive-or-failed, no OnFailure",
			name, svc, wantExec, tskTimeout)
	}
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	f.taskArmed()
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateApplying || c.Owner != core.OwnerEngine ||
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
		t.Errorf("while the unit runs: %+v, %v; want applying, owned by engine, locked", c, err)
	}
	f.taskPoll(func() {
		f.tsk.set("a", "1")
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
		f.units.set(name, false)
	})
	done := f.taskUntil(v.ID, core.StateCompleted)
	if done.Owner != "" || string(done.Output) != `{"token":"[redacted]","value":"1"}` ||
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 0 {
		t.Errorf("completed change = %+v (output %s), want unowned, unlocked, its output redacted", done, done.Output)
	}
}

func TestTaskApplyFails(t *testing.T) {
	cases := []struct {
		name     string
		running  bool          // the row reached running
		end      string        // the row's last state
		past     time.Duration // how long before now the row started
		code     string
		rowAfter string
	}{
		{"the task failed", true, taskRowFailed, 0, string(errcode.Unavailable), taskRowFailed},
		{"the process died before its deadline", true, taskRowRunning, 0, string(errcode.Internal), taskRowFailed},
		{"the unit outran its deadline", true, taskRowRunning, tskTimeout + time.Second,
			string(errcode.DeadlineExceeded), taskRowFailed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newTaskFixture(t)
			v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
			name := TaskUnitName(v.ID, core.TaskOpApply, 1)
			f.taskPoll(func() {
				f.units.set(name, true)
				f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
			})
			f.taskPoll(func() {
				if c.end == taskRowFailed {
					f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowFailed, "", string(errcode.Unavailable),
						"the package mirror is unreachable")
				}
				if c.past > 0 {
					f.recoverExec("UPDATE core_task_runs SET started_at = ? WHERE change_id = ?",
						formatTime(f.clock.Now().Add(-c.past)), v.ID)
				}
				f.units.set(name, false)
			})
			done := f.taskUntil(v.ID, core.StateFailed)
			if done.Owner != "" || done.Error == nil || done.Error.Code != c.code {
				t.Errorf("failed change = %+v (error %+v), want unowned with code %s", done, done.Error, c.code)
			}
			if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != c.rowAfter {
				t.Errorf("row = %s, want %s", got, c.rowAfter)
			}
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID); n != 0 {
				t.Errorf("locks = %d, want 0", n)
			}
		})
	}
}

func TestTaskApplyWhoseVerifyFails(t *testing.T) {
	f := newTaskFixture(t)
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	f.taskPoll(func() {
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
		f.units.set(name, false)
	})
	done := f.taskUntil(v.ID, core.StateFailed)
	if done.Owner != "" || done.Error == nil || done.Error.Code != string(errcode.Internal) {
		t.Errorf("failed change = %+v (error %+v), want unowned with Verify's internal error", done, done.Error)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID); n != 0 {
		t.Errorf("locks = %d, want 0", n)
	}
}

func TestTaskVerifyCutOffByCloseLeavesTheChangeOwned(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	entered := make(chan struct{})
	f.tsk.verify = func(ctx context.Context) error {
		close(entered)
		<-ctx.Done()
		return ctx.Err()
	}
	v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
	name := TaskUnitName(v.ID, core.TaskOpApply, 1)
	f.taskPoll(func() {
		f.units.set(name, true)
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
	})
	f.taskPoll(func() {
		f.tsk.set("a", "1")
		f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
		f.units.set(name, false)
	})
	<-entered
	f.eng.Close()
	if c, err := f.eng.Change(ctx, v.ID); err != nil || c.State != core.StateApplying || c.Owner != core.OwnerEngine ||
		f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
		t.Fatalf("after Close during Verify: %+v, %v; want applying, owned by engine, locked", c, err)
	}
	f.tsk.mu.Lock()
	f.tsk.verify = nil
	f.tsk.mu.Unlock()
	f.restartTask()
	if err := f.eng.Recover(ctx); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	done := f.taskUntil(v.ID, core.StateCompleted)
	if done.Owner != "" || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 0 {
		t.Errorf("completed change = %+v, want unowned and unlocked", done)
	}
}

func TestTaskStartFence(t *testing.T) {
	t.Run("a unit that never starts is fenced", func(t *testing.T) {
		f := newTaskFixture(t)
		f.units.startErr = errors.New("the bus connection dropped")
		v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		for range 4 {
			f.taskPoll(nil)
		}
		f.taskArmed()
		if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowStarting {
			t.Fatalf("row after 8 s = %s, want starting: two observations 10 s apart fence it", got)
		}
		done := f.taskUntil(v.ID, core.StateFailed)
		if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowCancelled || done.Error == nil ||
			done.Error.Code != string(errcode.Unavailable) || done.Owner != "" {
			t.Errorf("row %s, change %+v (error %+v); want cancelled, failed with unavailable, unowned", got, done, done.Error)
		}
	})
	t.Run("the process claims the row between the observations", func(t *testing.T) {
		f := newTaskFixture(t)
		f.units.startErr = errors.New("the bus connection dropped")
		v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		name := TaskUnitName(v.ID, core.TaskOpApply, 1)
		flipped := make(chan error, 1)
		f.units.mu.Lock()
		f.units.onState = func(n int) {
			// The sixth poll is the one 10 s after the first: the follower
			// has read the row starting and is about to fence it.
			if n == 6 {
				_, err := f.db.Writer().Exec(`UPDATE core_task_runs SET state = ?, pid = 4242
					WHERE change_id = ? AND op = ? AND attempt = 1`, taskRowRunning, v.ID, core.TaskOpApply)
				flipped <- err
			}
		}
		f.units.mu.Unlock()
		for range 5 {
			f.taskPoll(nil)
		}
		if err := <-flipped; err != nil {
			t.Fatal(err)
		}
		f.taskPoll(func() { f.units.set(name, true) })
		if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowRunning {
			t.Fatalf("row = %s, want running: the fence matched no row", got)
		}
		f.taskPoll(func() {
			f.tsk.set("a", "1")
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
			f.units.set(name, false)
		})
		f.taskUntil(v.ID, core.StateCompleted)
	})
	t.Run("a queued job holds the fence", func(t *testing.T) {
		f := newTaskFixture(t)
		f.units.onStart = func(name string, _ core.TransientService) {
			f.units.setState(name, core.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead", PendingJob: "start"})
		}
		v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		name := TaskUnitName(v.ID, core.TaskOpApply, 1)
		for range 7 {
			f.taskPoll(nil)
		}
		f.taskArmed()
		if got := f.taskRowState(v.ID, core.TaskOpApply, 1); got != taskRowStarting {
			t.Fatalf("row after 14 s with a queued job = %s, want starting", got)
		}
		f.taskPoll(func() {
			f.units.set(name, true)
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
		})
		f.taskPoll(func() {
			f.tsk.set("a", "1")
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
			f.units.set(name, false)
		})
		f.taskUntil(v.ID, core.StateCompleted)
	})
	t.Run("a start call that errors and a unit that appears", func(t *testing.T) {
		f := newTaskFixture(t)
		f.units.startErr = errors.New("the bus connection dropped")
		v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		name := TaskUnitName(v.ID, core.TaskOpApply, 1)
		f.taskPoll(nil)
		f.taskPoll(func() {
			f.units.set(name, true)
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
		})
		f.taskPoll(nil)
		f.taskPoll(func() {
			f.tsk.set("a", "1")
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowSucceeded, `{"value":"1","token":"tok-a"}`, "", "")
			f.units.set(name, false)
		})
		f.taskUntil(v.ID, core.StateCompleted)
		if got := f.units.startedUnits(); !reflect.DeepEqual(got, []string{name}) {
			t.Errorf("started units = %v, want only %s", got, name)
		}
	})
}

// taskLog is a log the run goroutine writes while the test reads it.
type taskLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *taskLog) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(p)
}

func (l *taskLog) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.String()
}

// taskLogged replaces the fixture's engine with one that has the fake units
// and logs to the returned log.
func (f *taskFixture) taskLogged() *taskLog {
	f.t.Helper()
	f.eng.Close()
	log := &taskLog{}
	cfg := f.config()
	cfg.Units, cfg.TaskBinary = f.units, tskBinary
	cfg.Base.Log = slog.New(slog.NewTextHandler(log, nil))
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
	return log
}

func TestTaskApplyRunErrorIsLogged(t *testing.T) {
	t.Run("a failed run is logged and its change stays applying", func(t *testing.T) {
		f := newTaskFixture(t)
		log := f.taskLogged()
		v := f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		name := TaskUnitName(v.ID, core.TaskOpApply, 1)
		f.taskPoll(func() {
			f.units.set(name, true)
			f.taskRowTo(v.ID, core.TaskOpApply, 1, taskRowRunning, "", "", "")
		})
		f.taskPoll(func() {
			ct, err := coretest.Sealer{}.Seal("wrong-ad", []byte(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			f.recoverExec(`UPDATE core_task_runs SET state = ?, output_ct = ?, finished_at = ?
				WHERE change_id = ? AND op = ? AND attempt = 1`,
				taskRowSucceeded, ct, formatTime(f.clock.Now()), v.ID, core.TaskOpApply)
			f.units.set(name, false)
		})
		deadline := time.Now().Add(10 * time.Second)
		for {
			text := log.String()
			if strings.Contains(text, "lifecycle: apply run") && strings.Contains(text, v.ID) {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("no apply run error was logged for %s:\n%s", v.ID, text)
			}
			time.Sleep(time.Millisecond)
		}
		c, err := f.eng.Change(context.Background(), v.ID)
		if err != nil || c.State != core.StateApplying || c.Owner != core.OwnerEngine ||
			f.count("SELECT count(*) FROM core_locks WHERE owner = ?", v.ID) != 1 {
			t.Errorf("after the failed run: %+v, %v; want applying, owned by engine, locked", c, err)
		}
	})
	t.Run("a close is not logged", func(t *testing.T) {
		f := newTaskFixture(t)
		log := f.taskLogged()
		f.taskApplyStart(refTskInstall, `{"name":"a","value":"1"}`)
		f.taskArmed()
		f.eng.Close()
		if text := log.String(); strings.Contains(text, "lifecycle: apply run") {
			t.Errorf("Close was logged as a failed apply run:\n%s", text)
		}
	})
}

func TestTaskApplyNeedsUnits(t *testing.T) {
	f := newTaskFixture(t)
	ctx := context.Background()
	f.eng.Close()
	eng, err := New(f.config())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(eng.Close)
	pl, err := eng.Plan(ctx, alice, refTskInstall, json.RawMessage(`{"name":"a","value":"1"}`), core.PlanOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.Internal)
	if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
		t.Errorf("changes = %d, want none", n)
	}
}

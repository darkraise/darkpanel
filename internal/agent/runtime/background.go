package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// The backoff of a background task whose Run or Watch failed (spec 1.4): it
// doubles from TaskBackoffMin up to TaskBackoffMax, and a successful pass
// resets it.
const (
	TaskBackoffMin = time.Second
	TaskBackoffMax = 5 * time.Minute
)

// background holds the modules whose background tasks run.
type background struct {
	// mu is held while a module's tasks start or stop, so a change of the
	// enabled flag, a preflight and Close never interleave, and each returns
	// only once the tasks it stopped have returned.
	mu      sync.Mutex
	running map[string]*moduleTasks
	closed  bool
}

// moduleTasks is one module's running tasks.
type moduleTasks struct {
	cancel context.CancelFunc
	done   sync.WaitGroup // every task's pass loop and watch
}

// taskRun is one running task.
type taskRun struct {
	module string
	def    core.BackgroundTask
	// wake holds at most one pending wake: wakes that arrive during a pass
	// coalesce into one further pass.
	wake chan struct{}
	// watchFailures counts Watch's consecutive errors for its backoff; a
	// successful pass resets it.
	watchFailures atomic.Int64
}

func (t *taskRun) wakeUp() {
	select {
	case t.wake <- struct{}{}:
	default:
	}
}

// taskBackoff is the wait after the failures-th consecutive failure.
func taskBackoff(failures int64) time.Duration {
	d := TaskBackoffMin
	for i := int64(1); i < failures && d < TaskBackoffMax; i++ {
		d *= 2
	}
	return min(d, TaskBackoffMax)
}

// startBackground starts the background tasks of every module whose enabled
// flag is set and whose preflight passed. Open calls it after startup
// recovery.
func (r *Runtime) startBackground(ctx context.Context) {
	r.bg = &background{running: map[string]*moduleTasks{}}
	for _, m := range r.modules() {
		if err := r.syncTasks(ctx, m.ID); err != nil {
			r.cfg.Log.Error("runtime: start background tasks", "module", m.ID, "error", err)
		}
	}
}

// stopBackground cancels every running task and returns once each Run and
// Watch returned; no task starts afterwards.
func (r *Runtime) stopBackground() {
	b := r.bg
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for _, m := range b.running {
		m.cancel()
	}
	for _, m := range b.running {
		m.done.Wait()
	}
	clear(b.running)
}

// syncTasks starts moduleID's tasks when its enabled flag is set and its last
// preflight passed, and otherwise stops them, returning once their Run and
// Watch returned. It reads the flag and the status anew, so the last of
// several concurrent calls decides.
func (r *Runtime) syncTasks(ctx context.Context, moduleID string) error {
	tasks := r.cfg.Registry.BackgroundTasks(moduleID)
	if len(tasks) == 0 {
		return nil
	}
	b := r.bg
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return nil
	}
	run := r.taskModuleAvailable(moduleID)
	if run {
		enabled, err := r.moduleEnabled(ctx, moduleID)
		if err != nil {
			return err
		}
		run = enabled
	}
	cur := b.running[moduleID]
	switch {
	case run && cur == nil:
		b.running[moduleID] = r.startTasks(moduleID, tasks)
	case !run && cur != nil:
		delete(b.running, moduleID)
		cur.cancel()
		cur.done.Wait()
	}
	return nil
}

func (r *Runtime) taskModuleAvailable(moduleID string) bool {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.status[moduleID].Available
}

// startTasks runs one goroutine per task, plus one per Watch, on a context
// only stopping the module cancels.
func (r *Runtime) startTasks(moduleID string, tasks []core.BackgroundTask) *moduleTasks {
	ctx, cancel := context.WithCancel(context.Background())
	m := &moduleTasks{cancel: cancel}
	for _, def := range tasks {
		t := &taskRun{module: moduleID, def: def, wake: make(chan struct{}, 1)}
		m.done.Go(func() { r.runTask(ctx, t) })
		if def.Watch != nil {
			m.done.Go(func() { r.watchTask(ctx, t) })
		}
	}
	return m
}

// runTask runs t's passes until ctx ends: one at once, then Every after the
// previous one ended and after each wake. A failed pass is logged and the
// next one waits with backoff, during which wakes wait too.
func (r *Runtime) runTask(ctx context.Context, t *taskRun) {
	tc := r.taskContext(t)
	var failures int64
	for {
		// A wake that came before this pass is answered by it.
		select {
		case <-t.wake:
		default:
		}
		skipped, err := r.taskPass(ctx, t, tc)
		if ctx.Err() != nil {
			return
		}
		wait := t.def.Every
		switch {
		case err != nil:
			failures++
			wait = taskBackoff(failures)
			r.cfg.Log.Warn("runtime: background task pass failed", "task", t.def.ID, "error", err, "retry", wait)
		case skipped && failures > 0:
			wait = taskBackoff(failures)
		case !skipped:
			failures = 0
			t.watchFailures.Store(0)
		}
		wake := t.wake
		if failures > 0 {
			wake = nil
		}
		if !r.taskWait(ctx, wait, wake) {
			return
		}
	}
}

// taskPass runs one pass of t, unless a change or an update operation holds
// one of its lock keys: the pass is then skipped without error.
func (r *Runtime) taskPass(ctx context.Context, t *taskRun, tc core.TaskContext) (skipped bool, err error) {
	held, err := r.taskLocksHeld(ctx, t.def.LockKeys)
	if err != nil || held {
		return held, err
	}
	return false, lifecycle.Protect(r.cfg.Log, t.def.ID, "Run", func() error { return t.def.Run(ctx, tc) })
}

// taskLocksHeld reports whether core_locks holds one of keys, or the key of
// an update operation that holds every lock.
func (r *Runtime) taskLocksHeld(ctx context.Context, keys []string) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	for _, key := range append([]string{lifecycle.ReserveAll}, keys...) {
		var owner string
		err := r.cfg.DB.Read().QueryRowContext(ctx, "SELECT owner FROM core_locks WHERE key = ?", key).Scan(&owner)
		switch {
		case errors.Is(err, sql.ErrNoRows):
		case err != nil:
			return false, fmt.Errorf("runtime: read lock %s: %w", key, err)
		default:
			return true, nil
		}
	}
	return false, nil
}

// taskWait waits d (forever when d is 0) or for a wake on wake (never when
// wake is nil); it reports false when ctx ended first.
func (r *Runtime) taskWait(ctx context.Context, d time.Duration, wake <-chan struct{}) bool {
	var fired chan struct{}
	if d > 0 {
		fired = make(chan struct{})
		timer := r.cfg.Clock.AfterFunc(d, func() { close(fired) })
		defer timer.Stop()
	}
	select {
	case <-ctx.Done():
		return false
	case <-fired:
		return true
	case <-wake:
		return true
	}
}

// watchTask runs t's Watch until ctx ends, restarting it with backoff after
// each error. The watch missed events while it was down, so each restart
// wakes the task for a full pass.
func (r *Runtime) watchTask(ctx context.Context, t *taskRun) {
	env := r.taskEnv(t)
	for {
		err := lifecycle.Protect(r.cfg.Log, t.def.ID, "Watch", func() error { return t.def.Watch(ctx, env, t.wakeUp) })
		if ctx.Err() != nil {
			return
		}
		wait := taskBackoff(t.watchFailures.Add(1))
		r.cfg.Log.Warn("runtime: background task watch failed", "task", t.def.ID, "error", err, "retry", wait)
		if !r.taskWait(ctx, wait, nil) {
			return
		}
		t.wakeUp()
	}
}

// taskEnv is the Env of t's Run and Watch: its module's, as the system
// principal named after the task (spec 1.4), with no call context.
func (r *Runtime) taskEnv(t *taskRun) core.Env {
	env := r.moduleEnv(t.module)
	env.Principal = core.Principal{Actor: "system:" + t.def.ID, Scope: core.ScopeAdmin}
	return env
}

func (r *Runtime) taskContext(t *taskRun) core.TaskContext {
	env := r.taskEnv(t)
	return core.TaskContext{Env: env, Apply: func(ctx context.Context, action string, input any) (core.ChangeView, error) {
		return r.taskApply(ctx, t, env.Principal, action, input)
	}}
}

// taskApply applies the highest agent-side version of action, which must be
// an action of t's own module, through the runtime's engine as a system
// change.
func (r *Runtime) taskApply(ctx context.Context, t *taskRun, p core.Principal, action string, input any) (core.ChangeView, error) {
	if module, _, _ := strings.Cut(action, "."); module != t.module {
		return core.ChangeView{}, fmt.Errorf("runtime: background task %s may apply only actions of module %s, not %s",
			t.def.ID, t.module, action)
	}
	ref := core.ActionRef{Name: action}
	for _, def := range r.cfg.Registry.Actions() {
		if def.Spec.Name == action && def.Spec.Side == core.SideAgent {
			ref.Version = max(ref.Version, def.Spec.Version)
		}
	}
	if ref.Version == 0 {
		return core.ChangeView{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", action))
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return core.ChangeView{}, fmt.Errorf("runtime: background task %s: encode the input of %s: %w", t.def.ID, action, err)
	}
	return r.engine.ApplySystem(ctx, p, ref, raw)
}

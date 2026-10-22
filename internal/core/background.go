package core

import (
	"context"
	"time"
)

// BackgroundTask is a module's agent-side loop. The runtime runs it only
// while the module's agent-side enabled flag is set and its preflight passes.
type BackgroundTask struct {
	ID       string        // "firewall.reconcile"; audit actor system:<ID>
	Every    time.Duration // 0: run only on wakes
	LockKeys []string      // the lock keys of every action the task may apply
	// Run is one pass: it returns when the pass is done. It mutates only
	// through tc.Apply.
	Run func(ctx context.Context, tc TaskContext) error
	// Watch, when set, is a long-lived subscription (Docker events) that
	// calls wake for each relevant event; it returns only on error or ctx done.
	Watch func(ctx context.Context, env Env, wake func()) error
}

// TaskContext is what one pass of a background task may use.
type TaskContext struct {
	Env Env // Principal is system:<ID>, ScopeAdmin; Call is empty
	// Apply plans and applies one action of the task's own module through
	// the runtime's lifecycle engine (never a second engine), confirms a
	// revertible change as soon as its verify passes, and marks the change
	// system. A plan with a failed guard returns plan.guard_failed.
	Apply func(ctx context.Context, action string, input any) (ChangeView, error)
}

// Background is implemented by a module with background tasks, found with a
// type assertion on the Module.
type Background interface{ BackgroundTasks() []BackgroundTask }

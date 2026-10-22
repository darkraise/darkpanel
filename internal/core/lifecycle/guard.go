package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"

	"github.com/darkraise/darkpanel/internal/core"
)

// panicError is a recovered handler panic. It carries no errcode, so the API
// answers internal and a change fails with code internal.
type panicError struct{ name, call string }

func (e *panicError) Error() string { return fmt.Sprintf("%s of %s panicked", e.call, e.name) }

// Protect runs f, the call named call of the handler name (an action
// reference, a resource or a module id), and returns its error. A panic in f
// is recovered and returned as a plain error "<call> of <name> panicked", and
// logged once at error level as "lifecycle: handler panic" with the handler,
// the call and the stack. The panic value is logged only when it is a runtime
// error; any other value is logged by its type alone, because a handler may
// have put its input in it.
func Protect(log *slog.Logger, name, call string, f func() error) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		value := fmt.Sprintf("%T", p)
		if re, ok := p.(runtime.Error); ok {
			value = re.Error()
		}
		log.Error("lifecycle: handler panic", "handler", name, "call", call, "panic", value, "stack", string(debug.Stack()))
		err = &panicError{name: name, call: call}
	}()
	return f()
}

// protect is Protect for a call of the action ref on the engine's log.
func (e *Engine) protect(ref core.ActionRef, call string, f func() error) error {
	return Protect(e.cfg.Base.Log, ref.String(), call, f)
}

// verifyRecovery runs Verify for startup recovery and reports what it found:
// "verify passed", "verify failed: <message>" or, when Verify has not returned
// within RecoveryVerifyBound, "verify did not finish". Recovery already
// decided the change's outcome and Verify only reads, so a Verify that does
// not finish is left running on its own goroutine with its context cancelled.
func (e *Engine) verifyRecovery(ctx context.Context, def core.ActionDef, env core.Env, in, out json.RawMessage) string {
	ctx, cancel := context.WithCancel(ctx)
	bound := make(chan struct{})
	timer := e.cfg.Base.Clock.AfterFunc(RecoveryVerifyBound, func() {
		cancel()
		close(bound)
	})
	done := make(chan error, 1)
	go func() {
		done <- e.protect(def.Ref(), "Verify", func() error { return def.Handler.Verify(ctx, env, in, out) })
	}()
	select {
	case err := <-done:
		timer.Stop()
		cancel()
		if err != nil {
			return "verify failed: " + e.changeError(def.Ref(), err).Message
		}
		return "verify passed"
	case <-bound:
		return "verify did not finish"
	}
}

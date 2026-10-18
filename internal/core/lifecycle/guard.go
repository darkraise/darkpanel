package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// VerifyTimeout bounds a handler's Verify, in apply and in Recover. It stays
// below the API servers' 60 s WriteTimeout, so an apply whose Verify hangs
// can still write its response.
const VerifyTimeout = 30 * time.Second

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

// verify runs the handler's Verify with a context that ends after the
// engine's verify timeout, and returns a plain error at that deadline even
// when the handler ignores its context. Such a handler keeps running in the
// background; Verify only reads the host, so the change's outcome is recorded
// without waiting for it.
func (e *Engine) verify(ctx context.Context, def core.ActionDef, env core.Env, in, out json.RawMessage) error {
	ctx, cancel := context.WithTimeout(ctx, e.verifyTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- e.protect(def.Ref(), "Verify", func() error { return def.Handler.Verify(ctx, env, in, out) })
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
		}
		return fmt.Errorf("lifecycle: Verify of %s did not finish within %s: %w", def.Ref(), e.verifyTimeout, ctx.Err())
	}
}

package firewall

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
)

// lockRead takes the host-state lock for reading and returns its release;
// without a lock it takes none.
func (f fw) lockRead() (unlock func()) {
	if f.mu == nil {
		return func() {}
	}
	f.mu.RLock()
	return f.mu.RUnlock
}

// lockWrite takes the host-state lock for writing and returns its release;
// without a lock it takes none.
func (f fw) lockWrite() (unlock func()) {
	if f.mu == nil {
		return func() {}
	}
	f.mu.Lock()
	return f.mu.Unlock
}

// readLocked is read under f's host-state lock for reading.
func readLocked[Out any](f fw, read func(context.Context, core.Env) (Out, error)) func(context.Context, core.Env) (Out, error) {
	return func(ctx context.Context, env core.Env) (Out, error) {
		defer f.lockRead()()
		return read(ctx, env)
	}
}

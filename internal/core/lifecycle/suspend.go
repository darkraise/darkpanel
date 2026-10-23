package lifecycle

import (
	"context"
	"errors"
)

// errRecovererSuspended answers a recovery handler run while the engine's
// automatic work is suspended. The change stays recovering and owned by
// module, so the next start's recovery runs the handler.
var errRecovererSuspended = errors.New("lifecycle: module recovery handlers are suspended")

// suspended reports whether Config.Suspended holds the engine's automatic
// work back: composite recovery, the composite runners that follow
// children, Wake and module recovery handlers. A restored controller
// database is quarantined until its operator releases it, and none of that
// work may act on its records before then. An error reading the flag counts
// as suspended, because acting on a quarantined record cannot be undone.
func (e *Engine) suspended(ctx context.Context, what string) bool {
	if e.cfg.Suspended == nil {
		return false
	}
	held, err := e.cfg.Suspended(ctx)
	if err != nil {
		e.cfg.Base.Log.Error("lifecycle: the suspension check failed; "+what+" is held back", "error", err)
		return true
	}
	return held
}

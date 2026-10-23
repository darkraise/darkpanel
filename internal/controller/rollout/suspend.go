package rollout

import (
	"context"
	"log/slog"
)

// suspended asks f whether the controller's automatic mutation sources are
// suspended (restore quarantine, spec 10). A failed read counts as
// suspended: no pass may run on a controller that may be quarantined.
func suspended(ctx context.Context, f func(ctx context.Context) (bool, error), log *slog.Logger, loop string) bool {
	if f == nil {
		return false
	}
	s, err := f(ctx)
	if err != nil {
		log.Warn("rollout: cannot tell whether automatic changes are suspended; the pass is skipped", "loop", loop, "error", err)
		return true
	}
	return s
}

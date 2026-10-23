package runtime

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.AgentReveal = (*Runtime)(nil)

// Reveal runs a reveal action, or with internal an Internal read, once the
// action's module is usable as Read requires (spec 1.7 and 1.14). The engine
// journals it before the value is returned; the controller ingests that
// entry into its audit log.
func (r *Runtime) Reveal(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
	if err := r.usable(a.Name); err != nil {
		return nil, err
	}
	if internal {
		return r.engine.ReadInternal(ctx, p, a, input)
	}
	return r.engine.Reveal(ctx, p, a, input)
}

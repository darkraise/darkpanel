package lifecycle

import (
	"context"

	"github.com/darkraise/darkpanel/internal/core"
)

// Available runs ref's Spec.Available under Protect with the env of a call
// without a principal, and returns its error, whose text is the reason the
// action is unavailable (spec 1.7). An action that declares no Available is
// available. An unknown action, or one of the other side, is action.unknown.
func (e *Engine) Available(ctx context.Context, ref core.ActionRef) error {
	def, err := e.action(ref)
	if err != nil {
		return err
	}
	if def.Spec.Available == nil {
		return nil
	}
	return e.protect(ref, "Available", func() error { return def.Spec.Available(ctx, e.env(core.Principal{}, def)) })
}

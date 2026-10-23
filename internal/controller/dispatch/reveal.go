package dispatch

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var _ core.DispatcherReveal = (*Dispatcher)(nil)

// Reveal runs a reveal action (spec 1.14): a controller-side one on the
// controller engine, an agent one on hostID's agent once its module is
// usable and the action available there. The executing side audits the
// reveal before it answers; the value passes through unchanged and is never
// indexed, ingested or kept. An Internal action is action.unknown here, as
// is any action that is not a reveal.
func (d *Dispatcher) Reveal(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	return d.reveal(ctx, p, hostID, a, input, false)
}

// ReadInternal runs an Internal read (spec 1.7) the way Reveal runs a reveal
// action; only the controller itself calls it, and every other action is
// action.unknown.
func (d *Dispatcher) ReadInternal(ctx context.Context, p core.Principal, hostID string, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	return d.reveal(ctx, p, hostID, ref, input, true)
}

func (d *Dispatcher) reveal(ctx context.Context, p core.Principal, hostID string, ref core.ActionRef, input json.RawMessage, internal bool) (json.RawMessage, error) {
	def, err := d.action(ref, hostID)
	if err != nil {
		return nil, err
	}
	if internal && !def.Spec.Internal {
		return nil, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is not an internal read", ref))
	}
	if !internal && !def.Spec.Reveal {
		return nil, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is not a reveal action", ref))
	}
	if def.Spec.Side == core.SideController {
		if err := d.available(ctx, "", def); err != nil {
			return nil, err
		}
		if internal {
			return d.cfg.Engine.ReadInternal(ctx, p, ref, input)
		}
		return d.cfg.Engine.Reveal(ctx, p, ref, input)
	}
	c, err := d.agent(ctx, hostID, ref.Name)
	if err != nil {
		return nil, err
	}
	if err := d.negotiate(hostID, def, input); err != nil {
		return nil, err
	}
	if err := d.available(ctx, hostID, def); err != nil {
		return nil, err
	}
	return c.Reveal(ctx, p, ref, input, internal)
}

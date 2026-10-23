package dispatch

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// Children is how the controller engine's composite runner reaches its
// steps (1.3). Plan, apply, read, confirm and revert go through the
// dispatcher's own methods, so every child is indexed and its journal
// ingested like an operator's change. Undo, release, lookup, retire and the
// private-output acknowledgement go to the controller engine for a
// controller-side child (hostID "") and to the host's agent otherwise,
// passing the composite's call.
func (d *Dispatcher) Children() lifecycle.Children { return dispatchChildren{d: d} }

type dispatchChildren struct{ d *Dispatcher }

var _ lifecycle.Children = dispatchChildren{}

func (c dispatchChildren) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	return c.d.Plan(ctx, p, hostID, a, input, opts)
}

func (c dispatchChildren) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return c.d.Apply(ctx, p, hostID, a, planID, opts)
}

// Read reads a read step; an Internal action goes the controller's own
// path, ReadInternal, never the public read (spec 1.7).
func (c dispatchChildren) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if def, ok := c.d.cfg.Registry.Action(a); ok && def.Spec.Internal {
		return c.d.ReadInternal(ctx, p, hostID, a, input)
	}
	return c.d.Read(ctx, p, hostID, a, input)
}

func (c dispatchChildren) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.d.Confirm(ctx, p, changeID)
}

func (c dispatchChildren) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.d.Revert(ctx, p, changeID)
}

// Undo undoes a child with the composite's call, then ingests an agent
// child's journal.
func (c dispatchChildren) Undo(ctx context.Context, p core.Principal, hostID, changeID string, call *core.CallContext) (core.ChangeView, error) {
	if hostID == "" {
		return c.d.cfg.Engine.Undo(ctx, p, changeID, call)
	}
	agent, err := c.d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := agent.Undo(ctx, p, changeID, call)
	c.d.ingest(ctx, hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	return checkChange(v, hostID, changeID)
}

// Release releases a child with the composite's call, then ingests an agent
// child's journal.
func (c dispatchChildren) Release(ctx context.Context, p core.Principal, hostID, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	if hostID == "" {
		return c.d.cfg.Engine.Release(ctx, p, changeID, reason, call)
	}
	agent, err := c.d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	r, err := agent.Release(ctx, p, changeID, reason, call)
	c.d.ingest(ctx, hostID)
	if err != nil {
		return core.ReleaseResult{}, err
	}
	if _, err := checkChange(r.Change, hostID, changeID); err != nil {
		return core.ReleaseResult{}, err
	}
	return r, nil
}

func (c dispatchChildren) Lookup(ctx context.Context, hostID, planID string, private bool) (core.PlanLookup, error) {
	if hostID == "" {
		return c.d.cfg.Engine.Lookup(ctx, planID, private)
	}
	agent, err := c.d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.PlanLookup{}, err
	}
	return agent.Lookup(ctx, planID, private)
}

func (c dispatchChildren) Retire(ctx context.Context, hostID, planID string) (core.PlanLookup, error) {
	if hostID == "" {
		return c.d.cfg.Engine.Retire(ctx, planID)
	}
	agent, err := c.d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.PlanLookup{}, err
	}
	return agent.Retire(ctx, planID)
}

func (c dispatchChildren) AckPrivate(ctx context.Context, hostID, changeID string, final bool) error {
	if hostID == "" {
		return c.d.cfg.Engine.AckPrivate(ctx, changeID, final)
	}
	agent, err := c.d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return err
	}
	return agent.AckPrivate(ctx, changeID, final)
}

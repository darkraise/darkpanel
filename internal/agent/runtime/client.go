package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	goruntime "runtime"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var (
	_ core.AgentClient    = (*Runtime)(nil)
	_ core.AgentChangeOps = (*Runtime)(nil)
)

// Hello reports the agent, every module's availability as of its last check
// (migrations and preflight at Open, or a later Preflight), the schema
// hashes of every agent-side action version, the journal epoch and whether
// a controller session is live.
func (r *Runtime) Hello(context.Context) (core.AgentHello, error) {
	h := core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: r.cfg.BinaryVersion, OS: goruntime.GOOS, Arch: goruntime.GOARCH,
		HostID: r.cfg.HostID, Modules: r.statuses(), SchemaHashes: maps.Clone(r.hashes), JournalEpoch: r.epoch,
	}
	if r.cfg.ControllerConnected != nil {
		h.ControllerConnected = r.cfg.ControllerConnected()
	}
	return h, nil
}

// Preflight runs moduleID's preflight checks again and keeps the result for
// Hello. A module whose migrations failed is not checked and stays
// unavailable. A module the registry lacks is not_found.
func (r *Runtime) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	m, ok := r.cfg.Registry.Module(moduleID)
	if !ok {
		return core.ModuleStatus{}, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
	st := r.check(ctx, m)
	r.setStatus(st)
	return st, nil
}

// usable refuses, with module.unavailable, an action or resource name of a
// module whose migrations failed. Enablement and preflight results are the
// dispatcher's to enforce.
func (r *Runtime) usable(name string) error {
	moduleID, _, _ := strings.Cut(name, ".")
	if r.isBroken(moduleID) {
		return errcode.New(errcode.ModuleUnavailable,
			fmt.Sprintf("module %s is unavailable: %s", moduleID, ReasonMigrationFailed))
	}
	return nil
}

// Read runs an agent read action.
func (r *Runtime) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if err := r.usable(a.Name); err != nil {
		return nil, err
	}
	return r.engine.Read(ctx, p, a, input)
}

// Resource reads a resource with the agent's Env and p, redacting secret
// fields. A panic in the resource is a plain error.
func (r *Runtime) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	def, ok := r.cfg.Registry.Resource(name)
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no resource %s", name))
	}
	if err := r.usable(name); err != nil {
		return nil, err
	}
	env := r.base
	env.Principal = p
	var out json.RawMessage
	err := lifecycle.Protect(r.cfg.Log, name, "Resource", func() error {
		var err error
		out, err = def.Read(ctx, env)
		return err
	})
	if err != nil {
		return nil, err
	}
	return schema.Redact(def.OutputType, out), nil
}

// Plan plans an agent action.
func (r *Runtime) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage, opts core.PlanOptions) (core.PlanView, error) {
	if err := r.usable(a.Name); err != nil {
		return core.PlanView{}, err
	}
	return r.engine.Plan(ctx, p, a, input, opts)
}

// Apply applies a plan made by Plan.
func (r *Runtime) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return r.engine.Apply(ctx, p, planID, opts)
}

// Confirm confirms a pending change.
func (r *Runtime) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return r.engine.Confirm(ctx, p, changeID)
}

// Revert reverts a pending change.
func (r *Runtime) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return r.engine.Revert(ctx, p, changeID)
}

// Change returns one change of this host.
func (r *Runtime) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	return r.engine.Change(ctx, changeID)
}

// Changes lists this host's changes as lifecycle.Engine.Changes does.
func (r *Runtime) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	return r.engine.Changes(ctx, f)
}

// Journal returns up to limit journal entries after afterSeq.
func (r *Runtime) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	return r.journal.Entries(ctx, afterSeq, limit)
}

// Ack deletes the journal entries up to seq, which the controller stored.
func (r *Runtime) Ack(ctx context.Context, seq int64) error {
	return r.journal.Ack(ctx, seq)
}

// Undo undoes a change of this host (spec 1.2). call names the composite
// whose unwind sends it, and is nil otherwise.
func (r *Runtime) Undo(ctx context.Context, p core.Principal, changeID string, call *core.CallContext) (core.ChangeView, error) {
	return r.engine.Undo(ctx, p, changeID, call)
}

// Repair repairs a change of this host in needs_repair (spec 1.3). A repair
// may plan an action of the change's module, so, as for Plan, that module
// must be usable.
func (r *Runtime) Repair(ctx context.Context, p core.Principal, changeID string, req core.RepairRequest) (core.RepairResult, error) {
	v, err := r.engine.Change(ctx, changeID)
	if err != nil {
		return core.RepairResult{}, err
	}
	if err := r.usable(v.Action); err != nil {
		return core.RepairResult{}, err
	}
	return r.engine.Repair(ctx, p, changeID, req)
}

// Release gives up a change of this host in needs_repair. call names the
// composite releasing its step, and is nil otherwise.
func (r *Runtime) Release(ctx context.Context, p core.Principal, changeID, reason string, call *core.CallContext) (core.ReleaseResult, error) {
	return r.engine.Release(ctx, p, changeID, reason, call)
}

// Effects lists a change's effects and those of its repair changes.
func (r *Runtime) Effects(ctx context.Context, _ core.Principal, changeID string) ([]core.EffectView, error) {
	return r.engine.Effects(ctx, changeID)
}

// Observe reads the resource of one effect now and changes nothing.
func (r *Runtime) Observe(ctx context.Context, p core.Principal, changeID string, seq int) (core.ObserveResult, error) {
	return r.engine.Observe(ctx, p, changeID, seq)
}

// Resolve records the operator's resolution of an unresolved effect.
func (r *Runtime) Resolve(ctx context.Context, p core.Principal, changeID string, seq int, req core.ResolveRequest) (core.EffectView, error) {
	return r.engine.Resolve(ctx, p, changeID, seq, req)
}

// Lookup reports what became of a plan; private adds a child change's
// unredacted output while it is kept.
func (r *Runtime) Lookup(ctx context.Context, planID string, private bool) (core.PlanLookup, error) {
	return r.engine.Lookup(ctx, planID, private)
}

// Retire marks a plan that has no change so that it is never applied.
func (r *Runtime) Retire(ctx context.Context, planID string) (core.PlanLookup, error) {
	return r.engine.Retire(ctx, planID)
}

// AckPrivate clears a change's private output once the composite sealed it;
// final says the composite is terminal.
func (r *Runtime) AckPrivate(ctx context.Context, changeID string, final bool) error {
	return r.engine.AckPrivate(ctx, changeID, final)
}

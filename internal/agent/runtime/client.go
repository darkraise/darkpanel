package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var _ core.AgentClient = (*Runtime)(nil)

// Hello reports the agent and every module's availability as of its last
// check (migrations and preflight at Open, or a later Preflight).
func (r *Runtime) Hello(context.Context) (core.AgentHello, error) {
	return core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: r.cfg.BinaryVersion, OS: goruntime.GOOS,
		HostID: r.cfg.HostID, Modules: r.statuses(),
	}, nil
}

// Preflight runs moduleID's preflight checks again and keeps the result for
// Hello. A module whose migrations failed is not checked and stays
// unavailable. A module the registry lacks, and the core, are not_found.
func (r *Runtime) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	m, ok := r.cfg.Registry.Module(moduleID)
	if !ok || moduleID == core.ModuleIDCore {
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
func (r *Runtime) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	if err := r.usable(a.Name); err != nil {
		return core.PlanView{}, err
	}
	return r.engine.Plan(ctx, p, a, input)
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

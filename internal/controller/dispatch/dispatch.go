package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// PlanIndexRetention is how long core_plan_index keeps a plan that was never
// applied after it expired, so a late apply answers plan.expired; after that
// the plan is plan.not_found. An applied plan is kept, so a repeated apply
// returns its change, also after a restart.
const PlanIndexRetention = time.Hour

// Ingester copies a host's agent journal into the audit log;
// *audit.Ingester implements it.
type Ingester interface {
	IngestHost(ctx context.Context, hostID string) error
}

// Config wires a Dispatcher.
type Config struct {
	Registry *core.Registry    // the one registry of the process
	Engine   *lifecycle.Engine // the controller's engine
	Hosts    *Hosts            // the controller engine's Base.Agents too
	DB       *store.DB         // the controller database: core_hosts, core_plan_index, core_change_index
	Clock    core.Clock
	Log      *slog.Logger
	// Ingest, when set, runs after every agent apply, confirm and revert.
	Ingest Ingester
}

// Dispatcher is the core.Dispatcher of the controller. Its methods are safe
// for concurrent use.
type Dispatcher struct {
	cfg   Config
	plans planLocks
}

// New returns a dispatcher for cfg; every field but Ingest is required.
func New(cfg Config) (*Dispatcher, error) {
	required := []struct {
		name string
		set  bool
	}{
		{"Registry", cfg.Registry != nil}, {"Engine", cfg.Engine != nil}, {"Hosts", cfg.Hosts != nil},
		{"DB", cfg.DB != nil}, {"Clock", cfg.Clock != nil}, {"Log", cfg.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return nil, fmt.Errorf("dispatch: Config.%s is required", r.name)
		}
	}
	return &Dispatcher{cfg: cfg}, nil
}

// moduleOf is the module id of an action or resource name.
func moduleOf(name string) string {
	id, _, _ := strings.Cut(name, ".")
	return id
}

// action returns the registered action ref, which must be controller-side
// when hostID is empty and agent-side when it is not.
func (d *Dispatcher) action(ref core.ActionRef, hostID string) (core.ActionDef, error) {
	def, ok := d.cfg.Registry.Action(ref)
	switch {
	case !ok:
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", ref))
	case def.Spec.Side == core.SideController && hostID != "":
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is a controller-side action and takes no host", ref))
	case def.Spec.Side == core.SideAgent && hostID == "":
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is an agent action and needs a host", ref))
	}
	return def, nil
}

// agent returns hostID's client once the module of name is usable there.
// The core module is exempt: it is always loaded and has no enablement.
func (d *Dispatcher) agent(ctx context.Context, hostID, name string) (core.AgentClient, error) {
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if module := moduleOf(name); module != core.ModuleIDCore {
		if err := d.cfg.Hosts.usable(ctx, hostID, module); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Read runs a read action: a controller-side one on the controller engine
// (hostID empty), an agent one on hostID's agent once its module is usable.
func (d *Dispatcher) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return nil, err
	}
	if def.Spec.Side == core.SideController {
		return d.cfg.Engine.Read(ctx, p, a, input)
	}
	c, err := d.agent(ctx, hostID, a.Name)
	if err != nil {
		return nil, err
	}
	return c.Read(ctx, p, a, input)
}

// Resource reads a resource on hostID's agent once its module is usable.
func (d *Dispatcher) Resource(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
	if _, ok := d.cfg.Registry.Resource(name); !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no resource %s", name))
	}
	c, err := d.agent(ctx, hostID, name)
	if err != nil {
		return nil, err
	}
	return c.Resource(ctx, p, name)
}

// Plan plans a mutating action where Read would run it, and records the
// plan's host and action in core_plan_index for Apply.
func (d *Dispatcher) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return core.PlanView{}, err
	}
	var v core.PlanView
	if def.Spec.Side == core.SideController {
		v, err = d.cfg.Engine.Plan(ctx, p, a, input)
	} else {
		var c core.AgentClient
		if c, err = d.agent(ctx, hostID, a.Name); err == nil {
			v, err = c.Plan(ctx, p, a, input)
		}
	}
	if err != nil {
		return core.PlanView{}, err
	}
	if v.HostID != hostID || v.Action != a.Name || v.Version != a.Version {
		return core.PlanView{}, fmt.Errorf("dispatch: %s on %q answered with a plan for %s/v%d on %s",
			a, hostID, v.Action, v.Version, v.HostID)
	}
	if err := d.remember(ctx, hostID, a, v); err != nil {
		return core.PlanView{}, err
	}
	return v, nil
}

// remember indexes a plan (register row 69, obligation 1) and deletes the
// unapplied plans whose retention has passed.
func (d *Dispatcher) remember(ctx context.Context, hostID string, a core.ActionRef, v core.PlanView) error {
	cutoff := d.cfg.Clock.Now().Add(-PlanIndexRetention).UTC().Format(timeFormat)
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM core_plan_index WHERE applied = 0 AND expires_at <= ?", cutoff); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_plan_index (plan_id, host_id, action, version, expires_at) VALUES (?, ?, ?, ?, ?)`,
			v.PlanID, hostID, a.Name, a.Version, v.ExpiresAt.UTC().Format(timeFormat))
		return err
	})
	if err != nil {
		return fmt.Errorf("dispatch: index plan %s: %w", v.PlanID, err)
	}
	return nil
}

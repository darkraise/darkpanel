package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// AvailabilityMaxAge is how long a host's answer about a module's actions is
// used before it is asked again (spec 1.7).
const AvailabilityMaxAge = 60 * time.Second

// availabilityCache holds the last answer of each host and module. Its zero
// value is ready to use.
type availabilityCache struct {
	mu      sync.Mutex
	entries map[availabilityKey]availabilityEntry
}

type availabilityKey struct{ host, module string }

// availabilityEntry is one answer, when it was asked and the module row's
// checked_at it was asked under: a hello or a module enable rewrites
// checked_at, which retires the answer.
type availabilityEntry struct {
	answer    core.ModuleAvailability
	askedAt   time.Time
	checkedAt string
}

func (c *availabilityCache) get(k availabilityKey) (availabilityEntry, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[k]
	return e, ok
}

func (c *availabilityCache) put(k availabilityKey, e availabilityEntry) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[availabilityKey]availabilityEntry{}
	}
	c.entries[k] = e
}

// Availability is hostID's answer about the actions of moduleID, from the
// cache while it is younger than AvailabilityMaxAge and the module's
// core_module_state.checked_at is the one it was asked under, otherwise
// asked again. An agent that answers not_found, one without the route, has
// every action available. A module the registry lacks is not_found.
func (d *Dispatcher) Availability(ctx context.Context, hostID, moduleID string) (core.ModuleAvailability, error) {
	if _, ok := d.cfg.Registry.Module(moduleID); !ok {
		return core.ModuleAvailability{}, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return core.ModuleAvailability{}, err
	}
	var checked sql.NullString
	err = d.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT checked_at FROM core_module_state WHERE host_id = ? AND module_id = ?", hostID, moduleID).Scan(&checked)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return core.ModuleAvailability{}, fmt.Errorf("dispatch: read module state: %w", err)
	}
	key, now := availabilityKey{hostID, moduleID}, d.cfg.Clock.Now()
	if e, ok := d.avail.get(key); ok && e.checkedAt == checked.String && now.Sub(e.askedAt) <= AvailabilityMaxAge {
		return cloneAvailability(e.answer), nil
	}
	m, err := c.Availability(ctx, moduleID)
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded) && coded.Code == errcode.NotFound:
		m = core.ModuleAvailability{Module: moduleID, Available: true}
	case err != nil:
		return core.ModuleAvailability{}, fmt.Errorf("dispatch: availability of module %s on host %s: %w", moduleID, hostID, err)
	}
	d.avail.put(key, availabilityEntry{answer: cloneAvailability(m), askedAt: now, checkedAt: checked.String})
	return m, nil
}

func cloneAvailability(m core.ModuleAvailability) core.ModuleAvailability {
	m.Actions = slices.Clone(m.Actions)
	return m
}

// available refuses def with module.unavailable when it is unavailable on
// hostID (spec 1.7): a controller-side action runs its Available now, an
// agent-side one is looked up in the host's Availability once the host may
// run it at all. The core module is exempt, as it is from enablement, so an
// agent outside the support window stays updatable. An answer that cannot
// be had is logged and refuses nothing: availability spares the operator a
// doomed plan, and the agent's handler still decides.
func (d *Dispatcher) available(ctx context.Context, hostID string, def core.ActionDef) error {
	name := def.Spec.Name
	if def.Spec.Side == core.SideController {
		if err := d.cfg.Engine.Available(ctx, def.Ref()); err != nil {
			return errcode.New(errcode.ModuleUnavailable,
				fmt.Sprintf("action %s is unavailable on the controller: %s", name, err.Error()))
		}
		return nil
	}
	module := moduleOf(name)
	if module == core.ModuleIDCore {
		return nil
	}
	if _, err := d.agent(ctx, hostID, name); err != nil {
		return err
	}
	m, err := d.Availability(ctx, hostID, module)
	if err != nil {
		d.cfg.Log.Warn("dispatch: availability unknown; dispatching", "host", hostID, "action", name, "error", err)
		return nil
	}
	ok, reason := m.Available, m.Reason
	for _, a := range m.Actions {
		if a.Name == name {
			ok, reason = a.Available, a.Reason
			break
		}
	}
	if ok {
		return nil
	}
	if reason == "" {
		reason = "unavailable"
	}
	return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("action %s is unavailable on host %s: %s", name, hostID, reason))
}

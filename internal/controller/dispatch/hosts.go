// Package dispatch routes the calls of the HTTP API and the controller socket
// (spec section 3, Dispatch): controller-side actions to the controller's
// lifecycle engine, agent actions and resources to the host's AgentClient. It
// keeps core_module_state current from each agent's Hello, refuses the
// actions and resources of a module that is not usable on a host, and records
// which host owns every agent change.
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// ReasonDisabled is the reason of a module an operator disabled on a host.
const ReasonDisabled = "disabled"

// timeFormat is RFC 3339 in UTC with a fixed width, as every core table
// stores times.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// HostsConfig wires a Hosts.
type HostsConfig struct {
	DB       *store.DB      // the controller database: core_module_state
	Registry *core.Registry // the one registry of the process
	Clock    core.Clock
	Log      *slog.Logger
}

// Hosts is the controller's core.HostDirectory: the agent client of each
// host. The first time a host is reached in this process, Hosts calls its
// Hello and records every module's availability; a failed Hello is tried
// again on the next reach. Its methods are safe for concurrent use.
type Hosts struct {
	cfg HostsConfig

	mu      sync.Mutex // also serialises Hello calls
	clients map[string]core.AgentClient
	greeted map[string]bool
}

var _ core.HostDirectory = (*Hosts)(nil)

// NewHosts returns an empty directory; Add registers each host's client.
func NewHosts(cfg HostsConfig) *Hosts {
	return &Hosts{cfg: cfg, clients: map[string]core.AgentClient{}, greeted: map[string]bool{}}
}

// Add registers the client of hostID, a row of core_hosts. In M1 the server
// adds the local host with the in-process agent runtime.
func (h *Hosts) Add(hostID string, c core.AgentClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[hostID] = c
	delete(h.greeted, hostID)
}

// Agent returns hostID's client, after its first successful Hello in this
// process. A host without a client is not_found.
func (h *Hosts) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[hostID]
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	if !h.greeted[hostID] {
		if err := h.hello(ctx, hostID, c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Hello calls hostID's Hello now and records every module's availability.
// The server calls it for the local host at startup, which seeds the host's
// module rows.
func (h *Hosts) Hello(ctx context.Context, hostID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[hostID]
	if !ok {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	return h.hello(ctx, hostID, c)
}

// hello runs under mu. It writes available, reason and checked_at of every
// module the registry has; a missing row takes enabled from the module's
// DefaultEnabled, and an existing row keeps its enabled, which only
// core.module.enable and core.module.disable change.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient) error {
	hello, err := c.Hello(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: hello %s: %w", hostID, err)
	}
	if hello.HostID != hostID {
		return fmt.Errorf("dispatch: host %s answered Hello as %s", hostID, hello.HostID)
	}
	now := h.cfg.Clock.Now().UTC().Format(timeFormat)
	err = h.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, st := range hello.Modules {
			m, ok := h.cfg.Registry.Module(st.ID)
			if !ok || m.ID == core.ModuleIDCore {
				continue
			}
			var reason any
			if !st.Available {
				reason = st.Reason
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (host_id, module_id) DO UPDATE SET available = excluded.available,
				 reason = excluded.reason, checked_at = excluded.checked_at`,
				hostID, st.ID, m.DefaultEnabled, st.Available, reason, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("dispatch: record the modules of %s: %w", hostID, err)
	}
	h.greeted[hostID] = true
	h.cfg.Log.Info("dispatch: host said hello", "host", hostID, "protocol", hello.ProtocolVersion,
		"version", hello.BinaryVersion, "os", hello.OS)
	return nil
}

// usable answers module.unavailable unless moduleID is enabled and available
// on hostID; the detail carries the reason: "disabled", the row's reason, or
// coreactions.ReasonNotReported when the host has no row for it.
func (h *Hosts) usable(ctx context.Context, hostID, moduleID string) error {
	var enabled, available bool
	var reason sql.NullString
	err := h.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT enabled, available, reason FROM core_module_state WHERE host_id = ? AND module_id = ?",
		hostID, moduleID).Scan(&enabled, &available, &reason)
	why := ""
	switch {
	case errors.Is(err, sql.ErrNoRows):
		why = coreactions.ReasonNotReported
	case err != nil:
		return fmt.Errorf("dispatch: read module state: %w", err)
	case !enabled:
		why = ReasonDisabled
	case !available:
		why = reason.String
		if why == "" {
			why = "unavailable"
		}
	default:
		return nil
	}
	return errcode.New(errcode.ModuleUnavailable,
		fmt.Sprintf("module %s is unavailable on host %s: %s", moduleID, hostID, why))
}

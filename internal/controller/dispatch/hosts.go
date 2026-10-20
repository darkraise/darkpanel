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
	Clients  ClientSource   // the agent client of each host
	Clock    core.Clock
	Log      *slog.Logger
}

// Hosts is the controller's core.HostDirectory: the agent client of each
// host, from its ClientSource. Before handing out a client of a generation
// it has not greeted, Hosts calls its Hello and records the agent's version,
// platform and every module's availability; a failed Hello is tried again on
// the next reach. Its methods are safe for concurrent use.
type Hosts struct {
	cfg HostsConfig

	mu      sync.Mutex             // guards greeted and locks; never held across I/O
	greeted map[string]greeting    // each host's last successful Hello
	locks   map[string]*sync.Mutex // one per host, held across the client lookup and Hello
}

// greeting is a host's last successful Hello and the client generation that
// answered it.
type greeting struct {
	gen   uint64
	hello core.AgentHello
}

var _ core.HostDirectory = (*Hosts)(nil)

// NewHosts returns a directory over cfg.Clients. A nil Clients is a wiring
// mistake in the one place that builds a Hosts, so it panics there rather
// than failing every later call.
func NewHosts(cfg HostsConfig) *Hosts {
	if cfg.Clients == nil {
		panic("dispatch: HostsConfig.Clients is required")
	}
	return &Hosts{cfg: cfg, greeted: map[string]greeting{}, locks: map[string]*sync.Mutex{}}
}

// Agent returns hostID's client, after the first successful Hello of its
// current generation. An id with no core_hosts row is not_found; a host
// without a client answers host.offline. A Hello that hangs delays only the
// calls for its own host.
func (h *Hosts) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	unlock, err := h.lockHost(ctx, hostID)
	if err != nil {
		return nil, err
	}
	defer unlock()
	c, gen, err := h.cfg.Clients.Client(ctx, hostID)
	if err != nil {
		return nil, err
	}
	// A source may number its generations from 0, so a missing entry must not
	// read as a greeted generation 0.
	h.mu.Lock()
	greeted, ok := h.greeted[hostID]
	h.mu.Unlock()
	if !ok || greeted.gen != gen {
		if err := h.hello(ctx, hostID, c, gen); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Hello calls hostID's Hello now and records every module's availability.
// The server calls it for the local host at startup, which seeds the host's
// module rows.
func (h *Hosts) Hello(ctx context.Context, hostID string) error {
	unlock, err := h.lockHost(ctx, hostID)
	if err != nil {
		return err
	}
	defer unlock()
	c, gen, err := h.cfg.Clients.Client(ctx, hostID)
	if err != nil {
		return err
	}
	return h.hello(ctx, hostID, c, gen)
}

// lockHost takes hostID's lock once core_hosts has the host, so that a
// mistyped id stays not_found without growing the lock table and only a known
// host is reported offline. Holding the lock across the client lookup and
// Hello keeps concurrent calls for one host to a single Hello.
func (h *Hosts) lockHost(ctx context.Context, hostID string) (unlock func(), err error) {
	var one int
	err = h.cfg.DB.Read().QueryRowContext(ctx, "SELECT 1 FROM core_hosts WHERE id = ?", hostID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	case err != nil:
		return nil, fmt.Errorf("dispatch: read host %s: %w", hostID, err)
	}
	h.mu.Lock()
	l, ok := h.locks[hostID]
	if !ok {
		l = &sync.Mutex{}
		h.locks[hostID] = l
	}
	h.mu.Unlock()
	l.Lock()
	return l.Unlock, nil
}

// greetingOf returns hostID's last successful Hello in this process.
func (h *Hosts) greetingOf(hostID string) (core.AgentHello, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	g, ok := h.greeted[hostID]
	return g.hello, ok
}

// JournalEpoch is the journal epoch hostID's agent reported in its last
// successful Hello: empty before one, or from an agent that reports none.
// The audit ingester reads it right after Agent, which says Hello first.
func (h *Hosts) JournalEpoch(hostID string) string {
	hello, _ := h.greetingOf(hostID)
	return hello.JournalEpoch
}

// hello runs under hostID's lock. It writes the agent's version, protocol,
// platform and last_seen_at to core_hosts, and available, reason and
// checked_at of every module the registry has; a missing row takes enabled
// from the module's DefaultEnabled, and an existing row keeps its enabled,
// which only core.module.enable and core.module.disable change. On success
// it records the Hello as gen's.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient, gen uint64) error {
	hello, err := c.Hello(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: hello %s: %w", hostID, err)
	}
	if hello.HostID != hostID {
		return fmt.Errorf("dispatch: host %s answered Hello as %s", hostID, hello.HostID)
	}
	now := h.cfg.Clock.Now().UTC().Format(timeFormat)
	err = h.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_hosts SET agent_version = ?, protocol_version = ?, os = ?, arch = ?, last_seen_at = ? WHERE id = ?`,
			nullText(hello.BinaryVersion), hello.ProtocolVersion, nullText(hello.OS), nullText(hello.Arch), now, hostID); err != nil {
			return err
		}
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
		return fmt.Errorf("dispatch: record the hello of %s: %w", hostID, err)
	}
	h.mu.Lock()
	h.greeted[hostID] = greeting{gen: gen, hello: hello}
	h.mu.Unlock()
	h.cfg.Log.Info("dispatch: host said hello", "host", hostID, "protocol", hello.ProtocolVersion,
		"version", hello.BinaryVersion, "os", hello.OS, "arch", hello.Arch, "journalEpoch", hello.JournalEpoch)
	return nil
}

// nullText stores an empty string as NULL, which reads as "not reported".
func nullText(s string) any {
	if s == "" {
		return nil
	}
	return s
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

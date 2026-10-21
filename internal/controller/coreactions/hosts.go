package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ReasonNotReported is the reason of a compiled-in module that has no
// core_module_state row for a host yet.
const ReasonNotReported = "not reported by the host yet"

// ModuleListInput is the input of core.module.list/v1.
type ModuleListInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host whose modules to list."`
}

// ModuleList is the output of core.module.list/v1.
type ModuleList struct {
	Modules []core.ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host, in registration order."`
}

func (m module) hostActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, core.HostList](core.ActionSpec{
			Name: "core.host.list", Version: 1, Side: core.SideController,
			Summary: "List the managed hosts with the state of every module on each.",
		}, hostList{m}),
		core.DefineRead[ModuleListInput, ModuleList](core.ActionSpec{
			Name: "core.module.list", Version: 1, Side: core.SideController,
			Summary: "List the state of every compiled-in module on one host.",
		}, moduleList{m}),
	}
}

type hostList struct{ m module }

func (h hostList) Read(ctx context.Context, env core.Env, _ NoInput) (core.HostList, error) {
	q := env.DB.Read()
	rows, err := q.QueryContext(ctx, `SELECT `+hostColumns+` FROM core_hosts ORDER BY is_local DESC, name, id`)
	if err != nil {
		return core.HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	out := core.HostList{Hosts: []core.Host{}}
	for rows.Next() {
		host, err := scanHost(rows)
		if err != nil {
			_ = rows.Close()
			return core.HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
		}
		out.Hosts = append(out.Hosts, host)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return core.HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	for i := range out.Hosts {
		if err := h.m.complete(ctx, env, q, &out.Hosts[i]); err != nil {
			return core.HostList{}, err
		}
	}
	return out, nil
}

// hostColumns are the core_hosts columns scanHost reads, in its order.
const hostColumns = `id, name, is_local, state, last_seen_at, agent_version, protocol_version, os, arch`

// scanHost reads one row of hostColumns: the stored half of a Host.
func scanHost(row interface{ Scan(dest ...any) error }) (core.Host, error) {
	var h core.Host
	var lastSeen, version, osName, arch sql.NullString
	var protocol sql.NullInt64
	if err := row.Scan(&h.ID, &h.Name, &h.IsLocal, &h.State, &lastSeen, &version, &protocol, &osName, &arch); err != nil {
		return core.Host{}, err
	}
	if lastSeen.Valid {
		at, err := parseTime(lastSeen.String)
		if err != nil {
			return core.Host{}, err
		}
		h.LastSeenAt = &at
	}
	if protocol.Valid {
		p := int(protocol.Int64)
		h.ProtocolVersion = &p
	}
	h.AgentVersion, h.OS, h.Arch = nullable(version), nullable(osName), nullable(arch)
	return h, nil
}

func nullable(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// complete fills in the half of h that core_hosts does not store: whether
// its agent is online, whether its version is supported, its modules and its
// journal ingestion health.
func (m module) complete(ctx context.Context, env core.Env, q querier, h *core.Host) error {
	h.Online = online(ctx, env, h.ID)
	h.Supported = h.AgentVersion != nil && h.ProtocolVersion != nil &&
		(m.d.Version == "" || Supported(m.d.Version, *h.AgentVersion, *h.ProtocolVersion))
	var err error
	if h.Modules, err = m.moduleStates(ctx, q, h.ID); err != nil {
		return err
	}
	h.Ingestion, err = ingestion(ctx, q, h.ID)
	return err
}

// ingestion reads hostID's core_ingest_status row; a host without one has
// not been ingested yet and has a nil state.
func ingestion(ctx context.Context, q querier, hostID string) (*core.IngestionStatus, error) {
	var since string
	var msg sql.NullString
	err := q.QueryRowContext(ctx, `SELECT since, error FROM core_ingest_status WHERE host_id = ?`, hostID).Scan(&since, &msg)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("coreactions: read ingestion status: %w", err)
	}
	at, err := parseTime(since)
	if err != nil {
		return nil, err
	}
	return &core.IngestionStatus{OK: !msg.Valid, Since: at, Error: nullable(msg)}, nil
}

// onlineTimeout bounds one host's online check. The check greets an agent
// not yet greeted on its session, and an agent whose heartbeat answers but
// whose Hello hangs would otherwise hold the host list for every caller.
const onlineTimeout = 5 * time.Second

// online reports whether hostID's agent has a live session. A host that
// answers host.offline is offline; so is one that fails in any other way,
// which is logged, because one broken agent must not fail a list of hosts.
func online(ctx context.Context, env core.Env, hostID string) bool {
	if env.Agents == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, onlineTimeout)
	defer cancel()
	_, err := env.Agents.Agent(ctx, hostID)
	if err != nil && errcode.From(err).Code != errcode.HostOffline && env.Log != nil {
		env.Log.Warn("coreactions: host unreachable", "host", hostID, "error", err)
	}
	return err == nil
}

type moduleList struct{ m module }

func (h moduleList) Read(ctx context.Context, env core.Env, in ModuleListInput) (ModuleList, error) {
	if err := hostExists(ctx, env.DB.Read(), in.HostID); err != nil {
		return ModuleList{}, err
	}
	states, err := h.m.moduleStates(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return ModuleList{}, err
	}
	return ModuleList{Modules: states}, nil
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// hostExists returns not_found for a host id core_hosts does not hold.
func hostExists(ctx context.Context, q querier, hostID string) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE id = ?`, hostID).Scan(&n); err != nil {
		return fmt.Errorf("coreactions: read host: %w", err)
	}
	if n == 0 {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	return nil
}

// moduleStates returns the state of every compiled-in module on hostID, in
// registration order. Rows of modules this binary does not have are left out.
func (m module) moduleStates(ctx context.Context, q querier, hostID string) ([]core.ModuleState, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT module_id, enabled, available, reason, checked_at FROM core_module_state WHERE host_id = ?`, hostID)
	if err != nil {
		return nil, fmt.Errorf("coreactions: read module state: %w", err)
	}
	stored := map[string]core.ModuleState{}
	for rows.Next() {
		var s core.ModuleState
		var reason sql.NullString
		var checked string
		if err := rows.Scan(&s.ID, &s.Enabled, &s.Available, &reason, &checked); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("coreactions: read module state: %w", err)
		}
		if reason.Valid && !s.Available {
			s.Reason = &reason.String
		}
		at, err := parseTime(checked)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		s.CheckedAt = &at
		stored[s.ID] = s
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("coreactions: read module state: %w", err)
	}
	out := make([]core.ModuleState, 0, len(m.catalog))
	for _, info := range m.catalog {
		s, ok := stored[info.id]
		if !ok {
			reason := ReasonNotReported
			s = core.ModuleState{ID: info.id, Reason: &reason}
		}
		s.Version = info.version
		out = append(out, s)
	}
	return out, nil
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("coreactions: parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}

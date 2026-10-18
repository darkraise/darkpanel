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

// ModuleState is one module's enablement on one host (spec section 12). A
// module can be used only when it is both enabled and available.
type ModuleState struct {
	ID        string     `json:"id" jsonschema_description:"Module id."`
	Version   int        `json:"version" jsonschema_description:"Module version compiled into this binary."`
	Enabled   bool       `json:"enabled" jsonschema_description:"Whether an operator enabled the module on the host."`
	Available bool       `json:"available" jsonschema_description:"Whether the module's migrations and preflight checks passed on the host."`
	Reason    *string    `json:"reason" jsonschema_description:"Why the module is unavailable; null when it is available."`
	CheckedAt *time.Time `json:"checkedAt" jsonschema_description:"When availability was last checked; null when it never was."`
}

// Host is a managed host (spec section 12).
type Host struct {
	ID      string        `json:"id" jsonschema_description:"Host id."`
	Name    string        `json:"name" jsonschema_description:"Host name."`
	IsLocal bool          `json:"isLocal" jsonschema_description:"Whether the host runs this controller."`
	Online  bool          `json:"online" jsonschema_description:"Whether the host's agent is reachable; always true for the local host in M1."`
	Modules []ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host."`
}

// HostList is the output of core.host.list/v1.
type HostList struct {
	Hosts []Host `json:"hosts" jsonschema_description:"Every host, the local host first, then by name."`
}

// ModuleListInput is the input of core.module.list/v1.
type ModuleListInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host whose modules to list."`
}

// ModuleList is the output of core.module.list/v1.
type ModuleList struct {
	Modules []ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host, in registration order."`
}

func (m module) hostActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, HostList](core.ActionSpec{
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

func (h hostList) Read(ctx context.Context, env core.Env, _ NoInput) (HostList, error) {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT id, name, is_local FROM core_hosts ORDER BY is_local DESC, name, id`)
	if err != nil {
		return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	out := HostList{Hosts: []Host{}}
	for rows.Next() {
		var host Host
		if err := rows.Scan(&host.ID, &host.Name, &host.IsLocal); err != nil {
			_ = rows.Close()
			return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
		}
		host.Online = host.IsLocal
		out.Hosts = append(out.Hosts, host)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	for i := range out.Hosts {
		if out.Hosts[i].Modules, err = h.m.moduleStates(ctx, env.DB.Read(), out.Hosts[i].ID); err != nil {
			return HostList{}, err
		}
	}
	return out, nil
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
func (m module) moduleStates(ctx context.Context, q querier, hostID string) ([]ModuleState, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT module_id, enabled, available, reason, checked_at FROM core_module_state WHERE host_id = ?`, hostID)
	if err != nil {
		return nil, fmt.Errorf("coreactions: read module state: %w", err)
	}
	stored := map[string]ModuleState{}
	for rows.Next() {
		var s ModuleState
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
	out := make([]ModuleState, 0, len(m.catalog))
	for _, info := range m.catalog {
		s, ok := stored[info.id]
		if !ok {
			reason := ReasonNotReported
			s = ModuleState{ID: info.id, Reason: &reason}
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

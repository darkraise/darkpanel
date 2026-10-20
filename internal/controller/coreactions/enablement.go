package coreactions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// LockModule is the lock key of core.module.enable and core.module.disable.
const LockModule = "core.module"

// ModuleInput is the input of core.module.enable/v1 and core.module.disable/v1.
type ModuleInput struct {
	HostID   string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host."`
	ModuleID string `json:"moduleId" jsonschema:"minLength=1" jsonschema_description:"Id of a compiled-in module other than core."`
}

func (m module) moduleActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[ModuleInput, core.ModuleState](core.ActionSpec{
			Name: "core.module.enable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			LockKeys: []string{LockModule}, CompensatedBy: "core.module.disable",
			Summary: "Enable a module on a host; its preflight checks run there again and set its availability.",
		}, enableModule{m}),
		core.DefineMutating[ModuleInput, core.ModuleState](core.ActionSpec{
			Name: "core.module.disable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			LockKeys: []string{LockModule}, CompensatedBy: "core.module.enable",
			Summary: "Disable a module on a host; its actions and resources answer module.unavailable.",
		}, disableModule{m}),
	}
}

// The enablement of a module on a host as a plan sees it.
const (
	stateEnabled     = "enabled"
	stateDisabled    = "disabled"
	stateNotReported = "not reported"
)

func errUnset(name string) error {
	return fmt.Errorf("coreactions: %s is not set", name)
}

// stateHash is the hex SHA-256 of parts, each prefixed with its length so no
// two lists of parts hash alike.
func stateHash(parts ...string) string {
	var sb strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&sb, "%d:%s;", len(p), p)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// target checks that in names a known host and a compiled-in module other
// than core, and returns the module's enablement on the host.
func (m module) target(ctx context.Context, q querier, in ModuleInput) (string, error) {
	if in.ModuleID == core.ModuleIDCore {
		return "", &errcode.Error{
			Code: errcode.InvalidInput, Detail: "the core module is always enabled",
			Fields: []errcode.FieldError{{Path: "/moduleId", Message: "the core module cannot be enabled or disabled"}},
		}
	}
	known := false
	for _, info := range m.catalog {
		known = known || info.id == in.ModuleID
	}
	if !known {
		return "", errcode.New(errcode.NotFound, fmt.Sprintf("no module %s in this binary", in.ModuleID))
	}
	if err := hostExists(ctx, q, in.HostID); err != nil {
		return "", err
	}
	var enabled bool
	err := q.QueryRowContext(ctx, `SELECT enabled FROM core_module_state WHERE host_id = ? AND module_id = ?`,
		in.HostID, in.ModuleID).Scan(&enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return stateNotReported, nil
	case err != nil:
		return "", fmt.Errorf("coreactions: read module state: %w", err)
	case enabled:
		return stateEnabled, nil
	default:
		return stateDisabled, nil
	}
}

// state returns one module's state on one host.
func (m module) state(ctx context.Context, q querier, hostID, moduleID string) (core.ModuleState, error) {
	states, err := m.moduleStates(ctx, q, hostID)
	if err != nil {
		return core.ModuleState{}, err
	}
	for _, s := range states {
		if s.ID == moduleID {
			return s, nil
		}
	}
	return core.ModuleState{}, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s in this binary", moduleID))
}

// wantEnabled is Verify of both actions: it decides from the stored row
// alone, because recovery passes no output.
func wantEnabled(ctx context.Context, env core.Env, in ModuleInput, want bool) error {
	var enabled bool
	err := env.DB.Read().QueryRowContext(ctx, `SELECT enabled FROM core_module_state WHERE host_id = ? AND module_id = ?`,
		in.HostID, in.ModuleID).Scan(&enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("coreactions: module %s has no state on host %s", in.ModuleID, in.HostID)
	case err != nil:
		return fmt.Errorf("coreactions: read module state: %w", err)
	case enabled != want:
		return fmt.Errorf("coreactions: module %s on host %s: enabled is %t, want %t", in.ModuleID, in.HostID, enabled, want)
	}
	return nil
}

type enableModule struct{ m module }

func (h enableModule) Plan(ctx context.Context, env core.Env, in ModuleInput) (core.PlanOutcome, error) {
	cur, err := h.m.target(ctx, env.DB.Read(), in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("module %s on host %s", in.ModuleID, in.HostID)
	var diff []core.DiffLine
	if cur == stateEnabled {
		diff = append(diff, core.DiffLine{Op: " ", Text: subject + ": already enabled; its preflight checks run again"})
	} else {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: subject + ": " + cur},
			core.DiffLine{Op: "+", Text: subject + ": enabled; its preflight checks run again and set its availability"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.module.disable"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(in.HostID, in.ModuleID, cur)}, nil
}

func (h enableModule) Apply(ctx context.Context, env core.Env, in ModuleInput) (core.ModuleState, error) {
	if env.Agents == nil {
		return core.ModuleState{}, errUnset("Env.Agents")
	}
	agent, err := env.Agents.Agent(ctx, in.HostID)
	if err != nil {
		return core.ModuleState{}, err
	}
	status, err := agent.Preflight(ctx, in.ModuleID)
	if err != nil {
		return core.ModuleState{}, err
	}
	var reason any
	if !status.Available {
		reason = status.Reason
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES (?, ?, 1, ?, ?, ?)
			ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = 1, available = excluded.available,
			reason = excluded.reason, checked_at = excluded.checked_at`,
			in.HostID, in.ModuleID, status.Available, reason, formatTime(env.Clock.Now()))
		return err
	})
	if err != nil {
		return core.ModuleState{}, fmt.Errorf("coreactions: enable module: %w", err)
	}
	return h.m.state(ctx, env.DB.Read(), in.HostID, in.ModuleID)
}

func (h enableModule) Verify(ctx context.Context, env core.Env, in ModuleInput, _ core.ModuleState) error {
	return wantEnabled(ctx, env, in, true)
}

type disableModule struct{ m module }

func (h disableModule) Plan(ctx context.Context, env core.Env, in ModuleInput) (core.PlanOutcome, error) {
	cur, err := h.m.target(ctx, env.DB.Read(), in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("module %s on host %s", in.ModuleID, in.HostID)
	var diff []core.DiffLine
	if cur == stateDisabled {
		diff = append(diff, core.DiffLine{Op: " ", Text: subject + ": already disabled; nothing changes"})
	} else {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: subject + ": " + cur},
			core.DiffLine{Op: "+", Text: subject + ": disabled; its actions and resources answer module.unavailable"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.module.enable"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(in.HostID, in.ModuleID, cur)}, nil
}

func (h disableModule) Apply(ctx context.Context, env core.Env, in ModuleInput) (core.ModuleState, error) {
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES (?, ?, 0, 0, ?, ?)
			ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = 0`,
			in.HostID, in.ModuleID, ReasonNotReported, formatTime(env.Clock.Now()))
		return err
	})
	if err != nil {
		return core.ModuleState{}, fmt.Errorf("coreactions: disable module: %w", err)
	}
	return h.m.state(ctx, env.DB.Read(), in.HostID, in.ModuleID)
}

func (h disableModule) Verify(ctx context.Context, env core.Env, in ModuleInput, _ core.ModuleState) error {
	return wantEnabled(ctx, env, in, false)
}

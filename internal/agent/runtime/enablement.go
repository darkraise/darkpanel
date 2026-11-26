package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// MetaModuleEnabledPrefix starts the core_meta key of a module's enabled flag
// (spec 1.4): module_enabled.<id> holds "1" or "0" as the controller last
// pushed it. The controller's core_module_state stays the authority; only the
// background tasks read this copy.
const MetaModuleEnabledPrefix = "module_enabled."

var _ core.AgentEnablement = (*Runtime)(nil)

// SetModuleEnabled stores moduleID's enabled flag as the controller pushed
// it, calls the module's EnablementHook, closes the module's channels when
// it is now off, then starts or stops the module's background tasks; when
// it stops them it returns once their Run and Watch returned. A module the
// registry lacks is not_found. A module the policy file denies keeps the
// stored flag for when the policy allows it, but runs as disabled, and
// enabling it is refused with module.unavailable.
func (r *Runtime) SetModuleEnabled(ctx context.Context, moduleID string, enabled bool) error {
	if _, ok := r.cfg.Registry.Module(moduleID); !ok {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
	r.enableMu.Lock()
	defer r.enableMu.Unlock()
	value := "0"
	if enabled {
		value = "1"
	}
	err := r.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, MetaModuleEnabledPrefix+moduleID, value)
		return err
	})
	if err != nil {
		return fmt.Errorf("runtime: store the enabled flag of %s: %w", moduleID, err)
	}
	r.callHook(ctx, moduleID, enabled)
	if !enabled || r.Policy().Denied(moduleID) {
		r.CloseModuleChannels(moduleID)
	}
	if err := r.syncTasks(ctx, moduleID); err != nil {
		return err
	}
	if enabled && r.Policy().Denied(moduleID) {
		return deniedError(moduleID)
	}
	return nil
}

// callHook calls moduleID's EnablementHook, when it has one, with enabled,
// or with false when the policy file denies the module. The flag is stored
// already, so a failing or panicking hook is logged and the flag stands.
func (r *Runtime) callHook(ctx context.Context, moduleID string, enabled bool) {
	h, ok := r.cfg.Registry.EnablementHook(moduleID)
	if !ok {
		return
	}
	enabled = enabled && !r.Policy().Denied(moduleID)
	err := lifecycle.Protect(r.cfg.Log, moduleID, "enablement hook", func() error {
		return h.OnEnabled(ctx, r.moduleEnv(moduleID), enabled)
	})
	if err != nil {
		r.cfg.Log.Warn("runtime: enablement hook", "module", moduleID, "enabled", enabled, "error", err)
	}
}

// callHooks calls every EnablementHook once with its module's stored flag,
// as the agent starts; callHook passes false for a module the policy denies.
func (r *Runtime) callHooks(ctx context.Context) {
	for _, m := range r.modules() {
		if _, ok := r.cfg.Registry.EnablementHook(m.ID); !ok {
			continue
		}
		enabled, err := r.moduleEnabled(ctx, m.ID)
		if err != nil {
			r.cfg.Log.Warn("runtime: enablement hook", "module", m.ID, "error", err)
			continue
		}
		r.callHook(ctx, m.ID, enabled)
	}
}

// moduleEnabled reads moduleID's enabled flag; without one the module's
// DefaultEnabled holds, as it does on the controller before an operator
// enables or disables the module.
func (r *Runtime) moduleEnabled(ctx context.Context, moduleID string) (bool, error) {
	m, ok := r.cfg.Registry.Module(moduleID)
	if !ok {
		return false, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
	var value string
	err := r.cfg.DB.Read().QueryRowContext(ctx, "SELECT value FROM core_meta WHERE key = ?",
		MetaModuleEnabledPrefix+moduleID).Scan(&value)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return m.DefaultEnabled, nil
	case err != nil:
		return false, fmt.Errorf("runtime: read the enabled flag of %s: %w", moduleID, err)
	}
	return value == "1", nil
}

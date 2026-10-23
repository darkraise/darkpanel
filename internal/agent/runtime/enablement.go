package runtime

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// MetaModuleEnabledPrefix starts the core_meta key of a module's enabled flag
// (spec 1.4): module_enabled.<id> holds "1" or "0" as the controller last
// pushed it. The controller's core_module_state stays the authority; only the
// background tasks read this copy.
const MetaModuleEnabledPrefix = "module_enabled."

var _ core.AgentEnablement = (*Runtime)(nil)

// SetModuleEnabled stores moduleID's enabled flag as the controller pushed
// it, then starts or stops the module's background tasks; when it stops them
// it returns once their Run and Watch returned. A module the registry lacks
// is not_found.
func (r *Runtime) SetModuleEnabled(ctx context.Context, moduleID string, enabled bool) error {
	if _, ok := r.cfg.Registry.Module(moduleID); !ok {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
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
	return r.syncTasks(ctx, moduleID)
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

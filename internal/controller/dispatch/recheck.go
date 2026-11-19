package dispatch

import (
	"context"
	"database/sql"
	"fmt"
)

// recheckTimeFormat is the checked_at format core.module.enable writes.
const recheckTimeFormat = "2006-01-02T15:04:05.000000000Z"

// RecheckModules asks the agent on hostID for each module's preflight and
// records the answer in core_module_state, leaving enabled as it is; a
// module without a row is recorded disabled. Each module's cached
// availability is retired, so the next request asks the agent again. An
// agent that cannot be reached leaves the rows as they are: the error is
// logged, not returned, because the change that asked has already ended.
func (d *Dispatcher) RecheckModules(ctx context.Context, hostID string, modules []string) error {
	agent, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		d.cfg.Log.Warn("dispatch: recheck modules: host unreachable", "host", hostID, "modules", modules, "error", err)
		return nil
	}
	for _, module := range modules {
		status, err := agent.Preflight(ctx, module)
		if err != nil {
			d.cfg.Log.Warn("dispatch: recheck module", "host", hostID, "module", module, "error", err)
			continue
		}
		var reason any
		if !status.Available {
			reason = status.Reason
		}
		now := d.cfg.Clock.Now().UTC().Format(recheckTimeFormat)
		err = d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
				VALUES (?, ?, 0, ?, ?, ?)
				ON CONFLICT (host_id, module_id) DO UPDATE SET available = excluded.available,
				reason = excluded.reason, checked_at = excluded.checked_at`,
				hostID, module, status.Available, reason, now)
			return err
		})
		if err != nil {
			return fmt.Errorf("dispatch: record the availability of module %s on host %s: %w", module, hostID, err)
		}
		d.avail.retire(availabilityKey{hostID, module})
	}
	return nil
}

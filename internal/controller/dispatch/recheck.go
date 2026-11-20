package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// recheckTimeFormat is the checked_at format core.module.enable writes.
const recheckTimeFormat = "2006-01-02T15:04:05.000000000Z"

// RecheckModules asks the agent on hostID for each module's preflight and
// records the answer in core_module_state, leaving enabled as it is. Only a
// module that already has a row is updated: rows are seeded at Hello from
// each module's default, and the recheck must not invent one. Each recorded
// module's cached availability is retired, so the next request asks the
// agent again. An agent that cannot be reached, or a preflight that fails,
// leaves the rows as they are and is returned, so the caller can retry.
func (d *Dispatcher) RecheckModules(ctx context.Context, hostID string, modules []string) error {
	agent, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return fmt.Errorf("dispatch: recheck modules on host %s: host unreachable: %w", hostID, err)
	}
	var failed []error
	for _, module := range modules {
		status, err := agent.Preflight(ctx, module)
		if err != nil {
			failed = append(failed, fmt.Errorf("dispatch: recheck module %s on host %s: %w", module, hostID, err))
			continue
		}
		var reason any
		if !status.Available {
			reason = status.Reason
		}
		now := d.cfg.Clock.Now().UTC().Format(recheckTimeFormat)
		err = d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE core_module_state SET available = ?, reason = ?, checked_at = ?
				WHERE host_id = ? AND module_id = ?`,
				status.Available, reason, now, hostID, module)
			return err
		})
		if err != nil {
			return fmt.Errorf("dispatch: record the availability of module %s on host %s: %w", module, hostID, err)
		}
		d.avail.retire(availabilityKey{hostID, module})
	}
	return errors.Join(failed...)
}

// Package jobs is the scheduled jobs module (M11 spec section 6): systemd timers the panel owns. It is off until an admin turns it on.
package jobs

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "jobs"

type module struct{}

// Module returns the jobs module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: false}
}

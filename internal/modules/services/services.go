// Package services is the services and journal module (M11 spec section 2): systemd units and the journal.
package services

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "services"

type module struct{}

// Module returns the services module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

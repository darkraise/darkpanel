// Package firewall is the UFW firewall module (spec section 2): rules, safe
// apply, snapshots, the Docker integration and the exposure view.
package firewall

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "firewall"

type module struct{}

// Module returns the firewall module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

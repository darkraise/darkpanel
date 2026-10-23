// Package tailscale is the Tailscale node module (spec 4.1): the node's
// status, preferences and tailscaled package.
package tailscale

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "tailscale"

type module struct{}

// Module returns the Tailscale node module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

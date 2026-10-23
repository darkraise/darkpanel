// Package panel is the panel exposure module (spec section 8): exposing the
// Darkpanel UI through Nginx Proxy Manager or a tunnel, and removing it.
package panel

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "panel"

type module struct{}

// Module returns the panel exposure module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

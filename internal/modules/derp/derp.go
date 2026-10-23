// Package derp is the DERP server module (spec 4.3): a custom derper on a
// host and its region in the tailnet policy.
package derp

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "derp"

type module struct{}

// Module returns the DERP module.
func Module() core.Module { return module{} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

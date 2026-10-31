// Package tailnet is the controller-side tailnet module (spec 4.2): the
// OAuth connection, devices, auth keys and the policy file.
package tailnet

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "tailnet"

type module struct{ t tn }

// Module returns the tailnet module.
func Module() core.Module { return module{t: productionTN()} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares no action, resource or check yet.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

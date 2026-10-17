// Package coreactions is the core module (spec section 10): the host, module,
// token, audit and user actions, under module id "core".
package coreactions

import "github.com/darkraise/darkpanel/internal/core"

// Deps are the controller services the core actions call. Callers that need
// only the registry's shape pass the zero value.
type Deps struct{}

// Module returns the core module.
func Module(_ Deps) core.Module { return module{} }

type module struct{}

// Manifest declares the core module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, DefaultEnabled: true}
}

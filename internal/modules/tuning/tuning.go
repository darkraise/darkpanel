// Package tuning is the tuning module (M11 spec section 7): kernel parameters and the swap file.
package tuning

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "tuning"

type module struct{}

// Module returns the tuning module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

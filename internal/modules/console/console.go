// Package console is the console module (M11 spec section 5): the web terminal and file manager. It is off until an admin turns it on.
package console

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "console"

type module struct{}

// Module returns the console module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: false}
}

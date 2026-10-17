// Package modules lists the compiled-in modules. Adding a module adds one
// line to All and changes nothing else in the core (R5).
package modules

import "github.com/darkraise/darkpanel/internal/core"

// All returns every compiled-in module in registration order.
func All() []core.Module {
	return []core.Module{}
}

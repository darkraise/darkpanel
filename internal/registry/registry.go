// Package registry builds the one module registry every component uses: the
// core module first, then modules.All() (spec section 2).
package registry

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

// Build returns the registry with a core module built from zero Deps, for
// callers that use only the registry's shape: the CLI, the schema generator
// and tests. Its core handlers must not be called.
func Build() (*core.Registry, error) {
	return BuildWith(coreactions.Module(coreactions.Deps{}))
}

// BuildWith returns the registry with the given core module, which must have
// module id "core"; the server passes one built with its stores.
func BuildWith(coreModule core.Module) (*core.Registry, error) {
	if id := coreModule.Manifest().ID; id != core.ModuleIDCore {
		return nil, fmt.Errorf("registry: core module id %q, want %q", id, core.ModuleIDCore)
	}
	return core.NewRegistry(append([]core.Module{coreModule}, modules.All()...)...)
}

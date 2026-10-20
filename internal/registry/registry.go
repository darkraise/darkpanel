// Package registry builds the one module registry every component uses: the
// core module first, then modules.All() (spec section 2).
package registry

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/update"
)

// CoreActions returns the update and rollout actions of the core manifest,
// for coreactions.Deps.Extra. Every process registers both sets so that the
// agent and the controller hold the same manifest and schema hashes: the
// agent passes its updater and runs only the update actions, the controller
// passes nil and its version and runs only the rollout actions.
func CoreActions(u *update.Updater, controllerVersion string) []core.ActionDef {
	return append(update.Actions(u), rollout.Actions(rollout.ActionsConfig{Version: controllerVersion})...)
}

// Build returns the registry with a core module built from zero Deps apart
// from the core actions and owned paths, for callers that use only the
// registry's shape: the CLI, the schema generator and tests. Its core
// handlers must not be called.
func Build() (*core.Registry, error) {
	return BuildWith(coreactions.Module(coreactions.Deps{Extra: CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}))
}

// BuildWith returns the registry with the given core module, which must have
// module id "core"; the server passes one built with its stores.
func BuildWith(coreModule core.Module) (*core.Registry, error) {
	if id := coreModule.Manifest().ID; id != core.ModuleIDCore {
		return nil, fmt.Errorf("registry: core module id %q, want %q", id, core.ModuleIDCore)
	}
	return core.NewRegistry(append([]core.Module{coreModule}, modules.All()...)...)
}

// Package panel is the panel exposure module (spec section 8): exposing the
// Darkpanel UI through Nginx Proxy Manager or a tunnel, and removing it.
package panel

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "panel"

type module struct{ d agentDeps }

// Module returns the panel exposure module. No Docker access happens before
// an agent action runs.
func Module() core.Module { return module{d: productionAgent()} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares the module's eight actions and its controller migrations, and no
// resource or check.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        regActions(m.d),
		Migrations:     map[core.Side]fs.FS{core.SideController: controllerMigrations()},
	}
}

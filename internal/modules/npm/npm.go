// Package npm is the Nginx Proxy Manager module (spec section 6): the
// Darkpanel-owned proxy container and its proxy hosts and certificates.
package npm

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "npm"

type module struct{ n nm }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9). The manifest
// declares the module's actions and no resource or preflight check. The
// module owns the managed directory of its upgrade archives and keeps its
// tables on the agent.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.n.actions(),
		OwnedPaths:     ownedPaths(),
		Migrations:     map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
	}
}

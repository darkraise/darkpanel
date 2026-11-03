// Package cloudflare is the Cloudflare Tunnel module (spec section 7): the
// account, tunnels, connectors and public hostnames.
package cloudflare

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "cloudflare"

type module struct{ c cf }

// Module returns the Cloudflare Tunnel module.
func Module() core.Module { return module{c: productionCF()} }

// Manifest declares the module: its controller-side account, tunnel and
// hostname actions, its agent-side connector actions (run, stop, list) and
// attachment claims (attach, detach), the controller composites that deploy a
// connector and expose a container through a tunnel, the controller
// migrations that hold the account, tunnel and hostname rows, the agent
// migrations that hold the connectors and their network claims, and the
// managed directory of the tunnel token files it owns on the host. Every
// module is enabled by default; preflight decides whether it is available on
// a host (R9).
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.c.actions(),
		OwnedPaths:     ownedPaths(),
		Migrations: map[core.Side]fs.FS{
			core.SideController: controllerMigrations(),
			core.SideAgent:      agentMigrations(),
		},
	}
}

// actions are spec 5's cloudflare rows: the controller-side account, tunnel
// and hostname actions, the agent-side connector actions (run, stop, list)
// and attachment claims (attach, detach), each mutating one under the
// cloudflare lock, and the controller composites that deploy a connector and
// expose a container through a tunnel or remove that exposure.
func (c cf) actions() []core.ActionDef {
	return []core.ActionDef{
		c.accountConnect(),
		c.accountDisconnect(),
		c.accountGet(),
		c.tunnelCreate(),
		c.tunnelDelete(),
		c.tunnelList(),
		c.tunnelToken(),
		c.tunnelAdopt(),
		c.hostnameAdd(),
		c.hostnameRemove(),
		c.hostnameList(),
		c.connectorRun(),
		c.connectorStop(),
		c.connectorList(),
		c.networkAttach(),
		c.networkDetach(),
		c.connectorDeploy(),
		c.exposeContainer(),
		c.exposeRemove(),
	}
}

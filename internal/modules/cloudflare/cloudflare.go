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
// hostname actions and the controller migrations that hold the account,
// tunnel and hostname rows. Every module is enabled by default; preflight
// decides whether it is available on a host (R9).
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.c.actions(),
		Migrations:     map[core.Side]fs.FS{core.SideController: controllerMigrations()},
	}
}

// actions are spec 5's cloudflare account, tunnel and hostname rows, all
// controller-side under the cloudflare lock.
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
	}
}

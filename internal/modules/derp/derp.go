// Package derp is the DERP server module (spec 4.3): a custom derper on a
// host and its region in the tailnet policy.
package derp

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "derp"

type module struct{ d dp }

// Module returns the DERP module: it installs, updates and removes a derper
// on an agent host, renews its IP certificate, and publishes its region in
// the tailnet policy from the controller.
func Module() core.Module { return module{d: productionDP()} }

// Manifest declares the module: its agent and controller actions, the paths
// and the unit it owns, and both sides' migrations. Every module is enabled
// by default; each action's own availability check decides whether it can
// run on a host (R9). The derp.servers service comes from module.Controller
// in servers.go.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.d.actions(),
		OwnedPaths:     ownedPaths(),
		Migrations: map[core.Side]fs.FS{
			core.SideAgent:      agentMigrations(),
			core.SideController: controllerMigrations(),
		},
	}
}

func (d dp) actions() []core.ActionDef {
	return []core.ActionDef{
		d.serverInstall(),
		d.binaryUpdate(),
		d.serverStatus(),
		d.nodeProbe(),
		d.serverUninstall(),
		d.certRenew(),
		d.serverProbe(),
		d.serverList(),
		d.regionEdits(),
		d.fallbackCheck(),
		d.serverDeploy(),
		d.serverUpgrade(),
		d.certRotate(),
		d.regionSet(),
		d.regionRemove(),
		d.serverRemove(),
	}
}

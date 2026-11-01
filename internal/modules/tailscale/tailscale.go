// Package tailscale is the Tailscale node module (spec 4.1): the node's
// status, preferences and tailscaled package.
package tailscale

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "tailscale"

type module struct{ t ts }

// Module returns the Tailscale node module.
func Module() core.Module { return module{t: productionTS()} }

// Manifest declares the module: the node's preference, login and package
// actions, the certificate read, the status and latency resources, the
// paths and units it manages and its agent migrations. Every module is
// enabled by default; preflight (the OS check) decides whether it is
// available on a host (R9).
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.t.actions(),
		Resources:      m.t.resources(),
		Preflight:      m.t.preflight(),
		OwnedPaths:     ownedPaths(),
		ExternalUnits:  externalUnits(),
		Migrations:     map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
	}
}

func (t ts) actions() []core.ActionDef {
	return []core.ActionDef{
		t.prefsSet(), t.prefsRepair(), t.nodeLogin(), t.nodeLogout(),
		t.packageInstall(), t.packageUpdate(), t.packagePin(), t.packageUnpin(),
		t.certGet(),
	}
}

func (t ts) resources() []core.ResourceDef {
	return []core.ResourceDef{t.statusResource(), t.latencyResource()}
}

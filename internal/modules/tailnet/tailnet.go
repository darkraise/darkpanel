// Package tailnet is the controller-side tailnet module (spec 4.2): the
// OAuth connection, devices, auth keys and the policy file.
package tailnet

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "tailnet"

type module struct{ t tn }

// Module returns the tailnet module.
func Module() core.Module { return module{t: productionTN()} }

// Manifest declares the module: its controller-side actions and the
// controller migrations that hold the account and the auth-key rows. Every
// module is enabled by default; preflight decides whether it is available
// on a host (R9).
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Actions:        m.t.actions(),
		Migrations:     map[core.Side]fs.FS{core.SideController: controllerMigrations()},
	}
}

// actions are spec 5's tailnet rows, all controller-side under the tailnet
// lock, plus the read actions account.get and authkey.list that spec 5 lets
// the building wave add.
func (t tn) actions() []core.ActionDef {
	return []core.ActionDef{
		t.accountConnect(),
		t.accountDisconnect(),
		t.accountGet(),
		t.deviceList(),
		t.deviceAuthorize(),
		t.deviceSetRoutes(),
		t.deviceExpire(),
		t.deviceDelete(),
		t.authkeyCreate(),
		t.authkeyList(),
		t.policyGet(),
		t.policySet(),
		t.policyPatch(),
	}
}

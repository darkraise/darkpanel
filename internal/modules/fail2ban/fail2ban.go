// Package fail2ban is the intrusion prevention module (M11 spec section 4): fail2ban jails and bans.
package fail2ban

import "github.com/darkraise/darkpanel/internal/core"

// ID is the module id.
const ID = "fail2ban"

type module struct{}

// Module returns the fail2ban module.
func Module() core.Module { return module{} }

// Manifest declares the module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true}
}

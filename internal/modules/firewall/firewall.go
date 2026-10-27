// Package firewall is the UFW firewall module (spec section 2): rules, safe
// apply, snapshots, the Docker integration and the exposure view.
package firewall

import (
	"io/fs"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/firewall/fwstate"
)

// ID is the module id.
const ID = "firewall"

// controllerUnit is the controller's unit, whose state tells the guard
// whether a controller runs on this host (owner ruling R3).
const controllerUnit = "darkpanel-controller.service"

// ufwSysctlFile is the only sysctl file UFW may apply that Darkpanel
// supports; snapshots restore it.
const ufwSysctlFile = "/etc/ufw/sysctl.conf"

// sysctlKeys are the supported sysctl keys of spec 2.1 with the values
// Darkpanel writes back; <iface> is all, default or an interface.
var sysctlKeys = []struct{ key, pattern string }{
	{"net/ipv4/ip_forward", "^[01]$"},
	{"net/ipv6/conf/<iface>/forwarding", "^[01]$"},
	{"net/ipv6/conf/<iface>/accept_ra", "^[012]$"},
	{"net/ipv4/conf/<iface>/rp_filter", "^[012]$"},
	{"net/ipv4/conf/<iface>/accept_source_route", "^[01]$"},
}

type module struct{ f fw }

// Module returns the firewall module over this host.
func Module() core.Module { return module{f: productionFW()} }

// Manifest declares the module. Every module is enabled by default;
// preflight decides whether it is available on a host (R9).
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Preflight:      m.f.preflight(),
		OwnedPaths:     ownedPaths(),
		Migrations:     map[core.Side]fs.FS{core.SideAgent: agentMigrations()},
		ExternalUnits:  []core.ExternalUnit{{Name: controllerUnit, Ops: []string{core.UnitOpState}}},
	}
}

// ownedPaths are the files a snapshot restores (kind restore: UFW's state
// files and its sysctl file, which uninstall never removes) and the sysctl
// keys a restore writes back.
func ownedPaths() []core.OwnedPath {
	var out []core.OwnedPath
	for _, p := range append(append([]string(nil), fwstate.StateFiles...), ufwSysctlFile) {
		out = append(out, core.OwnedPath{Path: p, Kind: core.OwnedRestore})
	}
	for _, k := range sysctlKeys {
		out = append(out, core.OwnedPath{Path: k.key, Kind: core.OwnedSysctl, Pattern: k.pattern})
	}
	return out
}

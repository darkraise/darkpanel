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
		Actions:        m.f.actions(),
		Resources:      m.f.resources(),
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

// actions are the firewall's actions (section 5): every mutating one, the
// UFW actions and the eight Docker ones alike, revertible under the firewall
// lock and repaired by firewall.state.repair, and the read
// firewall.snapshot.compare.
func (f fw) actions() []core.ActionDef {
	return []core.ActionDef{
		f.stateEnable(), f.stateDisable(), f.stateRepair(), f.defaultsSet(), f.loggingSet(),
		f.rulesApply(), f.ruleAdd(), f.ruleDelete(), f.snapshotRestore(), f.snapshotCompare(),
		f.dockerEnable(), f.dockerDisable(), f.dockerRepair(), f.dockerSourcesSet(),
		f.containerAllow(), f.containerDeny(), f.containerRemove(), f.containersApply(),
	}
}

// resources are firewall.status, firewall.snapshots, firewall.docker and
// firewall.exposure.
func (f fw) resources() []core.ResourceDef {
	return []core.ResourceDef{
		core.DefineResource(ID+".status", f.status), core.DefineResource(ID+".snapshots", f.snapshots),
		f.dockerResource(), f.exposureResource(),
	}
}

// BackgroundTasks is the firewall's agent-side reconcile loop (spec 2.8);
// declaring it makes module a core.Background.
func (m module) BackgroundTasks() []core.BackgroundTask {
	return []core.BackgroundTask{m.f.reconcileTask()}
}

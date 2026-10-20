// Package install is darkpanel system install and darkpanel uninstall (spec
// section 7): the directories, users, keys, units and ownership manifest of
// one host, created idempotently and removed in a fixed order.
package install

import (
	"fmt"
	"path/filepath"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// Role is what a host runs: the controller role runs the controller and an
// agent, the agent role an agent only.
type Role string

// The two roles of system install.
const (
	RoleController Role = "controller"
	RoleAgent      Role = "agent"
)

// ParseRole parses the value of --role.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleController, RoleAgent:
		return r, nil
	}
	return "", fmt.Errorf("install: role %q: want controller or agent", s)
}

// ServiceUser is the system user the controller runs as (ADR 0005).
const ServiceUser = "darkpanel"

// RecoverUnit finishes an interrupted update at boot, before the services
// start.
const RecoverUnit = "darkpanel-update-recover.service"

// The drop-in of the controller unit that holds the agent listener flags.
const (
	DropInDir  = "darkpanel-controller.service.d"
	DropInName = "10-agent.conf"
)

// AgentLockName is the file in Paths.AgentDir that the agent daemon holds an
// exclusive flock on while it runs; uninstall takes it to prove no agent is
// left.
const AgentLockName = "agent.lock"

// Layout is every location system install and uninstall touch.
type Layout struct {
	Paths     paths.Paths
	DataDir   string // the parent of the controller, agent and update directories
	ConfigDir string // the parent of the role keys and the agent credentials
	UnitDir   string // where unit files are written
}

// Production is the layout of an installed host.
func Production() Layout {
	return Layout{
		Paths:     paths.Production(),
		DataDir:   "/var/lib/darkpanel",
		ConfigDir: "/etc/darkpanel",
		UnitDir:   "/etc/systemd/system",
	}
}

// Rooted is Production with every path moved under root, for tests.
func Rooted(root string) Layout {
	l := Production()
	for _, p := range []*string{
		&l.Paths.ControllerDir, &l.Paths.AgentDir, &l.Paths.ControllerKey, &l.Paths.AgentKey,
		&l.Paths.ControllerSocket, &l.Paths.AgentSocket, &l.Paths.AgentTLSDir, &l.Paths.UpdateDir,
		&l.Paths.BinaryDir, &l.Paths.BinaryLink, &l.DataDir, &l.ConfigDir, &l.UnitDir,
	} {
		*p = filepath.Join(root, *p)
	}
	return l
}

// UnitPath is the unit file of unit.
func (l Layout) UnitPath(unit string) string { return filepath.Join(l.UnitDir, unit) }

// DropInPath is the controller unit's agent drop-in.
func (l Layout) DropInPath() string { return filepath.Join(l.UnitDir, DropInDir, DropInName) }

// OwnedPaths is what system install creates for role, in the order uninstall
// lists it: the agent role's paths, then the controller role's additions.
func OwnedPaths(l Layout, role Role) []core.OwnedPath {
	p := l.Paths
	out := []core.OwnedPath{
		{Path: l.UnitPath(update.AgentUnit), Kind: core.OwnedUnit},
		{Path: l.UnitPath(update.RollbackUnit), Kind: core.OwnedUnit},
		{Path: l.UnitPath(RecoverUnit), Kind: core.OwnedUnit},
		{Path: p.BinaryDir, Kind: core.OwnedDir},
		{Path: p.BinaryLink, Kind: core.OwnedFile},
		{Path: l.DataDir, Kind: core.OwnedDir},
		{Path: l.ConfigDir, Kind: core.OwnedDir},
		{Path: p.AgentDir, Kind: core.OwnedDir},
		{Path: p.UpdateDir, Kind: core.OwnedDir},
		{Path: p.AgentTLSDir, Kind: core.OwnedDir},
		{Path: p.AgentKey, Kind: core.OwnedFile},
	}
	if role == RoleController {
		out = append(out,
			core.OwnedPath{Path: l.UnitPath(update.ControllerUnit), Kind: core.OwnedUnit},
			core.OwnedPath{Path: filepath.Join(l.UnitDir, DropInDir), Kind: core.OwnedDir},
			core.OwnedPath{Path: l.DropInPath(), Kind: core.OwnedUnit},
			core.OwnedPath{Path: p.ControllerDir, Kind: core.OwnedDir},
			core.OwnedPath{Path: p.ControllerKey, Kind: core.OwnedFile},
			core.OwnedPath{Path: ServiceUser, Kind: core.OwnedUser},
		)
	}
	return out
}

// CoreOwnedPaths is the core module's declaration: everything either role
// creates on a production host.
func CoreOwnedPaths() []core.OwnedPath { return OwnedPaths(Production(), RoleController) }

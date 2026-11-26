// Package users is the users and SSH module (M11 spec section 3): local accounts, SSH keys and the SSH daemon.
package users

import (
	"os"
	"path/filepath"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "users"

// deps is what the module reaches outside core.Env; tests replace it.
type deps struct {
	readFile func(name string) ([]byte, error)
	glob     func(pattern string) ([]string, error)
}

type module struct{ d deps }

// Module returns the users module.
func Module() core.Module { return module{d: deps{readFile: os.ReadFile, glob: filepath.Glob}} }

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID: ID, Version: 1, DefaultEnabled: true,
		Actions: slices.Concat(m.accountActions(), m.keyActions(), m.sshdActions()),
		OwnedPaths: []core.OwnedPath{
			{Path: stateDir, Kind: core.OwnedManagedDir, Pattern: `^expiry-[a-z_][a-z0-9_-]{0,31}$`},
			{Path: dropInFile, Kind: core.OwnedFile},
		},
		ExternalUnits: []core.ExternalUnit{{Name: sshUnit, Ops: []string{core.UnitOpState, core.UnitOpReload}}},
	}
}

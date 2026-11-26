// Package tuning is the tuning module (M11 spec section 7): kernel parameters and the swap file.
package tuning

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "tuning"

// deps is what the module reaches outside core.Env; tests replace it.
type deps struct {
	readFile func(name string) ([]byte, error)
	glob     func(pattern string) ([]string, error)
}

type module struct {
	d      deps
	owners func() []ownedKey // the sysctl keys other modules declare
}

// Module returns the tuning module. others are the other compiled-in
// modules: tuning.sysctl.get lists the sysctl keys they declare as locked.
func Module(others ...core.Module) core.Module {
	return module{d: deps{readFile: os.ReadFile, glob: filepath.Glob}, owners: sync.OnceValue(func() []ownedKey {
		return declaredKeys(others)
	})}
}

// declaredKeys are the sysctl keys of modules, dotted.
func declaredKeys(modules []core.Module) []ownedKey {
	var out []ownedKey
	for _, m := range modules {
		man := m.Manifest()
		for _, op := range man.OwnedPaths {
			if op.Kind == core.OwnedSysctl {
				out = append(out, ownedKey{module: man.ID, name: strings.ReplaceAll(op.Path, "/", ".")})
			}
		}
	}
	return out
}

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	owned := []core.OwnedPath{{Path: sysctlFile, Kind: core.OwnedFile}}
	for _, k := range curated {
		owned = append(owned, core.OwnedPath{Path: k.path(), Kind: core.OwnedSysctl, Pattern: k.pattern})
	}
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true, Actions: m.sysctlActions(), OwnedPaths: owned}
}

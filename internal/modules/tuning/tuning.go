// Package tuning is the tuning module (M11 spec section 7): kernel parameters and the swap file.
package tuning

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/platform"
)

// ID is the module id.
const ID = "tuning"

// deps is what the module reaches outside core.Env; tests replace it.
type deps struct {
	readFile  func(name string) ([]byte, error)
	glob      func(pattern string) ([]string, error)
	fileSize  func(name string) (int64, error) // a regular file's size; fs.ErrNotExist when absent
	freeSpace func(path string) (int64, error) // bytes an unprivileged writer may still use on path's filesystem
}

type module struct {
	d      deps
	owners func() []ownedKey // the sysctl keys other modules declare
}

// Module returns the tuning module. others are the other compiled-in
// modules: tuning.sysctl.get lists the sysctl keys they declare as locked.
func Module(others ...core.Module) core.Module {
	return module{d: deps{readFile: os.ReadFile, glob: filepath.Glob, fileSize: fileSize, freeSpace: freeSpace}, owners: sync.OnceValue(func() []ownedKey {
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

// fileSize is the size of the regular file name, never following a symlink.
func fileSize(name string) (int64, error) {
	fi, err := os.Lstat(name)
	if err != nil {
		return 0, err
	}
	if !fi.Mode().IsRegular() {
		return 0, fmt.Errorf("%s is not a regular file", name)
	}
	return fi.Size(), nil
}

func freeSpace(path string) (int64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, err
	}
	return int64(st.Bavail) * st.Bsize, nil //nolint:gosec // G115: a filesystem's free blocks fit an int64
}

// Manifest declares the module.
func (m module) Manifest() core.Manifest {
	owned := []core.OwnedPath{
		{Path: sysctlFile, Kind: core.OwnedFile},
		{Path: platform.SwapFile, Kind: core.OwnedFile},
		{Path: fstab, Kind: core.OwnedBlock, BlockIDs: []string{fstabBlock}},
	}
	for _, k := range curated {
		owned = append(owned, core.OwnedPath{Path: k.path(), Kind: core.OwnedSysctl, Pattern: k.pattern})
	}
	return core.Manifest{ID: ID, Version: 1, DefaultEnabled: true, Actions: slices.Concat(m.sysctlActions(), m.swapActions()), OwnedPaths: owned}
}

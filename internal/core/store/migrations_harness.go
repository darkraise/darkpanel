//go:build harnessmigration

package store

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// harnessFS holds one extra core migration per side, numbered 9000 so that it
// sorts after every real one and an older binary ignores it.
//
//go:embed harnessmigrations/*/*.sql
var harnessFS embed.FS

// withHarnessMigrations returns the core migrations of side joined with the
// harness migration of side.
func withHarnessMigrations(side string, core fs.FS) (fs.FS, error) {
	extra, err := fs.Sub(harnessFS, "harnessmigrations/"+side)
	if err != nil {
		return nil, fmt.Errorf("store: harness migrations: %w", err)
	}
	return unionFS{core, extra}, nil
}

// unionFS is two flat directories read as one; a name in both is refused,
// so no harness file can shadow a real migration.
type unionFS [2]fs.FS

func (u unionFS) Open(name string) (fs.File, error) {
	// A directory handle would list one side only, so the directory is read
	// through ReadDir alone.
	if name == "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("use ReadDir")}
	}
	f, err := u[0].Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return u[1].Open(name)
	}
	return f, err
}

// Stat answers for the directory too, which fs.WalkDir stats before it
// reads it through ReadDir.
func (u unionFS) Stat(name string) (fs.FileInfo, error) {
	info, err := fs.Stat(u[0], name)
	if name != "." && errors.Is(err, fs.ErrNotExist) {
		return fs.Stat(u[1], name)
	}
	return info, err
}

func (u unionFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	var all []fs.DirEntry
	for _, f := range u {
		entries, err := fs.ReadDir(f, ".")
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	slices.SortFunc(all, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for i := 1; i < len(all); i++ {
		if all[i].Name() == all[i-1].Name() {
			return nil, fmt.Errorf("store: migration %s is in both the core and the harness migrations", all[i].Name())
		}
	}
	return all, nil
}

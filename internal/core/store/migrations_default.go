//go:build !harnessmigration

package store

import "io/fs"

// withHarnessMigrations returns the core migrations of side unchanged; only
// the harnessmigration build adds to them.
func withHarnessMigrations(_ string, core fs.FS) (fs.FS, error) { return core, nil }

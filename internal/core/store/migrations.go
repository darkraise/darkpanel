package store

import (
	"embed"
	"fmt"
	"io/fs"
)

// Only .sql files are embedded: the runner refuses any other file it finds.
//
//go:embed migrations/*/*.sql
var migrationsFS embed.FS

// CoreMigrations returns the core migration set of side. Each file under
// migrations/<side>/ has one owning part (spec section 4).
func CoreMigrations(side string) (MigrationSet, error) {
	if side != SideController && side != SideAgent {
		return MigrationSet{}, fmt.Errorf("store: unknown side %q", side)
	}
	sub, err := fs.Sub(migrationsFS, "migrations/"+side)
	if err != nil {
		return MigrationSet{}, fmt.Errorf("store: core migrations: %w", err)
	}
	return MigrationSet{Module: "core", FS: sub}, nil
}

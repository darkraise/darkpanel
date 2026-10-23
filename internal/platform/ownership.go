package platform

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// Ownership is the core.Ownership of the agent, over core_owned_files.
type Ownership struct {
	db *store.DB
}

var _ core.Ownership = (*Ownership)(nil)

// NewOwnership returns the ownership manifest stored in the agent database.
func NewOwnership(db *store.DB) *Ownership { return &Ownership{db: db} }

// Record adds e. Recording a module's path and block id that are already
// recorded changes nothing, so the first write's time is kept. Two modules'
// blocks of the same id in one file are separate rows, as their markers are.
func (o *Ownership) Record(ctx context.Context, e core.OwnedEntry) error {
	return o.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_owned_files (path, module, kind, block_id, created_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (path, module, block_id) DO NOTHING`,
			e.Path, e.Module, e.Kind, e.BlockID, e.CreatedAt.UTC().Format(time.RFC3339))
		if err != nil {
			return fmt.Errorf("platform: record %s %s: %w", e.Path, e.BlockID, err)
		}
		return nil
	})
}

// Forget removes the entry of module for path and blockID ("" for a whole
// file); an absent entry is not an error.
func (o *Ownership) Forget(ctx context.Context, module, path, blockID string) error {
	return o.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM core_owned_files WHERE module = ? AND path = ? AND block_id = ?`,
			module, path, blockID)
		if err != nil {
			return fmt.Errorf("platform: forget %s %s: %w", path, blockID, err)
		}
		return nil
	})
}

// Owns reports whether module has an entry for path, of any kind.
func (o *Ownership) Owns(ctx context.Context, module, path string) (bool, error) {
	var n int
	err := o.db.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM core_owned_files WHERE module = ? AND path = ?`, module, path).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("platform: owns %s: %w", path, err)
	}
	return n > 0, nil
}

// List returns every entry, sorted by path, block id and module.
func (o *Ownership) List(ctx context.Context) ([]core.OwnedEntry, error) {
	rows, err := o.db.Read().QueryContext(ctx,
		`SELECT path, module, kind, block_id, created_at FROM core_owned_files ORDER BY path, block_id, module`)
	if err != nil {
		return nil, fmt.Errorf("platform: list owned files: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.OwnedEntry
	for rows.Next() {
		var e core.OwnedEntry
		var at string
		if err := rows.Scan(&e.Path, &e.Module, &e.Kind, &e.BlockID, &at); err != nil {
			return nil, fmt.Errorf("platform: list owned files: %w", err)
		}
		if e.CreatedAt, err = time.Parse(time.RFC3339, at); err != nil {
			return nil, fmt.Errorf("platform: owned file %s: created_at: %w", e.Path, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("platform: list owned files: %w", err)
	}
	return out, nil
}

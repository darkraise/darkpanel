// Package store opens the SQLite databases of both roles, applies their
// migrations and takes backups (ADR 0005).
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"

	// Registers the pure-Go "sqlite" driver (ADR 0005: no CGO).
	_ "modernc.org/sqlite"
)

// The two sides; core.SideController and core.SideAgent are defined from
// these, so a migration record and an action name the same side.
const (
	SideController = "controller"
	SideAgent      = "agent"
)

// writerParams make every commit durable before the host is touched
// (synchronous FULL, ADR 0004) and make a write transaction wait for the lock
// instead of failing (immediate transactions plus a busy timeout).
const writerParams = "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)" +
	"&_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)&_txlock=immediate"

const readerParams = "_pragma=synchronous(FULL)&_pragma=foreign_keys(1)" +
	"&_pragma=busy_timeout(5000)&_pragma=query_only(1)"

// DB is one role's database: a single writer connection and a reader pool.
type DB struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens or creates the database at path. A new file is created with mode
// 0600 before SQLite opens it, so the database and its WAL files are private.
func Open(ctx context.Context, path string) (*DB, error) {
	if strings.ContainsAny(path, "?#%") {
		return nil, fmt.Errorf("store: database path %q must not contain ?, # or %%", path)
	}
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_EXCL, 0o600) //nolint:gosec // G304: the path comes from paths.Paths, never from a request
	switch {
	case err == nil:
		if err := f.Close(); err != nil {
			return nil, fmt.Errorf("store: create %s: %w", path, err)
		}
	case errors.Is(err, fs.ErrExist):
	default:
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}

	w, err := sql.Open("sqlite", "file:"+path+"?"+writerParams)
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open writer %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", "file:"+path+"?"+readerParams)
	if err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open reader: %w", err)
	}
	r.SetMaxOpenConns(4)
	if err := r.PingContext(ctx); err != nil {
		_ = w.Close()
		_ = r.Close()
		return nil, fmt.Errorf("store: open reader %s: %w", path, err)
	}
	return &DB{path: path, w: w, r: r}, nil
}

// Path is the database file's path.
func (d *DB) Path() string { return d.path }

// Read returns the reader pool. Statements on it cannot write.
func (d *DB) Read() *sql.DB { return d.r }

// Writer returns the single writer connection, for statements that cannot run
// inside a transaction (VACUUM INTO). Use Write for everything else.
func (d *DB) Writer() *sql.DB { return d.w }

// Write runs fn in an immediate transaction and commits it when fn returns
// nil; otherwise it rolls back and returns fn's error.
func (d *DB) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	// net/http recovers handler panics; without this rollback the only writer
	// connection would stay inside the open transaction and block every Write.
	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()
	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return errors.Join(err, fmt.Errorf("store: rollback: %w", rbErr))
		}
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("store: commit: %w", err)
	}
	return nil
}

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.w.Close(), d.r.Close())
}

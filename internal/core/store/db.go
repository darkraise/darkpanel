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
	"time"

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
// (synchronous FULL, ADR 0004), make a write transaction wait for the lock
// instead of failing (immediate transactions plus the busy timeout, which
// Open appends), and zero what a write deletes, so an erased secret leaves
// no bytes in the file's free space once a checkpoint copies the page back.
const writerParams = "_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)" +
	"&_pragma=foreign_keys(1)&_pragma=secure_delete(ON)&_txlock=immediate"

const readerParams = "_pragma=synchronous(FULL)&_pragma=foreign_keys(1)" +
	"&_pragma=query_only(1)"

// DefaultBusyTimeout is how long a statement waits for a lock unless
// WithBusyTimeout sets another.
const DefaultBusyTimeout = 5 * time.Second

// Option configures Open.
type Option func(*options)

type options struct{ busyTimeout time.Duration }

// WithBusyTimeout sets how long a statement waits for a lock before it
// fails with SQLITE_BUSY, on the writer and the readers.
func WithBusyTimeout(d time.Duration) Option { return func(o *options) { o.busyTimeout = d } }

// DB is one role's database: a single writer connection and a reader pool.
type DB struct {
	path string
	w    *sql.DB
	r    *sql.DB
}

// Open opens or creates the database at path. A new file is created with mode
// 0600, and an existing database and its -wal and -shm files are set to 0600,
// before SQLite opens it, so the database and its WAL files are private. A
// failure to set a mode fails Open.
func Open(ctx context.Context, path string, opts ...Option) (*DB, error) {
	o := options{busyTimeout: DefaultBusyTimeout}
	for _, opt := range opts {
		opt(&o)
	}
	busy := fmt.Sprintf("&_pragma=busy_timeout(%d)", o.busyTimeout.Milliseconds())
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
	// SQLite creates -wal and -shm with the database file's mode, so a database
	// left with a looser mode would pass it on.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && (p == path || !errors.Is(err, fs.ErrNotExist)) {
			return nil, fmt.Errorf("store: set mode of %s: %w", p, err)
		}
	}

	w, err := sql.Open("sqlite", "file:"+path+"?"+writerParams+busy)
	if err != nil {
		return nil, fmt.Errorf("store: open writer: %w", err)
	}
	w.SetMaxOpenConns(1)
	if err := w.PingContext(ctx); err != nil {
		_ = w.Close()
		return nil, fmt.Errorf("store: open writer %s: %w", path, err)
	}
	r, err := sql.Open("sqlite", "file:"+path+"?"+readerParams+busy)
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
// nil; otherwise it rolls back and returns fn's error. It is not re-entrant:
// the writer has one connection, so calling Write or Writer inside fn
// deadlocks, and a query on Read inside fn does not see fn's uncommitted
// writes; use tx for everything inside fn.
func (d *DB) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
	// net/http recovers handler panics, and fn may call runtime.Goexit (as
	// t.FailNow does); without this rollback the only writer connection would
	// stay inside the open transaction and block every Write. After fn's
	// error or a failed commit the transaction is already done and this
	// rollback returns sql.ErrTxDone.
	committed := false
	defer func() {
		if committed {
			return
		}
		p := recover()
		_ = tx.Rollback()
		if p != nil {
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
	committed = true
	return nil
}

// Close closes both pools.
func (d *DB) Close() error {
	return errors.Join(d.w.Close(), d.r.Close())
}

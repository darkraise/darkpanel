package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// MigrationSet is one module's migrations for one side: files named
// NNNN_<name>.sql at the root of FS. Numbers are unique and start at 1; gaps
// are allowed, because each core migration file belongs to a different part
// and the parts merge in any order (spec section 4).
type MigrationSet struct {
	Module string
	FS     fs.FS
}

// MigrateOptions configures one migration run.
type MigrateOptions struct {
	Side          string    // "controller" or "agent"; recorded in core_migrations
	BinaryVersion string    // the running binary; the backup's "to" and core_meta binary_version
	BackupRoot    string    // directory for Backup and PruneBackups
	Keep          int       // backups kept, at least 1; 5 in production
	Now           time.Time // the backup timestamp
}

// Applied is one migration a run applied.
type Applied struct {
	Module string
	Number int
	Name   string
}

// ModuleFailure is a module whose migrations could not be applied or
// verified; the module is unavailable, the rest of the system starts (R9).
type ModuleFailure struct {
	Module string
	Err    error
}

// Result reports what a run did.
type Result struct {
	Applied   []Applied
	BackupDir string // empty when no backup was taken
	Failed    []ModuleFailure
	PruneErr  error // a failed prune of old backups; it runs before any migration, so it can accompany a migration error
}

type migration struct {
	number   int
	name     string
	sql      string
	checksum string
}

var migrationName = regexp.MustCompile(`^(\d{4})_([a-z0-9_]+)\.sql$`)

const createMigrationsTable = `CREATE TABLE IF NOT EXISTS core_migrations (
	module     TEXT    NOT NULL,
	side       TEXT    NOT NULL,
	number     INTEGER NOT NULL,
	name       TEXT    NOT NULL,
	checksum   TEXT    NOT NULL,
	applied_at TEXT    NOT NULL,
	PRIMARY KEY (module, side, number)
)`

// Migrate applies the pending migrations of core and then of each module, in
// order, each migration in its own transaction (ADR 0005). Before applying
// anything it takes a Backup named from core_meta's binary_version ("none" on
// a new database) unless that already equals opts.BinaryVersion; a run with
// nothing pending takes none but still records opts.BinaryVersion. A changed
// checksum or failure in core returns an error and must stop
// startup; the same in a module is reported in Result.Failed and that module's
// remaining migrations are skipped. Recorded migrations numbered above the
// highest embedded one are ignored, so an older binary runs on a newer schema.
// Module sets named like the core or like each other are refused before
// anything runs, because they would share core_migrations rows, and so is a
// Keep below 1. A run that gets as far as deciding on a backup, including one
// with nothing pending, then prunes the backups beyond opts.Keep and reports
// a failure in Result.PruneErr without failing the run: a leftover backup
// costs only disk, and the next start retries the prune.
func Migrate(ctx context.Context, db *DB, opts MigrateOptions, core MigrationSet, modules []MigrationSet) (Result, error) {
	var res Result
	if opts.Keep < 1 {
		return res, fmt.Errorf("store: backups kept = %d, want at least 1", opts.Keep)
	}
	seen := map[string]bool{core.Module: true}
	for _, m := range modules {
		if seen[m.Module] {
			return res, fmt.Errorf("store: module set %q is named twice or takes the core's name", m.Module)
		}
		seen[m.Module] = true
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, createMigrationsTable)
		return err
	}); err != nil {
		return res, fmt.Errorf("store: create core_migrations: %w", err)
	}
	applied, err := loadApplied(ctx, db, opts.Side)
	if err != nil {
		return res, err
	}

	corePending, err := pending(core, applied)
	if err != nil {
		return res, fmt.Errorf("store: core migrations: %w", err)
	}
	modulePending := make([][]migration, len(modules))
	anyPending := len(corePending) > 0
	failed := map[string]bool{}
	for i, m := range modules {
		p, err := pending(m, applied)
		if err != nil {
			res.Failed = append(res.Failed, ModuleFailure{Module: m.Module, Err: err})
			failed[m.Module] = true
			continue
		}
		modulePending[i] = p
		anyPending = anyPending || len(p) > 0
	}
	dbName := filepath.Base(db.Path())
	if !anyPending {
		res.PruneErr = pruneBackups(opts.BackupRoot, dbName, "", opts.BinaryVersion, opts.Keep)
		return res, setBinaryVersion(ctx, db, opts.BinaryVersion)
	}

	from, err := binaryVersion(ctx, db)
	if err != nil {
		return res, err
	}
	// The first run of this binary version backed the database up already.
	// Backing up on every later start would let the five-backup limit rotate
	// out the pre-upgrade copy while a failing module migration keeps the
	// service restarting.
	current := ""
	if from != opts.BinaryVersion {
		dir, err := Backup(ctx, db, opts.BackupRoot, from, opts.BinaryVersion, opts.Now)
		if err != nil {
			return res, err
		}
		res.BackupDir = dir
		current = filepath.Base(dir)
	}
	res.PruneErr = pruneBackups(opts.BackupRoot, dbName, current, opts.BinaryVersion, opts.Keep)

	for _, m := range corePending {
		if err := apply(ctx, db, opts.Side, core.Module, m); err != nil {
			// Record the version even though startup stops: this run's backup is
			// the pre-upgrade copy, and a restart loop must not rotate it out.
			err = fmt.Errorf("store: core migration %04d_%s: %w", m.number, m.name, err)
			return res, errors.Join(err, setBinaryVersion(ctx, db, opts.BinaryVersion))
		}
		res.Applied = append(res.Applied, Applied{core.Module, m.number, m.name})
	}
	for i, set := range modules {
		if failed[set.Module] {
			continue
		}
		for _, m := range modulePending[i] {
			if err := apply(ctx, db, opts.Side, set.Module, m); err != nil {
				res.Failed = append(res.Failed, ModuleFailure{
					Module: set.Module,
					Err:    fmt.Errorf("migration %04d_%s: %w", m.number, m.name, err),
				})
				break
			}
			res.Applied = append(res.Applied, Applied{set.Module, m.number, m.name})
		}
	}

	if err := setBinaryVersion(ctx, db, opts.BinaryVersion); err != nil {
		return res, err
	}
	return res, nil
}

// loadApplied returns module -> number -> checksum for the side.
func loadApplied(ctx context.Context, db *DB, side string) (map[string]map[int]string, error) {
	rows, err := db.Read().QueryContext(ctx,
		"SELECT module, number, checksum FROM core_migrations WHERE side = ?", side)
	if err != nil {
		return nil, fmt.Errorf("store: read core_migrations: %w", err)
	}
	defer func() { _ = rows.Close() }()
	applied := map[string]map[int]string{}
	for rows.Next() {
		var module, checksum string
		var number int
		if err := rows.Scan(&module, &number, &checksum); err != nil {
			return nil, fmt.Errorf("store: read core_migrations: %w", err)
		}
		if applied[module] == nil {
			applied[module] = map[int]string{}
		}
		applied[module][number] = checksum
	}
	return applied, rows.Err()
}

// pending loads set, verifies the checksums of migrations already applied and
// returns the rest in order. A pending migration numbered below one already
// applied is refused: migrations run in number order on every database.
func pending(set MigrationSet, applied map[string]map[int]string) ([]migration, error) {
	all, err := load(set.FS)
	if err != nil {
		return nil, err
	}
	highest := 0
	for number := range applied[set.Module] {
		highest = max(highest, number)
	}
	var out []migration
	for _, m := range all {
		sum, ok := applied[set.Module][m.number]
		if !ok {
			if m.number < highest {
				return nil, fmt.Errorf("migration %04d_%s is numbered below the applied migration %04d; migrations must run in order",
					m.number, m.name, highest)
			}
			out = append(out, m)
			continue
		}
		if sum != m.checksum {
			return nil, fmt.Errorf("migration %04d_%s was changed after it was applied (checksum %s, recorded %s)",
				m.number, m.name, m.checksum, sum)
		}
	}
	return out, nil
}

func load(fsys fs.FS) ([]migration, error) {
	if fsys == nil {
		return nil, nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := migrationName.FindStringSubmatch(e.Name())
		if m == nil {
			return nil, fmt.Errorf("migration file %q does not match NNNN_<name>.sql", e.Name())
		}
		body, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		number, _ := strconv.Atoi(m[1])
		sum := sha256.Sum256(body)
		out = append(out, migration{number, m[2], string(body), hex.EncodeToString(sum[:])})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].number < out[j].number })
	for i, m := range out {
		if m.number == 0 {
			return nil, fmt.Errorf("migration %04d_%s: numbers start at 0001", m.number, m.name)
		}
		if i > 0 && out[i-1].number == m.number {
			return nil, fmt.Errorf("migrations %04d_%s and %04d_%s share a number", m.number, out[i-1].name, m.number, m.name)
		}
	}
	return out, nil
}

func apply(ctx context.Context, db *DB, side, module string, m migration) error {
	return db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, m.sql); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_migrations (module, side, number, name, checksum, applied_at)
			 VALUES (?, ?, ?, ?, ?, strftime('%Y-%m-%dT%H:%M:%SZ', 'now'))`,
			module, side, m.number, m.name, m.checksum)
		return err
	})
}

// binaryVersion reads core_meta's binary_version, or "none" when the table or
// the key does not exist yet.
func binaryVersion(ctx context.Context, db *DB) (string, error) {
	var v string
	err := db.Read().QueryRowContext(ctx,
		"SELECT value FROM core_meta WHERE key = 'binary_version'").Scan(&v)
	switch {
	case err == nil:
		return v, nil
	case errors.Is(err, sql.ErrNoRows), isNoSuchTable(err):
		return "none", nil
	default:
		return "", fmt.Errorf("store: read binary_version: %w", err)
	}
}

// setBinaryVersion records the running binary at the end of every run, and
// after a failed core migration so a restart loop keeps the pre-upgrade backup;
// it is a no-op until the core migration that creates core_meta has run.
func setBinaryVersion(ctx context.Context, db *DB, version string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES ('binary_version', ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, version)
		return err
	})
	if err != nil && !isNoSuchTable(err) {
		return fmt.Errorf("store: write binary_version: %w", err)
	}
	return nil
}

// SchemaVersion is the highest applied core migration number of side, or 0
// before any has run. /healthz reports it for the controller database.
func SchemaVersion(ctx context.Context, db *DB, side string) (int, error) {
	var n sql.NullInt64
	err := db.Read().QueryRowContext(ctx,
		"SELECT max(number) FROM core_migrations WHERE module = 'core' AND side = ?", side).Scan(&n)
	switch {
	case err == nil:
		return int(n.Int64), nil
	case isNoSuchTable(err):
		return 0, nil
	default:
		return 0, fmt.Errorf("store: read schema version: %w", err)
	}
}

func isNoSuchTable(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

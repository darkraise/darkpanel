## A1 — Task 15
Reason: Core-failure restart loops still rotate out the pre-upgrade backup the same-version ruling protects.
Cost if wrong: A later backup's <from> label names a version whose core migrations failed.
### Old
````text
	for _, m := range corePending {
		if err := apply(ctx, db, opts.Side, core.Module, m); err != nil {
			return res, fmt.Errorf("store: core migration %04d_%s: %w", m.number, m.name, err)
		}
````
### New
````text
	for _, m := range corePending {
		if err := apply(ctx, db, opts.Side, core.Module, m); err != nil {
			// Record the version even though startup stops: this run's backup is
			// the pre-upgrade copy, and a restart loop must not rotate it out.
			err = fmt.Errorf("store: core migration %04d_%s: %w", m.number, m.name, err)
			return res, errors.Join(err, setBinaryVersion(ctx, db, opts.BinaryVersion))
		}
````

## A2 — Task 15
Reason: Nothing tests the rule that a failing core migration must not repeat the backup.
Cost if wrong: One redundant test.
### Old
````text
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}
````
### New
````text
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}

func TestMigrateCoreFailureBacksUpOnce(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	core.FS = sqlFS(map[string]string{"0001_core.sql": metaSQL, "0002_bad.sql": "NOT SQL;"})
	opts.BinaryVersion = "v2"
	for range 6 {
		opts.Now = opts.Now.Add(time.Minute)
		if _, err := Migrate(ctx, db, opts, core, nil); err == nil {
			t.Fatal("Migrate succeeded with a broken core migration")
		}
	}
	entries, err := os.ReadDir(opts.BackupRoot)
	if err != nil || len(entries) != 2 || entries[0].Name() != "none-v1-20261017T010000Z" || entries[1].Name() != "v1-v2-20261017T010100Z" {
		t.Errorf("backups = %v, %v; want the first run's backup and one pre-upgrade backup", entries, err)
	}
}
````

## A3 — Task 2
Reason: SQLite reads `%HH` in the path as an escape, so it opens a different file from the one `Open` created with mode 0600.
Cost if wrong: Paths containing `%` are refused where escaping would have accepted them.
### Old
````text
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("store: database path %q must not contain ? or #", path)
````
### New
````text
	if strings.ContainsAny(path, "?#%") {
		return nil, fmt.Errorf("store: database path %q must not contain ?, # or %%", path)
````

## A4 — Task 2
Reason: Test every refused character, including the new `%`.
Cost if wrong: Two redundant test cases.
### Old
````text
func TestOpenRefusesURICharacters(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("Open accepted a path containing '?'")
	}
}
````
### New
````text
func TestOpenRefusesURICharacters(t *testing.T) {
	for _, name := range []string{"a?b.db", "a#b.db", "a%41.db"} {
		if _, err := Open(context.Background(), filepath.Join(t.TempDir(), name)); err == nil {
			t.Errorf("Open accepted %q", name)
		}
	}
}
````

## A5 — Task 2
Reason: A panic recovered higher up would leave the only writer connection stuck in an open transaction.
Cost if wrong: One deferred function on every write.
### Old
````text
func (d *DB) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := d.w.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("store: begin: %w", err)
	}
````
### New
````text
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
````

## A6 — Task 2
Reason: Nothing tests that a panic in `fn` frees the writer connection.
Cost if wrong: One redundant test.
### Old
````text
	if n != 1 {
		t.Errorf("rows = %d, want 1 (the second insert must roll back)", n)
	}
}
````
### New
````text
	if n != 1 {
		t.Errorf("rows = %d, want 1 (the second insert must roll back)", n)
	}
}

func TestWriteRollsBackOnPanic(t *testing.T) {
	db := openTemp(t)
	var p any
	func() {
		defer func() { p = recover() }()
		_ = db.Write(context.Background(), func(*sql.Tx) error { panic("boom") })
	}()
	if p == nil {
		t.Fatal("Write swallowed the panic")
	}
	if n := db.Writer().Stats().InUse; n != 0 {
		t.Errorf("writer connections in use after a panic = %d, want 0", n)
	}
}
````

## A7 — Header
Reason: `Open` now also refuses `%`, which SQLite would read as an escape in a URI filename.
Cost if wrong: The contract lists a refused character the code does not refuse.
### Old
````text
- Task 2 (`db.go`): `const SideController = "controller"`, `const SideAgent = "agent"`; `func Open(ctx context.Context, path string) (*DB, error)` creates a missing file with mode 0600 and refuses a path containing `?` or `#`; methods `(*DB) Path() string`, `Read() *sql.DB` (query-only pool), `Writer() *sql.DB` (the single writer connection, for `VACUUM INTO`), `Write(ctx context.Context, fn func(*sql.Tx) error) error` (immediate transaction), `Close() error`.
````
### New
````text
- Task 2 (`db.go`): `const SideController = "controller"`, `const SideAgent = "agent"`; `func Open(ctx context.Context, path string) (*DB, error)` creates a missing file with mode 0600 and refuses a path containing `?`, `#` or `%`; methods `(*DB) Path() string`, `Read() *sql.DB` (query-only pool), `Writer() *sql.DB` (the single writer connection, for `VACUUM INTO`), `Write(ctx context.Context, fn func(*sql.Tx) error) error` (immediate transaction), `Close() error`.
````

## A8 — Task 6
Reason: An unknown required scope ranks 0, so every valid scope passes `Allows`; authorization must fail closed.
Cost if wrong: A caller that passes an empty scope to mean "no scope needed" gets a 403 that a test would show.
### Old
````text
	return rank[s] > 0 && rank[s] >= rank[need]
````
### New
````text
	// An unknown scope on either side denies: a mistyped required scope must fail closed.
	return rank[s] > 0 && rank[need] > 0 && rank[s] >= rank[need]
````

## A9 — Task 6
Reason: No test checks an invalid required scope.
Cost if wrong: Two redundant test rows.
### Old
````text
		{Scope(""), ScopeRead, false},
		{Scope("root"), ScopeRead, false},
````
### New
````text
		{Scope(""), ScopeRead, false},
		{Scope("root"), ScopeRead, false},
		{ScopeAdmin, Scope(""), false},
		{ScopeRead, Scope("root"), false},
````

## A10 — Task 9
Reason: `decode` needs `errors` and `io` for the end-of-input check.
Cost if wrong: Two unused imports fail the build, which would be caught at once.
### Old
````text
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
````
### New
````text
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
````

## A11 — Task 9
Reason: `dec.More()` misses a trailing `}` or `]`; only an `io.EOF` from a second Decode proves the input ended.
Cost if wrong: One extra Decode call per handler input.
### Old
````text
	if dec.More() {
		return v, errcode.New(errcode.InvalidInput, "trailing data after the JSON value")
	}
````
### New
````text
	// More reports false before a stray '}' or ']', so only io.EOF proves the input ended.
	if !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
		return v, errcode.New(errcode.InvalidInput, "trailing data after the JSON value")
	}
````

## A12 — Task 9
Reason: The only trailing-data test uses `{} {}`, which `More` already catches.
Cost if wrong: Two redundant test cases.
### Old
````text
	_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"a"} {}`))
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("trailing data: err = %v, want %s", err, errcode.InvalidInput)
	}
````
### New
````text
	for _, raw := range []string{`{"name":"a"} {}`, `{"name":"a"}}`, `{"name":"a"}]`} {
		_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(raw))
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
			t.Errorf("trailing data %q: err = %v, want %s", raw, err, errcode.InvalidInput)
		}
	}
````

## A13 — Task 10
Reason: The validator needs `errors` and `io` for the end-of-input check.
Cost if wrong: Two unused imports fail the build, which would be caught at once.
### Old
````text
	"bytes"
	"encoding/json"
	"reflect"
````
### New
````text
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"reflect"
````

## A14 — Task 10
Reason: `dec.More()` misses a trailing `}` or `]`; only an `io.EOF` from a second Decode proves the input ended.
Cost if wrong: One extra Decode call per validation.
### Old
````text
	if dec.More() {
		return invalid("trailing data after the JSON value")
	}
````
### New
````text
	// More reports false before a stray '}' or ']', so only io.EOF proves the input ended.
	if !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
		return invalid("trailing data after the JSON value")
	}
````

## A15 — Task 10
Reason: The only trailing-data case, `{} {}`, is one that `More` already catches.
Cost if wrong: Two redundant test cases.
### Old
````text
	for _, raw := range []string{``, `{"name":"a","extra":1}`, `{"count":"one"}`, `{} {}`} {
````
### New
````text
	for _, raw := range []string{``, `{"name":"a","extra":1}`, `{"count":"one"}`, `{} {}`, `{}}`, `{} ]`} {
````

## A16 — Task 14
Reason: The cleanup on a failed copy joins errors with `errors.Join`.
Cost if wrong: An unused import fails the build, which would be caught at once.
### Old
````text
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
````
### New
````text
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
````

## A17 — Task 14
Reason: A failed copy left a directory whose name retention counts as a kept backup.
Cost if wrong: One extra RemoveAll on a path that already failed.
### Old
````text
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("store: VACUUM INTO %s: %w", target, err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", fmt.Errorf("store: backup mode: %w", err)
	}
````
### New
````text
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		// A failed copy must not leave a directory that retention counts as a backup.
		return "", errors.Join(fmt.Errorf("store: VACUUM INTO %s: %w", target, err), os.RemoveAll(dir))
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", errors.Join(fmt.Errorf("store: backup mode: %w", err), os.RemoveAll(dir))
	}
````

## A18 — Task 14
Reason: No test checks that a failed copy leaves no backup directory.
Cost if wrong: One redundant test.
### Old
````text
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err == nil {
		t.Fatal("second backup with the same name succeeded, want an error")
	}
}
````
### New
````text
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err == nil {
		t.Fatal("second backup with the same name succeeded, want an error")
	}
}

func TestBackupFailureLeavesNoDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Backup(ctx, db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), 5); err == nil {
		t.Fatal("Backup succeeded with a cancelled context")
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 0 {
		t.Errorf("entries after a failed backup = %v, %v; want none", entries, err)
	}
}
````

## A19 — Task 14
Reason: The doc comment must describe the new retention rule and the cleanup after a failed copy.
Cost if wrong: The comment misdescribes the code.
### Old
````text
// name> (directory 0700, file 0600) and then deletes all but the newest keep
// backup directories under root. Characters other than letters, digits, dot
````
### New
````text
// name> (directory 0700, file 0600) and then deletes backup directories under
// root beyond keep, counting the new one first whatever its timestamp. A failed
// copy removes its directory. Characters other than letters, digits, dot
````

## A20 — Task 14
Reason: Pruning must know which directory is the new one.
Cost if wrong: The build fails, which would be caught at once.
### Old
````text
	if err := pruneBackups(root, keep); err != nil {
````
### New
````text
	if err := pruneBackups(root, name, keep); err != nil {
````

## A21 — Task 14
Reason: Pruning takes the new directory's name so it never deletes it.
Cost if wrong: The build fails, which would be caught at once.
### Old
````text
// pruneBackups deletes the oldest backup directories beyond keep. It only
// considers directories whose names Backup produces.
func pruneBackups(root string, keep int) error {
````
### New
````text
// pruneBackups deletes the oldest backup directories beyond keep, never the
// one named current. It only considers directories whose names Backup produces.
func pruneBackups(root, current string, keep int) error {
````

## A22 — Task 14
Reason: After the clock moves back, the new backup can rank among the oldest and be deleted.
Cost if wrong: One string comparison per directory.
### Old
````text
		if !e.IsDir() || m == nil {
			continue
		}
````
### New
````text
		// current is excluded so a clock set back cannot rank it among the oldest.
		if !e.IsDir() || m == nil || e.Name() == current {
			continue
		}
````

## A23 — Task 14
Reason: The new directory is excluded from the list, so only keep-1 others are kept.
Cost if wrong: Retention keeps keep+1 directories.
### Old
````text
	for _, b := range backups[min(keep, len(backups)):] {
````
### New
````text
	for _, b := range backups[min(keep-1, len(backups)):] {
````

## A24 — Task 14
Reason: No test covers a backup taken at a time earlier than the existing ones.
Cost if wrong: One redundant test.
### Old
````text
	if err != nil || len(entries) != 0 {
		t.Errorf("entries after a failed backup = %v, %v; want none", entries, err)
	}
}
````
### New
````text
	if err != nil || len(entries) != 0 {
		t.Errorf("entries after a failed backup = %v, %v; want none", entries, err)
	}
}

func TestBackupKeepsNewDirectoryWhenClockGoesBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	later := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, err := Backup(ctx, db, root, "a", "b", later.Add(time.Duration(i)*time.Minute), 5); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	dir, err := Backup(ctx, db, root, "a", "b", later.Add(-time.Hour), 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test.db")); err != nil {
		t.Errorf("new backup: %v", err)
	}
	entries, err := os.ReadDir(root)
	if err != nil || len(entries) != 5 {
		t.Errorf("entries = %v, %v; want 5", entries, err)
	}
	if _, err := os.Stat(filepath.Join(root, "a-b-20261018T000000Z")); !os.IsNotExist(err) {
		t.Errorf("oldest of the later backups survived: %v", err)
	}
}
````

## A25 — Task 14
Reason: Pruning stopped at its first error, so each retried run added a full copy that was never removed.
Cost if wrong: A few extra RemoveAll calls after one fails.
### Old
````text
	for _, b := range backups[min(keep-1, len(backups)):] {
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			return fmt.Errorf("store: prune backup %s: %w", b.name, err)
		}
	}
	return nil
}
````
### New
````text
	var errs []error
	for _, b := range backups[min(keep-1, len(backups)):] {
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			// Keep going: stopping here would let every retried run add a copy.
			errs = append(errs, fmt.Errorf("store: prune backup %s: %w", b.name, err))
		}
	}
	return errors.Join(errs...)
}
````

## A26 — Header
Reason: The Task 14 contract must state that the new directory always survives and a failed copy leaves none.
Cost if wrong: The contract describes retention the code does not implement.
### Old
````text
- Task 14 (`backup.go`): `func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time, keep int) (string, error)` writes `root/<from>-<to>-<YYYYMMDDTHHMMSSZ>/<database file name>` (directory 0700, file 0600; characters outside `[A-Za-z0-9._]` in `from` and `to` become `_`), keeps the newest `keep` such directories and returns the new one.
````
### New
````text
- Task 14 (`backup.go`): `func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time, keep int) (string, error)` writes `root/<from>-<to>-<YYYYMMDDTHHMMSSZ>/<database file name>` (directory 0700, file 0600; characters outside `[A-Za-z0-9._]` in `from` and `to` become `_`), keeps the new directory and the newest `keep`-1 others whatever the clock says, removes its directory when the copy fails, and returns the new one.
````

## A27 — Task 15
Reason: Spec section 4 writes binary_version after every successful run, including one with nothing pending.
Cost if wrong: A same-version run whose migration sets changed would skip its backup.
### Old
````text
	if !anyPending {
		return res, nil
	}
````
### New
````text
	if !anyPending {
		return res, setBinaryVersion(ctx, db, opts.BinaryVersion)
	}
````

## A28 — Task 15
Reason: Migrate's doc comment must say a run with nothing pending still records the version.
Cost if wrong: A misleading comment only.
### Old
````text
// nothing pending takes none. A changed checksum or failure in core returns an error and must stop
````
### New
````text
// nothing pending takes none but still records opts.BinaryVersion. A changed
// checksum or failure in core returns an error and must stop
````

## A29 — Task 15
Reason: setBinaryVersion's comment says "after a successful run", but A1 also writes the version after a failed core migration.
Cost if wrong: A misleading comment only.
### Old
````text
// setBinaryVersion records the running binary after a successful run; it is a
// no-op until the core migration that creates core_meta has run.
````
### New
````text
// setBinaryVersion records the running binary at the end of every run, and
// after a failed core migration so a restart loop keeps the pre-upgrade backup;
// it is a no-op until the core migration that creates core_meta has run.
````

## A30 — Task 15
Reason: No test shows that a run with nothing pending keeps the next backup's "from" name current.
Cost if wrong: One redundant test.
### Old
````text
		t.Errorf("backup file: %v", err)
	}
}
````
### New
````text
		t.Errorf("backup file: %v", err)
	}
}

func TestMigrateRecordsVersionWhenNothingPending(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	opts.BinaryVersion = "v2"
	if res, err := Migrate(ctx, db, opts, core, nil); err != nil || res.BackupDir != "" {
		t.Fatalf("v2 run = %+v, %v; want success and no backup", res, err)
	}
	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})
	opts.BinaryVersion = "v3"
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.BackupRoot, "v2-v3-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
}
````

## A31 — Task 15
Reason: No test shows that a migration file runs every statement, or that a failed one leaves no table and no row.
Cost if wrong: One redundant test.
### Old
````text
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}
````
### New
````text
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}

func TestMigrateRunsWholeFileAtomically(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"multi", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE multi_a (id TEXT);\nCREATE TABLE multi_b (id TEXT);",
		})},
		{"partial", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE partial_a (id TEXT);\nNOT SQL;",
		})},
	}
	res, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if !tableExists(t, db, "multi_a") || !tableExists(t, db, "multi_b") {
		t.Error("multi: want every statement of 0001 applied")
	}
	if len(res.Failed) != 1 || res.Failed[0].Module != "partial" {
		t.Errorf("failures = %+v, want partial", res.Failed)
	}
	if tableExists(t, db, "partial_a") {
		t.Error("partial: the failed migration's first statement was kept")
	}
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM core_migrations WHERE module = 'partial'").Scan(&n); err != nil || n != 0 {
		t.Errorf("partial rows = %d, %v; want 0", n, err)
	}
}
````

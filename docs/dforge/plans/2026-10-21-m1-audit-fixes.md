# M1 Audit Fixes Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Fix every item of the ten M1 register rows the 2026-10-21 milestone audit reopened (platform-core rows 33, 38, 45, 59, 95, 113-117), following the binding review rulings.

**Architecture:** Each task is a small, test-first fix inside one package or page: the SQLite store, the lifecycle engine, the sealer, backup pruning, the dispatcher, the audit ingester and host list, the API middleware, the Makefile and the web session, shell and pages. The only shared additions are a fake-clock helper, a non-fatal prune result, an ingestion status table read by the host list, and a session generation counter, all fixed in Contracts.

**Tech Stack:** Go 1.27.3 (standard library, `modernc.org/sqlite`, `github.com/oklog/ulid/v2`), React 19 with react-router, TanStack Query and darkraise-ui, vitest and Testing Library, GNU make.

**Spec:** docs/dforge/specs/2026-10-21-m1-audit-fixes-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves

**Plan review:** 2026-10-21 — dr-superpowers:judge-opus — executability 16 / coherence 18 / coverage 18 / assumptions 15 (round 1)

**Parallelism:** waves — 1 | 2 | 3 | 4-6 | 7-9 | 10-12 | 13 | 14-16 | 17-19 | 20 | 21

**Integration check:** `timeout 2400 make lint test build verify-build`

**Worktree setup:** `timeout 600 pnpm -C web install --frozen-lockfile`

## Global Constraints

- Go 1.27.3 builds everything; the Makefile exports `GOTOOLCHAIN=local` (ADR 0001, ADR 0013), and the host's `go` is 1.27.3. Never change `go.mod` or `go.sum`.
- Every test, build and network command runs under `timeout`, as each step shows.
- Tests stay apart at run time: Go tests use `t.TempDir()`, in-memory fixtures and `httptest` servers on ephemeral ports; web tests run in jsdom. No task uses a fixed port, path or shared database.
- Commits: `<type>(<scope>): <subject>`, type one of feat, fix, docs, style, refactor, test, chore, perf; subject imperative, at most 50 characters, no period. No attribution lines.
- Comments only where the reason is not obvious from the code; never mention a task, an item number, the audit or the review.
- English only, in code, comments, messages and commits.
- Accepted ADRs (`docs/adr/0001`-`0011`) are never edited.
- Output JSON schemas change only in Task 14, and only through `make schemas`.

## Contracts

- **`coretest.FakeClock.StepBack`** (Task 4 produces, Task 7 consumes): `func (c *FakeClock) StepBack(d time.Duration)` in `internal/core/coretest/clock.go` moves `now` back by `d` and runs no timer.
- **`store.Result.PruneErr`** (Task 3 produces, Task 8 consumes): `Result` in `internal/core/store/migrate.go` gains `PruneErr error`, a failed prune of old backups after a migration that succeeded. `store.Backup` becomes `Backup(ctx context.Context, db *DB, root, from, to string, now time.Time) (string, error)` and fails only on a copy or chmod failure. `store.PruneBackups(root, dbName string, keep int) error` prunes on its own. `Migrate` refuses `Keep < 1` before doing anything, prunes on every run (sparing the backup it just took), and stores a prune failure in `Result.PruneErr` instead of returning it. The controller server, the agent runtime and the installer report a non-nil `PruneErr` (`Warn` log, or a printed warning in the installer).
- **Ingestion status table** (Task 13 produces, Task 14 consumes): migration `internal/core/store/migrations/controller/0008_ingest_status.sql`:
  ```sql
  CREATE TABLE core_ingest_status (
      host_id TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
      since TEXT NOT NULL,
      error TEXT
  ) STRICT;
  ```
  `since` is RFC 3339 UTC with nanoseconds, the time the current state began; `error` is NULL while ingestion succeeds. The Ingester writes a row only when a host's state changes, for scheduled and dispatcher-triggered ingestion alike. `audit.Ingester.IngestHost` returns nil for a per-host failure (the Ingester reports it), and `IngestAll` returns only a failure to list the hosts.
- **`core.Host.Ingestion`** (Task 14 produces, Task 17 consumes): in `internal/core/fleet.go`
  ```go
  Ingestion *IngestionStatus `json:"ingestion" jsonschema:"nullable" jsonschema_description:"Journal ingestion health; null until the controller first ingests the host's journal."`

  // IngestionStatus is a host's journal ingestion health.
  type IngestionStatus struct {
      OK    bool      `json:"ok" jsonschema_description:"Whether the last ingestion succeeded."`
      Since time.Time `json:"since" jsonschema_description:"When the current state began."`
      Error *string   `json:"error" jsonschema:"nullable" jsonschema_description:"The last ingestion error; null while ingestion succeeds."`
  }
  ```
  The web type in `web/src/api/types.ts` mirrors it as `ingestion: { ok: boolean; since: string; error: string | null } | null`.
- **`sessionGeneration`** (Task 20 produces, Task 21 consumes): `export function sessionGeneration(): number` in `web/src/api/client.ts` returns a counter that every `setCsrfToken` call increments. A request or session probe records it when it starts and acts on a 401 or a probe answer only if it is unchanged.

## Assumptions (evidence)

- Only `internal/core/store/migrate.go:137` calls `store.Backup` (`rg -n 'Backup\(ctx' --type go`, 2026-10-21), so changing its parameters touches no other package.
- The drafted code of every task was applied to a scratch copy of HEAD 60f10bb on 2026-10-21; each new test failed as its step says (or passed, where the step says coverage only), and with all tasks applied `go test ./...`, golangci-lint (`0 issues.`), vitest, `tsc --noEmit` and ESLint passed.
- The audit store's ids are stamped from each entry's own `At` (`internal/controller/audit/audit.go:69-75`), so they follow event time by design; no clamp is applied there (a clamp would list a late-ingested agent entry above newer ones).
- react-router 8.4.0's `NavLink` sets `aria-current` from its own match and never matches below `/` (read in `web/node_modules/react-router`, 2026-10-21); Task 16 therefore renders a plain `Link` for a prefix-table match.
- SQLite's unix VFS gives `-wal` and `-shm` the database file's mode when it creates them (`findCreateFileMode` in SQLite's `os_unix.c` copies the database file's mode), so Task 1's chmod before `sql.Open` covers files created later; Task 1's extended `TestOpenSetsPragmasAndMode` checks both files are 0600 after a write.
- No executor lane: `codex-gate` printed `lane=false reason=plugin-not-enabled` on 2026-10-21.

## Task index

1. SQLite store transaction and file modes
2. Sealer guards
3. Backup pruning safety
4. Fake clock step back
5. Redact schema cache
6. Makefile dependency stamp and host-arch check
7. Lifecycle ID ordering and plan refusals
8. Report backup prune failures
9. Dispatcher Hello, noChange and Apply checks
10. Audit journal sequence check
11. API panic headers and listeners HEAD
12. Audit page secret test
13. Audit ingestion health
14. Host list ingestion health
15. Token dialog field errors and blank names
16. Hosts sidebar highlight
17. Hosts page ingestion badge
18. Test hygiene review: core and controller packages
19. Test hygiene review: CLI, agent runtime and server sites
20. Session generation
21. Logout redirect and role label

---

### Task 1: SQLite store transaction and file modes

**Files:**
- Modify: `internal/core/store/db.go` (`Open`, `Write`)
- Test: `internal/core/store/db_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 33

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 3 = 4

- [ ] **Step 1: Write the failing test for a `runtime.Goexit` inside `Write`**

In `internal/core/store/db_test.go`, add `"runtime"` to the imports so the block reads:

```go
import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)
```

Add this test directly after `TestWriteRollsBackOnPanic`:

```go
func TestWriteRollsBackOnGoexit(t *testing.T) {
	db := openTemp(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = db.Write(context.Background(), func(*sql.Tx) error {
			runtime.Goexit()
			return nil
		})
	}()
	<-done
	if n := db.Writer().Stats().InUse; n != 0 {
		t.Errorf("writer connections in use after runtime.Goexit = %d, want 0", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/store/ -run '^TestWriteRollsBackOnGoexit$' -count=1`
Expected: FAIL with `writer connections in use after runtime.Goexit = 1, want 0`

- [ ] **Step 3: Write the implementation**

In `internal/core/store/db.go`, replace the body of `Write` from the comment through the end of the function. Old:

```go
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
```

New:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/store/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/store`)

- [ ] **Step 5: Write the coverage tests for the WAL file modes and the reader pool**

These pin behaviour the code already has. They pass as soon as they are written, and no implementation step follows them.

In `internal/core/store/db_test.go`, in `TestOpenSetsPragmasAndMode`, replace the final mode check. Old:

```go
	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("database mode = %o, want 600", mode)
	}
}
```

New:

```go
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER)")
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	for _, path := range []string{db.Path(), db.Path() + "-wal", db.Path() + "-shm"} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(path), mode)
		}
	}
}

func TestReaderPoolSetsPragmasOnEveryConnection(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	// Holding four leases at once makes the pool open its every connection.
	conns := make([]*sql.Conn, 4)
	for i := range conns {
		c, err := db.Read().Conn(ctx)
		if err != nil {
			t.Fatalf("reader connection %d: %v", i, err)
		}
		t.Cleanup(func() { _ = c.Close() })
		conns[i] = c
	}
	pragmas := map[string]string{
		"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000", "query_only": "1",
	}
	for i, c := range conns {
		for name, want := range pragmas {
			var got string
			if err := c.QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
				t.Fatalf("reader %d PRAGMA %s: %v", i, name, err)
			}
			if got != want {
				t.Errorf("reader %d PRAGMA %s = %s, want %s", i, name, got, want)
			}
		}
	}
}

func TestReadDuringWriteSeesLastCommit(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (1)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO t VALUES (2)"); err != nil {
			return err
		}
		var n int
		if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("rows a reader sees during the write = %d, want 1 (the last commit)", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, "SELECT count(*) FROM t").Scan(&n); err != nil || n != 2 {
		t.Errorf("rows after the commit = %d, %v; want 2", n, err)
	}
}
```

- [ ] **Step 6: Run the coverage tests**

Run: `timeout 300 go test ./internal/core/store/ -run '^(TestOpenSetsPragmasAndMode|TestReaderPoolSetsPragmasOnEveryConnection|TestReadDuringWriteSeesLastCommit)$' -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/store`): the code already does this, and the tests pin it.

- [ ] **Step 7: Write the failing test for an existing database's looser modes**

In `internal/core/store/db_test.go`, add this test directly after `TestOpenSetsPragmasAndMode`:

```go
func TestOpenTightensExistingModes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")
	files := []string{path, path + "-wal", path + "-shm"}
	for _, p := range files {
		if err := os.WriteFile(p, nil, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, 0o644); err != nil { //nolint:gosec // G302: the loose mode Open must tighten
			t.Fatal(err)
		}
	}
	db, err := Open(context.Background(), path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	for _, p := range files {
		info, err := os.Stat(p)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %o, want 600", filepath.Base(p), mode)
		}
	}
}
```

- [ ] **Step 8: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/store/ -run '^TestOpenTightensExistingModes$' -count=1`
Expected: FAIL with `test.db mode = 644, want 600` (and the same for `test.db-wal` and `test.db-shm`)

- [ ] **Step 9: Write the implementation**

In `internal/core/store/db.go`, replace the doc comment of `Open`. Old:

```go
// Open opens or creates the database at path. A new file is created with mode
// 0600 before SQLite opens it, so the database and its WAL files are private.
```

New:

```go
// Open opens or creates the database at path. A new file is created with mode
// 0600, and an existing database and its -wal and -shm files are set to 0600,
// before SQLite opens it, so the database and its WAL files are private. A
// failure to set a mode fails Open.
```

In `Open`, insert the chmod loop between the create `switch` and the writer's `sql.Open`. Old:

```go
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}

	w, err := sql.Open("sqlite", "file:"+path+"?"+writerParams)
```

New:

```go
		return nil, fmt.Errorf("store: create %s: %w", path, err)
	}
	// SQLite creates -wal and -shm with the database file's mode, so a database
	// left with a looser mode would pass it on.
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && (p == path || !errors.Is(err, fs.ErrNotExist)) {
			return nil, fmt.Errorf("store: set mode of %s: %w", p, err)
		}
	}

	w, err := sql.Open("sqlite", "file:"+path+"?"+writerParams)
```

(`errors`, `fs` and `os` are already imported in `db.go`.)

- [ ] **Step 10: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/store/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/store`)

Then run: `timeout 600 go test ./... -count=1`. Every package opens its databases through `Open`.
Expected: PASS for every package.

Then run: `timeout 600 golangci-lint run ./internal/core/store/...`
Expected: `0 issues.`

- [ ] **Step 11: Commit**

```bash
git add internal/core/store/db.go internal/core/store/db_test.go
git commit -m "fix(store): tighten Write rollback and file modes"
```

### Task 2: Sealer guards

**Files:**
- Modify: `internal/core/secret/sealer.go` (new `errNotInitialised`, `Seal`, `Open`, `Derive`)
- Test: `internal/core/secret/sealer_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 59

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 3 = 4

- [ ] **Step 1: Write the tests**

In `internal/core/secret/sealer_test.go`, add `"fmt"` and `"strings"` to the imports so the block reads:

```go
import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)
```

Insert these three tests directly before `TestNewSealerRefusesKeyLength`. The first one is coverage for the AEAD tag check and passes on the current code. The other two fail on the current code.

```go
// TestSealerOpenChecksTheTag reaches the AEAD check with the wrong key: a
// sealer of key 2 that carries key 1's id passes the key id check.
func TestSealerOpenChecksTheTag(t *testing.T) {
	ct, err := newTestSealer(t, 1).Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	other := newTestSealer(t, 2)
	other.keyID = newTestSealer(t, 1).keyID
	if _, err := other.Open("ad", ct); err == nil || !strings.HasPrefix(err.Error(), "secret: open:") {
		t.Errorf("Open with another key under the same id = %v, want a secret: open error", err)
	}
}

func TestUninitialisedSealerRefuses(t *testing.T) {
	ct, err := newTestSealer(t, 1).Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	for name, s := range map[string]*Sealer{"nil": nil, "zero value": {}} {
		if _, err := s.Seal("ad", []byte("x")); err == nil || err.Error() != "secret: sealer not initialised" {
			t.Errorf("%s: Seal = %v, want secret: sealer not initialised", name, err)
		}
		if _, err := s.Open("ad", ct); err == nil || err.Error() != "secret: sealer not initialised" {
			t.Errorf("%s: Open = %v, want secret: sealer not initialised", name, err)
		}
	}
}

func TestUninitialisedSealerDeriveFailsLoudly(t *testing.T) {
	for name, s := range map[string]*Sealer{"nil": nil, "zero value": {}} {
		func() {
			defer func() {
				if p := recover(); fmt.Sprint(p) != "secret: sealer not initialised" {
					t.Errorf("%s: Derive panicked with %v, want secret: sealer not initialised", name, p)
				}
			}()
			s.Derive(CSRFLabel)
		}()
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 go test ./internal/core/secret/ -run '^TestSealerOpenChecksTheTag$' -count=1`
Expected: PASS (coverage of existing behaviour)

Run: `timeout 300 go test ./internal/core/secret/ -run '^TestUninitialisedSealerRefuses$' -count=1`
Expected: FAIL with `panic: runtime error: invalid memory address or nil pointer dereference`

Run: `timeout 300 go test ./internal/core/secret/ -run '^TestUninitialisedSealerDeriveFailsLoudly$' -count=1`
Expected: FAIL with `nil: Derive panicked with runtime error: invalid memory address or nil pointer dereference, want secret: sealer not initialised` and `zero value: Derive panicked with <nil>, want secret: sealer not initialised`

- [ ] **Step 3: Write the implementation**

In `internal/core/secret/sealer.go`, after the `const` block that ends with `headerSize  = 1 + keyIDSize`, add:

```go

// errNotInitialised is the error of a nil or zero-value Sealer, which has no
// key: only NewSealer makes a usable one.
var errNotInitialised = errors.New("secret: sealer not initialised")
```

At the top of `Seal`, old:

```go
func (s *Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
```

New:

```go
func (s *Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, errNotInitialised
	}
```

At the top of `Open`, old:

```go
func (s *Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
```

New:

```go
func (s *Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
	if s == nil || s.aead == nil {
		return nil, errNotInitialised
	}
```

Change the end of `Derive`'s doc comment and the top of the function. Old:

```go
// CSRFLabel, so no subkey equals another or the role key.
func (s *Sealer) Derive(label string) []byte {
```

New:

```go
// CSRFLabel, so no subkey equals another or the role key. A nil or
// zero-value Sealer panics: deriving from its empty key would return a
// subkey anyone can compute.
func (s *Sealer) Derive(label string) []byte {
	if s == nil || s.key == nil {
		panic(errNotInitialised)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/secret/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/secret`)

Run: `timeout 600 golangci-lint run ./internal/core/secret/...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/secret/sealer.go internal/core/secret/sealer_test.go
git commit -m "fix(secret): refuse an uninitialised sealer"
```

### Task 3: Backup pruning safety

**Files:**
- Modify: `internal/core/store/backup.go` (`Backup`, new `PruneBackups`, `pruneBackups`, new `holdsDatabase`, new `chmod` seam)
- Modify: `internal/core/store/migrate.go` (`MigrateOptions` comments, `Result.PruneErr`, `Migrate`)
- Test: `internal/core/store/backup_test.go`, `internal/core/store/migrate_test.go`

**Interfaces:**
- Consumes: none
- Produces: Contracts "store.Result.PruneErr"

**Items:** 45

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Design notes from drafting, binding for this task:

- This task removes `Backup`'s `keep` parameter. The Contracts entry keeps `Backup`'s `(string, error)` result, but once pruning moves to `PruneBackups` the parameter has no use (revive would flag it), and `Backup` has no caller outside `internal/core/store`.
- This task has `Migrate` call an unexported `pruneBackups(root, dbName, current, keepTo, keep)`: `current` is the backup this run took (empty when it took none) and `keepTo` is the running binary version. Both the backup named `current` and the newest backup whose name's second label equals `keepTo` are never deleted and count first toward `keep`, so the pre-upgrade backup survives a clock set back on the run that made it and on every restart after it. The exported `PruneBackups(root, dbName, keep)` from the Contracts entry protects neither.
- This task makes `Migrate` refuse `Keep < 1` up front as a fatal error, before anything runs. Today `Backup` refuses it before copying; without the up-front check a bad `Keep` would only ever surface as a `PruneErr` warning.

- [ ] **Step 1: Write the failing store tests for the new pruning shape**

In `internal/core/store/backup_test.go`, replace the import block and add two helpers after it. Old:

```go
import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)
```

New:

```go
import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

// makeBackup creates root/name holding a test.db file, as Backup leaves a
// backup of openTemp's database.
func makeBackup(t *testing.T, root, name string) {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "test.db"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

// entryNames lists the names in root, sorted.
func entryNames(t *testing.T, root string) []string {
	t.Helper()
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}
```

In `TestBackupCopiesDatabase`, old:

```go
	dir, err := Backup(ctx, db, root, "v1.0.0+dirty", "v1.1.0", now, 5)
```

New:

```go
	dir, err := Backup(ctx, db, root, "v1.0.0+dirty", "v1.1.0", now)
```

Replace the head of `TestBackupKeepsNewest`, which pruning now reaches through `PruneBackups`. Old:

```go
func TestBackupKeepsNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		if _, err := Backup(ctx, db, root, "a", "b", start.Add(time.Duration(i)*time.Minute), 5); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
```

New:

```go
func TestPruneBackupsKeepsNewest(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, "unrelated"), 0o700); err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		if _, err := Backup(ctx, db, root, "a", "b", start.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	if err := PruneBackups(root, "test.db", 5); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
```

Replace the head of `TestBackupPrunesEmptyVersions`. Old:

```go
func TestBackupPrunesEmptyVersions(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		dir, err := Backup(ctx, db, root, "", "", start.Add(time.Duration(i)*time.Minute), 5)
		if err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
		if i == 0 && filepath.Base(dir) != "unknown-unknown-20261017T000000Z" {
			t.Errorf("backup dir = %s, want unknown-unknown-20261017T000000Z", filepath.Base(dir))
		}
	}
```

New:

```go
func TestPruneBackupsRecognizesEmptyVersions(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	start := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	for i := range 7 {
		dir, err := Backup(ctx, db, root, "", "", start.Add(time.Duration(i)*time.Minute))
		if err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
		if i == 0 && filepath.Base(dir) != "unknown-unknown-20261017T000000Z" {
			t.Errorf("backup dir = %s, want unknown-unknown-20261017T000000Z", filepath.Base(dir))
		}
	}
	if err := PruneBackups(root, "test.db", 5); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
```

In `TestBackupRefusesExistingDirectory`, old:

```go
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err == nil {
```

New:

```go
	if _, err := Backup(context.Background(), db, root, "a", "b", now); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), db, root, "a", "b", now); err == nil {
```

In `TestBackupFailureLeavesNoDirectory`, old:

```go
	if _, err := Backup(ctx, db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC), 5); err == nil {
```

New:

```go
	if _, err := Backup(ctx, db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)); err == nil {
```

Replace the head of `TestBackupKeepsNewDirectoryWhenClockGoesBack`. Old:

```go
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
```

New:

```go
func TestPruneKeepsNewDirectoryWhenClockGoesBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	root := t.TempDir()
	later := time.Date(2026, 10, 18, 0, 0, 0, 0, time.UTC)
	for i := range 5 {
		if _, err := Backup(ctx, db, root, "a", "b", later.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("Backup %d: %v", i, err)
		}
	}
	dir, err := Backup(ctx, db, root, "a", "b", later.Add(-time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if err := pruneBackups(root, "test.db", filepath.Base(dir), "", 5); err != nil {
		t.Fatalf("pruneBackups: %v", err)
	}
```

Append to the end of `internal/core/store/backup_test.go`:

```go
func TestPruneBackupsCountsOnlyDirectoriesHoldingTheDatabase(t *testing.T) {
	root := t.TempDir()
	for i := range 3 {
		makeBackup(t, root, fmt.Sprintf("a-b-20261017T00000%dZ", i))
	}
	// Named like backups and older than every real one, but holding no
	// database file of their own.
	empty := filepath.Join(root, "a-b-20200101T000000Z")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	linked := filepath.Join(root, "a-b-20200101T000001Z")
	if err := os.Mkdir(linked, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "a-b-20261017T000000Z", "test.db"), filepath.Join(linked, "test.db")); err != nil {
		t.Fatal(err)
	}
	if err := PruneBackups(root, "test.db", 2); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	want := []string{"a-b-20200101T000000Z", "a-b-20200101T000001Z", "a-b-20261017T000001Z", "a-b-20261017T000002Z"}
	if got := entryNames(t, root); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
}

func TestPruneBackupsIgnoresSymlinks(t *testing.T) {
	root := t.TempDir()
	outside := t.TempDir()
	makeBackup(t, outside, "a-b-20200101T000000Z")
	if err := os.Symlink(filepath.Join(outside, "a-b-20200101T000000Z"), filepath.Join(root, "a-b-20200101T000000Z")); err != nil {
		t.Fatal(err)
	}
	makeBackup(t, root, "a-b-20261017T000000Z")
	makeBackup(t, root, "a-b-20261017T000001Z")
	if err := PruneBackups(root, "test.db", 1); err != nil {
		t.Fatalf("PruneBackups: %v", err)
	}
	want := []string{"a-b-20200101T000000Z", "a-b-20261017T000001Z"}
	if got := entryNames(t, root); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("entries = %v, want %v", got, want)
	}
	if _, err := os.Stat(filepath.Join(outside, "a-b-20200101T000000Z", "test.db")); err != nil {
		t.Errorf("the symlink's target: %v, want it untouched", err)
	}
}

func TestPruneBackupsRefusesKeepBelowOne(t *testing.T) {
	root := t.TempDir()
	makeBackup(t, root, "a-b-20261017T000000Z")
	for _, keep := range []int{0, -1} {
		if err := PruneBackups(root, "test.db", keep); err == nil {
			t.Errorf("PruneBackups with keep %d succeeded, want an error", keep)
		}
	}
	if got := entryNames(t, root); len(got) != 1 {
		t.Errorf("entries = %v, want the one backup", got)
	}
}

func TestPruneBackupsWithoutRoot(t *testing.T) {
	if err := PruneBackups(filepath.Join(t.TempDir(), "missing"), "test.db", 5); err != nil {
		t.Errorf("PruneBackups of a missing root = %v, want nil", err)
	}
}
```

In `internal/core/store/migrate_test.go`, add `"fmt"` to the imports. Old:

```go
import (
	"context"
	"os"
```

New:

```go
import (
	"context"
	"fmt"
	"os"
```

Append to the end of `internal/core/store/migrate_test.go`:

```go
func TestMigrateRefusesKeepBelowOne(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	opts.Keep = 0
	if _, err := Migrate(context.Background(), db, opts, core, nil); err == nil || !strings.Contains(err.Error(), "backups kept") {
		t.Fatalf("Migrate error = %v, want one naming the backups kept", err)
	}
	if tableExists(t, db, "core_meta") {
		t.Error("core migrated with Keep 0")
	}
}

func TestMigratePrunesOnEveryRun(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	// Older backups that an earlier failed prune left behind.
	for i := range 6 {
		makeBackup(t, opts.BackupRoot, fmt.Sprintf("v0-v1-20261016T00000%dZ", i))
	}
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || res.BackupDir != "" || res.PruneErr != nil {
		t.Fatalf("second run = %+v, %v; want no backup and no prune error", res, err)
	}
	want := []string{
		"none-v1-20261017T010000Z", "v0-v1-20261016T000002Z", "v0-v1-20261016T000003Z",
		"v0-v1-20261016T000004Z", "v0-v1-20261016T000005Z",
	}
	if got := entryNames(t, opts.BackupRoot); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("backups = %v, want %v", got, want)
	}
}

func TestMigrateReportsAFailedPrune(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	opts.BackupRoot = filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(opts.BackupRoot, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil {
		t.Fatalf("Migrate: %v (a failed prune must not fail the run)", err)
	}
	if res.PruneErr == nil || !strings.Contains(res.PruneErr.Error(), "store: list backups") {
		t.Errorf("PruneErr = %v, want the failure to list the backups", res.PruneErr)
	}
}

func TestMigrateKeepsTheUpgradeBackupAfterAClockStepBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	for i := range 5 {
		makeBackup(t, opts.BackupRoot, fmt.Sprintf("v0-v1-20261018T00000%dZ", i))
	}
	// The upgrade runs while the clock reads a day before every backup.
	opts.BinaryVersion = "v2"
	opts.Now = time.Date(2026, 10, 16, 0, 0, 0, 0, time.UTC)
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || res.BackupDir == "" || res.PruneErr != nil {
		t.Fatalf("upgrade run = %+v, %v; want a backup and no prune error", res, err)
	}
	upgrade := filepath.Base(res.BackupDir)
	res, err = Migrate(ctx, db, opts, core, nil)
	if err != nil || res.BackupDir != "" || res.PruneErr != nil {
		t.Fatalf("restart = %+v, %v; want no backup and no prune error", res, err)
	}
	if _, err := os.Stat(filepath.Join(opts.BackupRoot, upgrade)); err != nil {
		t.Errorf("the upgrade backup %s did not survive the restart: %v", upgrade, err)
	}
	if got := len(entryNames(t, opts.BackupRoot)); got != 5 {
		t.Errorf("%d backups kept, want 5", got)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 go test ./internal/core/store/ -count=1`
Expected: FAIL (build failed) with `too many arguments in call to Backup`, `undefined: PruneBackups`, `not enough arguments in call to pruneBackups` and `res.PruneErr undefined`

- [ ] **Step 3: Write the implementation**

Replace the whole of `internal/core/store/backup.go` with:

```go
package store

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const backupTimeLayout = "20060102T150405Z"

var (
	unsafeVersionChars = regexp.MustCompile(`[^A-Za-z0-9._]`)
	backupDirName      = regexp.MustCompile(`^[A-Za-z0-9._]+-[A-Za-z0-9._]+-(\d{8}T\d{6}Z)$`)
)

// Backup copies db with VACUUM INTO to root/<from>-<to>-<UTC time>/<db file
// name> (directory 0700, file 0600). A failed copy or mode change removes its
// directory. Characters other than letters, digits, dot and underscore in
// from and to become underscores, and an empty one becomes "unknown", so
// retention always recognizes the name. It returns the new backup's
// directory and deletes no older backup; PruneBackups does. Backups are for
// disaster recovery only; nothing restores them automatically (ADR 0005).
func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time) (string, error) {
	name := fmt.Sprintf("%s-%s-%s", versionLabel(from), versionLabel(to), now.UTC().Format(backupTimeLayout))
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("store: backup root: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("store: backup directory: %w", err)
	}
	target := filepath.Join(dir, filepath.Base(db.Path()))
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		// A failed copy must not leave a directory that retention counts as a backup.
		return "", errors.Join(fmt.Errorf("store: VACUUM INTO %s: %w", target, err), os.RemoveAll(dir))
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", errors.Join(fmt.Errorf("store: backup mode: %w", err), os.RemoveAll(dir))
	}
	return dir, nil
}

func versionLabel(v string) string {
	if v == "" {
		return "unknown"
	}
	return unsafeVersionChars.ReplaceAllString(v, "_")
}

// PruneBackups deletes the oldest backups under root beyond the newest keep,
// ranked by the time in their names. A backup is a directory named as Backup
// names one that holds dbName as a regular file, so an unrelated directory, a
// symlink or a directory without the database is never counted or deleted.
// A root that does not exist holds no backups. A directory that cannot be
// deleted does not stop the others; every failure is returned.
func PruneBackups(root, dbName string, keep int) error {
	return pruneBackups(root, dbName, "", "", keep)
}

// pruneBackups is PruneBackups that never deletes the backup named current
// or the newest backup made for version keepTo, and counts them first. A
// clock set back would otherwise rank the backup taken before an upgrade
// among the oldest, on the run that took it or on any restart after it. An
// empty current or keepTo protects nothing.
func pruneBackups(root, dbName, current, keepTo string, keep int) error {
	if keep < 1 {
		return fmt.Errorf("store: backups kept = %d, want at least 1", keep)
	}
	entries, err := os.ReadDir(root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	type backup struct{ name, to, stamp string }
	var backups []backup
	for _, e := range entries {
		m := backupDirName.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil || !holdsDatabase(filepath.Join(root, e.Name()), dbName) {
			continue
		}
		// Version labels hold no '-', so a backup name splits into exactly
		// from, to and the time.
		backups = append(backups, backup{e.Name(), strings.Split(e.Name(), "-")[1], m[1]})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].stamp != backups[j].stamp {
			return backups[i].stamp > backups[j].stamp
		}
		return strings.Compare(backups[i].name, backups[j].name) > 0
	})
	protected := map[string]bool{}
	if current != "" {
		protected[current] = true
	}
	for _, b := range backups {
		if keepTo != "" && b.to == keepTo {
			protected[b.name] = true
			break
		}
	}
	kept := 0
	for _, b := range backups {
		if protected[b.name] {
			kept++
		}
	}
	var errs []error
	for _, b := range backups {
		if protected[b.name] {
			continue
		}
		if kept < keep {
			kept++
			continue
		}
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			// Keep going: stopping here would let every retried run add a copy.
			errs = append(errs, fmt.Errorf("store: prune backup %s: %w", b.name, err))
		}
	}
	return errors.Join(errs...)
}

// holdsDatabase reports whether dir holds name as a regular file; Lstat keeps
// a symlink from qualifying.
func holdsDatabase(dir, name string) bool {
	info, err := os.Lstat(filepath.Join(dir, name))
	return err == nil && info.Mode().IsRegular()
}
```

In `internal/core/store/migrate.go`, make these replacements.

Imports. Old:

```go
	"io/fs"
	"regexp"
```

New:

```go
	"io/fs"
	"path/filepath"
	"regexp"
```

`MigrateOptions`. Old:

```go
	BackupRoot    string    // directory for Backup
	Keep          int       // backups kept, 5 in production
```

New:

```go
	BackupRoot    string    // directory for Backup and PruneBackups
	Keep          int       // backups kept, at least 1; 5 in production
```

`Result`. Old:

```go
	Applied   []Applied
	BackupDir string // empty when no backup was taken
	Failed    []ModuleFailure
}
```

New:

```go
	Applied   []Applied
	BackupDir string // empty when no backup was taken
	Failed    []ModuleFailure
	PruneErr  error // a failed prune of old backups; the migration itself succeeded
}
```

The end of `Migrate`'s doc comment and its first lines. Old:

```go
// Module sets named like the core or like each other are refused before
// anything runs, because they would share core_migrations rows.
func Migrate(ctx context.Context, db *DB, opts MigrateOptions, core MigrationSet, modules []MigrationSet) (Result, error) {
	var res Result
	seen := map[string]bool{core.Module: true}
```

New:

```go
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
```

The run with nothing pending. Old:

```go
	if !anyPending {
		return res, setBinaryVersion(ctx, db, opts.BinaryVersion)
	}
```

New:

```go
	dbName := filepath.Base(db.Path())
	if !anyPending {
		res.PruneErr = PruneBackups(opts.BackupRoot, dbName, opts.Keep)
		return res, setBinaryVersion(ctx, db, opts.BinaryVersion)
	}
```

The backup step. Old:

```go
	if from != opts.BinaryVersion {
		dir, err := Backup(ctx, db, opts.BackupRoot, from, opts.BinaryVersion, opts.Now, opts.Keep)
		if err != nil {
			return res, err
		}
		res.BackupDir = dir
	}
```

New:

```go
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
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/store/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/store`)

- [ ] **Step 5: Write the failing test for the chmod-failure cleanup**

In `internal/core/store/backup_test.go`, add `"errors"` and `"io/fs"` to the imports so the block reads:

```go
import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)
```

Append:

```go
func TestBackupFailedChmodLeavesNoDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	boom := errors.New("boom")
	chmod = func(string, fs.FileMode) error { return boom }
	t.Cleanup(func() { chmod = os.Chmod })
	_, err := Backup(context.Background(), db, root, "a", "b", time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC))
	if !errors.Is(err, boom) {
		t.Fatalf("Backup error = %v, want the chmod failure", err)
	}
	if got := entryNames(t, root); len(got) != 0 {
		t.Errorf("entries after a failed chmod = %v, want none", got)
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/store/ -run '^TestBackupFailedChmodLeavesNoDirectory$' -count=1`
Expected: FAIL (build failed) with `undefined: chmod`

- [ ] **Step 7: Write the implementation**

In `internal/core/store/backup.go`, add the seam after the `var (...)` block. Old:

```go
	backupDirName      = regexp.MustCompile(`^[A-Za-z0-9._]+-[A-Za-z0-9._]+-(\d{8}T\d{6}Z)$`)
)
```

New:

```go
	backupDirName      = regexp.MustCompile(`^[A-Za-z0-9._]+-[A-Za-z0-9._]+-(\d{8}T\d{6}Z)$`)
)

// chmod is os.Chmod; a test replaces it to fail the backup's mode change.
var chmod = os.Chmod
```

In `Backup`, old:

```go
	if err := os.Chmod(target, 0o600); err != nil {
```

New:

```go
	if err := chmod(target, 0o600); err != nil {
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/store/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/store`)

- [ ] **Step 9: Run the whole suite and lint**

Every package that migrates a test database goes through `Migrate`, so run them all. The three `Migrate` callers still compile: they read only `Result.Applied` and `Result.BackupDir`, and Task 8 makes them report `PruneErr`.

Run: `timeout 900 go test ./... -count=1`
Expected: PASS for every package.

Run: `timeout 600 golangci-lint run ./...`
Expected: `0 issues.`

- [ ] **Step 10: Commit**

```bash
git add internal/core/store/backup.go internal/core/store/backup_test.go internal/core/store/migrate.go internal/core/store/migrate_test.go
git commit -m "fix(store): make backup pruning safe and non-fatal"
```

### Task 4: Fake clock step back

**Files:**
- Modify: `internal/core/coretest/clock.go` (`FakeClock` doc, new `StepBack`)
- Test: `internal/core/coretest/clock_test.go`

**Interfaces:**
- Consumes: none
- Produces: Contracts "coretest.FakeClock.StepBack"

**Items:** 59

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test for `FakeClock.StepBack`**

Append to `internal/core/coretest/clock_test.go`:

```go
func TestFakeClockStepBack(t *testing.T) {
	c := NewFakeClock(start)
	fired := false
	c.AfterFunc(time.Minute, func() { fired = true })
	c.StepBack(time.Hour)
	if !c.Now().Equal(start.Add(-time.Hour)) {
		t.Errorf("Now = %v, want start-1h", c.Now())
	}
	c.Advance(time.Hour)
	if fired {
		t.Error("the timer armed for start+1m fired at start")
	}
	c.Advance(time.Minute)
	if !fired {
		t.Error("the timer did not fire at start+1m")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/coretest/ -run '^TestFakeClockStepBack$' -count=1`
Expected: FAIL (build failed) with `c.StepBack undefined (type *FakeClock has no field or method StepBack)`

- [ ] **Step 3: Write the implementation**

In `internal/core/coretest/clock.go`, replace the type's doc comment. Old:

```go
// FakeClock is a core.Clock whose time moves only through Advance.
```

New:

```go
// FakeClock is a core.Clock whose time moves only through Advance and
// StepBack.
```

Insert directly before the `// Pending is the number of armed timers` comment:

```go
// StepBack moves the clock back by d, as a wall clock set back does. It runs
// no timer; armed timers keep their deadlines and fire when Advance reaches
// them.
func (c *FakeClock) StepBack(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(-d)
}

```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/coretest/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/coretest`)

- [ ] **Step 5: Commit**

```bash
git add internal/core/coretest/clock.go internal/core/coretest/clock_test.go
git commit -m "test(coretest): let the fake clock step back"
```

### Task 5: Redact schema cache

**Files:**
- Modify: `internal/core/schema/redact.go` (`Redact`, new `cachedOutput`, `outputSchemas`, `cachedSchema`)
- Test: `internal/core/schema/redact_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3

- [ ] **Step 1: Write the failing test**

In `internal/core/schema/redact_test.go` (package `schema_test`), add the `secret` import so the block reads:

```go
import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/secret"
)
```

Append to the end of the file:

```go
// TestRedactReusesTheSchema holds Redact to the cost of redacting over a
// schema already built: building one reflects the whole type.
func TestRedactReusesTheSchema(t *testing.T) {
	typ := reflect.TypeFor[redactTree]()
	raw := encode(t, redactTree{Label: "l", Inner: redactLeaf{Name: "a", Key: "k1"}, Items: []redactLeaf{{Name: "b", Key: "k2"}}})
	s, err := schema.Output(typ)
	if err != nil {
		t.Fatal(err)
	}
	schema.Redact(typ, raw)
	redact := testing.AllocsPerRun(20, func() { schema.Redact(typ, raw) })
	direct := testing.AllocsPerRun(20, func() { secret.RedactJSON(raw, s) })
	if redact > direct {
		t.Errorf("Redact allocates %v times a call, RedactJSON over the built schema %v: Redact rebuilds the schema", redact, direct)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/schema/ -run '^TestRedactReusesTheSchema$' -count=1`
Expected: FAIL with `Redact allocates 339 times a call, RedactJSON over the built schema 191: Redact rebuilds the schema` (the exact counts may differ; Redact's must be the higher)

- [ ] **Step 3: Write the implementation**

Replace the whole of `internal/core/schema/redact.go` with:

```go
package schema

import (
	"encoding/json"
	"reflect"
	"sync"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

// outputSchemas caches Output's result, schema or error, per type for Redact,
// which runs on every read, plan and change.
var outputSchemas sync.Map // reflect.Type -> cachedSchema

type cachedSchema struct {
	schema json.RawMessage
	err    error
}

// Redact returns raw, a JSON value of type t, with every field tagged
// secret:"true" replaced by "[redacted]" (spec section 5). It is the
// lifecycle engine's Config.Redact on both sides, for inputs, change outputs
// and read outputs alike. When t has no output schema the whole value is
// redacted, never returned as is.
func Redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := cachedOutput(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}

func cachedOutput(t reflect.Type) (json.RawMessage, error) {
	if c, ok := outputSchemas.Load(t); ok {
		cs := c.(cachedSchema)
		return cs.schema, cs.err
	}
	s, err := Output(t)
	outputSchemas.Store(t, cachedSchema{s, err})
	return s, err
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/schema/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/schema`). `TestRedactFailsClosed` still passes; it covers the cached error for a nil type.

Run: `timeout 300 go test -race ./internal/core/schema/ -run 'Redact' -count=1`
Expected: PASS

Run: `timeout 600 golangci-lint run ./internal/core/schema/...`
Expected: `0 issues.`

No generated schema or snapshot changes: `Output` is unchanged.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/redact.go internal/core/schema/redact_test.go
git commit -m "perf(schema): cache output schemas in Redact"
```

### Task 6: Makefile dependency stamp and host-arch check

**Files:**
- Modify: `Makefile` (`web-deps`, `verify-build`)

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 38

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 1 = 1

The Makefile has no test suite; each change is checked by running make.

- [ ] **Step 1: Show both defects**

Run: `timeout 300 make web-deps 2>&1 | tail -2; timeout 300 make web-deps 2>&1 | tail -2`
Expected: both runs print pnpm's install summary (for example `Done in ...`): the install reruns every time.

Run: `GOARCH=arm64 timeout 1200 make verify-build 2>&1 | tail -3`
Expected: FAIL on an amd64 host (an `exec format error`, or `go test` failing to run arm64 test binaries).

- [ ] **Step 2: Replace `web-deps` with a stamp**

Replace

```make
web-deps:
	pnpm -C web install --frozen-lockfile
```

with

```make
# The stamp reruns the install only when the manifest, the lockfile or the
# install policy changes.
WEB_STAMP := web/node_modules/.stamp

web-deps: $(WEB_STAMP)

$(WEB_STAMP): web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml
	pnpm -C web install --frozen-lockfile
	touch $@
```

`web-deps` stays in `.PHONY`; the stamp is a real file under the git-ignored `web/node_modules/`.

- [ ] **Step 3: Run the executing checks on the host platform**

Replace the last three lines of `verify-build`

```make
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 go test -count=1 -run '^(TestEndToEnd|TestFleetEndToEnd)$$' ./internal/controller/server/
	bin/darkpanel-linux-$$(go env GOARCH) version --json | grep -qF '"commit":"$(COMMIT)"'
```

with

```make
	DARKPANEL_REQUIRE_UI=1 env -u GOOS -u GOARCH go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 env -u GOOS -u GOARCH go test -count=1 -run '^(TestEndToEnd|TestFleetEndToEnd)$$' ./internal/controller/server/
	hostos=$$(go env GOHOSTOS); hostarch=$$(go env GOHOSTARCH); \
	if [ "$$hostos" = linux ] && [[ " $(ARCHES) " == *" $$hostarch "* ]]; then \
		bin/darkpanel-linux-$$hostarch version --json | grep -qF '"commit":"$(COMMIT)"'; \
	else \
		echo "verify-build: skipped the version run: a $$hostos/$$hostarch host cannot run the linux binaries"; \
	fi
```

The Makefile's `SHELL` is bash, so `[[ ... ]]` is available.

- [ ] **Step 4: Verify both fixes**

Run: `timeout 300 make web-deps 2>&1 | tail -2; timeout 300 make web-deps 2>&1 | tail -2`
Expected: the first run may install; the second prints `make: Nothing to be done for 'web-deps'.` and no pnpm output.

Run: `touch web/pnpm-workspace.yaml && timeout 300 make web-deps 2>&1 | tail -1`
Expected: pnpm runs again (its summary line).

Run: `GOARCH=arm64 timeout 1200 make verify-build && timeout 1200 make verify-build`
Expected: exit 0; both runs end with the `ok  	github.com/darkraise/darkpanel/internal/controller/server` test line and no error (the version run prints nothing on success).

- [ ] **Step 5: Commit**

```bash
git add Makefile
git commit -m "fix(make): stamp web-deps and run checks on host"
```

### Task 7: Lifecycle ID ordering and plan refusals

**Files:**
- Modify: `internal/core/lifecycle/engine.go` (`Engine` struct, `newID`, new `seedIDs`, `Plan`)
- Modify: `internal/core/lifecycle/apply.go` (the `newID` call in apply's intent)
- Modify: `internal/core/lifecycle/recover.go` (`Recover`)
- Modify: `internal/core/contract.go` (`Mutator.Verify` doc)
- Test: `internal/core/lifecycle/engine_test.go`

**Interfaces:**
- Consumes: Contracts "coretest.FakeClock.StepBack"
- Produces: none

**Items:** 59, 95

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5

Do not touch the audit store (`internal/controller/audit`): its ids follow each entry's own time by design.

Design note from drafting, binding for this task:

- This task seeds the id clock in `Engine.Recover`, because `New` takes no context and the 59a ruling says "engine start". `Recover` is documented as run once at start, and both production sides call it (`internal/agent/runtime/runtime.go:135`, `internal/controller/server/services.go:130`). Engines built in other packages' tests without `Recover` are not seeded, but the in-process clamp still applies to them. The seed reads only 26-character ids, because tests store ids such as `01CRASHED0000000000000000` (25 characters, `server/services_test.go:116`) and `plan-<id>` (`update/updater_test.go:206`); any of those would otherwise sort above the real ULIDs or fail to parse.

- [ ] **Step 1: Write the failing test for id ordering across a backwards clock step**

In `internal/core/lifecycle/engine_test.go`, add `"time"` to the imports so the block reads:

```go
import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)
```

Append to the end of `internal/core/lifecycle/engine_test.go`:

```go
func TestIDsFollowCreationOrderWhenTheClockStepsBack(t *testing.T) {
	f := newFixture(t)
	add := func(name string) (core.PlanView, core.ChangeView) {
		t.Helper()
		pl := f.plan(alice, refAdd, `{"name":"`+name+`","value":"1"}`)
		return pl, f.apply(alice, pl.PlanID, core.ApplyOptions{})
	}
	p1, c1 := add("a")
	f.clock.StepBack(time.Hour)
	p2, c2 := add("b")
	// A new engine seeds its ids from the stored ones.
	f.restart()
	f.recoverAll()
	f.clock.StepBack(time.Hour)
	p3, c3 := add("c")
	if p1.PlanID >= p2.PlanID || p2.PlanID >= p3.PlanID {
		t.Errorf("plan ids %s, %s, %s; want them ascending in creation order", p1.PlanID, p2.PlanID, p3.PlanID)
	}
	if c1.ID >= c2.ID || c2.ID >= c3.ID {
		t.Errorf("change ids %s, %s, %s; want them ascending in creation order", c1.ID, c2.ID, c3.ID)
	}
	if want := f.clock.Now().Add(PlanTTL); !p3.ExpiresAt.Equal(want) {
		t.Errorf("plan expires at %v, want %v: expiry follows the clock, not the id", p3.ExpiresAt, want)
	}
	page, err := f.eng.Changes(context.Background(), core.ChangeFilter{Before: c3.ID})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, v := range page {
		got = append(got, v.ID)
	}
	if want := []string{c2.ID, c1.ID}; !reflect.DeepEqual(got, want) {
		t.Errorf("changes before %s = %v, want %v", c3.ID, got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/core/lifecycle/ -run '^TestIDsFollowCreationOrderWhenTheClockStepsBack$' -count=1`
Expected: FAIL with `plan ids ..., ..., ...; want them ascending in creation order`, `change ids ...; want them ascending in creation order` and `changes before ... = [], want [...]`

- [ ] **Step 3: Write the implementation**

In `internal/core/lifecycle/engine.go`, add the id floor to the `Engine` struct. Old:

```go
	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
```

New:

```go
	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
	idMs    uint64 // no id is stamped below this millisecond
```

Replace `newID`, and add `seedIDs` after it. Old:

```go
// newID returns a ULID stamped with the engine's clock; ids made by one
// engine sort in creation order.
func (e *Engine) newID() string {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(e.cfg.Base.Clock.Now()), e.entropy).String()
}
```

New:

```go
// newID returns a ULID stamped with now, or with the newest id's millisecond
// when the clock has stepped back since. Ids made by one engine, and after
// Recover's seed by every engine on the database, sort in creation order,
// which Changes pages by.
func (e *Engine) newID(now time.Time) string {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	e.idMs = max(e.idMs, ulid.Timestamp(now))
	return ulid.MustNew(e.idMs, e.entropy).String()
}

// seedIDs makes every id newID returns sort after each stored plan and change
// id, even when the clock stepped back across a restart: the next id is
// stamped at least one millisecond after the newest stored one. Ids that are
// not ULIDs (26 characters) are ignored; the engine never made them.
func (e *Engine) seedIDs(ctx context.Context) error {
	var newest sql.NullString
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT max(id) FROM (
		 SELECT max(id) AS id FROM core_plans WHERE length(id) = 26
		 UNION ALL SELECT max(id) FROM core_changes WHERE length(id) = 26)`).Scan(&newest)
	if err != nil {
		return fmt.Errorf("lifecycle: read the newest id: %w", err)
	}
	if !newest.Valid {
		return nil
	}
	if id, err := ulid.ParseStrict(newest.String); err == nil {
		e.idMu.Lock()
		defer e.idMu.Unlock()
		e.idMs = max(e.idMs, id.Time()+1)
	}
	return nil
}
```

In `Plan`, read the clock once. Old:

```go
	v := core.PlanView{
		PlanID: e.newID(), HostID: e.cfg.Base.HostID, Action: def.Spec.Name, Version: def.Spec.Version,
		InputHash: e.inputHash(def, canonical), Confirmation: out.Confirmation,
		Diff: nonNil(out.Diff), Guards: nonNil(out.Guards), ExpiresAt: e.cfg.Base.Clock.Now().Add(PlanTTL),
	}
```

New:

```go
	// One reading: the id may be stamped later than now, but expiry and
	// created_at stay on the clock.
	now := e.cfg.Base.Clock.Now()
	v := core.PlanView{
		PlanID: e.newID(now), HostID: e.cfg.Base.HostID, Action: def.Spec.Name, Version: def.Spec.Version,
		InputHash: e.inputHash(def, canonical), Confirmation: out.Confirmation,
		Diff: nonNil(out.Diff), Guards: nonNil(out.Guards), ExpiresAt: now.Add(PlanTTL),
	}
```

Old:

```go
	created := formatTime(e.cfg.Base.Clock.Now())
```

New:

```go
	created := formatTime(now)
```

In `internal/core/lifecycle/apply.go`, old:

```go
	it := &intent{id: e.newID(), def: def, input: input, confirmNow: opts.ConfirmNow}
```

New:

```go
	it := &intent{id: e.newID(e.cfg.Base.Clock.Now()), def: def, input: input, confirmNow: opts.ConfirmNow}
```

In `internal/core/lifecycle/recover.go`, old:

```go
// irreversible change cut off during apply is marked interrupted with what its
// verify reports, and is never retried or compensated.
func (e *Engine) Recover(ctx context.Context) error {
```

New:

```go
// irreversible change cut off during apply is marked interrupted with what its
// verify reports, and is never retried or compensated. Recover first seeds the
// engine's ids from the stored ones, so new ids sort after them even when the
// clock stepped back across the restart.
func (e *Engine) Recover(ctx context.Context) error {
	if err := e.seedIDs(ctx); err != nil {
		return err
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/core/lifecycle/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/lifecycle`)

- [ ] **Step 5: Write the coverage test for an irreversible plan without a phrase**

This test pins behaviour `Plan` already has. It passes once written, and no implementation step follows it. In `internal/core/lifecycle/engine_test.go`, add `"strings"` to the imports, between `"reflect"` and `"testing"`. Then append:

```go
func TestPlanRefusesAnIrreversiblePlanWithoutAPhrase(t *testing.T) {
	f := newFixture(t)
	// The fixture's phrase is the item name, so an empty name gives none.
	_, err := f.eng.Plan(context.Background(), alice, refRemove, json.RawMessage(`{"name":""}`))
	if err == nil || !strings.Contains(err.Error(), "returned no confirmation phrase") {
		t.Fatalf("Plan = %v, want the missing confirmation phrase refused", err)
	}
	if n := f.count("SELECT count(*) FROM core_plans"); n != 0 {
		t.Errorf("plans stored = %d, want 0", n)
	}
}
```

- [ ] **Step 6: Run the test**

Run: `timeout 300 go test ./internal/core/lifecycle/ -run '^TestPlanRefusesAnIrreversiblePlanWithoutAPhrase$' -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/core/lifecycle`)

- [ ] **Step 7: Document the Verify contract**

There is no test for this step: it changes a doc comment only. ADR 0004 is Accepted and is not edited.

In `internal/core/contract.go`, in the `Mutator` interface, old:

```go
	// Verify checks that Apply took effect. Startup recovery also calls it
	// for a change cut off during Apply, with out the zero value because no
	// output was recorded, so Verify must check the host from in alone.
	Verify(ctx context.Context, env Env, in In, out Out) error
```

New:

```go
	// Verify checks that Apply took effect. Startup recovery also calls it
	// for a change cut off during Apply, with out the zero value because no
	// output was recorded, so Verify must check the host from in alone.
	// Verify must only read the host and must return once ctx is done. The
	// engine stops waiting when its verify timeout ends ctx and may then roll
	// the change back while an abandoned Verify still runs, so Verify must
	// also be safe to run alongside that rollback.
	Verify(ctx context.Context, env Env, in In, out Out) error
```

- [ ] **Step 8: Run the whole suite and lint**

`Recover` runs at every controller and agent start, so run every package.

Run: `timeout 600 go test ./... -count=1`
Expected: PASS for every package.

Run: `timeout 600 golangci-lint run ./...`
Expected: `0 issues.`

- [ ] **Step 9: Commit**

```bash
git add internal/core/lifecycle/engine.go internal/core/lifecycle/apply.go internal/core/lifecycle/recover.go internal/core/lifecycle/engine_test.go internal/core/contract.go
git commit -m "fix(lifecycle): order ids across clock steps"
```

### Task 8: Report backup prune failures

**Files:**
- Modify: `internal/controller/server/server.go` (`migrateController`)
- Modify: `internal/agent/runtime/runtime.go` (the migration step of `Open`)
- Modify: `internal/install/install.go` (`database`)
- Test: `internal/controller/server/server_test.go`, `internal/agent/runtime/runtime_test.go`, `internal/install/install_test.go`

**Interfaces:**
- Consumes: Contracts "store.Result.PruneErr"
- Produces: none

**Items:** 45

**Depends on:** Task 3

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing tests for the three callers**

In `internal/controller/server/server_test.go`, insert directly before the `// TestControllerOnly is serve without the embedded agent` comment:

```go
func TestStartWarnsWhenBackupsCannotBePruned(t *testing.T) {
	opts := testOptions(t)
	first, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	backups := opts.Paths.ControllerBackups()
	if err := os.RemoveAll(backups); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backups, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var logs lockedBuffer
	opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
	start(t, opts)
	if out := logs.String(); !strings.Contains(out, `level=WARN msg="server: old controller database backups could not be pruned"`) {
		t.Errorf("service log lacks the prune warning:\n%s", out)
	}
}

```

In `internal/agent/runtime/runtime_test.go`, add `"os"` and `"path/filepath"` to the imports so the block reads:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)
```

Insert directly before `func TestHandlersGetTheAgentEnv(t *testing.T) {`. The fixture's `bad` module fails its migration on every start, so this second `Open` still has migrations pending and takes the prune after the backup step:

```go
func TestOpenWarnsWhenBackupsCannotBePruned(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	notDir := filepath.Join(f.dir, "not-a-directory")
	if err := os.WriteFile(notDir, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	var buf bytes.Buffer
	c := f.config()
	c.BackupDir = notDir
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatalf("Open: %v (a failed prune must not stop the runtime)", err)
	}
	defer rt.Close()
	if text := buf.String(); !strings.Contains(text, `level=WARN msg="runtime: old agent database backups could not be pruned"`) {
		t.Errorf("log lacks the prune warning:\n%s", text)
	}
}

```

In `internal/install/install_test.go`, insert directly before the `// TestInstallRerunAfterEachCheckpoint fails Install at each step` comment:

```go
func TestInstallWarnsWhenBackupsCannotBePruned(t *testing.T) {
	h := newTestHost(t)
	cfg := h.config(RoleAgent)
	cfg.Token = "dpe1.agent"
	ctx := context.Background()
	if err := Install(ctx, cfg); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	backups := h.layout.Paths.AgentBackups()
	if err := os.RemoveAll(backups); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backups, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	h.out.Reset()
	cfg.Token = ""
	if err := Install(ctx, cfg); err != nil {
		t.Fatalf("second Install: %v (a failed prune must not fail it)\n%s", err, h.out)
	}
	if !strings.Contains(h.out.String(), "warning: old agent database backups could not be pruned") {
		t.Errorf("output lacks the prune warning:\n%s", h.out)
	}
}

```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 go test ./internal/controller/server/ -run '^TestStartWarnsWhenBackupsCannotBePruned$' -count=1`
Expected: FAIL with `service log lacks the prune warning`

Run: `timeout 300 go test ./internal/agent/runtime/ -run '^TestOpenWarnsWhenBackupsCannotBePruned$' -count=1`
Expected: FAIL with `log lacks the prune warning`

Run: `timeout 300 go test ./internal/install/ -run '^TestInstallWarnsWhenBackupsCannotBePruned$' -count=1`
Expected: FAIL with `output lacks the prune warning`

- [ ] **Step 3: Write the implementation**

In `internal/controller/server/server.go` (`migrateController`), old:

```go
		s.log.Info("server: controller database backed up before migrating", "dir", res.BackupDir)
	}
```

New:

```go
		s.log.Info("server: controller database backed up before migrating", "dir", res.BackupDir)
	}
	if res.PruneErr != nil {
		s.log.Warn("server: old controller database backups could not be pruned", "error", res.PruneErr)
	}
```

In `internal/agent/runtime/runtime.go`, old:

```go
		r.cfg.Log.Info("runtime: agent database backed up before migrating", "dir", res.BackupDir)
	}
```

New:

```go
		r.cfg.Log.Info("runtime: agent database backed up before migrating", "dir", res.BackupDir)
	}
	if res.PruneErr != nil {
		r.cfg.Log.Warn("runtime: old agent database backups could not be pruned", "error", res.PruneErr)
	}
```

In `internal/install/install.go` (`database`), old:

```go
	if err != nil {
		return "", err
	}
	for _, f := range res.Failed {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: the agent migrations of module %s failed; the agent reports it unavailable\n", f.Module)
	}
```

New:

```go
	if err != nil {
		return "", err
	}
	if res.PruneErr != nil {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: old agent database backups could not be pruned: %v\n", res.PruneErr)
	}
	for _, f := range res.Failed {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: the agent migrations of module %s failed; the agent reports it unavailable\n", f.Module)
	}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run each:
`timeout 300 go test ./internal/core/store/ -count=1`
`timeout 300 go test ./internal/controller/server/ -count=1`
`timeout 300 go test ./internal/agent/runtime/ -count=1`
`timeout 300 go test ./internal/install/ -count=1`
Expected: PASS (`ok ...`) for each.

Every package that migrates a test database goes through `Migrate` (rollout, fleet, coreactions, platform, listen, update, dispatch, auth, audit, api, daemon, lifecycle, cmd/darkpanel), so run the whole suite and lint:

Run: `timeout 600 go test ./... -count=1`
Expected: PASS for every package.

Run: `timeout 600 golangci-lint run ./...`
Expected: `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/server_test.go internal/agent/runtime/runtime.go internal/agent/runtime/runtime_test.go internal/install/install.go internal/install/install_test.go
git commit -m "fix(server): report a failed backup prune"
```

### Task 9: Dispatcher Hello, noChange and Apply checks

**Files:**
- Modify: `internal/controller/dispatch/hosts.go` (`Hosts` struct comment, `Hello`, `hello`, new `forget`)
- Modify: `internal/controller/dispatch/apply.go` (`Apply`, `noChange`, new `checkApplied`)
- Test: `internal/controller/dispatch/hosts_test.go`
- Test: `internal/controller/dispatch/apply_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 0 - risk 1 = 3

Do not touch `Dispatcher.ingest` or the `d.ingest(ctx, hostID)` call in `apply.go`; Task 13 owns ingestion reporting.

- [ ] **Step 1: Write the failing test (95f)**

In `internal/controller/dispatch/hosts_test.go`, insert after `TestAgentSaysHelloOnceAndRetriesAFailure` (before `TestAgentRefusesUnknownAndMismatchedHosts`). No import changes are needed.

```go
// switchedClients is a ClientSource of one client at generation 1 whose
// lookup fails with err while err is set.
type switchedClients struct {
	client core.AgentClient
	err    error
}

func (s *switchedClients) Client(context.Context, string) (core.AgentClient, uint64, error) {
	if s.err != nil {
		return nil, 0, s.err
	}
	return s.client, 1, nil
}

// TestAFailedHelloForgetsTheGreeting: after a successful Hello, an explicit
// Hello that fails leaves no greeting behind, so the host's journal epoch is
// no longer reported and the next reach says Hello again on the same
// generation.
func TestAFailedHelloForgetsTheGreeting(t *testing.T) {
	cases := []struct {
		name   string
		answer func() (core.AgentHello, error) // the failing Hello; nil when the lookup fails
		lookup error
		hellos int // Hello calls over the first reach, the failure and the next reach
	}{
		{"Hello fails", func() (core.AgentHello, error) { return core.AgentHello{}, errors.New("agent offline") }, nil, 3},
		{"another host answers", func() (core.AgentHello, error) {
			return core.AgentHello{ProtocolVersion: 1, HostID: "h3"}, nil
		}, nil, 3},
		{"the client lookup fails", nil, errcode.New(errcode.HostOffline, "host h2 is offline"), 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHarness(t)
			ctx := context.Background()
			h.addHost("h2")
			hellos := 0
			var failing func() (core.AgentHello, error)
			src := &switchedClients{client: &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
				hellos++
				if failing != nil {
					return failing()
				}
				return core.AgentHello{ProtocolVersion: 1, HostID: "h2", JournalEpoch: "01EPOCHA"}, nil
			}}}
			hosts := NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: src, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
			if _, err := hosts.Agent(ctx, "h2"); err != nil {
				t.Fatal(err)
			}
			failing, src.err = c.answer, c.lookup
			if err := hosts.Hello(ctx, "h2"); err == nil {
				t.Fatal("Hello succeeded; want the failure")
			}
			if got := hosts.JournalEpoch("h2"); got != "" {
				t.Errorf("epoch after a failed Hello = %q, want empty", got)
			}
			failing, src.err = nil, nil
			if _, err := hosts.Agent(ctx, "h2"); err != nil {
				t.Fatal(err)
			}
			if hellos != c.hellos {
				t.Errorf("Hello calls = %d, want %d: the reach after a failed Hello says Hello again", hellos, c.hellos)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/dispatch/ -run '^TestAFailedHelloForgetsTheGreeting$' -count=1`
Expected: FAIL in all three subtests with `epoch after a failed Hello = "01EPOCHA", want empty` and `Hello calls = 2, want 3` (`Hello calls = 1, want 2` for the lookup case).

- [ ] **Step 3: Write the implementation (95f)**

In `internal/controller/dispatch/hosts.go`, replace:

```go
	mu      sync.Mutex             // guards greeted and locks; never held across I/O
	greeted map[string]greeting    // each host's last successful Hello
```

with:

```go
	mu      sync.Mutex             // guards greeted and locks; never held across I/O
	greeted map[string]greeting    // each host's last Hello, while no later one has failed
```

Replace the whole `Hello` method:

```go
// Hello calls hostID's Hello now and records every module's availability.
// The server calls it for the local host at startup, which seeds the host's
// module rows.
func (h *Hosts) Hello(ctx context.Context, hostID string) error {
	unlock, err := h.lockHost(ctx, hostID)
	if err != nil {
		return err
	}
	defer unlock()
	c, gen, err := h.cfg.Clients.Client(ctx, hostID)
	if err != nil {
		return err
	}
	return h.hello(ctx, hostID, c, gen)
}
```

with:

```go
// Hello calls hostID's Hello now and records every module's availability.
// The server calls it for the local host at startup, which seeds the host's
// module rows. A failure, the client lookup's included, forgets the host's
// last Hello, so the next reach says Hello again.
func (h *Hosts) Hello(ctx context.Context, hostID string) error {
	unlock, err := h.lockHost(ctx, hostID)
	if err != nil {
		return err
	}
	defer unlock()
	c, gen, err := h.cfg.Clients.Client(ctx, hostID)
	if err != nil {
		h.forget(hostID)
		return err
	}
	return h.hello(ctx, hostID, c, gen)
}
```

Replace:

```go
// greetingOf returns hostID's last successful Hello in this process.
```

with:

```go
// forget drops hostID's last Hello: one that failed since no longer
// vouches for the agent.
func (h *Hosts) forget(hostID string) {
	h.mu.Lock()
	delete(h.greeted, hostID)
	h.mu.Unlock()
}

// greetingOf returns hostID's last successful Hello in this process.
```

Replace:

```go
// which only core.module.enable and core.module.disable change. On success
// it records the Hello as gen's.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient, gen uint64) error {
	hello, err := c.Hello(ctx)
```

with (the rest of the function body stays as it is; `err` is now the named result, so every existing `return` reaches the deferred check):

```go
// which only core.module.enable and core.module.disable change. On success
// it records the Hello as gen's; on any failure it forgets the host's last
// Hello.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient, gen uint64) (err error) {
	defer func() {
		if err != nil {
			h.forget(hostID)
		}
	}()
	hello, err := c.Hello(ctx)
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/dispatch/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/dispatch`)

- [ ] **Step 5: Write the failing test (95g)**

In `internal/controller/dispatch/apply_test.go`, change the import block's first group from:

```go
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
```

to:

```go
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
```

Insert after `TestApplyOfAnExpiredPlanIsExpired`:

```go
func TestNoChangeOnlyForRefusalsBeforeACommit(t *testing.T) {
	cases := []struct {
		err  error
		want bool
	}{
		{errcode.New(errcode.PlanNotFound, "no plan p1"), true},
		{errcode.New(errcode.PlanExpired, "the plan expired"), true},
		{errcode.New(errcode.InvalidInput, "bad confirm timeout"), true},
		{errcode.New(errcode.PlanConfirmationNeeded, "type the phrase"), true},
		{errcode.New(errcode.PlanGuardFailed, "guard g failed"), true},
		{errcode.New(errcode.ModuleUnavailable, "module kv is unavailable"), true},
		{errcode.New(errcode.LockHeld, "lock kv is held"), true},
		{errcode.New(errcode.ActionUnknown, "unknown action"), true},
		{errcode.New(errcode.PlanStateChanged, "the state changed"), true},
		{fmt.Errorf("agent: apply: %w", errcode.New(errcode.PlanExpired, "the plan expired")), true},
		{errcode.New(errcode.Internal, ""), false},
		{errcode.New(errcode.PlanPrincipalMismatch, "another principal"), false},
		{errcode.New(errcode.Unavailable, "shutting down"), false},
		{errcode.New(errcode.HostOffline, "host h1 is offline"), false},
		{errcode.New(errcode.ChangeNotPending, "change c1 is confirmed"), false},
		{errors.New("connection reset"), false},
	}
	for _, c := range cases {
		if got := noChange(c.err); got != c.want {
			t.Errorf("noChange(%v) = %v, want %v", c.err, got, c.want)
		}
	}
}
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/dispatch/ -run '^TestNoChangeOnlyForRefusalsBeforeACommit$' -count=1`
Expected: FAIL with `noChange(unavailable: shutting down) = true, want false`, `noChange(host.offline: host h1 is offline) = true, want false` and `noChange(change.not_pending: change c1 is confirmed) = true, want false`.

- [ ] **Step 7: Write the implementation (95g)**

In `internal/controller/dispatch/apply.go`, replace:

```go
// noChange reports whether err shows that the apply recorded no change. A lost
// reply or an internal error may follow a committed change, and
// plan.principal_mismatch is checked before the plan's change is looked up, so
// another principal's concurrent apply may hold one.
func noChange(err error) bool {
	c := errcode.From(err).Code
	return c != errcode.Internal && c != errcode.PlanPrincipalMismatch
}
```

with:

```go
// noChange reports whether err is a refusal the engines answer before any
// change is committed, so the plan may be unmarked. Every other error keeps
// it marked: a lost reply or an internal error may follow a committed
// change, plan.principal_mismatch is checked before the plan's change is
// looked up, so another principal's concurrent apply may hold one, and a code
// not listed here is not known to precede a commit.
func noChange(err error) bool {
	switch errcode.From(err).Code {
	case errcode.PlanNotFound, errcode.PlanExpired, errcode.InvalidInput, errcode.PlanConfirmationNeeded,
		errcode.PlanGuardFailed, errcode.ModuleUnavailable, errcode.LockHeld, errcode.ActionUnknown,
		errcode.PlanStateChanged:
		return true
	}
	return false
}
```

The call site `if err != nil && v.ID == "" && claimed && noChange(err)` stays unchanged: the claimed and empty-view conditions are kept.

- [ ] **Step 8: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/dispatch/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/dispatch`)

- [ ] **Step 9: Write the failing test (95h)**

In `internal/controller/dispatch/apply_test.go`, add `"strings"` to the imports between `"log/slog"` and `"sync/atomic"`:

```go
	"log/slog"
	"strings"
	"sync/atomic"
```

Then insert after `TestNoChangeOnlyForRefusalsBeforeACommit`:

```go
// misattributed is an agent whose apply commits the change and answers with
// its view altered by edit.
type misattributed struct {
	core.AgentClient
	edit func(*core.ChangeView)
}

func (c misattributed) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	v, err := c.AgentClient.Apply(ctx, p, planID, opts)
	if err == nil {
		c.edit(&v)
	}
	return v, err
}

// ingestCalls records the hosts it is asked to ingest.
type ingestCalls struct{ hosts []string }

func (c *ingestCalls) IngestHost(_ context.Context, hostID string) error {
	c.hosts = append(c.hosts, hostID)
	return nil
}

func TestApplyRefusesAViewOfAnotherHostOrAction(t *testing.T) {
	cases := []struct {
		name string
		edit func(*core.ChangeView)
		want string
	}{
		{"another host", func(v *core.ChangeView) { v.HostID = "h2" }, "answered for host h2"},
		{"another action", func(v *core.ChangeView) { v.Action = refGet.Name }, "of kv.item.get/v1"},
		{"another version", func(v *core.ChangeView) { v.Version = 2 }, "of kv.item.set/v2"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, d := newDispatcher(t)
			calls := &ingestCalls{}
			d.cfg.Ingest = calls
			pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
			h.clients.Add(testHost, misattributed{AgentClient: h.rt, edit: c.edit})
			v, err := d.Apply(context.Background(), alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
			if err == nil || v.ID != "" || !strings.Contains(err.Error(), c.want) {
				t.Errorf("Apply = %+v, %v; want a refusal naming %q", v, err, c.want)
			}
			if n := h.count("SELECT count(*) FROM core_change_index"); n != 0 {
				t.Errorf("index rows = %d, want none for a refused view", n)
			}
			if len(calls.hosts) != 0 {
				t.Errorf("ingested %v, want nothing for a refused view", calls.hosts)
			}
		})
	}
}
```

- [ ] **Step 10: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/dispatch/ -run '^TestApplyRefusesAViewOfAnotherHostOrAction$' -count=1`
Expected: FAIL in all three subtests with `want a refusal naming ...` (Apply returns the view and a nil error), `index rows = 1, want none for a refused view` and `ingested [h1], want nothing for a refused view`.

- [ ] **Step 11: Write the implementation (95h)**

In `internal/controller/dispatch/apply.go`, replace the end of `Apply`'s doc comment:

```go
// version, are plan.not_found (register row 69, obligation 1): the caller
// authorized a, so a plan of another action must not run. An agent apply also
// needs a's module to be usable on hostID; its change is recorded in
// core_change_index and the host's journal is ingested.
```

with:

```go
// version, are plan.not_found (register row 69, obligation 1): the caller
// authorized a, so a plan of another action must not run. An agent apply also
// needs a's module to be usable on hostID; its change is recorded in
// core_change_index and the host's journal is ingested. A returned view of
// another host, action or version is refused before it is recorded.
```

Replace:

```go
		return v, err
	}()
	if v.ID != "" && def.Spec.Side == core.SideAgent {
```

with (the `if v.ID != "" && def.Spec.Side == core.SideAgent` block that indexes and ingests stays exactly as it is):

```go
		return v, err
	}()
	if v.ID != "" {
		if refused := checkApplied(v, hostID, a); refused != nil {
			return core.ChangeView{}, refused
		}
	}
	if v.ID != "" && def.Spec.Side == core.SideAgent {
```

Insert directly above the `// noChange reports whether err is a refusal ...` comment:

```go
// checkApplied refuses the view of an apply that names another host, action
// or version than the apply was authorized for, as checkChange does for the
// other change calls.
func checkApplied(v core.ChangeView, hostID string, a core.ActionRef) error {
	if v.Action != a.Name || v.Version != a.Version {
		return fmt.Errorf("dispatch: apply of %s on host %s answered as change %s of %s/v%d",
			a, hostID, v.ID, v.Action, v.Version)
	}
	_, err := checkHost(v, hostID)
	return err
}
```

`checkHost` is the existing helper in `changes.go`; a controller-side view has the empty host id the controller-side apply asks for, so it passes.

- [ ] **Step 12: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/dispatch/ -count=1 && timeout 300 go vet ./internal/controller/dispatch/`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/dispatch`), no vet output.

- [ ] **Step 13: Commit**

```bash
git add internal/controller/dispatch/hosts.go internal/controller/dispatch/apply.go internal/controller/dispatch/hosts_test.go internal/controller/dispatch/apply_test.go
git commit -m "fix(dispatch): harden Hello and apply checks"
```

### Task 10: Audit journal sequence check

**Files:**
- Modify: `internal/controller/audit/audit.go` (`ingest`)
- Test: `internal/controller/audit/audit_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing test (95c)**

In `internal/controller/audit/audit_test.go`, change the standard-library imports from:

```go
	"errors"
	"path/filepath"
	"reflect"
	"testing"
```

to:

```go
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
```

Insert after `TestIngestDeduplicatesByHostAndSeq`:

```go
func TestIngestRefusesANonPositiveSequence(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	for _, seq := range []int64{0, -1} {
		err := s.Ingest(ctx, "h1", []core.JournalEntry{journalEntry(1, t0), journalEntry(seq, t0)})
		if err == nil || !strings.Contains(err.Error(), fmt.Sprintf("sequence %d", seq)) {
			t.Errorf("Ingest of sequence %d = %v, want a refusal", seq, err)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit"); n != 0 {
		t.Errorf("rows after the refusals = %d, want 0: a refused batch stores nothing", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/audit/ -run '^TestIngestRefusesANonPositiveSequence$' -count=1`
Expected: FAIL with `Ingest of sequence 0 = <nil>, want a refusal`, `Ingest of sequence -1 = <nil>, want a refusal` and `rows after the refusals = 3, want 0`.

- [ ] **Step 3: Write the implementation (95c)**

In `internal/controller/audit/audit.go`, replace:

```go
func (s *Store) ingest(ctx context.Context, tx *sql.Tx, hostID, epoch string, entries []core.JournalEntry) error {
	for _, e := range entries {
		if err := s.insert(ctx, tx, fromJournal(hostID, e), epoch, true); err != nil {
```

with:

```go
func (s *Store) ingest(ctx context.Context, tx *sql.Tx, hostID, epoch string, entries []core.JournalEntry) error {
	for _, e := range entries {
		// insert stores a zero sequence as NULL, which the (host_id, epoch,
		// seq) index never deduplicates.
		if e.Seq <= 0 {
			return fmt.Errorf("audit: a journal entry of host %s has sequence %d; sequences start at 1", hostID, e.Seq)
		}
		if err := s.insert(ctx, tx, fromJournal(hostID, e), epoch, true); err != nil {
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/audit/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/audit`)

- [ ] **Step 5: Commit**

```bash
git add internal/controller/audit/audit.go internal/controller/audit/audit_test.go
git commit -m "fix(audit): refuse journal sequences below 1"
```

### Task 11: API panic headers and listeners HEAD

**Files:**
- Modify: `internal/controller/api/middleware.go` (imports, `recoverPanics`)
- Test: `internal/controller/api/wire_test.go`
- Test: `internal/controller/api/meta_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3

- [ ] **Step 1: Write the failing test (95k)**

In `internal/controller/api/wire_test.go`, insert after `TestRecoverPanicsDropsCookies` (no import changes):

```go
// TestRecoverPanicsRestoresTheHeaders: the internal answer carries the
// headers set before the handler ran and none the handler added, changed or
// removed.
func TestRecoverPanicsRestoresTheHeaders(t *testing.T) {
	log, _ := testLog()
	h := securityHeaders(recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-Partial-Result", "half")
		w.Header().Set("Content-Security-Policy", "default-src *")
		w.Header().Del("Cache-Control")
		panic("boom after the headers")
	})))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/v1/changes", nil))
	decodeProblem(t, rec, 500, errcode.Internal)
	got := rec.Result().Header
	if v := got.Values("X-Partial-Result"); len(v) != 0 {
		t.Errorf("the panicking handler's header was sent: %q", v)
	}
	for name, want := range map[string]string{
		"Content-Security-Policy": ContentSecurityPolicy,
		"Cache-Control":           "no-store",
		"X-Content-Type-Options":  "nosniff",
	} {
		if v := got.Get(name); v != want {
			t.Errorf("%s = %q, want %q", name, v, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/api/ -run '^TestRecoverPanicsRestoresTheHeaders$' -count=1`
Expected: FAIL with `the panicking handler's header was sent: ["half"]`, `Content-Security-Policy = "default-src *", want ...` and `Cache-Control = "", want "no-store"`.

- [ ] **Step 3: Write the implementation (95k)**

In `internal/controller/api/middleware.go`, replace the imports:

```go
	"log/slog"
	"net/http"
```

with:

```go
	"log/slog"
	"maps"
	"net/http"
```

In `recoverPanics`'s doc comment, replace:

```go
// answer for a complete one. The internal answer drops any Set-Cookie the
// handler set, so a handler that opened a session and then panicked hands
// out no session. As in lifecycle.Protect, the panic value is logged only
```

with:

```go
// answer for a complete one. The internal answer carries the headers as they
// were before the handler ran, so a handler that opened a session and then
// panicked hands out no session, and no header it set, changed or removed
// reaches the client. As in lifecycle.Protect, the panic value is logged only
```

Replace:

```go
		tw := &trackingWriter{ResponseWriter: w}
		defer func() {
```

with:

```go
		tw := &trackingWriter{ResponseWriter: w}
		before := w.Header().Clone()
		defer func() {
```

Replace:

```go
			w.Header().Del("Set-Cookie")
			writeProblem(w, log, errcode.New(errcode.Internal, ""))
```

with:

```go
			h := w.Header()
			clear(h)
			maps.Copy(h, before)
			writeProblem(w, log, errcode.New(errcode.Internal, ""))
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/api/ -count=1 && timeout 300 go vet ./internal/controller/api/`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/api`; `TestRecoverPanicsDropsCookies` still passes), no vet output.

- [ ] **Step 5: Write the test (95l)**

This item adds missing coverage for behaviour that already holds, so the test passes as soon as it is written.

In `internal/controller/api/meta_test.go`, change the standard-library imports from:

```go
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
```

to:

```go
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
```

Insert after `TestListenersRoute`:

```go
// TestListenersRouteAnswersHEAD runs on a real server, because a
// ResponseRecorder keeps the body net/http drops from a HEAD response.
func TestListenersRouteAnswersHEAD(t *testing.T) {
	h := newHarness(t)
	srv := httptest.NewServer(h.srv.Handler())
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, srv.URL+"/api/v1/controller/listeners", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+h.token(core.ScopeRead))
	client := &http.Client{Timeout: 10 * time.Second}
	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = res.Body.Close() }()
	body, err := io.ReadAll(res.Body)
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK || len(body) != 0 || res.Header.Get("Cache-Control") != "no-store" {
		t.Errorf("HEAD = %d with %d body bytes and Cache-Control %q; want 200, no body, no-store",
			res.StatusCode, len(body), res.Header.Get("Cache-Control"))
	}
}
```

- [ ] **Step 6: Run the test**

Run: `timeout 300 go test ./internal/controller/api/ -run '^TestListenersRouteAnswersHEAD$' -count=1 -v`
Expected: PASS (`--- PASS: TestListenersRouteAnswersHEAD`). This is a coverage item: the route already answers HEAD, so there is no failing run.

- [ ] **Step 7: Run the package's tests**

Run: `timeout 300 go test ./internal/controller/api/ -count=1`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/controller/api`)

- [ ] **Step 8: Commit**

```bash
git add internal/controller/api/middleware.go internal/controller/api/wire_test.go internal/controller/api/meta_test.go
git commit -m "fix(api): restore headers after a handler panic"
```

### Task 12: Audit page secret test

**Files:**
- Test: `web/src/pages/audit.test.tsx`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 117

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

- [ ] **Step 1: Write the test**

Append at the end of `web/src/pages/audit.test.tsx`, after the test `"shows the server's reason when the read fails"`:

```tsx

test("never renders an entry's input, so a token in it stays hidden", async () => {
  const secret = "dpt_0123456789abcdef0123456789abcdef"
  stubFetch({
    [AUDIT]: { body: { output: { entries: [entry("a3", "demo.set", { hostId: "h1", input: { token: secret } })], nextBefore: null } } },
  })
  const { container } = renderWithProviders(<AuditPage />)
  await screen.findByText("demo.set/v1")
  expect(container.textContent).not.toContain("dpt_")
  expect(container.innerHTML).not.toContain(secret)
})
```

- [ ] **Step 2: Run the test and confirm it passes**

Run: `timeout 300 pnpm -C web exec vitest run src/pages/audit.test.tsx`
Expected: PASS (`Tests  5 passed (5)`). This test guards against a regression, so it passes against the current code: `AuditBody` in `web/src/pages/audit.tsx` renders only `at`, `actor`, `hostId`, `action`, `version`, `phase`, `result` and `errorCode`, and never `input`. A change that rendered the input (for example appending `JSON.stringify(e.input)` to the actor cell) fails it with `expected '…' not to contain 'dpt_'`.

- [ ] **Step 3: Run the whole suite**

Run: `timeout 300 pnpm -C web exec vitest run && timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: PASS (`Test Files  14 passed (14)`), typecheck and lint exit 0

- [ ] **Step 4: Commit**

```bash
git add web/src/pages/audit.test.tsx
git commit -m "test(web): check the audit page hides secrets"
```

### Task 13: Audit ingestion health

**Files:**
- Create: `internal/core/store/migrations/controller/0008_ingest_status.sql`
- Modify: `internal/controller/audit/ingest.go` (`Ingester` struct and doc, `NewIngester`, `IngestHost`, `IngestAll`, `ingestAll`, `ingestHost`, `ingest`; new `errOffline`, `ingestState`, `report`, `Store.recordIngestion`)
- Test: `internal/controller/audit/ingest_test.go`
- Test: `internal/controller/audit/epoch_test.go`

**Interfaces:**
- Consumes: none
- Produces: Contracts "Ingestion status table"

**Items:** 95

**Depends on:** Task 10

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`IngestHost` now always returns nil and `IngestAll` returns only a failure to list the hosts: the Ingester reports per-host failures itself, so the dispatcher's call stays unchanged and its own warning no longer fires for ingestion failures.

- [ ] **Step 1: Write the failing tests (95d reporting and status table)**

In `internal/controller/audit/ingest_test.go`, change the import block's first group from:

```go
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
```

to:

```go
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
```

In `TestIngestRefusesASequenceThatDoesNotAdvance`, replace:

```go
	err := e.ingester.IngestHost(ctx, "h1")
	if err == nil || !strings.Contains(err.Error(), "sequence 1 after 1") {
		t.Errorf("IngestHost = %v, want a refused sequence", err)
	}
}
```

with:

```go
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatalf("IngestHost = %v, want the failure reported, not returned", err)
	}
	if got := ingestStatus(t, e.db, "h1"); !strings.Contains(got, "sequence 1 after 1") {
		t.Errorf("ingestion status = %q, want a refused sequence", got)
	}
}
```

In `TestIngestAllContinuesPastAFailingHost`, replace:

```go
	err := e.ingester.IngestAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "host h1") {
		t.Errorf("IngestAll = %v, want the error of h1", err)
	}
```

with:

```go
	if err := e.ingester.IngestAll(context.Background()); err != nil {
		t.Fatalf("IngestAll = %v, want each host's failure reported, not returned", err)
	}
	if got, want := ingestStatus(t, e.db, "h1"), "2026-10-17T12:00:00.000000000Z audit: ingest host h1: not_found: host h1 not found"; got != want {
		t.Errorf("h1 status = %q, want %q", got, want)
	}
	if got, want := ingestStatus(t, e.db, "h2"), "2026-10-17T12:00:00.000000000Z <null>"; got != want {
		t.Errorf("h2 status = %q, want %q", got, want)
	}
```

Insert after `TestIngestAllContinuesPastAFailingHost` (it uses `logRecords` from `epoch_test.go`, same package):

```go
// ingestStatus is hostID's core_ingest_status row as "<since> <error>", with
// <null> for a NULL error, or "<none>" when the host has no row.
func ingestStatus(t *testing.T, db *store.DB, hostID string) string {
	t.Helper()
	var since string
	var msg sql.NullString
	err := db.Read().QueryRow("SELECT since, error FROM core_ingest_status WHERE host_id = ?", hostID).Scan(&since, &msg)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "<none>"
	case err != nil:
		t.Fatalf("ingestion status of %s: %v", hostID, err)
	}
	if !msg.Valid {
		return since + " <null>"
	}
	return since + " " + msg.String
}

// TestIngestReportsEachStateChange: a host's ingestion state is stored and
// logged when it changes, whether the loop or the dispatcher ran the
// ingestion; a repeated failure is logged at debug and keeps the row and its
// since.
func TestIngestReportsEachStateChange(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	var fail error
	e.hosts["h1"] = &coretest.FakeAgent{JournalFunc: func(context.Context, int64, int) ([]core.JournalEntry, error) {
		return nil, fail
	}}
	logs := &logRecords{}
	in := NewIngester(IngesterConfig{Store: e.store, Agents: e.hosts, Clock: e.clock, Log: slog.New(logs)})
	scheduled := func(ctx context.Context) error { return in.IngestAll(ctx) }
	dispatched := func(ctx context.Context) error { return in.IngestHost(ctx, "h1") }
	unreachable, refused := errors.New("agent unreachable"), errors.New("agent refused")
	const failed = "audit: ingest host h1: audit: read the journal of h1: "
	steps := []struct {
		name   string
		run    func(context.Context) error
		fail   error
		logs   []string
		status string
	}{
		{"first success", scheduled, nil, nil, "2026-10-17T12:00:01.000000000Z <null>"},
		{"first failure", dispatched, unreachable, []string{"WARN audit: journal ingestion failing host=h1"},
			"2026-10-17T12:00:02.000000000Z " + failed + "agent unreachable"},
		{"the same failure", scheduled, unreachable, []string{"DEBUG audit: journal ingestion still failing host=h1"},
			"2026-10-17T12:00:02.000000000Z " + failed + "agent unreachable"},
		{"another failure", dispatched, refused, []string{"WARN audit: journal ingestion failing host=h1"},
			"2026-10-17T12:00:04.000000000Z " + failed + "agent refused"},
		{"recovery", scheduled, nil, []string{"INFO audit: journal ingestion recovered host=h1"},
			"2026-10-17T12:00:05.000000000Z <null>"},
		{"another success", dispatched, nil, nil, "2026-10-17T12:00:05.000000000Z <null>"},
	}
	for _, s := range steps {
		e.clock.Advance(time.Second)
		fail = s.fail
		before := len(logs.lines())
		if err := s.run(context.Background()); err != nil {
			t.Fatalf("%s: %v, want the result reported, not returned", s.name, err)
		}
		if got := logs.lines()[before:]; !slices.Equal(got, s.logs) {
			t.Errorf("%s: logs = %q, want %q", s.name, got, s.logs)
		}
		if got := ingestStatus(t, e.db, "h1"); got != s.status {
			t.Errorf("%s: status = %q, want %q", s.name, got, s.status)
		}
	}
}
```

In `internal/controller/audit/epoch_test.go`, in `TestIngestDeadline`, replace:

```go
	case err := <-done:
		if err == nil || err.Error() != "audit: ingest host h1: audit: ingestion exceeded 30s" {
			t.Errorf("IngestAll = %v, want h1's deadline error", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IngestAll did not return after the deadline")
	}
```

with:

```go
	case err := <-done:
		if err != nil {
			t.Errorf("IngestAll = %v, want h1's deadline reported, not returned", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("IngestAll did not return after the deadline")
	}
	if got, want := ingestStatus(t, e.db, "h1"), "2026-10-17T12:00:30.000000000Z audit: ingest host h1: audit: ingestion exceeded 30s"; got != want {
		t.Errorf("h1 status = %q, want %q", got, want)
	}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 go test ./internal/controller/audit/ -run '^(TestIngestRefusesASequenceThatDoesNotAdvance|TestIngestAllContinuesPastAFailingHost|TestIngestReportsEachStateChange|TestIngestDeadline)$' -count=1`
Expected: FAIL: `IngestHost = audit: the journal of h1 returned sequence 1 after 1, want the failure reported, not returned`; `IngestAll = audit: ingest host h1: not_found: host h1 not found, want each host's failure reported, not returned`; `IngestAll = audit: ingest host h1: audit: ingestion exceeded 30s, want h1's deadline reported, not returned`; `TestIngestReportsEachStateChange` fails on `no such table: core_ingest_status`.

- [ ] **Step 3: Write the implementation (95d)**

Create `internal/core/store/migrations/controller/0008_ingest_status.sql`:

```sql
-- Journal ingestion health per host, read and written only through
-- internal/controller/audit and read by core.host.list. since is fixed-width
-- RFC 3339 UTC text: when the current state began. error is NULL while
-- ingestion succeeds. A host without a row has not been ingested yet.

CREATE TABLE core_ingest_status (
    host_id TEXT PRIMARY KEY REFERENCES core_hosts (id) ON DELETE CASCADE,
    since TEXT NOT NULL,
    error TEXT
) STRICT;
```

In `internal/controller/audit/ingest.go`, make these replacements.

Replace:

```go
// errIngestTimeout ends a host's ingestion that IngestTimeout cut short.
var errIngestTimeout = fmt.Errorf("audit: ingestion exceeded %s", IngestTimeout)
```

with:

```go
// errIngestTimeout ends a host's ingestion that IngestTimeout cut short.
var errIngestTimeout = fmt.Errorf("audit: ingestion exceeded %s", IngestTimeout)

// errOffline ends the ingestion of a host that answered host.offline: it is
// skipped, and its ingestion state stays as it was.
var errOffline = errors.New("audit: host offline")
```

Replace:

```go
// ackKey names one journal: a host's sequence starts again in a new epoch.
type ackKey struct{ host, epoch string }
```

with:

```go
// ackKey names one journal: a host's sequence starts again in a new epoch.
type ackKey struct{ host, epoch string }

// ingestState is the result of a host's last ingestion: failing with
// message, or succeeding.
type ingestState struct {
	failing bool
	message string
}
```

Replace:

```go
// host.offline is skipped, and each host's ingestion ends after
// IngestTimeout on the configured clock. Its methods are safe for concurrent
// use; ingestion runs one at a time.
```

with:

```go
// host.offline is skipped, and each host's ingestion ends after
// IngestTimeout on the configured clock. Each host's result is reported, in
// core_ingest_status and the log, whichever method ran it. Its methods are
// safe for concurrent use; ingestion runs one at a time.
```

Replace:

```go
	acked   map[ackKey]int64 // the last sequence acknowledged per journal by this process
```

with:

```go
	acked   map[ackKey]int64       // the last sequence acknowledged per journal by this process
	states  map[string]ingestState // each host's last result stored by this process
```

Replace:

```go
	return &Ingester{cfg: cfg, acked: map[ackKey]int64{}}
```

with:

```go
	return &Ingester{cfg: cfg, acked: map[ackKey]int64{}, states: map[string]ingestState{}}
```

Replace:

```go
// IngestHost ingests hostID's journal now; the dispatcher calls it after each
// agent apply, confirm and revert. After Stop it does nothing.
func (in *Ingester) IngestHost(ctx context.Context, hostID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return nil
	}
	return in.ingestHost(ctx, hostID)
}

// IngestAll ingests the journal of every host in core_hosts. A host that
// fails does not stop the others; their errors are joined. After Stop it
// does nothing.
```

with:

```go
// IngestHost ingests hostID's journal now; the dispatcher calls it after each
// agent apply, confirm and revert. It returns nil: a failure is reported as
// the loop reports it, and the loop retries it. After Stop it does nothing.
func (in *Ingester) IngestHost(ctx context.Context, hostID string) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if !in.stopped {
		in.ingestHost(ctx, hostID)
	}
	return nil
}

// IngestAll ingests the journal of every host in core_hosts. A host that
// fails does not stop the others, and its failure is reported, not returned;
// the error is a failure to list the hosts. After Stop it does nothing.
```

Replace (the end of `ingestAll`, all of `ingestHost`, and the start of `ingest`):

```go
	var errs []error
	for _, id := range hosts {
		if err := in.ingestHost(ctx, id); err != nil {
			if !errors.Is(err, errIngestTimeout) {
				err = fmt.Errorf("audit: ingest host %s: %w", id, err)
			}
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// ingestHost runs under mu. The deadline runs on the configured clock, so
// tests reach it without waiting.
func (in *Ingester) ingestHost(ctx context.Context, hostID string) error {
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := in.cfg.Clock.AfterFunc(IngestTimeout, func() { cancel(errIngestTimeout) })
	defer timer.Stop()
	err := in.ingest(ctx, hostID)
	if cause := context.Cause(ctx); err != nil && errors.Is(cause, errIngestTimeout) {
		return fmt.Errorf("audit: ingest host %s: %w", hostID, cause)
	}
	return err
}

func (in *Ingester) ingest(ctx context.Context, hostID string) error {
	agent, err := in.cfg.Agents.Agent(ctx, hostID)
	if errcode.From(err).Code == errcode.HostOffline {
		in.cfg.Log.Debug("audit: offline host skipped", "host", hostID)
		return nil
	}
```

with:

```go
	for _, id := range hosts {
		in.ingestHost(ctx, id)
	}
	return nil
}

// ingestHost runs under mu and reports the result. The deadline runs on the
// configured clock, so tests reach it without waiting.
func (in *Ingester) ingestHost(ctx context.Context, hostID string) {
	run, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	timer := in.cfg.Clock.AfterFunc(IngestTimeout, func() { cancel(errIngestTimeout) })
	defer timer.Stop()
	err := in.ingest(run, hostID)
	if errors.Is(err, errOffline) {
		return
	}
	if err != nil {
		if cause := context.Cause(run); errors.Is(cause, errIngestTimeout) {
			err = cause
		}
		err = fmt.Errorf("audit: ingest host %s: %w", hostID, err)
	}
	in.report(context.WithoutCancel(ctx), hostID, err)
}

// report records hostID's ingestion result, err nil for a success. The log
// says when a host starts failing, fails differently or recovers; a repeated
// failure is logged at debug only, since the loop repeats it every
// IngestInterval. core_ingest_status is written only when the result differs
// from the last one this process stored; a write that fails is tried again
// with the next result.
func (in *Ingester) report(ctx context.Context, hostID string, err error) {
	next := ingestState{}
	if err != nil {
		next = ingestState{failing: true, message: err.Error()}
	}
	prev, seen := in.states[hostID]
	switch {
	case seen && next == prev:
		if next.failing {
			in.cfg.Log.Debug("audit: journal ingestion still failing", "host", hostID, "error", err)
		}
		return
	case next.failing:
		in.cfg.Log.Warn("audit: journal ingestion failing", "host", hostID, "error", err)
	case seen:
		in.cfg.Log.Info("audit: journal ingestion recovered", "host", hostID)
	}
	if werr := in.cfg.Store.recordIngestion(ctx, hostID, next, in.cfg.Clock.Now()); werr != nil {
		in.cfg.Log.Error("audit: record the journal ingestion state", "host", hostID, "error", werr)
		return
	}
	in.states[hostID] = next
}

func (in *Ingester) ingest(ctx context.Context, hostID string) error {
	agent, err := in.cfg.Agents.Agent(ctx, hostID)
	if errcode.From(err).Code == errcode.HostOffline {
		in.cfg.Log.Debug("audit: offline host skipped", "host", hostID)
		return errOffline
	}
```

Replace:

```go
// restart moves hostID's cursor to the start of the journal epoch epoch.
```

with:

```go
// recordIngestion stores hostID's ingestion state st as begun at since. A
// stored row with st's error already keeps its since, so a process that
// restarts during a failure does not move it.
func (s *Store) recordIngestion(ctx context.Context, hostID string, st ingestState, since time.Time) error {
	var msg any
	if st.failing {
		msg = st.message
	}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_ingest_status (host_id, since, error) VALUES (?, ?, ?)
			 ON CONFLICT (host_id) DO UPDATE SET since = excluded.since, error = excluded.error
			 WHERE core_ingest_status.error IS NOT excluded.error`,
			hostID, since.UTC().Format(timeFormat), msg)
		return err
	})
	if err != nil {
		return fmt.Errorf("audit: record the ingestion state of %s: %w", hostID, err)
	}
	return nil
}

// restart moves hostID's cursor to the start of the journal epoch epoch.
```

`tick` keeps its `Warn` on the error of `ingestAll`, which is now only a failure to list the hosts.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/audit/ ./internal/controller/dispatch/ ./internal/controller/server/ ./internal/core/store/ -count=1 && timeout 300 go vet ./internal/controller/audit/ && gofmt -l internal/controller/audit/`
Expected: PASS (`ok` for all four packages), no vet or gofmt output.

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/controller/0008_ingest_status.sql internal/controller/audit/ingest.go internal/controller/audit/ingest_test.go internal/controller/audit/epoch_test.go
git commit -m "fix(audit): report ingestion health per host"
```

### Task 14: Host list ingestion health

**Files:**
- Modify: `internal/core/fleet.go` (`Host`, new `IngestionStatus`)
- Modify: `internal/controller/coreactions/hosts.go` (`complete`, new `ingestion`)
- Modify: `schemas/core.host.create/v1.json`, `schemas/core.host.delete/v1.json`, `schemas/core.host.enroll-local/v1.json`, `schemas/core.host.list/v1.json`, `schemas/core.host.revoke/v1.json`, `schemas/core.host.token/v1.json` (regenerated by `make schemas`)
- Test: `internal/controller/coreactions/module_test.go`

**Interfaces:**
- Consumes: Contracts "Ingestion status table"
- Produces: Contracts "core.Host.Ingestion"

**Items:** 95

**Depends on:** Task 13

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

In `internal/controller/coreactions/module_test.go`, insert after `TestHostListReportsAgents`:

```go
// TestHostListReportsIngestion: a host the controller has not ingested yet
// has a null ingestion state, apart from a healthy one and a failing one.
func TestHostListReportsIngestion(t *testing.T) {
	e := newTestEnv(t)
	e.addHost("h1", "alpha", true)
	e.addHost("h2", "beta", false)
	e.addHost("h3", "gamma", false)
	failedAt := t0.Add(time.Minute)
	e.exec(`INSERT INTO core_ingest_status (host_id, since, error) VALUES ('h2', ?, NULL), ('h3', ?, ?)`,
		formatTime(t0), formatTime(failedAt), "audit: ingest host h3: agent unreachable")

	var got core.HostList
	if err := e.read(root, "core.host.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]*core.IngestionStatus{
		"h1": nil,
		"h2": {OK: true, Since: t0},
		"h3": {Since: failedAt, Error: ptr("audit: ingest host h3: agent unreachable")},
	}
	if len(got.Hosts) != len(want) {
		t.Fatalf("hosts = %+v, want %d", got.Hosts, len(want))
	}
	for _, h := range got.Hosts {
		if !reflect.DeepEqual(h.Ingestion, want[h.ID]) {
			t.Errorf("%s ingestion = %+v, want %+v", h.ID, h.Ingestion, want[h.ID])
		}
	}
}
```

In `TestHostListJSON`, replace:

```go
		`"agentVersion":null,"protocolVersion":null,"os":null,"arch":null,"supported":false,"modules":[` +
		`{"id":"demo","version":1,"enabled":true,"available":true,"reason":null,"checkedAt":"2026-10-17T00:00:00Z"}]}]}`
```

with:

```go
		`"agentVersion":null,"protocolVersion":null,"os":null,"arch":null,"supported":false,"modules":[` +
		`{"id":"demo","version":1,"enabled":true,"available":true,"reason":null,"checkedAt":"2026-10-17T00:00:00Z"}],` +
		`"ingestion":null}]}`
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 go test ./internal/controller/coreactions/ -run '^(TestHostListReportsIngestion|TestHostListJSON)$' -count=1`
Expected: FAIL (build) with `undefined: core.IngestionStatus` and `h.Ingestion undefined (type core.Host has no field or method Ingestion)`.

- [ ] **Step 3: Write the implementation**

In `internal/core/fleet.go`, replace the whole `Host` struct with the following (gofmt realigns every field for the longer type), and add `IngestionStatus` after it:

```go
type Host struct {
	ID              string           `json:"id" jsonschema_description:"Host id."`
	Name            string           `json:"name" jsonschema_description:"Host name."`
	IsLocal         bool             `json:"isLocal" jsonschema_description:"Whether the host runs this controller."`
	State           string           `json:"state" jsonschema:"enum=pending,enum=enrolled,enum=revoked" jsonschema_description:"Enrollment state: pending until the agent enrolls, revoked after core.host.revoke."`
	Online          bool             `json:"online" jsonschema_description:"Whether the host's agent has a live session with the controller."`
	LastSeenAt      *time.Time       `json:"lastSeenAt" jsonschema:"nullable" jsonschema_description:"When the agent last answered; null when it never has."`
	AgentVersion    *string          `json:"agentVersion" jsonschema:"nullable" jsonschema_description:"Version of the agent's binary; null until its first Hello."`
	ProtocolVersion *int             `json:"protocolVersion" jsonschema:"nullable" jsonschema_description:"Agent protocol version; null until its first Hello."`
	OS              *string          `json:"os" jsonschema:"nullable" jsonschema_description:"Operating system the agent reports; null until its first Hello."`
	Arch            *string          `json:"arch" jsonschema:"nullable" jsonschema_description:"CPU architecture the agent reports; null until its first Hello."`
	Supported       bool             `json:"supported" jsonschema_description:"Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions."`
	Modules         []ModuleState    `json:"modules" jsonschema_description:"Every compiled-in module's state on the host."`
	Ingestion       *IngestionStatus `json:"ingestion" jsonschema:"nullable" jsonschema_description:"Journal ingestion health; null until the controller first ingests the host's journal."`
}

// IngestionStatus is a host's journal ingestion health.
type IngestionStatus struct {
	OK    bool      `json:"ok" jsonschema_description:"Whether the last ingestion succeeded."`
	Since time.Time `json:"since" jsonschema_description:"When the current state began."`
	Error *string   `json:"error" jsonschema:"nullable" jsonschema_description:"The last ingestion error; null while ingestion succeeds."`
}
```

In `internal/controller/coreactions/hosts.go`, replace:

```go
// complete fills in the half of h that core_hosts does not store: whether
// its agent is online, whether its version is supported, and its modules.
func (m module) complete(ctx context.Context, env core.Env, q querier, h *core.Host) error {
	h.Online = online(ctx, env, h.ID)
	h.Supported = h.AgentVersion != nil && h.ProtocolVersion != nil &&
		(m.d.Version == "" || Supported(m.d.Version, *h.AgentVersion, *h.ProtocolVersion))
	var err error
	h.Modules, err = m.moduleStates(ctx, q, h.ID)
	return err
}
```

with:

```go
// complete fills in the half of h that core_hosts does not store: whether
// its agent is online, whether its version is supported, its modules and its
// journal ingestion health.
func (m module) complete(ctx context.Context, env core.Env, q querier, h *core.Host) error {
	h.Online = online(ctx, env, h.ID)
	h.Supported = h.AgentVersion != nil && h.ProtocolVersion != nil &&
		(m.d.Version == "" || Supported(m.d.Version, *h.AgentVersion, *h.ProtocolVersion))
	var err error
	if h.Modules, err = m.moduleStates(ctx, q, h.ID); err != nil {
		return err
	}
	h.Ingestion, err = ingestion(ctx, q, h.ID)
	return err
}

// ingestion reads hostID's core_ingest_status row; a host without one has
// not been ingested yet and has a nil state.
func ingestion(ctx context.Context, q querier, hostID string) (*core.IngestionStatus, error) {
	var since string
	var msg sql.NullString
	err := q.QueryRowContext(ctx, `SELECT since, error FROM core_ingest_status WHERE host_id = ?`, hostID).Scan(&since, &msg)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	case err != nil:
		return nil, fmt.Errorf("coreactions: read ingestion status: %w", err)
	}
	at, err := parseTime(since)
	if err != nil {
		return nil, err
	}
	return &core.IngestionStatus{OK: !msg.Valid, Since: at, Error: nullable(msg)}, nil
}
```

Regenerate the schema snapshots with the repository's command:

Run: `timeout 300 make schemas && git status --short schemas/`
Expected: exactly these six files modified: `schemas/core.host.create/v1.json`, `schemas/core.host.delete/v1.json`, `schemas/core.host.enroll-local/v1.json`, `schemas/core.host.list/v1.json`, `schemas/core.host.revoke/v1.json`, `schemas/core.host.token/v1.json`. Each gains an `ingestion` property (`oneOf` of `#/$defs/IngestionStatus` and null), `ingestion` in the Host `required` list, and an `IngestionStatus` definition; a new output field is allowed by ADR 0002.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 go test ./internal/controller/coreactions/ ./internal/modules/ ./schemas/ ./internal/core/schema/ ./internal/controller/api/ ./internal/controller/server/ -count=1 && gofmt -l internal/core internal/controller/coreactions`
Expected: PASS (`ok` for every package; `internal/modules` checks the committed snapshots are current), no gofmt output.

- [ ] **Step 5: Commit**

```bash
git add internal/core/fleet.go internal/controller/coreactions/hosts.go internal/controller/coreactions/module_test.go schemas/core.host.create/v1.json schemas/core.host.delete/v1.json schemas/core.host.enroll-local/v1.json schemas/core.host.list/v1.json schemas/core.host.revoke/v1.json schemas/core.host.token/v1.json
git commit -m "feat(hosts): report journal ingestion health"
```

### Task 15: Token dialog field errors and blank names

**Files:**
- Modify: `web/src/pages/create-token-dialog.tsx` (`CreateTokenDialog`, new `Failure` interface)
- Test: `web/src/pages/create-token-dialog.test.tsx`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 114

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

- [ ] **Step 1: Write the failing tests**

Append at the end of `web/src/pages/create-token-dialog.test.tsx`, after the test `"shows a refusal from the plan"`:

```tsx

test("lists the field errors of an invalid input", async () => {
  stubFetch({
    [PLAN]: {
      status: 422,
      body: {
        type: "about:blank", title: "Invalid input", status: 422, detail: "invalid request", code: "request.invalid_input",
        errors: [
          { path: "/name", message: "must not contain control characters" },
          { path: "/expiresInDays", message: "must be at most 365" },
        ],
      },
    },
  })
  renderWithProviders(<CreateTokenDialog />)
  const dialog = await fillAndCreate(userEvent.setup())
  expect((await within(dialog).findByText("invalid request")).textContent).toBe("invalid request")
  expect(within(dialog).getAllByRole("listitem").map((item) => item.textContent)).toEqual([
    "/name: must not contain control characters",
    "/expiresInDays: must be at most 365",
  ])
})

test("asks for a name when the name is blank and sends nothing", async () => {
  const calls = stubFetch({ [PLAN]: { body: plan } })
  renderWithProviders(<CreateTokenDialog />)
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Create token" }))
  const dialog = screen.getByRole("dialog")
  await user.type(within(dialog).getByLabelText("Name"), "   ")
  await user.click(within(dialog).getByRole("button", { name: "Create" }))
  expect((await within(dialog).findByRole("alert")).textContent).toBe("Enter a name for the token.")
  expect(calls).toHaveLength(0)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 pnpm -C web exec vitest run src/pages/create-token-dialog.test.tsx`
Expected: FAIL, 2 tests:
- `lists the field errors of an invalid input` with `TestingLibraryElementError: Unable to find an accessible element with the role "listitem"`
- `asks for a name when the name is blank and sends nothing` with `AssertionError: expected 'no stub for POST /api/v1/actions/core…' to be 'Enter a name for the token.'` (the blank name went to the server)

- [ ] **Step 3: Write the implementation**

In `web/src/pages/create-token-dialog.tsx`, replace

```tsx
import { TOKENS_QUERY_KEY, applyPlan, changeError, errorMessage, planAction } from "@/api/client"
import type { NewToken, Scope } from "@/api/types"
```

with

```tsx
import { ApiError, TOKENS_QUERY_KEY, applyPlan, changeError, errorMessage, planAction } from "@/api/client"
import type { FieldError, NewToken, Scope } from "@/api/types"
```

replace

```tsx
  { value: "admin", label: "Admin", description: "Also manages tokens and module enablement." },
]
```

with

```tsx
  { value: "admin", label: "Admin", description: "Also manages tokens and module enablement." },
]

interface Failure {
  message: string
  fields: FieldError[]
}
```

replace

```tsx
  const [error, setError] = useState<string | null>(null)
```

with

```tsx
  const [error, setError] = useState<Failure | null>(null)
```

replace

```tsx
    if (busy) return
    const form = new FormData(event.currentTarget)
    setBusy(true)
    setError(null)
    try {
      const plan = await planAction("core.token.create", 1, {
        name: String(form.get("name") ?? "").trim(),
        scope,
```

with

```tsx
    if (busy) return
    const form = new FormData(event.currentTarget)
    const name = String(form.get("name") ?? "").trim()
    if (name === "") {
      setError({ message: "Enter a name for the token.", fields: [] })
      return
    }
    setBusy(true)
    setError(null)
    try {
      const plan = await planAction("core.token.create", 1, {
        name,
        scope,
```

replace

```tsx
        setError(failure ?? "The controller returned no token.")
```

with

```tsx
        setError({ message: failure ?? "The controller returned no token.", fields: [] })
```

replace

```tsx
      setError(errorMessage(err))
```

with

```tsx
      setError({ message: errorMessage(err), fields: err instanceof ApiError ? err.fields : [] })
```

and replace

```tsx
                <AlertDescription>{error}</AlertDescription>
```

with

```tsx
                <AlertDescription>
                  <p>{error.message}</p>
                  {error.fields.length > 0 && (
                    <ul className="mt-1 list-disc pl-5">
                      {error.fields.map((field, i) => (
                        <li key={i}>{`${field.path}: ${field.message}`}</li>
                      ))}
                    </ul>
                  )}
                </AlertDescription>
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run && timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: PASS (`Test Files  14 passed (14)`), typecheck and lint exit 0

- [ ] **Step 5: Commit**

```bash
git add web/src/pages/create-token-dialog.tsx web/src/pages/create-token-dialog.test.tsx
git commit -m "fix(web): show token field errors and blank names"
```

### Task 16: Hosts sidebar highlight

**Files:**
- Create: `web/src/router/active-prefixes.ts` (`activeByPrefix`)
- Modify: `web/src/router/adapter-link.tsx` (`AdapterLink`)
- Test: `web/src/router/adapter.test.tsx`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 115

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 2 = 3

Design note from drafting, binding for this task:
- the ruling asks for the prefix table to act "combined with NavLink's isActive". That cannot work through NavLink alone in react-router 8.4.0. NavLink always writes `aria-current` from its own match and overrides any `aria-current` prop. Also, a NavLink to "/" never matches below "/", even with `end={false}`: its boundary check reads the character after "/". So a link that the table marks active is rendered as a plain react-router `Link` that sets `aria-current="page"` and the active class itself. NavLink still decides every other case.

- [ ] **Step 1: Write the failing test**

In `web/src/router/adapter.test.tsx`, insert this test directly after the test `"activeExact matches only the exact path"` (before `"useNavigate and usePathname follow the router"`). The `Links` fixture renders the Hosts link as the kit's sidebar does: `to="/"` with `activeExact`.

```tsx
test.each(["/hosts/h1", "/hosts/h1/hostinfo/summary"])("the Hosts link is active on the host page %s", (path) => {
  renderAt(path)
  const hosts = screen.getByRole("link", { name: "Hosts" })
  expect(hosts.getAttribute("aria-current")).toBe("page")
  expect(hosts.className).toBe("active")
  expect(screen.getByRole("link", { name: "Audit" }).getAttribute("aria-current")).toBeNull()
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 pnpm -C web exec vitest run src/router/adapter.test.tsx`
Expected: FAIL, both rows of `the Hosts link is active on the host page ...` with `AssertionError: expected null to be 'page'`

- [ ] **Step 3: Write the implementation**

Create `web/src/router/active-prefixes.ts`:

```ts
// The kit marks its sidebar links activeExact, so the pages that belong to a
// link but sit outside its path are listed here by the link's `to`.
const ACTIVE_PREFIXES: Readonly<Record<string, readonly string[]>> = {
  "/": ["/hosts/"],
}

export function activeByPrefix(to: string, pathname: string): boolean {
  return (ACTIVE_PREFIXES[to] ?? []).some((prefix) => pathname.startsWith(prefix))
}
```

Replace the whole of `web/src/router/adapter-link.tsx` with:

```tsx
import type { AnchorHTMLAttributes, Ref } from "react"
import { Link, NavLink, useLocation } from "react-router"
import type { RouterLinkProps } from "darkraise-ui/router"
import { activeByPrefix } from "./active-prefixes"

// darkraise-ui passes more than RouterLinkProps at run time (a tooltip
// trigger's handlers, ids and ref through asChild), so everything but the
// adapter's own props goes on to the anchor.
type AdapterLinkProps = RouterLinkProps & AnchorHTMLAttributes<HTMLAnchorElement> & { ref?: Ref<HTMLAnchorElement> }

// NavLink sets aria-current="page" on the active link, which darkraise-ui
// requires of every router adapter. NavLink always sets aria-current from its
// own match, so a link active by prefix is a plain Link that sets it itself.
export function AdapterLink({ to, className, activeClassName, activeExact, children, ...rest }: AdapterLinkProps) {
  const { pathname } = useLocation()
  const classes = (active: boolean) => [className, active ? activeClassName : undefined].filter(Boolean).join(" ") || undefined
  if (activeByPrefix(to, pathname)) {
    return (
      <Link {...rest} to={to} aria-current="page" className={classes(true)}>
        {children}
      </Link>
    )
  }
  return (
    <NavLink {...rest} to={to} end={activeExact} className={({ isActive }) => classes(isActive)}>
      {children}
    </NavLink>
  )
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run && timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: PASS (`Test Files  14 passed (14)`), typecheck and lint exit 0

- [ ] **Step 5: Commit**

```bash
git add web/src/router/active-prefixes.ts web/src/router/adapter-link.tsx web/src/router/adapter.test.tsx
git commit -m "fix(web): highlight Hosts on host pages"
```

### Task 17: Hosts page ingestion badge

**Files:**
- Modify: `web/src/api/types.ts` (`Host`)
- Modify: `web/src/pages/hosts.tsx` (`HostsBody` status cell)
- Test: `web/src/pages/hosts.test.tsx`
- Modify (fixtures for the new required field): `web/src/pages/host.test.tsx`, `web/src/app.test.tsx`

**Interfaces:**
- Consumes: Contracts "core.Host.Ingestion"
- Produces: none

**Items:** 95

**Depends on:** Task 14

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing web test**

In `web/src/pages/hosts.test.tsx`, replace the `hosts` fixture:

```tsx
const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, online: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
  },
  { id: "h2", name: "db-1", isLocal: false, online: false, modules: [] },
]
```

with:

```tsx
const hosts: Host[] = [
  {
    id: "h1", name: "web-1", isLocal: true, online: true,
    modules: [
      { id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null },
      { id: "docker", version: 1, enabled: true, available: false, reason: "docker is not installed", checkedAt: null },
      { id: "ufw", version: 1, enabled: false, available: true, reason: null, checkedAt: null },
    ],
    ingestion: { ok: true, since: "2026-10-21T08:00:00Z", error: null },
  },
  { id: "h2", name: "db-1", isLocal: false, online: false, modules: [], ingestion: null },
]
```

Insert before `test("says so when there are no hosts", ...)`:

```tsx
test("marks only a host whose journal ingestion fails, with the error as the title", async () => {
  const failing: Host = {
    id: "h3", name: "cache-1", isLocal: false, online: true, modules: [],
    ingestion: { ok: false, since: "2026-10-21T08:05:00Z", error: "audit: ingest host h3: agent unreachable" },
  }
  stubFetch({ [HOSTS]: { body: { output: { hosts: [...hosts, failing] } } } })
  renderWithProviders(<HostsPage />)
  const row = (await screen.findByRole("link", { name: "cache-1" })).closest("tr")
  if (row === null) throw new Error("no row")
  const status = within(row).getByText("Journal ingestion failing")
  expect(status.getAttribute("title")).toBe("audit: ingest host h3: agent unreachable")
  expect(screen.getAllByText("Journal ingestion failing")).toHaveLength(1)
})
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 pnpm -C web exec vitest run src/pages/hosts.test.tsx`
Expected: FAIL in `marks only a host whose journal ingestion fails, with the error as the title` with `Unable to find an element with the text: Journal ingestion failing`.

- [ ] **Step 3: Write the web implementation**

In `web/src/api/types.ts`, replace:

```ts
export interface Host { id: string; name: string; isLocal: boolean; online: boolean; modules: ModuleState[] }
```

with:

```ts
export interface Host { id: string; name: string; isLocal: boolean; online: boolean; modules: ModuleState[]; ingestion: { ok: boolean; since: string; error: string | null } | null }
```

In `web/src/pages/hosts.tsx`, replace:

```tsx
            <TableCell>{h.online ? "Online" : "Offline"}</TableCell>
```

with:

```tsx
            <TableCell>
              <div className="flex flex-col items-start gap-1">
                <span>{h.online ? "Online" : "Offline"}</span>
                {h.ingestion !== null && !h.ingestion.ok && (
                  <Badge variant="destructive" size="sm" title={h.ingestion.error ?? undefined}>
                    Journal ingestion failing
                  </Badge>
                )}
              </div>
            </TableCell>
```

The other `Host` fixtures need the now-required field. In `web/src/pages/host.test.tsx`, replace:

```tsx
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules }
```

with:

```tsx
  return { id: "h1", name: "web-1", isLocal: true, online: true, modules, ingestion: null }
```

In `web/src/app.test.tsx`, replace:

```tsx
  modules: [{ id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null }],
}
```

with:

```tsx
  modules: [{ id: "hostinfo", version: 1, enabled: true, available: true, reason: null, checkedAt: null }],
  ingestion: null,
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run src/pages/hosts.test.tsx src/pages/host.test.tsx src/app.test.tsx && timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: PASS (`Test Files  3 passed (3)`), typecheck and lint exit 0 with no output from tsc or eslint.

- [ ] **Step 5: Commit**

```bash
git add web/src/api/types.ts web/src/pages/hosts.tsx web/src/pages/hosts.test.tsx web/src/pages/host.test.tsx web/src/app.test.tsx
git commit -m "feat(web): show journal ingestion health"
```

### Task 18: Test hygiene review: core and controller packages

**Files:**
- Test (review all; modify only where a check finds a gap):
  - `internal/controller/api/actions_test.go`
  - `internal/controller/api/listeners_test.go`
  - `internal/controller/api/meta_test.go`
  - `internal/controller/api/wire_test.go`
  - `internal/controller/audit/audit_test.go`
  - `internal/controller/audit/ingest_test.go`
  - `internal/controller/auth/login_test.go`
  - `internal/controller/auth/service_test.go`
  - `internal/controller/coreactions/tokens_test.go`
  - `internal/controller/coreactions/users_test.go`
  - `internal/controller/dispatch/apply_test.go`
  - `internal/controller/dispatch/changes_test.go`
  - `internal/controller/dispatch/dispatch_test.go`
  - `internal/controller/dispatch/fixture_test.go`
  - `internal/controller/dispatch/hosts_test.go`
  - `internal/core/lifecycle/fixture_test.go`
  - `internal/core/lifecycle/guard_test.go`
  - `internal/core/lifecycle/journal_test.go`
  - `internal/core/lifecycle/recover_test.go`
  - `internal/core/lifecycle/source_test.go`
  - `internal/core/lifecycle/verify_test.go`
  - `internal/core/schema/redact_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** Task 5, Task 7, Task 9, Task 10, Task 11, Task 13

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 0 = 3

Plan `docs/dforge/plans/2026-10-18-m1-dispatch-cli-and-assembly.md` (Tasks 2-34) wrote these test files. Its review ledger listed test-hygiene gaps and was never committed, so this task re-derives them with the checklist below. Earlier tasks of this plan edited some of these files; work on them as they are on your branch.

**Checklist.** Fix each finding in the test file itself; this task changes no production code.

1. **HTTP client without a timeout:** `http.DefaultClient`, `&http.Client{}`, `http.Get` or `http.Post` in a test. Use a client with `Timeout: 10 * time.Second`, reusing one the file already builds.
2. **Resource not released on a failure path:** a server, listener, database, file handle or goroutine released only at the end of the test body, so an earlier `t.Fatal` leaks it. Register the release with `t.Cleanup` right after creation. `t.TempDir()` needs nothing.
3. **`t.Fatal` or `t.FailNow` off the test goroutine:** inside a goroutine the test started. Send the error on a channel and fail on the test goroutine, or call `t.Error` and return.
4. **Unbounded wait:** a channel receive, `wg.Wait()` or poll loop with no deadline. Wrap it in a `select` with `time.After(10 * time.Second)` that fails the test.
5. **Sleep used as synchronisation:** `time.Sleep` waiting for another goroutine to reach a state. Replace it with a channel, the fake clock (`coretest.FakeClock`) or a bounded poll.
6. **Assertion that passes on empty output:** `strings.Contains(out, x)` where `x` may be empty, or a negative `!strings.Contains` on output that is empty because the command failed. Also assert success and non-empty output.
7. **Case loop without subtests:** a `for _, tc := range cases` loop whose failures do not name the case. Wrap the body in `t.Run(tc.name, ...)`.

- [ ] **Step 1: Record the starting state**

Run: `timeout 900 go test -count=1 ./internal/controller/api/ ./internal/controller/audit/ ./internal/controller/auth/ ./internal/controller/coreactions/ ./internal/controller/dispatch/ ./internal/core/lifecycle/ ./internal/core/schema/`
Expected: PASS. If anything fails before you edit, stop and report it.

- [ ] **Step 2: Review every listed file against the checklist**

Read each file top to bottom. Write each finding as `<file>:<line> <check number> <what>` into `.superpowers/sdd/hygiene-18.txt` (untracked scratch that is never committed; create the directory if needed), and `<file> clean` for a file with none.

- [ ] **Step 3: Fix each finding**

Apply the checklist's fix, one file at a time, running that package after each file:

Run: `timeout 600 go test -count=1 ./<package>/`
Expected: PASS

- [ ] **Step 4: Run the packages under the race detector**

Run: `timeout 1500 go test -race -count=1 ./internal/controller/api/ ./internal/controller/audit/ ./internal/controller/auth/ ./internal/controller/coreactions/ ./internal/controller/dispatch/ ./internal/core/lifecycle/ ./internal/core/schema/`
Expected: PASS

- [ ] **Step 5: Commit**

Add each modified test file by name. The commit body lists the findings from `.superpowers/sdd/hygiene-18.txt`, one per line, wrapped at 72 characters.

```bash
git add <each modified _test.go file>
git commit -m "test(core): fix test hygiene gaps" -m "<findings>"
```

### Task 19: Test hygiene review: CLI, agent runtime and server sites

**Files:**
- Test (review all; modify only where a check finds a gap):
  - `internal/agent/runtime/client_test.go`
  - `internal/agent/runtime/fixture_test.go`
  - `internal/agent/runtime/runtime_test.go`
  - `internal/cli/change_test.go`
  - `internal/cli/client_test.go`
  - `internal/cli/controller_test.go`
  - `internal/cli/enroll_test.go`
  - `internal/cli/fixture_test.go`
  - `internal/cli/input_test.go`
  - `internal/cli/mutate_test.go`
  - `internal/cli/names_test.go`
  - `internal/cli/output_test.go`
  - `internal/cli/pin_test.go`
  - `internal/cli/root_test.go`
  - `internal/controller/server/listeners_test.go`
  - `internal/controller/server/e2e_test.go`
  - `internal/controller/server/server_test.go`
  - `internal/controller/listen/service_test.go`

**Interfaces:**
- Consumes: none
- Produces: none

**Items:** 95

**Depends on:** Task 8

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 0 = 3

Plan `docs/dforge/plans/2026-10-18-m1-dispatch-cli-and-assembly.md` (Tasks 2-34) wrote the `cli` and `agent/runtime` test files. Its review ledger listed test-hygiene gaps and was never committed, so this task re-derives them with the checklist below. The four `server` and `listen` files carry known findings for check 1: `internal/controller/server/listeners_test.go:98,101,105` (`http.DefaultClient`), `internal/controller/server/e2e_test.go:89`, `internal/controller/server/server_test.go:445` and `internal/controller/listen/service_test.go:157`. Review those four files fully too. Line numbers may have moved; find the sites by their content.

**Checklist.** Fix each finding in the test file itself; this task changes no production code.

1. **HTTP client without a timeout:** `http.DefaultClient`, `&http.Client{}`, `http.Get` or `http.Post` in a test. Use a client with `Timeout: 10 * time.Second`, reusing one the file already builds (`internal/controller/server/listeners_test.go` builds one near its top).
2. **Resource not released on a failure path:** a server, listener, database, file handle or goroutine released only at the end of the test body, so an earlier `t.Fatal` leaks it. Register the release with `t.Cleanup` right after creation. `t.TempDir()` needs nothing.
3. **`t.Fatal` or `t.FailNow` off the test goroutine:** inside a goroutine the test started. Send the error on a channel and fail on the test goroutine, or call `t.Error` and return.
4. **Unbounded wait:** a channel receive, `wg.Wait()` or poll loop with no deadline. Wrap it in a `select` with `time.After(10 * time.Second)` that fails the test.
5. **Sleep used as synchronisation:** `time.Sleep` waiting for another goroutine to reach a state. Replace it with a channel, the fake clock (`coretest.FakeClock`) or a bounded poll.
6. **Assertion that passes on empty output:** `strings.Contains(out, x)` where `x` may be empty, or a negative `!strings.Contains` on output that is empty because the command failed. Also assert success and non-empty output.
7. **Case loop without subtests:** a `for _, tc := range cases` loop whose failures do not name the case. Wrap the body in `t.Run(tc.name, ...)`.

- [ ] **Step 1: Record the starting state**

Run: `timeout 900 go test -count=1 ./internal/cli/ ./internal/agent/runtime/ ./internal/controller/server/ ./internal/controller/listen/`
Expected: PASS. If anything fails before you edit, stop and report it.

- [ ] **Step 2: Review every listed file against the checklist**

Read each file top to bottom. Write each finding as `<file>:<line> <check number> <what>` into `.superpowers/sdd/hygiene-19.txt` (untracked scratch that is never committed; create the directory if needed), and `<file> clean` for a file with none.

- [ ] **Step 3: Fix each finding**

Apply the checklist's fix, one file at a time, running that package after each file:

Run: `timeout 600 go test -count=1 ./<package>/`
Expected: PASS

- [ ] **Step 4: Run the packages under the race detector**

Run: `timeout 1500 go test -race -count=1 ./internal/cli/ ./internal/agent/runtime/ ./internal/controller/server/ ./internal/controller/listen/`
Expected: PASS

- [ ] **Step 5: Commit**

Add each modified test file by name. The commit body lists the findings from `.superpowers/sdd/hygiene-19.txt`, one per line, wrapped at 72 characters.

```bash
git add <each modified _test.go file>
git commit -m "test(cli): fix test hygiene gaps" -m "<findings>"
```

### Task 20: Session generation

**Files:**
- Modify: `web/src/api/client.ts` (`setCsrfToken`, new `sessionGeneration`, `request`)
- Modify: `web/src/session/session.tsx` (`SessionProvider`)
- Test: `web/src/api/client.test.ts`
- Test: `web/src/session/session.test.tsx`

**Interfaces:**
- Consumes: none
- Produces: Contracts "sessionGeneration"

**Items:** 113

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests for stale session answers**

In `web/src/api/client.test.ts`, inside `describe("request", ...)`, insert this test directly after the existing test `"calls the unauthorized handler only for auth.required"` (before `"calls the CSRF-rejected handler only for auth.csrf"`):

```ts
  test("ignores auth.required for a request begun before the session changed", async () => {
    const handler = vi.fn()
    setUnauthorizedHandler(handler)
    setCsrfToken("csrf-old")
    let answer: (res: Response) => void = () => {}
    stub(() => new Promise<Response>((resolve) => {
      answer = resolve
    }))
    const pending = request("GET", "/api/v1/changes").catch((e: unknown) => e)
    setCsrfToken(null)
    setCsrfToken("csrf-new")
    answer(respond(401, { type: "about:blank", title: "Authentication required", status: 401, detail: "log in", code: "auth.required" }))
    expect(((await pending) as ApiError).code).toBe("auth.required")
    expect(handler).not.toHaveBeenCalled()
  })
```

In `web/src/session/session.test.tsx`, replace the first import line

```tsx
import { screen, waitFor } from "@testing-library/react"
```

with

```tsx
import { act, screen, waitFor } from "@testing-library/react"
```

and replace

```tsx
import { renderWithProviders, stubFetch } from "@/test/utils"
```

with

```tsx
import { renderWithProviders, stubFetch, type StubResponse } from "@/test/utils"
```

Then append at the end of the file, after the test `"a failing re-probe ends the session"`:

```tsx

function Switcher() {
  const { session, setSession } = useSession()
  return (
    <>
      <p data-testid="csrf">{session?.csrfToken ?? "signed out"}</p>
      <button onClick={() => setSession(sessionWith("csrf-a"))}>Log in as A</button>
      <button onClick={() => setSession(null)}>Log out</button>
      <button onClick={() => setSession(sessionWith("csrf-b"))}>Log in as B</button>
    </>
  )
}

test.each<[string, () => StubResponse]>([
  ["answers with the old session", () => ({ body: sessionWith("csrf-stale") })],
  ["answers auth.required", () => ({ status: 401, body: { type: "about:blank", title: "Auth", status: 401, detail: "log in", code: "auth.required" } })],
  ["cannot reach the controller", () => {
    throw new TypeError("Failed to fetch")
  }],
])("a session probe that %s after a logout and a new login leaves the new session", async (_, answer) => {
  let release: () => void = () => {}
  const gate = new Promise<void>((resolve) => {
    release = resolve
  })
  stubFetch({
    "GET /api/v1/auth/session": async () => {
      await gate
      return answer()
    },
  })
  renderWithProviders(
    <SessionProvider>
      <Switcher />
    </SessionProvider>,
  )
  const user = userEvent.setup()
  await user.click(screen.getByRole("button", { name: "Log in as A" }))
  await user.click(screen.getByRole("button", { name: "Log out" }))
  await user.click(screen.getByRole("button", { name: "Log in as B" }))
  expect(screen.getByTestId("csrf").textContent).toBe("csrf-b")

  await act(async () => {
    release()
    await new Promise((resolve) => setTimeout(resolve, 0))
  })
  expect(screen.getByTestId("csrf").textContent).toBe("csrf-b")
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 pnpm -C web exec vitest run src/api/client.test.ts src/session/session.test.tsx`
Expected: FAIL, 4 tests:
- `ignores auth.required for a request begun before the session changed` with `AssertionError: expected "vi.fn()" to not be called at all, but actually been called 1 times`
- `a session probe that answers with the old session ...` with `expected 'csrf-stale' to be 'csrf-b'`
- `a session probe that answers auth.required ...` and `a session probe that cannot reach the controller ...` with `expected 'signed out' to be 'csrf-b'`

- [ ] **Step 3: Write the implementation**

In `web/src/api/client.ts`, replace

```ts
let csrfToken: string | null = null
let unauthorizedHandler: (() => void) | null = null
let csrfRejectedHandler: (() => void) | null = null

export function setCsrfToken(token: string | null): void {
  csrfToken = token
}
```

with

```ts
let csrfToken: string | null = null
let generation = 0
let unauthorizedHandler: (() => void) | null = null
let csrfRejectedHandler: (() => void) | null = null

// Every login and logout sets the CSRF token and so starts a new session
// generation. An auth.required answer to a request begun in an earlier
// generation must not end the current session.
export function setCsrfToken(token: string | null): void {
  csrfToken = token
  generation++
}

export function sessionGeneration(): number {
  return generation
}
```

In `request`, replace

```ts
export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers: Record<string, string> = { Accept: "application/json", "Content-Type": "application/json" }
```

with

```ts
export async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const startedIn = generation
  const headers: Record<string, string> = { Accept: "application/json", "Content-Type": "application/json" }
```

and replace

```ts
    if (err.code === "auth.required") unauthorizedHandler?.()
```

with

```ts
    if (err.code === "auth.required" && startedIn === generation) unauthorizedHandler?.()
```

In `web/src/session/session.tsx`, replace

```tsx
import { request, setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
```

with

```tsx
import { request, sessionGeneration, setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
```

and replace

```tsx
    function probe() {
      probing = true
      request<Session>("GET", "/api/v1/auth/session")
        .then(
          (session) => {
            if (active) setSession(session)
          },
          () => {
            if (active) setSession(null)
          },
        )
```

with

```tsx
    function probe() {
      probing = true
      const startedIn = sessionGeneration()
      request<Session>("GET", "/api/v1/auth/session")
        .then(
          (session) => {
            if (active && startedIn === sessionGeneration()) setSession(session)
          },
          () => {
            if (active && startedIn === sessionGeneration()) setSession(null)
          },
        )
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run`
Expected: PASS (`Test Files  14 passed (14)`)

- [ ] **Step 5: Typecheck, lint and commit**

Run: `timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: exit 0 with no output from tsc or eslint

```bash
git add web/src/api/client.ts web/src/api/client.test.ts web/src/session/session.tsx web/src/session/session.test.tsx
git commit -m "fix(web): ignore session answers from before re-login"
```

### Task 21: Logout redirect and role label

**Files:**
- Modify: `web/src/session/session.tsx` (`SessionProvider`, `RequireSession`)
- Modify: `web/src/session/session-context.ts` (`SessionState`, `SessionContextValue`)
- Modify: `web/src/shell.tsx` (`Shell`)
- Modify: `web/src/test/location-probe.tsx` (`LocationProbe`)
- Test: `web/src/shell.test.tsx`

**Interfaces:**
- Consumes: `sessionGeneration()` (see Contracts); this task's whole-file replacements of `session.tsx` already contain Task 20's generation checks, so start from Task 20's committed tree
- Produces: none

**Items:** 116

**Depends on:** Task 20

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 2 = 5


- [ ] **Step 1: Write the failing tests for the logout redirect**

Replace the whole of `web/src/test/location-probe.tsx` with:

```tsx
import { useState } from "react"
import { useLocation } from "react-router"

// The probe also lists every location it has seen, so a test can tell a
// direct navigation from one that passed through another address.
export function LocationProbe() {
  const location = useLocation()
  const current = location.pathname + location.search
  const [seen, setSeen] = useState([current])
  if (seen[seen.length - 1] !== current) setSeen([...seen, current])
  return (
    <>
      <output data-testid="location">{current}</output>
      <output data-testid="locations">{seen.join(" ")}</output>
    </>
  )
}
```

In `web/src/shell.test.tsx`, insert this test directly after the test `"logging out ends the session and returns to the login page"` (before `"a failed logout keeps the session and says why"`):

```tsx
test.each([
  ["confirmed", { status: 204 }],
  ["answered with auth.required", problem(401, "auth.required", "log in")],
])("a logout %s goes from the page straight to the login page", async (_, answer) => {
  renderShell({ body: bound }, "/audit", answer)
  await screen.findByText("Audit content")
  await logOut(userEvent.setup())
  await screen.findByText("Login page")
  expect(screen.getByTestId("locations").textContent).toBe("/audit /login")
})
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `timeout 300 pnpm -C web exec vitest run src/shell.test.tsx src/test`
Expected: FAIL, both rows of `a logout ... goes from the page straight to the login page` with `AssertionError: expected '/audit /login?next=%2Faudit' to be '/audit /login'`; every other test passes.

- [ ] **Step 3: Write the implementation**

Replace the whole of `web/src/session/session-context.ts` with:

```ts
import { createContext } from "react"
import type { Session } from "@/api/types"

export interface SessionState {
  status: "loading" | "ready"
  session: Session | null
  // The session ended by a logout, so the login page gets no next parameter.
  loggedOut: boolean
}

export interface SessionContextValue extends SessionState {
  setSession: (session: Session | null) => void
  logout: () => Promise<void>
}

export const SessionContext = createContext<SessionContextValue | null>(null)
```

Replace the whole of `web/src/session/session.tsx` with (this includes Task 20's generation checks):

```tsx
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react"
import { Navigate, useLocation } from "react-router"
import { useQueryClient } from "@tanstack/react-query"
import { Spinner } from "darkraise-ui/components/spinner"
import { ApiError, request, sessionGeneration, setCsrfRejectedHandler, setCsrfToken, setUnauthorizedHandler } from "@/api/client"
import type { Session } from "@/api/types"
import { SessionContext, type SessionState } from "./session-context"
import { useSession } from "./use-session"

export function SessionProvider({ children }: { children: ReactNode }) {
  const client = useQueryClient()
  const [state, setState] = useState<SessionState>({ status: "loading", session: null, loggedOut: false })
  const loggingOut = useRef(false)

  const replaceSession = useCallback(
    (session: Session | null, loggedOut: boolean) => {
      setCsrfToken(session?.csrfToken ?? null)
      if (session === null) client.clear()
      setState({ status: "ready", session, loggedOut })
    },
    [client],
  )

  const setSession = useCallback((session: Session | null) => replaceSession(session, false), [replaceSession])

  // Only a logout the server confirmed ends the local session: the session
  // cookie is HttpOnly, so after a failure the browser would still be logged
  // in on reload. An auth.required answer means the session had already
  // ended; the unauthorized handler ends it here too, as a logout.
  const logout = useCallback(async () => {
    loggingOut.current = true
    try {
      await request<void>("POST", "/api/v1/auth/logout")
      replaceSession(null, true)
    } catch (err) {
      if (!(err instanceof ApiError && err.code === "auth.required")) throw err
    } finally {
      loggingOut.current = false
    }
  }, [replaceSession])

  useEffect(() => {
    let active = true
    let probing = false
    function probe() {
      probing = true
      const startedIn = sessionGeneration()
      request<Session>("GET", "/api/v1/auth/session")
        .then(
          (session) => {
            if (active && startedIn === sessionGeneration()) setSession(session)
          },
          () => {
            if (active && startedIn === sessionGeneration()) setSession(null)
          },
        )
        .finally(() => {
          probing = false
        })
    }
    setUnauthorizedHandler(() => {
      if (active) replaceSession(null, loggingOut.current)
    })
    // Another tab that logs in again replaces the session cookie, so this
    // tab's CSRF token goes stale and every unsafe request answers auth.csrf.
    // Probing the session fetches the token that belongs to the cookie.
    setCsrfRejectedHandler(() => {
      if (active && !probing) probe()
    })
    probe()
    return () => {
      active = false
      setUnauthorizedHandler(null)
      setCsrfRejectedHandler(null)
    }
  }, [setSession, replaceSession])

  const value = useMemo(() => ({ ...state, setSession, logout }), [state, setSession, logout])
  return <SessionContext value={value}>{children}</SessionContext>
}

export function SessionSpinner() {
  return (
    <div className="flex justify-center py-16">
      <Spinner aria-label="Loading" />
    </div>
  )
}

export function RequireSession({ children }: { children: ReactNode }) {
  const { status, session, loggedOut } = useSession()
  const location = useLocation()
  if (status === "loading") return <SessionSpinner />
  if (session === null) {
    const next = location.pathname + location.search
    return <Navigate to={loggedOut || next === "/" ? "/login" : `/login?next=${encodeURIComponent(next)}`} replace />
  }
  return <>{children}</>
}
```

In `web/src/shell.tsx`, replace

```tsx
import { errorMessage, getListenerState, request } from "@/api/client"
```

with

```tsx
import { errorMessage, getListenerState } from "@/api/client"
```

replace

```tsx
  const { session, setSession } = useSession()
  const [logoutError, setLogoutError] = useState<string | null>(null)

  // Only a logout the server confirmed ends the local session: the session
  // cookie is HttpOnly, so after a failure the browser would still be logged
  // in on reload. An auth.required answer ends it through the client's
  // unauthorized handler. Either way the guard then sends the page to the
  // login page.
  async function logout() {
    setLogoutError(null)
    try {
      await request<void>("POST", "/api/v1/auth/logout")
      setSession(null)
    } catch (err) {
      setLogoutError(`Logout failed: ${errorMessage(err)}`)
    }
  }
```

with

```tsx
  const { session, logout } = useSession()
  const [logoutError, setLogoutError] = useState<string | null>(null)

  async function logOut() {
    setLogoutError(null)
    try {
      await logout()
    } catch (err) {
      setLogoutError(`Logout failed: ${errorMessage(err)}`)
    }
  }
```

and replace

```tsx
      onLogout={() => void logout()}
```

with

```tsx
      onLogout={() => void logOut()}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run`
Expected: PASS (`Test Files  14 passed (14)`)

- [ ] **Step 5: Write the failing test for the role label**

In `web/src/shell.test.tsx`, replace

```tsx
// The user menu's trigger shows the user's initials, so "admin" is "A".
async function logOut(user: ReturnType<typeof userEvent.setup>) {
  const trigger = screen.getAllByRole("button", { name: "A" })[0]
  if (trigger === undefined) throw new Error("no user menu")
  await user.click(trigger)
  await user.click(await screen.findByRole("menuitem", { name: "Log out" }))
}
```

with

```tsx
// The user menu's trigger shows the user's initials, so "admin" is "A".
async function openUserMenu(user: ReturnType<typeof userEvent.setup>) {
  const trigger = screen.getAllByRole("button", { name: "A" })[0]
  if (trigger === undefined) throw new Error("no user menu")
  await user.click(trigger)
}

async function logOut(user: ReturnType<typeof userEvent.setup>) {
  await openUserMenu(user)
  await user.click(await screen.findByRole("menuitem", { name: "Log out" }))
}
```

and insert this test directly after the test `"the footer reports a failed listener request"` (before `"logging out ends the session and returns to the login page"`):

```tsx
test("the user menu names the user and their role", async () => {
  renderShell({ body: bound })
  await screen.findByText("Hosts content")
  await openUserMenu(userEvent.setup())
  expect((await screen.findByText("Role: admin")).textContent).toBe("Role: admin")
})
```

- [ ] **Step 6: Run the test to verify it fails**

Run: `timeout 300 pnpm -C web exec vitest run src/shell.test.tsx`
Expected: FAIL, `the user menu names the user and their role` with `TestingLibraryElementError: Unable to find an element with the text: Role: admin.`

- [ ] **Step 7: Write the implementation**

In `web/src/shell.tsx`, replace

```tsx
      user={session === null ? undefined : { name: session.user.username, email: session.user.role }}
```

with

```tsx
      user={session === null ? undefined : { name: session.user.username, email: `Role: ${session.user.role}` }}
```

- [ ] **Step 8: Run the tests to verify they pass**

Run: `timeout 300 pnpm -C web exec vitest run && timeout 300 pnpm -C web typecheck && timeout 300 pnpm -C web lint`
Expected: PASS (`Test Files  14 passed (14)`), typecheck and lint exit 0 with no output from tsc or eslint


- [ ] **Step 9: Commit**

```bash
git add web/src/session/session.tsx web/src/session/session-context.ts web/src/shell.tsx web/src/shell.test.tsx web/src/test/location-probe.tsx
git commit -m "fix(web): log out without a next page; show role"
```

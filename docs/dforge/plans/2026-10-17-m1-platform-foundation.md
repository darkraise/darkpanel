# M1 Platform Core Foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build part P0 of the M1 platform core (plan 1 of 6 in the spec's "Plans" section): every M1 Go dependency, the module contract and its fakes, the registry and its registration rules, the SQLite store with migrations and backups, the error codes, paths, build info, the Makefile fixes and ADRs 0010 and 0011, so that the eight wave-1 parts of plan 2 and later can code against fixed names.

**Architecture:** `internal/core` declares the contract of spec section 2a; `internal/core/store` opens both roles' SQLite databases, applies embedded forward-only migrations and backs up before them; `internal/core/errcode` is the stable error-code registry; `internal/core/coretest` holds the fakes later parts test with. `internal/registry` builds the one registry from the core module (a stub with no actions until plan 3) and `internal/modules.All()` (empty until plan 4). `cmd/darkpanel` reads its version from `internal/buildinfo` and gains a `serve` stub.

**Tech Stack:** Go 1.27.2, cobra v1.10.2, modernc.org/sqlite v1.60.1 (modernc.org/libc v1.77.1), golangci-lint v2.15.0, GNU make.

**Spec:** docs/dforge/specs/2026-10-17-m1-platform-core-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves.

**Plan review:** 2026-10-17 — dr-superpowers:judge-opus — executability 17 / coherence 18 / coverage 18 / assumptions 17 (round 2)

**Parallelism:** waves — 1 | 2 | 3-5 | 6-8 | 9-11 | 12-13 | 14 | 15 | 16

**Integration check:** `timeout 1500 make check`

**Worktree setup:** `timeout 300 pnpm -C web install --frozen-lockfile`

## Global Constraints

- The spec is the source of truth, including its "Owner decisions" section; this plan's Assumptions record where it departs and why. Where a step gives a file's complete content, write exactly that content.
- Pins are exact. Versions, verbatim from the spec: `modernc.org/sqlite` v1.60.1 with `modernc.org/libc` v1.77.1, `github.com/invopop/jsonschema` v0.14.0, `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3, `golang.org/x/crypto` v0.58.0, `golang.org/x/sys` v0.49.0, `golang.org/x/term` v0.47.0, `github.com/oklog/ulid/v2` v2.1.2, `github.com/mdp/qrterminal/v3` v3.2.1; `go.mod` keeps `go 1.27`, `toolchain go1.27.2` and `github.com/spf13/cobra` v1.10.2.
- Every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001). The `Makefile` exports it; a `go` command typed outside `make` is prefixed with it, as every step here does.
- A task edits only the files in its own `**Files:**` block. The one regenerated file is `go.sum`, written by `go mod tidy` in Task 1.
- In a waves run every task executes in its own git worktree created by the controller and commits only there; the controller merges a wave's worktrees before starting the next wave.
- `**Items:**` numbers are row numbers of the register `docs/dforge/registers/2026-10-17-m1-platform-core.md`.
- Test isolation (spec "Parallel decomposition"): every Go test uses `t.TempDir()` for databases, keys and directories, opens no port, writes no fixed path, and uses `coretest.FakeClock` for timers. No test in this plan shares a file, port or database with another task's tests.
- `golangci-lint run` reports `0 issues.` after every task: every exported identifier has a doc comment (revive), files are `gofmt`- and `goimports`-clean, and a `//nolint` carries the rule and a reason, as the code below shows.
- Every network, build and test command carries an explicit `timeout`.
- ADRs 0001 to 0009 are Accepted and are never edited; ADR changes go in new ADRs (0010, 0011).
- Never commit `.superpowers/`.
- English only. Commits use `<type>(<scope>): <subject>` with type one of feat, fix, docs, style, refactor, test, chore, perf; the whole first line is at most 50 characters, imperative, no trailing period; no AI attribution of any kind.
- `Makefile` recipe lines begin with a tab character, never spaces.

## Contracts

Later plans code against these names; spec section 2a is the authority for the interfaces, and Task 6's `contract.go` is their Go text.

**`internal/core/store`** (package `store`)

- Task 2 (`db.go`): `const SideController = "controller"`, `const SideAgent = "agent"`; `func Open(ctx context.Context, path string) (*DB, error)` creates a missing file with mode 0600 and refuses a path containing `?` or `#`; methods `(*DB) Path() string`, `Read() *sql.DB` (query-only pool), `Writer() *sql.DB` (the single writer connection, for `VACUUM INTO`), `Write(ctx context.Context, fn func(*sql.Tx) error) error` (immediate transaction), `Close() error`.
- Task 14 (`backup.go`): `func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time, keep int) (string, error)` writes `root/<from>-<to>-<YYYYMMDDTHHMMSSZ>/<database file name>` (directory 0700, file 0600; characters outside `[A-Za-z0-9._]` in `from` and `to` become `_`), keeps the newest `keep` such directories and returns the new one.
- Task 15 (`migrate.go`): `type MigrationSet struct { Module string; FS fs.FS }` (files `NNNN_<name>.sql` at the root of `FS`; numbers unique, from 1, gaps allowed); `type MigrateOptions struct { Side, BinaryVersion, BackupRoot string; Keep int; Now time.Time }`; `type Applied struct { Module string; Number int; Name string }`; `type ModuleFailure struct { Module string; Err error }`; `type Result struct { Applied []Applied; BackupDir string; Failed []ModuleFailure }`; `func Migrate(ctx context.Context, db *DB, opts MigrateOptions, core MigrationSet, modules []MigrationSet) (Result, error)`; `func SchemaVersion(ctx context.Context, db *DB, side string) (int, error)`. The runner owns table `core_migrations (module, side, number, name, checksum, applied_at)` and writes `core_meta` key `binary_version`.
- Task 16 (`migrations.go`): `func CoreMigrations(side string) (MigrationSet, error)` returns module `core`'s embedded set for `"controller"` or `"agent"`; files `migrations/controller/0001_core.sql` and `migrations/agent/0001_core.sql`. Later parts add `0002`+ files there (spec section 4).
- Test helpers shared between the package's test files: `openTemp(t *testing.T) *DB` (Task 2, `db_test.go`); `metaSQL`, `sqlFS(files map[string]string) fstest.MapFS`, `migrateOpts(t *testing.T, version string, now time.Time) MigrateOptions` (side `controller`, keep 5, a temporary backup root) and `tableExists(t *testing.T, db *DB, name string) bool` (Task 15, `migrate_test.go`).

**`internal/core/errcode`** (Task 3)

- `type Code string`; constants `InvalidJSON`, `InvalidInput`, `TooLarge`, `UnsupportedMediaType`, `MethodNotAllowed`, `AuthRequired`, `InvalidCredentials`, `RateLimited`, `CSRF`, `Forbidden`, `NotFound`, `ActionUnknown`, `ModuleUnavailable`, `PlanNotFound`, `PlanExpired`, `PlanStateChanged`, `PlanPrincipalMismatch`, `PlanConfirmationNeeded`, `PlanGuardFailed`, `ChangeNotFound`, `ChangeNotPending`, `LockHeld`, `Internal`, and the action-level `ChangeRevertFailed`, `ExecOutputTooLarge`, `FilesMarkersBad`, `JournalOverflow`, with the code strings and statuses of spec section 12.
- `(Code) Status() int`, `(Code) Title() string`, `func Known(c Code) bool`; `type FieldError struct { Path, Message string }` (JSON `path`, `message`); `type Error struct { Code Code; Detail string; Fields []FieldError; Err error }` with `Error()` and `Unwrap()`; `func New(code Code, detail string) *Error`, `func Wrap(code Code, err error, detail string) *Error`, `func From(err error) *Error`.

**`internal/core`** (Tasks 6 and 9)

- Task 6 (`contract.go`): every declaration of spec section 2a, plus `const ModuleIDCore = "core"`; the constants `SideController`, `SideAgent`, `ClassRead`, `ClassRevertible`, `ClassCompensable`, `ClassIrreversible`, `ScopeRead`, `ScopeOperate`, `ScopeAdmin`; `(Scope) Allows(need Scope) bool`; `(ActionRef) String()` (`<name>/v<n>`); `func ParseVersion(s string) (int, error)` (`"v1"` → 1); `type ErasedHandler` (fields `Read`, `Plan`, `Apply`, `Verify`, `Snapshot`, `Revert` over `json.RawMessage`); `type ActionDef struct { Spec ActionSpec; InputType, OutputType reflect.Type; Handler ErasedHandler }` with `Ref() ActionRef`; `type ResourceDef struct { Name string; OutputType reflect.Type; Read func(ctx context.Context, env Env) (json.RawMessage, error) }`; change states `StateApplying`, `StatePending`, `StateConfirmed`, `StateReverted`, `StateCompleted`, `StateFailed`, `StateInterrupted`; `type SystemClock struct{}`, the production `Clock`. `DiffLine` and `Guard` carry JSON tags `op`, `text`, `id`, `ok`, `message`.
- Task 9 (`define.go`, `registry.go`): `DefineRead`, `DefineMutating`, `DefineRevertible`, `DefineResource` with the signatures of spec section 2a (handlers decode input strictly: unknown fields and trailing data fail with `request.invalid_input`); `var ReservedNames []string`; `func NewRegistry(mods ...Module) (*Registry, error)` returns every rule violation joined; methods `Action(ActionRef) (ActionDef, bool)`, `Actions() []ActionDef` (sorted by name, then version), `Resource(name string) (ResourceDef, bool)`, `Resources() []ResourceDef` (sorted), `Modules() []Manifest` (registration order), `Module(id string) (Manifest, bool)`.

**`internal/core/coretest`** (Tasks 10 and 12)

- Task 10: `func NewFakeClock(start time.Time) *FakeClock` with `Now`, `AfterFunc`, `Advance(d time.Duration)` (runs due timers in deadline order on the caller's goroutine) and `Pending() int`; `type Sealer struct{}` (AES-GCM under a fixed key); `type Validator struct{}` (strict decode into the input type). Validators, the fake and plan 2's schema validator alike, refuse an empty input; the `Define*` handlers decode an empty input as the zero value only because callers validate first.
- Task 12: `type MemJournal struct { Err error }` with `Append` and `Entries() []core.JournalEntry`; `type MemAudit struct{}` implementing `core.Audit` (ids are 26-digit counters; `List` newest first); `type FakeAgent struct` and `type FakeDispatcher struct` with one `<Method>Func` field per interface method (a nil field makes the method return an error naming it); `type MapHosts map[string]core.AgentClient` (unknown host: `not_found`).

**Other packages**

- `internal/buildinfo` (Task 4): `var Version = "dev"`, `var Commit = "unknown"`, set with `-X github.com/darkraise/darkpanel/internal/buildinfo.Version=…` and `.Commit=…`.
- `internal/paths` (Task 5): `type Paths struct { ControllerDir, AgentDir, ControllerKey, AgentKey, ControllerSocket string }`; methods `ControllerDB()`, `AgentDB()`, `ControllerBackups()`, `AgentBackups()`, `MkdirAll() error`; `func Production() Paths`, `func Under(root string) Paths`, `func Resolve(dev bool) (Paths, error)`; the locations of spec section 1.
- `internal/controller/coreactions` (Task 11): `type Deps struct{}`, `func Module(Deps) core.Module` (id `core`, version 1, `DefaultEnabled`, no actions). Plan 3 takes the package over and adds fields to `Deps`.
- `internal/modules` (Task 11): `func All() []core.Module`, empty; plan 4 adds the host-information line.
- `internal/registry` (Task 13): `func Build() (*core.Registry, error)` and `func BuildWith(coreModule core.Module) (*core.Registry, error)`.
- `darkpanel serve` (Task 4): flags `--dev` (bool) and `--listen` (repeatable string); until plan 4 it exits 1 with `darkpanel: serve: the controller is not assembled yet`.
- `Makefile` (Task 7): `LDFLAGS` sets the two `internal/buildinfo` variables; new target `web-deps` (`pnpm -C web install --frozen-lockfile`), a prerequisite of `web`, `lint-web` and `test-web`; `verify-build` depends on `build` and checks that the host-architecture binary reports `"commit":"$(COMMIT)"`. Every other target keeps its name and recipe. Plan 2's schema part adds the `schemas` target (spec section 19); after this plan no task here edits the `Makefile` again.
- ADRs (Task 8): `docs/adr/0010-web-certificate-authority.md`, `docs/adr/0011-actors-cli-names-and-layout.md`, both `Proposed`, linked from `docs/adr/README.md`.

## Assumptions (evidence)

- Every task's code below was run on 2026-10-17 in a scratch copy of `main` at `f13e349`: for each task, a tree holding the earlier waves' files plus only that task's files passed `go vet ./...`, `go test -count=1 ./...`, `golangci-lint run ./...` (v2.15.0, `0 issues.`), `bash scripts/check-adrs.sh --complete` and `go mod tidy -diff`; the complete tree passed `make check` (lint, Go, Vitest and ADR tests, build, verify-build, `No vulnerabilities found.`) and `go test -race ./...`.
- `go mod tidy` with only Task 1's `internal/deps/deps.go` added reproduces Task 1's `go.mod` byte for byte and resolves `modernc.org/libc` v1.77.1 (run 2026-10-17); before Task 1, `go build -tags deps ./internal/deps/` fails with `no required module provides package`.
- The linker ignores `-X` for a variable that does not exist (https://pkg.go.dev/cmd/link, flag `-X`: effective only if the variable is declared), so Task 7's `verify-build` runs the built binary and checks its commit; verified 2026-10-17 with `make build verify-build`.
- golangci-lint v2.15.0 is installed on the development host (`golangci-lint --version`, 2026-10-17) and CI pins the same version (`.github/workflows/ci.yml`).
- `codex off — quota` (`codex-gate usable=false reason=quota`, 2026-10-17, resets 2026-10-22); every task runs on a Claude implementer.
- Plan-time ruling, spec section 2: `registry.Build()` builds the core module from zero `coreactions.Deps`, which suits its shape-only callers (CLI, schema generator, tests); the server needs core handlers wired to its stores, so Task 13 adds `registry.BuildWith(coreModule)` and `Build()` calls it. Both return the same registry shape.
- Plan-time ruling, spec section 4: migration numbers must be unique but may have gaps, and a pending migration numbered below an applied one is refused. Each core migration file has its own owning part, and in plan 2 the platform part's agent `0003_ownership.sql` can merge before the lifecycle part's `0002_lifecycle.sql`; a no-gaps rule would fail the platform part's tests in its own worktree.
- Plan-time ruling, spec section 4: no backup is taken when `core_meta`'s `binary_version` already equals the running version. That version's first run took one before migrating; without the rule, a module migration that keeps failing under systemd restarts would take a backup on every start, and the five-backup limit would delete the pre-upgrade copy. A new database is backed up as `none-<version>-<time>`, as the spec says.
- Plan-time ruling, spec section 4 ("a changed checksum or a failed core migration stops startup"): a changed checksum in a core migration stops startup; a changed checksum, bad file name or duplicated number in a module's migrations marks only that module unavailable, as R9 requires of a failing module. `TestMigrateModuleChecksumChangeIsIsolated` (Task 15) proves it.
- Consequence of the same-version backup rule: a build whose version stays `dev` (a plain `go build` or `go run` without the Makefile's `-ldflags`) backs up only on its first run against a database; release and Makefile builds carry a distinct version per commit and are unaffected.
- Additions beyond spec section 2a, none of which changes a 2a declaration: `store.DB.Writer` and `store.DB.Path` (for `VACUUM INTO` and backup names), `core.ModuleIDCore`, `core.SystemClock` (`time.AfterFunc` uses the monotonic clock, as section 3 requires), `store.SchemaVersion` (section 4's `/healthz` schema version), `paths.Production`, `paths.Under`, `Paths.MkdirAll` and the database and backup path methods, and the `MemAudit`, `FakeDispatcher` and `MapHosts` fakes.
- The `schemas` Make target (spec section 19) is added by plan 2 together with the generator it runs, so the Makefile never names a package that does not exist yet (Contracts › Other packages › `Makefile` records the handoff).
- `core_hosts.name` carries no unique constraint (spec section 4 asks for none): M2 hosts may share a hostname, and a forward-only schema could only drop such a constraint by rebuilding the table.
- The `serve` stub has the flags of spec section 1 and fails until plan 4 assembles the server; nothing before then needs it to run.
- `.golangci.yml` gets `build-tags: [harness]` as spec section 18 says; `internal/deps/deps.go` (tag `deps`) is outside the linted set and was checked with `golangci-lint run --build-tags deps ./internal/deps/` (`0 issues.`, 2026-10-17).
- Register row 1 (R5) is assigned here as the handoff of 2026-10-17 decided. This plan proves its registry-rules half; the "hostinfo registered by one line" half is proven when plan 4 adds that line, so finishing this plan records row 1 as `verify`, not `done`.

## Task index

1. Go dependencies
2. SQLite store
3. Error codes
4. Build info, version and serve stub
5. Paths
6. Module contract
7. Makefile and lint configuration
8. ADRs 0010 and 0011
9. Action definitions and registry
10. Clock, sealer and validator fakes
11. Module list and core module stub
12. Journal, audit, agent and dispatcher fakes
13. Registry build
14. Database backups
15. Migration runner
16. Core migrations

---

### Task 1: Go dependencies

**Files:**
- Create: `internal/deps/deps.go`
- Modify: `go.mod`
- Modify: `go.sum` (regenerated by `go mod tidy`)

**Interfaces:**
- Consumes: nothing.
- Produces: every M1 module pinned in `go.mod` (Global Constraints), so no later task touches the manifest. The `deps` package builds only with `-tags deps`; plan 4 deletes it.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the import file**

Create `internal/deps/deps.go`:

```go
//go:build deps

// Package deps keeps every M1 dependency in go.mod until code imports it.
package deps

// Blank imports only: each line pins one module version for a later part.
import (
	_ "github.com/invopop/jsonschema"            // schema reflection
	_ "github.com/mdp/qrterminal/v3"             // TOTP enrollment QR code
	_ "github.com/oklog/ulid/v2"                 // entity ids
	_ "github.com/santhosh-tekuri/jsonschema/v6" // input validation
	_ "golang.org/x/crypto/argon2"               // password hashing
	_ "golang.org/x/crypto/chacha20poly1305"     // sealer
	_ "golang.org/x/crypto/hkdf"                 // subkeys
	_ "golang.org/x/crypto/ssh"                  // harness SSH client
	_ "golang.org/x/sys/unix"                    // openat2, SO_PEERCRED, IP_FREEBIND
	_ "golang.org/x/term"                        // password prompts
	_ "modernc.org/sqlite"                       // SQLite without CGO
)
```

- [ ] **Step 2: Run the build to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go build -tags deps ./internal/deps/`
Expected: FAIL with `no required module provides package github.com/invopop/jsonschema` (and one such line per missing module).

- [ ] **Step 3: Write `go.mod`**

Replace `go.mod` with exactly:

```
module github.com/darkraise/darkpanel

go 1.27

toolchain go1.27.2

require (
	github.com/invopop/jsonschema v0.14.0
	github.com/mdp/qrterminal/v3 v3.2.1
	github.com/oklog/ulid/v2 v2.1.2
	github.com/santhosh-tekuri/jsonschema/v6 v6.0.3
	github.com/spf13/cobra v1.10.2
	golang.org/x/crypto v0.58.0
	golang.org/x/sys v0.49.0
	golang.org/x/term v0.47.0
	modernc.org/sqlite v1.60.1
)

require (
	github.com/bahlo/generic-list-go v0.2.0 // indirect
	github.com/buger/jsonparser v1.1.2 // indirect
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/inconshreveable/mousetrap v1.1.0 // indirect
	github.com/mattn/go-isatty v0.0.24 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/pb33f/ordered-map/v2 v2.3.1 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/spf13/pflag v1.0.9 // indirect
	go.yaml.in/yaml/v4 v4.0.0-rc.2 // indirect
	golang.org/x/text v0.43.0 // indirect
	modernc.org/libc v1.77.1 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.12.1 // indirect
	rsc.io/qr v0.2.0 // indirect
)
```

Then stage it, so the next step can compare tidy's result with this file: `git add go.mod`

- [ ] **Step 4: Regenerate `go.sum` and verify**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go mod tidy
git diff --exit-code go.mod
GOTOOLCHAIN=local timeout 300 go build -tags deps ./internal/deps/
GOTOOLCHAIN=local timeout 300 go mod verify
GOTOOLCHAIN=local timeout 300 go build ./...
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 300 golangci-lint run --build-tags deps ./internal/deps/
```

Expected: `go mod tidy` downloads the modules and rewrites `go.sum`; `git diff --exit-code go.mod` prints nothing and exits 0 (tidy agrees with Step 3's file); `go mod verify` prints `all modules verified`; the builds succeed; every test line starts with `ok`; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add go.mod go.sum internal/deps/deps.go
git commit -m "chore(deps): pin the M1 platform core modules"
```

---

### Task 2: SQLite store

**Files:**
- Create: `internal/core/store/db.go`
- Test: `internal/core/store/db_test.go`

**Interfaces:**
- Consumes: `modernc.org/sqlite` from Task 1.
- Produces: Contracts › `internal/core/store` › Task 2 (`Open`, `DB`, `SideController`, `SideAgent`, test helper `openTemp`).

**Items:** 4

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The writer connection's pragmas are what make every commit reach disk before the host is touched (ADR 0004, spec section 4); change none of them.

- [ ] **Step 1: Write the failing test**

Create `internal/core/store/db_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func openTemp(t *testing.T) *DB {
	t.Helper()
	db, err := Open(context.Background(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestOpenSetsPragmasAndMode(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	pragmas := map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000"}
	for name, want := range pragmas {
		var got string
		if err := db.Writer().QueryRowContext(ctx, "PRAGMA "+name).Scan(&got); err != nil {
			t.Fatalf("PRAGMA %s: %v", name, err)
		}
		if got != want {
			t.Errorf("writer PRAGMA %s = %s, want %s", name, got, want)
		}
	}
	info, err := os.Stat(db.Path())
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("database mode = %o, want 600", mode)
	}
}

func TestReaderCannotWrite(t *testing.T) {
	db := openTemp(t)
	if _, err := db.Read().Exec("CREATE TABLE t (x INTEGER)"); err == nil {
		t.Fatal("reader created a table, want an error")
	}
}

func TestWriteCommitsAndRollsBack(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (1)")
		return err
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO t VALUES (2)"); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write error = %v, want boom", err)
	}
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM t").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Errorf("rows = %d, want 1 (the second insert must roll back)", n)
	}
}

func TestOpenRefusesURICharacters(t *testing.T) {
	if _, err := Open(context.Background(), filepath.Join(t.TempDir(), "a?b.db")); err == nil {
		t.Fatal("Open accepted a path containing '?'")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/store/`
Expected: FAIL — the build reports `undefined: Open`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/db.go`:

```go
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
	if strings.ContainsAny(path, "?#") {
		return nil, fmt.Errorf("store: database path %q must not contain ? or #", path)
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
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/store/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/store/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/store`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/db.go internal/core/store/db_test.go
git commit -m "feat(store): open SQLite with durable pragmas"
```

---

### Task 3: Error codes

**Files:**
- Create: `internal/core/errcode/errcode.go`
- Test: `internal/core/errcode/errcode_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: Contracts › `internal/core/errcode`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/core/errcode/errcode_test.go`:

```go
package errcode

import (
	"errors"
	"fmt"
	"testing"
)

func TestStatus(t *testing.T) {
	tests := []struct {
		code Code
		want int
	}{
		{InvalidJSON, 400},
		{InvalidInput, 422},
		{TooLarge, 413},
		{UnsupportedMediaType, 415},
		{MethodNotAllowed, 405},
		{AuthRequired, 401},
		{InvalidCredentials, 401},
		{RateLimited, 429},
		{CSRF, 403},
		{Forbidden, 403},
		{NotFound, 404},
		{ActionUnknown, 404},
		{ModuleUnavailable, 409},
		{PlanNotFound, 404},
		{PlanExpired, 410},
		{PlanStateChanged, 409},
		{PlanPrincipalMismatch, 403},
		{PlanConfirmationNeeded, 422},
		{PlanGuardFailed, 409},
		{ChangeNotFound, 404},
		{ChangeNotPending, 409},
		{LockHeld, 423},
		{Internal, 500},
		{ChangeRevertFailed, 500},
		{Code("no.such_code"), 500},
	}
	for _, tt := range tests {
		if got := tt.code.Status(); got != tt.want {
			t.Errorf("%s.Status() = %d, want %d", tt.code, got, tt.want)
		}
	}
}

func TestEveryCodeHasATitle(t *testing.T) {
	for code, i := range registry {
		if i.title == "" {
			t.Errorf("%s has no title", code)
		}
	}
	if Code("no.such_code").Title() != "Internal error" {
		t.Errorf("unknown code title = %q", Code("no.such_code").Title())
	}
}

func TestFrom(t *testing.T) {
	coded := New(PlanExpired, "plan 01J expired")
	if got := From(fmt.Errorf("apply: %w", coded)); got != coded {
		t.Errorf("From(wrapped) = %v, want the original *Error", got)
	}
	cause := errors.New("disk full")
	got := From(cause)
	if got.Code != Internal || !errors.Is(got, cause) || got.Detail != "" {
		t.Errorf("From(plain) = %+v, want Internal wrapping the cause with no detail", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/errcode/`
Expected: FAIL — the build reports `undefined: Code` (and the other missing names).

- [ ] **Step 3: Write the implementation**

Create `internal/core/errcode/errcode.go`:

```go
// Package errcode is the registry of stable error codes (shared contracts in
// docs/adr/README.md) and the HTTP status each one maps to.
package errcode

import (
	"errors"
	"fmt"
	"net/http"
)

// Code is a stable, machine-readable error code such as "plan.expired".
type Code string

// Codes returned as RFC 9457 problem details before a change exists.
const (
	InvalidJSON            Code = "request.invalid_json"
	InvalidInput           Code = "request.invalid_input"
	TooLarge               Code = "request.too_large"
	UnsupportedMediaType   Code = "request.unsupported_media_type"
	MethodNotAllowed       Code = "method_not_allowed"
	AuthRequired           Code = "auth.required"
	InvalidCredentials     Code = "auth.invalid_credentials" //nolint:gosec // G101: an error code, not a credential
	RateLimited            Code = "auth.rate_limited"
	CSRF                   Code = "auth.csrf"
	Forbidden              Code = "auth.forbidden"
	NotFound               Code = "not_found"
	ActionUnknown          Code = "action.unknown"
	ModuleUnavailable      Code = "module.unavailable"
	PlanNotFound           Code = "plan.not_found"
	PlanExpired            Code = "plan.expired"
	PlanStateChanged       Code = "plan.state_changed"
	PlanPrincipalMismatch  Code = "plan.principal_mismatch"
	PlanConfirmationNeeded Code = "plan.confirmation_required"
	PlanGuardFailed        Code = "plan.guard_failed"
	ChangeNotFound         Code = "change.not_found"
	ChangeNotPending       Code = "change.not_pending"
	LockHeld               Code = "lock.held"
	Internal               Code = "internal"
)

// Action-level codes: they appear in a change's error or in the audit log,
// never as an HTTP status of their own.
const (
	ChangeRevertFailed Code = "change.revert_failed"
	ExecOutputTooLarge Code = "exec.output_too_large"
	FilesMarkersBad    Code = "files.markers_invalid"
	JournalOverflow    Code = "journal.overflow"
)

type info struct {
	status int
	title  string
}

var registry = map[Code]info{
	InvalidJSON:            {http.StatusBadRequest, "Invalid JSON"},
	InvalidInput:           {http.StatusUnprocessableEntity, "Invalid input"},
	TooLarge:               {http.StatusRequestEntityTooLarge, "Request too large"},
	UnsupportedMediaType:   {http.StatusUnsupportedMediaType, "Unsupported media type"},
	MethodNotAllowed:       {http.StatusMethodNotAllowed, "Method not allowed"},
	AuthRequired:           {http.StatusUnauthorized, "Authentication required"},
	InvalidCredentials:     {http.StatusUnauthorized, "Invalid credentials"},
	RateLimited:            {http.StatusTooManyRequests, "Too many attempts"},
	CSRF:                   {http.StatusForbidden, "CSRF token missing or invalid"},
	Forbidden:              {http.StatusForbidden, "Forbidden"},
	NotFound:               {http.StatusNotFound, "Not found"},
	ActionUnknown:          {http.StatusNotFound, "Unknown action"},
	ModuleUnavailable:      {http.StatusConflict, "Module unavailable"},
	PlanNotFound:           {http.StatusNotFound, "Plan not found"},
	PlanExpired:            {http.StatusGone, "Plan expired"},
	PlanStateChanged:       {http.StatusConflict, "State changed since the plan"},
	PlanPrincipalMismatch:  {http.StatusForbidden, "Plan belongs to another principal"},
	PlanConfirmationNeeded: {http.StatusUnprocessableEntity, "Confirmation required"},
	PlanGuardFailed:        {http.StatusConflict, "Guard failed"},
	ChangeNotFound:         {http.StatusNotFound, "Change not found"},
	ChangeNotPending:       {http.StatusConflict, "Change is not pending"},
	LockHeld:               {http.StatusLocked, "Resource locked by another change"},
	Internal:               {http.StatusInternalServerError, "Internal error"},
	ChangeRevertFailed:     {http.StatusInternalServerError, "Revert failed"},
	ExecOutputTooLarge:     {http.StatusInternalServerError, "Command output too large"},
	FilesMarkersBad:        {http.StatusInternalServerError, "Invalid DARKPANEL markers"},
	JournalOverflow:        {http.StatusInternalServerError, "Journal overflow"},
}

// Known reports whether c is in the registry.
func Known(c Code) bool {
	_, ok := registry[c]
	return ok
}

// Status is the HTTP status for c; unknown codes map to 500.
func (c Code) Status() int {
	if i, ok := registry[c]; ok {
		return i.status
	}
	return http.StatusInternalServerError
}

// Title is the short human title used as a problem's "title".
func (c Code) Title() string {
	if i, ok := registry[c]; ok {
		return i.title
	}
	return registry[Internal].title
}

// FieldError is one validation failure, reported in a problem's "errors".
type FieldError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

// Error carries a code to the API boundary. Err is the cause; it is logged but
// never sent to a client.
type Error struct {
	Code   Code
	Detail string
	Fields []FieldError
	Err    error
}

// New returns an error with code and a client-safe detail.
func New(code Code, detail string) *Error {
	return &Error{Code: code, Detail: detail}
}

// Wrap returns an error with code, a client-safe detail and a hidden cause.
func Wrap(code Code, err error, detail string) *Error {
	return &Error{Code: code, Detail: detail, Err: err}
}

func (e *Error) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("%s: %s: %v", e.Code, e.Detail, e.Err)
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Detail)
}

func (e *Error) Unwrap() error { return e.Err }

// From returns the *Error in err's chain, or an Internal error wrapping err.
func From(err error) *Error {
	var e *Error
	if errors.As(err, &e) {
		return e
	}
	return Wrap(Internal, err, "")
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/errcode/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/errcode/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/errcode`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/errcode/errcode.go internal/core/errcode/errcode_test.go
git commit -m "feat(errcode): add the stable error code registry"
```

---

### Task 4: Build info, version and serve stub

**Files:**
- Create: `internal/buildinfo/buildinfo.go`
- Modify: `cmd/darkpanel/main.go` (whole file below)
- Test: `cmd/darkpanel/version_test.go` (whole file below)

**Interfaces:**
- Consumes: nothing.
- Produces: Contracts › Other packages › `internal/buildinfo` and `darkpanel serve`. The `version` command's output is unchanged.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

Replace `cmd/darkpanel/version_test.go` with:

```go
package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/buildinfo"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs(args)
	if err := cmd.Execute(); err != nil {
		t.Fatalf("darkpanel %v: %v", args, err)
	}
	return out.String()
}

func TestVersionText(t *testing.T) {
	got := runRoot(t, "version")
	want := "darkpanel dev (commit unknown, " + runtime.Version() + ", " + runtime.GOOS + "/" + runtime.GOARCH + ")\n"
	if got != want {
		t.Errorf("version output = %q, want %q", got, want)
	}
}

func TestVersionJSON(t *testing.T) {
	var got map[string]any
	if err := json.Unmarshal([]byte(runRoot(t, "version", "--json")), &got); err != nil {
		t.Fatalf("version --json is not a JSON object: %v", err)
	}
	want := map[string]string{
		"version": "dev",
		"commit":  "unknown",
		"go":      runtime.Version(),
		"os":      runtime.GOOS,
		"arch":    runtime.GOARCH,
	}
	for key, value := range want {
		if got[key] != value {
			t.Errorf("version --json %q = %v, want %q", key, got[key], value)
		}
	}
	// The value depends on whether the web build has run, so only its type is fixed.
	if _, ok := got["ui"].(bool); !ok {
		t.Errorf("version --json \"ui\" = %v, want a boolean", got["ui"])
	}
	if len(got) != len(want)+1 {
		t.Errorf("version --json has keys %v, want exactly version, commit, go, os, arch, ui", got)
	}
}

func TestVersionReadsBuildinfo(t *testing.T) {
	oldVersion, oldCommit := buildinfo.Version, buildinfo.Commit
	t.Cleanup(func() { buildinfo.Version, buildinfo.Commit = oldVersion, oldCommit })
	buildinfo.Version, buildinfo.Commit = "v9.9.9", "abc1234"
	var got map[string]any
	if err := json.Unmarshal([]byte(runRoot(t, "version", "--json")), &got); err != nil {
		t.Fatal(err)
	}
	if got["version"] != "v9.9.9" || got["commit"] != "abc1234" {
		t.Errorf("version --json = %v, want version v9.9.9 and commit abc1234 from buildinfo", got)
	}
}

func TestServeNotAssembled(t *testing.T) {
	var out bytes.Buffer
	cmd := newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serve", "--dev", "--listen", "loopback", "--listen", "tailscale"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "not assembled yet") {
		t.Fatalf("serve error = %v, want the not-assembled error", err)
	}
	cmd = newRootCmd()
	cmd.SetOut(&out)
	cmd.SetErr(&out)
	cmd.SetArgs([]string{"serve", "extra"})
	if err := cmd.Execute(); err == nil || !strings.Contains(err.Error(), "unknown command") {
		t.Errorf("serve extra: err = %v, want an argument error", err)
	}
}

func TestUIEmbedded(t *testing.T) {
	tests := []struct {
		name string
		dist fstest.MapFS
		want bool
	}{
		{"built", fstest.MapFS{"dist/index.html": {Data: []byte("<html></html>")}, "dist/.gitkeep": {}}, true},
		{"placeholder only", fstest.MapFS{"dist/.gitkeep": {}}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := uiEmbedded(tt.dist); got != tt.want {
				t.Errorf("uiEmbedded() = %v, want %v", got, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./cmd/darkpanel/`
Expected: FAIL — the build reports `no required module provides package github.com/darkraise/darkpanel/internal/buildinfo` for `version_test.go`.

- [ ] **Step 3: Write the implementation**

Create `internal/buildinfo/buildinfo.go`:

```go
// Package buildinfo holds the version and commit stamped into the binary.
package buildinfo

// Set by the build with -ldflags "-X
// github.com/darkraise/darkpanel/internal/buildinfo.Version=... -X
// github.com/darkraise/darkpanel/internal/buildinfo.Commit=...". The linker
// ignores -X for a variable that does not exist, so make verify-build checks
// that a built binary reports the commit.
var (
	Version = "dev"
	Commit  = "unknown"
)
```

Replace `cmd/darkpanel/main.go` with:

```go
// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"runtime"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/web"
)

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	Go      string `json:"go"`
	OS      string `json:"os"`
	Arch    string `json:"arch"`
	UI      bool   `json:"ui"`
}

func currentVersion() versionInfo {
	return versionInfo{
		Version: buildinfo.Version,
		Commit:  buildinfo.Commit,
		Go:      runtime.Version(),
		OS:      runtime.GOOS,
		Arch:    runtime.GOARCH,
		// Reading web.Dist here is what keeps the UI in the binary: the linker
		// drops an embed.FS that no reachable code reads.
		UI: uiEmbedded(web.Dist),
	}
}

func uiEmbedded(dist fs.FS) bool {
	_, err := fs.Stat(dist, "dist/index.html")
	return err == nil
}

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "darkpanel",
		Short:         "Single-binary fleet manager for unmanaged Linux servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd(), newServeCmd())
	return root
}

func newVersionCmd() *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "version",
		Short: "Print the version, commit, Go version and platform",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return writeVersion(cmd.OutOrStdout(), currentVersion(), asJSON)
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "print the version as a JSON object")
	return cmd
}

func newServeCmd() *cobra.Command {
	var (
		dev    bool
		listen []string
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller and the local agent",
		Args:  cobra.NoArgs,
		RunE: func(*cobra.Command, []string) error {
			return errors.New("serve: the controller is not assembled yet")
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listen, "listen", nil,
		"listener to open instead of the defaults (repeatable): loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>")
	return cmd
}

func writeVersion(w io.Writer, v versionInfo, asJSON bool) error {
	if asJSON {
		return json.NewEncoder(w).Encode(v)
	}
	_, err := fmt.Fprintf(w, "darkpanel %s (commit %s, %s, %s/%s)\n", v.Version, v.Commit, v.Go, v.OS, v.Arch)
	return err
}

func main() {
	if err := newRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "darkpanel:", err)
		os.Exit(1)
	}
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./cmd/darkpanel/ ./internal/buildinfo/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./cmd/... ./internal/buildinfo/`
Expected: `ok  	github.com/darkraise/darkpanel/cmd/darkpanel` and `?   	github.com/darkraise/darkpanel/internal/buildinfo	[no test files]`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/buildinfo/buildinfo.go cmd/darkpanel/main.go cmd/darkpanel/version_test.go
git commit -m "feat(cli): move version to buildinfo, stub serve"
```

---

### Task 5: Paths

**Files:**
- Create: `internal/paths/paths.go`
- Test: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: nothing.
- Produces: Contracts › Other packages › `internal/paths`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/paths/paths_test.go`:

```go
package paths

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestResolve(t *testing.T) {
	noHome := func() (string, error) { return "", errors.New("no home") }
	home := func() (string, error) { return "/home/ada", nil }
	tests := []struct {
		name string
		dev  bool
		env  map[string]string
		home func() (string, error)
		want Paths
	}{
		{"production ignores the environment", false, map[string]string{"XDG_STATE_HOME": "/s", "XDG_RUNTIME_DIR": "/r"}, noHome, Paths{
			ControllerDir:    "/var/lib/darkpanel/controller",
			AgentDir:         "/var/lib/darkpanel/agent",
			ControllerKey:    "/etc/darkpanel/controller.key",
			AgentKey:         "/etc/darkpanel/agent.key",
			ControllerSocket: "/run/darkpanel-controller/controller.sock",
		}},
		{"dev with both variables", true, map[string]string{"XDG_STATE_HOME": "/s", "XDG_RUNTIME_DIR": "/r"}, noHome, Paths{
			ControllerDir:    "/s/darkpanel-dev/controller",
			AgentDir:         "/s/darkpanel-dev/agent",
			ControllerKey:    "/s/darkpanel-dev/keys/controller.key",
			AgentKey:         "/s/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/r/darkpanel-dev/controller.sock",
		}},
		{"dev defaults", true, map[string]string{}, home, Paths{
			ControllerDir:    "/home/ada/.local/state/darkpanel-dev/controller",
			AgentDir:         "/home/ada/.local/state/darkpanel-dev/agent",
			ControllerKey:    "/home/ada/.local/state/darkpanel-dev/keys/controller.key",
			AgentKey:         "/home/ada/.local/state/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
		}},
		{"dev ignores relative values", true, map[string]string{"XDG_STATE_HOME": "rel", "XDG_RUNTIME_DIR": "rel"}, home, Paths{
			ControllerDir:    "/home/ada/.local/state/darkpanel-dev/controller",
			AgentDir:         "/home/ada/.local/state/darkpanel-dev/agent",
			ControllerKey:    "/home/ada/.local/state/darkpanel-dev/keys/controller.key",
			AgentKey:         "/home/ada/.local/state/darkpanel-dev/keys/agent.key",
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolve(tt.dev, func(k string) string { return tt.env[k] }, tt.home)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if got != tt.want {
				t.Errorf("resolve = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestResolveDevWithoutHome(t *testing.T) {
	if _, err := resolve(true, func(string) string { return "" }, func() (string, error) { return "", errors.New("no home") }); err == nil {
		t.Fatal("resolve succeeded without XDG_STATE_HOME or a home directory")
	}
}

func TestDerivedPaths(t *testing.T) {
	p := Under("/x")
	for got, want := range map[string]string{
		p.ControllerDB():      "/x/controller/controller.db",
		p.AgentDB():           "/x/agent/agent.db",
		p.ControllerBackups(): "/x/controller/backups",
		p.AgentBackups():      "/x/agent/backups",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}

func TestMkdirAll(t *testing.T) {
	p := Under(filepath.Join(t.TempDir(), "dp"))
	if err := p.MkdirAll(); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	for _, d := range []string{p.ControllerDir, p.AgentDir, filepath.Dir(p.ControllerKey), filepath.Dir(p.ControllerSocket)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: dir %v mode %o, want a 0700 directory", d, info.IsDir(), info.Mode().Perm())
		}
	}
	if err := p.MkdirAll(); err != nil {
		t.Errorf("second MkdirAll: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/paths/`
Expected: FAIL — the build reports `undefined: resolve` and `undefined: Paths`.

- [ ] **Step 3: Write the implementation**

Create `internal/paths/paths.go`:

```go
// Package paths resolves every file and directory the controller and agent
// halves use (spec section 1). Components take a Paths value, so tests pass
// temporary paths.
package paths

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Paths are the locations of one installation.
type Paths struct {
	ControllerDir    string // controller.db and backups/
	AgentDir         string // agent.db and backups/
	ControllerKey    string
	AgentKey         string
	ControllerSocket string
}

// ControllerDB is the controller database file.
func (p Paths) ControllerDB() string { return filepath.Join(p.ControllerDir, "controller.db") }

// AgentDB is the agent database file.
func (p Paths) AgentDB() string { return filepath.Join(p.AgentDir, "agent.db") }

// ControllerBackups is where migrations back up the controller database.
func (p Paths) ControllerBackups() string { return filepath.Join(p.ControllerDir, "backups") }

// AgentBackups is where migrations back up the agent database.
func (p Paths) AgentBackups() string { return filepath.Join(p.AgentDir, "backups") }

// Production is the layout of an installed controller host.
func Production() Paths {
	return Paths{
		ControllerDir:    "/var/lib/darkpanel/controller",
		AgentDir:         "/var/lib/darkpanel/agent",
		ControllerKey:    "/etc/darkpanel/controller.key",
		AgentKey:         "/etc/darkpanel/agent.key",
		ControllerSocket: "/run/darkpanel-controller/controller.sock",
	}
}

// Under is the development layout rooted at root, with the socket at
// root/run/controller.sock.
func Under(root string) Paths {
	return Paths{
		ControllerDir:    filepath.Join(root, "controller"),
		AgentDir:         filepath.Join(root, "agent"),
		ControllerKey:    filepath.Join(root, "keys", "controller.key"),
		AgentKey:         filepath.Join(root, "keys", "agent.key"),
		ControllerSocket: filepath.Join(root, "run", "controller.sock"),
	}
}

// Resolve returns the production layout, or with dev the layout under
// $XDG_STATE_HOME/darkpanel-dev with the socket in
// $XDG_RUNTIME_DIR/darkpanel-dev when that variable is set.
func Resolve(dev bool) (Paths, error) {
	return resolve(dev, os.Getenv, os.UserHomeDir)
}

func resolve(dev bool, getenv func(string) string, home func() (string, error)) (Paths, error) {
	if !dev {
		return Production(), nil
	}
	// The XDG base directory specification says relative values are invalid
	// and must be ignored.
	state := getenv("XDG_STATE_HOME")
	if !filepath.IsAbs(state) {
		h, err := home()
		if err != nil {
			return Paths{}, fmt.Errorf("paths: XDG_STATE_HOME is unset and the home directory is unknown: %w", err)
		}
		if !filepath.IsAbs(h) {
			return Paths{}, errors.New("paths: XDG_STATE_HOME is unset and the home directory is not absolute")
		}
		state = filepath.Join(h, ".local", "state")
	}
	p := Under(filepath.Join(state, "darkpanel-dev"))
	if runtime := getenv("XDG_RUNTIME_DIR"); filepath.IsAbs(runtime) {
		p.ControllerSocket = filepath.Join(runtime, "darkpanel-dev", "controller.sock")
	}
	return p, nil
}

// MkdirAll creates every directory of p that is missing, with mode 0700.
// Existing directories keep their mode.
func (p Paths) MkdirAll() error {
	dirs := []string{
		p.ControllerDir, p.AgentDir,
		filepath.Dir(p.ControllerKey), filepath.Dir(p.AgentKey), filepath.Dir(p.ControllerSocket),
	}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("paths: create %s: %w", d, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/paths/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/paths/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/paths`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/paths/paths.go internal/paths/paths_test.go
git commit -m "feat(paths): resolve production and dev paths"
```

---

### Task 6: Module contract

**Files:**
- Create: `internal/core/contract.go`
- Test: `internal/core/contract_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/store` › Task 2 (`store.DB`, `store.SideController`, `store.SideAgent`).
- Produces: Contracts › `internal/core` › Task 6 — the declarations of spec section 2a that every later part codes against. Do not rename, reorder fields of, or retype any declaration: spec section 2a fixes them.

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/core/contract_test.go`:

```go
package core

import (
	"testing"
	"time"
)

func TestScopeAllows(t *testing.T) {
	tests := []struct {
		have, need Scope
		want       bool
	}{
		{ScopeRead, ScopeRead, true},
		{ScopeRead, ScopeOperate, false},
		{ScopeOperate, ScopeRead, true},
		{ScopeOperate, ScopeAdmin, false},
		{ScopeAdmin, ScopeOperate, true},
		{Scope(""), ScopeRead, false},
		{Scope("root"), ScopeRead, false},
	}
	for _, tt := range tests {
		if got := tt.have.Allows(tt.need); got != tt.want {
			t.Errorf("%q.Allows(%q) = %v, want %v", tt.have, tt.need, got, tt.want)
		}
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string]int{"v1": 1, "v2": 2, "v10": 10} {
		got, err := ParseVersion(in)
		if err != nil || got != want {
			t.Errorf("ParseVersion(%q) = %d, %v; want %d", in, got, err, want)
		}
	}
	for _, in := range []string{"", "1", "v", "v0", "v-1", "v01", "V1", "v1x"} {
		if _, err := ParseVersion(in); err == nil {
			t.Errorf("ParseVersion(%q) succeeded, want an error", in)
		}
	}
}

func TestActionRefString(t *testing.T) {
	if got := (ActionRef{"core.token.create", 1}).String(); got != "core.token.create/v1" {
		t.Errorf("String() = %q", got)
	}
}

func TestSystemClock(t *testing.T) {
	var c Clock = SystemClock{}
	if d := time.Since(c.Now()); d < 0 || d > time.Minute {
		t.Errorf("Now() is %v away from time.Now()", d)
	}
	fired := make(chan struct{})
	c.AfterFunc(time.Millisecond, func() { close(fired) })
	select {
	case <-fired:
	case <-time.After(5 * time.Second):
		t.Fatal("AfterFunc did not fire within 5s")
	}
	stopped := c.AfterFunc(time.Hour, func() { t.Error("stopped timer fired") })
	if !stopped.Stop() {
		t.Error("Stop() = false for a pending timer")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/`
Expected: FAIL — the build reports `undefined: Scope` (and the other missing names).

- [ ] **Step 3: Write the implementation**

Create `internal/core/contract.go`:

```go
// Package core is the module contract every Darkpanel component builds on:
// action and resource definitions, the registry, and the interfaces between
// the controller, the agent, the lifecycle engine and the API (spec section
// 2a). Changing a declaration here changes a contract other parts depend on.
package core

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"log/slog"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// ModuleIDCore is the module id of the core's own actions (spec section 2).
const ModuleIDCore = "core"

// Side is where an action executes.
type Side string

// The two sides.
const (
	SideController Side = store.SideController
	SideAgent      Side = store.SideAgent
)

// Class is an action's change class (shared contracts).
type Class string

// The four action classes.
const (
	ClassRead         Class = "read"
	ClassRevertible   Class = "revertible"
	ClassCompensable  Class = "compensable"
	ClassIrreversible Class = "irreversible"
)

// Scope is what a principal may do (spec section 10).
type Scope string

// The three scopes, in increasing order of power.
const (
	ScopeRead    Scope = "read"
	ScopeOperate Scope = "operate"
	ScopeAdmin   Scope = "admin"
)

// Allows reports whether s includes need.
func (s Scope) Allows(need Scope) bool {
	rank := map[Scope]int{ScopeRead: 1, ScopeOperate: 2, ScopeAdmin: 3}
	return rank[s] > 0 && rank[s] >= rank[need]
}

// ActionRef names one version of an action.
type ActionRef struct {
	Name    string
	Version int
}

// String renders the reference as "<name>/v<version>".
func (r ActionRef) String() string { return fmt.Sprintf("%s/v%d", r.Name, r.Version) }

// ParseVersion parses a path segment such as "v1" into 1.
func ParseVersion(s string) (int, error) {
	n, err := strconv.Atoi(strings.TrimPrefix(s, "v"))
	if !strings.HasPrefix(s, "v") || err != nil || n < 1 || strconv.Itoa(n) != s[1:] {
		return 0, fmt.Errorf("version %q: want v followed by a positive integer, such as v1", s)
	}
	return n, nil
}

// Principal is who is calling.
type Principal struct {
	Actor   string // shared-contract actor, e.g. "user:<id>", "token:<id>", "local:root@<host id>", "system:recovery"
	UserID  string // empty for local and system actors
	TokenID string // set only for token principals
	Scope   Scope  // local break-glass and system actors carry ScopeAdmin
	Source  string // client address, or "socket"
}

// Env is what a handler may use. HostID is empty for controller-side actions;
// Agents is nil on the agent side.
type Env struct {
	HostID    string
	Principal Principal
	DB        *store.DB
	Exec      Runner
	Files     FileWriter
	Sealer    Sealer
	Clock     Clock
	Log       *slog.Logger
	Agents    HostDirectory
}

// HostDirectory finds the agent of a host; dispatch implements it.
type HostDirectory interface {
	Agent(ctx context.Context, hostID string) (AgentClient, error)
}

// DiffLine is one line of a plan's human-readable diff.
type DiffLine struct {
	Op   string `json:"op"` // "+", "-" or " "
	Text string `json:"text"`
}

// Guard is one safety check a plan ran.
type Guard struct {
	ID      string `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// PlanOutcome is what a handler's Plan returns.
type PlanOutcome struct {
	Diff         []DiffLine
	Guards       []Guard
	StateHash    string
	Confirmation string // irreversible actions only: the phrase apply must repeat
}

// ActionSpec is an action's declaration (ADR 0002).
type ActionSpec struct {
	Name, Summary string
	Version       int
	Class         Class
	Side          Side
	BreakGlass    bool
	LockKeys      []string
	CompensatedBy string // compensable only, an action name
}

// Reader is the handler of a read action.
type Reader[In, Out any] interface {
	Read(ctx context.Context, env Env, in In) (Out, error)
}

// Mutator is the handler of a compensable or irreversible action.
type Mutator[In, Out any] interface {
	Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
	Apply(ctx context.Context, env Env, in In) (Out, error)
	Verify(ctx context.Context, env Env, in In, out Out) error
}

// Reverter is the handler of a revertible action.
type Reverter[In, Out any] interface {
	Mutator[In, Out]
	Snapshot(ctx context.Context, env Env, in In) ([]byte, error)
	Revert(ctx context.Context, env Env, snapshot []byte) error
}

// ErasedHandler is a handler working on JSON. DefineRead, DefineMutating and
// DefineRevertible build it; the fields a class does not use are nil.
type ErasedHandler struct {
	Read     func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Plan     func(ctx context.Context, env Env, in json.RawMessage) (PlanOutcome, error)
	Apply    func(ctx context.Context, env Env, in json.RawMessage) (json.RawMessage, error)
	Verify   func(ctx context.Context, env Env, in, out json.RawMessage) error
	Snapshot func(ctx context.Context, env Env, in json.RawMessage) ([]byte, error)
	Revert   func(ctx context.Context, env Env, snapshot []byte) error
}

// ActionDef is a type-erased action as the registry stores it.
type ActionDef struct {
	Spec       ActionSpec
	InputType  reflect.Type
	OutputType reflect.Type
	Handler    ErasedHandler
}

// Ref is the action's name and version.
func (a ActionDef) Ref() ActionRef { return ActionRef{a.Spec.Name, a.Spec.Version} }

// ResourceDef is a read endpoint without input, named "<module>.<resource>",
// whose schema version is fixed at 1.
type ResourceDef struct {
	Name       string
	OutputType reflect.Type
	Read       func(ctx context.Context, env Env) (json.RawMessage, error)
}

// PreflightCheck is a host check; the error text is the reason shown when it
// fails.
type PreflightCheck struct {
	ID  string
	Run func(ctx context.Context, env Env) error
}

// OwnedPath is a file or marked-block file a module may write (ADR 0009).
type OwnedPath struct {
	Path          string
	Kind          string // "file" or "block"
	BlockIDs      []string
	CommentPrefix string // default "#"
}

// Manifest is everything a module declares (R5).
type Manifest struct {
	ID             string
	Version        int
	Actions        []ActionDef
	Resources      []ResourceDef
	Settings       reflect.Type
	Preflight      []PreflightCheck
	OwnedPaths     []OwnedPath
	Uninstall      func(ctx context.Context, env Env) error
	Migrations     map[Side]fs.FS
	DefaultEnabled bool
}

// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
}

// Sealer encrypts and decrypts secrets with associated data (ADR 0005).
type Sealer interface {
	Seal(ad string, plaintext []byte) ([]byte, error)
	Open(ad string, ciphertext []byte) ([]byte, error)
	Derive(label string) []byte
}

// Validator checks an action's input against its schema; it returns an
// *errcode.Error with field errors.
type Validator interface {
	ValidateInput(def ActionDef, raw json.RawMessage) error
}

// Clock is the time source; tests use coretest.FakeClock.
type Clock interface {
	Now() time.Time
	AfterFunc(d time.Duration, f func()) Timer
}

// Timer is a pending AfterFunc.
type Timer interface {
	Stop() bool
}

// SystemClock is the production Clock. time.AfterFunc measures on the
// monotonic clock, so a wall-clock step never fires or delays a revert timer.
type SystemClock struct{}

// Now returns the current time.
func (SystemClock) Now() time.Time { return time.Now() }

// AfterFunc calls f in its own goroutine after d.
func (SystemClock) AfterFunc(d time.Duration, f func()) Timer { return time.AfterFunc(d, f) }

// Cmd is a program to run with an argument vector (ADR 0009).
type Cmd struct {
	Path  string
	Args  []string
	Stdin []byte
}

// CmdResult is a finished program's output.
type CmdResult struct {
	Stdout, Stderr []byte
	ExitCode       int
}

// Runner runs allowlisted programs.
type Runner interface {
	Run(ctx context.Context, cmd Cmd) (CmdResult, error)
}

// FileWriter writes manifest-declared files and marked blocks.
type FileWriter interface {
	WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error
	ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error
	RemoveBlock(ctx context.Context, module, path, blockID string) error
	Remove(ctx context.Context, module, path string) error
}

// OwnedEntry is one path or block Darkpanel wrote.
type OwnedEntry struct {
	Path, Module, Kind, BlockID string
	CreatedAt                   time.Time
}

// Ownership records what Darkpanel wrote, for uninstall.
type Ownership interface {
	Record(ctx context.Context, e OwnedEntry) error
	Forget(ctx context.Context, module, path, blockID string) error
	List(ctx context.Context) ([]OwnedEntry, error)
}

// JournalEntry is one lifecycle event of a change.
type JournalEntry struct {
	Seq       int64 // assigned by the agent journal; 0 on the controller side
	At        time.Time
	Actor     string
	Action    string
	Version   int
	Phase     string // "apply", "confirm", "revert", "recover", "interrupt"
	ChangeID  string
	Input     json.RawMessage // already redacted
	Result    string          // "ok", "error"
	ErrorCode string
}

// Journal appends lifecycle events inside the change's transaction.
type Journal interface {
	Append(ctx context.Context, tx *sql.Tx, e JournalEntry) error
}

// AuditEntry is one row of the audit log (spec section 11).
type AuditEntry struct {
	ID, Actor, HostID, Action, Phase, ChangeID, Result, ErrorCode, Source string
	At                                                                    time.Time
	Version                                                               int
	Seq                                                                   int64 // the agent journal sequence, 0 for controller-side rows
	Input                                                                 json.RawMessage
}

// AuditQuery filters core.audit.list.
type AuditQuery struct {
	Limit                         int
	Before, Actor, HostID, Action string
}

// Audit is the controller's audit log.
type Audit interface {
	Journal
	Record(ctx context.Context, e AuditEntry) error
	Ingest(ctx context.Context, hostID string, entries []JournalEntry) error
	List(ctx context.Context, q AuditQuery) (entries []AuditEntry, nextBefore string, err error)
}

// PlanView is a stored plan as callers see it.
type PlanView struct {
	PlanID, HostID, Action, InputHash, Confirmation string
	Version                                         int
	Diff                                            []DiffLine
	Guards                                          []Guard
	ExpiresAt                                       time.Time
}

// ChangeError is a failed change's error.
type ChangeError struct {
	Code, Message string
}

// ChangeView is a change as callers see it.
type ChangeView struct {
	ID, HostID, Action, Actor, CompensatedBy string
	Version                                  int
	Class                                    Class
	State                                    string
	CreatedAt                                time.Time
	UpdatedAt                                time.Time
	RevertDeadline                           *time.Time
	Output                                   json.RawMessage // secret fields present only in the apply response that created them
	Error                                    *ChangeError
}

// The change states (spec section 3).
const (
	StateApplying    = "applying"
	StatePending     = "pending"
	StateConfirmed   = "confirmed"
	StateReverted    = "reverted"
	StateCompleted   = "completed"
	StateFailed      = "failed"
	StateInterrupted = "interrupted"
)

// ApplyOptions carry apply's per-class options.
type ApplyOptions struct {
	ConfirmTimeout time.Duration
	Confirmation   string
	ConfirmNow     bool
}

// ChangeFilter filters change listings.
type ChangeFilter struct {
	State, HostID, Before string
	Limit                 int
}

// ModuleStatus is one module's availability on a host.
type ModuleStatus struct {
	ID        string
	Version   int
	Available bool
	Reason    string
}

// AgentHello is what an agent reports about itself (ADR 0003).
type AgentHello struct {
	ProtocolVersion int
	BinaryVersion   string
	OS              string
	HostID          string
	Modules         []ModuleStatus
}

// AgentClient is the controller's view of one agent; in M1 the in-process
// client, from M2 the mTLS transport.
type AgentClient interface {
	Hello(ctx context.Context) (AgentHello, error)
	Read(ctx context.Context, p Principal, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, a ActionRef, input json.RawMessage) (PlanView, error)
	Apply(ctx context.Context, p Principal, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, changeID string) (ChangeView, error)
	Changes(ctx context.Context, f ChangeFilter) ([]ChangeView, error)
	Preflight(ctx context.Context, moduleID string) (ModuleStatus, error)
	Journal(ctx context.Context, afterSeq int64, limit int) ([]JournalEntry, error)
	Ack(ctx context.Context, seq int64) error
}

// Dispatcher is what the HTTP API and the controller socket call. hostID is
// empty for controller-side actions.
type Dispatcher interface {
	Read(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (json.RawMessage, error)
	Resource(ctx context.Context, p Principal, hostID, name string) (json.RawMessage, error)
	Plan(ctx context.Context, p Principal, hostID string, a ActionRef, input json.RawMessage) (PlanView, error)
	Apply(ctx context.Context, p Principal, hostID string, a ActionRef, planID string, opts ApplyOptions) (ChangeView, error)
	Confirm(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Revert(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Change(ctx context.Context, p Principal, changeID string) (ChangeView, error)
	Changes(ctx context.Context, p Principal, f ChangeFilter) ([]ChangeView, error)
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/ && GOTOOLCHAIN=local timeout 300 go vet ./internal/core/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core`, vet prints nothing, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/contract.go internal/core/contract_test.go
git commit -m "feat(core): declare the module contract"
```

---

### Task 7: Makefile and lint configuration

**Files:**
- Modify: `Makefile` (whole file below)
- Modify: `.golangci.yml` (whole file below)

**Interfaces:**
- Consumes: Contracts › Other packages › `internal/buildinfo` (Task 4).
- Produces: Contracts › Other packages › `Makefile`.

**Items:** 27

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Run the failing check**

In the task's fresh worktree no binary has been built yet. Run: `timeout 900 make verify-build`
Expected: FAIL — `file` reports `bin/darkpanel-linux-amd64: cannot open` and the recipe stops, because `verify-build` does not build first.

- [ ] **Step 2: Write the Makefile**

Replace `Makefile` with exactly this content (recipe lines start with a tab):

```make
SHELL := bash
.SHELLFLAGS := -euo pipefail -c
.DEFAULT_GOAL := build

VERSION ?= $(shell git describe --tags --always --dirty)
COMMIT ?= $(shell git rev-parse --short HEAD)
ARCHES := amd64 arm64
BUILDINFO := github.com/darkraise/darkpanel/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT)
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>

# ADR 0001: build only with the toolchain go.mod pins, never a downloaded one.
export GOTOOLCHAIN := local

.PHONY: web-deps web build verify-build lint-go lint-web lint test-go test-web test-adr test vuln check clean

web-deps:
	pnpm -C web install --frozen-lockfile

web: web-deps
	pnpm -C web build

build: web
	for arch in $(ARCHES); do \
		CGO_ENABLED=0 GOOS=linux GOARCH=$$arch go build -trimpath -ldflags "$(LDFLAGS)" \
			-o bin/darkpanel-linux-$$arch ./cmd/darkpanel; \
	done

verify-build: build
	file bin/darkpanel-linux-amd64 | grep -F 'ELF 64-bit LSB executable, x86-64' | grep -qF 'statically linked'
	file bin/darkpanel-linux-arm64 | grep -F 'ELF 64-bit LSB executable, ARM aarch64' | grep -qF 'statically linked'
	for arch in $(ARCHES); do grep -qaF '$(UI_MARKER)' bin/darkpanel-linux-$$arch; done
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/
	bin/darkpanel-linux-$$(go env GOARCH) version --json | grep -qF '"commit":"$(COMMIT)"'

lint-go:
	golangci-lint run
	go vet ./...

lint-web: web-deps
	pnpm -C web lint
	pnpm -C web typecheck

lint: lint-go lint-web

test-go:
	go test ./...

test-web: web-deps
	pnpm -C web test

test-adr:
	bash scripts/check-adrs.test.sh
	bash scripts/check-adrs.sh --complete

test: test-go test-web test-adr

vuln:
	go run $(GOVULNCHECK) ./...

check: lint test build verify-build vuln

clean:
	rm -rf bin
	find web/dist -mindepth 1 ! -name .gitkeep -delete
```

- [ ] **Step 3: Write the lint configuration**

Replace `.golangci.yml` with:

```yaml
version: "2"

run:
  build-tags:
    - harness

linters:
  default: standard
  enable:
    - errorlint
    - gosec
    - misspell
    - revive

formatters:
  enable:
    - gofmt
    - goimports
```

- [ ] **Step 4: Run the checks to verify they pass**

Run: `timeout 900 make verify-build && timeout 600 make lint-go && timeout 600 make lint-web test-web`
Expected: `verify-build` installs the web dependencies, builds both binaries, prints `ok  	github.com/darkraise/darkpanel/web`, and its last line (the commit check) exits 0; `lint-go` prints `0 issues.` and vet prints nothing; ESLint and `tsc --noEmit` print no findings and the Vitest `Tests` line has no `failed`.

Then confirm the binary carries the commit: `bin/darkpanel-linux-amd64 version --json` prints `"commit":"<the output of git rev-parse --short HEAD>"`.

- [ ] **Step 5: Commit**

```bash
git add Makefile .golangci.yml
git commit -m "chore(build): stamp buildinfo, add web-deps target"
```

---

### Task 8: ADRs 0010 and 0011

**Files:**
- Create: `docs/adr/0010-web-certificate-authority.md`
- Create: `docs/adr/0011-actors-cli-names-and-layout.md`
- Modify: `docs/adr/README.md` (index, actor list, three traceability rows)

**Interfaces:**
- Consumes: nothing.
- Produces: Contracts › Other packages › ADRs.

**Items:** 19, 29

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Run the failing check**

Run: `timeout 60 bash scripts/check-adrs.sh docs/adr/0010-web-certificate-authority.md docs/adr/0011-actors-cli-names-and-layout.md`
Expected: FAIL with `check-adrs: docs/adr/0010-web-certificate-authority.md: no such file` and the same for 0011.

- [ ] **Step 2: Write ADR 0010**

Create `docs/adr/0010-web-certificate-authority.md`:

````markdown
# ADR 0010: Web certificate authority

**Requirements:** R20

## Status

Proposed

## Context

ADR 0006 has the Tailscale web listener present a certificate from the
internal authority when `tailscale cert` is not available, and ADR 0003 makes
that authority Ed25519. Browsers do not accept Ed25519 keys in X.509
certificate chains, so the two decisions together would make the Tailscale
web listener unusable in a browser.

M1 serves the web UI over HTTPS on the Tailscale addresses before the agent
authority exists (M2) and before `tailscale cert` can be obtained through the
agent (M5). Accepted ADRs are not edited, so the change is recorded here.

## Decision

- A separate root for web certificates only: ECDSA P-256, valid 10 years,
  created on the controller's first start. Its private key is sealed with the
  controller key and stored in `controller.db` (table `core_ca`, purpose
  `web`), as ADR 0005 stores the certificate authority key.
- The root issues one ECDSA P-256 leaf for the HTTPS listeners, valid 90 days.
  It is renewed after 60 days, on any change of the Tailscale addresses and on
  any change of the HTTPS bind addresses. Its SANs are the hostname,
  `localhost`, the Tailscale IP addresses and every explicit HTTPS bind
  address. The leaf lives in memory only; HTTPS listeners present the chain
  `[leaf, root]`.
- `darkpanel controller web-ca` prints the root certificate in PEM and its
  SHA-256 fingerprint through the controller socket, so the operator can trust
  it in a browser and pin it in the CLI.
- Agent identity stays on the Ed25519 authority of ADR 0003 from M2. ADR
  0006's `tailscale cert` path is unchanged; this root serves the case where
  that certificate is not available.

## Alternatives considered

- Ed25519 web certificates from the ADR 0003 authority: rejected; browsers
  reject them.
- One ECDSA authority for web and agent certificates: rejected. It would
  reopen ADR 0003, and a root that operators install in their browsers would
  also vouch for agents.
- A self-signed leaf without a root: rejected. Every renewal would need a new
  browser exception and a new CLI pin.

## Consequences

- An operator trusts one root per controller, once, in the browser and as
  `caFingerprint` in the CLI configuration; leaf renewals need no action.
- Losing `controller.key` loses the web root's key with the rest of the
  sealed data; a new root must then be trusted again.
- The MagicDNS name joins the SANs when M5 reads it from the Tailscale API.
````

- [ ] **Step 3: Write ADR 0011**

Create `docs/adr/0011-actors-cli-names-and-layout.md`:

````markdown
# ADR 0011: Actors, reserved CLI names and package layout

**Requirements:** R7, R18

## Status

Proposed

## Context

The M1 platform core design
(`docs/dforge/specs/2026-10-17-m1-platform-core-design.md`) needs two
audit actors that the shared contracts do not define, top-level CLI commands
for core actions that ADR 0006's reserved names do not cover, and packages
that ADR 0001's layout does not name. Accepted ADRs are not edited, so the
additions are recorded here.

## Decision

- **Actors.** Two actors join the shared contracts' list:
  - `anonymous`: a failed login whose username matched no user. The audit
    row's input holds the submitted username.
  - `local:uid-<n>@<host id>`: a caller on the controller socket whose uid is
    not 0, accepted only in `--dev` mode.
- **Reserved CLI names.** `host`, `module`, `token`, `audit`, `help` and
  `completion` join ADR 0006's reserved top-level names. Core actions (module
  id `core`) are exposed without the module segment, as
  `darkpanel <noun> <verb>`, so their nouns must not collide with a module id;
  `help` and `completion` are commands cobra adds itself. A module id that is
  a reserved name stops startup.
- **Package layout.** Additions to ADR 0001's layout:

```
internal/core/store/            SQLite, migrations and backups for both roles
internal/core/coretest/         fakes of the core interfaces for tests
internal/paths/                 file and directory locations of both roles
internal/buildinfo/             version and commit stamped by the build
internal/registry/              builds the one module registry
internal/cli/                   the CLI generated from the registry
internal/controller/api/        HTTP API and controller socket
internal/controller/auth/       users, passwords, TOTP, sessions, tokens
internal/controller/listen/     listeners and the web authority (ADR 0010)
internal/controller/dispatch/   routes actions to the controller or an agent
internal/controller/audit/      audit log and agent journal ingestion
internal/controller/coreactions/ the core module's actions
internal/controller/server/     startup order of the controller
internal/agent/runtime/         the agent half: database, engine, preflight
test/harness/                   VM integration harness (ADR 0008)
```

- **Moves** relative to ADR 0001: the database code is
  `internal/core/store`, shared by both roles, instead of a controller-only
  package; the audit log is `internal/controller/audit`, while the agent
  journal stays with the change lifecycle under `internal/core`; the web
  authority lives in `internal/controller/listen`.

## Alternatives considered

- Editing ADR 0001 and ADR 0006: rejected; they are Accepted, and the index's
  lifecycle requires a new ADR for a change.
- Recording a failed login for an unknown username with actor
  `user:<submitted name>`: rejected; it would mix user ids and names in one
  field and look like a real user.
- A `core` segment on core commands (`darkpanel core token create`):
  rejected; ADR 0006 already reserves `user` and `change` as top-level
  commands, and the core's other nouns would be inconsistent with them.

## Consequences

- The shared contracts' actor list in the index points to this ADR for the
  two additions.
- A module can never take a reserved name as its id; the registry refuses it.
- Later epics place files by ADR 0001 together with this layout.
````

- [ ] **Step 4: Update the index**

Make four replacements in `docs/adr/README.md`; nothing else in the file changes.

Replace the index line

```markdown
- [ADR 0009: Host execution safety](0009-host-execution-safety.md)
```

with

```markdown
- [ADR 0009: Host execution safety](0009-host-execution-safety.md)
- [ADR 0010: Web certificate authority](0010-web-certificate-authority.md)
- [ADR 0011: Actors, reserved CLI names and package
  layout](0011-actors-cli-names-and-layout.md)
```

Replace the shared-contracts actor bullet

```markdown
- **Actors.** Audit actors are `user:<id>`, `token:<id>`, `agent:<host id>`,
  `local:root@<host id>` and `system:<job>`.
```

with

```markdown
- **Actors.** Audit actors are `user:<id>`, `token:<id>`, `agent:<host id>`,
  `local:root@<host id>` and `system:<job>`; ADR 0011 adds `anonymous` and
  `local:uid-<n>@<host id>`.
```

In the Traceability table, replace these three rows

```markdown
| R7 | 0002 (action declaration), 0006 (REST, CLI) |
| R18 | 0003 (journal delivery), 0004 (apply ordering, compensable and irreversible actions, controller-side actions) |
| R20 | 0006 (listeners) |
```

with (each row stays in its place in the table)

```markdown
| R7 | 0002 (action declaration), 0006 (REST, CLI), 0011 (reserved CLI names) |
| R18 | 0003 (journal delivery), 0004 (apply ordering, compensable and irreversible actions, controller-side actions), 0011 (actors) |
| R20 | 0006 (listeners), 0010 (web authority) |
```

- [ ] **Step 5: Run the checks to verify they pass**

Run: `timeout 60 bash scripts/check-adrs.test.sh && timeout 60 bash scripts/check-adrs.sh --complete`
Expected: `check-adrs tests: 21 passed, 0 failed`, then no `check-adrs:` line and exit 0.

- [ ] **Step 6: Commit**

```bash
git add docs/adr/0010-web-certificate-authority.md docs/adr/0011-actors-cli-names-and-layout.md docs/adr/README.md
git commit -m "docs(adr): add ADRs 0010 and 0011"
```

---

### Task 9: Action definitions and registry

**Files:**
- Create: `internal/core/define.go`
- Create: `internal/core/registry.go`
- Test: `internal/core/registry_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core` › Task 6; Contracts › `internal/core/errcode` (Task 3).
- Produces: Contracts › `internal/core` › Task 9. The registration rules are spec section 2's; a violation stops startup.

**Items:** 1

**Depends on:** Task 3, Task 6

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/core/registry_test.go`:

```go
package core

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type echoIn struct {
	Name string `json:"name"`
}

type echoOut struct {
	Greeting string `json:"greeting"`
}

type echo struct{}

func (echo) Read(_ context.Context, _ Env, in echoIn) (echoOut, error) {
	return echoOut{"hello " + in.Name}, nil
}

func (echo) Plan(_ context.Context, _ Env, in echoIn) (PlanOutcome, error) {
	return PlanOutcome{Diff: []DiffLine{{"+", in.Name}}, StateHash: "h"}, nil
}

func (echo) Apply(_ context.Context, _ Env, in echoIn) (echoOut, error) {
	return echoOut{"applied " + in.Name}, nil
}

func (echo) Verify(_ context.Context, _ Env, _ echoIn, out echoOut) error {
	if !strings.HasPrefix(out.Greeting, "applied ") {
		return errors.New("bad output")
	}
	return nil
}

func (echo) Snapshot(_ context.Context, _ Env, in echoIn) ([]byte, error) {
	return []byte(in.Name), nil
}

func (echo) Revert(context.Context, Env, []byte) error { return nil }

type testModule Manifest

func (m testModule) Manifest() Manifest { return Manifest(m) }

func readAction(name string) ActionDef {
	return DefineRead[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: SideAgent}, echo{})
}

func mutating(name string, class Class, side Side, compensatedBy string) ActionDef {
	return DefineMutating[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Class: class, Side: side, CompensatedBy: compensatedBy}, echo{})
}

func revertible(name string, side Side) ActionDef {
	return DefineRevertible[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: side}, echo{})
}

func TestNewRegistryAccepts(t *testing.T) {
	mod := testModule{
		ID: "demo", Version: 1,
		Actions: []ActionDef{
			readAction("demo.item.get"),
			mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.remove"),
			mutating("demo.item.remove", ClassIrreversible, SideAgent, ""),
			revertible("demo.rule.set", SideAgent),
		},
		Resources: []ResourceDef{DefineResource("demo.summary", func(context.Context, Env) (echoOut, error) { return echoOut{"hi"}, nil })},
	}
	r, err := NewRegistry(mod)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := r.Action(ActionRef{"demo.item.add", 1}); !ok {
		t.Error("demo.item.add/v1 not found")
	}
	if got := len(r.Actions()); got != 4 {
		t.Errorf("len(Actions()) = %d, want 4", got)
	}
	if r.Actions()[0].Spec.Name != "demo.item.add" {
		t.Errorf("Actions() not sorted: first is %s", r.Actions()[0].Spec.Name)
	}
	if _, ok := r.Resource("demo.summary"); !ok {
		t.Error("demo.summary not found")
	}
	if m, ok := r.Module("demo"); !ok || m.ID != "demo" {
		t.Error("Module(demo) not found")
	}
}

func TestNewRegistryRules(t *testing.T) {
	bg := mutating("core.user.create", ClassCompensable, SideAgent, "core.user.create")
	bg.Spec.BreakGlass = true
	locked := readAction("demo.item.get")
	locked.Spec.LockKeys = []string{"demo"}
	noVerify := mutating("demo.item.add", ClassIrreversible, SideAgent, "")
	noVerify.Handler.Verify = nil
	tests := []struct {
		name string
		mods []Module
		want string
	}{
		{"bad module id", []Module{testModule{ID: "Demo", Version: 1}}, "must match"},
		{"reserved module id", []Module{testModule{ID: "token", Version: 1}}, "reserved"},
		{"duplicate module", []Module{testModule{ID: "demo", Version: 1}, testModule{ID: "demo", Version: 1}}, "registered twice"},
		{"module version", []Module{testModule{ID: "demo"}}, "version 0"},
		{"bad action name", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("demo.get")}}}, "must match"},
		{"foreign action prefix", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("other.item.get")}}}, "start with"},
		{"duplicate action", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{readAction("demo.item.get"), readAction("demo.item.get")}}}, "registered twice"},
		{"break-glass on agent", []Module{testModule{ID: "core", Version: 1, Actions: []ActionDef{bg}}}, "break-glass"},
		{"revertible on controller", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{revertible("demo.rule.set", SideController)}}}, "revertible actions are agent-side"},
		{"compensable without compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "")}}}, "name a compensating action"},
		{"unknown compensation", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassCompensable, SideAgent, "demo.item.gone")}}}, "not registered"},
		{"compensation on irreversible", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", ClassIrreversible, SideAgent, "demo.item.add")}}}, "only compensable"},
		{"read with locks", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{locked}}}, "take no locks"},
		{"missing handler", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{noVerify}}}, "Plan, Apply and Verify"},
		{"unknown class", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{mutating("demo.item.add", Class("magic"), SideAgent, "")}}}, "unknown class"},
		{"bad resource name", []Module{testModule{ID: "demo", Version: 1, Resources: []ResourceDef{{Name: "other.summary", Read: func(context.Context, Env) (json.RawMessage, error) { return nil, nil }}}}}, "start with"},
		{"migrations side", []Module{testModule{ID: "demo", Version: 1, Migrations: map[Side]fs.FS{"browser": nil}}}, "unknown side"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mods...)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}

func TestErasedHandlers(t *testing.T) {
	ctx := context.Background()
	read := readAction("demo.item.get")
	out, err := read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"ada"}`))
	if err != nil || string(out) != `{"greeting":"hello ada"}` {
		t.Errorf("Read = %s, %v", out, err)
	}
	_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"ada","extra":1}`))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("unknown field: err = %v, want %s", err, errcode.InvalidInput)
	}
	_, err = read.Handler.Read(ctx, Env{}, json.RawMessage(`{"name":"a"} {}`))
	if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
		t.Errorf("trailing data: err = %v, want %s", err, errcode.InvalidInput)
	}

	rev := revertible("demo.rule.set", SideAgent)
	applied, err := rev.Handler.Apply(ctx, Env{}, json.RawMessage(`{"name":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := rev.Handler.Verify(ctx, Env{}, json.RawMessage(`{"name":"x"}`), applied); err != nil {
		t.Errorf("Verify: %v", err)
	}
	snap, err := rev.Handler.Snapshot(ctx, Env{}, json.RawMessage(`{"name":"x"}`))
	if err != nil || string(snap) != "x" {
		t.Errorf("Snapshot = %q, %v", snap, err)
	}
	if rev.Spec.Class != ClassRevertible {
		t.Errorf("DefineRevertible class = %s", rev.Spec.Class)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/`
Expected: FAIL — the build reports `undefined: DefineRead` and `undefined: NewRegistry`.

- [ ] **Step 3: Write the action constructors**

Create `internal/core/define.go`:

```go
package core

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// decode parses raw into a T, refusing unknown fields and trailing data. An
// empty body decodes as the zero value; callers validate the input against
// its schema first, and validators refuse an empty body.
func decode[T any](raw json.RawMessage) (T, error) {
	var v T
	if len(bytes.TrimSpace(raw)) == 0 {
		return v, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		// A JSON syntax error quotes input bytes, which may belong to a secret
		// field, so the cause stays out of the client-visible detail.
		return v, errcode.Wrap(errcode.InvalidInput, err, "input does not match the action's input type")
	}
	if dec.More() {
		return v, errcode.New(errcode.InvalidInput, "trailing data after the JSON value")
	}
	return v, nil
}

func encode(v any) (json.RawMessage, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode output: %w", err)
	}
	return b, nil
}

func typeOf[T any]() reflect.Type { return reflect.TypeFor[T]() }

// DefineRead declares a read action.
func DefineRead[In, Out any](spec ActionSpec, h Reader[In, Out]) ActionDef {
	spec.Class = ClassRead
	return ActionDef{
		Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](),
		Handler: ErasedHandler{
			Read: func(ctx context.Context, env Env, raw json.RawMessage) (json.RawMessage, error) {
				in, err := decode[In](raw)
				if err != nil {
					return nil, err
				}
				out, err := h.Read(ctx, env, in)
				if err != nil {
					return nil, err
				}
				return encode(out)
			},
		},
	}
}

func mutatorHandler[In, Out any](h Mutator[In, Out]) ErasedHandler {
	return ErasedHandler{
		Plan: func(ctx context.Context, env Env, raw json.RawMessage) (PlanOutcome, error) {
			in, err := decode[In](raw)
			if err != nil {
				return PlanOutcome{}, err
			}
			return h.Plan(ctx, env, in)
		},
		Apply: func(ctx context.Context, env Env, raw json.RawMessage) (json.RawMessage, error) {
			in, err := decode[In](raw)
			if err != nil {
				return nil, err
			}
			out, err := h.Apply(ctx, env, in)
			if err != nil {
				return nil, err
			}
			return encode(out)
		},
		Verify: func(ctx context.Context, env Env, rawIn, rawOut json.RawMessage) error {
			in, err := decode[In](rawIn)
			if err != nil {
				return err
			}
			var out Out
			if err := json.Unmarshal(rawOut, &out); err != nil {
				return fmt.Errorf("decode output for verify: %w", err)
			}
			return h.Verify(ctx, env, in, out)
		},
	}
}

// DefineMutating declares a compensable or irreversible action; spec.Class
// must be one of the two.
func DefineMutating[In, Out any](spec ActionSpec, h Mutator[In, Out]) ActionDef {
	return ActionDef{Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](), Handler: mutatorHandler(h)}
}

// DefineRevertible declares a revertible action.
func DefineRevertible[In, Out any](spec ActionSpec, h Reverter[In, Out]) ActionDef {
	spec.Class = ClassRevertible
	eh := mutatorHandler[In, Out](h)
	eh.Snapshot = func(ctx context.Context, env Env, raw json.RawMessage) ([]byte, error) {
		in, err := decode[In](raw)
		if err != nil {
			return nil, err
		}
		return h.Snapshot(ctx, env, in)
	}
	eh.Revert = h.Revert
	return ActionDef{Spec: spec, InputType: typeOf[In](), OutputType: typeOf[Out](), Handler: eh}
}

// DefineResource declares a resource named "<module>.<resource>".
func DefineResource[Out any](name string, read func(ctx context.Context, env Env) (Out, error)) ResourceDef {
	return ResourceDef{
		Name: name, OutputType: typeOf[Out](),
		Read: func(ctx context.Context, env Env) (json.RawMessage, error) {
			out, err := read(ctx, env)
			if err != nil {
				return nil, err
			}
			return encode(out)
		},
	}
}
```

- [ ] **Step 4: Write the registry**

Create `internal/core/registry.go`:

```go
package core

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// ReservedNames are top-level CLI names no module id may take (ADR 0006 and
// ADR 0011).
var ReservedNames = []string{
	"serve", "agent", "update", "user", "controller", "change", "uninstall", "version",
	"host", "module", "token", "audit", "help", "completion",
}

var (
	moduleIDPattern     = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	actionNamePattern   = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	resourceNamePattern = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
)

// Registry holds every module, action and resource, validated once.
type Registry struct {
	modules   []Manifest
	actions   map[ActionRef]ActionDef
	resources map[string]ResourceDef
	names     map[string]bool // action names, any version
}

// NewRegistry validates the modules against the registration rules (spec
// section 2) and returns the registry, or every violation joined.
func NewRegistry(mods ...Module) (*Registry, error) {
	r := &Registry{
		actions:   map[ActionRef]ActionDef{},
		resources: map[string]ResourceDef{},
		names:     map[string]bool{},
	}
	var errs []error
	fail := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	ids := map[string]bool{}
	for _, mod := range mods {
		m := mod.Manifest()
		switch {
		case !moduleIDPattern.MatchString(m.ID):
			fail("module id %q must match %s", m.ID, moduleIDPattern)
		case slices.Contains(ReservedNames, m.ID):
			fail("module id %q is a reserved CLI name", m.ID)
		case ids[m.ID]:
			fail("module id %q registered twice", m.ID)
		}
		ids[m.ID] = true
		if m.Version < 1 {
			fail("module %s: version %d, want at least 1", m.ID, m.Version)
		}
		for side := range m.Migrations {
			if side != SideController && side != SideAgent {
				fail("module %s: migrations for unknown side %q", m.ID, side)
			}
		}
		for _, a := range m.Actions {
			if err := checkAction(m.ID, a); err != nil {
				errs = append(errs, err)
				continue
			}
			if _, dup := r.actions[a.Ref()]; dup {
				fail("action %s registered twice", a.Ref())
				continue
			}
			r.actions[a.Ref()] = a
			r.names[a.Spec.Name] = true
		}
		for _, res := range m.Resources {
			switch {
			case !resourceNamePattern.MatchString(res.Name) || !strings.HasPrefix(res.Name, m.ID+"."):
				fail("resource %q must match %s and start with %q", res.Name, resourceNamePattern, m.ID+".")
			case res.Read == nil:
				fail("resource %s has no Read", res.Name)
			case r.resources[res.Name].Name != "":
				fail("resource %s registered twice", res.Name)
			default:
				r.resources[res.Name] = res
			}
		}
		r.modules = append(r.modules, m)
	}
	for _, a := range r.actions {
		if a.Spec.Class == ClassCompensable && !r.names[a.Spec.CompensatedBy] {
			fail("action %s: compensating action %q is not registered", a.Ref(), a.Spec.CompensatedBy)
		}
	}
	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}
	return r, nil
}

func checkAction(moduleID string, a ActionDef) error {
	s, h := a.Spec, a.Handler
	ref := a.Ref()
	switch {
	case !actionNamePattern.MatchString(s.Name) || !strings.HasPrefix(s.Name, moduleID+"."):
		return fmt.Errorf("action %q must match %s and start with %q", s.Name, actionNamePattern, moduleID+".")
	case s.Version < 1:
		return fmt.Errorf("action %s: version must be at least 1", s.Name)
	case s.Side != SideController && s.Side != SideAgent:
		return fmt.Errorf("action %s: unknown side %q", ref, s.Side)
	case s.BreakGlass && s.Side != SideController:
		return fmt.Errorf("action %s: break-glass actions are controller-side", ref)
	case s.Class == ClassRevertible && s.Side != SideAgent:
		return fmt.Errorf("action %s: revertible actions are agent-side; controller-side actions have no revert timer", ref)
	case s.Class == ClassCompensable && s.CompensatedBy == "":
		return fmt.Errorf("action %s: compensable actions name a compensating action", ref)
	case s.Class != ClassCompensable && s.CompensatedBy != "":
		return fmt.Errorf("action %s: only compensable actions name a compensating action", ref)
	case s.Class == ClassRead && len(s.LockKeys) > 0:
		return fmt.Errorf("action %s: read actions take no locks", ref)
	}
	switch s.Class {
	case ClassRead:
		if h.Read == nil || h.Plan != nil || h.Apply != nil {
			return fmt.Errorf("action %s: a read action has only a Read handler", ref)
		}
	case ClassCompensable, ClassIrreversible:
		if h.Read != nil || h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot != nil || h.Revert != nil {
			return fmt.Errorf("action %s: a %s action has Plan, Apply and Verify handlers only", ref, s.Class)
		}
	case ClassRevertible:
		if h.Read != nil || h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Snapshot == nil || h.Revert == nil {
			return fmt.Errorf("action %s: a revertible action has Plan, Apply, Verify, Snapshot and Revert handlers", ref)
		}
	default:
		return fmt.Errorf("action %s: unknown class %q", ref, s.Class)
	}
	return nil
}

// Action returns one action version.
func (r *Registry) Action(ref ActionRef) (ActionDef, bool) {
	a, ok := r.actions[ref]
	return a, ok
}

// Actions returns every action, sorted by name and version.
func (r *Registry) Actions() []ActionDef {
	out := make([]ActionDef, 0, len(r.actions))
	for _, a := range r.actions {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Spec.Name != out[j].Spec.Name {
			return out[i].Spec.Name < out[j].Spec.Name
		}
		return out[i].Spec.Version < out[j].Spec.Version
	})
	return out
}

// Resource returns one resource.
func (r *Registry) Resource(name string) (ResourceDef, bool) {
	res, ok := r.resources[name]
	return res, ok
}

// Resources returns every resource, sorted by name.
func (r *Registry) Resources() []ResourceDef {
	out := make([]ResourceDef, 0, len(r.resources))
	for _, res := range r.resources {
		out = append(out, res)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Modules returns the manifests in registration order.
func (r *Registry) Modules() []Manifest { return slices.Clone(r.modules) }

// Module returns one module's manifest.
func (r *Registry) Module(id string) (Manifest, bool) {
	for _, m := range r.modules {
		if m.ID == id {
			return m, true
		}
	}
	return Manifest{}, false
}
```

- [ ] **Step 5: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core`, then `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/core/define.go internal/core/registry.go internal/core/registry_test.go
git commit -m "feat(core): add action definitions and registry"
```

---

### Task 10: Clock, sealer and validator fakes

**Files:**
- Create: `internal/core/coretest/clock.go`
- Create: `internal/core/coretest/sealer.go`
- Create: `internal/core/coretest/validator.go`
- Test: `internal/core/coretest/clock_test.go`
- Test: `internal/core/coretest/sealer_test.go`
- Test: `internal/core/coretest/validator_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core` › Task 6 (`Clock`, `Timer`, `Sealer`, `Validator`, `ActionDef`); Contracts › `internal/core/errcode` (Task 3).
- Produces: Contracts › `internal/core/coretest` › Task 10. `clock.go` carries the package comment.

**Depends on:** Task 3, Task 6

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

Create `internal/core/coretest/clock_test.go`:

```go
package coretest

import (
	"testing"
	"time"
)

var start = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func TestFakeClockFiresInOrder(t *testing.T) {
	c := NewFakeClock(start)
	var got []string
	var seen []time.Time
	record := func(name string) func() {
		return func() { got = append(got, name); seen = append(seen, c.Now()) }
	}
	c.AfterFunc(30*time.Second, record("b"))
	c.AfterFunc(10*time.Second, record("a"))
	c.AfterFunc(30*time.Second, record("c"))
	c.AfterFunc(2*time.Minute, record("late"))

	c.Advance(time.Minute)
	if want := []string{"a", "b", "c"}; len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("fired %v, want %v", got, want)
	}
	if !seen[0].Equal(start.Add(10*time.Second)) || !seen[1].Equal(start.Add(30*time.Second)) {
		t.Errorf("Now inside timers = %v, want each timer's deadline", seen)
	}
	if !c.Now().Equal(start.Add(time.Minute)) {
		t.Errorf("Now = %v, want start+1m", c.Now())
	}
	if c.Pending() != 1 {
		t.Errorf("Pending = %d, want 1", c.Pending())
	}
}

func TestFakeClockStop(t *testing.T) {
	c := NewFakeClock(start)
	timer := c.AfterFunc(time.Second, func() { t.Error("stopped timer fired") })
	if !timer.Stop() {
		t.Error("first Stop() = false, want true")
	}
	if timer.Stop() {
		t.Error("second Stop() = true, want false")
	}
	c.Advance(time.Minute)

	fired := c.AfterFunc(time.Second, func() {})
	c.Advance(time.Second)
	if fired.Stop() {
		t.Error("Stop() after firing = true, want false")
	}
}

func TestFakeClockTimerArmedByTimer(t *testing.T) {
	c := NewFakeClock(start)
	n := 0
	c.AfterFunc(time.Second, func() {
		n++
		c.AfterFunc(time.Second, func() { n++ })
	})
	c.Advance(5 * time.Second)
	if n != 2 {
		t.Errorf("fired %d timers, want 2 (the second was armed inside the first)", n)
	}
}

func TestFakeClockZeroAdvanceFiresDue(t *testing.T) {
	c := NewFakeClock(start)
	fired := false
	c.AfterFunc(0, func() { fired = true })
	if fired {
		t.Fatal("timer fired before Advance")
	}
	c.Advance(0)
	if !fired {
		t.Error("Advance(0) did not fire a timer due now")
	}
}
```

Create `internal/core/coretest/sealer_test.go`:

```go
package coretest

import (
	"bytes"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestSealer(t *testing.T) {
	var s core.Sealer = Sealer{}
	secret := []byte("totp-secret")
	ct, err := s.Seal("core_users.totp_secret_ct.01A", secret)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ct, secret) {
		t.Error("ciphertext contains the plaintext")
	}
	got, err := s.Open("core_users.totp_secret_ct.01A", ct)
	if err != nil || !bytes.Equal(got, secret) {
		t.Fatalf("Open = %q, %v", got, err)
	}
	if _, err := s.Open("core_users.totp_secret_ct.01B", ct); err == nil {
		t.Error("Open with other associated data succeeded")
	}
	ct[len(ct)-1] ^= 1
	if _, err := s.Open("core_users.totp_secret_ct.01A", ct); err == nil {
		t.Error("Open of tampered ciphertext succeeded")
	}
	if _, err := s.Open("x", []byte{1}); err == nil {
		t.Error("Open of a short ciphertext succeeded")
	}
	a, b := s.Derive("darkpanel csrf v1"), s.Derive("other")
	if len(a) != 32 || bytes.Equal(a, b) || !bytes.Equal(a, s.Derive("darkpanel csrf v1")) {
		t.Errorf("Derive: want 32 bytes, stable per label and distinct across labels")
	}
}
```

Create `internal/core/coretest/validator_test.go`:

```go
package coretest

import (
	"errors"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type validatorInput struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestValidator(t *testing.T) {
	def := core.ActionDef{InputType: reflect.TypeFor[validatorInput]()}
	if err := (Validator{}).ValidateInput(def, []byte(`{"name":"a","count":1}`)); err != nil {
		t.Errorf("valid input: %v", err)
	}
	for _, raw := range []string{``, `{"name":"a","extra":1}`, `{"count":"one"}`, `{} {}`} {
		err := (Validator{}).ValidateInput(def, []byte(raw))
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput || len(coded.Fields) != 1 {
			t.Errorf("ValidateInput(%q) = %v, want %s with one field error", raw, err, errcode.InvalidInput)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/coretest/`
Expected: FAIL — the build reports `undefined: NewFakeClock`, `undefined: Sealer` and `undefined: Validator`.

- [ ] **Step 3: Write the clock**

Create `internal/core/coretest/clock.go`:

```go
// Package coretest holds fakes of the core interfaces for tests in every
// part: a manual clock, a sealer, a validator, journal and audit stores, an
// agent client, a dispatcher and a host directory.
package coretest

import (
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// FakeClock is a core.Clock whose time moves only through Advance.
type FakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	clock *FakeClock
	at    time.Time
	f     func()
	done  bool // fired or stopped
}

// NewFakeClock returns a clock reading start.
func NewFakeClock(start time.Time) *FakeClock {
	return &FakeClock{now: start}
}

// Now returns the clock's current time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// AfterFunc arms f to run when Advance reaches now+d.
func (c *FakeClock) AfterFunc(d time.Duration, f func()) core.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{clock: c, at: c.now.Add(d), f: f}
	c.timers = append(c.timers, t)
	return t
}

// Advance moves the clock forward by d and runs every timer that falls due,
// earliest first (ties in arming order), on the calling goroutine. While a
// timer runs, Now reads its deadline; timers it arms that fall due within the
// advance run too.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	c.mu.Unlock()
	for {
		c.mu.Lock()
		var next *fakeTimer
		for _, t := range c.timers {
			if !t.done && !t.at.After(target) && (next == nil || t.at.Before(next.at)) {
				next = t
			}
		}
		if next == nil {
			c.now = target
			c.timers = pendingTimers(c.timers)
			c.mu.Unlock()
			return
		}
		next.done = true
		if next.at.After(c.now) {
			c.now = next.at
		}
		c.mu.Unlock()
		next.f()
	}
}

// Pending is the number of armed timers that have neither fired nor been
// stopped.
func (c *FakeClock) Pending() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(pendingTimers(c.timers))
}

func pendingTimers(ts []*fakeTimer) []*fakeTimer {
	var out []*fakeTimer
	for _, t := range ts {
		if !t.done {
			out = append(out, t)
		}
	}
	return out
}

// Stop disarms the timer; it reports false when the timer already fired or
// was stopped.
func (t *fakeTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	if t.done {
		return false
	}
	t.done = true
	return true
}
```

- [ ] **Step 4: Write the sealer**

Create `internal/core/coretest/sealer.go`:

```go
package coretest

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
)

var sealerKey = sha256.Sum256([]byte("darkpanel coretest sealer"))

// Sealer is a core.Sealer using AES-256-GCM under a fixed key. Like the
// production sealer it binds the associated data and detects tampering; its
// key and ciphertext layout are not the production ones. Tests only.
type Sealer struct{}

func (Sealer) aead() (cipher.AEAD, error) {
	block, err := aes.NewCipher(sealerKey[:])
	if err != nil {
		return nil, fmt.Errorf("coretest sealer: %w", err)
	}
	return cipher.NewGCM(block)
}

// Seal encrypts plaintext bound to ad.
func (s Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, a.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("coretest sealer: nonce: %w", err)
	}
	return a.Seal(nonce, nonce, plaintext, []byte(ad)), nil
}

// Open decrypts ciphertext sealed with the same ad.
func (s Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
	a, err := s.aead()
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < a.NonceSize() {
		return nil, errors.New("coretest sealer: ciphertext too short")
	}
	n := a.NonceSize()
	out, err := a.Open(nil, ciphertext[:n], ciphertext[n:], []byte(ad))
	if err != nil {
		return nil, fmt.Errorf("coretest sealer: open: %w", err)
	}
	return out, nil
}

// Derive returns HMAC-SHA256 of label under the fixed key.
func (Sealer) Derive(label string) []byte {
	m := hmac.New(sha256.New, sealerKey[:])
	m.Write([]byte(label))
	return m.Sum(nil)
}
```

- [ ] **Step 5: Write the validator**

Create `internal/core/coretest/validator.go`:

```go
package coretest

import (
	"bytes"
	"encoding/json"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Validator is a core.Validator that decodes the input strictly into the
// action's input type: empty input, unknown fields, wrong types and trailing
// data fail with request.invalid_input. It checks no patterns, enums or
// ranges; the schema package's validator does.
type Validator struct{}

// ValidateInput checks raw against def's input type.
func (Validator) ValidateInput(def core.ActionDef, raw json.RawMessage) error {
	invalid := func(msg string) error {
		return &errcode.Error{
			Code:   errcode.InvalidInput,
			Detail: "input does not match the action's schema",
			Fields: []errcode.FieldError{{Path: "", Message: msg}},
		}
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return invalid("input is empty")
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	// The decoder's error text can quote input bytes, so it is not reported.
	if err := dec.Decode(reflect.New(def.InputType).Interface()); err != nil {
		return invalid("input does not match the action's input type")
	}
	if dec.More() {
		return invalid("trailing data after the JSON value")
	}
	return nil
}
```

- [ ] **Step 6: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/coretest/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/coretest/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/coretest`, then `0 issues.`

- [ ] **Step 7: Commit**

```bash
git add internal/core/coretest/clock.go internal/core/coretest/clock_test.go internal/core/coretest/sealer.go internal/core/coretest/sealer_test.go internal/core/coretest/validator.go internal/core/coretest/validator_test.go
git commit -m "test(coretest): add clock, sealer and validator"
```

---

### Task 11: Module list and core module stub

**Files:**
- Create: `internal/controller/coreactions/module.go`
- Create: `internal/modules/modules.go`
- Test: `internal/controller/coreactions/module_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core` › Task 6 (`Module`, `Manifest`, `ModuleIDCore`).
- Produces: Contracts › Other packages › `internal/controller/coreactions` and `internal/modules`.

**Items:** 1

**Depends on:** Task 6

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/module_test.go`:

```go
package coreactions

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestManifest(t *testing.T) {
	m := Module(Deps{}).Manifest()
	if m.ID != core.ModuleIDCore || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("Manifest = %+v, want id core, version 1, enabled by default", m)
	}
	if len(m.Actions) != 0 || len(m.Resources) != 0 || m.Migrations != nil {
		t.Errorf("Manifest declares actions, resources or migrations: %+v", m)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/coreactions/`
Expected: FAIL — the build reports `undefined: Module` and `undefined: Deps`.

- [ ] **Step 3: Write the core module stub**

Create `internal/controller/coreactions/module.go`:

```go
// Package coreactions is the core module (spec section 10): the host, module,
// token, audit and user actions, under module id "core".
package coreactions

import "github.com/darkraise/darkpanel/internal/core"

// Deps are the controller services the core actions call. Callers that need
// only the registry's shape pass the zero value.
type Deps struct{}

// Module returns the core module.
func Module(_ Deps) core.Module { return module{} }

type module struct{}

// Manifest declares the core module.
func (module) Manifest() core.Manifest {
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, DefaultEnabled: true}
}
```

- [ ] **Step 4: Write the module list**

Create `internal/modules/modules.go`:

```go
// Package modules lists the compiled-in modules. Adding a module adds one
// line to All and changes nothing else in the core (R5).
package modules

import "github.com/darkraise/darkpanel/internal/core"

// All returns every compiled-in module in registration order.
func All() []core.Module {
	return []core.Module{}
}
```

- [ ] **Step 5: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/coreactions/ ./internal/modules/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/controller/... ./internal/modules/...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/controller/coreactions` and `?   	github.com/darkraise/darkpanel/internal/modules	[no test files]`, then `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/controller/coreactions/module.go internal/controller/coreactions/module_test.go internal/modules/modules.go
git commit -m "feat(modules): add the module list and core stub"
```

---

### Task 12: Journal, audit, agent and dispatcher fakes

**Files:**
- Create: `internal/core/coretest/audit.go`
- Create: `internal/core/coretest/agent.go`
- Create: `internal/core/coretest/dispatcher.go`
- Test: `internal/core/coretest/audit_test.go`
- Test: `internal/core/coretest/agent_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core` › Task 6 (`Journal`, `Audit`, `AgentClient`, `Dispatcher`, `HostDirectory` and their types); Contracts › `internal/core/errcode` (Task 3); package `coretest` and its package comment from Task 10.
- Produces: Contracts › `internal/core/coretest` › Task 12. `agent.go` defines the helper `notConfigured`, which `dispatcher.go` uses.

**Depends on:** Task 3, Task 6, Task 10

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 0 = 3

- [ ] **Step 1: Write the failing tests**

Create `internal/core/coretest/audit_test.go`:

```go
package coretest

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestMemJournal(t *testing.T) {
	var j MemJournal
	ctx := context.Background()
	if err := j.Append(ctx, nil, core.JournalEntry{Action: "demo.item.add", Phase: "apply"}); err != nil {
		t.Fatal(err)
	}
	j.Err = errors.New("disk full")
	if err := j.Append(ctx, nil, core.JournalEntry{Action: "demo.item.add"}); err == nil {
		t.Error("Append with Err set succeeded")
	}
	if got := j.Entries(); len(got) != 1 || got[0].Phase != "apply" {
		t.Errorf("Entries = %+v, want the one successful append", got)
	}
}

func TestMemAudit(t *testing.T) {
	var a MemAudit
	ctx := context.Background()
	var _ core.Audit = &a
	if err := a.Append(ctx, nil, core.JournalEntry{Actor: "user:1", Action: "core.token.create", Phase: "apply"}); err != nil {
		t.Fatal(err)
	}
	if err := a.Record(ctx, core.AuditEntry{Actor: "user:1", Phase: "login"}); err != nil {
		t.Fatal(err)
	}
	batch := []core.JournalEntry{{Seq: 1, Actor: "user:1", Action: "demo.item.add"}, {Seq: 2, Actor: "user:2", Action: "demo.item.add"}}
	for range 2 {
		if err := a.Ingest(ctx, "H1", batch); err != nil {
			t.Fatal(err)
		}
	}

	all, next, err := a.List(ctx, core.AuditQuery{})
	if err != nil || len(all) != 4 || next != "" {
		t.Fatalf("List = %d entries, next %q, %v; want 4 (ingest deduplicated), no next", len(all), next, err)
	}
	if all[0].Seq != 2 || all[0].HostID != "H1" || all[3].Action != "core.token.create" || all[3].HostID != "" {
		t.Errorf("List order or fields wrong: %+v", all)
	}

	page, next, _ := a.List(ctx, core.AuditQuery{Limit: 3})
	if len(page) != 3 || next != page[2].ID {
		t.Fatalf("page 1 = %d entries, next %q; want 3 and the last id", len(page), next)
	}
	rest, next, _ := a.List(ctx, core.AuditQuery{Limit: 3, Before: next})
	if len(rest) != 1 || next != "" || rest[0].Action != "core.token.create" {
		t.Errorf("page 2 = %+v, next %q; want the oldest entry and no next", rest, next)
	}

	byActor, _, _ := a.List(ctx, core.AuditQuery{Actor: "user:2"})
	byHost, _, _ := a.List(ctx, core.AuditQuery{HostID: "H1", Action: "demo.item.add"})
	if len(byActor) != 1 || len(byHost) != 2 {
		t.Errorf("filters: actor %d entries, host+action %d; want 1 and 2", len(byActor), len(byHost))
	}
}
```

Create `internal/core/coretest/agent_test.go`:

```go
package coretest

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestFakeAgent(t *testing.T) {
	ctx := context.Background()
	agent := &FakeAgent{
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			return core.ModuleStatus{ID: id, Available: true}, nil
		},
	}
	if st, err := agent.Preflight(ctx, "hostinfo"); err != nil || st.ID != "hostinfo" || !st.Available {
		t.Errorf("Preflight = %+v, %v", st, err)
	}
	if _, err := agent.Hello(ctx); err == nil || !strings.Contains(err.Error(), "Hello is not configured") {
		t.Errorf("unconfigured Hello error = %v", err)
	}
}

func TestFakeDispatcher(t *testing.T) {
	ctx := context.Background()
	d := &FakeDispatcher{
		ChangeFunc: func(_ context.Context, _ core.Principal, id string) (core.ChangeView, error) {
			return core.ChangeView{ID: id}, nil
		},
	}
	if c, err := d.Change(ctx, core.Principal{}, "01C"); err != nil || c.ID != "01C" {
		t.Errorf("Change = %+v, %v", c, err)
	}
	if _, err := d.Apply(ctx, core.Principal{}, "", core.ActionRef{}, "", core.ApplyOptions{}); err == nil || !strings.Contains(err.Error(), "Apply is not configured") {
		t.Errorf("unconfigured Apply error = %v", err)
	}
}

func TestMapHosts(t *testing.T) {
	agent := &FakeAgent{}
	hosts := MapHosts{"H1": agent}
	var dir core.HostDirectory = hosts
	got, err := dir.Agent(context.Background(), "H1")
	if err != nil || got != agent {
		t.Errorf("Agent(H1) = %v, %v", got, err)
	}
	_, err = dir.Agent(context.Background(), "H2")
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.NotFound {
		t.Errorf("Agent(H2) error = %v, want %s", err, errcode.NotFound)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/coretest/`
Expected: FAIL — the build reports `undefined: MemJournal`, `undefined: MemAudit`, `undefined: FakeAgent`, `undefined: FakeDispatcher` and `undefined: MapHosts`.

- [ ] **Step 3: Write the journal and audit fakes**

Create `internal/core/coretest/audit.go`:

```go
package coretest

import (
	"context"
	"database/sql"
	"fmt"
	"slices"
	"sync"

	"github.com/darkraise/darkpanel/internal/core"
)

// MemJournal is a core.Journal that keeps entries in memory. It ignores the
// transaction, so a rolled-back change keeps its entry. Set Err to make
// Append fail.
type MemJournal struct {
	mu      sync.Mutex
	entries []core.JournalEntry
	Err     error
}

// Append records e, or returns Err when it is set.
func (j *MemJournal) Append(_ context.Context, _ *sql.Tx, e core.JournalEntry) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.Err != nil {
		return j.Err
	}
	j.entries = append(j.entries, e)
	return nil
}

// Entries returns the recorded entries in order.
func (j *MemJournal) Entries() []core.JournalEntry {
	j.mu.Lock()
	defer j.mu.Unlock()
	return slices.Clone(j.entries)
}

// MemAudit is a core.Audit in memory. Ids are 26-digit counters, so they sort
// in insertion order like ULIDs; List returns the newest first.
type MemAudit struct {
	mu      sync.Mutex
	entries []core.AuditEntry
	seen    map[string]bool // "<host id>/<seq>" already ingested
	next    int
}

func (a *MemAudit) add(e core.AuditEntry) {
	a.next++
	if e.ID == "" {
		e.ID = fmt.Sprintf("%026d", a.next)
	}
	a.entries = append(a.entries, e)
}

func fromJournal(hostID string, e core.JournalEntry) core.AuditEntry {
	return core.AuditEntry{
		Actor: e.Actor, HostID: hostID, Action: e.Action, Phase: e.Phase, ChangeID: e.ChangeID,
		Result: e.Result, ErrorCode: e.ErrorCode, At: e.At, Version: e.Version, Seq: e.Seq, Input: e.Input,
	}
}

// Append records a controller-side change event.
func (a *MemAudit) Append(_ context.Context, _ *sql.Tx, e core.JournalEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.add(fromJournal("", e))
	return nil
}

// Record stores e, assigning an id when it has none.
func (a *MemAudit) Record(_ context.Context, e core.AuditEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.add(e)
	return nil
}

// Ingest stores agent journal entries, skipping any host and seq pair seen
// before.
func (a *MemAudit) Ingest(_ context.Context, hostID string, entries []core.JournalEntry) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.seen == nil {
		a.seen = map[string]bool{}
	}
	for _, e := range entries {
		key := fmt.Sprintf("%s/%d", hostID, e.Seq)
		if a.seen[key] {
			continue
		}
		a.seen[key] = true
		a.add(fromJournal(hostID, e))
	}
	return nil
}

// List returns entries newest first, filtered by the query's exact-match
// fields and by Before (ids below it). A zero Limit means 100. nextBefore is
// the last returned id when older entries remain, else empty.
func (a *MemAudit) List(_ context.Context, q core.AuditQuery) ([]core.AuditEntry, string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	limit := q.Limit
	if limit == 0 {
		limit = 100
	}
	var out []core.AuditEntry
	for i := len(a.entries) - 1; i >= 0; i-- {
		e := a.entries[i]
		switch {
		case q.Before != "" && e.ID >= q.Before,
			q.Actor != "" && e.Actor != q.Actor,
			q.HostID != "" && e.HostID != q.HostID,
			q.Action != "" && e.Action != q.Action:
			continue
		}
		if len(out) == limit {
			return out, out[len(out)-1].ID, nil
		}
		out = append(out, e)
	}
	return out, "", nil
}
```

- [ ] **Step 4: Write the agent client and host directory fakes**

Create `internal/core/coretest/agent.go`:

```go
package coretest

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func notConfigured(fake, method string) error {
	return fmt.Errorf("coretest.%s: %s is not configured", fake, method)
}

// FakeAgent is a core.AgentClient whose methods call the matching function
// field; a method whose field is nil returns an error naming it.
type FakeAgent struct {
	HelloFunc     func(ctx context.Context) (core.AgentHello, error)
	ReadFunc      func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ResourceFunc  func(ctx context.Context, p core.Principal, name string) (json.RawMessage, error)
	PlanFunc      func(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error)
	ApplyFunc     func(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	ConfirmFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RevertFunc    func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangeFunc    func(ctx context.Context, changeID string) (core.ChangeView, error)
	ChangesFunc   func(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error)
	PreflightFunc func(ctx context.Context, moduleID string) (core.ModuleStatus, error)
	JournalFunc   func(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error)
	AckFunc       func(ctx context.Context, seq int64) error
}

var _ core.AgentClient = (*FakeAgent)(nil)

// Hello calls HelloFunc.
func (f *FakeAgent) Hello(ctx context.Context) (core.AgentHello, error) {
	if f.HelloFunc == nil {
		return core.AgentHello{}, notConfigured("FakeAgent", "Hello")
	}
	return f.HelloFunc(ctx)
}

// Read calls ReadFunc.
func (f *FakeAgent) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if f.ReadFunc == nil {
		return nil, notConfigured("FakeAgent", "Read")
	}
	return f.ReadFunc(ctx, p, a, input)
}

// Resource calls ResourceFunc.
func (f *FakeAgent) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	if f.ResourceFunc == nil {
		return nil, notConfigured("FakeAgent", "Resource")
	}
	return f.ResourceFunc(ctx, p, name)
}

// Plan calls PlanFunc.
func (f *FakeAgent) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	if f.PlanFunc == nil {
		return core.PlanView{}, notConfigured("FakeAgent", "Plan")
	}
	return f.PlanFunc(ctx, p, a, input)
}

// Apply calls ApplyFunc.
func (f *FakeAgent) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if f.ApplyFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Apply")
	}
	return f.ApplyFunc(ctx, p, planID, opts)
}

// Confirm calls ConfirmFunc.
func (f *FakeAgent) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ConfirmFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Confirm")
	}
	return f.ConfirmFunc(ctx, p, changeID)
}

// Revert calls RevertFunc.
func (f *FakeAgent) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.RevertFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Revert")
	}
	return f.RevertFunc(ctx, p, changeID)
}

// Change calls ChangeFunc.
func (f *FakeAgent) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	if f.ChangeFunc == nil {
		return core.ChangeView{}, notConfigured("FakeAgent", "Change")
	}
	return f.ChangeFunc(ctx, changeID)
}

// Changes calls ChangesFunc.
func (f *FakeAgent) Changes(ctx context.Context, filter core.ChangeFilter) ([]core.ChangeView, error) {
	if f.ChangesFunc == nil {
		return nil, notConfigured("FakeAgent", "Changes")
	}
	return f.ChangesFunc(ctx, filter)
}

// Preflight calls PreflightFunc.
func (f *FakeAgent) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	if f.PreflightFunc == nil {
		return core.ModuleStatus{}, notConfigured("FakeAgent", "Preflight")
	}
	return f.PreflightFunc(ctx, moduleID)
}

// Journal calls JournalFunc.
func (f *FakeAgent) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	if f.JournalFunc == nil {
		return nil, notConfigured("FakeAgent", "Journal")
	}
	return f.JournalFunc(ctx, afterSeq, limit)
}

// Ack calls AckFunc.
func (f *FakeAgent) Ack(ctx context.Context, seq int64) error {
	if f.AckFunc == nil {
		return notConfigured("FakeAgent", "Ack")
	}
	return f.AckFunc(ctx, seq)
}

// MapHosts is a core.HostDirectory over a map from host id to client.
type MapHosts map[string]core.AgentClient

// Agent returns the host's client, or not_found.
func (m MapHosts) Agent(_ context.Context, hostID string) (core.AgentClient, error) {
	if c, ok := m[hostID]; ok {
		return c, nil
	}
	return nil, errcode.New(errcode.NotFound, fmt.Sprintf("host %s not found", hostID))
}
```

- [ ] **Step 5: Write the dispatcher fake**

Create `internal/core/coretest/dispatcher.go`:

```go
package coretest

import (
	"context"
	"encoding/json"

	"github.com/darkraise/darkpanel/internal/core"
)

// FakeDispatcher is a core.Dispatcher whose methods call the matching
// function field; a method whose field is nil returns an error naming it.
type FakeDispatcher struct {
	ReadFunc     func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error)
	ResourceFunc func(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error)
	PlanFunc     func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error)
	ApplyFunc    func(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error)
	ConfirmFunc  func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	RevertFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangeFunc   func(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error)
	ChangesFunc  func(ctx context.Context, p core.Principal, f core.ChangeFilter) ([]core.ChangeView, error)
}

var _ core.Dispatcher = (*FakeDispatcher)(nil)

// Read calls ReadFunc.
func (f *FakeDispatcher) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if f.ReadFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Read")
	}
	return f.ReadFunc(ctx, p, hostID, a, input)
}

// Resource calls ResourceFunc.
func (f *FakeDispatcher) Resource(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
	if f.ResourceFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Resource")
	}
	return f.ResourceFunc(ctx, p, hostID, name)
}

// Plan calls PlanFunc.
func (f *FakeDispatcher) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	if f.PlanFunc == nil {
		return core.PlanView{}, notConfigured("FakeDispatcher", "Plan")
	}
	return f.PlanFunc(ctx, p, hostID, a, input)
}

// Apply calls ApplyFunc.
func (f *FakeDispatcher) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	if f.ApplyFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Apply")
	}
	return f.ApplyFunc(ctx, p, hostID, a, planID, opts)
}

// Confirm calls ConfirmFunc.
func (f *FakeDispatcher) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ConfirmFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Confirm")
	}
	return f.ConfirmFunc(ctx, p, changeID)
}

// Revert calls RevertFunc.
func (f *FakeDispatcher) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.RevertFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Revert")
	}
	return f.RevertFunc(ctx, p, changeID)
}

// Change calls ChangeFunc.
func (f *FakeDispatcher) Change(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if f.ChangeFunc == nil {
		return core.ChangeView{}, notConfigured("FakeDispatcher", "Change")
	}
	return f.ChangeFunc(ctx, p, changeID)
}

// Changes calls ChangesFunc.
func (f *FakeDispatcher) Changes(ctx context.Context, p core.Principal, filter core.ChangeFilter) ([]core.ChangeView, error) {
	if f.ChangesFunc == nil {
		return nil, notConfigured("FakeDispatcher", "Changes")
	}
	return f.ChangesFunc(ctx, p, filter)
}
```

- [ ] **Step 6: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/coretest/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/coretest/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/coretest`, then `0 issues.`

- [ ] **Step 7: Commit**

```bash
git add internal/core/coretest/audit.go internal/core/coretest/audit_test.go internal/core/coretest/agent.go internal/core/coretest/agent_test.go internal/core/coretest/dispatcher.go
git commit -m "test(coretest): add journal, audit, agent fakes"
```

---

### Task 13: Registry build

**Files:**
- Create: `internal/registry/registry.go`
- Test: `internal/registry/registry_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core` › Task 9 (`NewRegistry`, `DefineRead`); Contracts › Other packages › `internal/controller/coreactions` and `internal/modules` (Task 11).
- Produces: Contracts › Other packages › `internal/registry`.

**Items:** 1

**Depends on:** Task 6, Task 9, Task 11

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/registry/registry_test.go`:

```go
package registry

import (
	"context"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

type hostList struct{}

type hostListOut struct {
	Hosts []string `json:"hosts"`
}

func (hostList) Read(context.Context, core.Env, struct{}) (hostListOut, error) {
	return hostListOut{}, nil
}

type fakeCore core.Manifest

func (m fakeCore) Manifest() core.Manifest { return core.Manifest(m) }

func TestBuild(t *testing.T) {
	r, err := Build()
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mods := r.Modules()
	if len(mods) != 1+len(modules.All()) || mods[0].ID != core.ModuleIDCore {
		t.Errorf("modules = %d, first %q; want core first, then modules.All()", len(mods), mods[0].ID)
	}
}

func TestBuildWith(t *testing.T) {
	read := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "core.host.list", Version: 1, Side: core.SideController}, hostList{})
	r, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{read}})
	if err != nil {
		t.Fatalf("BuildWith: %v", err)
	}
	if _, ok := r.Action(core.ActionRef{Name: "core.host.list", Version: 1}); !ok {
		t.Error("core.host.list/v1 not registered")
	}

	if _, err := BuildWith(fakeCore{ID: "demo", Version: 1}); err == nil || !strings.Contains(err.Error(), "want \"core\"") {
		t.Errorf("non-core module: err = %v", err)
	}
	foreign := core.DefineRead[struct{}, hostListOut](core.ActionSpec{Name: "demo.host.list", Version: 1, Side: core.SideController}, hostList{})
	if _, err := BuildWith(fakeCore{ID: core.ModuleIDCore, Version: 1, Actions: []core.ActionDef{foreign}}); err == nil {
		t.Error("BuildWith accepted a core module with a foreign action name")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/registry/`
Expected: FAIL — the build reports `undefined: Build` and `undefined: BuildWith`.

- [ ] **Step 3: Write the implementation**

Create `internal/registry/registry.go`:

```go
// Package registry builds the one module registry every component uses: the
// core module first, then modules.All() (spec section 2).
package registry

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules"
)

// Build returns the registry with a core module built from zero Deps, for
// callers that use only the registry's shape: the CLI, the schema generator
// and tests. Its core handlers must not be called.
func Build() (*core.Registry, error) {
	return BuildWith(coreactions.Module(coreactions.Deps{}))
}

// BuildWith returns the registry with the given core module, which must have
// module id "core"; the server passes one built with its stores.
func BuildWith(coreModule core.Module) (*core.Registry, error) {
	if id := coreModule.Manifest().ID; id != core.ModuleIDCore {
		return nil, fmt.Errorf("registry: core module id %q, want %q", id, core.ModuleIDCore)
	}
	return core.NewRegistry(append([]core.Module{coreModule}, modules.All()...)...)
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/registry/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/registry/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/registry`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go
git commit -m "feat(registry): build the registry from modules"
```

---

### Task 14: Database backups

**Files:**
- Create: `internal/core/store/backup.go`
- Test: `internal/core/store/backup_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/store` › Task 2 (`DB`, `Writer`, `Path`, `Write`, test helper `openTemp`).
- Produces: Contracts › `internal/core/store` › Task 14 (`Backup`).

**Items:** 4

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`pruneBackups` deletes directories. It only ever considers names that match `backupDirName`, and `TestBackupKeepsNewest` proves an unrelated directory survives; keep both properties.

- [ ] **Step 1: Write the failing test**

Create `internal/core/store/backup_test.go`:

```go
package store

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestBackupCopiesDatabase(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.Exec("CREATE TABLE t (x INTEGER); INSERT INTO t VALUES (42)")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "backups")
	now := time.Date(2026, 10, 17, 9, 30, 0, 0, time.UTC)

	dir, err := Backup(ctx, db, root, "v1.0.0+dirty", "v1.1.0", now, 5)
	if err != nil {
		t.Fatalf("Backup: %v", err)
	}
	if want := filepath.Join(root, "v1.0.0_dirty-v1.1.0-20261017T093000Z"); dir != want {
		t.Errorf("backup dir = %s, want %s", dir, want)
	}
	copyPath := filepath.Join(dir, "test.db")
	info, err := os.Stat(copyPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("backup mode = %o, want 600", info.Mode().Perm())
	}
	copyDB, err := Open(ctx, copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = copyDB.Close() }()
	var x int
	if err := copyDB.Read().QueryRow("SELECT x FROM t").Scan(&x); err != nil || x != 42 {
		t.Errorf("backup row = %d, %v; want 42", x, err)
	}
}

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
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	want := []string{
		"a-b-20261017T000200Z", "a-b-20261017T000300Z", "a-b-20261017T000400Z",
		"a-b-20261017T000500Z", "a-b-20261017T000600Z", "unrelated",
	}
	if len(names) != len(want) {
		t.Fatalf("entries = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("entries = %v, want %v", names, want)
		}
	}
}

func TestBackupRefusesExistingDirectory(t *testing.T) {
	db := openTemp(t)
	root := t.TempDir()
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err != nil {
		t.Fatal(err)
	}
	if _, err := Backup(context.Background(), db, root, "a", "b", now, 5); err == nil {
		t.Fatal("second backup with the same name succeeded, want an error")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/store/`
Expected: FAIL — the build reports `undefined: Backup`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/backup.go`:

```go
package store

import (
	"context"
	"fmt"
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
// name> (directory 0700, file 0600) and then deletes all but the newest keep
// backup directories under root. Characters other than letters, digits, dot
// and underscore in from and to become underscores, so the name always parses.
// It returns the new backup's directory. Backups are for disaster recovery
// only; nothing restores them automatically (ADR 0005).
func Backup(ctx context.Context, db *DB, root, from, to string, now time.Time, keep int) (string, error) {
	if keep < 1 {
		return "", fmt.Errorf("store: backup keep = %d, want at least 1", keep)
	}
	name := fmt.Sprintf("%s-%s-%s",
		unsafeVersionChars.ReplaceAllString(from, "_"),
		unsafeVersionChars.ReplaceAllString(to, "_"),
		now.UTC().Format(backupTimeLayout))
	dir := filepath.Join(root, name)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("store: backup root: %w", err)
	}
	if err := os.Mkdir(dir, 0o700); err != nil {
		return "", fmt.Errorf("store: backup directory: %w", err)
	}
	target := filepath.Join(dir, filepath.Base(db.Path()))
	if _, err := db.Writer().ExecContext(ctx, "VACUUM INTO ?", target); err != nil {
		return "", fmt.Errorf("store: VACUUM INTO %s: %w", target, err)
	}
	if err := os.Chmod(target, 0o600); err != nil {
		return "", fmt.Errorf("store: backup mode: %w", err)
	}
	if err := pruneBackups(root, keep); err != nil {
		return "", err
	}
	return dir, nil
}

// pruneBackups deletes the oldest backup directories beyond keep. It only
// considers directories whose names Backup produces.
func pruneBackups(root string, keep int) error {
	entries, err := os.ReadDir(root)
	if err != nil {
		return fmt.Errorf("store: list backups: %w", err)
	}
	type backup struct{ name, stamp string }
	var backups []backup
	for _, e := range entries {
		m := backupDirName.FindStringSubmatch(e.Name())
		if !e.IsDir() || m == nil {
			continue
		}
		backups = append(backups, backup{e.Name(), m[1]})
	}
	sort.Slice(backups, func(i, j int) bool {
		if backups[i].stamp != backups[j].stamp {
			return backups[i].stamp > backups[j].stamp
		}
		return strings.Compare(backups[i].name, backups[j].name) > 0
	})
	for _, b := range backups[min(keep, len(backups)):] {
		if err := os.RemoveAll(filepath.Join(root, b.name)); err != nil {
			return fmt.Errorf("store: prune backup %s: %w", b.name, err)
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/store/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/store/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/store`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/backup.go internal/core/store/backup_test.go
git commit -m "feat(store): back up databases with VACUUM INTO"
```

---

### Task 15: Migration runner

**Files:**
- Create: `internal/core/store/migrate.go`
- Test: `internal/core/store/migrate_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/store` › Task 2 and Task 14 (`DB`, `Backup`, test helper `openTemp`).
- Produces: Contracts › `internal/core/store` › Task 15 (`MigrationSet`, `MigrateOptions`, `Applied`, `ModuleFailure`, `Result`, `Migrate`, `SchemaVersion`, table `core_migrations`, test helpers `metaSQL`, `sqlFS`, `migrateOpts`, `tableExists`).

**Items:** 4

**Depends on:** Task 2, Task 14

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Rules this runner enforces (spec section 4 and the Assumptions' two rulings): core first, then modules in the order given; each migration in its own transaction, recorded in `core_migrations`; a backup before the first pending migration unless `binary_version` already equals the running version; a changed checksum or failed core migration returns an error; a module failure is reported in `Result.Failed` and skips that module's later migrations only; recorded migrations above the highest embedded one are ignored; numbers are unique, may have gaps, and a pending migration below an applied one is refused.

- [ ] **Step 1: Write the failing test**

Create `internal/core/store/migrate_test.go`:

```go
package store

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"
)

var metaSQL = "CREATE TABLE core_meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);"

func sqlFS(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys[name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

func migrateOpts(t *testing.T, version string, now time.Time) MigrateOptions {
	return MigrateOptions{
		Side: "controller", BinaryVersion: version,
		BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: now,
	}
}

func tableExists(t *testing.T, db *DB, name string) bool {
	t.Helper()
	var n int
	if err := db.Read().QueryRow("SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n == 1
}

func TestMigrateFreshDatabase(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"alpha", sqlFS(map[string]string{
			"0001_init.sql":  "CREATE TABLE alpha_items (id TEXT PRIMARY KEY);",
			"0002_names.sql": "ALTER TABLE alpha_items ADD COLUMN name TEXT;",
		})},
		{"beta", nil},
	}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	res, err := Migrate(context.Background(), db, opts, core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	if want := filepath.Join(opts.BackupRoot, "none-v1-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
	if len(res.Applied) != 3 || len(res.Failed) != 0 {
		t.Errorf("result = %+v, want 3 applied and no failures", res)
	}
	var v string
	if err := db.Read().QueryRow("SELECT value FROM core_meta WHERE key = 'binary_version'").Scan(&v); err != nil || v != "v1" {
		t.Errorf("binary_version = %q, %v; want v1", v, err)
	}
}

func TestMigrateBacksUpOnlyWhenPending(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts := migrateOpts(t, "v1", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}

	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || res.BackupDir != "" || len(res.Applied) != 0 {
		t.Fatalf("second run = %+v, %v; want nothing applied and no backup", res, err)
	}

	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})
	opts.BinaryVersion = "v2"
	res, err = Migrate(ctx, db, opts, core, nil)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(opts.BackupRoot, "v1-v2-20261017T010000Z"); res.BackupDir != want {
		t.Errorf("backup dir = %q, want %q", res.BackupDir, want)
	}
	if _, err := os.Stat(filepath.Join(res.BackupDir, "test.db")); err != nil {
		t.Errorf("backup file: %v", err)
	}
}

func TestMigrateSkipsBackupForSameVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	failing := []MigrationSet{{"broken", sqlFS(map[string]string{"0001_bad.sql": "NOT SQL;"})}}
	opts := migrateOpts(t, "v2", time.Date(2026, 10, 17, 1, 0, 0, 0, time.UTC))
	res, err := Migrate(ctx, db, opts, core, failing)
	if err != nil || res.BackupDir == "" || len(res.Failed) != 1 {
		t.Fatalf("first run = %+v, %v; want a backup and the module failure", res, err)
	}
	for range 6 {
		opts.Now = opts.Now.Add(time.Minute)
		res, err = Migrate(ctx, db, opts, core, failing)
		if err != nil || res.BackupDir != "" || len(res.Failed) != 1 {
			t.Fatalf("restart = %+v, %v; want the module failure again and no new backup", res, err)
		}
	}
	entries, err := os.ReadDir(opts.BackupRoot)
	if err != nil || len(entries) != 1 || entries[0].Name() != "none-v2-20261017T010000Z" {
		t.Errorf("backups = %v, %v; want only the first run's backup", entries, err)
	}
}

func TestMigrateCoreChecksumChangeStops(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	if _, err := Migrate(ctx, db, opts, core, nil); err != nil {
		t.Fatal(err)
	}
	core.FS = sqlFS(map[string]string{"0001_core.sql": metaSQL + "\n-- edited"})
	_, err := Migrate(ctx, db, opts, core, nil)
	if err == nil || !strings.Contains(err.Error(), "changed after it was applied") {
		t.Fatalf("err = %v, want a checksum error", err)
	}
}

func TestMigrateModuleFailureIsIsolated(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	mods := []MigrationSet{
		{"broken", sqlFS(map[string]string{
			"0001_init.sql": "CREATE TABLE broken_a (id TEXT);",
			"0002_bad.sql":  "THIS IS NOT SQL;",
			"0003_more.sql": "CREATE TABLE broken_c (id TEXT);",
		})},
		{"misnamed", sqlFS(map[string]string{"init.sql": "CREATE TABLE misnamed_a (id TEXT);"})},
		{"twice", sqlFS(map[string]string{
			"0001_a.sql": "CREATE TABLE twice_a (id TEXT);",
			"0001_b.sql": "CREATE TABLE twice_b (id TEXT);",
		})},
		{"zero", sqlFS(map[string]string{"0000_init.sql": "CREATE TABLE zero_a (id TEXT);"})},
		{"good", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE good_a (id TEXT);"})},
	}
	res, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods)
	if err != nil {
		t.Fatalf("Migrate: %v (a module failure must not fail the run)", err)
	}
	failed := map[string]bool{}
	for _, f := range res.Failed {
		failed[f.Module] = true
	}
	for _, m := range []string{"broken", "misnamed", "twice", "zero"} {
		if !failed[m] {
			t.Errorf("module %s not reported failed; failures = %+v", m, res.Failed)
		}
	}
	if failed["good"] || !tableExists(t, db, "good_a") {
		t.Error("module good did not migrate")
	}
	if !tableExists(t, db, "broken_a") || tableExists(t, db, "broken_c") {
		t.Error("broken: want 0001 applied and 0003 skipped after 0002 failed")
	}
}

func TestMigrateModuleChecksumChangeIsIsolated(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	alpha := MigrationSet{"alpha", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE alpha_a (id TEXT);"})}
	if _, err := Migrate(ctx, db, opts, core, []MigrationSet{alpha}); err != nil {
		t.Fatal(err)
	}
	alpha.FS = sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE alpha_a (id TEXT); -- edited"})
	beta := MigrationSet{"beta", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE beta_a (id TEXT);"})}
	res, err := Migrate(ctx, db, opts, core, []MigrationSet{alpha, beta})
	if err != nil {
		t.Fatalf("Migrate: %v (a module checksum change must not fail the run)", err)
	}
	if len(res.Failed) != 1 || res.Failed[0].Module != "alpha" || !strings.Contains(res.Failed[0].Err.Error(), "changed after it was applied") {
		t.Errorf("failures = %+v, want alpha with a checksum error", res.Failed)
	}
	if !tableExists(t, db, "beta_a") {
		t.Error("module beta did not migrate")
	}
}

func TestMigrateCoreFailureStops(t *testing.T) {
	db := openTemp(t)
	core := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": "NOT SQL;"})}
	mods := []MigrationSet{{"good", sqlFS(map[string]string{"0001_init.sql": "CREATE TABLE good_a (id TEXT);"})}}
	if _, err := Migrate(context.Background(), db, migrateOpts(t, "v1", time.Now()), core, mods); err == nil {
		t.Fatal("Migrate succeeded with a broken core migration")
	}
	if tableExists(t, db, "good_a") {
		t.Error("module migrated after core failed")
	}
}

func TestMigrateIgnoresNewerRecordedMigrations(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v2", time.Now())
	newer := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})}
	if _, err := Migrate(ctx, db, opts, newer, nil); err != nil {
		t.Fatal(err)
	}
	older := MigrationSet{"core", sqlFS(map[string]string{"0001_core.sql": metaSQL})}
	opts.BinaryVersion = "v1"
	res, err := Migrate(ctx, db, opts, older, nil)
	if err != nil || len(res.Applied) != 0 {
		t.Fatalf("older binary: %+v, %v; want success with nothing applied", res, err)
	}
}

func TestMigrateAllowsGapsButNotOutOfOrder(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	opts := migrateOpts(t, "v1", time.Now())
	core := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0003_later.sql": "CREATE TABLE core_later (id TEXT PRIMARY KEY);",
	})}
	res, err := Migrate(ctx, db, opts, core, nil)
	if err != nil || len(res.Applied) != 2 {
		t.Fatalf("gap: %+v, %v; want 0001 and 0003 applied", res, err)
	}
	core.FS = sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_early.sql": "CREATE TABLE core_early (id TEXT PRIMARY KEY);",
		"0003_later.sql": "CREATE TABLE core_later (id TEXT PRIMARY KEY);",
	})
	_, err = Migrate(ctx, db, opts, core, nil)
	if err == nil || !strings.Contains(err.Error(), "must run in order") {
		t.Fatalf("err = %v, want an ordering error", err)
	}
	if tableExists(t, db, "core_early") {
		t.Error("0002 ran after 0003")
	}
}

func TestSchemaVersion(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	if v, err := SchemaVersion(ctx, db, SideController); err != nil || v != 0 {
		t.Fatalf("before migrating: %d, %v; want 0", v, err)
	}
	core := MigrationSet{"core", sqlFS(map[string]string{
		"0001_core.sql":  metaSQL,
		"0002_hosts.sql": "CREATE TABLE core_hosts (id TEXT PRIMARY KEY);",
	})}
	mods := []MigrationSet{{"alpha", sqlFS(map[string]string{
		"0001_a.sql": "CREATE TABLE alpha_a (id TEXT);",
		"0002_b.sql": "CREATE TABLE alpha_b (id TEXT);",
		"0003_c.sql": "CREATE TABLE alpha_c (id TEXT);",
	})}}
	if _, err := Migrate(ctx, db, migrateOpts(t, "v1", time.Now()), core, mods); err != nil {
		t.Fatal(err)
	}
	if v, err := SchemaVersion(ctx, db, SideController); err != nil || v != 2 {
		t.Errorf("SchemaVersion = %d, %v; want 2 (module migrations do not count)", v, err)
	}
	if v, err := SchemaVersion(ctx, db, SideAgent); err != nil || v != 0 {
		t.Errorf("agent SchemaVersion = %d, %v; want 0", v, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/store/`
Expected: FAIL — the build reports `undefined: MigrationSet`, `undefined: Migrate` and `undefined: SchemaVersion`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrate.go`:

```go
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
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
	BackupRoot    string    // directory for Backup
	Keep          int       // backups kept, 5 in production
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
// nothing pending takes none. A changed checksum or failure in core returns an error and must stop
// startup; the same in a module is reported in Result.Failed and that module's
// remaining migrations are skipped. Recorded migrations numbered above the
// highest embedded one are ignored, so an older binary runs on a newer schema.
func Migrate(ctx context.Context, db *DB, opts MigrateOptions, core MigrationSet, modules []MigrationSet) (Result, error) {
	var res Result
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
	if !anyPending {
		return res, nil
	}

	from, err := binaryVersion(ctx, db)
	if err != nil {
		return res, err
	}
	// The first run of this binary version backed the database up already.
	// Backing up on every later start would let the five-backup limit rotate
	// out the pre-upgrade copy while a failing module migration keeps the
	// service restarting.
	if from != opts.BinaryVersion {
		dir, err := Backup(ctx, db, opts.BackupRoot, from, opts.BinaryVersion, opts.Now, opts.Keep)
		if err != nil {
			return res, err
		}
		res.BackupDir = dir
	}

	for _, m := range corePending {
		if err := apply(ctx, db, opts.Side, core.Module, m); err != nil {
			return res, fmt.Errorf("store: core migration %04d_%s: %w", m.number, m.name, err)
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

// setBinaryVersion records the running binary after a successful run; it is a
// no-op until the core migration that creates core_meta has run.
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
```

- [ ] **Step 4: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/store/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/store/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/store`, then `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrate.go internal/core/store/migrate_test.go
git commit -m "feat(store): apply forward-only migrations"
```

---

### Task 16: Core migrations

**Files:**
- Create: `internal/core/store/migrations.go`
- Create: `internal/core/store/migrations/controller/0001_core.sql`
- Create: `internal/core/store/migrations/agent/0001_core.sql`
- Test: `internal/core/store/migrations_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/store` › Task 2 and Task 15 (`SideController`, `SideAgent`, `MigrationSet`, `Migrate`, test helpers `openTemp`, `migrateOpts`, `tableExists`).
- Produces: Contracts › `internal/core/store` › Task 16 (`CoreMigrations`, the two `0001_core.sql` files and their tables, spec section 4).

**Items:** 4

**Depends on:** Task 2, Task 15

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Once a release ships, a `0001_core.sql` file can never change: the runner refuses a changed checksum. Write the SQL exactly as given.

- [ ] **Step 1: Write the failing test**

Create `internal/core/store/migrations_test.go`:

```go
package store

import (
	"context"
	"testing"
	"time"
)

func TestCoreMigrationsApply(t *testing.T) {
	tests := []struct {
		side   string
		tables []string
	}{
		{SideController, []string{"core_meta", "core_hosts", "core_module_state", "core_ca"}},
		{SideAgent, []string{"core_meta"}},
	}
	for _, tt := range tests {
		t.Run(tt.side, func(t *testing.T) {
			db := openTemp(t)
			set, err := CoreMigrations(tt.side)
			if err != nil {
				t.Fatal(err)
			}
			opts := migrateOpts(t, "v1", time.Now())
			opts.Side = tt.side
			res, err := Migrate(context.Background(), db, opts, set, nil)
			if err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			if len(res.Applied) == 0 {
				t.Fatal("no core migration applied")
			}
			for _, table := range tt.tables {
				if !tableExists(t, db, table) {
					t.Errorf("table %s missing", table)
				}
			}
		})
	}
}

func TestCoreMigrationsUnknownSide(t *testing.T) {
	if _, err := CoreMigrations("browser"); err == nil {
		t.Fatal("CoreMigrations accepted an unknown side")
	}
}

func TestOneLocalHost(t *testing.T) {
	db := openTemp(t)
	ctx := context.Background()
	set, _ := CoreMigrations(SideController)
	if _, err := Migrate(ctx, db, migrateOpts(t, "v1", time.Now()), set, nil); err != nil {
		t.Fatal(err)
	}
	insert := "INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 1, '2026-10-17T00:00:00Z')"
	if _, err := db.Writer().ExecContext(ctx, insert, "01A", "one"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, insert, "01B", "two"); err == nil {
		t.Fatal("a second local host was accepted")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/store/`
Expected: FAIL — the build reports `undefined: CoreMigrations`.

- [ ] **Step 3: Write the controller migration**

Create `internal/core/store/migrations/controller/0001_core.sql`:

```sql
-- Core tables owned by the foundation (spec section 4). Later core tables
-- arrive in their owning part's own numbered file.

CREATE TABLE core_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE core_hosts (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL,
    is_local   INTEGER NOT NULL DEFAULT 0 CHECK (is_local IN (0, 1)),
    created_at TEXT NOT NULL
) STRICT;

CREATE UNIQUE INDEX core_hosts_one_local ON core_hosts (is_local) WHERE is_local = 1;

CREATE TABLE core_module_state (
    host_id    TEXT NOT NULL REFERENCES core_hosts (id) ON DELETE CASCADE,
    module_id  TEXT NOT NULL,
    enabled    INTEGER NOT NULL CHECK (enabled IN (0, 1)),
    available  INTEGER NOT NULL CHECK (available IN (0, 1)),
    reason     TEXT,
    checked_at TEXT NOT NULL,
    PRIMARY KEY (host_id, module_id)
) STRICT;

CREATE TABLE core_ca (
    id         TEXT PRIMARY KEY,
    purpose    TEXT NOT NULL,
    cert_pem   TEXT NOT NULL,
    key_ct     BLOB NOT NULL,
    created_at TEXT NOT NULL
) STRICT;
```

- [ ] **Step 4: Write the agent migration**

Create `internal/core/store/migrations/agent/0001_core.sql`:

```sql
-- Core tables owned by the foundation (spec section 4). Later core tables
-- arrive in their owning part's own numbered file.

CREATE TABLE core_meta (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
```

- [ ] **Step 5: Write the embedding**

Create `internal/core/store/migrations.go`:

```go
package store

import (
	"embed"
	"fmt"
	"io/fs"
)

//go:embed migrations
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
```

- [ ] **Step 6: Run the tests and lint**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/core/store/ && GOTOOLCHAIN=local timeout 300 golangci-lint run ./internal/core/store/ && GOTOOLCHAIN=local timeout 300 go test -count=1 ./...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/store`, `0 issues.`, then every package line starts with `ok` or `?`.

- [ ] **Step 7: Commit**

```bash
git add internal/core/store/migrations.go internal/core/store/migrations_test.go internal/core/store/migrations/controller/0001_core.sql internal/core/store/migrations/agent/0001_core.sql
git commit -m "feat(store): add the core 0001 migrations"
```

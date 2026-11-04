# v1 wave 7: controller backup, restore and restore quarantine Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 7 of the v1 completion spec: the encrypted controller backup (`darkpanel controller backup` over the controller socket), the crash-safe restore with its journal and recovery (`darkpanel controller restore`, `restore --recover` run by the controller unit before every start), and restore quarantine (marking, the quarantine list, resolve, enrollment token and release routes and commands), with the harness test that restores a controller onto a fresh VM and re-enrolls an agent (register rows 54, 69, 229 and 232).

**Architecture:** Everything lives in `internal/controller/backup` except the CLI commands (`internal/cli/controller_backup.go`, `controller_quarantine.go`), one unit-file line in `internal/install`, and two Integrator tasks that edit frozen files: migration 0013 (the `quarantined_from` columns and the `quarantined` rollout state that wave 0 did not add) and the server's call site, which now passes `backup.Config` (key file, token issuer, agent address) beside `ext.Deps`. The backup route streams a VACUUM INTO copy and the key through a chunked XChaCha20-Poly1305 file format keyed by argon2id. Restore runs in the root CLI process with the services stopped: it stages, validates and quarantines a copy, then swaps files under a fsynced journal of rename intents, all through one `Ops` interface that tests wrap to crash before and after every rename and fsync. The quarantine routes read and write `controller.db` directly in single transactions with the lock rows.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`) (standard library, `golang.org/x/crypto/argon2` and `golang.org/x/crypto/chacha20poly1305` from the already required `golang.org/x/crypto v0.58.0`, `archive/tar`, `github.com/oklog/ulid/v2`, `github.com/spf13/cobra`), SQLite through `internal/core/store` (`modernc.org/sqlite v1.60.1`), systemd unit templates in `internal/install/units`, the KVM harness in `test/harness` (GitHub Actions `harness.yml`).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1 | 2-3 | 4-6 | 7 | 8-10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w7-tmp/integ && export TMPDIR=/root/.cache/w7-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w7-tmp/wave && export TMPDIR=/root/.cache/w7-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/controller/... ./internal/cli/... ./internal/install/... ./internal/core/... ./schemas/... && timeout 300 go vet -tags harness ./test/...`

**Plan review:** 2026-11-04 — dforge:judge-opus — executability 15 / coherence 15 / coverage 14 / assumptions 15 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 10's Backup format, Restore (phases 1–8, rename intent, recovery), Restore quarantine (marking, admitted operations, resolve outcomes, release) and Routes; the ExecStartPre line of the controller unit; the wave 7 row of Parallel decomposition; the tests section 10 lists; the plan rulings W7-R1 to W7-R15 under Assumptions, which Task 7 copies into spec section 10; and the register rows the tasks' **Items:** lines name. Out of scope: the runbook, CHANGELOG and versioning (wave 10), the fuzz target for the backup header and frames (wave 9a; Task 1 only keeps the parser fuzzable), panel exposures (wave 8; W7-R10), and the web UI (spec 10 defines none).
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No test or command of this plan reads or writes `/var/lib/darkpanel`, `/etc/darkpanel`, `/run/darkpanel*` or `/run/darkpanel-restore.lock`, talks to D-Bus or systemd, runs `systemctl`, `docker`, `ufw` or `iptables`, or starts, stops or reloads a unit. Every path in a unit test comes from `paths.Under(t.TempDir())` or `t.TempDir()`; every restore and recovery test builds a `backup.Host` (Contracts "Restore host") with a fake `core.Units`, a fake `Euid`, an `Owner` that answers the test process's own uid and gid, and a lock file in `t.TempDir()`. `backup.ProductionHost` is never called from a test. The real restore, recovery and unit run only on harness VMs (Task 19).
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w7-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` shows 90 % or more before a task's first build, trim the Go build cache (`go clean -cache` is acceptable).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`. `golang.org/x/crypto/argon2`, `golang.org/x/crypto/chacha20poly1305` and `github.com/oklog/ulid/v2` are already in the module graph; after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -short -count=1` on the task's packages (`-short` strides Task 15's crash matrices under the race detector; the plain suite runs them in full); `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. Only Task 7 runs `make schemas` and commits a snapshot.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN. Quote the RED and GREEN output in the report verbatim, not a paraphrase. A task that skips the full-suite line above says so and why in its report.
- Frozen files, which no task edits except where a task's **Files:** block names the file: `internal/core/**` (Task 7 names `internal/core/store/migrations/controller/0013_restore_quarantine.sql`, `internal/core/store/migrations_test.go` and `internal/core/fleet.go`), `internal/platform/**`, `internal/modules/**`, `internal/controller/**` except `internal/controller/backup/**` (Task 5 names `internal/controller/server/extensions.go`, `extensions_test.go` and `services_test.go`), `internal/cli/**` except `internal/cli/controller_backup*.go` and `internal/cli/controller_quarantine*.go`, `internal/install/**` except Task 18's files, `cmd/darkpanel/**`, `internal/paths/**`, `internal/update/**`, `internal/transport/**`, `go.mod`, `go.sum`, `web/**`, `schemas/**` except Task 7's regenerated snapshot, `Makefile`, `test/harness/**` except Task 19's files, `.github/workflows/**` except `harness.yml` in Task 19. A task that finds a missing code, field or extension point in a frozen file stops and reports it; it does not edit the file. Tasks 5 and 7 are Integrator tasks: spec 1.12 freezes their files, and the plan rulings W7-R1 and W7-R2 authorise exactly the edits their steps describe.
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files, databases, journals and lock files; no TCP listener except `httptest` on `127.0.0.1` port 0 or a Unix socket under `t.TempDir()`; `coretest.NewFakeClock` wherever a duration above one second is waited on; no package-level mutable state written by tests (fault injection goes through `backup.Ops` and `backup.Host` fields, never a replaced package variable). Tests that run argon2id use `backup.TestParams` (Contracts "Format") and are not marked `t.Parallel()`, so no more than one 64 MiB derivation runs per test binary at a time. Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier in `internal/controller/backup` carries its task's prefix from Contracts "Test prefixes"; only Task 3 declares the shared fixtures of Contracts "Controller database fixture", and only Task 2 declares the crash wrapper of Contracts "Durable file operations". Non-test code follows the same rule: an unexported top-level identifier a task adds in `internal/controller/backup` that neither Contracts nor the task's own text names starts with its task's word: Task 1 `format`, Task 2 `durable`, Task 3 `config`, Task 4 `journal`, Task 6 `guard`, Task 8 `mark`, Task 9 `quarantine`, Task 11 `validate`, Task 12 `stream`, Task 13 `resolve`, Task 14 `restore`, Task 15 `recover`, Task 16 `route`.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean. Error strings start with the package name (`backup:`, `cli:` only where the package already does, `server:`, `store:`, `harness:`). Route refusals are `*errcode.Error` with client-safe details using only codes already in `internal/core/errcode/errcode.go` (`core.quarantined`, `core.change_busy`, `plan.state_changed`, `request.invalid_input`, `not_found`); every other failure is a plain wrapped error.
- Secrets: no log line, error, audit row, problem detail, journal or test failure message carries a passphrase, the controller key, a key file's bytes, a token or decrypted backup bytes. The passphrase is held as `[]byte` and zeroed (`clear`) after the key derivation; the derived key is zeroed after the file is written or read.
- Durability: every file the restore and the recovery create is written through `backup.Ops` as create (O_EXCL, mode 0600), write, fsync, close, then rename where the step says so, then an fsync of the directory; every rename is followed by an fsync of its directory before the next step. No restore or recovery step writes to a database after phase 3 (spec 10); phase 7 and recovery open the installed database read-only.
- Values copied from the spec, used exactly: magic `DPBK`, version byte `1`, argon2id bounds t 1–10, m 65536–1048576 KiB, p 1–16, defaults t=3, m=65536, p=4; 16-byte salt, 16-byte nonce prefix, nonce `prefix || uint64 BE chunk index`; chunks of at most 64 KiB plaintext; associated data `header || 0x00` for every chunk but the last and `header || 0x01` for the last; at most 2^32 chunks; tar members exactly `controller.db` then `controller.key`; journal `/var/lib/darkpanel/controller/restore-<tx>.journal` (that is, `<ControllerDir>/restore-<tx>.journal`); staged names `<file>.restore-<tx>`, set-aside names `<file>.pre-<tx>`; lock file `/run/darkpanel-restore.lock`; installed owner `darkpanel` and mode 0600; the refusal text "start the controller or run `darkpanel controller restore --recover` first"; the unit line `ExecStartPre=+{{.Binary}} controller restore --recover`; `core_meta.restore_quarantine` `{"tx", "restoredAt", "backupCreatedAt"}`; the never-applied reason "restore: never applied"; the five routes and their bodies of spec 10 Routes; enrollment tokens valid one hour (`fleet.TokenTTL`).
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 18 and 19 touch `internal/install/`, `test/harness/` and `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag, and no task runs `release.yml`.

## Contracts

- **Test prefixes** (`internal/controller/backup`): Task 1 `Fm`, Task 2 `Du`, Task 3 `Cb`, Task 4 `Jn`, Task 6 `Rg`, Task 8 `Mk`, Task 9 `Ql`, Task 11 `Vf`, Task 12 `Bs`, Task 13 `Rs`, Task 14 `Rt`, Task 15 `Rc`, Task 16 `Ro`. Elsewhere: Task 5 `Xb` (`internal/controller/server`), Task 7 `Rq` (`internal/core/store`), Task 10 `Cq` and Task 17 `Ck` (`internal/cli`), Task 18 `Ru` (`internal/install`), Task 19 `M7r` (`test/harness`, harness tests `TestM7Restore*`, unit tests `TestM7rUnit*`).

- **Configuration and contribution** (Task 3, `internal/controller/backup/contribute.go`):

  ```go
  // TokenIssuer issues single-use enrollment tokens; *fleet.Authority implements it.
  type TokenIssuer interface {
      IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error)
  }

  // Config is what the assembly gives the backup package beside ext.Deps.
  type Config struct {
      KeyFile        string      // the controller key file, paths.Paths.ControllerKey
      Tokens         TokenIssuer // nil: enroll-token is refused
      AgentAdvertise string      // the address enrollment tokens name; "" refuses enroll-token
  }

  // Backup holds the backup routes' state.
  type Backup struct {
      cfg     Config
      running atomic.Bool // true while a backup streams; at most one at a time
  }

  func New(cfg Config) *Backup
  // Contribute returns the backup and quarantine routes; Task 3 returns nil, nil, nil, Task 16 the five routes.
  func (b *Backup) Contribute(deps ext.Deps) ([]ext.Route, []ext.Service, error)
  ```

  The package-level `func Contribute(ext.Deps) ([]ext.Route, []ext.Service, error)` stays, unchanged, until Task 16 deletes it with its test `TestContributeAddsNothingYet`; Task 5 moves the server to `New(...).Contribute`.

- **Server call site** (Task 5, `internal/controller/server/extensions.go`, `contributions()`): the backup contributor becomes `contributor{name: "backup", c: controllerFunc(backup.New(backup.Config{KeyFile: s.opts.Paths.ControllerKey, Tokens: s.authority, AgentAdvertise: s.opts.AgentAdvertise}).Contribute)}`. `ext.Deps` is unchanged. If `s.authority` can be nil when `contributions()` runs, Task 5 passes a nil `Tokens` in that case (never a typed nil pointer in the interface).

- **Controller database fixture** (Task 3, `internal/controller/backup/fixture_test.go`; every helper carries `//nolint:unused // shared helper for this package's tests` until a task uses it):

  ```go
  // fxDB opens <dir>/controller.db (dir from t.TempDir() unless given) migrated with
  // store.CoreMigrations(store.SideController) and every modules.All() controller set, and closes it at cleanup.
  func fxDB(t *testing.T, dir string) *store.DB
  // fxChange inserts a core_plans row and a core_changes row; empty fields take fixed defaults
  // (Class core.ClassCompensable, Actor "user:fixture", Version 1, InputJSON "{}", times fxNow).
  type fxChange struct{ ID, HostID, Action, Class, State, Owner, ParentID string }
  func fxInsertChange(t *testing.T, db *store.DB, c fxChange)
  // fxInsertComposite inserts the composite's change (class composite), its core_composites row
  // (direction "forward" unless dir is "unwind", cursor 0) and one core_composite_steps row per step.
  type fxStep struct{ ID, HostID, Action, State, ChildPlanID, ChildChangeID string }
  func fxInsertComposite(t *testing.T, db *store.DB, id, state, dir string, steps []fxStep)
  func fxInsertLock(t *testing.T, db *store.DB, key, owner string)
  // fxInsertRollout inserts a core_rollouts row; a draining rollout gets outcome "failed".
  func fxInsertRollout(t *testing.T, db *store.DB, id, state string, hosts ...string)
  func fxInsertListener(t *testing.T, db *store.DB, id, state string)
  func fxInsertHost(t *testing.T, db *store.DB, id, state string, local bool)
  // fxKey writes a fresh controller key file (secret.LoadOrCreateKey) under dir and returns its path,
  // its bytes and a sealer over it.
  func fxKey(t *testing.T, dir string) (path string, raw []byte, sealer *secret.Sealer)
  var fxNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
  ```

- **Format** (Task 1, `internal/controller/backup/format.go`; vectors `internal/controller/backup/testdata/short.dpbk` and `testdata/multi.dpbk`):

  ```go
  const (
      Magic         = "DPBK"
      FormatVersion = byte(1)
      HeaderSize    = 46 // magic 4, version 1, t 4, m 4, p 1, salt 16, nonce prefix 16
      ChunkSize     = 64 << 10
      MemberDB      = "controller.db"
      MemberKey     = "controller.key"
      MaxKeyMember  = 4096 // bytes
  )
  type Params struct {
      T uint32 `json:"t"` // 1-10
      M uint32 `json:"m"` // KiB, 65536-1048576
      P uint8  `json:"p"` // 1-16
  }
  var DefaultParams = Params{T: 3, M: 65536, P: 4}
  var TestParams = Params{T: 1, M: 65536, P: 1} // the smallest the format accepts; tests only
  // Validate refuses a parameter out of bounds with request.invalid_input naming argon2.t, argon2.m or argon2.p.
  func (p Params) Validate() error
  // Member is one archive member: exactly Size bytes are read from Body.
  type Member struct {
      Name string
      Size int64
      Body io.Reader
  }
  // Write writes the header (salt then nonce prefix read from rnd, 32 bytes) and the frames of a tar
  // holding exactly members[0] = MemberDB and members[1] = MemberKey (regular files, mode 0600,
  // uid and gid 0, mod time Unix 0). Memory stays O(ChunkSize) plus argon2id's m.
  func Write(w io.Writer, rnd io.Reader, passphrase []byte, p Params, members []Member) error
  // Header is a parsed backup header.
  type Header struct {
      Params      Params
      Salt        [16]byte
      NoncePrefix [16]byte
  }
  // Read checks the header's bounds before deriving the key, then decrypts frame by frame and calls
  // sink once per member, in order, with a reader of exactly size bytes. Read returns nil only when
  // the last frame authenticated with 0x01, the tar held exactly the two members and nothing but tar
  // padding followed; bytes a sink received are unauthenticated as a whole until then.
  func Read(r io.Reader, passphrase []byte, sink func(name string, size int64, body io.Reader) error) (Header, error)
  var (
      ErrNotBackup = errors.New("backup: not a darkpanel backup file")             // magic or version
      ErrHeader    = errors.New("backup: the backup header is out of bounds")       // argon2 bounds
      ErrAuth      = errors.New("backup: wrong passphrase or damaged backup file")  // any frame that fails to open, a frame length out of bounds, data after the last frame
      ErrTruncated = errors.New("backup: the backup file is truncated")             // EOF before a frame sealed with 0x01
      ErrMembers   = errors.New("backup: the archive must hold exactly controller.db and controller.key")
  )
  ```

  A frame's ciphertext length is at least 16 and at most `ChunkSize + 16`; the writer seals a chunk only once it knows whether more plaintext follows, so the last chunk is never empty unless the whole plaintext is. Read wraps each sentinel with `%w` and may add context; callers use `errors.Is`.

- **Durable file operations** (Task 2, `internal/controller/backup/durable.go`; crash wrapper in `durable_crash_test.go`):

  ```go
  // File is a file being written: Sync is its fsync.
  type File interface {
      io.Writer
      Sync() error
      Close() error
  }
  // Ops are the file operations of a restore and its recovery, so tests can crash between any two.
  type Ops interface {
      Create(path string, perm fs.FileMode) (File, error) // O_WRONLY|O_CREATE|O_EXCL|O_NOFOLLOW|O_CLOEXEC
      Rename(from, to string) error
      Remove(path string) error              // a missing path is not an error
      SyncDir(dir string) error
      Chown(path string, uid, gid int) error // lchown
      Chmod(path string, perm fs.FileMode) error
      Exists(path string) (bool, error)      // Lstat: a symbolic link exists
  }
  func OSOps() Ops
  ```

  Test-only (`durable_crash_test.go`, Task 2):

  ```go
  var errCrash = errors.New("backup test: simulated crash")
  // crashOps counts every call of every Ops method and of every File method of a file it created
  // (1-based). Call number at fails with errCrash without running when before is true, or runs and
  // then fails with errCrash when before is false; every later call fails with errCrash without running.
  type crashOps struct{ /* unexported fields */ }
  func newCrashOps(inner Ops, at int, before bool) *crashOps
  func (c *crashOps) Calls() int    // calls counted so far
  func (c *crashOps) Crashed() bool
  ```

- **Journal** (Task 4, `internal/controller/backup/journal.go`):

  ```go
  type Phase string
  const (
      PhaseBegun       Phase = "begun" // written before the first staged file (W7-R6)
      PhaseStaged      Phase = "staged"
      PhaseValidated   Phase = "validated"
      PhaseQuarantined Phase = "quarantined"
      PhaseJournaled   Phase = "journaled"
      PhaseMovedAside  Phase = "moved-aside"
      PhaseInstalled   Phase = "installed"
      PhaseVerified    Phase = "verified"
      PhaseRollback    Phase = "rollback" // recovery's own renames back
  )
  const (
      RenameIntended = "intended"
      RenameDone     = "done"
  )
  type JournalFile struct {
      Path    string `json:"path"`
      Existed bool   `json:"existed"`
  }
  type Rename struct {
      Phase Phase  `json:"phase"`
      From  string `json:"from"`
      To    string `json:"to"`
      State string `json:"state"`
  }
  type Journal struct {
      Format  int           `json:"format"` // 1
      Tx      string        `json:"tx"`
      Phase   Phase         `json:"phase"`
      Files   []JournalFile `json:"files,omitempty"` // set at PhaseJournaled: Targets order
      Renames []Rename      `json:"renames,omitempty"`
  }
  // Targets are the four originals a restore replaces or sets aside, in this order:
  // ControllerDB(), ControllerDB()+"-wal", ControllerDB()+"-shm", ControllerKey.
  func Targets(p paths.Paths) []string
  func StagedName(file, tx string) string // file + ".restore-" + tx
  func AsideName(file, tx string) string  // file + ".pre-" + tx
  func JournalPath(controllerDir, tx string) string // controllerDir/restore-<tx>.journal
  // SaveJournal removes a stale path+".new", writes path+".new" through ops (Create 0600, Write, Sync,
  // Close), renames it over path and fsyncs the directory.
  func SaveJournal(ops Ops, path string, j Journal) error
  func ReadJournal(path string) (Journal, error) // a format other than 1 or an unknown phase is an error
  // ListJournals returns the restore-<tx>.journal paths in dir sorted by tx ascending (ULIDs sort by
  // time); other names, including *.journal.new, are ignored.
  func ListJournals(dir string) ([]string, error)
  // RenameJournaled appends {phase, from, to, intended} to j and saves it, renames from to to, fsyncs
  // the directory of to, then marks the entry done and saves j again.
  func RenameJournaled(ops Ops, journalPath string, j *Journal, phase Phase, from, to string) error
  // Settle decides an intended rename from which names exist: from present and to absent → false (not
  // done); to present and from absent → true (done); both or neither → ErrAmbiguousRename.
  func Settle(ops Ops, r Rename) (bool, error)
  var ErrAmbiguousRename = errors.New("backup: a journaled rename cannot be settled")
  ```

- **Restore host** (Task 6, `internal/controller/backup/restore_guard.go`):

  ```go
  const RestoreLockPath = "/run/darkpanel-restore.lock"
  // Host is what a restore and a recovery touch on this host; tests fill every field.
  type Host struct {
      Paths    paths.Paths
      UnitFile string                         // the installed controller unit file; production: install.Production().UnitPath(update.ControllerUnit)
      LockFile string                         // RestoreLockPath in production
      Units    core.Units                     // production: platform.NewSystemd
      Euid     func() int                     // os.Geteuid in production
      Owner    func() (uid, gid int, err error) // the darkpanel user's ids (install.ServiceUser)
      Ops      Ops                            // OSOps() in production
      Clock    core.Clock
      NewTx    func() string                  // a fresh upper-case ULID
      Log      *slog.Logger
  }
  // ProductionHost returns the production Host over p; its Close releases the D-Bus connection.
  // With withUnits false it opens no D-Bus connection, Units is nil and Close does nothing: recovery
  // runs before every controller start and never checks the services, so it must not depend on D-Bus.
  func ProductionHost(ctx context.Context, p paths.Paths, withUnits bool) (Host, func() error, error)
  // lock takes an exclusive non-blocking flock on h.LockFile (created 0600) and returns its release.
  func (h Host) lock() (func(), error)
  // checkRestore runs the restore's preconditions in this order and returns the first refusal.
  func (h Host) checkRestore(ctx context.Context) error
  ```

  Refusal messages, exact: not root → `backup: a restore must run as root`; controller not installed (no `UnitFile`, no `Paths.ControllerDir` directory or no `Paths.ControllerKey`) → `backup: no controller is installed here; run darkpanel system install --role controller first`; a service whose `ActiveState` is not `inactive` or `failed` (a unit with `LoadState` `not-found` counts as stopped) → `backup: stop darkpanel-controller.service and darkpanel-agent.service before a restore (<unit> is <ActiveState>)`; a `restore-*.journal` file in `Paths.ControllerDir` (found by Task 6's own unexported `pendingJournals`, because Task 4's `ListJournals` lands in the same wave) → `backup: an unfinished restore exists (<journal path>); start the controller or run ` + "`darkpanel controller restore --recover`" + ` first`; lock held → `backup: another restore or recovery holds <LockFile>`. Units checked: `update.ControllerUnit` then `update.AgentUnit`.

- **Marking** (Task 8, `internal/controller/backup/mark.go`):

  ```go
  // NonTerminal are the change states restore quarantines (spec 1.3 "Locks across restarts" without quarantined).
  var NonTerminal = []string{core.StateApplying, core.StatePending, core.StateAwaitingConfirmation,
      core.StateUndoing, core.StateNeedsRepair, core.StateRecovering, core.StateReleasing}
  // Mark quarantines db in one transaction (spec 10 Marking, W7-R5).
  func Mark(ctx context.Context, db *store.DB, m quarantine.Marker) error
  ```

  In order, in one `db.Write`: upsert `core_meta.restore_quarantine` with `json.Marshal(m)`; every `core_changes` row whose state is in `NonTerminal` gets `quarantined_from = state`, `state = 'quarantined'`, `recovery_owner = ''`, `updated_at = <m.RestoredAt in the lifecycle format 2006-01-02T15:04:05.000000000Z>`; `core_task_runs` rows are deleted when that table exists; every `core_rollouts` row in `running` or `draining` gets `quarantined_from = state`, `state = 'quarantined'`, `updated_at`; every `core_listeners` row in `open` becomes `closed` with `updated_at`; every `core_locks` row whose owner is not the id of a `core_changes` row in state `quarantined` is deleted (update reservations and every other owner), the rest are kept. Records already `quarantined` (a backup taken during an earlier quarantine) keep their state and `quarantined_from`.

- **Validation and verification** (Task 11, `internal/controller/backup/validate.go`):

  ```go
  const CanaryKey = "backup_canary"                    // core_meta key
  const CanaryAD = "core_meta.value.backup_canary"     // the seal's associated data
  // Canary is the sealed canary's plaintext, JSON.
  type Canary struct {
      CreatedAt time.Time `json:"createdAt"`
  }
  // ValidateStaged opens dbPath, checks PRAGMA integrity_check answers exactly "ok", that no applied
  // core or module controller migration is newer than this binary embeds, and that a sealer over
  // keyFile opens the canary; it returns the canary's CreatedAt. The database is closed before return.
  func ValidateStaged(ctx context.Context, dbPath string, keyFile []byte) (time.Time, error)
  // VerifyInstalled opens dbPath read-only ("file:<path>?mode=ro" plus query_only) and checks spec 10
  // phase 7: core_meta.restore_quarantine decodes with Tx == tx, no core_changes row is in a
  // NonTerminal state, no core_rollouts row is running or draining, and every core_locks owner is a
  // core_changes row in state quarantined.
  func VerifyInstalled(ctx context.Context, dbPath, tx string) error
  var ErrNotQuarantined = errors.New("backup: the installed controller database is not in this restore's quarantine")
  ```

  The canary's value is `base64.StdEncoding` of `Sealer.Seal(CanaryAD, json.Marshal(Canary{CreatedAt}))`. A missing canary, a seal that does not open or a failed integrity check each fail with an error naming which check failed, never with key or database bytes.

- **Backup handler** (Task 12, `internal/controller/backup/stream.go`):

  ```go
  // BackupRequest is the body of POST /api/v1/controller/backup.
  type BackupRequest struct {
      Passphrase string  `json:"passphrase"`
      Argon2     *Params `json:"argon2,omitempty"` // nil: DefaultParams
  }
  func (b *Backup) backupHandler(deps ext.Deps) http.Handler
  ```

  Body at most 64 KiB, unknown fields refused; passphrase 1–1024 bytes; otherwise `request.invalid_input`. A second backup while one streams → `409 core.change_busy` "another backup is running". Before the copy the handler upserts the canary (Contracts "Validation and verification") with `deps.Clock.Now()`, then `VACUUM INTO` a temporary file `<dir of deps.DB.Path()>/backup-<ULID>.db` (removed when the handler returns, whatever happens), reads `cfg.KeyFile` (regular file, at most `MaxKeyMember` bytes, opened with `O_NOFOLLOW`), and only then answers `200` `application/octet-stream` with `Content-Disposition: attachment; filename="darkpanel-controller-<UTC 20060102T150405Z>.dpbk"`, streaming `Write(w, crypto/rand.Reader, ...)`. A failure after the first body byte aborts the response with `panic(http.ErrAbortHandler)` so the client sees a truncated file. One audit row per backup through `deps.Audit.Record` (Contracts "Audit").

- **Quarantine routes** (Task 9, `internal/controller/backup/quarantine_routes.go`; Task 13, `resolve.go`), wire types exact:

  ```go
  // Record is one quarantined record.
  type Record struct {
      ID              string   `json:"id"`
      Kind            string   `json:"kind"`            // "change" | "composite" | "cloud" | "rollout" (W7-R3)
      Action          string   `json:"action"`          // the change's action; "core.update.rollout" for a rollout
      HostID          string   `json:"hostId"`          // "" for controller-side records and rollouts
      QuarantinedFrom string   `json:"quarantinedFrom"`
      Locks           []string `json:"locks"`           // sorted; [] when none, never null
  }
  // Status answers GET /api/v1/controller/quarantine.
  type Status struct {
      Active     bool       `json:"active"`
      Tx         string     `json:"tx,omitempty"`
      RestoredAt *time.Time `json:"restoredAt,omitempty"`
      Records    []Record   `json:"records"` // every record in state quarantined, by id; [] when none
  }
  type ResolveRequest struct{ Outcome string `json:"outcome"` } // "done" | "never" | "repair"
  type EnrollTokenRequest struct{ HostID string `json:"hostId"` }
  type EnrollTokenResult struct {
      Token     string    `json:"token"`
      ExpiresAt time.Time `json:"expiresAt"`
  }
  type ReleaseRequest struct{ KeepUnresolved bool `json:"keepUnresolved"` }
  type HeldLock struct {
      Key    string `json:"key"`
      Record string `json:"record"`
  }
  type ReleaseResult struct{ HeldLocks []HeldLock `json:"heldLocks"` } // sorted by key; [] when none

  // Task 9:
  func listRecords(ctx context.Context, q queryer) ([]Record, error) // queryer: *sql.DB or *sql.Tx
  func recordByID(ctx context.Context, q queryer, id string) (Record, bool, error)
  type queryer interface {
      QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
      QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
  }
  func (b *Backup) quarantineHandler(deps ext.Deps) http.Handler  // GET
  func (b *Backup) enrollTokenHandler(deps ext.Deps) http.Handler // POST
  func (b *Backup) releaseHandler(deps ext.Deps) http.Handler     // POST
  // Task 13:
  func (b *Backup) resolveHandler(deps ext.Deps) http.Handler     // POST, path value "record"
  ```

  Resolve answers the record after the outcome as `Record` with `QuarantinedFrom` kept and `Kind` unchanged (the state is not part of `Record`; the answer adds nothing else). Every body is decoded by `decodeBody` and every problem answered by `writeProblem` (Contracts "Route helpers").

- **Route helpers** (Task 9, `internal/controller/backup/quarantine_routes.go`; Tasks 12 and 13 call them and declare no copy). No module or `internal/controller/api` helper is exported for contributed routes, so the package declares its own:

  ```go
  const maxRouteBody = 64 << 10
  // writeProblem answers err as application/problem+json with type "about:blank", title, status,
  // detail, code and errors, as internal/controller/api/wire.go does: it takes the *errcode.Error in
  // the chain (errcode.From), maps a code outside the registry to internal, and for a 5xx answers only
  // the code's title as the detail and logs the cause with log.Error.
  func writeProblem(w http.ResponseWriter, log *slog.Logger, err error)
  // writeJSON answers v as application/json with status.
  func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any)
  // decodeBody decodes one JSON object of at most maxRouteBody bytes (http.MaxBytesReader over w) into
  // dst, unknown members refused and nothing but whitespace after it; every defect is
  // request.invalid_input with a fixed text that never repeats input.
  func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error
  // recordAudit writes one Contracts "Audit" row with Result "ok", At deps.Clock.Now(), no Input and
  // Source and ID empty; a failed write is logged with deps.Log.Error and never changes the answer.
  func recordAudit(ctx context.Context, deps ext.Deps, action, phase, changeID, hostID string)
  ```

- **Routes** (Task 16, `Backup.Contribute`), all `ext.SurfaceSocket`: `POST /api/v1/controller/backup` (`Stream: true`), `GET /api/v1/controller/quarantine`, `POST /api/v1/controller/quarantine/{record}/resolve`, `POST /api/v1/controller/quarantine/enroll-token`, `POST /api/v1/controller/quarantine/release`. No service.

- **Restore and recovery** (Task 14, `restore.go`; Task 15, `recover.go`):

  ```go
  type RestoreResult struct {
      Tx              string
      BackupCreatedAt time.Time
  }
  // Restore runs spec 10's phases 1-8 under h.lock() after h.checkRestore.
  func Restore(ctx context.Context, h Host, in io.Reader, passphrase []byte) (RestoreResult, error)
  // Recover processes every journal oldest first under h.lock(); with no journal it returns nil at once
  // without taking the lock. It never checks the services (spec 10) and never uses h.Units, which is nil
  // in production recovery.
  func Recover(ctx context.Context, h Host) error

  // Unexported, declared by Task 14 in restore.go and consumed by Task 15:
  // removeTx applies the rollback rule below to tx, the journal last.
  func removeTx(h Host, tx string) error
  // installStaged is phase 6 and safe to repeat: for the staged database, then the staged key, it skips
  // a rename Settle reports done, and otherwise chowns the staged file to h.Owner(), chmods it 0600 and
  // calls RenameJournaled(h.Ops, journalPath, j, PhaseInstalled, staged, installed).
  func installStaged(h Host, journalPath string, j *Journal) error
  // finishTx is phases 7 and 8: VerifyInstalled (a failure keeps the journal), save PhaseVerified,
  // removeTx, then dropOldAside.
  func finishTx(ctx context.Context, h Host, journalPath string, j *Journal) error
  // dropOldAside removes every <target>.pre-<tx'> of Targets with tx' other than tx (W7-R11).
  func dropOldAside(h Host, tx string) error
  ```

  Rollback of a transaction removes, through `h.Ops` and in this order, every name carrying its tx: `StagedName(Paths.ControllerDB(), tx)`, then `StagedName(Paths.ControllerDB(), tx)+"-wal"` and `StagedName(Paths.ControllerDB(), tx)+"-shm"` (SQLite creates these two beside the staged database while phases 2 and 3 have it open), `StagedName(Paths.ControllerKey, tx)`, the journal path plus `.new`, and finally the journal, each followed by `SyncDir` of its directory; a missing name is not an error. Phase 6 installs only the staged database and key; before each rename it chowns the staged file to `h.Owner()` and chmods it 0600. A completed transaction removes every `<target>.pre-<tx'>` with `tx'` different from its own after its journal is removed (W7-R11).

- **CLI** (Task 10, `internal/cli/controller_quarantine.go`; Task 17, `internal/cli/controller_backup.go`):

  ```go
  // Task 10: the "quarantine" group with list, resolve <record> done|never|repair,
  // enroll-token <host> and release [--keep-unresolved]; each takes the socket flags
  // (addConnFlags(cmd, true, false)) and --json.
  func (a *app) quarantineCommand() *cobra.Command
  // Task 17: backupCommands returns backup, restore and a.quarantineCommand(), in that order.
  func backupCommands(a *app) []*cobra.Command
  ```

  `darkpanel controller backup --out <file> [--passphrase-file <path>] [--argon2-t N] [--argon2-m KiB] [--argon2-p N]`; `darkpanel controller restore --in <file> [--passphrase-file <path>]`; `darkpanel controller restore --recover` (takes no other flag). A passphrase file must be a regular file owned by uid 0 with no permission bits outside 0600; one trailing newline is stripped. Without `--passphrase-file` the passphrase is read twice through `Options.ReadSecret` on a terminal, and must match.

- **Audit** (Tasks 9, 12, 13): one `deps.Audit.Record` row per backup, enroll-token, resolve and release, with `Actor` `local:root@<deps.LocalHostID>`, `At` `deps.Clock.Now()`, `Source` and `ID` empty, and no input, passphrase or token in the row. Action and phase: `controller.backup` with `apply`; `controller.quarantine.enroll-token` with `apply` and `HostID` the host; `controller.quarantine.resolve` with `resolve` and `ChangeID` the record id; `controller.quarantine.release` with `release`. `Result` is `ok`, except a backup stream aborted after its first body byte, which writes `error` with `ErrorCode` `internal`. `core_audit.phase` carries a CHECK list (migrations 0004, 0010, 0011: apply, confirm, revert, recover, interrupt, login, logout, undo, resolve, release, revert-watchdog, reveal, uninstall-abort) that refuses every other phase, `check` and `delete` included, and `core_audit.result` accepts only `ok`, `error` and `override`.

- **Schema** (Task 7, migration `internal/core/store/migrations/controller/0013_restore_quarantine.sql`): `core_changes.quarantined_from TEXT` (NULL unless quarantined); `core_rollouts` and `core_rollout_hosts` rebuilt so `core_rollouts.state` also accepts `quarantined` and `core_rollouts` gains `quarantined_from TEXT`, every row, index and the `ON DELETE CASCADE` reference kept; `internal/core/fleet.go` gains `RolloutQuarantined = "quarantined"` and `enum=quarantined` on the rollout view's `state`.

- **Unit** (Task 18, `internal/install/units/darkpanel-controller.service`): the line `ExecStartPre=+{{.Binary}} controller restore --recover` directly above `ExecStart={{.Binary}} serve`.

## Assumptions (evidence)

Plan rulings (each closes a gap or an underdetermined point of spec 10; Task 7 copies them into spec section 10 under a new "Wave 7 rulings" paragraph):

- **W7-R1 Schema gap (wave 0).** Spec Contracts "the `restore_quarantine` marker with its restore transaction id and `quarantined_from` on quarantined records" was not built: no migration adds `quarantined_from` (`rg quarantined_from internal/core/store/migrations` finds nothing, 2026-11-04), and `core_rollouts.state` has `CHECK (state IN ('running', 'draining', 'succeeded', 'failed', 'cancelled'))` (`internal/core/store/migrations/controller/0007_rollout.sql:14`). Task 7 (Integrator) adds migration 0013. `core_rollout_hosts` references `core_rollouts (id) ON DELETE CASCADE` (`0007_rollout.sql:40`), and `store.Open` runs with `foreign_keys(1)` (`internal/core/store/db.go:27`), so dropping `core_rollouts` would delete every rollout host row: the migration rebuilds both tables, children copied before either old table is dropped and the child dropped first. Earlier rebuild precedent: `0010_audit_phases.sql`, `0011_platform_surfaces.sql` (`core_audit_new`).
- **W7-R2 Call-site gap (wave 0).** `ext.Deps` (`internal/core/ext/ext.go:46-57`) carries no key file path, token issuer or agent address, which the backup route (key member) and enroll-token (`fleet.Authority.IssueToken(ctx, tx, hostID, address)`, `internal/controller/fleet/tokens.go:31`) need. Task 5 (Integrator) changes only the backup contributor in `internal/controller/server/extensions.go:57`; `ext.Deps` and the modules' contributions are unchanged.
- **W7-R3 Record kinds.** `rollout`: a `core_rollouts` row; `composite`: a `core_changes` row of class `composite`; `cloud`: another `core_changes` row whose action's module (the part before the first dot) is `tailnet` or `cloudflare`; `change`: every other `core_changes` row. Cloudflare hostname phases and uncertain cloud effects belong to the change that owns them (`cloudflare_hostnames.change_id`, `core_effects.change_id`), and the hostnames service leaves an in-flight hostname to its operation (`internal/modules/cloudflare/hostnames_service.go:69-71`), so quarantining that change quarantines them. Auth-key sweeps are the `tailnet.keysweep` service, suspended until release by the service runner (`internal/controller/server/extservices.go:42-48`) and cleanup of keys Darkpanel owns afterwards, not records.
- **W7-R4 Resolve outcomes.** `change` and `cloud`: only `repair` (state `needs_repair`, `error_code` `core.needs_repair`, `error_message` "restore: quarantined; repair it after release", `recovery_owner` '', locks kept); `done` and `never` are refused with `request.invalid_input` "a controller-side change is reconciled by repair: resolve it repair and run darkpanel change repair <id> after release", because no agent executes it and only its effects' Observe (1.2) can read the provider. `composite`: `repair` as above; `done` and `never` only when every step's child is on an agent (non-empty `host_id`; a step with an empty `host_id` and a child refuses with `request.invalid_input` naming the step and asking for repair), from each child's lookup (`HostDirectory.Agent(ctx, host).Lookup(ctx, planID, false)`): an agent that is not connected refuses with the agent's own error; `running` or `recovering` → `409 core.change_busy` naming the step; applied = `terminal` with state `confirmed`, `completed`, `repaired` or `kept`; not applied = `absent` or `retired`, or `terminal` with state `reverted`, `failed`, `interrupted` or `abandoned` and `made` 0; a step with no child plan is not applied; a read step (state `read`) counts as neither. `done` needs every other step applied and a `forward` composite, and sets `completed`; `never` needs every other step not applied, first retires every `absent` child plan (`Retire`; an answer other than `retired` or `absent` → `409 plan.state_changed`), and sets `abandoned` with `error_message` "restore: never applied"; any other mix refuses with `request.invalid_input` "the composite is partly applied; resolve it repair". `rollout`: only `never`, which sets `cancelled` with `error_message` "restore: never applied" (a rollout only orchestrates; each agent's update runs its own transaction, ADR 0007); `done` and `repair` refuse with `request.invalid_input` "a rollout resolves never; start a new rollout after release". Every outcome runs in one transaction that re-reads the record's state (`quarantined`, else `409 plan.state_changed`) and deletes the record's `core_locks` rows on `done` and `never`. A non-composite record is always one the controller's own engine executes: an agent-side apply runs in the agent's engine and database (`internal/controller/dispatch/apply.go:40-49`, `c.Apply`), and the controller keeps only a `core_change_index` row for it (`apply.go:172-184`), so agents' single changes are never in the controller's `core_changes` and restore leaves them to their agents.
- **W7-R5 Migrate, then mark.** Spec 10 phase 3 says migrate and mark "in one SQLite transaction", but `store.Migrate` runs each migration in its own transaction (`internal/core/store/migrate.go:280`, `apply`). Restore runs `store.Migrate` on the staged database, then `Mark` in one transaction, then `PRAGMA wal_checkpoint(TRUNCATE)` and `PRAGMA journal_mode=DELETE` before closing it, then removes a leftover staged `-shm`, fsyncs the file and its directory, and only then journals `quarantined`. A crash before that journal write rolls the transaction back and discards the staged file, so nothing unmarked is ever installed: the spec's guarantee holds. `store.Open` always opens in WAL mode (`db.go:27`), hence the switch.
- **W7-R6 Begun phase.** The journal is first saved with phase `begun` before the first staged file is created, so recovery can remove a partly written staging; spec 10 lists phases from `staged`.
- **W7-R7 Canary.** No sealed canary exists (`rg -i canary internal` finds none, 2026-11-04). The backup handler upserts `core_meta.backup_canary` before each copy (Contracts "Validation and verification"); its sealed `createdAt` is the marker's `backupCreatedAt`, since the file format has no timestamp.
- **W7-R8 Enroll token.** Allowed only while the quarantine is active; otherwise the route refuses with `request.invalid_input` "the controller is not in restore quarantine; use darkpanel host token"; the host must exist (`not_found`) and not be the local host (`request.invalid_input`); `cfg.Tokens` nil or `cfg.AgentAdvertise` empty refuse with `request.invalid_input` "start the controller with --agent-advertise to issue enrollment tokens". `fleet.Authority.Enroll` accepts only a `pending` or `revoked` host (`internal/controller/fleet/tokens.go:96`), and a restored host is `enrolled`, so the route sets an `enrolled` host to `pending` in the transaction it passes to `IssueToken`. The answer's `token` is the string `IssueToken` returns.
- **W7-R9 Release.** With the quarantine not active, release answers `200` with the held locks of the remaining quarantined records (idempotent). With it active and any record quarantined and `keepUnresolved` false → `409 core.quarantined` "<n> records are still quarantined; resolve each one, or release with --keep-unresolved". Otherwise one transaction deletes `core_meta.restore_quarantine` and reads the held locks. The suspended sources start on their own: the service runner re-checks every 10 s (`extservices.go:19-22`), the rollout loops each pass (`internal/controller/rollout/suspend.go`), and composite recovery has nothing to claim on a marked database (every unfinished composite is quarantined).
- **W7-R10 Exposures.** No exposure table exists before wave 8 (panel exposure); `Mark` marks runtime listeners only. Wave 8 adds its exposures' `needs_repair` marking to `Mark` and `VerifyInstalled`; Task 7 records a register row for it.
- **W7-R11 Set-aside files.** A successful restore removes the `.pre-<tx'>` files of earlier transactions after its own journal is removed; this wave adds no separate cleanup command. A `restore-<tx>.journal.new` that a crash inside the very first journal save leaves names no file of the installation, is ignored by `ListJournals` and by the precondition check, and stays until uninstall removes the directory.
- **W7-R12 Purge (rows 229, 232).** The purge of expired plans, sessions and tokens stays ungated: spec 10's list of suspended sources does not name it, and the comment in `internal/controller/server/services.go` (the `quarantined` doc comment) already says so. A plan referenced by a change cannot be purged, because `core_changes.plan_id REFERENCES core_plans (id)` (`0002_lifecycle.sql:25`) under `foreign_keys(1)`; Task 5 pins that with a test.
- **W7-R13 Audit.** Spec 10 names no audit rows for the socket routes; this plan writes one per backup, enroll-token, resolve and release (Contracts "Audit") because each is a root operation on the controller.
- **W7-R14 Service check.** The restore checks both units through `core.Units.State` over D-Bus (`internal/platform/systemd.go`, `NewSystemd`), so no exec allowlist rule is added.
- **W7-R15 Unit rollback.** `ExecStartPre` names a command only binaries from this wave on have; a unit is written only by `system install` (`internal/install/units.go`), so it never runs a binary older than the one that wrote it, except after a manual binary rollback below this wave, which v1's reinstall boundary (spec 10 Release candidate: 0.x → 1.0 is a reinstall) excludes.

Facts the tasks rely on:

- `quarantine.Marker`, `MetaKey`, `Read`, `Active`, `Check` exist (`internal/controller/quarantine/quarantine.go`); the dispatcher refuses applies and change mutations in quarantine (`internal/controller/dispatch/quarantine.go`), the engine suspends composite recovery, the child watcher and module recovery handlers (`internal/core/lifecycle/suspend.go`, `composite_watch.go:69-79`), the rollout scheduler and driver skip passes (`internal/controller/server/services.go`, `startRollouts`), and services wait (`extservices.go`); `TestQuarantinedControllerHoldsMutations` (`internal/controller/server/assembly_quarantine_test.go:24`) shows a deleted flag releases them. Wave 7 only sets and clears the flag (spec 1.12 Quarantine gate).
- Startup recovery keeps the locks of `quarantined` changes (`internal/core/lifecycle/recover.go:277`).
- `store.DB` has one writer connection and four reader connections (`internal/core/store/db.go:71`, `:81`), so the reader pool must be closed before `journal_mode=DELETE`; `store.Migrate` copies the database only when `core_meta.binary_version` differs from `opts.BinaryVersion` (`internal/core/store/migrate.go:80-82`, `:148`); `buildinfo.Version` is `"dev"` unless the build sets it (`internal/buildinfo`, `Version = "dev"`).
- The spec 10 test "the update scheduler does not run" in quarantine is wave 0's `TestQuarantineSuspendsSchedulerAndEngine` (`internal/controller/server/assembly_quarantine_test.go:117`); the composite half of "a backup taken mid-composite" is Task 19's seeded composite, with `TestQuarantineHoldsCompositeRecoveryAtStart` (`assembly_quarantine_test.go:158`) for the engine.
- Unverified — Task 19 verifies it: a composite row inserted into the running source controller's database is left alone until the backup, because composite recovery claims composites only at start (`RecoverComposites`, `internal/core/lifecycle/composite_watch.go:60-70`).
- The controller socket admits only root (and the service's own uid in dev mode) by `SO_PEERCRED` (`internal/controller/api/socket.go:107-110`).
- The controller key file is `KeyPrefix + base64(32 bytes) + "\n"`, owned by `darkpanel` 0600 (`internal/core/secret/key.go:60`, ADR 0005), readable by the controller process; `/etc/darkpanel` (`Layout.ConfigDir`) and `ControllerDir` are owned directories that uninstall removes with their contents (`internal/install/layout.go:99-111`), so every staged, set-aside and journal file is already covered for uninstall (spec 10 "declared in the install layout for uninstall only").
- `store.Backup` already copies a live database with `VACUUM INTO` (`internal/core/store/backup.go:43`); the backup handler uses the same statement on `deps.DB.Writer()`.
- The controller unit runs `User=darkpanel` with `ProtectSystem=strict`, `ReadWritePaths={{.ControllerDir}}`, `PrivateTmp=yes` and `TimeoutStartSec=30s` (`internal/install/units/darkpanel-controller.service`); systemd's `+` prefix runs `ExecStartPre` with full privileges, outside those restrictions.
- `core.HostDirectory.Agent` returns an `AgentClient` with `Lookup` and `Retire` (`internal/core/contract.go:186`, spec 1.3 Lookup and retire); `PlanLookup` has `Status`, `State`, `Made` (`contract.go:870-882`).
- Rollout views enumerate `state` (`internal/core/fleet.go:246`), so a quarantined rollout needs `enum=quarantined` there and in `schemas/core.update.rollouts/v1.json`.
- The full suite last passed in 261 s on 2026-11-04 under wave 6b's Integration check key; this plan's key differs, so the Task check is rule 4.
- Unverified — Task 19 verifies it: a harness VM can stop both services, run `darkpanel controller restore`, start the controller into quarantine, and re-enroll an agent from `quarantine enroll-token` with the existing install path (`darkpanel system install --role agent` with a token, as `test/harness/m2_test.go` does).
- No task carries an **Executor:** line: the agy executor offer timed out with no answer on 2026-11-04.

## Task index

1. Backup file format
2. Durable file operations and the crash wrapper
3. Backup configuration and the controller database fixture
4. Restore journal
5. Backup call site and the purge ruling
6. Restore preconditions and lock
7. Restore-quarantine schema and spec rulings
8. Quarantine marking
9. Quarantine list, enrollment token and release routes
10. Quarantine CLI commands
11. Staged validation and installed verification
12. Backup stream route
13. Quarantine resolve route
14. Restore transaction
15. Restore recovery and the crash matrix
16. Backup routes end to end
17. Backup and restore CLI commands
18. Controller unit recovery hook
19. Harness restore test and workflow step

---

### Task 1: Backup file format

**Files:**
- Create: `internal/controller/backup/format.go`
- Create: `internal/controller/backup/format_test.go`
- Create (generated by the command in Step 3, then committed): `internal/controller/backup/testdata/short.dpbk`, `internal/controller/backup/testdata/multi.dpbk`

**Interfaces:**
- Consumes: `errcode.New` and `errcode.InvalidInput` (`internal/core/errcode/errcode.go`); `argon2.IDKey` from `golang.org/x/crypto/argon2` and `chacha20poly1305.NewX` from `golang.org/x/crypto/chacha20poly1305` (both already in the module graph, see Global Constraints "Dependencies"); the values of Global Constraints "Values copied from the spec".
- Produces: every name of Contracts "Format", exactly as written there, and the two committed vectors. Beyond Contracts: `Write` and `Read` zero (`clear`) the passphrase slice they are given as soon as the key is derived, so a caller passes a slice it does not use again.

**Items:** 54

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/format_test.go` with the code below. The helpers `fmOpen` and `fmBuilder` re-implement the spec's frame layout on their own (argon2id key, XChaCha20-Poly1305, nonce `prefix || uint64 BE index`, associated data `header || 0x00/0x01`), so `Write` is checked against the spec and not only against `Read`. Tests that need a damaged file start from the committed vectors, which Step 3 generates; until then they fail with a missing file as well.

```go
package backup

import (
	"archive/tar"
	"bytes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"testing/iotest"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/chacha20poly1305"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fmUpdate rewrites the committed vectors: go test -run '^TestFmWriteVectors$' ./internal/controller/backup -args -update
var fmUpdate = flag.Bool("update", false, "rewrite testdata/short.dpbk and testdata/multi.dpbk")

const fmPassphrase = "darkpanel test passphrase"

func fmPass() []byte { return []byte(fmPassphrase) }

func fmSalt() [16]byte {
	var b [16]byte
	for i := range b {
		b[i] = 0xA0 + byte(i)
	}
	return b
}

func fmPrefix() [16]byte {
	var b [16]byte
	for i := range b {
		b[i] = 0xB0 + byte(i)
	}
	return b
}

// fmRnd answers the 32 random bytes Write asks for: the salt, then the nonce prefix.
func fmRnd() io.Reader {
	salt, prefix := fmSalt(), fmPrefix()
	return bytes.NewReader(append(salt[:], prefix[:]...))
}

func fmMix(x uint64) uint64 {
	x += 0x9e3779b97f4a7c15
	x = (x ^ (x >> 30)) * 0xbf58476d1ce4e5b9
	x = (x ^ (x >> 27)) * 0x94d049bb133111eb
	return x ^ (x >> 31)
}

// fmStream is size bytes of deterministic pseudo-random data: byte i depends
// only on the seed and i, whatever sizes the reads have.
type fmStream struct {
	pos, size uint64
	seed      uint64
}

func newFmStream(size, seed uint64) *fmStream { return &fmStream{size: size, seed: seed} }

func (s *fmStream) Read(p []byte) (int, error) {
	if s.pos >= s.size {
		return 0, io.EOF
	}
	if rest := s.size - s.pos; uint64(len(p)) > rest {
		p = p[:rest]
	}
	var word uint64
	for i := range p {
		at := s.pos + uint64(i)
		if i == 0 || at%8 == 0 {
			word = fmMix(s.seed ^ (at/8)*0x2545f4914f6cdd1d)
		}
		p[i] = byte(word >> (8 * (at % 8))) //nolint:gosec // G115: keeping the low byte is the point
	}
	s.pos += uint64(len(p))
	return len(p), nil
}

func fmBytes(n int, seed uint64) []byte {
	b := make([]byte, n)
	if _, err := io.ReadFull(newFmStream(uint64(n), seed), b); err != nil && n > 0 { //nolint:gosec // G115: n is a test size
		panic(err)
	}
	return b
}

func fmKeyFile(seed uint64) []byte {
	return []byte("darkpanel-key-v1:" + base64.StdEncoding.EncodeToString(fmBytes(32, seed)) + "\n")
}

// fmVector is one committed test vector and the members it must hold.
type fmVector struct {
	file    string
	db, key []byte
}

func fmVectors() []fmVector {
	return []fmVector{
		{"short.dpbk", fmBytes(3000, 1), fmKeyFile(3)},
		{"multi.dpbk", fmBytes(150*1024+777, 2), fmKeyFile(3)},
	}
}

func fmMembers(db, key []byte) []Member {
	return []Member{
		{Name: MemberDB, Size: int64(len(db)), Body: bytes.NewReader(db)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	}
}

func fmWrite(t *testing.T, db, key []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := Write(&buf, fmRnd(), fmPass(), TestParams, fmMembers(db, key)); err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

func fmVectorFile(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name)) //nolint:gosec // G304: a fixed name under testdata
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// fmResult is what Read gave a collecting sink.
type fmResult struct {
	db, key []byte
	order   []string
	header  Header
	err     error
}

func fmRead(r io.Reader, pass []byte) fmResult {
	var res fmResult
	res.header, res.err = Read(r, pass, func(name string, size int64, body io.Reader) error {
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("the sink read %d bytes of %s, size says %d", len(b), name, size)
		}
		res.order = append(res.order, name)
		switch name {
		case MemberDB:
			res.db = b
		case MemberKey:
			res.key = b
		}
		return nil
	})
	return res
}

// fmSplit cuts a backup file into its header and its frames, each frame
// with its 4-byte length.
func fmSplit(t *testing.T, file []byte) ([]byte, [][]byte) {
	t.Helper()
	if len(file) < HeaderSize {
		t.Fatalf("file of %d bytes has no header", len(file))
	}
	hdr, rest := file[:HeaderSize], file[HeaderSize:]
	var frames [][]byte
	for len(rest) > 0 {
		if len(rest) < 4 {
			t.Fatalf("%d bytes left, not a frame", len(rest))
		}
		n := 4 + int(binary.BigEndian.Uint32(rest))
		if n > len(rest) {
			t.Fatalf("frame of %d bytes, %d left", n, len(rest))
		}
		frames = append(frames, rest[:n])
		rest = rest[n:]
	}
	return hdr, frames
}

func fmJoin(hdr []byte, frames ...[]byte) []byte {
	out := bytes.Clone(hdr)
	for _, f := range frames {
		out = append(out, f...)
	}
	return out
}

// fmAEAD derives the key of the vectors' passphrase, salt and parameters
// on its own, so the tests below do not rely on Write or Read for it.
func fmAEAD(t *testing.T, hdr []byte) cipher.AEAD {
	t.Helper()
	salt := hdr[14:30]
	key := argon2.IDKey(fmPass(), salt, binary.BigEndian.Uint32(hdr[5:]), binary.BigEndian.Uint32(hdr[9:]), hdr[13], 32)
	aead, err := chacha20poly1305.NewX(key)
	if err != nil {
		t.Fatal(err)
	}
	return aead
}

func fmNonce(hdr []byte, idx int) []byte {
	n := make([]byte, chacha20poly1305.NonceSizeX)
	copy(n, hdr[30:46])
	binary.BigEndian.PutUint64(n[16:], uint64(idx)) //nolint:gosec // G115: idx counts frames of a test file
	return n
}

// fmOpen decrypts a whole backup file with the format of the spec written
// out again, and returns its plaintext (the tar).
func fmOpen(t *testing.T, file []byte) []byte {
	t.Helper()
	hdr, frames := fmSplit(t, file)
	aead := fmAEAD(t, hdr)
	var plain []byte
	for i, f := range frames {
		ct := f[4:]
		if len(ct) < 16 || len(ct) > ChunkSize+16 {
			t.Fatalf("frame %d holds %d ciphertext bytes", i, len(ct))
		}
		last := byte(0)
		if i == len(frames)-1 {
			last = 1
		}
		pt, err := aead.Open(nil, fmNonce(hdr, i), ct, append(bytes.Clone(hdr), last))
		if err != nil {
			t.Fatalf("frame %d does not open as the spec seals it: %v", i, err)
		}
		if i < len(frames)-1 && len(pt) != ChunkSize {
			t.Fatalf("frame %d holds %d plaintext bytes; only the last may hold fewer than %d", i, len(pt), ChunkSize)
		}
		plain = append(plain, pt...)
	}
	return plain
}

// fmBuilder returns a function that seals chunks into a backup file the way
// the spec says, with TestParams, the vectors' passphrase, salt and prefix.
func fmBuilder(t *testing.T) func(chunks [][]byte) []byte {
	t.Helper()
	hdr := make([]byte, HeaderSize)
	copy(hdr, Magic)
	hdr[4] = FormatVersion
	binary.BigEndian.PutUint32(hdr[5:], TestParams.T)
	binary.BigEndian.PutUint32(hdr[9:], TestParams.M)
	hdr[13] = TestParams.P
	salt, prefix := fmSalt(), fmPrefix()
	copy(hdr[14:], salt[:])
	copy(hdr[30:], prefix[:])
	aead := fmAEAD(t, hdr)
	return func(chunks [][]byte) []byte {
		out := bytes.Clone(hdr)
		for i, c := range chunks {
			last := byte(0)
			if i == len(chunks)-1 {
				last = 1
			}
			ct := aead.Seal(nil, fmNonce(hdr, i), c, append(bytes.Clone(hdr), last))
			out = binary.BigEndian.AppendUint32(out, uint32(len(ct))) //nolint:gosec // G115: at most ChunkSize+16
			out = append(out, ct...)
		}
		return out
	}
}

func fmChunks(plain []byte) [][]byte {
	var out [][]byte
	for len(plain) > ChunkSize {
		out = append(out, plain[:ChunkSize])
		plain = plain[ChunkSize:]
	}
	return append(out, plain)
}

// fmEntry is one tar entry; a zero Typeflag means a regular file.
type fmEntry struct {
	hdr  tar.Header
	body []byte
}

func fmStd(db, key []byte) []fmEntry {
	return []fmEntry{{hdr: tar.Header{Name: MemberDB}, body: db}, {hdr: tar.Header{Name: MemberKey}, body: key}}
}

func fmTar(t *testing.T, entries []fmEntry) []byte {
	t.Helper()
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, e := range entries {
		h := e.hdr
		h.Size = int64(len(e.body))
		if h.Typeflag == 0 {
			h.Typeflag = tar.TypeReg
		}
		h.Mode = 0o600
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write(e.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestFmParamsValidate(t *testing.T) {
	for _, tt := range []struct {
		name  string
		p     Params
		field string
	}{
		{"defaults", DefaultParams, ""},
		{"test params", TestParams, ""},
		{"lower bounds", Params{T: 1, M: 65536, P: 1}, ""},
		{"upper bounds", Params{T: 10, M: 1 << 20, P: 16}, ""},
		{"t zero", Params{T: 0, M: 65536, P: 1}, "argon2.t"},
		{"t above ten", Params{T: 11, M: 65536, P: 1}, "argon2.t"},
		{"m below 64 MiB", Params{T: 1, M: 65535, P: 1}, "argon2.m"},
		{"m above 1 GiB", Params{T: 1, M: 1<<20 + 1, P: 1}, "argon2.m"},
		{"p zero", Params{T: 1, M: 65536, P: 0}, "argon2.p"},
		{"p above sixteen", Params{T: 1, M: 65536, P: 17}, "argon2.p"},
	} {
		err := tt.p.Validate()
		if tt.field == "" {
			if err != nil {
				t.Errorf("%s: Validate = %v, want nil", tt.name, err)
			}
			continue
		}
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || !strings.Contains(ce.Detail, tt.field) {
			t.Errorf("%s: Validate = %v, want request.invalid_input naming %s", tt.name, err, tt.field)
		}
	}
	if DefaultParams != (Params{T: 3, M: 65536, P: 4}) || TestParams != (Params{T: 1, M: 65536, P: 1}) {
		t.Errorf("DefaultParams = %+v, TestParams = %+v", DefaultParams, TestParams)
	}
}

func TestFmReadVectors(t *testing.T) {
	wantFrames := map[string]int{"short.dpbk": 1, "multi.dpbk": 3}
	for _, v := range fmVectors() {
		file := fmVectorFile(t, v.file)
		if _, frames := fmSplit(t, file); len(frames) != wantFrames[v.file] {
			t.Errorf("%s holds %d frames, want %d", v.file, len(frames), wantFrames[v.file])
		}
		res := fmRead(bytes.NewReader(file), fmPass())
		if res.err != nil {
			t.Errorf("%s: Read = %v", v.file, res.err)
			continue
		}
		if !bytes.Equal(res.db, v.db) || !bytes.Equal(res.key, v.key) {
			t.Errorf("%s: members differ from the generator's (db %d bytes, key %d bytes)", v.file, len(res.db), len(res.key))
		}
		if !slices.Equal(res.order, []string{MemberDB, MemberKey}) {
			t.Errorf("%s: the sink saw %v, want controller.db then controller.key", v.file, res.order)
		}
		if want := (Header{Params: TestParams, Salt: fmSalt(), NoncePrefix: fmPrefix()}); res.header != want {
			t.Errorf("%s: header = %+v, want %+v", v.file, res.header, want)
		}
	}
	if len(fmVectors()[1].db) < 150*1024 {
		t.Error("the multi-chunk vector's database is under 150 KiB")
	}
}

func TestFmWriteVectors(t *testing.T) {
	for _, v := range fmVectors() {
		got := fmWrite(t, v.db, v.key)
		path := filepath.Join("testdata", v.file)
		if *fmUpdate {
			if err := os.MkdirAll("testdata", 0o750); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, got, 0o600); err != nil {
				t.Fatal(err)
			}
			continue
		}
		if want := fmVectorFile(t, v.file); !bytes.Equal(got, want) {
			t.Errorf("Write produced %d bytes that differ from %s (%d bytes); regenerate the vectors only for a deliberate format change", len(got), path, len(want))
		}
	}
}

func TestFmWriteLayout(t *testing.T) {
	v := fmVectors()[0]
	file := fmWrite(t, v.db, v.key)
	hdr, frames := fmSplit(t, file)
	salt, prefix := fmSalt(), fmPrefix()
	if string(hdr[:4]) != "DPBK" || hdr[4] != 1 || binary.BigEndian.Uint32(hdr[5:]) != 1 || binary.BigEndian.Uint32(hdr[9:]) != 65536 ||
		hdr[13] != 1 || !bytes.Equal(hdr[14:30], salt[:]) || !bytes.Equal(hdr[30:46], prefix[:]) {
		t.Errorf("header = %x", hdr)
	}
	if len(frames) != 1 {
		t.Fatalf("a 5.5 KiB archive is %d frames, want 1", len(frames))
	}
	tr := tar.NewReader(bytes.NewReader(fmOpen(t, file)))
	for _, want := range []struct {
		name string
		body []byte
	}{{MemberDB, v.db}, {MemberKey, v.key}} {
		h, err := tr.Next()
		if err != nil {
			t.Fatalf("tar member %s: %v", want.name, err)
		}
		if h.Name != want.name || h.Typeflag != tar.TypeReg || h.Mode != 0o600 || h.Uid != 0 || h.Gid != 0 || h.ModTime.Unix() != 0 || h.Size != int64(len(want.body)) {
			t.Errorf("tar header of %s = %+v", want.name, *h)
		}
		if body, err := io.ReadAll(tr); err != nil || !bytes.Equal(body, want.body) {
			t.Errorf("tar body of %s differs (%d bytes, %v)", want.name, len(body), err)
		}
	}
	if _, err := tr.Next(); !errors.Is(err, io.EOF) {
		t.Errorf("tar has a third entry: %v", err)
	}
}

// TestFmWriteChunking pins where chunks end: a plaintext that is an exact
// multiple of the chunk size ends in a full last chunk, never an empty one.
func TestFmWriteChunking(t *testing.T) {
	key := fmKeyFile(3)
	// The tar around a database of n bytes is c + n rounded up to 512.
	c := len(fmOpen(t, fmWrite(t, fmBytes(1000, 5), key))) - 1024
	for _, tt := range []struct {
		name       string
		dbSize     int
		wantPlain  int
		wantFrames int
		wantLast   int // plaintext bytes of the last frame
	}{
		{"one full chunk", ChunkSize - c, ChunkSize, 1, ChunkSize},
		{"two full chunks", 2*ChunkSize - c, 2 * ChunkSize, 2, ChunkSize},
		{"one block over a chunk", ChunkSize - c + 512, ChunkSize + 512, 2, 512},
	} {
		if tt.dbSize%512 != 0 {
			t.Fatalf("%s: database of %d bytes is not a multiple of 512; the tar overhead is %d", tt.name, tt.dbSize, c)
		}
		db := fmBytes(tt.dbSize, 6)
		file := fmWrite(t, db, key)
		_, frames := fmSplit(t, file)
		if len(frames) != tt.wantFrames {
			t.Errorf("%s: %d frames, want %d", tt.name, len(frames), tt.wantFrames)
			continue
		}
		if got := len(frames[len(frames)-1]) - 4 - 16; got != tt.wantLast {
			t.Errorf("%s: the last frame holds %d plaintext bytes, want %d", tt.name, got, tt.wantLast)
		}
		if plain := fmOpen(t, file); len(plain) != tt.wantPlain {
			t.Errorf("%s: plaintext is %d bytes, want %d", tt.name, len(plain), tt.wantPlain)
		}
		if res := fmRead(bytes.NewReader(file), fmPass()); res.err != nil || !bytes.Equal(res.db, db) || !bytes.Equal(res.key, key) {
			t.Errorf("%s: Read = %v, members equal %v", tt.name, res.err, bytes.Equal(res.db, db))
		}
	}
}

func TestFmReadArchiveEnds(t *testing.T) {
	build := fmBuilder(t)
	key := fmKeyFile(3)
	exact := ChunkSize - len(fmTar(t, fmStd(nil, key))) // a database that makes the tar exactly one chunk
	db := fmBytes(exact, 8)
	plain := fmTar(t, fmStd(db, key))
	if len(plain) != ChunkSize {
		t.Fatalf("tar is %d bytes, want %d", len(plain), ChunkSize)
	}
	short := fmTar(t, fmStd(fmBytes(100, 9), key))
	for _, tt := range []struct {
		name   string
		chunks [][]byte
		db     []byte
		want   error
	}{
		{"an empty last frame after a full chunk", append(fmChunks(plain), []byte{}), db, nil},
		{"an empty last frame after a short chunk", [][]byte{short, {}}, fmBytes(100, 9), nil},
		{"zero padding after the archive", fmChunks(append(bytes.Clone(short), make([]byte, 9000)...)), fmBytes(100, 9), nil},
		{"a byte after the archive", fmChunks(append(append(bytes.Clone(short), make([]byte, 100)...), 1)), nil, ErrMembers},
	} {
		res := fmRead(bytes.NewReader(build(tt.chunks)), fmPass())
		if !errors.Is(res.err, tt.want) || (tt.want == nil && res.err != nil) {
			t.Errorf("%s: Read = %v, want %v", tt.name, res.err, tt.want)
			continue
		}
		if tt.want == nil && (!bytes.Equal(res.db, tt.db) || !bytes.Equal(res.key, key)) {
			t.Errorf("%s: members differ", tt.name)
		}
	}
}

func TestFmReadMembers(t *testing.T) {
	build := fmBuilder(t)
	db := fmBytes(2000, 10)
	key := fmKeyFile(3)
	keyAt := func(n int) fmEntry {
		return fmEntry{hdr: tar.Header{Name: MemberKey}, body: bytes.Repeat([]byte{'k'}, n)}
	}
	dbEntry := fmEntry{hdr: tar.Header{Name: MemberDB}, body: db}
	keyEntry := fmEntry{hdr: tar.Header{Name: MemberKey}, body: key}
	for _, tt := range []struct {
		name    string
		entries []fmEntry
		want    error
	}{
		{"exactly the two members", []fmEntry{dbEntry, keyEntry}, nil},
		{"a key member at the limit", []fmEntry{dbEntry, keyAt(MaxKeyMember)}, nil},
		{"a key member over the limit", []fmEntry{dbEntry, keyAt(MaxKeyMember + 1)}, ErrMembers},
		{"the members in the wrong order", []fmEntry{keyEntry, dbEntry}, ErrMembers},
		{"an extra member", []fmEntry{dbEntry, keyEntry, {hdr: tar.Header{Name: "extra.txt"}, body: []byte("x")}}, ErrMembers},
		{"the database only", []fmEntry{dbEntry}, ErrMembers},
		{"the key only", []fmEntry{keyEntry}, ErrMembers},
		{"no member", nil, ErrMembers},
		{"the database twice", []fmEntry{dbEntry, dbEntry}, ErrMembers},
		{"a symbolic link for the database", []fmEntry{{hdr: tar.Header{Name: MemberDB, Typeflag: tar.TypeSymlink, Linkname: "/etc/passwd"}}, keyEntry}, ErrMembers},
		{"a hard link for the key", []fmEntry{dbEntry, {hdr: tar.Header{Name: MemberKey, Typeflag: tar.TypeLink, Linkname: MemberDB}}}, ErrMembers},
		{"a directory for the database", []fmEntry{{hdr: tar.Header{Name: MemberDB, Typeflag: tar.TypeDir}}, keyEntry}, ErrMembers},
		{"a path with a slash", []fmEntry{{hdr: tar.Header{Name: "dir/" + MemberDB}, body: db}, keyEntry}, ErrMembers},
		{"a dot-slash path", []fmEntry{{hdr: tar.Header{Name: "./" + MemberDB}, body: db}, keyEntry}, ErrMembers},
		{"an absolute path", []fmEntry{dbEntry, {hdr: tar.Header{Name: "/" + MemberKey}, body: key}}, ErrMembers},
	} {
		res := fmRead(bytes.NewReader(build(fmChunks(fmTar(t, tt.entries)))), fmPass())
		switch {
		case tt.want == nil && res.err != nil:
			t.Errorf("%s: Read = %v", tt.name, res.err)
		case tt.want != nil && !errors.Is(res.err, tt.want):
			t.Errorf("%s: Read = %v, want %v", tt.name, res.err, tt.want)
		}
	}
}

func TestFmReadDamage(t *testing.T) {
	type mutate func(hdr []byte, frames [][]byte) []byte
	flip := func(frame int, at int) mutate {
		return func(hdr []byte, frames [][]byte) []byte {
			frames = slices.Clone(frames)
			if frame < 0 {
				frame = len(frames) + frame
			}
			f := bytes.Clone(frames[frame])
			if at < 0 {
				at = len(f) + at
			}
			f[at] ^= 1
			frames[frame] = f
			return fmJoin(hdr, frames...)
		}
	}
	flipHeader := func(at int, x byte) mutate {
		return func(hdr []byte, frames [][]byte) []byte {
			h := bytes.Clone(hdr)
			h[at] ^= x
			return fmJoin(h, frames...)
		}
	}
	for _, tt := range []struct {
		name string
		file string
		do   mutate
		want error
	}{
		{"the last frame is missing", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[:len(f)-1]...) }, ErrTruncated},
		{"every frame is missing", "short.dpbk", func(h []byte, _ [][]byte) []byte { return fmJoin(h) }, ErrTruncated},
		{"the file ends inside the last frame", "multi.dpbk", func(h []byte, f [][]byte) []byte {
			b := fmJoin(h, f...)
			return b[:len(b)-10]
		}, ErrTruncated},
		{"the file ends inside the first frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0][:20]) }, ErrTruncated},
		{"the file ends inside a frame length", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[1][:2]) }, ErrTruncated},
		{"a byte of the first frame is flipped", "multi.dpbk", flip(0, 104), ErrAuth},
		{"a byte of the last frame is flipped", "multi.dpbk", flip(-1, 9), ErrAuth},
		{"the tag of the last frame is flipped", "short.dpbk", flip(-1, -1), ErrAuth},
		{"a frame length grows past the bound", "multi.dpbk", flip(0, 3), ErrAuth},
		{"a frame length is below the tag", "multi.dpbk", func(h []byte, f [][]byte) []byte {
			f = slices.Clone(f)
			bad := bytes.Clone(f[0])
			binary.BigEndian.PutUint32(bad, 15)
			f[0] = bad
			return fmJoin(h, f...)
		}, ErrAuth},
		{"two frames are swapped", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[1], f[0], f[2]) }, ErrAuth},
		{"a middle frame is dropped", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[2]) }, ErrAuth},
		{"a frame is repeated", "multi.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[0], f[1], f[2]) }, ErrAuth},
		{"a frame follows the last frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return fmJoin(h, f[0], f[0]) }, ErrAuth},
		{"a byte follows the last frame", "short.dpbk", func(h []byte, f [][]byte) []byte { return append(fmJoin(h, f...), 0) }, ErrAuth},
		{"the salt is changed", "short.dpbk", flipHeader(14, 1), ErrAuth},
		{"the nonce prefix is changed", "short.dpbk", flipHeader(30, 1), ErrAuth},
		{"argon2 t is changed to another valid value", "short.dpbk", flipHeader(8, 2), ErrAuth},
		{"the version is changed", "short.dpbk", flipHeader(4, 1), ErrNotBackup},
		{"the magic is changed", "short.dpbk", flipHeader(0, 1), ErrNotBackup},
	} {
		file := fmVectorFile(t, tt.file)
		hdr, frames := fmSplit(t, file)
		_, err := Read(bytes.NewReader(tt.do(hdr, frames)), fmPass(), func(string, int64, io.Reader) error { return nil })
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Read = %v, want %v", tt.name, err, tt.want)
		}
		for _, other := range []error{ErrNotBackup, ErrHeader, ErrAuth, ErrTruncated, ErrMembers} {
			if !errors.Is(other, tt.want) && errors.Is(err, other) {
				t.Errorf("%s: Read = %v, which also matches %v", tt.name, err, other)
			}
		}
	}
}

func TestFmReadWrongPassphrase(t *testing.T) {
	file := fmVectorFile(t, "short.dpbk")
	called := false
	_, err := Read(bytes.NewReader(file), []byte("not the passphrase"), func(string, int64, io.Reader) error {
		called = true
		return nil
	})
	if !errors.Is(err, ErrAuth) || errors.Is(err, ErrTruncated) {
		t.Errorf("Read with a wrong passphrase = %v, want ErrAuth", err)
	}
	if called {
		t.Error("a member reached the sink before its frame authenticated")
	}
}

func fmHeaderBytes(version byte, t, m uint32, p uint8) []byte {
	h := make([]byte, HeaderSize)
	copy(h, Magic)
	h[4] = version
	binary.BigEndian.PutUint32(h[5:], t)
	binary.BigEndian.PutUint32(h[9:], m)
	h[13] = p
	return h
}

// TestFmReadHeader: a header out of bounds is refused before any key is
// derived (argon2 would panic on t or p of zero and allocate m KiB).
func TestFmReadHeader(t *testing.T) {
	good := fmHeaderBytes(1, 1, 65536, 1)
	for _, tt := range []struct {
		name string
		in   []byte
		want error
	}{
		{"empty input", nil, ErrNotBackup},
		{"the magic only", []byte("DPBK"), ErrNotBackup},
		{"another magic", append([]byte("XPBK"), good[4:]...), ErrNotBackup},
		{"version 2", fmHeaderBytes(2, 1, 65536, 1), ErrNotBackup},
		{"version 0", fmHeaderBytes(0, 1, 65536, 1), ErrNotBackup},
		{"the header cut after the version", good[:5], ErrTruncated},
		{"the header cut inside the nonce prefix", good[:HeaderSize-1], ErrTruncated},
		{"t zero", fmHeaderBytes(1, 0, 65536, 1), ErrHeader},
		{"t eleven", fmHeaderBytes(1, 11, 65536, 1), ErrHeader},
		{"t at the maximum of uint32", fmHeaderBytes(1, 1<<32-1, 65536, 1), ErrHeader},
		{"m below 64 MiB", fmHeaderBytes(1, 1, 65535, 1), ErrHeader},
		{"m zero", fmHeaderBytes(1, 1, 0, 1), ErrHeader},
		{"m above 1 GiB", fmHeaderBytes(1, 1, 1<<20+1, 1), ErrHeader},
		{"p zero", fmHeaderBytes(1, 1, 65536, 0), ErrHeader},
		{"p seventeen", fmHeaderBytes(1, 1, 65536, 17), ErrHeader},
		{"p 255", fmHeaderBytes(1, 1, 65536, 255), ErrHeader},
		{"m at the maximum of uint32", fmHeaderBytes(1, 1, 1<<32-1, 1), ErrHeader},
		{"a valid header and no frame", good, ErrTruncated},
	} {
		_, err := Read(bytes.NewReader(tt.in), fmPass(), func(string, int64, io.Reader) error { return nil })
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Read = %v, want %v", tt.name, err, tt.want)
		}
	}
}

func TestFmReadSink(t *testing.T) {
	file := fmVectorFile(t, "short.dpbk")
	errSink := errors.New("sink refused")
	_, err := Read(bytes.NewReader(file), fmPass(), func(string, int64, io.Reader) error { return errSink })
	if !errors.Is(err, errSink) {
		t.Errorf("Read with a failing sink = %v, want the sink's error", err)
	}
	var names []string
	if _, err := Read(bytes.NewReader(file), fmPass(), func(name string, _ int64, _ io.Reader) error {
		names = append(names, name)
		return nil
	}); err != nil || !slices.Equal(names, []string{MemberDB, MemberKey}) {
		t.Errorf("Read with a sink that ignores the bodies = %v after %v, want nil after both members", err, names)
	}
}

func TestFmReadShortReads(t *testing.T) {
	v := fmVectors()[0]
	file := fmVectorFile(t, v.file)
	for name, r := range map[string]io.Reader{
		"one byte at a time":  iotest.OneByteReader(bytes.NewReader(file)),
		"data with its error": iotest.DataErrReader(bytes.NewReader(file)),
	} {
		res := fmRead(r, fmPass())
		if res.err != nil || !bytes.Equal(res.db, v.db) || !bytes.Equal(res.key, v.key) {
			t.Errorf("%s: Read = %v, members equal %v", name, res.err, bytes.Equal(res.db, v.db))
		}
	}
}

func TestFmWriteRefuses(t *testing.T) {
	keyAt := func(n int) Member {
		return Member{Name: MemberKey, Size: int64(n), Body: bytes.NewReader(bytes.Repeat([]byte{'k'}, n))}
	}
	dbOf := func(n int) Member {
		return Member{Name: MemberDB, Size: int64(n), Body: bytes.NewReader(make([]byte, n))}
	}
	for _, tt := range []struct {
		name    string
		p       Params
		members []Member
		want    error
	}{
		{"no member", TestParams, nil, ErrMembers},
		{"the database only", TestParams, []Member{dbOf(10)}, ErrMembers},
		{"the members in the wrong order", TestParams, []Member{keyAt(10), dbOf(10)}, ErrMembers},
		{"a third member", TestParams, []Member{dbOf(10), keyAt(10), dbOf(1)}, ErrMembers},
		{"another name", TestParams, []Member{{Name: "other", Size: 1, Body: bytes.NewReader([]byte{1})}, keyAt(10)}, ErrMembers},
		{"a key member over the limit", TestParams, []Member{dbOf(10), keyAt(MaxKeyMember + 1)}, ErrMembers},
		{"a negative size", TestParams, []Member{{Name: MemberDB, Size: -1, Body: bytes.NewReader(nil)}, keyAt(10)}, ErrMembers},
	} {
		var buf bytes.Buffer
		err := Write(&buf, fmRnd(), fmPass(), tt.p, tt.members)
		if !errors.Is(err, tt.want) {
			t.Errorf("%s: Write = %v, want %v", tt.name, err, tt.want)
		}
		if buf.Len() != 0 {
			t.Errorf("%s: Write wrote %d bytes before refusing", tt.name, buf.Len())
		}
	}
	var buf bytes.Buffer
	err := Write(&buf, fmRnd(), fmPass(), Params{T: 0, M: 65536, P: 1}, fmMembers([]byte("db"), []byte("key")))
	var ce *errcode.Error
	if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || buf.Len() != 0 {
		t.Errorf("Write with t=0 = %v after %d bytes, want request.invalid_input and nothing written", err, buf.Len())
	}
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{dbOf(10), keyAt(MaxKeyMember)}); err != nil {
		t.Errorf("Write with a key member at the limit = %v", err)
	}
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{dbOf(10), {Name: MemberKey, Size: 10, Body: bytes.NewReader([]byte("short"))}}); err == nil {
		t.Error("Write accepted a member whose body ends before its size")
	}
	body := bytes.NewReader([]byte("0123456789"))
	if err := Write(io.Discard, fmRnd(), fmPass(), TestParams, []Member{{Name: MemberDB, Size: 4, Body: body}, keyAt(10)}); err != nil || body.Len() != 6 {
		t.Errorf("Write with a longer body = %v, %d bytes left unread; want nil and 6", err, body.Len())
	}
}

var fmErrWrite = errors.New("disk full")

type fmFailWriter struct{ budget int }

func (w *fmFailWriter) Write(p []byte) (int, error) {
	if len(p) > w.budget {
		return 0, fmErrWrite
	}
	w.budget -= len(p)
	return len(p), nil
}

func TestFmWriteFailures(t *testing.T) {
	v := fmVectors()[0]
	if err := Write(io.Discard, bytes.NewReader(make([]byte, 16)), fmPass(), TestParams, fmMembers(v.db, v.key)); err == nil {
		t.Error("Write accepted 16 random bytes where it needs 32")
	}
	for name, budget := range map[string]int{"the header": 0, "the first frame": HeaderSize + 10} {
		err := Write(&fmFailWriter{budget: budget}, fmRnd(), fmPass(), TestParams, fmMembers(v.db, v.key))
		if !errors.Is(err, fmErrWrite) {
			t.Errorf("Write failing at %s = %v, want the writer's error", name, err)
		}
	}
}

func TestFmClearsPassphrase(t *testing.T) {
	v := fmVectors()[0]
	zeroed := func(b []byte) bool { return !slices.ContainsFunc(b, func(c byte) bool { return c != 0 }) }
	pass := fmPass()
	if err := Write(io.Discard, fmRnd(), pass, TestParams, fmMembers(v.db, v.key)); err != nil || !zeroed(pass) {
		t.Errorf("after Write: err %v, passphrase zeroed %v", err, zeroed(pass))
	}
	pass = fmPass()
	if res := fmRead(bytes.NewReader(fmVectorFile(t, v.file)), pass); res.err != nil || !zeroed(pass) {
		t.Errorf("after Read: err %v, passphrase zeroed %v", res.err, zeroed(pass))
	}
	pass = []byte("wrong")
	if res := fmRead(bytes.NewReader(fmVectorFile(t, v.file)), pass); !errors.Is(res.err, ErrAuth) || !zeroed(pass) {
		t.Errorf("after a failed Read: err %v, passphrase zeroed %v", res.err, zeroed(pass))
	}
	pass = fmPass()
	if _, err := Read(bytes.NewReader(nil), pass, nil); !errors.Is(err, ErrNotBackup) || !zeroed(pass) {
		t.Errorf("after a refused header: err %v, passphrase zeroed %v", err, zeroed(pass))
	}
}

// TestFmMemoryBound streams a 128 MiB database through Write and Read; the
// allocation of each is argon2id's 64 MiB plus a margin, not the database.
func TestFmMemoryBound(t *testing.T) {
	const dbSize = 128 << 20
	key := fmKeyFile(3)
	limit := uint64(64+32) << 20
	path := filepath.Join(t.TempDir(), "big.dpbk")
	out, err := os.Create(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	written := sha256.New()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	err = Write(out, fmRnd(), fmPass(), TestParams, []Member{
		{Name: MemberDB, Size: dbSize, Body: io.TeeReader(newFmStream(dbSize, 7), written)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	runtime.ReadMemStats(&after)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew >= limit {
		t.Errorf("Write allocated %d MiB for a %d MiB database, want under %d MiB", grew>>20, dbSize>>20, limit>>20)
	}
	if st, err := os.Stat(path); err != nil || st.Size() < dbSize {
		t.Fatalf("backup file: %v, %v", st, err)
	}

	in, err := os.Open(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = in.Close() }()
	read := sha256.New()
	var sawDB int64
	runtime.ReadMemStats(&before)
	_, err = Read(in, fmPass(), func(name string, size int64, body io.Reader) error {
		dst := io.Discard
		if name == MemberDB {
			dst = read
			sawDB = size
		}
		_, err := io.Copy(dst, body)
		return err
	})
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if grew := after.TotalAlloc - before.TotalAlloc; grew >= limit {
		t.Errorf("Read allocated %d MiB for a %d MiB database, want under %d MiB", grew>>20, dbSize>>20, limit>>20)
	}
	if sawDB != dbSize || !bytes.Equal(read.Sum(nil), written.Sum(nil)) {
		t.Errorf("Read gave %d database bytes, digest equal %v", sawDB, bytes.Equal(read.Sum(nil), written.Sum(nil)))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w7-tmp/1 && export TMPDIR=/root/.cache/w7-tmp/1 GOTOOLCHAIN=local
df -h /
timeout 300 go test -count=1 ./internal/controller/backup 2>&1 | head -20
```

When `df -h /` shows 90 % or more, run `go clean -cache` first (Global Constraints). Expected: FAIL with a build error naming the first undefined identifier (`undefined: Params`, then `Write`, `Read`, `Member`, `Magic`, ...). Quote the output in the report.

- [ ] **Step 3: Implement**

Create `internal/controller/backup/format.go` (package `backup`; the package comment lives in `contribute.go`). Every exported identifier has a doc comment. Name every unexported identifier of this file with the prefix `frame` or `format` (for example `frameWriter`, `frameReader`, `formatKey`), so no other task's helper in this package can collide with it.

1. Declare the constants, `Params`, `DefaultParams`, `TestParams`, `Member`, `Header` and the five sentinel errors exactly as Contracts "Format" gives them, with the error texts verbatim.
2. `Params.Validate` checks `T` (1 to 10), then `M` (65536 to 1048576 KiB), then `P` (1 to 16) and returns, for the first parameter out of bounds, `errcode.New(errcode.InvalidInput, <detail>)` whose detail names `argon2.t`, `argon2.m` or `argon2.p`. It returns nil for `DefaultParams`, `TestParams` and both ends of every range.
3. `Write(w, rnd, passphrase, p, members)`:
   1. `defer clear(passphrase)`.
   2. Before writing a byte: return the `Validate` error unchanged for bad parameters; refuse, with an error that wraps `ErrMembers` (`fmt.Errorf("%w: ...", ErrMembers)`), anything but exactly two members named `MemberDB` then `MemberKey`, a negative `Size`, and a key member whose `Size` is over `MaxKeyMember`.
   3. Read 32 bytes from `rnd` with `io.ReadFull` (salt, then nonce prefix); a short read is an error that wraps the reader's error.
   4. Derive the 32-byte key with `argon2.IDKey(passphrase, salt, T, M, P, 32)`, `clear(passphrase)` at once, build the AEAD with `chacha20poly1305.NewX`, and `defer clear(key)`.
   5. Write the 46-byte header (`Magic`, `FormatVersion`, `T` and `M` as big-endian uint32, `P`, salt, nonce prefix), then the frames of a tar made with `archive/tar`: per member `tar.Header{Typeflag: tar.TypeReg, Name, Size, Mode: 0o600, ModTime: time.Unix(0, 0)}` with `Uid` and `Gid` 0, `Format` left unspecified and no user or group names, then exactly `Size` bytes copied from `Body` (never more: `io.LimitReader`). A body that ends early makes `Write` fail with an error (the tar writer reports the missed bytes). Close the tar writer so its trailer is part of the plaintext.
   6. The frame writer collects plaintext in one reusable `ChunkSize` buffer. It seals a full buffer as a non-last chunk (associated data `header || 0x00`) only when more plaintext arrives, and seals what remains at the end as the last chunk (`header || 0x01`), so a plaintext that is an exact multiple of `ChunkSize` ends in a full last chunk and never in an empty one. A frame is a big-endian uint32 ciphertext length followed by the ciphertext, handed to `w` in a single `Write` call. The nonce is the 16-byte prefix followed by the chunk index as uint64 big-endian; the index starts at 0; a stream of more than 2^32 chunks is an error.
   7. An error from `w` is returned wrapped with `%w` (the tests use `errors.Is`), whichever write failed.
4. `Read(r, passphrase, sink)`:
   1. `defer clear(passphrase)`. Wrap `r` in a `bufio.Reader` of fixed size so the next byte can be peeked.
   2. Header. Fewer than 5 bytes available (an empty input included), a magic other than `DPBK` or a version other than 1 give `ErrNotBackup`. EOF inside the remaining 41 header bytes gives `ErrTruncated`. A header whose parameters fail `Validate` gives an error that wraps `ErrHeader` (and the `Validate` error); this check, and all of the above, happen before `argon2.IDKey` is called, because argon2 panics on `T` or `P` of zero and allocates `M` KiB.
   3. Derive and clear exactly as in `Write`; return the parsed `Header` (parameters, salt, nonce prefix) with a nil error only when everything below succeeded.
   4. Frames. A length prefix or ciphertext cut by EOF, and EOF where the first frame should start, give `ErrTruncated`. A length below 16 or above `ChunkSize + 16`, or a chunk index of 2^32, gives `ErrAuth`. After reading a frame's ciphertext, peek one byte. If nothing follows, open the frame with `0x01`; on failure try `0x00`: success there means the file was cut at a frame boundary (`ErrTruncated`), failure means `ErrAuth`. If more bytes follow, open the frame with `0x00`; on failure the result is `ErrAuth` (when `0x01` would have opened it, say in the error text that data follows the last frame). Open into a separate plaintext buffer so a failed attempt leaves the ciphertext intact for the second attempt, and never hand plaintext of a frame that did not open to the tar reader. An empty last frame (16 bytes of ciphertext) is valid.
   5. Members. Feed the plaintext to `archive/tar.Reader`. Take the first entry: it must be a regular file named exactly `controller.db`; the second exactly `controller.key` with a size of at most `MaxKeyMember`; a different name (a path with a slash, `./`, an absolute path included), type (symbolic link, hard link, directory), order, a missing member or a third member gives an error that wraps `ErrMembers`. Call `sink(name, size, io.LimitReader(tr, size))` for each, in order; an error from the sink is returned as it is. When the frame reader has failed, return its error (`ErrAuth` or `ErrTruncated`) and not the tar reader's short-read error that follows from it.
   6. After the tar's end, read the rest of the plaintext through the frame reader to its end. It must be all zero bytes (tar padding); any other byte gives `ErrMembers`. Reading to the end is what authenticates the last frame, so `Read` returns nil only after a frame that opened with `0x01`.
5. Memory. Allocate the plaintext buffer, the ciphertext buffer (`ChunkSize + 16`), the nonce and the associated-data slices once per call and reuse them for every frame, so a call allocates argon2id's `M` KiB plus O(`ChunkSize`) however large the database is (the memory test measures `runtime.MemStats.TotalAlloc`). Slice sizes taken from the input are used only after the bounds of item 4.4, so no input makes `Read` panic or allocate by its own size.
6. Generate the vectors. They are written by `TestFmWriteVectors` run with `-update`, which uses the fixed salt, nonce prefix and passphrase and the deterministic members the test defines; run it once, from the repository root:

```bash
timeout 300 go test -count=1 -run '^TestFmWriteVectors$' ./internal/controller/backup -args -update
```

   It creates `internal/controller/backup/testdata/short.dpbk` (one frame) and `testdata/multi.dpbk` (three frames, a database member of 150 KiB plus 777 bytes). Run the same test again without `-update`: it must pass, and `git status` must show exactly the two new files. Regenerate them only for a deliberate format change.

- [ ] **Step 4: Run test to verify it passes**

```bash
mkdir -p /root/.cache/w7-tmp/1 && export TMPDIR=/root/.cache/w7-tmp/1 GOTOOLCHAIN=local
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 600 go test -count=1 ./internal/controller/backup
timeout 900 go test -race -short -count=1 ./internal/controller/backup
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/1 && go clean -testcache
```

Expected: every test passes (`ok`), `go vet` prints nothing, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing. `TestFmMemoryBound` writes a 128 MiB temporary file under `TMPDIR`; `df -h /` must show room for it. Record the real output in the report.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/format.go internal/controller/backup/format_test.go internal/controller/backup/testdata/short.dpbk internal/controller/backup/testdata/multi.dpbk
git commit -m "feat(backup): add the encrypted backup format"
```

### Task 2: Durable file operations and the crash wrapper

**Files:**
- Create: `internal/controller/backup/durable.go`
- Create: `internal/controller/backup/durable_crash_test.go`
- Create: `internal/controller/backup/durable_test.go`

**Interfaces:**
- Consumes: nothing from earlier tasks.
- Produces: Contracts "Durable file operations": `File`, `Ops`, `OSOps` in `durable.go`, and in `durable_crash_test.go` `errCrash`, `crashOps`, `newCrashOps`, `(*crashOps).Calls` and `(*crashOps).Crashed`, which the tests of every task that takes an `Ops` (journal, restore, recovery) wrap around `OSOps()`. Beyond Contracts: a call number `at` that no call reaches (`0`, or more than the calls made) never crashes, which makes `newCrashOps(ops, 0, true)` a counting run; calls that arrive after the crash are not counted, so after a crash `Calls()` equals `at`.

**Items:** 54

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/durable_test.go` with the code below. It tests `OSOps` on real files in `t.TempDir()` and `crashOps` over `OSOps`: the counting (every Ops method, every File method), each of the ten calls of a script crashed before and after (the directory shows whether the call ran), the dead state after a crash, and a Create that crashes after it ran.

```go
package backup

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

func duWriteFile(t *testing.T, ops Ops, path, data string) {
	t.Helper()
	f, err := ops.Create(path, 0o600)
	if err != nil {
		t.Fatalf("Create %s: %v", path, err)
	}
	if _, err := f.Write([]byte(data)); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
}

func duRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestDuOSOpsCreate(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	p := filepath.Join(dir, "staged")
	duWriteFile(t, ops, p, "hello")
	st, err := os.Stat(p)
	if err != nil || st.Mode() != 0o600 || duRead(t, p) != "hello" {
		t.Fatalf("created file: %v, %v, %q; want mode 0600 holding hello", st, err, duRead(t, p))
	}

	if f, err := ops.Create(p, 0o600); !errors.Is(err, fs.ErrExist) || f != nil {
		t.Errorf("Create over an existing file = %v, %v; want fs.ErrExist and no file", f, err)
	}
	if duRead(t, p) != "hello" {
		t.Error("a refused Create changed the existing file")
	}

	target := filepath.Join(dir, "victim")
	for name, setup := range map[string]func(link string){
		"a dangling link":  func(link string) { _ = os.Symlink(target, link) },
		"a link to a file": func(link string) { duWriteFile(t, ops, target, "keep"); _ = os.Symlink(target, link) },
	} {
		link := filepath.Join(dir, strings.ReplaceAll(name, " ", "-"))
		setup(link)
		if f, err := ops.Create(link, 0o600); err == nil {
			_ = f.Close()
			t.Errorf("Create through %s succeeded", name)
		}
	}
	if _, err := os.Stat(target); err == nil && duRead(t, target) != "keep" {
		t.Error("Create wrote through a symbolic link")
	}

	if f, err := ops.Create(filepath.Join(dir, "missing", "file"), 0o600); !errors.Is(err, fs.ErrNotExist) || f != nil {
		t.Errorf("Create in a missing directory = %v, %v; want fs.ErrNotExist", f, err)
	}
}

func TestDuOSOpsRenameAndRemove(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	duWriteFile(t, ops, a, "A")
	duWriteFile(t, ops, b, "B")
	if err := ops.Rename(a, b); err != nil || duRead(t, b) != "A" {
		t.Fatalf("Rename over an existing name = %v, target holds %q; want it replaced by A", err, duRead(t, b))
	}
	if _, err := os.Lstat(a); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the source still exists after Rename: %v", err)
	}
	if err := ops.Rename(a, b); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename of a missing source = %v, want fs.ErrNotExist", err)
	}

	if err := ops.Remove(b); err != nil {
		t.Errorf("Remove of a file = %v", err)
	}
	if err := ops.Remove(b); err != nil {
		t.Errorf("Remove of a missing path = %v, want nil", err)
	}
	keep := filepath.Join(dir, "keep")
	duWriteFile(t, ops, keep, "kept")
	link := filepath.Join(dir, "link")
	if err := os.Symlink(keep, link); err != nil {
		t.Fatal(err)
	}
	if err := ops.Remove(link); err != nil || duRead(t, keep) != "kept" {
		t.Errorf("Remove of a link = %v; the target must stay", err)
	}
	if _, err := os.Lstat(link); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the link survived Remove: %v", err)
	}
	full := filepath.Join(dir, "full")
	if err := os.Mkdir(full, 0o700); err != nil {
		t.Fatal(err)
	}
	duWriteFile(t, ops, filepath.Join(full, "x"), "x")
	if err := ops.Remove(full); err == nil {
		t.Error("Remove deleted a directory that holds files")
	}
}

func TestDuOSOpsDirectoryAndMetadata(t *testing.T) {
	dir := t.TempDir()
	ops := OSOps()
	if err := ops.SyncDir(dir); err != nil {
		t.Errorf("SyncDir of a directory = %v", err)
	}
	if err := ops.SyncDir(filepath.Join(dir, "missing")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("SyncDir of a missing directory = %v, want fs.ErrNotExist", err)
	}

	p := filepath.Join(dir, "f")
	duWriteFile(t, ops, p, "x")
	if err := ops.Chmod(p, 0o640); err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(p); err != nil || st.Mode().Perm() != 0o640 {
		t.Errorf("mode after Chmod = %v, %v; want 0640", st.Mode().Perm(), err)
	}
	if err := ops.Chmod(filepath.Join(dir, "missing"), 0o600); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chmod of a missing path = %v, want fs.ErrNotExist", err)
	}
	if err := ops.Chown(p, os.Getuid(), os.Getgid()); err != nil {
		t.Errorf("Chown to the owner's own ids = %v", err)
	}
	if err := ops.Chown(filepath.Join(dir, "missing"), os.Getuid(), os.Getgid()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chown of a missing path = %v, want fs.ErrNotExist", err)
	}
	dangling := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), dangling); err != nil {
		t.Fatal(err)
	}
	if err := ops.Chown(dangling, os.Getuid(), os.Getgid()); err != nil {
		t.Errorf("Chown of a dangling link = %v; it must change the link, not follow it", err)
	}

	for _, tt := range []struct {
		name, path string
		want       bool
	}{
		{"a file", p, true}, {"a directory", dir, true}, {"a dangling link", dangling, true}, {"a missing path", filepath.Join(dir, "nope"), false},
	} {
		if got, err := ops.Exists(tt.path); err != nil || got != tt.want {
			t.Errorf("Exists(%s) = %v, %v; want %v", tt.name, got, err, tt.want)
		}
	}
}

// duScript makes one call of each kind in a fixed order and stops at the
// first error: Create, Write, Sync, Close, Rename, Exists, Chmod, Chown,
// SyncDir, Remove.
func duScript(ops Ops, dir string) error {
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	f, err := ops.Create(a, 0o600)
	if err != nil {
		return err
	}
	if _, err := f.Write([]byte("data")); err != nil {
		return err
	}
	if err := f.Sync(); err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := ops.Rename(a, b); err != nil {
		return err
	}
	if _, err := ops.Exists(b); err != nil {
		return err
	}
	if err := ops.Chmod(b, 0o640); err != nil {
		return err
	}
	if err := ops.Chown(b, os.Getuid(), os.Getgid()); err != nil {
		return err
	}
	if err := ops.SyncDir(dir); err != nil {
		return err
	}
	return ops.Remove(b)
}

const duScriptCalls = 10

// duState lists dir as sorted "name=content:mode" words.
func duState(t *testing.T, dir string) string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, e.Name()+"="+duRead(t, filepath.Join(dir, e.Name()))+":"+info.Mode().Perm().String())
	}
	sort.Strings(out)
	return strings.Join(out, " ")
}

func TestDuCrashOpsCountsWithoutCrashing(t *testing.T) {
	for _, at := range []int{0, duScriptCalls + 1, 1 << 30} {
		dir := t.TempDir()
		c := newCrashOps(OSOps(), at, true)
		if err := duScript(c, dir); err != nil {
			t.Fatalf("at %d: the script failed: %v", at, err)
		}
		if c.Calls() != duScriptCalls || c.Crashed() {
			t.Errorf("at %d: Calls = %d, Crashed = %v; want %d and false (every Ops and File method counts)", at, c.Calls(), c.Crashed(), duScriptCalls)
		}
		if s := duState(t, dir); s != "" {
			t.Errorf("at %d: the script left %q", at, s)
		}
	}
}

func TestDuCrashOpsCountsFilesOfSeveralCreates(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 0, true)
	f1, err := c.Create(filepath.Join(dir, "1"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	f2, err := c.Create(filepath.Join(dir, "2"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []File{f1, f2} {
		if _, err := f.Write([]byte("x")); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if c.Calls() != 8 {
		t.Errorf("Calls = %d, want 8: two Creates and three File calls on each file", c.Calls())
	}
}

func TestDuCrashOpsErrorsOfTheInnerOpsPass(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 0, true)
	if err := c.Rename(filepath.Join(dir, "missing"), filepath.Join(dir, "to")); !errors.Is(err, fs.ErrNotExist) || errors.Is(err, errCrash) {
		t.Errorf("Rename of a missing file = %v, want the inner fs.ErrNotExist", err)
	}
	if c.Calls() != 1 || c.Crashed() {
		t.Errorf("Calls = %d, Crashed = %v; a failing call still counts and is no crash", c.Calls(), c.Crashed())
	}
}

// TestDuCrashOpsEveryCall crashes the script on each of its calls, before
// and after, and checks what the disk holds: a call that crashed before ran
// has no effect, one that crashed after ran has its full effect.
func TestDuCrashOpsEveryCall(t *testing.T) {
	type outcome struct{ before, after string }
	want := map[int]outcome{
		1:  {"", "a=:-rw-------"},
		2:  {"a=:-rw-------", "a=data:-rw-------"},
		3:  {"a=data:-rw-------", "a=data:-rw-------"},
		4:  {"a=data:-rw-------", "a=data:-rw-------"},
		5:  {"a=data:-rw-------", "b=data:-rw-------"},
		6:  {"b=data:-rw-------", "b=data:-rw-------"},
		7:  {"b=data:-rw-------", "b=data:-rw-r-----"},
		8:  {"b=data:-rw-r-----", "b=data:-rw-r-----"},
		9:  {"b=data:-rw-r-----", "b=data:-rw-r-----"},
		10: {"b=data:-rw-r-----", ""},
	}
	for k := 1; k <= duScriptCalls; k++ {
		for _, before := range []bool{true, false} {
			dir := t.TempDir()
			c := newCrashOps(OSOps(), k, before)
			err := duScript(c, dir)
			if !errors.Is(err, errCrash) || !c.Crashed() || c.Calls() != k {
				t.Errorf("call %d before=%v: err %v, Crashed %v, Calls %d; want errCrash, true, %d", k, before, err, c.Crashed(), c.Calls(), k)
			}
			w := want[k].after
			if before {
				w = want[k].before
			}
			if got := duState(t, dir); got != w {
				t.Errorf("call %d before=%v: the directory holds %q, want %q", k, before, got, w)
			}
		}
	}
}

// TestDuCrashOpsDeadAfterCrash: after the crash nothing runs, whatever the
// method, and the count stays where it stopped.
func TestDuCrashOpsDeadAfterCrash(t *testing.T) {
	dir := t.TempDir()
	a, b, later := filepath.Join(dir, "a"), filepath.Join(dir, "b"), filepath.Join(dir, "later")
	c := newCrashOps(OSOps(), 3, false)
	f, err := c.Create(a, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	if err := f.Sync(); !errors.Is(err, errCrash) {
		t.Fatalf("the crashing Sync = %v, want errCrash", err)
	}
	if _, err := f.Write([]byte("after")); !errors.Is(err, errCrash) {
		t.Errorf("Write after the crash = %v, want errCrash", err)
	}
	if err := f.Sync(); !errors.Is(err, errCrash) {
		t.Errorf("Sync after the crash = %v, want errCrash", err)
	}
	if err := f.Close(); !errors.Is(err, errCrash) {
		t.Errorf("Close after the crash = %v, want errCrash", err)
	}
	if g, err := c.Create(later, 0o600); !errors.Is(err, errCrash) || g != nil {
		t.Errorf("Create after the crash = %v, %v; want no file and errCrash", g, err)
	}
	if err := c.Rename(a, b); !errors.Is(err, errCrash) {
		t.Errorf("Rename after the crash = %v, want errCrash", err)
	}
	if err := c.Remove(a); !errors.Is(err, errCrash) {
		t.Errorf("Remove after the crash = %v, want errCrash", err)
	}
	if err := c.SyncDir(dir); !errors.Is(err, errCrash) {
		t.Errorf("SyncDir after the crash = %v, want errCrash", err)
	}
	if err := c.Chmod(a, 0o644); !errors.Is(err, errCrash) {
		t.Errorf("Chmod after the crash = %v, want errCrash", err)
	}
	if err := c.Chown(a, os.Getuid(), os.Getgid()); !errors.Is(err, errCrash) {
		t.Errorf("Chown after the crash = %v, want errCrash", err)
	}
	if ok, err := c.Exists(a); !errors.Is(err, errCrash) || ok {
		t.Errorf("Exists after the crash = %v, %v; want false and errCrash", ok, err)
	}
	if got := duState(t, dir); got != "a=before:-rw-------" {
		t.Errorf("the directory holds %q after the crash, want only a holding what was written before it", got)
	}
	if c.Calls() != 3 || !c.Crashed() {
		t.Errorf("Calls = %d, Crashed = %v; want 3 and true", c.Calls(), c.Crashed())
	}
}

// TestDuCrashOpsCrashingCreateGivesNoFile: a Create that crashes after it
// ran leaves the empty file on disk and hands the caller nothing.
func TestDuCrashOpsCrashingCreateGivesNoFile(t *testing.T) {
	dir := t.TempDir()
	c := newCrashOps(OSOps(), 1, false)
	f, err := c.Create(filepath.Join(dir, "a"), 0o600)
	if f != nil || !errors.Is(err, errCrash) {
		t.Errorf("Create = %v, %v; want no file and errCrash", f, err)
	}
	if got := duState(t, dir); got != "a=:-rw-------" {
		t.Errorf("the directory holds %q, want the empty file", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w7-tmp/2 && export TMPDIR=/root/.cache/w7-tmp/2 GOTOOLCHAIN=local
df -h /
timeout 300 go test -count=1 ./internal/controller/backup 2>&1 | head -20
```

When `df -h /` shows 90 % or more, run `go clean -cache` first. Expected: FAIL with a build error naming the first undefined identifier (`undefined: OSOps`, `undefined: Ops`, `undefined: newCrashOps`, `undefined: errCrash`, ...).

- [ ] **Step 3: Implement**

1. `internal/controller/backup/durable.go` (package `backup`; doc comment on every exported identifier). Declare `File` and `Ops` as Contracts gives them. `OSOps()` returns a stateless unexported type (name it `osOps`) over the standard library. Return the `os` errors as they are, so `errors.Is(err, fs.ErrNotExist)` and `fs.ErrExist` work on them. Use `//nolint:gosec // G304: ...` with the reason on an `os.OpenFile` or `os.Open` of a caller-supplied path, as `internal/core/secret/key.go` does.
   - `Create(path, perm)`: `os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, perm)`. On error return an untyped `nil` `File` and the error. Returning the `*os.File` result directly would hand the caller a non-nil interface holding a nil pointer, which the tests catch.
   - `Rename(from, to)`: `os.Rename`; it replaces an existing `to`.
   - `Remove(path)`: `os.Remove`; an error that is `fs.ErrNotExist` becomes nil, every other error (a directory that holds files, for example) is returned.
   - `SyncDir(dir)`: open the directory, `Sync` it, close it, return the first error.
   - `Chown(path, uid, gid)`: `os.Lchown`, so a link is changed and not followed. `Chmod(path, perm)`: `os.Chmod`.
   - `Exists(path)`: `os.Lstat`; nil means `true`; `fs.ErrNotExist` means `false, nil`; any other error means `false, err`.
2. `internal/controller/backup/durable_crash_test.go` (package `backup`, test-only). Declare `errCrash = errors.New("backup test: simulated crash")`, `crashOps`, `newCrashOps(inner Ops, at int, before bool) *crashOps`, `Calls()`, `Crashed()` as Contracts gives them, plus `var _ Ops = (*crashOps)(nil)`. Every other identifier of the file carries the prefix `du` (the file wrapper is `duCrashFile`). Behaviour:
   - `crashOps` holds the inner `Ops`, `at`, `before`, a mutex, the call counter, a crashed flag and the list of inner files it created.
   - Every `Ops` method, and every `Write`, `Sync` and `Close` of a `File` that `crashOps.Create` returned, passes the same gate under the mutex. If the crash already happened, the call returns `errCrash` at once without running and without being counted (`Create` returns a nil `File`, `Exists` returns `false`, `Write` returns 0). Otherwise the counter goes up by one. A call whose number is not `at` runs on the inner `Ops` or file and returns its result unchanged (the inner error passes through, as the test of a failing `Rename` shows).
   - The call whose number is `at` sets the crashed flag. With `before` true it does not run, closes every tracked inner file, and returns `errCrash`. With `before` false it runs on the inner `Ops` or file, then closes every tracked inner file and returns `errCrash` whatever the inner call returned (`Write` returns the inner byte count with it; a `Create` that crashed after it ran closes the file it created and returns a nil `File`, leaving the empty file on disk).
   - Closing the tracked files at the crash stands for the operating system closing a dead process's descriptors: data already written stays on disk, nothing is rolled back, and no descriptor leaks. A close error there is ignored.
   - `Calls()` and `Crashed()` read their fields under the mutex.

- [ ] **Step 4: Run test to verify it passes**

```bash
mkdir -p /root/.cache/w7-tmp/2 && export TMPDIR=/root/.cache/w7-tmp/2 GOTOOLCHAIN=local
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 600 go test -count=1 ./internal/controller/backup
timeout 900 go test -race -short -count=1 ./internal/controller/backup
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/2 && go clean -testcache
```

Expected: every test passes (`ok`), `go vet` prints nothing, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output in the report.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/durable.go internal/controller/backup/durable_crash_test.go internal/controller/backup/durable_test.go
git commit -m "feat(backup): add durable ops and crash wrapper"
```

### Task 3: Backup configuration and the controller database fixture

**Files:**
- Modify: `internal/controller/backup/contribute.go`
- Modify: `internal/controller/backup/contribute_test.go`
- Create: `internal/controller/backup/fixture_test.go`

**Interfaces:**
- Consumes: `(*fleet.Authority).IssueToken` (`internal/controller/fleet/tokens.go:31`), which `TokenIssuer` must match; `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrationSet` (`internal/core/store`); `modules.All()` and each module's `Manifest().Migrations[core.SideController]`, as `migrateController` in `internal/controller/server/server.go` builds the module sets; `secret.LoadOrCreateKey`, `secret.NewSealer` (`internal/core/secret`).
- Produces: Contracts "Configuration and contribution" (`TokenIssuer`, `Config`, `Backup`, `New`, `(*Backup).Contribute`, with the package-level `Contribute` and `TestContributeAddsNothingYet` unchanged) and Contracts "Controller database fixture" (`fxDB`, `fxChange`, `fxInsertChange`, `fxStep`, `fxInsertComposite`, `fxInsertLock`, `fxInsertRollout`, `fxInsertListener`, `fxInsertHost`, `fxKey`, `fxNow`). Beyond Contracts: `fxKey` returns the key file's bytes (the `KeyPrefix` line, newline included) as `raw`, which Task 11's `ValidateStaged` takes as its `keyFile` argument; the fixed defaults below.

**Items:** 54, 69

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Write the failing test**

Replace `internal/controller/backup/contribute_test.go` with the code below. `TestContributeAddsNothingYet` stays exactly as it is. The `TestCbFixture*` tests give every fixture helper at least one caller, so the helpers compile and are known to insert rows the schema accepts. The compile-time assertion pins `TokenIssuer` to `*fleet.Authority`'s method.

```go
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The fleet authority is the TokenIssuer the assembly passes.
var _ TokenIssuer = (*fleet.Authority)(nil)

// TestContributeAddsNothingYet: until the backup routes exist the
// contribution mounts no route and starts no service.
func TestContributeAddsNothingYet(t *testing.T) {
	routes, services, err := Contribute(ext.Deps{})
	if err != nil || len(routes) != 0 || len(services) != 0 {
		t.Errorf("Contribute = %v, %v, %v; want nothing", routes, services, err)
	}
}

func TestCbNewKeepsTheConfig(t *testing.T) {
	cfg := Config{KeyFile: "/etc/darkpanel/controller.key", AgentAdvertise: "10.77.0.10:7443"}
	b := New(cfg)
	if b == nil || b.cfg != cfg {
		t.Fatalf("New(%+v) = %+v, want a Backup holding the config", cfg, b)
	}
	if b.running.Load() {
		t.Error("a new Backup reports a backup running")
	}
	if New(Config{}).cfg.Tokens != nil {
		t.Error("an empty Config gained a token issuer")
	}
}

func TestCbBackupContributeAddsNothingYet(t *testing.T) {
	routes, services, err := New(Config{}).Contribute(ext.Deps{})
	if err != nil || len(routes) != 0 || len(services) != 0 {
		t.Errorf("Contribute = %v, %v, %v; want nothing", routes, services, err)
	}
}

func cbScan(t *testing.T, db *store.DB, query string, dest ...any) {
	t.Helper()
	if err := db.Read().QueryRowContext(context.Background(), query).Scan(dest...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func TestCbFixtureDB(t *testing.T) {
	dir := t.TempDir()
	db := fxDB(t, dir)
	if db.Path() != filepath.Join(dir, "controller.db") {
		t.Errorf("database at %s, want controller.db in %s", db.Path(), dir)
	}
	for _, table := range []string{"core_meta", "core_hosts", "core_plans", "core_changes", "core_locks", "core_composites",
		"core_composite_steps", "core_rollouts", "core_rollout_hosts", "core_listeners", "cloudflare_hostnames", "tailnet_keys"} {
		var n int
		cbScan(t, db, `SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = '`+table+`'`, &n)
		if n != 1 {
			t.Errorf("the migrated database has no table %s", table)
		}
	}
	var modulesApplied int
	cbScan(t, db, `SELECT COUNT(DISTINCT module) FROM core_migrations WHERE module IN ('core', 'cloudflare', 'tailnet')`, &modulesApplied)
	if modulesApplied != 3 {
		t.Errorf("core_migrations names %d of core, cloudflare and tailnet, want all 3", modulesApplied)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !slices.Contains([]string{"controller.db", "controller.db-wal", "controller.db-shm"}, e.Name()) {
			t.Errorf("fxDB left %s in the database directory", e.Name())
		}
	}
	if other := fxDB(t, ""); other.Path() == db.Path() {
		t.Error("fxDB with no directory reused a path")
	}
}

func TestCbFixtureInsertChange(t *testing.T) {
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c1", HostID: "h1", Action: "core.test.apply", State: core.StateApplying, Owner: "owner-1"})
	fxInsertChange(t, db, fxChange{ID: "c2", Action: "tailnet.key.create", Class: string(core.ClassIrreversible), State: core.StatePending, ParentID: "c1"})
	want := fxNow.Format("2006-01-02T15:04:05.000000000Z")
	for _, tt := range []struct {
		id, host, action, class, state, owner string
		parent                                sql.NullString
	}{
		{"c1", "h1", "core.test.apply", "compensable", core.StateApplying, "owner-1", sql.NullString{}},
		{"c2", "", "tailnet.key.create", "irreversible", core.StatePending, "", sql.NullString{String: "c1", Valid: true}},
	} {
		var host, action, class, state, owner, actor, input, created, updated, planAction, planHost string
		var version int
		var parent sql.NullString
		cbScan(t, db, `SELECT c.host_id, c.action, c.class, c.state, c.recovery_owner, c.actor, c.input_json, c.version, c.parent_change_id,
				c.created_at, c.updated_at, p.action, p.host_id
			FROM core_changes c JOIN core_plans p ON p.id = c.plan_id WHERE c.id = '`+tt.id+`'`,
			&host, &action, &class, &state, &owner, &actor, &input, &version, &parent, &created, &updated, &planAction, &planHost)
		if host != tt.host || action != tt.action || class != tt.class || state != tt.state || owner != tt.owner || parent != tt.parent {
			t.Errorf("change %s = host %q action %q class %q state %q owner %q parent %v", tt.id, host, action, class, state, owner, parent)
		}
		if actor != "user:fixture" || input != "{}" || version != 1 || created != want || updated != want {
			t.Errorf("change %s defaults = actor %q input %q version %d created %q updated %q; want user:fixture, {}, 1, %s", tt.id, actor, input, version, created, updated, want)
		}
		if planAction != tt.action || planHost != tt.host {
			t.Errorf("change %s has a plan for %q on %q, want %q on %q", tt.id, planAction, planHost, tt.action, tt.host)
		}
	}
}

func TestCbFixtureInsertComposite(t *testing.T) {
	db := fxDB(t, "")
	fxInsertComposite(t, db, "comp1", core.StateApplying, "", []fxStep{
		{ID: "s1", HostID: "h1", State: "applying", ChildPlanID: "p1", ChildChangeID: "cc1"},
		{ID: "s2", Action: "core.x/v1"},
	})
	fxInsertComposite(t, db, "comp2", core.StateUndoing, "unwind", nil)
	for _, tt := range []struct{ id, state, dir string }{{"comp1", core.StateApplying, "forward"}, {"comp2", core.StateUndoing, "unwind"}} {
		var class, state, dir string
		var cursor int
		cbScan(t, db, `SELECT c.class, c.state, k.direction, k.cursor FROM core_changes c JOIN core_composites k ON k.change_id = c.id
			WHERE c.id = '`+tt.id+`'`, &class, &state, &dir, &cursor)
		if class != "composite" || state != tt.state || dir != tt.dir || cursor != 0 {
			t.Errorf("composite %s = class %q state %q direction %q cursor %d", tt.id, class, state, dir, cursor)
		}
	}
	rows, err := db.Read().QueryContext(context.Background(), `SELECT step_id, ordinal, host_id, action, state, child_plan_id, child_change_id
		FROM core_composite_steps WHERE change_id = 'comp1' ORDER BY ordinal`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	type step struct {
		id          string
		ordinal     int
		host        string
		action      string
		state       string
		plan, child sql.NullString
	}
	var got []step
	for rows.Next() {
		var s step
		if err := rows.Scan(&s.id, &s.ordinal, &s.host, &s.action, &s.state, &s.plan, &s.child); err != nil {
			t.Fatal(err)
		}
		got = append(got, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	want := []step{
		{"s1", 0, "h1", "test.step/v1", "applying", sql.NullString{String: "p1", Valid: true}, sql.NullString{String: "cc1", Valid: true}},
		{"s2", 1, "", "core.x/v1", "pending", sql.NullString{}, sql.NullString{}},
	}
	if !slices.Equal(got, want) {
		t.Errorf("steps = %+v, want %+v", got, want)
	}
}

func TestCbFixtureRows(t *testing.T) {
	db := fxDB(t, "")
	fxInsertLock(t, db, "docker:web", "c1")
	var owner string
	cbScan(t, db, `SELECT owner FROM core_locks WHERE key = 'docker:web'`, &owner)
	if owner != "c1" {
		t.Errorf("lock owner = %q, want c1", owner)
	}

	fxInsertRollout(t, db, "r1", "running", "hostA", "hostB")
	fxInsertRollout(t, db, "r2", "succeeded")
	var state string
	var outcome sql.NullString
	cbScan(t, db, `SELECT state, outcome FROM core_rollouts WHERE id = 'r1'`, &state, &outcome)
	if state != "running" || outcome.Valid {
		t.Errorf("rollout r1 = %q, outcome %v; want running with no outcome", state, outcome)
	}
	var hosts string
	cbScan(t, db, `SELECT group_concat(host_id || ':' || position || ':' || state, ',') FROM
		(SELECT * FROM core_rollout_hosts WHERE rollout_id = 'r1' ORDER BY position)`, &hosts)
	if hosts != "hostA:0:waiting,hostB:1:waiting" {
		t.Errorf("rollout hosts = %q", hosts)
	}
	var n int
	cbScan(t, db, `SELECT COUNT(*) FROM core_rollout_hosts WHERE rollout_id = 'r2'`, &n)
	if n != 0 {
		t.Errorf("rollout r2 has %d host rows, want none", n)
	}
	draining := fxDB(t, "")
	fxInsertRollout(t, draining, "r3", "draining", "hostA")
	cbScan(t, draining, `SELECT state, outcome FROM core_rollouts WHERE id = 'r3'`, &state, &outcome)
	if state != "draining" || outcome.String != "failed" {
		t.Errorf("rollout r3 = %q, outcome %v; want draining with outcome failed", state, outcome)
	}

	fxInsertListener(t, db, "l1", "open")
	fxInsertListener(t, db, "l2", "closed")
	cbScan(t, db, `SELECT group_concat(id || ':' || state, ',') FROM (SELECT * FROM core_listeners ORDER BY id)`, &hosts)
	if hosts != "l1:open,l2:closed" {
		t.Errorf("listeners = %q", hosts)
	}

	fxInsertHost(t, db, "h-local", "enrolled", true)
	fxInsertHost(t, db, "h-agent", "pending", false)
	cbScan(t, db, `SELECT group_concat(id || ':' || state || ':' || is_local, ',') FROM (SELECT * FROM core_hosts ORDER BY id)`, &hosts)
	if hosts != "h-agent:pending:0,h-local:enrolled:1" {
		t.Errorf("hosts = %q", hosts)
	}
}

func TestCbFixtureKey(t *testing.T) {
	dir := t.TempDir()
	path, raw, sealer := fxKey(t, dir)
	if path != filepath.Join(dir, "controller.key") {
		t.Errorf("key at %s, want controller.key in %s", path, dir)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("key file: %v, %v; want mode 0600", st, err)
	}
	if onDisk, err := os.ReadFile(path); err != nil || !bytes.Equal(onDisk, raw) { //nolint:gosec // G304: a path under t.TempDir()
		t.Errorf("raw differs from the file's bytes: %v", err)
	}
	if !bytes.HasPrefix(raw, []byte(secret.KeyPrefix)) || !bytes.HasSuffix(raw, []byte("\n")) {
		t.Errorf("raw = %q, want a key file line", raw)
	}
	key, err := secret.LoadOrCreateKey(path)
	if err != nil {
		t.Fatal(err)
	}
	same, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal("ad", []byte("plain"))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := same.Open("ad", sealed); err != nil || string(got) != "plain" {
		t.Errorf("a sealer over the key file opens the fixture sealer's value as %q, %v", got, err)
	}
	if _, raw2, _ := fxKey(t, t.TempDir()); bytes.Equal(raw, raw2) {
		t.Error("two fixture keys are equal")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w7-tmp/3 && export TMPDIR=/root/.cache/w7-tmp/3 GOTOOLCHAIN=local
df -h /
timeout 300 go test -count=1 ./internal/controller/backup 2>&1 | head -20
```

When `df -h /` shows 90 % or more, run `go clean -cache` first. Expected: FAIL with a build error naming the first undefined identifier (`undefined: TokenIssuer`, `undefined: Config`, `undefined: New`, `undefined: fxDB`, ...).

- [ ] **Step 3: Implement**

1. `internal/controller/backup/contribute.go`: add `TokenIssuer`, `Config`, `Backup` and `New(cfg Config) *Backup` exactly as Contracts "Configuration and contribution" gives them (new imports `context`, `database/sql`, `sync/atomic`, `time`), each with a doc comment, and `func (b *Backup) Contribute(_ ext.Deps) ([]ext.Route, []ext.Service, error)` returning `nil, nil, nil`. `New` stores `cfg` and leaves `running` false. Keep the package-level `Contribute` and the package comment unchanged. The tests read `cfg` and `running`, so neither field needs a `//nolint:unused`.
2. `internal/controller/backup/fixture_test.go` (package `backup`, test-only; no test function in this file). Declare the helpers with the signatures of Contracts "Controller database fixture". Every helper calls `t.Helper()` and fails the test with `t.Fatal` on an error. Each is used by a `TestCbFixture*` test above, so none carries the `//nolint:unused` directive that Contracts mentions for a helper nobody uses yet. Time columns hold `fxNow` formatted `2006-01-02T15:04:05.000000000Z` (the lifecycle's format). Write rows through `db.Write` (the writer connection), one statement per call.
   - `fxNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)`.
   - `fxDB(t, dir)`: `dir` empty means `t.TempDir()`. Open `<dir>/controller.db` with `store.Open`, close it in `t.Cleanup`. Migrate it with `store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideController, BinaryVersion: "test", BackupRoot: t.TempDir(), Keep: 5, Now: fxNow}, coreSet, moduleSets)` where `coreSet` is `store.CoreMigrations(store.SideController)` and `moduleSets` has one `store.MigrationSet{Module: man.ID, FS: fsys}` per module of `modules.All()` that has a controller migration FS. The backup root is a directory of its own, so `dir` holds nothing but the database files. Fail the test when `Migrate` returns an error or `Result.Failed` is not empty.
   - `fxInsertChange`: an empty `Class` becomes `string(core.ClassCompensable)`. Insert a `core_plans` row (`id` = `"plan-"` + change `ID`, `host_id` and `action` from the change, `version` 1, `actor` `user:fixture`, `input_hash`, `state_hash` and `confirmation` empty, `diff_json` `{}`, `guards_json` `[]`, `created_at` and `expires_at` `fxNow`), then a `core_changes` row (`plan_id` that plan, `version` 1, `compensated_by` empty, `actor` `user:fixture`, `input_json` `{}`, `recovery_owner` = `Owner`, `parent_change_id` = `ParentID` or NULL when empty, `created_at` and `updated_at` `fxNow`; `ID`, `HostID`, `Action`, `Class`, `State` as given, empty strings stored as empty).
   - `fxInsertComposite`: an empty `dir` means `forward`. Insert the composite's change through `fxInsertChange` (`Action` `core.fixture.composite`, class `composite`, the given `state`), a `core_composites` row (`direction`, `cursor` 0, `steps_deadline` `fxNow`) and one `core_composite_steps` row per step with `ordinal` = its index, `input_template` `{}`, an empty step `Action` becoming `test.step/v1`, an empty step `State` becoming `pending`, and `child_plan_id` and `child_change_id` NULL when empty.
   - `fxInsertLock`: one `core_locks` row. `fxInsertRollout`: a `core_rollouts` row (`version` `1.0.0`, `channel` `stable`, `outcome` `failed` when `state` is `draining` and NULL otherwise, times `fxNow`) and one `core_rollout_hosts` row per host (`position` = its index, `state` `waiting`); the state is passed through unchanged, so a state the schema's check refuses fails the test (a rollout state `quarantined` is accepted only once Task 7's migration exists). `fxInsertListener`: a `core_listeners` row (`address` `127.0.0.1`, `port` 8443, `allowed_peer` `100.64.0.1`, `client_header` `X-Darkpanel-Client`, `public_host` `fixture.example.com`, `change_id` empty, the given `state`, times `fxNow`). `fxInsertHost`: a `core_hosts` row (`name` = `"host-"` + `id`, `is_local` 1 or 0, the given `state`, `created_at` `fxNow`).
   - `fxKey(t, dir)`: `secret.LoadOrCreateKey(<dir>/controller.key)` creates the file; `raw` is the file's bytes read back; `sealer` is `secret.NewSealer` over the 32 key bytes it returned.

- [ ] **Step 4: Run test to verify it passes**

```bash
mkdir -p /root/.cache/w7-tmp/3 && export TMPDIR=/root/.cache/w7-tmp/3 GOTOOLCHAIN=local
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 600 go test -count=1 ./internal/controller/backup
timeout 900 go test -race -short -count=1 ./internal/controller/backup
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/3 && go clean -testcache
```

Expected: every test passes (`ok`), `go vet` prints nothing, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output in the report.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/contribute.go internal/controller/backup/contribute_test.go internal/controller/backup/fixture_test.go
git commit -m "feat(backup): add backup config and test fixtures"
```

### Task 4: Restore journal

**Files:**
- Create: `internal/controller/backup/journal.go`
- Test: `internal/controller/backup/journal_test.go`

**Interfaces:**
- Consumes: `Ops`, `File` and `OSOps()` (Contracts "Durable file operations", Task 2, `durable.go`); in tests `errCrash`, `newCrashOps` and `(*crashOps).Crashed` (Task 2, `durable_crash_test.go`); `paths.Paths` (`internal/paths`).
- Produces: every identifier of Contracts "Journal": `Phase` and its nine constants, `RenameIntended`, `RenameDone`, `JournalFile`, `Rename`, `Journal`, `Targets`, `StagedName`, `AsideName`, `JournalPath`, `SaveJournal`, `ReadJournal`, `ListJournals`, `RenameJournaled`, `Settle` and `ErrAmbiguousRename`. Behaviour the Contract leaves open and this task fixes, which Tasks 14 and 15 rely on: `SaveJournal` writes `Format` 1 whatever `j.Format` holds (the caller's value is not changed) and refuses an unknown or empty phase without touching the disk; `ListJournals` matches the names `restore-*.journal`, returns full paths, and answers an empty list and no error for a directory that does not exist; `RenameJournaled` does not change `j.Phase`, and when a step fails the entry stays in `j` and on disk as it was last saved, because recovery settles an `intended` entry from the names that exist.

**Items:** 54

**Depends on:** Task 2
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/journal_test.go` with exactly this content. The crash tests iterate the call number of `newCrashOps` from 1 until no crash fires, so they cover every call the implementation makes whatever their count.

```go
package backup

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/paths"
)

const jnTx = "01JTXAAAAAAAAAAAAAAAAAAAAA"

// jnWrite creates path with content, and its directory when missing.
func jnWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// jnExists reports whether path exists, without following a symbolic link.
func jnExists(t *testing.T, path string) bool {
	t.Helper()
	ok, err := OSOps().Exists(path)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

// jnRecorder logs the calls of the Ops and Files that order a journal write,
// with paths relative to root, and then runs them on the wrapped Ops.
type jnRecorder struct {
	Ops
	root string
	log  []string
}

func (r *jnRecorder) note(format string, path string, more ...any) {
	rel, err := filepath.Rel(r.root, path)
	if err != nil {
		rel = path
	}
	r.log = append(r.log, fmt.Sprintf(format, append([]any{rel}, more...)...))
}

func (r *jnRecorder) Create(path string, perm fs.FileMode) (File, error) {
	r.note("create %s %04o", path, perm)
	f, err := r.Ops.Create(path, perm)
	if err != nil {
		return nil, err
	}
	return &jnRecFile{File: f, r: r, path: path}, nil
}

func (r *jnRecorder) Rename(from, to string) error {
	rel, err := filepath.Rel(r.root, to)
	if err != nil {
		rel = to
	}
	r.note("rename %s %s", from, rel)
	return r.Ops.Rename(from, to)
}

func (r *jnRecorder) Remove(path string) error {
	r.note("remove %s", path)
	return r.Ops.Remove(path)
}

func (r *jnRecorder) SyncDir(dir string) error {
	r.note("syncdir %s", dir)
	return r.Ops.SyncDir(dir)
}

type jnRecFile struct {
	File
	r    *jnRecorder
	path string
}

func (f *jnRecFile) Sync() error {
	f.r.note("sync %s", f.path)
	return f.File.Sync()
}

func (f *jnRecFile) Close() error {
	f.r.note("close %s", f.path)
	return f.File.Close()
}

// jnSaveSteps is what one SaveJournal of the journal at rel does, in order.
func jnSaveSteps(rel string) []string {
	return []string{
		"remove " + rel + ".new",
		"create " + rel + ".new 0600",
		"sync " + rel + ".new",
		"close " + rel + ".new",
		"rename " + rel + ".new " + rel,
		"syncdir " + filepath.Dir(rel),
	}
}

func jnSample() Journal {
	return Journal{
		Format: 1, Tx: jnTx, Phase: PhaseMovedAside,
		Files: []JournalFile{
			{Path: "/var/lib/darkpanel/controller/controller.db", Existed: true},
			{Path: "/var/lib/darkpanel/controller/controller.db-wal", Existed: false},
			{Path: "/var/lib/darkpanel/controller/controller.db-shm", Existed: false},
			{Path: "/etc/darkpanel/controller.key", Existed: true},
		},
		Renames: []Rename{
			{Phase: PhaseMovedAside, From: "/a/controller.db", To: "/a/controller.db.pre-" + jnTx, State: RenameDone},
			{Phase: PhaseMovedAside, From: "/k/controller.key", To: "/k/controller.key.pre-" + jnTx, State: RenameIntended},
		},
	}
}

func TestJnNames(t *testing.T) {
	p := paths.Under("/r")
	targets := Targets(p)
	want := []string{"/r/controller/controller.db", "/r/controller/controller.db-wal", "/r/controller/controller.db-shm",
		"/r/keys/controller.key"}
	if !slices.Equal(targets, want) {
		t.Fatalf("Targets = %v, want %v", targets, want)
	}
	for _, f := range targets {
		if got, want := StagedName(f, jnTx), f+".restore-"+jnTx; got != want {
			t.Errorf("StagedName(%s) = %s, want %s", f, got, want)
		}
		if got, want := AsideName(f, jnTx), f+".pre-"+jnTx; got != want {
			t.Errorf("AsideName(%s) = %s, want %s", f, got, want)
		}
	}
	if got, want := JournalPath("/r/controller", jnTx), "/r/controller/restore-"+jnTx+".journal"; got != want {
		t.Errorf("JournalPath = %s, want %s", got, want)
	}
}

// TestJnWireValues pins the strings a journal stores, which a later binary
// must read.
func TestJnWireValues(t *testing.T) {
	phases := map[Phase]string{
		PhaseBegun: "begun", PhaseStaged: "staged", PhaseValidated: "validated", PhaseQuarantined: "quarantined",
		PhaseJournaled: "journaled", PhaseMovedAside: "moved-aside", PhaseInstalled: "installed",
		PhaseVerified: "verified", PhaseRollback: "rollback",
	}
	dir := t.TempDir()
	for phase, text := range phases {
		if string(phase) != text {
			t.Errorf("phase %q, want %q", phase, text)
		}
		path := filepath.Join(dir, "restore-"+text+".journal")
		jnWrite(t, path, `{"format":1,"tx":"T","phase":"`+text+`"}`)
		if j, err := ReadJournal(path); err != nil || j.Phase != phase {
			t.Errorf("ReadJournal with phase %s = %+v, %v", text, j, err)
		}
	}
	if RenameIntended != "intended" || RenameDone != "done" {
		t.Errorf("rename states %q and %q, want intended and done", RenameIntended, RenameDone)
	}
}

func TestJnSaveAndReadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	j := jnSample()
	if err := SaveJournal(OSOps(), path, j); err != nil {
		t.Fatal(err)
	}
	got, err := ReadJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, j) {
		t.Errorf("ReadJournal = %+v, want %+v", got, j)
	}
	if st, err := os.Stat(path); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("journal mode = %v, %v; want 0600", st, err)
	}
	names, err := os.ReadDir(dir)
	if err != nil || len(names) != 1 {
		t.Errorf("directory holds %v, %v; want only the journal", names, err)
	}

	j.Phase = PhaseInstalled
	j.Format = 0
	if err := SaveJournal(OSOps(), path, j); err != nil {
		t.Fatal(err)
	}
	got, err = ReadJournal(path)
	if err != nil || got.Phase != PhaseInstalled || got.Format != 1 {
		t.Errorf("after the second save ReadJournal = %+v, %v; want phase installed and format 1", got, err)
	}
}

func TestJnSaveRefusesAnUnknownPhase(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	if err := SaveJournal(OSOps(), path, Journal{Format: 1, Tx: jnTx, Phase: "bogus"}); err == nil {
		t.Fatal("SaveJournal accepted an unknown phase")
	}
	names, _ := os.ReadDir(dir)
	if len(names) != 0 {
		t.Errorf("directory holds %v after a refused save, want nothing", names)
	}
}

func TestJnSaveReplacesAStaleNewFile(t *testing.T) {
	dir := t.TempDir()
	path := JournalPath(dir, jnTx)
	jnWrite(t, path+".new", "half a journal")
	if err := SaveJournal(OSOps(), path, jnSample()); err != nil {
		t.Fatalf("SaveJournal beside a stale .new file: %v", err)
	}
	if _, err := ReadJournal(path); err != nil {
		t.Errorf("ReadJournal: %v", err)
	}
	if jnExists(t, path+".new") {
		t.Error("the .new file is still there")
	}
}

func TestJnSaveOrdersTheDurabilitySteps(t *testing.T) {
	dir := t.TempDir()
	rec := &jnRecorder{Ops: OSOps(), root: dir}
	if err := SaveJournal(rec, JournalPath(dir, jnTx), jnSample()); err != nil {
		t.Fatal(err)
	}
	if want := jnSaveSteps("restore-" + jnTx + ".journal"); !slices.Equal(rec.log, want) {
		t.Errorf("SaveJournal did\n%s\nwant\n%s", strings.Join(rec.log, "\n"), strings.Join(want, "\n"))
	}
}

func TestJnReadRefusals(t *testing.T) {
	tests := []struct {
		name, content string
		ok            bool
	}{
		{"minimal", `{"format":1,"tx":"T","phase":"begun"}`, true},
		{"format 2", `{"format":2,"tx":"T","phase":"begun"}`, false},
		{"format 0", `{"tx":"T","phase":"begun"}`, false},
		{"unknown phase", `{"format":1,"tx":"T","phase":"bogus"}`, false},
		{"empty phase", `{"format":1,"tx":"T"}`, false},
		{"not json", `restore in progress`, false},
		{"empty file", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "restore-T.journal")
			jnWrite(t, path, tt.content)
			j, err := ReadJournal(path)
			if tt.ok != (err == nil) {
				t.Fatalf("ReadJournal = %+v, %v; want ok %v", j, err, tt.ok)
			}
		})
	}
	_, err := ReadJournal(filepath.Join(t.TempDir(), "restore-missing.journal"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadJournal of a missing file = %v, want an error wrapping fs.ErrNotExist", err)
	}
}

// TestJnSaveSurvivesACrashAtEveryCall: whichever call of SaveJournal fails
// and on whichever side, the journal on disk is the old one or the new one,
// never a torn file, and a later save succeeds.
func TestJnSaveSurvivesACrashAtEveryCall(t *testing.T) {
	oldJ := jnSample()
	oldJ.Phase = PhaseJournaled
	oldJ.Renames = nil
	newJ := jnSample()
	for _, before := range []bool{true, false} {
		for at := 1; ; at++ {
			if at > 40 {
				t.Fatalf("before=%v: SaveJournal still crashes at call 40", before)
			}
			dir := t.TempDir()
			path := JournalPath(dir, jnTx)
			if err := SaveJournal(OSOps(), path, oldJ); err != nil {
				t.Fatal(err)
			}
			c := newCrashOps(OSOps(), at, before)
			err := SaveJournal(c, path, newJ)
			if !c.Crashed() {
				if err != nil {
					t.Fatalf("before=%v at=%d: no crash fired but SaveJournal = %v", before, at, err)
				}
				if at < 6 {
					t.Fatalf("SaveJournal made only %d calls, want at least its 6 durability steps", at-1)
				}
				break
			}
			if !errors.Is(err, errCrash) {
				t.Fatalf("before=%v at=%d: SaveJournal = %v, want the simulated crash", before, at, err)
			}
			got, rerr := ReadJournal(path)
			if rerr != nil {
				t.Fatalf("before=%v at=%d: the journal is unreadable after the crash: %v", before, at, rerr)
			}
			if !reflect.DeepEqual(got, oldJ) && !reflect.DeepEqual(got, newJ) {
				t.Fatalf("before=%v at=%d: journal %+v is neither the old nor the new one", before, at, got)
			}
			if err := SaveJournal(OSOps(), path, newJ); err != nil {
				t.Fatalf("before=%v at=%d: the save after the crash: %v", before, at, err)
			}
			if got, err := ReadJournal(path); err != nil || !reflect.DeepEqual(got, newJ) {
				t.Fatalf("before=%v at=%d: after the second save ReadJournal = %+v, %v", before, at, got, err)
			}
		}
	}
}

func TestJnListJournals(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{
		"restore-01B.journal", "restore-01A.journal", "restore-01A.journal.new", "restore-01C.journal.bak",
		"controller.db", "journal", "restore.journal.d",
	} {
		jnWrite(t, filepath.Join(dir, name), "x")
	}
	got, err := ListJournals(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{filepath.Join(dir, "restore-01A.journal"), filepath.Join(dir, "restore-01B.journal")}
	if !slices.Equal(got, want) {
		t.Errorf("ListJournals = %v, want %v", got, want)
	}
	if got, err := ListJournals(filepath.Join(dir, "missing")); err != nil || len(got) != 0 {
		t.Errorf("ListJournals of a missing directory = %v, %v; want nothing and no error", got, err)
	}
}

func TestJnRenameJournaledOrdersIntentRenameAndDone(t *testing.T) {
	root := t.TempDir()
	journal := JournalPath(filepath.Join(root, "j"), jnTx)
	from, to := filepath.Join(root, "a", "controller.db"), filepath.Join(root, "b", "controller.db.pre-"+jnTx)
	jnWrite(t, from, "database")
	if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
		t.Fatal(err)
	}
	j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
	if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := SaveJournal(OSOps(), journal, j); err != nil {
		t.Fatal(err)
	}

	rec := &jnRecorder{Ops: OSOps(), root: root}
	if err := RenameJournaled(rec, journal, &j, PhaseMovedAside, from, to); err != nil {
		t.Fatal(err)
	}
	rel := filepath.Join("j", "restore-"+jnTx+".journal")
	want := slices.Concat(jnSaveSteps(rel), []string{"rename a/controller.db b/controller.db.pre-" + jnTx, "syncdir b"}, jnSaveSteps(rel))
	if !slices.Equal(rec.log, want) {
		t.Errorf("RenameJournaled did\n%s\nwant\n%s", strings.Join(rec.log, "\n"), strings.Join(want, "\n"))
	}
	if jnExists(t, from) || !jnExists(t, to) {
		t.Errorf("after the rename %s exists %v, %s exists %v; want moved", from, jnExists(t, from), to, jnExists(t, to))
	}
	wantRenames := []Rename{{Phase: PhaseMovedAside, From: from, To: to, State: RenameDone}}
	if !reflect.DeepEqual(j.Renames, wantRenames) {
		t.Errorf("j.Renames = %+v, want %+v", j.Renames, wantRenames)
	}
	if got, err := ReadJournal(journal); err != nil || !reflect.DeepEqual(got.Renames, wantRenames) {
		t.Errorf("saved renames = %+v, %v; want %+v", got.Renames, err, wantRenames)
	}

	second := filepath.Join(root, "a", "controller.key")
	jnWrite(t, second, "key")
	if err := RenameJournaled(OSOps(), journal, &j, PhaseInstalled, second, filepath.Join(root, "b", "controller.key")); err != nil {
		t.Fatal(err)
	}
	if got, err := ReadJournal(journal); err != nil || len(got.Renames) != 2 || got.Renames[1].Phase != PhaseInstalled ||
		got.Renames[1].State != RenameDone {
		t.Errorf("saved renames after the second = %+v, %v; want two, the second installed and done", got.Renames, err)
	}
}

// TestJnRenameJournaledKeepsTheIntentWhenTheRenameFails: a rename that
// fails leaves the journal naming it as intended, which is what recovery
// settles from the names that exist.
func TestJnRenameJournaledKeepsTheIntentWhenTheRenameFails(t *testing.T) {
	dir := t.TempDir()
	journal := JournalPath(dir, jnTx)
	j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
	if err := SaveJournal(OSOps(), journal, j); err != nil {
		t.Fatal(err)
	}
	from, to := filepath.Join(dir, "missing"), filepath.Join(dir, "target")
	if err := RenameJournaled(OSOps(), journal, &j, PhaseMovedAside, from, to); err == nil {
		t.Fatal("RenameJournaled of a missing file succeeded")
	}
	want := []Rename{{Phase: PhaseMovedAside, From: from, To: to, State: RenameIntended}}
	if got, err := ReadJournal(journal); err != nil || !reflect.DeepEqual(got.Renames, want) {
		t.Errorf("saved renames = %+v, %v; want %+v", got.Renames, err, want)
	}
}

// TestJnRenameJournaledSurvivesACrashAtEveryCall: after a crash at any call,
// on either side, the journal reads, exactly one of the two names exists, a
// rename that happened is in the journal, and an intended entry settles to
// what happened.
func TestJnRenameJournaledSurvivesACrashAtEveryCall(t *testing.T) {
	for _, before := range []bool{true, false} {
		for at := 1; ; at++ {
			if at > 60 {
				t.Fatalf("before=%v: RenameJournaled still crashes at call 60", before)
			}
			root := t.TempDir()
			journal := JournalPath(filepath.Join(root, "j"), jnTx)
			from, to := filepath.Join(root, "a", "controller.db"), filepath.Join(root, "b", "controller.db.pre-"+jnTx)
			jnWrite(t, from, "database")
			if err := os.MkdirAll(filepath.Dir(to), 0o700); err != nil {
				t.Fatal(err)
			}
			j := Journal{Format: 1, Tx: jnTx, Phase: PhaseMovedAside}
			if err := os.MkdirAll(filepath.Dir(journal), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := SaveJournal(OSOps(), journal, j); err != nil {
				t.Fatal(err)
			}
			c := newCrashOps(OSOps(), at, before)
			err := RenameJournaled(c, journal, &j, PhaseMovedAside, from, to)
			if !c.Crashed() {
				if err != nil {
					t.Fatalf("before=%v at=%d: no crash fired but RenameJournaled = %v", before, at, err)
				}
				break
			}
			if !errors.Is(err, errCrash) {
				t.Fatalf("before=%v at=%d: RenameJournaled = %v, want the simulated crash", before, at, err)
			}
			got, rerr := ReadJournal(journal)
			if rerr != nil {
				t.Fatalf("before=%v at=%d: the journal is unreadable: %v", before, at, rerr)
			}
			fromHere, toHere := jnExists(t, from), jnExists(t, to)
			if fromHere == toHere {
				t.Fatalf("before=%v at=%d: from exists %v and to exists %v; want exactly one", before, at, fromHere, toHere)
			}
			if toHere && len(got.Renames) != 1 {
				t.Fatalf("before=%v at=%d: the rename ran but the journal holds %+v", before, at, got.Renames)
			}
			if len(got.Renames) == 0 {
				continue
			}
			entry := got.Renames[0]
			if entry.Phase != PhaseMovedAside || entry.From != from || entry.To != to {
				t.Fatalf("before=%v at=%d: journal entry %+v does not name the rename", before, at, entry)
			}
			switch entry.State {
			case RenameDone:
				if !toHere {
					t.Fatalf("before=%v at=%d: the entry is done but the rename did not run", before, at)
				}
			case RenameIntended:
				done, serr := Settle(OSOps(), entry)
				if serr != nil || done != toHere {
					t.Fatalf("before=%v at=%d: Settle = %v, %v; want %v", before, at, done, serr, toHere)
				}
			default:
				t.Fatalf("before=%v at=%d: entry state %q", before, at, entry.State)
			}
		}
	}
}

func TestJnSettle(t *testing.T) {
	tests := []struct {
		name         string
		from, to     bool
		want, wantOK bool
	}{
		{"only from: not done", true, false, false, true},
		{"only to: done", false, true, true, true},
		{"both: ambiguous", true, true, false, false},
		{"neither: ambiguous", false, false, false, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			r := Rename{Phase: PhaseInstalled, From: filepath.Join(dir, "from"), To: filepath.Join(dir, "to"), State: RenameIntended}
			if tt.from {
				jnWrite(t, r.From, "x")
			}
			if tt.to {
				jnWrite(t, r.To, "x")
			}
			done, err := Settle(OSOps(), r)
			if tt.wantOK {
				if err != nil || done != tt.want {
					t.Fatalf("Settle = %v, %v; want %v", done, err, tt.want)
				}
				return
			}
			if !errors.Is(err, ErrAmbiguousRename) {
				t.Fatalf("Settle = %v, %v; want ErrAmbiguousRename", done, err)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/4 && TMPDIR=/root/.cache/w7-tmp/4 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJn' ./internal/controller/backup/`
Expected: FAIL with a build error naming undefined identifiers, for example `undefined: Journal`, `Phase`, `Targets`, `SaveJournal`. Quote the output.

- [ ] **Step 3: Implement**

Create `internal/controller/backup/journal.go`, package `backup`, with a doc comment on every exported identifier, error strings starting `backup:`, and gofmt formatting.

1. Declare the identifiers of Contracts "Journal" exactly as written there: `Phase` and the nine constants with their string values, `RenameIntended` and `RenameDone`, and the `JournalFile`, `Rename` and `Journal` structs with the given JSON tags (`files` and `renames` keep `omitempty`). Keep the nine phases in one unexported slice, which `SaveJournal` and `ReadJournal` both use to decide whether a phase is known.
2. `Targets(p)` returns `p.ControllerDB()`, `p.ControllerDB()+"-wal"`, `p.ControllerDB()+"-shm"` and `p.ControllerKey`, in that order. `StagedName(file, tx)` is `file + ".restore-" + tx`; `AsideName(file, tx)` is `file + ".pre-" + tx`; `JournalPath(dir, tx)` joins `dir` and `"restore-" + tx + ".journal"`.
3. `SaveJournal(ops, path, j)`. First refuse a phase that is not in the known slice with an error that names it. Then set `Format` to 1 on the local copy `j` (the parameter is a value), encode it with `encoding/json`, and perform exactly these calls on `ops`, in this order: `Remove(path+".new")`, `Create(path+".new", 0o600)`, one `Write` of the whole encoding, `Sync`, `Close`, `Rename(path+".new", path)`, `SyncDir(filepath.Dir(path))`. A failure in any call returns a wrapped error at once (a file that was created is closed best-effort first). Nothing else writes to the disk: `TestJnSaveOrdersTheDurabilitySteps` pins the call sequence.
4. `ReadJournal(path)` reads the whole file with `os.ReadFile`, which trips gosec G304 on a variable path: annotate it `//nolint:gosec // G304: the path is a journal in the controller directory`. It decodes with `json.Unmarshal` and returns an error for a decode failure (an empty file included), for a `Format` other than 1 and for a phase outside the known slice (an empty phase included). A read error wraps the `os` error with `%w`, so `errors.Is(err, fs.ErrNotExist)` holds for a missing file.
5. `ListJournals(dir)` reads `dir` with `os.ReadDir`; a `fs.ErrNotExist` from it returns `nil, nil`, any other error is returned wrapped. It keeps the entries whose names match `restore-*.journal` (`filepath.Match`), so `restore-<tx>.journal.new`, `restore-<tx>.journal.bak` and unrelated names are ignored, and returns `filepath.Join(dir, name)` for each, sorted ascending by the text between the `restore-` prefix and the `.journal` suffix.
6. `RenameJournaled(ops, journalPath, j, phase, from, to)`: append `Rename{Phase: phase, From: from, To: to, State: RenameIntended}` to `j.Renames`; `SaveJournal(ops, journalPath, *j)`; `ops.Rename(from, to)`; `ops.SyncDir(filepath.Dir(to))`; set the appended entry's state to `RenameDone`; `SaveJournal` again. Return a wrapped error at the first failing call, leaving the entry in `j` as it is.
7. `Settle(ops, r)` asks `ops.Exists` for `r.From` and `r.To`. From present and to absent returns `false, nil`; to present and from absent returns `true, nil`; both present or both absent returns `false` and an error that wraps `ErrAmbiguousRename` with `%w` and names both paths. An `Exists` error is returned wrapped. `ErrAmbiguousRename` is `errors.New("backup: a journaled rename cannot be settled")`.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w7-tmp/4 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJn' ./internal/controller/backup/`
Expected: PASS. Quote the output.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w7-tmp/4 GOTOOLCHAIN=local
mkdir -p "$TMPDIR"
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf "$TMPDIR" && go clean -testcache
```

Expected: `go build`, `go vet` and `go mod tidy -diff` print nothing; the race run prints `ok` for `internal/controller/backup`; `golangci-lint` prints `0 issues.`; the full suite prints only `ok` or `?` lines. Record the real output of each command in the report. The `df -h /` line of Global Constraints applies before the first build.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/backup/journal.go internal/controller/backup/journal_test.go
git commit -m "feat(backup): add the restore journal"
```

### Task 5: Backup call site and the purge ruling

**Files:**
- Modify: `internal/controller/server/extensions.go` (the var block of interface assertions, the `controllerFunc` and `contributions` doc comments, `contributions()`, and a new method `backupConfig`)
- Modify (test): `internal/controller/server/extensions_test.go`, `internal/controller/server/services_test.go`
- Read, do not edit: `internal/controller/server/server.go` (`Options.Paths`, `Options.AgentAdvertise`, the startup order `openStorage`, `startServices`, `startListeners`) and `internal/controller/server/services.go` (`startServices` sets `s.authority`; `purge` and `startPurges`)

**Interfaces:**
- Consumes: `backup.New`, `backup.Config`, `backup.TokenIssuer` and `(*backup.Backup).Contribute` (Contracts "Configuration and contribution", Task 3); `fleet.Authority.IssueToken` (`internal/controller/fleet/tokens.go`), which already has the `TokenIssuer` signature; `Options.Paths.ControllerKey` and `Options.AgentAdvertise`.
- Produces: the unexported `(*Server).backupConfig() backup.Config`, and the backup contributor of Contracts "Server call site". `ext.Deps` and every other contributor are unchanged. The package-level `backup.Contribute` still exists and is still tested; Task 16 deletes it.

**Items:** 54, 229, 232

**Depends on:** Task 3
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

This is an Integrator task (plan rulings W7-R2 and W7-R12): it edits `extensions.go` and the two test files above and nothing else in `internal/controller/server`. `startServices` assigns `s.authority` before `startListeners` calls `contributions()`, so a started server always has an authority; only a bare `&Server{...}` built by a unit test has none.

- [ ] **Step 1: Write the failing test**

Append to `internal/controller/server/extensions_test.go`, and add `"github.com/darkraise/darkpanel/internal/controller/fleet"` to its import block (before `.../core/ext`):

```go
// TestXbBackupConfigCarriesTheServersValues: the backup contribution gets the
// controller key file, the agent address and the fleet authority as its
// token issuer.
func TestXbBackupConfigCarriesTheServersValues(t *testing.T) {
	opts := testOptions(t)
	opts.AgentAdvertise = "controller.example.test:7443"
	s := start(t, opts)
	cfg := s.backupConfig()
	if cfg.KeyFile != opts.Paths.ControllerKey {
		t.Errorf("KeyFile = %q, want %q", cfg.KeyFile, opts.Paths.ControllerKey)
	}
	if cfg.AgentAdvertise != opts.AgentAdvertise {
		t.Errorf("AgentAdvertise = %q, want %q", cfg.AgentAdvertise, opts.AgentAdvertise)
	}
	if got, ok := cfg.Tokens.(*fleet.Authority); !ok || got == nil || got != s.authority {
		t.Errorf("Tokens = %T %v, want the server's fleet authority", cfg.Tokens, cfg.Tokens)
	}
}

// TestXbBackupConfigWithoutAuthorityHasNoIssuer: before the fleet authority
// exists the config holds a nil interface, not a typed nil pointer, so the
// backup package's nil check refuses enrollment tokens.
func TestXbBackupConfigWithoutAuthorityHasNoIssuer(t *testing.T) {
	s := &Server{opts: testOptions(t)}
	cfg := s.backupConfig()
	if cfg.Tokens != nil {
		t.Errorf("Tokens = %T %v, want a nil interface", cfg.Tokens, cfg.Tokens)
	}
	if cfg.KeyFile != s.opts.Paths.ControllerKey {
		t.Errorf("KeyFile = %q, want %q", cfg.KeyFile, s.opts.Paths.ControllerKey)
	}
}
```

Append to `internal/controller/server/services_test.go`, and add `"github.com/darkraise/darkpanel/internal/controller/quarantine"` to its import block (after `.../controller/listen`):

```go
// TestXbPurgeInQuarantineKeepsReferencedPlans: the purge is not one of the
// sources restore quarantine suspends, so an expired plan that no change
// references is purged while the controller is quarantined, and the expired
// plan of a quarantined change stays, because the change references it.
func TestXbPurgeInQuarantineKeepsReferencedPlans(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	host := s.LocalHostID()
	expires := clock.Now().Add(time.Minute).UTC().Format(timeFormat)
	for _, id := range []string{"01XBKEPT", "01XBUNUSED"} {
		exec(t, s, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json,
			guards_json, confirmation, created_at, expires_at)
			VALUES (?, ?, 'core.module.disable', 1, 'user:fixture', 'ih', 'sh', '[]', '[]', '', ?, ?)`,
			id, host, expires, expires)
	}
	exec(t, s, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		input_json, created_at, updated_at)
		VALUES ('01XBCHANGE', '01XBKEPT', ?, 'core.module.disable', 1, 'compensable', '', 'user:fixture', 'quarantined',
		'{}', ?, ?)`, host, expires, expires)
	exec(t, s, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"01XBTX","restoredAt":"2026-10-18T12:00:00Z","backupCreatedAt":"2026-10-17T12:00:00Z"}`)
	if on, err := s.quarantined(context.Background()); err != nil || !on {
		t.Fatalf("quarantined = %v, %v; want true", on, err)
	}

	clock.Advance(PurgeInterval)

	if n := count(t, s, `SELECT count(*) FROM core_plans WHERE id = '01XBUNUSED'`); n != 0 {
		t.Errorf("the unreferenced expired plan survived a purge in quarantine (%d rows)", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_plans WHERE id = '01XBKEPT'`); n != 1 {
		t.Errorf("the quarantined change's plan has %d rows after the purge, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_changes WHERE id = '01XBCHANGE' AND state = 'quarantined'`); n != 1 {
		t.Errorf("the quarantined change has %d rows after the purge, want 1", n)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/5 && TMPDIR=/root/.cache/w7-tmp/5 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestXb' ./internal/controller/server/`
Expected: FAIL with a build error, `s.backupConfig undefined (type *Server has no field or method backupConfig)`. Quote the output. `TestXbPurgeInQuarantineKeepsReferencedPlans` pins behaviour the tree already has (W7-R12: the engine's purge deletes only plans no change references, and nothing gates the purge on the quarantine flag), so it is red here only because the package does not build, and it must pass unchanged after Step 3; if it fails after Step 3, report it instead of changing `services.go` or the engine.

- [ ] **Step 3: Implement**

In `internal/controller/server/extensions.go`:

1. Import `internal/controller/fleet`. In the existing `var (...)` block of compile-time assertions add `_ backup.TokenIssuer = (*fleet.Authority)(nil)` (gofmt aligns the block).
2. Add the method `func (s *Server) backupConfig() backup.Config`, with a doc comment, above `extDeps`. It returns a `backup.Config` whose `KeyFile` is `s.opts.Paths.ControllerKey` and whose `AgentAdvertise` is `s.opts.AgentAdvertise`, and whose `Tokens` is `s.authority` only when `s.authority != nil`. It must never store a nil `*fleet.Authority` in the interface: the backup package refuses enrollment tokens on `Tokens == nil`, and a typed nil would read as a working issuer.
3. In `contributions()` replace `controllerFunc(backup.Contribute)` by `controllerFunc(backup.New(s.backupConfig()).Contribute)`. The contributor's `name` stays `"backup"` and it stays last, so error messages and route order do not change.
4. Reword the doc comments of `controllerFunc` ("such as backup.Contribute") and of `contributions` ("and on backup.Contribute") so they name `Backup.Contribute` of the backup package and the config it is built from; keep them accurate, no other prose changes.

Change nothing in `server.go` or `services.go`: the comment on `quarantined` in `services.go` already says the purge keeps running, which W7-R12 confirms.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w7-tmp/5 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestXb|TestContribution|TestControllerFunc|TestPurges|TestQuarantine' ./internal/controller/server/`
Expected: PASS. Quote the output.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w7-tmp/5 GOTOOLCHAIN=local
mkdir -p "$TMPDIR"
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/controller/server/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf "$TMPDIR" && go clean -testcache
```

Expected: `go build`, `go vet` and `go mod tidy -diff` print nothing; the race run prints `ok` for `internal/controller/server`; `golangci-lint` prints `0 issues.`; the full suite prints only `ok` or `?` lines. Record the real output of each command in the report. The `df -h /` line of Global Constraints applies before the first build.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/server/extensions.go internal/controller/server/extensions_test.go internal/controller/server/services_test.go
git commit -m "feat(server): give the backup its config"
```

### Task 6: Restore preconditions and lock

**Files:**
- Create: `internal/controller/backup/restore_guard.go`
- Test: `internal/controller/backup/restore_guard_test.go`
- Read: `internal/core/units.go` (`core.Units`, `core.UnitState`), `internal/platform/systemd.go` (`NewSystemd`, `DBusSystemd.Close`), `internal/update/updater.go` (`ControllerUnit`, `AgentUnit`), `internal/install/layout.go` (`Production`, `Layout.UnitPath`, `ServiceUser`) and `internal/install/install.go` (`Users`, `SystemUsers`), `internal/controller/server/server.go` (`lock`, the flock pattern)

**Interfaces:**
- Consumes: `Ops` and `OSOps()` (Contracts "Durable file operations", Task 2, merged before this wave); `core.Units`, `core.UnitState`, `core.Clock`, `core.SystemClock`; `update.ControllerUnit` and `update.AgentUnit`; `install.Production()`, `install.ServiceUser`, `install.Users` and `install.SystemUsers`; `platform.NewSystemd`; `paths.Paths`; `coretest.NewFakeClock` in tests.
- Produces: everything in Contracts "Restore host": `RestoreLockPath`, `Host`, `ProductionHost`, `(Host).lock`, `(Host).checkRestore`, with the refusal messages exactly as written there. Also three unexported helpers: `serviceOwner(users install.Users) func() (uid, gid int, err error)`, `ulidTx(clock core.Clock) func() string` and `pendingJournals(dir string) ([]string, error)`. `pendingJournals` lists `restore-*.journal` itself because `ListJournals` (Task 4) is built in the same wave and this task must not call it; Task 15 may replace `pendingJournals` by `ListJournals`, which has the same ordering. `checkRestore` takes the lock as its last check and releases it at once, so a caller (Task 14) runs `checkRestore` before it takes `lock()` for the restore, never after.

**Items:** 54

**Depends on:** Task 2
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

The restore command and the recovery both start from a `Host`. Host safety applies: no test calls `ProductionHost`, talks to systemd or D-Bus, or touches a path outside `t.TempDir()`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/restore_guard_test.go` with exactly this content. `rgUnits` embeds a nil `core.Units`, so any call but `State` panics and fails the test.

```go
package backup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

var rgNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// rgUnits answers State from a table and records which units were asked
// about. Every other method panics through the nil embedded interface, so a
// check that touched a unit in any other way fails the test.
type rgUnits struct {
	core.Units
	states map[string]core.UnitState
	errs   map[string]error
	asked  []string
}

func (u *rgUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	u.asked = append(u.asked, unit)
	if err := u.errs[unit]; err != nil {
		return core.UnitState{}, err
	}
	return u.states[unit], nil
}

// rgWrite creates path with content, and its directory when missing.
func rgWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

// rgJournal is the path of the journal of transaction tx in dir.
func rgJournal(dir, tx string) string { return filepath.Join(dir, "restore-"+tx+".journal") }

// rgHost is a Host over a temporary installation that passes every check:
// root, the unit file, the controller directory and key present, the
// controller inactive, the agent failed, no journal, the lock free.
func rgHost(t *testing.T) (Host, *rgUnits) {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	rgWrite(t, p.ControllerKey, "key")
	if err := os.MkdirAll(p.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	unitFile := filepath.Join(root, "units", update.ControllerUnit)
	rgWrite(t, unitFile, "[Unit]\n")
	units := &rgUnits{
		states: map[string]core.UnitState{
			update.ControllerUnit: {LoadState: "loaded", ActiveState: "inactive"},
			update.AgentUnit:      {LoadState: "loaded", ActiveState: "failed"},
		},
		errs: map[string]error{},
	}
	return Host{
		Paths: p, UnitFile: unitFile, LockFile: filepath.Join(root, "darkpanel-restore.lock"),
		Units: units, Euid: func() int { return 0 },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   OSOps(), Clock: coretest.NewFakeClock(rgNow),
		NewTx: func() string { return "01RGTXAAAAAAAAAAAAAAAAAAAA" },
		Log:   slog.New(slog.DiscardHandler),
	}, units
}

func TestRgCheckRestore(t *testing.T) {
	const (
		notRoot      = "backup: a restore must run as root"
		notInstalled = "backup: no controller is installed here; run darkpanel system install --role controller first"
	)
	stop := func(unit, state string) func(Host) string {
		return func(Host) string {
			return fmt.Sprintf("backup: stop darkpanel-controller.service and darkpanel-agent.service before a restore (%s is %s)", unit, state)
		}
	}
	text := func(s string) func(Host) string { return func(Host) string { return s } }
	journal := func(name string) func(Host) string {
		return func(h Host) string {
			return fmt.Sprintf("backup: an unfinished restore exists (%s); start the controller or run `darkpanel controller restore --recover` first",
				filepath.Join(h.Paths.ControllerDir, name))
		}
	}
	held := func(h Host) string { return "backup: another restore or recovery holds " + h.LockFile }
	takeLock := func(t *testing.T, h *Host) {
		release, err := h.lock()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(release)
	}

	tests := []struct {
		name  string
		setup func(t *testing.T, h *Host, u *rgUnits)
		want  func(Host) string // nil: no refusal
	}{
		{"installed and stopped", func(*testing.T, *Host, *rgUnits) {}, nil},
		{"not root", func(_ *testing.T, h *Host, _ *rgUnits) { h.Euid = func() int { return 1000 } }, text(notRoot)},
		{"not root before not installed", func(t *testing.T, h *Host, _ *rgUnits) {
			h.Euid = func() int { return 1000 }
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, text(notRoot)},
		{"no unit file", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"no controller directory", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.RemoveAll(h.Paths.ControllerDir); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"controller directory is a file", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.RemoveAll(h.Paths.ControllerDir); err != nil {
				t.Fatal(err)
			}
			rgWrite(t, h.Paths.ControllerDir, "not a directory")
		}, text(notInstalled)},
		{"no controller key", func(t *testing.T, h *Host, _ *rgUnits) {
			if err := os.Remove(h.Paths.ControllerKey); err != nil {
				t.Fatal(err)
			}
		}, text(notInstalled)},
		{"not installed before a running service", func(t *testing.T, h *Host, u *rgUnits) {
			if err := os.Remove(h.UnitFile); err != nil {
				t.Fatal(err)
			}
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, text(notInstalled)},
		{"controller running", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.ControllerUnit, "active")},
		{"agent activating", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "activating"}
		}, stop(update.AgentUnit, "activating")},
		{"both running names the controller", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.ControllerUnit, "active")},
		{"units that are not found count as stopped", func(_ *testing.T, _ *Host, u *rgUnits) {
			u.states[update.ControllerUnit] = core.UnitState{LoadState: "not-found", ActiveState: "active"}
			u.states[update.AgentUnit] = core.UnitState{LoadState: "not-found", ActiveState: "active"}
		}, nil},
		{"journal", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
		{"two journals name the oldest", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01B"), "{}")
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
		{"a running service before a journal", func(t *testing.T, h *Host, u *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
			u.states[update.AgentUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
		}, stop(update.AgentUnit, "active")},
		{"a journal's temporary file alone is no journal", func(t *testing.T, h *Host, _ *rgUnits) {
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A")+".new", "{")
		}, nil},
		{"lock held", func(t *testing.T, h *Host, _ *rgUnits) { takeLock(t, h) }, held},
		{"a journal before a held lock", func(t *testing.T, h *Host, _ *rgUnits) {
			takeLock(t, h)
			rgWrite(t, rgJournal(h.Paths.ControllerDir, "01A"), "{}")
		}, journal("restore-01A.journal")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, units := rgHost(t)
			tt.setup(t, &h, units)
			err := h.checkRestore(context.Background())
			if tt.want == nil {
				if err != nil {
					t.Fatalf("checkRestore = %v, want no refusal", err)
				}
				return
			}
			if want := tt.want(h); err == nil || err.Error() != want {
				t.Fatalf("checkRestore = %v, want %q", err, want)
			}
		})
	}
}

// TestRgCheckRestoreAsksControllerThenAgent: the units are read through
// State alone, the controller first, and the first refusal ends the check.
func TestRgCheckRestoreAsksControllerThenAgent(t *testing.T) {
	h, units := rgHost(t)
	if err := h.checkRestore(context.Background()); err != nil {
		t.Fatal(err)
	}
	if want := []string{update.ControllerUnit, update.AgentUnit}; strings.Join(units.asked, ",") != strings.Join(want, ",") {
		t.Errorf("units asked = %v, want %v", units.asked, want)
	}

	h, units = rgHost(t)
	units.states[update.ControllerUnit] = core.UnitState{LoadState: "loaded", ActiveState: "active"}
	if err := h.checkRestore(context.Background()); err == nil {
		t.Fatal("checkRestore accepted a running controller")
	}
	if want := []string{update.ControllerUnit}; strings.Join(units.asked, ",") != strings.Join(want, ",") {
		t.Errorf("units asked after a refusal = %v, want %v", units.asked, want)
	}
}

func TestRgCheckRestoreReportsAStateError(t *testing.T) {
	h, units := rgHost(t)
	boom := errors.New("dbus: connection closed")
	units.errs[update.AgentUnit] = boom
	err := h.checkRestore(context.Background())
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), update.AgentUnit) {
		t.Errorf("checkRestore = %v, want an error wrapping the bus error and naming %s", err, update.AgentUnit)
	}
}

// TestRgCheckRestoreLeavesTheLockFree: the lock check takes and releases the
// lock, so the restore that follows can take it.
func TestRgCheckRestoreLeavesTheLockFree(t *testing.T) {
	h, _ := rgHost(t)
	if err := h.checkRestore(context.Background()); err != nil {
		t.Fatal(err)
	}
	release, err := h.lock()
	if err != nil {
		t.Fatalf("lock after a passed check: %v", err)
	}
	release()
}

func TestRgLockIsExclusiveUntilReleased(t *testing.T) {
	h, _ := rgHost(t)
	release, err := h.lock()
	if err != nil {
		t.Fatal(err)
	}
	if st, err := os.Stat(h.LockFile); err != nil || st.Mode().Perm() != 0o600 {
		t.Errorf("lock file = %v, %v; want mode 0600", st, err)
	}
	_, err = h.lock()
	if want := "backup: another restore or recovery holds " + h.LockFile; err == nil || err.Error() != want {
		t.Fatalf("second lock = %v, want %q", err, want)
	}
	release()
	release()
	again, err := h.lock()
	if err != nil {
		t.Fatalf("lock after release: %v", err)
	}
	if _, err := h.lock(); err == nil {
		t.Error("a repeated release freed the lock a later caller holds")
	}
	again()
}

func TestRgLockRefusesWhatItCannotOpen(t *testing.T) {
	h, _ := rgHost(t)
	h.LockFile = filepath.Join(filepath.Dir(h.LockFile), "missing", "restore.lock")
	if _, err := h.lock(); err == nil || strings.Contains(err.Error(), "another restore") {
		t.Errorf("lock in a missing directory = %v, want an open error", err)
	}

	target := filepath.Join(t.TempDir(), "target")
	rgWrite(t, target, "")
	link := filepath.Join(t.TempDir(), "restore.lock")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	h.LockFile = link
	if _, err := h.lock(); err == nil {
		t.Error("lock followed a symbolic link")
	}
}

type rgUsers struct {
	uid, gid int
	found    bool
	err      error
	asked    string
}

func (u *rgUsers) Lookup(name string) (int, int, bool, error) {
	u.asked = name
	return u.uid, u.gid, u.found, u.err
}

func TestRgServiceOwner(t *testing.T) {
	users := &rgUsers{uid: 996, gid: 995, found: true}
	uid, gid, err := serviceOwner(users)()
	if err != nil || uid != 996 || gid != 995 || users.asked != "darkpanel" {
		t.Errorf("serviceOwner = %d, %d, %v after asking for %q; want 996, 995 and the darkpanel user", uid, gid, err, users.asked)
	}
	if _, _, err := serviceOwner(&rgUsers{})(); err == nil || !strings.Contains(err.Error(), "darkpanel") {
		t.Errorf("serviceOwner for a missing user = %v, want an error naming the user", err)
	}
	boom := errors.New("nss: unavailable")
	if _, _, err := serviceOwner(&rgUsers{err: boom})(); !errors.Is(err, boom) {
		t.Errorf("serviceOwner = %v, want the lookup error wrapped", err)
	}
}

func TestRgUlidTx(t *testing.T) {
	clock := coretest.NewFakeClock(rgNow)
	newTx := ulidTx(clock)
	first, second := newTx(), newTx()
	id, err := ulid.ParseStrict(first)
	if err != nil || first != strings.ToUpper(first) || len(first) != 26 {
		t.Fatalf("transaction id %q: %v; want 26 upper-case ULID characters", first, err)
	}
	if id.Time() != uint64(rgNow.UnixMilli()) {
		t.Errorf("transaction id time = %d, want the clock's %d", id.Time(), rgNow.UnixMilli())
	}
	if first == second {
		t.Error("two transaction ids are equal")
	}
	clock.Advance(time.Second)
	if later := newTx(); later <= first {
		t.Errorf("a later transaction id %s sorts before %s", later, first)
	}
}

func TestRgRestoreLockPath(t *testing.T) {
	if RestoreLockPath != "/run/darkpanel-restore.lock" {
		t.Errorf("RestoreLockPath = %q", RestoreLockPath)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/6 && TMPDIR=/root/.cache/w7-tmp/6 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRg' ./internal/controller/backup/`
Expected: FAIL with a build error naming undefined identifiers, for example `undefined: Host`, `serviceOwner`, `ulidTx`, `RestoreLockPath`. The test names journals with its own helper `rgJournal`, so it needs nothing from Task 4. Quote the output.

- [ ] **Step 3: Implement**

Create `internal/controller/backup/restore_guard.go`, package `backup`, doc comment on every exported identifier, error strings starting `backup:`.

1. `const RestoreLockPath = "/run/darkpanel-restore.lock"` and the `Host` struct with the fields and types of Contracts "Restore host".
2. `(h Host) lock()`: open `h.LockFile` with `golang.org/x/sys/unix` `Open(path, O_RDWR|O_CREAT|O_NOFOLLOW|O_CLOEXEC, 0o600)` (the same pattern as `Server.lock` in `internal/controller/server/server.go`: a raw descriptor, no `*os.File`), then `unix.Flock(fd, LOCK_EX|LOCK_NB)`. An open failure returns `backup: open the lock file: <err>`. `EWOULDBLOCK` closes the descriptor and returns exactly `backup: another restore or recovery holds <h.LockFile>` (no wrapping suffix); any other flock error returns `backup: lock <h.LockFile>: <err>`. The release function closes the descriptor once (`sync.Once`), so calling it twice cannot close a descriptor a later `lock()` reused; the file itself stays on disk.
3. `(h Host) checkRestore(ctx)` returns the first refusal of these, in this order, each message exactly as in Contracts "Restore host" and returned as a plain `errors.New`/`fmt.Errorf` value whose `Error()` equals the text:
   1. `h.Euid() != 0`: `backup: a restore must run as root`.
   2. Not installed: `h.UnitFile` missing (`os.Lstat`), `h.Paths.ControllerDir` missing or not a directory (`os.Stat`), or `h.Paths.ControllerKey` missing (`os.Lstat`): `backup: no controller is installed here; run darkpanel system install --role controller first`. A stat error other than not-exist is returned as `backup: <err>`.
   3. Services: for `update.ControllerUnit` then `update.AgentUnit`, call `h.Units.State(ctx, unit)` and nothing else on `h.Units`. A `State` error returns `backup: read the state of <unit>: %w`. A state whose `LoadState` is `not-found` counts as stopped; otherwise an `ActiveState` other than `inactive` or `failed` returns `backup: stop darkpanel-controller.service and darkpanel-agent.service before a restore (<unit> is <ActiveState>)`. The first refusal ends the check, so the agent is not asked after a running controller.
   4. Journal: `pendingJournals(h.Paths.ControllerDir)` returns the paths matching `restore-*.journal` (`filepath.Match`) in `os.ReadDir` of the directory, sorted ascending by the text between `restore-` and `.journal`; `restore-<tx>.journal.new` does not match. Any journal returns the message ``backup: an unfinished restore exists (<oldest journal path>); start the controller or run `darkpanel controller restore --recover` first``.
   5. Lock: `h.lock()`; its error is returned as it is; on success call the release at once.
4. `serviceOwner(users)` returns a function that calls `users.Lookup(install.ServiceUser)` and returns the ids; a lookup error is wrapped with `%w` under `backup:`; `found == false` returns an error that names the user (`backup: the darkpanel user does not exist`).
5. `ulidTx(clock)` returns a function that makes `ulid.MustNew(ulid.Timestamp(clock.Now()), crypto/rand.Reader).String()`, a fresh upper-case ULID per call (`github.com/oklog/ulid/v2`).
6. `ProductionHost(ctx, p, withUnits)`: when `withUnits` is true, `platform.NewSystemd(ctx)` (an error is returned wrapped under `backup:`); when it is false, no connection, `Units` nil and a close function that returns nil; a `core.SystemClock{}`, and a `Host` with `Paths: p`, `UnitFile: install.Production().UnitPath(update.ControllerUnit)`, `LockFile: RestoreLockPath`, `Units` the systemd connection (or nil), `Euid: os.Geteuid`, `Owner: serviceOwner(install.SystemUsers{})`, `Ops: OSOps()`, `Clock` the system clock, `NewTx: ulidTx(clock)` and `Log: slog.New(slog.NewTextHandler(os.Stderr, nil))`. The second result is the connection's `Close`. It has no unit test (host safety); `go vet`, the linter and Task 19's harness cover it.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w7-tmp/6 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRg' ./internal/controller/backup/`
Expected: PASS. Quote the output.

- [ ] **Step 5: Verify the task**

```bash
export TMPDIR=/root/.cache/w7-tmp/6 GOTOOLCHAIN=local
mkdir -p "$TMPDIR"
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf "$TMPDIR" && go clean -testcache
```

Expected: `go build`, `go vet` and `go mod tidy -diff` print nothing; the race run prints `ok` for `internal/controller/backup`; `golangci-lint` prints `0 issues.`; the full suite prints only `ok` or `?` lines. Record the real output of each command in the report. The `df -h /` line of Global Constraints applies before the first build.

- [ ] **Step 6: Commit**

```bash
git add internal/controller/backup/restore_guard.go internal/controller/backup/restore_guard_test.go
git commit -m "feat(backup): add restore preconditions and lock"
```

### Task 7: Restore-quarantine schema and spec rulings

**Files:**
- Create: `internal/core/store/migrations/controller/0013_restore_quarantine.sql`
- Modify: `internal/core/fleet.go` (the `// Rollout states.` const block and the `State` field of `Rollout`)
- Modify (test): `internal/core/store/migrations_test.go` (appended, plus four imports)
- Regenerate: `schemas/core.update.rollouts/v1.json`, `schemas/core.update.rollout/v1.json` and `schemas/core.update.rollout-cancel/v1.json` (each embeds the `Rollout` type; `make schemas` rewrites exactly these three)
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section 10: one new bullet, no other line)
- Modify: `docs/dforge/registers/2026-10-21-v1-completion.md` (one new row, written with the register script)
- Read: `internal/core/store/migrations/controller/0007_rollout.sql`, `0010_audit_phases.sql`, `0011_platform_surfaces.sql`, `0002_lifecycle.sql`, `internal/core/store/migrate.go`, the `schemas` target of the `Makefile`

**Interfaces:**
- Consumes: `store.CoreMigrations`, `store.Migrate` and the test helpers `openTemp`, `migrateOpts`, `coreSetUpTo`, `lifecycleRows`, `surfacesRows` of `internal/core/store`.
- Produces: Contracts "Schema": `core_changes.quarantined_from TEXT`; `core_rollouts` rebuilt with `state` accepting `quarantined` and a `quarantined_from TEXT` column; `core_rollout_hosts` rebuilt with its reference and cascade; `core.RolloutQuarantined = "quarantined"` and `enum=quarantined` on the rollout view's `state`. Task 8 (`Mark`) and Task 13 (rollout resolve) write `core_rollouts.state = 'quarantined'` and `quarantined_from`; Tasks 8 and 9 read `core_changes.quarantined_from`.

**Items:** 54, 69

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 1 - coupling 2 - risk 3 = 8

This is an Integrator task (plan ruling W7-R1): it adds a migration and the rollout view's state, which wave 0 left out. `internal/core/store/migrations_test.go` has no table-driven test a new row would fit (each existing migration test is its own function, and `TestCoreMigrationsApply` lists tables, not columns), so the migration gets its own test function with one subtest per active rollout state. SQLite cannot change a CHECK constraint in place, and `store.Open` runs with `foreign_keys(1)`: dropping `core_rollouts` while `core_rollout_hosts` still references it would delete every host row through the cascade, which is why the migration copies both tables first and drops the child first (precedent: `0010_audit_phases.sql`, `0011_platform_surfaces.sql`).

- [ ] **Step 1: Write the failing test**

In `internal/core/store/migrations_test.go` add `"database/sql"`, `"encoding/json"`, `"os"` and `"path/filepath"` to the import block (alphabetical, gofmt order) and append the following. The package `store` cannot import `internal/core` (core imports store), so the second test reads the committed snapshot instead of the Go constants.

```go
// rqRolloutStates are the states core_rollouts.state accepts once migration
// 0013 has run, and the enum of the rollout view's state.
var rqRolloutStates = []string{"running", "draining", "quarantined", "succeeded", "failed", "cancelled"}

const rqAt = "2026-11-04T00:00:00.000000000Z"

const (
	rqRolloutRows = `SELECT json_array(id, version, channel, state, outcome, error_code, error_message, change_id,
		created_at, updated_at) FROM core_rollouts ORDER BY id`
	rqHostRows = `SELECT json_array(rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id,
		error_code, error_message, followed_at) FROM core_rollout_hosts ORDER BY rollout_id, host_id`
)

// rqSeed fills a database migrated to 0012 with four rollouts, one of them
// active in state active, and eight hosts in every rollout host state.
func rqSeed(t *testing.T, db *DB, active string) {
	t.Helper()
	ctx := context.Background()
	activeOutcome := ""
	if active == "draining" {
		activeOutcome = "failed"
	}
	for _, r := range []struct{ id, state, outcome, code, message, change string }{
		{"RO1", "succeeded", "", "", "", "C1"},
		{"RO2", "failed", "failed", "update.failed", "host H2 did not commit", ""},
		{"RO3", "cancelled", "cancelled", "", "", ""},
		{"RO4", active, activeOutcome, "", "", "C2"},
	} {
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_rollouts (id, version, channel, state, outcome,
			error_code, error_message, change_id, created_at, updated_at)
			VALUES (?, '0.2.0', 'stable', ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
			r.id, r.state, r.outcome, r.code, r.message, r.change, rqAt, rqAt); err != nil {
			t.Fatalf("rollout %s: %v", r.id, err)
		}
	}
	for _, h := range []struct {
		rollout, host string
		position      int
		state         string
		plan, op      string
		code, message string
	}{
		{"RO1", "H1", 0, "committed", "P1", "OP1", "", ""},
		{"RO1", "H2", 1, "skipped", "", "", "", ""},
		{"RO2", "H1", 0, "committed", "P2", "OP2", "", ""},
		{"RO2", "H2", 1, "failed", "P3", "OP3", "update.timeout", "the update did not finish"},
		{"RO2", "H3", 2, "skipped", "", "", "", ""},
		{"RO3", "H1", 0, "waiting", "", "", "", ""},
		{"RO4", "H1", 0, "updating", "P4", "OP4", "", ""},
		{"RO4", "H2", 1, "waiting", "", "", "", ""},
	} {
		sent := ""
		if h.plan != "" {
			sent = rqAt
		}
		if _, err := db.Writer().ExecContext(ctx, `INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state,
			plan_id, apply_sent_at, operation_id, error_code, error_message, followed_at)
			VALUES (?, ?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''), NULLIF(?, ''))`,
			h.rollout, h.host, h.position, h.state, h.plan, sent, h.op, h.code, h.message, sent); err != nil {
			t.Fatalf("host %s of %s: %v", h.host, h.rollout, err)
		}
	}
}

// rqCount runs a query that returns one integer.
func rqCount(t *testing.T, db *DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

func TestRqRestoreQuarantineMigration(t *testing.T) {
	for _, active := range []string{"running", "draining"} {
		t.Run(active, func(t *testing.T) {
			ctx := context.Background()
			db := openTemp(t)
			opts := migrateOpts(t, "w7", time.Now())
			opts.Side = SideController
			if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideController, 12), nil); err != nil {
				t.Fatal(err)
			}
			lifecycleRows(t, db)
			rqSeed(t, db, active)
			rolloutsBefore, hostsBefore := surfacesRows(t, db, rqRolloutRows), surfacesRows(t, db, rqHostRows)
			if len(rolloutsBefore) != 4 || len(hostsBefore) != 8 {
				t.Fatalf("seeded %d rollouts and %d hosts, want 4 and 8", len(rolloutsBefore), len(hostsBefore))
			}

			set, err := CoreMigrations(SideController)
			if err != nil {
				t.Fatal(err)
			}
			res, err := Migrate(ctx, db, migrateOpts(t, "w7a", time.Now()), set, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(res.Applied) == 0 || res.Applied[0].Number != 13 || res.Applied[0].Name != "restore_quarantine" {
				t.Fatalf("applied %+v, want 0013_restore_quarantine first", res.Applied)
			}

			// Rows, integrity and shape.
			if after := surfacesRows(t, db, rqRolloutRows); !slices.Equal(after, rolloutsBefore) {
				t.Errorf("rollouts after the migration = %v, want %v", after, rolloutsBefore)
			}
			if after := surfacesRows(t, db, rqHostRows); !slices.Equal(after, hostsBefore) {
				t.Errorf("rollout hosts after the migration = %v, want %v", after, hostsBefore)
			}
			if n := rqCount(t, db, `SELECT count(*) FROM core_rollouts WHERE quarantined_from IS NOT NULL`); n != 0 {
				t.Errorf("%d migrated rollouts carry quarantined_from, want none", n)
			}
			violations, err := db.Read().QueryContext(ctx, "PRAGMA foreign_key_check")
			if err != nil {
				t.Fatal(err)
			}
			if violations.Next() {
				t.Error("PRAGMA foreign_key_check reports a violation")
			}
			if err := violations.Close(); err != nil {
				t.Fatal(err)
			}
			var integrity string
			if err := db.Read().QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&integrity); err != nil || integrity != "ok" {
				t.Errorf("PRAGMA integrity_check = %q, %v; want ok", integrity, err)
			}
			for _, table := range []string{"core_rollouts", "core_rollout_hosts"} {
				var tableSQL string
				if err := db.Read().QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", table).
					Scan(&tableSQL); err != nil {
					t.Fatalf("table %s: %v", table, err)
				}
				if !strings.HasSuffix(strings.TrimSpace(tableSQL), "STRICT") {
					t.Errorf("%s is not STRICT: %s", table, tableSQL)
				}
			}
			if n := rqCount(t, db, `SELECT count(*) FROM sqlite_master WHERE name LIKE '%\_new' ESCAPE '\'`); n != 0 {
				t.Errorf("%d leftover _new objects", n)
			}
			var parent, from, to, onDelete string
			if err := db.Read().QueryRowContext(ctx,
				`SELECT "table", "from", "to", on_delete FROM pragma_foreign_key_list('core_rollout_hosts')`).
				Scan(&parent, &from, &to, &onDelete); err != nil {
				t.Fatal(err)
			}
			if parent != "core_rollouts" || from != "rollout_id" || to != "id" || onDelete != "CASCADE" {
				t.Errorf("core_rollout_hosts references %s(%s) from %s ON DELETE %s, want core_rollouts(id) from rollout_id ON DELETE CASCADE",
					parent, to, from, onDelete)
			}
			for _, ix := range []struct {
				table, name     string
				unique, partial int
			}{
				{"core_rollouts", "core_rollouts_one_active", 1, 1},
				{"core_rollouts", "core_rollouts_created", 0, 0},
				{"core_rollout_hosts", "core_rollout_hosts_one_updating", 1, 1},
			} {
				var unique, partial int
				if err := db.Read().QueryRowContext(ctx, `SELECT "unique", partial FROM pragma_index_list(?) WHERE name = ?`,
					ix.table, ix.name).Scan(&unique, &partial); err != nil {
					t.Errorf("index %s: %v", ix.name, err)
					continue
				}
				if unique != ix.unique || partial != ix.partial {
					t.Errorf("index %s unique %d partial %d, want %d and %d", ix.name, unique, partial, ix.unique, ix.partial)
				}
			}

			// A quarantined rollout is not active; the one-active index still
			// refuses a second running or draining rollout beside RO4.
			insert := func(id, state, outcome, quarantinedFrom string) error {
				_, err := db.Writer().ExecContext(ctx, `INSERT INTO core_rollouts (id, version, channel, state, outcome,
					quarantined_from, created_at, updated_at) VALUES (?, '0.3.0', 'stable', ?, NULLIF(?, ''), NULLIF(?, ''), ?, ?)`,
					id, state, outcome, quarantinedFrom, rqAt, rqAt)
				return err
			}
			if err := insert("Q1", "quarantined", "", "running"); err != nil {
				t.Errorf("a quarantined rollout beside an active one: %v", err)
			}
			if err := insert("Q2", "quarantined", "failed", "draining"); err != nil {
				t.Errorf("a second quarantined rollout: %v", err)
			}
			var kept string
			if err := db.Read().QueryRowContext(ctx, "SELECT quarantined_from FROM core_rollouts WHERE id = 'Q2'").Scan(&kept); err != nil || kept != "draining" {
				t.Errorf("quarantined_from of Q2 = %q, %v; want draining", kept, err)
			}
			refused := []struct{ name, state, outcome string }{
				{"a second running rollout", "running", ""},
				{"a second draining rollout", "draining", "failed"},
				{"an unknown state", "paused", ""},
				{"an unknown outcome", "succeeded", "bogus"},
			}
			for i, r := range refused {
				if err := insert("X"+strconv.Itoa(i), r.state, r.outcome, ""); err == nil {
					t.Errorf("%s: accepted", r.name)
				}
			}

			// The host table keeps its constraints and its reference.
			host := func(rollout, host string, position int, state string) error {
				_, err := db.Writer().ExecContext(ctx, `INSERT INTO core_rollout_hosts (rollout_id, host_id, position, state)
					VALUES (?, ?, ?, ?)`, rollout, host, position, state)
				return err
			}
			if err := host("Q1", "H1", 0, "updating"); err != nil {
				t.Errorf("a first updating host: %v", err)
			}
			hostRefused := []struct {
				name, rollout, host string
				position            int
				state               string
			}{
				{"a second updating host", "Q1", "H2", 1, "updating"},
				{"a repeated position", "Q1", "H3", 0, "waiting"},
				{"a repeated host", "Q1", "H1", 2, "waiting"},
				{"a negative position", "Q1", "H4", -1, "waiting"},
				{"an unknown host state", "Q1", "H5", 3, "paused"},
				{"an unknown rollout", "NOPE", "H1", 0, "waiting"},
			}
			for _, r := range hostRefused {
				if err := host(r.rollout, r.host, r.position, r.state); err == nil {
					t.Errorf("%s: accepted", r.name)
				}
			}

			// Deleting a rollout still deletes its hosts and only its hosts.
			if _, err := db.Writer().ExecContext(ctx, "DELETE FROM core_rollouts WHERE id = 'RO1'"); err != nil {
				t.Fatal(err)
			}
			for rollout, want := range map[string]int{"RO1": 0, "RO2": 3, "RO3": 1, "RO4": 2, "Q1": 1} {
				if n := rqCount(t, db, "SELECT count(*) FROM core_rollout_hosts WHERE rollout_id = ?", rollout); n != want {
					t.Errorf("hosts of %s after deleting RO1 = %d, want %d", rollout, n, want)
				}
			}
			if _, err := db.Writer().ExecContext(ctx, "DELETE FROM core_rollouts"); err != nil {
				t.Fatal(err)
			}
			if n := rqCount(t, db, "SELECT count(*) FROM core_rollout_hosts"); n != 0 {
				t.Errorf("%d host rows are left after every rollout was deleted", n)
			}

			// Every state of the view is stored, and the draining rule holds.
			for _, state := range rqRolloutStates {
				outcome := ""
				if state == "draining" {
					outcome = "failed"
				}
				if err := insert("S-"+state, state, outcome, ""); err != nil {
					t.Errorf("state %s refused: %v", state, err)
				}
				if _, err := db.Writer().ExecContext(ctx, "DELETE FROM core_rollouts WHERE id = ?", "S-"+state); err != nil {
					t.Fatal(err)
				}
			}
			if err := insert("D1", "draining", "", ""); err == nil {
				t.Error("a draining rollout without an outcome was accepted")
			}
			if err := insert("A1", "running", "", ""); err != nil {
				t.Errorf("a running rollout with none active: %v", err)
			}
			if err := insert("A2", "running", "", ""); err == nil {
				t.Error("a second running rollout was accepted")
			}

			// core_changes remembers the state a restore quarantined a change from.
			var changeFrom sql.NullString
			if err := db.Read().QueryRowContext(ctx, "SELECT quarantined_from FROM core_changes WHERE id = 'C1'").Scan(&changeFrom); err != nil || changeFrom.Valid {
				t.Errorf("quarantined_from of the old change = %v, %v; want NULL", changeFrom, err)
			}
			if _, err := db.Writer().ExecContext(ctx,
				"UPDATE core_changes SET state = 'quarantined', quarantined_from = 'applying' WHERE id = 'C1'"); err != nil {
				t.Fatal(err)
			}
			if err := db.Read().QueryRowContext(ctx, "SELECT quarantined_from FROM core_changes WHERE id = 'C1'").Scan(&changeFrom); err != nil ||
				changeFrom.String != "applying" {
				t.Errorf("quarantined_from after the update = %v, %v; want applying", changeFrom, err)
			}
		})
	}
}

// TestRqRolloutViewListsEveryStoredState: the committed snapshot of the
// rollout view enumerates the states the table accepts, so a rollout in
// restore quarantine can be listed.
func TestRqRolloutViewListsEveryStoredState(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "..", "..", "schemas", "core.update.rollouts", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	var snapshot struct {
		Output struct {
			Defs struct {
				Rollout struct {
					Properties struct {
						State struct {
							Enum []string `json:"enum"`
						} `json:"state"`
					} `json:"properties"`
				} `json:"Rollout"`
			} `json:"$defs"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatal(err)
	}
	got := slices.Sorted(slices.Values(snapshot.Output.Defs.Rollout.Properties.State.Enum))
	if want := slices.Sorted(slices.Values(rqRolloutStates)); !slices.Equal(got, want) {
		t.Errorf("the rollout view's state enum = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/7 && TMPDIR=/root/.cache/w7-tmp/7 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRq' ./internal/core/store/`
Expected: FAIL. `TestRqRestoreQuarantineMigration/running` and `/draining` fail with `applied [], want 0013_restore_quarantine first`, and `TestRqRolloutViewListsEveryStoredState` fails with the enum `[cancelled draining failed running succeeded]` where it wants `quarantined` as well. Quote the output.

- [ ] **Step 3: Implement**

1. Create `internal/core/store/migrations/controller/0013_restore_quarantine.sql` with exactly this content (a migration is a configuration file; it was run against a copy of the 0007 tables with rows in every state, foreign keys on, and kept every row, index and the cascade):

```sql
-- Restore quarantine (spec 10): a restored database marks its non-terminal
-- records quarantined and remembers the state each had. core_changes gains
-- quarantined_from. core_rollouts.state also accepts quarantined and the
-- table gains quarantined_from; SQLite cannot change a CHECK constraint in
-- place, so core_rollouts is rebuilt with the wider list, keeping every
-- column, row and index.
--
-- core_rollout_hosts references core_rollouts with ON DELETE CASCADE and
-- the connection enforces foreign keys, so dropping core_rollouts while the
-- old child exists would delete every host row. Both tables are therefore
-- copied into new tables first, the old child is dropped before its parent,
-- and the renames follow; SQLite rewrites the new child's reference to the
-- renamed parent.

ALTER TABLE core_changes ADD COLUMN quarantined_from TEXT;

CREATE TABLE core_rollouts_new (
    id               TEXT PRIMARY KEY,
    version          TEXT NOT NULL,
    channel          TEXT NOT NULL CHECK (channel IN ('stable', 'beta')),
    state            TEXT NOT NULL
        CHECK (state IN ('running', 'draining', 'quarantined', 'succeeded', 'failed', 'cancelled')),
    outcome          TEXT CHECK (outcome IN ('failed', 'cancelled')),
    error_code       TEXT,
    error_message    TEXT,
    change_id        TEXT,
    created_at       TEXT NOT NULL,
    updated_at       TEXT NOT NULL,
    quarantined_from TEXT,
    CHECK (state <> 'draining' OR outcome IS NOT NULL)
) STRICT;

INSERT INTO core_rollouts_new (id, version, channel, state, outcome, error_code, error_message, change_id,
                               created_at, updated_at)
    SELECT id, version, channel, state, outcome, error_code, error_message, change_id, created_at, updated_at
    FROM core_rollouts;

CREATE TABLE core_rollout_hosts_new (
    rollout_id    TEXT NOT NULL REFERENCES core_rollouts_new (id) ON DELETE CASCADE,
    host_id       TEXT NOT NULL,
    position      INTEGER NOT NULL CHECK (position >= 0),
    state         TEXT NOT NULL
        CHECK (state IN ('waiting', 'updating', 'committed', 'failed', 'skipped')),
    plan_id       TEXT,
    apply_sent_at TEXT,
    operation_id  TEXT,
    error_code    TEXT,
    error_message TEXT,
    followed_at   TEXT,
    PRIMARY KEY (rollout_id, host_id),
    UNIQUE (rollout_id, position)
) STRICT;

INSERT INTO core_rollout_hosts_new (rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id,
                                    error_code, error_message, followed_at)
    SELECT rollout_id, host_id, position, state, plan_id, apply_sent_at, operation_id, error_code, error_message,
           followed_at
    FROM core_rollout_hosts;

DROP TABLE core_rollout_hosts;
DROP TABLE core_rollouts;

ALTER TABLE core_rollouts_new RENAME TO core_rollouts;
ALTER TABLE core_rollout_hosts_new RENAME TO core_rollout_hosts;

-- Every active rollout has the same index key, so the database itself
-- refuses a second one (rollout.in_progress). A quarantined rollout is not
-- active.
CREATE UNIQUE INDEX core_rollouts_one_active ON core_rollouts ((1))
    WHERE state IN ('running', 'draining');
CREATE INDEX core_rollouts_created ON core_rollouts (created_at);

CREATE UNIQUE INDEX core_rollout_hosts_one_updating ON core_rollout_hosts (rollout_id)
    WHERE state = 'updating';
```

2. In `internal/core/fleet.go` add `RolloutQuarantined = "quarantined"` to the `// Rollout states.` const block directly after `RolloutDraining` (run `gofmt -w`, which realigns the block's `=` signs), and in the `State` field of `Rollout` add `enum=quarantined` between `enum=draining` and `enum=succeeded` in the `jsonschema` tag, and append `; quarantined means a controller restore stopped it until it is resolved` to the `jsonschema_description` text, before its closing full stop, so it reads "Rollout state; draining means dispatch stopped while a host's update is still running; quarantined means a controller restore stopped it until it is resolved." Change nothing else in the file.
3. Regenerate the snapshots: `GOTOOLCHAIN=local TMPDIR=/root/.cache/w7-tmp/7 timeout 300 make schemas`. `git status --short schemas` must list exactly the three files named under **Files**, and each diff must add only the `quarantined` enum value and the description text.
4. Add the spec paragraph. Insert the following bullet in section 10 of `docs/dforge/specs/2026-10-21-v1-completion-design.md` directly above the line that begins `- **Runbook (R81).**`, with the text exactly as given, so that the new bullet's last line is followed at once by the `- **Runbook (R81).**` line. Use the Edit tool with that line as the anchor; touch no other line.

```markdown
- **Wave 7 rulings.** Wave 7 (plan `v1-w7-backup-restore`) settles these
  gaps and underdetermined points of the text above:
  - **W7-R1 schema.** Wave 0 built neither `quarantined_from` nor a
    `quarantined` rollout state. Migration
    `controller/0013_restore_quarantine.sql` adds
    `core_changes.quarantined_from` and rebuilds `core_rollouts` (its
    `state` also accepts `quarantined`, and it gains `quarantined_from`) and
    `core_rollout_hosts`, keeping every row, every index and the `ON DELETE
    CASCADE` reference. Both tables are copied before either is dropped and
    the child is dropped first, because foreign keys are enforced.
  - **W7-R2 call site.** `ext.Deps` carries no key file path, token issuer
    or agent address, so the server hands them to the backup contribution as
    `backup.Config` beside `ext.Deps`. `ext.Deps` and the modules'
    contributions do not change.
  - **W7-R3 record kinds.** `rollout` is a `core_rollouts` row; `composite`
    is a change of class composite; `cloud` is another change whose action's
    module (the part before the first dot) is `tailnet` or `cloudflare`;
    `change` is every other change. Cloudflare hostname phases and uncertain
    cloud effects belong to the change that owns them, so quarantining that
    change quarantines them. Auth-key sweeps are a service suspended until
    release, not records.
  - **W7-R4 resolve outcomes.** A `change` or `cloud` record resolves only
    `repair` (state `needs_repair`, error `core.needs_repair`, locks kept),
    because no agent executes it and only its effects' Observe can read the
    provider; `done` and `never` are refused. A `composite` resolves
    `repair`, or `done` and `never` only when every step's child is on an
    agent, judged from each child's lookup: an agent that is not connected
    refuses with its own error; `running` or `recovering` answers `409
    core.change_busy`; applied means terminal `confirmed`, `completed`,
    `repaired` or `kept`; not applied means `absent`, `retired`, or terminal
    `reverted`, `failed`, `interrupted` or `abandoned` having made nothing;
    a step with no child plan is not applied; a read step counts as neither.
    `done` needs every other step applied and a forward composite and sets
    `completed`; `never` needs every other step not applied, retires each
    `absent` child plan, and sets `abandoned` with "restore: never applied";
    any other mix is refused ("the composite is partly applied; resolve it
    repair"). A `rollout` resolves only `never`, which sets `cancelled`,
    because each agent's update runs its own transaction (ADR 0007). Every
    outcome runs in one transaction that re-reads the record's state
    (`quarantined`, else `409 plan.state_changed`) and deletes the record's
    lock rows on `done` and `never`.
  - **W7-R5 migrate, then mark.** `store.Migrate` runs each migration in its
    own transaction, so phase 3 migrates the staged database and then marks
    it in one transaction, checkpoints it, switches it to
    `journal_mode=DELETE`, closes and fsyncs it, and only then journals
    `quarantined`. A crash before that write rolls the transaction back and
    discards the staged file, so nothing unmarked is ever installed.
  - **W7-R6 begun phase.** The journal is first saved with phase `begun`,
    before the first staged file is created, so recovery can remove a partly
    written staging.
  - **W7-R7 canary.** The backup route upserts a sealed
    `core_meta.backup_canary` (`{"createdAt"}`) before each copy; its
    `createdAt` is the marker's `backupCreatedAt`, because the file format
    carries no timestamp.
  - **W7-R8 enrollment token.** `quarantine enroll-token` works only while
    the quarantine is active, for an existing host other than the local one,
    and only when the controller has an agent address (`--agent-advertise`).
    A restored host is `enrolled`, but enrollment accepts only `pending` or
    `revoked`, so the route sets the host `pending` in the transaction that
    issues the token.
  - **W7-R9 release.** With the quarantine not active, release answers with
    the held locks of the remaining quarantined records. While it is active,
    release is refused with `409 core.quarantined` if any record is
    quarantined and `--keep-unresolved` is not given; otherwise one
    transaction deletes the flag and reads the held locks. The suspended
    sources then start on their own at their next poll.
  - **W7-R10 exposures.** No exposure table exists before wave 8, so marking
    closes runtime listeners only; wave 8 adds each exposure's
    `needs_repair` marking to marking and to verification.
  - **W7-R11 set-aside files.** A successful restore removes the `.pre-<tx>`
    files of earlier transactions after its own journal is removed; this
    wave adds no separate cleanup command.
  - **W7-R12 purge.** The purge of expired plans, sessions and tokens stays
    ungated: it is not among the suspended sources. A plan that a change
    references is never purged, because `core_changes.plan_id` references
    `core_plans` and foreign keys are enforced.
  - **W7-R13 audit.** Each backup, enroll-token, resolve and release writes
    one audit row, because each is a root operation on the controller.
  - **W7-R14 service check.** Restore reads both units' states through
    `core.Units` over D-Bus, so no exec allowlist rule is added.
  - **W7-R15 unit rollback.** `ExecStartPre` names a command only binaries
    from wave 7 on have. A unit file is written only by `system install`, so
    a unit never runs an older binary, except after a manual binary rollback
    below this wave, which the reinstall boundary of the release candidate
    (0.x → 1.0 is a reinstall) excludes.
```

5. Add the register row for W7-R10 with the register script, from the repository root (the row number is the next free one; do not edit the file by hand):

```bash
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register add docs/dforge/registers/2026-10-21-v1-completion.md "v1-w8 panel exposure: backup.Mark sets every exposure needs_repair and backup.VerifyInstalled refuses an installed database whose exposure is in another state; wave 7 marks runtime listeners only because no exposure table exists before wave 8 (plan v1-w7-backup-restore, W7-R10)" --acceptance "Mark sets each exposure needs_repair and VerifyInstalled fails on an exposure in any other state, with wave 8 tests for both"
bash /root/.claude-alt/plugins/cache/darkraise/dforge/4.11.1/scripts/register check docs/dforge/registers/2026-10-21-v1-completion.md
```

   The `check` prints `register: 0 errors`. The new row's state is `open` and its assignment `-`, so the wave 8 plan picks it up.

- [ ] **Step 4: Run test to verify it passes**

Run: `TMPDIR=/root/.cache/w7-tmp/7 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestRq|TestMigrate|TestCoreMigrations' ./internal/core/store/ && TMPDIR=/root/.cache/w7-tmp/7 GOTOOLCHAIN=local timeout 600 go test -count=1 ./schemas/... ./internal/modules/ ./internal/controller/rollout/`
Expected: PASS (`TestSnapshotsCurrent` in `internal/modules` is the check that the three snapshots match `fleet.go`). Quote the output.

- [ ] **Step 5: Verify the task**

Check the spec edit added lines only, and the register and schemas are as expected:

```bash
git diff --numstat docs/dforge/specs/2026-10-21-v1-completion-design.md
git diff --stat -- schemas docs/dforge/registers
GOTOOLCHAIN=local TMPDIR=/root/.cache/w7-tmp/7 timeout 300 make schemas && git status --short schemas
```

Expected: the numstat prints `85` added lines and `0` deleted for the spec file (the second column is `0`); the stat shows the three snapshot files and one added register row; the repeated `make schemas` changes nothing further. Then run the gates:

```bash
export TMPDIR=/root/.cache/w7-tmp/7 GOTOOLCHAIN=local
mkdir -p "$TMPDIR"
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/core/ ./internal/core/store/ ./schemas/... ./internal/controller/rollout/ ./internal/modules/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 1500 go test -count=1 ./...
rm -rf "$TMPDIR" && go clean -testcache
```

Expected: `go build`, `go vet` and `go mod tidy -diff` print nothing; the race run prints `ok` for the listed packages; `golangci-lint` prints `0 issues.`; the full suite prints only `ok` or `?` lines. Record the real output of each command in the report. The `df -h /` line of Global Constraints applies before the first build.

- [ ] **Step 6: Commit**

Three commits, each atomic:

```bash
git add internal/core/store/migrations/controller/0013_restore_quarantine.sql internal/core/store/migrations_test.go internal/core/fleet.go schemas/core.update.rollouts/v1.json schemas/core.update.rollout/v1.json schemas/core.update.rollout-cancel/v1.json
git commit -m "feat(store): add restore quarantine schema"
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): add the wave 7 rulings"
git add docs/dforge/registers/2026-10-21-v1-completion.md
git commit -m "docs(register): add the exposure marking row"
```

### Task 8: Quarantine marking

**Files:**
- Create: `internal/controller/backup/mark.go`
- Test: `internal/controller/backup/mark_test.go`

**Interfaces:**
- Consumes: Contracts "Controller database fixture" (Task 3: `fxDB`, `fxInsertChange`, `fxInsertComposite`, `fxInsertLock`, `fxInsertRollout`, `fxInsertListener`, `fxStep`, `fxChange`); Contracts "Schema" (Task 7: `core_changes.quarantined_from`, `core_rollouts.quarantined_from` and the rollout state `quarantined`, `core.RolloutQuarantined`); `quarantine.Marker`, `quarantine.MetaKey` and `quarantine.Read` from `internal/controller/quarantine`; `core.State*`, `core.RolloutRunning`, `core.RolloutDraining`, `core.ListenerOpen`, `core.ListenerClosed`.
- Produces: `NonTerminal` and `Mark`, exactly as Contracts "Marking" declares them; Task 11's tests mark a fixture database with `Mark`, and Task 14's phase 3 calls it on the staged database.

**Items:** 69

**Depends on:** Task 3, Task 7
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

The test helpers carry the prefix `mk`; `mkDump` renders any query's rows as strings so a test compares whole tables before and after.

```go
package backup

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var (
	mkNonTerminalStates = []string{"applying", "pending", "awaiting_confirmation", "undoing", "needs_repair", "recovering", "releasing"}
	mkTerminalStates    = []string{"confirmed", "reverted", "completed", "failed", "interrupted", "repaired", "kept", "abandoned"}
	mkRestoredAt        = time.Date(2026, 11, 4, 16, 30, 15, 123456789, time.FixedZone("plus1", 3600))
)

// mkStamp is mkRestoredAt in the lifecycle time format, in UTC.
const mkStamp = "2026-11-04T15:30:15.123456789Z"

func mkMarker() quarantine.Marker {
	return quarantine.Marker{
		Tx:              "01JTXMARKER000000000000000",
		RestoredAt:      mkRestoredAt,
		BackupCreatedAt: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC),
	}
}

func mkMark(t *testing.T, db *store.DB) {
	t.Helper()
	if err := Mark(context.Background(), db, mkMarker()); err != nil {
		t.Fatalf("Mark: %v", err)
	}
}

// mkDump renders every row of query as one string, columns joined by "|"; a
// NULL renders as "<nil>".
func mkDump(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// mkByID maps the first column of each dumped row to the whole row.
func mkByID(rows []string) map[string]string {
	m := make(map[string]string, len(rows))
	for _, r := range rows {
		id, _, _ := strings.Cut(r, "|")
		m[id] = r
	}
	return m
}

func mkExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

const mkChangeQuery = `SELECT id, state, COALESCE(quarantined_from, 'NULL'), recovery_owner, updated_at FROM core_changes ORDER BY id`

func TestMkNonTerminalIsTheSevenUnfinishedStates(t *testing.T) {
	got := slices.Sorted(slices.Values(NonTerminal))
	want := slices.Sorted(slices.Values(mkNonTerminalStates))
	if !slices.Equal(got, want) {
		t.Errorf("NonTerminal = %v, want the states of spec 1.3 without quarantined: %v", got, want)
	}
}

func TestMkQuarantinesEveryNonTerminalChangeOnly(t *testing.T) {
	db := fxDB(t, "")
	for _, s := range mkNonTerminalStates {
		fxInsertChange(t, db, fxChange{ID: "n-" + s, HostID: "host-a", Action: "fixture.act", State: s, Owner: "engine"})
	}
	for _, s := range mkTerminalStates {
		fxInsertChange(t, db, fxChange{ID: "t-" + s, HostID: "host-a", Action: "fixture.act", State: s})
	}
	before := mkByID(mkDump(t, db, mkChangeQuery))
	mkMark(t, db)
	after := mkByID(mkDump(t, db, mkChangeQuery))
	for _, s := range mkNonTerminalStates {
		id := "n-" + s
		want := fmt.Sprintf("%s|quarantined|%s||%s", id, s, mkStamp)
		if after[id] != want {
			t.Errorf("change in %s:\n got %s\nwant %s", s, after[id], want)
		}
	}
	for _, s := range mkTerminalStates {
		id := "t-" + s
		if after[id] != before[id] {
			t.Errorf("terminal change in %s changed:\n got %s\nwant %s", s, after[id], before[id])
		}
		if !strings.Contains(after[id], "|"+s+"|NULL|") {
			t.Errorf("terminal change in %s: %s, want state kept and no quarantined_from", s, after[id])
		}
	}
}

func TestMkLeavesCompositesAndTheirStepsUnwound(t *testing.T) {
	db := fxDB(t, "")
	steps := []fxStep{
		{ID: "s1", HostID: "host-a", Action: "fixture.one/v1", State: "applied", ChildPlanID: "plan-child", ChildChangeID: "child-change"},
		{ID: "s2", HostID: "host-b", Action: "fixture.two/v1", State: "pending"},
	}
	for _, s := range mkNonTerminalStates {
		dir := "forward"
		if s == "undoing" || s == "releasing" {
			dir = "unwind"
		}
		fxInsertComposite(t, db, "k-"+s, s, dir, steps)
	}
	for _, s := range []string{"completed", "abandoned", "failed", "interrupted"} {
		fxInsertComposite(t, db, "kt-"+s, s, "forward", steps)
	}
	fxInsertChange(t, db, fxChange{ID: "child-of-applying", HostID: "host-a", Action: "fixture.one", State: "applying", ParentID: "k-applying"})
	compositesBefore := mkDump(t, db, `SELECT * FROM core_composites ORDER BY change_id`)
	stepsBefore := mkDump(t, db, `SELECT * FROM core_composite_steps ORDER BY change_id, step_id`)
	terminalBefore := mkByID(mkDump(t, db, mkChangeQuery))

	mkMark(t, db)

	after := mkByID(mkDump(t, db, mkChangeQuery))
	for _, s := range mkNonTerminalStates {
		id := "k-" + s
		if want := fmt.Sprintf("%s|quarantined|%s||%s", id, s, mkStamp); after[id] != want {
			t.Errorf("composite in %s:\n got %s\nwant %s", s, after[id], want)
		}
	}
	for _, s := range []string{"completed", "abandoned", "failed", "interrupted"} {
		if id := "kt-" + s; after[id] != terminalBefore[id] {
			t.Errorf("terminal composite %s changed:\n got %s\nwant %s", s, after[id], terminalBefore[id])
		}
	}
	if want := fmt.Sprintf("child-of-applying|quarantined|applying||%s", mkStamp); after["child-of-applying"] != want {
		t.Errorf("child change:\n got %s\nwant %s", after["child-of-applying"], want)
	}
	if got := mkDump(t, db, `SELECT * FROM core_composites ORDER BY change_id`); !slices.Equal(got, compositesBefore) {
		t.Errorf("core_composites changed:\n got %v\nwant %v", got, compositesBefore)
	}
	if got := mkDump(t, db, `SELECT * FROM core_composite_steps ORDER BY change_id, step_id`); !slices.Equal(got, stepsBefore) {
		t.Errorf("core_composite_steps changed:\n got %v\nwant %v", got, stepsBefore)
	}
}

func TestMkKeepsRecordsThatAreAlreadyQuarantined(t *testing.T) {
	db := fxDB(t, "")
	ctx := context.Background()
	fxInsertChange(t, db, fxChange{ID: "q-change", Action: "fixture.act", State: "quarantined"})
	mkExec(t, db, `UPDATE core_changes SET quarantined_from = 'needs_repair' WHERE id = 'q-change'`)
	fxInsertRollout(t, db, "q-rollout", "quarantined", "host-a")
	mkExec(t, db, `UPDATE core_rollouts SET quarantined_from = 'draining' WHERE id = 'q-rollout'`)
	fxInsertChange(t, db, fxChange{ID: "n-change", Action: "fixture.act", State: "applying"})
	mkExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey,
		`{"tx":"OLDTX","restoredAt":"2026-10-01T00:00:00Z","backupCreatedAt":"2026-09-30T00:00:00Z"}`)
	changeBefore := mkByID(mkDump(t, db, mkChangeQuery))["q-change"]
	rolloutBefore := mkDump(t, db, `SELECT * FROM core_rollouts WHERE id = 'q-rollout'`)

	mkMark(t, db)

	if got := mkByID(mkDump(t, db, mkChangeQuery))["q-change"]; got != changeBefore {
		t.Errorf("already quarantined change:\n got %s\nwant %s", got, changeBefore)
	}
	if got := mkDump(t, db, `SELECT * FROM core_rollouts WHERE id = 'q-rollout'`); !slices.Equal(got, rolloutBefore) {
		t.Errorf("already quarantined rollout:\n got %v\nwant %v", got, rolloutBefore)
	}
	if got, want := mkByID(mkDump(t, db, mkChangeQuery))["n-change"], fmt.Sprintf("n-change|quarantined|applying||%s", mkStamp); got != want {
		t.Errorf("unfinished change next to them:\n got %s\nwant %s", got, want)
	}
	m, ok, err := quarantine.Read(ctx, db)
	if err != nil || !ok || m.Tx != mkMarker().Tx {
		t.Errorf("marker after a second restore = %+v, %v, %v; want tx %s from the new restore", m, ok, err, mkMarker().Tx)
	}
}

func TestMkRollouts(t *testing.T) {
	cases := []struct {
		name, state, preFrom, wantState, wantFrom string
		touched                                   bool
	}{
		{"running", "running", "", "quarantined", "running", true},
		{"draining", "draining", "", "quarantined", "draining", true},
		{"succeeded", "succeeded", "", "succeeded", "NULL", false},
		{"failed", "failed", "", "failed", "NULL", false},
		{"cancelled", "cancelled", "", "cancelled", "NULL", false},
		{"quarantined", "quarantined", "running", "quarantined", "running", false},
	}
	const q = `SELECT state, COALESCE(quarantined_from, 'NULL'), COALESCE(outcome, 'NULL'), updated_at FROM core_rollouts WHERE id = 'r1'`
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := fxDB(t, "")
			fxInsertRollout(t, db, "r1", c.state, "host-a", "host-b")
			if c.preFrom != "" {
				mkExec(t, db, `UPDATE core_rollouts SET quarantined_from = ? WHERE id = 'r1'`, c.preFrom)
			}
			before := strings.Split(mkDump(t, db, q)[0], "|")
			hostsBefore := mkDump(t, db, `SELECT * FROM core_rollout_hosts ORDER BY rollout_id, host_id`)
			if len(hostsBefore) != 2 {
				t.Fatalf("fixture rollout hosts = %v, want 2 rows", hostsBefore)
			}

			mkMark(t, db)

			got := strings.Split(mkDump(t, db, q)[0], "|")
			wantUpdated := before[3]
			if c.touched {
				wantUpdated = mkStamp
			}
			if got[0] != c.wantState || got[1] != c.wantFrom || got[2] != before[2] || got[3] != wantUpdated {
				t.Errorf("rollout %s = %v, want state %s, quarantined_from %s, outcome %s, updated_at %s",
					c.state, got, c.wantState, c.wantFrom, before[2], wantUpdated)
			}
			if after := mkDump(t, db, `SELECT * FROM core_rollout_hosts ORDER BY rollout_id, host_id`); !slices.Equal(after, hostsBefore) {
				t.Errorf("rollout hosts changed:\n got %v\nwant %v", after, hostsBefore)
			}
		})
	}
}

func TestMkClosesOnlyOpenListeners(t *testing.T) {
	db := fxDB(t, "")
	for id, state := range map[string]string{"l-open": "open", "l-revoked": "revoked", "l-closed": "closed"} {
		fxInsertListener(t, db, id, state)
	}
	const q = `SELECT id, state, updated_at FROM core_listeners ORDER BY id`
	before := mkByID(mkDump(t, db, q))
	mkMark(t, db)
	after := mkByID(mkDump(t, db, q))
	if want := "l-open|closed|" + mkStamp; after["l-open"] != want {
		t.Errorf("open listener = %s, want %s", after["l-open"], want)
	}
	for _, id := range []string{"l-revoked", "l-closed"} {
		if after[id] != before[id] {
			t.Errorf("listener %s changed:\n got %s\nwant %s", id, after[id], before[id])
		}
	}
}

func TestMkDisposesOfLocksByOwner(t *testing.T) {
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c-live", Action: "fixture.act", State: "applying", Owner: "engine"})
	fxInsertChange(t, db, fxChange{ID: "c-repair", Action: "fixture.act", State: "needs_repair"})
	fxInsertChange(t, db, fxChange{ID: "c-already", Action: "fixture.act", State: "quarantined"})
	fxInsertChange(t, db, fxChange{ID: "c-done", Action: "fixture.act", State: "completed"})
	fxInsertLock(t, db, "k.live1", "c-live")
	fxInsertLock(t, db, "k.live2", "c-live")
	fxInsertLock(t, db, "k.repair", "c-repair")
	fxInsertLock(t, db, "k.already", "c-already")
	fxInsertLock(t, db, "k.done", "c-done")
	fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
	fxInsertLock(t, db, "k.orphan", "no-such-owner")

	mkMark(t, db)

	want := []string{"k.already|c-already", "k.live1|c-live", "k.live2|c-live", "k.repair|c-repair"}
	if got := mkDump(t, db, `SELECT key, owner FROM core_locks ORDER BY key`); !slices.Equal(got, want) {
		t.Errorf("locks after marking:\n got %v\nwant %v (a terminal change's, an update reservation's and an orphan's are deleted)", got, want)
	}
}

const mkTaskRunsDDL = `CREATE TABLE core_task_runs (
    change_id     TEXT NOT NULL REFERENCES core_changes (id),
    op            TEXT NOT NULL CHECK (op IN ('apply', 'undo', 'recover')),
    attempt       INTEGER NOT NULL CHECK (attempt >= 1),
    state         TEXT NOT NULL CHECK (state IN ('starting', 'running', 'succeeded', 'failed', 'cancelled')),
    output_ct     BLOB,
    error_code    TEXT NOT NULL DEFAULT '',
    error_message TEXT NOT NULL DEFAULT '',
    pid           INTEGER,
    started_at    TEXT NOT NULL,
    finished_at   TEXT,
    PRIMARY KEY (change_id, op, attempt)
) STRICT`

func TestMkTaskRuns(t *testing.T) {
	const tableCount = `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'core_task_runs'`
	t.Run("absent", func(t *testing.T) {
		db := fxDB(t, "")
		if got := mkDump(t, db, tableCount); got[0] != "0" {
			t.Fatalf("the controller schema has core_task_runs (%s); this case needs it absent", got[0])
		}
		fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying"})
		mkMark(t, db)
		if got := mkByID(mkDump(t, db, mkChangeQuery))["c1"]; !strings.Contains(got, "|quarantined|applying|") {
			t.Errorf("change = %s, want quarantined from applying", got)
		}
	})
	t.Run("present", func(t *testing.T) {
		db := fxDB(t, "")
		mkExec(t, db, mkTaskRunsDDL)
		fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying"})
		fxInsertChange(t, db, fxChange{ID: "c2", Action: "fixture.act", State: "completed"})
		for _, run := range [][]any{{"c1", "apply", 1, "running"}, {"c1", "recover", 2, "starting"}, {"c2", "apply", 1, "succeeded"}} {
			mkExec(t, db, `INSERT INTO core_task_runs (change_id, op, attempt, state, started_at) VALUES (?, ?, ?, ?, '2026-11-04T12:00:00Z')`, run...)
		}
		mkMark(t, db)
		if got := mkDump(t, db, `SELECT count(*) FROM core_task_runs`); got[0] != "0" {
			t.Errorf("core_task_runs rows after marking = %s, want 0", got[0])
		}
		if got := mkDump(t, db, tableCount); got[0] != "1" {
			t.Errorf("core_task_runs tables after marking = %s, want the table kept", got[0])
		}
	})
}

func TestMkIsAtomic(t *testing.T) {
	cases := []struct{ name, trigger string }{
		{"the listener update fails", `CREATE TRIGGER mk_fail BEFORE UPDATE ON core_listeners BEGIN SELECT RAISE(ABORT, 'mk: injected failure'); END`},
		{"the lock delete fails", `CREATE TRIGGER mk_fail BEFORE DELETE ON core_locks BEGIN SELECT RAISE(ABORT, 'mk: injected failure'); END`},
	}
	tables := []string{"core_meta", "core_changes", "core_rollouts", "core_locks", "core_listeners"}
	dump := func(t *testing.T, db *store.DB) map[string][]string {
		out := map[string][]string{}
		for _, tbl := range tables {
			out[tbl] = mkDump(t, db, "SELECT * FROM "+tbl+" ORDER BY 1")
		}
		return out
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			db := fxDB(t, "")
			fxInsertChange(t, db, fxChange{ID: "c1", Action: "fixture.act", State: "applying", Owner: "engine"})
			fxInsertRollout(t, db, "r1", "running", "host-a")
			fxInsertListener(t, db, "l1", "open")
			fxInsertLock(t, db, "k.one", "c1")
			fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
			mkExec(t, db, c.trigger)
			before := dump(t, db)

			err := Mark(context.Background(), db, mkMarker())
			if err == nil || !strings.Contains(err.Error(), "injected failure") {
				t.Fatalf("Mark = %v, want the injected failure", err)
			}
			after := dump(t, db)
			for _, tbl := range tables {
				if !slices.Equal(after[tbl], before[tbl]) {
					t.Errorf("%s changed although Mark failed:\n got %v\nwant %v", tbl, after[tbl], before[tbl])
				}
			}
			if _, ok, err := quarantine.Read(context.Background(), db); err != nil || ok {
				t.Errorf("quarantine flag after a failed Mark = %v, %v; want unset", ok, err)
			}

			mkExec(t, db, `DROP TRIGGER mk_fail`)
			mkMark(t, db)
			if got := mkByID(mkDump(t, db, mkChangeQuery))["c1"]; !strings.Contains(got, "|quarantined|applying|") {
				t.Errorf("change after the retry = %s, want quarantined from applying", got)
			}
		})
	}
}

func TestMkWritesTheMarker(t *testing.T) {
	db := fxDB(t, "")
	ctx := context.Background()
	m := mkMarker()
	if err := Mark(ctx, db, m); err != nil {
		t.Fatal(err)
	}
	want, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	raw := mkDump(t, db, `SELECT value FROM core_meta WHERE key = 'restore_quarantine'`)
	if len(raw) != 1 || raw[0] != string(want) {
		t.Errorf("core_meta restore_quarantine = %v, want %s", raw, want)
	}
	got, ok, err := quarantine.Read(ctx, db)
	if err != nil || !ok || got.Tx != m.Tx || !got.RestoredAt.Equal(m.RestoredAt) || !got.BackupCreatedAt.Equal(m.BackupCreatedAt) {
		t.Errorf("quarantine.Read = %+v, %v, %v; want %+v", got, ok, err, m)
	}
	m.Tx = "01JTXOTHER0000000000000000"
	if err := Mark(ctx, db, m); err != nil {
		t.Fatalf("marking a marked database: %v", err)
	}
	if got, _, _ := quarantine.Read(ctx, db); got.Tx != m.Tx {
		t.Errorf("tx after a second Mark = %s, want %s", got.Tx, m.Tx)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/8 && export TMPDIR=/root/.cache/w7-tmp/8 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Mk' ./internal/controller/backup/`
Expected: FAIL at build time with `undefined: NonTerminal` and `undefined: Mark`.

- [ ] **Step 3: Implement**

1. In `internal/controller/backup/mark.go` declare `NonTerminal` as Contracts "Marking" gives it (the seven states of spec 1.3 "Locks across restarts" without `quarantined`, built from the `core.State*` constants, in the order the Contract lists) and an unexported time layout constant `markStampLayout = "2006-01-02T15:04:05.000000000Z"` (the lifecycle format). Give both a doc comment.
2. `Mark(ctx, db, m)` runs one `db.Write`; every statement below executes on its `*sql.Tx`, in this order, and any error returns from the callback wrapped as `backup: mark <what failed>: %w` so `db.Write` rolls the whole transaction back. Nothing is written outside the transaction.
   - Upsert `core_meta` key `quarantine.MetaKey` with `json.Marshal(m)` (`INSERT ... ON CONFLICT (key) DO UPDATE SET value = excluded.value`): a backup taken during an earlier quarantine carries an old marker, which the new one replaces.
   - `at := m.RestoredAt.UTC().Format(markStampLayout)`. `UPDATE core_changes SET quarantined_from = state, state = 'quarantined', recovery_owner = '', updated_at = at WHERE state IN (NonTerminal)`: bind the states as `?` parameters built from the slice, so the list has one source. Rows in a terminal state and rows already `quarantined` are not selected, so they keep their state, `quarantined_from` and `updated_at`.
   - If the table `core_task_runs` exists (look it up in `sqlite_master` inside the transaction; the controller schema has none, the agent schema does), `DELETE FROM core_task_runs`. Never create it.
   - `UPDATE core_rollouts SET quarantined_from = state, state = 'quarantined', updated_at = at WHERE state IN ('running', 'draining')` (use `core.RolloutRunning`, `core.RolloutDraining`, `core.RolloutQuarantined`). `outcome`, `error_*` and every `core_rollout_hosts` row stay as they are.
   - `UPDATE core_listeners SET state = 'closed', updated_at = at WHERE state = 'open'`; `revoked` and `closed` rows are untouched.
   - `DELETE FROM core_locks WHERE owner NOT IN (SELECT id FROM core_changes WHERE state = 'quarantined')`. It runs after the change update, so the changes just quarantined keep their locks, and an update reservation, a terminal change's lock and an orphan lock are deleted.
3. `Mark` does not touch `core_composites`, `core_composite_steps`, `core_effects`, `core_lock_epochs`, `core_plans` or any row the statements above do not select: a composite is quarantined as the `core_changes` row it is, and nothing is replayed, resumed or unwound. Wave 8 adds the exposure marking later (W7-R10); this task adds none.
4. Doc comment on `Mark`: it quarantines `db` in one transaction and why locks of quarantined records stay (a new change on their keys fails with `lock.held` naming the record).

- [ ] **Step 4: Run test to verify it passes, then the task's gates**

Run: `export TMPDIR=/root/.cache/w7-tmp/8 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Mk' -v ./internal/controller/backup/`
Expected: PASS for `TestMkNonTerminalIsTheSevenUnfinishedStates`, `TestMkQuarantinesEveryNonTerminalChangeOnly`, `TestMkLeavesCompositesAndTheirStepsUnwound`, `TestMkKeepsRecordsThatAreAlreadyQuarantined`, `TestMkRollouts` (six subtests), `TestMkClosesOnlyOpenListeners`, `TestMkDisposesOfLocksByOwner`, `TestMkTaskRuns` (absent, present), `TestMkIsAtomic` (two subtests), `TestMkWritesTheMarker`.

Then the gates of Global Constraints:

```bash
mkdir -p /root/.cache/w7-tmp/8 && export TMPDIR=/root/.cache/w7-tmp/8 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/8 && go clean -testcache
```

Expected: every command exits 0, `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, golangci-lint prints `0 issues.`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/mark.go internal/controller/backup/mark_test.go
git commit -m "feat(backup): add restore quarantine marking"
```

### Task 9: Quarantine list, enrollment token and release routes

**Files:**
- Create: `internal/controller/backup/quarantine_routes.go`
- Test: `internal/controller/backup/quarantine_routes_test.go`

**Interfaces:**
- Consumes: Contracts "Configuration and contribution" (Task 3: `Backup`, `Config`, `TokenIssuer`, `New`); Contracts "Controller database fixture" (Task 3: `fxDB`, `fxInsertChange`, `fxInsertComposite`, `fxInsertLock`, `fxInsertRollout`, `fxInsertHost`); Contracts "Schema" (Task 7); `quarantine.Read`, `quarantine.Marker`, `quarantine.MetaKey`; `ext.Deps` (`DB`, `Clock`, `Log`, `Audit`, `LocalHostID`); `errcode` codes already in the registry; `coretest.MemAudit`, `coretest.NewFakeClock` and `internal/controller/audit` in the tests.
- Produces: the wire types, `queryer`, `listRecords`, `recordByID`, `quarantineHandler`, `enrollTokenHandler` and `releaseHandler` exactly as Contracts "Quarantine routes" declares them. `recordByID` finds a record by id in `core_changes` or `core_rollouts` whenever its `quarantined_from` is not NULL, whatever its state now, so Task 13 can answer a record it has just resolved with `QuarantinedFrom` kept; `listRecords` returns only records in state `quarantined`.
- Produces: Contracts "Route helpers" (`maxRouteBody`, `writeProblem`, `writeJSON`, `decodeBody`, `recordAudit`) exactly as stated there, in `quarantine_routes.go`; Tasks 12 and 13 call them and declare no copy.
- Audit: Contracts "Audit" fixes the phases; this task writes `apply` for enroll-token and `release` for release.

**Items:** 69

**Depends on:** Task 3, Task 7
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

The helpers carry the prefix `ql`. `qlIssuer` writes a token row in the transaction it is given and records the host's state as that transaction sees it, so the test can tell whether the host was set to `pending` before `IssueToken` and whether a failure rolled both back. `TestQlAuditRowsFitTheAuditTable` writes through the real audit store, whose table restricts `phase` and `result`.

```go
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	qlAdvertise = "controller.example:7443"
	qlTx        = "01JTXQUARANTINE0000000000"
)

var qlExpiry = fxNow.Add(time.Hour)

func qlToken(host string) string { return "qltoken-secret-for-" + host }

// qlIssued is one IssueToken call as the fake issuer saw it.
type qlIssued struct{ host, address, stateInTx string }

// qlIssuer is a TokenIssuer that writes a token row in the transaction it is
// given and records the host's state as that transaction sees it.
type qlIssuer struct {
	mu    sync.Mutex
	calls []qlIssued
	err   error
}

func (i *qlIssuer) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	var state string
	if err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&state); err != nil {
		return "", time.Time{}, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO core_enroll_tokens (host_id, hash, expires_at, consumed_at, created_at)
		VALUES (?, x'00', ?, NULL, ?)`, hostID, qlExpiry.Format("2006-01-02T15:04:05.000000000Z"), "2026-11-04T12:00:00.000000000Z"); err != nil {
		return "", time.Time{}, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.calls = append(i.calls, qlIssued{hostID, address, state})
	if i.err != nil {
		return "", time.Time{}, i.err
	}
	return qlToken(hostID), qlExpiry, nil
}

func (i *qlIssuer) issued() []qlIssued {
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]qlIssued(nil), i.calls...)
}

// qlFailingAudit is a core.Audit whose Record always fails.
type qlFailingAudit struct{ *coretest.MemAudit }

func (qlFailingAudit) Record(context.Context, core.AuditEntry) error {
	return errors.New("audit store is down")
}

type qlEnv struct {
	db     *store.DB
	audit  *coretest.MemAudit
	issuer *qlIssuer
	deps   ext.Deps
	b      *Backup
}

// qlNew returns a migrated controller database with four hosts (host-local,
// host-enr enrolled, host-pen pending, host-rev revoked) and a Backup whose
// issuer and advertise address are set; the quarantine flag is not.
func qlNew(t *testing.T) *qlEnv {
	t.Helper()
	db := fxDB(t, "")
	fxInsertHost(t, db, "host-local", "enrolled", true)
	fxInsertHost(t, db, "host-enr", "enrolled", false)
	fxInsertHost(t, db, "host-pen", "pending", false)
	fxInsertHost(t, db, "host-rev", "revoked", false)
	e := &qlEnv{db: db, audit: &coretest.MemAudit{}, issuer: &qlIssuer{}}
	e.deps = ext.Deps{
		DB: db, Clock: coretest.NewFakeClock(fxNow), Audit: e.audit, LocalHostID: "host-local",
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	e.b = New(Config{Tokens: e.issuer, AgentAdvertise: qlAdvertise})
	return e
}

func qlExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func qlScalar(t *testing.T, db *store.DB, query string, args ...any) string {
	t.Helper()
	var s sql.NullString
	if err := db.Read().QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s.String
}

// qlDump renders every row of query as one string, columns joined by "|".
func qlDump(t *testing.T, db *store.DB, query string) []string {
	t.Helper()
	rows, err := db.Read().QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	cols, err := rows.Columns()
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for rows.Next() {
		vals := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			t.Fatal(err)
		}
		parts := make([]string, len(cols))
		for i, v := range vals {
			parts[i] = fmt.Sprint(v)
		}
		out = append(out, strings.Join(parts, "|"))
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

func qlActivate(t *testing.T, db *store.DB) {
	t.Helper()
	raw, err := json.Marshal(quarantine.Marker{Tx: qlTx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-time.Hour)})
	if err != nil {
		t.Fatal(err)
	}
	qlExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, quarantine.MetaKey, string(raw))
}

// qlQuarantine inserts a change already in state quarantined, remembering from.
func qlQuarantine(t *testing.T, db *store.DB, c fxChange, from string) {
	t.Helper()
	c.State = "quarantined"
	fxInsertChange(t, db, c)
	qlExec(t, db, `UPDATE core_changes SET quarantined_from = ? WHERE id = ?`, from, c.ID)
}

func qlCall(t *testing.T, h http.Handler, method, target, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// qlWantProblem checks an RFC 9457 answer; an empty detail is not compared.
func qlWantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code, detail string) {
	t.Helper()
	if rec.Code != status {
		t.Errorf("status = %d, want %d (body %s)", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Errorf("Content-Type = %q, want application/problem+json", ct)
	}
	var p struct {
		Status int          `json:"status"`
		Code   errcode.Code `json:"code"`
		Detail string       `json:"detail"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem body %q: %v", rec.Body, err)
	}
	if p.Status != status || p.Code != code {
		t.Errorf("problem = %+v, want status %d code %s", p, status, code)
	}
	if detail != "" && p.Detail != detail {
		t.Errorf("detail = %q, want %q", p.Detail, detail)
	}
}

func qlDecode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var v T
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("body %q: %v", rec.Body, err)
	}
	return v
}

func (e *qlEnv) auditRows(t *testing.T) []core.AuditEntry {
	t.Helper()
	rows, _, err := e.audit.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func (e *qlEnv) hostStates(t *testing.T) string {
	t.Helper()
	return strings.Join(qlDump(t, e.db, `SELECT id, state FROM core_hosts ORDER BY id`), ",")
}

// qlSeed inserts the records of the list test and returns what the list must answer.
func qlSeed(t *testing.T, db *store.DB) []Record {
	t.Helper()
	qlQuarantine(t, db, fxChange{ID: "a-change", HostID: "host-a", Action: "core.host.create"}, "applying")
	fxInsertLock(t, db, "net.b", "a-change")
	fxInsertLock(t, db, "net.a", "a-change")
	fxInsertRollout(t, db, "b-rollout", "quarantined", "host-a")
	qlExec(t, db, `UPDATE core_rollouts SET quarantined_from = 'running' WHERE id = 'b-rollout'`)
	fxInsertComposite(t, db, "c-composite", "quarantined", "forward", []fxStep{{ID: "s1", HostID: "host-a", Action: "fixture.one/v1", State: "applied"}})
	qlExec(t, db, `UPDATE core_changes SET quarantined_from = 'needs_repair' WHERE id = 'c-composite'`)
	qlQuarantine(t, db, fxChange{ID: "d-tailnet", Action: "tailnet.key.rotate"}, "recovering")
	qlQuarantine(t, db, fxChange{ID: "e-cloudflare", Action: "cloudflare.hostname.add"}, "applying")
	qlQuarantine(t, db, fxChange{ID: "f-core", Action: "core.host.revoke"}, "undoing")
	fxInsertLock(t, db, "dns.x", "f-core")
	// Not quarantined: none of these is listed, and their lock is nobody's record.
	fxInsertChange(t, db, fxChange{ID: "g-applying", Action: "core.host.create", State: "applying"})
	fxInsertLock(t, db, "other.key", "g-applying")
	fxInsertChange(t, db, fxChange{ID: "h-done", Action: "core.host.create", State: "completed"})
	fxInsertRollout(t, db, "i-running", "running", "host-a")
	fxInsertRollout(t, db, "j-done", "succeeded", "host-a")
	return []Record{
		{ID: "a-change", Kind: "change", Action: "core.host.create", HostID: "host-a", QuarantinedFrom: "applying", Locks: []string{"net.a", "net.b"}},
		{ID: "b-rollout", Kind: "rollout", Action: "core.update.rollout", HostID: "", QuarantinedFrom: "running", Locks: []string{}},
		{ID: "c-composite", Kind: "composite", Action: qlScalar(t, db, `SELECT action FROM core_changes WHERE id = 'c-composite'`),
			HostID: qlScalar(t, db, `SELECT host_id FROM core_changes WHERE id = 'c-composite'`), QuarantinedFrom: "needs_repair", Locks: []string{}},
		{ID: "d-tailnet", Kind: "cloud", Action: "tailnet.key.rotate", QuarantinedFrom: "recovering", Locks: []string{}},
		{ID: "e-cloudflare", Kind: "cloud", Action: "cloudflare.hostname.add", QuarantinedFrom: "applying", Locks: []string{}},
		{ID: "f-core", Kind: "change", Action: "core.host.revoke", QuarantinedFrom: "undoing", Locks: []string{"dns.x"}},
	}
}

func TestQlListRecordsKindsAndLocks(t *testing.T) {
	e := qlNew(t)
	want := qlSeed(t, e.db)
	ctx := context.Background()

	got, err := listRecords(ctx, e.db.Read())
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("listRecords(*sql.DB) = %+v, %v\nwant %+v", got, err, want)
	}
	err = e.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		got, err = listRecords(ctx, tx)
		return err
	})
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("listRecords(*sql.Tx) = %+v, %v\nwant %+v", got, err, want)
	}

	qlActivate(t, e.db)
	rec := qlCall(t, e.b.quarantineHandler(e.deps), http.MethodGet, "/api/v1/controller/quarantine", "")
	st := qlDecode[Status](t, rec)
	if !st.Active || st.Tx != qlTx || st.RestoredAt == nil || !st.RestoredAt.Equal(fxNow) {
		t.Errorf("status = %+v, want active with tx %s restored at %s", st, qlTx, fxNow)
	}
	if !reflect.DeepEqual(st.Records, want) {
		t.Errorf("records:\n got %+v\nwant %+v", st.Records, want)
	}
	if !strings.Contains(rec.Body.String(), `"locks":[]`) || strings.Contains(rec.Body.String(), `"locks":null`) {
		t.Errorf("a record without locks must answer \"locks\":[]: %s", rec.Body)
	}
}

func TestQlStatusWhenNotActive(t *testing.T) {
	e := qlNew(t)
	h := e.b.quarantineHandler(e.deps)

	rec := qlCall(t, h, http.MethodGet, "/api/v1/controller/quarantine", "")
	if got := rec.Body.String(); got != `{"active":false,"records":[]}` {
		t.Errorf("with nothing quarantined: %s, want {\"active\":false,\"records\":[]}", got)
	}

	qlQuarantine(t, e.db, fxChange{ID: "kept", Action: "core.host.create"}, "pending")
	fxInsertLock(t, e.db, "net.kept", "kept")
	rec = qlCall(t, h, http.MethodGet, "/api/v1/controller/quarantine", "")
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if _, ok := raw["tx"]; ok {
		t.Errorf("an inactive status carries tx: %s", rec.Body)
	}
	if _, ok := raw["restoredAt"]; ok {
		t.Errorf("an inactive status carries restoredAt: %s", rec.Body)
	}
	st := qlDecode[Status](t, rec)
	want := []Record{{ID: "kept", Kind: "change", Action: "core.host.create", QuarantinedFrom: "pending", Locks: []string{"net.kept"}}}
	if st.Active || !reflect.DeepEqual(st.Records, want) {
		t.Errorf("status = %+v, want inactive with the kept record %+v", st, want)
	}
}

func TestQlRecordByID(t *testing.T) {
	e := qlNew(t)
	want := qlSeed(t, e.db)
	ctx := context.Background()
	q := e.db.Read()

	for _, w := range want {
		got, ok, err := recordByID(ctx, q, w.ID)
		if err != nil || !ok || !reflect.DeepEqual(got, w) {
			t.Errorf("recordByID(%s) = %+v, %v, %v; want %+v", w.ID, got, ok, err, w)
		}
	}
	for _, id := range []string{"g-applying", "h-done", "i-running", "j-done", "no-such-record"} {
		if got, ok, err := recordByID(ctx, q, id); err != nil || ok {
			t.Errorf("recordByID(%s) = %+v, %v, %v; want not found (it was never quarantined)", id, got, ok, err)
		}
	}

	qlExec(t, e.db, `UPDATE core_changes SET state = 'needs_repair' WHERE id = 'a-change'`)
	got, ok, err := recordByID(ctx, q, "a-change")
	if err != nil || !ok || got.QuarantinedFrom != "applying" || got.Kind != "change" || !reflect.DeepEqual(got.Locks, []string{"net.a", "net.b"}) {
		t.Errorf("recordByID of a resolved record = %+v, %v, %v; want it found with quarantinedFrom kept", got, ok, err)
	}
	listed, err := listRecords(ctx, q)
	if err != nil || len(listed) != len(want)-1 {
		t.Errorf("listRecords after resolving one = %d records, %v; want %d", len(listed), err, len(want)-1)
	}
}

func TestQlEnrollTokenRefusals(t *testing.T) {
	long := strings.Repeat(" ", 64<<10)
	cases := []struct {
		name    string
		prepare func(e *qlEnv)
		body    string
		status  int
		code    errcode.Code
		detail  string
		active  bool
	}{
		{"not in quarantine", nil, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"the controller is not in restore quarantine; use darkpanel host token", false},
		{"unknown host", nil, `{"hostId":"host-nope"}`, 404, errcode.NotFound, "", true},
		{"the local host", nil, `{"hostId":"host-local"}`, 422, errcode.InvalidInput, "", true},
		{"no token issuer", func(e *qlEnv) { e.b = New(Config{AgentAdvertise: qlAdvertise}) }, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"start the controller with --agent-advertise to issue enrollment tokens", true},
		{"no advertise address", func(e *qlEnv) { e.b = New(Config{Tokens: e.issuer}) }, `{"hostId":"host-enr"}`, 422, errcode.InvalidInput,
			"start the controller with --agent-advertise to issue enrollment tokens", true},
		{"empty host id", nil, `{"hostId":""}`, 422, errcode.InvalidInput, "", true},
		{"no host id", nil, `{}`, 422, errcode.InvalidInput, "", true},
		{"unknown member", nil, `{"hostId":"host-enr","extra":1}`, 422, errcode.InvalidInput, "", true},
		{"two JSON values", nil, `{"hostId":"host-enr"} {}`, 422, errcode.InvalidInput, "", true},
		{"not JSON", nil, `host-enr`, 422, errcode.InvalidInput, "", true},
		{"empty body", nil, ``, 422, errcode.InvalidInput, "", true},
		{"body over 64 KiB", nil, `{"hostId":"host-enr"}` + long, 422, errcode.InvalidInput, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := qlNew(t)
			if c.active {
				qlActivate(t, e.db)
			}
			if c.prepare != nil {
				c.prepare(e)
			}
			hostsBefore := e.hostStates(t)
			rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", c.body)
			qlWantProblem(t, rec, c.status, c.code, c.detail)
			if n := len(e.issuer.issued()); n != 0 {
				t.Errorf("the issuer was called %d times", n)
			}
			if rows := e.auditRows(t); len(rows) != 0 {
				t.Errorf("audit rows = %+v, want none for a refusal", rows)
			}
			if got := e.hostStates(t); got != hostsBefore {
				t.Errorf("host states changed on a refusal: %s, was %s", got, hostsBefore)
			}
		})
	}
}

func TestQlEnrollTokenIssuesInTheHostsTransaction(t *testing.T) {
	cases := []struct{ host, stateInTx, stateAfter string }{
		{"host-enr", "pending", "pending"},
		{"host-pen", "pending", "pending"},
		{"host-rev", "revoked", "revoked"},
	}
	for _, c := range cases {
		t.Run(c.host, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			others := map[string]string{"host-local": "enrolled", "host-enr": "enrolled", "host-pen": "pending", "host-rev": "revoked"}
			others[c.host] = c.stateAfter
			var want []string
			for _, id := range []string{"host-enr", "host-local", "host-pen", "host-rev"} {
				want = append(want, id+"|"+others[id])
			}

			rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"`+c.host+`"}`)
			res := qlDecode[EnrollTokenResult](t, rec)
			if res.Token != qlToken(c.host) || !res.ExpiresAt.Equal(qlExpiry) {
				t.Errorf("answer = %+v, want the issuer's token and expiry %s", res, qlExpiry)
			}
			if got := e.issuer.issued(); !reflect.DeepEqual(got, []qlIssued{{c.host, qlAdvertise, c.stateInTx}}) {
				t.Errorf("issuer calls = %+v, want one for %s at %s seeing the host %s", got, c.host, qlAdvertise, c.stateInTx)
			}
			if got := qlDump(t, e.db, `SELECT id, state FROM core_hosts ORDER BY id`); !reflect.DeepEqual(got, want) {
				t.Errorf("host states = %v, want %v", got, want)
			}
			if n := qlScalar(t, e.db, `SELECT count(*) FROM core_enroll_tokens WHERE host_id = ?`, c.host); n != "1" {
				t.Errorf("token rows of %s = %s, want 1", c.host, n)
			}

			rows := e.auditRows(t)
			if len(rows) != 1 {
				t.Fatalf("audit rows = %+v, want exactly one", rows)
			}
			r := rows[0]
			if r.Actor != "local:root@host-local" || r.Action != "controller.quarantine.enroll-token" || r.HostID != c.host ||
				r.Phase != "apply" || r.Result != "ok" || r.ChangeID != "" || len(r.Input) != 0 {
				t.Errorf("audit row = %+v", r)
			}
			if b, _ := json.Marshal(rows); strings.Contains(string(b), qlToken(c.host)) {
				t.Errorf("the audit row carries the token: %s", b)
			}
		})
	}
}

func TestQlEnrollTokenIssuerFailureRollsBack(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	e.issuer.err = errors.New("issuer: secret backend exploded")
	hostsBefore := e.hostStates(t)

	rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	qlWantProblem(t, rec, http.StatusInternalServerError, errcode.Internal, "")
	if strings.Contains(rec.Body.String(), "exploded") {
		t.Errorf("the issuer's error reached the client: %s", rec.Body)
	}
	if got := e.issuer.issued(); len(got) != 1 || got[0].stateInTx != "pending" {
		t.Errorf("issuer calls = %+v, want one that saw the host pending", got)
	}
	if got := e.hostStates(t); got != hostsBefore {
		t.Errorf("host states after the issuer failed = %s, want %s (the pending change rolled back)", got, hostsBefore)
	}
	if n := qlScalar(t, e.db, `SELECT count(*) FROM core_enroll_tokens`); n != "0" {
		t.Errorf("token rows = %s, want 0 (the issuer's write rolled back)", n)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none", rows)
	}
}

func TestQlReleaseRefusesWhileRecordsAreQuarantined(t *testing.T) {
	for _, body := range []string{`{}`, `{"keepUnresolved":false}`} {
		t.Run(body, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
			fxInsertRollout(t, e.db, "q2", "quarantined")
			rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
			qlWantProblem(t, rec, http.StatusConflict, errcode.Quarantined,
				"2 records are still quarantined; resolve each one, or release with --keep-unresolved")
			if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
				t.Errorf("quarantine active = %v, %v after a refused release; want still set", active, err)
			}
			if rows := e.auditRows(t); len(rows) != 0 {
				t.Errorf("audit rows = %+v, want none for a refusal", rows)
			}
		})
	}
}

func TestQlReleaseKeepUnresolvedNamesTheHeldLocks(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
	qlQuarantine(t, e.db, fxChange{ID: "q2", Action: "core.host.revoke"}, "needs_repair")
	fxInsertLock(t, e.db, "net.b", "q1")
	fxInsertLock(t, e.db, "net.a", "q1")
	fxInsertLock(t, e.db, "dns.x", "q2")

	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{"keepUnresolved":true}`)
	res := qlDecode[ReleaseResult](t, rec)
	want := []HeldLock{{Key: "dns.x", Record: "q2"}, {Key: "net.a", Record: "q1"}, {Key: "net.b", Record: "q1"}}
	if !reflect.DeepEqual(res.HeldLocks, want) {
		t.Errorf("held locks = %+v, want %+v", res.HeldLocks, want)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v after release; want cleared", active, err)
	}
	if got := qlDump(t, e.db, `SELECT id, state FROM core_changes ORDER BY id`); !reflect.DeepEqual(got, []string{"q1|quarantined", "q2|quarantined"}) {
		t.Errorf("records after --keep-unresolved = %v, want both still quarantined", got)
	}
	if got := qlDump(t, e.db, `SELECT key FROM core_locks ORDER BY key`); len(got) != 3 {
		t.Errorf("locks after release = %v, want all three kept", got)
	}
	rows := e.auditRows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %+v, want exactly one", rows)
	}
	if r := rows[0]; r.Actor != "local:root@host-local" || r.Action != "controller.quarantine.release" || r.Phase != "release" ||
		r.Result != "ok" || r.HostID != "" || r.ChangeID != "" || len(r.Input) != 0 {
		t.Errorf("audit row = %+v", r)
	}
}

func TestQlReleaseWithNothingToResolve(t *testing.T) {
	e := qlNew(t)
	qlActivate(t, e.db)
	fxInsertChange(t, e.db, fxChange{ID: "done", Action: "core.host.create", State: "completed"})
	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`)
	if rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != `{"heldLocks":[]}` {
		t.Errorf("release = %d %s, want 200 {\"heldLocks\":[]}", rec.Code, rec.Body)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v after release; want cleared", active, err)
	}
	if rows := e.auditRows(t); len(rows) != 1 {
		t.Errorf("audit rows = %+v, want one", rows)
	}
}

func TestQlReleaseWhenNotActiveChangesNothing(t *testing.T) {
	e := qlNew(t)
	qlQuarantine(t, e.db, fxChange{ID: "q1", Action: "core.host.create"}, "applying")
	fxInsertLock(t, e.db, "net.a", "q1")
	for _, body := range []string{`{}`, `{"keepUnresolved":true}`} {
		rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
		res := qlDecode[ReleaseResult](t, rec)
		if !reflect.DeepEqual(res.HeldLocks, []HeldLock{{Key: "net.a", Record: "q1"}}) {
			t.Errorf("body %s: held locks = %+v, want the quarantined record's lock", body, res.HeldLocks)
		}
	}
	if got := qlDump(t, e.db, `SELECT id, state FROM core_changes`); !reflect.DeepEqual(got, []string{"q1|quarantined"}) {
		t.Errorf("records = %v, want unchanged", got)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("audit rows = %+v, want none: nothing was released", rows)
	}
}

func TestQlReleaseRefusesBadBodies(t *testing.T) {
	for name, body := range map[string]string{
		"unknown member": `{"keepUnresolved":true,"force":true}`,
		"not JSON":       `release`,
		"two values":     `{} {}`,
		"empty":          ``,
		"over 64 KiB":    `{}` + strings.Repeat(" ", 64<<10),
	} {
		t.Run(name, func(t *testing.T) {
			e := qlNew(t)
			qlActivate(t, e.db)
			rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", body)
			qlWantProblem(t, rec, http.StatusUnprocessableEntity, errcode.InvalidInput, "")
			if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
				t.Errorf("quarantine active = %v, %v; want still set", active, err)
			}
		})
	}
}

func TestQlAnAuditFailureDoesNotHideTheResult(t *testing.T) {
	e := qlNew(t)
	e.deps.Audit = qlFailingAudit{e.audit}
	qlActivate(t, e.db)
	rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`)
	if rec.Code != http.StatusOK {
		t.Errorf("release = %d %s, want 200: the release committed", rec.Code, rec.Body)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("quarantine active = %v, %v; want cleared", active, err)
	}
	qlActivate(t, e.db)
	rec = qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-enr"}`)
	if res := qlDecode[EnrollTokenResult](t, rec); res.Token != qlToken("host-enr") {
		t.Errorf("enroll-token answer = %+v, want the token: it is shown once", res)
	}
}

// TestQlAuditRowsFitTheAuditTable writes the rows through the real audit
// store, whose table restricts phase and result.
func TestQlAuditRowsFitTheAuditTable(t *testing.T) {
	e := qlNew(t)
	log := audit.New(e.db, e.deps.Clock)
	e.deps.Audit = log
	qlActivate(t, e.db)
	if rec := qlCall(t, e.b.enrollTokenHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/enroll-token", `{"hostId":"host-pen"}`); rec.Code != http.StatusOK {
		t.Fatalf("enroll-token = %d %s", rec.Code, rec.Body)
	}
	if rec := qlCall(t, e.b.releaseHandler(e.deps), http.MethodPost, "/api/v1/controller/quarantine/release", `{}`); rec.Code != http.StatusOK {
		t.Fatalf("release = %d %s", rec.Code, rec.Body)
	}
	rows, _, err := log.List(context.Background(), core.AuditQuery{})
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, r := range rows {
		actions = append(actions, fmt.Sprintf("%s/%s/%s", r.Action, r.Phase, r.Result))
	}
	want := []string{"controller.quarantine.release/release/ok", "controller.quarantine.enroll-token/apply/ok"}
	if !reflect.DeepEqual(actions, want) {
		t.Errorf("stored audit rows = %v, want %v (newest first)", actions, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/9 && export TMPDIR=/root/.cache/w7-tmp/9 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Ql' ./internal/controller/backup/`
Expected: FAIL at build time with `undefined: Status` (then `Record`, `ReleaseResult`, `listRecords`, and the three handler methods).

- [ ] **Step 3: Implement**

1. `quarantine_routes.go`: declare the wire types of Contracts "Quarantine routes" (`Record`, `Status`, `ResolveRequest`, `EnrollTokenRequest`, `EnrollTokenResult`, `ReleaseRequest`, `HeldLock`, `ReleaseResult`) with the JSON tags the Contract gives and a doc comment each, the `queryer` interface, and the shared helpers listed under Interfaces. `ResolveRequest` is used by Task 13 only.
2. `listRecords(ctx, q)` reads, through `q`, the `core_changes` rows and the `core_rollouts` rows whose state is `quarantined`, and every `core_locks` row (key and owner). A change is `composite` when its `class` is `composite` (`core.ClassComposite`), else `cloud` when the part of its `action` before the first `.` is `tailnet` or `cloudflare`, else `change`. A rollout is `rollout` with `Action` `core.update.rollout` and an empty `HostID`; a change's `HostID` is its stored `host_id`. `QuarantinedFrom` is `quarantined_from` (empty when NULL). `Locks` is the sorted keys whose owner is the record's id, a non-nil empty slice when none (the JSON must say `[]`, never `null`). The result is sorted by `ID` across both tables and is non-nil when empty. `recordByID(ctx, q, id)` builds the same `Record` for a row whose `quarantined_from` is not NULL, whatever its state, from `core_changes` first and then `core_rollouts`; it returns `false` for any other id.
3. `quarantineHandler(deps)`: `Status{Active, Tx, RestoredAt}` from `quarantine.Read(ctx, deps.DB)` (the three fields stay zero, and `tx` and `restoredAt` are omitted from the JSON, when the flag is not set) and `Records` from `listRecords(ctx, deps.DB.Read())`, whether or not the flag is set: records kept by `release --keep-unresolved` still show. Answer 200 `application/json`. A read error is a plain error (500).
4. `enrollTokenHandler(deps)` (W7-R8), in this order, each refusal an `*errcode.Error` and no audit row on any refusal:
   - `decodeBody` into `EnrollTokenRequest`; an empty `hostId` is `request.invalid_input`.
   - The flag is not set (`quarantine.Active`): `request.invalid_input` "the controller is not in restore quarantine; use darkpanel host token".
   - `b.cfg.Tokens == nil` or `b.cfg.AgentAdvertise == ""`: `request.invalid_input` "start the controller with --agent-advertise to issue enrollment tokens".
   - One `deps.DB.Write`: read the host's `is_local` and `state` through the `*sql.Tx` (no row: `not_found` "no host <id>"; local: `request.invalid_input` naming the host and saying the local host is enrolled with `core.host.enroll-local` over the controller socket); when the state is `enrolled`, `UPDATE core_hosts SET state = 'pending'` (`core.HostPending`) in the same transaction (`fleet.Authority.Enroll` accepts only `pending` or `revoked`); a `pending` or `revoked` host keeps its state; then `b.cfg.Tokens.IssueToken(ctx, tx, hostID, b.cfg.AgentAdvertise)`. Return its error unchanged so the transaction, the state change and the issuer's own writes roll back together; a plain error becomes a 500 `internal` whose cause is only logged.
   - After the commit, `recordAudit(ctx, deps, "controller.quarantine.enroll-token", "apply", "", hostID)`, then 200 with `EnrollTokenResult{Token, ExpiresAt.UTC()}` where `Token` is the string `IssueToken` returned. The audit row carries neither the token nor any input.
5. `releaseHandler(deps)` (W7-R9): `decodeBody` into `ReleaseRequest`, then one `deps.DB.Write` that uses only its `*sql.Tx` (the reader pool does not see the transaction's writes): read whether `core_meta` has `quarantine.MetaKey`; `listRecords(ctx, tx)`; when the flag is set, records exist and `KeepUnresolved` is false, return `core.quarantined` (`errcode.Quarantined`) "<n> records are still quarantined; resolve each one, or release with --keep-unresolved" with `n` the record count (the text says "records" for every n); otherwise, when the flag is set, delete the `core_meta` row; build `HeldLocks` from every listed record's locks as `{Key, Record: id}` sorted by key, `[]HeldLock{}` when none. When the flag was not set the call changes nothing and answers the same held locks (idempotent). After the commit, only when this call deleted the flag, `recordAudit(ctx, deps, "controller.quarantine.release", "release", "", "")`. Answer 200.
6. Doc comments on every exported type and a comment only where the why is not obvious (the single transaction in release, `recordByID` ignoring the state).

- [ ] **Step 4: Run test to verify it passes, then the task's gates**

Run: `export TMPDIR=/root/.cache/w7-tmp/9 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Ql' -v ./internal/controller/backup/`
Expected: PASS for every `TestQl*` test and subtest (list kinds and locks, status when not active, `recordByID`, twelve enroll-token refusals, three enroll-token issues, the issuer failure, release refusals, `--keep-unresolved`, nothing to resolve, not active, bad bodies, audit failure, audit table).

Then the gates of Global Constraints:

```bash
mkdir -p /root/.cache/w7-tmp/9 && export TMPDIR=/root/.cache/w7-tmp/9 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/9 && go clean -testcache
```

Expected: every command exits 0, `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, golangci-lint prints `0 issues.`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/quarantine_routes.go internal/controller/backup/quarantine_routes_test.go
git commit -m "feat(backup): add quarantine routes"
```

### Task 10: Quarantine CLI commands

**Files:**
- Create: `internal/cli/controller_quarantine.go`
- Test: `internal/cli/controller_quarantine_test.go`

**Interfaces:**
- Consumes: the wire types of Contracts "Quarantine routes", redeclared here as unexported structs because `internal/cli` does not import the backup package (`quarantineRecord`, `quarantineStatus`, `quarantineHeldLock`, with the same JSON names; the request bodies are small maps or structs); in `internal/cli`: `addConnFlags`, `(*app).connect`, `(*client).do`, `writeTable`, `sanitize`, `usagef`; in the tests `cliEnv`, `newFakeSocket`, `exchange` and `jsonEqual`.
- Produces: `func (a *app) quarantineCommand() *cobra.Command` as Contracts "CLI" declares it. This task does not edit `controller_backup.go`: Task 17 makes `backupCommands` return `a.quarantineCommand()`, so `darkpanel controller quarantine` exists only after Task 17. The tests mount the group as a top-level `quarantine` command through `cliEnv.commands`.

**Items:** 69

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 1 = 5

- [ ] **Step 1: Write the failing test**

`cqFields` splits the table output into words per line so the assertions do not depend on column padding. Every case that must not reach the controller uses `newFakeSocket(t)` with no exchange, which fails the test on any request.

```go
package cli

import (
	"net/http"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cqRun runs the quarantine group as a top-level command, so the tests do not
// depend on where the assembly mounts it.
func cqRun(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	return cliEnv{commands: []*cobra.Command{(&app{}).quarantineCommand()}}.run(t, args...)
}

// cqFields splits out into lines of whitespace-separated words, so a test does
// not depend on column padding.
func cqFields(out string) [][]string {
	var lines [][]string
	for _, l := range strings.Split(strings.TrimRight(out, "\n"), "\n") {
		lines = append(lines, strings.Fields(l))
	}
	return lines
}

func cqSameLines(got, want [][]string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if strings.Join(got[i], "\x00") != strings.Join(want[i], "\x00") {
			return false
		}
	}
	return true
}

const (
	cqBase    = "/api/v1/controller/quarantine"
	cqRecordC = `{"id":"c1","kind":"change","action":"core.host.create","hostId":"h1","quarantinedFrom":"applying","locks":["net.a","net.b"]}`
	cqRecordR = `{"id":"r1","kind":"rollout","action":"core.update.rollout","hostId":"","quarantinedFrom":"running","locks":[]}`
	cqActive  = `"active":true,"tx":"01JTX","restoredAt":"2026-11-04T12:00:00Z"`
	cqHeader  = "Restore quarantine is active: restore 01JTX, restored 2026-11-04T12:00:00Z.\n"
)

func TestCqList(t *testing.T) {
	table := [][]string{
		{"ID", "KIND", "ACTION", "HOST", "FROM", "LOCKS"},
		{"c1", "change", "core.host.create", "h1", "applying", "net.a,net.b"},
		{"r1", "rollout", "core.update.rollout", "-", "running", "-"},
	}
	records := `"records":[` + cqRecordC + `,` + cqRecordR + `]`
	cases := []struct {
		name, reply, head string
		table             [][]string
		tail              string
	}{
		{"active with records", `{` + cqActive + `,` + records + `}`, cqHeader, table, ""},
		{"active without records", `{` + cqActive + `,"records":[]}`, cqHeader, nil, "No records are quarantined.\n"},
		{"kept records after release", `{"active":false,` + records + `}`, "The controller is not in restore quarantine.\n", table, ""},
		{"nothing", `{"active":false,"records":[]}`, "The controller is not in restore quarantine.\n", nil, "No records are quarantined.\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: c.reply})
			out, errOut, code := cqRun(t, "quarantine", "list", "--socket", sock)
			if code != 0 || errOut != "" {
				t.Fatalf("exit %d, err %q", code, errOut)
			}
			rest, ok := strings.CutPrefix(out, c.head)
			if !ok {
				t.Fatalf("output %q does not start with %q", out, c.head)
			}
			if c.table == nil {
				if rest != c.tail {
					t.Errorf("after the heading: %q, want %q", rest, c.tail)
				}
				return
			}
			if got := cqFields(rest); !cqSameLines(got, c.table) {
				t.Errorf("table = %v, want %v", got, c.table)
			}
		})
	}
}

func TestCqListJSONPrintsTheAnswerAsSent(t *testing.T) {
	reply := `{` + cqActive + `,"records":[` + cqRecordC + `],"future":1}`
	sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: reply})
	out, errOut, code := cqRun(t, "quarantine", "list", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqListNeverWritesServerControlCharacters(t *testing.T) {
	reply := `{"active":false,"records":[{"id":"c1","kind":"change","action":"evil\u001b[2Jaction","hostId":"","quarantinedFrom":"applying","locks":[]}]}`
	sock := newFakeSocket(t, exchange{method: "GET", path: cqBase, reply: reply})
	out, _, code := cqRun(t, "quarantine", "list", "--socket", sock)
	if code != 0 || strings.Contains(out, "\x1b") || !strings.Contains(out, `evil\x1b[2Jaction`) {
		t.Errorf("exit %d, out %q; want the escape written as text", code, out)
	}
}

func TestCqResolve(t *testing.T) {
	answer := func(id, kind string) string {
		return `{"id":"` + id + `","kind":"` + kind + `","action":"x","hostId":"","quarantinedFrom":"applying","locks":[]}`
	}
	cases := []struct {
		args    []string
		path    string
		body    string
		reply   string
		wantOut string
	}{
		{[]string{"c1", "done"}, cqBase + "/c1/resolve", `{"outcome":"done"}`, answer("c1", "change"),
			"Resolved change c1 as done; its locks are released.\n"},
		{[]string{"k1", "never"}, cqBase + "/k1/resolve", `{"outcome":"never"}`, answer("k1", "composite"),
			"Resolved composite k1 as never; its locks are released.\n"},
		{[]string{"k1", "repair"}, cqBase + "/k1/resolve", `{"outcome":"repair"}`, answer("k1", "composite"),
			"Resolved composite k1 as repair; its locks stay held.\nRepair it after release: darkpanel change repair k1\n"},
		{[]string{"a/b", "done"}, cqBase + "/a%2Fb/resolve", `{"outcome":"done"}`, answer("a/b", "change"),
			"Resolved change a/b as done; its locks are released.\n"},
	}
	for _, c := range cases {
		t.Run(strings.Join(c.args, " "), func(t *testing.T) {
			sock := newFakeSocket(t, exchange{method: "POST", path: c.path, body: c.body, reply: c.reply})
			out, errOut, code := cqRun(t, append([]string{"quarantine", "resolve"}, append(c.args, "--socket", sock)...)...)
			if code != 0 || errOut != "" || out != c.wantOut {
				t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, c.wantOut, errOut)
			}
		})
	}
	reply := answer("c1", "change")
	sock := newFakeSocket(t, exchange{method: "POST", path: cqBase + "/c1/resolve", body: `{"outcome":"repair"}`, reply: reply})
	out, errOut, code := cqRun(t, "quarantine", "resolve", "c1", "repair", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqEnrollToken(t *testing.T) {
	reply := `{"token":"dpt_secret_value","expiresAt":"2026-11-04T13:00:00Z"}`
	ex := exchange{method: "POST", path: cqBase + "/enroll-token", body: `{"hostId":"web-1"}`, reply: reply}
	sock := newFakeSocket(t, ex, ex)
	out, errOut, code := cqRun(t, "quarantine", "enroll-token", "web-1", "--socket", sock)
	want := "Enrollment token for host web-1 (valid until 2026-11-04T13:00:00Z, shown once):\ndpt_secret_value\n"
	if code != 0 || errOut != "" || out != want {
		t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	out, errOut, code = cqRun(t, "quarantine", "enroll-token", "web-1", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(reply)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqRelease(t *testing.T) {
	held := `{"heldLocks":[{"key":"dns.x","record":"q2"},{"key":"net.a","record":"q1"}]}`
	sock := newFakeSocket(t,
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":false}`, reply: `{"heldLocks":[]}`},
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":true}`, reply: held},
		exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":true}`, reply: held},
	)
	out, errOut, code := cqRun(t, "quarantine", "release", "--socket", sock)
	if want := "Restore quarantine released. No locks stay held.\n"; code != 0 || errOut != "" || out != want {
		t.Errorf("plain release: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}

	out, errOut, code = cqRun(t, "quarantine", "release", "--keep-unresolved", "--socket", sock)
	head := "Restore quarantine released. These locks stay held by quarantined records:\n"
	rest, ok := strings.CutPrefix(out, head)
	wantTable := [][]string{{"KEY", "RECORD"}, {"dns.x", "q2"}, {"net.a", "q1"}}
	if code != 0 || errOut != "" || !ok || !cqSameLines(cqFields(rest), wantTable) {
		t.Errorf("--keep-unresolved: exit %d\nout %q\nwant %q then %v\nerr %q", code, out, head, wantTable, errOut)
	}

	out, errOut, code = cqRun(t, "quarantine", "release", "--keep-unresolved", "--json", "--socket", sock)
	if code != 0 || errOut != "" || !jsonEqual([]byte(out), []byte(held)) {
		t.Errorf("--json: exit %d, out %q, err %q; want the answer unchanged", code, out, errOut)
	}
}

func TestCqArgumentsAreCheckedBeforeAnyRequest(t *testing.T) {
	cases := []struct {
		name string
		args []string
		want string // in the error, when set
	}{
		{"outcome not one of the three", []string{"resolve", "c1", "maybe"}, `the outcome is done, never or repair, not "maybe"`},
		{"outcome in the wrong case", []string{"resolve", "c1", "DONE"}, `not "DONE"`},
		{"no outcome", []string{"resolve", "c1"}, ""},
		{"no record", []string{"resolve"}, ""},
		{"a third argument", []string{"resolve", "c1", "done", "extra"}, ""},
		{"an empty record id", []string{"resolve", "", "done"}, "record"},
		{"enroll-token without a host", []string{"enroll-token"}, ""},
		{"enroll-token with two hosts", []string{"enroll-token", "a", "b"}, ""},
		{"enroll-token with an empty host", []string{"enroll-token", ""}, "host"},
		{"list with an argument", []string{"list", "extra"}, ""},
		{"release with an argument", []string{"release", "extra"}, ""},
		{"release with an unknown flag", []string{"release", "--force"}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t) // any request fails the test
			_, errOut, code := cqRun(t, append(append([]string{"quarantine"}, c.args...), "--socket", sock)...)
			if code != ExitUsage {
				t.Errorf("exit %d, err %q; want %d", code, errOut, ExitUsage)
			}
			if c.want != "" && !strings.Contains(errOut, c.want) {
				t.Errorf("err %q does not contain %q", errOut, c.want)
			}
		})
	}
	if out, errOut, code := cqRun(t, "quarantine"); code != 0 || !strings.Contains(out, "list") || errOut != "" {
		t.Errorf("bare quarantine: exit %d, out %q, err %q; want its help", code, out, errOut)
	}
	if _, errOut, code := cqRun(t, "quarantine", "bogus"); code != ExitUsage || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Errorf("quarantine bogus: exit %d, err %q; want a usage error naming the command", code, errOut)
	}
}

func TestCqPrintsAProblemAnswerLikeOtherCommands(t *testing.T) {
	cases := []struct {
		name    string
		args    []string
		ex      exchange
		wantErr string
	}{
		{"release refused", []string{"release"},
			exchange{method: "POST", path: cqBase + "/release", body: `{"keepUnresolved":false}`, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"2 records are still quarantined; resolve each one, or release with --keep-unresolved","code":"core.quarantined"}`},
			"darkpanel: 2 records are still quarantined; resolve each one, or release with --keep-unresolved (core.quarantined)\n"},
		{"resolve busy", []string{"resolve", "k1", "done"},
			exchange{method: "POST", path: cqBase + "/k1/resolve", body: `{"outcome":"done"}`, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"step s1 is still running","code":"core.change_busy"}`},
			"darkpanel: step s1 is still running (core.change_busy)\n"},
		{"enroll-token with a field error", []string{"enroll-token", "web-1"},
			exchange{method: "POST", path: cqBase + "/enroll-token", body: `{"hostId":"web-1"}`, status: http.StatusUnprocessableEntity, problem: true,
				reply: `{"status":422,"detail":"not valid","code":"request.invalid_input","errors":[{"path":"/hostId","message":"is the local host"}]}`},
			"darkpanel: not valid (request.invalid_input)\n  /hostId: is the local host\n"},
		{"control characters in the detail", []string{"list"},
			exchange{method: "GET", path: cqBase, status: http.StatusConflict, problem: true,
				reply: `{"status":409,"detail":"held\u001b[31m","code":"core.quarantined"}`},
			`darkpanel: held\x1b[31m (core.quarantined)` + "\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sock := newFakeSocket(t, c.ex)
			out, errOut, code := cqRun(t, append(append([]string{"quarantine"}, c.args...), "--socket", sock)...)
			if code != ExitError || out != "" || errOut != c.wantErr {
				t.Errorf("exit %d, out %q\nerr %q\nwant exit %d and %q", code, out, errOut, ExitError, c.wantErr)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/10 && export TMPDIR=/root/.cache/w7-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Cq' ./internal/cli/`
Expected: FAIL at build time with `quarantineCommand undefined (type *app has no field or method quarantineCommand)`.

- [ ] **Step 3: Implement**

1. `controller_quarantine.go`: the local wire structs (doc comment each, JSON tags `id`, `kind`, `action`, `hostId`, `quarantinedFrom`, `locks`; `active`, `tx`, `restoredAt` as `*time.Time`, `records`; `key`, `record`) and `func (a *app) quarantineCommand() *cobra.Command`: `Use: "quarantine"`, a group whose run, like `controllerCommand`'s, returns `usagef("unknown command %q for %q", ...)` for an argument and prints help otherwise. Subcommands, each with `addConnFlags(cmd, true, false)` (so `--socket` and `--dev`), a `--json` flag ("print the raw JSON answer") and every API call through `a.connect(cmd)` and `c.do`, so a problem answer comes back as the `*problemError` the other commands print (`darkpanel: <detail> (<code>)`, then one line per field error; exit 1).
2. `list` (`cobra.NoArgs`): `GET /api/v1/controller/quarantine`, no body. With `--json` print the raw answer and a newline, as `controller web-ca --json` does. Otherwise print `Restore quarantine is active: restore <tx>, restored <restoredAt in RFC 3339 UTC>.` when `active`, else `The controller is not in restore quarantine.`; then `No records are quarantined.` when there are none, else a `writeTable` with the header `ID KIND ACTION HOST FROM LOCKS` and one row per record (`hostId` and an empty locks list shown as `-`, locks joined by `,`). Server text passes through `writeTable`/`sanitize`, never raw.
3. `resolve <record> <outcome>` (`cobra.ExactArgs(2)`): before any request, an empty record id is `usagef("the record id is required")` and an outcome other than `done`, `never` or `repair` is `usagef("the outcome is done, never or repair, not %q", outcome)` (exit 2). `POST /api/v1/controller/quarantine/<url.PathEscape(record)>/resolve` with body `{"outcome": "<outcome>"}`. With `--json` print the raw answer. Otherwise, for `done` and `never`: `Resolved <kind> <id> as <outcome>; its locks are released.`; for `repair`: `Resolved <kind> <id> as repair; its locks stay held.` and a second line `Repair it after release: darkpanel change repair <id>`; kind and id come from the answered record, sanitized.
4. `enroll-token <host>` (`cobra.ExactArgs(1)`): an empty host is `usagef("the host is required")`. `POST /api/v1/controller/quarantine/enroll-token` with body `{"hostId": "<host>"}`. With `--json` print the raw answer. Otherwise print `Enrollment token for host <host> (valid until <expiresAt in RFC 3339 UTC>, shown once):` and the token on the next line, both sanitized.
5. `release` (`cobra.NoArgs`) with `--keep-unresolved` (default false): `POST /api/v1/controller/quarantine/release` with body `{"keepUnresolved": <flag>}` always sent. With `--json` print the raw answer. Otherwise print `Restore quarantine released. No locks stay held.` when `heldLocks` is empty, else `Restore quarantine released. These locks stay held by quarantined records:` followed by a `writeTable` with the header `KEY RECORD`.
6. Argument errors are raised before `a.connect` is called, so no request is made; cobra's own argument-count and unknown-flag errors already exit 2. The command takes no passphrase and prompts for nothing.

- [ ] **Step 4: Run test to verify it passes, then the task's gates**

Run: `export TMPDIR=/root/.cache/w7-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Cq' -v ./internal/cli/`
Expected: PASS for `TestCqList` (four subtests), `TestCqListJSONPrintsTheAnswerAsSent`, `TestCqListNeverWritesServerControlCharacters`, `TestCqResolve`, `TestCqEnrollToken`, `TestCqRelease`, `TestCqArgumentsAreCheckedBeforeAnyRequest`, `TestCqPrintsAProblemAnswerLikeOtherCommands`.

Then the gates of Global Constraints:

```bash
mkdir -p /root/.cache/w7-tmp/10 && export TMPDIR=/root/.cache/w7-tmp/10 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -short -count=1 ./internal/cli/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/10 && go clean -testcache
```

Expected: every command exits 0, `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, golangci-lint prints `0 issues.`.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/controller_quarantine.go internal/cli/controller_quarantine_test.go
git commit -m "feat(cli): add controller quarantine commands"
```

### Task 11: Staged validation and installed verification

**Files:**
- Create: `internal/controller/backup/validate.go`
- Test: `internal/controller/backup/validate_test.go`

**Interfaces:**
- Consumes: Contracts "Controller database fixture" (Task 3: `fxDB`, `fxKey`, `fxInsertChange`, `fxInsertLock`, `fxInsertRollout`, `fxInsertListener`; the tests read the key file from the path `fxKey` returns instead of relying on its second result); `NonTerminal` and `Mark` (Task 8); Contracts "Schema" (Task 7); `MaxKeyMember` (Task 1, Contracts "Format"); `store.Open`, `store.CoreMigrations`, `modules.All()`; `secret.NewSealer`, `secret.KeyPrefix`, `secret.KeySize`; `quarantine.Marker`, `quarantine.MetaKey`.
- Produces: `CanaryKey`, `CanaryAD`, `Canary`, `ValidateStaged`, `VerifyInstalled` and `ErrNotQuarantined` exactly as Contracts "Validation and verification" declares them. Task 12 seals the canary with `CanaryAD`; Task 14 calls `ValidateStaged` in phase 2 and `VerifyInstalled` in phase 7; Task 15 calls `VerifyInstalled` after a completed recovery.

**Items:** 54, 69

**Depends on:** Task 1, Task 3, Task 7, Task 8
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security, silent-failures

- [ ] **Step 1: Write the failing test**

The helpers carry the prefix `vf`. `vfInstalled` builds the database as phase 6 leaves it: records marked by `Mark`, then switched to `journal_mode=DELETE` and closed (the staged database is converted the same way before it is installed, W7-R5), so `VerifyInstalled` can be shown to open it read-only without creating a `-wal` or `-shm` file.

```go
package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var vfCreatedAt = time.Date(2026, 11, 3, 8, 15, 0, 500, time.UTC)

func vfExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if _, err := db.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// vfSealCanary stores a canary sealed by sealer in db's core_meta.
func vfSealCanary(t *testing.T, db *store.DB, sealer *secret.Sealer, createdAt time.Time) {
	t.Helper()
	plain, err := json.Marshal(Canary{CreatedAt: createdAt})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(CanaryAD, plain)
	if err != nil {
		t.Fatal(err)
	}
	vfExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?) ON CONFLICT (key) DO UPDATE SET value = excluded.value`,
		CanaryKey, base64.StdEncoding.EncodeToString(sealed))
}

type vfStaged struct {
	path    string // the closed database
	keyFile []byte // the key file's bytes
	sealer  *secret.Sealer
}

// vfNewStaged builds a closed controller database holding a canary sealed
// with a fresh key, as a backup holds it, and returns it with that key.
func vfNewStaged(t *testing.T) vfStaged {
	t.Helper()
	keyPath, _, sealer := fxKey(t, t.TempDir())
	keyFile, err := os.ReadFile(keyPath) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	db := fxDB(t, "")
	vfSealCanary(t, db, sealer, vfCreatedAt)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	return vfStaged{path: db.Path(), keyFile: keyFile, sealer: sealer}
}

// vfReopen runs fn on the staged database opened for writing and closes it.
func vfReopen(t *testing.T, path string, fn func(db *store.DB)) {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	fn(db)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
}

func vfNoSideFiles(t *testing.T, path string) {
	t.Helper()
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(path + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s%s exists (stat error %v); want none", path, suffix, err)
		}
	}
}

func TestVfValidateStagedAcceptsAGoodDatabase(t *testing.T) {
	s := vfNewStaged(t)
	got, err := ValidateStaged(context.Background(), s.path, s.keyFile)
	if err != nil {
		t.Fatalf("ValidateStaged: %v", err)
	}
	if !got.Equal(vfCreatedAt) {
		t.Errorf("created at = %s, want the canary's %s", got, vfCreatedAt)
	}
	vfNoSideFiles(t, s.path)
}

func TestVfValidateStagedAcceptsAnOlderSchema(t *testing.T) {
	s := vfNewStaged(t)
	vfReopen(t, s.path, func(db *store.DB) {
		vfExec(t, db, `DELETE FROM core_migrations WHERE module = 'cloudflare'`)
		vfExec(t, db, `DELETE FROM core_migrations WHERE module = 'core' AND number = (SELECT max(number) FROM core_migrations WHERE module = 'core')`)
	})
	if _, err := ValidateStaged(context.Background(), s.path, s.keyFile); err != nil {
		t.Errorf("a database that lacks migrations the binary has: %v; it is migrated in a later phase", err)
	}
}

func TestVfValidateStagedRefusals(t *testing.T) {
	_, _, otherSealer := fxKey(t, t.TempDir())
	insertMigration := func(module string) func(t *testing.T, s vfStaged) {
		return func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) {
				vfExec(t, db, `INSERT INTO core_migrations (module, side, number, name, checksum, applied_at)
					VALUES (?, 'controller', 9999, 'from_the_future', 'x', '2026-11-04T12:00:00Z')`, module)
			})
		}
	}
	cases := []struct {
		name   string
		damage func(t *testing.T, s vfStaged)
		key    func(s vfStaged) []byte // nil: the staged database's key file
		want   string
	}{
		{"garbage instead of a database", func(t *testing.T, s vfStaged) {
			if err := os.WriteFile(s.path, bytes.Repeat([]byte("not a database "), 600), 0o600); err != nil {
				t.Fatal(err)
			}
		}, nil, ""},
		{"damaged pages", func(t *testing.T, s vfStaged) {
			f, err := os.OpenFile(s.path, os.O_WRONLY, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = f.Close() }()
			if info, err := f.Stat(); err != nil || info.Size() < 4*4096 {
				t.Fatalf("fixture database is %v (%v); the test damages pages 2 and 3", info, err)
			}
			if _, err := f.WriteAt(bytes.Repeat([]byte{0xFF}, 2*4096), 4096); err != nil {
				t.Fatal(err)
			}
		}, nil, "integrity"},
		{"a core migration newer than the binary", insertMigration("core"), nil, "core"},
		{"a module migration newer than the binary", insertMigration("cloudflare"), nil, "cloudflare"},
		{"a module the binary does not have", insertMigration("ghostmodule"), nil, "ghostmodule"},
		{"no canary", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) { vfExec(t, db, `DELETE FROM core_meta WHERE key = ?`, CanaryKey) })
		}, nil, "canary"},
		{"a canary sealed with another key", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) { vfSealCanary(t, db, otherSealer, vfCreatedAt) })
		}, nil, "canary"},
		{"a canary sealed for another place", func(t *testing.T, s vfStaged) {
			vfReopen(t, s.path, func(db *store.DB) {
				sealed, err := s.sealer.Seal("core_meta.value.other", []byte(`{"createdAt":"2026-11-03T08:15:00Z"}`))
				if err != nil {
					t.Fatal(err)
				}
				vfExec(t, db, `UPDATE core_meta SET value = ? WHERE key = ?`, base64.StdEncoding.EncodeToString(sealed), CanaryKey)
			})
		}, nil, "canary"},
		{"a key member that is not a key file", nil, func(vfStaged) []byte { return []byte("hello\n") }, "key"},
		{"a key member over the size limit", nil, func(s vfStaged) []byte {
			return append(bytes.Clone(s.keyFile[:len(s.keyFile)-1]), bytes.Repeat([]byte("A"), MaxKeyMember)...)
		}, "key"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := vfNewStaged(t)
			if c.damage != nil {
				c.damage(t, s)
			}
			key := s.keyFile
			if c.key != nil {
				key = c.key(s)
			}
			got, err := ValidateStaged(context.Background(), s.path, key)
			if err == nil {
				t.Fatal("ValidateStaged accepted it")
			}
			if !got.IsZero() {
				t.Errorf("created at = %s on a refusal, want the zero time", got)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
			if encoded := strings.TrimSpace(strings.TrimPrefix(string(s.keyFile), secret.KeyPrefix)); strings.Contains(err.Error(), encoded) {
				t.Errorf("error %q carries the key", err)
			}
		})
	}
}

func TestVfValidateStagedCreatesNothingForAMissingFile(t *testing.T) {
	s := vfNewStaged(t)
	missing := s.path + ".absent"
	if _, err := ValidateStaged(context.Background(), missing, s.keyFile); err == nil {
		t.Fatal("ValidateStaged accepted a database that does not exist")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("ValidateStaged created %s (stat error %v)", missing, err)
	}
}

// vfInstalled builds a controller database as the restore's phase 6 leaves
// it: unfinished records marked by Mark with tx, in journal_mode=DELETE and
// closed. breakIt, when set, runs after Mark on the open database.
func vfInstalled(t *testing.T, tx string, breakIt func(t *testing.T, db *store.DB)) string {
	t.Helper()
	db := fxDB(t, "")
	fxInsertChange(t, db, fxChange{ID: "c-applying", HostID: "host-a", Action: "fixture.act", State: "applying", Owner: "engine"})
	fxInsertChange(t, db, fxChange{ID: "c-repair", HostID: "host-a", Action: "fixture.act", State: "needs_repair"})
	fxInsertChange(t, db, fxChange{ID: "c-done", HostID: "host-a", Action: "fixture.act", State: "completed"})
	fxInsertLock(t, db, "k.applying", "c-applying")
	fxInsertLock(t, db, "k.repair", "c-repair")
	fxInsertLock(t, db, "k.update", "01JUPDATE0000000000000000A")
	fxInsertRollout(t, db, "r1", "running", "host-a")
	fxInsertListener(t, db, "l1", "open")
	m := quarantine.Marker{Tx: tx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-time.Hour)}
	if err := Mark(context.Background(), db, m); err != nil {
		t.Fatalf("Mark: %v", err)
	}
	if breakIt != nil {
		breakIt(t, db)
	}
	path := db.Path()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	vfDeleteJournal(t, path)
	return path
}

// vfDeleteJournal switches the closed database at path from WAL to the
// DELETE journal, which the installed database uses.
func vfDeleteJournal(t *testing.T, path string) {
	t.Helper()
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	var mode string
	if err := raw.QueryRow(`PRAGMA journal_mode=DELETE`).Scan(&mode); err != nil || mode != "delete" {
		t.Fatalf("journal_mode=DELETE answered %q, %v", mode, err)
	}
	if err := raw.Close(); err != nil {
		t.Fatal(err)
	}
	vfNoSideFiles(t, path)
}

const vfTx = "01JTXVERIFY00000000000000A"

func TestVfVerifyInstalledAcceptsAMarkedDatabase(t *testing.T) {
	path := vfInstalled(t, vfTx, nil)
	if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
		t.Errorf("VerifyInstalled: %v", err)
	}
}

func TestVfVerifyInstalledRefusals(t *testing.T) {
	type refusal struct {
		name, tx string
		breakIt  func(t *testing.T, db *store.DB)
		want     string // the error names it
	}
	cases := []refusal{
		{"no marker", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `DELETE FROM core_meta WHERE key = ?`, quarantine.MetaKey)
		}, "restore_quarantine"},
		{"another transaction's marker", "01JTXOTHER0000000000000000", nil, vfTx},
		{"a marker that does not decode", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_meta SET value = 'not json' WHERE key = ?`, quarantine.MetaKey)
		}, "restore_quarantine"},
		{"a running rollout", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_rollouts SET state = 'running' WHERE id = 'r1'`)
		}, "r1"},
		{"a draining rollout", vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_rollouts SET state = 'draining', outcome = 'failed' WHERE id = 'r1'`)
		}, "r1"},
		{"a lock of an update reservation", vfTx, func(t *testing.T, db *store.DB) {
			fxInsertLock(t, db, "k.reservation", "01JUPDATE0000000000000000B")
		}, "k.reservation"},
		{"a lock of a finished change", vfTx, func(t *testing.T, db *store.DB) { fxInsertLock(t, db, "k.finished", "c-done") }, "k.finished"},
	}
	for _, state := range []string{"applying", "pending", "awaiting_confirmation", "undoing", "needs_repair", "recovering", "releasing"} {
		cases = append(cases, refusal{"a change left " + state, vfTx, func(t *testing.T, db *store.DB) {
			vfExec(t, db, `UPDATE core_changes SET state = ?, quarantined_from = NULL WHERE id = 'c-repair'`, state)
		}, "c-repair"})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			path := vfInstalled(t, c.tx, c.breakIt)
			err := VerifyInstalled(context.Background(), path, vfTx)
			if !errors.Is(err, ErrNotQuarantined) {
				t.Fatalf("VerifyInstalled = %v, want ErrNotQuarantined", err)
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not name %q", err, c.want)
			}
		})
	}
}

func TestVfVerifyInstalledOpensReadOnly(t *testing.T) {
	path := vfInstalled(t, vfTx, nil)
	digest := func() [32]byte {
		b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
		if err != nil {
			t.Fatal(err)
		}
		return sha256.Sum256(b)
	}
	before := digest()
	if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
		t.Fatal(err)
	}
	vfNoSideFiles(t, path)
	if digest() != before {
		t.Error("VerifyInstalled changed the database file")
	}

	missing := path + ".absent"
	if err := VerifyInstalled(context.Background(), missing, vfTx); err == nil || errors.Is(err, ErrNotQuarantined) {
		t.Errorf("VerifyInstalled of a missing file = %v, want an open error", err)
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("VerifyInstalled created %s (stat error %v)", missing, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/11 && export TMPDIR=/root/.cache/w7-tmp/11 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Vf' ./internal/controller/backup/`
Expected: FAIL at build time with `undefined: Canary` (then `CanaryAD`, `CanaryKey`, `ValidateStaged`, `VerifyInstalled`, `ErrNotQuarantined`).

- [ ] **Step 3: Implement**

1. `validate.go`: declare `CanaryKey`, `CanaryAD`, `Canary` and `ErrNotQuarantined` as in Contracts, each with a doc comment. The canary's stored value is `base64.StdEncoding` of `Sealer.Seal(CanaryAD, json.Marshal(Canary{CreatedAt}))`.
2. `ValidateStaged(ctx, dbPath, keyFile)` runs these checks in order and returns on the first failure, always with the zero time. Every error starts with `backup: staged database:` (the key check with `backup:`), names the failed check, and carries neither key bytes nor database content:
   - key: an unexported helper builds a `secret.Sealer` from the key member's bytes. `secret.LoadOrCreateKey` reads a path and checks ownership, so it is not used. The helper accepts exactly what the key file format is: at most `MaxKeyMember` bytes, one line `secret.KeyPrefix` + standard base64 of `secret.KeySize` bytes + `\n`; anything else is an error containing the word `key`, without quoting the bytes.
   - open: `os.Stat` must show a regular file (`store.Open` would create a missing one; the error says `open`), then `store.Open(ctx, dbPath)`; a file that is not a database fails here (`open`). The database is closed by a deferred `Close` on every path, which also removes the `-wal` and `-shm` files `store.Open` leaves while open.
   - integrity: `PRAGMA integrity_check` read through the reader pool; the answer must be exactly one row `ok`; a query error or any other answer is an error naming `integrity check`, not echoing the rows.
   - schema: for side `controller`, read `SELECT module, max(number) FROM core_migrations WHERE side = 'controller' GROUP BY module`. The highest embedded number of module `core` comes from `store.CoreMigrations(store.SideController)` and of each module from `modules.All()` (`Manifest().Migrations[core.SideController]`, keyed by `Manifest().ID`), parsed from the `NNNN_` prefix of the file names with `fs.ReadDir`. An applied number above the embedded one is an error `schema is newer than this binary: module <m> has migration <n>, the binary has <k>`; so is a module with applied rows that this binary does not have (embedded 0). A database with fewer applied migrations than the binary embeds is accepted: phase 3 migrates it.
   - canary: no `core_meta` row `CanaryKey`, a value that is not base64, a seal that does not open with the sealer under `CanaryAD` (another key, another associated data), or a payload that does not decode to a `Canary` with a non-zero `CreatedAt` are each an error containing `canary` and saying which. Return `CreatedAt`.
3. `VerifyInstalled(ctx, dbPath, tx)`: refuse a path containing `?`, `#` or `%` as `store.Open` does; open with `sql.Open("sqlite", u.String())` where `u` is a `net/url.URL{Scheme: "file", Path: dbPath, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"}` (the driver is registered by the `store` import). Never use `store.Open`: it sets `journal_mode=WAL`, creates `-wal` and `-shm` and chmods. Close the handle on return. An open or read failure, including a missing file, is a plain error (not `ErrNotQuarantined`) and creates no file. Then, each failure `fmt.Errorf("%w: <detail>", ErrNotQuarantined)`:
   - `core_meta` has no `quarantine.MetaKey` row: detail `core_meta restore_quarantine is missing`; the value does not decode to a `quarantine.Marker`: `core_meta restore_quarantine does not decode`; `Marker.Tx != tx`: `core_meta restore_quarantine carries tx <marker tx>, this restore is <tx>`.
   - a `core_changes` row whose state is in `NonTerminal` (bind the states as parameters; report the lowest id): `change <id> is still <state>`.
   - a `core_rollouts` row in state `running` or `draining`: `rollout <id> is still <state>`.
   - a `core_locks` row whose owner is not the id of a `core_changes` row in state `quarantined` (lowest key first): `lock <key> is owned by <owner>, which is not a quarantined change`.
   Names of ids and keys may appear in errors; nothing else from the database does.
4. VerifyInstalled writes nothing: the database file is byte-identical afterwards.

- [ ] **Step 4: Run test to verify it passes, then the task's gates**

Run: `export TMPDIR=/root/.cache/w7-tmp/11 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'Vf' -v ./internal/controller/backup/`
Expected: PASS for `TestVfValidateStagedAcceptsAGoodDatabase`, `TestVfValidateStagedAcceptsAnOlderSchema`, `TestVfValidateStagedRefusals` (ten subtests), `TestVfValidateStagedCreatesNothingForAMissingFile`, `TestVfVerifyInstalledAcceptsAMarkedDatabase`, `TestVfVerifyInstalledRefusals` (fourteen subtests), `TestVfVerifyInstalledOpensReadOnly`.

Then the gates of Global Constraints:

```bash
mkdir -p /root/.cache/w7-tmp/11 && export TMPDIR=/root/.cache/w7-tmp/11 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/11 && go clean -testcache
```

Expected: every command exits 0, `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, golangci-lint prints `0 issues.`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/validate.go internal/controller/backup/validate_test.go
git commit -m "feat(backup): add staged and installed checks"
```

### Task 12: Backup stream route

**Files:**
- Create: `internal/controller/backup/stream.go`
- Test: `internal/controller/backup/stream_test.go`

**Interfaces:**
- Consumes: Contracts "Backup handler" (the shape of `BackupRequest` and `backupHandler`), "Configuration and contribution" (`Backup`, `Config`, `New`, the `running` flag), "Format" (`Write`, `Params`, `DefaultParams`, `Member`, `MemberDB`, `MemberKey`, `MaxKeyMember`; the tests also use `Read`, `Header`, `TestParams`, `HeaderSize`, `Magic`, `FormatVersion`), "Validation and verification" (`CanaryKey`, `CanaryAD`, `Canary`), "Audit", "Route helpers" (Task 9: `writeProblem`, `decodeBody`, `recordAudit`), and in the tests "Controller database fixture" (`fxDB`, `fxKey`, `fxInsertHost`, `fxInsertChange`, `fxNow`). `ext.Deps` fields used: `DB`, `Sealer`, `Clock`, `Log`, `Audit`, `LocalHostID`.
- Produces: `BackupRequest` and `func (b *Backup) backupHandler(deps ext.Deps) http.Handler` exactly as Contracts "Backup handler" states them; Task 16 mounts the handler on `POST /api/v1/controller/backup` with `Stream: true`. The file declares only `BackupRequest`, `backupHandler` and unexported helpers whose names start with `stream`, so no other task's helper can collide.

**Items:** 54

**Depends on:** Task 1, Task 3, Task 9, Task 11
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/stream_test.go`. The tests call the handler directly through `httptest`; they use the real audit store (`audit.New`) over the fixture database, because `core_audit` refuses a phase or result outside its CHECK lists and an in-memory fake would hide that.

```go
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// bsEnv is a migrated controller database with a key file, a clock, the
// real audit log and the backup handler over them.
type bsEnv struct {
	db      *store.DB
	dir     string // beside controller.db, where the handler makes its temporary copy
	keyPath string
	key     []byte // the key file's bytes
	sealer  *secret.Sealer
	clock   *coretest.FakeClock
	audit   *audit.Store
	b       *Backup
	h       http.Handler
}

func bsNewEnv(t *testing.T) *bsEnv {
	t.Helper()
	dir := t.TempDir()
	db := fxDB(t, dir)
	fxInsertHost(t, db, "host-local", "enrolled", true)
	fxInsertChange(t, db, fxChange{ID: "01BSCHANGE00000000000000AA", HostID: "host-local", Action: "docker.container-stop", State: core.StateApplying})
	keyPath, key, sealer := fxKey(t, t.TempDir())
	clock := coretest.NewFakeClock(fxNow)
	log := audit.New(db, clock)
	deps := ext.Deps{
		DB: db, Sealer: sealer, Clock: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Audit: log, LocalHostID: "host-local",
	}
	b := New(Config{KeyFile: keyPath})
	return &bsEnv{db: db, dir: dir, keyPath: keyPath, key: key, sealer: sealer, clock: clock, audit: log, b: b, h: b.backupHandler(deps)}
}

func bsBody(t *testing.T, passphrase string, p *Params) string {
	t.Helper()
	b, err := json.Marshal(BackupRequest{Passphrase: passphrase, Argon2: p})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func bsRequest(body string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/backup", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return req
}

func (e *bsEnv) post(body string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, bsRequest(body))
	return rec
}

// copies lists the temporary database copies left beside controller.db.
func (e *bsEnv) copies(t *testing.T) []string {
	t.Helper()
	names, err := filepath.Glob(filepath.Join(e.dir, "backup-*.db"))
	if err != nil {
		t.Fatal(err)
	}
	return names
}

// rows returns the audit rows of backups, newest first.
func (e *bsEnv) rows(t *testing.T) []core.AuditEntry {
	t.Helper()
	got, _, err := e.audit.List(context.Background(), core.AuditQuery{Action: "controller.backup"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type bsProblem struct {
	Status int    `json:"status"`
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func bsDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) bsProblem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (status %d)", ct, rec.Code)
	}
	var p bsProblem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	if p.Status != rec.Code {
		t.Errorf("problem status = %d, response status = %d", p.Status, rec.Code)
	}
	return p
}

// bsOpen decrypts a backup body with Read and returns its header, the members
// by name and their order.
func bsOpen(t *testing.T, body []byte, passphrase string) (Header, map[string][]byte, []string) {
	t.Helper()
	members := map[string][]byte{}
	var order []string
	hdr, err := Read(bytes.NewReader(body), []byte(passphrase), func(name string, size int64, r io.Reader) error {
		b, err := io.ReadAll(r)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			return fmt.Errorf("member %s: read %d bytes, size %d", name, len(b), size)
		}
		members[name] = b
		order = append(order, name)
		return nil
	})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	return hdr, members, order
}

func bsQuery(t *testing.T, db *sql.DB, query string) []string {
	t.Helper()
	rows, err := db.QueryContext(context.Background(), query)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer func() { _ = rows.Close() }()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			t.Fatal(err)
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// bsCanary opens the sealed canary of db with sealer and returns its CreatedAt.
func bsCanary(t *testing.T, sealer *secret.Sealer, db *sql.DB) time.Time {
	t.Helper()
	got := bsQuery(t, db, `SELECT value FROM core_meta WHERE key = '`+CanaryKey+`'`)
	if len(got) != 1 {
		t.Fatalf("core_meta holds %d %s rows, want 1", len(got), CanaryKey)
	}
	sealed, err := base64.StdEncoding.DecodeString(got[0])
	if err != nil {
		t.Fatalf("the canary is not base64: %v", err)
	}
	plain, err := sealer.Open(CanaryAD, sealed)
	if err != nil {
		t.Fatalf("the key does not open the canary: %v", err)
	}
	var c Canary
	if err := json.Unmarshal(plain, &c); err != nil {
		t.Fatalf("decode the canary: %v", err)
	}
	return c.CreatedAt
}

func TestBsRoundTrip(t *testing.T) {
	e := bsNewEnv(t)
	passphrase := strings.Repeat("p", 1024)

	rec := e.post(bsBody(t, passphrase, &TestParams))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("Content-Type = %q, want application/octet-stream", got)
	}
	disposition := regexp.MustCompile(`^attachment; filename="darkpanel-controller-20261104T120000Z\.dpbk"$`)
	if got := rec.Header().Get("Content-Disposition"); !disposition.MatchString(got) {
		t.Errorf("Content-Disposition = %q, want the UTC time of the clock in the file name", got)
	}

	hdr, members, order := bsOpen(t, rec.Body.Bytes(), passphrase)
	if hdr.Params != TestParams {
		t.Errorf("header params = %+v, want the requested %+v", hdr.Params, TestParams)
	}
	if len(order) != 2 || order[0] != MemberDB || order[1] != MemberKey {
		t.Fatalf("members = %v, want [%s %s]", order, MemberDB, MemberKey)
	}
	if !bytes.Equal(members[MemberKey], e.key) {
		t.Error("the key member differs from the key file")
	}

	restored := filepath.Join(t.TempDir(), "restored.db")
	if err := os.WriteFile(restored, members[MemberDB], 0o600); err != nil {
		t.Fatal(err)
	}
	rdb, err := store.Open(context.Background(), restored)
	if err != nil {
		t.Fatalf("open the database member: %v", err)
	}
	t.Cleanup(func() { _ = rdb.Close() })
	for _, q := range []string{
		`SELECT id || '|' || state FROM core_changes ORDER BY id`,
		`SELECT id FROM core_hosts ORDER BY id`,
		`SELECT module || '|' || number FROM core_migrations ORDER BY module, number`,
		`SELECT key || '=' || value FROM core_meta ORDER BY key`,
	} {
		live, got := bsQuery(t, e.db.Read(), q), bsQuery(t, rdb.Read(), q)
		if strings.Join(live, "\n") != strings.Join(got, "\n") {
			t.Errorf("%s\nlive:     %q\nrestored: %q", q, live, got)
		}
	}
	if got := bsCanary(t, e.sealer, rdb.Read()); !got.Equal(fxNow) {
		t.Errorf("canary createdAt = %s, want the clock's %s", got, fxNow)
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copies left after a backup: %v", left)
	}
}

func TestBsDefaultParams(t *testing.T) {
	e := bsNewEnv(t)
	rec := e.post(`{"passphrase":"p"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	h := rec.Body.Bytes()
	if len(h) < HeaderSize || string(h[:4]) != Magic || h[4] != FormatVersion {
		t.Fatalf("the body does not start with a backup header")
	}
	got := Params{T: binary.BigEndian.Uint32(h[5:9]), M: binary.BigEndian.Uint32(h[9:13]), P: h[13]}
	if got != DefaultParams {
		t.Errorf("header params = %+v, want the defaults %+v", got, DefaultParams)
	}
}

func TestBsRefusesBadRequests(t *testing.T) {
	e := bsNewEnv(t)
	cases := []struct{ name, body, path string }{
		{"empty body", ``, ""},
		{"not JSON", `passphrase`, ""},
		{"two JSON values", `{"passphrase":"x"}{"passphrase":"x"}`, ""},
		{"unknown field", `{"passphrase":"x","extra":1}`, ""},
		{"empty passphrase", `{"passphrase":""}`, ""},
		{"missing passphrase", `{}`, ""},
		{"passphrase of 1025 bytes", `{"passphrase":"` + strings.Repeat("a", 1025) + `"}`, ""},
		{"body over 64 KiB", `{"passphrase":"` + strings.Repeat("a", 70000) + `"}`, ""},
		{"argon2.t 0", `{"passphrase":"x","argon2":{"t":0,"m":65536,"p":1}}`, "argon2.t"},
		{"argon2.t 11", `{"passphrase":"x","argon2":{"t":11,"m":65536,"p":1}}`, "argon2.t"},
		{"argon2.m 65535", `{"passphrase":"x","argon2":{"t":1,"m":65535,"p":1}}`, "argon2.m"},
		{"argon2.m 1048577", `{"passphrase":"x","argon2":{"t":1,"m":1048577,"p":1}}`, "argon2.m"},
		{"argon2.p 0", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":0}}`, "argon2.p"},
		{"argon2.p 17", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":17}}`, "argon2.p"},
		{"argon2.p 300", `{"passphrase":"x","argon2":{"t":1,"m":65536,"p":300}}`, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rec := e.post(c.body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := bsDecodeProblem(t, rec); p.Code != "request.invalid_input" {
				t.Errorf("code = %q, want request.invalid_input", p.Code)
			}
			if c.path != "" && !strings.Contains(rec.Body.String(), c.path) {
				t.Errorf("the problem does not name %s: %s", c.path, rec.Body.String())
			}
			if e.b.running.Load() {
				t.Error("a refused request left the backup slot taken")
			}
		})
	}
	if got := bsQuery(t, e.db.Read(), `SELECT key FROM core_meta WHERE key = '`+CanaryKey+`'`); len(got) != 0 {
		t.Error("a refused request wrote the canary")
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copies left after refused requests: %v", left)
	}
	if rows := e.rows(t); len(rows) != 0 {
		t.Errorf("refused requests wrote %d audit rows", len(rows))
	}
}

// bsGate is a response writer whose first body write waits until open is called.
type bsGate struct {
	header   http.Header
	status   int
	entered  chan struct{} // closed when the first body write begins
	release  chan struct{}
	enter    sync.Once
	openOnce sync.Once
}

func bsNewGate() *bsGate {
	return &bsGate{header: http.Header{}, entered: make(chan struct{}), release: make(chan struct{})}
}

func (g *bsGate) Header() http.Header  { return g.header }
func (g *bsGate) WriteHeader(code int) { g.status = code }
func (g *bsGate) Write(p []byte) (int, error) {
	g.enter.Do(func() { close(g.entered) })
	<-g.release
	return len(p), nil
}
func (g *bsGate) open() { g.openOnce.Do(func() { close(g.release) }) }

func TestBsRefusesASecondBackupWhileOneStreams(t *testing.T) {
	e := bsNewEnv(t)
	gate := bsNewGate()
	t.Cleanup(gate.open)
	first := bsRequest(bsBody(t, "first", &TestParams))
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.h.ServeHTTP(gate, first)
	}()
	select {
	case <-gate.entered:
	case <-time.After(30 * time.Second):
		t.Fatal("the first backup never began to stream")
	}

	rec := e.post(bsBody(t, "second", &TestParams))
	if rec.Code != http.StatusConflict {
		t.Fatalf("second backup status = %d, want 409 (body %.300s)", rec.Code, rec.Body.String())
	}
	p := bsDecodeProblem(t, rec)
	if p.Code != "core.change_busy" || !strings.Contains(p.Detail, "another backup is running") {
		t.Errorf("problem = %+v, want core.change_busy naming the running backup", p)
	}

	gate.open()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("the first backup never finished")
	}
	if gate.status != http.StatusOK {
		t.Errorf("first backup status = %d, want 200", gate.status)
	}
	if rec := e.post(bsBody(t, "third", &TestParams)); rec.Code != http.StatusOK {
		t.Errorf("a backup after the first finished: status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	if rows := e.rows(t); len(rows) != 2 {
		t.Errorf("audit rows = %d, want 2 (the refused request writes none)", len(rows))
	}
}

func TestBsKeyFileRules(t *testing.T) {
	cases := []struct {
		name    string
		arrange func(t *testing.T, e *bsEnv)
		wantOK  bool
	}{
		{"symbolic link", func(t *testing.T, e *bsEnv) {
			target := filepath.Join(t.TempDir(), "real.key")
			if err := os.WriteFile(target, e.key, 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(target, e.keyPath); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"directory", func(t *testing.T, e *bsEnv) {
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(e.keyPath, 0o700); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"missing", func(t *testing.T, e *bsEnv) {
			if err := os.Remove(e.keyPath); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"one byte over the limit", func(t *testing.T, e *bsEnv) {
			if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte("k"), MaxKeyMember+1), 0o600); err != nil {
				t.Fatal(err)
			}
		}, false},
		{"exactly the limit", func(t *testing.T, e *bsEnv) {
			if err := os.WriteFile(e.keyPath, bytes.Repeat([]byte("k"), MaxKeyMember), 0o600); err != nil {
				t.Fatal(err)
			}
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := bsNewEnv(t)
			c.arrange(t, e)
			rec := e.post(bsBody(t, "p", &TestParams))
			if c.wantOK {
				if rec.Code != http.StatusOK {
					t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
				}
				_, members, _ := bsOpen(t, rec.Body.Bytes(), "p")
				if len(members[MemberKey]) != MaxKeyMember {
					t.Errorf("key member is %d bytes, want %d", len(members[MemberKey]), MaxKeyMember)
				}
				return
			}
			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := bsDecodeProblem(t, rec); p.Code != "internal" {
				t.Errorf("code = %q, want internal", p.Code)
			}
			if strings.Contains(rec.Body.String(), secret.KeyPrefix) {
				t.Error("the problem carries key file bytes")
			}
			if left := e.copies(t); len(left) != 0 {
				t.Errorf("temporary copy left after a refused key file: %v", left)
			}
			if rows := e.rows(t); len(rows) != 0 {
				t.Errorf("a backup that never streamed wrote %d audit rows", len(rows))
			}
		})
	}
}

// bsBrokenWriter accepts its first body write and fails every later one, as
// a client that disconnects in the middle of the download.
type bsBrokenWriter struct {
	header http.Header
	status int
	writes int
}

func (w *bsBrokenWriter) Header() http.Header  { return w.header }
func (w *bsBrokenWriter) WriteHeader(code int) { w.status = code }
func (w *bsBrokenWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes > 1 {
		return 0, errors.New("broken pipe")
	}
	return len(p), nil
}

// bsServe runs h and returns what it panicked with, nil when it returned.
func bsServe(h http.Handler, w http.ResponseWriter, r *http.Request) (recovered any) {
	defer func() { recovered = recover() }()
	h.ServeHTTP(w, r)
	return nil
}

func TestBsAbortsTheResponseWhenAWriteFailsAfterTheFirstByte(t *testing.T) {
	e := bsNewEnv(t)
	w := &bsBrokenWriter{header: http.Header{}}

	recovered := bsServe(e.h, w, bsRequest(bsBody(t, "p", &TestParams)))
	if err, ok := recovered.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
		t.Fatalf("handler panicked with %v, want http.ErrAbortHandler", recovered)
	}
	if w.status != http.StatusOK || w.writes < 2 {
		t.Errorf("status = %d after %d body writes, want 200 and a failure on a later write", w.status, w.writes)
	}
	if left := e.copies(t); len(left) != 0 {
		t.Errorf("temporary copy left after an aborted response: %v", left)
	}
	if rec := e.post(bsBody(t, "p", &TestParams)); rec.Code != http.StatusOK {
		t.Errorf("a backup after the aborted one: status = %d, want 200 (the slot must be free)", rec.Code)
	}
	var ok, failed int
	for _, r := range e.rows(t) {
		switch {
		case r.Result == "ok":
			ok++
		case r.Result == "error" && r.ErrorCode == "internal":
			failed++
		}
	}
	if ok != 1 || failed != 1 {
		t.Errorf("audit rows: %d ok and %d failed, want one of each", ok, failed)
	}
}

func TestBsAuditsOneRowWithoutThePassphrase(t *testing.T) {
	e := bsNewEnv(t)
	const passphrase = "correct horse battery staple 7f3a91"

	if rec := e.post(bsBody(t, passphrase, &TestParams)); rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	rows := e.rows(t)
	if len(rows) != 1 {
		t.Fatalf("audit rows = %d, want 1", len(rows))
	}
	r := rows[0]
	if r.Actor != "local:root@host-local" || r.Action != "controller.backup" || r.Phase != "apply" || r.Result != "ok" {
		t.Errorf("row = %+v, want actor local:root@host-local, action controller.backup, phase apply, result ok", r)
	}
	if r.ErrorCode != "" || r.ChangeID != "" || r.HostID != "" || len(r.Input) != 0 {
		t.Errorf("row = %+v, want no error code, change, host or input", r)
	}
	if !r.At.Equal(fxNow) {
		t.Errorf("row time = %s, want the clock's %s", r.At, fxNow)
	}
	if strings.Contains(fmt.Sprintf("%+v", r), passphrase) {
		t.Error("the audit row carries the passphrase")
	}
}

func TestBsRefreshesTheCanaryBeforeEachCopy(t *testing.T) {
	e := bsNewEnv(t)
	for i := range 2 {
		want := fxNow.Add(time.Duration(i) * time.Hour)
		if i > 0 {
			e.clock.Advance(time.Hour)
		}
		if rec := e.post(bsBody(t, "p", &TestParams)); rec.Code != http.StatusOK {
			t.Fatalf("backup %d: status = %d, want 200 (body %.300s)", i, rec.Code, rec.Body.String())
		}
		if got := bsCanary(t, e.sealer, e.db.Read()); !got.Equal(want) {
			t.Errorf("after backup %d the canary createdAt = %s, want %s", i, got, want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/12 && export TMPDIR=/root/.cache/w7-tmp/12 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run '^TestBs' ./internal/controller/backup/`
Expected: FAIL, the build fails with `undefined: BackupRequest` and `b.backupHandler undefined (type *Backup has no field or method backupHandler)`.

- [ ] **Step 3: Implement**

Write `stream.go` in package `backup`:

1. Declare `BackupRequest` (Contracts "Backup handler", JSON tags `passphrase` and `argon2,omitempty`) and `func (b *Backup) backupHandler(deps ext.Deps) http.Handler`, which returns an `http.HandlerFunc`. Declare no other exported name.
2. Problems. Answer every refusal, and every failure before the body begins, with `writeProblem(w, deps.Log, err)` from `quarantine_routes.go` (Contracts "Route helpers"); declare no copy of it.
3. Request. `decodeBody(w, r, &req)` into a `BackupRequest` (Contracts "Route helpers": at most `maxRouteBody` bytes, unknown fields refused, nothing but whitespace after the object); every decoding failure (empty body, bad JSON, unknown field, second value, body over 64 KiB) comes back as `request.invalid_input` and is answered unchanged. The passphrase must be 1 to 1024 bytes, else `errcode.InvalidInput` "the passphrase must be 1 to 1024 bytes". `Argon2` nil means `DefaultParams`; otherwise `Params.Validate()` must pass and its error (already `request.invalid_input` naming `argon2.t`, `argon2.m` or `argon2.p`) is answered unchanged. All of this happens before anything touches the database or the slot.
4. Slot. `b.running.CompareAndSwap(false, true)`; when it fails answer `errcode.New(errcode.ChangeBusy, "another backup is running")`. Release it with `defer b.running.Store(false)` so a panic frees it too.
5. Canary. Upsert `core_meta` key `CanaryKey` through `deps.DB.Write` with `INSERT ... ON CONFLICT (key) DO UPDATE SET value = excluded.value`. The value is `base64.StdEncoding` of `deps.Sealer.Seal(CanaryAD, json.Marshal(Canary{CreatedAt: deps.Clock.Now().UTC()}))`.
6. Copy. The temporary file is `filepath.Join(filepath.Dir(deps.DB.Path()), "backup-"+ulid.Make().String()+".db")` (`github.com/oklog/ulid/v2`). Register `defer os.Remove(path)` before the copy and ignore a missing file, so the copy is removed on success, on every error and on a panic. Run `VACUUM INTO ?` on `deps.DB.Writer()` with the path as the bound parameter and the request context, then `os.Chmod(path, 0o600)`.
7. Key. `streamReadKey(path string) ([]byte, error)` opens `Config.KeyFile` with `syscall.O_RDONLY|O_NOFOLLOW|O_CLOEXEC|O_NONBLOCK` (so a symbolic link fails and a FIFO does not block), requires `Stat` on the open file to be a regular file of at most `MaxKeyMember` bytes, and reads at most `MaxKeyMember` bytes. Every refusal is a plain wrapped error `backup: ...` that names the path and the rule but never file content; the handler answers it through `writeProblem` as a 500. `clear` the returned bytes when the handler returns.
8. Stream. Open the copy with `os.Open` and `Stat` its size. Members are `{MemberDB, size, file}` then `{MemberKey, len(key), bytes.NewReader(key)}`. Wrap `w` in a `streamWriter` whose first `Write` call, before delegating, sets `Content-Type: application/octet-stream` and `Content-Disposition: attachment; filename="darkpanel-controller-<deps.Clock.Now().UTC().Format("20060102T150405Z")>.dpbk"`, calls `WriteHeader(http.StatusOK)` and records that the body began (set it before the delegated write returns, so a failed first write also counts). Call `Write(sw, rand.Reader, passphraseBytes, params, members)` with `crypto/rand`, where `passphraseBytes` is `[]byte(req.Passphrase)`; the format code clears it.
9. Outcome. When `Write` returns an error and the body has not begun, answer it through `writeProblem` (a 500). When the body has begun, write the failure audit row (below), then `panic(http.ErrAbortHandler)` so the client sees a truncated file; a normal return writes the success row.
10. Audit. One row per backup that reached step 8 (Contracts "Audit"), on a context from `context.WithoutCancel(r.Context())`. A normal return writes it with `recordAudit(ctx, deps, "controller.backup", "apply", "", "")`. The aborted stream writes it with a direct `deps.Audit.Record` call, because `recordAudit` writes only `ok` rows: `Actor` `"local:root@" + deps.LocalHostID`, `Action` `"controller.backup"`, `Phase` `"apply"`, `Result` `"error"`, `ErrorCode` `string(errcode.Internal)`, `At` `deps.Clock.Now()`, and no `Input`, `ChangeID` or `HostID`; a failed `Record` is logged with `deps.Log.Error`. Requests refused before step 8 write no row.
11. Lint. `os.OpenFile` and `os.Open` of a path that is not a literal need `//nolint:gosec // G304: the path comes from configuration or the database directory, never from a request`; keep error strings starting `backup:`.

- [ ] **Step 4: Run test to verify it passes**

Run: `mkdir -p /root/.cache/w7-tmp/12 && export TMPDIR=/root/.cache/w7-tmp/12 GOTOOLCHAIN=local && timeout 600 go test -count=1 -v -run '^TestBs' ./internal/controller/backup/`
Expected: every `TestBs...` test and subtest PASS, final line `ok  	github.com/darkraise/darkpanel/internal/controller/backup`.

Run: `export TMPDIR=/root/.cache/w7-tmp/12 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 go test -race -short -count=1 ./internal/controller/backup/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: build and vet print nothing, every test line is `ok` or `?`, lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Then `rm -rf /root/.cache/w7-tmp/12 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/stream.go internal/controller/backup/stream_test.go
git commit -m "feat(backup): stream the encrypted backup"
```

### Task 13: Quarantine resolve route

**Files:**
- Create: `internal/controller/backup/resolve.go`
- Test: `internal/controller/backup/resolve_test.go`

**Interfaces:**
- Consumes: Contracts "Quarantine routes" (`Record`, `ResolveRequest`, `recordByID`, the `queryer` interface and the three handlers Task 9 wrote, `Backup`, `New`, `Config`), "Configuration and contribution", "Marking" (the `quarantined` state and `quarantined_from` Task 7 and Task 8 define), "Audit", and "Route helpers" (Task 9: `maxRouteBody`, `writeProblem`, `writeJSON`, `decodeBody`, `recordAudit`). In the tests: "Controller database fixture" (`fxDB`, `fxInsertChange`, `fxInsertComposite`, `fxInsertLock`, `fxInsertRollout`, `fxStep`, `fxChange`, `fxNow`), `coretest.FakeAgent` and `coretest.Sealer`, `audit.New`. Core names: `core.HostDirectory`, `core.AgentClient` (`Lookup`, `Retire`), `core.PlanLookup` and the `core.Lookup*` and `core.State*` constants.
- Produces: `func (b *Backup) resolveHandler(deps ext.Deps) http.Handler` as Contracts "Quarantine routes" states it (path value `record`); Task 16 mounts it on `POST /api/v1/controller/quarantine/{record}/resolve`.

**Items:** 69

**Depends on:** Task 3, Task 7, Task 8, Task 9
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** silent-failures

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/resolve_test.go`. `TestRsResolve` follows the resolve rules row by row; its record builders add a `quarantined_from` of `applying` (`running` for a rollout) and one lock row `lock:R1` owned by the record, so every refusal can show the lock untouched and every outcome the lock deleted or kept. The fake agents answer lookups from a script and fail the test on a lookup nobody scripted.

```go
package backup

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const rsID = "R1"

// rsEnv is a controller database, scripted agents and the resolve handler.
type rsEnv struct {
	db    *store.DB
	audit *audit.Store
	h     http.Handler

	mu            sync.Mutex
	lookups       map[string]core.PlanLookup // by plan id
	lookupErrs    map[string]error           // by plan id
	agentErrs     map[string]error           // by host id: what Agent returns
	retireAnswers map[string]core.PlanLookup // by plan id; the default is retired
	asked         []string                   // plan ids looked up; "(private)" is added to a private lookup
	retired       []string                   // plan ids Retire was called with
}

// rsHosts is the core.HostDirectory over an rsEnv's scripts.
type rsHosts struct{ e *rsEnv }

func (h rsHosts) Agent(_ context.Context, hostID string) (core.AgentClient, error) {
	e := h.e
	e.mu.Lock()
	defer e.mu.Unlock()
	if err, ok := e.agentErrs[hostID]; ok {
		return nil, err
	}
	return &coretest.FakeAgent{
		LookupFunc: func(_ context.Context, planID string, private bool) (core.PlanLookup, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			if private {
				planID += " (private)"
			}
			e.asked = append(e.asked, planID)
			if err, ok := e.lookupErrs[planID]; ok {
				return core.PlanLookup{}, err
			}
			lk, ok := e.lookups[planID]
			if !ok {
				return core.PlanLookup{}, fmt.Errorf("rs test: no scripted lookup for plan %s", planID)
			}
			return lk, nil
		},
		RetireFunc: func(_ context.Context, planID string) (core.PlanLookup, error) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.retired = append(e.retired, planID)
			if lk, ok := e.retireAnswers[planID]; ok {
				return lk, nil
			}
			return core.PlanLookup{Status: core.LookupRetired}, nil
		},
	}, nil
}

func rsNewEnv(t *testing.T) *rsEnv {
	t.Helper()
	db := fxDB(t, t.TempDir())
	clock := coretest.NewFakeClock(fxNow)
	e := &rsEnv{
		db: db, audit: audit.New(db, clock),
		lookups: map[string]core.PlanLookup{}, lookupErrs: map[string]error{},
		agentErrs: map[string]error{}, retireAnswers: map[string]core.PlanLookup{},
	}
	deps := ext.Deps{
		DB: db, Sealer: coretest.Sealer{}, Clock: clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Hosts: rsHosts{e}, Audit: e.audit, LocalHostID: "host-local",
	}
	e.h = New(Config{}).resolveHandler(deps)
	return e
}

func (e *rsEnv) exec(t *testing.T, query string, args ...any) {
	t.Helper()
	if err := e.db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// quarantine marks the change id as restore marks one that was applying, and
// gives it one lock row.
func (e *rsEnv) quarantine(t *testing.T, id string) {
	t.Helper()
	e.exec(t, `UPDATE core_changes SET state = ?, quarantined_from = ? WHERE id = ?`,
		core.StateQuarantined, core.StateApplying, id)
	fxInsertLock(t, e.db, "lock:"+id, id)
}

// change adds a quarantined single change owned, before the restore, by the engine.
func (e *rsEnv) change(t *testing.T, id, action string) {
	t.Helper()
	fxInsertChange(t, e.db, fxChange{ID: id, HostID: "h1", Action: action, State: core.StateApplying, Owner: core.OwnerEngine})
	e.quarantine(t, id)
}

// rsStep is a composite step and what the step's agent answers for its child plan.
type rsStep struct {
	fxStep
	lookup *core.PlanLookup
	err    error
}

// rsAt is the step "s-<plan>" on host whose child plan the agent answers with lk.
func rsAt(host, plan string, lk core.PlanLookup) rsStep {
	return rsStep{fxStep: fxStep{ID: "s-" + plan, HostID: host, Action: "a/v1", State: core.StepApplying, ChildPlanID: plan}, lookup: &lk}
}

func rsTerminal(state string, made int) core.PlanLookup {
	return core.PlanLookup{Status: core.LookupTerminal, State: state, Made: made}
}

var (
	rsAbsent     = core.PlanLookup{Status: core.LookupAbsent}
	rsRetiredLk  = core.PlanLookup{Status: core.LookupRetired}
	rsRunningLk  = core.PlanLookup{Status: core.LookupRunning}
	rsRecovering = core.PlanLookup{Status: core.LookupRecovering}
)

// composite adds a quarantined composite with the steps in order.
func (e *rsEnv) composite(t *testing.T, id, direction string, steps ...rsStep) {
	t.Helper()
	fx := make([]fxStep, len(steps))
	for i, s := range steps {
		fx[i] = s.fxStep
		if s.lookup != nil {
			e.lookups[s.ChildPlanID] = *s.lookup
		}
		if s.err != nil {
			e.lookupErrs[s.ChildPlanID] = s.err
		}
	}
	fxInsertComposite(t, e.db, id, core.StateApplying, direction, fx)
	e.quarantine(t, id)
}

// rollout adds a quarantined rollout that was running.
func (e *rsEnv) rollout(t *testing.T, id string) {
	t.Helper()
	fxInsertRollout(t, e.db, id, "quarantined", "h1")
	e.exec(t, `UPDATE core_rollouts SET quarantined_from = 'running' WHERE id = ?`, id)
	fxInsertLock(t, e.db, "lock:"+id, id)
}

func (e *rsEnv) resolve(id, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/controller/quarantine/"+id+"/resolve", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetPathValue("record", id)
	rec := httptest.NewRecorder()
	e.h.ServeHTTP(rec, req)
	return rec
}

func rsOutcome(outcome string) string { return `{"outcome":"` + outcome + `"}` }

// rsRow is a record's stored state.
type rsRow struct{ State, ErrorCode, Message, Owner, From string }

func (e *rsEnv) row(t *testing.T, id string) rsRow {
	t.Helper()
	var r rsRow
	err := e.db.Read().QueryRowContext(context.Background(),
		`SELECT state, coalesce(error_code, ''), coalesce(error_message, ''), recovery_owner, coalesce(quarantined_from, '')
		 FROM core_changes WHERE id = ?`, id).Scan(&r.State, &r.ErrorCode, &r.Message, &r.Owner, &r.From)
	if errors.Is(err, sql.ErrNoRows) {
		err = e.db.Read().QueryRowContext(context.Background(),
			`SELECT state, coalesce(error_code, ''), coalesce(error_message, ''), '', coalesce(quarantined_from, '')
			 FROM core_rollouts WHERE id = ?`, id).Scan(&r.State, &r.ErrorCode, &r.Message, &r.Owner, &r.From)
	}
	if err != nil {
		t.Fatalf("read record %s: %v", id, err)
	}
	return r
}

func (e *rsEnv) locks(t *testing.T, owner string) int {
	t.Helper()
	var n int
	if err := e.db.Read().QueryRowContext(context.Background(),
		`SELECT count(*) FROM core_locks WHERE owner = ?`, owner).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (e *rsEnv) auditRows(t *testing.T) []core.AuditEntry {
	t.Helper()
	got, _, err := e.audit.List(context.Background(), core.AuditQuery{Action: "controller.quarantine.resolve"})
	if err != nil {
		t.Fatal(err)
	}
	return got
}

type rsProblem struct {
	Code   string `json:"code"`
	Detail string `json:"detail"`
}

func rsDecodeProblem(t *testing.T, rec *httptest.ResponseRecorder) rsProblem {
	t.Helper()
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json (status %d, body %.300s)", ct, rec.Code, rec.Body.String())
	}
	var p rsProblem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	return p
}

type rsCase struct {
	name    string
	build   func(t *testing.T, e *rsEnv)
	outcome string

	// A refusal: status is not 200, code and detail are the problem's.
	status int
	code   string
	detail string

	// An outcome: the answered Record, the stored row and the locks left.
	kind      string
	from      string   // the Record's QuarantinedFrom
	wantLocks []string // the Record's Locks
	state     string   // the record's state afterwards
	message   string   // error_message afterwards
	errorCode string   // error_code afterwards

	retired   []string // plan ids the agents were asked to retire
	noLookups bool     // no agent was asked anything
}

func TestRsResolve(t *testing.T) {
	applied := func(host, plan string, state string) rsStep { return rsAt(host, plan, rsTerminal(state, 1)) }
	cases := []rsCase{
		{
			name:  "change: repair",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "repair",
			status: 200, kind: "change", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name:  "change: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "change: never is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "firewall.rule-add") }, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "cloud: repair",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "cloudflare.hostname-add") }, outcome: "repair",
			status: 200, kind: "cloud", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name:  "cloud: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.change(t, rsID, "tailnet.devices-authorize") }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a controller-side change is reconciled by repair",
			state: core.StateQuarantined, noLookups: true,
		},
		{
			name: "composite: repair",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p1", rsTerminal(core.StateConfirmed, 1)))
			}, outcome: "repair",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{"lock:R1"},
			state: core.StateNeedsRepair, errorCode: "core.needs_repair", message: "restore: quarantined; repair it after release",
			noLookups: true,
		},
		{
			name: "composite: done when every step is applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward",
					applied("h1", "p1", core.StateConfirmed), applied("h2", "p2", core.StateCompleted),
					applied("h1", "p3", core.StateRepaired), applied("h2", "p4", core.StateKept))
			}, outcome: "done",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateCompleted,
		},
		{
			name: "composite: done ignores a read step",
			build: func(t *testing.T, e *rsEnv) {
				read := rsStep{fxStep: fxStep{ID: "s-read", HostID: "h1", Action: "a/v1", State: core.StepRead, ChildPlanID: "p-read"}}
				e.composite(t, rsID, "forward", read, applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateCompleted,
		},
		{
			name: "composite: never when no step was applied",
			build: func(t *testing.T, e *rsEnv) {
				none := rsStep{fxStep: fxStep{ID: "s-none", HostID: "h1", Action: "a/v1", State: core.StepPending}}
				e.composite(t, rsID, "forward",
					rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-ret", rsRetiredLk), rsAt("h1", "p-abs2", rsAbsent),
					rsAt("h2", "p-rev", rsTerminal(core.StateReverted, 0)), rsAt("h1", "p-fail", rsTerminal(core.StateFailed, 0)),
					rsAt("h2", "p-int", rsTerminal(core.StateInterrupted, 0)), rsAt("h1", "p-aband", rsTerminal(core.StateAbandoned, 0)),
					none)
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs", "p-abs2"},
		},
		{
			name: "composite: never accepts retire answering absent",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent))
				e.retireAnswers["p-abs"] = rsAbsent
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs"},
		},
		{
			name: "composite: never when a retire finds a change",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-late", rsAbsent))
				e.retireAnswers["p-late"] = rsRunningLk
			}, outcome: "never",
			status: 409, code: "plan.state_changed", state: core.StateQuarantined, retired: []string{"p-late"},
		},
		{
			name: "composite: never when a made effect remains",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-made", rsTerminal(core.StateFailed, 1)))
			}, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: done when a step was not applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), rsAt("h2", "p2", rsAbsent))
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: never when a step was applied",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p1", rsAbsent), applied("h2", "p2", core.StateConfirmed))
			}, outcome: "never",
			status: 422, code: "request.invalid_input", detail: "partly applied", state: core.StateQuarantined,
		},
		{
			name: "composite: done while a step runs",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), rsAt("h2", "p-run", rsRunningLk))
			}, outcome: "done",
			status: 409, code: "core.change_busy", detail: "s-p-run", state: core.StateQuarantined,
		},
		{
			name: "composite: done while a step recovers",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h2", "p-rec", rsRecovering), applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 409, code: "core.change_busy", detail: "s-p-rec", state: core.StateQuarantined,
		},
		{
			name: "composite: never retires nothing while a step runs",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-run", rsRunningLk))
			}, outcome: "never",
			status: 409, code: "core.change_busy", detail: "s-p-run", state: core.StateQuarantined,
		},
		{
			name: "composite: an agent that is not connected",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), applied("h2", "p2", core.StateConfirmed))
				e.agentErrs["h2"] = errcode.New(errcode.HostOffline, "host h2 is offline")
			}, outcome: "done",
			status: 503, code: "host.offline", state: core.StateQuarantined,
		},
		{
			name: "composite: a lookup that fails",
			build: func(t *testing.T, e *rsEnv) {
				bad := rsAt("h1", "p-bad", rsAbsent)
				bad.err = errcode.New(errcode.HostOffline, "the session was lost")
				e.composite(t, rsID, "forward", bad)
			}, outcome: "never",
			status: 503, code: "host.offline", state: core.StateQuarantined,
		},
		{
			name: "composite: a step that runs on the controller",
			build: func(t *testing.T, e *rsEnv) {
				ctl := rsStep{fxStep: fxStep{ID: "s-ctl", Action: "a/v1", State: core.StepApplying, ChildPlanID: "p-ctl"}}
				e.composite(t, rsID, "forward", applied("h1", "p1", core.StateConfirmed), ctl)
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "s-ctl", state: core.StateQuarantined, noLookups: true,
		},
		{
			name: "composite: done on an unwinding composite",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "unwind", applied("h1", "p1", core.StateConfirmed))
			}, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "unwinding", state: core.StateQuarantined,
		},
		{
			name: "composite: never on an unwinding composite",
			build: func(t *testing.T, e *rsEnv) {
				e.composite(t, rsID, "unwind", rsAt("h1", "p-abs", rsAbsent), rsAt("h2", "p-rev", rsTerminal(core.StateReverted, 0)))
			}, outcome: "never",
			status: 200, kind: "composite", from: core.StateApplying, wantLocks: []string{}, state: core.StateAbandoned,
			message: "restore: never applied", retired: []string{"p-abs"},
		},
		{
			name:  "rollout: never",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "never",
			status: 200, kind: "rollout", from: "running", wantLocks: []string{}, state: "cancelled",
			message: "restore: never applied", noLookups: true,
		},
		{
			name:  "rollout: done is refused",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "done",
			status: 422, code: "request.invalid_input", detail: "a rollout resolves never", state: core.StateQuarantined, noLookups: true,
		},
		{
			name:  "rollout: repair is refused",
			build: func(t *testing.T, e *rsEnv) { e.rollout(t, rsID) }, outcome: "repair",
			status: 422, code: "request.invalid_input", detail: "a rollout resolves never", state: core.StateQuarantined, noLookups: true,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			e := rsNewEnv(t)
			c.build(t, e)
			locksBefore := e.locks(t, rsID)

			rec := e.resolve(rsID, rsOutcome(c.outcome))
			if rec.Code != c.status {
				t.Fatalf("status = %d, want %d (body %.300s)", rec.Code, c.status, rec.Body.String())
			}
			row := e.row(t, rsID)
			if row.State != c.state {
				t.Errorf("state = %q, want %q", row.State, c.state)
			}
			if !slices.Equal(e.retired, c.retired) {
				t.Errorf("retired plans = %v, want %v", e.retired, c.retired)
			}
			if c.noLookups && len(e.asked) != 0 {
				t.Errorf("agents were asked about %v, want no lookup", e.asked)
			}
			if slices.ContainsFunc(e.asked, func(s string) bool { return strings.HasSuffix(s, "(private)") }) {
				t.Errorf("a lookup asked for private output: %v", e.asked)
			}

			if c.status != 200 {
				p := rsDecodeProblem(t, rec)
				if p.Code != c.code || !strings.Contains(p.Detail, c.detail) {
					t.Errorf("problem = %+v, want code %s and a detail containing %q", p, c.code, c.detail)
				}
				if got := e.locks(t, rsID); got != locksBefore {
					t.Errorf("a refused resolve changed the record's locks: %d, want %d", got, locksBefore)
				}
				if rows := e.auditRows(t); len(rows) != 0 {
					t.Errorf("a refused resolve wrote %d audit rows", len(rows))
				}
				return
			}

			var got Record
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatalf("decode the record: %v", err)
			}
			if got.ID != rsID || got.Kind != c.kind || got.QuarantinedFrom != c.from {
				t.Errorf("record = %+v, want id %s, kind %s, quarantinedFrom %s", got, rsID, c.kind, c.from)
			}
			if got.Locks == nil || !slices.Equal(got.Locks, c.wantLocks) {
				t.Errorf("answered locks = %#v, want %#v (never null)", got.Locks, c.wantLocks)
			}
			if row.Message != c.message || row.ErrorCode != c.errorCode || row.From != c.from {
				t.Errorf("stored row = %+v, want error code %q, message %q, quarantined_from %q", row, c.errorCode, c.message, c.from)
			}
			if c.state == core.StateNeedsRepair && row.Owner != "" {
				t.Errorf("recovery_owner = %q, want it cleared", row.Owner)
			}
			if want := len(c.wantLocks); e.locks(t, rsID) != want {
				t.Errorf("lock rows left = %d, want %d", e.locks(t, rsID), want)
			}

			rows := e.auditRows(t)
			if len(rows) != 1 {
				t.Fatalf("audit rows = %d, want 1", len(rows))
			}
			a := rows[0]
			if a.Actor != "local:root@host-local" || a.Action != "controller.quarantine.resolve" || a.Phase != "resolve" ||
				a.Result != "ok" || a.ChangeID != rsID || a.HostID != "" || len(a.Input) != 0 {
				t.Errorf("audit row = %+v, want actor local:root@host-local, phase resolve, result ok, change %s and no input", a, rsID)
			}
		})
	}
}

func TestRsUnknownRecord(t *testing.T) {
	e := rsNewEnv(t)
	e.change(t, rsID, "firewall.rule-add")
	rec := e.resolve("NOPE", rsOutcome("repair"))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %.300s)", rec.Code, rec.Body.String())
	}
	if p := rsDecodeProblem(t, rec); p.Code != "not_found" {
		t.Errorf("code = %q, want not_found", p.Code)
	}
	if rows := e.auditRows(t); len(rows) != 0 {
		t.Errorf("an unknown record wrote %d audit rows", len(rows))
	}
}

func TestRsRecordThatIsNotQuarantinedIsStateChanged(t *testing.T) {
	e := rsNewEnv(t)
	e.change(t, rsID, "firewall.rule-add")
	fxInsertChange(t, e.db, fxChange{ID: "R2", HostID: "h1", Action: "firewall.rule-add", State: core.StateApplying})
	fxInsertChange(t, e.db, fxChange{ID: "R3", HostID: "h1", Action: "firewall.rule-add", State: core.StateNeedsRepair})

	if rec := e.resolve(rsID, rsOutcome("repair")); rec.Code != http.StatusOK {
		t.Fatalf("first resolve: status = %d, want 200 (body %.300s)", rec.Code, rec.Body.String())
	}
	for _, id := range []string{rsID, "R2", "R3"} {
		rec := e.resolve(id, rsOutcome("repair"))
		if rec.Code != http.StatusConflict {
			t.Fatalf("resolve of %s: status = %d, want 409 (body %.300s)", id, rec.Code, rec.Body.String())
		}
		if p := rsDecodeProblem(t, rec); p.Code != "plan.state_changed" {
			t.Errorf("resolve of %s: code = %q, want plan.state_changed", id, p.Code)
		}
	}
	if rows := e.auditRows(t); len(rows) != 1 {
		t.Errorf("audit rows = %d, want 1 (only the first resolve)", len(rows))
	}
}

func TestRsRefusesABadRequest(t *testing.T) {
	bodies := map[string]string{
		"empty body":      ``,
		"not JSON":        `repair`,
		"empty outcome":   `{"outcome":""}`,
		"unknown outcome": `{"outcome":"maybe"}`,
		"unknown field":   `{"outcome":"repair","force":true}`,
		"no outcome":      `{}`,
		"two values":      `{"outcome":"repair"}{"outcome":"repair"}`,
		"over 64 KiB":     `{"outcome":"` + strings.Repeat("a", 70000) + `"}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			e := rsNewEnv(t)
			e.change(t, rsID, "firewall.rule-add")
			rec := e.resolve(rsID, body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422 (body %.300s)", rec.Code, rec.Body.String())
			}
			if p := rsDecodeProblem(t, rec); p.Code != "request.invalid_input" {
				t.Errorf("code = %q, want request.invalid_input", p.Code)
			}
			if got := e.row(t, rsID).State; got != core.StateQuarantined {
				t.Errorf("state = %q, want the record untouched", got)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/13 && export TMPDIR=/root/.cache/w7-tmp/13 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run '^TestRs' ./internal/controller/backup/`
Expected: FAIL, the build fails with `New(Config{}).resolveHandler undefined (type *Backup has no field or method resolveHandler)`.

- [ ] **Step 3: Implement**

Write `resolve.go` in package `backup` with `func (b *Backup) resolveHandler(deps ext.Deps) http.Handler`. Read `quarantine_routes.go` first and use the four functions of Contracts "Route helpers" (`writeProblem`, `writeJSON`, `decodeBody`, `recordAudit`); declare no second copy of any of them. If one of the four does not exist there, stop and report NEEDS_CONTEXT naming it. Declare in `resolve.go` only the handler and unexported names starting `resolve`.

The rules below restate what the implementer cannot see elsewhere. They are the only rules; refusals are `*errcode.Error` with these client-safe texts (substitute the ids):

1. Request. Decode a `ResolveRequest` (body at most 64 KiB, unknown fields refused). An outcome other than `done`, `never` or `repair`, and every decoding failure, is `errcode.InvalidInput` "the outcome must be done, never or repair". The record id is `r.PathValue("record")`.
2. Find the record. Read the `state` of the id from `core_changes`, else from `core_rollouts`, on `deps.DB.Read()`. Neither table has it: `errcode.NotFound` "no such record". A state other than `quarantined`: `errcode.PlanStateChanged` "the record is no longer quarantined". Then `recordByID(ctx, deps.DB.Read(), id)` gives the `Record` (its `Kind` is `change`, `composite`, `cloud` or `rollout`); `!ok` is the same `plan.state_changed` refusal.
3. `change` and `cloud`. Only `repair`. `done` and `never` are `errcode.InvalidInput` "a controller-side change is reconciled by repair: resolve it repair and run darkpanel change repair <id> after release". No lookups.
4. `rollout`. Only `never`; `done` and `repair` are `errcode.InvalidInput` "a rollout resolves never; start a new rollout after release". No lookups.
5. `composite` with `repair`: as 3, no lookups. A composite with `done` or `never` is decided from its steps (`SELECT step_id, host_id, state, child_plan_id FROM core_composite_steps WHERE change_id = ? ORDER BY ordinal`) and its direction (`core_composites.direction`):
   1. A step whose state is `read` is skipped entirely: no lookup, it counts as neither applied nor not applied. A step with no child plan (`child_plan_id` NULL or empty) counts as not applied, with no lookup. A step with a child plan and an empty `host_id` runs on the controller: refuse `errcode.InvalidInput` "step <step id> runs on the controller and no agent can show whether it ran; resolve the composite repair". Check every step this way first, before any lookup.
   2. `done` on a composite whose direction is `unwind`: refuse `errcode.InvalidInput` "a composite that is unwinding cannot be resolved done; resolve it never or repair", before any lookup.
   3. Then, in ordinal order, for each remaining step: `deps.Hosts.Agent(ctx, hostID)` and `Lookup(ctx, childPlanID, false)` (never private). An error from `Agent` or `Lookup` is returned unchanged (the agent's own error, for example `host.offline`). Status `running` or `recovering`: `errcode.ChangeBusy` "step <step id> is still running on host <host id>; resolve the composite after the agent finishes". Applied: status `terminal` with state `confirmed`, `completed`, `repaired` or `kept`. Not applied: status `absent` or `retired`, or status `terminal` with state `reverted`, `failed`, `interrupted` or `abandoned` and `Made` equal to 0. Anything else is neither. Stop at the first refusal.
   4. `done` needs every non-read step applied, `never` needs every non-read step not applied; any other mix is `errcode.InvalidInput` "the composite is partly applied; resolve it repair".
   5. `never` only then retires: for each step whose lookup status was `absent`, in ordinal order, `Retire(ctx, childPlanID)` on the step's agent. An error is returned unchanged; an answer whose status is neither `retired` nor `absent` is `errcode.PlanStateChanged` "a child plan of step <step id> changed while it was being retired". Nothing is retired when any earlier rule refused.
6. Transaction. `deps.DB.Write` runs one transaction that first re-reads the record's state in the table of its kind (`quarantined`, else `errcode.PlanStateChanged`) and then updates it with `... WHERE id = ? AND state = 'quarantined'`, requiring one affected row. `updated_at` is `deps.Clock.Now().UTC().Format("2006-01-02T15:04:05.000000000Z")` (declare that layout as a constant `resolveTimeLayout`). `quarantined_from` is never changed.
   - `repair` (change, cloud, composite): `core_changes` gets `state` `needs_repair`, `error_code` `core.needs_repair`, `error_message` "restore: quarantined; repair it after release", `recovery_owner` `''`. The record's `core_locks` rows stay.
   - composite `done`: `state` `completed`, `error_code`, `error_message` and `error_cause` NULL, `recovery_owner` `''`; then `DELETE FROM core_locks WHERE owner = ?` with the id.
   - composite `never`: `state` `abandoned`, `error_message` "restore: never applied", `error_code` and `error_cause` NULL, `recovery_owner` `''`; locks deleted as above.
   - rollout `never`: `core_rollouts` gets `state` `cancelled` and `error_message` "restore: never applied"; locks owned by the id are deleted.
7. Answer `200` with the `Record` read in rule 2, with `Locks` set to a non-nil empty slice after `done` or `never` and left as read after `repair` (`Kind` and `QuarantinedFrom` unchanged).
8. Audit, after the commit: `recordAudit(context.WithoutCancel(r.Context()), deps, "controller.quarantine.resolve", "resolve", <record id>, "")` (Contracts "Audit"; `resolve` is on the `core_audit.phase` CHECK list). A refusal writes no row. A failed write is logged and the answer stays `200`.

- [ ] **Step 4: Run test to verify it passes**

Run: `mkdir -p /root/.cache/w7-tmp/13 && export TMPDIR=/root/.cache/w7-tmp/13 GOTOOLCHAIN=local && timeout 600 go test -count=1 -v -run '^TestRs' ./internal/controller/backup/`
Expected: every `TestRs...` test and subtest PASS, final line `ok  	github.com/darkraise/darkpanel/internal/controller/backup`.

Run: `export TMPDIR=/root/.cache/w7-tmp/13 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 go test -race -short -count=1 ./internal/controller/backup/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: build and vet print nothing, every test line is `ok` or `?`, lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Then `rm -rf /root/.cache/w7-tmp/13 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/resolve.go internal/controller/backup/resolve_test.go
git commit -m "feat(backup): add the quarantine resolve route"
```

### Task 14: Restore transaction

**Files:**
- Create: `internal/controller/backup/restore.go`
- Test: `internal/controller/backup/restore_test.go`

**Interfaces:**
- Consumes: Contracts "Restore and recovery" (`Restore`, `RestoreResult`, the rollback rule and the W7-R11 rule), "Restore host" (`Host`, `h.lock`, `h.checkRestore` and their refusal messages), "Journal" (`Journal`, `Phase*`, `Rename*`, `Targets`, `StagedName`, `AsideName`, `JournalPath`, `SaveJournal`, `RenameJournaled`, `Settle`, `ListJournals`), "Durable file operations" (`Ops`, `File`, `OSOps`), "Format" (`Read`, `Member`, `MemberDB`, `MemberKey`, `MaxKeyMember`; the tests use `Write`, `TestParams`), "Marking" (`Mark`), "Validation and verification" (`ValidateStaged`, `VerifyInstalled`, `Canary`, `CanaryKey`, `CanaryAD`), `quarantine.Marker` and `quarantine.MetaKey`, and in the tests "Controller database fixture" (`fxDB`, `fxKey`, `fxInsertChange`, `fxInsertLock`, `fxNow`).
- Produces: `Restore` and `RestoreResult` exactly as Contracts "Restore and recovery" states them. Task 15 also consumes the unexported `removeTx`, `installStaged`, `finishTx` and `dropOldAside` of Contracts "Restore and recovery", which this task declares in `restore.go` as follows:
  - `func removeTx(h Host, tx string) error`: removes, through `h.Ops`, each of these names (a missing one is not an error) and calls `h.Ops.SyncDir` on its directory after each, stopping at the first error: `StagedName(h.Paths.ControllerDB(), tx)`, that name plus `-wal`, that name plus `-shm` (the files SQLite makes beside a staged database), `StagedName(h.Paths.ControllerKey, tx)`, the journal path plus `.new`, and last the journal `JournalPath(h.Paths.ControllerDir, tx)`.
  - `func installStaged(h Host, journalPath string, j *Journal) error`: phase 6, safe to repeat.
  - `func finishTx(ctx context.Context, h Host, journalPath string, j *Journal) error`: phases 7 and 8.
  - `func dropOldAside(h Host, tx string) error`.
- Produces (test helpers in `restore_test.go`, consumed by Task 15): `const rtPass, rtChangeID, rtControllerUnit, rtAgentUnit`; `func rtTx(n int) string`; `type rtUnits` (field `active map[string]string`); `type rtLayout struct{ paths paths.Paths; host Host; units *rtUnits; clock *coretest.FakeClock }` with methods `dirs() []string` and `digest(t) map[string]string`; `func rtNewLayout(t *testing.T, tx string) *rtLayout`; `type rtBackup struct{ file, db, key []byte; createdAt time.Time }`; `func rtMakeBackup(t *testing.T, mutate ...func(*testing.T, *store.DB)) rtBackup`; `func rtPack(t, dbBytes, key []byte) []byte`; `func rtRestore(t, l, bk) RestoreResult`; `func rtDiff(want, got map[string]string) string`; `func rtSum([]byte) string`; `func rtRead(t, path) []byte`; `func rtFiles(t, dir) []string`; `func rtOpenRO(t, path) *sql.DB`; `func rtMeta(t, db *sql.DB, key string) string`; `func rtMarker(t, db *sql.DB) quarantine.Marker`.

**Items:** 54, 69

**Depends on:** Task 1, Task 2, Task 3, Task 4, Task 6, Task 8, Task 11
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** silent-failures

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/restore_test.go`. Every path comes from `paths.Under(t.TempDir())`; the `Host` has a fake `core.Units`, a fake `Euid`, an `Owner` that answers the test process's own ids (the install test answers 4242 and 4343 and records the chown instead of doing it) and a lock file under the temporary root; `ProductionHost` is never called. `rtMakeBackup` builds the backup from a second fixture database whose canary is sealed with a second key.

```go
package backup

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)

const (
	rtPass     = "restore passphrase"
	rtChangeID = "01RTCHANGE00000000000000AA"

	rtControllerUnit = "darkpanel-controller.service"
	rtAgentUnit      = "darkpanel-agent.service"

	rtOwnerUID = 4242
	rtOwnerGID = 4343
)

// rtTx is the n-th transaction id: upper-case ULIDs, so a higher n sorts later.
func rtTx(n int) string {
	return ulid.MustNew(ulid.Timestamp(fxNow.Add(time.Duration(n)*time.Minute)), nil).String()
}

// rtUnits answers core.Units.State from a table; the embedded nil interface
// makes any other call panic.
type rtUnits struct {
	core.Units
	active map[string]string // unit -> ActiveState; a unit not listed is inactive
}

func (u *rtUnits) State(_ context.Context, unit string) (core.UnitState, error) {
	state := "inactive"
	if s, ok := u.active[unit]; ok {
		state = s
	}
	return core.UnitState{LoadState: "loaded", ActiveState: state}, nil
}

// rtLayout is an installed controller under a temporary directory and a Host over it.
type rtLayout struct {
	paths paths.Paths
	host  Host
	units *rtUnits
	clock *coretest.FakeClock
}

// rtNewLayout installs a controller: a migrated database carrying rt_marker
// "installed" with a stale -wal and -shm beside it, and a key file. Restore
// transaction ids come from tx.
func rtNewLayout(t *testing.T, tx string) *rtLayout {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	if err := p.MkdirAll(); err != nil {
		t.Fatal(err)
	}
	db := fxDB(t, p.ControllerDir)
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('rt_marker', 'installed')`)
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	keyPath, _, _ := fxKey(t, t.TempDir())
	if err := os.Rename(keyPath, p.ControllerKey); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{p.ControllerDB() + "-wal", p.ControllerDB() + "-shm"} {
		if err := os.WriteFile(f, []byte("stale "+filepath.Base(f)), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	unit := filepath.Join(root, "units", "darkpanel-controller.service")
	run := filepath.Join(root, "run")
	for _, dir := range []string{filepath.Dir(unit), run} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	units := &rtUnits{active: map[string]string{}}
	clock := coretest.NewFakeClock(fxNow.Add(time.Hour))
	return &rtLayout{paths: p, units: units, clock: clock, host: Host{
		Paths: p, UnitFile: unit, LockFile: filepath.Join(run, "restore.lock"), Units: units,
		Euid:  func() int { return 0 },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   OSOps(), Clock: clock, NewTx: func() string { return tx },
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}}
}

func rtExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	if err := db.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.ExecContext(context.Background(), query, args...)
		return err
	}); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

func rtRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rtSum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// rtBackup is a backup file and what it holds.
type rtBackup struct {
	file      []byte
	db, key   []byte    // the plaintext members
	createdAt time.Time // the sealed canary's createdAt
}

// rtPack writes a backup file holding the given members, under rtPass.
func rtPack(t *testing.T, dbBytes, key []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := Write(&buf, rand.Reader, []byte(rtPass), TestParams, []Member{
		{Name: MemberDB, Size: int64(len(dbBytes)), Body: bytes.NewReader(dbBytes)},
		{Name: MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	return buf.Bytes()
}

// rtMakeBackup builds a backup from a second fixture database: its canary is
// sealed with a second key, rt_marker is "backup", and it holds an applying
// change with a lock plus an update reservation lock. Each mutate runs on the
// database before it is closed and packed.
func rtMakeBackup(t *testing.T, mutate ...func(*testing.T, *store.DB)) rtBackup {
	t.Helper()
	db := fxDB(t, t.TempDir())
	_, key, sealer := fxKey(t, t.TempDir())
	created := fxNow.Add(-24 * time.Hour)
	plain, err := json.Marshal(Canary{CreatedAt: created})
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal(CanaryAD, plain)
	if err != nil {
		t.Fatal(err)
	}
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES (?, ?)`, CanaryKey, base64.StdEncoding.EncodeToString(sealed))
	rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('rt_marker', 'backup')`)
	fxInsertChange(t, db, fxChange{ID: rtChangeID, HostID: "h1", Action: "firewall.rule-add", State: core.StateApplying, Owner: core.OwnerEngine})
	fxInsertLock(t, db, "firewall:h1", rtChangeID)
	fxInsertLock(t, db, "update:h1", "update-tx-1")
	for _, m := range mutate {
		m(t, db)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	dbBytes := rtRead(t, db.Path())
	return rtBackup{file: rtPack(t, dbBytes, key), db: dbBytes, key: key, createdAt: created}
}

// rtDigest maps every file under dirs to the SHA-256 of its content and every
// directory to "dir", so two states compare without printing file content.
func rtDigest(t *testing.T, dirs ...string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				out[path] = "dir"
				return nil
			}
			out[path] = rtSum(rtRead(t, path))
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	return out
}

func (l *rtLayout) dirs() []string {
	return []string{l.paths.ControllerDir, filepath.Dir(l.paths.ControllerKey)}
}

func (l *rtLayout) digest(t *testing.T) map[string]string {
	t.Helper()
	return rtDigest(t, l.dirs()...)
}

// rtDiff names what differs between two digests, "" when they are equal.
func rtDiff(want, got map[string]string) string {
	if maps.Equal(want, got) {
		return ""
	}
	var out []string
	for path, sum := range want {
		switch g, ok := got[path]; {
		case !ok:
			out = append(out, "missing "+filepath.Base(path))
		case g != sum:
			out = append(out, "changed "+filepath.Base(path))
		}
	}
	for path := range got {
		if _, ok := want[path]; !ok {
			out = append(out, "new "+filepath.Base(path))
		}
	}
	slices.Sort(out)
	return strings.Join(out, ", ")
}

// rtFiles lists the regular files directly in dir, sorted.
func rtFiles(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	slices.Sort(out)
	return out
}

func rtOpenRO(t *testing.T, path string) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro&_pragma=query_only(1)")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// rtMeta is core_meta's value for key, "" when the row is missing.
func rtMeta(t *testing.T, db *sql.DB, key string) string {
	t.Helper()
	var v string
	err := db.QueryRowContext(context.Background(), `SELECT value FROM core_meta WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return ""
	}
	if err != nil {
		t.Fatalf("read core_meta %s: %v", key, err)
	}
	return v
}

func rtMarker(t *testing.T, db *sql.DB) quarantine.Marker {
	t.Helper()
	var m quarantine.Marker
	if err := json.Unmarshal([]byte(rtMeta(t, db, quarantine.MetaKey)), &m); err != nil {
		t.Fatalf("decode the restore_quarantine marker: %v", err)
	}
	return m
}

func rtRestore(t *testing.T, l *rtLayout, bk rtBackup) RestoreResult {
	t.Helper()
	res, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	return res
}

// rtOps records the renames, chowns and chmods of a restore. Chown only
// records: the test process cannot give a file to another user.
type rtOps struct {
	Ops
	mu     sync.Mutex
	events []string
}

func (o *rtOps) note(format string, args ...any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, fmt.Sprintf(format, args...))
}

func (o *rtOps) Chown(path string, uid, gid int) error {
	o.note("chown %s %d %d", filepath.Base(path), uid, gid)
	return nil
}

func (o *rtOps) Chmod(path string, perm fs.FileMode) error {
	o.note("chmod %s %v", filepath.Base(path), perm)
	return o.Ops.Chmod(path, perm)
}

func (o *rtOps) Rename(from, to string) error {
	o.note("rename %s %s", filepath.Base(from), filepath.Base(to))
	return o.Ops.Rename(from, to)
}

func TestRtRestoreInstallsTheBackup(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	p := l.paths
	ops := &rtOps{Ops: OSOps()}
	l.host.Ops = ops
	l.host.Owner = func() (int, int, error) { return rtOwnerUID, rtOwnerGID, nil }
	originals := map[string]string{}
	for _, f := range Targets(p) {
		originals[f] = rtSum(rtRead(t, f))
	}

	res := rtRestore(t, l, bk)
	if res.Tx != tx || !res.BackupCreatedAt.Equal(bk.createdAt) {
		t.Errorf("result = %+v, want tx %s and backupCreatedAt %s", res, tx, bk.createdAt)
	}

	db := rtOpenRO(t, p.ControllerDB())
	if got := rtMeta(t, db, "rt_marker"); got != "backup" {
		t.Errorf("installed database rt_marker = %q, want the backup's", got)
	}
	m := rtMarker(t, db)
	if m.Tx != tx || !m.RestoredAt.Equal(l.clock.Now()) || !m.BackupCreatedAt.Equal(bk.createdAt) {
		t.Errorf("marker = %+v, want tx %s, restoredAt %s and backupCreatedAt %s", m, tx, l.clock.Now(), bk.createdAt)
	}
	var state string
	if err := db.QueryRowContext(context.Background(),
		`SELECT state || '/' || quarantined_from FROM core_changes WHERE id = ?`, rtChangeID).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "quarantined/applying" {
		t.Errorf("the applying change is %q, want quarantined/applying", state)
	}
	var locks []string
	rows, err := db.QueryContext(context.Background(), `SELECT key FROM core_locks ORDER BY key`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		locks = append(locks, k)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(locks, []string{"firewall:h1"}) {
		t.Errorf("locks = %v, want only the quarantined change's", locks)
	}
	if !bytes.Equal(rtRead(t, p.ControllerKey), bk.key) {
		t.Error("the installed key is not the backup's key")
	}

	for _, f := range Targets(p) {
		aside := AsideName(f, tx)
		if got := rtSum(rtRead(t, aside)); got != originals[f] {
			t.Errorf("%s does not hold the original %s", filepath.Base(aside), filepath.Base(f))
		}
	}
	for _, f := range []string{p.ControllerDB(), p.ControllerKey} {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, error %v, want 0600", filepath.Base(f), info, err)
		}
	}
	wantDir := []string{"controller.db", "controller.db-shm.pre-" + tx, "controller.db-wal.pre-" + tx, "controller.db.pre-" + tx}
	slices.Sort(wantDir)
	if got := rtFiles(t, p.ControllerDir); !slices.Equal(got, wantDir) {
		t.Errorf("controller directory files = %v, want %v (no journal, staging or -wal/-shm file)", got, wantDir)
	}
	wantKey := []string{"controller.key", "controller.key.pre-" + tx}
	if got := rtFiles(t, filepath.Dir(p.ControllerKey)); !slices.Equal(got, wantKey) {
		t.Errorf("key directory files = %v, want %v", got, wantKey)
	}

	var staged []string
	for _, e := range ops.events {
		if strings.Contains(e, ".restore-"+tx) {
			staged = append(staged, e)
		}
	}
	owner := fmt.Sprintf("%d %d", rtOwnerUID, rtOwnerGID)
	wantEvents := []string{
		"chown controller.db.restore-" + tx + " " + owner,
		"chmod controller.db.restore-" + tx + " -rw-------",
		"rename controller.db.restore-" + tx + " controller.db",
		"chown controller.key.restore-" + tx + " " + owner,
		"chmod controller.key.restore-" + tx + " -rw-------",
		"rename controller.key.restore-" + tx + " controller.key",
	}
	if !slices.Equal(staged, wantEvents) {
		t.Errorf("install steps =\n%s\nwant\n%s", strings.Join(staged, "\n"), strings.Join(wantEvents, "\n"))
	}
}

func TestRtRestoreRefusals(t *testing.T) {
	bk := rtMakeBackup(t)
	cases := []struct {
		name    string
		arrange func(t *testing.T, l *rtLayout)
		want    string
	}{
		{"not root", func(_ *testing.T, l *rtLayout) { l.host.Euid = func() int { return 1000 } },
			"a restore must run as root"},
		{"unit file missing", func(t *testing.T, l *rtLayout) {
			if err := os.Remove(l.host.UnitFile); err != nil {
				t.Fatal(err)
			}
		}, "no controller is installed here"},
		{"key file missing", func(t *testing.T, l *rtLayout) {
			if err := os.Remove(l.paths.ControllerKey); err != nil {
				t.Fatal(err)
			}
		}, "no controller is installed here"},
		{"controller running", func(_ *testing.T, l *rtLayout) { l.units.active[rtControllerUnit] = "active" },
			"darkpanel-controller.service is active"},
		{"agent running", func(_ *testing.T, l *rtLayout) { l.units.active[rtAgentUnit] = "active" },
			"darkpanel-agent.service is active"},
		{"journal exists", func(t *testing.T, l *rtLayout) {
			if err := os.WriteFile(JournalPath(l.paths.ControllerDir, rtTx(0)), []byte("{}"), 0o600); err != nil {
				t.Fatal(err)
			}
		}, "an unfinished restore exists"},
		{"lock held", func(t *testing.T, l *rtLayout) {
			release, err := l.host.lock()
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(release)
		}, "another restore or recovery holds"},
		{"owner unknown", func(_ *testing.T, l *rtLayout) {
			l.host.Owner = func() (int, int, error) { return 0, 0, errors.New("user darkpanel not found") }
		}, "user darkpanel not found"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := rtNewLayout(t, rtTx(1))
			c.arrange(t, l)
			before := l.digest(t)
			_, err := Restore(context.Background(), l.host, bytes.NewReader(bk.file), []byte(rtPass))
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("Restore error = %v, want one containing %q", err, c.want)
			}
			if diff := rtDiff(before, l.digest(t)); diff != "" {
				t.Errorf("the refused restore changed the installation: %s", diff)
			}
		})
	}
}

func TestRtRestoreRejectsABadBackup(t *testing.T) {
	good := rtMakeBackup(t)
	_, otherKey, _ := fxKey(t, t.TempDir())
	is := func(target error) func(error) bool {
		return func(err error) bool { return errors.Is(err, target) }
	}
	cases := []struct {
		name string
		file []byte
		pass string
		ok   func(error) bool
	}{
		{"wrong passphrase", good.file, "not the passphrase", is(ErrAuth)},
		{"truncated file", good.file[:len(good.file)-100], rtPass,
			func(err error) bool { return errors.Is(err, ErrTruncated) || errors.Is(err, ErrAuth) }},
		{"not a backup", append([]byte("NOPE"), make([]byte, 200)...), rtPass, is(ErrNotBackup)},
		{"database that is not SQLite", rtPack(t, []byte("this is not a database"), good.key), rtPass,
			func(err error) bool { return err != nil }},
		{"key that does not open the canary", rtPack(t, good.db, otherKey), rtPass,
			func(err error) bool { return err != nil }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := rtNewLayout(t, rtTx(1))
			before := l.digest(t)
			_, err := Restore(context.Background(), l.host, bytes.NewReader(c.file), []byte(c.pass))
			if err == nil || !c.ok(err) {
				t.Fatalf("Restore error = %v, want the failure of this row", err)
			}
			if diff := rtDiff(before, l.digest(t)); diff != "" {
				t.Errorf("the failed restore left changes behind (originals, staging or a journal): %s", diff)
			}
		})
	}
}

func TestRtSecondRestoreRemovesTheFirstRestoresAsides(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	bk := rtMakeBackup(t)
	p := l.paths

	rtRestore(t, l, bk)
	installedByFirst := rtSum(rtRead(t, p.ControllerDB()))
	l.host.NewTx = func() string { return tx2 }
	res := rtRestore(t, l, bk)
	if res.Tx != tx2 {
		t.Fatalf("second restore tx = %s, want %s", res.Tx, tx2)
	}

	wantDir := []string{"controller.db", "controller.db.pre-" + tx2}
	if got := rtFiles(t, p.ControllerDir); !slices.Equal(got, wantDir) {
		t.Errorf("controller directory files = %v, want %v (the first restore's .pre files are gone)", got, wantDir)
	}
	wantKey := []string{"controller.key", "controller.key.pre-" + tx2}
	if got := rtFiles(t, filepath.Dir(p.ControllerKey)); !slices.Equal(got, wantKey) {
		t.Errorf("key directory files = %v, want %v", got, wantKey)
	}
	if got := rtSum(rtRead(t, AsideName(p.ControllerDB(), tx2))); got != installedByFirst {
		t.Error("the second restore did not set aside the database the first one installed")
	}
	if got := rtMarker(t, rtOpenRO(t, p.ControllerDB())).Tx; got != tx2 {
		t.Errorf("installed marker tx = %s, want %s", got, tx2)
	}
}

func TestRtRestoreMigratesAnOlderBackupWithoutAnotherCopy(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	older := rtMakeBackup(t, func(t *testing.T, db *store.DB) {
		rtExec(t, db, `DROP TABLE tailnet_keys`)
		rtExec(t, db, `DROP TABLE tailnet_accounts`)
		rtExec(t, db, `DELETE FROM core_migrations WHERE module = 'tailnet'`)
		rtExec(t, db, `INSERT INTO core_meta (key, value) VALUES ('binary_version', '0.0.1')
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`)
	})
	dirsOf := func(m map[string]string) []string {
		var out []string
		for path, sum := range m {
			if sum == "dir" {
				out = append(out, path)
			}
		}
		slices.Sort(out)
		return out
	}
	before := dirsOf(l.digest(t))

	rtRestore(t, l, older)

	if after := dirsOf(l.digest(t)); !slices.Equal(before, after) {
		t.Errorf("directories before %v, after %v: the migration of the staged database made a copy of it", before, after)
	}
	db := rtOpenRO(t, l.paths.ControllerDB())
	var n int
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM tailnet_keys`).Scan(&n); err != nil {
		t.Errorf("the tailnet tables were not migrated into the restored database: %v", err)
	}
	if err := db.QueryRowContext(context.Background(), `SELECT count(*) FROM core_migrations WHERE module = 'tailnet'`).Scan(&n); err != nil || n != 1 {
		t.Errorf("tailnet migrations recorded = %d (error %v), want 1", n, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/14 && export TMPDIR=/root/.cache/w7-tmp/14 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run '^TestRt' ./internal/controller/backup/`
Expected: FAIL, the build fails with `undefined: Restore` and `undefined: RestoreResult`.

- [ ] **Step 3: Implement**

Write `restore.go` in package `backup`. Imports include `internal/buildinfo`, `internal/modules`, `internal/core/store` and `internal/controller/quarantine`. All file work goes through `h.Ops`; the only direct file calls are the database fsync of rule 5 and `os.ReadDir` in `dropOldAside`.

`Restore(ctx, h, in, passphrase)`:

1. Refusals first. Call `h.checkRestore(ctx)` and return its error unchanged. Then `uid, gid, err := h.Owner()`, wrapping a failure as `backup: look up the owner of the controller files: %w`, before anything is written. Then `release, err := h.lock()` (error unchanged) with `defer release()`. `Restore` does not check `h.Euid` or `h.Units` itself. Defer `clear` of the passphrase.
2. Begin. `tx := h.NewTx()`, `journalPath := JournalPath(h.Paths.ControllerDir, tx)`, `j := Journal{Format: 1, Tx: tx, Phase: PhaseBegun}`, `SaveJournal(h.Ops, journalPath, j)`. Every journal change below sets `j.Phase` (and the other fields) and calls `SaveJournal` again. Every error up to and including the save of `PhaseMovedAside` (rule 7) returns `errors.Join(err, removeTx(h, tx))`: the journal and every file of the transaction go, the installation is untouched. An error from the first `RenameJournaled` of phase 5 on keeps the journal and is returned as `backup: restore <tx> stopped after it began to move the installation aside; run darkpanel controller restore --recover: %w`.
3. Phase 1, `PhaseStaged`. `Read(in, passphrase, sink)`. The sink for `MemberDB` creates `StagedName(h.Paths.ControllerDB(), tx)` and for `MemberKey` `StagedName(h.Paths.ControllerKey, tx)`, each with `h.Ops.Create(path, 0o600)`, `io.Copy` of the body, `Sync` and `Close` (close on every path). The key member is first read fully into memory, refused when its size is over `MaxKeyMember`, and written from there; the bytes are kept for rule 4 and cleared when `Restore` returns. `Read`'s error is returned as it is, so `errors.Is(err, ErrAuth)`, `ErrTruncated` and `ErrNotBackup` work. After `Read` returns nil, `SyncDir` both staged files' directories, then save `PhaseStaged`.
4. Phase 2, `PhaseValidated`. `createdAt, err := ValidateStaged(ctx, <staged database>, key)`; save `PhaseValidated`.
5. Phase 3, `PhaseQuarantined`. In a function `restoreQuarantine(ctx, h, tx, createdAt)`: `store.Open` the staged database; if anything fails below, close it before returning. Write `core_meta.binary_version = buildinfo.Version` first (upsert): `store.Migrate` copies the database with `VACUUM INTO` under `BackupRoot` whenever the backup came from another binary version and migrations are pending, and the backup file already is the pre-migration copy; a copy would add a directory no rollback removes. Then `store.Migrate(ctx, db, store.MigrateOptions{Side: store.SideController, BinaryVersion: buildinfo.Version, BackupRoot: filepath.Join(h.Paths.ControllerDir, "restore-"+tx+".nobackup"), Keep: 1, Now: h.Clock.Now()}, coreSet, sets)`: the root is never created. The sets are built as `migrateController` in `internal/controller/server/server.go` builds them: `store.CoreMigrations(store.SideController)` and one `store.MigrationSet{Module: man.ID, FS: fsys}` for each module of `modules.All()` whose `Manifest().Migrations[core.SideController]` is non-nil. A returned error fails the restore; each `Result.Failed` entry is logged with `h.Log.Warn` naming only the module (the error may quote SQL) and the restore goes on, as the server does. Then `Mark(ctx, db, quarantine.Marker{Tx: tx, RestoredAt: h.Clock.Now().UTC(), BackupCreatedAt: createdAt})`. Then leave WAL mode: SQLite refuses `journal_mode=DELETE` while another connection is open, so first `db.Read().Close()`, then on `db.Writer()` run `PRAGMA wal_checkpoint(TRUNCATE)` and `PRAGMA journal_mode=DELETE` (the answer must be `delete`), then `db.Close()`. Then `h.Ops.Remove` the staged database's `-wal` and `-shm` names (`StagedName(db, tx)+"-wal"`, `+"-shm"`), fsync the staged database with `os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW, 0)`, `Sync`, `Close` (`Ops` has no way to reopen an existing file; SQLite's `synchronous=FULL` already made each commit durable), and `SyncDir` the controller directory. Save `PhaseQuarantined`.
6. Phase 4, `PhaseJournaled`. `j.Files` gets one `JournalFile{Path, Existed}` for each of `Targets(h.Paths)` in that order, `Existed` from `h.Ops.Exists`. Save.
7. Phase 5, `PhaseMovedAside`. Set the phase and save, then for each `j.Files` entry with `Existed`, in order, `RenameJournaled(h.Ops, journalPath, &j, PhaseMovedAside, f.Path, AsideName(f.Path, tx))`.
8. Phase 6, `PhaseInstalled`. Set the phase and save before any staged file moves, then `installStaged(h, journalPath, &j)`.
9. Phases 7 and 8. `finishTx(ctx, h, journalPath, &j)`. Return `RestoreResult{Tx: tx, BackupCreatedAt: createdAt}`.

`installStaged(h, journalPath, j)`: `uid, gid, err := h.Owner()`; then for the staged database and then the staged key, with `from` the staged name and `to` the installed path: `done, err := Settle(h.Ops, Rename{From: from, To: to})`. An error is returned as `backup: journal <journalPath>: <from> and <to>: %w` (it wraps `ErrAmbiguousRename`). When `done`, go on. Otherwise `h.Ops.Chown(from, uid, gid)`, `h.Ops.Chmod(from, 0o600)`, then `RenameJournaled(h.Ops, journalPath, j, PhaseInstalled, from, to)`. Chown and chmod come before the rename, so the installed file never has the wrong owner or mode, even after a crash.

`finishTx(ctx, h, journalPath, j)`: `VerifyInstalled(ctx, h.Paths.ControllerDB(), j.Tx)`; on error return `backup: restore <tx>: the installed database failed verification and the journal <journalPath> is kept: %w` and leave the journal. On success set `PhaseVerified`, save, then `removeTx(h, j.Tx)` (the staged names are gone by then; it removes the journal last), then `dropOldAside(h, j.Tx)`.

`dropOldAside(h, tx)`: for each of `Targets(h.Paths)`, list the directory of the target with `os.ReadDir` and `h.Ops.Remove` every entry named `<target base name>.pre-<other>` where `<other>` is not `tx`; `SyncDir` each directory that lost a file. This is the removal of earlier restores' set-aside files after a restore completes; the entry for `controller.db` must not match `controller.db-wal.pre-...`, which is the next target's.

Keep exported documentation on `Restore` and `RestoreResult`; comments elsewhere only for a non-obvious reason (the binary-version write, the reader close before `journal_mode`, the fsync outside `Ops`).

- [ ] **Step 4: Run test to verify it passes**

Run: `mkdir -p /root/.cache/w7-tmp/14 && export TMPDIR=/root/.cache/w7-tmp/14 GOTOOLCHAIN=local && timeout 600 go test -count=1 -v -run '^TestRt' ./internal/controller/backup/`
Expected: every `TestRt...` test and subtest PASS, final line `ok  	github.com/darkraise/darkpanel/internal/controller/backup`.

Run: `export TMPDIR=/root/.cache/w7-tmp/14 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 go test -race -short -count=1 ./internal/controller/backup/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: build and vet print nothing, every test line is `ok` or `?`, lint prints `0 issues.`, `go mod tidy -diff` prints nothing. Then `rm -rf /root/.cache/w7-tmp/14 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/restore.go internal/controller/backup/restore_test.go
git commit -m "feat(backup): add the crash-safe restore"
```

### Task 15: Restore recovery and the crash matrix

**Files:**
- Create: `internal/controller/backup/recover.go`
- Test: `internal/controller/backup/recover_test.go`, `internal/controller/backup/recover_crash_test.go`

**Interfaces:**
- Consumes: Contracts "Restore and recovery" (`Recover` and the rollback rule), "Restore host" (`Host`, `h.lock` and its refusal message), "Journal" (`Journal`, `JournalFile`, `Rename`, `Phase*`, `Rename*`, `Targets`, `AsideName`, `StagedName`, `JournalPath`, `SaveJournal`, `ReadJournal`, `ListJournals`, `RenameJournaled`, `Settle`, `ErrAmbiguousRename`), "Durable file operations" (`Ops`, `OSOps`; in the tests `newCrashOps`, `crashOps.Calls`, `crashOps.Crashed`), "Validation and verification" (`ErrNotQuarantined`), and from Task 14 `Restore`, `RestoreResult` and the unexported `removeTx(h Host, tx string) error`, `installStaged(h Host, journalPath string, j *Journal) error`, `finishTx(ctx context.Context, h Host, journalPath string, j *Journal) error`. The tests also use Task 14's helpers: `rtPass`, `rtControllerUnit`, `rtAgentUnit`, `rtTx(n)`, `rtLayout` (fields `paths`, `host`, `units`, `clock`; methods `dirs()`, `digest(t)`), `rtNewLayout(t, tx)`, `rtBackup` (fields `file`, `db`, `key`, `createdAt`), `rtMakeBackup(t)`, `rtRestore(t, l, bk)`, `rtDiff`, `rtSum`, `rtRead`, `rtFiles`, `rtOpenRO`, `rtMeta`, `rtMarker`, and `fxDB` from Contracts "Controller database fixture".
- Produces: `func Recover(ctx context.Context, h Host) error` exactly as Contracts "Restore and recovery" states it; Task 17 calls it from `darkpanel controller restore --recover`. Unexported: `rollbackTx(h Host, journalPath string, j *Journal) error` and `recoverOne(ctx context.Context, h Host, journalPath string) error`.

**Items:** 54, 69

**Depends on:** Task 2, Task 3, Task 4, Task 6, Task 11, Task 14
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** silent-failures

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/recover_test.go` and `internal/controller/backup/recover_crash_test.go`. The first holds the helpers that save and reset a layout, the timeline of a restore's journal states (a spy over `Ops` that reads the journal after every rename and removal and numbers the calls as `crashOps` does), the end-state assertions and six tests: no journal takes no lock; rollback before phase 6; completion from phase 6 with both services reported active (recovery never checks them); a hand-made installed database without the quarantine flag keeps the journal and fails twice the same way; an ambiguous rename keeps both journals and names the journal and both names; two journals are processed oldest first. The second holds the matrices:

- `TestRcCrashMatrix`: one successful restore is counted (`total` Ops calls), then for every call number and both sides (before, after) the layout is reset to its saved original state, `Restore` runs under `newCrashOps`, and `Recover` runs under `OSOps`. The expected end state comes from the journal the crash left: phase `installed` or `verified` (or no journal and an installed database that differs from the original) means the restored, quarantined installation with only `.pre-<tx>` files carrying the transaction id; anything else means exactly the original installation, tolerating only the `restore-<tx>.journal.new` file a crash inside the very first journal save leaves (no journal names it, so recovery cannot). It also checks that no original is ever lost, that a journal in phase 6 is never written while an original is still in place, that the sequence of expected states never goes from restored back to rolled back, and that a second `Recover` changes nothing.
- `TestRcCrashInsideRecover`: for one crash point per distinct journal state of the restore, every Ops call of `Recover` on both sides, followed by another `Recover`, reaches the same end state.
- `TestRcTwoRestoresWithACrashInTheSecond`: restore twice, crash the second at about twenty call numbers on both sides; a rolled-back second restore leaves the first restore's installation and `.pre-<tx1>` files exactly as they were, a completed one leaves the first restore's files at `.pre-<tx2>` and has removed the `.pre-<tx1>` files unless the crash came after its journal was removed.

`go test -short` strides the matrices (every 7th call number, every 3rd sample); the commands below run the full matrices once and the race detector with `-short`.

```go
package backup

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
)

// rcFile is a saved file.
type rcFile struct {
	data []byte
	mode fs.FileMode
}

// rcTree is a saved copy of the two directories of a layout, so a test can
// return to a state without redoing the work that made it.
type rcTree struct {
	roots  []string
	dirs   []string // every directory below the roots, parents first
	files  map[string]rcFile
	digest map[string]string
}

func rcSave(t *testing.T, l *rtLayout) rcTree {
	t.Helper()
	tree := rcTree{roots: l.dirs(), files: map[string]rcFile{}}
	for _, root := range tree.roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if path != root {
					tree.dirs = append(tree.dirs, path)
				}
				return nil
			}
			info, err := d.Info()
			if err != nil {
				return err
			}
			tree.files[path] = rcFile{data: rtRead(t, path), mode: info.Mode().Perm()}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	tree.digest = l.digest(t)
	return tree
}

// rcReset empties the layout's directories and writes tree back.
func rcReset(t *testing.T, l *rtLayout, tree rcTree) {
	t.Helper()
	for _, root := range tree.roots {
		entries, err := os.ReadDir(root)
		if err != nil {
			t.Fatal(err)
		}
		for _, e := range entries {
			if err := os.RemoveAll(filepath.Join(root, e.Name())); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, d := range tree.dirs {
		if err := os.MkdirAll(d, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	for path, f := range tree.files {
		if err := os.WriteFile(path, f.data, 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, f.mode); err != nil {
			t.Fatal(err)
		}
	}
}

// rcPoint is the journal on disk after one rename or removal of a restore.
type rcPoint struct {
	call    int // the number of that call among the restore's Ops calls
	journal Journal
}

// rcSpyOps counts calls through a never-crashing crashOps and reads the
// journal after every rename and removal.
type rcSpyOps struct {
	Ops
	counter *crashOps
	dir     string
	points  []rcPoint
}

func (s *rcSpyOps) observe() {
	js, err := ListJournals(s.dir)
	if err != nil || len(js) != 1 {
		return
	}
	j, err := ReadJournal(js[0])
	if err != nil {
		return
	}
	s.points = append(s.points, rcPoint{call: s.counter.Calls(), journal: j})
}

func (s *rcSpyOps) Rename(from, to string) error {
	err := s.Ops.Rename(from, to)
	s.observe()
	return err
}

func (s *rcSpyOps) Remove(path string) error {
	err := s.Ops.Remove(path)
	s.observe()
	return err
}

// rcTimeline runs one successful restore and returns the journal states it
// went through, then puts the layout back to tree.
func rcTimeline(t *testing.T, l *rtLayout, bk rtBackup, tree rcTree) []rcPoint {
	t.Helper()
	counter := newCrashOps(OSOps(), math.MaxInt, false)
	spy := &rcSpyOps{Ops: counter, counter: counter, dir: l.paths.ControllerDir}
	h := l.host
	h.Ops = spy
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	rcReset(t, l, tree)
	return spy.points
}

// rcDone counts the journal's finished renames of phase.
func rcDone(j Journal, phase Phase) int {
	n := 0
	for _, r := range j.Renames {
		if r.Phase == phase && r.State == RenameDone {
			n++
		}
	}
	return n
}

// rcFind returns the call number after which the journal first matches pred.
func rcFind(t *testing.T, points []rcPoint, pred func(Journal) bool) int {
	t.Helper()
	for _, p := range points {
		if pred(p.journal) {
			return p.call
		}
	}
	t.Fatal("no state of the restore matches")
	return 0
}

// rcCrashRestore runs Restore with a crash before or after Ops call number at.
func rcCrashRestore(t *testing.T, l *rtLayout, bk rtBackup, at int, before bool) {
	t.Helper()
	ops := newCrashOps(OSOps(), at, before)
	h := l.host
	h.Ops = ops
	_, _ = Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass))
	if !ops.Crashed() {
		t.Fatalf("Restore made %d calls and never reached call %d", ops.Calls(), at)
	}
}

func rcExists(path string) bool {
	_, err := os.Lstat(path)
	return err == nil
}

// rcSumAt is the SHA-256 of the file at path, "" when it cannot be read.
func rcSumAt(path string) string {
	b, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		return ""
	}
	return rtSum(b)
}

// rcTxNames lists the files of both directories whose name carries tx, sorted.
func rcTxNames(t *testing.T, l *rtLayout, tx string) []string {
	t.Helper()
	var out []string
	for _, dir := range l.dirs() {
		for _, name := range rtFiles(t, dir) {
			if strings.Contains(name, tx) {
				out = append(out, name)
			}
		}
	}
	slices.Sort(out)
	return out
}

// rcExpectRestored says which end state the layout's journal commands: true
// when phase 6 has begun (or the transaction already finished), false when
// the restore must roll back (or never began).
func rcExpectRestored(t *testing.T, l *rtLayout, baseline rcTree) bool {
	t.Helper()
	js, err := ListJournals(l.paths.ControllerDir)
	if err != nil {
		t.Fatal(err)
	}
	switch len(js) {
	case 0:
		return rcSumAt(l.paths.ControllerDB()) != baseline.digest[l.paths.ControllerDB()]
	case 1:
		j, err := ReadJournal(js[0])
		if err != nil {
			t.Fatal(err)
		}
		return j.Phase == PhaseInstalled || j.Phase == PhaseVerified
	}
	t.Fatalf("%d journals after one restore", len(js))
	return false
}

// rcAssertNothingLost checks that every original of baseline is still in
// place or at its set-aside name.
func rcAssertNothingLost(t *testing.T, l *rtLayout, baseline rcTree, tx string) {
	t.Helper()
	for _, f := range Targets(l.paths) {
		orig, existed := baseline.files[f]
		if !existed {
			continue
		}
		want := rtSum(orig.data)
		if rcSumAt(f) != want && rcSumAt(AsideName(f, tx)) != want {
			t.Errorf("the original %s is neither in place nor at its set-aside name", filepath.Base(f))
		}
	}
}

// rcAssertAsideBeforeInstall checks that a journal in phase 6 or later is
// never written while an original is still in place.
func rcAssertAsideBeforeInstall(t *testing.T, l *rtLayout) {
	t.Helper()
	js, err := ListJournals(l.paths.ControllerDir)
	if err != nil || len(js) != 1 {
		return
	}
	j, err := ReadJournal(js[0])
	if err != nil || (j.Phase != PhaseInstalled && j.Phase != PhaseVerified) {
		return
	}
	for _, f := range j.Files {
		if f.Existed && !rcExists(AsideName(f.Path, j.Tx)) {
			t.Errorf("the journal is in phase %s but %s is not at its set-aside name", j.Phase, filepath.Base(f.Path))
		}
	}
}

// rcAssertOriginals checks the layout equals baseline. With tolerateNew, the
// restore-<tx>.journal.new file of a crash inside the transaction's very
// first journal save is ignored: no journal names it, so nothing can
// attribute it.
func rcAssertOriginals(t *testing.T, l *rtLayout, baseline rcTree, tx string, tolerateNew bool) {
	t.Helper()
	got := l.digest(t)
	if tolerateNew {
		delete(got, JournalPath(l.paths.ControllerDir, tx)+".new")
	}
	if diff := rtDiff(baseline.digest, got); diff != "" {
		t.Errorf("the installation is not the original one: %s", diff)
	}
	if js, err := ListJournals(l.paths.ControllerDir); err != nil || len(js) != 0 {
		t.Errorf("journals left: %v (error %v)", js, err)
	}
}

// rcAssertRestored checks the layout holds transaction tx's restore of bk on
// top of baseline: the backup's key and quarantined database installed,
// baseline's files at their .pre-<tx> names, and no other name carrying tx.
func rcAssertRestored(t *testing.T, l *rtLayout, bk rtBackup, baseline rcTree, tx string) {
	t.Helper()
	p := l.paths
	if !bytes.Equal(rtRead(t, p.ControllerKey), bk.key) {
		t.Error("the installed key is not the backup's key")
	}
	db := rtOpenRO(t, p.ControllerDB())
	if got := rtMarker(t, db).Tx; got != tx {
		t.Errorf("installed marker tx = %q, want %q", got, tx)
	}
	if got := rtMeta(t, db, "rt_marker"); got != "backup" {
		t.Errorf("installed database rt_marker = %q, want the backup's", got)
	}
	var wantNames []string
	for _, f := range Targets(p) {
		orig, existed := baseline.files[f]
		if !existed {
			continue
		}
		wantNames = append(wantNames, filepath.Base(AsideName(f, tx)))
		if rcSumAt(AsideName(f, tx)) != rtSum(orig.data) {
			t.Errorf("%s does not hold the original %s", filepath.Base(AsideName(f, tx)), filepath.Base(f))
		}
	}
	slices.Sort(wantNames)
	if got := rcTxNames(t, l, tx); !slices.Equal(got, wantNames) {
		t.Errorf("files carrying the transaction id = %v, want only %v", got, wantNames)
	}
	for _, f := range []string{p.ControllerDB(), p.ControllerKey} {
		if info, err := os.Stat(f); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("%s: mode %v, error %v, want 0600", filepath.Base(f), info, err)
		}
	}
	if js, err := ListJournals(p.ControllerDir); err != nil || len(js) != 0 {
		t.Errorf("journals left: %v (error %v)", js, err)
	}
}

// rcAssertEnd checks the end state a recovery must reach.
func rcAssertEnd(t *testing.T, l *rtLayout, bk rtBackup, baseline rcTree, tx string, restored bool) {
	t.Helper()
	if restored {
		rcAssertRestored(t, l, bk, baseline, tx)
		return
	}
	rcAssertOriginals(t, l, baseline, tx, true)
}

// rcOrderOps records the removals in order.
type rcOrderOps struct {
	Ops
	mu      sync.Mutex
	removed []string
}

func (o *rcOrderOps) Remove(path string) error {
	o.mu.Lock()
	o.removed = append(o.removed, path)
	o.mu.Unlock()
	return o.Ops.Remove(path)
}

func TestRcWithoutAJournalTakesNoLock(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	release, err := l.host.lock()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(release)

	before := l.digest(t)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover with no journal while the lock is held elsewhere: %v", err)
	}
	if diff := rtDiff(before, l.digest(t)); diff != "" {
		t.Errorf("Recover with no journal changed the installation: %s", diff)
	}

	if err := SaveJournal(OSOps(), JournalPath(l.paths.ControllerDir, tx), Journal{Format: 1, Tx: tx, Phase: PhaseBegun}); err != nil {
		t.Fatal(err)
	}
	before = l.digest(t)
	err = Recover(context.Background(), l.host)
	if err == nil || !strings.Contains(err.Error(), "another restore or recovery holds") {
		t.Fatalf("Recover with a journal while the lock is held = %v, want the lock refusal", err)
	}
	if diff := rtDiff(before, l.digest(t)); diff != "" {
		t.Errorf("a refused Recover changed the installation: %s", diff)
	}
}

func TestRcRollsBackBeforePhaseSix(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseMovedAside && rcDone(j, PhaseMovedAside) == 1 })

	rcCrashRestore(t, l, bk, at, false)
	if !rcExists(AsideName(l.paths.ControllerDB(), tx)) || rcExists(l.paths.ControllerDB()) {
		t.Fatal("the crash did not leave the database at its set-aside name")
	}

	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rcAssertOriginals(t, l, pristine, tx, false)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	rcAssertOriginals(t, l, pristine, tx, false)
}

func TestRcCompletesFromPhaseSixWithoutCheckingServices(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseInstalled && rcDone(j, PhaseInstalled) == 1 })

	rcCrashRestore(t, l, bk, at, false)
	if !rcExists(StagedName(l.paths.ControllerKey, tx)) || rcExists(StagedName(l.paths.ControllerDB(), tx)) {
		t.Fatal("the crash did not leave the database installed and the key staged")
	}
	l.units.active[rtControllerUnit] = "active"
	l.units.active[rtAgentUnit] = "active"

	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	rcAssertRestored(t, l, bk, pristine, tx)
	settled := l.digest(t)
	if err := Recover(context.Background(), l.host); err != nil {
		t.Fatalf("second Recover: %v", err)
	}
	if diff := rtDiff(settled, l.digest(t)); diff != "" {
		t.Errorf("a second Recover changed the installation: %s", diff)
	}
}

func TestRcFailedVerificationKeepsTheJournal(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)
	at := rcFind(t, points, func(j Journal) bool { return j.Phase == PhaseInstalled && rcDone(j, PhaseInstalled) == 2 })
	rcCrashRestore(t, l, bk, at, false)

	// A database made by hand carries no restore_quarantine flag.
	other := fxDB(t, t.TempDir())
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(l.paths.ControllerDB(), rtRead(t, other.Path()), 0o600); err != nil {
		t.Fatal(err)
	}

	for i := range 2 {
		err := Recover(context.Background(), l.host)
		if !errors.Is(err, ErrNotQuarantined) {
			t.Fatalf("Recover %d = %v, want ErrNotQuarantined", i+1, err)
		}
		js, lerr := ListJournals(l.paths.ControllerDir)
		if lerr != nil || len(js) != 1 {
			t.Fatalf("journals after the failed verification = %v (error %v), want the journal kept", js, lerr)
		}
		for _, f := range Targets(l.paths) {
			if _, existed := pristine.files[f]; existed && !rcExists(AsideName(f, tx)) {
				t.Errorf("%s was removed although verification failed", filepath.Base(AsideName(f, tx)))
			}
		}
	}
}

func TestRcAmbiguousRenameKeepsTheJournals(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	p := l.paths
	aside := AsideName(p.ControllerDB(), tx1)
	if err := os.WriteFile(aside, []byte("set aside"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The database and its set-aside name both exist: no one can tell which
	// is the original.
	first := Journal{
		Format: 1, Tx: tx1, Phase: PhaseMovedAside,
		Files: []JournalFile{
			{Path: p.ControllerDB(), Existed: true}, {Path: p.ControllerDB() + "-wal", Existed: true},
			{Path: p.ControllerDB() + "-shm", Existed: true}, {Path: p.ControllerKey, Existed: true},
		},
		Renames: []Rename{{Phase: PhaseMovedAside, From: p.ControllerDB(), To: aside, State: RenameIntended}},
	}
	firstPath := JournalPath(p.ControllerDir, tx1)
	if err := SaveJournal(OSOps(), firstPath, first); err != nil {
		t.Fatal(err)
	}
	// A younger transaction that recovery must not reach after the first fails.
	staged := StagedName(p.ControllerKey, tx2)
	if err := os.WriteFile(staged, []byte("staged"), 0o600); err != nil {
		t.Fatal(err)
	}
	secondPath := JournalPath(p.ControllerDir, tx2)
	if err := SaveJournal(OSOps(), secondPath, Journal{Format: 1, Tx: tx2, Phase: PhaseBegun}); err != nil {
		t.Fatal(err)
	}
	dbSum := rcSumAt(p.ControllerDB())

	err := Recover(context.Background(), l.host)
	if !errors.Is(err, ErrAmbiguousRename) {
		t.Fatalf("Recover = %v, want ErrAmbiguousRename", err)
	}
	for _, want := range []string{firstPath, aside, p.ControllerDB()} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not name %s: %v", want, err)
		}
	}
	js, lerr := ListJournals(p.ControllerDir)
	if lerr != nil || !slices.Equal(js, []string{firstPath, secondPath}) {
		t.Errorf("journals = %v (error %v), want both kept", js, lerr)
	}
	if rcSumAt(p.ControllerDB()) != dbSum || rcSumAt(aside) != rtSum([]byte("set aside")) {
		t.Error("recovery touched the two files whose rename it could not settle")
	}
	if !rcExists(staged) {
		t.Error("recovery went on to the second transaction after the first failed")
	}
}

func TestRcProcessesJournalsOldestFirst(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	p := l.paths
	pristine := l.digest(t)
	for _, tx := range []string{tx2, tx1} {
		if err := os.WriteFile(StagedName(p.ControllerKey, tx), []byte("staged"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := SaveJournal(OSOps(), JournalPath(p.ControllerDir, tx), Journal{Format: 1, Tx: tx, Phase: PhaseBegun}); err != nil {
			t.Fatal(err)
		}
	}
	ops := &rcOrderOps{Ops: OSOps()}
	h := l.host
	h.Ops = ops

	if err := Recover(context.Background(), h); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	if diff := rtDiff(pristine, l.digest(t)); diff != "" {
		t.Errorf("both transactions should be rolled back to the installation: %s", diff)
	}
	last1, first2 := -1, -1
	for i, path := range ops.removed {
		if strings.Contains(path, tx1) {
			last1 = i
		}
		if strings.Contains(path, tx2) && first2 < 0 {
			first2 = i
		}
	}
	if last1 < 0 || first2 < 0 || last1 > first2 {
		t.Errorf("removals of the older transaction end at %d, the younger begin at %d; want older first (%v)", last1, first2, ops.removed)
	}
}
```

```go
package backup

import (
	"bytes"
	"context"
	"fmt"
	"math"
	"slices"
	"testing"
)

func rcSide(before bool) string {
	if before {
		return "before"
	}
	return "after"
}

// TestRcCrashMatrix stops one restore at every Ops call, on both sides of
// it, and recovers. The end state must be exactly the original installation
// when the crash came before phase 6 began, and the restored, quarantined
// installation from then on; a second recovery changes nothing.
func TestRcCrashMatrix(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)

	counter := newCrashOps(OSOps(), math.MaxInt, false)
	h := l.host
	h.Ops = counter
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	total := counter.Calls()
	rcAssertRestored(t, l, bk, pristine, tx)
	t.Logf("one restore makes %d Ops calls", total)

	step := 1
	if testing.Short() {
		step = 7
	}
	restoredFrom := 0
	for n := 1; n <= total; n += step {
		for _, before := range []bool{true, false} {
			t.Run(fmt.Sprintf("call %03d %s", n, rcSide(before)), func(t *testing.T) {
				rcReset(t, l, pristine)
				rcCrashRestore(t, l, bk, n, before)
				rcAssertNothingLost(t, l, pristine, tx)
				rcAssertAsideBeforeInstall(t, l)

				restored := rcExpectRestored(t, l, pristine)
				switch {
				case restored && restoredFrom == 0:
					restoredFrom = n
				case !restored && restoredFrom != 0:
					t.Errorf("a crash at call %d must roll back although one at call %d completes", n, restoredFrom)
				}

				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				rcAssertEnd(t, l, bk, pristine, tx, restored)

				settled := l.digest(t)
				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("second Recover: %v", err)
				}
				if diff := rtDiff(settled, l.digest(t)); diff != "" {
					t.Errorf("a second Recover changed the installation: %s", diff)
				}
			})
		}
	}
	if restoredFrom == 0 {
		t.Error("no crash point leaves a restore that recovery completes")
	}
}

// TestRcCrashInsideRecover stops recovery itself at every Ops call, on both
// sides of it, for restores that stopped in each phase, and recovers again:
// the end state is the one an uninterrupted recovery reaches.
func TestRcCrashInsideRecover(t *testing.T) {
	tx := rtTx(1)
	l := rtNewLayout(t, tx)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	points := rcTimeline(t, l, bk, pristine)

	first := map[string]int{}
	for _, p := range points {
		key := fmt.Sprintf("%s/%d/%d", p.journal.Phase, rcDone(p.journal, PhaseMovedAside), rcDone(p.journal, PhaseInstalled))
		if _, ok := first[key]; !ok {
			first[key] = p.call
		}
	}
	var samples []int
	for _, call := range first {
		samples = append(samples, call)
	}
	slices.Sort(samples)
	if testing.Short() {
		var thin []int
		for i, call := range samples {
			if i%3 == 0 {
				thin = append(thin, call)
			}
		}
		samples = thin
	}

	for _, s := range samples {
		t.Run(fmt.Sprintf("restore stopped after call %d", s), func(t *testing.T) {
			rcReset(t, l, pristine)
			rcCrashRestore(t, l, bk, s, false)
			crashed := rcSave(t, l)
			restored := rcExpectRestored(t, l, pristine)

			counter := newCrashOps(OSOps(), math.MaxInt, false)
			h := l.host
			h.Ops = counter
			if err := Recover(context.Background(), h); err != nil {
				t.Fatalf("Recover: %v", err)
			}
			for k := 1; k <= counter.Calls(); k++ {
				for _, before := range []bool{true, false} {
					rcReset(t, l, crashed)
					h.Ops = newCrashOps(OSOps(), k, before)
					_ = Recover(context.Background(), h)
					if err := Recover(context.Background(), l.host); err != nil {
						t.Fatalf("recovery stopped at call %d %s, then Recover: %v", k, rcSide(before), err)
					}
					rcAssertEnd(t, l, bk, pristine, tx, restored)
				}
			}
		})
	}
}

// TestRcTwoRestoresWithACrashInTheSecond crashes the second of two restores
// in a row. Until the second completes the first restore's installation and
// its .pre files stay exactly as they were; once it completes, the first
// restore's .pre files are gone, or, when the crash came after the second
// restore's journal was removed, are the files the first restore set aside.
func TestRcTwoRestoresWithACrashInTheSecond(t *testing.T) {
	tx1, tx2 := rtTx(1), rtTx(2)
	l := rtNewLayout(t, tx1)
	bk := rtMakeBackup(t)
	pristine := rcSave(t, l)
	rtRestore(t, l, bk)
	first := rcSave(t, l)
	l.host.NewTx = func() string { return tx2 }

	counter := newCrashOps(OSOps(), math.MaxInt, false)
	h := l.host
	h.Ops = counter
	if _, err := Restore(context.Background(), h, bytes.NewReader(bk.file), []byte(rtPass)); err != nil {
		t.Fatalf("second Restore: %v", err)
	}
	total := counter.Calls()
	rcAssertRestored(t, l, bk, first, tx2)

	stride := max(1, total/20)
	if testing.Short() {
		stride *= 3
	}
	var calls []int
	for n := 1; n <= total; n += stride {
		calls = append(calls, n)
	}
	if calls[len(calls)-1] != total {
		calls = append(calls, total)
	}
	for _, n := range calls {
		for _, before := range []bool{true, false} {
			t.Run(fmt.Sprintf("call %03d %s", n, rcSide(before)), func(t *testing.T) {
				rcReset(t, l, first)
				rcCrashRestore(t, l, bk, n, before)
				rcAssertNothingLost(t, l, first, tx2)
				js, err := ListJournals(l.paths.ControllerDir)
				if err != nil {
					t.Fatal(err)
				}
				hadJournal := len(js) > 0
				restored := rcExpectRestored(t, l, first)

				if err := Recover(context.Background(), l.host); err != nil {
					t.Fatalf("Recover: %v", err)
				}
				if !restored {
					rcAssertOriginals(t, l, first, tx2, true)
					return
				}
				rcAssertRestored(t, l, bk, first, tx2)
				for _, f := range Targets(l.paths) {
					orig, existed := pristine.files[f]
					old := AsideName(f, tx1)
					switch {
					case !existed:
					case hadJournal && rcExists(old):
						t.Errorf("recovery completed the second restore but left %s", old)
					case rcExists(old) && rcSumAt(old) != rtSum(orig.data):
						t.Errorf("%s no longer holds the original it was set aside with", old)
					}
				}
			})
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/w7-tmp/15 && export TMPDIR=/root/.cache/w7-tmp/15 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run '^TestRc' ./internal/controller/backup/`
Expected: FAIL, the build fails with `undefined: Recover`.

- [ ] **Step 3: Implement**

Write `recover.go` in package `backup`.

`Recover(ctx, h)`:

1. `ListJournals(h.Paths.ControllerDir)`. An error is wrapped `backup: list restore journals: %w`. No journal: return nil at once, without taking the lock (a lock held by another process must not matter).
2. `release, err := h.lock()` (error unchanged) with `defer release()`. List the journals again under the lock, because the holder that made them may have finished. Recovery never calls `h.checkRestore`, never reads `h.Units` or `h.Euid`.
3. For each journal path in the order `ListJournals` returns (oldest first), call `recoverOne`; the first error is returned and later journals are not touched.

`recoverOne(ctx, h, journalPath)`: `j, err := ReadJournal(journalPath)` (wrap `backup: read journal %s: %w`); log the transaction at info level with `h.Log`. Phase `installed` or `verified` means phase 6 began: `installStaged(h, journalPath, &j)` and then `finishTx(ctx, h, journalPath, &j)`; a verification failure keeps the journal and is returned (it wraps `ErrNotQuarantined` when the database is not in this transaction's quarantine). Every other phase (`begun`, `staged`, `validated`, `quarantined`, `journaled`, `moved-aside`, `rollback`) means `rollbackTx(h, journalPath, &j)`.

`rollbackTx(h, journalPath, j)`:

1. When `j.Phase` is not `PhaseRollback`, set it and `SaveJournal`, so a crash from here on resumes the rollback.
2. For each `j.Files` entry in order. When `Existed`: `aside := AsideName(f.Path, j.Tx)`; `done, err := Settle(h.Ops, Rename{From: aside, To: f.Path})`; an error is returned as `backup: journal <journalPath>: <aside> and <f.Path>: %w` (it wraps `ErrAmbiguousRename`; the journal stays); when not done, `RenameJournaled(h.Ops, journalPath, j, PhaseRollback, aside, f.Path)`. When the original did not exist: `h.Ops.Remove(f.Path)` and `SyncDir` its directory.
3. `removeTx(h, j.Tx)` last, so the journal is removed only after every file of the transaction. The earlier restores' `.pre` files are not touched by a rollback.

An entry of `j.Files` is only acted on through the pair of names derived from this journal's own `Tx`; recovery never lists or removes a name carrying another transaction id.

- [ ] **Step 4: Run test to verify it passes**

Run: `mkdir -p /root/.cache/w7-tmp/15 && export TMPDIR=/root/.cache/w7-tmp/15 GOTOOLCHAIN=local && timeout 600 go test -count=1 -v -run '^TestRc' ./internal/controller/backup/`
Expected: every `TestRc...` test and subtest PASS, final line `ok  	github.com/darkraise/darkpanel/internal/controller/backup`; the matrix logs the number of Ops calls of one restore. Record that number and the run time in the report.

Run: `export TMPDIR=/root/.cache/w7-tmp/15 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 go test -race -short -count=1 ./internal/controller/backup/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Expected: build and vet print nothing, every test line is `ok` or `?`, lint prints `0 issues.`, `go mod tidy -diff` prints nothing. The race run uses `-short` because the full matrices under the race detector are slow; say so in the report. Then `rm -rf /root/.cache/w7-tmp/15 && go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/recover.go internal/controller/backup/recover_test.go internal/controller/backup/recover_crash_test.go
git commit -m "feat(backup): recover an unfinished restore"
```

### Task 16: Backup routes end to end

**Files:**
- Modify: `internal/controller/backup/contribute.go` (`(*Backup).Contribute` returns the five routes; the package-level `Contribute` and the package comment's mention of it are removed)
- Modify: `internal/controller/backup/contribute_test.go` (delete `TestContributeAddsNothingYet`, which tests the package-level `Contribute`, and any import only it used)
- Create: `internal/controller/backup/routes_test.go`

**Interfaces:**
- Consumes: Contracts "Configuration and contribution" (`Backup`, `Config`, `New`, `TokenIssuer`), "Routes", "Backup handler" (`backupHandler`), "Quarantine routes" (`Status`, `Record`, `EnrollTokenResult`, `ReleaseResult`, `HeldLock`, and the handlers `quarantineHandler`, `enrollTokenHandler`, `releaseHandler` of Task 9 and `resolveHandler` of Task 13), "Marking" (`Mark`), "Format" (`Read`, `MemberDB`, `MemberKey`), "Validation and verification" (`ValidateStaged`), "Audit", and in the tests "Controller database fixture" (`fxDB`, `fxKey`, `fxNow`, `fxInsertHost`, `fxInsertChange`, `fxInsertComposite`, `fxInsertLock`, `fxChange`, `fxStep`). Outside the package: `ext.Route`, `ext.Deps`, `audit.New` and `quarantine.Active`.
- Produces: no new identifier. `(*Backup).Contribute(deps ext.Deps) ([]ext.Route, []ext.Service, error)` returns exactly these five routes, in this order, every one with `Surface: ext.SurfaceSocket`, plus no service and a nil error. The package-level `Contribute` no longer exists.

  | Method | Path | Stream | Handler | Summary |
  | --- | --- | --- | --- | --- |
  | POST | `/api/v1/controller/backup` | true | `b.backupHandler(deps)` | Stream an encrypted backup of the controller database and key |
  | GET | `/api/v1/controller/quarantine` | false | `b.quarantineHandler(deps)` | List the records a restore quarantined |
  | POST | `/api/v1/controller/quarantine/{record}/resolve` | false | `b.resolveHandler(deps)` | Resolve one quarantined record |
  | POST | `/api/v1/controller/quarantine/enroll-token` | false | `b.enrollTokenHandler(deps)` | Issue an enrollment token for a restored host |
  | POST | `/api/v1/controller/quarantine/release` | false | `b.releaseHandler(deps)` | Release the restore quarantine |

**Items:** 54, 69

**Depends on:** Task 1, Task 3, Task 8, Task 9, Task 11, Task 12, Task 13
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Create `internal/controller/backup/routes_test.go`. `TestRoContributeRoutes` pins the shape of the contribution, and that every route's pattern coexists on one `http.ServeMux` (a conflicting pattern panics there, and the server mounts contributed routes the same way). `TestRoRoutesEndToEnd` mounts the five routes, serves them on `127.0.0.1` through `httptest`, and drives the quarantine of a restored controller from the list to the release; it then takes a backup of the released database and reads it back with `Read` and `ValidateStaged`, which is the check a restore makes. The database holds a composite (applying, two steps, no lock), a single change with a lock and a completed change, marked by `Mark` as a restore marks them. The composite has no lock on purpose: the only lock in any answer is the single change's, so no assertion depends on how a lock of a composite is attributed. The audit log is the real `audit.New` store over the fixture database, because `core_audit` refuses a phase or result outside its CHECK lists and an in-memory fake would hide a row the real log rejects. The fixtures come from Task 3 and are not redeclared; every identifier here starts with `ro` or `Ro`.

```go
package backup

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// TestRoContributeRoutes: the contribution is exactly the five routes of the
// spec, all on the socket surface, and only the backup stream is a Stream
// route. Contribute reads nothing from its deps while building them.
func TestRoContributeRoutes(t *testing.T) {
	routes, services, err := New(Config{}).Contribute(ext.Deps{})
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	if len(services) != 0 {
		t.Errorf("Contribute returned %d services, want none", len(services))
	}
	want := []struct {
		method, path string
		stream       bool
	}{
		{http.MethodPost, "/api/v1/controller/backup", true},
		{http.MethodGet, "/api/v1/controller/quarantine", false},
		{http.MethodPost, "/api/v1/controller/quarantine/{record}/resolve", false},
		{http.MethodPost, "/api/v1/controller/quarantine/enroll-token", false},
		{http.MethodPost, "/api/v1/controller/quarantine/release", false},
	}
	if len(routes) != len(want) {
		t.Fatalf("Contribute returned %d routes, want %d", len(routes), len(want))
	}
	mux := http.NewServeMux()
	for i, w := range want {
		rt := routes[i]
		if rt.Surface != ext.SurfaceSocket || rt.Method != w.method || rt.Path != w.path || rt.Stream != w.stream {
			t.Errorf("route %d = surface %d %s %s stream %t, want a socket %s %s with stream %t",
				i, rt.Surface, rt.Method, rt.Path, rt.Stream, w.method, w.path, w.stream)
		}
		if rt.Handler == nil || rt.Summary == "" {
			t.Errorf("route %d (%s %s) has handler %v and summary %q, want both set", i, rt.Method, rt.Path, rt.Handler, rt.Summary)
			continue
		}
		// ServeMux panics on a pattern that conflicts with an earlier one,
		// which fails startup for a contributed route.
		mux.Handle(rt.Method+" "+rt.Path, rt.Handler)
	}
}

const (
	roToken   = "dpt-ro-enrollment-token" //nolint:gosec // G101: a test value, not a credential
	roAddress = "agents.example.test:7443"
	roTx      = "01JRESTORETX00000000000001"
	roPass    = "ro-backup-passphrase" //nolint:gosec // G101: a test value, not a credential
)

// roIssuer is a TokenIssuer that records what the route asked and the state
// of the host it saw inside the transaction it was given.
type roIssuer struct {
	hostID, address, stateSeen string
}

func (i *roIssuer) IssueToken(ctx context.Context, tx *sql.Tx, hostID, address string) (string, time.Time, error) {
	i.hostID, i.address = hostID, address
	if err := tx.QueryRowContext(ctx, `SELECT state FROM core_hosts WHERE id = ?`, hostID).Scan(&i.stateSeen); err != nil {
		return "", time.Time{}, err
	}
	return roToken, fxNow.Add(time.Hour), nil
}

// roEnv is a quarantined controller database behind the five routes, served
// over HTTP on 127.0.0.1.
type roEnv struct {
	dir     string
	db      *store.DB
	keyPath string
	issuer  *roIssuer
	audit   *audit.Store
	srv     *httptest.Server
}

// newRoEnv marks a database holding a composite and a single change, both
// non-terminal, the single change with a lock, and a completed change.
func newRoEnv(t *testing.T) *roEnv {
	t.Helper()
	dir := t.TempDir()
	db := fxDB(t, dir)
	keyPath, _, sealer := fxKey(t, t.TempDir())
	fxInsertHost(t, db, "h-local", "enrolled", true)
	fxInsertHost(t, db, "h-agent", "enrolled", false)
	fxInsertComposite(t, db, "c-comp", core.StateApplying, "forward", []fxStep{
		{ID: "s1", HostID: "h-agent", Action: "demo.alpha/v1", State: "applied"},
		{ID: "s2", HostID: "h-agent", Action: "demo.beta/v1", State: "pending"},
	})
	fxInsertChange(t, db, fxChange{ID: "c-single", HostID: "h-agent", Action: "firewall.rule.add", State: core.StatePending})
	fxInsertLock(t, db, "firewall.rule:8191", "c-single")
	fxInsertChange(t, db, fxChange{ID: "c-done", HostID: "h-agent", Action: "firewall.rule.add", State: core.StateCompleted})
	marker := quarantine.Marker{Tx: roTx, RestoredAt: fxNow, BackupCreatedAt: fxNow.Add(-2 * time.Hour)}
	if err := Mark(context.Background(), db, marker); err != nil {
		t.Fatalf("Mark: %v", err)
	}

	issuer := &roIssuer{}
	auditLog := audit.New(db, coretest.NewFakeClock(fxNow))
	deps := ext.Deps{
		DB: db, Sealer: sealer, Clock: coretest.NewFakeClock(fxNow),
		Log: slog.New(slog.NewTextHandler(io.Discard, nil)), Audit: auditLog, LocalHostID: "h-local",
	}
	routes, _, err := New(Config{KeyFile: keyPath, Tokens: issuer, AgentAdvertise: roAddress}).Contribute(deps)
	if err != nil {
		t.Fatalf("Contribute: %v", err)
	}
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.Method+" "+rt.Path, rt.Handler)
	}
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &roEnv{dir: dir, db: db, keyPath: keyPath, issuer: issuer, audit: auditLog, srv: srv}
}

// call sends one request to the mounted routes and returns the status, the
// headers and the whole body.
func (e *roEnv) call(t *testing.T, method, path, body string) (int, http.Header, []byte) {
	t.Helper()
	req, err := http.NewRequest(method, e.srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := e.srv.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("%s %s: read the answer: %v", method, path, err)
	}
	return resp.StatusCode, resp.Header, data
}

// query returns the single text column of a query on the database.
func (e *roEnv) query(t *testing.T, query string, args ...any) string {
	t.Helper()
	var s string
	if err := e.db.Read().QueryRowContext(context.Background(), query, args...).Scan(&s); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return s
}

func roDecode[T any](t *testing.T, data []byte) T {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var v T
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %q: %v", data, err)
	}
	return v
}

// roProblemCode returns the code of an application/problem+json answer.
func roProblemCode(t *testing.T, h http.Header, data []byte) string {
	t.Helper()
	if ct := h.Get("Content-Type"); !strings.HasPrefix(ct, "application/problem+json") {
		t.Fatalf("Content-Type %q, want application/problem+json; body %q", ct, data)
	}
	var p struct {
		Code string `json:"code"`
	}
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("decode the problem %q: %v", data, err)
	}
	return p.Code
}

// TestRoRoutesEndToEnd drives the mounted routes through a restored
// controller's recovery: list, resolve a composite to repair, issue an
// enrollment token, a release refused while a record is unresolved, a release
// that keeps it, and then a backup of the database, read back with Read.
func TestRoRoutesEndToEnd(t *testing.T) {
	e := newRoEnv(t)
	const quarantinePath = "/api/v1/controller/quarantine"

	code, _, data := e.call(t, http.MethodGet, quarantinePath, "")
	if code != http.StatusOK {
		t.Fatalf("GET quarantine = %d %s", code, data)
	}
	st := roDecode[Status](t, data)
	if !st.Active || st.Tx != roTx || st.RestoredAt == nil || !st.RestoredAt.Equal(fxNow) || len(st.Records) != 2 {
		t.Fatalf("GET quarantine = %+v, want active in %s since %s with two records", st, roTx, fxNow)
	}
	comp, single := st.Records[0], st.Records[1]
	if comp.ID != "c-comp" || comp.Kind != "composite" || comp.QuarantinedFrom != core.StateApplying {
		t.Errorf("first record = %+v, want composite c-comp quarantined from %s", comp, core.StateApplying)
	}
	if single.ID != "c-single" || single.Kind != "change" || single.Action != "firewall.rule.add" ||
		single.HostID != "h-agent" || single.QuarantinedFrom != core.StatePending ||
		len(single.Locks) != 1 || single.Locks[0] != "firewall.rule:8191" {
		t.Errorf("second record = %+v, want change c-single of firewall.rule.add on h-agent quarantined from %s holding firewall.rule:8191",
			single, core.StatePending)
	}
	var rawList struct {
		Records []struct {
			Locks json.RawMessage `json:"locks"`
		} `json:"records"`
	}
	if err := json.Unmarshal(data, &rawList); err != nil || string(rawList.Records[0].Locks) != "[]" {
		t.Errorf("the lock list of a record without locks is %s (%v), want [] and not null", rawList.Records[0].Locks, err)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/c-comp/resolve", `{"outcome":"repair"}`)
	if code != http.StatusOK {
		t.Fatalf("resolve c-comp repair = %d %s", code, data)
	}
	if rec := roDecode[Record](t, data); rec.ID != "c-comp" || rec.Kind != "composite" || rec.QuarantinedFrom != core.StateApplying {
		t.Errorf("resolve answered %+v, want the composite with its quarantinedFrom kept", rec)
	}
	if got := e.query(t, `SELECT state || '|' || error_code FROM core_changes WHERE id = 'c-comp'`); got != "needs_repair|core.needs_repair" {
		t.Errorf("c-comp is %q after resolve repair, want needs_repair|core.needs_repair", got)
	}
	code, h, data := e.call(t, http.MethodPost, quarantinePath+"/c-comp/resolve", `{"outcome":"repair"}`)
	if got := roProblemCode(t, h, data); code != http.StatusConflict || got != string(errcode.PlanStateChanged) {
		t.Errorf("resolving c-comp twice = %d %s, want 409 %s", code, got, errcode.PlanStateChanged)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/enroll-token", `{"hostId":"h-agent"}`)
	if code != http.StatusOK {
		t.Fatalf("enroll-token = %d %s", code, data)
	}
	if res := roDecode[EnrollTokenResult](t, data); res.Token != roToken || !res.ExpiresAt.Equal(fxNow.Add(time.Hour)) {
		t.Errorf("enroll-token answered %+v, want the issuer's token expiring at %s", res, fxNow.Add(time.Hour))
	}
	if e.issuer.hostID != "h-agent" || e.issuer.address != roAddress || e.issuer.stateSeen != "pending" {
		t.Errorf("the issuer saw host %q, address %q, state %q; want h-agent, %s and pending inside the route's transaction",
			e.issuer.hostID, e.issuer.address, e.issuer.stateSeen, roAddress)
	}

	code, h, data = e.call(t, http.MethodPost, quarantinePath+"/release", `{"keepUnresolved":false}`)
	if got := roProblemCode(t, h, data); code != http.StatusConflict || got != string(errcode.Quarantined) {
		t.Errorf("release with c-single unresolved = %d %s, want 409 %s", code, got, errcode.Quarantined)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || !active {
		t.Errorf("the quarantine is active %t (%v) after a refused release, want true", active, err)
	}

	code, _, data = e.call(t, http.MethodPost, quarantinePath+"/release", `{"keepUnresolved":true}`)
	if code != http.StatusOK {
		t.Fatalf("release --keep-unresolved = %d %s", code, data)
	}
	if res := roDecode[ReleaseResult](t, data); len(res.HeldLocks) != 1 || res.HeldLocks[0] != (HeldLock{Key: "firewall.rule:8191", Record: "c-single"}) {
		t.Errorf("release answered %+v, want firewall.rule:8191 held by c-single", res)
	}
	if active, err := quarantine.Active(context.Background(), e.db); err != nil || active {
		t.Errorf("the quarantine is active %t (%v) after the release, want false", active, err)
	}
	if got := e.query(t, `SELECT state FROM core_changes WHERE id = 'c-single'`); got != core.StateQuarantined {
		t.Errorf("c-single is %q after the release, want it left %s", got, core.StateQuarantined)
	}

	code, _, data = e.call(t, http.MethodGet, quarantinePath, "")
	if st = roDecode[Status](t, data); code != http.StatusOK || st.Active || len(st.Records) != 1 || st.Records[0].ID != "c-single" {
		t.Errorf("GET quarantine after the release = %d %+v, want inactive with only c-single", code, st)
	}

	e.checkBackup(t)
	e.checkAudit(t)
}

// checkBackup streams a backup, reads it back with Read and checks that the
// database member passes the restore's own validation with the key member.
func (e *roEnv) checkBackup(t *testing.T) {
	t.Helper()
	body := `{"passphrase":"` + roPass + `","argon2":{"t":1,"m":65536,"p":1}}`
	code, h, data := e.call(t, http.MethodPost, "/api/v1/controller/backup", body)
	if code != http.StatusOK {
		t.Fatalf("backup = %d %s", code, data)
	}
	if ct := h.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("backup Content-Type = %q, want application/octet-stream", ct)
	}
	disposition := regexp.MustCompile(`^attachment; filename="darkpanel-controller-\d{8}T\d{6}Z\.dpbk"$`)
	if cd := h.Get("Content-Disposition"); !disposition.MatchString(cd) {
		t.Errorf("backup Content-Disposition = %q, want attachment; filename=\"darkpanel-controller-<UTC time>.dpbk\"", cd)
	}

	var names []string
	members := map[string][]byte{}
	_, err := Read(bytes.NewReader(data), []byte(roPass), func(name string, size int64, body io.Reader) error {
		b, err := io.ReadAll(body)
		if err != nil {
			return err
		}
		if int64(len(b)) != size {
			t.Errorf("member %s: %d bytes read, size %d announced", name, len(b), size)
		}
		names = append(names, name)
		members[name] = b
		return nil
	})
	if err != nil {
		t.Fatalf("Read of the streamed backup: %v", err)
	}
	if len(names) != 2 || names[0] != MemberDB || names[1] != MemberKey {
		t.Fatalf("the backup holds members %v, want [%s %s]", names, MemberDB, MemberKey)
	}
	wantKey, err := os.ReadFile(e.keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(members[MemberKey], wantKey) {
		t.Error("the key member differs from the controller key file")
	}
	staged := filepath.Join(t.TempDir(), "controller.db")
	if err := os.WriteFile(staged, members[MemberDB], 0o600); err != nil {
		t.Fatal(err)
	}
	createdAt, err := ValidateStaged(context.Background(), staged, members[MemberKey])
	if err != nil {
		t.Fatalf("ValidateStaged of the backed-up database: %v", err)
	}
	if !createdAt.Equal(fxNow) {
		t.Errorf("the backup canary says it was created at %s, want %s", createdAt, fxNow)
	}
	if left, _ := filepath.Glob(filepath.Join(e.dir, "backup-*.db")); len(left) != 0 {
		t.Errorf("the backup left %v beside the database", left)
	}
}

// checkAudit requires one audit action per kind of route used, all by the
// local root actor, and no secret in any row.
func (e *roEnv) checkAudit(t *testing.T) {
	t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]core.AuditEntry{}
	for _, en := range entries {
		seen[en.Action] = en
		if en.Actor != "local:root@h-local" {
			t.Errorf("audit row %s has actor %q, want local:root@h-local", en.Action, en.Actor)
		}
		b, _ := json.Marshal(en)
		for _, secret := range []string{roPass, roToken} {
			if strings.Contains(string(b), secret) {
				t.Errorf("audit row %s holds a secret: %s", en.Action, b)
			}
		}
	}
	for _, action := range []string{"controller.backup", "controller.quarantine.resolve", "controller.quarantine.enroll-token", "controller.quarantine.release"} {
		if _, ok := seen[action]; !ok {
			t.Errorf("no audit row for %s", action)
		}
	}
	if en := seen["controller.quarantine.resolve"]; en.ChangeID != "c-comp" {
		t.Errorf("the resolve audit row names change %q, want c-comp", en.ChangeID)
	}
	if en := seen["controller.quarantine.enroll-token"]; en.HostID != "h-agent" {
		t.Errorf("the enroll-token audit row names host %q, want h-agent", en.HostID)
	}
}
```

- [ ] **Step 2: Run the test and see it fail**

```bash
mkdir -p /root/.cache/w7-tmp/16
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/16 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRo' ./internal/controller/backup/
```

Expected RED, a runtime failure and not a build failure, because every identifier the test uses already exists: `TestRoContributeRoutes` reports `Contribute returned 0 routes, want 5`, and `TestRoRoutesEndToEnd` stops at its first call with `GET quarantine = 404 404 page not found` (the mux has no route). A build failure naming `fxDB`, `Mark`, `Status`, `ValidateStaged` or one of the handlers means the base lacks work of Tasks 3, 8, 9, 11, 12 or 13: stop and report it, do not stub it.

- [ ] **Step 3: Implement**

All of this is in `contribute.go`; no handler file is edited, because each handler belongs to the task that wrote it.

1. `(*Backup).Contribute` builds the slice of five `ext.Route` values from the table under Interfaces, in that order: `Surface` `ext.SurfaceSocket`, the method and path of the table, `Stream` true only for the backup route, the summary of the table, and the handler of the table built from the `deps` argument. It returns no service and a nil error. It reads no field of `deps` itself, and the first test calls it with an empty `ext.Deps`, so a handler constructor must keep `deps.DB`, `deps.Sealer` and the like for request time. If a constructor of Task 9, 12 or 13 dereferences a `deps` field while it builds the handler, the test panics: report that as a defect of that task, do not work around it here.
2. Delete the package-level `Contribute`. First run `rg -n 'backup\.Contribute\(' --glob '*.go' .`: Task 5 moved the server to `New(...).Contribute`, so only the old test may call it. If another caller remains, stop and report it.
3. Update the doc comments. The package comment says the routes reach the controller through `Backup.Contribute`; the method's comment names the five routes and says it starts no service, and no longer says it returns nothing.
4. In `contribute_test.go` delete `TestContributeAddsNothingYet` and the imports only it used; keep every other test of the file as it is.
5. Do not add route logic here. Bodies, status codes, audit rows and quarantine semantics belong to Tasks 9, 12 and 13. If `TestRoRoutesEndToEnd` fails inside a handler after step 1, the report quotes the failing output and names the task whose handler is at fault.

- [ ] **Step 4: Run the tests and see them pass**

```bash
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/16 GOTOOLCHAIN=local timeout 300 go test -count=1 -v -run 'TestRo' ./internal/controller/backup/
```

Expected GREEN: `TestRoContributeRoutes` and `TestRoRoutesEndToEnd` print `--- PASS`. Then run the task's full verification and record the real output of each command:

```bash
mkdir -p /root/.cache/w7-tmp/16
export TMPDIR=/root/.cache/w7-tmp/16 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/controller/backup/... ./internal/controller/server/...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

`go build ./...` and `go vet ./...` prove that no caller of the deleted package-level `Contribute` remains; `./internal/controller/server/...` is in the race run because the server mounts these routes.

- [ ] **Step 5: Commit, then clean up**

```bash
git add internal/controller/backup/contribute.go internal/controller/backup/contribute_test.go internal/controller/backup/routes_test.go
git commit -m "feat(backup): mount the backup and quarantine routes"
```

```bash
rm -rf /root/.cache/w7-tmp/16
GOTOOLCHAIN=local timeout 120 go clean -testcache
```

### Task 17: Backup and restore CLI commands

**Files:**
- Modify: `internal/cli/controller_backup.go` (replace the stub `backupCommands`; this file is Task 17's by the frozen-files rule)
- Create: `internal/cli/controller_backup_test.go`

**Interfaces:**
- Consumes: Contracts "CLI", "Restore and recovery" (`Restore`, `Recover`, `RestoreResult`), "Restore host" (`Host`, `ProductionHost`), "Backup handler" (`BackupRequest`), "Format" (`Params`, `DefaultParams`, `TestParams`, `Write`, `Member`, `MemberDB`, `MemberKey`), `backup.OSOps`, and Task 10's `(*app).quarantineCommand`. In `internal/cli`: `addConnFlags`, `(*app).connect`, `newSocketClient`, `client` (fields `base` and `http`), `answerError`, `maxResponseBytes`, `requestTimeout`, `usagef`, `PrintError`, `ExitCode`, `ExitUsage`, `ExitError`, `markRuns`, `Options` (`SelectPaths`, `IsTerminal`, `ReadSecret`), `withDefaults`, `terminal`, `NewRoot` and, in the tests, `testRegistry`. `internal/cli` now imports `internal/controller/backup`; neither `internal/install`, `internal/update` nor `internal/platform` imports `internal/cli`, so there is no cycle.
- Produces, all in `controller_backup.go`:

  ```go
  // backupCommands returns backup, restore and a.quarantineCommand(), in that order
  // (it is the entry of controllerCommands in modules.go, which already exists).
  func backupCommands(a *app) []*cobra.Command
  // backupFlow holds what the two commands share and the seams the tests replace.
  type backupFlow struct {
      a         *app
      fileOwner uint32 // the uid a passphrase file must belong to; the zero value is root
      newHost   func(cmd *cobra.Command) (backup.Host, func() error, error) // production: SelectPaths(cmd, false), then backup.ProductionHost(..., !recover)
  }
  func newBackupFlow(a *app) *backupFlow
  func (f *backupFlow) backupCommand() *cobra.Command
  func (f *backupFlow) restoreCommand() *cobra.Command
  // backupHTTPClient returns a client over a clone of c's transport with no total timeout.
  func backupHTTPClient(c *client) (*http.Client, error)
  func readPassphraseFile(path string, owner uint32) ([]byte, error)
  func restoreSummary(res backup.RestoreResult) string
  ```

**Items:** 54, 69

**Depends on:** Task 1, Task 2, Task 6, Task 10, Task 12, Task 14, Task 15
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

- [ ] **Step 1: Write the failing test**

Create `internal/cli/controller_backup_test.go`. Two seams keep the tests off the live host (Global Constraints, Host safety): `backupFlow.newHost` hands the restore a `backup.Host` over `t.TempDir()` with stopped fake services, so `backup.ProductionHost` is never called, and `backupFlow.fileOwner` lets a test own its passphrase file, because a test cannot create a root-owned file. A successful restore needs a full controller database, which is Task 14's test and Task 19's VM run; here the wiring is proven by what a restore does first: the refusal when not root, the authentication failure with a wrong passphrase against the validation failure of a test archive with the right one (the archive's members are not a database and a key), and the host's close function running on every path. The summary text is a pure function and is tested as one. Argon2id runs only in `ckBackupFile`, with `backup.TestParams`, and the tests do not call `t.Parallel()`. Every identifier starts with `ck` or `Ck`.

| Test | What it pins |
| --- | --- |
| `TestCkBackupWritesTheFile` | The streamed bytes land in `--out` with mode 0600 and nothing else is left in the directory; the two prompts; the success line; the request carries the passphrase and no `argon2` member; no output shows the passphrase. |
| `TestCkBackupArgon2Parameters` | `argon2` is sent only when a flag was given, and a flag left out takes the default. |
| `TestCkBackupArgon2Bounds` | Each out-of-range value is a usage error, before any prompt and any request. |
| `TestCkBackupRefusesBeforeAsking` | No `--out`, an existing file, a dangling link and a missing directory are refused before the passphrase is asked. |
| `TestCkBackupFailuresLeaveNothing` | A refusal, an answer that is not problem details and a stream cut after 5000 bytes each leave no file and no temporary file. |
| `TestCkBackupClientHasNoTotalTimeout` | The backup client has no total timeout and no header timeout, is a clone, and leaves the socket client's timeout alone. |
| `TestCkPassphraseSources` | Typed twice, mismatch, empty, too long, no terminal, a failing terminal, and a file (which wins over the terminal) with its owner, mode and length rules. |
| `TestCkReadPassphraseFile` | The file rules one by one, including the one-newline strip, symbolic links, pipes and directories, and that no error shows the file's content. |
| `TestCkRestoreFlags` | The flag combinations of `restore`, and that no host is built for them. |
| `TestCkRestoreUsesTheHostAndThePassphrase` | The passphrase from a file or the terminal reaches the restore, the host is the injected one, and its close function runs. |
| `TestCkRecover` | `--recover` is silent when there is nothing to do, and fails the unit when a journal cannot be read. |
| `TestCkRestoreSummary` | The summary text, in UTC. |
| `TestCkBackupCommandsMount` | `backupCommands` returns backup, restore and quarantine, and the real root has them under `controller` with the right flags. |

```go
package cli

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/oklog/ulid/v2"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/controller/backup"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/paths"
)

const (
	ckPass      = "correct horse battery"
	ckPrompts   = "Passphrase: \nRepeat passphrase: \n"
	ckLongError = "darkpanel: the passphrase is longer than 1024 bytes\n"
)

// ckUID is the uid a passphrase file of these tests belongs to: the test
// process's own, where the command requires uid 0.
func ckUID() uint32 {
	return uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in uint32
}

// ckServe serves h on a Unix socket in a short directory and returns the
// socket path.
func ckServe(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "ck")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); _ = os.RemoveAll(dir) })
	return path
}

// ckSeen is the backup requests a fake controller received.
type ckSeen struct {
	mu   sync.Mutex
	reqs []backup.BackupRequest
}

func (s *ckSeen) all() []backup.BackupRequest {
	s.mu.Lock()
	defer s.mu.Unlock()
	return slices.Clone(s.reqs)
}

// ckBackupServer serves POST /api/v1/controller/backup on a socket: it
// records the decoded request, refusing a body with an unknown field, and
// lets reply answer.
func ckBackupServer(t *testing.T, reply http.HandlerFunc) (string, *ckSeen) {
	t.Helper()
	seen := &ckSeen{}
	sock := ckServe(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/controller/backup" {
			t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Content-Type"); got != "application/json" {
			t.Errorf("Content-Type %q, want application/json", got)
		}
		dec := json.NewDecoder(io.LimitReader(r.Body, 64<<10))
		dec.DisallowUnknownFields()
		var req backup.BackupRequest
		if err := dec.Decode(&req); err != nil {
			t.Errorf("the request body: %v", err)
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		seen.mu.Lock()
		seen.reqs = append(seen.reqs, req)
		seen.mu.Unlock()
		reply(w, r)
	}))
	return sock, seen
}

// ckOctets answers 200 with body in 4 KiB writes, flushing each.
func ckOctets(body []byte) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		for off := 0; off < len(body); off += 4096 {
			_, _ = w.Write(body[off:min(off+4096, len(body))])
			_ = http.NewResponseController(w).Flush()
		}
	}
}

// ckProblem answers with RFC 9457 problem details.
func ckProblem(status int, code, detail string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_, _ = fmt.Fprintf(w, `{"type":"about:blank","title":"refused","status":%d,"detail":%q,"code":%q}`, status, detail, code)
	}
}

// ckEnv runs the backup and restore commands under a controller group, with
// the terminal, the secret prompts, the passphrase file owner and the
// restore host of the test.
type ckEnv struct {
	terminal bool
	secrets  []string
	owner    uint32 // the uid a passphrase file must belong to
	newHost  func(*cobra.Command) (backup.Host, func() error, error)
}

func (e ckEnv) app(out, errb io.Writer) *app {
	secrets := slices.Clone(e.secrets)
	opts := withDefaults(Options{
		Stdin: strings.NewReader(""), Stdout: out, Stderr: errb,
		IsTerminal: func() bool { return e.terminal },
		ReadSecret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
	})
	return &app{opts: opts, term: &terminal{in: bufio.NewReader(opts.Stdin), out: opts.Stderr, isTerm: opts.IsTerminal(), readSecret: opts.ReadSecret}}
}

func (e ckEnv) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	var out, errb bytes.Buffer
	flow := newBackupFlow(e.app(&out, &errb))
	flow.fileOwner = e.owner
	flow.newHost = e.newHost
	if flow.newHost == nil {
		flow.newHost = func(*cobra.Command) (backup.Host, func() error, error) {
			t.Error("the command built the restore host")
			return backup.Host{}, nil, errors.New("no host in this test")
		}
	}
	root := &cobra.Command{Use: "darkpanel", SilenceUsage: true, SilenceErrors: true}
	root.SetIn(strings.NewReader(""))
	root.SetOut(&out)
	root.SetErr(&errb)
	ctl := &cobra.Command{Use: "controller"}
	ctl.AddCommand(flow.backupCommand(), flow.restoreCommand())
	root.AddCommand(ctl)
	markRuns(root)
	root.SetArgs(append([]string{"controller"}, args...))
	err := root.Execute()
	PrintError(&errb, err)
	return out.String(), errb.String(), ExitCode(err)
}

// ckPassFile writes a passphrase file with the given content and mode.
func ckPassFile(t *testing.T, content string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

// ckBackupFile writes a backup under pass whose members authenticate but are
// not a database and a key, so a restore passes phase 1 and stops at phase 2.
func ckBackupFile(t *testing.T, dir, pass string) string {
	t.Helper()
	path := filepath.Join(dir, "in.dpbk")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	db, key := []byte("this is not an SQLite database"), []byte("this is not a key file")
	err = backup.Write(f, rand.Reader, []byte(pass), backup.TestParams, []backup.Member{
		{Name: backup.MemberDB, Size: int64(len(db)), Body: bytes.NewReader(db)},
		{Name: backup.MemberKey, Size: int64(len(key)), Body: bytes.NewReader(key)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return path
}

// ckUnits is a core.Units whose services are all stopped; any other method
// panics, because a restore must not call it.
type ckUnits struct{ core.Units }

func (ckUnits) State(context.Context, string) (core.UnitState, error) {
	return core.UnitState{LoadState: "loaded", ActiveState: "inactive"}, nil
}

// ckHost is a restore host in a temporary directory: a controller that is
// installed, its services stopped, and euid as the effective user.
func ckHost(t *testing.T, euid int) backup.Host {
	t.Helper()
	root := t.TempDir()
	p := paths.Under(root)
	unit := filepath.Join(root, "darkpanel-controller.service")
	for dir, file := range map[string]string{p.ControllerDir: "", filepath.Dir(p.ControllerKey): p.ControllerKey, root: unit} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
		if file != "" {
			if err := os.WriteFile(file, []byte("x\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
	}
	return backup.Host{
		Paths: p, UnitFile: unit, LockFile: filepath.Join(root, "restore.lock"),
		Units: ckUnits{}, Euid: func() int { return euid },
		Owner: func() (int, int, error) { return os.Getuid(), os.Getgid(), nil },
		Ops:   backup.OSOps(), Clock: coretest.NewFakeClock(time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)),
		NewTx: func() string { return ulid.Make().String() },
		Log:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

// ckHostFor returns a newHost hook that hands out h and records that its
// close function ran.
func ckHostFor(h backup.Host) (hook func(*cobra.Command) (backup.Host, func() error, error), closed *bool) {
	closed = new(bool)
	return func(*cobra.Command) (backup.Host, func() error, error) {
		return h, func() error { *closed = true; return nil }, nil
	}, closed
}

func TestCkBackupWritesTheFile(t *testing.T) {
	body := bytes.Repeat([]byte("darkpanel backup frame "), 20000)
	sock, seen := ckBackupServer(t, ckOctets(body))
	out := filepath.Join(t.TempDir(), "controller.dpbk")
	stdout, stderr, code := ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}.run(t, "backup", "--out", out, "--socket", sock)
	if code != 0 {
		t.Fatalf("backup: exit %d, stdout %q, stderr %q", code, stdout, stderr)
	}
	want := fmt.Sprintf("Backup written to %s (%d bytes); keep the passphrase, the file cannot be opened without it\n", out, len(body))
	if stdout != want {
		t.Errorf("stdout %q, want %q", stdout, want)
	}
	if stderr != ckPrompts {
		t.Errorf("stderr %q, want only the two prompts %q", stderr, ckPrompts)
	}
	if got, err := os.ReadFile(out); err != nil || !bytes.Equal(got, body) {
		t.Errorf("the file holds %d bytes (error %v), want the %d bytes the controller streamed", len(got), err, len(body))
	}
	info, err := os.Stat(out)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("the file has mode %v, want 0600", info.Mode().Perm())
	}
	if entries, err := os.ReadDir(filepath.Dir(out)); err != nil || len(entries) != 1 {
		t.Errorf("the directory holds %d entries (error %v), want only the backup", len(entries), err)
	}
	reqs := seen.all()
	if len(reqs) != 1 || reqs[0].Passphrase != ckPass || reqs[0].Argon2 != nil {
		t.Errorf("the controller received %+v, want the passphrase and no argon2 member", reqs)
	}
	for _, s := range []string{stdout, stderr} {
		if strings.Contains(s, ckPass) {
			t.Errorf("the output shows the passphrase: %q", s)
		}
	}
}

func TestCkBackupArgon2Parameters(t *testing.T) {
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for i, tc := range []struct {
		args []string
		want *backup.Params
	}{
		{nil, nil},
		{[]string{"--argon2-t", "1", "--argon2-m", "65536", "--argon2-p", "1"}, &backup.Params{T: 1, M: 65536, P: 1}},
		{[]string{"--argon2-p", "2"}, &backup.Params{T: 3, M: 65536, P: 2}},
		{[]string{"--argon2-m", "131072"}, &backup.Params{T: 3, M: 131072, P: 4}},
	} {
		out := filepath.Join(t.TempDir(), "b.dpbk")
		args := append([]string{"backup", "--out", out, "--socket", sock}, tc.args...)
		if _, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, args...); code != 0 {
			t.Fatalf("%v: exit %d, stderr %q", tc.args, code, stderr)
		}
		reqs := seen.all()
		if len(reqs) != i+1 {
			t.Fatalf("%v: the controller has received %d requests, want %d", tc.args, len(reqs), i+1)
		}
		got := reqs[i].Argon2
		if tc.want == nil && got != nil || tc.want != nil && (got == nil || *got != *tc.want) {
			t.Errorf("%v: the request carried argon2 %+v, want %+v", tc.args, got, tc.want)
		}
	}
}

func TestCkBackupArgon2Bounds(t *testing.T) {
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for _, tc := range []struct{ flag, value, msg string }{
		{"--argon2-t", "0", "--argon2-t must be 1 to 10, not 0"},
		{"--argon2-t", "11", "--argon2-t must be 1 to 10, not 11"},
		{"--argon2-m", "65535", "--argon2-m must be 65536 to 1048576 KiB, not 65535"},
		{"--argon2-m", "1048577", "--argon2-m must be 65536 to 1048576 KiB, not 1048577"},
		{"--argon2-p", "0", "--argon2-p must be 1 to 16, not 0"},
		{"--argon2-p", "17", "--argon2-p must be 1 to 16, not 17"},
	} {
		out := filepath.Join(t.TempDir(), "b.dpbk")
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, "backup", "--out", out, "--socket", sock, tc.flag, tc.value)
		if want := "darkpanel: " + tc.msg + "\n"; code != ExitUsage || stdout != "" || stderr != want {
			t.Errorf("%s %s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q before any prompt", tc.flag, tc.value, code, stdout, stderr, ExitUsage, want)
		}
	}
	if n := len(seen.all()); n != 0 {
		t.Errorf("the controller received %d requests, want none", n)
	}
}

func TestCkBackupRefusesBeforeAsking(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "old.dpbk")
	if err := os.WriteFile(existing, []byte("old backup"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "link.dpbk")
	if err := os.Symlink(filepath.Join(dir, "nowhere"), link); err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(dir, "no")
	sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
	for _, tc := range []struct {
		name   string
		args   []string
		code   int
		stderr string
	}{
		{"no --out", nil, ExitUsage, "darkpanel: --out is required\n"},
		{"an existing file", []string{"--out", existing}, ExitError,
			"darkpanel: refusing to overwrite " + existing + "; remove it or choose another --out\n"},
		{"a dangling link", []string{"--out", link}, ExitError,
			"darkpanel: refusing to overwrite " + link + "; remove it or choose another --out\n"},
		{"a missing directory", []string{"--out", filepath.Join(missing, "b.dpbk")}, ExitError,
			"darkpanel: --out directory: stat " + missing + ": no such file or directory\n"},
	} {
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, append([]string{"backup", "--socket", sock}, tc.args...)...)
		if code != tc.code || stdout != "" || stderr != tc.stderr {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q, with no prompt", tc.name, code, stdout, stderr, tc.code, tc.stderr)
		}
	}
	if got, err := os.ReadFile(existing); err != nil || string(got) != "old backup" {
		t.Errorf("the existing file now holds %q (error %v), want it untouched", got, err)
	}
	if n := len(seen.all()); n != 0 {
		t.Errorf("the controller received %d requests, want none", n)
	}
}

func TestCkBackupFailuresLeaveNothing(t *testing.T) {
	for _, tc := range []struct {
		name, stderr, contains string
		reply                  http.HandlerFunc
	}{
		{name: "a refusal", reply: ckProblem(http.StatusConflict, "core.change_busy", "another backup is running"),
			stderr: "darkpanel: another backup is running (core.change_busy)\n"},
		{name: "an answer that is not problem details", contains: "unexpected answer 500 Internal Server Error",
			reply: func(w http.ResponseWriter, _ *http.Request) { http.Error(w, "boom", http.StatusInternalServerError) }},
		{name: "a stream cut after the first bytes", contains: "receive the backup",
			reply: func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/octet-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write(bytes.Repeat([]byte("x"), 5000))
				_ = http.NewResponseController(w).Flush()
				panic(http.ErrAbortHandler)
			}},
	} {
		sock, _ := ckBackupServer(t, tc.reply)
		dir := t.TempDir()
		out := filepath.Join(dir, "b.dpbk")
		stdout, stderr, code := (ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}).run(t, "backup", "--out", out, "--socket", sock)
		if code != ExitError || stdout != "" {
			t.Errorf("%s: exit %d, stdout %q, want exit %d and no stdout", tc.name, code, stdout, ExitError)
		}
		if tc.stderr != "" && stderr != ckPrompts+tc.stderr || !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: stderr %q, want %q%s", tc.name, stderr, ckPrompts+tc.stderr, tc.contains)
		}
		if entries, err := os.ReadDir(dir); err != nil || len(entries) != 0 {
			t.Errorf("%s: the directory holds %d entries (error %v), want none: no backup and no temporary file", tc.name, len(entries), err)
		}
	}
}

// ckRoundTripper is an http.RoundTripper that is not an *http.Transport.
type ckRoundTripper func(*http.Request) (*http.Response, error)

func (f ckRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestCkBackupClientHasNoTotalTimeout(t *testing.T) {
	sock := ckServe(t, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") }))
	c := newSocketClient(sock)
	hc, err := backupHTTPClient(c)
	if err != nil {
		t.Fatal(err)
	}
	if hc.Timeout != 0 {
		t.Errorf("the backup client has a total timeout of %v, want none", hc.Timeout)
	}
	tr, ok := hc.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("the backup client's transport is %T, want an *http.Transport", hc.Transport)
	}
	if http.RoundTripper(tr) == c.http.Transport || tr.ResponseHeaderTimeout != 0 {
		t.Errorf("the backup client shares the socket client's transport (%t) or has a header timeout of %v, want its own clone without one",
			http.RoundTripper(tr) == c.http.Transport, tr.ResponseHeaderTimeout)
	}
	if c.http.Timeout != requestTimeout {
		t.Errorf("the socket client's timeout is now %v, want %v unchanged", c.http.Timeout, requestTimeout)
	}
	resp, err := hc.Get("http://controller/x")
	if err != nil {
		t.Fatalf("a request through the backup client: %v", err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("status %d through the backup client, want it to reach the socket", resp.StatusCode)
	}
	other := &client{base: "http://x", http: &http.Client{Transport: ckRoundTripper(nil)}}
	if _, err := backupHTTPClient(other); err == nil {
		t.Error("backupHTTPClient accepted a transport that is not an *http.Transport")
	}
}

func TestCkPassphraseSources(t *testing.T) {
	good := ckPassFile(t, "from file\n", 0o600)
	loose := ckPassFile(t, "from file\n", 0o644)
	long := strings.Repeat("a", 1025)
	longFile := ckPassFile(t, long, 0o600)
	emptyFile := ckPassFile(t, "\n", 0o600)
	missing := filepath.Join(t.TempDir(), "nowhere")
	tests := []struct {
		name     string
		env      ckEnv
		args     []string
		code     int
		stderr   string // exact; "" compares contains instead
		contains string
		want     string // the passphrase the controller receives; "" when no request is made
	}{
		{name: "typed twice", env: ckEnv{terminal: true, secrets: []string{ckPass, ckPass}}, stderr: ckPrompts, want: ckPass},
		{name: "typed twice differently", env: ckEnv{terminal: true, secrets: []string{"one", "two"}}, code: ExitUsage,
			stderr: ckPrompts + "darkpanel: the passphrases do not match\n"},
		{name: "typed empty", env: ckEnv{terminal: true, secrets: []string{"", ""}}, code: ExitUsage,
			stderr: ckPrompts + "darkpanel: the passphrase is empty\n"},
		{name: "typed too long", env: ckEnv{terminal: true, secrets: []string{long, long}}, code: ExitUsage, stderr: ckPrompts + ckLongError},
		{name: "no terminal and no file", env: ckEnv{}, code: ExitUsage,
			stderr: "darkpanel: no terminal to read the passphrase from; pass --passphrase-file\n"},
		{name: "the terminal fails", env: ckEnv{terminal: true}, code: ExitError, stderr: "Passphrase: \ndarkpanel: read the passphrase: EOF\n"},
		{name: "a file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", good}, want: "from file"},
		{name: "a file beats the terminal", env: ckEnv{owner: ckUID(), terminal: true, secrets: []string{"typed", "typed"}},
			args: []string{"--passphrase-file", good}, want: "from file"},
		{name: "a file with a loose mode", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", loose}, code: ExitError,
			contains: "permission bits outside 0600"},
		{name: "a missing file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", missing}, code: ExitError, contains: "no such file"},
		{name: "a file of another owner", env: ckEnv{owner: ckUID() + 1}, args: []string{"--passphrase-file", good}, code: ExitError,
			contains: "must be owned by uid"},
		{name: "a file too long", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", longFile}, code: ExitUsage, stderr: ckLongError},
		{name: "an empty file", env: ckEnv{owner: ckUID()}, args: []string{"--passphrase-file", emptyFile}, code: ExitUsage,
			stderr: "darkpanel: the passphrase is empty\n"},
	}
	for _, tc := range tests {
		sock, seen := ckBackupServer(t, ckOctets([]byte("x")))
		out := filepath.Join(t.TempDir(), "b.dpbk")
		args := append([]string{"backup", "--out", out, "--socket", sock}, tc.args...)
		stdout, stderr, code := tc.env.run(t, args...)
		if code != tc.code {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d", tc.name, code, stdout, stderr, tc.code)
		}
		switch {
		case tc.stderr != "" && stderr != tc.stderr, tc.stderr == "" && !strings.Contains(stderr, tc.contains),
			tc.stderr == "" && tc.contains == "" && stderr != "":
			t.Errorf("%s: stderr %q, want %q%s", tc.name, stderr, tc.stderr, tc.contains)
		}
		reqs := seen.all()
		switch {
		case tc.want == "" && len(reqs) != 0:
			t.Errorf("%s: the controller received %d requests, want none", tc.name, len(reqs))
		case tc.want != "" && (len(reqs) != 1 || reqs[0].Passphrase != tc.want):
			t.Errorf("%s: the controller received %+v, want one request with the passphrase %q", tc.name, reqs, tc.want)
		}
		for _, s := range []string{stdout, stderr} {
			if tc.want != "" && strings.Contains(s, tc.want) {
				t.Errorf("%s: the output shows the passphrase: %q", tc.name, s)
			}
		}
	}
}

func TestCkReadPassphraseFile(t *testing.T) {
	uid := ckUID()
	dir := t.TempDir()
	write := func(name, content string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatal(err)
		}
		return p
	}
	target := write("target", "secret\n", 0o600)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name, path string
		owner      uint32
		want, err  string
	}{
		{"plain", write("a", "secret", 0o600), uid, "secret", ""},
		{"one newline is stripped", write("b", "secret\n", 0o600), uid, "secret", ""},
		{"only one", write("c", "secret\n\n", 0o600), uid, "secret\n", ""},
		{"a carriage return stays", write("d", "secret\r\n", 0o600), uid, "secret\r", ""},
		{"mode 0400", write("e", "secret\n", 0o400), uid, "secret", ""},
		{"empty", write("f", "", 0o600), uid, "", ""},
		{"readable by the group", write("g", "secret\n", 0o640), uid, "", "permission bits outside 0600"},
		{"writable by others", write("h", "secret\n", 0o602), uid, "", "permission bits outside 0600"},
		{"another owner", write("i", "secret\n", 0o600), uid + 1, "", "must be owned by uid"},
		{"a directory", sub, uid, "", "not a regular file"},
		{"a named pipe", fifo, uid, "", "not a regular file"},
		{"a symbolic link", link, uid, "", "symbolic link"},
		{"missing", filepath.Join(dir, "nowhere"), uid, "", "no such file"},
		{"1024 bytes and a newline", write("j", strings.Repeat("a", 1024)+"\n", 0o600), uid, strings.Repeat("a", 1024), ""},
		{"past 1025 bytes", write("k", strings.Repeat("a", 1026), 0o600), uid, "", "longer than 1024 bytes"},
	}
	for _, tc := range tests {
		got, err := readPassphraseFile(tc.path, tc.owner)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it read", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: error %v, want one containing %q", tc.name, err, tc.err)
		case tc.err == "" && string(got) != tc.want:
			t.Errorf("%s: read %q, want %q", tc.name, got, tc.want)
		}
		if err != nil && strings.Contains(err.Error(), "secret") {
			t.Errorf("%s: the error shows the file's content: %v", tc.name, err)
		}
	}
}

func TestCkRestoreFlags(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "nowhere.dpbk")
	for _, tc := range []struct {
		name     string
		args     []string
		code     int
		stderr   string
		contains string
	}{
		{"no flag", []string{"restore"}, ExitUsage,
			"darkpanel: give --in <file> to restore, or --recover to finish an interrupted restore\n", ""},
		{"--recover with --in", []string{"restore", "--recover", "--in", missing}, ExitUsage, "darkpanel: --recover takes no other flag\n", ""},
		{"--recover with a passphrase file", []string{"restore", "--recover", "--passphrase-file", missing}, ExitUsage,
			"darkpanel: --recover takes no other flag\n", ""},
		{"an input that is missing", []string{"restore", "--in", missing}, ExitError, "", missing + ": no such file or directory"},
	} {
		stdout, stderr, code := (ckEnv{terminal: true}).run(t, tc.args...)
		if code != tc.code || stdout != "" || tc.stderr != "" && stderr != tc.stderr || !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and stderr %q%s, with no host built and no prompt",
				tc.name, code, stdout, stderr, tc.code, tc.stderr, tc.contains)
		}
	}
}

func TestCkRestoreUsesTheHostAndThePassphrase(t *testing.T) {
	const pass = "restore passphrase"
	in := ckBackupFile(t, t.TempDir(), pass)
	right := ckPassFile(t, pass+"\n", 0o600)
	wrong := ckPassFile(t, "not "+pass+"\n", 0o600)
	const authFailure = "wrong passphrase or damaged backup file"
	for _, tc := range []struct {
		name          string
		euid          int
		env           ckEnv
		file          string
		contains      string
		notContaining string
	}{
		{name: "not root", euid: 1000, env: ckEnv{owner: ckUID()}, file: right, contains: "backup: a restore must run as root"},
		{name: "a wrong passphrase from a file", env: ckEnv{owner: ckUID()}, file: wrong, contains: authFailure},
		{name: "the right passphrase from a file", env: ckEnv{owner: ckUID()}, file: right, notContaining: authFailure},
		{name: "a wrong passphrase typed twice", env: ckEnv{terminal: true, secrets: []string{"bad", "bad"}}, contains: authFailure},
		{name: "the right passphrase typed twice", env: ckEnv{terminal: true, secrets: []string{pass, pass}}, notContaining: authFailure},
	} {
		hook, closed := ckHostFor(ckHost(t, tc.euid))
		tc.env.newHost = hook
		args := []string{"restore", "--in", in}
		if tc.file != "" {
			args = append(args, "--passphrase-file", tc.file)
		}
		stdout, stderr, code := tc.env.run(t, args...)
		// The members of the test archive are not a database and a key, so a
		// restore that gets past the passphrase still fails, in phase 2.
		if code != ExitError || stdout != "" || !strings.HasPrefix(stderr, "darkpanel: ") && !strings.Contains(stderr, "\ndarkpanel: ") {
			t.Errorf("%s: exit %d, stdout %q, stderr %q; want exit %d and an error", tc.name, code, stdout, stderr, ExitError)
		}
		if tc.contains != "" && !strings.Contains(stderr, tc.contains) {
			t.Errorf("%s: stderr %q, want it to contain %q", tc.name, stderr, tc.contains)
		}
		if tc.notContaining != "" && strings.Contains(stderr, tc.notContaining) {
			t.Errorf("%s: stderr %q contains %q, so the passphrase did not reach the restore", tc.name, stderr, tc.notContaining)
		}
		if !*closed {
			t.Errorf("%s: the host's close function did not run", tc.name)
		}
		if strings.Contains(stderr, pass) || strings.Contains(stdout, pass) {
			t.Errorf("%s: the output shows the passphrase", tc.name)
		}
	}
}

func TestCkRecover(t *testing.T) {
	clean := ckHost(t, 0)
	hook, closed := ckHostFor(clean)
	stdout, stderr, code := (ckEnv{newHost: hook}).run(t, "restore", "--recover")
	if code != 0 || stdout != "" || stderr != "" || !*closed {
		t.Errorf("--recover with no journal: exit %d, stdout %q, stderr %q, closed %t; want a silent success", code, stdout, stderr, *closed)
	}

	broken := ckHost(t, 0)
	journal := filepath.Join(broken.Paths.ControllerDir, "restore-"+ulid.Make().String()+".journal")
	if err := os.WriteFile(journal, []byte("{"), 0o600); err != nil {
		t.Fatal(err)
	}
	hook, closed = ckHostFor(broken)
	stdout, stderr, code = (ckEnv{newHost: hook}).run(t, "restore", "--recover")
	if code != ExitError || stdout != "" || !strings.HasPrefix(stderr, "darkpanel: ") || !*closed {
		t.Errorf("--recover over an unreadable journal: exit %d, stdout %q, stderr %q, closed %t; want exit %d with an error, so the unit fails",
			code, stdout, stderr, *closed, ExitError)
	}
}

func TestCkRestoreSummary(t *testing.T) {
	want := "Restored the backup taken 2026-11-03T10:00:00Z (restore 01JTESTTX).\n" +
		"The controller database starts in restore quarantine. Start darkpanel-controller.service and darkpanel-agent.service, then run:\n" +
		"  darkpanel controller quarantine list\n"
	utc := time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)
	if got := restoreSummary(backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: utc}); got != want {
		t.Errorf("restoreSummary = %q, want %q", got, want)
	}
	zoned := utc.In(time.FixedZone("plus2", 2*3600))
	if got := restoreSummary(backup.RestoreResult{Tx: "01JTESTTX", BackupCreatedAt: zoned}); got != want {
		t.Errorf("restoreSummary of a time in another zone = %q, want it in UTC: %q", got, want)
	}
}

func TestCkBackupCommandsMount(t *testing.T) {
	var out, errb bytes.Buffer
	cmds := backupCommands((ckEnv{}).app(&out, &errb))
	var names []string
	for _, c := range cmds {
		names = append(names, c.Name())
	}
	if want := []string{"backup", "restore", "quarantine"}; !slices.Equal(names, want) {
		t.Fatalf("backupCommands returned %v, want %v in that order", names, want)
	}
	root, err := NewRoot(Options{Registry: testRegistry(t)})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name         string
		flags, never []string
	}{
		{"backup", []string{"out", "passphrase-file", "argon2-t", "argon2-m", "argon2-p", "socket", "dev"}, []string{"url", "recover", "in"}},
		{"restore", []string{"in", "passphrase-file", "recover"}, []string{"socket", "dev", "url", "out"}},
		{"quarantine", nil, nil},
	} {
		cmd, rest, err := root.Find([]string{"controller", tc.name})
		if err != nil || len(rest) != 0 || cmd.CommandPath() != "darkpanel controller "+tc.name {
			t.Errorf("controller %s: not found under controller (error %v, %d arguments left over)", tc.name, err, len(rest))
			continue
		}
		for _, f := range tc.flags {
			if cmd.Flags().Lookup(f) == nil {
				t.Errorf("controller %s has no --%s", tc.name, f)
			}
		}
		for _, f := range tc.never {
			if cmd.Flags().Lookup(f) != nil {
				t.Errorf("controller %s has a --%s", tc.name, f)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test and see it fail**

```bash
mkdir -p /root/.cache/w7-tmp/17
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/17 GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestCk' ./internal/cli/
```

Expected RED: the build fails and its first lines name `undefined: newBackupFlow`, followed by `backupHTTPClient`, `readPassphraseFile` and `restoreSummary`. A failure that names `backup.Host`, `backup.Restore`, `backup.Recover`, `backup.Write` or `quarantineCommand` means the base lacks Task 1, 6, 10, 14 or 15: stop and report it.

- [ ] **Step 3: Implement**

Replace the stub in `controller_backup.go`. The messages below are exact; the tests compare them. A usage error is `usagef`, which exits 2, and every other failure is a plain error, which exits 1; both print as `darkpanel: <message>`.

1. `backupCommands(a)` builds one `backupFlow` with `newBackupFlow(a)` and returns `backupCommand()`, `restoreCommand()` and `a.quarantineCommand()`, in that order. `newBackupFlow` sets `fileOwner` to 0 and `newHost` to a function that resolves the paths with `a.opts.SelectPaths(cmd, false)` (the installed controller, never the development layout) and calls `backup.ProductionHost(cmd.Context(), paths, !recover)`, where `recover` is the command's `--recover` flag: the recovery runs as the controller unit's `ExecStartPre` before every start and must not depend on D-Bus.
2. `backup` takes `cobra.NoArgs` and the flags `--out` (string), `--passphrase-file` (string), `--argon2-t` (int, default `backup.DefaultParams.T`), `--argon2-m` (int, default `DefaultParams.M`), `--argon2-p` (int, default `DefaultParams.P`), and the socket flags from `addConnFlags(cmd, true, false)`, so it has `--socket` and `--dev` and no `--url`.
3. `backup` checks in this order, each failure before the passphrase is asked and before any request:
   1. `--out` empty: usage error `--out is required`.
   2. When at least one `--argon2-*` flag was given (`Flags().Changed`), validate all three: `--argon2-t must be 1 to 10, not <n>`, `--argon2-m must be 65536 to 1048576 KiB, not <n>`, `--argon2-p must be 1 to 16, not <n>`, each a usage error. Then the request carries `Argon2` as a `*backup.Params` of the three values (a flag left out has its default). With no flag given, `Argon2` is nil and the controller applies its own default.
   3. `--out` exists, as `Lstat` sees it (a dangling link counts): `refusing to overwrite <out>; remove it or choose another --out`.
   4. The directory of `--out` (`filepath.Dir`) cannot be `Stat`ed or is not a directory: `--out directory: <the Stat error>`, or `--out directory: <dir> is not a directory`.
4. The passphrase (rules in item 7) is read after those checks and sent as `backup.BackupRequest` JSON. Marshal it, then clear the passphrase bytes at once. The request is `POST /api/v1/controller/backup` with `Content-Type: application/json` and `Accept: application/octet-stream`, built with `http.NewRequestWithContext(cmd.Context(), ...)` against the base URL of `a.connect(cmd)`, and sent by `backupHTTPClient`.
5. `backupHTTPClient(c)` returns an `*http.Client` over `Clone()` of `c.http.Transport`, which must be an `*http.Transport` (any other type is an error), with `Timeout` zero and the redirect policy of `c.http`. The clone has no `ResponseHeaderTimeout`; the socket client's own timeout (`requestTimeout`) stays on the shared client for every other command. The backup streams for as long as the copy takes, so no total deadline may apply; the cancellation of `cmd.Context()` is the only limit.
6. Handling the answer: a status outside 200 to 299 is read with `io.LimitReader(body, maxResponseBytes)` and returned through `answerError(http.MethodPost, path, resp, data)`, which gives `another backup is running (core.change_busy)` for a problem and `unexpected answer 500 Internal Server Error` for anything else. On 2xx the body goes to a temporary file created beside `--out` with `os.CreateTemp(dir, "."+base+".*.partial")` (mode 0600), through `io.Copy`; a copy error is `receive the backup: <err>`. Then `Sync` and `Close` the file, check once more that `--out` does not exist (same refusal as 3.3), `os.Rename` the temporary file to `--out`, and `Sync` the directory. A deferred cleanup closes and removes the temporary file on every path that did not rename it. The success line, on standard output, is `Backup written to <out> (<n> bytes); keep the passphrase, the file cannot be opened without it`, with `<n>` the number of bytes copied.
7. The passphrase, a method of `backupFlow` used by both commands. With `--passphrase-file`: `readPassphraseFile(path, f.fileOwner)`. Without it: if `a.opts.IsTerminal()` is false, usage error `no terminal to read the passphrase from; pass --passphrase-file`; otherwise write `Passphrase: ` to the command's standard error, call `a.opts.ReadSecret()`, write a newline whether or not the read succeeded, then the same with `Repeat passphrase: `; a read failure is `read the passphrase: <err>`; the two values compared with `subtle.ConstantTimeCompare` must be equal, else usage error `the passphrases do not match`. The result is checked by one rule for both sources: empty is usage error `the passphrase is empty`, over 1024 bytes is usage error `the passphrase is longer than 1024 bytes`. Every buffer that is not returned is cleared. The terminal prompts are on standard error, so standard output carries only the success line.
8. `readPassphraseFile(path, owner)` opens with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` and judges the open file, not the path, so nothing can change between the check and the read: a symbolic link (`ELOOP`) is `the passphrase file <path> is a symbolic link`; any other open error is `read the passphrase file: <err>`; not a regular file is `the passphrase file <path> is not a regular file`; an owner uid other than `owner` is `the passphrase file <path> must be owned by uid <owner>`; a mode with any bit outside 0600 is `the passphrase file <path> has permission bits outside 0600; run chmod 600 on it`. It then reads at most 1026 bytes; more than 1025 is `the passphrase file <path> is longer than 1024 bytes`. Exactly one trailing `\n` is removed (a `\r` stays). Errors name the path and never the content.
9. `restore` takes `cobra.NoArgs` and the flags `--in`, `--passphrase-file` and `--recover` (bool); it has no socket flags, because it never talks to the controller. `runRestore` checks the flags first, with no host built and no prompt: `--recover` together with `--in` or `--passphrase-file` is usage error `--recover takes no other flag`; neither `--recover` nor `--in` is usage error `give --in <file> to restore, or --recover to finish an interrupted restore`.
10. `restore --recover`: `newHost`, `backup.Recover(cmd.Context(), host)`, then the host's close function, and return `errors.Join(recoverErr, closeErr)`. It prints nothing on success, so the unit's `ExecStartPre` stays quiet in the journal, and any error exits 1 so the unit does not start the controller over a half-finished restore.
11. `restore --in`: open the file first (an open error is returned as it is, `<path>: no such file or directory`), then read the passphrase, then `newHost` (clear the passphrase if it fails), then `backup.Restore(cmd.Context(), host, file, passphrase)`, clear the passphrase, close the host and join the errors, close the file with `defer`. On success write `restoreSummary(result)` to standard output. The command does not check `Euid` itself: `Restore` refuses a non-root caller with its own message, and the test relies on that.
12. `restoreSummary(res)` returns exactly three lines: `Restored the backup taken <BackupCreatedAt in UTC, RFC 3339> (restore <Tx>).`, then `The controller database starts in restore quarantine. Start darkpanel-controller.service and darkpanel-agent.service, then run:`, then two spaces and `darkpanel controller quarantine list`, each ending in a newline.
13. Doc comments on every new declaration, in full sentences; comments only for a why that is not obvious (for example, why the backup client has no timeout, and why the file is judged by its descriptor).

- [ ] **Step 4: Run the tests and see them pass**

```bash
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/17 GOTOOLCHAIN=local timeout 300 go test -count=1 -v -run 'TestCk' ./internal/cli/
```

Expected GREEN: the thirteen `TestCk` tests print `--- PASS`. If `TestCkRestoreUsesTheHostAndThePassphrase` fails only on the wrong-passphrase cases with a message other than `wrong passphrase or damaged backup file`, `backup.Restore` reports the format error differently from Contracts "Format" (`ErrAuth`): report it to Task 14 and do not change the expected text. Then run the task's full verification and record the real output of each command:

```bash
mkdir -p /root/.cache/w7-tmp/17
export TMPDIR=/root/.cache/w7-tmp/17 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -short -count=1 ./internal/cli/... ./internal/controller/backup/...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

- [ ] **Step 5: Commit, then clean up**

```bash
git add internal/cli/controller_backup.go internal/cli/controller_backup_test.go
git commit -m "feat(cli): add controller backup and restore"
```

```bash
rm -rf /root/.cache/w7-tmp/17
GOTOOLCHAIN=local timeout 120 go clean -testcache
```

### Task 18: Controller unit recovery hook

**Files:**
- Modify: `internal/install/units/darkpanel-controller.service`
- Modify: `internal/install/units_test.go`
- Modify: `internal/install/layout_test.go`

`internal/install/units.go` and `layout.go` stay as they are: the template is rendered by the existing `Units`, and the directories the layout test checks are already declared by `OwnedPaths`.

**Interfaces:**
- Consumes: `Units`, `Layout`, `Production`, `Rooted`, `OwnedPaths` and the `unitsByPath` helper of `units_test.go`; `update.ControllerUnit`; `core.OwnedDir`.
- Produces: Contracts "Unit": the line `ExecStartPre=+{{.Binary}} controller restore --recover` directly above `ExecStart={{.Binary}} serve`, which runs Task 17's `restore --recover` command and which Task 19's harness test checks. The line lands only after that command exists, so no commit of the branch installs a controller unit that cannot start. It is the only change to a unit file; the drop-in, the agent unit and the rollback units do not change.

**Items:** 54

**Depends on:** Task 17
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 1 - coupling 1 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

In `internal/install/units_test.go` add `"slices"` to the standard-library imports and `"github.com/darkraise/darkpanel/internal/update"` to the module imports (keep the import blocks sorted), then append the two tests below at the end of the file. They render the controller unit for the production layout and for a rooted one, and require the recovery line directly above `ExecStart` with the layout's own binary, exactly one `ExecStartPre` line, `User=darkpanel` and `TimeoutStartSec=30s` still present, and no `ExecStartPre` in any other unit or in the drop-in (an `ExecStartPre=` line in the drop-in would reset it).

```go
// TestRuControllerUnitRecoversBeforeStart: the controller unit runs the
// restore recovery as root, with the unit's own binary, directly before it
// starts the controller.
func TestRuControllerUnitRecoversBeforeStart(t *testing.T) {
	for name, l := range map[string]Layout{"production": Production(), "rooted": Rooted(t.TempDir())} {
		files, err := Units(l, RoleController, nil, "")
		if err != nil {
			t.Fatal(err)
		}
		var text string
		for _, f := range files {
			if f.Path == l.UnitPath(update.ControllerUnit) {
				text = string(f.Content)
			}
		}
		lines := strings.Split(text, "\n")
		pre := slices.Index(lines, "ExecStartPre=+"+l.Paths.Binary()+" controller restore --recover")
		start := slices.Index(lines, "ExecStart="+l.Paths.Binary()+" serve")
		if pre < 0 || start != pre+1 {
			t.Errorf("%s: ExecStartPre is at line %d and ExecStart at %d; want the recovery line directly above ExecStart:\n%s", name, pre, start, text)
		}
		n := 0
		for _, line := range lines {
			if strings.HasPrefix(line, "ExecStartPre=") {
				n++
			}
		}
		if n != 1 {
			t.Errorf("%s: the controller unit has %d ExecStartPre lines, want 1", name, n)
		}
		if !slices.Contains(lines, "User=darkpanel") || !slices.Contains(lines, "TimeoutStartSec=30s") {
			t.Errorf("%s: the controller unit lost User=darkpanel or TimeoutStartSec=30s:\n%s", name, text)
		}
	}
}

// TestRuOnlyTheControllerUnitRecovers: the agent does not read the
// controller's files, and a drop-in that reset ExecStartPre= would silence
// the recovery.
func TestRuOnlyTheControllerUnitRecovers(t *testing.T) {
	for _, units := range []map[string]string{
		unitsByPath(t, RoleController, []string{"10.77.0.10:7443"}, "10.77.0.10:7443"),
		unitsByPath(t, RoleController, nil, ""),
		unitsByPath(t, RoleAgent, nil, ""),
	} {
		for path, text := range units {
			has := strings.Contains(text, "ExecStartPre")
			if want := path == "/etc/systemd/system/darkpanel-controller.service"; has != want {
				t.Errorf("%s: mentions ExecStartPre = %v, want %v", path, has, want)
			}
		}
	}
}
```

In `internal/install/layout_test.go` add `"slices"` to the standard-library imports and append the test below. It names the staged, set-aside and journal files of Contracts "Journal" (`StagedName` is `<file>.restore-<tx>`, `AsideName` is `<file>.pre-<tx>`, `JournalPath` is `<ControllerDir>/restore-<tx>.journal`, and `SaveJournal` writes `<journal>.new`) for the four targets (the controller database, its `-wal` and `-shm` files, the controller key) and requires each to lie under a directory that `OwnedPaths(l, RoleController)` marks `core.OwnedDir`, in the production layout and a rooted one. The names are spelled out here because the backup package imports `install` (its production host takes the unit path and the service user from it), so this package's tests cannot import `backup`. This test already passes on the current tree: it pins the property so that a later layout change that moves a directory out of the owned set fails it.

```go
// TestRuRestoreFilesLieInOwnedDirs: every file a controller restore makes
// (the staged and set-aside copies of the database, its two WAL files and
// the key, the journal and its temporary file) sits inside a directory
// system install owns, so uninstall removes them. The names are spelled out
// because the backup package imports this one.
func TestRuRestoreFilesLieInOwnedDirs(t *testing.T) {
	const tx = "01JTESTTXTESTTXTESTTXTESTT"
	for name, l := range map[string]Layout{"production": Production(), "rooted": Rooted(t.TempDir())} {
		var owned []string
		for _, op := range OwnedPaths(l, RoleController) {
			if op.Kind == core.OwnedDir {
				owned = append(owned, op.Path)
			}
		}
		db := l.Paths.ControllerDB()
		var files []string
		for _, target := range []string{db, db + "-wal", db + "-shm", l.Paths.ControllerKey} {
			files = append(files, target+".restore-"+tx, target+".pre-"+tx)
		}
		journal := filepath.Join(l.Paths.ControllerDir, "restore-"+tx+".journal")
		files = append(files, journal, journal+".new")
		for _, f := range files {
			if !slices.ContainsFunc(owned, func(dir string) bool { return strings.HasPrefix(f, dir+"/") }) {
				t.Errorf("%s layout: %s is outside every owned directory %v", name, f, owned)
			}
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
mkdir -p /root/.cache/w7-tmp/18 && export TMPDIR=/root/.cache/w7-tmp/18 GOTOOLCHAIN=local
df -h /
timeout 300 go test -count=1 -run 'TestRu' -v ./internal/install 2>&1 | tail -30
```

When `df -h /` shows 90 % or more, run `go clean -cache` first. Expected: `TestRuControllerUnitRecoversBeforeStart` and `TestRuOnlyTheControllerUnitRecovers` FAIL (the first with "ExecStartPre is at line -1", the second with "mentions ExecStartPre = false, want true"); `TestRuRestoreFilesLieInOwnedDirs` PASSES, as its comment says.

- [ ] **Step 3: Implement**

In `internal/install/units/darkpanel-controller.service`, add this line directly above the `ExecStart={{.Binary}} serve` line, and change nothing else in the file:

```ini
ExecStartPre=+{{.Binary}} controller restore --recover
```

The `+` prefix makes systemd run the command with full privileges, outside the unit's `User=darkpanel`, `ProtectSystem=strict` and `ReadWritePaths` restrictions, so the recovery can rename the controller's files and the key under `/etc/darkpanel`. `{{.Binary}}` is the same value `ExecStart` uses.

- [ ] **Step 4: Run test to verify it passes**

```bash
mkdir -p /root/.cache/w7-tmp/18 && export TMPDIR=/root/.cache/w7-tmp/18 GOTOOLCHAIN=local
timeout 300 go build ./...
timeout 300 go vet ./...
timeout 600 go test -count=1 ./internal/install
timeout 900 go test -race -short -count=1 ./internal/install
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w7-tmp/18 && go clean -testcache
```

Expected: every test passes (`ok`), `go vet` prints nothing, golangci-lint prints `0 issues.`, `go mod tidy -diff` prints nothing. This task edits `internal/install/`, so gate 7 (harness) applies to the branch once it is pushed (Global Constraints); no harness command runs here. Record the real output in the report.

- [ ] **Step 5: Commit**

```bash
git add internal/install/units/darkpanel-controller.service internal/install/units_test.go internal/install/layout_test.go
git commit -m "feat(install): run restore recovery at start"
```

### Task 19: Harness restore test and workflow step

**Files:**
- Create: `test/harness/restore_test.go` (build tag `harness`: the flag, the constants, the helpers and `TestM7Restore`)
- Create: `test/harness/restore_unit_test.go` (build tag `harness`: `TestM7rUnit*` for the helpers)
- Create: `test/harness/restore_workflow_unit_test.go` (build tag `harness`: `TestM7rUnitWorkflow`, `TestM7rUnitWorkflowFile`)
- Modify: `.github/workflows/harness.yml` (append the job `vm-m7r` after `vm-m6c`)

**Interfaces:**
- Consumes: Contracts "Quarantine routes" (`backup.Status`, `Record`, `EnrollTokenResult`, `ReleaseResult`), "Format" (`backup.Read`, `Write`, `TestParams`, `MemberDB`, `MemberKey`, `MaxKeyMember`), "Restore host" (the refusal text for running services), "CLI" (the commands and `--json`, which prints the controller's answer unchanged as Task 10 implements it), "Unit" (`ExecStartPre`); `ctlfleet.TokenTTL` (`internal/controller/fleet`), `secret.KeyPrefix`, `install.CheckAgentAddress`, `paths.Production()`, `listen.AgentPort`, `core.StateApplying`, `core.StatePending`; Contracts "Marking" (`backup.Mark`) and `quarantine.Marker`; `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrateOptions`, `store.MigrationSet`, `modules.All`. In the package `harness`: `osFlag`, `m2Flag`, `binFlag`, `commitFlag`, `workdirFlag`, `fleet`, `NewLab`, `Lab.Boot`, `harnessImages`, `TargetScript`, `bootVMs` through `Lab.Boot`, `createAdmin`, `login`, `nextCode`, `waitHosts`, `findHost`, `eventually`, `unitState`, `vmNow`, `m3Run`, `(*API).Apply`, `APIError`, the address constants `targetIPv4`, `targetIPv6`, `agentIPv4`, `agentIPv6`, `controllerPriv`, `agentPriv`, `agentAddress`, and for the workflow test `m3dxExpoJob`, `m3dxExpoLimitRE`, `m3dxExpoTimeoutRE`, `m4lSetup`, `m4lStep`, `m4lJobMax`.
- Produces, all unexported and in `restore_test.go`:

  ```go
  var m7rFlag = flag.Bool("m7r", false, "run TestM7Restore; needs -os and -m2, and is a go test run of its own")
  const m7rDeadline = 40 * time.Minute
  const (
      m7rRestoredIPv4 = "198.18.0.12"
      m7rRestoredIPv6 = "2001:db8:d0::12"
      m7rRestoredPriv = "10.77.0.12"
  )
  var m7rRestoredAddress = net.JoinHostPort(m7rRestoredPriv, strconv.Itoa(listen.AgentPort))
  // The seeded in-flight composite (W7-R3: a controller-side record) and the locks it and an update hold.
  const (
      m7rCompositeID      = "01JW7R00000000000000000C0M"
      m7rCompositePlan    = "01JW7R00000000000000000P1N"
      m7rCompositeAction  = "derp.server.deploy"
      m7rLockKey          = "derp.server:m7r"
      m7rReservationKey   = "core.update:m7r"
      m7rReservationOwner = "01JW7R00000000000000000RSV"
  )
  // m7rSeedStatements are the INSERT statements, without trailing semicolons, that seed the composite.
  func m7rSeedStatements(agentID string, now time.Time) []string

  func m7rDecode[T any](out string) (T, error)
  func m7rCheckBackup(data, passphrase []byte) error
  func m7rCheckQuarantine(st backup.Status, want backup.Record) error
  func m7rCheckToken(res backup.EnrollTokenResult, now time.Time) error
  func m7rCheckNames(listing, base string) error
  func m7rExecStartPre(show string) error
  func m7rBinaryCommands() (prepare, link string)
  func m7rControllerInstall(addr string) string
  type m7rRun struct{ /* the scenario's state, see Step 3 */ }
  func (r *m7rRun) logDiagnostics(t *testing.T)
  func TestM7Restore(t *testing.T)
  ```

**Items:** 54, 69

**Depends on:** Task 1, Task 5, Task 6, Task 8, Task 9, Task 10, Task 16, Task 17, Task 18
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Create the two unit-test files. Neither starts a VM, reads a path of the host or runs a command: the helpers take text and return an error. The helpers are what `TestM7Restore` trusts to judge what a VM printed, so each is pinned with a case that must be accepted and one case for every error it can return. `TestM7rUnitLab` pins that the restored controller's addresses are free, that its agent address passes the install code's own check, and that the install commands are the ones `install.sh` runs. `TestM7rUnitWorkflow` feeds `m7rwWorkflow` a miniature `harness.yml` and changes one thing at a time (each case must be the only change and the fixture must hold the replaced text exactly once); `TestM7rUnitWorkflowFile` checks the committed workflow, which is the only place `TestM7Restore` runs. The workflow checker also fails when the new job's `-timeout` is under the scenario deadline plus 10 minutes, when the job limit is under the step's `-timeout` plus 20 minutes, or when the job limit passes 120 minutes (gate 7), and when any of `vm-m5`, `vm-m5d`, `vm-m6n` or `vm-m6c` holds the test, its flag or its work directory.

`test/harness/restore_unit_test.go`:

```go
//go:build harness

package harness

import (
	"bytes"
	"context"
	"crypto/rand"
	"database/sql"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/backup"
	ctlfleet "github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/quarantine"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestM7rUnitDecode(t *testing.T) {
	st, err := m7rDecode[backup.Status](`{"active":true,"tx":"T","restoredAt":"2026-11-04T12:00:00Z","records":[]}` + "\n")
	if err != nil || !st.Active || st.Tx != "T" || st.RestoredAt == nil || st.Records == nil || len(st.Records) != 0 {
		t.Errorf("m7rDecode of a status = %+v, %v; want it decoded with an empty list", st, err)
	}
	for _, tc := range []struct{ name, in string }{
		{"an unknown member", `{"active":false,"records":[],"extra":1}`},
		{"a second document", `{"active":false,"records":[]} {}`},
		{"text before the document", "sudo: unable to resolve host\n" + `{"active":false,"records":[]}`},
		{"nothing", ""},
	} {
		if _, err := m7rDecode[backup.Status](tc.in); err == nil {
			t.Errorf("%s: m7rDecode accepted %q", tc.name, tc.in)
		}
	}
}

// m7rUnitArchive is a backup file under pass holding db and key.
func m7rUnitArchive(t *testing.T, pass, db, key string) []byte {
	t.Helper()
	var buf bytes.Buffer
	err := backup.Write(&buf, rand.Reader, []byte(pass), backup.TestParams, []backup.Member{
		{Name: backup.MemberDB, Size: int64(len(db)), Body: strings.NewReader(db)},
		{Name: backup.MemberKey, Size: int64(len(key)), Body: strings.NewReader(key)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestM7rUnitCheckBackup(t *testing.T) {
	const pass = "m7r unit passphrase" //nolint:gosec // G101: a test passphrase
	sqlite := "SQLite format 3\x00" + strings.Repeat("d", 100000)
	key := secret.KeyPrefix + strings.Repeat("A", 43) + "=\n"
	good := m7rUnitArchive(t, pass, sqlite, key)
	if err := m7rCheckBackup(good, []byte(pass)); err != nil {
		t.Fatalf("a good backup: %v", err)
	}
	for _, tc := range []struct {
		name string
		data []byte
		pass string
		want string
	}{
		{"a wrong passphrase", good, "another passphrase", "wrong passphrase"},
		{"a database that is not SQLite", m7rUnitArchive(t, pass, strings.Repeat("d", 1000), key), pass, "not an SQLite database"},
		{"a key that is not a key file", m7rUnitArchive(t, pass, sqlite, "not a key"), pass, "not a controller key file"},
		{"the passphrase in the clear", []byte("header " + pass + " trailer"), pass, "in the clear"},
		{"a truncated file", good[:len(good)-5], pass, "backup file"},
	} {
		err := m7rCheckBackup(tc.data, []byte(tc.pass))
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitCheckQuarantine(t *testing.T) {
	restored := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	want := backup.Record{
		ID: m7rCompositeID, Kind: "composite", Action: m7rCompositeAction, HostID: "",
		QuarantinedFrom: core.StateApplying, Locks: []string{m7rLockKey},
	}
	good := func() backup.Status {
		r := want
		r.Locks = append([]string(nil), want.Locks...)
		return backup.Status{Active: true, Tx: "01JTX", RestoredAt: &restored, Records: []backup.Record{r}}
	}
	if err := m7rCheckQuarantine(good(), want); err != nil {
		t.Fatalf("the good status: %v", err)
	}
	for _, tc := range []struct {
		name   string
		change func(*backup.Status)
		want   string
	}{
		{"inactive", func(s *backup.Status) { s.Active = false }, "not active"},
		{"no transaction", func(s *backup.Status) { s.Tx = "" }, "no restore transaction"},
		{"no restore time", func(s *backup.Status) { s.RestoredAt = nil }, "restoredAt"},
		{"no record", func(s *backup.Status) { s.Records = nil }, "0 records"},
		{"two records", func(s *backup.Status) { s.Records = append(s.Records, s.Records[0]) }, "2 records"},
		{"another record", func(s *backup.Status) { s.Records[0].ID = "c2" }, "is c2, want " + m7rCompositeID},
		{"a single change", func(s *backup.Status) { s.Records[0].Kind = "change" }, "kind"},
		{"another action", func(s *backup.Status) { s.Records[0].Action = "derp.server.upgrade" }, "action"},
		{"a host", func(s *backup.Status) { s.Records[0].HostID = "h2" }, "host"},
		{"another prior state", func(s *backup.Status) { s.Records[0].QuarantinedFrom = core.StatePending }, "quarantinedFrom"},
		{"null locks", func(s *backup.Status) { s.Records[0].Locks = nil }, "locks are null"},
		{"another lock", func(s *backup.Status) { s.Records[0].Locks = []string{"other"} }, "locks are [other]"},
	} {
		st := good()
		tc.change(&st)
		if err := m7rCheckQuarantine(st, want); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitSeed runs the seed on a migrated controller database and marks
// it as a restore does: the rows fit every CHECK and reference, and marking
// quarantines the composite, keeps its lock and deletes the update's.
func TestM7rUnitSeed(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "dev", BackupRoot: filepath.Join(dir, "backups"), Keep: 1, Now: now,
	}, coreSet, sets); err != nil {
		t.Fatal(err)
	}
	const agent = "01JW7RAGENT0000000000000AG"
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		for _, stmt := range m7rSeedStatements(agent, now) {
			if strings.HasSuffix(strings.TrimSpace(stmt), ";") {
				return fmt.Errorf("a statement ends in a semicolon: %s", stmt)
			}
			if _, err := tx.ExecContext(ctx, stmt); err != nil {
				return fmt.Errorf("%s: %w", stmt, err)
			}
		}
		return nil
	}); err != nil {
		t.Fatalf("the seed does not fit the controller schema: %v", err)
	}
	q := func(query string, args ...any) string {
		t.Helper()
		var v string
		if err := db.Read().QueryRowContext(ctx, query, args...).Scan(&v); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
		return v
	}
	if got := q("SELECT state || '|' || recovery_owner || '|' || class FROM core_changes WHERE id = ?", m7rCompositeID); got != "applying|composite|composite" {
		t.Errorf("the seeded composite = %q, want applying|composite|composite", got)
	}
	if got := q("SELECT count(*) FROM core_locks WHERE (key = ? AND owner = ?) OR (key = ? AND owner = ?)",
		m7rLockKey, m7rCompositeID, m7rReservationKey, m7rReservationOwner); got != "2" {
		t.Errorf("seeded lock rows = %s, want 2", got)
	}
	if err := backup.Mark(ctx, db, quarantine.Marker{Tx: "01JTX", RestoredAt: now, BackupCreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ name, query, want string }{
		{"the composite", "SELECT state || '|' || quarantined_from FROM core_changes WHERE id = ?", "quarantined|applying"},
		{"its position", "SELECT direction || '|' || cursor FROM core_composites WHERE change_id = ?", "forward|0"},
		{"its step", "SELECT state || '|' || host_id || '|' || coalesce(child_plan_id, '') FROM core_composite_steps WHERE change_id = ?", "pending|" + agent + "|"},
		{"its children", "SELECT count(*) FROM core_changes WHERE parent_change_id = ?", "0"},
		{"its locks", "SELECT group_concat(key) FROM core_locks WHERE owner = ?", m7rLockKey},
	} {
		if got := q(c.query, m7rCompositeID); got != c.want {
			t.Errorf("%s after marking = %q, want %q", c.name, got, c.want)
		}
	}
	if got := q("SELECT count(*) FROM core_locks WHERE key = ?", m7rReservationKey); got != "0" {
		t.Errorf("update reservation rows after marking = %s, want 0: marking deletes a lock whose owner is not a change", got)
	}
}

func TestM7rUnitCheckToken(t *testing.T) {
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		res  backup.EnrollTokenResult
		want string // "" accepts
	}{
		{"an hour", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(ctlfleet.TokenTTL)}, ""},
		{"an hour less clock skew", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(ctlfleet.TokenTTL - 4*time.Minute)}, ""},
		{"no token", backup.EnrollTokenResult{ExpiresAt: now.Add(ctlfleet.TokenTTL)}, "empty"},
		{"expired", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(-time.Second)}, "already expired"},
		{"two hours", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(2 * time.Hour)}, "more than"},
		{"ten minutes", backup.EnrollTokenResult{Token: "t", ExpiresAt: now.Add(10 * time.Minute)}, "less than"},
	} {
		err := m7rCheckToken(tc.res, now)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitCheckNames(t *testing.T) {
	dbDir := "backups\ncontroller.db\ncontroller.db-shm\ncontroller.db-wal\ncontroller.db-wal.pre-01JTX\ncontroller.db.pre-01JTX\n"
	for _, tc := range []struct {
		name, listing, base, want string
	}{
		{"the database directory", dbDir, "controller.db", ""},
		{"the key directory", "agent\ncontroller.key\ncontroller.key.pre-01JTX\n", "controller.key", ""},
		{"no database", "controller.db.pre-01JTX\n", "controller.db", "controller.db is missing"},
		{"no set-aside copy", "controller.db\ncontroller.db-wal.pre-01JTX\n", "controller.db", "set-aside"},
		{"a staged file left", dbDir + "controller.db.restore-01JTX\n", "controller.db", "staged file controller.db.restore-01JTX"},
		{"a staged wal left", dbDir + "controller.db-wal.restore-01JTX\n", "controller.db", "staged file"},
		{"a journal left", dbDir + "restore-01JTX.journal\n", "controller.db", "journal restore-01JTX.journal"},
		{"nothing", "", "controller.key", "missing"},
	} {
		err := m7rCheckNames(tc.listing, tc.base)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM7rUnitExecStartPre(t *testing.T) {
	const bin = "/usr/local/lib/darkpanel/darkpanel"
	show := func(argv, tail string) string {
		return "ExecStartPre={ path=" + bin + " ; argv[]=" + argv + " ; ignore_errors=no ; start_time=[Wed 2026-11-04 12:00:01 UTC] ; stop_time=[Wed 2026-11-04 12:00:01 UTC] ; pid=812 ; " + tail + " }\n"
	}
	for _, tc := range []struct {
		name, out, want string
	}{
		{"a clean run", show(bin+" controller restore --recover", "code=exited ; status=0"), ""},
		{"a run that failed", show(bin+" controller restore --recover", "code=exited ; status=1"), "clean exit"},
		{"a run that was killed", show(bin+" controller restore --recover", "code=killed ; status=9/KILL"), "clean exit"},
		{"a unit that never ran it", "ExecStartPre={ path=" + bin + " ; argv[]=" + bin + " controller restore --recover ; ignore_errors=no ; start_time=[n/a] ; stop_time=[n/a] ; pid=0 ; code=(null) ; status=0/0 }\n", "clean exit"},
		{"another command", show(bin+" serve", "code=exited ; status=0"), "other than controller restore --recover"},
		{"no line", "", "no ExecStartPre"},
	} {
		err := m7rExecStartPre(tc.out)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitLab: the restored controller's addresses are the next ones of
// the M2 fleet and are valid agent addresses, and the commands that install
// the binary and the role are the ones install.sh runs.
func TestM7rUnitLab(t *testing.T) {
	if want := net.JoinHostPort(m7rRestoredPriv, strconv.Itoa(listen.AgentPort)); m7rRestoredAddress != want {
		t.Errorf("m7rRestoredAddress = %q, want %q", m7rRestoredAddress, want)
	}
	if err := install.CheckAgentAddress(m7rRestoredAddress); err != nil {
		t.Errorf("the restored controller's address: %v", err)
	}
	seen := map[string]string{}
	for name, addr := range map[string]string{
		"controller IPv4": targetIPv4, "agent IPv4": agentIPv4, "restored IPv4": m7rRestoredIPv4,
		"controller IPv6": targetIPv6, "agent IPv6": agentIPv6, "restored IPv6": m7rRestoredIPv6,
		"controller private": controllerPriv, "agent private": agentPriv, "restored private": m7rRestoredPriv,
	} {
		if other, dup := seen[addr]; dup {
			t.Errorf("%s and %s share the address %s", name, other, addr)
		}
		seen[addr] = name
	}
	p := paths.Production()
	prepare, link := m7rBinaryCommands()
	if prepare != "sudo install -d -m 0755 "+p.BinaryDir || link != "sudo ln -sfn "+p.Binary()+" "+p.BinaryLink {
		t.Errorf("m7rBinaryCommands = %q, %q; want the binary directory made and the link on PATH set", prepare, link)
	}
	want := "sudo darkpanel system install --role controller --agent-listen 10.77.0.12:" + strconv.Itoa(listen.AgentPort) +
		" --agent-advertise 10.77.0.12:" + strconv.Itoa(listen.AgentPort)
	if got := m7rControllerInstall(m7rRestoredAddress); got != want {
		t.Errorf("m7rControllerInstall = %q, want %q", got, want)
	}
	if m7rDeadline < 30*time.Minute || m7rDeadline > 60*time.Minute {
		t.Errorf("m7rDeadline = %v, want 30 to 60 minutes: three VMs boot, install and restore", m7rDeadline)
	}
}
```

`test/harness/restore_workflow_unit_test.go`:

```go
//go:build harness

package harness

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

const (
	m7rwRunStep = "Run the M7 restore harness"
	m7rwJob     = "vm-m7r"
	// m7rwStepTimeout is the -timeout in minutes of the TestM7Restore step
	// and m7rwJobLimit the job's timeout-minutes.
	m7rwStepTimeout = 50
	m7rwJobLimit    = 70
)

// m7rwWorkflow checks the text of harness.yml for TestM7Restore: the vm-m7r
// job has the vm jobs' setup, a timeout-minutes of m7rwJobLimit, and a step
// that runs the test with the lab flags, a work directory of its own, a
// -timeout of m7rwStepTimeout minutes and its failure logs; and none of the
// vm-m5, vm-m5d, vm-m6n and vm-m6c jobs holds the test.
func m7rwWorkflow(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n", "vm-m6c"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM7Restore", "harness-m7r", "-m7r"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m7rwJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m7rwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m7rwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m7rwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m7rwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m7rwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m7rwJob, have, m7rwJobLimit)
	}

	run, ok := m4lStep(job, m7rwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m7rwJob, m7rwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM7Restore$'", " -m7r", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m7r"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m7rwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m7rwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m7rwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m7rwRunStep, m[1], m7rwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m7rDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m7rwRunStep, m[1], m7rDeadline)
	}
	if limit, _ := strconv.Atoi(limit[1]); limit < n+20 {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, under the step's -timeout plus 20 minutes of setup", m7rwJob, limit)
	}
	for _, want := range []string{"name: logs-m7r-${{ matrix.os }}", "harness-m7r/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m7rwJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m7rwWorkflowDoc has the parts of harness.yml that m7rwWorkflow reads.
const m7rwWorkflowDoc = `jobs:
  build:
    runs-on: ubuntu-24.04
    steps:
      - run: make harness-builds
  vm-m5:
    needs: build
    timeout-minutes: 100
    steps:
      - name: Run the M5 node harness
        run: >-
          go test -tags harness -timeout 70m -v -run '^TestM5Node$' ./test/harness/
          -args -os ${{ matrix.os }} -m5
  vm-m5d:
    needs: build
    timeout-minutes: 60
    steps:
      - name: Run the M5 DERP harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM5DERP$' ./test/harness/
          -args -os ${{ matrix.os }} -m5d
  vm-m6n:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M6 NPM harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6NPM$' ./test/harness/
          -args -os ${{ matrix.os }} -m6n
  vm-m6c:
    needs: build
    timeout-minutes: 60
    steps:
      - name: Run the M6 Cloudflare harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/
          -args -os ${{ matrix.os }} -m6c
  vm-m7r:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 70
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: test -e /dev/kvm
      - run: sudo apt-get install -y qemu-system-x86
      - uses: actions/checkout@x
      - uses: actions/setup-go@x
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-linux-amd64
      - uses: actions/download-artifact@x
        with:
          name: darkpanel-harness
      - uses: actions/cache@x
      - run: go vet -tags harness ./test/...
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m7r"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m7r-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m7r/**/*.log
`

func TestM7rUnitWorkflow(t *testing.T) {
	if err := m7rwWorkflow(m7rwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m7r job", "  vm-m7r:\n", "  vm-other:\n", "no vm-m7r job"},
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m6n job", "  vm-m6n:\n", "  vm-other:\n", "no vm-m6n job"},
		{"limit under 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "timeout-minutes 60, want 70"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 70"},
		{"no needs", "  vm-m7r:\n    needs: build\n", "  vm-m7r:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no restore step", "Run the M7 restore harness", "Run M7 restore", `no step named "Run the M7 restore harness"`},
		{"wrong test", "'^TestM7Restore$'", "'^TestM7Other$'", "TestM7Restore"},
		{"no flag", " -m7r\n", "\n", "-m7r"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m7r\"\n", "", "-workdir"},
		{"workdir of vm-m6c", "-workdir \"$RUNNER_TEMP/harness-m7r\"", "-workdir \"$RUNNER_TEMP/harness-m6c\"", "harness-m7r"},
		{"short timeout", "-timeout 50m -v -run '^TestM7Restore$'", "-timeout 40m -v -run '^TestM7Restore$'", "-timeout 40m"},
		{"long timeout", "-timeout 50m -v -run '^TestM7Restore$'", "-timeout 60m -v -run '^TestM7Restore$'", "-timeout 60m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m7r/**/*.log\n", "", "harness-m7r/**/*.log"},
		{"artifact name of vm-m6c", "name: logs-m7r-${{ matrix.os }}", "name: logs-m6c-${{ matrix.os }}", "logs-m7r-"},
		{"test added to vm-m6c", "-run '^TestM6Cloudflare$'", "-run '^TestM6Cloudflare$|^TestM7Restore$'", "vm-m6c job holds TestM7Restore"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m7r", "vm-m5d job holds -m7r"},
	} {
		if n := strings.Count(m7rwWorkflowDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m7rwWorkflowDoc, tc.from, tc.to, 1)
		if err := m7rwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM7rUnitWorkflowFile checks the committed workflow, which is the only
// place TestM7Restore runs.
func TestM7rUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m7rwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 2: Run the tests and see them fail**

```bash
mkdir -p /root/.cache/w7-tmp/19
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/19 GOTOOLCHAIN=local timeout 600 go test -tags harness -count=1 -run 'TestM7rUnit' ./test/harness/
```

Expected RED: the build fails and its first line names `undefined: m7rDecode` in `restore_unit_test.go`, followed by `m7rSeedStatements`, `m7rCompositeID`, `m7rCheckBackup`, `m7rCheckQuarantine`, `m7rCheckToken`, `m7rCheckNames`, `m7rExecStartPre`, `m7rRestoredAddress`, `m7rBinaryCommands`, `m7rControllerInstall` and `m7rDeadline`. A failure that names `backup.Status`, `backup.Read`, `backup.Write` or `ctlfleet.TokenTTL` means the base lacks Task 1, 9 or 16, and one that names `backup.Mark` the base lacks Task 8: stop and report it.

- [ ] **Step 3: Implement**

Create `restore_test.go` first and run the unit tests (only `TestM7rUnitWorkflowFile` may still fail, with `the workflow has no vm-m7r job`), then edit the workflow. Errors start with `harness:`. Every helper judges text and never touches a VM.

*The flag and the constants.* `m7rFlag` and `m7rDeadline` are as under Interfaces. The three restored addresses continue the lab's numbering (the controller is host 10, the agent 11, the restored controller 12) and are the values under Interfaces. `m7rRestoredAddress` is the restored controller's agent listen address and the address the enrollment token names.

*The helpers*, each with the exact error text fragments the unit tests look for:

1. `m7rDecode[T]` decodes the output of a CLI run with `--json`: one JSON document, unknown members refused (`json.Decoder.DisallowUnknownFields`), and nothing after it (`harness: the CLI printed more than one JSON document`). Text before the document, empty output and a decode error are errors that start `harness: decode the CLI's JSON:`. The Go zero value is returned with every error.
2. `m7rCheckBackup(data, passphrase)`: first, data that contains the passphrase bytes is `harness: the backup file holds the passphrase in the clear`. Then `backup.Read` over `data` with a copy of the passphrase (the format code clears the slice it is given), with a sink that, for each member, reads its first 17 bytes, drains the rest, and records the name. The database member must begin with `SQLite format 3` and a NUL byte (`harness: the database member is not an SQLite database`); the key member must begin with `secret.KeyPrefix` and be at most `backup.MaxKeyMember` bytes (`harness: the key member is not a controller key file`). A `Read` error is returned as `harness: read the backup: %w`, so the wrong-passphrase and truncated-file messages of the format code show through. Afterwards the members must be exactly `backup.MemberDB` then `backup.MemberKey`.
3. `m7rCheckQuarantine(st, want)` checks, in this order, with these fragments: `the quarantine is not active`; `names no restore transaction`; `has no restoredAt`; `lists <n> records, want the one record <want.ID>`; `the quarantined record is <id>, want <want.ID>`; `the record's kind is "<kind>", want <want.Kind>`; `the record's action is "<action>", want <want.Action>`; `the record's host is "<host>", want "<want.HostID>"`; `the record's quarantinedFrom is "<state>", want <want.QuarantinedFrom>`; `the record's locks are null, want a list`; and, when the locks differ from `want.Locks` (`slices.Equal`), `the record's locks are <%v of the locks>, want <%v of want.Locks>`.
4. `m7rCheckToken(res, now)`: empty token `the enrollment token is empty`; `ExpiresAt` not after `now` `has already expired`; later than `now + ctlfleet.TokenTTL + 5 min` `is valid for more than <TTL>`; earlier than `now + TTL - 5 min` `is valid for less than <TTL>`. The 5 minutes are the clock skew allowed between the runner and the VM.
5. `m7rCheckNames(listing, base)` reads the whitespace-separated names of a directory listing (`ls -A`) and returns the first of these, scanning the names in order: a name containing `.restore-` is `the restore left the staged file <name>`; a name starting with `restore-` and ending `.journal` is `the restore left the journal <name>`. After the scan: `<base> is missing after the restore` when `base` itself is absent, then `no <base>.pre-<tx> set-aside copy of the original` when no name starts with `base + ".pre-"`. A name such as `controller.db-wal.pre-<tx>` therefore does not count as the set-aside copy of `controller.db`.
6. `m7rExecStartPre(show)` judges the output of `systemctl show -p ExecStartPre darkpanel-controller.service`: after trimming space, it must start with `ExecStartPre={` (`systemctl show printed no ExecStartPre`); its `argv[]=` field, up to the next `;`, must contain the words `controller restore --recover` (`ExecStartPre runs a command other than controller restore --recover`); and it must contain `code=exited ; status=0` as whole words (`ExecStartPre has not run to a clean exit`). A unit that never ran the line prints `code=(null)` and fails the third check. Use two package-level `regexp.MustCompile` values for the last two.
7. `m7rBinaryCommands()` returns `sudo install -d -m 0755 ` plus `paths.Production().BinaryDir`, and `sudo ln -sfn ` plus the production binary path, a space and the production link path (`BinaryLink`).
8. `m7rControllerInstall(addr)` returns `sudo darkpanel system install --role controller --agent-listen <addr> --agent-advertise <addr>`.
9. `m7rSeedStatements(agentID, now)` returns six `INSERT` statements, without trailing semicolons, in this order, every time `now.UTC()` formatted `2006-01-02T15:04:05.000000000Z` and every string a single-quoted SQL literal with `'` doubled: `core_plans` (`id` `m7rCompositePlan`, `host_id` '', `action` `m7rCompositeAction`, `version` 1, `actor` 'user:m7r', `input_hash` 'm7r', `state_hash` 'm7r', `diff_json` '[]', `guards_json` '[]', `confirmation` '', `created_at` now, `expires_at` now plus 24 hours); `core_changes` (`id` `m7rCompositeID`, `plan_id` `m7rCompositePlan`, `host_id` '', `action` `m7rCompositeAction`, `version` 1, `class` 'composite', `compensated_by` '', `actor` 'user:m7r', `state` 'applying', `input_json` '{}', `recovery_owner` 'composite', `created_at` and `updated_at` now); `core_composites` (`change_id` `m7rCompositeID`, `direction` 'forward', `cursor` 0, `steps_deadline` now plus 24 hours); `core_composite_steps` (`change_id` `m7rCompositeID`, `step_id` 'tailscaled', `ordinal` 0, `host_id` agentID, `action` 'tailscale.package.pin/v1', `after_json` '[]', `input_template` '{}', `state` 'pending', `updated_at` now); `core_locks` (`m7rLockKey`, `m7rCompositeID`); `core_locks` (`m7rReservationKey`, `m7rReservationOwner`). The composite is controller-side and has started no step, so it is in flight without a timing race and no agent work hangs on it.

*`TestM7Restore`.* It skips unless `-os`, `-m2` and `-m7r` are given (the same message as `TestM6Cloudflare`, naming `-m7r`), so the plain `go test` runs and `make test-go` never start a VM. It is a `go test` run of its own: the workflow selects it with `-run '^TestM7Restore$'`. Model it on `TestM6Cloudflare` and its `m6cRun`: a context with `m7rDeadline` cancelled at cleanup, a struct `m7rRun` holding the context, a `*fleet` for the source controller (its `scanner` stays nil because the lab has none), the restored VM, a second `*fleet` `g` for the restored controller's API session, the backup bytes, the passphrase (`rand.Text()`) and the quarantine transaction, and a table of named steps run with `t.Run`. A failed step marked `gate` ends the test; every failed step calls `(*m7rRun).logDiagnostics(t)`, which logs the last 60 lines of `journalctl -u darkpanel-controller.service --no-pager` of the source and the restored controller.

The lab is three target VMs and no scanner, because the harness limit is three VMs (`-vms`, default 3) and a scanner would be a fourth. `NewLab(t)`, `harnessImages`, `vm.NewKey` and `lab.Boot` are used as `m5dLab` uses them, with three `vm.Config` values built the way the `target` closure of `m5Configs` builds one (2048 MiB, 2 CPUs, the lab's MACs and multicast ports, `TargetScript`, the shared key): `controller` is host 10 with `targetIPv4`/`targetIPv6` and `controllerPriv`, `agent` is host 11 with `agentIPv4`/`agentIPv6` and `agentPriv`, `restored` is host 12 with the three `m7rRestored*` values. Do not edit `m5_helpers_test.go`; the closure is copied, not shared. With no release server, every binary is installed by hand: `m7rBinaryCommands()` prepare command, `VM.CopyFile(ctx, *binFlag, paths.Production().Binary(), 0o755)`, the link command.

The steps, in order (`gate` steps are marked):

1. `install-source` (gate): install the binary on the controller and the agent VMs; run `m7rControllerInstall(agentAddress)` on the controller; wait for both units of the controller (`darkpanel-controller.service`, `darkpanel-agent.service`) to be active, quoting their journals when they are not; `createAdmin`, `login`; wait with `waitHosts` until the local host is listed enrolled and online and keep its id in `fleet.localID`; create the agent's host with `f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant)`, keep `grant.Host.ID` as `agentID`, and enroll the agent with `RunInput(ctx, "sudo darkpanel system install --role agent", strings.NewReader(grant.Token+"\n"))` (the token goes through standard input, never into the command line); wait until the agent is enrolled and online.
2. `seed` (gate): on the source controller, while it runs, `RunInput(ctx, "sudo sqlite3 -bail " + paths.Production().ControllerDB(), strings.NewReader(".timeout 10000\nBEGIN IMMEDIATE;\n" + strings.Join(m7rSeedStatements(f.agentID, time.Unix(vmNow(ctx, t, controller), 0)), ";\n") + ";\nCOMMIT;\n"))`. Then `sudo sqlite3 -separator '|' <ControllerDB> "SELECT state, recovery_owner FROM core_changes WHERE id = '<m7rCompositeID>'"` must print `applying|composite`, and `SELECT count(*) FROM core_locks WHERE owner IN ('<m7rCompositeID>', '<m7rReservationOwner>')` must print `2`. Wave 7's own code never writes these rows; the seed stands for a composite and an update that were in flight when the backup was taken.
3. `backup` (gate): write the passphrase to `/root/m7r-pass` on the controller through `RunInput` into `sudo sh -c 'umask 077 && cat > /root/m7r-pass'`; run `sudo darkpanel controller backup --out /var/tmp/m7r.dpbk --passphrase-file /root/m7r-pass`; its output must contain `Backup written to /var/tmp/m7r.dpbk`. Read the file back with `sudo cat`, require its length to equal `sudo stat -c %s`, and run `m7rCheckBackup` on it with the passphrase. A second run to the same `--out` must fail with `refusing to overwrite`, and `ls -A /var/tmp` must show no `.m7r.dpbk.` partial file. The two `seed` queries, run again on the source controller, must print the same answers: the composite and both locks were in the database before and after the copy, so the backup holds them.
4. `install-restored` (gate): install the binary on the restored VM, run `m7rControllerInstall(m7rRestoredAddress)`, wait for both units to be active. Copy the backup, the passphrase file and a second file holding a different passphrase to `/var/tmp/m7r.dpbk`, `/root/m7r-pass` and `/root/m7r-wrong` through local files in `t.TempDir()` and `VM.CopyFile` (which writes as root; the mode is 0600).
5. `refused-while-running`: `sudo darkpanel controller restore --in /var/tmp/m7r.dpbk --passphrase-file /root/m7r-pass` on the restored VM fails, its error text contains `stop darkpanel-controller.service and darkpanel-agent.service before a restore`, and `sudo ls -A` of the controller directory shows no name containing `.pre-` or `.restore-` and no journal.
6. `stop-services` (gate): `sudo systemctl stop darkpanel-controller.service darkpanel-agent.service`, and wait until `unitState` reports both inactive.
7. `wrong-passphrase`: the same restore with `--passphrase-file /root/m7r-wrong` fails with `wrong passphrase or damaged backup file`, and the controller directory is still untouched as in step 5.
8. `restore` (gate): the restore with the right file succeeds; its output contains `Restored the backup taken` and `restore quarantine`. `sudo ls -A` of the controller directory goes through `m7rCheckNames(listing, "controller.db")` and `sudo ls -A` of the directory of the key file through `m7rCheckNames(listing, "controller.key")`. `sudo stat -c '%U %a'` of the installed database and key must each print `darkpanel 600`. The SHA-256 of the key file on the restored VM (`sudo sha256sum`, first field) must equal that on the source controller: the key came from the backup, not from the fresh install.
9. `start` (gate): `sudo systemctl start darkpanel-controller.service darkpanel-agent.service`; wait for the controller unit to be active, quoting its journal on failure; `sudo systemctl show -p ExecStartPre darkpanel-controller.service` goes through `m7rExecStartPre`. The restored VM's own agent holds credentials of the fresh install, which the restored database does not know, so no step asserts anything about it.
10. `quarantine` (gate): `sudo darkpanel controller quarantine list --json`, retried with `eventually` for 2 minutes while the controller starts, through `m7rDecode[backup.Status]` and `m7rCheckQuarantine(st, backup.Record{ID: m7rCompositeID, Kind: "composite", Action: m7rCompositeAction, HostID: "", QuarantinedFrom: core.StateApplying, Locks: []string{m7rLockKey}})`. Then read the database with `sudo sqlite3 -separator '|' <ControllerDB>`: `SELECT state, quarantined_from FROM core_changes WHERE id = '<m7rCompositeID>'` prints `quarantined|applying`; `SELECT count(*) FROM core_locks WHERE owner = '<m7rCompositeID>'` prints `1` (the composite's lock is kept); `SELECT count(*) FROM core_locks WHERE key = '<m7rReservationKey>'` prints `0` (the update reservation is deleted). Nothing is unwound: `SELECT direction, cursor FROM core_composites WHERE change_id = '<m7rCompositeID>'` prints `forward|0`, `SELECT state FROM core_composite_steps WHERE change_id = '<m7rCompositeID>'` prints `pending`, and `SELECT count(*) FROM core_changes WHERE parent_change_id = '<m7rCompositeID>'` prints `0`.
11. `refused-in-quarantine`: `g := &fleet{controller: restored, password: f.password, totpSecret: f.totpSecret, lastCode: f.lastCode}`, `login(ctx, t, g)` (the restored database holds the admin and its TOTP secret), then `g.api.Apply(ctx, "", "core.host.create/v1", ...)` for a host named `blocked` must fail with an `*APIError` (`errors.As`) whose `Code` is `core.quarantined`.
12. `re-enroll` (gate): `sudo darkpanel controller quarantine enroll-token <agentID> --json` on the restored VM, through `m7rDecode[backup.EnrollTokenResult]` and `m7rCheckToken(res, time.Unix(vmNow(ctx, t, restored), 0))`; then on the agent VM `RunInput(ctx, "sudo darkpanel system install --role agent", strings.NewReader(res.Token+"\n"))`; then `g.api.Hosts` until the agent's host is `core.HostEnrolled` and `Online` on the restored controller (3 minutes).
13. `release-refused`: `sudo darkpanel controller quarantine release` fails with `records are still quarantined`, and `quarantine list --json` still reports `Active`. The three "nothing is unwound" queries of step 10, run again now that the controller has been up for minutes, print the same answers.
14. `resolve` (gate): `quarantine resolve <m7rCompositeID> never --json` through `m7rDecode[backup.Record]` must answer that id with kind `composite` and `QuarantinedFrom` `applying` (its one step names the agent and has no child plan, so it is not applied, W7-R4). The database now answers `abandoned|restore: never applied` to `SELECT state, error_message FROM core_changes WHERE id = '<m7rCompositeID>'` and `0` to `SELECT count(*) FROM core_locks WHERE owner = '<m7rCompositeID>'`.
15. `release` (gate): `quarantine release --json` through `m7rDecode[backup.ReleaseResult]` has an empty `HeldLocks`; `quarantine list --json` is no longer `Active`; and within a minute (`eventually`) `g.api.Apply` of `core.host.create/v1` for a host named `after-release` completes.

Budget: the three VMs boot in parallel, and the longest steps are the two installs and the re-enrollment; the scenario deadline is 40 minutes, the workflow step's `-timeout` 50 and the job's `timeout-minutes` 70, under the 120 minutes of gate 7.

Four points no test of this plan verifies and the first VM run settles; if the run shows one of them wrong, fix a wrong harness expectation in this task and report a product defect as a finding in the task report, never weakening a step to pass. (a) `systemctl show -p ExecStartPre` prints `argv[]=` and `code=exited ; status=0` in this shape on all four images (systemd 249 to 257); if not, change `m7rExecStartPre` and its unit-test fixture together. (b) `system install --role agent` on an agent that already holds credentials accepts a token from another controller and replaces them. (c) A token issued by the restored controller enrolls an agent that is enrolled to the source controller, although the two controllers have different addresses and the restored one starts in quarantine. (d) The running source controller leaves the seeded composite alone until the backup (the `backup` step's repeated queries show it), because composite recovery claims composites only at start; if it does not, stop and report it as a finding rather than changing the seed.

*The workflow.* Append this job to `.github/workflows/harness.yml` after `vm-m6c`, separated by one blank line, as it is (it is `vm-m6c` with its name, limit, comments, step name, flag, work directory and artifact name changed):

```yaml
  vm-m7r:
    # The controller restore test's job, beside vm-m6c. The limit is the
    # step's -timeout plus 20 minutes of setup. TestM7Restore boots three
    # target VMs and no scanner, and does not read -derper.
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 70
    strategy:
      fail-fast: false
      matrix:
        os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]
    steps:
      - name: Enable KVM
        run: |
          if [ ! -e /dev/kvm ]; then
            echo "::error::/dev/kvm is missing on this runner; the harness needs KVM (ADR 0008)"
            exit 1
          fi
          echo 'KERNEL=="kvm", GROUP="kvm", MODE="0666", OPTIONS+="static_node=kvm"' | sudo tee /etc/udev/rules.d/99-kvm4all.rules
          sudo udevadm control --reload-rules
          sudo udevadm trigger --name-match=kvm
      - name: Install QEMU and cloud-image-utils
        run: |
          sudo apt-get update
          sudo apt-get install -y --no-install-recommends qemu-system-x86 qemu-utils cloud-image-utils
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
          cache-dependency-path: go.sum
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-linux-amd64
          path: bin
      - uses: actions/download-artifact@9000827ccba6bdab643e8b6fd33ac0654aef8333 # v8.0.2
        with:
          name: darkpanel-harness
          path: bin/harness
      - uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: ~/.cache/darkpanel-harness
          key: harness-images-${{ matrix.os }}-${{ hashFiles('test/harness/images.json') }}
      - run: go vet -tags harness ./test/...
      # TestM7Restore installs a controller and an agent, seeds a composite
      # in flight, backs the controller up, restores the backup onto a
      # third VM, and re-enrolls the agent there through the restore
      # quarantine. Its own deadline is 40 minutes, and the extra 10 leave its
      # VM cleanups time to finish.
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m7r"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-m7r-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m7r/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

`harness.yml` is the only workflow this task edits, and `release.yml`, `ci.yml` and `runner-probe.yml` stay as they are. No other job's text changes.

- [ ] **Step 4: Run the tests and see them pass**

```bash
cd "$(git rev-parse --show-toplevel)" && TMPDIR=/root/.cache/w7-tmp/19 GOTOOLCHAIN=local timeout 600 go test -tags harness -count=1 -v -run 'TestM7rUnit' ./test/harness/
TMPDIR=/root/.cache/w7-tmp/19 GOTOOLCHAIN=local timeout 300 go test -tags harness -count=1 -v -run 'TestM7Restore$' ./test/harness/
```

Expected GREEN: the nine tests `TestM7rUnitDecode`, `TestM7rUnitCheckBackup`, `TestM7rUnitCheckQuarantine`, `TestM7rUnitCheckToken`, `TestM7rUnitCheckNames`, `TestM7rUnitExecStartPre`, `TestM7rUnitLab`, `TestM7rUnitWorkflow` and `TestM7rUnitWorkflowFile` print `--- PASS`, and `TestM7Restore` prints `--- SKIP` with its no-flags message. `TestM7Restore` itself runs only in `harness.yml`: the report says it did not run on this host and that its first execution is the branch's gate 7 run (Global Constraints). Then run the task's full verification and record the real output of each command:

```bash
mkdir -p /root/.cache/w7-tmp/19
export TMPDIR=/root/.cache/w7-tmp/19 GOTOOLCHAIN=local
timeout 600 go build ./...
timeout 300 go vet ./...
timeout 900 go test -race -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
timeout 300 go vet -tags harness ./test/...
timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
```

- [ ] **Step 5: Commit, then clean up**

```bash
git add test/harness/restore_test.go test/harness/restore_unit_test.go test/harness/restore_workflow_unit_test.go .github/workflows/harness.yml
git commit -m "test(harness): restore a controller backup on a VM"
```

```bash
rm -rf /root/.cache/w7-tmp/19
GOTOOLCHAIN=local timeout 120 go clean -testcache
```

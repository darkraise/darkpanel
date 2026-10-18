# M1 Dispatch, CLI and Assembly Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build parts P11, P13 and P14 of the M1 platform core (plan 4 of 6 in the spec's "Plans" section) — the controller audit store and journal ingester, the agent runtime, the dispatcher, the generated CLI and the assembled `darkpanel serve` — together with plan 3's follow-ups in the API, identity and core actions, so that the first admin, a token, the host-information module and the audit log work end to end through the socket, the API and the CLI.

**Architecture:** Four parts land in one plan. Part A (P11) adds `internal/controller/audit`, `internal/agent/runtime` and `internal/controller/dispatch`, plus additive changes to `internal/core/lifecycle` (journal source, handler panic policy, Verify deadline) and `schema.Redact`. Part B (P13) adds `internal/cli`, a cobra tree generated from the registry with typed flags, plan-confirm-apply prompts and a pinned web CA. Part C closes plan 3's follow-ups in `api`, `auth` and `coreactions`. Part D (P14) adds `internal/controller/server`, which opens storage, starts the services and serves the listeners and the controller socket in a fixed order, rewires `cmd/darkpanel` to the generated CLI, registers hostinfo, commits the schema snapshots, removes `internal/deps` and adds the end-to-end test.

**Tech Stack:** Go 1.27.2, modernc.org/sqlite v1.60.1, github.com/spf13/cobra v1.10.2, golang.org/x/term v0.47.0, github.com/mdp/qrterminal/v3 v3.2.1, golang.org/x/sys v0.49.0, github.com/oklog/ulid/v2 v2.1.2, the standard library (`net/http`, `crypto/tls`, `log/slog`), golangci-lint v2.15.0, GNU make.

**Spec:** docs/superpowers/specs/2026-10-17-m1-platform-core-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves (31 of its 43 tasks are heavy).

**Parallelism:** waves — 1-3 | 4 | 5 | 6 | 7-9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19-21 | 22 | 23-24 | 25 | 26 | 27 | 28 | 29 | 30 | 31 | 32 | 33 | 34 | 35-36 | 37 | 38 | 39 | 40-42 | 43

**Integration check:** `timeout 1500 make check`

**Worktree setup:** `timeout 300 pnpm -C web install --frozen-lockfile`

## Global Constraints

- The spec is the source of truth, including its "Owner decisions" section; this plan's Assumptions record where it departs and why. Where a step gives a file's complete content, write exactly that content; where it gives replacement blocks, apply exactly those.
- Part labels: Part A is P11 (dispatch, agent runtime and audit; Tasks 4, 5, 7, 10-14, 19, 23, 25-27), Part B is P13 (the CLI; Tasks 1, 8, 15-18, 20, 22, 24, 28, 29), Part C is plan 3's follow-ups in the API, identity and core actions (Tasks 2, 3, 6, 9, 30-34), Part D is P14 (assembly; Tasks 21, 35-43). Task text and Contracts refer to them by these labels.
- No dependency changes: every module this plan imports is already pinned in `go.mod`. No task edits `go.mod` or `go.sum`, and `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing after every task, including Task 36, which deletes `internal/deps/deps.go`.
- Every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001). The `Makefile` exports it; a `go` command typed outside `make` is prefixed with it, as every step here does. Every build, test and lint command carries an explicit `timeout`.
- A task edits only the files in its own `**Files:**` block.
- In a waves run every task executes in its own git worktree created by the controller and commits only there; the controller merges a wave's worktrees before starting the next wave.
- `**Items:**` numbers are row numbers of the register `docs/superpowers/registers/2026-10-17-m1-platform-core.md`.
- After every task these pass: `GOTOOLCHAIN=local timeout 300 go vet ./...` (no output); `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` (every line `ok` or `?`); `GOTOOLCHAIN=local timeout 600 go test -race -count=1` on the task's packages (`ok`); `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` (no output). Tasks of one wave lint at the same time and golangci-lint guards its cache with one lock file, hence `--allow-serial-runners`.
- Test isolation (spec "Parallel decomposition"): every Go test uses `t.TempDir()` for databases, keys, files and home directories, binds TCP only on `127.0.0.1` port `0`, builds Unix socket paths with `os.MkdirTemp("", "dp")` (socket paths are limited to 108 bytes), writes no fixed path outside its temporary directory, and uses `coretest.FakeClock` for timers and expiry (the exceptions are named in the part constraints below). No test shares a file, port or database with another task's tests.
- TDD: Step 1 writes the test, Step 2 runs it and sees the failure the step quotes, Step 3 implements, Step 4 runs the test and the checks above. Tasks 21, 36 and 43 change no Go behaviour and have no test step; Tasks 40 and 42 add tests over code that already exists, so their Step 2 expects PASS.
- Code style: match the surrounding code. Every exported identifier has a doc comment (revive); files are `gofmt`- and `goimports`-clean; comments only for a non-obvious why; a `//nolint` names the rule and a reason.
- Errors: refusals are `*errcode.Error` with client-safe details (no secret, path or SQL text); anything else is a plain error, which the API reduces to `internal`. Redacted secret values are the JSON string `"[redacted]"` (`secret.Redacted`). No password, TOTP code or secret, session id, CSRF token or token secret appears in a log line, error detail, diff line or guard message. Test failure messages may print the fixed test credentials a test made itself (they are not secrets of any real system); `noSecret` in `internal/controller/coreactions` names positions only (Part C).
- Migrations are SQL files named `NNNN_<name>.sql` with STRICT tables, times as fixed-width RFC 3339 UTC text (`2006-01-02T15:04:05.000000000Z`) and ULID text ids. This plan adds exactly two: `internal/core/store/migrations/agent/0004_journal_source.sql` (Task 5) and `internal/core/store/migrations/controller/0004_audit.sql` (Task 12).
- A development database created by `serve --dev` before Task 5 or Task 12 merged lacks that task's migration; delete the development state directory (`$XDG_STATE_HOME/darkpanel-dev/`) before running `serve --dev` on a later tree (register row 31). Go tests always start from fresh temporary databases.
- Never commit `.superpowers/`.
- English only. Commits use `<type>(<scope>): <subject>` with type one of feat, fix, docs, style, refactor, test, chore, perf; the whole first line is at most 50 characters, imperative, no trailing period; no AI attribution of any kind.
- P11 (Tasks 4, 5, 7, 10, 11, 12, 13, 14, 19, 23, 25, 26 and 27): the part owns `internal/controller/audit/`, `internal/controller/dispatch/`, `internal/agent/runtime/`, `internal/core/schema/redact.go` and `redact_test.go`, and the migrations `internal/core/store/migrations/controller/0004_audit.sql` and `internal/core/store/migrations/agent/0004_journal_source.sql`. Its edits to `internal/core/contract.go` (only `JournalEntry.Source`) and to `internal/core/lifecycle/` (Source propagation, `Protect`, `VerifyTimeout`) are additive: no existing exported signature changes. It edits nothing in `internal/controller/api`, `auth`, `coreactions`, `listen`, `internal/cli`, `cmd/`, `internal/modules/modules.go`, `.golangci.yml`, `Makefile` or `go.mod`.
- P11: `core_audit` and `core_journal_cursor` are read and written only through `internal/controller/audit`; `core_change_index` only through `internal/controller/dispatch`. The controller never deletes `core_audit` rows. Audit inputs are stored exactly as given: the engine and `auth` hand them over already redacted.
- P11: `dispatch.Hosts` writes `core_module_state` only from an agent's `Hello`: `available`, `reason` and `checked_at` of existing rows, and whole rows (with `enabled` from the module's `DefaultEnabled`) for modules that have none. It never changes `enabled` on an existing row; only `core.module.enable` and `core.module.disable` do.
- P11: a module is usable on a host only when its `core_module_state` row has `enabled = 1` and `available = 1`. Every agent action and resource of another module answers `module.unavailable`; controller-side actions are never refused as unavailable; `Confirm` and `Revert` are never refused as unavailable (a pending change must stay settleable).
- P11 (register row 69, obligation 1): `Dispatcher.Apply` runs only a plan id that the same dispatcher's `Plan` indexed for exactly that host and action reference; any other plan id is `plan.not_found`. A later change must not weaken this.
- P11 (register row 69, obligation 2): `Dispatcher.Change`, `Confirm` and `Revert` act on the change that the controller engine holds, else on the change of the host that `core_change_index` names, and a view whose `HostID` differs from that host is refused.
- P11 (register row 69, obligation 3): every `*errcode.Error` below status 500 that dispatch or the runtime creates carries only module ids, host ids, action references, plan and change ids, the fixed reasons (`ReasonDisabled`, `ReasonMigrationFailed`, `coreactions.ReasonNotReported`) and the module-authored preflight reason. A migration error is logged and never returned. Agent errors that are not `*errcode.Error` pass through as plain errors.
- P11: every handler call the lifecycle engine makes (Read, Plan, Apply, Verify, Snapshot, Revert) and every resource read and preflight check the runtime makes runs inside `lifecycle.Protect`. A panic is logged once, with the stack, and its value only when it is a `runtime.Error`; it is never logged with the handler's input.
- P11: `Verify` runs under `lifecycle.VerifyTimeout` in apply and in `Recover`. Lifecycle tests shorten the engine's unexported `verifyTimeout` to 50 ms; they are the only lifecycle tests that wait on real time, apart from goroutine synchronisation.
- P11: lifecycle tests simulate a crash with the fixture's `CrashApply` input flag (`runtime.Goexit`), never with a panic, because the engine now recovers panics.
- P11: the runtime's purge loop and the ingester's loop use `core.Clock.AfterFunc`, never `time.Ticker`; their tests use `coretest.FakeClock`.
- P11: the fixture modules of the runtime and dispatch tests (`kv`, `bad`, `off`, `flaky`, `boom`, `ctl`) exist only in `_test.go` files and are registered only there.
- P13 (Tasks 1, 8, 15, 16, 17, 18, 20, 22, 24, 28 and 29, `internal/cli`): only files under `internal/cli/` change. The package never imports `internal/controller/server`, `internal/controller/api`, `internal/controller/listen` or anything that opens a database; its non-test code imports from this repository only `internal/core`, `internal/core/errcode`, `internal/core/schema` and `internal/paths`. Tests may import `internal/controller/coreactions` (for the real core module's shape, built with zero `Deps`) and `internal/controller/auth` (for `TOTPCode`) and nothing else from the controller.
- P13: no `go.mod` or `go.sum` change. `github.com/spf13/pflag` is never imported directly (it is an indirect requirement); flag helpers take `*cobra.Command` and use `cmd.Flags()`.
- P13: every string the CLI writes for people, including problem details, field errors, diff lines, guard messages, table cells and headers, keys, values, list scalars and hints, passes through `sanitize` (Task 1). `--json` writes the server's bytes unchanged. `controller web-ca` writes only a re-encoded PEM block and its `SHA-256 fingerprint:` line, or with `--json` the server's bytes.
- P13: a secret input field never gets a flag. Secrets come from the `--input` document, from `terminal.secret` (no echo) on a terminal, or one line each from standard input. The CLI never prints a password, TOTP code or token except the secret outputs the API returns once (`token`, `totpSecret`), and `noteSecrets` says so after them.
- P13: every command's `RunE` is wrapped by `markRuns`, which `NewRoot` calls last; a command added to the tree after `NewRoot` returns is not wrapped and its errors exit 2. Usage errors are `usagef`, refused changes `refusedf`; `ExitCode` is the only mapping to exit codes.
- P13: requests carry `Content-Type: application/json` exactly when they have a body and `Authorization: Bearer <token>` only when a token is configured; the socket client never sends a token. The config file is read only through `loadConfig`, which checks the open descriptor.
- P13: a mutating command applies only after `--yes` or a terminal `y`; an irreversible one only with its exact confirmation phrase, from `--confirmation` or typed. A failing guard, a declined question or a wrong phrase sends no apply request.
- P13: tests use the fake API (`newFakeAPI`, `httptest` on 127.0.0.1 port 0) or the fake socket (`newFakeSocket`, a path under `os.MkdirTemp("", "dp")`), `t.TempDir()` for every file and home directory, and `Options.Getenv`/`Options.UserHomeDir` instead of the process environment. No test opens a database or starts the real server.
- Part C (Tasks 2, 3, 6, 9, 30, 31, 32, 33 and 34, API, identity and core-action follow-ups): edits only `internal/controller/api/`, `internal/controller/auth/`, `internal/controller/coreactions/` and `.golangci.yml`. It adds no migration, no error code and no dependency, and it does not read `core.JournalEntry.Source`.
- Part C: `auth.Service.ResetPassword` revokes every unrevoked token of the user (`revoked_at` = now) in the same write transaction that stores the new hash and ends the sessions; a token already revoked keeps its first `revoked_at`. `ResetTOTP` still keeps tokens.
- Part C: in `auth.Login`, any error after the password verified counts once in the limiter and writes one login audit row with result `error` and error code `internal`; the original error is returned, and an error from that audit row is dropped. Errors before the password check are neither counted nor audited. Nothing here changes the timing of a login.
- Part C: the network listeners never show a change of a break-glass action, nor a controller-side change whose action is not registered. `GET /api/v1/changes` leaves them out; `GET /api/v1/changes/{id}`, `POST /api/v1/changes/{id}/confirm` and `.../revert` answer them `change.not_found` with detail `no change <id>` (`errHiddenChange`), exactly as a missing change, and confirm and revert dispatch nothing. Any later network route that takes or returns changes goes through `networkVisible`. The controller socket, the apply response and `core.audit.list` are unchanged.
- Part C: `recoverPanics` never sends a handler's `Set-Cookie` with its `internal` answer.
- Part C: no file under `internal/controller/api/` calls `http.Error`; the `forbidigo` rule in `.golangci.yml` enforces it, and its `exclusions` rule (`path-except: ^internal/controller/api/`) keeps it off every other package. A later edit of `.golangci.yml` keeps both entries.
- Part C: the listener state is read on each request through an atomic pointer; `SetListenerState` is safe to call at any time, including while requests are served.
- Part C: test failure messages never quote a secret; `noSecret` in `internal/controller/coreactions` names positions only.
- P14 (Tasks 21, 35, 36, 37, 38, 39, 40, 41, 42 and 43, assembly): the part owns `internal/controller/server/` (new), `cmd/darkpanel/`, `internal/modules/modules.go` and `internal/modules/*_test.go`, the generated `schemas/` directory, `README.md`, `CONTRIBUTING.md` and the wording of `docs/adr/0010-web-certificate-authority.md`, and it deletes `internal/deps/`. It edits no other package, adds no migration, no error code and no dependency, and `go.mod`/`go.sum` stay byte-identical (`go mod tidy -diff` prints nothing after Task 36).
- P14: `server.Start` runs its three steps in this order and only this order: `openStorage` (Task 37: directories, socket-directory check, keys, lock, databases, controller migrations, local host), `startServices` (Task 38: audit store, identity, registry, agent runtime, Hello, controller engine, Recover, purges, ingester, dispatcher), `startListeners` (Task 39: web authority, API, listener service, listener-state source, controller socket). Each acquisition registers its stop with `onShutdown` right after it succeeds, and nothing else stops it.
- P14 (register row 74): `Shutdown` runs the registered stops in reverse, one at a time, under one mutex; a second call, concurrent or later, waits and returns the first call's result. No code calls `listen.Service.Shutdown`, `http.Server.Shutdown` of the socket, `Engine.Close`, `Runtime.Close`, `Ingester.Stop` or a database's `Close` except through these stops.
- P14 (register rows 69 obligation 4, 76): the controller socket's directory is checked (a directory, no group or other write bit, owned by the effective uid) before anything is created in it, and an exclusive `flock` on `controller.lock` in the socket's directory and in both data directories (`Paths.ControllerDir`, `Paths.AgentDir`) is taken before either database is opened or the socket is touched, so two serves that share the socket or a database refuse each other. The lock files are never deleted.
- P14: the server builds exactly one registry, `registry.BuildWith(coreactions.Module(coreactions.Deps{Auth, Audit, Modules: modules.All()}))`, and hands it to the agent runtime, `dispatch.Hosts`, the controller engine, the dispatcher and the API. The CLI, the schema generator and the tests use `registry.Build()`; `TestServerRegistryHasTheSameShape` keeps the two identical.
- P14 (register row 52): key, database, backup and socket paths come only from `server.Options.Paths` (`paths.Paths`). Production passes `paths.Resolve(dev)`, a text `slog` logger on standard error as `Log`, and nil or zero for every other override (`Clock`, `Hostname`, `Bind`, `Interfaces`, `AuthParams`). Tests pass `coretest.FakeClock`, `localBind` (every endpoint on `127.0.0.1:0`), a socket under `os.MkdirTemp("", "dp")` and cheap argon2id parameters.
- P14: `schemas/` changes only through `make schemas`, in the same commit as the type change that causes it; `go test ./internal/modules/` fails while a snapshot is missing, stale or orphaned. `schemas/released/` is never written by the generator.
- P14 (register row 70): every pointer field of a registered action's or resource's output type carries `jsonschema:"nullable"`; `TestPointerOutputsAreNullable` enforces it for every module a later plan registers.
- P14 (register row 30): in `cmd/darkpanel`, a refusal of `serve`'s command line (`--listen`, production mode without root) is returned from `PreRunE`, which `cli` leaves unmarked, so it exits 2; an error from `RunE` exits 1; `main` exits with `cli.ExitCode` and prints with `cli.PrintError`.
- P14: the end-to-end test drives the CLI in-process through `cli.NewRoot` over `registry.Build()`, never by executing a binary, and builds its deny-by-default matrix from `schema.OpenAPI(reg, api.Routes())`, so a route added later is covered without editing the test.

## Contracts

The foundation's names (`core`, `store`, `errcode`, `coretest`, `buildinfo`, `registry`, `paths`) are those of `docs/superpowers/plans/2026-10-17-m1-platform-foundation.md` › Contracts, plan 2's (`secret`, `platform`, `schema`, `lifecycle`, `hostinfo`) those of `docs/superpowers/plans/2026-10-17-m1-framework-libraries.md` › Contracts, and plan 3's (`auth`, `listen`, `api`, `coreactions`) those of `docs/superpowers/plans/2026-10-17-m1-identity-and-api.md` › Contracts, all as merged on `main` (`f28be88`; later commits change only register files). Where a document and the merged code differ, the code wins; where Part C below changes a behaviour plan 3 described, Part C's text replaces it.

This plan adds the names below, grouped by part (labels in Global Constraints). A citation `Contracts › Task N` points at the entry for Task N in its part. Each part's "How the next parts use it" and "Obligations on later plans" lists keep their drafting numbering, which tasks cite (for example "Part B obligation 3"); the last subsection says which task discharges each obligation that falls inside this plan.

#### Part A — P11: dispatch, agent runtime and audit

The foundation's names (`core`, `store`, `errcode`, `coretest`, `registry`, `paths`), plan 2's (`secret`, `platform`, `schema`, `lifecycle`) and plan 3's (`auth`, `coreactions`, `api`) are as merged on `main` at `f28be88`; where a document and the code differ, the code wins. Part P11 adds the following.

**`internal/core/schema`** (Task 4)

- `func Redact(t reflect.Type, raw json.RawMessage) json.RawMessage` (`redact.go`): `secret.RedactJSON(raw, schema.Output(t))`; when `schema.Output` fails (a nil type) it returns the JSON string `"[redacted]"`, never `raw`. It is `lifecycle.Config.Redact` on both sides, so it serves inputs, change outputs and `Engine.Read` alike (register row 50).

**`internal/core`** (Task 5)

- `core.JournalEntry` gains a last field `Source string`: the acting principal's `Source` (`"socket"` or a client address); empty for system actors (the revert timer, recovery, the journal's overflow entry).

**`internal/core/lifecycle`** (Tasks 5, 7, 10 and 11)

- Task 5: the agent migration `0004_journal_source.sql` adds `core_journal.source TEXT NOT NULL DEFAULT ''`. `(*AgentJournal).Append` stores `e.Source`; `(*AgentJournal).Entries` returns it.
- Task 7: every journal entry the engine writes carries the acting principal's `Source`: apply (`ok` and `error`), confirm and revert by a caller. Timer reverts (`system:revert-timer`), recovery (`system:recovery`) and the overflow entry carry `""`. Unexported: the `transition` struct gains `source string`.
- Task 10 (`guard.go`): `func Protect(log *slog.Logger, name, call string, f func() error) error` runs `f`; a panic in `f` is recovered and returned as a plain error whose text is `"<call> of <name> panicked"` (for example `Apply of fix.item.set/v1 panicked`, `preflight check ready of flaky panicked`). It is logged once at error level as `lifecycle: handler panic` with the attributes `handler` (name), `call`, `panic` (the text of a `runtime.Error`, otherwise only the value's type such as `string`) and `stack`. Unexported: `panicError{name, call}`, `(*Engine).protect(ref, call, f)`, `(*Engine).recheck` (formerly the function `recheck`). The engine wraps Read, Plan (in `Plan` and in apply's recheck), Snapshot, Apply, Verify and Revert. Outcomes: a panic before a change exists (Read, Plan, Snapshot, recheck) is a plain error and stores nothing, and the apply mutex is released; a panic in Apply or Verify fails the change with code `internal` and message `Internal error` (a revertible change is reverted first); a panic in Revert fails the change with `change.revert_failed`; a panic in recovery's Verify reports `verify failed: Internal error`. `changeError` does not log a panic a second time.
- Task 11: `const VerifyTimeout = 60 * time.Second`. Unexported: `Engine.verifyTimeout` (set to `VerifyTimeout` by `New`), `(*Engine).verify(ctx, def, env, in, out) error`, which runs `Verify` in its own goroutine (inside `protect`) with a context that ends after `verifyTimeout`, and returns the plain error `lifecycle: Verify of <ref> did not finish within <d>: context deadline exceeded` at the deadline even when the handler ignores its context. Apply and `Recover` both call it.
- Test fixture changes (`fixture_test.go`), used by Tasks 10 and 11: `itemIn.CrashApply` (`crashApply`) stops the goroutine with `runtime.Goexit` (replaces `PanicApply` and `errCrash`); `fixHost.panicOn string` and `(*fixHost).panicIf(call, name)` make one handler call panic with a value quoting the item name; `fixHost.onVerify func(ctx)` runs at the start of Verify. `guard_test.go` adds `item` (`"zz-item"`), `(*fixture).logged() *bytes.Buffer`, `(*fixture).panicOn(call)`, `wantPanicLogged`, `wantPlain`; `verify_test.go` adds `(*fixture).hangVerify()`.

**`internal/controller/audit`** (package `audit`, Tasks 12 and 13)

- Task 12 (`audit.go`, carries the package doc comment; migration `0004_audit.sql`):
  - Tables: `core_audit (id TEXT PRIMARY KEY, seq INTEGER, at TEXT, actor TEXT, host_id TEXT, action TEXT, version INTEGER, phase TEXT CHECK in apply/confirm/revert/recover/interrupt/login/logout, change_id TEXT, input_json TEXT, result TEXT CHECK ok/error, error_code TEXT, source TEXT)` with `UNIQUE INDEX core_audit_host_seq (host_id, seq)`; `core_journal_cursor (host_id TEXT PRIMARY KEY, acked_seq INTEGER)`; `core_change_index (change_id TEXT PRIMARY KEY, host_id, action, version, created_at)` with index `core_change_index_host`. All STRICT; empty strings, a zero seq and an empty input are stored as NULL.
  - `const DefaultListLimit = 100`, `MaxListLimit = 500`.
  - `type Store struct` (safe for concurrent use); `var _ core.Audit = (*Store)(nil)`; `func New(db *store.DB, clock core.Clock) *Store` (`clock` stamps entries whose `At` is zero).
  - `(*Store) Append(ctx, tx *sql.Tx, e core.JournalEntry) error`: the controller engine's `core.Journal`; inserts inside the change's transaction a row with null `host_id` and `seq`, `source` from `e.Source`.
  - `(*Store) Record(ctx, e core.AuditEntry) error`: keeps a non-empty `e.ID`, otherwise a ULID stamped from `e.At`; accepts any action, including `core.auth.login` and `core.auth.logout` with version 1.
  - `(*Store) Ingest(ctx, hostID string, entries []core.JournalEntry) error`: one transaction; a `(hostID, seq)` pair already stored is skipped (`ON CONFLICT (host_id, seq) DO NOTHING`).
  - `(*Store) List(ctx, q core.AuditQuery) ([]core.AuditEntry, string, error)`: newest first by id, exact-match filters `Actor`, `HostID`, `Action`, `Before` (ids below it); `Limit` 0 or less means 100, at most 500; `nextBefore` is the last returned id when older matching entries remain, else `""` (the semantics `coreactions/audit.go` relies on); an empty result is a non-nil empty slice.
  - Ids are ULIDs stamped from the event time, so id order is event order to the millisecond. Unexported: `timeFormat`, `(*Store).newID(at)`, `(*Store).insert(ctx, tx, e, skipDuplicate)`, `null(s)`, `fromJournal(hostID, e)`, `(*Store).ingest(ctx, tx, hostID, entries)`, `scan(rows)`. Test helpers in `audit_test.go`, used by Task 13: `t0`, `openControllerDB(t)`, `newTestStore(t)`, `count(t, db, query, args...)`, `list(t, s, q)`, `journalEntry(seq, at)`.
- Task 13 (`ingest.go`):
  - `const IngestInterval = 5 * time.Second`; unexported `journalPage = 500`.
  - `type IngesterConfig struct { Store *Store; Agents core.HostDirectory; Clock core.Clock; Log *slog.Logger }`; `type Ingester struct`; `func NewIngester(cfg IngesterConfig) *Ingester`.
  - `(*Ingester) IngestHost(ctx, hostID string) error`: reads the host's journal after its `core_journal_cursor` in pages of 500, stores each page and the new cursor in one transaction, then calls the agent's `Ack(cursor)` when the cursor is above what this process last acknowledged (so the first run after a restart acknowledges a stored cursor even with nothing new). A page whose sequences do not increase past the cursor is an error. After `Stop` it does nothing and returns nil.
  - `(*Ingester) IngestAll(ctx) error`: every `core_hosts` id in id order; one host's error (`audit: ingest host <id>: ...`) does not stop the others; errors are joined.
  - `(*Ingester) Start()`: arms `Clock.AfterFunc(IngestInterval)`; each tick runs `IngestAll`, logs an error at warn level and re-arms. A second `Start` does nothing.
  - `(*Ingester) Stop()`: stops the loop and waits for a running ingestion (one mutex is held for every run).
  - Unexported: `(*Store).cursor(ctx, hostID) (int64, error)`, `(*Store).advance(ctx, hostID, entries, seq) error`.

**`internal/agent/runtime`** (package `runtime`, Tasks 14 and 19)

- Task 14 (`runtime.go`, carries the package doc comment):
  - `const ProtocolVersion = 1`, `PurgeInterval = 10 * time.Minute`, `BackupsKept = 5`, `ReasonMigrationFailed = "the module's agent migrations failed; see the service log"`.
  - `type Config struct { DB *store.DB; Sealer core.Sealer; Clock core.Clock; Log *slog.Logger; Registry *core.Registry; HostID string; BinaryVersion string; BackupDir string; Exec core.Runner; FilesTestMode bool }`; every field but `FilesTestMode` is required (`runtime: Config.<Name> is required`).
  - `type Runtime struct`; `func Open(ctx context.Context, cfg Config) (*Runtime, error)`, in this order: migrates `cfg.DB` with `store.Migrate` (side agent, `BinaryVersion`, `BackupRoot: BackupDir`, `Keep: 5`, `Now: Clock.Now()`; core set first, then every non-core module's `Migrations[core.SideAgent]` in registration order); a core failure fails `Open`, a module failure is logged (`runtime: module migrations failed`, with the module and the error) and makes that module unavailable with `ReasonMigrationFailed` for this process. Then it builds the agent's one `platform.Files` from `platform.OwnedPaths(Registry.Modules())`, `platform.NewOwnership(DB)`, `Clock` and `FilesTestMode`, builds the agent engine (`lifecycle.NewAgentJournal(DB)`, `schema.NewValidator()`, `schema.Redact`, `Base` = HostID, DB, Exec, Files, Sealer, Clock, Log), runs `Recover` (an error fails `Open`), runs every non-core module's preflight checks in order (inside `lifecycle.Protect`, with the base `Env` and a zero principal; the first failure's error text is the reason; a module whose migration failed is not checked), runs `Purge` (an error fails `Open`) and arms the purge loop every `PurgeInterval`.
  - `(*Runtime) Close()`: stops the purge loop, waiting for a running purge, then `Engine.Close` (which waits for a running timer revert). Repeatable. The caller closes `DB` afterwards.
  - Unexported, used by Task 19: `Runtime` fields `cfg`, `base core.Env`, `engine *lifecycle.Engine`, `journal *lifecycle.AgentJournal`; `(*Runtime).modules() []core.Manifest` (non-core, registration order); `(*Runtime).check(ctx, m) core.ModuleStatus`; `(*Runtime).isBroken(id) bool`; `(*Runtime).setStatus(st)`; `(*Runtime).statuses() []core.ModuleStatus`. Test helpers in `fixture_test.go`: modules `kv` (agent table `kv_items`, `kv.item.get` read, `kv.item.set` revertible with lock `kv`, resource `kv.status` with a secret `token`), `bad` (failing migration, preflight counting runs), `flaky` (version 2, preflight fails until `fixHost.ready`), `boom` (preflight and resource panic); `t0`, `testHost` (`h1`), `alice`, `refGet`, `refSet`, `fixHost`, `fixture`, `newFixture(t)`, `(*fixture).config()`, `open()`, `restart()`, `count(...)`.
- Task 19 (`client.go`): `var _ core.AgentClient = (*Runtime)(nil)`.
  - `Hello(ctx) (core.AgentHello, error)`: `{ProtocolVersion: 1, BinaryVersion, OS: runtime.GOOS, HostID, Modules}`, the modules' last statuses in registration order (core excluded).
  - `Preflight(ctx, moduleID) (core.ModuleStatus, error)`: runs the module's checks again and keeps the result for `Hello`; a module whose migration failed is not checked and stays unavailable; `core` and unknown ids are `not_found` (`no module <id>`).
  - `Read`, `Plan`: `module.unavailable` (`module <id> is unavailable: <ReasonMigrationFailed>`) for a module whose migration failed, otherwise the engine's. Preflight failures and enablement are not checked here; the dispatcher enforces them.
  - `Resource(ctx, p, name)`: `not_found` (`no resource <name>`) for an unknown name; the migration gate; the resource's `Read` with the base `Env` and `p`, inside `lifecycle.Protect`; the output through `schema.Redact`.
  - `Apply`, `Confirm`, `Revert`, `Change`, `Changes`: the engine's. `Journal(ctx, afterSeq, limit)`: `AgentJournal.Entries`. `Ack(ctx, seq)`: `AgentJournal.Ack`.
  - Unexported: `(*Runtime).usable(name) error`.

**`internal/controller/dispatch`** (package `dispatch`, Tasks 23, 25, 26 and 27)

- Task 23 (`hosts.go`, carries the package doc comment):
  - `const ReasonDisabled = "disabled"`; unexported `timeFormat`.
  - `type HostsConfig struct { DB *store.DB; Registry *core.Registry; Clock core.Clock; Log *slog.Logger }`; `type Hosts struct`; `var _ core.HostDirectory = (*Hosts)(nil)`; `func NewHosts(cfg HostsConfig) *Hosts`.
  - `(*Hosts) Add(hostID string, c core.AgentClient)`: registers (or replaces) a host's client; the next reach says Hello again.
  - `(*Hosts) Agent(ctx, hostID) (core.AgentClient, error)`: `not_found` (`no host <id>`) without a client; on the first reach in this process, and after a failed Hello, calls `Hello` first and returns its error.
  - `(*Hosts) Hello(ctx, hostID) error`: calls the agent's `Hello`; a reply whose `HostID` differs is a plain error (`dispatch: host <id> answered Hello as <other>`) and writes nothing; otherwise, in one transaction, for every reported module the registry has (not `core`): `INSERT ... ON CONFLICT (host_id, module_id) DO UPDATE SET available, reason, checked_at` with `enabled` = `DefaultEnabled` for a new row, `reason` NULL when available, `checked_at` the clock's now. Logs `dispatch: host said hello`.
  - Unexported `(*Hosts).usable(ctx, hostID, moduleID) error`: nil when the row has `enabled = 1` and `available = 1`; otherwise `module.unavailable` with detail `module <m> is unavailable on host <h>: <reason>`, the reason being `ReasonDisabled` (enabled 0, whatever the availability), the row's reason, or `coreactions.ReasonNotReported` without a row.
  - Test helpers in `fixture_test.go`, used by Tasks 25, 26 and 27: modules `kv` (as in the runtime), `bad`, `off` (not `DefaultEnabled`), `flaky` (preflight always fails), `ctl` (controller-side `ctl.note.get` read and `ctl.note.set` compensable by itself); `t0`, `testHost` (`h1`), `refGet`, `refSet`, `refCtlGet`, `refCtlSet`, `harness{t, clock, reg, db, agentDB, rt, hosts}`, `newHarness(t)` (migrated controller database with local host `h1`, a real `runtime.Runtime` over its own database, `Hosts` with `h1` added), `migrated(t, path, side)`, `(*harness).count(...)`, `wantCode(t, err, code) *errcode.Error`.
- Task 25 (`dispatch.go`):
  - `const PlanIndexRetention = time.Hour`.
  - `type Ingester interface { IngestHost(ctx context.Context, hostID string) error }` (`*audit.Ingester` satisfies it).
  - `type Config struct { Registry *core.Registry; Engine *lifecycle.Engine; Hosts *Hosts; DB *store.DB; Clock core.Clock; Log *slog.Logger; Ingest Ingester }`; every field but `Ingest` is required (`dispatch: Config.<Name> is required`).
  - `type Dispatcher struct`; `func New(cfg Config) (*Dispatcher, error)`.
  - Routing rule (unexported `(*Dispatcher).action(ref, hostID)`): an unregistered reference, a controller-side action with a host, or an agent action without a host is `action.unknown`. Agent calls go through unexported `(*Dispatcher).agent(ctx, hostID, name)`: `Hosts.Agent` (`not_found` for an unknown host), then `Hosts.usable` of the name's module.
  - `Read(ctx, p, hostID, a, input)`: controller-side → `Engine.Read`; agent → the host's `Read`.
  - `Resource(ctx, p, hostID, name)`: `not_found` (`no resource <name>`) for an unregistered name; then the host's `Resource`.
  - `Plan(ctx, p, hostID, a, input)`: as `Read`, through `Engine.Plan` or the host's `Plan`; a returned view whose `HostID`, `Action` or `Version` differ from the request is a plain error; then the plan id is indexed with `(hostID, a, ExpiresAt)`, and entries whose `ExpiresAt + PlanIndexRetention` has passed are dropped.
  - Unexported: `planEntry{hostID, ref, expiresAt}`, `Dispatcher.plans map[string]planEntry` (guarded by `mu`), `moduleOf(name)`, `(*Dispatcher).remember(planID, e)`. Test helpers in `dispatch_test.go`: `alice`, `(*harness).config(journal core.Journal) Config` (a real controller engine with `schema.NewValidator()`, `schema.Redact`, `Base.Agents = hosts`), `newDispatcher(t)`.
- Task 26 (`apply.go`):
  - `Apply(ctx, p, hostID, a, planID, opts)`: the routing rule; then a plan id that is not indexed for exactly `(hostID, a)` is `plan.not_found` with detail `no plan <id> for <ref>` plus ` on host <h>` for an agent action (register row 69, obligation 1); controller-side → `Engine.Apply`; agent → the module gate, the host's `Apply`, and, whenever the returned view has an id, an `INSERT ... ON CONFLICT (change_id) DO NOTHING` into `core_change_index` (a failure is logged, not returned) followed by `Ingest.IngestHost` (an error is logged at warn level).
  - Unexported: `(*Dispatcher).planFor(planID, hostID, a) error`, `(*Dispatcher).index(ctx, hostID, v)`, `(*Dispatcher).ingest(ctx, hostID)`. Test helpers in `apply_test.go`: `newAudited(t) (*harness, *Dispatcher, *audit.Store)` (controller journal and `Ingest` real), `(*harness).plan(d, hostID, ref, input) core.PlanView`.
- Task 27 (`changes.go`): `const DefaultChangeLimit = 100`, `MaxChangeLimit = 500`; `var _ core.Dispatcher = (*Dispatcher)(nil)`.
  - `Change(ctx, p, id)`: the controller engine's change; on `change.not_found`, the host `core_change_index` names (`change.not_found` without a row) and that host's `Change`; a view whose `HostID` differs from that host is a plain error. The view's `Action`, `Version` and `HostID` are the stored ones (register row 69, obligation 2).
  - `Confirm(ctx, p, id)`, `Revert(ctx, p, id)`: the controller engine first; on `change.not_found`, the owning host's `Confirm`/`Revert`, then `Ingest.IngestHost` on success, then the same host check. No module gate.
  - `Changes(ctx, p, f)`: pages exactly like `lifecycle.Engine.Changes` (controller ruling 2026-10-18 for the API's refill): `f.Limit` 0 or less means 100 and at most 500; every source is asked for the changes with id below `f.Before` matching `f.State`, at most `f.Limit`; the union is sorted by id descending and cut to `f.Limit`. A page shorter than the limit therefore means nothing more matches. Without `f.HostID`: the controller engine's changes and those of every `core_hosts` host (each asked with its own `HostID`); a host whose client is missing or whose listing fails fails the whole call. With `f.HostID`: only that host's agent. Break-glass changes are not filtered here (the API hides them).
  - Unexported: `isChangeNotFound(err)`, `(*Dispatcher).owner(ctx, id) (string, core.AgentClient, error)`, `checkHost(v, hostID)`, `(*Dispatcher).settle(ctx, id, onController, onAgent)`, `(*Dispatcher).hostIDs(ctx)`. Test helpers in `changes_test.go`: `bob`, `(*harness).applyAgent`, `(*harness).applyCtl`, `ids`.

**How the next parts use it** (P14, `internal/controller/server`)

1. Open and migrate the controller database (`store.CoreMigrations(store.SideController)` now includes `0004_audit.sql`), then ensure the local host's `core_hosts` row exists **before** step 6: `core_module_state.host_id` references `core_hosts`, so `Hello` fails its foreign key without it.
2. `auditLog := audit.New(controllerDB, clock)`.
3. `authService := auth.New(controllerDB, controllerSealer, clock, auditLog, auth.Options{})` (plan 3).
4. `reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: authService, Audit: auditLog, Modules: modules.All()}))`. This one registry goes to the runtime, `Hosts`, the controller engine and the dispatcher.
5. `agentDB, err := store.Open(ctx, p.AgentDB())`, then `rt, err := runtime.Open(ctx, runtime.Config{DB: agentDB, Sealer: agentSealer, Clock: clock, Log: log, Registry: reg, HostID: localHostID, BinaryVersion: buildinfo.Version, BackupDir: p.AgentBackups(), Exec: platform.NewRunner(platform.ProductionAllowlist())})` (`FilesTestMode` false). This covers register row 51 for the agent: migrations, the one `platform.Files`, `Base.Clock`, `Recover` once, `Purge` at start and every 10 minutes. An error stops startup.
6. `hosts := dispatch.NewHosts(dispatch.HostsConfig{DB: controllerDB, Registry: reg, Clock: clock, Log: log})`, `hosts.Add(localHostID, rt)`, `err := hosts.Hello(ctx, localHostID)` (seeds the local host's module rows from `DefaultEnabled`; an error stops startup).
7. `engine, err := lifecycle.New(lifecycle.Config{Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: auditLog, Redact: schema.Redact, Base: core.Env{DB: controllerDB, Sealer: controllerSealer, Clock: clock, Log: log, Agents: hosts}})`; `engine.Recover(ctx)` once; `engine.Purge(ctx)` and `authService.Purge(ctx)` at start and every 10 minutes through `clock.AfterFunc` (register row 51, controller half; the runtime runs only the agent's).
8. `ingester := audit.NewIngester(audit.IngesterConfig{Store: auditLog, Agents: hosts, Clock: clock, Log: log})`, then `ingester.IngestAll(ctx)` (log an error; it catches up entries a previous run left), then `ingester.Start()`.
9. `d, err := dispatch.New(dispatch.Config{Registry: reg, Engine: engine, Hosts: hosts, DB: controllerDB, Clock: clock, Log: log, Ingest: ingester})`; `d` is `api.Config.Dispatcher`.
10. Shutdown, in this order: the listeners and the socket server, `ingester.Stop()`, the purge timer, `engine.Close()`, `rt.Close()`, then `agentDB.Close()` and `controllerDB.Close()`.
11. Register row 69 obligations 1-3 are met inside `dispatch` (Tasks 25, 26 and 27); the end-to-end test can check obligation 1 by applying a host plan through the controller route and expecting `plan.not_found`.

**Obligations on later plans**

1. P14: add a client to `Hosts` for every `core_hosts` row before serving. `Dispatcher.Changes` without a host filter fails when a listed host has no client, and `audit.Ingester.IngestAll` reports it on every tick. In M1 only the local host exists.
2. P14: the controller engine's `Recover`, `Purge` loop and `Close` are P14's (step 7); `runtime.Runtime` runs them only for the agent engine.
3. M2 transport: `Hosts` says Hello once per process per host. A reconnecting remote agent must be re-added with `Hosts.Add` (which resets the greeting) so its module availability is written again.
4. Settled by the controller ruling of 2026-10-18 (Assumptions › Part D; see "Obligations between the parts"), kept for the record: a preflight reason is the check's error text and reaches clients in `module.unavailable` details and `core.module.list`; hostinfo's `meminfo` check reason quotes a path (`cannot read /proc/meminfo: ...`), which obligation 3 forbids in a detail.
5. Register row 52's remaining wiring items (the key path from `paths.Paths` only, `[]` for empty diff and guards) are not touched here.

#### Part B — P13: the CLI

Part P13 adds package `internal/cli` only: no migration, no error code, no dependency. P14 (assembly) codes against the exported names; the unexported names are listed because later tasks of this part consume them. Every file is new.

**`internal/cli`** (package `cli`, P13)

- Task 1 (`errors.go`, carries the package doc comment):
  - `const ExitOK = 0`, `ExitError = 1`, `ExitUsage = 2`, `ExitRefused = 3`, `ExitAuth = 4` (spec section 15).
  - `func ExitCode(err error) int`: 0 for nil; 2 when the chain holds a `*usageError`; 3 for a `*refusedError`; 4 for a `*problemError` whose code starts with `auth.`; 1 for any other error marked by `ranError`; 2 for any unmarked error (cobra's flag, argument and unknown-command errors).
  - `func PrintError(w io.Writer, err error)`: writes `darkpanel: <sanitize(err.Error())>\n`, then for a `*problemError` one line `  <path>: <message>\n` per field error, both sanitized; nil writes nothing.
  - Unexported: `usageError`, `usagef(format string, args ...any) error`; `refusedError`, `refusedf(format string, args ...any) error`; `problemError{Status int; Code errcode.Code; Detail string; Fields []errcode.FieldError}` whose `Error()` is `<detail> (<code>)`; `runError`, `ranError(err error) error` (nil stays nil); `markRuns(root *cobra.Command)` wraps the `RunE` of root and every descendant with `ranError`; `sanitize(s string) string` replaces every `unicode.IsControl` rune, U+2028, U+2029, U+200E, U+200F, U+202A-U+202E and U+2066-U+2069 with `\xNN` (two lowercase hex digits) below U+0080 and `\uNNNN` (four lowercase hex digits) otherwise; `isFormatHazard(r rune) bool`.
- Task 15 (`pin.go`): `const requestTimeout = 90 * time.Second`; `newHTTPClient(base *url.URL, fingerprint string) (*http.Client, error)`: refuses a non-empty fingerprint that is not 64 hexadecimal digits (`caFingerprint must be 64 hexadecimal digits, ...`); plain HTTP, or HTTPS without a fingerprint, uses Go's defaults (system roots); HTTPS with a fingerprint uses `pinnedTLS(strings.ToLower(fingerprint), base.Hostname())`. `pinnedTLS(fingerprint, host string) *tls.Config`: TLS 1.2 minimum, `InsecureSkipVerify` with a `VerifyConnection` that finds the presented certificate whose SHA-256 equals the fingerprint (`no certificate the server presented matches caFingerprint <fp>`), requires it to be a CA (`... is not a CA`), and verifies the leaf against it alone for `host`. Test helpers in `pin_test.go`: `testCert{cert, key}` with `fingerprint() string`, `newCert`, `newTestCA(t)`, `newTestLeaf(t, ca, ip)`, `serveTLS(t, leaf, ca) *url.URL`, `get(t, u, fingerprint) error`.
- Task 16 (`config.go`, `client.go`):
  - `const EnvURL = "DARKPANEL_URL"`, `EnvToken = "DARKPANEL_TOKEN"`, `DefaultURL = "http://127.0.0.1:8080"`.
  - Unexported `fileConfig{URL, Token, CAFingerprint}` (JSON `url`, `token`, `caFingerprint`; unknown fields refused); `configPath(getenv, home) (string, error)` (`$XDG_CONFIG_HOME/darkpanel/cli.json`, `~/.config` when unset or relative); `maxConfigBytes = 1 << 20`; `loadConfig(path string) (fileConfig, error)` (absent file → zero value; refused with `the config file <path> <why>; run chmod 600 <path>` when it is a symbolic link, not a regular file, has any group or other permission bit, or is not owned by the effective uid, all checked on the open descriptor); `connectNetwork(flagURL string, getenv func(string) string, home func() (string, error)) (*client, error)` (URL: flag, `DARKPANEL_URL`, config, `DefaultURL`; token: `DARKPANEL_TOKEN`, config; fingerprint: config; a URL that is not `http(s)://<host>` is a usage error); `firstOf(values ...string) string`.
  - Unexported `client{base, token string; http *http.Client}`; `newSocketClient(path string) *client` (Unix dialer, base `http://controller`, no token; a dial error names the path); `(*client).do(ctx context.Context, method, path string, body, out any) error` (JSON body with `Content-Type: application/json` only when `body != nil`; `Accept: application/json`; bearer token when set; 2xx decoded into `out` unless nil; `application/problem+json` with a code → `*problemError`; any other non-2xx → `<METHOD> <path>: unexpected answer <status>`); `answerError`; `maxResponseBytes = 16 << 20`.
  - Test helpers in `client_test.go`, used by every later task: `exchange{method, path, body string; status int; reply string; problem bool}` (path includes the query; `body` "" expects no body; `status` 0 answers 200); `newFakeAPI(t, token string, want ...exchange) string` (base URL; requests must arrive in order with `Authorization: Bearer <token>`, or none when token is "", and the right content type; cleanup fails the test on unserved exchanges); `newFakeSocket(t, want ...exchange) string` (socket path; no token); `fakeAPI`; `jsonEqual(a, b []byte) bool`; `writeConfig(t, home, content, mode) string`; `envOf(map[string]string) func(string) string`; `homeAt(dir) func() (string, error)`.
- Task 8 (`names.go`): `func CommandPath(name string) []string`: the words after `darkpanel` for an action or resource name (`demo.item.get` → `[demo item get]`, `core.host.list` → `[host list]`, `demo.rule` → `[demo rule get]`). Unexported `handWritten = []string{"serve", "version", "change", "controller", "help", "completion"}`; `entry{Name string; Path []string; Action *core.ActionDef; Resource *core.ResourceDef}`; `entries(reg *core.Registry, reserved []string) ([]entry, error)` (one entry per action name at its highest version and per resource, sorted by path); `checkEntries(es []entry, reserved []string) error` (errors: `<name>: command "darkpanel <path>" clashes with the hand-written command "<word>"`, `<a> and <b> both map to "darkpanel <path>"`, `"darkpanel <path>" is the command of <a> and is also a command group of <b>`, joined). Test fixture in `fixture_test.go`: `fixtureHandler[In, Out]` (every handler method, never run), module `demoModule` (`demo.item.get` v1 and v2 read, `demo.item.list` read, `demo.item.add` compensable by `demo.item.remove` with secret inputs `apiKey`, `passphrase` and secret output `token`, `demo.item.remove` irreversible, `demo.rule.set` revertible with a required nested `limits`, resources `demo.rule` and `demo.status`; all agent-side) and its types `demoNoInput`, `demoItemQuery`, `demoItem`, `demoItems`, `demoAddInput`, `demoAdded`, `demoIDInput`, `demoLimits`, `demoRuleInput`, `demoRule`, `demoStatus`; `testRegistry(t) *core.Registry` (`coreactions.Module(coreactions.Deps{})` plus `demoModule`). `names_test.go` adds `fakeCore` (a core module of read actions by name).
- Task 17 (`input.go`):
  - `terminal{in *bufio.Reader; out io.Writer; isTerm bool; readSecret func() (string, error)}`; `(*terminal).line(prompt string) (string, error)` (prompt only on a terminal; `io.EOF` when the input ended before any character); `(*terminal).secret(prompt string) (string, error)` (on a terminal: prompt, `readSecret`, newline; else `line("")`).
  - `reservedFlags` (`url`, `socket`, `dev`, `help`, `json`, `host`, `input`, `yes`, `confirmation`, `confirm`); `inputField{Name, Flag, Description, Type, Items string; Enum []string; Required, Secret bool}` (`Type` "" means nested); `propSchema`; `scalarTypes`; `inputFields(t reflect.Type) ([]inputField, error)` (schema order; errors for a non-string secret, a flag in `reservedFlags`, two fields with one flag); `kebab(name string) string`; `addInputFlags(cmd *cobra.Command, fields []inputField)` (typed flags; `--input` when there is any field); `flagValue(cmd, f) (any, error)`; `collectInput(cmd *cobra.Command, fields []inputField, term *terminal) (json.RawMessage, error)` (the `--input` object, then set flags, then defaults and usage errors for missing required fields, then missing secrets in schema order; usage errors `required flag --<flag> not set`, `required input field "<name>" missing; give it in --input`, `no value for <name>: ...`, `--input <path>: ...`); `readInputDoc`; `member{Key string; Value json.RawMessage}`; `members(raw json.RawMessage) ([]member, error)` (object members in document order; empty input has none).
- Task 18 (`output.go`): `render(w io.Writer, raw json.RawMessage) error` (rules in the task text; every string sanitized; invalid JSON is an error); `writeTable(w io.Writer, header []string, rows [][]string) error` (sanitized, two-space columns); `cellText(raw json.RawMessage) string` (scalar text, `-` for null, comma-joined scalar list, else compact JSON); `scalarText`, `compact`, `flatten`, `isList`, `objectRows`, `renderList`, `writeLines`; `secretOutputs(t reflect.Type) ([]string, error)` (top-level `x-secret` properties of `schema.Output(t)` in order); `noteSecrets(w io.Writer, names []string, raw json.RawMessage)` (`\nThe <a> above will not be shown again; store it now.\n`, or `The <a> and <b> ... store them now.`, for the named fields present and non-null).
- Task 20 (`root.go`, `read.go`):
  - `const AnnotationAction = "darkpanel.action"`, `AnnotationResource = "darkpanel.resource"`: set on every generated command to the action or resource name.
  - `type Options struct { Registry *core.Registry; Commands []*cobra.Command; Stdin io.Reader; Stdout, Stderr io.Writer; Getenv func(string) string; UserHomeDir func() (string, error); IsTerminal func() bool; ReadSecret func() (string, error) }`: `Registry` is required; `Commands` are the assembly's hand-written commands (their names join the reserved words); nil fields mean `os.Stdin`, `os.Stdout`, `os.Stderr`, `os.Getenv`, `os.UserHomeDir`, `term.IsTerminal` on standard input, and `term.ReadPassword` on standard input.
  - `func NewRoot(opts Options) (*cobra.Command, error)`: root `darkpanel` (`SilenceUsage`, `SilenceErrors`), `opts.Commands`, one command per entry under its noun groups, then (Tasks 24, 28 and 29) `change`, `controller` and the enrollment wrappers, then `markRuns`. Errors: `cli: Options.Registry is required`, `cli: <clash>` from `entries`, `cli: <input schema error>`.
  - `func FindCommand(root *cobra.Command, name string) (*cobra.Command, bool)`: the command at `CommandPath(name)` whose annotation names `name`.
  - Unexported: `app{opts Options; term *terminal}`; `withDefaults`; `group(root, path) *cobra.Command` (creates noun groups; an unknown subcommand is a usage error, no argument prints help); `(*app).actionCommand(e entry) (*cobra.Command, error)`; `addConnFlags(cmd, breakGlass, agent bool)` (`--socket` and `--dev` for break-glass, else `--url`; `--host` for agent-side); `(*app).connect(cmd) (*client, error)` (socket from `--socket`, else `paths.Resolve(--dev).ControllerSocket`; else `connectNetwork`); `actionPath(def core.ActionDef, hostID, op string) string` (`/api/v1/hosts/<host>/actions/<name>/v<n>/<op>` or `/api/v1/actions/<name>/v<n>/<op>`); `hostRef{ID, Name}`; `resolveHost(ctx, c *client, want string) (string, error)`; `hostFor(cmd, c, side) (string, error)`; `(*app).runRead(cmd, def, fields, secrets) error`; `(*app).resourceCommand(e entry) (*cobra.Command, error)`; `(*app).show(cmd, raw, secrets) error`.
  - Test helpers in `root_test.go`: `cliEnv{reg *core.Registry; env map[string]string; stdin string; terminal bool; secrets []string; commands []*cobra.Command}` with `run(t, args...) (stdout, stderr string, code int)` (nil `reg` is `testRegistry`; prints the error with `PrintError` into stderr); `hostList(hosts ...string) exchange` (`"<id>:<name>"`); `testToken`; `tokenEnv`.
- Task 22 (`mutate.go`; `root.go` gains the mutating branch of `actionCommand`): `planWire`, `changeWire` (the API's Plan and Change fields the CLI reads); `addMutatingFlags(cmd, class core.Class)` (`--yes`; `--confirmation` on irreversible; `--confirm` on revertible); `(*app).runMutating(cmd, def, fields, secrets) error`; `(*app).planAndApply(cmd *cobra.Command, c *client, def core.ActionDef, host string, input any, agreed bool) (changeWire, json.RawMessage, error)` (plan → print → guards → `Apply? [y/N]` unless `--yes` or `agreed` → phrase → apply with `confirmNow` when `--confirm`); `(*app).confirmation(cmd, want) (string, error)`; `printPlan(w, planWire)`; `printChange(w, changeWire, secrets) error`; `changeOutcome(changeWire) error` (`change <id> <state>: <message> (<code>)` for failed, reverted and interrupted). Test helpers `planJSON(planID, action, host, confirmation, guards string) string`, `changeJSON(id, action, class, state, extra string) string`, `noExtra`.
- Task 24 (`change.go`; `root.go` adds `change`): `changeColumns`; `(*app).changeCommand()`, `changeListCommand()`, `changeShowCommand()`, `changeOpCommand(op, want, short string)`; `printChanges(cmd, raw) error`.
- Task 28 (`controller.go`; `root.go` adds `controller`): `(*app).controllerCommand()`, `(*app).runWebCA(cmd, args) error` (prints `<PEM>SHA-256 fingerprint: <lowercase hex>\n`).
- Task 29 (`enroll.go`; `root.go` calls `wrapEnrollment`): `actionUserCreate`, `actionUserResetTOTP`, `actionUserActivate`; `latestAction(reg, name) (core.ActionDef, bool)`; `(*app).wrapEnrollment(root)`; `(*app).runEnrollment(cmd, def, activate core.ActionDef) error`; `(*app).newPassword() (string, error)`; `enrollmentURI(username, secret string) string`. Test constants `testTOTPSecret`, `testUserPassword`; helpers `enrollmentChange`, `activation`.

**How the next parts use it**

- P14 rewires `cmd/darkpanel/main.go` (it keeps `serve` and `version` as its own commands and drops `newRootCmd`):

  ```go
  reg, err := registry.Build()
  if err != nil {
  	fmt.Fprintln(os.Stderr, "darkpanel:", err)
  	os.Exit(cli.ExitError)
  }
  root, err := cli.NewRoot(cli.Options{Registry: reg, Commands: []*cobra.Command{newServeCmd(), newVersionCmd()}})
  if err != nil {
  	fmt.Fprintln(os.Stderr, "darkpanel:", err)
  	os.Exit(cli.ExitError)
  }
  if err := root.Execute(); err != nil {
  	cli.PrintError(os.Stderr, err)
  	os.Exit(cli.ExitCode(err))
  }
  ```

  `NewRoot` marks the `RunE` of `serve` and `version` too, so their errors exit 1 and their flag errors 2 (register row 30). `serve --dev` is `serve`'s own flag; the generated break-glass commands carry their own local `--dev`.
- P14's CLI parity test (register row 3): build `root` as above with `registry.Build()` and, for every `def` in `reg.Actions()` (and every resource of `reg.Resources()`), require `cli.FindCommand(root, def.Spec.Name)` (or the resource name) to return `ok`. `cli.CommandPath(name)` gives the expected words; the enrollment commands `user create` and `user reset-totp` are found like any other.
- P14's end-to-end test (register rows 3, 11, 13) drives `NewRoot` in-process with `Options{Stdin, Stdout, Stderr, Getenv, UserHomeDir: <t.TempDir()>, IsTerminal: func() bool { return false }}` against the assembled server:
  1. First admin over the socket: `user create --username admin --socket <path> --yes` with standard input `<password>\n` and then end of input. The user is created and the command ends with exit 2 and `no code given; activate the user with: darkpanel user activate --username admin`; read the `Secret: <base32>` line from standard output, compute the code with `auth.TOTPCode`, and run `user activate --username admin --yes --socket <path>` with standard input `<code>\n`. (Alternatively, feed standard input through an `io.Pipe` and write the code after the `Secret:` line appears.)
  2. The CLI has no login command (spec section 15 defines none), so the test logs in with `POST /api/v1/auth/login`, creates a token through the API with the session and CSRF header, and then runs the CLI with `Getenv` returning `DARKPANEL_URL=<server URL>` and `DARKPANEL_TOKEN=<token>`.
  3. With the token: `hostinfo summary get` reads the host-information resource (exit 0, `key: value` lines), `token list`, `audit list`, and a refused call without a token exits 4.
- `controller web-ca --socket <path>` prints the root PEM and `SHA-256 fingerprint: <hex>`; `{"caFingerprint": "<hex>"}` in the 0600 config file pins it.

**Obligations on later plans**

1. P14 (assembly): `cmd/darkpanel/main.go` exits with `cli.ExitCode` and prints with `cli.PrintError` as above; until it does, register row 30 stays open.
2. P14 (assembly): the CLI parity test and the end-to-end CLI run with a token, as above (register rows 3 and 13).
3. P14 (assembly): `internal/cli` now imports `github.com/spf13/cobra`, `golang.org/x/term` and `github.com/mdp/qrterminal/v3`, so their lines in `internal/deps/deps.go` are no longer needed to keep `go.mod` stable when plan 4 removes that file.
4. Later plans: a new core noun or a new hand-written top-level command must not clash with a generated one: `NewRoot` fails, and with it every CLI invocation, so the registry tests of the plan that adds it must build `cli.NewRoot` once.
5. Later plans: an input field must not be named so that its flag is one of `url`, `socket`, `dev`, `help`, `json`, `host`, `input`, `yes`, `confirmation`, `confirm` (for example a field `host`); `NewRoot` refuses it. A secret input field must be a string.

#### Part C — plan 3 follow-ups: API, identity and core actions

The names below extend plan 3's Contracts (`docs/superpowers/plans/2026-10-17-m1-identity-and-api.md`); where this part changes a behaviour plan 3 described, the text here replaces it.

**`internal/controller/coreactions`** (tests only)

- Task 2 (`tokens_test.go`): `noSecret(t *testing.T, secrets []string, values ...any)` keeps its signature and callers. It fails with `t.Fatal(secretLeak(...))` when the message is not empty. `secretLeak(secrets []string, values ...any) string` returns `""` when no rendering of any value contains a non-empty secret. Otherwise it names the first leak as `value <i> (<%T of the value>) rendered as <%+v | Error() | JSON> contains secret <j>`. Renderings are checked in that order: `%+v`, then `Error()` for an error, then JSON (of the `*errcode.Error` in an error's chain, else of the value). Test type `hidden` (a `Stringer` that hides its value).

**`internal/controller/auth`** (package `auth`)

- Task 6 (`service.go`): `(*Service) ResetPassword(ctx context.Context, username, password string) (User, error)` keeps its signature. Besides storing the hash and ending the sessions it runs `UPDATE core_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL` in the same transaction, with the time also written to `core_users.updated_at`. A revoked token is then `auth.required` in `Authenticate`. Its refusals are unchanged: `/password` for an invalid password, `not_found` for an unknown user.
- Task 30 (`login.go`): `(*Service) Login(ctx, LoginRequest) (LoginResult, error)` keeps its signature. A fault after the password verified is counted in the limiter (`limiter.fail(username, source)`) and audited through `recordLogin(ctx, r, userID, errcode.Internal)`; its error is ignored, and the fault's own error is returned. Such a fault is a database or sealer error inside the session transaction, or a failure of the success audit row. Unexported signatures change:
  - `(*Service).evaluate(ctx, r LoginRequest) (LoginResult, string, bool, error)`;
  - `(*Service).attempt(ctx, r LoginRequest) (LoginResult, string, bool, error)`; the `bool` is `verified`, true once `verifyHeld` accepted the password, including when the attempt then ends in `auth.invalid_credentials`.
  - Test helpers in `login_test.go`: `errSealerDown`, `errAuditDown`, `openFails` (a `coretest.Sealer` whose `Open` fails), `failingAudit{*coretest.MemAudit; fail int}` (fails its first `fail` `Record` calls) and `failures(s, key) int`.

**`internal/controller/coreactions`**

- Task 9 (`users.go`): `core.user.reset-password/v1` plans the diff `- user <u>: current password` and `+ user <u>: new password; its sessions end and its API tokens are revoked`. `Guards` is `[]`, `Confirmation` is the username and `StateHash` is still `stateHash(u.ID)`. Summary: `Set a user's password, end its sessions and revoke its API tokens; the confirmation phrase is the username.` (the schema snapshots do not carry summaries, and OpenAPI skips break-glass actions).

**`internal/controller/api`** (package `api`)

- Task 31 (`middleware.go`): `recoverPanics(log, next)` calls `w.Header().Del("Set-Cookie")` before `writeProblem(w, log, errcode.New(errcode.Internal, ""))`. When the response has already started it still re-panics `http.ErrAbortHandler`.
- Task 3 (`.golangci.yml`): linter `forbidigo` enabled, with settings `analyze-types: true` and `forbid: [{pattern: ^http\.Error$, pkg: ^net/http$, msg: answer refusals with writeProblem}]`. `linters.exclusions.rules` has `{path-except: ^internal/controller/api/, linters: [forbidigo]}`. Report text: ``use of `http.Error` forbidden because "answer refusals with writeProblem" (forbidigo)``.
- Task 32 (`actions.go`):
  - `const defaultChangeLimit = 100` (the engine's default) next to `maxChangeLimit = 500`; `const maxExtraChangeFetches = 4`.
  - `(*Server).networkVisible(v core.ChangeView) bool` is `!def.Spec.BreakGlass` when the registry has `v`'s action and version, else `v.HostID != ""`.
  - `handleChanges` fetches pages from `Dispatcher.Changes`:
    - It always passes an explicit `Limit` (the query's, or `defaultChangeLimit`) and keeps only visible changes, stopping at that limit.
    - While the page is short and the last fetch returned as many changes as it asked for, it fetches again with `Before` = the last fetched id and `Limit: maxChangeLimit`, keeping `State` and `HostID`, at most `maxExtraChangeFetches` times.
    - The answer is `{"changes":[...]}` as before.
  - `errHiddenChange(id string) error` is `errcode.New(errcode.ChangeNotFound, "no change <id>")`, the engine's wording for a missing change.
  - `handleChange` and `changeOp` (confirm, revert) answer a change that is not `networkVisible` with `errHiddenChange` of the route's id; `changeOp` checks this after `Dispatcher.Change` and before `authorizeChange`, so `Confirm` and `Revert` are never called for it.
  - `(*Server).authorizeChange(p, v)` now only authorizes `RequiredScope` of the change's registered class and side, or of its stored class on the agent side when its action is unregistered (such a change is agent-side, since hidden ones never reach it).
  - Tests: `TestChangeListHidesBreakGlass`, `TestChangeShowHidesBreakGlass`, `TestChangeOpsHideBreakGlass`; `TestChangeOpsAuthorizeTheChangesAction` expects 404 `change.not_found` for `cbg` and `cunk`.
  - Test helpers in `actions_test.go`: `changesOf(kinds string) []core.ChangeView`, where `v`, `a`, `b` and `u` are a visible agent change, an unregistered agent change, a break-glass change and an unregistered controller change, with ids `c00001`… newest first. Also `changeStore(views, *[]core.ChangeFilter)`, a fake `Changes` paging like the engine, and `listedIDs(t, rec) []string`.
- Task 33 (`server.go`):
  - ``type ListenerState struct { TailscaleBound bool `json:"tailscaleBound"`; Message string `json:"message"` }``.
  - `func (s *Server) SetListenerState(state func() ListenerState)`: stores the source atomically; nil clears it.
  - Unexported: the field `listeners atomic.Pointer[func() ListenerState]`, `const listenersNotStarted = "The listeners are not started yet."`, and `(*Server).listenerState() ListenerState`, which calls the source, or answers `{false, listenersNotStarted}` when none is set.
- Task 34 (`meta.go`, `routes.go`):
  - Route `GET /api/v1/controller/listeners`, summary `Listener state for the UI footer: Tailscale bound, and a message`, not public, read scope, answering the `ListenerState` JSON. Unexported handler `(*Server).handleListeners`.
  - The final `Routes()` is plan 3's nine routes followed by `GET /api/v1/controller/listeners`.
  - The route is network-only: `SocketHandler` does not serve it.

**How the next parts use it**

- P14 (assembly) wires the listener state right after the listener service starts:

  ```go
  srv, err := api.New(api.Config{ /* as plan 3 says */ })
  // ...
  svc, err := listen.Start(ctx, listen.Config{ /* ... */ Handler: srv.Handler() /* ... */ })
  if err != nil { /* ... */ }
  srv.SetListenerState(func() api.ListenerState {
  	st := svc.State()
  	return api.ListenerState{TailscaleBound: st.TailscaleBound, Message: st.Message}
  })
  ```

  `svc.State()` is a lock-free copy, so the closure is cheap per request. Keep the source set after `svc.Shutdown`, so the footer then reads `The listeners are shut down.`. Its assembly test's `api.CheckOpenAPI(reg)` and deny-by-default matrix cover the new route without extra code. If P14 commits an OpenAPI snapshot, the snapshot includes `GET /api/v1/controller/listeners`.
- The web UI footer (spec section 13) reads `GET /api/v1/controller/listeners` → `{tailscaleBound: boolean, message: string}` with the session cookie; no CSRF header is needed for a GET.
- P11 (dispatch): see obligation 1 below. P13 (CLI): nothing changes on the socket.

**Obligations on later plans**

1. P11 dispatch: `Dispatcher.Changes(ctx, p, f)` must page like the lifecycle engine. That means newest first by id, only ids below `f.Before` when it is set, and at most `f.Limit` changes. It returns fewer than `f.Limit` only when no more changes match. The API now always passes a `Limit` from 1 to 500 and never 0, and it detects the end of the list by a short fetch.
2. P14 assembly: call `srv.SetListenerState` with a closure over `listen.Service.State()` after `listen.Start` succeeds (above). Until then, and if it is never called, the footer reads `The listeners are not started yet.`.

#### Part D — P14: assembly

The names of `main` at `f28be88` and of parts A (dispatch, agent runtime, audit), B (CLI) and C (API, identity and core-action follow-ups) are as their part plans state; where a document and the code differ, the code wins. Part P14 adds the following.

**`internal/modules`** (Tasks 35 and 40)

- Task 35 (`modules.go`): `func All() []core.Module` returns `[]core.Module{hostinfo.Module()}`: the one registration line of R5. `schemas/<action>/v<n>.json` and `schemas/hostinfo.summary/v1.json` are generated by `make schemas` (15 files at this commit: the fourteen core actions and `hostinfo.summary`); `schemas/released/.gitkeep` keeps the released-snapshot directory. Test helpers in `modules_test.go` (package `modules_test`), used by Task 40: `snapshotDir` (`../../schemas`), `releasedDir`, `built(t) *core.Registry` (`registry.Build()`), `snapshots(t, reg) map[string][]byte` (`schema.Snapshots`).
- Task 40 (`registry_test.go`): tests only. Unexported test names: `moduleIDRule`, `actionNameRule`, `resourceRule`, `reservedCLI` (the spec's rules, written out); `nullableProblems(t, owner string, typ reflect.Type) []string`, which walks an output type with its published schema and returns one line per pointer field whose property does not accept `null` (`<owner>: output field <path> is a pointer but its schema does not accept null; tag it jsonschema:"nullable"`, paths like `tokens[].lastUsedAt`); `nullWalker` with `resolve`, `branches`, `acceptsNull`, `walk`, `fields`.

**`internal/controller/server`** (package `server`, Tasks 37, 38, 39 and 42; every file is new)

- Task 37 (`server.go`, carries the package doc comment):
  - `const LockName = "controller.lock"`, `BackupsKept = 5`, `ShutdownTimeout = 30 * time.Second`; unexported `timeFormat` (`2006-01-02T15:04:05.000000000Z`).
  - `type Options struct { Paths paths.Paths; Listen []listen.Spec; Dev bool; Clock core.Clock; Log *slog.Logger; Hostname func() (string, error); Bind listen.BindFunc; Interfaces listen.InterfaceAddrs; AuthParams auth.Params }`: `Paths` is required (`server: Options.Paths is required`); nil `Listen` means `listen.ParseSpecs(nil)` (loopback and tailscale); nil `Clock` means `core.SystemClock{}`; nil `Log` discards; nil `Hostname` means `os.Hostname`; nil `Bind` and `Interfaces` mean listen's defaults; zero `AuthParams` means `auth.DefaultParams`. `Dev` is passed to `api.Config.Dev`.
  - `type Server struct`; `func Start(ctx context.Context, opts Options) (*Server, error)`: runs the steps of the part constraint; on an error it runs `Shutdown` with a `ShutdownTimeout` context detached from `ctx` and returns the step's error joined with any shutdown error.
  - `(*Server) LocalHostID() string`, `(*Server) SocketPath() string` (`opts.Paths.ControllerSocket`).
  - `(*Server) Shutdown(ctx context.Context) error`: the reverse stops, errors joined as `server: stop <name>: <err>`; idempotent and serialised (part constraint).
  - `openStorage`, in order: `Paths.MkdirAll()` (0700); `checkPrivateDir(<socket dir>)`, which refuses a non-directory, a group- or other-writable directory (`server: the controller socket directory <dir> is writable by group or other; run chmod 700 <dir>`) and one owned by another uid (`... is owned by uid <n>, not by this process's uid <m>`); `secret.LoadOrCreateKey` + `secret.NewSealer` for `ControllerKey` then `AgentKey`; the lock (`os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW`, 0600, `unix.Flock(LOCK_EX|LOCK_NB)`; held: `server: another darkpanel serve holds <path>; stop it first`); `store.Open` of `ControllerDB()` then `AgentDB()`; `store.Migrate` of the controller database (side controller, `buildinfo.Version`, backups under `ControllerBackups()`, keep 5, the clock's now; core set, then each `modules.All()` module's `Migrations[core.SideController]`; a core failure stops startup as `server: controller migrations: ...`, a module failure is logged at error level as `server: module controller migrations failed` and startup continues); `ensureLocalHost`.
  - `ensureLocalHost`: in one write transaction, reads `core_meta.local_host_id`; when absent inserts `core_hosts (id, name, is_local 1, created_at)` with a ULID id from the clock and `core_meta ('local_host_id', id)`. The name is the hostname, or `localhost` (with a warning) when the lookup fails or is empty; the leaf's hostname is then `""`. A later start reads the id and changes nothing.
  - Unexported: `closer{name; close func(ctx) error}`, `(*Server).onShutdown(name string, stop func(ctx context.Context) error)`, `openStorage`, `checkPrivateDir(dir) error`, `loadSealer(path) (*secret.Sealer, error)`, `(*Server).lock(path) error`, `(*Server).openDB(ctx, name, path) (*store.DB, error)`, `migrateController`, `ensureLocalHost`; fields `opts`, `clock`, `log`, `hostname`, `controllerDB`, `agentDB`, `controllerSealer`, `agentSealer`, `localHostID`, `mu`, `closers`, `shutdown`, `err`.
  - Test helpers in `server_test.go`, used by Tasks 38, 39 and 42: `t0` (2026-10-18T12:00:00Z), `testHostname` (`test-host`), `testParams`, `localBind(ctx, listen.Endpoint) (net.Listener, error)`, `testOptions(t) Options` (temporary paths, socket in its own `os.MkdirTemp("", "dp")` directory, `Listen` = loopback, `Dev` true, a `FakeClock` at `t0`, `localBind`, `testParams`), `start(t, opts) *Server` (shuts down at cleanup), `openDB(t, path)`, `wantMode(t, path, mode)`.
- Task 38 (`services.go`; `server.go` gains the embedded `services` field and the `startServices` step):
  - `const PurgeInterval = 10 * time.Minute`.
  - `(*Server) Registry() *core.Registry`: the server's registry.
  - `startServices`, in Part A's order (Part A, "How the next parts use it", steps 2-9): `audit.New`; `auth.New(controllerDB, controllerSealer, clock, auditLog, auth.Options{Params: AuthParams})`; `registry.BuildWith(...)` (`server: registry: ...`); `runtime.Open` (agent database, agent sealer, `HostID` the local host, `buildinfo.Version`, `AgentBackups()`, `platform.NewRunner(platform.ProductionAllowlist())`, `FilesTestMode` false) with stop `agent runtime`; `dispatch.NewHosts`, `Add(localHostID, rt)`, `Hello` (`server: hello from the local agent: ...`); `lifecycle.New` on the controller side (`schema.NewValidator()`, `Journal` the audit store, `Redact: schema.Redact`, `Base` = controller database and sealer, clock, log, `Agents` = the `Hosts`) with stop `controller engine`; `Recover` once (`server: recover: ...`); `startPurges`; `audit.NewIngester`, `IngestAll` (an error is logged at warn level), `Start`, stop `audit ingester`; `dispatch.New` with `Ingest` the ingester.
  - `startPurges`: `purge` now (an error stops startup as `server: purge: ...`), then every `PurgeInterval` through `Clock.AfterFunc` (`purgeTick` logs an error at warn level and re-arms); stop `purges` waits for a running purge and disarms the timer. `purge` is `errors.Join(engine.Purge, authService.Purge)`.
  - Unexported: `type services struct { auditLog *audit.Store; authService *auth.Service; registry *core.Registry; runtime *runtime.Runtime; hosts *dispatch.Hosts; engine *lifecycle.Engine; ingester *audit.Ingester; dispatcher *dispatch.Dispatcher; purgeMu sync.Mutex; purgeTimer core.Timer; purgeClosed bool }`, `startServices`, `purge`, `startPurges`, `purgeTick`. Test helpers in `services_test.go`, used by Task 42: `root(hostID) core.Principal` (the socket principal for uid 0), `refDisable`, `count(t, s, query, args...)`, `planDisable(t, s) core.PlanView`.
- Task 39 (`listeners.go`; `server.go` gains the embedded `listeners` field and the `startListeners` step):
  - `(*Server) Listeners() []listen.Bound`: the bound network listeners (`listen.Service.State().Listeners`).
  - `func Run(ctx context.Context, opts Options) error`: `Start`, wait for `ctx` to end, then `Shutdown` within `ShutdownTimeout` (a context detached from `ctx`). `serve` calls it.
  - `startListeners`: `listen.LoadOrCreateAuthority`; `api.New(api.Config{Dispatcher, Auth, Registry, WebCA: the authority, SchemaVersion: store.SchemaVersion(ctx, controllerDB, store.SideController), HostID, Dev, UI: api.EmbeddedUI(), Log})`; `listen.Start(listen.Config{Specs, Hostname, DB, Authority, Clock, Handler: srv.Handler(), Log, Interfaces, Bind})` with stop `listeners`; `srv.SetListenerState` over `svc.State()` (kept after shutdown); `api.ListenSocket(SocketPath)`, `srv.SocketServer()`, `Serve(srv.SocketListener(ln, nil))` in a goroutine, stop `controller socket` (`http.Server.Shutdown`, then waits for `Serve` to return; an error other than `http.ErrServerClosed` is joined). It logs `server: serving` with `message`, `listeners` (URLs) and `socket`.
  - Shutdown order that results: controller socket, listeners, audit ingester, purges, controller engine, agent runtime, agent database, controller database, lock.
  - Unexported: `type listeners struct { apiServer *api.Server; listenService *listen.Service }`, `startListeners`. Test helpers in `listeners_test.go`, used by Task 42: `baseURL(t, s) string` (the IPv4 listener's `http://` URL), `socketClient(path) *http.Client`, `get(t, client, url, token) (int, string)`, `readToken(t, s, clock) string`.
- Task 42 (`e2e_test.go`): tests only. `adminPassword`, `e2e`, `reply` with `problemCode()`, `credentials`, `(*e2e).do`, `admin`, `want`, `wantProblem`, `change`, `apply`, `cli`, `secretLine`, `denyByDefault`, `checkAudit`.

**`cmd/darkpanel`** (package `main`, Task 41)

- `var serveRun = server.Run`, `var geteuid = os.Geteuid` (tests replace both).
- `newRoot(opts cli.Options) (*cobra.Command, error)`: `registry.Build()`, then `cli.NewRoot` with `Commands: [serve, version]`. `newRootCmd` is removed.
- `run(args []string, opts cli.Options) int`: builds the tree (an error prints through `cli.PrintError` and returns 1), runs it with `ExecuteContext(context.Background())`, prints an error with `cli.PrintError` and returns `cli.ExitCode(err)`. `main` is `os.Exit(run(os.Args[1:], cli.Options{}))`.
- `newServeCmd()`: flags `--dev`, `--listen` (repeatable). `PreRunE`: `listen.ParseSpecs` of the `--listen` values, then without `--dev` and with `geteuid() != 0` the error `serve runs as root in production mode; run it with sudo, or add --dev to use per-user development paths` (both exit 2). `RunE`: `paths.Resolve(dev)`, `signal.NotifyContext(SIGINT, SIGTERM)`, `serveRun(ctx, server.Options{Paths, Listen: specs, Dev, Log: slog text handler on standard error})`.
- `newVersionCmd`, `versionInfo`, `currentVersion`, `uiEmbedded`, `writeVersion` are unchanged.
- Test helpers: `runRoot(t, args...)` (now through `run`), `fakeServe(t, euid int, result error) *[]server.Options`, `runArgs(args...) (string, int)`.

**How the next parts use it**

P14 is the last part of plan 4. Plans 5 (web UI) and 6 (harness) run the assembled binary:

1. A development server is `go run ./cmd/darkpanel serve --dev` (state under `$XDG_STATE_HOME/darkpanel-dev/`, socket under `$XDG_RUNTIME_DIR/darkpanel-dev/`, the caller's own uid accepted on the socket); production is `sudo darkpanel serve`. `--listen` values replace the defaults.
2. The first admin without a terminal: `printf '%s\n' "$PASSWORD" | darkpanel user create --username admin --yes` (exits 2 after printing `Secret: <base32>`), then `printf '%s\n' "$CODE" | darkpanel user activate --username admin --yes`, with `--dev` or `--socket <path>` as needed and a code computed from the secret. On a terminal, `user create` asks for both itself.
3. A browser or client logs in with `POST /api/v1/auth/login`, then creates an API token with `core.token.create` (plan and apply with the `X-CSRF-Token` header); the CLI takes it in `DARKPANEL_TOKEN`. The CLI has no login command.
4. Tests of later parts that need a running server in Go use `server.Start` with `Options` as `testOptions` builds them (temporary `paths.Under`, a short socket directory, `Bind` mapping endpoints to `127.0.0.1:0`, `coretest.FakeClock`, cheap `AuthParams`), and read the URL from `Listeners()`.

**Obligations on later plans**

1. Plan 5 (web UI): register row 89 (split from row 9), the manual Playwright check against `serve --dev` (log in with TOTP, the host-information page, token create and revoke, the audit log, no CSP violation). The web UI's Tokens page is how an operator gets the first API token without `curl`.
2. The first plan that registers a module with controller-side migrations (register row 88): a failed controller migration is only logged (Task 37), and the dispatcher does not gate controller-side actions by module, so that plan adds the gate (R9) before such a module ships.
3. M2 (fleet): `Hosts.Add` a client for every `core_hosts` row before serving (Part A obligation 1); write `core_meta.controller_id` when agent enrollment first needs it (nothing reads it in M1, so Task 37 does not write it).
4. M2 install epic: `serve` still creates its directories and keys and, in production mode, requires root (`cmd/darkpanel`'s `PreRunE`); the epic that splits the `darkpanel` and root users moves both.
5. Register row 19: ADR 0010 becomes Accepted when the epic closes; Task 21 corrected its wording while it is Proposed.

#### Obligations between the parts

The parts' obligation lists above were written before the parts were joined. Those that fall inside this plan are discharged as follows; the rest stand for later plans.

- Part A obligation 1 (a `Hosts` client for every `core_hosts` row): Task 38 adds the local host, the only host in M1; remote hosts are Part D obligation 3 (M2).
- Part A obligation 2 (the controller engine's `Recover`, purge loop and `Close`): Task 38.
- Part A obligation 4 (hostinfo's preflight reason quotes a path): settled by the controller ruling of 2026-10-18 in Assumptions › Part D: a preflight reason is operator-facing text the module writes; register row 69's obligation 3 covers core, dispatcher and engine errors, and hostinfo is unchanged.
- Part A obligation 5 (register row 52's remaining wiring): key paths come only from `paths.Paths` in Task 37 (Part D constraint, row 52); `[]` rather than `null` for an empty plan diff and guards is already on `main` (`internal/controller/api/actions.go:105-109`, `internal/core/lifecycle/engine.go:177`).
- Part B obligation 1 (`main` exits through `cli.ExitCode` and prints with `cli.PrintError`): Task 41.
- Part B obligation 2 (CLI parity test and the end-to-end CLI run with a token): Tasks 40 and 42.
- Part B obligation 3 (`internal/cli` now imports the pinned modules): Task 36 removes `internal/deps/deps.go`.
- Part C obligation 1 (`Dispatcher.Changes` pages like the engine): Task 27 (`TestChangesMergeAndPageLikeTheEngine`).
- Part C obligation 2 (`SetListenerState` after `listen.Start`): Task 39.
- Standing for later plans: Part A obligation 3, Part B obligations 4 and 5, Part D obligations 1-5.

## Assumptions (evidence)

- The four parts were drafted and verified on 2026-10-18 in separate scratch clones: Parts A, B and C on `main` at `f28be88`, Part D on `897de97` (`f28be88` plus Parts B, C and A). A script then merged them in this plan's task order, renumbered the tasks and rewrote task references outside code blocks; the bullets below are each part's own evidence, with task numbers converted to this plan's. `main` has moved to `c6e2af5` since; `git diff --stat f28be88 c6e2af5` touches only `docs/superpowers/registers/` (checked 2026-10-18), so the code base is the one the parts were verified on.
- Plan-order replay on 2026-10-18: the 43 verified commits were cherry-picked onto `f28be88` in this plan's task order (each part's internal order is kept; parts interleave). `GOTOOLCHAIN=local timeout 600 go vet ./...` passed after every one of the 43 picks, and the final tree is identical to the verified assembly commit `dd64d4f` (`git diff --stat dd64d4f HEAD` prints nothing). On that tree `timeout 300 pnpm -C web install --frozen-lockfile && timeout 1500 make check` exited 0 (golangci-lint `0 issues.`, every Go package `ok`, govulncheck: no vulnerability in called code).
- Waves: the wave line follows the plan rules mechanically. Tasks 4, 5, 6, 10-18, 22, 26-34 and 37-39 have risk 3 and run alone; the other tasks group into consecutive waves of at most three that share no file and depend only on earlier waves (1-3, 7-9, 19-21, 23-24, 35-36, 40-42). Task 36 deletes `internal/deps/deps.go`, the blank-import pin file, but leaves `go.mod` and `go.sum` byte-identical (Part D's evidence), so it is not a manifest change and may share wave 35-36 with Task 35. Every same-wave pair was built together in the replay above, and each task was also built on its dependency closure alone (per-part bullets below).
- Register items this plan does not answer are recorded as register rows, not here: rows 49 (M2 uninstall), 75, 77, 78, 80 and 81 are deferred to the M7 security review, and the adjacent findings at the end of Part D's bullets are register rows of their own.

**Part A — P11: dispatch, agent runtime and audit**

- Every task's code below was run on 2026-10-18 in a scratch clone of `main` at `f28be88` (`/tmp/claude-0/-root-repositories-darkpanel/610205fd-ff50-4e4e-8d80-4c0d4053f8da/scratchpad/clone-A`), in task order, each committed with its planned message. After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`: `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages (`ok`); `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`); `go mod tidy -diff` (no output, exit 0). Each RED step was run on the task's parent commit with only that task's test files added, and its expected text is the real output. The code blocks were generated from the committed files by script.
- Each task whose `Depends on` is not simply the previous task was rebuilt on 2026-10-18 on a detached worktree at `f28be88` with only its dependency closure cherry-picked, and passed all five checks there: Task 5 alone; Task 12 on Task 5; Task 13 on Task 5 and Task 12; Task 14 on Task 4, Task 5, Task 7, Task 10; Task 23 on Task 4, Task 5, Task 7, Task 10, Task 14 and Task 19; Task 26 on Task 4, Task 5, Task 7, Task 10 and Task 12, Task 13, Task 14, Task 19, Task 23, Task 25 (without Task 11); Task 27 on the same plus Task 26.
- The plan text itself was checked on 2026-10-18: a script applied every task's `Create`, `Replace the whole of` and replacement blocks of this file, in task order, to a fresh worktree at `f28be88`, and `git diff --cached 9b343e2` on the result was empty (identical to the final commit). Three commit subjects were shortened to 50 characters with `git filter-branch --msg-filter` before generation (trees unchanged); the RED records and the dependency rebuilds above were re-run on the rewritten commits from helper scripts kept in the drafter's own `A-work` directory, after another drafter was found to have written helpers with the same names in the shared scratchpad; the new RED outputs were byte-identical to the earlier ones apart from timings.
- `go test -race -count=5` passed on 2026-10-18 for `./internal/core/lifecycle/` (17 s) and for `./internal/controller/dispatch/`, `./internal/agent/runtime/` and `./internal/controller/audit/`, so the real-time tests (the 50 ms verify deadline, `TestStopWaitsForARunningIngestion`) are not flaky on this host.
- Ruling from the brief (row 48): `Source string` is added to `core.JournalEntry`; the engine sets it from the acting principal (Task 7); agent migration `0004_journal_source.sql` adds the column (STRICT accepts `TEXT NOT NULL DEFAULT ''`: `TestJournalKeepsSource` reads it back); the audit store maps an empty source to SQL NULL, which `coreactions` renders as `source: null` (`internal/controller/coreactions/audit.go:106`, `nonEmpty`).
- Ruling from the brief (row 50): `schema.Redact` (Task 4). `internal/core/secret` imports no internal package (`rg -n '"github.com/darkraise' internal/core/secret/*.go` on 2026-10-18 finds only test imports of `internal/core`), so `schema` importing `secret` creates no cycle. The test lives in package `schema_test` so it can use the real `coreactions.Enrollment`, `coreactions.NewToken` and `coreactions.AuditList` types.
- Ruling from the brief (row 52): handler panics and the Verify deadline (Tasks 10 and 11). Ruling (this part): only a `runtime.Error`'s text is logged as the panic value; any other value is logged by type, because a handler may have put its input in it. `Protect` is exported so the runtime's preflight checks and resource reads follow the same policy (additive to `lifecycle`).
- Ruling (this part): the engine's crash tests used `panic(errCrash)` inside Apply (`internal/core/lifecycle/recover_test.go:29-45` at `f28be88`), which the panic policy now recovers into a failed change. Task 10 replaces it with `runtime.Goexit` in the handler (deferred calls run, `recover` returns nil, the goroutine ends after intent is persisted), which still leaves the change `applying` for recovery.
- Ruling (this part): a handler that ignores its context still returns from `Verify` at the deadline: the engine runs Verify in its own goroutine and abandons it at the deadline. Verify only reads the host by contract, so the change's outcome (and, for a revertible change, its revert) is recorded while the abandoned call may still run. `context.WithTimeout` uses real time; `coretest.FakeClock` cannot drive a context deadline, so the tests shorten the unexported `verifyTimeout` to 50 ms. What an abandoned Verify of a real module does after its context ends is not verified by this plan: no M1 module has a mutating action (hostinfo has none, `internal/modules/hostinfo/hostinfo.go`), so no production Verify exists to abandon. The first plan that registers a module with a mutating action owns that check for its own Verify (register row 52's policy applies to it).
- Ruling (this part): a Verify timeout is a plain error, so the change's error is code `internal` with message `Internal error`, and `changeError` logs `lifecycle: action failed` with `Verify of <ref> did not finish within <d>` (`TestAHungVerifyFailsTheChange`).
- Ruling from the brief (audit store rules): `Record` keeps a supplied id and accepts `core.auth.login`/`core.auth.logout` version 1 (`TestRecordKeepsTheIDAndLoginActions`); `List` follows `core.AuditQuery` and the `nextBefore` semantics of `coretest.MemAudit.List` (`internal/core/coretest/audit.go`), which `coreactions` tests already rely on. Ruling (this part): new audit ids are ULIDs stamped from the event's `At`, so newest-first by id is event order; an entry with a zero `At` takes the store's clock.
- Ruling (this part): `core_audit.phase` and `result` have CHECK constraints listing spec section 11's values, so an unknown phase fails the change's transaction rather than writing an unreadable row. `core_journal_cursor` and `core_change_index` have no foreign key to `core_hosts`, so removing a host (M2) decides their fate explicitly and audit history is never cascaded away.
- Ruling from the brief (ingestion): after each agent apply, confirm and revert, and every 5 s with `Clock.AfterFunc`. Ruling (this part): `IngestHost` acknowledges only when the cursor moved past what this process acknowledged, and its first run acknowledges a stored cursor, which covers a crash between storing and `Ack`; the host list comes from `core_hosts`; `IngestHost` and `IngestAll` after `Stop` do nothing.
- Ruling from the brief (runtime shape): the runtime takes an opened `*store.DB` and resolved values, builds the one `platform.Files` itself from the registry's owned paths (`internal/platform/files.go:40` requires exactly one per agent), and sets `Base.Clock` (`internal/modules/hostinfo/hostinfo.go:154` reads `env.Clock`). Ruling (this part): `Recover` and the first `Purge` failing fail `Open`; preflight runs after `Recover` with a zero principal; a module whose migration failed is gated by the runtime itself (its tables may be missing) with `ReasonMigrationFailed`, and the error, which may quote SQL, is logged only (`store.Migrate` reports it in `Result.Failed`, `internal/core/store/migrate.go:117`, `:159`). Preflight failures and enablement are gated by the dispatcher alone, so a later `core.module.enable` whose preflight passes is not contradicted by a stale agent-side status.
- Ruling (this part): a preflight reason is the check's error text, as spec section 2a defines (`PreflightCheck` comment in `internal/core/contract.go`); a panicking check's reason is `preflight check <id> of <module> panicked`. Needs a controller decision: hostinfo's check returns `cannot read /proc/meminfo: <cause>` (`internal/modules/hostinfo/hostinfo.go:121`, `checkMeminfo`), and that text reaches the `module.unavailable` detail, which obligation 3 says must carry no path. Settled by the controller ruling of 2026-10-18 in Part D's bullets: module-authored operator text, hostinfo unchanged.
- Ruling from the brief (Hello): `Hosts` says Hello on the first reach of a host in this process and again after a failure; P14 calls `Hosts.Hello` at startup to seed rows. It writes only modules the registry has, never `core`, refuses a reply for another host id, and never changes `enabled` on an existing row (`TestHelloNeverChangesEnabled`).
- Ruling from the brief (obligation 1): an in-memory plan index filled by `Plan`. Ruling (this part): entries live until `PlanIndexRetention` (1 h) after the plan expires, so a late apply still gets the engine's `plan.expired` and a repeated apply within the hour returns its change; after that, and after a restart, the plan is `plan.not_found`. The index also checks the agent's returned `PlanView` against the request. No new contract and no table were needed.
- Ruling (this part): a controller-side action with a host, or an agent action without one, is `action.unknown`, matching the API's own side check (`internal/controller/api/actions.go:147`, `lookupAction`). Apply re-checks the module gate, so a module disabled after its plan cannot be applied; `Confirm` and `Revert` are not gated.
- Ruling (this part): a failed `core_change_index` insert or a failed ingestion after an agent apply is logged, not returned: the change exists and its apply response may carry a secret output shown only once. Without the index row the change can no longer be confirmed through the dispatcher and its revert timer reverts it.
- Controller ruling 2026-10-18 (relayed from the API drafter): `Dispatcher.Changes` pages exactly like `lifecycle.Engine.Changes` (`internal/core/lifecycle/engine.go:310`). Each source returns its newest `Limit` matches below `Before`, so the union's newest `Limit` is exact; `TestChangesMergeAndPageLikeTheEngine` interleaves controller and agent changes, pages from a cursor in the middle, and walks every page of 2. Ruling (this part): a host that cannot be listed fails the whole call instead of being skipped, because a silently short page would end the API's refill early.
- Ruling (this part): `coretest.MemAudit` does not learn `Source` (`internal/core/coretest/audit.go:57`, `fromJournal`): no test outside this part needs it, and this part's tests use the real `audit.Store`.
- Ruling (this part), Task 10's size: it touches seven files, which this plan counts as two shapes (implementation and test), as the drafting brief directs; the four implementation edits are one decision (wrap every handler call in `protect`), and the three test files are the new tests plus the crash-simulation change the policy forces.

**Part B — P13: the CLI**

- Every task's code below was run on 2026-10-18 in a scratch clone of `main` at `f28be88`, in task order, each committed with its planned message. After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`: `go vet ./...` (no output); `go test -count=1 ./...` (all `ok`/`?`); `go test -race -count=1 ./internal/cli/` (`ok`); `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`); `go mod tidy -diff` (no output, exit 0). After the controller's two rulings were folded into Tasks 1, 16, 18 and 22, every one of the eleven rewritten commits was checked out again on 2026-10-18 and passed all five checks. The final tree has 49 tests in `internal/cli`, none skipped, on kernel `7.0.14-19-pve`, running as root.
- Tasks whose `Depends on` line is not the previous task were rebuilt on `f28be88` plus only their dependencies (`git cherry-pick`) on 2026-10-18 and passed all five checks there: Task 8 on Task 1; Task 17 on Task 1; Task 18 on Tasks 1 and 17. Task 20's dependencies (Tasks 16, 8, 17 and 18, and through them Tasks 1 and 15) are every earlier Part B task, so its tree is Part B's linear one.
- Every RED step was run before its implementation, and rerun on 2026-10-18 against each task's parent commit with only that task's test files (the expected texts above are those runs). Tasks 22, 24, 28 and 29 add no identifier their tests reference, so their RED is a test failure rather than a build failure.
- The plan's code blocks were generated by script from the verified commits (`git show <commit>:<path>`); a modified `root.go` is shown as exact replacement blocks computed from the commit's diff, each unique in the file before the task.
- Controller ruling (2026-10-18, security review of `config.go`): the config file is opened once with `O_RDONLY|O_NOFOLLOW|O_NONBLOCK` and checked on the open descriptor with `f.Stat()`: a regular file, `Perm()&0o077 == 0`, owner uid equal to `os.Geteuid()`; the refusal names the file and says `run chmod 600 <path>`. This tightens spec section 15's "refused if group or other can read it", because a group-writable file could redirect the token to another URL. `O_NONBLOCK` keeps a FIFO at the path from blocking the open. `TestConfigFileRefusals` covers modes 0640, 0604, 0644, 0620, 0602, 0610, a symbolic link, and (only when the test runs as root, as it did here) a file chowned to uid 65534.
- Controller ruling (2026-10-18, security review of `output.go`): one function, `sanitize` (Task 1, so `PrintError` and every later task share it), escapes `unicode.IsControl` runes (C0 including ESC, DEL, C1), U+2028, U+2029, U+200E, U+200F, U+202A-U+202E and U+2066-U+2069. Form: `\xNN` below U+0080 (so ESC is `\x1b`), `\uNNNN` with lowercase hex otherwise (so RLO is `\u202e`). Tabs and newlines in values therefore show as `\x09` and `\x0a`. `--json` writes the server's bytes unchanged (Go's encoder on the server escapes C0 controls). `TestRenderSanitizes`, `TestSanitize`, `TestPrintErrorSanitizes`, `TestMutatingSanitizesThePlan` and the web-ca preamble case cover each path.
- Plan-time ruling, brief ruling 1 (package shape): `NewRoot(opts Options) (*cobra.Command, error)`; it returns an error because generation can fail (clashes, refused input fields). `Options` has no clock: nothing in the CLI needs time except the test's `auth.TOTPCode`.
- Plan-time ruling, cobra errors as usage errors: cobra 1.10.2 validates flags and arguments before `RunE` (`command.go:955`, `:968`) and returns plain errors, and the root's unknown-command error comes from `legacyArgs` (`args.go:28`). Rather than parse their text, `markRuns` marks every error a `RunE` returns, and `ExitCode` maps unmarked errors to 2.
- Plan-time ruling: the CLI uses the highest version of each action name (`core.Registry.Actions` sorts by name and version, `internal/core/registry.go`); older versions get no command.
- Plan-time ruling: the version path segment is `v<n>` (for example `/api/v1/actions/core.host.list/v1/read`), not `<n>` as the brief writes: the API parses it with `core.ParseVersion`, which requires the `v` (`internal/core/contract.go:72-75`, `internal/controller/api/actions.go:149`).
- Plan-time ruling, connection flags: `--url` sits on each network command and `--socket`/`--dev` on each break-glass command and `controller web-ca`, not on the root as persistent flags, so `serve`'s own `--dev` is not shadowed and `--help` shows only the flags that apply. When `--socket` is empty the path is `paths.Resolve(--dev).ControllerSocket`, which reads the process environment (`internal/paths/paths.go:60`), not `Options.Getenv`; tests pass `--socket`.
- Plan-time ruling, config: the file is read on every network command when it exists, and its `caFingerprint` applies whatever the URL's source; unknown fields are refused (a misspelt `caFingerprint` must not silently disable pinning); a refused or malformed file exits 1; an invalid URL is a usage error. `caFingerprint` must be exactly 64 hexadecimal digits, without colons.
- Plan-time ruling, TLS: `VerifyConnection` replaces `VerifyPeerCertificate` (spec section 15 names the latter) because gosec G123 flags that resumed sessions skip `VerifyPeerCertificate`; the checks are the spec's. A mutation run on 2026-10-18 that removed the CA check made `TestPinnedCARefusals` fail (`fingerprint of the leaf: error <nil>`). Requests time out after 90 seconds.
- Plan-time ruling, input: `--input` may carry any field, secrets included (a file is not a flag); flags override its fields; secrets still missing are read afterwards. With `--input -` standard input is consumed, so a missing secret then fails as a usage error. A required boolean that is absent is sent as `false`. Enum values are listed in the flag help and validated by the server.
- Plan-time ruling, host: `--host` matches an id first, then a name; an unknown or ambiguous host, or several hosts without `--host`, is a usage error listing `<id> (<name>)`; no hosts at all exits 1. Every agent-side command makes one `core.host.list` read first. Controller-side actions that take a host in their input (`core.module.*`) get a plain `--host-id` flag.
- Plan-time ruling, output: rendering rules are in Task 18. A mutating command with `--json` writes the plan to standard error and the raw `Change` to standard output. `change list` shows the columns `id, hostId, action, version, class, state, actor, updatedAt, revertDeadline`.
- Plan-time ruling, mutating flow: only `y` or `yes` (any case) applies; end of input declines. A `--confirmation` that differs from the plan's phrase is refused locally with exit 3 before any apply. Without a terminal, an irreversible action needs `--confirmation` even with `--yes`. `confirmTimeoutSeconds` has no flag (spec section 15 names none). After apply, `failed`, `reverted` and `interrupted` exit 1; `change confirm` and `change revert` exit 1 unless the change ends `confirmed` or `reverted`.
- Plan-time ruling, enrollment: the password is asked twice only on a terminal (differing answers are a usage error) and read once from a pipe; the TOTP code is an `x-secret` input (plan 3's ruling), so it is read without echo; the activation step skips the second `Apply? [y/N]` (`planAndApply`'s `agreed`), never a phrase; one code attempt, after which the error names `darkpanel user activate --username <name>`; `--json` is refused on the two enrollment commands; a `totpSecret` that is not unpadded base32 is refused rather than printed. The QR code uses `qrterminal.GenerateHalfBlock` (`qrterminal.go:252`), which never calls `IsSixelSupported` (`:52`), the function that writes an escape to the terminal and reads its answer.
- Plan-time ruling: usage errors for the enrollment commands and the `change` group are raised in their own `RunE` (missing `--username`, unknown subcommand); a missing `change` id is cobra's `ExactArgs(1)`, unmarked, so exit 2.
- Plan-time ruling: input fields whose flag would be one of the CLI's own flags, or two fields with one flag, fail generation (obligation 5); no registered action has one today (`TestNewRootTree` and `TestEveryActionHasACommand` build the real core module).
- Advances register row 11 (first admin by CLI on the host): the CLI half of its end-to-end acceptance; the end-to-end test is P14's.
- Settled by the controller ruling of 2026-10-18 in Part D's bullets (no CLI login in M1; the README documents the `curl` sequence), kept for the record: spec section 15 defines no `login` command, so the CLI has no way to obtain its first API token; it comes from the web UI or the API with a session (see "How the next parts use it", step 2). The controller decides whether M1 needs a CLI login or token bootstrap through the socket.
- The break-glass routes the socket serves are exactly `GET /healthz`, `GET /api/v1/controller/web-ca` and plan, apply and read of break-glass actions (`internal/controller/api/socket.go:191-196`); `GET /api/v1/controller/web-ca` answers `{pem, fingerprint}` (`socket.go:212-219`). The six `core.user.*` actions are break-glass (`internal/controller/coreactions/users.go:41-53`, `enroll.go:21-26`). Apply answers 200 with the change whatever its outcome (`internal/controller/api/actions.go:202`).
- `schema.Input` lists properties in struct order with `required`, `enum`, `x-secret` and a `$ref` into `$defs` (`internal/core/schema/reflect.go:24-36`, `:115`; probed on 2026-10-18 for every core action input). `github.com/spf13/pflag` is `// indirect` in `go.mod` (line 29), which is why no task imports it.

**Part C — plan 3 follow-ups: API, identity and core actions**

- Every task's code was run. The part was drafted on 2026-10-18 in `/tmp/claude-0/-root-repositories-darkpanel/610205fd-ff50-4e4e-8d80-4c0d4053f8da/scratchpad/clone-C`, cloned from `main` at `f28be88` (`docs(plans): complete m1-identity-and-api`).
  - Each task followed TDD. Every RED output quoted in a Step 2 was re-run with the task's final test text on the task's parent commit.
  - After each task's commit these passed: `go vet ./...` (no output), `go test -count=1 ./...` (every line `ok` or `?`), `go test -race -count=1` on the task's package, `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`) and `go mod tidy -diff` (no output).
  - Dependencies were checked by cherry-picking onto `f28be88` and running the same five checks, all green: Task 6, Task 30, Task 31, Task 3, Task 32 and Task 33 each alone, Task 9 with Task 6 only, and Task 34 with Task 33 only. After the controller's amendment of Task 32 (2026-10-18) Task 32 was amended, Task 33 and Task 34 replayed on it, and every one of these rebuilds and the RED of Task 32 were run again, all as expected.
  - The code blocks were generated by script from the commits (`git show <commit>:<path>` and line diffs between parent and commit). A second script applied every task's text literally to a fresh checkout of `f28be88` and found each resulting file byte-identical to its commit.
- Plan-time ruling (row 66): `core.user.reset-password`'s `StateHash` stays `stateHash(u.ID)` and does not cover the token set. Its diff states a rule ("its API tokens are revoked"), not a list of tokens, so a token created or revoked between plan and apply changes nothing the plan showed, and the apply revokes whatever is live then. `core.user.delete` is the same: its diff names "its sessions and API tokens" while its hash covers only id, state and last-admin (`internal/controller/coreactions/users.go:203-206`).
- Plan-time ruling (row 66): the action's `Summary` changes with its diff line. The schema snapshots do not carry summaries; they hold name, version, class, side, breakGlass and the schemas (`internal/core/schema/snapshot.go:43`). The OpenAPI document skips break-glass actions (`internal/core/schema/openapi.go:180`), so no snapshot or document changes.
- Plan-time ruling (row 66, confirmed by the controller on 2026-10-18): `ResetTOTP` and `core.user.reset-totp` still keep the user's tokens (`internal/controller/coreactions/users.go:110`); the owner's ruling names only the password reset.
- Plan-time ruling (row 67): "after the password verified" means `verifyHeld` accepted the password. Errors before that point (the user lookup, the dummy hash, a malformed stored hash) stay uncounted and unaudited, as before.
- Plan-time ruling (row 67, confirmed by the controller on 2026-10-18): a failure of the success audit row counts as such a fault. The session is already committed then: the attempt is counted, one `error`/`internal` row is attempted and the audit error is returned. The orphan session stays in `core_sessions` until it expires and is purged. Its id is never returned, so nobody can use it; ending it is not part of this task.
- Plan-time ruling (row 67): the limiter clears a key's failures when it locks (`internal/controller/auth/ratelimit.go:194-197`), so the test expects `(i+1) % UsernameFailureLimit` failures after attempt `i` and `auth.rate_limited` after the fifth fault.
- Plan-time ruling (row 68): agent-side changes of unregistered actions stay visible, because the registry refuses an agent-side break-glass action (`internal/core/registry.go:141`), so such a change cannot be break-glass.
- Controller ruling of 2026-10-18 (row 68): show, confirm and revert answer a hidden change `change.not_found` with detail `no change <id>`, the engine's own wording (`internal/core/lifecycle/engine.go:286`), so on the network a hidden id cannot be told from a missing one. This replaces the brief's "confirm and revert keep refusing" (which answered `auth.forbidden` and so revealed the id); the change is still refused before `Dispatcher.Confirm` or `Revert` runs. `TestChangeOpsAuthorizeTheChangesAction`'s three hidden-change rows move from 403 `auth.forbidden` to 404 `change.not_found`, and `TestChangeOpsHideBreakGlass` checks both operations; run against the earlier Task 32 code on 2026-10-18 both failed with `status = 403, want 404`.
- Plan-time ruling (row 68, short page accepted by the controller on 2026-10-18), refill bound: at most 4 further fetches of 500 changes each, so a page comes back short only after a run of more than 2,000 hidden changes in a row. To a client that page looks like the end of the list, which is accepted because break-glass changes are rare user-administration changes. The default page size the API now passes explicitly is 100, the engine's `defaultChangeLimit` (`internal/core/lifecycle/engine.go:28`).
- Plan-time ruling (row 72): the rule matches by type (`analyze-types: true`, `pkg: ^net/http$`), so a renamed import is also reported. It is scoped with a `linters.exclusions.rules` entry using `path-except`. `timeout 120 golangci-lint config verify` exited 0 on 2026-10-18.
  - Probe run on 2026-10-18 with temporary files that were then deleted: one in `internal/controller/api` called `http.Error` once directly and once through a `nethttp "net/http"` import, and one in `internal/controller/listen` called it directly. The linter reported ``internal/controller/api/zz_probe.go:9:2: use of `http.Error` forbidden because "answer refusals with writeProblem" (forbidigo)`` and ``internal/controller/api/zz_probe.go:10:2: use of `nethttp.Error` forbidden because "answer refusals with writeProblem" (forbidigo)``. It reported nothing for the `listen` file (staticcheck's ST1019 also flagged the double import in that probe).
  - The Task 3 probes gave `0 issues.` under the old configuration and exactly the one forbidigo report quoted in Task 3 under the new one.
- Plan-time ruling: rows 73 and 72 are two tasks (Task 31, Task 3), not one, so each commit is atomic and a reviewer can accept one without the other. Row 66 is likewise split into the identity change (Task 6) and the action's wording (Task 9).
- Plan-time ruling (row 79): the message names the value's position and Go type, the rendering and the secret's position. Go 1.27 prints a `json.RawMessage` as its text under `%+v`, observed on 2026-10-18 when a first draft of the test expected a JSON-only leak from it, so the JSON-only case uses the `hidden` Stringer type.
- Plan-time ruling (obligation 7 of row 69): the design is a setter on `*Server` (`SetListenerState`), not a `Config` field, because the `*listen.Service` exists only after `api.New` and `srv.Handler()`. The source is a `func() ListenerState` held in an `atomic.Pointer`; nil means "not started". The route is `GET /api/v1/controller/listeners` with read scope, listed last in `Routes()`. `TestDenyByDefault` walks `h.srv.networkRoutes()` (`internal/controller/api/deny_test.go:45`), so the matrix covers the route without edits; `TestRoutes` checks that it is not public and that `routeAccess` agrees.
- The listener messages the footer shows are P10's: `listen.Service.State()` (`internal/controller/listen/service.go:98`), for example `The listeners are shut down.` (`service.go:114`).
- Commit subjects were counted by script to be at most 50 characters (the longest is 50: `feat(api): serve the listener state for the footer`).

**Part D — P14: assembly**

- Every task's code below was run on 2026-10-18 in `/tmp/claude-0/-root-repositories-darkpanel/610205fd-ff50-4e4e-8d80-4c0d4053f8da/scratchpad/clone-D`, branch `assembly`, base `897de97` (`main` at `f28be88` plus parts B, C and A cherry-picked; vet, tests, lint and tidy clean there per the controller), in task order, each committed with its planned message. After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`: `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's package (`ok`); `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`); `go mod tidy -diff` (no output, exit 0). Each RED step was re-run on the task's parent commit with only that task's test files added; the expected texts are those runs. The code blocks were generated from the commits by script (`git show <commit>:<path>`), and the replacement blocks were checked by applying them to the parent's file and comparing with the commit's.
- Each task whose `Depends on` is not simply the previous task was rebuilt on 2026-10-18 in a detached worktree at `897de97` with only its dependency closure cherry-picked, and passed all five checks there: Task 37 alone; Task 38 on Task 35 and Task 37; Task 42 on Task 35, Task 37, Task 38 and Task 39 (without Task 40 and Task 41); Task 36 alone; Task 43 on Task 35, Task 37, Task 38, Task 39 and Task 41; Task 21 alone. Task 40, Task 39 and Task 41 depend only on the previous task. Script and logs: `/tmp/claude-0/-root-repositories-darkpanel/610205fd-ff50-4e4e-8d80-4c0d4053f8da/scratchpad/D-work/deps.sh`, `.../D-work/logs/dep-*.log`.
- The plan text itself was checked on 2026-10-18: `D-work/replay.py` (in the scratchpad) applied every task's `Create`, `Replace the whole content of` and `Edit` blocks, Task 35's `make schemas` and Task 36's `git rm`, in task order, to a fresh worktree at `897de97`, and `git diff --cached dd64d4f` on the result was empty.
- `go test -race -count=5` passed on 2026-10-18 for `./internal/controller/server/` (13.8 s), `./cmd/darkpanel/` and `./internal/modules/`, so the end-to-end test and the socket and shutdown tests are not flaky on this host (kernel `7.0.14-19-pve`, running as root).
- Mutation runs on 2026-10-18 (each reverted): removing `jsonschema:"nullable"` from `auth.TokenInfo.LastUsedAt` made `TestPointerOutputsAreNullable` fail for `core.token.create/v1`, `core.token.list/v1` (`tokens[].lastUsedAt`) and `core.token.revoke/v1`; skipping the commands of `core.token.revoke` and `hostinfo.summary` in `cli.NewRoot` made `TestEveryActionHasACLICommand` name both; removing `Recover` made `TestRecoverRunsAtStart` fail (`interrupt audit rows: 0, want 1`); not re-arming the purge timer made `TestPurgesRunAtStartAndEveryTenMinutes` fail; an identity `Redact` in the controller engine made `TestEndToEnd` fail four ways (stored token, a secret in `audit list`, the activation code and the password unredacted); disabling the CSRF check in `api` made 111 cells of the end-to-end matrix fail with `want 403 auth.csrf`. Making the hostinfo resource route public in `routeAccess` alone did not open it: `requireCaller` still answered 401.
- The built binary was run on 2026-10-18 from Part D's commit of Task 36 (in Part D's own order that commit followed Tasks 35 and 37-42, so its tree held the assembled server and the wired CLI; in this plan's order Task 36 comes earlier, and the same run applies to the tree after Task 41): `serve --dev --listen http://127.0.0.1:18080` answered `/healthz`; `controller web-ca --dev` printed the fingerprint; a second `serve` failed with `another darkpanel serve holds .../controller.lock`; `serve --listen bogus` exited 2; SIGTERM shut it down and removed the socket. The README's flow (first admin with `--yes` and piped input, the `curl` login, plan and apply of `core.token.create`, then `hostinfo summary get` and `audit list` with `DARKPANEL_URL`/`DARKPANEL_TOKEN`) worked against that server. Port 8080 is taken on this host by another process, hence port 18080.
- Ruling from the brief (startup order) and controller ruling of 2026-10-18 (Part A's ten steps are binding where more specific): the local `core_hosts` row exists before `Hosts.Hello`, which seeds the module rows from `DefaultEnabled` (spec section 2's "on first start the assembly seeds"); the controller engine's `Recover` and purge loop are this part's, the agent runtime runs its own (`internal/agent/runtime/runtime.go:89`, `Open`). Where the brief lists ingestion before the purges and Part A lists the purges first, Part A's order is used; nothing depends on it, and the shutdown order Part A states (ingester before the purge timer) follows from it.
- Ruling from the brief (lock, row 76): `flock(2)` locks belong to the open file description, so a second `serve` in the same process (the tests) is refused like one in another process (`TestOneServerAtATime`). The keys are loaded before the lock, as the brief orders; `secret.LoadOrCreateKey` creates with `link(2)` and loads on `EEXIST` (spec section 5), so two first starts cannot produce two keys.
- Ruling (this part), obligation 4: the socket directory must also be owned by the effective uid, because its owner can chmod it; checked with `syscall.Stat_t.Uid` (`TestSocketDirectoryMustBePrivate` chowns it to uid 65534 when run as root). `os.MkdirAll` creates a missing directory 0700; an existing one keeps its mode (`internal/paths/paths.go:101`), which is why the check exists.
- Ruling (this part): a failed module controller migration is logged and startup continues (spec section 4 and R9: a failing module never blocks others). No module has controller migrations in M1 (`hostinfo` declares none, `internal/modules/hostinfo/hostinfo.go:111`), and dispatch has no controller-side module gate, so obligation 2 records the gap.
- Ruling (this part): the local host is named `localhost` when the hostname cannot be read, because `core_hosts.name` is `NOT NULL` (`internal/core/store/migrations/controller/0001_core.sql`); the listener leaf then gets an empty hostname, which plan 3 defines as SANs `localhost` only. The name is set on the first start only.
- Ruling (this part): `core_meta.controller_id` (spec section 4's key list) is not written: no code reads it (`rg -n controller_id internal cmd` finds nothing on 2026-10-18). Obligation 3.
- Ruling from the brief (row 30): `serve`'s refusals of its command line are `PreRunE` errors, which `cli.markRuns` does not wrap (it wraps `RunE` only, `internal/cli/errors.go`, `markRuns`), so `cli.ExitCode` maps them to 2 without a new exported error type in `cli`. Ruling (this part): running production mode without root is such a refusal (exit 2), because its fix is the command line (`sudo` or `--dev`); `TestExitCodes` covers both and that `serve` then never starts.
- Controller ruling of 2026-10-18: the CLI has no login command (spec section 15 defines none); the first API token comes from the web UI's Tokens page (plan 5) or the API. The end-to-end test logs in over the API, creates a token through `core.token.create` and runs the CLI with it; the README documents the `curl` sequence.
- Controller ruling of 2026-10-18: a module's preflight reason (hostinfo's `cannot read /proc/meminfo: ...`) is operator-facing text the module writes (spec section 2a); register row 69's obligation 3 covers core, dispatcher and engine errors. hostinfo is unchanged.
- Controller ruling of 2026-10-18: Part A's dispatcher abandons a `Verify` that passes its deadline; accepted, nothing here changes it.
- Controller ruling of 2026-10-18: `api.(*Server).SetListenerState` and `GET /api/v1/controller/listeners` are Part C's; `cli.NewRoot` returns `(*cobra.Command, error)`, connection flags are per command, and the version path segment is `v<n>` (Part B). Task 39 sets the source after `listen.Start`; `TestServesTheNetworkAndTheSocket` reads `Tailscale listener disabled by --listen.` through the route.
- Ruling from the brief (testability): `Options` carries the overrides; `ParseSpecs` refuses port 0 (`internal/controller/listen/spec.go`), so tests list `loopback` and `localBind` maps every endpoint to `127.0.0.1:0`, as plan 3's `fakeBinder` does (`internal/controller/listen/set_test.go:30`). With `loopback` alone the Tailscale interface is never read; `TestDefaultListenValues` passes an `Interfaces` that reports no `tailscale0`.
- Ruling (this part): the end-to-end test lives in package `server` (`internal/controller/server/e2e_test.go`) so it reuses `testOptions`, `start`, `baseURL` and `count`; it reaches the server only over HTTP, the socket and the CLI, except `Registry()` for `api.CheckOpenAPI` and the OpenAPI matrix. It sets `Dev` so the socket accepts a non-root test runner; the expected socket actor is `local:root@<id>` for uid 0 and `local:uid-<n>@<id>` otherwise. The first admin is created in two CLI runs (create, then `user activate`), the alternative Part B describes, because standard input is not a terminal.
- Ruling (this part), the deny-by-default matrix: paths come from `schema.OpenAPI(reg, api.Routes())` (every action, resource and hand-written route; `api.CheckOpenAPI` proves the document and the network mux agree) plus five unrouted paths; methods GET, POST, PUT, PATCH, DELETE and OPTIONS (spec "Verification"); public cells (POST login, GET `/healthz`, GET of a non-API path) are skipped; each other cell is refused 401 `auth.required` with no credentials, an invalid bearer and an invalid cookie, and every unsafe method with a valid session and no CSRF token is refused 403 `auth.csrf`.
- Ruling (this part), register rows 8 and 14: an audit row holds the action's input only, so "token secret and TOTP secret redacted" is checked as: neither the token, the TOTP secret nor the password appears anywhere in `audit list --json`, `core.user.create`'s input shows `"password":"[redacted]"` and `core.user.activate`'s `"code":"[redacted]"`, and the stored change of `core.token.create` shows `"token":"[redacted]"` while its apply response carried the token.
- Ruling (this part), register row 5 end to end: `core.module.disable` of hostinfo through the API makes the resource answer 409 `module.unavailable` over HTTP and exit 1 in the CLI while `token list` still works; `core.module.enable` (which runs the preflight again through the agent) serves it again.
- Ruling (this part), schema snapshots: the plan does not print the fifteen generated JSON files; Task 35's step runs `make schemas`, whose output is deterministic, and `TestSnapshotsCurrent` compares every committed file with the in-memory regeneration and flags orphans outside `schemas/released/`. `schemas/released/.gitkeep` keeps the directory (git stores no empty directory); `schema.CheckReleased` ignores non-JSON files and a missing directory (`internal/core/schema/compare.go:80`).
- Ruling (this part), register row 71: `schemagen/main.go`'s doc comment ("a test in internal/modules fails when the committed snapshots are out of date") becomes true with Task 35; the comment is not edited (not this part's file).
- Ruling (this part), register row 39: ADR 0010's claim is narrowed to what is checked: the CA/Browser Forum Baseline Requirements, section 6.1.5, admit only RSA and ECDSA P-256, P-384 and P-521 keys ("No other algorithms or key sizes are permitted", https://cabforum.org/working-groups/server/baseline-requirements/documents/, searched 2026-10-18). Whether a given browser verifies an Ed25519 server chain was not checked; the ADR now says browsers cannot be relied on to, and its rejected alternative says they are not required to accept them. `scripts/check-adrs.sh --complete` and its tests pass.
- Ruling (this part), Task 36: removing `internal/deps/deps.go` leaves `go.mod` and `go.sum` unchanged: every module it pinned is now imported by non-test code (`internal/cli` imports cobra, `golang.org/x/term` and qrterminal, Part B obligation 3), and `golang.org/x/crypto/ssh` shares its module with argon2, so the harness keeps it.
- Ruling (this part), task shapes: Tasks 40 and 42 add tests over code that already exists, so their Step 2 expects PASS on the parent (the mutation runs above show they fail when the code is wrong); Tasks 21, 36 and 43 change no Go code and have no test of their own, so their steps are change, checks, commit.
- Register row 9: the end-to-end test, `/healthz` and the one-line hostinfo registration are here; the Playwright check needs plan 5's pages and is obligation 1, filed as its own register row for plan 5.
- Adjacent finding, not fixed (no file of this part; filed as a register row): a controller socket path longer than the kernel's 108-byte `sun_path` makes `serve` fail with `api: listen unix <path>: bind: invalid argument`, which does not say why (observed 2026-10-18 with a long `XDG_RUNTIME_DIR`). `api.ListenSocket` could refuse such a path with a clear message.
- Adjacent finding (filed as a register row): the `controller` migration backup is not logged by the server, while the agent runtime logs its own (`runtime: agent database backed up before migrating`); `store.Result.BackupDir` is available if an operator-facing line is wanted.

## Task index

1. Exit codes, error reporting and terminal sanitising
2. Secret scans name positions, not text
3. Lint rule against http.Error in the API
4. Redact adapter for the lifecycle engine
5. Journal source column
6. Password reset revokes the user's API tokens
7. Engine records the acting principal's source
8. Command naming and clash rules
9. core.user.reset-password says it revokes tokens
10. Handler panic policy
11. Verify deadline
12. Controller audit tables and audit store
13. Agent journal ingester
14. Agent runtime start and stop
15. HTTPS fingerprint pinning
16. Config file, connection precedence, API and socket clients
17. Input flags, --input and secret prompts
18. Output rendering
19. Agent runtime client methods
20. Command tree, read actions and resources
21. ADR 0010: narrow the Ed25519 claim
22. Mutating flow: plan, guards, prompts, phrase and apply
23. Host directory and module availability
24. change list, show, confirm and revert
25. Dispatcher reads, resources and plans
26. Apply, change index and ingestion trigger
27. Change views, confirm, revert and the merged listing
28. controller web-ca
29. user create and reset-totp enrollment
30. Count and audit login faults after the password
31. A panicking handler hands out no cookie
32. Network change views hide break-glass changes
33. Late-bound listener state source
34. Listener state route for the UI footer
35. Register hostinfo and commit the schema snapshots
36. Remove the dependency pin package
37. Server storage: paths, keys, lock, stores and local host
38. Server services: identity, agent runtime, engine, purges and dispatch
39. Server listeners, controller socket and Run
40. Registration rules, CLI parity and nullable outputs
41. darkpanel runs the generated CLI and serve
42. End-to-end test
43. README and CONTRIBUTING: run, first admin, web root, CLI token

---

### Task 1: Exit codes, error reporting and terminal sanitising

**Files:**
- Create: `internal/cli/errors.go`
- Test: `internal/cli/errors_test.go`

**Interfaces:**
- Consumes: `errcode.Code` with `Status()` and `Title()`, `errcode.FieldError`, `errcode.InvalidInput`, `errcode.AuthRequired`, `errcode.Forbidden`, `errcode.NotFound` (merged `internal/core/errcode`); `github.com/spf13/cobra` (pinned).
- Produces: Contracts › Task 1 (`ExitOK`…`ExitAuth`, `ExitCode`, `PrintError`, `usagef`, `refusedf`, `problemError`, `ranError`, `markRuns`, `sanitize`).

**Items:** 30

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Every later task reports failures through these names, so the exit-code mapping of spec section 15 lives in one place: a usage error is 2, a refused change 3, an API refusal with an `auth.*` code 4, anything a command's run returns 1. Cobra's own flag, argument and unknown-command errors are never marked by `markRuns`, which is how `ExitCode` classifies them as usage errors without parsing their text (register row 30). `sanitize` is the one function every human-readable string passes through, so a server cannot move the cursor, recolour the screen or reorder text with control or bidi characters. The tests pin every exit code, including real cobra failures, the problem-detail printing and the escape forms.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/errors_test.go`:

```go
package cli

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestExitCode(t *testing.T) {
	problem := func(code errcode.Code) error {
		return &problemError{Status: code.Status(), Code: code, Detail: code.Title()}
	}
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, ExitOK},
		{"usage", usagef("required flag --name not set"), ExitUsage},
		{"refused", refusedf("not applied"), ExitRefused},
		{"auth.required", ranError(problem(errcode.AuthRequired)), ExitAuth},
		{"auth.forbidden", ranError(problem(errcode.Forbidden)), ExitAuth},
		{"other problem", ranError(problem(errcode.NotFound)), ExitError},
		{"run error", ranError(errors.New("dial tcp: connection refused")), ExitError},
		{"usage inside a run", ranError(fmt.Errorf("input: %w", usagef("bad --input"))), ExitUsage},
		{"refusal inside a run", ranError(refusedf("a guard failed")), ExitRefused},
		{"unmarked error from cobra", errors.New(`unknown flag: --bogus`), ExitUsage},
	}
	for _, c := range cases {
		if got := ExitCode(c.err); got != c.want {
			t.Errorf("%s: ExitCode = %d, want %d", c.name, got, c.want)
		}
	}
}

func TestExitCodeOfCobraErrors(t *testing.T) {
	newRoot := func() *cobra.Command {
		root := &cobra.Command{Use: "darkpanel", SilenceUsage: true, SilenceErrors: true}
		fail := &cobra.Command{Use: "fail", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error {
			return errors.New("boom")
		}}
		fail.Flags().Int("count", 0, "a number")
		ok := &cobra.Command{Use: "ok", Args: cobra.NoArgs, RunE: func(*cobra.Command, []string) error { return nil }}
		root.AddCommand(fail, ok)
		markRuns(root)
		return root
	}
	cases := []struct {
		args []string
		want int
	}{
		{[]string{"ok"}, ExitOK},
		{[]string{"fail"}, ExitError},
		{[]string{"fail", "--bogus"}, ExitUsage},
		{[]string{"fail", "--count", "many"}, ExitUsage},
		{[]string{"fail", "extra"}, ExitUsage},
		{[]string{"nosuch"}, ExitUsage},
	}
	for _, c := range cases {
		root := newRoot()
		root.SetArgs(c.args)
		root.SetOut(new(bytes.Buffer))
		if got := ExitCode(root.Execute()); got != c.want {
			t.Errorf("darkpanel %v: exit %d, want %d", c.args, got, c.want)
		}
	}
}

func TestPrintError(t *testing.T) {
	var b bytes.Buffer
	PrintError(&b, ranError(&problemError{Status: 422, Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: "/name", Message: "required"}, {Path: "/port", Message: "must be at least 1"}}}))
	want := "darkpanel: invalid input (request.invalid_input)\n  /name: required\n  /port: must be at least 1\n"
	if b.String() != want {
		t.Errorf("problem printed as\n%q\nwant\n%q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, ranError(errors.New("dial tcp 127.0.0.1:8080: connection refused")))
	if want := "darkpanel: dial tcp 127.0.0.1:8080: connection refused\n"; b.String() != want {
		t.Errorf("plain error printed as %q, want %q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, nil)
	if b.Len() != 0 {
		t.Errorf("nil error printed %q", b.String())
	}
}

func TestSanitize(t *testing.T) {
	cases := map[string]string{
		"plain text, ünïcode":            "plain text, ünïcode",
		"\x1b[31mred\x1b[0m":             `\x1b[31mred\x1b[0m`,
		"a\tb\nc\rd\x00e\x7f":            `a\x09b\x0ac\x0dd\x00e\x7f`,
		"c1 \u0085 \u009b":               `c1 \u0085 \u009b`,
		"evil\u202Etxt.exe":              `evil\u202etxt.exe`,
		"\u200e\u200f\u202a\u2066\u2069": `\u200e\u200f\u202a\u2066\u2069`,
		"line\u2028para\u2029":           `line\u2028para\u2029`,
	}
	for in, want := range cases {
		if got := sanitize(in); got != want {
			t.Errorf("sanitize(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPrintErrorSanitizes(t *testing.T) {
	var b bytes.Buffer
	PrintError(&b, ranError(&problemError{Status: 422, Code: errcode.InvalidInput, Detail: "bad \x1b[2J input\u202E",
		Fields: []errcode.FieldError{{Path: "/n\x1b]0;x\x07", Message: "no\u2066pe"}}}))
	want := `darkpanel: bad \x1b[2J input\u202e (request.invalid_input)` + "\n" + `  /n\x1b]0;x\x07: no\u2066pe` + "\n"
	if b.String() != want {
		t.Errorf("printed %q, want %q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, ranError(errors.New("change c1 failed: disk \x1b[1mfull\u202e")))
	if want := `darkpanel: change c1 failed: disk \x1b[1mfull\u202e` + "\n"; b.String() != want {
		t.Errorf("printed %q, want %q", b.String(), want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestExitCode|TestExitCodeOfCobraErrors|TestPrintError|TestSanitize|TestPrintErrorSanitizes)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/errors_test.go:16:11: undefined: problemError`, then `undefined: ExitOK`, `undefined: usagef`, `undefined: ExitUsage`, `undefined: refusedf`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/errors.go`:

```go
// Package cli is the darkpanel command line (spec section 15): commands
// generated from the module registry, the hand-written change, controller
// and user enrollment commands, and the HTTP and controller-socket clients
// they share.
package cli

import (
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Exit codes (spec section 15).
const (
	ExitOK      = 0
	ExitError   = 1
	ExitUsage   = 2
	ExitRefused = 3 // a guard failed or a prompt was declined
	ExitAuth    = 4
)

// usageError is a command line the CLI cannot act on: a missing or invalid
// flag, argument or input.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usagef(format string, args ...any) error {
	return &usageError{msg: fmt.Sprintf(format, args...)}
}

// refusedError is a change the CLI did not apply: a failing guard, a
// declined prompt or a confirmation phrase that does not match.
type refusedError struct{ msg string }

func (e *refusedError) Error() string { return e.msg }

func refusedf(format string, args ...any) error {
	return &refusedError{msg: fmt.Sprintf(format, args...)}
}

// problemError is a refusal the API answered as RFC 9457 problem details
// (spec section 12).
type problemError struct {
	Status int
	Code   errcode.Code
	Detail string
	Fields []errcode.FieldError
}

func (e *problemError) Error() string { return fmt.Sprintf("%s (%s)", e.Detail, e.Code) }

// runError marks an error that a command's run returned. Cobra's own flag,
// argument and unknown-command errors are never marked, which is how
// ExitCode tells them apart as usage errors.
type runError struct{ err error }

func (e *runError) Error() string { return e.err.Error() }

func (e *runError) Unwrap() error { return e.err }

func ranError(err error) error {
	if err == nil {
		return nil
	}
	return &runError{err: err}
}

// markRuns wraps the RunE of root and of every command below it, so their
// errors are marked as run errors.
func markRuns(root *cobra.Command) {
	if run := root.RunE; run != nil {
		root.RunE = func(cmd *cobra.Command, args []string) error { return ranError(run(cmd, args)) }
	}
	for _, c := range root.Commands() {
		markRuns(c)
	}
}

// ExitCode is the process exit code for err, the error of a command tree
// that NewRoot built: 0 for nil, 2 for a usage error (including every error
// cobra raises while parsing the command line), 3 for a refused change, 4
// when the API refused with an auth.* code, and 1 for any other error.
func ExitCode(err error) int {
	var (
		usage   *usageError
		refused *refusedError
		problem *problemError
		run     *runError
	)
	switch {
	case err == nil:
		return ExitOK
	case errors.As(err, &usage):
		return ExitUsage
	case errors.As(err, &refused):
		return ExitRefused
	case errors.As(err, &problem) && strings.HasPrefix(string(problem.Code), "auth."):
		return ExitAuth
	case errors.As(err, &run):
		return ExitError
	default:
		return ExitUsage
	}
}

// PrintError writes err as "darkpanel: <message>", followed by one indented
// line per field error of an API refusal, all sanitized. A nil err prints
// nothing.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
	_, _ = fmt.Fprintf(w, "darkpanel: %s\n", sanitize(err.Error()))
	var problem *problemError
	if errors.As(err, &problem) {
		for _, f := range problem.Fields {
			_, _ = fmt.Fprintf(w, "  %s: %s\n", sanitize(f.Path), sanitize(f.Message))
		}
	}
}

// sanitize makes server-supplied text safe to write to a terminal: every
// control character (C0 including ESC, DEL, C1), the line and paragraph
// separators and the bidirectional controls become a visible escape, \xNN
// below U+0080 and \uNNNN above, so no answer can move the cursor, recolour
// the screen or reorder what the operator reads. Every string the CLI
// writes for people passes through it; --json output does not.
func sanitize(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r < 0x80 && unicode.IsControl(r):
			fmt.Fprintf(&b, "\\x%02x", r)
		case unicode.IsControl(r) || isFormatHazard(r):
			fmt.Fprintf(&b, "\\u%04x", r)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

func isFormatHazard(r rune) bool {
	return r == 0x2028 || r == 0x2029 || r == 0x200e || r == 0x200f ||
		(r >= 0x202a && r <= 0x202e) || (r >= 0x2066 && r <= 0x2069)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestExitCode|TestExitCodeOfCobraErrors|TestPrintError|TestSanitize|TestPrintErrorSanitizes)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/errors.go internal/cli/errors_test.go
git commit -m "feat(cli): add exit codes and error reporting"
```

### Task 2: Secret scans name positions, not text

**Files:**
- Modify: `internal/controller/coreactions/tokens_test.go`

**Interfaces:**
- Consumes: the merged `noSecret` helper of `internal/controller/coreactions/tokens_test.go` (plan 3 Contracts › Task 24); `errcode.New`, `errcode.NotFound`.
- Produces: Contracts › Task 2 (`noSecret` keeps its signature; `secretLeak`).

**Items:** 79

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 1 = 1

`noSecret` printed the whole text that contained a secret, so a failing run wrote the secret into the test log. The scan moves into `secretLeak`, which returns a message naming the value's position and type, the rendering (`%+v`, `Error()` or `JSON`) and the secret's position, and never the text; `noSecret` keeps its signature and every caller. The test feeds a leak through each rendering and checks the exact message and that it carries no secret. Go 1.27 prints a `json.RawMessage` as text under `%+v`, so the JSON-only case uses a type whose `String` hides its value.

- [ ] **Step 1: Write the failing test**

Add at the end of `internal/controller/coreactions/tokens_test.go` (after its last function, separated by one blank line):

```go
// hidden prints as "hidden" but marshals its value, so only the JSON
// rendering shows it.
type hidden struct{ Value string }

func (hidden) String() string { return "hidden" }

func TestSecretLeakNamesPositionsOnly(t *testing.T) {
	const secret = "s3cret-token-value"
	type holder struct{ Value string }
	for _, tc := range []struct {
		name    string
		secrets []string
		values  []any
		want    string
	}{
		{"no leak", []string{"other", secret}, []any{"clean", holder{"clean"}}, ""},
		{"empty secret ignored", []string{""}, []any{"anything"}, ""},
		{"struct", []string{"other", secret}, []any{"clean", holder{"x" + secret}},
			"value 1 (coreactions.holder) rendered as %+v contains secret 1"},
		{"JSON only", []string{secret}, []any{hidden{secret}},
			"value 0 (coreactions.hidden) rendered as JSON contains secret 0"},
		{"error", []string{secret}, []any{errcode.New(errcode.NotFound, "no token "+secret)},
			"value 0 (*errcode.Error) rendered as %+v contains secret 0"},
	} {
		got := secretLeak(tc.secrets, tc.values...)
		if got != tc.want {
			t.Errorf("%s: secretLeak = %q, want %q", tc.name, got, tc.want)
		}
		if strings.Contains(got, secret) {
			t.Errorf("%s: the message carries the secret", tc.name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSecretLeakNamesPositionsOnly' ./internal/controller/coreactions/`

Expected: FAIL — the build reports `internal/controller/coreactions/tokens_test.go:275:10: undefined: secretLeak` and `FAIL	github.com/darkraise/darkpanel/internal/controller/coreactions [build failed]`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/tokens_test.go`, replace this block (it occurs once):

```go
// testPassword is a valid password; tests scan outputs and errors for it.
const testPassword = "correct horse battery staple"

// noSecret fails when any of values, rendered with %+v or as JSON, contains
// one of secrets.
func noSecret(t *testing.T, secrets []string, values ...any) {
	t.Helper()
	for _, v := range values {
		texts := []string{fmt.Sprintf("%+v", v)}
		if err, ok := v.(error); ok {
			texts = append(texts, err.Error())
			var e *errcode.Error
			if errors.As(err, &e) {
				v = e
			}
		}
		if b, err := json.Marshal(v); err == nil {
			texts = append(texts, string(b))
		}
		for _, s := range secrets {
			for _, text := range texts {
				if s != "" && strings.Contains(text, s) {
					t.Fatalf("a secret appears in %s", text)
				}
			}
		}
	}
}

// activeUser creates and activates username through the identity service
```

with:

```go
// testPassword is a valid password; tests scan outputs and errors for it.
const testPassword = "correct horse battery staple"

// noSecret fails when any of values, rendered with %+v, with Error() or as
// JSON, contains one of secrets.
func noSecret(t *testing.T, secrets []string, values ...any) {
	t.Helper()
	if msg := secretLeak(secrets, values...); msg != "" {
		t.Fatal(msg)
	}
}

// secretLeak returns "" when no rendering of values contains one of secrets,
// else a message naming the first leaking value, its rendering and the
// secret by position. It never quotes the text, so a failing run does not
// write the secret into the test log.
func secretLeak(secrets []string, values ...any) string {
	for i, v := range values {
		type rendering struct{ name, text string }
		texts := []rendering{{"%+v", fmt.Sprintf("%+v", v)}}
		js := v
		if err, ok := v.(error); ok {
			texts = append(texts, rendering{"Error()", err.Error()})
			var e *errcode.Error
			if errors.As(err, &e) {
				js = e
			}
		}
		if b, err := json.Marshal(js); err == nil {
			texts = append(texts, rendering{"JSON", string(b)})
		}
		for _, r := range texts {
			for j, s := range secrets {
				if s != "" && strings.Contains(r.text, s) {
					return fmt.Sprintf("value %d (%T) rendered as %s contains secret %d", i, v, r.name, j)
				}
			}
		}
	}
	return ""
}

// activeUser creates and activates username through the identity service
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSecretLeakNamesPositionsOnly|TestToken|TestUser' ./internal/controller/coreactions/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/coreactions/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/tokens_test.go
git commit -m "test(coreactions): name secret leaks by position"
```

### Task 3: Lint rule against http.Error in the API

**Files:**
- Modify: `.golangci.yml`

**Interfaces:**
- Consumes: golangci-lint v2.15.0 and its `forbidigo` linter; the merged `.golangci.yml`.
- Produces: Contracts › Task 3 (the `forbidigo` rule and its path scope).

**Items:** 72

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 2 = 2

Plan 3's rule that every API refusal goes through `writeProblem` and no handler calls `http.Error` was enforced by review only. A `forbidigo` rule now forbids `http.Error` (matched by type, so a renamed `net/http` import is caught too), and an `exclusions` rule with `path-except: ^internal/controller/api/` keeps it off every other package. The proof is a temporary call in each of two packages: before the change the linter reports nothing; after it, exactly the API package's call.

- [ ] **Step 1: Write the failing check**

The check is the linter itself. Create two temporary probe files; they are never committed and Step 4 deletes them. They are the one exception to the rule that a task edits only its `**Files:**` block, and the probe under `internal/controller/listen/` is the one exception to Part C's package list; `git status --short` after Step 4 shows that neither remains.

Create `internal/controller/api/zz_forbidigo_probe.go`:

```go
package api

import "net/http"

// probeHTTPError is a temporary call the forbidigo rule must report.
func probeHTTPError(w http.ResponseWriter) { http.Error(w, "probe", http.StatusTeapot) }

var _ = probeHTTPError
```

Create `internal/controller/listen/zz_forbidigo_probe.go`:

```go
package listen

import "net/http"

// probeHTTPError is a temporary call outside the API the rule must ignore.
func probeHTTPError(w http.ResponseWriter) { http.Error(w, "probe", http.StatusTeapot) }

var _ = probeHTTPError
```

- [ ] **Step 2: Run the check to verify it fails**

Run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...`

Expected: FAIL — golangci-lint prints `0 issues.` and exits 0: no rule reports the API probe's `http.Error` call yet.

- [ ] **Step 3: Write the configuration**

Replace the whole content of `.golangci.yml` with:

```yaml
version: "2"

run:
  build-tags:
    - harness

linters:
  default: standard
  enable:
    - errorlint
    - forbidigo
    - gosec
    - misspell
    - revive
  settings:
    forbidigo:
      analyze-types: true
      forbid:
        # Every API refusal goes through writeProblem, which answers RFC 9457
        # problem details with a registered code; http.Error answers text.
        - pattern: ^http\.Error$
          pkg: ^net/http$
          msg: answer refusals with writeProblem
  exclusions:
    rules:
      - path-except: ^internal/controller/api/
        linters:
          - forbidigo

formatters:
  enable:
    - gofmt
    - goimports
```

Run `timeout 120 golangci-lint config verify` — Expected: no output, exit 0.

- [ ] **Step 4: Run the check and the checks**

Run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` with the probes still present — Expected: exit 1 with exactly this report (the probe in `internal/controller/listen` is not reported):

```text
internal/controller/api/zz_forbidigo_probe.go:6:46: use of `http.Error` forbidden because "answer refusals with writeProblem" (forbidigo)
func probeHTTPError(w http.ResponseWriter) { http.Error(w, "probe", http.StatusTeapot) }
                                             ^
1 issues:
* forbidigo: 1
```

Delete both probes: `rm internal/controller/api/zz_forbidigo_probe.go internal/controller/listen/zz_forbidigo_probe.go`, and confirm `git status --short` lists only `.golangci.yml`.

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add .golangci.yml
git commit -m "chore(lint): forbid http.Error in the API package"
```

### Task 4: Redact adapter for the lifecycle engine

**Files:**
- Create: `internal/core/schema/redact.go`
- Test: `internal/core/schema/redact_test.go`

**Interfaces:**
- Consumes: `schema.Output` (plan 2), `secret.RedactJSON`, `secret.Redacted`; `coreactions.Enrollment`, `coreactions.NewToken`, `coreactions.AuditList`, `coreactions.AuditEntry`, `coreactions.AuditInput`, `auth.User`, `auth.TokenInfo` (plan 3, test only)
- Produces: Contracts › `internal/core/schema` (`Redact`)

**Items:** 50, 8

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`schema.Redact` is the one `lifecycle.Config.Redact` both engines use (register row 50): `secret.RedactJSON` over the type's output schema, failing closed to `"[redacted]"` when there is no schema. The test feeds it the real outputs that reach read-scope callers (the TOTP secret of `core.user.create` and `core.user.reset-totp`, the token of `core.token.create`), a nested and an array case, and an audit list whose recorded inputs must pass unchanged; a second test proves a missing schema and invalid JSON never return the raw value.

- [ ] **Step 1: Write the failing test**

Create `internal/core/schema/redact_test.go`:

```go
package schema_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

type redactLeaf struct {
	Name string `json:"name"`
	Key  string `json:"key" secret:"true"`
}

type redactTree struct {
	Label string       `json:"label"`
	Inner redactLeaf   `json:"inner"`
	Items []redactLeaf `json:"items"`
}

func encode(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestRedact(t *testing.T) {
	created := time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)
	user := auth.User{ID: "01U", Username: "alice", Role: auth.RoleAdmin, State: auth.StatePending, CreatedAt: created}
	token := auth.TokenInfo{ID: "01T", Name: "ci", Scope: "read", CreatedAt: created, ExpiresAt: created.Add(time.Hour)}
	cases := []struct {
		name string
		t    reflect.Type
		in   any
		want string
	}{
		{
			// core.user.create and core.user.reset-totp return an Enrollment.
			"enrollment", reflect.TypeFor[coreactions.Enrollment](),
			coreactions.Enrollment{User: user, TOTPSecret: "JBSWY3DPEHPK3PXP"}, //nolint:gosec // G101: a test value, not a credential
			`{"totpSecret":"[redacted]","user":{"createdAt":"2026-10-17T12:00:00Z","id":"01U","role":"admin","state":"pending","username":"alice"}}`,
		},
		{
			// core.token.create returns a NewToken; the embedded TokenInfo is flattened.
			"new token", reflect.TypeFor[coreactions.NewToken](),
			coreactions.NewToken{TokenInfo: token, Token: "dpt_01T_SECRETSECRET"}, //nolint:gosec // G101: a test value, not a credential
			`{"createdAt":"2026-10-17T12:00:00Z","expiresAt":"2026-10-17T13:00:00Z","id":"01T","lastUsedAt":null,` +
				`"name":"ci","revokedAt":null,"scope":"read","token":"[redacted]"}`,
		},
		{
			"nested and array", reflect.TypeFor[redactTree](),
			redactTree{Label: "l", Inner: redactLeaf{Name: "a", Key: "k1"}, Items: []redactLeaf{{Name: "b", Key: "k2"}, {Name: "c", Key: "k3"}}},
			`{"inner":{"key":"[redacted]","name":"a"},"items":[{"key":"[redacted]","name":"b"},{"key":"[redacted]","name":"c"}],"label":"l"}`,
		},
		{
			// The audit list's inputs were redacted when recorded and pass unchanged.
			"audit list", reflect.TypeFor[coreactions.AuditList](),
			coreactions.AuditList{Entries: []coreactions.AuditEntry{{
				ID: "01A", At: created, Actor: "user:01U", Action: "core.token.create", Version: 1, Phase: "apply",
				Input: coreactions.AuditInput(`{"name":"ci","nested":{"password":"[redacted]"}}`), Result: "ok",
			}}},
			`{"entries":[{"action":"core.token.create","actor":"user:01U","at":"2026-10-17T12:00:00Z","changeId":null,` +
				`"errorCode":null,"hostId":null,"id":"01A","input":{"name":"ci","nested":{"password":"[redacted]"}},` +
				`"phase":"apply","result":"ok","source":null,"version":1}],"nextBefore":null}`,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := schema.Redact(c.t, encode(t, c.in)); string(got) != c.want {
				t.Errorf("Redact =\n%s\nwant\n%s", got, c.want)
			}
		})
	}
}

func TestRedactFailsClosed(t *testing.T) {
	// schema.Output fails for a nil type: nothing of the value may come back.
	if got := schema.Redact(nil, json.RawMessage(`{"token":"dpt_x"}`)); string(got) != `"[redacted]"` {
		t.Errorf("Redact(nil) = %s, want \"[redacted]\"", got)
	}
	if got := schema.Redact(reflect.TypeFor[coreactions.NewToken](), json.RawMessage(`{"token":`)); string(got) != `"[redacted]"` {
		t.Errorf("Redact(invalid JSON) = %s, want \"[redacted]\"", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRedact' ./internal/core/schema/`

Expected: FAIL — the build reports `undefined: schema.Redact`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/core/schema_test [github.com/darkraise/darkpanel/internal/core/schema.test]
internal/core/schema/redact_test.go:76:21: undefined: schema.Redact
internal/core/schema/redact_test.go:85:19: undefined: schema.Redact
internal/core/schema/redact_test.go:88:19: undefined: schema.Redact
FAIL	github.com/darkraise/darkpanel/internal/core/schema [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/redact.go`:

```go
package schema

import (
	"encoding/json"
	"reflect"

	"github.com/darkraise/darkpanel/internal/core/secret"
)

// Redact returns raw, a JSON value of type t, with every field tagged
// secret:"true" replaced by "[redacted]" (spec section 5). It is the
// lifecycle engine's Config.Redact on both sides, for inputs, change outputs
// and read outputs alike. When t has no output schema the whole value is
// redacted, never returned as is.
func Redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := Output(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRedact' ./internal/core/schema/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/core/schema`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/redact.go internal/core/schema/redact_test.go
git commit -m "feat(schema): add Redact adapter for the engine"
```


---

### Task 5: Journal source column

**Files:**
- Modify: `internal/core/contract.go`
- Modify: `internal/core/lifecycle/journal.go`
- Create: `internal/core/store/migrations/agent/0004_journal_source.sql`
- Test: `internal/core/lifecycle/journal_test.go` (modify)

**Interfaces:**
- Consumes: `core.JournalEntry`, `lifecycle.AgentJournal` (plan 2); the lifecycle test helpers `openDB`, `appendEntries`, `entry` (`journal_test.go`)
- Produces: Contracts › `internal/core` (`JournalEntry.Source`) and `internal/core/lifecycle` Task 5

**Items:** 48

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Register row 48: audit rows must carry the client address, and nothing in the journal held it. This task adds `Source` as the last field of `core.JournalEntry` (additive), a `source` column to the agent's `core_journal` in its own migration, and stores and returns it in the agent journal. The test appends an entry with and without a source and reads both back, and checks the column stores an empty string for a system entry.

- [ ] **Step 1: Write the failing test**

In `internal/core/lifecycle/journal_test.go`, make this replacement (the block occurs exactly once):

1. Replace this block:

```go
		t.Errorf("overflow entry = %+v, want interrupt/error/journal.overflow counting 3 dropped", m)
	}
}
```

with:

```go
		t.Errorf("overflow entry = %+v, want interrupt/error/journal.overflow counting 3 dropped", m)
	}
}

func TestJournalKeepsSource(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	fromSocket := entry("c1")
	fromSocket.Source = "socket"
	appendEntries(t, db, j, fromSocket, entry("c2"))

	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Source != "socket" || got[1].Source != "" {
		t.Fatalf("entries = %+v, want sources socket and empty", got)
	}
	var source string
	if err := db.Read().QueryRow("SELECT source FROM core_journal WHERE change_id = 'c2'").Scan(&source); err != nil {
		t.Fatalf("read source column: %v", err)
	}
	if source != "" {
		t.Errorf("stored source of an entry without one = %q, want empty", source)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJournalKeepsSource' ./internal/core/lifecycle/`

Expected: FAIL — the build reports `fromSocket.Source undefined (type core.JournalEntry has no field or method Source)`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/core/lifecycle [github.com/darkraise/darkpanel/internal/core/lifecycle.test]
internal/core/lifecycle/journal_test.go:194:13: fromSocket.Source undefined (type core.JournalEntry has no field or method Source)
internal/core/lifecycle/journal_test.go:201:29: got[0].Source undefined (type core.JournalEntry has no field or method Source)
internal/core/lifecycle/journal_test.go:201:58: got[1].Source undefined (type core.JournalEntry has no field or method Source)
FAIL	github.com/darkraise/darkpanel/internal/core/lifecycle [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/agent/0004_journal_source.sql`:

```sql
-- The client address of the principal behind each journal entry (spec
-- section 11: audit rows carry the source). System actors, such as the revert
-- timer and recovery, record an empty source.

ALTER TABLE core_journal ADD COLUMN source TEXT NOT NULL DEFAULT '';
```

In `internal/core/contract.go`, make this replacement (the block occurs exactly once):

1. Replace this block:

```go
	Result    string          // "ok", "error"
	ErrorCode string
}

```

with:

```go
	Result    string          // "ok", "error"
	ErrorCode string
	Source    string // the acting principal's Source; empty for system actors
}

```

In `internal/core/lifecycle/journal.go`, make these 3 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
func insertEntry(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO core_journal (at, actor, action, version, phase, change_id, input_json, result, error_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(e.At), e.Actor, e.Action, e.Version, e.Phase, e.ChangeID, nullJSON(e.Input), e.Result, e.ErrorCode)
	if err != nil {
		return fmt.Errorf("lifecycle: append journal: %w", err)
```

with:

```go
func insertEntry(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO core_journal (at, actor, action, version, phase, change_id, input_json, result, error_code, source)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(e.At), e.Actor, e.Action, e.Version, e.Phase, e.ChangeID, nullJSON(e.Input), e.Result, e.ErrorCode,
		e.Source)
	if err != nil {
		return fmt.Errorf("lifecycle: append journal: %w", err)
```

2. Replace this block:

```go
	}
	rows, err := j.db.Read().QueryContext(ctx,
		`SELECT seq, at, actor, action, version, phase, change_id, input_json, result, error_code
		 FROM core_journal WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
```

with:

```go
	}
	rows, err := j.db.Read().QueryContext(ctx,
		`SELECT seq, at, actor, action, version, phase, change_id, input_json, result, error_code, source
		 FROM core_journal WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
```

3. Replace this block:

```go
		var input sql.NullString
		if err := rows.Scan(&e.Seq, &at, &e.Actor, &e.Action, &e.Version, &e.Phase, &e.ChangeID,
			&input, &e.Result, &e.ErrorCode); err != nil {
			return nil, fmt.Errorf("lifecycle: read journal: %w", err)
		}
```

with:

```go
		var input sql.NullString
		if err := rows.Scan(&e.Seq, &at, &e.Actor, &e.Action, &e.Version, &e.Phase, &e.ChangeID,
			&input, &e.Result, &e.ErrorCode, &e.Source); err != nil {
			return nil, fmt.Errorf("lifecycle: read journal: %w", err)
		}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJournalKeepsSource' ./internal/core/lifecycle/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/lifecycle/ ./internal/core/store/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/contract.go internal/core/lifecycle/journal.go internal/core/store/migrations/agent/0004_journal_source.sql internal/core/lifecycle/journal_test.go
git commit -m "feat(lifecycle): record the source in the journal"
```


---

### Task 6: Password reset revokes the user's API tokens

**Files:**
- Modify: `internal/controller/auth/service.go`
- Test: `internal/controller/auth/service_test.go`

**Interfaces:**
- Consumes: the merged `auth.Service` (plan 3 Contracts › Task 4 `ResetPassword`, `endSessions`, `formatTime`; Task 6 `CreateToken`, `RevokeToken`, `GetToken`; Task 7 `Authenticate`); test helpers `newTestService`, `createActive`, `wantCode`.
- Produces: Contracts › Task 6 (`ResetPassword` revokes tokens).

**Items:** 66

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Owner decision of 2026-10-18 (register row 66): a token that leaked with the password must not survive the reset. `ResetPassword` sets `revoked_at` to now on every token of the user whose `revoked_at` is NULL, in the same write transaction that stores the hash and ends the sessions. The test proves a live token is revoked at the reset time and no longer authenticates, an already revoked token keeps its first `revokedAt`, and another user's token is untouched.

- [ ] **Step 1: Write the failing test**

In `internal/controller/auth/service_test.go`, make these 2 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
```

with:

```go
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
```

2. Replace:

```go
	wantCode(t, err, errcode.NotFound)
}

func TestDeleteUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
```

with:

```go
	wantCode(t, err, errcode.NotFound)
}

func TestResetPasswordRevokesTokens(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	alice, _ := createActive(t, s, clock, "alice")
	bob, _ := createActive(t, s, clock, "bob")
	live, liveToken, err := s.CreateToken(ctx, alice.ID, "live", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	early, _, err := s.CreateToken(ctx, alice.ID, "early", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevokeToken(ctx, alice.ID, early.ID); err != nil {
		t.Fatal(err)
	}
	firstRevoked := clock.Now()
	bobs, bobsToken, err := s.CreateToken(ctx, bob.ID, "bob", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(time.Minute)
	if _, err := s.ResetPassword(ctx, "alice", "a different long password"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetToken(ctx, alice.ID, live.ID)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(clock.Now()) {
		t.Errorf("live token after reset = %+v, %v; want revoked at %s", got, err, clock.Now())
	}
	got, err = s.GetToken(ctx, alice.ID, early.ID)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(firstRevoked) {
		t.Errorf("revoked token after reset = %+v, %v; want its first revocation %s kept", got, err, firstRevoked)
	}
	_, err = s.Authenticate(ctx, Credentials{Authorization: []string{"Bearer " + liveToken}})
	wantCode(t, err, errcode.AuthRequired)
	if got, err := s.GetToken(ctx, bob.ID, bobs.ID); err != nil || got.RevokedAt != nil {
		t.Errorf("another user's token = %+v, %v; want it untouched", got, err)
	}
	if _, err := s.Authenticate(ctx, Credentials{Authorization: []string{"Bearer " + bobsToken}}); err != nil {
		t.Errorf("another user's token no longer authenticates: %v", err)
	}
}

func TestDeleteUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestResetPassword' ./internal/controller/auth/`

Expected: FAIL — `--- FAIL: TestResetPasswordRevokesTokens` with `service_test.go:277: live token after reset = {ID:… Name:live Scope:read … RevokedAt:<nil>}, <nil>; want revoked at 2026-10-17 00:02:00 +0000 UTC` and `service_test.go:284: error = <nil>, want auth.required`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/auth/service.go`, make these 2 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
	return u, encodeTOTPSecret(key), nil
}

// ResetPassword sets username's password and ends its sessions. An invalid
// password is request.invalid_input.
func (s *Service) ResetPassword(ctx context.Context, username, password string) (User, error) {
	if err := checkPassword(password); err != nil {
		return User{}, err
```

with:

```go
	return u, encodeTOTPSecret(key), nil
}

// ResetPassword sets username's password, ends its sessions and revokes its
// API tokens, since a token may have leaked with the password (owner
// decision). A token already revoked keeps its first revocation time. An invalid password is request.invalid_input.
func (s *Service) ResetPassword(ctx context.Context, username, password string) (User, error) {
	if err := checkPassword(password); err != nil {
		return User{}, err
```

2. Replace:

```go
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: reset password: %w", err)
		}
		u = r.User
		return endSessions(ctx, tx, r.ID)
```

with:

```go
		if err != nil {
			return err
		}
		now := formatTime(s.now())
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, now, r.ID); err != nil {
			return fmt.Errorf("auth: reset password: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_tokens SET revoked_at = ? WHERE user_id = ? AND revoked_at IS NULL`,
			now, r.ID); err != nil {
			return fmt.Errorf("auth: revoke tokens: %w", err)
		}
		u = r.User
		return endSessions(ctx, tx, r.ID)
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestResetPassword|TestResetTOTP|TestDeleteUser' ./internal/controller/auth/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/auth`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/service.go internal/controller/auth/service_test.go
git commit -m "fix(auth): revoke API tokens on password reset"
```

### Task 7: Engine records the acting principal's source

**Files:**
- Modify: `internal/core/lifecycle/apply.go`
- Modify: `internal/core/lifecycle/revert.go`
- Test: `internal/core/lifecycle/source_test.go`

**Interfaces:**
- Consumes: Contracts › Task 5 (`JournalEntry.Source`); the lifecycle fixture (`newFixture`, `alice`, `refSet`, `refAdd`, `f.plan`, `f.restart`, `f.recoverAll`)
- Produces: Contracts › `internal/core/lifecycle` Task 7

**Items:** 48

**Depends on:** Task 5

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

Every journal entry the engine writes now carries the caller's `Source`, so the controller audit log (through `Journal.Append`) and the agent journal (through ingestion) record where an apply, confirm or revert came from; system actors leave it empty. The test runs one change through each transition (confirm, revert, failed apply, timer revert, recovery) and checks every entry's phase, actor and source.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/source_test.go`:

```go
package lifecycle

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestJournalEntriesCarryTheSource(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	bob := core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.7"}

	confirmed := f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID
	v, err := f.eng.Apply(ctx, alice, confirmed, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Confirm(ctx, bob, v.ID); err != nil {
		t.Fatal(err)
	}
	reverted := f.plan(alice, refSet, `{"name":"b","value":"1"}`).PlanID
	if v, err = f.eng.Apply(ctx, alice, reverted, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Revert(ctx, bob, v.ID); err != nil {
		t.Fatal(err)
	}
	failed := f.plan(alice, refAdd, `{"name":"c","value":"1","failApply":true}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, failed, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	timed := f.plan(alice, refSet, `{"name":"d","value":"1"}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, timed, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(DefaultConfirmTimeout)
	left := f.plan(alice, refSet, `{"name":"e","value":"1"}`).PlanID
	if _, err := f.eng.Apply(ctx, alice, left, core.ApplyOptions{}); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()

	entries, err := f.journal.Entries(ctx, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.Actor+"/"+e.Source)
	}
	want := []string{
		"apply/user:alice/test", "confirm/user:bob/192.0.2.7",
		"apply/user:alice/test", "revert/user:bob/192.0.2.7",
		"apply/user:alice/test",
		"apply/user:alice/test", "revert/system:revert-timer/",
		"apply/user:alice/test", "recover/system:recovery/",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("journal phase/actor/source =\n%v\nwant\n%v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJournalEntriesCarryTheSource' ./internal/core/lifecycle/`

Expected: FAIL — the test fails because every entry's source is empty. The output begins:

```text
--- FAIL: TestJournalEntriesCarryTheSource (0.01s)
    source_test.go:63: journal phase/actor/source =
        [apply/user:alice/ confirm/user:bob/ apply/user:alice/ revert/user:bob/ apply/user:alice/ apply/user:alice/ revert/system:revert-timer/ apply/user:alice/ recover/system:recovery/]
        want
        [apply/user:alice/test confirm/user:bob/192.0.2.7 apply/user:alice/test revert/user:bob/192.0.2.7 apply/user:alice/test apply/user:alice/test revert/system:revert-timer/ apply/user:alice/test recover/system:recovery/]
FAIL
FAIL	github.com/darkraise/darkpanel/internal/core/lifecycle	0.007s
FAIL
```

- [ ] **Step 3: Write the implementation**

In `internal/core/lifecycle/apply.go`, make these 4 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
	v, err := e.transition(ctx, it.id, transition{
		from: core.StateApplying, to: to, output: e.cfg.Redact(it.def.OutputType, out),
		release: !revertible, phase: phaseApply, result: resultOK, actor: p.Actor,
	})
	if err != nil {
```

with:

```go
	v, err := e.transition(ctx, it.id, transition{
		from: core.StateApplying, to: to, output: e.cfg.Redact(it.def.OutputType, out),
		release: !revertible, phase: phaseApply, result: resultOK, actor: p.Actor, source: p.Source,
	})
	if err != nil {
```

2. Replace this block:

```go
	tr := transition{
		from: core.StateApplying, to: core.StateFailed, err: e.changeError(it.def.Ref(), cause),
		release: true, phase: phaseApply, result: resultError, actor: p.Actor,
	}
	if it.def.Spec.Class == core.ClassRevertible {
```

with:

```go
	tr := transition{
		from: core.StateApplying, to: core.StateFailed, err: e.changeError(it.def.Ref(), cause),
		release: true, phase: phaseApply, result: resultError, actor: p.Actor, source: p.Source,
	}
	if it.def.Spec.Class == core.ClassRevertible {
```

3. Replace this block:

```go
	result   string
	actor    string
}

```

with:

```go
	result   string
	actor    string
	source   string // the acting principal's Source; empty for system actors
}

```

4. Replace this block:

```go
		entry := core.JournalEntry{
			At: now, Actor: tr.actor, Action: v.Action, Version: v.Version, Phase: tr.phase,
			ChangeID: changeID, Input: json.RawMessage(input), Result: tr.result,
		}
		if tr.err != nil {
```

with:

```go
		entry := core.JournalEntry{
			At: now, Actor: tr.actor, Action: v.Action, Version: v.Version, Phase: tr.phase,
			ChangeID: changeID, Input: json.RawMessage(input), Result: tr.result, Source: tr.source,
		}
		if tr.err != nil {
```

Replace the whole of `internal/core/lifecycle/revert.go` with:

```go
package lifecycle

import (
	"context"
	"errors"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRevertTimer is the actor of a revert that a confirm timer triggers.
const ActorRevertTimer = "system:revert-timer"

var timerPrincipal = core.Principal{Actor: ActorRevertTimer, Scope: core.ScopeAdmin}

// Confirm keeps a pending revertible change and releases its locks. Any
// principal may confirm; the caller authorizes it. Confirm and revert race in
// one transaction each, and the one that commits second gets
// change.not_pending.
func (e *Engine) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	v, err := e.transition(ctx, changeID, transition{
		from: core.StatePending, to: core.StateConfirmed, release: true,
		phase: phaseConfirm, result: resultOK, actor: p.Actor, source: p.Source,
	})
	if err != nil {
		return v, err
	}
	e.stopTimer(changeID)
	return v, nil
}

// Revert reverts a pending revertible change from its snapshot. Any principal
// may revert; the caller authorizes it.
func (e *Engine) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return e.revertPending(context.WithoutCancel(ctx), p, changeID)
}

// revertPending claims a pending change by moving it back to applying, so a
// racing confirm loses and a crash during the revert is recovered from the
// snapshot at the next start; then it reverts and records the outcome.
func (e *Engine) revertPending(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	if v, err := e.transition(ctx, changeID, transition{from: core.StatePending, to: core.StateApplying}); err != nil {
		return v, err
	}
	e.stopTimer(changeID)
	tr := transition{
		from: core.StateApplying, to: core.StateReverted, release: true,
		phase: phaseRevert, result: resultOK, actor: p.Actor, source: p.Source,
	}
	if err := e.runRevert(ctx, p, changeID); err != nil {
		e.cfg.Base.Log.Error("lifecycle: revert", "change", changeID, "error", err)
		tr.to, tr.err, tr.result = core.StateFailed, revertFailed(), resultError
	}
	return e.transition(ctx, changeID, tr)
}

// armTimer reverts the pending change after d unless it is confirmed or
// reverted first. Deadlines run on the clock's monotonic timers (ADR 0004).
func (e *Engine) armTimer(changeID string, d time.Duration) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if e.closed {
		return
	}
	e.timers[changeID] = e.cfg.Base.Clock.AfterFunc(d, func() {
		e.timerMu.Lock()
		if e.closed {
			e.timerMu.Unlock()
			return
		}
		delete(e.timers, changeID)
		e.inflight.Add(1)
		e.timerMu.Unlock()
		defer e.inflight.Done()
		_, err := e.revertPending(context.Background(), timerPrincipal, changeID)
		var coded *errcode.Error
		if err != nil && (!errors.As(err, &coded) || coded.Code != errcode.ChangeNotPending) {
			e.cfg.Base.Log.Error("lifecycle: revert timer", "change", changeID, "error", err)
		}
	})
}

func (e *Engine) stopTimer(changeID string) {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	if t, ok := e.timers[changeID]; ok {
		t.Stop()
		delete(e.timers, changeID)
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestJournalEntriesCarryTheSource' ./internal/core/lifecycle/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/apply.go internal/core/lifecycle/revert.go internal/core/lifecycle/source_test.go
git commit -m "feat(lifecycle): journal the principal's source"
```


---

### Task 8: Command naming and clash rules

**Files:**
- Create: `internal/cli/names.go`
- Test: `internal/cli/names_test.go`
- Test: `internal/cli/fixture_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (package doc comment); `core.Registry` (`Actions`, `Resources`), `core.ActionDef`, `core.ResourceDef`, `core.ModuleIDCore`, `core.NewRegistry`, `core.DefineRead`, `core.DefineMutating`, `core.DefineRevertible`, `core.DefineResource` (merged `internal/core`); `coreactions.Module(coreactions.Deps{})` in the fixture (merged).
- Produces: Contracts › Task 8 (`CommandPath`, `handWritten`, `entry`, `entries`, `checkEntries`; test fixture `fixtureHandler`, `demoModule` and its types, `testRegistry`).

**Items:** 31

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Spec section 15 naming: `<module>.<noun>.<verb>` is `darkpanel <module> <noun> <verb>`, a core action drops `core`, and resource `<module>.<resource>` is `darkpanel <module> <resource> get`, so `demo rule get` and `demo rule set` share one noun group (register row 31). Each action is generated once, at its highest version. Generation fails when two commands share a path, when one command's path is another's group, or when a path starts with a hand-written top-level name (`serve`, `version`, `change`, `controller`, `help`, `completion`, plus the assembly's own), so a future core noun cannot shadow them. The fixture module (`demo`) has agent-side read, compensable, irreversible and revertible actions, two versions of one action, a nested input, secret inputs and a secret output, and resources sharing a noun with actions; `testRegistry` adds the real core module as `registry.Build` builds it.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/names_test.go`:

```go
package cli

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestCommandPath(t *testing.T) {
	cases := map[string]string{
		"demo.item.get":        "demo item get",
		"demo.rule.set":        "demo rule set",
		"demo.rule":            "demo rule get",
		"core.host.list":       "host list",
		"core.user.reset-totp": "user reset-totp",
		"core.status":          "status get",
	}
	for name, want := range cases {
		if got := strings.Join(CommandPath(name), " "); got != want {
			t.Errorf("CommandPath(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestEntries(t *testing.T) {
	es, err := entries(testRegistry(t), handWritten)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]entry{}
	var paths []string
	for _, e := range es {
		p := strings.Join(e.Path, " ")
		got[p] = e
		paths = append(paths, p)
	}
	want := []string{
		"audit list",
		"demo item add", "demo item get", "demo item list", "demo item remove",
		"demo rule get", "demo rule set", "demo status get",
		"host list",
		"module disable", "module enable", "module list",
		"token create", "token list", "token revoke",
		"user activate", "user create", "user delete", "user list", "user reset-password", "user reset-totp",
	}
	if !slices.Equal(paths, want) {
		t.Errorf("paths\n%q\nwant\n%q", paths, want)
	}
	if e := got["demo item get"]; e.Action == nil || e.Action.Spec.Version != 2 || e.Name != "demo.item.get" {
		t.Errorf("demo item get is %+v, want demo.item.get version 2", e)
	}
	if e := got["demo rule get"]; e.Resource == nil || e.Action != nil || e.Name != "demo.rule" {
		t.Errorf("demo rule get is %+v, want resource demo.rule", e)
	}
	if e := got["user create"]; e.Action == nil || e.Name != "core.user.create" || !e.Action.Spec.BreakGlass {
		t.Errorf("user create is %+v, want the break-glass core.user.create", e)
	}
}

// fakeCore is a core module with the given read actions, standing in for a
// future core noun.
type fakeCore []string

func (f fakeCore) Manifest() core.Manifest {
	m := core.Manifest{ID: core.ModuleIDCore, Version: 1}
	for _, name := range f {
		m.Actions = append(m.Actions, core.DefineRead(core.ActionSpec{Name: name, Version: 1, Side: core.SideController},
			fixtureHandler[demoNoInput, demoNoInput]{}))
	}
	return m
}

func TestEntriesRefuseHandWrittenNames(t *testing.T) {
	for _, noun := range []string{"change", "controller", "serve", "version", "help", "completion", "agent"} {
		reg, err := core.NewRegistry(fakeCore{"core." + noun + ".list"})
		if err != nil {
			t.Fatal(err)
		}
		_, err = entries(reg, append(slices.Clone(handWritten), "agent"))
		if err == nil || !strings.Contains(err.Error(), `hand-written command "`+noun+`"`) {
			t.Errorf("core.%s.list: error %v, want a clash with the hand-written %q", noun, err, noun)
		}
	}
}

func TestCheckEntriesRefusesClashes(t *testing.T) {
	read := core.DefineRead(core.ActionSpec{Name: "demo.rule.get", Version: 1, Side: core.SideAgent},
		fixtureHandler[demoNoInput, demoNoInput]{})
	res := core.DefineResource("demo.rule", func(context.Context, core.Env) (demoRule, error) { return demoRule{}, nil })
	same := []entry{
		{Name: "demo.rule.get", Path: CommandPath("demo.rule.get"), Action: &read},
		{Name: "demo.rule", Path: CommandPath("demo.rule"), Resource: &res},
	}
	if err := checkEntries(same, handWritten); err == nil || !strings.Contains(err.Error(), `"darkpanel demo rule get"`) {
		t.Errorf("same path: error %v", err)
	}
	prefix := []entry{
		{Name: "core.demo.rule", Path: []string{"demo", "rule"}, Action: &read},
		{Name: "demo.rule.get", Path: CommandPath("demo.rule.get"), Action: &read},
	}
	if err := checkEntries(prefix, handWritten); err == nil || !strings.Contains(err.Error(), "is also a command group") {
		t.Errorf("prefix: error %v", err)
	}
}
```

Create `internal/cli/fixture_test.go`:

```go
package cli

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
)

// fixtureHandler satisfies every handler interface. The CLI never runs a
// handler; it only reads the declarations.
type fixtureHandler[In, Out any] struct{}

func (fixtureHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (fixtureHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (fixtureHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (fixtureHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) {
	return nil, nil
}

func (fixtureHandler[In, Out]) Revert(context.Context, core.Env, []byte) error { return nil }

type demoNoInput struct{}

type demoItemQuery struct {
	ID      string `json:"id" jsonschema_description:"Item id."`
	Verbose bool   `json:"verbose,omitempty" jsonschema_description:"Include every detail."`
}

type demoItem struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type demoItems struct {
	Items []demoItem `json:"items"`
}

type demoAddInput struct {
	Name       string `json:"name" jsonschema_description:"Item name."`
	APIKey     string `json:"apiKey" secret:"true" jsonschema_description:"The upstream API key."`
	Passphrase string `json:"passphrase" secret:"true" jsonschema_description:"The key's passphrase."`
}

type demoAdded struct {
	ID    string `json:"id"`
	Token string `json:"token" secret:"true"`
}

type demoIDInput struct {
	ID string `json:"id" jsonschema_description:"Item id."`
}

type demoLimits struct {
	Max   int `json:"max"`
	Burst int `json:"burst,omitempty"`
}

type demoRuleInput struct {
	Name    string     `json:"name" jsonschema_description:"Rule name."`
	Port    int        `json:"port" jsonschema_description:"Port number."`
	Enabled bool       `json:"enabled,omitempty" jsonschema_description:"Whether the rule is on."`
	Weight  float64    `json:"weight,omitempty" jsonschema_description:"Relative weight."`
	Mode    string     `json:"mode,omitempty" jsonschema:"enum=allow,enum=deny" jsonschema_description:"allow or deny."`
	Ports   []int      `json:"ports,omitempty" jsonschema_description:"Extra ports."`
	Tags    []string   `json:"tags,omitempty" jsonschema_description:"Labels."`
	Limits  demoLimits `json:"limits" jsonschema_description:"Rate limits."`
}

type demoRule struct {
	Name string `json:"name"`
	Port int    `json:"port"`
}

type demoStatus struct {
	Healthy bool `json:"healthy"`
	Rules   int  `json:"rules"`
}

// demoModule is module "demo": agent-side read, compensable, irreversible
// and revertible actions, two versions of demo.item.get, a nested input
// (demo.rule.set's limits), secret inputs and a secret output
// (demo.item.add), and resources sharing a noun with actions (demo.rule).
type demoModule struct{}

func (demoModule) Manifest() core.Manifest {
	agent := func(name string, version int, class core.Class) core.ActionSpec {
		return core.ActionSpec{Name: name, Version: version, Class: class, Side: core.SideAgent, Summary: "Demo " + name + "."}
	}
	add := agent("demo.item.add", 1, core.ClassCompensable)
	add.CompensatedBy = "demo.item.remove"
	return core.Manifest{
		ID: "demo", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRead(agent("demo.item.get", 1, core.ClassRead), fixtureHandler[demoItemQuery, demoItem]{}),
			core.DefineRead(agent("demo.item.get", 2, core.ClassRead), fixtureHandler[demoItemQuery, demoItem]{}),
			core.DefineRead(agent("demo.item.list", 1, core.ClassRead), fixtureHandler[demoNoInput, demoItems]{}),
			core.DefineMutating(add, fixtureHandler[demoAddInput, demoAdded]{}),
			core.DefineMutating(agent("demo.item.remove", 1, core.ClassIrreversible), fixtureHandler[demoIDInput, demoNoInput]{}),
			core.DefineRevertible(agent("demo.rule.set", 1, core.ClassRevertible), fixtureHandler[demoRuleInput, demoRule]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.rule", func(context.Context, core.Env) (demoRule, error) { return demoRule{}, nil }),
			core.DefineResource("demo.status", func(context.Context, core.Env) (demoStatus, error) { return demoStatus{}, nil }),
		},
	}
}

// testRegistry is the real core module, built as registry.Build builds it,
// plus demoModule.
func testRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(coreactions.Module(coreactions.Deps{}), demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestCommandPath|TestEntries|TestEntriesRefuseHandWrittenNames|TestCheckEntriesRefusesClashes)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/names_test.go:22:26: undefined: CommandPath`, then `undefined: entries`, `undefined: handWritten`, `undefined: entry`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/names.go`:

```go
package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
)

// handWritten are the top-level commands that no generated command may
// take: those this package writes, cobra's own, and those the assembly
// passes in Options.Commands (spec section 15).
var handWritten = []string{"serve", "version", "change", "controller", "help", "completion"}

// CommandPath is the words after "darkpanel" of the command generated for
// an action or resource name (spec section 15): action <module>.<noun>.<verb>
// is [<module> <noun> <verb>], resource <module>.<resource> is
// [<module> <resource> get], and module "core" drops its segment.
func CommandPath(name string) []string {
	words := strings.Split(name, ".")
	if len(words) == 2 {
		words = append(words, "get")
	}
	if words[0] == core.ModuleIDCore {
		words = words[1:]
	}
	return words
}

// entry is one generated command: an action at its highest version, or a
// resource.
type entry struct {
	Name     string
	Path     []string
	Action   *core.ActionDef
	Resource *core.ResourceDef
}

// entries returns the generated commands of reg sorted by path, or an error
// when two of them share a path, one's path is a group of another's, or one
// starts with a reserved top-level word.
func entries(reg *core.Registry, reserved []string) ([]entry, error) {
	latest := map[string]core.ActionDef{}
	for _, a := range reg.Actions() {
		latest[a.Spec.Name] = a // sorted by name and version, so the last one wins
	}
	var es []entry
	for name, a := range latest {
		es = append(es, entry{Name: name, Path: CommandPath(name), Action: &a})
	}
	for _, r := range reg.Resources() {
		es = append(es, entry{Name: r.Name, Path: CommandPath(r.Name), Resource: &r})
	}
	slices.SortFunc(es, func(a, b entry) int { return slices.Compare(a.Path, b.Path) })
	if err := checkEntries(es, reserved); err != nil {
		return nil, err
	}
	return es, nil
}

// checkEntries refuses entries whose commands would clash.
func checkEntries(es []entry, reserved []string) error {
	var errs []error
	owners := map[string]string{}
	for _, e := range es {
		p := strings.Join(e.Path, " ")
		if slices.Contains(reserved, e.Path[0]) {
			errs = append(errs, fmt.Errorf("%s: command \"darkpanel %s\" clashes with the hand-written command %q", e.Name, p, e.Path[0]))
		}
		if other, ok := owners[p]; ok {
			errs = append(errs, fmt.Errorf("%s and %s both map to \"darkpanel %s\"", other, e.Name, p))
		}
		owners[p] = e.Name
	}
	for _, e := range es {
		for i := 1; i < len(e.Path); i++ {
			if other, ok := owners[strings.Join(e.Path[:i], " ")]; ok {
				errs = append(errs, fmt.Errorf("\"darkpanel %s\" is the command of %s and is also a command group of %s",
					strings.Join(e.Path[:i], " "), other, e.Name))
			}
		}
	}
	return errors.Join(errs...)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestCommandPath|TestEntries|TestEntriesRefuseHandWrittenNames|TestCheckEntriesRefusesClashes)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/names.go internal/cli/names_test.go internal/cli/fixture_test.go
git commit -m "feat(cli): name generated commands and refuse clashes"
```

### Task 9: core.user.reset-password says it revokes tokens

**Files:**
- Modify: `internal/controller/coreactions/users.go`
- Test: `internal/controller/coreactions/users_test.go`

**Interfaces:**
- Consumes: Contracts › Task 6 (`ResetPassword` revokes tokens); the merged `core.user.reset-password` handler (plan 3 Contracts › Task 28); test helpers `newTestEnv`, `(e) plan`, `(e) apply`, `(e) verify`, `(e) login`, `noSecret`, `root`, `newPassword`.
- Produces: Contracts › Task 9 (diff line and summary of `core.user.reset-password`).

**Items:** 66

**Depends on:** Task 6

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

The plan diff of `core.user.reset-password` said the user's API tokens stay; after Task 6 they are revoked, so the line and the action's summary say so. The state hash stays `stateHash(u.ID)`: the diff names no token, so a token created or revoked between plan and apply changes nothing the plan showed, and the apply revokes whatever is live then. The test runs the action through the real engine and checks the new diff line and that the user's token is revoked.

- [ ] **Step 1: Write the failing test**

In `internal/controller/coreactions/users_test.go`, make these 3 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.ActivateUser(ctx, "alice", code); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)

	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.reset-password", in)
```

with:

```go
	if err != nil {
		t.Fatal(err)
	}
	alice, err := e.auth.ActivateUser(ctx, "alice", code)
	if err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	token, _, err := e.auth.CreateToken(ctx, alice.ID, "ci", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}

	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.reset-password", in)
```

2. Replace:

```go
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: current password"},
		{Op: "+", Text: "user alice: new password; its sessions end and its API tokens stay"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
```

with:

```go
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: current password"},
		{Op: "+", Text: "user alice: new password; its sessions end and its API tokens are revoked"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
```

3. Replace:

```go
		t.Fatalf("apply = %+v, %v", v, err)
	}
	noSecret(t, []string{newPassword, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.user.reset-password", in); err != nil {
		t.Errorf("verify: %v", err)
	}
```

with:

```go
		t.Fatalf("apply = %+v, %v", v, err)
	}
	noSecret(t, []string{newPassword, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if got, err := e.auth.GetToken(ctx, alice.ID, token.ID); err != nil || got.RevokedAt == nil {
		t.Errorf("token after reset-password = %+v, %v; want it revoked", got, err)
	}
	if err := e.verify("core.user.reset-password", in); err != nil {
		t.Errorf("verify: %v", err)
	}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestUserResetPassword' ./internal/controller/coreactions/`

Expected: FAIL — `--- FAIL: TestUserResetPassword` with `users_test.go:142: plan = {… Diff:[{Op:- Text:user alice: current password} {Op:+ Text:user alice: new password; its sessions end and its API tokens stay}] …}, want diff [… {Op:+ Text:user alice: new password; its sessions end and its API tokens are revoked}] and phrase alice`.

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/users.go`, make these 2 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
		}, userResetTOTP{m}),
		core.DefineMutating[UserPasswordInput, auth.User](core.ActionSpec{
			Name: "core.user.reset-password", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Set a user's password and end its sessions; the confirmation phrase is the username.",
		}, userResetPassword{m}),
		core.DefineMutating[UsernameInput, Empty](core.ActionSpec{
			Name: "core.user.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
```

with:

```go
		}, userResetTOTP{m}),
		core.DefineMutating[UserPasswordInput, auth.User](core.ActionSpec{
			Name: "core.user.reset-password", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Set a user's password, end its sessions and revoke its API tokens; the confirmation phrase is the username.",
		}, userResetPassword{m}),
		core.DefineMutating[UsernameInput, Empty](core.ActionSpec{
			Name: "core.user.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
```

2. Replace:

```go
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: current password", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: new password; its sessions end and its API tokens stay", u.Username)},
		},
		Guards:       []core.Guard{},
		StateHash:    stateHash(u.ID),
		Confirmation: u.Username,
	}, nil
```

with:

```go
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: current password", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: new password; its sessions end and its API tokens are revoked", u.Username)},
		},
		Guards: []core.Guard{},
		// The diff names no token, so tokens made or revoked after the plan
		// change nothing it shows: the apply revokes whatever is live then.
		StateHash:    stateHash(u.ID),
		Confirmation: u.Username,
	}, nil
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestUser' ./internal/controller/coreactions/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/coreactions/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/users.go internal/controller/coreactions/users_test.go
git commit -m "fix(coreactions): reset-password revokes tokens"
```

### Task 10: Handler panic policy

**Files:**
- Create: `internal/core/lifecycle/guard.go`
- Modify: `internal/core/lifecycle/engine.go`
- Modify: `internal/core/lifecycle/apply.go`
- Modify: `internal/core/lifecycle/recover.go`
- Test: `internal/core/lifecycle/fixture_test.go` (modify)
- Test: `internal/core/lifecycle/recover_test.go` (modify)
- Test: `internal/core/lifecycle/guard_test.go`

**Interfaces:**
- Consumes: the lifecycle engine and its fixture (plan 2; `f.apply`, `f.pending`, `f.state`, `f.count`, `f.crash`, `f.config`); Contracts › Task 7
- Produces: Contracts › `internal/core/lifecycle` Task 10 (`Protect`, the fixture's `CrashApply`, `panicOn`, `logged`)

**Items:** 52

**Depends on:** Task 7

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Register row 52's panic half: a panicking handler must not crash the process or leave the apply mutex locked, and must not log its input. `Protect` recovers a panic into a plain error (so the API answers `internal` and a change fails with code `internal`), logged once with the stack; the engine wraps every handler call in it. Because a panic is now recovered, the recovery tests' crash simulation moves from `panic` to `runtime.Goexit`, which still stops Apply after intent is persisted. The tests make each call panic in turn and check the outcome, that the panic is logged exactly once without the item name, and that the apply mutex is free afterwards.

- [ ] **Step 1: Write the failing test**

In `internal/core/lifecycle/fixture_test.go`, make these 11 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
	"path/filepath"
	"reflect"
	"strings"
	"sync"
```

with:

```go
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
```

2. Replace this block:

```go

// The fixture module "fix" is registered only in these tests. Its host is a
// map of named items; one handler serves every mutating class, and input
// flags make it fail, panic or refuse to revert.

const testHost = "host-1"
```

with:

```go

// The fixture module "fix" is registered only in these tests. Its host is a
// map of named items; one handler serves every mutating class, input flags
// make it fail, stop or refuse to revert, and panicOn makes a handler call
// panic.

const testHost = "host-1"
```

3. Replace this block:

```go
)

// errCrash is what a PanicApply handler panics with: it stands for the
// process dying between persisting intent and recording the result.
var errCrash = errors.New("simulated crash")

type fixHost struct {
	mu      sync.Mutex
```

with:

```go
)

type fixHost struct {
	mu      sync.Mutex
```

4. Replace this block:

```go
	// onSnapshot, when set, runs at the start of Snapshot without mu held.
	onSnapshot func()
}

```

with:

```go
	// onSnapshot, when set, runs at the start of Snapshot without mu held.
	onSnapshot func()
	// panicOn names the handler call ("Read", "Plan", "Apply", "Verify",
	// "Snapshot" or "Revert") that panics while set.
	panicOn string
}

// panicIf panics when call is panicOn. The value quotes the item name, as a
// careless handler's panic might quote its input.
func (h *fixHost) panicIf(call, name string) {
	h.mu.Lock()
	on := h.panicOn
	h.mu.Unlock()
	if on == call {
		panic("fixture " + call + " panicked on item " + name)
	}
}

```

5. Replace this block:

```go
	FailVerify bool   `json:"failVerify,omitempty"`
	FailRevert bool   `json:"failRevert,omitempty"`
	PanicApply bool   `json:"panicApply,omitempty"`
}

```

with:

```go
	FailVerify bool   `json:"failVerify,omitempty"`
	FailRevert bool   `json:"failRevert,omitempty"`
	CrashApply bool   `json:"crashApply,omitempty"`
}

```

6. Replace this block:

```go

func (x itemHandler) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
```

with:

```go

func (x itemHandler) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.panicIf("Plan", in.Name)
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
```

7. Replace this block:

```go

func (x itemHandler) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	if in.PanicApply {
		panic(errCrash)
	}
	if in.FailApply {
		return itemOut{}, errors.New("apply failed")
```

with:

```go

func (x itemHandler) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	// CrashApply stops the goroutine without a panic: it stands for the
	// process dying between persisting intent and recording the result.
	if in.CrashApply {
		runtime.Goexit()
	}
	x.h.panicIf("Apply", in.Name)
	if in.FailApply {
		return itemOut{}, errors.New("apply failed")
```

8. Replace this block:

```go

func (x itemHandler) Verify(_ context.Context, _ core.Env, in itemIn, _ itemOut) error {
	if in.FailVerify {
		return errcode.New(errcode.Internal, "verify failed")
```

with:

```go

func (x itemHandler) Verify(_ context.Context, _ core.Env, in itemIn, _ itemOut) error {
	x.h.panicIf("Verify", in.Name)
	if in.FailVerify {
		return errcode.New(errcode.Internal, "verify failed")
```

9. Replace this block:

```go
		x.h.onSnapshot()
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
```

with:

```go
		x.h.onSnapshot()
	}
	x.h.panicIf("Snapshot", in.Name)
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
```

10. Replace this block:

```go
		return err
	}
	if s.FailRevert {
		return errors.New("revert failed")
```

with:

```go
		return err
	}
	x.h.panicIf("Revert", s.Name)
	if s.FailRevert {
		return errors.New("revert failed")
```

11. Replace this block:

```go

func (g getHandler) Read(_ context.Context, env core.Env, in getIn) (getOut, error) {
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
```

with:

```go

func (g getHandler) Read(_ context.Context, env core.Env, in getIn) (getOut, error) {
	g.h.panicIf("Read", in.Name)
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
```

In `internal/core/lifecycle/recover_test.go`, make these 4 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
import (
	"context"
	"errors"
	"reflect"
	"strings"
```

with:

```go
import (
	"context"
	"reflect"
	"strings"
```

2. Replace this block:

```go
}

// crash plans and applies an input whose handler panics in Apply, which
// stands for the process dying after the intent is persisted and before the
// result is recorded. It returns the plan and change ids.
func (f *fixture) crash(ref core.ActionRef, input string, opts core.ApplyOptions) (planID, changeID string) {
	f.t.Helper()
	planID = f.plan(alice, ref, input).PlanID
	func() {
		defer func() {
			r := recover()
			if err, ok := r.(error); !ok || !errors.Is(err, errCrash) {
				f.t.Fatalf("recover() = %v, want the simulated crash", r)
			}
		}()
		_, _ = f.eng.Apply(context.Background(), alice, planID, opts)
	}()
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", planID).Scan(&changeID); err != nil {
		f.t.Fatal(err)
```

with:

```go
}

// crash plans and applies an input whose handler stops its goroutine in
// Apply, which stands for the process dying after the intent is persisted and
// before the result is recorded. It returns the plan and change ids.
func (f *fixture) crash(ref core.ActionRef, input string, opts core.ApplyOptions) (planID, changeID string) {
	f.t.Helper()
	planID = f.plan(alice, ref, input).PlanID
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		_, _ = f.eng.Apply(context.Background(), alice, planID, opts)
		f.t.Error("Apply returned; want its goroutine stopped inside the handler")
	}()
	<-stopped
	if err := f.db.Read().QueryRow("SELECT id FROM core_changes WHERE plan_id = ?", planID).Scan(&changeID); err != nil {
		f.t.Fatal(err)
```

3. Replace this block:

```go
	f := newFixture(t)
	f.host.items["a"] = "old"
	planID, id := f.crash(refSet, `{"name":"a","value":"1","panicApply":true}`, core.ApplyOptions{})
	if s := f.state(id); s != core.StateApplying || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id) != 1 {
		t.Fatalf("after the crash: state %s, want applying with its lock", s)
```

with:

```go
	f := newFixture(t)
	f.host.items["a"] = "old"
	planID, id := f.crash(refSet, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	if s := f.state(id); s != core.StateApplying || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id) != 1 {
		t.Fatalf("after the crash: state %s, want applying with its lock", s)
```

4. Replace this block:

```go
			f := newFixture(t)
			ctx := context.Background()
			planID, id := f.crash(c.ref, `{"name":"a","value":"1","panicApply":true}`, c.opts)
			if c.mutated {
				f.host.items["a"] = "1"
```

with:

```go
			f := newFixture(t)
			ctx := context.Background()
			planID, id := f.crash(c.ref, `{"name":"a","value":"1","crashApply":true}`, c.opts)
			if c.mutated {
				f.host.items["a"] = "1"
```

Create `internal/core/lifecycle/guard_test.go`:

```go
package lifecycle

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// item is the name every panic test uses; the fixture's panic value quotes
// it, and the log must not.
const item = "zz-item"

// logged replaces the fixture's engine with one that logs to the returned
// buffer.
func (f *fixture) logged() *bytes.Buffer {
	f.t.Helper()
	f.eng.Close()
	buf := &bytes.Buffer{}
	cfg := f.config()
	cfg.Base.Log = slog.New(slog.NewTextHandler(buf, nil))
	eng, err := New(cfg)
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
	return buf
}

func (f *fixture) panicOn(call string) {
	f.host.mu.Lock()
	defer f.host.mu.Unlock()
	f.host.panicOn = call
}

// wantPanicLogged checks that log holds exactly one handler panic record for
// call, with a stack, and nothing of the input.
func wantPanicLogged(t *testing.T, log *bytes.Buffer, call string) {
	t.Helper()
	text := log.String()
	if n := strings.Count(text, `msg="lifecycle: handler panic"`); n != 1 {
		t.Errorf("handler panic logged %d times, want once:\n%s", n, text)
	}
	if !strings.Contains(text, "call="+call) || !strings.Contains(text, "handler=fix.item.") ||
		!strings.Contains(text, "panic=string") || !strings.Contains(text, "stack=") {
		t.Errorf("log lacks the call, handler, panic type or stack:\n%s", text)
	}
	if strings.Contains(text, item) {
		t.Errorf("log quotes the input:\n%s", text)
	}
	if strings.Contains(text, "lifecycle: action failed") {
		t.Errorf("the panic was logged a second time as a failed action:\n%s", text)
	}
}

// wantPlain checks that err is a plain error, which the API answers as
// internal.
func wantPlain(t *testing.T, err error) {
	t.Helper()
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) {
		t.Fatalf("err = %v, want a plain error", err)
	}
}

func TestPanicsBeforeAChangeAreErrors(t *testing.T) {
	input := `{"name":"` + item + `","value":"1"}`
	cases := []struct {
		call string
		run  func(f *fixture) error
	}{
		{"Read", func(f *fixture) error {
			_, err := f.eng.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"`+item+`"}`))
			return err
		}},
		{"Plan", func(f *fixture) error {
			_, err := f.eng.Plan(context.Background(), alice, refSet, json.RawMessage(input))
			return err
		}},
	}
	for _, c := range cases {
		t.Run(c.call, func(t *testing.T) {
			f := newFixture(t)
			log := f.logged()
			f.panicOn(c.call)
			wantPlain(t, c.run(f))
			wantPanicLogged(t, log, c.call)
			if n := f.count("SELECT count(*) FROM core_plans"); n != 0 {
				t.Errorf("plans = %d, want 0", n)
			}
		})
	}
}

func TestPanicsDuringApplyChecksAreErrors(t *testing.T) {
	// Plan panics in apply's recheck; Snapshot before the intent is stored.
	for _, call := range []string{"Plan", "Snapshot"} {
		t.Run(call, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			pl := f.plan(alice, refSet, `{"name":"`+item+`","value":"1"}`)
			log := f.logged()
			f.panicOn(call)
			_, err := f.eng.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{})
			wantPlain(t, err)
			wantPanicLogged(t, log, call)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Errorf("changes = %d, want 0", n)
			}
			// The apply mutex was released and the plan is still usable.
			f.panicOn("")
			if v := f.apply(alice, pl.PlanID, core.ApplyOptions{}); v.State != core.StatePending {
				t.Errorf("apply after the panic: state %s, want pending", v.State)
			}
		})
	}
}

func TestPanicsAfterTheIntentFailTheChange(t *testing.T) {
	cases := []struct {
		name, call, input string
		ref               core.ActionRef
		opts              core.ApplyOptions
		state, code       string
	}{
		{"revertible apply", "Apply", `"value":"1"`, refSet, core.ApplyOptions{}, core.StateReverted, string(errcode.Internal)},
		{"compensable apply", "Apply", `"value":"1"`, refAdd, core.ApplyOptions{}, core.StateFailed, string(errcode.Internal)},
		{"irreversible verify", "Verify", `"value":"1"`, refRemove, core.ApplyOptions{Confirmation: item}, core.StateFailed, string(errcode.Internal)},
		{"revertible verify", "Verify", `"value":"1"`, refSet, core.ApplyOptions{}, core.StateReverted, string(errcode.Internal)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			pl := f.plan(alice, c.ref, `{"name":"`+item+`",`+c.input+`}`)
			log := f.logged()
			f.panicOn(c.call)
			v := f.apply(alice, pl.PlanID, c.opts)
			if v.State != c.state || v.Error == nil || v.Error.Code != c.code || v.Error.Message != errcode.Internal.Title() {
				t.Errorf("change = %+v (error %+v), want %s with %s and only its title", v, v.Error, c.state, c.code)
			}
			wantPanicLogged(t, log, c.call)
			if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
				t.Errorf("locks = %d, want 0", n)
			}
		})
	}
}

func TestPanickingRevertFailsTheChange(t *testing.T) {
	t.Run("after a failed apply", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"`+item+`","value":"1","failApply":true}`)
		log := f.logged()
		f.panicOn("Revert")
		v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
		if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.ChangeRevertFailed) {
			t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", v, v.Error)
		}
		if n := strings.Count(log.String(), `msg="lifecycle: handler panic"`); n != 1 {
			t.Errorf("handler panic logged %d times, want once", n)
		}
	})
	t.Run("by the timer", func(t *testing.T) {
		f := newFixture(t)
		log := f.logged()
		v := f.pending(`{"name":"` + item + `","value":"1"}`)
		f.panicOn("Revert")
		f.clock.Advance(DefaultConfirmTimeout)
		if s := f.state(v.ID); s != core.StateFailed {
			t.Errorf("state %s, want failed", s)
		}
		if n := strings.Count(log.String(), `msg="lifecycle: handler panic"`); n != 1 {
			t.Errorf("handler panic logged %d times, want once", n)
		}
	})
}

func TestRecoveryReportsAPanickingVerify(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"`+item+`","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	log := f.logged()
	f.panicOn("Verify")
	f.recoverAll()
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil ||
		!strings.HasSuffix(v.Error.Message, "verify failed: "+errcode.Internal.Title()) {
		t.Errorf("change = %+v (error %+v), want interrupted reporting the failed verify", v, v.Error)
	}
	wantPanicLogged(t, log, "Verify")
}

func TestProtect(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))
	boom := errors.New("boom")
	if err := Protect(log, "demo.item.get/v1", "Read", func() error { return boom }); !errors.Is(err, boom) {
		t.Errorf("Protect passed %v through, want boom", err)
	}
	var items []string
	err := Protect(log, "demo.status", "Resource", func() error {
		_ = items[len(item)] // a runtime error: index out of range
		return nil
	})
	if err == nil || err.Error() != "Resource of demo.status panicked" {
		t.Errorf("Protect = %v, want the panic as an error", err)
	}
	if text := buf.String(); !strings.Contains(text, "panic=\"runtime error: index out of range [7] with length 0\"") ||
		!strings.Contains(text, "handler=demo.status") {
		t.Errorf("log does not show the runtime error:\n%s", text)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Panic|Protect|Recover' ./internal/core/lifecycle/`

Expected: FAIL — the build reports `undefined: Protect`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/core/lifecycle [github.com/darkraise/darkpanel/internal/core/lifecycle.test]
internal/core/lifecycle/guard_test.go:207:12: undefined: Protect
internal/core/lifecycle/guard_test.go:211:9: undefined: Protect
FAIL	github.com/darkraise/darkpanel/internal/core/lifecycle [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/core/lifecycle/guard.go`:

```go
package lifecycle

import (
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"

	"github.com/darkraise/darkpanel/internal/core"
)

// panicError is a recovered handler panic. It carries no errcode, so the API
// answers internal and a change fails with code internal.
type panicError struct{ name, call string }

func (e *panicError) Error() string { return fmt.Sprintf("%s of %s panicked", e.call, e.name) }

// Protect runs f, the call named call of the handler name (an action
// reference, a resource or a module id), and returns its error. A panic in f
// is recovered and returned as a plain error "<call> of <name> panicked", and
// logged once at error level as "lifecycle: handler panic" with the handler,
// the call and the stack. The panic value is logged only when it is a runtime
// error; any other value is logged by its type alone, because a handler may
// have put its input in it.
func Protect(log *slog.Logger, name, call string, f func() error) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		value := fmt.Sprintf("%T", p)
		if re, ok := p.(runtime.Error); ok {
			value = re.Error()
		}
		log.Error("lifecycle: handler panic", "handler", name, "call", call, "panic", value, "stack", string(debug.Stack()))
		err = &panicError{name: name, call: call}
	}()
	return f()
}

// protect is Protect for a call of the action ref on the engine's log.
func (e *Engine) protect(ref core.ActionRef, call string, f func() error) error {
	return Protect(e.cfg.Base.Log, ref.String(), call, f)
}
```

In `internal/core/lifecycle/engine.go`, make these 2 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
		return nil, err
	}
	out, err := def.Handler.Read(ctx, e.env(p), input)
	if err != nil {
		return nil, err
```

with:

```go
		return nil, err
	}
	var out json.RawMessage
	err = e.protect(ref, "Read", func() error {
		var err error
		out, err = def.Handler.Read(ctx, e.env(p), input)
		return err
	})
	if err != nil {
		return nil, err
```

2. Replace this block:

```go
		return core.PlanView{}, err
	}
	out, err := def.Handler.Plan(ctx, e.env(p), canonical)
	if err != nil {
		return core.PlanView{}, err
```

with:

```go
		return core.PlanView{}, err
	}
	var out core.PlanOutcome
	err = e.protect(ref, "Plan", func() error {
		var err error
		out, err = def.Handler.Plan(ctx, e.env(p), canonical)
		return err
	})
	if err != nil {
		return core.PlanView{}, err
```

In `internal/core/lifecycle/apply.go`, make these 6 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
	}
	env := e.env(p)
	if err := recheck(ctx, env, def, pl, input); err != nil {
		return nil, nil, err
	}
```

with:

```go
	}
	env := e.env(p)
	if err := e.recheck(ctx, env, def, pl, input); err != nil {
		return nil, nil, err
	}
```

2. Replace this block:

```go
	var deadline any
	if def.Spec.Class == core.ClassRevertible {
		raw, err := def.Handler.Snapshot(ctx, env, input)
		if err != nil {
			return nil, nil, err
```

with:

```go
	var deadline any
	if def.Spec.Class == core.ClassRevertible {
		var raw []byte
		err := e.protect(def.Ref(), "Snapshot", func() error {
			var err error
			raw, err = def.Handler.Snapshot(ctx, env, input)
			return err
		})
		if err != nil {
			return nil, nil, err
```

3. Replace this block:

```go
// and the apply is refused when the state hash changed or a guard that passed
// now fails (plan.state_changed), or a guard failed (plan.guard_failed).
func recheck(ctx context.Context, env core.Env, def core.ActionDef, pl planRecord, input json.RawMessage) error {
	out, err := def.Handler.Plan(ctx, env, input)
	if err != nil {
		return err
```

with:

```go
// and the apply is refused when the state hash changed or a guard that passed
// now fails (plan.state_changed), or a guard failed (plan.guard_failed).
func (e *Engine) recheck(ctx context.Context, env core.Env, def core.ActionDef, pl planRecord, input json.RawMessage) error {
	var out core.PlanOutcome
	err := e.protect(def.Ref(), "Plan", func() error {
		var err error
		out, err = def.Handler.Plan(ctx, env, input)
		return err
	})
	if err != nil {
		return err
```

4. Replace this block:

```go
	env := e.env(p)
	h := it.def.Handler
	out, err := h.Apply(ctx, env, it.input)
	if err == nil {
		err = h.Verify(ctx, env, it.input, out)
	}
	if err != nil {
```

with:

```go
	env := e.env(p)
	h := it.def.Handler
	var out json.RawMessage
	err := e.protect(it.def.Ref(), "Apply", func() error {
		var err error
		out, err = h.Apply(ctx, env, it.input)
		return err
	})
	if err == nil {
		err = e.protect(it.def.Ref(), "Verify", func() error { return h.Verify(ctx, env, it.input, out) })
	}
	if err != nil {
```

5. Replace this block:

```go
// changeError is what a change's error shows: the code and the client-safe
// detail of an errcode error, and only the title of an internal one, whose
// cause is logged instead.
func (e *Engine) changeError(ref core.ActionRef, err error) *core.ChangeError {
	c := errcode.From(err)
	msg := c.Detail
	if c.Code == errcode.Internal {
		e.cfg.Base.Log.Error("lifecycle: action failed", "action", ref.String(), "error", err)
		msg = ""
	}
```

with:

```go
// changeError is what a change's error shows: the code and the client-safe
// detail of an errcode error, and only the title of an internal one, whose
// cause is logged instead (a panic was logged with its stack already).
func (e *Engine) changeError(ref core.ActionRef, err error) *core.ChangeError {
	c := errcode.From(err)
	msg := c.Detail
	if c.Code == errcode.Internal {
		var pe *panicError
		if !errors.As(err, &pe) {
			e.cfg.Base.Log.Error("lifecycle: action failed", "action", ref.String(), "error", err)
		}
		msg = ""
	}
```

6. Replace this block:

```go
		return fmt.Errorf("lifecycle: open snapshot of %s: %w", changeID, err)
	}
	return def.Handler.Revert(ctx, e.env(p), snapshot)
}

```

with:

```go
		return fmt.Errorf("lifecycle: open snapshot of %s: %w", changeID, err)
	}
	return e.protect(ref, "Revert", func() error { return def.Handler.Revert(ctx, e.env(p), snapshot) })
}

```

Replace the whole of `internal/core/lifecycle/recover.go` with:

```go
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRecovery is the actor of what startup recovery does.
const ActorRecovery = "system:recovery"

var recoveryPrincipal = core.Principal{Actor: ActorRecovery, Scope: core.ScopeAdmin}

// Recover settles every change a previous run left unfinished and releases
// the locks of finished changes (ADR 0004 startup recovery). Run it once,
// before the engine serves requests. Every unconfirmed revertible change is
// reverted from its snapshot whatever its deadline; a compensable or
// irreversible change cut off during apply is marked interrupted with what its
// verify reports, and is never retried or compensated.
func (e *Engine) Recover(ctx context.Context) error {
	type unfinished struct{ id, class, state string }
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
		"SELECT id, class, state FROM core_changes WHERE state IN (?, ?) ORDER BY id",
		core.StateApplying, core.StatePending)
	if err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}
	var todo []unfinished
	for rows.Next() {
		var u unfinished
		if err := rows.Scan(&u.id, &u.class, &u.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
		}
		todo = append(todo, u)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}

	var errs []error
	for _, u := range todo {
		tr := transition{
			from: u.state, to: core.StateReverted, release: true,
			phase: phaseRecover, result: resultOK, actor: ActorRecovery,
		}
		if core.Class(u.class) == core.ClassRevertible {
			if err := e.runRevert(ctx, recoveryPrincipal, u.id); err != nil {
				e.cfg.Base.Log.Error("lifecycle: recovery revert", "change", u.id, "error", err)
				tr.to, tr.err, tr.result = core.StateFailed, revertFailed(), resultError
			}
		} else {
			tr.to, tr.phase, tr.result = core.StateInterrupted, phaseInterrupt, resultError
			tr.err = &core.ChangeError{
				Code:    string(errcode.Internal),
				Message: "interrupted by a restart during apply; " + e.verifyStored(ctx, u.id),
			}
		}
		if _, err := e.transition(ctx, u.id, tr); err != nil {
			errs = append(errs, err)
		}
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"DELETE FROM core_locks WHERE owner IN (SELECT id FROM core_changes WHERE state NOT IN (?, ?))",
			core.StateApplying, core.StatePending)
		return err
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("lifecycle: release finished locks: %w", err))
	}
	return errors.Join(errs...)
}

// verifyStored runs the action's Verify against the plan's stored input, so
// the operator learns whether the interrupted mutation took effect.
func (e *Engine) verifyStored(ctx context.Context, changeID string) string {
	var action, planID string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT c.action, c.version, p.id, p.input_ct FROM core_changes c
		 JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?`, changeID).
		Scan(&action, &version, &planID, &ct); err != nil {
		return "verify not run: the stored input is unreadable"
	}
	def, ok := e.cfg.Registry.Action(core.ActionRef{Name: action, Version: version})
	if !ok {
		return "verify not run: the action is not registered"
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(planID), ct)
	if err != nil {
		return "verify not run: the stored input does not open"
	}
	err = e.protect(def.Ref(), "Verify", func() error {
		return def.Handler.Verify(ctx, e.env(recoveryPrincipal), input, json.RawMessage("null"))
	})
	if err != nil {
		return "verify failed: " + e.changeError(def.Ref(), err).Message
	}
	return "verify passed"
}

// Purge deletes expired plans that never became a change. A plan with a change
// is kept, so a repeated apply still returns that change (spec section 3).
func (e *Engine) Purge(ctx context.Context) error {
	// An apply that loaded a plan before it expired inserts its change later;
	// deleting the plan in between would fail that insert's foreign key.
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	now := formatTime(e.cfg.Base.Clock.Now())
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM core_plans WHERE expires_at <= ?
			 AND NOT EXISTS (SELECT 1 FROM core_changes WHERE plan_id = core_plans.id)`, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: purge plans: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Panic|Protect|Recover' ./internal/core/lifecycle/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/guard.go internal/core/lifecycle/engine.go internal/core/lifecycle/apply.go internal/core/lifecycle/recover.go internal/core/lifecycle/fixture_test.go internal/core/lifecycle/recover_test.go internal/core/lifecycle/guard_test.go
git commit -m "feat(lifecycle): turn handler panics into errors"
```


---

### Task 11: Verify deadline

**Files:**
- Modify: `internal/core/lifecycle/guard.go`
- Modify: `internal/core/lifecycle/engine.go`
- Modify: `internal/core/lifecycle/apply.go`
- Modify: `internal/core/lifecycle/recover.go`
- Test: `internal/core/lifecycle/fixture_test.go` (modify)
- Test: `internal/core/lifecycle/verify_test.go`

**Interfaces:**
- Consumes: Contracts › Task 10 (`protect`, `logged`, `CrashApply`)
- Produces: Contracts › `internal/core/lifecycle` Task 11 (`VerifyTimeout`)

**Items:** 52

**Depends on:** Task 10

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Register row 52's deadline half (preflight-39): `Recover` ran `Verify` with no deadline, so a hung handler blocked startup. `Verify` now runs under `VerifyTimeout` in apply and in recovery, in its own goroutine so even a handler that ignores its context returns at the deadline. The tests check the deadline a handler sees, and that a hung Verify fails an apply (reverting it) and an interrupted change's recovery within a shortened 50 ms deadline.

- [ ] **Step 1: Write the failing test**

In `internal/core/lifecycle/fixture_test.go`, make these 2 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
	// "Snapshot" or "Revert") that panics while set.
	panicOn string
}

```

with:

```go
	// "Snapshot" or "Revert") that panics while set.
	panicOn string
	// onVerify, when set, runs at the start of Verify without mu held.
	onVerify func(ctx context.Context)
}

```

2. Replace this block:

```go
}

func (x itemHandler) Verify(_ context.Context, _ core.Env, in itemIn, _ itemOut) error {
	x.h.panicIf("Verify", in.Name)
	if in.FailVerify {
```

with:

```go
}

func (x itemHandler) Verify(ctx context.Context, _ core.Env, in itemIn, _ itemOut) error {
	if x.h.onVerify != nil {
		x.h.onVerify(ctx)
	}
	x.h.panicIf("Verify", in.Name)
	if in.FailVerify {
```

Create `internal/core/lifecycle/verify_test.go`:

```go
package lifecycle

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// hangVerify makes Verify ignore its context and block until the test ends,
// and shortens the engine's verify deadline so the test runs in real time.
func (f *fixture) hangVerify() {
	f.t.Helper()
	release := make(chan struct{})
	returned := make(chan struct{}, 10)
	f.host.onVerify = func(context.Context) {
		<-release
		returned <- struct{}{}
	}
	f.eng.verifyTimeout = 50 * time.Millisecond
	f.t.Cleanup(func() {
		close(release)
		<-returned // the abandoned Verify finishes before the database closes
	})
}

func TestVerifyRunsUnderADeadline(t *testing.T) {
	if VerifyTimeout != 60*time.Second {
		t.Fatalf("VerifyTimeout = %v, want 60s", VerifyTimeout)
	}
	f := newFixture(t)
	if f.eng.verifyTimeout != VerifyTimeout {
		t.Fatalf("engine verify timeout = %v, want VerifyTimeout", f.eng.verifyTimeout)
	}
	var deadline time.Time
	var ok bool
	f.host.onVerify = func(ctx context.Context) { deadline, ok = ctx.Deadline() }
	before := time.Now()
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if !ok || deadline.Before(before.Add(VerifyTimeout)) || deadline.After(time.Now().Add(VerifyTimeout)) {
		t.Errorf("Verify deadline = %v (set %v), want VerifyTimeout from the call", deadline, ok)
	}
}

func TestAHungVerifyFailsTheChange(t *testing.T) {
	f := newFixture(t)
	log := f.logged()
	f.hangVerify()
	start := time.Now()
	v := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Apply took %v, want about the 50ms verify deadline", elapsed)
	}
	if v.State != core.StateReverted || v.Error == nil || v.Error.Code != string(errcode.Internal) {
		t.Errorf("change = %+v (error %+v), want reverted with internal", v, v.Error)
	}
	if _, ok := f.host.items["a"]; ok {
		t.Error("the item was not reverted")
	}
	if !strings.Contains(log.String(), "Verify of fix.item.set/v1 did not finish within 50ms") {
		t.Errorf("log does not report the deadline:\n%s", log)
	}
}

func TestRecoveryBoundsVerify(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	f.hangVerify()
	f.recoverAll()
	v, err := f.eng.Change(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil ||
		!strings.HasSuffix(v.Error.Message, "verify failed: "+errcode.Internal.Title()) {
		t.Errorf("change = %+v (error %+v), want interrupted reporting the failed verify", v, v.Error)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Verify' ./internal/core/lifecycle/`

Expected: FAIL — the build reports `f.eng.verifyTimeout undefined (type *Engine has no field or method verifyTimeout)` and `undefined: VerifyTimeout`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/core/lifecycle [github.com/darkraise/darkpanel/internal/core/lifecycle.test]
internal/core/lifecycle/verify_test.go:23:8: f.eng.verifyTimeout undefined (type *Engine has no field or method verifyTimeout)
internal/core/lifecycle/verify_test.go:31:5: undefined: VerifyTimeout
internal/core/lifecycle/verify_test.go:32:44: undefined: VerifyTimeout
internal/core/lifecycle/verify_test.go:35:11: f.eng.verifyTimeout undefined (type *Engine has no field or method verifyTimeout)
internal/core/lifecycle/verify_test.go:35:28: undefined: VerifyTimeout
internal/core/lifecycle/verify_test.go:36:68: f.eng.verifyTimeout undefined (type *Engine has no field or method verifyTimeout)
internal/core/lifecycle/verify_test.go:43:39: undefined: VerifyTimeout
```

- [ ] **Step 3: Write the implementation**

Replace the whole of `internal/core/lifecycle/guard.go` with:

```go
package lifecycle

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime"
	"runtime/debug"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// VerifyTimeout bounds a handler's Verify, in apply and in Recover.
const VerifyTimeout = 60 * time.Second

// panicError is a recovered handler panic. It carries no errcode, so the API
// answers internal and a change fails with code internal.
type panicError struct{ name, call string }

func (e *panicError) Error() string { return fmt.Sprintf("%s of %s panicked", e.call, e.name) }

// Protect runs f, the call named call of the handler name (an action
// reference, a resource or a module id), and returns its error. A panic in f
// is recovered and returned as a plain error "<call> of <name> panicked", and
// logged once at error level as "lifecycle: handler panic" with the handler,
// the call and the stack. The panic value is logged only when it is a runtime
// error; any other value is logged by its type alone, because a handler may
// have put its input in it.
func Protect(log *slog.Logger, name, call string, f func() error) (err error) {
	defer func() {
		p := recover()
		if p == nil {
			return
		}
		value := fmt.Sprintf("%T", p)
		if re, ok := p.(runtime.Error); ok {
			value = re.Error()
		}
		log.Error("lifecycle: handler panic", "handler", name, "call", call, "panic", value, "stack", string(debug.Stack()))
		err = &panicError{name: name, call: call}
	}()
	return f()
}

// protect is Protect for a call of the action ref on the engine's log.
func (e *Engine) protect(ref core.ActionRef, call string, f func() error) error {
	return Protect(e.cfg.Base.Log, ref.String(), call, f)
}

// verify runs the handler's Verify with a context that ends after the
// engine's verify timeout, and returns a plain error at that deadline even
// when the handler ignores its context. Such a handler keeps running in the
// background; Verify only reads the host, so the change's outcome is recorded
// without waiting for it.
func (e *Engine) verify(ctx context.Context, def core.ActionDef, env core.Env, in, out json.RawMessage) error {
	ctx, cancel := context.WithTimeout(ctx, e.verifyTimeout)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- e.protect(def.Ref(), "Verify", func() error { return def.Handler.Verify(ctx, env, in, out) })
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		select {
		case err := <-done:
			return err
		default:
		}
		return fmt.Errorf("lifecycle: Verify of %s did not finish within %s: %w", def.Ref(), e.verifyTimeout, ctx.Err())
	}
}
```

In `internal/core/lifecycle/engine.go`, make these 2 replacements, in order (each block occurs exactly once):

1. Replace this block:

```go
// are safe for concurrent use.
type Engine struct {
	cfg Config

	idMu    sync.Mutex
```

with:

```go
// are safe for concurrent use.
type Engine struct {
	cfg           Config
	verifyTimeout time.Duration // VerifyTimeout; tests shorten it

	idMu    sync.Mutex
```

2. Replace this block:

```go
		}
	}
	return &Engine{cfg: cfg, entropy: ulid.Monotonic(rand.Reader, 0), timers: map[string]core.Timer{}}, nil
}

```

with:

```go
		}
	}
	return &Engine{
		cfg: cfg, verifyTimeout: VerifyTimeout,
		entropy: ulid.Monotonic(rand.Reader, 0), timers: map[string]core.Timer{},
	}, nil
}

```

In `internal/core/lifecycle/apply.go`, make this replacement (the block occurs exactly once):

1. Replace this block:

```go
	})
	if err == nil {
		err = e.protect(it.def.Ref(), "Verify", func() error { return h.Verify(ctx, env, it.input, out) })
	}
	if err != nil {
```

with:

```go
	})
	if err == nil {
		err = e.verify(ctx, it.def, env, it.input, out)
	}
	if err != nil {
```

Replace the whole of `internal/core/lifecycle/recover.go` with:

```go
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ActorRecovery is the actor of what startup recovery does.
const ActorRecovery = "system:recovery"

var recoveryPrincipal = core.Principal{Actor: ActorRecovery, Scope: core.ScopeAdmin}

// Recover settles every change a previous run left unfinished and releases
// the locks of finished changes (ADR 0004 startup recovery). Run it once,
// before the engine serves requests. Every unconfirmed revertible change is
// reverted from its snapshot whatever its deadline; a compensable or
// irreversible change cut off during apply is marked interrupted with what its
// verify reports, and is never retried or compensated.
func (e *Engine) Recover(ctx context.Context) error {
	type unfinished struct{ id, class, state string }
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx,
		"SELECT id, class, state FROM core_changes WHERE state IN (?, ?) ORDER BY id",
		core.StateApplying, core.StatePending)
	if err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}
	var todo []unfinished
	for rows.Next() {
		var u unfinished
		if err := rows.Scan(&u.id, &u.class, &u.state); err != nil {
			_ = rows.Close()
			return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
		}
		todo = append(todo, u)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("lifecycle: find unfinished changes: %w", err)
	}

	var errs []error
	for _, u := range todo {
		tr := transition{
			from: u.state, to: core.StateReverted, release: true,
			phase: phaseRecover, result: resultOK, actor: ActorRecovery,
		}
		if core.Class(u.class) == core.ClassRevertible {
			if err := e.runRevert(ctx, recoveryPrincipal, u.id); err != nil {
				e.cfg.Base.Log.Error("lifecycle: recovery revert", "change", u.id, "error", err)
				tr.to, tr.err, tr.result = core.StateFailed, revertFailed(), resultError
			}
		} else {
			tr.to, tr.phase, tr.result = core.StateInterrupted, phaseInterrupt, resultError
			tr.err = &core.ChangeError{
				Code:    string(errcode.Internal),
				Message: "interrupted by a restart during apply; " + e.verifyStored(ctx, u.id),
			}
		}
		if _, err := e.transition(ctx, u.id, tr); err != nil {
			errs = append(errs, err)
		}
	}
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			"DELETE FROM core_locks WHERE owner IN (SELECT id FROM core_changes WHERE state NOT IN (?, ?))",
			core.StateApplying, core.StatePending)
		return err
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("lifecycle: release finished locks: %w", err))
	}
	return errors.Join(errs...)
}

// verifyStored runs the action's Verify against the plan's stored input, so
// the operator learns whether the interrupted mutation took effect.
func (e *Engine) verifyStored(ctx context.Context, changeID string) string {
	var action, planID string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT c.action, c.version, p.id, p.input_ct FROM core_changes c
		 JOIN core_plans p ON p.id = c.plan_id WHERE c.id = ?`, changeID).
		Scan(&action, &version, &planID, &ct); err != nil {
		return "verify not run: the stored input is unreadable"
	}
	def, ok := e.cfg.Registry.Action(core.ActionRef{Name: action, Version: version})
	if !ok {
		return "verify not run: the action is not registered"
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(planID), ct)
	if err != nil {
		return "verify not run: the stored input does not open"
	}
	if err := e.verify(ctx, def, e.env(recoveryPrincipal), input, json.RawMessage("null")); err != nil {
		return "verify failed: " + e.changeError(def.Ref(), err).Message
	}
	return "verify passed"
}

// Purge deletes expired plans that never became a change. A plan with a change
// is kept, so a repeated apply still returns that change (spec section 3).
func (e *Engine) Purge(ctx context.Context) error {
	// An apply that loaded a plan before it expired inserts its change later;
	// deleting the plan in between would fail that insert's foreign key.
	e.applyMu.Lock()
	defer e.applyMu.Unlock()
	now := formatTime(e.cfg.Base.Clock.Now())
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`DELETE FROM core_plans WHERE expires_at <= ?
			 AND NOT EXISTS (SELECT 1 FROM core_changes WHERE plan_id = core_plans.id)`, now)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: purge plans: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Verify' ./internal/core/lifecycle/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/guard.go internal/core/lifecycle/engine.go internal/core/lifecycle/apply.go internal/core/lifecycle/recover.go internal/core/lifecycle/fixture_test.go internal/core/lifecycle/verify_test.go
git commit -m "feat(lifecycle): bound Verify with a deadline"
```


---

### Task 12: Controller audit tables and audit store

**Files:**
- Create: `internal/controller/audit/audit.go`
- Create: `internal/core/store/migrations/controller/0004_audit.sql`
- Test: `internal/controller/audit/audit_test.go`

**Interfaces:**
- Consumes: `core.Audit`, `core.AuditEntry`, `core.AuditQuery`, `core.JournalEntry` (with `Source`, Contracts › Task 5), `store.DB`, `store.Migrate`, `store.CoreMigrations`, `coretest.FakeClock`
- Produces: Contracts › `internal/controller/audit` Task 12

**Items:** 14, 48

**Depends on:** Task 5

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The controller's `core.Audit` over `core_audit` (spec section 11), with the controller migration that also creates the journal cursor and the change index for Tasks 13 and 26. `Append` is the controller engine's journal and writes inside the change's transaction; `Record` keeps the ids `auth` sets; `Ingest` deduplicates by host and sequence through the unique index; `List` pages newest first as `core.audit.list` expects. The tests cover the tables and constraints, the login and logout rows, the rollback of an appended row, deduplication, every filter, paging and the limits.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/audit/audit_test.go`:

```go
package audit

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// openControllerDB returns a migrated controller database in a temporary
// directory.
func openControllerDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	db, err := store.Open(ctx, filepath.Join(dir, "controller.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func newTestStore(t *testing.T) (*Store, *store.DB, *coretest.FakeClock) {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(t0)
	return New(db, clock), db, clock
}

func count(t *testing.T, db *store.DB, query string, args ...any) int {
	t.Helper()
	var n int
	if err := db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func list(t *testing.T, s *Store, q core.AuditQuery) ([]core.AuditEntry, string) {
	t.Helper()
	entries, next, err := s.List(context.Background(), q)
	if err != nil {
		t.Fatal(err)
	}
	return entries, next
}

func TestAuditTables(t *testing.T) {
	db := openControllerDB(t)
	ctx := context.Background()
	for _, table := range []string{"core_audit", "core_journal_cursor", "core_change_index"} {
		if n := count(t, db, "SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = ?", table); n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
	insert := `INSERT INTO core_audit (id, seq, at, actor, host_id, action, version, phase, result)
		VALUES (?, ?, '2026-10-17T12:00:00.000000000Z', 'user:u1', ?, 'a.b.c', 1, 'apply', 'ok')`
	for _, row := range [][]any{{"01A", nil, nil}, {"01B", nil, nil}, {"01C", 1, "h1"}, {"01D", 1, "h2"}} {
		if _, err := db.Writer().ExecContext(ctx, insert, row...); err != nil {
			t.Fatalf("insert %v: %v", row, err)
		}
	}
	if _, err := db.Writer().ExecContext(ctx, insert, "01E", 1, "h1"); err == nil {
		t.Error("a second row for host h1 and seq 1 was accepted")
	}
	if _, err := db.Writer().ExecContext(ctx,
		`INSERT INTO core_audit (id, at, actor, action, version, phase, result)
		 VALUES ('01F', '2026-10-17T12:00:00.000000000Z', 'user:u1', 'a.b.c', 1, 'delete', 'ok')`); err == nil {
		t.Error("an unknown phase was accepted")
	}
}

func TestRecordKeepsTheIDAndLoginActions(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	login := core.AuditEntry{
		ID: "01JAAAAAAAAAAAAAAAAAAAAAAA", Actor: "anonymous", Action: "core.auth.login", Phase: "login",
		Result: "error", ErrorCode: "auth.invalid_credentials", Source: "192.0.2.1", At: t0, Version: 1,
		Input: json.RawMessage(`{"username":"mallory"}`),
	}
	logout := core.AuditEntry{
		Actor: "user:u1", Action: "core.auth.logout", Phase: "logout", Result: "ok", Source: "192.0.2.1",
		At: t0.Add(time.Second), Version: 1, Input: json.RawMessage(`{}`),
	}
	for _, e := range []core.AuditEntry{login, logout} {
		if err := s.Record(ctx, e); err != nil {
			t.Fatalf("Record %s: %v", e.Action, err)
		}
	}
	got, next := list(t, s, core.AuditQuery{})
	if len(got) != 2 || next != "" {
		t.Fatalf("List = %d entries, next %q; want 2 and no next page", len(got), next)
	}
	if len(got[0].ID) != 26 || got[0].ID == login.ID {
		t.Errorf("logout id = %q, want a new ULID", got[0].ID)
	}
	logout.ID = got[0].ID
	for i, want := range []core.AuditEntry{logout, login} {
		if !reflect.DeepEqual(got[i], want) {
			t.Errorf("entry %d =\n%+v\nwant\n%+v", i, got[i], want)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE change_id IS NULL AND host_id IS NULL AND seq IS NULL"); n != 2 {
		t.Errorf("rows with null change, host and seq = %d, want 2", n)
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE error_code IS NULL"); n != 1 {
		t.Errorf("rows with a null error code = %d, want 1 (the logout)", n)
	}
}

func TestAppendWritesInTheChangeTransaction(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	e := core.JournalEntry{
		At: t0, Actor: "user:u1", Action: "core.token.create", Version: 1, Phase: "apply", ChangeID: "01CHANGE",
		Input: json.RawMessage(`{"name":"ci","secret":"[redacted]"}`), Result: "ok", Source: "192.0.2.1",
	}
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := s.Append(ctx, tx, e); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v, want boom", err)
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit"); n != 0 {
		t.Fatalf("rows after the rollback = %d, want 0", n)
	}
	system := e
	system.Actor, system.Source, system.Phase, system.Result, system.ErrorCode = "system:recovery", "", "interrupt", "error", "internal"
	for _, entry := range []core.JournalEntry{e, system} {
		if err := db.Write(ctx, func(tx *sql.Tx) error { return s.Append(ctx, tx, entry) }); err != nil {
			t.Fatal(err)
		}
	}
	got, _ := list(t, s, core.AuditQuery{})
	want := core.AuditEntry{
		ID: got[1].ID, Actor: "user:u1", Action: "core.token.create", Phase: "apply", ChangeID: "01CHANGE",
		Result: "ok", Source: "192.0.2.1", At: t0, Version: 1, Input: e.Input,
	}
	if !reflect.DeepEqual(got[1], want) {
		t.Errorf("entry =\n%+v\nwant\n%+v", got[1], want)
	}
	if got[0].Source != "" || got[0].ErrorCode != "internal" {
		t.Errorf("system entry = %+v, want no source and error internal", got[0])
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE source IS NULL"); n != 1 {
		t.Errorf("rows with a null source = %d, want 1", n)
	}
}

func journalEntry(seq int64, at time.Time) core.JournalEntry {
	return core.JournalEntry{
		Seq: seq, At: at, Actor: "user:u1", Action: "kv.item.set", Version: 1, Phase: "apply",
		ChangeID: "01C", Input: json.RawMessage(`{"name":"a"}`), Result: "ok", Source: "192.0.2.1",
	}
}

func TestIngestDeduplicatesByHostAndSeq(t *testing.T) {
	s, db, _ := newTestStore(t)
	ctx := context.Background()
	steps := []struct {
		host string
		seqs []int64
	}{{"h1", []int64{1, 2}}, {"h1", []int64{2, 3}}, {"h2", []int64{1}}}
	for _, step := range steps {
		var entries []core.JournalEntry
		for _, seq := range step.seqs {
			entries = append(entries, journalEntry(seq, t0.Add(time.Duration(seq)*time.Second)))
		}
		if err := s.Ingest(ctx, step.host, entries); err != nil {
			t.Fatal(err)
		}
	}
	if n := count(t, db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1'"); n != 3 {
		t.Errorf("h1 rows = %d, want 3", n)
	}
	got, _ := list(t, s, core.AuditQuery{HostID: "h2"})
	want := core.AuditEntry{
		ID: got[0].ID, Actor: "user:u1", HostID: "h2", Action: "kv.item.set", Phase: "apply", ChangeID: "01C",
		Result: "ok", Source: "192.0.2.1", At: t0.Add(time.Second), Version: 1, Seq: 1, Input: json.RawMessage(`{"name":"a"}`),
	}
	if len(got) != 1 || !reflect.DeepEqual(got[0], want) {
		t.Errorf("h2 entries = %+v, want %+v", got, want)
	}
}

func TestListFiltersAndPages(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	add := func(i int, actor, host, action string) {
		e := core.AuditEntry{Actor: actor, HostID: host, Action: action, Phase: "apply", Result: "ok",
			At: t0.Add(time.Duration(i) * time.Minute), Version: 1, Seq: int64(i), Input: json.RawMessage(`{}`)}
		if err := s.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	add(1, "user:a", "h1", "kv.item.set")
	add(2, "user:b", "", "core.token.create")
	add(3, "user:a", "h2", "kv.item.set")
	add(4, "user:a", "", "core.token.revoke")
	add(5, "user:b", "h1", "kv.item.get")
	seqs := func(entries []core.AuditEntry) []int64 {
		out := []int64{}
		for _, e := range entries {
			out = append(out, e.Seq)
		}
		return out
	}
	cases := []struct {
		name string
		q    core.AuditQuery
		want []int64
	}{
		{"all, newest first", core.AuditQuery{}, []int64{5, 4, 3, 2, 1}},
		{"actor", core.AuditQuery{Actor: "user:a"}, []int64{4, 3, 1}},
		{"host", core.AuditQuery{HostID: "h1"}, []int64{5, 1}},
		{"action", core.AuditQuery{Action: "kv.item.set"}, []int64{3, 1}},
		{"combined", core.AuditQuery{Actor: "user:a", HostID: "h1"}, []int64{1}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, next := list(t, s, c.q)
			if !reflect.DeepEqual(seqs(got), c.want) || next != "" {
				t.Errorf("List = %v next %q, want %v and no next page", seqs(got), next, c.want)
			}
		})
	}
	first, next := list(t, s, core.AuditQuery{Limit: 2})
	if !reflect.DeepEqual(seqs(first), []int64{5, 4}) || next != first[1].ID {
		t.Fatalf("page 1 = %v next %q, want [5 4] and the last id", seqs(first), next)
	}
	second, next := list(t, s, core.AuditQuery{Limit: 2, Before: next})
	if !reflect.DeepEqual(seqs(second), []int64{3, 2}) || next != second[1].ID {
		t.Fatalf("page 2 = %v next %q, want [3 2] and the last id", seqs(second), next)
	}
	third, next := list(t, s, core.AuditQuery{Limit: 2, Before: next})
	if !reflect.DeepEqual(seqs(third), []int64{1}) || next != "" {
		t.Errorf("page 3 = %v next %q, want [1] and no next page", seqs(third), next)
	}
	empty, next := list(t, s, core.AuditQuery{Actor: "user:nobody"})
	if empty == nil || len(empty) != 0 || next != "" {
		t.Errorf("no match = %#v next %q, want an empty non-nil slice", empty, next)
	}
}

func TestListLimits(t *testing.T) {
	s, _, _ := newTestStore(t)
	ctx := context.Background()
	var entries []core.JournalEntry
	for i := range MaxListLimit + 1 {
		entries = append(entries, journalEntry(int64(i+1), t0.Add(time.Duration(i)*time.Millisecond)))
	}
	if err := s.Ingest(ctx, "h1", entries); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct{ limit, want int }{{0, DefaultListLimit}, {-1, DefaultListLimit}, {7, 7}, {MaxListLimit + 50, MaxListLimit}} {
		got, next := list(t, s, core.AuditQuery{Limit: c.limit})
		if len(got) != c.want || next == "" {
			t.Errorf("Limit %d: %d entries next %q, want %d and a next page", c.limit, len(got), next, c.want)
		}
	}
}

func TestZeroTimeTakesTheClock(t *testing.T) {
	s, _, clock := newTestStore(t)
	clock.Advance(time.Hour)
	if err := s.Record(context.Background(), core.AuditEntry{Actor: "user:u1", Action: "core.auth.logout",
		Phase: "logout", Result: "ok", Version: 1}); err != nil {
		t.Fatal(err)
	}
	got, _ := list(t, s, core.AuditQuery{})
	if len(got) != 1 || !got[0].At.Equal(t0.Add(time.Hour)) || got[0].Input != nil {
		t.Errorf("entry = %+v, want the clock's time and no input", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAuditTables|TestRecord|TestAppend|TestIngestDeduplicates|TestList|TestZeroTime' ./internal/controller/audit/`

Expected: FAIL — the build reports `undefined: Store` and `undefined: New`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/audit [github.com/darkraise/darkpanel/internal/controller/audit.test]
internal/controller/audit/audit_test.go:43:35: undefined: Store
internal/controller/audit/audit_test.go:47:9: undefined: New
internal/controller/audit/audit_test.go:59:28: undefined: Store
internal/controller/audit/audit_test.go:273:17: undefined: MaxListLimit
internal/controller/audit/audit_test.go:279:52: undefined: DefaultListLimit
internal/controller/audit/audit_test.go:279:104: undefined: MaxListLimit
FAIL	github.com/darkraise/darkpanel/internal/controller/audit [build failed]
```

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/controller/0004_audit.sql`:

```sql
-- Audit tables (spec section 11), read and written only through
-- internal/controller/audit and internal/controller/dispatch. Times are
-- fixed-width RFC 3339 UTC text, so they compare as text. The controller never
-- prunes core_audit in v1.

-- One row per login, logout and change event. Controller-side rows have a
-- null host_id and seq; agent rows carry the agent journal's sequence, and the
-- unique index makes their ingestion idempotent (null pairs never collide).
CREATE TABLE core_audit (
    id         TEXT PRIMARY KEY,
    seq        INTEGER,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    host_id    TEXT,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    phase      TEXT NOT NULL
        CHECK (phase IN ('apply', 'confirm', 'revert', 'recover', 'interrupt', 'login', 'logout')),
    change_id  TEXT,
    input_json TEXT,
    result     TEXT NOT NULL CHECK (result IN ('ok', 'error')),
    error_code TEXT,
    source     TEXT
) STRICT;

CREATE UNIQUE INDEX core_audit_host_seq ON core_audit (host_id, seq);

-- The highest agent journal sequence stored in core_audit, per host.
CREATE TABLE core_journal_cursor (
    host_id   TEXT PRIMARY KEY,
    acked_seq INTEGER NOT NULL
) STRICT;

-- The host of every agent change, so a change id finds its agent.
CREATE TABLE core_change_index (
    change_id  TEXT PRIMARY KEY,
    host_id    TEXT NOT NULL,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX core_change_index_host ON core_change_index (host_id);
```

Create `internal/controller/audit/audit.go`:

```go
// Package audit is the controller's audit log over core_audit (spec section
// 11): the controller engine's journal, the login and logout records, the
// ingestion of agent journals and the listing behind core.audit.list.
package audit

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// List returns DefaultListLimit entries unless asked for fewer, and never
// more than MaxListLimit.
const (
	DefaultListLimit = 100
	MaxListLimit     = 500
)

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// correctly as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Store is the core.Audit of the controller database. Its methods are safe
// for concurrent use.
type Store struct {
	db    *store.DB
	clock core.Clock

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
}

var _ core.Audit = (*Store)(nil)

// New returns the audit log of the migrated controller database db. clock
// stamps entries that arrive without a time.
func New(db *store.DB, clock core.Clock) *Store {
	return &Store{db: db, clock: clock, entropy: ulid.Monotonic(rand.Reader, 0)}
}

// newID returns a ULID stamped with at, so ids sort like the events.
func (s *Store) newID(at time.Time) (string, error) {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	id, err := ulid.New(ulid.Timestamp(at), s.entropy)
	if err != nil {
		return "", fmt.Errorf("audit: new id: %w", err)
	}
	return id.String(), nil
}

// insert writes e inside tx. Empty strings, a zero seq and an empty input are
// stored as NULL; with skipDuplicate an agent row already stored is skipped.
func (s *Store) insert(ctx context.Context, tx *sql.Tx, e core.AuditEntry, skipDuplicate bool) error {
	if e.At.IsZero() {
		e.At = s.clock.Now()
	}
	if e.ID == "" {
		id, err := s.newID(e.At)
		if err != nil {
			return err
		}
		e.ID = id
	}
	query := `INSERT INTO core_audit (id, seq, at, actor, host_id, action, version, phase, change_id, input_json,
		result, error_code, source) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`
	if skipDuplicate {
		query += " ON CONFLICT (host_id, seq) DO NOTHING"
	}
	var seq any
	if e.Seq != 0 {
		seq = e.Seq
	}
	var input any
	if len(e.Input) > 0 {
		input = string(e.Input)
	}
	_, err := tx.ExecContext(ctx, query, e.ID, seq, e.At.UTC().Format(timeFormat), e.Actor, null(e.HostID),
		e.Action, e.Version, e.Phase, null(e.ChangeID), input, e.Result, null(e.ErrorCode), null(e.Source))
	if err != nil {
		return fmt.Errorf("audit: insert entry: %w", err)
	}
	return nil
}

// null stores an empty string as NULL.
func null(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// fromJournal is the audit row of a journal entry of hostID ("" for the
// controller).
func fromJournal(hostID string, e core.JournalEntry) core.AuditEntry {
	return core.AuditEntry{
		Actor: e.Actor, HostID: hostID, Action: e.Action, Phase: e.Phase, ChangeID: e.ChangeID,
		Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source, At: e.At, Version: e.Version, Seq: e.Seq,
		Input: e.Input,
	}
}

// Append records a controller-side change event inside the change's
// transaction, as the controller engine's core.Journal. The input is stored
// as given: the engine redacted it.
func (s *Store) Append(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	e.Seq = 0
	return s.insert(ctx, tx, fromJournal("", e), false)
}

// Record stores a login or logout (or any event without a change), keeping a
// supplied ID; an entry without one gets a ULID.
func (s *Store) Record(ctx context.Context, e core.AuditEntry) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error { return s.insert(ctx, tx, e, false) })
}

// Ingest stores agent journal entries of hostID in one transaction, skipping
// any host and sequence pair already stored.
func (s *Store) Ingest(ctx context.Context, hostID string, entries []core.JournalEntry) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error { return s.ingest(ctx, tx, hostID, entries) })
}

func (s *Store) ingest(ctx context.Context, tx *sql.Tx, hostID string, entries []core.JournalEntry) error {
	for _, e := range entries {
		if err := s.insert(ctx, tx, fromJournal(hostID, e), true); err != nil {
			return err
		}
	}
	return nil
}

// List returns entries newest first, filtered by the query's exact-match
// fields and by Before (ids below it). A Limit of 0 or less means
// DefaultListLimit, and at most MaxListLimit are returned. nextBefore is the
// last returned id when older matching entries remain, else empty.
func (s *Store) List(ctx context.Context, q core.AuditQuery) ([]core.AuditEntry, string, error) {
	query := `SELECT id, seq, at, actor, host_id, action, version, phase, change_id, input_json, result,
		error_code, source FROM core_audit WHERE 1 = 1`
	var args []any
	for _, f := range []struct{ column, value string }{
		{"actor", q.Actor}, {"host_id", q.HostID}, {"action", q.Action},
	} {
		if f.value != "" {
			query += " AND " + f.column + " = ?"
			args = append(args, f.value)
		}
	}
	if q.Before != "" {
		query += " AND id < ?"
		args = append(args, q.Before)
	}
	limit := q.Limit
	if limit <= 0 {
		limit = DefaultListLimit
	}
	limit = min(limit, MaxListLimit)
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("audit: list: %w", err)
	}
	out := []core.AuditEntry{}
	for rows.Next() {
		e, err := scan(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		out = append(out, e)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, "", fmt.Errorf("audit: list: %w", err)
	}
	if len(out) > limit {
		out = out[:limit]
		return out, out[limit-1].ID, nil
	}
	return out, "", nil
}

func scan(rows *sql.Rows) (core.AuditEntry, error) {
	var e core.AuditEntry
	var seq sql.NullInt64
	var at string
	var host, change, input, code, source sql.NullString
	if err := rows.Scan(&e.ID, &seq, &at, &e.Actor, &host, &e.Action, &e.Version, &e.Phase, &change, &input,
		&e.Result, &code, &source); err != nil {
		return e, fmt.Errorf("audit: list: %w", err)
	}
	t, err := time.Parse(timeFormat, at)
	if err != nil {
		return e, fmt.Errorf("audit: stored time %q: %w", at, err)
	}
	e.At, e.Seq, e.HostID, e.ChangeID = t, seq.Int64, host.String, change.String
	e.ErrorCode, e.Source = code.String, source.String
	if input.Valid {
		e.Input = json.RawMessage(input.String)
	}
	return e, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAuditTables|TestRecord|TestAppend|TestIngestDeduplicates|TestList|TestZeroTime' ./internal/controller/audit/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/audit`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/audit/ ./internal/core/store/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/audit/audit.go internal/core/store/migrations/controller/0004_audit.sql internal/controller/audit/audit_test.go
git commit -m "feat(audit): store the audit log in core_audit"
```


---

### Task 13: Agent journal ingester

**Files:**
- Create: `internal/controller/audit/ingest.go`
- Test: `internal/controller/audit/ingest_test.go`

**Interfaces:**
- Consumes: Contracts › Task 12 (`Store`, `ingest`, `core_journal_cursor`, test helpers `newTestStore`, `count`, `list`, `journalEntry`, `t0`); `core.HostDirectory`, `core.AgentClient.Journal` and `Ack`, `coretest.FakeAgent`, `coretest.MapHosts`, `coretest.FakeClock`
- Produces: Contracts › `internal/controller/audit` Task 13 (`Ingester`)

**Items:** 14

**Depends on:** Task 12

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

ADR 0003's journal flow: entries after the host's cursor are stored with the new cursor in one transaction and then acknowledged, so a crash can neither lose nor repeat rows. The ingester runs on demand (the dispatcher calls it after each agent change) and every 5 seconds on `Clock.AfterFunc`, and `Stop` waits for a running ingestion. The tests cover storing and acknowledging, paging, acknowledging a stored cursor after a restart, a non-increasing sequence, one failing host among several, the loop on a fake clock, and `Stop` waiting.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/audit/ingest_test.go`:

```go
package audit

import (
	"context"
	"log/slog"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// memJournal is an agent journal in memory behind a coretest.FakeAgent.
type memJournal struct {
	mu      sync.Mutex
	entries []core.JournalEntry
	acks    []int64
	reads   []int64 // the afterSeq of every Journal call
	// entered, when set, receives a value as Journal starts; Journal then
	// waits for release.
	entered chan struct{}
	release chan struct{}
}

func (m *memJournal) add(n int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	next := int64(1)
	if len(m.entries) > 0 {
		next = m.entries[len(m.entries)-1].Seq + 1
	}
	if len(m.acks) > 0 {
		next = max(next, m.acks[len(m.acks)-1]+1)
	}
	for i := range n {
		m.entries = append(m.entries, journalEntry(next+int64(i), t0.Add(time.Duration(next+int64(i))*time.Second)))
	}
}

func (m *memJournal) agent() *coretest.FakeAgent {
	return &coretest.FakeAgent{
		JournalFunc: func(_ context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
			if m.entered != nil {
				m.entered <- struct{}{}
				<-m.release
			}
			m.mu.Lock()
			defer m.mu.Unlock()
			m.reads = append(m.reads, afterSeq)
			var out []core.JournalEntry
			for _, e := range m.entries {
				if e.Seq > afterSeq && len(out) < limit {
					out = append(out, e)
				}
			}
			return out, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			m.acks = append(m.acks, seq)
			var kept []core.JournalEntry
			for _, e := range m.entries {
				if e.Seq > seq {
					kept = append(kept, e)
				}
			}
			m.entries = kept
			return nil
		},
	}
}

func (m *memJournal) state() (acks, reads []int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]int64(nil), m.acks...), append([]int64(nil), m.reads...)
}

func addHost(t *testing.T, db *store.DB, id string) {
	t.Helper()
	if _, err := db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, '2026-10-17T00:00:00Z')", id, id); err != nil {
		t.Fatal(err)
	}
}

type ingestEnv struct {
	store    *Store
	db       *store.DB
	clock    *coretest.FakeClock
	hosts    coretest.MapHosts
	ingester *Ingester
}

func newIngestEnv(t *testing.T) *ingestEnv {
	t.Helper()
	s, db, clock := newTestStore(t)
	e := &ingestEnv{store: s, db: db, clock: clock, hosts: coretest.MapHosts{}}
	e.ingester = e.newIngester()
	return e
}

func (e *ingestEnv) newIngester() *Ingester {
	return NewIngester(IngesterConfig{Store: e.store, Agents: e.hosts, Clock: e.clock, Log: slog.New(slog.DiscardHandler)})
}

func (e *ingestEnv) host(t *testing.T, id string) *memJournal {
	t.Helper()
	addHost(t, e.db, id)
	m := &memJournal{}
	e.hosts[id] = m.agent()
	return m
}

func cursorOf(t *testing.T, db *store.DB, hostID string) int64 {
	t.Helper()
	var seq int64
	if err := db.Read().QueryRow("SELECT acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).Scan(&seq); err != nil {
		t.Fatalf("cursor of %s: %v", hostID, err)
	}
	return seq
}

func TestIngestHostStoresAdvancesAndAcks(t *testing.T) {
	e := newIngestEnv(t)
	ctx := context.Background()
	m := e.host(t, "h1")
	m.add(3)
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h1'"); n != 3 {
		t.Errorf("audit rows = %d, want 3", n)
	}
	if c := cursorOf(t, e.db, "h1"); c != 3 {
		t.Errorf("cursor = %d, want 3", c)
	}
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{3}) || !reflect.DeepEqual(reads, []int64{0, 3}) {
		t.Errorf("acks %v reads %v, want acks [3] and reads [0 3]", acks, reads)
	}
	got, _ := list(t, e.store, core.AuditQuery{HostID: "h1", Limit: 1})
	if len(got) != 1 || got[0].Seq != 3 || got[0].Source != "192.0.2.1" || got[0].HostID != "h1" {
		t.Errorf("newest entry = %+v, want seq 3 of h1 with its source", got)
	}
}

func TestIngestHostReadsEveryPage(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	m.add(2*journalPage + 1)
	if err := e.ingester.IngestHost(context.Background(), "h1"); err != nil {
		t.Fatal(err)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2*journalPage+1 {
		t.Errorf("audit rows = %d, want %d", n, 2*journalPage+1)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{2*journalPage + 1}) || !reflect.DeepEqual(reads, []int64{0, journalPage, 2 * journalPage}) {
		t.Errorf("acks %v reads %v, want one ack of the last entry after three pages", acks, reads)
	}
}

func TestIngestAcksTheStoredCursorAfterARestart(t *testing.T) {
	e := newIngestEnv(t)
	ctx := context.Background()
	m := e.host(t, "h1")
	m.add(5)
	// A run that stored the entries and stopped before its Ack.
	m.mu.Lock()
	entries := append([]core.JournalEntry(nil), m.entries...)
	m.mu.Unlock()
	if err := e.store.advance(ctx, "h1", entries, 5); err != nil {
		t.Fatal(err)
	}
	if err := e.newIngester().IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	acks, reads := m.state()
	if !reflect.DeepEqual(acks, []int64{5}) || !reflect.DeepEqual(reads, []int64{5}) {
		t.Errorf("acks %v reads %v, want the stored cursor acknowledged once", acks, reads)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 5 {
		t.Errorf("audit rows = %d, want 5", n)
	}
}

func TestIngestRefusesASequenceThatDoesNotAdvance(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1")
	e.hosts["h1"] = &coretest.FakeAgent{
		JournalFunc: func(context.Context, int64, int) ([]core.JournalEntry, error) {
			return []core.JournalEntry{journalEntry(1, t0)}, nil
		},
		AckFunc: func(context.Context, int64) error { return nil },
	}
	ctx := context.Background()
	if err := e.ingester.IngestHost(ctx, "h1"); err != nil {
		t.Fatal(err)
	}
	err := e.ingester.IngestHost(ctx, "h1")
	if err == nil || !strings.Contains(err.Error(), "sequence 1 after 1") {
		t.Errorf("IngestHost = %v, want a refused sequence", err)
	}
}

func TestIngestAllContinuesPastAFailingHost(t *testing.T) {
	e := newIngestEnv(t)
	addHost(t, e.db, "h1") // no agent: the directory answers not_found
	m := e.host(t, "h2")
	m.add(2)
	err := e.ingester.IngestAll(context.Background())
	if err == nil || !strings.Contains(err.Error(), "host h1") {
		t.Errorf("IngestAll = %v, want the error of h1", err)
	}
	if n := count(t, e.db, "SELECT count(*) FROM core_audit WHERE host_id = 'h2'"); n != 2 {
		t.Errorf("h2 rows = %d, want 2", n)
	}
}

func TestIngesterRunsEveryIntervalUntilStopped(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	e.ingester.Start()
	e.ingester.Start() // a second Start arms nothing more
	if p := e.clock.Pending(); p != 1 {
		t.Fatalf("armed timers = %d, want 1", p)
	}
	m.add(1)
	e.clock.Advance(IngestInterval)
	m.add(1)
	e.clock.Advance(IngestInterval)
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2 {
		t.Errorf("audit rows after two intervals = %d, want 2", n)
	}
	e.ingester.Stop()
	m.add(1)
	e.clock.Advance(IngestInterval)
	if n := count(t, e.db, "SELECT count(*) FROM core_audit"); n != 2 {
		t.Errorf("audit rows after Stop = %d, want 2", n)
	}
	if p := e.clock.Pending(); p != 0 {
		t.Errorf("armed timers after Stop = %d, want 0", p)
	}
	if err := e.ingester.IngestHost(context.Background(), "h1"); err != nil {
		t.Errorf("IngestHost after Stop = %v, want nil", err)
	}
	if _, reads := m.state(); len(reads) != 2 {
		t.Errorf("journal reads = %v, want none after Stop", reads)
	}
}

func TestStopWaitsForARunningIngestion(t *testing.T) {
	e := newIngestEnv(t)
	m := e.host(t, "h1")
	m.entered, m.release = make(chan struct{}), make(chan struct{})
	e.ingester.Start()
	go e.clock.Advance(IngestInterval)
	<-m.entered
	stopped := make(chan struct{})
	go func() {
		e.ingester.Stop()
		close(stopped)
	}()
	select {
	case <-stopped:
		t.Fatal("Stop returned while an ingestion was running")
	case <-time.After(50 * time.Millisecond):
	}
	close(m.release)
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Fatal("Stop did not return after the ingestion finished")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Ingest|Stop' ./internal/controller/audit/`

Expected: FAIL — the build reports `undefined: Ingester`, `undefined: NewIngester` and `undefined: IngesterConfig`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/audit [github.com/darkraise/darkpanel/internal/controller/audit.test]
internal/controller/audit/ingest_test.go:97:12: undefined: Ingester
internal/controller/audit/ingest_test.go:108:36: undefined: Ingester
internal/controller/audit/ingest_test.go:109:9: undefined: NewIngester
internal/controller/audit/ingest_test.go:109:21: undefined: IngesterConfig
internal/controller/audit/ingest_test.go:159:10: undefined: journalPage
internal/controller/audit/ingest_test.go:163:68: undefined: journalPage
internal/controller/audit/ingest_test.go:164:45: undefined: journalPage
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/audit/ingest.go`:

```go
package audit

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// IngestInterval is how often a started Ingester reads every host's journal
// (spec section 20).
const IngestInterval = 5 * time.Second

// journalPage is how many journal entries one Journal call asks for.
const journalPage = 500

// IngesterConfig wires an Ingester.
type IngesterConfig struct {
	Store  *Store             // the controller's audit log, whose database lists the hosts
	Agents core.HostDirectory // the agents whose journals are read
	Clock  core.Clock
	Log    *slog.Logger
}

// Ingester copies agent journals into core_audit (ADR 0003, spec section
// 11): it reads the entries after the host's core_journal_cursor, stores them
// with the cursor in one transaction, and then acknowledges the cursor so the
// agent deletes them. Its methods are safe for concurrent use; ingestion runs
// one at a time.
type Ingester struct {
	cfg IngesterConfig

	// mu is held for a whole ingestion run, so runs never overlap and Stop
	// waits for a running one.
	mu      sync.Mutex
	acked   map[string]int64 // the last sequence acknowledged per host by this process
	timer   core.Timer
	started bool
	stopped bool
}

// NewIngester returns an ingester for cfg. Call Start to ingest every
// IngestInterval.
func NewIngester(cfg IngesterConfig) *Ingester {
	return &Ingester{cfg: cfg, acked: map[string]int64{}}
}

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
func (in *Ingester) IngestAll(ctx context.Context) error {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return nil
	}
	return in.ingestAll(ctx)
}

// Start ingests every host each IngestInterval until Stop. A second Start
// does nothing.
func (in *Ingester) Start() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.started || in.stopped {
		return
	}
	in.started = true
	in.timer = in.cfg.Clock.AfterFunc(IngestInterval, in.tick)
}

// Stop ends the loop and waits for a running ingestion to finish. Later
// calls ingest nothing.
func (in *Ingester) Stop() {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.stopped = true
	if in.timer != nil {
		in.timer.Stop()
	}
}

func (in *Ingester) tick() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.stopped {
		return
	}
	if err := in.ingestAll(context.Background()); err != nil {
		in.cfg.Log.Warn("audit: journal ingestion", "error", err)
	}
	in.timer = in.cfg.Clock.AfterFunc(IngestInterval, in.tick)
}

func (in *Ingester) ingestAll(ctx context.Context) error {
	rows, err := in.cfg.Store.db.Read().QueryContext(ctx, "SELECT id FROM core_hosts ORDER BY id")
	if err != nil {
		return fmt.Errorf("audit: list hosts: %w", err)
	}
	var hosts []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return fmt.Errorf("audit: list hosts: %w", err)
		}
		hosts = append(hosts, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("audit: list hosts: %w", err)
	}
	var errs []error
	for _, id := range hosts {
		if err := in.ingestHost(ctx, id); err != nil {
			errs = append(errs, fmt.Errorf("audit: ingest host %s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// ingestHost runs under mu.
func (in *Ingester) ingestHost(ctx context.Context, hostID string) error {
	agent, err := in.cfg.Agents.Agent(ctx, hostID)
	if err != nil {
		return err
	}
	cursor, err := in.cfg.Store.cursor(ctx, hostID)
	if err != nil {
		return err
	}
	for {
		entries, err := agent.Journal(ctx, cursor, journalPage)
		if err != nil {
			return fmt.Errorf("audit: read the journal of %s: %w", hostID, err)
		}
		if len(entries) == 0 {
			break
		}
		prev := cursor
		for _, e := range entries {
			if e.Seq <= prev {
				return fmt.Errorf("audit: the journal of %s returned sequence %d after %d", hostID, e.Seq, prev)
			}
			prev = e.Seq
		}
		if err := in.cfg.Store.advance(ctx, hostID, entries, prev); err != nil {
			return err
		}
		cursor = prev
		if len(entries) < journalPage {
			break
		}
	}
	// A previous process may have stored entries and stopped before its Ack,
	// so the first run of this process acknowledges the stored cursor too.
	if cursor > in.acked[hostID] {
		if err := agent.Ack(ctx, cursor); err != nil {
			return fmt.Errorf("audit: acknowledge the journal of %s: %w", hostID, err)
		}
		in.acked[hostID] = cursor
	}
	return nil
}

// cursor is the highest journal sequence of hostID stored in core_audit, or
// 0 before the first ingestion.
func (s *Store) cursor(ctx context.Context, hostID string) (int64, error) {
	var seq int64
	err := s.db.Read().QueryRowContext(ctx,
		"SELECT acked_seq FROM core_journal_cursor WHERE host_id = ?", hostID).Scan(&seq)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return 0, nil
	case err != nil:
		return 0, fmt.Errorf("audit: read the journal cursor of %s: %w", hostID, err)
	}
	return seq, nil
}

// advance stores entries of hostID and moves its cursor to seq in one
// transaction, so a crash between the two can neither lose nor repeat rows.
func (s *Store) advance(ctx context.Context, hostID string, entries []core.JournalEntry, seq int64) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if err := s.ingest(ctx, tx, hostID, entries); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_journal_cursor (host_id, acked_seq) VALUES (?, ?)
			 ON CONFLICT (host_id) DO UPDATE SET acked_seq = max(acked_seq, excluded.acked_seq)`, hostID, seq)
		if err != nil {
			return fmt.Errorf("audit: advance the journal cursor of %s: %w", hostID, err)
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'Ingest|Stop' ./internal/controller/audit/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/audit`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/audit/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/audit/ingest.go internal/controller/audit/ingest_test.go
git commit -m "feat(audit): ingest agent journals"
```


---

### Task 14: Agent runtime start and stop

**Files:**
- Create: `internal/agent/runtime/runtime.go`
- Test: `internal/agent/runtime/fixture_test.go`
- Test: `internal/agent/runtime/runtime_test.go`

**Interfaces:**
- Consumes: Contracts › Task 4 (`schema.Redact`) and Task 10 (`lifecycle.Protect`); `store.Migrate`, `store.CoreMigrations`, `lifecycle.New`, `lifecycle.NewAgentJournal`, `Engine.Recover`, `Purge`, `Close`, `schema.NewValidator`, `platform.NewFiles`, `platform.OwnedPaths`, `platform.NewOwnership`, `platform.NewRunner`
- Produces: Contracts › `internal/agent/runtime` Task 14 (`Config`, `Runtime`, `Open`, `Close`)

**Items:** 51, 5

**Depends on:** Task 4, Task 10

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The agent half's lifecycle (spec sections 1 and 3, register row 51): `Open` migrates agent.db (a failed module migration makes only that module unavailable), builds the agent's one `platform.Files`, the engine with `Base.Clock` set, runs `Recover` once, the modules' preflight checks and `Purge`, then purges every 10 minutes until `Close`. The tests use fixture modules registered only here and check the config, the migrations and statuses (including a failed migration that is logged and skips preflight, and a panicking check), the handler `Env`, recovery and purge across a restart, and the purge loop on the fake clock.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/runtime/fixture_test.go`:

```go
package runtime

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The fixture modules are registered only in these tests:
//   - kv keeps items in its own agent table kv_items, with a read, a
//     revertible set and a resource;
//   - bad has a migration that fails, so it is never usable;
//   - flaky fails its preflight check until the host is ready;
//   - boom panics in its preflight check and in its resource.

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const testHost = "h1"

var alice = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeOperate, Source: "192.0.2.7"}

// fixHost is what the fixture modules' checks look at.
type fixHost struct {
	mu        sync.Mutex
	ready     bool // flaky's preflight passes
	badChecks int  // runs of bad's preflight check
}

func (h *fixHost) isReady() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.ready
}

type itemIn struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type itemOut struct {
	Value    string `json:"value"`
	Actor    string `json:"actor"`
	HostID   string `json:"hostId"`
	HasFiles bool   `json:"hasFiles"`
	HasExec  bool   `json:"hasExec"`
}

type status struct {
	Count int    `json:"count"`
	Token string `json:"token" secret:"true"`
}

func readItem(ctx context.Context, env core.Env, name string) (string, bool, error) {
	var v string
	err := env.DB.Read().QueryRowContext(ctx, "SELECT value FROM kv_items WHERE name = ?", name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

type getItem struct{}

func (getItem) Read(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return itemOut{Value: v, Actor: env.Principal.Actor, HostID: env.HostID,
		HasFiles: env.Files != nil, HasExec: env.Exec != nil}, err
}

type itemSnap struct {
	Name, Value string
	Present     bool
}

type setItem struct{}

func (setItem) Plan(ctx context.Context, env core.Env, in itemIn) (core.PlanOutcome, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}}, StateHash: v}, err
}

func (setItem) Apply(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Value}, env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv_items (name, value) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET value = excluded.value`, in.Name, in.Value)
		return err
	})
}

func (setItem) Verify(ctx context.Context, env core.Env, in itemIn, _ itemOut) error {
	v, _, err := readItem(ctx, env, in.Name)
	if err == nil && v != in.Value {
		err = fmt.Errorf("item %s is %q", in.Name, v)
	}
	return err
}

func (setItem) Snapshot(ctx context.Context, env core.Env, in itemIn) ([]byte, error) {
	v, ok, err := readItem(ctx, env, in.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(itemSnap{Name: in.Name, Value: v, Present: ok})
}

func (setItem) Revert(ctx context.Context, env core.Env, snapshot []byte) error {
	var s itemSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		if !s.Present {
			_, err := tx.ExecContext(ctx, "DELETE FROM kv_items WHERE name = ?", s.Name)
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE kv_items SET value = ? WHERE name = ?", s.Value, s.Name)
		return err
	})
}

var (
	refGet = core.ActionRef{Name: "kv.item.get", Version: 1}
	refSet = core.ActionRef{Name: "kv.item.set", Version: 1}
)

type kvModule struct{}

func (kvModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "kv", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: refGet.Name, Version: 1, Side: core.SideAgent}, getItem{}),
			core.DefineRevertible(core.ActionSpec{Name: refSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"kv"}}, setItem{}),
		},
		Resources: []core.ResourceDef{core.DefineResource("kv.status", func(ctx context.Context, env core.Env) (status, error) {
			var n int
			err := env.DB.Read().QueryRowContext(ctx, "SELECT count(*) FROM kv_items").Scan(&n)
			return status{Count: n, Token: "kv-secret-token"}, err
		})},
		Migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_kv.sql": {Data: []byte("CREATE TABLE kv_items (name TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;")},
		}},
	}
}

// echo is the read handler of the modules that only need an action.
type echo struct{}

func (echo) Read(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Name}, nil
}

type badModule struct{ host *fixHost }

func (m badModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "bad", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: "bad.item.get", Version: 1, Side: core.SideAgent}, echo{})},
		Preflight: []core.PreflightCheck{{ID: "counted", Run: func(context.Context, core.Env) error {
			m.host.mu.Lock()
			defer m.host.mu.Unlock()
			m.host.badChecks++
			return nil
		}}},
		Migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_bad.sql": {Data: []byte("CREATE TABLE bad_items (")},
		}},
	}
}

type flakyModule struct{ host *fixHost }

func (m flakyModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "flaky", Version: 2,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: "flaky.item.get", Version: 1, Side: core.SideAgent}, echo{})},
		Preflight: []core.PreflightCheck{{ID: "ready", Run: func(context.Context, core.Env) error {
			if !m.host.isReady() {
				return errors.New("flaky: the host is not ready")
			}
			return nil
		}}},
	}
}

type boomModule struct{}

func (boomModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "boom", Version: 1,
		Resources: []core.ResourceDef{core.DefineResource("boom.status", func(context.Context, core.Env) (status, error) {
			panic("boom resource")
		})},
		Preflight: []core.PreflightCheck{{ID: "explode", Run: func(context.Context, core.Env) error {
			panic("boom preflight")
		}}},
	}
}

// fixture is a runtime over a temporary agent database.
type fixture struct {
	t     *testing.T
	host  *fixHost
	clock *coretest.FakeClock
	reg   *core.Registry
	dir   string
	db    *store.DB
	rt    *Runtime
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := &fixHost{}
	reg, err := core.NewRegistry(kvModule{}, badModule{h}, flakyModule{h}, boomModule{})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{t: t, host: h, clock: coretest.NewFakeClock(t0), reg: reg, dir: t.TempDir()}
	f.open()
	return f
}

func (f *fixture) config() Config {
	return Config{
		DB: f.db, Sealer: coretest.Sealer{}, Clock: f.clock, Log: slog.New(slog.DiscardHandler), Registry: f.reg,
		HostID: testHost, BinaryVersion: "test", BackupDir: filepath.Join(f.dir, "backups"),
		Exec: platform.NewRunner(nil), FilesTestMode: true,
	}
}

// open opens the database and a runtime over it, as a starting agent does.
func (f *fixture) open() {
	f.t.Helper()
	db, err := store.Open(context.Background(), filepath.Join(f.dir, "agent.db"))
	if err != nil {
		f.t.Fatal(err)
	}
	f.db = db
	f.t.Cleanup(func() { _ = db.Close() })
	rt, err := Open(context.Background(), f.config())
	if err != nil {
		f.t.Fatalf("Open: %v", err)
	}
	f.rt = rt
	f.t.Cleanup(rt.Close)
}

// restart closes the runtime and the database and opens both again.
func (f *fixture) restart() {
	f.t.Helper()
	f.rt.Close()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.open()
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}
```

Create `internal/agent/runtime/runtime_test.go`:

```go
package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

func TestOpenRequiresItsConfig(t *testing.T) {
	f := newFixture(t)
	breaks := map[string]func(*Config){
		"DB":            func(c *Config) { c.DB = nil },
		"Sealer":        func(c *Config) { c.Sealer = nil },
		"Clock":         func(c *Config) { c.Clock = nil },
		"Log":           func(c *Config) { c.Log = nil },
		"Registry":      func(c *Config) { c.Registry = nil },
		"HostID":        func(c *Config) { c.HostID = "" },
		"BinaryVersion": func(c *Config) { c.BinaryVersion = "" },
		"BackupDir":     func(c *Config) { c.BackupDir = "" },
		"Exec":          func(c *Config) { c.Exec = nil },
	}
	for name, brk := range breaks {
		c := f.config()
		brk(&c)
		if _, err := Open(context.Background(), c); err == nil || !strings.Contains(err.Error(), "Config."+name) {
			t.Errorf("%s: Open = %v, want an error naming Config.%s", name, err, name)
		}
	}
}

func TestOpenMigratesAndChecksEveryModule(t *testing.T) {
	f := newFixture(t)
	if n := f.count("SELECT count(*) FROM sqlite_master WHERE name = 'kv_items'"); n != 1 {
		t.Error("kv's agent migration did not run")
	}
	if n := f.count("SELECT count(*) FROM pragma_table_info('core_journal') WHERE name = 'source'"); n != 1 {
		t.Error("the core agent migrations did not run")
	}
	want := []core.ModuleStatus{
		{ID: "kv", Version: 1, Available: true},
		{ID: "bad", Version: 1, Reason: ReasonMigrationFailed},
		{ID: "flaky", Version: 2, Reason: "flaky: the host is not ready"},
		{ID: "boom", Version: 1, Reason: "preflight check explode of boom panicked"},
	}
	got := f.rt.statuses()
	if len(got) != len(want) {
		t.Fatalf("statuses = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("status %d = %+v, want %+v", i, got[i], want[i])
		}
	}
	if f.host.badChecks != 0 {
		t.Errorf("bad's preflight ran %d times, want 0 after its migration failed", f.host.badChecks)
	}
}

func TestOpenLogsAFailedModuleMigration(t *testing.T) {
	f := newFixture(t)
	f.rt.Close()
	var buf bytes.Buffer
	c := f.config()
	c.Log = slog.New(slog.NewTextHandler(&buf, nil))
	rt, err := Open(context.Background(), c)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// The failed migration is retried on every start and logged with its cause.
	if text := buf.String(); !strings.Contains(text, "runtime: module migrations failed") ||
		!strings.Contains(text, "module=bad") || !strings.Contains(text, "0001_bad") {
		t.Errorf("log does not report bad's migration:\n%s", text)
	}
}

func TestHandlersGetTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	raw, err := f.rt.engine.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	var out itemOut
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if out.HostID != testHost || out.Actor != alice.Actor || !out.HasFiles || !out.HasExec {
		t.Errorf("handler saw %+v, want host h1, alice, files and exec", out)
	}
}

func TestOpenRecoversAndPurges(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	v, err := f.rt.engine.Apply(ctx, alice, pl.PlanID, core.ApplyOptions{ConfirmTimeout: lifecycle.MaxConfirmTimeout})
	if err != nil || v.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v; want pending", v, err)
	}
	unused, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"b","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.rt.Close() // stops the purge loop and the revert timer; the change stays pending
	f.clock.Advance(lifecycle.PlanTTL)
	f.restart()

	got, err := f.rt.engine.Change(ctx, v.ID)
	if err != nil || got.State != core.StateReverted {
		t.Errorf("change after restart = %+v, %v; want reverted by recovery", got, err)
	}
	if n := f.count("SELECT count(*) FROM kv_items"); n != 0 {
		t.Errorf("kv_items rows = %d, want 0 after the revert", n)
	}
	if n := f.count("SELECT count(*) FROM core_journal WHERE phase = 'recover' AND change_id = ?", v.ID); n != 1 {
		t.Errorf("recover entries = %d, want 1", n)
	}
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", unused.PlanID); n != 0 {
		t.Errorf("the expired unused plan was kept")
	}
}

func TestPurgeRunsEveryIntervalUntilClose(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if p := f.clock.Pending(); p != 1 {
		t.Fatalf("armed timers = %d, want the purge timer", p)
	}
	pl, err := f.rt.engine.Plan(ctx, alice, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(PurgeInterval)
	if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", pl.PlanID); n != 0 {
		t.Error("the purge loop kept an expired plan")
	}
	if p := f.clock.Pending(); p != 1 {
		t.Errorf("armed timers after a purge = %d, want 1", p)
	}
	f.rt.Close()
	if p := f.clock.Pending(); p != 0 {
		t.Errorf("armed timers after Close = %d, want 0", p)
	}
	f.rt.Close() // a second Close is harmless
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestOpen|TestHandlers|TestPurge' ./internal/agent/runtime/`

Expected: FAIL — the build reports `undefined: Runtime`, `undefined: Config` and `undefined: Open`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/agent/runtime [github.com/darkraise/darkpanel/internal/agent/runtime.test]
internal/agent/runtime/fixture_test.go:222:9: undefined: Runtime
internal/agent/runtime/fixture_test.go:237:28: undefined: Config
internal/agent/runtime/fixture_test.go:238:9: undefined: Config
internal/agent/runtime/fixture_test.go:254:13: undefined: Open
internal/agent/runtime/runtime_test.go:17:29: undefined: Config
internal/agent/runtime/runtime_test.go:18:28: undefined: Config
internal/agent/runtime/runtime_test.go:19:28: undefined: Config
```

- [ ] **Step 3: Write the implementation**

Create `internal/agent/runtime/runtime.go`:

```go
// Package runtime is the agent half of Darkpanel (spec sections 1 and 3): it
// owns agent.db and its migrations, the agent lifecycle engine, module
// preflight checks and resource reads, and serves core.AgentClient in
// process. In M2 the mTLS transport serves the same methods.
package runtime

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// ProtocolVersion is the agent protocol Hello reports.
const ProtocolVersion = 1

// PurgeInterval is how often expired plans are deleted (spec section 20).
const PurgeInterval = 10 * time.Minute

// BackupsKept is how many pre-migration backups of agent.db are kept.
const BackupsKept = 5

// ReasonMigrationFailed is the reason of a module whose agent migrations
// failed. The error itself is logged, because it may quote SQL.
const ReasonMigrationFailed = "the module's agent migrations failed; see the service log"

// Config is what the agent half needs, already resolved by the server.
type Config struct {
	DB            *store.DB      // agent.db, opened; Open migrates it
	Sealer        core.Sealer    // the agent key's sealer
	Clock         core.Clock     // also handed to handlers (hostinfo needs it)
	Log           *slog.Logger   // the service logger
	Registry      *core.Registry // the one registry of the process
	HostID        string         // the local host id
	BinaryVersion string         // buildinfo.Version, for the migration backup
	BackupDir     string         // paths.AgentBackups()
	Exec          core.Runner    // platform.NewRunner(platform.ProductionAllowlist()) in production
	FilesTestMode bool           // platform.FilesConfig.TestMode; false in production
}

func (c Config) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"DB", c.DB != nil}, {"Sealer", c.Sealer != nil}, {"Clock", c.Clock != nil}, {"Log", c.Log != nil},
		{"Registry", c.Registry != nil}, {"HostID", c.HostID != ""}, {"BinaryVersion", c.BinaryVersion != ""},
		{"BackupDir", c.BackupDir != ""}, {"Exec", c.Exec != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("runtime: Config.%s is required", r.name)
		}
	}
	return nil
}

// Runtime is the agent half of one host. Its methods are safe for concurrent
// use.
type Runtime struct {
	cfg     Config
	base    core.Env
	engine  *lifecycle.Engine
	journal *lifecycle.AgentJournal

	statusMu sync.Mutex
	status   map[string]core.ModuleStatus
	broken   map[string]bool // modules whose migrations failed; unusable until a restart

	// loopMu is held while a purge runs, so Close waits for it.
	loopMu sync.Mutex
	timer  core.Timer
	closed bool
}

// Open migrates agent.db (core first, then each module's agent migrations; a
// failed module migration makes that module unavailable), builds the one
// platform.Files of the agent from the registry's owned paths, builds the
// agent lifecycle engine, runs its Recover once, runs every module's
// preflight checks, purges expired plans and then every PurgeInterval. Call
// Close on shutdown.
func Open(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	r := &Runtime{cfg: cfg, status: map[string]core.ModuleStatus{}, broken: map[string]bool{}}
	if err := r.migrate(ctx); err != nil {
		return nil, err
	}
	files := platform.NewFiles(platform.FilesConfig{
		Owned:     platform.OwnedPaths(cfg.Registry.Modules()),
		Ownership: platform.NewOwnership(cfg.DB),
		Clock:     cfg.Clock,
		TestMode:  cfg.FilesTestMode,
	})
	r.base = core.Env{
		HostID: cfg.HostID, DB: cfg.DB, Exec: cfg.Exec, Files: files, Sealer: cfg.Sealer, Clock: cfg.Clock, Log: cfg.Log,
	}
	r.journal = lifecycle.NewAgentJournal(cfg.DB)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideAgent, Registry: cfg.Registry, Validator: schema.NewValidator(), Journal: r.journal,
		Redact: schema.Redact, Base: r.base,
	})
	if err != nil {
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.engine = eng
	if err := eng.Recover(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: recover: %w", err)
	}
	for _, m := range r.modules() {
		r.setStatus(r.check(ctx, m))
	}
	if err := eng.Purge(ctx); err != nil {
		eng.Close()
		return nil, fmt.Errorf("runtime: %w", err)
	}
	r.timer = cfg.Clock.AfterFunc(PurgeInterval, r.purge)
	return r, nil
}

// Close stops the purge loop, waiting for a running purge, and closes the
// engine, which waits for a running timer revert. The caller closes the
// database afterwards.
func (r *Runtime) Close() {
	r.loopMu.Lock()
	r.closed = true
	if r.timer != nil {
		r.timer.Stop()
	}
	r.loopMu.Unlock()
	r.engine.Close()
}

func (r *Runtime) purge() {
	r.loopMu.Lock()
	defer r.loopMu.Unlock()
	if r.closed {
		return
	}
	if err := r.engine.Purge(context.Background()); err != nil {
		r.cfg.Log.Warn("runtime: purge", "error", err)
	}
	r.timer = r.cfg.Clock.AfterFunc(PurgeInterval, r.purge)
}

// modules returns the registered modules except the core, which has no agent
// half, in registration order.
func (r *Runtime) modules() []core.Manifest {
	var out []core.Manifest
	for _, m := range r.cfg.Registry.Modules() {
		if m.ID != core.ModuleIDCore {
			out = append(out, m)
		}
	}
	return out
}

func (r *Runtime) migrate(ctx context.Context) error {
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		return fmt.Errorf("runtime: %w", err)
	}
	var sets []store.MigrationSet
	for _, m := range r.modules() {
		if fsys := m.Migrations[core.SideAgent]; fsys != nil {
			sets = append(sets, store.MigrationSet{Module: m.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, r.cfg.DB, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: r.cfg.BinaryVersion, BackupRoot: r.cfg.BackupDir,
		Keep: BackupsKept, Now: r.cfg.Clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return fmt.Errorf("runtime: agent migrations: %w", err)
	}
	if res.BackupDir != "" {
		r.cfg.Log.Info("runtime: agent database backed up before migrating", "dir", res.BackupDir)
	}
	for _, f := range res.Failed {
		r.cfg.Log.Error("runtime: module migrations failed", "module", f.Module, "error", f.Err)
		r.broken[f.Module] = true
	}
	return nil
}

// check runs m's preflight checks in order; the first failure's error text is
// the reason. A module whose migrations failed is not checked.
func (r *Runtime) check(ctx context.Context, m core.Manifest) core.ModuleStatus {
	st := core.ModuleStatus{ID: m.ID, Version: m.Version}
	if r.isBroken(m.ID) {
		st.Reason = ReasonMigrationFailed
		return st
	}
	for _, c := range m.Preflight {
		err := lifecycle.Protect(r.cfg.Log, m.ID, "preflight check "+c.ID, func() error { return c.Run(ctx, r.base) })
		if err != nil {
			st.Reason = err.Error()
			return st
		}
	}
	st.Available = true
	return st
}

func (r *Runtime) isBroken(moduleID string) bool {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	return r.broken[moduleID]
}

func (r *Runtime) setStatus(st core.ModuleStatus) {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	r.status[st.ID] = st
}

// statuses returns every module's last status in registration order.
func (r *Runtime) statuses() []core.ModuleStatus {
	r.statusMu.Lock()
	defer r.statusMu.Unlock()
	out := []core.ModuleStatus{}
	for _, m := range r.modules() {
		if st, ok := r.status[m.ID]; ok {
			out = append(out, st)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestOpen|TestHandlers|TestPurge' ./internal/agent/runtime/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/agent/runtime`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/agent/runtime/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runtime/runtime.go internal/agent/runtime/fixture_test.go internal/agent/runtime/runtime_test.go
git commit -m "feat(agent): open the agent runtime"
```


---

### Task 15: HTTPS fingerprint pinning

**Files:**
- Create: `internal/cli/pin.go`
- Test: `internal/cli/pin_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (package doc comment only); the standard library (`crypto/tls`, `crypto/x509`, `crypto/sha256`).
- Produces: Contracts › Task 15 (`requestTimeout`, `newHTTPClient`, `pinnedTLS`; test helpers `testCert`, `newCert`, `newTestCA`, `newTestLeaf`, `serveTLS`, `get`).

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Spec section 15: without `caFingerprint` the CLI verifies against the system roots; with it, it trusts only the presented CA certificate whose SHA-256 is the fingerprint, and verifies the leaf against that root for the dialled host or IP, so leaf renewals need no new pin. The check runs in `VerifyConnection`, not `VerifyPeerCertificate`, because only `VerifyConnection` also runs on resumed TLS sessions (gosec G123). The tests make a CA and leaves with `crypto/x509` and serve them with `httptest.NewUnstartedServer` on 127.0.0.1: the right fingerprint (any case) passes; a wrong fingerprint, the fingerprint of the non-CA leaf, a leaf for another IP, and no fingerprint against a private root all fail; a malformed fingerprint is refused before any connection.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/pin_test.go`:

```go
package cli

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

// testCert is a certificate made in the test with its key.
type testCert struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey
}

func (c testCert) fingerprint() string {
	sum := sha256.Sum256(c.cert.Raw)
	return hex.EncodeToString(sum[:])
}

// newCert makes a certificate signed by parent, or a self-signed one when
// parent is nil.
func newCert(t *testing.T, tmpl *x509.Certificate, parent *testCert) testCert {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, signerCert := key, tmpl
	if parent != nil {
		signer, signerCert = parent.key, parent.cert
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, signerCert, &key.PublicKey, signer)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return testCert{cert: cert, key: key}
}

func newTestCA(t *testing.T) testCert {
	return newCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "darkpanel web root test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, MaxPathLenZero: true,
		KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}, nil)
}

func newTestLeaf(t *testing.T, ca testCert, ip string) testCert {
	return newCert(t, &x509.Certificate{
		SerialNumber: big.NewInt(2), Subject: pkix.Name{CommonName: "darkpanel"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP(ip)},
	}, &ca)
}

// serveTLS starts an HTTPS server on 127.0.0.1 presenting [leaf, ca].
func serveTLS(t *testing.T, leaf, ca testCert) *url.URL {
	t.Helper()
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	srv.Config.ErrorLog = log.New(io.Discard, "", 0) // refused handshakes are expected
	srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{{
		Certificate: [][]byte{leaf.cert.Raw, ca.cert.Raw}, PrivateKey: leaf.key,
	}}}
	srv.StartTLS()
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}

func get(t *testing.T, u *url.URL, fingerprint string) error {
	t.Helper()
	c, err := newHTTPClient(u, fingerprint)
	if err != nil {
		t.Fatalf("newHTTPClient: %v", err)
	}
	resp, err := c.Get(u.String())
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	return nil
}

func TestPinnedCA(t *testing.T) {
	ca := newTestCA(t)
	u := serveTLS(t, newTestLeaf(t, ca, "127.0.0.1"), ca)
	if err := get(t, u, ca.fingerprint()); err != nil {
		t.Errorf("right fingerprint: %v", err)
	}
	if err := get(t, u, strings.ToUpper(ca.fingerprint())); err != nil {
		t.Errorf("upper-case fingerprint: %v", err)
	}
}

func TestPinnedCARefusals(t *testing.T) {
	ca := newTestCA(t)
	leaf := newTestLeaf(t, ca, "127.0.0.1")
	other := newTestCA(t)
	cases := []struct {
		name        string
		u           *url.URL
		fingerprint string
		want        string
	}{
		{"wrong fingerprint", serveTLS(t, leaf, ca), other.fingerprint(), "matches caFingerprint"},
		{"fingerprint of the leaf", serveTLS(t, leaf, ca), leaf.fingerprint(), "is not a CA"},
		{"leaf for another address", serveTLS(t, newTestLeaf(t, ca, "192.0.2.1"), ca), ca.fingerprint(), "192.0.2.1"},
		{"no fingerprint and a private root", serveTLS(t, leaf, ca), "", "certificate"},
	}
	for _, c := range cases {
		err := get(t, c.u, c.fingerprint)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want one containing %q", c.name, err, c.want)
		}
	}
}

func TestNewHTTPClientRefusesBadFingerprint(t *testing.T) {
	u, _ := url.Parse("https://127.0.0.1:8443")
	for _, fp := range []string{"abc", strings.Repeat("g", 64), strings.Repeat("ab:", 32)} {
		if _, err := newHTTPClient(u, fp); err == nil || !strings.Contains(err.Error(), "64 hexadecimal digits") {
			t.Errorf("fingerprint %q: error %v", fp, err)
		}
	}
	plain, _ := url.Parse("http://127.0.0.1:8080")
	if _, err := newHTTPClient(plain, strings.Repeat("ab", 32)); err != nil {
		t.Errorf("plain HTTP with a fingerprint: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestPinnedCA|TestPinnedCARefusals|TestNewHTTPClientRefusesBadFingerprint)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/pin_test.go:97:12: undefined: newHTTPClient` (and the same at lines 149 and 154), then `FAIL github.com/darkraise/darkpanel/internal/cli [build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/pin.go`:

```go
package cli

import (
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// requestTimeout bounds one API request, so a hung server cannot hang the
// CLI; it exceeds the server's 60-second write timeout.
const requestTimeout = 90 * time.Second

// newHTTPClient returns the client for the server at base. Plain HTTP and
// HTTPS without a fingerprint use Go's defaults, so HTTPS verifies against
// the system roots. With a fingerprint, HTTPS trusts only the presented CA
// certificate whose SHA-256 is that fingerprint (spec section 15).
func newHTTPClient(base *url.URL, fingerprint string) (*http.Client, error) {
	if fingerprint != "" {
		if b, err := hex.DecodeString(fingerprint); err != nil || len(b) != sha256.Size {
			return nil, errors.New("caFingerprint must be 64 hexadecimal digits, the SHA-256 that darkpanel controller web-ca prints")
		}
	}
	if base.Scheme != "https" || fingerprint == "" {
		return &http.Client{Timeout: requestTimeout}, nil
	}
	tr, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, errors.New("http.DefaultTransport is not an *http.Transport")
	}
	tr = tr.Clone()
	tr.TLSClientConfig = pinnedTLS(strings.ToLower(fingerprint), base.Hostname())
	return &http.Client{Transport: tr, Timeout: requestTimeout}, nil
}

// pinnedTLS replaces Go's verification, which would require a system root,
// with one rooted at the pinned CA: the leaf must chain to the presented
// certificate whose SHA-256 is fingerprint, that certificate must be a CA,
// and the leaf must be valid for host. Leaf renewals need no new pin. The
// check runs in VerifyConnection rather than VerifyPeerCertificate because
// only VerifyConnection also runs on resumed sessions.
func pinnedTLS(fingerprint, host string) *tls.Config {
	return &tls.Config{
		MinVersion: tls.VersionTLS12,
		//nolint:gosec // G402: VerifyConnection below verifies the chain against the pinned root and the host
		InsecureSkipVerify: true,
		VerifyConnection: func(cs tls.ConnectionState) error {
			certs := cs.PeerCertificates
			if len(certs) == 0 {
				return errors.New("the server presented no certificate")
			}
			var root *x509.Certificate
			intermediates := x509.NewCertPool()
			for _, c := range certs[1:] {
				intermediates.AddCert(c)
			}
			for _, c := range certs {
				sum := sha256.Sum256(c.Raw)
				if hex.EncodeToString(sum[:]) == fingerprint {
					root = c
				}
			}
			if root == nil {
				return fmt.Errorf("no certificate the server presented matches caFingerprint %s", fingerprint)
			}
			if !root.BasicConstraintsValid || !root.IsCA {
				return fmt.Errorf("the certificate matching caFingerprint %s is not a CA", fingerprint)
			}
			roots := x509.NewCertPool()
			roots.AddCert(root)
			if _, err := certs[0].Verify(x509.VerifyOptions{Roots: roots, Intermediates: intermediates, DNSName: host}); err != nil {
				return fmt.Errorf("server certificate: %w", err)
			}
			return nil
		},
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestPinnedCA|TestPinnedCARefusals|TestNewHTTPClientRefusesBadFingerprint)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/pin.go internal/cli/pin_test.go
git commit -m "feat(cli): pin the web CA by fingerprint"
```

### Task 16: Config file, connection precedence, API and socket clients

**Files:**
- Create: `internal/cli/config.go`
- Create: `internal/cli/client.go`
- Test: `internal/cli/client_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (`usagef`, `problemError`, `ExitCode`, `ranError`), Contracts › Task 15 (`newHTTPClient`, `requestTimeout`); `errcode.Code`, `errcode.FieldError`.
- Produces: Contracts › Task 16 (`EnvURL`, `EnvToken`, `DefaultURL`, `fileConfig`, `configPath`, `loadConfig`, `connectNetwork`, `firstOf`, `client`, `newSocketClient`, `client.do`, `answerError`; test helpers `exchange`, `fakeAPI`, `newFakeAPI`, `newFakeSocket`, `jsonEqual`, `writeConfig`, `envOf`, `homeAt`).

**Items:** 13

**Depends on:** Task 1, Task 15

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The CLI's one HTTP path: the URL from `--url`, `DARKPANEL_URL`, the config file, then `http://127.0.0.1:8080`; the token from `DARKPANEL_TOKEN`, then the config file; `Authorization: Bearer <token>` and, on every request with a body, `Content-Type: application/json`; problem details become `*problemError`. The config file holds a token and the URL it is sent to, so (controller ruling) it is opened once with `O_NOFOLLOW|O_NONBLOCK` and refused unless the open descriptor is a regular file owned by the effective uid with no group or other permission bit. The socket client dials a Unix socket and never sends a token. The test file also holds the fake API every later test uses: it asserts each request's method, path, query, bearer token, content type and JSON body in order and answers canned wire shapes.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/client_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// exchange is one request the fake API expects, in order, and its answer.
type exchange struct {
	method, path string // path includes the query string
	body         string // the expected JSON body; "" expects no body
	status       int    // 0 answers 200
	reply        string // the JSON answer; "" answers no body
	problem      bool   // answer as application/problem+json
}

// fakeAPI answers the expected exchanges in order and fails the test on any
// other request. Every request must carry Authorization: Bearer <token>, or
// no Authorization header when token is "".
type fakeAPI struct {
	t     *testing.T
	token string
	mu    sync.Mutex
	want  []exchange
	n     int
}

func (f *fakeAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n >= len(f.want) {
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.RequestURI())
		w.WriteHeader(http.StatusTeapot)
		return
	}
	x := f.want[f.n]
	f.n++
	if r.Method != x.method || r.URL.RequestURI() != x.path {
		f.t.Errorf("request %d: %s %s, want %s %s", f.n, r.Method, r.URL.RequestURI(), x.method, x.path)
	}
	wantAuth := ""
	if f.token != "" {
		wantAuth = "Bearer " + f.token
	}
	if got := r.Header.Get("Authorization"); got != wantAuth {
		f.t.Errorf("request %d (%s): Authorization %q, want %q", f.n, x.path, got, wantAuth)
	}
	body, _ := io.ReadAll(r.Body)
	wantType := ""
	if x.body != "" {
		wantType = "application/json"
		if !jsonEqual(body, []byte(x.body)) {
			f.t.Errorf("request %d (%s): body %s, want %s", f.n, x.path, body, x.body)
		}
	} else if len(body) > 0 {
		f.t.Errorf("request %d (%s): unexpected body %s", f.n, x.path, body)
	}
	if got := r.Header.Get("Content-Type"); got != wantType {
		f.t.Errorf("request %d (%s): Content-Type %q, want %q", f.n, x.path, got, wantType)
	}
	if x.reply != "" {
		w.Header().Set("Content-Type", "application/json")
		if x.problem {
			w.Header().Set("Content-Type", "application/problem+json")
		}
	}
	status := x.status
	if status == 0 {
		status = http.StatusOK
	}
	w.WriteHeader(status)
	_, _ = io.WriteString(w, x.reply)
}

func (f *fakeAPI) check() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.n != len(f.want) {
		f.t.Errorf("the fake API served %d of %d expected requests", f.n, len(f.want))
	}
}

// newFakeAPI serves want over HTTP on 127.0.0.1 and returns the base URL.
func newFakeAPI(t *testing.T, token string, want ...exchange) string {
	t.Helper()
	f := &fakeAPI{t: t, token: token, want: want}
	srv := httptest.NewServer(f)
	t.Cleanup(func() { srv.Close(); f.check() })
	return srv.URL
}

// newFakeSocket serves want on a Unix socket and returns its path. The
// socket carries no token.
func newFakeSocket(t *testing.T, want ...exchange) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "c.sock")
	ln, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeAPI{t: t, want: want}
	srv := &http.Server{Handler: f, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close(); f.check() })
	return path
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	return json.Unmarshal(a, &x) == nil && json.Unmarshal(b, &y) == nil && reflect.DeepEqual(x, y)
}

// writeConfig writes the CLI config file under home's .config with mode.
func writeConfig(t *testing.T, home, content string, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(home, ".config", "darkpanel", "cli.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func envOf(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func homeAt(dir string) func() (string, error) { return func() (string, error) { return dir, nil } }

func TestConnectNetworkPrecedence(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `{"url":"http://127.0.0.3:8080","token":"dpt_config"}`, 0o600)
	cases := []struct {
		name, flag          string
		env                 map[string]string
		wantBase, wantToken string
	}{
		{"flag first", "http://127.0.0.1:9000/", map[string]string{EnvURL: "http://127.0.0.2:8080", EnvToken: "dpt_env"},
			"http://127.0.0.1:9000", "dpt_env"},
		{"environment next", "", map[string]string{EnvURL: "http://127.0.0.2:8080"}, "http://127.0.0.2:8080", "dpt_config"},
		{"config file last", "", map[string]string{}, "http://127.0.0.3:8080", "dpt_config"},
	}
	for _, c := range cases {
		cl, err := connectNetwork(c.flag, envOf(c.env), homeAt(home))
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		if cl.base != c.wantBase || cl.token != c.wantToken {
			t.Errorf("%s: base %q token %q, want %q %q", c.name, cl.base, cl.token, c.wantBase, c.wantToken)
		}
	}
	cl, err := connectNetwork("", envOf(nil), homeAt(t.TempDir()))
	if err != nil || cl.base != DefaultURL || cl.token != "" {
		t.Errorf("no config: base %q token %q err %v, want %q and no token", cl.base, cl.token, err, DefaultURL)
	}
}

func TestConfigPath(t *testing.T) {
	home := t.TempDir()
	cases := []struct {
		xdg, want string
	}{
		{"/srv/config", "/srv/config/darkpanel/cli.json"},
		{"relative/config", filepath.Join(home, ".config", "darkpanel", "cli.json")},
		{"", filepath.Join(home, ".config", "darkpanel", "cli.json")},
	}
	for _, c := range cases {
		got, err := configPath(envOf(map[string]string{"XDG_CONFIG_HOME": c.xdg}), homeAt(home))
		if err != nil || got != c.want {
			t.Errorf("XDG_CONFIG_HOME %q: %q, %v; want %q", c.xdg, got, err, c.want)
		}
	}
}

func TestConfigFileRefusals(t *testing.T) {
	refused := func(name, home, path, want string) {
		t.Helper()
		_, err := connectNetwork("", envOf(nil), homeAt(home))
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "chmod 600 "+path) ||
			strings.Contains(err.Error(), "dpt_secret") {
			t.Errorf("%s: error %v, want a refusal saying %q and naming chmod 600 %s", name, err, want, path)
		}
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o620, 0o602, 0o610} {
		home := t.TempDir()
		path := writeConfig(t, home, `{"token":"dpt_secret"}`, mode)
		refused(fmt.Sprintf("mode %o", mode), home, path, "accessible by group or others")
	}
	home := t.TempDir()
	target := writeConfig(t, t.TempDir(), `{"token":"dpt_secret"}`, 0o600)
	link := filepath.Join(home, ".config", "darkpanel", "cli.json")
	if err := os.MkdirAll(filepath.Dir(link), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	refused("symbolic link", home, link, "symbolic link")
	if os.Geteuid() == 0 {
		home := t.TempDir()
		path := writeConfig(t, home, `{"token":"dpt_secret"}`, 0o600)
		if err := os.Chown(path, 65534, 65534); err != nil {
			t.Fatal(err)
		}
		refused("another owner", home, path, "not owned by the current user")
	}
	home = t.TempDir()
	writeConfig(t, home, `{"token":"dpt_secret","fingerprint":"x"}`, 0o600)
	if _, err := connectNetwork("", envOf(nil), homeAt(home)); err == nil || !strings.Contains(err.Error(), `"fingerprint"`) {
		t.Errorf("unknown field: error %v", err)
	}
	if _, err := connectNetwork("ftp://127.0.0.1", envOf(nil), homeAt(t.TempDir())); ExitCode(err) != ExitUsage {
		t.Errorf("ftp URL: error %v, want a usage error", err)
	}
}

func TestClientDo(t *testing.T) {
	base := newFakeAPI(t, "dpt_t1",
		exchange{method: "POST", path: "/api/v1/actions/core.host.list/v1/read", body: `{"input":{}}`,
			reply: `{"output":{"hosts":[]}}`},
		exchange{method: "GET", path: "/api/v1/changes?limit=5", reply: `{"changes":[]}`},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":""}}`,
			status: 422, problem: true,
			reply: `{"type":"about:blank","title":"Invalid input","status":422,"detail":"invalid input",` +
				`"code":"request.invalid_input","errors":[{"path":"/name","message":"too short"}]}`},
		exchange{method: "GET", path: "/api/v1/changes", status: 401, problem: true,
			reply: `{"type":"about:blank","title":"Authentication required","status":401,` +
				`"detail":"authentication required","code":"auth.required"}`},
		exchange{method: "GET", path: "/api/v1/changes/c1", status: 502},
	)
	c, err := connectNetwork(base, envOf(map[string]string{EnvToken: "dpt_t1"}), homeAt(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	var out struct {
		Output json.RawMessage `json:"output"`
	}
	if err := c.do(ctx, "POST", "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, &out); err != nil {
		t.Fatal(err)
	}
	if string(out.Output) != `{"hosts":[]}` {
		t.Errorf("output %s", out.Output)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes?limit=5", nil, nil); err != nil {
		t.Fatal(err)
	}
	err = c.do(ctx, "POST", "/api/v1/actions/core.token.create/v1/plan", map[string]any{"input": map[string]any{"name": ""}}, nil)
	var p *problemError
	if !errors.As(err, &p) || p.Code != errcode.InvalidInput || p.Status != 422 || len(p.Fields) != 1 || p.Fields[0].Path != "/name" {
		t.Errorf("422: error %#v", err)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes", nil, nil); ExitCode(ranError(err)) != ExitAuth {
		t.Errorf("401: error %v, want exit %d", err, ExitAuth)
	}
	if err := c.do(ctx, "GET", "/api/v1/changes/c1", nil, nil); err == nil || !strings.Contains(err.Error(), "502") {
		t.Errorf("502 without problem details: error %v", err)
	}
}

func TestSocketClient(t *testing.T) {
	path := newFakeSocket(t, exchange{method: "GET", path: "/api/v1/controller/web-ca",
		reply: `{"pem":"-----BEGIN CERTIFICATE-----\n","fingerprint":"ab"}`})
	var out struct {
		PEM         string `json:"pem"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := newSocketClient(path).do(context.Background(), "GET", "/api/v1/controller/web-ca", nil, &out); err != nil {
		t.Fatal(err)
	}
	if out.Fingerprint != "ab" {
		t.Errorf("fingerprint %q", out.Fingerprint)
	}
	missing := filepath.Join(t.TempDir(), "none.sock")
	if err := newSocketClient(missing).do(context.Background(), "GET", "/healthz", nil, nil); err == nil ||
		!strings.Contains(err.Error(), missing) {
		t.Errorf("missing socket: error %v, want one naming %s", err, missing)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestConnectNetworkPrecedence|TestConfigPath|TestConfigFileRefusals|TestClientDo|TestSocketClient)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/client_test.go:161:62: undefined: EnvURL`, then `undefined: EnvToken`, `undefined: connectNetwork`, `undefined: DefaultURL`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/config.go`:

```go
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// Connection settings (spec section 15).
const (
	EnvURL     = "DARKPANEL_URL"
	EnvToken   = "DARKPANEL_TOKEN"
	DefaultURL = "http://127.0.0.1:8080"
)

// fileConfig is the CLI config file, $XDG_CONFIG_HOME/darkpanel/cli.json.
type fileConfig struct {
	URL           string `json:"url"`
	Token         string `json:"token"`
	CAFingerprint string `json:"caFingerprint"`
}

// configPath is $XDG_CONFIG_HOME/darkpanel/cli.json, with ~/.config when
// the variable is unset or relative (the XDG specification ignores relative
// values).
func configPath(getenv func(string) string, home func() (string, error)) (string, error) {
	dir := getenv("XDG_CONFIG_HOME")
	if !filepath.IsAbs(dir) {
		h, err := home()
		if err != nil {
			return "", fmt.Errorf("XDG_CONFIG_HOME is unset and the home directory is unknown: %w", err)
		}
		dir = filepath.Join(h, ".config")
	}
	return filepath.Join(dir, "darkpanel", "cli.json"), nil
}

// maxConfigBytes bounds the config file the CLI reads.
const maxConfigBytes = 1 << 20

// loadConfig reads the config file at path; a missing file is the zero
// config. Because the file holds a token and the URL the token is sent to,
// it must be a regular file, not a symbolic link, owned by the effective
// user, with no group or other permission bits. The checks run on the open
// descriptor, so the file cannot be swapped between check and read.
func loadConfig(path string) (fileConfig, error) {
	var cfg fileConfig
	refuse := func(why string) (fileConfig, error) {
		return cfg, fmt.Errorf("the config file %s %s; run chmod 600 %s", path, why, path)
	}
	// O_NONBLOCK keeps a FIFO planted at path from blocking the open; the
	// regular-file check below then refuses it.
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0) //nolint:gosec // G304: the path is the user's own config file
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return cfg, nil
	case errors.Is(err, syscall.ELOOP):
		return refuse("is a symbolic link; replace it with a regular file")
	case err != nil:
		return cfg, fmt.Errorf("read the config file: %w", err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return cfg, fmt.Errorf("read the config file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return refuse("is not a regular file")
	}
	if info.Mode().Perm()&0o077 != 0 {
		return refuse("is accessible by group or others")
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if euid := os.Geteuid(); !ok || int64(st.Uid) != int64(euid) {
		return refuse(fmt.Sprintf("is not owned by the current user (uid %d)", euid))
	}
	dec := json.NewDecoder(io.LimitReader(f, maxConfigBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("the config file %s: %w", path, err)
	}
	return cfg, nil
}

// connectNetwork returns the client of the network API. The URL comes from
// flagURL, then DARKPANEL_URL, then the config file, then DefaultURL; the
// token from DARKPANEL_TOKEN, then the config file; the CA fingerprint from
// the config file. The config file is read whenever it exists.
func connectNetwork(flagURL string, getenv func(string) string, home func() (string, error)) (*client, error) {
	var cfg fileConfig
	if path, err := configPath(getenv, home); err == nil {
		if cfg, err = loadConfig(path); err != nil {
			return nil, err
		}
	}
	raw := firstOf(flagURL, getenv(EnvURL), cfg.URL, DefaultURL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, usagef("server URL %q: want http://<host>:<port> or https://<host>:<port>", raw)
	}
	hc, err := newHTTPClient(u, cfg.CAFingerprint)
	if err != nil {
		return nil, err
	}
	return &client{base: strings.TrimSuffix(u.String(), "/"), token: firstOf(getenv(EnvToken), cfg.Token), http: hc}, nil
}

func firstOf(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}
```

Create `internal/cli/client.go`:

```go
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxResponseBytes bounds the response body the CLI reads.
const maxResponseBytes = 16 << 20

// client talks to the network API with a bearer token, or to the controller
// socket without one.
type client struct {
	base  string // scheme and host, no trailing slash
	token string // "" sends no Authorization header
	http  *http.Client
}

// newSocketClient returns a client that reaches the controller socket at
// path (spec section 14); the peer credentials are the authentication.
func newSocketClient(path string) *client {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("controller socket %s: %w", path, err)
		}
		return c, nil
	}}
	return &client{base: "http://controller", http: &http.Client{Transport: tr, Timeout: requestTimeout}}
}

// do sends a request with body as JSON (nil sends none) and decodes a 2xx
// answer into out (nil discards it). An answer in problem details is a
// *problemError; any other failure is a plain error.
func (c *client) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode the request: %w", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes))
	if err != nil {
		return fmt.Errorf("%s %s: read the answer: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(method, path, resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: decode the answer: %w", method, path, err)
	}
	return nil
}

// answerError turns a non-2xx answer into a *problemError when it carries
// problem details with a code, else into a plain error naming the status.
func answerError(method, path string, resp *http.Response, data []byte) error {
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == "application/problem+json" {
		var p struct {
			Status int                  `json:"status"`
			Detail string               `json:"detail"`
			Code   errcode.Code         `json:"code"`
			Errors []errcode.FieldError `json:"errors"`
		}
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &problemError{Status: resp.StatusCode, Code: p.Code, Detail: p.Detail, Fields: p.Errors}
		}
	}
	return fmt.Errorf("%s %s: unexpected answer %s", method, path, resp.Status)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestConnectNetworkPrecedence|TestConfigPath|TestConfigFileRefusals|TestClientDo|TestSocketClient)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/config.go internal/cli/client.go internal/cli/client_test.go
git commit -m "feat(cli): add API and controller socket clients"
```

### Task 17: Input flags, --input and secret prompts

**Files:**
- Create: `internal/cli/input.go`
- Test: `internal/cli/input_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (`usagef`, `ExitCode`, `ranError`); `schema.Input` (merged `internal/core/schema`: properties in struct order, `required`, `enum`, `x-secret`, `$ref` into `$defs`).
- Produces: Contracts › Task 17 (`reservedFlags`, `terminal` with `line` and `secret`, `inputField`, `propSchema`, `scalarTypes`, `inputFields`, `kebab`, `addInputFlags`, `flagValue`, `collectInput`, `readInputDoc`, `member`, `members`).

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Spec section 15 input: scalar fields (string, integer, number, boolean, enum, arrays of scalars) become `--kebab-case` flags; anything nested comes from `--input <file|->`; secret fields never get a flag and are read without echo on a terminal, or one line each from standard input, in schema order. Flags override the `--input` document's fields; a missing required flag or nested field is a usage error naming it; a required boolean that is absent is false. A field whose flag would take one of the CLI's own flags fails generation. The helpers take `*cobra.Command`, not `*pflag.FlagSet`, because importing `pflag` directly would move it from an indirect to a direct requirement in `go.mod`. The tests cover the field list, every flag type, the merge order, terminal and pipe secrets and every usage error.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/input_test.go`:

```go
package cli

import (
	"bufio"
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

type sampleLimits struct {
	Max int `json:"max"`
}

type sampleInput struct {
	Name       string       `json:"name" jsonschema_description:"Rule name."`
	Port       int          `json:"port" jsonschema_description:"Port number."`
	Weight     float64      `json:"weight,omitempty"`
	Force      bool         `json:"force"`
	Mode       string       `json:"mode,omitempty" jsonschema:"enum=allow,enum=deny"`
	Tags       []string     `json:"tags,omitempty"`
	Ports      []int        `json:"ports,omitempty"`
	HostID     string       `json:"hostId,omitempty"`
	Limits     sampleLimits `json:"limits"`
	APIKey     string       `json:"apiKey" secret:"true"`
	Passphrase string       `json:"passphrase" secret:"true"`
}

func TestInputFields(t *testing.T) {
	fields, err := inputFields(reflect.TypeFor[sampleInput]())
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, f := range fields {
		s := f.Name + ":" + f.Flag + ":" + f.Type + ":" + f.Items
		if f.Required {
			s += ":required"
		}
		if f.Secret {
			s += ":secret"
		}
		if len(f.Enum) > 0 {
			s += ":" + strings.Join(f.Enum, "|")
		}
		got = append(got, s)
	}
	want := []string{
		"name:name:string::required", "port:port:integer::required", "weight:weight:number:", "force:force:boolean::required",
		"mode:mode:string::allow|deny", "tags:tags:array:string", "ports:ports:array:integer", "hostId:host-id:string:",
		"limits:limits:::required", "apiKey:api-key:string::required:secret", "passphrase:passphrase:string::required:secret",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("fields\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	for _, name := range []string{"name", "port", "weight", "force", "mode", "tags", "ports", "host-id", "input"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("no flag --%s", name)
		}
	}
	for _, name := range []string{"limits", "api-key", "apiKey", "passphrase"} {
		if cmd.Flags().Lookup(name) != nil {
			t.Errorf("unexpected flag --%s", name)
		}
	}
	if u := cmd.Flags().Lookup("mode").Usage; !strings.Contains(u, "allow, deny") {
		t.Errorf("--mode usage %q does not list the values", u)
	}
	if u := cmd.Flags().Lookup("name").Usage; !strings.Contains(u, "Rule name.") || !strings.Contains(u, "required") {
		t.Errorf("--name usage %q", u)
	}
}

func TestInputFieldsRefuseReservedFlags(t *testing.T) {
	type hostInput struct {
		Host string `json:"host"`
	}
	type yesInput struct {
		Yes bool `json:"yes,omitempty"`
	}
	type twiceInput struct {
		HostID  string `json:"hostId"`
		HostID2 string `json:"host-id"`
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[hostInput](), reflect.TypeFor[yesInput](), reflect.TypeFor[twiceInput]()} {
		if _, err := inputFields(typ); err == nil || !strings.Contains(err.Error(), "flag --") {
			t.Errorf("%s: error %v", typ, err)
		}
	}
}

// collect parses args into a command with sampleInput's flags and collects
// the input. secrets answers the no-echo prompts on a terminal.
func collect(t *testing.T, args []string, stdin string, onTerminal bool, secrets ...string) (string, string, error) {
	t.Helper()
	fields, err := inputFields(reflect.TypeFor[sampleInput]())
	if err != nil {
		t.Fatal(err)
	}
	cmd := &cobra.Command{Use: "x"}
	addInputFlags(cmd, fields)
	if err := cmd.ParseFlags(args); err != nil {
		t.Fatal(err)
	}
	var prompts bytes.Buffer
	term := &terminal{in: bufio.NewReader(strings.NewReader(stdin)), out: &prompts, isTerm: onTerminal,
		readSecret: func() (string, error) {
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		}}
	raw, err := collectInput(cmd, fields, term)
	return string(raw), prompts.String(), err
}

func TestCollectInput(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(file, []byte(`{"limits":{"max":5},"port":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	got, prompts, err := collect(t, []string{"--name", "web", "--port", "443", "--tags", "a", "--tags", "b",
		"--ports", "80,8080", "--mode", "allow", "--weight", "0.5", "--input", file}, "k-123\npass phrase\n", false)
	want := `{"apiKey":"k-123","force":false,"limits":{"max":5},"mode":"allow","name":"web","passphrase":"pass phrase",` +
		`"port":443,"ports":[80,8080],"tags":["a","b"],"weight":0.5}`
	if err != nil || got != want || prompts != "" {
		t.Errorf("flags and file: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}

	got, prompts, err = collect(t, []string{"--name", "web", "--port", "1", "--force", "--input", "-"},
		`{"limits":{"max":1},"apiKey":"from-file","passphrase":"pp"}`, false)
	want = `{"apiKey":"from-file","force":true,"limits":{"max":1},"name":"web","passphrase":"pp","port":1}`
	if err != nil || got != want || prompts != "" {
		t.Errorf("input from stdin: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}

	got, prompts, err = collect(t, []string{"--name", "web", "--port", "1", "--input", file}, "", true, "tty-key", "tty-pass")
	want = `{"apiKey":"tty-key","force":false,"limits":{"max":5},"name":"web","passphrase":"tty-pass","port":1}`
	if err != nil || got != want || prompts != "apiKey: \npassphrase: \n" {
		t.Errorf("terminal: %s, %v, prompts %q\nwant %s", got, err, prompts, want)
	}
}

func TestCollectInputUsageErrors(t *testing.T) {
	file := filepath.Join(t.TempDir(), "in.json")
	if err := os.WriteFile(file, []byte(`{"limits":{"max":5}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name  string
		args  []string
		stdin string
		want  string
	}{
		{"missing flag", []string{"--port", "1", "--input", file}, "k\np\n", "--name"},
		{"missing nested field", []string{"--name", "a", "--port", "1"}, "k\np\n", `"limits"`},
		{"missing secret", []string{"--name", "a", "--port", "1", "--input", file}, "k\n", "passphrase"},
		{"bad JSON", []string{"--name", "a", "--port", "1", "--input", "-"}, `{"limits":`, "--input"},
		{"not an object", []string{"--name", "a", "--port", "1", "--input", "-"}, `[1]`, "--input"},
		{"no file", []string{"--name", "a", "--port", "1", "--input", filepath.Join(t.TempDir(), "none")}, "", "--input"},
	}
	for _, c := range cases {
		_, _, err := collect(t, c.args, c.stdin, false)
		if ExitCode(ranError(err)) != ExitUsage || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: error %v, want a usage error naming %s", c.name, err, c.want)
		}
	}
}

func TestKebab(t *testing.T) {
	for in, want := range map[string]string{"name": "name", "hostId": "host-id", "expiresInDays": "expires-in-days",
		"apiKey": "api-key", "ipv6Addr": "ipv6-addr", "snake_case": "snake-case", "URL": "url"} {
		if got := kebab(in); got != want {
			t.Errorf("kebab(%q) = %q, want %q", in, got, want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestInputFields|TestInputFieldsRefuseReservedFlags|TestCollectInput|TestCollectInputUsageErrors|TestKebab)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/input_test.go:34:17: undefined: inputFields`, then `undefined: addInputFlags`, `undefined: terminal`, `undefined: collectInput`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/input.go`:

```go
package cli

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"strings"
	"unicode"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// reservedFlags are the flags the CLI defines itself; no input field may
// take one (generation fails instead).
var reservedFlags = []string{"url", "socket", "dev", "help", "json", "host", "input", "yes", "confirmation", "confirm"}

// terminal reads the operator's answers: from a terminal with prompts, or
// line by line from standard input otherwise.
type terminal struct {
	in         *bufio.Reader
	out        io.Writer // where prompts go
	isTerm     bool
	readSecret func() (string, error) // one line without echo; used only on a terminal
}

// line prompts on a terminal and reads one line without its line ending.
// It returns io.EOF when the input ended before any character.
func (t *terminal) line(prompt string) (string, error) {
	if t.isTerm {
		_, _ = io.WriteString(t.out, prompt)
	}
	s, err := t.in.ReadString('\n')
	if err != nil && (!errors.Is(err, io.EOF) || s == "") {
		return "", err
	}
	return strings.TrimRight(s, "\r\n"), nil
}

// secret reads one line: without echo after a prompt on a terminal, else
// as line does without a prompt.
func (t *terminal) secret(prompt string) (string, error) {
	if !t.isTerm {
		return t.line("")
	}
	_, _ = io.WriteString(t.out, prompt)
	s, err := t.readSecret()
	_, _ = io.WriteString(t.out, "\n")
	return s, err
}

// inputField is one property of an action's input schema.
type inputField struct {
	Name        string // the JSON name
	Flag        string // the kebab-case flag name
	Description string
	Type        string // string, integer, number, boolean or array; "" for a nested value
	Items       string // the scalar item type of an array
	Enum        []string
	Required    bool
	Secret      bool
}

// propSchema is the part of a JSON Schema the CLI reads.
type propSchema struct {
	Ref         string          `json:"$ref"`
	Type        json.RawMessage `json:"type"`
	Items       *propSchema     `json:"items"`
	Enum        []any           `json:"enum"`
	Description string          `json:"description"`
	Secret      bool            `json:"x-secret"`
	Required    []string        `json:"required"`
	Properties  json.RawMessage `json:"properties"`
}

var scalarTypes = []string{"string", "integer", "number", "boolean"}

// inputFields reads the properties of t's input schema (schema.Input) in
// schema order. Scalars and arrays of scalars get a flag; anything else is
// nested and comes from --input; secret fields get no flag and must be
// strings.
func inputFields(t reflect.Type) ([]inputField, error) {
	raw, err := schema.Input(t)
	if err != nil {
		return nil, err
	}
	var doc struct {
		propSchema
		Defs map[string]*propSchema `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("input schema of %s: %w", t, err)
	}
	resolve := func(p *propSchema) *propSchema {
		if name, ok := strings.CutPrefix(p.Ref, "#/$defs/"); ok && doc.Defs[name] != nil {
			return doc.Defs[name]
		}
		return p
	}
	root := resolve(&doc.propSchema)
	props, err := members(root.Properties)
	if err != nil {
		return nil, fmt.Errorf("input schema of %s: %w", t, err)
	}
	var fields []inputField
	flags := map[string]string{}
	for _, m := range props {
		var p propSchema
		if err := json.Unmarshal(m.Value, &p); err != nil {
			return nil, fmt.Errorf("input schema of %s, property %s: %w", t, m.Key, err)
		}
		f := inputField{Name: m.Key, Flag: kebab(m.Key), Description: p.Description, Secret: p.Secret,
			Required: slices.Contains(root.Required, m.Key)}
		rp := resolve(&p)
		var typ string
		_ = json.Unmarshal(rp.Type, &typ)
		switch {
		case slices.Contains(scalarTypes, typ):
			f.Type = typ
		case typ == "array" && rp.Items != nil:
			var item string
			_ = json.Unmarshal(resolve(rp.Items).Type, &item)
			if slices.Contains(scalarTypes, item) {
				f.Type, f.Items = typ, item
			}
		}
		for _, e := range rp.Enum {
			if s, ok := e.(string); ok {
				f.Enum = append(f.Enum, s)
			}
		}
		switch {
		case f.Secret && f.Type != "string":
			return nil, fmt.Errorf("input of %s: secret field %s must be a string", t, f.Name)
		case f.Secret || f.Type == "":
		case slices.Contains(reservedFlags, f.Flag):
			return nil, fmt.Errorf("input of %s: field %s would take the CLI's own flag --%s", t, f.Name, f.Flag)
		case flags[f.Flag] != "":
			return nil, fmt.Errorf("input of %s: fields %s and %s both map to flag --%s", t, flags[f.Flag], f.Name, f.Flag)
		default:
			flags[f.Flag] = f.Name
		}
		fields = append(fields, f)
	}
	return fields, nil
}

// kebab turns a JSON name such as expiresInDays into expires-in-days.
func kebab(name string) string {
	var b strings.Builder
	runes := []rune(name)
	for i, r := range runes {
		switch {
		case r == '_':
			b.WriteRune('-')
		case unicode.IsUpper(r):
			if i > 0 && (unicode.IsLower(runes[i-1]) || unicode.IsDigit(runes[i-1])) {
				b.WriteRune('-')
			}
			b.WriteRune(unicode.ToLower(r))
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// addInputFlags defines a flag for every scalar field and, when the input
// has any field, --input.
func addInputFlags(cmd *cobra.Command, fields []inputField) {
	fs := cmd.Flags()
	for _, f := range fields {
		if f.Secret || f.Type == "" {
			continue
		}
		usage := f.Description
		if len(f.Enum) > 0 {
			usage += " One of: " + strings.Join(f.Enum, ", ") + "."
		}
		if f.Required && f.Type != "boolean" {
			usage += " (required)"
		}
		usage = strings.TrimSpace(usage)
		switch f.Type + "/" + f.Items {
		case "string/":
			fs.String(f.Flag, "", usage)
		case "integer/":
			fs.Int64(f.Flag, 0, usage)
		case "number/":
			fs.Float64(f.Flag, 0, usage)
		case "boolean/":
			fs.Bool(f.Flag, false, usage)
		case "array/string":
			fs.StringArray(f.Flag, nil, usage+" (repeatable)")
		case "array/integer":
			fs.Int64Slice(f.Flag, nil, usage+" (comma-separated or repeated)")
		case "array/number":
			fs.Float64Slice(f.Flag, nil, usage+" (comma-separated or repeated)")
		case "array/boolean":
			fs.BoolSlice(f.Flag, nil, usage+" (comma-separated or repeated)")
		}
	}
	if len(fields) > 0 {
		fs.String("input", "", "read the input as a JSON object from this file, or from standard input with -; flags override its fields")
	}
}

// flagValue is the value of f's flag.
func flagValue(cmd *cobra.Command, f inputField) (any, error) {
	fs := cmd.Flags()
	switch f.Type + "/" + f.Items {
	case "string/":
		return fs.GetString(f.Flag)
	case "integer/":
		return fs.GetInt64(f.Flag)
	case "number/":
		return fs.GetFloat64(f.Flag)
	case "boolean/":
		return fs.GetBool(f.Flag)
	case "array/string":
		return fs.GetStringArray(f.Flag)
	case "array/integer":
		return fs.GetInt64Slice(f.Flag)
	case "array/number":
		return fs.GetFloat64Slice(f.Flag)
	case "array/boolean":
		return fs.GetBoolSlice(f.Flag)
	}
	return nil, fmt.Errorf("field %s has no flag", f.Name)
}

// collectInput builds the input object: the --input document, overridden
// by the flags that were set, then every secret still missing, read in
// schema order from the terminal or standard input. A required boolean that
// is missing is false; any other missing required field is a usage error.
func collectInput(cmd *cobra.Command, fields []inputField, term *terminal) (json.RawMessage, error) {
	obj := map[string]json.RawMessage{}
	if fl := cmd.Flags().Lookup("input"); fl != nil && fl.Value.String() != "" {
		var err error
		if obj, err = readInputDoc(fl.Value.String(), term); err != nil {
			return nil, err
		}
	}
	for _, f := range fields {
		if f.Secret || f.Type == "" || !cmd.Flags().Changed(f.Flag) {
			continue
		}
		v, err := flagValue(cmd, f)
		if err != nil {
			return nil, err
		}
		if obj[f.Name], err = json.Marshal(v); err != nil {
			return nil, err
		}
	}
	for _, f := range fields {
		if _, ok := obj[f.Name]; ok || !f.Required || f.Secret {
			continue
		}
		switch f.Type {
		case "boolean":
			obj[f.Name] = json.RawMessage("false")
		case "":
			return nil, usagef("required input field %q missing; give it in --input", f.Name)
		default:
			return nil, usagef("required flag --%s not set", f.Flag)
		}
	}
	for _, f := range fields {
		if _, ok := obj[f.Name]; ok || !f.Secret {
			continue
		}
		s, err := term.secret(f.Name + ": ")
		if errors.Is(err, io.EOF) {
			return nil, usagef("no value for %s: run on a terminal, or give one line per secret on standard input", f.Name)
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", f.Name, err)
		}
		if obj[f.Name], err = json.Marshal(s); err != nil {
			return nil, err
		}
	}
	return json.Marshal(obj)
}

// readInputDoc reads --input: a JSON object from path, or from standard
// input for "-".
func readInputDoc(path string, term *terminal) (map[string]json.RawMessage, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(term.in)
	} else {
		data, err = os.ReadFile(path) //nolint:gosec // G304: the operator names the file to read
	}
	if err != nil {
		return nil, usagef("--input %s: %v", path, err)
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(data, &obj); err != nil || obj == nil {
		return nil, usagef("--input %s: want a JSON object", path)
	}
	return obj, nil
}

// member is one member of a JSON object, in document order.
type member struct {
	Key   string
	Value json.RawMessage
}

// members returns the members of the JSON object raw in document order; an
// empty raw has none.
func members(raw json.RawMessage) ([]member, error) {
	if len(bytes.TrimSpace(raw)) == 0 {
		return nil, nil
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return nil, errors.New("not a JSON object")
	}
	var out []member
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, _ := tok.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, err
		}
		out = append(out, member{Key: key, Value: v})
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestInputFields|TestInputFieldsRefuseReservedFlags|TestCollectInput|TestCollectInputUsageErrors|TestKebab)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/input.go internal/cli/input_test.go
git commit -m "feat(cli): map action inputs to flags and prompts"
```

### Task 18: Output rendering

**Files:**
- Create: `internal/cli/output.go`
- Test: `internal/cli/output_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (`sanitize`), Contracts › Task 17 (`members`, `propSchema`); `schema.Output` (merged).
- Produces: Contracts › Task 18 (`render`, `writeLines`, `isList`, `objectRows`, `renderList`, `writeTable`, `flatten`, `cellText`, `scalarText`, `compact`, `secretOutputs`, `noteSecrets`).

**Depends on:** Task 1, Task 17

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Spec section 15 output: tables for lists, `key: value` lines otherwise. An object that holds only a list (`{hosts: [...]}`) is that list's table; nested objects flatten to dotted keys; lists of scalars are comma-separated; JSON key order is kept. Secret outputs (`totpSecret`, `token`) are printed, because the API returns them only once (spec section 5), followed by a line saying they will not be shown again; `secretOutputs` finds them from `schema.Output`'s `x-secret` marks. Every rendered string goes through `sanitize` (controller ruling: hostnames, OS names and audit inputs come from the server). The tests cover each rendering path, a hostile ESC sequence and bidi override in every one, and the secret note.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/output_test.go`:

```go
package cli

import (
	"bytes"
	"reflect"
	"slices"
	"testing"
)

func TestRender(t *testing.T) {
	cases := []struct {
		name, raw, want string
	}{
		{"a single list is a table",
			`{"hosts":[{"id":"h1","name":"web","isLocal":true,"modules":[{"id":"hostinfo"}]},` +
				`{"id":"h2","name":"db-primary","isLocal":false,"modules":[]}]}`,
			"id  name        isLocal  modules\n" +
				"h1  web         true     [{\"id\":\"hostinfo\"}]\n" +
				"h2  db-primary  false    []\n"},
		{"an empty list", `{"tokens":[]}`, "(none)\n"},
		{"rows with different keys", `[{"a":1},{"b":"x\ty","a":null}]`, "a  b\n1  \n-  x\\x09y\n"},
		{"nested objects flatten",
			`{"user":{"id":"u1","username":"ann","createdAt":"2026-10-18T00:00:00Z"},"totpSecret":"ABC","n":null}`,
			"user.id: u1\nuser.username: ann\nuser.createdAt: 2026-10-18T00:00:00Z\ntotpSecret: ABC\nn: -\n"},
		{"a list among other fields",
			`{"entries":[{"id":"a1","actor":"user:u1"}],"nextBefore":"a1","tags":["x","y"],"empty":{}}`,
			"entries:\nid  actor\na1  user:u1\nnextBefore: a1\ntags: x, y\nempty: {}\n"},
		{"scalars in a list", `["a",2,true]`, "a\n2\ntrue\n"},
		{"a scalar", `"done"`, "done\n"},
		{"null", `null`, ""},
		{"an empty object", `{}`, ""},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := render(&b, []byte(c.raw)); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b.String() != c.want {
			t.Errorf("%s: rendered\n%s\nwant\n%s", c.name, b.String(), c.want)
		}
	}
	if err := render(new(bytes.Buffer), []byte(`{"a":`)); err == nil {
		t.Error("malformed JSON rendered without an error")
	}
}

func TestRenderSanitizes(t *testing.T) {
	// esc is an ESC sequence and a bidi override in JSON, as a hostile
	// server would send them; shown is how they must reach the terminal.
	const esc, shown = `\u001b[2J\u202e`, `\x1b[2J\u202e`
	cases := []struct {
		name, raw, want string
	}{
		{"table cell", `[{"name":"a` + esc + `"}]`, "name\na" + shown + "\n"},
		{"table header", `[{"n` + esc + `":1}]`, "n" + shown + "\n1\n"},
		{"single-list table", `{"hosts":[{"id":"h` + esc + `"}]}`, "id\nh" + shown + "\n"},
		{"key: value value", `{"prettyName":"Ubuntu` + esc + `"}`, "prettyName: Ubuntu" + shown + "\n"},
		{"key: value key", `{"k` + esc + `":1}`, "k" + shown + ": 1\n"},
		{"nested value", `{"os":{"name":"x` + esc + `"}}`, "os.name: x" + shown + "\n"},
		{"list of scalars in a value", `{"tags":["t` + esc + `"],"n":1}`, "tags: t" + shown + "\nn: 1\n"},
		{"section key", `{"e` + esc + `":[{"a":1}],"b":2}`, "e" + shown + ":\na\n1\nb: 2\n"},
		{"list scalar", `["s` + esc + `"]`, "s" + shown + "\n"},
		{"top-level scalar", `"v` + esc + `"`, "v" + shown + "\n"},
		{"newline in a value", `{"m":"one\ntwo"}`, `m: one\x0atwo` + "\n"},
	}
	for _, c := range cases {
		var b bytes.Buffer
		if err := render(&b, []byte(c.raw)); err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if b.String() != c.want {
			t.Errorf("%s: rendered %q, want %q", c.name, b.String(), c.want)
		}
	}
}

type tokenInfoOut struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type newTokenOut struct {
	tokenInfoOut
	Token string `json:"token" secret:"true"`
}

type enrollmentOut struct {
	User       tokenInfoOut `json:"user"`
	TOTPSecret string       `json:"totpSecret" secret:"true"`
	Recovery   string       `json:"recovery" secret:"true"`
}

func TestSecretOutputs(t *testing.T) {
	cases := map[reflect.Type][]string{
		reflect.TypeFor[newTokenOut]():   {"token"},
		reflect.TypeFor[enrollmentOut](): {"totpSecret", "recovery"},
		reflect.TypeFor[tokenInfoOut]():  nil,
	}
	for typ, want := range cases {
		got, err := secretOutputs(typ)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %q, %v; want %q", typ, got, err, want)
		}
	}
}

func TestNoteSecrets(t *testing.T) {
	cases := []struct {
		names     []string
		raw, want string
	}{
		{[]string{"token"}, `{"id":"t1","token":"dpt_x"}`, "\nThe token above will not be shown again; store it now.\n"},
		{[]string{"totpSecret", "recovery"}, `{"totpSecret":"A","recovery":"B"}`,
			"\nThe totpSecret and recovery above will not be shown again; store them now.\n"},
		{[]string{"token"}, `{"id":"t1","token":null}`, ""},
		{nil, `{"id":"t1"}`, ""},
	}
	for _, c := range cases {
		var b bytes.Buffer
		noteSecrets(&b, c.names, []byte(c.raw))
		if b.String() != c.want {
			t.Errorf("%q in %s: %q, want %q", c.names, c.raw, b.String(), c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestRender|TestRenderSanitizes|TestSecretOutputs|TestNoteSecrets)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/output_test.go:35:13: undefined: render`, then `undefined: secretOutputs`, `undefined: noteSecrets`, and `FAIL github.com/darkraise/darkpanel/internal/cli [build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/output.go`:

```go
package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// render writes an output for people (spec section 15): a list of objects
// is a table, any other value is "key: value" lines with nested keys joined
// by dots. An object holding only a list is the list's table. Null prints
// nothing. Every string it writes is sanitized.
func render(w io.Writer, raw json.RawMessage) error {
	if !json.Valid(raw) {
		return fmt.Errorf("render: the output is not valid JSON")
	}
	raw = bytes.TrimSpace(raw)
	switch raw[0] {
	case '[':
		return renderList(w, raw)
	case '{':
		ms, err := members(raw)
		if err != nil {
			return err
		}
		if len(ms) == 1 && isList(ms[0].Value) {
			return renderList(w, ms[0].Value)
		}
		var lines []string
		for _, m := range ms {
			if rows := objectRows(m.Value); len(rows) > 0 {
				if err := writeLines(w, lines); err != nil {
					return err
				}
				lines = nil
				if _, err := fmt.Fprintf(w, "%s:\n", sanitize(m.Key)); err != nil {
					return err
				}
				if err := renderList(w, m.Value); err != nil {
					return err
				}
				continue
			}
			lines = flatten(lines, m.Key, m.Value)
		}
		return writeLines(w, lines)
	case 'n':
		return nil
	}
	_, err := fmt.Fprintln(w, sanitize(scalarText(raw)))
	return err
}

func writeLines(w io.Writer, lines []string) error {
	for _, l := range lines {
		if _, err := fmt.Fprintln(w, sanitize(l)); err != nil {
			return err
		}
	}
	return nil
}

func isList(raw json.RawMessage) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '['
}

// objectRows returns the elements of raw when it is a non-empty list of
// objects only.
func objectRows(raw json.RawMessage) []json.RawMessage {
	var items []json.RawMessage
	if !isList(raw) || json.Unmarshal(raw, &items) != nil || len(items) == 0 {
		return nil
	}
	for _, it := range items {
		if t := bytes.TrimSpace(it); len(t) == 0 || t[0] != '{' {
			return nil
		}
	}
	return items
}

// renderList writes a list of objects as a table, an empty list as
// "(none)", and anything else one element per line.
func renderList(w io.Writer, raw json.RawMessage) error {
	var items []json.RawMessage
	if err := json.Unmarshal(raw, &items); err != nil {
		return err
	}
	if len(items) == 0 {
		_, err := fmt.Fprintln(w, "(none)")
		return err
	}
	rows := objectRows(raw)
	if rows == nil {
		for _, it := range items {
			if _, err := fmt.Fprintln(w, sanitize(cellText(it))); err != nil {
				return err
			}
		}
		return nil
	}
	var header []string
	cells := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		ms, err := members(r)
		if err != nil {
			return err
		}
		row := map[string]string{}
		for _, m := range ms {
			if !slices.Contains(header, m.Key) {
				header = append(header, m.Key)
			}
			row[m.Key] = cellText(m.Value)
		}
		cells = append(cells, row)
	}
	table := make([][]string, 0, len(cells))
	for _, row := range cells {
		line := make([]string, len(header))
		for i, h := range header {
			line[i] = row[h]
		}
		table = append(table, line)
	}
	return writeTable(w, header, table)
}

// writeTable writes header and rows, sanitized, as columns separated by
// two spaces. Sanitizing also escapes tabs and newlines, which would break
// the columns.
func writeTable(w io.Writer, header []string, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 8, 2, ' ', 0)
	for _, r := range append([][]string{header}, rows...) {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = sanitize(c)
		}
		if _, err := fmt.Fprintln(tw, strings.Join(cells, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

// flatten appends "key: value" lines for raw under key: objects recurse
// with dotted keys, lists of scalars are comma-separated, and anything
// else nested is compact JSON.
func flatten(lines []string, key string, raw json.RawMessage) []string {
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '{' {
		ms, err := members(raw)
		if err == nil && len(ms) > 0 {
			for _, m := range ms {
				lines = flatten(lines, key+"."+m.Key, m.Value)
			}
			return lines
		}
	}
	return append(lines, key+": "+cellText(raw))
}

// cellText is a value as one line: a scalar's text, "-" for null, a list
// of scalars comma-separated, anything else compact JSON.
func cellText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var items []json.RawMessage
	if isList(raw) && json.Unmarshal(raw, &items) == nil {
		texts := make([]string, 0, len(items))
		for _, it := range items {
			if t := bytes.TrimSpace(it); len(t) > 0 && (t[0] == '{' || t[0] == '[') {
				return compact(raw)
			}
			texts = append(texts, scalarText(it))
		}
		if len(items) > 0 {
			return strings.Join(texts, ", ")
		}
	}
	if len(raw) > 0 && (raw[0] == '{' || raw[0] == '[') {
		return compact(raw)
	}
	return scalarText(raw)
}

func scalarText(raw json.RawMessage) string {
	raw = bytes.TrimSpace(raw)
	var s string
	switch {
	case string(raw) == "null": // checked first: null unmarshals into a string as ""
		return "-"
	case json.Unmarshal(raw, &s) == nil:
		return s
	}
	return string(raw)
}

func compact(raw json.RawMessage) string {
	var b bytes.Buffer
	if json.Compact(&b, raw) != nil {
		return string(raw)
	}
	return b.String()
}

// secretOutputs are the top-level properties of t's output schema
// (schema.Output) marked secret, in schema order.
func secretOutputs(t reflect.Type) ([]string, error) {
	raw, err := schema.Output(t)
	if err != nil {
		return nil, err
	}
	var doc struct {
		propSchema
		Defs map[string]*propSchema `json:"$defs"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("output schema of %s: %w", t, err)
	}
	root := &doc.propSchema
	if name, ok := strings.CutPrefix(root.Ref, "#/$defs/"); ok && doc.Defs[name] != nil {
		root = doc.Defs[name]
	}
	props, err := members(root.Properties)
	if err != nil {
		return nil, fmt.Errorf("output schema of %s: %w", t, err)
	}
	var names []string
	for _, m := range props {
		var p propSchema
		if json.Unmarshal(m.Value, &p) == nil && p.Secret {
			names = append(names, m.Key)
		}
	}
	return names, nil
}

// noteSecrets tells the operator that the secret outputs shown are shown
// only once (spec section 5): the API never returns them again.
func noteSecrets(w io.Writer, names []string, raw json.RawMessage) {
	ms, _ := members(raw)
	var shown []string
	for _, m := range ms {
		if slices.Contains(names, m.Key) && scalarText(m.Value) != "-" && scalarText(m.Value) != "" {
			shown = append(shown, m.Key)
		}
	}
	switch len(shown) {
	case 0:
	case 1:
		_, _ = fmt.Fprintf(w, "\nThe %s above will not be shown again; store it now.\n", shown[0])
	default:
		_, _ = fmt.Fprintf(w, "\nThe %s above will not be shown again; store them now.\n", strings.Join(shown, " and "))
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestRender|TestRenderSanitizes|TestSecretOutputs|TestNoteSecrets)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/output.go internal/cli/output_test.go
git commit -m "feat(cli): render outputs as tables and key lines"
```

### Task 19: Agent runtime client methods

**Files:**
- Create: `internal/agent/runtime/client.go`
- Test: `internal/agent/runtime/client_test.go`

**Interfaces:**
- Consumes: Contracts › Task 14 (`Runtime` internals and the test fixture); `core.AgentClient`, `lifecycle.Protect`, `schema.Redact`, `lifecycle.AgentJournal.Entries` and `Ack`
- Produces: Contracts › `internal/agent/runtime` Task 19 (`*Runtime` implements `core.AgentClient`)

**Items:** 5

**Depends on:** Task 14

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The in-process `core.AgentClient` (spec section 3, Dispatch): Hello with each module's availability, Preflight that re-checks a module and keeps the result, reads and resources gated only on a failed migration, the engine's change methods, and the journal for ingestion. The tests cover Hello, a preflight that starts passing, a broken module's `module.unavailable` detail, a redacted resource and a panicking one, and a confirm and a revert whose journal entries carry the callers' sources.

- [ ] **Step 1: Write the failing test**

Create `internal/agent/runtime/client_test.go`:

```go
package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func wantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}

func TestHelloReportsTheHostAndItsModules(t *testing.T) {
	f := newFixture(t)
	h, err := f.rt.Hello(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: "test", OS: goruntime.GOOS, HostID: testHost,
		Modules: f.rt.statuses(),
	}
	if !reflect.DeepEqual(h, want) || len(h.Modules) != 4 {
		t.Errorf("Hello = %+v, want %+v", h, want)
	}
}

func TestPreflightChecksAgain(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.mu.Lock()
	f.host.ready = true
	f.host.mu.Unlock()
	st, err := f.rt.Preflight(ctx, "flaky")
	if err != nil || st != (core.ModuleStatus{ID: "flaky", Version: 2, Available: true}) {
		t.Fatalf("Preflight(flaky) = %+v, %v; want available", st, err)
	}
	h, err := f.rt.Hello(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !h.Modules[2].Available {
		t.Errorf("Hello after the preflight = %+v, want flaky available", h.Modules[2])
	}
	st, err = f.rt.Preflight(ctx, "bad")
	if err != nil || st.Available || st.Reason != ReasonMigrationFailed || f.host.badChecks != 0 {
		t.Errorf("Preflight(bad) = %+v, %v (checks %d); want the migration failure without a check", st, err, f.host.badChecks)
	}
	for _, id := range []string{"core", "nope"} {
		_, err := f.rt.Preflight(ctx, id)
		_ = wantCode(t, err, errcode.NotFound)
	}
}

func TestReadsRefuseABrokenModule(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "bad.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`))
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module bad is unavailable: "+ReasonMigrationFailed {
		t.Errorf("detail = %q", e.Detail)
	}
	// A failed preflight is the dispatcher's to refuse; the agent serves it.
	if _, err := f.rt.Read(ctx, alice, core.ActionRef{Name: "flaky.item.get", Version: 1}, json.RawMessage(`{"name":"a"}`)); err != nil {
		t.Errorf("Read of flaky = %v, want served", err)
	}
	_, err = f.rt.Read(ctx, alice, core.ActionRef{Name: "kv.item.nope", Version: 1}, json.RawMessage(`{}`))
	_ = wantCode(t, err, errcode.ActionUnknown)
}

func TestResourceReadsWithTheAgentEnv(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	out, err := f.rt.Resource(ctx, alice, "kv.status")
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != `{"count":0,"token":"[redacted]"}` {
		t.Errorf("kv.status = %s, want the token redacted", out)
	}
	_, err = f.rt.Resource(ctx, alice, "kv.nope")
	_ = wantCode(t, err, errcode.NotFound)
	_, err = f.rt.Resource(ctx, alice, "boom.status")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "panicked") {
		t.Errorf("boom.status = %v, want a plain panic error", err)
	}
}

func TestChangesThroughTheClient(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	bob := core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.8"}
	plan := func(input string) core.PlanView {
		t.Helper()
		v, err := f.rt.Plan(ctx, alice, refSet, json.RawMessage(input))
		if err != nil {
			t.Fatal(err)
		}
		if v.HostID != testHost || v.Action != refSet.Name {
			t.Fatalf("plan = %+v", v)
		}
		return v
	}
	first, err := f.rt.Apply(ctx, alice, plan(`{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil || first.State != core.StatePending {
		t.Fatalf("Apply = %+v, %v", first, err)
	}
	if v, err := f.rt.Confirm(ctx, bob, first.ID); err != nil || v.State != core.StateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	second, err := f.rt.Apply(ctx, alice, plan(`{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if v, err := f.rt.Revert(ctx, bob, second.ID); err != nil || v.State != core.StateReverted {
		t.Fatalf("Revert = %+v, %v", v, err)
	}
	if v, err := f.rt.Change(ctx, first.ID); err != nil || v.State != core.StateConfirmed || v.HostID != testHost {
		t.Errorf("Change = %+v, %v", v, err)
	}
	list, err := f.rt.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(list) != 2 || list[0].ID != second.ID {
		t.Errorf("Changes = %+v, %v; want both, newest first", list, err)
	}

	entries, err := f.rt.Journal(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.Source)
	}
	if want := []string{"apply/192.0.2.7", "confirm/192.0.2.8", "apply/192.0.2.7", "revert/192.0.2.8"}; !reflect.DeepEqual(got, want) {
		t.Errorf("journal = %v, want %v", got, want)
	}
	if err := f.rt.Ack(ctx, entries[1].Seq); err != nil {
		t.Fatal(err)
	}
	left, err := f.rt.Journal(ctx, 0, 10)
	if err != nil || len(left) != 2 || left[0].Seq != entries[2].Seq {
		t.Errorf("journal after Ack = %+v, %v; want the last two entries", left, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestHello|TestPreflight|TestReads|TestResource|TestChangesThroughTheClient' ./internal/agent/runtime/`

Expected: FAIL — the build reports `f.rt.Hello undefined (type *Runtime has no field or method Hello)`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/agent/runtime [github.com/darkraise/darkpanel/internal/agent/runtime.test]
internal/agent/runtime/client_test.go:27:17: f.rt.Hello undefined (type *Runtime has no field or method Hello)
internal/agent/runtime/client_test.go:46:18: f.rt.Preflight undefined (type *Runtime has no field or method Preflight)
internal/agent/runtime/client_test.go:50:17: f.rt.Hello undefined (type *Runtime has no field or method Hello)
internal/agent/runtime/client_test.go:57:17: f.rt.Preflight undefined (type *Runtime has no field or method Preflight)
internal/agent/runtime/client_test.go:62:18: f.rt.Preflight undefined (type *Runtime has no field or method Preflight)
internal/agent/runtime/client_test.go:70:17: f.rt.Read undefined (type *Runtime has no field or method Read)
internal/agent/runtime/client_test.go:76:20: f.rt.Read undefined (type *Runtime has no field or method Read)
```

- [ ] **Step 3: Write the implementation**

Create `internal/agent/runtime/client.go`:

```go
package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var _ core.AgentClient = (*Runtime)(nil)

// Hello reports the agent and every module's availability as of its last
// check (migrations and preflight at Open, or a later Preflight).
func (r *Runtime) Hello(context.Context) (core.AgentHello, error) {
	return core.AgentHello{
		ProtocolVersion: ProtocolVersion, BinaryVersion: r.cfg.BinaryVersion, OS: goruntime.GOOS,
		HostID: r.cfg.HostID, Modules: r.statuses(),
	}, nil
}

// Preflight runs moduleID's preflight checks again and keeps the result for
// Hello. A module whose migrations failed is not checked and stays
// unavailable. A module the registry lacks, and the core, are not_found.
func (r *Runtime) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	m, ok := r.cfg.Registry.Module(moduleID)
	if !ok || moduleID == core.ModuleIDCore {
		return core.ModuleStatus{}, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s", moduleID))
	}
	st := r.check(ctx, m)
	r.setStatus(st)
	return st, nil
}

// usable refuses, with module.unavailable, an action or resource name of a
// module whose migrations failed. Enablement and preflight results are the
// dispatcher's to enforce.
func (r *Runtime) usable(name string) error {
	moduleID, _, _ := strings.Cut(name, ".")
	if r.isBroken(moduleID) {
		return errcode.New(errcode.ModuleUnavailable,
			fmt.Sprintf("module %s is unavailable: %s", moduleID, ReasonMigrationFailed))
	}
	return nil
}

// Read runs an agent read action.
func (r *Runtime) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	if err := r.usable(a.Name); err != nil {
		return nil, err
	}
	return r.engine.Read(ctx, p, a, input)
}

// Resource reads a resource with the agent's Env and p, redacting secret
// fields. A panic in the resource is a plain error.
func (r *Runtime) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	def, ok := r.cfg.Registry.Resource(name)
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no resource %s", name))
	}
	if err := r.usable(name); err != nil {
		return nil, err
	}
	env := r.base
	env.Principal = p
	var out json.RawMessage
	err := lifecycle.Protect(r.cfg.Log, name, "Resource", func() error {
		var err error
		out, err = def.Read(ctx, env)
		return err
	})
	if err != nil {
		return nil, err
	}
	return schema.Redact(def.OutputType, out), nil
}

// Plan plans an agent action.
func (r *Runtime) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	if err := r.usable(a.Name); err != nil {
		return core.PlanView{}, err
	}
	return r.engine.Plan(ctx, p, a, input)
}

// Apply applies a plan made by Plan.
func (r *Runtime) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return r.engine.Apply(ctx, p, planID, opts)
}

// Confirm confirms a pending change.
func (r *Runtime) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return r.engine.Confirm(ctx, p, changeID)
}

// Revert reverts a pending change.
func (r *Runtime) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return r.engine.Revert(ctx, p, changeID)
}

// Change returns one change of this host.
func (r *Runtime) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	return r.engine.Change(ctx, changeID)
}

// Changes lists this host's changes as lifecycle.Engine.Changes does.
func (r *Runtime) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	return r.engine.Changes(ctx, f)
}

// Journal returns up to limit journal entries after afterSeq.
func (r *Runtime) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	return r.journal.Entries(ctx, afterSeq, limit)
}

// Ack deletes the journal entries up to seq, which the controller stored.
func (r *Runtime) Ack(ctx context.Context, seq int64) error {
	return r.journal.Ack(ctx, seq)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestHello|TestPreflight|TestReads|TestResource|TestChangesThroughTheClient' ./internal/agent/runtime/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/agent/runtime`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/agent/runtime/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/agent/runtime/client.go internal/agent/runtime/client_test.go
git commit -m "feat(agent): serve the agent client in process"
```


---

### Task 20: Command tree, read actions and resources

**Files:**
- Create: `internal/cli/root.go`
- Create: `internal/cli/read.go`
- Test: `internal/cli/root_test.go`

**Interfaces:**
- Consumes: Contracts › Task 1 (`markRuns`, `usagef`, `PrintError`, `ExitCode`), Task 16 (`connectNetwork`, `newSocketClient`, `client.do`, `EnvURL`, `EnvToken`, `DefaultURL`; test helpers), Task 8 (`entries`, `handWritten`, `CommandPath`; `testRegistry`, `fakeCore` is Task 8's test type), Task 17 (`terminal`, `inputFields`, `addInputFlags`, `collectInput`), Task 18 (`render`, `secretOutputs`, `noteSecrets`); `paths.Resolve` (merged `internal/paths`); `golang.org/x/term` (pinned).
- Produces: Contracts › Task 20 (`Options`, `NewRoot`, `FindCommand`, `AnnotationAction`, `AnnotationResource`, `app`, `withDefaults`, `group`, `actionCommand`, `addConnFlags`, `connect`, `actionPath`, `hostRef`, `resolveHost`, `hostFor`, `runRead`, `resourceCommand`, `show`; test helpers `cliEnv`, `hostList`, `testToken`, `tokenEnv`).

**Items:** 3, 13

**Depends on:** Task 8, Task 16, Task 17, Task 18

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`NewRoot` builds the tree from a `*core.Registry` passed in (production passes `registry.Build()`), so the package imports no server and opens no database. Read actions run `POST .../read` (agent-side under `/api/v1/hosts/{host}`); resources run `GET /api/v1/hosts/{host}/resources/{module}/{resource}`; `--json` prints the raw output. `--host` takes an id or a name; without it the only host is used, else the command fails listing the hosts (spec section 15). Break-glass commands get `--socket`/`--dev` and use the controller socket without a token; every other command gets `--url`. Mutating actions get no command yet: Task 22 adds the mutating flow. The tests drive the real cobra tree against the fake API and fake socket, including the bearer token from `DARKPANEL_TOKEN`, `auth.required` exiting 4, and usage errors that send no request.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/root_test.go`:

```go
package cli

import (
	"bytes"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// cliEnv runs the CLI in a test: stdin is the given text, the terminal
// check answers terminal, and secrets answer the no-echo prompts in order.
type cliEnv struct {
	reg      *core.Registry
	env      map[string]string
	stdin    string
	terminal bool
	secrets  []string
	commands []*cobra.Command
}

func (c cliEnv) run(t *testing.T, args ...string) (stdout, stderr string, code int) {
	t.Helper()
	reg := c.reg
	if reg == nil {
		reg = testRegistry(t)
	}
	secrets := slices.Clone(c.secrets)
	var out, errb bytes.Buffer
	root, err := NewRoot(Options{
		Registry: reg, Commands: c.commands,
		Stdin: strings.NewReader(c.stdin), Stdout: &out, Stderr: &errb,
		Getenv: envOf(c.env), UserHomeDir: homeAt(t.TempDir()),
		IsTerminal: func() bool { return c.terminal },
		ReadSecret: func() (string, error) {
			if len(secrets) == 0 {
				return "", io.EOF
			}
			s := secrets[0]
			secrets = secrets[1:]
			return s, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.Execute()
	PrintError(&errb, err)
	return out.String(), errb.String(), ExitCode(err)
}

// hostList is the core.host.list read the CLI makes to resolve --host;
// hosts are "<id>:<name>".
func hostList(hosts ...string) exchange {
	var items []string
	for i, h := range hosts {
		id, name, _ := strings.Cut(h, ":")
		items = append(items, fmt.Sprintf(`{"id":%q,"name":%q,"isLocal":%t,"online":true,"modules":[]}`, id, name, i == 0))
	}
	return exchange{method: "POST", path: "/api/v1/actions/core.host.list/v1/read", body: `{"input":{}}`,
		reply: `{"output":{"hosts":[` + strings.Join(items, ",") + `]}}`}
}

const testToken = "dpt_01JTEST_secret" //nolint:gosec // G101: a token the fake API expects, not a credential

var tokenEnv = map[string]string{EnvToken: testToken}

func TestNewRootTree(t *testing.T) {
	version := &cobra.Command{Use: "version", RunE: func(*cobra.Command, []string) error { return nil }}
	root, err := NewRoot(Options{Registry: testRegistry(t), Commands: []*cobra.Command{version}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"demo.item.get", "demo.item.list", "core.host.list", "core.audit.list", "core.user.list",
		"demo.rule", "demo.status"} {
		cmd, ok := FindCommand(root, name)
		if !ok || cmd.CommandPath() != "darkpanel "+strings.Join(CommandPath(name), " ") {
			t.Errorf("FindCommand(%q) = %v, %v", name, cmd, ok)
		}
	}
	if _, ok := FindCommand(root, "demo.nothing.here"); ok {
		t.Error("FindCommand found an unregistered action")
	}
	if c, _, err := root.Find([]string{"version"}); err != nil || c != version {
		t.Errorf("version command: %v, %v", c, err)
	}
	get, _ := FindCommand(root, "demo.item.get")
	for _, f := range []string{"id", "verbose", "host", "url", "json", "input"} {
		if get.Flags().Lookup(f) == nil {
			t.Errorf("demo item get has no --%s", f)
		}
	}
	users, _ := FindCommand(root, "core.user.list")
	if users.Flags().Lookup("socket") == nil || users.Flags().Lookup("dev") == nil || users.Flags().Lookup("url") != nil {
		t.Error("user list must take --socket and --dev, not --url")
	}
	hosts, _ := FindCommand(root, "core.host.list")
	if hosts.Flags().Lookup("host") != nil {
		t.Error("a controller-side action takes no --host")
	}
}

func TestNewRootRefusesClashes(t *testing.T) {
	reg, err := core.NewRegistry(fakeCore{"core.change.list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewRoot(Options{Registry: reg}); err == nil || !strings.Contains(err.Error(), `"change"`) {
		t.Errorf("core.change.list: error %v", err)
	}
	reg, err = core.NewRegistry(fakeCore{"core.agent.list"})
	if err != nil {
		t.Fatal(err)
	}
	agent := &cobra.Command{Use: "agent"}
	if _, err := NewRoot(Options{Registry: reg, Commands: []*cobra.Command{agent}}); err == nil ||
		!strings.Contains(err.Error(), `"agent"`) {
		t.Errorf("core.agent.list against a hand-written agent command: error %v", err)
	}
}

func TestReadAgentAction(t *testing.T) {
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.get/v2/read", body: `{"input":{"id":"i1"}}`,
			reply: `{"output":{"id":"i1","name":"first","tags":["a","b"]}}`},
		hostList("h1:web", "h2:db"),
		exchange{method: "POST", path: "/api/v1/hosts/h2/actions/demo.item.list/v1/read", body: `{"input":{}}`,
			reply: `{"output":{"items":[{"id":"i1","name":"first","tags":[]}]}}`},
		hostList("h1:web", "h2:db"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.get/v2/read", body: `{"input":{"id":"i2","verbose":true}}`,
			reply: `{"output":{"id":"i2","name":"second","tags":null}}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "demo", "item", "get", "--id", "i1", "--url", base)
	if code != 0 || out != "id: i1\nname: first\ntags: a, b\n" {
		t.Errorf("one host: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "list", "--host", "db", "--url", base)
	if code != 0 || out != "id  name   tags\ni1  first  []\n" {
		t.Errorf("--host by name: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "get", "--id", "i2", "--verbose", "--host", "h1", "--json", "--url", base)
	if code != 0 || out != `{"id":"i2","name":"second","tags":null}`+"\n" {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestHostResolutionFailures(t *testing.T) {
	base := newFakeAPI(t, testToken, hostList("h1:web", "h2:db"), hostList("h1:web", "h2:db"), hostList())
	cli := cliEnv{env: tokenEnv}
	_, errOut, code := cli.run(t, "demo", "item", "list", "--url", base)
	if code != ExitUsage || !strings.Contains(errOut, "h1 (web), h2 (db)") || !strings.Contains(errOut, "--host") {
		t.Errorf("two hosts without --host: exit %d, err %q", code, errOut)
	}
	_, errOut, code = cli.run(t, "demo", "item", "list", "--host", "mail", "--url", base)
	if code != ExitUsage || !strings.Contains(errOut, `"mail"`) || !strings.Contains(errOut, "h1 (web), h2 (db)") {
		t.Errorf("unknown host: exit %d, err %q", code, errOut)
	}
	_, errOut, code = cli.run(t, "demo", "item", "list", "--url", base)
	if code != ExitError || !strings.Contains(errOut, "no hosts") {
		t.Errorf("no hosts: exit %d, err %q", code, errOut)
	}
}

func TestReadControllerActionsAndResources(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.audit.list/v1/read", body: `{"input":{"limit":5,"actor":"user:u1"}}`,
			reply: `{"output":{"entries":[{"id":"a1","actor":"user:u1","action":"core.token.create"}],"nextBefore":null}}`},
		hostList("h1:web"),
		exchange{method: "GET", path: "/api/v1/hosts/h1/resources/demo/rule", reply: `{"name":"web","port":443}`},
		exchange{method: "POST", path: "/api/v1/actions/core.token.list/v1/read", body: `{"input":{}}`, status: 401, problem: true,
			reply: `{"type":"about:blank","title":"Authentication required","status":401,"detail":"authentication required","code":"auth.required"}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "audit", "list", "--limit", "5", "--actor", "user:u1", "--url", base)
	want := "entries:\nid  actor    action\na1  user:u1  core.token.create\nnextBefore: -\n"
	if code != 0 || out != want {
		t.Errorf("audit list: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "rule", "get", "--url", base)
	if code != 0 || out != "name: web\nport: 443\n" {
		t.Errorf("resource: exit %d, out %q, err %q", code, out, errOut)
	}
	_, errOut, code = cli.run(t, "token", "list", "--url", base)
	if code != ExitAuth || errOut != "darkpanel: authentication required (auth.required)\n" {
		t.Errorf("auth.required: exit %d, err %q", code, errOut)
	}
}

func TestBreakGlassReadUsesTheSocket(t *testing.T) {
	sock := newFakeSocket(t, exchange{method: "POST", path: "/api/v1/actions/core.user.list/v1/read", body: `{"input":{}}`,
		reply: `{"output":{"users":[{"id":"u1","username":"ann","role":"admin","state":"active","createdAt":"2026-10-18T00:00:00Z"}]}}`})
	out, errOut, code := cliEnv{env: tokenEnv}.run(t, "user", "list", "--socket", sock)
	want := "id  username  role   state   createdAt\nu1  ann       admin  active  2026-10-18T00:00:00Z\n"
	if code != 0 || out != want {
		t.Errorf("user list: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestUsageErrorsSendNothing(t *testing.T) {
	base := newFakeAPI(t, testToken)
	cli := cliEnv{env: tokenEnv}
	for _, args := range [][]string{
		{"demo", "item", "get", "--url", base},
		{"demo", "item", "get", "--id", "x", "extra", "--url", base},
		{"demo", "item", "get", "--id", "x", "--bogus", "--url", base},
		{"demo", "bogus"},
		{"bogus"},
		{"audit", "list", "--limit", "many", "--url", base},
	} {
		if _, errOut, code := cli.run(t, args...); code != ExitUsage {
			t.Errorf("darkpanel %q: exit %d, err %q", args, code, errOut)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestNewRootTree|TestNewRootRefusesClashes|TestReadAgentAction|TestHostResolutionFailures|TestReadControllerActionsAndResources|TestBreakGlassReadUsesTheSocket|TestUsageErrorsSendNothing)$' ./internal/cli/`
Expected: FAIL — the build fails: `internal/cli/root_test.go:35:15: undefined: NewRoot`, then `undefined: Options`, `undefined: FindCommand`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/root.go`:

```go
package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/core"
)

// Annotations on generated commands, naming the action or resource each
// one serves; FindCommand reads them.
const (
	AnnotationAction   = "darkpanel.action"
	AnnotationResource = "darkpanel.resource"
)

// Options configure NewRoot. Registry is required; production passes
// registry.Build(). A nil field takes the process's own: os.Stdin,
// os.Stdout, os.Stderr, os.Getenv, os.UserHomeDir, a terminal check of
// standard input and a no-echo read from it.
type Options struct {
	Registry *core.Registry
	// Commands are hand-written commands the assembly adds, such as serve
	// and version; no generated command may take their names.
	Commands    []*cobra.Command
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	UserHomeDir func() (string, error)
	// IsTerminal reports whether standard input is an interactive terminal.
	IsTerminal func() bool
	// ReadSecret reads one line from the terminal without echo.
	ReadSecret func() (string, error)
}

// app is the state the generated commands share.
type app struct {
	opts Options
	term *terminal
}

// NewRoot returns the darkpanel command tree: one command per action and
// resource of opts.Registry (spec section 15) plus the hand-written ones.
// It fails when two commands would clash. Errors of the returned tree go
// to PrintError and ExitCode.
func NewRoot(opts Options) (*cobra.Command, error) {
	if opts.Registry == nil {
		return nil, errors.New("cli: Options.Registry is required")
	}
	opts = withDefaults(opts)
	a := &app{opts: opts, term: &terminal{in: bufio.NewReader(opts.Stdin), out: opts.Stderr,
		isTerm: opts.IsTerminal(), readSecret: opts.ReadSecret}}
	root := &cobra.Command{
		Use:           "darkpanel",
		Short:         "Single-binary fleet manager for unmanaged Linux servers",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.SetIn(opts.Stdin)
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	reserved := slices.Clone(handWritten)
	for _, c := range opts.Commands {
		reserved = append(reserved, c.Name())
	}
	root.AddCommand(opts.Commands...)
	es, err := entries(opts.Registry, reserved)
	if err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	for _, e := range es {
		var leaf *cobra.Command
		if e.Resource != nil {
			leaf, err = a.resourceCommand(e)
		} else {
			leaf, err = a.actionCommand(e)
		}
		if err != nil {
			return nil, fmt.Errorf("cli: %w", err)
		}
		if leaf != nil {
			group(root, e.Path[:len(e.Path)-1]).AddCommand(leaf)
		}
	}
	markRuns(root)
	return root, nil
}

func withDefaults(o Options) Options {
	if o.Stdin == nil {
		o.Stdin = os.Stdin
	}
	if o.Stdout == nil {
		o.Stdout = os.Stdout
	}
	if o.Stderr == nil {
		o.Stderr = os.Stderr
	}
	if o.Getenv == nil {
		o.Getenv = os.Getenv
	}
	if o.UserHomeDir == nil {
		o.UserHomeDir = os.UserHomeDir
	}
	stdin, isFile := o.Stdin.(*os.File)
	if o.IsTerminal == nil {
		o.IsTerminal = func() bool { return isFile && term.IsTerminal(int(stdin.Fd())) } //nolint:gosec // G115: a file descriptor fits in an int
	}
	if o.ReadSecret == nil {
		o.ReadSecret = func() (string, error) {
			if !isFile {
				return "", errors.New("standard input is not a terminal")
			}
			b, err := term.ReadPassword(int(stdin.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
			return string(b), err
		}
	}
	return o
}

// group returns the command at path below root, creating the missing
// command groups. A group run with an unknown subcommand is a usage error;
// run alone it prints its help.
func group(root *cobra.Command, path []string) *cobra.Command {
	cur := root
	for i, word := range path {
		var next *cobra.Command
		for _, c := range cur.Commands() {
			if c.Name() == word {
				next = c
			}
		}
		if next == nil {
			next = &cobra.Command{
				Use:   word,
				Short: fmt.Sprintf("%s commands", strings.Join(path[:i+1], " ")),
				RunE: func(cmd *cobra.Command, args []string) error {
					if len(args) > 0 {
						return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
					}
					return cmd.Help()
				},
			}
			cur.AddCommand(next)
		}
		cur = next
	}
	return cur
}

// FindCommand returns the command of root generated for an action or
// resource name, the way the assembly's CLI parity test looks one up.
func FindCommand(root *cobra.Command, name string) (*cobra.Command, bool) {
	cmd, rest, err := root.Find(CommandPath(name))
	if err != nil || len(rest) > 0 {
		return nil, false
	}
	if cmd.Annotations[AnnotationAction] != name && cmd.Annotations[AnnotationResource] != name {
		return nil, false
	}
	return cmd, true
}

// actionCommand builds the command of an action: its input flags, the
// connection flags, --host for an agent-side action and --json.
func (a *app) actionCommand(e entry) (*cobra.Command, error) {
	def := *e.Action
	fields, err := inputFields(def.InputType)
	if err != nil {
		return nil, err
	}
	secrets, err := secretOutputs(def.OutputType)
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{
		Use:         e.Path[len(e.Path)-1],
		Short:       def.Spec.Summary,
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationAction: e.Name},
	}
	addInputFlags(cmd, fields)
	addConnFlags(cmd, def.Spec.BreakGlass, def.Spec.Side == core.SideAgent)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	if def.Spec.Class != core.ClassRead {
		return nil, nil // no mutating flow yet, so mutating actions get no command
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runRead(cmd, def, fields, secrets) }
	return cmd, nil
}
```

Create `internal/cli/read.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

// addConnFlags adds the flags that choose the server: --socket and --dev
// for break-glass commands, which use the controller socket (spec section
// 14), --url for every other; and --host for agent-side commands.
func addConnFlags(cmd *cobra.Command, breakGlass, agent bool) {
	if breakGlass {
		cmd.Flags().String("socket", "", "controller socket path (default: the production path, or the development path with --dev)")
		cmd.Flags().Bool("dev", false, "use the development controller socket under $XDG_RUNTIME_DIR or $XDG_STATE_HOME")
	} else {
		cmd.Flags().String("url", "", "API URL (default: $"+EnvURL+", then the config file, then "+DefaultURL+")")
	}
	if agent {
		cmd.Flags().String("host", "", "host id or name; may be omitted when there is exactly one host")
	}
}

// connect returns the client cmd's connection flags choose.
func (a *app) connect(cmd *cobra.Command) (*client, error) {
	if cmd.Flags().Lookup("socket") != nil {
		path, _ := cmd.Flags().GetString("socket")
		if path == "" {
			dev, _ := cmd.Flags().GetBool("dev")
			p, err := paths.Resolve(dev)
			if err != nil {
				return nil, err
			}
			path = p.ControllerSocket
		}
		return newSocketClient(path), nil
	}
	u, _ := cmd.Flags().GetString("url")
	return connectNetwork(u, a.opts.Getenv, a.opts.UserHomeDir)
}

// actionPath is the API path of op on def: under the host for an
// agent-side action, without one for a controller-side action.
func actionPath(def core.ActionDef, hostID, op string) string {
	tail := fmt.Sprintf("/actions/%s/v%d/%s", url.PathEscape(def.Spec.Name), def.Spec.Version, op)
	if def.Spec.Side == core.SideAgent {
		return "/api/v1/hosts/" + url.PathEscape(hostID) + tail
	}
	return "/api/v1" + tail
}

// hostRef is the part of core.host.list's Host the CLI reads.
type hostRef struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// resolveHost returns the id of the host --host names, by id or name, or
// of the only host when want is empty (spec section 15). Hosts come from
// core.host.list.
func resolveHost(ctx context.Context, c *client, want string) (string, error) {
	var resp struct {
		Output struct {
			Hosts []hostRef `json:"hosts"`
		} `json:"output"`
	}
	if err := c.do(ctx, http.MethodPost, "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, &resp); err != nil {
		return "", err
	}
	hosts := resp.Output.Hosts
	var listed []string
	for _, h := range hosts {
		listed = append(listed, h.ID+" ("+h.Name+")")
	}
	if want == "" {
		switch len(hosts) {
		case 0:
			return "", fmt.Errorf("the controller lists no hosts")
		case 1:
			return hosts[0].ID, nil
		}
		return "", usagef("there are %d hosts; choose one with --host: %s", len(hosts), strings.Join(listed, ", "))
	}
	var named []string
	for _, h := range hosts {
		if h.ID == want {
			return h.ID, nil
		}
		if h.Name == want {
			named = append(named, h.ID)
		}
	}
	switch len(named) {
	case 0:
		return "", usagef("no host %q; hosts: %s", want, strings.Join(listed, ", "))
	case 1:
		return named[0], nil
	}
	return "", usagef("several hosts are named %q; give the host id: %s", want, strings.Join(named, ", "))
}

// hostFor resolves cmd's --host for an agent-side action; a
// controller-side action has no host.
func hostFor(cmd *cobra.Command, c *client, side core.Side) (string, error) {
	if side != core.SideAgent {
		return "", nil
	}
	want, _ := cmd.Flags().GetString("host")
	return resolveHost(cmd.Context(), c, want)
}

// runRead runs a read action and shows its output.
func (a *app) runRead(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	var resp struct {
		Output json.RawMessage `json:"output"`
	}
	if err := c.do(cmd.Context(), http.MethodPost, actionPath(def, host, "read"), map[string]any{"input": input}, &resp); err != nil {
		return err
	}
	return a.show(cmd, resp.Output, secrets)
}

// resourceCommand builds `<module> <resource> get`.
func (a *app) resourceCommand(e entry) (*cobra.Command, error) {
	res := *e.Resource
	secrets, err := secretOutputs(res.OutputType)
	if err != nil {
		return nil, err
	}
	cmd := &cobra.Command{
		Use:         "get",
		Short:       "Read the " + res.Name + " resource of a host",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationResource: e.Name},
	}
	addConnFlags(cmd, false, true)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		c, err := a.connect(cmd)
		if err != nil {
			return err
		}
		host, err := hostFor(cmd, c, core.SideAgent)
		if err != nil {
			return err
		}
		module, name, _ := strings.Cut(res.Name, ".")
		var raw json.RawMessage
		path := "/api/v1/hosts/" + url.PathEscape(host) + "/resources/" + url.PathEscape(module) + "/" + url.PathEscape(name)
		if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
			return err
		}
		return a.show(cmd, raw, secrets)
	}
	return cmd, nil
}

// show writes an output: the raw JSON with --json, else rendered for
// people with the note on secret outputs.
func (a *app) show(cmd *cobra.Command, raw json.RawMessage, secrets []string) error {
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(w, "%s\n", raw)
		return err
	}
	if err := render(w, raw); err != nil {
		return err
	}
	noteSecrets(w, secrets, raw)
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestNewRootTree|TestNewRootRefusesClashes|TestReadAgentAction|TestHostResolutionFailures|TestReadControllerActionsAndResources|TestBreakGlassReadUsesTheSocket|TestUsageErrorsSendNothing)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/root.go internal/cli/read.go internal/cli/root_test.go
git commit -m "feat(cli): generate read and resource commands"
```

### Task 21: ADR 0010: narrow the Ed25519 claim

**Files:**
- Modify: `docs/adr/0010-web-certificate-authority.md`

**Interfaces:**
- Consumes: nothing.
- Produces: nothing.

**Items:** 39

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 0 = 0

Register row 39: ADR 0010 said browsers do not accept Ed25519 keys in X.509 chains, a broad claim nobody checked. While the ADR is Proposed it is corrected to the checked fact, the CA/Browser Forum Baseline Requirements' key list (section 6.1.5), and the rejected alternative is worded to match. The decision does not change.

- [ ] **Step 1: Correct the wording**

Edit `docs/adr/0010-web-certificate-authority.md`.

1. Replace this block:

```markdown
that authority Ed25519. Browsers do not accept Ed25519 keys in X.509
certificate chains, so the two decisions together would make the Tailscale
web listener unusable in a browser.
```

   with:

```markdown
that authority Ed25519. The CA/Browser Forum Baseline Requirements (section
6.1.5) admit only RSA and ECDSA P-256, P-384 and P-521 keys in publicly
trusted TLS certificates, so browsers cannot be relied on to verify an Ed25519
chain, and the two decisions together could leave the Tailscale web listener
unusable in a browser.
```

2. Replace this block:

```markdown
- Ed25519 web certificates from the ADR 0003 authority: rejected; browsers
  reject them.
```

   with:

```markdown
- Ed25519 web certificates from the ADR 0003 authority: rejected; browsers
  are not required to accept them.
```

- [ ] **Step 2: Run the checks**

Run: `timeout 60 bash scripts/check-adrs.sh --complete` — Expected: exit 0
Then run: `timeout 60 bash scripts/check-adrs.test.sh` — Expected: exit 0
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/modules/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 3: Commit**

```bash
git add docs/adr/0010-web-certificate-authority.md
git commit -m "docs(adr): narrow ADR 0010's Ed25519 claim"
```

### Task 22: Mutating flow: plan, guards, prompts, phrase and apply

**Files:**
- Create: `internal/cli/mutate.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/mutate_test.go`

**Interfaces:**
- Consumes: Contracts › Task 20 (`app`, `actionCommand`, `connect`, `hostFor`, `actionPath`, `NewRoot`, `FindCommand`; test helpers), Task 17 (`collectInput`, `terminal.line`), Task 18 (`render`, `noteSecrets`), Task 1 (`refusedf`, `sanitize`); `core.DiffLine`, `core.Guard`, `core.Class*`, `core.StatePending`, `core.StateFailed`, `core.StateReverted`, `core.StateInterrupted` (merged).
- Produces: Contracts › Task 22 (`planWire`, `changeWire`, `addMutatingFlags`, `runMutating`, `planAndApply`, `confirmation`, `printPlan`, `printChange`, `changeOutcome`; test helpers `planJSON`, `changeJSON`, `noExtra`).

**Items:** 3

**Depends on:** Task 20

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Spec section 15's mutating flow: plan, print the diff and guards, refuse with exit 3 on a failing guard, ask `Apply? [y/N]` (`--yes` answers only this), require the irreversible phrase from `--confirmation` or typed on the terminal, then apply. Without a terminal and without `--yes` the command refuses rather than hanging. Apply answers a `Change` with status 200 whatever its outcome, so a `failed`, `reverted` or `interrupted` change exits 1 with its code and message; a pending revertible change prints its id and revert deadline; `--confirm` sends `confirmNow`. `planAndApply` is separate so the enrollment flow (Task 29) reuses it. The tests prove every mutating action of the registry now has a command (the parity property of register row 3), and cover each class, every refusal, the failed change, `--json` and a hostile diff and guard.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/mutate_test.go`:

```go
package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// planJSON is a Plan answer (spec section 12); host and confirmation ""
// are null, and guards is the JSON list of guards.
func planJSON(planID, action, host, confirmation, guards string) string {
	nullable := func(s string) string {
		if s == "" {
			return "null"
		}
		return fmt.Sprintf("%q", s)
	}
	return fmt.Sprintf(`{"planId":%q,"hostId":%s,"action":%q,"version":1,"inputHash":"ih",`+
		`"diff":[{"op":"+","text":"add %s"},{"op":" ","text":"keep"}],"guards":%s,`+
		`"expiresAt":"2026-10-18T12:05:00Z","confirmation":%s}`,
		planID, nullable(host), action, action, guards, nullable(confirmation))
}

// changeJSON is a Change answer; extra holds the fields after "state".
func changeJSON(id, action, class, state, extra string) string {
	return fmt.Sprintf(`{"id":%q,"hostId":"h1","action":%q,"version":1,"class":%q,"state":%q,"actor":"token:t1",`+
		`"createdAt":"2026-10-18T12:00:00Z","updatedAt":"2026-10-18T12:00:01Z",%s}`, id, action, class, state, extra)
}

const noExtra = `"revertDeadline":null,"output":null,"error":null,"compensatedBy":null`

func TestEveryActionHasACommand(t *testing.T) {
	reg := testRegistry(t)
	root, err := NewRoot(Options{Registry: reg})
	if err != nil {
		t.Fatal(err)
	}
	for _, def := range reg.Actions() {
		cmd, ok := FindCommand(root, def.Spec.Name)
		if !ok {
			t.Errorf("%s has no command", def.Spec.Name)
			continue
		}
		mutating := def.Spec.Class != core.ClassRead
		if (cmd.Flags().Lookup("yes") != nil) != mutating {
			t.Errorf("%s: --yes present %v, want %v", def.Spec.Name, !mutating, mutating)
		}
		if (cmd.Flags().Lookup("confirmation") != nil) != (def.Spec.Class == core.ClassIrreversible) {
			t.Errorf("%s: --confirmation only on irreversible actions", def.Spec.Name)
		}
		if (cmd.Flags().Lookup("confirm") != nil) != (def.Spec.Class == core.ClassRevertible) {
			t.Errorf("%s: --confirm only on revertible actions", def.Spec.Name)
		}
	}
}

func TestMutatingCompensable(t *testing.T) {
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/plan",
			body:  `{"input":{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}}`,
			reply: planJSON("p1", "demo.item.add", "h1", "", `[{"id":"name-free","ok":true,"message":"the name is free"}]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p1"}`,
			reply: changeJSON("c1", "demo.item.add", "compensable", "completed",
				`"revertDeadline":null,"output":{"id":"i9","token":"tok-secret"},"error":null,"compensatedBy":"demo.item.remove"`)},
	)
	out, errOut, code := cliEnv{env: tokenEnv, stdin: "key-1\npass-1\n"}.run(t,
		"demo", "item", "add", "--name", "n1", "--yes", "--url", base)
	want := "Plan for demo.item.add/v1 on host h1:\n" +
		"  + add demo.item.add\n" +
		"    keep\n" +
		"Guards:\n" +
		"  ok      name-free: the name is free\n" +
		"Change c1: completed\n" +
		"id: i9\n" +
		"token: tok-secret\n" +
		"\nThe token above will not be shown again; store it now.\n"
	if code != 0 || out != want {
		t.Errorf("exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
}

func TestMutatingRevertible(t *testing.T) {
	limits := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(limits, []byte(`{"limits":{"max":10}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	input := `{"input":{"limits":{"max":10},"name":"web","port":443}}`
	base := newFakeAPI(t, testToken,
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/plan", body: input,
			reply: planJSON("p2", "demo.rule.set", "h1", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/apply", body: `{"planId":"p2"}`,
			reply: changeJSON("c2", "demo.rule.set", "revertible", "pending",
				`"revertDeadline":"2026-10-18T12:02:00Z","output":{"name":"web","port":443},"error":null,"compensatedBy":null`)},
		hostList("h1:web"),
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/plan", body: input,
			reply: planJSON("p3", "demo.rule.set", "h1", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.rule.set/v1/apply", body: `{"planId":"p3","confirmNow":true}`,
			reply: changeJSON("c3", "demo.rule.set", "revertible", "confirmed", noExtra)},
	)
	cli := cliEnv{env: tokenEnv}
	args := []string{"demo", "rule", "set", "--name", "web", "--port", "443", "--input", limits, "--yes", "--url", base}
	out, errOut, code := cli.run(t, args...)
	if code != 0 || !strings.Contains(out, "Change c2: pending\n") ||
		!strings.Contains(out, "Confirm it before 2026-10-18T12:02:00Z with: darkpanel change confirm c2\n") ||
		!strings.Contains(out, "name: web\nport: 443\n") {
		t.Errorf("pending: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, append(args, "--confirm")...)
	if code != 0 || !strings.Contains(out, "Change c3: confirmed\n") || strings.Contains(out, "Confirm it before") {
		t.Errorf("--confirm: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestMutatingIrreversible(t *testing.T) {
	plan := func(id string) exchange {
		return exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.remove/v1/plan", body: `{"input":{"id":"i1"}}`,
			reply: planJSON(id, "demo.item.remove", "h1", "i1", `[]`)}
	}
	apply := func(id string) exchange {
		return exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.remove/v1/apply",
			body: `{"planId":"` + id + `","confirmation":"i1"}`, reply: changeJSON("c"+id, "demo.item.remove", "irreversible", "completed", noExtra)}
	}
	base := newFakeAPI(t, testToken,
		hostList("h1:web"), plan("p1"), apply("p1"),
		hostList("h1:web"), plan("p2"),
		hostList("h1:web"), plan("p3"),
		hostList("h1:web"), plan("p4"), apply("p4"),
		hostList("h1:web"), plan("p5"),
	)
	args := []string{"demo", "item", "remove", "--id", "i1", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, append(args, "--yes", "--confirmation", "i1")...); code != 0 || !strings.Contains(out, "Change cp1: completed") {
		t.Errorf("--confirmation: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, append(args, "--yes", "--confirmation", "i2")...); code != ExitRefused || !strings.Contains(errOut, `"i1"`) {
		t.Errorf("wrong --confirmation: exit %d, err %q", code, errOut)
	}
	if _, errOut, code := cli.run(t, append(args, "--yes")...); code != ExitRefused || !strings.Contains(errOut, "--confirmation i1") {
		t.Errorf("no terminal, no --confirmation: exit %d, err %q", code, errOut)
	}
	term := cliEnv{env: tokenEnv, terminal: true, stdin: "y\ni1\n"}
	if out, errOut, code := term.run(t, args...); code != 0 || !strings.Contains(out, "Change cp4: completed") ||
		errOut != "Apply? [y/N] This cannot be undone. Type i1 to confirm: " {
		t.Errorf("terminal: exit %d, out %q, err %q", code, out, errOut)
	}
	term.stdin = "y\ni2\n"
	if _, errOut, code := term.run(t, args...); code != ExitRefused || !strings.Contains(errOut, "does not match") {
		t.Errorf("terminal, wrong phrase: exit %d, err %q", code, errOut)
	}
}

func TestMutatingRefusals(t *testing.T) {
	plan := func(id, guards string) exchange {
		return exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON(id, "core.token.create", "", "", guards)}
	}
	base := newFakeAPI(t, testToken,
		plan("p1", `[{"id":"owner-active","ok":false,"message":"the owner is not active"}]`),
		plan("p2", `[]`),
		plan("p3", `[]`),
		plan("p4", `[]`),
	)
	args := []string{"token", "create", "--name", "ci", "--scope", "read", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, append(args, "--yes")...); code != ExitRefused ||
		!strings.Contains(out, "FAILED  owner-active: the owner is not active") || !strings.Contains(errOut, "owner-active") {
		t.Errorf("failing guard: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, args...); code != ExitRefused || !strings.Contains(errOut, "--yes") {
		t.Errorf("no terminal: exit %d, err %q", code, errOut)
	}
	for _, answer := range []string{"n\n", ""} {
		_, errOut, code := cliEnv{env: tokenEnv, terminal: true, stdin: answer}.run(t, args...)
		if code != ExitRefused || !strings.Contains(errOut, "not applied") {
			t.Errorf("answer %q: exit %d, err %q", answer, code, errOut)
		}
	}
}

func TestMutatingFailedChangeAndJSON(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON("p1", "core.token.create", "", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/apply", body: `{"planId":"p1"}`,
			reply: changeJSON("c1", "core.token.create", "compensable", "failed",
				`"revertDeadline":null,"output":null,"error":{"code":"internal","message":"Internal error"},"compensatedBy":"core.token.revoke"`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: planJSON("p2", "core.token.create", "", "", `[]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/apply", body: `{"planId":"p2"}`,
			reply: changeJSON("c2", "core.token.create", "compensable", "completed", noExtra)},
	)
	args := []string{"token", "create", "--name", "ci", "--scope", "read", "--yes", "--url", base}
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, args...); code != ExitError || !strings.Contains(out, "Change c1: failed") ||
		errOut != "darkpanel: change c1 failed: Internal error (internal)\n" {
		t.Errorf("failed change: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code := cli.run(t, append(args, "--json")...)
	if code != 0 || out != changeJSON("c2", "core.token.create", "compensable", "completed", noExtra)+"\n" ||
		!strings.Contains(errOut, "Plan for core.token.create/v1:") {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestBreakGlassMutatingUsesTheSocket(t *testing.T) {
	sock := newFakeSocket(t,
		exchange{method: "POST", path: "/api/v1/actions/core.user.delete/v1/plan", body: `{"input":{"username":"bob"}}`,
			reply: planJSON("p1", "core.user.delete", "", "bob", `[{"id":"not-last-admin","ok":true,"message":"another admin remains"}]`)},
		exchange{method: "POST", path: "/api/v1/actions/core.user.delete/v1/apply", body: `{"planId":"p1","confirmation":"bob"}`,
			reply: changeJSON("c1", "core.user.delete", "irreversible", "completed", `"revertDeadline":null,"output":{},"error":null,"compensatedBy":null`)},
	)
	out, errOut, code := cliEnv{}.run(t, "user", "delete", "--username", "bob", "--yes", "--confirmation", "bob", "--socket", sock)
	if code != 0 || !strings.Contains(out, "Change c1: completed\n") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestMutatingSanitizesThePlan(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/actions/core.token.create/v1/plan", body: `{"input":{"name":"ci","scope":"read"}}`,
			reply: `{"planId":"p1","hostId":null,"action":"core.token.create","version":1,"inputHash":"ih",` +
				`"diff":[{"op":"+","text":"name \u001b[2Jci\u202e"}],"guards":[{"id":"g\u001b","ok":false,"message":"no\u2066pe"}],` +
				`"expiresAt":"2026-10-18T12:05:00Z","confirmation":null}`},
	)
	out, errOut, code := cliEnv{env: tokenEnv}.run(t, "token", "create", "--name", "ci", "--scope", "read", "--yes", "--url", base)
	if code != ExitRefused || !strings.Contains(out, `  + name \x1b[2Jci\u202e`) || !strings.Contains(out, `FAILED  g\x1b: no\u2066pe`) ||
		strings.ContainsAny(out+errOut, "\x1b\u202e\u2066") {
		t.Errorf("exit %d, out %q, err %q", code, out, errOut)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestEveryActionHasACommand|TestMutatingCompensable|TestMutatingRevertible|TestMutatingIrreversible|TestMutatingRefusals|TestMutatingFailedChangeAndJSON|TestBreakGlassMutatingUsesTheSocket|TestMutatingSanitizesThePlan)$' ./internal/cli/`
Expected: FAIL — the tests fail (they reference no new identifier): `TestEveryActionHasACommand` reports `core.module.disable has no command` and the same for every other mutating action, and the flow tests fail with `darkpanel: unknown flag: --name` (or `--id`), exit 2.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/mutate.go`:

```go
package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// planWire is the part of the API's Plan (spec section 12) the CLI reads.
type planWire struct {
	PlanID       string          `json:"planId"`
	HostID       *string         `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	Confirmation *string         `json:"confirmation"`
}

// changeWire is the part of the API's Change the CLI reads.
type changeWire struct {
	ID             string          `json:"id"`
	HostID         *string         `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Class          core.Class      `json:"class"`
	State          string          `json:"state"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
	CompensatedBy *string `json:"compensatedBy"`
}

// addMutatingFlags adds --yes, and --confirmation for an irreversible
// action or --confirm for a revertible one.
func addMutatingFlags(cmd *cobra.Command, class core.Class) {
	cmd.Flags().Bool("yes", false, "apply without asking Apply? [y/N]; an irreversible action still needs its confirmation phrase")
	switch class {
	case core.ClassIrreversible:
		cmd.Flags().String("confirmation", "", "the confirmation phrase the plan names, instead of typing it")
	case core.ClassRevertible:
		cmd.Flags().Bool("confirm", false, "confirm the change at once instead of leaving it pending until its revert deadline")
	}
}

// runMutating runs the mutating flow of one action (spec section 15) and
// shows the change: with --json its raw JSON on standard output, else its
// state and output for people.
func (a *app) runMutating(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	ch, raw, err := a.planAndApply(cmd, c, def, host, input, false)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		if _, err := fmt.Fprintf(out, "%s\n", raw); err != nil {
			return err
		}
	} else if err := printChange(out, ch, secrets); err != nil {
		return err
	}
	return changeOutcome(ch)
}

// planAndApply plans def with input, shows the plan, asks, and applies. A
// failing guard, a declined question or a wrong phrase refuses before
// anything is applied. The plan goes to standard output, or to standard
// error with --json. agreed skips the Apply? [y/N] question, as --yes
// does, for a step of a flow the operator already agreed to; it never
// skips the confirmation phrase.
func (a *app) planAndApply(cmd *cobra.Command, c *client, def core.ActionDef, host string, input any,
	agreed bool) (changeWire, json.RawMessage, error) {
	ctx := cmd.Context()
	info := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		info = cmd.ErrOrStderr()
	}
	var plan planWire
	if err := c.do(ctx, http.MethodPost, actionPath(def, host, "plan"), map[string]any{"input": input}, &plan); err != nil {
		return changeWire{}, nil, err
	}
	printPlan(info, plan)
	for _, g := range plan.Guards {
		if !g.OK {
			return changeWire{}, nil, refusedf("guard %s failed: %s; nothing was applied", g.ID, g.Message)
		}
	}
	if yes, _ := cmd.Flags().GetBool("yes"); !yes && !agreed {
		if !a.term.isTerm {
			return changeWire{}, nil, refusedf("standard input is not a terminal; pass --yes to apply")
		}
		answer, err := a.term.line("Apply? [y/N] ")
		if err != nil && !errors.Is(err, io.EOF) {
			return changeWire{}, nil, err
		}
		if ans := strings.ToLower(strings.TrimSpace(answer)); ans != "y" && ans != "yes" {
			return changeWire{}, nil, refusedf("not applied")
		}
	}
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil && *plan.Confirmation != "" {
		phrase, err := a.confirmation(cmd, *plan.Confirmation)
		if err != nil {
			return changeWire{}, nil, err
		}
		body["confirmation"] = phrase
	}
	if now, _ := cmd.Flags().GetBool("confirm"); now {
		body["confirmNow"] = true
	}
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodPost, actionPath(def, host, "apply"), body, &raw); err != nil {
		return changeWire{}, nil, err
	}
	var ch changeWire
	if err := json.Unmarshal(raw, &ch); err != nil {
		return changeWire{}, nil, fmt.Errorf("decode the change: %w", err)
	}
	return ch, raw, nil
}

// confirmation returns the phrase an irreversible plan needs: from
// --confirmation, else typed on the terminal. A phrase that differs is
// refused here, before the apply.
func (a *app) confirmation(cmd *cobra.Command, want string) (string, error) {
	given, _ := cmd.Flags().GetString("confirmation")
	if given == "" {
		if !a.term.isTerm {
			return "", refusedf("this change cannot be undone: pass --confirmation %s, or run on a terminal", want)
		}
		var err error
		given, err = a.term.line(fmt.Sprintf("This cannot be undone. Type %s to confirm: ", sanitize(want)))
		if err != nil && !errors.Is(err, io.EOF) {
			return "", err
		}
	}
	if given != want {
		return "", refusedf("the confirmation does not match %q; nothing was applied", want)
	}
	return given, nil
}

// printPlan writes the plan's diff and guards, sanitized.
func printPlan(w io.Writer, p planWire) {
	target := fmt.Sprintf("%s/v%d", p.Action, p.Version)
	if p.HostID != nil {
		target += " on host " + *p.HostID
	}
	_, _ = fmt.Fprintf(w, "Plan for %s:\n", sanitize(target))
	if len(p.Diff) == 0 {
		_, _ = fmt.Fprintln(w, "  (no changes)")
	}
	for _, d := range p.Diff {
		_, _ = fmt.Fprintf(w, "  %s %s\n", sanitize(d.Op), sanitize(d.Text))
	}
	if len(p.Guards) == 0 {
		return
	}
	_, _ = fmt.Fprintln(w, "Guards:")
	for _, g := range p.Guards {
		status := "ok    "
		if !g.OK {
			status = "FAILED"
		}
		_, _ = fmt.Fprintf(w, "  %s  %s: %s\n", status, sanitize(g.ID), sanitize(g.Message))
	}
}

// printChange writes a change's state, how to confirm a pending one, and
// its output with the note on secret outputs.
func printChange(w io.Writer, ch changeWire, secrets []string) error {
	_, _ = fmt.Fprintf(w, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State))
	if ch.State == core.StatePending && ch.RevertDeadline != nil {
		_, _ = fmt.Fprintf(w, "Confirm it before %s with: darkpanel change confirm %s\n",
			ch.RevertDeadline.UTC().Format(time.RFC3339), sanitize(ch.ID))
		_, _ = fmt.Fprintln(w, "Unless confirmed by then, it is reverted.")
	}
	if len(ch.Output) == 0 || string(ch.Output) == "null" {
		return nil
	}
	if err := render(w, ch.Output); err != nil {
		return err
	}
	noteSecrets(w, secrets, ch.Output)
	return nil
}

// changeOutcome is the error of a change that did not end well: failed,
// reverted or interrupted. Apply answers such changes with status 200
// (spec section 12), so the CLI decides the exit code from the state.
func changeOutcome(ch changeWire) error {
	switch ch.State {
	case core.StateFailed, core.StateReverted, core.StateInterrupted:
	default:
		return nil
	}
	if ch.Error != nil {
		return fmt.Errorf("change %s %s: %s (%s)", ch.ID, ch.State, ch.Error.Message, ch.Error.Code)
	}
	return fmt.Errorf("change %s %s", ch.ID, ch.State)
}
```

In `internal/cli/root.go`, edit 1 of 2. Replace this block:

```go
// actionCommand builds the command of an action: its input flags, the
// connection flags, --host for an agent-side action and --json.
func (a *app) actionCommand(e entry) (*cobra.Command, error) {
```

with:

```go
// actionCommand builds the command of an action: its input flags, the
// connection flags, --host for an agent-side action, --json, and for a
// mutating action the flags of the mutating flow.
func (a *app) actionCommand(e entry) (*cobra.Command, error) {
```

In `internal/cli/root.go`, edit 2 of 2. Replace this block:

```go
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	if def.Spec.Class != core.ClassRead {
		return nil, nil // no mutating flow yet, so mutating actions get no command
	}
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runRead(cmd, def, fields, secrets) }
	return cmd, nil
```

with:

```go
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	if def.Spec.Class == core.ClassRead {
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runRead(cmd, def, fields, secrets) }
		return cmd, nil
	}
	addMutatingFlags(cmd, def.Spec.Class)
	cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runMutating(cmd, def, fields, secrets) }
	return cmd, nil
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestEveryActionHasACommand|TestMutatingCompensable|TestMutatingRevertible|TestMutatingIrreversible|TestMutatingRefusals|TestMutatingFailedChangeAndJSON|TestBreakGlassMutatingUsesTheSocket|TestMutatingSanitizesThePlan)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/mutate.go internal/cli/root.go internal/cli/mutate_test.go
git commit -m "feat(cli): plan, confirm and apply changes"
```

### Task 23: Host directory and module availability

**Files:**
- Create: `internal/controller/dispatch/hosts.go`
- Test: `internal/controller/dispatch/fixture_test.go`
- Test: `internal/controller/dispatch/hosts_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 14 and 19 (`runtime.Open`, `runtime.Config`, `runtime.ReasonMigrationFailed`, `*Runtime` as `core.AgentClient`); `core.HostDirectory`, `coreactions.ReasonNotReported`, `core_module_state` (P0), `coretest.FakeAgent`
- Produces: Contracts › `internal/controller/dispatch` Task 23 (`Hosts`, `ReasonDisabled`, `usable`, the test harness)

**Items:** 5

**Depends on:** Task 19

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Spec section 2's enablement rules on the controller: the first reach of a host says Hello and writes each module's availability into `core_module_state`, seeding missing rows from `DefaultEnabled` and never touching `enabled` on an existing row; `usable` answers `module.unavailable` with the reason. The harness is a real runtime over its own database with fixture modules that fail a migration, start disabled or fail preflight. The tests check the rows Hello writes, every unusable reason, that a second Hello keeps `enabled`, Hello once per process with a retry after failure, and refusal of unknown and mismatched hosts.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/fixture_test.go`:

```go
package dispatch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"path/filepath"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// The fixture modules are registered only in these tests:
//   - kv keeps items in its agent table kv_items, with a read, a revertible
//     set and a resource;
//   - bad has an agent migration that fails;
//   - off is disabled by default;
//   - flaky fails its preflight check;
//   - ctl has controller-side actions only.

var t0 = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const testHost = "h1"

var (
	refGet    = core.ActionRef{Name: "kv.item.get", Version: 1}
	refSet    = core.ActionRef{Name: "kv.item.set", Version: 1}
	refCtlGet = core.ActionRef{Name: "ctl.note.get", Version: 1}
	refCtlSet = core.ActionRef{Name: "ctl.note.set", Version: 1}
)

type itemIn struct {
	Name  string `json:"name"`
	Value string `json:"value,omitempty"`
}

type itemOut struct {
	Value string `json:"value"`
	Actor string `json:"actor"`
}

type status struct {
	Count int    `json:"count"`
	Token string `json:"token" secret:"true"`
}

func readItem(ctx context.Context, env core.Env, name string) (string, bool, error) {
	var v string
	err := env.DB.Read().QueryRowContext(ctx, "SELECT value FROM kv_items WHERE name = ?", name).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	return v, err == nil, err
}

type getItem struct{}

func (getItem) Read(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return itemOut{Value: v, Actor: env.Principal.Actor}, err
}

type itemSnap struct {
	Name, Value string
	Present     bool
}

type setItem struct{}

func (setItem) Plan(ctx context.Context, env core.Env, in itemIn) (core.PlanOutcome, error) {
	v, _, err := readItem(ctx, env, in.Name)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name + "=" + in.Value}}, StateHash: v}, err
}

func (setItem) Apply(ctx context.Context, env core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Value}, env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO kv_items (name, value) VALUES (?, ?)
			ON CONFLICT (name) DO UPDATE SET value = excluded.value`, in.Name, in.Value)
		return err
	})
}

func (setItem) Verify(ctx context.Context, env core.Env, in itemIn, _ itemOut) error {
	v, _, err := readItem(ctx, env, in.Name)
	if err == nil && v != in.Value {
		err = fmt.Errorf("item %s is %q", in.Name, v)
	}
	return err
}

func (setItem) Snapshot(ctx context.Context, env core.Env, in itemIn) ([]byte, error) {
	v, ok, err := readItem(ctx, env, in.Name)
	if err != nil {
		return nil, err
	}
	return json.Marshal(itemSnap{Name: in.Name, Value: v, Present: ok})
}

func (setItem) Revert(ctx context.Context, env core.Env, snapshot []byte) error {
	var s itemSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return err
	}
	return env.DB.Write(ctx, func(tx *sql.Tx) error {
		if !s.Present {
			_, err := tx.ExecContext(ctx, "DELETE FROM kv_items WHERE name = ?", s.Name)
			return err
		}
		_, err := tx.ExecContext(ctx, "UPDATE kv_items SET value = ? WHERE name = ?", s.Value, s.Name)
		return err
	})
}

type kvModule struct{}

func (kvModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: "kv", Version: 1, DefaultEnabled: true,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: refGet.Name, Version: 1, Side: core.SideAgent}, getItem{}),
			core.DefineRevertible(core.ActionSpec{Name: refSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"kv"}}, setItem{}),
		},
		Resources: []core.ResourceDef{core.DefineResource("kv.status", func(ctx context.Context, env core.Env) (status, error) {
			var n int
			err := env.DB.Read().QueryRowContext(ctx, "SELECT count(*) FROM kv_items").Scan(&n)
			return status{Count: n, Token: "kv-secret-token"}, err
		})},
		Migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_kv.sql": {Data: []byte("CREATE TABLE kv_items (name TEXT PRIMARY KEY, value TEXT NOT NULL) STRICT;")},
		}},
	}
}

// echo is the read handler of the modules that only need an action.
type echo struct{}

func (echo) Read(_ context.Context, env core.Env, in itemIn) (itemOut, error) {
	return itemOut{Value: in.Name, Actor: env.Principal.Actor}, nil
}

// readOnly is a module with one agent read action <id>.item.get.
type readOnly struct {
	id             string
	defaultEnabled bool
	migrations     map[core.Side]fs.FS
	preflight      []core.PreflightCheck
}

func (m readOnly) Manifest() core.Manifest {
	return core.Manifest{
		ID: m.id, Version: 1, DefaultEnabled: m.defaultEnabled, Migrations: m.migrations, Preflight: m.preflight,
		Actions: []core.ActionDef{core.DefineRead(core.ActionSpec{Name: m.id + ".item.get", Version: 1, Side: core.SideAgent}, echo{})},
	}
}

// note is the controller-side state of module ctl.
type note struct{ text string }

type ctlGet struct{ n *note }

func (h ctlGet) Read(context.Context, core.Env, itemIn) (itemOut, error) {
	return itemOut{Value: h.n.text}, nil
}

type ctlSet struct{ n *note }

func (h ctlSet) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Value}}, StateHash: h.n.text}, nil
}

func (h ctlSet) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	h.n.text = in.Value
	return itemOut{Value: in.Value}, nil
}

func (ctlSet) Verify(context.Context, core.Env, itemIn, itemOut) error { return nil }

type ctlModule struct{ state *note }

func (m ctlModule) Manifest() core.Manifest {
	return core.Manifest{ID: "ctl", Version: 1, DefaultEnabled: true, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: refCtlGet.Name, Version: 1, Side: core.SideController}, ctlGet{m.state}),
		core.DefineMutating(core.ActionSpec{
			Name: refCtlSet.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: refCtlSet.Name,
		}, ctlSet{m.state}),
	}}
}

func modules() []core.Module {
	return []core.Module{
		kvModule{},
		readOnly{id: "bad", defaultEnabled: true, migrations: map[core.Side]fs.FS{core.SideAgent: fstest.MapFS{
			"0001_bad.sql": {Data: []byte("CREATE TABLE bad_items (")},
		}}},
		readOnly{id: "off"},
		readOnly{id: "flaky", defaultEnabled: true, preflight: []core.PreflightCheck{{ID: "ready",
			Run: func(context.Context, core.Env) error { return errors.New("flaky: the host is not ready") }}}},
		ctlModule{state: &note{}},
	}
}

// harness is a controller database with the local host h1, served by a real
// agent runtime over its own database.
type harness struct {
	t       *testing.T
	clock   *coretest.FakeClock
	reg     *core.Registry
	db      *store.DB // the controller database
	agentDB *store.DB
	rt      *runtime.Runtime
	hosts   *Hosts
}

func migrated(t *testing.T, path, side string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{Side: side, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups-"+side), Keep: 5, Now: t0}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	h := &harness{t: t, clock: coretest.NewFakeClock(t0)}
	var err error
	if h.reg, err = core.NewRegistry(modules()...); err != nil {
		t.Fatal(err)
	}
	h.db = migrated(t, filepath.Join(dir, "controller.db"), store.SideController)
	if _, err := h.db.Writer().ExecContext(ctx,
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, 'local', 1, '2026-10-17T00:00:00Z')", testHost); err != nil {
		t.Fatal(err)
	}
	if h.agentDB, err = store.Open(ctx, filepath.Join(dir, "agent.db")); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.agentDB.Close() })
	log := slog.New(slog.DiscardHandler)
	h.rt, err = runtime.Open(ctx, runtime.Config{
		DB: h.agentDB, Sealer: coretest.Sealer{}, Clock: h.clock, Log: log, Registry: h.reg, HostID: testHost,
		BinaryVersion: "test", BackupDir: filepath.Join(dir, "backups-agent"), Exec: platform.NewRunner(nil),
		FilesTestMode: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(h.rt.Close)
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clock: h.clock, Log: log})
	h.hosts.Add(testHost, h.rt)
	return h
}

func (h *harness) count(query string, args ...any) int {
	h.t.Helper()
	var n int
	if err := h.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		h.t.Fatal(err)
	}
	return n
}

func wantCode(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
	return e
}
```

Create `internal/controller/dispatch/hosts_test.go`:

```go
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"log/slog"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

type stateRow struct {
	Enabled, Available bool
	Reason             string // "<null>" for NULL
	CheckedAt          string
}

func (h *harness) states() map[string]stateRow {
	h.t.Helper()
	rows, err := h.db.Read().Query(
		"SELECT module_id, enabled, available, reason, checked_at FROM core_module_state WHERE host_id = ?", testHost)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	out := map[string]stateRow{}
	for rows.Next() {
		var id string
		var r stateRow
		var reason sql.NullString
		if err := rows.Scan(&id, &r.Enabled, &r.Available, &reason, &r.CheckedAt); err != nil {
			h.t.Fatal(err)
		}
		r.Reason = "<null>"
		if reason.Valid {
			r.Reason = reason.String
		}
		out[id] = r
	}
	return out
}

func TestFirstReachWritesModuleState(t *testing.T) {
	h := newHarness(t)
	c, err := h.hosts.Agent(context.Background(), testHost)
	if err != nil {
		t.Fatal(err)
	}
	if c != h.rt {
		t.Errorf("Agent returned %v, want the runtime", c)
	}
	at := "2026-10-17T12:00:00.000000000Z"
	want := map[string]stateRow{
		"kv":    {true, true, "<null>", at},
		"bad":   {true, false, runtime.ReasonMigrationFailed, at},
		"off":   {false, true, "<null>", at},
		"flaky": {true, false, "flaky: the host is not ready", at},
		"ctl":   {true, true, "<null>", at},
	}
	if got := h.states(); !reflect.DeepEqual(got, want) {
		t.Errorf("module state =\n%+v\nwant\n%+v", got, want)
	}
}

func TestUsable(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if _, err := h.hosts.Agent(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if err := h.hosts.usable(ctx, testHost, "kv"); err != nil {
		t.Errorf("kv: %v, want usable", err)
	}
	for module, reason := range map[string]string{
		"bad":   runtime.ReasonMigrationFailed,
		"off":   ReasonDisabled,
		"flaky": "flaky: the host is not ready",
		"nope":  coreactions.ReasonNotReported,
	} {
		e := wantCode(t, h.hosts.usable(ctx, testHost, module), errcode.ModuleUnavailable)
		if want := "module " + module + " is unavailable on host h1: " + reason; e.Detail != want {
			t.Errorf("%s: detail %q, want %q", module, e.Detail, want)
		}
	}
	// Disabled wins over any availability.
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'flaky'"); err != nil {
		t.Fatal(err)
	}
	e := wantCode(t, h.hosts.usable(ctx, testHost, "flaky"), errcode.ModuleUnavailable)
	if !strings.HasSuffix(e.Detail, ": "+ReasonDisabled) {
		t.Errorf("disabled flaky: detail %q", e.Detail)
	}
}

func TestHelloNeverChangesEnabled(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	if err := h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec(
		"UPDATE core_module_state SET enabled = 1 - enabled WHERE module_id IN ('kv', 'off')"); err != nil {
		t.Fatal(err)
	}
	h.clock.Advance(time.Minute)
	if err := h.hosts.Hello(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	got := h.states()
	at := "2026-10-17T12:01:00.000000000Z"
	if got["kv"] != (stateRow{false, true, "<null>", at}) || got["off"] != (stateRow{true, true, "<null>", at}) {
		t.Errorf("after a second Hello: kv %+v, off %+v; want enabled kept and checked now", got["kv"], got["off"])
	}
}

func TestAgentSaysHelloOnceAndRetriesAFailure(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hellos := 0
	fail := true
	h.hosts.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		hellos++
		if fail {
			return core.AgentHello{}, errors.New("agent offline")
		}
		return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
	}})
	if _, err := h.hosts.Agent(ctx, "h2"); err == nil {
		t.Fatal("Agent succeeded although Hello failed")
	}
	fail = false
	for range 2 {
		if _, err := h.hosts.Agent(ctx, "h2"); err != nil {
			t.Fatal(err)
		}
	}
	if hellos != 2 {
		t.Errorf("Hello calls = %d, want 2 (one failure, one success)", hellos)
	}
}

func TestAgentRefusesUnknownAndMismatchedHosts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	_, err := h.hosts.Agent(ctx, "h9")
	_ = wantCode(t, err, errcode.NotFound)
	h.hosts.Add("h2", &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		return core.AgentHello{ProtocolVersion: 1, HostID: "h3"}, nil
	}})
	_, err = h.hosts.Agent(ctx, "h2")
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "answered Hello as h3") {
		t.Errorf("Agent(h2) = %v, want a plain error naming the mismatch", err)
	}
	if n := h.count("SELECT count(*) FROM core_module_state WHERE host_id = 'h2'"); n != 0 {
		t.Errorf("rows of the mismatched host = %d, want 0", n)
	}
}

func TestHelloSkipsModulesTheRegistryLacks(t *testing.T) {
	h := newHarness(t)
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	h.hosts.Add(testHost, &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
		return core.AgentHello{ProtocolVersion: 1, HostID: testHost, Modules: []core.ModuleStatus{
			{ID: "kv", Version: 1, Available: true}, {ID: "ghost", Version: 1, Available: true},
			{ID: core.ModuleIDCore, Version: 1, Available: true},
		}}, nil
	}})
	if err := h.hosts.Hello(context.Background(), testHost); err != nil {
		t.Fatal(err)
	}
	if got := h.states(); len(got) != 1 || !got["kv"].Available {
		t.Errorf("module state = %+v, want only kv", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestFirstReach|TestUsable|TestHello|TestAgent' ./internal/controller/dispatch/`

Expected: FAIL — the build reports `undefined: Hosts`, `undefined: NewHosts` and `undefined: HostsConfig`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/dispatch [github.com/darkraise/darkpanel/internal/controller/dispatch.test]
internal/controller/dispatch/fixture_test.go:223:11: undefined: Hosts
internal/controller/dispatch/fixture_test.go:274:12: undefined: NewHosts
internal/controller/dispatch/fixture_test.go:274:21: undefined: HostsConfig
internal/controller/dispatch/hosts_test.go:84:12: undefined: ReasonDisabled
internal/controller/dispatch/hosts_test.go:98:39: undefined: ReasonDisabled
internal/controller/dispatch/hosts_test.go:170:12: undefined: NewHosts
internal/controller/dispatch/hosts_test.go:170:21: undefined: HostsConfig
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/dispatch/hosts.go`:

```go
// Package dispatch routes the calls of the HTTP API and the controller socket
// (spec section 3, Dispatch): controller-side actions to the controller's
// lifecycle engine, agent actions and resources to the host's AgentClient. It
// keeps core_module_state current from each agent's Hello, refuses the
// actions and resources of a module that is not usable on a host, and records
// which host owns every agent change.
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// ReasonDisabled is the reason of a module an operator disabled on a host.
const ReasonDisabled = "disabled"

// timeFormat is RFC 3339 in UTC with a fixed width, as every core table
// stores times.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// HostsConfig wires a Hosts.
type HostsConfig struct {
	DB       *store.DB      // the controller database: core_module_state
	Registry *core.Registry // the one registry of the process
	Clock    core.Clock
	Log      *slog.Logger
}

// Hosts is the controller's core.HostDirectory: the agent client of each
// host. The first time a host is reached in this process, Hosts calls its
// Hello and records every module's availability; a failed Hello is tried
// again on the next reach. Its methods are safe for concurrent use.
type Hosts struct {
	cfg HostsConfig

	mu      sync.Mutex // also serialises Hello calls
	clients map[string]core.AgentClient
	greeted map[string]bool
}

var _ core.HostDirectory = (*Hosts)(nil)

// NewHosts returns an empty directory; Add registers each host's client.
func NewHosts(cfg HostsConfig) *Hosts {
	return &Hosts{cfg: cfg, clients: map[string]core.AgentClient{}, greeted: map[string]bool{}}
}

// Add registers the client of hostID, a row of core_hosts. In M1 the server
// adds the local host with the in-process agent runtime.
func (h *Hosts) Add(hostID string, c core.AgentClient) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.clients[hostID] = c
	delete(h.greeted, hostID)
}

// Agent returns hostID's client, after its first successful Hello in this
// process. A host without a client is not_found.
func (h *Hosts) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[hostID]
	if !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	if !h.greeted[hostID] {
		if err := h.hello(ctx, hostID, c); err != nil {
			return nil, err
		}
	}
	return c, nil
}

// Hello calls hostID's Hello now and records every module's availability.
// The server calls it for the local host at startup, which seeds the host's
// module rows.
func (h *Hosts) Hello(ctx context.Context, hostID string) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, ok := h.clients[hostID]
	if !ok {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	return h.hello(ctx, hostID, c)
}

// hello runs under mu. It writes available, reason and checked_at of every
// module the registry has; a missing row takes enabled from the module's
// DefaultEnabled, and an existing row keeps its enabled, which only
// core.module.enable and core.module.disable change.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient) error {
	hello, err := c.Hello(ctx)
	if err != nil {
		return fmt.Errorf("dispatch: hello %s: %w", hostID, err)
	}
	if hello.HostID != hostID {
		return fmt.Errorf("dispatch: host %s answered Hello as %s", hostID, hello.HostID)
	}
	now := h.cfg.Clock.Now().UTC().Format(timeFormat)
	err = h.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		for _, st := range hello.Modules {
			m, ok := h.cfg.Registry.Module(st.ID)
			if !ok || m.ID == core.ModuleIDCore {
				continue
			}
			var reason any
			if !st.Available {
				reason = st.Reason
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
				 VALUES (?, ?, ?, ?, ?, ?)
				 ON CONFLICT (host_id, module_id) DO UPDATE SET available = excluded.available,
				 reason = excluded.reason, checked_at = excluded.checked_at`,
				hostID, st.ID, m.DefaultEnabled, st.Available, reason, now); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("dispatch: record the modules of %s: %w", hostID, err)
	}
	h.greeted[hostID] = true
	h.cfg.Log.Info("dispatch: host said hello", "host", hostID, "protocol", hello.ProtocolVersion,
		"version", hello.BinaryVersion, "os", hello.OS)
	return nil
}

// usable answers module.unavailable unless moduleID is enabled and available
// on hostID; the detail carries the reason: "disabled", the row's reason, or
// coreactions.ReasonNotReported when the host has no row for it.
func (h *Hosts) usable(ctx context.Context, hostID, moduleID string) error {
	var enabled, available bool
	var reason sql.NullString
	err := h.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT enabled, available, reason FROM core_module_state WHERE host_id = ? AND module_id = ?",
		hostID, moduleID).Scan(&enabled, &available, &reason)
	why := ""
	switch {
	case errors.Is(err, sql.ErrNoRows):
		why = coreactions.ReasonNotReported
	case err != nil:
		return fmt.Errorf("dispatch: read module state: %w", err)
	case !enabled:
		why = ReasonDisabled
	case !available:
		why = reason.String
		if why == "" {
			why = "unavailable"
		}
	default:
		return nil
	}
	return errcode.New(errcode.ModuleUnavailable,
		fmt.Sprintf("module %s is unavailable on host %s: %s", moduleID, hostID, why))
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestFirstReach|TestUsable|TestHello|TestAgent' ./internal/controller/dispatch/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/dispatch`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/dispatch/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/hosts.go internal/controller/dispatch/fixture_test.go internal/controller/dispatch/hosts_test.go
git commit -m "feat(dispatch): track hosts and module state"
```


---

### Task 24: change list, show, confirm and revert

**Files:**
- Create: `internal/cli/change.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/change_test.go`

**Interfaces:**
- Consumes: Contracts › Task 22 (`changeWire`, `printChange`), Task 20 (`app.connect`, `addConnFlags`, `resolveHost`), Task 18 (`render`, `writeTable`, `cellText`), Task 8 (`CommandPath`), Task 1 (`usagef`, `sanitize`); `core.StateConfirmed`, `core.StateReverted`, `core.StateInterrupted`, `core.ClassCompensable` (merged).
- Produces: Contracts › Task 24 (`changeColumns`, `changeCommand`, `changeListCommand`, `printChanges`, `changeShowCommand`, `changeOpCommand`).

**Depends on:** Task 22

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

The hand-written `change` commands of spec section 15 over `/api/v1/changes...`: `list` (filters `--state`, `--host` by id or name, `--before`, `--limit`; a table of the main columns, `--json` for all), `show <id>` (every field, and for an interrupted compensable change the compensating action and its command), `confirm <id>` and `revert <id>` (exit 1 unless the change ends confirmed or reverted). A missing or extra id is a usage error.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/change_test.go`:

```go
package cli

import (
	"strings"
	"testing"
)

func TestChangeList(t *testing.T) {
	pending := changeJSON("c1", "demo.rule.set", "revertible", "pending",
		`"revertDeadline":"2026-10-18T12:02:00Z","output":{"name":"web"},"error":null,"compensatedBy":null`)
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes?limit=5&state=pending", reply: `{"changes":[` + pending + `]}`},
		hostList("h1:web"),
		exchange{method: "GET", path: "/api/v1/changes?before=c9&hostId=h1", reply: `{"changes":[]}`},
		exchange{method: "GET", path: "/api/v1/changes", reply: `{"changes":[` + pending + `]}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "list", "--state", "pending", "--limit", "5", "--url", base)
	want := "id  hostId  action         version  class       state    actor     updatedAt             revertDeadline\n" +
		"c1  h1      demo.rule.set  1        revertible  pending  token:t1  2026-10-18T12:00:01Z  2026-10-18T12:02:00Z\n"
	if code != 0 || out != want {
		t.Errorf("list: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	out, errOut, code = cli.run(t, "change", "list", "--host", "web", "--before", "c9", "--url", base)
	if code != 0 || out != "(none)\n" {
		t.Errorf("empty list: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "change", "list", "--json", "--url", base)
	if code != 0 || out != `{"changes":[`+pending+"]}\n" {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
}

func TestChangeShow(t *testing.T) {
	interrupted := changeJSON("c2", "demo.item.add", "compensable", "interrupted",
		`"revertDeadline":null,"output":null,"error":{"code":"internal","message":"Internal error"},"compensatedBy":"demo.item.remove"`)
	base := newFakeAPI(t, testToken,
		exchange{method: "GET", path: "/api/v1/changes/c2", reply: interrupted},
		exchange{method: "GET", path: "/api/v1/changes/nope", status: 404, problem: true,
			reply: `{"type":"about:blank","title":"Change not found","status":404,"detail":"no change nope","code":"change.not_found"}`},
	)
	cli := cliEnv{env: tokenEnv}
	out, errOut, code := cli.run(t, "change", "show", "c2", "--url", base)
	for _, line := range []string{"id: c2\n", "state: interrupted\n", "error.code: internal\n", "compensatedBy: demo.item.remove\n",
		"\nThis change was interrupted and may be partly applied; its compensating action is demo.item.remove:\n" +
			"  darkpanel demo item remove\n"} {
		if !strings.Contains(out, line) {
			t.Errorf("show: out %q lacks %q (exit %d, err %q)", out, line, code, errOut)
		}
	}
	if code != 0 {
		t.Errorf("show: exit %d", code)
	}
	if _, errOut, code := cli.run(t, "change", "show", "nope", "--url", base); code != ExitError ||
		errOut != "darkpanel: no change nope (change.not_found)\n" {
		t.Errorf("not found: exit %d, err %q", code, errOut)
	}
}

func TestChangeConfirmAndRevert(t *testing.T) {
	base := newFakeAPI(t, testToken,
		exchange{method: "POST", path: "/api/v1/changes/c1/confirm", reply: changeJSON("c1", "demo.rule.set", "revertible", "confirmed", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c2/revert", reply: changeJSON("c2", "demo.rule.set", "revertible", "reverted", noExtra)},
		exchange{method: "POST", path: "/api/v1/changes/c3/revert", reply: changeJSON("c3", "demo.rule.set", "revertible", "failed",
			`"revertDeadline":null,"output":null,"error":{"code":"change.revert_failed","message":"Revert failed"},"compensatedBy":null`)},
	)
	cli := cliEnv{env: tokenEnv}
	if out, errOut, code := cli.run(t, "change", "confirm", "c1", "--url", base); code != 0 || out != "Change c1: confirmed\n" {
		t.Errorf("confirm: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "revert", "c2", "--url", base); code != 0 || out != "Change c2: reverted\n" {
		t.Errorf("revert: exit %d, out %q, err %q", code, out, errOut)
	}
	if out, errOut, code := cli.run(t, "change", "revert", "c3", "--url", base); code != ExitError || out != "Change c3: failed\n" ||
		errOut != "darkpanel: change c3 is failed, not reverted: Revert failed (change.revert_failed)\n" {
		t.Errorf("failed revert: exit %d, out %q, err %q", code, out, errOut)
	}
	for _, args := range [][]string{{"change", "confirm"}, {"change", "revert", "a", "b"}, {"change", "show"}} {
		if _, errOut, code := cli.run(t, args...); code != ExitUsage {
			t.Errorf("%q: exit %d, err %q", args, code, errOut)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestChangeList|TestChangeShow|TestChangeConfirmAndRevert)$' ./internal/cli/`
Expected: FAIL — the tests fail: `darkpanel: unknown command "change" for "darkpanel"`, exit 2, and `the fake API served 0 of 4 expected requests`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/change.go`:

```go
package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// changeColumns are the Change fields `change list` shows; --json has all.
var changeColumns = []string{"id", "hostId", "action", "version", "class", "state", "actor", "updatedAt", "revertDeadline"}

// changeCommand is the hand-written `change` group (spec section 15):
// list, show, confirm and revert over the network API's change routes.
func (a *app) changeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "change",
		Short: "List, show, confirm and revert changes",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	cmd.AddCommand(a.changeListCommand(), a.changeShowCommand(),
		a.changeOpCommand("confirm", core.StateConfirmed, "Confirm a pending revertible change before its revert deadline"),
		a.changeOpCommand("revert", core.StateReverted, "Revert a pending revertible change now"))
	return cmd
}

func (a *app) changeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List changes, newest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			q := url.Values{}
			for _, name := range []string{"state", "before"} {
				if v, _ := cmd.Flags().GetString(name); v != "" {
					q.Set(name, v)
				}
			}
			if limit, _ := cmd.Flags().GetInt("limit"); limit != 0 {
				q.Set("limit", strconv.Itoa(limit))
			}
			if want, _ := cmd.Flags().GetString("host"); want != "" {
				host, err := resolveHost(cmd.Context(), c, want)
				if err != nil {
					return err
				}
				q.Set("hostId", host)
			}
			path := "/api/v1/changes"
			if len(q) > 0 {
				path += "?" + q.Encode()
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, path, nil, &raw); err != nil {
				return err
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
				return err
			}
			return printChanges(cmd, raw)
		},
	}
	cmd.Flags().String("state", "", "only changes in this state: applying, pending, confirmed, reverted, completed, failed or interrupted")
	cmd.Flags().String("host", "", "only changes on this host, by id or name")
	cmd.Flags().String("before", "", "only changes older than this change id")
	cmd.Flags().Int("limit", 0, "most changes to list, 1 to 500")
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// printChanges writes the changeColumns of each change as a table.
func printChanges(cmd *cobra.Command, raw json.RawMessage) error {
	var resp struct {
		Changes []map[string]json.RawMessage `json:"changes"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode the changes: %w", err)
	}
	if len(resp.Changes) == 0 {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "(none)")
		return err
	}
	rows := make([][]string, 0, len(resp.Changes))
	for _, ch := range resp.Changes {
		row := make([]string, len(changeColumns))
		for i, col := range changeColumns {
			row[i] = cellText(ch[col])
		}
		rows = append(rows, row)
	}
	return writeTable(cmd.OutOrStdout(), changeColumns, rows)
}

func (a *app) changeShowCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "show <id>",
		Short: "Show one change",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/changes/"+url.PathEscape(args[0]), nil, &raw); err != nil {
				return err
			}
			w := cmd.OutOrStdout()
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err := fmt.Fprintf(w, "%s\n", raw)
				return err
			}
			if err := render(w, raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if ch.State == core.StateInterrupted && ch.Class == core.ClassCompensable && ch.CompensatedBy != nil {
				_, err = fmt.Fprintf(w, "\nThis change was interrupted and may be partly applied; its compensating action is %s:\n  darkpanel %s\n",
					sanitize(*ch.CompensatedBy), sanitize(strings.Join(CommandPath(*ch.CompensatedBy), " ")))
			}
			return err
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}

// changeOpCommand is `change confirm` or `change revert`; the change must
// end in state want.
func (a *app) changeOpCommand(op, want, short string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   op + " <id>",
		Short: short,
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := a.connect(cmd)
			if err != nil {
				return err
			}
			var raw json.RawMessage
			path := "/api/v1/changes/" + url.PathEscape(args[0]) + "/" + op
			if err := c.do(cmd.Context(), http.MethodPost, path, nil, &raw); err != nil {
				return err
			}
			var ch changeWire
			if err := json.Unmarshal(raw, &ch); err != nil {
				return fmt.Errorf("decode the change: %w", err)
			}
			if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
				_, err = fmt.Fprintf(cmd.OutOrStdout(), "%s\n", raw)
			} else {
				err = printChange(cmd.OutOrStdout(), ch, nil)
			}
			if err != nil {
				return err
			}
			if ch.State == want {
				return nil
			}
			if ch.Error != nil {
				return fmt.Errorf("change %s is %s, not %s: %s (%s)", ch.ID, ch.State, want, ch.Error.Message, ch.Error.Code)
			}
			return fmt.Errorf("change %s is %s, not %s", ch.ID, ch.State, want)
		},
	}
	cmd.Flags().Bool("json", false, "print the raw JSON answer")
	addConnFlags(cmd, false, false)
	return cmd
}
```

In `internal/cli/root.go`, edit 1 of 1. Replace this block:

```go
	}
	markRuns(root)
```

with:

```go
	}
	root.AddCommand(a.changeCommand())
	markRuns(root)
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestChangeList|TestChangeShow|TestChangeConfirmAndRevert)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/change.go internal/cli/root.go internal/cli/change_test.go
git commit -m "feat(cli): add the change commands"
```

### Task 25: Dispatcher reads, resources and plans

**Files:**
- Create: `internal/controller/dispatch/dispatch.go`
- Test: `internal/controller/dispatch/dispatch_test.go`

**Interfaces:**
- Consumes: Contracts › Task 23 (`Hosts`, `usable`, the harness) and Task 4 (`schema.Redact`, test only); `lifecycle.New`, `Engine.Read`, `Engine.Plan`, `core.Dispatcher`'s Read, Resource and Plan signatures, `coretest.MemAudit`
- Produces: Contracts › `internal/controller/dispatch` Task 25 (`Config`, `Dispatcher`, `New`, `Ingester`, `PlanIndexRetention`, the plan index)

**Items:** 5, 69

**Depends on:** Task 23

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The routing half of `core.Dispatcher`: controller-side actions to the controller engine, agent actions and resources to the host's client once the module is usable, and an index of every plan's host and action that Apply needs for register row 69's obligation 1. The tests cover the config, routing by side, every refusal code (unknown action, wrong side, unknown host, each unusable module), a redacted resource, the plan index and its retention, and a host that answers a plan for another action.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/dispatch_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var alice = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeAdmin, Source: "192.0.2.7"}

// config is the dispatcher's configuration over the harness, with a real
// controller engine journaling to journal.
func (h *harness) config(journal core.Journal) Config {
	h.t.Helper()
	log := slog.New(slog.DiscardHandler)
	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: h.reg, Validator: schema.NewValidator(), Journal: journal,
		Redact: schema.Redact,
		Base:   core.Env{DB: h.db, Sealer: coretest.Sealer{}, Clock: h.clock, Log: log, Agents: h.hosts},
	})
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(eng.Close)
	return Config{Registry: h.reg, Engine: eng, Hosts: h.hosts, DB: h.db, Clock: h.clock, Log: log}
}

func newDispatcher(t *testing.T) (*harness, *Dispatcher) {
	t.Helper()
	h := newHarness(t)
	d, err := New(h.config(&coretest.MemAudit{}))
	if err != nil {
		t.Fatal(err)
	}
	return h, d
}

func TestNewRequiresItsConfig(t *testing.T) {
	h := newHarness(t)
	breaks := map[string]func(*Config){
		"Registry": func(c *Config) { c.Registry = nil },
		"Engine":   func(c *Config) { c.Engine = nil },
		"Hosts":    func(c *Config) { c.Hosts = nil },
		"DB":       func(c *Config) { c.DB = nil },
		"Clock":    func(c *Config) { c.Clock = nil },
		"Log":      func(c *Config) { c.Log = nil },
	}
	for name, brk := range breaks {
		c := h.config(&coretest.MemAudit{})
		brk(&c)
		if _, err := New(c); err == nil || !strings.Contains(err.Error(), "Config."+name) {
			t.Errorf("%s: New = %v, want an error naming Config.%s", name, err, name)
		}
	}
}

func TestReadRoutesBySide(t *testing.T) {
	_, d := newDispatcher(t)
	ctx := context.Background()
	in := json.RawMessage(`{"name":"a"}`)
	out, err := d.Read(ctx, alice, testHost, refGet, in)
	if err != nil || string(out) != `{"actor":"user:alice","value":""}` {
		t.Errorf("agent read = %s, %v", out, err)
	}
	if _, err := d.Read(ctx, alice, "", refCtlGet, in); err != nil {
		t.Errorf("controller read: %v", err)
	}
	cases := []struct {
		name, host string
		ref        core.ActionRef
		code       errcode.Code
	}{
		{"controller action with a host", testHost, refCtlGet, errcode.ActionUnknown},
		{"agent action without a host", "", refGet, errcode.ActionUnknown},
		{"unknown action", testHost, core.ActionRef{Name: "kv.item.nope", Version: 1}, errcode.ActionUnknown},
		{"unknown version", testHost, core.ActionRef{Name: refGet.Name, Version: 2}, errcode.ActionUnknown},
		{"unknown host", "h9", refGet, errcode.NotFound},
		{"failed migration", testHost, core.ActionRef{Name: "bad.item.get", Version: 1}, errcode.ModuleUnavailable},
		{"disabled", testHost, core.ActionRef{Name: "off.item.get", Version: 1}, errcode.ModuleUnavailable},
		{"failed preflight", testHost, core.ActionRef{Name: "flaky.item.get", Version: 1}, errcode.ModuleUnavailable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Read(ctx, alice, c.host, c.ref, in)
			_ = wantCode(t, err, c.code)
		})
	}
}

func TestResource(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	out, err := d.Resource(ctx, alice, testHost, "kv.status")
	if err != nil || string(out) != `{"count":0,"token":"[redacted]"}` {
		t.Errorf("kv.status = %s, %v", out, err)
	}
	_, err = d.Resource(ctx, alice, testHost, "kv.nope")
	_ = wantCode(t, err, errcode.NotFound)
	_, err = d.Resource(ctx, alice, "h9", "kv.status")
	_ = wantCode(t, err, errcode.NotFound)
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err = d.Resource(ctx, alice, testHost, "kv.status")
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module kv is unavailable on host h1: disabled" {
		t.Errorf("detail = %q", e.Detail)
	}
}

func TestPlanRemembersWhereAndWhat(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentPlan, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err != nil {
		t.Fatal(err)
	}
	ctlPlan, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"x"}`))
	if err != nil {
		t.Fatal(err)
	}
	if agentPlan.HostID != testHost || ctlPlan.HostID != "" {
		t.Errorf("plan hosts %q and %q, want h1 and none", agentPlan.HostID, ctlPlan.HostID)
	}
	want := map[string]planEntry{
		agentPlan.PlanID: {hostID: testHost, ref: refSet, expiresAt: agentPlan.ExpiresAt},
		ctlPlan.PlanID:   {hostID: "", ref: refCtlSet, expiresAt: ctlPlan.ExpiresAt},
	}
	for id, w := range want {
		if got := d.plans[id]; got != w {
			t.Errorf("index of %s = %+v, want %+v", id, got, w)
		}
	}
	// An entry outlives its plan by PlanIndexRetention, then the next plan drops it.
	h.clock.Advance(lifecycle.PlanTTL + PlanIndexRetention - time.Second)
	if _, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"y"}`)); err != nil {
		t.Fatal(err)
	}
	if len(d.plans) != 3 {
		t.Errorf("index holds %d plans, want 3 inside the retention", len(d.plans))
	}
	h.clock.Advance(time.Second)
	if _, err := d.Plan(ctx, alice, "", refCtlSet, json.RawMessage(`{"name":"n","value":"z"}`)); err != nil {
		t.Fatal(err)
	}
	if _, ok := d.plans[agentPlan.PlanID]; ok || len(d.plans) != 2 {
		t.Errorf("index = %+v, want the two old plans dropped", d.plans)
	}
}

func TestPlanRefusesAnUnusableModule(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	if _, err := h.hosts.Agent(ctx, testHost); err != nil {
		t.Fatal(err)
	}
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET available = 0, reason = 'kv: broken' WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Plan(ctx, alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	e := wantCode(t, err, errcode.ModuleUnavailable)
	if e.Detail != "module kv is unavailable on host h1: kv: broken" || len(d.plans) != 0 {
		t.Errorf("detail %q, index %+v", e.Detail, d.plans)
	}
}

func TestPlanChecksTheAgentsAnswer(t *testing.T) {
	h, d := newDispatcher(t)
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		PlanFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (core.PlanView, error) {
			return core.PlanView{PlanID: "p1", HostID: testHost, Action: "kv.item.get", Version: 1}, nil
		},
	})
	_, err := d.Plan(context.Background(), alice, testHost, refSet, json.RawMessage(`{"name":"a","value":"1"}`))
	if err == nil || !strings.Contains(err.Error(), "answered with a plan for kv.item.get/v1 on h1") || len(d.plans) != 0 {
		t.Errorf("Plan = %v (index %+v), want the mismatch refused", err, d.plans)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestNewRequires|TestRead|TestResource|TestPlan' ./internal/controller/dispatch/`

Expected: FAIL — the build reports `undefined: Config`, `undefined: Dispatcher` and `undefined: New`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/dispatch [github.com/darkraise/darkpanel/internal/controller/dispatch.test]
internal/controller/dispatch/dispatch_test.go:22:48: undefined: Config
internal/controller/dispatch/dispatch_test.go:34:9: undefined: Config
internal/controller/dispatch/dispatch_test.go:37:46: undefined: Dispatcher
internal/controller/dispatch/dispatch_test.go:40:12: undefined: New
internal/controller/dispatch/dispatch_test.go:49:29: undefined: Config
internal/controller/dispatch/dispatch_test.go:50:23: undefined: Config
internal/controller/dispatch/dispatch_test.go:51:23: undefined: Config
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/dispatch/dispatch.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// PlanIndexRetention is how long the dispatcher remembers a plan after it
// expired, so a late apply answers plan.expired and a repeated apply returns
// its change. After that, and after a restart, the plan is plan.not_found.
const PlanIndexRetention = time.Hour

// Ingester copies a host's agent journal into the audit log;
// *audit.Ingester implements it.
type Ingester interface {
	IngestHost(ctx context.Context, hostID string) error
}

// Config wires a Dispatcher.
type Config struct {
	Registry *core.Registry    // the one registry of the process
	Engine   *lifecycle.Engine // the controller's engine
	Hosts    *Hosts            // the controller engine's Base.Agents too
	DB       *store.DB         // the controller database: core_hosts, core_change_index
	Clock    core.Clock
	Log      *slog.Logger
	// Ingest, when set, runs after every agent apply, confirm and revert.
	Ingest Ingester
}

// Dispatcher is the core.Dispatcher of the controller. Its methods are safe
// for concurrent use.
type Dispatcher struct {
	cfg Config

	mu    sync.Mutex
	plans map[string]planEntry // by plan id
}

// planEntry is where a plan was made and for which action, so Apply can
// refuse a plan id used with another host or action (register row 69,
// obligation 1).
type planEntry struct {
	hostID    string
	ref       core.ActionRef
	expiresAt time.Time
}

// New returns a dispatcher for cfg; every field but Ingest is required.
func New(cfg Config) (*Dispatcher, error) {
	required := []struct {
		name string
		set  bool
	}{
		{"Registry", cfg.Registry != nil}, {"Engine", cfg.Engine != nil}, {"Hosts", cfg.Hosts != nil},
		{"DB", cfg.DB != nil}, {"Clock", cfg.Clock != nil}, {"Log", cfg.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return nil, fmt.Errorf("dispatch: Config.%s is required", r.name)
		}
	}
	return &Dispatcher{cfg: cfg, plans: map[string]planEntry{}}, nil
}

// moduleOf is the module id of an action or resource name.
func moduleOf(name string) string {
	id, _, _ := strings.Cut(name, ".")
	return id
}

// action returns the registered action ref, which must be controller-side
// when hostID is empty and agent-side when it is not.
func (d *Dispatcher) action(ref core.ActionRef, hostID string) (core.ActionDef, error) {
	def, ok := d.cfg.Registry.Action(ref)
	switch {
	case !ok:
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", ref))
	case def.Spec.Side == core.SideController && hostID != "":
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is a controller-side action and takes no host", ref))
	case def.Spec.Side == core.SideAgent && hostID == "":
		return def, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is an agent action and needs a host", ref))
	}
	return def, nil
}

// agent returns hostID's client once the module of name is usable there.
func (d *Dispatcher) agent(ctx context.Context, hostID, name string) (core.AgentClient, error) {
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if err := d.cfg.Hosts.usable(ctx, hostID, moduleOf(name)); err != nil {
		return nil, err
	}
	return c, nil
}

// Read runs a read action: a controller-side one on the controller engine
// (hostID empty), an agent one on hostID's agent once its module is usable.
func (d *Dispatcher) Read(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return nil, err
	}
	if def.Spec.Side == core.SideController {
		return d.cfg.Engine.Read(ctx, p, a, input)
	}
	c, err := d.agent(ctx, hostID, a.Name)
	if err != nil {
		return nil, err
	}
	return c.Read(ctx, p, a, input)
}

// Resource reads a resource on hostID's agent once its module is usable.
func (d *Dispatcher) Resource(ctx context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
	if _, ok := d.cfg.Registry.Resource(name); !ok {
		return nil, errcode.New(errcode.NotFound, fmt.Sprintf("no resource %s", name))
	}
	c, err := d.agent(ctx, hostID, name)
	if err != nil {
		return nil, err
	}
	return c.Resource(ctx, p, name)
}

// Plan plans a mutating action where Read would run it, and remembers the
// plan's host and action for Apply.
func (d *Dispatcher) Plan(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return core.PlanView{}, err
	}
	var v core.PlanView
	if def.Spec.Side == core.SideController {
		v, err = d.cfg.Engine.Plan(ctx, p, a, input)
	} else {
		var c core.AgentClient
		if c, err = d.agent(ctx, hostID, a.Name); err == nil {
			v, err = c.Plan(ctx, p, a, input)
		}
	}
	if err != nil {
		return core.PlanView{}, err
	}
	if v.HostID != hostID || v.Action != a.Name || v.Version != a.Version {
		return core.PlanView{}, fmt.Errorf("dispatch: %s on %q answered with a plan for %s/v%d on %s",
			a, hostID, v.Action, v.Version, v.HostID)
	}
	d.remember(v.PlanID, planEntry{hostID: hostID, ref: a, expiresAt: v.ExpiresAt})
	return v, nil
}

// remember indexes a plan and drops entries past their retention.
func (d *Dispatcher) remember(planID string, e planEntry) {
	now := d.cfg.Clock.Now()
	d.mu.Lock()
	defer d.mu.Unlock()
	for id, old := range d.plans {
		if !now.Before(old.expiresAt.Add(PlanIndexRetention)) {
			delete(d.plans, id)
		}
	}
	d.plans[planID] = e
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestNewRequires|TestRead|TestResource|TestPlan' ./internal/controller/dispatch/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/dispatch`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/dispatch/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/dispatch.go internal/controller/dispatch/dispatch_test.go
git commit -m "feat(dispatch): route reads, resources and plans"
```


---

### Task 26: Apply, change index and ingestion trigger

**Files:**
- Create: `internal/controller/dispatch/apply.go`
- Test: `internal/controller/dispatch/apply_test.go`

**Interfaces:**
- Consumes: Contracts › Task 25 (`Dispatcher`, `action`, `agent`, `plans`, `newDispatcher`, `alice`, `(*harness).config`) and Task 13 (`audit.New`, `audit.NewIngester`, `IngesterConfig`); `core_change_index` (Task 12)
- Produces: Contracts › `internal/controller/dispatch` Task 26 (`Apply`, `planFor`, `index`, `ingest`, `newAudited`, `(*harness).plan`)

**Items:** 69, 14

**Depends on:** Task 13, Task 25

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Register row 69, obligation 1: the API authorizes from the route's action, so Apply must run only a plan made for that same host and action; anything else is `plan.not_found`. An agent apply is recorded in `core_change_index` and its journal is ingested right away. The tests try every mismatch and a plan from before a restart, then check the index row, the ingested audit entry with the caller's source, an acknowledged agent journal, the controller-side audit row, a module disabled after planning, and an expired plan.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/apply_test.go`:

```go
package dispatch

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// newAudited returns a dispatcher whose controller engine journals to a real
// audit store and which ingests the agent journal after each change.
func newAudited(t *testing.T) (*harness, *Dispatcher, *audit.Store) {
	t.Helper()
	h := newHarness(t)
	store := audit.New(h.db, h.clock)
	cfg := h.config(store)
	cfg.Ingest = audit.NewIngester(audit.IngesterConfig{Store: store, Agents: h.hosts, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	d, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return h, d, store
}

func (h *harness) plan(d *Dispatcher, hostID string, ref core.ActionRef, input string) core.PlanView {
	h.t.Helper()
	v, err := d.Plan(context.Background(), alice, hostID, ref, json.RawMessage(input))
	if err != nil {
		h.t.Fatalf("Plan %s: %v", ref, err)
	}
	return v
}

func TestApplyRefusesAPlanOfAnotherHostOrAction(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentPlan := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	ctlPlan := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	cases := []struct {
		name, host string
		ref        core.ActionRef
		planID     string
	}{
		{"agent plan as a controller action", "", refCtlSet, agentPlan.PlanID},
		{"agent plan as another action", testHost, refGet, agentPlan.PlanID},
		{"agent plan on another host", "h2", refSet, agentPlan.PlanID},
		{"controller plan as an agent action", testHost, refSet, ctlPlan.PlanID},
		{"unknown plan", testHost, refSet, "01UNKNOWN"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := d.Apply(ctx, alice, c.host, c.ref, c.planID, core.ApplyOptions{})
			_ = wantCode(t, err, errcode.PlanNotFound)
		})
	}
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 0 {
		t.Errorf("index rows after refusals = %d, want 0", n)
	}
	// A dispatcher started later does not know the plan: the operator plans again.
	restarted, err := New(d.cfg)
	if err != nil {
		t.Fatal(err)
	}
	_, err = restarted.Apply(ctx, alice, testHost, refSet, agentPlan.PlanID, core.ApplyOptions{})
	e := wantCode(t, err, errcode.PlanNotFound)
	if e.Detail != "no plan "+agentPlan.PlanID+" for kv.item.set/v1 on host h1" {
		t.Errorf("detail = %q", e.Detail)
	}
	for _, c := range []struct {
		host string
		ref  core.ActionRef
		id   string
	}{{testHost, refSet, agentPlan.PlanID}, {"", refCtlSet, ctlPlan.PlanID}} {
		if _, err := d.Apply(ctx, alice, c.host, c.ref, c.id, core.ApplyOptions{}); err != nil {
			t.Errorf("Apply of %s: %v", c.ref, err)
		}
	}
}

func TestApplyRecordsTheChangeAndIngests(t *testing.T) {
	h, d, store := newAudited(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	v, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	if err != nil || v.State != core.StatePending || v.HostID != testHost {
		t.Fatalf("Apply = %+v, %v; want pending on h1", v, err)
	}
	var host, action, created string
	var version int
	if err := h.db.Read().QueryRow("SELECT host_id, action, version, created_at FROM core_change_index WHERE change_id = ?", v.ID).
		Scan(&host, &action, &version, &created); err != nil {
		t.Fatalf("index row: %v", err)
	}
	if host != testHost || action != refSet.Name || version != 1 || created != "2026-10-17T12:00:00.000000000Z" {
		t.Errorf("index row = %s %s %d %s", host, action, version, created)
	}
	entries, _, err := store.List(ctx, core.AuditQuery{HostID: testHost})
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Phase != "apply" || entries[0].ChangeID != v.ID ||
		entries[0].Actor != alice.Actor || entries[0].Source != alice.Source {
		t.Errorf("audit = %+v, want the apply of the change with alice's source", entries)
	}
	if left, err := h.rt.Journal(ctx, 0, 10); err != nil || len(left) != 0 {
		t.Errorf("agent journal after ingestion = %+v, %v; want acknowledged", left, err)
	}
	again, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	if err != nil || again.ID != v.ID {
		t.Errorf("repeated apply = %+v, %v; want the same change", again, err)
	}
	if n := h.count("SELECT count(*) FROM core_change_index"); n != 1 {
		t.Errorf("index rows = %d, want 1", n)
	}

	ctl := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	cv, err := d.Apply(ctx, alice, "", refCtlSet, ctl.PlanID, core.ApplyOptions{})
	if err != nil || cv.State != core.StateCompleted || cv.HostID != "" {
		t.Fatalf("controller apply = %+v, %v", cv, err)
	}
	if n := h.count("SELECT count(*) FROM core_change_index WHERE change_id = ?", cv.ID); n != 0 {
		t.Error("a controller-side change was indexed")
	}
	entries, _, err = store.List(ctx, core.AuditQuery{Action: refCtlSet.Name})
	if err != nil || len(entries) != 1 || entries[0].HostID != "" || entries[0].Source != alice.Source {
		t.Errorf("controller audit = %+v, %v; want one row with alice's source", entries, err)
	}
}

func TestApplyRefusesAModuleDisabledAfterThePlan(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	pl := h.plan(d, testHost, refSet, `{"name":"a","value":"1"}`)
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 0 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.ModuleUnavailable)
	if _, err := h.db.Writer().Exec("UPDATE core_module_state SET enabled = 1 WHERE module_id = 'kv'"); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Apply(ctx, alice, testHost, refSet, pl.PlanID, core.ApplyOptions{}); err != nil {
		t.Errorf("Apply once enabled again: %v", err)
	}
}

func TestApplyOfAnExpiredPlanIsExpired(t *testing.T) {
	h, d := newDispatcher(t)
	pl := h.plan(d, "", refCtlSet, `{"name":"n","value":"x"}`)
	h.clock.Advance(lifecycle.PlanTTL)
	_, err := d.Apply(context.Background(), alice, "", refCtlSet, pl.PlanID, core.ApplyOptions{})
	_ = wantCode(t, err, errcode.PlanExpired)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestApply' ./internal/controller/dispatch/`

Expected: FAIL — the build reports `d.Apply undefined (type *Dispatcher has no field or method Apply)`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/dispatch [github.com/darkraise/darkpanel/internal/controller/dispatch.test]
internal/controller/dispatch/apply_test.go:57:16: d.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:69:21: restarted.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:79:18: d.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:89:14: d.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:113:18: d.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:122:15: d.Apply undefined (type *Dispatcher has no field or method Apply)
internal/controller/dispatch/apply_test.go:142:14: d.Apply undefined (type *Dispatcher has no field or method Apply)
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/dispatch/apply.go`:

```go
package dispatch

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Apply applies a plan this dispatcher made for exactly hostID and a. A plan
// id it does not know, one made for another host, action or version, and one
// made before a restart are plan.not_found (register row 69, obligation 1):
// the caller authorized a, so a plan of another action must not run. An agent
// apply also needs a's module to be usable on hostID; its change is recorded
// in core_change_index and the host's journal is ingested.
func (d *Dispatcher) Apply(ctx context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	def, err := d.action(a, hostID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if err := d.planFor(planID, hostID, a); err != nil {
		return core.ChangeView{}, err
	}
	if def.Spec.Side == core.SideController {
		return d.cfg.Engine.Apply(ctx, p, planID, opts)
	}
	c, err := d.agent(ctx, hostID, a.Name)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err := c.Apply(ctx, p, planID, opts)
	if v.ID != "" {
		d.index(ctx, hostID, v)
		d.ingest(ctx, hostID)
	}
	return v, err
}

// planFor refuses planID unless Plan indexed it for hostID and a.
func (d *Dispatcher) planFor(planID, hostID string, a core.ActionRef) error {
	d.mu.Lock()
	e, ok := d.plans[planID]
	d.mu.Unlock()
	if ok && e.hostID == hostID && e.ref == a {
		return nil
	}
	detail := fmt.Sprintf("no plan %s for %s", planID, a)
	if hostID != "" {
		detail += " on host " + hostID
	}
	return errcode.New(errcode.PlanNotFound, detail)
}

// index records the host of an agent change, so Change, Confirm and Revert
// find it. A failure is logged, not returned: the change exists and its
// apply response may carry a secret output shown only once.
func (d *Dispatcher) index(ctx context.Context, hostID string, v core.ChangeView) {
	ctx = context.WithoutCancel(ctx)
	err := d.cfg.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_change_index (change_id, host_id, action, version, created_at)
			 VALUES (?, ?, ?, ?, ?) ON CONFLICT (change_id) DO NOTHING`,
			v.ID, hostID, v.Action, v.Version, v.CreatedAt.UTC().Format(timeFormat))
		return err
	})
	if err != nil {
		d.cfg.Log.Error("dispatch: index change", "change", v.ID, "host", hostID, "error", err)
	}
}

// ingest copies hostID's journal into the audit log now; the ingester's loop
// retries what fails here.
func (d *Dispatcher) ingest(ctx context.Context, hostID string) {
	if d.cfg.Ingest == nil {
		return
	}
	if err := d.cfg.Ingest.IngestHost(context.WithoutCancel(ctx), hostID); err != nil {
		d.cfg.Log.Warn("dispatch: journal ingestion", "host", hostID, "error", err)
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestApply' ./internal/controller/dispatch/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/dispatch`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/dispatch/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/apply.go internal/controller/dispatch/apply_test.go
git commit -m "feat(dispatch): apply plans and index changes"
```


---

### Task 27: Change views, confirm, revert and the merged listing

**Files:**
- Create: `internal/controller/dispatch/changes.go`
- Test: `internal/controller/dispatch/changes_test.go`

**Interfaces:**
- Consumes: Contracts › Task 26 (`Apply`, `ingest`, `newAudited`, `(*harness).plan`) and Task 25; `Engine.Change`, `Confirm`, `Revert`, `Changes`; `core_change_index`
- Produces: Contracts › `internal/controller/dispatch` Task 27 (`Change`, `Confirm`, `Revert`, `Changes`, `DefaultChangeLimit`, `MaxChangeLimit`; `*Dispatcher` implements `core.Dispatcher`)

**Items:** 69

**Depends on:** Task 26

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Register row 69, obligation 2: the API authorizes confirm and revert from the view `Change` returns, so that view must be the real change (action, version, host) and confirm and revert must act on it. `Changes` merges the controller's and every host's changes and pages exactly like the engine (controller ruling 2026-10-18), because the API refills pages by calling it again with the last id. The tests cover both kinds of change, a lost index row, a host answering for another, confirm and revert with their ingested audit rows, interleaved sources with a cursor in the middle and a limit below the total, the filter passed to every source, and a host that cannot be listed.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/changes_test.go`:

```go
package dispatch

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var bob = core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "192.0.2.8"}

// applyAgent plans and applies kv.item.set on h1.
func (h *harness) applyAgent(d *Dispatcher, name string, opts core.ApplyOptions) core.ChangeView {
	h.t.Helper()
	pl := h.plan(d, testHost, refSet, `{"name":"`+name+`","value":"1"}`)
	v, err := d.Apply(context.Background(), alice, testHost, refSet, pl.PlanID, opts)
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

// applyCtl plans and applies ctl.note.set on the controller.
func (h *harness) applyCtl(d *Dispatcher, value string) core.ChangeView {
	h.t.Helper()
	pl := h.plan(d, "", refCtlSet, `{"name":"n","value":"`+value+`"}`)
	v, err := d.Apply(context.Background(), alice, "", refCtlSet, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		h.t.Fatal(err)
	}
	return v
}

func TestChangeReturnsTheRealChange(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	agentChange := h.applyAgent(d, "a", core.ApplyOptions{})
	ctlChange := h.applyCtl(d, "x")
	got, err := d.Change(ctx, alice, agentChange.ID)
	if err != nil || got.Action != refSet.Name || got.Version != 1 || got.HostID != testHost || got.State != core.StatePending {
		t.Errorf("agent change = %+v, %v", got, err)
	}
	got, err = d.Change(ctx, alice, ctlChange.ID)
	if err != nil || got.Action != refCtlSet.Name || got.HostID != "" || got.State != core.StateCompleted {
		t.Errorf("controller change = %+v, %v", got, err)
	}
	_, err = d.Change(ctx, alice, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
	// An index row whose host does not know the change.
	if _, err := h.db.Writer().Exec(`INSERT INTO core_change_index (change_id, host_id, action, version, created_at)
		VALUES ('01LOST', 'h1', 'kv.item.set', 1, '2026-10-17T12:00:00.000000000Z')`); err != nil {
		t.Fatal(err)
	}
	_, err = d.Change(ctx, alice, "01LOST")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func TestChangeRefusesAHostThatAnswersForAnother(t *testing.T) {
	h, d := newDispatcher(t)
	v := h.applyAgent(d, "a", core.ApplyOptions{})
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			return core.ChangeView{ID: id, HostID: "h2", Action: refSet.Name, Version: 1}, nil
		},
	})
	_, err := d.Change(context.Background(), alice, v.ID)
	var coded *errcode.Error
	if err == nil || errors.As(err, &coded) || !strings.Contains(err.Error(), "answered for host h2") {
		t.Errorf("Change = %v, want a plain error naming the mismatch", err)
	}
}

func TestConfirmAndRevertActOnTheOwnerAndIngest(t *testing.T) {
	h, d, store := newAudited(t)
	ctx := context.Background()
	first := h.applyAgent(d, "a", core.ApplyOptions{})
	v, err := d.Confirm(ctx, bob, first.ID)
	if err != nil || v.ID != first.ID || v.State != core.StateConfirmed {
		t.Fatalf("Confirm = %+v, %v", v, err)
	}
	second := h.applyAgent(d, "b", core.ApplyOptions{})
	if v, err = d.Revert(ctx, bob, second.ID); err != nil || v.ID != second.ID || v.State != core.StateReverted {
		t.Fatalf("Revert = %+v, %v", v, err)
	}
	entries, _, err := store.List(ctx, core.AuditQuery{Actor: bob.Actor})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range entries {
		got = append(got, e.Phase+"/"+e.ChangeID+"/"+e.HostID+"/"+e.Source)
	}
	want := []string{"revert/" + second.ID + "/h1/192.0.2.8", "confirm/" + first.ID + "/h1/192.0.2.8"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("audit of bob = %v, want %v", got, want)
	}
	_, err = d.Confirm(ctx, bob, first.ID)
	_ = wantCode(t, err, errcode.ChangeNotPending)
	ctl := h.applyCtl(d, "x")
	_, err = d.Revert(ctx, bob, ctl.ID)
	_ = wantCode(t, err, errcode.ChangeNotPending)
	_, err = d.Revert(ctx, bob, "01UNKNOWN")
	_ = wantCode(t, err, errcode.ChangeNotFound)
}

func ids(vs []core.ChangeView) []string {
	out := []string{}
	for _, v := range vs {
		out = append(out, v.ID)
	}
	return out
}

func TestChangesMergeAndPageLikeTheEngine(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	// c[0], c[2] and c[4] are agent changes, the others controller changes.
	var c []string
	for i := range 3 {
		c = append(c, h.applyAgent(d, string(rune('a'+i)), core.ApplyOptions{ConfirmNow: true}).ID)
		h.clock.Advance(time.Second)
		c = append(c, h.applyCtl(d, string(rune('a'+i))).ID)
		h.clock.Advance(time.Second)
	}
	list := func(f core.ChangeFilter) []string {
		t.Helper()
		vs, err := d.Changes(ctx, alice, f)
		if err != nil {
			t.Fatal(err)
		}
		return ids(vs)
	}
	cases := []struct {
		name string
		f    core.ChangeFilter
		want []string
	}{
		{"everything, newest first", core.ChangeFilter{}, []string{c[5], c[4], c[3], c[2], c[1], c[0]}},
		{"limit below the total", core.ChangeFilter{Limit: 4}, []string{c[5], c[4], c[3], c[2]}},
		{"cursor in the middle", core.ChangeFilter{Before: c[3], Limit: 2}, []string{c[2], c[1]}},
		{"last page is short", core.ChangeFilter{Before: c[2], Limit: 4}, []string{c[1], c[0]}},
		{"one host", core.ChangeFilter{HostID: testHost}, []string{c[4], c[2], c[0]}},
		{"state", core.ChangeFilter{State: core.StateCompleted}, []string{c[5], c[3], c[1]}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := list(tc.f); !reflect.DeepEqual(got, tc.want) {
				t.Errorf("Changes = %v, want %v", got, tc.want)
			}
		})
	}
	var paged []string
	for before := ""; ; {
		page := list(core.ChangeFilter{Before: before, Limit: 2})
		paged = append(paged, page...)
		if len(page) < 2 {
			break
		}
		before = page[len(page)-1]
	}
	if want := []string{c[5], c[4], c[3], c[2], c[1], c[0]}; !reflect.DeepEqual(paged, want) {
		t.Errorf("pages of 2 = %v, want %v", paged, want)
	}
}

func TestChangesPassTheLimitToEverySource(t *testing.T) {
	h, d := newDispatcher(t)
	var seen []core.ChangeFilter
	h.hosts.Add(testHost, &coretest.FakeAgent{
		HelloFunc: h.rt.Hello,
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			seen = append(seen, f)
			return []core.ChangeView{}, nil
		},
	})
	for _, limit := range []int{0, 7, 900} {
		if _, err := d.Changes(context.Background(), alice, core.ChangeFilter{Limit: limit, Before: "01B", State: "pending"}); err != nil {
			t.Fatal(err)
		}
	}
	want := []core.ChangeFilter{
		{State: "pending", HostID: testHost, Before: "01B", Limit: 100},
		{State: "pending", HostID: testHost, Before: "01B", Limit: 7},
		{State: "pending", HostID: testHost, Before: "01B", Limit: 500},
	}
	if !reflect.DeepEqual(seen, want) {
		t.Errorf("agent filters = %+v, want %+v", seen, want)
	}
}

func TestChangesFailWhenAHostCannotBeListed(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	h.applyAgent(d, "a", core.ApplyOptions{})
	if _, err := h.db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h9', 'gone', 0, '2026-10-17T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	_, err := d.Changes(ctx, alice, core.ChangeFilter{})
	_ = wantCode(t, err, errcode.NotFound)
	vs, err := d.Changes(ctx, alice, core.ChangeFilter{HostID: testHost})
	if err != nil || len(vs) != 1 {
		t.Errorf("Changes of h1 = %+v, %v; want its one change", vs, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestChange|TestConfirm' ./internal/controller/dispatch/`

Expected: FAIL — the build reports `d.Change undefined (type *Dispatcher has no field or method Change)`. The output begins:

```text
# github.com/darkraise/darkpanel/internal/controller/dispatch [github.com/darkraise/darkpanel/internal/controller/dispatch.test]
internal/controller/dispatch/changes_test.go:45:16: d.Change undefined (type *Dispatcher has no field or method Change)
internal/controller/dispatch/changes_test.go:49:15: d.Change undefined (type *Dispatcher has no field or method Change)
internal/controller/dispatch/changes_test.go:53:13: d.Change undefined (type *Dispatcher has no field or method Change)
internal/controller/dispatch/changes_test.go:60:13: d.Change undefined (type *Dispatcher has no field or method Change)
internal/controller/dispatch/changes_test.go:73:14: d.Change undefined (type *Dispatcher has no field or method Change)
internal/controller/dispatch/changes_test.go:84:14: d.Confirm undefined (type *Dispatcher has no field or method Confirm)
internal/controller/dispatch/changes_test.go:89:16: d.Revert undefined (type *Dispatcher has no field or method Revert)
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/dispatch/changes.go`:

```go
package dispatch

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Changes returns DefaultChangeLimit changes unless asked for fewer, and
// never more than MaxChangeLimit, as lifecycle.Engine.Changes does.
const (
	DefaultChangeLimit = 100
	MaxChangeLimit     = 500
)

var _ core.Dispatcher = (*Dispatcher)(nil)

func isChangeNotFound(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.ChangeNotFound
}

// owner returns the client of the host core_change_index names for an agent
// change; a change it does not hold is change.not_found.
func (d *Dispatcher) owner(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	var hostID string
	err := d.cfg.DB.Read().QueryRowContext(ctx,
		"SELECT host_id FROM core_change_index WHERE change_id = ?", changeID).Scan(&hostID)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return "", nil, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	case err != nil:
		return "", nil, fmt.Errorf("dispatch: read change index: %w", err)
	}
	c, err := d.cfg.Hosts.Agent(ctx, hostID)
	return hostID, c, err
}

// checkHost refuses a view whose host is not the one the index names, so the
// caller always authorizes and acts on the same change (register row 69,
// obligation 2).
func checkHost(v core.ChangeView, hostID string) (core.ChangeView, error) {
	if v.HostID != hostID {
		return core.ChangeView{}, fmt.Errorf("dispatch: change %s of host %s answered for host %s", v.ID, hostID, v.HostID)
	}
	return v, nil
}

// Change returns a change as its engine stores it, with its real action,
// version and host (empty for a controller-side change): the controller
// engine's first, else the agent's that core_change_index names.
func (d *Dispatcher) Change(ctx context.Context, _ core.Principal, changeID string) (core.ChangeView, error) {
	v, err := d.cfg.Engine.Change(ctx, changeID)
	if !isChangeNotFound(err) {
		return v, err
	}
	hostID, c, err := d.owner(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	if v, err = c.Change(ctx, changeID); err != nil {
		return v, err
	}
	return checkHost(v, hostID)
}

// Confirm confirms the change Change returns for changeID, then ingests its
// host's journal.
func (d *Dispatcher) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return d.settle(ctx, changeID,
		func() (core.ChangeView, error) { return d.cfg.Engine.Confirm(ctx, p, changeID) },
		func(c core.AgentClient) (core.ChangeView, error) { return c.Confirm(ctx, p, changeID) })
}

// Revert reverts the change Change returns for changeID, then ingests its
// host's journal.
func (d *Dispatcher) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return d.settle(ctx, changeID,
		func() (core.ChangeView, error) { return d.cfg.Engine.Revert(ctx, p, changeID) },
		func(c core.AgentClient) (core.ChangeView, error) { return c.Revert(ctx, p, changeID) })
}

// settle runs a confirm or revert on the controller engine, or, for a change
// it does not hold, on the owning agent. Neither refuses an unusable module:
// a pending change must stay confirmable and revertible.
func (d *Dispatcher) settle(ctx context.Context, changeID string, onController func() (core.ChangeView, error),
	onAgent func(core.AgentClient) (core.ChangeView, error)) (core.ChangeView, error) {
	v, err := onController()
	if !isChangeNotFound(err) {
		return v, err
	}
	hostID, c, err := d.owner(ctx, changeID)
	if err != nil {
		return core.ChangeView{}, err
	}
	v, err = onAgent(c)
	if err != nil {
		return v, err
	}
	d.ingest(ctx, hostID)
	return checkHost(v, hostID)
}

// Changes lists changes newest first and pages exactly like
// lifecycle.Engine.Changes: only changes with an id below f.Before, at most
// f.Limit (100 when 0 or less, 500 at most), and fewer than the limit only
// when nothing more matches. Without f.HostID it merges the controller
// engine's changes with those of every host in core_hosts; a host that cannot
// be listed fails the call, because a silently short page would end a
// caller's paging early. With f.HostID only that host's agent is listed.
func (d *Dispatcher) Changes(ctx context.Context, _ core.Principal, f core.ChangeFilter) ([]core.ChangeView, error) {
	if f.Limit <= 0 {
		f.Limit = DefaultChangeLimit
	}
	f.Limit = min(f.Limit, MaxChangeLimit)
	all := []core.ChangeView{}
	hosts := []string{f.HostID}
	if f.HostID == "" {
		vs, err := d.cfg.Engine.Changes(ctx, f)
		if err != nil {
			return nil, err
		}
		all = append(all, vs...)
		if hosts, err = d.hostIDs(ctx); err != nil {
			return nil, err
		}
	}
	for _, hostID := range hosts {
		c, err := d.cfg.Hosts.Agent(ctx, hostID)
		if err != nil {
			return nil, err
		}
		hf := f
		hf.HostID = hostID
		vs, err := c.Changes(ctx, hf)
		if err != nil {
			return nil, err
		}
		all = append(all, vs...)
	}
	// Every source returned its newest f.Limit matches, so the newest f.Limit
	// of their union are the newest f.Limit overall.
	sort.Slice(all, func(i, j int) bool { return all[i].ID > all[j].ID })
	if len(all) > f.Limit {
		all = all[:f.Limit]
	}
	return all, nil
}

func (d *Dispatcher) hostIDs(ctx context.Context) ([]string, error) {
	rows, err := d.cfg.DB.Read().QueryContext(ctx, "SELECT id FROM core_hosts ORDER BY id")
	if err != nil {
		return nil, fmt.Errorf("dispatch: list hosts: %w", err)
	}
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("dispatch: list hosts: %w", err)
		}
		out = append(out, id)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("dispatch: list hosts: %w", err)
	}
	return out, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestChange|TestConfirm' ./internal/controller/dispatch/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/dispatch`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/dispatch/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/changes.go internal/controller/dispatch/changes_test.go
git commit -m "feat(dispatch): find, settle and list changes"
```

### Task 28: controller web-ca

**Files:**
- Create: `internal/cli/controller.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/controller_test.go`

**Interfaces:**
- Consumes: Contracts › Task 24 (the `root.AddCommand(a.changeCommand())` line it edits), Task 20 (`app.connect`, `addConnFlags`), Task 16 (`newFakeSocket`, `jsonEqual`), Task 15 (`newTestCA`, `testCert.fingerprint`), Task 1 (`usagef`); API contract `GET /api/v1/controller/web-ca` → `{pem, fingerprint}` on the socket (merged `internal/controller/api/socket.go:192`, `:212-219`).
- Produces: Contracts › Task 28 (`controllerCommand`, `runWebCA`).

**Depends on:** Task 24

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Spec section 13: `darkpanel controller web-ca` prints the web root in PEM and its SHA-256 fingerprint through the controller socket, for the browser and for `caFingerprint`. The CLI decodes the PEM, recomputes the fingerprint and refuses an answer whose fingerprint does not match, then prints the re-encoded certificate, so no other server text reaches the terminal. The tests use a CA made in the test behind the fake socket: a correct answer, a hostile preamble with an upper-case fingerprint, a wrong fingerprint, no PEM, `--json`, and an unknown subcommand.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/controller_test.go`:

```go
package cli

import (
	"encoding/json"
	"encoding/pem"
	"strings"
	"testing"
)

func webCAReply(t *testing.T, pemText, fingerprint string) string {
	t.Helper()
	b, err := json.Marshal(map[string]string{"pem": pemText, "fingerprint": fingerprint})
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestControllerWebCA(t *testing.T) {
	ca := newTestCA(t)
	rootPEM := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: ca.cert.Raw}))
	get := func(reply string) exchange {
		return exchange{method: "GET", path: "/api/v1/controller/web-ca", reply: reply}
	}
	sock := newFakeSocket(t,
		get(webCAReply(t, rootPEM, ca.fingerprint())),
		get(webCAReply(t, "\x1b[2J\u202eheader\n"+rootPEM, strings.ToUpper(ca.fingerprint()))),
		get(webCAReply(t, rootPEM, newTestCA(t).fingerprint())),
		get(webCAReply(t, "not a certificate", ca.fingerprint())),
		get(webCAReply(t, rootPEM, ca.fingerprint())),
	)
	cli := cliEnv{}
	want := rootPEM + "SHA-256 fingerprint: " + ca.fingerprint() + "\n"
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != 0 || out != want {
		t.Errorf("web-ca: exit %d\nout %q\nwant %q\nerr %q", code, out, want, errOut)
	}
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != 0 || out != want {
		t.Errorf("preamble and upper-case fingerprint: exit %d\nout %q\nerr %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != ExitError || !strings.Contains(errOut, "does not match") {
		t.Errorf("wrong fingerprint: exit %d, err %q", code, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "web-ca", "--socket", sock); code != ExitError || !strings.Contains(errOut, "no PEM certificate") {
		t.Errorf("no PEM: exit %d, err %q", code, errOut)
	}
	if out, errOut, code := cli.run(t, "controller", "web-ca", "--json", "--socket", sock); code != 0 ||
		!jsonEqual([]byte(out), []byte(webCAReply(t, rootPEM, ca.fingerprint()))) {
		t.Errorf("--json: exit %d, out %q, err %q", code, out, errOut)
	}
	if _, errOut, code := cli.run(t, "controller", "bogus"); code != ExitUsage {
		t.Errorf("controller bogus: exit %d, err %q", code, errOut)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestControllerWebCA)$' ./internal/cli/`
Expected: FAIL — the test fails: `web-ca: exit 2` with `darkpanel: unknown command "controller" for "darkpanel"`.

- [ ] **Step 3: Write the implementation**

Create `internal/cli/controller.go`:

```go
package cli

import (
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/spf13/cobra"
)

// controllerCommand is the hand-written `controller` group; in M1 it has
// web-ca only.
func (a *app) controllerCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "controller",
		Short: "Commands for the controller on this host",
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(args) > 0 {
				return usagef("unknown command %q for %q", args[0], cmd.CommandPath())
			}
			return cmd.Help()
		},
	}
	webCA := &cobra.Command{
		Use:   "web-ca",
		Short: "Print the web certificate authority's root certificate and its SHA-256 fingerprint",
		Long: "Print the root certificate of the controller's web certificate authority in PEM, to trust in a " +
			"browser, and its SHA-256 fingerprint, to pin as caFingerprint in the CLI config file. " +
			"It reads both from the controller socket.",
		Args: cobra.NoArgs,
		RunE: a.runWebCA,
	}
	addConnFlags(webCA, true, false)
	webCA.Flags().Bool("json", false, "print the raw JSON answer")
	cmd.AddCommand(webCA)
	return cmd
}

// runWebCA prints the root from GET /api/v1/controller/web-ca. The PEM is
// decoded, checked against the fingerprint and written re-encoded, so
// nothing but the certificate reaches the terminal.
func (a *app) runWebCA(cmd *cobra.Command, _ []string) error {
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	var raw json.RawMessage
	if err := c.do(cmd.Context(), http.MethodGet, "/api/v1/controller/web-ca", nil, &raw); err != nil {
		return err
	}
	w := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		_, err := fmt.Fprintf(w, "%s\n", raw)
		return err
	}
	var resp struct {
		PEM         string `json:"pem"`
		Fingerprint string `json:"fingerprint"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return fmt.Errorf("decode the web CA: %w", err)
	}
	block, _ := pem.Decode([]byte(resp.PEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return errors.New("the controller answered no PEM certificate")
	}
	if _, err := x509.ParseCertificate(block.Bytes); err != nil {
		return fmt.Errorf("the controller's web CA certificate: %w", err)
	}
	sum := sha256.Sum256(block.Bytes)
	fingerprint := hex.EncodeToString(sum[:])
	if !strings.EqualFold(fingerprint, resp.Fingerprint) {
		return errors.New("the fingerprint the controller sent does not match its certificate")
	}
	if _, err := w.Write(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: block.Bytes})); err != nil {
		return err
	}
	_, err = fmt.Fprintf(w, "SHA-256 fingerprint: %s\n", fingerprint)
	return err
}
```

In `internal/cli/root.go`, edit 1 of 1. Replace this block:

```go
	}
	root.AddCommand(a.changeCommand())
	markRuns(root)
```

with:

```go
	}
	root.AddCommand(a.changeCommand(), a.controllerCommand())
	markRuns(root)
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestControllerWebCA)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/controller.go internal/cli/root.go internal/cli/controller_test.go
git commit -m "feat(cli): print the web CA through the socket"
```

### Task 29: user create and reset-totp enrollment

**Files:**
- Create: `internal/cli/enroll.go`
- Modify: `internal/cli/root.go`
- Test: `internal/cli/enroll_test.go`

**Interfaces:**
- Consumes: Contracts › Task 22 (`planAndApply`, `changeOutcome`), Task 20 (`app`, `connect`, `FindCommand`; `cliEnv`), Task 17 (`terminal.secret`), Task 16 (`newFakeSocket`), Task 1 (`usagef`, `sanitize`); `auth.TOTPCode` in the test (merged `internal/controller/auth/totp.go`); `github.com/mdp/qrterminal/v3` `GenerateHalfBlock` and `L` (pinned).
- Produces: Contracts › Task 29 (`actionUserCreate`, `actionUserResetTOTP`, `actionUserActivate`, `latestAction`, `wrapEnrollment`, `runEnrollment`, `newPassword`, `enrollmentURI`).

**Items:** 3

**Depends on:** Task 28

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Spec sections 8 and 15: `darkpanel user create --username <name>` asks for the password twice without echo, applies `core.user.create` through the mutating flow on the controller socket, renders the `otpauth://totp/Darkpanel:<username>?secret=<base32>&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30` URI as a terminal QR code with the URI and secret below it, asks for a code and applies `core.user.activate`. `user reset-totp` runs the same enrollment after its irreversible reset. The generated commands are wrapped in place, so they keep their flags and annotations and `FindCommand` still reports them. The tests compute codes with `auth.TOTPCode`, and cover the terminal and piped flows, differing passwords, a missing username or password, `--json` refused, and a wrong code ending with the `user activate` hint.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/enroll_test.go`:

```go
package cli

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
)

// testTOTPSecret is a 20-byte TOTP secret in unpadded base32.
const testTOTPSecret = "JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP" //nolint:gosec // G101: a test TOTP secret, not a credential

const testUserPassword = "correct horse battery" //nolint:gosec // G101: a test password, not a credential

func enrollmentChange(id, action string) string {
	return changeJSON(id, action, "compensable", "completed", fmt.Sprintf(`"revertDeadline":null,`+
		`"output":{"user":{"id":"u1","username":"ann","role":"admin","state":"pending","createdAt":"2026-10-18T12:00:00Z"},`+
		`"totpSecret":%q},"error":null,"compensatedBy":"core.user.delete"`, testTOTPSecret))
}

// activation is the plan and apply of core.user.activate with code.
func activation(code, state string) []exchange {
	extra := `"revertDeadline":null,"output":{"id":"u1","username":"ann","role":"admin","state":"active","createdAt":"2026-10-18T12:00:00Z"},` +
		`"error":null,"compensatedBy":"core.user.reset-totp"`
	if state == "failed" {
		extra = `"revertDeadline":null,"output":null,"error":{"code":"request.invalid_input","message":"invalid input"},` +
			`"compensatedBy":"core.user.reset-totp"`
	}
	return []exchange{
		{method: "POST", path: "/api/v1/actions/core.user.activate/v1/plan", body: `{"input":{"username":"ann","code":"` + code + `"}}`,
			reply: planJSON("pa", "core.user.activate", "", "", `[{"id":"user-pending","ok":true,"message":"ann is pending"}]`)},
		{method: "POST", path: "/api/v1/actions/core.user.activate/v1/apply", body: `{"planId":"pa"}`,
			reply: changeJSON("ca", "core.user.activate", "compensable", state, extra)},
	}
}

func TestUserCreateEnrollment(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	want := append([]exchange{
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/plan",
			body:  `{"input":{"username":"ann","password":"` + testUserPassword + `"}}`,
			reply: planJSON("pc", "core.user.create", "", "", `[{"id":"username-free","ok":true,"message":"ann is free"}]`)},
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/apply", body: `{"planId":"pc"}`,
			reply: enrollmentChange("cc", "core.user.create")},
	}, activation(code, "completed")...)
	sock := newFakeSocket(t, want...)
	cli := cliEnv{terminal: true, stdin: "y\n", secrets: []string{testUserPassword, testUserPassword, code}}
	out, errOut, exit := cli.run(t, "user", "create", "--username", "ann", "--socket", sock)
	uri := "otpauth://totp/Darkpanel:ann?secret=" + testTOTPSecret + "&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30"
	for _, s := range []string{"Change cc: completed\n", "URI: " + uri + "\n", "Secret: " + testTOTPSecret + "\n",
		"The secret will not be shown again", "▀", "Change ca: completed\n", "User ann is active"} {
		if !strings.Contains(out, s) {
			t.Errorf("stdout lacks %q", s)
		}
	}
	if exit != 0 || errOut != "Password: \nRepeat the password: \nApply? [y/N] Code from the authenticator app: \n" {
		t.Errorf("exit %d, err %q\nout %s", exit, errOut, out)
	}
	if strings.Contains(out+errOut, testUserPassword) {
		t.Error("the password was echoed")
	}
}

func TestUserCreateWithoutTerminal(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sock := newFakeSocket(t, append([]exchange{
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/plan",
			body:  `{"input":{"username":"ann","password":"` + testUserPassword + `"}}`,
			reply: planJSON("pc", "core.user.create", "", "", `[]`)},
		{method: "POST", path: "/api/v1/actions/core.user.create/v1/apply", body: `{"planId":"pc"}`,
			reply: enrollmentChange("cc", "core.user.create")},
	}, activation(code, "completed")...)...)
	out, errOut, exit := cliEnv{stdin: testUserPassword + "\n" + code + "\n"}.run(t, "user", "create", "--username", "ann", "--yes", "--socket", sock)
	if exit != 0 || !strings.Contains(out, "User ann is active") {
		t.Errorf("exit %d, out %q, err %q", exit, out, errOut)
	}
}

func TestUserCreateRefusals(t *testing.T) {
	sock := newFakeSocket(t)
	cases := []struct {
		name string
		cli  cliEnv
		args []string
		want string
	}{
		{"passwords differ", cliEnv{terminal: true, secrets: []string{testUserPassword, "other password!"}},
			[]string{"--username", "ann"}, "do not match"},
		{"no username", cliEnv{terminal: true}, nil, "--username"},
		{"no password", cliEnv{}, []string{"--username", "ann", "--yes"}, "no password"},
		{"--json", cliEnv{terminal: true}, []string{"--username", "ann", "--json"}, "--json"},
	}
	for _, c := range cases {
		_, errOut, exit := c.cli.run(t, append(append([]string{"user", "create"}, c.args...), "--socket", sock)...)
		if exit != ExitUsage || !strings.Contains(errOut, c.want) {
			t.Errorf("%s: exit %d, err %q", c.name, exit, errOut)
		}
	}
}

func TestResetTOTPEnrollment(t *testing.T) {
	code, err := auth.TOTPCode(testTOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	reset := []exchange{
		{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/plan", body: `{"input":{"username":"ann"}}`,
			reply: planJSON("pr", "core.user.reset-totp", "", "ann", `[]`)},
		{method: "POST", path: "/api/v1/actions/core.user.reset-totp/v1/apply", body: `{"planId":"pr","confirmation":"ann"}`,
			reply: changeJSON("cr", "core.user.reset-totp", "irreversible", "completed", fmt.Sprintf(`"revertDeadline":null,`+
				`"output":{"user":{"id":"u1","username":"ann","role":"admin","state":"pending","createdAt":"2026-10-18T12:00:00Z"},`+
				`"totpSecret":%q},"error":null,"compensatedBy":null`, testTOTPSecret))},
	}
	sock := newFakeSocket(t, append(append(reset, activation(code, "completed")...), append(reset, activation("000000", "failed")...)...)...)
	args := []string{"user", "reset-totp", "--username", "ann", "--yes", "--confirmation", "ann", "--socket", sock}
	out, errOut, exit := cliEnv{stdin: code + "\n"}.run(t, args...)
	if exit != 0 || !strings.Contains(out, "Secret: "+testTOTPSecret) || !strings.Contains(out, "User ann is active") {
		t.Errorf("reset-totp: exit %d, out %q, err %q", exit, out, errOut)
	}
	_, errOut, exit = cliEnv{stdin: "000000\n"}.run(t, args...)
	if exit != ExitError || !strings.Contains(errOut, "darkpanel user activate --username ann") {
		t.Errorf("wrong code: exit %d, err %q", exit, errOut)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserCreateEnrollment|TestUserCreateWithoutTerminal|TestUserCreateRefusals|TestResetTOTPEnrollment)$' ./internal/cli/`
Expected: FAIL — the tests fail: `stdout lacks "URI: otpauth://totp/Darkpanel:ann?secret=JBSWY3DPEHPK3PXPJBSWY3DPEHPK3PXP&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30\n"` and the same for the secret note, the QR block and the activation, because the generic flow runs instead (`err "password: \nApply? [y/N] "`).

- [ ] **Step 3: Write the implementation**

Create `internal/cli/enroll.go`:

```go
package cli

import (
	"encoding/base32"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"

	"github.com/mdp/qrterminal/v3"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// The actions whose generated commands run the TOTP enrollment (spec
// section 15), and the one that ends it.
const (
	actionUserCreate    = "core.user.create"
	actionUserResetTOTP = "core.user.reset-totp"
	actionUserActivate  = "core.user.activate"
)

// latestAction returns the highest version of the action name in reg.
func latestAction(reg *core.Registry, name string) (core.ActionDef, bool) {
	var def core.ActionDef
	found := false
	for _, a := range reg.Actions() {
		if a.Spec.Name == name {
			def, found = a, true
		}
	}
	return def, found
}

// wrapEnrollment makes the generated `user create` and `user reset-totp`
// commands run the enrollment flow. They keep their flags and annotations,
// so FindCommand still reports them.
func (a *app) wrapEnrollment(root *cobra.Command) {
	activate, ok := latestAction(a.opts.Registry, actionUserActivate)
	if !ok {
		return
	}
	for _, name := range []string{actionUserCreate, actionUserResetTOTP} {
		cmd, found := FindCommand(root, name)
		def, ok := latestAction(a.opts.Registry, name)
		if !found || !ok {
			continue
		}
		cmd.RunE = func(cmd *cobra.Command, _ []string) error { return a.runEnrollment(cmd, def, activate) }
	}
}

// runEnrollment applies def (core.user.create, which first asks for the
// password twice, or core.user.reset-totp), shows the new TOTP secret as a
// QR code with its otpauth URI and the secret below it, asks for a code and
// applies core.user.activate with it.
func (a *app) runEnrollment(cmd *cobra.Command, def, activate core.ActionDef) error {
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		return usagef("%s is interactive and has no --json output", cmd.CommandPath())
	}
	username, _ := cmd.Flags().GetString("username")
	if username == "" {
		return usagef("required flag --username not set")
	}
	input := map[string]any{"username": username}
	if def.Spec.Name == actionUserCreate {
		password, err := a.newPassword()
		if err != nil {
			return err
		}
		input["password"] = password
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	out := cmd.OutOrStdout()
	ch, _, err := a.planAndApply(cmd, c, def, "", input, false)
	if err != nil {
		return err
	}
	_, _ = fmt.Fprintf(out, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State))
	if err := changeOutcome(ch); err != nil {
		return err
	}
	var en struct {
		TOTPSecret string `json:"totpSecret"`
	}
	if err := json.Unmarshal(ch.Output, &en); err != nil {
		return fmt.Errorf("decode the enrollment: %w", err)
	}
	if _, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(en.TOTPSecret); err != nil || en.TOTPSecret == "" {
		return errors.New("the controller answered no valid TOTP secret")
	}
	uri := enrollmentURI(username, en.TOTPSecret)
	_, _ = fmt.Fprintln(out, "Scan this QR code with an authenticator app, or enter the secret by hand:")
	qrterminal.GenerateHalfBlock(uri, qrterminal.L, out)
	_, _ = fmt.Fprintf(out, "URI: %s\nSecret: %s\nThe secret will not be shown again.\n", sanitize(uri), en.TOTPSecret)
	retry := fmt.Sprintf("activate the user with: darkpanel user activate --username %s", username)
	code, err := a.term.secret("Code from the authenticator app: ")
	if errors.Is(err, io.EOF) {
		return usagef("no code given; %s", retry)
	}
	if err != nil {
		return err
	}
	act, _, err := a.planAndApply(cmd, c, activate, "", map[string]any{"username": username, "code": code}, true)
	if err != nil {
		return fmt.Errorf("%w; %s", err, retry)
	}
	_, _ = fmt.Fprintf(out, "Change %s: %s\n", sanitize(act.ID), sanitize(act.State))
	if err := changeOutcome(act); err != nil {
		return fmt.Errorf("%w; %s", err, retry)
	}
	_, err = fmt.Fprintf(out, "User %s is active and can log in.\n", sanitize(username))
	return err
}

// newPassword reads the new user's password: twice on a terminal, without
// echo, refusing two different answers; once from standard input otherwise.
func (a *app) newPassword() (string, error) {
	password, err := a.term.secret("Password: ")
	if errors.Is(err, io.EOF) {
		return "", usagef("no password: run on a terminal, or give it as one line on standard input")
	}
	if err != nil || !a.term.isTerm {
		return password, err
	}
	again, err := a.term.secret("Repeat the password: ")
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	if again != password {
		return "", usagef("the passwords do not match")
	}
	return password, nil
}

// enrollmentURI is the otpauth URI of spec section 8.
func enrollmentURI(username, secret string) string {
	return "otpauth://totp/Darkpanel:" + url.PathEscape(username) + "?secret=" + secret +
		"&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30"
}
```

In `internal/cli/root.go`, edit 1 of 1. Replace this block:

```go
	root.AddCommand(a.changeCommand(), a.controllerCommand())
	markRuns(root)
```

with:

```go
	root.AddCommand(a.changeCommand(), a.controllerCommand())
	a.wrapEnrollment(root)
	markRuns(root)
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserCreateEnrollment|TestUserCreateWithoutTerminal|TestUserCreateRefusals|TestResetTOTPEnrollment)$' ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/cli/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without tests); the race run prints `ok  	github.com/darkraise/darkpanel/internal/cli`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/cli/enroll.go internal/cli/root.go internal/cli/enroll_test.go
git commit -m "feat(cli): enroll TOTP on user create and reset"
```

### Task 30: Count and audit login faults after the password

**Files:**
- Modify: `internal/controller/auth/login.go`
- Test: `internal/controller/auth/login_test.go`

**Interfaces:**
- Consumes: the merged `auth.Service` login path (plan 3 Contracts › Task 7 `Login`, `evaluate`, `attempt`, `recordLogin`; Task 3 `limiter`, `usernameKey`, `UsernameFailureLimit`); `coretest.Sealer`, `coretest.MemAudit`; test helpers `newTestService`, `createActive`, `code`, `wantCode`.
- Produces: Contracts › Task 30 (`Login` fault rule; `evaluate` and `attempt` return `verified`).

**Items:** 67

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 3 = 4

Owner decision of 2026-10-18 (register row 67): an error after the password verified (the database, the sealer, or the audit write of a success) counts as a failed attempt in the limiter and writes a login audit row with result `error` and code `internal`; the original error is still returned, answered `internal`, and a failure of that audit row is ignored. `attempt` now reports whether the password verified, so faults before that point (user lookup, dummy hash, malformed stored hash) stay uncounted as before. Timing is unchanged. The tests inject a sealer whose `Open` fails and an audit log that fails its first `Record` calls, and check the limiter's count, the lock after `UsernameFailureLimit` faults, the audit row, and which error is returned.

- [ ] **Step 1: Write the failing test**

Add at the end of `internal/controller/auth/login_test.go`, after one blank line:

```go
var (
	errSealerDown = errors.New("sealer is down")
	errAuditDown  = errors.New("audit log is down")
)

// openFails is a sealer whose Open always fails, so a login with the right
// password fails inside its write transaction.
type openFails struct{ coretest.Sealer }

func (openFails) Open(string, []byte) ([]byte, error) { return nil, errSealerDown }

// failingAudit fails its first fail Record calls, then records.
type failingAudit struct {
	*coretest.MemAudit
	fail int
}

func (a *failingAudit) Record(ctx context.Context, e core.AuditEntry) error {
	if a.fail > 0 {
		a.fail--
		return errAuditDown
	}
	return a.MemAudit.Record(ctx, e)
}

// failures is the number of failures the limiter holds for key.
func failures(s *Service, key string) int {
	s.limiter.mu.Lock()
	defer s.limiter.mu.Unlock()
	if c, ok := s.limiter.keys[key]; ok {
		return len(c.failures)
	}
	return 0
}

func TestLoginFaultAfterPasswordCounts(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	s.sealer = openFails{}
	req := LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"}
	for i := range UsernameFailureLimit {
		_, err := s.Login(ctx, req)
		if !errors.Is(err, errSealerDown) || errcode.From(err).Code != errcode.Internal {
			t.Fatalf("attempt %d: error = %v, want the sealer's error, answered internal", i, err)
		}
		// The last failure locks the username and clears its count.
		if n, want := failures(s, usernameKey("alice")), (i+1)%UsernameFailureLimit; n != want {
			t.Fatalf("attempt %d: %d failures counted for alice, want %d", i, n, want)
		}
		entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
		e := entries[0]
		if len(entries) != 1 || e.Actor != "user:"+u.ID || e.Action != AuditLoginAction || e.Phase != "login" ||
			e.Result != "error" || e.ErrorCode != string(errcode.Internal) || e.Source != "10.0.0.1" ||
			string(e.Input) != `{"username":"alice"}` {
			t.Fatalf("attempt %d: audit entry = %+v, want a login error with code internal", i, e)
		}
	}
	_, err := s.Login(ctx, req)
	wantCode(t, err, errcode.RateLimited)

	_, err = s.Login(ctx, LoginRequest{Username: "bob", Password: testPassword, Code: "000000", Source: "10.0.0.2"})
	wantCode(t, err, errcode.InvalidCredentials)
	entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
	if entries[0].ErrorCode != string(errcode.InvalidCredentials) {
		t.Errorf("an unknown user with a failing sealer was audited as %+v, want invalid credentials", entries[0])
	}
}

func TestLoginAuditFaultCounts(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	audit := &failingAudit{MemAudit: &coretest.MemAudit{}, fail: 1}
	s.audit = audit
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if !errors.Is(err, errAuditDown) || errcode.From(err).Code != errcode.Internal {
		t.Fatalf("error = %v, want the audit's error, answered internal", err)
	}
	if n := failures(s, usernameKey("alice")); n != 1 {
		t.Errorf("%d failures counted for alice, want 1", n)
	}
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(entries) != 1 || entries[0].Actor != "user:"+u.ID || entries[0].Result != "error" ||
		entries[0].ErrorCode != string(errcode.Internal) {
		t.Errorf("audit = %+v, want one login error with code internal", entries)
	}

	clock.Advance(TOTPPeriod)
	s.sealer = openFails{}
	audit.fail = 1
	_, err = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if !errors.Is(err, errSealerDown) || errors.Is(err, errAuditDown) {
		t.Errorf("error = %v, want the sealer's error alone when the audit row also fails", err)
	}
	if n := failures(s, usernameKey("alice")); n != 2 {
		t.Errorf("%d failures counted for alice, want 2", n)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLoginFaultAfterPasswordCounts|TestLoginAuditFaultCounts' ./internal/controller/auth/`

Expected: FAIL — `--- FAIL: TestLoginFaultAfterPasswordCounts` with `login_test.go:394: attempt 0: 0 failures counted for alice, want 1`, and `--- FAIL: TestLoginAuditFaultCounts` with `login_test.go:426: 0 failures counted for alice, want 1`, `login_test.go:431: audit = [], want one login error with code internal` and `login_test.go:442: 0 failures counted for alice, want 2`.

- [ ] **Step 3: Write the implementation**

Replace the whole content of `internal/controller/auth/login.go` with:

```go
package auth

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Audit rows of logins and logouts (spec section 11, ADR 0011).
const (
	AuditLoginAction  = "core.auth.login"
	AuditLogoutAction = "core.auth.logout"
	ActorAnonymous    = "anonymous"
)

// LoginRequest is one login attempt. Source is the client address and
// UserAgent its User-Agent header; both are stored with the session.
type LoginRequest struct {
	Username, Password, Code, Source, UserAgent string
}

// LoginResult is a successful login. SessionID is the session cookie's value.
type LoginResult struct {
	SessionID string
	CSRFToken string
	ExpiresAt time.Time
	User      User
}

// Credentials are what a request presents. Authorization holds every value
// of the Authorization header and is nil when the header is absent; Session
// is the session cookie's value, "" when absent.
type Credentials struct {
	Authorization []string
	Session       string
	Source        string
}

func errInvalidCredentials() *errcode.Error {
	return errcode.New(errcode.InvalidCredentials, "invalid username, password or code")
}

// Login checks username, password and TOTP code and opens a session. Every
// failure is the same auth.invalid_credentials error. A username or source
// address locked by the rate limits is auth.rate_limited, with a
// *RetryAfterError cause, and is not evaluated. Evaluated attempts are
// audited; a username that fails the pattern is refused before anything
// else, unaudited.
//
// A fault after the password verified (database, sealer or audit write)
// counts as a failed attempt and is audited with result error and code
// internal, so an attacker who can provoke faults gains no free guesses at
// the code. The fault itself is returned, and answers internal.
func (s *Service) Login(ctx context.Context, r LoginRequest) (LoginResult, error) {
	if checkUsername(r.Username) != nil {
		return LoginResult{}, errInvalidCredentials()
	}
	if d := s.limiter.blocked(r.Username, r.Source); d > 0 {
		return LoginResult{}, errRateLimited(d)
	}
	if err := s.hasher.acquire(ctx); err != nil {
		return LoginResult{}, err
	}
	res, userID, verified, err := s.evaluate(ctx, r)
	s.hasher.release()
	if err != nil {
		switch {
		case errcode.From(err).Code == errcode.InvalidCredentials:
			if aerr := s.recordLogin(ctx, r, userID, errcode.InvalidCredentials); aerr != nil {
				return LoginResult{}, aerr
			}
		case verified:
			_ = s.recordLogin(ctx, r, userID, errcode.Internal)
		}
		return LoginResult{}, err
	}
	if err := s.recordLogin(ctx, r, userID, ""); err != nil {
		s.limiter.fail(r.Username, r.Source)
		_ = s.recordLogin(ctx, r, userID, errcode.Internal)
		return LoginResult{}, err
	}
	return res, nil
}

func errRateLimited(d time.Duration) error {
	return errcode.Wrap(errcode.RateLimited, &RetryAfterError{After: d},
		"too many failed login attempts; try again later")
}

// evaluate runs attempt for a caller holding a hashing slot. The limits are
// checked again and a failure is counted under the slot: attempts that passed
// Login's first check while waiting for a slot would otherwise all be
// evaluated before any of their failures counted. Invalid credentials and
// any error after the password verified count as failures.
func (s *Service) evaluate(ctx context.Context, r LoginRequest) (LoginResult, string, bool, error) {
	if d := s.limiter.blocked(r.Username, r.Source); d > 0 {
		return LoginResult{}, "", false, errRateLimited(d)
	}
	res, userID, verified, err := s.attempt(ctx, r)
	if err != nil && (verified || errcode.From(err).Code == errcode.InvalidCredentials) {
		s.limiter.fail(r.Username, r.Source)
	}
	return res, userID, verified, err
}

// attempt evaluates a login and returns the matched user's id ("" when none
// matched) and whether the password verified. An unknown username still
// costs one argon2id hash. The caller holds a hashing slot.
func (s *Service) attempt(ctx context.Context, r LoginRequest) (LoginResult, string, bool, error) {
	rec, err := loadUser(ctx, s.db.Read(), r.Username)
	if err != nil {
		if errcode.From(err).Code != errcode.NotFound {
			return LoginResult{}, "", false, err
		}
		if err := s.hasher.verifyDummyHeld(r.Password); err != nil {
			return LoginResult{}, "", false, err
		}
		return LoginResult{}, "", false, errInvalidCredentials()
	}
	ok, err := s.hasher.verifyHeld(rec.passwordHash, r.Password)
	if err != nil {
		return LoginResult{}, rec.ID, false, err
	}
	if !ok || rec.State != StateActive {
		return LoginResult{}, rec.ID, ok, errInvalidCredentials()
	}
	var res LoginResult
	// The code's step is checked and stored in the same write transaction as
	// the session, so two logins racing with one code cannot both succeed.
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		cur, err := loadUser(ctx, tx, r.Username)
		if err != nil {
			if errcode.From(err).Code == errcode.NotFound {
				return errInvalidCredentials()
			}
			return err
		}
		if cur.ID != rec.ID || cur.passwordHash != rec.passwordHash || cur.State != StateActive {
			return errInvalidCredentials()
		}
		key, err := s.sealer.Open(totpAD(cur.ID), cur.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, r.Code, s.clock.Now(), cur.lastStep)
		if !ok {
			return errInvalidCredentials()
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET totp_last_step = ? WHERE id = ?`, step, cur.ID); err != nil {
			return fmt.Errorf("auth: store TOTP step: %w", err)
		}
		id, exp, err := s.createSession(ctx, tx, cur.ID, r.Source, r.UserAgent)
		if err != nil {
			return err
		}
		res = LoginResult{SessionID: id, CSRFToken: s.CSRFToken(id), ExpiresAt: exp, User: cur.User}
		return nil
	})
	return res, rec.ID, true, err
}

// recordLogin audits a login; code is "" for a success. The row is written
// even if the request was cancelled, so a session never exists unaudited.
func (s *Service) recordLogin(ctx context.Context, r LoginRequest, userID string, code errcode.Code) error {
	actor := ActorAnonymous
	if userID != "" {
		actor = "user:" + userID
	}
	input, err := json.Marshal(map[string]string{"username": r.Username})
	if err != nil {
		return fmt.Errorf("auth: audit login: %w", err)
	}
	e := core.AuditEntry{ID: s.newID(), Actor: actor, Action: AuditLoginAction, Version: 1, Phase: "login",
		Result: "ok", Source: r.Source, At: s.now(), Input: input}
	if code != "" {
		e.Result, e.ErrorCode = "error", string(code)
	}
	if err := s.audit.Record(context.WithoutCancel(ctx), e); err != nil {
		return fmt.Errorf("auth: audit login: %w", err)
	}
	return nil
}

// Logout ends the session sessionID and audits it. Ending a session that does
// not exist does nothing.
func (s *Service) Logout(ctx context.Context, sessionID, source string) error {
	userID, ok, err := s.endSession(ctx, sessionID)
	if err != nil || !ok {
		return err
	}
	e := core.AuditEntry{ID: s.newID(), Actor: "user:" + userID, Action: AuditLogoutAction, Version: 1, Phase: "logout",
		Result: "ok", Source: source, At: s.now(), Input: json.RawMessage(`{}`)}
	if err := s.audit.Record(context.WithoutCancel(ctx), e); err != nil {
		return fmt.Errorf("auth: audit logout: %w", err)
	}
	return nil
}

// Authenticate resolves a request's caller. When an Authorization header is
// present only its bearer token is considered and the cookie never is (spec
// section 9); otherwise the session cookie is. Any failure is auth.required.
func (s *Service) Authenticate(ctx context.Context, c Credentials) (Identity, error) {
	if c.Authorization != nil {
		if len(c.Authorization) != 1 {
			return Identity{}, errAuthRequired()
		}
		scheme, token, ok := strings.Cut(c.Authorization[0], " ")
		if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
			return Identity{}, errAuthRequired()
		}
		return s.authenticateToken(ctx, token, c.Source)
	}
	if c.Session == "" {
		return Identity{}, errAuthRequired()
	}
	return s.authenticateSession(ctx, c.Session, c.Source)
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLogin|TestConcurrentLogins' ./internal/controller/auth/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/auth`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/login.go internal/controller/auth/login_test.go
git commit -m "fix(auth): count and audit faults after password"
```

### Task 31: A panicking handler hands out no cookie

**Files:**
- Modify: `internal/controller/api/middleware.go`
- Test: `internal/controller/api/wire_test.go`

**Interfaces:**
- Consumes: the merged `recoverPanics`, `writeProblem` (plan 3 Contracts › Task 11) and `sessionCookie` (Task 20); test helpers `testLog`, `decodeProblem`.
- Produces: Contracts › Task 31 (`recoverPanics` drops `Set-Cookie`).

**Items:** 73

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 3 = 4

`recoverPanics` kept the headers a handler had set before it panicked, so a handler that set a session cookie and then panicked would still hand out the session with its 500 answer. It now deletes `Set-Cookie` before it writes the `internal` problem. The test sets a session cookie through `sessionCookie`, panics, and checks the answer is `internal` with no `Set-Cookie`.

- [ ] **Step 1: Write the failing test**

In `internal/controller/api/wire_test.go`, replace this block (it occurs once):

```go
	}
}

func TestRecoverPanicsAfterWriteAborts(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
```

with:

```go
	}
}

func TestRecoverPanicsDropsCookies(t *testing.T) {
	log, _ := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.SetCookie(w, sessionCookie(r, "session-from-a-failed-handler", 0))
		panic("boom after the cookie")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, loginPath, nil))
	decodeProblem(t, rec, 500, errcode.Internal)
	if got := rec.Result().Header.Values("Set-Cookie"); len(got) != 0 {
		t.Errorf("a panicking handler's cookie was sent: %q", got)
	}
}

func TestRecoverPanicsAfterWriteAborts(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRecoverPanics' ./internal/controller/api/`

Expected: FAIL — `--- FAIL: TestRecoverPanicsDropsCookies` with `wire_test.go:286: a panicking handler's cookie was sent: ["darkpanel_session=session-from-a-failed-handler; Path=/; HttpOnly; SameSite=Strict"]`.

- [ ] **Step 3: Write the implementation**

Replace the whole content of `internal/controller/api/middleware.go` with:

```go
package api

import (
	"errors"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ContentSecurityPolicy is sent on every response (spec section 12).
// style-src allows inline styles because the UI kit sets style attributes;
// scripts stay strict.
const ContentSecurityPolicy = "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; " +
	"img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; " +
	"form-action 'self'"

const healthzPath = "/healthz"

// isAPIPath reports whether path belongs to the API rather than the UI.
func isAPIPath(path string) bool {
	return path == "/api" || strings.HasPrefix(path, "/api/")
}

// securityHeaders sets the response headers of spec section 12. HSTS is sent
// only over HTTPS, and API and health responses are never cached.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", ContentSecurityPolicy)
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Cross-Origin-Opener-Policy", "same-origin")
		if r.TLS != nil {
			h.Set("Strict-Transport-Security", "max-age=31536000")
		}
		if isAPIPath(r.URL.Path) || r.URL.Path == healthzPath {
			h.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}

// trackingWriter records whether the response has started.
type trackingWriter struct {
	http.ResponseWriter
	wrote bool
}

func (t *trackingWriter) WriteHeader(status int) {
	t.wrote = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *trackingWriter) Write(b []byte) (int, error) {
	t.wrote = true
	return t.ResponseWriter.Write(b)
}

func (t *trackingWriter) Unwrap() http.ResponseWriter { return t.ResponseWriter }

// recoverPanics logs a handler panic with its stack and answers internal. A
// panic inside the lifecycle engine can leave a change applying until
// recovery, so it must reach the log. When the response has already started
// it aborts the connection instead, so the client never takes a truncated
// answer for a complete one. The internal answer drops any Set-Cookie the
// handler set, so a handler that opened a session and then panicked hands
// out no session.
func recoverPanics(log *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tw := &trackingWriter{ResponseWriter: w}
		defer func() {
			v := recover()
			if v == nil {
				return
			}
			if err, ok := v.(error); ok && errors.Is(err, http.ErrAbortHandler) {
				panic(v)
			}
			log.Error("api: handler panic", "method", r.Method, "path", r.URL.Path, "panic", v, "stack", string(debug.Stack()))
			if tw.wrote {
				panic(http.ErrAbortHandler)
			}
			w.Header().Del("Set-Cookie")
			writeProblem(w, log, errcode.New(errcode.Internal, ""))
		}()
		next.ServeHTTP(tw, r)
	})
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRecoverPanics' ./internal/controller/api/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/middleware.go internal/controller/api/wire_test.go
git commit -m "fix(api): drop Set-Cookie when a handler panics"
```

### Task 32: Network change views hide break-glass changes

**Files:**
- Modify: `internal/controller/api/actions.go`
- Test: `internal/controller/api/actions_test.go`

**Interfaces:**
- Consumes: the merged `handleChanges`, `handleChange`, `changeOp`, `authorizeChange`, `changeFilter`, `toChange`, `maxChangeLimit` (plan 3 Contracts › Task 21); `core.Registry.Action`, `core.ChangeFilter`, `core.Dispatcher.Changes` and `Change`; `errcode.ChangeNotFound`; test helpers `newHarness`, `stubDispatcher` (ids `c1`, `c7`, `cbg`, `cunk`, `cua`, `missing`), `request`, `withToken`, `decodeProblem`.
- Produces: Contracts › Task 32 (`networkVisible`, `errHiddenChange`, `defaultChangeLimit`, `maxExtraChangeFetches`, list, show, confirm and revert behaviour).

**Items:** 68

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Owner decision of 2026-10-18 (register row 68): `GET /api/v1/changes` leaves out every change of a break-glass action and every controller-side change whose action is not registered (whether it was break-glass cannot be known), and `GET /api/v1/changes/{id}`, `POST /api/v1/changes/{id}/confirm` and `.../revert` answer those `change.not_found` with the engine's own detail `no change <id>` (controller ruling of 2026-10-18), so a hidden id cannot be told from a missing one. Agent-side changes of unregistered actions stay visible, because the registry refuses agent-side break-glass actions. To keep pages full the list always passes an explicit limit (100 when the query names none) and, while hidden changes leave the page short and the last fetch was full, fetches further pages with `Before` set to the last fetched id and limit 500, at most 4 times. `authorizeChange` keeps only the scope check, since hidden changes are refused before it. The tests use a fake store that pages like the engine and check the listed ids, every filter the dispatcher received (state and host kept, cursor and limit), and the bound; show, confirm and revert of a hidden change answer exactly like a missing one and confirm and revert dispatch nothing; `TestChangeOpsAuthorizeTheChangesAction` changes its three hidden-change rows from 403 to 404.

- [ ] **Step 1: Write the failing test**

In `internal/controller/api/actions_test.go`, make these 3 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
```

with:

```go
import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
```

2. Replace:

```go
			{"unregistered agent action with operate", "cua", operate, false, 200, ""},
			{"controller change with operate", "c1", operate, false, 403, errcode.Forbidden},
			{"controller change with admin", "c1", admin, false, 200, ""},
			{"break-glass change with admin", "cbg", admin, false, 403, errcode.Forbidden},
			{"break-glass change with a session", "cbg", "", true, 403, errcode.Forbidden},
			{"unregistered controller action with admin", "cunk", admin, false, 403, errcode.Forbidden},
			{"missing change", "missing", admin, false, 404, errcode.ChangeNotFound},
		} {
			last = call{}
```

with:

```go
			{"unregistered agent action with operate", "cua", operate, false, 200, ""},
			{"controller change with operate", "c1", operate, false, 403, errcode.Forbidden},
			{"controller change with admin", "c1", admin, false, 200, ""},
			{"break-glass change with admin", "cbg", admin, false, 404, errcode.ChangeNotFound},
			{"break-glass change with a session", "cbg", "", true, 404, errcode.ChangeNotFound},
			{"unregistered controller action with admin", "cunk", admin, false, 404, errcode.ChangeNotFound},
			{"missing change", "missing", admin, false, 404, errcode.ChangeNotFound},
		} {
			last = call{}
```

3. Add at the end of the file, after one blank line:

```go
// changesOf returns one change per letter of kinds, newest first, with ids
// that sort like ULIDs: v an agent-side demo.item.add change, a an agent-side
// change of an unregistered action, b a change of the break-glass
// demo.secret.reset, u a controller-side change of an unregistered action.
func changesOf(kinds string) []core.ChangeView {
	out := make([]core.ChangeView, 0, len(kinds))
	for i, k := range kinds {
		v := core.ChangeView{ID: fmt.Sprintf("c%05d", len(kinds)-i), HostID: "h1", Action: "demo.item.add", Version: 1,
			Class: core.ClassRevertible, State: core.StateCompleted, CreatedAt: changeTime, UpdatedAt: changeTime}
		switch k {
		case 'a':
			v.Action = "other.item.set"
		case 'b':
			v.HostID, v.Action, v.Class = "", "demo.secret.reset", core.ClassIrreversible
			v.Output = json.RawMessage(`{"secret":"[redacted]"}`)
		case 'u':
			v.HostID, v.Action, v.Class = "", "demo.gone.away", core.ClassCompensable
		}
		out = append(out, v)
	}
	return out
}

// changeStore answers Dispatcher.Changes from views as the engine does
// (newest first, before the cursor, at most the limit) and records each
// filter.
func changeStore(views []core.ChangeView, filters *[]core.ChangeFilter) func(context.Context, core.Principal, core.ChangeFilter) ([]core.ChangeView, error) {
	return func(_ context.Context, _ core.Principal, f core.ChangeFilter) ([]core.ChangeView, error) {
		*filters = append(*filters, f)
		out := []core.ChangeView{}
		for _, v := range views {
			if f.Before != "" && v.ID >= f.Before {
				continue
			}
			if len(out) == f.Limit {
				break
			}
			out = append(out, v)
		}
		return out, nil
	}
}

func listedIDs(t *testing.T, rec *httptest.ResponseRecorder) []string {
	t.Helper()
	var body struct {
		Changes []struct {
			ID string `json:"id"`
		} `json:"changes"`
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &body) != nil {
		t.Fatalf("list: %d %s", rec.Code, rec.Body)
	}
	ids := []string{}
	for _, c := range body.Changes {
		ids = append(ids, c.ID)
	}
	return ids
}

func TestChangeListHidesBreakGlass(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeRead)
	for _, tc := range []struct {
		name, query, kinds string
		want               []string
		filters            []core.ChangeFilter
	}{
		{"refilled page", "?limit=3&state=completed&hostId=h1", "vbuavbv", []string{"c00007", "c00004", "c00003"},
			[]core.ChangeFilter{{State: "completed", HostID: "h1", Limit: 3},
				{State: "completed", HostID: "h1", Before: "c00005", Limit: maxChangeLimit}}},
		{"nothing hidden", "?limit=2", "vvvb", []string{"c00004", "c00003"},
			[]core.ChangeFilter{{Limit: 2}}},
		{"end of the changes", "?limit=3", "bvbu", []string{"c00003"},
			[]core.ChangeFilter{{Limit: 3}, {Before: "c00002", Limit: maxChangeLimit}}},
		{"default limit", "", "v", []string{"c00001"},
			[]core.ChangeFilter{{Limit: defaultChangeLimit}}},
	} {
		var filters []core.ChangeFilter
		h.disp.ChangesFunc = changeStore(changesOf(tc.kinds), &filters)
		rec := h.serve(withToken(request("GET", "/api/v1/changes"+tc.query, ""), token))
		if got := listedIDs(t, rec); !slices.Equal(got, tc.want) {
			t.Errorf("%s: listed %v, want %v", tc.name, got, tc.want)
		}
		if !slices.Equal(filters, tc.filters) {
			t.Errorf("%s: fetched %+v, want %+v", tc.name, filters, tc.filters)
		}
		if strings.Contains(rec.Body.String(), "demo.secret.reset") || strings.Contains(rec.Body.String(), "demo.gone.away") {
			t.Errorf("%s: a hidden change is listed: %s", tc.name, rec.Body)
		}
	}

	// Further fetches are bounded: a run of hidden changes longer than they
	// cover ends the page short rather than scanning without end.
	var filters []core.ChangeFilter
	h.disp.ChangesFunc = changeStore(changesOf(strings.Repeat("b", 1+maxExtraChangeFetches*maxChangeLimit)+"v"), &filters)
	rec := h.serve(withToken(request("GET", "/api/v1/changes?limit=1", ""), token))
	if got := listedIDs(t, rec); len(got) != 0 || len(filters) != 1+maxExtraChangeFetches {
		t.Errorf("bounded refill: listed %v after %d fetches, want none after %d", got, len(filters), 1+maxExtraChangeFetches)
	}

	h.disp.ChangesFunc = func(context.Context, core.Principal, core.ChangeFilter) ([]core.ChangeView, error) {
		return nil, errcode.New(errcode.InvalidInput, "bad cursor")
	}
	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/changes", ""), token)), 422, errcode.InvalidInput)
}

func TestChangeShowHidesBreakGlass(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeAdmin)
	for id, visible := range map[string]bool{"c1": true, "c7": true, "cua": true, "cbg": false, "cunk": false} {
		rec := h.serve(withToken(request("GET", "/api/v1/changes/"+id, ""), token))
		if visible {
			if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"id":"`+id+`"`) {
				t.Errorf("%s: %d %s, want the change", id, rec.Code, rec.Body)
			}
			continue
		}
		p := decodeProblem(t, rec, 404, errcode.ChangeNotFound)
		if p.Detail != "no change "+id || strings.Contains(rec.Body.String(), "demo.") {
			t.Errorf("%s: problem %+v, want the not-found answer of a missing change", id, p)
		}
	}
	p := decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/changes/missing", ""), token)), 404, errcode.ChangeNotFound)
	if p.Detail != "no change missing" {
		t.Errorf("missing change: detail %q", p.Detail)
	}
}

// TestChangeOpsHideBreakGlass shows that confirm and revert answer a change
// the network hides exactly as a missing one, and act on nothing.
func TestChangeOpsHideBreakGlass(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeAdmin)
	for _, op := range []string{"confirm", "revert"} {
		for _, id := range []string{"cbg", "cunk", "missing"} {
			last = call{}
			rec := h.serve(withToken(request("POST", "/api/v1/changes/"+id+"/"+op, ""), token))
			p := decodeProblem(t, rec, 404, errcode.ChangeNotFound)
			if p.Detail != "no change "+id || strings.Contains(rec.Body.String(), "demo.") {
				t.Errorf("%s %s: problem %+v, want the not-found answer of a missing change", op, id, p)
			}
			if last.method != "Change" || last.changeID != id {
				t.Errorf("%s %s: last call %+v, want only the change lookup", op, id, last)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestChangeListHidesBreakGlass|TestChangeShowHidesBreakGlass|TestChangeOpsHideBreakGlass|TestChangeOpsAuthorizeTheChangesAction' ./internal/controller/api/`

Expected: FAIL — the build reports `internal/controller/api/actions_test.go:486:32: undefined: defaultChangeLimit` and `actions_test.go:505:67: undefined: maxExtraChangeFetches` (and two more lines naming `maxExtraChangeFetches`).

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/actions.go`, make these 5 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
)

// maxChangeLimit is the largest limit of GET /api/v1/changes; the engine
// lists no more.
const maxChangeLimit = 500

var changeStates = []string{
	core.StateApplying, core.StatePending, core.StateConfirmed, core.StateReverted,
```

with:

```go
)

// maxChangeLimit is the largest limit of GET /api/v1/changes; the engine
// lists no more. defaultChangeLimit is the limit when the query names none,
// the engine's default.
const (
	maxChangeLimit     = 500
	defaultChangeLimit = 100
)

// maxExtraChangeFetches bounds the further pages GET /api/v1/changes fetches
// to fill a page that hidden changes thinned out. Each asks for
// maxChangeLimit changes, so a page comes back short only after a run of
// more than 2,000 hidden changes.
const maxExtraChangeFetches = 4

var changeStates = []string{
	core.StateApplying, core.StatePending, core.StateConfirmed, core.StateReverted,
```

2. Replace:

```go
		writeProblem(w, s.log, err)
		return
	}
	views, err := s.cfg.Dispatcher.Changes(r.Context(), c.principal, f)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	out := changesResponse{Changes: make([]changeResponse, 0, len(views))}
	for _, v := range views {
		out.Changes = append(out.Changes, toChange(v))
	}
	writeJSON(w, s.log, http.StatusOK, out)
}
```

with:

```go
		writeProblem(w, s.log, err)
		return
	}
	if f.Limit == 0 {
		f.Limit = defaultChangeLimit
	}
	want := f.Limit
	out := changesResponse{Changes: make([]changeResponse, 0, want)}
	for range 1 + maxExtraChangeFetches {
		views, err := s.cfg.Dispatcher.Changes(r.Context(), c.principal, f)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		for _, v := range views {
			if len(out.Changes) == want {
				break
			}
			if s.networkVisible(v) {
				out.Changes = append(out.Changes, toChange(v))
			}
		}
		if len(out.Changes) == want || len(views) < f.Limit {
			break
		}
		f.Before, f.Limit = views[len(views)-1].ID, maxChangeLimit
	}
	writeJSON(w, s.log, http.StatusOK, out)
}
```

3. Replace:

```go
		writeProblem(w, s.log, err)
		return
	}
	v, err := s.cfg.Dispatcher.Change(r.Context(), c.principal, r.PathValue("id"))
	if err != nil {
		writeProblem(w, s.log, err)
		return
```

with:

```go
		writeProblem(w, s.log, err)
		return
	}
	id := r.PathValue("id")
	v, err := s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
	if err == nil && !s.networkVisible(v) {
		err = errHiddenChange(id)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
```

4. Replace:

```go
	writeJSON(w, s.log, http.StatusOK, toChange(v))
}

// authorizeChange applies to a confirm or revert the checks of the change's
// own action, never of the route: a break-glass change is refused, and the
// scope is RequiredScope of the action's class and side. A controller-side
// change whose action is no longer registered is refused, because whether
// it was break-glass cannot be known.
func (s *Server) authorizeChange(p core.Principal, v core.ChangeView) error {
	class, side := v.Class, core.SideController
	if v.HostID != "" {
		side = core.SideAgent
	}
	def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version})
	switch {
	case ok && def.Spec.BreakGlass:
		return errcode.New(errcode.Forbidden, "changes of break-glass actions are served only on the controller socket")
	case ok:
		class, side = def.Spec.Class, def.Spec.Side
	case side == core.SideController:
		return errcode.New(errcode.Forbidden, fmt.Sprintf("action %s/v%d is not registered", v.Action, v.Version))
	}
	return authorize(p, RequiredScope(class, side))
}

// changeOp serves confirm or revert. It reads the change first, as the
// caller, and authorizes against the change's action.
func (s *Server) changeOp(confirm bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
```

with:

```go
	writeJSON(w, s.log, http.StatusOK, toChange(v))
}

// networkVisible reports whether the network listeners show change v. They
// hide changes of break-glass actions, whose inputs and outputs are the
// controller socket's alone (owner decision), and controller-side changes
// whose action is no longer registered, because whether it was break-glass
// cannot be known. Show, confirm and revert answer a hidden change with
// errHiddenChange.
func (s *Server) networkVisible(v core.ChangeView) bool {
	def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version})
	if ok {
		return !def.Spec.BreakGlass
	}
	return v.HostID != ""
}

// errHiddenChange is the answer to a change the network hides: the lifecycle
// engine's own answer for a missing change, so the two cannot be told apart.
func errHiddenChange(id string) error {
	return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", id))
}

// authorizeChange applies to a confirm or revert the scope of the change's
// own action, never of the route: RequiredScope of its class and side. v
// must be networkVisible, so an unregistered action is agent-side and its
// stored class decides.
func (s *Server) authorizeChange(p core.Principal, v core.ChangeView) error {
	class, side := v.Class, core.SideAgent
	if def, ok := s.cfg.Registry.Action(core.ActionRef{Name: v.Action, Version: v.Version}); ok {
		class, side = def.Spec.Class, def.Spec.Side
	}
	return authorize(p, RequiredScope(class, side))
}

// changeOp serves confirm or revert. It reads the change first, as the
// caller, answers a change the network hides as missing, and authorizes
// against the change's action.
func (s *Server) changeOp(confirm bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
```

5. Replace:

```go
		var v core.ChangeView
		if err == nil {
			v, err = s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
		}
		if err == nil {
			err = s.authorizeChange(c.principal, v)
```

with:

```go
		var v core.ChangeView
		if err == nil {
			v, err = s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
		}
		if err == nil && !s.networkVisible(v) {
			err = errHiddenChange(id)
		}
		if err == nil {
			err = s.authorizeChange(c.principal, v)
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestChange' ./internal/controller/api/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/actions.go internal/controller/api/actions_test.go
git commit -m "fix(api): hide break-glass changes on the network"
```

### Task 33: Late-bound listener state source

**Files:**
- Modify: `internal/controller/api/server.go`
- Create: `internal/controller/api/listeners_test.go`

**Interfaces:**
- Consumes: the merged `api.Server` and `api.New` (plan 3 Contracts › Task 19); test helper `newHarness`.
- Produces: Contracts › Task 33 (`ListenerState`, `SetListenerState`, `listenerState`, `listenersNotStarted`).

**Items:** 69

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Spec section 13's UI footer reports the listener state, but `listen.Start` needs `srv.Handler()` before the listener service exists, so the API cannot take the state in `Config`. `Server` gains an atomic, late-bound source: `SetListenerState(func() ListenerState)` may be called at any time, also while requests are served, and a nil source reverts to `The listeners are not started yet.`. The tests check the default, a set source, a reset, and a concurrent set and read under the race detector.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/listeners_test.go`:

```go
package api

import (
	"sync"
	"testing"
)

func TestListenerState(t *testing.T) {
	h := newHarness(t)
	notStarted := ListenerState{Message: "The listeners are not started yet."}
	if got := h.srv.listenerState(); got != notStarted {
		t.Errorf("before a source is set: %+v, want %+v", got, notStarted)
	}
	bound := ListenerState{TailscaleBound: true, Message: "Serving on Tailscale: https://100.64.0.1:8443."}
	h.srv.SetListenerState(func() ListenerState { return bound })
	if got := h.srv.listenerState(); got != bound {
		t.Errorf("after SetListenerState: %+v, want %+v", got, bound)
	}
	h.srv.SetListenerState(nil)
	if got := h.srv.listenerState(); got != notStarted {
		t.Errorf("after SetListenerState(nil): %+v, want %+v", got, notStarted)
	}
}

// TestListenerStateConcurrent sets the source while requests read it; the
// race detector checks the hand-over.
func TestListenerStateConcurrent(t *testing.T) {
	h := newHarness(t)
	loopback := ListenerState{Message: "No Tailscale address found; serving loopback only."}
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 100 {
			h.srv.SetListenerState(func() ListenerState { return loopback })
		}
	})
	for range 4 {
		wg.Go(func() {
			for range 100 {
				if got := h.srv.listenerState(); got.TailscaleBound || got.Message == "" {
					t.Errorf("read %+v during SetListenerState", got)
					return
				}
			}
		})
	}
	wg.Wait()
	if got := h.srv.listenerState(); got != loopback {
		t.Errorf("after the writes: %+v, want %+v", got, loopback)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestListenerState' ./internal/controller/api/`

Expected: FAIL — the build reports `internal/controller/api/listeners_test.go:10:16: undefined: ListenerState` and `listeners_test.go:11:18: h.srv.listenerState undefined (type *Server has no field or method listenerState)`, followed by more of the same.

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/server.go`, make these 2 edits in order; each block to replace occurs once in the file at that point.

1. Replace:

```go
	"net"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
```

with:

```go
	"net"
	"net/http"
	"strings"
	"sync/atomic"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
```

2. Replace:

```go

// Server serves the API. Its handlers are safe for concurrent use.
type Server struct {
	cfg     Config
	log     *slog.Logger
	mux     *http.ServeMux
	handler http.Handler
}

// New returns a server for cfg.
```

with:

```go

// Server serves the API. Its handlers are safe for concurrent use.
type Server struct {
	cfg       Config
	log       *slog.Logger
	mux       *http.ServeMux
	handler   http.Handler
	listeners atomic.Pointer[func() ListenerState]
}

// ListenerState is the listener state the UI footer shows (spec section 13):
// whether a Tailscale listener is bound, and the listener service's message.
type ListenerState struct {
	TailscaleBound bool   `json:"tailscaleBound"`
	Message        string `json:"message"`
}

const listenersNotStarted = "The listeners are not started yet."

// SetListenerState sets the source of the listener state the API reports.
// The listener service is started with Handler, so it exists only after New;
// the source is therefore set late, and may be set while requests are
// served. A nil source reports that the listeners are not started.
func (s *Server) SetListenerState(state func() ListenerState) {
	if state == nil {
		s.listeners.Store(nil)
		return
	}
	s.listeners.Store(&state)
}

// listenerState reads the source SetListenerState set.
func (s *Server) listenerState() ListenerState {
	if state := s.listeners.Load(); state != nil {
		return (*state)()
	}
	return ListenerState{Message: listenersNotStarted}
}

// New returns a server for cfg.
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestListenerState' ./internal/controller/api/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/server.go internal/controller/api/listeners_test.go
git commit -m "feat(api): add a late-bound listener state source"
```

### Task 34: Listener state route for the UI footer

**Files:**
- Modify: `internal/controller/api/meta.go`
- Modify: `internal/controller/api/routes.go`
- Test: `internal/controller/api/meta_test.go`

**Interfaces:**
- Consumes: Contracts › Task 33 (`ListenerState`, `SetListenerState`, `listenerState`); the merged `networkRoutes`, `Routes`, `authorize`, `writeJSON`, `writeProblem` (plan 3 Contracts › Tasks 11, 16, 19, 22); test helpers `newHarness`, `login`, `token`, `request`, `withToken`, `withSession`, `decodeProblem`, `noCredentials`, `tokenSecret`.
- Produces: Contracts › Task 34 (`GET /api/v1/controller/listeners`, `handleListeners`, the final `Routes()`).

**Items:** 69

**Depends on:** Task 33

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

A read-scope route `GET /api/v1/controller/listeners` answers `{tailscaleBound, message}` from the source Task 33 added. It is a hand-written network route, so `Routes()` lists it (last), the OpenAPI document and `CheckOpenAPI` cover it, and the deny-by-default matrix (`TestDenyByDefault` walks `networkRoutes()`) refuses it without credentials. The test checks the answer before and after the source is set, for a token and a session, the 401 without credentials, the 405 with `Allow: GET, HEAD`, and that no credential leaks; `TestRoutes` and `TestOpenAPIRoute` gain the route.

- [ ] **Step 1: Write the failing test**

Replace the whole content of `internal/controller/api/meta_test.go` with:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

func TestHealthz(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	readToken := h.token(core.ScopeRead)
	var recs []*httptest.ResponseRecorder
	detail := fmt.Sprintf(`{"status":"ok","version":%q,"commit":%q,"schemaVersion":3}`, buildinfo.Version, buildinfo.Commit)
	for _, tc := range []struct {
		name string
		body string
		r    func() *http.Request
	}{
		{"no credentials", `{"status":"ok"}`, func() *http.Request { return request("GET", "/healthz", "") }},
		{"invalid bearer", `{"status":"ok"}`, func() *http.Request { return withToken(request("GET", "/healthz", ""), "dpt_bogus") }},
		{"invalid session", `{"status":"ok"}`, func() *http.Request { return withSession(request("GET", "/healthz", ""), "bogus", "") }},
		{"read token", detail, func() *http.Request { return withToken(request("GET", "/healthz", ""), readToken) }},
		{"session", detail, func() *http.Request { return withSession(request("GET", "/healthz", ""), session, "") }},
	} {
		rec := h.serve(tc.r())
		recs = append(recs, rec)
		if rec.Code != 200 || rec.Body.String() != tc.body || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s %v, want 200 %s", tc.name, rec.Code, rec.Body, rec.Header(), tc.body)
		}
	}
	head := h.serve(request("HEAD", "/healthz", ""))
	recs = append(recs, head)
	if head.Code != 200 {
		t.Errorf("HEAD /healthz: %d", head.Code)
	}

	h.srv.cfg.SchemaVersion = func(context.Context) (int, error) { return 0, errors.New("database is locked") }
	failing := h.serve(withSession(request("GET", "/healthz", ""), session, ""))
	recs = append(recs, failing)
	decodeProblem(t, failing, 500, errcode.Internal)
	public := h.serve(request("GET", "/healthz", ""))
	recs = append(recs, public)
	if public.Code != 200 || public.Body.String() != `{"status":"ok"}` {
		t.Errorf("public health with a failing database: %d %s", public.Code, public.Body)
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf, "read token secret": tokenSecret(readToken),
		"invalid bearer token": "dpt_bogus", "invalid session id": "bogus"})
}

func TestOpenAPIRoute(t *testing.T) {
	h := newHarness(t)
	unauthenticated := h.serve(request("GET", "/api/v1/openapi.json", ""))
	decodeProblem(t, unauthenticated, 401, errcode.AuthRequired)

	readToken := h.token(core.ScopeRead)
	rec := h.serve(withToken(request("GET", "/api/v1/openapi.json", ""), readToken))
	noCredentials(t, h, []*httptest.ResponseRecorder{unauthenticated, rec}, map[string]string{"read token secret": tokenSecret(readToken)})
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("%d %v", rec.Code, rec.Header())
	}
	var doc struct {
		OpenAPI string                    `json:"openapi"`
		Paths   map[string]map[string]any `json:"paths"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &doc); err != nil || doc.OpenAPI != "3.1.0" {
		t.Fatalf("document: %v %q", err, doc.OpenAPI)
	}
	for path, method := range map[string]string{
		"/healthz":                                           "get",
		"/api/v1/auth/login":                                 "post",
		"/api/v1/openapi.json":                               "get",
		"/api/v1/controller/listeners":                       "get",
		"/api/v1/changes/{id}/confirm":                       "post",
		"/api/v1/hosts/{host}/actions/demo.item.add/v1/plan": "post",
		"/api/v1/actions/demo.item.list/v1/read":             "post",
		"/api/v1/hosts/{host}/resources/demo/status":         "get",
	} {
		if _, ok := doc.Paths[path][method]; !ok {
			t.Errorf("document lacks %s %s", method, path)
		}
	}
	for _, path := range []string{"/api/v1/actions/demo.secret.reset/v1/plan", "/api/v1/actions/demo.secret.show/v1/read",
		"/api/v1/hosts/{host}/actions/{name}/{version}/plan"} {
		if _, ok := doc.Paths[path]; ok {
			t.Errorf("document has %s", path)
		}
	}
}

func TestRoutes(t *testing.T) {
	want := []schema.Route{
		{Method: "POST", Path: "/api/v1/auth/login", Public: true},
		{Method: "POST", Path: "/api/v1/auth/logout"},
		{Method: "GET", Path: "/api/v1/auth/session"},
		{Method: "GET", Path: "/api/v1/changes"},
		{Method: "GET", Path: "/api/v1/changes/{id}"},
		{Method: "POST", Path: "/api/v1/changes/{id}/confirm"},
		{Method: "POST", Path: "/api/v1/changes/{id}/revert"},
		{Method: "GET", Path: "/healthz", Public: true},
		{Method: "GET", Path: "/api/v1/openapi.json"},
		{Method: "GET", Path: "/api/v1/controller/listeners"},
	}
	got := Routes()
	if len(got) != len(want) {
		t.Fatalf("Routes() = %+v, want %d routes", got, len(want))
	}
	for i, r := range got {
		if r.Method != want[i].Method || r.Path != want[i].Path || r.Public != want[i].Public || r.Summary == "" {
			t.Errorf("route %d = %+v, want %+v with a summary", i, r, want[i])
		}
		if r.Public != (routeAccess(r.Method, r.Path) != accessAuthenticated) {
			t.Errorf("%s %s: Public %v disagrees with routeAccess", r.Method, r.Path, r.Public)
		}
	}
}

func TestListenersRoute(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	readToken := h.token(core.ScopeRead)
	const path = "/api/v1/controller/listeners"
	var recs []*httptest.ResponseRecorder

	rec := h.serve(request("GET", path, ""))
	recs = append(recs, rec)
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	rec = h.serve(withToken(request("GET", path, ""), readToken))
	recs = append(recs, rec)
	if want := `{"tailscaleBound":false,"message":"The listeners are not started yet."}`; rec.Code != 200 || rec.Body.String() != want ||
		rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("before the listeners start: %d %s %v, want 200 %s", rec.Code, rec.Body, rec.Header(), want)
	}

	h.srv.SetListenerState(func() ListenerState {
		return ListenerState{TailscaleBound: true, Message: "Serving on Tailscale: https://100.64.0.1:8443."}
	})
	want := `{"tailscaleBound":true,"message":"Serving on Tailscale: https://100.64.0.1:8443."}`
	for name, r := range map[string]*http.Request{
		"read token": withToken(request("GET", path, ""), readToken),
		"session":    withSession(request("GET", path, ""), session, ""),
	} {
		rec := h.serve(r)
		recs = append(recs, rec)
		if rec.Code != 200 || rec.Body.String() != want {
			t.Errorf("%s: %d %s, want 200 %s", name, rec.Code, rec.Body, want)
		}
	}

	rec = h.serve(withToken(request("POST", path, ""), readToken))
	recs = append(recs, rec)
	decodeProblem(t, rec, http.StatusMethodNotAllowed, errcode.MethodNotAllowed)
	if rec.Header().Get("Allow") != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", rec.Header().Get("Allow"))
	}
	noCredentials(t, h, recs, map[string]string{"session id": session, "CSRF token": csrf, "read token secret": tokenSecret(readToken)})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRoutes|TestOpenAPIRoute|TestListenersRoute' ./internal/controller/api/`

Expected: FAIL — `--- FAIL: TestOpenAPIRoute` with `meta_test.go:89: document lacks get /api/v1/controller/listeners`; `--- FAIL: TestRoutes` with `meta_test.go:115: Routes() = [… 9 routes …], want 10 routes`; `--- FAIL: TestListenersRoute` with `meta_test.go:142: before the listeners start: 404 {… "code":"not_found"} …`, two `meta_test.go:156` lines (404 for the read token and the session) and `meta_test.go:162: status = 404, want 405`.

- [ ] **Step 3: Write the implementation**

Replace the whole content of `internal/controller/api/meta.go` with:

```go
package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

type healthStatus struct {
	Status string `json:"status"`
}

type healthDetail struct {
	Status        string `json:"status"`
	Version       string `json:"version"`
	Commit        string `json:"commit"`
	SchemaVersion int    `json:"schemaVersion"`
}

// handleHealthz answers the status alone, or the full detail for a caller
// with valid credentials (spec section 12). The authentication middleware
// drops invalid credentials on this route instead of refusing them.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	c, ok := callerFrom(r.Context())
	if !ok || authorize(c.principal, core.ScopeRead) != nil {
		writeJSON(w, s.log, http.StatusOK, healthStatus{Status: "ok"})
		return
	}
	s.writeHealthDetail(w, r)
}

// writeHealthDetail answers the version, the commit and the controller
// database's schema version.
func (s *Server) writeHealthDetail(w http.ResponseWriter, r *http.Request) {
	n, err := s.cfg.SchemaVersion(r.Context())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, healthDetail{Status: "ok", Version: buildinfo.Version, Commit: buildinfo.Commit, SchemaVersion: n})
}

// handleOpenAPI answers the OpenAPI document (spec section 7), built from
// the registry and Routes on each request; it is rarely fetched.
func (s *Server) handleOpenAPI(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	doc, err := schema.OpenAPI(s.cfg.Registry, Routes())
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(doc)
}

// handleListeners answers the listener state for the UI footer (spec section
// 13) from the source SetListenerState set.
func (s *Server) handleListeners(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, s.listenerState())
}
```

Replace the whole content of `internal/controller/api/routes.go` with:

```go
package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// route is one route of the network mux. A public route must also be public
// in routeAccess, which the authentication middleware consults.
type route struct {
	method, path, summary string
	public                bool
	// perAction marks a route serving every registry action or resource of
	// one kind; schema.OpenAPI documents those from the registry, one
	// operation each, so Routes leaves them out.
	perAction bool
	handler   http.HandlerFunc
}

// networkRoutes lists the network mux's routes.
func (s *Server) networkRoutes() []route {
	const hostActions, controllerActions = "/api/v1/hosts/{host}/actions/{name}/{version}/", "/api/v1/actions/{name}/{version}/"
	return []route{
		{method: http.MethodPost, path: loginPath, summary: "Log in with username, password and TOTP code", public: true, handler: s.handleLogin},
		{method: http.MethodPost, path: "/api/v1/auth/logout", summary: "Log out and clear the session cookie", handler: s.handleLogout},
		{method: http.MethodGet, path: "/api/v1/auth/session", summary: "Describe the calling session or token", handler: s.handleSession},
		{method: http.MethodGet, path: "/api/v1/hosts/{host}/resources/{module}/{resource}", perAction: true, handler: s.handleResource},
		{method: http.MethodPost, path: hostActions + opPlan, perAction: true, handler: s.actionHandler(core.SideAgent, opPlan)},
		{method: http.MethodPost, path: hostActions + opApply, perAction: true, handler: s.actionHandler(core.SideAgent, opApply)},
		{method: http.MethodPost, path: hostActions + opRead, perAction: true, handler: s.actionHandler(core.SideAgent, opRead)},
		{method: http.MethodPost, path: controllerActions + opPlan, perAction: true, handler: s.actionHandler(core.SideController, opPlan)},
		{method: http.MethodPost, path: controllerActions + opApply, perAction: true, handler: s.actionHandler(core.SideController, opApply)},
		{method: http.MethodPost, path: controllerActions + opRead, perAction: true, handler: s.actionHandler(core.SideController, opRead)},
		{method: http.MethodGet, path: "/api/v1/changes", summary: "List changes, newest first", handler: s.handleChanges},
		{method: http.MethodGet, path: "/api/v1/changes/{id}", summary: "Show a change", handler: s.handleChange},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/confirm", summary: "Confirm a pending change", handler: s.changeOp(true)},
		{method: http.MethodPost, path: "/api/v1/changes/{id}/revert", summary: "Revert a pending change", handler: s.changeOp(false)},
		{method: http.MethodGet, path: healthzPath, summary: "Health; with credentials also version, commit and schema version",
			public: true, handler: s.handleHealthz},
		{method: http.MethodGet, path: "/api/v1/openapi.json", summary: "This OpenAPI document", handler: s.handleOpenAPI},
		{method: http.MethodGet, path: "/api/v1/controller/listeners", summary: "Listener state for the UI footer: Tailscale bound, and a message",
			handler: s.handleListeners},
	}
}

// Routes returns the hand-written network routes that schema.OpenAPI
// documents next to the registry's actions and resources.
func Routes() []schema.Route {
	out := []schema.Route{}
	for _, rt := range (&Server{}).networkRoutes() {
		if !rt.perAction {
			out = append(out, schema.Route{Method: rt.method, Path: rt.path, Summary: rt.summary, Public: rt.public})
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRoutes|TestOpenAPIRoute|TestListenersRoute|TestDenyByDefault|TestCheckOpenAPI|TestUnroutedAfterAuthentication' ./internal/controller/api/` — Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 900 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing; every `go test` line starts with `ok` (or `?` for packages without test files), the race run included; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/meta.go internal/controller/api/routes.go internal/controller/api/meta_test.go
git commit -m "feat(api): serve the listener state for the footer"
```

### Task 35: Register hostinfo and commit the schema snapshots

**Files:**
- Modify: `internal/modules/modules.go`
- Create (generated by `make schemas`): `schemas/core.audit.list/v1.json`, `schemas/core.host.list/v1.json`, `schemas/core.module.disable/v1.json`, `schemas/core.module.enable/v1.json`, `schemas/core.module.list/v1.json`, `schemas/core.token.create/v1.json`, `schemas/core.token.list/v1.json`, `schemas/core.token.revoke/v1.json`, `schemas/core.user.activate/v1.json`, `schemas/core.user.create/v1.json`, `schemas/core.user.delete/v1.json`, `schemas/core.user.list/v1.json`, `schemas/core.user.reset-password/v1.json`, `schemas/core.user.reset-totp/v1.json`, `schemas/hostinfo.summary/v1.json`
- Create: `schemas/released/.gitkeep` (empty)
- Test: `internal/modules/modules_test.go`

**Interfaces:**
- Consumes: `hostinfo.Module()` (plan 2); `registry.Build`, `registry.BuildWith` (plan 1); `schema.Snapshots`, `schema.CheckReleased` (plan 2); `coreactions.Module`, `coreactions.Deps` (plan 3); `auth.Service`, `coretest.MemAudit`; the Makefile target `schemas` (`go run ./internal/core/schema/schemagen -out schemas`).
- Produces: Contracts › Task 35 (`modules.All` with hostinfo; `schemas/`; test helpers `snapshotDir`, `releasedDir`, `built`, `snapshots`).

**Items:** 1, 2, 9, 71

**Depends on:** Task 9

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

hostinfo joins the registry through its one line in `modules.All` (R5), and the schema snapshots of every action and resource are committed under `schemas/` (spec section 7). The tests prove hostinfo is the registered module, regenerate every snapshot in memory and fail on a missing, stale or orphaned file, run `schema.CheckReleased` over `schemas/released/`, and show that the server's registry (built with real dependencies) has the same snapshots as `registry.Build()`. The snapshot files are generated by `make schemas`, not typed: they are deterministic output of the registry.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/modules_test.go`:

```go
package modules_test

import (
	"bytes"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/registry"
)

// snapshotDir is the committed schemas/ directory at the repository root.
var snapshotDir = filepath.Join("..", "..", "schemas")

// releasedDir holds the frozen snapshots of released tags (ADR 0002).
var releasedDir = filepath.Join(snapshotDir, "released")

func built(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	return reg
}

func snapshots(t *testing.T, reg *core.Registry) map[string][]byte {
	t.Helper()
	files, err := schema.Snapshots(reg)
	if err != nil {
		t.Fatalf("schema.Snapshots: %v", err)
	}
	return files
}

// TestAllRegistersHostinfo is register row 1: hostinfo is compiled in by
// its one line in All, and the registry serves its resource.
func TestAllRegistersHostinfo(t *testing.T) {
	var ids []string
	for _, m := range modules.All() {
		ids = append(ids, m.Manifest().ID)
	}
	if !slices.Equal(ids, []string{"hostinfo"}) {
		t.Fatalf("modules.All() = %v, want [hostinfo]", ids)
	}
	if _, ok := built(t).Resource("hostinfo.summary"); !ok {
		t.Error("the registry has no hostinfo.summary resource")
	}
}

// TestSnapshotsCurrent regenerates every snapshot in memory and fails when a
// committed file under schemas/ differs, is missing, or belongs to no
// registered action or resource (spec section 7). make schemas fixes all
// three.
func TestSnapshotsCurrent(t *testing.T) {
	files := snapshots(t, built(t))
	for _, name := range slices.Sorted(maps.Keys(files)) {
		got, err := os.ReadFile(filepath.Join(snapshotDir, filepath.FromSlash(name))) //nolint:gosec // G304: a snapshot path under the committed schemas/ directory
		switch {
		case errors.Is(err, fs.ErrNotExist):
			t.Errorf("schemas/%s is missing; run make schemas", name)
		case err != nil:
			t.Errorf("schemas/%s: %v", name, err)
		case !bytes.Equal(got, files[name]):
			t.Errorf("schemas/%s is out of date; run make schemas", name)
		}
	}
	err := filepath.WalkDir(snapshotDir, func(path string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case d.IsDir() && path == releasedDir:
			return fs.SkipDir
		case d.IsDir() || filepath.Ext(path) != ".json":
			return nil
		}
		rel, err := filepath.Rel(snapshotDir, path)
		if err != nil {
			return err
		}
		if _, ok := files[filepath.ToSlash(rel)]; !ok {
			t.Errorf("schemas/%s belongs to no registered action or resource; run make schemas", filepath.ToSlash(rel))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk schemas/: %v", err)
	}
}

// TestReleasedSnapshotsCompatible runs the compatibility check of ADR 0002
// for every snapshot under schemas/released/<tag>/; until the first release
// there is none.
func TestReleasedSnapshotsCompatible(t *testing.T) {
	if err := schema.CheckReleased(releasedDir, snapshots(t, built(t))); err != nil {
		t.Errorf("released snapshots: %v", err)
	}
}

// TestServerRegistryHasTheSameShape shows that the registry the server
// builds with its services has the actions, resources and snapshots of
// registry.Build(), which the CLI, the schema generator and these tests use.
func TestServerRegistryHasTheSameShape(t *testing.T) {
	server, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Auth: new(auth.Service), Audit: &coretest.MemAudit{}, Modules: modules.All(),
	}))
	if err != nil {
		t.Fatalf("registry.BuildWith: %v", err)
	}
	want, got := snapshots(t, built(t)), snapshots(t, server)
	if !slices.Equal(slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want))) {
		t.Fatalf("server registry snapshots %v, want %v", slices.Sorted(maps.Keys(got)), slices.Sorted(maps.Keys(want)))
	}
	for name, b := range want {
		if !bytes.Equal(got[name], b) {
			t.Errorf("server registry snapshot %s differs from registry.Build()'s", name)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAllRegistersHostinfo|TestSnapshotsCurrent|TestReleasedSnapshotsCompatible|TestServerRegistryHasTheSameShape' ./internal/modules/`
Expected: FAIL:

```text
--- FAIL: TestAllRegistersHostinfo (0.00s)
    modules_test.go:54: modules.All() = [], want [hostinfo]
--- FAIL: TestSnapshotsCurrent (0.00s)
    modules_test.go:71: schemas/core.audit.list/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.host.list/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.module.disable/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.module.enable/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.module.list/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.token.create/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.token.list/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.token.revoke/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.activate/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.create/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.delete/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.list/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.reset-password/v1.json is missing; run make schemas
    modules_test.go:71: schemas/core.user.reset-totp/v1.json is missing; run make schemas
    modules_test.go:97: walk schemas/: lstat ../../schemas: no such file or directory
FAIL
FAIL	github.com/darkraise/darkpanel/internal/modules	<time>
FAIL
```

- [ ] **Step 3: Write the implementation**

Replace the whole content of `internal/modules/modules.go` with:

```go
// Package modules lists the compiled-in modules. Adding a module adds one
// line to All and changes nothing else in the core (R5).
package modules

import (
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/hostinfo"
)

// All returns every compiled-in module in registration order.
func All() []core.Module {
	return []core.Module{
		hostinfo.Module(),
	}
}
```

Then generate the snapshots and keep the released directory:

```bash
GOTOOLCHAIN=local timeout 300 make schemas
mkdir -p schemas/released && touch schemas/released/.gitkeep
find schemas -type f | sort
```

Expected: the fifteen `v1.json` files listed under **Files** and `schemas/released/.gitkeep`.

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/modules/` — Expected: PASS
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/modules/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/modules/modules.go internal/modules/modules_test.go schemas
git commit -m "feat(modules): register hostinfo, commit schemas"
```

### Task 36: Remove the dependency pin package

**Files:**
- Delete: `internal/deps/deps.go`

**Interfaces:**
- Consumes: Part B obligation 3 (`internal/cli` imports cobra, `golang.org/x/term` and qrterminal).
- Produces: nothing; `go.mod` and `go.sum` unchanged.

**Depends on:** Task 29

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 0 - risk 1 = 1

`internal/deps/deps.go` (build tag `deps`) kept every M1 dependency in `go.mod` until code imported it (spec "Parallel decomposition", P0). Every one is imported now, so the file goes and `go mod tidy -diff` stays empty: `golang.org/x/crypto/ssh` (for the harness) is in the same module as argon2, which `internal/controller/auth` imports.

- [ ] **Step 1: Remove the package**

```bash
git rm internal/deps/deps.go
```

- [ ] **Step 2: Run the checks**

Run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/modules/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

The last check is the one this task can break: an empty diff means `go.mod` and `go.sum` stay as they are.

- [ ] **Step 3: Commit**

```bash
git commit -m "chore(deps): remove the dependency pin package"
```

### Task 37: Server storage: paths, keys, lock, stores and local host

**Files:**
- Create: `internal/controller/server/server.go`
- Test: `internal/controller/server/server_test.go`

**Interfaces:**
- Consumes: `paths.Paths` (`MkdirAll`, `ControllerDB`, `AgentDB`, `ControllerBackups`, `Under`); `secret.LoadOrCreateKey`, `secret.NewSealer`; `store.Open`, `store.Migrate`, `store.MigrateOptions`, `store.MigrationSet`, `store.CoreMigrations`, `store.SchemaVersion`, `store.SideController`; `modules.All` (Task 35 adds hostinfo; this task needs only the function); `buildinfo.Version`; `core.Clock`, `core.SystemClock`, `core.SideController`; `listen.Spec`, `listen.BindFunc`, `listen.InterfaceAddrs`, `listen.Endpoint`, `listen.ParseSpecs` (plan 3); `auth.Params` (plan 3); `coretest.NewFakeClock`; `golang.org/x/sys/unix` (`Flock`), `github.com/oklog/ulid/v2`.
- Produces: Contracts › Task 37 (`Options`, `Server`, `Start`, `Shutdown`, `LocalHostID`, `SocketPath`, `LockName`, `BackupsKept`, `ShutdownTimeout`, `onShutdown`; test helpers `t0`, `testHostname`, `testParams`, `localBind`, `testOptions`, `start`, `openDB`, `wantMode`).

**Items:** 52, 69, 74, 76

**Depends on:** Task 5, Task 12

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The first step of `serve`'s startup (spec section 1 and the P14 row): create the directories, refuse a controller socket directory another user could write (register row 69, obligation 4), load or create both keys from `paths.Paths` only (row 52), take the single-instance `flock` before any database or the socket is touched (row 76), open both stores, migrate the controller database with its backups, and record the local host on the first start. Every acquisition registers its stop, and `Shutdown` runs them in reverse, once, serialised (row 74). The tests check modes, the backup, the local host row across a restart, the hostname fallback, the lock refusal and its release, the directory refusals (including a directory owned by another uid when run as root), the cleanup of a failed start, and concurrent `Shutdown` calls under the race detector.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/server_test.go`:

```go
package server

import (
	"context"
	"errors"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)

// t0 is the fake clock's start.
var t0 = time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)

// testHostname is the hostname the tests report.
const testHostname = "test-host"

// testParams are cheap argon2id parameters; production uses
// auth.DefaultParams.
var testParams = auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// localBind maps every endpoint to 127.0.0.1 port 0, because listen values
// cannot name port 0.
func localBind(ctx context.Context, _ listen.Endpoint) (net.Listener, error) {
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

// testOptions are options over temporary paths, with the socket in a short
// directory of its own (Unix socket paths are limited to 108 bytes).
func testOptions(t *testing.T) Options {
	t.Helper()
	run, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(run) })
	p := paths.Under(t.TempDir())
	p.ControllerSocket = filepath.Join(run, "controller.sock")
	specs, err := listen.ParseSpecs([]string{"loopback"})
	if err != nil {
		t.Fatal(err)
	}
	return Options{
		Paths:      p,
		Listen:     specs,
		Dev:        true,
		Clock:      coretest.NewFakeClock(t0),
		Hostname:   func() (string, error) { return testHostname, nil },
		Bind:       localBind,
		AuthParams: testParams,
	}
}

// start starts a server and shuts it down when the test ends.
func start(t *testing.T, opts Options) *Server {
	t.Helper()
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(func() {
		if err := s.Shutdown(context.Background()); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	})
	return s
}

// openDB opens a database file of a stopped server.
func openDB(t *testing.T, path string) *store.DB {
	t.Helper()
	db, err := store.Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func wantMode(t *testing.T, path string, want fs.FileMode) {
	t.Helper()
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Mode().Perm(); got != want {
		t.Errorf("%s has mode %v, want %v", path, got, want)
	}
}

func TestStartPreparesStorage(t *testing.T) {
	opts := testOptions(t)
	p := opts.Paths
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := s.LocalHostID()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, dir := range []string{p.ControllerDir, p.AgentDir, filepath.Dir(p.ControllerKey), filepath.Dir(p.ControllerSocket)} {
		wantMode(t, dir, 0o700)
	}
	wantMode(t, p.ControllerKey, 0o600)
	wantMode(t, p.AgentKey, 0o600)
	wantMode(t, filepath.Join(filepath.Dir(p.ControllerSocket), LockName), 0o600)
	backups, err := os.ReadDir(p.ControllerBackups())
	if err != nil || len(backups) != 1 || !strings.HasPrefix(backups[0].Name(), "none-dev-") {
		t.Errorf("controller backups = %v (%v), want one none-dev-<time> backup", backups, err)
	}

	db := openDB(t, p.ControllerDB())
	ctx := context.Background()
	var name, metaID string
	var local int
	if err := db.Read().QueryRowContext(ctx, `SELECT name, is_local FROM core_hosts WHERE id = ?`, id).Scan(&name, &local); err != nil {
		t.Fatalf("local host %q: %v", id, err)
	}
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'local_host_id'`).Scan(&metaID); err != nil {
		t.Fatal(err)
	}
	if name != testHostname || local != 1 || metaID != id || len(id) != 26 {
		t.Errorf("local host %q named %q, is_local %d, core_meta local_host_id %q; want a ULID named %s, local, in core_meta",
			id, name, local, metaID, testHostname)
	}
	if v, err := store.SchemaVersion(ctx, db, store.SideController); err != nil || v < 4 {
		t.Errorf("controller schema version %d (%v), want the core migrations applied", v, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	again := start(t, opts)
	if again.LocalHostID() != id {
		t.Errorf("restart: local host id %q, want %q", again.LocalHostID(), id)
	}
	var hosts int
	if err := again.controllerDB.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_hosts`).Scan(&hosts); err != nil || hosts != 1 {
		t.Errorf("restart: %d hosts (%v), want 1", hosts, err)
	}
}

func TestLocalHostNameFallsBack(t *testing.T) {
	opts := testOptions(t)
	opts.Hostname = func() (string, error) { return "", errors.New("no hostname") }
	s := start(t, opts)
	var name string
	if err := s.controllerDB.Read().QueryRowContext(context.Background(),
		`SELECT name FROM core_hosts WHERE id = ?`, s.LocalHostID()).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if name != "localhost" {
		t.Errorf("local host name %q, want localhost when the hostname is unknown", name)
	}
}

// TestOneServerAtATime is register row 76: a second serve on the same paths
// fails on the lock before it touches the databases or the socket.
func TestOneServerAtATime(t *testing.T) {
	opts := testOptions(t)
	first, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	_, err = Start(context.Background(), opts)
	if err == nil || !strings.Contains(err.Error(), "another darkpanel serve holds") {
		t.Fatalf("second Start: %v, want the lock refusal", err)
	}
	if err := first.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestSocketDirectoryMustBePrivate is register row 69's obligation 4.
func TestSocketDirectoryMustBePrivate(t *testing.T) {
	for _, mode := range []fs.FileMode{0o720, 0o702, 0o777} {
		opts := testOptions(t)
		dir := filepath.Dir(opts.Paths.ControllerSocket)
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		_, err := Start(context.Background(), opts)
		if err == nil || !strings.Contains(err.Error(), "writable by group or other") {
			t.Errorf("socket directory mode %v: Start = %v, want a refusal", mode, err)
		}
		if _, err := os.Stat(filepath.Join(dir, LockName)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("socket directory mode %v: the lock file was created (%v)", mode, err)
		}
	}
	if os.Geteuid() != 0 {
		t.Log("not root: the directory owned by another user is not checked")
		return
	}
	opts := testOptions(t)
	if err := os.Chown(filepath.Dir(opts.Paths.ControllerSocket), 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "is owned by uid 65534") {
		t.Errorf("socket directory of another user: Start = %v, want a refusal", err)
	}
}

// TestFailedStartUndoesItself shows that a start that fails part-way closes
// what it opened and releases the lock.
func TestFailedStartUndoesItself(t *testing.T) {
	opts := testOptions(t)
	if err := os.MkdirAll(opts.Paths.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(opts.Paths.ControllerDB(), []byte("not a database, not at all"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), opts); err == nil {
		t.Fatal("Start succeeded on a corrupt controller database")
	}
	if err := os.Remove(opts.Paths.ControllerDB()); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestShutdownOnce is register row 74: concurrent Shutdown calls run the
// shutdown once, and every caller gets its result after it finished.
func TestShutdownOnce(t *testing.T) {
	s, err := Start(context.Background(), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make([]error, 4)
	for i := range errs {
		wg.Go(func() { errs[i] = s.Shutdown(context.Background()) })
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("Shutdown %d: %v", i, err)
		}
	}
	if err := s.controllerDB.Read().PingContext(context.Background()); err == nil {
		t.Error("the controller database is still open after Shutdown")
	}
}

func TestStartNeedsPaths(t *testing.T) {
	if _, err := Start(context.Background(), Options{}); err == nil || !strings.Contains(err.Error(), "Options.Paths") {
		t.Errorf("Start without paths: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/server/`
Expected: FAIL (build failure):

```text
# github.com/darkraise/darkpanel/internal/controller/server [github.com/darkraise/darkpanel/internal/controller/server.test]
internal/controller/server/server_test.go:41:32: undefined: Options
internal/controller/server/server_test.go:54:9: undefined: Options
internal/controller/server/server_test.go:66:31: undefined: Options
internal/controller/server/server_test.go:66:41: undefined: Server
internal/controller/server/server_test.go:68:12: undefined: Start
internal/controller/server/server_test.go:105:12: undefined: Start
internal/controller/server/server_test.go:118:62: undefined: LockName
internal/controller/server/server_test.go:173:16: undefined: Start
internal/controller/server/server_test.go:177:11: undefined: Start
internal/controller/server/server_test.go:195:13: undefined: Start
internal/controller/server/server_test.go:195:13: too many errors
FAIL	github.com/darkraise/darkpanel/internal/controller/server [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/server/server.go`:

```go
// Package server assembles `darkpanel serve` (spec section 1, part P14): the
// controller and, in M1, the agent half in one process. Start opens the
// stores and starts the services and listeners in the order the spec sets;
// Shutdown stops them in reverse.
package server

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/oklog/ulid/v2"
	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/paths"
)

// LockName is the single-instance lock file in the controller socket's
// directory.
const LockName = "controller.lock"

// BackupsKept is how many migration backups the controller database keeps.
const BackupsKept = 5

// ShutdownTimeout bounds the shutdown of Run, and the cleanup of a failed
// Start, once their context has ended.
const ShutdownTimeout = 30 * time.Second

// timeFormat is the fixed-width RFC 3339 UTC form of every stored time.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Options configure Start. Paths is required. Production passes
// paths.Resolve(dev), the parsed --listen values and nil or zero for every
// other field; tests override the rest.
type Options struct {
	Paths paths.Paths
	// Listen are the listeners to serve, from listen.ParseSpecs; nil means
	// the defaults, loopback and tailscale.
	Listen []listen.Spec
	// Dev accepts the service's own non-root uid on the controller socket.
	Dev bool
	// Clock is nil for core.SystemClock.
	Clock core.Clock
	// Log is nil to discard the service log.
	Log *slog.Logger
	// Hostname is nil for os.Hostname.
	Hostname func() (string, error)
	// Bind is nil for listen.Bind.
	Bind listen.BindFunc
	// Interfaces is nil for listen.SystemInterfaceAddrs.
	Interfaces listen.InterfaceAddrs
	// AuthParams is zero for auth.DefaultParams.
	AuthParams auth.Params
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
type closer struct {
	name  string
	close func(ctx context.Context) error
}

// Server is one running controller with its local agent.
type Server struct {
	opts     Options
	clock    core.Clock
	log      *slog.Logger
	hostname string // "" when unknown

	controllerDB     *store.DB
	agentDB          *store.DB
	controllerSealer *secret.Sealer
	agentSealer      *secret.Sealer
	localHostID      string

	mu       sync.Mutex // serialises Shutdown
	closers  []closer
	shutdown bool
	err      error
}

// Start opens the stores and starts the services and the listeners. On an
// error it stops what it started and returns the error.
func Start(ctx context.Context, opts Options) (*Server, error) {
	if opts.Paths == (paths.Paths{}) {
		return nil, errors.New("server: Options.Paths is required")
	}
	s := &Server{opts: opts, clock: opts.Clock, log: opts.Log}
	if s.clock == nil {
		s.clock = core.SystemClock{}
	}
	if s.log == nil {
		s.log = slog.New(slog.DiscardHandler)
	}
	for _, step := range []func(context.Context) error{s.openStorage} {
		if err := step(ctx); err != nil {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
			defer cancel()
			return nil, errors.Join(err, s.Shutdown(cleanup))
		}
	}
	return s, nil
}

// LocalHostID is the id of this host's core_hosts row.
func (s *Server) LocalHostID() string { return s.localHostID }

// SocketPath is the controller socket's path.
func (s *Server) SocketPath() string { return s.opts.Paths.ControllerSocket }

// onShutdown registers a step for Shutdown, which runs the steps in reverse.
func (s *Server) onShutdown(name string, stop func(ctx context.Context) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closers = append(s.closers, closer{name, stop})
}

// Shutdown stops everything Start started, in reverse order, one step at a
// time; listeners let running requests finish until ctx ends. A second call,
// concurrent or later, waits for the first and returns its result.
func (s *Server) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shutdown {
		return s.err
	}
	s.shutdown = true
	var errs []error
	for i := len(s.closers) - 1; i >= 0; i-- {
		if err := s.closers[i].close(ctx); err != nil {
			errs = append(errs, fmt.Errorf("server: stop %s: %w", s.closers[i].name, err))
		}
	}
	s.closers = nil
	s.err = errors.Join(errs...)
	return s.err
}

// openStorage creates the directories, checks the socket's directory, loads
// or creates both keys, takes the single-instance lock, opens both
// databases, migrates the controller database and records the local host.
func (s *Server) openStorage(ctx context.Context) error {
	p := s.opts.Paths
	if err := p.MkdirAll(); err != nil {
		return err
	}
	runDir := filepath.Dir(p.ControllerSocket)
	if err := checkPrivateDir(runDir); err != nil {
		return err
	}
	var err error
	if s.controllerSealer, err = loadSealer(p.ControllerKey); err != nil {
		return err
	}
	if s.agentSealer, err = loadSealer(p.AgentKey); err != nil {
		return err
	}
	if err := s.lock(filepath.Join(runDir, LockName)); err != nil {
		return err
	}
	if s.controllerDB, err = s.openDB(ctx, "controller database", p.ControllerDB()); err != nil {
		return err
	}
	if s.agentDB, err = s.openDB(ctx, "agent database", p.AgentDB()); err != nil {
		return err
	}
	if err := s.migrateController(ctx); err != nil {
		return err
	}
	return s.ensureLocalHost(ctx)
}

// checkPrivateDir refuses a directory that another user could write, so no
// one else can replace the controller socket or the lock file (register row
// 69, obligation 4).
func checkPrivateDir(dir string) error {
	st, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if !st.IsDir() {
		return fmt.Errorf("server: the controller socket directory %s is not a directory", dir)
	}
	if st.Mode().Perm()&0o022 != 0 {
		return fmt.Errorf("server: the controller socket directory %s is writable by group or other; run chmod 700 %s", dir, dir)
	}
	if sys, ok := st.Sys().(*syscall.Stat_t); ok && int64(sys.Uid) != int64(os.Geteuid()) {
		return fmt.Errorf("server: the controller socket directory %s is owned by uid %d, not by this process's uid %d", dir, sys.Uid, os.Geteuid())
	}
	return nil
}

func loadSealer(path string) (*secret.Sealer, error) {
	key, err := secret.LoadOrCreateKey(path)
	if err != nil {
		return nil, err
	}
	return secret.NewSealer(key)
}

// lock takes an exclusive flock on path, held until Shutdown, so a second
// serve on the same paths fails before it opens a database or touches the
// socket (register row 76).
func (s *Server) lock(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|syscall.O_NOFOLLOW, 0o600) //nolint:gosec // G304: the path comes from paths.Paths, never from a request
	if err != nil {
		return fmt.Errorf("server: open the lock file: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil { //nolint:gosec // G115: a file descriptor fits in an int
		_ = f.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return fmt.Errorf("server: another darkpanel serve holds %s; stop it first", path)
		}
		return fmt.Errorf("server: lock %s: %w", path, err)
	}
	// Closing the file releases the lock. The file stays, so a waiting
	// serve never locks a file that a third one has just replaced.
	s.onShutdown("lock", func(context.Context) error { return f.Close() })
	return nil
}

func (s *Server) openDB(ctx context.Context, name, path string) (*store.DB, error) {
	db, err := store.Open(ctx, path)
	if err != nil {
		return nil, err
	}
	s.onShutdown(name, func(context.Context) error { return db.Close() })
	return db, nil
}

// migrateController applies the core controller migrations and then each
// module's controller migrations. A failed module migration is logged and
// does not stop startup (R9); the error may quote SQL, so it is only logged.
func (s *Server) migrateController(ctx context.Context) error {
	coreSet, err := store.CoreMigrations(store.SideController)
	if err != nil {
		return err
	}
	var sets []store.MigrationSet
	for _, m := range modules.All() {
		man := m.Manifest()
		if fsys, ok := man.Migrations[core.SideController]; ok && fsys != nil {
			sets = append(sets, store.MigrationSet{Module: man.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, s.controllerDB, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: buildinfo.Version, BackupRoot: s.opts.Paths.ControllerBackups(),
		Keep: BackupsKept, Now: s.clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return fmt.Errorf("server: controller migrations: %w", err)
	}
	for _, f := range res.Failed {
		s.log.Error("server: module controller migrations failed", "module", f.Module, "error", f.Err)
	}
	return nil
}

// ensureLocalHost creates this host's core_hosts row and core_meta's
// local_host_id on the first start, named from the hostname, and reads the
// id on every later start.
func (s *Server) ensureLocalHost(ctx context.Context) error {
	hostname := "localhost"
	lookup := s.opts.Hostname
	if lookup == nil {
		lookup = os.Hostname
	}
	if h, err := lookup(); err != nil || h == "" {
		s.log.Warn("server: the hostname is unknown; the local host is named localhost", "error", err)
	} else {
		hostname, s.hostname = h, h
	}
	err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'local_host_id'`).Scan(&s.localHostID)
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		now := s.clock.Now().UTC()
		id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 1, ?)`,
			id, hostname, now.Format(timeFormat)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES ('local_host_id', ?)`, id); err != nil {
			return err
		}
		s.localHostID = id
		return nil
	})
	if err != nil {
		return fmt.Errorf("server: local host record: %w", err)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/server/` — Expected: PASS
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/server/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/server_test.go
git commit -m "feat(server): open stores and the local host"
```

### Task 38: Server services: identity, agent runtime, engine, purges and dispatch

**Files:**
- Create: `internal/controller/server/services.go`
- Modify: `internal/controller/server/server.go`
- Test: `internal/controller/server/services_test.go`

**Interfaces:**
- Consumes: Contracts › Task 35 (hostinfo registered) and Task 37 (`Server` fields, `onShutdown`, test helpers); Contracts › Task 4 (`schema.Redact`), Task 12 (`audit.New`, `audit.Store`), Task 13 (`audit.NewIngester`, `IngesterConfig`, `IngestAll`, `Start`, `Stop`), Tasks 14 and 19 (`runtime.Open`, `runtime.Config`, `Close`), Task 23 (`dispatch.NewHosts`, `HostsConfig`, `Add`, `Hello`), Task 25 (`dispatch.New`, `dispatch.Config`); Part A's "How the next parts use it" steps 2-9; plan 3's `auth.New`, `auth.Options`, `auth.Service.Purge`, `coreactions.Module`, `coreactions.Deps`; `registry.BuildWith`; `lifecycle.New`, `lifecycle.Config`, `Engine.Recover`, `Engine.Purge`, `Engine.Close`; `schema.NewValidator`; `platform.NewRunner`, `platform.ProductionAllowlist`.
- Produces: Contracts › Task 38 (`PurgeInterval`, `Registry`, `services`; test helpers `root`, `refDisable`, `count`, `planDisable`).

**Items:** 5, 51, 52

**Depends on:** Task 27, Task 30, Task 35, Task 37

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The second step wires the services over the migrated stores in Part A's order: the audit store, the identity service, the one registry with real dependencies, the agent runtime (its migrations, recovery and purge loop), the host directory whose `Hello` seeds the local host's module rows from `DefaultEnabled`, the controller engine with `schema.Redact` and the audit store as journal, its `Recover` once, the controller purges at start and every 10 minutes (register row 51), the ingester and the dispatcher. The tests read hostinfo and `core.host.list` through the dispatcher, settle a change left `applying` by an earlier run, purge an expired plan at start and plans and idle sessions on the timer, and leave no timer armed after `Shutdown`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/services_test.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// root is the controller socket's principal for uid 0 on host id.
func root(id string) core.Principal {
	return core.Principal{Actor: "local:root@" + id, Scope: core.ScopeAdmin, Source: "socket"}
}

var refDisable = core.ActionRef{Name: "core.module.disable", Version: 1}

func count(t *testing.T, s *Server, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.controllerDB.Read().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// planDisable plans core.module.disable of hostinfo, which stores a plan.
func planDisable(t *testing.T, s *Server) core.PlanView {
	t.Helper()
	in := json.RawMessage(`{"hostId":"` + s.LocalHostID() + `","moduleId":"hostinfo"}`)
	v, err := s.dispatcher.Plan(context.Background(), root(s.LocalHostID()), "", refDisable, in)
	if err != nil {
		t.Fatalf("plan core.module.disable: %v", err)
	}
	return v
}

// TestStartSeedsAndServesModules shows the first start's module rows, seeded
// from DefaultEnabled by the agent's Hello, and a resource and a core action
// read through the dispatcher with the server's own registry.
func TestStartSeedsAndServesModules(t *testing.T) {
	s := start(t, testOptions(t))
	ctx := context.Background()
	id := s.LocalHostID()
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE host_id = ? AND module_id = 'hostinfo' AND enabled = 1 AND available = 1`, id); n != 1 {
		t.Fatalf("hostinfo module rows enabled and available on the local host: %d, want 1", n)
	}
	if _, ok := s.Registry().Resource("hostinfo.summary"); !ok {
		t.Fatal("the server's registry has no hostinfo.summary")
	}
	out, err := s.dispatcher.Resource(ctx, root(id), id, "hostinfo.summary")
	if err != nil {
		t.Fatalf("hostinfo.summary: %v", err)
	}
	var summary struct{ Hostname, DarkpanelVersion string }
	if err := json.Unmarshal(out, &summary); err != nil || summary.Hostname == "" || summary.DarkpanelVersion != "dev" {
		t.Errorf("hostinfo.summary = %s (%v), want a hostname and darkpanelVersion dev", out, err)
	}
	out, err = s.dispatcher.Read(ctx, root(id), "", core.ActionRef{Name: "core.host.list", Version: 1}, json.RawMessage(`{}`))
	if err != nil {
		t.Fatalf("core.host.list: %v", err)
	}
	var hosts struct {
		Hosts []struct {
			ID, Name string
			IsLocal  bool
			Modules  []struct {
				ID        string
				Enabled   bool
				Available bool
			}
		}
	}
	if err := json.Unmarshal(out, &hosts); err != nil {
		t.Fatal(err)
	}
	h := hosts.Hosts
	if len(h) != 1 || h[0].ID != id || h[0].Name != testHostname || !h[0].IsLocal || len(h[0].Modules) != 1 ||
		h[0].Modules[0].ID != "hostinfo" || !h[0].Modules[0].Enabled || !h[0].Modules[0].Available {
		t.Errorf("core.host.list = %s, want the local host with hostinfo enabled and available", out)
	}
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE module_id = 'core'`); n != 0 {
		t.Errorf("the core module has %d state rows, want none", n)
	}
}

// TestRecoverRunsAtStart is register row 51: a controller change that a
// previous run left applying is settled by Recover before the server
// serves, and its audit row is written through the audit store.
func TestRecoverRunsAtStart(t *testing.T) {
	opts := testOptions(t)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	plan := planDisable(t, s)
	at := t0.Format(timeFormat)
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		 input_json, created_at, updated_at)
		 VALUES ('01CRASHED0000000000000000', ?, '', 'core.module.disable', 1, 'compensable', 'core.module.enable',
		 'local:root@h', 'applying', '{}', ?, ?)`, plan.PlanID, at, at); err != nil {
		t.Fatal(err)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}

	s = start(t, opts)
	v, err := s.dispatcher.Change(context.Background(), root(s.LocalHostID()), "01CRASHED0000000000000000")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateInterrupted || v.Error == nil || !strings.Contains(v.Error.Message, "interrupted by a restart") {
		t.Errorf("after restart the change is %s (%+v), want interrupted by a restart", v.State, v.Error)
	}
	if n := count(t, s, `SELECT count(*) FROM core_audit WHERE change_id = '01CRASHED0000000000000000' AND phase = 'interrupt'`); n != 1 {
		t.Errorf("interrupt audit rows: %d, want 1", n)
	}
}

// TestPurgesRunAtStartAndEveryTenMinutes is register row 51 and spec
// section 20: expired plans and sessions are purged at start and every
// PurgeInterval.
func TestPurgesRunAtStartAndEveryTenMinutes(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	ctx := context.Background()
	s, err := Start(ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	planDisable(t, s)
	if err := s.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.SessionIdle) // the plan expired while the server was down
	s = start(t, opts)
	if n := count(t, s, `SELECT count(*) FROM core_plans`); n != 0 {
		t.Errorf("plans after a start: %d, want the expired plan purged", n)
	}

	planDisable(t, s)
	_, secret, err := s.authService.CreateUser(ctx, "admin", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.authService.ActivateUser(ctx, "admin", code); err != nil {
		t.Fatal(err)
	}
	clock.Advance(auth.TOTPPeriod)
	code, _ = auth.TOTPCode(secret, clock.Now())
	if _, err := s.authService.Login(ctx, auth.LoginRequest{Username: "admin", Password: "correct horse battery", Code: code, Source: "127.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	if count(t, s, `SELECT count(*) FROM core_plans`) != 1 || count(t, s, `SELECT count(*) FROM core_sessions`) != 1 {
		t.Fatal("want one plan and one session before the purges")
	}
	clock.Advance(PurgeInterval)
	if n := count(t, s, `SELECT count(*) FROM core_plans`); n != 0 {
		t.Errorf("plans after one purge interval: %d, want 0", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_sessions`); n != 1 {
		t.Errorf("sessions after one purge interval: %d, want the live one kept", n)
	}
	clock.Advance(auth.SessionIdle)
	if n := count(t, s, `SELECT count(*) FROM core_sessions`); n != 0 {
		t.Errorf("sessions an hour later: %d, want the idle session purged", n)
	}
}

// TestShutdownStopsTheTimers shows that after Shutdown no purge, ingestion
// or revert timer of the server is left armed.
func TestShutdownStopsTheTimers(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	if clock.Pending() == 0 {
		t.Fatal("no timer is armed while the server runs")
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if n := clock.Pending(); n != 0 {
		t.Errorf("%d timers armed after Shutdown, want 0", n)
	}
	clock.Advance(time.Hour) // nothing runs against the closed databases
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestStartSeedsAndServesModules|TestRecoverRunsAtStart|TestPurgesRunAtStartAndEveryTenMinutes|TestShutdownStopsTheTimers' ./internal/controller/server/`
Expected: FAIL (build failure):

```text
# github.com/darkraise/darkpanel/internal/controller/server [github.com/darkraise/darkpanel/internal/controller/server.test]
internal/controller/server/services_test.go:35:14: s.dispatcher undefined (type *Server has no field or method dispatcher)
internal/controller/server/services_test.go:52:16: s.Registry undefined (type *Server has no field or method Registry)
internal/controller/server/services_test.go:55:16: s.dispatcher undefined (type *Server has no field or method dispatcher)
internal/controller/server/services_test.go:63:15: s.dispatcher undefined (type *Server has no field or method dispatcher)
internal/controller/server/services_test.go:114:14: s.dispatcher undefined (type *Server has no field or method dispatcher)
internal/controller/server/services_test.go:148:22: s.authService undefined (type *Server has no field or method authService)
internal/controller/server/services_test.go:156:17: s.authService undefined (type *Server has no field or method authService)
internal/controller/server/services_test.go:161:17: s.authService undefined (type *Server has no field or method authService)
internal/controller/server/services_test.go:167:16: undefined: PurgeInterval
FAIL	github.com/darkraise/darkpanel/internal/controller/server [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/server/services.go`:

```go
package server

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/audit"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
)

// PurgeInterval is how often the controller purges expired plans, sessions
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// services are the controller's and the local agent's services.
type services struct {
	auditLog    *audit.Store
	authService *auth.Service
	registry    *core.Registry
	runtime     *runtime.Runtime
	hosts       *dispatch.Hosts
	engine      *lifecycle.Engine
	ingester    *audit.Ingester
	dispatcher  *dispatch.Dispatcher

	purgeMu     sync.Mutex // held while a purge runs, so Shutdown waits for it
	purgeTimer  core.Timer
	purgeClosed bool
}

// Registry is the server's registry, built with its services.
func (s *Server) Registry() *core.Registry { return s.registry }

// startServices builds the services over the migrated stores, in the order
// that the controller's foreign keys and recovery need: the agent half says
// Hello (seeding the local host's module rows from DefaultEnabled) before
// the controller engine recovers, and nothing is served until both have.
func (s *Server) startServices(ctx context.Context) error {
	s.auditLog = audit.New(s.controllerDB, s.clock)
	s.authService = auth.New(s.controllerDB, s.controllerSealer, s.clock, s.auditLog, auth.Options{Params: s.opts.AuthParams})
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
	}))
	if err != nil {
		return fmt.Errorf("server: registry: %w", err)
	}
	s.registry = reg

	rt, err := runtime.Open(ctx, runtime.Config{
		DB: s.agentDB, Sealer: s.agentSealer, Clock: s.clock, Log: s.log, Registry: reg,
		HostID: s.localHostID, BinaryVersion: buildinfo.Version, BackupDir: s.opts.Paths.AgentBackups(),
		Exec: platform.NewRunner(platform.ProductionAllowlist()),
	})
	if err != nil {
		return err
	}
	s.runtime = rt
	s.onShutdown("agent runtime", func(context.Context) error { rt.Close(); return nil })

	// Every core_hosts row needs a client here, or Changes and ingestion
	// fail on it; the local host is the only one in M1.
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{DB: s.controllerDB, Registry: reg, Clock: s.clock, Log: s.log})
	s.hosts.Add(s.localHostID, rt)
	if err := s.hosts.Hello(ctx, s.localHostID); err != nil {
		return fmt.Errorf("server: hello from the local agent: %w", err)
	}

	eng, err := lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: reg, Validator: schema.NewValidator(), Journal: s.auditLog,
		Redact: schema.Redact,
		Base: core.Env{
			DB: s.controllerDB, Sealer: s.controllerSealer, Clock: s.clock, Log: s.log, Agents: s.hosts,
		},
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	s.engine = eng
	s.onShutdown("controller engine", func(context.Context) error { eng.Close(); return nil })
	if err := eng.Recover(ctx); err != nil {
		return fmt.Errorf("server: recover: %w", err)
	}
	if err := s.startPurges(ctx); err != nil {
		return err
	}

	s.ingester = audit.NewIngester(audit.IngesterConfig{Store: s.auditLog, Agents: s.hosts, Clock: s.clock, Log: s.log})
	if err := s.ingester.IngestAll(ctx); err != nil {
		s.log.Warn("server: audit ingestion", "error", err)
	}
	s.ingester.Start()
	s.onShutdown("audit ingester", func(context.Context) error { s.ingester.Stop(); return nil })

	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}

// purge deletes the controller engine's expired plans and the identity
// service's expired sessions and tokens.
func (s *Server) purge(ctx context.Context) error {
	return errors.Join(s.engine.Purge(ctx), s.authService.Purge(ctx))
}

// startPurges purges now, failing startup on an error, and then every
// PurgeInterval, logging an error.
func (s *Server) startPurges(ctx context.Context) error {
	if err := s.purge(ctx); err != nil {
		return fmt.Errorf("server: purge: %w", err)
	}
	s.purgeMu.Lock()
	s.purgeTimer = s.clock.AfterFunc(PurgeInterval, s.purgeTick)
	s.purgeMu.Unlock()
	s.onShutdown("purges", func(context.Context) error {
		s.purgeMu.Lock()
		defer s.purgeMu.Unlock()
		s.purgeClosed = true
		s.purgeTimer.Stop()
		return nil
	})
	return nil
}

func (s *Server) purgeTick() {
	s.purgeMu.Lock()
	defer s.purgeMu.Unlock()
	if s.purgeClosed {
		return
	}
	if err := s.purge(context.Background()); err != nil {
		s.log.Warn("server: purge", "error", err)
	}
	s.purgeTimer = s.clock.AfterFunc(PurgeInterval, s.purgeTick)
}
```

Edit `internal/controller/server/server.go`.

1. Replace this block:

```go
	localHostID      string

	mu       sync.Mutex // serialises Shutdown
```

   with:

```go
	localHostID      string

	services

	mu       sync.Mutex // serialises Shutdown
```

2. Replace this block:

```go
	for _, step := range []func(context.Context) error{s.openStorage} {
```

   with:

```go
	for _, step := range []func(context.Context) error{s.openStorage, s.startServices} {
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/server/` — Expected: PASS
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/server/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/services.go internal/controller/server/services_test.go
git commit -m "feat(server): start the services and purges"
```

### Task 39: Server listeners, controller socket and Run

**Files:**
- Create: `internal/controller/server/listeners.go`
- Modify: `internal/controller/server/server.go`
- Test: `internal/controller/server/listeners_test.go`

**Interfaces:**
- Consumes: Contracts › Task 38 (`services` fields, test helpers) and Task 37 (`onShutdown`, test helpers); plan 3's `listen.LoadOrCreateAuthority`, `listen.Start`, `listen.Config`, `listen.Service` (`State`, `Shutdown`), `listen.Bound`, `listen.ParseSpecs`, `api.New`, `api.Config`, `api.EmbeddedUI`, `api.ListenSocket`, `(*api.Server).Handler`, `SocketServer`, `SocketListener`; Contracts › Tasks 33 and 34 (`api.ListenerState`, `SetListenerState`, `GET /api/v1/controller/listeners`); `store.SchemaVersion`; `auth.TOTPCode`, `auth.Service.CreateUser`, `ActivateUser`, `CreateToken`.
- Produces: Contracts › Task 39 (`Listeners`, `Run`, `listeners`; test helpers `baseURL`, `socketClient`, `get`, `readToken`).

**Items:** 69, 74

**Depends on:** Task 34, Task 38

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The last step serves: the web authority, the API with the embedded UI and `Dev` (register row 69, obligations 5 and 6), the listener service with the API handler, the listener-state source over `svc.State()` (obligation 7), and the controller socket through `api.ListenSocket`, `SocketServer` and `SocketListener`. Their stops come first in `Shutdown`: the socket, then the listeners, one at a time (row 74). `Run` is `serve`: start, wait for the context, shut down within 30 seconds. The tests read `/healthz` and the listener state over HTTP and health and the web root over the socket, check the socket's mode, show `Shutdown` closes both, refuse a socket another process serves (and release the lock), run `Run` until its context ends, and start with the default listen values.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/listeners_test.go`:

```go
package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// baseURL is the URL of the server's IPv4 loopback listener.
func baseURL(t *testing.T, s *Server) string {
	t.Helper()
	for _, b := range s.Listeners() {
		if b.Endpoint.Addr.Addr().Is4() {
			return "http://" + b.Addr.String()
		}
	}
	t.Fatalf("no IPv4 listener in %v", s.Listeners())
	return ""
}

// socketClient is an HTTP client that dials the controller socket.
func socketClient(path string) *http.Client {
	return &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "unix", path)
		},
	}}
}

func get(t *testing.T, c *http.Client, url, token string) (int, string) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, string(body)
}

// readToken creates an active user with a read token through the identity
// service.
func readToken(t *testing.T, s *Server, clock *coretest.FakeClock) string {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.authService.CreateUser(ctx, "reader", "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.authService.ActivateUser(ctx, "reader", code)
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := s.authService.CreateToken(ctx, u.ID, "test", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	return token
}

// TestServesTheNetworkAndTheSocket checks the assembled handlers: /healthz
// and the listener state on the network listener, and full health and the
// web root on the controller socket.
func TestServesTheNetworkAndTheSocket(t *testing.T) {
	opts := testOptions(t)
	s := start(t, opts)
	url := baseURL(t, s)
	if code, body := get(t, http.DefaultClient, url+"/healthz", ""); code != 200 || strings.TrimSpace(body) != `{"status":"ok"}` {
		t.Errorf("GET /healthz = %d %s", code, body)
	}
	if code, _ := get(t, http.DefaultClient, url+"/api/v1/controller/listeners", ""); code != 401 {
		t.Errorf("GET /api/v1/controller/listeners without credentials = %d, want 401", code)
	}
	token := readToken(t, s, opts.Clock.(*coretest.FakeClock))
	code, body := get(t, http.DefaultClient, url+"/api/v1/controller/listeners", token)
	if code != 200 || strings.TrimSpace(body) != `{"tailscaleBound":false,"message":"Tailscale listener disabled by --listen."}` {
		t.Errorf("GET /api/v1/controller/listeners = %d %s, want the listener service's state", code, body)
	}

	sock := socketClient(s.SocketPath())
	code, body = get(t, sock, "http://controller/healthz", "")
	var health struct {
		Status, Version string
		SchemaVersion   int
	}
	if err := json.Unmarshal([]byte(body), &health); err != nil || code != 200 || health.Version != "dev" || health.SchemaVersion < 4 {
		t.Errorf("socket GET /healthz = %d %s, want full detail", code, body)
	}
	code, body = get(t, sock, "http://controller/api/v1/controller/web-ca", "")
	var ca struct{ PEM, Fingerprint string }
	if err := json.Unmarshal([]byte(body), &ca); err != nil || code != 200 || len(ca.Fingerprint) != 64 ||
		!strings.HasPrefix(ca.PEM, "-----BEGIN CERTIFICATE-----") {
		t.Errorf("socket GET /api/v1/controller/web-ca = %d %s", code, body)
	}
	st, err := os.Stat(s.SocketPath())
	if err != nil || st.Mode().Perm() != 0o600 || st.Mode().Type() != fs.ModeSocket {
		t.Errorf("controller socket %v (%v), want a socket with mode 0600", st, err)
	}
}

// TestShutdownClosesTheListeners shows that Shutdown stops the network
// listener and removes the controller socket.
func TestShutdownClosesTheListeners(t *testing.T) {
	s, err := Start(context.Background(), testOptions(t))
	if err != nil {
		t.Fatal(err)
	}
	url := baseURL(t, s)
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	if resp, err := http.Get(url + "/healthz"); err == nil {
		_ = resp.Body.Close()
		t.Error("the network listener still answers after Shutdown")
	}
	if _, err := os.Stat(s.SocketPath()); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller socket after Shutdown: %v, want it removed", err)
	}
}

// TestStartRefusesALiveSocket shows that a socket another process serves
// stops startup, and that the failed start releases the lock.
func TestStartRefusesALiveSocket(t *testing.T) {
	opts := testOptions(t)
	var lc net.ListenConfig
	other, err := lc.Listen(context.Background(), "unix", opts.Paths.ControllerSocket)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Start(context.Background(), opts); err == nil || !strings.Contains(err.Error(), "served by another process") {
		t.Fatalf("Start beside a live socket: %v", err)
	}
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	start(t, opts)
}

// TestRunStopsWhenTheContextEnds is serve's life: Run serves until its
// context ends, then shuts down.
func TestRunStopsWhenTheContextEnds(t *testing.T) {
	opts := testOptions(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Run(ctx, opts) }()
	sock := socketClient(opts.Paths.ControllerSocket)
	deadline := time.Now().Add(30 * time.Second)
	for {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://controller/healthz", nil)
		if resp, err := sock.Do(req); err == nil {
			_ = resp.Body.Close()
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("Run did not serve the controller socket within 30 s")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("Run did not return after its context ended")
	}
	if _, err := os.Stat(opts.Paths.ControllerSocket); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("controller socket after Run: %v, want it removed", err)
	}
}

// TestDefaultListenValues shows that nil Listen means the defaults,
// loopback and tailscale, with no Tailscale interface present.
func TestDefaultListenValues(t *testing.T) {
	opts := testOptions(t)
	opts.Listen = nil
	opts.Interfaces = func(string) ([]netip.Addr, bool, error) { return nil, false, nil }
	s := start(t, opts)
	got := map[string]bool{}
	for _, b := range s.Listeners() {
		got[b.Endpoint.URL()] = true
	}
	if !got["http://127.0.0.1:8080"] {
		t.Errorf("listeners %v, want the loopback default", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestServesTheNetworkAndTheSocket|TestShutdownClosesTheListeners|TestStartRefusesALiveSocket|TestRunStopsWhenTheContextEnds|TestDefaultListenValues' ./internal/controller/server/`
Expected: FAIL (build failure):

```text
# github.com/darkraise/darkpanel/internal/controller/server [github.com/darkraise/darkpanel/internal/controller/server.test]
internal/controller/server/listeners_test.go:25:22: s.Listeners undefined (type *Server has no field or method Listeners)
internal/controller/server/listeners_test.go:30:39: s.Listeners undefined (type *Server has no field or method Listeners)
internal/controller/server/listeners_test.go:173:22: undefined: Run
internal/controller/server/listeners_test.go:209:22: s.Listeners undefined (type *Server has no field or method Listeners)
FAIL	github.com/darkraise/darkpanel/internal/controller/server [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Create `internal/controller/server/listeners.go`:

```go
package server

import (
	"context"
	"errors"
	"net/http"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// listeners are the network listeners and the controller socket.
type listeners struct {
	apiServer     *api.Server
	listenService *listen.Service
}

// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// startListeners loads or creates the web authority, builds the API, starts
// the network listeners with it and serves the controller socket.
func (s *Server) startListeners(ctx context.Context) error {
	authority, err := listen.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock)
	if err != nil {
		return err
	}
	srv, err := api.New(api.Config{
		Dispatcher: s.dispatcher, Auth: s.authService, Registry: s.registry, WebCA: authority,
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log,
	})
	if err != nil {
		return err
	}
	s.apiServer = srv

	specs := s.opts.Listen
	if specs == nil {
		if specs, err = listen.ParseSpecs(nil); err != nil {
			return err
		}
	}
	svc, err := listen.Start(ctx, listen.Config{
		Specs: specs, Hostname: s.hostname, DB: s.controllerDB, Authority: authority, Clock: s.clock,
		Handler: srv.Handler(), Log: s.log, Interfaces: s.opts.Interfaces, Bind: s.opts.Bind,
	})
	if err != nil {
		return err
	}
	s.listenService = svc
	s.onShutdown("listeners", svc.Shutdown)
	// The source stays set after Shutdown, so the state then reads that the
	// listeners are shut down.
	srv.SetListenerState(func() api.ListenerState {
		st := svc.State()
		return api.ListenerState{TailscaleBound: st.TailscaleBound, Message: st.Message}
	})

	ln, err := api.ListenSocket(s.opts.Paths.ControllerSocket)
	if err != nil {
		return err
	}
	sock := srv.SocketServer()
	served := make(chan error, 1)
	go func() { served <- sock.Serve(srv.SocketListener(ln, nil)) }()
	s.onShutdown("controller socket", func(ctx context.Context) error {
		err := sock.Shutdown(ctx)
		if serveErr := <-served; !errors.Is(serveErr, http.ErrServerClosed) {
			err = errors.Join(err, serveErr)
		}
		return err
	})

	urls := make([]string, 0, len(svc.State().Listeners))
	for _, b := range svc.State().Listeners {
		urls = append(urls, b.Endpoint.URL())
	}
	s.log.Info("server: serving", "message", svc.State().Message, "listeners", urls, "socket", s.opts.Paths.ControllerSocket)
	return nil
}

// Run starts a server, serves until ctx ends and then shuts it down within
// ShutdownTimeout. It is `darkpanel serve`.
func Run(ctx context.Context, opts Options) error {
	s, err := Start(ctx, opts)
	if err != nil {
		return err
	}
	<-ctx.Done()
	stop, cancel := context.WithTimeout(context.WithoutCancel(ctx), ShutdownTimeout)
	defer cancel()
	return s.Shutdown(stop)
}
```

Edit `internal/controller/server/server.go`.

1. Replace this block:

```go
	services

	mu       sync.Mutex // serialises Shutdown
```

   with:

```go
	services
	listeners

	mu       sync.Mutex // serialises Shutdown
```

2. Replace this block:

```go
	for _, step := range []func(context.Context) error{s.openStorage, s.startServices} {
```

   with:

```go
	for _, step := range []func(context.Context) error{s.openStorage, s.startServices, s.startListeners} {
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/server/` — Expected: PASS
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/server/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/listeners.go internal/controller/server/listeners_test.go
git commit -m "feat(server): serve the listeners and the socket"
```

### Task 40: Registration rules, CLI parity and nullable outputs

**Files:**
- Test: `internal/modules/registry_test.go`

**Interfaces:**
- Consumes: Contracts › Task 35 (`built`); `cli.NewRoot`, `cli.Options`, `cli.FindCommand`, `cli.CommandPath` (Contracts › Tasks 8 and 20); `core.Registry` (`Modules`, `Actions`, `Action`, `Resources`, `Resource`), `core.ModuleIDCore`; `schema.Output`; `github.com/santhosh-tekuri/jsonschema/v6`.
- Produces: Contracts › Task 40 (tests only).

**Items:** 3, 70

**Depends on:** Task 29, Task 35

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 2 - risk 0 = 2

Three guards over the real registry, each a test a later module must pass. `TestRegistrationRules` re-states spec section 2's rules (written out, so a rule weakened in `internal/core` fails here). `TestEveryActionHasACLICommand` is spec section 10's parity test: the CLI built as `cmd/darkpanel` builds it has a command for every action and resource (register row 3). `TestPointerOutputsAreNullable` is register row 70's guard: it walks every registered output type with its published schema and fails on a pointer field whose property does not accept `null`; `TestNullableGuardFindsUntaggedPointers` proves the walker on a probe type. These tests guard code that already exists, so they pass on the parent; the Assumptions record the mutation runs that make each fail.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/registry_test.go`:

```go
package modules_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// The registration rules of spec section 2, written out here rather than
// taken from internal/core, so that a rule weakened there fails this test.
var (
	moduleIDRule   = regexp.MustCompile(`^[a-z][a-z0-9]*$`)
	actionNameRule = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	resourceRule   = regexp.MustCompile(`^[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`)
	reservedCLI    = []string{
		"serve", "agent", "update", "user", "controller", "change", "uninstall", "version",
		"host", "module", "token", "audit", "help", "completion",
	}
)

// TestRegistrationRules checks every rule of spec section 2 over the real
// registry: module ids, action and resource names, unique versions,
// compensating actions, and the sides of break-glass and revertible actions.
func TestRegistrationRules(t *testing.T) {
	reg := built(t)
	names := map[string]bool{}
	for _, def := range reg.Actions() {
		names[def.Spec.Name] = true
	}
	ids := map[string]bool{}
	for _, m := range reg.Modules() {
		switch {
		case !moduleIDRule.MatchString(m.ID):
			t.Errorf("module id %q does not match %s", m.ID, moduleIDRule)
		case slices.Contains(reservedCLI, m.ID):
			t.Errorf("module id %q is a reserved CLI name", m.ID)
		case ids[m.ID]:
			t.Errorf("module id %q is registered twice", m.ID)
		}
		ids[m.ID] = true
		refs := map[core.ActionRef]bool{}
		for _, a := range m.Actions {
			s := a.Spec
			if !actionNameRule.MatchString(s.Name) || !strings.HasPrefix(s.Name, m.ID+".") {
				t.Errorf("action %s: name must match %s and start with %s.", s.Name, actionNameRule, m.ID)
			}
			if refs[a.Ref()] {
				t.Errorf("action %s is registered twice", a.Ref())
			}
			refs[a.Ref()] = true
			if s.Class == core.ClassCompensable && !names[s.CompensatedBy] {
				t.Errorf("action %s: compensating action %q is not registered", a.Ref(), s.CompensatedBy)
			}
			if s.BreakGlass && s.Side != core.SideController {
				t.Errorf("action %s: break-glass but %s-side", a.Ref(), s.Side)
			}
			if s.Class == core.ClassRevertible && s.Side != core.SideAgent {
				t.Errorf("action %s: revertible but %s-side", a.Ref(), s.Side)
			}
			if def, ok := reg.Action(a.Ref()); !ok || def.Spec.Name != s.Name {
				t.Errorf("action %s of module %s is not in the registry", a.Ref(), m.ID)
			}
		}
		for _, res := range m.Resources {
			if !resourceRule.MatchString(res.Name) || !strings.HasPrefix(res.Name, m.ID+".") {
				t.Errorf("resource %s: name must match %s and start with %s.", res.Name, resourceRule, m.ID)
			}
			if _, ok := reg.Resource(res.Name); !ok {
				t.Errorf("resource %s of module %s is not in the registry", res.Name, m.ID)
			}
		}
	}
	if !ids[core.ModuleIDCore] || reg.Modules()[0].ID != core.ModuleIDCore {
		t.Errorf("the core module is not registered first")
	}
}

// TestEveryActionHasACLICommand is spec section 10's parity test (register
// row 3): the CLI built from the real registry, as cmd/darkpanel builds it,
// has a generated command for every action, mutating or read, and for every
// resource.
func TestEveryActionHasACLICommand(t *testing.T) {
	reg := built(t)
	root, err := cli.NewRoot(cli.Options{Registry: reg, Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}}})
	if err != nil {
		t.Fatalf("cli.NewRoot: %v", err)
	}
	mutating := 0
	for _, def := range reg.Actions() {
		if def.Spec.Class != core.ClassRead {
			mutating++
		}
		if _, ok := cli.FindCommand(root, def.Spec.Name); !ok {
			t.Errorf("action %s has no CLI command darkpanel %s", def.Ref(), strings.Join(cli.CommandPath(def.Spec.Name), " "))
		}
	}
	for _, res := range reg.Resources() {
		if _, ok := cli.FindCommand(root, res.Name); !ok {
			t.Errorf("resource %s has no CLI command darkpanel %s", res.Name, strings.Join(cli.CommandPath(res.Name), " "))
		}
	}
	if mutating == 0 {
		t.Error("the registry has no mutating action, so parity proved nothing")
	}
}

// TestPointerOutputsAreNullable is register row 70's guard: a pointer field
// encodes null, so every pointer field of a registered output type must be
// published with a schema that accepts null (the jsonschema:"nullable"
// tag).
func TestPointerOutputsAreNullable(t *testing.T) {
	reg := built(t)
	var problems []string
	for _, def := range reg.Actions() {
		problems = append(problems, nullableProblems(t, def.Ref().String(), def.OutputType)...)
	}
	for _, res := range reg.Resources() {
		problems = append(problems, nullableProblems(t, res.Name, res.OutputType)...)
	}
	for _, p := range problems {
		t.Error(p)
	}
}

// TestNullableGuardFindsUntaggedPointers proves the guard on a type with an
// untagged pointer at the top and inside a list.
func TestNullableGuardFindsUntaggedPointers(t *testing.T) {
	type item struct {
		Note *int `json:"note"`
	}
	type probe struct {
		Bare   *string  `json:"bare"`
		Tagged *string  `json:"tagged" jsonschema:"nullable"`
		Items  []item   `json:"items"`
		Nested *[]item  `json:"nested" jsonschema:"nullable"`
		Plain  []string `json:"plain"`
	}
	got := nullableProblems(t, "probe", reflect.TypeFor[probe]())
	want := []string{
		"probe: output field bare is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
		"probe: output field items[].note is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
		"probe: output field nested[].note is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
	}
	if !slices.Equal(got, want) {
		t.Errorf("problems:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

// nullableProblems walks output type typ together with its published schema
// and describes every pointer field whose property does not accept null.
func nullableProblems(t *testing.T, owner string, typ reflect.Type) []string {
	t.Helper()
	raw, err := schema.Output(typ)
	if err != nil {
		t.Fatalf("%s: schema.Output: %v", owner, err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("%s: %v", owner, err)
	}
	defs, _ := doc["$defs"].(map[string]any)
	w := &nullWalker{t: t, owner: owner, defs: defs}
	w.walk(typ, doc, "")
	return w.problems
}

type nullWalker struct {
	t        *testing.T
	owner    string
	defs     map[string]any
	problems []string
}

// resolve follows "#/$defs/<name>" references.
func (w *nullWalker) resolve(s map[string]any) map[string]any {
	for range 32 {
		name, ok := strings.CutPrefix(fmt.Sprint(s["$ref"]), "#/$defs/")
		if !ok {
			return s
		}
		s, _ = w.defs[name].(map[string]any)
	}
	return s
}

// branches returns the alternatives of s that are not the null type: the
// members of its oneOf or anyOf, or s itself.
func (w *nullWalker) branches(s map[string]any) []map[string]any {
	s = w.resolve(s)
	var out []map[string]any
	for _, key := range []string{"oneOf", "anyOf"} {
		list, _ := s[key].([]any)
		for _, b := range list {
			if m, ok := b.(map[string]any); ok && m["type"] != "null" {
				out = append(out, w.branches(m)...)
			}
		}
	}
	if len(out) == 0 && s != nil {
		out = append(out, s)
	}
	return out
}

// acceptsNull compiles the property schema s with the document's $defs, as
// the production validator compiles schemas, and validates null.
func (w *nullWalker) acceptsNull(s map[string]any) bool {
	doc := map[string]any{"$defs": w.defs}
	for k, v := range s {
		doc[k] = v
	}
	b, err := json.Marshal(doc)
	if err != nil {
		w.t.Fatal(err)
	}
	parsed, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		w.t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("urn:darkpanel:property", parsed); err != nil {
		w.t.Fatal(err)
	}
	sch, err := c.Compile("urn:darkpanel:property")
	if err != nil {
		w.t.Fatalf("%s: compile a property schema: %v", w.owner, err)
	}
	return sch.Validate(nil) == nil
}

func (w *nullWalker) walk(typ reflect.Type, s map[string]any, path string) {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	for _, b := range w.branches(s) {
		switch typ.Kind() {
		case reflect.Slice, reflect.Array:
			if items, ok := b["items"].(map[string]any); ok {
				w.walk(typ.Elem(), items, strings.TrimSuffix(path, ".")+"[].")
			}
		case reflect.Map:
			if values, ok := b["additionalProperties"].(map[string]any); ok {
				w.walk(typ.Elem(), values, strings.TrimSuffix(path, ".")+"{}.")
			}
		case reflect.Struct:
			if props, ok := b["properties"].(map[string]any); ok {
				w.fields(typ, props, path)
			} // otherwise a type with its own representation, such as time.Time
		}
	}
}

func (w *nullWalker) fields(typ reflect.Type, props map[string]any, path string) {
	for f := range typ.Fields() {
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch {
		case name == "-":
			continue
		case f.Anonymous && name == "" && ft.Kind() == reflect.Struct:
			w.fields(ft, props, path) // embedded fields are inlined
			continue
		case !f.IsExported():
			continue
		case name == "":
			name = f.Name
		}
		p, ok := props[name].(map[string]any)
		if !ok {
			continue
		}
		if f.Type.Kind() == reflect.Pointer && !w.acceptsNull(p) {
			w.problems = append(w.problems, fmt.Sprintf(
				"%s: output field %s%s is a pointer but its schema does not accept null; tag it jsonschema:\"nullable\"",
				w.owner, path, name))
		}
		w.walk(f.Type, p, path+name+".")
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRegistrationRules|TestEveryActionHasACLICommand|TestPointerOutputsAreNullable|TestNullableGuardFindsUntaggedPointers' ./internal/modules/`
Expected: PASS (the tests guard code that exists already):

```text
ok  	github.com/darkraise/darkpanel/internal/modules	<time>
```

- [ ] **Step 3: Write the implementation**

No implementation: the merged registry, CLI and schemas already satisfy these guards.

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/modules/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/modules/registry_test.go
git commit -m "test(modules): check rules, CLI parity, nullables"
```

### Task 41: darkpanel runs the generated CLI and serve

**Files:**
- Modify: `cmd/darkpanel/main.go`
- Modify: `cmd/darkpanel/version_test.go`
- Test: `cmd/darkpanel/serve_test.go`

**Interfaces:**
- Consumes: Contracts › Task 39 (`server.Run`, `server.Options`); Contracts › Task 1 (`cli.ExitCode`, `cli.PrintError`, `ExitOK`, `ExitError`, `ExitUsage`, unmarked `PreRunE` errors) and Task 20 (`cli.NewRoot`, `cli.Options`); `registry.Build`; `listen.ParseSpecs`, `listen.Spec.String`; `paths.Resolve`, `paths.Under`, `paths.Production`.
- Produces: Contracts › `cmd/darkpanel` (`run`, `newRoot`, `serveRun`, `geteuid`; test helpers `fakeServe`, `runArgs`).

**Items:** 30

**Depends on:** Task 29, Task 39

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`main` now builds the generated command tree with `serve` and `version` as hand-written commands, as Part B's contract shows, prints errors with `cli.PrintError` and exits with `cli.ExitCode` (register row 30). `serve`'s refusals of its command line, a bad `--listen` value and production mode without root (spec section 1's M1 rule), run in `PreRunE`, so they exit 2; a failed run exits 1. `RunE` resolves the paths and runs `server.Run` until SIGINT or SIGTERM. The version tests keep their expectations through `run`; the new tests check the exit codes and the options `serve` passes in development and production mode, with the server replaced by a fake.

- [ ] **Step 1: Write the failing test**

Create `cmd/darkpanel/serve_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/paths"
)

// fakeServe replaces serveRun and geteuid for one test and records the
// options serve passed.
func fakeServe(t *testing.T, euid int, result error) *[]server.Options {
	t.Helper()
	var got []server.Options
	oldRun, oldEUID := serveRun, geteuid
	t.Cleanup(func() { serveRun, geteuid = oldRun, oldEUID })
	serveRun = func(ctx context.Context, opts server.Options) error {
		if ctx.Err() != nil {
			t.Error("serve passed a context that has already ended")
		}
		got = append(got, opts)
		return result
	}
	geteuid = func() int { return euid }
	return &got
}

func runArgs(args ...string) (string, int) {
	var out bytes.Buffer
	code := run(args, cli.Options{Stdout: &out, Stderr: &out})
	return out.String(), code
}

// TestExitCodes is register row 30: usage errors exit 2, failed runs 1.
func TestExitCodes(t *testing.T) {
	served := fakeServe(t, 1000, errors.New("the controller database is locked"))
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"version"}, cli.ExitOK, "darkpanel dev"},
		{[]string{"version", "extra"}, cli.ExitUsage, `unknown command "extra"`},
		{[]string{"nope"}, cli.ExitUsage, `unknown command "nope"`},
		{[]string{"serve", "--nope"}, cli.ExitUsage, "unknown flag: --nope"},
		{[]string{"serve", "--dev", "--listen", "http://192.0.2.1:80"}, cli.ExitUsage, "plain HTTP is allowed only on a loopback address"},
		{[]string{"serve", "--dev", "--listen", "ftp://127.0.0.1:21"}, cli.ExitUsage, "is not loopback, tailscale"},
		{[]string{"serve"}, cli.ExitUsage, "serve runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"serve", "--dev"}, cli.ExitError, "darkpanel: the controller database is locked"},
		{[]string{"token", "list", "--help"}, cli.ExitOK, "List"},
		{[]string{"hostinfo", "summary", "get", "--help"}, cli.ExitOK, "--host"},
	}
	for _, tt := range tests {
		out, code := runArgs(tt.args...)
		if code != tt.code || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit %d and %q", strings.Join(tt.args, " "), code, out, tt.code, tt.out)
		}
	}
	if len(*served) != 1 {
		t.Errorf("serve ran %d times, want once (only serve --dev passes its checks)", len(*served))
	}
}

func TestServeDevOptions(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	served := fakeServe(t, 1000, nil)
	if out, code := runArgs("serve", "--dev", "--listen", "loopback", "--listen", "https://127.0.0.1:9443"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if len(*served) != 1 {
		t.Fatalf("serve ran %d times, want once", len(*served))
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Under(filepath.Join(state, "darkpanel-dev")) || !opts.Dev || opts.Log == nil ||
		!slices.Equal(listens, []string{"loopback", "https://127.0.0.1:9443"}) {
		t.Errorf("serve --dev passed %+v, want the dev paths, Dev, a logger and both listen values", opts)
	}
}

func TestServeProductionOptions(t *testing.T) {
	served := fakeServe(t, 0, nil)
	if out, code := runArgs("serve"); code != cli.ExitOK {
		t.Fatalf("serve as root: exit %d: %s", code, out)
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Production() || opts.Dev || !slices.Equal(listens, []string{"loopback", "tailscale"}) {
		t.Errorf("serve passed %+v, want the production paths and the default listeners", opts)
	}
}
```

Replace the whole content of `cmd/darkpanel/version_test.go` with:

```go
package main

import (
	"bytes"
	"encoding/json"
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
)

func runRoot(t *testing.T, args ...string) string {
	t.Helper()
	var out bytes.Buffer
	if code := run(args, cli.Options{Stdout: &out, Stderr: &out}); code != cli.ExitOK {
		t.Fatalf("darkpanel %v: exit %d: %s", args, code, out.String())
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

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./cmd/darkpanel/`
Expected: FAIL (build failure):

```text
# github.com/darkraise/darkpanel/cmd/darkpanel [github.com/darkraise/darkpanel/cmd/darkpanel.test]
cmd/darkpanel/serve_test.go:22:21: undefined: serveRun
cmd/darkpanel/serve_test.go:22:31: undefined: geteuid
cmd/darkpanel/serve_test.go:23:21: undefined: serveRun
cmd/darkpanel/serve_test.go:23:31: undefined: geteuid
cmd/darkpanel/serve_test.go:24:2: undefined: serveRun
cmd/darkpanel/serve_test.go:31:2: undefined: geteuid
cmd/darkpanel/serve_test.go:37:10: undefined: run
cmd/darkpanel/version_test.go:17:13: undefined: run
FAIL	github.com/darkraise/darkpanel/cmd/darkpanel [build failed]
FAIL
```

- [ ] **Step 3: Write the implementation**

Replace the whole content of `cmd/darkpanel/main.go` with:

```go
// Command darkpanel is the Darkpanel controller, agent and CLI.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/web"
)

// serveRun runs the server for serve; tests replace it.
var serveRun = server.Run

// geteuid is the effective uid serve checks; tests replace it.
var geteuid = os.Geteuid

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

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve and version.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd()}
	return cli.NewRoot(opts)
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
		dev         bool
		listenFlags []string
		specs       []listen.Spec
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller and the local agent",
		Args:  cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			var err error
			if specs, err = listen.ParseSpecs(listenFlags); err != nil {
				return err
			}
			if !dev && geteuid() != 0 {
				return errors.New("serve runs as root in production mode; run it with sudo, or add --dev to use per-user development paths")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Resolve(dev)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return serveRun(ctx, server.Options{Paths: p, Listen: specs, Dev: dev, Log: log})
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listenFlags, "listen", nil,
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

// run executes the command line args and returns the process exit code
// (spec section 15). Nil fields of opts are the process's own.
func run(args []string, opts cli.Options) int {
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	root, err := newRoot(opts)
	if err != nil {
		cli.PrintError(stderr, err)
		return cli.ExitError
	}
	root.SetArgs(args)
	if err := root.ExecuteContext(context.Background()); err != nil {
		cli.PrintError(stderr, err)
		return cli.ExitCode(err)
	}
	return cli.ExitOK
}

func main() {
	os.Exit(run(os.Args[1:], cli.Options{}))
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./cmd/darkpanel/` — Expected: PASS
Then run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./cmd/darkpanel/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/main.go cmd/darkpanel/version_test.go cmd/darkpanel/serve_test.go
git commit -m "feat(cmd): run the CLI and serve the controller"
```

### Task 42: End-to-end test

**Files:**
- Test: `internal/controller/server/e2e_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 37, 38 and 39 (`Start`, `Registry`, `LocalHostID`, `SocketPath`; test helpers `testOptions`, `start`, `baseURL`); Contracts › Task 16 (`cli.EnvURL`, `cli.EnvToken`), Task 20 (`cli.NewRoot`, `cli.Options`), Task 29 (enrollment flow), Task 1 (`cli.ExitCode`, `PrintError`); plan 3's `api.CheckOpenAPI`, `api.Routes`, `api.SessionCookiePlain`, `api.CSRFHeader`, `auth.TOTPCode`, `auth.TOTPPeriod`, `auth.TokenPrefix`, `errcode` codes; `schema.OpenAPI`; `registry.Build`.
- Produces: Contracts › Task 42 (tests only).

**Items:** 3, 5, 8, 9, 11, 12, 13, 14

**Depends on:** Task 29, Task 32, Task 35, Task 39

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 2 - risk 0 = 2

Spec section "Verification"'s end-to-end test over one assembled server on temporary paths with port-0 binds. It checks `api.CheckOpenAPI` with the server's registry; creates the first admin through the controller socket with the CLI and a code from `auth.TOTPCode` (row 11); logs in over HTTP; runs the deny-by-default matrix over every documented route and five unrouted paths for GET, POST, PUT, PATCH, DELETE and OPTIONS, with no credentials, an invalid bearer, an invalid cookie, and a session without CSRF (row 12); refuses a break-glass action on the network; reads `hostinfo.summary` through the API (row 9); creates a token whose secret is in the apply response only (row 8); checks bearer-over-cookie precedence; runs `darkpanel hostinfo summary get --json` and `audit list --json` through the CLI with the token (rows 3, 13, 14) with every secret absent or redacted; checks that `GET /api/v1/changes` hides the break-glass changes (row 68); disables and re-enables hostinfo (row 5); and reads `/healthz`. It tests assembled code, so it passes on the parent; the Assumptions record the mutations it catches.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/e2e_test.go`:

```go
package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

// adminPassword is the first admin's password in the end-to-end test.
const adminPassword = "e2e correct horse battery" //nolint:gosec // G101: a test password, not a credential

// e2e drives one assembled server over HTTP and through the CLI.
type e2e struct {
	t       *testing.T
	s       *Server
	clock   *coretest.FakeClock
	url     string
	home    string
	session string // the session cookie's value
	csrf    string
}

// reply is one HTTP answer.
type reply struct {
	status int
	header http.Header
	body   []byte
}

// problemCode is the code of an RFC 9457 answer, "" for any other body.
func (r reply) problemCode() errcode.Code {
	var p struct{ Code errcode.Code }
	if r.header.Get("Content-Type") != "application/problem+json" || json.Unmarshal(r.body, &p) != nil {
		return ""
	}
	return p.Code
}

// credentials of a request: a session cookie, its CSRF token and a bearer
// token, each sent when not empty.
type credentials struct{ session, csrf, bearer string }

func (e *e2e) do(method, path string, body any, c credentials) reply {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			e.t.Fatal(err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, e.url+path, rd)
	if err != nil {
		e.t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.session != "" {
		req.AddCookie(&http.Cookie{Name: api.SessionCookiePlain, Value: c.session}) //nolint:gosec // G124: a request cookie carries only name and value
	}
	if c.csrf != "" {
		req.Header.Set(api.CSRFHeader, c.csrf)
	}
	if c.bearer != "" {
		req.Header.Set("Authorization", "Bearer "+c.bearer)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		e.t.Fatal(err)
	}
	return reply{status: resp.StatusCode, header: resp.Header, body: b}
}

// admin is the logged-in session with its CSRF token.
func (e *e2e) admin() credentials { return credentials{session: e.session, csrf: e.csrf} }

// want fails unless r has the status, decoding a 2xx body into out.
func (e *e2e) want(r reply, status int, out any) {
	e.t.Helper()
	if r.status != status {
		e.t.Fatalf("status %d, want %d: %s", r.status, status, r.body)
	}
	if out != nil {
		if err := json.Unmarshal(r.body, out); err != nil {
			e.t.Fatalf("decode %s: %v", r.body, err)
		}
	}
}

func (e *e2e) wantProblem(r reply, status int, code errcode.Code) {
	e.t.Helper()
	if r.status != status || r.problemCode() != code {
		e.t.Errorf("got %d %s, want %d %s", r.status, r.body, status, code)
	}
}

// change is the part of the API's Change the test reads.
type change struct {
	ID     string          `json:"id"`
	Action string          `json:"action"`
	State  string          `json:"state"`
	Output json.RawMessage `json:"output"`
}

// apply plans and applies a controller-side action with the admin session.
func (e *e2e) apply(name string, input any) change {
	e.t.Helper()
	var plan struct {
		PlanID string `json:"planId"`
	}
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": input}, e.admin()), 200, &plan)
	var ch change
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/apply", map[string]any{"planId": plan.PlanID}, e.admin()), 200, &ch)
	if ch.State != "completed" {
		e.t.Fatalf("%s: change %s is %s", name, ch.ID, ch.State)
	}
	return ch
}

// cli runs the darkpanel CLI in-process, as cmd/darkpanel builds it, with
// the given environment and standard input.
func (e *e2e) cli(env map[string]string, stdin string, args ...string) (stdout, stderr string, code int) {
	e.t.Helper()
	reg, err := registry.Build()
	if err != nil {
		e.t.Fatal(err)
	}
	var out, errs bytes.Buffer
	root, err := cli.NewRoot(cli.Options{
		Registry: reg, Stdin: strings.NewReader(stdin), Stdout: &out, Stderr: &errs,
		Getenv:      func(k string) string { return env[k] },
		UserHomeDir: func() (string, error) { return e.home, nil },
		IsTerminal:  func() bool { return false },
	})
	if err != nil {
		e.t.Fatal(err)
	}
	root.SetArgs(args)
	err = root.ExecuteContext(context.Background())
	cli.PrintError(&errs, err)
	return out.String(), errs.String(), cli.ExitCode(err)
}

var secretLine = regexp.MustCompile(`(?m)^Secret: ([A-Z2-7]+)$`)

// TestEndToEnd is the end-to-end test of spec section "Verification" over
// one assembled server.
func TestEndToEnd(t *testing.T) {
	opts := testOptions(t)
	opts.Hostname = func() (string, error) { return "e2e-host", nil }
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	hostID := e.s.LocalHostID()
	sock := e.s.SocketPath()

	// The OpenAPI document covers every network route with the real
	// registry (register row 69, obligation 5).
	if err := api.CheckOpenAPI(e.s.Registry()); err != nil {
		t.Fatalf("CheckOpenAPI: %v", err)
	}

	// The first admin is created through the controller socket by the CLI,
	// with a code computed from the printed secret (register row 11).
	out, errOut, code := e.cli(nil, adminPassword+"\n", "user", "create", "--username", "admin", "--socket", sock, "--yes")
	if code != cli.ExitUsage || !strings.Contains(errOut, "no code given; activate the user with: darkpanel user activate --username admin") {
		t.Fatalf("user create: exit %d\n%s\n%s", code, out, errOut)
	}
	m := secretLine.FindStringSubmatch(out)
	if m == nil {
		t.Fatalf("user create printed no secret:\n%s", out)
	}
	totpSecret := m[1]
	totp, err := auth.TOTPCode(totpSecret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if out, errOut, code := e.cli(nil, totp+"\n", "user", "activate", "--username", "admin", "--socket", sock, "--yes"); code != cli.ExitOK {
		t.Fatalf("user activate: exit %d\n%s\n%s", code, out, errOut)
	}

	// Log in over HTTP with password and a code of the next step.
	e.clock.Advance(auth.TOTPPeriod)
	totp, _ = auth.TOTPCode(totpSecret, e.clock.Now())
	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "code": totp}, credentials{})
	var login struct {
		CSRFToken string `json:"csrfToken"`
		User      struct {
			ID string `json:"id"`
		} `json:"user"`
	}
	e.want(r, 200, &login)
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.SessionCookiePlain {
			e.session = c.Value
		}
	}
	if e.session == "" || login.CSRFToken == "" {
		t.Fatalf("login set no session cookie or CSRF token: %v %s", r.header, r.body)
	}
	e.csrf = login.CSRFToken

	t.Run("deny by default", func(t *testing.T) { e.denyByDefault(t, hostID) })

	// A break-glass action is refused on the network listener, even for an
	// admin session.
	e.wantProblem(e.do("POST", "/api/v1/actions/core.user.list/v1/read", map[string]any{"input": map[string]any{}}, e.admin()),
		403, errcode.Forbidden)

	// hostinfo.summary through the API.
	resource := "/api/v1/hosts/" + hostID + "/resources/hostinfo/summary"
	var summary struct {
		Hostname         string `json:"hostname"`
		DarkpanelVersion string `json:"darkpanelVersion"`
	}
	e.want(e.do("GET", resource, nil, e.admin()), 200, &summary)
	if summary.Hostname == "" || summary.DarkpanelVersion != "dev" {
		t.Errorf("hostinfo.summary = %+v", summary)
	}

	// A token: its secret is in the apply response only (register row 8).
	created := e.apply("core.token.create", map[string]any{"name": "cli", "scope": "read"})
	var newToken struct {
		ID    string `json:"id"`
		Token string `json:"token"`
	}
	if err := json.Unmarshal(created.Output, &newToken); err != nil || !strings.HasPrefix(newToken.Token, auth.TokenPrefix) {
		t.Fatalf("core.token.create output %s (%v), want a token", created.Output, err)
	}
	token := newToken.Token
	var stored change
	e.want(e.do("GET", "/api/v1/changes/"+created.ID, nil, e.admin()), 200, &stored)
	if !strings.Contains(string(stored.Output), `"token":"[redacted]"`) || strings.Contains(string(stored.Output), token) {
		t.Errorf("stored change output %s, want the token redacted", stored.Output)
	}

	// The bearer token takes precedence over the session cookie.
	var who struct {
		Token *struct {
			ID string `json:"id"`
		} `json:"token"`
		CSRFToken *string `json:"csrfToken"`
	}
	e.want(e.do("GET", "/api/v1/auth/session", nil, credentials{session: e.session, bearer: token}), 200, &who)
	if who.Token == nil || who.Token.ID != newToken.ID || who.CSRFToken != nil {
		t.Errorf("session with a token and a cookie answered %+v, want the token", who)
	}
	e.wantProblem(e.do("GET", "/api/v1/auth/session", nil, credentials{session: e.session, bearer: "dpt_bogus"}), 401, errcode.AuthRequired)

	// The CLI reads hostinfo.summary with the token (register rows 3, 13).
	env := map[string]string{cli.EnvURL: e.url, cli.EnvToken: token}
	out, errOut, code = e.cli(env, "", "hostinfo", "summary", "get", "--json")
	var cliSummary struct {
		Hostname string `json:"hostname"`
	}
	if code != cli.ExitOK || json.Unmarshal([]byte(out), &cliSummary) != nil || cliSummary.Hostname != summary.Hostname {
		t.Errorf("darkpanel hostinfo summary get --json: exit %d\n%s\n%s", code, out, errOut)
	}
	if _, errOut, code := e.cli(map[string]string{cli.EnvURL: e.url}, "", "hostinfo", "summary", "get"); code != cli.ExitAuth {
		t.Errorf("without a token: exit %d (%s), want %d", code, errOut, cli.ExitAuth)
	}

	// The audit log lists the login and the token creation, with every
	// secret redacted or absent (register rows 8, 14).
	out, errOut, code = e.cli(env, "", "audit", "list", "--json")
	if code != cli.ExitOK {
		t.Fatalf("darkpanel audit list --json: exit %d\n%s", code, errOut)
	}
	e.checkAudit(out, login.User.ID, token, totpSecret)

	// Network change listings leave out the break-glass user changes
	// (register row 68).
	var listed struct {
		Changes []change `json:"changes"`
	}
	e.want(e.do("GET", "/api/v1/changes", nil, e.admin()), 200, &listed)
	var actions []string
	for _, c := range listed.Changes {
		actions = append(actions, c.Action)
	}
	if !slices.Equal(actions, []string{"core.token.create"}) {
		t.Errorf("GET /api/v1/changes lists %v, want only core.token.create", actions)
	}

	// A disabled module answers module.unavailable and the others keep
	// working; enabling it again serves it (register row 5).
	module := map[string]any{"hostId": hostID, "moduleId": "hostinfo"}
	e.apply("core.module.disable", module)
	e.wantProblem(e.do("GET", resource, nil, e.admin()), 409, errcode.ModuleUnavailable)
	if _, errOut, code := e.cli(env, "", "hostinfo", "summary", "get"); code != cli.ExitError || !strings.Contains(errOut, "module.unavailable") {
		t.Errorf("CLI read of a disabled module: exit %d %s", code, errOut)
	}
	if _, _, code := e.cli(env, "", "token", "list"); code != cli.ExitOK {
		t.Errorf("token list with hostinfo disabled: exit %d", code)
	}
	e.apply("core.module.enable", module)
	e.want(e.do("GET", resource, nil, e.admin()), 200, nil)

	// /healthz answers the status alone, and details to a token.
	var health struct {
		Status        string `json:"status"`
		Version       string `json:"version"`
		SchemaVersion int    `json:"schemaVersion"`
	}
	e.want(e.do("GET", "/healthz", nil, credentials{}), 200, &health)
	if health.Status != "ok" || health.Version != "" {
		t.Errorf("anonymous /healthz = %+v", health)
	}
	e.want(e.do("GET", "/healthz", nil, credentials{bearer: token}), 200, &health)
	if health.Version != "dev" || health.SchemaVersion < 4 {
		t.Errorf("/healthz with a token = %+v", health)
	}
}

// denyByDefault is register row 12 over the assembled handler: every method
// on every route the OpenAPI document lists, and on paths no route serves,
// refuses a request without valid credentials, and an unsafe method with a
// session but no CSRF token.
func (e *e2e) denyByDefault(t *testing.T, hostID string) {
	doc, err := schema.OpenAPI(e.s.Registry(), api.Routes())
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Paths map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		t.Fatal(err)
	}
	fill := strings.NewReplacer("{host}", hostID, "{id}", "c1")
	targets := []string{"/api", "/api/v1/nope", "/api/v1/hosts/" + hostID + "/nope", "/", "/hosts/" + hostID}
	for p := range d.Paths {
		targets = append(targets, fill.Replace(p))
	}
	slices.Sort(targets)
	if len(targets) < 20 {
		t.Fatalf("only %d paths to check: %v", len(targets), targets)
	}
	public := func(method, path string) bool {
		get := method == "GET"
		switch {
		case path == "/api/v1/auth/login":
			return method == "POST"
		case path == "/healthz":
			return get
		case path == "/api" || strings.HasPrefix(path, "/api/"):
			return false
		}
		return get // the web UI's files and its index fallback
	}
	for _, path := range targets {
		for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"} {
			if public(method, path) {
				continue
			}
			body := map[string]any{"input": map[string]any{}, "planId": "p"}
			for name, c := range map[string]credentials{
				"no credentials": {},
				"invalid bearer": {bearer: "dpt_01J_bogus"}, //nolint:gosec // G101: a malformed token, not a credential
				"invalid cookie": {session: "bogus"},
			} {
				r := e.do(method, path, body, c)
				if r.status != 401 || r.problemCode() != errcode.AuthRequired {
					t.Errorf("%s %s with %s: %d %s, want 401 auth.required", method, path, name, r.status, r.body)
				}
			}
			if method != "GET" && method != "OPTIONS" {
				r := e.do(method, path, body, credentials{session: e.session})
				if r.status != 403 || r.problemCode() != errcode.CSRF {
					t.Errorf("%s %s with a session and no CSRF token: %d %s, want 403 auth.csrf", method, path, r.status, r.body)
				}
			}
		}
	}
}

// checkAudit reads `darkpanel audit list --json`: the login and the token
// creation are there, the user changes' secrets are redacted, and neither
// the token, the TOTP secret nor the password appears.
func (e *e2e) checkAudit(out, userID, token, totpSecret string) {
	t := e.t
	for _, s := range []string{token, totpSecret, adminPassword} {
		if strings.Contains(out, s) {
			t.Errorf("audit list output contains a secret (%d characters)", len(s))
		}
	}
	var list struct {
		Entries []struct {
			Actor  string          `json:"actor"`
			Action string          `json:"action"`
			Phase  string          `json:"phase"`
			Result string          `json:"result"`
			Source *string         `json:"source"`
			Input  json.RawMessage `json:"input"`
		} `json:"entries"`
	}
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		t.Fatalf("audit list: %v\n%s", err, out)
	}
	found := map[string]string{}
	for _, en := range list.Entries {
		key := en.Action + " " + en.Phase + " " + en.Result
		found[key] = en.Actor
		switch en.Action {
		case "core.user.create":
			if !strings.Contains(string(en.Input), `"password":"[redacted]"`) {
				t.Errorf("core.user.create audit input %s, want the password redacted", en.Input)
			}
			if en.Source == nil || *en.Source != "socket" {
				t.Errorf("core.user.create audit source %v, want socket", en.Source)
			}
		case "core.user.activate":
			if !strings.Contains(string(en.Input), `"code":"[redacted]"`) {
				t.Errorf("core.user.activate audit input %s, want the code redacted", en.Input)
			}
		}
	}
	socketActor := "local:root@" + e.s.LocalHostID()
	if uid := os.Getuid(); uid != 0 {
		socketActor = fmt.Sprintf("local:uid-%d@%s", uid, e.s.LocalHostID())
	}
	for key, actor := range map[string]string{
		"core.auth.login login ok":    "user:" + userID,
		"core.token.create apply ok":  "user:" + userID,
		"core.user.create apply ok":   socketActor,
		"core.user.activate apply ok": socketActor,
	} {
		if got, ok := found[key]; got != actor {
			t.Errorf("audit entry %q: actor %q (present %v), want %q", key, got, ok, actor)
		}
	}
	for key := range found {
		if strings.HasSuffix(key, " error") {
			t.Errorf("audit has an unexpected failed entry %q", key)
		}
	}
}
```

- [ ] **Step 2: Run the tests**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run TestEndToEnd ./internal/controller/server/`
Expected: PASS (the tests guard code that exists already):

```text
ok  	github.com/darkraise/darkpanel/internal/controller/server	<time>
```

- [ ] **Step 3: Write the implementation**

No implementation: Tasks 35, 37, 38, 39, 40 and 41 and the merged parts are what the test exercises.

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/server/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/e2e_test.go
git commit -m "test(server): add the end-to-end test"
```

### Task 43: README and CONTRIBUTING: run, first admin, web root, CLI token

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`

**Interfaces:**
- Consumes: Contracts › Task 41 (`serve`, its flags and refusals); Part B's commands (`user create`, `user activate`, `user reset-totp`, `controller web-ca`, `token revoke`, `audit list`, the config file `cli.json`, `DARKPANEL_URL`, `DARKPANEL_TOKEN`); plan 3's routes (`POST /api/v1/auth/login`, plan and apply of `core.token.create`, `X-CSRF-Token`).
- Produces: nothing.

**Depends on:** Task 41

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

The README explains how to run `serve`, create the first admin through the socket, trust the web root (browser and `caFingerprint`), and get an API token for the CLI through the API (the CLI has no login command, by the controller's ruling); CONTRIBUTING adds `make schemas`, how to run a development server with `--dev`, and the snapshot rule. The commands were run against a server built from this tree (see Assumptions).

- [ ] **Step 1: Write the documents**

Replace the whole content of `README.md` with:

````markdown
# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M1 (Foundations). `darkpanel serve` runs the
controller and the local agent, serves the REST API with TOTP login and the
host-information module, and the CLI drives it. The web UI's pages are not
built yet, and nothing installs the service; do not use it on a server you
care about.

## Prerequisites

- Go 1.27.2
- Node 24
- pnpm 11.17.0
- GNU make

## Build

```sh
make build
```

This builds the web UI, then two static Linux binaries with the UI embedded:

- `bin/darkpanel-linux-amd64`
- `bin/darkpanel-linux-arm64`

`bin/darkpanel-linux-amd64 version` prints the version the binary was built
from.

## Run

In M1 the service runs as root:

```sh
sudo darkpanel serve
```

It keeps its databases under `/var/lib/darkpanel/`, its keys under
`/etc/darkpanel/` and its controller socket under `/run/darkpanel-controller/`,
and serves HTTP on `127.0.0.1:8080` and `[::1]:8080` and HTTPS on port 8443 of
each Tailscale address. `--listen` replaces those listeners and may be
repeated: `loopback`, `tailscale`, `https://<ip>:<port>` or
`http://<loopback ip>:<port>`. Reach the loopback listener from another
machine through SSH port forwarding: `ssh -L 8080:127.0.0.1:8080 <host>`.

### The first admin

There is no default account and no setup page. Create the first admin on the
host itself, through the controller socket:

```sh
sudo darkpanel user create --username admin
```

It asks for the password twice, shows a QR code and the TOTP secret for an
authenticator app, and asks for a code to activate the user. The secret is not
shown again; `sudo darkpanel user reset-totp --username admin` issues a new
one.

### Trust the web root

HTTPS listeners present a certificate from the controller's own web root.
Print it and its SHA-256 fingerprint:

```sh
sudo darkpanel controller web-ca
```

Import the PEM block into your browser's or system's trusted roots. To let the
CLI verify the HTTPS listeners against it, put the fingerprint in
`~/.config/darkpanel/cli.json`, which must have mode 0600:

```json
{"url": "https://100.64.0.1:8443", "caFingerprint": "<fingerprint>"}
```

### Use the CLI with a token

The CLI has no login command. It authenticates with an API token, which you
create once with your session: on the web UI's Tokens page when the UI is
built, or through the API. With the API, log in, then plan and apply
`core.token.create`, passing the `csrfToken` of the login answer:

```sh
URL=http://127.0.0.1:8080
curl -c session -H 'Content-Type: application/json' \
  -d '{"username": "admin", "password": "<password>", "code": "<code>"}' $URL/api/v1/auth/login
curl -b session -H 'Content-Type: application/json' -H 'X-CSRF-Token: <csrfToken>' \
  -d '{"input": {"name": "laptop", "scope": "admin"}}' $URL/api/v1/actions/core.token.create/v1/plan
curl -b session -H 'Content-Type: application/json' -H 'X-CSRF-Token: <csrfToken>' \
  -d '{"planId": "<planId>"}' $URL/api/v1/actions/core.token.create/v1/apply
rm session
```

The apply answer's `output.token` is shown only this once. Give it to the CLI
in `DARKPANEL_TOKEN` or as `"token"` in `cli.json`:

```sh
export DARKPANEL_URL=http://127.0.0.1:8080 DARKPANEL_TOKEN=<token>
darkpanel hostinfo summary get
darkpanel audit list
darkpanel --help
```

A token's scope is `read`, `operate` or `admin`, and it expires after 90 days
unless `expiresInDays` says otherwise; `darkpanel token revoke --id <id>`
revokes one.

[CONTRIBUTING.md](CONTRIBUTING.md) explains the make targets and how to run a
development server. [SECURITY.md](SECURITY.md) explains how to report a
vulnerability.

## License

MIT; see [LICENSE](LICENSE).
````

Replace the whole content of `CONTRIBUTING.md` with:

````markdown
# Contributing to Darkpanel

## Prerequisites

- Go 1.27.2. The build sets `GOTOOLCHAIN=local`, so it never downloads another
  Go toolchain.
- golangci-lint v2.15.0
- Node 24 and pnpm 11.17.0
- GNU make

## Make targets

| Target | What it runs |
|---|---|
| `make web` | installs the web dependencies from the lockfile and builds the UI into `web/dist/` |
| `make build` | `make web`, then static `linux/amd64` and `linux/arm64` binaries in `bin/` |
| `make verify-build` | checks that both binaries are static ELF for the right architecture and embed the UI |
| `make lint-go` | `golangci-lint run` and `go vet ./...` |
| `make lint-web` | ESLint and the TypeScript type check |
| `make lint` | `lint-go` and `lint-web` |
| `make test-go` | `go test ./...` |
| `make test-web` | the Vitest suite |
| `make test-adr` | the ADR checker's tests and the ADR structural check |
| `make test` | `test-go`, `test-web` and `test-adr` |
| `make schemas` | regenerates the action and resource schema snapshots in `schemas/` |
| `make vuln` | govulncheck v1.9.0 on every Go package |
| `make check` | `lint`, `test`, `build`, `verify-build` and `vuln` |
| `make clean` | removes `bin/` and the built UI |

`make build` is the one build command. CI runs these same targets.

## Run a development server

`--dev` keeps everything under your own user: the databases and keys in
`$XDG_STATE_HOME/darkpanel-dev/` (`~/.local/state/darkpanel-dev/` by default)
and the controller socket in `$XDG_RUNTIME_DIR/darkpanel-dev/`. In `--dev` the
socket also accepts your own uid, so the break-glass commands need no `sudo`.

```sh
make web    # once, so the binary embeds the UI
go run ./cmd/darkpanel serve --dev --listen loopback
```

In another terminal, create the first admin through the socket and print the
web root:

```sh
go run ./cmd/darkpanel user create --username admin --dev
go run ./cmd/darkpanel controller web-ca --dev
```

Then create an API token for the CLI as the README's "Use the CLI with a
token" shows. A second `serve` on the same paths
fails while the first runs; stop the first with Ctrl-C. Delete
`~/.local/state/darkpanel-dev/` to start again from nothing.

## Schemas

Every action and resource has a committed schema snapshot under `schemas/`.
After changing an action's or resource's input or output type, run
`make schemas` and commit the result; `go test ./internal/modules/` fails while
a snapshot is missing or out of date. `schemas/released/<tag>/` will hold the
snapshots of released versions, which a change may only extend (ADR 0002).

## Before you open a pull request

Every change passes `make check`. On a fresh clone, run
`pnpm -C web install --frozen-lockfile` first: `make check` runs the web lint
and tests before `make build` installs the web dependencies.

## Commit messages

Commit messages use `<type>(<scope>): <subject>`:

- `<type>` is one of `feat`, `fix`, `docs`, `style`, `refactor`, `test`,
  `chore`, `perf`;
- `<subject>` is imperative, at most 50 characters, with no trailing period.

## Decisions

Architecture decisions are recorded as ADRs in [docs/adr/](docs/adr/). An
accepted ADR changes only through a new ADR that supersedes it.

## Language

Code, comments, documentation, commit messages and error messages are written
in English.
````

- [ ] **Step 2: Run the checks**

Run: `GOTOOLCHAIN=local timeout 300 go vet ./...` — Expected: no output
Then run: `GOTOOLCHAIN=local timeout 900 go test -count=1 ./...` — Expected: every line `ok` or `?`
Then run: `GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/modules/` — Expected: `ok`
Then run: `GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` — Expected: `0 issues.`
Then run: `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` — Expected: no output

- [ ] **Step 3: Commit**

```bash
git add README.md CONTRIBUTING.md
git commit -m "docs(readme): document serve and the CLI token"
```

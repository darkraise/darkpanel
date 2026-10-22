# M1 Framework Libraries Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build parts P1, P2, P3, P4 and P6 of the M1 platform core (plan 2 of 6 in the spec's "Plans" section): the key files, sealer and redaction; the host execution providers and ownership manifest; schema reflection, validation, snapshots, compatibility checking and OpenAPI; the change lifecycle engine with the agent journal; and the host-information module, so that plans 3 and 4 can wire them into the API, dispatch and server.

**Architecture:** Five packages that share no file and import only the foundation (`internal/core`, `internal/core/store`, `internal/core/errcode`, `internal/core/coretest`, `internal/buildinfo`, `internal/registry`): `internal/core/secret`, `internal/platform`, `internal/core/schema` (with the `schemagen` command), `internal/core/lifecycle` and `internal/modules/hostinfo`. Where one part needs another's behaviour (the engine needs redaction and validation), it takes a function or a `core` interface and is tested with a fake; plan 4 joins them.

**Tech Stack:** Go 1.27.2, modernc.org/sqlite v1.60.1, github.com/invopop/jsonschema v0.14.0, github.com/santhosh-tekuri/jsonschema/v6 v6.0.3, golang.org/x/crypto v0.58.0, golang.org/x/sys v0.49.0, github.com/oklog/ulid/v2 v2.1.2, golangci-lint v2.15.0, GNU make.

**Spec:** docs/dforge/specs/2026-10-17-m1-platform-core-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves.

**Plan review:** 2026-10-17 — dr-superpowers:judge-opus — executability 17 / coherence 18 / coverage 15 / assumptions 16 (round 1)

**Parallelism:** waves — 1-3 | 4-5 | 6 | 7 | 8-9 | 10 | 11 | 12 | 13 | 14 | 15 | 16 | 17 | 18 | 19 | 20

**Integration check:** `timeout 1500 make check`

**Worktree setup:** `timeout 300 pnpm -C web install --frozen-lockfile`

## Global Constraints

- The spec is the source of truth, including its "Owner decisions" section; this plan's Assumptions record where it departs and why. Where a step gives a file's complete content, write exactly that content.
- No dependency changes: every module this plan imports is already pinned in `go.mod` by the foundation (`internal/deps/deps.go` keeps them until plan 4 removes it). No task edits `go.mod` or `go.sum`, and `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing after every task.
- Every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001). The `Makefile` exports it; a `go` command typed outside `make` is prefixed with it, as every step here does.
- A task edits only the files in its own `**Files:**` block.
- In a waves run every task executes in its own git worktree created by the controller and commits only there; the controller merges a wave's worktrees before starting the next wave.
- `**Items:**` numbers are row numbers of the register `docs/dforge/registers/2026-10-17-m1-platform-core.md`.
- Test isolation (spec "Parallel decomposition"): every Go test uses `t.TempDir()` for databases, keys and files, opens no network port, writes no fixed path outside its temporary directory, and uses `coretest.FakeClock` for timers and expiry. The exec tests run only standard programs present on every Linux host and CI runner (`/usr/bin/env`, `/usr/bin/printf`, `/usr/bin/cat`, `/usr/bin/sleep`, `/usr/bin/yes`, `/usr/bin/false`) and the test binary itself as a helper process. No test in this plan shares a file, port or database with another task's tests.
- `golangci-lint run` reports `0 issues.` after every task: every exported identifier has a doc comment (revive), files are `gofmt`- and `goimports`-clean, and a `//nolint` carries the rule and a reason, as the code below shows.
- Every build and test command carries an explicit `timeout`.
- Redaction marker: every redacted secret value is the JSON string `"[redacted]"` (`secret.Redacted`).
- Never commit `.superpowers/`.
- English only. Commits use `<type>(<scope>): <subject>` with type one of feat, fix, docs, style, refactor, test, chore, perf; the whole first line is at most 50 characters, imperative, no trailing period; no AI attribution of any kind.
- `Makefile` recipe lines begin with a tab character, never spaces.

## Contracts

The foundation's names (`core`, `store`, `errcode`, `coretest`, `buildinfo`, `registry`) are those of `docs/dforge/plans/2026-10-17-m1-platform-foundation.md` › Contracts, as merged on `main`. This plan adds the following; plans 3 and 4 code against them.

**`internal/core/secret`** (package `secret`, P1)

- Task 10 (`key.go`, carries the package doc comment): `const KeyPrefix = "darkpanel-key-v1:"`; `const KeySize = 32`; `func LoadOrCreateKey(path string) ([]byte, error)` — creates a missing key (32 random bytes written as `darkpanel-key-v1:<std base64>\n` to a temporary file in the same directory with mode 0600, fsynced, `link(2)`ed to `path` so an existing key is never replaced — on `EEXIST` the existing file is loaded — then the temporary name unlinked and the directory fsynced) and loads it; loading refuses a symlink, a non-regular file, a mode allowing any group or other access, an owner other than the effective uid, and content that is not exactly one prefixed base64 line of 32 bytes.
- Task 13 (`sealer.go`): `type Sealer struct` implementing `core.Sealer`; `func NewSealer(key []byte) (*Sealer, error)` (refuses a key that is not 32 bytes). Ciphertext layout: version byte `1`, 8-byte key id (first 8 bytes of SHA-256 of the key), 24-byte random nonce, XChaCha20-Poly1305 sealed data with associated data `ad`. `Open` refuses an unknown version, a different key id, a short input or a tag mismatch. `Derive(label)` returns 32 bytes of HKDF-SHA256 (standard library `crypto/hkdf`) over the key with `info = label` and no salt. `const CSRFLabel = "darkpanel csrf v1"`. The layout and the associated-data strings (`<table>.<column>.<row id>`) are a stored format.
- Task 15 (`redact.go`): `const Redacted = "[redacted]"`; `func RedactJSON(raw, schema json.RawMessage) json.RawMessage` — `schema` is a JSON Schema document as `internal/core/schema` produces it (`$ref` of the form `#/$defs/<name>`); every value whose schema carries `"x-secret": true` becomes `"[redacted]"`; objects, arrays (element by element), `$ref`, `oneOf`, `anyOf` and `allOf` are followed. It fails closed: a value whose schema cannot be resolved, an object property or array element no schema describes (including properties admitted only by an absent or `true` `additionalProperties`), anything inside an object or array under the free-form schema `true`, and input that is not valid JSON are redacted. The result is compact JSON with sorted object keys and numbers kept as written.

**`internal/platform`** (package `platform`, P2)

- Task 2 (`blocks.go`, carries the package doc comment): unexported `replaceBlock` and `removeBlock` used by Task 19; markers are `<prefix> BEGIN DARKPANEL <module>/<block>` and `<prefix> END DARKPANEL <module>/<block>` on lines of their own, default prefix `#`; duplicated, nested or unterminated markers fail with `errcode.FilesMarkersBad`.
- Task 17 (`exec.go`): `type Runner struct` implementing `core.Runner`; `func NewRunner(allowlist []string) *Runner` (absolute paths only; a relative entry panics, a programming error); `func ProductionAllowlist() []string` returns a new empty slice in M1. `Run` refuses a path not in the allowlist before starting anything; runs in `/` with exactly the environment `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `LANG=C.UTF-8`, `LC_ALL=C.UTF-8`; applies a 30 s timeout when `ctx` has no deadline; a non-zero exit is returned as `CmdResult.ExitCode` with a nil error; output above 4 MiB on either stream stops the program and fails with `*errcode.Error` code `errcode.ExecOutputTooLarge`.
- Task 18 (`ownership.go`): `type Ownership struct` implementing `core.Ownership` over the agent table `core_owned_files`; `func NewOwnership(db *store.DB) *Ownership`; `Record` of an existing (path, block id) pair is a no-op that keeps the first row; file entries store an empty block id. Migration `internal/core/store/migrations/agent/0003_ownership.sql`.
- Task 19 (`files.go`): `type FilesConfig struct { Owned map[string][]core.OwnedPath; Ownership core.Ownership; Clock core.Clock; TestMode bool }` (`Owned` is keyed by module id; `TestMode` makes the required parent owner the effective uid instead of root and must stay false in production); `type Files struct` implementing `core.FileWriter`; `func NewFiles(cfg FilesConfig) *Files`; `func OwnedPaths(mods []core.Manifest) map[string][]core.OwnedPath`. A path or block not declared for the calling module, or an unclean or relative path, is refused before anything is opened. Ownership is recorded before the write. `ReplaceBlock` creates a missing file with mode 0644; `RemoveBlock` and `Remove` treat a missing file or block as success; `Remove` unlinks a symlink at the path without following it.

**`internal/core/schema`** (package `schema`, P3)

- Task 1 (`reflect.go`): `func Input(t reflect.Type) (json.RawMessage, error)` and `func Output(t reflect.Type) (json.RawMessage, error)` return draft 2020-12 schemas from invopop: every field required unless its JSON tag has `omitempty`; input objects carry `"additionalProperties": false`, output objects do not restrict additional properties; a field tagged `secret:"true"` gets `"x-secret": true`, plus `"writeOnly": true` in input schemas; `jsonschema` struct tags supply patterns, enums, minimum and maximum. Named struct types appear under `$defs` and are referenced as `#/$defs/<Go type name>`. Output is deterministic (same type, same bytes).
- Task 4 (`compare.go`): `func Compare(prev, next []byte) ([]string, error)` compares two action snapshot files (Task 8's format) and returns one message per violation — removed field, changed type, changed default or constraint (pattern, enum set, minimum, maximum, format), new required input field, removed enum value, new input enum value, changed class, side or break-glass flag, changed `x-secret` or `writeOnly` — and nothing for allowed changes (new optional input field, new output field, new output enum value); `func CheckReleased(releasedDir string, current map[string][]byte) error` compares every `<releasedDir>/<tag>/<path>` file with `current[<path>]` (a missing current file is a violation: a released version was removed) and returns every violation joined, or nil when `releasedDir` does not exist.
- Task 7 (`validate.go`): `type Validator struct` implementing `core.Validator`; `func NewValidator() *Validator`; each action's input schema is compiled once (santhosh-tekuri) and cached per `core.ActionRef`; failures return `*errcode.Error` with code `errcode.InvalidInput` and one `errcode.FieldError{Path, Message}` per failing leaf, `Path` a JSON pointer such as `/port` (a missing or unknown property points at the property itself, the root is `""`); an empty body, invalid JSON or trailing data also fail with `errcode.InvalidInput`; no message quotes the submitted value, so the API copies `Fields` into the problem's `errors` as they are.
- Task 8 (`snapshot.go`): `func ActionSnapshot(def core.ActionDef) ([]byte, error)` returns `{"name","version","class","side","breakGlass","input","output"}`; `func ResourceSnapshot(def core.ResourceDef) ([]byte, error)` returns `{"name","output"}`; both indented with two spaces and ending in a newline. `func Snapshots(reg *core.Registry) (map[string][]byte, error)` keys them `<action name>/v<n>.json` and `<resource name>/v1.json`. `func WriteSnapshots(dir string, files map[string][]byte) error` creates `dir` when missing, writes every file and deletes stale `.json` files outside `<dir>/released/`.
- Task 9 (`openapi.go`): `type Route struct { Method, Path, Summary string; Public bool }` (a hand-written route of spec section 12, passed in by the API package); `func OpenAPI(reg *core.Registry, routes []Route) ([]byte, error)` returns an OpenAPI 3.1 document: the routes exactly as passed, plus per non-break-glass action `POST /api/v1/hosts/{host}/actions/<name>/v<n>/<op>` (agent side) or `POST /api/v1/actions/<name>/v<n>/<op>` (controller side), where `<op>` is `read` for read actions and `plan` and `apply` otherwise, plus per resource `GET /api/v1/hosts/{host}/resources/<module>/<resource>`; operations reuse the action schemas; components `Plan`, `Change`, `ApplyRequest` and `Problem` mirror spec section 12's shapes (empty `diff` and `guards` are `[]`, never `null`); non-public operations declare the `session` (cookie) and `token` (bearer) security schemes. Plan 4's route-coverage test matches each mux pattern against the document's paths segment by segment, a `{x}` segment matching any segment.
- Task 11 (`schemagen/main.go`, `Makefile`): `go run ./internal/core/schema/schemagen -out <dir>` builds `registry.Build()` and calls `WriteSnapshots`. Make target `schemas` runs it with `-out schemas`. The committed `schemas/` directory is plan 4's.
- Test helpers shared between tasks: `fixture_test.go` (Task 1: `fixtureAddress`, `fixtureCredential`, `fixtureAuth`, `fixtureInput`, `fixtureOutput`) and `module_test.go` (Task 7: `fixtureModule`, `fixtureHandler`, `fixtureEmpty`, `fixtureStatus`, `fixtureRegistry(t)`, `fixtureAction(t, name)`), used by Tasks 7, 8 and 9.

**`internal/core/lifecycle`** (package `lifecycle`, P4)

- Task 6 (`journal.go`, migrations): controller `internal/core/store/migrations/controller/0002_lifecycle.sql` (`core_plans`, `core_changes`, `core_locks`) and agent `internal/core/store/migrations/agent/0002_lifecycle.sql` (the same three plus `core_journal`). `const JournalCap = 10000`; `type AgentJournal struct` implementing `core.Journal`; `func NewAgentJournal(db *store.DB) *AgentJournal`; `(*AgentJournal) Entries(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error)` (ascending seq; a limit of 0 or less means 1000); `(*AgentJournal) Ack(ctx context.Context, seq int64) error` deletes entries up to `seq`. When an append leaves more than `JournalCap` entries, the oldest are deleted and one entry with actor `system:journal`, phase `interrupt`, result `error`, error code `journal.overflow` and input `{"dropped":N}` (the running total) records the loss; only one such entry exists at a time.
- Task 12 (`engine.go`): `type Config struct { Side core.Side; Registry *core.Registry; Validator core.Validator; Journal core.Journal; Redact func(t reflect.Type, raw json.RawMessage) json.RawMessage; Base core.Env }` — `Base.DB`, `Base.Sealer`, `Base.Clock` and `Base.Log` are the engine's own and must be set; `Base.HostID` is the agent's host id (empty on the controller); every handler call receives `Base` with `Principal` set to the caller. `func New(cfg Config) (*Engine, error)`. Methods `Read(ctx, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error)`, `Plan(ctx, p, ref, input) (core.PlanView, error)` (`Diff` and `Guards` never nil), `Change(ctx, changeID string) (core.ChangeView, error)`, `Changes(ctx, f core.ChangeFilter) ([]core.ChangeView, error)` (newest first, default limit 100, at most 500, `Before` is a change id), `Close()` (stops every armed timer). Only actions whose `Spec.Side` equals `Config.Side` are served; any other ref answers `action.unknown`. `const PlanTTL = 10 * time.Minute`. One engine per database: apply's checks, snapshot and intent insert are serialised in process. Handlers write diff lines, guard messages and error details as free text the engine cannot redact by schema, and the engine stores, returns and logs them as written: a handler must never put a secret value in them (plan 3's identity handlers, which take passwords and TOTP codes, rely on this rule).
- Task 14 (`apply.go`): `Apply(ctx, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error)` for every mutating class, with ADR 0004's ordering, lock keys, the typed confirmation, the plan principal check (by `Actor`) and repeated-apply semantics of spec section 3; a revertible change runs up to `pending`. The returned view's `Output` is the handler's raw output; the stored change, every later view and the journal carry `Redact(OutputType, output)`. Once intent is persisted the request context's cancellation no longer applies. `const DefaultConfirmTimeout = 60 * time.Second`, `MinConfirmTimeout = 10 * time.Second`, `MaxConfirmTimeout = 15 * time.Minute`; a timeout outside the bounds fails with `request.invalid_input` at path `/confirmTimeoutSeconds`.
- Task 16 (`revert.go`): `Confirm(ctx, p, changeID)`, `Revert(ctx, p, changeID)`, revert timers and `ApplyOptions.ConfirmNow`; `const ActorRevertTimer = "system:revert-timer"`. A revert claims the change by moving it from `pending` back to `applying` in one transaction, so a racing confirm gets `change.not_pending` and a crash mid-revert is reverted again by recovery.
- Task 20 (`recover.go`): `(*Engine) Recover(ctx context.Context) error`, run once before serving (spec section 3's recovery rows; `const ActorRecovery = "system:recovery"`); an interrupted change carries error code `internal` with a message naming what verify reported; `(*Engine) Purge(ctx context.Context) error` deletes expired plans that have no change. Plan 4's server calls `Recover` at start, `Purge` at start and every 10 minutes, and `Close` on shutdown.
- Test helpers shared between tasks: `journal_test.go` (Task 6: `openDBAt`, `openDB`); `fixture_test.go` (Task 12: the fixture module, `newFixture`, `config`, `open`, `plan`, `testRedact`, `wantCode`, `alice`, the action refs, `errCrash`, `testHost`); `apply_test.go` (Task 14: `bob`, `apply`, `phases`, `count`, `planInputCleared`); `revert_test.go` (Task 16: `pending`, `state`).

**`internal/modules/hostinfo`** (package `hostinfo`, P6)

- Task 3 (`parse.go`): unexported parsers `parseOSRelease`, `parseCPUInfo`, `parseMemInfo`, `parseLoadAvg`, `parseUptime`, each with a fuzz target; fixtures `testdata/os-release/<os>` and `testdata/proc-<arch>/`; test helper `readFixture` in `parse_test.go`, used by Task 5.
- Task 5 (`hostinfo.go`): `const ID = "hostinfo"`; `type Summary struct` (JSON fields of spec section 16, with `OS`, `CPU`, `Memory`, `Swap`, `Load` and `RootFS`); `type Source struct { FS fs.FS; Hostname func() (string, error); Statfs func(path string) (totalBytes, availableBytes uint64, err error); Version func() string }`; `func New(src Source) core.Module`; `func Module() core.Module` (the production source: `os.DirFS("/")`, `os.Hostname`, `unix.Statfs`, `buildinfo.Version`). The module is id `hostinfo`, version 1, `DefaultEnabled`, no actions, no migrations, one resource `hostinfo.summary`, one preflight check `meminfo` that reads `proc/meminfo`. `arch` is `runtime.GOARCH`; `bootTime` is `env.Clock` minus the whole seconds of uptime, so the caller must set `env.Clock`. Plan 4 registers it in `internal/modules/modules.go`.

## Assumptions (evidence)

- Every task's code below was run on 2026-10-17 in scratch clones of `main` at `28ad0f7`, one per part: after each task, `go vet ./...`, `go test -count=1 ./...`, `go test -race` on the part's package, `golangci-lint run ./...` (v2.15.0, `0 issues.`) and `go mod tidy -diff` (no output) passed, and each task's commit was checked on a tree holding only its own dependencies. The plan's code blocks were generated from those files by script. The five parts merged in one tree without conflict and passed `timeout 1500 make check` (exit 0: lint `0 issues.`, every Go and Vitest package ok, build, verify-build, `No vulnerabilities found.`), `go test -race -count=1 ./...` and `go mod tidy -diff` (no output).
- `codex off — quota` (`codex-gate usable=false reason=quota`, 2026-10-17, resets 2026-10-22); every task runs on a Claude implementer and every review seat is a Claude judge.
- Plan-time ruling, spec section 3: the input hash is SHA-256 of `Redact(InputType, <input re-encoded from the typed struct>)`, so secret fields hash as the shared marker `"[redacted]"` rather than spec section 3's `"[secret]"`; the property the spec asks for (nothing about a secret leaks through the hash) holds, and one marker serves the hash, the journal and the audit log.
- Plan-time ruling, spec section 3 ("confirm and revert accept any principal whose scope allows the action"): the scope check belongs to the API and dispatch (ADR 0004: "the controller re-authorizes the caller on apply and confirm; the agent trusts the controller"); the engine checks only that apply's principal `Actor` equals the plan's. Module availability (`module.unavailable`) is likewise dispatch's check.
- Plan-time ruling, spec section 3: the engine takes `Redact` and `core.Validator` as injected dependencies, so the lifecycle part shares no code with the secret and schema parts and can run in parallel with them; plan 4 passes `schema.NewValidator()` and a redact function that calls `secret.RedactJSON(raw, schema.Output(t))`, used for input and output types alike (both schemas carry `x-secret`).
- Plan-time ruling, spec section 3 ("`interrupted` (verify output kept)"): `errcode` has no interrupted code and is the foundation's, so an interrupted change carries code `internal` and a message stating whether verify passed, failed (with its error) or could not run.
- Plan-time ruling, spec section 3: a revert claims a pending change by moving it back to `applying` before it touches the host, so confirm loses the race in one transaction and a crash mid-revert is recovered; a reader can see `applying` for the duration of a revert.
- Plan-time ruling, spec section 5 ("audit, journal, logs, diffs and error messages pass through [redaction]"): the engine redacts by schema what it can see as typed JSON — the journal input, the input hash and the stored output. Diff lines, guard messages and handler error details are free text written by the action's own handler, which knows which of its values are secret; the rule that a handler never writes a secret into them is a Contracts entry (Task 12) that plan 3's identity handlers must meet, and their reviews check it. Input decoding never reaches these texts with secret bytes: the validator writes its own messages and `core`'s decoder keeps the cause out of the detail.
- Plan-time ruling, spec sections 3 and 20 ("expired plans are deleted"): `Purge` deletes only expired plans that never became a change. A plan with a change must stay, because a repeated apply returns that change (spec section 3) and `core_changes.plan_id` references it; its sealed input is already cleared once the change has a result, so only the hash remains.
- Plan-time ruling, spec section 5: `RedactJSON` fails closed more widely than the spec's "a value whose schema it cannot resolve": unknown properties and free-form contents are redacted too, so the controller must redact with its own binary's schemas. `Derive` uses the standard library's `crypto/hkdf`, which `golang.org/x/crypto/hkdf` v0.58.0 wraps (`hkdf/hkdf.go` imports it); a test pins the derivation to RFC 5869.
- Plan-time ruling, ADR 0002: `Compare` also refuses a new input enum value, a changed side or break-glass flag and a changed `x-secret` or `writeOnly` keyword, all of which change what a published version accepts or how it is handled; ADR 0002 permits new enum values only in outputs.
- Plan-time ruling, spec section 7: `WriteSnapshots` is exported so the generator holds no logic of its own and the write-and-prune behaviour is unit-tested; the generator task was split from the snapshot task under Rule S.
- Task 7 (input validation) is scored risk 3: it is the only check that enforces input patterns before a value can reach a program (ADR 0009), so it runs alone in its wave.
- `openat2(RESOLVE_NO_SYMLINKS)` works on the development host (kernel `7.0.14-19-pve`, `uname -r`, 2026-10-17; `unix.Openat2` in `golang.org/x/sys` v0.49.0). On GitHub's ubuntu-24.04 runners it is unverified — Task 19's tests verify it when the branch's CI runs them (gate 6, before merge); those runners' 6.x kernels exceed ADR 0009's 5.6 floor.
- The platform tests pass as root and as uid 65534 (`setpriv --reuid=65534 --regid=65534 --clear-groups ./platform.test`, 2026-10-17); the key test that needs a foreign-owned file and the file-writer cases that chown run only as root and skip elsewhere, so CI runs the rest.
- Host-information fixtures: Debian 13's `os-release` is this host's (`DEBIAN_VERSION_FULL=13.5`); Ubuntu 22.04.5, Ubuntu 24.04.3 and Debian 12 were written from those releases' published files without fetching them, and the `cpuinfo` and `meminfo` fixtures are representative cloud VMs (Xeon Platinum 8259CL amd64, Neoverse N1 arm64). The parsers do not depend on exact values; plan 6's harness reads the real files on all four operating systems.
- Register rows 2 (R6) and 8 (R12) are assigned here and their acceptance has a second half in later plans: the committed snapshot test (plan 4) and the secret output returned once and redacted in audit (plans 3 and 4); finishing this plan records them `verify`.

## Task index

1. Schema reflection
2. Marked blocks
3. Host-information parsers
4. Schema compatibility
5. Host-information module
6. Lifecycle tables and agent journal
7. Input validation
8. Schema snapshots
9. OpenAPI document
10. Key files
11. Schema generator
12. Plans and reads
13. Sealer
14. Apply
15. Redaction
16. Revertible changes
17. Exec runner
18. Ownership manifest
19. File writer
20. Recovery and purge

---

### Task 1: Schema reflection

**Files:**
- Create: `internal/core/schema/reflect.go`
- Test: `internal/core/schema/reflect_test.go`
- Test: `internal/core/schema/fixture_test.go`

**Interfaces:**
- Consumes: `github.com/invopop/jsonschema` v0.14.0 (pinned in `go.mod`).
- Produces: Contracts › `internal/core/schema` › Task 1 (`Input`, `Output`, the `x-secret` keyword), and the test fixture types of `fixture_test.go` (`fixtureAddress`, `fixtureCredential`, `fixtureAuth`, `fixtureInput`, `fixtureOutput`) that Tasks 7, 8 and 9 reuse.

**Items:** 2

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Input schemas refuse unknown properties (`additionalProperties: false`) so a misspelt safety field is rejected rather than ignored (ADR 0002), and the `x-secret` marks drive redaction everywhere (shared contracts); keep both exactly as written.

- [ ] **Step 1: Write the failing test**

Create `internal/core/schema/fixture_test.go`:

```go
package schema

// The fixture types cover what reflection must handle: patterns, enums,
// ranges, optional fields, nested and repeated structs, an embedded struct
// and secret fields at several depths.

type fixtureAddress struct {
	Host string `json:"host" jsonschema:"pattern=^[a-z0-9.-]+$"`
	Port int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
}

type fixtureCredential struct {
	User string `json:"user"`
	Key  string `json:"key" secret:"true"`
}

type fixtureAuth struct {
	APIKey string `json:"apiKey,omitempty" secret:"true"`
}

type fixtureInput struct {
	fixtureAuth
	Name     string              `json:"name" jsonschema:"pattern=^[a-z][a-z0-9-]*$"`
	Mode     string              `json:"mode" jsonschema:"enum=fast,enum=safe"`
	Count    int                 `json:"count,omitempty" jsonschema:"minimum=1,maximum=10"`
	Password string              `json:"password" secret:"true"`
	Target   fixtureAddress      `json:"target"`
	Backups  []fixtureAddress    `json:"backups,omitempty"`
	Creds    []fixtureCredential `json:"creds,omitempty"`
	Tags     []string            `json:"tags,omitempty"`
}

type fixtureOutput struct {
	ID     string         `json:"id"`
	Token  string         `json:"token" secret:"true"`
	Target fixtureAddress `json:"target"`
}
```

Create `internal/core/schema/reflect_test.go`:

```go
package schema

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

// decodeSchema unmarshals a schema for inspection in tests.
func decodeSchema(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("schema is not JSON: %v\n%s", err, raw)
	}
	return m
}

// def returns the $defs entry name of a decoded schema.
func def(t *testing.T, s map[string]any, name string) map[string]any {
	t.Helper()
	defs, _ := s["$defs"].(map[string]any)
	d, ok := defs[name].(map[string]any)
	if !ok {
		t.Fatalf("$defs has no %s: %v", name, s["$defs"])
	}
	return d
}

func prop(t *testing.T, obj map[string]any, name string) map[string]any {
	t.Helper()
	props, _ := obj["properties"].(map[string]any)
	p, ok := props[name].(map[string]any)
	if !ok {
		t.Fatalf("no property %s in %v", name, obj["properties"])
	}
	return p
}

func required(obj map[string]any) []string {
	var out []string
	for _, r := range obj["required"].([]any) {
		out = append(out, r.(string))
	}
	slices.Sort(out)
	return out
}

func TestInputSchema(t *testing.T) {
	raw, err := Input(reflect.TypeFor[fixtureInput]())
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSchema(t, raw)
	if s["$schema"] != "https://json-schema.org/draft/2020-12/schema" {
		t.Errorf("$schema = %v", s["$schema"])
	}
	if _, ok := s["$id"]; ok {
		t.Errorf("schema has an $id: %v", s["$id"])
	}
	if s["$ref"] != "#/$defs/fixtureInput" {
		t.Errorf("$ref = %v, want #/$defs/fixtureInput", s["$ref"])
	}
	in := def(t, s, "fixtureInput")
	if in["additionalProperties"] != false {
		t.Errorf("input additionalProperties = %v, want false", in["additionalProperties"])
	}
	if got, want := required(in), []string{"mode", "name", "password", "target"}; !slices.Equal(got, want) {
		t.Errorf("required = %v, want %v", got, want)
	}
	if p := prop(t, in, "name"); p["pattern"] != "^[a-z][a-z0-9-]*$" {
		t.Errorf("name pattern = %v", p["pattern"])
	}
	if p := prop(t, in, "mode"); !reflect.DeepEqual(p["enum"], []any{"fast", "safe"}) {
		t.Errorf("mode enum = %v", p["enum"])
	}
	if p := prop(t, in, "count"); p["minimum"] != 1.0 || p["maximum"] != 10.0 {
		t.Errorf("count range = %v..%v", p["minimum"], p["maximum"])
	}
	for _, name := range []string{"password", "apiKey"} {
		if p := prop(t, in, name); p["x-secret"] != true || p["writeOnly"] != true {
			t.Errorf("%s = %v, want x-secret and writeOnly", name, p)
		}
	}
	if p := prop(t, in, "name"); p["x-secret"] != nil || p["writeOnly"] != nil {
		t.Errorf("name is marked secret: %v", p)
	}
	if p := prop(t, in, "target"); p["$ref"] != "#/$defs/fixtureAddress" {
		t.Errorf("target = %v, want a $ref to fixtureAddress", p)
	}
	addr := def(t, s, "fixtureAddress")
	if addr["additionalProperties"] != false {
		t.Errorf("nested additionalProperties = %v, want false", addr["additionalProperties"])
	}
	if p := prop(t, addr, "port"); p["minimum"] != 1.0 || p["maximum"] != 65535.0 {
		t.Errorf("port range = %v..%v", p["minimum"], p["maximum"])
	}
	if p := prop(t, def(t, s, "fixtureCredential"), "key"); p["x-secret"] != true || p["writeOnly"] != true {
		t.Errorf("creds[].key = %v, want x-secret and writeOnly", p)
	}
}

func TestOutputSchema(t *testing.T) {
	raw, err := Output(reflect.TypeFor[fixtureOutput]())
	if err != nil {
		t.Fatal(err)
	}
	s := decodeSchema(t, raw)
	out := def(t, s, "fixtureOutput")
	if _, ok := out["additionalProperties"]; ok {
		t.Errorf("output additionalProperties = %v, want absent", out["additionalProperties"])
	}
	if _, ok := def(t, s, "fixtureAddress")["additionalProperties"]; ok {
		t.Error("nested output object restricts additional properties")
	}
	p := prop(t, out, "token")
	if p["x-secret"] != true {
		t.Errorf("token x-secret = %v, want true", p["x-secret"])
	}
	if _, ok := p["writeOnly"]; ok {
		t.Error("output field marked writeOnly")
	}
}

func TestSchemaDeterministic(t *testing.T) {
	a, err := Input(reflect.TypeFor[fixtureInput]())
	if err != nil {
		t.Fatal(err)
	}
	for range 5 {
		b, err := Input(reflect.TypeFor[fixtureInput]())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(a, b) {
			t.Fatalf("schemas differ:\n%s\n%s", a, b)
		}
	}
}

func TestSchemaNilType(t *testing.T) {
	if _, err := Input(nil); err == nil {
		t.Error("Input(nil) succeeded")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/`
Expected: FAIL — the build reports `undefined: Input`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/reflect.go`:

```go
// Package schema turns action and resource types into JSON Schemas (draft
// 2020-12), validates inputs against them, writes the committed snapshots,
// checks released snapshots for compatibility and builds the OpenAPI
// document (spec section 7, ADR 0002).
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/invopop/jsonschema"
)

// secretKeyword marks a property whose value is a secret (shared contracts:
// the secret:"true" struct tag).
const secretKeyword = "x-secret"

// Input returns the schema of an action's input type: every field is required
// unless its JSON tag has omitempty, and every object refuses unknown
// properties, so a misspelt safety field is rejected rather than ignored
// (ADR 0002). Secret fields carry "x-secret" and "writeOnly".
func Input(t reflect.Type) (json.RawMessage, error) { return build(t, true) }

// Output returns the schema of an action's or resource's output type. Objects
// allow additional properties, so clients accept fields added later; secret
// fields carry "x-secret".
func Output(t reflect.Type) (json.RawMessage, error) { return build(t, false) }

func build(t reflect.Type, input bool) (json.RawMessage, error) {
	if t == nil {
		return nil, errors.New("schema: nil type")
	}
	r := &jsonschema.Reflector{Anonymous: true, AllowAdditionalProperties: !input}
	s := r.ReflectFromType(t)
	markSecrets(t, s, s.Definitions, input, map[*jsonschema.Schema]bool{})
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("schema: marshal %s: %w", t, err)
	}
	return b, nil
}

// resolve follows a "#/$defs/<name>" reference.
func resolve(s *jsonschema.Schema, defs jsonschema.Definitions) *jsonschema.Schema {
	if name, ok := strings.CutPrefix(s.Ref, "#/$defs/"); ok {
		return defs[name]
	}
	return s
}

// markSecrets walks t and its schema s together and marks every property
// whose struct field is tagged secret:"true".
func markSecrets(t reflect.Type, s *jsonschema.Schema, defs jsonschema.Definitions, input bool, seen map[*jsonschema.Schema]bool) {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	s = resolve(s, defs)
	if s == nil || seen[s] {
		return
	}
	seen[s] = true
	switch t.Kind() {
	case reflect.Slice, reflect.Array:
		if s.Items != nil {
			markSecrets(t.Elem(), s.Items, defs, input, seen)
		}
	case reflect.Map:
		if s.AdditionalProperties != nil {
			markSecrets(t.Elem(), s.AdditionalProperties, defs, input, seen)
		}
		for _, p := range s.PatternProperties {
			markSecrets(t.Elem(), p, defs, input, seen)
		}
	case reflect.Struct:
		markFields(t, s, defs, input, seen)
	}
}

func markFields(t reflect.Type, s *jsonschema.Schema, defs jsonschema.Definitions, input bool, seen map[*jsonschema.Schema]bool) {
	if s.Properties == nil {
		return // a type with its own representation, such as time.Time
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if name == "-" {
			continue
		}
		ft := f.Type
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		if f.Anonymous && name == "" && ft.Kind() == reflect.Struct {
			markFields(ft, s, defs, input, seen) // embedded fields are inlined
			continue
		}
		if !f.IsExported() {
			continue
		}
		if name == "" {
			name = f.Name
		}
		p, ok := s.Properties.Get(name)
		if !ok {
			continue
		}
		if f.Tag.Get("secret") == "true" {
			if p.Extras == nil {
				p.Extras = map[string]any{}
			}
			p.Extras[secretKeyword] = true
			p.WriteOnly = input
		}
		markSecrets(f.Type, p, defs, input, seen)
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`, `go vet` prints nothing, golangci-lint prints `0 issues.`, and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/fixture_test.go internal/core/schema/reflect_test.go internal/core/schema/reflect.go
git commit -m "feat(schema): reflect action types into schemas"
```

---

### Task 2: Marked blocks

**Files:**
- Create: `internal/platform/blocks.go`
- Test: `internal/platform/blocks_test.go`

**Interfaces:**
- Consumes: `errcode.FilesMarkersBad` and `errcode.New` (foundation).
- Produces: Contracts › `internal/platform` › Task 2 — package `platform` with its package doc comment, and the unexported `replaceBlock(content []byte, prefix, module, blockID string, body []byte) ([]byte, error)` and `removeBlock(content []byte, prefix, module, blockID string) ([]byte, error)` that Task 19 calls.

**Items:** 7

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

A file whose markers are duplicated, nested or unterminated is refused, never repaired (ADR 0009), and `replaceBlock` re-checks its own result so a body holding marker lines is refused too; keep both checks.

- [ ] **Step 1: Write the failing test**

Create `internal/platform/blocks_test.go`:

```go
package platform

import (
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestReplaceBlock(t *testing.T) {
	tests := []struct {
		name, prefix, in, body, want string
	}{
		{"empty file", "#", "", "allow 22\n", "# BEGIN DARKPANEL fw/rules\nallow 22\n# END DARKPANEL fw/rules\n"},
		{"append after content", "#", "a\n", "x\n", "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n"},
		{"append adds a missing newline", "#", "a", "x", "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n"},
		{
			"replace keeps surrounding lines", "#",
			"a\n# BEGIN DARKPANEL fw/rules\nold\nold2\n# END DARKPANEL fw/rules\nb\n", "new\n",
			"a\n# BEGIN DARKPANEL fw/rules\nnew\n# END DARKPANEL fw/rules\nb\n",
		},
		{
			"other blocks untouched", "#",
			"# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n", "x\n",
			"# BEGIN DARKPANEL fw/other\nkeep\n# END DARKPANEL fw/other\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\n",
		},
		{"empty body", "#", "", "", "# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n"},
		{"comment prefix", "//", "", "x\n", "// BEGIN DARKPANEL fw/rules\nx\n// END DARKPANEL fw/rules\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := replaceBlock([]byte(tt.in), tt.prefix, "fw", "rules", []byte(tt.body))
			if err != nil {
				t.Fatalf("replaceBlock: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("got\n%q\nwant\n%q", got, tt.want)
			}
		})
	}
}

func TestRemoveBlock(t *testing.T) {
	in := "a\n# BEGIN DARKPANEL fw/rules\nx\n# END DARKPANEL fw/rules\nb\n"
	got, err := removeBlock([]byte(in), "#", "fw", "rules")
	if err != nil {
		t.Fatalf("removeBlock: %v", err)
	}
	if want := "a\nb\n"; string(got) != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got, err = removeBlock([]byte("a\n"), "#", "fw", "rules")
	if err != nil || string(got) != "a\n" {
		t.Errorf("absent block: got %q, %v; want the content unchanged", got, err)
	}
}

func TestInvalidMarkers(t *testing.T) {
	tests := map[string]string{
		"duplicated":        "# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n# BEGIN DARKPANEL fw/rules\n# END DARKPANEL fw/rules\n",
		"nested":            "# BEGIN DARKPANEL fw/a\n# BEGIN DARKPANEL fw/b\n# END DARKPANEL fw/b\n# END DARKPANEL fw/a\n",
		"unterminated":      "a\n# BEGIN DARKPANEL fw/rules\nx\n",
		"end without begin": "# END DARKPANEL fw/rules\n",
		"mismatched end":    "# BEGIN DARKPANEL fw/a\n# END DARKPANEL fw/b\n",
	}
	for name, in := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := replaceBlock([]byte(in), "#", "fw", "rules", []byte("x\n"))
			assertMarkersInvalid(t, err)
			_, err = removeBlock([]byte(in), "#", "fw", "rules")
			assertMarkersInvalid(t, err)
		})
	}
}

func TestBodyWithMarkerRefused(t *testing.T) {
	_, err := replaceBlock(nil, "#", "fw", "rules", []byte("# END DARKPANEL fw/rules\n"))
	assertMarkersInvalid(t, err)
}

func assertMarkersInvalid(t *testing.T, err error) {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.FilesMarkersBad {
		t.Errorf("err = %v, want %s", err, errcode.FilesMarkersBad)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/platform/`
Expected: FAIL — the build reports `undefined: replaceBlock` and `undefined: removeBlock`.

- [ ] **Step 3: Write the implementation**

Create `internal/platform/blocks.go`:

```go
// Package platform is the only code that runs programs or writes host files
// (ADR 0009): an allowlisted exec runner, a file writer limited to the paths
// and marked blocks each module declares, and the ownership manifest that
// darkpanel uninstall reverses.
package platform

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// span is a block's BEGIN and END marker line indexes.
type span struct{ begin, end int }

func markersInvalid(format string, args ...any) error {
	return errcode.New(errcode.FilesMarkersBad, fmt.Sprintf(format, args...))
}

// splitLines splits content after each newline, so joining the lines gives
// content back; a last line without a newline is kept as it is.
func splitLines(content []byte) [][]byte {
	lines := bytes.SplitAfter(content, []byte("\n"))
	if len(lines[len(lines)-1]) == 0 {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// scanMarkers checks every DARKPANEL marker with prefix in lines and returns
// each block's span. A file with duplicated, nested or unterminated markers
// is refused, never repaired by guessing (ADR 0009).
func scanMarkers(lines [][]byte, prefix string) (map[string]span, error) {
	begin, end := prefix+" BEGIN DARKPANEL ", prefix+" END DARKPANEL "
	spans := map[string]span{}
	open, openAt := "", -1
	for i, raw := range lines {
		line := strings.TrimSuffix(string(raw), "\n")
		switch {
		case strings.HasPrefix(line, begin):
			id := strings.TrimPrefix(line, begin)
			if open != "" {
				return nil, markersInvalid("line %d: block %s begins inside block %s", i+1, id, open)
			}
			if _, dup := spans[id]; dup {
				return nil, markersInvalid("line %d: block %s appears twice", i+1, id)
			}
			open, openAt = id, i
		case strings.HasPrefix(line, end):
			id := strings.TrimPrefix(line, end)
			if id != open {
				return nil, markersInvalid("line %d: end of block %s without its beginning", i+1, id)
			}
			spans[id] = span{openAt, i}
			open = ""
		}
	}
	if open != "" {
		return nil, markersInvalid("block %s has no end marker", open)
	}
	return spans, nil
}

// replaceBlock returns content with the body of block <module>/<blockID>
// replaced, or with the block appended when it is absent.
func replaceBlock(content []byte, prefix, module, blockID string, body []byte) ([]byte, error) {
	lines := splitLines(content)
	spans, err := scanMarkers(lines, prefix)
	if err != nil {
		return nil, err
	}
	id := module + "/" + blockID
	var block bytes.Buffer
	fmt.Fprintf(&block, "%s BEGIN DARKPANEL %s\n", prefix, id)
	block.Write(body)
	if len(body) > 0 && !bytes.HasSuffix(body, []byte("\n")) {
		block.WriteByte('\n')
	}
	fmt.Fprintf(&block, "%s END DARKPANEL %s\n", prefix, id)

	var out bytes.Buffer
	if s, ok := spans[id]; ok {
		out.Write(bytes.Join(lines[:s.begin], nil))
		out.Write(block.Bytes())
		out.Write(bytes.Join(lines[s.end+1:], nil))
	} else {
		out.Write(content)
		if len(content) > 0 && !bytes.HasSuffix(content, []byte("\n")) {
			out.WriteByte('\n')
		}
		out.Write(block.Bytes())
	}
	// A body holding marker lines would leave a file this package refuses.
	if _, err := scanMarkers(splitLines(out.Bytes()), prefix); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// removeBlock returns content without block <module>/<blockID>; content
// without the block is returned unchanged.
func removeBlock(content []byte, prefix, module, blockID string) ([]byte, error) {
	lines := splitLines(content)
	spans, err := scanMarkers(lines, prefix)
	if err != nil {
		return nil, err
	}
	s, ok := spans[module+"/"+blockID]
	if !ok {
		return content, nil
	}
	return append(bytes.Join(lines[:s.begin], nil), bytes.Join(lines[s.end+1:], nil)...), nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/platform/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/blocks.go internal/platform/blocks_test.go
git commit -m "feat(platform): edit DARKPANEL marked blocks"
```

---

### Task 3: Host-information parsers

**Files:**
- Create: `internal/modules/hostinfo/parse.go`
- Create: `internal/modules/hostinfo/testdata/os-release/ubuntu-22.04`, `ubuntu-24.04`, `debian-12`, `debian-13`
- Create: `internal/modules/hostinfo/testdata/proc-amd64/cpuinfo`, `internal/modules/hostinfo/testdata/proc-amd64/meminfo`
- Create: `internal/modules/hostinfo/testdata/proc-arm64/cpuinfo`, `internal/modules/hostinfo/testdata/proc-arm64/meminfo`
- Test: `internal/modules/hostinfo/parse_test.go`

**Interfaces:**
- Consumes: nothing beyond the standard library.
- Produces: Contracts › `internal/modules/hostinfo` › Task 3 — the unexported parsers `parseOSRelease`, `parseCPUInfo`, `parseMemInfo`, `parseLoadAvg`, `parseUptime` and their result types `osRelease`, `cpuInfo`, `memInfo`, `loadAvg` (field names and types match Task 5's `OS`, `CPU` and `Load`, which convert from them), the fixtures, and the test helper `readFixture(t *testing.T, path ...string) []byte` that Task 5's test uses.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

Fixtures vary by operating system (`os-release`: the real files of Ubuntu 22.04.5, Ubuntu 24.04.3, Debian 12 and Debian 13) and by architecture (`cpuinfo` and `meminfo` of an amd64 and an arm64 cloud VM, trimmed to two CPUs); Task 5's test combines them into all eight operating system and architecture pairs. The arm64 `cpuinfo` has no `model name` line, which is why the model falls back to `CPU implementer`/`CPU part`.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/hostinfo/parse_test.go`:

```go
package hostinfo

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, path ...string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(append([]string{"testdata"}, path...)...))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestParseOSRelease(t *testing.T) {
	tests := []struct {
		file string
		want osRelease
	}{
		{"ubuntu-22.04", osRelease{ID: "ubuntu", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"}},
		{"ubuntu-24.04", osRelease{ID: "ubuntu", VersionID: "24.04", PrettyName: "Ubuntu 24.04.3 LTS"}},
		{"debian-12", osRelease{ID: "debian", VersionID: "12", PrettyName: "Debian GNU/Linux 12 (bookworm)"}},
		{"debian-13", osRelease{ID: "debian", VersionID: "13", PrettyName: "Debian GNU/Linux 13 (trixie)"}},
	}
	for _, tt := range tests {
		t.Run(tt.file, func(t *testing.T) {
			if got := parseOSRelease(readFixture(t, "os-release", tt.file)); got != tt.want {
				t.Errorf("parseOSRelease = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseOSReleaseSyntax(t *testing.T) {
	tests := []struct {
		name, in string
		want     osRelease
	}{
		{"empty file takes the os-release defaults", "", osRelease{ID: "linux", PrettyName: "Linux"}},
		{"single quotes, comments and blank lines", "# comment\n\nID='alpine'\nPRETTY_NAME='Alpine Linux'\n", osRelease{ID: "alpine", PrettyName: "Alpine Linux"}},
		{"escaped quote in a double-quoted value", `PRETTY_NAME="Say \"hi\""` + "\nID=x\n", osRelease{ID: "x", PrettyName: `Say "hi"`}},
		{"line without = is skipped", "garbage\nID=debian\n", osRelease{ID: "debian", PrettyName: "Linux"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseOSRelease([]byte(tt.in)); got != tt.want {
				t.Errorf("parseOSRelease = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseCPUInfo(t *testing.T) {
	tests := []struct {
		name string
		in   []byte
		want cpuInfo
	}{
		{"amd64", readFixture(t, "proc-amd64", "cpuinfo"), cpuInfo{Model: "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", LogicalCount: 2}},
		{"arm64 implementer and part", readFixture(t, "proc-arm64", "cpuinfo"), cpuInfo{Model: "0x41/0xd0c", LogicalCount: 2}},
		{"Hardware line", []byte("processor\t: 0\nHardware\t: BCM2835\nCPU implementer\t: 0x41\nCPU part\t: 0xd08\n"), cpuInfo{Model: "BCM2835", LogicalCount: 1}},
		{"model name wins over Hardware", []byte("processor\t: 0\nmodel name\t: ARMv7 Processor rev 4 (v7l)\nHardware\t: BCM2835\n"), cpuInfo{Model: "ARMv7 Processor rev 4 (v7l)", LogicalCount: 1}},
		{"nothing known", []byte("processor\t: 0\nprocessor\t: 1\nprocessor\t: 2\n"), cpuInfo{Model: "unknown", LogicalCount: 3}},
		{"empty", nil, cpuInfo{Model: "unknown", LogicalCount: 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseCPUInfo(tt.in); got != tt.want {
				t.Errorf("parseCPUInfo = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestParseMemInfo(t *testing.T) {
	tests := []struct {
		arch string
		want memInfo
	}{
		{"amd64", memInfo{TotalBytes: 8007412 * 1024, AvailableBytes: 7421576 * 1024, SwapTotalBytes: 2097148 * 1024, SwapFreeBytes: 2097148 * 1024}},
		{"arm64", memInfo{TotalBytes: 3924604 * 1024, AvailableBytes: 3512876 * 1024}},
	}
	for _, tt := range tests {
		t.Run(tt.arch, func(t *testing.T) {
			got, err := parseMemInfo(readFixture(t, "proc-"+tt.arch, "meminfo"))
			if err != nil || got != tt.want {
				t.Errorf("parseMemInfo = %+v, %v; want %+v", got, err, tt.want)
			}
		})
	}
	for _, in := range []string{"", "MemFree: 1 kB\n", "MemTotal: x kB\nMemAvailable: 1 kB\n", "MemTotal: 1 kB\n", "MemTotal:0 00\nMemAvailable:1 00000000000000", "MemTotal: 2 kB\nMemAvailable: 1 kB\nSwapTotal: 0 kB\nSwapFree: 1 kB\n"} {
		if _, err := parseMemInfo([]byte(in)); err == nil {
			t.Errorf("parseMemInfo(%q) succeeded, want an error", in)
		}
	}
}

func TestParseLoadAvg(t *testing.T) {
	got, err := parseLoadAvg([]byte("0.08 0.03 1.50 1/123 4567\n"))
	if want := (loadAvg{One: 0.08, Five: 0.03, Fifteen: 1.5}); err != nil || got != want {
		t.Errorf("parseLoadAvg = %+v, %v; want %+v", got, err, want)
	}
	for _, in := range []string{"", "0.1 0.2", "a b c 1/2 3", "NaN 0 0 1/1 1", "-1 0 0 1/1 1"} {
		if _, err := parseLoadAvg([]byte(in)); err == nil {
			t.Errorf("parseLoadAvg(%q) succeeded, want an error", in)
		}
	}
}

func TestParseUptime(t *testing.T) {
	got, err := parseUptime([]byte("122610.97 1469212.45\n"))
	if err != nil || got != 122610 {
		t.Errorf("parseUptime = %d, %v; want 122610", got, err)
	}
	for _, in := range []string{"", "x 1", "-5 1", "Inf 1", "1e300 1"} {
		if _, err := parseUptime([]byte(in)); err == nil {
			t.Errorf("parseUptime(%q) succeeded, want an error", in)
		}
	}
}

func FuzzParseOSRelease(f *testing.F) {
	f.Add([]byte("ID=debian\nPRETTY_NAME=\"Debian\"\n"))
	f.Add([]byte(`PRETTY_NAME="a\"b\\"` + "\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if r := parseOSRelease(in); r.ID == "" || r.PrettyName == "" {
			t.Errorf("parseOSRelease(%q) = %+v, want ID and PrettyName set", in, r)
		}
	})
}

func FuzzParseCPUInfo(f *testing.F) {
	f.Add([]byte("processor\t: 0\nmodel name\t: x\n"))
	f.Add([]byte("processor\t: 0\nCPU implementer\t: 0x41\nCPU part\t: 0xd0c\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if c := parseCPUInfo(in); c.Model == "" || c.LogicalCount < 0 {
			t.Errorf("parseCPUInfo(%q) = %+v", in, c)
		}
	})
}

func FuzzParseMemInfo(f *testing.F) {
	f.Add([]byte("MemTotal: 2 kB\nMemAvailable: 1 kB\nSwapTotal: 0 kB\nSwapFree: 0 kB\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if m, err := parseMemInfo(in); err == nil && (m.AvailableBytes > m.TotalBytes || m.SwapFreeBytes > m.SwapTotalBytes) {
			t.Errorf("parseMemInfo(%q) = %+v, more available than total", in, m)
		}
	})
}

func FuzzParseLoadAvg(f *testing.F) {
	f.Add([]byte("0.08 0.03 0.01 1/123 4567\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if l, err := parseLoadAvg(in); err == nil && (l.One < 0 || l.Five < 0 || l.Fifteen < 0) {
			t.Errorf("parseLoadAvg(%q) = %+v", in, l)
		}
	})
}

func FuzzParseUptime(f *testing.F) {
	f.Add([]byte("122610.97 1469212.45\n"))
	f.Fuzz(func(t *testing.T, in []byte) {
		if s, err := parseUptime(in); err == nil && s < 0 {
			t.Errorf("parseUptime(%q) = %d", in, s)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/modules/hostinfo/`
Expected: FAIL — the build reports `undefined: osRelease`.

- [ ] **Step 3: Write the fixtures and the implementation**

Create `internal/modules/hostinfo/testdata/os-release/ubuntu-22.04`:

```
PRETTY_NAME="Ubuntu 22.04.5 LTS"
NAME="Ubuntu"
VERSION_ID="22.04"
VERSION="22.04.5 LTS (Jammy Jellyfish)"
VERSION_CODENAME=jammy
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
SUPPORT_URL="https://help.ubuntu.com/"
BUG_REPORT_URL="https://bugs.launchpad.net/ubuntu/"
PRIVACY_POLICY_URL="https://www.ubuntu.com/legal/terms-and-policies/privacy-policy"
UBUNTU_CODENAME=jammy
```

Create `internal/modules/hostinfo/testdata/os-release/ubuntu-24.04`:

```
PRETTY_NAME="Ubuntu 24.04.3 LTS"
NAME="Ubuntu"
VERSION_ID="24.04"
VERSION="24.04.3 LTS (Noble Numbat)"
VERSION_CODENAME=noble
ID=ubuntu
ID_LIKE=debian
HOME_URL="https://www.ubuntu.com/"
SUPPORT_URL="https://help.ubuntu.com/"
BUG_REPORT_URL="https://bugs.launchpad.net/ubuntu/"
PRIVACY_POLICY_URL="https://www.ubuntu.com/legal/terms-and-policies/privacy-policy"
UBUNTU_CODENAME=noble
LOGO=ubuntu-logo
```

Create `internal/modules/hostinfo/testdata/os-release/debian-12`:

```
PRETTY_NAME="Debian GNU/Linux 12 (bookworm)"
NAME="Debian GNU/Linux"
VERSION_ID="12"
VERSION="12 (bookworm)"
VERSION_CODENAME=bookworm
ID=debian
HOME_URL="https://www.debian.org/"
SUPPORT_URL="https://www.debian.org/support"
BUG_REPORT_URL="https://bugs.debian.org/"
```

Create `internal/modules/hostinfo/testdata/os-release/debian-13`:

```
PRETTY_NAME="Debian GNU/Linux 13 (trixie)"
NAME="Debian GNU/Linux"
VERSION_ID="13"
VERSION="13 (trixie)"
VERSION_CODENAME=trixie
DEBIAN_VERSION_FULL=13.5
ID=debian
HOME_URL="https://www.debian.org/"
SUPPORT_URL="https://www.debian.org/support"
BUG_REPORT_URL="https://bugs.debian.org/"
```

Create `internal/modules/hostinfo/testdata/proc-amd64/meminfo`:

```
MemTotal:        8007412 kB
MemFree:         6512340 kB
MemAvailable:    7421576 kB
Buffers:           39812 kB
Cached:           987456 kB
SwapCached:            0 kB
Active:           512344 kB
Inactive:         764128 kB
SwapTotal:       2097148 kB
SwapFree:        2097148 kB
Dirty:                84 kB
Writeback:             0 kB
AnonPages:        249312 kB
Mapped:           187240 kB
Shmem:              1012 kB
HugePages_Total:       0
HugePages_Free:        0
Hugepagesize:       2048 kB
```

Create `internal/modules/hostinfo/testdata/proc-arm64/meminfo`:

```
MemTotal:        3924604 kB
MemFree:         2983128 kB
MemAvailable:    3512876 kB
Buffers:           27408 kB
Cached:           573252 kB
SwapCached:            0 kB
Active:           301456 kB
Inactive:         458932 kB
SwapTotal:             0 kB
SwapFree:              0 kB
Dirty:                40 kB
Writeback:             0 kB
AnonPages:        159788 kB
Mapped:           141964 kB
Shmem:               876 kB
HugePages_Total:       0
HugePages_Free:        0
Hugepagesize:       2048 kB
```

The two `cpuinfo` files separate keys from values with tab characters, as the kernel does, so create them with these commands rather than by hand (run from the repository root):

```bash
for i in 0 1; do printf 'processor\t: %s\nvendor_id\t: GenuineIntel\ncpu family\t: 6\nmodel\t\t: 85\nmodel name\t: Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz\nstepping\t: 7\nmicrocode\t: 0x5003901\ncpu MHz\t\t: 2499.998\ncache size\t: 36608 KB\nphysical id\t: 0\nsiblings\t: 2\ncore id\t\t: 0\ncpu cores\t: 1\napicid\t\t: %s\ninitial apicid\t: %s\nfpu\t\t: yes\nfpu_exception\t: yes\ncpuid level\t: 13\nwp\t\t: yes\nflags\t\t: fpu vme de pse tsc msr pae mce cx8 apic sep mtrr pge mca cmov pat pse36 clflush mmx fxsr sse sse2 ss ht syscall nx pdpe1gb rdtscp lm constant_tsc rep_good nopl xtopology nonstop_tsc cpuid tsc_known_freq pni pclmulqdq ssse3 fma cx16 pcid sse4_1 sse4_2 x2apic movbe popcnt aes xsave avx f16c rdrand hypervisor lahf_lm abm 3dnowprefetch avx512f avx512dq rdseed adx smap clflushopt clwb avx512cd avx512bw avx512vl xsaveopt xsavec xgetbv1 xsaves ida arat pku ospke\nbugs\t\t: spectre_v1 spectre_v2 spec_store_bypass swapgs mmio_stale_data retbleed gds\nbogomips\t: 4999.99\nclflush size\t: 64\ncache_alignment\t: 64\naddress sizes\t: 46 bits physical, 48 bits virtual\npower management:\n\n' $i $i $i; done > internal/modules/hostinfo/testdata/proc-amd64/cpuinfo
for i in 0 1; do printf 'processor\t: %s\nBogoMIPS\t: 243.75\nFeatures\t: fp asimd evtstrm aes pmull sha1 sha2 crc32 atomics fphp asimdhp cpuid asimdrdm lrcpc dcpop asimddp ssbs\nCPU implementer\t: 0x41\nCPU architecture: 8\nCPU variant\t: 0x3\nCPU part\t: 0xd0c\nCPU revision\t: 1\n\n' $i; done > internal/modules/hostinfo/testdata/proc-arm64/cpuinfo
```

Then check every fixture byte for byte; the command prints nothing and exits 0:

```bash
(cd internal/modules/hostinfo && sha256sum -c --quiet <<'EOF'
59a77b5f2666d9c85c489bd1911a6eebbd91ef22fe48b90a3b75f1b21f3844d4  testdata/os-release/debian-12
a757e4cae6c9e1aa1ddbbb7c8ad281bbda286b4c3a6a2762df78c05db9f90d82  testdata/os-release/debian-13
594d5ddd35aedb47f00d9c34d140017907a5b9f93c975aba125fc924daac5c07  testdata/os-release/ubuntu-22.04
3e5851448bae5b36f351becde037a8b13b77307279f484eda808f8177d9a4293  testdata/os-release/ubuntu-24.04
cd2403abc3871a809da6e6029347cc813d5b5f225b215ad4d6c28ed06773f545  testdata/proc-amd64/cpuinfo
a559f1a5e6d237c4a9cbd080d7de8cc98ed4edd850c1513679e2781ecb4189f7  testdata/proc-amd64/meminfo
532efd05528a51c82f573d4cf7b41ea51cb8212114874ca5ebeb4438344d0133  testdata/proc-arm64/cpuinfo
bac98fab5b0c2451fad7d875e9e69e24cf7faf62bf2b95ab7222641f9ab4311a  testdata/proc-arm64/meminfo
EOF
)
```

Create `internal/modules/hostinfo/parse.go`:

```go
// Package hostinfo is the host-information module (spec section 16): one
// resource, hostinfo.summary, read from /etc/os-release, /proc and statfs.
package hostinfo

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

type osRelease struct {
	ID, VersionID, PrettyName string
}

// parseOSRelease reads os-release(5). Missing ID and PRETTY_NAME take the
// defaults that os-release(5) specifies, "linux" and "Linux".
func parseOSRelease(data []byte) osRelease {
	var r osRelease
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.HasPrefix(line, "#") {
			continue
		}
		switch key {
		case "ID":
			r.ID = unquote(value)
		case "VERSION_ID":
			r.VersionID = unquote(value)
		case "PRETTY_NAME":
			r.PrettyName = unquote(value)
		}
	}
	if r.ID == "" {
		r.ID = "linux"
	}
	if r.PrettyName == "" {
		r.PrettyName = "Linux"
	}
	return r
}

// unquote strips os-release quoting: single quotes literally, double quotes
// with backslash escapes.
func unquote(v string) string {
	if len(v) >= 2 && v[0] == '\'' && v[len(v)-1] == '\'' {
		return v[1 : len(v)-1]
	}
	if len(v) < 2 || v[0] != '"' || v[len(v)-1] != '"' {
		return v
	}
	var b strings.Builder
	inner := v[1 : len(v)-1]
	for i := 0; i < len(inner); i++ {
		if inner[i] == '\\' && i+1 < len(inner) {
			i++
		}
		b.WriteByte(inner[i])
	}
	return b.String()
}

type cpuInfo struct {
	Model        string
	LogicalCount int
}

// parseCPUInfo counts processor entries and names the CPU from the first
// "model name" line, else the first "Hardware" line, else the first
// "CPU implementer" and "CPU part" lines (arm64 cloud images have no model
// name), else "unknown".
func parseCPUInfo(data []byte) cpuInfo {
	var c cpuInfo
	first := map[string]string{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		if !ok {
			continue
		}
		key, value = strings.TrimSpace(key), strings.TrimSpace(value)
		if key == "processor" {
			c.LogicalCount++
		}
		if _, seen := first[key]; !seen && value != "" {
			first[key] = value
		}
	}
	switch {
	case first["model name"] != "":
		c.Model = first["model name"]
	case first["Hardware"] != "":
		c.Model = first["Hardware"]
	case first["CPU implementer"] != "" && first["CPU part"] != "":
		c.Model = first["CPU implementer"] + "/" + first["CPU part"]
	default:
		c.Model = "unknown"
	}
	return c
}

type memInfo struct {
	TotalBytes, AvailableBytes, SwapTotalBytes, SwapFreeBytes uint64
}

// parseMemInfo reads the four fields the summary needs, in bytes. MemTotal
// and MemAvailable are required, as every supported kernel reports both, and
// neither available memory nor free swap may exceed its total.
func parseMemInfo(data []byte) (memInfo, error) {
	var m memInfo
	fields := map[string]*uint64{
		"MemTotal": &m.TotalBytes, "MemAvailable": &m.AvailableBytes,
		"SwapTotal": &m.SwapTotalBytes, "SwapFree": &m.SwapFreeBytes,
	}
	found := map[string]bool{}
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		key, value, ok := strings.Cut(sc.Text(), ":")
		dst := fields[key]
		if !ok || dst == nil {
			continue
		}
		num, unit, _ := strings.Cut(strings.TrimSpace(value), " ")
		n, err := strconv.ParseUint(num, 10, 64)
		if err != nil {
			return memInfo{}, fmt.Errorf("meminfo %s: %w", key, err)
		}
		if unit == "kB" {
			if n > math.MaxUint64/1024 {
				return memInfo{}, fmt.Errorf("meminfo %s: value out of range", key)
			}
			n *= 1024
		}
		*dst = n
		found[key] = true
	}
	if !found["MemTotal"] || !found["MemAvailable"] {
		return memInfo{}, errors.New("meminfo: MemTotal or MemAvailable missing")
	}
	if m.AvailableBytes > m.TotalBytes || m.SwapFreeBytes > m.SwapTotalBytes {
		return memInfo{}, errors.New("meminfo: available or free exceeds the total")
	}
	return m, nil
}

type loadAvg struct {
	One, Five, Fifteen float64
}

// parseLoadAvg reads the three load averages of /proc/loadavg.
func parseLoadAvg(data []byte) (loadAvg, error) {
	f := strings.Fields(string(data))
	if len(f) < 3 {
		return loadAvg{}, errors.New("loadavg: want at least three fields")
	}
	var v [3]float64
	for i := range v {
		n, err := strconv.ParseFloat(f[i], 64)
		if err != nil || math.IsNaN(n) || math.IsInf(n, 0) || n < 0 {
			return loadAvg{}, fmt.Errorf("loadavg: field %d is not a non-negative number", i+1)
		}
		v[i] = n
	}
	return loadAvg{One: v[0], Five: v[1], Fifteen: v[2]}, nil
}

// parseUptime reads the whole seconds since boot from /proc/uptime.
func parseUptime(data []byte) (int64, error) {
	f := strings.Fields(string(data))
	if len(f) == 0 {
		return 0, errors.New("uptime: empty")
	}
	s, err := strconv.ParseFloat(f[0], 64)
	// 2^53 seconds is far beyond any real uptime and keeps the conversion exact.
	if err != nil || math.IsNaN(s) || s < 0 || s >= 1<<53 {
		return 0, errors.New("uptime: first field is not a non-negative number of seconds")
	}
	return int64(s), nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/modules/hostinfo/
for f in FuzzParseOSRelease FuzzParseCPUInfo FuzzParseMemInfo FuzzParseLoadAvg FuzzParseUptime; do GOTOOLCHAIN=local timeout 120 go test -run '^$' -fuzz "^$f\$" -fuzztime 10s ./internal/modules/hostinfo/ || break; done
test ! -e internal/modules/hostinfo/testdata/fuzz
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `ok` for the test run and for each of the five fuzz runs (about 11 s each); the fuzzer creates no `testdata/fuzz` directory (it writes one only when it finds a failing input — report that input instead of committing it); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/hostinfo/parse.go internal/modules/hostinfo/parse_test.go internal/modules/hostinfo/testdata
git commit -m "feat(hostinfo): parse os-release and /proc files"
```

---

### Task 4: Schema compatibility

**Files:**
- Create: `internal/core/schema/compare.go`
- Test: `internal/core/schema/compare_test.go`
- Test: `internal/core/schema/testdata/compat/added-input-enum-value.json`
- Test: `internal/core/schema/testdata/compat/base.json`
- Test: `internal/core/schema/testdata/compat/changed-class.json`
- Test: `internal/core/schema/testdata/compat/changed-break-glass.json`
- Test: `internal/core/schema/testdata/compat/changed-secret.json`
- Test: `internal/core/schema/testdata/compat/changed-side.json`
- Test: `internal/core/schema/testdata/compat/changed-write-only.json`
- Test: `internal/core/schema/testdata/compat/optional-became-required.json`
- Test: `internal/core/schema/testdata/compat/changed-constraint.json`
- Test: `internal/core/schema/testdata/compat/changed-default.json`
- Test: `internal/core/schema/testdata/compat/changed-type.json`
- Test: `internal/core/schema/testdata/compat/new-optional-input-field.json`
- Test: `internal/core/schema/testdata/compat/new-output-enum-value.json`
- Test: `internal/core/schema/testdata/compat/new-output-field.json`
- Test: `internal/core/schema/testdata/compat/new-required-input-field.json`
- Test: `internal/core/schema/testdata/compat/removed-enum-value.json`
- Test: `internal/core/schema/testdata/compat/removed-field.json`
- Test: `internal/core/schema/testdata/compat/renamed-field.json`

**Interfaces:**
- Consumes: Contracts › `internal/core/schema` › Task 1 (the unexported `secretKeyword` constant); the snapshot file format of Contracts › Task 8 (read as JSON, no code dependency).
- Produces: Contracts › `internal/core/schema` › Task 4 (`Compare`, `CheckReleased`). Plan 4's registry tests build `files, err := schema.Snapshots(reg)` and run `schema.CheckReleased("schemas/released", files)`.

**Items:** 2

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Each fixture under `testdata/compat/` is `base.json` with exactly one change; the test table names the one violation each must produce, or none for the three allowed changes.

- [ ] **Step 1: Write the failing test**

Create the fixtures. `base.json` is the published snapshot; every other file is `base.json` with one change.

Create `internal/core/schema/testdata/compat/added-input-enum-value.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe", "slow"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/base.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-class.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "irreversible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-break-glass.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": true,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-secret.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$", "x-secret": true},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-side.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "controller",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-write-only.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$", "writeOnly": true},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/optional-became-required.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["mode", "name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-constraint.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z0-9]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-default.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "fast"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/changed-type.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "string", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/new-optional-input-field.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          },
          "comment": {"type": "string"}
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/new-output-enum-value.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle", "draining"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/new-output-field.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]},
          "createdAt": {"type": "string", "format": "date-time"}
        },
        "required": ["id", "state", "createdAt"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/new-required-input-field.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          },
          "zone": {"type": "string"}
        },
        "additionalProperties": false,
        "required": ["name", "port", "zone"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/removed-enum-value.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/removed-field.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "id": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["id", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/testdata/compat/renamed-field.json`:

```json
{
  "name": "demo.item.add",
  "version": 1,
  "class": "revertible",
  "side": "agent",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addInput",
    "$defs": {
      "addInput": {
        "type": "object",
        "properties": {
          "name": {"type": "string", "pattern": "^[a-z]+$"},
          "port": {"type": "integer", "minimum": 1, "maximum": 65535},
          "mode": {
            "type": "string",
            "enum": ["fast", "safe"],
            "default": "safe"
          }
        },
        "additionalProperties": false,
        "required": ["name", "port"]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/addOutput",
    "$defs": {
      "addOutput": {
        "type": "object",
        "properties": {
          "identifier": {"type": "string"},
          "state": {"type": "string", "enum": ["active", "idle"]}
        },
        "required": ["identifier", "state"]
      }
    }
  }
}
```

Create `internal/core/schema/compare_test.go`:

```go
package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "compat", name+".json")) //nolint:gosec // G304: a fixture name from the test table
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestCompare(t *testing.T) {
	tests := []struct {
		fixture string
		want    string // "" when the change is allowed
	}{
		{"base", ""},
		{"new-optional-input-field", ""},
		{"new-output-field", ""},
		{"new-output-enum-value", ""},
		{"removed-field", "input/port: field removed"},
		{"renamed-field", "output/id: field removed"},
		{"changed-type", `input/port: changed type from "integer" to "string"`},
		{"changed-default", `input/mode: changed default from "safe" to "fast"`},
		{"changed-constraint", `input/name: changed pattern from "^[a-z]+$" to "^[a-z0-9]+$"`},
		{"new-required-input-field", "input/zone: new required input field"},
		{"removed-enum-value", `output/state: removed enum value "idle"`},
		{"added-input-enum-value", `input/mode: added enum value "slow" to an input`},
		{"changed-class", `class changed from "revertible" to "irreversible"`},
		{"changed-side", `side changed from "agent" to "controller"`},
		{"changed-break-glass", "breakGlass changed from false to true"},
		{"changed-secret", "input/name: changed x-secret from none to true"},
		{"changed-write-only", "input/name: changed writeOnly from none to true"},
		{"optional-became-required", "input/mode: required changed from false to true"},
	}
	base := readFixture(t, "base")
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			got, err := Compare(base, readFixture(t, tt.fixture))
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == "" {
				if len(got) != 0 {
					t.Errorf("violations = %q, want none", got)
				}
				return
			}
			if len(got) != 1 || got[0] != tt.want {
				t.Errorf("violations = %q, want [%q]", got, tt.want)
			}
		})
	}
}

func TestCompareRefusesDifferentActions(t *testing.T) {
	other := strings.Replace(string(readFixture(t, "base")), `"demo.item.add"`, `"demo.item.remove"`, 1)
	if _, err := Compare(readFixture(t, "base"), []byte(other)); err == nil {
		t.Error("Compare accepted snapshots of two different actions")
	}
	if _, err := Compare([]byte("{"), readFixture(t, "base")); err == nil {
		t.Error("Compare accepted invalid JSON")
	}
}

func TestCheckReleased(t *testing.T) {
	dir := t.TempDir()
	released := filepath.Join(dir, "released")
	path := "demo.item.add/v1.json"
	if err := os.MkdirAll(filepath.Join(released, "v1.0.0", "demo.item.add"), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(released, "v1.0.0", path), readFixture(t, "base"), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := CheckReleased(filepath.Join(dir, "absent"), nil); err != nil {
		t.Errorf("missing released directory: %v, want nil", err)
	}
	current := map[string][]byte{path: readFixture(t, "new-output-field")}
	if err := CheckReleased(released, current); err != nil {
		t.Errorf("allowed change: %v, want nil", err)
	}
	current[path] = readFixture(t, "removed-field")
	err := CheckReleased(released, current)
	if err == nil || !strings.Contains(err.Error(), "v1.0.0/demo.item.add/v1.json: input/port: field removed") {
		t.Errorf("breaking change: err = %v", err)
	}
	err = CheckReleased(released, map[string][]byte{})
	if err == nil || !strings.Contains(err.Error(), "v1.0.0/demo.item.add/v1.json: released version removed") {
		t.Errorf("removed version: err = %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/`
Expected: FAIL — the build reports `undefined: Compare`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/compare.go`:

```go
package schema

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
)

// snapshotFile is the committed form of an action or resource (written by
// ActionSnapshot and ResourceSnapshot); a resource has only name and output.
type snapshotFile struct {
	Name       string         `json:"name"`
	Version    int            `json:"version"`
	Class      string         `json:"class"`
	Side       string         `json:"side"`
	BreakGlass bool           `json:"breakGlass"`
	Input      map[string]any `json:"input"`
	Output     map[string]any `json:"output"`
}

// constraintKeywords are compared for equality: any change to one of them in
// a published version is a breaking change (ADR 0002).
var constraintKeywords = []string{
	"type", "format", "pattern", "default", "const", "minimum", "maximum",
	"exclusiveMinimum", "exclusiveMaximum", "minLength", "maxLength", "minItems",
	"maxItems", "uniqueItems", "minProperties", "maxProperties", "contentEncoding",
	secretKeyword, "writeOnly",
}

// Compare reports every change from the snapshot prev to the snapshot next of
// the same action version that ADR 0002 forbids: a removed or renamed field, a
// changed type, default or constraint, a new required input field, a removed
// enum value (or any enum change in an input), or a changed class, side or
// break-glass flag. New optional input fields, new output fields and new
// output enum values are allowed. It fails only when the files cannot be
// compared.
func Compare(prev, next []byte) ([]string, error) {
	var p, n snapshotFile
	if err := json.Unmarshal(prev, &p); err != nil {
		return nil, fmt.Errorf("schema: old snapshot: %w", err)
	}
	if err := json.Unmarshal(next, &n); err != nil {
		return nil, fmt.Errorf("schema: new snapshot: %w", err)
	}
	if p.Name != n.Name || p.Version != n.Version {
		return nil, fmt.Errorf("schema: comparing %s/v%d with %s/v%d", p.Name, p.Version, n.Name, n.Version)
	}
	var out []string
	for _, f := range []struct {
		name       string
		prev, next any
	}{{"class", p.Class, n.Class}, {"side", p.Side, n.Side}, {"breakGlass", p.BreakGlass, n.BreakGlass}} {
		if f.prev != f.next {
			out = append(out, fmt.Sprintf("%s changed from %s to %s", f.name, render(f.prev), render(f.next)))
		}
	}
	for _, dir := range []struct {
		name       string
		prev, next map[string]any
		input      bool
	}{{"input", p.Input, n.Input, true}, {"output", p.Output, n.Output, false}} {
		c := comparer{prevDefs: defsOf(dir.prev), nextDefs: defsOf(dir.next), input: dir.input, active: map[[2]string]bool{}}
		c.compare(dir.name, dir.prev, dir.next)
		out = append(out, c.out...)
	}
	return out, nil
}

// CheckReleased compares every released snapshot, releasedDir/<tag>/<path>,
// with current[<path>] and returns every violation joined. A released file
// with no current counterpart is a removed version. A missing releasedDir has
// nothing to check.
func CheckReleased(releasedDir string, current map[string][]byte) error {
	if _, err := os.Stat(releasedDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	var errs []error
	err := filepath.WalkDir(releasedDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".json" {
			return err
		}
		rel, err := filepath.Rel(releasedDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		_, key, ok := strings.Cut(rel, "/") // drop the tag
		if !ok {
			return nil
		}
		now, ok := current[key]
		if !ok {
			errs = append(errs, fmt.Errorf("%s: released version removed", rel))
			return nil
		}
		old, err := os.ReadFile(path) //nolint:gosec // G304: a path found under the released snapshots directory
		if err != nil {
			return err
		}
		violations, err := Compare(old, now)
		if err != nil {
			return fmt.Errorf("%s: %w", rel, err)
		}
		for _, v := range violations {
			errs = append(errs, fmt.Errorf("%s: %s", rel, v))
		}
		return nil
	})
	if err != nil {
		errs = append(errs, fmt.Errorf("schema: released snapshots: %w", err))
	}
	return errors.Join(errs...)
}

func defsOf(s map[string]any) map[string]any {
	d, _ := s["$defs"].(map[string]any)
	return d
}

func render(v any) string {
	if v == nil {
		return "none"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

type comparer struct {
	prevDefs, nextDefs map[string]any
	input              bool
	active             map[[2]string]bool // definition pairs being compared, against recursion
	out                []string
}

func (c *comparer) fail(path, format string, args ...any) {
	c.out = append(c.out, path+": "+fmt.Sprintf(format, args...))
}

// resolveJSON replaces a "#/$defs/<name>" reference with the definition, keeping
// keywords written beside the reference (such as x-secret on a field).
func resolveJSON(s, defs map[string]any) (map[string]any, string) {
	ref, _ := s["$ref"].(string)
	name, ok := strings.CutPrefix(ref, "#/$defs/")
	if !ok {
		return s, ""
	}
	d, _ := defs[name].(map[string]any)
	if d == nil {
		return nil, name
	}
	merged := maps.Clone(d)
	for k, v := range s {
		if k != "$ref" {
			merged[k] = v
		}
	}
	return merged, name
}

func (c *comparer) compare(path string, prev, next map[string]any) {
	prev, pname := resolveJSON(prev, c.prevDefs)
	next, nname := resolveJSON(next, c.nextDefs)
	if prev == nil || next == nil {
		if prev != nil || next != nil {
			c.fail(path, "reference %q cannot be resolved", pname+nname)
		}
		return
	}
	for _, kw := range constraintKeywords {
		if !reflect.DeepEqual(prev[kw], next[kw]) {
			c.fail(path, "changed %s from %s to %s", kw, render(prev[kw]), render(next[kw]))
		}
	}
	c.compareEnum(path, prev["enum"], next["enum"])
	key := [2]string{pname, nname}
	if pname != "" || nname != "" {
		if c.active[key] {
			return
		}
		c.active[key] = true
		defer delete(c.active, key)
	}
	c.compareProperties(path, prev, next)
	for _, kw := range []string{"items", "additionalProperties"} {
		ps, pok := prev[kw].(map[string]any)
		ns, nok := next[kw].(map[string]any)
		switch {
		case pok && nok:
			c.compare(path+"/"+kw, ps, ns)
		case !reflect.DeepEqual(prev[kw], next[kw]):
			c.fail(path, "changed %s from %s to %s", kw, render(prev[kw]), render(next[kw]))
		}
	}
	for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
		ps, _ := prev[kw].([]any)
		ns, _ := next[kw].([]any)
		if len(ps) != len(ns) {
			c.fail(path, "changed %s from %d to %d branches", kw, len(ps), len(ns))
			continue
		}
		for i := range ps {
			pm, _ := ps[i].(map[string]any)
			nm, _ := ns[i].(map[string]any)
			c.compare(fmt.Sprintf("%s/%s/%d", path, kw, i), pm, nm)
		}
	}
}

func (c *comparer) compareEnum(path string, prev, next any) {
	ps, _ := prev.([]any)
	ns, _ := next.([]any)
	switch {
	case prev == nil && next == nil:
		return
	case prev == nil || next == nil:
		c.fail(path, "changed enum from %s to %s", render(prev), render(next))
		return
	}
	for _, v := range ps {
		if !slices.ContainsFunc(ns, func(w any) bool { return reflect.DeepEqual(v, w) }) {
			c.fail(path, "removed enum value %s", render(v))
		}
	}
	if c.input {
		for _, v := range ns {
			if !slices.ContainsFunc(ps, func(w any) bool { return reflect.DeepEqual(v, w) }) {
				c.fail(path, "added enum value %s to an input", render(v))
			}
		}
	}
}

func (c *comparer) compareProperties(path string, prev, next map[string]any) {
	pp, _ := prev["properties"].(map[string]any)
	np, _ := next["properties"].(map[string]any)
	preq, nreq := stringSet(prev["required"]), stringSet(next["required"])
	for _, name := range slices.Sorted(maps.Keys(pp)) {
		at := path + "/" + name
		ns, ok := np[name].(map[string]any)
		if !ok {
			c.fail(at, "field removed")
			continue
		}
		if preq[name] != nreq[name] {
			c.fail(at, "required changed from %t to %t", preq[name], nreq[name])
		}
		ps, _ := pp[name].(map[string]any)
		c.compare(at, ps, ns)
	}
	for _, name := range slices.Sorted(maps.Keys(np)) {
		if _, ok := pp[name]; !ok && c.input && nreq[name] {
			c.fail(path+"/"+name, "new required input field")
		}
	}
}

func stringSet(v any) map[string]bool {
	out := map[string]bool{}
	list, _ := v.([]any)
	for _, s := range list {
		if name, ok := s.(string); ok {
			out[name] = true
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`, `go vet` prints nothing, golangci-lint prints `0 issues.`, and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/compare.go internal/core/schema/compare_test.go internal/core/schema/testdata/compat
git commit -m "feat(schema): check snapshot compatibility"
```

---

### Task 5: Host-information module

**Files:**
- Create: `internal/modules/hostinfo/hostinfo.go`
- Test: `internal/modules/hostinfo/hostinfo_test.go`

**Interfaces:**
- Consumes: Task 3's parsers, result types, fixtures and `readFixture`; foundation `core.Module`, `core.Manifest`, `core.DefineResource`, `core.PreflightCheck`, `core.Env`, `core.SystemClock`, `core.NewRegistry`, `coretest.NewFakeClock`, `buildinfo.Version`.
- Produces: Contracts › `internal/modules/hostinfo` › Task 5 (`ID`, `Summary` with `OS`, `CPU`, `Memory`, `Swap`, `Load`, `RootFS`, `Source`, `New`, `Module`).

**Depends on:** Task 3

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

The module is not registered in `internal/modules/modules.go` here; plan 4 adds that one line. `arch` is `runtime.GOARCH` (`amd64` or `arm64`, the names of the release binaries), and `bootTime` is the handler clock's time minus the whole seconds of uptime, in UTC, truncated to the second. `TestProductionModuleReadsThisHost` reads the real `/etc/os-release`, `/proc` and `statfs("/")` of the machine running the test, which every Linux development host and CI runner has.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/hostinfo/hostinfo_test.go`:

```go
package hostinfo

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"runtime"
	"testing"
	"testing/fstest"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
)

var testNow = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

func fixtureFS(t *testing.T, osName, arch string) fstest.MapFS {
	t.Helper()
	return fstest.MapFS{
		"etc/os-release":            {Data: readFixture(t, "os-release", osName)},
		"proc/cpuinfo":              {Data: readFixture(t, "proc-"+arch, "cpuinfo")},
		"proc/meminfo":              {Data: readFixture(t, "proc-"+arch, "meminfo")},
		"proc/loadavg":              {Data: []byte("0.08 0.03 0.01 1/123 4567\n")},
		"proc/uptime":               {Data: []byte("3600.50 7000.00\n")},
		"proc/sys/kernel/osrelease": {Data: []byte("6.8.0-45-generic\n")},
	}
}

func fixtureSource(fsys fstest.MapFS) Source {
	return Source{
		FS:       fsys,
		Hostname: func() (string, error) { return "vps-1", nil },
		Statfs: func(path string) (uint64, uint64, error) {
			if path != "/" {
				return 0, 0, errors.New("statfs: unexpected path " + path)
			}
			return 50 << 30, 20 << 30, nil
		},
		Version: func() string { return "v0.1.0" },
	}
}

func readSummary(t *testing.T, m core.Module) (json.RawMessage, error) {
	t.Helper()
	res := m.Manifest().Resources
	if len(res) != 1 {
		t.Fatalf("resources = %d, want 1", len(res))
	}
	return res[0].Read(context.Background(), core.Env{Clock: coretest.NewFakeClock(testNow)})
}

func TestSummaryPerOSAndArch(t *testing.T) {
	oses := map[string]OS{
		"ubuntu-22.04": {ID: "ubuntu", VersionID: "22.04", PrettyName: "Ubuntu 22.04.5 LTS"},
		"ubuntu-24.04": {ID: "ubuntu", VersionID: "24.04", PrettyName: "Ubuntu 24.04.3 LTS"},
		"debian-12":    {ID: "debian", VersionID: "12", PrettyName: "Debian GNU/Linux 12 (bookworm)"},
		"debian-13":    {ID: "debian", VersionID: "13", PrettyName: "Debian GNU/Linux 13 (trixie)"},
	}
	type archWant struct {
		cpu  CPU
		mem  Memory
		swap Swap
	}
	arches := map[string]archWant{
		"amd64": {
			CPU{Model: "Intel(R) Xeon(R) Platinum 8259CL CPU @ 2.50GHz", LogicalCount: 2},
			Memory{TotalBytes: 8007412 << 10, AvailableBytes: 7421576 << 10},
			Swap{TotalBytes: 2097148 << 10, FreeBytes: 2097148 << 10},
		},
		"arm64": {
			CPU{Model: "0x41/0xd0c", LogicalCount: 2},
			Memory{TotalBytes: 3924604 << 10, AvailableBytes: 3512876 << 10},
			Swap{},
		},
	}
	for osName, wantOS := range oses {
		for arch, w := range arches {
			t.Run(osName+"-"+arch, func(t *testing.T) {
				raw, err := readSummary(t, New(fixtureSource(fixtureFS(t, osName, arch))))
				if err != nil {
					t.Fatalf("Read: %v", err)
				}
				var got Summary
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				want := Summary{
					Hostname: "vps-1", OS: wantOS, Kernel: "6.8.0-45-generic", Arch: runtime.GOARCH,
					UptimeSeconds: 3600, BootTime: testNow.Add(-time.Hour),
					CPU: w.cpu, Memory: w.mem, Swap: w.swap,
					Load:   Load{One: 0.08, Five: 0.03, Fifteen: 0.01},
					RootFS: RootFS{TotalBytes: 50 << 30, AvailableBytes: 20 << 30}, DarkpanelVersion: "v0.1.0",
				}
				if !reflect.DeepEqual(got, want) {
					t.Errorf("summary =\n%+v\nwant\n%+v", got, want)
				}
			})
		}
	}
}

func TestSummaryJSONFieldNames(t *testing.T) {
	raw, err := readSummary(t, New(fixtureSource(fixtureFS(t, "debian-13", "arm64"))))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hostname":"vps-1","os":{"id":"debian","versionId":"13","prettyName":"Debian GNU/Linux 13 (trixie)"},` +
		`"kernel":"6.8.0-45-generic","arch":"` + runtime.GOARCH + `","uptimeSeconds":3600,"bootTime":"2026-10-17T11:00:00Z",` +
		`"cpu":{"model":"0x41/0xd0c","logicalCount":2},"memory":{"totalBytes":4018794496,"availableBytes":3597185024},` +
		`"swap":{"totalBytes":0,"freeBytes":0},"load":{"one":0.08,"five":0.03,"fifteen":0.01},` +
		`"rootFs":{"totalBytes":53687091200,"availableBytes":21474836480},"darkpanelVersion":"v0.1.0"}`
	if string(raw) != want {
		t.Errorf("summary JSON =\n%s\nwant\n%s", raw, want)
	}
}

func TestSummaryFailsOnMissingSource(t *testing.T) {
	for _, name := range []string{"etc/os-release", "proc/cpuinfo", "proc/meminfo", "proc/loadavg", "proc/uptime", "proc/sys/kernel/osrelease"} {
		fsys := fixtureFS(t, "debian-12", "amd64")
		delete(fsys, name)
		if _, err := readSummary(t, New(fixtureSource(fsys))); err == nil {
			t.Errorf("Read without %s succeeded, want an error", name)
		}
	}
	src := fixtureSource(fixtureFS(t, "debian-12", "amd64"))
	src.Statfs = func(string) (uint64, uint64, error) { return 0, 0, errors.New("statfs failed") }
	if _, err := readSummary(t, New(src)); err == nil {
		t.Error("Read with a failing statfs succeeded, want an error")
	}
}

func TestManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled || len(m.Actions) != 0 || len(m.Migrations) != 0 {
		t.Errorf("manifest = %+v, want id hostinfo, version 1, enabled by default, no actions or migrations", m)
	}
	if len(m.Resources) != 1 || m.Resources[0].Name != "hostinfo.summary" || m.Resources[0].OutputType != reflect.TypeFor[Summary]() {
		t.Errorf("resources = %+v, want hostinfo.summary returning Summary", m.Resources)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("NewRegistry: %v", err)
	}
}

func TestPreflightReadsMeminfo(t *testing.T) {
	fsys := fixtureFS(t, "ubuntu-24.04", "amd64")
	checks := New(fixtureSource(fsys)).Manifest().Preflight
	if len(checks) != 1 || checks[0].ID != "meminfo" {
		t.Fatalf("preflight = %+v, want one check meminfo", checks)
	}
	if err := checks[0].Run(context.Background(), core.Env{}); err != nil {
		t.Errorf("preflight with meminfo: %v", err)
	}
	delete(fsys, "proc/meminfo")
	if err := checks[0].Run(context.Background(), core.Env{}); err == nil {
		t.Error("preflight without meminfo succeeded, want an error")
	}
}

func TestProductionModuleReadsThisHost(t *testing.T) {
	m := Module()
	raw, err := m.Manifest().Resources[0].Read(context.Background(), core.Env{Clock: core.SystemClock{}})
	if err != nil {
		t.Fatalf("Read on this host: %v", err)
	}
	var s Summary
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatal(err)
	}
	if s.Hostname == "" || s.Kernel == "" || s.CPU.LogicalCount < 1 || s.Memory.TotalBytes == 0 ||
		s.RootFS.TotalBytes == 0 || s.DarkpanelVersion != buildinfo.Version {
		t.Errorf("summary of this host = %+v, want every field filled", s)
	}
	if err := m.Manifest().Preflight[0].Run(context.Background(), core.Env{}); err != nil {
		t.Errorf("preflight on this host: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/modules/hostinfo/`
Expected: FAIL — the build reports `undefined: Source`.

- [ ] **Step 3: Write the implementation**

Create `internal/modules/hostinfo/hostinfo.go`:

```go
package hostinfo

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"runtime"
	"strings"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
)

// ID is the module id.
const ID = "hostinfo"

// Summary is the output of resource hostinfo.summary (v1).
type Summary struct {
	Hostname         string    `json:"hostname"`
	OS               OS        `json:"os"`
	Kernel           string    `json:"kernel"`
	Arch             string    `json:"arch"`
	UptimeSeconds    int64     `json:"uptimeSeconds"`
	BootTime         time.Time `json:"bootTime"`
	CPU              CPU       `json:"cpu"`
	Memory           Memory    `json:"memory"`
	Swap             Swap      `json:"swap"`
	Load             Load      `json:"load"`
	RootFS           RootFS    `json:"rootFs"`
	DarkpanelVersion string    `json:"darkpanelVersion"`
}

// OS is the distribution from /etc/os-release.
type OS struct {
	ID         string `json:"id"`
	VersionID  string `json:"versionId"`
	PrettyName string `json:"prettyName"`
}

// CPU describes the processors.
type CPU struct {
	Model        string `json:"model"`
	LogicalCount int    `json:"logicalCount"`
}

// Memory is physical memory in bytes.
type Memory struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

// Swap is swap space in bytes.
type Swap struct {
	TotalBytes uint64 `json:"totalBytes"`
	FreeBytes  uint64 `json:"freeBytes"`
}

// Load is the 1, 5 and 15 minute load averages.
type Load struct {
	One     float64 `json:"one"`
	Five    float64 `json:"five"`
	Fifteen float64 `json:"fifteen"`
}

// RootFS is the size of the filesystem mounted at / in bytes.
type RootFS struct {
	TotalBytes     uint64 `json:"totalBytes"`
	AvailableBytes uint64 `json:"availableBytes"`
}

// Source is where the module reads the host from; tests pass fixtures. FS is
// rooted at /, so paths read from it have no leading slash.
type Source struct {
	FS       fs.FS
	Hostname func() (string, error)
	Statfs   func(path string) (totalBytes, availableBytes uint64, err error)
	Version  func() string
}

// New returns the module reading from src.
func New(src Source) core.Module { return module{src: src} }

// Module returns the module reading from this host.
func Module() core.Module {
	return New(Source{
		FS:       os.DirFS("/"),
		Hostname: os.Hostname,
		Statfs:   statfs,
		Version:  func() string { return buildinfo.Version },
	})
}

func statfs(path string) (uint64, uint64, error) {
	var st unix.Statfs_t
	if err := unix.Statfs(path, &st); err != nil {
		return 0, 0, fmt.Errorf("statfs %s: %w", path, err)
	}
	bsize := uint64(st.Bsize) //nolint:gosec // G115: the kernel reports a positive block size
	return st.Blocks * bsize, st.Bavail * bsize, nil
}

type module struct {
	src Source
}

// Manifest declares the module: one resource, no actions, no migrations.
func (m module) Manifest() core.Manifest {
	return core.Manifest{
		ID:             ID,
		Version:        1,
		DefaultEnabled: true,
		Resources:      []core.ResourceDef{core.DefineResource(ID+".summary", m.summary)},
		Preflight:      []core.PreflightCheck{{ID: "meminfo", Run: m.checkMeminfo}},
	}
}

func (m module) checkMeminfo(context.Context, core.Env) error {
	if _, err := fs.ReadFile(m.src.FS, "proc/meminfo"); err != nil {
		return fmt.Errorf("cannot read /proc/meminfo: %w", err)
	}
	return nil
}

func (m module) read(name string) ([]byte, error) {
	b, err := fs.ReadFile(m.src.FS, name)
	if err != nil {
		return nil, fmt.Errorf("hostinfo: read /%s: %w", name, err)
	}
	return b, nil
}

func (m module) summary(_ context.Context, env core.Env) (Summary, error) {
	var s Summary
	var err error
	if s.Hostname, err = m.src.Hostname(); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: hostname: %w", err)
	}
	files := map[string][]byte{}
	for _, name := range []string{"etc/os-release", "proc/sys/kernel/osrelease", "proc/uptime", "proc/cpuinfo", "proc/meminfo", "proc/loadavg"} {
		if files[name], err = m.read(name); err != nil {
			return Summary{}, err
		}
	}
	s.OS = OS(parseOSRelease(files["etc/os-release"]))
	s.Kernel = strings.TrimSpace(string(files["proc/sys/kernel/osrelease"]))
	s.Arch = runtime.GOARCH
	if s.UptimeSeconds, err = parseUptime(files["proc/uptime"]); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.BootTime = env.Clock.Now().UTC().Add(-time.Duration(s.UptimeSeconds) * time.Second).Truncate(time.Second)
	s.CPU = CPU(parseCPUInfo(files["proc/cpuinfo"]))
	mem, err := parseMemInfo(files["proc/meminfo"])
	if err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.Memory = Memory{TotalBytes: mem.TotalBytes, AvailableBytes: mem.AvailableBytes}
	s.Swap = Swap{TotalBytes: mem.SwapTotalBytes, FreeBytes: mem.SwapFreeBytes}
	load, err := parseLoadAvg(files["proc/loadavg"])
	if err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.Load = Load(load)
	if s.RootFS.TotalBytes, s.RootFS.AvailableBytes, err = m.src.Statfs("/"); err != nil {
		return Summary{}, fmt.Errorf("hostinfo: %w", err)
	}
	s.DarkpanelVersion = m.src.Version()
	return s, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -race ./internal/modules/hostinfo/
for f in FuzzParseOSRelease FuzzParseCPUInfo FuzzParseMemInfo FuzzParseLoadAvg FuzzParseUptime; do GOTOOLCHAIN=local timeout 120 go test -run '^$' -fuzz "^$f\$" -fuzztime 10s ./internal/modules/hostinfo/ || break; done
test ! -e internal/modules/hostinfo/testdata/fuzz
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `ok` for the test run (eight `TestSummaryPerOSAndArch` subtests) and for each of the five fuzz runs; no `testdata/fuzz` directory; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/hostinfo/hostinfo.go internal/modules/hostinfo/hostinfo_test.go
git commit -m "feat(hostinfo): add the host-information module"
```

---

### Task 6: Lifecycle tables and agent journal

**Files:**
- Create: `internal/core/store/migrations/controller/0002_lifecycle.sql`
- Create: `internal/core/store/migrations/agent/0002_lifecycle.sql`
- Create: `internal/core/lifecycle/journal.go`
- Test: `internal/core/lifecycle/journal_test.go`

**Interfaces:**
- Consumes: the foundation's `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrateOptions`, `core.Journal`, `core.JournalEntry` and `errcode.JournalOverflow`.
- Produces: Contracts › `internal/core/lifecycle` › Task 6 (`JournalCap`, `AgentJournal`, `NewAgentJournal`, `Entries`, `Ack`, the two `0002_lifecycle.sql` files); package-internal names later tasks use: `formatTime`, `parseTime`, `nullJSON`, the phase and result constants; test helpers `openDBAt(t *testing.T, path, side string) *store.DB` and `openDB(t *testing.T, side string) *store.DB` in `journal_test.go`.

**Items:** 6

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The two migration files are checksummed once applied and can never change (ADR 0005): copy them byte for byte. The agent file is the controller file plus `core_journal`. Times are stored in the fixed-width `timeFormat`, so `expires_at` and `revert_deadline` compare correctly as text; do not switch to `time.RFC3339Nano`, which trims trailing zeros.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/journal_test.go`:

```go
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// openDBAt opens the database at path and applies side's core migrations.
func openDBAt(t *testing.T, path, side string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(side)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: side, BinaryVersion: "test", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 5, Now: time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	return db
}

// openDB opens a new migrated database of side in a temporary directory.
func openDB(t *testing.T, side string) *store.DB {
	t.Helper()
	return openDBAt(t, filepath.Join(t.TempDir(), "test.db"), side)
}

func tableNames(t *testing.T, db *store.DB) map[string]bool {
	t.Helper()
	rows, err := db.Read().Query("SELECT name FROM sqlite_master WHERE type = 'table'")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	names := map[string]bool{}
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			t.Fatal(err)
		}
		names[n] = true
	}
	return names
}

func TestLifecycleTables(t *testing.T) {
	for _, side := range []string{store.SideController, store.SideAgent} {
		names := tableNames(t, openDB(t, side))
		for _, table := range []string{"core_plans", "core_changes", "core_locks"} {
			if !names[table] {
				t.Errorf("%s: table %s missing", side, table)
			}
		}
		if names["core_journal"] != (side == store.SideAgent) {
			t.Errorf("%s: core_journal present = %v, want %v", side, names["core_journal"], side == store.SideAgent)
		}
	}
}

func appendEntries(t *testing.T, db *store.DB, j *AgentJournal, entries ...core.JournalEntry) {
	t.Helper()
	for _, e := range entries {
		if err := db.Write(context.Background(), func(tx *sql.Tx) error { return j.Append(context.Background(), tx, e) }); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}
}

func entry(change string) core.JournalEntry {
	return core.JournalEntry{
		At: time.Date(2026, 10, 17, 1, 2, 3, 4, time.UTC), Actor: "user:alice", Action: "fix.item.set",
		Version: 1, Phase: "apply", ChangeID: change, Input: json.RawMessage(`{"name":"a"}`), Result: "ok",
	}
}

func TestJournalEntriesAndAck(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	noInput := entry("c3")
	noInput.Input = nil
	noInput.Result, noInput.ErrorCode = "error", "internal"
	appendEntries(t, db, j, entry("c1"), entry("c2"), noInput)

	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %d, want 3", len(got))
	}
	want := entry("c1")
	want.Seq = 1
	if g := got[0]; g.Seq != want.Seq || !g.At.Equal(want.At) || g.Actor != want.Actor || g.Action != want.Action ||
		g.Version != want.Version || g.Phase != want.Phase || g.ChangeID != want.ChangeID ||
		string(g.Input) != string(want.Input) || g.Result != want.Result || g.ErrorCode != "" {
		t.Errorf("entry 1 = %+v, want %+v", g, want)
	}
	if g := got[2]; g.Seq != 3 || g.Input != nil || g.Result != "error" || g.ErrorCode != "internal" {
		t.Errorf("entry 3 = %+v, want seq 3, no input, error internal", g)
	}
	if after, _ := j.Entries(ctx, 1, 1); len(after) != 1 || after[0].Seq != 2 {
		t.Errorf("Entries(1, 1) = %+v, want only seq 2", after)
	}
	if err := j.Ack(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if left, _ := j.Entries(ctx, 0, 10); len(left) != 1 || left[0].Seq != 3 {
		t.Errorf("after Ack(2) = %+v, want only seq 3", left)
	}
}

func TestJournalAppendRollsBackWithTransaction(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	boom := errors.New("boom")
	err := db.Write(ctx, func(tx *sql.Tx) error {
		if err := j.Append(ctx, tx, entry("c1")); err != nil {
			return err
		}
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("Write = %v, want boom", err)
	}
	if got, _ := j.Entries(ctx, 0, 10); len(got) != 0 {
		t.Errorf("entries after rollback = %+v, want none", got)
	}
}

func TestJournalOverflowKeepsOneCountingEntry(t *testing.T) {
	ctx := context.Background()
	db := openDB(t, store.SideAgent)
	j := NewAgentJournal(db)
	if j.cap != JournalCap {
		t.Fatalf("cap = %d, want JournalCap", j.cap)
	}
	j.cap = 3
	for _, c := range []string{"c1", "c2", "c3", "c4", "c5"} {
		appendEntries(t, db, j, entry(c))
	}
	got, err := j.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 {
		t.Fatalf("entries = %+v, want 3", got)
	}
	if got[0].ChangeID != "c4" || got[2].ChangeID != "c5" {
		t.Errorf("kept %s and %s, want c4 and c5", got[0].ChangeID, got[2].ChangeID)
	}
	m := got[1]
	if m.Phase != "interrupt" || m.Result != "error" || m.ErrorCode != "journal.overflow" ||
		m.Actor != "system:journal" || string(m.Input) != `{"dropped":3}` {
		t.Errorf("overflow entry = %+v, want interrupt/error/journal.overflow counting 3 dropped", m)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/`
Expected: FAIL — the build reports `undefined: NewAgentJournal`.

- [ ] **Step 3: Write the migrations and the journal**

Create `internal/core/store/migrations/controller/0002_lifecycle.sql`:

```sql
-- Change lifecycle tables (spec section 3, ADR 0004), read and written only
-- through internal/core/lifecycle. Every plan and change row carries record
-- format 1; times are fixed-width RFC 3339 UTC text, so they compare as text.

CREATE TABLE core_plans (
    id           TEXT PRIMARY KEY,
    format       INTEGER NOT NULL DEFAULT 1,
    host_id      TEXT NOT NULL,
    action       TEXT NOT NULL,
    version      INTEGER NOT NULL,
    actor        TEXT NOT NULL,
    input_ct     BLOB,
    input_hash   TEXT NOT NULL,
    state_hash   TEXT NOT NULL,
    diff_json    TEXT NOT NULL,
    guards_json  TEXT NOT NULL,
    confirmation TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
) STRICT;

CREATE TABLE core_changes (
    id              TEXT PRIMARY KEY,
    format          INTEGER NOT NULL DEFAULT 1,
    plan_id         TEXT NOT NULL UNIQUE REFERENCES core_plans (id),
    host_id         TEXT NOT NULL,
    action          TEXT NOT NULL,
    version         INTEGER NOT NULL,
    class           TEXT NOT NULL,
    compensated_by  TEXT NOT NULL,
    actor           TEXT NOT NULL,
    state           TEXT NOT NULL,
    input_json      TEXT NOT NULL,
    snapshot_ct     BLOB,
    revert_deadline TEXT,
    output_json     TEXT,
    error_code      TEXT,
    error_message   TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
) STRICT;

CREATE INDEX core_changes_state ON core_changes (state);

-- A lock's owner is a change id, or an update transaction id from M2 (ADR 0007).
CREATE TABLE core_locks (
    key   TEXT PRIMARY KEY,
    owner TEXT NOT NULL
) STRICT;
```

Create `internal/core/store/migrations/agent/0002_lifecycle.sql`:

```sql
-- Change lifecycle tables (spec section 3, ADR 0004), read and written only
-- through internal/core/lifecycle. Every plan and change row carries record
-- format 1; times are fixed-width RFC 3339 UTC text, so they compare as text.

CREATE TABLE core_plans (
    id           TEXT PRIMARY KEY,
    format       INTEGER NOT NULL DEFAULT 1,
    host_id      TEXT NOT NULL,
    action       TEXT NOT NULL,
    version      INTEGER NOT NULL,
    actor        TEXT NOT NULL,
    input_ct     BLOB,
    input_hash   TEXT NOT NULL,
    state_hash   TEXT NOT NULL,
    diff_json    TEXT NOT NULL,
    guards_json  TEXT NOT NULL,
    confirmation TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL
) STRICT;

CREATE TABLE core_changes (
    id              TEXT PRIMARY KEY,
    format          INTEGER NOT NULL DEFAULT 1,
    plan_id         TEXT NOT NULL UNIQUE REFERENCES core_plans (id),
    host_id         TEXT NOT NULL,
    action          TEXT NOT NULL,
    version         INTEGER NOT NULL,
    class           TEXT NOT NULL,
    compensated_by  TEXT NOT NULL,
    actor           TEXT NOT NULL,
    state           TEXT NOT NULL,
    input_json      TEXT NOT NULL,
    snapshot_ct     BLOB,
    revert_deadline TEXT,
    output_json     TEXT,
    error_code      TEXT,
    error_message   TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
) STRICT;

CREATE INDEX core_changes_state ON core_changes (state);

-- A lock's owner is a change id, or an update transaction id from M2 (ADR 0007).
CREATE TABLE core_locks (
    key   TEXT PRIMARY KEY,
    owner TEXT NOT NULL
) STRICT;

-- The agent journal (ADR 0003): the controller reads entries after its cursor
-- and acknowledges them; acknowledged entries are deleted.
CREATE TABLE core_journal (
    seq        INTEGER PRIMARY KEY AUTOINCREMENT,
    format     INTEGER NOT NULL DEFAULT 1,
    at         TEXT NOT NULL,
    actor      TEXT NOT NULL,
    action     TEXT NOT NULL,
    version    INTEGER NOT NULL,
    phase      TEXT NOT NULL,
    change_id  TEXT NOT NULL,
    input_json TEXT,
    result     TEXT NOT NULL,
    error_code TEXT NOT NULL
) STRICT;
```

Create `internal/core/lifecycle/journal.go`:

```go
// Package lifecycle is the change lifecycle engine of ADR 0004 (spec section
// 3): plans, apply, confirm, revert, recovery and the agent journal. Both
// sides use it, the agent runtime for agent actions and the controller for
// controller-side actions, each over its own database.
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// JournalCap is the most unacknowledged entries the agent journal keeps
// (spec section 11).
const JournalCap = 10000

// Journal phases and results (spec section 3).
const (
	phaseApply     = "apply"
	phaseConfirm   = "confirm"
	phaseRevert    = "revert"
	phaseRecover   = "recover"
	phaseInterrupt = "interrupt"
	resultOK       = "ok"
	resultError    = "error"
)

const actorJournal = "system:journal"

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// correctly as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("lifecycle: stored time %q: %w", s, err)
	}
	return t, nil
}

// nullJSON stores an empty document as NULL.
func nullJSON(raw json.RawMessage) any {
	if len(raw) == 0 {
		return nil
	}
	return string(raw)
}

// AgentJournal is the agent's core.Journal over core_journal. The controller
// reads it with Entries and acknowledges what it stored with Ack (ADR 0003).
type AgentJournal struct {
	db  *store.DB
	cap int
}

// NewAgentJournal returns the journal of the agent database db.
func NewAgentJournal(db *store.DB) *AgentJournal {
	return &AgentJournal{db: db, cap: JournalCap}
}

type overflowInput struct {
	Dropped int `json:"dropped"`
}

// Append writes e inside the change's transaction. When the journal is full,
// the oldest entries are dropped and a single overflow entry, replacing any
// earlier one, counts every entry dropped since the controller last
// acknowledged one.
func (j *AgentJournal) Append(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	var n int
	if err := tx.QueryRowContext(ctx, "SELECT count(*) FROM core_journal").Scan(&n); err != nil {
		return fmt.Errorf("lifecycle: count journal: %w", err)
	}
	if n >= j.cap {
		if err := j.overflow(ctx, tx, n, e.At); err != nil {
			return err
		}
	}
	return insertEntry(ctx, tx, e)
}

// overflow makes room for the new entry and an overflow entry; n is the
// current number of entries.
func (j *AgentJournal) overflow(ctx context.Context, tx *sql.Tx, n int, at time.Time) error {
	var dropped overflowInput
	var seq int64
	var input string
	err := tx.QueryRowContext(ctx,
		"SELECT seq, input_json FROM core_journal WHERE error_code = ? ORDER BY seq DESC LIMIT 1",
		string(errcode.JournalOverflow)).Scan(&seq, &input)
	switch {
	case err == nil:
		if err := json.Unmarshal([]byte(input), &dropped); err != nil {
			return fmt.Errorf("lifecycle: read overflow entry: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM core_journal WHERE seq = ?", seq); err != nil {
			return fmt.Errorf("lifecycle: replace overflow entry: %w", err)
		}
		n--
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("lifecycle: read overflow entry: %w", err)
	}
	res, err := tx.ExecContext(ctx,
		"DELETE FROM core_journal WHERE seq IN (SELECT seq FROM core_journal ORDER BY seq LIMIT ?)", n-j.cap+2)
	if err != nil {
		return fmt.Errorf("lifecycle: drop journal entries: %w", err)
	}
	k, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("lifecycle: drop journal entries: %w", err)
	}
	dropped.Dropped += int(k)
	raw, err := json.Marshal(dropped)
	if err != nil {
		return fmt.Errorf("lifecycle: overflow entry: %w", err)
	}
	return insertEntry(ctx, tx, core.JournalEntry{
		At: at, Actor: actorJournal, Phase: phaseInterrupt, Input: raw,
		Result: resultError, ErrorCode: string(errcode.JournalOverflow),
	})
}

func insertEntry(ctx context.Context, tx *sql.Tx, e core.JournalEntry) error {
	_, err := tx.ExecContext(ctx,
		`INSERT INTO core_journal (at, actor, action, version, phase, change_id, input_json, result, error_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		formatTime(e.At), e.Actor, e.Action, e.Version, e.Phase, e.ChangeID, nullJSON(e.Input), e.Result, e.ErrorCode)
	if err != nil {
		return fmt.Errorf("lifecycle: append journal: %w", err)
	}
	return nil
}

// Entries returns up to limit entries after afterSeq in sequence order; a
// limit of 0 or less means 1000.
func (j *AgentJournal) Entries(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	if limit <= 0 {
		limit = 1000
	}
	rows, err := j.db.Read().QueryContext(ctx,
		`SELECT seq, at, actor, action, version, phase, change_id, input_json, result, error_code
		 FROM core_journal WHERE seq > ? ORDER BY seq LIMIT ?`, afterSeq, limit)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: read journal: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var out []core.JournalEntry
	for rows.Next() {
		var e core.JournalEntry
		var at string
		var input sql.NullString
		if err := rows.Scan(&e.Seq, &at, &e.Actor, &e.Action, &e.Version, &e.Phase, &e.ChangeID,
			&input, &e.Result, &e.ErrorCode); err != nil {
			return nil, fmt.Errorf("lifecycle: read journal: %w", err)
		}
		if e.At, err = parseTime(at); err != nil {
			return nil, err
		}
		if input.Valid {
			e.Input = json.RawMessage(input.String)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

// Ack deletes every entry up to and including seq, once the controller has
// stored them.
func (j *AgentJournal) Ack(ctx context.Context, seq int64) error {
	return j.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM core_journal WHERE seq <= ?", seq); err != nil {
			return fmt.Errorf("lifecycle: ack journal: %w", err)
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/ ./internal/core/store/
GOTOOLCHAIN=local timeout 600 go test -race -count=3 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/controller/0002_lifecycle.sql internal/core/store/migrations/agent/0002_lifecycle.sql internal/core/lifecycle/journal.go internal/core/lifecycle/journal_test.go
git commit -m "feat(lifecycle): add lifecycle tables and journal"
```

---

### Task 7: Input validation

**Files:**
- Create: `internal/core/schema/validate.go`
- Test: `internal/core/schema/validate_test.go`
- Test: `internal/core/schema/module_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/schema` › Task 1 (`Input`; the fixture types of `fixture_test.go`); `core.Validator`, `core.ActionDef`, `core.NewRegistry` and the `Define*` constructors from the foundation; `errcode.InvalidInput`, `errcode.Internal`, `errcode.FieldError`; `github.com/santhosh-tekuri/jsonschema/v6` v6.0.3.
- Produces: Contracts › `internal/core/schema` › Task 7 (`Validator`, `NewValidator`), and the test helpers of `module_test.go` (`fixtureModule`, `fixtureHandler`, `fixtureEmpty`, `fixtureStatus`, `fixtureRegistry(t)`, `fixtureAction(t, name)`) that Tasks 8 and 9 reuse.

**Items:** 2

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The library's own messages quote the failing value (a pattern failure prints the submitted string), and inputs may hold secrets, so `leaves` writes its own messages and names only the keyword; keep it that way.

- [ ] **Step 1: Write the failing test**

Create `internal/core/schema/module_test.go`:

```go
package schema

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// fixtureEmpty is an input with no fields.
type fixtureEmpty struct{}

type fixtureStatus struct {
	Healthy bool   `json:"healthy"`
	Detail  string `json:"detail,omitempty"`
}

// fixtureHandler implements every handler interface and does nothing; the
// schema package never calls handlers.
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

// fixtureModule has one action per class, a controller-side break-glass
// action and a resource.
type fixtureModule struct{}

func (fixtureModule) Manifest() core.Manifest {
	h := fixtureHandler[fixtureInput, fixtureOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Version: 1, Side: core.SideAgent, LockKeys: []string{"demo"}}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Version: 1, Class: core.ClassCompensable, Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.purge", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true},
				fixtureHandler[fixtureEmpty, fixtureOutput]{}),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (fixtureStatus, error) {
				return fixtureStatus{}, nil
			}),
		},
	}
}

// fixtureRegistry returns a registry holding only fixtureModule.
func fixtureRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(fixtureModule{})
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	return reg
}

// fixtureAction returns one action of fixtureModule.
func fixtureAction(t *testing.T, name string) core.ActionDef {
	t.Helper()
	def, ok := fixtureRegistry(t).Action(core.ActionRef{Name: name, Version: 1})
	if !ok {
		t.Fatalf("fixture action %s missing", name)
	}
	return def
}
```

Create `internal/core/schema/validate_test.go`:

```go
package schema

import (
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var _ core.Validator = (*Validator)(nil)

const validInput = `{"name":"web","mode":"fast","password":"hunter2hunter2",` +
	`"target":{"host":"example.org","port":443},"backups":[{"host":"b.example.org","port":22}]}`

func TestValidateAccepts(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	for _, raw := range []string{validInput, ` ` + validInput + "\n"} {
		if err := v.ValidateInput(def, json.RawMessage(raw)); err != nil {
			t.Errorf("ValidateInput(%s) = %v, want nil", raw, err)
		}
	}
}

func TestValidateRefuses(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	tests := []struct {
		name, raw, path, message string
	}{
		{"empty", ``, "", "input is empty"},
		{"not json", `{"name":`, "", "input is not a single JSON value"},
		{"trailing value", validInput + ` {}`, "", "input is not a single JSON value"},
		{"trailing bracket", validInput + `}`, "", "input is not a single JSON value"},
		{"missing field", `{"mode":"fast","password":"p","target":{"host":"a","port":1}}`, "/name", "required"},
		{"unknown field", strings.Replace(validInput, `"mode"`, `"extra":1,"mode"`, 1), "/extra", "unknown property"},
		{"wrong type", strings.Replace(validInput, `"name":"web"`, `"name":7`, 1), "/name", "got number, want string"},
		{"pattern", strings.Replace(validInput, `"name":"web"`, `"name":"Web Server"`, 1), "/name", `value fails the "pattern" constraint`},
		{"enum", strings.Replace(validInput, `"fast"`, `"slow"`, 1), "/mode", `value fails the "enum" constraint`},
		{"nested range", strings.Replace(validInput, `"port":443`, `"port":0`, 1), "/target/port", `value fails the "minimum" constraint`},
		{"array element", strings.Replace(validInput, `"port":22`, `"port":22,"zone":"x"`, 1), "/backups/0/zone", "unknown property"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := v.ValidateInput(def, json.RawMessage(tt.raw))
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
				t.Fatalf("err = %v, want %s", err, errcode.InvalidInput)
			}
			found := false
			for _, f := range coded.Fields {
				if f.Path == tt.path && f.Message == tt.message {
					found = true
				}
			}
			if !found {
				t.Errorf("fields = %+v, want {%s %s}", coded.Fields, tt.path, tt.message)
			}
		})
	}
}

func TestValidateNeverQuotesValues(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	raw := strings.Replace(validInput, `"name":"web"`, `"name":"S3cr3t Value"`, 1)
	raw = strings.Replace(raw, `"password":"hunter2hunter2"`, `"password":["hunter2hunter2"]`, 1)
	err := v.ValidateInput(def, json.RawMessage(raw))
	var coded *errcode.Error
	if !errors.As(err, &coded) || len(coded.Fields) < 2 {
		t.Fatalf("err = %v, want two field errors", err)
	}
	for _, text := range append([]string{coded.Error()}, fieldTexts(coded.Fields)...) {
		if strings.Contains(text, "S3cr3t") || strings.Contains(text, "hunter2") {
			t.Errorf("error text quotes the submitted value: %s", text)
		}
	}
}

func fieldTexts(fields []errcode.FieldError) []string {
	var out []string
	for _, f := range fields {
		out = append(out, f.Path+" "+f.Message)
	}
	return out
}

func TestValidateCompilesOnce(t *testing.T) {
	v := NewValidator()
	def := fixtureAction(t, "demo.item.add")
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if err := v.ValidateInput(def, json.RawMessage(validInput)); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := v.ValidateInput(fixtureAction(t, "demo.secret.reset"), json.RawMessage(`{}`)); err != nil {
		t.Errorf("empty object input: %v", err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if len(v.schemas) != 2 {
		t.Errorf("compiled schemas = %d, want 2", len(v.schemas))
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/`
Expected: FAIL — the build reports `undefined: Validator`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/validate.go`:

```go
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/santhosh-tekuri/jsonschema/v6/kind"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Validator is the production core.Validator: it checks an input against the
// action's input schema, compiled once per action version.
type Validator struct {
	mu      sync.Mutex
	schemas map[core.ActionRef]*jsonschema.Schema
}

// NewValidator returns an empty validator; schemas compile on first use.
func NewValidator() *Validator {
	return &Validator{schemas: map[core.ActionRef]*jsonschema.Schema{}}
}

// ValidateInput checks raw against def's input schema. A refusal is an
// *errcode.Error with code request.invalid_input and one field error per
// failing value; no message quotes a submitted value, which may be a secret.
func (v *Validator) ValidateInput(def core.ActionDef, raw json.RawMessage) error {
	sch, err := v.compiled(def)
	if err != nil {
		return errcode.Wrap(errcode.Internal, err, "")
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return invalid(errcode.FieldError{Path: "", Message: "input is empty"})
	}
	// UnmarshalJSON refuses trailing data; its error text can quote input
	// bytes, so it is not reported.
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return invalid(errcode.FieldError{Path: "", Message: "input is not a single JSON value"})
	}
	err = sch.Validate(doc)
	var verr *jsonschema.ValidationError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &verr):
		return invalid(leaves(verr, nil)...)
	default:
		return errcode.Wrap(errcode.Internal, err, "")
	}
}

func invalid(fields ...errcode.FieldError) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "input does not match the action's schema", Fields: fields}
}

func (v *Validator) compiled(def core.ActionDef) (*jsonschema.Schema, error) {
	ref := def.Ref()
	v.mu.Lock()
	sch, ok := v.schemas[ref]
	v.mu.Unlock()
	if ok {
		return sch, nil
	}
	raw, err := Input(def.InputType)
	if err != nil {
		return nil, err
	}
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		return nil, fmt.Errorf("schema: %s: %w", ref, err)
	}
	url := "urn:darkpanel:input:" + ref.String()
	c := jsonschema.NewCompiler()
	if err := c.AddResource(url, doc); err != nil {
		return nil, fmt.Errorf("schema: %s: %w", ref, err)
	}
	sch, err = c.Compile(url)
	if err != nil {
		return nil, fmt.Errorf("schema: compile %s: %w", ref, err)
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.schemas[ref] = sch
	return sch, nil
}

// leaves turns the failing leaves of a validation error tree into field
// errors.
func leaves(e *jsonschema.ValidationError, out []errcode.FieldError) []errcode.FieldError {
	if len(e.Causes) > 0 {
		for _, c := range e.Causes {
			out = leaves(c, out)
		}
		return out
	}
	at := pointer(e.InstanceLocation)
	switch k := e.ErrorKind.(type) {
	case *kind.Required:
		for _, name := range k.Missing {
			out = append(out, errcode.FieldError{Path: at + "/" + escape(name), Message: "required"})
		}
	case *kind.AdditionalProperties:
		for _, name := range k.Properties {
			out = append(out, errcode.FieldError{Path: at + "/" + escape(name), Message: "unknown property"})
		}
	case *kind.Type:
		out = append(out, errcode.FieldError{Path: at, Message: fmt.Sprintf("got %s, want %s", k.Got, strings.Join(k.Want, " or "))})
	default:
		// The library's own messages quote the failing value, so only the
		// keyword is named.
		out = append(out, errcode.FieldError{Path: at, Message: fmt.Sprintf("value fails the %q constraint", strings.Join(k.KeywordPath(), "/"))})
	}
	return out
}

// pointer renders an instance location as a JSON pointer ("" for the root).
func pointer(tokens []string) string {
	var sb strings.Builder
	for _, tok := range tokens {
		sb.WriteString("/" + escape(tok))
	}
	return sb.String()
}

func escape(tok string) string {
	return strings.ReplaceAll(strings.ReplaceAll(tok, "~", "~0"), "/", "~1")
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`, `go vet` prints nothing, golangci-lint prints `0 issues.`, and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/module_test.go internal/core/schema/validate_test.go internal/core/schema/validate.go
git commit -m "feat(schema): validate inputs against schemas"
```

---

### Task 8: Schema snapshots

**Files:**
- Create: `internal/core/schema/snapshot.go`
- Test: `internal/core/schema/snapshot_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/schema` › Task 1 (`Input`, `Output`), Task 7 (test helpers `fixtureRegistry`, `fixtureAction`), Task 4 (`Compare`, used by the test to prove a snapshot compares clean with itself).
- Produces: Contracts › `internal/core/schema` › Task 8 (`ActionSnapshot`, `ResourceSnapshot`, `Snapshots`, `WriteSnapshots`).

**Items:** 2

**Depends on:** Task 1, Task 4, Task 7

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

`WriteSnapshots` never touches `released/`: those are the frozen snapshots of released tags (ADR 0002). It works through `os.Root`, so a symlink inside the snapshot directory cannot redirect a write or a delete.

- [ ] **Step 1: Write the failing test**

Create `internal/core/schema/snapshot_test.go`:

```go
package schema

import (
	"bytes"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestActionSnapshot(t *testing.T) {
	def := fixtureAction(t, "demo.item.add")
	b, err := ActionSnapshot(def)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "{\n  \"name\": \"demo.item.add\",\n") || !strings.HasSuffix(string(b), "}\n") {
		t.Errorf("snapshot is not two-space indented with a final newline:\n%s", b)
	}
	var got struct {
		Name       string          `json:"name"`
		Version    int             `json:"version"`
		Class      string          `json:"class"`
		Side       string          `json:"side"`
		BreakGlass bool            `json:"breakGlass"`
		Input      json.RawMessage `json:"input"`
		Output     json.RawMessage `json:"output"`
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Name != "demo.item.add" || got.Version != 1 || got.Class != "revertible" || got.Side != "agent" || got.BreakGlass {
		t.Errorf("header = %+v", got)
	}
	in, err := Input(def.InputType)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Output(def.OutputType)
	if err != nil {
		t.Fatal(err)
	}
	if !jsonEqual(t, got.Input, in) || !jsonEqual(t, got.Output, out) {
		t.Error("snapshot schemas differ from Input and Output")
	}
	if violations, err := Compare(b, b); err != nil || len(violations) != 0 {
		t.Errorf("Compare(snapshot, snapshot) = %q, %v; want none", violations, err)
	}
}

func jsonEqual(t *testing.T, a, b []byte) bool {
	t.Helper()
	var x, y bytes.Buffer
	if err := json.Compact(&x, a); err != nil {
		t.Fatal(err)
	}
	if err := json.Compact(&y, b); err != nil {
		t.Fatal(err)
	}
	return bytes.Equal(x.Bytes(), y.Bytes())
}

func TestResourceSnapshot(t *testing.T) {
	res, ok := fixtureRegistry(t).Resource("demo.status")
	if !ok {
		t.Fatal("fixture resource missing")
	}
	b, err := ResourceSnapshot(res)
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]json.RawMessage
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if keys := slices.Sorted(maps.Keys(got)); !slices.Equal(keys, []string{"name", "output"}) {
		t.Errorf("keys = %v, want [name output]", keys)
	}
	if string(got["name"]) != `"demo.status"` {
		t.Errorf("name = %s", got["name"])
	}
}

func TestSnapshots(t *testing.T) {
	files, err := Snapshots(fixtureRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"demo.item.add/v1.json", "demo.item.get/v1.json", "demo.item.purge/v1.json",
		"demo.item.remove/v1.json", "demo.secret.reset/v1.json", "demo.status/v1.json",
	}
	if got := slices.Sorted(maps.Keys(files)); !slices.Equal(got, want) {
		t.Errorf("paths = %v, want %v", got, want)
	}
}

func TestWriteSnapshots(t *testing.T) {
	dir := t.TempDir()
	stale := map[string]string{
		"demo.item.old/v1.json":         "{}\n",
		"released/v0.1.0/x.y.z/v1.json": "{}\n",
		"README.md":                     "kept\n",
	}
	for name, content := range stale {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Snapshots(fixtureRegistry(t))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 { // a second run changes nothing
		if err := WriteSnapshots(dir, files); err != nil {
			t.Fatal(err)
		}
	}
	for name, want := range files {
		got, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // G304: a path under the test's temporary directory
		if err != nil || !bytes.Equal(got, want) {
			t.Errorf("%s: %v, content equal %t", name, err, bytes.Equal(got, want))
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "demo.item.old")); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stale snapshot directory: %v, want it removed", err)
	}
	for _, kept := range []string{"released/v0.1.0/x.y.z/v1.json", "README.md"} {
		if _, err := os.Stat(filepath.Join(dir, kept)); err != nil {
			t.Errorf("%s: %v, want it kept", kept, err)
		}
	}
}

func TestWriteSnapshotsCreatesDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "schemas")
	if err := WriteSnapshots(dir, map[string][]byte{}); err != nil {
		t.Fatalf("empty registry: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("snapshot directory: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/`
Expected: FAIL — the build reports `undefined: ActionSnapshot`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/snapshot.go`:

```go
package schema

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path"
	"slices"

	"github.com/darkraise/darkpanel/internal/core"
)

type actionSnapshot struct {
	Name       string          `json:"name"`
	Version    int             `json:"version"`
	Class      core.Class      `json:"class"`
	Side       core.Side       `json:"side"`
	BreakGlass bool            `json:"breakGlass"`
	Input      json.RawMessage `json:"input"`
	Output     json.RawMessage `json:"output"`
}

type resourceSnapshot struct {
	Name   string          `json:"name"`
	Output json.RawMessage `json:"output"`
}

// ActionSnapshot returns the committed form of one action version: its
// declaration and both schemas, indented with two spaces, ending in a newline.
func ActionSnapshot(def core.ActionDef) ([]byte, error) {
	in, err := Input(def.InputType)
	if err != nil {
		return nil, err
	}
	out, err := Output(def.OutputType)
	if err != nil {
		return nil, err
	}
	s := def.Spec
	return indented(actionSnapshot{s.Name, s.Version, s.Class, s.Side, s.BreakGlass, in, out})
}

// ResourceSnapshot returns the committed form of a resource: its name and
// output schema.
func ResourceSnapshot(def core.ResourceDef) ([]byte, error) {
	out, err := Output(def.OutputType)
	if err != nil {
		return nil, err
	}
	return indented(resourceSnapshot{def.Name, out})
}

func indented(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("schema: snapshot: %w", err)
	}
	return append(b, '\n'), nil
}

// Snapshots returns every snapshot of reg keyed by its path under the
// snapshot directory: "<action>/v<n>.json" and "<resource>/v1.json".
func Snapshots(reg *core.Registry) (map[string][]byte, error) {
	files := map[string][]byte{}
	for _, def := range reg.Actions() {
		b, err := ActionSnapshot(def)
		if err != nil {
			return nil, err
		}
		files[fmt.Sprintf("%s/v%d.json", def.Spec.Name, def.Spec.Version)] = b
	}
	for _, def := range reg.Resources() {
		b, err := ResourceSnapshot(def)
		if err != nil {
			return nil, err
		}
		files[def.Name+"/v1.json"] = b
	}
	return files, nil
}

// WriteSnapshots writes files under dir, creating it when missing, and deletes
// every other .json file there, and directories left empty, except under
// dir/released, which holds the frozen snapshots of released tags (ADR 0002).
// Every operation stays inside dir, even through a symlink.
func WriteSnapshots(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return fmt.Errorf("schema: %w", err)
	}
	defer func() { _ = root.Close() }()
	for name, b := range files {
		if err := root.MkdirAll(path.Dir(name), 0o750); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		if err := root.WriteFile(name, b, 0o600); err != nil {
			return fmt.Errorf("schema: %w", err)
		}
	}
	var dirs []string
	err = fs.WalkDir(root.FS(), ".", func(name string, d fs.DirEntry, err error) error {
		switch {
		case err != nil:
			return err
		case name == "released":
			return fs.SkipDir
		case d.IsDir():
			if name != "." {
				dirs = append(dirs, name)
			}
			return nil
		case path.Ext(name) != ".json":
			return nil
		}
		if _, ok := files[name]; !ok {
			return root.Remove(name)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("schema: prune %s: %w", dir, err)
	}
	// Deepest first, so a parent is empty once its children are gone.
	slices.Sort(dirs)
	slices.Reverse(dirs)
	for _, d := range dirs {
		entries, err := fs.ReadDir(root.FS(), d)
		if err != nil {
			return fmt.Errorf("schema: %w", err)
		}
		if len(entries) == 0 {
			if err := root.Remove(d); err != nil {
				return fmt.Errorf("schema: %w", err)
			}
		}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`, `go vet` prints nothing, golangci-lint prints `0 issues.`, and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/snapshot.go internal/core/schema/snapshot_test.go
git commit -m "feat(schema): write schema snapshots"
```

---

### Task 9: OpenAPI document

**Files:**
- Create: `internal/core/schema/openapi.go`
- Test: `internal/core/schema/openapi_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/schema` › Task 1 (`Input`, `Output`), Task 7 (test helper `fixtureRegistry`); `core.DiffLine`, `core.Guard`, `errcode.FieldError` (their JSON tags shape the shared components).
- Produces: Contracts › `internal/core/schema` › Task 9 (`Route`, `OpenAPI`). Component names: `Plan`, `Change`, `ApplyRequest`, `Problem` (schemas) and `Problem` (response); action schemas are `<action>.v<n>.input` and `<action>.v<n>.output`, resource schemas `<resource>.output`, each nested type `<that name>.<Go type name>`; operation ids `<action>.v<n>.read|plan|apply` and `<resource>.get`.

**Depends on:** Task 1, Task 7

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

The document embeds the action schemas, whose `#/$defs/` references would resolve against the document root, so `lift` moves every definition into `components/schemas` and rewrites the references; `TestOpenAPISchemasCompile` proves they resolve. The wire types mirror spec section 12; plan 3's API package must emit the same shapes, including `[]` rather than `null` for empty `diff` and `guards`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/schema/openapi_test.go`:

```go
package schema

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var testRoutes = []Route{
	{Method: "GET", Path: "/healthz", Summary: "Health", Public: true},
	{Method: "POST", Path: "/api/v1/auth/login", Summary: "Log in", Public: true},
	{Method: "GET", Path: "/api/v1/changes/{id}", Summary: "Show a change"},
	{Method: "POST", Path: "/api/v1/changes/{id}/confirm", Summary: "Confirm a change"},
}

func openAPIDoc(t *testing.T) (map[string]any, []byte) {
	t.Helper()
	b, err := OpenAPI(fixtureRegistry(t), testRoutes)
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatalf("document is not JSON: %v", err)
	}
	return doc, b
}

func findOperation(t *testing.T, doc map[string]any, method, path string) map[string]any {
	t.Helper()
	item, ok := doc["paths"].(map[string]any)[path].(map[string]any)
	if !ok {
		t.Fatalf("no path %s", path)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no %s %s", method, path)
	}
	return op
}

func TestOpenAPIStructure(t *testing.T) {
	doc, _ := openAPIDoc(t)
	if doc["openapi"] != "3.1.0" {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	if info, _ := doc["info"].(map[string]any); info["title"] != "Darkpanel API" || info["version"] != "v1" {
		t.Errorf("info = %v", doc["info"])
	}
	components, _ := doc["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	if _, ok := schemes["session"]; !ok {
		t.Error("no session security scheme")
	}
	if token, _ := schemes["token"].(map[string]any); token["scheme"] != "bearer" {
		t.Errorf("token scheme = %v", schemes["token"])
	}
	for _, name := range []string{"Plan", "Change", "ApplyRequest", "Problem"} {
		if _, ok := components["schemas"].(map[string]any)[name]; !ok {
			t.Errorf("no component schema %s", name)
		}
	}
}

func TestOpenAPIRoutes(t *testing.T) {
	doc, _ := openAPIDoc(t)
	if sec, ok := findOperation(t, doc, "get", "/healthz")["security"].([]any); !ok || len(sec) != 0 {
		t.Errorf("public route security = %v, want []", sec)
	}
	op := findOperation(t, doc, "get", "/api/v1/changes/{id}")
	if sec, _ := op["security"].([]any); len(sec) != 2 {
		t.Errorf("protected route security = %v, want session and token", sec)
	}
	params, _ := op["parameters"].([]any)
	if len(params) != 1 || params[0].(map[string]any)["name"] != "id" || params[0].(map[string]any)["in"] != "path" {
		t.Errorf("parameters = %v, want the path parameter id", params)
	}
	findOperation(t, doc, "post", "/api/v1/changes/{id}/confirm")
}

func TestOpenAPIActions(t *testing.T) {
	doc, raw := openAPIDoc(t)
	for _, want := range []struct{ method, path string }{
		{"post", "/api/v1/hosts/{host}/actions/demo.item.get/v1/read"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.add/v1/plan"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.add/v1/apply"},
		{"post", "/api/v1/hosts/{host}/actions/demo.item.purge/v1/plan"},
		{"post", "/api/v1/actions/demo.item.remove/v1/plan"},
		{"post", "/api/v1/actions/demo.item.remove/v1/apply"},
		{"get", "/api/v1/hosts/{host}/resources/demo/status"},
	} {
		op := findOperation(t, doc, want.method, want.path)
		if sec, _ := op["security"].([]any); len(sec) != 2 {
			t.Errorf("%s %s security = %v", want.method, want.path, sec)
		}
	}
	if _, ok := doc["paths"].(map[string]any)["/api/v1/hosts/{host}/actions/demo.item.get/v1/plan"]; ok {
		t.Error("a read action has a plan operation")
	}
	if bytes.Contains(raw, []byte("demo.secret.reset")) {
		t.Error("the break-glass action appears in the document")
	}
	if bytes.Contains(raw, []byte(`#/$defs/`)) {
		t.Error("a $defs reference was not rewritten")
	}
	in := doc["components"].(map[string]any)["schemas"].(map[string]any)["demo.item.add.v1.input.fixtureInput"].(map[string]any)
	pw := in["properties"].(map[string]any)["password"].(map[string]any)
	if pw["x-secret"] != true || pw["writeOnly"] != true {
		t.Errorf("password = %v, want x-secret and writeOnly", pw)
	}
}

// TestOpenAPIRefsResolve checks that every $ref names a component that exists.
func TestOpenAPIRefsResolve(t *testing.T) {
	doc, _ := openAPIDoc(t)
	components := doc["components"].(map[string]any)
	var walk func(v any)
	walk = func(v any) {
		switch v := v.(type) {
		case map[string]any:
			if ref, ok := v["$ref"].(string); ok {
				parts := strings.Split(strings.TrimPrefix(ref, "#/components/"), "/")
				group, _ := components[parts[0]].(map[string]any)
				if !strings.HasPrefix(ref, "#/components/") || len(parts) != 2 || group[parts[1]] == nil {
					t.Errorf("unresolved $ref %s", ref)
				}
			}
			for _, x := range v {
				walk(x)
			}
		case []any:
			for _, x := range v {
				walk(x)
			}
		}
	}
	walk(doc)
}

// TestOpenAPISchemasCompile compiles every component schema as draft 2020-12
// inside the whole document, which also resolves every reference.
func TestOpenAPISchemasCompile(t *testing.T) {
	_, raw := openAPIDoc(t)
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	c.DefaultDraft(jsonschema.Draft2020)
	if err := c.AddResource("urn:darkpanel:openapi", doc); err != nil {
		t.Fatal(err)
	}
	schemas := doc.(map[string]any)["components"].(map[string]any)["schemas"].(map[string]any)
	for name := range schemas {
		if _, err := c.Compile("urn:darkpanel:openapi#/components/schemas/" + name); err != nil {
			t.Errorf("component %s: %v", name, err)
		}
	}
}

func TestOpenAPIDeterministic(t *testing.T) {
	_, a := openAPIDoc(t)
	_, b := openAPIDoc(t)
	if !bytes.Equal(a, b) {
		t.Error("two builds differ")
	}
}

func TestOpenAPIRefusesDuplicateRoutes(t *testing.T) {
	routes := append([]Route{{Method: "GET", Path: "/healthz"}}, testRoutes...)
	if _, err := OpenAPI(fixtureRegistry(t), routes); err == nil {
		t.Error("OpenAPI accepted a route listed twice")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/`
Expected: FAIL — the build reports `undefined: Route`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/openapi.go`:

```go
package schema

import (
	"encoding/json"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Route is a hand-written API route (spec section 12) that the API package
// passes to OpenAPI. Path uses {name} for path parameters.
type Route struct {
	Method, Path, Summary string
	Public                bool // served without a session or token
}

// The wire shapes of spec section 12 that every action operation shares.
// They mirror the API package's response types, which this package cannot
// import.
type (
	planWire struct {
		PlanID       string          `json:"planId"`
		HostID       *string         `json:"hostId" jsonschema:"nullable"`
		Action       string          `json:"action"`
		Version      int             `json:"version"`
		InputHash    string          `json:"inputHash"`
		Diff         []core.DiffLine `json:"diff"`
		Guards       []core.Guard    `json:"guards"`
		ExpiresAt    time.Time       `json:"expiresAt"`
		Confirmation *string         `json:"confirmation" jsonschema:"nullable"`
	}
	changeErrorWire struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	changeWire struct {
		ID             string           `json:"id"`
		HostID         *string          `json:"hostId" jsonschema:"nullable"`
		Action         string           `json:"action"`
		Version        int              `json:"version"`
		Class          string           `json:"class" jsonschema:"enum=revertible,enum=compensable,enum=irreversible"`
		State          string           `json:"state" jsonschema:"enum=applying,enum=pending,enum=confirmed,enum=reverted,enum=completed,enum=failed,enum=interrupted"`
		Actor          string           `json:"actor"`
		CreatedAt      time.Time        `json:"createdAt"`
		UpdatedAt      time.Time        `json:"updatedAt"`
		RevertDeadline *time.Time       `json:"revertDeadline" jsonschema:"nullable"`
		Output         json.RawMessage  `json:"output"`
		Error          *changeErrorWire `json:"error" jsonschema:"nullable"`
		CompensatedBy  *string          `json:"compensatedBy" jsonschema:"nullable"`
	}
	applyWire struct {
		PlanID                string `json:"planId"`
		ConfirmTimeoutSeconds int    `json:"confirmTimeoutSeconds,omitempty" jsonschema:"minimum=10,maximum=900"`
		Confirmation          string `json:"confirmation,omitempty"`
		ConfirmNow            bool   `json:"confirmNow,omitempty"`
	}
	problemWire struct {
		Type   string               `json:"type"`
		Title  string               `json:"title"`
		Status int                  `json:"status"`
		Detail string               `json:"detail"`
		Code   string               `json:"code"`
		Errors []errcode.FieldError `json:"errors,omitempty"`
	}
)

var (
	pathParam     = regexp.MustCompile(`\{([A-Za-z][A-Za-z0-9]*)\}`)
	componentChar = regexp.MustCompile(`[^A-Za-z0-9._-]`)
)

const componentRef = "#/components/schemas/"

// OpenAPI returns the OpenAPI 3.1 document served at /api/v1/openapi.json:
// the hand-written routes, one read operation per read action and one plan
// and one apply operation per mutating action, and one get operation per
// resource. Break-glass actions are served only on the controller socket and
// are left out (spec section 10).
func OpenAPI(reg *core.Registry, routes []Route) ([]byte, error) {
	d := &document{paths: map[string]map[string]any{}, schemas: map[string]any{}}
	shared := []struct {
		name  string
		t     reflect.Type
		input bool
	}{
		{"Plan", reflect.TypeFor[planWire](), false},
		{"Change", reflect.TypeFor[changeWire](), false},
		{"ApplyRequest", reflect.TypeFor[applyWire](), true},
		{"Problem", reflect.TypeFor[problemWire](), false},
	}
	for _, s := range shared {
		d.schemas[s.name] = inline(s.t, s.input)
	}
	for _, r := range routes {
		if err := d.add(r.Method, r.Path, operation(r.Summary, "", r.Public, nil, nil)); err != nil {
			return nil, err
		}
	}
	for _, def := range reg.Actions() {
		if err := d.addAction(def); err != nil {
			return nil, err
		}
	}
	for _, res := range reg.Resources() {
		out, err := Output(res.OutputType)
		if err != nil {
			return nil, err
		}
		ref, err := d.lift(res.Name+".output", out)
		if err != nil {
			return nil, err
		}
		module, name, _ := strings.Cut(res.Name, ".")
		op := operation("Read "+res.Name, res.Name+".get", false, nil, map[string]any{"$ref": ref})
		if err := d.add("GET", "/api/v1/hosts/{host}/resources/"+module+"/"+name, op); err != nil {
			return nil, err
		}
	}
	b, err := json.Marshal(map[string]any{
		"openapi":           "3.1.0",
		"info":              map[string]any{"title": "Darkpanel API", "version": "v1"},
		"jsonSchemaDialect": "https://json-schema.org/draft/2020-12/schema",
		"paths":             d.paths,
		"components": map[string]any{
			"schemas": d.schemas,
			"securitySchemes": map[string]any{
				"session": map[string]any{"type": "apiKey", "in": "cookie", "name": "__Host-darkpanel_session",
					"description": "Session cookie; darkpanel_session on the plain-HTTP loopback listener."},
				"token": map[string]any{"type": "http", "scheme": "bearer", "description": "API token dpt_<id>_<secret>."},
			},
			"responses": map[string]any{
				"Problem": map[string]any{
					"description": "RFC 9457 problem details",
					"content":     map[string]any{"application/problem+json": map[string]any{"schema": map[string]any{"$ref": componentRef + "Problem"}}},
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("schema: openapi: %w", err)
	}
	return b, nil
}

type document struct {
	paths   map[string]map[string]any
	schemas map[string]any
}

func (d *document) add(method, path string, op map[string]any) error {
	m := strings.ToLower(method)
	item := d.paths[path]
	if item == nil {
		item = map[string]any{}
		d.paths[path] = item
	}
	if _, dup := item[m]; dup {
		return fmt.Errorf("schema: openapi: %s %s listed twice", method, path)
	}
	var params []any
	for _, p := range pathParam.FindAllStringSubmatch(path, -1) {
		params = append(params, map[string]any{"name": p[1], "in": "path", "required": true, "schema": map[string]any{"type": "string"}})
	}
	if params != nil {
		op["parameters"] = params
	}
	item[m] = op
	return nil
}

func (d *document) addAction(def core.ActionDef) error {
	s := def.Spec
	if s.BreakGlass {
		return nil
	}
	id := fmt.Sprintf("%s.v%d", s.Name, s.Version)
	in, err := Input(def.InputType)
	if err != nil {
		return err
	}
	out, err := Output(def.OutputType)
	if err != nil {
		return err
	}
	inRef, err := d.lift(id+".input", in)
	if err != nil {
		return err
	}
	outRef, err := d.lift(id+".output", out)
	if err != nil {
		return err
	}
	base := fmt.Sprintf("/api/v1/actions/%s/v%d/", s.Name, s.Version)
	if s.Side == core.SideAgent {
		base = fmt.Sprintf("/api/v1/hosts/{host}/actions/%s/v%d/", s.Name, s.Version)
	}
	inputBody := wrapper("input", inRef, true)
	if s.Class == core.ClassRead {
		return d.add("POST", base+"read", operation(s.Summary, id+".read", false, inputBody, wrapper("output", outRef, false)))
	}
	if err := d.add("POST", base+"plan", operation(s.Summary, id+".plan", false, inputBody, map[string]any{"$ref": componentRef + "Plan"})); err != nil {
		return err
	}
	return d.add("POST", base+"apply", operation(s.Summary, id+".apply", false,
		map[string]any{"$ref": componentRef + "ApplyRequest"}, map[string]any{"$ref": componentRef + "Change"}))
}

// lift moves a schema from Input or Output into the components under prefix:
// its $defs become "<prefix>.<name>" and its references are rewritten, so the
// document holds no "#/$defs/" reference, which would resolve against the
// document root. It returns the reference to the lifted root.
func (d *document) lift(prefix string, raw json.RawMessage) (string, error) {
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		return "", fmt.Errorf("schema: openapi: %w", err)
	}
	name := func(def string) string { return componentChar.ReplaceAllString(prefix+"."+def, "_") }
	defs, _ := root["$defs"].(map[string]any)
	delete(root, "$defs")
	delete(root, "$schema")
	for def, s := range defs {
		d.schemas[name(def)] = rewriteRefs(s, name)
	}
	d.schemas[prefix] = rewriteRefs(root, name)
	return componentRef + prefix, nil
}

func rewriteRefs(v any, name func(string) string) any {
	switch v := v.(type) {
	case map[string]any:
		for k, x := range v {
			if s, ok := x.(string); ok && k == "$ref" {
				if def, ok := strings.CutPrefix(s, "#/$defs/"); ok {
					v[k] = componentRef + name(def)
				}
				continue
			}
			v[k] = rewriteRefs(x, name)
		}
	case []any:
		for i, x := range v {
			v[i] = rewriteRefs(x, name)
		}
	}
	return v
}

// inline reflects a shared wire type into one self-contained schema.
func inline(t reflect.Type, input bool) map[string]any {
	r := &jsonschema.Reflector{Anonymous: true, DoNotReference: true, AllowAdditionalProperties: !input}
	s := r.ReflectFromType(t)
	s.Version = ""
	b, err := json.Marshal(s)
	if err != nil {
		panic(fmt.Sprintf("schema: openapi: %s: %v", t, err)) // fixed types: a failure is a programming error
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		panic(fmt.Sprintf("schema: openapi: %s: %v", t, err))
	}
	return m
}

// wrapper is the {input} request or {output} response body of spec section 12.
func wrapper(field, ref string, request bool) map[string]any {
	w := map[string]any{
		"type":       "object",
		"properties": map[string]any{field: map[string]any{"$ref": ref}},
		"required":   []string{field},
	}
	if request {
		w["additionalProperties"] = false
	}
	return w
}

func operation(summary, id string, public bool, body, ok map[string]any) map[string]any {
	success := map[string]any{"description": "Success"}
	if ok != nil {
		success["content"] = map[string]any{"application/json": map[string]any{"schema": ok}}
	}
	op := map[string]any{
		"responses": map[string]any{"2XX": success, "default": map[string]any{"$ref": "#/components/responses/Problem"}},
		"security":  []any{},
	}
	if !public {
		op["security"] = []any{map[string]any{"session": []any{}}, map[string]any{"token": []any{}}}
	}
	if summary != "" {
		op["summary"] = summary
	}
	if id != "" {
		op["operationId"] = id
	}
	if body != nil {
		op["requestBody"] = map[string]any{
			"required": true,
			"content":  map[string]any{"application/json": map[string]any{"schema": body}},
		}
	}
	return op
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/schema/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`, `go vet` prints nothing, golangci-lint prints `0 issues.`, and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/openapi.go internal/core/schema/openapi_test.go
git commit -m "feat(schema): build the OpenAPI document"
```

---

### Task 10: Key files

**Files:**
- Create: `internal/core/secret/key.go`
- Test: `internal/core/secret/key_test.go`

**Interfaces:**
- Consumes: nothing beyond the standard library.
- Produces: Contracts › `internal/core/secret` › Task 10 (`KeyPrefix`, `KeySize`, `LoadOrCreateKey`), and the package doc comment of `internal/core/secret`.

**Items:** 8

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The key is installed with `link(2)`, never `rename(2)`: `link` fails with `EEXIST` instead of replacing a file, so an existing key, or one a concurrent `serve` just created, survives and is loaded (spec section 5). Loading opens with `O_NOFOLLOW` and checks mode and owner on the opened descriptor, and no error message quotes key material; keep all three.

- [ ] **Step 1: Write the failing test**

Create `internal/core/secret/key_test.go`:

```go
package secret

import (
	"bytes"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func writeKeyFile(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
}

func TestLoadOrCreateKeyCreates(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.key")
	key, err := LoadOrCreateKey(path)
	if err != nil {
		t.Fatalf("LoadOrCreateKey: %v", err)
	}
	if len(key) != KeySize {
		t.Fatalf("key length = %d, want %d", len(key), KeySize)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("key mode = %o, want 600", mode)
	}
	raw, err := os.ReadFile(path) //nolint:gosec // G304: a path under t.TempDir()
	if err != nil {
		t.Fatal(err)
	}
	if want := KeyPrefix + base64.StdEncoding.EncodeToString(key) + "\n"; string(raw) != want {
		t.Errorf("key file = %q, want %q", raw, want)
	}
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Errorf("directory entries = %v, %v; want only the key (no temporary file left)", entries, err)
	}
	again, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(again, key) {
		t.Errorf("second call = %x, %v; want the same key", again, err)
	}
}

func TestLoadOrCreateKeyKeepsExistingKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "agent.key")
	want := bytes.Repeat([]byte{7}, KeySize)
	writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(want)+"\n", 0o600)
	got, err := LoadOrCreateKey(path)
	if err != nil || !bytes.Equal(got, want) {
		t.Errorf("LoadOrCreateKey = %x, %v; want the existing key", got, err)
	}
}

func TestLoadOrCreateKeyConcurrentCallersAgree(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.key")
	keys := make([][]byte, 8)
	errs := make([]error, 8)
	var wg sync.WaitGroup
	for i := range keys {
		wg.Go(func() { keys[i], errs[i] = LoadOrCreateKey(path) })
	}
	wg.Wait()
	for i := range keys {
		if errs[i] != nil || !bytes.Equal(keys[i], keys[0]) {
			t.Errorf("caller %d = %x, %v; want %x", i, keys[i], errs[i], keys[0])
		}
	}
}

func TestLoadOrCreateKeyRefusesOpenMode(t *testing.T) {
	for _, mode := range []os.FileMode{0o640, 0o604, 0o660} {
		path := filepath.Join(t.TempDir(), "controller.key")
		writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", mode)
		if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "mode") {
			t.Errorf("mode %o: err = %v, want a mode error", mode, err)
		}
	}
}

func TestLoadOrCreateKeyRefusesBadContent(t *testing.T) {
	good := base64.StdEncoding.EncodeToString(make([]byte, KeySize))
	for _, content := range []string{
		"",
		good + "\n",
		"darkpanel-key-v2:" + good + "\n",
		KeyPrefix + "not base64!\n",
		KeyPrefix + base64.StdEncoding.EncodeToString(make([]byte, 16)) + "\n",
		KeyPrefix + good + "\n" + KeyPrefix + good + "\n",
		KeyPrefix + good + "\n\n",
	} {
		path := filepath.Join(t.TempDir(), "controller.key")
		writeKeyFile(t, path, content, 0o600)
		if _, err := LoadOrCreateKey(path); err == nil {
			t.Errorf("content %q accepted", content)
		}
	}
}

func TestLoadOrCreateKeyRefusesSymlink(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "real.key")
	writeKeyFile(t, target, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", 0o600)
	link := filepath.Join(dir, "controller.key")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(link); err == nil {
		t.Error("a symlinked key file was accepted")
	}
}

func TestLoadOrCreateKeyRefusesForeignOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("changing a file's owner needs root")
	}
	path := filepath.Join(t.TempDir(), "controller.key")
	writeKeyFile(t, path, KeyPrefix+base64.StdEncoding.EncodeToString(make([]byte, KeySize))+"\n", 0o600)
	if err := os.Chown(path, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateKey(path); err == nil || !strings.Contains(err.Error(), "owner") {
		t.Errorf("err = %v, want an owner error", err)
	}
}
```

`TestLoadOrCreateKeyRefusesForeignOwner` needs root to change a file's owner, so it skips on CI runners and runs on the development host, where tests run as root.

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/secret/`
Expected: FAIL — the build reports `undefined: LoadOrCreateKey` (and `undefined: KeySize`, `undefined: KeyPrefix`).

- [ ] **Step 3: Write the implementation**

Create `internal/core/secret/key.go`:

```go
// Package secret holds the role keys, the sealer that encrypts secrets at rest
// and the redaction of secret fields (ADR 0005, spec section 5).
package secret

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// KeyPrefix starts every key file; the version lets the format change later.
const KeyPrefix = "darkpanel-key-v1:"

// KeySize is the length of a role key in bytes.
const KeySize = 32

// maxKeyFile bounds what is read from a key file; a valid one is 62 bytes.
const maxKeyFile = 1024

// LoadOrCreateKey returns the role key at path, creating it first when the
// file does not exist. Creation never replaces an existing key: the new key is
// written to a temporary file and hard-linked to path, so a concurrent creator
// or an existing file wins and is loaded instead. Loading refuses a symlink, a
// file group or other can access, a file another user owns and any content
// other than one KeyPrefix line holding KeySize bytes in standard base64.
func LoadOrCreateKey(path string) ([]byte, error) {
	_, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		if err := createKey(path); err != nil {
			return nil, err
		}
	case err != nil:
		return nil, fmt.Errorf("secret: key %s: %w", path, err)
	}
	return loadKey(path)
}

func createKey(path string) (err error) {
	key := make([]byte, KeySize)
	if _, err := rand.Read(key); err != nil {
		return fmt.Errorf("secret: generate key: %w", err)
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".darkpanel-key-*") // created with mode 0600
	if err != nil {
		return fmt.Errorf("secret: create key: %w", err)
	}
	defer func() {
		if rmErr := os.Remove(tmp.Name()); rmErr != nil && !errors.Is(rmErr, fs.ErrNotExist) {
			err = errors.Join(err, fmt.Errorf("secret: remove temporary key: %w", rmErr))
		}
	}()
	_, err = tmp.WriteString(KeyPrefix + base64.StdEncoding.EncodeToString(key) + "\n")
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("secret: write key: %w", err)
	}
	// link(2) fails with EEXIST instead of replacing, unlike rename(2): an
	// existing key, or one a concurrent creator just linked, is kept.
	if err := os.Link(tmp.Name(), path); err != nil && !errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("secret: install key: %w", err)
	}
	return syncDir(dir)
}

func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the key directory comes from paths.Paths, never from a request
	if err != nil {
		return fmt.Errorf("secret: open key directory: %w", err)
	}
	err = d.Sync()
	if closeErr := d.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return fmt.Errorf("secret: sync key directory: %w", err)
	}
	return nil
}

func loadKey(path string) ([]byte, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0) //nolint:gosec // G304: the key path comes from paths.Paths, never from a request
	if err != nil {
		return nil, fmt.Errorf("secret: open key %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, fmt.Errorf("secret: stat key %s: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("secret: key %s is not a regular file", path)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		return nil, fmt.Errorf("secret: key %s has mode %o; group and other must have no access (chmod 600)", path, perm)
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Geteuid() {
		return nil, fmt.Errorf("secret: key %s has another owner than uid %d", path, os.Geteuid())
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxKeyFile))
	if err != nil {
		return nil, fmt.Errorf("secret: read key %s: %w", path, err)
	}
	line, ok := bytes.CutSuffix(raw, []byte("\n"))
	encoded, hasPrefix := bytes.CutPrefix(line, []byte(KeyPrefix))
	if !ok || !hasPrefix || bytes.ContainsAny(encoded, "\r\n") {
		return nil, fmt.Errorf("secret: key %s: want one line starting with %s", path, KeyPrefix)
	}
	key, err := base64.StdEncoding.DecodeString(string(encoded))
	if err != nil || len(key) != KeySize {
		// The decoder's error would quote key material, so it is not reported.
		return nil, fmt.Errorf("secret: key %s: want %d bytes in standard base64 after %s", path, KeySize, KeyPrefix)
	}
	return key, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -v ./internal/core/secret/
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/secret/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `--- ` line of the secret package reads `--- PASS` (`TestLoadOrCreateKeyRefusesForeignOwner` reads `--- SKIP` when not run as root); the race run prints `ok`; vet prints nothing; every package line of `go test ./...` starts with `ok` or `?`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/secret/key.go internal/core/secret/key_test.go
git commit -m "feat(secret): load and create role key files"
```

---

### Task 11: Schema generator

**Files:**
- Create: `internal/core/schema/schemagen/main.go`
- Modify: `Makefile`

**Interfaces:**
- Consumes: Contracts › `internal/core/schema` › Task 8 (`Snapshots`, `WriteSnapshots`); `registry.Build()` from the foundation.
- Produces: Contracts › `internal/core/schema` › Task 11 (the `schemagen` command and the `schemas` Make target). The committed `schemas/` directory is plan 4's: this task creates none.

**Items:** 2

**Depends on:** Task 8

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

The command has no unit test of its own: its logic is `WriteSnapshots`, tested in Task 8, and the check here is running the Make target.

- [ ] **Step 1: Write the failing test**

The failing check is the Make target itself; there is no new test file.

- [ ] **Step 2: Run the test to verify it fails**

Run: `timeout 300 make schemas`
Expected: FAIL with `make: *** No rule to make target 'schemas'.  Stop.`

- [ ] **Step 3: Write the implementation**

Create `internal/core/schema/schemagen/main.go`:

```go
// Command schemagen writes the schema snapshot of every registered action and
// resource (spec section 7). Run it through `make schemas`; a test in
// internal/modules fails when the committed snapshots are out of date.
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

func main() {
	out := flag.String("out", "schemas", "directory to write the snapshots into")
	flag.Parse()
	if err := run(*out); err != nil {
		fmt.Fprintln(os.Stderr, "schemagen:", err)
		os.Exit(1)
	}
}

func run(out string) error {
	reg, err := registry.Build()
	if err != nil {
		return err
	}
	files, err := schema.Snapshots(reg)
	if err != nil {
		return err
	}
	return schema.WriteSnapshots(out, files)
}
```

In `Makefile`, add `schemas` to the `.PHONY` line and add the target after the `test:` rule; the recipe line starts with a tab. The change, as a diff:

```diff
@@ -14,7 +14,7 @@ UI_MARKER := <title>Darkpanel</title>
 # ADR 0001: build only with the toolchain go.mod pins, never a downloaded one.
 export GOTOOLCHAIN := local
 
-.PHONY: web-deps web build verify-build lint-go lint-web lint test-go test-web test-adr test vuln check clean
+.PHONY: web-deps web build verify-build lint-go lint-web lint test-go test-web test-adr test schemas vuln check clean
 
 web-deps:
 	pnpm -C web install --frozen-lockfile
@@ -57,6 +57,9 @@ test-adr:
 
 test: test-go test-web test-adr
 
+schemas:
+	go run ./internal/core/schema/schemagen -out schemas
+
 vuln:
 	go run $(GOVULNCHECK) ./...
 
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
timeout 300 make schemas
find schemas
rm -rf schemas
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `make schemas` prints `go run ./internal/core/schema/schemagen -out schemas` and exits 0; `find schemas` prints only `schemas` (the registry holds no actions or resources until plans 3 and 4); the generated directory is removed again, because plan 4 commits it; `go vet` prints nothing, golangci-lint prints `0 issues.` and `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/schema/schemagen/main.go Makefile
git commit -m "feat(schema): add the schemas Make target"
```

---

### Task 12: Plans and reads

**Files:**
- Create: `internal/core/lifecycle/engine.go`
- Test: `internal/core/lifecycle/fixture_test.go`
- Test: `internal/core/lifecycle/engine_test.go`

**Interfaces:**
- Consumes: Task 6 (`AgentJournal`, `formatTime`, `parseTime`, `openDBAt`); the foundation's `core.Registry`, `core.Define*`, `coretest.Validator`, `coretest.Sealer`, `coretest.NewFakeClock`.
- Produces: Contracts › `internal/core/lifecycle` › Task 12 (`Config`, `New`, `Engine`, `Read`, `Plan`, `Change`, `Changes`, `PlanTTL`) and `(*Engine) Close()`; package-internal names later tasks use: `Engine.cfg`, `Engine.timerMu`, `Engine.timers`, `Engine.closed`, `newID`, `env`, `action`, `planInputAD`, `changeColumns`, `scanChange`; the fixture module and test helpers in `fixture_test.go` (`fixture`, `newFixture`, `(*fixture).config`, `(*fixture).open`, `(*fixture).plan`, `testRedact`, `wantCode`, `alice`, `refGet`, `refSet`, `refAdd`, `refRemove`, `refCtl`, `errCrash`, `testHost`, `fixHost` with `items`, `blocked` and `applies`).

**Items:** 6

**Depends on:** Task 6

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`fixture_test.go` is the fixture module of spec section 3, registered only in tests; Tasks 14, 16 and 20 use it unchanged. Its `itemIn` flags (`failApply`, `failVerify`, `failRevert`, `panicApply`) drive every failure row of the state-transition table. The plan input is sealed under `core_plans.input_ct.<plan id>` (ADR 0005); keep that associated data exactly.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/fixture_test.go`:

```go
package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The fixture module "fix" is registered only in these tests. Its host is a
// map of named items; one handler serves every mutating class, and input
// flags make it fail, panic or refuse to revert.

const testHost = "host-1"

var (
	alice = core.Principal{Actor: "user:alice", UserID: "alice", Scope: core.ScopeOperate, Source: "test"}

	refGet    = core.ActionRef{Name: "fix.item.get", Version: 1}
	refSet    = core.ActionRef{Name: "fix.item.set", Version: 1}    // revertible
	refAdd    = core.ActionRef{Name: "fix.item.add", Version: 1}    // compensable
	refRemove = core.ActionRef{Name: "fix.item.remove", Version: 1} // irreversible
	refCtl    = core.ActionRef{Name: "fix.ctl.get", Version: 1}     // controller-side read
)

// errCrash is what a PanicApply handler panics with: it stands for the
// process dying between persisting intent and recording the result.
var errCrash = errors.New("simulated crash")

type fixHost struct {
	mu      sync.Mutex
	items   map[string]string
	blocked bool // the not-blocked guard fails while set
	applies int
}

type itemIn struct {
	Name       string `json:"name"`
	Value      string `json:"value,omitempty"`
	Secret     string `json:"secret,omitempty" secret:"true"`
	FailApply  bool   `json:"failApply,omitempty"`
	FailVerify bool   `json:"failVerify,omitempty"`
	FailRevert bool   `json:"failRevert,omitempty"`
	PanicApply bool   `json:"panicApply,omitempty"`
}

type itemOut struct {
	Previous string `json:"previous"`
	Token    string `json:"token" secret:"true"`
}

type itemSnap struct {
	Name, Value         string
	Present, FailRevert bool
}

type getIn struct {
	Name string `json:"name"`
}

type getOut struct {
	Value  string `json:"value"`
	Actor  string `json:"actor"`
	HostID string `json:"hostId"`
}

type itemHandler struct{ h *fixHost }

func (x itemHandler) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	state, err := json.Marshal(x.h.items)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff:         []core.DiffLine{{Op: "-", Text: in.Name + "=" + x.h.items[in.Name]}, {Op: "+", Text: in.Name + "=" + in.Value}},
		Guards:       []core.Guard{{ID: "not-blocked", OK: !x.h.blocked, Message: "the host is blocked"}},
		StateHash:    string(state),
		Confirmation: in.Name,
	}, nil
}

func (x itemHandler) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	if in.PanicApply {
		panic(errCrash)
	}
	if in.FailApply {
		return itemOut{}, errors.New("apply failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	x.h.applies++
	prev := x.h.items[in.Name]
	x.h.items[in.Name] = in.Value
	return itemOut{Previous: prev, Token: "tok-" + in.Name}, nil
}

func (x itemHandler) Verify(_ context.Context, _ core.Env, in itemIn, _ itemOut) error {
	if in.FailVerify {
		return errcode.New(errcode.Internal, "verify failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if got := x.h.items[in.Name]; got != in.Value {
		return fmt.Errorf("item %s is %q, want %q", in.Name, got, in.Value)
	}
	return nil
}

func (x itemHandler) Snapshot(_ context.Context, _ core.Env, in itemIn) ([]byte, error) {
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	v, ok := x.h.items[in.Name]
	return json.Marshal(itemSnap{Name: in.Name, Value: v, Present: ok, FailRevert: in.FailRevert})
}

func (x itemHandler) Revert(_ context.Context, _ core.Env, snapshot []byte) error {
	var s itemSnap
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return err
	}
	if s.FailRevert {
		return errors.New("revert failed")
	}
	x.h.mu.Lock()
	defer x.h.mu.Unlock()
	if s.Present {
		x.h.items[s.Name] = s.Value
	} else {
		delete(x.h.items, s.Name)
	}
	return nil
}

type getHandler struct{ h *fixHost }

func (g getHandler) Read(_ context.Context, env core.Env, in getIn) (getOut, error) {
	g.h.mu.Lock()
	defer g.h.mu.Unlock()
	return getOut{Value: g.h.items[in.Name], Actor: env.Principal.Actor, HostID: env.HostID}, nil
}

type fixModule struct{ host *fixHost }

func (m fixModule) Manifest() core.Manifest {
	h := itemHandler{m.host}
	return core.Manifest{ID: "fix", Version: 1, Actions: []core.ActionDef{
		core.DefineRead(core.ActionSpec{Name: refGet.Name, Version: 1, Side: core.SideAgent}, getHandler{m.host}),
		core.DefineRevertible(core.ActionSpec{Name: refSet.Name, Version: 1, Side: core.SideAgent, LockKeys: []string{"fix"}}, h),
		core.DefineMutating(core.ActionSpec{
			Name: refAdd.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"fix"}, CompensatedBy: refRemove.Name,
		}, h),
		core.DefineMutating(core.ActionSpec{
			Name: refRemove.Name, Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent,
			LockKeys: []string{"fix", "fix"},
		}, h),
		core.DefineRead(core.ActionSpec{Name: refCtl.Name, Version: 1, Side: core.SideController}, getHandler{m.host}),
	}}
}

// testRedact replaces top-level fields tagged secret:"true"; production
// engines use secret.RedactJSON over the type's schema.
func testRedact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil {
		return json.RawMessage(`"[redacted]"`)
	}
	for i := range t.NumField() {
		f := t.Field(i)
		name := strings.Split(f.Tag.Get("json"), ",")[0]
		if _, ok := m[name]; ok && f.Tag.Get("secret") == "true" {
			m[name] = json.RawMessage(`"[redacted]"`)
		}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return json.RawMessage(`"[redacted]"`)
	}
	return b
}

type fixture struct {
	t       *testing.T
	host    *fixHost
	clock   *coretest.FakeClock
	reg     *core.Registry
	path    string
	db      *store.DB
	journal *AgentJournal
	eng     *Engine
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	h := &fixHost{items: map[string]string{}}
	reg, err := core.NewRegistry(fixModule{h})
	if err != nil {
		t.Fatal(err)
	}
	f := &fixture{
		t: t, host: h, reg: reg, path: filepath.Join(t.TempDir(), "agent.db"),
		clock: coretest.NewFakeClock(time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)),
	}
	f.open()
	return f
}

func (f *fixture) config() Config {
	return Config{
		Side: core.SideAgent, Registry: f.reg, Validator: coretest.Validator{}, Journal: f.journal, Redact: testRedact,
		Base: core.Env{
			HostID: testHost, DB: f.db, Sealer: coretest.Sealer{}, Clock: f.clock,
			Log: slog.New(slog.DiscardHandler),
		},
	}
}

// open opens the database and a new engine over it, as a starting agent does.
func (f *fixture) open() {
	f.t.Helper()
	f.db = openDBAt(f.t, f.path, store.SideAgent)
	f.journal = NewAgentJournal(f.db)
	eng, err := New(f.config())
	if err != nil {
		f.t.Fatal(err)
	}
	f.eng = eng
	f.t.Cleanup(eng.Close)
}

func (f *fixture) plan(p core.Principal, ref core.ActionRef, input string) core.PlanView {
	f.t.Helper()
	v, err := f.eng.Plan(context.Background(), p, ref, json.RawMessage(input))
	if err != nil {
		f.t.Fatalf("Plan %s: %v", ref, err)
	}
	return v
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}
```

Create `internal/core/lifecycle/engine_test.go`:

```go
package lifecycle

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestNewRequiresDependencies(t *testing.T) {
	f := newFixture(t)
	breaks := map[string]func(*Config){
		"side":      func(c *Config) { c.Side = "browser" },
		"registry":  func(c *Config) { c.Registry = nil },
		"validator": func(c *Config) { c.Validator = nil },
		"journal":   func(c *Config) { c.Journal = nil },
		"redact":    func(c *Config) { c.Redact = nil },
		"db":        func(c *Config) { c.Base.DB = nil },
		"sealer":    func(c *Config) { c.Base.Sealer = nil },
		"clock":     func(c *Config) { c.Base.Clock = nil },
		"log":       func(c *Config) { c.Base.Log = nil },
	}
	for name, brk := range breaks {
		c := f.config()
		brk(&c)
		if _, err := New(c); err == nil {
			t.Errorf("%s: New accepted an incomplete config", name)
		}
	}
}

func TestReadServesReadActionsOfItsSide(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "1"
	out, err := f.eng.Read(context.Background(), alice, refGet, json.RawMessage(`{"name":"a"}`))
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"value":"1","actor":"user:alice","hostId":"host-1"}`; string(out) != want {
		t.Errorf("Read = %s, want %s", out, want)
	}
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		code  errcode.Code
	}{
		{"empty input", refGet, ``, errcode.InvalidInput},
		{"unknown field", refGet, `{"name":"a","x":1}`, errcode.InvalidInput},
		{"mutating action", refSet, `{"name":"a"}`, errcode.ActionUnknown},
		{"other side", refCtl, `{"name":"a"}`, errcode.ActionUnknown},
		{"unknown version", core.ActionRef{Name: refGet.Name, Version: 2}, `{"name":"a"}`, errcode.ActionUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.eng.Read(context.Background(), alice, c.ref, json.RawMessage(c.input))
			wantCode(t, err, c.code)
		})
	}
}

func TestPlanStoresSealedInput(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "old"
	v := f.plan(alice, refSet, `{"value":"new","secret":"s3cret","name":"a"}`)

	if v.PlanID == "" || v.HostID != testHost || v.Action != refSet.Name || v.Version != 1 || v.Confirmation != "" {
		t.Errorf("plan view = %+v", v)
	}
	if want := f.clock.Now().Add(PlanTTL); !v.ExpiresAt.Equal(want) {
		t.Errorf("expires at %v, want %v", v.ExpiresAt, want)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "a=old"}, {Op: "+", Text: "a=new"}}
	wantGuards := []core.Guard{{ID: "not-blocked", OK: true, Message: "the host is blocked"}}
	if !reflect.DeepEqual(v.Diff, wantDiff) || !reflect.DeepEqual(v.Guards, wantGuards) {
		t.Errorf("diff %+v, guards %+v", v.Diff, v.Guards)
	}
	sum := sha256.Sum256([]byte(`{"name":"a","secret":"[redacted]","value":"new"}`)) // testRedact's output
	if want := hex.EncodeToString(sum[:]); v.InputHash != want {
		t.Errorf("input hash = %s, want %s", v.InputHash, want)
	}

	var format int
	var actor string
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT format, actor, input_ct FROM core_plans WHERE id = ?", v.PlanID).
		Scan(&format, &actor, &ct); err != nil {
		t.Fatal(err)
	}
	if format != 1 || actor != alice.Actor {
		t.Errorf("format %d, actor %s; want 1 and %s", format, actor, alice.Actor)
	}
	if bytes.Contains(ct, []byte("s3cret")) {
		t.Error("stored plan input contains the secret in clear")
	}
	plain, err := coretest.Sealer{}.Open("core_plans.input_ct."+v.PlanID, ct)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"name":"a","value":"new","secret":"s3cret"}`; string(plain) != want {
		t.Errorf("sealed input = %s, want the canonical input %s", plain, want)
	}
	if _, err := (coretest.Sealer{}).Open("core_plans.input_ct.other", ct); err == nil {
		t.Error("the sealed input opened under another row's associated data")
	}
}

func TestPlanInputHash(t *testing.T) {
	f := newFixture(t)
	hash := func(in string) string { return f.plan(alice, refSet, in).InputHash }
	base := hash(`{"name":"a","value":"1","secret":"x"}`)
	if hash(`{"secret":"x","value":"1","name":"a"}`) != base {
		t.Error("field order changed the input hash")
	}
	if hash(`{"name":"a","value":"1","secret":"y"}`) != base {
		t.Error("the secret changed the input hash")
	}
	if hash(`{"name":"a","value":"2","secret":"x"}`) == base {
		t.Error("a different value kept the input hash")
	}
}

func TestPlanConfirmationOnlyForIrreversible(t *testing.T) {
	f := newFixture(t)
	if v := f.plan(alice, refRemove, `{"name":"a"}`); v.Confirmation != "a" {
		t.Errorf("irreversible confirmation = %q, want the handler's phrase", v.Confirmation)
	}
	if v := f.plan(alice, refAdd, `{"name":"a"}`); v.Confirmation != "" {
		t.Errorf("compensable confirmation = %q, want none", v.Confirmation)
	}
}

func TestPlanRefusals(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name  string
		ref   core.ActionRef
		input string
		code  errcode.Code
	}{
		{"read action", refGet, `{"name":"a"}`, errcode.ActionUnknown},
		{"empty input", refSet, ``, errcode.InvalidInput},
		{"unknown action", core.ActionRef{Name: "fix.item.nope", Version: 1}, `{}`, errcode.ActionUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := f.eng.Plan(context.Background(), alice, c.ref, json.RawMessage(c.input))
			wantCode(t, err, c.code)
		})
	}
	var n int
	if err := f.db.Read().QueryRow("SELECT count(*) FROM core_plans").Scan(&n); err != nil || n != 0 {
		t.Errorf("plans stored = %d, %v; want 0", n, err)
	}
}

func TestChangeNotFound(t *testing.T) {
	f := newFixture(t)
	_, err := f.eng.Change(context.Background(), "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
	if cs, err := f.eng.Changes(context.Background(), core.ChangeFilter{}); err != nil || len(cs) != 0 {
		t.Errorf("Changes = %v, %v; want none", cs, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/`
Expected: FAIL — the build reports `undefined: Config`.

- [ ] **Step 3: Write the engine**

Create `internal/core/lifecycle/engine.go`:

```go
package lifecycle

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// PlanTTL is how long a plan can be applied after it is made (ADR 0004).
const PlanTTL = 10 * time.Minute

// Change listings return defaultChangeLimit changes unless asked for fewer,
// and never more than maxChangeLimit.
const (
	defaultChangeLimit = 100
	maxChangeLimit     = 500
)

// Config wires an engine to one side. Base.DB, Base.Sealer, Base.Clock and
// Base.Log are the engine's own; Base.HostID is the agent's host id, empty on
// the controller. Every handler call receives Base with Principal set to the
// caller.
type Config struct {
	Side      core.Side
	Registry  *core.Registry
	Validator core.Validator
	Journal   core.Journal
	// Redact returns raw, a JSON value of type t, with every secret field
	// replaced by "[redacted]".
	Redact func(t reflect.Type, raw json.RawMessage) json.RawMessage
	Base   core.Env
}

// Engine runs the change lifecycle of one side over its database. Its methods
// are safe for concurrent use.
type Engine struct {
	cfg Config

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy

	timerMu sync.Mutex
	timers  map[string]core.Timer // armed revert timers by change id
	closed  bool
}

// New returns an engine for cfg. Run Recover before serving requests.
func New(cfg Config) (*Engine, error) {
	if cfg.Side != core.SideController && cfg.Side != core.SideAgent {
		return nil, fmt.Errorf("lifecycle: unknown side %q", cfg.Side)
	}
	required := []struct {
		name string
		set  bool
	}{
		{"Registry", cfg.Registry != nil},
		{"Validator", cfg.Validator != nil},
		{"Journal", cfg.Journal != nil},
		{"Redact", cfg.Redact != nil},
		{"Base.DB", cfg.Base.DB != nil},
		{"Base.Sealer", cfg.Base.Sealer != nil},
		{"Base.Clock", cfg.Base.Clock != nil},
		{"Base.Log", cfg.Base.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return nil, fmt.Errorf("lifecycle: Config.%s is required", r.name)
		}
	}
	return &Engine{cfg: cfg, entropy: ulid.Monotonic(rand.Reader, 0), timers: map[string]core.Timer{}}, nil
}

// Close stops every armed revert timer and arms no new one. A change whose
// timer was stopped stays pending until the next start's Recover reverts it.
func (e *Engine) Close() {
	e.timerMu.Lock()
	defer e.timerMu.Unlock()
	e.closed = true
	for id, t := range e.timers {
		t.Stop()
		delete(e.timers, id)
	}
}

// newID returns a ULID stamped with the engine's clock; ids made by one
// engine sort in creation order.
func (e *Engine) newID() string {
	e.idMu.Lock()
	defer e.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(e.cfg.Base.Clock.Now()), e.entropy).String()
}

func (e *Engine) env(p core.Principal) core.Env {
	env := e.cfg.Base
	env.Principal = p
	return env
}

// action returns a registered action of this engine's side.
func (e *Engine) action(ref core.ActionRef) (core.ActionDef, error) {
	def, ok := e.cfg.Registry.Action(ref)
	if !ok || def.Spec.Side != e.cfg.Side {
		return core.ActionDef{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("unknown action %s", ref))
	}
	return def, nil
}

// Read runs a read action. Reads are not journaled; they change nothing.
func (e *Engine) Read(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	def, err := e.action(ref)
	if err != nil {
		return nil, err
	}
	if def.Spec.Class != core.ClassRead {
		return nil, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is not a read action", ref))
	}
	if err := e.cfg.Validator.ValidateInput(def, input); err != nil {
		return nil, err
	}
	return def.Handler.Read(ctx, e.env(p), input)
}

// Plan validates input, runs the handler's Plan and stores the plan with its
// canonical input sealed (spec section 3). The plan expires after PlanTTL.
func (e *Engine) Plan(ctx context.Context, p core.Principal, ref core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	def, err := e.action(ref)
	if err != nil {
		return core.PlanView{}, err
	}
	if def.Spec.Class == core.ClassRead {
		return core.PlanView{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("%s is a read action and has no plan", ref))
	}
	canonical, err := e.canonical(def, input)
	if err != nil {
		return core.PlanView{}, err
	}
	out, err := def.Handler.Plan(ctx, e.env(p), canonical)
	if err != nil {
		return core.PlanView{}, err
	}
	if def.Spec.Class != core.ClassIrreversible {
		out.Confirmation = ""
	} else if out.Confirmation == "" {
		return core.PlanView{}, fmt.Errorf("lifecycle: %s returned no confirmation phrase", ref)
	}
	v := core.PlanView{
		PlanID: e.newID(), HostID: e.cfg.Base.HostID, Action: def.Spec.Name, Version: def.Spec.Version,
		InputHash: e.inputHash(def, canonical), Confirmation: out.Confirmation,
		Diff: nonNil(out.Diff), Guards: nonNil(out.Guards), ExpiresAt: e.cfg.Base.Clock.Now().Add(PlanTTL),
	}
	sealed, err := e.cfg.Base.Sealer.Seal(planInputAD(v.PlanID), canonical)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: seal plan input: %w", err)
	}
	diff, err := json.Marshal(v.Diff)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: encode diff: %w", err)
	}
	guards, err := json.Marshal(v.Guards)
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: encode guards: %w", err)
	}
	created := formatTime(e.cfg.Base.Clock.Now())
	err = e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_plans (id, host_id, action, version, actor, input_ct, input_hash, state_hash,
			 diff_json, guards_json, confirmation, created_at, expires_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			v.PlanID, v.HostID, v.Action, v.Version, p.Actor, sealed, v.InputHash, out.StateHash,
			string(diff), string(guards), v.Confirmation, created, formatTime(v.ExpiresAt))
		return err
	})
	if err != nil {
		return core.PlanView{}, fmt.Errorf("lifecycle: store plan: %w", err)
	}
	return v, nil
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// canonical validates raw and re-encodes it from the typed input, so equal
// inputs have equal bytes whatever their field order.
func (e *Engine) canonical(def core.ActionDef, raw json.RawMessage) (json.RawMessage, error) {
	if err := e.cfg.Validator.ValidateInput(def, raw); err != nil {
		return nil, err
	}
	v := reflect.New(def.InputType)
	if err := json.Unmarshal(raw, v.Interface()); err != nil {
		return nil, errcode.Wrap(errcode.InvalidInput, err, "input does not match the action's input type")
	}
	b, err := json.Marshal(v.Interface())
	if err != nil {
		return nil, fmt.Errorf("lifecycle: encode input: %w", err)
	}
	return b, nil
}

// inputHash is SHA-256 over the canonical input with its secret fields
// redacted, so the hash the API and audit expose reveals nothing about a
// secret (spec section 3).
func (e *Engine) inputHash(def core.ActionDef, canonical json.RawMessage) string {
	sum := sha256.Sum256(e.cfg.Redact(def.InputType, canonical))
	return hex.EncodeToString(sum[:])
}

// planInputAD is the associated data of a plan's sealed input (ADR 0005).
func planInputAD(planID string) string { return "core_plans.input_ct." + planID }

const changeColumns = `id, host_id, action, version, class, compensated_by, actor, state,
	revert_deadline, output_json, error_code, error_message, created_at, updated_at`

type scanner interface {
	Scan(dest ...any) error
}

func scanChange(s scanner) (core.ChangeView, error) {
	var v core.ChangeView
	var class, created, updated string
	var deadline, output, code, msg sql.NullString
	if err := s.Scan(&v.ID, &v.HostID, &v.Action, &v.Version, &class, &v.CompensatedBy, &v.Actor, &v.State,
		&deadline, &output, &code, &msg, &created, &updated); err != nil {
		return v, err
	}
	v.Class = core.Class(class)
	var err error
	if v.CreatedAt, err = parseTime(created); err != nil {
		return v, err
	}
	if v.UpdatedAt, err = parseTime(updated); err != nil {
		return v, err
	}
	if deadline.Valid {
		t, err := parseTime(deadline.String)
		if err != nil {
			return v, err
		}
		v.RevertDeadline = &t
	}
	if output.Valid {
		v.Output = json.RawMessage(output.String)
	}
	if code.Valid {
		v.Error = &core.ChangeError{Code: code.String, Message: msg.String}
	}
	return v, nil
}

// Change returns one change; secret output fields are redacted.
func (e *Engine) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	v, err := scanChange(e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID))
	if errors.Is(err, sql.ErrNoRows) {
		return v, errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
	}
	if err != nil {
		return v, fmt.Errorf("lifecycle: read change: %w", err)
	}
	return v, nil
}

// Changes lists changes newest first. Before is a change id: only older
// changes are listed. A Limit of 0 or less means 100; at most 500 are listed.
func (e *Engine) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	query := "SELECT " + changeColumns + " FROM core_changes WHERE 1 = 1"
	var args []any
	if f.State != "" {
		query += " AND state = ?"
		args = append(args, f.State)
	}
	if f.HostID != "" {
		query += " AND host_id = ?"
		args = append(args, f.HostID)
	}
	if f.Before != "" {
		query += " AND id < ?"
		args = append(args, f.Before)
	}
	limit := f.Limit
	if limit <= 0 {
		limit = defaultChangeLimit
	}
	query += " ORDER BY id DESC LIMIT ?"
	args = append(args, min(limit, maxChangeLimit))
	rows, err := e.cfg.Base.DB.Read().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("lifecycle: list changes: %w", err)
	}
	defer func() { _ = rows.Close() }()
	out := []core.ChangeView{}
	for rows.Next() {
		v, err := scanChange(rows)
		if err != nil {
			return nil, fmt.Errorf("lifecycle: list changes: %w", err)
		}
		out = append(out, v)
	}
	return out, rows.Err()
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 600 go test -race -count=3 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/engine.go internal/core/lifecycle/fixture_test.go internal/core/lifecycle/engine_test.go
git commit -m "feat(lifecycle): store plans and serve reads"
```

---

### Task 13: Sealer

**Files:**
- Create: `internal/core/secret/sealer.go`
- Test: `internal/core/secret/sealer_test.go`

**Interfaces:**
- Consumes: Contracts › `internal/core/secret` › Task 10 (`KeySize`, the package doc comment); `core.Sealer` from the foundation's `internal/core/contract.go`.
- Produces: Contracts › `internal/core/secret` › Task 13 (`Sealer`, `NewSealer`, `CSRFLabel`).

**Items:** 8

**Depends on:** Task 10

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The ciphertext layout (version byte, 8-byte key id, 24-byte nonce, sealed data) and the associated data, exactly the caller's `ad` string, are a stored format: every sealed column written from now on depends on them (ADR 0005). `Derive` is HKDF-SHA256 with no salt and the label as info; `TestDeriveIsHKDF` pins it from RFC 5869's definition. HKDF comes from the standard library's `crypto/hkdf` (Go 1.24 and later), which `golang.org/x/crypto/hkdf` itself wraps.

- [ ] **Step 1: Write the failing test**

Create `internal/core/secret/sealer_test.go`:

```go
package secret

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

var _ core.Sealer = (*Sealer)(nil)

func newTestSealer(t *testing.T, fill byte) *Sealer {
	t.Helper()
	s, err := NewSealer(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func TestSealerRoundTrip(t *testing.T) {
	s := newTestSealer(t, 1)
	const ad = "core_users.totp_secret_ct.01JABCDEF"
	plaintext := []byte("JBSWY3DPEHPK3PXP")
	ct, err := s.Seal(ad, plaintext)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	keyID := sha256.Sum256(bytes.Repeat([]byte{1}, KeySize))
	if ct[0] != 1 || !bytes.Equal(ct[1:9], keyID[:8]) {
		t.Errorf("header = %x, want version 1 and key id %x", ct[:9], keyID[:8])
	}
	if want := 1 + 8 + 24 + len(plaintext) + 16; len(ct) != want {
		t.Errorf("ciphertext length = %d, want %d", len(ct), want)
	}
	if bytes.Contains(ct, plaintext) {
		t.Error("ciphertext contains the plaintext")
	}
	got, err := s.Open(ad, ct)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Errorf("Open = %q, %v; want %q", got, err, plaintext)
	}
	again, err := s.Seal(ad, plaintext)
	if err != nil || bytes.Equal(again, ct) {
		t.Errorf("second Seal = %x, %v; want a different nonce", again, err)
	}
}

func TestSealerRefuses(t *testing.T) {
	s := newTestSealer(t, 1)
	const ad = "core_plans.input_ct.01JPLAN"
	ct, err := s.Seal(ad, []byte("input"))
	if err != nil {
		t.Fatal(err)
	}
	flip := func(i int) []byte {
		c := bytes.Clone(ct)
		c[i] ^= 1
		return c
	}
	cases := map[string]struct {
		sealer *Sealer
		ad     string
		ct     []byte
	}{
		"other associated data": {s, "core_plans.input_ct.01JOTHER", ct},
		"other key":             {newTestSealer(t, 2), ad, ct},
		"unknown version":       {s, ad, flip(0)},
		"changed key id":        {s, ad, flip(1)},
		"changed nonce":         {s, ad, flip(9)},
		"changed data":          {s, ad, flip(len(ct) - 1)},
		"short":                 {s, ad, ct[:1+8+24+15]},
		"empty":                 {s, ad, nil},
	}
	for name, c := range cases {
		if _, err := c.sealer.Open(c.ad, c.ct); err == nil {
			t.Errorf("%s: Open succeeded", name)
		}
	}
}

func TestNewSealerRefusesKeyLength(t *testing.T) {
	for _, n := range []int{0, 16, 31, 33, 64} {
		if _, err := NewSealer(make([]byte, n)); err == nil {
			t.Errorf("NewSealer accepted a %d-byte key", n)
		}
	}
}

func TestSealerCopiesKey(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := s.Seal("ad", []byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	clear(key)
	if _, err := s.Open("ad", ct); err != nil {
		t.Errorf("Open after the caller cleared its key: %v", err)
	}
}

func TestDerive(t *testing.T) {
	s := newTestSealer(t, 1)
	a := s.Derive(CSRFLabel)
	if len(a) != 32 {
		t.Fatalf("subkey length = %d, want 32", len(a))
	}
	if !bytes.Equal(a, s.Derive(CSRFLabel)) {
		t.Error("Derive is not deterministic")
	}
	if bytes.Equal(a, s.Derive("darkpanel other v1")) {
		t.Error("two labels derived the same subkey")
	}
	if bytes.Equal(a, newTestSealer(t, 2).Derive(CSRFLabel)) {
		t.Error("two keys derived the same subkey")
	}
	if bytes.Equal(a, bytes.Repeat([]byte{1}, KeySize)) {
		t.Error("Derive returned the role key")
	}
}

// TestDeriveIsHKDF pins the construction of spec section 5, HKDF-SHA256 with
// no salt and the label as info, computed here from RFC 5869's definition.
func TestDeriveIsHKDF(t *testing.T) {
	key := bytes.Repeat([]byte{1}, KeySize)
	extract := hmac.New(sha256.New, make([]byte, sha256.Size))
	extract.Write(key)
	expand := hmac.New(sha256.New, extract.Sum(nil))
	expand.Write([]byte(CSRFLabel))
	expand.Write([]byte{1})
	if got, want := newTestSealer(t, 1).Derive(CSRFLabel), expand.Sum(nil); !bytes.Equal(got, want) {
		t.Errorf("Derive = %x, want %x", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/secret/`
Expected: FAIL — the build reports `undefined: Sealer` and `undefined: NewSealer`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/secret/sealer.go`:

```go
package secret

import (
	"bytes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"

	"golang.org/x/crypto/chacha20poly1305"
)

// CSRFLabel is the Derive label of the CSRF token key (spec section 9).
const CSRFLabel = "darkpanel csrf v1"

const (
	sealVersion = 1
	keyIDSize   = 8
	headerSize  = 1 + keyIDSize
)

// Sealer encrypts secrets at rest with XChaCha20-Poly1305 (ADR 0005). A
// ciphertext is the version byte 1, an 8-byte key id (the first 8 bytes of
// SHA-256 of the key), a random 24-byte nonce and the sealed data. The
// associated data binds it to its place, "<table>.<column>.<row id>".
type Sealer struct {
	key   []byte
	keyID [keyIDSize]byte
	aead  cipher.AEAD
}

// NewSealer returns a sealer for a KeySize-byte role key; it keeps its own
// copy of the key.
func NewSealer(key []byte) (*Sealer, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("secret: key is %d bytes, want %d", len(key), KeySize)
	}
	k := bytes.Clone(key)
	aead, err := chacha20poly1305.NewX(k)
	if err != nil {
		return nil, fmt.Errorf("secret: sealer: %w", err)
	}
	s := &Sealer{key: k, aead: aead}
	sum := sha256.Sum256(k)
	copy(s.keyID[:], sum[:keyIDSize])
	return s, nil
}

// Seal encrypts plaintext bound to the associated data ad.
func (s *Sealer) Seal(ad string, plaintext []byte) ([]byte, error) {
	out := make([]byte, headerSize+chacha20poly1305.NonceSizeX, headerSize+chacha20poly1305.NonceSizeX+len(plaintext)+s.aead.Overhead())
	out[0] = sealVersion
	copy(out[1:headerSize], s.keyID[:])
	nonce := out[headerSize:]
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secret: nonce: %w", err)
	}
	return s.aead.Seal(out, nonce, plaintext, []byte(ad)), nil
}

// Open decrypts a ciphertext that Seal produced with the same key and ad. It
// refuses an unknown version, another key's id and any modified byte.
func (s *Sealer) Open(ad string, ciphertext []byte) ([]byte, error) {
	if len(ciphertext) < headerSize+chacha20poly1305.NonceSizeX+s.aead.Overhead() {
		return nil, errors.New("secret: ciphertext too short")
	}
	if ciphertext[0] != sealVersion {
		return nil, fmt.Errorf("secret: ciphertext version %d, want %d", ciphertext[0], sealVersion)
	}
	if !bytes.Equal(ciphertext[1:headerSize], s.keyID[:]) {
		return nil, errors.New("secret: ciphertext was sealed with another key")
	}
	nonce := ciphertext[headerSize : headerSize+chacha20poly1305.NonceSizeX]
	plaintext, err := s.aead.Open(nil, nonce, ciphertext[headerSize+chacha20poly1305.NonceSizeX:], []byte(ad))
	if err != nil {
		return nil, fmt.Errorf("secret: open: %w", err)
	}
	return plaintext, nil
}

// Derive returns a 32-byte subkey for label: HKDF-SHA256 over the role key
// with no salt and the label as info. Each use takes its own label, such as
// CSRFLabel, so no subkey equals another or the role key.
func (s *Sealer) Derive(label string) []byte {
	sub, err := hkdf.Key(sha256.New, s.key, nil, label, 32)
	if err != nil {
		// hkdf.Key fails only for an output longer than 255 hash lengths or
		// under FIPS 140 enforcement; neither applies to 32 bytes of SHA-256.
		panic(fmt.Sprintf("secret: derive %q: %v", label, err))
	}
	return sub
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -v ./internal/core/secret/
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/secret/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `--- ` line of the secret package reads `--- PASS` (`TestLoadOrCreateKeyRefusesForeignOwner` reads `--- SKIP` when not run as root); the race run prints `ok`; vet prints nothing; every package line of `go test ./...` starts with `ok` or `?`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/secret/sealer.go internal/core/secret/sealer_test.go
git commit -m "feat(secret): seal secrets with XChaCha20-Poly1305"
```

---

### Task 14: Apply

**Files:**
- Create: `internal/core/lifecycle/apply.go`
- Modify: `internal/core/lifecycle/engine.go` (the `Engine` struct gains `applyMu`)
- Test: `internal/core/lifecycle/apply_test.go`

**Interfaces:**
- Consumes: Task 12 (`Engine`, `Config`, `newID`, `env`, `action`, `planInputAD`, `changeColumns`, `scanChange`, the fixture and its helpers) and Task 6 (`formatTime`, `parseTime`, `nullJSON`, phase and result constants).
- Produces: Contracts › `internal/core/lifecycle` › Task 14 (`Apply`) and the confirm-timeout constants `DefaultConfirmTimeout`, `MinConfirmTimeout`, `MaxConfirmTimeout`; package-internal names later tasks use: `transition` (type and method), `runRevert`, `revertFailed`, `changeError`, `snapshotAD`, `intent`; test helpers in `apply_test.go`: `bob`, `(*fixture).apply`, `(*fixture).phases`, `(*fixture).count`, `(*fixture).planInputCleared`.

**Items:** 6

**Depends on:** Task 12

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

This task implements ADR 0004's apply ordering for every mutating class: the revertible class up to `pending` (snapshot, revert deadline, revert after a failed apply or verify). Confirm, revert and the revert timer are Task 16. Keep the order in `begin` (plan principal, existing change, expiry, confirmation, step 1 recheck, snapshot, then locks and intent in one committed transaction) and keep every handler call outside a database transaction: `store.DB.Write` is not re-entrant, and handlers may use `env.DB`.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/apply_test.go`:

```go
package lifecycle

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var bob = core.Principal{Actor: "user:bob", UserID: "bob", Scope: core.ScopeOperate, Source: "test"}

func (f *fixture) apply(p core.Principal, planID string, opts core.ApplyOptions) core.ChangeView {
	f.t.Helper()
	v, err := f.eng.Apply(context.Background(), p, planID, opts)
	if err != nil {
		f.t.Fatalf("Apply: %v", err)
	}
	return v
}

// phases lists the journal as "<phase>/<result>[/<error code>]" lines.
func (f *fixture) phases() []string {
	f.t.Helper()
	entries, err := f.journal.Entries(context.Background(), 0, 100)
	if err != nil {
		f.t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		line := e.Phase + "/" + e.Result
		if e.ErrorCode != "" {
			line += "/" + e.ErrorCode
		}
		out = append(out, line)
	}
	return out
}

func (f *fixture) count(query string, args ...any) int {
	f.t.Helper()
	var n int
	if err := f.db.Read().QueryRow(query, args...).Scan(&n); err != nil {
		f.t.Fatal(err)
	}
	return n
}

// planInputCleared reports whether the plan's sealed input is gone.
func (f *fixture) planInputCleared(planID string) bool {
	f.t.Helper()
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT input_ct FROM core_plans WHERE id = ?", planID).Scan(&ct); err != nil {
		f.t.Fatal(err)
	}
	return ct == nil
}

func TestApplyCompensableCompletes(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1","secret":"s"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})

	if v.State != core.StateCompleted || v.Class != core.ClassCompensable || v.CompensatedBy != refRemove.Name ||
		v.HostID != testHost || v.Actor != alice.Actor || v.Action != refAdd.Name || v.Version != 1 ||
		v.Error != nil || v.RevertDeadline != nil {
		t.Errorf("change = %+v", v)
	}
	if want := `{"previous":"","token":"tok-a"}`; string(v.Output) != want {
		t.Errorf("apply output = %s, want %s", v.Output, want)
	}
	if f.host.items["a"] != "1" {
		t.Errorf("item a = %q, want 1", f.host.items["a"])
	}
	stored, err := f.eng.Change(ctx, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"previous":"","token":"[redacted]"}`; string(stored.Output) != want {
		t.Errorf("stored output = %s, want %s", stored.Output, want)
	}
	if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
		t.Errorf("locks = %d, want 0", n)
	}
	if !f.planInputCleared(pl.PlanID) {
		t.Error("the plan's sealed input was kept after the result")
	}
	entries, err := f.journal.Entries(ctx, 0, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("journal = %+v, want one entry", entries)
	}
	e := entries[0]
	if e.Phase != "apply" || e.Result != "ok" || e.Actor != alice.Actor || e.ChangeID != v.ID ||
		e.Action != refAdd.Name || e.Version != 1 || string(e.Input) != `{"name":"a","secret":"[redacted]","value":"1"}` {
		t.Errorf("journal entry = %+v", e)
	}

	again := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if again.ID != v.ID || string(again.Output) != `{"previous":"","token":"[redacted]"}` {
		t.Errorf("repeated apply = %+v, want the stored change", again)
	}
	if f.host.applies != 1 {
		t.Errorf("handler applied %d times, want 1", f.host.applies)
	}
}

func TestApplyIrreversibleNeedsTheConfirmation(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refRemove, `{"name":"a","value":"x"}`)
	for _, phrase := range []string{"", "b", "A"} {
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{Confirmation: phrase})
		wantCode(t, err, errcode.PlanConfirmationNeeded)
	}
	if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
		t.Fatalf("changes after refused confirmations = %d, want 0", n)
	}
	if v := f.apply(alice, pl.PlanID, core.ApplyOptions{Confirmation: "a"}); v.State != core.StateCompleted {
		t.Errorf("state = %s, want completed", v.State)
	}
}

func TestApplyRevertiblePends(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	if v.State != core.StatePending || v.Class != core.ClassRevertible {
		t.Fatalf("change = %+v, want a pending revertible change", v)
	}
	if want := f.clock.Now().Add(DefaultConfirmTimeout); v.RevertDeadline == nil || !v.RevertDeadline.Equal(want) {
		t.Errorf("revert deadline = %v, want %v", v.RevertDeadline, want)
	}
	var owner string
	if err := f.db.Read().QueryRow("SELECT owner FROM core_locks WHERE key = 'fix'").Scan(&owner); err != nil || owner != v.ID {
		t.Errorf("lock fix owner = %q, %v; want %s", owner, err, v.ID)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok"}) {
		t.Errorf("journal = %v", got)
	}
	var ct []byte
	if err := f.db.Read().QueryRow("SELECT snapshot_ct FROM core_changes WHERE id = ?", v.ID).Scan(&ct); err != nil || ct == nil {
		t.Fatalf("snapshot = %v, %v; want a sealed snapshot", ct, err)
	}
	if _, err := f.eng.cfg.Base.Sealer.Open("core_changes.snapshot_ct."+v.ID, ct); err != nil {
		t.Errorf("snapshot does not open under its associated data: %v", err)
	}
}

func TestApplyFailures(t *testing.T) {
	cases := []struct {
		name    string
		ref     core.ActionRef
		input   string
		opts    core.ApplyOptions
		state   string
		code    string
		journal []string
		item    string
	}{
		{"compensable apply fails", refAdd, `{"name":"a","value":"1","failApply":true}`, core.ApplyOptions{},
			core.StateFailed, "internal", []string{"apply/error/internal"}, "old"},
		{"irreversible verify fails", refRemove, `{"name":"a","value":"1","failVerify":true}`, core.ApplyOptions{Confirmation: "a"},
			core.StateFailed, "internal", []string{"apply/error/internal"}, "1"},
		{"revertible apply fails and reverts", refSet, `{"name":"a","value":"1","failApply":true}`, core.ApplyOptions{},
			core.StateReverted, "internal", []string{"apply/error/internal"}, "old"},
		{"revertible verify fails and reverts", refSet, `{"name":"a","value":"1","failVerify":true}`, core.ApplyOptions{},
			core.StateReverted, "internal", []string{"apply/error/internal"}, "old"},
		{"revertible revert fails", refSet, `{"name":"a","value":"1","failVerify":true,"failRevert":true}`, core.ApplyOptions{},
			core.StateFailed, "change.revert_failed", []string{"revert/error/change.revert_failed"}, "1"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			f.host.items["a"] = "old"
			pl := f.plan(alice, c.ref, c.input)
			v := f.apply(alice, pl.PlanID, c.opts)
			if v.State != c.state || v.Error == nil || v.Error.Code != c.code || v.Error.Message == "" {
				t.Errorf("change = %+v (error %+v), want %s with %s", v, v.Error, c.state, c.code)
			}
			if got := f.phases(); !reflect.DeepEqual(got, c.journal) {
				t.Errorf("journal = %v, want %v", got, c.journal)
			}
			if f.host.items["a"] != c.item {
				t.Errorf("item a = %q, want %q", f.host.items["a"], c.item)
			}
			if n := f.count("SELECT count(*) FROM core_locks"); n != 0 {
				t.Errorf("locks = %d, want 0", n)
			}
			if !f.planInputCleared(pl.PlanID) {
				t.Error("the plan's sealed input was kept after the result")
			}
		})
	}
}

func TestApplyErrorMessageHidesInternalCauses(t *testing.T) {
	f := newFixture(t)
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1","failApply":true}`).PlanID, core.ApplyOptions{})
	if v.Error == nil || v.Error.Message != "Internal error" {
		t.Errorf("error = %+v, want the internal title without the cause", v.Error)
	}
}

func TestApplyRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture) // runs between plan and apply
		input string
		p     core.Principal
		opts  core.ApplyOptions
		code  errcode.Code
	}{
		{"principal mismatch", nil, `{"name":"a","value":"1"}`, bob, core.ApplyOptions{}, errcode.PlanPrincipalMismatch},
		{"expired", func(f *fixture) { f.clock.Advance(PlanTTL) }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanExpired},
		{"state changed", func(f *fixture) { f.host.items["b"] = "x" }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanStateChanged},
		{"guard passed then fails", func(f *fixture) { f.host.blocked = true }, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{}, errcode.PlanStateChanged},
		{"confirm timeout too short", nil, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{ConfirmTimeout: 9 * time.Second}, errcode.InvalidInput},
		{"confirm timeout too long", nil, `{"name":"a","value":"1"}`, alice, core.ApplyOptions{ConfirmTimeout: 15*time.Minute + time.Second}, errcode.InvalidInput},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			pl := f.plan(alice, refSet, c.input)
			if c.setup != nil {
				c.setup(f)
			}
			_, err := f.eng.Apply(context.Background(), c.p, pl.PlanID, c.opts)
			wantCode(t, err, c.code)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 || f.host.applies != 0 {
				t.Errorf("changes = %d, applies = %d; want none", n, f.host.applies)
			}
		})
	}
}

func TestApplyUnknownPlan(t *testing.T) {
	f := newFixture(t)
	_, err := f.eng.Apply(context.Background(), alice, "01NOPLAN", core.ApplyOptions{})
	wantCode(t, err, errcode.PlanNotFound)
}

func TestApplyGuardFailedAtPlan(t *testing.T) {
	f := newFixture(t)
	f.host.blocked = true
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.PlanGuardFailed)
}

func TestApplyConfirmTimeoutBounds(t *testing.T) {
	for _, d := range []time.Duration{MinConfirmTimeout, MaxConfirmTimeout} {
		f := newFixture(t)
		v := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{ConfirmTimeout: d})
		if want := f.clock.Now().Add(d); v.RevertDeadline == nil || !v.RevertDeadline.Equal(want) {
			t.Errorf("timeout %v: deadline %v, want %v", d, v.RevertDeadline, want)
		}
	}
}

func TestApplyLockHeld(t *testing.T) {
	f := newFixture(t)
	first := f.apply(alice, f.plan(alice, refSet, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	pl := f.plan(alice, refAdd, `{"name":"b","value":"2"}`)
	_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.LockHeld)
	if n := f.count("SELECT count(*) FROM core_changes WHERE id != ?", first.ID); n != 0 {
		t.Errorf("changes besides the lock holder = %d, want 0", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", first.ID); n != 1 {
		t.Errorf("locks of the holder = %d, want 1", n)
	}
}

func TestApplySamePlanConcurrentlyActsOnce(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	ids := make([]string, 8)
	var wg sync.WaitGroup
	for i := range ids {
		wg.Go(func() {
			v, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
			if err != nil {
				t.Error(err)
			}
			ids[i] = v.ID
		})
	}
	wg.Wait()
	for _, id := range ids {
		if id != ids[0] {
			t.Fatalf("change ids = %v, want one change", ids)
		}
	}
	if f.host.applies != 1 {
		t.Errorf("handler applied %d times, want 1", f.host.applies)
	}
}

func TestChangesFilters(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	f.apply(alice, f.plan(alice, refAdd, `{"name":"b","value":"1"}`).PlanID, core.ApplyOptions{})
	f.apply(alice, f.plan(alice, refSet, `{"name":"c","value":"1"}`).PlanID, core.ApplyOptions{})

	all, err := f.eng.Changes(ctx, core.ChangeFilter{})
	if err != nil || len(all) != 3 {
		t.Fatalf("Changes = %d, %v; want 3", len(all), err)
	}
	if all[0].ID <= all[1].ID || all[1].ID <= all[2].ID || all[0].State != core.StatePending {
		t.Errorf("changes not newest first: %s %s %s", all[0].ID, all[1].ID, all[2].ID)
	}
	filters := []struct {
		f    core.ChangeFilter
		want int
	}{
		{core.ChangeFilter{State: core.StatePending}, 1},
		{core.ChangeFilter{State: core.StateCompleted}, 2},
		{core.ChangeFilter{Before: all[0].ID}, 2},
		{core.ChangeFilter{Limit: 1}, 1},
		{core.ChangeFilter{HostID: testHost}, 3},
		{core.ChangeFilter{HostID: "other"}, 0},
	}
	for _, c := range filters {
		got, err := f.eng.Changes(ctx, c.f)
		if err != nil || len(got) != c.want {
			t.Errorf("Changes(%+v) = %d, %v; want %d", c.f, len(got), err, c.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/`
Expected: FAIL — the build reports `f.eng.Apply undefined (type *Engine has no field or method Apply)`.

- [ ] **Step 3: Write the implementation**

In `internal/core/lifecycle/engine.go`, replace this block of the `Engine` struct:

```go
	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy

	timerMu sync.Mutex
```

with:

```go
	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy

	// applyMu is held from apply's checks until its intent is persisted, so no
	// other apply takes a lock or the same plan in between.
	applyMu sync.Mutex

	timerMu sync.Mutex
```

Create `internal/core/lifecycle/apply.go`:

```go
package lifecycle

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Confirm timeouts of revertible changes (spec section 3).
const (
	DefaultConfirmTimeout = 60 * time.Second
	MinConfirmTimeout     = 10 * time.Second
	MaxConfirmTimeout     = 15 * time.Minute
)

// snapshotAD is the associated data of a change's sealed snapshot (ADR 0005).
func snapshotAD(changeID string) string { return "core_changes.snapshot_ct." + changeID }

func revertFailed() *core.ChangeError {
	return &core.ChangeError{
		Code:    string(errcode.ChangeRevertFailed),
		Message: "the revert failed; the host may still be in the applied state",
	}
}

type planRecord struct {
	id, action, actor, stateHash, confirmation string
	version                                    int
	inputCT                                    []byte
	guards                                     []core.Guard
	expiresAt                                  time.Time
}

// intent is a change whose intent is persisted; its mutation comes next.
type intent struct {
	id       string
	def      core.ActionDef
	input    json.RawMessage
	deadline time.Time // revertible only
}

// Apply executes a stored plan with ADR 0004's ordering. A plan that already
// has a change returns that change's current view whatever its state; it never
// acts twice. The returned Output is the handler's output as is; the stored
// change, every later view and the journal carry it redacted.
func (e *Engine) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	e.applyMu.Lock()
	it, existing, err := e.begin(ctx, p, planID, opts)
	e.applyMu.Unlock()
	switch {
	case err != nil:
		return core.ChangeView{}, err
	case existing != nil:
		return *existing, nil
	}
	// The intent is persisted: a cancelled request must not stop the change
	// half way, so the rest runs until its result is recorded.
	return e.run(context.WithoutCancel(ctx), p, it)
}

// begin runs apply's checks and persists the intent (ADR 0004 steps 1 and 2).
// It runs under applyMu.
func (e *Engine) begin(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (*intent, *core.ChangeView, error) {
	db := e.cfg.Base.DB
	pl, err := e.loadPlan(ctx, planID)
	if err != nil {
		return nil, nil, err
	}
	if p.Actor != pl.actor {
		return nil, nil, errcode.New(errcode.PlanPrincipalMismatch, "the plan was made by another principal")
	}
	existing, err := scanChange(db.Read().QueryRowContext(ctx,
		"SELECT "+changeColumns+" FROM core_changes WHERE plan_id = ?", planID))
	switch {
	case err == nil:
		return nil, &existing, nil
	case !errors.Is(err, sql.ErrNoRows):
		return nil, nil, fmt.Errorf("lifecycle: read change of plan %s: %w", planID, err)
	}
	now := e.cfg.Base.Clock.Now()
	if !now.Before(pl.expiresAt) {
		return nil, nil, errcode.New(errcode.PlanExpired, "the plan expired; plan again")
	}
	def, err := e.action(core.ActionRef{Name: pl.action, Version: pl.version})
	if err != nil {
		return nil, nil, err
	}
	timeout, err := confirmTimeout(def.Spec.Class, opts.ConfirmTimeout)
	if err != nil {
		return nil, nil, err
	}
	if def.Spec.Class == core.ClassIrreversible && opts.Confirmation != pl.confirmation {
		return nil, nil, errcode.New(errcode.PlanConfirmationNeeded,
			fmt.Sprintf("type the confirmation phrase %q to apply", pl.confirmation))
	}
	input, err := e.cfg.Base.Sealer.Open(planInputAD(pl.id), pl.inputCT)
	if err != nil {
		return nil, nil, fmt.Errorf("lifecycle: open plan input: %w", err)
	}
	env := e.env(p)
	if err := recheck(ctx, env, def, pl, input); err != nil {
		return nil, nil, err
	}

	it := &intent{id: e.newID(), def: def, input: input}
	var snapshot []byte
	var deadline any
	if def.Spec.Class == core.ClassRevertible {
		raw, err := def.Handler.Snapshot(ctx, env, input)
		if err != nil {
			return nil, nil, err
		}
		if snapshot, err = e.cfg.Base.Sealer.Seal(snapshotAD(it.id), raw); err != nil {
			return nil, nil, fmt.Errorf("lifecycle: seal snapshot: %w", err)
		}
		it.deadline = now.Add(timeout)
		deadline = formatTime(it.deadline)
	}
	created := formatTime(now)
	err = db.Write(ctx, func(tx *sql.Tx) error {
		if err := takeLocks(ctx, tx, it.id, def.Spec.LockKeys); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
			 input_json, snapshot_ct, revert_deadline, created_at, updated_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			it.id, pl.id, e.cfg.Base.HostID, def.Spec.Name, def.Spec.Version, string(def.Spec.Class),
			def.Spec.CompensatedBy, p.Actor, core.StateApplying, string(e.cfg.Redact(def.InputType, input)),
			snapshot, deadline, created, created)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return nil, nil, err
	case err != nil:
		return nil, nil, fmt.Errorf("lifecycle: persist intent: %w", err)
	}
	return it, nil, nil
}

func (e *Engine) loadPlan(ctx context.Context, planID string) (planRecord, error) {
	pl := planRecord{id: planID}
	var guards, expires string
	err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		`SELECT action, version, actor, input_ct, state_hash, guards_json, confirmation, expires_at
		 FROM core_plans WHERE id = ?`, planID).
		Scan(&pl.action, &pl.version, &pl.actor, &pl.inputCT, &pl.stateHash, &guards, &pl.confirmation, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return pl, errcode.New(errcode.PlanNotFound, fmt.Sprintf("no plan %s", planID))
	}
	if err != nil {
		return pl, fmt.Errorf("lifecycle: read plan: %w", err)
	}
	if err := json.Unmarshal([]byte(guards), &pl.guards); err != nil {
		return pl, fmt.Errorf("lifecycle: read plan guards: %w", err)
	}
	pl.expiresAt, err = parseTime(expires)
	return pl, err
}

func confirmTimeout(class core.Class, d time.Duration) (time.Duration, error) {
	switch {
	case class != core.ClassRevertible:
		return 0, nil
	case d == 0:
		return DefaultConfirmTimeout, nil
	case d < MinConfirmTimeout || d > MaxConfirmTimeout:
		return 0, &errcode.Error{
			Code: errcode.InvalidInput, Detail: "the confirm timeout must be between 10 seconds and 15 minutes",
			Fields: []errcode.FieldError{{Path: "/confirmTimeoutSeconds", Message: "must be between 10 and 900"}},
		}
	}
	return d, nil
}

// recheck is apply's step 1: the handler plans again from the stored input,
// and the apply is refused when the state hash changed or a guard that passed
// now fails (plan.state_changed), or a guard failed (plan.guard_failed).
func recheck(ctx context.Context, env core.Env, def core.ActionDef, pl planRecord, input json.RawMessage) error {
	out, err := def.Handler.Plan(ctx, env, input)
	if err != nil {
		return err
	}
	if out.StateHash != pl.stateHash {
		return errcode.New(errcode.PlanStateChanged, "the state changed since the plan was made; plan again")
	}
	passed := map[string]bool{}
	for _, g := range pl.guards {
		passed[g.ID] = g.OK
	}
	for _, g := range out.Guards {
		switch {
		case g.OK:
		case passed[g.ID]:
			return errcode.New(errcode.PlanStateChanged,
				fmt.Sprintf("guard %s passed when the plan was made and fails now; plan again", g.ID))
		default:
			return errcode.New(errcode.PlanGuardFailed, fmt.Sprintf("guard %s failed: %s", g.ID, g.Message))
		}
	}
	return nil
}

// takeLocks gives every key to owner, or fails with lock.held when another
// owner has one. A key listed twice is taken once.
func takeLocks(ctx context.Context, tx *sql.Tx, owner string, keys []string) error {
	for _, key := range keys {
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO core_locks (key, owner) VALUES (?, ?) ON CONFLICT (key) DO NOTHING", key, owner); err != nil {
			return err
		}
		var held string
		if err := tx.QueryRowContext(ctx, "SELECT owner FROM core_locks WHERE key = ?", key).Scan(&held); err != nil {
			return err
		}
		if held != owner {
			return errcode.New(errcode.LockHeld, fmt.Sprintf("lock %s is held by change %s", key, held))
		}
	}
	return nil
}

// run mutates, verifies and records the result (ADR 0004 steps 3 and 4).
func (e *Engine) run(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	env := e.env(p)
	h := it.def.Handler
	out, err := h.Apply(ctx, env, it.input)
	if err == nil {
		err = h.Verify(ctx, env, it.input, out)
	}
	if err != nil {
		return e.failApply(ctx, p, it, err)
	}
	revertible := it.def.Spec.Class == core.ClassRevertible
	to := core.StateCompleted
	if revertible {
		to = core.StatePending
	}
	v, err := e.transition(ctx, it.id, transition{
		from: core.StateApplying, to: to, output: e.cfg.Redact(it.def.OutputType, out),
		release: !revertible, phase: phaseApply, result: resultOK, actor: p.Actor,
	})
	if err != nil {
		return v, err
	}
	v.Output = out
	return v, nil
}

// failApply records a failed mutation or verify; a revertible change is
// reverted from its snapshot first.
func (e *Engine) failApply(ctx context.Context, p core.Principal, it *intent, cause error) (core.ChangeView, error) {
	tr := transition{
		from: core.StateApplying, to: core.StateFailed, err: e.changeError(it.def.Ref(), cause),
		release: true, phase: phaseApply, result: resultError, actor: p.Actor,
	}
	if it.def.Spec.Class == core.ClassRevertible {
		if err := e.runRevert(ctx, p, it.id); err != nil {
			e.cfg.Base.Log.Error("lifecycle: revert after a failed apply", "change", it.id, "error", err)
			tr.err, tr.phase = revertFailed(), phaseRevert
		} else {
			tr.to = core.StateReverted
		}
	}
	return e.transition(ctx, it.id, tr)
}

// changeError is what a change's error shows: the code and the client-safe
// detail of an errcode error, and only the title of an internal one, whose
// cause is logged instead.
func (e *Engine) changeError(ref core.ActionRef, err error) *core.ChangeError {
	c := errcode.From(err)
	if c.Code == errcode.Internal {
		e.cfg.Base.Log.Error("lifecycle: action failed", "action", ref.String(), "error", err)
	}
	msg := c.Detail
	if msg == "" {
		msg = c.Code.Title()
	}
	return &core.ChangeError{Code: string(c.Code), Message: msg}
}

// runRevert runs the action's Revert with the change's stored snapshot.
func (e *Engine) runRevert(ctx context.Context, p core.Principal, changeID string) error {
	var action string
	var version int
	var ct []byte
	if err := e.cfg.Base.DB.Read().QueryRowContext(ctx,
		"SELECT action, version, snapshot_ct FROM core_changes WHERE id = ?", changeID).
		Scan(&action, &version, &ct); err != nil {
		return fmt.Errorf("lifecycle: read snapshot of %s: %w", changeID, err)
	}
	ref := core.ActionRef{Name: action, Version: version}
	def, ok := e.cfg.Registry.Action(ref)
	if !ok {
		return fmt.Errorf("lifecycle: action %s is not registered", ref)
	}
	snapshot, err := e.cfg.Base.Sealer.Open(snapshotAD(changeID), ct)
	if err != nil {
		return fmt.Errorf("lifecycle: open snapshot of %s: %w", changeID, err)
	}
	return def.Handler.Revert(ctx, e.env(p), snapshot)
}

// transition is one state change of a change.
type transition struct {
	from, to string
	output   json.RawMessage   // stored when set
	err      *core.ChangeError // stored when set
	release  bool              // release the change's locks
	phase    string            // journal phase; empty journals nothing
	result   string
	actor    string
}

// transition commits a state change together with its journal entry in one
// transaction (spec section 3). A change that is not in state tr.from answers
// change.not_pending, so of two racing transitions the first to commit wins.
func (e *Engine) transition(ctx context.Context, changeID string, tr transition) (core.ChangeView, error) {
	var v core.ChangeView
	err := e.cfg.Base.DB.Write(ctx, func(tx *sql.Tx) error {
		var state, input string
		err := tx.QueryRowContext(ctx, "SELECT state, input_json FROM core_changes WHERE id = ?", changeID).Scan(&state, &input)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return errcode.New(errcode.ChangeNotFound, fmt.Sprintf("no change %s", changeID))
		case err != nil:
			return err
		case state != tr.from:
			return errcode.New(errcode.ChangeNotPending, fmt.Sprintf("change %s is %s", changeID, state))
		}
		now := e.cfg.Base.Clock.Now()
		var code, msg any
		if tr.err != nil {
			code, msg = tr.err.Code, tr.err.Message
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE core_changes SET state = ?, output_json = coalesce(?, output_json),
			 error_code = coalesce(?, error_code), error_message = coalesce(?, error_message), updated_at = ?
			 WHERE id = ?`,
			tr.to, nullJSON(tr.output), code, msg, formatTime(now), changeID); err != nil {
			return err
		}
		if tr.release {
			if _, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", changeID); err != nil {
				return err
			}
		}
		if tr.to != core.StateApplying {
			// Once a plan has a result only its hash is kept (spec section 3).
			if _, err := tx.ExecContext(ctx,
				"UPDATE core_plans SET input_ct = NULL WHERE id = (SELECT plan_id FROM core_changes WHERE id = ?)",
				changeID); err != nil {
				return err
			}
		}
		if v, err = scanChange(tx.QueryRowContext(ctx,
			"SELECT "+changeColumns+" FROM core_changes WHERE id = ?", changeID)); err != nil {
			return err
		}
		if tr.phase == "" {
			return nil
		}
		entry := core.JournalEntry{
			At: now, Actor: tr.actor, Action: v.Action, Version: v.Version, Phase: tr.phase,
			ChangeID: changeID, Input: json.RawMessage(input), Result: tr.result,
		}
		if tr.err != nil {
			entry.ErrorCode = tr.err.Code
		}
		return e.cfg.Journal.Append(ctx, tx, entry)
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return v, err
	case err != nil:
		return v, fmt.Errorf("lifecycle: record change %s: %w", changeID, err)
	}
	return v, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 600 go test -race -count=3 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/apply.go internal/core/lifecycle/engine.go internal/core/lifecycle/apply_test.go
git commit -m "feat(lifecycle): apply plans with ADR 0004 order"
```

---

### Task 15: Redaction

**Files:**
- Create: `internal/core/secret/redact.go`
- Test: `internal/core/secret/redact_test.go`

**Interfaces:**
- Consumes: the package doc comment from Task 10; the schema shape of Contracts › `internal/core/schema` › Task 1 (the tests write that shape by hand, so this task does not import the schema package).
- Produces: Contracts › `internal/core/secret` › Task 15 (`Redacted`, `RedactJSON`).

**Items:** 8

**Depends on:** Task 10

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Redaction fails closed (spec section 5): an unresolvable `$ref`, a reference cycle, an object property or array element that no schema describes, a free-form (`true`) schema holding an object or array, and invalid JSON in either argument all redact rather than pass through. A property is described when `properties` or a matching `patternProperties` entry names it, or else when `additionalProperties` is a schema object; `additionalProperties: true` or absent does not describe it. Keep these rules: audit, journal and log entries depend on them never leaking a secret.

- [ ] **Step 1: Write the failing test**

Create `internal/core/secret/redact_test.go`:

```go
package secret

import (
	"encoding/json"
	"testing"
)

// inSchema has the shape internal/core/schema produces from
//
//	type Cred struct {
//		User     string `json:"user"`
//		Password string `json:"password" secret:"true"`
//	}
//	type In struct {
//		Name  string            `json:"name"`
//		Creds []Cred            `json:"creds,omitempty"`
//		Main  *Cred             `json:"main,omitempty"`
//		Tags  map[string]string `json:"tags,omitempty"`
//		Extra any               `json:"extra,omitempty"`
//	}
const inSchema = `{
	"$schema": "https://json-schema.org/draft/2020-12/schema",
	"$ref": "#/$defs/In",
	"$defs": {
		"Cred": {
			"type": "object",
			"properties": {
				"user": {"type": "string"},
				"password": {"type": "string", "x-secret": true, "writeOnly": true}
			},
			"additionalProperties": false,
			"required": ["user", "password"]
		},
		"In": {
			"type": "object",
			"properties": {
				"name": {"type": "string"},
				"creds": {"type": "array", "items": {"$ref": "#/$defs/Cred"}},
				"main": {"$ref": "#/$defs/Cred"},
				"tags": {"type": "object", "additionalProperties": {"type": "string"}},
				"extra": true
			},
			"additionalProperties": false,
			"required": ["name"]
		}
	}
}`

func TestRedactJSON(t *testing.T) {
	tests := []struct {
		name, raw, schema, want string
	}{
		{
			name:   "nested secrets through refs, arrays and pointers",
			raw:    `{"name":"web","creds":[{"user":"a","password":"p1"},{"user":"b","password":"p2"}],"main":{"user":"c","password":"p3"},"tags":{"env":"prod"}}`,
			schema: inSchema,
			want:   `{"creds":[{"password":"[redacted]","user":"a"},{"password":"[redacted]","user":"b"}],"main":{"password":"[redacted]","user":"c"},"name":"web","tags":{"env":"prod"}}`,
		},
		{
			name:   "a secret of any type is replaced whole",
			raw:    `{"main":{"user":"c","password":{"nested":["x"]}},"name":"web"}`,
			schema: inSchema,
			want:   `{"main":{"password":"[redacted]","user":"c"},"name":"web"}`,
		},
		{
			name:   "null and missing values",
			raw:    `{"name":"web","main":null}`,
			schema: inSchema,
			want:   `{"main":null,"name":"web"}`,
		},
		{
			name:   "an unknown property is redacted",
			raw:    `{"name":"web","token":"t0p"}`,
			schema: inSchema,
			want:   `{"name":"web","token":"[redacted]"}`,
		},
		{
			name:   "a free-form value keeps scalars and redacts what it contains",
			raw:    `{"name":"web","extra":{"apiKey":"k","list":[1]}}`,
			schema: inSchema,
			want:   `{"extra":{"apiKey":"[redacted]","list":"[redacted]"},"name":"web"}`,
		},
		{
			name:   "a free-form scalar is kept",
			raw:    `{"name":"web","extra":"plain"}`,
			schema: inSchema,
			want:   `{"extra":"plain","name":"web"}`,
		},
		{
			name:   "a secret root",
			raw:    `"hunter2"`,
			schema: `{"type": "string", "x-secret": true}`,
			want:   `"[redacted]"`,
		},
		{
			name:   "a secret field whose type is a referenced struct",
			raw:    `{"main":{"user":"c","password":"p3"}}`,
			schema: `{"type":"object","properties":{"main":{"$ref":"#/$defs/Cred","x-secret":true}},"$defs":{"Cred":{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string"}}}}}`,
			want:   `{"main":"[redacted]"}`,
		},
		{
			name:   "oneOf and anyOf branches",
			raw:    `{"a":{"user":"u","password":"p"},"b":{"user":"v","password":"q"},"c":"text"}`,
			schema: `{"type":"object","properties":{"a":{"oneOf":[{"type":"string"},{"$ref":"#/$defs/Cred"}]},"b":{"anyOf":[{"type":"null"},{"$ref":"#/$defs/Cred"}]},"c":{"oneOf":[{"type":"string"},{"$ref":"#/$defs/Cred"}]}},"$defs":{"Cred":{"type":"object","properties":{"user":{"type":"string"},"password":{"type":"string","x-secret":true}}}}}`,
			want:   `{"a":{"password":"[redacted]","user":"u"},"b":{"password":"[redacted]","user":"v"},"c":"text"}`,
		},
		{
			name:   "a branch that marks the value secret wins",
			raw:    `{"a":"s3cret"}`,
			schema: `{"type":"object","properties":{"a":{"anyOf":[{"type":"string"},{"type":"string","x-secret":true}]}}}`,
			want:   `{"a":"[redacted]"}`,
		},
		{
			name:   "an unresolvable reference redacts the value whole",
			raw:    `{"a":{"user":"u"},"b":1}`,
			schema: `{"type":"object","properties":{"a":{"$ref":"#/$defs/Missing"},"b":{"type":"integer"}}}`,
			want:   `{"a":"[redacted]","b":1}`,
		},
		{
			name:   "a reference cycle redacts instead of looping",
			raw:    `{"x":1}`,
			schema: `{"$ref":"#/$defs/A","$defs":{"A":{"$ref":"#/$defs/B"},"B":{"$ref":"#/$defs/A"}}}`,
			want:   `"[redacted]"`,
		},
		{
			name:   "a recursive type follows the data",
			raw:    `{"key":"k1","children":[{"key":"k2","children":[{"key":"k3","children":[]}]}]}`,
			schema: `{"$ref":"#/$defs/Node","$defs":{"Node":{"type":"object","properties":{"key":{"type":"string","x-secret":true},"children":{"type":"array","items":{"$ref":"#/$defs/Node"}}}}}}`,
			want:   `{"children":[{"children":[{"children":[],"key":"[redacted]"}],"key":"[redacted]"}],"key":"[redacted]"}`,
		},
		{
			name:   "prefixItems apply by position and an array without items redacts its elements",
			raw:    `{"pair":["user","pass","more"],"loose":["a"]}`,
			schema: `{"type":"object","properties":{"pair":{"type":"array","prefixItems":[{"type":"string"},{"type":"string","x-secret":true}]},"loose":{"type":"array"}}}`,
			want:   `{"loose":["[redacted]"],"pair":["user","[redacted]","[redacted]"]}`,
		},
		{
			name:   "patternProperties",
			raw:    `{"80":"web","x":"y"}`,
			schema: `{"type":"object","patternProperties":{"^[0-9]+$":{"type":"string"}},"additionalProperties":{"type":"string","x-secret":true}}`,
			want:   `{"80":"web","x":"[redacted]"}`,
		},
		{
			name:   "numbers keep their text and HTML is not escaped",
			raw:    `{"big":12345678901234567890,"dec":1.50,"html":"<a&b>"}`,
			schema: `{"type":"object","properties":{"big":{"type":"integer"},"dec":{"type":"number"},"html":{"type":"string"}}}`,
			want:   `{"big":12345678901234567890,"dec":1.50,"html":"<a&b>"}`,
		},
		{name: "invalid JSON", raw: `{"name":`, schema: inSchema, want: `"[redacted]"`},
		{name: "trailing data", raw: `{"name":"a"} {}`, schema: inSchema, want: `"[redacted]"`},
		{name: "empty input", raw: ``, schema: inSchema, want: `"[redacted]"`},
		{name: "invalid schema", raw: `{"name":"a"}`, schema: `{"type":`, want: `"[redacted]"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RedactJSON(json.RawMessage(tt.raw), json.RawMessage(tt.schema))
			if string(got) != tt.want {
				t.Errorf("RedactJSON =\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRedactJSONLeavesInputUnchanged(t *testing.T) {
	raw := json.RawMessage(`{"name":"web","main":{"user":"c","password":"p3"}}`)
	before := string(raw)
	_ = RedactJSON(raw, json.RawMessage(inSchema))
	if string(raw) != before {
		t.Errorf("input changed to %s", raw)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/secret/`
Expected: FAIL — the build reports `undefined: RedactJSON`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/secret/redact.go`:

```go
package secret

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"
)

// Redacted replaces every secret value in audit, journal, logs, diffs and
// errors.
const Redacted = "[redacted]"

var redactedJSON = json.RawMessage(`"` + Redacted + `"`)

// maxRefDepth bounds the $ref and combinator nesting followed for one value,
// so a reference cycle redacts instead of looping.
const maxRefDepth = 32

// RedactJSON returns raw with every value whose schema carries
// "x-secret": true replaced by "[redacted]". It follows $ref into $defs,
// object properties, patternProperties and additionalProperties, array items
// and prefixItems, and every oneOf, anyOf and allOf branch; a branch that marks
// a value secret wins. It fails closed: a value whose schema cannot be
// resolved, an object property or array element no schema describes, invalid
// JSON in raw or in schema, all come back as "[redacted]". The result is
// compact JSON; raw is not modified.
func RedactJSON(raw, schema json.RawMessage) json.RawMessage {
	value, err := decodeJSON(raw)
	if err != nil {
		return redactedJSON
	}
	root, err := decodeJSON(schema)
	if err != nil {
		return redactedJSON
	}
	r := redactor{root: root}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r.redact(value, []any{root})); err != nil {
		return redactedJSON
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n"))
}

// decodeJSON parses exactly one JSON value, keeping numbers as written.
func decodeJSON(raw []byte) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	if !errors.Is(dec.Decode(new(json.RawMessage)), io.EOF) {
		return nil, errors.New("trailing data after the JSON value")
	}
	return v, nil
}

type redactor struct {
	root any
}

// redact returns v redacted under every schema in schemas, which all apply to
// v at once (the branches of oneOf, anyOf and allOf are unioned).
func (r redactor) redact(v any, schemas []any) any {
	leaves, ok := r.expand(schemas)
	if !ok {
		return Redacted
	}
	for _, s := range leaves {
		if s["x-secret"] == true {
			return Redacted
		}
	}
	switch val := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(val))
		for key, child := range val {
			out[key] = r.redactChild(child, propertySchemas(leaves, key))
		}
		return out
	case []any:
		out := make([]any, len(val))
		for i, child := range val {
			out[i] = r.redactChild(child, itemSchemas(leaves, i))
		}
		return out
	default:
		return v
	}
}

// redactChild redacts a property or element; one that no schema describes is
// redacted whole.
func (r redactor) redactChild(v any, schemas []any) any {
	if len(schemas) == 0 {
		return Redacted
	}
	return r.redact(v, schemas)
}

// expand resolves $ref and flattens oneOf, anyOf and allOf into the object
// schemas that apply to a value. Boolean schemas describe no structure and
// are dropped. ok is false when a reference cannot be resolved or the nesting
// exceeds maxRefDepth.
func (r redactor) expand(schemas []any) (leaves []map[string]any, ok bool) {
	var walk func(s any, depth int) bool
	walk = func(s any, depth int) bool {
		if depth > maxRefDepth {
			return false
		}
		obj, isObj := s.(map[string]any)
		if !isObj {
			_, isBool := s.(bool)
			return isBool
		}
		leaves = append(leaves, obj)
		if ref, has := obj["$ref"]; has {
			target, found := r.resolve(ref)
			if !found || !walk(target, depth+1) {
				return false
			}
		}
		for _, kw := range []string{"oneOf", "anyOf", "allOf"} {
			branches, has := obj[kw]
			if !has {
				continue
			}
			list, isList := branches.([]any)
			if !isList {
				return false
			}
			for _, b := range list {
				if !walk(b, depth+1) {
					return false
				}
			}
		}
		return true
	}
	for _, s := range schemas {
		if !walk(s, 0) {
			return nil, false
		}
	}
	return leaves, true
}

// resolve finds a "#/$defs/<name>" reference in the root schema.
func (r redactor) resolve(ref any) (any, bool) {
	s, ok := ref.(string)
	name, cut := strings.CutPrefix(s, "#/$defs/")
	if !ok || !cut {
		return nil, false
	}
	root, ok := r.root.(map[string]any)
	if !ok {
		return nil, false
	}
	defs, ok := root["$defs"].(map[string]any)
	if !ok {
		return nil, false
	}
	target, ok := defs[name]
	return target, ok
}

// propertySchemas returns the schemas of an object's property key: its
// properties entry and matching patternProperties, or else
// additionalProperties when that is a schema object (JSON Schema 2020-12).
func propertySchemas(leaves []map[string]any, key string) []any {
	var out []any
	for _, s := range leaves {
		matched := false
		if props, ok := s["properties"].(map[string]any); ok {
			if p, ok := props[key]; ok {
				out = append(out, p)
				matched = true
			}
		}
		if patterns, ok := s["patternProperties"].(map[string]any); ok {
			for pattern, p := range patterns {
				if re, err := regexp.Compile(pattern); err == nil && re.MatchString(key) {
					out = append(out, p)
					matched = true
				}
			}
		}
		if extra, ok := s["additionalProperties"].(map[string]any); ok && !matched {
			out = append(out, extra)
		}
	}
	return out
}

// itemSchemas returns the schemas of array element i: its prefixItems entry,
// or else items when that is a schema object.
func itemSchemas(leaves []map[string]any, i int) []any {
	var out []any
	for _, s := range leaves {
		if prefix, ok := s["prefixItems"].([]any); ok && i < len(prefix) {
			out = append(out, prefix[i])
			continue
		}
		if items, ok := s["items"].(map[string]any); ok {
			out = append(out, items)
		}
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -v ./internal/core/secret/
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/core/secret/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `--- ` line of the secret package reads `--- PASS` (`TestLoadOrCreateKeyRefusesForeignOwner` reads `--- SKIP` when not run as root); the race run prints `ok`; vet prints nothing; every package line of `go test ./...` starts with `ok` or `?`; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/secret/redact.go internal/core/secret/redact_test.go
git commit -m "feat(secret): redact secret fields by schema"
```

---

### Task 16: Revertible changes

**Files:**
- Create: `internal/core/lifecycle/revert.go`
- Modify: `internal/core/lifecycle/apply.go` (the `intent` struct, the `intent` built in `begin`, the end of `run`)
- Test: `internal/core/lifecycle/revert_test.go`

**Interfaces:**
- Consumes: Task 14 (`transition`, `runRevert`, `revertFailed`, `intent`, `run`, the confirm-timeout constants, the `apply_test.go` helpers) and Task 12 (`Engine.timerMu`, `Engine.timers`, `Engine.closed`, the fixture).
- Produces: Contracts › `internal/core/lifecycle` › Task 16 (`Confirm`, `Revert`, `ActorRevertTimer`, `ApplyOptions.ConfirmNow` handling, revert timers); package-internal `revertPending`, `armTimer`, `stopTimer`; test helpers in `revert_test.go`: `(*fixture).pending`, `(*fixture).state`.

**Items:** 6

**Depends on:** Task 14

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Confirm and revert are arbitrated by `transition`'s state check inside one immediate transaction: a revert first claims the change by moving it from `pending` back to `applying`, so a racing confirm gets `change.not_pending`, and a crash during the revert is reverted again from the snapshot by recovery (Task 20). Keep that claim; do not replace it with an in-memory flag.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/revert_test.go`:

```go
package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// pending applies fix.item.set as alice and returns the pending change.
func (f *fixture) pending(input string) core.ChangeView {
	f.t.Helper()
	v := f.apply(alice, f.plan(alice, refSet, input).PlanID, core.ApplyOptions{})
	if v.State != core.StatePending {
		f.t.Fatalf("state = %s, want pending", v.State)
	}
	return v
}

func (f *fixture) state(changeID string) string {
	f.t.Helper()
	v, err := f.eng.Change(context.Background(), changeID)
	if err != nil {
		f.t.Fatal(err)
	}
	return v.State
}

func TestConfirmKeepsTheChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.items["a"] = "old"
	v := f.pending(`{"name":"a","value":"1"}`)
	if f.clock.Pending() != 1 {
		t.Fatalf("armed timers = %d, want 1", f.clock.Pending())
	}
	c, err := f.eng.Confirm(ctx, bob, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateConfirmed || c.Actor != alice.Actor {
		t.Errorf("confirmed change = %+v, want confirmed with the applier as actor", c)
	}
	if f.clock.Pending() != 0 || f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Errorf("timers = %d, locks = %d; want none", f.clock.Pending(), f.count("SELECT count(*) FROM core_locks"))
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "confirm/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if entries, _ := f.journal.Entries(ctx, 0, 10); entries[1].Actor != bob.Actor {
		t.Errorf("confirm actor = %s, want %s", entries[1].Actor, bob.Actor)
	}
	f.clock.Advance(2 * DefaultConfirmTimeout)
	if f.host.items["a"] != "1" {
		t.Errorf("item a = %q after the deadline, want the confirmed 1", f.host.items["a"])
	}
	_, err = f.eng.Confirm(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Revert(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
}

func TestRevertRestoresTheSnapshot(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.host.items["a"] = "old"
	v := f.pending(`{"name":"a","value":"1"}`)
	r, err := f.eng.Revert(ctx, bob, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != core.StateReverted || r.Error != nil || f.host.items["a"] != "old" {
		t.Errorf("reverted change = %+v, item a = %q; want reverted to old", r, f.host.items["a"])
	}
	if f.clock.Pending() != 0 || f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("the revert left a timer or a lock")
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "revert/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if entries, _ := f.journal.Entries(ctx, 0, 10); entries[1].Actor != bob.Actor {
		t.Errorf("revert actor = %s, want %s", entries[1].Actor, bob.Actor)
	}
}

func TestRevertFailureFailsTheChange(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	r, err := f.eng.Revert(context.Background(), alice, v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if r.State != core.StateFailed || r.Error == nil || r.Error.Code != string(errcode.ChangeRevertFailed) {
		t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", r, r.Error)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "revert/error/change.revert_failed"}) {
		t.Errorf("journal = %v", got)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("a failed revert kept its locks")
	}
}

func TestTimerRevertsAtTheDeadline(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.pending(`{"name":"a","value":"1"}`)
	f.clock.Advance(DefaultConfirmTimeout - time.Second)
	if s := f.state(v.ID); s != core.StatePending {
		t.Fatalf("state before the deadline = %s, want pending", s)
	}
	f.clock.Advance(time.Second)
	if s := f.state(v.ID); s != core.StateReverted {
		t.Fatalf("state at the deadline = %s, want reverted", s)
	}
	if _, ok := f.host.items["a"]; ok {
		t.Error("the timer revert left item a in place")
	}
	entries, err := f.journal.Entries(ctx, 0, 10)
	if err != nil || len(entries) != 2 || entries[1].Phase != "revert" || entries[1].Actor != ActorRevertTimer {
		t.Errorf("journal = %+v, %v; want a revert by %s", entries, err, ActorRevertTimer)
	}
}

func TestConfirmNowConfirmsInTheApply(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{ConfirmNow: true})
	if v.State != core.StateConfirmed || string(v.Output) != `{"previous":"","token":"tok-a"}` {
		t.Errorf("change = %+v, want confirmed with the raw output", v)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "confirm/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("armed timers = %d, want 0", f.clock.Pending())
	}
}

func TestConfirmAndRevertRace(t *testing.T) {
	for range 20 {
		f := newFixture(t)
		ctx := context.Background()
		v := f.pending(`{"name":"a","value":"1"}`)
		var confirmErr, revertErr error
		var wg sync.WaitGroup
		wg.Go(func() { _, confirmErr = f.eng.Confirm(ctx, alice, v.ID) })
		wg.Go(func() { _, revertErr = f.eng.Revert(ctx, bob, v.ID) })
		wg.Wait()

		var coded *errcode.Error
		state := f.state(v.ID)
		switch {
		case confirmErr == nil && errors.As(revertErr, &coded) && coded.Code == errcode.ChangeNotPending:
			if state != core.StateConfirmed || f.host.items["a"] != "1" {
				t.Fatalf("confirm won but state = %s, item = %q", state, f.host.items["a"])
			}
		case revertErr == nil && errors.As(confirmErr, &coded) && coded.Code == errcode.ChangeNotPending:
			if _, ok := f.host.items["a"]; state != core.StateReverted || ok {
				t.Fatalf("revert won but state = %s, item present = %v", state, ok)
			}
		default:
			t.Fatalf("confirm = %v, revert = %v; want exactly one change.not_pending", confirmErr, revertErr)
		}
		if f.count("SELECT count(*) FROM core_locks") != 0 || len(f.phases()) != 2 {
			t.Fatalf("locks = %d, journal = %v; want no locks and two entries", f.count("SELECT count(*) FROM core_locks"), f.phases())
		}
	}
}

func TestConfirmAndRevertOnlyPending(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	_, err := f.eng.Confirm(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Revert(ctx, alice, v.ID)
	wantCode(t, err, errcode.ChangeNotPending)
	_, err = f.eng.Confirm(ctx, alice, "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
	_, err = f.eng.Revert(ctx, alice, "01NOPE")
	wantCode(t, err, errcode.ChangeNotFound)
}

func TestCloseStopsTimers(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1"}`)
	f.eng.Close()
	if f.clock.Pending() != 0 {
		t.Fatalf("armed timers after Close = %d, want 0", f.clock.Pending())
	}
	f.clock.Advance(2 * DefaultConfirmTimeout)
	if s := f.state(v.ID); s != core.StatePending {
		t.Errorf("state = %s, want pending until recovery", s)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/`
Expected: FAIL — the build reports `f.eng.Revert undefined (type *Engine has no field or method Revert)`.

- [ ] **Step 3: Write the implementation**

In `internal/core/lifecycle/apply.go` make three replacements; each old block occurs exactly once.

1. Replace:

```go
// intent is a change whose intent is persisted; its mutation comes next.
type intent struct {
	id       string
	def      core.ActionDef
	input    json.RawMessage
	deadline time.Time // revertible only
}
```

with:

```go
// intent is a change whose intent is persisted; its mutation comes next.
type intent struct {
	id         string
	def        core.ActionDef
	input      json.RawMessage
	deadline   time.Time // revertible only
	confirmNow bool
}
```

2. Replace:

```go
	it := &intent{id: e.newID(), def: def, input: input}
```

with:

```go
	it := &intent{id: e.newID(), def: def, input: input, confirmNow: opts.ConfirmNow}
```

3. Replace:

```go
	if err != nil {
		return v, err
	}
	v.Output = out
	return v, nil
}
```

with:

```go
	if err != nil {
		return v, err
	}
	if revertible {
		e.armTimer(it.id, it.deadline.Sub(e.cfg.Base.Clock.Now()))
		if it.confirmNow {
			if v, err = e.Confirm(ctx, p, it.id); err != nil {
				return v, err
			}
		}
	}
	v.Output = out
	return v, nil
}
```

Create `internal/core/lifecycle/revert.go`:

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
		phase: phaseConfirm, result: resultOK, actor: p.Actor,
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
		phase: phaseRevert, result: resultOK, actor: p.Actor,
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
		delete(e.timers, changeID)
		e.timerMu.Unlock()
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

- [ ] **Step 4: Run the tests to verify they pass**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 600 go test -race -count=3 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

Then run the race tests harder: `GOTOOLCHAIN=local timeout 600 go test -race -count=20 -run 'Race|Concurrently' ./internal/core/lifecycle/`
Expected: `ok`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/revert.go internal/core/lifecycle/apply.go internal/core/lifecycle/revert_test.go
git commit -m "feat(lifecycle): confirm, revert and revert timers"
```

---

### Task 17: Exec runner

**Files:**
- Create: `internal/platform/exec.go`
- Test: `internal/platform/exec_test.go`

**Interfaces:**
- Consumes: `core.Runner`, `core.Cmd`, `core.CmdResult`, `errcode.ExecOutputTooLarge` (foundation); package `platform` from Task 2.
- Produces: Contracts › `internal/platform` › Task 17 (`Runner`, `NewRunner`, `ProductionAllowlist`); plan 4's agent runtime passes `NewRunner(ProductionAllowlist())` as `core.Env.Exec`.

**Items:** 7

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The runner is the only way a module runs a program (ADR 0009): exact allowlist match on the absolute path, an argument vector, the fixed three-variable environment and no shell. Do not inherit any environment variable, do not resolve names through `PATH`, and keep the 4 MiB cap on both streams. `TestExecHelperProcess` is a helper, not a test: `TestRunCapsOutput` starts the test binary itself to write more than 4 MiB to stderr, which no standard program does without a shell.

- [ ] **Step 1: Write the failing test**

Create `internal/platform/exec_test.go`:

```go
package platform

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

const helperArg = "darkpanel-exec-helper"

// TestExecHelperProcess is not a test: Run starts the test binary with
// helperArg so a program can write more than 4 MiB to stderr, which no
// standard program does without a shell.
func TestExecHelperProcess(*testing.T) {
	if os.Args[len(os.Args)-1] != helperArg {
		return
	}
	_, _ = os.Stderr.Write(bytes.Repeat([]byte("e"), maxOutput+1))
	os.Exit(0)
}

func TestRunRefusesUnlistedPrograms(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/printf"})
	for _, path := range []string{"/usr/bin/env", "printf", "/usr/bin/../bin/printf"} {
		if _, err := r.Run(context.Background(), core.Cmd{Path: path}); err == nil {
			t.Errorf("Run(%q) succeeded, want a refusal", path)
		}
	}
}

func TestNewRunnerPanicsOnRelativePath(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewRunner accepted a relative path")
		}
	}()
	NewRunner([]string{"printf"})
}

func TestProductionAllowlistIsEmpty(t *testing.T) {
	if got := ProductionAllowlist(); len(got) != 0 {
		t.Errorf("ProductionAllowlist() = %v, want empty in M1", got)
	}
}

func TestRunUsesFixedEnvironment(t *testing.T) {
	t.Setenv("DARKPANEL_LEAK", "1")
	res, err := NewRunner([]string{"/usr/bin/env"}).Run(context.Background(), core.Cmd{Path: "/usr/bin/env"})
	if err != nil {
		t.Fatal(err)
	}
	want := "PATH=/usr/sbin:/usr/bin:/sbin:/bin\nLANG=C.UTF-8\nLC_ALL=C.UTF-8\n"
	if string(res.Stdout) != want {
		t.Errorf("environment = %q, want %q", res.Stdout, want)
	}
}

func TestRunPassesArgumentsAndStdin(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/printf", "/usr/bin/cat"})
	res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/printf", Args: []string{"%s|", "a b", "--x", "$(id)"}})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(res.Stdout); got != "a b|--x|$(id)|" {
		t.Errorf("printf output = %q", got)
	}
	res, err = r.Run(context.Background(), core.Cmd{Path: "/usr/bin/cat", Stdin: []byte("hello")})
	if err != nil || string(res.Stdout) != "hello" {
		t.Errorf("cat = %q, %v; want hello", res.Stdout, err)
	}
}

func TestRunReportsExitCodeAndStderr(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/false", "/usr/bin/cat"})
	res, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/false"})
	if err != nil || res.ExitCode != 1 {
		t.Errorf("false = exit %d, %v; want exit 1 and no error", res.ExitCode, err)
	}
	res, err = r.Run(context.Background(), core.Cmd{Path: "/usr/bin/cat", Args: []string{"--", "/nonexistent-darkpanel"}})
	if err != nil || res.ExitCode == 0 || !strings.Contains(string(res.Stderr), "nonexistent-darkpanel") {
		t.Errorf("cat missing file = exit %d, stderr %q, %v", res.ExitCode, res.Stderr, err)
	}
}

func TestRunTimesOut(t *testing.T) {
	r := NewRunner([]string{"/usr/bin/sleep"})
	r.defaultTimeout = 100 * time.Millisecond
	start := time.Now()
	_, err := r.Run(context.Background(), core.Cmd{Path: "/usr/bin/sleep", Args: []string{"10"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("no deadline: err = %v, want the default timeout", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	r.defaultTimeout = time.Hour
	_, err = r.Run(ctx, core.Cmd{Path: "/usr/bin/sleep", Args: []string{"10"}})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("caller deadline: err = %v, want deadline exceeded", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Errorf("timeouts took %v", elapsed)
	}
}

func TestRunCapsOutput(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRunner([]string{"/usr/bin/cat", "/usr/bin/yes", self})
	ctx := context.Background()
	res, err := r.Run(ctx, core.Cmd{Path: "/usr/bin/cat", Stdin: bytes.Repeat([]byte("x"), maxOutput)})
	if err != nil || len(res.Stdout) != maxOutput {
		t.Errorf("exactly 4 MiB: %d bytes, %v; want it accepted", len(res.Stdout), err)
	}
	cmds := map[string]core.Cmd{
		"stdout one byte over": {Path: "/usr/bin/cat", Stdin: bytes.Repeat([]byte("x"), maxOutput+1)},
		"stdout endless":       {Path: "/usr/bin/yes"},
		"stderr":               {Path: self, Args: []string{"-test.run=^TestExecHelperProcess$", "--", helperArg}},
	}
	for name, cmd := range cmds {
		_, err := r.Run(ctx, cmd)
		var coded *errcode.Error
		if !errors.As(err, &coded) || coded.Code != errcode.ExecOutputTooLarge {
			t.Errorf("%s: err = %v, want %s", name, err, errcode.ExecOutputTooLarge)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/platform/`
Expected: FAIL — the build reports `undefined: maxOutput`, `undefined: NewRunner` and `undefined: ProductionAllowlist`.

- [ ] **Step 3: Write the implementation**

Create `internal/platform/exec.go`:

```go
package platform

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// maxOutput is the most a program may write to either stream.
const maxOutput = 4 << 20

// environment is the whole environment of every program; nothing is inherited
// from the agent's own (ADR 0009).
var environment = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8"}

var errOutputTooLarge = errors.New("output too large")

// Runner is the core.Runner of the agent: it runs only allowlisted programs,
// by absolute path, with an argument vector and never through a shell.
type Runner struct {
	allowed        map[string]bool
	defaultTimeout time.Duration // applied when the caller's context has no deadline
}

var _ core.Runner = (*Runner)(nil)

// NewRunner returns a runner for the given absolute program paths. A relative
// or unclean path is a programming error and panics.
func NewRunner(allowlist []string) *Runner {
	r := &Runner{allowed: map[string]bool{}, defaultTimeout: 30 * time.Second}
	for _, p := range allowlist {
		if !filepath.IsAbs(p) || filepath.Clean(p) != p {
			panic(fmt.Sprintf("platform: allowlist entry %q is not a clean absolute path", p))
		}
		r.allowed[p] = true
	}
	return r
}

// ProductionAllowlist is the agent's allowlist. It is empty in M1; later
// epics add entries such as /usr/sbin/ufw.
func ProductionAllowlist() []string { return []string{} }

// cappedBuffer keeps at most limit bytes and cancels the run when a program
// writes more.
type cappedBuffer struct {
	buf    bytes.Buffer
	limit  int
	over   bool
	cancel context.CancelCauseFunc
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if c.buf.Len()+len(p) > c.limit {
		c.over = true
		c.cancel(errOutputTooLarge)
		return 0, errOutputTooLarge
	}
	return c.buf.Write(p)
}

// Run runs cmd. A non-zero exit is reported in the result's ExitCode, not as
// an error; a refused path, a timeout or a failure to start is an error, and
// more than 4 MiB on either stream fails with exec.output_too_large.
func (r *Runner) Run(ctx context.Context, cmd core.Cmd) (core.CmdResult, error) {
	if !r.allowed[cmd.Path] {
		return core.CmdResult{}, fmt.Errorf("platform: %s is not in the exec allowlist", cmd.Path)
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, r.defaultTimeout)
		defer cancel()
	}
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	c := exec.CommandContext(ctx, cmd.Path, cmd.Args...) //nolint:gosec // G204: an allowlisted absolute path and an argument vector, never a shell
	c.Env = slices.Clone(environment)
	c.Dir = "/"
	if len(cmd.Stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}
	stdout := &cappedBuffer{limit: maxOutput, cancel: cancel}
	stderr := &cappedBuffer{limit: maxOutput, cancel: cancel}
	c.Stdout, c.Stderr = stdout, stderr
	// A child that inherits the output pipes would otherwise keep Run waiting
	// after the program itself was killed.
	c.WaitDelay = time.Second

	err := c.Run()
	if stdout.over || stderr.over {
		return core.CmdResult{}, errcode.New(errcode.ExecOutputTooLarge, cmd.Path+" wrote more than 4 MiB")
	}
	res := core.CmdResult{Stdout: stdout.buf.Bytes(), Stderr: stderr.buf.Bytes()}
	if c.ProcessState != nil {
		res.ExitCode = c.ProcessState.ExitCode()
	}
	var exitErr *exec.ExitError
	switch {
	case ctx.Err() != nil:
		return res, fmt.Errorf("platform: %s: %w", cmd.Path, context.Cause(ctx))
	case errors.As(err, &exitErr):
		return res, nil
	case err != nil:
		return res, fmt.Errorf("platform: run %s: %w", cmd.Path, err)
	}
	return res, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/platform/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/exec.go internal/platform/exec_test.go
git commit -m "feat(platform): run allowlisted programs"
```

---

### Task 18: Ownership manifest

**Files:**
- Create: `internal/core/store/migrations/agent/0003_ownership.sql`
- Create: `internal/platform/ownership.go`
- Test: `internal/platform/ownership_test.go`

**Interfaces:**
- Consumes: `core.Ownership`, `core.OwnedEntry`, `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrateOptions` (foundation); package `platform` from Task 2.
- Produces: Contracts › `internal/platform` › Task 18 (`Ownership`, `NewOwnership`) and the agent table `core_owned_files`; plan 4's agent runtime passes `NewOwnership(agentDB)` as `FilesConfig.Ownership`, and M2's uninstall reads `List`.

**Items:** 7

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The migration is a new file next to the foundation's `0001_core.sql`, which must not be edited; `store.CoreMigrations` embeds it without any other change. Migration numbers may have gaps (foundation plan, Assumptions), so this file merges before or after the lifecycle part's `0002_lifecycle.sql`. A whole file has an empty `block_id`, and the table's `CHECK` keeps kind and block id consistent.

- [ ] **Step 1: Write the failing test**

Create `internal/platform/ownership_test.go`:

```go
package platform

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// openAgentDB returns a temporary agent database with the core migrations
// applied.
func openAgentDB(t *testing.T) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"),
		Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestOwnershipRecordListForget(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
	file := core.OwnedEntry{Path: "/etc/darkpanel/a.conf", Module: "demo", Kind: "file", CreatedAt: at}
	block := core.OwnedEntry{Path: "/etc/hosts", Module: "demo", Kind: "block", BlockID: "names", CreatedAt: at}
	for _, e := range []core.OwnedEntry{block, file, file} {
		if err := o.Record(ctx, e); err != nil {
			t.Fatalf("Record(%+v): %v", e, err)
		}
	}
	later := file
	later.CreatedAt = at.Add(time.Hour)
	if err := o.Record(ctx, later); err != nil {
		t.Fatalf("Record again: %v", err)
	}
	got, err := o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.OwnedEntry{file, block}; !reflect.DeepEqual(got, want) {
		t.Errorf("List = %+v, want %+v (sorted by path, first record kept)", got, want)
	}
	if err := o.Forget(ctx, "demo", "/etc/hosts", "names"); err != nil {
		t.Fatal(err)
	}
	if err := o.Forget(ctx, "other", "/etc/darkpanel/a.conf", ""); err != nil {
		t.Fatal(err)
	}
	got, err = o.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if want := []core.OwnedEntry{file}; !reflect.DeepEqual(got, want) {
		t.Errorf("after Forget: %+v, want %+v (another module cannot forget demo's file)", got, want)
	}
}

func TestOwnershipRefusesInvalidEntries(t *testing.T) {
	o := NewOwnership(openAgentDB(t))
	ctx := context.Background()
	bad := []core.OwnedEntry{
		{Path: "/etc/a", Module: "demo", Kind: "dir"},
		{Path: "/etc/a", Module: "demo", Kind: "file", BlockID: "x"},
		{Path: "/etc/a", Module: "demo", Kind: "block"},
	}
	for _, e := range bad {
		if err := o.Record(ctx, e); err == nil {
			t.Errorf("Record(%+v) succeeded, want an error", e)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/platform/`
Expected: FAIL — the build reports `undefined: NewOwnership`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/agent/0003_ownership.sql`:

```sql
-- The ownership manifest (ADR 0009): every file and marked block Darkpanel
-- wrote on this host, which darkpanel uninstall reverses. A whole file has an
-- empty block_id.

CREATE TABLE core_owned_files (
    path       TEXT NOT NULL,
    module     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('file', 'block')),
    block_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (path, block_id),
    CHECK ((kind = 'file') = (block_id = ''))
) STRICT;
```

Create `internal/platform/ownership.go`:

```go
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

// Record adds e. Recording a path and block id that are already recorded
// changes nothing, so the first write's time is kept.
func (o *Ownership) Record(ctx context.Context, e core.OwnedEntry) error {
	return o.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_owned_files (path, module, kind, block_id, created_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (path, block_id) DO NOTHING`,
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

// List returns every entry, sorted by path and block id.
func (o *Ownership) List(ctx context.Context) ([]core.OwnedEntry, error) {
	rows, err := o.db.Read().QueryContext(ctx,
		`SELECT path, module, kind, block_id, created_at FROM core_owned_files ORDER BY path, block_id`)
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
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/platform/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/agent/0003_ownership.sql internal/platform/ownership.go internal/platform/ownership_test.go
git commit -m "feat(platform): record owned files and blocks"
```

---

### Task 19: File writer

**Files:**
- Create: `internal/platform/files.go`
- Test: `internal/platform/files_test.go`

**Interfaces:**
- Consumes: `replaceBlock` and `removeBlock` from Task 2; `core.FileWriter`, `core.Ownership`, `core.OwnedPath`, `core.OwnedEntry`, `core.Manifest`, `core.Clock`, `coretest.NewFakeClock`, `errcode.FilesMarkersBad` (foundation). The tests use an in-memory `core.Ownership` fake, so this task does not need Task 18.
- Produces: Contracts › `internal/platform` › Task 19 (`FilesConfig`, `Files`, `NewFiles`, `OwnedPaths`); plan 4's agent runtime builds `NewFiles(FilesConfig{Owned: OwnedPaths(registry.Modules()), Ownership: NewOwnership(agentDB), Clock: core.SystemClock{}})` as `core.Env.Files`.

**Items:** 7

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

These are ADR 0009's defences; keep each: the declaration check before anything is opened, `openat2` with `RESOLVE_NO_SYMLINKS` on the parent, the parent owner and mode check, `O_NOFOLLOW|O_NONBLOCK` on the target (a symlink is refused, a FIFO cannot block the open) with a regular-file check, the temporary file renamed over the target relative to the directory descriptor, the fsyncs, and `Fchmod` after `Fchown` (which clears set-id bits). Ownership is recorded before the host is touched. The tests run as root locally and as an ordinary user on CI runners; the root-only cases check `os.Geteuid()`.

- [ ] **Step 1: Write the failing test**

Create `internal/platform/files_test.go`:

```go
package platform

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"golang.org/x/sys/unix"
)

// fakeOwnership is a core.Ownership in memory.
type fakeOwnership struct {
	mu      sync.Mutex
	entries []core.OwnedEntry
}

func (o *fakeOwnership) Record(_ context.Context, e core.OwnedEntry) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !slices.ContainsFunc(o.entries, func(x core.OwnedEntry) bool { return x.Path == e.Path && x.BlockID == e.BlockID }) {
		o.entries = append(o.entries, e)
	}
	return nil
}

func (o *fakeOwnership) Forget(_ context.Context, module, path, blockID string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.entries = slices.DeleteFunc(o.entries, func(x core.OwnedEntry) bool {
		return x.Module == module && x.Path == path && x.BlockID == blockID
	})
	return nil
}

func (o *fakeOwnership) List(context.Context) ([]core.OwnedEntry, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.entries), nil
}

var filesNow = time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)

// newTestFiles returns a writer in TestMode for module "demo", which owns
// <dir>/app.conf as a file, block "names" of <dir>/hosts and block "rules" of
// <dir>/rules.conf with comment prefix "//".
func newTestFiles(t *testing.T) (*Files, *fakeOwnership, string) {
	t.Helper()
	dir := t.TempDir()
	own := &fakeOwnership{}
	f := NewFiles(FilesConfig{
		Owned: map[string][]core.OwnedPath{"demo": {
			{Path: filepath.Join(dir, "app.conf"), Kind: "file"},
			{Path: filepath.Join(dir, "hosts"), Kind: "block", BlockIDs: []string{"names"}},
			{Path: filepath.Join(dir, "rules.conf"), Kind: "block", BlockIDs: []string{"rules"}, CommentPrefix: "//"},
		}},
		Ownership: own,
		Clock:     coretest.NewFakeClock(filesNow),
		TestMode:  true,
	})
	return f, own, dir
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test's temporary path
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func dirNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestWriteFileCreatesAndRecords(t *testing.T) {
	f, own, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := f.WriteFile(context.Background(), "demo", path, []byte("a=1\n"), 0o640); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if got := readFile(t, path); got != "a=1\n" {
		t.Errorf("content = %q", got)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o640 {
		t.Errorf("mode = %o, want 640", mode)
	}
	if names := dirNames(t, dir); !reflect.DeepEqual(names, []string{"app.conf"}) {
		t.Errorf("directory holds %v, want only app.conf (no temporary file left)", names)
	}
	want := []core.OwnedEntry{{Path: path, Module: "demo", Kind: "file", CreatedAt: filesNow}}
	if got, _ := own.List(context.Background()); !reflect.DeepEqual(got, want) {
		t.Errorf("ownership = %+v, want %+v", got, want)
	}
}

func TestWriteFileKeepsModeAndOwner(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := os.WriteFile(path, []byte("old\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o604); err != nil { //nolint:gosec // G302: an unusual mode the write must keep
		t.Fatal(err)
	}
	uid, gid := os.Geteuid(), os.Getegid()
	if uid == 0 {
		uid, gid = 12345, 12346
		if err := os.Chown(path, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := f.WriteFile(context.Background(), "demo", path, []byte("new\n"), 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st := info.Sys().(*syscall.Stat_t)
	if info.Mode().Perm() != 0o604 || int(st.Uid) != uid || int(st.Gid) != gid {
		t.Errorf("mode %o owner %d:%d, want 604 and %d:%d", info.Mode().Perm(), st.Uid, st.Gid, uid, gid)
	}
	if got := readFile(t, path); got != "new\n" {
		t.Errorf("content = %q", got)
	}
}

func TestUndeclaredWritesRefused(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	app, hosts := filepath.Join(dir, "app.conf"), filepath.Join(dir, "hosts")
	attempts := map[string]error{
		"other path":        f.WriteFile(ctx, "demo", filepath.Join(dir, "other.conf"), []byte("x"), 0o644),
		"other module":      f.WriteFile(ctx, "other", app, []byte("x"), 0o644),
		"unclean path":      f.WriteFile(ctx, "demo", dir+"/./app.conf", []byte("x"), 0o644),
		"file as block":     f.ReplaceBlock(ctx, "demo", app, "names", []byte("x")),
		"block as file":     f.WriteFile(ctx, "demo", hosts, []byte("x"), 0o644),
		"undeclared block":  f.ReplaceBlock(ctx, "demo", hosts, "other", []byte("x")),
		"remove undeclared": f.Remove(ctx, "demo", hosts),
	}
	for name, err := range attempts {
		if err == nil {
			t.Errorf("%s: succeeded, want a refusal", name)
		}
	}
	if names := dirNames(t, dir); len(names) != 0 {
		t.Errorf("directory holds %v, want nothing written", names)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestSymlinkedParentRefused(t *testing.T) {
	dir := t.TempDir()
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(realDir, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "link", "app.conf")
	f := NewFiles(FilesConfig{
		Owned:     map[string][]core.OwnedPath{"demo": {{Path: path, Kind: "file"}}},
		Ownership: &fakeOwnership{}, Clock: coretest.NewFakeClock(filesNow), TestMode: true,
	})
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile through a symlinked parent succeeded")
	}
	if names := dirNames(t, realDir); len(names) != 0 {
		t.Errorf("real directory holds %v, want nothing", names)
	}
}

func TestSymlinkTargetRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(victim, []byte("keep\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, filepath.Join(dir, "app.conf")); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(context.Background(), "demo", filepath.Join(dir, "app.conf"), []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a symlink succeeded")
	}
	if got := readFile(t, victim); got != "keep\n" {
		t.Errorf("symlink target changed to %q", got)
	}
}

func TestNonRegularTargetRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	if err := unix.Mkfifo(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile over a FIFO succeeded")
	}
}

func TestUnsafeParentRefused(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "app.conf")
	for _, mode := range []os.FileMode{0o770, 0o707} {
		if err := os.Chmod(dir, mode); err != nil {
			t.Fatal(err)
		}
		if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
			t.Errorf("parent mode %o: WriteFile succeeded, want a refusal", mode)
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory needs its search bit
		t.Fatal(err)
	}
	if os.Geteuid() == 0 {
		if err := os.Chown(dir, 12345, 12345); err != nil {
			t.Fatal(err)
		}
		if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
			t.Error("parent owned by another user: WriteFile succeeded, want a refusal")
		}
	}
}

func TestProductionModeRequiresRootParent(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: every temporary directory is owned by root")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "app.conf")
	f := NewFiles(FilesConfig{
		Owned:     map[string][]core.OwnedPath{"demo": {{Path: path, Kind: "file"}}},
		Ownership: &fakeOwnership{}, Clock: coretest.NewFakeClock(filesNow),
	})
	if err := f.WriteFile(context.Background(), "demo", path, []byte("x"), 0o644); err == nil {
		t.Fatal("WriteFile into a directory not owned by root succeeded")
	}
}

func TestBlocks(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	hosts := filepath.Join(dir, "hosts")
	if err := f.ReplaceBlock(ctx, "demo", hosts, "names", []byte("10.0.0.1 a\n")); err != nil {
		t.Fatalf("ReplaceBlock (create): %v", err)
	}
	info, err := os.Stat(hosts)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Errorf("new block file mode = %o, want 644", info.Mode().Perm())
	}
	if err := os.WriteFile(hosts, []byte("127.0.0.1 localhost\n"+readFile(t, hosts)), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := f.ReplaceBlock(ctx, "demo", hosts, "names", []byte("10.0.0.2 b\n")); err != nil {
		t.Fatalf("ReplaceBlock (replace): %v", err)
	}
	want := "127.0.0.1 localhost\n# BEGIN DARKPANEL demo/names\n10.0.0.2 b\n# END DARKPANEL demo/names\n"
	if got := readFile(t, hosts); got != want {
		t.Errorf("hosts = %q, want %q", got, want)
	}
	wantOwn := []core.OwnedEntry{{Path: hosts, Module: "demo", Kind: "block", BlockID: "names", CreatedAt: filesNow}}
	if got, _ := own.List(ctx); !reflect.DeepEqual(got, wantOwn) {
		t.Errorf("ownership = %+v, want %+v", got, wantOwn)
	}
	if err := f.RemoveBlock(ctx, "demo", hosts, "names"); err != nil {
		t.Fatalf("RemoveBlock: %v", err)
	}
	if got := readFile(t, hosts); got != "127.0.0.1 localhost\n" {
		t.Errorf("after RemoveBlock: %q", got)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership after RemoveBlock = %+v, want none", got)
	}
	if err := f.RemoveBlock(ctx, "demo", filepath.Join(dir, "rules.conf"), "rules"); err != nil {
		t.Errorf("RemoveBlock of a missing file: %v", err)
	}
}

func TestBlockCommentPrefix(t *testing.T) {
	f, _, dir := newTestFiles(t)
	path := filepath.Join(dir, "rules.conf")
	if err := f.ReplaceBlock(context.Background(), "demo", path, "rules", []byte("x\n")); err != nil {
		t.Fatal(err)
	}
	if got, want := readFile(t, path), "// BEGIN DARKPANEL demo/rules\nx\n// END DARKPANEL demo/rules\n"; got != want {
		t.Errorf("content = %q, want %q", got, want)
	}
}

func TestInvalidMarkersLeaveFileUnchanged(t *testing.T) {
	f, own, dir := newTestFiles(t)
	hosts := filepath.Join(dir, "hosts")
	bad := "# BEGIN DARKPANEL demo/names\nx\n"
	if err := os.WriteFile(hosts, []byte(bad), 0o600); err != nil {
		t.Fatal(err)
	}
	err := f.ReplaceBlock(context.Background(), "demo", hosts, "names", []byte("y\n"))
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != errcode.FilesMarkersBad {
		t.Errorf("err = %v, want %s", err, errcode.FilesMarkersBad)
	}
	if got := readFile(t, hosts); got != bad {
		t.Errorf("file changed to %q", got)
	}
	if got, _ := own.List(context.Background()); len(got) != 0 {
		t.Errorf("ownership = %+v, want nothing recorded", got)
	}
}

func TestRemove(t *testing.T) {
	f, own, dir := newTestFiles(t)
	ctx := context.Background()
	path := filepath.Join(dir, "app.conf")
	if err := f.WriteFile(ctx, "demo", path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.Remove(ctx, "demo", path); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("file still present: %v", err)
	}
	if got, _ := own.List(ctx); len(got) != 0 {
		t.Errorf("ownership = %+v, want none", got)
	}
	if err := f.Remove(ctx, "demo", path); err != nil {
		t.Errorf("Remove of a missing file: %v", err)
	}
}

func TestOwnedPaths(t *testing.T) {
	a := core.OwnedPath{Path: "/etc/a", Kind: "file"}
	got := OwnedPaths([]core.Manifest{{ID: "one", OwnedPaths: []core.OwnedPath{a}}, {ID: "two"}})
	if want := map[string][]core.OwnedPath{"one": {a}}; !reflect.DeepEqual(got, want) {
		t.Errorf("OwnedPaths = %+v, want %+v", got, want)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/platform/`
Expected: FAIL — the build reports `undefined: Files`, `undefined: NewFiles` and `undefined: FilesConfig`.

- [ ] **Step 3: Write the implementation**

Create `internal/platform/files.go`:

```go
package platform

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
)

// The owned-path kinds of core.OwnedPath and core.OwnedEntry.
const (
	kindFile  = "file"
	kindBlock = "block"
)

// newBlockFileMode is the mode of a file ReplaceBlock creates.
const newBlockFileMode = 0o644

// FilesConfig configures a Files writer.
type FilesConfig struct {
	Owned     map[string][]core.OwnedPath // the paths each module declares, by module id
	Ownership core.Ownership
	Clock     core.Clock
	TestMode  bool // parent directories must be owned by the effective uid instead of root
}

// Files is the core.FileWriter of the agent: it writes only the paths and
// marked blocks a module declares, never through a symlink (ADR 0009).
type Files struct {
	cfg       FilesConfig
	parentUID int
}

var _ core.FileWriter = (*Files)(nil)

// NewFiles returns a writer for cfg.
func NewFiles(cfg FilesConfig) *Files {
	uid := 0
	if cfg.TestMode {
		uid = os.Geteuid()
	}
	return &Files{cfg: cfg, parentUID: uid}
}

// OwnedPaths collects the owned paths of the manifests by module id, for
// FilesConfig.Owned.
func OwnedPaths(mods []core.Manifest) map[string][]core.OwnedPath {
	out := map[string][]core.OwnedPath{}
	for _, m := range mods {
		if len(m.OwnedPaths) > 0 {
			out[m.ID] = slices.Clone(m.OwnedPaths)
		}
	}
	return out
}

// declared returns module's declaration of path as kind, which for a block
// must list blockID.
func (f *Files) declared(module, path, kind, blockID string) (core.OwnedPath, error) {
	if filepath.IsAbs(path) && filepath.Clean(path) == path && filepath.Base(path) != "/" {
		for _, op := range f.cfg.Owned[module] {
			if op.Path == path && op.Kind == kind && (kind == kindFile || slices.Contains(op.BlockIDs, blockID)) {
				return op, nil
			}
		}
	}
	if kind == kindBlock {
		return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare block %s of %s", module, blockID, path)
	}
	return core.OwnedPath{}, fmt.Errorf("platform: module %s does not declare file %s", module, path)
}

// parent is a target's parent directory, opened without following any
// symlink and checked for ownership and mode.
type parent struct {
	fd   int
	name string // the target's name inside the directory
	path string // the target's full path, for messages
}

func (f *Files) openParent(path string) (*parent, error) {
	dir, name := filepath.Split(path)
	// RESOLVE_NO_SYMLINKS fails if any component of dir is a symlink, which
	// O_NOFOLLOW alone would not catch (ADR 0009).
	fd, err := unix.Openat2(unix.AT_FDCWD, dir, &unix.OpenHow{
		Flags:   unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC,
		Resolve: unix.RESOLVE_NO_SYMLINKS,
	})
	if err != nil {
		return nil, fmt.Errorf("platform: open directory %s: %w", dir, err)
	}
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("platform: stat directory %s: %w", dir, err)
	}
	switch {
	case int(st.Uid) != f.parentUID:
		_ = unix.Close(fd)
		return nil, fmt.Errorf("platform: directory %s is owned by uid %d, want %d", dir, st.Uid, f.parentUID)
	case st.Mode&0o022 != 0:
		_ = unix.Close(fd)
		return nil, fmt.Errorf("platform: directory %s is writable by group or others", dir)
	}
	return &parent{fd: fd, name: name, path: path}, nil
}

func (p *parent) close() { _ = unix.Close(p.fd) }

// read returns the target's content and status; exists is false when there
// is no target. A symlink or anything but a regular file is refused.
func (p *parent) read() (data []byte, st unix.Stat_t, exists bool, err error) {
	// O_NONBLOCK keeps a FIFO planted at the target from blocking the open.
	fd, err := unix.Openat(p.fd, p.name, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	switch {
	case errors.Is(err, unix.ENOENT):
		return nil, st, false, nil
	case errors.Is(err, unix.ELOOP):
		return nil, st, false, fmt.Errorf("platform: %s is a symlink", p.path)
	case err != nil:
		return nil, st, false, fmt.Errorf("platform: open %s: %w", p.path, err)
	}
	file := os.NewFile(uintptr(fd), p.path)
	defer func() { _ = file.Close() }()
	if err := unix.Fstat(fd, &st); err != nil {
		return nil, st, false, fmt.Errorf("platform: stat %s: %w", p.path, err)
	}
	if st.Mode&unix.S_IFMT != unix.S_IFREG {
		return nil, st, false, fmt.Errorf("platform: %s is not a regular file", p.path)
	}
	if data, err = io.ReadAll(file); err != nil {
		return nil, st, false, fmt.Errorf("platform: read %s: %w", p.path, err)
	}
	return data, st, true, nil
}

// write replaces the target with data through a temporary file renamed over
// it, so readers see the old or the new content and never a mix. An existing
// target's mode and owner are kept; a new one gets mode.
func (p *parent) write(data []byte, mode uint32, existing *unix.Stat_t) error {
	var rnd [8]byte
	if _, err := rand.Read(rnd[:]); err != nil {
		return fmt.Errorf("platform: temporary name: %w", err)
	}
	tmp := fmt.Sprintf(".%s.darkpanel-%x", p.name, rnd)
	fd, err := unix.Openat(p.fd, tmp, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return fmt.Errorf("platform: create temporary file for %s: %w", p.path, err)
	}
	file := os.NewFile(uintptr(fd), tmp)
	err = func() error {
		if _, err := file.Write(data); err != nil {
			return err
		}
		if existing != nil {
			mode = existing.Mode & 0o7777
			if err := unix.Fchown(fd, int(existing.Uid), int(existing.Gid)); err != nil {
				return err
			}
		}
		// After Fchown, which clears set-user-ID and set-group-ID bits.
		if err := unix.Fchmod(fd, mode); err != nil {
			return err
		}
		return file.Sync()
	}()
	err = errors.Join(err, file.Close())
	if err == nil {
		err = unix.Renameat(p.fd, tmp, p.fd, p.name)
	}
	if err != nil {
		_ = unix.Unlinkat(p.fd, tmp, 0)
		return fmt.Errorf("platform: write %s: %w", p.path, err)
	}
	if err := unix.Fsync(p.fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", p.path, err)
	}
	return nil
}

// edit rewrites path with change applied to its current content (empty when
// it does not exist), recording ownership first.
func (f *Files) edit(ctx context.Context, module, path, blockID string, mode uint32, change func(old []byte) ([]byte, error)) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	if err != nil {
		return err
	}
	defer p.close()
	old, st, exists, err := p.read()
	if err != nil {
		return err
	}
	data, err := change(old)
	if err != nil {
		return err
	}
	kind := kindFile
	if blockID != "" {
		kind = kindBlock
	}
	// Recorded before the host is touched, so a crash after the write still
	// leaves the path for uninstall to find.
	entry := core.OwnedEntry{Path: path, Module: module, Kind: kind, BlockID: blockID, CreatedAt: f.cfg.Clock.Now().UTC()}
	if err := f.cfg.Ownership.Record(ctx, entry); err != nil {
		return err
	}
	var existing *unix.Stat_t
	if exists {
		existing = &st
	}
	return p.write(data, mode, existing)
}

// WriteFile replaces the whole file path, which module must declare as a
// file; a new file gets mode's permission bits.
func (f *Files) WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error {
	if _, err := f.declared(module, path, kindFile, ""); err != nil {
		return err
	}
	return f.edit(ctx, module, path, "", uint32(mode.Perm()), func([]byte) ([]byte, error) { return data, nil })
}

// ReplaceBlock sets the content of block blockID in path, appending the block
// when absent and creating the file (mode 0644) when missing.
func (f *Files) ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error {
	op, err := f.declared(module, path, kindBlock, blockID)
	if err != nil {
		return err
	}
	prefix := commentPrefix(op)
	return f.edit(ctx, module, path, blockID, newBlockFileMode, func(old []byte) ([]byte, error) {
		return replaceBlock(old, prefix, module, blockID, content)
	})
}

// RemoveBlock deletes block blockID from path and forgets it; a missing file
// or block is not an error.
func (f *Files) RemoveBlock(ctx context.Context, module, path, blockID string) error {
	op, err := f.declared(module, path, kindBlock, blockID)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	if err != nil {
		return err
	}
	defer p.close()
	old, st, exists, err := p.read()
	if err != nil {
		return err
	}
	if exists {
		data, err := removeBlock(old, commentPrefix(op), module, blockID)
		if err != nil {
			return err
		}
		if !bytes.Equal(data, old) {
			if err := p.write(data, 0, &st); err != nil {
				return err
			}
		}
	}
	return f.cfg.Ownership.Forget(ctx, module, path, blockID)
}

// Remove deletes the file path and forgets it; a missing file is not an
// error. A symlink at path is removed itself, never followed.
func (f *Files) Remove(ctx context.Context, module, path string) error {
	if _, err := f.declared(module, path, kindFile, ""); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	p, err := f.openParent(path)
	if err != nil {
		return err
	}
	defer p.close()
	if err := unix.Unlinkat(p.fd, p.name, 0); err != nil && !errors.Is(err, unix.ENOENT) {
		return fmt.Errorf("platform: remove %s: %w", path, err)
	}
	if err := unix.Fsync(p.fd); err != nil {
		return fmt.Errorf("platform: sync directory of %s: %w", path, err)
	}
	return f.cfg.Ownership.Forget(ctx, module, path, "")
}

func commentPrefix(op core.OwnedPath) string {
	if op.CommentPrefix == "" {
		return "#"
	}
	return op.CommentPrefix
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/platform/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/platform/files.go internal/platform/files_test.go
git commit -m "feat(platform): write declared files and blocks"
```

---

### Task 20: Recovery and purge

**Files:**
- Create: `internal/core/lifecycle/recover.go`
- Test: `internal/core/lifecycle/recover_test.go`

**Interfaces:**
- Consumes: Task 14 (`transition`, `runRevert`, `revertFailed`, `changeError`, `apply_test.go` helpers), Task 16 (`revert_test.go` helpers `pending` and `state`), Task 12 (`planInputAD`, `env`, the fixture's `open` and `errCrash`).
- Produces: Contracts › `internal/core/lifecycle` › Task 20 (`Recover`, `Purge`, `ActorRecovery`); test helpers in `recover_test.go`: `(*fixture).restart`, `(*fixture).crash`, `(*fixture).recoverAll`.

**Items:** 6

**Depends on:** Task 16

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The crash tests simulate a process that dies after the intent is persisted: the fixture's `panicApply` flag panics inside the handler, the test recovers the panic, then closes and reopens the database and builds a new engine on the same file before calling `Recover`. Recovery never retries or compensates a compensable or irreversible change (ADR 0004); it only reports what the action's verify says.

- [ ] **Step 1: Write the failing test**

Create `internal/core/lifecycle/recover_test.go`:

```go
package lifecycle

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// restart closes the engine and the database and opens both again on the
// same file, as a restarted agent does.
func (f *fixture) restart() {
	f.t.Helper()
	f.eng.Close()
	if err := f.db.Close(); err != nil {
		f.t.Fatal(err)
	}
	f.open()
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
	}
	return planID, changeID
}

func (f *fixture) recoverAll() {
	f.t.Helper()
	if err := f.eng.Recover(context.Background()); err != nil {
		f.t.Fatalf("Recover: %v", err)
	}
}

func TestRecoverRevertsAnInterruptedRevertibleApply(t *testing.T) {
	f := newFixture(t)
	f.host.items["a"] = "old"
	planID, id := f.crash(refSet, `{"name":"a","value":"1","panicApply":true}`, core.ApplyOptions{})
	if s := f.state(id); s != core.StateApplying || f.count("SELECT count(*) FROM core_locks WHERE owner = ?", id) != 1 {
		t.Fatalf("after the crash: state %s, want applying with its lock", s)
	}
	f.host.items["a"] = "half" // the mutation had begun
	f.restart()
	f.recoverAll()

	if s := f.state(id); s != core.StateReverted || f.host.items["a"] != "old" {
		t.Errorf("state %s, item a %q; want reverted to old", s, f.host.items["a"])
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"recover/ok"}) {
		t.Errorf("journal = %v", got)
	}
	entries, _ := f.journal.Entries(context.Background(), 0, 10)
	if entries[0].Actor != ActorRecovery {
		t.Errorf("recovery actor = %s, want %s", entries[0].Actor, ActorRecovery)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 || !f.planInputCleared(planID) {
		t.Error("recovery kept the lock or the plan's sealed input")
	}
}

func TestRecoverRevertsPendingChangesWhateverTheDeadline(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1"}`)
	f.restart()
	f.recoverAll()
	if _, ok := f.host.items["a"]; f.state(v.ID) != core.StateReverted || ok {
		t.Errorf("state %s, item present %v; want reverted", f.state(v.ID), ok)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "recover/ok"}) {
		t.Errorf("journal = %v", got)
	}
	if f.clock.Pending() != 0 {
		t.Errorf("armed timers = %d, want 0", f.clock.Pending())
	}
}

func TestRecoverRecordsAFailedRevert(t *testing.T) {
	f := newFixture(t)
	v := f.pending(`{"name":"a","value":"1","failRevert":true}`)
	f.restart()
	f.recoverAll()
	c, err := f.eng.Change(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	if c.State != core.StateFailed || c.Error == nil || c.Error.Code != string(errcode.ChangeRevertFailed) {
		t.Errorf("change = %+v (error %+v), want failed with change.revert_failed", c, c.Error)
	}
	if got := f.phases(); !reflect.DeepEqual(got, []string{"apply/ok", "recover/error/change.revert_failed"}) {
		t.Errorf("journal = %v", got)
	}
	if f.count("SELECT count(*) FROM core_locks") != 0 {
		t.Error("recovery kept the lock of a failed change")
	}
}

func TestRecoverInterruptsCompensableAndIrreversible(t *testing.T) {
	cases := []struct {
		name    string
		ref     core.ActionRef
		opts    core.ApplyOptions
		mutated bool // whether the host shows the change at restart
		verify  string
		compBy  string
	}{
		{"compensable, not applied", refAdd, core.ApplyOptions{}, false, "verify failed", refRemove.Name},
		{"irreversible, applied", refRemove, core.ApplyOptions{Confirmation: "a"}, true, "verify passed", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			ctx := context.Background()
			planID, id := f.crash(c.ref, `{"name":"a","value":"1","panicApply":true}`, c.opts)
			if c.mutated {
				f.host.items["a"] = "1"
			}
			f.restart()
			f.recoverAll()

			v, err := f.eng.Change(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if v.State != core.StateInterrupted || v.CompensatedBy != c.compBy || v.Error == nil ||
				v.Error.Code != string(errcode.Internal) || !strings.Contains(v.Error.Message, c.verify) {
				t.Errorf("change = %+v (error %+v), want interrupted reporting %q", v, v.Error, c.verify)
			}
			if got := f.phases(); !reflect.DeepEqual(got, []string{"interrupt/error/internal"}) {
				t.Errorf("journal = %v", got)
			}
			if f.count("SELECT count(*) FROM core_locks") != 0 || !f.planInputCleared(planID) {
				t.Error("recovery kept the lock or the plan's sealed input")
			}
			again, err := f.eng.Apply(ctx, alice, planID, c.opts)
			if err != nil || again.ID != id || again.State != core.StateInterrupted {
				t.Errorf("repeated apply = %+v, %v; want the interrupted change", again, err)
			}
			if f.host.applies != 0 {
				t.Errorf("handler applied %d times, want 0: recovery never retries", f.host.applies)
			}
		})
	}
}

func TestRecoverReleasesLocksOfFinishedChanges(t *testing.T) {
	f := newFixture(t)
	done := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	for key, owner := range map[string]string{"stray": done.ID, "update": "update-txn-1"} {
		if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES (?, ?)", key, owner); err != nil {
			t.Fatal(err)
		}
	}
	f.restart()
	f.recoverAll()
	var keys []string
	rows, err := f.db.Read().Query("SELECT key FROM core_locks ORDER BY key")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var k string
		if err := rows.Scan(&k); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, k)
	}
	if !reflect.DeepEqual(keys, []string{"update"}) {
		t.Errorf("locks after recovery = %v, want only the update transaction's", keys)
	}
}

func TestRecoverOnACleanStartDoesNothing(t *testing.T) {
	f := newFixture(t)
	f.recoverAll()
	if got := f.phases(); len(got) != 0 {
		t.Errorf("journal = %v, want empty", got)
	}
}

func TestPurgeDeletesExpiredPlansWithoutAChange(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	unused := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	applied := f.plan(alice, refAdd, `{"name":"b","value":"1"}`)
	f.apply(alice, applied.PlanID, core.ApplyOptions{})
	f.clock.Advance(PlanTTL)
	fresh := f.plan(alice, refAdd, `{"name":"c","value":"1"}`)
	if err := f.eng.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{unused.PlanID: 0, applied.PlanID: 1, fresh.PlanID: 1} {
		if n := f.count("SELECT count(*) FROM core_plans WHERE id = ?", id); n != want {
			t.Errorf("plan %s rows = %d, want %d", id, n, want)
		}
	}
	_, err := f.eng.Apply(ctx, alice, unused.PlanID, core.ApplyOptions{})
	wantCode(t, err, errcode.PlanNotFound)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/`
Expected: FAIL — the build reports `undefined: ActorRecovery`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/lifecycle/recover.go`:

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
	if err := def.Handler.Verify(ctx, e.env(recoveryPrincipal), input, json.RawMessage("null")); err != nil {
		return "verify failed: " + e.changeError(def.Ref(), err).Message
	}
	return "verify passed"
}

// Purge deletes expired plans that never became a change. A plan with a change
// is kept, so a repeated apply still returns that change (spec section 3).
func (e *Engine) Purge(ctx context.Context) error {
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

- [ ] **Step 4: Run the tests to verify they pass**

Run, in order:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 600 go test -race -count=3 ./internal/core/lifecycle/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every test line starts with `ok`; `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add internal/core/lifecycle/recover.go internal/core/lifecycle/recover_test.go
git commit -m "feat(lifecycle): recover unfinished changes"
```

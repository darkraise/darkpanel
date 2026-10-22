# M1 epics: module framework, walking skeleton, identity and test harness

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17; revised for the Fable
review (docs/dforge/findings/2026-10-17-m1-platform-core-review.md),
whose recommendations the owner accepted on 2026-10-17
**Product spec:** docs/dforge/specs/2026-10-17-darkpanel-design.md
**Requirements:** R5–R21
**Execution strategy:** parallel — one foundation part fixes every shared
type, interface, dependency and file layout first; then the secrets, platform,
schema, lifecycle, identity, host-information, web and harness parts share no
file and run together, followed by the API, listener, dispatch, core-action
and CLI parts, and a final assembly part (owner ruling 2026-10-17: favour
parallel to reach v1 sooner).

## Goal

Meet the M1 exit criteria that remain after the ADR and tooling epics: one
binary serves the host-information module through the web UI, the REST API and
the CLI behind a TOTP login; CI and gates are green; the integration harness
boots all four operating systems. The module framework, identity and audit are
built to their production shape, so later milestones add modules and a network
transport without reworking them.

## Owner decisions (2026-10-17)

- One spec covers the four remaining M1 epics (module framework, walking
  skeleton, identity, sessions and audit, integration test harness).
- Execution is parallel subagent waves.
- The harness workflow runs on pushes to `main`, nightly and on manual
  dispatch; pull requests and branch pushes skip it.
- A lost TOTP device is recovered only through `darkpanel user reset-totp` on
  the controller host; there are no recovery codes.
- Identity operations are core registry actions (approach A), so they get REST,
  CLI and audit from the same generic code as module actions.
- The web certificate comes from an internal authority created in M1
  (ADR 0010, section 21).
- During execution, work branches may be pushed to `darkraise/darkpanel` and
  the harness workflow dispatched on them, because the development host has no
  `/dev/kvm`; branches are deleted after merge.
- Every Fable review recommendation is accepted, including the four modified
  fixes: split session cookies (section 9), a QEMU multicast link for the
  harness (section 18), one migration file per owning part (section 4), and
  ADR 0011 for the contract additions (section 21).

## Versions

Checked against the Go module proxy and npm on 2026-10-17. Pins are exact.

| Package | Version | Use |
|---|---|---|
| `modernc.org/sqlite` | v1.60.1 | SQLite without CGO (ADR 0005); `modernc.org/libc` pinned to the version it requires |
| `github.com/invopop/jsonschema` | v0.14.0 | schemas from Go types (shared contracts) |
| `github.com/santhosh-tekuri/jsonschema/v6` | v6.0.3 | input validation against the generated schemas |
| `golang.org/x/crypto` | v0.58.0 | argon2id, XChaCha20-Poly1305, HKDF, SSH client for the harness |
| `golang.org/x/sys` | v0.49.0 | `openat2`, `SO_PEERCRED`, `IP_FREEBIND` |
| `golang.org/x/term` | v0.47.0 | password prompts without echo |
| `github.com/oklog/ulid/v2` | v2.1.2 | ULIDs for host, change, plan, user, token and audit ids |
| `github.com/mdp/qrterminal/v3` | v3.2.1 | TOTP enrollment QR code in the terminal |
| `react-router` | 8.4.0 | web routing in declarative mode; `NavLink` sets `aria-current` as darkraise-ui requires |
| `@tanstack/react-query` | 5.104.3 | API data fetching (ADR 0001) |
| `@fontsource-variable/hanken-grotesk` | 5.3.0 | self-hosted UI font (direction A) |
| `@fontsource-variable/jetbrains-mono` | 5.3.0 | self-hosted monospace font (direction A) |

The harness pins its cloud images, Docker Engine and Tailscale packages by
exact URL, version and SHA-256 at plan time (section 18).

## Design

### 1. Process, paths and configuration

`darkpanel serve` runs the controller and, in M1, the agent half in the same
process (ADR 0001). The two halves keep separate databases, keys and runtimes,
and talk only through the `AgentClient` interface (section 2a), so M2 replaces
the in-process client with the mTLS transport and changes nothing else.

| Item | Production (default) | `--dev` |
|---|---|---|
| Controller directory | `/var/lib/darkpanel/controller/` | `$XDG_STATE_HOME/darkpanel-dev/controller/` |
| Agent directory | `/var/lib/darkpanel/agent/` | `$XDG_STATE_HOME/darkpanel-dev/agent/` |
| Controller key | `/etc/darkpanel/controller.key` | `$XDG_STATE_HOME/darkpanel-dev/keys/controller.key` |
| Agent key | `/etc/darkpanel/agent.key` | `$XDG_STATE_HOME/darkpanel-dev/keys/agent.key` |
| Controller socket | `/run/darkpanel-controller/controller.sock` | `$XDG_RUNTIME_DIR/darkpanel-dev/controller.sock` |

`$XDG_STATE_HOME` defaults to `~/.local/state`; when `$XDG_RUNTIME_DIR` is
unset, the socket goes under the state directory as `run/controller.sock`.
In M1, `serve` creates missing directories (0700) and key files (0600) in both
modes. The M2 install epic takes over creating them and splits the
`darkpanel` and root users; until then a production-mode `serve` runs as root.
All paths come from `paths.Resolve(dev bool)` in `internal/paths`, and every
component takes a `paths.Paths` value, so tests pass temporary paths.

The build version and commit move from `package main` to
`internal/buildinfo` (`Version`, `Commit`), set by
`-X github.com/darkraise/darkpanel/internal/buildinfo.Version=...`, so
`/healthz` and the host-information module can read them.

`serve` flags: `--dev`, and `--listen` (repeatable, section 13). There is no
configuration file in M1.

### 2. Registry and module contract (R5, R6, R7, R9)

Package `internal/core` defines the contract (section 2a has the signatures).
Modules are listed once in `internal/modules/modules.go`
(`All() []core.Module`), the single registration line R5 allows.
`internal/registry.Build() (*core.Registry, error)` returns the registry of
the core module (`internal/controller/coreactions.Module()`) plus
`modules.All()`; the server, the CLI, the schema generator and the tests all
call it, so no component assembles the registry its own way.

- **Manifest.** `Module.Manifest()` returns id, version, actions, resources,
  settings type (nil allowed), preflight checks, owned paths and marked blocks
  (ADR 0009), an uninstall hook (nil until M2), embedded migrations per side,
  and `DefaultEnabled`.
- **Actions** are defined with generic constructors (`DefineRead`,
  `DefineMutating`, `DefineRevertible`) that return a type-erased
  `core.ActionDef` working on `json.RawMessage`. Handlers implement `Plan`,
  `Apply`, `Verify`, and for revertible actions `Snapshot` and `Revert`; read
  actions implement only `Read`.
- **Resources** are read endpoints without input, `DefineResource[Out]`; a
  resource name is `<module>.<resource>` and its schema is fixed at `v1` (a
  breaking change takes a new resource name; ADR 0002's additive rule applies).
- **Env** passed to handlers carries the host id, the calling principal, the
  side's database (for the module's own `<module>_` tables), the exec and file
  providers, the sealer, a clock and a logger.
- **Registration rules,** each with a test; a violation stops startup:
  action names match `^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$` and
  start with their module's id; module ids are not reserved CLI names
  (section 15); names and versions are unique; a compensable action names an
  existing compensating action; break-glass actions are controller-side; a
  revertible action is agent-side, because controller-side actions have no
  revert timer (ADR 0004).
- **Core module.** Actions owned by the core use module id `core`; the CLI
  exposes them without the module segment (section 15).
- **Enablement and preflight (R9).** `core_module_state` (controller database)
  records per host and module: enabled, available, reason, checked at. The
  agent half reports, in `Hello`, each module's availability after its
  migrations and preflight checks; `core.module.enable` runs that module's
  preflight again through `AgentClient.Preflight`. The dispatcher writes
  `core_module_state` from `Hello`; `core.module.enable` and
  `core.module.disable` write it inside their own change. On first start the assembly seeds the local
  host's rows from `DefaultEnabled`. A module that is disabled, failed its
  migration or failed preflight is unavailable: its actions and resources
  answer `module.unavailable` with the reason, and every other module keeps
  working.

### 2a. Foundation interfaces

The foundation (P0) writes these in `internal/core` and
`internal/core/coretest` (fakes: clock with manual advance, sealer, validator,
journal, agent client). They are P0's acceptance criterion; later parts code
against them and may not change them without a plan-time ruling.

```go
type Side string  // "controller", "agent"
type Class string // "read", "revertible", "compensable", "irreversible"
type Scope string // "read", "operate", "admin"

type ActionRef struct { Name string; Version int }

type Principal struct {
    Actor   string // shared-contract actor, e.g. "user:<id>", "token:<id>", "local:root@<host id>", "system:recovery"
    UserID  string // empty for local and system actors
    TokenID string // set only for token principals
    Scope   Scope  // local break-glass and system actors carry "admin"
    Source  string // client address, or "socket"
}

type Env struct {
    HostID    string // empty for controller-side actions
    Principal Principal
    DB        *store.DB
    Exec      Runner
    Files     FileWriter
    Sealer    Sealer
    Clock     Clock
    Log       *slog.Logger
    Agents    HostDirectory // controller side only; nil on the agent
}

type HostDirectory interface {
    Agent(ctx context.Context, hostID string) (AgentClient, error) // implemented by dispatch
}

type DiffLine struct { Op string; Text string } // Op: "+", "-" or " "
type Guard struct { ID string; OK bool; Message string }
type PlanOutcome struct {
    Diff         []DiffLine
    Guards       []Guard
    StateHash    string
    Confirmation string // irreversible actions only: the phrase apply must repeat
}

type ActionSpec struct {
    Name, Summary string
    Version       int
    Class         Class
    Side          Side
    BreakGlass    bool
    LockKeys      []string
    CompensatedBy string // compensable only, an action name
}

type Reader[In, Out any] interface { Read(ctx context.Context, env Env, in In) (Out, error) }
type Mutator[In, Out any] interface {
    Plan(ctx context.Context, env Env, in In) (PlanOutcome, error)
    Apply(ctx context.Context, env Env, in In) (Out, error)
    Verify(ctx context.Context, env Env, in In, out Out) error
}
type Reverter[In, Out any] interface {
    Mutator[In, Out]
    Snapshot(ctx context.Context, env Env, in In) ([]byte, error)
    Revert(ctx context.Context, env Env, snapshot []byte) error
}

func DefineRead[In, Out any](spec ActionSpec, h Reader[In, Out]) ActionDef
func DefineMutating[In, Out any](spec ActionSpec, h Mutator[In, Out]) ActionDef
func DefineRevertible[In, Out any](spec ActionSpec, h Reverter[In, Out]) ActionDef
func DefineResource[Out any](name string, read func(ctx context.Context, env Env) (Out, error)) ResourceDef

type PreflightCheck struct { ID string; Run func(ctx context.Context, env Env) error } // the error text is the reason
type OwnedPath struct { Path string; Kind string; BlockIDs []string; CommentPrefix string } // Kind: "file", "block"

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
type Module interface { Manifest() Manifest }

func NewRegistry(mods ...Module) (*Registry, error) // applies the registration rules
// Registry methods: Action(ActionRef) (ActionDef, bool), Actions() []ActionDef,
// Resource(name string) (ResourceDef, bool), Modules() []Manifest.

type Sealer interface {
    Seal(ad string, plaintext []byte) ([]byte, error)
    Open(ad string, ciphertext []byte) ([]byte, error)
    Derive(label string) []byte
}
type Validator interface {
    ValidateInput(def ActionDef, raw json.RawMessage) error // *errcode.Error with field errors
}
type Clock interface {
    Now() time.Time
    AfterFunc(d time.Duration, f func()) Timer
}
type Timer interface { Stop() bool }

type Cmd struct { Path string; Args []string; Stdin []byte }
type CmdResult struct { Stdout, Stderr []byte; ExitCode int }
type Runner interface { Run(ctx context.Context, cmd Cmd) (CmdResult, error) }
type FileWriter interface {
    WriteFile(ctx context.Context, module, path string, data []byte, mode fs.FileMode) error
    ReplaceBlock(ctx context.Context, module, path, blockID string, content []byte) error
    RemoveBlock(ctx context.Context, module, path, blockID string) error
    Remove(ctx context.Context, module, path string) error
}
type OwnedEntry struct { Path, Module, Kind, BlockID string; CreatedAt time.Time }
type Ownership interface {
    Record(ctx context.Context, e OwnedEntry) error
    Forget(ctx context.Context, module, path, blockID string) error
    List(ctx context.Context) ([]OwnedEntry, error)
}

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
type Journal interface {
    Append(ctx context.Context, tx *sql.Tx, e JournalEntry) error // in the change's transaction
}

type AuditEntry struct {
    ID, Actor, HostID, Action, Phase, ChangeID, Result, ErrorCode, Source string
    At      time.Time
    Version int
    Seq     int64 // the agent journal sequence, 0 for controller-side rows
    Input   json.RawMessage
}
type AuditQuery struct { Limit int; Before, Actor, HostID, Action string }
type Audit interface {
    Journal                                                              // controller-side changes
    Record(ctx context.Context, e AuditEntry) error                      // logins and logouts
    Ingest(ctx context.Context, hostID string, entries []JournalEntry) error // deduplicated by host and seq
    List(ctx context.Context, q AuditQuery) (entries []AuditEntry, nextBefore string, err error)
}

type PlanView struct {
    PlanID, HostID, Action, InputHash, Confirmation string
    Version   int
    Diff      []DiffLine
    Guards    []Guard
    ExpiresAt time.Time
}
type ChangeError struct { Code, Message string }
type ChangeView struct {
    ID, HostID, Action, Actor, CompensatedBy string
    Version        int
    Class          Class
    State          string // section 3
    CreatedAt      time.Time
    UpdatedAt      time.Time
    RevertDeadline *time.Time
    Output         json.RawMessage // secret fields present only in the apply response that created them
    Error          *ChangeError
}
type ApplyOptions struct { ConfirmTimeout time.Duration; Confirmation string; ConfirmNow bool }
type ChangeFilter struct { State, HostID, Before string; Limit int }

type ModuleStatus struct { ID string; Version int; Available bool; Reason string }
type AgentHello struct {
    ProtocolVersion int
    BinaryVersion   string
    OS              string
    HostID          string
    Modules         []ModuleStatus
}

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

// Dispatcher is what the HTTP API and the socket call. hostID is empty for
// controller-side actions.
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

`store.DB` (in `internal/core/store`) wraps one writer `*sql.DB` (a single
connection, immediate transactions) and a reader `*sql.DB` pool, with
`Write(ctx, func(*sql.Tx) error) error` and `Read() *sql.DB`.

### 3. Change lifecycle engine (R10)

Package `internal/core/lifecycle` implements ADR 0004 once and is used by both
sides: the agent runtime for agent actions and the controller for
controller-side actions. Its API is `Read`, `Plan`, `Apply`, `Confirm`,
`Revert`, `Change`, `Changes` and `Recover`, mirroring `AgentClient`.

- `Plan` validates the input against the action's schema, runs the handler's
  `Plan`, and stores a plan record with the canonical input sealed (section 5).
  It returns a `PlanView` with a 10-minute expiry.
- `Apply` follows ADR 0004's ordering. Step 1 re-runs the handler's `Plan` with
  the stored input and refuses with `plan.state_changed` when the state hash
  differs or a guard that passed now fails; a failing guard refuses with
  `plan.guard_failed` (overrides arrive with the M3 lockout guard). Then it
  takes the lock keys (`lock.held` when another change owns one), persists
  intent and, for a revertible action, the snapshot and revert deadline in a
  committed transaction before touching the host, mutates, verifies, and
  records the result. Once a plan has a result, its sealed input is cleared
  and only its hash kept.
- `opts.ConfirmTimeout` (revertible; default 60 s, allowed 10 s to 15 min);
  `opts.Confirmation` (irreversible; must equal the plan's phrase exactly, else
  `plan.confirmation_required`); `opts.ConfirmNow` confirms right after a
  successful verify (the CLI's `--confirm`).
- Apply requires the principal that made the plan (`plan.principal_mismatch`).
  Confirm and revert accept any principal whose scope allows the action, so an
  admin can revert someone else's change.
- A second apply of a plan that already has a change returns that change's
  current view, whatever its state; it never acts twice.
- Revert timers use `Clock.AfterFunc` on the monotonic clock. Timer reverts
  use actor `system:revert-timer`; recovery uses `system:recovery`.
- Expired plans are deleted on start and every 10 minutes.

**State transitions.** Every transition commits the change row and its journal
entry in one transaction (section 11).

| From | Event | To | Locks | Journal phase, result |
|---|---|---|---|---|
| (plan) | apply passes step 1 and intent is persisted | `applying` | taken | none |
| `applying` | mutate and verify succeed, revertible | `pending` | held, timer armed | `apply`, ok |
| `applying` | mutate and verify succeed, compensable or irreversible | `completed` | released | `apply`, ok |
| `applying` | mutate or verify fails, revertible; revert succeeds | `reverted` | released | `apply`, error |
| `applying` | mutate or verify fails, revertible; revert fails | `failed` (`change.revert_failed`) | released | `revert`, error |
| `applying` | mutate or verify fails, compensable or irreversible | `failed` | released | `apply`, error |
| `pending` | confirm (wins the race) | `confirmed` | released | `confirm`, ok |
| `pending` | revert or timer (wins the race) | `reverted`, or `failed` if the revert fails | released | `revert`, ok or error |
| `applying` or `pending` at start, revertible | recovery reverts from the snapshot | `reverted` or `failed` | released | `recover`, ok or error |
| `applying` at start, compensable or irreversible | recovery | `interrupted` (verify output kept) | released | `interrupt`, error |

Confirm and revert race in one transaction; the loser gets
`change.not_pending`. A repeated apply of an interrupted plan returns the
interrupted change. `change show` prints the compensating action of an
interrupted compensable change (ADR 0004). Plan, snapshot and journal records
carry `format: 1`.

The **input hash** is SHA-256 over the typed input struct re-encoded with
`encoding/json`, with every secret field replaced by `"[secret]"`; the API and
audit expose it as ADR 0004 requires, and nothing about a secret leaks
through it.

The engine is tested with a fixture module registered only in tests: one
action per class, a revertible action whose verify fails, one whose revert
fails, confirm and revert racing, timer expiry with the fake clock, a recovery
test that reopens the database mid-apply, a repeated apply and the typed
confirmation. Host-information has no mutating action.

**Dispatch.** Package `internal/controller/dispatch` implements `Dispatcher`.
Controller-side actions go to the controller's engine. Agent actions go through
`AgentClient`; every agent apply is recorded in `core_change_index` (change id,
host id, action, version, created at), so `Change`, `Confirm` and `Revert` find
the owning host, and `Changes` merges the controller engine's changes with each
host's `AgentClient.Changes`. In M1 the only `AgentClient` is the in-process
client over the agent runtime (`internal/agent/runtime`), which owns
`agent.db`, its engine, migrations, preflight and resource reads. The
dispatcher handles `Hello` and keeps `core_module_state` current.

### 4. Storage and migrations (R8)

Package `internal/core/store` opens SQLite as ADR 0005 specifies. The writer
connection string sets `_pragma=journal_mode(WAL)`,
`_pragma=synchronous(FULL)`, `_pragma=foreign_keys(1)`,
`_pragma=busy_timeout(5000)` and `_txlock=immediate`, so every commit reaches
disk before the host is touched (ADR 0004) and a write transaction waits for
the lock instead of failing. Readers use the same pragmas without `_txlock`.

- Migrations are embedded SQL files named `NNNN_<name>.sql` per module and
  side, each run in a transaction and recorded in `core_migrations` (module,
  side, number, name, checksum SHA-256, applied at), which the runner creates
  itself. Core runs first, then modules in registration order.
- Before applying any pending migration, the store copies the database with
  `VACUUM INTO` to `backups/<from>-<to>-<UTC timestamp>/` in the role's
  directory (mode 0600, the last five kept). `<from>` is `core_meta`'s
  `binary_version` (or `none` on a new database) and `<to>` the running binary
  version; `binary_version` is written after every successful migration run. No
  backup is taken when nothing is pending.
- A changed checksum or a failed core migration stops startup. A failed module
  migration marks that module unavailable with the error (section 2).
- Rows numbered above the binary's highest migration are ignored (ADR 0005).
- The schema version reported by `/healthz` is the highest applied core
  migration number of the controller database.

Core migrations live in `internal/core/store/migrations/<side>/`. Each file has
one owning part, so parallel parts never edit the same file:

| Side | File | Owner | Tables |
|---|---|---|---|
| controller | `0001_core.sql` | P0 | `core_meta` (key, value: `controller_id`, `local_host_id`, `tailscale_addrs`, `binary_version`), `core_hosts` (id, name, is_local, created_at), `core_module_state` (host_id, module_id, enabled, available, reason, checked_at; primary key host and module), `core_ca` (id, purpose `web`, cert_pem, key_ct, created_at) |
| controller | `0002_lifecycle.sql` | P4 | `core_plans`, `core_changes`, `core_locks` |
| controller | `0003_identity.sql` | P5 | `core_users` (id, username unique, role, state `pending` or `active`, password_hash, totp_secret_ct, totp_last_step, created_at, updated_at), `core_sessions` (id_hash, user_id, created_at, last_seen_at, expires_at, source_ip, user_agent), `core_tokens` (id, user_id, name, scope, secret_hash, created_at, expires_at, last_used_at, revoked_at) |
| controller | `0004_audit.sql` | P11 | `core_audit` (section 11), `core_journal_cursor` (host_id, acked_seq), `core_change_index` |
| agent | `0001_core.sql` | P0 | `core_meta` |
| agent | `0002_lifecycle.sql` | P4 | `core_plans`, `core_changes`, `core_locks`, `core_journal` (seq integer primary key autoincrement, at, actor, action, version, phase, change_id, input_json, result, error_code) |
| agent | `0003_ownership.sql` | P2 | `core_owned_files` (path, module, kind `file` or `block`, block_id, created_at) |

P4 chooses the columns of the lifecycle tables; they are read only through its
engine. Entity ids (hosts, users, tokens, plans, changes, audit rows) are ULID
text primary keys; `core_sessions` is keyed by the hex SHA-256 of the session
id, and `core_journal` by its integer sequence. Times are RFC 3339 UTC text.

### 5. Secrets and keys (R12)

Package `internal/core/secret`:

- **Key files** hold 32 random bytes as `darkpanel-key-v1:<base64>`. Creation
  writes a temporary file in the target directory, fsyncs it, `link(2)`s it to
  the final name (so an existing key is never replaced; on `EEXIST` the
  existing file is loaded), unlinks the temporary name and fsyncs the
  directory. Loading refuses a file whose mode allows group or other access or
  whose owner is not the process's effective user.
- **Sealer.** `Seal` and `Open` use XChaCha20-Poly1305 with a random 24-byte
  nonce. The ciphertext layout is version byte `1`, an 8-byte key id (the first
  8 bytes of SHA-256 of the key), the nonce, then the sealed data. The
  associated data is `<table>.<column>.<row id>` (ADR 0005). Plan inputs and
  snapshots are sealed whole.
- **Subkeys.** `Derive(label)` returns an HKDF-SHA256 subkey of the role key;
  the CSRF key uses label `darkpanel csrf v1`.
- **Schemas mark secrets.** The schema reflector (section 7) turns the
  `secret:"true"` tag into `"x-secret": true` on the property, plus
  `"writeOnly": true` on input properties. `secret.RedactJSON(raw, schema)`
  resolves `$ref` into `$defs`, walks objects, arrays (element by element) and
  `oneOf`/`anyOf` branches, and replaces every marked value with
  `"[redacted]"`; a value whose schema it cannot resolve is redacted whole
  (fail closed). Audit, journal, logs, diffs and error messages pass through
  it.
- **Secret outputs.** An output field tagged `secret:"true"` (a new API token,
  a TOTP enrollment secret) is returned only in the apply response that
  created it. The stored change result, journal and audit carry it redacted, so
  a repeated apply after a lost response returns `"[redacted]"`; the UI's copy
  dialog and the CLI's printout are the only chance to keep it. This is how the
  write-only rule of the shared contracts applies to outputs.

TOTP secrets are stored sealed in `core_users.totp_secret_ct`. M1 has no
third-party credentials yet; the first ones arrive with M5 and M6 and use the
same sealer.

### 6. Host execution providers (R11)

Package `internal/platform` implements ADR 0009:

- `exec` implements `core.Runner`: only binaries whose absolute path is in its
  allowlist, argument vectors, the environment
  `PATH=/usr/sbin:/usr/bin:/sbin:/bin`, `LANG=C.UTF-8`, `LC_ALL=C.UTF-8` and
  nothing else, and the caller's context deadline (a call without a deadline
  gets 30 s). Output above 4 MiB on either stream stops the program and fails
  with `exec.output_too_large`. Callers separate values that could be read as
  options with `--` where the tool supports it (ADR 0009), and input schemas
  carry patterns for every value passed on. The production allowlist is empty
  in M1; later epics add entries such as `/usr/sbin/ufw`.
- `files` implements `core.FileWriter` for paths and marked blocks declared in
  the calling module's manifest. It opens the parent with
  `openat2(RESOLVE_NO_SYMLINKS)`, requires the parent to be owned by root (by
  the effective user when built with `TestMode`) and not writable by group or
  other, reads an existing target only with
  `openat(dirfd, name, O_RDONLY|O_NOFOLLOW|O_CLOEXEC)` and requires a regular
  file, writes a temporary file, fsyncs, renames it over the target relative to
  the directory descriptor, fsyncs the directory, and keeps an existing
  target's mode and owner.
- Marked blocks are delimited by `<comment> BEGIN DARKPANEL <module>/<block>`
  and `<comment> END DARKPANEL <module>/<block>` lines (comment prefix declared
  per path, default `#`). `ReplaceBlock` appends the block when absent and
  replaces it when present; `RemoveBlock` deletes it. A file with duplicated,
  nested or unterminated markers is refused with `files.markers_invalid`.
- Every write records the path or block through `core.Ownership`; P2 also
  implements `Ownership` over the agent's `core_owned_files`, for
  `darkpanel uninstall` (M2).

### 7. Schemas, compatibility and OpenAPI (R6, R7)

Package `internal/core/schema`:

- **Reflection** with invopop: every field is required unless its JSON tag has
  `omitempty`; input objects get `additionalProperties: false` so an unknown
  input field is rejected rather than ignored (ADR 0002's safety concern);
  output objects allow additional properties. Patterns, enums, minimum and
  maximum come from `jsonschema` struct tags; every value later passed to a
  program must carry a pattern (ADR 0009).
- **Validation** implements `core.Validator`: each schema compiled once with
  santhosh-tekuri, errors returned as `{path, message}` items for the problem
  details `errors` field.
- **Snapshots.** `go run ./internal/core/schema/schemagen` (Make target
  `schemas`) builds the registry with `registry.Build()` and writes
  `schemas/<action>/v<n>.json` with
  `{name, version, class, side, breakGlass, input, output}` and
  `schemas/<module>.<resource>/v1.json` with `{name, output}`. A test in
  `internal/modules` regenerates them in memory and fails when a committed file
  differs or is missing.
- **Compatibility.** `schema.Compare(old, new)` reports ADR 0002 violations
  (removed or renamed field, changed type, default or constraint, new required
  input field, removed enum value, changed class). A test runs it for every
  file under `schemas/released/<tag>/`; until the first release that directory
  is empty, so the checker is proven with fixture pairs, one per rule.
- **OpenAPI 3.1.** `schema.OpenAPI(registry, routes)` builds the document
  served at `/api/v1/openapi.json` from a route list the API package passes in
  (the hand-written routes of section 12) plus one plan, apply or read
  operation per action and one get operation per resource, reusing the action
  schemas. Break-glass actions are left out. An assembly test checks that every
  pattern registered on the network mux appears in the document.

### 8. Users, passwords and TOTP (R15, R19)

Package `internal/controller/auth`:

- **Usernames** match `^[a-z0-9][a-z0-9._-]{0,63}$`.
- **Passwords** are hashed with argon2id, memory 64 MiB, 3 iterations,
  parallelism 1, 16-byte salt, 32-byte key, stored as a PHC string so the
  parameters can change later. At most two hashes run at once; a third caller
  waits on its request context. Passwords must be 12 to 128 characters; there
  are no composition rules.
- **TOTP** follows RFC 6238: SHA-1, 6 digits, 30-second step, 20-byte random
  secret. A code is accepted for the current step and one step either side.
  The step of the last accepted code is stored, and a code for that step or an
  earlier one is refused, so a code cannot be replayed. The enrollment URI is
  `otpauth://totp/Darkpanel:<username>?secret=<base32>&issuer=Darkpanel&algorithm=SHA1&digits=6&period=30`,
  built by the CLI from the secret; the API never returns it.
- **Roles (R19).** `core_users.role` exists and only `admin` is accepted.
- **User states.** A user is `pending` until a TOTP code confirms enrollment,
  then `active`. A pending user cannot log in. `reset-totp` returns the user to
  `pending` with a new secret.
- The package exposes login, logout, request authentication, CSRF tokens,
  user operations (create, activate, reset TOTP, reset password, delete,
  list), token operations (create, list, revoke) and a purge of expired
  sessions and tokens; P9 and P12 consume it after wave 1 merges.

### 9. Sessions, CSRF, rate limits and API tokens (R16, R17)

- **Login** is one request with username, password and TOTP code. A username
  that fails the pattern is refused before anything else. Every failure returns
  the same `auth.invalid_credentials` problem. An unknown username still runs
  one argon2id hash against a fixed dummy hash, so timing does not reveal
  whether a user exists.
- **Rate limits** (in memory): per username, 5 failures in 15 minutes lock
  that username for 15 minutes; per source address, 20 failures in 15 minutes
  block that address for 15 minutes. A blocked attempt returns
  `auth.rate_limited` with `Retry-After` and is not evaluated. Counters older
  than 15 minutes are pruned on each insert, and at most 10,000 keys are kept
  (the oldest dropped first).
- **Sessions.** A 32-byte random id travels in a cookie (HttpOnly,
  SameSite=Strict, Path=/, no Domain); the database stores only its SHA-256.
  On HTTPS listeners the cookie is `__Host-darkpanel_session` with `Secure`; on
  the plain-HTTP loopback listener it is `darkpanel_session` without `Secure`,
  because browsers do not all accept Secure cookies over plain HTTP, and
  loopback traffic never leaves the host. A session ends after 1 hour without a
  request or 12 hours after login, whichever comes first; `last_seen_at` is
  updated at most once a minute, the only state a GET request changes. Logout,
  `reset-totp`, `reset-password` and `user delete` end the user's sessions.
- **CSRF.** The token is HMAC-SHA256 of the session id under the derived CSRF
  key, so nothing extra is stored. Login and `GET /api/v1/auth/session` return
  it; the client sends it as `X-CSRF-Token` on every request whose method is not
  GET, HEAD or OPTIONS, and the middleware compares in constant time. Requests
  authenticated with an API token skip the CSRF check.
- **Credential precedence.** When an `Authorization` header is present, only
  the bearer token is considered: an invalid or expired token gets
  `auth.required` and the cookie is never consulted. `/healthz` ignores invalid
  credentials and returns status only.
- **API tokens.** Format `dpt_<id>_<secret>` where `<id>` is the token's ULID
  and `<secret>` is 32 random bytes in base32 without padding; the database
  stores SHA-256 of the secret. Scopes are `read`, `operate` and `admin`. Every
  token expires: 1 to 365 days, default 90. Only an `admin` session or `admin`
  token creates tokens. Listing and revoking cover the tokens of the
  principal's user. Deleting a user revokes its tokens. Tokens are sent as
  `Authorization: Bearer <token>`.
- **Purge.** Expired sessions are deleted on start and every 10 minutes; tokens
  are deleted 30 days after they expire or are revoked.

### 10. Authorization and core actions (R7, R17)

| Scope | Allows |
|---|---|
| `read` | read actions, resources, change views, health details |
| `operate` | `read`, plus plan, apply, confirm and revert of module actions |
| `admin` | `operate`, plus core mutating actions (tokens, module enablement) |

A session of an `admin` user has the `admin` scope. Break-glass actions are
served only on the controller socket; on a network listener they answer
`auth.forbidden` (403) and are absent from the OpenAPI document.

Core actions (all controller-side):

| Action | Class | Input → output |
|---|---|---|
| `core.host.list/v1` | read | `{}` → `{hosts: [Host]}` |
| `core.module.list/v1` | read | `{hostId}` → `{modules: [ModuleState]}` |
| `core.module.enable/v1` | compensable (by `core.module.disable`) | `{hostId, moduleId}` → `ModuleState`; runs preflight; lock `core.module` |
| `core.module.disable/v1` | compensable (by `core.module.enable`) | `{hostId, moduleId}` → `ModuleState`; lock `core.module` |
| `core.token.list/v1` | read | `{}` → `{tokens: [TokenInfo]}` |
| `core.token.create/v1` | compensable (by `core.token.revoke`) | `{name, scope, expiresInDays?}` → `TokenInfo` plus secret `token` |
| `core.token.revoke/v1` | irreversible | `{id}` → `TokenInfo`; phrase is the token id |
| `core.audit.list/v1` | read | `{limit?, before?, actor?, hostId?, action?}` (limit 1–500, default 100) → `{entries: [AuditEntry], nextBefore}` |
| `core.user.list/v1` | read, break-glass | `{}` → `{users: [User]}` |
| `core.user.create/v1` | compensable (by `core.user.delete`), break-glass | `{username, password (secret)}` → `{user: User, totpSecret (secret)}` |
| `core.user.activate/v1` | compensable (by `core.user.reset-totp`), break-glass | `{username, code}` → `User` |
| `core.user.reset-totp/v1` | irreversible, break-glass | `{username}` → `{user: User, totpSecret (secret)}`; phrase is the username |
| `core.user.reset-password/v1` | irreversible, break-glass | `{username, password (secret)}` → `User`; phrase is the username |
| `core.user.delete/v1` | irreversible, break-glass | `{username}` → `{}`; refuses the last active admin; phrase is the username |

A test enumerates the registry and fails when a mutating action has no
generated CLI command (S4 from M1 on).

### 11. Audit (R18)

`core_audit` columns: id (ULID), seq (agent journal sequence, null for
controller-side rows), at, actor, host_id (null for controller-side actions),
action, version, phase (`apply`, `confirm`, `revert`, `recover`,
`interrupt`, `login`, `logout`), change_id, input_json (redacted), result
(`ok` or `error`), error_code, source (client address or `socket`). A unique
index on `(host_id, seq)` makes ingestion idempotent.

- Controller-side actions write their audit row through the engine's
  `Journal`, which P11's audit package implements over `core_audit`, in the
  same transaction as the change result.
- Agent actions journal on the agent side through P4's `core_journal`
  implementation, with the actor the controller passed. P11's ingester (in
  `internal/controller/audit`) reads entries after the host's
  `core_journal_cursor`, writes them to `core_audit`, advances the cursor and
  calls `Ack` (ADR 0003). In M1 ingestion runs right after each dispatched
  apply, confirm or revert and every 5 seconds. The agent deletes acknowledged
  entries; unacknowledged entries are kept up to 10,000, beyond which the
  oldest are dropped and an entry with phase `interrupt` and error code
  `journal.overflow` records the loss.
- Logins (success and failure, without the password or code) and logouts are
  audited with actor `user:<id>`, or `anonymous` with the submitted username
  in input_json when no user matched (ADR 0011).
- Plans and reads are not audited; they change nothing.
- The controller never prunes `core_audit` in v1; retention is a post-v1
  decision the M7 footprint measurement may raise.
- Viewing: the audit page in the UI and `darkpanel audit list`.

### 12. HTTP API (R7, R13, R16)

Package `internal/controller/api`. Every route sits behind one authentication
middleware that denies any method on any path unless the route is in the public
list: `POST /api/v1/auth/login`, `GET /healthz`, and `GET`/`HEAD` of the
embedded UI's static files and its index fallback (they carry no data).
Routes use the standard library router with whole-segment wildcards (ADR 0006).

| Route | Purpose |
|---|---|
| `POST /api/v1/auth/login` | `{username, password, code}` → `{user, csrfToken, expiresAt}` and the session cookie |
| `POST /api/v1/auth/logout` | `204`, clears the cookie |
| `GET /api/v1/auth/session` | `{user, csrfToken, expiresAt}` for a session; `{token: TokenInfo}` for a token |
| `GET /healthz` | `{status: "ok"}`; with a valid session or token also `version`, `commit`, `schemaVersion` |
| `GET /api/v1/hosts/{host}/resources/{module}/{resource}` | a resource's output |
| `POST /api/v1/hosts/{host}/actions/{name}/{version}/{plan,apply,read}` | agent actions |
| `POST /api/v1/actions/{name}/{version}/{plan,apply,read}` | controller-side actions |
| `GET /api/v1/changes`, `GET /api/v1/changes/{id}` | `{changes: [Change]}` (filters `state`, `hostId`, `before`, `limit`), `Change` |
| `POST /api/v1/changes/{id}/confirm`, `.../revert` | `Change` |
| `GET /api/v1/openapi.json` | OpenAPI 3.1 (section 7) |

Hosts are listed through `core.host.list/v1`; there is no separate host route.

**Request bodies:** plan `{input}`; apply `{planId, confirmTimeoutSeconds?,
confirmation?, confirmNow?}`; read `{input}`. Every request with a body must
send `Content-Type: application/json`.

**Response shapes** (camelCase; `null` where a field does not apply):

```
User        {id, username, role, state, createdAt}
TokenInfo   {id, name, scope, createdAt, expiresAt, lastUsedAt, revokedAt}
Host        {id, name, isLocal, online, modules: [ModuleState]}
ModuleState {id, version, enabled, available, reason, checkedAt}
Plan        {planId, hostId, action, version, inputHash,
             diff: [{op, text}], guards: [{id, ok, message}],
             expiresAt, confirmation}
Change      {id, hostId, action, version, class, state, actor, createdAt,
             updatedAt, revertDeadline, output, error: {code, message},
             compensatedBy}
AuditEntry  {id, at, actor, hostId, action, version, phase, changeId,
             input, result, errorCode, source}
Read        {output}
```

Apply returns `Change` with status 200 whatever the change's outcome; an
action that fails after its change exists reports `state: "failed"` and the
error inside `Change.error`. Problem details are for refusals before a change
exists. In M1 `Host.online` is always true for the local host.

**Errors** are RFC 9457 problem details (`application/problem+json`) with
`type: "about:blank"`, `title`, `status`, `detail`, the stable `code` from
`internal/core/errcode`, and `errors` for validation. `internal` never carries
the underlying error text.

| Code | Status | Code | Status |
|---|---|---|---|
| `request.invalid_json` | 400 | `action.unknown` | 404 |
| `request.invalid_input` | 422 | `module.unavailable` | 409 |
| `request.too_large` | 413 | `plan.not_found` | 404 |
| `request.unsupported_media_type` | 415 | `plan.expired` | 410 |
| `method_not_allowed` | 405 | `plan.state_changed` | 409 |
| `auth.required` | 401 | `plan.principal_mismatch` | 403 |
| `auth.invalid_credentials` | 401 | `plan.confirmation_required` | 422 |
| `auth.rate_limited` | 429 | `plan.guard_failed` | 409 |
| `auth.csrf` | 403 | `change.not_found` | 404 |
| `auth.forbidden` | 403 | `change.not_pending` | 409 |
| `not_found` | 404 | `lock.held` | 423 |
| `internal` | 500 | | |

`change.revert_failed`, `exec.output_too_large`, `files.markers_invalid` and
`journal.overflow` are action-level codes: they appear in `Change.error.code`
or in the audit log, not as HTTP statuses.

- **Limits.** Request bodies up to 1 MiB; `ReadHeaderTimeout` 10 s,
  `ReadTimeout` 30 s, `WriteTimeout` 60 s, `IdleTimeout` 120 s.
- **Headers** on every response: `Content-Security-Policy: default-src 'self';
  script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;
  font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none';
  form-action 'self'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`,
  and `Strict-Transport-Security: max-age=31536000` on HTTPS (browsers ignore
  it for IP-address hosts, so it protects nothing until names arrive in M5).
  API responses also carry `Cache-Control: no-store`. `style-src
  'unsafe-inline'` is needed because the UI kit sets inline style attributes;
  scripts stay strict.
- **Static UI.** `web.Dist` is served at `/`; any non-`/api/` GET path without
  a file returns `index.html` so client routes work on reload.

### 13. Listeners and web certificate (R20)

Package `internal/controller/listen`:

- **Defaults** are `loopback` and `tailscale`. `loopback` is plain HTTP on
  `127.0.0.1:8080` and `[::1]:8080` (a host without IPv6 loopback skips the
  second), which also serves SSH port forwarding. `tailscale` is HTTPS on port
  8443 of each Tailscale address.
- **Tailscale addresses** are the addresses of interface `tailscale0` inside
  `100.64.0.0/10` or `fd7a:115c:a1e0::/48`, read every 10 seconds. They are
  saved in `core_meta`, so the next start binds them at once with
  `IP_FREEBIND`/`IPV6_FREEBIND` (ADR 0006) even before tailscaled is up. On a
  change the listener set is rebound. Without any Tailscale address the
  controller serves loopback only, and the UI's footer and `serve`'s log say
  so.
- **`--listen`** replaces the defaults. Values: `loopback`, `tailscale`,
  `https://<ip>:<port>`, or `http://<loopback ip>:<port>`. Plain HTTP on a
  non-loopback address is refused at startup with a clear message.
- **Web authority (ADR 0010).** On first start the controller creates an
  ECDSA P-256 root (10 years) for web certificates only and stores its key
  sealed in `core_ca` (purpose `web`). It issues an ECDSA P-256 leaf (90 days,
  renewed after 60 days, on any Tailscale address change and on a change of
  HTTPS bind addresses) with SANs for the hostname, `localhost`, the Tailscale
  IP addresses and every explicit HTTPS bind address. The MagicDNS name needs
  the Tailscale API and arrives with M5. The leaf lives in memory only. HTTPS
  listeners present the chain `[leaf, root]`.
- `darkpanel controller web-ca` prints the root certificate in PEM and its
  SHA-256 fingerprint through the controller socket, so the operator can trust
  it in a browser and pin it in the CLI.

### 14. Controller socket and break-glass (R15)

`internal/controller/api` also serves the controller socket with its own mux:
`GET /healthz` (full detail), `GET /api/v1/controller/web-ca`, and the plan,
apply and read routes of controller-side actions marked break-glass. The socket
file is mode 0600; each connection is checked with `SO_PEERCRED` and accepted
only from uid 0 or the service's own uid. The actor is
`local:root@<host id>` for uid 0; in `--dev` mode a non-root caller is
`local:uid-<n>@<host id>` (ADR 0011). No session, token or CSRF applies on the
socket; the peer credentials are the authentication.

### 15. CLI (R7, R17)

Package `internal/cli`, generated from `registry.Build()` with cobra:

- **Names.** A module action `<module>.<noun>.<verb>` is
  `darkpanel <module> <noun> <verb>`; a core action is
  `darkpanel <noun> <verb>`; a resource `<module>.<resource>` is
  `darkpanel <module> <resource> get`. Reserved top-level names, which no
  module id may use: ADR 0006's `serve`, `agent`, `update`, `user`,
  `controller`, `change`, `uninstall`, `version`, plus `host`, `module`,
  `token`, `audit`, `help` and `completion` (ADR 0011).
- **Input.** Scalar fields (string, integer, number, boolean, enum, arrays of
  scalars) become `--kebab-case` flags; anything nested comes from
  `--input <file|->` as JSON. Secret fields are prompted without echo on a
  terminal, or read one line each from stdin, in schema order, when stdin is
  not a terminal; there is never a flag for them.
- **Host.** `--host <id or name>`; when omitted and exactly one host exists it
  is used, otherwise the command fails listing the hosts.
- **Mutating flow.** Plan, print the diff and guards, ask `Apply? [y/N]`
  (`--yes` answers this question only), for an irreversible action require the
  confirmation phrase typed or given with `--confirmation <phrase>`, then
  apply. A revertible change prints its id and revert deadline; it is confirmed
  only by `--confirm` (sent as `confirmNow`) or `darkpanel change confirm <id>`.
- **Output.** Tables for lists, `key: value` lines otherwise, `--json` for the
  raw output. Exit codes: 0 success, 1 error, 2 usage, 3 refused by a guard or
  a declined prompt, 4 authentication.
- **Connection.** URL from `--url`, then `DARKPANEL_URL`, then the config
  file, then `http://127.0.0.1:8080`. Token from `DARKPANEL_TOKEN`, then the
  config file `$XDG_CONFIG_HOME/darkpanel/cli.json`
  (`{url, token, caFingerprint}`, refused if group or other can read it).
  The config file's token is used only when the chosen URL has the same
  scheme, host, port and path (ignoring a trailing slash) as the file's URL,
  or as `http://127.0.0.1:8080` when the file names no URL, so a `--url` or
  `DARKPANEL_URL` naming another server never receives it. The CLI refuses,
  with a usage error, to send any token over plain `http` to a host other than
  `localhost` or a loopback IP address (amended 2026-10-19 by the owner,
  register row 91).
- **HTTPS verification.** Without `caFingerprint` the CLI verifies against the
  system roots. With it, the CLI disables default verification and, in
  `VerifyPeerCertificate`, finds the presented certificate whose SHA-256 over
  its DER equals the fingerprint, requires it to be a CA, uses it as the only
  root, and verifies the leaf against it for the dialled host name or IP. Leaf
  renewals therefore need no change on the CLI.
- **Break-glass.** Break-glass actions and `controller web-ca` use the
  controller socket (`--socket` overrides; `--dev` resolves the dev path).
  `darkpanel user create --username <name>` prompts for the password twice,
  applies `core.user.create`, builds the `otpauth://` URI (section 8), renders
  it as a terminal QR code with the URI and secret printed below it, prompts for
  a code and applies `core.user.activate`. `reset-totp` runs the same
  enrollment after the reset.
- **Hand-written commands:** `serve`, `version`,
  `change list|show|confirm|revert`, `controller web-ca`.

### 16. Host-information module (R13)

Module `hostinfo`, version 1, enabled by default, agent-side, no actions, no
migrations, one resource `hostinfo.summary` (v1):

```
hostname, os {id, versionId, prettyName}, kernel, arch, uptimeSeconds,
bootTime, cpu {model, logicalCount}, memory {totalBytes, availableBytes},
swap {totalBytes, freeBytes}, load {one, five, fifteen},
rootFs {totalBytes, availableBytes}, darkpanelVersion
```

Sources: `os.Hostname`, `/etc/os-release`, `/proc/sys/kernel/osrelease`,
`/proc/uptime`, `/proc/cpuinfo`, `/proc/meminfo`, `/proc/loadavg` and
`statfs("/")`. Files are read through an `fs.FS` rooted at `/` and `statfs`
through an injected function, so tests use fixtures. `cpu.model` is the
`model name` line, else the `Hardware` line, else `CPU implementer` and
`CPU part` as `<implementer>/<part>` (arm64 cloud images), else `unknown`.
The parsers have fixture tests for each supported OS on amd64 and arm64 and a
fuzz target each (R74 starts here). Preflight checks that `/proc/meminfo` is
readable. On first start `serve` creates the local host record (name from the
hostname, `is_local` true) and the agent half uses its id.

### 17. Web UI (R13, R14)

Direction A from the foundations register: darkraise-ui's quiet style, classic
shell, zinc surface, blue accent, compact density, Hanken Grotesk and JetBrains
Mono (self-hosted, no external requests). Exact theme values are set at plan
time against darkraise-ui 6.9.6.

- **Routing.** react-router in declarative mode with a darkraise-ui router
  adapter built on `NavLink`. Routes: `/login`, `/` (hosts overview),
  `/hosts/:hostId/:moduleId/*` (module pages), `/audit`, `/settings/tokens`.
  Every route except `/login` sits behind a session guard that redirects to
  `/login` and returns afterwards.
- **Module route registry (R14).** `web/src/modules/registry.ts` lists one
  entry per module: id, label, icon and a lazily imported route bundle that
  exports its host routes. Adding a module adds one line there and a folder
  `web/src/modules/<id>/`; no other module's files change. The host page builds
  its tabs from the registry for the host's enabled modules; an enabled but
  unavailable module shows its tab with the reason instead of its pages (R9).
- **API client.** One `fetch` wrapper sends `credentials: "same-origin"` and
  `Content-Type: application/json`, adds `X-CSRF-Token` on unsafe methods,
  parses problem details, and on `auth.required` clears the session and goes to
  `/login`. TanStack Query caches reads. Request and response types are
  hand-written in `web/src/api/types.ts` from section 12.
- **Pages.** Login (username, password, 6-digit code; one generic error; the
  wait time on `auth.rate_limited`). Hosts overview (`core.host.list`: name,
  local marker, module states). Host-information page (the summary as labelled
  values with human-readable sizes and uptime). Audit (`core.audit.list`: time,
  actor, host, action, result; older entries by `nextBefore`). Tokens (list;
  create dialog with name, scope and expiry that shows the token once with a
  copy button and says it will not be shown again; revoke with the typed
  confirmation).
- **Pre-paint script** moves from `web/index.html` into
  `web/public/theme-init.js` loaded with a plain `<script src>`, so the CSP
  needs no inline script, and it falls back to `dark` to match `themeConfig`
  (tooling register row 11).
- Tests: Vitest with Testing Library and a stubbed `fetch`, for the login
  flow, the session guard, CSRF header handling, the registry-driven tabs
  including an unavailable module, the host-information page and the token
  dialog.

### 18. Integration harness (R21)

Under `test/harness/`, in Go behind the build tag `harness`, run with
`go test -tags harness ./test/harness/ -args -os <id>`. The package path names
the root package alone because `go test` passes `-args` to every test binary
its patterns match, and only the root package defines `-os`; without `-args`,
`go test -tags harness ./test/harness/...` runs the driver's unit tests and
skips the VM tests (amended 2026-10-19 at plan time):

- **Images.** `images.json` pins Ubuntu 22.04 and 24.04 and Debian 12 and 13
  cloud images by dated release URL and SHA-256 (never a `current` link); the
  scanner uses the Debian 13 image. Downloads are verified before use and
  cached between runs.
- **VMs.** QEMU with KVM, a qcow2 overlay per run, and a cloud-init NoCloud
  seed built with `cloud-localds`. Each VM has two NICs: a user-mode
  management NIC with a host port forward for SSH (and outbound access for
  package installs), and an "internet" NIC on a QEMU
  `-netdev socket,mcast=230.0.0.1:<port>` link shared by the target and the
  scanner, with a port unique to the job. The link needs no bridge, tap device
  or root on the runner. cloud-init `network-config` v2 matches each NIC by MAC
  address, because interface names differ between Ubuntu and Debian images. The
  target is `198.18.0.10/15` and `2001:db8:d0::10/64`; the scanner is `.20` and
  `::20` (ADR 0008).
- **Provisioning** through cloud-init: on the target, UFW installed and
  enabled with `allow 22/tcp`, Docker Engine from Docker's apt repository and
  tailscaled from Tailscale's stable repository, both pinned to exact versions
  at plan time (tailscaled is installed and running but not logged in until
  M5); on the scanner, `nmap`. The packages `docker-ce`, `docker-ce-cli`,
  `containerd.io` and `tailscale` are downloaded as `.deb` files from those
  repositories' pools by pinned URL, checked against their pinned SHA-256 and
  installed from the local files; no Docker or Tailscale apt source is
  configured (amended 2026-10-19 by the owner's ruling to pin packages by URL
  and SHA-256). When a pinned package has left its repository,
  provisioning fails with `pinned package <name>=<version> missing; update the
  harness pins`, never with a silent upgrade.
- **Driver API** (`vm` package): `Boot`, `Run` (SSH with a per-run Ed25519
  key, through `golang.org/x/crypto/ssh`), `CopyFile`, `Reboot` (and wait for
  SSH), `KillProcess`, `ConsoleLog`, `Shutdown`. These are the fault-injection
  building blocks ADR 0008 needs; M2 and M3 tests use them.
- **M1 tests**, per OS: the VM boots and `/etc/os-release` matches; UFW, Docker
  and tailscaled are active; the build installs and `darkpanel version --json`
  reports the build's commit and `ui: true`; `darkpanel serve --dev` started as
  a transient systemd unit answers `GET http://127.0.0.1:8080/healthz` inside
  the VM; from the scanner,
  `nmap -Pn -n -sS -p- --min-rate 2000 --max-retries 2 --max-rtt-timeout 500ms`
  against the target's IPv4 address and again with `-6` against its IPv6
  address finds exactly `22/tcp` open, which shows the panel is not reachable
  from the internet by default; `Reboot` and `KillProcess` self-tests pass.
  The two scans are expected to take a few minutes in total (inference, to be
  measured on the first run).
- **Workflow** `.github/workflows/harness.yml`: on push to `main`, nightly at
  03:17 UTC and on `workflow_dispatch`. A build job runs `make build
  verify-build` and uploads the amd64 binary; a matrix job per OS on
  `ubuntu-24.04` enables KVM access, fails loudly if `/dev/kvm` is missing,
  installs QEMU, `qemu-utils` and `cloud-image-utils`, runs
  `go vet -tags harness ./test/...` and the tests with a 45-minute timeout, and
  uploads console logs on failure. Third-party actions are pinned by commit SHA
  as in `ci.yml`.
- **Gate.** `docs/dforge/gates.md` gains gate 7, "Harness green", the same
  shape as gate 6 for `harness.yml` (its `gh run list --branch --commit`
  query finds `workflow_dispatch` runs as well as push runs), applying on
  `main` and on any branch whose diff touches `test/harness/` or
  `.github/workflows/harness.yml`.
- `.golangci.yml` sets `build-tags: [harness]` so the harness code is linted.

### 19. Tooling fixes folded in

From the tooling register: row 8 (a `web-deps` target that runs
`pnpm -C web install --frozen-lockfile`, prerequisite of `web`, `lint-web` and
`test-web`; `verify-build` depends on `build`), row 10 (`vite.config.ts` uses
`import.meta.dirname`), row 11 (section 17) and row 12 (a `tsconfig.node.json`
covering `vite.config.ts` and `vitest.config.ts`, and a `typecheck` script
that runs `tsc --noEmit -p tsconfig.json && tsc --noEmit -p
tsconfig.node.json`, because `tsc --noEmit` alone skips project references).
A new `schemas` target runs the generator (section 7), and `LDFLAGS` sets the
`internal/buildinfo` variables (section 1).

### 20. Purges and timers summary

| What | When |
|---|---|
| Expired plans; sealed input of plans with a result | start and every 10 minutes; input cleared when the result is recorded |
| Expired sessions | start and every 10 minutes |
| Tokens 30 days after expiry or revocation | start and every 10 minutes |
| Acknowledged agent journal entries | after each `Ack` |
| Tailscale address check | every 10 seconds |
| Journal ingestion | after each dispatched mutation and every 5 seconds |
| Web leaf renewal | after 60 days or on an address change |

### 21. ADRs 0010 and 0011

Accepted ADRs are not edited, so the foundation adds two ADRs (Status:
Proposed until this epic closes), links them from the README index and adds
their traceability rows:

- **ADR 0010: Web certificate authority** (Requirements: R20). ADR 0003 makes
  the internal authority Ed25519, and ADR 0006 has the web listener use "a
  certificate from the internal authority". Browsers do not accept Ed25519 in
  X.509 certificate chains, so that combination would make the Tailscale web
  listener unusable. Decision: a separate ECDSA P-256 root used only for web
  certificates, stored like the CA key in ADR 0005; agent identity stays on the
  Ed25519 authority from M2.
- **ADR 0011: Actors, reserved CLI names and package layout** (Requirements:
  R7, R18). Adds the actors `anonymous` (a failed login whose username matched
  no user) and `local:uid-<n>@<host id>` (a non-root controller-socket caller
  in `--dev` mode only); adds the reserved CLI names `host`, `module`, `token`,
  `audit`, `help` and `completion` to ADR 0006's list; records the package
  layout additions and moves relative to ADR 0001: `internal/core/store`
  (shared by both roles, instead of a controller-only database package),
  `internal/paths`, `internal/buildinfo`, `internal/registry`, `internal/cli`,
  `internal/controller/{api,auth,listen,dispatch,audit,coreactions,server}`,
  `internal/agent/runtime` and `test/harness`. The README's shared actor list
  points to ADR 0011 for the additions.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| P0 Foundation | 0 | `go.mod`, `go.sum`, `internal/deps/deps.go` (build tag `deps`, blank imports that keep every M1 dependency in `go.mod` until used), `internal/core/*.go` (section 2a), `internal/core/coretest/`, `internal/core/errcode/`, `internal/core/store/` with `migrations/controller/0001_core.sql` and `migrations/agent/0001_core.sql`, `internal/paths/`, `internal/buildinfo/`, `internal/registry/`, `internal/modules/modules.go` (empty `All`), `internal/controller/coreactions/module.go` (a stub core module with no actions), `cmd/darkpanel/` (root command, `version` on `buildinfo`, `serve` stub), `Makefile`, `.golangci.yml`, `docs/adr/0010-*.md`, `docs/adr/0011-*.md`, `docs/adr/README.md` | this spec |
| P1 Secrets | 1 | `internal/core/secret/` | P0 |
| P2 Platform | 1 | `internal/platform/`, `migrations/agent/0003_ownership.sql` | P0 |
| P3 Schemas | 1 | `internal/core/schema/` (reflection, validation, generator, compatibility, OpenAPI) | P0 |
| P4 Lifecycle | 1 | `internal/core/lifecycle/` (engine, agent journal, recovery, fixture module in test files), `migrations/controller/0002_lifecycle.sql`, `migrations/agent/0002_lifecycle.sql` | P0; `coretest` fakes for sealer, validator and clock |
| P5 Identity | 1 | `internal/controller/auth/`, `migrations/controller/0003_identity.sql` | P0 |
| P6 Host-info | 1 | `internal/modules/hostinfo/` | P0 |
| P7 Web UI | 1 | everything under `web/` except `embed.go`, `embed_test.go`, `dist/.gitkeep` | section 12 |
| P8 Harness | 1 | `test/harness/`, `.github/workflows/harness.yml`, `docs/dforge/gates.md` | `Makefile` build targets |
| P9 HTTP API | 2 | `internal/controller/api/` (routes, authentication middleware, problem details, headers, static UI, controller socket) | P3, P5, P0 `Dispatcher` and `Audit` (fakes in tests) |
| P10 Listeners | 2 | `internal/controller/listen/` (listener set, Tailscale watcher, web authority and leaf) | P1, P0 store |
| P11 Dispatch | 2 | `internal/controller/dispatch/`, `internal/agent/`, `internal/controller/audit/` (audit store, controller `Journal`, ingester), `migrations/controller/0004_audit.sql` | P1, P3, P4 |
| P12 Core actions | 2 | `internal/controller/coreactions/` (host, module, token, audit, user actions; it takes over `module.go`) | P5, P0 `Audit`, `HostDirectory` and `AgentClient` (fakes in tests) |
| P13 CLI | 2 | `internal/cli/` | P3, P0 `registry.Build`, section 12; a fake server in tests |
| P14 Assembly | 3 | `internal/controller/server/` (startup order: paths, keys, stores, migrations, local host and module-state seeding, recovery, engines, ingestion, purges, listeners, socket), `cmd/darkpanel/` wiring, hostinfo registration in `internal/modules/modules.go`, `internal/modules/*_test.go` (registry, CLI parity and schema snapshot tests), `schemas/` snapshots, end-to-end tests, the OpenAPI route-coverage test, removal of `internal/deps/`, `README.md` and `CONTRIBUTING.md` updates | everything |

Migration paths in the table are under `internal/core/store/`; P0 owns that
directory except the migration files assigned to P2, P4, P5 and P11.

- Wave 0 runs alone: it owns the dependency manifest, every shared interface
  and the build configuration.
- Wave 1 runs P1 to P8 together; wave 2 runs P9 to P13 together; wave 3 runs
  P14 alone. A part consumes only P0's interfaces and earlier waves' merged
  code. Where a wave-2 part needs another wave-2 part's behaviour (the API
  needs dispatch and audit), it codes against the P0 interface with a fake, and
  P14 joins them.
- Files P0 creates and a later part takes over (`module.go` in
  `coreactions`, `modules.go`, `cmd/darkpanel/`) have one owner per wave.
- The risk parts (P1 secrets, P2 platform, P4 lifecycle, P5 identity, P9 API
  with the authentication middleware) are each their own task with a
  risk-level review. They run beside siblings only because they share no file,
  database, port or path with them.
- Test isolation: every Go test uses `t.TempDir()` databases and keys, port
  `0` on `127.0.0.1`, socket paths from `os.MkdirTemp("", "dp")` (Unix socket
  paths are limited to 108 bytes), and the `coretest` fake clock for timers and
  expiry. Vitest uses jsdom and a stubbed `fetch`. Harness tests run only on
  GitHub runners, verified before merge by pushing the work branch and
  dispatching `harness.yml`.

## Plans

The ladder's rules (a risk-3 task runs alone in its wave, at most three tasks
per wave, Rule S splits, complete code in every task) turn this spec into
about fifty tasks, too many for one plan. It is planned as six plans, each
written just before it runs so later plans cite merged code:

1. Foundation: P0.
2. Framework libraries: P1, P2, P3, P4, P6.
3. Identity and HTTP API: P5, P9, P10, P12.
4. Dispatch, CLI and assembly: P11, P13, P14.
5. Web UI: P7.
6. Harness: P8.

Plans 5 and 6 share no file with plans 1–4 and may run in parallel sessions.

## Verification

- Gates 1–6 green on the branch and on `main`; gate 7 (harness) green on
  `main` for all four operating systems.
- The end-to-end Go test (P14): start `serve` on temporary paths and port 0,
  create the first admin through the socket with a computed TOTP code, log in
  over HTTP with password and code, check that every non-public route refuses
  without a session for each of GET, POST, PUT, PATCH, DELETE and OPTIONS,
  check CSRF refusal, bearer-over-cookie precedence and that a break-glass
  action is refused on the network listener, read `hostinfo.summary` through
  the API, create a token, run `darkpanel hostinfo summary get --json` against
  the server with that token, and find the token creation and login in
  `darkpanel audit list` with the token secret and TOTP secret redacted.
- Lifecycle tests with the fixture module cover every row of the state
  transition table.
- A manual browser check with Playwright against `serve --dev` on
  `http://127.0.0.1:8080`: log in with TOTP, open the host-information page,
  create and revoke a token, read the audit log, and confirm no CSP violation
  in the console.
- The registry tests: CLI parity, registration rules, schema snapshots
  current, compatibility fixtures, OpenAPI route coverage.

## Rejected alternatives

- Four specs, one per epic: rejected by the owner; one spec lets the shared
  foundation be settled once.
- Hand-written identity endpoints instead of core actions: each would need its
  own audit and CLI code and would escape the CLI parity test.
- TOTP recovery codes: rejected by the owner; host break-glass suffices.
- Ed25519 web certificates: browsers reject them (ADR 0010).
- A `__Host-` Secure cookie on the plain-HTTP loopback listener: not every
  browser accepts it over plain HTTP.
- A runner bridge with tap devices for the harness network: it needs root on
  the runner and a sysctl workaround for Docker's forward policy; the QEMU
  multicast link needs neither.
- All core tables in one foundation migration: the lifecycle and identity
  columns belong to the parts that use them, and one file per part keeps one
  owner per file.
- Server-sent events in M1: nothing in M1 needs live updates.
- A netlink watcher for Tailscale addresses: polling every 10 seconds is
  simpler and the addresses rarely change.
- TanStack Router: react-router's `NavLink` already meets darkraise-ui's
  `aria-current` rule and suits a registry of lazily added routes.
- Mock Service Worker for UI tests: a stubbed `fetch` covers the few calls.
- A login split into password then TOTP steps: it adds a half-authenticated
  state to secure for no benefit.
- Separate host routes (`GET /api/v1/hosts`): `core.host.list` serves the
  same data with one shape.

## Out of scope (recorded as deferred register rows)

- Server-sent events `/api/v1/events` (ADR 0006): M3 Firewall safe apply,
  which shows revert countdowns.
- Web certificates from `tailscale cert` through the agent (ADR 0006): M5
  Tailscale node management.
- Schema-hash capability negotiation in hello (ADR 0002, R24): M2
  Controller-agent fleet.
- The agent socket and agent break-glass commands (R31): M2 Install, service
  and uninstall.
- Separate `darkpanel` and root service users and directories: M2 Install.
- User management and self-service password change in the UI: M9 roles
  candidate; in M1 the break-glass commands cover them.
- Guard overrides on apply (R40): M3 Firewall safe apply.

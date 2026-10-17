# M1 epics: module framework, walking skeleton, identity and test harness

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R5–R21
**Execution strategy:** parallel — one foundation part fixes every shared
type, table, dependency and file layout first; then the secrets, platform,
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
- The web certificate comes from an internal authority created in M1 (ADR 0010
  below).
- During execution, work branches may be pushed to `darkraise/darkpanel` and
  the harness workflow dispatched on them, because the development host has no
  `/dev/kvm`; branches are deleted after merge.

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
| `react-router` | 8.4.0 | web routing in library mode; `NavLink` sets `aria-current` as darkraise-ui requires |
| `@tanstack/react-query` | 5.104.3 | API data fetching (ADR 0001) |
| `@fontsource-variable/hanken-grotesk` | 5.3.0 | self-hosted UI font (direction A) |
| `@fontsource-variable/jetbrains-mono` | 5.3.0 | self-hosted monospace font (direction A) |

The harness pins its cloud images, Docker Engine and Tailscale packages by
exact URL, version and SHA-256 at plan time (section 18).

## Design

### 1. Process, paths and configuration

`darkpanel serve` runs the controller and, in M1, the agent half in the same
process (ADR 0001). The two halves keep separate databases, keys and runtimes,
and talk only through the `AgentClient` interface (section 3), so M2 replaces
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
All paths come from one `paths.Resolve(dev bool)` function, so tests pass their
own temporary paths.

`serve` flags: `--dev`, and `--listen` (repeatable, section 13). There is no
configuration file in M1.

### 2. Registry and module contract (R5, R6, R7, R9)

Package `internal/core` defines the contract; modules are listed once in
`internal/modules/modules.go` (`All() []core.Module`), which is the single
registration line R5 allows.

- **Manifest.** `Module.Manifest()` returns id, version, actions, resources,
  settings type (nil allowed), preflight checks, owned paths and marked blocks
  (ADR 0009), an uninstall hook (nil allowed until M2), embedded migrations per
  side, and `DefaultEnabled`.
- **Actions** are defined with a generic constructor,
  `core.DefineAction[In, Out](spec, handler)`, which returns a type-erased
  `core.ActionDef` holding the name, version, class, side, break-glass flag,
  lock keys, compensating action name, the input and output Go types and a
  handler that works on `json.RawMessage`. Handlers implement:
  - every class: `Plan(ctx, env, in) (core.PlanOutcome, error)` returning a
    human-readable diff, guard results and the state hash of its lock keys;
  - mutating classes: `Apply(ctx, env, in) (Out, error)` and
    `Verify(ctx, env, in, out) error`;
  - revertible: `Snapshot(ctx, env, in) ([]byte, error)` and
    `Revert(ctx, env, snapshot []byte) error`;
  - read: `Read(ctx, env, in) (Out, error)` instead of the above.
- **Resources** are read endpoints without input:
  `core.DefineResource[Out](name, read)`; a resource name is
  `<module>.<resource>` and its schema is fixed at `v1` (a breaking change takes
  a new resource name, the additive rule of ADR 0002 applies).
- **Env** passed to handlers carries the host id, the side's database (for the
  module's own `<module>_` tables), the exec and file providers, the sealer,
  a clock and a logger.
- **Validation** of every name happens at registration: action names match
  `^[a-z][a-z0-9]*\.[a-z][a-z0-9]*\.[a-z][a-z0-9-]*$`, module ids are not one of
  the reserved CLI names (section 15), a compensable action names an existing
  compensating action, and break-glass actions are controller-side. A
  registration error stops startup; tests cover each rule.
- **Core module.** Actions and resources owned by the core use module id
  `core`; the CLI exposes them without the module segment (section 15).
- **Enablement and preflight (R9).** `core_module_state` (controller database)
  records per host and module: enabled, available, reason, checked at.
  Preflight checks run on the agent side when the agent half starts and on
  `core.module.enable`. A module that is disabled, failed its migration or
  failed preflight is unavailable: its actions and resources answer
  `module.unavailable` with the reason, and every other module keeps working.

### 3. Change lifecycle engine (R10)

Package `internal/core/lifecycle` implements ADR 0004 once and is used by both
sides: the agent half for agent actions and the controller for controller-side
actions. Its API:

- `Read(ctx, principal, action, input)` runs a read action directly.
- `Plan(ctx, principal, host, action, input)` validates the input against the
  action's schema, runs the handler's `Plan`, and stores a plan record with the
  input encrypted (section 5). It returns the plan id, diff, guards, input hash,
  expiry (10 minutes) and, for an irreversible action, the confirmation phrase.
- `Apply(ctx, principal, planID, opts)` follows ADR 0004's ordering: re-check
  guards and the state hash; take the lock keys; persist intent (and for
  revertible actions the snapshot and revert deadline) with a committed
  transaction before touching the host; mutate; verify, reverting at once if
  verify fails; record the result against the plan id. A repeated apply of the
  same plan returns the stored result. `opts` carries the confirm timeout
  (revertible; default 60 s, allowed 10 s to 15 min) and the typed
  confirmation (irreversible; must equal the plan's phrase exactly).
- `Confirm(ctx, principal, changeID)` and `Revert(ctx, principal, changeID)`
  race in one transaction; the first to commit wins, the other gets
  `change.not_pending`.
- Revert timers use the monotonic clock while running. `Recover(ctx)` runs on
  start before any request: it reverts every revertible change without a
  confirmation, marks compensable and irreversible intents without a result as
  `interrupted`, and releases locks whose owner has a result.
- Every apply, confirm, revert, recovery revert and interruption appends an
  entry through the `Journal` interface inside the same transaction as the
  change record (section 11). P4 implements it over `core_journal` for the
  agent side; the audit package implements it over `core_audit` for the
  controller side.

Change states: `applying`, `pending` (revertible, awaiting confirm),
`confirmed`, `reverted`, `completed` (compensable and irreversible), `failed`,
`interrupted`. Plan, snapshot and journal records carry `format: 1`.

The **input hash** is SHA-256 over the canonical input: the typed input struct
re-encoded with `encoding/json`, with every secret field replaced by the string
`"[secret]"`, so a client can check the non-secret fields and nothing about a
secret leaks.

Apply requires the same principal that made the plan
(`plan.principal_mismatch` otherwise). Confirm and revert accept any principal
with the scope the action needs, so an admin can revert someone else's change.

The engine is tested with a fixture module registered only in tests, with one
action per class, including a revertible action whose verify fails and a
recovery test that reopens the database mid-apply. Host-information has no
mutating action.

**Dispatch.** Package `internal/controller/dispatch` routes an API call: a
controller-side action goes to the controller's engine; an agent action goes
through `AgentClient` (methods `Hello`, `Plan`, `Apply`, `Confirm`, `Revert`,
`Read`, `Resource`, `Journal`, `Ack`) to the agent half. In M1 the only
`AgentClient` is `inprocess`, which calls the agent runtime
(`internal/agent/runtime`) directly. The agent runtime owns `agent.db`, its
engine, preflight and resource reads.

### 4. Storage and migrations (R8)

Package `internal/core/store` opens SQLite as ADR 0005 specifies (write-ahead
log, pragmas in the connection string including `foreign_keys(1)` and
`busy_timeout(5000)`, one writer connection plus a read pool), runs migrations
and takes backups.

- Migrations are embedded SQL files named `NNNN_<name>.sql` per module and side,
  each run in a transaction and recorded in `core_migrations` (module, side,
  number, name, checksum SHA-256, applied at). Core runs first, then modules in
  registration order.
- Before applying any pending migration, the store copies the database with
  `VACUUM INTO` to `backups/<from>-<to>-<UTC timestamp>/` in the role's
  directory (mode 0600, the last five kept), where `<from>` and `<to>` are the
  binary versions. No backup is taken when nothing is pending.
- A changed checksum or a failed core migration stops startup. A failed module
  migration marks that module unavailable with the error (section 2).
- Rows numbered above the binary's highest migration are ignored (ADR 0005).
- The schema version reported by `/healthz` is the highest applied core
  migration number of the controller database.

The foundation writes every core table for M1 in two files, so no later part
adds a core migration:

`controller/0001_core.sql`: `core_meta` (key, value: controller id, local host
id, last known Tailscale addresses), `core_hosts` (id, name, is_local,
created_at), `core_module_state`, `core_users` (id, username unique, role,
state `pending|active`, password_hash, totp_secret_ct, totp_last_step,
created_at, updated_at), `core_sessions` (id_hash primary key, user_id,
created_at, last_seen_at, expires_at, source_ip, user_agent), `core_tokens`
(id, user_id, name, scope, secret_hash, created_at, expires_at, last_used_at,
revoked_at), `core_audit` (section 11), `core_journal_cursor` (host_id,
acked_seq), `core_ca` (id, purpose `web`, cert_pem, key_ct, created_at), and
`core_plans`, `core_changes`, `core_locks` for controller-side actions.

`agent/0001_core.sql`: `core_meta`, `core_plans`, `core_changes`,
`core_locks`, `core_journal` (seq integer primary key autoincrement, at,
actor, action, version, phase, change_id, input_json, result, error_code), and
`core_owned_files` (path, module, kind `file|block`, block_id, created_at).

Every id column is a ULID text primary key; times are RFC 3339 UTC text.

### 5. Secrets and keys (R12)

Package `internal/core/secret`:

- **Key files** hold 32 random bytes as `darkpanel-key-v1:<base64>` and are
  created atomically (temporary file, fsync, link to the final name so an
  existing key is never replaced). Loading refuses a file whose mode allows
  group or other access or whose owner is not the process's effective user.
- **Sealer.** `Seal(ad string, plaintext []byte) ([]byte, error)` and
  `Open(ad string, ciphertext []byte) ([]byte, error)` use XChaCha20-Poly1305
  with a random 24-byte nonce. The ciphertext layout is version byte `1`, an
  8-byte key id (the first 8 bytes of SHA-256 of the key), the nonce, then the
  sealed data. The associated data is `<table>.<column>.<row id>` (ADR 0005).
  Plan inputs and snapshots are sealed whole.
- **Subkeys.** `Derive(label string) []byte` returns an HKDF-SHA256 subkey of
  the role key; the CSRF key uses label `darkpanel csrf v1`.
- **Schemas mark secrets.** The schema reflector (section 7) turns the
  `secret:"true"` tag into `"x-secret": true` on the property, plus
  `"writeOnly": true` on input properties. `secret.RedactJSON(raw, schema)`
  replaces every marked value with `"[redacted]"`; audit, journal, logs, diffs
  and error messages pass through it.
- **Secret outputs.** An output field tagged `secret:"true"` (a new API token,
  a TOTP enrollment secret) is returned only in the apply response that created
  it. The stored change result, journal and audit carry it redacted. This is
  how the write-only rule of the shared contracts applies to outputs.

TOTP secrets are stored sealed in `core_users.totp_secret_ct`. M1 has no
third-party credentials yet; the first ones arrive with M5 and M6 and use the
same sealer.

### 6. Host execution providers (R11)

Package `internal/platform` implements ADR 0009:

- `exec.Runner` runs only binaries whose absolute path is in its allowlist,
  with argument vectors, the environment `PATH=/usr/sbin:/usr/bin:/sbin:/bin`,
  `LANG=C.UTF-8`, `LC_ALL=C.UTF-8` and nothing else, and the caller's context
  deadline (a call without a deadline gets 30 s). It returns stdout, stderr and
  exit code; output is capped at 4 MiB per stream. The production allowlist is
  empty in M1; later epics add entries such as `/usr/sbin/ufw`.
- `files.Writer` writes only paths and marked blocks declared in the calling
  module's manifest. It opens the parent with `openat2(RESOLVE_NO_SYMLINKS)`,
  requires the parent to be owned by root (by the effective user when the
  writer is built with `TestMode`) and not writable by group or other, writes a
  temporary file, fsyncs, renames it over the target relative to the directory
  descriptor, fsyncs the directory, and keeps an existing target's mode and
  owner.
- Marked blocks are delimited by `<comment> BEGIN DARKPANEL <module>/<block>`
  and `<comment> END DARKPANEL <module>/<block>` lines (comment prefix declared
  per path, default `#`). `ReplaceBlock` appends the block when absent and
  replaces it when present; `RemoveBlock` deletes it. A file with duplicated,
  nested or unterminated markers is refused with `files.markers_invalid`.
- Every write records the path or block in `core_owned_files` through an
  `Ownership` interface, for `darkpanel uninstall` (M2).

### 7. Schemas, compatibility and OpenAPI (R6, R7)

Package `internal/core/schema`:

- **Reflection** with invopop: every field is required unless its JSON tag has
  `omitempty`; input objects get `additionalProperties: false` so an unknown
  input field is rejected rather than ignored (ADR 0002's safety concern);
  output objects allow additional properties. Patterns, enums, minimum and
  maximum come from `jsonschema` struct tags; every value later passed to a
  program must carry a pattern (ADR 0009).
- **Validation** compiles each schema once with santhosh-tekuri and returns
  errors as a list of `{path, message}` for the problem details `errors`
  field.
- **Snapshots.** `go run ./internal/core/schema/schemagen` (Make target
  `schemas`) writes `schemas/<action>/v<n>.json` with
  `{name, version, class, side, breakGlass, input, output}` and
  `schemas/<module>.<resource>/v1.json` with `{name, output}`. A test in
  `internal/modules` regenerates them in memory from `modules.All()` plus the
  core module and fails when a committed file differs or is missing.
- **Compatibility.** `schema.Compare(old, new)` reports ADR 0002 violations
  (removed or renamed field, changed type, default or constraint, new required
  input field, removed enum value, changed class). A test runs it for every
  file under `schemas/released/<tag>/`; until the first release that directory
  is empty, so the checker is proven with fixture pairs, one per rule.
- **OpenAPI 3.1.** `schema.OpenAPI(registry)` builds the document served at
  `/api/v1/openapi.json`: the hand-written auth, host, change and health routes
  plus one plan, apply or read operation per action and one get operation per
  resource, reusing the action schemas.

### 8. Users, passwords and TOTP (R15, R19)

Package `internal/controller/auth`:

- **Passwords** are hashed with argon2id, memory 64 MiB, 3 iterations,
  parallelism 1, 16-byte salt, 32-byte key, stored as a PHC string so the
  parameters can change later. At most two hashes run at once (a semaphore), to
  keep the controller within its memory budget (S6). Passwords must be 12 to
  128 characters; there are no composition rules.
- **TOTP** follows RFC 6238: SHA-1, 6 digits, 30-second step, 20-byte random
  secret. A code is accepted for the current step and one step either side.
  The step of the last accepted code is stored, and a code for that step or an
  earlier one is refused, so a code cannot be replayed.
- **Roles (R19).** `core_users.role` exists and only `admin` is accepted.
- **Users** are `pending` until a TOTP code confirms enrollment, then
  `active`. A pending user cannot log in.

### 9. Sessions, CSRF, rate limits and API tokens (R16, R17)

- **Login** is one request with username, password and TOTP code. Every
  failure returns the same `auth.invalid_credentials` problem. An unknown
  username still runs one argon2id hash against a fixed dummy hash, so timing
  does not reveal whether a user exists.
- **Rate limits** (in memory): per username, 5 failures in 15 minutes lock
  that username for 15 minutes; per source address, 20 failures in 15 minutes
  block that address for 15 minutes. A blocked attempt returns
  `auth.rate_limited` with `Retry-After` and is not evaluated.
- **Sessions.** A 32-byte random id travels in the cookie
  `__Host-darkpanel_session` (Secure, HttpOnly, SameSite=Strict, Path=/, no
  Domain); the database stores only its SHA-256. A session ends after 1 hour
  without a request or 12 hours after login, whichever comes first. Logout,
  `reset-totp`, `reset-password` and `user delete` end the user's sessions.
- **CSRF.** The token is HMAC-SHA256 of the session id under the derived CSRF
  key, so nothing extra is stored. Login and `GET /api/v1/auth/session` return
  it; the client sends it as `X-CSRF-Token` on every request whose method is not
  GET, HEAD or OPTIONS, and the middleware compares in constant time. GET and
  HEAD handlers never change state. Requests authenticated with an API token
  skip the CSRF check.
- **API tokens.** Format `dpt_<id>_<secret>` where `<id>` is the token's ULID
  and `<secret>` is 32 random bytes in base32 without padding; the database
  stores SHA-256 of the secret. Scopes are `read`, `operate` and `admin`.
  Every token expires: 1 to 365 days, default 90. A token can create tokens only
  with a scope at or below its own and only when its scope is `admin`.
  Tokens are sent as `Authorization: Bearer <token>`.

### 10. Authorization and core actions (R7, R17)

| Scope | Allows |
|---|---|
| `read` | read actions, resources, host list, change list, audit list, health details |
| `operate` | `read`, plus plan, apply, confirm and revert of module actions |
| `admin` | `operate`, plus core mutating actions (tokens, module enablement) |

A session of an `admin` user has the `admin` scope. Break-glass actions are
served only on the controller socket, never on a network listener.

Core actions (all controller-side):

| Action | Class | Notes |
|---|---|---|
| `core.host.list/v1` | read | hosts with module states |
| `core.module.list/v1` | read | input `hostId` |
| `core.module.enable/v1` | compensable (by `core.module.disable`) | input `hostId`, `moduleId`; runs preflight; lock `core.module` |
| `core.module.disable/v1` | compensable (by `core.module.enable`) | lock `core.module` |
| `core.token.list/v1` | read | the caller's tokens, never secrets |
| `core.token.create/v1` | compensable (by `core.token.revoke`) | input `name`, `scope`, `expiresInDays`; output includes secret `token` |
| `core.token.revoke/v1` | irreversible | input `id`; confirmation phrase is the token id |
| `core.audit.list/v1` | read | input `limit` (1–500, default 100), `before` (audit id), optional `actor`, `hostId`, `action` |
| `core.user.list/v1` | read, break-glass | |
| `core.user.create/v1` | compensable (by `core.user.delete`), break-glass | input `username`, secret `password`; output secret `totpSecret` and `otpauthUri` |
| `core.user.activate/v1` | compensable (by `core.user.reset-totp`), break-glass | input `username`, `code` |
| `core.user.reset-totp/v1` | irreversible, break-glass | new pending secret; ends sessions; phrase is the username |
| `core.user.reset-password/v1` | irreversible, break-glass | secret `password`; ends sessions; phrase is the username |
| `core.user.delete/v1` | irreversible, break-glass | refuses to delete the last active admin; phrase is the username |

A test enumerates the registry and fails when a mutating action has no
generated CLI command (S4 from M1 on).

### 11. Audit (R18)

`core_audit` columns: id (ULID), at, actor (shared-contract actor string),
host_id (null for controller-side actions), action, version, phase (`apply`,
`confirm`, `revert`, `recover`, `interrupt`, `login`, `logout`), change_id,
input_json (redacted), result (`ok` or `error`), error_code, source (client
address or `socket`). The table has no update or delete path in code.

- Controller-side actions write their audit row through the engine's
  `Journal` interface, in the same transaction as the change result.
- Agent actions journal on the agent side (section 3, actor passed from the
  controller); the controller ingests journal entries into `core_audit` in
  sequence order, deduplicated by host and sequence, and acknowledges them
  (ADR 0003). In M1 ingestion runs right after each dispatched call and every
  5 seconds.
- Logins (success and failure, without the password or code) and logouts are
  audited with actor `user:<id>`, or `anonymous` with the submitted username
  in input_json when no user matched.
- Plans and reads are not audited; they change nothing.
- Viewing: the audit page in the UI and `darkpanel audit list`.

### 12. HTTP API (R7, R13, R16)

Package `internal/controller/api`. Every route sits behind one authentication
middleware that denies any method on any path unless the route is in the public
list: `POST /api/v1/auth/login`, `GET /healthz`, and `GET`/`HEAD` of the
embedded UI's static files and its index fallback (they carry no data).
Routes use the standard library router with whole-segment wildcards (ADR 0006).

| Route | Purpose |
|---|---|
| `POST /api/v1/auth/login` | `{username, password, code}` → `200 {user, csrfToken, expiresAt}` and the session cookie |
| `POST /api/v1/auth/logout` | `204`, clears the cookie |
| `GET /api/v1/auth/session` | `{user, csrfToken, expiresAt}` for a session, `{token: {id, name, scope, expiresAt}}` for a token |
| `GET /healthz` | `{status: "ok"}`; with a session or token also `version`, `commit`, `schemaVersion` |
| `GET /api/v1/hosts`, `GET /api/v1/hosts/{host}` | hosts and their module states |
| `GET /api/v1/hosts/{host}/resources/{module}/{resource}` | a resource |
| `POST /api/v1/hosts/{host}/actions/{name}/{version}/{plan,apply,read}` | agent actions |
| `POST /api/v1/actions/{name}/{version}/{plan,apply,read}` | controller-side actions |
| `GET /api/v1/changes`, `GET /api/v1/changes/{id}` | changes, filter `state` |
| `POST /api/v1/changes/{id}/confirm`, `.../revert` | lifecycle |
| `GET /api/v1/openapi.json` | OpenAPI 3.1 (section 7) |

Bodies: plan `{input}` → `{planId, inputHash, diff, guards, expiresAt,
confirmation?}`; apply `{planId, confirmTimeoutSeconds?, confirmation?}` →
`{changeId, state, output, revertDeadline?}`; read `{input}` → `{output}`.

- **Errors** are RFC 9457 problem details (`application/problem+json`) with
  `type: "about:blank"`, `title`, `status`, `detail`, the stable `code` from
  `internal/core/errcode`, and `errors` for validation. M1 codes:
  `request.invalid_json`, `request.invalid_input`, `request.too_large`,
  `auth.required`, `auth.invalid_credentials`, `auth.rate_limited`,
  `auth.csrf`, `auth.forbidden`, `not_found`, `action.unknown`,
  `module.unavailable`, `plan.not_found`, `plan.expired`,
  `plan.state_changed`, `plan.principal_mismatch`,
  `plan.confirmation_required`, `plan.guard_failed`, `change.not_found`,
  `change.not_pending`, `lock.held`, `files.markers_invalid`, `internal`.
  `internal` never carries the underlying error text.
- **Limits.** Request bodies up to 1 MiB; `ReadHeaderTimeout` 10 s,
  `ReadTimeout` 30 s, `WriteTimeout` 60 s, `IdleTimeout` 120 s.
- **Headers** on every response: `Content-Security-Policy: default-src 'self';
  script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:;
  font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none';
  form-action 'self'`, `X-Content-Type-Options: nosniff`,
  `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin`,
  and `Strict-Transport-Security: max-age=31536000` on HTTPS. API responses
  also carry `Cache-Control: no-store`. `style-src 'unsafe-inline'` is needed
  because the UI kit sets inline style attributes; scripts stay strict.
- **Static UI.** `web.Dist` is served at `/`; any non-`/api/` GET path without
  a file returns `index.html` so client routes work on reload.

### 13. Listeners and web certificate (R20)

Package `internal/controller/listen`:

- **Defaults** are `loopback` and `tailscale`. `loopback` is plain HTTP on
  `127.0.0.1:8080`, which also serves SSH port forwarding. `tailscale` is HTTPS
  on port 8443 of each Tailscale address.
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
  renewed after 60 days or on any address change) with SANs for the hostname,
  `localhost` and the Tailscale IP addresses. The MagicDNS name needs the
  Tailscale API and arrives with M5. The leaf lives in memory only.
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
`local:uid-<n>@<host id>`. No session, token or CSRF applies on the socket; the
peer credentials are the authentication.

### 15. CLI (R7, R17)

Package `internal/cli`, generated from the registry with cobra:

- **Names.** A module action `<module>.<noun>.<verb>` is
  `darkpanel <module> <noun> <verb>`; a core action is `darkpanel <noun>
  <verb>`; a resource `<module>.<resource>` is
  `darkpanel <module> <resource> get`. Reserved top-level names, which no
  module id may use: `serve`, `agent`, `update`, `user`, `controller`,
  `change`, `uninstall`, `version`, `host`, `module`, `token`, `audit`,
  `help`, `completion`.
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
  only by `--confirm` (confirms right after a successful apply) or
  `darkpanel change confirm <id>`.
- **Output.** Tables for lists, `key: value` lines otherwise, `--json` for the
  raw output. Exit codes: 0 success, 1 error, 2 usage, 3 refused by a guard or
  a declined prompt, 4 authentication.
- **Connection.** URL from `--url`, then `DARKPANEL_URL`, then the config
  file, then `http://127.0.0.1:8080`. Token from `DARKPANEL_TOKEN`, then the
  config file `$XDG_CONFIG_HOME/darkpanel/cli.json`
  (`{url, token, caFingerprint}`, refused if group or other can read it). HTTPS
  verifies against system roots, or against the pinned web root when
  `caFingerprint` is set.
- **Break-glass.** Break-glass actions and `controller web-ca` use the
  controller socket (`--socket` overrides; `--dev` resolves the dev path).
  `darkpanel user create --username <name>` prompts for the password twice,
  applies `core.user.create`, renders the `otpauth://` URI as a terminal QR
  code with the URI and secret printed below it, prompts for a code and applies
  `core.user.activate`. `reset-totp` runs the same enrollment after the reset.
- **Hand-written commands:** `serve`, `version`, `change list|show|confirm|revert`,
  `controller web-ca`.

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
`statfs("/")`. Files are read through an `fs.FS` rooted at `/`, so tests use
fixtures; the parsers have fixture tests for each supported OS and a fuzz
target each (R74 starts here). Preflight checks that `/proc/meminfo` is
readable. On first start `serve` creates the local host record (name from the
hostname, `is_local` true) and the agent half uses its id.

### 17. Web UI (R13, R14)

Direction A from the foundations register: darkraise-ui's quiet style, classic
shell, zinc surface, blue accent, compact density, Hanken Grotesk and JetBrains
Mono (self-hosted, no external requests). Exact theme values are set at plan
time against darkraise-ui 6.9.6.

- **Routing.** react-router in library mode with a darkraise-ui router adapter
  built on `NavLink`. Routes: `/login`, `/` (hosts overview),
  `/hosts/:hostId/:moduleId/*` (module pages), `/audit`, `/settings/tokens`.
  Every route except `/login` sits behind a session guard that redirects to
  `/login` and returns afterwards.
- **Module route registry (R14).** `web/src/modules/registry.ts` lists one
  entry per module: id, label, icon and a lazily imported route bundle that
  exports its host routes. Adding a module adds one line there and a folder
  `web/src/modules/<id>/`; no other module's files change. The host page builds
  its tabs from the registry filtered by the host's enabled modules.
- **API client.** One `fetch` wrapper sends `credentials: "same-origin"`, adds
  `X-CSRF-Token` on unsafe methods, parses problem details, and on
  `auth.required` clears the session and goes to `/login`. TanStack Query
  caches reads. Request and response types are hand-written in
  `web/src/api/types.ts` from section 12.
- **Pages.** Login (username, password, 6-digit code; one generic error; the
  wait time on `auth.rate_limited`). Hosts overview (name, local marker,
  module states). Host-information page (the summary as labelled values with
  human-readable sizes and uptime). Audit (table with time, actor, host,
  action, result; older entries by `before` cursor). Tokens (list; create
  dialog with name, scope and expiry that shows the token once with a copy
  button; revoke with the typed confirmation).
- **Pre-paint script** moves from `web/index.html` into
  `web/public/theme-init.js` loaded with a plain `<script src>`, so the CSP
  needs no inline script, and it falls back to `dark` to match `themeConfig`
  (tooling register row 11).
- Tests: Vitest with Testing Library and a stubbed `fetch`, for the login
  flow, the session guard, CSRF header handling, the registry-driven tabs, the
  host-information page and the token dialog.

### 18. Integration harness (R21)

Under `test/harness/`, in Go behind the build tag `harness`, run with
`go test -tags harness ./test/harness/... -args -os <id>`:

- **Images.** `images.json` pins Ubuntu 22.04 and 24.04 and Debian 12 and 13
  cloud images by dated release URL and SHA-256 (never a `current` link); the
  scanner uses the Debian 13 image. Downloads are verified before use and
  cached between runs.
- **VMs.** QEMU with KVM, a qcow2 overlay per run, and a cloud-init NoCloud
  seed built with `cloud-localds`. Each VM has two NICs: a user-mode
  management NIC with a host port forward for SSH, and an "internet" NIC on a
  runner bridge. The target is `198.18.0.10/15` and `2001:db8:d0::10/64`; the
  scanner is `.20` and `::20` (ADR 0008). The runner disables
  `bridge-nf-call-iptables` and `bridge-nf-call-ip6tables`, because Docker on
  the runner sets the forward policy to drop and would otherwise block bridged
  traffic.
- **Provisioning** through cloud-init: UFW installed and enabled with
  `allow 22/tcp`, Docker Engine from Docker's apt repository and tailscaled from
  Tailscale's stable repository, both pinned to exact versions at plan time;
  tailscaled is installed and running but not logged in until M5.
- **Driver API** (`vm` package): `Boot`, `Run` (SSH with a per-run Ed25519
  key, through `golang.org/x/crypto/ssh`), `CopyFile`, `Reboot` (and wait for
  SSH), `KillProcess`, `ConsoleLog`, `Shutdown`. These are the fault-injection
  building blocks ADR 0008 needs; M2 and M3 tests use them.
- **M1 tests**, per OS: the VM boots and `/etc/os-release` matches; UFW, Docker
  and tailscaled are active; the build installs and `darkpanel version --json`
  reports the build's commit and `ui: true`; `darkpanel serve --dev` started as
  a transient systemd unit answers `GET http://127.0.0.1:8080/healthz` inside
  the VM; an nmap TCP scan of all ports from the scanner over IPv4 and IPv6
  finds exactly `22/tcp` open, which shows the panel is not reachable from the
  internet by default; `Reboot` and `KillProcess` self-tests pass.
- **Workflow** `.github/workflows/harness.yml`: on push to `main`, nightly at
  03:17 UTC and on `workflow_dispatch`. A build job runs `make build
  verify-build` and uploads the amd64 binary; a matrix job per OS on
  `ubuntu-24.04` enables KVM access, fails loudly if `/dev/kvm` is missing,
  installs QEMU, `qemu-utils` and `cloud-image-utils`, runs the tests with a
  45-minute timeout and uploads console logs on failure. Third-party actions are
  pinned by commit SHA as in `ci.yml`.
- **Gate.** `docs/superpowers/gates.md` gains gate 7, "Harness green", the same
  shape as gate 6 for `harness.yml`, applying on `main` and on any branch whose
  diff touches `test/harness/` or `.github/workflows/harness.yml`.

### 19. Tooling fixes folded in

From the tooling register: row 8 (a `web-deps` target that runs
`pnpm -C web install --frozen-lockfile`, prerequisite of `web`, `lint-web` and
`test-web`; `verify-build` depends on `build`), row 10 (`vite.config.ts` uses
`import.meta.dirname`), row 11 (section 17) and row 12 (type-check
`vite.config.ts` and `vitest.config.ts` through a `tsconfig.node.json`
reference). A new `schemas` target runs the generator (section 7).

### 20. ADR 0010: web certificate authority

ADR 0003 makes the internal authority Ed25519, and ADR 0006 has the web
listener use "a certificate from the internal authority". Browsers do not
accept Ed25519 in X.509 certificate chains, so that combination would make the
Tailscale web listener unusable. Accepted ADRs are not edited, so the
foundation adds `docs/adr/0010-web-certificate-authority.md` (Requirements:
R20; Status: Proposed until this epic closes) deciding a separate ECDSA P-256
root used only for web certificates, stored like the CA key in ADR 0005, while
agent identity stays on the Ed25519 authority from M2. The ADR README index and
traceability table gain the new entry. The README's shared actor list gains
two values this spec needs: `anonymous`, for a failed login whose username
matched no user, and `local:uid-<n>@<host id>`, for a non-root caller of the
controller socket in `--dev` mode only.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| P0 Foundation | 0 | `go.mod`, `go.sum`, `internal/deps/deps.go` (build tag `deps`, blank imports that keep every M1 dependency in `go.mod` until used), `internal/core/*.go` (contract types and interfaces: module, manifest, action and resource definitions, env, principal, class, `AgentClient`, `Dispatcher`, `Sealer`, `Validator`, `AuditWriter`, `Clock`), `internal/core/errcode/`, `internal/core/store/` (open, migrations, backups) and both core migration files, `internal/paths/`, `internal/modules/modules.go`, `cmd/darkpanel/` (root command with `serve` and CLI wiring stubs), `Makefile`, `docs/adr/0010-*.md`, `docs/adr/README.md` | this spec |
| P1 Secrets | 1 | `internal/core/secret/` | P0 interfaces |
| P2 Platform | 1 | `internal/platform/` | P0 interfaces, `core_owned_files` |
| P3 Schemas | 1 | `internal/core/schema/` (reflection, validation, snapshots generator, compatibility, OpenAPI) | P0 types |
| P4 Lifecycle | 1 | `internal/core/lifecycle/` (engine, journal, recovery, fixture module in test files) | P0 interfaces, core tables; fakes for sealer and validator |
| P5 Identity | 1 | `internal/controller/auth/` (passwords, TOTP, rate limits, sessions, CSRF, tokens, principals) | P0 interfaces, core tables |
| P6 Host-info | 1 | `internal/modules/hostinfo/` | P0 types |
| P7 Web UI | 1 | everything under `web/` except `embed.go`, `embed_test.go`, `dist/.gitkeep` | the API contract in section 12 |
| P8 Harness | 1 | `test/harness/`, `.github/workflows/harness.yml`, `docs/superpowers/gates.md` | the `Makefile` build targets |
| P9 HTTP API | 2 | `internal/controller/api/` (routes, authentication middleware, problem details, headers, static UI, controller socket) | P3, P5, P0 `Dispatcher` (fake in tests) |
| P10 Listeners | 2 | `internal/controller/listen/` (listener set, Tailscale watcher, web authority and leaf) | P1, P0 store |
| P11 Dispatch | 2 | `internal/controller/dispatch/`, `internal/agent/` (runtime, in-process client, journal ingestion), `internal/controller/audit/` | P4, P1, P3 |
| P12 Core actions | 2 | `internal/controller/coreactions/` (host, module, token, audit, user actions) | P5, P0 `AuditWriter`, P4 through P0 interfaces |
| P13 CLI | 2 | `internal/cli/` | P3, the API contract in section 12; a fake server in tests |
| P14 Assembly | 3 | `internal/controller/server/` (startup order: paths, keys, stores, migrations, local host, recovery, engines, ingestion, listeners, socket), `cmd/darkpanel/` wiring, `internal/modules/modules.go` registration of hostinfo, `schemas/` snapshots, end-to-end tests, `internal/modules/*_test.go` (registry, CLI parity and schema snapshot tests), removal of `internal/deps/`, `README.md` and `CONTRIBUTING.md` updates | everything |

- Wave 0 runs alone: it owns the dependency manifest, every shared type and
  every core migration.
- Wave 1 runs P1 to P8 together; wave 2 runs P9 to P13 together; wave 3 runs
  P14 alone. A part consumes only the foundation's interfaces and earlier
  waves' merged code. Where a wave-2 part needs another wave-2 part's
  behaviour (the API needs dispatch), it codes against the P0 interface with a
  fake, and P14 joins them.
- The risk parts (P1 secrets, P2 platform, P4 lifecycle, P5 identity, P9 API
  with the authentication middleware) are each their own task with a
  risk-level review. They run beside siblings only because they share no file,
  database, port or path with them.
- Test isolation: every Go test uses `t.TempDir()` databases and keys, port
  `0` on `127.0.0.1`, and socket paths from `os.MkdirTemp("", "dp")` (Unix
  socket paths are limited to 108 bytes). Vitest uses jsdom and a stubbed
  `fetch`. Harness tests run only on GitHub runners.

## Verification

- Gates 1–6 green on the branch and on `main`; gate 7 (harness) green on
  `main` for all four operating systems.
- The end-to-end Go test (P14): start `serve` on temporary paths and port 0,
  create the first admin through the socket with a computed TOTP code, log in
  over HTTP with password and code, check that every non-public route refuses
  without a session for each of GET, POST, PUT, PATCH, DELETE and OPTIONS,
  check CSRF refusal, read `hostinfo.summary` through the API, create a token,
  run `darkpanel hostinfo summary get --json` against the server with that
  token, and find the token creation and login in `darkpanel audit list`.
- Lifecycle tests with the fixture module cover each class, verify failure,
  confirm and revert racing, timer expiry, recovery after a simulated crash,
  repeated apply and the typed confirmation.
- A manual browser check with Playwright against `serve --dev`: log in with
  TOTP, open the host-information page, create and revoke a token, read the
  audit log, and confirm no CSP violation in the console.
- The registry tests: CLI parity, reserved names, schema snapshots current,
  compatibility fixtures.

## Rejected alternatives

- Four specs, one per epic: rejected by the owner; one spec lets the shared
  foundation be settled once.
- Hand-written identity endpoints instead of core actions: each would need its
  own audit and CLI code and would escape the CLI parity test.
- TOTP recovery codes: rejected by the owner; host break-glass suffices.
- Ed25519 web certificates: browsers reject them (ADR 0010).
- Server-sent events in M1: nothing in M1 needs live updates.
- A netlink watcher for Tailscale addresses: polling every 10 seconds is
  simpler and the addresses rarely change.
- TanStack Router: react-router's `NavLink` already meets darkraise-ui's
  `aria-current` rule and suits a registry of lazily added routes.
- Mock Service Worker for UI tests: a stubbed `fetch` covers the few calls.
- A login split into password then TOTP steps: it adds a half-authenticated
  state to secure for no benefit.

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

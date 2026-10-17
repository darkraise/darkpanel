# M1 Identity and HTTP API Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build parts P5, P9, P10 and P12 of the M1 platform core (plan 3 of 6 in the spec's "Plans" section): the identity service (users, argon2id passwords, TOTP, sessions, CSRF, API tokens, login rate limits), the HTTP API with its controller socket, the listener set with the web certificate authority, and the controller-side core actions, so that plan 4 can wire dispatch, audit, the CLI and the server around them.

**Architecture:** Four packages that import only the foundation and plan 2 (`internal/core`, `internal/core/store`, `internal/core/errcode`, `internal/core/coretest`, `internal/core/secret`, `internal/core/schema`, `internal/core/lifecycle`, `internal/buildinfo`, `internal/registry`, `web`): `internal/controller/auth`, `internal/controller/listen`, `internal/controller/api` and `internal/controller/coreactions`. The API and the core actions use the identity service; where a part needs behaviour that plan 4 builds (the API needs dispatch and audit), it codes against a `core` interface and is tested with a fake, and plan 4 joins them.

**Tech Stack:** Go 1.27.2, modernc.org/sqlite v1.60.1, github.com/oklog/ulid/v2 v2.1.2, golang.org/x/crypto v0.58.0 (argon2id), golang.org/x/sys v0.49.0 (`SO_PEERCRED`, `IP_FREEBIND`), the standard library (`net/http`, `crypto/ecdsa`, `crypto/tls`, `crypto/x509`), golangci-lint v2.15.0, GNU make.

**Spec:** docs/superpowers/specs/2026-10-17-m1-platform-core-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves.

**Parallelism:** waves — 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 | 9-11 | 12 | 13 | 14 | 15 | 16 | 17-18 | 19 | 20 | 21 | 22-23 | 24 | 25 | 26-27 | 28 | 29 | 30

**Integration check:** `timeout 1500 make check`

**Worktree setup:** `timeout 300 pnpm -C web install --frozen-lockfile`

## Global Constraints

- The spec is the source of truth, including its "Owner decisions" section; this plan's Assumptions record where it departs and why. Where a step gives a file's complete content, write exactly that content.
- No dependency changes: every module this plan imports is already pinned in `go.mod` by the foundation (`internal/deps/deps.go` keeps them until plan 4 removes it). No task edits `go.mod` or `go.sum`, and `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing after every task.
- Every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001). The `Makefile` exports it; a `go` command typed outside `make` is prefixed with it, as every step here does.
- A task edits only the files in its own `**Files:**` block.
- In a waves run every task executes in its own git worktree created by the controller and commits only there; the controller merges a wave's worktrees before starting the next wave.
- `**Items:**` numbers are row numbers of the register `docs/superpowers/registers/2026-10-17-m1-platform-core.md`.
- Test isolation (spec "Parallel decomposition"): every Go test uses `t.TempDir()` for databases, keys and files, binds only port `0` on `127.0.0.1` (the one IPv6 loopback bind in the listener tests skips with a reason where the host has none), builds socket paths with `os.MkdirTemp("", "dp")` (Unix socket paths are limited to 108 bytes), writes no fixed path outside its temporary directory, and uses `coretest.FakeClock` for timers, expiry and rate-limit windows. No test in this plan shares a file, port or database with another task's tests.
- `golangci-lint run` reports `0 issues.` after every task: every exported identifier has a doc comment (revive), files are `gofmt`- and `goimports`-clean, and a `//nolint` carries the rule and a reason, as the code below shows. Tasks of one wave lint at the same time and golangci-lint guards its cache with one lock file, so every `golangci-lint run ./...` a step gives runs as `golangci-lint run --allow-serial-runners ./...` and the step's `timeout 600` stays.
- Every build and test command carries an explicit `timeout`.
- Redaction marker: every redacted secret value is the JSON string `"[redacted]"` (`secret.Redacted`). Handlers never write a secret value (password, TOTP code or secret, session id, CSRF token, API token) into diff lines, guard messages, error details, logs or problem details; the tests of the identity and API tasks scan for the submitted values.
- Never commit `.superpowers/`.
- English only. Commits use `<type>(<scope>): <subject>` with type one of feat, fix, docs, style, refactor, test, chore, perf; the whole first line is at most 50 characters, imperative, no trailing period; no AI attribution of any kind.
- `Makefile` recipe lines begin with a tab character, never spaces. Migration files are SQL, named `NNNN_<name>.sql`, with STRICT tables.
- `internal/controller/auth` takes every time from `core.Clock` and never calls `time.Now`, so expiry, lockouts and TOTP steps run on `coretest.FakeClock` in tests; ULIDs are stamped from the same clock.
- Nothing inside a `store.DB.Write` callback calls `Write`, `Writer` or `core.Audit.Record` (one writer connection: it would deadlock).
- No password, TOTP code, TOTP secret, session id, CSRF token or token secret appears in an error detail, field message, audit input or log line; hashes, codes and CSRF tokens are compared in constant time.
- Tests build the service with `testParams` (cheap argon2id); only production uses `DefaultParams`.
- P10 edits only files under `internal/controller/listen/`; it adds no migration and never edits `0001_core.sql`.
- The web root's private key exists in clear only in memory: stored sealed in `core_ca.key_ct` with associated data `core_ca.key_ct.<row id>`, never logged, never written to a file; the PKCS #8 buffer is cleared after sealing and after opening. The leaf and its key live in memory only.
- Time comes only from `core.Clock`; tests use `coretest.FakeClock`. The hostname is injected, never read inside the package.
- Listener tests bind `127.0.0.1` port 0 only, through `fakeBinder`, which maps every endpoint address (Tailscale, IPv6, explicit) to `127.0.0.1:0`; only `TestBindSetsFreeBind/ipv6` binds `[::1]:0`.
- Package `internal/controller/api` never writes a session id, CSRF token, password, TOTP code or token secret into a log line or a problem detail. Tests assert that the password and the token secret never appear in responses.
- Every refusal goes through `writeProblem`, and every successful JSON body through `writeJSON`. No handler calls `http.Error`.
- Every route that is not in spec section 12's public list is wrapped by `requireCaller`, and every route is reached only through `Server.Handler()` (network) or `SocketHandler()` (socket); no other handler is exported.
- Tests use `request(...)` with `testRemote`, the harness's fake clock, and `os.MkdirTemp("", "dp")` for socket paths. They bind no TCP port.
- Only `internal/controller/coreactions` changes. `auth`, `registry`, `errcode`, `core` and the migrations are consumed as they are.
- No handler reads or writes `core_users`, `core_sessions` or `core_tokens` directly; identity goes through `*auth.Service` only (P5's ownership). Handlers write only `core_module_state`, and only inside `core.module.enable`/`disable`'s `Apply` through `Env.DB.Write`.
- A `Plan` never writes, never calls an agent, and returns `Guards` as a non-nil slice. Its `StateHash` covers every stored value its `Apply` depends on, as the contracts table lists.
- No diff line, guard message or error detail contains a submitted password or code, a TOTP secret or a token. Test inputs use `testPassword`, `newPassword` and generated codes so the scans are meaningful.
- Tests run every action through a real `lifecycle.Engine` (`newTestEnv`), so validation, redaction, confirmation phrases and the state-hash recheck are the production ones.

## Contracts

The foundation's names (`core`, `store`, `errcode`, `coretest`, `buildinfo`, `registry`, `paths`) are those of `docs/superpowers/plans/2026-10-17-m1-platform-foundation.md` › Contracts, and plan 2's (`secret`, `platform`, `schema`, `lifecycle`, `hostinfo`) those of `docs/superpowers/plans/2026-10-17-m1-framework-libraries.md` › Contracts, both as merged on `main`; where a document and the merged code differ, the code wins. This plan adds the following; plan 4 codes against them. Part labels P5 (identity), P10 (listeners), P9 (HTTP API) and P12 (core actions) are the spec's "Parallel decomposition" parts.

Part P5 adds package `internal/controller/auth` and the controller migration `internal/core/store/migrations/controller/0003_identity.sql`. P9 (HTTP API), P12 (core actions) and P14 (assembly) code against the exported names below; the unexported names are listed because later tasks of this part consume them.

**`internal/controller/auth`** (package `auth`, P5)

- Task 1 (`password.go`, carries the package doc comment): `type Params struct { Memory uint32; Iterations uint32; Parallelism uint8; SaltLen int; KeyLen uint32 }` (argon2id parameters, `Memory` in KiB); `var DefaultParams = Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}`; `const MaxConcurrentHashes = 2`; `const MinPasswordLength = 12`, `const MaxPasswordLength = 128` (Unicode characters). Unexported, used by Tasks 4 and 7: `newHasher(p Params) *hasher` (hashes one random password at construction to make the dummy hash); `(*hasher).hash(ctx context.Context, password string) (string, error)` returns the PHC string `$argon2id$v=19$m=<KiB>,t=<iterations>,p=<parallelism>$<salt>$<key>` (salt and key in unpadded standard base64); `(*hasher).verify(ctx, phc, password string) (bool, error)` reads the parameters from `phc`, compares in constant time, and returns an error for a malformed `phc`; `(*hasher).verifyDummy(ctx, password string) error` runs one verification against the dummy hash; the field `compute func(password, salt []byte, p Params) []byte` is the argon2id call, replaced by tests. Every hash and verification waits for one of `MaxConcurrentHashes` slots or for `ctx` to end, then returns `ctx.Err()`. `checkUsername(username string) error` and `checkPassword(password string) error` return `*errcode.Error` with code `request.invalid_input` and one field error at `/username` (pattern `^[a-z0-9][a-z0-9._-]{0,63}$`) or `/password`, and never quote the value. Test helpers in `password_test.go`: `testParams` (cheap argon2id parameters) and `wantField(t, err, path)`.
- Task 2 (`totp.go`): `const TOTPSecretSize = 20`; `const TOTPPeriod = 30 * time.Second`; `func TOTPCode(secret string, t time.Time) (string, error)` returns the six-digit code at `t` for an unpadded base32 secret, the form `CreateUser` and `ResetTOTP` return. P12's tests and P14's end-to-end test use it to compute codes. Unexported: `b32` (`base32.StdEncoding` without padding), `newTOTPSecret() []byte`, `encodeTOTPSecret(key []byte) string`, `totpStep(t time.Time) int64` (Unix seconds / 30), `totpCode(key []byte, step int64) string` (RFC 6238, HMAC-SHA-1, six digits), `matchTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool)` (exactly six ASCII digits, checked against the steps `now-1`, `now` and `now+1` that are greater than `lastStep`; returns the matching step). Test helper `rfcKey`.
- Task 3 (`ratelimit.go`): `const UsernameFailureLimit = 5`, `SourceFailureLimit = 20`, `FailureWindow = 15 * time.Minute`, `LockDuration = 15 * time.Minute`, `MaxLimiterKeys = 10000`; `type RetryAfterError struct { After time.Duration }` with `Error() string` and `Seconds() int` (whole seconds rounded up, at least 1). It is the cause inside every `auth.rate_limited` error: P9 calls `errors.As(err, &ra)` with `var ra *auth.RetryAfterError` and sends `Retry-After: <ra.Seconds()>`. Unexported, used by Tasks 4 and 7: `newLimiter(clock core.Clock) *limiter`, `(*limiter).blocked(username, source string) time.Duration` (0 means evaluate), `(*limiter).fail(username, source string)`. Keys are `u:<username>` and `s:<normalised source>` (`sourceKey`: IPv4 whole, IPv4-mapped IPv6 unmapped to IPv4, other IPv6 collapsed to its `/64`, a port dropped, anything else used as given). A failure prunes keys with no failure inside the window and no running lock. At the cap, a new key evicts the least recently failed unlocked key; when every key is locked, nothing is evicted, and `blocked` refuses an attempt that would need a new key until the shortest lock ends. Test var `t0` (2026-10-17T00:00:00Z).
- Task 4 (`service.go`, migration): the controller tables `core_users` (id, username unique, role, state `pending`/`active`, password_hash, totp_secret_ct, totp_last_step default 0, created_at, updated_at), `core_sessions` (id_hash primary key, user_id with on-delete cascade, created_at, last_seen_at, expires_at, source_ip, user_agent) and `core_tokens` (id, user_id with on-delete cascade, name, scope `read`/`operate`/`admin`, secret_hash, created_at, expires_at, last_used_at, revoked_at), all STRICT, times fixed-width RFC 3339 UTC text (`2006-01-02T15:04:05.000000000Z`). Only this package reads or writes them. `const RoleAdmin = "admin"`, `StatePending = "pending"`, `StateActive = "active"`. Types (JSON shapes of spec section 12, no `omitempty`, so absent times are `null`):
  - `type User struct { ID, Username, Role, State string; CreatedAt time.Time }` with tags `id`, `username`, `role`, `state`, `createdAt`;
  - `type TokenInfo struct { ID, Name string; Scope core.Scope; CreatedAt, ExpiresAt time.Time; LastUsedAt, RevokedAt *time.Time }` with tags `id`, `name`, `scope`, `createdAt`, `expiresAt`, `lastUsedAt`, `revokedAt`;
  - `type Identity struct { Principal core.Principal; FromSession bool; User User; ExpiresAt time.Time; Token *TokenInfo }` (`ExpiresAt` is when the session or token ends as of this request; `Token` is nil for a session);
  - `type Options struct { Params Params }` (zero `Params` means `DefaultParams`);
  - `type Service struct` (safe for concurrent use); `func New(db *store.DB, sealer core.Sealer, clock core.Clock, audit core.Audit, opts Options) *Service` (derives the CSRF key with `sealer.Derive(secret.CSRFLabel)`).

  User operations, the inputs and outputs of spec section 10's break-glass actions. Refusals are `*errcode.Error`: an invalid username, password or code, a taken username, a user that is not pending, and the last active admin are `request.invalid_input` with one field error at the named path; an unknown user is `not_found`. No message quotes a password, code or secret.
  - `CreateUser(ctx, username, password string) (User, string, error)`: a pending admin and its TOTP secret in unpadded base32, returned only here. Sealed in `core_users.totp_secret_ct` with associated data `core_users.totp_secret_ct.<user id>`. Paths: `/username`, `/password`.
  - `ActivateUser(ctx, username, code string) (User, error)`: pending to active; the code's step is stored, so the same code cannot then log in. Paths: `/username` (not pending), `/code`.
  - `ResetTOTP(ctx, username string) (User, string, error)`: new secret, back to pending, sessions ended, user's tokens kept.
  - `ResetPassword(ctx, username, password string) (User, error)`: sessions ended. Path `/password`.
  - `DeleteUser(ctx, username string) error`: deletes the user with its sessions and tokens; the last active admin is refused at `/username`.
  - `ListUsers(ctx) ([]User, error)`: sorted by username, empty non-nil slice when none.
  - `GetUser(ctx, username string) (User, error)`.

  Unexported, used by Tasks 5, 6 and 7: `timeFormat`, `formatTime(time.Time) string`, `parseTime(string) (time.Time, error)`, `(*Service).now() time.Time` (clock in UTC, monotonic reading stripped), `(*Service).newID() string` (ULID stamped by the clock, monotonic), `totpAD(userID) string`, `invalidField(path, message string) *errcode.Error`, `errUserNotFound()`, `querier` (a `*sql.Tx` or the reader pool), `userRecord` (`User` plus `passwordHash`, `totpCT`, `lastStep`), `loadUser(ctx, q querier, username string) (userRecord, error)` (absent is `not_found`), `endSessions(ctx, tx, userID) error`; `Service` fields `db`, `sealer`, `clock`, `audit`, `hasher`, `limiter`, `csrfKey`. Test helpers in `service_test.go`: `testPassword`, `newTestService(t) (*Service, *coretest.FakeClock, *coretest.MemAudit)`, `createActive(t, s, clock, username) (User, string)` (creates, activates, then advances the clock one TOTP period), `count(t, s, table, where, args...)`, `insertSession`, `insertToken`, `wantCode(t, err, code)`.
- Task 5 (`session.go`): `const SessionIdle = time.Hour`, `SessionLifetime = 12 * time.Hour`, `LastSeenInterval = time.Minute`; `(*Service) CSRFToken(sessionID string) string`: HMAC-SHA256 of the session id's bytes under the derived key, in unpadded base64url; `""` for a malformed id. `(*Service) VerifyCSRF(sessionID, token string) bool`: constant-time comparison, false for an empty token or a malformed id. A session id is 32 random bytes in unpadded base64url (43 characters); `core_sessions.id_hash` is the hex SHA-256 of those bytes. Unexported, used by Tasks 6 and 7: `errAuthRequired()` (`auth.required`, detail `authentication required`), `b64url`, `newSessionID() (string, []byte)`, `decodeSessionID(string) ([]byte, bool)`, `sessionHash([]byte) string`, `truncate(s string, n int) string`, `maxUserAgent = 512`, `(*Service).createSession(ctx, tx *sql.Tx, userID, source, userAgent string) (string, time.Time, error)` (returns the id and the idle expiry), `roleScope(role string) (core.Scope, bool)`, `(*Service).authenticateSession(ctx, id, source string) (Identity, error)` (principal `user:<id>`, scope `admin`, `last_seen_at` written at most once a minute, effective expiry the earlier of last seen plus one hour and created plus twelve hours), `(*Service).endSession(ctx, id string) (userID string, ok bool, err error)`. Test helpers `newSession(t, s, userID)`, `lastSeen(t, s, id)`.
- Task 6 (`token.go`): `const TokenPrefix = "dpt_"`, `DefaultTokenDays = 90`, `MaxTokenDays = 365`, `MaxTokenName = 64`, `LastUsedInterval = time.Minute`, `TokenRetention = 30 * 24 * time.Hour`. A token is `dpt_<ULID>_<secret>`, where the secret is 32 random bytes in unpadded standard base32 (52 characters, canonical encoding only); `core_tokens.secret_hash` is the hex SHA-256 of those bytes. The token operations below cover only `userID`'s tokens: another user's token is `not_found`.
  - `(*Service) CreateToken(ctx, userID, name string, scope core.Scope, days int) (TokenInfo, string, error)`: `days` 0 means 90; the token string is returned only here. Refusals: `/name` (1 to 64 characters), `/scope`, `/expiresInDays` (1 to 365); an unknown or inactive user is `not_found`.
  - `ListTokens(ctx, userID string) ([]TokenInfo, error)`: newest first, revoked and expired tokens included until purged, empty non-nil slice when none.
  - `GetToken(ctx, userID, id string) (TokenInfo, error)`.
  - `RevokeToken(ctx, userID, id string) (TokenInfo, error)`: a repeat keeps the first `revokedAt`.
  - `(*Service) Purge(ctx) error`: deletes sessions past either limit, and tokens whose `expires_at` or `revoked_at` is 30 days or more in the past.

  Unexported `(*Service).authenticateToken(ctx, token, source string) (Identity, error)`: principal `token:<id>`, `UserID` the owner, `TokenID` the id, `Scope` the token's scope; `last_used_at` written at most once a minute. A malformed, unknown, revoked or expired token, a wrong secret, or an inactive owner is `auth.required`. Test helper `day`.
- Task 7 (`login.go`): `const AuditLoginAction = "core.auth.login"`, `AuditLogoutAction = "core.auth.logout"`, `ActorAnonymous = "anonymous"`;
  - `type LoginRequest struct { Username, Password, Code, Source, UserAgent string }`;
  - `type LoginResult struct { SessionID, CSRFToken string; ExpiresAt time.Time; User User }`;
  - `type Credentials struct { Authorization []string; Session string; Source string }` (`Authorization` is every value of the header, nil when it is absent);
  - `(*Service) Login(ctx, r LoginRequest) (LoginResult, error)`;
  - `(*Service) Logout(ctx, sessionID, source string) error` (an unknown session is a no-op);
  - `(*Service) Authenticate(ctx, c Credentials) (Identity, error)`.

  `Login` errors:
  - Every evaluated failure (unknown user, wrong password, wrong or replayed code, pending user) and a username that fails the pattern return `auth.invalid_credentials` with detail `invalid username, password or code`.
  - A locked username or source returns `auth.rate_limited` (detail `too many failed login attempts; try again later`) with a `*RetryAfterError` cause, and the attempt is not evaluated.
  - Anything else (audit or database failure, cancelled context) passes through and maps to `internal`.

  The code check, the step update and the session insert share one write transaction. Each evaluated attempt writes one `core.AuditEntry` through `core.Audit.Record`, after the transaction and with `context.WithoutCancel`:
  - `ID` is a ULID the service makes; `Action` is `core.auth.login`, `Version` 1, `Phase` `login`; `At` comes from the clock and `Source` is the request's.
  - `Actor` is `user:<id>` when the username matched a user, else `anonymous`; `Input` is `{"username":"<submitted>"}`.
  - `Result` is `ok`, or `error` with `ErrorCode` `auth.invalid_credentials`.

  A logout row has `Action` `core.auth.logout`, `Phase` `logout`, `Actor` `user:<id>`, `Input` `{}` and `Result` `ok`. Blocked and pattern-refused attempts write no row. `Authenticate` considers only the bearer token when `Authorization` is non-nil: exactly one value of the form `Bearer <token>` (scheme case-insensitive), else `auth.required`, and the cookie is never consulted. Otherwise it uses the session cookie; with neither it returns `auth.required`.

**How the next parts use it**

- P9 (API):
  - Build `auth.Credentials{Authorization: r.Header.Values("Authorization"), Session: <cookie value>, Source: <client address>}` and call `Authenticate`. Only for a session principal (`FromSession`) on a method other than GET, HEAD or OPTIONS, refuse with `auth.csrf` unless `VerifyCSRF(<cookie value>, r.Header.Get("X-CSRF-Token"))`.
  - Login sets the cookie (name and attributes are P9's) to `LoginResult.SessionID`, and Logout calls `Logout(ctx, <cookie value>, source)`.
  - `GET /api/v1/auth/session` answers `{user: Identity.User, csrfToken: CSRFToken(<cookie value>), expiresAt: Identity.ExpiresAt}` for a session and `{token: *Identity.Token}` for a token.
  - The `Retry-After` value is `RetryAfterError.Seconds()`. P9's tests use a real `Service` over a temporary database with cheap `Params` (for example `auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}`).
- P12 (core actions) calls the operations with the principal's `UserID`:

  | Action | Service call |
  |---|---|
  | `core.user.list` | `ListUsers` |
  | `core.user.create` | `CreateUser(username, password)` → `{user, totpSecret}` |
  | `core.user.activate` | `ActivateUser(username, code)` |
  | `core.user.reset-totp` | `ResetTOTP(username)` → `{user, totpSecret}` |
  | `core.user.reset-password` | `ResetPassword(username, password)` |
  | `core.user.delete` | `DeleteUser(username)` |
  | `core.token.list` | `ListTokens(p.UserID)` |
  | `core.token.create` | `CreateToken(p.UserID, name, scope, expiresInDays)` → `TokenInfo` plus `token` |
  | `core.token.revoke` | `RevokeToken(p.UserID, id)` |

  Plans and `Verify` may read state with `GetUser`, `ListUsers`, `GetToken` and `ListTokens`. The service's `request.invalid_input` field paths match the action input fields, so P12 can pass the errors through. The engine's handler rule applies: no diff line, guard message or error detail may carry a password, code, TOTP secret or token, and the service's own messages never do.
- P14 (assembly) builds `auth.New(controllerDB, sealer, clock, audit, auth.Options{})` once, after migrations. It calls `Purge` at start and every 10 minutes (spec section 20).

**`internal/controller/listen`** (package `listen`, P10). Every file below is new; the package imports only `internal/core` (`Clock`, `Timer`, `Sealer`), `internal/core/store`, `github.com/oklog/ulid/v2`, `golang.org/x/sys/unix` and the standard library.

- Task 8 (`spec.go`, carries the package doc comment): `const LoopbackPort = 8080`, `const TailscalePort = 8443`; `type Kind uint8` with `KindLoopback`, `KindTailscale`, `KindHTTPS`, `KindHTTP`; `type Spec struct { Kind Kind; Addr netip.AddrPort }` (`Addr` set for `KindHTTPS` and `KindHTTP`) with `String()` returning the command-line form (`loopback`, `tailscale`, `https://<addr>`, `http://<addr>`). `func ParseSpecs(values []string) ([]Spec, error)`: no values means `[loopback, tailscale]`; repeated values count once; values are `loopback`, `tailscale`, `https://<ip>:<port>`, `http://<loopback ip>:<port>` (IP literals only, IPv4-mapped addresses unmapped). Errors (all prefixed `listen: `): `"<v>" is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>`; `"<v>": plain HTTP is allowed only on a loopback address; use https://<addr>`; `"<v>" needs a port between 1 and 65535`; `"<v>": the address must be a specific address of this host` (unspecified or multicast); `"<v>": the address must not carry a zone`; `<addr> is listed as both HTTP and HTTPS`. `func HasTailscale(specs []Spec) bool`. `type Endpoint struct { Addr netip.AddrPort; TLS, FreeBind, Optional bool }` (comparable; `FreeBind` on every Tailscale endpoint; `Optional` only on `[::1]:8080` from `loopback`) with `URL() string` (`https://100.64.0.1:8443`, `http://[::1]:8080`). `func Endpoints(specs []Spec, tailscale []netip.Addr) []Endpoint`: `loopback` gives `127.0.0.1:8080` and `[::1]:8080` (optional), `tailscale` gives `<addr>:8443` TLS FreeBind per address, explicit values give themselves; one address listed twice is one endpoint (FreeBind ORed, Optional ANDed); sorted by address.
- Task 9 (`tailscale.go`): `const TailscaleInterface = "tailscale0"`; `func TailscaleAddrs(list []netip.Addr) []netip.Addr` keeps addresses inside `100.64.0.0/10` or `fd7a:115c:a1e0::/48` (unmapped, sorted, no repeats; nil when none); `type InterfaceAddrs func(name string) (addrs []netip.Addr, present bool, err error)`; `func SystemInterfaceAddrs(name string) ([]netip.Addr, bool, error)` (production; `present` false when no interface has that name); `func LoadTailscaleAddrs(ctx context.Context, db *store.DB) ([]netip.Addr, error)` (nil when no row or an empty list; an unparsable value fails with an error naming `core_meta tailscale_addrs`); `func SaveTailscaleAddrs(ctx context.Context, db *store.DB, addrs []netip.Addr) error`. Stored format: `core_meta` key `tailscale_addrs`, value a JSON array of address strings such as `["100.64.0.1","fd7a:115c:a1e0::1"]`, `[]` for none.
- Task 12 (`authority.go`): `const PurposeWeb = "web"`; `type WebCA interface { RootPEM() []byte; Fingerprint() string }` — the only surface P9's controller-socket route `GET /api/v1/controller/web-ca` needs (P9 may declare the same two-method interface locally instead of importing this package; `*Authority` satisfies both); `type Authority struct` (unexported fields) with `RootPEM() []byte` (one PEM `CERTIFICATE` block, a fresh copy per call) and `Fingerprint() string` (lowercase hex SHA-256 of the root's DER, 64 characters, no separators; P13's CLI computes the same over the presented root to compare with `caFingerprint`); `func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error)`. Stored format: one `core_ca` row, `id` a ULID, `purpose` `web`, `cert_pem` the PEM, `key_ct` = `sealer.Seal("core_ca.key_ct.<id>", <PKCS #8 DER of the ECDSA P-256 key>)`, `created_at` RFC 3339 UTC. The root: ECDSA P-256, subject CN `darkpanel web root <id>`, valid from one hour before creation to 10 years after (`AddDate(10, 0, 0)`), CA with `MaxPathLen` 0, key usage cert sign and CRL sign. Overlapping first starts agree on one row: the insert happens only inside a `store.DB.Write` (immediate) transaction that finds no `web` row. Every load error is prefixed `listen: web authority`.
- Task 13 (`leaf.go`): `const LeafValidity = 90 * 24 * time.Hour`, `const LeafRenewAfter = 60 * 24 * time.Hour`; `type Leaf struct`; `func NewLeaf(a *Authority, clock core.Clock, hostname string) *Leaf`; `(*Leaf) Ensure(ips []netip.Addr) (issued bool, err error)` issues a new leaf when none exists, when the normalised IP set differs from the current leaf's, or when `LeafRenewAfter` has passed since it was issued; `(*Leaf) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error)` returns the chain `[leaf, root]` (error before the first `Ensure`); `(*Leaf) TLSConfig() *tls.Config` = `MinVersion: tls.VersionTLS12` plus `GetCertificate`. The leaf: ECDSA P-256, DNS SANs `[<hostname>, localhost]` (only `localhost` when the hostname is empty or `localhost`), IP SANs = the `Ensure` addresses, valid from one hour before issue to `LeafValidity` after, digital-signature key usage, server-auth extended key usage, in memory only.
- Task 10 (`bind.go`): `type BindFunc func(ctx context.Context, e Endpoint) (net.Listener, error)`; `func Bind(ctx context.Context, e Endpoint) (net.Listener, error)` — `tcp4` or `tcp6` by address family, `IP_FREEBIND`/`IPV6_FREEBIND` set before bind when `e.FreeBind`; errors read `listen: bind <e.URL()>: <cause>` and wrap the `syscall.Errno`.
- Task 14 (`set.go`): `const ReadHeaderTimeout = 10 * time.Second`, `ReadTimeout = 30 * time.Second`, `WriteTimeout = 60 * time.Second`, `IdleTimeout = 120 * time.Second`; `var ErrClosed`; `type SetConfig struct { Handler http.Handler; TLS *tls.Config; Bind BindFunc; Log *slog.Logger }` (nil `Bind` means `Bind`, nil `Log` discards); `type Bound struct { Endpoint Endpoint; Addr net.Addr }`; `type Set struct`; `func NewSet(cfg SetConfig) *Set`; `(*Set) Update(ctx context.Context, want []Endpoint) error` (stops removed endpoints first, binds missing ones, leaves unchanged ones alone; an `Optional` endpoint failing with `EADDRNOTAVAIL` or `EAFNOSUPPORT` is skipped once with a log line and never retried; other bind failures are returned joined while the rest keep serving); `(*Set) Bound() []Bound` (sorted by endpoint address); `(*Set) Shutdown(ctx context.Context) error` (graceful until `ctx` ends; later `Update` returns `ErrClosed`). HTTPS endpoints terminate TLS inside `http.Server`, so **P9 tells an HTTPS request by `r.TLS != nil`** (session cookie name, HSTS); the plain-HTTP loopback listener has `r.TLS == nil`.
- Task 15 (`service.go`): `const CheckInterval = 10 * time.Second`; `type Config struct { Specs []Spec; Hostname string; DB *store.DB; Authority *Authority; Clock core.Clock; Handler http.Handler; Log *slog.Logger; Interfaces InterfaceAddrs; Bind BindFunc }` (nil `Log` discards, nil `Interfaces` means `SystemInterfaceAddrs`, nil `Bind` means `Bind`); `type State struct { TailscaleBound bool; Message string; Listeners []Bound }`; `type Service struct`; `func Start(ctx context.Context, cfg Config) (*Service, error)`; `(*Service) State() State` (lock-free copy; the UI footer and `serve`'s log read `TailscaleBound` and `Message`); `(*Service) Shutdown(ctx context.Context) error`. Messages, exactly: `Tailscale listener disabled by --listen.`, `Serving on Tailscale: <url>, <url>.`, `Tailscale addresses found but not bound; see the serve log.`, `No Tailscale address found; serving loopback only.`, `No Tailscale address found; not serving on the tailnet.`, and after Shutdown `The listeners are shut down.`; each change is logged once as `listen: <message>`. Plan 4's server (P14) calls, after migrations: `specs, err := listen.ParseSpecs(<--listen values>)` (a usage error), `auth, err := listen.LoadOrCreateAuthority(ctx, controllerDB, controllerSealer, clock)`, builds the API handler (passing `auth` as the socket's `WebCA` and the service's state to the footer route), then `svc, err := listen.Start(ctx, listen.Config{...Hostname: os.Hostname()...})`, and `svc.Shutdown(ctx)` on exit.
- Test helpers shared between tasks: `tailscale_test.go` (Task 9: `openControllerDB(t)`, `openControllerDBAt(t, path)`, `addrs(...string)`), used by Tasks 12, 13 and 15; `authority_test.go` (Task 12: `testStart`, `newSealer(t)`, `rootCert(t, a)`), used by Tasks 13 and 15; `set_test.go` (Task 14: `fakeBinder`, `testTLS`, `schemeHandler`, `newTestSet`, `get`, `boundAddr`, `epHTTP`, `epTS1`, `epTS2`, `epTS3`, `epLoop6`), used by Task 15; `leaf_test.go` (Task 13: `newLeaf`, `current`).

Part P9 adds package `internal/controller/api` only: no migration, no new error code, no dependency. P10 (listeners), P12 (core actions), P13 (CLI), plan 4's dispatch (P11) and assembly (P14) code against the exported names below; the unexported names are listed because later tasks of this part consume them.

**`internal/controller/api`** (package `api`, P9)

- Task 11 (`wire.go`, carries the package doc comment; `middleware.go`):
  - `const MaxBodyBytes = 1 << 20` (spec section 12).
  - Unexported `problem` struct, the RFC 9457 body: `type` (always `"about:blank"`), `title`, `status`, `detail`, `code` (`errcode.Code`), `errors` (`[]errcode.FieldError`, `omitempty`), served as `application/problem+json`.
  - `writeProblem(w http.ResponseWriter, log *slog.Logger, err error)`: takes `errcode.From(err)`; a code outside the registry becomes `internal`; the status is `Code.Status()` (the table of spec section 12 as `errcode` holds it). A 5xx answer carries the code's title as `detail` and no `errors`, and logs `"api: request failed"` with the code and the full error. An empty detail becomes the title. For `auth.rate_limited` with a `*auth.RetryAfterError` cause it sets `Retry-After: <Seconds()>`.
  - `writeJSON(w, log, status int, v any)`: `Content-Type: application/json`; a value that does not marshal (invalid raw output) is answered `internal`.
  - `decodeBody(w, r, dst any) error`: `request.unsupported_media_type` unless the media type is `application/json` (parameters allowed); at most `MaxBodyBytes` (`request.too_large`); unknown fields, a non-object, malformed or truncated JSON, an empty body or a second value are `request.invalid_json`, whose detail names the offending field or byte offset and never echoes a value.
  - `const ContentSecurityPolicy` (the exact policy of spec section 12); `const healthzPath = "/healthz"`; `isAPIPath(path string) bool` (`/api` or under `/api/`).
  - `securityHeaders(next http.Handler) http.Handler`: CSP, `X-Content-Type-Options: nosniff`, `Referrer-Policy: no-referrer`, `Cross-Origin-Opener-Policy: same-origin` on every response; `Strict-Transport-Security: max-age=31536000` only when `Request.TLS != nil`; `Cache-Control: no-store` on API paths and `/healthz`.
  - `recoverPanics(log, next) http.Handler`: logs `"api: handler panic"` with method, path, panic value and stack and answers `internal`; when the response has started it re-panics `http.ErrAbortHandler` (the server drops the connection); an `http.ErrAbortHandler` panic passes through unlogged.
  - Test helpers in `wire_test.go`: `testLog() (*slog.Logger, *bytes.Buffer)`, `decodeProblem(t, rec, status int, code errcode.Code) problem`.
- Task 16 (`scope.go`): `func RequiredScope(class core.Class, side core.Side) core.Scope` (spec section 10): class `read` on any side → `read`; `revertible`, `compensable` or `irreversible` on `agent` → `operate`; those on `controller` → `admin`; an unknown class or side → `admin`. Plan 4's registry tests may assert it for every registered action. `authorize(p core.Principal, need core.Scope) error` → `auth.forbidden` with detail `this request needs the <need> scope` unless `p.Scope.Allows(need)`.
- Task 17 (`static.go`): `func EmbeddedUI() fs.FS` (`web.Dist` under `dist`, the value production passes as `Config.UI`); `serveUI(w, r, ui fs.FS) bool` serves the regular file at the cleaned request path, else `index.html`, through `http.ServeContent` (content type by extension, `Range`, `HEAD`), and returns false without writing when neither exists or `ui` is nil. Test fixture `testUI fstest.MapFS` (`index.html`, `assets/app.js`, `theme-init.js`).
- Task 19 (`server.go`, `routes.go`):
  - `const SessionCookieSecure = "__Host-darkpanel_session"`, `SessionCookiePlain = "darkpanel_session"`, `CSRFHeader = "X-CSRF-Token"`; unexported `loginPath = "/api/v1/auth/login"`.
  - `type Authenticator interface { Login(ctx, auth.LoginRequest) (auth.LoginResult, error); Logout(ctx, sessionID, source string) error; Authenticate(ctx, auth.Credentials) (auth.Identity, error); CSRFToken(sessionID string) string; VerifyCSRF(sessionID, token string) bool }`, which `*auth.Service` satisfies (compile-time check).
  - `type WebCA interface { RootPEM() []byte; Fingerprint() string }`: the method set of P10's `listen.WebCA`, so `*listen.Authority` satisfies it.
  - `type Config struct { Dispatcher core.Dispatcher; Auth Authenticator; Registry *core.Registry; WebCA WebCA; SchemaVersion func(ctx context.Context) (int, error); HostID string; Dev bool; UI fs.FS; Log *slog.Logger }`; every field but `UI` (nil serves no UI) and `Log` (nil discards) is required.
  - `type Server struct` (handlers safe for concurrent use); `func New(cfg Config) (*Server, error)` (error naming the required fields when one is missing).
  - `(*Server) Handler() http.Handler`: the one handler for every network listener, chained `securityHeaders` → `recoverPanics` → `authenticate` → mux. A request is HTTPS exactly when `Request.TLS != nil` (P10's `listen.Set` guarantees it); that picks the session cookie name and HSTS.
  - Unexported: `caller{principal core.Principal; identity auth.Identity; session string}`, `withCaller`, `callerFrom`, `sessionCookieName(r)`, `clientAddr(r)` (host part of `RemoteAddr`; forwarded headers ignored), `access` with `accessAuthenticated`, `accessPublic`, `accessOptional`, `routeAccess(method, path string) access` (public: `POST /api/v1/auth/login`; optional: `GET`/`HEAD /healthz`; public: `GET`/`HEAD` of any non-API path; everything else authenticated), `safeMethod`, `(*Server).identify(r) (caller, error)`, `(*Server).authenticate(next)`, `requireCaller(log, next)`, `(*Server).networkFallback`, `(*Server).unrouted(w, r, mux, ui bool)`, `allowedMethods(mux, r)`.
  - `routes.go`: `route{method, path, summary string; public bool; handler http.HandlerFunc}` (Task 21 adds `perAction bool`), `(*Server).networkRoutes() []route`, and `func Routes() []schema.Route` (the hand-written routes that `schema.OpenAPI` takes).
  - Test harness in `server_test.go`: `testStart` (2026-10-17T12:00:00Z), `testPassword`, `testHostID`, `testRemote` (`"192.0.2.10:51234"`), `testParams` (cheap argon2id), `demoInput`, `demoOutput`, `demoStatus`, `demoHandler[In, Out]`, `demoModule` (module `demo`: agent-side `demo.item.get` read, `demo.item.add` revertible, `demo.item.purge` irreversible; controller-side `demo.item.list` read, `demo.item.remove` compensable; break-glass `demo.secret.reset` irreversible and `demo.secret.show` read; resource `demo.status`), `fakeWebCA`, `harness{t, srv, auth, clock, audit, disp, user, secret, log}`, `newHarness(t)` (real `auth.Service` over a temporary migrated database with one active admin `admin`), `(*harness).code()`, `login() (session, csrf string)`, `token(scope) string`, `serve(r)`, `through(r)` (runs `authenticate` around a `probe`), `request(method, target, body)`, `withSession(r, session, csrf)`, `withToken(r, token)`, `probe`, `allMethods`.
- Task 20 (`auth.go`): routes `POST /api/v1/auth/login` (public; body `{username, password, code}`), `POST /api/v1/auth/logout` (`204`; ends a session principal's session; clears the cookie for any principal), `GET /api/v1/auth/session`. Unexported `loginRequest`, `sessionResponse{user, csrfToken, expiresAt, token}` (all four always present: a session has `token: null`; a token has `csrfToken: null` and `user` its owner, `expiresAt` the token's expiry), `sessionCookie(r, value string, maxAge int) *http.Cookie` (name per listener, `Path=/`, `HttpOnly`, `SameSite=Strict`, `Secure` exactly on HTTPS, no `Domain`, no expiry; `maxAge -1` clears). Test helpers `loginBody`, `sessionCookieOf`, `sessionBody`.
- Task 21 (`actions.go`; `routes.go` gains `perAction`):
  - Routes: `GET /api/v1/hosts/{host}/resources/{module}/{resource}` (raw output; `not_found` for a resource the registry lacks); `POST /api/v1/hosts/{host}/actions/{name}/{version}/{plan|apply|read}` (agent-side actions) and `POST /api/v1/actions/{name}/{version}/{plan|apply|read}` (controller-side), all `perAction`; `GET /api/v1/changes` (query `state`, `hostId`, `before`, `limit` 1-500; field errors at `state` and `limit`), `GET /api/v1/changes/{id}`, `POST /api/v1/changes/{id}/confirm`, `POST /api/v1/changes/{id}/revert`.
  - Bodies: plan and read `{input}` (missing → `request.invalid_input` at `/input`); apply `{planId, confirmTimeoutSeconds?, confirmation?, confirmNow?}` (missing `planId` → `/planId`; a timeout other than 0 outside `lifecycle.MinConfirmTimeout`..`MaxConfirmTimeout` in seconds → `/confirmTimeoutSeconds`).
  - Wire types: `planResponse` (`diff` and `guards` are `[]`, never `null`; `hostId`, `confirmation` null when empty), `changeResponse` (`hostId`, `revertDeadline`, `output`, `error`, `compensatedBy` null when absent), `changeError`, `changesResponse` (`changes` is `[]` when empty), `readResponse{output}`, `inputRequest`, `applyRequest`; helpers `nullable`, `orNull`, `toPlan`, `toChange`, `invalidField(path, message)`; constants `opPlan`, `opApply`, `opRead`, `maxChangeLimit = 500`, `changeStates`.
  - `(*Server).lookupAction(name, version string, side core.Side, op string) (core.ActionDef, error)`: `action.unknown` unless `core.ParseVersion` accepts the segment, the registry has the action, its side is `side`, and `op` is `read` exactly when the class is `read`.
  - `(*Server).actionHandler(side, op)`: break-glass → `auth.forbidden` (`break-glass actions are served only on the controller socket`), then `authorize(RequiredScope(class, side))`, then `runAction`.
  - `(*Server).runAction(w, r, p, hostID string, def core.ActionDef, op string)`: always passes `def.Ref()` (the route's action) and the route's `hostID` to the dispatcher; apply answers 200 with the `Change` whatever its outcome.
  - `(*Server).authorizeChange(p, v core.ChangeView) error` and `changeOp(confirm bool)`: confirm and revert first read the change with `Dispatcher.Change(ctx, p, id)` (which needs `read`), then authorize against the change's own action: break-glass → `auth.forbidden`; registered → `RequiredScope` of its class and side; unregistered controller-side → `auth.forbidden`; unregistered agent-side → `RequiredScope(change class, agent)`.
  - Test helpers `call`, `changeTime`, `deadline`, `(*harness).stubDispatcher(last *call)` (change ids `c1` controller compensable, `cbg` break-glass, `cunk` unregistered controller action, `cua` unregistered agent action, `missing` → `change.not_found`, any other id a pending `demo.item.add` change on `h1`).
- Task 22 (`meta.go`): `GET /healthz` (public, optional credentials: `{"status":"ok"}` alone, or with a `read`-capable caller `{status, version, commit, schemaVersion}` from `buildinfo` and `Config.SchemaVersion`), `GET /api/v1/openapi.json` (`schema.OpenAPI(cfg.Registry, Routes())`, built per request). Unexported `healthStatus`, `healthDetail`, `handleHealthz`, `writeHealthDetail(w, r)`, `handleOpenAPI`. The final `Routes()` is, in order: `POST /api/v1/auth/login` (public), `POST /api/v1/auth/logout`, `GET /api/v1/auth/session`, `GET /api/v1/changes`, `GET /api/v1/changes/{id}`, `POST /api/v1/changes/{id}/confirm`, `POST /api/v1/changes/{id}/revert`, `GET /healthz` (public), `GET /api/v1/openapi.json`.
- Task 25 (`socket.go`):
  - `const SocketSource = "socket"`; `type PeerUIDFunc func(c net.Conn) (uint32, error)`; `func SocketPeerUID(c net.Conn) (uint32, error)` (`SO_PEERCRED` through `unix.GetsockoptUcred`; an error for a non-unix connection).
  - `func ListenSocket(path string) (net.Listener, error)`: refuses a path that is not a socket or that a live process serves (a dial succeeds), removes a stale socket, listens, and chmods the file to 0600. Go's unix listener removes the file on `Close`.
  - `(*Server) SocketListener(l net.Listener, peerUID PeerUIDFunc) net.Listener` (nil `peerUID` means `SocketPeerUID`): `Accept` closes, with a warning log, every connection whose uid is unreadable or not allowed. Allowed: uid 0 always; the service's own uid (`os.Getuid`, through the test variable `getuid`) only when `Config.Dev`.
  - `(*Server) SocketServer() *http.Server`: `SocketHandler()` with `ReadHeaderTimeout` 10 s, `ReadTimeout` 30 s, `WriteTimeout` 60 s, `IdleTimeout` 120 s, and a `ConnContext` attaching the accepted connection's uid. Serve it on `SocketListener`'s result.
  - `(*Server) SocketHandler() http.Handler`: its own mux with `GET /healthz` (always full detail), `GET /api/v1/controller/web-ca` → `{"pem": <RootPEM as a string>, "fingerprint": <Fingerprint()>}` (`webCAResponse`), and `POST /api/v1/actions/{name}/{version}/{plan|apply|read}` for controller-side break-glass actions only (another action → `auth.forbidden`); unknown routes `not_found` or `method_not_allowed`. A request whose context lacks an allowed uid is `auth.forbidden`. The principal is `{Actor: "local:root@<HostID>" for uid 0, "local:uid-<n>@<HostID>" otherwise; Scope: admin; Source: "socket"}`; no session, token or CSRF applies.
  - Unexported `socketAllowed`, `socketPrincipal`, `peerListener`, `peerConn`, `withPeerUID`, `peerUIDFrom`, `socketPeer`, `handleWebCA`, `socketAction(op)`.
- Task 27 (`coverage.go`): `func CheckOpenAPI(reg *core.Registry) error` checks the route-coverage invariant of spec section 7. Every hand-written network route must appear in the document as itself. Every per-action route must appear once for each non-break-glass action (of its side, with its operation) or resource of `reg`. Every document operation must be served by a network route, and no document path may name a break-glass action. Unexported `checkOpenAPI(reg, routes []route, doc []byte) error`, `instances(reg, rt route) []string`.

**How the next parts use it**

- P10 (listeners): `listen.Config.Handler` is `srv.Handler()`. P10's `listen.Set` already guarantees that `Request.TLS` is set exactly on HTTPS, which this part relies on for the cookie name and HSTS, and P10's `*listen.Authority` satisfies `api.WebCA`.
- P14 (assembly, plan 4):
  - Build the server with `api.New(api.Config{Dispatcher: <dispatch>, Auth: <*auth.Service>, Registry: <registry.BuildWith(...)>, WebCA: <*listen.Authority>, SchemaVersion: func(ctx context.Context) (int, error) { return store.SchemaVersion(ctx, controllerDB, store.SideController) }, HostID: <local host id>, Dev: <--dev>, UI: api.EmbeddedUI(), Log: <logger>})`.
  - Serve the socket with `ln, err := api.ListenSocket(<socket path from paths.Paths>)`, `srv := s.SocketServer()`, `go srv.Serve(s.SocketListener(ln, nil))`, and `srv.Shutdown(ctx)` on stop.
  - Its assembly test calls `api.CheckOpenAPI(reg)` with the real registry. The deny-by-default end-to-end test can reuse this part's matrix shape against the assembled handler.
- P12 (core actions): break-glass actions reach handlers only through the socket, with the principal above. Token actions see `Principal.UserID` from P5's identity.
- P13 (CLI) and the web UI:
  - Problem details carry `code`. A login answers `Set-Cookie` plus `{user, csrfToken, expiresAt, token}`, and `auth.rate_limited` carries `Retry-After`.
  - `controller web-ca` reads `{pem, fingerprint}` from the socket.
  - The CLI sends `Content-Type: application/json` on every request with a body.

**Obligations on later plans**

1. Plan 4 dispatch (P11): `Dispatcher.Apply(ctx, p, hostID, ref, planID, opts)` must refuse with `plan.not_found` a `planID` whose recorded host, action or version differ from the `(hostID, ref)` arguments. The API decides scope and break-glass from the route's action and always passes that action's `Ref()` and the route's host (Task 21), so the gate and the applied action can disagree only if the dispatcher ignores those arguments.
2. Plan 4 dispatch (P11): `Dispatcher.Change(ctx, p, id)` must return the change's real `Action`, `Version` and `HostID` (empty for controller-side changes), and `Confirm` and `Revert` must act on that same change. The API authorizes confirm and revert from that view (Task 21).
3. Plan 4 dispatch (P11) and every handler: the `detail` of any `*errcode.Error` below status 500 reaches the client verbatim. Dispatcher, engine and handler refusals must therefore carry client-safe details: no secret, no path, no SQL text. Anything else must be returned as a plain error or wrapped as `internal`, which the API reduces to its title.
4. Plan 4 assembly (P14): the controller socket's runtime directory must not be writable by other users. `ListenSocket` creates the file with the process umask and chmods it to 0600 right after; the `SO_PEERCRED` check on every connection covers that window.
5. Plan 4 assembly (P14): production passes `UI: api.EmbeddedUI()`. Its assembly test calls `api.CheckOpenAPI` with the real registry and runs the deny-by-default matrix over the assembled network handler (register row 12's end-to-end requirement).
6. Plan 4 assembly (P14): the service's own non-root uid is accepted on the socket only with `--dev`. A production service running as a dedicated user is reachable on the socket by root alone (see Assumptions).

Part P12 takes over the foundation's stub package `internal/controller/coreactions` (`module.go`, `module_test.go`). It adds no migration and no error code, and it edits no package outside `internal/controller/coreactions`.

**`internal/controller/coreactions`** (package `coreactions`, P12)

- Task 18 (`module.go`, `hosts.go`):
  - `type Deps struct { Auth *auth.Service; Audit core.Audit; Modules []core.Module }`. `func Module(d Deps) core.Module` keeps the stub's signature, so `registry.Build()` still calls `coreactions.Module(coreactions.Deps{})`.
  - The manifest is id `core`, version 1, `DefaultEnabled: true`, with no resources, migrations or preflight checks. `Deps` changes no declaration and no schema, so the zero-`Deps` registry that `registry.Build()` returns (used by the CLI and the schema generator) and the server's registry have identical actions and snapshots.
  - A handler whose dependency is missing returns a plain error. The engine turns it into `internal`: `Deps.Auth` for every token and user action, `Deps.Audit` for `core.audit.list`, and `Env.Agents` for `core.module.enable`'s apply.
  - `type NoInput struct{}` is the input of `core.host.list`, `core.token.list` and `core.user.list`. `type Empty struct{}` is the output of `core.user.delete`.
  - `const ReasonNotReported = "not reported by the host yet"`.
  - `type ModuleState struct { ID string; Version int; Enabled, Available bool; Reason *string; CheckedAt *time.Time }`, JSON `id, version, enabled, available, reason, checkedAt`.
    - `Version` is the module's version in `Deps.Modules`. `Reason` is null when the module is available.
    - A compiled-in module with no `core_module_state` row has `enabled: false, available: false, reason: ReasonNotReported, checkedAt: null`. Rows of modules that are not in `Deps.Modules` are left out.
    - The core module has no row and is never listed.
  - `type Host struct { ID, Name string; IsLocal, Online bool; Modules []ModuleState }`, JSON `id, name, isLocal, online, modules`. `Online` equals `IsLocal` in M1.
  - `type HostList struct { Hosts []Host }`: the local host first, then by name and id.
  - `type ModuleListInput struct { HostID string }` (`hostId`, minLength 1) and `type ModuleList struct { Modules []ModuleState }` (in `Deps.Modules` order). An unknown host is `not_found`.
  - Unexported names used by later tasks: the `module` struct (fields `d Deps` and `catalog []moduleInfo`, where `moduleInfo` is `{id string; version int}`), the method `(module) hostActions()`, `querier` (a `*sql.DB` or `*sql.Tx`), `hostExists(ctx, q, hostID) error` (`not_found` for an unknown host), `(module) moduleStates(ctx, q, hostID) ([]ModuleState, error)` and `parseTime`.
  - Test helpers in `module_test.go`:
    - `t0`, the fixed start time;
    - `fakeModule{id, version}`, a module with no actions;
    - `testEnv`, with fields `t`, `db`, `clock`, `auth`, `audit`, `hosts` (`coretest.MapHosts`), `reg` and `engine`;
    - `root`, the socket principal `local:root@h1`;
    - `newTestEnv(t, mods...)`, which builds a migrated controller database, a real `auth.Service` with cheap argon2id parameters, a `coretest.MemAudit`, the core module over `mods`, and a real `lifecycle.Engine` with `schema.NewValidator()`, the production `redact` and `Base.Agents = hosts`;
    - `ref(name)`, `encodeInput`, `(e) read(p, name, in, out) error`, `(e) exec`, `(e) addHost(id, name, local)`, `(e) setModuleState(hostID, moduleID, enabled, available, reason)`, `wantCode`, `ptr` and `jsonEqual`.
- Task 23 (`enablement.go`):
  - `const LockModule = "core.module"`.
  - `type ModuleInput struct { HostID, ModuleID string }` (`hostId`, `moduleId`, both minLength 1).
  - The actions `core.module.enable/v1` and `core.module.disable/v1` (table below).
  - Unexported names used by later tasks: `errUnset(name string) error`, `stateHash(parts ...string) string` (hex SHA-256 over length-prefixed parts), `timeFormat`, `formatTime`, `(module) target`, `(module) state` and `wantEnabled`.
  - Test helpers in `enablement_test.go`:
    - `operator`, a network principal `user:u1` with the admin scope;
    - `(e) plan`, `(e) apply` and `(e) run(p, name, in, out) core.ChangeView`, which plans, applies with the plan's phrase and requires `completed`;
    - `(e) verify(name, in) error`, which runs `Verify` as recovery does: actor `system:recovery`, no `UserID`, output `null`;
    - `preflight(status, &calls) *coretest.FakeAgent` and `newModuleEnv(t)`.
- Task 24 (`tokens.go`):
  - `type TokenList struct { Tokens []auth.TokenInfo }`.
  - `type TokenCreateInput struct { Name string; Scope core.Scope; ExpiresInDays int }`. JSON: `name` is 1 to 64 characters, `scope` is the enum `read`/`operate`/`admin`, and `expiresInDays` is `omitempty`, from 1 to 365.
  - `type NewToken struct { auth.TokenInfo; Token string }`. The embedded struct is flattened, so the JSON is TokenInfo's fields plus `token`, which is `secret:"true"`.
  - `type TokenRevokeInput struct { ID string }` (`id`).
  - Unexported: `(module) authService() (*auth.Service, error)` and `(module) tokenOwner(p) (*auth.Service, string, error)`. The second refuses a principal without a `UserID` with `auth.forbidden`.
  - Test helpers in `tokens_test.go`: `testPassword`, `noSecret(t, secrets, values...)` (fails when any secret is a substring of a value rendered with `%+v`, `Error()` or JSON) and `(e) activeUser(username) (auth.User, core.Principal)`.
- Task 26 (`audit.go`):
  - `const DefaultAuditLimit = 100`, `MaxAuditLimit = 500`.
  - `type AuditListInput struct { Limit int; Before, Actor, HostID, Action string }`. All fields are `omitempty`; `limit` runs from 1 to 500.
  - `type AuditList struct { Entries []AuditEntry; NextBefore *string }`.
  - `type AuditEntry struct`, with JSON `id, at, actor, hostId, action, version, phase, changeId, input, result, errorCode, source`. `hostId`, `changeId`, `errorCode` and `source` are null when the stored value is empty.
  - `type AuditInput json.RawMessage`. It has `MarshalJSON`, `UnmarshalJSON` and `JSONSchema()`, whose recursive any-JSON schema sits in `$defs` as `AuditInput`. Without it, `secret.RedactJSON` redacts every property of the input, because no schema names them.
  - Test helper `(e) fillAudit()`.
- Task 28 (`users.go`):
  - `type UserList struct { Users []auth.User }`.
  - `type UsernameInput struct { Username string }`; `username` carries the pattern `^[a-z0-9][a-z0-9._-]{0,63}$`.
  - `type UserPasswordInput struct { Username, Password string }`. `password` is `secret:"true"` and 12 to 128 characters, so `schema.Input` marks it `x-secret` and `writeOnly`.
  - `type Enrollment struct { User auth.User; TOTPSecret string }`, JSON `user` and `totpSecret`, which is `secret:"true"`.
  - Unexported: `(module) userState(ctx, username) (*auth.Service, auth.User, bool, error)`. The bool reports that the user is the last active admin; an unknown user is `not_found`. `wantUserState(ctx, m, username, state) error`.
  - Test helpers in `users_test.go`: `newPassword`, `(e) login(username, password, totpSecret) error`, `(e) change(id) core.ChangeView` and `(e) auditEntries()`.
- Task 29 (`enroll.go`): `type UserActivateInput struct { Username, Code string }`. `code` is `secret:"true"` with the pattern `^[0-9]{6}$`. Test helper `(e) wrongCode(totpSecret) string`.
- Task 30 (`registry_test.go`, package `coreactions_test`) produces nothing.

**The fourteen actions** (all version 1 and `Side: controller`). "Hash" is what `PlanOutcome.StateHash` covers, so the engine's recheck refuses with `plan.state_changed` when it changes. A failing guard refuses the apply with `plan.guard_failed`.

| Action | Class | Break-glass | Lock | Compensated by | Phrase | Hash | Guards |
|---|---|---|---|---|---|---|---|
| `core.host.list` | read | no | - | - | - | - | - |
| `core.module.list` | read | no | - | - | - | - | - |
| `core.module.enable` | compensable | no | `core.module` | `core.module.disable` | - | host, module, enablement (`enabled`/`disabled`/`not reported`) | none |
| `core.module.disable` | compensable | no | `core.module` | `core.module.enable` | - | same as enable | none |
| `core.token.list` | read | no | - | - | - | - | - |
| `core.token.create` | compensable | no | - | `core.token.revoke` | - | owner id and state | none |
| `core.token.revoke` | irreversible | no | - | - | token id | user id, token id, `revokedAt` or `active` | none |
| `core.audit.list` | read | no | - | - | - | - | - |
| `core.user.list` | read | yes | - | - | - | - | - |
| `core.user.create` | compensable | yes | - | `core.user.delete` | - | username, taken | `username-free` |
| `core.user.activate` | compensable | yes | - | `core.user.reset-totp` | - | user id, state | `user-pending` |
| `core.user.reset-totp` | irreversible | yes | - | - | username | user id, state, last active admin | none |
| `core.user.reset-password` | irreversible | yes | - | - | username | user id | none |
| `core.user.delete` | irreversible | yes | - | - | username | user id, state, last active admin | `not-last-admin` |

How each action behaves:

- **Diffs and guards.** Every mutating plan returns `Guards: []core.Guard{}` or a guard list, never nil. Every compensable plan's diff ends with the line `compensated by <action>`; the token and user lines add what the compensation needs, without a secret. No diff line, guard message or error detail carries a password, TOTP code, TOTP secret or token. Tasks 24, 28 and 29 test this with `noSecret` over plan views, stored changes, audit entries and errors.
- **Plans are read-only.** They read through `Env.DB.Read()` or the `auth.Service` read methods (`ListUsers`, `GetUser`, `GetToken`). `core.module.enable` runs no preflight in `Plan`.
- **Module enablement.**
  - `core.module.enable` applies by calling `Env.Agents.Agent(ctx, hostID)` and then `AgentClient.Preflight(ctx, moduleID)`. An agent error passes through and fails the change.
  - It then upserts `core_module_state` in one `Env.DB.Write`: `enabled = 1`, availability and reason from the preflight, `checked_at = Clock.Now()`. A failed preflight still records `enabled: true, available: false, reason`.
  - `core.module.disable` sets only `enabled = 0` and keeps availability. A host without a row gets `enabled 0, available 0, reason ReasonNotReported, checked_at now`.
  - Module id `core` is refused as `request.invalid_input` at `/moduleId`. A module that is not in `Deps.Modules` and a host that is not in `core_hosts` are `not_found`.
- **Token actions** use `Principal.UserID`; a principal without one gets `auth.forbidden`. `core.token.create` refuses an owner that is missing or not active with `not_found`.
- **Verify.** It decides from the input and the stored state:
  - module actions: the row's `enabled`;
  - `create` and `reset-password`: the user exists;
  - `activate`: the user is active;
  - `reset-totp`: the user is pending;
  - `delete`: the user is gone (`not_found`);
  - token actions: the token by id from the output (`create`) or the input (`revoke`).

  Under recovery the principal is `system:recovery` with no `UserID`, so an interrupted token change verifies as `auth.forbidden`. The interrupted change says so, and the operator checks with `core.token.list`.

**How the next parts use it, and what plan 4 must add**

- P14 (assembly) builds `registry.BuildWith(coreactions.Module(coreactions.Deps{Auth: authService, Audit: auditLog, Modules: modules.All()}))` once and uses that registry for the controller `lifecycle.Engine` and the dispatcher. The rules for the engine and the services:
  - The engine's `Config.Base` sets `DB` to the controller database that `authService` was built on, and `Agents` to the dispatcher's `core.HostDirectory`.
  - `Config.Validator` is `schema.NewValidator()`.
  - `Config.Redact` is `secret.RedactJSON(raw, schema.Output(t))` (register row 50).

  `registry.BuildWith` already exists (`internal/registry/registry.go:22-27`), so the registry package needs no change. The CLI and the schema generator keep calling `registry.Build()`.
- The dispatcher (plan 4) must keep these `core_module_state` semantics:
  - A module is usable on a host only when `enabled = 1` and `available = 1`. Otherwise its actions and resources answer `module.unavailable` with the reason, or with "disabled" when `enabled = 0`.
  - `Hello` writes `available`, `reason` and `checked_at` and inserts missing rows with `enabled` from `DefaultEnabled`. It never changes `enabled` on an existing row; only `core.module.enable` and `core.module.disable` do.
  - Controller-side actions, including every `core.*` action, are never refused as unavailable: the core module has no row.
- P9 (API) and the socket call these actions only through the dispatcher.
  - Break-glass (`core.user.*`) is the socket's alone (spec section 14).
  - The API's scope check gives `core.token.*` and `core.module.enable`/`disable` the `admin` scope, and the read actions the `read` scope (spec section 10).
  - `core.audit.list` returns inputs as recorded, so P11's audit store must store inputs already redacted. The engine's journal does this, and `auth` writes only `{"username": ...}`.
- Plan 4's CLI generator gets a `Summary` for every action and a `jsonschema_description` for every input field; Task 30 enforces both. Secret inputs (`password`, `code`) have no flag and are prompted (spec section 15).

## Assumptions (evidence)

- Each part was drafted by its own drafter on 2026-10-17 and 2026-10-18 in a scratch clone: P5 and P10 on `main` at `2c0c73c`, P9 and P12 on a copy of P5's finished clone. Per the drafters' reports, after each task `go vet ./...`, `go test -count=1 ./...`, `go test -race` on the part's package, `golangci-lint run` (v2.15.0, `0 issues.`) and `go mod tidy -diff` (no output) passed, each task's red step was run before its implementation, and the plan's code blocks were generated from the committed files by script. The parts were not yet merged in one tree when this plan was written; the steps of each task re-run those checks and the integration check proves the merged tree.
- `codex off — quota` (resets 2026-10-22): every task runs on a Claude implementer and every review seat is a Claude judge.
- Plan-time ruling, wave layout: the four parts were drafted as independent chains, so independent tasks of different parts share a wave only when neither is risk 3 (risk-3 tasks run alone). P5's seven tasks are all risk 3, so identity runs first, one task at a time; the listener tasks follow; the API and core-action chains interleave once identity has merged. Tasks 9-11 (`Tailscale addresses`, `Free-bind sockets` and `Problem details, JSON bodies and response headers`), 17-18, 22-23 and 26-27 are the shared waves; a wave the pre-flight seat or the run doubts is run one task after another, which is always correct.
- Plan-time ruling, spec section 9 ("at most 10,000 keys are kept (the oldest dropped first)"): an automated security review of the first draft showed that evicting a key whose lock is still running lets an attacker who rotates sources (an IPv6 /64 gives billions) flood the table and clear a victim's lockout. The limiter therefore drops expired counters first, then the oldest keys that are not locked, and fails closed (refuses unknown keys with `auth.rate_limited`) when every key is locked; IPv6 sources count by /64 and IPv4-mapped IPv6 addresses count as IPv4.
- Plan-time ruling, spec section 14 against ADR 0011 (uid 0 or the service's own uid, against non-zero uid only in `--dev`): the controller socket accepts uid 0 always and the service's own uid only with `--dev`.
- Plan-time ruling, spec section 10 (the `core.user.activate` input lists `code` without a secret mark): the TOTP code is marked `secret:"true"` so audit, journal and plan hashes carry it redacted and the CLI prompts for it.
- Plan-time ruling, authorization of apply, confirm and revert (an automated security review of the first API draft): the gate that decides scope and break-glass reads the action from the URL (apply) or from the change (confirm, revert), and the engine applies what the plan id names. Exploiting the difference needs a plan made by the same principal, so no scope can be gained today, but plan 4's dispatcher must refuse a plan id whose recorded host, action or version differ from the route's (obligation 1 in the API contracts above); until then the API passes the route's resolved reference and the tests assert it.
- Plan-time ruling, handler `Verify` during crash recovery: the engine passes a null output, so the core actions decide from their input and stored state; an interrupted identity or token change is reported as unverified when the recovery principal has no user.
- Defect found in plan 2's merged `internal/core/schema` while drafting P12 and not fixed here: pointer fields reflect as non-nullable `type: string`, so output schemas, snapshots and OpenAPI never state that a field can be null (`lastUsedAt`, `revokedAt`, `revertDeadline`, `error`). It is a register row for plan 4; the wire shapes in this plan already send `null`.
- Register rows 50 to 52 (follow-ups recorded when plan 2 merged) bind this plan: the `Config.Redact` adapter, the server's recovery and purge wiring, the panic policy, the CSRF label, `[]` for empty diff and guards, and the identity handlers' secret rule. Plan 4 carries the wiring rows; this plan's tasks carry the handler and middleware rules.
- Obligations on plan 4 that this plan records and does not meet (each becomes a register row when the plan finishes): the API contracts' numbered list "Obligations on later plans"; the core-action contracts' dispatcher and assembly obligations (a module is usable only when enabled and available; the controller engine's `Base.DB` and `Base.Agents`; `registry.BuildWith(coreactions.New(...))`); the identity service's `Audit.Record` rules (keep a supplied id, accept the actions `core.auth.login` and `core.auth.logout`); and the assembly rules for `listen.Service` (plan 4's server starts it with the API handler) and for the controller socket's runtime directory.
- Every task's code below was run on 2026-10-17 in a scratch clone of `main` at `2c0c73c`. After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`:
  - `go vet ./...`;
  - `go test -count=1 ./...`;
  - `go test -race -count=1 ./internal/controller/auth/` (about 2 s for the whole package);
  - `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`);
  - `go mod tidy -diff` (no output).

  Each RED step was run before its implementation, and its expected text is the real compiler output. Each task's commit was then rebuilt on `2c0c73c` plus only the files of the tasks named in its `**Depends on:**` line, and passed `go vet`, `go test` and golangci-lint there. The plan's code blocks were generated from the verified files by script.
- The five auth codes exist: `errcode.AuthRequired`, `InvalidCredentials`, `RateLimited`, `CSRF` and `Forbidden` (`internal/core/errcode/errcode.go:21-25`). `InvalidInput` and `NotFound` exist as well (`errcode.go:17`, `:26`). This part needs no new code. `Forbidden` and `CSRF` are not returned by this package; P9 uses them.
- `secret.CSRFLabel` is `"darkpanel csrf v1"` (`internal/core/secret/sealer.go:16`). `New` derives the CSRF key with it, which covers register row 52's CSRF item.
- `crypto/rand.Read` never returns an error in Go 1.27, so its result is discarded (`$GOROOT/src/crypto/rand/rand.go:41`). `argon2.IDKey` has the signature this part calls (`golang.org/x/crypto@v0.58.0/argon2/argon2.go:101`).
- `http.Header.Values` returns nil for an absent header (`$GOROOT/src/net/http/header.go:58`), so P9's one-line `Credentials` construction gives absent/present semantics.
- `core.Audit.Record` takes no transaction (`internal/core/contract.go:336`), and `store.DB.Write` is not re-entrant. This package therefore audits after its transaction commits and never inside it.
- `coretest.MemAudit.Record` keeps a supplied id (`internal/core/coretest/audit.go:51`). Unverified for P11's audit store in plan 4: it must store a supplied non-empty `AuditEntry.ID`, which this part always sets to a ULID. The controller session carries this as a plan-time note to P11.
- Unverified for P11 and P13: audit rows of logins use `Action` `core.auth.login` or `core.auth.logout` with `Version` 1, which are not registry actions. P11's table must accept them, and `darkpanel audit list --action` filters on them as on any action. The controller session carries this as a plan-time note.
- Plan-time ruling (controller session, 2026-10-17), spec section 9, "at most 10,000 keys are kept (the oldest dropped first)": this is read as the oldest **unlocked** key dropped first.
  - Taken literally, an attacker rotating addresses could fill the table and evict a victim's running lock.
  - When the table is full the limiter first prunes expired counters, then evicts the least recently failed unlocked key. When every key is locked it evicts nothing and refuses an attempt that would need a new key with `auth.rate_limited` and the shortest remaining lock as `Retry-After`.
  - Source addresses are normalised before use: IPv6 to its `/64`, because one host commonly holds a whole `/64`; IPv4-mapped IPv6 to IPv4; IPv4 whole.
  - Task 3's tests cover a locked username surviving 10,000 other failing usernames, the full-and-all-locked case failing closed, and two addresses of one `/64` sharing a counter.
- Plan-time ruling, spec section 9 ("a blocked attempt ... is not evaluated"; "a username that fails the pattern is refused before anything else"): neither kind of attempt is audited or counted.
  - Both are cheap to send, and auditing them would let any client grow `core_audit`, which v1 never prunes, at network speed.
  - Evaluated failures are audited, and each costs one argon2id hash.
- Plan-time ruling, spec section 9: a successful login does not reset the failure counters. The spec states none, and they age out of the 15-minute window.
- Plan-time ruling, spec section 9: `last_used_at` of a token is written at most once a minute, as `last_seen_at` of a session is. Otherwise every token-authenticated GET would be a database write.
- Plan-time ruling, spec section 9: the idle limit counts from `last_seen_at`, which is written at most once a minute (as the spec requires). A session can therefore end up to one minute before a full hour has passed since its last request.
- Plan-time ruling, spec section 9 ("deleting a user revokes its tokens"): `DeleteUser` deletes the user's token and session rows. The foreign key does not let a token outlive its user, and a deleted row can never authenticate again. Audit rows keep the `token:<id>` actors.
- Plan-time ruling, spec sections 8 and 10: user-operation refusals that have no code of their own use `request.invalid_input` with a field error. These are a taken username, a user that is not pending, a wrong activation code and the last active admin. An unknown user or token is `not_found`, so errcode needs no new code.
- Plan-time ruling: a token name is 1 to 64 characters; the spec sets no limit.
- Plan-time ruling: the user agent is stored truncated to 512 bytes, at a character boundary.
- Plan-time ruling: password length is counted in Unicode code points.
- Plan-time ruling: `ResetTOTP` may leave no active admin. It runs only on the controller host, where `activate` recovers.
- Plan-time ruling, spec section 8 (R19): `core_users.role` has no SQL `CHECK`; the code accepts only `admin`, so a later role needs no table rebuild.
- Plan-time ruling: the session id and token secret are hashed as raw bytes, not as their text forms. A token's secret must be the canonical base32 encoding, because the decoder ignores the last character's four spare bits and one secret would otherwise have 16 accepted spellings; a test covers it.
- Plan-time ruling: `Authorization` present with zero, two or more values, an empty value or a scheme other than Bearer is `auth.required`. This is spec section 9's "only the bearer token is considered".
- Plan-time ruling: when the audit write fails, `Login` and `Logout` return the error (`internal`) rather than succeed unaudited. A session committed before such a failure is unknown to the client and is purged after an hour.
- `DELETE ... RETURNING` (used by `endSession`) works on `modernc.org/sqlite` v1.60.1: Task 5's `TestEndSession` passed on 2026-10-17.
- Task 7's concurrency test was checked against the bug it guards: with the code checked against the step read before the transaction, it failed on 2026-10-17 with `round 1: 2 of two concurrent logins with one code succeeded, want exactly 1`.
- Every task's code below was run on 2026-10-17 in a scratch clone of `main` at `2c0c73c`, in task order, each committed with its planned message: after each task `go vet ./...` (no output), `go test -count=1 ./...` (all `ok`), `go test -race -count=1 ./internal/controller/listen/...` (`ok`), `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`) and `go mod tidy -diff` (no output, exit 0) passed; the code blocks were generated from the committed files by script. Final state: 32 tests pass, none skipped, on kernel `7.0.14-19-pve`. Tasks 9, 10 and 14 were also checked on trees holding only their dependencies (Task 8 + Task 9; Task 8 + Task 10; Task 8 + Task 10 + Task 14): vet, test, lint `0 issues.` and tidy clean.
- Every RED step was run before its implementation; the failure is a build failure naming the first missing identifier (for example `spec_test.go:11:14: undefined: ParseSpecs`, then `FAIL github.com/darkraise/darkpanel/internal/controller/listen [build failed]`).
- `IP_FREEBIND` and `IPV6_FREEBIND` need no privilege: the whole package test binary passed as uid 65534 (`setpriv --reuid=65534 --regid=65534 --clear-groups`, 2026-10-17), including `TestBindSetsFreeBind` for both families. `TestBindSetsFreeBind/ipv6` binds `[::1]:0` (a departure from "port 0 on 127.0.0.1 only", needed to read `IPV6_FREEBIND`) and skips with a reason on a host without IPv6 loopback, on `EPERM` or on `ENOPROTOOPT`.
- `golang.org/x/sys/unix` (`IP_FREEBIND`, `IPV6_FREEBIND`, `SOL_IP`, `SOL_IPV6`) and `github.com/oklog/ulid/v2` are already imported outside `internal/deps` (`internal/platform/files.go`, `internal/core/lifecycle/engine.go`), so no `go.mod` change; `bind.go` is Linux-only without a build tag, as `internal/platform/files.go` (openat2) already is.
- `core_ca` (`internal/core/store/migrations/controller/0001_core.sql`) has no unique index on `purpose`, the file is P0's, and spec section 4 gives P10 no migration. Ruling: one-winner creation comes from the select-then-insert inside the store's immediate write transaction (`internal/core/store/db.go` writer DSN `_txlock=immediate`, `busy_timeout(5000)`), which serialises writers across processes. `TestAuthorityConcurrentStartsAgree` (four `store.DB` handles on one file) fails when the in-transaction check is removed (mutation run 2026-10-17: `start 1 has root 9014…, start 0 has dd82…`).
- Ruling, TLS version: ADR 0006 and spec section 13 set no version (`rg -n -i 'tls ?1\.[23]|MinVersion' docs/` finds TLS 1.3 only for ADR 0003's agent listener), so the web listeners take TLS 1.2 as minimum for browser reach.
- Ruling, spec section 13 ("on a change the listener set is rebound"): an absent `tailscale0` (tailscaled down or not started) keeps the last addresses bound with FREEBIND; a present interface replaces them, even with none, which then saves `[]` and serves loopback only. Otherwise the saved set would be dropped within 10 s of a boot before tailscaled, defeating its purpose.
- Ruling, leaf SANs: the IP SANs are the addresses of the TLS endpoints, that is the Tailscale addresses when the `tailscale` listener is configured plus every explicit HTTPS bind; with `--listen` lacking `tailscale` the interface is never read and no Tailscale IP enters the leaf.
- Ruling, renewal: "renewed after 60 days" is checked on every 10-second check against the clock, not by a 60-day timer, so a suspended host renews on wake; the leaf and root are backdated one hour (`NotBefore`) for clients whose clock runs slightly behind.
- Ruling, `--listen` input: hostnames, zones, port 0 and unspecified or multicast addresses are refused; one address listed as both HTTP and HTTPS is refused; repeated values are merged.
- Ruling, bind failures: at `Start` any failure except the IPv6 loopback skip is fatal (the set is shut down and no check is armed); during a check failures are logged and retried at the next check.
- `coretest.FakeClock.Advance` rescans every expired timer for each due timer, so advancing 60 days through 10-second checks is quadratic (518,400 ticks; the first attempt timed out at 300 s). `TestServiceRenewsTheLeaf` stops the check timer, jumps the clock and calls `check` directly (register row 41 already records FakeClock limits).
- `TestSystemInterfaceAddrs` reads the real `lo` interface (read-only, skipped when absent); no test reads `tailscale0` or depends on Tailscale.
- Known limit, not handled in M1: the root is valid 10 years and nothing re-creates it; a leaf issued in the root's last 90 days would outlive it.
- The `http.Server` error log goes to the service logger at warn level, so a browser that does not yet trust the root produces `TLS handshake error` warnings.
- Every task's code below was run on 2026-10-17 in a copy of P5's verified clone (Task 7 at `6e1b1b1`, on `main` at `2c0c73c`). After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`:
  - `go vet ./...`;
  - `go test -count=1 ./...`;
  - `go test -race -count=1 ./internal/controller/api/` (about 2 s for the whole package, 50 tests at the end);
  - `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`);
  - `go mod tidy -diff` (no output).

  Each RED step was run before its implementation; its expected text is the real output. The plan's code blocks were generated from the verified commits by script.

  Each task was also rebuilt on P5's final commit plus only the tasks it depends on, and passed every check there:
  - Task 16 on Task 11;
  - Task 17 on Task 11;
  - Task 19 on Tasks 11 and 17 (without Task 16);
  - Task 27 on Tasks 11, 16, 17, 19, 20, 21 and 22 (without Task 25).

  Tasks 20, 21, 22 and 25 were checked on the linear history. Tasks 16 or 17 alone on P5 fails only revive's `package-comments`: the package doc comment is Task 11's.
- P5's exported names match its part contracts, confirmed against its code on 2026-10-17. `Authenticate` considers only the bearer token when an `Authorization` header is present (`internal/controller/auth/login.go:175-190`), and principals carry `Source` (`session.go:145`, `token.go:248`). Every `*auth.Service` method `Authenticator` lists exists with that signature: the compile-time assertion in `server.go` builds.
- P10's `listen.WebCA` has exactly `RootPEM() []byte` and `Fingerprint() string`, and `listen.Set` serves one `Handler` for every endpoint with `Request.TLS` set exactly on HTTPS. Read from P10's drafting clone on 2026-10-17: `internal/controller/listen/authority.go:35-40` and `set.go:46-48`. If P10 changes either, P14 adapts the wiring; this part does not import `listen`.
- `errcode` already holds every code and status of spec section 12's table (`internal/core/errcode/errcode.go:16-38`, `:55-83`); this part adds none. The action-level codes map to 500, so a stray one is answered as an internal-style problem with its title only.
- `encoding/json` in Go 1.27.2 reports an unknown field as `json: unknown field "<name>"` (`$GOROOT/src/encoding/json/decode.go:741`); `decodeError` relies on that prefix, and `TestDecodeBody`'s unknown-field row fails if it changes.
- Go 1.27's `ServeMux` (`$GOROOT/src/net/http/server.go:2731-2785`):
  - Its `Handler` returns the pattern `""` for a 404 or 405 and redirects unclean paths before any handler runs. Such a redirect reveals nothing; the redirected request is authenticated as usual.
  - A `GET` pattern also matches `HEAD`, which gives `/healthz` and the UI their `HEAD` answers.
  - The fallback pattern `/` matches every method, so the mux never writes its own plain-text 405; `allowedMethods` probes the other methods instead.
- `schema.OpenAPI` lists one read operation per read action, and one plan and one apply operation per mutating action, under `/api/v1/actions/<name>/v<n>/` or `/api/v1/hosts/{host}/actions/...`; it skips break-glass actions (`internal/core/schema/openapi.go:178-213`). `CheckOpenAPI` mirrors that rule.
- Plan-time ruling, spec sections 12 and 13: `Handler()` takes no `secure` argument. The prompt suggested `Handler(secure bool)`, but P10 serves one handler on every listener, so the cookie name and HSTS follow `Request.TLS`, which P10 documents.
- Plan-time ruling, ADR 0004 and controller ruling of 2026-10-17:
  - Apply passes the route's resolved action and host to `Dispatcher.Apply` (obligation 1).
  - Confirm and revert authorize against the change's own action, read first through `Dispatcher.Change` as the caller (obligation 2).
  - A break-glass change is never confirmed or reverted over a network listener.
  - A controller-side change whose action is no longer registered is refused, because whether it was break-glass cannot be known.
- Plan-time ruling, spec section 10: `RequiredScope` is decided from class and side: mutating agent-side actions need `operate`, mutating controller-side actions `admin`. In M1 every controller-side action is a core action, so this is spec section 10's module/core split. A later controller-side module action would need `admin` until a ruling changes it.
- Plan-time ruling, spec section 14 versus ADR 0011: spec section 14 accepts "uid 0 or the service's own uid", while ADR 0011 says a non-zero uid is accepted "only in --dev mode". This part satisfies both: uid 0 always, and the service's own uid only with `Dev`. A production service running as root has own uid 0 anyway. A production service running as a dedicated user refuses that user on the socket; operators use `sudo darkpanel ...`.
- Plan-time ruling: on the socket, a non-break-glass action is `auth.forbidden` (mirroring the network's answer for break-glass actions), and a host action route does not exist (`not_found`).
- Plan-time ruling: an action route whose action exists but on the other side, or without that operation (`read` of a mutating action, `plan` of a read action), is `action.unknown`. That matches the OpenAPI document, which lists no such operation.
- Plan-time ruling, spec section 12 ("every request with a body must send `Content-Type: application/json`"): the media type is enforced on the routes that read a body: login, plan, apply and read. Logout, confirm and revert read no body and ignore one.
- Plan-time ruling: an unknown field in a request envelope is `request.invalid_json` (400), like any other decoding failure. Unknown fields inside `input` are the validator's `request.invalid_input` (422), because the input schemas set `additionalProperties: false`.
- Plan-time ruling: field errors on query parameters of `GET /api/v1/changes` use the parameter name (`limit`, `state`) as `path`, since they are not JSON pointers into a body.
- Plan-time ruling: `GET /api/v1/auth/session` and the login response share one shape, `{user, csrfToken, expiresAt, token}`, with `null` where a field does not apply. This is a superset of the two shapes in spec section 12. For a token, `user` is the owner and `expiresAt` the token's expiry.
- Plan-time ruling: logout with a token principal answers 204, clears the cookie and ends nothing.
- Plan-time ruling: `GET /healthz` also answers `HEAD`. With credentials whose scope does not allow `read` (none exists in v1) it answers the status alone. When the schema version cannot be read it answers `internal` to an authenticated caller, while the unauthenticated status stays `ok`.
- Plan-time ruling: `/healthz` and `/api/v1/openapi.json` send `Cache-Control: no-store`; static UI files carry no `Cache-Control`.
- Plan-time ruling: the session cookie has no `Expires` or `Max-Age`. The server ends sessions (1 hour idle, 12 hours absolute), and a fixed expiry would end an active session at its first idle deadline.
- Plan-time ruling: the confirm timeout is bounded in the API to 0 (the default) or 10 to 900 seconds before it is converted to a duration, so a huge value cannot overflow. The engine bounds it again.
- Every task's code below was run on 2026-10-17 in a scratch clone of P5's verified clone, which is `main` at `2c0c73c` plus P5's seven commits ending `6e1b1b1`. After each task these passed, all with `GOTOOLCHAIN=local` and a `timeout`:
  - `go vet ./...`;
  - `go test -count=1 ./...`;
  - `go test -race -count=1 ./internal/controller/coreactions/` (about 2 s);
  - `golangci-lint run --allow-serial-runners ./...` (v2.15.0, `0 issues.`);
  - `go mod tidy -diff` (no output).

  Each RED step was run on the task's parent commit with only that task's test file added, and its expected text is the real compiler output. The tasks form a chain, so each commit was verified on exactly its dependencies. The plan's code blocks were generated from the committed files by script.
- `coreactions.Module(Deps)` and `registry.BuildWith(core.Module)` already exist in the foundation (`internal/controller/coreactions/module.go` stub, `internal/registry/registry.go:16-27`). The dependency mechanism therefore needs no registry change: Task 18 fills `Deps`.
- The engine calls handlers with `Config.Base` plus the caller's principal (`internal/core/lifecycle/engine.go:115-119`). It re-runs `Plan` under its apply mutex and compares `StateHash` and guards (`apply.go:196-219`). Recovery calls `Verify` with principal `system:recovery`, no `UserID` and output `null` (`recover.go:17`, `recover.go:100`). `Apply` runs outside any `store.DB.Write`, so handlers may call `Env.DB.Write` and the `auth.Service` writers (`apply.go:257-263`, after `begin`'s write commits).
- `schema.NewValidator` reports field errors without quoting values (`internal/core/schema/validate.go:107-121`), so a short password or a malformed code is refused at `/password` or `/code` without its value. Tasks 28 and 29 test this.
- invopop v0.14.0 splits `jsonschema` tags on unescaped commas (`reflect.go:1153`), so the username pattern is written `{0\\,63}` in the tag. Field descriptions use the `jsonschema_description` tag (`reflect.go:613`). A type with a `JSONSchema()` method is put in `$defs` under its type name (`reflect.go:373-381`). Embedded structs are inlined, and `schema.Output` marks a flattened secret field (`internal/core/schema/reflect.go:97`). `TestTokenCreate` shows `token` stored as `"[redacted]"`.
- `secret.RedactJSON` redacts any object property that no schema describes. This was confirmed on 2026-10-17 by renaming `AuditInput.JSONSchema`: every value of every audit input came back `"[redacted]"`. Hence `AuditInput`.
- Plan-time ruling, spec section 10: `core.user.activate`'s `code` is marked `secret:"true"`, although the spec's table marks only passwords and TOTP secrets.
  - A TOTP code is a credential, and spec section 11 keeps codes out of login audit rows.
  - With the mark, the journal and audit store `"code":"[redacted]"`, and the CLI prompts for it, as spec section 15's enrollment flow already describes.
  - `TestUserCreateAndActivate` fails without the mark.
- Plan-time ruling, spec sections 2 and 10: `core.module.enable` with a failing preflight completes and records `enabled: true, available: false` with the preflight's reason.
  - Enabling records the operator's intent. Availability is what the host can do, and a later `Hello` refreshes it.
  - `core.module.disable` keeps availability for the same reason.
  - The dispatcher derives `module.unavailable` from both columns (contracts above).
- Plan-time ruling: the core module cannot be enabled or disabled (`request.invalid_input` at `/moduleId`) and has no `core_module_state` row. Disabling it would cut off the actions that re-enable it.
- Plan-time ruling, spec section 12 ("`null` where a field does not apply"): `ModuleState.reason` and `checkedAt`, `AuditEntry.hostId`, `changeId`, `errorCode` and `source`, and `AuditList.nextBefore` are nullable pointers. `source` is null for controller-side rows because `core.JournalEntry` has no `Source` (register row 48 stays open for P11).
- Plan-time ruling: a non-local host is `online: false` in M1, which has no remote transport.
- Plan-time ruling: refusals that depend on state are guards (`username-free`, `user-pending`, `not-last-admin`), so the CLI shows them before apply (exit 3). An unknown user, token, host or module is a `not_found` error from `Plan`. A wrong activation code can be checked only by spending it, so it fails the apply: the change is `failed` with `request.invalid_input`, and the code appears nowhere.
- Plan-time ruling: `core.user.reset-totp`'s diff warns when the user is the last active admin, but does not block it. P5's ruling lets `reset-totp` leave no active admin, because only the controller host runs it and `activate` recovers.
- Plan-time ruling: `core.user.reset-password`'s `Verify` checks only that the user exists, because `auth.Service` exposes no password check outside `Login`.
- Plan-time ruling: `core.audit.list` clamps `limit` to 500 even though the schema already refuses more. P11's `List` contract does not promise a clamp.
- Core-contract gap, reported and not fixed here: the schema reflector maps a pointer field such as `*string` to `{"type":"string"}`, without `null`. Output schemas and the OpenAPI document therefore do not say that `TokenInfo.lastUsedAt`, `ModuleState.reason` or `AuditList.nextBefore` can be `null`. Nothing validates outputs, so no request fails, but generated clients may mistype these fields. The schema package (plan 2) or plan 4 must decide.
- Core-contract gap: recovery's principal has no `UserID`, so a user-scoped action (`core.token.*`) cannot verify an interrupted change. Task 24 reports it as unverified (`auth.forbidden`) rather than guessing.
- Unverified for plan 4: the spec section 15 test that every mutating action has a generated CLI command is plan 4's. Task 30 checks only that every `Summary` and input description exists.

## Task index

1. Password hashing
2. TOTP
3. Login rate limiter
4. Identity tables and user operations
5. Sessions and CSRF
6. API tokens and purge
7. Login, logout and authentication
8. Listen values and endpoints
9. Tailscale addresses
10. Free-bind sockets
11. Problem details, JSON bodies and response headers
12. Web authority root
13. Web leaf and TLS configuration
14. Listener set
15. Listener service
16. Scope model
17. Static web UI
18. Core module dependencies, host and module lists
19. Server, authentication middleware and CSRF
20. Login, logout and session routes
21. Action, resource and change routes
22. Health and OpenAPI routes
23. Module enable and disable
24. Token actions
25. Controller socket and break-glass routes
26. Audit list
27. Route coverage and the deny-by-default matrix
28. User list, TOTP reset, password reset and delete
29. User create and activate
30. Core action registry test

---

### Task 1: Password hashing

**Files:**
- Create: `internal/controller/auth/password.go`
- Test: `internal/controller/auth/password_test.go`

**Interfaces:**
- Consumes: `errcode.Error`, `errcode.FieldError`, `errcode.InvalidInput` (foundation); `golang.org/x/crypto/argon2` v0.58.0 (pinned).
- Produces: Contracts › Task 1 (`Params`, `DefaultParams`, `MaxConcurrentHashes`, the password length bounds, `newHasher` and its methods, `checkUsername`, `checkPassword`, the test helpers `testParams` and `wantField`).

**Items:** 11

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The hasher bounds concurrent argon2id runs with a two-slot semaphore, because each run holds 64 MiB; a third caller waits on its context. The dummy hash is made once at construction from a random password, so verifying an unknown user's password costs one real hash and matches nothing. The `compute` field exists so tests can count and block hashes.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/password_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// testParams keep argon2id cheap in tests; production uses DefaultParams.
var testParams = Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

func TestHashAndVerify(t *testing.T) {
	h := newHasher(testParams)
	ctx := context.Background()
	phc, err := h.hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(phc, "$argon2id$v=19$m=8,t=1,p=1$") {
		t.Fatalf("hash = %q, want a PHC argon2id string with the parameters", phc)
	}
	again, err := h.hash(ctx, "correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if again == phc {
		t.Error("two hashes of one password are equal; the salt is not random")
	}
	for _, tt := range []struct {
		password string
		want     bool
	}{
		{"correct horse battery", true},
		{"correct horse batterz", false},
		{"", false},
	} {
		got, err := h.verify(ctx, phc, tt.password)
		if err != nil {
			t.Fatalf("verify(%q): %v", tt.password, err)
		}
		if got != tt.want {
			t.Errorf("verify(%q) = %v, want %v", tt.password, got, tt.want)
		}
	}
}

func TestVerifyReadsParametersFromTheHash(t *testing.T) {
	old := newHasher(Params{Memory: 16, Iterations: 2, Parallelism: 1, SaltLen: 16, KeyLen: 32})
	ctx := context.Background()
	phc, err := old.hash(ctx, "a long password")
	if err != nil {
		t.Fatal(err)
	}
	ok, err := newHasher(testParams).verify(ctx, phc, "a long password")
	if err != nil || !ok {
		t.Fatalf("verify with other default parameters = %v, %v; want true", ok, err)
	}
}

func TestVerifyRefusesMalformedHashes(t *testing.T) {
	h := newHasher(testParams)
	for _, phc := range []string{
		"",
		"$argon2i$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=18$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=0,p=1$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1x$c2FsdHNhbHRzYWx0c2FsdA$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$!!$a2V5",
		"$argon2id$v=19$m=8,t=1,p=1$c2FsdHNhbHRzYWx0c2FsdA$",
	} {
		if ok, err := h.verify(context.Background(), phc, "x"); err == nil || ok {
			t.Errorf("verify(%q) = %v, %v; want false and an error", phc, ok, err)
		}
	}
}

func TestDummyHashRuns(t *testing.T) {
	h := newHasher(testParams)
	var calls atomic.Int32
	inner := h.compute
	h.compute = func(password, salt []byte, p Params) []byte {
		calls.Add(1)
		return inner(password, salt, p)
	}
	if err := h.verifyDummy(context.Background(), "anything"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Errorf("verifyDummy ran %d hashes, want 1", calls.Load())
	}
}

func TestAtMostTwoHashesAtOnce(t *testing.T) {
	h := newHasher(testParams)
	release := make(chan struct{})
	var running, peak atomic.Int32
	h.compute = func([]byte, []byte, Params) []byte {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		<-release
		running.Add(-1)
		return make([]byte, 32)
	}
	var wg sync.WaitGroup
	for range MaxConcurrentHashes {
		wg.Go(func() { _, _ = h.hash(context.Background(), "a long password") })
	}
	deadline := time.Now().Add(5 * time.Second)
	for running.Load() < MaxConcurrentHashes {
		if time.Now().After(deadline) {
			t.Fatal("the first two hashes did not start")
		}
		time.Sleep(time.Millisecond)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := h.hash(ctx, "a third password")
		done <- err
	}()
	select {
	case err := <-done:
		t.Fatalf("a third hash returned %v while two were running; want it to wait", err)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("third hash after cancel = %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the waiting hash ignored its context")
	}
	close(release)
	wg.Wait()
	if peak.Load() != MaxConcurrentHashes {
		t.Errorf("peak concurrent hashes = %d, want %d", peak.Load(), MaxConcurrentHashes)
	}
}

func TestCheckUsername(t *testing.T) {
	for _, tt := range []struct {
		name string
		ok   bool
	}{
		{"alice", true},
		{"0ps.team_a-1", true},
		{strings.Repeat("a", 64), true},
		{strings.Repeat("a", 65), false},
		{"", false},
		{"Alice", false},
		{".alice", false},
		{"-alice", false},
		{"al ice", false},
		{"alice\n", false},
	} {
		err := checkUsername(tt.name)
		if (err == nil) != tt.ok {
			t.Errorf("checkUsername(%q) = %v, want ok=%v", tt.name, err, tt.ok)
		}
		if err != nil {
			wantField(t, err, "/username")
		}
	}
}

func TestCheckPassword(t *testing.T) {
	for _, tt := range []struct {
		password string
		ok       bool
	}{
		{strings.Repeat("a", 11), false},
		{strings.Repeat("a", 12), true},
		{strings.Repeat("é", 12), true},
		{strings.Repeat("a", 128), true},
		{strings.Repeat("a", 129), false},
		{"", false},
	} {
		err := checkPassword(tt.password)
		if (err == nil) != tt.ok {
			t.Errorf("checkPassword(len %d) = %v, want ok=%v", len(tt.password), err, tt.ok)
		}
		if err != nil {
			wantField(t, err, "/password")
			if strings.Contains(err.Error(), tt.password) && tt.password != "" {
				t.Errorf("error %q quotes the password", err)
			}
		}
	}
}

// wantField fails unless err is request.invalid_input with one field error
// at path.
func wantField(t *testing.T, err error, path string) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Fatalf("error = %v, want %s", err, errcode.InvalidInput)
	}
	if len(e.Fields) != 1 || e.Fields[0].Path != path {
		t.Fatalf("fields = %+v, want one at %s", e.Fields, path)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `undefined: Params` and `undefined: newHasher`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/password.go`:

```go
// Package auth is the controller's identity: users with argon2id passwords
// and TOTP, sessions with CSRF tokens, login rate limits and scoped API tokens
// (spec sections 8 and 9).
package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"

	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Params are argon2id parameters. Memory is in KiB.
type Params struct {
	Memory      uint32
	Iterations  uint32
	Parallelism uint8
	SaltLen     int
	KeyLen      uint32
}

// DefaultParams are the production parameters (spec section 8). Stored hashes
// carry their own parameters, so changing these affects only new hashes.
var DefaultParams = Params{Memory: 64 * 1024, Iterations: 3, Parallelism: 1, SaltLen: 16, KeyLen: 32}

// MaxConcurrentHashes bounds the argon2id hashes running at once; each holds
// Params.Memory, so the bound is what keeps a login flood from exhausting
// memory.
const MaxConcurrentHashes = 2

// Password length bounds, in characters.
const (
	MinPasswordLength = 12
	MaxPasswordLength = 128
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

var b64 = base64.RawStdEncoding

type hasher struct {
	params  Params
	sem     chan struct{}
	dummy   string
	compute func(password, salt []byte, p Params) []byte
}

func argon2id(password, salt []byte, p Params) []byte {
	return argon2.IDKey(password, salt, p.Iterations, p.Memory, p.Parallelism, p.KeyLen)
}

// newHasher returns a hasher for p. It hashes a random password once, without
// waiting for a slot, to make the dummy hash that verifyDummy checks against.
func newHasher(p Params) *hasher {
	h := &hasher{params: p, sem: make(chan struct{}, MaxConcurrentHashes), compute: argon2id}
	pw := make([]byte, 32)
	_, _ = rand.Read(pw)
	h.dummy = h.encode(pw)
	return h
}

func (h *hasher) encode(password []byte) string {
	salt := make([]byte, h.params.SaltLen)
	_, _ = rand.Read(salt)
	key := h.compute(password, salt, h.params)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		h.params.Memory, h.params.Iterations, h.params.Parallelism, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// acquire waits for a hashing slot or for ctx to end.
func (h *hasher) acquire(ctx context.Context) error {
	select {
	case h.sem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (h *hasher) release() { <-h.sem }

// hash returns password's PHC string:
// $argon2id$v=19$m=<KiB>,t=<iterations>,p=<parallelism>$<salt>$<key>, salt and
// key in unpadded standard base64.
func (h *hasher) hash(ctx context.Context, password string) (string, error) {
	if err := h.acquire(ctx); err != nil {
		return "", err
	}
	defer h.release()
	return h.encode([]byte(password)), nil
}

// verify reports whether password matches phc, using the parameters stored in
// phc. A malformed phc is an error.
func (h *hasher) verify(ctx context.Context, phc, password string) (bool, error) {
	p, salt, want, err := parsePHC(phc)
	if err != nil {
		return false, err
	}
	if err := h.acquire(ctx); err != nil {
		return false, err
	}
	defer h.release()
	got := h.compute([]byte(password), salt, p)
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// verifyDummy runs one verification against a hash no password matches, so a
// login for an unknown username costs what a real one costs.
func (h *hasher) verifyDummy(ctx context.Context, password string) error {
	_, err := h.verify(ctx, h.dummy, password)
	return err
}

func parsePHC(phc string) (Params, []byte, []byte, error) {
	bad := errors.New("auth: malformed password hash")
	parts := strings.Split(phc, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" || parts[2] != fmt.Sprintf("v=%d", argon2.Version) {
		return Params{}, nil, nil, bad
	}
	var p Params
	_, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.Memory, &p.Iterations, &p.Parallelism)
	if err != nil || p.Memory == 0 || p.Iterations == 0 || p.Parallelism == 0 ||
		fmt.Sprintf("m=%d,t=%d,p=%d", p.Memory, p.Iterations, p.Parallelism) != parts[3] {
		return Params{}, nil, nil, bad
	}
	salt, err := b64.DecodeString(parts[4])
	if err != nil || len(salt) == 0 {
		return Params{}, nil, nil, bad
	}
	key, err := b64.DecodeString(parts[5])
	if err != nil || len(key) == 0 {
		return Params{}, nil, nil, bad
	}
	p.SaltLen = len(salt)
	p.KeyLen = uint32(len(key)) //nolint:gosec // G115: a decoded hash field is far below 4 GiB
	return p, salt, key, nil
}

// checkUsername refuses a username that does not match spec section 8's
// pattern, with a field error at /username.
func checkUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid username",
			Fields: []errcode.FieldError{{Path: "/username", Message: "must match ^[a-z0-9][a-z0-9._-]{0,63}$"}}}
	}
	return nil
}

// checkPassword refuses a password outside the length bounds, with a field
// error at /password that never quotes the value.
func checkPassword(password string) error {
	if n := utf8.RuneCountInString(password); n < MinPasswordLength || n > MaxPasswordLength {
		return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid password",
			Fields: []errcode.FieldError{{Path: "/password", Message: fmt.Sprintf("must be %d to %d characters", MinPasswordLength, MaxPasswordLength)}}}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/password.go internal/controller/auth/password_test.go
git commit -m "feat(auth): hash passwords with argon2id"
```


### Task 2: TOTP

**Files:**
- Create: `internal/controller/auth/totp.go`
- Test: `internal/controller/auth/totp_test.go`

**Interfaces:**
- Consumes: package `auth` and its doc comment from Task 1.
- Produces: Contracts › Task 2 (`TOTPSecretSize`, `TOTPPeriod`, `TOTPCode`, `b32`, `newTOTPSecret`, `encodeTOTPSecret`, `totpStep`, `totpCode`, `matchTOTP`).

**Items:** 11

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

RFC 6238 with SHA-1, six digits and a 30-second step; `matchTOTP` accepts one step either side and never a step at or before the last accepted one, which is how replay is refused. The test pins the code to the RFC's appendix B vectors (their eight-digit values reduced to the last six digits, which is the same truncation modulo 10^6).

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/totp_test.go`:

```go
package auth

import (
	"testing"
	"time"
)

// rfcKey is the SHA-1 seed of RFC 6238 appendix B.
var rfcKey = []byte("12345678901234567890")

func TestTOTPCodeMatchesRFC6238(t *testing.T) {
	// The RFC's eight-digit values, truncated to their last six digits: the
	// code is the same dynamic binary code modulo 10^6.
	for _, tt := range []struct {
		unix int64
		want string
	}{
		{59, "287082"},
		{1111111109, "081804"},
		{1111111111, "050471"},
		{1234567890, "005924"},
		{2000000000, "279037"},
		{20000000000, "353130"},
	} {
		if got := totpCode(rfcKey, totpStep(time.Unix(tt.unix, 0))); got != tt.want {
			t.Errorf("code at %d = %s, want %s", tt.unix, got, tt.want)
		}
	}
}

func TestTOTPCodeFromEncodedSecret(t *testing.T) {
	secret := encodeTOTPSecret(rfcKey)
	if secret != "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" { //nolint:gosec // G101: the RFC 6238 test seed, not a credential
		t.Fatalf("encoded secret = %s, want unpadded base32", secret)
	}
	got, err := TOTPCode(secret, time.Unix(59, 0))
	if err != nil || got != "287082" {
		t.Fatalf("TOTPCode = %q, %v; want 287082", got, err)
	}
	if _, err := TOTPCode("not base32!", time.Unix(59, 0)); err == nil {
		t.Error("TOTPCode accepted an invalid secret")
	}
}

func TestNewTOTPSecret(t *testing.T) {
	a, b := newTOTPSecret(), newTOTPSecret()
	if len(a) != TOTPSecretSize || len(b) != TOTPSecretSize {
		t.Fatalf("secret sizes %d and %d, want %d", len(a), len(b), TOTPSecretSize)
	}
	if string(a) == string(b) {
		t.Error("two secrets are equal")
	}
}

func TestMatchTOTP(t *testing.T) {
	now := time.Unix(1111111111, 0)
	step := totpStep(now)
	code := func(s int64) string { return totpCode(rfcKey, s) }
	for _, tt := range []struct {
		name     string
		code     string
		lastStep int64
		want     int64
		ok       bool
	}{
		{"current step", code(step), 0, step, true},
		{"previous step", code(step - 1), 0, step - 1, true},
		{"next step", code(step + 1), 0, step + 1, true},
		{"two steps old", code(step - 2), 0, 0, false},
		{"two steps ahead", code(step + 2), 0, 0, false},
		{"replay of the last accepted step", code(step), step, 0, false},
		{"step before the last accepted", code(step - 1), step, 0, false},
		{"step after the last accepted", code(step + 1), step, step + 1, true},
		{"five digits", code(step)[1:], 0, 0, false},
		{"seven digits", code(step) + "0", 0, 0, false},
		{"not digits", "12345a", 0, 0, false},
		{"empty", "", 0, 0, false},
	} {
		got, ok := matchTOTP(rfcKey, tt.code, now, tt.lastStep)
		if ok != tt.ok || got != tt.want {
			t.Errorf("%s: matchTOTP = %d, %v; want %d, %v", tt.name, got, ok, tt.want, tt.ok)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `undefined: totpCode`, `undefined: totpStep` and `undefined: encodeTOTPSecret`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/totp.go`:

```go
package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1" //nolint:gosec // G505: RFC 6238 TOTP with SHA-1 is what authenticator apps implement (spec section 8)
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"time"
)

// TOTP parameters (spec section 8, RFC 6238).
const (
	TOTPSecretSize = 20
	TOTPPeriod     = 30 * time.Second
	totpDigits     = 6
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func newTOTPSecret() []byte {
	s := make([]byte, TOTPSecretSize)
	_, _ = rand.Read(s)
	return s
}

// encodeTOTPSecret renders key as unpadded base32, the form enrollment URIs
// and authenticator apps take.
func encodeTOTPSecret(key []byte) string { return b32.EncodeToString(key) }

func totpStep(t time.Time) int64 { return t.Unix() / int64(TOTPPeriod/time.Second) }

func totpCode(key []byte, step int64) string {
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step)) //nolint:gosec // G115: steps of real times are positive
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%0*d", totpDigits, bin%1000000)
}

// TOTPCode returns the code for the unpadded base32 secret at t, as an
// authenticator app shows it.
func TOTPCode(secret string, t time.Time) (string, error) {
	key, err := b32.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("auth: TOTP secret: %w", err)
	}
	return totpCode(key, totpStep(t)), nil
}

// matchTOTP checks code against the steps around now, one either side, and
// returns the matching step. Steps at or before lastStep are never accepted,
// so a code cannot be replayed.
func matchTOTP(key []byte, code string, now time.Time, lastStep int64) (int64, bool) {
	if len(code) != totpDigits {
		return 0, false
	}
	for _, c := range code {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	cur := totpStep(now)
	for s := cur - 1; s <= cur+1; s++ {
		if s <= lastStep {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(totpCode(key, s)), []byte(code)) == 1 {
			return s, true
		}
	}
	return 0, false
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/totp.go internal/controller/auth/totp_test.go
git commit -m "feat(auth): check RFC 6238 TOTP codes"
```


### Task 3: Login rate limiter

**Files:**
- Create: `internal/controller/auth/ratelimit.go`
- Test: `internal/controller/auth/ratelimit_test.go`

**Interfaces:**
- Consumes: `core.Clock`, `coretest.NewFakeClock` (foundation); package `auth` from Task 1.
- Produces: Contracts › Task 3 (the limit constants, `RetryAfterError`, `newLimiter`, `blocked`, `fail`, `sourceKey`, the test var `t0`).

**Items:** 12

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

In-memory counters per username and per normalised source address, under the controller session's ruling on the key cap (Assumptions): eviction never removes a running lock, and a table full of locks fails closed. Counters sit in a list ordered by their last failure; because a lock lasts exactly the failure window, every counter in front of the first one inside the window is stale and unlocked, so pruning and eviction walk only from the front and a 10,000-key flood stays linear.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/ratelimit_test.go`:

```go
package auth

import (
	"fmt"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

var t0 = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

func TestUsernameLock(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range UsernameFailureLimit - 1 {
		l.fail("alice", "10.0.0."+string(rune('1'+i)))
	}
	if d := l.blocked("alice", "10.0.0.9"); d != 0 {
		t.Fatalf("blocked after %d failures for %s, want allowed", UsernameFailureLimit-1, d)
	}
	clock.Advance(time.Minute)
	l.fail("alice", "10.0.0.9")
	if d := l.blocked("alice", "10.0.0.200"); d != LockDuration {
		t.Fatalf("after %d failures blocked for %s, want %s from any source", UsernameFailureLimit, d, LockDuration)
	}
	if d := l.blocked("bob", "10.0.0.9"); d != 0 {
		t.Errorf("another username is blocked for %s", d)
	}
	clock.Advance(LockDuration - time.Second)
	if d := l.blocked("alice", "10.0.0.9"); d != time.Second {
		t.Errorf("one second before the lock ends: blocked for %s, want 1s", d)
	}
	clock.Advance(time.Second)
	if d := l.blocked("alice", "10.0.0.9"); d != 0 {
		t.Errorf("lock outlived %s: blocked for %s", LockDuration, d)
	}
}

func TestFailuresOutsideTheWindowDoNotCount(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit - 1 {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(FailureWindow)
	l.fail("alice", "10.0.0.1")
	if d := l.blocked("alice", "10.0.0.1"); d != 0 {
		t.Errorf("failures older than %s counted: blocked for %s", FailureWindow, d)
	}
}

func TestSourceBlock(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail("user"+string(rune('a'+i)), "10.0.0.1")
	}
	if d := l.blocked("fresh", "10.0.0.1"); d != LockDuration {
		t.Errorf("source after %d failures blocked for %s, want %s", SourceFailureLimit, d, LockDuration)
	}
	if d := l.blocked("fresh", "10.0.0.2"); d != 0 {
		t.Errorf("another source is blocked for %s", d)
	}
}

func TestBlockedReturnsTheLongerWait(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail("user"+string(rune('a'+i)), "10.0.0.1")
	}
	clock.Advance(5 * time.Minute)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.2")
	}
	if d := l.blocked("alice", "10.0.0.1"); d != LockDuration {
		t.Errorf("blocked for %s, want the username's %s", d, LockDuration)
	}
	if d := l.blocked("bob", "10.0.0.1"); d != LockDuration-5*time.Minute {
		t.Errorf("blocked for %s, want the source's remaining %s", d, LockDuration-5*time.Minute)
	}
}

func TestStaleKeysArePruned(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("locked", "10.0.0.1")
	}
	l.fail("stale", "10.0.0.2")
	clock.Advance(FailureWindow - time.Minute)
	l.fail("other", "10.0.0.3")
	clock.Advance(time.Minute)
	l.fail("new", "10.0.0.4")
	for _, k := range []string{"u:stale", "s:10.0.0.2"} {
		if _, ok := l.keys[k]; ok {
			t.Errorf("key %s survived its window", k)
		}
	}
	for _, k := range []string{"u:other", "s:10.0.0.3", "u:new", "s:10.0.0.4"} {
		if _, ok := l.keys[k]; !ok {
			t.Errorf("key %s was pruned inside its window", k)
		}
	}
	if _, ok := l.keys["u:locked"]; ok {
		t.Error("a key whose lock ended was kept")
	}
}

func TestLockedKeyIsKeptAfterItsFailuresAge(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(LockDuration - time.Minute)
	l.fail("bob", "10.0.0.2")
	if d := l.blocked("alice", "10.0.0.3"); d != time.Minute {
		t.Errorf("lock dropped by pruning: blocked for %s, want 1m", d)
	}
}

func TestKeyCapDropsTheOldest(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	l.max = 3
	l.fail("a", "x") // u:a, s:x at t0
	clock.Advance(time.Second)
	l.fail("a", "y") // u:a and s:y at t0+1s
	clock.Advance(time.Second)
	l.fail("b", "y") // u:b is new: s:x, the oldest, is dropped
	if len(l.keys) != 3 {
		t.Fatalf("%d keys kept, want the cap of 3", len(l.keys))
	}
	if _, ok := l.keys["s:x"]; ok {
		t.Error("the oldest key was kept")
	}
	for _, k := range []string{"u:a", "s:y", "u:b"} {
		if _, ok := l.keys[k]; !ok {
			t.Errorf("key %s was dropped", k)
		}
	}
	if MaxLimiterKeys != 10000 || newLimiter(clock).max != MaxLimiterKeys {
		t.Errorf("default cap is not %d", 10000)
	}
}

func TestLockSurvivesAFloodOfNewKeys(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1")
	}
	clock.Advance(time.Minute)
	for i := range MaxLimiterKeys {
		l.fail(fmt.Sprintf("user%d", i), fmt.Sprintf("10.%d.%d.%d", 1+i>>16, i>>8&0xff, i&0xff))
	}
	if len(l.keys) > MaxLimiterKeys {
		t.Fatalf("%d keys kept, above the cap of %d", len(l.keys), MaxLimiterKeys)
	}
	if d := l.blocked("alice", "192.0.2.1"); d != LockDuration-time.Minute {
		t.Errorf("after %d other failing usernames alice is blocked for %s, want %s", MaxLimiterKeys, d, LockDuration-time.Minute)
	}
}

func TestFullOfLockedKeysFailsClosed(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for range UsernameFailureLimit {
		l.fail("alice", "10.0.0.1") // u:alice locked until t0+15m
	}
	clock.Advance(5 * time.Minute)
	for range SourceFailureLimit - UsernameFailureLimit {
		l.fail("bob", "10.0.0.1") // u:bob and s:10.0.0.1 locked until t0+20m
	}
	l.max = 3
	if len(l.keys) != 3 {
		t.Fatalf("%d keys, want 3 locked keys", len(l.keys))
	}
	if d := l.blocked("carol", "10.0.0.9"); d != 10*time.Minute {
		t.Errorf("new keys while every key is locked: blocked for %s, want the shortest lock, 10m", d)
	}
	if d := l.blocked("bob", "10.0.0.9"); d != LockDuration {
		t.Errorf("known locked username with a new source: blocked for %s, want %s", d, LockDuration)
	}
	l.fail("carol", "10.0.0.9")
	if len(l.keys) != 3 || l.keys["u:carol"] != nil {
		t.Errorf("a failure displaced a locked key: %d keys, carol recorded %v", len(l.keys), l.keys["u:carol"] != nil)
	}
	clock.Advance(10 * time.Minute)
	if d := l.blocked("carol", "10.0.0.9"); d != 0 {
		t.Errorf("after the first lock ended: blocked for %s, want allowed", d)
	}
}

func TestSourceKey(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"10.0.0.1", "s:10.0.0.1"},
		{"10.0.0.1:52000", "s:10.0.0.1"},
		{"::ffff:10.0.0.1", "s:10.0.0.1"},
		{"2001:db8:1:2::1", "s:2001:db8:1:2::/64"},
		{"[2001:db8:1:2:ffff::9]:443", "s:2001:db8:1:2::/64"},
		{"fe80::1%eth0", "s:fe80::/64"},
		{"socket", "s:socket"},
	} {
		if got := sourceKey(tt.in); got != tt.want {
			t.Errorf("sourceKey(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestOneIPv6PrefixSharesACounter(t *testing.T) {
	clock := coretest.NewFakeClock(t0)
	l := newLimiter(clock)
	for i := range SourceFailureLimit {
		l.fail(fmt.Sprintf("user%d", i), fmt.Sprintf("2001:db8:1:2::%x", i+1))
	}
	if d := l.blocked("fresh", "2001:db8:1:2:ffff::9"); d != LockDuration {
		t.Errorf("another address of the /64 is blocked for %s, want %s", d, LockDuration)
	}
	if d := l.blocked("fresh", "2001:db8:1:3::1"); d != 0 {
		t.Errorf("an address of another /64 is blocked for %s", d)
	}
}

func TestRetryAfterSeconds(t *testing.T) {
	for _, tt := range []struct {
		d    time.Duration
		want int
	}{
		{15 * time.Minute, 900},
		{1500 * time.Millisecond, 2},
		{time.Millisecond, 1},
		{0, 1},
	} {
		if got := (&RetryAfterError{After: tt.d}).Seconds(); got != tt.want {
			t.Errorf("Seconds(%s) = %d, want %d", tt.d, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `undefined: newLimiter` and `undefined: UsernameFailureLimit`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/ratelimit.go`:

```go
package auth

import (
	"container/list"
	"fmt"
	"net/netip"
	"sync"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Login rate limits (spec section 9).
const (
	UsernameFailureLimit = 5
	SourceFailureLimit   = 20
	FailureWindow        = 15 * time.Minute
	LockDuration         = 15 * time.Minute
	MaxLimiterKeys       = 10000
)

// RetryAfterError is the cause inside an auth.rate_limited error: how long
// the client must wait before its next login attempt is evaluated.
type RetryAfterError struct {
	After time.Duration
}

func (e *RetryAfterError) Error() string { return fmt.Sprintf("retry after %s", e.After) }

// Seconds is the Retry-After header value: whole seconds rounded up, at least
// 1.
func (e *RetryAfterError) Seconds() int {
	s := int((e.After + time.Second - 1) / time.Second)
	return max(s, 1)
}

type counter struct {
	key         string
	failures    []time.Time
	lockedUntil time.Time
	last        time.Time
	elem        *list.Element
}

func (c *counter) locked(now time.Time) bool { return now.Before(c.lockedUntil) }

// limiter counts failed logins per username ("u:" keys) and per source
// address ("s:" keys) in memory. At the key cap it evicts only unlocked keys,
// so flooding it with fresh keys cannot clear a running lock; when every key
// is locked, attempts that would need a new key are refused instead.
//
// order holds the counters least recently failed first. A lock starts at a
// failure and lasts as long as the window, so a locked counter's last failure
// is always inside the window: walking from the front, every counter before
// the first one inside the window is stale and unlocked.
type limiter struct {
	mu    sync.Mutex
	clock core.Clock
	keys  map[string]*counter
	order *list.List
	max   int
}

func newLimiter(clock core.Clock) *limiter {
	return &limiter{clock: clock, keys: map[string]*counter{}, order: list.New(), max: MaxLimiterKeys}
}

// sourceKey normalises a client address: an IPv4-mapped IPv6 address becomes
// its IPv4 form and any other IPv6 address its /64 prefix, because one host
// commonly holds a whole /64; IPv4 stays whole. A port is dropped, and a
// value that is not an address is used as it is.
func sourceKey(source string) string {
	addr, err := netip.ParseAddr(source)
	if err != nil {
		ap, perr := netip.ParseAddrPort(source)
		if perr != nil {
			return "s:" + source
		}
		addr = ap.Addr()
	}
	addr = addr.WithZone("").Unmap()
	if addr.Is6() {
		p, _ := addr.Prefix(64)
		return "s:" + p.String()
	}
	return "s:" + addr.String()
}

func usernameKey(username string) string { return "u:" + username }

// blocked returns how long username or source stays locked, the longer of
// the two, or 0 when the attempt may be evaluated. When the table is full of
// locked keys and either key is new, the attempt could not be counted, so it
// is refused until the first lock ends.
func (l *limiter) blocked(username, source string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	if len(l.keys) >= l.max {
		l.prune(now)
	}
	var wait time.Duration
	unknown := false
	for _, k := range []string{usernameKey(username), sourceKey(source)} {
		c, ok := l.keys[k]
		switch {
		case !ok:
			unknown = true
		case c.locked(now):
			wait = max(wait, c.lockedUntil.Sub(now))
		}
	}
	if unknown && len(l.keys) >= l.max {
		if first, full := l.allLocked(now); full {
			wait = max(wait, first)
		}
	}
	return wait
}

// allLocked reports whether every key is locked and, if so, the shortest
// remaining lock.
func (l *limiter) allLocked(now time.Time) (time.Duration, bool) {
	var first time.Duration
	for e := l.order.Front(); e != nil; e = e.Next() {
		c := e.Value.(*counter)
		if !c.locked(now) {
			return 0, false
		}
		if d := c.lockedUntil.Sub(now); first == 0 || d < first {
			first = d
		}
	}
	return first, true
}

// fail records one failed login for username and source.
func (l *limiter) fail(username, source string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.clock.Now()
	l.prune(now)
	l.record(usernameKey(username), UsernameFailureLimit, now)
	l.record(sourceKey(source), SourceFailureLimit, now)
}

// prune drops every key left with neither failures inside the window nor a
// running lock.
func (l *limiter) prune(now time.Time) {
	for e := l.order.Front(); e != nil; {
		c := e.Value.(*counter)
		if now.Sub(c.last) < FailureWindow {
			return
		}
		e = e.Next()
		l.remove(c)
	}
}

func (l *limiter) remove(c *counter) {
	l.order.Remove(c.elem)
	delete(l.keys, c.key)
}

func recent(failures []time.Time, now time.Time) []time.Time {
	kept := failures[:0]
	for _, f := range failures {
		if now.Sub(f) < FailureWindow {
			kept = append(kept, f)
		}
	}
	return kept
}

// record counts a failure for key. A new key that finds the table full takes
// the place of the oldest unlocked key; when every key is locked it is not
// recorded (blocked refuses such attempts before they are evaluated).
func (l *limiter) record(key string, limit int, now time.Time) {
	c, ok := l.keys[key]
	if !ok {
		if len(l.keys) >= l.max && !l.dropOldestUnlocked(now) {
			return
		}
		c = &counter{key: key}
		c.elem = l.order.PushBack(c)
		l.keys[key] = c
	}
	c.last = now
	l.order.MoveToBack(c.elem)
	c.failures = append(recent(c.failures, now), now)
	if len(c.failures) >= limit {
		c.lockedUntil = now.Add(LockDuration)
		c.failures = nil
	}
}

func (l *limiter) dropOldestUnlocked(now time.Time) bool {
	for e := l.order.Front(); e != nil; e = e.Next() {
		if c := e.Value.(*counter); !c.locked(now) {
			l.remove(c)
			return true
		}
	}
	return false
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/ratelimit.go internal/controller/auth/ratelimit_test.go
git commit -m "feat(auth): rate-limit failed logins in memory"
```


### Task 4: Identity tables and user operations

**Files:**
- Create: `internal/core/store/migrations/controller/0003_identity.sql`
- Create: `internal/controller/auth/service.go`
- Test: `internal/controller/auth/service_test.go`

**Interfaces:**
- Consumes: `store.DB`, `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrateOptions`, `core.Sealer`, `core.Clock`, `core.Audit`, `core.Principal`, `core.Scope`, `coretest.Sealer`, `coretest.MemAudit`, `coretest.FakeClock`, `errcode` (foundation); `secret.CSRFLabel` (plan 2); Tasks 1, 2 and 3 (`newHasher`, `checkUsername`, `checkPassword`, `newTOTPSecret`, `encodeTOTPSecret`, `matchTOTP`, `TOTPCode`, `newLimiter`).
- Produces: Contracts › Task 4 (the three tables, `User`, `TokenInfo`, `Identity`, `Options`, `Service`, `New`, the user operations, the unexported helpers and the test helpers of `service_test.go`).

**Items:** 8, 11, 15

**Depends on:** Tasks 1, 2 and 3

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The migration is a new file beside `0001_core.sql` and `0002_lifecycle.sql`, which must not be edited; `store.CoreMigrations` embeds it with no other change. `Service` carries every field later tasks use (the limiter, the CSRF key, the audit log), so no later task edits this file. Usernames are checked for uniqueness inside the immediate write transaction rather than by parsing a constraint error. TOTP secrets are sealed with associated data `core_users.totp_secret_ct.<user id>` (ADR 0005); activation stores the accepted step, so the enrollment code cannot also log in.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/service_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const testPassword = "correct horse battery"

// newTestService returns a service over a new migrated controller database,
// with a fake clock reading t0 and an in-memory audit log.
func newTestService(t *testing.T) (*Service, *coretest.FakeClock, *coretest.MemAudit) {
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
	clock := coretest.NewFakeClock(t0)
	audit := &coretest.MemAudit{}
	return New(db, coretest.Sealer{}, clock, audit, Options{Params: testParams}), clock, audit
}

// createActive creates and activates username with testPassword and returns
// the user and its TOTP secret. It then advances the clock by one TOTP period,
// so the next code is not the step activation consumed.
func createActive(t *testing.T, s *Service, clock *coretest.FakeClock, username string) (User, string) {
	t.Helper()
	ctx := context.Background()
	_, secret, err := s.CreateUser(ctx, username, testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, err := TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	u, err := s.ActivateUser(ctx, username, code)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(TOTPPeriod)
	return u, secret
}

// count returns the number of rows of table matching where (with args).
func count(t *testing.T, s *Service, table, where string, args ...any) int {
	t.Helper()
	var n int
	if err := s.db.Read().QueryRow("SELECT count(*) FROM "+table+" WHERE "+where, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// insertSession adds a raw session row for userID, standing in for a login.
func insertSession(t *testing.T, s *Service, userID, idHash string) {
	t.Helper()
	now := formatTime(s.clock.Now())
	if _, err := s.db.Writer().Exec(`INSERT INTO core_sessions
		(id_hash, user_id, created_at, last_seen_at, expires_at, source_ip, user_agent) VALUES (?, ?, ?, ?, ?, '', '')`,
		idHash, userID, now, now, now); err != nil {
		t.Fatal(err)
	}
}

// insertToken adds a raw token row for userID.
func insertToken(t *testing.T, s *Service, userID, id string) {
	t.Helper()
	now := formatTime(s.clock.Now())
	if _, err := s.db.Writer().Exec(`INSERT INTO core_tokens
		(id, user_id, name, scope, secret_hash, created_at, expires_at) VALUES (?, ?, 'ci', 'read', '', ?, ?)`,
		id, userID, now, now); err != nil {
		t.Fatal(err)
	}
}

// wantCode fails unless err carries code.
func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("error = %v, want %s", err, code)
	}
}

func TestCreateUser(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	u, secret, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if u.ID == "" || u.Username != "alice" || u.Role != RoleAdmin || u.State != StatePending || !u.CreatedAt.Equal(t0) {
		t.Errorf("user = %+v, want a pending admin created at %s", u, t0)
	}
	key, err := b32.DecodeString(secret)
	if err != nil || len(key) != TOTPSecretSize {
		t.Fatalf("secret %q: %v, want %d bytes of unpadded base32", secret, err, TOTPSecretSize)
	}
	var hash string
	var ct []byte
	if err := s.db.Read().QueryRow(`SELECT password_hash, totp_secret_ct FROM core_users WHERE id = ?`, u.ID).
		Scan(&hash, &ct); err != nil {
		t.Fatal(err)
	}
	if ok, err := s.hasher.verify(ctx, hash, testPassword); err != nil || !ok {
		t.Errorf("stored hash does not verify the password: %v", err)
	}
	if opened, err := s.sealer.Open("core_users.totp_secret_ct."+u.ID, ct); err != nil || string(opened) != string(key) {
		t.Errorf("stored TOTP secret does not open with the row's associated data: %v", err)
	}
	if _, err := s.sealer.Open("core_users.totp_secret_ct.other", ct); err == nil {
		t.Error("stored TOTP secret opens with another row's associated data")
	}
	if strings.Contains(string(ct), string(key)) {
		t.Error("TOTP secret stored in clear")
	}
}

func TestCreateUserRefusals(t *testing.T) {
	s, _, _ := newTestService(t)
	ctx := context.Background()
	if _, _, err := s.CreateUser(ctx, "alice", testPassword); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, username, password, path string
	}{
		{"duplicate", "alice", testPassword, "/username"},
		{"bad username", "Alice", testPassword, "/username"},
		{"short password", "bob", "short pass", "/password"},
	} {
		_, _, err := s.CreateUser(ctx, tt.username, tt.password)
		if err == nil {
			t.Errorf("%s: created", tt.name)
			continue
		}
		wantField(t, err, tt.path)
		if strings.Contains(err.Error(), tt.password) {
			t.Errorf("%s: error %q quotes the password", tt.name, err)
		}
	}
	if n := count(t, s, "core_users", "1 = 1"); n != 1 {
		t.Errorf("%d users, want 1", n)
	}
}

func TestActivateUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, _ := TOTPCode(secret, clock.Now())
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	_, err = s.ActivateUser(ctx, "alice", wrong)
	wantField(t, err, "/code")
	if _, err := s.ActivateUser(ctx, "nobody", code); err == nil {
		t.Error("activated an unknown user")
	} else {
		wantCode(t, err, errcode.NotFound)
	}
	u, err := s.ActivateUser(ctx, "alice", code)
	if err != nil {
		t.Fatal(err)
	}
	if u.State != StateActive {
		t.Errorf("state = %s, want active", u.State)
	}
	if n := count(t, s, "core_users", "id = ? AND totp_last_step = ?", u.ID, totpStep(clock.Now())); n != 1 {
		t.Error("activation did not store the accepted step")
	}
	_, err = s.ActivateUser(ctx, "alice", code)
	wantField(t, err, "/username")
}

func TestResetTOTP(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	insertSession(t, s, u.ID, "h1")
	insertToken(t, s, u.ID, "01TOKEN")
	got, fresh, err := s.ResetTOTP(ctx, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StatePending || fresh == secret {
		t.Errorf("after reset: state %s, new secret differs %v; want pending and a new secret", got.State, fresh != secret)
	}
	if count(t, s, "core_sessions", "user_id = ?", u.ID) != 0 {
		t.Error("reset-totp kept the user's sessions")
	}
	if count(t, s, "core_tokens", "user_id = ?", u.ID) != 1 {
		t.Error("reset-totp removed the user's tokens")
	}
	code, _ := TOTPCode(fresh, clock.Now())
	if _, err := s.ActivateUser(ctx, "alice", code); err != nil {
		t.Errorf("activate with the new secret: %v", err)
	}
	_, _, err = s.ResetTOTP(ctx, "nobody")
	wantCode(t, err, errcode.NotFound)
}

func TestResetPassword(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	insertSession(t, s, u.ID, "h1")
	const next = "a different long password"
	if _, err := s.ResetPassword(ctx, "alice", next); err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.Read().QueryRow(`SELECT password_hash FROM core_users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if ok, _ := s.hasher.verify(ctx, hash, next); !ok {
		t.Error("the new password does not verify")
	}
	if count(t, s, "core_sessions", "user_id = ?", u.ID) != 0 {
		t.Error("reset-password kept the user's sessions")
	}
	_, err := s.ResetPassword(ctx, "alice", "too short")
	wantField(t, err, "/password")
	_, err = s.ResetPassword(ctx, "nobody", next)
	wantCode(t, err, errcode.NotFound)
}

func TestDeleteUser(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	alice, _ := createActive(t, s, clock, "alice")
	if _, _, err := s.CreateUser(ctx, "pending", testPassword); err != nil {
		t.Fatal(err)
	}
	err := s.DeleteUser(ctx, "alice")
	wantField(t, err, "/username")
	if err := s.DeleteUser(ctx, "pending"); err != nil {
		t.Errorf("delete a pending user beside the last admin: %v", err)
	}
	bob, _ := createActive(t, s, clock, "bob")
	insertSession(t, s, bob.ID, "h1")
	insertToken(t, s, bob.ID, "01TOKEN")
	if err := s.DeleteUser(ctx, "bob"); err != nil {
		t.Fatal(err)
	}
	if count(t, s, "core_sessions", "user_id = ?", bob.ID)+count(t, s, "core_tokens", "user_id = ?", bob.ID) != 0 {
		t.Error("delete kept the user's sessions or tokens")
	}
	if _, err := s.GetUser(ctx, "bob"); err == nil {
		t.Error("deleted user still found")
	}
	wantCode(t, s.DeleteUser(ctx, "bob"), errcode.NotFound)
	if _, err := s.GetUser(ctx, "alice"); err != nil || alice.ID == "" {
		t.Errorf("alice: %v", err)
	}
}

func TestListAndGetUsers(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	empty, err := s.ListUsers(ctx)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ListUsers on no users = %v, %v; want an empty non-nil slice", empty, err)
	}
	bob, _ := createActive(t, s, clock, "bob")
	alice, _, err := s.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	users, err := s.ListUsers(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(users) != 2 || users[0].ID != alice.ID || users[1].ID != bob.ID {
		t.Fatalf("ListUsers = %+v, want alice then bob", users)
	}
	if users[1].State != StateActive || users[0].State != StatePending {
		t.Errorf("states = %s, %s; want pending, active", users[0].State, users[1].State)
	}
	got, err := s.GetUser(ctx, "bob")
	if err != nil || got.ID != bob.ID || !got.CreatedAt.Equal(bob.CreatedAt) {
		t.Errorf("GetUser(bob) = %+v, %v; want %+v", got, err, bob)
	}
	_, err = s.GetUser(ctx, "nobody")
	wantCode(t, err, errcode.NotFound)
	_, err = s.GetUser(ctx, "Not Valid")
	wantCode(t, err, errcode.NotFound)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `undefined: Service`, `undefined: New` and `undefined: Options`.

- [ ] **Step 3: Write the implementation**

Create `internal/core/store/migrations/controller/0003_identity.sql`:

```sql
-- Identity tables (spec sections 8 and 9), read and written only through
-- internal/controller/auth. Times are fixed-width RFC 3339 UTC text, so they
-- compare as text. Sessions are keyed by the hex SHA-256 of the session id and
-- tokens store the hex SHA-256 of their secret; neither secret is stored.
-- Role is checked in code, not here, so a later role needs no table rebuild.

CREATE TABLE core_users (
    id             TEXT PRIMARY KEY,
    username       TEXT NOT NULL UNIQUE,
    role           TEXT NOT NULL,
    state          TEXT NOT NULL CHECK (state IN ('pending', 'active')),
    password_hash  TEXT NOT NULL,
    totp_secret_ct BLOB NOT NULL,
    totp_last_step INTEGER NOT NULL DEFAULT 0,
    created_at     TEXT NOT NULL,
    updated_at     TEXT NOT NULL
) STRICT;

CREATE TABLE core_sessions (
    id_hash      TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES core_users (id) ON DELETE CASCADE,
    created_at   TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    source_ip    TEXT NOT NULL,
    user_agent   TEXT NOT NULL
) STRICT;

CREATE INDEX core_sessions_user ON core_sessions (user_id);

CREATE TABLE core_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES core_users (id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    scope        TEXT NOT NULL CHECK (scope IN ('read', 'operate', 'admin')),
    secret_hash  TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    expires_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked_at   TEXT
) STRICT;

CREATE INDEX core_tokens_user ON core_tokens (user_id);
```

Create `internal/controller/auth/service.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"database/sql"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// The only role (R19) and the two user states (spec section 8).
const (
	RoleAdmin    = "admin"
	StatePending = "pending"
	StateActive  = "active"
)

// User is a user as the API and the CLI show it (spec section 12).
type User struct {
	ID        string    `json:"id"`
	Username  string    `json:"username"`
	Role      string    `json:"role"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"createdAt"`
}

// TokenInfo describes an API token without its secret (spec section 12).
type TokenInfo struct {
	ID         string     `json:"id"`
	Name       string     `json:"name"`
	Scope      core.Scope `json:"scope"`
	CreatedAt  time.Time  `json:"createdAt"`
	ExpiresAt  time.Time  `json:"expiresAt"`
	LastUsedAt *time.Time `json:"lastUsedAt"`
	RevokedAt  *time.Time `json:"revokedAt"`
}

// Identity is the authenticated caller of a request.
type Identity struct {
	Principal   core.Principal
	FromSession bool       // true for a session cookie, false for a bearer token
	User        User       // the principal's user
	ExpiresAt   time.Time  // when the session or token ends, as of this request
	Token       *TokenInfo // the bearer token; nil for a session
}

// Options configure a Service.
type Options struct {
	// Params are the argon2id parameters of new hashes; the zero value means
	// DefaultParams.
	Params Params
}

// Service is the controller's identity over the core_users, core_sessions
// and core_tokens tables. Its methods are safe for concurrent use.
type Service struct {
	db      *store.DB
	sealer  core.Sealer
	clock   core.Clock
	audit   core.Audit
	hasher  *hasher
	limiter *limiter
	csrfKey []byte

	idMu    sync.Mutex
	entropy *ulid.MonotonicEntropy
}

// New returns the identity service of the controller database db. It hashes
// once to prepare the unknown-user dummy hash.
func New(db *store.DB, sealer core.Sealer, clock core.Clock, audit core.Audit, opts Options) *Service {
	p := opts.Params
	if p == (Params{}) {
		p = DefaultParams
	}
	return &Service{
		db: db, sealer: sealer, clock: clock, audit: audit,
		hasher: newHasher(p), limiter: newLimiter(clock), csrfKey: sealer.Derive(secret.CSRFLabel),
		entropy: ulid.Monotonic(rand.Reader, 0),
	}
}

// newID returns a ULID stamped with the service's clock; ids it makes sort in
// creation order.
func (s *Service) newID() string {
	s.idMu.Lock()
	defer s.idMu.Unlock()
	return ulid.MustNew(ulid.Timestamp(s.clock.Now()), s.entropy).String()
}

// now is the clock's time in UTC, truncated to what the database keeps.
func (s *Service) now() time.Time { return s.clock.Now().UTC().Round(0) }

// timeFormat is RFC 3339 in UTC with a fixed width, so stored times compare
// as text.
const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(timeFormat, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("auth: stored time %q: %w", s, err)
	}
	return t, nil
}

// totpAD is the associated data sealing a user's TOTP secret (ADR 0005).
func totpAD(userID string) string { return "core_users.totp_secret_ct." + userID }

func invalidField(path, message string) *errcode.Error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid input",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

func errUserNotFound() *errcode.Error { return errcode.New(errcode.NotFound, "user not found") }

// querier is a transaction or the reader pool.
type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// userRecord is a core_users row.
type userRecord struct {
	User
	passwordHash string
	totpCT       []byte
	lastStep     int64
}

const userColumns = `id, username, role, state, created_at, password_hash, totp_secret_ct, totp_last_step`

// loadUser reads username's row; an absent user is errcode.NotFound.
func loadUser(ctx context.Context, q querier, username string) (userRecord, error) {
	var r userRecord
	var created string
	err := q.QueryRowContext(ctx, `SELECT `+userColumns+` FROM core_users WHERE username = ?`, username).
		Scan(&r.ID, &r.Username, &r.Role, &r.State, &created, &r.passwordHash, &r.totpCT, &r.lastStep)
	if errors.Is(err, sql.ErrNoRows) {
		return userRecord{}, errUserNotFound()
	}
	if err != nil {
		return userRecord{}, fmt.Errorf("auth: load user: %w", err)
	}
	if r.CreatedAt, err = parseTime(created); err != nil {
		return userRecord{}, err
	}
	return r, nil
}

// CreateUser adds a pending admin with password and a new TOTP secret, and
// returns the user and the secret in unpadded base32, the only time it is
// shown. A taken or invalid username or an invalid password is
// request.invalid_input.
func (s *Service) CreateUser(ctx context.Context, username, password string) (User, string, error) {
	if err := checkUsername(username); err != nil {
		return User{}, "", err
	}
	if err := checkPassword(password); err != nil {
		return User{}, "", err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return User{}, "", err
	}
	key := newTOTPSecret()
	u := User{ID: s.newID(), Username: username, Role: RoleAdmin, State: StatePending, CreatedAt: s.now()}
	ct, err := s.sealer.Seal(totpAD(u.ID), key)
	if err != nil {
		return User{}, "", fmt.Errorf("auth: seal TOTP secret: %w", err)
	}
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		var n int
		if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE username = ?`, username).Scan(&n); err != nil {
			return fmt.Errorf("auth: create user: %w", err)
		}
		if n > 0 {
			return invalidField("/username", "a user with this name already exists")
		}
		at := formatTime(u.CreatedAt)
		_, err := tx.ExecContext(ctx, `INSERT INTO core_users
			(id, username, role, state, password_hash, totp_secret_ct, totp_last_step, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, 0, ?, ?)`, u.ID, u.Username, u.Role, u.State, hash, ct, at, at)
		if err != nil {
			return fmt.Errorf("auth: create user: %w", err)
		}
		return nil
	})
	if err != nil {
		return User{}, "", err
	}
	return u, encodeTOTPSecret(key), nil
}

// ActivateUser confirms a pending user's enrollment with a TOTP code and makes
// the user active. The code's step is stored, so it cannot also log in. A
// wrong code or a user that is not pending is request.invalid_input.
func (s *Service) ActivateUser(ctx context.Context, username, code string) (User, error) {
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.State != StatePending {
			return invalidField("/username", "the user is not pending")
		}
		key, err := s.sealer.Open(totpAD(r.ID), r.totpCT)
		if err != nil {
			return fmt.Errorf("auth: open TOTP secret: %w", err)
		}
		step, ok := matchTOTP(key, code, s.clock.Now(), r.lastStep)
		if !ok {
			return invalidField("/code", "does not match the authenticator")
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, totp_last_step = ?, updated_at = ? WHERE id = ?`,
			StateActive, step, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: activate user: %w", err)
		}
		r.State = StateActive
		u = r.User
		return nil
	})
	return u, err
}

// endSessions deletes every session of userID inside tx.
func endSessions(ctx context.Context, tx *sql.Tx, userID string) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM core_sessions WHERE user_id = ?`, userID); err != nil {
		return fmt.Errorf("auth: end sessions: %w", err)
	}
	return nil
}

// ResetTOTP gives username a new TOTP secret, returns the user to pending and
// ends its sessions; it returns the user and the new secret in unpadded
// base32. This is the only recovery for a lost device (owner decision).
func (s *Service) ResetTOTP(ctx context.Context, username string) (User, string, error) {
	key := newTOTPSecret()
	var u User
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		ct, err := s.sealer.Seal(totpAD(r.ID), key)
		if err != nil {
			return fmt.Errorf("auth: seal TOTP secret: %w", err)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET state = ?, totp_secret_ct = ?, totp_last_step = 0,
			updated_at = ? WHERE id = ?`, StatePending, ct, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: reset TOTP: %w", err)
		}
		r.State = StatePending
		u = r.User
		return endSessions(ctx, tx, r.ID)
	})
	if err != nil {
		return User{}, "", err
	}
	return u, encodeTOTPSecret(key), nil
}

// ResetPassword sets username's password and ends its sessions. An invalid
// password is request.invalid_input.
func (s *Service) ResetPassword(ctx context.Context, username, password string) (User, error) {
	if err := checkPassword(password); err != nil {
		return User{}, err
	}
	if _, err := s.GetUser(ctx, username); err != nil {
		return User{}, err
	}
	hash, err := s.hasher.hash(ctx, password)
	if err != nil {
		return User{}, err
	}
	var u User
	err = s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE core_users SET password_hash = ?, updated_at = ? WHERE id = ?`,
			hash, formatTime(s.now()), r.ID); err != nil {
			return fmt.Errorf("auth: reset password: %w", err)
		}
		u = r.User
		return endSessions(ctx, tx, r.ID)
	})
	return u, err
}

// DeleteUser deletes username with its sessions and tokens. Deleting the last
// active admin is request.invalid_input.
func (s *Service) DeleteUser(ctx context.Context, username string) error {
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		r, err := loadUser(ctx, tx, username)
		if err != nil {
			return err
		}
		if r.State == StateActive && r.Role == RoleAdmin {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM core_users WHERE state = ? AND role = ?`,
				StateActive, RoleAdmin).Scan(&n); err != nil {
				return fmt.Errorf("auth: delete user: %w", err)
			}
			if n <= 1 {
				return invalidField("/username", "the user is the last active admin")
			}
		}
		for _, q := range []string{
			`DELETE FROM core_tokens WHERE user_id = ?`,
			`DELETE FROM core_sessions WHERE user_id = ?`,
			`DELETE FROM core_users WHERE id = ?`,
		} {
			if _, err := tx.ExecContext(ctx, q, r.ID); err != nil {
				return fmt.Errorf("auth: delete user: %w", err)
			}
		}
		return nil
	})
}

// ListUsers returns every user sorted by username; no users is an empty,
// non-nil slice.
func (s *Service) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT id, username, role, state, created_at FROM core_users ORDER BY username`)
	if err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	users := []User{}
	for rows.Next() {
		var u User
		var created string
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.State, &created); err != nil {
			return nil, fmt.Errorf("auth: list users: %w", err)
		}
		if u.CreatedAt, err = parseTime(created); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list users: %w", err)
	}
	return users, nil
}

// GetUser returns username; an unknown user is errcode.NotFound.
func (s *Service) GetUser(ctx context.Context, username string) (User, error) {
	r, err := loadUser(ctx, s.db.Read(), username)
	return r.User, err
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/core/store/migrations/controller/0003_identity.sql internal/controller/auth/service.go internal/controller/auth/service_test.go
git commit -m "feat(auth): store users with sealed TOTP secrets"
```


### Task 5: Sessions and CSRF

**Files:**
- Create: `internal/controller/auth/session.go`
- Test: `internal/controller/auth/session_test.go`

**Interfaces:**
- Consumes: Task 4 (`Service`, `Identity`, `User`, `RoleAdmin`, `StateActive`, `formatTime`, `parseTime`, `now`, the `core_sessions` table, the test helpers `newTestService`, `createActive`, `count`, `wantCode`); `secret.CSRFLabel` (plan 2) and `coretest.Sealer` in the test.
- Produces: Contracts › Task 5 (`SessionIdle`, `SessionLifetime`, `LastSeenInterval`, `CSRFToken`, `VerifyCSRF`, `errAuthRequired`, `createSession`, `authenticateSession`, `endSession` and the other unexported names, the test helpers `newSession` and `lastSeen`).

**Items:** 12, 52

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Cookie names and attributes belong to the API (P9); this task deals only in the raw session id and its expiry. The database keys a session by the hex SHA-256 of the id's 32 bytes; the CSRF token is an HMAC of the same bytes under the key `New` derived, so nothing extra is stored. `last_seen_at` is written only when it is a minute or more old, the only write a GET request causes.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/session_test.go`:

```go
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
)

// newSession creates a session for userID as a login would.
func newSession(t *testing.T, s *Service, userID string) (string, time.Time) {
	t.Helper()
	var id string
	var exp time.Time
	err := s.db.Write(context.Background(), func(tx *sql.Tx) error {
		var err error
		id, exp, err = s.createSession(context.Background(), tx, userID, "10.0.0.1", "test-agent")
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return id, exp
}

func lastSeen(t *testing.T, s *Service, id string) time.Time {
	t.Helper()
	raw, ok := decodeSessionID(id)
	if !ok {
		t.Fatalf("session id %q does not decode", id)
	}
	var at string
	if err := s.db.Read().QueryRow(`SELECT last_seen_at FROM core_sessions WHERE id_hash = ?`, sessionHash(raw)).Scan(&at); err != nil {
		t.Fatal(err)
	}
	v, err := parseTime(at)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSessionStoresOnlyItsHash(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, exp := newSession(t, s, u.ID)
	raw, ok := decodeSessionID(id)
	if !ok || len(raw) != 32 {
		t.Fatalf("session id %q is not 32 bytes of unpadded base64url", id)
	}
	if want := clock.Now().Add(SessionIdle); !exp.Equal(want) {
		t.Errorf("expiry = %s, want %s", exp, want)
	}
	sum := sha256.Sum256(raw)
	if count(t, s, "core_sessions", "id_hash = ? AND user_id = ? AND source_ip = '10.0.0.1' AND user_agent = 'test-agent'",
		hex.EncodeToString(sum[:]), u.ID) != 1 {
		t.Fatal("no session row keyed by the hex SHA-256 of the id")
	}
	if count(t, s, "core_sessions", "id_hash = ?", id) != 0 {
		t.Error("the raw session id is stored")
	}
	other, _ := newSession(t, s, u.ID)
	if other == id {
		t.Error("two sessions share an id")
	}
}

func TestAuthenticateSession(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	start := clock.Now()
	clock.Advance(30 * time.Minute)
	got, err := s.authenticateSession(ctx, id, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	want := core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "10.0.0.2"}
	if got.Principal != want || !got.FromSession || got.Token != nil || got.User.ID != u.ID {
		t.Errorf("identity = %+v, want principal %+v from a session", got, want)
	}
	if exp := clock.Now().Add(SessionIdle); !got.ExpiresAt.Equal(exp) {
		t.Errorf("expiry = %s, want %s (idle limit from this request)", got.ExpiresAt, exp)
	}
	for range 11 {
		clock.Advance(59 * time.Minute)
		if _, err := s.authenticateSession(ctx, id, "10.0.0.2"); err != nil {
			t.Fatalf("active session refused at %s: %v", clock.Now().Sub(start), err)
		}
	}
	got, err = s.authenticateSession(ctx, id, "10.0.0.2")
	if err != nil {
		t.Fatal(err)
	}
	if abs := start.Add(SessionLifetime); !got.ExpiresAt.Equal(abs) {
		t.Errorf("expiry near the end = %s, want the absolute limit %s", got.ExpiresAt, abs)
	}
	clock.Advance(start.Add(SessionLifetime).Sub(clock.Now()))
	_, err = s.authenticateSession(ctx, id, "10.0.0.2")
	wantCode(t, err, errcode.AuthRequired)
}

func TestSessionIdleExpiry(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	clock.Advance(SessionIdle - time.Second)
	if _, err := s.authenticateSession(context.Background(), id, ""); err != nil {
		t.Fatalf("refused one second before the idle limit: %v", err)
	}
	clock.Advance(SessionIdle)
	_, err := s.authenticateSession(context.Background(), id, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestLastSeenWrittenAtMostOnceAMinute(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	created := clock.Now()
	clock.Advance(LastSeenInterval - time.Second)
	if _, err := s.authenticateSession(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if got := lastSeen(t, s, id); !got.Equal(created) {
		t.Errorf("last_seen_at = %s after %s, want unchanged %s", got, LastSeenInterval-time.Second, created)
	}
	clock.Advance(time.Second)
	if _, err := s.authenticateSession(ctx, id, ""); err != nil {
		t.Fatal(err)
	}
	if got := lastSeen(t, s, id); !got.Equal(clock.Now()) {
		t.Errorf("last_seen_at = %s after %s, want %s", got, LastSeenInterval, clock.Now())
	}
}

func TestAuthenticateSessionRefusals(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	unknown, _ := newSessionID()
	for _, bad := range []string{"", "short", id + "x", strings.ToUpper(id), unknown} {
		_, err := s.authenticateSession(ctx, bad, "")
		wantCode(t, err, errcode.AuthRequired)
	}
	if _, err := s.db.Writer().Exec(`UPDATE core_users SET state = 'pending' WHERE id = ?`, u.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.authenticateSession(ctx, id, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestEndSession(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	keep, _ := newSession(t, s, u.ID)
	userID, ok, err := s.endSession(ctx, id)
	if err != nil || !ok || userID != u.ID {
		t.Fatalf("endSession = %q, %v, %v; want %s, true", userID, ok, err, u.ID)
	}
	if _, err := s.authenticateSession(ctx, id, ""); err == nil {
		t.Error("ended session still authenticates")
	}
	if _, err := s.authenticateSession(ctx, keep, ""); err != nil {
		t.Errorf("another session of the user ended: %v", err)
	}
	if _, ok, err := s.endSession(ctx, id); err != nil || ok {
		t.Errorf("ending an ended session = %v, %v; want false, nil", ok, err)
	}
	if _, ok, err := s.endSession(ctx, "malformed"); err != nil || ok {
		t.Errorf("ending a malformed id = %v, %v; want false, nil", ok, err)
	}
}

func TestCSRF(t *testing.T) {
	s, clock, _ := newTestService(t)
	u, _ := createActive(t, s, clock, "alice")
	id, _ := newSession(t, s, u.ID)
	other, _ := newSession(t, s, u.ID)
	token := s.CSRFToken(id)
	raw, _ := decodeSessionID(id)
	m := hmac.New(sha256.New, coretest.Sealer{}.Derive(secret.CSRFLabel))
	m.Write(raw)
	if want := b64url.EncodeToString(m.Sum(nil)); token != want {
		t.Fatalf("CSRF token = %q, want HMAC-SHA256 of the session id under the derived key %q", token, want)
	}
	if s.CSRFToken(id) != token {
		t.Error("CSRF token is not deterministic")
	}
	if !s.VerifyCSRF(id, token) {
		t.Error("the session's own token is refused")
	}
	tampered := []byte(token)
	tampered[0] ^= 1
	for name, tt := range map[string][2]string{
		"another session's token": {id, s.CSRFToken(other)},
		"tampered token":          {id, string(tampered)},
		"empty token":             {id, ""},
		"malformed session id":    {"malformed", s.CSRFToken("malformed")},
	} {
		if s.VerifyCSRF(tt[0], tt[1]) {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestTruncate(t *testing.T) {
	long := strings.Repeat("a", maxUserAgent-1) + "é"
	got := truncate(long, maxUserAgent)
	if got != strings.Repeat("a", maxUserAgent-1) {
		t.Errorf("truncate split a rune: %d bytes", len(got))
	}
	if truncate("short", maxUserAgent) != "short" {
		t.Error("truncate changed a short value")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `s.createSession undefined (type *Service has no field or method createSession)`, `undefined: decodeSessionID` and `undefined: sessionHash`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/session.go`:

```go
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"time"
	"unicode/utf8"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Session limits (spec section 9).
const (
	SessionIdle      = time.Hour
	SessionLifetime  = 12 * time.Hour
	LastSeenInterval = time.Minute
	sessionIDSize    = 32
	maxUserAgent     = 512
)

var b64url = base64.RawURLEncoding

func errAuthRequired() *errcode.Error {
	return errcode.New(errcode.AuthRequired, "authentication required")
}

// newSessionID returns a new session id, 32 random bytes in unpadded
// base64url, and those bytes.
func newSessionID() (string, []byte) {
	raw := make([]byte, sessionIDSize)
	_, _ = rand.Read(raw)
	return b64url.EncodeToString(raw), raw
}

func decodeSessionID(id string) ([]byte, bool) {
	raw, err := b64url.DecodeString(id)
	if err != nil || len(raw) != sessionIDSize {
		return nil, false
	}
	return raw, true
}

// sessionHash is the core_sessions key: the hex SHA-256 of the id's bytes.
func sessionHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// truncate shortens s to at most n bytes without splitting a UTF-8 sequence.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	cut := n
	for cut > 0 && cut > n-utf8.UTFMax && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// createSession inserts a session for userID inside tx and returns its id and
// its expiry, the idle limit from now. The user agent is kept to 512 bytes.
func (s *Service) createSession(ctx context.Context, tx *sql.Tx, userID, source, userAgent string) (string, time.Time, error) {
	id, raw := newSessionID()
	now := s.now()
	at := formatTime(now)
	_, err := tx.ExecContext(ctx, `INSERT INTO core_sessions
		(id_hash, user_id, created_at, last_seen_at, expires_at, source_ip, user_agent) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		sessionHash(raw), userID, at, at, formatTime(now.Add(SessionLifetime)), source, truncate(userAgent, maxUserAgent))
	if err != nil {
		return "", time.Time{}, fmt.Errorf("auth: create session: %w", err)
	}
	return id, now.Add(min(SessionIdle, SessionLifetime)), nil
}

// roleScope is the scope a session of a user with role carries (spec section
// 10).
func roleScope(role string) (core.Scope, bool) {
	if role == RoleAdmin {
		return core.ScopeAdmin, true
	}
	return "", false
}

// authenticateSession resolves a session cookie value. An unknown, malformed
// or expired id, or a user that is not active, is auth.required. It moves
// last_seen_at forward when it is a minute or more old.
func (s *Service) authenticateSession(ctx context.Context, id, source string) (Identity, error) {
	raw, ok := decodeSessionID(id)
	if !ok {
		return Identity{}, errAuthRequired()
	}
	key := sessionHash(raw)
	var u User
	var seenText, expText, createdText string
	err := s.db.Read().QueryRowContext(ctx, `SELECT s.last_seen_at, s.expires_at, u.id, u.username, u.role, u.state, u.created_at
		FROM core_sessions s JOIN core_users u ON u.id = s.user_id WHERE s.id_hash = ?`, key).
		Scan(&seenText, &expText, &u.ID, &u.Username, &u.Role, &u.State, &createdText)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, errAuthRequired()
	}
	if err != nil {
		return Identity{}, fmt.Errorf("auth: load session: %w", err)
	}
	seen, err := parseTime(seenText)
	if err != nil {
		return Identity{}, err
	}
	exp, err := parseTime(expText)
	if err != nil {
		return Identity{}, err
	}
	if u.CreatedAt, err = parseTime(createdText); err != nil {
		return Identity{}, err
	}
	now := s.now()
	scope, ok := roleScope(u.Role)
	if !now.Before(exp) || !now.Before(seen.Add(SessionIdle)) || u.State != StateActive || !ok {
		return Identity{}, errAuthRequired()
	}
	if now.Sub(seen) >= LastSeenInterval {
		err := s.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE core_sessions SET last_seen_at = ? WHERE id_hash = ? AND last_seen_at < ?`,
				formatTime(now), key, formatTime(now))
			return err
		})
		if err != nil {
			return Identity{}, fmt.Errorf("auth: touch session: %w", err)
		}
		seen = now
	}
	if idle := seen.Add(SessionIdle); idle.Before(exp) {
		exp = idle
	}
	return Identity{
		Principal:   core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: scope, Source: source},
		FromSession: true, User: u, ExpiresAt: exp,
	}, nil
}

// endSession deletes the session id and returns its user; ok is false when
// no such session exists.
func (s *Service) endSession(ctx context.Context, id string) (string, bool, error) {
	raw, ok := decodeSessionID(id)
	if !ok {
		return "", false, nil
	}
	var userID string
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		return tx.QueryRowContext(ctx, `DELETE FROM core_sessions WHERE id_hash = ? RETURNING user_id`, sessionHash(raw)).Scan(&userID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("auth: end session: %w", err)
	}
	return userID, true, nil
}

// CSRFToken returns the CSRF token of a session: HMAC-SHA256 of the session
// id's bytes under the derived CSRF key, in unpadded base64url. A malformed id
// has no token ("").
func (s *Service) CSRFToken(sessionID string) string {
	raw, ok := decodeSessionID(sessionID)
	if !ok {
		return ""
	}
	m := hmac.New(sha256.New, s.csrfKey)
	m.Write(raw)
	return b64url.EncodeToString(m.Sum(nil))
}

// VerifyCSRF reports, in constant time, whether token is the session's CSRF
// token.
func (s *Service) VerifyCSRF(sessionID, token string) bool {
	want := s.CSRFToken(sessionID)
	return want != "" && subtle.ConstantTimeCompare([]byte(want), []byte(token)) == 1
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/session.go internal/controller/auth/session_test.go
git commit -m "feat(auth): add sessions and CSRF tokens"
```


### Task 6: API tokens and purge

**Files:**
- Create: `internal/controller/auth/token.go`
- Test: `internal/controller/auth/token_test.go`

**Interfaces:**
- Consumes: Task 4 (`Service`, `TokenInfo`, `Identity`, `User`, `StateActive`, `formatTime`, `parseTime`, `now`, `newID`, `invalidField`, `errUserNotFound`, `querier`, the `core_tokens` table, the test helpers), Task 2 (`b32`), Task 5 (`errAuthRequired`, `SessionIdle`; the test helpers `newSession`, `decodeSessionID`, `sessionHash`, `authenticateSession`).
- Produces: Contracts › Task 6 (the token constants, `CreateToken`, `ListTokens`, `GetToken`, `RevokeToken`, `Purge`, `authenticateToken`, `parseToken`, the test helper `day`).

**Items:** 13

**Depends on:** Task 5

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Tokens are `dpt_<ULID>_<52 base32 characters>`; only the hex SHA-256 of the 32 secret bytes is stored. The parser accepts only the canonical base32 spelling, because the decoder ignores the last character's four spare bits. `Purge` also deletes expired sessions, so the server has one purge call for the identity tables.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/token_test.go`:

```go
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var tokenPattern = regexp.MustCompile(`^dpt_[0-9A-HJKMNP-TV-Z]{26}_[A-Z2-7]{52}$`)

const day = 24 * time.Hour

func TestCreateToken(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeRead, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !tokenPattern.MatchString(token) {
		t.Fatalf("token %q does not match dpt_<ULID>_<52 base32 characters>", token)
	}
	if !strings.HasPrefix(token, TokenPrefix+info.ID+"_") {
		t.Errorf("token %q does not carry the id %s", token, info.ID)
	}
	now := clock.Now()
	if info.Name != "ci" || info.Scope != core.ScopeRead || !info.CreatedAt.Equal(now) ||
		!info.ExpiresAt.Equal(now.Add(DefaultTokenDays*day)) || info.LastUsedAt != nil || info.RevokedAt != nil {
		t.Errorf("info = %+v, want a read token expiring in %d days", info, DefaultTokenDays)
	}
	raw, err := b32.DecodeString(token[len(TokenPrefix)+27:])
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(raw)
	if count(t, s, "core_tokens", "id = ? AND secret_hash = ?", info.ID, hex.EncodeToString(sum[:])) != 1 {
		t.Error("the stored hash is not the hex SHA-256 of the secret")
	}
	if count(t, s, "core_tokens", "secret_hash LIKE ?", "%"+token[len(TokenPrefix)+27:]+"%") != 0 {
		t.Error("the secret is stored")
	}
	year, _, err := s.CreateToken(ctx, u.ID, "year", core.ScopeAdmin, MaxTokenDays)
	if err != nil || !year.ExpiresAt.Equal(now.Add(MaxTokenDays*day)) {
		t.Errorf("365-day token = %+v, %v", year, err)
	}
}

func TestCreateTokenRefusals(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	for _, tt := range []struct {
		name  string
		scope core.Scope
		days  int
		path  string
	}{
		{"ci", core.ScopeRead, MaxTokenDays + 1, "/expiresInDays"},
		{"ci", core.ScopeRead, -1, "/expiresInDays"},
		{"ci", "root", 1, "/scope"},
		{"", core.ScopeRead, 1, "/name"},
		{strings.Repeat("n", 65), core.ScopeRead, 1, "/name"},
	} {
		_, _, err := s.CreateToken(ctx, u.ID, tt.name, tt.scope, tt.days)
		wantField(t, err, tt.path)
	}
	_, _, err := s.CreateToken(ctx, "01NOSUCHUSER", "ci", core.ScopeRead, 1)
	wantCode(t, err, errcode.NotFound)
	if n := count(t, s, "core_tokens", "1 = 1"); n != 0 {
		t.Errorf("%d tokens created by refused calls", n)
	}
}

func TestAuthenticateToken(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeOperate, 1)
	if err != nil {
		t.Fatal(err)
	}
	got, err := s.authenticateToken(ctx, token, "10.0.0.5")
	if err != nil {
		t.Fatal(err)
	}
	want := core.Principal{Actor: "token:" + info.ID, UserID: u.ID, TokenID: info.ID, Scope: core.ScopeOperate, Source: "10.0.0.5"}
	if got.Principal != want || got.FromSession || got.Token == nil || got.Token.ID != info.ID ||
		got.User.ID != u.ID || !got.ExpiresAt.Equal(info.ExpiresAt) {
		t.Errorf("identity = %+v, want principal %+v from a token", got, want)
	}
	used := clock.Now()
	if got.Token.LastUsedAt == nil || !got.Token.LastUsedAt.Equal(used) {
		t.Errorf("lastUsedAt = %v, want %s", got.Token.LastUsedAt, used)
	}
	clock.Advance(LastUsedInterval - time.Second)
	if got, _ := s.GetToken(ctx, u.ID, info.ID); got.LastUsedAt == nil || !got.LastUsedAt.Equal(used) {
		t.Fatalf("lastUsedAt = %v, want %s", got.LastUsedAt, used)
	}
	if _, err := s.authenticateToken(ctx, token, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetToken(ctx, u.ID, info.ID); !got.LastUsedAt.Equal(used) {
		t.Errorf("lastUsedAt moved within a minute: %v", got.LastUsedAt)
	}
	clock.Advance(time.Second)
	if _, err := s.authenticateToken(ctx, token, ""); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetToken(ctx, u.ID, info.ID); !got.LastUsedAt.Equal(clock.Now()) {
		t.Errorf("lastUsedAt = %v after a minute, want %s", got.LastUsedAt, clock.Now())
	}

	mid := len(token) - 10
	flipped := token[:mid] + map[bool]string{true: "B", false: "A"}[token[mid] == 'A'] + token[mid+1:]
	// The last character carries one bit of the secret and four spare bits;
	// setting a spare bit decodes to the same bytes.
	spare := token[:len(token)-1] + string(token[len(token)-1]+1)
	other, _, err := s.CreateToken(ctx, u.ID, "other", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	swapped := TokenPrefix + other.ID + token[len(TokenPrefix)+26:]
	for _, bad := range []string{"", "dpt_", flipped, spare, swapped, strings.TrimPrefix(token, TokenPrefix),
		"dpx_" + token[4:], token + "A", strings.ToLower(token), token[:len(token)-1]} {
		_, err := s.authenticateToken(ctx, bad, "")
		wantCode(t, err, errcode.AuthRequired)
	}
	clock.Advance(day)
	_, err = s.authenticateToken(ctx, token, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestTokenOfInactiveOrRevoked(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	createActive(t, s, clock, "bob")
	revoked, revokedToken, _ := s.CreateToken(ctx, u.ID, "a", core.ScopeRead, 1)
	_, pendingToken, _ := s.CreateToken(ctx, u.ID, "b", core.ScopeRead, 1)
	if _, err := s.RevokeToken(ctx, u.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	_, err := s.authenticateToken(ctx, revokedToken, "")
	wantCode(t, err, errcode.AuthRequired)
	if _, _, err := s.ResetTOTP(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = s.authenticateToken(ctx, pendingToken, "")
	wantCode(t, err, errcode.AuthRequired)
	if err := s.DeleteUser(ctx, "alice"); err != nil {
		t.Fatal(err)
	}
	_, err = s.authenticateToken(ctx, pendingToken, "")
	wantCode(t, err, errcode.AuthRequired)
}

func TestListGetRevokeTokens(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	alice, _ := createActive(t, s, clock, "alice")
	bob, _ := createActive(t, s, clock, "bob")
	empty, err := s.ListTokens(ctx, alice.ID)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("ListTokens with none = %v, %v; want an empty non-nil slice", empty, err)
	}
	first, _, _ := s.CreateToken(ctx, alice.ID, "first", core.ScopeRead, 1)
	second, _, _ := s.CreateToken(ctx, alice.ID, "second", core.ScopeRead, 1)
	bobs, _, _ := s.CreateToken(ctx, bob.ID, "bob", core.ScopeRead, 1)
	list, err := s.ListTokens(ctx, alice.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].ID != second.ID || list[1].ID != first.ID {
		t.Fatalf("ListTokens = %+v, want alice's two tokens newest first", list)
	}
	_, err = s.GetToken(ctx, alice.ID, bobs.ID)
	wantCode(t, err, errcode.NotFound)
	_, err = s.RevokeToken(ctx, alice.ID, bobs.ID)
	wantCode(t, err, errcode.NotFound)
	got, err := s.RevokeToken(ctx, alice.ID, first.ID)
	if err != nil || got.RevokedAt == nil || !got.RevokedAt.Equal(clock.Now()) {
		t.Fatalf("RevokeToken = %+v, %v; want revokedAt %s", got, err, clock.Now())
	}
	at := clock.Now()
	clock.Advance(time.Hour)
	again, err := s.RevokeToken(ctx, alice.ID, first.ID)
	if err != nil || !again.RevokedAt.Equal(at) {
		t.Errorf("second revoke = %+v, %v; want the first revocation time kept", again, err)
	}
	list, _ = s.ListTokens(ctx, alice.ID)
	if len(list) != 2 || list[1].RevokedAt == nil {
		t.Errorf("a revoked token is missing from the list or not marked: %+v", list)
	}
}

func TestPurgeSessions(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	idle, _ := newSession(t, s, u.ID)
	old, _ := newSession(t, s, u.ID)
	for range 15 {
		clock.Advance(50 * time.Minute)
		if _, err := s.authenticateSession(ctx, old, ""); err != nil && clock.Now().Sub(t0) < SessionLifetime {
			t.Fatal(err)
		}
	}
	fresh, _ := newSession(t, s, u.ID)
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for name, id := range map[string]string{"idle": idle, "past its lifetime": old} {
		raw, _ := decodeSessionID(id)
		if count(t, s, "core_sessions", "id_hash = ?", sessionHash(raw)) != 0 {
			t.Errorf("%s session survived the purge", name)
		}
	}
	raw, _ := decodeSessionID(fresh)
	if count(t, s, "core_sessions", "id_hash = ?", sessionHash(raw)) != 1 {
		t.Error("a live session was purged")
	}
}

func TestPurgeTokens(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, _ := createActive(t, s, clock, "alice")
	expired, _, _ := s.CreateToken(ctx, u.ID, "expired", core.ScopeRead, 1)
	recent, _, _ := s.CreateToken(ctx, u.ID, "recently expired", core.ScopeRead, 3)
	revoked, _, _ := s.CreateToken(ctx, u.ID, "revoked", core.ScopeRead, 90)
	live, _, _ := s.CreateToken(ctx, u.ID, "live", core.ScopeRead, 90)
	if _, err := s.RevokeToken(ctx, u.ID, revoked.ID); err != nil {
		t.Fatal(err)
	}
	clock.Advance(31 * day)
	if err := s.Purge(ctx); err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		info TokenInfo
		kept bool
	}{{expired, false}, {recent, true}, {revoked, false}, {live, true}} {
		if got := count(t, s, "core_tokens", "id = ?", tt.info.ID) == 1; got != tt.kept {
			t.Errorf("token %q kept = %v, want %v", tt.info.Name, got, tt.kept)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `s.CreateToken undefined (type *Service has no field or method CreateToken)`, `undefined: TokenPrefix` and `undefined: DefaultTokenDays`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/token.go`:

```go
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// API token rules (spec section 9).
const (
	TokenPrefix      = "dpt_"
	DefaultTokenDays = 90
	MaxTokenDays     = 365
	MaxTokenName     = 64
	LastUsedInterval = time.Minute
	TokenRetention   = 30 * 24 * time.Hour
	tokenSecretSize  = 32
)

func errTokenNotFound() *errcode.Error { return errcode.New(errcode.NotFound, "token not found") }

func secretHash(raw []byte) string {
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}

// parseToken splits dpt_<ULID>_<base32 secret> into the id and the secret's
// bytes. Only the canonical encoding is accepted: the decoder ignores the
// last character's four spare bits, which would otherwise give one secret
// several spellings.
func parseToken(token string) (string, []byte, bool) {
	rest, ok := strings.CutPrefix(token, TokenPrefix)
	if !ok {
		return "", nil, false
	}
	id, sec, ok := strings.Cut(rest, "_")
	if !ok || len(id) != ulid.EncodedSize {
		return "", nil, false
	}
	if _, err := ulid.ParseStrict(id); err != nil {
		return "", nil, false
	}
	raw, err := b32.DecodeString(sec)
	if err != nil || len(raw) != tokenSecretSize || b32.EncodeToString(raw) != sec {
		return "", nil, false
	}
	return id, raw, true
}

const tokenColumns = `t.id, t.name, t.scope, t.created_at, t.expires_at, t.last_used_at, t.revoked_at` //nolint:gosec // G101: column names, not a credential

// scanToken reads tokenColumns, then extra destinations, from row.
func scanToken(row interface{ Scan(...any) error }, extra ...any) (TokenInfo, error) {
	var info TokenInfo
	var created, expires string
	var used, revoked sql.NullString
	dest := append([]any{&info.ID, &info.Name, &info.Scope, &created, &expires, &used, &revoked}, extra...)
	if err := row.Scan(dest...); err != nil {
		return TokenInfo{}, err
	}
	var err error
	if info.CreatedAt, err = parseTime(created); err != nil {
		return TokenInfo{}, err
	}
	if info.ExpiresAt, err = parseTime(expires); err != nil {
		return TokenInfo{}, err
	}
	if info.LastUsedAt, err = parseNullTime(used); err != nil {
		return TokenInfo{}, err
	}
	if info.RevokedAt, err = parseNullTime(revoked); err != nil {
		return TokenInfo{}, err
	}
	return info, nil
}

func parseNullTime(s sql.NullString) (*time.Time, error) {
	if !s.Valid {
		return nil, nil
	}
	t, err := parseTime(s.String)
	if err != nil {
		return nil, err
	}
	return &t, nil
}

// CreateToken creates an API token of userID and returns its description and
// the token string dpt_<id>_<secret>, the only time the secret is shown. days
// is the lifetime, 1 to 365, and 0 means 90. An invalid name, scope or
// lifetime is request.invalid_input; an unknown or inactive user is
// errcode.NotFound.
func (s *Service) CreateToken(ctx context.Context, userID, name string, scope core.Scope, days int) (TokenInfo, string, error) {
	if n := utf8.RuneCountInString(name); n < 1 || n > MaxTokenName {
		return TokenInfo{}, "", invalidField("/name", fmt.Sprintf("must be 1 to %d characters", MaxTokenName))
	}
	if scope != core.ScopeRead && scope != core.ScopeOperate && scope != core.ScopeAdmin {
		return TokenInfo{}, "", invalidField("/scope", "must be read, operate or admin")
	}
	if days == 0 {
		days = DefaultTokenDays
	}
	if days < 1 || days > MaxTokenDays {
		return TokenInfo{}, "", invalidField("/expiresInDays", fmt.Sprintf("must be 1 to %d", MaxTokenDays))
	}
	raw := make([]byte, tokenSecretSize)
	_, _ = rand.Read(raw)
	now := s.now()
	info := TokenInfo{ID: s.newID(), Name: name, Scope: scope, CreatedAt: now,
		ExpiresAt: now.Add(time.Duration(days) * 24 * time.Hour)}
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var state string
		err := tx.QueryRowContext(ctx, `SELECT state FROM core_users WHERE id = ?`, userID).Scan(&state)
		if errors.Is(err, sql.ErrNoRows) || (err == nil && state != StateActive) {
			return errUserNotFound()
		}
		if err != nil {
			return fmt.Errorf("auth: create token: %w", err)
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO core_tokens (id, user_id, name, scope, secret_hash, created_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`, info.ID, userID, info.Name, string(info.Scope), secretHash(raw),
			formatTime(info.CreatedAt), formatTime(info.ExpiresAt))
		if err != nil {
			return fmt.Errorf("auth: create token: %w", err)
		}
		return nil
	})
	if err != nil {
		return TokenInfo{}, "", err
	}
	return info, TokenPrefix + info.ID + "_" + b32.EncodeToString(raw), nil
}

// ListTokens returns the tokens of userID, newest first, revoked and expired
// ones included until they are purged; none is an empty, non-nil slice.
func (s *Service) ListTokens(ctx context.Context, userID string) ([]TokenInfo, error) {
	rows, err := s.db.Read().QueryContext(ctx, `SELECT `+tokenColumns+` FROM core_tokens t WHERE t.user_id = ? ORDER BY t.id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()
	tokens := []TokenInfo{}
	for rows.Next() {
		info, err := scanToken(rows)
		if err != nil {
			return nil, fmt.Errorf("auth: list tokens: %w", err)
		}
		tokens = append(tokens, info)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: list tokens: %w", err)
	}
	return tokens, nil
}

func getToken(ctx context.Context, q querier, userID, id string) (TokenInfo, error) {
	info, err := scanToken(q.QueryRowContext(ctx, `SELECT `+tokenColumns+` FROM core_tokens t WHERE t.id = ? AND t.user_id = ?`, id, userID))
	if errors.Is(err, sql.ErrNoRows) {
		return TokenInfo{}, errTokenNotFound()
	}
	if err != nil {
		return TokenInfo{}, fmt.Errorf("auth: get token: %w", err)
	}
	return info, nil
}

// GetToken returns the token id of userID; a token of another user, or none,
// is errcode.NotFound.
func (s *Service) GetToken(ctx context.Context, userID, id string) (TokenInfo, error) {
	return getToken(ctx, s.db.Read(), userID, id)
}

// RevokeToken revokes the token id of userID and returns it. Revoking a
// revoked token keeps its first revocation time; a token of another user, or
// none, is errcode.NotFound.
func (s *Service) RevokeToken(ctx context.Context, userID, id string) (TokenInfo, error) {
	var info TokenInfo
	err := s.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		if info, err = getToken(ctx, tx, userID, id); err != nil {
			return err
		}
		if info.RevokedAt != nil {
			return nil
		}
		now := s.now()
		if _, err := tx.ExecContext(ctx, `UPDATE core_tokens SET revoked_at = ? WHERE id = ?`, formatTime(now), id); err != nil {
			return fmt.Errorf("auth: revoke token: %w", err)
		}
		info.RevokedAt = &now
		return nil
	})
	return info, err
}

// authenticateToken resolves a bearer token. A malformed, unknown, revoked or
// expired token, a wrong secret, or a user that is not active is
// auth.required. It moves last_used_at forward when it is unset or a minute
// or more old.
func (s *Service) authenticateToken(ctx context.Context, token, source string) (Identity, error) {
	id, raw, ok := parseToken(token)
	if !ok {
		return Identity{}, errAuthRequired()
	}
	var hash, createdText string
	var u User
	info, err := scanToken(s.db.Read().QueryRowContext(ctx, `SELECT `+tokenColumns+`, t.secret_hash,
		u.id, u.username, u.role, u.state, u.created_at FROM core_tokens t JOIN core_users u ON u.id = t.user_id
		WHERE t.id = ?`, id), &hash, &u.ID, &u.Username, &u.Role, &u.State, &createdText)
	if errors.Is(err, sql.ErrNoRows) {
		return Identity{}, errAuthRequired()
	}
	if err != nil {
		return Identity{}, fmt.Errorf("auth: load token: %w", err)
	}
	if u.CreatedAt, err = parseTime(createdText); err != nil {
		return Identity{}, err
	}
	now := s.now()
	if subtle.ConstantTimeCompare([]byte(secretHash(raw)), []byte(hash)) != 1 ||
		info.RevokedAt != nil || !now.Before(info.ExpiresAt) || u.State != StateActive {
		return Identity{}, errAuthRequired()
	}
	if info.LastUsedAt == nil || now.Sub(*info.LastUsedAt) >= LastUsedInterval {
		err := s.db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, `UPDATE core_tokens SET last_used_at = ? WHERE id = ?`, formatTime(now), id)
			return err
		})
		if err != nil {
			return Identity{}, fmt.Errorf("auth: touch token: %w", err)
		}
		info.LastUsedAt = &now
	}
	return Identity{
		Principal: core.Principal{Actor: "token:" + id, UserID: u.ID, TokenID: id, Scope: info.Scope, Source: source},
		User:      u, ExpiresAt: info.ExpiresAt, Token: &info,
	}, nil
}

// Purge deletes sessions past their idle or absolute limit and tokens 30
// days after they expired or were revoked (spec section 9). The server runs
// it at start and every 10 minutes.
func (s *Service) Purge(ctx context.Context) error {
	now := s.now()
	return s.db.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_sessions WHERE expires_at <= ? OR last_seen_at <= ?`,
			formatTime(now), formatTime(now.Add(-SessionIdle))); err != nil {
			return fmt.Errorf("auth: purge sessions: %w", err)
		}
		cutoff := formatTime(now.Add(-TokenRetention))
		if _, err := tx.ExecContext(ctx, `DELETE FROM core_tokens WHERE expires_at <= ? OR revoked_at <= ?`,
			cutoff, cutoff); err != nil {
			return fmt.Errorf("auth: purge tokens: %w", err)
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/token.go internal/controller/auth/token_test.go
git commit -m "feat(auth): add scoped API tokens and purge"
```


### Task 7: Login, logout and authentication

**Files:**
- Create: `internal/controller/auth/login.go`
- Test: `internal/controller/auth/login_test.go`

**Interfaces:**
- Consumes: `core.AuditEntry`, `core.Audit.Record`, `core.AuditQuery`, `coretest.MemAudit.List` (foundation); Task 1 (`checkUsername`, `verify`, `verifyDummy`, the `compute` hook), Task 2 (`matchTOTP`, `TOTPCode`), Task 3 (`blocked`, `fail`, `RetryAfterError`, the limit constants), Task 4 (`Service`, `loadUser`, `totpAD`, `newID`, `now`, the test helpers), Task 5 (`createSession`, `authenticateSession`, `endSession`, `CSRFToken`, `errAuthRequired`), Task 6 (`authenticateToken`, `CreateToken`).
- Produces: Contracts › Task 7 (`AuditLoginAction`, `AuditLogoutAction`, `ActorAnonymous`, `LoginRequest`, `LoginResult`, `Credentials`, `Login`, `Logout`, `Authenticate`) — the surface P9 codes against.

**Items:** 11, 12, 13, 14

**Depends on:** Tasks 3, 5 and 6

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The password is verified outside any transaction (it is slow); the code check, the step update and the session insert then run in one immediate write transaction that re-reads the user, so two logins racing with one code cannot both succeed (`TestConcurrentLoginsWithOneCode`). Every failure returns the same error and costs one argon2id hash, which the test asserts per case. The audit row is written after the commit, never inside it, with a context that ignores cancellation.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/auth/login_test.go`:

```go
package auth

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func code(t *testing.T, secret string, clock *coretest.FakeClock) string {
	t.Helper()
	c, err := TOTPCode(secret, clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// wrongCode returns a six-digit code that is not right.
func wrongCode(right string) string {
	if right == "000000" {
		return "111111"
	}
	return "000000"
}

// countHashes makes s count the argon2id computations it runs.
func countHashes(s *Service) *atomic.Int32 {
	var n atomic.Int32
	inner := s.hasher.compute
	s.hasher.compute = func(password, salt []byte, p Params) []byte {
		n.Add(1)
		return inner(password, salt, p)
	}
	return &n
}

func TestLogin(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	c := code(t, secret, clock)
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: c, Source: "10.0.0.1", UserAgent: "ua"})
	if err != nil {
		t.Fatal(err)
	}
	if res.User.ID != u.ID || res.User.State != StateActive || !res.ExpiresAt.Equal(clock.Now().Add(SessionIdle)) {
		t.Errorf("result = %+v, want alice with a session ending %s", res, clock.Now().Add(SessionIdle))
	}
	if res.CSRFToken == "" || res.CSRFToken != s.CSRFToken(res.SessionID) {
		t.Errorf("CSRF token %q is not the session's", res.CSRFToken)
	}
	id, err := s.Authenticate(ctx, Credentials{Session: res.SessionID, Source: "10.0.0.1"})
	if err != nil || id.Principal.UserID != u.ID {
		t.Fatalf("Authenticate with the new session = %+v, %v", id, err)
	}
	if count(t, s, "core_users", "id = ? AND totp_last_step = ?", u.ID, totpStep(clock.Now())) != 1 {
		t.Error("login did not store the code's step")
	}
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(entries) != 1 {
		t.Fatalf("%d audit entries, want 1", len(entries))
	}
	e := entries[0]
	if e.Actor != "user:"+u.ID || e.Action != AuditLoginAction || e.Version != 1 || e.Phase != "login" ||
		e.Result != "ok" || e.ErrorCode != "" || e.Source != "10.0.0.1" || !e.At.Equal(clock.Now()) ||
		string(e.Input) != `{"username":"alice"}` || e.ID == "" {
		t.Errorf("audit entry = %+v", e)
	}
}

func TestLoginFailuresAreIdentical(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	alice, secret := createActive(t, s, clock, "alice")
	bob, bobSecret, err := s.CreateUser(ctx, "bob", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	right := code(t, secret, clock)
	hashes := countHashes(s)
	cases := []struct {
		name  string
		req   LoginRequest
		actor string
	}{
		{"unknown user", LoginRequest{Username: "nobody", Password: testPassword, Code: right}, ActorAnonymous},
		{"wrong password", LoginRequest{Username: "alice", Password: "wrong horse battery", Code: right}, "user:" + alice.ID},
		{"wrong code", LoginRequest{Username: "alice", Password: testPassword, Code: wrongCode(right)}, "user:" + alice.ID},
		{"pending user", LoginRequest{Username: "bob", Password: testPassword, Code: code(t, bobSecret, clock)}, "user:" + bob.ID},
	}
	var first *errcode.Error
	for _, tt := range cases {
		tt.req.Source = "10.0.0.1"
		hashes.Store(0)
		_, err := s.Login(ctx, tt.req)
		var e *errcode.Error
		if !errors.As(err, &e) || e.Code != errcode.InvalidCredentials {
			t.Fatalf("%s: error = %v, want %s", tt.name, err, errcode.InvalidCredentials)
		}
		if first == nil {
			first = e
		} else if e.Detail != first.Detail || e.Error() != first.Error() || len(e.Fields) != 0 {
			t.Errorf("%s: error %q differs from %q", tt.name, e.Error(), first.Error())
		}
		if n := hashes.Load(); n != 1 {
			t.Errorf("%s: ran %d argon2id hashes, want 1", tt.name, n)
		}
		entries, _, _ := audit.List(ctx, core.AuditQuery{Limit: 1})
		got := entries[0]
		if got.Actor != tt.actor || got.Result != "error" || got.ErrorCode != string(errcode.InvalidCredentials) ||
			got.Phase != "login" || string(got.Input) != `{"username":"`+tt.req.Username+`"}` {
			t.Errorf("%s: audit entry = %+v, want actor %s", tt.name, got, tt.actor)
		}
		for _, secretValue := range []string{tt.req.Password, tt.req.Code} {
			if strings.Contains(string(got.Input), secretValue) || strings.Contains(e.Error(), secretValue) {
				t.Errorf("%s: audit input or error carries a password or code", tt.name)
			}
		}
	}

	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: right, Source: "10.0.0.1"}); err != nil {
		t.Fatal(err)
	}
	_, err = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: right, Source: "10.0.0.1"})
	if e := errcode.From(err); e.Code != errcode.InvalidCredentials || e.Error() != first.Error() {
		t.Errorf("replayed code: error = %v, want the same %s", err, errcode.InvalidCredentials)
	}

	before, _, _ := audit.List(ctx, core.AuditQuery{})
	hashes.Store(0)
	_, err = s.Login(ctx, LoginRequest{Username: "Not A Name", Password: testPassword, Code: right, Source: "10.0.0.1"})
	if e := errcode.From(err); e.Code != errcode.InvalidCredentials || e.Error() != first.Error() {
		t.Errorf("invalid username: error = %v, want the same %s", err, errcode.InvalidCredentials)
	}
	after, _, _ := audit.List(ctx, core.AuditQuery{})
	if len(after) != len(before) || hashes.Load() != 0 {
		t.Errorf("an invalid username was evaluated: %d audit entries added, %d hashes", len(after)-len(before), hashes.Load())
	}
}

func TestLoginRateLimit(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for range UsernameFailureLimit {
		_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: "wrong horse battery", Code: "000000", Source: "10.0.0.1"})
		wantCode(t, err, errcode.InvalidCredentials)
	}
	before, _, _ := audit.List(ctx, core.AuditQuery{})
	hashes := countHashes(s)
	_, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"})
	wantCode(t, err, errcode.RateLimited)
	var ra *RetryAfterError
	if !errors.As(err, &ra) || ra.Seconds() != int(LockDuration/time.Second) {
		t.Fatalf("error %v carries no Retry-After of %s", err, LockDuration)
	}
	after, _, _ := audit.List(ctx, core.AuditQuery{})
	if hashes.Load() != 0 || len(after) != len(before) {
		t.Errorf("a blocked attempt was evaluated: %d hashes, %d audit entries added", hashes.Load(), len(after)-len(before))
	}
	clock.Advance(LockDuration)
	if _, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.2"}); err != nil {
		t.Errorf("login after the lock ended: %v", err)
	}
}

func TestConcurrentLoginsWithOneCode(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	_, secret := createActive(t, s, clock, "alice")
	for round := range 5 {
		c := code(t, secret, clock)
		start := make(chan struct{})
		errs := make([]error, 2)
		var wg sync.WaitGroup
		for i := range errs {
			wg.Go(func() {
				<-start
				_, errs[i] = s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: c, Source: "10.0.0.1"})
			})
		}
		close(start)
		wg.Wait()
		ok := 0
		for _, err := range errs {
			if err == nil {
				ok++
			} else {
				wantCode(t, err, errcode.InvalidCredentials)
			}
		}
		if ok != 1 {
			t.Fatalf("round %d: %d of two concurrent logins with one code succeeded, want exactly 1", round, ok)
		}
		clock.Advance(TOTPPeriod)
	}
}

func TestLogout(t *testing.T) {
	s, clock, audit := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Logout(ctx, res.SessionID, "10.0.0.3"); err != nil {
		t.Fatal(err)
	}
	_, err = s.Authenticate(ctx, Credentials{Session: res.SessionID})
	wantCode(t, err, errcode.AuthRequired)
	entries, _, _ := audit.List(ctx, core.AuditQuery{})
	e := entries[0]
	if len(entries) != 2 || e.Actor != "user:"+u.ID || e.Action != AuditLogoutAction || e.Phase != "logout" ||
		e.Result != "ok" || e.Source != "10.0.0.3" || string(e.Input) != `{}` {
		t.Errorf("logout audit entry = %+v (of %d)", e, len(entries))
	}
	if err := s.Logout(ctx, res.SessionID, "10.0.0.3"); err != nil {
		t.Errorf("second logout = %v, want nil", err)
	}
	if again, _, _ := audit.List(ctx, core.AuditQuery{}); len(again) != 2 {
		t.Errorf("logging out an ended session was audited")
	}
}

func TestAuthenticateCredentials(t *testing.T) {
	s, clock, _ := newTestService(t)
	ctx := context.Background()
	u, secret := createActive(t, s, clock, "alice")
	res, err := s.Login(ctx, LoginRequest{Username: "alice", Password: testPassword, Code: code(t, secret, clock), Source: "10.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	info, token, err := s.CreateToken(ctx, u.ID, "ci", core.ScopeRead, 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name    string
		c       Credentials
		session bool
		ok      bool
	}{
		{"session cookie", Credentials{Session: res.SessionID}, true, true},
		{"bearer token", Credentials{Authorization: []string{"Bearer " + token}}, false, true},
		{"lower-case scheme", Credentials{Authorization: []string{"bearer " + token}}, false, true},
		{"bearer wins over a cookie", Credentials{Authorization: []string{"Bearer " + token}, Session: res.SessionID}, false, true},
		{"invalid bearer never falls back to the cookie", Credentials{Authorization: []string{"Bearer dpt_x"}, Session: res.SessionID}, false, false},
		{"other scheme", Credentials{Authorization: []string{"Basic YWxpY2U6eA=="}, Session: res.SessionID}, false, false},
		{"empty header", Credentials{Authorization: []string{""}, Session: res.SessionID}, false, false},
		{"two headers", Credentials{Authorization: []string{"Bearer " + token, "Bearer " + token}}, false, false},
		{"present but no value", Credentials{Authorization: []string{}, Session: res.SessionID}, false, false},
		{"nothing", Credentials{}, false, false},
	} {
		tt.c.Source = "10.0.0.9"
		id, err := s.Authenticate(ctx, tt.c)
		if !tt.ok {
			if e := errcode.From(err); e.Code != errcode.AuthRequired {
				t.Errorf("%s: error = %v, want %s", tt.name, err, errcode.AuthRequired)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: %v", tt.name, err)
			continue
		}
		if id.FromSession != tt.session || id.Principal.Source != "10.0.0.9" {
			t.Errorf("%s: identity = %+v, want from session %v", tt.name, id, tt.session)
		}
		if !tt.session && id.Principal.TokenID != info.ID {
			t.Errorf("%s: principal %+v is not the token's", tt.name, id.Principal)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 ./internal/controller/auth/`
Expected: FAIL — the build reports `s.Login undefined (type *Service has no field or method Login)`, `undefined: LoginRequest` and `s.Authenticate undefined (type *Service has no field or method Authenticate)`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/auth/login.go`:

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
func (s *Service) Login(ctx context.Context, r LoginRequest) (LoginResult, error) {
	if checkUsername(r.Username) != nil {
		return LoginResult{}, errInvalidCredentials()
	}
	if d := s.limiter.blocked(r.Username, r.Source); d > 0 {
		return LoginResult{}, errcode.Wrap(errcode.RateLimited, &RetryAfterError{After: d},
			"too many failed login attempts; try again later")
	}
	res, userID, err := s.attempt(ctx, r)
	if err != nil {
		if errcode.From(err).Code != errcode.InvalidCredentials {
			return LoginResult{}, err
		}
		s.limiter.fail(r.Username, r.Source)
		if aerr := s.recordLogin(ctx, r, userID, errcode.InvalidCredentials); aerr != nil {
			return LoginResult{}, aerr
		}
		return LoginResult{}, err
	}
	if err := s.recordLogin(ctx, r, userID, ""); err != nil {
		return LoginResult{}, err
	}
	return res, nil
}

// attempt evaluates a login and returns the matched user's id ("" when none
// matched). An unknown username still costs one argon2id hash.
func (s *Service) attempt(ctx context.Context, r LoginRequest) (LoginResult, string, error) {
	rec, err := loadUser(ctx, s.db.Read(), r.Username)
	if err != nil {
		if errcode.From(err).Code != errcode.NotFound {
			return LoginResult{}, "", err
		}
		if err := s.hasher.verifyDummy(ctx, r.Password); err != nil {
			return LoginResult{}, "", err
		}
		return LoginResult{}, "", errInvalidCredentials()
	}
	ok, err := s.hasher.verify(ctx, rec.passwordHash, r.Password)
	if err != nil {
		return LoginResult{}, rec.ID, err
	}
	if !ok || rec.State != StateActive {
		return LoginResult{}, rec.ID, errInvalidCredentials()
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
	return res, rec.ID, err
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

Run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/auth/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/auth/login.go internal/controller/auth/login_test.go
git commit -m "feat(auth): log in, log out and authenticate"
```


### Task 8: Listen values and endpoints

**Files:**
- Create: `internal/controller/listen/spec.go`
- Test: `internal/controller/listen/spec_test.go`

**Interfaces:**
- Consumes: nothing beyond the standard library.
- Produces: Contracts › Task 8 (`LoopbackPort`, `TailscalePort`, `Kind` and its constants, `Spec`, `ParseSpecs`, `HasTailscale`, `Endpoint`, `Endpoint.URL`, `Endpoints`) and the package doc comment; Tasks 10, 14 and 15 build on `Endpoint`, Task 15 on `ParseSpecs` and `Endpoints`, and plan 4's `serve` passes its `--listen` values to `ParseSpecs`.

**Items:** 16

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The refusal of plain HTTP on a non-loopback address is the R20 boundary ("other binds need explicit configuration; plain HTTP only on 127.0.0.1"), so the task is risk 3. Conflicts are checked on the unmerged expansion of each value (`expand`), because `Endpoints` merges one address listed twice.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/spec_test.go`:

```go
package listen

import (
	"net/netip"
	"reflect"
	"strings"
	"testing"
)

func TestParseSpecsDefaults(t *testing.T) {
	got, err := ParseSpecs(nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSpecs(nil) = %v, want %v", got, want)
	}
}

func TestParseSpecsValues(t *testing.T) {
	got, err := ParseSpecs([]string{
		"tailscale",
		"https://192.168.1.5:9443",
		"https://[2001:db8::5]:443",
		"http://127.0.0.2:8081",
		"http://[::1]:9090",
		"https://[::ffff:10.0.0.1]:8443",
		"tailscale",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := []Spec{
		{Kind: KindTailscale},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("192.168.1.5:9443")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("[2001:db8::5]:443")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("127.0.0.2:8081")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("[::1]:9090")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("10.0.0.1:8443")},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseSpecs = %v, want %v", got, want)
	}
	var s []string
	for _, sp := range got {
		s = append(s, sp.String())
	}
	if joined := strings.Join(s, " "); joined != "tailscale https://192.168.1.5:9443 https://[2001:db8::5]:443 http://127.0.0.2:8081 http://[::1]:9090 https://10.0.0.1:8443" {
		t.Fatalf("String() = %q", joined)
	}
}

func TestParseSpecsRefuses(t *testing.T) {
	cases := []struct {
		value, msg string
	}{
		{"http://192.168.1.5:8080", "plain HTTP is allowed only on a loopback address"},
		{"http://[2001:db8::1]:8080", "plain HTTP is allowed only on a loopback address"},
		{"http://localhost:8080", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://example.com:443", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5:8443/", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"tcp://127.0.0.1:80", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"", "is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>"},
		{"https://192.168.1.5:0", "needs a port between 1 and 65535"},
		{"https://0.0.0.0:8443", "must be a specific address"},
		{"https://[::]:8443", "must be a specific address"},
		{"https://224.0.0.1:8443", "must be a specific address"},
		{"https://[fe80::1%eth0]:8443", "must not carry a zone"},
	}
	for _, c := range cases {
		_, err := ParseSpecs([]string{c.value})
		if err == nil || !strings.Contains(err.Error(), c.msg) {
			t.Errorf("ParseSpecs(%q) error = %v, want it to contain %q", c.value, err, c.msg)
		}
	}
}

func TestParseSpecsRefusesSchemeConflict(t *testing.T) {
	for _, values := range [][]string{
		{"loopback", "https://127.0.0.1:8080"},
		{"http://127.0.0.1:9000", "https://127.0.0.1:9000"},
	} {
		_, err := ParseSpecs(values)
		if err == nil || !strings.Contains(err.Error(), "is listed as both HTTP and HTTPS") {
			t.Errorf("ParseSpecs(%q) error = %v, want a scheme conflict", values, err)
		}
	}
}

func TestEndpoints(t *testing.T) {
	specs := []Spec{
		{Kind: KindTailscale},
		{Kind: KindLoopback},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("100.64.0.7:8443")},
		{Kind: KindHTTPS, Addr: netip.MustParseAddrPort("192.168.1.5:9443")},
		{Kind: KindHTTP, Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
	}
	ts := []netip.Addr{netip.MustParseAddr("fd7a:115c:a1e0::7"), netip.MustParseAddr("100.64.0.7")}
	got := Endpoints(specs, ts)
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("100.64.0.7:8443"), TLS: true, FreeBind: true},
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("192.168.1.5:9443"), TLS: true},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
		{Addr: netip.MustParseAddrPort("[fd7a:115c:a1e0::7]:8443"), TLS: true, FreeBind: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Endpoints = %v,\nwant %v", got, want)
	}
	if u := got[4].URL(); u != "https://[fd7a:115c:a1e0::7]:8443" {
		t.Fatalf("URL() = %q", u)
	}
	if u := got[1].URL(); u != "http://127.0.0.1:8080" {
		t.Fatalf("URL() = %q", u)
	}
}

func TestEndpointsWithoutTailscaleAddresses(t *testing.T) {
	got := Endpoints([]Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}, nil)
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Endpoints = %v, want %v", got, want)
	}
	if HasTailscale([]Spec{{Kind: KindLoopback}}) || !HasTailscale([]Spec{{Kind: KindTailscale}}) {
		t.Fatal("HasTailscale is wrong")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestParseSpecs|TestEndpoints' ./internal/controller/listen/`
Expected: FAIL with `spec_test.go:11:14: undefined: ParseSpecs` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/spec.go`:

```go
// Package listen opens the controller's web and API listeners (spec section
// 13, ADR 0006): plain HTTP on loopback, HTTPS on the Tailscale addresses and
// on explicit binds, with a certificate from the controller's web authority
// (ADR 0010).
package listen

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Default ports of the loopback and tailscale listeners.
const (
	LoopbackPort  = 8080
	TailscalePort = 8443
)

// Kind is the form of one --listen value.
type Kind uint8

// The --listen forms.
const (
	KindLoopback  Kind = iota + 1 // plain HTTP on 127.0.0.1:8080 and [::1]:8080
	KindTailscale                 // HTTPS on port 8443 of each Tailscale address
	KindHTTPS                     // https://<ip>:<port>
	KindHTTP                      // http://<loopback ip>:<port>
)

// Spec is one parsed --listen value. Addr is set for KindHTTPS and KindHTTP.
type Spec struct {
	Kind Kind
	Addr netip.AddrPort
}

// String returns the value as it is written on the command line.
func (s Spec) String() string {
	switch s.Kind {
	case KindLoopback:
		return "loopback"
	case KindTailscale:
		return "tailscale"
	case KindHTTPS:
		return "https://" + s.Addr.String()
	case KindHTTP:
		return "http://" + s.Addr.String()
	}
	return fmt.Sprintf("Kind(%d)", s.Kind)
}

// ParseSpecs parses the --listen values; none means the defaults, loopback
// and tailscale. Repeated values count once. It refuses plain HTTP on an
// address that is not loopback, and one address listed as both HTTP and
// HTTPS.
func ParseSpecs(values []string) ([]Spec, error) {
	if len(values) == 0 {
		return []Spec{{Kind: KindLoopback}, {Kind: KindTailscale}}, nil
	}
	var out []Spec
	for _, v := range values {
		s, err := parseSpec(v)
		if err != nil {
			return nil, err
		}
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	scheme := map[netip.AddrPort]bool{}
	for _, s := range out {
		for _, e := range expand(s, nil) {
			if tls, seen := scheme[e.Addr]; seen && tls != e.TLS {
				return nil, fmt.Errorf("listen: %s is listed as both HTTP and HTTPS", e.Addr)
			}
			scheme[e.Addr] = e.TLS
		}
	}
	return out, nil
}

func parseSpec(v string) (Spec, error) {
	switch v {
	case "loopback":
		return Spec{Kind: KindLoopback}, nil
	case "tailscale":
		return Spec{Kind: KindTailscale}, nil
	}
	kind := KindHTTPS
	rest, ok := strings.CutPrefix(v, "https://")
	if !ok {
		kind = KindHTTP
		if rest, ok = strings.CutPrefix(v, "http://"); !ok {
			return Spec{}, badValue(v)
		}
	}
	ap, err := netip.ParseAddrPort(rest)
	if err != nil {
		return Spec{}, badValue(v)
	}
	addr := ap.Addr()
	switch {
	case addr.Zone() != "":
		return Spec{}, fmt.Errorf("listen: %q: the address must not carry a zone", v)
	case ap.Port() == 0:
		return Spec{}, fmt.Errorf("listen: %q needs a port between 1 and 65535", v)
	}
	addr = addr.Unmap()
	if addr.IsUnspecified() || addr.IsMulticast() {
		return Spec{}, fmt.Errorf("listen: %q: the address must be a specific address of this host", v)
	}
	if kind == KindHTTP && !addr.IsLoopback() {
		return Spec{}, fmt.Errorf("listen: %q: plain HTTP is allowed only on a loopback address; use https://%s", v, ap)
	}
	return Spec{Kind: kind, Addr: netip.AddrPortFrom(addr, ap.Port())}, nil
}

func badValue(v string) error {
	return fmt.Errorf("listen: %q is not loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>", v)
}

// HasTailscale reports whether specs include the tailscale listener.
func HasTailscale(specs []Spec) bool {
	return slices.ContainsFunc(specs, func(s Spec) bool { return s.Kind == KindTailscale })
}

// Endpoint is one address the controller listens on.
type Endpoint struct {
	Addr netip.AddrPort
	TLS  bool
	// FreeBind binds with IP_FREEBIND or IPV6_FREEBIND, so a Tailscale address
	// can be bound before tailscaled has configured it.
	FreeBind bool
	// Optional marks the IPv6 loopback endpoint: a host without that address
	// skips it with one log line instead of failing.
	Optional bool
}

// URL returns the endpoint as a base URL, such as https://100.64.0.1:8443.
func (e Endpoint) URL() string {
	if e.TLS {
		return "https://" + e.Addr.String()
	}
	return "http://" + e.Addr.String()
}

var (
	loopback4 = netip.AddrPortFrom(netip.MustParseAddr("127.0.0.1"), LoopbackPort)
	loopback6 = netip.AddrPortFrom(netip.IPv6Loopback(), LoopbackPort)
)

// Endpoints expands specs into endpoints, with tailscale as the current
// Tailscale addresses. One address listed twice is one endpoint; it keeps
// FreeBind if either listing has it. The result is sorted by address.
func Endpoints(specs []Spec, tailscale []netip.Addr) []Endpoint {
	byAddr := map[netip.AddrPort]Endpoint{}
	add := func(e Endpoint) {
		if prev, ok := byAddr[e.Addr]; ok {
			e.FreeBind = e.FreeBind || prev.FreeBind
			e.Optional = e.Optional && prev.Optional
		}
		byAddr[e.Addr] = e
	}
	for _, s := range specs {
		for _, e := range expand(s, tailscale) {
			add(e)
		}
	}
	out := make([]Endpoint, 0, len(byAddr))
	for _, e := range byAddr {
		out = append(out, e)
	}
	slices.SortFunc(out, func(a, b Endpoint) int { return a.Addr.Compare(b.Addr) })
	return out
}

func expand(s Spec, tailscale []netip.Addr) []Endpoint {
	switch s.Kind {
	case KindLoopback:
		return []Endpoint{{Addr: loopback4}, {Addr: loopback6, Optional: true}}
	case KindTailscale:
		out := make([]Endpoint, 0, len(tailscale))
		for _, a := range tailscale {
			out = append(out, Endpoint{Addr: netip.AddrPortFrom(a, TailscalePort), TLS: true, FreeBind: true})
		}
		return out
	case KindHTTPS:
		return []Endpoint{{Addr: s.Addr, TLS: true}}
	case KindHTTP:
		return []Endpoint{{Addr: s.Addr}}
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestParseSpecs|TestEndpoints' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestParseSpecsDefaults`, `TestParseSpecsValues`, `TestParseSpecsRefuses`, `TestParseSpecsRefusesSchemeConflict`, `TestEndpoints` and `TestEndpointsWithoutTailscaleAddresses` PASS; every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/spec.go internal/controller/listen/spec_test.go
git commit -m "feat(listen): parse listen values into endpoints"
```


### Task 9: Tailscale addresses

**Files:**
- Create: `internal/controller/listen/tailscale.go`
- Test: `internal/controller/listen/tailscale_test.go`

**Interfaces:**
- Consumes: `store.DB` (`Read`, `Write`, `Writer`), `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrateOptions`, `store.SideController` (foundation); the `core_meta` table of `0001_core.sql`; package `listen` from Task 8.
- Produces: Contracts › Task 9 (`TailscaleInterface`, `TailscaleAddrs`, `InterfaceAddrs`, `SystemInterfaceAddrs`, `LoadTailscaleAddrs`, `SaveTailscaleAddrs`, the `tailscale_addrs` stored format) and the test helpers `openControllerDB`, `openControllerDBAt`, `addrs`, used by Tasks 12, 13 and 15.

**Items:** 16

**Depends on:** Task 8

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The `core_meta` value is a stored format read at every start. `SystemInterfaceAddrs` finds the interface by listing `net.Interfaces()`, because `net.InterfaceByName`'s "no such interface" error is unexported and cannot be told apart from a real failure.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/tailscale_test.go`:

```go
package listen

import (
	"context"
	"net/netip"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// openControllerDB returns a temporary controller database with the core
// migrations applied.
func openControllerDB(t *testing.T) *store.DB {
	t.Helper()
	return openControllerDBAt(t, filepath.Join(t.TempDir(), "controller.db"))
}

// openControllerDBAt opens the controller database at path, applying the
// core migrations when they are pending.
func openControllerDBAt(t *testing.T, path string) *store.DB {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	opts := store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test",
		BackupRoot: filepath.Join(filepath.Dir(path), "backups"), Keep: 5, Now: time.Now(),
	}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	return db
}

func addrs(s ...string) []netip.Addr {
	out := make([]netip.Addr, 0, len(s))
	for _, a := range s {
		out = append(out, netip.MustParseAddr(a))
	}
	return out
}

func TestTailscaleAddrs(t *testing.T) {
	got := TailscaleAddrs(addrs(
		"fd7a:115c:a1e0::9", "100.127.255.255", "100.64.0.1", "100.128.0.1", "100.63.255.255",
		"fe80::1", "fd7a:115c:a1e1::1", "192.168.1.5", "::ffff:100.100.1.1", "100.64.0.1",
	))
	want := addrs("100.64.0.1", "100.100.1.1", "100.127.255.255", "fd7a:115c:a1e0::9")
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("TailscaleAddrs = %v, want %v", got, want)
	}
	if got := TailscaleAddrs(addrs("192.168.1.5")); got != nil {
		t.Fatalf("TailscaleAddrs without a match = %v, want nil", got)
	}
}

func TestSystemInterfaceAddrs(t *testing.T) {
	got, present, err := SystemInterfaceAddrs("lo")
	if err != nil {
		t.Fatal(err)
	}
	if !present {
		t.Skip("this host has no interface lo")
	}
	if !slices.Contains(got, netip.MustParseAddr("127.0.0.1")) {
		t.Fatalf("lo addresses %v lack 127.0.0.1", got)
	}
	got, present, err = SystemInterfaceAddrs("dp-absent-if0")
	if err != nil || present || got != nil {
		t.Fatalf("absent interface = %v, %v, %v; want nil, false, nil", got, present, err)
	}
}

func TestTailscaleAddrsRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	got, err := LoadTailscaleAddrs(ctx, db)
	if err != nil || got != nil {
		t.Fatalf("Load on a new database = %v, %v; want nil, nil", got, err)
	}
	want := addrs("100.64.0.1", "fd7a:115c:a1e0::1")
	if err := SaveTailscaleAddrs(ctx, db, want); err != nil {
		t.Fatal(err)
	}
	var stored string
	if err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'tailscale_addrs'`).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if stored != `["100.64.0.1","fd7a:115c:a1e0::1"]` {
		t.Fatalf("stored value = %s", stored)
	}
	if got, err = LoadTailscaleAddrs(ctx, db); err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("Load = %v, %v; want %v", got, err, want)
	}
	if err := SaveTailscaleAddrs(ctx, db, nil); err != nil {
		t.Fatal(err)
	}
	if got, err = LoadTailscaleAddrs(ctx, db); err != nil || got != nil {
		t.Fatalf("Load after saving none = %v, %v; want nil, nil", got, err)
	}
}

func TestLoadTailscaleAddrsRefusesBadValue(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	for _, v := range []string{`not json`, `["100.64.0.1","bogus"]`} {
		if _, err := db.Writer().ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES ('tailscale_addrs', ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, v); err != nil {
			t.Fatal(err)
		}
		_, err := LoadTailscaleAddrs(ctx, db)
		if err == nil || !strings.Contains(err.Error(), "core_meta tailscale_addrs") {
			t.Errorf("Load of %s: error = %v, want a core_meta tailscale_addrs error", v, err)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestTailscaleAddrs|TestSystemInterfaceAddrs|TestLoadTailscaleAddrs' ./internal/controller/listen/`
Expected: FAIL with `undefined: TailscaleAddrs` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/tailscale.go`:

```go
package listen

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// TailscaleInterface is the interface tailscaled creates.
const TailscaleInterface = "tailscale0"

var (
	tailscale4 = netip.MustParsePrefix("100.64.0.0/10")
	tailscale6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// TailscaleAddrs returns the addresses inside 100.64.0.0/10 or
// fd7a:115c:a1e0::/48, sorted and without repeats, or nil when there are none.
func TailscaleAddrs(list []netip.Addr) []netip.Addr {
	var out []netip.Addr
	for _, a := range list {
		a = a.Unmap()
		if (tailscale4.Contains(a) || tailscale6.Contains(a)) && !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return out
}

// InterfaceAddrs lists the addresses of the named interface; present is
// false when no interface has that name.
type InterfaceAddrs func(name string) (addrs []netip.Addr, present bool, err error)

// SystemInterfaceAddrs is the production InterfaceAddrs.
func SystemInterfaceAddrs(name string) ([]netip.Addr, bool, error) {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil, false, fmt.Errorf("listen: list interfaces: %w", err)
	}
	i := slices.IndexFunc(ifs, func(ifi net.Interface) bool { return ifi.Name == name })
	if i < 0 {
		return nil, false, nil
	}
	list, err := ifs[i].Addrs()
	if err != nil {
		return nil, true, fmt.Errorf("listen: addresses of %s: %w", name, err)
	}
	var out []netip.Addr
	for _, a := range list {
		ipnet, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		if addr, ok := netip.AddrFromSlice(ipnet.IP); ok {
			out = append(out, addr.Unmap())
		}
	}
	return out, true, nil
}

const metaTailscaleAddrs = "tailscale_addrs"

// LoadTailscaleAddrs returns the Tailscale addresses saved in core_meta, or
// nil when none are saved.
func LoadTailscaleAddrs(ctx context.Context, db *store.DB) ([]netip.Addr, error) {
	var raw string
	err := db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = ?`, metaTailscaleAddrs).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("listen: read core_meta %s: %w", metaTailscaleAddrs, err)
	}
	var list []netip.Addr
	if err := json.Unmarshal([]byte(raw), &list); err != nil {
		return nil, fmt.Errorf("listen: core_meta %s: %w", metaTailscaleAddrs, err)
	}
	if len(list) == 0 {
		return nil, nil
	}
	return list, nil
}

// SaveTailscaleAddrs saves addrs in core_meta as a JSON array of address
// strings, such as ["100.64.0.1","fd7a:115c:a1e0::1"].
func SaveTailscaleAddrs(ctx context.Context, db *store.DB, addrs []netip.Addr) error {
	if addrs == nil {
		addrs = []netip.Addr{}
	}
	raw, err := json.Marshal(addrs)
	if err != nil {
		return fmt.Errorf("listen: encode %s: %w", metaTailscaleAddrs, err)
	}
	return db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx,
			`INSERT INTO core_meta (key, value) VALUES (?, ?)
			 ON CONFLICT (key) DO UPDATE SET value = excluded.value`, metaTailscaleAddrs, string(raw))
		if err != nil {
			return fmt.Errorf("listen: save core_meta %s: %w", metaTailscaleAddrs, err)
		}
		return nil
	})
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestTailscaleAddrs|TestSystemInterfaceAddrs|TestLoadTailscaleAddrs' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestTailscaleAddrs`, `TestSystemInterfaceAddrs`, `TestTailscaleAddrsRoundTrip` and `TestLoadTailscaleAddrsRefusesBadValue` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/tailscale.go internal/controller/listen/tailscale_test.go
git commit -m "feat(listen): find and save Tailscale addresses"
```


### Task 10: Free-bind sockets

**Files:**
- Create: `internal/controller/listen/bind.go`
- Test: `internal/controller/listen/bind_test.go`

**Interfaces:**
- Consumes: `Endpoint`, `Endpoint.URL` (Task 8); `golang.org/x/sys/unix`.
- Produces: Contracts › Task 10 (`BindFunc`, `Bind`); Task 14 calls `BindFunc` and defaults to `Bind`.

**Items:** 16

**Depends on:** Task 8

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Every listener's socket is opened here. The test reads the option back from the bound socket on loopback rather than binding an address the host lacks, so it needs no privilege and no foreign address; it skips with a reason where the kernel refuses or cannot report the option.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/bind_test.go`:

```go
package listen

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"strings"
	"syscall"
	"testing"

	"golang.org/x/sys/unix"
)

// sockopt reads an integer socket option of a TCP listener.
func sockopt(t *testing.T, ln net.Listener, level, opt int) (int, error) {
	t.Helper()
	raw, err := ln.(*net.TCPListener).SyscallConn()
	if err != nil {
		t.Fatal(err)
	}
	var v int
	var gerr error
	if err := raw.Control(func(fd uintptr) { v, gerr = unix.GetsockoptInt(int(fd), level, opt) }); err != nil { //nolint:gosec // G115: a file descriptor fits in an int
		t.Fatal(err)
	}
	return v, gerr
}

func TestBindSetsFreeBind(t *testing.T) {
	cases := []struct {
		name       string
		addr       string
		level, opt int
	}{
		{"ipv4", "127.0.0.1:0", unix.SOL_IP, unix.IP_FREEBIND},
		{"ipv6", "[::1]:0", unix.SOL_IPV6, unix.IPV6_FREEBIND},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			for _, free := range []bool{true, false} {
				ln, err := Bind(context.Background(), Endpoint{Addr: netip.MustParseAddrPort(c.addr), FreeBind: free})
				switch {
				case c.name == "ipv6" && (errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)):
					t.Skip("this host has no IPv6 loopback address")
				case errors.Is(err, syscall.EPERM):
					t.Skipf("the kernel refuses the free-bind option to this user: %v", err)
				case err != nil:
					t.Fatal(err)
				}
				v, err := sockopt(t, ln, c.level, c.opt)
				_ = ln.Close()
				if errors.Is(err, syscall.ENOPROTOOPT) {
					t.Skipf("the kernel cannot report the free-bind option: %v", err)
				}
				if err != nil {
					t.Fatal(err)
				}
				if (v == 1) != free {
					t.Fatalf("FreeBind %v: socket option = %d", free, v)
				}
			}
		})
	}
}

func TestBindReportsTheEndpoint(t *testing.T) {
	first, err := Bind(context.Background(), Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0")})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = first.Close() }()
	busy := netip.MustParseAddrPort(first.Addr().String())
	_, err = Bind(context.Background(), Endpoint{Addr: busy, TLS: true})
	if err == nil || !strings.Contains(err.Error(), "listen: bind https://"+busy.String()) || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("second bind of %s: error = %v, want a wrapped EADDRINUSE naming the endpoint", busy, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestBind' ./internal/controller/listen/`
Expected: FAIL with `undefined: Bind` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/bind.go`:

```go
package listen

import (
	"context"
	"fmt"
	"net"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// BindFunc opens the listening socket of one endpoint. Tests pass their own
// to stand in for addresses the test host lacks.
type BindFunc func(ctx context.Context, e Endpoint) (net.Listener, error)

// Bind is the production BindFunc: TCP on the endpoint's address family,
// with IP_FREEBIND or IPV6_FREEBIND when e.FreeBind is set (ADR 0006).
func Bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	network := "tcp4"
	if e.Addr.Addr().Is6() {
		network = "tcp6"
	}
	var lc net.ListenConfig
	if e.FreeBind {
		lc.Control = freeBind
	}
	ln, err := lc.Listen(ctx, network, e.Addr.String())
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	return ln, nil
}

func freeBind(network, _ string, c syscall.RawConn) error {
	level, opt := unix.SOL_IP, unix.IP_FREEBIND
	if network == "tcp6" {
		level, opt = unix.SOL_IPV6, unix.IPV6_FREEBIND
	}
	var serr error
	if err := c.Control(func(fd uintptr) {
		serr = unix.SetsockoptInt(int(fd), level, opt, 1) //nolint:gosec // G115: a file descriptor fits in an int
	}); err != nil {
		return err
	}
	if serr != nil {
		return os.NewSyscallError("setsockopt", serr)
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestBind' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestBindSetsFreeBind` (subtests `ipv4` and `ipv6`; `ipv6` may SKIP on a host without IPv6 loopback) and `TestBindReportsTheEndpoint` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/bind.go internal/controller/listen/bind_test.go
git commit -m "feat(listen): bind sockets with IP_FREEBIND"
```


### Task 11: Problem details, JSON bodies and response headers

**Files:**
- Create: `internal/controller/api/wire.go`
- Create: `internal/controller/api/middleware.go`
- Test: `internal/controller/api/wire_test.go`

**Interfaces:**
- Consumes: `errcode` (foundation: `From`, `Known`, `Code.Status`, `Code.Title`, `FieldError`); `auth.RetryAfterError` (Task 3).
- Produces: Contracts › Task 11 (the package doc comment, `MaxBodyBytes`, `problem`, `writeProblem`, `writeJSON`, `decodeBody`, `ContentSecurityPolicy`, `healthzPath`, `isAPIPath`, `securityHeaders`, `recoverPanics`, and the test helpers `testLog` and `decodeProblem`).

**Items:** 12, 52

**Depends on:** Task 3

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Every refusal of this package goes through `writeProblem`, so the code-to-status table and the rule that a 5xx answer never carries its cause live in one place. `recoverPanics` logs a panic with its stack before answering `internal`, because a panic inside the lifecycle engine leaves a change `applying` until recovery and must not disappear (register row 52); when the response has already started it aborts the connection instead of appending a problem to a half-written body.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/wire_test.go`:

```go
package api

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// testLog returns a logger writing text lines into the returned buffer.
func testLog() (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return slog.New(slog.NewTextHandler(&buf, nil)), &buf
}

// decodeProblem checks that rec holds a problem with status and code and
// returns it.
func decodeProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code) problem {
	t.Helper()
	if rec.Code != status {
		t.Fatalf("status = %d, want %d; body %s", rec.Code, status, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var p problem
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
		t.Fatalf("problem is not JSON: %v; body %s", err, rec.Body)
	}
	if p.Code != code || p.Status != status || p.Type != "about:blank" || p.Title != code.Title() {
		t.Fatalf("problem = %+v, want code %s status %d", p, code, status)
	}
	return p
}

func TestWriteProblemStatuses(t *testing.T) {
	for code, status := range map[errcode.Code]int{
		errcode.InvalidJSON:            400,
		errcode.InvalidInput:           422,
		errcode.TooLarge:               413,
		errcode.UnsupportedMediaType:   415,
		errcode.MethodNotAllowed:       405,
		errcode.AuthRequired:           401,
		errcode.InvalidCredentials:     401,
		errcode.RateLimited:            429,
		errcode.CSRF:                   403,
		errcode.Forbidden:              403,
		errcode.NotFound:               404,
		errcode.ActionUnknown:          404,
		errcode.ModuleUnavailable:      409,
		errcode.PlanNotFound:           404,
		errcode.PlanExpired:            410,
		errcode.PlanStateChanged:       409,
		errcode.PlanPrincipalMismatch:  403,
		errcode.PlanConfirmationNeeded: 422,
		errcode.PlanGuardFailed:        409,
		errcode.ChangeNotFound:         404,
		errcode.ChangeNotPending:       409,
		errcode.LockHeld:               423,
		errcode.Internal:               500,
	} {
		log, _ := testLog()
		rec := httptest.NewRecorder()
		writeProblem(rec, log, errcode.New(code, "the detail"))
		p := decodeProblem(t, rec, status, code)
		want := "the detail"
		if status == 500 {
			want = code.Title()
		}
		if p.Detail != want {
			t.Errorf("%s: detail = %q, want %q", code, p.Detail, want)
		}
	}
}

func TestWriteProblemHidesInternalText(t *testing.T) {
	cause := errors.New("open /var/lib/darkpanel/controller.db: hunter2")
	for name, err := range map[string]error{
		"plain error":   cause,
		"wrapped":       errcode.Wrap(errcode.Internal, cause, "detail with hunter2"),
		"unknown code":  errcode.Wrap("made.up", cause, "hunter2"),
		"action code":   errcode.Wrap(errcode.ChangeRevertFailed, cause, "hunter2"),
		"with a field":  &errcode.Error{Code: errcode.Internal, Fields: []errcode.FieldError{{Path: "/x", Message: "hunter2"}}, Err: cause},
		"empty detail ": errcode.New(errcode.Internal, ""),
	} {
		log, logged := testLog()
		rec := httptest.NewRecorder()
		writeProblem(rec, log, err)
		if rec.Code != 500 {
			t.Fatalf("%s: status = %d, want 500", name, rec.Code)
		}
		if strings.Contains(rec.Body.String(), "hunter2") {
			t.Errorf("%s: body leaks the cause: %s", name, rec.Body)
		}
		var p problem
		if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil {
			t.Fatal(err)
		}
		if p.Detail == "" || p.Errors != nil {
			t.Errorf("%s: problem = %+v, want a title detail and no errors", name, p)
		}
		if name != "empty detail " && !strings.Contains(logged.String(), "hunter2") {
			t.Errorf("%s: the cause was not logged: %s", name, logged)
		}
	}
}

func TestWriteProblemFields(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	writeProblem(rec, log, &errcode.Error{Code: errcode.InvalidInput, Detail: "bad",
		Fields: []errcode.FieldError{{Path: "/name", Message: "required"}}})
	p := decodeProblem(t, rec, 422, errcode.InvalidInput)
	if len(p.Errors) != 1 || p.Errors[0].Path != "/name" || p.Errors[0].Message != "required" {
		t.Errorf("errors = %+v", p.Errors)
	}

	rec = httptest.NewRecorder()
	writeProblem(rec, log, errcode.New(errcode.NotFound, "no"))
	if strings.Contains(rec.Body.String(), `"errors"`) {
		t.Errorf("a problem without field errors has an errors member: %s", rec.Body)
	}
}

func TestWriteProblemRetryAfter(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	err := errcode.Wrap(errcode.RateLimited, &auth.RetryAfterError{After: 90*time.Second + time.Millisecond}, "slow down")
	writeProblem(rec, log, err)
	decodeProblem(t, rec, 429, errcode.RateLimited)
	if got := rec.Header().Get("Retry-After"); got != "91" {
		t.Errorf("Retry-After = %q, want 91", got)
	}

	rec = httptest.NewRecorder()
	writeProblem(rec, log, errcode.New(errcode.NotFound, "no"))
	if got := rec.Header().Get("Retry-After"); got != "" {
		t.Errorf("Retry-After on not_found = %q", got)
	}
}

func TestWriteJSON(t *testing.T) {
	log, _ := testLog()
	rec := httptest.NewRecorder()
	writeJSON(rec, log, http.StatusCreated, map[string]int{"n": 1})
	if rec.Code != 201 || rec.Header().Get("Content-Type") != "application/json" || rec.Body.String() != `{"n":1}` {
		t.Errorf("got %d %q %s", rec.Code, rec.Header().Get("Content-Type"), rec.Body)
	}

	rec = httptest.NewRecorder()
	writeJSON(rec, log, http.StatusOK, struct {
		Output json.RawMessage `json:"output"`
	}{json.RawMessage(`{not json`)})
	decodeProblem(t, rec, 500, errcode.Internal)
}

type decodeTarget struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
}

func TestDecodeBody(t *testing.T) {
	big := `{"name":"` + strings.Repeat("a", MaxBodyBytes) + `"}`
	for _, tc := range []struct {
		name, contentType, body string
		code                    errcode.Code // "" means success
		detail                  string
	}{
		{"valid", "application/json", `{"name":"x","count":2}`, "", ""},
		{"charset parameter", "application/json; charset=utf-8", `{"name":"x"}`, "", ""},
		{"trailing space", "application/json", "{\"name\":\"x\"}\n ", "", ""},
		{"no content type", "", `{"name":"x"}`, errcode.UnsupportedMediaType, "application/json"},
		{"text", "text/plain", `{"name":"x"}`, errcode.UnsupportedMediaType, "application/json"},
		{"too large", "application/json", big, errcode.TooLarge, "1048576"},
		{"empty", "application/json", "", errcode.InvalidJSON, "empty"},
		{"truncated", "application/json", `{"name":`, errcode.InvalidJSON, "ends early"},
		{"malformed", "application/json", `{"name" "x"}`, errcode.InvalidJSON, "byte"},
		{"unknown field", "application/json", `{"name":"x","extra":1}`, errcode.InvalidJSON, `unknown field "extra"`},
		{"wrong type", "application/json", `{"count":"two"}`, errcode.InvalidJSON, `"count"`},
		{"not an object", "application/json", `[1]`, errcode.InvalidJSON, "JSON object"},
		{"two values", "application/json", `{"name":"x"}{"name":"y"}`, errcode.InvalidJSON, "more than one"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/v1/x", strings.NewReader(tc.body))
			if tc.contentType != "" {
				r.Header.Set("Content-Type", tc.contentType)
			}
			var dst decodeTarget
			err := decodeBody(httptest.NewRecorder(), r, &dst)
			if tc.code == "" {
				if err != nil || dst.Name != "x" {
					t.Fatalf("err = %v, dst = %+v", err, dst)
				}
				return
			}
			var e *errcode.Error
			if !errors.As(err, &e) || e.Code != tc.code {
				t.Fatalf("err = %v, want code %s", err, tc.code)
			}
			if !strings.Contains(e.Detail, tc.detail) {
				t.Errorf("detail = %q, want it to contain %q", e.Detail, tc.detail)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	h := securityHeaders(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for _, tc := range []struct {
		path    string
		tls     bool
		noStore bool
	}{
		{"/api/v1/changes", false, true},
		{"/api", true, true},
		{"/healthz", false, true},
		{"/assets/app.js", true, false},
		{"/", false, false},
	} {
		r := httptest.NewRequest(http.MethodGet, tc.path, nil)
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		got := rec.Header()
		want := map[string]string{
			"Content-Security-Policy":    "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; font-src 'self'; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
			"X-Content-Type-Options":     "nosniff",
			"Referrer-Policy":            "no-referrer",
			"Cross-Origin-Opener-Policy": "same-origin",
		}
		for k, v := range want {
			if got.Get(k) != v {
				t.Errorf("%s: %s = %q, want %q", tc.path, k, got.Get(k), v)
			}
		}
		if hsts := got.Get("Strict-Transport-Security"); (hsts == "max-age=31536000") != tc.tls || (!tc.tls && hsts != "") {
			t.Errorf("%s (tls %v): Strict-Transport-Security = %q", tc.path, tc.tls, hsts)
		}
		if cc := got.Get("Cache-Control"); (cc == "no-store") != tc.noStore {
			t.Errorf("%s: Cache-Control = %q, want no-store %v", tc.path, cc, tc.noStore)
		}
	}
}

func TestRecoverPanics(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom in a handler")
	}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/actions/demo.item.remove/v1/apply", nil))
	p := decodeProblem(t, rec, 500, errcode.Internal)
	if strings.Contains(rec.Body.String(), "boom") || p.Detail != errcode.Internal.Title() {
		t.Errorf("the panic value reached the client: %s", rec.Body)
	}
	for _, want := range []string{"boom in a handler", "goroutine", "/api/v1/actions/demo.item.remove/v1/apply"} {
		if !strings.Contains(logged.String(), want) {
			t.Errorf("log lacks %q: %s", want, logged)
		}
	}
}

func TestRecoverPanicsAfterWriteAborts(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		panic("late boom")
	}))
	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
		if !strings.Contains(logged.String(), "late boom") {
			t.Errorf("log lacks the panic: %s", logged)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/changes", nil))
}

func TestRecoverPanicsPassesAbort(t *testing.T) {
	log, logged := testLog()
	h := recoverPanics(log, http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic(http.ErrAbortHandler)
	}))
	defer func() {
		v := recover()
		if err, ok := v.(error); !ok || !errors.Is(err, http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", v)
		}
		if logged.Len() != 0 {
			t.Errorf("an abort was logged: %s", logged)
		}
	}()
	h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/", nil))
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestWriteProblem|TestWriteJSON|TestDecodeBody|TestSecurityHeaders|TestRecoverPanics' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: problem`, `undefined: writeProblem` and `undefined: writeJSON` (and more).

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/wire.go`:

```go
// Package api serves the controller's HTTP API (spec section 12) on the
// network listeners and the break-glass routes on the controller socket
// (spec section 14).
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"strconv"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// MaxBodyBytes is the largest request body the API reads (spec section 12).
const MaxBodyBytes = 1 << 20

const problemContentType = "application/problem+json"

// problem is an RFC 9457 problem details body (spec section 12).
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as problem details. A code outside the registry
// becomes internal. A 5xx answer carries only the code's title and no field
// errors, because neither the detail nor the cause of an internal failure is
// client-safe; the cause is logged instead.
func writeProblem(w http.ResponseWriter, log *slog.Logger, err error) {
	e := errcode.From(err)
	code := e.Code
	if !errcode.Known(code) {
		code = errcode.Internal
	}
	status := code.Status()
	p := problem{Type: "about:blank", Title: code.Title(), Status: status, Detail: e.Detail, Code: code, Errors: e.Fields}
	if status >= http.StatusInternalServerError {
		p.Detail, p.Errors = code.Title(), nil
		log.Error("api: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	var ra *auth.RetryAfterError
	if code == errcode.RateLimited && errors.As(err, &ra) {
		w.Header().Set("Retry-After", strconv.Itoa(ra.Seconds()))
	}
	b, merr := json.Marshal(p)
	if merr != nil {
		panic(fmt.Sprintf("api: marshal problem: %v", merr)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeJSON answers v as JSON with status. A value that does not marshal,
// such as invalid raw output, is answered as internal.
func writeJSON(w http.ResponseWriter, log *slog.Logger, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		writeProblem(w, log, fmt.Errorf("api: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// decodeBody reads one JSON object into dst. The body must be sent as
// application/json, hold at most MaxBodyBytes, name no field dst lacks and
// hold nothing after the object.
func decodeBody(w http.ResponseWriter, r *http.Request, dst any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return errcode.New(errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json")
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, MaxBodyBytes))
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		return decodeError(err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			return decodeError(err)
		}
		return errcode.New(errcode.InvalidJSON, "the request body holds more than one JSON value")
	}
	return nil
}

func decodeError(err error) error {
	var (
		tooLarge  *http.MaxBytesError
		syntax    *json.SyntaxError
		wrongType *json.UnmarshalTypeError
	)
	detail := "the request body is not valid JSON"
	switch {
	case errors.As(err, &tooLarge):
		return errcode.New(errcode.TooLarge, fmt.Sprintf("the request body exceeds %d bytes", MaxBodyBytes))
	case errors.Is(err, io.EOF):
		detail = "the request body is empty"
	case errors.Is(err, io.ErrUnexpectedEOF):
		detail = "the request body ends early"
	case errors.As(err, &syntax):
		detail = fmt.Sprintf("malformed JSON at byte %d", syntax.Offset)
	case errors.As(err, &wrongType) && wrongType.Field == "":
		detail = "the request body must be a JSON object"
	case errors.As(err, &wrongType):
		detail = fmt.Sprintf("field %q has the wrong type", wrongType.Field)
	case strings.HasPrefix(err.Error(), "json: unknown field "):
		detail = strings.TrimPrefix(err.Error(), "json: ")
	}
	return errcode.New(errcode.InvalidJSON, detail)
}
```

Create `internal/controller/api/middleware.go`:

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
// answer for a complete one.
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
			writeProblem(w, log, errcode.New(errcode.Internal, ""))
		}()
		next.ServeHTTP(tw, r)
	})
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestWriteProblem|TestWriteJSON|TestDecodeBody|TestSecurityHeaders|TestRecoverPanics' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/wire.go internal/controller/api/middleware.go internal/controller/api/wire_test.go
git commit -m "feat(api): add problem details and headers"
```


### Task 12: Web authority root

**Files:**
- Create: `internal/controller/listen/authority.go`
- Test: `internal/controller/listen/authority_test.go`

**Interfaces:**
- Consumes: `core.Sealer`, `core.Clock` (foundation); `secret.NewSealer`, `secret.KeySize` (plan 2, tests only); `coretest.NewFakeClock`; `store.DB`; the `core_ca` table of `0001_core.sql`; test helpers `openControllerDB`, `openControllerDBAt` from Task 9.
- Produces: Contracts › Task 12 (`PurposeWeb`, `WebCA`, `Authority`, `RootPEM`, `Fingerprint`, `LoadOrCreateAuthority`, the `core_ca` stored format) and the test helpers `testStart`, `newSealer`, `rootCert`; Task 13 issues leaves from `Authority`, P9 serves `WebCA`, plan 4's server calls `LoadOrCreateAuthority`.

**Items:** 16

**Depends on:** Task 9

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Key custody (ADR 0005, ADR 0010): the private key is sealed before it is stored and the associated data binds it to its row id, so a ciphertext moved to another row does not open. Overlapping first starts are made safe by finding no row inside the immediate write transaction before inserting; the test runs four starts on four database handles and must see one root.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/authority_test.go`:

```go
package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// testStart is the fake clock's start; whole seconds, as X.509 times are.
var testStart = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

// newSealer returns a production sealer over a random key.
func newSealer(t *testing.T) *secret.Sealer {
	t.Helper()
	key := make([]byte, secret.KeySize)
	if _, err := rand.Read(key); err != nil {
		t.Fatal(err)
	}
	s, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// rootCert parses an authority's root certificate PEM.
func rootCert(t *testing.T, a *Authority) *x509.Certificate {
	t.Helper()
	block, rest := pem.Decode(a.RootPEM())
	if block == nil || block.Type != "CERTIFICATE" || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("RootPEM is not one CERTIFICATE block: %q", a.RootPEM())
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return cert
}

func TestAuthorityCreatesRoot(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	cert := rootCert(t, a)
	pub, ok := cert.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() {
		t.Fatalf("root key is %T, want ECDSA P-256", cert.PublicKey)
	}
	if !cert.IsCA || !cert.BasicConstraintsValid || cert.MaxPathLen != 0 || !cert.MaxPathLenZero {
		t.Fatalf("root constraints: IsCA %v, valid %v, MaxPathLen %d, zero %v",
			cert.IsCA, cert.BasicConstraintsValid, cert.MaxPathLen, cert.MaxPathLenZero)
	}
	if cert.KeyUsage != x509.KeyUsageCertSign|x509.KeyUsageCRLSign {
		t.Fatalf("root key usage = %v", cert.KeyUsage)
	}
	if !cert.NotAfter.Equal(testStart.AddDate(10, 0, 0)) || !cert.NotBefore.Equal(testStart.Add(-time.Hour)) {
		t.Fatalf("root validity %v to %v", cert.NotBefore, cert.NotAfter)
	}
	if err := cert.CheckSignatureFrom(cert); err != nil {
		t.Fatalf("root is not self-signed: %v", err)
	}
	sum := sha256.Sum256(cert.Raw)
	if a.Fingerprint() != hex.EncodeToString(sum[:]) {
		t.Fatalf("Fingerprint = %s, want %x", a.Fingerprint(), sum)
	}

	var id, purpose, certPEM, createdAt string
	var keyCT []byte
	if err := db.Read().QueryRowContext(ctx,
		`SELECT id, purpose, cert_pem, key_ct, created_at FROM core_ca`).Scan(&id, &purpose, &certPEM, &keyCT, &createdAt); err != nil {
		t.Fatal(err)
	}
	if purpose != PurposeWeb || certPEM != string(a.RootPEM()) || createdAt != testStart.Format(time.RFC3339) {
		t.Fatalf("core_ca row: purpose %q, created_at %q, cert_pem matches %v", purpose, createdAt, certPEM == string(a.RootPEM()))
	}
	der, err := sealer.Open("core_ca.key_ct."+id, keyCT)
	if err != nil {
		t.Fatalf("key_ct does not open with its row's associated data: %v", err)
	}
	if bytes.Contains(keyCT, der) {
		t.Fatal("key_ct holds the key in clear")
	}
	key, err := x509.ParsePKCS8PrivateKey(der)
	if err != nil {
		t.Fatal(err)
	}
	if k, ok := key.(*ecdsa.PrivateKey); !ok || !k.PublicKey.Equal(cert.PublicKey) {
		t.Fatal("the sealed key is not the root's key")
	}
}

func TestAuthorityLoadsTheSameRoot(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	first, err := LoadOrCreateAuthority(ctx, openControllerDBAt(t, path), sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(24 * time.Hour)
	db := openControllerDBAt(t, path)
	second, err := LoadOrCreateAuthority(ctx, db, sealer, clock)
	if err != nil {
		t.Fatal(err)
	}
	if second.Fingerprint() != first.Fingerprint() || !second.key.Equal(first.key) {
		t.Fatal("a second start created a new root")
	}
	var n int
	if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", n, err)
	}
}

func TestAuthorityConcurrentStartsAgree(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "controller.db")
	sealer := newSealer(t)
	clock := coretest.NewFakeClock(testStart)
	const starts = 4
	dbs := make([]*store.DB, starts)
	for i := range dbs {
		dbs[i] = openControllerDBAt(t, path)
	}
	prints := make([]string, starts)
	errs := make([]error, starts)
	var ready, wg sync.WaitGroup
	gate := make(chan struct{})
	for i := range starts {
		ready.Add(1)
		wg.Add(1)
		go func() {
			defer wg.Done()
			ready.Done()
			<-gate
			a, err := LoadOrCreateAuthority(ctx, dbs[i], sealer, clock)
			if err != nil {
				errs[i] = err
				return
			}
			prints[i] = a.Fingerprint()
		}()
	}
	ready.Wait()
	close(gate)
	wg.Wait()
	for i := range starts {
		if errs[i] != nil {
			t.Fatalf("start %d: %v", i, errs[i])
		}
		if prints[i] != prints[0] {
			t.Fatalf("start %d has root %s, start 0 has %s", i, prints[i], prints[0])
		}
	}
	var n int
	if err := dbs[0].Read().QueryRowContext(ctx, `SELECT count(*) FROM core_ca`).Scan(&n); err != nil || n != 1 {
		t.Fatalf("core_ca rows = %d, %v; want 1", n, err)
	}
}

func TestAuthorityRefusesAnotherKeyOrMovedRow(t *testing.T) {
	ctx := context.Background()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	if _, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateAuthority(ctx, db, newSealer(t), clock)
	if err == nil || !strings.Contains(err.Error(), "listen: web authority") {
		t.Fatalf("load with another controller key: error = %v", err)
	}

	db = openControllerDB(t)
	sealer := newSealer(t)
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_ca SET id = 'moved'`); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateAuthority(ctx, db, sealer, clock); err == nil {
		t.Fatal("a key sealed for another row id was accepted")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAuthority' ./internal/controller/listen/`
Expected: FAIL with `undefined: Authority` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/authority.go`:

```go
package listen

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/oklog/ulid/v2"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// PurposeWeb is the core_ca purpose of the web certificate root (ADR 0010).
const PurposeWeb = "web"

// backdate is how far before its issue time a certificate becomes valid, so
// a client whose clock runs a little behind still accepts it.
const backdate = time.Hour

// WebCA is the read-only view of the web root that the controller socket's
// web-ca route serves.
type WebCA interface {
	// RootPEM is the root certificate as one PEM CERTIFICATE block.
	RootPEM() []byte
	// Fingerprint is the lowercase hex SHA-256 of the root's DER encoding.
	Fingerprint() string
}

// Authority is the controller's web certificate root: ECDSA P-256, valid 10
// years, used only to issue the HTTPS listeners' leaf (ADR 0010).
type Authority struct {
	cert        *x509.Certificate
	key         *ecdsa.PrivateKey
	pem         []byte
	fingerprint string
}

var _ WebCA = (*Authority)(nil)

// RootPEM returns the root certificate in PEM.
func (a *Authority) RootPEM() []byte { return bytes.Clone(a.pem) }

// Fingerprint returns the lowercase hex SHA-256 of the root's DER encoding.
func (a *Authority) Fingerprint() string { return a.fingerprint }

// LoadOrCreateAuthority loads the web root from core_ca, creating it on the
// first start. Its private key is stored sealed with associated data
// core_ca.key_ct.<row id>. Starts that overlap agree on one root: the row is
// inserted only inside a write transaction that finds none.
func LoadOrCreateAuthority(ctx context.Context, db *store.DB, sealer core.Sealer, clock core.Clock) (*Authority, error) {
	row, found, err := selectRoot(ctx, db.Read())
	if err != nil {
		return nil, err
	}
	if !found {
		fresh, err := newRoot(sealer, clock.Now())
		if err != nil {
			return nil, err
		}
		err = db.Write(ctx, func(tx *sql.Tx) error {
			var existing bool
			row, existing, err = selectRoot(ctx, tx)
			if err != nil || existing {
				return err
			}
			row = fresh
			_, err = tx.ExecContext(ctx,
				`INSERT INTO core_ca (id, purpose, cert_pem, key_ct, created_at) VALUES (?, ?, ?, ?, ?)`,
				row.id, PurposeWeb, row.certPEM, row.keyCT, row.createdAt)
			if err != nil {
				return fmt.Errorf("listen: web authority: store root: %w", err)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return openRoot(row, sealer)
}

type rootRow struct {
	id, certPEM, createdAt string
	keyCT                  []byte
}

type querier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

func selectRoot(ctx context.Context, q querier) (rootRow, bool, error) {
	var r rootRow
	err := q.QueryRowContext(ctx,
		`SELECT id, cert_pem, key_ct, created_at FROM core_ca WHERE purpose = ? ORDER BY created_at, id LIMIT 1`,
		PurposeWeb).Scan(&r.id, &r.certPEM, &r.keyCT, &r.createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return rootRow{}, false, nil
	}
	if err != nil {
		return rootRow{}, false, fmt.Errorf("listen: web authority: read core_ca: %w", err)
	}
	return r, true, nil
}

func keyAD(id string) string { return "core_ca.key_ct." + id }

func newRoot(sealer core.Sealer, now time.Time) (rootRow, error) {
	id := ulid.MustNew(ulid.Timestamp(now), rand.Reader).String()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return rootRow{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "darkpanel web root " + id},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: create root: %w", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: encode key: %w", err)
	}
	defer clear(pkcs8)
	ct, err := sealer.Seal(keyAD(id), pkcs8)
	if err != nil {
		return rootRow{}, fmt.Errorf("listen: web authority: seal key: %w", err)
	}
	return rootRow{
		id:        id,
		certPEM:   string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})),
		keyCT:     ct,
		createdAt: now.UTC().Format(time.RFC3339),
	}, nil
}

func openRoot(r rootRow, sealer core.Sealer) (*Authority, error) {
	block, _ := pem.Decode([]byte(r.certPEM))
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, fmt.Errorf("listen: web authority: root %s: cert_pem holds no certificate", r.id)
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: %w", r.id, err)
	}
	if !cert.IsCA {
		return nil, fmt.Errorf("listen: web authority: root %s is not a CA certificate", r.id)
	}
	pkcs8, err := sealer.Open(keyAD(r.id), r.keyCT)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: open key: %w", r.id, err)
	}
	defer clear(pkcs8)
	parsed, err := x509.ParsePKCS8PrivateKey(pkcs8)
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: root %s: parse key: %w", r.id, err)
	}
	key, ok := parsed.(*ecdsa.PrivateKey)
	if !ok || key.Curve != elliptic.P256() || !key.PublicKey.Equal(cert.PublicKey) {
		return nil, fmt.Errorf("listen: web authority: root %s: the key does not match the certificate", r.id)
	}
	sum := sha256.Sum256(cert.Raw)
	return &Authority{
		cert:        cert,
		key:         key,
		pem:         pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}),
		fingerprint: hex.EncodeToString(sum[:]),
	}, nil
}

func randomSerial() (*big.Int, error) {
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("listen: web authority: serial: %w", err)
	}
	return serial, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestAuthority' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestAuthorityCreatesRoot`, `TestAuthorityLoadsTheSameRoot`, `TestAuthorityConcurrentStartsAgree` and `TestAuthorityRefusesAnotherKeyOrMovedRow` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/authority.go internal/controller/listen/authority_test.go
git commit -m "feat(listen): create the web certificate root"
```


### Task 13: Web leaf and TLS configuration

**Files:**
- Create: `internal/controller/listen/leaf.go`
- Test: `internal/controller/listen/leaf_test.go`

**Interfaces:**
- Consumes: `Authority`, `LoadOrCreateAuthority`, `backdate` and `randomSerial` (Task 12, same package); `core.Clock`; test helpers `openControllerDB`, `addrs` (Task 9) and `testStart`, `newSealer`, `rootCert` (Task 12).
- Produces: Contracts › Task 13 (`LeafValidity`, `LeafRenewAfter`, `Leaf`, `NewLeaf`, `Ensure`, `GetCertificate`, `TLSConfig`) and the test helpers `newLeaf`, `current`; Task 15 owns one `Leaf` and hands `TLSConfig()` to the listener set.

**Items:** 16

**Depends on:** Task 12

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The handshake test trusts only the generated root (`RootCAs` holds nothing else) and verifies by IP, by `localhost` and by the hostname; a name outside the SANs and a TLS 1.1 client must fail. `GetCertificate` reads an atomic pointer, so handshakes never wait for a renewal.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/leaf_test.go`:

```go
package listen

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"net/netip"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
)

// newLeaf returns a leaf manager for host "panel" on a fresh authority, with
// the authority and the fake clock it reads.
func newLeaf(t *testing.T) (*Leaf, *Authority, *coretest.FakeClock) {
	t.Helper()
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), openControllerDB(t), newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return NewLeaf(a, clock, "panel"), a, clock
}

func current(t *testing.T, l *Leaf) *tls.Certificate {
	t.Helper()
	c, err := l.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestLeafSANsAndChain(t *testing.T) {
	l, a, _ := newLeaf(t)
	if _, err := l.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("GetCertificate before Ensure returned a certificate")
	}
	issued, err := l.Ensure(addrs("fd7a:115c:a1e0::1", "192.168.1.5", "100.64.0.1", "192.168.1.5"))
	if err != nil || !issued {
		t.Fatalf("Ensure = %v, %v; want true, nil", issued, err)
	}
	c := current(t, l)
	if len(c.Certificate) != 2 || !reflect.DeepEqual(c.Certificate[1], rootCert(t, a).Raw) {
		t.Fatal("the chain is not [leaf, root]")
	}
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(leaf.DNSNames, []string{"panel", "localhost"}) {
		t.Fatalf("DNS SANs = %v", leaf.DNSNames)
	}
	var ips []string
	for _, ip := range leaf.IPAddresses {
		ips = append(ips, ip.String())
	}
	if !reflect.DeepEqual(ips, []string{"100.64.0.1", "192.168.1.5", "fd7a:115c:a1e0::1"}) {
		t.Fatalf("IP SANs = %v", ips)
	}
	pub, ok := leaf.PublicKey.(*ecdsa.PublicKey)
	if !ok || pub.Curve != elliptic.P256() || leaf.IsCA {
		t.Fatalf("leaf key %T, IsCA %v; want an ECDSA P-256 end-entity certificate", leaf.PublicKey, leaf.IsCA)
	}
	if !leaf.NotBefore.Equal(testStart.Add(-time.Hour)) || !leaf.NotAfter.Equal(testStart.Add(90*24*time.Hour)) {
		t.Fatalf("leaf validity %v to %v", leaf.NotBefore, leaf.NotAfter)
	}
	if leaf.KeyUsage != x509.KeyUsageDigitalSignature || !reflect.DeepEqual(leaf.ExtKeyUsage, []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}) {
		t.Fatalf("leaf usage %v %v", leaf.KeyUsage, leaf.ExtKeyUsage)
	}
	roots := x509.NewCertPool()
	roots.AddCert(rootCert(t, a))
	for _, name := range []string{"panel", "localhost", "100.64.0.1", "fd7a:115c:a1e0::1"} {
		if _, err := leaf.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, CurrentTime: testStart}); err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
}

func TestLeafRenewal(t *testing.T) {
	l, _, clock := newLeaf(t)
	ips := addrs("100.64.0.1")
	if _, err := l.Ensure(ips); err != nil {
		t.Fatal(err)
	}
	first := current(t, l)
	clock.Advance(LeafRenewAfter - time.Second)
	if issued, err := l.Ensure(ips); err != nil || issued || current(t, l) != first {
		t.Fatalf("Ensure before 60 days = %v, %v; want the same leaf", issued, err)
	}
	clock.Advance(time.Second)
	if issued, err := l.Ensure(ips); err != nil || !issued || current(t, l) == first {
		t.Fatalf("Ensure at 60 days = %v, %v; want a new leaf", issued, err)
	}
	second := current(t, l)
	if second.Leaf.NotAfter != clock.Now().Add(LeafValidity) {
		t.Fatalf("renewed leaf expires %v", second.Leaf.NotAfter)
	}
	if issued, err := l.Ensure(addrs("100.64.0.2")); err != nil || !issued || current(t, l) == second {
		t.Fatalf("Ensure after an address change = %v, %v; want a new leaf", issued, err)
	}
	if issued, err := l.Ensure(addrs("100.64.0.2", "100.64.0.2")); err != nil || issued {
		t.Fatalf("Ensure with the same set = %v, %v; want no new leaf", issued, err)
	}
}

func TestLeafHandshake(t *testing.T) {
	l, a, clock := newLeaf(t)
	if _, err := l.Ensure(addrs("127.0.0.1")); err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", l.TLSConfig())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			_ = conn.(*tls.Conn).Handshake()
			_ = conn.Close()
		}
	}()
	roots := x509.NewCertPool()
	roots.AddCert(rootCert(t, a))
	dial := func(cfg *tls.Config) (*tls.Conn, error) {
		cfg.RootCAs = roots
		cfg.Time = clock.Now
		d := &tls.Dialer{NetDialer: &net.Dialer{Timeout: 5 * time.Second}, Config: cfg}
		conn, err := d.DialContext(context.Background(), "tcp", ln.Addr().String())
		if err != nil {
			return nil, err
		}
		return conn.(*tls.Conn), nil
	}
	for _, name := range []string{"", "localhost", "panel"} {
		conn, err := dial(&tls.Config{ServerName: name})
		if err != nil {
			t.Fatalf("handshake with ServerName %q: %v", name, err)
		}
		if n := len(conn.ConnectionState().PeerCertificates); n != 2 {
			t.Fatalf("server sent %d certificates, want 2", n)
		}
		_ = conn.Close()
	}
	var unknown x509.HostnameError
	if _, err := dial(&tls.Config{ServerName: "other.example"}); !errors.As(err, &unknown) {
		t.Fatalf("handshake for a name outside the SANs: %v, want a hostname error", err)
	}
	if _, err := dial(&tls.Config{MaxVersion: tls.VersionTLS11}); err == nil { //nolint:gosec // G402: an old client must be refused
		t.Fatal("a TLS 1.1 client completed the handshake")
	}
	if cfg := l.TLSConfig(); cfg.MinVersion != tls.VersionTLS12 {
		t.Fatalf("MinVersion = %x, want TLS 1.2", cfg.MinVersion)
	}
}

func TestLeafConcurrentReads(t *testing.T) {
	l, _, clock := newLeaf(t)
	if _, err := l.Ensure(addrs("100.64.0.1")); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					if _, err := l.GetCertificate(&tls.ClientHelloInfo{}); err != nil {
						t.Error(err)
						return
					}
				}
			}
		})
	}
	for i := range 20 {
		clock.Advance(LeafRenewAfter)
		if _, err := l.Ensure([]netip.Addr{netip.AddrFrom4([4]byte{100, 64, 0, byte(i)})}); err != nil {
			t.Error(err)
		}
	}
	close(stop)
	wg.Wait()
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLeaf' ./internal/controller/listen/`
Expected: FAIL with `undefined: Leaf` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/leaf.go`:

```go
package listen

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"slices"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

// Leaf lifetimes (ADR 0010).
const (
	LeafValidity   = 90 * 24 * time.Hour
	LeafRenewAfter = 60 * 24 * time.Hour
)

// Leaf holds the HTTPS listeners' certificate, issued by the web authority
// and kept in memory only. Its SANs are the hostname, localhost and the IP
// addresses passed to Ensure.
type Leaf struct {
	auth     *Authority
	clock    core.Clock
	hostname string
	mu       sync.Mutex // serialises Ensure
	cur      atomic.Pointer[issuedLeaf]
}

type issuedLeaf struct {
	cert *tls.Certificate
	ips  []netip.Addr
	at   time.Time
}

// NewLeaf returns a leaf manager for hostname; no certificate exists until
// the first Ensure.
func NewLeaf(a *Authority, clock core.Clock, hostname string) *Leaf {
	return &Leaf{auth: a, clock: clock, hostname: hostname}
}

// Ensure issues a new leaf when none exists, when ips differ from the current
// leaf's IP SANs, or when LeafRenewAfter has passed since the current one was
// issued. It reports whether it issued one.
func (l *Leaf) Ensure(ips []netip.Addr) (bool, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	want := normalizeAddrs(ips)
	now := l.clock.Now()
	if cur := l.cur.Load(); cur != nil && slices.Equal(cur.ips, want) && now.Sub(cur.at) < LeafRenewAfter {
		return false, nil
	}
	cert, err := l.auth.issue(l.hostname, want, now)
	if err != nil {
		return false, err
	}
	l.cur.Store(&issuedLeaf{cert: cert, ips: want, at: now})
	return true, nil
}

// GetCertificate serves tls.Config.GetCertificate with the chain
// [leaf, root].
func (l *Leaf) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	cur := l.cur.Load()
	if cur == nil {
		return nil, errors.New("listen: no web certificate issued yet")
	}
	return cur.cert, nil
}

// TLSConfig returns the HTTPS listeners' configuration: TLS 1.2 or later and
// the current leaf.
func (l *Leaf) TLSConfig() *tls.Config {
	return &tls.Config{MinVersion: tls.VersionTLS12, GetCertificate: l.GetCertificate}
}

func normalizeAddrs(list []netip.Addr) []netip.Addr {
	out := make([]netip.Addr, 0, len(list))
	for _, a := range list {
		out = append(out, a.Unmap())
	}
	slices.SortFunc(out, netip.Addr.Compare)
	return slices.Compact(out)
}

func (a *Authority) issue(hostname string, ips []netip.Addr, now time.Time) (*tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: generate key: %w", err)
	}
	serial, err := randomSerial()
	if err != nil {
		return nil, err
	}
	names := []string{"localhost"}
	if hostname != "" && hostname != "localhost" {
		names = []string{hostname, "localhost"}
	}
	ipSANs := make([]net.IP, 0, len(ips))
	for _, ip := range ips {
		ipSANs = append(ipSANs, ip.AsSlice())
	}
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: names[0]},
		DNSNames:              names,
		IPAddresses:           ipSANs,
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(LeafValidity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, &key.PublicKey, a.key)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("listen: web leaf: %w", err)
	}
	return &tls.Certificate{Certificate: [][]byte{der, a.cert.Raw}, PrivateKey: key, Leaf: leaf}, nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLeaf' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestLeafSANsAndChain`, `TestLeafRenewal`, `TestLeafHandshake` and `TestLeafConcurrentReads` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/leaf.go internal/controller/listen/leaf_test.go
git commit -m "feat(listen): issue the in-memory web leaf"
```


### Task 14: Listener set

**Files:**
- Create: `internal/controller/listen/set.go`
- Test: `internal/controller/listen/set_test.go`

**Interfaces:**
- Consumes: `Endpoint` (Task 8); `BindFunc`, `Bind` (Task 10).
- Produces: Contracts › Task 14 (the timeout constants, `ErrClosed`, `SetConfig`, `Bound`, `Set`, `NewSet`, `Update`, `Bound()`, `Shutdown`, and the `r.TLS != nil` rule P9 relies on) and the test helpers `fakeBinder`, `testTLS`, `schemeHandler`, `newTestSet`, `get`, `boundAddr`, `epHTTP`, `epTS1`, `epTS2`, `epTS3`, `epLoop6`; Task 15 owns one `Set`.

**Items:** 16

**Depends on:** Tasks 8 and 10

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Concurrency: `Update`, `Bound` and `Shutdown` hold one mutex, and every serving goroutine is counted so `Shutdown` returns only after they exit. A stopped server's socket is closed explicitly after `Shutdown`, because `http.Server.Shutdown` closes only listeners that `Serve` has already registered and the address must be free before a rebind. Tests serve every endpoint on `127.0.0.1:0` through `fakeBinder`; the TLS endpoints use httptest's certificate, so this task does not depend on the authority.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/set_test.go`:

```go
package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// fakeBinder stands in for Bind: every endpoint, whatever its address, is
// bound on 127.0.0.1 with port 0, unless fail names an error for it.
type fakeBinder struct {
	mu    sync.Mutex
	calls []Endpoint
	fail  map[netip.AddrPort]error
}

func (b *fakeBinder) bind(ctx context.Context, e Endpoint) (net.Listener, error) {
	b.mu.Lock()
	b.calls = append(b.calls, e)
	err := b.fail[e.Addr]
	b.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("listen: bind %s: %w", e.URL(), err)
	}
	var lc net.ListenConfig
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

func (b *fakeBinder) called() []Endpoint {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]Endpoint(nil), b.calls...)
}

// testTLS returns a server configuration with httptest's certificate (valid
// for 127.0.0.1) and a client that trusts it.
func testTLS(t *testing.T) (*tls.Config, *http.Client) {
	t.Helper()
	ts := httptest.NewTLSServer(http.NotFoundHandler())
	cfg := &tls.Config{MinVersion: tls.VersionTLS12, Certificates: ts.TLS.Certificates}
	client := ts.Client()
	ts.Close()
	client.Timeout = 5 * time.Second
	return cfg, client
}

func schemeHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answer := "tls=false"
		if r.TLS != nil {
			answer = "tls=true"
		}
		_, _ = io.WriteString(w, answer)
	})
}

func newTestSet(t *testing.T, h http.Handler, b *fakeBinder) (*Set, *http.Client) {
	t.Helper()
	cfg, client := testTLS(t)
	s := NewSet(SetConfig{Handler: h, TLS: cfg, Bind: b.bind, Log: slog.New(slog.DiscardHandler)})
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.Shutdown(ctx)
	})
	return s, client
}

func get(t *testing.T, client *http.Client, url string) string {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// boundAddr returns the real address the set bound for e.
func boundAddr(t *testing.T, s *Set, e Endpoint) string {
	t.Helper()
	for _, b := range s.Bound() {
		if b.Endpoint == e {
			return b.Addr.String()
		}
	}
	t.Fatalf("%s is not bound; bound: %v", e.URL(), s.Bound())
	return ""
}

var (
	epHTTP  = Endpoint{Addr: netip.MustParseAddrPort("127.0.0.1:0")}
	epTS1   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.1:8443"), TLS: true, FreeBind: true}
	epTS2   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.2:8443"), TLS: true, FreeBind: true}
	epTS3   = Endpoint{Addr: netip.MustParseAddrPort("100.64.0.3:8443"), TLS: true, FreeBind: true}
	epLoop6 = Endpoint{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true}
)

func TestSetServesEveryEndpoint(t *testing.T) {
	b := &fakeBinder{}
	s, client := newTestSet(t, schemeHandler(), b)
	if err := s.Update(context.Background(), []Endpoint{epHTTP, epTS1}); err != nil {
		t.Fatal(err)
	}
	if got := get(t, client, "http://"+boundAddr(t, s, epHTTP)+"/"); got != "tls=false" {
		t.Fatalf("HTTP endpoint answered %q", got)
	}
	if got := get(t, client, "https://"+boundAddr(t, s, epTS1)+"/"); got != "tls=true" {
		t.Fatalf("HTTPS endpoint answered %q", got)
	}
	bound := s.Bound()
	if len(bound) != 2 || bound[0].Endpoint != epTS1 || bound[1].Endpoint != epHTTP {
		t.Fatalf("Bound = %v, want the two endpoints sorted by address", bound)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for e, r := range s.servers {
		srv := r.srv
		if srv.ReadHeaderTimeout != 10*time.Second || srv.ReadTimeout != 30*time.Second ||
			srv.WriteTimeout != 60*time.Second || srv.IdleTimeout != 120*time.Second {
			t.Errorf("%s timeouts: header %v, read %v, write %v, idle %v", e.URL(),
				srv.ReadHeaderTimeout, srv.ReadTimeout, srv.WriteTimeout, srv.IdleTimeout)
		}
	}
}

func TestSetUpdateRebindsOnlyTheChange(t *testing.T) {
	b := &fakeBinder{}
	s, client := newTestSet(t, schemeHandler(), b)
	ctx := context.Background()
	if err := s.Update(ctx, []Endpoint{epTS1, epTS2}); err != nil {
		t.Fatal(err)
	}
	gone := boundAddr(t, s, epTS1)
	kept := boundAddr(t, s, epTS2)
	if err := s.Update(ctx, []Endpoint{epTS2, epTS3}); err != nil {
		t.Fatal(err)
	}
	if boundAddr(t, s, epTS2) != kept {
		t.Fatal("an unchanged endpoint was rebound")
	}
	if got := get(t, client, "https://"+boundAddr(t, s, epTS3)+"/"); got != "tls=true" {
		t.Fatalf("new endpoint answered %q", got)
	}
	if _, err := client.Get("https://" + gone + "/"); err == nil {
		t.Fatal("a removed endpoint still answers")
	}
	calls := b.called()
	if len(calls) != 3 || calls[2] != epTS3 {
		t.Fatalf("bind calls = %v, want epTS1, epTS2, epTS3", calls)
	}
}

func TestSetSkipsAMissingOptionalAddress(t *testing.T) {
	b := &fakeBinder{fail: map[netip.AddrPort]error{
		epLoop6.Addr: &net.OpError{Op: "listen", Net: "tcp6", Err: os.NewSyscallError("bind", syscall.EADDRNOTAVAIL)},
		epTS2.Addr:   &net.OpError{Op: "listen", Net: "tcp4", Err: os.NewSyscallError("bind", syscall.EADDRINUSE)},
	}}
	s, _ := newTestSet(t, schemeHandler(), b)
	ctx := context.Background()
	for range 2 {
		if err := s.Update(ctx, []Endpoint{epHTTP, epLoop6}); err != nil {
			t.Fatalf("Update with a missing optional address: %v", err)
		}
	}
	if n := len(b.called()); n != 2 {
		t.Fatalf("bind called %d times, want 2 (the optional address only once)", n)
	}
	err := s.Update(ctx, []Endpoint{epHTTP, epLoop6, epTS1, epTS2})
	if err == nil || !strings.Contains(err.Error(), epTS2.URL()) || !errors.Is(err, syscall.EADDRINUSE) {
		t.Fatalf("Update with a busy address: error = %v", err)
	}
	if bound := s.Bound(); len(bound) != 2 {
		t.Fatalf("Bound = %v, want epHTTP and epTS1", bound)
	}
}

func TestSetRefusesHTTPSWithoutTLSConfig(t *testing.T) {
	b := &fakeBinder{}
	s := NewSet(SetConfig{Handler: schemeHandler(), Bind: b.bind})
	defer func() { _ = s.Shutdown(context.Background()) }()
	err := s.Update(context.Background(), []Endpoint{epTS1})
	if err == nil || !strings.Contains(err.Error(), "needs a TLS configuration") {
		t.Fatalf("Update = %v", err)
	}
}

func TestSetShutdownIsGraceful(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	})
	b := &fakeBinder{}
	s, client := newTestSet(t, h, b)
	if err := s.Update(context.Background(), []Endpoint{epHTTP}); err != nil {
		t.Fatal(err)
	}
	url := "http://" + boundAddr(t, s, epHTTP) + "/"
	answer := make(chan string, 1)
	go func() {
		resp, err := client.Get(url)
		if err != nil {
			answer <- err.Error()
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, _ := io.ReadAll(resp.Body)
		answer <- string(body)
	}()
	<-entered
	done := make(chan error, 1)
	go func() { done <- s.Shutdown(context.Background()) }()
	select {
	case err := <-done:
		t.Fatalf("Shutdown returned %v while a request was running", err)
	case <-time.After(50 * time.Millisecond):
	}
	close(release)
	if got := <-answer; got != "done" {
		t.Fatalf("the running request got %q", got)
	}
	if err := <-done; err != nil {
		t.Fatalf("Shutdown = %v", err)
	}
	if err := s.Update(context.Background(), []Endpoint{epHTTP}); !errors.Is(err, ErrClosed) {
		t.Fatalf("Update after Shutdown = %v, want ErrClosed", err)
	}
	if len(s.Bound()) != 0 {
		t.Fatal("Bound is not empty after Shutdown")
	}
	if _, err := client.Get(url); err == nil {
		t.Fatal("the listener still answers after Shutdown")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSet' ./internal/controller/listen/`
Expected: FAIL with `undefined: Set` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/set.go`:

```go
package listen

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"slices"
	"sync"
	"syscall"
	"time"
)

// Server limits of every listener (spec section 12).
const (
	ReadHeaderTimeout = 10 * time.Second
	ReadTimeout       = 30 * time.Second
	WriteTimeout      = 60 * time.Second
	IdleTimeout       = 120 * time.Second
)

// ErrClosed is returned by Update after Shutdown.
var ErrClosed = errors.New("listen: the listener set is shut down")

// SetConfig configures a Set.
type SetConfig struct {
	Handler http.Handler
	// TLS serves the HTTPS endpoints; each server takes a clone.
	TLS *tls.Config
	// Bind opens the sockets; nil means Bind.
	Bind BindFunc
	// Log receives one line per bind, skip and serve failure; nil discards.
	Log *slog.Logger
}

// Bound is one running listener: the endpoint and the address its socket is
// bound to (the real port when the endpoint asked for port 0).
type Bound struct {
	Endpoint Endpoint
	Addr     net.Addr
}

// Set runs Handler on every bound endpoint. HTTPS endpoints terminate TLS in
// the server, so a request has a non-nil Request.TLS exactly when it arrived
// over HTTPS. Its methods are safe for concurrent use.
type Set struct {
	cfg     SetConfig
	mu      sync.Mutex
	servers map[Endpoint]*running
	skipped map[Endpoint]bool
	closed  bool
	wg      sync.WaitGroup
}

type running struct {
	srv *http.Server
	ln  net.Listener
}

// NewSet returns a set with nothing bound.
func NewSet(cfg SetConfig) *Set {
	if cfg.Bind == nil {
		cfg.Bind = Bind
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	return &Set{cfg: cfg, servers: map[Endpoint]*running{}, skipped: map[Endpoint]bool{}}
}

// Update makes want the bound endpoints: it shuts down the servers of
// endpoints not in want (gracefully until ctx ends, then forcibly) and binds
// those not yet bound, leaving unchanged ones untouched. An optional endpoint
// whose address the host lacks is skipped once with a log line and never
// retried. It returns every other bind failure joined; the endpoints that did
// bind keep serving, and a later Update retries the failed ones.
func (s *Set) Update(ctx context.Context, want []Endpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return ErrClosed
	}
	for e, r := range s.servers {
		if !slices.Contains(want, e) {
			if err := r.stop(ctx); err != nil {
				s.cfg.Log.Warn("listen: stopped, cutting off running requests", "url", e.URL(), "err", err)
			} else {
				s.cfg.Log.Info("listen: stopped", "url", e.URL())
			}
			delete(s.servers, e)
		}
	}
	var errs []error
	for _, e := range want {
		if s.servers[e] != nil || s.skipped[e] {
			continue
		}
		if e.TLS && s.cfg.TLS == nil {
			errs = append(errs, fmt.Errorf("listen: %s needs a TLS configuration", e.URL()))
			continue
		}
		ln, err := s.cfg.Bind(ctx, e)
		if err != nil {
			if e.Optional && addrUnavailable(err) {
				s.skipped[e] = true
				s.cfg.Log.Info("listen: skipped: this host lacks the address", "url", e.URL(), "err", err)
				continue
			}
			errs = append(errs, err)
			continue
		}
		s.servers[e] = s.serve(e, ln)
		s.cfg.Log.Info("listen: serving", "url", e.URL(), "addr", ln.Addr().String())
	}
	return errors.Join(errs...)
}

func addrUnavailable(err error) bool {
	return errors.Is(err, syscall.EADDRNOTAVAIL) || errors.Is(err, syscall.EAFNOSUPPORT)
}

func (s *Set) serve(e Endpoint, ln net.Listener) *running {
	srv := &http.Server{
		Handler:           s.cfg.Handler,
		ReadHeaderTimeout: ReadHeaderTimeout,
		ReadTimeout:       ReadTimeout,
		WriteTimeout:      WriteTimeout,
		IdleTimeout:       IdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.cfg.Log.Handler(), slog.LevelWarn),
	}
	if e.TLS {
		srv.TLSConfig = s.cfg.TLS.Clone()
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		var err error
		if e.TLS {
			err = srv.ServeTLS(ln, "", "")
		} else {
			err = srv.Serve(ln)
		}
		if !errors.Is(err, http.ErrServerClosed) {
			s.cfg.Log.Error("listen: serving stopped", "url", e.URL(), "err", err)
		}
	}()
	return &running{srv: srv, ln: ln}
}

// stop shuts the server down and closes its socket even when Serve has not
// started yet, so the address is free when stop returns. It returns ctx's
// error when running requests had to be cut off.
func (r *running) stop(ctx context.Context) error {
	err := r.srv.Shutdown(ctx)
	if err != nil {
		_ = r.srv.Close()
	}
	_ = r.ln.Close()
	return err
}

// Bound returns the running listeners sorted by endpoint address.
func (s *Set) Bound() []Bound {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Bound, 0, len(s.servers))
	for e, r := range s.servers {
		out = append(out, Bound{Endpoint: e, Addr: r.ln.Addr()})
	}
	slices.SortFunc(out, func(a, b Bound) int { return a.Endpoint.Addr.Compare(b.Endpoint.Addr) })
	return out
}

// Shutdown stops every server, letting running requests finish until ctx
// ends, and waits for the serving goroutines. Later Updates fail with
// ErrClosed. It returns ctx's error when requests had to be cut off.
func (s *Set) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	servers := s.servers
	s.servers = map[Endpoint]*running{}
	s.mu.Unlock()
	errs := make([]error, 0, len(servers))
	var mu sync.Mutex
	var stops sync.WaitGroup
	for _, r := range servers {
		stops.Go(func() {
			if err := r.stop(ctx); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	stops.Wait()
	s.wg.Wait()
	if len(errs) > 0 {
		return fmt.Errorf("listen: shutdown: %w", errs[0])
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSet' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestSetServesEveryEndpoint`, `TestSetUpdateRebindsOnlyTheChange`, `TestSetSkipsAMissingOptionalAddress`, `TestSetRefusesHTTPSWithoutTLSConfig` and `TestSetShutdownIsGraceful` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/set.go internal/controller/listen/set_test.go
git commit -m "feat(listen): serve the handler on a listener set"
```


### Task 15: Listener service

**Files:**
- Create: `internal/controller/listen/service.go`
- Test: `internal/controller/listen/service_test.go`

**Interfaces:**
- Consumes: `Spec`, `ParseSpecs`, `HasTailscale`, `Endpoints` (Task 8); `TailscaleInterface`, `TailscaleAddrs`, `InterfaceAddrs`, `SystemInterfaceAddrs`, `LoadTailscaleAddrs`, `SaveTailscaleAddrs` (Task 9); `Authority`, `LoadOrCreateAuthority` (Task 12); `Leaf`, `NewLeaf`, `LeafValidity`, `LeafRenewAfter` (Task 13); `BindFunc` (Task 10); `Set`, `SetConfig`, `Bound`, `NewSet` (Task 14); `core.Clock`, `core.Timer`, `coretest.NewFakeClock`, `store.DB`; the test helpers of Tasks 9, 12 and 14.
- Produces: Contracts › Task 15 (`CheckInterval`, `Config`, `State`, `Service`, `Start`, `State()`, `Shutdown`, the state messages); plan 4's server starts it and P9's footer route reads `State`.

**Items:** 16

**Depends on:** Tasks 9, 13 and 14

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Concurrency: one mutex serialises the checks and `Shutdown`; a check re-arms its timer before releasing the mutex, and `Shutdown` sets `closed` and stops the newest timer under the same mutex, so no check runs or re-arms after it. `State` reads an atomic pointer, so the UI footer never waits for a rebind. `TestServiceRenewsTheLeaf` disarms the timer and calls `check` directly after jumping the clock, because `coretest.FakeClock.Advance` through 60 days of 10-second checks is quadratic.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/listen/service_test.go`:

```go
package listen

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"log/slog"
	"net/http"
	"net/netip"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// fakeInterfaces stands in for SystemInterfaceAddrs.
type fakeInterfaces struct {
	mu      sync.Mutex
	addrs   []netip.Addr
	present bool
	calls   int
}

func (f *fakeInterfaces) set(present bool, list ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.present = present
	f.addrs = addrs(list...)
}

func (f *fakeInterfaces) list(name string) ([]netip.Addr, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if name != TailscaleInterface || !f.present {
		return nil, false, nil
	}
	return append(f.addrs, netip.MustParseAddr("fe80::1")), true, nil
}

func (f *fakeInterfaces) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type serviceFixture struct {
	db     *store.DB
	clock  *coretest.FakeClock
	auth   *Authority
	ifs    *fakeInterfaces
	binder *fakeBinder
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	db := openControllerDB(t)
	clock := coretest.NewFakeClock(testStart)
	a, err := LoadOrCreateAuthority(context.Background(), db, newSealer(t), clock)
	if err != nil {
		t.Fatal(err)
	}
	return &serviceFixture{db: db, clock: clock, auth: a, ifs: &fakeInterfaces{}, binder: &fakeBinder{}}
}

func (f *serviceFixture) start(t *testing.T, values ...string) (*Service, error) {
	t.Helper()
	specs, err := ParseSpecs(values)
	if err != nil {
		t.Fatal(err)
	}
	s, err := Start(context.Background(), Config{
		Specs: specs, Hostname: "panel", DB: f.db, Authority: f.auth, Clock: f.clock,
		Handler: schemeHandler(), Log: slog.New(slog.DiscardHandler),
		Interfaces: f.ifs.list, Bind: f.binder.bind,
	})
	if err == nil {
		t.Cleanup(func() { _ = s.Shutdown(context.Background()) })
	}
	return s, err
}

func endpointsOf(st State) []Endpoint {
	out := make([]Endpoint, 0, len(st.Listeners))
	for _, b := range st.Listeners {
		out = append(out, b.Endpoint)
	}
	return out
}

func savedAddrs(t *testing.T, db *store.DB) string {
	t.Helper()
	var v string
	if err := db.Read().QueryRowContext(context.Background(),
		`SELECT value FROM core_meta WHERE key = 'tailscale_addrs'`).Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

// rootClient trusts only the authority's root and checks the name
// localhost, because the fake binder serves every endpoint on 127.0.0.1.
func rootClient(t *testing.T, a *Authority) *http.Client {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(a.RootPEM()) {
		t.Fatal("RootPEM did not parse")
	}
	return &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: pool, ServerName: "localhost"},
	}}
}

func TestServiceDefaultsWithoutTailscale(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	want := []Endpoint{
		{Addr: netip.MustParseAddrPort("127.0.0.1:8080")},
		{Addr: netip.MustParseAddrPort("[::1]:8080"), Optional: true},
	}
	if !reflect.DeepEqual(endpointsOf(st), want) {
		t.Fatalf("listeners = %v, want %v", endpointsOf(st), want)
	}
	if st.TailscaleBound || st.Message != "No Tailscale address found; serving loopback only." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	if got := get(t, http.DefaultClient, "http://"+st.Listeners[0].Addr.String()+"/"); got != "tls=false" {
		t.Fatalf("loopback answered %q", got)
	}
	if _, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); err == nil {
		t.Fatal("a leaf was issued with no HTTPS listener")
	}
}

func TestServiceBindsSavedAddressesBeforeTailscaled(t *testing.T) {
	f := newServiceFixture(t)
	if err := SaveTailscaleAddrs(context.Background(), f.db, addrs("100.64.0.1")); err != nil {
		t.Fatal(err)
	}
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	ts := Endpoint{Addr: netip.MustParseAddrPort("100.64.0.1:8443"), TLS: true, FreeBind: true}
	if eps := endpointsOf(st); len(eps) != 3 || eps[0] != ts {
		t.Fatalf("listeners = %v, want the saved Tailscale address first", eps)
	}
	if !st.TailscaleBound || st.Message != "Serving on Tailscale: https://100.64.0.1:8443." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	if got := get(t, rootClient(t, f.auth), "https://"+st.Listeners[0].Addr.String()+"/"); got != "tls=true" {
		t.Fatalf("Tailscale listener answered %q", got)
	}
	c, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(c.Leaf.IPAddresses) != 1 || c.Leaf.IPAddresses[0].String() != "100.64.0.1" {
		t.Fatalf("leaf = %v, %v; want the IP SAN 100.64.0.1", c, err)
	}
}

func TestServiceFollowsAddressChanges(t *testing.T) {
	f := newServiceFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	if savedAddrs(t, f.db) != `["100.64.0.1"]` || !s.State().TailscaleBound {
		t.Fatal("the first read was not saved and bound")
	}

	f.ifs.set(true, "fd7a:115c:a1e0::2", "100.64.0.2")
	f.clock.Advance(CheckInterval)
	st := s.State()
	if savedAddrs(t, f.db) != `["100.64.0.2","fd7a:115c:a1e0::2"]` {
		t.Fatalf("saved = %s", savedAddrs(t, f.db))
	}
	if st.Message != "Serving on Tailscale: https://100.64.0.2:8443, https://[fd7a:115c:a1e0::2]:8443." {
		t.Fatalf("message = %q", st.Message)
	}
	c, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(c.Leaf.IPAddresses) != 2 || c.Leaf.IPAddresses[0].String() != "100.64.0.2" {
		t.Fatalf("leaf after the change = %v, %v", c, err)
	}

	f.ifs.set(false)
	f.clock.Advance(CheckInterval)
	if !s.State().TailscaleBound || savedAddrs(t, f.db) != `["100.64.0.2","fd7a:115c:a1e0::2"]` {
		t.Fatal("an absent interface dropped the last addresses")
	}

	f.ifs.set(true)
	f.clock.Advance(CheckInterval)
	st = s.State()
	if st.TailscaleBound || len(st.Listeners) != 2 || savedAddrs(t, f.db) != `[]` {
		t.Fatalf("after the addresses went away: state %v, saved %s", st, savedAddrs(t, f.db))
	}
	if st.Message != "No Tailscale address found; serving loopback only." {
		t.Fatalf("message = %q", st.Message)
	}
}

func TestServiceRenewsTheLeaf(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t, "https://192.168.1.5:9443")
	if err != nil {
		t.Fatal(err)
	}
	st := s.State()
	if st.TailscaleBound || st.Message != "Tailscale listener disabled by --listen." {
		t.Fatalf("state = %v, %q", st.TailscaleBound, st.Message)
	}
	first, err := s.leaf.GetCertificate(&tls.ClientHelloInfo{})
	if err != nil || len(first.Leaf.IPAddresses) != 1 || first.Leaf.IPAddresses[0].String() != "192.168.1.5" {
		t.Fatalf("leaf = %v, %v; want the IP SAN 192.168.1.5", first, err)
	}
	// Jump close to 60 days with the check disarmed: ticking every 10 seconds
	// through the fake clock would take minutes. check re-arms itself.
	s.mu.Lock()
	s.timer.Stop()
	s.mu.Unlock()
	f.clock.Advance(LeafRenewAfter - CheckInterval)
	s.check()
	if c, _ := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); c != first {
		t.Fatal("the leaf was renewed before 60 days")
	}
	f.clock.Advance(CheckInterval)
	if c, _ := s.leaf.GetCertificate(&tls.ClientHelloInfo{}); c == first {
		t.Fatal("the leaf was not renewed after 60 days")
	}
	if f.ifs.count() != 0 {
		t.Fatal("the interfaces were read with the tailscale listener disabled")
	}
}

func TestServiceStartFailsOnABusyAddress(t *testing.T) {
	f := newServiceFixture(t)
	f.binder.fail = map[netip.AddrPort]error{netip.MustParseAddrPort("127.0.0.1:8080"): syscall.EADDRINUSE}
	_, err := f.start(t)
	if err == nil || !strings.Contains(err.Error(), "listen: bind http://127.0.0.1:8080") {
		t.Fatalf("Start = %v, want the bind failure", err)
	}
	if f.clock.Pending() != 0 {
		t.Fatal("a failed Start left the check timer armed")
	}
}

func TestServiceShutdownStopsTheChecks(t *testing.T) {
	f := newServiceFixture(t)
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	reads := f.ifs.count()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.clock.Advance(time.Minute)
	if f.ifs.count() != reads || f.clock.Pending() != 0 || len(s.State().Listeners) != 0 {
		t.Fatal("checks or listeners outlived Shutdown")
	}
}

func TestServiceShutdownDuringChecks(t *testing.T) {
	f := newServiceFixture(t)
	f.ifs.set(true, "100.64.0.1")
	s, err := f.start(t)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := range 200 {
			f.ifs.set(true, netip.AddrFrom4([4]byte{100, 64, 0, byte(i % 4)}).String())
			f.clock.Advance(CheckInterval)
		}
	}()
	_ = s.State()
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	<-done
	if f.clock.Pending() != 0 || len(s.State().Listeners) != 0 {
		t.Fatal("a check re-armed or rebound after Shutdown")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestService' ./internal/controller/listen/`
Expected: FAIL with `undefined: Service` and `[build failed]`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/listen/service.go`:

```go
package listen

import (
	"context"
	"log/slog"
	"net/http"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// CheckInterval is how often the service reads the Tailscale addresses and
// checks the leaf's age (spec section 20).
const CheckInterval = 10 * time.Second

// Config configures Start.
type Config struct {
	Specs     []Spec // from ParseSpecs
	Hostname  string // the leaf's first DNS SAN
	DB        *store.DB
	Authority *Authority
	Clock     core.Clock
	Handler   http.Handler
	// Log receives the listener changes and the state message; nil discards.
	Log *slog.Logger
	// Interfaces lists interface addresses; nil means SystemInterfaceAddrs.
	Interfaces InterfaceAddrs
	// Bind opens the sockets; nil means Bind.
	Bind BindFunc
}

// State is what the UI footer and serve's log report.
type State struct {
	// TailscaleBound is true while at least one Tailscale address is served.
	TailscaleBound bool
	// Message is one sentence for the operator, such as "No Tailscale
	// address found; serving loopback only."
	Message   string
	Listeners []Bound
}

// Service runs the controller's listeners: it binds the endpoints of the
// specs, keeps the leaf current and, every CheckInterval, follows the
// Tailscale addresses and renews the leaf when due.
type Service struct {
	cfg   Config
	leaf  *Leaf
	set   *Set
	state atomic.Pointer[State]

	mu     sync.Mutex // serialises checks and Shutdown
	addrs  []netip.Addr
	timer  core.Timer
	closed bool
}

// Start binds the listeners and arms the periodic check. With the tailscale
// listener it first binds the addresses saved in core_meta, replaced by
// those of tailscale0 when that interface exists, so a restart serves the
// tailnet before tailscaled is up. A failed bind fails Start, except an
// IPv6 loopback address the host lacks.
func Start(ctx context.Context, cfg Config) (*Service, error) {
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	if cfg.Interfaces == nil {
		cfg.Interfaces = SystemInterfaceAddrs
	}
	s := &Service{cfg: cfg, leaf: NewLeaf(cfg.Authority, cfg.Clock, cfg.Hostname)}
	s.set = NewSet(SetConfig{Handler: cfg.Handler, TLS: s.leaf.TLSConfig(), Bind: cfg.Bind, Log: cfg.Log})
	if HasTailscale(cfg.Specs) {
		saved, err := LoadTailscaleAddrs(ctx, cfg.DB)
		if err != nil {
			return nil, err
		}
		s.addrs = saved
		s.refresh(ctx)
	}
	if err := s.apply(ctx); err != nil {
		_ = s.set.Shutdown(ctx)
		return nil, err
	}
	s.publish(true)
	s.mu.Lock()
	s.timer = cfg.Clock.AfterFunc(CheckInterval, s.check)
	s.mu.Unlock()
	return s, nil
}

// State returns the current listener state.
func (s *Service) State() State {
	st := *s.state.Load()
	st.Listeners = slices.Clone(st.Listeners)
	return st
}

// Shutdown stops the checks and the listeners, letting running requests
// finish until ctx ends.
func (s *Service) Shutdown(ctx context.Context) error {
	s.mu.Lock()
	s.closed = true
	if s.timer != nil {
		s.timer.Stop()
	}
	s.mu.Unlock()
	err := s.set.Shutdown(ctx)
	s.state.Store(&State{Message: "The listeners are shut down."})
	return err
}

func (s *Service) check() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), CheckInterval)
	defer cancel()
	if HasTailscale(s.cfg.Specs) {
		s.refresh(ctx)
	}
	if err := s.apply(ctx); err != nil {
		s.cfg.Log.Warn("listen: not every listener is bound; retrying at the next check", "err", err)
	}
	s.publish(false)
	s.timer = s.cfg.Clock.AfterFunc(CheckInterval, s.check)
}

// refresh reads tailscale0. An absent interface means tailscaled is not up,
// so the last addresses stay bound; a present one replaces them, even with
// none.
func (s *Service) refresh(ctx context.Context) {
	list, present, err := s.cfg.Interfaces(TailscaleInterface)
	if err != nil {
		s.cfg.Log.Warn("listen: cannot read the Tailscale addresses", "err", err)
		return
	}
	if !present {
		return
	}
	next := TailscaleAddrs(list)
	if slices.Equal(next, s.addrs) {
		return
	}
	s.cfg.Log.Info("listen: Tailscale addresses changed", "from", s.addrs, "to", next)
	s.addrs = next
	if err := SaveTailscaleAddrs(ctx, s.cfg.DB, next); err != nil {
		s.cfg.Log.Warn("listen: cannot save the Tailscale addresses", "err", err)
	}
}

func (s *Service) apply(ctx context.Context) error {
	eps := Endpoints(s.cfg.Specs, s.addrs)
	var ips []netip.Addr
	for _, e := range eps {
		if e.TLS {
			ips = append(ips, e.Addr.Addr())
		}
	}
	if len(ips) > 0 {
		issued, err := s.leaf.Ensure(ips)
		if err != nil {
			return err
		}
		if issued {
			s.cfg.Log.Info("listen: issued the web certificate", "hostname", s.cfg.Hostname, "ips", ips,
				"expires", s.cfg.Clock.Now().Add(LeafValidity).UTC().Format(time.RFC3339))
		}
	}
	return s.set.Update(ctx, eps)
}

// publish stores the state for State and logs its message when it changed,
// or always when force is set.
func (s *Service) publish(force bool) {
	bound := s.set.Bound()
	st := &State{Listeners: bound}
	var tsURLs []string
	loopbackOnly := true
	for _, b := range bound {
		if slices.Contains(s.addrs, b.Endpoint.Addr.Addr()) {
			tsURLs = append(tsURLs, b.Endpoint.URL())
		}
		if !b.Endpoint.Addr.Addr().IsLoopback() {
			loopbackOnly = false
		}
	}
	st.TailscaleBound = len(tsURLs) > 0
	switch {
	case !HasTailscale(s.cfg.Specs):
		st.Message = "Tailscale listener disabled by --listen."
	case st.TailscaleBound:
		st.Message = "Serving on Tailscale: " + strings.Join(tsURLs, ", ") + "."
	case len(s.addrs) > 0:
		st.Message = "Tailscale addresses found but not bound; see the serve log."
	case loopbackOnly:
		st.Message = "No Tailscale address found; serving loopback only."
	default:
		st.Message = "No Tailscale address found; not serving on the tailnet."
	}
	prev := s.state.Swap(st)
	if force || prev == nil || prev.Message != st.Message {
		s.cfg.Log.Info("listen: " + st.Message)
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run:

```bash
GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestService' -v ./internal/controller/listen/
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/listen/...
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: `TestServiceDefaultsWithoutTailscale`, `TestServiceBindsSavedAddressesBeforeTailscaled`, `TestServiceFollowsAddressChanges`, `TestServiceRenewsTheLeaf`, `TestServiceStartFailsOnABusyAddress`, `TestServiceShutdownStopsTheChecks` and `TestServiceShutdownDuringChecks` PASS; every `go test` line starts with `ok` (or `?`); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/listen/service.go internal/controller/listen/service_test.go
git commit -m "feat(listen): follow Tailscale and renew the leaf"
```


### Task 16: Scope model

**Files:**
- Create: `internal/controller/api/scope.go`
- Test: `internal/controller/api/scope_test.go`

**Interfaces:**
- Consumes: `core.Class`, `core.Side`, `core.Scope`, `core.Scope.Allows`, `core.Principal` (foundation); `errcode.Forbidden`; Contracts › Task 11 (package doc comment).
- Produces: Contracts › Task 16 (`RequiredScope`, `authorize`).

**Items:** 13

**Depends on:** Task 11

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`RequiredScope` is a pure function of an action's class and side so plan 4's registry tests can call it for every registered action. The test writes the expected scope for every class and side, including unknown values, and the scopes each principal scope satisfies, rather than deriving them from the code under test.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/scope_test.go`:

```go
package api

import (
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestRequiredScope(t *testing.T) {
	const unknownClass, unknownSide = core.Class("bogus"), core.Side("bogus")
	want := map[core.Class]map[core.Side]core.Scope{
		core.ClassRead: {
			core.SideAgent: core.ScopeRead, core.SideController: core.ScopeRead, unknownSide: core.ScopeRead,
		},
		core.ClassRevertible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassCompensable: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		core.ClassIrreversible: {
			core.SideAgent: core.ScopeOperate, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
		unknownClass: {
			core.SideAgent: core.ScopeAdmin, core.SideController: core.ScopeAdmin, unknownSide: core.ScopeAdmin,
		},
	}
	// The needs each principal scope satisfies, written out rather than
	// derived, so the test does not share the code's ordering.
	satisfies := map[core.Scope]map[core.Scope]bool{
		core.ScopeRead:    {core.ScopeRead: true},
		core.ScopeOperate: {core.ScopeRead: true, core.ScopeOperate: true},
		core.ScopeAdmin:   {core.ScopeRead: true, core.ScopeOperate: true, core.ScopeAdmin: true},
		"":                {},
		"superuser":       {},
	}
	for class, sides := range want {
		for side, need := range sides {
			if got := RequiredScope(class, side); got != need {
				t.Errorf("RequiredScope(%s, %s) = %s, want %s", class, side, got, need)
			}
			for scope, ok := range satisfies {
				err := authorize(core.Principal{Actor: "token:t1", Scope: scope}, RequiredScope(class, side))
				if ok[need] != (err == nil) {
					t.Errorf("scope %q, class %s, side %s: authorize = %v, want allowed %v", scope, class, side, err, ok[need])
				}
				var e *errcode.Error
				if err != nil && (!errors.As(err, &e) || e.Code != errcode.Forbidden || e.Detail != "this request needs the "+string(need)+" scope") {
					t.Errorf("scope %q, need %s: error = %v, want auth.forbidden naming the scope", scope, need, err)
				}
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRequiredScope' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: RequiredScope` and `undefined: authorize`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/scope.go`:

```go
package api

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// RequiredScope is the scope a principal needs for an action of class on
// side (spec section 10). Read actions need read. Mutating agent-side
// actions, the module actions, need operate for plan and apply, as their
// changes' confirm and revert do. Mutating controller-side actions, the
// core's token and module-enablement actions, need admin. An unknown class
// or side needs admin, so a mistake fails closed.
func RequiredScope(class core.Class, side core.Side) core.Scope {
	switch {
	case class == core.ClassRead:
		return core.ScopeRead
	case side == core.SideAgent &&
		(class == core.ClassRevertible || class == core.ClassCompensable || class == core.ClassIrreversible):
		return core.ScopeOperate
	default:
		return core.ScopeAdmin
	}
}

// authorize refuses p with auth.forbidden unless its scope includes need.
func authorize(p core.Principal, need core.Scope) error {
	if !p.Scope.Allows(need) {
		return errcode.New(errcode.Forbidden, fmt.Sprintf("this request needs the %s scope", need))
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRequiredScope' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/scope.go internal/controller/api/scope_test.go
git commit -m "feat(api): map action class and side to scope"
```


### Task 17: Static web UI

**Files:**
- Create: `internal/controller/api/static.go`
- Test: `internal/controller/api/static_test.go`

**Interfaces:**
- Consumes: `web.Dist` (`web/embed.go`: `//go:embed all:dist`, rooted at `dist/`); Contracts › Task 11 (package doc comment).
- Produces: Contracts › Task 17 (`EmbeddedUI`, `serveUI`, the test fixture `testUI`).

**Items:** 9

**Depends on:** Task 11

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

`serveUI` never touches the dispatcher or any credential; it only reads the UI file system. The request path is cleaned with `path.Clean` and checked with `fs.ValidPath` before it is opened, so `..` cannot leave the UI root. A directory or a missing file falls back to `index.html`; when that is missing too, the caller answers `not_found`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/static_test.go`:

```go
package api

import (
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// testUI stands in for the built web UI.
var testUI = fstest.MapFS{
	"index.html":    {Data: []byte("<!doctype html><title>Darkpanel</title>")},
	"assets/app.js": {Data: []byte("console.log(1)")},
	"theme-init.js": {Data: []byte("document.documentElement.dataset.theme='dark'")},
}

func TestServeUI(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, contentType string
	}{
		{"GET", "/", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/index.html", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets/app.js", "console.log(1)", "text/javascript; charset=utf-8"},
		{"GET", "/theme-init.js", "document.documentElement.dataset.theme='dark'", "text/javascript; charset=utf-8"},
		{"GET", "/hosts/01J/hostinfo", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/assets/missing.js", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"GET", "/../index.html", "<!doctype html><title>Darkpanel</title>", "text/html; charset=utf-8"},
		{"HEAD", "/assets/app.js", "", "text/javascript; charset=utf-8"},
	} {
		r := httptest.NewRequest(tc.method, "/", nil)
		r.URL.Path = tc.path
		rec := httptest.NewRecorder()
		if !serveUI(rec, r, testUI) {
			t.Errorf("%s %s: not served", tc.method, tc.path)
			continue
		}
		if rec.Code != http.StatusOK || rec.Body.String() != tc.body || rec.Header().Get("Content-Type") != tc.contentType {
			t.Errorf("%s %s: got %d %q %q, want 200 %q %q", tc.method, tc.path, rec.Code,
				rec.Header().Get("Content-Type"), rec.Body, tc.contentType, tc.body)
		}
	}
}

func TestServeUIWithoutIndex(t *testing.T) {
	noIndex := fstest.MapFS{"assets/app.js": {Data: []byte("x")}}
	for _, ui := range []fs.FS{noIndex, nil} {
		r := httptest.NewRequest(http.MethodGet, "/hosts", nil)
		rec := httptest.NewRecorder()
		if serveUI(rec, r, ui) {
			t.Errorf("served %q without an index.html", rec.Body)
		}
		if rec.Body.Len() != 0 || rec.Code != http.StatusOK {
			t.Errorf("wrote a response: %d %q", rec.Code, rec.Body)
		}
	}
}

func TestEmbeddedUI(t *testing.T) {
	ui := EmbeddedUI()
	st, err := fs.Stat(ui, ".")
	if err != nil || !st.IsDir() {
		t.Fatalf("Stat(.) = %v, %v; want the dist directory", st, err)
	}
	entries, err := fs.ReadDir(ui, ".")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "dist") {
			t.Errorf("EmbeddedUI is not rooted at dist: found %s", e.Name())
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestServeUI|TestEmbeddedUI' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: serveUI` and `undefined: EmbeddedUI`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/static.go`:

```go
package api

import (
	"bytes"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"path"
	"strings"

	"github.com/darkraise/darkpanel/web"
)

// EmbeddedUI returns the built web UI embedded in the binary (web.Dist),
// rooted at its dist directory: the value production passes as Config.UI.
func EmbeddedUI() fs.FS {
	ui, err := fs.Sub(web.Dist, "dist")
	if err != nil {
		panic(fmt.Sprintf("api: embedded UI: %v", err)) // "dist" is a valid path; Sub cannot fail
	}
	return ui
}

// serveUI answers a GET or HEAD outside the API with the UI file at the
// request path, or with index.html when no file is there, so client routes
// work on reload (spec section 12). It writes nothing and returns false when
// neither exists.
func serveUI(w http.ResponseWriter, r *http.Request, ui fs.FS) bool {
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	return serveFile(w, r, ui, name) || serveFile(w, r, ui, "index.html")
}

func serveFile(w http.ResponseWriter, r *http.Request, ui fs.FS, name string) bool {
	if ui == nil || !fs.ValidPath(name) {
		return false
	}
	f, err := ui.Open(name)
	if err != nil {
		return false
	}
	defer func() { _ = f.Close() }()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() {
		return false
	}
	content, ok := f.(io.ReadSeeker)
	if !ok {
		b, err := io.ReadAll(f)
		if err != nil {
			return false
		}
		content = bytes.NewReader(b)
	}
	http.ServeContent(w, r, name, st.ModTime(), content)
	return true
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestServeUI|TestEmbeddedUI' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/static.go internal/controller/api/static_test.go
git commit -m "feat(api): serve the embedded web UI"
```


### Task 18: Core module dependencies, host and module lists

**Files:**
- Modify: `internal/controller/coreactions/module.go` (whole file)
- Create: `internal/controller/coreactions/hosts.go`
- Test (whole file): `internal/controller/coreactions/module_test.go`

**Interfaces:**
- Consumes: `core.DefineRead`, `core.Env`, `core.Module`, `store.DB`, `errcode.NotFound` (foundation); `auth.New`, `auth.Params`, `auth.Options` (Tasks 1 and 4); `lifecycle.New`, `lifecycle.Config`, `schema.NewValidator`, `schema.Output`, `secret.RedactJSON`, `secret.Redacted` (plan 2); `coretest.MapHosts`, `coretest.MemAudit`, `coretest.FakeClock`, `coretest.Sealer` (foundation).
- Produces: Contracts › Task 18 (`Deps`, `Module`, `NoInput`, `Empty`, `ReasonNotReported`, `ModuleState`, `Host`, `HostList`, `ModuleListInput`, `ModuleList`, the unexported helpers and the `module_test.go` test environment).

**Items:** 3, 5

**Depends on:** none (Task 7 merged)

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

This task replaces the foundation's stub: `Deps` gains the identity service, the audit log and the module catalog, and the manifest gets its first two read actions. `Module(Deps{})` must keep registering, because `registry.Build()` calls it for the CLI and the schema generator. The test file becomes the shared test environment: every later task runs its actions through the real lifecycle engine it builds, so the production validator, redaction, confirmation phrase and state-hash recheck are what the tests see.

- [ ] **Step 1: Write the failing test**

Replace the whole content of `internal/controller/coreactions/module_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var t0 = time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC)

// fakeModule is a compiled-in module with no actions, for the core.module
// actions to manage.
type fakeModule struct {
	id      string
	version int
}

func (f fakeModule) Manifest() core.Manifest { return core.Manifest{ID: f.id, Version: f.version} }

// testEnv is the controller side of a server: a migrated controller database,
// the identity service, an in-memory audit log, the core module and a
// lifecycle engine running it.
type testEnv struct {
	t      *testing.T
	db     *store.DB
	clock  *coretest.FakeClock
	auth   *auth.Service
	audit  *coretest.MemAudit
	hosts  coretest.MapHosts
	reg    *core.Registry
	engine *lifecycle.Engine
}

// root is the controller socket's principal; break-glass actions run as it.
var root = core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: "socket"}

func newTestEnv(t *testing.T, mods ...core.Module) *testEnv {
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
	e := &testEnv{t: t, db: db, clock: coretest.NewFakeClock(t0), audit: &coretest.MemAudit{}, hosts: coretest.MapHosts{}}
	params := auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}
	e.auth = auth.New(db, coretest.Sealer{}, e.clock, e.audit, auth.Options{Params: params})
	coreModule := Module(Deps{Auth: e.auth, Audit: e.audit, Modules: mods})
	if e.reg, err = core.NewRegistry(append([]core.Module{coreModule}, mods...)...); err != nil {
		t.Fatal(err)
	}
	e.engine, err = lifecycle.New(lifecycle.Config{
		Side: core.SideController, Registry: e.reg, Validator: schema.NewValidator(), Journal: e.audit,
		Redact: redact,
		Base: core.Env{DB: db, Sealer: coretest.Sealer{}, Clock: e.clock,
			Log: slog.New(slog.DiscardHandler), Agents: e.hosts},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.engine.Close)
	return e
}

// redact is the production redaction (plan 4 wires the same function).
func redact(t reflect.Type, raw json.RawMessage) json.RawMessage {
	s, err := schema.Output(t)
	if err != nil {
		return json.RawMessage(`"` + secret.Redacted + `"`)
	}
	return secret.RedactJSON(raw, s)
}

func ref(name string) core.ActionRef { return core.ActionRef{Name: name, Version: 1} }

func encodeInput(t *testing.T, in any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// read runs a read action and decodes its output into out.
func (e *testEnv) read(p core.Principal, name string, in, out any) error {
	raw, err := e.engine.Read(context.Background(), p, ref(name), encodeInput(e.t, in))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(raw, out); err != nil {
		e.t.Fatalf("%s output %s: %v", name, raw, err)
	}
	return nil
}

func (e *testEnv) exec(query string, args ...any) {
	e.t.Helper()
	if _, err := e.db.Writer().Exec(query, args...); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) addHost(id, name string, local bool) {
	e.t.Helper()
	e.exec(`INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, ?, ?)`,
		id, name, local, t0.Format(time.RFC3339))
}

func (e *testEnv) setModuleState(hostID, moduleID string, enabled, available bool, reason string) {
	e.t.Helper()
	var r any
	if reason != "" {
		r = reason
	}
	e.exec(`INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
		VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = excluded.enabled,
		available = excluded.available, reason = excluded.reason, checked_at = excluded.checked_at`,
		hostID, moduleID, enabled, available, r, t0.Format(time.RFC3339))
}

func wantCode(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != code {
		t.Fatalf("err = %v, want code %s", err, code)
	}
}

func ptr[T any](v T) *T { return &v }

// jsonEqual reports whether got and want are the same JSON value.
func jsonEqual(t *testing.T, got json.RawMessage, want string) bool {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("got %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("want %s: %v", want, err)
	}
	return reflect.DeepEqual(g, w)
}

func TestManifest(t *testing.T) {
	m := Module(Deps{}).Manifest()
	if m.ID != core.ModuleIDCore || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("Manifest = %+v, want id core, version 1, enabled by default", m)
	}
	if m.Resources != nil || m.Migrations != nil || m.Preflight != nil {
		t.Errorf("Manifest declares resources, migrations or preflight checks: %+v", m)
	}
	if _, err := core.NewRegistry(Module(Deps{})); err != nil {
		t.Errorf("the core module with zero Deps does not register: %v", err)
	}
}

func TestHostList(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 3}, fakeModule{"extra", 2})
	e.addHost("h2", "beta", false)
	e.addHost("h1", "alpha", true)
	e.setModuleState("h1", "demo", true, true, "")
	e.setModuleState("h1", "extra", true, false, "tool missing")
	e.setModuleState("h1", "gone", true, true, "")

	var got HostList
	if err := e.read(root, "core.host.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	want := HostList{Hosts: []Host{
		{ID: "h1", Name: "alpha", IsLocal: true, Online: true, Modules: []ModuleState{
			{ID: "demo", Version: 3, Enabled: true, Available: true, CheckedAt: ptr(t0)},
			{ID: "extra", Version: 2, Enabled: true, Reason: ptr("tool missing"), CheckedAt: ptr(t0)},
		}},
		{ID: "h2", Name: "beta", Modules: []ModuleState{
			{ID: "demo", Version: 3, Reason: ptr(ReasonNotReported)},
			{ID: "extra", Version: 2, Reason: ptr(ReasonNotReported)},
		}},
	}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hosts = %+v\nwant    %+v", got, want)
	}
}

func TestHostListJSON(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 1})
	e.addHost("h1", "alpha", true)
	e.setModuleState("h1", "demo", true, true, "")
	raw, err := e.engine.Read(context.Background(), root, ref("core.host.list"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"hosts":[{"id":"h1","name":"alpha","isLocal":true,"online":true,"modules":[` +
		`{"id":"demo","version":1,"enabled":true,"available":true,"reason":null,"checkedAt":"2026-10-17T00:00:00Z"}]}]}`
	if !jsonEqual(t, raw, want) {
		t.Errorf("output = %s\nwant     %s", raw, want)
	}

	empty := newTestEnv(t)
	raw, err = empty.engine.Read(context.Background(), root, ref("core.host.list"), json.RawMessage(`{}`))
	if err != nil || string(raw) != `{"hosts":[]}` {
		t.Errorf("no hosts: output = %s, %v; want {\"hosts\":[]}", raw, err)
	}
}

func TestModuleList(t *testing.T) {
	e := newTestEnv(t, fakeModule{"demo", 3})
	e.addHost("h1", "alpha", true)
	e.setModuleState("h1", "demo", false, true, "")

	var got ModuleList
	if err := e.read(root, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	want := ModuleList{Modules: []ModuleState{{ID: "demo", Version: 3, Available: true, CheckedAt: ptr(t0)}}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("modules = %+v, want %+v", got, want)
	}

	wantCode(t, e.read(root, "core.module.list", ModuleListInput{HostID: "nope"}, &got), errcode.NotFound)
	wantCode(t, e.read(root, "core.module.list", ModuleListInput{}, &got), errcode.InvalidInput)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestManifest|TestHostList|TestHostListJSON|TestModuleList)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/module_test.go:72:28: unknown field Auth in struct literal of type Deps
internal/controller/coreactions/module_test.go:72:42: unknown field Audit in struct literal of type Deps
internal/controller/coreactions/module_test.go:72:58: unknown field Modules in struct literal of type Deps
internal/controller/coreactions/module_test.go:191:10: undefined: HostList
```

- [ ] **Step 3: Write the implementation**

Replace the whole content of `internal/controller/coreactions/module.go`:

```go
// Package coreactions is the core module (spec section 10): the host, module,
// token, audit and user actions, under module id "core". Every action is
// controller-side, and the six user actions are break-glass.
package coreactions

import (
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// Deps are the controller services the core actions call. registry.Build
// passes the zero value for callers that use only the registry's shape; a
// handler that needs a dependency it was not given fails with an internal
// error.
type Deps struct {
	// Auth is the identity service over the controller database.
	Auth *auth.Service
	// Audit is the controller's audit log, read by core.audit.list.
	Audit core.Audit
	// Modules are the compiled-in modules whose enablement the core.module
	// actions manage, in registration order; the server passes modules.All().
	Modules []core.Module
}

// Module returns the core module.
func Module(d Deps) core.Module {
	m := module{d: d}
	for _, mod := range d.Modules {
		man := mod.Manifest()
		m.catalog = append(m.catalog, moduleInfo{id: man.ID, version: man.Version})
	}
	return m
}

type moduleInfo struct {
	id      string
	version int
}

type module struct {
	d       Deps
	catalog []moduleInfo
}

// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}

// NoInput is the input of an action that takes none.
type NoInput struct{}

// Empty is the output of an action that returns nothing.
type Empty struct{}
```

Create `internal/controller/coreactions/hosts.go`:

```go
package coreactions

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ReasonNotReported is the reason of a compiled-in module that has no
// core_module_state row for a host yet.
const ReasonNotReported = "not reported by the host yet"

// ModuleState is one module's enablement on one host (spec section 12). A
// module can be used only when it is both enabled and available.
type ModuleState struct {
	ID        string     `json:"id" jsonschema_description:"Module id."`
	Version   int        `json:"version" jsonschema_description:"Module version compiled into this binary."`
	Enabled   bool       `json:"enabled" jsonschema_description:"Whether an operator enabled the module on the host."`
	Available bool       `json:"available" jsonschema_description:"Whether the module's migrations and preflight checks passed on the host."`
	Reason    *string    `json:"reason" jsonschema_description:"Why the module is unavailable; null when it is available."`
	CheckedAt *time.Time `json:"checkedAt" jsonschema_description:"When availability was last checked; null when it never was."`
}

// Host is a managed host (spec section 12).
type Host struct {
	ID      string        `json:"id" jsonschema_description:"Host id."`
	Name    string        `json:"name" jsonschema_description:"Host name."`
	IsLocal bool          `json:"isLocal" jsonschema_description:"Whether the host runs this controller."`
	Online  bool          `json:"online" jsonschema_description:"Whether the host's agent is reachable; always true for the local host in M1."`
	Modules []ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host."`
}

// HostList is the output of core.host.list/v1.
type HostList struct {
	Hosts []Host `json:"hosts" jsonschema_description:"Every host, the local host first, then by name."`
}

// ModuleListInput is the input of core.module.list/v1.
type ModuleListInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host whose modules to list."`
}

// ModuleList is the output of core.module.list/v1.
type ModuleList struct {
	Modules []ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host, in registration order."`
}

func (m module) hostActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, HostList](core.ActionSpec{
			Name: "core.host.list", Version: 1, Side: core.SideController,
			Summary: "List the managed hosts with the state of every module on each.",
		}, hostList{m}),
		core.DefineRead[ModuleListInput, ModuleList](core.ActionSpec{
			Name: "core.module.list", Version: 1, Side: core.SideController,
			Summary: "List the state of every compiled-in module on one host.",
		}, moduleList{m}),
	}
}

type hostList struct{ m module }

func (h hostList) Read(ctx context.Context, env core.Env, _ NoInput) (HostList, error) {
	rows, err := env.DB.Read().QueryContext(ctx,
		`SELECT id, name, is_local FROM core_hosts ORDER BY is_local DESC, name, id`)
	if err != nil {
		return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	out := HostList{Hosts: []Host{}}
	for rows.Next() {
		var host Host
		if err := rows.Scan(&host.ID, &host.Name, &host.IsLocal); err != nil {
			_ = rows.Close()
			return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
		}
		host.Online = host.IsLocal
		out.Hosts = append(out.Hosts, host)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return HostList{}, fmt.Errorf("coreactions: list hosts: %w", err)
	}
	for i := range out.Hosts {
		if out.Hosts[i].Modules, err = h.m.moduleStates(ctx, env.DB.Read(), out.Hosts[i].ID); err != nil {
			return HostList{}, err
		}
	}
	return out, nil
}

type moduleList struct{ m module }

func (h moduleList) Read(ctx context.Context, env core.Env, in ModuleListInput) (ModuleList, error) {
	if err := hostExists(ctx, env.DB.Read(), in.HostID); err != nil {
		return ModuleList{}, err
	}
	states, err := h.m.moduleStates(ctx, env.DB.Read(), in.HostID)
	if err != nil {
		return ModuleList{}, err
	}
	return ModuleList{Modules: states}, nil
}

// querier is a *sql.DB or a *sql.Tx.
type querier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// hostExists returns not_found for a host id core_hosts does not hold.
func hostExists(ctx context.Context, q querier, hostID string) error {
	var n int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM core_hosts WHERE id = ?`, hostID).Scan(&n); err != nil {
		return fmt.Errorf("coreactions: read host: %w", err)
	}
	if n == 0 {
		return errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	}
	return nil
}

// moduleStates returns the state of every compiled-in module on hostID, in
// registration order. Rows of modules this binary does not have are left out.
func (m module) moduleStates(ctx context.Context, q querier, hostID string) ([]ModuleState, error) {
	rows, err := q.QueryContext(ctx,
		`SELECT module_id, enabled, available, reason, checked_at FROM core_module_state WHERE host_id = ?`, hostID)
	if err != nil {
		return nil, fmt.Errorf("coreactions: read module state: %w", err)
	}
	stored := map[string]ModuleState{}
	for rows.Next() {
		var s ModuleState
		var reason sql.NullString
		var checked string
		if err := rows.Scan(&s.ID, &s.Enabled, &s.Available, &reason, &checked); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("coreactions: read module state: %w", err)
		}
		if reason.Valid && !s.Available {
			s.Reason = &reason.String
		}
		at, err := parseTime(checked)
		if err != nil {
			_ = rows.Close()
			return nil, err
		}
		s.CheckedAt = &at
		stored[s.ID] = s
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, fmt.Errorf("coreactions: read module state: %w", err)
	}
	out := make([]ModuleState, 0, len(m.catalog))
	for _, info := range m.catalog {
		s, ok := stored[info.id]
		if !ok {
			reason := ReasonNotReported
			s = ModuleState{ID: info.id, Reason: &reason}
		}
		s.Version = info.version
		out = append(out, s)
	}
	return out, nil
}

func parseTime(s string) (time.Time, error) {
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("coreactions: parse time %q: %w", s, err)
	}
	return t.UTC(), nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestManifest|TestHostList|TestHostListJSON|TestModuleList)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/hosts.go \
  internal/controller/coreactions/module_test.go
git commit -m "feat(coreactions): list hosts and module states"
```


### Task 19: Server, authentication middleware and CSRF

**Files:**
- Create: `internal/controller/api/server.go`
- Create: `internal/controller/api/routes.go`
- Test: `internal/controller/api/server_test.go`

**Interfaces:**
- Consumes: Contracts › Task 11 (`writeProblem`, `securityHeaders`, `recoverPanics`, `isAPIPath`, `healthzPath`, `testLog`, `decodeProblem`) and Task 17 (`serveUI`, `testUI`); Task 7 (`auth.Service.Login`, `Logout`, `Authenticate`, `auth.Credentials`, `auth.Identity`, `auth.LoginRequest`, `auth.LoginResult`), Task 5 (`CSRFToken`, `VerifyCSRF`), Task 4 (`auth.New`, `CreateUser`, `ActivateUser`, `auth.Options`), Task 6 (`CreateToken`), Task 2 (`TOTPCode`, `TOTPPeriod`), Task 1 (`auth.Params`); `store.Open`, `store.Migrate`, `store.CoreMigrations`, `coretest.FakeClock`, `coretest.MemAudit`, `coretest.Sealer`, `coretest.FakeDispatcher`, `core.NewRegistry` and the `core.Define*` constructors (foundation); `schema.Route` (plan 2).
- Produces: Contracts › Task 19 (`SessionCookieSecure`, `SessionCookiePlain`, `CSRFHeader`, `Authenticator`, `WebCA`, `Config`, `Server`, `New`, `Server.Handler`, `Routes`, the unexported middleware and fallback names, and the `server_test.go` harness).

**Items:** 12

**Depends on:** Tasks 11 and 17

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Two independent layers keep a protected handler from running without credentials. The outer one is the single authentication middleware: `routeAccess` names the public list of spec section 12 from the method and the decoded path, and everything else needs valid credentials (plus a matching CSRF token for a session on an unsafe method). The inner one is `requireCaller`, which wraps every non-public route, and the fallback's own caller check, so a mistake in `routeAccess` alone exposes nothing. `not_found` and `method_not_allowed` are answered only after authentication. The route list starts empty; Tasks 20, 21 and 22 replace `routes.go` as they add routes.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/server_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

var testStart = time.Date(2026, 10, 17, 12, 0, 0, 0, time.UTC)

const (
	testPassword = "correct horse battery"
	testHostID   = "01HOST0000000000000000000A"
	testRemote   = "192.0.2.10:51234"
)

// testParams keep argon2id cheap in tests.
var testParams = auth.Params{Memory: 8, Iterations: 1, Parallelism: 1, SaltLen: 16, KeyLen: 32}

type demoInput struct {
	Name string `json:"name" jsonschema:"pattern=^[a-z]+$"`
}

type demoOutput struct {
	OK bool `json:"ok"`
}

type demoStatus struct {
	Healthy bool `json:"healthy"`
}

// demoHandler implements every handler interface and is never called: the
// tests' dispatcher is a fake.
type demoHandler[In, Out any] struct{}

func (demoHandler[In, Out]) Read(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (demoHandler[In, Out]) Plan(context.Context, core.Env, In) (core.PlanOutcome, error) {
	return core.PlanOutcome{}, nil
}

func (demoHandler[In, Out]) Apply(context.Context, core.Env, In) (Out, error) {
	var out Out
	return out, nil
}

func (demoHandler[In, Out]) Verify(context.Context, core.Env, In, Out) error { return nil }

func (demoHandler[In, Out]) Snapshot(context.Context, core.Env, In) ([]byte, error) { return nil, nil }

func (demoHandler[In, Out]) Revert(context.Context, core.Env, []byte) error { return nil }

// demoModule has an agent-side read, revertible and irreversible action, a
// controller-side read and compensable action, two break-glass actions and
// a resource.
type demoModule struct{}

func (demoModule) Manifest() core.Manifest {
	h := demoHandler[demoInput, demoOutput]{}
	return core.Manifest{
		ID: "demo", Version: 1,
		Actions: []core.ActionDef{
			core.DefineRead(core.ActionSpec{Name: "demo.item.get", Version: 1, Side: core.SideAgent}, h),
			core.DefineRevertible(core.ActionSpec{Name: "demo.item.add", Version: 1, Side: core.SideAgent, LockKeys: []string{"demo"}}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.purge", Version: 1, Class: core.ClassIrreversible, Side: core.SideAgent}, h),
			core.DefineRead(core.ActionSpec{Name: "demo.item.list", Version: 1, Side: core.SideController}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.item.remove", Version: 1, Class: core.ClassCompensable,
				Side: core.SideController, CompensatedBy: "demo.item.add"}, h),
			core.DefineMutating(core.ActionSpec{Name: "demo.secret.reset", Version: 1, Class: core.ClassIrreversible,
				Side: core.SideController, BreakGlass: true}, h),
			core.DefineRead(core.ActionSpec{Name: "demo.secret.show", Version: 1, Side: core.SideController, BreakGlass: true}, h),
		},
		Resources: []core.ResourceDef{
			core.DefineResource("demo.status", func(context.Context, core.Env) (demoStatus, error) {
				return demoStatus{}, nil
			}),
		},
	}
}

type fakeWebCA struct{}

func (fakeWebCA) RootPEM() []byte {
	return []byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n")
}

func (fakeWebCA) Fingerprint() string { return strings.Repeat("ab", 32) }

// harness is a server over a real auth service with one active admin, a
// fake dispatcher and demoModule's registry.
type harness struct {
	t      *testing.T
	srv    *Server
	auth   *auth.Service
	clock  *coretest.FakeClock
	audit  *coretest.MemAudit
	disp   *coretest.FakeDispatcher
	user   auth.User
	secret string
	log    *bytes.Buffer
}

func newHarness(t *testing.T) *harness {
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
		BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: testStart}
	if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
		t.Fatal(err)
	}
	h := &harness{t: t, clock: coretest.NewFakeClock(testStart), audit: &coretest.MemAudit{}, disp: &coretest.FakeDispatcher{}}
	h.auth = auth.New(db, coretest.Sealer{}, h.clock, h.audit, auth.Options{Params: testParams})
	if _, h.secret, err = h.auth.CreateUser(ctx, "admin", testPassword); err != nil {
		t.Fatal(err)
	}
	if h.user, err = h.auth.ActivateUser(ctx, "admin", h.code()); err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	log, buf := testLog()
	h.log = buf
	h.srv, err = New(Config{
		Dispatcher:    h.disp,
		Auth:          h.auth,
		Registry:      reg,
		WebCA:         fakeWebCA{},
		SchemaVersion: func(context.Context) (int, error) { return 3, nil },
		HostID:        testHostID,
		UI:            testUI,
		Log:           log,
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// code advances the clock one TOTP period, so no code is used twice, and
// returns the admin's current code.
func (h *harness) code() string {
	h.t.Helper()
	h.clock.Advance(auth.TOTPPeriod)
	code, err := auth.TOTPCode(h.secret, h.clock.Now())
	if err != nil {
		h.t.Fatal(err)
	}
	return code
}

// login opens a session for the admin and returns its id and CSRF token.
func (h *harness) login() (session, csrf string) {
	h.t.Helper()
	res, err := h.auth.Login(context.Background(), auth.LoginRequest{
		Username: "admin", Password: testPassword, Code: h.code(), Source: "192.0.2.10"})
	if err != nil {
		h.t.Fatal(err)
	}
	return res.SessionID, res.CSRFToken
}

// token creates an API token of the admin with scope.
func (h *harness) token(scope core.Scope) string {
	h.t.Helper()
	_, tok, err := h.auth.CreateToken(context.Background(), h.user.ID, "test", scope, 0)
	if err != nil {
		h.t.Fatal(err)
	}
	return tok
}

func (h *harness) serve(r *http.Request) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.srv.Handler().ServeHTTP(rec, r)
	return rec
}

// request builds a request from testRemote; a non-empty body is sent as
// JSON.
func request(method, target, body string) *http.Request {
	var rd io.Reader
	if body != "" {
		rd = strings.NewReader(body)
	}
	r := httptest.NewRequest(method, target, rd)
	r.RemoteAddr = testRemote
	if body != "" {
		r.Header.Set("Content-Type", "application/json")
	}
	return r
}

// withSession adds the plain listener's session cookie and, when csrf is
// set, the CSRF header.
func withSession(r *http.Request, session, csrf string) *http.Request {
	r.Header.Add("Cookie", SessionCookiePlain+"="+session)
	if csrf != "" {
		r.Header.Set(CSRFHeader, csrf)
	}
	return r
}

func withToken(r *http.Request, token string) *http.Request {
	r.Header.Set("Authorization", "Bearer "+token)
	return r
}

// probe records whether it ran and with which caller.
type probe struct {
	ran    bool
	caller caller
	found  bool
}

func (p *probe) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.ran = true
	p.caller, p.found = callerFrom(r.Context())
	w.WriteHeader(http.StatusNoContent)
}

func (h *harness) through(r *http.Request) (*httptest.ResponseRecorder, *probe) {
	p := &probe{}
	rec := httptest.NewRecorder()
	h.srv.authenticate(p).ServeHTTP(rec, r)
	return rec, p
}

var allMethods = []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions}

func TestRouteAccess(t *testing.T) {
	for _, tc := range []struct {
		method, path string
		want         access
	}{
		{"POST", "/api/v1/auth/login", accessPublic},
		{"GET", "/api/v1/auth/login", accessAuthenticated},
		{"GET", "/healthz", accessOptional},
		{"HEAD", "/healthz", accessOptional},
		{"POST", "/healthz", accessAuthenticated},
		{"GET", "/", accessPublic},
		{"HEAD", "/assets/app.js", accessPublic},
		{"GET", "/hosts/01J/hostinfo", accessPublic},
		{"POST", "/hosts", accessAuthenticated},
		{"OPTIONS", "/", accessAuthenticated},
		{"GET", "/api", accessAuthenticated},
		{"GET", "/api/v1/openapi.json", accessAuthenticated},
		{"GET", "/api/v1/changes", accessAuthenticated},
		{"POST", "/api/v1/auth/login/", accessAuthenticated},
	} {
		if got := routeAccess(tc.method, tc.path); got != tc.want {
			t.Errorf("routeAccess(%s, %s) = %d, want %d", tc.method, tc.path, got, tc.want)
		}
	}
}

func TestAuthenticateDeniesWithoutCredentials(t *testing.T) {
	h := newHarness(t)
	for _, path := range []string{"/api/v1/changes", "/api/v1/nope", "/api", "/hosts"} {
		for _, m := range allMethods {
			if routeAccess(m, path) != accessAuthenticated {
				continue
			}
			rec, p := h.through(request(m, path, ""))
			if p.ran {
				t.Errorf("%s %s reached the handler without credentials", m, path)
			}
			decodeProblem(t, rec, 401, errcode.AuthRequired)
		}
	}
}

func TestAuthenticatePublicRoutes(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/v1/auth/login"},
		{"GET", "/"},
		{"HEAD", "/assets/app.js"},
	} {
		for name, r := range map[string]*http.Request{
			"no credentials":  request(tc.method, tc.path, ""),
			"invalid bearer":  withToken(request(tc.method, tc.path, ""), "dpt_bogus"),
			"a valid session": withSession(request(tc.method, tc.path, ""), session, ""),
		} {
			rec, p := h.through(r)
			if !p.ran || p.found || rec.Code != http.StatusNoContent {
				t.Errorf("%s %s with %s: ran %v, caller %v, status %d; want served without a caller",
					tc.method, tc.path, name, p.ran, p.found, rec.Code)
			}
		}
	}
}

func TestAuthenticateHealthzIgnoresInvalidCredentials(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	token := h.token(core.ScopeRead)
	for _, tc := range []struct {
		name   string
		r      *http.Request
		caller string
	}{
		{"no credentials", request("GET", "/healthz", ""), ""},
		{"invalid bearer", withToken(request("GET", "/healthz", ""), "dpt_bogus"), ""},
		{"invalid session", withSession(request("HEAD", "/healthz", ""), "bogus", ""), ""},
		{"valid token", withToken(request("GET", "/healthz", ""), token), "token:"},
		{"valid session", withSession(request("GET", "/healthz", ""), session, ""), "user:" + h.user.ID},
	} {
		rec, p := h.through(tc.r)
		if !p.ran || rec.Code != http.StatusNoContent {
			t.Errorf("%s: ran %v, status %d; want served", tc.name, p.ran, rec.Code)
			continue
		}
		if tc.caller == "" && p.found {
			t.Errorf("%s: caller %+v, want none", tc.name, p.caller.principal)
		}
		if tc.caller != "" && (!p.found || !strings.HasPrefix(p.caller.principal.Actor, tc.caller)) {
			t.Errorf("%s: caller %+v, want actor %s...", tc.name, p.caller.principal, tc.caller)
		}
	}
}

func TestCredentialPrecedence(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	token := h.token(core.ScopeOperate)

	rec, p := h.through(withSession(withToken(request("GET", "/api/v1/changes", ""), "dpt_bogus"), session, csrf))
	if p.ran {
		t.Error("an invalid bearer token fell back to the session cookie")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	r := withSession(request("GET", "/api/v1/changes", ""), session, csrf)
	r.Header.Add("Authorization", "Bearer "+token)
	r.Header.Add("Authorization", "Bearer "+token)
	rec, p = h.through(r)
	if p.ran {
		t.Error("two Authorization values were accepted")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	_, p = h.through(withSession(withToken(request("GET", "/api/v1/changes", ""), token), "bogus", ""))
	if !p.found || !strings.HasPrefix(p.caller.principal.Actor, "token:") || p.caller.principal.Scope != core.ScopeOperate ||
		p.caller.identity.FromSession || p.caller.session != "" {
		t.Errorf("a valid token with a stale cookie: caller %+v, want the token", p.caller)
	}
}

func TestCSRF(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	token := h.token(core.ScopeAdmin)
	for _, m := range allMethods {
		safe := m == "GET" || m == "HEAD" || m == "OPTIONS"
		for _, tc := range []struct {
			name string
			r    *http.Request
			ok   bool
		}{
			{"session without token", withSession(request(m, "/api/v1/changes", ""), session, ""), safe},
			{"session with a wrong token", withSession(request(m, "/api/v1/changes", ""), session, csrf+"x"), safe},
			{"session with another session's token", withSession(request(m, "/api/v1/changes", ""), session, h.auth.CSRFToken(strings.Repeat("A", 43))), safe},
			{"session with its token", withSession(request(m, "/api/v1/changes", ""), session, csrf), true},
			{"bearer token without CSRF", withToken(request(m, "/api/v1/changes", ""), token), true},
		} {
			rec, p := h.through(tc.r)
			if p.ran != tc.ok {
				t.Errorf("%s %s: ran %v, want %v (status %d)", m, tc.name, p.ran, tc.ok, rec.Code)
			}
			if !tc.ok {
				decodeProblem(t, rec, 403, errcode.CSRF)
			}
			if tc.ok && p.caller.identity.FromSession && p.caller.session != session {
				t.Errorf("%s %s: caller session %q, want the cookie's value", m, tc.name, p.caller.session)
			}
		}
	}
}

func TestSessionCookiePerListener(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	for _, tc := range []struct {
		name   string
		tls    bool
		cookie string
		ok     bool
	}{
		{"plain listener, plain cookie", false, SessionCookiePlain, true},
		{"plain listener, secure cookie", false, SessionCookieSecure, false},
		{"HTTPS listener, secure cookie", true, SessionCookieSecure, true},
		{"HTTPS listener, plain cookie", true, SessionCookiePlain, false},
	} {
		r := request("GET", "/api/v1/changes", "")
		if tc.tls {
			r.TLS = &tls.ConnectionState{}
		}
		r.Header.Add("Cookie", tc.cookie+"="+session)
		rec, p := h.through(r)
		if p.ran != tc.ok {
			t.Errorf("%s: ran %v, want %v (status %d)", tc.name, p.ran, tc.ok, rec.Code)
		}
	}
}

func TestSourceAddress(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)
	for remote, want := range map[string]string{
		"192.0.2.10:51234":   "192.0.2.10",
		"[2001:db8::1]:8443": "2001:db8::1",
		"@":                  "@",
	} {
		r := withToken(request("GET", "/api/v1/changes", ""), token)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-For", "203.0.113.9")
		_, p := h.through(r)
		if p.caller.principal.Source != want {
			t.Errorf("RemoteAddr %q: source %q, want %q", remote, p.caller.principal.Source, want)
		}
	}
}

func TestRequireCaller(t *testing.T) {
	log, _ := testLog()
	p := &probe{}
	rec := httptest.NewRecorder()
	requireCaller(log, p).ServeHTTP(rec, request("GET", "/api/v1/changes", ""))
	if p.ran {
		t.Error("requireCaller ran the handler without a caller")
	}
	decodeProblem(t, rec, 401, errcode.AuthRequired)

	r := request("GET", "/api/v1/changes", "")
	r = r.WithContext(withCaller(r.Context(), caller{principal: core.Principal{Actor: "token:t1", Scope: core.ScopeRead}}))
	requireCaller(log, p).ServeHTTP(httptest.NewRecorder(), r)
	if !p.ran {
		t.Error("requireCaller refused a request with a caller")
	}
}

func TestFallback(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)

	rec := h.serve(request("GET", "/api/v1/nope", ""))
	decodeProblem(t, rec, 401, errcode.AuthRequired)
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "no-store" {
		t.Errorf("refusal lacks the security headers: %v", rec.Header())
	}
	decodeProblem(t, h.serve(request("POST", "/hosts", "")), 401, errcode.AuthRequired)

	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/nope", ""), token)), 404, errcode.NotFound)
	rec = h.serve(withToken(request("DELETE", "/hosts", ""), token))
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}

	rec = h.serve(request("GET", "/hosts/01J/hostinfo", ""))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>Darkpanel</title>") {
		t.Errorf("UI fallback: %d %s", rec.Code, rec.Body)
	}
	if rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("Cache-Control") != "" {
		t.Errorf("UI response headers: %v", rec.Header())
	}

	h.srv.cfg.UI = nil
	decodeProblem(t, h.serve(request("GET", "/hosts", "")), 404, errcode.NotFound)
	decodeProblem(t, h.serve(withToken(request("DELETE", "/hosts", ""), token)), 404, errcode.NotFound)
}

func TestNewRequiresDependencies(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Error("New accepted an empty Config")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRouteAccess|TestAuthenticate|TestCredentialPrecedence|TestCSRF|TestSessionCookiePerListener|TestSourceAddress|TestRequireCaller|TestFallback|TestNewRequiresDependencies' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: Server`, `undefined: New`, `undefined: Config`, `undefined: SessionCookiePlain`, `undefined: CSRFHeader` (and more).

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/server.go`:

```go
package api

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// Session cookie names and the CSRF header (spec section 9). HTTPS
// listeners use the __Host- cookie with Secure; the plain-HTTP loopback
// listener uses the other name without Secure.
const (
	SessionCookieSecure = "__Host-darkpanel_session"
	SessionCookiePlain  = "darkpanel_session"
	CSRFHeader          = "X-CSRF-Token"
)

const loginPath = "/api/v1/auth/login"

// Authenticator is the part of *auth.Service the API calls.
type Authenticator interface {
	Login(ctx context.Context, r auth.LoginRequest) (auth.LoginResult, error)
	Logout(ctx context.Context, sessionID, source string) error
	Authenticate(ctx context.Context, c auth.Credentials) (auth.Identity, error)
	CSRFToken(sessionID string) string
	VerifyCSRF(sessionID, token string) bool
}

var _ Authenticator = (*auth.Service)(nil)

// WebCA is the web certificate root that the controller socket serves. Its
// method set is listen.WebCA's.
type WebCA interface {
	// RootPEM is the root certificate as one PEM CERTIFICATE block.
	RootPEM() []byte
	// Fingerprint is the lowercase hex SHA-256 of the root's DER encoding.
	Fingerprint() string
}

// Config configures New. Every field but UI and Log is required.
type Config struct {
	Dispatcher core.Dispatcher
	Auth       Authenticator
	Registry   *core.Registry
	WebCA      WebCA
	// SchemaVersion returns the highest applied core migration of the
	// controller database, which /healthz reports.
	SchemaVersion func(ctx context.Context) (int, error)
	// HostID is the controller host's id, part of the socket actors.
	HostID string
	// Dev accepts the service's own non-root uid on the controller socket.
	Dev bool
	// UI is the web UI served at /; nil serves none.
	UI fs.FS
	// Log receives internal errors and panics; nil discards.
	Log *slog.Logger
}

// Server serves the API. Its handlers are safe for concurrent use.
type Server struct {
	cfg     Config
	log     *slog.Logger
	mux     *http.ServeMux
	handler http.Handler
}

// New returns a server for cfg.
func New(cfg Config) (*Server, error) {
	if cfg.Dispatcher == nil || cfg.Auth == nil || cfg.Registry == nil || cfg.WebCA == nil ||
		cfg.SchemaVersion == nil || cfg.HostID == "" {
		return nil, errors.New("api: Config needs Dispatcher, Auth, Registry, WebCA, SchemaVersion and HostID")
	}
	if cfg.Log == nil {
		cfg.Log = slog.New(slog.DiscardHandler)
	}
	s := &Server{cfg: cfg, log: cfg.Log, mux: http.NewServeMux()}
	for _, rt := range s.networkRoutes() {
		h := rt.handler
		if !rt.public {
			h = requireCaller(s.log, h)
		}
		s.mux.Handle(rt.method+" "+rt.path, h)
	}
	s.mux.HandleFunc("/", s.networkFallback)
	s.handler = securityHeaders(recoverPanics(s.log, s.authenticate(s.mux)))
	return s, nil
}

// Handler serves the network listeners. A request is HTTPS exactly when
// Request.TLS is set, which picks the session cookie and HSTS.
func (s *Server) Handler() http.Handler { return s.handler }

// caller is the authenticated party of a request.
type caller struct {
	principal core.Principal
	identity  auth.Identity // zero on the controller socket
	session   string        // the session cookie's value, for a session principal only
}

type callerKey struct{}

func withCaller(ctx context.Context, c caller) context.Context {
	return context.WithValue(ctx, callerKey{}, c)
}

func callerFrom(ctx context.Context) (caller, bool) {
	c, ok := ctx.Value(callerKey{}).(caller)
	return c, ok
}

// sessionCookieName is the session cookie of the listener r arrived on.
func sessionCookieName(r *http.Request) string {
	if r.TLS != nil {
		return SessionCookieSecure
	}
	return SessionCookiePlain
}

// clientAddr is the host part of the peer address. Forwarded headers are
// ignored: no proxy is trusted in M1.
func clientAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

type access int

const (
	accessAuthenticated access = iota // credentials required
	accessPublic                      // credentials ignored
	accessOptional                    // valid credentials used, invalid ones ignored
)

// routeAccess is the public list of spec section 12: login, /healthz, and
// GET or HEAD of the UI's files and index fallback. Everything else, on any
// method and any path, needs credentials.
func routeAccess(method, path string) access {
	get := method == http.MethodGet || method == http.MethodHead
	switch {
	case method == http.MethodPost && path == loginPath:
		return accessPublic
	case get && path == healthzPath:
		return accessOptional
	case get && !isAPIPath(path):
		return accessPublic
	default:
		return accessAuthenticated
	}
}

func safeMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead || method == http.MethodOptions
}

// identify authenticates r. When an Authorization header is present only
// its bearer token counts and the cookie is never read (spec section 9).
func (s *Server) identify(r *http.Request) (caller, error) {
	var session string
	if ck, err := r.Cookie(sessionCookieName(r)); err == nil {
		session = ck.Value
	}
	id, err := s.cfg.Auth.Authenticate(r.Context(), auth.Credentials{
		Authorization: r.Header.Values("Authorization"),
		Session:       session,
		Source:        clientAddr(r),
	})
	if err != nil {
		return caller{}, err
	}
	c := caller{principal: id.Principal, identity: id}
	if id.FromSession {
		c.session = session
	}
	return c, nil
}

// authenticate is the one authentication middleware of the network
// listeners: it denies every request whose route is not public unless it
// carries valid credentials, and a session request with an unsafe method
// unless its CSRF token matches.
func (s *Server) authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch routeAccess(r.Method, r.URL.Path) {
		case accessPublic:
			next.ServeHTTP(w, r)
			return
		case accessOptional:
			if c, err := s.identify(r); err == nil {
				r = r.WithContext(withCaller(r.Context(), c))
			}
			next.ServeHTTP(w, r)
			return
		}
		c, err := s.identify(r)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		if c.identity.FromSession && !safeMethod(r.Method) && !s.cfg.Auth.VerifyCSRF(c.session, r.Header.Get(CSRFHeader)) {
			writeProblem(w, s.log, errcode.New(errcode.CSRF, "missing or invalid "+CSRFHeader+" header"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withCaller(r.Context(), c)))
	})
}

// requireCaller refuses a request that reached a protected handler without
// a caller. authenticate already guarantees one; this keeps a mistake in
// routeAccess from exposing a handler.
func requireCaller(log *slog.Logger, next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := callerFrom(r.Context()); !ok {
			writeProblem(w, log, errcode.New(errcode.AuthRequired, "authentication required"))
			return
		}
		next.ServeHTTP(w, r)
	}
}

// networkFallback serves the UI for GET and HEAD outside the API, and
// answers not_found or method_not_allowed for everything else that no route
// matched, only once the request is authenticated.
func (s *Server) networkFallback(w http.ResponseWriter, r *http.Request) {
	if routeAccess(r.Method, r.URL.Path) == accessPublic && !isAPIPath(r.URL.Path) {
		if !serveUI(w, r, s.cfg.UI) {
			writeProblem(w, s.log, errcode.New(errcode.NotFound, "no such page"))
		}
		return
	}
	if _, ok := callerFrom(r.Context()); !ok {
		writeProblem(w, s.log, errcode.New(errcode.AuthRequired, "authentication required"))
		return
	}
	s.unrouted(w, r, s.mux, s.cfg.UI != nil)
}

// unrouted answers a request no route of mux matched: method_not_allowed
// with Allow when another method has a route at the path, else not_found.
// With ui, every path outside the API allows GET and HEAD.
func (s *Server) unrouted(w http.ResponseWriter, r *http.Request, mux *http.ServeMux, ui bool) {
	allow := []string{http.MethodGet, http.MethodHead}
	if !ui || isAPIPath(r.URL.Path) {
		allow = allowedMethods(mux, r)
	}
	if len(allow) == 0 {
		writeProblem(w, s.log, errcode.New(errcode.NotFound, "no such route"))
		return
	}
	w.Header().Set("Allow", strings.Join(allow, ", "))
	writeProblem(w, s.log, errcode.New(errcode.MethodNotAllowed, r.Method+" is not allowed here"))
}

// allowedMethods lists the methods that a route other than the fallback
// serves at r's path.
func allowedMethods(mux *http.ServeMux, r *http.Request) []string {
	var allow []string
	for _, m := range []string{http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		probe := r.Clone(r.Context())
		probe.Method = m
		if _, pattern := mux.Handler(probe); pattern != "" && pattern != "/" {
			allow = append(allow, m)
		}
	}
	return allow
}
```

Create `internal/controller/api/routes.go`:

```go
package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// route is one route of the network mux. A public route must also be public
// in routeAccess, which the authentication middleware consults.
type route struct {
	method, path, summary string
	public                bool
	handler               http.HandlerFunc
}

// networkRoutes lists the network mux's routes.
func (s *Server) networkRoutes() []route {
	return []route{}
}

// Routes returns the hand-written network routes that schema.OpenAPI
// documents next to the registry's actions and resources.
func Routes() []schema.Route {
	out := []schema.Route{}
	for _, rt := range (&Server{}).networkRoutes() {
		out = append(out, schema.Route{Method: rt.method, Path: rt.path, Summary: rt.summary, Public: rt.public})
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestRouteAccess|TestAuthenticate|TestCredentialPrecedence|TestCSRF|TestSessionCookiePerListener|TestSourceAddress|TestRequireCaller|TestFallback|TestNewRequiresDependencies' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/server.go internal/controller/api/routes.go internal/controller/api/server_test.go
git commit -m "feat(api): authenticate every non-public request"
```


### Task 20: Login, logout and session routes

**Files:**
- Create: `internal/controller/api/auth.go`
- Modify: `internal/controller/api/routes.go` (the step gives the whole new file)
- Test: `internal/controller/api/auth_test.go`

**Interfaces:**
- Consumes: Contracts › Task 11 (`decodeBody`, `writeJSON`, `writeProblem`) and Task 19 (`Server`, `callerFrom`, `caller`, `sessionCookieName`, `clientAddr`, `loginPath`, `route`, `networkRoutes`, the harness); Task 7 (`Login`, `Logout`, `LoginRequest`, `AuditLoginAction`, `AuditLogoutAction`), Task 5 (`CSRFToken`), Task 3 (`UsernameFailureLimit`, `LockDuration`), Task 4 (`auth.User`), Task 6 (`auth.TokenInfo`).
- Produces: Contracts › Task 20 (`POST /api/v1/auth/login`, `POST /api/v1/auth/logout`, `GET /api/v1/auth/session`, `sessionResponse`, `sessionCookie`).

**Items:** 12

**Depends on:** Tasks 11 and 19

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The cookie is named for the listener the request arrived on and is `Secure` exactly on HTTPS (spec section 9); it has no expiry, because the server ends the session. P5's service audits logins and logouts, so these handlers add no audit of their own. The gosec `G124` finding on the cookie is suppressed with its reason: the plain-HTTP loopback listener's cookie cannot be `Secure`.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/auth_test.go`:

```go
package api

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func loginBody(username, password, code string) string {
	b, _ := json.Marshal(map[string]string{"username": username, "password": password, "code": code})
	return string(b)
}

// sessionCookieOf returns the session cookie rec set.
func sessionCookieOf(t *testing.T, rec interface{ Result() *http.Response }) *http.Cookie {
	t.Helper()
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie: %v, want one cookie", cookies)
	}
	return cookies[0]
}

type sessionBody struct {
	User      *auth.User      `json:"user"`
	CSRFToken *string         `json:"csrfToken"`
	ExpiresAt *string         `json:"expiresAt"`
	Token     *auth.TokenInfo `json:"token"`
}

func TestLogin(t *testing.T) {
	for _, secure := range []bool{false, true} {
		h := newHarness(t)
		r := request("POST", "/api/v1/auth/login", loginBody("admin", testPassword, h.code()))
		r.Header.Set("User-Agent", "test-agent")
		if secure {
			r.TLS = &tls.ConnectionState{}
		}
		rec := h.serve(r)
		if rec.Code != 200 {
			t.Fatalf("secure %v: status %d %s", secure, rec.Code, rec.Body)
		}
		ck := sessionCookieOf(t, rec)
		wantName := SessionCookiePlain
		if secure {
			wantName = SessionCookieSecure
		}
		if ck.Name != wantName || ck.Secure != secure || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode ||
			ck.Path != "/" || ck.Domain != "" || ck.MaxAge != 0 || !ck.Expires.IsZero() || len(ck.Value) != 43 {
			t.Errorf("secure %v: cookie %+v", secure, ck)
		}
		var body sessionBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if body.User == nil || body.User.Username != "admin" || body.CSRFToken == nil ||
			*body.CSRFToken != h.auth.CSRFToken(ck.Value) || body.ExpiresAt == nil || body.Token != nil {
			t.Errorf("secure %v: body %s", secure, rec.Body)
		}
		if !strings.Contains(rec.Body.String(), `"token":null`) {
			t.Errorf("secure %v: body lacks token null: %s", secure, rec.Body)
		}
		entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
		if err != nil || len(entries) != 1 || entries[0].Action != auth.AuditLoginAction || entries[0].Result != "ok" ||
			entries[0].Source != "192.0.2.10" {
			t.Errorf("secure %v: audit %+v, %v", secure, entries, err)
		}
	}
}

func TestLoginRefusals(t *testing.T) {
	h := newHarness(t)
	wrong := loginBody("admin", testPassword, "000000")
	for _, tc := range []struct {
		name        string
		r           *http.Request
		status      int
		code        errcode.Code
		contentType string
	}{
		{"wrong code", request("POST", "/api/v1/auth/login", wrong), 401, errcode.InvalidCredentials, ""},
		{"unknown user", request("POST", "/api/v1/auth/login", loginBody("nobody", testPassword, "123456")), 401, errcode.InvalidCredentials, ""},
		{"text body", request("POST", "/api/v1/auth/login", wrong), 415, errcode.UnsupportedMediaType, "text/plain"},
		{"unknown field", request("POST", "/api/v1/auth/login", `{"username":"admin","password":"`+testPassword+`","code":"1","remember":true}`),
			400, errcode.InvalidJSON, ""},
	} {
		if tc.contentType != "" {
			tc.r.Header.Set("Content-Type", tc.contentType)
		}
		rec := h.serve(tc.r)
		decodeProblem(t, rec, tc.status, tc.code)
		if strings.Contains(rec.Body.String(), testPassword) || len(rec.Result().Cookies()) != 0 {
			t.Errorf("%s: response leaks the password or sets a cookie: %v %s", tc.name, rec.Result().Cookies(), rec.Body)
		}
	}
}

func TestLoginRateLimited(t *testing.T) {
	h := newHarness(t)
	for range auth.UsernameFailureLimit {
		decodeProblem(t, h.serve(request("POST", "/api/v1/auth/login", loginBody("admin", "wrong password!", "000000"))),
			401, errcode.InvalidCredentials)
	}
	rec := h.serve(request("POST", "/api/v1/auth/login", loginBody("admin", testPassword, h.code())))
	decodeProblem(t, rec, 429, errcode.RateLimited)
	if n, err := strconv.Atoi(rec.Header().Get("Retry-After")); err != nil || n < 1 || n > int(auth.LockDuration.Seconds()) {
		t.Errorf("Retry-After = %q", rec.Header().Get("Retry-After"))
	}
}

func TestLogout(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()

	decodeProblem(t, h.serve(withSession(request("POST", "/api/v1/auth/logout", ""), session, "")), 403, errcode.CSRF)
	if rec := h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, "")); rec.Code != 200 {
		t.Fatalf("a refused logout ended the session: %d", rec.Code)
	}

	rec := h.serve(withSession(request("POST", "/api/v1/auth/logout", ""), session, csrf))
	if rec.Code != http.StatusNoContent || rec.Body.Len() != 0 {
		t.Fatalf("logout: %d %s", rec.Code, rec.Body)
	}
	ck := sessionCookieOf(t, rec)
	if ck.Name != SessionCookiePlain || ck.Value != "" || ck.MaxAge != -1 || !ck.HttpOnly || ck.SameSite != http.SameSiteStrictMode || ck.Path != "/" {
		t.Errorf("cleared cookie %+v", ck)
	}
	decodeProblem(t, h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, "")), 401, errcode.AuthRequired)
	entries, _, err := h.audit.List(context.Background(), core.AuditQuery{Limit: 10})
	if err != nil || len(entries) != 2 || entries[0].Action != auth.AuditLogoutAction || entries[0].Actor != "user:"+h.user.ID {
		t.Errorf("audit %+v, %v", entries, err)
	}

	token := h.token(core.ScopeRead)
	if rec := h.serve(withToken(request("POST", "/api/v1/auth/logout", ""), token)); rec.Code != http.StatusNoContent {
		t.Errorf("token logout: %d %s", rec.Code, rec.Body)
	}
	if rec := h.serve(withToken(request("GET", "/api/v1/auth/session", ""), token)); rec.Code != 200 {
		t.Errorf("logout ended a token: %d", rec.Code)
	}
}

func TestSessionRoute(t *testing.T) {
	h := newHarness(t)
	session, csrf := h.login()
	rec := h.serve(withSession(request("GET", "/api/v1/auth/session", ""), session, ""))
	var body sessionBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if body.User == nil || body.User.ID != h.user.ID || body.CSRFToken == nil || *body.CSRFToken != csrf ||
		body.ExpiresAt == nil || body.Token != nil {
		t.Errorf("session body %s", rec.Body)
	}

	token := h.token(core.ScopeOperate)
	rec = h.serve(withToken(request("GET", "/api/v1/auth/session", ""), token))
	body = sessionBody{}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil || rec.Code != 200 {
		t.Fatalf("%d %s %v", rec.Code, rec.Body, err)
	}
	if body.Token == nil || body.Token.Scope != core.ScopeOperate || !strings.HasPrefix(token, "dpt_"+body.Token.ID+"_") ||
		body.CSRFToken != nil || body.User == nil || body.User.ID != h.user.ID || body.ExpiresAt == nil {
		t.Errorf("token body %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), strings.TrimPrefix(token, "dpt_"+body.Token.ID+"_")) {
		t.Error("the session route echoed the token secret")
	}

	decodeProblem(t, h.serve(request("GET", "/api/v1/auth/session", "")), 401, errcode.AuthRequired)
}

func TestAuthRouteMethods(t *testing.T) {
	h := newHarness(t)
	token := h.token(core.ScopeRead)
	decodeProblem(t, h.serve(request("GET", "/api/v1/auth/login", "")), 401, errcode.AuthRequired)
	rec := h.serve(withToken(request("GET", "/api/v1/auth/login", ""), token))
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "POST" {
		t.Errorf("Allow = %q, want POST", got)
	}
	rec = h.serve(withToken(request("DELETE", "/api/v1/auth/session", ""), token))
	decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
	if got := rec.Header().Get("Allow"); got != "GET, HEAD" {
		t.Errorf("Allow = %q, want GET, HEAD", got)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLogin|TestLogout|TestSessionRoute|TestAuthRouteMethods' ./internal/controller/api/`
Expected: FAIL — `TestLogin` fails with `status 401 ... "code":"auth.required"` (the login route does not exist yet, so the fallback refuses), and `TestLogout`, `TestSessionRoute` and `TestAuthRouteMethods` fail with `404 ... "code":"not_found"`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/auth.go`:

```go
package api

import (
	"net/http"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
)

type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// sessionResponse is the body of a login and of GET /api/v1/auth/session
// (spec section 12). A field that does not apply is null: csrfToken for a
// token, token for a session.
type sessionResponse struct {
	User      *auth.User      `json:"user"`
	CSRFToken *string         `json:"csrfToken"`
	ExpiresAt *time.Time      `json:"expiresAt"`
	Token     *auth.TokenInfo `json:"token"`
}

// sessionCookie is the session cookie of r's listener (spec section 9):
// HttpOnly, SameSite=Strict, Path=/, no Domain, and Secure exactly on HTTPS.
// It has no expiry; the server ends the session. A negative maxAge clears
// it.
func sessionCookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure is set on HTTPS; the plain-HTTP loopback cookie cannot be Secure (spec section 9)
		Name:     sessionCookieName(r),
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		Secure:   r.TLS != nil,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	}
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var in loginRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	res, err := s.cfg.Auth.Login(r.Context(), auth.LoginRequest{
		Username:  in.Username,
		Password:  in.Password,
		Code:      in.Code,
		Source:    clientAddr(r),
		UserAgent: r.UserAgent(),
	})
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	http.SetCookie(w, sessionCookie(r, res.SessionID, 0))
	writeJSON(w, s.log, http.StatusOK, sessionResponse{User: &res.User, CSRFToken: &res.CSRFToken, ExpiresAt: &res.ExpiresAt})
}

// handleLogout ends the caller's session, when it is one, and clears the
// cookie either way.
func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if c.identity.FromSession {
		if err := s.cfg.Auth.Logout(r.Context(), c.session, clientAddr(r)); err != nil {
			writeProblem(w, s.log, err)
			return
		}
	}
	http.SetCookie(w, sessionCookie(r, "", -1))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	id := c.identity
	resp := sessionResponse{User: &id.User, ExpiresAt: &id.ExpiresAt, Token: id.Token}
	if id.FromSession {
		csrf := s.cfg.Auth.CSRFToken(c.session)
		resp.CSRFToken = &csrf
	}
	writeJSON(w, s.log, http.StatusOK, resp)
}
```

Replace the whole of `internal/controller/api/routes.go` with:

```go
package api

import (
	"net/http"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

// route is one route of the network mux. A public route must also be public
// in routeAccess, which the authentication middleware consults.
type route struct {
	method, path, summary string
	public                bool
	handler               http.HandlerFunc
}

// networkRoutes lists the network mux's routes.
func (s *Server) networkRoutes() []route {
	return []route{
		{method: http.MethodPost, path: loginPath, summary: "Log in with username, password and TOTP code", public: true, handler: s.handleLogin},
		{method: http.MethodPost, path: "/api/v1/auth/logout", summary: "Log out and clear the session cookie", handler: s.handleLogout},
		{method: http.MethodGet, path: "/api/v1/auth/session", summary: "Describe the calling session or token", handler: s.handleSession},
	}
}

// Routes returns the hand-written network routes that schema.OpenAPI
// documents next to the registry's actions and resources.
func Routes() []schema.Route {
	out := []schema.Route{}
	for _, rt := range (&Server{}).networkRoutes() {
		out = append(out, schema.Route{Method: rt.method, Path: rt.path, Summary: rt.summary, Public: rt.public})
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestLogin|TestLogout|TestSessionRoute|TestAuthRouteMethods' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/auth.go internal/controller/api/routes.go internal/controller/api/auth_test.go
git commit -m "feat(api): add login, logout and session routes"
```


### Task 21: Action, resource and change routes

**Files:**
- Create: `internal/controller/api/actions.go`
- Modify: `internal/controller/api/routes.go` (the step gives the whole new file)
- Test: `internal/controller/api/actions_test.go`

**Interfaces:**
- Consumes: Contracts › Task 11 (`decodeBody`, `writeJSON`, `writeProblem`), Task 16 (`RequiredScope`, `authorize`), Task 19 (`Server`, `callerFrom`, the harness, `demoModule`) and Task 20 (`routes.go` content); `core.Dispatcher`, `core.PlanView`, `core.ChangeView`, `core.ApplyOptions`, `core.ChangeFilter`, `core.ParseVersion`, the `core.State*` constants (foundation); `lifecycle.MinConfirmTimeout`, `lifecycle.MaxConfirmTimeout` (plan 2).
- Produces: Contracts › Task 21 (the action, resource and change routes, the wire types, `lookupAction`, `actionHandler`, `runAction`, `authorizeChange`, `changeOp`, the `perAction` route flag, and the test helpers `call` and `stubDispatcher`).

**Items:** 3, 5, 13, 52

**Depends on:** Tasks 11, 16, 19 and 20

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Authorization follows ADR 0004: the API decides scope and break-glass before dispatching. Plan, apply and read resolve the action from the route's `{name}` and `{version}`, refuse break-glass actions with `auth.forbidden`, and pass that action's `Ref()` and the route's host to the dispatcher, which must refuse a plan id recorded for another host or action (part contracts, obligation 1). Confirm and revert carry only a change id, so they first read the change through `Dispatcher.Change` as the caller and authorize against the change's own action. `Plan.diff` and `Plan.guards` are `[]`, never `null` (register row 52), and apply answers 200 with the change whatever its outcome.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/actions_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// call records what the fake dispatcher received.
type call struct {
	method, hostID, planID, changeID, name string
	ref                                    core.ActionRef
	input                                  string
	opts                                   core.ApplyOptions
	filter                                 core.ChangeFilter
	principal                              core.Principal
}

var (
	changeTime = time.Date(2026, 10, 17, 12, 5, 0, 0, time.UTC)
	deadline   = changeTime.Add(time.Minute)
)

// stubDispatcher answers every dispatcher method and records the last call
// in *last.
func (h *harness) stubDispatcher(last *call) {
	d := h.disp
	d.ReadFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
		*last = call{method: "Read", principal: p, hostID: hostID, ref: a, input: string(in)}
		return json.RawMessage(`{"ok":true}`), nil
	}
	d.ResourceFunc = func(_ context.Context, p core.Principal, hostID, name string) (json.RawMessage, error) {
		*last = call{method: "Resource", principal: p, hostID: hostID, name: name}
		return json.RawMessage(`{"healthy": true}`), nil
	}
	d.PlanFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, in json.RawMessage) (core.PlanView, error) {
		*last = call{method: "Plan", principal: p, hostID: hostID, ref: a, input: string(in)}
		return core.PlanView{PlanID: "plan1", HostID: hostID, Action: a.Name, Version: a.Version, InputHash: "h",
			ExpiresAt: changeTime.Add(10 * time.Minute)}, nil
	}
	d.ApplyFunc = func(_ context.Context, p core.Principal, hostID string, a core.ActionRef, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
		*last = call{method: "Apply", principal: p, hostID: hostID, ref: a, planID: planID, opts: opts}
		return core.ChangeView{ID: "change1", HostID: hostID, Action: a.Name, Version: a.Version, Class: core.ClassRevertible,
			State: core.StateFailed, Actor: p.Actor, CreatedAt: changeTime, UpdatedAt: changeTime,
			Error: &core.ChangeError{Code: "files.markers_invalid", Message: "markers out of order"}}, nil
	}
	d.ConfirmFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Confirm", principal: p, changeID: id}
		return core.ChangeView{ID: id, State: core.StateConfirmed}, nil
	}
	d.RevertFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Revert", principal: p, changeID: id}
		return core.ChangeView{ID: id, State: core.StateReverted}, nil
	}
	// Change c1 is a controller-side compensable change, cbg a break-glass
	// change, cunk a controller-side change of an unregistered action, cua
	// an agent-side one, missing does not exist, and any other id is a
	// pending change of the agent-side revertible demo.item.add.
	d.ChangeFunc = func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
		*last = call{method: "Change", principal: p, changeID: id}
		v := core.ChangeView{ID: id, HostID: "h1", Action: "demo.item.add", Version: 1, Class: core.ClassRevertible,
			State: core.StatePending, Actor: "user:u1", CreatedAt: changeTime, UpdatedAt: changeTime}
		switch id {
		case "c1":
			v.HostID, v.Action, v.Class, v.State = "", "demo.item.remove", core.ClassCompensable, core.StateCompleted
		case "cbg":
			v.HostID, v.Action, v.Class, v.State = "", "demo.secret.reset", core.ClassIrreversible, core.StateCompleted
		case "cunk":
			v.HostID, v.Action, v.Class = "", "demo.gone.away", core.ClassCompensable
		case "cua":
			v.Action = "other.item.set"
		case "missing":
			return core.ChangeView{}, errcode.New(errcode.ChangeNotFound, "no change missing")
		}
		return v, nil
	}
	d.ChangesFunc = func(_ context.Context, p core.Principal, f core.ChangeFilter) ([]core.ChangeView, error) {
		*last = call{method: "Changes", principal: p, filter: f}
		return nil, nil
	}
}

func TestActionRoutesDispatch(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeAdmin)
	for _, tc := range []struct {
		path, body string
		want       call
		response   string
	}{
		{"/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{"input":{"name":"x"}}`,
			call{method: "Read", hostID: "h1", ref: core.ActionRef{Name: "demo.item.get", Version: 1}, input: `{"name":"x"}`},
			`{"output":{"ok":true}}`},
		{"/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`,
			call{method: "Read", ref: core.ActionRef{Name: "demo.item.list", Version: 1}, input: `{}`},
			`{"output":{"ok":true}}`},
		{"/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{"name":"x"}}`,
			call{method: "Plan", hostID: "h1", ref: core.ActionRef{Name: "demo.item.add", Version: 1}, input: `{"name":"x"}`},
			`{"planId":"plan1","hostId":"h1","action":"demo.item.add","version":1,"inputHash":"h","diff":[],"guards":[],"expiresAt":"2026-10-17T12:15:00Z","confirmation":null}`},
		{"/api/v1/actions/demo.item.remove/v1/plan", `{"input":{"name":"x"}}`,
			call{method: "Plan", ref: core.ActionRef{Name: "demo.item.remove", Version: 1}, input: `{"name":"x"}`},
			`{"planId":"plan1","hostId":null,"action":"demo.item.remove","version":1,"inputHash":"h","diff":[],"guards":[],"expiresAt":"2026-10-17T12:15:00Z","confirmation":null}`},
		{"/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"plan1","confirmTimeoutSeconds":30,"confirmNow":true}`,
			call{method: "Apply", hostID: "h1", ref: core.ActionRef{Name: "demo.item.add", Version: 1}, planID: "plan1",
				opts: core.ApplyOptions{ConfirmTimeout: 30 * time.Second, ConfirmNow: true}},
			`{"id":"change1","hostId":"h1","action":"demo.item.add","version":1,"class":"revertible","state":"failed","actor":"ACTOR","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":{"code":"files.markers_invalid","message":"markers out of order"},"compensatedBy":null}`},
		{"/api/v1/hosts/h1/actions/demo.item.purge/v1/apply", `{"planId":"plan2","confirmation":"purge it"}`,
			call{method: "Apply", hostID: "h1", ref: core.ActionRef{Name: "demo.item.purge", Version: 1}, planID: "plan2",
				opts: core.ApplyOptions{Confirmation: "purge it"}},
			`{"id":"change1","hostId":"h1","action":"demo.item.purge","version":1,"class":"revertible","state":"failed","actor":"ACTOR","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":{"code":"files.markers_invalid","message":"markers out of order"},"compensatedBy":null}`},
	} {
		last = call{}
		rec := h.serve(withToken(request("POST", tc.path, tc.body), token))
		if rec.Code != 200 {
			t.Errorf("%s: status %d %s", tc.path, rec.Code, rec.Body)
			continue
		}
		if !strings.HasPrefix(last.principal.Actor, "token:") || last.principal.Scope != core.ScopeAdmin || last.principal.Source != "192.0.2.10" {
			t.Errorf("%s: principal %+v", tc.path, last.principal)
		}
		want := strings.Replace(tc.response, "ACTOR", last.principal.Actor, 1)
		tc.want.principal = last.principal
		if last != tc.want {
			t.Errorf("%s: dispatched %+v, want %+v", tc.path, last, tc.want)
		}
		if rec.Body.String() != want {
			t.Errorf("%s: body\n%s\nwant\n%s", tc.path, rec.Body, want)
		}
		if rec.Header().Get("Content-Type") != "application/json" || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: headers %v", tc.path, rec.Header())
		}
	}
}

func TestActionRouteRefusals(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	h.disp.PlanFunc = func(context.Context, core.Principal, string, core.ActionRef, json.RawMessage) (core.PlanView, error) {
		return core.PlanView{}, errcode.New(errcode.ModuleUnavailable, "module demo is disabled")
	}
	token := h.token(core.ScopeAdmin)
	plan := `{"input":{"name":"x"}}`
	for _, tc := range []struct {
		name, path, body, contentType string
		status                        int
		code                          errcode.Code
		field                         string
	}{
		{"unknown action", "/api/v1/hosts/h1/actions/demo.item.nope/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"unknown version", "/api/v1/hosts/h1/actions/demo.item.add/v2/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"version without v", "/api/v1/hosts/h1/actions/demo.item.add/1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"version with a leading zero", "/api/v1/hosts/h1/actions/demo.item.add/v01/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"agent action on the controller route", "/api/v1/actions/demo.item.get/v1/read", plan, "", 404, errcode.ActionUnknown, ""},
		{"controller action on a host route", "/api/v1/hosts/h1/actions/demo.item.remove/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"read of a mutating action", "/api/v1/hosts/h1/actions/demo.item.add/v1/read", plan, "", 404, errcode.ActionUnknown, ""},
		{"plan of a read action", "/api/v1/hosts/h1/actions/demo.item.get/v1/plan", plan, "", 404, errcode.ActionUnknown, ""},
		{"break-glass plan", "/api/v1/actions/demo.secret.reset/v1/plan", plan, "", 403, errcode.Forbidden, ""},
		{"break-glass apply", "/api/v1/actions/demo.secret.reset/v1/apply", `{"planId":"p"}`, "", 403, errcode.Forbidden, ""},
		{"break-glass read", "/api/v1/actions/demo.secret.show/v1/read", `{"input":{}}`, "", 403, errcode.Forbidden, ""},
		{"missing input", "/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{}`, "", 422, errcode.InvalidInput, "/input"},
		{"missing plan id", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"confirmNow":true}`, "", 422, errcode.InvalidInput, "/planId"},
		{"short confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":9}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"long confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":901}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"huge confirm timeout", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","confirmTimeoutSeconds":9223372036854775807}`, "", 422, errcode.InvalidInput, "/confirmTimeoutSeconds"},
		{"unknown apply field", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p","force":true}`, "", 400, errcode.InvalidJSON, ""},
		{"text body", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", plan, "text/plain", 415, errcode.UnsupportedMediaType, ""},
		{"dispatcher refusal", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", plan, "", 409, errcode.ModuleUnavailable, ""},
	} {
		last = call{}
		r := withToken(request("POST", tc.path, tc.body), token)
		if tc.contentType != "" {
			r.Header.Set("Content-Type", tc.contentType)
		}
		p := decodeProblem(t, h.serve(r), tc.status, tc.code)
		if tc.field != "" && (len(p.Errors) != 1 || p.Errors[0].Path != tc.field) {
			t.Errorf("%s: errors %+v, want one at %s", tc.name, p.Errors, tc.field)
		}
		if tc.code != errcode.ModuleUnavailable && last.method != "" {
			t.Errorf("%s: dispatched %s", tc.name, last.method)
		}
	}
}

func TestScopes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	tokens := map[core.Scope]string{}
	for _, s := range []core.Scope{core.ScopeRead, core.ScopeOperate, core.ScopeAdmin} {
		tokens[s] = h.token(s)
	}
	session, csrf := h.login()
	for _, tc := range []struct {
		method, path, body string
		need               core.Scope
	}{
		{"GET", "/api/v1/hosts/h1/resources/demo/status", "", core.ScopeRead},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.get/v1/read", `{"input":{}}`, core.ScopeRead},
		{"POST", "/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`, core.ScopeRead},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{}}`, core.ScopeOperate},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", `{"planId":"p"}`, core.ScopeOperate},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.purge/v1/plan", `{"input":{}}`, core.ScopeOperate},
		{"POST", "/api/v1/actions/demo.item.remove/v1/plan", `{"input":{}}`, core.ScopeAdmin},
		{"POST", "/api/v1/actions/demo.item.remove/v1/apply", `{"planId":"p"}`, core.ScopeAdmin},
		{"GET", "/api/v1/changes", "", core.ScopeRead},
		{"GET", "/api/v1/changes/c1", "", core.ScopeRead},
		{"POST", "/api/v1/changes/c7/confirm", "", core.ScopeOperate},
		{"POST", "/api/v1/changes/c7/revert", "", core.ScopeOperate},
		{"POST", "/api/v1/changes/c1/confirm", "", core.ScopeAdmin},
		{"POST", "/api/v1/changes/c1/revert", "", core.ScopeAdmin},
	} {
		for scope, token := range tokens {
			last = call{}
			rec := h.serve(withToken(request(tc.method, tc.path, tc.body), token))
			if scope.Allows(tc.need) {
				if rec.Code != 200 || last.method == "" {
					t.Errorf("%s %s with %s: %d %s, want dispatched", tc.method, tc.path, scope, rec.Code, rec.Body)
				}
				continue
			}
			p := decodeProblem(t, rec, 403, errcode.Forbidden)
			// A refused confirm or revert has looked the change up, nothing more.
			if p.Detail != "this request needs the "+string(tc.need)+" scope" || (last.method != "" && last.method != "Change") {
				t.Errorf("%s %s with %s: detail %q, dispatched %q", tc.method, tc.path, scope, p.Detail, last.method)
			}
		}
		if rec := h.serve(withSession(request(tc.method, tc.path, tc.body), session, csrf)); rec.Code != 200 {
			t.Errorf("%s %s with an admin session: %d %s", tc.method, tc.path, rec.Code, rec.Body)
		}
	}
}

func TestResourceRoute(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeRead)
	rec := h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/status", ""), token))
	if rec.Code != 200 || rec.Body.String() != `{"healthy":true}` {
		t.Errorf("resource: %d %s", rec.Code, rec.Body)
	}
	if last.method != "Resource" || last.hostID != "h1" || last.name != "demo.status" {
		t.Errorf("dispatched %+v", last)
	}

	last = call{}
	decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/nope", ""), token)), 404, errcode.NotFound)
	if last.method != "" {
		t.Errorf("an unknown resource was dispatched: %+v", last)
	}

	h.disp.ResourceFunc = func(context.Context, core.Principal, string, string) (json.RawMessage, error) { return nil, nil }
	if rec := h.serve(withToken(request("GET", "/api/v1/hosts/h1/resources/demo/status", ""), token)); rec.Body.String() != "null" {
		t.Errorf("empty output: %s", rec.Body)
	}
}

func TestChangeRoutes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeOperate)

	rec := h.serve(withToken(request("GET", "/api/v1/changes?state=pending&hostId=h1&before=c9&limit=5", ""), token))
	if rec.Code != 200 || rec.Body.String() != `{"changes":[]}` {
		t.Errorf("list: %d %s", rec.Code, rec.Body)
	}
	if want := (core.ChangeFilter{State: "pending", HostID: "h1", Before: "c9", Limit: 5}); last.filter != want {
		t.Errorf("filter %+v, want %+v", last.filter, want)
	}
	for _, q := range []string{"limit=0", "limit=501", "limit=x", "state=done"} {
		last = call{}
		p := decodeProblem(t, h.serve(withToken(request("GET", "/api/v1/changes?"+q, ""), token)), 422, errcode.InvalidInput)
		if len(p.Errors) != 1 || !strings.HasPrefix(q, p.Errors[0].Path+"=") || last.method != "" {
			t.Errorf("%s: errors %+v, dispatched %q", q, p.Errors, last.method)
		}
	}

	rec = h.serve(withToken(request("GET", "/api/v1/changes/c1", ""), token))
	want := `{"id":"c1","hostId":null,"action":"demo.item.remove","version":1,"class":"compensable","state":"completed","actor":"user:u1","createdAt":"2026-10-17T12:05:00Z","updatedAt":"2026-10-17T12:05:00Z","revertDeadline":null,"output":null,"error":null,"compensatedBy":null}`
	if rec.Code != 200 || rec.Body.String() != want || last.method != "Change" || last.changeID != "c1" {
		t.Errorf("get: %d %s (dispatched %+v)", rec.Code, rec.Body, last)
	}

	h.disp.ChangesFunc = func(context.Context, core.Principal, core.ChangeFilter) ([]core.ChangeView, error) {
		return []core.ChangeView{{ID: "c2", HostID: "h1", Action: "demo.item.add", Version: 1, Class: core.ClassRevertible,
			State: core.StatePending, RevertDeadline: &deadline, Output: json.RawMessage(`{"ok":true}`), CompensatedBy: "c3"}}, nil
	}
	rec = h.serve(withToken(request("GET", "/api/v1/changes", ""), token))
	if !strings.Contains(rec.Body.String(), `"hostId":"h1"`) || !strings.Contains(rec.Body.String(), `"revertDeadline":"2026-10-17T12:06:00Z"`) ||
		!strings.Contains(rec.Body.String(), `"output":{"ok":true}`) || !strings.Contains(rec.Body.String(), `"compensatedBy":"c3"`) {
		t.Errorf("list body %s", rec.Body)
	}

	for path, method := range map[string]string{"/api/v1/changes/c7/confirm": "Confirm", "/api/v1/changes/c7/revert": "Revert"} {
		last = call{}
		rec := h.serve(withToken(request("POST", path, ""), token))
		if rec.Code != 200 || last.method != method || last.changeID != "c7" {
			t.Errorf("%s: %d %s (dispatched %+v)", path, rec.Code, rec.Body, last)
		}
	}

	h.disp.ConfirmFunc = func(context.Context, core.Principal, string) (core.ChangeView, error) {
		return core.ChangeView{}, errcode.New(errcode.ChangeNotPending, "change c7 is confirmed")
	}
	decodeProblem(t, h.serve(withToken(request("POST", "/api/v1/changes/c7/confirm", ""), token)), 409, errcode.ChangeNotPending)
	rec = h.serve(withToken(request("PUT", "/api/v1/changes/c7/confirm", ""), token))
	decodeProblem(t, rec, http.StatusMethodNotAllowed, errcode.MethodNotAllowed)
	if rec.Header().Get("Allow") != "POST" {
		t.Errorf("Allow = %q", rec.Header().Get("Allow"))
	}
}

func TestChangeOpsAuthorizeTheChangesAction(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	operate, admin := h.token(core.ScopeOperate), h.token(core.ScopeAdmin)
	session, csrf := h.login()
	for _, op := range []string{"confirm", "revert"} {
		for _, tc := range []struct {
			name, id, token string
			session         bool
			status          int
			code            errcode.Code
		}{
			{"agent change with operate", "c7", operate, false, 200, ""},
			{"unregistered agent action with operate", "cua", operate, false, 200, ""},
			{"controller change with operate", "c1", operate, false, 403, errcode.Forbidden},
			{"controller change with admin", "c1", admin, false, 200, ""},
			{"break-glass change with admin", "cbg", admin, false, 403, errcode.Forbidden},
			{"break-glass change with a session", "cbg", "", true, 403, errcode.Forbidden},
			{"unregistered controller action with admin", "cunk", admin, false, 403, errcode.Forbidden},
			{"missing change", "missing", admin, false, 404, errcode.ChangeNotFound},
		} {
			last = call{}
			r := request("POST", "/api/v1/changes/"+tc.id+"/"+op, "")
			if tc.session {
				r = withSession(r, session, csrf)
			} else {
				r = withToken(r, tc.token)
			}
			rec := h.serve(r)
			if tc.status == 200 {
				want := "Confirm"
				if op == "revert" {
					want = "Revert"
				}
				if rec.Code != 200 || last.method != want || last.changeID != tc.id {
					t.Errorf("%s %s: %d %s, dispatched %+v", op, tc.name, rec.Code, rec.Body, last)
				}
				continue
			}
			decodeProblem(t, rec, tc.status, tc.code)
			if last.method != "Change" || last.changeID != tc.id || !strings.HasPrefix(last.principal.Actor, "token:") && !tc.session {
				t.Errorf("%s %s: last call %+v, want only the change lookup as the caller", op, tc.name, last)
			}
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestActionRoutes|TestActionRouteRefusals|TestScopes|TestResourceRoute|TestChangeRoutes|TestChangeOpsAuthorizeTheChangesAction' ./internal/controller/api/`
Expected: FAIL — `TestActionRoutesDispatch` and the other tests fail with `status 404 {... "detail":"no such route","code":"not_found"}`, and `TestActionRouteRefusals` with `want code action.unknown status 404`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/actions.go`:

```go
package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
)

// The operations of an action route, its last path segment.
const (
	opPlan  = "plan"
	opApply = "apply"
	opRead  = "read"
)

// maxChangeLimit is the largest limit of GET /api/v1/changes; the engine
// lists no more.
const maxChangeLimit = 500

var changeStates = []string{
	core.StateApplying, core.StatePending, core.StateConfirmed, core.StateReverted,
	core.StateCompleted, core.StateFailed, core.StateInterrupted,
}

// planResponse is the Plan shape of spec section 12.
type planResponse struct {
	PlanID       string          `json:"planId"`
	HostID       *string         `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	InputHash    string          `json:"inputHash"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	ExpiresAt    time.Time       `json:"expiresAt"`
	Confirmation *string         `json:"confirmation"`
}

type changeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// changeResponse is the Change shape of spec section 12.
type changeResponse struct {
	ID             string          `json:"id"`
	HostID         *string         `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Class          core.Class      `json:"class"`
	State          string          `json:"state"`
	Actor          string          `json:"actor"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *changeError    `json:"error"`
	CompensatedBy  *string         `json:"compensatedBy"`
}

type changesResponse struct {
	Changes []changeResponse `json:"changes"`
}

type readResponse struct {
	Output json.RawMessage `json:"output"`
}

// inputRequest is the body of plan and read.
type inputRequest struct {
	Input json.RawMessage `json:"input"`
}

type applyRequest struct {
	PlanID                string `json:"planId"`
	ConfirmTimeoutSeconds int64  `json:"confirmTimeoutSeconds"`
	Confirmation          string `json:"confirmation"`
	ConfirmNow            bool   `json:"confirmNow"`
}

func nullable(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func orNull(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}

// toPlan converts a plan; its diff and guards are [] rather than null.
func toPlan(v core.PlanView) planResponse {
	diff, guards := v.Diff, v.Guards
	if diff == nil {
		diff = []core.DiffLine{}
	}
	if guards == nil {
		guards = []core.Guard{}
	}
	return planResponse{PlanID: v.PlanID, HostID: nullable(v.HostID), Action: v.Action, Version: v.Version,
		InputHash: v.InputHash, Diff: diff, Guards: guards, ExpiresAt: v.ExpiresAt, Confirmation: nullable(v.Confirmation)}
}

func toChange(v core.ChangeView) changeResponse {
	c := changeResponse{ID: v.ID, HostID: nullable(v.HostID), Action: v.Action, Version: v.Version, Class: v.Class,
		State: v.State, Actor: v.Actor, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, RevertDeadline: v.RevertDeadline,
		Output: orNull(v.Output), CompensatedBy: nullable(v.CompensatedBy)}
	if v.Error != nil {
		c.Error = &changeError{Code: v.Error.Code, Message: v.Error.Message}
	}
	return c
}

func invalidField(path, message string) *errcode.Error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: "invalid request",
		Fields: []errcode.FieldError{{Path: path, Message: message}}}
}

// options checks an apply body. The confirm timeout is bounded here too, so
// a huge value cannot overflow the duration.
func (a applyRequest) options() (core.ApplyOptions, error) {
	if a.PlanID == "" {
		return core.ApplyOptions{}, invalidField("/planId", "required")
	}
	lo, hi := int64(lifecycle.MinConfirmTimeout/time.Second), int64(lifecycle.MaxConfirmTimeout/time.Second)
	if a.ConfirmTimeoutSeconds != 0 && (a.ConfirmTimeoutSeconds < lo || a.ConfirmTimeoutSeconds > hi) {
		return core.ApplyOptions{}, invalidField("/confirmTimeoutSeconds", fmt.Sprintf("must be %d to %d", lo, hi))
	}
	return core.ApplyOptions{ConfirmTimeout: time.Duration(a.ConfirmTimeoutSeconds) * time.Second,
		Confirmation: a.Confirmation, ConfirmNow: a.ConfirmNow}, nil
}

// lookupAction resolves an action route's {name} and {version} to an action
// that side serves with op: a read action has only read, a mutating action
// only plan and apply.
func (s *Server) lookupAction(name, version string, side core.Side, op string) (core.ActionDef, error) {
	unknown := errcode.New(errcode.ActionUnknown, fmt.Sprintf("no action %s/%s with a %s operation at this route", name, version, op))
	v, err := core.ParseVersion(version)
	if err != nil {
		return core.ActionDef{}, unknown
	}
	def, ok := s.cfg.Registry.Action(core.ActionRef{Name: name, Version: v})
	if !ok || def.Spec.Side != side || (op == opRead) != (def.Spec.Class == core.ClassRead) {
		return core.ActionDef{}, unknown
	}
	return def, nil
}

// actionHandler serves op of the network's action routes: agent-side
// actions under /api/v1/hosts/{host}, controller-side ones without a host.
// Break-glass actions are refused here; only the controller socket serves
// them (spec section 10).
func (s *Server) actionHandler(side core.Side, op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
		def, err := s.lookupAction(r.PathValue("name"), r.PathValue("version"), side, op)
		if err == nil && def.Spec.BreakGlass {
			err = errcode.New(errcode.Forbidden, "break-glass actions are served only on the controller socket")
		}
		if err == nil {
			err = authorize(c.principal, RequiredScope(def.Spec.Class, def.Spec.Side))
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		s.runAction(w, r, c.principal, r.PathValue("host"), def, op)
	}
}

// runAction decodes op's body and dispatches it. Apply answers 200 with the
// change whatever its outcome (spec section 12).
func (s *Server) runAction(w http.ResponseWriter, r *http.Request, p core.Principal, hostID string, def core.ActionDef, op string) {
	ctx := r.Context()
	if op == opApply {
		var in applyRequest
		if err := decodeBody(w, r, &in); err != nil {
			writeProblem(w, s.log, err)
			return
		}
		opts, err := in.options()
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		v, err := s.cfg.Dispatcher.Apply(ctx, p, hostID, def.Ref(), in.PlanID, opts)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, toChange(v))
		return
	}
	var in inputRequest
	if err := decodeBody(w, r, &in); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	if len(in.Input) == 0 {
		writeProblem(w, s.log, invalidField("/input", "required"))
		return
	}
	if op == opRead {
		out, err := s.cfg.Dispatcher.Read(ctx, p, hostID, def.Ref(), in.Input)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, readResponse{Output: orNull(out)})
		return
	}
	v, err := s.cfg.Dispatcher.Plan(ctx, p, hostID, def.Ref(), in.Input)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, toPlan(v))
}

// handleResource answers a resource's output as it is.
func (s *Server) handleResource(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	name := r.PathValue("module") + "." + r.PathValue("resource")
	var err error
	if _, ok := s.cfg.Registry.Resource(name); !ok {
		err = errcode.New(errcode.NotFound, "no resource "+name)
	} else {
		err = authorize(c.principal, core.ScopeRead)
	}
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	out, err := s.cfg.Dispatcher.Resource(r.Context(), c.principal, r.PathValue("host"), name)
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
	writeJSON(w, s.log, http.StatusOK, orNull(out))
}

// changeFilter reads the query of GET /api/v1/changes.
func changeFilter(q url.Values) (core.ChangeFilter, error) {
	f := core.ChangeFilter{State: q.Get("state"), HostID: q.Get("hostId"), Before: q.Get("before")}
	if f.State != "" && !slices.Contains(changeStates, f.State) {
		return f, invalidField("state", fmt.Sprintf("must be one of %v", changeStates))
	}
	if l := q.Get("limit"); l != "" {
		n, err := strconv.Atoi(l)
		if err != nil || n < 1 || n > maxChangeLimit {
			return f, invalidField("limit", fmt.Sprintf("must be 1 to %d", maxChangeLimit))
		}
		f.Limit = n
	}
	return f, nil
}

func (s *Server) handleChanges(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	f, err := changeFilter(r.URL.Query())
	if err != nil {
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

func (s *Server) handleChange(w http.ResponseWriter, r *http.Request) {
	c, _ := callerFrom(r.Context())
	if err := authorize(c.principal, core.ScopeRead); err != nil {
		writeProblem(w, s.log, err)
		return
	}
	v, err := s.cfg.Dispatcher.Change(r.Context(), c.principal, r.PathValue("id"))
	if err != nil {
		writeProblem(w, s.log, err)
		return
	}
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
		id := r.PathValue("id")
		err := authorize(c.principal, core.ScopeRead)
		var v core.ChangeView
		if err == nil {
			v, err = s.cfg.Dispatcher.Change(r.Context(), c.principal, id)
		}
		if err == nil {
			err = s.authorizeChange(c.principal, v)
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		run := s.cfg.Dispatcher.Revert
		if confirm {
			run = s.cfg.Dispatcher.Confirm
		}
		v, err = run(r.Context(), c.principal, id)
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		writeJSON(w, s.log, http.StatusOK, toChange(v))
	}
}
```

Replace the whole of `internal/controller/api/routes.go` with:

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

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestActionRoutes|TestActionRouteRefusals|TestScopes|TestResourceRoute|TestChangeRoutes|TestChangeOpsAuthorizeTheChangesAction' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/actions.go internal/controller/api/routes.go internal/controller/api/actions_test.go
git commit -m "feat(api): add action, resource and change routes"
```


### Task 22: Health and OpenAPI routes

**Files:**
- Create: `internal/controller/api/meta.go`
- Modify: `internal/controller/api/routes.go` (the step gives the whole new file)
- Test: `internal/controller/api/meta_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 11 and 16 (`authorize`), Task 19 (`Config.SchemaVersion`, the harness) and Task 21 (`routes.go` content); `buildinfo.Version`, `buildinfo.Commit` (foundation); `schema.OpenAPI`, `schema.Route` (plan 2).
- Produces: Contracts › Task 22 (`GET /healthz`, `GET /api/v1/openapi.json`, `writeHealthDetail`, the final `Routes()` list).

**Items:** 3, 9

**Depends on:** Tasks 11, 16, 19 and 21

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`/healthz` is public: without valid credentials it answers `{"status":"ok"}` only, and the authentication middleware drops invalid credentials on this route instead of refusing them (spec section 9). The OpenAPI document is built per request from the registry and `Routes()`; it is fetched rarely, and building it keeps `Server` free of a cached copy.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/meta_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

func TestHealthz(t *testing.T) {
	h := newHarness(t)
	session, _ := h.login()
	detail := fmt.Sprintf(`{"status":"ok","version":%q,"commit":%q,"schemaVersion":3}`, buildinfo.Version, buildinfo.Commit)
	for _, tc := range []struct {
		name string
		body string
		r    func() *http.Request
	}{
		{"no credentials", `{"status":"ok"}`, func() *http.Request { return request("GET", "/healthz", "") }},
		{"invalid bearer", `{"status":"ok"}`, func() *http.Request { return withToken(request("GET", "/healthz", ""), "dpt_bogus") }},
		{"invalid session", `{"status":"ok"}`, func() *http.Request { return withSession(request("GET", "/healthz", ""), "bogus", "") }},
		{"read token", detail, func() *http.Request { return withToken(request("GET", "/healthz", ""), h.token(core.ScopeRead)) }},
		{"session", detail, func() *http.Request { return withSession(request("GET", "/healthz", ""), session, "") }},
	} {
		rec := h.serve(tc.r())
		if rec.Code != 200 || rec.Body.String() != tc.body || rec.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("%s: %d %s %v, want 200 %s", tc.name, rec.Code, rec.Body, rec.Header(), tc.body)
		}
	}
	if rec := h.serve(request("HEAD", "/healthz", "")); rec.Code != 200 {
		t.Errorf("HEAD /healthz: %d", rec.Code)
	}

	h.srv.cfg.SchemaVersion = func(context.Context) (int, error) { return 0, errors.New("database is locked") }
	decodeProblem(t, h.serve(withSession(request("GET", "/healthz", ""), session, "")), 500, errcode.Internal)
	if rec := h.serve(request("GET", "/healthz", "")); rec.Code != 200 || rec.Body.String() != `{"status":"ok"}` {
		t.Errorf("public health with a failing database: %d %s", rec.Code, rec.Body)
	}
}

func TestOpenAPIRoute(t *testing.T) {
	h := newHarness(t)
	decodeProblem(t, h.serve(request("GET", "/api/v1/openapi.json", "")), 401, errcode.AuthRequired)

	rec := h.serve(withToken(request("GET", "/api/v1/openapi.json", ""), h.token(core.ScopeRead)))
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
		"/healthz":                     "get",
		"/api/v1/auth/login":           "post",
		"/api/v1/openapi.json":         "get",
		"/api/v1/changes/{id}/confirm": "post",
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
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestHealthz|TestOpenAPIRoute|TestRoutes' ./internal/controller/api/`
Expected: FAIL — `TestHealthz` fails with `no credentials: 401 ... auth.required`, `TestOpenAPIRoute` with `404`, and `TestRoutes` with `want 9 routes` (7 listed).

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/meta.go`:

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
```

Replace the whole of `internal/controller/api/routes.go` with:

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

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestHealthz|TestOpenAPIRoute|TestRoutes' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/meta.go internal/controller/api/routes.go internal/controller/api/meta_test.go
git commit -m "feat(api): add health and OpenAPI routes"
```


### Task 23: Module enable and disable

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Manifest`'s action list)
- Create: `internal/controller/coreactions/enablement.go`
- Test: `internal/controller/coreactions/enablement_test.go`

**Interfaces:**
- Consumes: Contracts › Task 18 (`module`, `moduleStates`, `hostExists`, `querier`, `ModuleState`, `ReasonNotReported`, the test environment); `core.HostDirectory`, `core.AgentClient.Preflight`, `core.ModuleStatus`, `coretest.FakeAgent` (foundation).
- Produces: Contracts › Task 23 (`LockModule`, `ModuleInput`, the two actions, `errUnset`, `stateHash`, `formatTime`, the test helpers `operator`, `plan`, `apply`, `run`, `verify`, `preflight`, `newModuleEnv`).

**Items:** 5, 52

**Depends on:** Task 18

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`core.module.enable` and `core.module.disable` compensate each other and share the lock `core.module`. Enable runs the module's preflight again through `Env.Agents` in `Apply` (never in `Plan`, which must not touch the host) and records the result even when the preflight fails: enabled is the operator's intent, availability is the host's. The state hash covers only the host, the module and its enablement, so an agent hello that rewrites availability does not invalidate a plan, while a concurrent enable or disable does. `Verify` reads the stored row, never the output, because recovery passes none.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/enablement_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// operator is a network principal with the admin scope.
var operator = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin, Source: "127.0.0.1"}

func (e *testEnv) plan(p core.Principal, name string, in any) (core.PlanView, error) {
	return e.engine.Plan(context.Background(), p, ref(name), encodeInput(e.t, in))
}

func (e *testEnv) apply(p core.Principal, planID, confirmation string) (core.ChangeView, error) {
	return e.engine.Apply(context.Background(), p, planID, core.ApplyOptions{Confirmation: confirmation})
}

// run plans and applies name, repeating the plan's confirmation phrase, and
// fails the test unless the change completes; it decodes the apply
// response's output into out when out is not nil.
func (e *testEnv) run(p core.Principal, name string, in, out any) core.ChangeView {
	e.t.Helper()
	pv, err := e.plan(p, name, in)
	if err != nil {
		e.t.Fatalf("plan %s: %v", name, err)
	}
	v, err := e.apply(p, pv.PlanID, pv.Confirmation)
	if err != nil {
		e.t.Fatalf("apply %s: %v", name, err)
	}
	if v.State != core.StateCompleted {
		e.t.Fatalf("apply %s: state %s, error %+v", name, v.State, v.Error)
	}
	if out != nil {
		if err := json.Unmarshal(v.Output, out); err != nil {
			e.t.Fatalf("%s output %s: %v", name, v.Output, err)
		}
	}
	return v
}

// verify runs name's Verify as startup recovery does: with a null output.
func (e *testEnv) verify(name string, in any) error {
	def, ok := e.reg.Action(ref(name))
	if !ok {
		e.t.Fatalf("%s is not registered", name)
	}
	env := core.Env{DB: e.db, Clock: e.clock, Principal: core.Principal{Actor: "system:recovery", Scope: core.ScopeAdmin}}
	return def.Handler.Verify(context.Background(), env, encodeInput(e.t, in), json.RawMessage("null"))
}

// preflight answers every Preflight with status and counts the calls.
func preflight(status core.ModuleStatus, calls *int) *coretest.FakeAgent {
	return &coretest.FakeAgent{PreflightFunc: func(_ context.Context, moduleID string) (core.ModuleStatus, error) {
		*calls++
		status.ID = moduleID
		return status, nil
	}}
}

func newModuleEnv(t *testing.T) *testEnv {
	e := newTestEnv(t, fakeModule{"demo", 3})
	e.addHost("h1", "alpha", true)
	return e
}

func TestModuleEnable(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.enable", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "module demo on host h1: disabled"},
		{Op: "+", Text: "module demo on host h1: enabled; its preflight checks run again and set its availability"},
		{Op: " ", Text: "compensated by core.module.disable"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v, no guards, no confirmation", pv, wantDiff)
	}
	if calls != 0 {
		t.Errorf("plan ran preflight %d times; plans must not touch the host", calls)
	}

	e.clock.Advance(time.Minute)
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	if v.CompensatedBy != "core.module.disable" || calls != 1 {
		t.Errorf("compensated by %q, preflight calls %d; want core.module.disable, 1", v.CompensatedBy, calls)
	}
	var got ModuleState
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	want := ModuleState{ID: "demo", Version: 3, Enabled: true, Available: true, CheckedAt: ptr(t0.Add(time.Minute))}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v", got, want)
	}
	if err := e.verify("core.module.enable", in); err != nil {
		t.Errorf("verify after enable: %v", err)
	}
	if err := e.verify("core.module.disable", in); err == nil {
		t.Error("disable's verify passed on an enabled module")
	}
}

func TestModuleEnableRecordsFailedPreflight(t *testing.T) {
	e := newModuleEnv(t)
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Reason: "ufw is not installed"}, &calls)

	pv, err := e.plan(operator, "core.module.enable", ModuleInput{HostID: "h1", ModuleID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if pv.Diff[0] != (core.DiffLine{Op: "-", Text: "module demo on host h1: not reported"}) {
		t.Errorf("diff = %+v, want the module shown as not reported", pv.Diff)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var got ModuleList
	if err := e.read(operator, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	want := []ModuleState{{ID: "demo", Version: 3, Enabled: true, Reason: ptr("ufw is not installed"), CheckedAt: ptr(t0)}}
	if !reflect.DeepEqual(got.Modules, want) {
		t.Errorf("modules = %+v, want %+v", got.Modules, want)
	}
}

func TestModuleEnableRefusals(t *testing.T) {
	e := newModuleEnv(t)
	for _, tt := range []struct {
		name string
		in   ModuleInput
		code errcode.Code
	}{
		{"core module", ModuleInput{HostID: "h1", ModuleID: "core"}, errcode.InvalidInput},
		{"unknown module", ModuleInput{HostID: "h1", ModuleID: "nope"}, errcode.NotFound},
		{"unknown host", ModuleInput{HostID: "h9", ModuleID: "demo"}, errcode.NotFound},
		{"empty host", ModuleInput{ModuleID: "demo"}, errcode.InvalidInput},
	} {
		for _, action := range []string{"core.module.enable", "core.module.disable"} {
			_, err := e.plan(operator, action, tt.in)
			var ce *errcode.Error
			if !errors.As(err, &ce) || ce.Code != tt.code {
				t.Errorf("%s, %s: err = %v, want %s", tt.name, action, err, tt.code)
			}
		}
	}
}

func TestModuleEnableFailsWithoutTheAgent(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	pv, err := e.plan(operator, "core.module.enable", ModuleInput{HostID: "h1", ModuleID: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.NotFound) {
		t.Errorf("change = %+v, want failed with not_found from the host directory", v)
	}
	var got ModuleList
	if err := e.read(operator, "core.module.list", ModuleListInput{HostID: "h1"}, &got); err != nil {
		t.Fatal(err)
	}
	if got.Modules[0].Enabled {
		t.Error("a failed enable left the module enabled")
	}
}

func TestModuleDisable(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", true, true, "")
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.disable", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "module demo on host h1: enabled"},
		{Op: "+", Text: "module demo on host h1: disabled; its actions and resources answer module.unavailable"},
		{Op: " ", Text: "compensated by core.module.enable"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) {
		t.Errorf("diff = %+v, want %+v", pv.Diff, wantDiff)
	}
	v, err := e.apply(operator, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.module.enable" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var got ModuleState
	if err := json.Unmarshal(v.Output, &got); err != nil {
		t.Fatal(err)
	}
	want := ModuleState{ID: "demo", Version: 3, Available: true, CheckedAt: ptr(t0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v (availability kept)", got, want)
	}
	if err := e.verify("core.module.disable", in); err != nil {
		t.Errorf("verify after disable: %v", err)
	}

	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	var back ModuleState
	e.run(operator, "core.module.enable", in, &back)
	if !back.Enabled || calls != 1 {
		t.Errorf("compensating enable = %+v after %d preflight calls", back, calls)
	}
}

func TestModuleDisableNotReported(t *testing.T) {
	e := newModuleEnv(t)
	var got ModuleState
	e.run(operator, "core.module.disable", ModuleInput{HostID: "h1", ModuleID: "demo"}, &got)
	want := ModuleState{ID: "demo", Version: 3, Reason: ptr(ReasonNotReported), CheckedAt: ptr(t0)}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("output = %+v, want %+v", got, want)
	}
}

func TestModuleStateHash(t *testing.T) {
	e := newModuleEnv(t)
	e.setModuleState("h1", "demo", false, true, "")
	calls := 0
	e.hosts["h1"] = preflight(core.ModuleStatus{Version: 3, Available: true}, &calls)
	in := ModuleInput{HostID: "h1", ModuleID: "demo"}

	pv, err := e.plan(operator, "core.module.enable", in)
	if err != nil {
		t.Fatal(err)
	}
	// An agent hello rewrites availability; the plan still applies.
	e.setModuleState("h1", "demo", false, false, "tool missing")
	if v, err := e.apply(operator, pv.PlanID, ""); err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply after an availability change = %+v, %v", v, err)
	}

	pv, err = e.plan(operator, "core.module.disable", in)
	if err != nil {
		t.Fatal(err)
	}
	e.setModuleState("h1", "demo", false, true, "")
	_, err = e.apply(operator, pv.PlanID, "")
	wantCode(t, err, errcode.PlanStateChanged)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestModuleEnable|TestModuleEnableRecordsFailedPreflight|TestModuleEnableRefusals|TestModuleEnableFailsWithoutTheAgent|TestModuleDisable|TestModuleDisableNotReported|TestModuleStateHash)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/enablement_test.go:81:8: undefined: ModuleInput
internal/controller/coreactions/enablement_test.go:128:52: undefined: ModuleInput
internal/controller/coreactions/enablement_test.go:153:8: undefined: ModuleInput
internal/controller/coreactions/enablement_test.go:156:19: undefined: ModuleInput
```

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/module.go`, add the line `m.moduleActions(),` after `m.hostActions(),` in `Manifest`'s list, so the function reads:

```go
// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}
```

Create `internal/controller/coreactions/enablement.go`:

```go
package coreactions

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// LockModule is the lock key of core.module.enable and core.module.disable.
const LockModule = "core.module"

// ModuleInput is the input of core.module.enable/v1 and core.module.disable/v1.
type ModuleInput struct {
	HostID   string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the host."`
	ModuleID string `json:"moduleId" jsonschema:"minLength=1" jsonschema_description:"Id of a compiled-in module other than core."`
}

func (m module) moduleActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[ModuleInput, ModuleState](core.ActionSpec{
			Name: "core.module.enable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			LockKeys: []string{LockModule}, CompensatedBy: "core.module.disable",
			Summary: "Enable a module on a host; its preflight checks run there again and set its availability.",
		}, enableModule{m}),
		core.DefineMutating[ModuleInput, ModuleState](core.ActionSpec{
			Name: "core.module.disable", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			LockKeys: []string{LockModule}, CompensatedBy: "core.module.enable",
			Summary: "Disable a module on a host; its actions and resources answer module.unavailable.",
		}, disableModule{m}),
	}
}

// The enablement of a module on a host as a plan sees it.
const (
	stateEnabled     = "enabled"
	stateDisabled    = "disabled"
	stateNotReported = "not reported"
)

func errUnset(name string) error {
	return fmt.Errorf("coreactions: %s is not set", name)
}

// stateHash is the hex SHA-256 of parts, each prefixed with its length so no
// two lists of parts hash alike.
func stateHash(parts ...string) string {
	var sb strings.Builder
	for _, p := range parts {
		fmt.Fprintf(&sb, "%d:%s;", len(p), p)
	}
	sum := sha256.Sum256([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

const timeFormat = "2006-01-02T15:04:05.000000000Z"

func formatTime(t time.Time) string { return t.UTC().Format(timeFormat) }

// target checks that in names a known host and a compiled-in module other
// than core, and returns the module's enablement on the host.
func (m module) target(ctx context.Context, q querier, in ModuleInput) (string, error) {
	if in.ModuleID == core.ModuleIDCore {
		return "", &errcode.Error{
			Code: errcode.InvalidInput, Detail: "the core module is always enabled",
			Fields: []errcode.FieldError{{Path: "/moduleId", Message: "the core module cannot be enabled or disabled"}},
		}
	}
	known := false
	for _, info := range m.catalog {
		known = known || info.id == in.ModuleID
	}
	if !known {
		return "", errcode.New(errcode.NotFound, fmt.Sprintf("no module %s in this binary", in.ModuleID))
	}
	if err := hostExists(ctx, q, in.HostID); err != nil {
		return "", err
	}
	var enabled bool
	err := q.QueryRowContext(ctx, `SELECT enabled FROM core_module_state WHERE host_id = ? AND module_id = ?`,
		in.HostID, in.ModuleID).Scan(&enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return stateNotReported, nil
	case err != nil:
		return "", fmt.Errorf("coreactions: read module state: %w", err)
	case enabled:
		return stateEnabled, nil
	default:
		return stateDisabled, nil
	}
}

// state returns one module's state on one host.
func (m module) state(ctx context.Context, q querier, hostID, moduleID string) (ModuleState, error) {
	states, err := m.moduleStates(ctx, q, hostID)
	if err != nil {
		return ModuleState{}, err
	}
	for _, s := range states {
		if s.ID == moduleID {
			return s, nil
		}
	}
	return ModuleState{}, errcode.New(errcode.NotFound, fmt.Sprintf("no module %s in this binary", moduleID))
}

// wantEnabled is Verify of both actions: it decides from the stored row
// alone, because recovery passes no output.
func wantEnabled(ctx context.Context, env core.Env, in ModuleInput, want bool) error {
	var enabled bool
	err := env.DB.Read().QueryRowContext(ctx, `SELECT enabled FROM core_module_state WHERE host_id = ? AND module_id = ?`,
		in.HostID, in.ModuleID).Scan(&enabled)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("coreactions: module %s has no state on host %s", in.ModuleID, in.HostID)
	case err != nil:
		return fmt.Errorf("coreactions: read module state: %w", err)
	case enabled != want:
		return fmt.Errorf("coreactions: module %s on host %s: enabled is %t, want %t", in.ModuleID, in.HostID, enabled, want)
	}
	return nil
}

type enableModule struct{ m module }

func (h enableModule) Plan(ctx context.Context, env core.Env, in ModuleInput) (core.PlanOutcome, error) {
	cur, err := h.m.target(ctx, env.DB.Read(), in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("module %s on host %s", in.ModuleID, in.HostID)
	var diff []core.DiffLine
	if cur == stateEnabled {
		diff = append(diff, core.DiffLine{Op: " ", Text: subject + ": already enabled; its preflight checks run again"})
	} else {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: subject + ": " + cur},
			core.DiffLine{Op: "+", Text: subject + ": enabled; its preflight checks run again and set its availability"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.module.disable"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(in.HostID, in.ModuleID, cur)}, nil
}

func (h enableModule) Apply(ctx context.Context, env core.Env, in ModuleInput) (ModuleState, error) {
	if env.Agents == nil {
		return ModuleState{}, errUnset("Env.Agents")
	}
	agent, err := env.Agents.Agent(ctx, in.HostID)
	if err != nil {
		return ModuleState{}, err
	}
	status, err := agent.Preflight(ctx, in.ModuleID)
	if err != nil {
		return ModuleState{}, err
	}
	var reason any
	if !status.Available {
		reason = status.Reason
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES (?, ?, 1, ?, ?, ?)
			ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = 1, available = excluded.available,
			reason = excluded.reason, checked_at = excluded.checked_at`,
			in.HostID, in.ModuleID, status.Available, reason, formatTime(env.Clock.Now()))
		return err
	})
	if err != nil {
		return ModuleState{}, fmt.Errorf("coreactions: enable module: %w", err)
	}
	return h.m.state(ctx, env.DB.Read(), in.HostID, in.ModuleID)
}

func (h enableModule) Verify(ctx context.Context, env core.Env, in ModuleInput, _ ModuleState) error {
	return wantEnabled(ctx, env, in, true)
}

type disableModule struct{ m module }

func (h disableModule) Plan(ctx context.Context, env core.Env, in ModuleInput) (core.PlanOutcome, error) {
	cur, err := h.m.target(ctx, env.DB.Read(), in)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("module %s on host %s", in.ModuleID, in.HostID)
	var diff []core.DiffLine
	if cur == stateDisabled {
		diff = append(diff, core.DiffLine{Op: " ", Text: subject + ": already disabled; nothing changes"})
	} else {
		diff = append(diff,
			core.DiffLine{Op: "-", Text: subject + ": " + cur},
			core.DiffLine{Op: "+", Text: subject + ": disabled; its actions and resources answer module.unavailable"})
	}
	diff = append(diff, core.DiffLine{Op: " ", Text: "compensated by core.module.enable"})
	return core.PlanOutcome{Diff: diff, Guards: []core.Guard{}, StateHash: stateHash(in.HostID, in.ModuleID, cur)}, nil
}

func (h disableModule) Apply(ctx context.Context, env core.Env, in ModuleInput) (ModuleState, error) {
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_module_state (host_id, module_id, enabled, available, reason, checked_at)
			VALUES (?, ?, 0, 0, ?, ?)
			ON CONFLICT (host_id, module_id) DO UPDATE SET enabled = 0`,
			in.HostID, in.ModuleID, ReasonNotReported, formatTime(env.Clock.Now()))
		return err
	})
	if err != nil {
		return ModuleState{}, fmt.Errorf("coreactions: disable module: %w", err)
	}
	return h.m.state(ctx, env.DB.Read(), in.HostID, in.ModuleID)
}

func (h disableModule) Verify(ctx context.Context, env core.Env, in ModuleInput, _ ModuleState) error {
	return wantEnabled(ctx, env, in, false)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestModuleEnable|TestModuleEnableRecordsFailedPreflight|TestModuleEnableRefusals|TestModuleEnableFailsWithoutTheAgent|TestModuleDisable|TestModuleDisableNotReported|TestModuleStateHash)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/enablement.go \
  internal/controller/coreactions/enablement_test.go
git commit -m "feat(coreactions): enable and disable modules"
```


### Task 24: Token actions

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Manifest`'s action list)
- Create: `internal/controller/coreactions/tokens.go`
- Test: `internal/controller/coreactions/tokens_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 18 and 23 (`module`, `NoInput`, `errUnset`, `stateHash`, `formatTime`, the test environment and `plan`, `apply`, `run`, `verify`); `auth.Service.ListUsers`, `CreateToken`, `ListTokens`, `GetToken`, `RevokeToken`, `auth.TokenInfo`, `auth.User`, `auth.StateActive`, `auth.DefaultTokenDays`, `auth.TokenPrefix`, `auth.TOTPCode`, `auth.TOTPPeriod` (Tasks 2, 4 and 6).
- Produces: Contracts › Task 24 (`TokenList`, `TokenCreateInput`, `NewToken`, `TokenRevokeInput`, the three actions, `authService`, `tokenOwner`, the test helpers `testPassword`, `noSecret`, `activeUser`).

**Items:** 8, 13, 52

**Depends on:** Task 23

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Token actions work on the calling user's tokens (`Principal.UserID`); a local or system principal has no user and gets `auth.forbidden`. The new token is a `secret:"true"` output field: the apply response carries it once, and the engine stores and journals it as `"[redacted]"`, which the test checks together with a scan of the plan, the stored change and the audit rows for the token and its secret part. Revoke is irreversible and its confirmation phrase is the token id. Recovery verifies as `system:recovery`, which has no user, so an interrupted token change is reported unverified rather than guessed.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/tokens_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

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
// and returns the user and its principal on a network listener. It then
// advances the clock by one TOTP period, so the next code is a fresh step.
func (e *testEnv) activeUser(username string) (auth.User, core.Principal) {
	e.t.Helper()
	ctx := context.Background()
	_, totp, err := e.auth.CreateUser(ctx, username, testPassword)
	if err != nil {
		e.t.Fatal(err)
	}
	code, err := auth.TOTPCode(totp, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	u, err := e.auth.ActivateUser(ctx, username, code)
	if err != nil {
		e.t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)
	return u, core.Principal{Actor: "user:" + u.ID, UserID: u.ID, Scope: core.ScopeAdmin, Source: "127.0.0.1"}
}

func TestTokenCreate(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	in := TokenCreateInput{Name: "ci", Scope: core.ScopeOperate, ExpiresInDays: 30}

	pv, err := e.plan(alice, "core.token.create", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: `token "ci" with scope operate for user alice, expiring in 30 days`},
		{Op: " ", Text: "compensated by core.token.revoke with the new token's id"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v", pv, wantDiff)
	}
	v, err := e.apply(alice, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	if v.CompensatedBy != "core.token.revoke" {
		t.Errorf("compensated by %q, want core.token.revoke", v.CompensatedBy)
	}
	var out NewToken
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out.Token, auth.TokenPrefix+out.ID+"_") || out.Name != "ci" || out.Scope != core.ScopeOperate {
		t.Errorf("output = %+v, want the new token of scope operate", out)
	}
	if want := out.CreatedAt.Add(30 * 24 * time.Hour); !out.ExpiresAt.Equal(want) {
		t.Errorf("expiresAt = %v, want %v", out.ExpiresAt, want)
	}

	stored, err := e.engine.Change(context.Background(), v.ID)
	if err != nil {
		t.Fatal(err)
	}
	var redacted map[string]any
	if err := json.Unmarshal(stored.Output, &redacted); err != nil {
		t.Fatal(err)
	}
	if redacted["token"] != "[redacted]" || redacted["id"] != out.ID {
		t.Errorf("stored output = %s, want the token redacted and the id kept", stored.Output)
	}
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{})
	if err != nil || len(entries) != 1 || entries[0].Action != "core.token.create" {
		t.Fatalf("audit = %+v, %v; want one core.token.create row", entries, err)
	}
	noSecret(t, []string{out.Token, strings.TrimPrefix(out.Token, auth.TokenPrefix+out.ID+"_")}, pv, stored, entries)

	var list TokenList
	if err := e.read(alice, "core.token.list", NoInput{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Tokens) != 1 || !reflect.DeepEqual(list.Tokens[0], out.TokenInfo) {
		t.Errorf("tokens = %+v, want [%+v]", list.Tokens, out.TokenInfo)
	}
}

func TestTokenCreateDefaultsAndRefusals(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	pv, err := e.plan(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(pv.Diff[0].Text, "expiring in 90 days") {
		t.Errorf("diff = %+v, want the 90-day default", pv.Diff)
	}
	for _, raw := range []string{
		`{"name":"ci","scope":"root"}`,
		`{"name":"","scope":"read"}`,
		`{"name":"ci","scope":"read","expiresInDays":366}`,
		`{"name":"ci","scope":"read","expiresInDays":0}`,
		`{"name":"ci"}`,
	} {
		_, err := e.engine.Plan(context.Background(), alice, ref("core.token.create"), json.RawMessage(raw))
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput {
			t.Errorf("plan %s: err = %v, want request.invalid_input", raw, err)
		}
	}

	pending, _, err := e.auth.CreateUser(context.Background(), "bob", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	bob := core.Principal{Actor: "user:" + pending.ID, UserID: pending.ID, Scope: core.ScopeAdmin}
	_, err = e.plan(bob, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	wantCode(t, err, errcode.NotFound)
}

func TestTokenRevoke(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)

	pv, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	subject := fmt.Sprintf(`token %s ("ci", scope read)`, tok.ID)
	wantDiff := []core.DiffLine{
		{Op: "-", Text: subject + ": active"},
		{Op: "+", Text: subject + ": revoked; a request that sends it gets auth.required"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != tok.ID {
		t.Errorf("plan = %+v, want diff %+v and the token id as the phrase", pv, wantDiff)
	}
	_, err = e.apply(alice, pv.PlanID, "")
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := e.apply(alice, pv.PlanID, tok.ID)
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out auth.TokenInfo
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.RevokedAt == nil || !out.RevokedAt.Equal(e.clock.Now()) {
		t.Errorf("revokedAt = %v, want %v", out.RevokedAt, e.clock.Now())
	}

	again, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{{Op: " ", Text: subject + ": already revoked at " +
		e.clock.Now().UTC().Format(time.RFC3339) + "; nothing changes"}}
	if !reflect.DeepEqual(again.Diff, want) {
		t.Errorf("diff = %+v, want %+v", again.Diff, want)
	}
}

func TestTokenRevokeStateChanged(t *testing.T) {
	e := newTestEnv(t)
	u, alice := e.activeUser("alice")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)
	pv, err := e.plan(alice, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.RevokeToken(context.Background(), u.ID, tok.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(alice, pv.PlanID, tok.ID)
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestTokensBelongToTheirUser(t *testing.T) {
	e := newTestEnv(t)
	_, alice := e.activeUser("alice")
	_, bob := e.activeUser("bob")
	var tok NewToken
	e.run(alice, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}, &tok)

	var list TokenList
	if err := e.read(bob, "core.token.list", NoInput{}, &list); err != nil || len(list.Tokens) != 0 {
		t.Errorf("bob's tokens = %+v, %v; want none", list.Tokens, err)
	}
	_, err := e.plan(bob, "core.token.revoke", TokenRevokeInput{ID: tok.ID})
	wantCode(t, err, errcode.NotFound)
}

func TestTokenActionsNeedAUser(t *testing.T) {
	e := newTestEnv(t)
	var list TokenList
	wantCode(t, e.read(root, "core.token.list", NoInput{}, &list), errcode.Forbidden)
	_, err := e.plan(root, "core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead})
	wantCode(t, err, errcode.Forbidden)
	_, err = e.plan(root, "core.token.revoke", TokenRevokeInput{ID: "01JZ"})
	wantCode(t, err, errcode.Forbidden)

	// Recovery verifies as system:recovery, which has no user: an interrupted
	// token change is reported unverified rather than guessed.
	wantCode(t, e.verify("core.token.create", TokenCreateInput{Name: "ci", Scope: core.ScopeRead}), errcode.Forbidden)
	wantCode(t, e.verify("core.token.revoke", TokenRevokeInput{ID: "01JZ"}), errcode.Forbidden)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestTokenCreate|TestTokenCreateDefaultsAndRefusals|TestTokenRevoke|TestTokenRevokeStateChanged|TestTokensBelongToTheirUser|TestTokenActionsNeedAUser)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/tokens_test.go:72:8: undefined: TokenCreateInput
internal/controller/coreactions/tokens_test.go:92:10: undefined: NewToken
internal/controller/coreactions/tokens_test.go:120:11: undefined: TokenList
internal/controller/coreactions/tokens_test.go:132:48: undefined: TokenCreateInput
```

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/module.go`, add the line `m.tokenActions(),` after `m.moduleActions(),` in `Manifest`'s list, so the function reads:

```go
// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
		m.tokenActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}
```

Create `internal/controller/coreactions/tokens.go`:

```go
package coreactions

import (
	"context"
	"fmt"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// TokenList is the output of core.token.list/v1.
type TokenList struct {
	Tokens []auth.TokenInfo `json:"tokens" jsonschema_description:"The calling user's tokens, newest first; revoked and expired tokens stay listed until they are purged."`
}

// TokenCreateInput is the input of core.token.create/v1.
type TokenCreateInput struct {
	Name          string     `json:"name" jsonschema:"minLength=1,maxLength=64" jsonschema_description:"A name to recognise the token by, 1 to 64 characters."`
	Scope         core.Scope `json:"scope" jsonschema:"enum=read,enum=operate,enum=admin" jsonschema_description:"What the token may do: read, operate or admin."`
	ExpiresInDays int        `json:"expiresInDays,omitempty" jsonschema:"minimum=1,maximum=365" jsonschema_description:"Days until the token expires, 1 to 365; 90 when omitted."`
}

// NewToken is the output of core.token.create/v1: the new token's description
// and the token itself, which only the apply that created it returns.
type NewToken struct {
	auth.TokenInfo
	Token string `json:"token" secret:"true" jsonschema_description:"The token, sent as Authorization: Bearer <token>; shown only once."`
}

// TokenRevokeInput is the input of core.token.revoke/v1.
type TokenRevokeInput struct {
	ID string `json:"id" jsonschema:"minLength=1" jsonschema_description:"Id of one of the calling user's tokens."`
}

func (m module) tokenActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, TokenList](core.ActionSpec{
			Name: "core.token.list", Version: 1, Side: core.SideController,
			Summary: "List the calling user's API tokens.",
		}, tokenList{m}),
		core.DefineMutating[TokenCreateInput, NewToken](core.ActionSpec{
			Name: "core.token.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController,
			CompensatedBy: "core.token.revoke",
			Summary:       "Create an API token for the calling user; the token is shown only once.",
		}, tokenCreate{m}),
		core.DefineMutating[TokenRevokeInput, auth.TokenInfo](core.ActionSpec{
			Name: "core.token.revoke", Version: 1, Class: core.ClassIrreversible, Side: core.SideController,
			Summary: "Revoke one of the calling user's API tokens; the confirmation phrase is the token id.",
		}, tokenRevoke{m}),
	}
}

func (m module) authService() (*auth.Service, error) {
	if m.d.Auth == nil {
		return nil, errUnset("Deps.Auth")
	}
	return m.d.Auth, nil
}

// tokenOwner returns the service and the calling user's id. Tokens belong to
// users, so a principal without one (a local or system actor) is refused.
func (m module) tokenOwner(p core.Principal) (*auth.Service, string, error) {
	svc, err := m.authService()
	if err != nil {
		return nil, "", err
	}
	if p.UserID == "" {
		return nil, "", errcode.New(errcode.Forbidden, "token actions need a user's session or token")
	}
	return svc, p.UserID, nil
}

type tokenList struct{ m module }

func (h tokenList) Read(ctx context.Context, env core.Env, _ NoInput) (TokenList, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return TokenList{}, err
	}
	tokens, err := svc.ListTokens(ctx, userID)
	if err != nil {
		return TokenList{}, err
	}
	return TokenList{Tokens: tokens}, nil
}

type tokenCreate struct{ m module }

func (h tokenCreate) Plan(ctx context.Context, env core.Env, in TokenCreateInput) (core.PlanOutcome, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	var owner auth.User
	for _, u := range users {
		if u.ID == userID {
			owner = u
		}
	}
	if owner.State != auth.StateActive {
		return core.PlanOutcome{}, errcode.New(errcode.NotFound, "the calling user is not an active user")
	}
	days := in.ExpiresInDays
	if days == 0 {
		days = auth.DefaultTokenDays
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("token %q with scope %s for user %s, expiring in %d days", in.Name, in.Scope, owner.Username, days)},
			{Op: " ", Text: "compensated by core.token.revoke with the new token's id"},
		},
		Guards:    []core.Guard{},
		StateHash: stateHash(owner.ID, owner.State),
	}, nil
}

func (h tokenCreate) Apply(ctx context.Context, env core.Env, in TokenCreateInput) (NewToken, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return NewToken{}, err
	}
	info, token, err := svc.CreateToken(ctx, userID, in.Name, in.Scope, in.ExpiresInDays)
	if err != nil {
		return NewToken{}, err
	}
	return NewToken{TokenInfo: info, Token: token}, nil
}

// Verify finds the new token by the id in out. Recovery passes no output and
// a principal without a user, so an interrupted create is reported as not
// verified; the operator lists the tokens and revokes an unexpected one.
func (h tokenCreate) Verify(ctx context.Context, env core.Env, _ TokenCreateInput, out NewToken) error {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return err
	}
	info, err := svc.GetToken(ctx, userID, out.ID)
	if err != nil {
		return err
	}
	if info.RevokedAt != nil {
		return fmt.Errorf("coreactions: token %s is revoked", info.ID)
	}
	return nil
}

type tokenRevoke struct{ m module }

func (h tokenRevoke) Plan(ctx context.Context, env core.Env, in TokenRevokeInput) (core.PlanOutcome, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	info, err := svc.GetToken(ctx, userID, in.ID)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	subject := fmt.Sprintf("token %s (%q, scope %s)", info.ID, info.Name, info.Scope)
	state := "active"
	var diff []core.DiffLine
	if info.RevokedAt != nil {
		state = formatTime(*info.RevokedAt)
		diff = []core.DiffLine{{Op: " ", Text: fmt.Sprintf("%s: already revoked at %s; nothing changes",
			subject, info.RevokedAt.UTC().Format(time.RFC3339))}}
	} else {
		diff = []core.DiffLine{
			{Op: "-", Text: subject + ": active"},
			{Op: "+", Text: subject + ": revoked; a request that sends it gets auth.required"},
		}
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(userID, info.ID, state),
		Confirmation: info.ID,
	}, nil
}

func (h tokenRevoke) Apply(ctx context.Context, env core.Env, in TokenRevokeInput) (auth.TokenInfo, error) {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return auth.TokenInfo{}, err
	}
	return svc.RevokeToken(ctx, userID, in.ID)
}

// Verify reads the token from in. Recovery's principal has no user, so an
// interrupted revoke is reported as not verified.
func (h tokenRevoke) Verify(ctx context.Context, env core.Env, in TokenRevokeInput, _ auth.TokenInfo) error {
	svc, userID, err := h.m.tokenOwner(env.Principal)
	if err != nil {
		return err
	}
	info, err := svc.GetToken(ctx, userID, in.ID)
	if err != nil {
		return err
	}
	if info.RevokedAt == nil {
		return fmt.Errorf("coreactions: token %s is not revoked", info.ID)
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestTokenCreate|TestTokenCreateDefaultsAndRefusals|TestTokenRevoke|TestTokenRevokeStateChanged|TestTokensBelongToTheirUser|TestTokenActionsNeedAUser)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/tokens.go \
  internal/controller/coreactions/tokens_test.go
git commit -m "feat(coreactions): create, list and revoke tokens"
```


### Task 25: Controller socket and break-glass routes

**Files:**
- Create: `internal/controller/api/socket.go`
- Test: `internal/controller/api/socket_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 11, 16 and 19 (`Server`, `Config.Dev`, `Config.HostID`, `Config.WebCA`, `caller`, `withCaller`, `unrouted`, the harness), Task 21 (`lookupAction`, `runAction`, `stubDispatcher`) and Task 22 (`writeHealthDetail`); `golang.org/x/sys/unix` v0.49.0 (`GetsockoptUcred`, pinned).
- Produces: Contracts › Task 25 (`SocketSource`, `PeerUIDFunc`, `SocketPeerUID`, `ListenSocket`, `Server.SocketListener`, `Server.SocketServer`, `Server.SocketHandler`, `webCAResponse`).

**Items:** 11

**Depends on:** Tasks 11, 16, 19, 21 and 22

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The socket's peer credentials are its authentication (spec section 14). Every accepted connection is checked twice: `SocketListener` closes a connection whose `SO_PEERCRED` uid is not allowed before HTTP starts, and `SocketHandler` refuses a request whose context lacks an allowed uid. Root is always allowed; the service's own uid only in `--dev` (ADR 0011). The tests inject uids through `PeerUIDFunc` and the `getuid` variable; one test runs over a real unix socket in `os.MkdirTemp("", "dp")`, checks mode 0600 and that the uid read is this process's. That test sets `Dev`, so it also passes when not run as root.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/socket_test.go`:

```go
package api

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
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// setUID makes the service's own uid uid for the test.
func setUID(t *testing.T, uid int) {
	t.Helper()
	old := getuid
	getuid = func() int { return uid }
	t.Cleanup(func() { getuid = old })
}

// socketRequest serves r on the socket handler as if from a peer with uid.
func (h *harness) socketRequest(r *http.Request, uid uint32) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	h.srv.SocketHandler().ServeHTTP(rec, r.WithContext(withPeerUID(r.Context(), uid)))
	return rec
}

func TestSocketPeers(t *testing.T) {
	h := newHarness(t)
	setUID(t, 1000)
	for _, tc := range []struct {
		uid   uint32
		dev   bool
		actor string // "" means refused
	}{
		{0, false, "local:root@" + testHostID},
		{0, true, "local:root@" + testHostID},
		{1000, false, ""},
		{1000, true, "local:uid-1000@" + testHostID},
		{1001, true, ""},
		{1001, false, ""},
	} {
		h.srv.cfg.Dev = tc.dev
		if got := h.srv.socketAllowed(tc.uid); got != (tc.actor != "") {
			t.Errorf("uid %d dev %v: allowed %v", tc.uid, tc.dev, got)
		}
		var last call
		h.stubDispatcher(&last)
		rec := h.socketRequest(request("POST", "/api/v1/actions/demo.secret.show/v1/read", `{"input":{}}`), tc.uid)
		if tc.actor == "" {
			decodeProblem(t, rec, 403, errcode.Forbidden)
			continue
		}
		want := core.Principal{Actor: tc.actor, Scope: core.ScopeAdmin, Source: SocketSource}
		if rec.Code != 200 || last.principal != want {
			t.Errorf("uid %d dev %v: %d %s, principal %+v, want %+v", tc.uid, tc.dev, rec.Code, rec.Body, last.principal, want)
		}
	}

	rec := httptest.NewRecorder()
	h.srv.SocketHandler().ServeHTTP(rec, request("GET", "/healthz", ""))
	decodeProblem(t, rec, 403, errcode.Forbidden)
}

func TestSocketRoutes(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)

	rec := h.socketRequest(request("GET", "/healthz", ""), 0)
	want := fmt.Sprintf(`{"status":"ok","version":%q,"commit":%q,"schemaVersion":3}`, buildinfo.Version, buildinfo.Commit)
	if rec.Code != 200 || rec.Body.String() != want {
		t.Errorf("healthz: %d %s", rec.Code, rec.Body)
	}

	rec = h.socketRequest(request("GET", "/api/v1/controller/web-ca", ""), 0)
	var ca webCAResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ca); err != nil || rec.Code != 200 ||
		ca.PEM != string((fakeWebCA{}).RootPEM()) || ca.Fingerprint != (fakeWebCA{}).Fingerprint() {
		t.Errorf("web-ca: %d %s", rec.Code, rec.Body)
	}

	for path, method := range map[string]string{
		"/api/v1/actions/demo.secret.reset/v1/plan":  "Plan",
		"/api/v1/actions/demo.secret.reset/v1/apply": "Apply",
		"/api/v1/actions/demo.secret.show/v1/read":   "Read",
	} {
		last = call{}
		body := `{"input":{"name":"x"}}`
		if method == "Apply" {
			body = `{"planId":"plan1","confirmation":"x"}`
		}
		rec := h.socketRequest(request("POST", path, body), 0)
		if rec.Code != 200 || last.method != method || last.hostID != "" || last.principal.Actor != "local:root@"+testHostID {
			t.Errorf("%s: %d %s, dispatched %+v", path, rec.Code, rec.Body, last)
		}
	}

	for _, tc := range []struct {
		method, path, body string
		status             int
		code               errcode.Code
	}{
		{"POST", "/api/v1/actions/demo.item.remove/v1/plan", `{"input":{}}`, 403, errcode.Forbidden},
		{"POST", "/api/v1/actions/demo.item.list/v1/read", `{"input":{}}`, 403, errcode.Forbidden},
		{"POST", "/api/v1/actions/demo.secret.nope/v1/plan", `{"input":{}}`, 404, errcode.ActionUnknown},
		{"POST", "/api/v1/hosts/h1/actions/demo.item.add/v1/plan", `{"input":{}}`, 404, errcode.NotFound},
		{"GET", "/api/v1/changes", "", 404, errcode.NotFound},
		{"POST", "/api/v1/auth/login", `{}`, 404, errcode.NotFound},
		{"GET", "/", "", 404, errcode.NotFound},
		{"GET", "/api/v1/actions/demo.secret.reset/v1/plan", "", 405, errcode.MethodNotAllowed},
		{"POST", "/api/v1/actions/demo.secret.reset/v1/plan", `{"input":{}`, 400, errcode.InvalidJSON},
	} {
		last = call{}
		decodeProblem(t, h.socketRequest(request(tc.method, tc.path, tc.body), 0), tc.status, tc.code)
		if last.method != "" {
			t.Errorf("%s %s: dispatched %s", tc.method, tc.path, last.method)
		}
	}
}

// pipeListener hands out the server ends of net.Pipe connections.
type pipeListener struct {
	conns  chan net.Conn
	closed chan struct{}
	once   sync.Once
}

func newPipeListener() *pipeListener {
	return &pipeListener{conns: make(chan net.Conn), closed: make(chan struct{})}
}

func (l *pipeListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.conns:
		return c, nil
	case <-l.closed:
		return nil, net.ErrClosed
	}
}

func (l *pipeListener) Close() error {
	l.once.Do(func() { close(l.closed) })
	return nil
}

func (l *pipeListener) Addr() net.Addr { return &net.UnixAddr{Name: "pipe", Net: "unix"} }

func TestSocketListenerRefusesPeers(t *testing.T) {
	h := newHarness(t)
	setUID(t, 1000)
	h.srv.cfg.Dev = true
	pl := newPipeListener()
	var mu sync.Mutex
	uids := map[net.Conn]uint32{}
	peerUID := func(c net.Conn) (uint32, error) {
		mu.Lock()
		defer mu.Unlock()
		uid, ok := uids[c]
		if !ok {
			return 0, errors.New("no credentials")
		}
		return uid, nil
	}
	ln := h.srv.SocketListener(pl, peerUID)
	accepted := make(chan net.Conn)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				close(accepted)
				return
			}
			accepted <- c
		}
	}()
	defer func() {
		_ = ln.Close()
		for range accepted {
		}
	}()

	for _, tc := range []struct {
		name     string
		uid      uint32
		known    bool
		accepted bool
	}{
		{"another user", 1001, true, false},
		{"unreadable credentials", 0, false, false},
		{"root", 0, true, true},
		{"the service's own uid in dev mode", 1000, true, true},
	} {
		server, client := net.Pipe()
		mu.Lock()
		if tc.known {
			uids[server] = tc.uid
		}
		mu.Unlock()
		pl.conns <- server
		if !tc.accepted {
			_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
			if _, err := client.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
				t.Errorf("%s: read %v, want the connection closed", tc.name, err)
			}
			continue
		}
		select {
		case c := <-accepted:
			if pc, ok := c.(*peerConn); !ok || pc.uid != tc.uid {
				t.Errorf("%s: accepted %#v", tc.name, c)
			}
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: not accepted", tc.name)
		}
		_ = client.Close()
	}
}

func TestSocketOverUnixSocket(t *testing.T) {
	h := newHarness(t)
	h.srv.cfg.Dev = true
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "controller.sock")
	if err := os.WriteFile(path, []byte("not a socket"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := ListenSocket(path); err == nil || !strings.Contains(err.Error(), "not a socket") {
		t.Fatalf("ListenSocket over a regular file: %v", err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}

	ln, err := ListenSocket(path)
	if err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("socket mode %v, %v; want 0600", st.Mode(), err)
	}
	if _, err := ListenSocket(path); err == nil || !strings.Contains(err.Error(), "another process") {
		t.Errorf("ListenSocket over a live socket: %v", err)
	}

	var seen []uint32
	var mu sync.Mutex
	recordUID := func(c net.Conn) (uint32, error) {
		uid, err := SocketPeerUID(c)
		mu.Lock()
		seen = append(seen, uid)
		mu.Unlock()
		return uid, err
	}
	srv := h.srv.SocketServer()
	done := make(chan error, 1)
	go func() { done <- srv.Serve(h.srv.SocketListener(ln, recordUID)) }()
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Error(err)
		}
	}()

	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, "unix", path)
		},
	}}
	defer client.CloseIdleConnections()
	resp, err := client.Get("http://controller/healthz")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"schemaVersion":3`) {
		t.Errorf("healthz over the socket: %d %s", resp.StatusCode, body)
	}
	mu.Lock()
	defer mu.Unlock()
	// ListenSocket's probe of the live socket is accepted too, so every
	// connection so far must show this process's uid.
	if len(seen) == 0 {
		t.Fatal("no peer uid was read")
	}
	for _, uid := range seen {
		if int64(uid) != int64(os.Getuid()) {
			t.Errorf("peer uids %v, want only %d", seen, os.Getuid())
		}
	}
}

func TestSocketPeerUIDRefusesOtherConns(t *testing.T) {
	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }()
	if _, err := SocketPeerUID(a); err == nil {
		t.Error("SocketPeerUID read credentials of a pipe")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSocket' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: getuid`, `h.srv.SocketHandler undefined`, `undefined: withPeerUID`, `h.srv.socketAllowed undefined` and `undefined: SocketSource`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/socket.go`:

```go
package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"os"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// SocketSource is the Principal.Source of every controller socket request.
const SocketSource = "socket"

// The controller socket server's timeouts, the listeners' values of spec
// section 12.
const (
	socketReadHeaderTimeout = 10 * time.Second
	socketReadTimeout       = 30 * time.Second
	socketWriteTimeout      = 60 * time.Second
	socketIdleTimeout       = 120 * time.Second
)

// getuid is the service's own uid; tests replace it.
var getuid = os.Getuid

// PeerUIDFunc returns the uid of the process at the other end of c.
type PeerUIDFunc func(c net.Conn) (uint32, error)

// SocketPeerUID reads the peer's uid of a unix connection with SO_PEERCRED.
func SocketPeerUID(c net.Conn) (uint32, error) {
	uc, ok := c.(*net.UnixConn)
	if !ok {
		return 0, fmt.Errorf("api: peer credentials of a %T", c)
	}
	raw, err := uc.SyscallConn()
	if err != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", err)
	}
	var cred *unix.Ucred
	var credErr error
	if err := raw.Control(func(fd uintptr) {
		cred, credErr = unix.GetsockoptUcred(int(fd), unix.SOL_SOCKET, unix.SO_PEERCRED)
	}); err != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", err)
	}
	if credErr != nil {
		return 0, fmt.Errorf("api: peer credentials: %w", credErr)
	}
	return cred.Uid, nil
}

// ListenSocket listens on the controller socket at path with mode 0600. It
// replaces a stale socket file, but refuses a path that is not a socket or
// that another process still serves. Until the chmod the file has the
// umask's mode; the peer check on every connection covers that window.
func ListenSocket(path string) (net.Listener, error) {
	if st, err := os.Lstat(path); err == nil {
		if st.Mode().Type() != fs.ModeSocket {
			return nil, fmt.Errorf("api: %s exists and is not a socket", path)
		}
		if c, err := net.DialTimeout("unix", path, time.Second); err == nil {
			_ = c.Close()
			return nil, fmt.Errorf("api: %s is served by another process", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, fmt.Errorf("api: remove stale socket: %w", err)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("api: %w", err)
	}
	l, err := net.Listen("unix", path)
	if err != nil {
		return nil, fmt.Errorf("api: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		_ = l.Close()
		return nil, fmt.Errorf("api: %w", err)
	}
	return l, nil
}

// socketAllowed reports whether a peer uid may use the controller socket:
// root always, and the service's own uid only in dev mode (ADR 0011).
func (s *Server) socketAllowed(uid uint32) bool {
	return uid == 0 || (s.cfg.Dev && int64(uid) == int64(getuid()))
}

// socketPrincipal is the actor of a socket peer (ADR 0011). The peer
// credentials are its authentication, so it carries the admin scope.
func (s *Server) socketPrincipal(uid uint32) core.Principal {
	actor := "local:root@" + s.cfg.HostID
	if uid != 0 {
		actor = fmt.Sprintf("local:uid-%d@%s", uid, s.cfg.HostID)
	}
	return core.Principal{Actor: actor, Scope: core.ScopeAdmin, Source: SocketSource}
}

// peerListener accepts only connections from allowed peers.
type peerListener struct {
	net.Listener
	s       *Server
	peerUID PeerUIDFunc
}

type peerConn struct {
	net.Conn
	uid uint32
}

// Accept returns the next connection whose peer is allowed, closing the
// others.
func (l *peerListener) Accept() (net.Conn, error) {
	for {
		c, err := l.Listener.Accept()
		if err != nil {
			return nil, err
		}
		uid, err := l.peerUID(c)
		if err != nil || !l.s.socketAllowed(uid) {
			l.s.log.Warn("api: refused a controller socket connection", "uid", uid, "err", err)
			_ = c.Close()
			continue
		}
		return &peerConn{Conn: c, uid: uid}, nil
	}
}

// SocketListener wraps l so that Accept returns only connections from uid 0
// or, in dev mode, the service's own uid. A nil peerUID means SocketPeerUID.
func (s *Server) SocketListener(l net.Listener, peerUID PeerUIDFunc) net.Listener {
	if peerUID == nil {
		peerUID = SocketPeerUID
	}
	return &peerListener{Listener: l, s: s, peerUID: peerUID}
}

type peerUIDKey struct{}

func withPeerUID(ctx context.Context, uid uint32) context.Context {
	return context.WithValue(ctx, peerUIDKey{}, uid)
}

func peerUIDFrom(ctx context.Context) (uint32, bool) {
	uid, ok := ctx.Value(peerUIDKey{}).(uint32)
	return uid, ok
}

// SocketServer returns the controller socket's server: SocketHandler with
// the listeners' timeouts and a ConnContext that attaches each connection's
// peer uid. Serve it on SocketListener's result.
func (s *Server) SocketServer() *http.Server {
	return &http.Server{
		Handler:           s.SocketHandler(),
		ReadHeaderTimeout: socketReadHeaderTimeout,
		ReadTimeout:       socketReadTimeout,
		WriteTimeout:      socketWriteTimeout,
		IdleTimeout:       socketIdleTimeout,
		ErrorLog:          slog.NewLogLogger(s.log.Handler(), slog.LevelWarn),
		ConnContext: func(ctx context.Context, c net.Conn) context.Context {
			if pc, ok := c.(*peerConn); ok {
				return withPeerUID(ctx, pc.uid)
			}
			return ctx
		},
	}
}

// SocketHandler serves the controller socket's own mux (spec section 14):
// GET /healthz with full detail, GET /api/v1/controller/web-ca, and plan,
// apply and read of break-glass actions. No session, token or CSRF applies;
// the peer uid is the authentication, and a request without an allowed one
// is refused.
func (s *Server) SocketHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+healthzPath, s.writeHealthDetail)
	mux.HandleFunc("GET /api/v1/controller/web-ca", s.handleWebCA)
	for _, op := range []string{opPlan, opApply, opRead} {
		mux.HandleFunc("POST /api/v1/actions/{name}/{version}/"+op, s.socketAction(op))
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { s.unrouted(w, r, mux, false) })
	return securityHeaders(recoverPanics(s.log, s.socketPeer(mux)))
}

// socketPeer puts the socket principal of the request's peer in its context.
func (s *Server) socketPeer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		uid, ok := peerUIDFrom(r.Context())
		if !ok || !s.socketAllowed(uid) {
			writeProblem(w, s.log, errcode.New(errcode.Forbidden, "the controller socket accepts only root or the service's own user"))
			return
		}
		next.ServeHTTP(w, r.WithContext(withCaller(r.Context(), caller{principal: s.socketPrincipal(uid)})))
	})
}

type webCAResponse struct {
	PEM         string `json:"pem"`
	Fingerprint string `json:"fingerprint"`
}

func (s *Server) handleWebCA(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, s.log, http.StatusOK, webCAResponse{PEM: string(s.cfg.WebCA.RootPEM()), Fingerprint: s.cfg.WebCA.Fingerprint()})
}

// socketAction serves op of a break-glass action; the socket serves no other
// action.
func (s *Server) socketAction(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		c, _ := callerFrom(r.Context())
		def, err := s.lookupAction(r.PathValue("name"), r.PathValue("version"), core.SideController, op)
		if err == nil && !def.Spec.BreakGlass {
			err = errcode.New(errcode.Forbidden, "the controller socket serves only break-glass actions")
		}
		if err == nil {
			err = authorize(c.principal, RequiredScope(def.Spec.Class, def.Spec.Side))
		}
		if err != nil {
			writeProblem(w, s.log, err)
			return
		}
		s.runAction(w, r, c.principal, "", def, op)
	}
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestSocket' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/socket.go internal/controller/api/socket_test.go
git commit -m "feat(api): serve break-glass routes on the socket"
```


### Task 26: Audit list

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Manifest`'s action list)
- Create: `internal/controller/coreactions/audit.go`
- Test: `internal/controller/coreactions/audit_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 18 and 23 (`module`, `errUnset`, the test environment, `jsonEqual`, `operator`); `core.Audit.List`, `core.AuditQuery`, `core.AuditEntry` (foundation); `github.com/invopop/jsonschema` v0.14.0 (pinned).
- Produces: Contracts › Task 26 (`DefaultAuditLimit`, `MaxAuditLimit`, `AuditListInput`, `AuditList`, `AuditEntry`, `AuditInput`, the test helper `fillAudit`).

**Items:** 14

**Depends on:** Task 24

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

`core.audit.list` maps `core.AuditEntry` to spec section 12's shape, with `null` for absent host, change, error code and source. The engine redacts every read output by its schema and fails closed on properties no schema names, so a plain `json.RawMessage` input would come back with every value `"[redacted]"`. `AuditInput` therefore declares a recursive any-JSON schema: the inputs were redacted when they were recorded, and the list returns them as stored.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/audit_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// fillAudit records a failed login, a controller-side change and an ingested
// agent event, in that order.
func (e *testEnv) fillAudit() {
	e.t.Helper()
	ctx := context.Background()
	if err := e.audit.Record(ctx, core.AuditEntry{
		Actor: "anonymous", Action: "core.auth.login", Version: 1, Phase: "login", Result: "error",
		ErrorCode: "auth.invalid_credentials", Source: "127.0.0.1", At: t0, Input: json.RawMessage(`{"username":"mallory"}`),
	}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.audit.Append(ctx, nil, core.JournalEntry{
		At: t0, Actor: "user:u1", Action: "core.token.create", Version: 1, Phase: "apply", ChangeID: "c1",
		Input: json.RawMessage(`{"name":"ci","scope":"read","nested":{"list":[1,{"deep":true}]}}`), Result: "ok",
	}); err != nil {
		e.t.Fatal(err)
	}
	if err := e.audit.Ingest(ctx, "h1", []core.JournalEntry{{
		Seq: 1, At: t0, Actor: "user:u1", Action: "demo.item.add", Version: 2, Phase: "confirm", ChangeID: "c2",
		Input: json.RawMessage(`{"password":"[redacted]"}`), Result: "ok",
	}}); err != nil {
		e.t.Fatal(err)
	}
}

func TestAuditList(t *testing.T) {
	e := newTestEnv(t)
	e.fillAudit()
	raw, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"entries":[
		{"id":"00000000000000000000000003","at":"2026-10-17T00:00:00Z","actor":"user:u1","hostId":"h1",
		 "action":"demo.item.add","version":2,"phase":"confirm","changeId":"c2","input":{"password":"[redacted]"},
		 "result":"ok","errorCode":null,"source":null},
		{"id":"00000000000000000000000002","at":"2026-10-17T00:00:00Z","actor":"user:u1","hostId":null,
		 "action":"core.token.create","version":1,"phase":"apply","changeId":"c1",
		 "input":{"name":"ci","scope":"read","nested":{"list":[1,{"deep":true}]}},
		 "result":"ok","errorCode":null,"source":null},
		{"id":"00000000000000000000000001","at":"2026-10-17T00:00:00Z","actor":"anonymous","hostId":null,
		 "action":"core.auth.login","version":1,"phase":"login","changeId":null,"input":{"username":"mallory"},
		 "result":"error","errorCode":"auth.invalid_credentials","source":"127.0.0.1"}
	],"nextBefore":null}`
	if !jsonEqual(t, raw, want) {
		t.Errorf("output = %s\nwant %s", raw, want)
	}
}

func TestAuditListFiltersAndPages(t *testing.T) {
	e := newTestEnv(t)
	e.fillAudit()
	ids := func(l AuditList) []string {
		var out []string
		for _, entry := range l.Entries {
			out = append(out, entry.ID)
		}
		return out
	}
	for _, tt := range []struct {
		name string
		in   AuditListInput
		want []string
		next string
	}{
		{"actor", AuditListInput{Actor: "anonymous"}, []string{"00000000000000000000000001"}, ""},
		{"host", AuditListInput{HostID: "h1"}, []string{"00000000000000000000000003"}, ""},
		{"action", AuditListInput{Action: "core.token.create"}, []string{"00000000000000000000000002"}, ""},
		{"first page", AuditListInput{Limit: 2}, []string{"00000000000000000000000003", "00000000000000000000000002"}, "00000000000000000000000002"},
		{"second page", AuditListInput{Limit: 2, Before: "00000000000000000000000002"}, []string{"00000000000000000000000001"}, ""},
	} {
		var got AuditList
		if err := e.read(operator, "core.audit.list", tt.in, &got); err != nil {
			t.Fatalf("%s: %v", tt.name, err)
		}
		next := ""
		if got.NextBefore != nil {
			next = *got.NextBefore
		}
		if g := ids(got); !slices.Equal(g, tt.want) || next != tt.next {
			t.Errorf("%s: ids %v, next %q; want %v, %q", tt.name, g, next, tt.want, tt.next)
		}
	}

	var empty AuditList
	if err := e.read(operator, "core.audit.list", AuditListInput{Actor: "nobody"}, &empty); err != nil {
		t.Fatal(err)
	}
	if empty.Entries == nil || len(empty.Entries) != 0 {
		t.Errorf("entries = %#v, want an empty list", empty.Entries)
	}
}

func TestAuditListLimits(t *testing.T) {
	e := newTestEnv(t)
	for _, raw := range []string{`{"limit":0}`, `{"limit":501}`, `{"limit":-1}`, `{"since":"x"}`} {
		_, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(raw))
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput {
			t.Errorf("%s: err = %v, want request.invalid_input", raw, err)
		}
	}
	for _, raw := range []string{`{"limit":1}`, `{"limit":500}`} {
		if _, err := e.engine.Read(context.Background(), operator, ref("core.audit.list"), json.RawMessage(raw)); err != nil {
			t.Errorf("%s: %v", raw, err)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestAuditList|TestAuditListFiltersAndPages|TestAuditListLimits)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/audit_test.go:66:16: undefined: AuditList
internal/controller/coreactions/audit_test.go:75:8: undefined: AuditListInput
internal/controller/coreactions/audit_test.go:79:13: undefined: AuditListInput
internal/controller/coreactions/audit_test.go:80:12: undefined: AuditListInput
```

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/module.go`, add the line `m.auditActions(),` after `m.tokenActions(),` in `Manifest`'s list, so the function reads:

```go
// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
		m.tokenActions(),
		m.auditActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}
```

Create `internal/controller/coreactions/audit.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"time"

	"github.com/invopop/jsonschema"

	"github.com/darkraise/darkpanel/internal/core"
)

// The page sizes of core.audit.list/v1 (spec section 10).
const (
	DefaultAuditLimit = 100
	MaxAuditLimit     = 500
)

// AuditListInput is the input of core.audit.list/v1.
type AuditListInput struct {
	Limit  int    `json:"limit,omitempty" jsonschema:"minimum=1,maximum=500" jsonschema_description:"Most entries to return, 1 to 500; 100 when omitted."`
	Before string `json:"before,omitempty" jsonschema_description:"The nextBefore of a previous page: only older entries are returned."`
	Actor  string `json:"actor,omitempty" jsonschema_description:"Only entries of this actor, such as user:<id> or local:root@<host id>."`
	HostID string `json:"hostId,omitempty" jsonschema_description:"Only entries of agent actions on this host."`
	Action string `json:"action,omitempty" jsonschema_description:"Only entries of this action, such as core.token.create."`
}

// AuditList is the output of core.audit.list/v1.
type AuditList struct {
	Entries    []AuditEntry `json:"entries" jsonschema_description:"Matching entries, newest first."`
	NextBefore *string      `json:"nextBefore" jsonschema_description:"Pass as before to read the next page; null on the last page."`
}

// AuditEntry is one row of the audit log (spec section 12).
type AuditEntry struct {
	ID        string     `json:"id" jsonschema_description:"Entry id."`
	At        time.Time  `json:"at" jsonschema_description:"When the event happened."`
	Actor     string     `json:"actor" jsonschema_description:"Who acted, such as user:<id>, token:<id>, local:root@<host id>, anonymous or system:recovery."`
	HostID    *string    `json:"hostId" jsonschema_description:"The host of an agent action; null for controller-side events."`
	Action    string     `json:"action" jsonschema_description:"The action, or core.auth.login and core.auth.logout."`
	Version   int        `json:"version" jsonschema_description:"The action version."`
	Phase     string     `json:"phase" jsonschema_description:"apply, confirm, revert, recover, interrupt, login or logout."`
	ChangeID  *string    `json:"changeId" jsonschema_description:"The change; null for logins and logouts."`
	Input     AuditInput `json:"input" jsonschema_description:"The input, with secret fields redacted when it was recorded."`
	Result    string     `json:"result" jsonschema_description:"ok or error."`
	ErrorCode *string    `json:"errorCode" jsonschema_description:"The error code of a failed event; null when it succeeded."`
	Source    *string    `json:"source" jsonschema_description:"The client address, or socket; null when it was not recorded."`
}

// AuditInput is an audit entry's input as recorded, already redacted. Its
// schema describes every JSON value as not secret, so the engine's output
// redaction returns it unchanged instead of redacting every property no
// schema names.
type AuditInput json.RawMessage

// MarshalJSON returns the input as recorded, or null when there is none.
func (a AuditInput) MarshalJSON() ([]byte, error) {
	if len(a) == 0 {
		return []byte("null"), nil
	}
	return a, nil
}

// UnmarshalJSON keeps a copy of data.
func (a *AuditInput) UnmarshalJSON(data []byte) error {
	*a = append((*a)[:0], data...)
	return nil
}

// JSONSchema describes any JSON value, at any depth.
func (AuditInput) JSONSchema() *jsonschema.Schema {
	self := &jsonschema.Schema{Ref: "#/$defs/AuditInput"}
	return &jsonschema.Schema{AdditionalProperties: self, Items: self}
}

func (m module) auditActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[AuditListInput, AuditList](core.ActionSpec{
			Name: "core.audit.list", Version: 1, Side: core.SideController,
			Summary: "List audit log entries, newest first, filtered by actor, host or action.",
		}, auditList{m}),
	}
}

type auditList struct{ m module }

func (h auditList) Read(ctx context.Context, _ core.Env, in AuditListInput) (AuditList, error) {
	if h.m.d.Audit == nil {
		return AuditList{}, errUnset("Deps.Audit")
	}
	limit := in.Limit
	if limit == 0 {
		limit = DefaultAuditLimit
	}
	entries, next, err := h.m.d.Audit.List(ctx, core.AuditQuery{
		Limit: min(limit, MaxAuditLimit), Before: in.Before, Actor: in.Actor, HostID: in.HostID, Action: in.Action,
	})
	if err != nil {
		return AuditList{}, err
	}
	out := AuditList{Entries: make([]AuditEntry, 0, len(entries))}
	for _, e := range entries {
		out.Entries = append(out.Entries, AuditEntry{
			ID: e.ID, At: e.At, Actor: e.Actor, HostID: nonEmpty(e.HostID), Action: e.Action, Version: e.Version,
			Phase: e.Phase, ChangeID: nonEmpty(e.ChangeID), Input: AuditInput(e.Input), Result: e.Result,
			ErrorCode: nonEmpty(e.ErrorCode), Source: nonEmpty(e.Source),
		})
	}
	out.NextBefore = nonEmpty(next)
	return out, nil
}

// nonEmpty is nil for "", so absent values encode as null.
func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestAuditList|TestAuditListFiltersAndPages|TestAuditListLimits)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/audit.go \
  internal/controller/coreactions/audit_test.go
git commit -m "feat(coreactions): list the audit log"
```


### Task 27: Route coverage and the deny-by-default matrix

**Files:**
- Create: `internal/controller/api/coverage.go`
- Test: `internal/controller/api/coverage_test.go`
- Test: `internal/controller/api/deny_test.go`

**Interfaces:**
- Consumes: Contracts › Task 19 (`routeAccess`, the harness, `allMethods`), Task 21 (`stubDispatcher`, `call`, `perAction`, `opRead`) and Task 22 (`networkRoutes`, `Routes`); `schema.OpenAPI` (plan 2); `registry.Build` (foundation).
- Produces: Contracts › Task 27 (`CheckOpenAPI`).

**Items:** 3, 12

**Depends on:** Tasks 19, 21 and 22

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

`CheckOpenAPI` states the route-coverage invariant of spec section 7 as code, so plan 4's assembly test can run it with the real registry. The deny-by-default matrix is register row 12's requirement against the real mux: every method on every non-public route and on unrouted paths refuses without valid credentials, and nothing is dispatched. Its public list is written out in the test instead of taken from `routeAccess`, so a mistake there would show up here.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/api/coverage_test.go`:

```go
package api

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

func TestCheckOpenAPI(t *testing.T) {
	demo, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	built, err := registry.Build()
	if err != nil {
		t.Fatal(err)
	}
	for name, reg := range map[string]*core.Registry{"demo": demo, "built": built} {
		if err := CheckOpenAPI(reg); err != nil {
			t.Errorf("%s registry: %v", name, err)
		}
	}
}

func TestCheckOpenAPIFindsGaps(t *testing.T) {
	reg, err := core.NewRegistry(demoModule{})
	if err != nil {
		t.Fatal(err)
	}
	routes := (&Server{}).networkRoutes()
	doc, err := schema.OpenAPI(reg, Routes())
	if err != nil {
		t.Fatal(err)
	}

	missing := strings.Replace(string(doc), `"/api/v1/changes/{id}/revert"`, `"/api/v1/changes/{id}/undo"`, 1)
	err = checkOpenAPI(reg, routes, []byte(missing))
	for _, want := range []string{
		"route POST /api/v1/changes/{id}/revert: the document lacks",
		"document operation POST /api/v1/changes/{id}/undo: no network route serves it",
	} {
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("renamed route: error %v, want %q", err, want)
		}
	}

	noAction := strings.Replace(string(doc), `"/api/v1/hosts/{host}/actions/demo.item.add/v1/plan"`, `"/api/v1/hosts/{host}/actions/demo.item.add/v1/draft"`, 1)
	err = checkOpenAPI(reg, routes, []byte(noAction))
	if err == nil || !strings.Contains(err.Error(), "the document lacks POST /api/v1/hosts/{host}/actions/demo.item.add/v1/plan") {
		t.Errorf("missing action operation: %v", err)
	}

	breakGlass := strings.Replace(string(doc), `"/api/v1/actions/demo.item.remove/v1/plan"`, `"/api/v1/actions/demo.secret.reset/v1/plan"`, 1)
	err = checkOpenAPI(reg, routes, []byte(breakGlass))
	if err == nil || !strings.Contains(err.Error(), "break-glass action demo.secret.reset is listed") {
		t.Errorf("listed break-glass action: %v", err)
	}

	if err := checkOpenAPI(reg, routes, []byte("{")); err == nil {
		t.Error("checkOpenAPI accepted a malformed document")
	}
}
```

Create `internal/controller/api/deny_test.go`:

```go
package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// concrete fills a route pattern's wildcards with values the demo registry
// knows.
var concrete = strings.NewReplacer("{host}", "h1", "{name}", "demo.item.add", "{version}", "v1",
	"{module}", "demo", "{resource}", "status", "{id}", "c7")

// unknownPaths match no route.
var unknownPaths = []string{"/api", "/api/", "/api/v1/nope", "/api/v2/changes", "/api/v1/hosts/h1/nope",
	"/api/v1/changes/c7/explode", "/api/v1/hosts/h1/actions/demo.item.add/v1/draft"}

// specPublic is the public list of spec section 12, written out rather than
// taken from routeAccess so that a mistake there fails this test.
func specPublic(method, path string) bool {
	get := method == "GET" || method == "HEAD"
	switch path {
	case "/api/v1/auth/login":
		return method == "POST"
	case "/healthz", "/", "/hosts/h1/hostinfo", "/assets/app.js":
		return get
	}
	return false
}

// TestDenyByDefault is spec section 12's requirement against the real mux:
// every method on every route that is not public, and on every path that
// matches no route, refuses a request without valid credentials, and nothing
// is dispatched.
func TestDenyByDefault(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	session, _ := h.login()

	var targets []string
	for _, rt := range h.srv.networkRoutes() {
		targets = append(targets, concrete.Replace(rt.path))
	}
	targets = append(targets, unknownPaths...)
	targets = append(targets, "/", "/hosts/h1/hostinfo", "/assets/app.js")
	for _, path := range targets {
		for _, m := range allMethods {
			if specPublic(m, path) {
				continue
			}
			for name, r := range map[string]func() *http.Request{
				"no credentials": func() *http.Request { return request(m, path, `{"input":{}}`) },
				"invalid bearer": func() *http.Request { return withToken(request(m, path, `{"input":{}}`), "dpt_01J_bogus") },
				"invalid cookie": func() *http.Request { return withSession(request(m, path, `{"input":{}}`), "bogus", "") },
			} {
				last = call{}
				rec := h.serve(r())
				decodeProblem(t, rec, 401, errcode.AuthRequired)
				if last.method != "" {
					t.Errorf("%s %s with %s: dispatched %s", m, path, name, last.method)
				}
			}
			if m != "GET" && m != "HEAD" && m != "OPTIONS" {
				last = call{}
				decodeProblem(t, h.serve(withSession(request(m, path, `{"input":{}}`), session, "")), 403, errcode.CSRF)
				if last.method != "" {
					t.Errorf("%s %s with a session and no CSRF token: dispatched %s", m, path, last.method)
				}
			}
		}
	}
}

// TestUnroutedAfterAuthentication shows that not_found and
// method_not_allowed come only after authentication.
func TestUnroutedAfterAuthentication(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeRead)
	for _, path := range unknownPaths {
		for _, m := range allMethods {
			decodeProblem(t, h.serve(withToken(request(m, path, ""), token)), 404, errcode.NotFound)
		}
	}
	for path, allow := range map[string]string{
		"/api/v1/changes":                                "GET, HEAD",
		"/api/v1/changes/c7/confirm":                     "POST",
		"/api/v1/hosts/h1/actions/demo.item.add/v1/plan": "POST",
		"/api/v1/auth/login":                             "POST",
		"/healthz":                                       "GET, HEAD",
		"/hosts/h1":                                      "GET, HEAD",
	} {
		rec := h.serve(withToken(request("PATCH", path, ""), token))
		decodeProblem(t, rec, 405, errcode.MethodNotAllowed)
		if got := rec.Header().Get("Allow"); got != allow {
			t.Errorf("PATCH %s: Allow %q, want %q", path, got, allow)
		}
	}
	if last.method != "" {
		t.Errorf("dispatched %s", last.method)
	}
}

// TestBreakGlassRefusedOnTheNetwork checks every break-glass action of the
// registry with an admin token.
func TestBreakGlassRefusedOnTheNetwork(t *testing.T) {
	h := newHarness(t)
	var last call
	h.stubDispatcher(&last)
	token := h.token(core.ScopeAdmin)
	n := 0
	for _, def := range h.srv.cfg.Registry.Actions() {
		if !def.Spec.BreakGlass {
			continue
		}
		ops := []string{opPlan, opApply}
		if def.Spec.Class == core.ClassRead {
			ops = []string{opRead}
		}
		for _, op := range ops {
			n++
			path := "/api/v1/actions/" + def.Spec.Name + "/v1/" + op
			decodeProblem(t, h.serve(withToken(request("POST", path, `{"input":{},"planId":"p"}`), token)), 403, errcode.Forbidden)
		}
	}
	if n != 3 || last.method != "" {
		t.Errorf("checked %d break-glass operations, dispatched %q; want 3 and none", n, last.method)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestCheckOpenAPI|TestDenyByDefault|TestUnroutedAfterAuthentication|TestBreakGlassRefusedOnTheNetwork' ./internal/controller/api/`
Expected: FAIL — the build reports `undefined: CheckOpenAPI` and `undefined: checkOpenAPI`.

- [ ] **Step 3: Write the implementation**

Create `internal/controller/api/coverage.go`:

```go
package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var pathParam = regexp.MustCompile(`\{[A-Za-z]+\}`)

// CheckOpenAPI verifies the route-coverage invariant of spec section 7 for
// reg: every route of the network mux appears in the OpenAPI document (a
// hand-written route as itself, a per-action route once for each
// non-break-glass action or resource of reg it serves), and every operation
// of the document is served by a network route and names no break-glass
// action. Tests call it; plan 4's assembly test calls it with the real
// registry.
func CheckOpenAPI(reg *core.Registry) error {
	doc, err := schema.OpenAPI(reg, Routes())
	if err != nil {
		return err
	}
	return checkOpenAPI(reg, (&Server{}).networkRoutes(), doc)
}

func checkOpenAPI(reg *core.Registry, routes []route, doc []byte) error {
	var d struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(doc, &d); err != nil {
		return fmt.Errorf("api: openapi: %w", err)
	}
	var errs []error
	mux := http.NewServeMux()
	for _, rt := range routes {
		mux.Handle(rt.method+" "+rt.path, rt.handler)
		paths := []string{rt.path}
		if rt.perAction {
			paths = instances(reg, rt)
		}
		for _, p := range paths {
			if _, ok := d.Paths[p][strings.ToLower(rt.method)]; !ok {
				errs = append(errs, fmt.Errorf("route %s %s: the document lacks %s %s", rt.method, rt.path, rt.method, p))
			}
		}
	}
	for p, ops := range d.Paths {
		for _, def := range reg.Actions() {
			if def.Spec.BreakGlass && strings.Contains(p, "/"+def.Spec.Name+"/") {
				errs = append(errs, fmt.Errorf("document path %s: break-glass action %s is listed", p, def.Spec.Name))
			}
		}
		for m := range ops {
			r, err := http.NewRequest(strings.ToUpper(m), "http://controller"+pathParam.ReplaceAllString(p, "x"), nil)
			if err != nil {
				errs = append(errs, fmt.Errorf("document operation %s %s: %w", strings.ToUpper(m), p, err))
				continue
			}
			if _, pattern := mux.Handler(r); pattern == "" {
				errs = append(errs, fmt.Errorf("document operation %s %s: no network route serves it", strings.ToUpper(m), p))
			}
		}
	}
	return errors.Join(errs...)
}

// instances lists the document paths of a per-action route: one per
// resource for the resource route, and for an action route one per
// non-break-glass action of its side whose class has its operation.
func instances(reg *core.Registry, rt route) []string {
	if strings.Contains(rt.path, "/resources/") {
		var out []string
		for _, res := range reg.Resources() {
			module, name, _ := strings.Cut(res.Name, ".")
			out = append(out, strings.NewReplacer("{module}", module, "{resource}", name).Replace(rt.path))
		}
		return out
	}
	side := core.SideController
	if strings.Contains(rt.path, "{host}") {
		side = core.SideAgent
	}
	op := rt.path[strings.LastIndex(rt.path, "/")+1:]
	var out []string
	for _, def := range reg.Actions() {
		s := def.Spec
		if s.BreakGlass || s.Side != side || (op == opRead) != (s.Class == core.ClassRead) {
			continue
		}
		out = append(out, strings.NewReplacer("{name}", s.Name, "{version}", fmt.Sprintf("v%d", s.Version)).Replace(rt.path))
	}
	return out
}
```

- [ ] **Step 4: Run the tests and checks**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run 'TestCheckOpenAPI|TestDenyByDefault|TestUnroutedAfterAuthentication|TestBreakGlassRefusedOnTheNetwork' ./internal/controller/api/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/api`).

Then run:

```bash
GOTOOLCHAIN=local timeout 600 go test -count=1 ./...
GOTOOLCHAIN=local timeout 600 go test -race -count=1 ./internal/controller/api/
GOTOOLCHAIN=local timeout 300 go vet ./...
GOTOOLCHAIN=local timeout 600 golangci-lint run --allow-serial-runners ./...
GOTOOLCHAIN=local timeout 300 go mod tidy -diff
```

Expected: every `go test` line starts with `ok` (or `?` for packages without tests); `go vet` prints nothing; golangci-lint prints `0 issues.`; `go mod tidy -diff` prints nothing and exits 0.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/coverage.go internal/controller/api/coverage_test.go internal/controller/api/deny_test.go
git commit -m "feat(api): check OpenAPI route coverage"
```


### Task 28: User list, TOTP reset, password reset and delete

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Manifest`'s action list)
- Create: `internal/controller/coreactions/users.go`
- Test: `internal/controller/coreactions/users_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 18, 23 and 24 (`module`, `NoInput`, `Empty`, `stateHash`, `authService`, the test environment, `plan`, `apply`, `run`, `verify`, `root`, `noSecret`, `testPassword`, `activeUser`); `auth.Service.ListUsers`, `GetUser`, `ResetTOTP`, `ResetPassword`, `DeleteUser`, `CreateUser`, `ActivateUser`, `CreateToken`, `ListTokens`, `Login`, `auth.LoginRequest`, `auth.RoleAdmin`, `auth.StatePending`, `auth.StateActive` (Tasks 4, 6 and 7).
- Produces: Contracts › Task 28 (`UserList`, `UsernameInput`, `UserPasswordInput`, `Enrollment`, the four actions, `userState`, `wantUserState`, the test helpers `newPassword`, `login`, `change`, `auditEntries`).

**Items:** 11, 52

**Depends on:** Task 26

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

The four break-glass actions that compensate nothing come first, because the registry refuses a compensable action whose compensating action is not registered yet (Task 29 names `core.user.delete` and `core.user.reset-totp`). The three irreversible actions use the username as their confirmation phrase. `core.user.delete` shows the last-active-admin refusal as the guard `not-last-admin`, so the operator sees it before apply, and its state hash includes that fact, so an admin deleted in between turns a passing plan into `plan.state_changed`. Passwords and TOTP secrets never reach a diff, guard or error: the tests scan for them.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/users_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// newPassword is the password reset tests set; tests scan for it.
const newPassword = "an entirely new passphrase"

// login logs username in through the identity service with a code from
// totpSecret.
func (e *testEnv) login(username, password, totpSecret string) error {
	e.t.Helper()
	code, err := auth.TOTPCode(totpSecret, e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	_, err = e.auth.Login(context.Background(), auth.LoginRequest{Username: username, Password: password, Code: code, Source: "127.0.0.1"})
	return err
}

// change returns a change as stored, with its output redacted.
func (e *testEnv) change(id string) core.ChangeView {
	e.t.Helper()
	v, err := e.engine.Change(context.Background(), id)
	if err != nil {
		e.t.Fatal(err)
	}
	return v
}

// auditEntries returns every audit entry, newest first.
func (e *testEnv) auditEntries() []core.AuditEntry {
	e.t.Helper()
	entries, _, err := e.audit.List(context.Background(), core.AuditQuery{Limit: 500})
	if err != nil {
		e.t.Fatal(err)
	}
	return entries
}

func TestUserList(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("bob")
	if _, _, err := e.auth.CreateUser(context.Background(), "alice", testPassword); err != nil {
		t.Fatal(err)
	}
	var got UserList
	if err := e.read(root, "core.user.list", NoInput{}, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Users) != 2 || got.Users[0].Username != "alice" || got.Users[0].State != auth.StatePending ||
		got.Users[1].Username != "bob" || got.Users[1].State != auth.StateActive {
		t.Errorf("users = %+v, want pending alice then active bob", got.Users)
	}
}

func TestUserResetTOTP(t *testing.T) {
	e := newTestEnv(t)
	u, _ := e.activeUser("alice")
	pv, err := e.plan(root, "core.user.reset-totp", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: active, with its enrolled authenticator"},
		{Op: "+", Text: "user alice: pending, with a new TOTP secret shown once; its sessions end and its API tokens stay"},
		{Op: " ", Text: "core.user.activate with a code from the new secret makes the user active again"},
		{Op: " ", Text: "alice is the last active admin: until it is activated again, no one can log in"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || len(pv.Guards) != 0 || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanConfirmationNeeded)
	v, err := e.apply(root, pv.PlanID, "alice")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out Enrollment
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.ID != u.ID || out.User.State != auth.StatePending || len(out.TOTPSecret) != 32 {
		t.Errorf("output = %+v, want alice pending with a 32-character secret", out)
	}
	stored := e.change(v.ID)
	if !strings.Contains(string(stored.Output), `"totpSecret":"[redacted]"`) {
		t.Errorf("stored output = %s, want the secret redacted", stored.Output)
	}
	noSecret(t, []string{out.TOTPSecret}, pv, stored, e.auditEntries())
	if err := e.verify("core.user.reset-totp", UsernameInput{Username: "alice"}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if code, err := auth.TOTPCode(out.TOTPSecret, e.clock.Now()); err != nil {
		t.Fatal(err)
	} else if _, err := e.auth.ActivateUser(context.Background(), "alice", code); err != nil {
		t.Errorf("activating with the new secret: %v", err)
	}
}

func TestUserResetPassword(t *testing.T) {
	e := newTestEnv(t)
	ctx := context.Background()
	_, totp, err := e.auth.CreateUser(ctx, "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	code, err := auth.TOTPCode(totp, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := e.auth.ActivateUser(ctx, "alice", code); err != nil {
		t.Fatal(err)
	}
	e.clock.Advance(auth.TOTPPeriod)

	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.reset-password", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "-", Text: "user alice: current password"},
		{Op: "+", Text: "user alice: new password; its sessions end and its API tokens stay"},
	}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || pv.Confirmation != "alice" {
		t.Errorf("plan = %+v, want diff %+v and phrase alice", pv, wantDiff)
	}
	v, err := e.apply(root, pv.PlanID, "alice")
	if err != nil || v.State != core.StateCompleted {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	noSecret(t, []string{newPassword, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.user.reset-password", in); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := e.login("alice", newPassword, totp); err != nil {
		t.Errorf("login with the new password: %v", err)
	}
}

func TestUserPasswordRefusalsKeepThePasswordSecret(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	for _, password := range []string{"short-pw-11", strings.Repeat("p", 129)} {
		_, err := e.plan(root, "core.user.reset-password", UserPasswordInput{Username: "alice", Password: password})
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || len(ce.Fields) != 1 || ce.Fields[0].Path != "/password" {
			t.Errorf("password of %d characters: err = %+v, want request.invalid_input at /password", len(password), err)
		}
		noSecret(t, []string{password}, err)
	}
	_, err := e.plan(root, "core.user.reset-password", UserPasswordInput{Username: "nobody", Password: newPassword})
	wantCode(t, err, errcode.NotFound)
	noSecret(t, []string{newPassword}, err)
}

func TestUserDelete(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	bob, _ := e.activeUser("bob")
	if _, _, err := e.auth.CreateToken(context.Background(), bob.ID, "ci", core.ScopeRead, 0); err != nil {
		t.Fatal(err)
	}

	pv, err := e.plan(root, "core.user.delete", UsernameInput{Username: "bob"})
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{{Op: "-", Text: "user bob (active, role admin), with its sessions and API tokens"}}
	wantGuards := []core.Guard{{ID: "not-last-admin", OK: true, Message: "bob is not the last active admin"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) || pv.Confirmation != "bob" {
		t.Errorf("plan = %+v, want diff %+v, guards %+v, phrase bob", pv, wantDiff, wantGuards)
	}
	var out Empty
	v := e.run(root, "core.user.delete", UsernameInput{Username: "bob"}, &out)
	if string(v.Output) != "{}" {
		t.Errorf("output = %s, want {}", v.Output)
	}
	if _, err := e.auth.GetUser(context.Background(), "bob"); err == nil {
		t.Error("bob still exists")
	}
	if tokens, err := e.auth.ListTokens(context.Background(), bob.ID); err != nil || len(tokens) != 0 {
		t.Errorf("bob's tokens = %+v, %v; want none", tokens, err)
	}
	if err := e.verify("core.user.delete", UsernameInput{Username: "bob"}); err != nil {
		t.Errorf("verify: %v", err)
	}
	if err := e.verify("core.user.delete", UsernameInput{Username: "alice"}); err == nil {
		t.Error("verify passed for a user that exists")
	}

	pv, err = e.plan(root, "core.user.delete", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "not-last-admin", Message: "alice is the last active admin; create and activate another admin first"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "alice")
	wantCode(t, err, errcode.PlanGuardFailed)
}

func TestUserDeleteStateChanged(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	e.activeUser("bob")
	pv, err := e.plan(root, "core.user.delete", UsernameInput{Username: "alice"})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.auth.DeleteUser(context.Background(), "bob"); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(root, pv.PlanID, "alice")
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestUserActionsRefuseUnknownUsers(t *testing.T) {
	e := newTestEnv(t)
	for _, action := range []string{"core.user.reset-totp", "core.user.delete"} {
		_, err := e.plan(root, action, UsernameInput{Username: "nobody"})
		wantCode(t, err, errcode.NotFound)
	}
	_, err := e.plan(root, "core.user.delete", UsernameInput{Username: "Not Valid"})
	wantCode(t, err, errcode.InvalidInput)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserList|TestUserResetTOTP|TestUserResetPassword|TestUserPasswordRefusalsKeepThePasswordSecret|TestUserDelete|TestUserDeleteStateChanged|TestUserActionsRefuseUnknownUsers)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/users_test.go:57:10: undefined: UserList
internal/controller/coreactions/users_test.go:70:50: undefined: UsernameInput
internal/controller/coreactions/users_test.go:89:10: undefined: Enrollment
internal/controller/coreactions/users_test.go:101:45: undefined: UsernameInput
```

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/module.go`, add the line `m.userActions(),` after `m.auditActions(),` in `Manifest`'s list, so the function reads:

```go
// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
		m.tokenActions(),
		m.auditActions(),
		m.userActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}
```

Create `internal/controller/coreactions/users.go`:

```go
package coreactions

import (
	"context"
	"errors"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// UserList is the output of core.user.list/v1.
type UserList struct {
	Users []auth.User `json:"users" jsonschema_description:"Every user, sorted by username."`
}

// UsernameInput is the input of core.user.reset-totp/v1 and core.user.delete/v1.
type UsernameInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name."`
}

// UserPasswordInput is the input of core.user.create/v1 and
// core.user.reset-password/v1.
type UserPasswordInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The user's name: lower-case letters, digits, dot, underscore and hyphen, starting with a letter or digit, at most 64 characters."`
	Password string `json:"password" secret:"true" jsonschema:"minLength=12,maxLength=128" jsonschema_description:"The password, 12 to 128 characters."`
}

// Enrollment is the output of core.user.create/v1 and core.user.reset-totp/v1:
// the user and its new TOTP secret, which only the apply that made it returns.
type Enrollment struct {
	User       auth.User `json:"user" jsonschema_description:"The user, pending until core.user.activate confirms a code."`
	TOTPSecret string    `json:"totpSecret" secret:"true" jsonschema_description:"The TOTP secret in unpadded base32, for an authenticator app; shown only once."`
}

func (m module) userActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineRead[NoInput, UserList](core.ActionSpec{
			Name: "core.user.list", Version: 1, Side: core.SideController, BreakGlass: true,
			Summary: "List the users; served only on the controller socket.",
		}, userList{m}),
		core.DefineMutating[UsernameInput, Enrollment](core.ActionSpec{
			Name: "core.user.reset-totp", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Give a user a new TOTP secret and return it to pending, ending its sessions; the confirmation phrase is the username.",
		}, userResetTOTP{m}),
		core.DefineMutating[UserPasswordInput, auth.User](core.ActionSpec{
			Name: "core.user.reset-password", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Set a user's password and end its sessions; the confirmation phrase is the username.",
		}, userResetPassword{m}),
		core.DefineMutating[UsernameInput, Empty](core.ActionSpec{
			Name: "core.user.delete", Version: 1, Class: core.ClassIrreversible, Side: core.SideController, BreakGlass: true,
			Summary: "Delete a user with its sessions and API tokens, refusing the last active admin; the confirmation phrase is the username.",
		}, userDelete{m}),
	}
}

// userState returns username and whether it is the last active admin, the
// one user core.user.delete refuses.
func (m module) userState(ctx context.Context, username string) (*auth.Service, auth.User, bool, error) {
	svc, err := m.authService()
	if err != nil {
		return nil, auth.User{}, false, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return nil, auth.User{}, false, err
	}
	var u auth.User
	admins := 0
	for _, each := range users {
		if each.Username == username {
			u = each
		}
		if each.State == auth.StateActive && each.Role == auth.RoleAdmin {
			admins++
		}
	}
	if u.ID == "" {
		return nil, auth.User{}, false, errcode.New(errcode.NotFound, fmt.Sprintf("no user %s", username))
	}
	last := u.State == auth.StateActive && u.Role == auth.RoleAdmin && admins == 1
	return svc, u, last, nil
}

type userList struct{ m module }

func (h userList) Read(ctx context.Context, _ core.Env, _ NoInput) (UserList, error) {
	svc, err := h.m.authService()
	if err != nil {
		return UserList{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return UserList{}, err
	}
	return UserList{Users: users}, nil
}

type userResetTOTP struct{ m module }

func (h userResetTOTP) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	_, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	diff := []core.DiffLine{
		{Op: "-", Text: fmt.Sprintf("user %s: %s, with its enrolled authenticator", u.Username, u.State)},
		{Op: "+", Text: fmt.Sprintf("user %s: pending, with a new TOTP secret shown once; its sessions end and its API tokens stay", u.Username)},
		{Op: " ", Text: "core.user.activate with a code from the new secret makes the user active again"},
	}
	if last {
		diff = append(diff, core.DiffLine{Op: " ", Text: u.Username + " is the last active admin: until it is activated again, no one can log in"})
	}
	return core.PlanOutcome{
		Diff: diff, Guards: []core.Guard{},
		StateHash:    stateHash(u.ID, u.State, strconv.FormatBool(last)),
		Confirmation: u.Username,
	}, nil
}

func (h userResetTOTP) Apply(ctx context.Context, _ core.Env, in UsernameInput) (Enrollment, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Enrollment{}, err
	}
	u, secret, err := svc.ResetTOTP(ctx, in.Username)
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{User: u, TOTPSecret: secret}, nil
}

func (h userResetTOTP) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ Enrollment) error {
	return wantUserState(ctx, h.m, in.Username, auth.StatePending)
}

// wantUserState fails unless username exists and, when state is not empty,
// is in that state.
func wantUserState(ctx context.Context, m module, username, state string) error {
	svc, err := m.authService()
	if err != nil {
		return err
	}
	u, err := svc.GetUser(ctx, username)
	if err != nil {
		return err
	}
	if state != "" && u.State != state {
		return fmt.Errorf("coreactions: user %s is %s, want %s", username, u.State, state)
	}
	return nil
}

type userResetPassword struct{ m module }

func (h userResetPassword) Plan(ctx context.Context, _ core.Env, in UserPasswordInput) (core.PlanOutcome, error) {
	_, u, _, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: current password", u.Username)},
			{Op: "+", Text: fmt.Sprintf("user %s: new password; its sessions end and its API tokens stay", u.Username)},
		},
		Guards:       []core.Guard{},
		StateHash:    stateHash(u.ID),
		Confirmation: u.Username,
	}, nil
}

func (h userResetPassword) Apply(ctx context.Context, _ core.Env, in UserPasswordInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.ResetPassword(ctx, in.Username, in.Password)
}

// Verify can check only that the user still exists: the service compares a
// password only during a login.
func (h userResetPassword) Verify(ctx context.Context, _ core.Env, in UserPasswordInput, _ auth.User) error {
	return wantUserState(ctx, h.m, in.Username, "")
}

type userDelete struct{ m module }

func (h userDelete) Plan(ctx context.Context, _ core.Env, in UsernameInput) (core.PlanOutcome, error) {
	_, u, last, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	guard := core.Guard{ID: "not-last-admin", OK: !last, Message: u.Username + " is not the last active admin"}
	if last {
		guard.Message = u.Username + " is the last active admin; create and activate another admin first"
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s (%s, role %s), with its sessions and API tokens", u.Username, u.State, u.Role)},
		},
		Guards:       []core.Guard{guard},
		StateHash:    stateHash(u.ID, u.State, strconv.FormatBool(last)),
		Confirmation: u.Username,
	}, nil
}

func (h userDelete) Apply(ctx context.Context, _ core.Env, in UsernameInput) (Empty, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Empty{}, err
	}
	return Empty{}, svc.DeleteUser(ctx, in.Username)
}

func (h userDelete) Verify(ctx context.Context, _ core.Env, in UsernameInput, _ Empty) error {
	err := wantUserState(ctx, h.m, in.Username, "")
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded) && coded.Code == errcode.NotFound:
		return nil
	case err != nil:
		return err
	}
	return fmt.Errorf("coreactions: user %s still exists", in.Username)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserList|TestUserResetTOTP|TestUserResetPassword|TestUserPasswordRefusalsKeepThePasswordSecret|TestUserDelete|TestUserDeleteStateChanged|TestUserActionsRefuseUnknownUsers)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/users.go \
  internal/controller/coreactions/users_test.go
git commit -m "feat(coreactions): list, reset and delete users"
```


### Task 29: User create and activate

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Manifest`'s action list)
- Create: `internal/controller/coreactions/enroll.go`
- Test: `internal/controller/coreactions/enroll_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 18, 23, 24, 26 and 28 (`module`, `stateHash`, `authService`, `userState`, `wantUserState`, `UserPasswordInput`, `Enrollment`, the test environment and helpers of Tasks 23, 24 and 28); `auth.Service.CreateUser`, `ActivateUser`, `ListUsers`, `auth.TOTPCode`, `auth.TOTPPeriod` (Tasks 2 and 4).
- Produces: Contracts › Task 29 (`UserActivateInput`, the two actions, the test helper `wrongCode`).

**Items:** 8, 11, 52

**Depends on:** Task 28

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

`core.user.create` (compensated by `core.user.delete`) returns the TOTP secret once; `core.user.activate` (compensated by `core.user.reset-totp`) takes the code as a `secret:"true"` field, so the journal and the audit log store `"[redacted]"` and the CLI prompts for it. A taken username and a user that is not pending are guards. `Plan` cannot check the code, because checking spends its step; a wrong code fails the apply with `request.invalid_input`, and the tests scan the failed change and the audit rows for it.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/enroll_test.go`:

```go
package coreactions

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// wrongCode returns a six-digit code that matches none of the steps
// ActivateUser accepts for totpSecret now.
func (e *testEnv) wrongCode(totpSecret string) string {
	e.t.Helper()
	var valid []string
	for _, step := range []int{-1, 0, 1} {
		c, err := auth.TOTPCode(totpSecret, e.clock.Now().Add(auth.TOTPPeriod*time.Duration(step)))
		if err != nil {
			e.t.Fatal(err)
		}
		valid = append(valid, c)
	}
	for _, c := range []string{"918273", "827364", "736455", "645546"} {
		if !slices.Contains(valid, c) {
			return c
		}
	}
	e.t.Fatal("no wrong code found")
	return ""
}

func TestUserCreateAndActivate(t *testing.T) {
	e := newTestEnv(t)
	in := UserPasswordInput{Username: "alice", Password: testPassword}
	pv, err := e.plan(root, "core.user.create", in)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff := []core.DiffLine{
		{Op: "+", Text: "user alice: pending admin, with a new TOTP secret shown once"},
		{Op: " ", Text: "core.user.activate with a code from the secret makes the user active"},
		{Op: " ", Text: "compensated by core.user.delete"},
	}
	wantGuards := []core.Guard{{ID: "username-free", OK: true, Message: "no user is named alice"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) || pv.Confirmation != "" {
		t.Errorf("plan = %+v, want diff %+v and guards %+v", pv, wantDiff, wantGuards)
	}
	v, err := e.apply(root, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.user.delete" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var out Enrollment
	if err := json.Unmarshal(v.Output, &out); err != nil {
		t.Fatal(err)
	}
	if out.User.Username != "alice" || out.User.State != auth.StatePending || out.User.Role != auth.RoleAdmin || len(out.TOTPSecret) != 32 {
		t.Errorf("output = %+v, want pending admin alice and a 32-character secret", out)
	}
	stored := e.change(v.ID)
	if !strings.Contains(string(stored.Output), `"totpSecret":"[redacted]"`) {
		t.Errorf("stored output = %s, want the secret redacted", stored.Output)
	}
	noSecret(t, []string{testPassword, out.TOTPSecret}, pv, stored, e.auditEntries())
	if err := e.verify("core.user.create", in); err != nil {
		t.Errorf("verify create: %v", err)
	}

	code, err := auth.TOTPCode(out.TOTPSecret, e.clock.Now())
	if err != nil {
		t.Fatal(err)
	}
	act := UserActivateInput{Username: "alice", Code: code}
	pv, err = e.plan(root, "core.user.activate", act)
	if err != nil {
		t.Fatal(err)
	}
	wantDiff = []core.DiffLine{
		{Op: "-", Text: "user alice: pending"},
		{Op: "+", Text: "user alice: active, if the code matches its authenticator"},
		{Op: " ", Text: "compensated by core.user.reset-totp"},
	}
	wantGuards = []core.Guard{{ID: "user-pending", OK: true, Message: "alice is pending"}}
	if !reflect.DeepEqual(pv.Diff, wantDiff) || !reflect.DeepEqual(pv.Guards, wantGuards) {
		t.Errorf("plan = %+v, want diff %+v and guards %+v", pv, wantDiff, wantGuards)
	}
	v, err = e.apply(root, pv.PlanID, "")
	if err != nil || v.State != core.StateCompleted || v.CompensatedBy != "core.user.reset-totp" {
		t.Fatalf("apply = %+v, %v", v, err)
	}
	var user auth.User
	if err := json.Unmarshal(v.Output, &user); err != nil || user.State != auth.StateActive {
		t.Errorf("output = %s, want alice active", v.Output)
	}
	noSecret(t, []string{code, out.TOTPSecret, testPassword}, pv, e.change(v.ID), e.auditEntries())
	if err := e.verify("core.user.activate", act); err != nil {
		t.Errorf("verify activate: %v", err)
	}

	e.clock.Advance(auth.TOTPPeriod)
	if err := e.login("alice", testPassword, out.TOTPSecret); err != nil {
		t.Errorf("login after activation: %v", err)
	}
}

func TestUserCreateTakenUsername(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	in := UserPasswordInput{Username: "alice", Password: newPassword}
	pv, err := e.plan(root, "core.user.create", in)
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "username-free", Message: "a user named alice already exists"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanGuardFailed)
	noSecret(t, []string{newPassword}, pv, err)
}

func TestUserCreateStateChanged(t *testing.T) {
	e := newTestEnv(t)
	pv, err := e.plan(root, "core.user.create", UserPasswordInput{Username: "alice", Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := e.auth.CreateUser(context.Background(), "alice", newPassword); err != nil {
		t.Fatal(err)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanStateChanged)
}

func TestUserActivateWrongCode(t *testing.T) {
	e := newTestEnv(t)
	_, totp, err := e.auth.CreateUser(context.Background(), "alice", testPassword)
	if err != nil {
		t.Fatal(err)
	}
	wrong := e.wrongCode(totp)
	pv, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: wrong})
	if err != nil {
		t.Fatal(err)
	}
	v, err := e.apply(root, pv.PlanID, "")
	if err != nil {
		t.Fatal(err)
	}
	if v.State != core.StateFailed || v.Error == nil || v.Error.Code != string(errcode.InvalidInput) {
		t.Errorf("change = %+v, want failed with request.invalid_input", v)
	}
	noSecret(t, []string{wrong, totp}, pv, v, e.auditEntries())
	if u, err := e.auth.GetUser(context.Background(), "alice"); err != nil || u.State != auth.StatePending {
		t.Errorf("alice = %+v, %v; want still pending", u, err)
	}

	for _, code := range []string{"12345", "1234567", "12a456"} {
		_, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: code})
		var ce *errcode.Error
		if !errors.As(err, &ce) || ce.Code != errcode.InvalidInput || len(ce.Fields) != 1 || ce.Fields[0].Path != "/code" {
			t.Errorf("code %q: err = %+v, want request.invalid_input at /code", code, err)
		}
		noSecret(t, []string{code}, err)
	}
}

func TestUserActivateNotPending(t *testing.T) {
	e := newTestEnv(t)
	e.activeUser("alice")
	pv, err := e.plan(root, "core.user.activate", UserActivateInput{Username: "alice", Code: "918273"})
	if err != nil {
		t.Fatal(err)
	}
	want := []core.Guard{{ID: "user-pending", Message: "alice is already active"}}
	if !reflect.DeepEqual(pv.Guards, want) {
		t.Errorf("guards = %+v, want %+v", pv.Guards, want)
	}
	_, err = e.apply(root, pv.PlanID, "")
	wantCode(t, err, errcode.PlanGuardFailed)
	_, err = e.plan(root, "core.user.activate", UserActivateInput{Username: "nobody", Code: "918273"})
	wantCode(t, err, errcode.NotFound)
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserCreateAndActivate|TestUserCreateTakenUsername|TestUserCreateStateChanged|TestUserActivateWrongCode|TestUserActivateNotPending)$' ./internal/controller/coreactions/`

Expected: FAIL to compile; the first lines are:

```text
# github.com/darkraise/darkpanel/internal/controller/coreactions [github.com/darkraise/darkpanel/internal/controller/coreactions.test]
internal/controller/coreactions/enroll_test.go:79:9: undefined: UserActivateInput
internal/controller/coreactions/enroll_test.go:149:48: undefined: UserActivateInput
internal/controller/coreactions/enroll_test.go:166:48: undefined: UserActivateInput
internal/controller/coreactions/enroll_test.go:178:48: undefined: UserActivateInput
```

- [ ] **Step 3: Write the implementation**

In `internal/controller/coreactions/module.go`, add the line `m.enrollActions(),` after `m.userActions(),` in `Manifest`'s list, so the function reads:

```go
// Manifest declares the core module.
func (m module) Manifest() core.Manifest {
	var actions []core.ActionDef
	for _, group := range [][]core.ActionDef{
		m.hostActions(),
		m.moduleActions(),
		m.tokenActions(),
		m.auditActions(),
		m.userActions(),
		m.enrollActions(),
	} {
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
}
```

Create `internal/controller/coreactions/enroll.go`:

```go
package coreactions

import (
	"context"
	"fmt"
	"strconv"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/core"
)

// UserActivateInput is the input of core.user.activate/v1.
type UserActivateInput struct {
	Username string `json:"username" jsonschema:"pattern=^[a-z0-9][a-z0-9._-]{0\\,63}$" jsonschema_description:"The pending user's name."`
	Code     string `json:"code" secret:"true" jsonschema:"pattern=^[0-9]{6}$" jsonschema_description:"The six-digit code the authenticator shows for the user's new TOTP secret."`
}

func (m module) enrollActions() []core.ActionDef {
	return []core.ActionDef{
		core.DefineMutating[UserPasswordInput, Enrollment](core.ActionSpec{
			Name: "core.user.create", Version: 1, Class: core.ClassCompensable, Side: core.SideController, BreakGlass: true,
			CompensatedBy: "core.user.delete",
			Summary:       "Create a pending admin user and return its TOTP secret, shown only once.",
		}, userCreate{m}),
		core.DefineMutating[UserActivateInput, auth.User](core.ActionSpec{
			Name: "core.user.activate", Version: 1, Class: core.ClassCompensable, Side: core.SideController, BreakGlass: true,
			CompensatedBy: "core.user.reset-totp",
			Summary:       "Activate a pending user with a code from its authenticator, confirming its TOTP enrollment.",
		}, userActivate{m}),
	}
}

type userCreate struct{ m module }

func (h userCreate) Plan(ctx context.Context, _ core.Env, in UserPasswordInput) (core.PlanOutcome, error) {
	svc, err := h.m.authService()
	if err != nil {
		return core.PlanOutcome{}, err
	}
	users, err := svc.ListUsers(ctx)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	taken := false
	for _, u := range users {
		taken = taken || u.Username == in.Username
	}
	guard := core.Guard{ID: "username-free", OK: !taken, Message: "no user is named " + in.Username}
	if taken {
		guard.Message = "a user named " + in.Username + " already exists"
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "+", Text: fmt.Sprintf("user %s: pending admin, with a new TOTP secret shown once", in.Username)},
			{Op: " ", Text: "core.user.activate with a code from the secret makes the user active"},
			{Op: " ", Text: "compensated by core.user.delete"},
		},
		Guards:    []core.Guard{guard},
		StateHash: stateHash(in.Username, strconv.FormatBool(taken)),
	}, nil
}

func (h userCreate) Apply(ctx context.Context, _ core.Env, in UserPasswordInput) (Enrollment, error) {
	svc, err := h.m.authService()
	if err != nil {
		return Enrollment{}, err
	}
	u, secret, err := svc.CreateUser(ctx, in.Username, in.Password)
	if err != nil {
		return Enrollment{}, err
	}
	return Enrollment{User: u, TOTPSecret: secret}, nil
}

func (h userCreate) Verify(ctx context.Context, _ core.Env, in UserPasswordInput, _ Enrollment) error {
	return wantUserState(ctx, h.m, in.Username, "")
}

type userActivate struct{ m module }

// Plan cannot check the code: checking it needs the sealed secret and would
// spend the code's step, so a wrong code fails the apply instead.
func (h userActivate) Plan(ctx context.Context, _ core.Env, in UserActivateInput) (core.PlanOutcome, error) {
	_, u, _, err := h.m.userState(ctx, in.Username)
	if err != nil {
		return core.PlanOutcome{}, err
	}
	pending := u.State == auth.StatePending
	guard := core.Guard{ID: "user-pending", OK: pending, Message: u.Username + " is pending"}
	if !pending {
		guard.Message = u.Username + " is already " + u.State
	}
	return core.PlanOutcome{
		Diff: []core.DiffLine{
			{Op: "-", Text: fmt.Sprintf("user %s: %s", u.Username, u.State)},
			{Op: "+", Text: fmt.Sprintf("user %s: active, if the code matches its authenticator", u.Username)},
			{Op: " ", Text: "compensated by core.user.reset-totp"},
		},
		Guards:    []core.Guard{guard},
		StateHash: stateHash(u.ID, u.State),
	}, nil
}

func (h userActivate) Apply(ctx context.Context, _ core.Env, in UserActivateInput) (auth.User, error) {
	svc, err := h.m.authService()
	if err != nil {
		return auth.User{}, err
	}
	return svc.ActivateUser(ctx, in.Username, in.Code)
}

func (h userActivate) Verify(ctx context.Context, _ core.Env, in UserActivateInput, _ auth.User) error {
	return wantUserState(ctx, h.m, in.Username, auth.StateActive)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestUserCreateAndActivate|TestUserCreateTakenUsername|TestUserCreateStateChanged|TestUserActivateWrongCode|TestUserActivateNotPending)$' ./internal/controller/coreactions/`

Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/controller/coreactions`).

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go \
  internal/controller/coreactions/enroll.go \
  internal/controller/coreactions/enroll_test.go
git commit -m "feat(coreactions): create and activate users"
```


### Task 30: Core action registry test

**Files:**
- Test: `internal/controller/coreactions/registry_test.go`

**Interfaces:**
- Consumes: Contracts › Tasks 18, 23, 24, 26, 28 and 29 (all fourteen actions); `registry.Build` (foundation); `schema.ActionSnapshot`, `schema.Input` (plan 2); `errcode.From` (foundation).
- Produces: nothing (tests only).

**Items:** 3

**Depends on:** Task 29

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 0 - spec 0 - coupling 1 - risk 0 = 1

One external test file pins what the CLI generator, the OpenAPI document and the API's authorization read from the registry that `registry.Build()` returns: the fourteen core actions and no others, each version 1 and controller-side with a summary, with the expected class, break-glass flag, lock keys and compensating action; exactly the expected secret fields (`writeOnly` on inputs); a description on every input field; and an `internal` refusal, not a panic, from every handler of the zero-`Deps` module.

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/registry_test.go`:

```go
package coreactions_test

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/registry"
)

type declaration struct {
	class         core.Class
	breakGlass    bool
	lockKeys      []string
	compensatedBy string
}

var declarations = map[string]declaration{
	"core.host.list":           {class: core.ClassRead},
	"core.module.list":         {class: core.ClassRead},
	"core.module.enable":       {class: core.ClassCompensable, lockKeys: []string{"core.module"}, compensatedBy: "core.module.disable"},
	"core.module.disable":      {class: core.ClassCompensable, lockKeys: []string{"core.module"}, compensatedBy: "core.module.enable"},
	"core.token.list":          {class: core.ClassRead},
	"core.token.create":        {class: core.ClassCompensable, compensatedBy: "core.token.revoke"},
	"core.token.revoke":        {class: core.ClassIrreversible},
	"core.audit.list":          {class: core.ClassRead},
	"core.user.list":           {class: core.ClassRead, breakGlass: true},
	"core.user.create":         {class: core.ClassCompensable, breakGlass: true, compensatedBy: "core.user.delete"},
	"core.user.activate":       {class: core.ClassCompensable, breakGlass: true, compensatedBy: "core.user.reset-totp"},
	"core.user.reset-totp":     {class: core.ClassIrreversible, breakGlass: true},
	"core.user.reset-password": {class: core.ClassIrreversible, breakGlass: true},
	"core.user.delete":         {class: core.ClassIrreversible, breakGlass: true},
}

func coreActions(t *testing.T) []core.ActionDef {
	t.Helper()
	reg, err := registry.Build()
	if err != nil {
		t.Fatalf("registry.Build: %v", err)
	}
	var out []core.ActionDef
	for _, a := range reg.Actions() {
		if strings.HasPrefix(a.Spec.Name, core.ModuleIDCore+".") {
			out = append(out, a)
		}
	}
	return out
}

func TestCoreActionDeclarations(t *testing.T) {
	actions := coreActions(t)
	var names []string
	for _, a := range actions {
		names = append(names, a.Spec.Name)
		want, ok := declarations[a.Spec.Name]
		if !ok {
			t.Errorf("unexpected core action %s", a.Ref())
			continue
		}
		s := a.Spec
		got := declaration{class: s.Class, breakGlass: s.BreakGlass, lockKeys: s.LockKeys, compensatedBy: s.CompensatedBy}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", a.Ref(), got, want)
		}
		if s.Version != 1 || s.Side != core.SideController || s.Summary == "" {
			t.Errorf("%s: version %d, side %s, summary %q; want version 1, controller side, a summary", a.Ref(), s.Version, s.Side, s.Summary)
		}
	}
	if len(names) != len(declarations) {
		var want []string
		for name := range declarations {
			want = append(want, name)
		}
		sort.Strings(want)
		t.Errorf("core actions = %v, want %v", names, want)
	}
}

// schemaOf returns the object schema a top-level "$ref" points to, and the
// "$defs" it resolves against.
func schemaOf(t *testing.T, raw json.RawMessage) (map[string]any, map[string]any) {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	defs, _ := root["$defs"].(map[string]any)
	if ref, ok := root["$ref"].(string); ok {
		obj, _ := defs[strings.TrimPrefix(ref, "#/$defs/")].(map[string]any)
		return obj, defs
	}
	return root, defs
}

// secretProperties returns the top-level properties of raw marked x-secret,
// noting whether each is also writeOnly.
func secretProperties(t *testing.T, raw json.RawMessage) map[string]bool {
	t.Helper()
	obj, _ := schemaOf(t, raw)
	props, _ := obj["properties"].(map[string]any)
	out := map[string]bool{}
	for name, p := range props {
		prop, _ := p.(map[string]any)
		if prop["x-secret"] == true {
			out[name] = prop["writeOnly"] == true
		}
	}
	return out
}

func TestCoreActionSecrets(t *testing.T) {
	wantIn := map[string]map[string]bool{
		"core.user.create":         {"password": true},
		"core.user.reset-password": {"password": true},
		"core.user.activate":       {"code": true},
	}
	wantOut := map[string]map[string]bool{
		"core.token.create":    {"token": false},
		"core.user.create":     {"totpSecret": false},
		"core.user.reset-totp": {"totpSecret": false},
	}
	for _, a := range coreActions(t) {
		snap, err := schema.ActionSnapshot(a)
		if err != nil {
			t.Fatalf("%s: %v", a.Ref(), err)
		}
		var s struct{ Input, Output json.RawMessage }
		if err := json.Unmarshal(snap, &s); err != nil {
			t.Fatal(err)
		}
		in, out := secretProperties(t, s.Input), secretProperties(t, s.Output)
		if want := wantIn[a.Spec.Name]; !maps.Equal(in, want) {
			t.Errorf("%s: secret input fields (writeOnly) = %v, want %v", a.Ref(), in, want)
		}
		if want := wantOut[a.Spec.Name]; !maps.Equal(out, want) {
			t.Errorf("%s: secret output fields = %v, want %v", a.Ref(), out, want)
		}
	}
}

// TestCoreInputFieldsAreDocumented guards the CLI generator's input: every
// input field becomes a flag or a prompt whose help is its description.
func TestCoreInputFieldsAreDocumented(t *testing.T) {
	for _, a := range coreActions(t) {
		raw, err := schema.Input(a.InputType)
		if err != nil {
			t.Fatal(err)
		}
		obj, _ := schemaOf(t, raw)
		props, _ := obj["properties"].(map[string]any)
		for name, p := range props {
			if prop, _ := p.(map[string]any); prop["description"] == nil || prop["description"] == "" {
				t.Errorf("%s: input field %s has no description", a.Ref(), name)
			}
		}
	}
}

// TestZeroDepsFailClosed calls the handlers of the registry.Build() core
// module, which has no services: each refuses with internal instead of
// panicking.
func TestZeroDepsFailClosed(t *testing.T) {
	ctx := context.Background()
	defs := map[string]core.ActionDef{}
	for _, a := range coreActions(t) {
		defs[a.Spec.Name] = a
	}
	env := core.Env{Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin}}
	calls := map[string]func() error{}
	for name, input := range map[string]string{
		"core.token.list": `{}`, "core.audit.list": `{}`, "core.user.list": `{}`,
	} {
		calls[name] = func() error { _, err := defs[name].Handler.Read(ctx, env, json.RawMessage(input)); return err }
	}
	for name, input := range map[string]string{ //nolint:gosec // G101: test inputs, not credentials
		"core.token.create":        `{"name":"ci","scope":"read"}`,
		"core.token.revoke":        `{"id":"t1"}`,
		"core.user.create":         `{"username":"alice","password":"correct horse battery"}`,
		"core.user.activate":       `{"username":"alice","code":"123456"}`,
		"core.user.reset-totp":     `{"username":"alice"}`,
		"core.user.reset-password": `{"username":"alice","password":"correct horse battery"}`,
		"core.user.delete":         `{"username":"alice"}`,
	} {
		calls[name] = func() error { _, err := defs[name].Handler.Plan(ctx, env, json.RawMessage(input)); return err }
	}
	calls["core.module.enable apply"] = func() error {
		_, err := defs["core.module.enable"].Handler.Apply(ctx, env, json.RawMessage(`{"hostId":"h1","moduleId":"demo"}`))
		return err
	}
	var names []string
	for name := range calls {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		err := calls[name]()
		if err == nil || errcode.From(err).Code != errcode.Internal {
			t.Errorf("%s with zero Deps: err = %v, want internal", name, err)
		}
	}
}
```

- [ ] **Step 2: Run the test**

Run: `GOTOOLCHAIN=local timeout 300 go test -count=1 -run '^(TestCoreActionDeclarations|TestCoreActionSecrets|TestCoreInputFieldsAreDocumented|TestZeroDepsFailClosed)$' ./internal/controller/coreactions/`

Expected: PASS. This task adds tests only; Tasks 18, 23, 24, 26, 28 and 29 built the behaviour they pin, so a failure here is a defect in those tasks and is reported, not worked around.

- [ ] **Step 3: Run the full checks**

Run, each with `GOTOOLCHAIN=local`:
- `timeout 300 go vet ./...`
- `timeout 900 go test -count=1 ./...`
- `timeout 600 go test -race -count=1 ./internal/controller/coreactions/`
- `timeout 900 golangci-lint run --allow-serial-runners ./...`
- `timeout 300 go mod tidy -diff`

Expected: every package `ok`, lint `0 issues.`, and no output from `go mod tidy -diff`.

- [ ] **Step 4: Commit**

```bash
git add internal/controller/coreactions/registry_test.go
git commit -m "test(coreactions): check the core action registry"
```


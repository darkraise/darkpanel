# M2 foundation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build M2 part P0: every shared dependency, type, interface, error code, path, data package and generator the later M2 parts consume, so that parts P1-P7 can run in parallel against fixed names.

**Architecture:** Additive changes across the existing packages plus five new ones. `internal/core` gains the fleet and update action types, the Hello fields, `Env.ChangeID`, `core.Uninstaller`, owned-path kinds and the duplicate-ownership rule; `internal/core/lifecycle` gains the operation reservation (`Reserve`, `Release`, the `*` key); `internal/controller/dispatch` reads agent clients through `ClientSource`; `internal/cli` gains group adoption and local fallbacks; `internal/platform` gains the systemd D-Bus provider. New packages: `internal/agent/wire` (agent API bodies), `schemas` (embedded snapshots and schema hashes), `internal/update/trustedkeys` (release keys), `scripts/gen-install-sh` (renders `install.sh.in`), and `internal/deps` (keeps the new modules in `go.mod` until code imports them). The running binary's behaviour changes in one place only: a host that has a `core_hosts` row but no agent client answers `host.offline` where M1 answered `not_found` (C9).

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `github.com/godbus/dbus/v5` v5.2.4, `aead.dev/minisign` v0.3.0, `github.com/hashicorp/yamux` v0.1.2, `github.com/invopop/jsonschema` (existing), POSIX `sh` with OpenSSL 3 for `install.sh.in`.

**Spec:** docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves

**Plan review:** 2026-10-19 — dr-superpowers:judge-opus — executability 16 / coherence 17 / coverage 17 / assumptions 14 (round 1)

**Parallelism:** waves — 1 | 2-4 | 5-7 | 8 | 9-11 | 12-14 | 15-17 | 18

**Integration check:** `GOTOOLCHAIN=local go build ./... && GOTOOLCHAIN=local go test -count=1 ./... && GOTOOLCHAIN=local go vet ./... && golangci-lint run --allow-serial-runners && GOTOOLCHAIN=local go mod tidy -diff && GOTOOLCHAIN=local go vet -tags harnesskeys ./internal/update/trustedkeys/ ./scripts/gen-install-sh/ && golangci-lint run --allow-serial-runners --build-tags harnesskeys ./internal/update/trustedkeys/ ./scripts/gen-install-sh/`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- Dependencies: only Task 1 edits `go.mod` and `go.sum`. After every later task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing. Pins are exact: `aead.dev/minisign v0.3.0`, `github.com/godbus/dbus/v5 v5.2.4`, `github.com/hashicorp/yamux v0.1.2` (spec "Versions").
- JSON fields are camelCase. A struct tag value is a Go quoted string, so a backslash or a comma inside a `jsonschema` pattern is written doubled, as `internal/controller/coreactions/enroll.go:14` does (`{0\\,63}`, `\\.`). Optional input fields use `omitempty`; nullable output fields are pointers tagged `jsonschema:"nullable"`, as `internal/controller/coreactions/hosts.go:25` does. Types in `internal/core/contract.go` keep their style of no JSON tags; action input and output types in `internal/core/fleet.go` carry JSON tags.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix the file already uses (`lifecycle:`, `dispatch:`, `platform:`, `cli:`, `core:`, `paths:`); a typed error built with `errcode.New` or `errcode.Wrap` carries its code instead.
- No task creates a production signing key, the GitHub `release` environment or a secret (spec section 9: the owner does that). The only key material in this plan is the harness test key pair of Task 16, trusted only by builds with the `harnesskeys` tag.
- Nothing in this plan talks to the real systemd or D-Bus except `TestSystemdLive` (Task 13), which runs only when `DARKPANEL_SYSTEMD_LIVE=1` and the user is root, and skips otherwise. Every other test uses `t.TempDir()`, binds only `127.0.0.1:0`, and keeps its sockets under `os.MkdirTemp("", "dp")`, so tasks of one wave share no port, path or global state.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean. `--allow-serial-runners` is required because golangci-lint holds a shared lock and tasks of one wave may lint together. Files behind the `harnesskeys` tag are also checked with `GOTOOLCHAIN=local go vet -tags harnesskeys ./internal/update/trustedkeys/ ./scripts/gen-install-sh/` and `golangci-lint run --allow-serial-runners --build-tags harnesskeys ./internal/update/trustedkeys/ ./scripts/gen-install-sh/`.
- Each task of a wave runs in its own worktree from the wave's base, so a package-wide command sees only merged work plus the task's own files. Two tasks of one wave never share a file.
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.

## Contracts

Every name below is produced by exactly one task and consumed as written. Later M2 parts consume them too.

**C1. Dependencies (Task 1).** `go.mod` requires `aead.dev/minisign v0.3.0`, `github.com/godbus/dbus/v5 v5.2.4` and `github.com/hashicorp/yamux v0.1.2`. `internal/deps/deps.go` (build tag `deps`, package `deps`) blank-imports the three so `go mod tidy` keeps them until code imports them; the M2 assembly part removes the file, as M1's did.

**C2. Error codes (Task 2), `internal/core/errcode`.** Problem codes (constant, value, HTTP status, title):

| Constant | Value | Status | Title |
|---|---|---|---|
| `HostOffline` | `host.offline` | 503 | Host offline |
| `HostVersionUnsupported` | `host.version_unsupported` | 409 | Agent version unsupported |
| `AgentNotEnrolled` | `agent.not_enrolled` | 409 | Agent not enrolled |
| `EnrollTokenInvalid` | `enroll.token_invalid` | 403 | Invalid enrollment token |
| `EnrollRateLimited` | `enroll.rate_limited` | 429 | Too many enrollment attempts |
| `UpdateInProgress` | `update.in_progress` | 409 | Update in progress |
| `UpdateUnconfirmedChanges` | `update.unconfirmed_changes` | 409 | Unconfirmed changes |
| `UpdateDowngradeRefused` | `update.downgrade_refused` | 409 | Downgrade refused |
| `UpdateNotFound` | `update.not_found` | 404 | Release not found |
| `UpdateNothingToRollBack` | `update.nothing_to_roll_back` | 409 | Nothing to roll back |
| `RolloutInProgress` | `rollout.in_progress` | 409 | Rollout in progress |

Action-level codes (they appear in an update record's or a change's error, status 500): `UpdateSignatureInvalid` = `update.signature_invalid` (Release signature invalid), `UpdatePrevMismatch` = `update.prev_mismatch` (Previous binary does not match), `UpdateInterrupted` = `update.interrupted` (Update interrupted).

**C3. Paths (Task 3), `internal/paths`.** `Paths` gains five fields:

| Field | `Production()` | `Under(root)` |
|---|---|---|
| `AgentSocket` | `/run/darkpanel-agent/agent.sock` | `root/run/agent.sock` |
| `AgentTLSDir` | `/etc/darkpanel/agent` | `root/keys/agent` |
| `UpdateDir` | `/var/lib/darkpanel/update` | `root/update` |
| `BinaryDir` | `/usr/local/lib/darkpanel` | `root/bin` |
| `BinaryLink` | `/usr/local/bin/darkpanel` | `root/link/darkpanel` |

In the development layout, when `XDG_RUNTIME_DIR` is absolute, `AgentSocket` is `$XDG_RUNTIME_DIR/darkpanel-dev/agent.sock`. Methods (all `filepath.Join` of a field and a fixed name): `Binary()` = `BinaryDir/darkpanel`, `PrevBinary()` = `BinaryDir/darkpanel.prev`, `RecoveryBinary()` = `BinaryDir/darkpanel.recovery`, `FailedBinary()` = `BinaryDir/darkpanel.failed`, `NewBinary(tx string)` = `BinaryDir/darkpanel.new-<tx>`, `UpdateLock()` = `UpdateDir/lock`, `UpdateRecord(tx string)` = `UpdateDir/<tx>.json`, `AgentTLSCurrent()` = `AgentTLSDir/current`. `MkdirAll` is unchanged: the controller calls it (internal/controller/server/server.go:177) and from M2 runs unprivileged, so it must never create the root-only agent directories. A new `MkdirAgent()` creates `AgentTLSDir`, `UpdateDir` and the directory of `AgentSocket` with mode 0700, for the agent daemon and the installer. Neither creates `BinaryDir` or the link's directory, which only the installer writes.

**C4. Core contract (Task 4), `internal/core/contract.go`.**
- `Env.ChangeID string`: the id of the change whose Apply or Verify is running; empty for Read, Plan, Snapshot and Revert.
- `AgentHello` gains `Arch string` (`runtime.GOARCH`), `JournalEpoch string` (a ULID), `ControllerConnected *bool` (nil when the agent is not enrolled) and `SchemaHashes map[string]SchemaHash`, keyed `"<action name>@<version>"`.
- `type SchemaHash struct{ Input, Output string }`: lower-case hex SHA-256 values (C12).
- `type Uninstaller interface { Uninstall(ctx context.Context, env Env, keepData bool) error }`: a `Module` implements it optionally; uninstall finds it with a type assertion. `Manifest.Uninstall` is removed (nothing sets it).
- Owned-path kinds: `OwnedFile = "file"`, `OwnedBlock = "block"`, `OwnedDir = "dir"`, `OwnedUser = "user"`, `OwnedUnit = "unit"`. `OwnedPath.Kind` takes one of them; for `OwnedUser`, `Path` is the user name.

**C5. Fleet and update types (Task 5), `internal/core/fleet.go`.** Constants: host states `HostPending = "pending"`, `HostEnrolled = "enrolled"`, `HostRevoked = "revoked"`; channels `ChannelStable = "stable"`, `ChannelBeta = "beta"`; actors `ActorRollout = "system:rollout"`, `ActorUpdateSchedule = "system:update-schedule"`, `ActorUnenrolled = "local:root@unenrolled"`; transaction kinds `UpdateKindUpdate = "update"`, `UpdateKindRollback = "rollback"`; transaction states `UpdatePending = "pending"`, `UpdateDownloading = "downloading"`, `UpdateStaged = "staged"`, `UpdateSwapped = "swapped"`, `UpdateRollingBack = "rolling-back"`, `UpdateCommitted = "committed"`, `UpdateRolledBack = "rolled-back"`, `UpdateFailed = "failed"`; rollout states `RolloutRunning = "running"`, `RolloutDraining = "draining"`, `RolloutSucceeded = "succeeded"`, `RolloutFailed = "failed"`, `RolloutCancelled = "cancelled"`; rollout host states `RolloutHostWaiting = "waiting"`, `RolloutHostUpdating = "updating"`, `RolloutHostCommitted = "committed"`, `RolloutHostFailed = "failed"`, `RolloutHostSkipped = "skipped"`. Types (JSON names in parentheses):
- `ModuleState` (`id`, `version`, `enabled`, `available`, `reason` nullable, `checkedAt` nullable): the same shape as `coreactions.ModuleState`. The host-actions part switches `core.host.list` to the `core` types and deletes the `coreactions` copies; this plan leaves `coreactions` unchanged.
- `Host` (`id`, `name`, `isLocal`, `state`, `online`, `lastSeenAt` nullable, `agentVersion` nullable, `protocolVersion` nullable, `os` nullable, `arch` nullable, `supported`, `modules`); `HostList` (`hosts`).
- `HostCreateInput` (`name`, pattern `^[a-z0-9][a-z0-9.-]{0,62}$`; `controllerAddress` optional); `HostTokenInput` (`hostId`; `controllerAddress` optional); `HostIDInput` (`hostId`); `EnrollmentGrant` (`host`, `token` with `secret:"true"`, `expiresAt`). `controllerAddress` has pattern `^(\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+):[0-9]{1,5}$`.
- `UpdateApplyInput` (`version`, semantic version pattern `^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`; `channel` enum stable, beta; `force`); `UpdateRollbackInput` (`force`); `UpdateStatusInput` (`operationId` optional, `changeId` optional); `UpdateStatus` (`transactions`); `ErrorDetail` (`code`, `message`); `UpdateTransaction` (`id`, `operationId`, `parentId` nullable, `kind`, `fromVersion`, `toVersion`, `targetVersion`, `state`, `error` nullable `*ErrorDetail`, `startedAt`, `updatedAt`).
- `UpdateSchedule` (`days`: enum `mon`…`sun`, at least one; `time`: pattern `^([01][0-9]|2[0-3]):[0-5][0-9]$`); `UpdatePolicy` (`hostId`, `channel`, `schedule` nullable `*UpdateSchedule`), used as both input and output of `core.host.update-policy/v1`.
- `RolloutInput` (`hosts`, at least one; `version`; `channel`); `RolloutCancelInput` (`rolloutId`, `force`); `RolloutsInput` (`limit` optional, 1 to 100, 20 when omitted); `RolloutHost` (`hostId`, `position`, `state`, `planId` nullable, `operationId` nullable, `error` nullable `*ErrorDetail`); `Rollout` (`id`, `version`, `channel`, `state`, `error` nullable `*ErrorDetail`, `createdAt`, `updatedAt`, `hosts`); `RolloutList` (`rollouts`).

**C6. Registry rules (Task 6), `internal/core/registry.go`.** `ReservedNames` gains `"system"`. `NewRegistry` refuses two modules (the core included) that declare the same owned path, the same marked block (same path and block id), or the same user; a path declared as `OwnedBlock` by two modules with no common block id is allowed. The error names both module ids: `owned path <path> is declared by modules <a> and <b>`, `marked block <id> of <path> is declared by modules <a> and <b>`, `user <name> is declared by modules <a> and <b>`.

**C7. Change id in the engine (Task 7), `internal/core/lifecycle`.** The engine sets `Env.ChangeID` to the change id for the handler's Apply and Verify calls, including the Verify that startup recovery runs.

**C8. Operation reservation (Task 8), `internal/core/lifecycle/reserve.go`.**
- `const ReserveAll = "*"`; `func ReservationOwner(operationID string) string` returns `"op:" + operationID`.
- `func Reserve(ctx context.Context, db *store.DB, key, owner string) error` runs one write transaction of its own (never inside another): it succeeds without change when the only lock row is `(key, owner)`; otherwise it fails with `update.in_progress` when another owner holds a `*` row, and with `update.unconfirmed_changes` when any lock row other than `(key, owner)` exists or, unless owner already holds key, when a revertible change is `applying` or `pending`; else it inserts `(key, owner)`.
- `func Release(ctx context.Context, db *store.DB, owner string) error` deletes every lock row of `owner`.
- While a `*` row exists, the engine's lock check before the plan recheck and its lock taking refuse every key of every other owner with `lock.held`, detail `lock <key> is unavailable while update operation <owner> holds every lock`.
- Startup recovery's lock sweep is unchanged: it deletes only rows owned by finished changes, so `op:` rows survive it.

**C9. Agent clients (Task 9), `internal/controller/dispatch/clients.go`.**
```go
type ClientSource interface {
	Client(ctx context.Context, hostID string) (core.AgentClient, uint64, error)
}
type StaticClients struct{ /* unexported */ }
func NewStaticClients() *StaticClients
func (s *StaticClients) Add(hostID string, c core.AgentClient) // a new generation per call
func (s *StaticClients) Client(ctx context.Context, hostID string) (core.AgentClient, uint64, error)
```
A host without a client answers `errcode.HostOffline` with detail `host <id> is offline`. Generations are positive and never repeat within a process. `dispatch.Config.Hosts` stays `*Hosts`; the seam the spec's section 6 describes is `dispatch.HostsConfig.Clients ClientSource`, which is required (`NewHosts` panics without it) and `Hosts.Add` is removed (Task 12). `Hosts.Agent` and `Hosts.Hello` answer `not_found` (`no host <id>`) for an id with no `core_hosts` row, as in M1, and the source's `host.offline` for a known host without a client. A change missing from `core_change_index` is looked for on every `core_hosts` row, skipping a host that answers `host.offline`.

**C10. Core module slots (Task 10), `internal/controller/coreactions.Deps`.** `Extra []core.ActionDef` (appended to the core manifest's actions after the built-in groups) and `OwnedPaths []core.OwnedPath` (the core manifest's `OwnedPaths`).

**C11. CLI hooks (Task 11), `internal/cli.Options`.**
```go
// Adopt maps a hand-written top-level command in Commands to the action
// names whose generated commands become its subcommands.
Adopt map[string][]string
// LocalFallbacks run an agent-side action in-process when --local finds
// no agent socket, keyed by action name.
LocalFallbacks map[string]LocalFallback

type LocalFallback func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
```
`NewRoot` fails when an `Adopt` key is not the name of a command in `Commands`, an adopted name is not a registered action, an adopted action's `CommandPath` does not start with that key, or an adopted command's name equals an existing subcommand of the group; and when a `LocalFallbacks` key is not a registered agent-side action. The `--local` flag that calls a fallback is the assembly part's; it reads `Options.LocalFallbacks`.

**C12. Schemas (Task 15), package `schemas` (`schemas/embed.go`, import path `github.com/darkraise/darkpanel/schemas`).** `var FS embed.FS` holds `<action or resource>/v<n>.json` and `released/<tag>/<action or resource>/v<n>.json`; `schemas/released/README.md` keeps the directory present. `func Hash(member json.RawMessage) (string, error)`: decode into `any`, `json.Marshal`, lower-case hex SHA-256. `func SnapshotHashes(snapshot []byte) (core.SchemaHash, error)`: `Hash` of the snapshot's `input` and `output` members (a resource snapshot has no `input`; its `Input` is the hash of JSON `null`).

**C13. Systemd (Task 13), `internal/platform/systemd.go`.**
```go
type Systemd interface {
	StartTransientService(ctx context.Context, name string, svc TransientService) error
	StartTransientTimer(ctx context.Context, name string, t TransientTimer) error
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	Kill(ctx context.Context, unit string, sig syscall.Signal) error
	ResetFailed(ctx context.Context, unit string) error
	Enable(ctx context.Context, unitFiles ...string) error
	Disable(ctx context.Context, unitFiles ...string) error
	Reload(ctx context.Context) error
	State(ctx context.Context, unit string) (UnitState, error)
}
type TransientService struct {
	Description string
	ExecStart   []string // ExecStart[0] is an absolute path
	OnFailure   []string // unit names
}
type TransientTimer struct {
	Description string
	OnActive    time.Duration
	Accuracy    time.Duration
	Service     TransientService // started by the timer as <timer name without .timer>.service
}
type UnitState struct{ LoadState, ActiveState, SubState string }
func NewSystemd(ctx context.Context) (*DBusSystemd, error) // the system bus
func (s *DBusSystemd) Close() error
```
Every method queues its job and returns; callers poll `State` for completion. A unit that does not exist reports `LoadState` `not-found`. `ProductionAllowlist()` returns `/usr/sbin/useradd` and `/usr/sbin/userdel`.

**C14. Agent wire types (Task 14), `internal/agent/wire`.** Header names `HeaderPrincipal = "Darkpanel-Principal"`, `HeaderProtocol = "Darkpanel-Protocol"`, `HeaderVersion = "Darkpanel-Version"`, `HeaderControllerID = "Darkpanel-Controller-Id"`; `MaxBody = 4 << 20`. Types with camelCase JSON, times in UTC, durations in integer seconds, nullable fields as pointers: `Principal`, `Hello`, `SchemaHash`, `ModuleStatus`, `PlanView`, `ApplyOptions` (`confirmTimeoutSeconds`, `confirmation`, `confirmNow`), `ChangeView`, `ChangeError`, `JournalEntry`, `Ack` (`seq`). Each type `T` has `FromT(core value) T` and `(T) Core() core value` (`FromPrincipal`, `FromHello`, `FromModuleStatus`, `FromPlanView`, `FromApplyOptions`, `FromChangeView`, `FromJournalEntry`); `ChangeFilterQuery(core.ChangeFilter) url.Values` and `ParseChangeFilter(url.Values) (core.ChangeFilter, error)` carry the change filter as query parameters `state`, `hostId`, `before`, `limit`; an unknown or repeated parameter, or a `limit` that is not a positive integer, is `request.invalid_input`.

**C15. Trusted keys (Task 16), `internal/update/trustedkeys`.** `type Key struct{ ID, PublicKey string }`: `ID` is the minisign key id as 16 upper-case hex digits, zero-padded (`fmt.Sprintf("%016X", minisign.PublicKey.ID())`); `PublicKey` is the base64 minisign public key line. `func Keys() []Key` (a copy), `func TestBuild() bool`, `func BaseURL() string`. Without the `harnesskeys` tag: no keys until the owner adds the production key, `TestBuild()` false, base `https://github.com/Darkraise/darkpanel/releases/download`. With `harnesskeys`: the harness test key `BCB10AB3E9AF694E` / `RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD`, `TestBuild()` true, base `http://198.18.0.20:8000`. The harness private key is committed as `internal/update/trustedkeys/testdata/harness.key` (unencrypted minisign format).

**C16. Release signatures.** Release files are `<base>/v<version>/darkpanel_<version>_linux_<arch>.tar.gz`, `checksums.txt` and one `checksums.txt.<Key.ID>.minisig` per signing key, in minisign hashed mode (algorithm `ED`), with trusted comment exactly `version=<version> channel=<stable|beta> purpose=checksums`. Each tarball holds `darkpanel`, `LICENSE` and `README.md` at the archive root, with no directory prefix. `checksums.txt` is `sha256sum` output (`<hex>  <name>` lines) covering both tarballs and the SBOM `darkpanel_<version>.cdx.json`, not `install.sh`, so `install.sh` checks only its own tarball's line. The release build regenerates `install.sh` and fails when it differs from the committed file, so the generator's output is deterministic. The release index is `<base>/index/releases.json` = `{"formatVersion": 1, "releases": [{"version": "…", "channel": "stable|beta"}, …]}`; each release object has exactly the members `version` then `channel`, in that order, because `install.sh` reads the file with `tr`, `grep` and `sort` and has no JSON parser. `install.sh` accepts a trusted comment whose channel is `stable` or `beta`, as long as the version is the one being installed.

**C17. ADR 0012 (Task 17).** `docs/adr/0012-m2-contract-and-layout-additions.md`, Status `Proposed`, listed in `docs/adr/README.md`.

**C18. Install script generator (Task 18).** `install.sh.in` at the repository root holds the placeholders `@DARKPANEL_KEYS@` and `@DARKPANEL_BASE_URL@`. `GOTOOLCHAIN=local go run ./scripts/gen-install-sh [-in install.sh.in] [-out install.sh]` writes the script with mode 0755 from `trustedkeys.Keys()` and `trustedkeys.BaseURL()`; with `-tags harnesskeys` it embeds the harness key and base. `install.sh` takes `--role controller|agent` (required), `--version <v>`, `--base-url <url>` and `--token-file <path>`, and ends by running `darkpanel system install --role <role>` with the agent's token on standard input. Standard input of `darkpanel system install`: empty for the controller role; for the agent role, the token when `--token-file` was given or when no saved credentials exist (then the script prompts on `/dev/tty`), and empty when `<AgentTLSDir>/current` exists and no `--token-file` was given. `system install` therefore treats an empty standard input as "no token", and when `darkpanel agent check` refuses the saved credentials (exit 1) without a token it fails with a message telling the operator to rerun `install.sh` with `--token-file`. `install.sh` checks `--token-file` (owner, mode 0600 or 0400) before downloading anything. For `--role controller` it also takes `--agent-listen <addr:port>` (repeatable) and `--agent-advertise <host:port>` and passes them to `darkpanel system install`; each value must match `^[][A-Za-z0-9.:-]+$`. Test hooks, which the install part's fixture tests rely on: `DARKPANEL_INSTALL_SH_LIB=1` defines the functions without running anything; `DARKPANEL_INSTALL_ROOT=<dir>` prefixes the `/etc` and `/usr/local` paths and only then skips the root check, and with it set `--token-file` must be owned by the calling uid instead of root. This plan does not commit a generated `install.sh`; the install part does.

## Assumptions (evidence)

- The three pins exist on the module proxy and add only their own lines: on 2026-10-19 `go list -m -versions` listed `v0.3.0`, `v5.2.4` and `v0.1.2` as the newest of each, and in a scratch worktree of `a14bbf3` adding `internal/deps/deps.go` and running `go get` with the three pins then `go mod tidy` added exactly three `require` lines and six `go.sum` lines, after which `go build -tags deps ./internal/deps/` succeeded. Sums: `aead.dev/minisign` `h1:8Xafzy5PEVZqYDNP60yJHARlW1eOQtsKNp/Ph2c0vRA=`, `godbus/dbus/v5` `h1:F2gUCy++NMIxGmo9J+z5le/+ZC8W1OgeR51P4i9ZB/U=`, `yamux` `h1:XtB8kyFOyHXYVFnwT5C3+Bdo8gArse7j2AQ0DA0Uey8=`.
- `aead.dev/minisign` v0.3.0 signs in hashed mode (`ED`) only through `minisign.NewReader(r).SignWithComments`, while `minisign.Sign` signs legacy `Ed`: minisign.go:96-160 of the module. `PrivateKey.MarshalText` writes an unencrypted key that `PrivateKey.UnmarshalText` reads: private.go:107-135. `PublicKey.MarshalText` prints the key id without zero padding: public.go:75.
- `install.sh`'s verification works with OpenSSL alone: on 2026-10-19 a POSIX `sh` script on this host (OpenSSL 3.5.6, `dash -n` clean) verified a hashed minisign signature made by the Go library with `openssl dgst -blake2b512 -binary`, `openssl pkey -pubin -inform DER` from the 12-byte Ed25519 SPKI prefix `302a300506032b6570032100` plus the 32 key bytes, and `openssl pkeyutl -verify -pubin -rawin -sigfile`, and refused a changed version, a changed channel and a changed `checksums.txt`. Ubuntu 22.04 ships OpenSSL 3.0.2, where `pkeyutl -rawin` and `blake2b512` exist; on 2026-10-19 the script's `verify_release` from a dry run of Task 18, run in lib mode under `dash` in `docker run --rm` containers, gave identical results on ubuntu:22.04 (OpenSSL 3.0.2), ubuntu:24.04 (3.0.13), debian:12 (3.0.22), debian:13 (3.5.7) and this host (3.5.6): a valid release accepted; a tampered `checksums.txt`, a wrong version, an untrusted key under the trusted key's file name, and that signature with its key-id bytes patched to the trusted id all refused.
- `StartTransientUnit` accepts `ExecStart` as `a(sasb)`, `OnFailure` as `as`, and a timer's `OnActiveSec` and `AccuracyUSec` as `t` microseconds with the service as an auxiliary unit, as `systemd-run --on-active` sends them: on 2026-10-19 a dry run of Task 13's code on this host (systemd 257, root) gave `--- PASS: TestSystemdLive (0.07s)` with `DARKPANEL_SYSTEMD_LIVE=1`: the transient service and timer started (`active/waiting` for the timer), `KillUnit` with SIGKILL left `failed`, `ResetFailedUnit` cleared it, `StopUnit` worked, `LoadUnit` reported `not-found` for a missing unit, and no `darkpanel-live-*` unit remained. Task 13 reruns it.
- Startup recovery deletes only lock rows whose owner is a finished change (internal/core/lifecycle/recover.go:68-72), so an `op:` reservation survives a restart with no change to recovery.
- Every caller of `Hosts.Add` and `NewHosts` is in `internal/controller/dispatch/*_test.go` or `internal/controller/server/services.go:75-76` (`rg -n 'NewHosts|hosts\.Add\(' --type go`, 2026-10-19).
- Nothing sets `core.Manifest.Uninstall` (`rg -n 'Uninstall' --type go`, 2026-10-19: only its declaration at internal/core/contract.go:216).
- The snapshot test and `schema.WriteSnapshots` ignore non-JSON files under `schemas/` and skip `schemas/released/` (internal/modules/modules_test.go:78-92, internal/core/schema/snapshot.go:126-140), so `schemas/embed.go` and `schemas/released/README.md` need no change there. `go:embed` patterns cannot exclude a file, so `schemas/embed.go` embeds `*/*.json` (every current snapshot is one directory deep) and the `released` directory, which the README keeps present.
- Schema hash test vectors, computed on 2026-10-19 with `encoding/json` decode into `any` then `Marshal`: `schemas/core.user.delete/v1.json` input `d3b3f8f18795bff4bd079f933449546518a8447431dd2ccfe0ad762b157a511d`, output `66fd38057a96d6525f6628f34c25cf81ea54119c1b052af31a4f53b0208781fc`; `schemas/core.token.revoke/v1.json` input `70958110f1480feebf8146303df39c9d80494e4eb6f5ec162590966eb3793c45`, output `0459807f13c2660e3255cd4a9773c4779e5b43882056eb9ecaf9e4807dd7d920`. Task 15's test embeds copies of the two files so a later snapshot change cannot move the vectors.
- `shellcheck` is not installed on this host (2026-10-19); Task 18's test runs it when present and otherwise skips that check with a log line. A dry run with ShellCheck 0.11.0 fetched into a scratch directory reported nothing on the production and the harness renderings of Task 18's script. CI's shellcheck step belongs to the release-workflow plan.
- The error codes' HTTP statuses are this plan's choice; the spec names the codes only. `host.offline` is 503 because the request may succeed later without change; `enroll.token_invalid` is 403, not 401, because no credential scheme applies.
- Codex is off this session (`codex-gate usable=false reason=plugin-not-enabled`, 2026-10-19), so no task carries an `**Executor:**` line and plan review runs on `dr-superpowers:judge-opus`.

## Task index

1. Pin the M2 dependencies
2. Error codes for fleet, enrollment, update and rollout
3. Agent, update and binary paths
4. Core contract additions
5. Fleet and update action types
6. Duplicate ownership rule and the reserved name system
7. Change id in the lifecycle engine
8. Operation reservation and the all-locks key
9. Agent client source
10. Core module slots for extra actions and owned paths
11. CLI group adoption and local fallbacks
12. Dispatch hosts over the client source
13. Systemd D-Bus provider and exec allowlist
14. Agent API wire types
15. Embedded schema snapshots and schema hashes
16. Trusted release keys
17. ADR 0012
18. Install script template and generator

---

### Task 1: Pin the M2 dependencies

**Files:**
- Create: `internal/deps/deps.go`
- Modify: `go.mod`, `go.sum`

**Interfaces:**
- Consumes: none
- Produces: C1 — the three modules are in `go.mod` and `go.sum`, kept there by `internal/deps/deps.go` (build tag `deps`) until code imports them.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 1 = 2

This task adds no test: its check is that the pin file builds with the `deps` tag, that `go mod tidy` keeps exactly the three new modules, and that the rest of the tree still builds.

- [ ] **Step 1: Create the pin file**

Create `internal/deps/deps.go`:

```go
//go:build deps

// Package deps keeps the M2 modules in go.mod until code imports them; go mod
// tidy considers every build tag, so this file is enough. The M2 assembly
// removes it once each module has an importer.
package deps

import (
	_ "aead.dev/minisign"
	_ "github.com/godbus/dbus/v5"
	_ "github.com/hashicorp/yamux"
)
```

- [ ] **Step 2: Check that it does not build yet**

Run: `GOTOOLCHAIN=local go build -tags deps ./internal/deps/`
Expected: FAIL with `no required module provides package aead.dev/minisign` (and the same for the other two).

- [ ] **Step 3: Add the modules at their pinned versions**

Run:

```bash
GOTOOLCHAIN=local go get aead.dev/minisign@v0.3.0 github.com/godbus/dbus/v5@v5.2.4 github.com/hashicorp/yamux@v0.1.2
GOTOOLCHAIN=local go mod tidy
```

Expected: `go get` prints `go: added aead.dev/minisign v0.3.0`, `go: added github.com/godbus/dbus/v5 v5.2.4` and `go: added github.com/hashicorp/yamux v0.1.2`. `git diff --stat` then shows `go.mod | 3 +++` and `go.sum | 6 ++++++` and nothing else under the repository besides the new file. The direct `require` block gains exactly:

```
	aead.dev/minisign v0.3.0
	github.com/godbus/dbus/v5 v5.2.4
	github.com/hashicorp/yamux v0.1.2
```

and `go.sum` gains exactly these six lines:

```
aead.dev/minisign v0.3.0 h1:8Xafzy5PEVZqYDNP60yJHARlW1eOQtsKNp/Ph2c0vRA=
aead.dev/minisign v0.3.0/go.mod h1:NLvG3Uoq3skkRMDuc3YHpWUTMTrSExqm+Ij73W13F6Y=
github.com/godbus/dbus/v5 v5.2.4 h1:F2gUCy++NMIxGmo9J+z5le/+ZC8W1OgeR51P4i9ZB/U=
github.com/godbus/dbus/v5 v5.2.4/go.mod h1:3AAv2+hPq5rdnr5txxxRwiGjPXamgoIHgz9FPBfOp3c=
github.com/hashicorp/yamux v0.1.2 h1:XtB8kyFOyHXYVFnwT5C3+Bdo8gArse7j2AQ0DA0Uey8=
github.com/hashicorp/yamux v0.1.2/go.mod h1:C+zze2n6e/7wshOZep2A70/aQU6QBRWJO/G6FT1wIns=
```

If any other line of `go.mod` or `go.sum` changes, stop and report it: no other module may move.

- [ ] **Step 4: Verify**

Run:

```bash
GOTOOLCHAIN=local go build -tags deps ./internal/deps/
GOTOOLCHAIN=local go build ./...
GOTOOLCHAIN=local go mod tidy -diff
golangci-lint run --allow-serial-runners
```

Expected: the two builds print nothing, `go mod tidy -diff` prints nothing, golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/deps/deps.go go.mod go.sum
git commit -m "chore(deps): pin the M2 modules"
```

---

### Task 2: Error codes for fleet, enrollment, update and rollout

**Files:**
- Modify: `internal/core/errcode/errcode.go`
- Test: `internal/core/errcode/errcode_test.go`

**Interfaces:**
- Consumes: none
- Produces: C2 — the fourteen codes, their statuses and titles.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

In `internal/core/errcode/errcode_test.go`, in `TestStatus`, replace

```go
		{ChangeRevertFailed, 500},
		{Code("no.such_code"), 500},
```

with

```go
		{ChangeRevertFailed, 500},
		{HostOffline, 503},
		{HostVersionUnsupported, 409},
		{AgentNotEnrolled, 409},
		{EnrollTokenInvalid, 403},
		{EnrollRateLimited, 429},
		{UpdateInProgress, 409},
		{UpdateUnconfirmedChanges, 409},
		{UpdateDowngradeRefused, 409},
		{UpdateNotFound, 404},
		{UpdateNothingToRollBack, 409},
		{RolloutInProgress, 409},
		{UpdateSignatureInvalid, 500},
		{UpdatePrevMismatch, 500},
		{UpdateInterrupted, 500},
		{Code("no.such_code"), 500},
```

and insert this test directly above `func TestEveryCodeHasATitle(t *testing.T) {`:

```go
// The values are shared contracts: agents, the CLI and the web UI match on
// them.
func TestFleetCodeValues(t *testing.T) {
	for code, want := range map[Code]string{
		HostOffline:              "host.offline",
		HostVersionUnsupported:   "host.version_unsupported",
		AgentNotEnrolled:         "agent.not_enrolled",
		EnrollTokenInvalid:       "enroll.token_invalid",
		EnrollRateLimited:        "enroll.rate_limited",
		UpdateInProgress:         "update.in_progress",
		UpdateUnconfirmedChanges: "update.unconfirmed_changes",
		UpdateDowngradeRefused:   "update.downgrade_refused",
		UpdateNotFound:           "update.not_found",
		UpdateNothingToRollBack:  "update.nothing_to_roll_back",
		RolloutInProgress:        "rollout.in_progress",
		UpdateSignatureInvalid:   "update.signature_invalid",
		UpdatePrevMismatch:       "update.prev_mismatch",
		UpdateInterrupted:        "update.interrupted",
	} {
		if string(code) != want || !Known(code) {
			t.Errorf("code %q: want value %q registered", code, want)
		}
	}
}

```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/core/errcode/`
Expected: FAIL to build with `undefined: HostOffline` and the other new names.

- [ ] **Step 3: Add the codes**

In `internal/core/errcode/errcode.go`, insert directly above `type info struct {` (two new const blocks, so gofmt leaves the alignment of the existing blocks alone):

```go
// Problem codes of the fleet, enrollment, update and rollout.
const (
	HostOffline              Code = "host.offline"
	HostVersionUnsupported   Code = "host.version_unsupported"
	AgentNotEnrolled         Code = "agent.not_enrolled"
	EnrollTokenInvalid       Code = "enroll.token_invalid" //nolint:gosec // G101: an error code, not a credential
	EnrollRateLimited        Code = "enroll.rate_limited"
	UpdateInProgress         Code = "update.in_progress"
	UpdateUnconfirmedChanges Code = "update.unconfirmed_changes"
	UpdateDowngradeRefused   Code = "update.downgrade_refused"
	UpdateNotFound           Code = "update.not_found"
	UpdateNothingToRollBack  Code = "update.nothing_to_roll_back"
	RolloutInProgress        Code = "rollout.in_progress"
)

// Action-level codes of an update: they appear in an update record's error.
const (
	UpdateSignatureInvalid Code = "update.signature_invalid"
	UpdatePrevMismatch     Code = "update.prev_mismatch"
	UpdateInterrupted      Code = "update.interrupted"
)

```

The `//nolint:gosec` is required: without it gosec reports G101 on the name `EnrollTokenInvalid`, as it does for `InvalidCredentials`.

In the `registry` map, replace

```go
	JournalOverflow:        {http.StatusInternalServerError, "Journal overflow"},
}
```

with (the blank line keeps gofmt from realigning the entries above it)

```go
	JournalOverflow:        {http.StatusInternalServerError, "Journal overflow"},

	// A host whose agent has no live session may answer later unchanged.
	HostOffline:              {http.StatusServiceUnavailable, "Host offline"},
	HostVersionUnsupported:   {http.StatusConflict, "Agent version unsupported"},
	AgentNotEnrolled:         {http.StatusConflict, "Agent not enrolled"},
	EnrollTokenInvalid:       {http.StatusForbidden, "Invalid enrollment token"},
	EnrollRateLimited:        {http.StatusTooManyRequests, "Too many enrollment attempts"},
	UpdateInProgress:         {http.StatusConflict, "Update in progress"},
	UpdateUnconfirmedChanges: {http.StatusConflict, "Unconfirmed changes"},
	UpdateDowngradeRefused:   {http.StatusConflict, "Downgrade refused"},
	UpdateNotFound:           {http.StatusNotFound, "Release not found"},
	UpdateNothingToRollBack:  {http.StatusConflict, "Nothing to roll back"},
	RolloutInProgress:        {http.StatusConflict, "Rollout in progress"},
	UpdateSignatureInvalid:   {http.StatusInternalServerError, "Release signature invalid"},
	UpdatePrevMismatch:       {http.StatusInternalServerError, "Previous binary does not match"},
	UpdateInterrupted:        {http.StatusInternalServerError, "Update interrupted"},
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/core/errcode/ ./internal/controller/api/`
Expected: two `ok` lines.

- [ ] **Step 5: Lint**

Run: `gofmt -l internal/core/errcode/ && golangci-lint run --allow-serial-runners ./internal/core/errcode/ && GOTOOLCHAIN=local go mod tidy -diff`
Expected: gofmt prints nothing, golangci-lint prints `0 issues.`, tidy prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/core/errcode/errcode.go internal/core/errcode/errcode_test.go
git commit -m "feat(errcode): add fleet update and rollout codes"
```

---

### Task 3: Agent, update and binary paths

**Files:**
- Modify: `internal/paths/paths.go`
- Test: `internal/paths/paths_test.go`

**Interfaces:**
- Consumes: none
- Produces: C3 — the five `Paths` fields, their production and `Under` values, the development agent socket, the eight path methods and `MkdirAgent`. `MkdirAll` is unchanged: the controller calls it (internal/controller/server/server.go:177) and from M2 runs unprivileged, so it must not create the agent's root-owned directories.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

In `internal/paths/paths_test.go`, `TestResolve`:

In the case `"production ignores the environment"`, replace

```go
			ControllerSocket: "/run/darkpanel-controller/controller.sock",
		}},
```

with

```go
			ControllerSocket: "/run/darkpanel-controller/controller.sock",
			AgentSocket:      "/run/darkpanel-agent/agent.sock",
			AgentTLSDir:      "/etc/darkpanel/agent",
			UpdateDir:        "/var/lib/darkpanel/update",
			BinaryDir:        "/usr/local/lib/darkpanel",
			BinaryLink:       "/usr/local/bin/darkpanel",
		}},
```

In the case `"dev with both variables"`, replace

```go
			ControllerSocket: "/r/darkpanel-dev/controller.sock",
		}},
```

with

```go
			ControllerSocket: "/r/darkpanel-dev/controller.sock",
			AgentSocket:      "/r/darkpanel-dev/agent.sock",
			AgentTLSDir:      "/s/darkpanel-dev/keys/agent",
			UpdateDir:        "/s/darkpanel-dev/update",
			BinaryDir:        "/s/darkpanel-dev/bin",
			BinaryLink:       "/s/darkpanel-dev/link/darkpanel",
		}},
```

In both cases `"dev defaults"` and `"dev ignores relative values"`, replace

```go
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
		}},
```

with

```go
			ControllerSocket: "/home/ada/.local/state/darkpanel-dev/run/controller.sock",
			AgentSocket:      "/home/ada/.local/state/darkpanel-dev/run/agent.sock",
			AgentTLSDir:      "/home/ada/.local/state/darkpanel-dev/keys/agent",
			UpdateDir:        "/home/ada/.local/state/darkpanel-dev/update",
			BinaryDir:        "/home/ada/.local/state/darkpanel-dev/bin",
			BinaryLink:       "/home/ada/.local/state/darkpanel-dev/link/darkpanel",
		}},
```

Replace the whole `TestDerivedPaths` function with:

```go
func TestDerivedPaths(t *testing.T) {
	p := Under("/x")
	for got, want := range map[string]string{
		p.ControllerDB():       "/x/controller/controller.db",
		p.AgentDB():            "/x/agent/agent.db",
		p.ControllerBackups():  "/x/controller/backups",
		p.AgentBackups():       "/x/agent/backups",
		p.Binary():             "/x/bin/darkpanel",
		p.PrevBinary():         "/x/bin/darkpanel.prev",
		p.RecoveryBinary():     "/x/bin/darkpanel.recovery",
		p.FailedBinary():       "/x/bin/darkpanel.failed",
		p.NewBinary("01TX"):    "/x/bin/darkpanel.new-01TX",
		p.UpdateLock():         "/x/update/lock",
		p.UpdateRecord("01TX"): "/x/update/01TX.json",
		p.AgentTLSCurrent():    "/x/keys/agent/current",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}
}
```

Leave `TestMkdirAll` as it is, and append to the end of the file:

```go
func TestMkdirAgent(t *testing.T) {
	p := Under(filepath.Join(t.TempDir(), "dp"))
	if err := p.MkdirAgent(); err != nil {
		t.Fatalf("MkdirAgent: %v", err)
	}
	for _, d := range []string{p.AgentTLSDir, p.UpdateDir, filepath.Dir(p.AgentSocket)} {
		info, err := os.Stat(d)
		if err != nil {
			t.Fatal(err)
		}
		if !info.IsDir() || info.Mode().Perm() != 0o700 {
			t.Errorf("%s: dir %v mode %o, want a 0700 directory", d, info.IsDir(), info.Mode().Perm())
		}
	}
	if err := p.MkdirAgent(); err != nil {
		t.Errorf("second MkdirAgent: %v", err)
	}
	// The controller's directories are MkdirAll's, and only the installer
	// creates the binary directory and the link's.
	for _, d := range []string{p.ControllerDir, p.BinaryDir, filepath.Dir(p.BinaryLink)} {
		if _, err := os.Stat(d); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("%s: stat error %v, want it absent", d, err)
		}
	}
}
```

(`errors`, `os` and `path/filepath` are already imported.)

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./internal/paths/`
Expected: FAIL to build with `unknown field AgentSocket in struct literal of type Paths`.

- [ ] **Step 3: Implement**

In `internal/paths/paths.go`:

Replace

```go
	ControllerSocket string
}
```

with

```go
	ControllerSocket string
	AgentSocket      string // the agent's break-glass socket
	AgentTLSDir      string // the agent's credential generations
	UpdateDir        string // update transaction records and the update lock
	BinaryDir        string // the installed binary and its update siblings
	BinaryLink       string // the symlink on PATH to the installed binary
}
```

Directly below the `AgentBackups` method, add:

```go

// Binary is the installed binary that the units run.
func (p Paths) Binary() string { return filepath.Join(p.BinaryDir, "darkpanel") }

// PrevBinary is the binary an update replaced, kept for rollback.
func (p Paths) PrevBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.prev") }

// RecoveryBinary is the hard link of PrevBinary that the recovery units run
// while an update is unfinished.
func (p Paths) RecoveryBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.recovery") }

// FailedBinary is where a rollback moves the binary that failed.
func (p Paths) FailedBinary() string { return filepath.Join(p.BinaryDir, "darkpanel.failed") }

// NewBinary is where update transaction tx stages its binary, on the same
// filesystem as Binary so that the swap is a rename.
func (p Paths) NewBinary(tx string) string { return filepath.Join(p.BinaryDir, "darkpanel.new-"+tx) }

// UpdateLock is the file every update and rollback step locks.
func (p Paths) UpdateLock() string { return filepath.Join(p.UpdateDir, "lock") }

// UpdateRecord is the record of update transaction tx.
func (p Paths) UpdateRecord(tx string) string { return filepath.Join(p.UpdateDir, tx+".json") }

// AgentTLSCurrent is the symlink to the agent's current credential generation.
func (p Paths) AgentTLSCurrent() string { return filepath.Join(p.AgentTLSDir, "current") }
```

In `Production`, replace

```go
		ControllerSocket: "/run/darkpanel-controller/controller.sock",
	}
}
```

with

```go
		ControllerSocket: "/run/darkpanel-controller/controller.sock",
		AgentSocket:      "/run/darkpanel-agent/agent.sock",
		AgentTLSDir:      "/etc/darkpanel/agent",
		UpdateDir:        "/var/lib/darkpanel/update",
		BinaryDir:        "/usr/local/lib/darkpanel",
		BinaryLink:       "/usr/local/bin/darkpanel",
	}
}
```

Replace the doc comment of `Under`

```go
// Under is the development layout rooted at root, with the socket at
// root/run/controller.sock.
```

with

```go
// Under is the development layout rooted at root, with the sockets in
// root/run.
```

and in its body replace

```go
		ControllerSocket: filepath.Join(root, "run", "controller.sock"),
	}
}
```

with

```go
		ControllerSocket: filepath.Join(root, "run", "controller.sock"),
		AgentSocket:      filepath.Join(root, "run", "agent.sock"),
		AgentTLSDir:      filepath.Join(root, "keys", "agent"),
		UpdateDir:        filepath.Join(root, "update"),
		BinaryDir:        filepath.Join(root, "bin"),
		BinaryLink:       filepath.Join(root, "link", "darkpanel"),
	}
}
```

In the doc comment of `Resolve`, replace `$XDG_STATE_HOME/darkpanel-dev with the socket in` with `$XDG_STATE_HOME/darkpanel-dev with the sockets in`. In `resolve`, replace

```go
		p.ControllerSocket = filepath.Join(runtime, "darkpanel-dev", "controller.sock")
```

with

```go
		p.ControllerSocket = filepath.Join(runtime, "darkpanel-dev", "controller.sock")
		p.AgentSocket = filepath.Join(runtime, "darkpanel-dev", "agent.sock")
```

Leave `MkdirAll` as it is. At the end of the file, add:

```go
// MkdirAgent creates the agent's directories of p that are missing, with mode
// 0700: the credential generations, the update records and the socket's
// directory. Existing directories keep their mode. MkdirAll leaves them out
// because the controller runs it as its own unprivileged user.
func (p Paths) MkdirAgent() error {
	for _, d := range []string{p.AgentTLSDir, p.UpdateDir, filepath.Dir(p.AgentSocket)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return fmt.Errorf("paths: create %s: %w", d, err)
		}
	}
	return nil
}
```

(`fmt` and `os` are already imported.)

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/paths/ && GOTOOLCHAIN=local go build ./...`
Expected: `ok  	github.com/darkraise/darkpanel/internal/paths`, and the build prints nothing.

- [ ] **Step 5: Lint**

Run: `gofmt -l internal/paths/ && golangci-lint run --allow-serial-runners ./internal/paths/ && GOTOOLCHAIN=local go mod tidy -diff`
Expected: gofmt prints nothing, `0 issues.`, tidy prints nothing.

- [ ] **Step 6: Commit**

```bash
git add internal/paths/paths.go internal/paths/paths_test.go
git commit -m "feat(paths): add agent, update and binary paths"
```

---

### Task 4: Core contract additions

**Files:**
- Modify: `internal/core/contract.go`

**Interfaces:**
- Consumes: none
- Produces: C4 — `Env.ChangeID`, the four `AgentHello` fields, `SchemaHash`, `Uninstaller` (and the removal of `Manifest.Uninstall`), the five owned-path kind constants.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 0 - spec 0 - coupling 3 - risk 1 = 4

The changes are declarations only; later tasks test their behaviour (Task 7 `Env.ChangeID`, Task 6 the kinds, Task 14 the Hello fields). This task's check is that the whole tree still builds and every existing test passes, which also proves nothing used `Manifest.Uninstall`.

- [ ] **Step 1: Add `Env.ChangeID`**

In `internal/core/contract.go`, replace

```go
// Env is what a handler may use. HostID is empty for controller-side actions;
// Agents is nil on the agent side.
type Env struct {
	HostID    string
	Principal Principal
```

with

```go
// Env is what a handler may use. HostID is empty for controller-side actions;
// Agents is nil on the agent side. ChangeID is the change whose Apply or
// Verify is running and is empty in every other call, because a plan has no
// change yet.
type Env struct {
	HostID    string
	ChangeID  string
	Principal Principal
```

- [ ] **Step 2: Add the owned-path kinds**

Replace

```go
// OwnedPath is a file or marked-block file a module may write (ADR 0009).
type OwnedPath struct {
	Path          string
	Kind          string // "file" or "block"
```

with

```go
// The kinds of an owned path. Only files and blocks are written through
// FileWriter; directories, users and units are the installer's, recorded so
// that uninstall removes them.
const (
	OwnedFile  = "file"
	OwnedBlock = "block"
	OwnedDir   = "dir"
	OwnedUser  = "user"
	OwnedUnit  = "unit"
)

// OwnedPath is something a module or the core creates on the host and
// uninstall removes (ADR 0009). For OwnedUser, Path is the user name.
type OwnedPath struct {
	Path          string
	Kind          string // one of the Owned kinds
```

- [ ] **Step 3: Replace `Manifest.Uninstall` with `Uninstaller`**

In `type Manifest struct`, delete the line

```go
	Uninstall      func(ctx context.Context, env Env) error
```

Then replace

```go
// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
}
```

with

```go
// Module is a compiled-in module.
type Module interface {
	Manifest() Manifest
}

// Uninstaller is the optional agent-side uninstall step of a module, found
// with a type assertion on the Module. It runs before uninstall removes the
// module's marked blocks and files; with keepData it leaves the module's data
// in place for a reinstall.
type Uninstaller interface {
	Uninstall(ctx context.Context, env Env, keepData bool) error
}
```

- [ ] **Step 4: Add the Hello fields and `SchemaHash`**

Replace

```go
// AgentHello is what an agent reports about itself (ADR 0003).
type AgentHello struct {
	ProtocolVersion int
	BinaryVersion   string
	OS              string
	HostID          string
	Modules         []ModuleStatus
}
```

with

```go
// AgentHello is what an agent reports about itself (ADR 0003).
type AgentHello struct {
	ProtocolVersion int
	BinaryVersion   string
	OS              string
	Arch            string
	HostID          string
	Modules         []ModuleStatus
	// JournalEpoch is a ULID that changes only when the agent's journal is
	// recreated, so the controller can tell a reset sequence from an old one.
	JournalEpoch string
	// ControllerConnected is nil when the agent is not enrolled.
	ControllerConnected *bool
	// SchemaHashes are keyed "<action name>@<version>" for every agent-side
	// action version the agent serves.
	SchemaHashes map[string]SchemaHash
}

// SchemaHash identifies the input and output schemas of one action version:
// each is the lower-case hex SHA-256 of the schema re-encoded by
// encoding/json (ADR 0012).
type SchemaHash struct {
	Input, Output string
}
```

- [ ] **Step 5: Verify**

Run:

```bash
gofmt -l internal/core/
GOTOOLCHAIN=local go build ./...
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go test -count=1 ./internal/...
golangci-lint run --allow-serial-runners ./internal/core/
GOTOOLCHAIN=local go mod tidy -diff
```

Expected: gofmt, build, vet and tidy print nothing; every test package prints `ok` or `no test files`; golangci-lint prints `0 issues.` A build error naming `Uninstall` means some code set the removed field: stop and report it.

- [ ] **Step 6: Commit**

```bash
git add internal/core/contract.go
git commit -m "feat(core): add hello fields, uninstaller and kinds"
```

---

### Task 5: Fleet and update action types

**Files:**
- Create: `internal/core/fleet.go`
- Test: `internal/core/fleet_test.go`

**Interfaces:**
- Consumes: none
- Produces: C5 (every constant and type named there, in `internal/core/fleet.go`). `coreactions.ModuleState`, `coreactions.Host` and `coreactions.HostList` stay as they are; the host-actions part switches `core.host.list` to the `core` types.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/core/fleet_test.go` (package `core_test`, so it can import `internal/core/schema`, which imports `core`):

```go
package core_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

var fleetInputs = []any{
	core.HostCreateInput{}, core.HostTokenInput{}, core.HostIDInput{},
	core.UpdateApplyInput{}, core.UpdateRollbackInput{}, core.UpdateStatusInput{}, core.UpdatePolicy{},
	core.RolloutInput{}, core.RolloutCancelInput{}, core.RolloutsInput{},
}

var fleetOutputs = []any{
	core.HostList{}, core.Host{}, core.EnrollmentGrant{},
	core.UpdateTransaction{}, core.UpdateStatus{}, core.UpdatePolicy{},
	core.Rollout{}, core.RolloutList{},
}

func TestFleetTypesHaveSchemas(t *testing.T) {
	for _, v := range fleetInputs {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("schema.Input(%T): %v", v, err)
		}
	}
	for _, v := range fleetOutputs {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("schema.Output(%T): %v", v, err)
		}
	}
}

var camelCase = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// checkJSONNames fails for every exported field reachable from typ whose
// JSON name is missing or not camelCase.
func checkJSONNames(t *testing.T, typ reflect.Type, seen map[reflect.Type]bool) {
	t.Helper()
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
		typ = typ.Elem()
	}
	if typ.Kind() != reflect.Struct || typ == reflect.TypeFor[time.Time]() || seen[typ] {
		return
	}
	seen[typ] = true
	for i := range typ.NumField() {
		f := typ.Field(i)
		name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
		if !camelCase.MatchString(name) {
			t.Errorf("%s.%s: JSON name %q is not camelCase", typ.Name(), f.Name, name)
		}
		checkJSONNames(t, f.Type, seen)
	}
}

func TestFleetJSONNamesAreCamelCase(t *testing.T) {
	seen := map[reflect.Type]bool{}
	for _, v := range append(append([]any{}, fleetInputs...), fleetOutputs...) {
		checkJSONNames(t, reflect.TypeOf(v), seen)
	}
}

func TestEnrollmentTokenIsSecret(t *testing.T) {
	f, ok := reflect.TypeFor[core.EnrollmentGrant]().FieldByName("Token")
	if !ok || f.Tag.Get("secret") != "true" {
		t.Fatal(`EnrollmentGrant.Token is not tagged secret:"true"`)
	}
	out, err := schema.Output(reflect.TypeFor[core.EnrollmentGrant]())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(out, []byte(`"x-secret":true`)) {
		t.Errorf("EnrollmentGrant schema has no x-secret property: %s", out)
	}
}

func TestFleetInputPatterns(t *testing.T) {
	v := schema.NewValidator()
	tests := []struct {
		in    any
		raw   string
		valid bool
	}{
		{core.HostCreateInput{}, `{"name":"web-1"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"panel.example.com:7443"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"[fd7a:115c::1]:7443"}`, true},
		{core.HostCreateInput{}, `{"name":"web-1","controllerAddress":"panel.example.com"}`, false},
		{core.HostCreateInput{}, `{"name":"Web"}`, false},
		{core.HostCreateInput{}, `{"name":"-web"}`, false},
		{core.HostCreateInput{}, `{"name":"` + strings.Repeat("a", 63) + `"}`, true},
		{core.HostCreateInput{}, `{"name":"` + strings.Repeat("a", 64) + `"}`, false},
		{core.HostIDInput{}, `{"hostId":""}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3.0","channel":"stable","force":false}`, true},
		{core.UpdateApplyInput{}, `{"version":"1.3.0-beta.1","channel":"beta","force":true}`, true},
		{core.UpdateApplyInput{}, `{"version":"01.3.0","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"v1.3.0","channel":"stable","force":false}`, false},
		{core.UpdateApplyInput{}, `{"version":"1.3.0","channel":"nightly","force":false}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"beta","schedule":null}`, true},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["mon","fri"],"time":"03:30"}}`, true},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":[],"time":"03:30"}}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["monday"],"time":"03:30"}}`, false},
		{core.UpdatePolicy{}, `{"hostId":"h1","channel":"stable","schedule":{"days":["mon"],"time":"24:00"}}`, false},
		{core.RolloutInput{}, `{"hosts":[],"version":"1.0.0","channel":"stable"}`, false},
		{core.RolloutsInput{}, `{}`, true},
		{core.RolloutsInput{}, `{"limit":0}`, false},
		{core.RolloutsInput{}, `{"limit":101}`, false},
	}
	for _, tt := range tests {
		// The validator caches a compiled schema per action, so each type
		// gets its own name.
		typ := reflect.TypeOf(tt.in)
		def := core.ActionDef{Spec: core.ActionSpec{Name: "core.fleet." + strings.ToLower(typ.Name()), Version: 1}, InputType: typ}
		err := v.ValidateInput(def, json.RawMessage(tt.raw))
		var coded *errcode.Error
		switch {
		case tt.valid && err != nil:
			t.Errorf("%T %s: %v, want valid", tt.in, tt.raw, err)
		case !tt.valid && (!errors.As(err, &coded) || coded.Code != errcode.InvalidInput):
			t.Errorf("%T %s: %v, want %s", tt.in, tt.raw, err, errcode.InvalidInput)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/core/`
Expected: FAIL to compile with `undefined: core.HostCreateInput` (and the other new names).

- [ ] **Step 3: Write the types**

Create `internal/core/fleet.go`. A struct tag value is a Go quoted string, so the pattern backslashes and the commas inside `{0,62}` and `{1,5}` are written doubled; `invopop/jsonschema` turns `\\,` back into a comma.

```go
package core

import "time"

// Host states.
const (
	HostPending  = "pending"
	HostEnrolled = "enrolled"
	HostRevoked  = "revoked"
)

// Release channels.
const (
	ChannelStable = "stable"
	ChannelBeta   = "beta"
)

// System and local actors added by ADR 0012.
const (
	ActorRollout        = "system:rollout"
	ActorUpdateSchedule = "system:update-schedule"
	ActorUnenrolled     = "local:root@unenrolled"
)

// Update transaction kinds.
const (
	UpdateKindUpdate   = "update"
	UpdateKindRollback = "rollback"
)

// Update transaction states.
const (
	UpdatePending     = "pending"
	UpdateDownloading = "downloading"
	UpdateStaged      = "staged"
	UpdateSwapped     = "swapped"
	UpdateRollingBack = "rolling-back"
	UpdateCommitted   = "committed"
	UpdateRolledBack  = "rolled-back"
	UpdateFailed      = "failed"
)

// Rollout states.
const (
	RolloutRunning   = "running"
	RolloutDraining  = "draining"
	RolloutSucceeded = "succeeded"
	RolloutFailed    = "failed"
	RolloutCancelled = "cancelled"
)

// Rollout host states.
const (
	RolloutHostWaiting   = "waiting"
	RolloutHostUpdating  = "updating"
	RolloutHostCommitted = "committed"
	RolloutHostFailed    = "failed"
	RolloutHostSkipped   = "skipped"
)

// ModuleState is one module's enablement on one host. A module can be used
// only when it is both enabled and available.
type ModuleState struct {
	ID        string     `json:"id" jsonschema_description:"Module id."`
	Version   int        `json:"version" jsonschema_description:"Module version compiled into this binary."`
	Enabled   bool       `json:"enabled" jsonschema_description:"Whether an operator enabled the module on the host."`
	Available bool       `json:"available" jsonschema_description:"Whether the module's migrations and preflight checks passed on the host."`
	Reason    *string    `json:"reason" jsonschema:"nullable" jsonschema_description:"Why the module is unavailable; null when it is available."`
	CheckedAt *time.Time `json:"checkedAt" jsonschema:"nullable" jsonschema_description:"When availability was last checked; null when it never was."`
}

// Host is a managed host. Online comes from the controller's live sessions
// and is never stored; the version and platform fields are null until the
// host's first Hello.
type Host struct {
	ID              string        `json:"id" jsonschema_description:"Host id."`
	Name            string        `json:"name" jsonschema_description:"Host name."`
	IsLocal         bool          `json:"isLocal" jsonschema_description:"Whether the host runs this controller."`
	State           string        `json:"state" jsonschema:"enum=pending,enum=enrolled,enum=revoked" jsonschema_description:"Enrollment state: pending until the agent enrolls, revoked after core.host.revoke."`
	Online          bool          `json:"online" jsonschema_description:"Whether the host's agent has a live session with the controller."`
	LastSeenAt      *time.Time    `json:"lastSeenAt" jsonschema:"nullable" jsonschema_description:"When the agent last answered; null when it never has."`
	AgentVersion    *string       `json:"agentVersion" jsonschema:"nullable" jsonschema_description:"Version of the agent's binary; null until its first Hello."`
	ProtocolVersion *int          `json:"protocolVersion" jsonschema:"nullable" jsonschema_description:"Agent protocol version; null until its first Hello."`
	OS              *string       `json:"os" jsonschema:"nullable" jsonschema_description:"Operating system the agent reports; null until its first Hello."`
	Arch            *string       `json:"arch" jsonschema:"nullable" jsonschema_description:"CPU architecture the agent reports; null until its first Hello."`
	Supported       bool          `json:"supported" jsonschema_description:"Whether the agent's version is inside the controller's support window; an unsupported agent accepts only update actions."`
	Modules         []ModuleState `json:"modules" jsonschema_description:"Every compiled-in module's state on the host."`
}

// HostList is the output of core.host.list/v1.
type HostList struct {
	Hosts []Host `json:"hosts" jsonschema_description:"Every host, the local host first, then by name."`
}

// HostCreateInput is the input of core.host.create/v1.
type HostCreateInput struct {
	Name              string `json:"name" jsonschema:"pattern=^[a-z0-9][a-z0-9.-]{0\\,62}$" jsonschema_description:"Host name: lower-case letters, digits, dots and hyphens, starting with a letter or digit."`
	ControllerAddress string `json:"controllerAddress,omitempty" jsonschema:"pattern=^(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+):[0-9]{1\\,5}$" jsonschema_description:"host:port the agent dials; the controller's --agent-advertise address when omitted."`
}

// HostTokenInput is the input of core.host.token/v1.
type HostTokenInput struct {
	HostID            string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Id of the pending or revoked host to enroll."`
	ControllerAddress string `json:"controllerAddress,omitempty" jsonschema:"pattern=^(\\[[0-9A-Fa-f:.]+\\]|[A-Za-z0-9.-]+):[0-9]{1\\,5}$" jsonschema_description:"host:port the agent dials; the controller's --agent-advertise address when omitted."`
}

// HostIDInput is the input of core.host.revoke/v1 and core.host.delete/v1.
type HostIDInput struct {
	HostID string `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Host id."`
}

// EnrollmentGrant is a host with its one-time enrollment token, shown once.
type EnrollmentGrant struct {
	Host      Host      `json:"host" jsonschema_description:"The host the token enrolls."`
	Token     string    `json:"token" secret:"true" jsonschema_description:"Single-use enrollment token for darkpanel agent enroll; shown only once."`
	ExpiresAt time.Time `json:"expiresAt" jsonschema_description:"When the token stops working, one hour after it was made."`
}

// UpdateApplyInput is the input of core.update.apply/v1.
type UpdateApplyInput struct {
	Version string `json:"version" jsonschema:"pattern=^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$" jsonschema_description:"Semantic version to install, such as 1.3.0 or 1.3.0-beta.1."`
	Channel string `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version must be signed for."`
	Force   bool   `json:"force" jsonschema_description:"Allow a downgrade of at most one minor version."`
}

// UpdateRollbackInput is the input of core.update.rollback/v1.
type UpdateRollbackInput struct {
	Force bool `json:"force" jsonschema_description:"Roll back although an enrolled agent runs a newer version than the previous binary."`
}

// UpdateStatusInput is the input of core.update.status/v1; at most one field
// is set.
type UpdateStatusInput struct {
	OperationID string `json:"operationId,omitempty" jsonschema:"minLength=1" jsonschema_description:"Only the records of this update operation."`
	ChangeID    string `json:"changeId,omitempty" jsonschema:"minLength=1" jsonschema_description:"Only the records of the operation this change started."`
}

// ErrorDetail is the error of a failed update record, rollout or rollout
// host.
type ErrorDetail struct {
	Code    string `json:"code" jsonschema_description:"Stable error code, such as update.signature_invalid."`
	Message string `json:"message" jsonschema_description:"Human-readable explanation."`
}

// UpdateTransaction is one step of an update or rollback operation.
type UpdateTransaction struct {
	ID            string       `json:"id" jsonschema_description:"Transaction id."`
	OperationID   string       `json:"operationId" jsonschema_description:"Id of the operation: the id of its first transaction."`
	ParentID      *string      `json:"parentId" jsonschema:"nullable" jsonschema_description:"The previous step of a stepped update; null for the first."`
	Kind          string       `json:"kind" jsonschema:"enum=update,enum=rollback" jsonschema_description:"update or rollback."`
	FromVersion   string       `json:"fromVersion" jsonschema_description:"Version running before this step."`
	ToVersion     string       `json:"toVersion" jsonschema_description:"Version this step installs."`
	TargetVersion string       `json:"targetVersion" jsonschema_description:"Version the whole operation ends on."`
	State         string       `json:"state" jsonschema:"enum=pending,enum=downloading,enum=staged,enum=swapped,enum=rolling-back,enum=committed,enum=rolled-back,enum=failed" jsonschema_description:"Transaction state."`
	Error         *ErrorDetail `json:"error" jsonschema:"nullable" jsonschema_description:"Why the step failed or rolled back; null otherwise."`
	StartedAt     time.Time    `json:"startedAt" jsonschema_description:"When the step started."`
	UpdatedAt     time.Time    `json:"updatedAt" jsonschema_description:"When the step last changed state."`
}

// UpdateStatus is the output of core.update.status/v1.
type UpdateStatus struct {
	Transactions []UpdateTransaction `json:"transactions" jsonschema_description:"The matching records, newest first."`
}

// UpdateSchedule is a weekly two-hour update window in UTC.
type UpdateSchedule struct {
	Days []string `json:"days" jsonschema:"minItems=1,uniqueItems=true,enum=mon,enum=tue,enum=wed,enum=thu,enum=fri,enum=sat,enum=sun" jsonschema_description:"Days the window opens."`
	Time string   `json:"time" jsonschema:"pattern=^([01][0-9]|2[0-3]):[0-5][0-9]$" jsonschema_description:"UTC time the window opens, HH:MM."`
}

// UpdatePolicy is a host's update channel and schedule: the input and output
// of core.host.update-policy/v1.
type UpdatePolicy struct {
	HostID   string          `json:"hostId" jsonschema:"minLength=1" jsonschema_description:"Host id."`
	Channel  string          `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Release channel the host follows."`
	Schedule *UpdateSchedule `json:"schedule" jsonschema:"nullable" jsonschema_description:"Weekly update window; null for on-demand updates only."`
}

// RolloutInput is the input of core.update.rollout/v1.
type RolloutInput struct {
	Hosts   []string `json:"hosts" jsonschema:"minItems=1,uniqueItems=true" jsonschema_description:"Ids of the hosts to update, in order; the controller's own host always goes first."`
	Version string   `json:"version" jsonschema:"pattern=^(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)\\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\\.[0-9A-Za-z-]+)*)?$" jsonschema_description:"Semantic version to roll out."`
	Channel string   `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version must be signed for."`
}

// RolloutCancelInput is the input of core.update.rollout-cancel/v1.
type RolloutCancelInput struct {
	RolloutID string `json:"rolloutId" jsonschema:"minLength=1" jsonschema_description:"Rollout id."`
	Force     bool   `json:"force" jsonschema_description:"Mark the rollout cancelled at once, without waiting for a host's update to finish."`
}

// RolloutsInput is the input of core.update.rollouts/v1.
type RolloutsInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"minimum=1,maximum=100" jsonschema_description:"Most rollouts to return, 1 to 100; 20 when omitted."`
}

// RolloutHost is one host of a rollout.
type RolloutHost struct {
	HostID      string       `json:"hostId" jsonschema_description:"Host id."`
	Position    int          `json:"position" jsonschema_description:"Order in which the host is updated, from 0."`
	State       string       `json:"state" jsonschema:"enum=waiting,enum=updating,enum=committed,enum=failed,enum=skipped" jsonschema_description:"Host state within the rollout."`
	PlanID      *string      `json:"planId" jsonschema:"nullable" jsonschema_description:"Plan of the host's core.update.apply; null before it is planned."`
	OperationID *string      `json:"operationId" jsonschema:"nullable" jsonschema_description:"The host's update operation; null before it starts."`
	Error       *ErrorDetail `json:"error" jsonschema:"nullable" jsonschema_description:"Why the host failed; null otherwise."`
}

// Rollout is an update of selected hosts, one host at a time.
type Rollout struct {
	ID        string        `json:"id" jsonschema_description:"Rollout id."`
	Version   string        `json:"version" jsonschema_description:"Version being rolled out."`
	Channel   string        `json:"channel" jsonschema:"enum=stable,enum=beta" jsonschema_description:"Channel the version is signed for."`
	State     string        `json:"state" jsonschema:"enum=running,enum=draining,enum=succeeded,enum=failed,enum=cancelled" jsonschema_description:"Rollout state; draining means dispatch stopped while a host's update is still running."`
	Error     *ErrorDetail  `json:"error" jsonschema:"nullable" jsonschema_description:"Why the rollout failed; null otherwise."`
	CreatedAt time.Time     `json:"createdAt" jsonschema_description:"When the rollout was created."`
	UpdatedAt time.Time     `json:"updatedAt" jsonschema_description:"When the rollout last changed."`
	Hosts     []RolloutHost `json:"hosts" jsonschema_description:"The rollout's hosts in order."`
}

// RolloutList is the output of core.update.rollouts/v1.
type RolloutList struct {
	Rollouts []Rollout `json:"rollouts" jsonschema_description:"Rollouts, newest first."`
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 -run 'Fleet|Enrollment' -v ./internal/core/`
Expected: PASS for `TestFleetTypesHaveSchemas`, `TestFleetJSONNamesAreCamelCase`, `TestEnrollmentTokenIsSecret` and `TestFleetInputPatterns`. The refusal rows prove the escaped patterns and the item enum of `days` reached the schema.

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/core/... && GOTOOLCHAIN=local go vet ./internal/core/... && golangci-lint run --allow-serial-runners ./internal/core/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: every package `ok`, no vet output, `0 issues.`, no tidy output.

- [ ] **Step 5: Commit**

```bash
git add internal/core/fleet.go internal/core/fleet_test.go
git commit -m "feat(core): add fleet and update action types"
```

---

### Task 6: Duplicate ownership rule and the reserved name system

**Files:**
- Modify: `internal/core/registry.go` (`ReservedNames`; `NewRegistry`; new `checkOwnership` above `checkAction`)
- Test: `internal/core/registry_test.go`
- Test: `internal/modules/registry_test.go:27-30` (`reservedCLI`)

**Interfaces:**
- Consumes: C4 (`OwnedBlock`, `OwnedUser`, `OwnedFile`, `OwnedDir`, `OwnedUnit`)
- Produces: C6

**Items:** 20

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

- [ ] **Step 1: Write the failing tests**

In `internal/core/registry_test.go`, in the `tests` table of `TestNewRegistryRules`, add a row directly below the `"reserved module id"` row:

```go
		{"reserved module id system", []Module{testModule{ID: "system", Version: 1}}, "reserved"},
```

Append to the end of `internal/core/registry_test.go` (its imports already include `strings` and `testing`):

```go
func TestNewRegistryOwnership(t *testing.T) {
	file := func(p string) OwnedPath { return OwnedPath{Path: p, Kind: OwnedFile} }
	block := func(p string, ids ...string) OwnedPath { return OwnedPath{Path: p, Kind: OwnedBlock, BlockIDs: ids} }
	owner := func(id string, paths ...OwnedPath) Module {
		return testModule{ID: id, Version: 1, OwnedPaths: paths}
	}
	tests := []struct {
		name string
		mods []Module
		want string // "" when the registry is valid
	}{
		{"distinct paths", []Module{owner("core", file("/etc/a")), owner("demo", file("/etc/b"))}, ""},
		{"blocks with distinct ids", []Module{owner("core", block("/etc/ufw/after.rules", "core")),
			owner("demo", block("/etc/ufw/after.rules", "demo"))}, ""},
		{"one module declaring a path twice", []Module{owner("demo", file("/etc/a"), file("/etc/a"))}, ""},
		{"same file", []Module{owner("core", file("/etc/a")), owner("demo", file("/etc/a"))},
			"owned path /etc/a is declared by modules core and demo"},
		{"file and block", []Module{owner("core", file("/etc/a")), owner("demo", block("/etc/a", "x"))},
			"owned path /etc/a is declared by modules core and demo"},
		{"directory and unit", []Module{owner("core", OwnedPath{Path: "/etc/x", Kind: OwnedDir}),
			owner("demo", OwnedPath{Path: "/etc/x", Kind: OwnedUnit})}, "owned path /etc/x is declared by modules core and demo"},
		{"same block", []Module{owner("alpha", block("/etc/a", "x", "y")), owner("demo", block("/etc/a", "y"))},
			"marked block y of /etc/a is declared by modules alpha and demo"},
		{"same user", []Module{owner("core", OwnedPath{Path: "darkpanel", Kind: OwnedUser}),
			owner("demo", OwnedPath{Path: "darkpanel", Kind: OwnedUser})}, "user darkpanel is declared by modules core and demo"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRegistry(tt.mods...)
			switch {
			case tt.want == "" && err != nil:
				t.Fatalf("NewRegistry: %v", err)
			case tt.want != "" && (err == nil || !strings.Contains(err.Error(), tt.want)):
				t.Fatalf("NewRegistry error = %v, want one containing %q", err, tt.want)
			}
		})
	}
}
```

`internal/modules/registry_test.go` keeps its own copy of the reserved names, so that a rule weakened in `internal/core` fails there. Change the last line of `reservedCLI` (lines 27-30) from

```go
		"host", "module", "token", "audit", "help", "completion",
	}
```

to

```go
		"host", "module", "token", "audit", "help", "completion", "system",
	}
```

That copy checks the real registry's module ids, so it passes before and after Step 3; it is updated here so the two lists stay equal.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -run NewRegistry ./internal/core/`
Expected: FAIL: `TestNewRegistryRules/reserved_module_id_system` and the five refusal rows of `TestNewRegistryOwnership` (`same_file`, `file_and_block`, `directory_and_unit`, `same_block`, `same_user`).

- [ ] **Step 3: Implement the rule**

In `internal/core/registry.go`, change the last line of `ReservedNames` from

```go
	"host", "module", "token", "audit", "help", "completion",
}
```

to

```go
	"host", "module", "token", "audit", "help", "completion", "system",
}
```

In `NewRegistry`, below the line `errs = append(errs, r.checkCLINames(ids)...)`, add:

```go
	errs = append(errs, checkOwnership(r.modules)...)
```

Insert directly above `func checkAction(moduleID string, a ActionDef) error {` (the file already imports `fmt` and `slices`):

```go
// checkOwnership refuses two modules, the core included, that declare the
// same owned path, marked block or user: the file writer and uninstall could
// not tell which module a path or block belongs to. Blocks of one file with
// distinct ids are how modules share a file, so they are allowed.
func checkOwnership(mods []Manifest) []error {
	type declaration struct {
		module string
		path   OwnedPath
	}
	var decls []declaration
	for _, m := range mods {
		for _, op := range m.OwnedPaths {
			decls = append(decls, declaration{m.ID, op})
		}
	}
	var errs []error
	for i, a := range decls {
		for _, b := range decls[i+1:] {
			if a.module == b.module || a.path.Path != b.path.Path {
				continue
			}
			aUser, bUser := a.path.Kind == OwnedUser, b.path.Kind == OwnedUser
			switch {
			case aUser != bUser:
				// A user name and a file path never name the same thing.
			case aUser:
				errs = append(errs, fmt.Errorf("user %s is declared by modules %s and %s", a.path.Path, a.module, b.module))
			case a.path.Kind == OwnedBlock && b.path.Kind == OwnedBlock:
				for _, id := range a.path.BlockIDs {
					if slices.Contains(b.path.BlockIDs, id) {
						errs = append(errs, fmt.Errorf("marked block %s of %s is declared by modules %s and %s",
							id, a.path.Path, a.module, b.module))
					}
				}
			default:
				errs = append(errs, fmt.Errorf("owned path %s is declared by modules %s and %s", a.path.Path, a.module, b.module))
			}
		}
	}
	return errs
}
```

`r.modules` is in registration order and `registry.BuildWith` registers the core first, so the core is always named first in a clash with it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/core/ ./internal/registry/ ./internal/controller/coreactions/ ./internal/modules/`
Expected: four `ok` lines (the production registry declares no clashing path, and no module id is `system`).

Run: `GOTOOLCHAIN=local go vet ./internal/core/... && golangci-lint run --allow-serial-runners ./internal/core/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no vet output, `0 issues.`, no tidy output.

- [ ] **Step 5: Commit**

```bash
git add internal/core/registry.go internal/core/registry_test.go internal/modules/registry_test.go
git commit -m "feat(core): refuse duplicate owned paths"
```

---

### Task 7: Change id in the lifecycle engine

**Files:**
- Modify: `internal/core/lifecycle/apply.go`, `internal/core/lifecycle/recover.go`
- Modify (test fixture): `internal/core/lifecycle/fixture_test.go`
- Test: `internal/core/lifecycle/changeid_test.go`

**Interfaces:**
- Consumes: C4 `Env.ChangeID`
- Produces: C7 — handlers see the change id in Apply and Verify, including recovery's Verify, and an empty id everywhere else.

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Make the fixture handler record the change id**

In `internal/core/lifecycle/fixture_test.go`, replace

```go
	// onVerify, when set, runs at the start of Verify without mu held.
	onVerify func(ctx context.Context)
}
```

with

```go
	// onVerify, when set, runs at the start of Verify without mu held.
	onVerify func(ctx context.Context)
	// changeIDs records "<call>=<env.ChangeID>" for every Plan, Apply and
	// Verify call, in call order.
	changeIDs []string
}

func (h *fixHost) sawChangeID(call string, env core.Env) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.changeIDs = append(h.changeIDs, call+"="+env.ChangeID)
}
```

Replace

```go
func (x itemHandler) Plan(_ context.Context, _ core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.panicIf("Plan", in.Name)
```

with

```go
func (x itemHandler) Plan(_ context.Context, env core.Env, in itemIn) (core.PlanOutcome, error) {
	x.h.sawChangeID("Plan", env)
	x.h.panicIf("Plan", in.Name)
```

Replace

```go
func (x itemHandler) Apply(_ context.Context, _ core.Env, in itemIn) (itemOut, error) {
	// CrashApply
```

with

```go
func (x itemHandler) Apply(_ context.Context, env core.Env, in itemIn) (itemOut, error) {
	x.h.sawChangeID("Apply", env)
	// CrashApply
```

Replace

```go
func (x itemHandler) Verify(ctx context.Context, _ core.Env, in itemIn, _ itemOut) error {
	if x.h.onVerify != nil {
		x.h.onVerify(ctx)
	}
```

with

```go
func (x itemHandler) Verify(ctx context.Context, env core.Env, in itemIn, _ itemOut) error {
	if x.h.onVerify != nil {
		x.h.onVerify(ctx)
	}
	x.h.sawChangeID("Verify", env)
```

- [ ] **Step 2: Write the failing tests**

Create `internal/core/lifecycle/changeid_test.go`:

```go
package lifecycle

import (
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestApplyAndVerifySeeTheChangeID(t *testing.T) {
	f := newFixture(t)
	pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
	v := f.apply(alice, pl.PlanID, core.ApplyOptions{})
	// Plan runs once for the plan and once more as apply's recheck, both
	// before the change exists.
	want := []string{"Plan=", "Plan=", "Apply=" + v.ID, "Verify=" + v.ID}
	if !reflect.DeepEqual(f.host.changeIDs, want) {
		t.Errorf("change ids seen = %v, want %v", f.host.changeIDs, want)
	}
}

func TestRecoveryVerifySeesTheChangeID(t *testing.T) {
	f := newFixture(t)
	_, id := f.crash(refAdd, `{"name":"a","value":"1","crashApply":true}`, core.ApplyOptions{})
	f.restart()
	f.host.changeIDs = nil
	f.recoverAll()
	if want := []string{"Verify=" + id}; !reflect.DeepEqual(f.host.changeIDs, want) {
		t.Errorf("change ids seen during recovery = %v, want %v", f.host.changeIDs, want)
	}
}
```

- [ ] **Step 3: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test -count=1 -run 'SeeTheChangeID|SeesTheChangeID' ./internal/core/lifecycle/`
Expected: FAIL: `change ids seen = [Plan= Plan= Apply= Verify=], want [Plan= Plan= Apply=<id> Verify=<id>]` and `change ids seen during recovery = [Verify=], want [Verify=<id>]`.

- [ ] **Step 4: Set the change id**

In `internal/core/lifecycle/apply.go`, replace

```go
func (e *Engine) run(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	env := e.env(p)
```

with

```go
func (e *Engine) run(ctx context.Context, p core.Principal, it *intent) (core.ChangeView, error) {
	env := e.env(p)
	env.ChangeID = it.id
```

In `internal/core/lifecycle/recover.go`, in `verifyStored`, replace

```go
	if err := e.verify(ctx, def, e.env(recoveryPrincipal), input, json.RawMessage("null")); err != nil {
```

with

```go
	env := e.env(recoveryPrincipal)
	env.ChangeID = changeID
	if err := e.verify(ctx, def, env, input, json.RawMessage("null")); err != nil {
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -race -count=1 ./internal/core/lifecycle/`
Expected: `ok  	github.com/darkraise/darkpanel/internal/core/lifecycle`

- [ ] **Step 6: Lint**

Run: `gofmt -l internal/core/lifecycle/ && golangci-lint run --allow-serial-runners ./internal/core/lifecycle/ && GOTOOLCHAIN=local go mod tidy -diff`
Expected: gofmt prints nothing, `0 issues.`, tidy prints nothing.

- [ ] **Step 7: Commit**

```bash
git add internal/core/lifecycle/apply.go internal/core/lifecycle/recover.go internal/core/lifecycle/fixture_test.go internal/core/lifecycle/changeid_test.go
git commit -m "feat(lifecycle): pass the change id to apply and verify"
```

---

### Task 8: Operation reservation and the all-locks key

**Files:**
- Create: `internal/core/lifecycle/reserve.go`
- Modify: `internal/core/lifecycle/apply.go` (`locksFree`, `takeLocks`)
- Test: `internal/core/lifecycle/reserve_test.go`

**Interfaces:**
- Consumes: C2 `errcode.UpdateInProgress`, `errcode.UpdateUnconfirmedChanges`; the Task 7 fixture (`fixHost.onSnapshot` already exists)
- Produces: C8 — `ReserveAll`, `ReservationOwner`, `Reserve`, `Release`, and the `*` rule in the engine's lock check and lock taking.

**Depends on:** Task 2, Task 7

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

Why the rule sits in two places: `locksFree` runs before the plan recheck and the snapshot, without a transaction; `takeLocks` runs inside the write transaction that persists the change. A reservation committed between the two must still stop the change, so `takeLocks` checks again under the database writer, which also serialises it with `Reserve`.

- [ ] **Step 1: Write the failing tests**

Create `internal/core/lifecycle/reserve_test.go`:

```go
package lifecycle

import (
	"context"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	opA = ReservationOwner("01OPA")
	opB = ReservationOwner("01OPB")
)

func (f *fixture) reserve(owner string) error {
	f.t.Helper()
	return Reserve(context.Background(), f.db, ReserveAll, owner)
}

func TestReservationOwner(t *testing.T) {
	if got := ReservationOwner("01OP"); got != "op:01OP" {
		t.Errorf("ReservationOwner = %q, want op:01OP", got)
	}
}

func TestReserveTakesTheKey(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatalf("Reserve: %v", err)
	}
	if err := f.reserve(opA); err != nil {
		t.Fatalf("second Reserve by the same owner: %v", err)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE key = ? AND owner = ?", ReserveAll, opA); n != 1 {
		t.Errorf("reservation rows = %d, want 1", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks"); n != 1 {
		t.Errorf("lock rows = %d, want 1", n)
	}
}

func TestReserveRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(f *fixture)
		want  errcode.Code
	}{
		{"another operation holds every lock", func(f *fixture) {
			if err := f.reserve(opB); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateInProgress},
		{"a change holds a lock", func(f *fixture) {
			if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('fix', '01CHANGE')"); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateUnconfirmedChanges},
		{"a revertible change is pending", func(f *fixture) {
			f.pending(`{"name":"a","value":"1"}`)
		}, errcode.UpdateUnconfirmedChanges},
		{"a pending revertible change holds no lock", func(f *fixture) {
			v := f.pending(`{"name":"a","value":"1"}`)
			if _, err := f.db.Writer().Exec("DELETE FROM core_locks WHERE owner = ?", v.ID); err != nil {
				f.t.Fatal(err)
			}
		}, errcode.UpdateUnconfirmedChanges},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newFixture(t)
			c.setup(f)
			before := f.count("SELECT count(*) FROM core_locks")
			wantCode(t, f.reserve(opA), c.want)
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
				t.Errorf("refused Reserve left %d rows of its owner", n)
			}
			if n := f.count("SELECT count(*) FROM core_locks"); n != before {
				t.Errorf("lock rows = %d, want %d", n, before)
			}
		})
	}
}

func TestReserveAfterTheChangeFinishes(t *testing.T) {
	f := newFixture(t)
	f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{})
	if err := f.reserve(opA); err != nil {
		t.Fatalf("Reserve after a completed change: %v", err)
	}
}

func TestReservationRefusesEveryLock(t *testing.T) {
	t.Run("planned before the reservation", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
	})
	t.Run("planned after the reservation", func(t *testing.T) {
		f := newFixture(t)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		pl := f.plan(alice, refAdd, `{"name":"a","value":"1"}`)
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
	})
	t.Run("reserved after the lock check passed", func(t *testing.T) {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
		// Snapshot runs after the lock check and before the locks are taken.
		f.host.onSnapshot = func() {
			if err := f.reserve(opA); err != nil {
				t.Error(err)
			}
		}
		_, err := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wantCode(t, err, errcode.LockHeld)
		if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
			t.Errorf("changes = %d, want none", n)
		}
	})
	t.Run("released", func(t *testing.T) {
		f := newFixture(t)
		if err := f.reserve(opA); err != nil {
			t.Fatal(err)
		}
		if err := Release(context.Background(), f.db, opA); err != nil {
			t.Fatal(err)
		}
		if v := f.apply(alice, f.plan(alice, refAdd, `{"name":"a","value":"1"}`).PlanID, core.ApplyOptions{}); v.State != core.StateCompleted {
			t.Errorf("state after the release = %s, want completed", v.State)
		}
	})
}

func TestReadsRunWhileReserved(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	if _, err := f.eng.Read(context.Background(), alice, refGet, []byte(`{"name":"a"}`)); err != nil {
		t.Errorf("Read while reserved: %v", err)
	}
}

func TestReleaseDeletesOnlyItsOwnersRows(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Writer().Exec("INSERT INTO core_locks (key, owner) VALUES ('other', ?)", opB); err != nil {
		t.Fatal(err)
	}
	if err := Release(context.Background(), f.db, opA); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
		t.Errorf("rows of the released owner = %d, want 0", n)
	}
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opB); n != 1 {
		t.Errorf("rows of the other owner = %d, want 1", n)
	}
}

func TestRecoverKeepsAReservation(t *testing.T) {
	f := newFixture(t)
	if err := f.reserve(opA); err != nil {
		t.Fatal(err)
	}
	f.restart()
	f.recoverAll()
	if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 1 {
		t.Errorf("reservation rows after recovery = %d, want 1", n)
	}
}

// Reserve and an apply race for the lock table. Reserve starts from the
// snapshot hook, after the apply's lock check and just before it takes its
// locks, so either may reach the database writer first; the writer serialises
// them, exactly one wins, and the loser leaves nothing behind.
func TestReserveRacesAnApply(t *testing.T) {
	for range 50 {
		f := newFixture(t)
		pl := f.plan(alice, refSet, `{"name":"a","value":"1"}`)
		var wg sync.WaitGroup
		var reserveErr error
		f.host.onSnapshot = func() {
			wg.Add(1)
			go func() {
				defer wg.Done()
				reserveErr = Reserve(context.Background(), f.db, ReserveAll, opA)
			}()
		}
		_, applyErr := f.eng.Apply(context.Background(), alice, pl.PlanID, core.ApplyOptions{})
		wg.Wait()
		switch {
		case reserveErr == nil && applyErr == nil:
			t.Fatal("both the reservation and the apply succeeded")
		case reserveErr == nil:
			wantCode(t, applyErr, errcode.LockHeld)
			if n := f.count("SELECT count(*) FROM core_changes"); n != 0 {
				t.Fatalf("changes = %d after a refused apply, want 0", n)
			}
		case applyErr == nil:
			wantCode(t, reserveErr, errcode.UpdateUnconfirmedChanges)
			if n := f.count("SELECT count(*) FROM core_locks WHERE owner = ?", opA); n != 0 {
				t.Fatalf("reservation rows = %d after a refused Reserve, want 0", n)
			}
		default:
			t.Fatalf("both failed: reserve %v, apply %v", reserveErr, applyErr)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./internal/core/lifecycle/`
Expected: FAIL to build with `undefined: ReservationOwner`, `undefined: Reserve`, `undefined: ReserveAll`.

- [ ] **Step 3: Write the reservation**

Create `internal/core/lifecycle/reserve.go`:

```go
package lifecycle

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
)

// ReserveAll is the lock key of an operation that excludes every change:
// while a row with this key exists, no change of another owner can take any
// lock (ADR 0012).
const ReserveAll = "*"

// ReservationOwner is the lock owner of an update operation. No change id
// starts with "op:", so a sweep of reservations never touches a change's lock.
func ReservationOwner(operationID string) string { return "op:" + operationID }

// rowQuerier is a *sql.DB or a *sql.Tx.
type rowQuerier interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Reserve gives key to owner in a write transaction of its own, so it must
// not run inside another Write. It succeeds without change when owner already
// holds key and nothing else is locked. It fails with update.in_progress
// while another owner holds ReserveAll, and with update.unconfirmed_changes
// while any other lock is held or a revertible change is applying or pending.
func Reserve(ctx context.Context, db *store.DB, key, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		var holder string
		err := tx.QueryRowContext(ctx,
			"SELECT owner FROM core_locks WHERE key = ? AND owner <> ?", ReserveAll, owner).Scan(&holder)
		switch {
		case err == nil:
			return errcode.New(errcode.UpdateInProgress, fmt.Sprintf("update operation %s is in progress", holder))
		case !errors.Is(err, sql.ErrNoRows):
			return err
		}
		var others, mine int
		if err := tx.QueryRowContext(ctx,
			`SELECT count(*) FILTER (WHERE NOT (key = ? AND owner = ?)), count(*) FILTER (WHERE key = ? AND owner = ?)
			 FROM core_locks`, key, owner, key, owner).Scan(&others, &mine); err != nil {
			return err
		}
		switch {
		case others > 0:
			return errcode.New(errcode.UpdateUnconfirmedChanges, "a change holds a lock; wait for it to finish")
		case mine > 0:
			return nil
		}
		var unconfirmed int
		if err := tx.QueryRowContext(ctx,
			"SELECT count(*) FROM core_changes WHERE class = ? AND state IN (?, ?)",
			string(core.ClassRevertible), core.StateApplying, core.StatePending).Scan(&unconfirmed); err != nil {
			return err
		}
		if unconfirmed > 0 {
			return errcode.New(errcode.UpdateUnconfirmedChanges,
				"a revertible change is not confirmed yet; confirm or revert it first")
		}
		_, err = tx.ExecContext(ctx, "INSERT INTO core_locks (key, owner) VALUES (?, ?)", key, owner)
		return err
	})
	var coded *errcode.Error
	switch {
	case errors.As(err, &coded):
		return err
	case err != nil:
		return fmt.Errorf("lifecycle: reserve %s for %s: %w", key, owner, err)
	}
	return nil
}

// Release deletes every lock row of owner.
func Release(ctx context.Context, db *store.DB, owner string) error {
	err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, "DELETE FROM core_locks WHERE owner = ?", owner)
		return err
	})
	if err != nil {
		return fmt.Errorf("lifecycle: release the locks of %s: %w", owner, err)
	}
	return nil
}

// notReserved fails with lock.held, naming key, when an owner other than
// owner holds ReserveAll.
func notReserved(ctx context.Context, q rowQuerier, owner, key string) error {
	var holder string
	err := q.QueryRowContext(ctx,
		"SELECT owner FROM core_locks WHERE key = ? AND owner <> ?", ReserveAll, owner).Scan(&holder)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil
	case err != nil:
		return fmt.Errorf("lifecycle: read lock %s: %w", ReserveAll, err)
	}
	return errcode.New(errcode.LockHeld,
		fmt.Sprintf("lock %s is unavailable while update operation %s holds every lock", key, holder))
}
```

`count(*) FILTER (WHERE …)` needs SQLite 3.30 or later; the pure-Go driver `modernc.org/sqlite` v1.60.1 bundles a newer one.

- [ ] **Step 4: Refuse locks while an operation holds every lock**

In `internal/core/lifecycle/apply.go`, replace

```go
// locksFree fails with lock.held when a change holds one of keys.
func locksFree(ctx context.Context, db *sql.DB, keys []string) error {
	for _, key := range keys {
```

with

```go
// locksFree fails with lock.held when a change holds one of keys, or an
// update operation holds every lock.
func locksFree(ctx context.Context, db *sql.DB, keys []string) error {
	if len(keys) > 0 {
		if err := notReserved(ctx, db, "", keys[0]); err != nil {
			return err
		}
	}
	for _, key := range keys {
```

and replace

```go
// takeLocks gives every key to owner, or fails with lock.held when another
// owner has one. A key listed twice is taken once.
func takeLocks(ctx context.Context, tx *sql.Tx, owner string, keys []string) error {
	for _, key := range keys {
```

with

```go
// takeLocks gives every key to owner, or fails with lock.held when another
// owner has one or an update operation holds every lock. A key listed twice
// is taken once.
func takeLocks(ctx context.Context, tx *sql.Tx, owner string, keys []string) error {
	if len(keys) > 0 {
		if err := notReserved(ctx, tx, owner, keys[0]); err != nil {
			return err
		}
	}
	for _, key := range keys {
```

- [ ] **Step 5: Run the tests to verify they pass**

Run:

```bash
GOTOOLCHAIN=local go test -race -count=1 ./internal/core/lifecycle/
GOTOOLCHAIN=local go test -race -count=5 -run 'Reserve|Reservation|Release' ./internal/core/lifecycle/
```

Expected: `ok` both times. `TestReserveRacesAnApply` sees both outcomes over its 50 rounds (in the dry run, 80 apply wins and 70 reservation wins over three runs); it asserts the invariant either way.

- [ ] **Step 6: Lint and the whole suite**

Run:

```bash
gofmt -l internal/core/lifecycle/
golangci-lint run --allow-serial-runners ./internal/core/lifecycle/
GOTOOLCHAIN=local go vet ./...
GOTOOLCHAIN=local go test -count=1 ./...
GOTOOLCHAIN=local go mod tidy -diff
```

Expected: gofmt, vet and tidy print nothing, `0 issues.`, every package `ok` or `no test files`.

- [ ] **Step 7: Commit**

```bash
git add internal/core/lifecycle/reserve.go internal/core/lifecycle/reserve_test.go internal/core/lifecycle/apply.go
git commit -m "feat(lifecycle): add the operation reservation"
```

---

### Task 9: Agent client source

**Files:**
- Create: `internal/controller/dispatch/clients.go`
- Test: `internal/controller/dispatch/clients_test.go`

**Interfaces:**
- Consumes: C2 (`errcode.HostOffline`)
- Produces: C9 (`ClientSource`, `StaticClients`, `NewStaticClients`, `Add`, `Client`). Task 12 makes `Hosts` read from a `ClientSource`; this task leaves `Hosts` unchanged.

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/controller/dispatch/clients_test.go` (`wantCode` is the helper in `fixture_test.go` of the same package):

```go
package dispatch

import (
	"context"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

func TestStaticClientsUnknownHostIsOffline(t *testing.T) {
	_, _, err := NewStaticClients().Client(context.Background(), "h9")
	e := wantCode(t, err, errcode.HostOffline)
	if e.Detail != "host h9 is offline" {
		t.Errorf("detail = %q", e.Detail)
	}
}

func TestStaticClientsGenerations(t *testing.T) {
	ctx := context.Background()
	s := NewStaticClients()
	a, b := &coretest.FakeAgent{}, &coretest.FakeAgent{}
	s.Add("h1", a)
	c, gen1, err := s.Client(ctx, "h1")
	if err != nil || c != a || gen1 == 0 {
		t.Fatalf("Client(h1) = %v, %d, %v; want a with a positive generation", c, gen1, err)
	}
	if _, again, _ := s.Client(ctx, "h1"); again != gen1 {
		t.Errorf("generation changed without Add: %d, then %d", gen1, again)
	}
	s.Add("h1", b)
	c, gen2, err := s.Client(ctx, "h1")
	if err != nil || c != b || gen2 == gen1 {
		t.Errorf("after Add: %v, %d, %v; want b with a new generation (old %d)", c, gen2, err, gen1)
	}
	s.Add("h2", a)
	if _, gen3, _ := s.Client(ctx, "h2"); gen3 == gen1 || gen3 == gen2 {
		t.Errorf("h2 generation %d repeats %d or %d", gen3, gen1, gen2)
	}
	// Generations never repeat across sources either.
	other := NewStaticClients()
	other.Add("h1", a)
	if _, gen4, _ := other.Client(ctx, "h1"); gen4 == gen1 || gen4 == gen2 {
		t.Errorf("a second source repeated generation %d", gen4)
	}
}

func TestStaticClientsConcurrentUse(t *testing.T) {
	ctx := context.Background()
	s := NewStaticClients()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				s.Add("h1", &coretest.FakeAgent{})
				if _, _, err := s.Client(ctx, "h1"); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	wg.Wait()
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/controller/dispatch/`
Expected: FAIL to compile with `undefined: NewStaticClients`.

- [ ] **Step 3: Write the source**

Create `internal/controller/dispatch/clients.go`:

```go
package dispatch

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ClientSource yields the current agent client of a host.
type ClientSource interface {
	// Client returns the host's client and a generation that changes whenever
	// the host's session is replaced; Hosts re-runs Hello when it changes.
	// A host with no live session answers errcode.HostOffline.
	Client(ctx context.Context, hostID string) (core.AgentClient, uint64, error)
}

// generations numbers every client a StaticClients is given. One counter for
// the process keeps a generation from ever repeating, so a Hosts never
// mistakes a new client for one it already greeted.
var generations atomic.Uint64

// StaticClients is a ClientSource over clients set by Add: the in-process
// agent of --embedded-agent, and tests. Its methods are safe for concurrent
// use.
type StaticClients struct {
	mu      sync.Mutex
	clients map[string]staticClient
}

type staticClient struct {
	client     core.AgentClient
	generation uint64
}

var _ ClientSource = (*StaticClients)(nil)

// NewStaticClients returns a source with no clients.
func NewStaticClients() *StaticClients {
	return &StaticClients{clients: map[string]staticClient{}}
}

// Add sets hostID's client. Every call starts a new generation, so Hosts says
// Hello again, as it does after an agent reconnects.
func (s *StaticClients) Add(hostID string, c core.AgentClient) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.clients[hostID] = staticClient{client: c, generation: generations.Add(1)}
}

// Client returns hostID's client and its generation; a host without a client
// is offline.
func (s *StaticClients) Client(_ context.Context, hostID string) (core.AgentClient, uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sc, ok := s.clients[hostID]
	if !ok {
		return nil, 0, errcode.New(errcode.HostOffline, fmt.Sprintf("host %s is offline", hostID))
	}
	return sc.client, sc.generation, nil
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -race -count=1 -run StaticClients -v ./internal/controller/dispatch/`
Expected: PASS for `TestStaticClientsUnknownHostIsOffline`, `TestStaticClientsGenerations` and `TestStaticClientsConcurrentUse`, with no race report.

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/controller/dispatch/ && GOTOOLCHAIN=local go vet ./internal/controller/dispatch/ && golangci-lint run --allow-serial-runners ./internal/controller/dispatch/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `ok`, no vet output, `0 issues.`, no tidy output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/clients.go internal/controller/dispatch/clients_test.go
git commit -m "feat(dispatch): add agent client source"
```

---

### Task 10: Core module slots for extra actions and owned paths

**Files:**
- Modify: `internal/controller/coreactions/module.go` (`Deps`, `module.Manifest`)
- Test: `internal/controller/coreactions/slots_test.go`

**Interfaces:**
- Consumes: none
- Produces: C10 (`coreactions.Deps.Extra`, `coreactions.Deps.OwnedPaths`). The assembly part fills both; with both empty the core manifest is unchanged.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `internal/controller/coreactions/slots_test.go`:

```go
package coreactions_test

import (
	"context"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
)

type things struct{}

func (things) Read(context.Context, core.Env, coreactions.NoInput) (coreactions.Empty, error) {
	return coreactions.Empty{}, nil
}

func TestModuleCarriesExtraActionsAndOwnedPaths(t *testing.T) {
	extra := core.DefineRead[coreactions.NoInput, coreactions.Empty](core.ActionSpec{
		Name: "core.thing.list", Version: 1, Side: core.SideAgent, Summary: "List things.",
	}, things{})
	owned := []core.OwnedPath{{Path: "/etc/systemd/system/darkpanel-agent.service", Kind: "file"}}
	mod := coreactions.Module(coreactions.Deps{Extra: []core.ActionDef{extra}, OwnedPaths: owned})
	m := mod.Manifest()
	if last := m.Actions[len(m.Actions)-1]; last.Spec.Name != "core.thing.list" {
		t.Errorf("last core action = %s, want the extra core.thing.list", last.Spec.Name)
	}
	if !reflect.DeepEqual(m.OwnedPaths, owned) {
		t.Errorf("OwnedPaths = %+v, want %+v", m.OwnedPaths, owned)
	}
	reg, err := core.NewRegistry(mod)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}
	if _, ok := reg.Action(core.ActionRef{Name: "core.thing.list", Version: 1}); !ok {
		t.Error("the extra action is not in the registry")
	}
	if len(coreactions.Module(coreactions.Deps{}).Manifest().Actions) != len(m.Actions)-1 {
		t.Error("an empty Extra changed the built-in actions")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/controller/coreactions/`
Expected: FAIL to compile with `unknown field Extra in struct literal of type coreactions.Deps` and `unknown field OwnedPaths in struct literal of type coreactions.Deps`.

- [ ] **Step 3: Add the slots**

In `internal/controller/coreactions/module.go`, replace the end of `Deps`

```go
	// actions manage, in registration order; the server passes modules.All().
	Modules []core.Module
}
```

with

```go
	// actions manage, in registration order; the server passes modules.All().
	Modules []core.Module
	// Extra are core actions defined outside this package, such as the update
	// and rollout actions; they join the one core manifest after the built-in
	// ones.
	Extra []core.ActionDef
	// OwnedPaths are the files, directories, units and user the core itself
	// installs, declared like a module's so that the registry's ownership
	// rule and uninstall cover them.
	OwnedPaths []core.OwnedPath
}
```

and in `Manifest`, replace

```go
		actions = append(actions, group...)
	}
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, DefaultEnabled: true}
```

with

```go
		actions = append(actions, group...)
	}
	actions = append(actions, m.d.Extra...)
	return core.Manifest{ID: core.ModuleIDCore, Version: 1, Actions: actions, OwnedPaths: m.d.OwnedPaths, DefaultEnabled: true}
```

The package comment ("Every action is controller-side") stays true while `Extra` is empty; the part that first fills `Extra` with agent-side actions updates it.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 ./internal/controller/coreactions/ ./internal/registry/ ./internal/modules/`
Expected: three `ok` lines; `TestModuleCarriesExtraActionsAndOwnedPaths` passes and the schema snapshot test is unchanged.

Run: `GOTOOLCHAIN=local go vet ./internal/controller/coreactions/ && golangci-lint run --allow-serial-runners ./internal/controller/coreactions/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no vet output, `0 issues.`, no tidy output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/coreactions/module.go internal/controller/coreactions/slots_test.go
git commit -m "feat(coreactions): add extra action and owned path slots"
```

---

### Task 11: CLI group adoption and local fallbacks

**Files:**
- Modify: `internal/cli/root.go` (`Options`, a new `LocalFallback` type, `NewRoot`)
- Modify: `internal/cli/names.go` (`entries`, `checkEntries`, new `adopted` and `checkHooks`)
- Modify: `internal/cli/names_test.go` (four call sites gain a `nil` argument)
- Test: `internal/cli/hooks_test.go` (new)

**Interfaces:**
- Consumes: none
- Produces: C11 — `Options.Adopt`, `Options.LocalFallbacks`, `type LocalFallback`. The assembly part passes a hand-written `update` group with `Adopt: {"update": {"core.update.apply", "core.update.rollback", "core.update.status"}}` and reads `Options.LocalFallbacks` when `--local` finds no agent socket.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

The generated command of an action `core.update.apply` is `darkpanel update apply` (`CommandPath` drops the core segment), and `update` is a reserved hand-written top-level name, so today `checkEntries` refuses it. Adoption lets a hand-written group in `Options.Commands` take named generated commands: `group()` in root.go already reuses an existing top-level command of the same name, so only the clash check and the hook validation change.

- [ ] **Step 1: Write the failing test**

Create `internal/cli/hooks_test.go` (`fixtureHandler` and `demoNoInput` are the existing helpers of `internal/cli/fixture_test.go`):

```go
package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)

// updateCore is a core module whose agent-side actions share their first
// command word with a hand-written update group.
type updateCore struct{}

func (updateCore) Manifest() core.Manifest {
	m := core.Manifest{ID: core.ModuleIDCore, Version: 1}
	for _, name := range []string{"core.update.apply", "core.update.rollback", "core.update.status", "core.host.list"} {
		side := core.SideAgent
		if name == "core.host.list" {
			side = core.SideController
		}
		m.Actions = append(m.Actions, core.DefineRead(core.ActionSpec{Name: name, Version: 1, Side: side},
			fixtureHandler[demoNoInput, demoNoInput]{}))
	}
	return m
}

func updateRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(updateCore{})
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

// updateGroup is a hand-written update command with a hand-written run
// subcommand, as the assembly passes it.
func updateGroup() (group, run *cobra.Command) {
	group = &cobra.Command{Use: "update", Short: "Update commands"}
	run = &cobra.Command{Use: "run", RunE: func(*cobra.Command, []string) error { return nil }}
	group.AddCommand(run)
	return group, run
}

var updateActions = []string{"core.update.apply", "core.update.rollback", "core.update.status"}

func noFallback(context.Context, json.RawMessage) (json.RawMessage, error) { return nil, nil }

func TestAdoptPutsGeneratedCommandsUnderAHandWrittenGroup(t *testing.T) {
	group, run := updateGroup()
	root, err := NewRoot(Options{
		Registry: updateRegistry(t), Commands: []*cobra.Command{group},
		Adopt:          map[string][]string{"update": updateActions},
		LocalFallbacks: map[string]LocalFallback{"core.update.rollback": noFallback},
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range updateActions {
		cmd, ok := FindCommand(root, name)
		if !ok || cmd.Parent() != group {
			t.Errorf("FindCommand(%q) = %v, %v; want a subcommand of the hand-written update group", name, cmd, ok)
		}
	}
	if c, _, err := root.Find([]string{"update", "run"}); err != nil || c != run {
		t.Errorf("update run: %v, %v; want the hand-written run command", c, err)
	}
	if _, ok := FindCommand(root, "core.host.list"); !ok {
		t.Error("core.host.list is missing")
	}
}

func TestAdoptRefusals(t *testing.T) {
	tests := []struct {
		name      string
		adopt     map[string][]string
		fallbacks map[string]LocalFallback
		clash     bool // the group gets a hand-written apply subcommand
		want      string
	}{
		{name: "no adoption keeps the clash", want: `clashes with the hand-written command "update"`},
		{name: "unknown group", adopt: map[string][]string{"upgrade": updateActions},
			want: `adopting group "upgrade" is not a hand-written command`},
		{name: "unregistered action", adopt: map[string][]string{"update": append([]string{"core.update.freeze"}, updateActions...)},
			want: `update adopts core.update.freeze, which is not a registered action`},
		{name: "action outside the group", adopt: map[string][]string{"update": append([]string{"core.host.list"}, updateActions...)},
			want: `update adopts core.host.list, whose command "darkpanel host list" is not under it`},
		{name: "adopted name taken by a hand-written subcommand", adopt: map[string][]string{"update": updateActions}, clash: true,
			want: `core.update.apply would replace the hand-written command "darkpanel update apply"`},
		{name: "fallback for an unregistered action", adopt: map[string][]string{"update": updateActions},
			fallbacks: map[string]LocalFallback{"core.update.freeze": noFallback},
			want:      `local fallback core.update.freeze is not a registered agent-side action`},
		{name: "fallback for a controller-side action", adopt: map[string][]string{"update": updateActions},
			fallbacks: map[string]LocalFallback{"core.host.list": noFallback},
			want:      `local fallback core.host.list is not a registered agent-side action`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			group, _ := updateGroup()
			if tt.clash {
				group.AddCommand(&cobra.Command{Use: "apply"})
			}
			_, err := NewRoot(Options{
				Registry: updateRegistry(t), Commands: []*cobra.Command{group},
				Adopt: tt.adopt, LocalFallbacks: tt.fallbacks,
			})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Errorf("error %v, want one containing %q", err, tt.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./internal/cli/ -run TestAdopt`
Expected: FAIL to build: `unknown field Adopt in struct literal of type Options` and `undefined: LocalFallback`.

- [ ] **Step 3: Add the options and the hook check**

In `internal/cli/root.go`, add `"context"` and `"encoding/json"` to the standard-library import group (keep it sorted: `bufio`, `context`, `encoding/json`, `errors`, ...). Replace the `Options` fields from `Commands` through `UserHomeDir`:

```go
	Commands    []*cobra.Command
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	UserHomeDir func() (string, error)
```

with:

```go
	Commands []*cobra.Command
	// Adopt maps a hand-written top-level command in Commands to the action
	// names whose generated commands become its subcommands, so a reserved
	// name such as update can hold both hand-written and generated commands.
	Adopt map[string][]string
	// LocalFallbacks run an agent-side action in-process when --local finds
	// no agent socket, keyed by action name.
	LocalFallbacks map[string]LocalFallback
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Getenv         func(string) string
	UserHomeDir    func() (string, error)
```

Directly after the closing brace of `Options` (after the `ReadSecret func() (string, error)` field), add:

```go

// LocalFallback runs an agent-side action without the agent: it takes the
// action's input and returns its output, as the agent socket would.
type LocalFallback func(ctx context.Context, input json.RawMessage) (json.RawMessage, error)
```

In `NewRoot`, replace:

```go
	root.AddCommand(opts.Commands...)
	es, err := entries(opts.Registry, reserved)
```

with:

```go
	root.AddCommand(opts.Commands...)
	if err := checkHooks(opts); err != nil {
		return nil, fmt.Errorf("cli: %w", err)
	}
	es, err := entries(opts.Registry, reserved, adopted(opts.Adopt))
```

In `internal/cli/names.go`, replace the import block with:

```go
import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core"
)
```

Replace the doc comment and signature of `entries`:

```go
// entries returns the generated commands of reg sorted by path, or an error
// when two of them share a path, one's path is a group of another's, or one
// starts with a reserved top-level word.
func entries(reg *core.Registry, reserved []string) ([]entry, error) {
```

with:

```go
// entries returns the generated commands of reg sorted by path, or an error
// when two of them share a path, one's path is a group of another's, or one
// not in adopted starts with a reserved top-level word.
func entries(reg *core.Registry, reserved []string, adopted map[string]bool) ([]entry, error) {
```

In its body replace `if err := checkEntries(es, reserved); err != nil {` with `if err := checkEntries(es, reserved, adopted); err != nil {`. Replace:

```go
// checkEntries refuses entries whose commands would clash.
func checkEntries(es []entry, reserved []string) error {
```

with:

```go
// checkEntries refuses entries whose commands would clash; an adopted entry
// may start with a reserved word, because its hand-written group takes it.
func checkEntries(es []entry, reserved []string, adopted map[string]bool) error {
```

and in its body replace `if slices.Contains(reserved, e.Path[0]) {` with `if slices.Contains(reserved, e.Path[0]) && !adopted[e.Name] {`. Append to the end of `names.go`:

```go
// adopted is the set of every action name some hand-written group adopts.
func adopted(adopt map[string][]string) map[string]bool {
	out := map[string]bool{}
	for _, names := range adopt {
		for _, name := range names {
			out[name] = true
		}
	}
	return out
}

// checkHooks refuses an Adopt or LocalFallbacks entry that names no
// hand-written group, no registered action, an action outside its group, an
// adopted command a hand-written one already takes, or a fallback for an
// action the agent does not run.
func checkHooks(opts Options) error {
	sides := map[string]core.Side{}
	for _, a := range opts.Registry.Actions() {
		sides[a.Spec.Name] = a.Spec.Side
	}
	groups := map[string]*cobra.Command{}
	for _, c := range opts.Commands {
		groups[c.Name()] = c
	}
	var errs []error
	for _, name := range slices.Sorted(maps.Keys(opts.Adopt)) {
		g, ok := groups[name]
		if !ok {
			errs = append(errs, fmt.Errorf("adopting group %q is not a hand-written command", name))
			continue
		}
		taken := map[string]bool{}
		for _, c := range g.Commands() {
			taken[c.Name()] = true
		}
		for _, action := range opts.Adopt[name] {
			path := CommandPath(action)
			switch {
			case sides[action] == "":
				errs = append(errs, fmt.Errorf("%s adopts %s, which is not a registered action", name, action))
			case path[0] != name:
				errs = append(errs, fmt.Errorf("%s adopts %s, whose command \"darkpanel %s\" is not under it",
					name, action, strings.Join(path, " ")))
			case taken[path[1]]:
				errs = append(errs, fmt.Errorf("%s would replace the hand-written command \"darkpanel %s %s\"",
					action, name, path[1]))
			}
		}
	}
	for _, action := range slices.Sorted(maps.Keys(opts.LocalFallbacks)) {
		if sides[action] != core.SideAgent {
			errs = append(errs, fmt.Errorf("local fallback %s is not a registered agent-side action", action))
		}
	}
	return errors.Join(errs...)
}
```

In `internal/cli/names_test.go`, pass `nil` as the new last argument at the four call sites: `entries(testRegistry(t), handWritten, nil)`, `entries(reg, append(slices.Clone(handWritten), "agent"), nil)`, `checkEntries(same, handWritten, nil)` and `checkEntries(prefix, handWritten, nil)`.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -race -count=1 ./internal/cli/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/cli`).

- [ ] **Step 5: Lint**

Run: `gofmt -l internal/cli/ && GOTOOLCHAIN=local go vet ./internal/cli/ && golangci-lint run --allow-serial-runners ./internal/cli/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from `gofmt -l`, `0 issues.`, and no diff.

- [ ] **Step 6: Commit**

```bash
git add internal/cli/root.go internal/cli/names.go internal/cli/names_test.go internal/cli/hooks_test.go
git commit -m "feat(cli): add group adoption and local fallbacks"
```

---

### Task 12: Dispatch hosts over the client source

**Files:**
- Modify: `internal/controller/dispatch/hosts.go` (`HostsConfig`, `Hosts`, `NewHosts`, `Add` removed, `Agent`, `Hello`, `hello`)
- Modify: `internal/controller/dispatch/changes.go` (`find`, `ids` removed, new `isHostOffline`)
- Modify: `internal/controller/server/services.go:73-76`
- Test: `internal/controller/dispatch/fixture_test.go`, `apply_test.go`, `changes_test.go`, `dispatch_test.go`, `hosts_test.go`

**Interfaces:**
- Consumes: C9 (`ClientSource`, `StaticClients`), C2 (`errcode.HostOffline`)
- Produces: `dispatch.HostsConfig.Clients` (required); `Hosts.Add` no longer exists; `Hosts.Agent` and `Hosts.Hello` answer `not_found` for an id with no `core_hosts` row and pass on the source's `host.offline` for a known host without a client. The fleet part passes its session registry as `Clients`; `--embedded-agent` and tests pass a `StaticClients`.

**Depends on:** Task 9

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Behaviour changes, all deliberate:
- An id with no `core_hosts` row still answers `not_found` (`no host <id>`) from `Hosts.Agent` and `Hosts.Hello`, as in M1: `Hosts` reads `core_hosts` once before asking the `ClientSource`. A host that has a `core_hosts` row but no client now answers `host.offline` (503) where M1 answered `not_found`; the only existing expectation this changes is `TestChangesFailWhenAHostCannotBeListed` (its `h9` has a row and no client). Tests that give a client to a host id now insert that host's `core_hosts` row first. (Skipping offline hosts in `Changes` and audit ingestion is the host-actions part's.)
- `Hosts` says Hello again whenever the client's generation changes, not only once per process; `StaticClients.Add` starts a new generation, so a test that swaps a host's client still sees a fresh Hello, as with M1's `Add`.
- `find` (the lookup of a change missing from `core_change_index`) used to ask every host that had a client; a `ClientSource` cannot be enumerated, so it now asks every `core_hosts` row and skips a host that answers `host.offline`. With M1's single local host the set is the same.
- `NewHosts` panics when `Clients` is nil. It has one production caller, so a nil there is a wiring mistake that should stop startup at once, as `platform.NewRunner` does for a bad allowlist; returning an error would change every caller's signature for a case no input can reach.

- [ ] **Step 1: Convert the tests and add the new ones**

In `internal/controller/dispatch/fixture_test.go`, in `type harness struct`, replace

```go
	rt      *runtime.Runtime
	hosts   *Hosts
}
```

with

```go
	rt      *runtime.Runtime
	clients *StaticClients // the agent client of each host
	hosts   *Hosts
}
```

and at the end of `newHarness`, replace

```go
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clock: h.clock, Log: log})
	h.hosts.Add(testHost, h.rt)
```

with

```go
	h.clients = NewStaticClients()
	h.clients.Add(testHost, h.rt)
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: log})
```

Point every other test's client swap at the source (13 call sites, all of the form `h.hosts.Add(`: 4 in `apply_test.go`, 5 in `changes_test.go`, 3 in `hosts_test.go`, 1 in `dispatch_test.go`; `rg -c 'h\.hosts\.Add\(' internal/controller/dispatch/*_test.go` lists them, plus the one in `fixture_test.go` replaced above):

```bash
sed -i 's/h\.hosts\.Add(/h.clients.Add(/' internal/controller/dispatch/apply_test.go internal/controller/dispatch/changes_test.go internal/controller/dispatch/dispatch_test.go internal/controller/dispatch/hosts_test.go
```

In `internal/controller/dispatch/hosts_test.go`, `TestHelloSkipsModulesTheRegistryLacks` builds its own `Hosts`; replace

```go
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	h.clients.Add(testHost, &coretest.FakeAgent{
```

with

```go
	h.clients = NewStaticClients()
	h.hosts = NewHosts(HostsConfig{DB: h.db, Registry: h.reg, Clients: h.clients, Clock: h.clock, Log: slog.New(slog.DiscardHandler)})
	h.clients.Add(testHost, &coretest.FakeAgent{
```

One expectation changes: in `changes_test.go`, `TestChangesFailWhenAHostCannotBeListed` inserts `h9` into `core_hosts` without a client, so `_ = wantCode(t, err, errcode.NotFound)` after `d.Changes(ctx, alice, core.ChangeFilter{})` becomes `_ = wantCode(t, err, errcode.HostOffline)`. The unknown-host expectations of `TestReadRoutesBySide`, `TestResource` and `TestAgentRefusesUnknownAndMismatchedHosts` (`h9`, no row) stay `errcode.NotFound`.

A host now needs a `core_hosts` row before its client is reached. In `hosts_test.go`, insert `h.addHost("h2")` (the helper is appended below) on its own line directly above `h.clients.Add("h2", ...` in `TestAgentSaysHelloOnceAndRetriesAFailure` and in `TestAgentRefusesUnknownAndMismatchedHosts` (in the latter, after the `h9` expectation).

Append to `internal/controller/dispatch/hosts_test.go`:

```go
func TestAgentSaysHelloAgainForANewGeneration(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	hellos := 0
	agent := func() *coretest.FakeAgent {
		return &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) {
			hellos++
			return core.AgentHello{ProtocolVersion: 1, HostID: "h2"}, nil
		}}
	}
	first := agent()
	h.addHost("h2")
	h.clients.Add("h2", first)
	for range 2 {
		c, err := h.hosts.Agent(ctx, "h2")
		if err != nil || c != first {
			t.Fatalf("Agent(h2) = %v, %v; want the first client", c, err)
		}
	}
	if hellos != 1 {
		t.Fatalf("Hello calls = %d, want 1 for one generation", hellos)
	}
	second := agent()
	h.clients.Add("h2", second)
	c, err := h.hosts.Agent(ctx, "h2")
	if err != nil || c != second {
		t.Fatalf("Agent(h2) after a new session = %v, %v; want the second client", c, err)
	}
	if hellos != 2 {
		t.Errorf("Hello calls = %d, want 2: a new generation says Hello again", hellos)
	}
}

func TestNewHostsRequiresClients(t *testing.T) {
	defer func() {
		if r := recover(); r != "dispatch: HostsConfig.Clients is required" {
			t.Errorf("recover() = %v, want the missing-Clients panic", r)
		}
	}()
	NewHosts(HostsConfig{})
}

// addHost inserts a core_hosts row for id, with no client.
func (h *harness) addHost(id string) {
	h.t.Helper()
	if _, err := h.db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES (?, ?, 0, '2026-10-17T00:00:00Z')", id, id); err != nil {
		h.t.Fatal(err)
	}
}

func TestUnknownAndOfflineHosts(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.addHost("h8")
	tests := []struct {
		name, host string
		code       errcode.Code
		detail     string
	}{
		{"no core_hosts row", "h9", errcode.NotFound, "no host h9"},
		{"a row without a client", "h8", errcode.HostOffline, "host h8 is offline"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := h.hosts.Agent(ctx, tt.host)
			if e := wantCode(t, err, tt.code); e.Detail != tt.detail {
				t.Errorf("Agent detail = %q, want %q", e.Detail, tt.detail)
			}
			if e := wantCode(t, h.hosts.Hello(ctx, tt.host), tt.code); e.Detail != tt.detail {
				t.Errorf("Hello detail = %q, want %q", e.Detail, tt.detail)
			}
		})
	}
}
```

Append to `internal/controller/dispatch/changes_test.go`:

```go
func TestChangeLookupSkipsOfflineHosts(t *testing.T) {
	h, d := newDispatcher(t)
	ctx := context.Background()
	v := h.applyAgent(d, "a", core.ApplyOptions{})
	if _, err := h.db.Writer().Exec("DELETE FROM core_change_index"); err != nil {
		t.Fatal(err)
	}
	// h0 is looked at before h1 and has no client, so it is offline.
	if _, err := h.db.Writer().Exec(
		"INSERT INTO core_hosts (id, name, is_local, created_at) VALUES ('h0', 'away', 0, '2026-10-17T00:00:00Z')"); err != nil {
		t.Fatal(err)
	}
	got, err := d.Change(ctx, alice, v.ID)
	if err != nil || got.ID != v.ID || got.HostID != testHost {
		t.Errorf("Change = %+v, %v; want the change on h1", got, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go vet ./internal/controller/dispatch/`
Expected: FAIL with `unknown field Clients in struct literal of type HostsConfig`.

- [ ] **Step 3: Read clients from the source**

In `internal/controller/dispatch/hosts.go`, replace everything from `// HostsConfig wires a Hosts.` down to and including the signature line `func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient) error {` with:

```go
// HostsConfig wires a Hosts.
type HostsConfig struct {
	DB       *store.DB      // the controller database: core_module_state
	Registry *core.Registry // the one registry of the process
	Clients  ClientSource   // the agent client of each host
	Clock    core.Clock
	Log      *slog.Logger
}

// Hosts is the controller's core.HostDirectory: the agent client of each
// host, from its ClientSource. Before handing out a client of a generation
// it has not greeted, Hosts calls its Hello and records every module's
// availability; a failed Hello is tried again on the next reach. Its methods
// are safe for concurrent use.
type Hosts struct {
	cfg HostsConfig

	mu      sync.Mutex        // also serialises Hello calls
	greeted map[string]uint64 // the client generation each host last said Hello with
}

var _ core.HostDirectory = (*Hosts)(nil)

// NewHosts returns a directory over cfg.Clients. A nil Clients is a wiring
// mistake in the one place that builds a Hosts, so it panics there rather
// than failing every later call.
func NewHosts(cfg HostsConfig) *Hosts {
	if cfg.Clients == nil {
		panic("dispatch: HostsConfig.Clients is required")
	}
	return &Hosts{cfg: cfg, greeted: map[string]uint64{}}
}

// Agent returns hostID's client, after the first successful Hello of its
// current generation. An id with no core_hosts row is not_found; a host
// without a client answers host.offline.
func (h *Hosts) Agent(ctx context.Context, hostID string) (core.AgentClient, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	c, gen, err := h.client(ctx, hostID)
	if err != nil {
		return nil, err
	}
	if h.greeted[hostID] != gen {
		if err := h.hello(ctx, hostID, c, gen); err != nil {
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
	c, gen, err := h.client(ctx, hostID)
	if err != nil {
		return err
	}
	return h.hello(ctx, hostID, c, gen)
}

// client returns hostID's client and generation from the ClientSource once
// core_hosts has the host, so that a mistyped id stays not_found and only a
// known host is reported offline.
func (h *Hosts) client(ctx context.Context, hostID string) (core.AgentClient, uint64, error) {
	var one int
	err := h.cfg.DB.Read().QueryRowContext(ctx, "SELECT 1 FROM core_hosts WHERE id = ?", hostID).Scan(&one)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		return nil, 0, errcode.New(errcode.NotFound, fmt.Sprintf("no host %s", hostID))
	case err != nil:
		return nil, 0, fmt.Errorf("dispatch: read host %s: %w", hostID, err)
	}
	return h.cfg.Clients.Client(ctx, hostID)
}

// hello runs under mu. It writes available, reason and checked_at of every
// module the registry has; a missing row takes enabled from the module's
// DefaultEnabled, and an existing row keeps its enabled, which only
// core.module.enable and core.module.disable change. On success it records
// gen as greeted.
func (h *Hosts) hello(ctx context.Context, hostID string, c core.AgentClient, gen uint64) error {
```

and further down in `hello`, replace `h.greeted[hostID] = true` with `h.greeted[hostID] = gen`. The imports stay as they are: `database/sql`, `errors`, `errcode` and `fmt` are already imported for `usable`.

In `internal/controller/dispatch/changes.go`, add below `isChangeNotFound`:

```go
func isHostOffline(err error) bool {
	var e *errcode.Error
	return errors.As(err, &e) && e.Code == errcode.HostOffline
}
```

replace the head of `find`

```go
// find asks every host Hosts knows for changeID and indexes the first that
// answers with a view of that change on its own host. A host's change.not_found means it
// does not hold the change; any other error fails the lookup.
func (d *Dispatcher) find(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	for _, hostID := range d.cfg.Hosts.ids() {
		c, err := d.cfg.Hosts.Agent(ctx, hostID)
		if err != nil {
			return "", nil, fmt.Errorf("dispatch: find change %s on host %s: %w", changeID, hostID, err)
		}
```

with

```go
// find asks every online host for changeID and indexes the first that
// answers with a view of that change on its own host. A host's
// change.not_found means it does not hold the change, and an offline host
// cannot answer, so both are skipped; any other error fails the lookup.
func (d *Dispatcher) find(ctx context.Context, changeID string) (string, core.AgentClient, error) {
	hosts, err := d.hostIDs(ctx)
	if err != nil {
		return "", nil, err
	}
	for _, hostID := range hosts {
		c, err := d.cfg.Hosts.Agent(ctx, hostID)
		switch {
		case isHostOffline(err):
			continue
		case err != nil:
			return "", nil, fmt.Errorf("dispatch: find change %s on host %s: %w", changeID, hostID, err)
		}
```

and delete the whole `ids` method with its comment (`// ids returns the hosts that have a client, in id order.` through its closing brace). `sort` stays imported for `Changes`.

In `internal/controller/server/services.go`, replace

```go
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{DB: s.controllerDB, Registry: reg, Clock: s.clock, Log: s.log})
	s.hosts.Add(s.localHostID, rt)
```

with

```go
	clients := dispatch.NewStaticClients()
	clients.Add(s.localHostID, rt)
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{
		DB: s.controllerDB, Registry: reg, Clients: clients, Clock: s.clock, Log: s.log,
	})
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go build ./... && GOTOOLCHAIN=local go test -race -count=1 ./internal/controller/...`
Expected: every package `ok`, including `TestUnknownAndOfflineHosts`, `TestAgentSaysHelloAgainForANewGeneration`, `TestNewHostsRequiresClients`, `TestChangeLookupSkipsOfflineHosts`, `TestAgentSaysHelloOnceAndRetriesAFailure` and the server's end-to-end tests.

Run: `GOTOOLCHAIN=local go test -count=1 ./... && GOTOOLCHAIN=local go vet ./... && golangci-lint run --allow-serial-runners && GOTOOLCHAIN=local go mod tidy -diff`
Expected: every package `ok`, no vet output, `0 issues.`, no tidy output.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/dispatch/hosts.go internal/controller/dispatch/changes.go internal/controller/server/services.go \
  internal/controller/dispatch/fixture_test.go internal/controller/dispatch/apply_test.go internal/controller/dispatch/changes_test.go \
  internal/controller/dispatch/dispatch_test.go internal/controller/dispatch/hosts_test.go
git commit -m "refactor(dispatch): read agent clients from a source"
```

---

### Task 13: Systemd D-Bus provider and exec allowlist

**Files:**
- Create: `internal/platform/systemd.go`
- Test: `internal/platform/systemd_test.go` (new)
- Modify: `internal/platform/exec.go` (`ProductionAllowlist`)
- Modify: `internal/platform/exec_test.go` (`TestProductionAllowlistIsEmpty` becomes `TestProductionAllowlist`)

**Interfaces:**
- Consumes: C1 (`github.com/godbus/dbus/v5` in `go.mod`)
- Produces: C13 — `Systemd`, `TransientService`, `TransientTimer`, `UnitState`, `DBusSystemd`, `NewSystemd`, and the two allowlist entries. The install, update and agent-daemon parts call it; they test against their own fakes of the `Systemd` interface.

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Every systemd operation goes through D-Bus (spec section 7; ADR 0009 forbids a shell). The provider calls `org.freedesktop.systemd1.Manager` methods through a narrow unexported `busCaller`, so unit tests record the exact method names and argument encodings on a fake. `TestSystemdLive` drives the real systemd and runs only when `DARKPANEL_SYSTEMD_LIVE=1` and the test runs as root.

- [ ] **Step 1: Write the failing tests**

Create `internal/platform/systemd_test.go`:

```go
package platform

import (
	"context"
	"errors"
	"fmt"
	"os"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/godbus/dbus/v5"
)

// busCall is one method call the fake bus received.
type busCall struct {
	path   dbus.ObjectPath
	method string
	args   []any
}

// fakeBus records every call and answers with reply, which returns the
// call's body or an error.
type fakeBus struct {
	calls []busCall
	reply func(c busCall) ([]any, error)
}

func (f *fakeBus) call(_ context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	c := busCall{path: path, method: method, args: args}
	f.calls = append(f.calls, c)
	if f.reply == nil {
		return nil, nil
	}
	return f.reply(c)
}

// sig is the D-Bus signature of a call's arguments, which is what systemd
// checks a method call against.
func sig(args []any) string {
	return dbus.SignatureOf(args...).String()
}

func TestSystemdUnitMethods(t *testing.T) {
	job := []any{dbus.ObjectPath("/org/freedesktop/systemd1/job/1")}
	tests := []struct {
		name   string
		run    func(s *DBusSystemd) error
		method string
		args   []any
		sig    string
		body   []any
	}{
		{"start", func(s *DBusSystemd) error { return s.Start(context.Background(), "darkpanel-agent.service") },
			"StartUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"stop", func(s *DBusSystemd) error { return s.Stop(context.Background(), "darkpanel-agent.service") },
			"StopUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"restart", func(s *DBusSystemd) error { return s.Restart(context.Background(), "darkpanel-agent.service") },
			"RestartUnit", []any{"darkpanel-agent.service", "replace"}, "ss", job},
		{"kill", func(s *DBusSystemd) error {
			return s.Kill(context.Background(), "darkpanel-update-01J.service", syscall.SIGKILL)
		}, "KillUnit", []any{"darkpanel-update-01J.service", "all", int32(9)}, "ssi", nil},
		{"reset failed", func(s *DBusSystemd) error { return s.ResetFailed(context.Background(), "darkpanel-agent.service") },
			"ResetFailedUnit", []any{"darkpanel-agent.service"}, "s", nil},
		{"enable", func(s *DBusSystemd) error {
			return s.Enable(context.Background(), "darkpanel-agent.service", "darkpanel-update-recover.service")
		}, "EnableUnitFiles", []any{[]string{"darkpanel-agent.service", "darkpanel-update-recover.service"}, false, false},
			"asbb", []any{true, [][]any{}}},
		{"disable", func(s *DBusSystemd) error { return s.Disable(context.Background(), "darkpanel-agent.service") },
			"DisableUnitFiles", []any{[]string{"darkpanel-agent.service"}, false}, "asb", []any{[][]any{}}},
		{"reload", func(s *DBusSystemd) error { return s.Reload(context.Background()) }, "Reload", nil, "", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bus := &fakeBus{reply: func(busCall) ([]any, error) { return tt.body, nil }}
			if err := tt.run(newSystemd(bus)); err != nil {
				t.Fatal(err)
			}
			want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager." + tt.method, args: tt.args}
			if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
				t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
			}
			if got := sig(bus.calls[0].args); got != tt.sig {
				t.Errorf("signature %q, want %q", got, tt.sig)
			}
		})
	}
}

func TestSystemdErrorsNameTheUnit(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) {
		return nil, dbus.Error{Name: "org.freedesktop.systemd1.NoSuchUnit", Body: []any{"Unit x.service not loaded."}}
	}}
	err := newSystemd(bus).Stop(context.Background(), "x.service")
	if err == nil || !strings.Contains(err.Error(), "platform: stop x.service") {
		t.Errorf("error %v, want one naming the operation and unit", err)
	}
}

func TestStartTransientService(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) { return []any{dbus.ObjectPath("/job/2")}, nil }}
	err := newSystemd(bus).StartTransientService(context.Background(), "darkpanel-update-01J.service", TransientService{
		Description: "Darkpanel update 01J",
		ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
		OnFailure:   []string{"darkpanel-rollback.service"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.StartTransientUnit", args: []any{
		"darkpanel-update-01J.service", "fail",
		[]property{
			{"Description", dbus.MakeVariant("Darkpanel update 01J")},
			{"ExecStart", dbus.MakeVariant([]execCommand{{
				Path: "/usr/local/lib/darkpanel/darkpanel",
				Args: []string{"/usr/local/lib/darkpanel/darkpanel", "update", "run", "01J"},
			}})},
			{"OnFailure", dbus.MakeVariant([]string{"darkpanel-rollback.service"})},
		},
		[]auxUnit{},
	}}
	if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
		t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
	}
	if got := sig(bus.calls[0].args); got != "ssa(sv)a(sa(sv))" {
		t.Errorf("signature %q, want ssa(sv)a(sa(sv))", got)
	}
	if got := bus.calls[0].args[2].([]property)[1].Value.Signature().String(); got != "a(sasb)" {
		t.Errorf("ExecStart signature %q, want a(sasb)", got)
	}
}

func TestStartTransientTimer(t *testing.T) {
	bus := &fakeBus{reply: func(busCall) ([]any, error) { return []any{dbus.ObjectPath("/job/3")}, nil }}
	err := newSystemd(bus).StartTransientTimer(context.Background(), "darkpanel-update-deadline-01J.timer", TransientTimer{
		Description: "Darkpanel update deadline 01J",
		OnActive:    80 * time.Second,
		Accuracy:    time.Second,
		Service: TransientService{
			Description: "Darkpanel update deadline rollback 01J",
			ExecStart:   []string{"/usr/local/lib/darkpanel/darkpanel.recovery", "update", "recover", "--deadline", "01J"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := busCall{path: "/org/freedesktop/systemd1", method: "org.freedesktop.systemd1.Manager.StartTransientUnit", args: []any{
		"darkpanel-update-deadline-01J.timer", "fail",
		[]property{
			{"Description", dbus.MakeVariant("Darkpanel update deadline 01J")},
			{"OnActiveSec", dbus.MakeVariant(uint64(80_000_000))},
			{"AccuracyUSec", dbus.MakeVariant(uint64(1_000_000))},
		},
		[]auxUnit{{Name: "darkpanel-update-deadline-01J.service", Props: []property{
			{"Description", dbus.MakeVariant("Darkpanel update deadline rollback 01J")},
			{"ExecStart", dbus.MakeVariant([]execCommand{{
				Path: "/usr/local/lib/darkpanel/darkpanel.recovery",
				Args: []string{"/usr/local/lib/darkpanel/darkpanel.recovery", "update", "recover", "--deadline", "01J"},
			}})},
		}}},
	}}
	if len(bus.calls) != 1 || !reflect.DeepEqual(bus.calls[0], want) {
		t.Fatalf("calls = %#v\nwant [%#v]", bus.calls, want)
	}
	if got := sig(bus.calls[0].args); got != "ssa(sv)a(sa(sv))" {
		t.Errorf("signature %q, want ssa(sv)a(sa(sv))", got)
	}
}

func TestTransientUnitsRefuseBadInput(t *testing.T) {
	s := newSystemd(&fakeBus{})
	ctx := context.Background()
	good := TransientService{ExecStart: []string{"/bin/true"}}
	tests := []struct {
		name string
		err  error
	}{
		{"relative program", s.StartTransientService(ctx, "a.service", TransientService{ExecStart: []string{"true"}})},
		{"no program", s.StartTransientService(ctx, "a.service", TransientService{})},
		{"service name", s.StartTransientService(ctx, "a.timer", good)},
		{"timer name", s.StartTransientTimer(ctx, "a.service", TransientTimer{OnActive: time.Second, Service: good})},
		{"no delay", s.StartTransientTimer(ctx, "a.timer", TransientTimer{Service: good})},
		{"negative accuracy", s.StartTransientTimer(ctx, "a.timer", TransientTimer{OnActive: time.Second, Accuracy: -1, Service: good})},
		{"timer program", s.StartTransientTimer(ctx, "a.timer", TransientTimer{OnActive: time.Second})},
	}
	for _, tt := range tests {
		if tt.err == nil {
			t.Errorf("%s: no error", tt.name)
		}
	}
}

func TestState(t *testing.T) {
	unitPath := dbus.ObjectPath("/org/freedesktop/systemd1/unit/darkpanel_2dagent_2eservice")
	values := map[string]string{"LoadState": "loaded", "ActiveState": "active", "SubState": "running"}
	bus := &fakeBus{reply: func(c busCall) ([]any, error) {
		switch c.method {
		case "org.freedesktop.systemd1.Manager.LoadUnit":
			return []any{unitPath}, nil
		case "org.freedesktop.DBus.Properties.Get":
			if c.path != unitPath || c.args[0] != "org.freedesktop.systemd1.Unit" {
				return nil, fmt.Errorf("unexpected Get %v on %s", c.args, c.path)
			}
			return []any{dbus.MakeVariant(values[c.args[1].(string)])}, nil
		}
		return nil, errors.New("unexpected method " + c.method)
	}}
	st, err := newSystemd(bus).State(context.Background(), "darkpanel-agent.service")
	if err != nil {
		t.Fatal(err)
	}
	if st != (UnitState{LoadState: "loaded", ActiveState: "active", SubState: "running"}) {
		t.Errorf("State = %+v", st)
	}
	if bus.calls[0].method != "org.freedesktop.systemd1.Manager.LoadUnit" ||
		!reflect.DeepEqual(bus.calls[0].args, []any{"darkpanel-agent.service"}) {
		t.Errorf("first call %#v, want LoadUnit of the unit", bus.calls[0])
	}
	var props []string
	for _, c := range bus.calls[1:] {
		props = append(props, c.args[1].(string))
	}
	if !reflect.DeepEqual(props, []string{"LoadState", "ActiveState", "SubState"}) {
		t.Errorf("properties read %v", props)
	}
}

// TestSystemdLive drives the real systemd of the host, so it runs only when
// DARKPANEL_SYSTEMD_LIVE=1 and the test runs as root. It proves the D-Bus
// encodings the fake bus only records; every unit it starts is transient and
// stopped again.
func TestSystemdLive(t *testing.T) {
	if os.Getenv("DARKPANEL_SYSTEMD_LIVE") != "1" || os.Geteuid() != 0 {
		t.Skip("set DARKPANEL_SYSTEMD_LIVE=1 and run as root to drive the host's systemd")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	s, err := NewSystemd(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	prefix := fmt.Sprintf("darkpanel-live-%d-%d", os.Getpid(), time.Now().UnixNano())
	var started []string
	t.Cleanup(func() {
		for _, u := range started {
			_ = s.Stop(context.Background(), u)
			_ = s.ResetFailed(context.Background(), u)
		}
	})
	waitFor := func(unit string, ok func(UnitState) bool) UnitState {
		t.Helper()
		var st UnitState
		for range 100 {
			if st, err = s.State(ctx, unit); err != nil {
				t.Fatal(err)
			}
			if ok(st) {
				return st
			}
			time.Sleep(50 * time.Millisecond)
		}
		t.Fatalf("%s stayed %+v", unit, st)
		return st
	}
	gone := func(st UnitState) bool { return st.LoadState == "not-found" || st.ActiveState == "inactive" }
	sleep := TransientService{Description: "Darkpanel live test", ExecStart: []string{"/bin/sleep", "30"}}

	killed := prefix + "-killed.service"
	svc := sleep
	svc.OnFailure = []string{prefix + "-hook.service"}
	started = append(started, killed)
	if err := s.StartTransientService(ctx, killed, svc); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, func(st UnitState) bool { return st.ActiveState == "active" })
	if err := s.Kill(ctx, killed, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, func(st UnitState) bool { return st.ActiveState == "failed" })
	if err := s.ResetFailed(ctx, killed); err != nil {
		t.Fatal(err)
	}
	waitFor(killed, gone)

	stopped := prefix + "-stopped.service"
	started = append(started, stopped)
	if err := s.StartTransientService(ctx, stopped, sleep); err != nil {
		t.Fatal(err)
	}
	waitFor(stopped, func(st UnitState) bool { return st.ActiveState == "active" })
	if err := s.Stop(ctx, stopped); err != nil {
		t.Fatal(err)
	}
	waitFor(stopped, gone)

	timer := prefix + ".timer"
	started = append(started, timer)
	err = s.StartTransientTimer(ctx, timer, TransientTimer{
		Description: "Darkpanel live test timer", OnActive: time.Hour, Accuracy: time.Second,
		Service: TransientService{Description: "Darkpanel live test timer job", ExecStart: []string{"/bin/true"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(timer, func(st UnitState) bool { return st.ActiveState == "active" && st.SubState == "waiting" })
	if err := s.Stop(ctx, timer); err != nil {
		t.Fatal(err)
	}
	waitFor(timer, gone)

	if st, err := s.State(ctx, prefix+"-missing.service"); err != nil || st.LoadState != "not-found" {
		t.Errorf("a missing unit: %+v, %v; want LoadState not-found", st, err)
	}
}
```

In `internal/platform/exec_test.go`, add `"slices"` to the standard-library imports (after `"os/exec"`), and replace:

```go
func TestProductionAllowlistIsEmpty(t *testing.T) {
	if got := ProductionAllowlist(); len(got) != 0 {
		t.Errorf("ProductionAllowlist() = %v, want empty in M1", got)
	}
}
```

with:

```go
func TestProductionAllowlist(t *testing.T) {
	want := []string{"/usr/sbin/useradd", "/usr/sbin/userdel"}
	if got := ProductionAllowlist(); !slices.Equal(got, want) {
		t.Errorf("ProductionAllowlist() = %v, want %v", got, want)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./internal/platform/`
Expected: FAIL to build: `undefined: DBusSystemd`, `undefined: TransientService` and the other new names.

- [ ] **Step 3: Write the provider and the allowlist**

Create `internal/platform/systemd.go`:

```go
package platform

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/godbus/dbus/v5"
)

const (
	systemdDest      = "org.freedesktop.systemd1"
	systemdPath      = dbus.ObjectPath("/org/freedesktop/systemd1")
	managerInterface = "org.freedesktop.systemd1.Manager"
	unitInterface    = "org.freedesktop.systemd1.Unit"
)

// Systemd manages units through systemd's D-Bus API; Darkpanel never runs
// systemctl (ADR 0009). Every method queues its job and returns without
// waiting for it, so a caller that needs the outcome polls State.
type Systemd interface {
	StartTransientService(ctx context.Context, name string, svc TransientService) error
	StartTransientTimer(ctx context.Context, name string, t TransientTimer) error
	Start(ctx context.Context, unit string) error
	Stop(ctx context.Context, unit string) error
	Restart(ctx context.Context, unit string) error
	Kill(ctx context.Context, unit string, sig syscall.Signal) error
	ResetFailed(ctx context.Context, unit string) error
	Enable(ctx context.Context, unitFiles ...string) error
	Disable(ctx context.Context, unitFiles ...string) error
	Reload(ctx context.Context) error
	State(ctx context.Context, unit string) (UnitState, error)
}

// TransientService is a service unit that exists until it stops.
type TransientService struct {
	Description string
	ExecStart   []string // ExecStart[0] is an absolute path
	OnFailure   []string // unit names
}

// TransientTimer is a timer unit that exists until it is stopped; it starts
// Service, named like the timer with .service, once OnActive has passed.
type TransientTimer struct {
	Description string
	OnActive    time.Duration
	Accuracy    time.Duration
	Service     TransientService
}

// UnitState is a unit's state as systemd reports it. A unit that does not
// exist has LoadState "not-found".
type UnitState struct {
	LoadState, ActiveState, SubState string
}

// busCaller calls one method of a systemd object, so tests can stand in for
// the system bus.
type busCaller interface {
	call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error)
}

type connCaller struct{ conn *dbus.Conn }

func (c connCaller) call(ctx context.Context, path dbus.ObjectPath, method string, args ...any) ([]any, error) {
	call := c.conn.Object(systemdDest, path).CallWithContext(ctx, method, 0, args...)
	return call.Body, call.Err
}

// property, auxUnit and execCommand encode as the D-Bus structures
// StartTransientUnit takes: a(sv), a(sa(sv)) and ExecStart's a(sasb).
type property struct {
	Name  string
	Value dbus.Variant
}

type auxUnit struct {
	Name  string
	Props []property
}

type execCommand struct {
	Path          string
	Args          []string
	IgnoreFailure bool
}

// DBusSystemd is the Systemd of a host, over the system bus.
type DBusSystemd struct {
	bus  busCaller
	conn *dbus.Conn
}

var _ Systemd = (*DBusSystemd)(nil)

// NewSystemd connects to the system bus and checks that systemd answers on
// it. Call Close when done.
func NewSystemd(ctx context.Context) (*DBusSystemd, error) {
	conn, err := dbus.ConnectSystemBus()
	if err != nil {
		return nil, fmt.Errorf("platform: connect to the system bus: %w", err)
	}
	s := &DBusSystemd{bus: connCaller{conn}, conn: conn}
	if _, err := s.bus.call(ctx, systemdPath, "org.freedesktop.DBus.Peer.Ping"); err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("platform: systemd does not answer on the system bus: %w", err)
	}
	return s, nil
}

func newSystemd(bus busCaller) *DBusSystemd { return &DBusSystemd{bus: bus} }

// Close closes the bus connection.
func (s *DBusSystemd) Close() error { return s.conn.Close() }

func (s *DBusSystemd) manager(ctx context.Context, what, method string, args ...any) ([]any, error) {
	body, err := s.bus.call(ctx, systemdPath, managerInterface+"."+method, args...)
	if err != nil {
		return nil, fmt.Errorf("platform: %s: %w", what, err)
	}
	return body, nil
}

// StartTransientService starts name, which must end in .service. The
// "fail" mode refuses the job when name is already loaded, so a stale unit
// of the same name is never reused.
func (s *DBusSystemd) StartTransientService(ctx context.Context, name string, svc TransientService) error {
	what := "start " + name
	props, err := serviceProps(svc)
	if err == nil && !strings.HasSuffix(name, ".service") {
		err = errors.New("the unit name must end in .service")
	}
	if err != nil {
		return fmt.Errorf("platform: %s: %w", what, err)
	}
	_, err = s.manager(ctx, what, "StartTransientUnit", name, "fail", props, []auxUnit{})
	return err
}

// StartTransientTimer starts name, which must end in .timer, together with
// the service it starts.
func (s *DBusSystemd) StartTransientTimer(ctx context.Context, name string, t TransientTimer) error {
	what := "start " + name
	base, ok := strings.CutSuffix(name, ".timer")
	svcProps, err := serviceProps(t.Service)
	switch {
	case err != nil:
	case !ok:
		err = errors.New("the unit name must end in .timer")
	case t.OnActive <= 0:
		err = errors.New("the timer needs a positive delay")
	case t.Accuracy < 0:
		err = errors.New("the timer's accuracy is negative")
	}
	if err != nil {
		return fmt.Errorf("platform: %s: %w", what, err)
	}
	props := []property{
		{"Description", dbus.MakeVariant(t.Description)},
		// systemd takes both in microseconds.
		{"OnActiveSec", dbus.MakeVariant(uint64(t.OnActive.Microseconds()))},  //nolint:gosec // G115: positive, checked above
		{"AccuracyUSec", dbus.MakeVariant(uint64(t.Accuracy.Microseconds()))}, //nolint:gosec // G115: non-negative, checked above
	}
	aux := []auxUnit{{Name: base + ".service", Props: svcProps}}
	_, err = s.manager(ctx, what, "StartTransientUnit", name, "fail", props, aux)
	return err
}

func serviceProps(svc TransientService) ([]property, error) {
	if len(svc.ExecStart) == 0 || !filepath.IsAbs(svc.ExecStart[0]) {
		return nil, errors.New("ExecStart must start with an absolute program path")
	}
	props := []property{
		{"Description", dbus.MakeVariant(svc.Description)},
		{"ExecStart", dbus.MakeVariant([]execCommand{{Path: svc.ExecStart[0], Args: slices.Clone(svc.ExecStart)}})},
	}
	if len(svc.OnFailure) > 0 {
		props = append(props, property{"OnFailure", dbus.MakeVariant(slices.Clone(svc.OnFailure))})
	}
	return props, nil
}

// Start queues a start job for unit.
func (s *DBusSystemd) Start(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "start "+unit, "StartUnit", unit, "replace")
	return err
}

// Stop queues a stop job for unit.
func (s *DBusSystemd) Stop(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "stop "+unit, "StopUnit", unit, "replace")
	return err
}

// Restart queues a restart job for unit.
func (s *DBusSystemd) Restart(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "restart "+unit, "RestartUnit", unit, "replace")
	return err
}

// Kill sends sig to every process of unit.
func (s *DBusSystemd) Kill(ctx context.Context, unit string, sig syscall.Signal) error {
	_, err := s.manager(ctx, "kill "+unit, "KillUnit", unit, "all", int32(sig)) //nolint:gosec // G115: signal numbers are below 65
	return err
}

// ResetFailed clears unit's failed state and start-limit counter, which a
// crash-looping unit needs before it starts again.
func (s *DBusSystemd) ResetFailed(ctx context.Context, unit string) error {
	_, err := s.manager(ctx, "reset-failed "+unit, "ResetFailedUnit", unit)
	return err
}

// Enable enables unit files in /etc, as systemctl enable does.
func (s *DBusSystemd) Enable(ctx context.Context, unitFiles ...string) error {
	_, err := s.manager(ctx, "enable "+strings.Join(unitFiles, " "), "EnableUnitFiles", slices.Clone(unitFiles), false, false)
	return err
}

// Disable disables unit files in /etc, as systemctl disable does.
func (s *DBusSystemd) Disable(ctx context.Context, unitFiles ...string) error {
	_, err := s.manager(ctx, "disable "+strings.Join(unitFiles, " "), "DisableUnitFiles", slices.Clone(unitFiles), false)
	return err
}

// Reload reloads every unit file, as systemctl daemon-reload does.
func (s *DBusSystemd) Reload(ctx context.Context) error {
	_, err := s.manager(ctx, "daemon-reload", "Reload")
	return err
}

// State reports unit's load, active and sub state. LoadUnit, unlike
// GetUnit, answers for a unit that is not loaded, so a missing unit reports
// LoadState "not-found" instead of an error.
func (s *DBusSystemd) State(ctx context.Context, unit string) (UnitState, error) {
	what := "state of " + unit
	body, err := s.manager(ctx, what, "LoadUnit", unit)
	if err != nil {
		return UnitState{}, err
	}
	var path dbus.ObjectPath
	if err := dbus.Store(body, &path); err != nil {
		return UnitState{}, fmt.Errorf("platform: %s: %w", what, err)
	}
	var st UnitState
	for _, p := range []struct {
		name string
		dst  *string
	}{{"LoadState", &st.LoadState}, {"ActiveState", &st.ActiveState}, {"SubState", &st.SubState}} {
		body, err := s.bus.call(ctx, path, "org.freedesktop.DBus.Properties.Get", unitInterface, p.name)
		if err == nil {
			var v dbus.Variant
			if err = dbus.Store(body, &v); err == nil {
				err = v.Store(p.dst)
			}
		}
		if err != nil {
			return UnitState{}, fmt.Errorf("platform: %s: read %s: %w", what, p.name, err)
		}
	}
	return st, nil
}
```

In `internal/platform/exec.go`, replace:

```go
// ProductionAllowlist is the agent's allowlist. It is empty in M1; later
// epics add entries such as /usr/sbin/ufw.
func ProductionAllowlist() []string { return []string{} }
```

with:

```go
// ProductionAllowlist is the agent's allowlist. The installer runs
// useradd and userdel for the controller's system user; every systemd
// operation goes through D-Bus instead (Systemd).
func ProductionAllowlist() []string { return []string{"/usr/sbin/useradd", "/usr/sbin/userdel"} }
```

The three `//nolint:gosec // G115` comments are required: without them gosec reports the `int64` to `uint64` and `int` to `int32` conversions even after the range checks.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -race -count=1 ./internal/platform/ ./internal/agent/... ./internal/controller/server/`
Expected: PASS; `TestSystemdLive` reports SKIP without the variable.

- [ ] **Step 5: Run the live test when the host allows it**

When `id -u` prints `0` and `/run/dbus/system_bus_socket` exists, run: `DARKPANEL_SYSTEMD_LIVE=1 GOTOOLCHAIN=local go test -count=1 -run TestSystemdLive -v ./internal/platform/`
Expected: `--- PASS: TestSystemdLive`. Then `systemctl list-units --all 'darkpanel-live-*' --no-legend` prints nothing: the test stopped every unit it started. On a host without root or a system bus, record in the task report that the live test was skipped.

- [ ] **Step 6: Lint**

Run: `gofmt -l internal/platform/ && GOTOOLCHAIN=local go vet ./internal/platform/ && golangci-lint run --allow-serial-runners ./internal/platform/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from `gofmt -l`, `0 issues.`, and no diff (`godbus/dbus/v5` moves from `internal/deps` to a real import; `go.mod` keeps its line).

- [ ] **Step 7: Commit**

```bash
git add internal/platform/systemd.go internal/platform/systemd_test.go internal/platform/exec.go internal/platform/exec_test.go
git commit -m "feat(platform): add the systemd D-Bus provider"
```

---

### Task 14: Agent API wire types

**Files:**
- Create: `internal/agent/wire/wire.go`
- Test: `internal/agent/wire/wire_test.go`

**Interfaces:**
- Consumes: C4 (`AgentHello.Arch`, `JournalEpoch`, `ControllerConnected`, `SchemaHashes`; `core.SchemaHash`)
- Produces: C14 — the header names, `MaxBody`, the wire types, their `From*` and `Core` conversions, `ChangeFilterQuery` and `ParseChangeFilter`. The agent API part serves and calls the agent routes with these bodies only.

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

The `core` types carry no JSON tags, so the agent API never encodes them directly (spec section 5). Each wire type converts to and from its `core` counterpart; a round trip through JSON returns the original value, with times in UTC, the confirm timeout in whole seconds, and an absent `Output` or `Input` as `nil` on both sides (JSON `null` on the wire). `core.DiffLine` and `core.Guard` already carry camelCase JSON tags and are reused as they are.

- [ ] **Step 1: Write the failing tests**

Create `internal/agent/wire/wire_test.go`:

```go
package wire

import (
	"encoding/json"
	"net/url"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	t0 = time.Date(2026, 10, 19, 12, 0, 0, 123456789, time.UTC)
	t1 = time.Date(2026, 10, 19, 12, 5, 0, 0, time.UTC)
)

// roundTrip sends v through JSON as the agent API does and returns what
// the other side decodes.
func roundTrip[T any](t *testing.T, v T) T {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func check[C any](t *testing.T, name string, got, want C) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s round trip\n got %#v\nwant %#v", name, got, want)
	}
}

func TestRoundTrips(t *testing.T) {
	yes := true
	deadline := t1
	for _, p := range []core.Principal{
		{Actor: "user:01J", UserID: "01J", Scope: core.ScopeAdmin, Source: "100.64.0.2"},
		{Actor: "token:01K", UserID: "01J", TokenID: "01K", Scope: core.ScopeRead, Source: "100.64.0.3"},
	} {
		check(t, "Principal", roundTrip(t, FromPrincipal(p)).Core(), p)
	}
	for _, h := range []core.AgentHello{
		{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "amd64", HostID: "01H", JournalEpoch: "01E",
			ControllerConnected: &yes,
			Modules:             []core.ModuleStatus{{ID: "hostinfo", Version: 1, Available: true}, {ID: "fw", Version: 2, Reason: "ufw missing"}},
			SchemaHashes:        map[string]core.SchemaHash{"hostinfo.summary@1": {Input: "aa", Output: "bb"}}},
		{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "arm64", HostID: "01H", JournalEpoch: "01E"},
	} {
		check(t, "Hello", roundTrip(t, FromHello(h)).Core(), h)
	}
	for _, pv := range []core.PlanView{
		{PlanID: "01P", HostID: "01H", Action: "demo.rule.set", Version: 2, InputHash: "ff", Confirmation: "delete web",
			Diff: []core.DiffLine{{Op: "+", Text: "allow 22"}}, Guards: []core.Guard{{ID: "ssh", OK: true, Message: "kept"}}, ExpiresAt: t0},
		{PlanID: "01P", HostID: "01H", Action: "demo.rule.set", Version: 1, ExpiresAt: t0},
	} {
		check(t, "PlanView", roundTrip(t, FromPlanView(pv)).Core(), pv)
	}
	for _, o := range []core.ApplyOptions{
		{ConfirmTimeout: 90 * time.Second, Confirmation: "delete web", ConfirmNow: true},
		{},
	} {
		check(t, "ApplyOptions", roundTrip(t, FromApplyOptions(o)).Core(), o)
	}
	for _, c := range []core.ChangeView{
		{ID: "01C", HostID: "01H", Action: "demo.rule.set", Actor: "user:01J", CompensatedBy: "demo.rule.unset", Version: 1,
			Class: core.ClassRevertible, State: core.StatePending, CreatedAt: t0, UpdatedAt: t1, RevertDeadline: &deadline,
			Output: json.RawMessage(`{"rule":"allow 22"}`), Error: &core.ChangeError{Code: "plan.expired", Message: "too late"}},
		{ID: "01C", HostID: "01H", Action: "demo.rule.get", Version: 1, Class: core.ClassCompensable, State: core.StateCompleted,
			CreatedAt: t0, UpdatedAt: t0},
	} {
		check(t, "ChangeView", roundTrip(t, FromChangeView(c)).Core(), c)
	}
	for _, e := range []core.JournalEntry{
		{Seq: 7, At: t0, Actor: "user:01J", Action: "demo.rule.set", Version: 1, Phase: "apply", ChangeID: "01C",
			Input: json.RawMessage(`{"rule":"allow 22"}`), Result: "error", ErrorCode: "plan.expired", Source: "100.64.0.2"},
		{Seq: 8, At: t1, Actor: "system:revert-timer", Action: "demo.rule.set", Version: 1, Phase: "revert", ChangeID: "01C", Result: "ok"},
	} {
		check(t, "JournalEntry", roundTrip(t, FromJournalEntry(e)).Core(), e)
	}
	check(t, "Ack", roundTrip(t, Ack{Seq: 9}), Ack{Seq: 9})
}

func TestTimesAreSentInUTC(t *testing.T) {
	local := time.Date(2026, 10, 19, 19, 0, 0, 0, time.FixedZone("ICT", 7*3600))
	b, err := json.Marshal(FromPlanView(core.PlanView{ExpiresAt: local}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"expiresAt":"2026-10-19T12:00:00Z"`) {
		t.Errorf("PlanView JSON %s, want expiresAt in UTC", b)
	}
	if got := roundTrip(t, FromPlanView(core.PlanView{ExpiresAt: local})).Core().ExpiresAt; !got.Equal(local) || got.Location() != time.UTC {
		t.Errorf("ExpiresAt = %v, want the same instant in UTC", got)
	}
}

func TestNullFields(t *testing.T) {
	b, err := json.Marshal(FromChangeView(core.ChangeView{}))
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{`"revertDeadline":null`, `"output":null`, `"error":null`} {
		if !strings.Contains(string(b), f) {
			t.Errorf("ChangeView JSON %s lacks %s", b, f)
		}
	}
	b, err = json.Marshal(FromHello(core.AgentHello{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"controllerConnected":null`) {
		t.Errorf("Hello JSON %s, want controllerConnected null for an agent that is not enrolled", b)
	}
	b, err = json.Marshal(FromApplyOptions(core.ApplyOptions{ConfirmTimeout: 90 * time.Second}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"confirmTimeoutSeconds":90`) {
		t.Errorf("ApplyOptions JSON %s, want the timeout in whole seconds", b)
	}
}

var camelCase = regexp.MustCompile(`^[a-z][a-zA-Z0-9]*$`)

// TestJSONNamesAreCamelCase walks every wire type, and the core types they
// embed, and requires a camelCase JSON name on every exported field.
func TestJSONNamesAreCamelCase(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var walk func(reflect.Type)
	walk = func(typ reflect.Type) {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Map {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || seen[typ] || typ == reflect.TypeFor[time.Time]() {
			return
		}
		seen[typ] = true
		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name, _, _ := strings.Cut(f.Tag.Get("json"), ",")
			if !camelCase.MatchString(name) {
				t.Errorf("%s.%s has JSON name %q, want camelCase", typ, f.Name, name)
			}
			walk(f.Type)
		}
	}
	for _, v := range []any{Principal{}, Hello{}, SchemaHash{}, ModuleStatus{}, PlanView{}, ApplyOptions{}, ChangeView{},
		ChangeError{}, JournalEntry{}, Ack{}} {
		walk(reflect.TypeOf(v))
	}
}

func TestChangeFilterQuery(t *testing.T) {
	for _, f := range []core.ChangeFilter{
		{State: "pending", HostID: "01H", Before: "01C", Limit: 50},
		{},
	} {
		q := ChangeFilterQuery(f)
		got, err := ParseChangeFilter(q)
		if err != nil {
			t.Fatal(err)
		}
		if got != f {
			t.Errorf("ParseChangeFilter(%v) = %+v, want %+v", q, got, f)
		}
	}
	if q := ChangeFilterQuery(core.ChangeFilter{State: "pending", Limit: 5}).Encode(); q != "limit=5&state=pending" {
		t.Errorf("query %q, want only the set fields", q)
	}
}

func TestParseChangeFilterRefuses(t *testing.T) {
	for _, q := range []string{"limit=0", "limit=-1", "limit=ten", "limit=1&limit=2", "host=01H"} {
		v, err := url.ParseQuery(q)
		if err != nil {
			t.Fatal(err)
		}
		_, err = ParseChangeFilter(v)
		if e := errcode.From(err); err == nil || e.Code != errcode.InvalidInput {
			t.Errorf("ParseChangeFilter(%q) = %v, want request.invalid_input", q, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./internal/agent/wire/`
Expected: FAIL to build: `undefined: FromPrincipal`, `undefined: FromHello` and the other wire names.

- [ ] **Step 3: Write the wire types**

Create `internal/agent/wire/wire.go`:

```go
// Package wire holds the request and response bodies of the agent API
// (ADR 0012). The core types carry no JSON tags, so the API never
// encodes them directly: every body is one of these types, with camelCase
// fields, times in UTC, durations in whole seconds and absent values as JSON
// null, and with conversions to and from its core counterpart.
package wire

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"slices"
	"strconv"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// The agent API's headers (ADR 0012).
const (
	// HeaderPrincipal carries the JSON of the calling Principal; the agent
	// accepts it only from the authenticated controller.
	HeaderPrincipal    = "Darkpanel-Principal"
	HeaderProtocol     = "Darkpanel-Protocol"
	HeaderVersion      = "Darkpanel-Version"
	HeaderControllerID = "Darkpanel-Controller-Id"
)

// MaxBody is the largest request or response body the agent API accepts.
const MaxBody = 4 << 20

// Principal is core.Principal.
type Principal struct {
	Actor   string `json:"actor"`
	UserID  string `json:"userId"`
	TokenID string `json:"tokenId"`
	Scope   string `json:"scope"`
	Source  string `json:"source"`
}

// FromPrincipal converts p.
func FromPrincipal(p core.Principal) Principal {
	return Principal{Actor: p.Actor, UserID: p.UserID, TokenID: p.TokenID, Scope: string(p.Scope), Source: p.Source}
}

// Core converts p back.
func (p Principal) Core() core.Principal {
	return core.Principal{Actor: p.Actor, UserID: p.UserID, TokenID: p.TokenID, Scope: core.Scope(p.Scope), Source: p.Source}
}

// SchemaHash is core.SchemaHash.
type SchemaHash struct {
	Input  string `json:"input"`
	Output string `json:"output"`
}

// ModuleStatus is core.ModuleStatus.
type ModuleStatus struct {
	ID        string `json:"id"`
	Version   int    `json:"version"`
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// FromModuleStatus converts s.
func FromModuleStatus(s core.ModuleStatus) ModuleStatus {
	return ModuleStatus{ID: s.ID, Version: s.Version, Available: s.Available, Reason: s.Reason}
}

// Core converts s back.
func (s ModuleStatus) Core() core.ModuleStatus {
	return core.ModuleStatus{ID: s.ID, Version: s.Version, Available: s.Available, Reason: s.Reason}
}

// Hello is core.AgentHello. ControllerConnected is null for an agent that
// is not enrolled.
type Hello struct {
	ProtocolVersion     int                   `json:"protocolVersion"`
	BinaryVersion       string                `json:"binaryVersion"`
	OS                  string                `json:"os"`
	Arch                string                `json:"arch"`
	HostID              string                `json:"hostId"`
	JournalEpoch        string                `json:"journalEpoch"`
	ControllerConnected *bool                 `json:"controllerConnected"`
	Modules             []ModuleStatus        `json:"modules"`
	SchemaHashes        map[string]SchemaHash `json:"schemaHashes"`
}

// FromHello converts h.
func FromHello(h core.AgentHello) Hello {
	out := Hello{
		ProtocolVersion: h.ProtocolVersion, BinaryVersion: h.BinaryVersion, OS: h.OS, Arch: h.Arch, HostID: h.HostID,
		JournalEpoch: h.JournalEpoch, ControllerConnected: clonePtr(h.ControllerConnected),
	}
	if h.Modules != nil {
		out.Modules = make([]ModuleStatus, len(h.Modules))
		for i, m := range h.Modules {
			out.Modules[i] = FromModuleStatus(m)
		}
	}
	if h.SchemaHashes != nil {
		out.SchemaHashes = make(map[string]SchemaHash, len(h.SchemaHashes))
		for k, v := range h.SchemaHashes {
			out.SchemaHashes[k] = SchemaHash{Input: v.Input, Output: v.Output}
		}
	}
	return out
}

// Core converts h back.
func (h Hello) Core() core.AgentHello {
	out := core.AgentHello{
		ProtocolVersion: h.ProtocolVersion, BinaryVersion: h.BinaryVersion, OS: h.OS, Arch: h.Arch, HostID: h.HostID,
		JournalEpoch: h.JournalEpoch, ControllerConnected: clonePtr(h.ControllerConnected),
	}
	if h.Modules != nil {
		out.Modules = make([]core.ModuleStatus, len(h.Modules))
		for i, m := range h.Modules {
			out.Modules[i] = m.Core()
		}
	}
	if h.SchemaHashes != nil {
		out.SchemaHashes = make(map[string]core.SchemaHash, len(h.SchemaHashes))
		for k, v := range h.SchemaHashes {
			out.SchemaHashes[k] = core.SchemaHash{Input: v.Input, Output: v.Output}
		}
	}
	return out
}

// PlanView is core.PlanView.
type PlanView struct {
	PlanID       string          `json:"planId"`
	HostID       string          `json:"hostId"`
	Action       string          `json:"action"`
	Version      int             `json:"version"`
	InputHash    string          `json:"inputHash"`
	Confirmation string          `json:"confirmation"`
	Diff         []core.DiffLine `json:"diff"`
	Guards       []core.Guard    `json:"guards"`
	ExpiresAt    time.Time       `json:"expiresAt"`
}

// FromPlanView converts v.
func FromPlanView(v core.PlanView) PlanView {
	return PlanView{
		PlanID: v.PlanID, HostID: v.HostID, Action: v.Action, Version: v.Version, InputHash: v.InputHash,
		Confirmation: v.Confirmation, Diff: slices.Clone(v.Diff), Guards: slices.Clone(v.Guards), ExpiresAt: v.ExpiresAt.UTC(),
	}
}

// Core converts v back.
func (v PlanView) Core() core.PlanView {
	return core.PlanView{
		PlanID: v.PlanID, HostID: v.HostID, Action: v.Action, Version: v.Version, InputHash: v.InputHash,
		Confirmation: v.Confirmation, Diff: slices.Clone(v.Diff), Guards: slices.Clone(v.Guards), ExpiresAt: v.ExpiresAt.UTC(),
	}
}

// ApplyOptions is core.ApplyOptions, with the confirm timeout in whole
// seconds.
type ApplyOptions struct {
	ConfirmTimeoutSeconds int    `json:"confirmTimeoutSeconds"`
	Confirmation          string `json:"confirmation"`
	ConfirmNow            bool   `json:"confirmNow"`
}

// FromApplyOptions converts o; a fraction of a second is dropped.
func FromApplyOptions(o core.ApplyOptions) ApplyOptions {
	return ApplyOptions{
		ConfirmTimeoutSeconds: int(o.ConfirmTimeout / time.Second), Confirmation: o.Confirmation, ConfirmNow: o.ConfirmNow,
	}
}

// Core converts o back.
func (o ApplyOptions) Core() core.ApplyOptions {
	return core.ApplyOptions{
		ConfirmTimeout: time.Duration(o.ConfirmTimeoutSeconds) * time.Second, Confirmation: o.Confirmation, ConfirmNow: o.ConfirmNow,
	}
}

// ChangeError is core.ChangeError.
type ChangeError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ChangeView is core.ChangeView.
type ChangeView struct {
	ID             string          `json:"id"`
	HostID         string          `json:"hostId"`
	Action         string          `json:"action"`
	Version        int             `json:"version"`
	Actor          string          `json:"actor"`
	Class          string          `json:"class"`
	CompensatedBy  string          `json:"compensatedBy"`
	State          string          `json:"state"`
	CreatedAt      time.Time       `json:"createdAt"`
	UpdatedAt      time.Time       `json:"updatedAt"`
	RevertDeadline *time.Time      `json:"revertDeadline"`
	Output         json.RawMessage `json:"output"`
	Error          *ChangeError    `json:"error"`
}

// FromChangeView converts v.
func FromChangeView(v core.ChangeView) ChangeView {
	out := ChangeView{
		ID: v.ID, HostID: v.HostID, Action: v.Action, Version: v.Version, Actor: v.Actor, Class: string(v.Class),
		CompensatedBy: v.CompensatedBy, State: v.State, CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
		RevertDeadline: utcPtr(v.RevertDeadline), Output: raw(v.Output),
	}
	if v.Error != nil {
		out.Error = &ChangeError{Code: v.Error.Code, Message: v.Error.Message}
	}
	return out
}

// Core converts v back.
func (v ChangeView) Core() core.ChangeView {
	out := core.ChangeView{
		ID: v.ID, HostID: v.HostID, Action: v.Action, Version: v.Version, Actor: v.Actor, Class: core.Class(v.Class),
		CompensatedBy: v.CompensatedBy, State: v.State, CreatedAt: v.CreatedAt.UTC(), UpdatedAt: v.UpdatedAt.UTC(),
		RevertDeadline: utcPtr(v.RevertDeadline), Output: raw(v.Output),
	}
	if v.Error != nil {
		out.Error = &core.ChangeError{Code: v.Error.Code, Message: v.Error.Message}
	}
	return out
}

// JournalEntry is core.JournalEntry.
type JournalEntry struct {
	Seq       int64           `json:"seq"`
	At        time.Time       `json:"at"`
	Actor     string          `json:"actor"`
	Action    string          `json:"action"`
	Version   int             `json:"version"`
	Phase     string          `json:"phase"`
	ChangeID  string          `json:"changeId"`
	Input     json.RawMessage `json:"input"`
	Result    string          `json:"result"`
	ErrorCode string          `json:"errorCode"`
	Source    string          `json:"source"`
}

// FromJournalEntry converts e.
func FromJournalEntry(e core.JournalEntry) JournalEntry {
	return JournalEntry{
		Seq: e.Seq, At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Version: e.Version, Phase: e.Phase,
		ChangeID: e.ChangeID, Input: raw(e.Input), Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source,
	}
}

// Core converts e back.
func (e JournalEntry) Core() core.JournalEntry {
	return core.JournalEntry{
		Seq: e.Seq, At: e.At.UTC(), Actor: e.Actor, Action: e.Action, Version: e.Version, Phase: e.Phase,
		ChangeID: e.ChangeID, Input: raw(e.Input), Result: e.Result, ErrorCode: e.ErrorCode, Source: e.Source,
	}
}

// Ack is the body of POST journal/ack: the highest sequence the controller
// stored.
type Ack struct {
	Seq int64 `json:"seq"`
}

// The query parameters of GET changes.
var changeFilterParams = []string{"before", "hostId", "limit", "state"}

// ChangeFilterQuery encodes f as the query of GET changes, leaving out the
// fields that are not set.
func ChangeFilterQuery(f core.ChangeFilter) url.Values {
	q := url.Values{}
	for k, v := range map[string]string{"state": f.State, "hostId": f.HostID, "before": f.Before} {
		if v != "" {
			q.Set(k, v)
		}
	}
	if f.Limit != 0 {
		q.Set("limit", strconv.Itoa(f.Limit))
	}
	return q
}

// ParseChangeFilter decodes the query of GET changes. An unknown or
// repeated parameter, or a limit that is not a positive integer, is
// request.invalid_input.
func ParseChangeFilter(q url.Values) (core.ChangeFilter, error) {
	for _, k := range slices.Sorted(maps.Keys(q)) {
		switch {
		case !slices.Contains(changeFilterParams, k):
			return core.ChangeFilter{}, invalidQuery(k, "unknown parameter")
		case len(q[k]) > 1:
			return core.ChangeFilter{}, invalidQuery(k, "given more than once")
		}
	}
	f := core.ChangeFilter{State: q.Get("state"), HostID: q.Get("hostId"), Before: q.Get("before")}
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 {
			return core.ChangeFilter{}, invalidQuery("limit", "must be a positive integer")
		}
		f.Limit = n
	}
	return f, nil
}

func invalidQuery(param, msg string) error {
	return &errcode.Error{
		Code: errcode.InvalidInput, Detail: fmt.Sprintf("query parameter %s: %s", param, msg),
		Fields: []errcode.FieldError{{Path: "/" + param, Message: msg}},
	}
}

func clonePtr[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func utcPtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	u := t.UTC()
	return &u
}

// raw copies m, mapping JSON null to nil, so an absent value is nil on both
// sides of the API.
func raw(m json.RawMessage) json.RawMessage {
	if m == nil || bytes.Equal(m, []byte("null")) {
		return nil
	}
	return bytes.Clone(m)
}
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -race -count=1 ./internal/agent/wire/`
Expected: PASS (`ok  	github.com/darkraise/darkpanel/internal/agent/wire`).

- [ ] **Step 5: Lint**

Run: `gofmt -l internal/agent/wire/ && GOTOOLCHAIN=local go vet ./internal/agent/wire/ && golangci-lint run --allow-serial-runners ./internal/agent/wire/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output from `gofmt -l`, `0 issues.`, and no diff.

- [ ] **Step 6: Commit**

```bash
git add internal/agent/wire/wire.go internal/agent/wire/wire_test.go
git commit -m "feat(wire): add the agent API wire types"
```

---

### Task 15: Embedded schema snapshots and schema hashes

**Files:**
- Create: `schemas/embed.go`
- Create: `schemas/released/README.md`
- Test: `schemas/embed_test.go`

**Interfaces:**
- Consumes: C4 (`core.SchemaHash`).
- Produces: C12 (`schemas.FS`, `schemas.Hash`, `schemas.SnapshotHashes`). The host-actions part looks agent hashes up in `FS`; the agent daemon part fills `AgentHello.SchemaHashes` with `SnapshotHashes`.

**Depends on:** Task 4

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

`schemas/released/` already exists and holds only `.gitkeep`. Embedding skips hidden files, and a `go:embed` pattern that matches a directory with nothing embeddable is a build error, so the README is what keeps `//go:embed */*.json released` valid before any tag is released. Leave `.gitkeep` in place. Neither the snapshot test (`internal/modules/modules_test.go`) nor `make schemas` (`schema.WriteSnapshots`) looks at non-JSON files or under `released/`, so they need no change.

- [ ] **Step 1: Write the failing test**

Create `schemas/embed_test.go`. The two constants are byte-for-byte copies of `schemas/core.user.delete/v1.json` and `schemas/core.token.revoke/v1.json` at the time of writing, so a later snapshot change cannot move the vectors (the constant names avoid the word "token", which gosec's G101 reads as a credential):

```go
package schemas

import (
	"encoding/json"
	"io/fs"
	"testing"
)

// userDeleteSnapshot and revokeSnapshot are copies of two committed snapshots, so a
// later change to the files under schemas/ cannot move the test vectors.
const userDeleteSnapshot = `{
  "name": "core.user.delete",
  "version": 1,
  "class": "irreversible",
  "side": "controller",
  "breakGlass": true,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/UsernameInput",
    "$defs": {
      "UsernameInput": {
        "properties": {
          "username": {
            "type": "string",
            "pattern": "^[a-z0-9][a-z0-9._-]{0,63}$",
            "description": "The user's name."
          }
        },
        "additionalProperties": false,
        "type": "object",
        "required": [
          "username"
        ]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/Empty",
    "$defs": {
      "Empty": {
        "properties": {},
        "type": "object"
      }
    }
  }
}`

const revokeSnapshot = `{
  "name": "core.token.revoke",
  "version": 1,
  "class": "irreversible",
  "side": "controller",
  "breakGlass": false,
  "input": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/TokenRevokeInput",
    "$defs": {
      "TokenRevokeInput": {
        "properties": {
          "id": {
            "type": "string",
            "minLength": 1,
            "description": "Id of one of the calling user's tokens."
          }
        },
        "additionalProperties": false,
        "type": "object",
        "required": [
          "id"
        ]
      }
    }
  },
  "output": {
    "$schema": "https://json-schema.org/draft/2020-12/schema",
    "$ref": "#/$defs/TokenInfo",
    "$defs": {
      "TokenInfo": {
        "properties": {
          "id": {
            "type": "string"
          },
          "name": {
            "type": "string"
          },
          "scope": {
            "type": "string"
          },
          "createdAt": {
            "type": "string",
            "format": "date-time"
          },
          "expiresAt": {
            "type": "string",
            "format": "date-time"
          },
          "lastUsedAt": {
            "oneOf": [
              {
                "type": "string",
                "format": "date-time"
              },
              {
                "type": "null"
              }
            ]
          },
          "revokedAt": {
            "oneOf": [
              {
                "type": "string",
                "format": "date-time"
              },
              {
                "type": "null"
              }
            ]
          }
        },
        "type": "object",
        "required": [
          "id",
          "name",
          "scope",
          "createdAt",
          "expiresAt",
          "lastUsedAt",
          "revokedAt"
        ]
      }
    }
  }
}`

func TestSnapshotHashes(t *testing.T) {
	tests := []struct {
		name, snapshot, input, output string
	}{
		{"core.user.delete/v1", userDeleteSnapshot,
			"d3b3f8f18795bff4bd079f933449546518a8447431dd2ccfe0ad762b157a511d",
			"66fd38057a96d6525f6628f34c25cf81ea54119c1b052af31a4f53b0208781fc"},
		{"core.token.revoke/v1", revokeSnapshot,
			"70958110f1480feebf8146303df39c9d80494e4eb6f5ec162590966eb3793c45",
			"0459807f13c2660e3255cd4a9773c4779e5b43882056eb9ecaf9e4807dd7d920"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := SnapshotHashes([]byte(tt.snapshot))
			if err != nil {
				t.Fatalf("SnapshotHashes: %v", err)
			}
			if got.Input != tt.input || got.Output != tt.output {
				t.Errorf("SnapshotHashes = %+v, want input %s, output %s", got, tt.input, tt.output)
			}
		})
	}
}

func TestHashIgnoresLayout(t *testing.T) {
	a, err := Hash(json.RawMessage(`{"b": 1, "a": [true, null]}`))
	if err != nil {
		t.Fatal(err)
	}
	b, err := Hash(json.RawMessage(`{"a":[true,null],"b":1}`))
	if err != nil {
		t.Fatal(err)
	}
	if a != b {
		t.Errorf("Hash differs by key order and whitespace: %s, %s", a, b)
	}
	if _, err := Hash(json.RawMessage(`{`)); err == nil {
		t.Error("Hash of invalid JSON succeeded")
	}
}

func TestResourceSnapshotHashesNullInput(t *testing.T) {
	got, err := SnapshotHashes([]byte(`{"name": "x.y", "output": {"type": "object"}}`))
	if err != nil {
		t.Fatal(err)
	}
	null, err := Hash(json.RawMessage("null"))
	if err != nil {
		t.Fatal(err)
	}
	if got.Input != null {
		t.Errorf("Input = %s, want the hash of null %s", got.Input, null)
	}
	if got.Output == null || got.Output == "" {
		t.Errorf("Output = %q, want the hash of the output schema", got.Output)
	}
}

func TestFS(t *testing.T) {
	for _, name := range []string{"core.host.list/v1.json", "hostinfo.summary/v1.json", "released/README.md"} {
		if _, err := fs.Stat(FS, name); err != nil {
			t.Errorf("FS lacks %s: %v", name, err)
		}
	}
	if _, err := fs.Stat(FS, "embed.go"); err == nil {
		t.Error("FS embeds embed.go")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `GOTOOLCHAIN=local go test ./schemas/`
Expected: FAIL, build failed with `undefined: SnapshotHashes`, `undefined: Hash` and `undefined: FS`.

- [ ] **Step 3: Write the implementation**

Create `schemas/embed.go`. Keep the comment wrapped so that no comment line starts with `go:embed` (staticcheck SA9009 reads `// go:embed` at a line start as a broken directive):

```go
// Package schemas embeds the committed schema snapshots (ADR 0002), so a
// schema hash an agent reports can be mapped back to the schema it names.
package schemas

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"encoding/json"
	"fmt"

	"github.com/darkraise/darkpanel/internal/core"
)

// FS holds "<action or resource>/v<n>.json" for the current tree and
// "released/<tag>/<action or resource>/v<n>.json" for released tags. An
// embed pattern cannot exclude this file, so the snapshots are matched by
// their one-directory depth; released/README.md keeps that directory
// embeddable before any tag exists, because embedding skips hidden files.
//
//go:embed */*.json released
var FS embed.FS

// Hash is the lower-case hex SHA-256 of member re-encoded by encoding/json
// after decoding it into any: sorted keys and no whitespace, so the layout of
// a snapshot file does not change its hash.
func Hash(member json.RawMessage) (string, error) {
	var v any
	if err := json.Unmarshal(member, &v); err != nil {
		return "", fmt.Errorf("schemas: hash: %w", err)
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "", fmt.Errorf("schemas: hash: %w", err)
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:]), nil
}

// SnapshotHashes returns the hashes of a snapshot's input and output
// schemas. A resource snapshot has no input; its Input is the hash of null.
func SnapshotHashes(snapshot []byte) (core.SchemaHash, error) {
	var s struct {
		Input  json.RawMessage `json:"input"`
		Output json.RawMessage `json:"output"`
	}
	if err := json.Unmarshal(snapshot, &s); err != nil {
		return core.SchemaHash{}, fmt.Errorf("schemas: read snapshot: %w", err)
	}
	if s.Input == nil {
		s.Input = json.RawMessage("null")
	}
	in, err := Hash(s.Input)
	if err != nil {
		return core.SchemaHash{}, err
	}
	out, err := Hash(s.Output)
	if err != nil {
		return core.SchemaHash{}, err
	}
	return core.SchemaHash{Input: in, Output: out}, nil
}
```

Create `schemas/released/README.md`:

```markdown
# Released schema snapshots

`make release-prep VERSION=x.y.z` copies the current snapshots here as
`released/vx.y.z/`. A released snapshot is never edited (ADR 0002): the
controller validates an older agent's input against it, and an agent's schema
hash is looked up among these files.
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 ./schemas/ ./internal/modules/`
Expected: two `ok` lines.

- [ ] **Step 5: Vet, lint and check the module files**

Run: `GOTOOLCHAIN=local go vet ./schemas/ && golangci-lint run --allow-serial-runners ./schemas/ && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` and no other output.

- [ ] **Step 6: Commit**

```bash
git add schemas/embed.go schemas/embed_test.go schemas/released/README.md
git commit -m "feat(schemas): embed snapshots and hash schemas"
```

---

### Task 16: Trusted release keys

**Files:**
- Create: `internal/update/trustedkeys/keys.go`
- Create: `internal/update/trustedkeys/production.go`
- Create: `internal/update/trustedkeys/harness.go`
- Create: `internal/update/trustedkeys/testdata/harness.key`
- Test: `internal/update/trustedkeys/keys_test.go`, `internal/update/trustedkeys/keys_production_test.go`, `internal/update/trustedkeys/keys_harness_test.go`

**Interfaces:**
- Consumes: C1 (`aead.dev/minisign`, test only).
- Produces: C15 (`trustedkeys.Key`, `Keys`, `TestBuild`, `BaseURL`, the harness key pair). Task 18's generator, the update library and `darkpanel version --json` read it.

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

The production list stays empty: only the owner adds the production key (spec section 9). The harness key pair below was generated on 2026-10-19 with `aead.dev/minisign` v0.3.0 `GenerateKey`; its private key is committed unencrypted because only builds with the `harnesskeys` tag trust it, and `keys_production_test.go` proves a default build does not.

- [ ] **Step 1: Write the failing tests**

Create `internal/update/trustedkeys/keys_test.go` (runs in both builds):

```go
package trustedkeys

import (
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"testing"
)

// harnessKeyID is the id of the harness test key, which no production
// build may trust.
const harnessKeyID = "BCB10AB3E9AF694E"

func TestKeysAreWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, k := range Keys() {
		raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
		if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
			t.Errorf("key %s: public key %q is not a minisign Ed25519 key", k.ID, k.PublicKey)
			continue
		}
		if id := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])); id != k.ID {
			t.Errorf("key %s: its public key carries id %s", k.ID, id)
		}
		if seen[k.ID] {
			t.Errorf("key %s listed twice", k.ID)
		}
		seen[k.ID] = true
	}
}

func TestKeysReturnsACopy(t *testing.T) {
	ks := Keys()
	if len(ks) == 0 {
		t.Skip("no trusted keys in this build")
	}
	ks[0].ID = "changed"
	if Keys()[0].ID == "changed" {
		t.Error("Keys returned the package's own slice")
	}
}
```

Create `internal/update/trustedkeys/keys_production_test.go`:

```go
//go:build !harnesskeys

package trustedkeys

import "testing"

func TestProductionBuild(t *testing.T) {
	if TestBuild() {
		t.Error("TestBuild() = true in a build without the harnesskeys tag")
	}
	if got, want := BaseURL(), "https://github.com/Darkraise/darkpanel/releases/download"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
	for _, k := range Keys() {
		if k.ID == harnessKeyID {
			t.Errorf("the production build trusts the harness test key %s", k.ID)
		}
	}
}
```

Create `internal/update/trustedkeys/keys_harness_test.go`:

```go
//go:build harnesskeys

package trustedkeys

import (
	"fmt"
	"os"
	"testing"

	"aead.dev/minisign"
)

func TestHarnessBuild(t *testing.T) {
	if !TestBuild() {
		t.Error("TestBuild() = false in a harnesskeys build")
	}
	if got, want := BaseURL(), "http://198.18.0.20:8000"; got != want {
		t.Errorf("BaseURL() = %q, want %q", got, want)
	}
	text, err := os.ReadFile("testdata/harness.key")
	if err != nil {
		t.Fatal(err)
	}
	var priv minisign.PrivateKey
	if err := priv.UnmarshalText(text); err != nil {
		t.Fatalf("testdata/harness.key: %v", err)
	}
	pub, ok := priv.Public().(minisign.PublicKey)
	if !ok {
		t.Fatalf("public key of type %T", priv.Public())
	}
	ks := Keys()
	if len(ks) != 1 {
		t.Fatalf("Keys() = %v, want only the harness key", ks)
	}
	if got := (Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}); ks[0] != got || got.ID != harnessKeyID {
		t.Errorf("Keys()[0] = %+v, want the public half of testdata/harness.key %+v", ks[0], got)
	}
}
```

Write the harness private key with a trailing newline (the minisign library's own output has none; Go's base64 decoder skips newlines, and `TestHarnessBuild` proves the file parses):

```bash
mkdir -p internal/update/trustedkeys/testdata
printf '%s\n%s\n' 'untrusted comment: minisign encrypted secret key' 'RWQAAEIyAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAATmmv6bMKsbzjZkwst+H8/SaGvQSv+Tg9qUYNoNfpNtrmogEG8rwn47cQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvDAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=' > internal/update/trustedkeys/testdata/harness.key
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./internal/update/trustedkeys/`
Expected: FAIL, build failed with `undefined: Keys` (and `TestBuild`, `BaseURL`).

- [ ] **Step 3: Write the implementation**

Create `internal/update/trustedkeys/keys.go`:

```go
// Package trustedkeys lists the minisign public keys a release must be signed
// with (ADR 0007) and where releases are published. It holds data only: the
// updater and the install script generator read the same list, so the two can
// never trust different keys.
package trustedkeys

import "slices"

// Key is one trusted minisign public key.
type Key struct {
	// ID is the minisign key id as 16 upper-case hexadecimal digits,
	// zero-padded: the little-endian id bytes of PublicKey read as a uint64.
	// Release signature files are named checksums.txt.<ID>.minisig.
	ID string
	// PublicKey is the base64 minisign public key line, starting "RW".
	PublicKey string
}

// Keys returns the keys this build trusts. Any one valid signature from one
// of them accepts a release, so a key rotation signs with both keys for at
// least two minor releases (ADR 0007).
func Keys() []Key { return slices.Clone(keys) }

// TestBuild reports whether this build trusts the harness test key instead
// of the production keys; a release must never be such a build.
func TestBuild() bool { return testBuild }

// BaseURL is the URL under which the release index (index/releases.json) and
// each version's files (v<version>/) are published.
func BaseURL() string { return baseURL }
```

Create `internal/update/trustedkeys/production.go`:

```go
//go:build !harnesskeys

package trustedkeys

// keys is empty until the owner adds the production release key; until then
// every update and install.sh refuses to verify a release.
var keys = []Key{}

const (
	testBuild = false
	baseURL   = "https://github.com/Darkraise/darkpanel/releases/download"
)
```

Create `internal/update/trustedkeys/harness.go`:

```go
//go:build harnesskeys

package trustedkeys

// keys trusts only the harness test key, whose private half is committed as
// testdata/harness.key: anyone can sign for it, which is why only a build
// with the harnesskeys tag carries it.
var keys = []Key{
	{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"},
}

const (
	testBuild = true
	// baseURL is the scanner VM, which serves the harness's test releases
	// over plain HTTP; signatures, not the transport, make them trusted.
	baseURL = "http://198.18.0.20:8000"
)
```

- [ ] **Step 4: Run the tests in both builds**

Run: `GOTOOLCHAIN=local go test -count=1 -v ./internal/update/trustedkeys/`
Expected: `TestProductionBuild` and `TestKeysAreWellFormed` PASS, `TestKeysReturnsACopy` SKIP (no production key yet), `ok`.

Run: `GOTOOLCHAIN=local go test -count=1 -v -tags harnesskeys ./internal/update/trustedkeys/`
Expected: `TestHarnessBuild`, `TestKeysAreWellFormed` and `TestKeysReturnsACopy` PASS, `ok`.

- [ ] **Step 5: Vet, lint and check the module files**

Run: `GOTOOLCHAIN=local go vet ./internal/update/trustedkeys/ && GOTOOLCHAIN=local go vet -tags harnesskeys ./internal/update/trustedkeys/ && golangci-lint run --allow-serial-runners ./internal/update/... && golangci-lint run --allow-serial-runners --build-tags harnesskeys ./internal/update/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` twice and no other output. The harness test imports `aead.dev/minisign`, which Task 1 already requires, so `go.mod` does not change.

- [ ] **Step 6: Commit**

```bash
git add internal/update/trustedkeys
git commit -m "feat(update): add trusted release keys"
```

---

### Task 17: ADR 0012

**Files:**
- Create: `docs/adr/0012-m2-contract-and-layout-additions.md`
- Modify: `docs/adr/README.md`

**Interfaces:**
- Consumes: none
- Produces: C17 (ADR 0012, Proposed). It becomes Accepted when M2 closes, the rule ADRs 0010 and 0011 followed for M1.

**Items:** 22

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 0 - risk 0 = 1

`scripts/check-adrs.sh` requires the title line, a `**Requirements:**` line whose requirement ids are separate words (a range such as `R22–R36` would not match the traceability check, so every id is listed), and the five headings in order. With `--complete` it also requires the index to link every ADR and every traceability row's ADR to list that requirement.

- [ ] **Step 1: Run the check to see it fail**

Run: `bash scripts/check-adrs.sh docs/adr/0012-m2-contract-and-layout-additions.md`
Expected: exit 1 with `check-adrs: docs/adr/0012-m2-contract-and-layout-additions.md: no such file`.

- [ ] **Step 2: Write the ADR**

Create `docs/adr/0012-m2-contract-and-layout-additions.md`:

````markdown
# ADR 0012: M2 contract and layout additions

**Requirements:** R22, R23, R24, R25, R26, R27, R28, R29, R30, R31, R32, R33, R34, R35, R36, R80

## Status

Proposed

## Context

The M2 fleet and lifecycle design
(`docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`) makes
concrete several contracts that ADR 0002, ADR 0003, ADR 0004 and ADR 0007
state in outline, closes the index's deferred decision on how the install
script verifies a download, and adds packages that ADR 0001 and ADR 0011 do
not name. Accepted ADRs are not edited, so the additions are recorded here.

## Decision

- **Install script verification.** `install.sh` verifies a release with
  OpenSSL 3 alone, against the trusted public keys embedded in the script when
  it is generated from `install.sh.in`; it downloads nothing but the release
  files. It hashes `checksums.txt` with BLAKE2b-512, checks one minisign
  hashed-mode (`ED`) signature over that digest and the global signature over
  the signature and its trusted comment with `openssl pkeyutl -verify -rawin`,
  requires the trusted comment
  `version=<version> channel=<stable|beta> purpose=checksums` for the version
  being installed, then checks the tarball against `checksums.txt`. Any one
  valid signature from an embedded key suffices.
- **Signature files and release index.** Each signing key produces
  `checksums.txt.<key id>.minisig`, the key id written as 16 upper-case
  hexadecimal digits. A missing or failing signature file is one unsuccessful
  candidate, not an error. The rolling release `index` holds
  `releases.json`, `{"formatVersion": 1, "releases": [{"version", "channel"},
  …]}`, regenerated from every published release on each publication; it only
  selects versions, and trust comes from each version's own signatures.
- **Agent routes.** The agent API is the concrete form of ADR 0003's endpoint
  list. Under `/agent/v1/`: `GET hello`; `POST actions/{name}/{version}/read`;
  `GET resources/{module}/{resource}`; `POST actions/{name}/{version}/plan`;
  `POST plans/{id}/apply`; `POST changes/{id}/confirm`;
  `POST changes/{id}/revert`; `GET changes/{id}`; `GET changes` with the
  change filter as query parameters; `GET modules/{id}/preflight`;
  `GET journal?after=&limit=`; `POST journal/ack`. Bodies are explicit wire
  types with camelCase fields, UTC RFC 3339 times, durations in integer
  seconds and nullable fields as JSON `null`, limited to 4 MiB; errors are RFC
  9457 problem details. Frozen for protocol version 1: `hello`, the plan,
  apply and read routes of `core.update.apply/1`, `core.update.rollback/1` and
  `core.update.status/1`, the yamux framing and the headers below.
- **Headers.** The controller sends `Darkpanel-Principal` (the JSON of the
  calling principal) on every request, and on `GET hello` also
  `Darkpanel-Protocol`, `Darkpanel-Version` and `Darkpanel-Controller-Id`, so
  the hello runs both ways. The agent accepts `Darkpanel-Principal` only from
  the authenticated controller; on its local socket the principal is always
  `local:root@<host id>`.
- **Schema hashes.** A schema hash is the hex SHA-256 of a snapshot's `input`
  or `output` member re-encoded with Go's `encoding/json` `Marshal` after
  decoding it into `any`. Hello reports one input and output hash per
  agent-side action version, keyed `<name>@<version>`. The binary embeds
  `schemas/`, including released snapshots. Per action version the
  dispatcher sends as is when the hash equals its own; when the hash matches
  a released snapshot it validates the input against that older schema with
  unknown properties refused, then sends; an unknown hash makes the action
  version unavailable on that host.
- **Journal epoch.** The agent stores a ULID `journal_epoch`, replaced only
  when its database or journal table is recreated and kept across
  re-enrollment. The controller's journal cursor and audit rows carry the
  epoch; a new epoch restarts the host's cursor at zero, and the audit's
  uniqueness is `(host, epoch, seq)`.
- **Operation reservation.** `lifecycle.Reserve` takes the lock key `*` for
  an owner `op:<operation id>` in its own write transaction, refused while any
  lock is held or a revertible change is unconfirmed; while `*` is held, every
  other lock is refused. `lifecycle.Release` frees an owner's rows. The engine
  passes the running change's id to Apply and Verify as `Env.ChangeID`.
- **Update actions.** `core.update.apply/v1` (compensable, compensated by
  `core.update.rollback`), `core.update.rollback/v1` (irreversible) and
  `core.update.status/v1` (read) are agent-side core actions. Their inputs
  carry no URL or key: the agent builds every download URL from its embedded
  base and enforces the downgrade and one-minor stepping rules itself. Apply
  reserves `*`, records the transaction and starts a transient systemd unit
  that runs the update, then returns before any restart; the result is read
  through `core.update.status`, never through the change.
- **Update and rollout shapes.** The Go types are in `internal/core/fleet.go`;
  JSON fields are:
  - `core.update.apply/v1`: input `UpdateApplyInput` {`version`, `channel`,
    `force`}, output `UpdateTransaction`.
  - `core.update.rollback/v1`: input `UpdateRollbackInput` {`force`}, output
    `UpdateTransaction`.
  - `core.update.status/v1`: input `UpdateStatusInput` {`operationId`,
    `changeId`, both optional, at most one}, output `UpdateStatus`
    {`transactions`}.
  - `UpdateTransaction` is {`id`, `operationId`, `parentId`, `kind` (`update`,
    `rollback`), `fromVersion`, `toVersion`, `targetVersion`, `state`
    (`pending`, `downloading`, `staged`, `swapped`, `rolling-back`,
    `committed`, `rolled-back`, `failed`), `error` (`ErrorDetail` {`code`,
    `message`} or null), `startedAt`, `updatedAt`}.
  - `core.host.update-policy/v1`: input and output `UpdatePolicy` {`hostId`,
    `channel`, `schedule` (null or `UpdateSchedule` {`days`, `time`})}.
  - `core.update.rollout/v1`: input `RolloutInput` {`hosts`, `version`,
    `channel`}, output `Rollout`; `core.update.rollout-cancel/v1`: input
    `RolloutCancelInput` {`rolloutId`, `force`}, output `Rollout`;
    `core.update.rollouts/v1`: input `RolloutsInput` {`limit`}, output
    `RolloutList` {`rollouts`}.
  - `Rollout` is {`id`, `version`, `channel`, `state` (`running`, `draining`,
    `succeeded`, `failed`, `cancelled`), `error`, `createdAt`, `updatedAt`,
    `hosts`}, and each `RolloutHost` is {`hostId`, `position`, `state`
    (`waiting`, `updating`, `committed`, `failed`, `skipped`), `planId`,
    `operationId`, `error`}.
- **Update lock and rollback claim.** Every change to a transaction record
  and every rename in the binary directory happens under an exclusive `flock`
  on `update/lock`. A recover path that cannot take the lock within 5 s kills
  the runner's unit and takes it. A rollback first claims the record
  `swapped → rolling-back` under the lock; only the claimant proceeds.
- **Recovery executable.** ADR 0007's `darkpanel.prev update rollback
  --pending` takes the form `darkpanel.recovery update recover`:
  `darkpanel.recovery` is a hard link to the previous binary, made before the
  swap and removed when the operation is terminal, so a crash at any point
  leaves a binary that can finish the rollback. The rollback and boot
  recovery units run it.
- **Health rule.** After a swap the runner polls every 2 s until an 80 s
  deadline. The controller passes when `/healthz` on its socket reports the
  new version with status `ok`, and fails at once on the new version with any
  other status; the agent passes when its hello reports the new version and,
  when enrolled, a live controller connection. A refused connection, a timeout
  or the old version is "not yet"; the deadline is a failure.
- **Actors and reserved names.** `system:rollout` (the rollout driver),
  `system:update-schedule` (scheduled updates) and `local:root@unenrolled`
  (the agent socket before enrollment) join the shared contracts' actors.
  `system` joins the reserved top-level CLI names.
- **Dependency.** `github.com/hashicorp/yamux` (MPL-2.0, file-level copyleft)
  is accepted, used unmodified, keeping ADR 0003's frozen yamux framing.
- **Package layout.** Additions to ADR 0001 and ADR 0011:

```
internal/transport/             TLS policies, peer checks, yamux session, heartbeat
internal/agent/api/             agent API handler and client
internal/agent/wire/            agent API wire types
internal/agent/daemon/          dial loop, enrollment, renewal, credentials
internal/agent/socket/          the agent's local socket
internal/controller/fleet/      agent authority, enrollment, session registry
internal/controller/rollout/    fleet rollouts and the update scheduler
internal/install/               system install, units and uninstall
internal/update/                release verification, staging, swap, rollback
internal/update/trustedkeys/    trusted release keys and the release base URL
internal/platform/systemd.go    systemd over D-Bus
schemas/embed.go                the embedded schema snapshots
```

## Alternatives considered

- A pinned minisign binary or a required `minisign` package for the install
  script: rejected; OpenSSL 3 ships on every supported operating system.
- Sending only schema hashes and marking every mismatch unavailable:
  rejected; each additive field would break the previous-minor window (R25).
- Modelling an update as a change that completes after the restart: rejected;
  startup recovery would mark it interrupted.
- The GitHub API as the release source: rejected; unauthenticated rate
  limits and no harness equivalent.
- `xtaci/smux` (MIT) instead of yamux: rejected; ADR 0003 freezes yamux
  framing.

## Consequences

- The index's deferred decision on install-script verification is closed.
- The shared contracts' actor list in the index points to this ADR for the
  three additions.
- The frozen protocol-1 surface is the route table and headers above, so a
  change to them needs protocol version 2.
- Later epics place files by ADR 0001, ADR 0011 and this layout.
````

- [ ] **Step 3: Update the index**

Make these four edits to `docs/adr/README.md`.

In `## Index`, after the ADR 0011 entry, add:

```markdown
- [ADR 0012: M2 contract and layout
  additions](0012-m2-contract-and-layout-additions.md)
```

In `## Shared contracts`, replace the end of the **Actors** bullet:

```markdown
  `local:root@<host id>` and `system:<job>`; ADR 0011 adds `anonymous` and
  `local:uid-<n>@<host id>`.
```

with:

```markdown
  `local:root@<host id>` and `system:<job>`; ADR 0011 adds `anonymous` and
  `local:uid-<n>@<host id>`; ADR 0012 adds `system:rollout`,
  `system:update-schedule` and `local:root@unenrolled`.
```

In `## Deferred decisions`, delete these two lines (ADR 0012 decides the method):

```markdown
- How the install script verifies a download (it must not trust TLS alone):
  M2 install epic.
```

In `## Traceability`, replace three rows in place:

```markdown
| R24 | 0002 (schema hashes), 0003 (hello) |
| R29 | 0007 (release hosting; method deferred to the M2 install epic) |
| R33 | 0007 (update action, update transaction, rollback) |
```

with, respectively:

```markdown
| R24 | 0002 (schema hashes), 0003 (hello), 0012 (schema hashes, agent routes) |
| R29 | 0007 (release hosting), 0012 (install script verification) |
| R33 | 0007 (update action, update transaction, rollback), 0012 (update lock, rollback claim, health rule) |
```

and add a last row after `| R36 | 0007 (order and rollouts) |`:

```markdown
| R80 | 0012 (signature files, release index) |
```

- [ ] **Step 4: Run the checks**

Run: `bash scripts/check-adrs.sh docs/adr/0012-m2-contract-and-layout-additions.md && make test-adr`
Expected: exit 0; `check-adrs tests: 21 passed, 0 failed`, and `check-adrs.sh --complete` prints nothing.

- [ ] **Step 5: Commit**

```bash
git add docs/adr/0012-m2-contract-and-layout-additions.md docs/adr/README.md
git commit -m "docs(adr): add ADR 0012 for M2 contracts and layout"
```

---

### Task 18: Install script template and generator

**Files:**
- Create: `install.sh.in`
- Create: `scripts/gen-install-sh/main.go`
- Test: `scripts/gen-install-sh/main_test.go`

**Interfaces:**
- Consumes: C1 (`aead.dev/minisign`, test only), C15 (`trustedkeys.Keys`, `trustedkeys.BaseURL`), C16 (release file names, signature format, `releases.json` layout).
- Produces: C18 (`install.sh.in`, `scripts/gen-install-sh`, `Generate`). The release-workflow plan's `make install-sh` runs the generator; the install part commits the generated `install.sh` and adds its own fixture tests; the assembly part implements `darkpanel system install`, which the script runs.

**Depends on:** Task 16

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

The script verifies with OpenSSL alone (spec section 7, owner decision). Each embedded key is one word `<ID>:<id bytes as hex>:<DER public key, base64>` that the generator derives from `trustedkeys.Key`: the id names the signature file, the id bytes are compared with the bytes inside the signature, and the DER key (the 12-byte Ed25519 SubjectPublicKeyInfo prefix `302a300506032b6570032100` followed by the 32 key bytes) is wrapped as PEM for `openssl pkeyutl -verify -rawin`. The tests sign with keys generated per test, in minisign hashed mode through `minisign.NewReader(...).SignWithComments` (`minisign.Sign` would make a legacy `Ed` signature, which the script refuses). Tests that need `openssl`, `sha256sum`, `tar`, `base64`, `od` or `curl`/`wget` skip when a tool is missing; `shellcheck` runs when it is on `PATH`. Standard input of `darkpanel system install` follows C18: empty for the controller; the token file or the prompted token for an agent; empty for an agent with saved credentials (`<AgentTLSDir>/current` exists) and no `--token-file`, when the script also prints that a revoked agent needs `--token-file`. `--token-file` is checked in `main` before the OS check and before anything is downloaded: it must be owned by root (by the calling uid under `DARKPANEL_INSTALL_ROOT`) and have mode exactly 0600 or 0400. This task does not generate or commit `install.sh`.

- [ ] **Step 1: Write the failing tests**

Create `scripts/gen-install-sh/main_test.go`:

```go
package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strings"
	"testing"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const templatePath = "../../install.sh.in"

func readTemplate(t *testing.T) []byte {
	t.Helper()
	b, err := os.ReadFile(templatePath)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// signer is a minisign key pair generated for one test.
type signer struct {
	key  trustedkeys.Key
	priv minisign.PrivateKey
}

func newSigner(t *testing.T) signer {
	t.Helper()
	pub, priv, err := minisign.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return signer{trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}, priv}
}

// sign writes dir/checksums.txt.<id>.minisig over dir/checksums.txt in hashed
// mode with the release trusted comment.
func (s signer) sign(t *testing.T, dir, comment string) {
	t.Helper()
	msg := readFile(t, filepath.Join(dir, "checksums.txt"))
	r := minisign.NewReader(strings.NewReader(msg))
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	sig := r.SignWithComments(s.priv, comment, "signature from darkpanel test key")
	writeFile(t, filepath.Join(dir, "checksums.txt."+s.key.ID+".minisig"), string(sig))
}

// script writes install.sh generated with keys and returns its path.
func script(t *testing.T, keys []trustedkeys.Key, base string) string {
	t.Helper()
	b, err := Generate(readTemplate(t), keys, base)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "install.sh")
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func needTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s is not installed", tool)
		}
	}
}

var keysLine = regexp.MustCompile(`(?m)^KEYS='([^']*)'$`)

func TestGenerateEmbedsTheTrustedKeys(t *testing.T) {
	out, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil {
		t.Fatal(err)
	}
	m := keysLine.FindSubmatch(out)
	if m == nil {
		t.Fatal("install.sh has no KEYS line")
	}
	var want []string
	for _, k := range trustedkeys.Keys() {
		w, err := keyWord(k)
		if err != nil {
			t.Fatal(err)
		}
		want = append(want, w)
	}
	if got := strings.Fields(string(m[1])); !slices.Equal(got, want) {
		t.Errorf("embedded keys = %v, want %v", got, want)
	}
	if !bytes.Contains(out, []byte("DEFAULT_BASE_URL='"+trustedkeys.BaseURL()+"'")) {
		t.Errorf("install.sh does not embed the base URL %s", trustedkeys.BaseURL())
	}
	if bytes.Contains(out, []byte("@DARKPANEL_")) {
		t.Error("install.sh still holds a placeholder")
	}
	again, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil || !bytes.Equal(out, again) {
		t.Error("Generate is not deterministic")
	}
}

func TestGenerateRefuses(t *testing.T) {
	good := newSigner(t).key
	tests := []struct {
		name     string
		template string
		key      trustedkeys.Key
		base     string
	}{
		{"no keys placeholder", "B='@DARKPANEL_BASE_URL@'", good, "https://example.com"},
		{"no base placeholder", "K='@DARKPANEL_KEYS@'", good, "https://example.com"},
		{"id mismatch", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", trustedkeys.Key{ID: "0000000000000000", PublicKey: good.PublicKey}, "https://example.com"},
		{"not a key", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", trustedkeys.Key{ID: good.ID, PublicKey: "bm90IGEga2V5"}, "https://example.com"},
		{"quote in base", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", good, "https://example.com/'x"},
		{"not http", "K='@DARKPANEL_KEYS@' B='@DARKPANEL_BASE_URL@'", good, "ftp://example.com"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Generate([]byte(tt.template), []trustedkeys.Key{tt.key}, tt.base); err == nil {
				t.Error("Generate succeeded")
			}
		})
	}
}

func TestScriptSyntax(t *testing.T) {
	p := script(t, []trustedkeys.Key{newSigner(t).key}, "https://example.com/releases")
	for _, sh := range []string{"sh", "dash"} {
		if _, err := exec.LookPath(sh); err != nil {
			continue
		}
		if out, err := exec.Command(sh, "-n", p).CombinedOutput(); err != nil { //nolint:gosec // G204: a shell from a fixed list and a test file
			t.Errorf("%s -n: %v\n%s", sh, err, out)
		}
	}
	if _, err := exec.LookPath("shellcheck"); err != nil {
		t.Log("shellcheck is not installed; skipping the shellcheck run")
		return
	}
	if out, err := exec.Command("shellcheck", "-s", "sh", p).CombinedOutput(); err != nil { //nolint:gosec // G204: a test file
		t.Errorf("shellcheck: %v\n%s", err, out)
	}
}

// verify runs the script's verify_release on dir for version and reports
// whether it accepted the release.
func verify(t *testing.T, scriptPath, dir, version string) bool {
	t.Helper()
	cmd := exec.Command("sh", "-c", `. "$1"; verify_release "$2" "$3"`, "sh", scriptPath, dir, version) //nolint:gosec // G204: a fixed command over test files
	cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1")
	out, err := cmd.CombinedOutput()
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("run verify_release: %v\n%s", err, out)
	}
	return err == nil
}

func TestVerifyRelease(t *testing.T) {
	needTools(t, "sh", "openssl", "base64", "od")
	const comment = "version=1.4.0 channel=stable purpose=checksums"
	tests := []struct {
		name   string
		setup  func(t *testing.T, dir string, a, b signer)
		keys   func(a, b signer) []trustedkeys.Key
		accept bool
	}{
		{"valid", func(t *testing.T, dir string, a, _ signer) { a.sign(t, dir, comment) }, oneKey, true},
		{"beta channel", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.4.0 channel=beta purpose=checksums")
		}, oneKey, true},
		{"other version", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.3.0 channel=stable purpose=checksums")
		}, oneKey, false},
		{"other purpose", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, "version=1.4.0 channel=stable purpose=other")
		}, oneKey, false},
		{"only an unknown key signed", func(t *testing.T, dir string, _, b signer) { b.sign(t, dir, comment) }, oneKey, false},
		{"unknown key signed under a trusted id", func(t *testing.T, dir string, a, b signer) {
			b.sign(t, dir, comment)
			rename(t, filepath.Join(dir, "checksums.txt."+b.key.ID+".minisig"), filepath.Join(dir, "checksums.txt."+a.key.ID+".minisig"))
		}, oneKey, false},
		{"tampered checksums", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			appendFile(t, filepath.Join(dir, "checksums.txt"), "0000  evil.tar.gz\n")
		}, oneKey, false},
		{"bad global signature", func(t *testing.T, dir string, a, _ signer) {
			a.sign(t, dir, comment)
			p := filepath.Join(dir, "checksums.txt."+a.key.ID+".minisig")
			lines := strings.Split(readFile(t, p), "\n")
			lines[3] = strings.Repeat("A", 86) + "=="
			writeFile(t, p, strings.Join(lines, "\n"))
		}, oneKey, false},
		{"no signature files", func(*testing.T, string, signer, signer) {}, oneKey, false},
		{"second of two keys signed", func(t *testing.T, dir string, _, b signer) { b.sign(t, dir, comment) }, twoKeys, true},
		{"both keys signed", func(t *testing.T, dir string, a, b signer) {
			a.sign(t, dir, comment)
			b.sign(t, dir, comment)
		}, twoKeys, true},
		{"no keys embedded", func(t *testing.T, dir string, a, _ signer) { a.sign(t, dir, comment) }, noKeys, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, b := newSigner(t), newSigner(t)
			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "checksums.txt"), "abc  darkpanel_1.4.0_linux_amd64.tar.gz\n")
			tt.setup(t, dir, a, b)
			if got := verify(t, script(t, tt.keys(a, b), "https://example.com"), dir, "1.4.0"); got != tt.accept {
				t.Errorf("verify_release accepted = %v, want %v", got, tt.accept)
			}
		})
	}
}

func oneKey(a, _ signer) []trustedkeys.Key  { return []trustedkeys.Key{a.key} }
func twoKeys(a, b signer) []trustedkeys.Key { return []trustedkeys.Key{a.key, b.key} }
func noKeys(signer, signer) []trustedkeys.Key {
	return nil
}

func TestInstallAgentEndToEnd(t *testing.T) {
	needTools(t, "sh", "openssl", "base64", "od", "sha256sum", "tar", "stat")
	if _, err := exec.LookPath("curl"); err != nil {
		needTools(t, "wget")
	}
	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
		t.Skipf("install.sh supports amd64 and arm64, not %s", runtime.GOARCH)
	}
	const version = "1.10.0"
	s := newSigner(t)
	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$DARKPANEL_STUB_LOG.args\"\ncat >\"$DARKPANEL_STUB_LOG.stdin\"\n"
	tarball := "darkpanel_" + version + "_linux_" + runtime.GOARCH + ".tar.gz"
	release := t.TempDir()
	writeFile(t, filepath.Join(release, tarball), string(tarGz(t, map[string]string{"darkpanel": stub, "LICENSE": "license\n", "README.md": "readme\n"})))
	sum := sha256.Sum256([]byte(readFile(t, filepath.Join(release, tarball))))
	// The other architecture's tarball and the SBOM are listed but never fetched.
	writeFile(t, filepath.Join(release, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+tarball+"\n"+
		strings.Repeat("0", 64)+"  darkpanel_"+version+"_linux_other.tar.gz\n"+
		strings.Repeat("1", 64)+"  darkpanel_"+version+".cdx.json\n")
	s.sign(t, release, "version="+version+" channel=stable purpose=checksums")
	index := `{"formatVersion": 1, "releases": [
  {"version": "1.11.0-beta.1", "channel": "beta"},
  {"version": "1.2.0", "channel": "stable"},
  {"version": "1.10.0", "channel": "stable"}
]}`
	mux := http.NewServeMux()
	mux.HandleFunc("GET /index/releases.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(index)) })
	mux.Handle("GET /v"+version+"/", http.StripPrefix("/v"+version+"/", http.FileServer(http.Dir(release))))
	srv := httptest.NewServer(mux)
	defer srv.Close()

	root := t.TempDir()
	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
	tokenFile := filepath.Join(root, "token")
	writeFile(t, tokenFile, "dpe1.example-token\n")
	logBase := filepath.Join(root, "stub")
	run := func(args ...string) ([]byte, error) {
		cmd := exec.Command("sh", append([]string{script(t, []trustedkeys.Key{s.key}, srv.URL)}, args...)...) //nolint:gosec // G204: the generated test script
		cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_ROOT="+root, "DARKPANEL_STUB_LOG="+logBase)
		return cmd.CombinedOutput()
	}
	if out, err := run("--role", "agent", "--token-file", tokenFile, "--base-url", srv.URL); err != nil {
		t.Fatalf("install.sh: %v\n%s", err, out)
	}
	bin := filepath.Join(root, "usr", "local", "lib", "darkpanel", "darkpanel")
	if got := readFile(t, bin); got != stub {
		t.Errorf("installed binary = %q, want the stub", got)
	}
	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 {
		t.Errorf("installed binary mode = %v, %v; want 0755", info, err)
	}
	if target, err := os.Readlink(filepath.Join(root, "usr", "local", "bin", "darkpanel")); err != nil || target != bin {
		t.Errorf("link = %q, %v; want %s", target, err, bin)
	}
	if got := readFile(t, logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
		t.Errorf("darkpanel ran with %q, want system install --role agent", got)
	}
	if got := readFile(t, logBase+".stdin"); got != "dpe1.example-token\n" {
		t.Errorf("darkpanel read %q on standard input, want the token", got)
	}

	// With saved credentials and no token file, darkpanel gets an empty
	// standard input and the operator is told how to recover a revoked agent.
	writeFile(t, filepath.Join(root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
	out, err := run("--role", "agent")
	if err != nil {
		t.Fatalf("install.sh with saved credentials: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "rerun install.sh with --token-file") {
		t.Errorf("output %q does not say how to recover a revoked agent", out)
	}
	if got := readFile(t, logBase+".stdin"); got != "" {
		t.Errorf("darkpanel read %q on standard input, want nothing", got)
	}

	if err := os.Chmod(tokenFile, 0o644); err != nil { //nolint:gosec // G302: the test needs a token file others can read
		t.Fatal(err)
	}
	// A bad token file is refused before anything is downloaded.
	srv.Close()
	if out, err := run("--role", "agent", "--token-file", tokenFile); err == nil || !strings.Contains(string(out), "mode 0600 or 0400, not 0644") {
		t.Errorf("a world-readable token file: err %v, output %s", err, out)
	}
	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"20.04\"\nPRETTY_NAME=\"Ubuntu 20.04.6 LTS\"\n")
	if out, err := run("--role", "controller"); err == nil || !strings.Contains(string(out), "this host is Ubuntu 20.04.6 LTS") {
		t.Errorf("an unsupported OS: err %v, output %s", err, out)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	for _, name := range names {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(files[name])), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(files[name])); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func readFile(t *testing.T, p string) string {
	t.Helper()
	b, err := os.ReadFile(p) //nolint:gosec // G304: a test file
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func writeFile(t *testing.T, p, s string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(s), 0o600); err != nil {
		t.Fatal(err)
	}
}

func appendFile(t *testing.T, p, s string) {
	t.Helper()
	writeFile(t, p, readFile(t, p)+s)
}

func rename(t *testing.T, from, to string) {
	t.Helper()
	if err := os.Rename(from, to); err != nil {
		t.Fatal(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `GOTOOLCHAIN=local go test ./scripts/gen-install-sh/`
Expected: FAIL, build failed with `undefined: Generate` and `undefined: keyWord`.

- [ ] **Step 3: Write the template**

Create `install.sh.in` at the repository root (tabs for indentation, as written):

```sh
#!/bin/sh
# Darkpanel install script. install.sh is generated from install.sh.in by
# scripts/gen-install-sh, which embeds the trusted release keys; edit the
# template, never install.sh.
#
# Usage:
#   sudo sh install.sh --role controller [--version X.Y.Z] [--base-url URL]
#       [--agent-listen ADDR:PORT]... [--agent-advertise HOST:PORT]
#   sudo sh install.sh --role agent [--version X.Y.Z] [--base-url URL]
#       [--token-file PATH]
#
# The script trusts no transport: it installs a release only when one of the
# embedded keys signed its checksums.txt for exactly that version, so
# --base-url may point at any mirror.
#
# Test hooks:
#   DARKPANEL_INSTALL_SH_LIB=1 defines the functions and runs nothing.
#   DARKPANEL_INSTALL_ROOT=<dir> prefixes every path the script reads or
#   writes under /etc and /usr/local, and only then skips the root check. That
#   is safe because the variable only narrows where the script writes: a
#   caller who is not root can write only what it could write anyway, and sudo
#   does not pass the variable through by default.
set -eu

KEYS='@DARKPANEL_KEYS@'
DEFAULT_BASE_URL='@DARKPANEL_BASE_URL@'

ROOT=${DARKPANEL_INSTALL_ROOT:-}
VERSION_RE='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'

die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

# os_value prints a key's value from os-release, without quotes.
os_value() {
	sed -n "s/^$1=//p" "$ROOT/etc/os-release" | head -n 1 | tr -d '"'
}

check_os() {
	[ -r "$ROOT/etc/os-release" ] || die "cannot read /etc/os-release"
	id=$(os_value ID)
	version_id=$(os_value VERSION_ID)
	case "$id $version_id" in
	"ubuntu 22.04" | "ubuntu 24.04" | "debian 12" | "debian 13") ;;
	*) die "darkpanel supports Ubuntu 22.04 and 24.04 and Debian 12 and 13; this host is $(os_value PRETTY_NAME)" ;;
	esac
}

detect_arch() {
	case "$(uname -m)" in
	x86_64) echo amd64 ;;
	aarch64) echo arm64 ;;
	*) die "darkpanel supports x86_64 and aarch64; this host is $(uname -m)" ;;
	esac
}

# fetch downloads $1 to $2 and fails when the server has no such file.
fetch() {
	if command -v curl >/dev/null 2>&1; then
		curl -fsSL -o "$2" "$1"
	else
		wget -q -O "$2" "$1"
	fi
}

# newest_stable prints the newest stable version listed in the release index
# $1. Every release object is {"version": ..., "channel": ...} in that order,
# so removing whitespace leaves one fixed pattern per release.
newest_stable() {
	tr -d ' \t\r\n' <"$1" |
		grep -o '{"version":"[^"]*","channel":"stable"}' |
		sed 's/^{"version":"\([^"]*\)".*/\1/' |
		grep -Ev -- '-' |
		sort -t . -k 1,1n -k 2,2n -k 3,3n |
		tail -n 1
}

# hex prints its input as lower-case hexadecimal.
hex() {
	od -An -tx1 -v | tr -d ' \n'
}

# verify_release succeeds when one embedded key signed $1/checksums.txt for
# version $2: a minisign hashed-mode signature (algorithm ED) over the
# BLAKE2b-512 digest of the file, and the global signature over that signature
# and its trusted comment, checked with OpenSSL alone. A missing or bad
# signature file is one failed candidate, not an error.
verify_release() {
	vdir=$1
	vversion=$2
	vtmp=$(mktemp -d)
	verified=1
	openssl dgst -blake2b512 -binary "$vdir/checksums.txt" >"$vtmp/digest"
	for key in $KEYS; do
		kid=${key%%:*}
		rest=${key#*:}
		kle=${rest%%:*}
		kder=${rest#*:}
		sig="$vdir/checksums.txt.$kid.minisig"
		[ -f "$sig" ] || continue
		comment=$(sed -n 3p "$sig")
		case "$comment" in
		"trusted comment: version=$vversion channel=stable purpose=checksums") ;;
		"trusted comment: version=$vversion channel=beta purpose=checksums") ;;
		*) continue ;;
		esac
		sed -n 2p "$sig" | base64 -d >"$vtmp/sig" 2>/dev/null || continue
		[ "$(wc -c <"$vtmp/sig" | tr -d ' ')" = 74 ] || continue
		[ "$(head -c 2 "$vtmp/sig")" = ED ] || continue
		[ "$(head -c 10 "$vtmp/sig" | tail -c 8 | hex)" = "$kle" ] || continue
		tail -c 64 "$vtmp/sig" >"$vtmp/signature"
		printf '%s\n' '-----BEGIN PUBLIC KEY-----' "$kder" '-----END PUBLIC KEY-----' >"$vtmp/key.pem"
		openssl pkeyutl -verify -pubin -inkey "$vtmp/key.pem" -rawin \
			-in "$vtmp/digest" -sigfile "$vtmp/signature" >/dev/null 2>&1 || continue
		sed -n 4p "$sig" | base64 -d >"$vtmp/global" 2>/dev/null || continue
		{
			cat "$vtmp/signature"
			printf '%s' "${comment#trusted comment: }"
		} >"$vtmp/signed"
		openssl pkeyutl -verify -pubin -inkey "$vtmp/key.pem" -rawin \
			-in "$vtmp/signed" -sigfile "$vtmp/global" >/dev/null 2>&1 || continue
		verified=0
		break
	done
	rm -rf "$vtmp"
	return "$verified"
}

# verify_checksum checks the tarball $2 in $1 against its one line of the
# verified checksums.txt.
verify_checksum() {
	awk -v f="$2" '$2 == f || $2 == "*" f' "$1/checksums.txt" >"$1/tarball.sha256"
	[ "$(wc -l <"$1/tarball.sha256" | tr -d ' ')" = 1 ] || die "checksums.txt has no single line for $2"
	(cd "$1" && sha256sum --status -c tarball.sha256) || die "$2 does not match checksums.txt"
}

# install_binary extracts the binary from the tarball $1 into the binary
# directory and points the link at it. The copy is synced before the rename,
# so a crash leaves the old binary or the complete new one.
install_binary() {
	bindir="$ROOT/usr/local/lib/darkpanel"
	mkdir -p "$tmp/extract"
	tar -xzf "$1" -C "$tmp/extract" darkpanel
	mkdir -p "$bindir"
	chmod 0755 "$bindir"
	cp "$tmp/extract/darkpanel" "$bindir/darkpanel.new-install"
	chmod 0755 "$bindir/darkpanel.new-install"
	sync "$bindir/darkpanel.new-install"
	mv -f "$bindir/darkpanel.new-install" "$bindir/darkpanel"
	sync "$bindir"
	mkdir -p "$ROOT/usr/local/bin"
	ln -sfn "$bindir/darkpanel" "$ROOT/usr/local/bin/darkpanel"
}

# check_token_file refuses a token file other users could read or replace.
check_token_file() {
	[ -f "$1" ] || die "token file $1 does not exist"
	owner=$(stat -c %u "$1")
	mode=$(stat -c %a "$1")
	if [ -z "$ROOT" ]; then
		[ "$owner" = 0 ] || die "token file $1 must be owned by root (uid 0), not uid $owner"
	else
		want=$(id -u)
		[ "$owner" = "$want" ] || die "token file $1 must be owned by uid $want, the caller, not uid $owner"
	fi
	case "$mode" in
	600 | 400) ;;
	*) die "token file $1 must have mode 0600 or 0400, not 0$mode" ;;
	esac
}

# read_token prompts for the enrollment token on the terminal without echo.
read_token() {
	[ -r /dev/tty ] && [ -w /dev/tty ] || die "no terminal to read the enrollment token from; pass --token-file"
	printf 'Enrollment token: ' >/dev/tty
	stty -echo </dev/tty
	trap 'stty echo </dev/tty' INT TERM
	IFS= read -r token </dev/tty || token=
	stty echo </dev/tty
	trap - INT TERM
	printf '\n' >/dev/tty
	[ -n "$token" ] || die "no enrollment token entered"
}

usage() {
	die "usage: install.sh --role controller|agent [--version X.Y.Z] [--base-url URL] [--token-file PATH] [--agent-listen ADDR:PORT]... [--agent-advertise HOST:PORT]"
}

# check_address refuses an address that is not a plain host:port, since
# addresses are passed on as separate words.
check_address() {
	printf '%s' "$1" | grep -Eq '^[][A-Za-z0-9.:-]+$' || die "address $1 is not host:port"
}

main() {
	role=''
	version=''
	base_url=''
	token_file=''
	listen_args=''
	while [ $# -gt 0 ]; do
		[ $# -ge 2 ] || usage
		case "$1" in
		--role) role=$2 ;;
		--version) version=$2 ;;
		--base-url) base_url=$2 ;;
		--token-file) token_file=$2 ;;
		--agent-listen)
			check_address "$2"
			listen_args="$listen_args --agent-listen $2"
			;;
		--agent-advertise)
			check_address "$2"
			listen_args="$listen_args --agent-advertise $2"
			;;
		*) usage ;;
		esac
		shift 2
	done
	case "$role" in
	controller) [ -z "$token_file" ] || die "--token-file is for --role agent" ;;
	agent) [ -z "$listen_args" ] || die "--agent-listen and --agent-advertise are for --role controller" ;;
	*) usage ;;
	esac
	[ -n "$ROOT" ] || [ "$(id -u)" = 0 ] || die "run as root: sudo sh install.sh ..."
	# Checked before anything is downloaded, so a bad token file costs nothing.
	[ -z "$token_file" ] || check_token_file "$token_file"
	[ -n "$KEYS" ] || die "this install.sh embeds no trusted release keys and cannot verify a release"
	check_os
	arch=$(detect_arch)
	for tool in openssl sha256sum tar base64 od; do
		command -v "$tool" >/dev/null 2>&1 || die "$tool is required"
	done
	command -v curl >/dev/null 2>&1 || command -v wget >/dev/null 2>&1 || die "curl or wget is required"

	base=${base_url:-$DEFAULT_BASE_URL}
	base=${base%/}
	case "$base" in
	https://* | http://*) ;;
	*) die "--base-url must start with https:// or http://" ;;
	esac

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	if [ -z "$version" ]; then
		fetch "$base/index/releases.json" "$tmp/releases.json" || die "cannot download $base/index/releases.json"
		version=$(newest_stable "$tmp/releases.json")
		[ -n "$version" ] || die "the release index lists no stable release"
	fi
	printf '%s' "$version" | grep -Eq "$VERSION_RE" || die "version $version is not a semantic version"

	tarball="darkpanel_${version}_linux_${arch}.tar.gz"
	echo "Downloading darkpanel $version for linux/$arch from $base"
	fetch "$base/v$version/$tarball" "$tmp/$tarball" || die "cannot download $tarball"
	fetch "$base/v$version/checksums.txt" "$tmp/checksums.txt" || die "cannot download checksums.txt"
	for key in $KEYS; do
		kid=${key%%:*}
		fetch "$base/v$version/checksums.txt.$kid.minisig" "$tmp/checksums.txt.$kid.minisig" 2>/dev/null ||
			rm -f "$tmp/checksums.txt.$kid.minisig"
	done
	verify_release "$tmp" "$version" || die "no trusted key signed checksums.txt of version $version"
	verify_checksum "$tmp" "$tarball"
	install_binary "$tmp/$tarball"
	echo "Installed darkpanel $version"

	bin="$ROOT/usr/local/lib/darkpanel/darkpanel"
	if [ "$role" = controller ]; then
		set -f
		# shellcheck disable=SC2086 # listen_args holds checked words
		"$bin" system install --role controller $listen_args </dev/null
		set +f
	elif [ -n "$token_file" ]; then
		"$bin" system install --role agent <"$token_file"
	elif [ -e "$ROOT/etc/darkpanel/agent/current" ]; then
		echo "Saved agent credentials found; installing without an enrollment token."
		echo "If this agent was revoked, rerun install.sh with --token-file."
		"$bin" system install --role agent </dev/null
	else
		read_token
		printf '%s\n' "$token" | "$bin" system install --role agent
	fi
}

if [ "${DARKPANEL_INSTALL_SH_LIB:-}" != 1 ]; then
	main "$@"
fi
```

- [ ] **Step 4: Write the generator**

Create `scripts/gen-install-sh/main.go`:

```go
// Command gen-install-sh renders install.sh from install.sh.in, embedding the
// trusted release keys and the release base URL of this build, so the script
// and the updater always trust the same keys. Build it with -tags harnesskeys
// for the harness's install.sh.
package main

import (
	"bytes"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

const (
	keysPlaceholder = "@DARKPANEL_KEYS@"
	basePlaceholder = "@DARKPANEL_BASE_URL@"
)

// ed25519SPKIPrefix is the DER SubjectPublicKeyInfo header of an Ed25519
// key; followed by the 32 key bytes it is what OpenSSL reads as a public key.
var ed25519SPKIPrefix = []byte{0x30, 0x2a, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x03, 0x21, 0x00}

// The base URL lands inside single quotes in the script.
var baseURLPattern = regexp.MustCompile(`^https?://[A-Za-z0-9.:/_~-]+$`)

func main() {
	in := flag.String("in", "install.sh.in", "the template to render")
	out := flag.String("out", "install.sh", "the script to write")
	flag.Parse()
	if err := run(*in, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen-install-sh:", err)
		os.Exit(1)
	}
}

func run(in, out string) error {
	template, err := os.ReadFile(in) //nolint:gosec // G304: the template path is the caller's argument
	if err != nil {
		return err
	}
	script, err := Generate(template, trustedkeys.Keys(), trustedkeys.BaseURL())
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, script, 0o755); err != nil { //nolint:gosec // G306: install.sh is an executable script
		return err
	}
	// WriteFile keeps the mode of an existing file.
	return os.Chmod(out, 0o755) //nolint:gosec // G302: install.sh is an executable script
}

// Generate replaces the template's placeholders with one word per key and
// the base URL. Its output depends only on its arguments, so a regenerated
// install.sh can be compared with the committed one.
func Generate(template []byte, keys []trustedkeys.Key, baseURL string) ([]byte, error) {
	for _, p := range []string{keysPlaceholder, basePlaceholder} {
		if !bytes.Contains(template, []byte(p)) {
			return nil, fmt.Errorf("template has no %s", p)
		}
	}
	if !baseURLPattern.MatchString(baseURL) {
		return nil, fmt.Errorf("base URL %q must match %s", baseURL, baseURLPattern)
	}
	words := make([]string, 0, len(keys))
	for _, k := range keys {
		w, err := keyWord(k)
		if err != nil {
			return nil, err
		}
		words = append(words, w)
	}
	script := bytes.ReplaceAll(template, []byte(keysPlaceholder), []byte(strings.Join(words, " ")))
	return bytes.ReplaceAll(script, []byte(basePlaceholder), []byte(baseURL)), nil
}

// keyWord is "<ID>:<id bytes as hex>:<DER public key, base64>", the form the
// script splits: the id names the signature file, the id bytes are compared
// with the signature's, and the DER key is what OpenSSL verifies with.
func keyWord(k trustedkeys.Key) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(k.PublicKey)
	if err != nil || len(raw) != 42 || string(raw[:2]) != "Ed" {
		return "", fmt.Errorf("key %s: %q is not a minisign Ed25519 public key", k.ID, k.PublicKey)
	}
	if id := fmt.Sprintf("%016X", binary.LittleEndian.Uint64(raw[2:10])); id != k.ID {
		return "", fmt.Errorf("key %s: its public key carries id %s", k.ID, id)
	}
	der := append(append([]byte{}, ed25519SPKIPrefix...), raw[10:]...)
	return k.ID + ":" + hex.EncodeToString(raw[2:10]) + ":" + base64.StdEncoding.EncodeToString(der), nil
}
```

- [ ] **Step 5: Run the tests to verify they pass**

Run: `GOTOOLCHAIN=local go test -count=1 -v ./scripts/gen-install-sh/`
Expected: `TestGenerateEmbedsTheTrustedKeys`, `TestGenerateRefuses` (6 subtests), `TestScriptSyntax`, `TestVerifyRelease` (12 subtests) and `TestInstallAgentEndToEnd` PASS; without shellcheck installed `TestScriptSyntax` logs `shellcheck is not installed; skipping the shellcheck run`.

Run: `GOTOOLCHAIN=local go test -count=1 -tags harnesskeys ./scripts/gen-install-sh/`
Expected: `ok` (the embedded-keys test now compares against the harness key).

Run: `dash -n install.sh.in && GOTOOLCHAIN=local go run ./scripts/gen-install-sh -out "$(mktemp -d)/install.sh"`
Expected: no output, exit 0. Do not run the generator with the default `-out`: `install.sh` is committed by the install part, not here.

- [ ] **Step 6: Vet, lint and check the module files**

Run: `GOTOOLCHAIN=local go vet ./scripts/gen-install-sh/ && GOTOOLCHAIN=local go vet -tags harnesskeys ./scripts/gen-install-sh/ && golangci-lint run --allow-serial-runners ./scripts/... && golangci-lint run --allow-serial-runners --build-tags harnesskeys ./scripts/... && GOTOOLCHAIN=local go mod tidy -diff`
Expected: `0 issues.` twice and no other output.

- [ ] **Step 7: Commit**

```bash
git add install.sh.in scripts/gen-install-sh/main.go scripts/gen-install-sh/main_test.go
git commit -m "feat(install): add install.sh template and generator"
```

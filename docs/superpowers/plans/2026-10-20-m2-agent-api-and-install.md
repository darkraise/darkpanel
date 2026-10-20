# M2 Agent API and Install Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build M2 plan 3, parts P3 (agent API) and P4 (install): the agent API handler, client and socket rules in `internal/agent/api`; and system install, the unit templates, uninstall, the widened ownership manifest and the hardened `install.sh` in `internal/install`, `internal/platform` and the repository root. Wave 2 parts P8 and P10 and the assembly part P12 consume these packages.

**Architecture:** `internal/agent/api` is one `http.Handler` over a `core.AgentClient` (the agent runtime), built twice: `NewHandler` for transport streams, where the controller names the principal in a header, and `NewSocketHandler` for the agent socket, where the caller is always local root and mutations wait for enrollment. Its `Client` implements `core.AgentClient` over any dialer, one stream per request. `internal/install` derives every checkpoint from the host: `Install` creates directories, the service user, role keys, agent.db with the core's ownership entries, and the units, then starts and enrolls; `PlanUninstall` reads the manifest into memory and removes it in a fixed order. The agent migration `0005_ownership_kinds.sql` lets the manifest hold directories, users and units, which `platform.Files` ignores.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `net/http` (`ServeMux` method patterns), `text/template` and `embed`, `golang.org/x/sys/unix` (flock), `modernc.org/sqlite` through `internal/core/store`, POSIX `sh` with OpenSSL 3, `aead.dev/minisign` v0.3.0 (tests only).

**Spec:** docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves

**Plan review:** 2026-10-20 — dr-superpowers:judge-opus — executability 17 / coherence 17 / coverage 17 / assumptions 14 (round 1)

**Parallelism:** waves — 1-2 | 3-4 | 5 | 6 | 7 | 8 | 9

**Integration check:** `GOTOOLCHAIN=local go build ./... && GOTOOLCHAIN=local go test -count=1 ./... && GOTOOLCHAIN=local go vet ./... && golangci-lint run --allow-serial-runners && GOTOOLCHAIN=local go mod tidy -diff && shellcheck release/*.sh install.sh`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is a small tmpfs that parallel Go builds fill: every `go` command, `golangci-lint` and `make` run with `TMPDIR` set to a directory on disk, `TMPDIR=/root/.cache/plan3-tmp/<task number>` (create it with `mkdir -p` and empty it when the task ends).
- `/nonexistent` exists on this host. A test that needs a missing path uses `filepath.Join(t.TempDir(), "missing")`, never a fixed name.
- Dependencies: no task edits `go.mod` or `go.sum`; every module this plan imports is already required. After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing.
- Files: a task touches only the files its **Files:** block names. The plan as a whole touches only `internal/agent/api/`, `internal/install/`, `internal/platform/files.go`, `internal/platform/files_test.go`, `internal/platform/ownership_test.go`, `internal/core/store/migrations/agent/0005_ownership_kinds.sql`, `install.sh.in`, `install.sh` and `scripts/gen-install-sh/main_test.go`.
- JSON fields are camelCase. The rebuilt table stays `STRICT` and keeps the fixed-width RFC 3339 UTC `created_at` text M1 uses.
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix (`api:`, `install:`; `Uninstall.Run` errors start `uninstall:`); a typed error built with `errcode.New` or `errcode.Wrap` carries its code instead.
- Nothing in this plan talks to the real systemd, D-Bus, `useradd`, `userdel`, `/etc`, `/var/lib` or `/usr/local`: systemd is a fake implementing `platform.Systemd`, commands go through a fake `core.Runner`, users through a fake `install.Users`, ownership changes through an injected `Chown`, every install path comes from `install.Rooted(t.TempDir())`, `install.sh` runs with `DARKPANEL_INSTALL_ROOT` set to a test directory, and servers are `httptest` servers or listeners on `127.0.0.1:0`. Tasks of one wave therefore share no port, path or global state.
- Tasks of one wave never share a Go package: wave 1-2 and wave 3-4 each pair `internal/install` with `internal/agent/api`. Within a package, each task's **Interfaces:** block lists the top-level names it adds, and no later task redeclares one.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean, and `install.sh` passes `shellcheck -s sh`. `--allow-serial-runners` is required because golangci-lint holds a shared lock and tasks of one wave may lint together.
- Each task of a wave runs in its own worktree from the wave's base, so a package-wide command sees only merged work plus the task's own files.
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.

## Contracts

Every name below is produced by exactly one task and consumed as written. Later M2 parts (P8 fleet service, P10 agent daemon, P12 assembly) consume them too. Foundation names (`core.OwnedDir`, `core.OwnedUser`, `core.OwnedUnit`, `core.Uninstaller`, `core.ActorUnenrolled`, `wire.Header*`, `wire.MaxBody`, `paths.Paths` and its methods, `update.ControllerUnit`, `update.AgentUnit`, `update.RollbackUnit`, `errcode.AgentNotEnrolled`, `errcode.EnrollTokenInvalid`) and the names of plans 2 and 4 (`transport.*`, the Makefile targets, `release/sign.sh`) are cited in Assumptions with their lines.

**C1. Install layout (Task 1), `internal/install/layout.go`.**

```go
type Role string
const (
	RoleController Role = "controller"
	RoleAgent      Role = "agent"
)
func ParseRole(s string) (Role, error) // error text: install: role "<s>": want controller or agent

const ServiceUser = "darkpanel"
const RecoverUnit = "darkpanel-update-recover.service"
const (
	DropInDir  = "darkpanel-controller.service.d"
	DropInName = "10-agent.conf"
)
// AgentLockName is the file in Paths.AgentDir the agent daemon (P10) holds an
// exclusive flock on (LOCK_EX) for as long as it runs; uninstall takes it
// with LOCK_NB and refuses while it is held.
const AgentLockName = "agent.lock" // P10's duty: register row 67

type Layout struct {
	Paths     paths.Paths
	DataDir   string // /var/lib/darkpanel
	ConfigDir string // /etc/darkpanel
	UnitDir   string // /etc/systemd/system
}
func Production() Layout
func Rooted(root string) Layout                 // Production with every path, Paths fields included, under root
func (l Layout) UnitPath(unit string) string    // UnitDir/<unit>
func (l Layout) DropInPath() string             // UnitDir/darkpanel-controller.service.d/10-agent.conf
func OwnedPaths(l Layout, role Role) []core.OwnedPath
func CoreOwnedPaths() []core.OwnedPath          // OwnedPaths(Production(), RoleController)
```

`OwnedPaths` lists, in this order, for both roles: the agent, rollback and recover units (`unit`), `BinaryDir` (`dir`), `BinaryLink` (`file`), `DataDir`, `ConfigDir`, `AgentDir`, `UpdateDir`, `AgentTLSDir` (`dir`), `AgentKey` (`file`); the controller role adds the controller unit (`unit`), the drop-in directory (`dir`), the drop-in (`unit`), `ControllerDir` (`dir`), `ControllerKey` (`file`) and the user `darkpanel` (`user`, `Path` is the user name). P12 registers `CoreOwnedPaths()` as the core module's `OwnedPaths`.

**C2. Agent API handler (Task 2), `internal/agent/api/handler.go`.**

```go
const prefix = "/agent/v1/"
const problemContentType = "application/problem+json"
const (
	opHello = "hello"; opRead = "read"; opResource = "resource"; opPlan = "plan"
	opApply = "apply"; opConfirm = "confirm"; opRevert = "revert"; opChange = "change"
	opChanges = "changes"; opPreflight = "preflight"; opJournal = "journal"; opAck = "ack"
)
type ControllerHello struct {
	Protocol     int
	Version      string
	ControllerID string
}
type Config struct {
	Agent   core.AgentClient // the agent runtime
	Log     *slog.Logger
	OnHello func(ControllerHello) // transport streams only; nil ignores the controller's hello
}
func NewHandler(cfg Config) http.Handler

// The three hooks are what Task 6's socket handler replaces.
type handler struct {
	cfg       Config
	principal func(r *http.Request) (core.Principal, error)
	allow     func(ctx context.Context, op, name string) error // name: the action or "<module>.<resource>", else ""
	hello     func(r *http.Request) error
}
func (h *handler) routes(journal bool) *http.ServeMux
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}
```

The routes are the spec's section 5 table under `/agent/v1/`: `GET hello`; `POST actions/{name}/{version}/read`; `GET resources/{module}/{resource}`; `POST actions/{name}/{version}/plan`; `POST plans/{id}/apply`; `POST changes/{id}/confirm`; `POST changes/{id}/revert`; `GET changes/{id}`; `GET changes` with the `ChangeFilter` query of `wire.ParseChangeFilter`; `GET modules/{id}/preflight`; `GET journal?after=&limit=`; `POST journal/ack` with `{"seq": n}`, answering 204. `{version}` is `v<n>`. Rules:

- `Darkpanel-Principal` (the JSON of `wire.Principal`, non-empty `actor`) is required on read, resource, plan, apply, confirm and revert, the routes whose `core.AgentClient` method takes a principal, and ignored elsewhere; a missing or bad header answers `request.invalid_input`. The controller sends it on every request (C4).
- On a transport stream `GET hello` requires `Darkpanel-Protocol` (a positive integer), `Darkpanel-Version` and `Darkpanel-Controller-Id`, else `request.invalid_input`, and passes them to `OnHello`.
- Request bodies are `application/json` (else `request.unsupported_media_type`), at most `wire.MaxBody` bytes (else `request.too_large`), one non-empty JSON value (else `request.invalid_json`). Unknown members of `ApplyOptions` and `Ack` are ignored, so a controller one minor newer may send members this agent does not know.
- Errors are problem details (`problem`, `Content-Type: application/problem+json`, `type` `about:blank`). A code outside the registry is answered as `internal`; a 5xx answer's `detail` is its title and carries no field errors, and the cause is logged. An unknown route answers `not_found`.
- Action and resource outputs pass through as JSON; no output is `null`.

**C3. Unit templates (Task 3), `internal/install/units.go` and `internal/install/units/`.**

```go
type UnitFile struct {
	Path    string
	Content []byte
}
// CheckAgentAddress refuses a value that is not host:port over [][A-Za-z0-9.:-]
// with a port 1-65535: install: agent address "<s>" is not host:port.
func CheckAgentAddress(s string) error
// Units renders the agent, rollback and recover units, and for the controller
// role the controller unit and the drop-in; the flags are for the controller
// role only.
func Units(l Layout, role Role, agentListen []string, agentAdvertise string) ([]UnitFile, error)
// EnabledUnits: controller role [controller, agent, recover]; agent role [agent, recover].
func EnabledUnits(role Role) []string
// writeIfChanged writes data with mode 0644 through a synced temporary file
// and rename unless the file holds exactly data, and reports whether it wrote.
func writeIfChanged(path string, data []byte) (bool, error)
func syncDir(dir string) error
```

Unit contents are spec section 7's, with `ExecStart` from `Paths.Binary()` (`serve`, `agent run`) and `Paths.RecoveryBinary()`. Two decisions P12 implements (register row 68): the rollback unit runs `darkpanel.recovery update recover` (mode `update.RecoverFailure`) and the boot recover unit runs `darkpanel.recovery update recover --boot` (mode `update.RecoverBoot`, which restarts nothing). The agent unit adds `RuntimeDirectoryMode=0700`, the mode spec section 1 gives the socket directory. The drop-in holds `ExecStart=` followed by `ExecStart=<binary> serve` with each `--agent-listen <addr>` and the optional `--agent-advertise <addr>`.

**C4. Agent API client (Task 4), `internal/agent/api/client.go`.**

```go
type ClientConfig struct {
	Dial       func(ctx context.Context) (net.Conn, error) // transport.Session.Open, or a dial of AgentSocket
	Controller *ControllerHello                             // nil sends no hello headers (a socket client)
}
type Client struct{ /* unexported */ }
var _ core.AgentClient = (*Client)(nil)
func NewClient(cfg ClientConfig) *Client
```

The client sends `Darkpanel-Protocol` as `agentruntime.ProtocolVersion` whatever `Controller.Protocol` holds. A client with a `Controller` sends `Darkpanel-Principal` on every request, as ADR 0012 freezes: the caller's principal where the method takes one, else `controllerPrincipal`, `core.Principal{Actor: "system:controller", Scope: core.ScopeAdmin}` (register row 69); a client without one (the socket's) sends only the caller's. It disables keep-alives, so every request opens one stream and closes it; it sets no timeout of its own, so callers bound requests with their context. An answer in problem details becomes `*errcode.Error{Code, Detail, Fields}` with the agent's code kept even when this binary does not know it; any other non-2xx answer is a plain `api:` error naming the status; an answer above `wire.MaxBody` is refused. `Resource` takes `"<module>.<resource>"` and answers `not_found` for a name without a dot.

**C5. Ownership kinds (Task 5).** `internal/core/store/migrations/agent/0005_ownership_kinds.sql` rebuilds `core_owned_files` with `kind IN ('file', 'block', 'dir', 'user', 'unit')` and `CHECK ((kind = 'block') = (block_id <> ''))`, keeping every row, the primary key `(path, module, block_id)` and `STRICT`. `platform.NewFiles` skips declarations whose kind is not `file` or `block`: it holds no lock for them and refuses to write or remove them as undeclared. P10 adds `0006_journal_epoch.sql` after it.

**C6. Agent socket rules (Task 6), `internal/agent/api/socket.go`.**

```go
const SocketSource = "socket" // Principal.Source of every socket request
type SocketConfig struct {
	// Identity is asked on every request, so an enrollment while the socket
	// serves takes effect at once.
	Identity func() (hostID string, enrolled bool)
}
func NewSocketHandler(cfg Config, sock SocketConfig) http.Handler
```

The caller is always `core.Principal{Actor: "local:root@<host id>" (core.ActorUnenrolled before enrollment), Scope: core.ScopeAdmin, Source: SocketSource}`, whatever header the peer sends. Before enrollment plan, apply, confirm and revert answer `agent.not_enrolled`. Read, resource and plan run the module's preflight through `Config.Agent.Preflight` and answer `module.unavailable` when it is not available; module `core` is exempt. Hello needs no controller headers. The journal routes are absent (`not_found`). The handler checks no peer credentials: P10's socket listener accepts only peers whose `SO_PEERCRED` uid is 0.

**C7. install.sh (Task 7).** `install.sh.in` reads the token file through one descriptor (`read_token_file`) after testing that the path is a readable regular file, refusing an empty file, a file not owned by root (by the caller under `DARKPANEL_INSTALL_ROOT`) and a mode other than 0600 or 0400, and pipes the token to `darkpanel system install --role agent`; refuses with `cannot create a temporary directory` when `mktemp -d` fails; refuses without a controlling terminal with `no terminal to read the enrollment token from; pass --token-file`; and exits 130 on INT and 143 on TERM after removing its temporary directory. The repository root carries the generated `install.sh`, mode 0755, equal to `make install-sh` output (production keys, which are empty until the owner adds them).

**C8. System install (Task 8), `internal/install/install.go`.**

```go
const HealthTimeout = 60 * time.Second
const pollInterval = time.Second
type Controller interface {
	Healthy(ctx context.Context) error                 // GET /healthz on the controller socket answers status ok
	EnrollLocal(ctx context.Context) (string, error)   // core.host.enroll-local; returns the token
}
type CheckResult int
const (
	CheckAccepted    CheckResult = iota // 0: exit status of darkpanel agent check
	CheckRefused                        // 1
	CheckUnreachable                    // 2
)
type Agent interface {
	Check(ctx context.Context) (CheckResult, error) // one mTLS handshake with the saved credentials
	Enroll(ctx context.Context, token string) error // consume token, write a credential generation
}
type Users interface {
	Lookup(name string) (uid, gid int, found bool, err error)
}
type SystemUsers struct{} // Users over os/user
type Config struct {
	Layout         Layout
	Role           Role
	AgentListen    []string
	AgentAdvertise string
	Token          string // agent role: the trimmed token from standard input, or ""
	Modules        []core.Manifest
	BinaryVersion  string
	Systemd        platform.Systemd
	Exec           core.Runner
	Users          Users
	Controller     Controller // controller role only
	Agent          Agent
	Clock          core.Clock
	Out            io.Writer
	Chown          func(path string, uid, gid int) error // nil: os.Chown
	PollInterval   time.Duration                         // 0: 1 s
	HealthTimeout  time.Duration                         // 0: HealthTimeout
}
func Install(ctx context.Context, cfg Config) error
```

`Install` runs the steps `directories`, `keys`, `database`, `units`, `controller`, `enrollment`, `agent`, printing `<step>: <line>` for each step that has one, and wraps a failure as `install: <step>: <cause>`. A unit is started with `Restart`, after `ResetFailed` when its `ActiveState` is `failed`. P12 implements `Controller` over the controller socket and `Agent` over the agent daemon's credential code (P10), and `darkpanel agent check` exits with the `CheckResult` value.

**C9. Uninstall (Task 9), `internal/install/uninstall.go`.**

```go
const StopTimeout = 30 * time.Second
type UninstallConfig struct {
	Layout        Layout
	KeepData      bool
	Modules       []core.Module // each implementing core.Uninstaller runs its step
	Systemd       platform.Systemd
	Exec          core.Runner
	Users         Users
	Clock         core.Clock
	Log           *slog.Logger
	FilesTestMode bool          // platform.FilesConfig.TestMode; false in production
	PollInterval  time.Duration // 0: 1 s
	StopTimeout   time.Duration // 0: StopTimeout
}
type Uninstall struct{ /* unexported */ }
func PlanUninstall(ctx context.Context, cfg UninstallConfig) (*Uninstall, error)
func (u *Uninstall) Describe(w io.Writer)
func (u *Uninstall) Run(ctx context.Context, w io.Writer) error // closes the plan
func (u *Uninstall) Close() error                              // safe to call twice
```

The plan is the union of agent.db's manifest and `OwnedPaths(l, RoleController)`. Steps, in order: `services` (stop the controller and agent units that exist and wait until inactive, then disable them with the recover unit), `agent lock`, `modules`, `marked blocks`, `files`, `units` (then `Reload`), `binary`, `data`. Without `KeepData`, `data` removes `ConfigDir`, every recorded user still present (through `/usr/sbin/userdel`), and `DataDir` with agent.db last; with `KeepData` it keeps `DataDir`, `ConfigDir` and every user, and the `files` step skips core entries under them. A failed step stops the run with `uninstall: <step>: <cause>; not run: <steps>`; a new plan picks up where it stopped. P12 wires `darkpanel uninstall [--keep-data] [--yes]` over it.

**C10. Shared test helpers.** Task 2's `handler_test.go` declares `testPrincipal`, `testTime`, `testChange`, `testPlan`, `discardLog`, `principalHeader`, `call`, `wantProblem` and `decode`, which Tasks 4 and 6 use. Task 8's `fakes_test.go` declares `fakeSystemd` (fields `states`, `missing`, `stuck`), `newFakeSystemd`, `setState`, `failOn` (by method, or `"<Method> <unit>"`), `recorded`, `fakeUsers`, `fakeRunner`, `commands`, `fakeController`, `fakeAgent`, `chowns`, `testHost`, `installTime`, `newTestHost` and `config`, and its `install_test.go` declares `manifest`, `wantManifest`, `wantMode` and `readBytes`; Task 9 uses them.

## Assumptions (evidence)

- Foundation names this plan consumes exist on `main` at `a7b2a9d`: `core.OwnedDir`, `OwnedUser`, `OwnedUnit` (`internal/core/contract.go:208-210`), `core.Uninstaller` (`internal/core/contract.go:244-246`), `core.ModuleIDCore` (`internal/core/contract.go:23`), `core.ActorUnenrolled` (`internal/core/fleet.go:22`), `wire.HeaderPrincipal`, `HeaderProtocol`, `HeaderVersion`, `HeaderControllerID` and `MaxBody` (`internal/agent/wire/wire.go:26-33`), `Paths.AgentDB`, `AgentBackups`, `Binary`, `RecoveryBinary`, `AgentTLSCurrent` (`internal/paths/paths.go:31-63`), `update.ControllerUnit`, `AgentUnit`, `RollbackUnit` (`internal/update/updater.go:32-34`), `update.RecoverBoot` and `RecoverFailure` (`internal/update/recover.go:18-25`), `agentruntime.ProtocolVersion` and `BackupsKept` (`internal/agent/runtime/runtime.go:22-28`), `errcode.ModuleUnavailable`, `AgentNotEnrolled`, `EnrollTokenInvalid` (`internal/core/errcode/errcode.go:28,54-55`), the `platform.Systemd` interface (`internal/platform/systemd.go:26-38`) and the exec allowlist entries for `useradd` and `userdel` (`internal/platform/exec.go:52`), all read 2026-10-20.
- Every task's code was dry-run before it was written into this plan: on 2026-10-20 the nine tasks were committed one after another in a scratch worktree from `a7b2a9d`, and at every commit `go build ./...`, `go vet ./...`, `go test -race` of the task's packages and `golangci-lint run --allow-serial-runners ./...` passed. On the last commit `go test -count=1 ./...`, `go mod tidy -diff` and `shellcheck release/*.sh install.sh` passed. The code blocks below are those commits' files and patches, unedited.
- `aead.dev/minisign/cmd/minisign` builds from the module cache with no `go.mod` or `go.sum` change (`go build -o … aead.dev/minisign/cmd/minisign`, `git status` clean afterwards, 2026-10-20), so Task 7's round-trip test builds the real signer. It signs in hashed mode (`ED`) and reads no password for an unencrypted key (`cmd/minisign/minisign.go:221-230,259`). The committed harness key `internal/update/trustedkeys/testdata/harness.key` is unencrypted despite its untrusted comment (its KDF bytes are zero), so the test signs with it, the key row 37 names and the harness (P13) will use.
- Names of plans 2 and 4 this plan consumes: `transport.ControllerSession`, `AgentSession`, `Session.Open`, `MaxControllerStreams` (`internal/transport/session.go:16,37,47,74`); the Makefile targets `lint-sh` and `install-sh` (`Makefile:55-56,108-109`, which export `GOTOOLCHAIN=local` at line 22) and `release/sign.sh`. Other foundation and M1 names the code uses: `errcode.Known`, `Code.Status`, `Code.Title` (`internal/core/errcode/errcode.go:124-138`), `core.ParseVersion` (`internal/core/contract.go:72`), `wire.FromApplyOptions`, `ChangeFilterQuery`, `ParseChangeFilter` (`internal/agent/wire/wire.go:172,276,292`), `coretest.FakeAgent` (`internal/core/coretest/agent.go:18`), `secret.LoadOrCreateKey`, `secret.NewSealer` (`internal/core/secret/key.go:33`, `sealer.go:36`), `platform.OwnedPaths` (`internal/platform/files.go:73`), `Ownership.Forget` (`internal/platform/ownership.go:40`), and the first-admin command `darkpanel user create --username admin` (`internal/controller/coreactions/enroll.go:21`, `README.md:64`), all read 2026-10-20.
- Ownership: the spec's decomposition gives `install.sh.in` and `scripts/gen-install-sh` to P0 and P4 only "the generated `install.sh` and its fixture tests". With P0 merged, Task 7 takes over `install.sh.in` and the generator's tests, as register row 28 asks; no other part of plans 3 and 4 touches them.
- Waves 5, 6 and 7 share no package but stay one task per wave: each has risk 3, and a risk-3 task always runs alone in its wave.
- Codex is off this session (`codex-gate usable=false reason=plugin-not-enabled source=probe`, 2026-10-20), so no task carries an `**Executor:**` line and plan review runs on `dr-superpowers:judge-opus`.
- The M2 core register's rows 1-15 and 17-21 (`docs/superpowers/registers/2026-10-19-m2-fleet-lifecycle-core.md`) are each discharged across several parts and their acceptance includes harness scenarios, so they stay assigned to the spec and close with the assembly and harness plan. This plan schedules rows 28 and 37, both on Task 7, and records the duties it hands to P10 and P12 as rows 67 and 68.
- Row 34 asks for the committed `install.sh` in mode 100755 matching `make install-sh`; Task 7 delivers that half (C7), and the row stays with the spec for the `version --json` fields P12 adds. Until the owner adds the production key (row 35) the committed script embeds no key and refuses every release with `this install.sh embeds no trusted release keys`.
- Row 25 (the release index is unsigned, so the automatic newest-stable lookup can be steered to an older signed release) stays deferred: the ruling seat parked it because spec sections 7 and 8 accept that trust model. An automated security review flagged the same lines again on 2026-10-20; nothing here changes them.

Agent API (P3):

- Decision: the agent requires `Darkpanel-Principal` only on the routes whose `core.AgentClient` method takes a principal and ignores it elsewhere (C2), while the controller's client sends it on every request as spec section 5 and ADR 0012 freeze (C4). `core.AgentClient`'s `Hello`, `Change`, `Changes`, `Preflight`, `Journal` and `Ack` name no caller, so on those routes the client sends `system:controller`, an actor neither spec section 11 nor ADR 0012 lists yet; the agent never records it, and register row 69 adds it to both at the next amendment. Without the fallback `TestClientRoundTrip` fails on those six routes (2026-10-20).
- Decision: unknown members of request bodies are ignored, not refused (C2), because spec section 5's version window lets a controller one minor newer talk to this agent.
- `ClientConfig` disables keep-alives because each idle pooled connection would hold one of the session's 32 stream slots (`transport.MaxControllerStreams`): `TestClientOverSession` sends 96 requests one after another and 64 at once over one session and passed in the dry run (2026-10-20).
- Decisions on the socket (C6): the principal header is ignored, not refused, since the socket's peer is already root; resource reads run the preflight like actions; the journal routes are absent because only the controller collects and acknowledges the journal. Spec section 5 lists the journal routes without saying whether the socket serves them.

Install (P4):

- Decision: `Install` restarts units (with `ResetFailed` first for a failed unit) where spec section 7 says "start", because a unit that hit its start limit refuses a plain start (systemd `StartUnit` on a unit in `failed` state past `StartLimitBurst`) and a rerun after a binary change must run the new binary.
- Decision: the boot recover unit runs `update recover --boot` (C3). Spec section 7 gives both recovery units `update recover` and says that at boot it never restarts services; the flag is how one command tells the two callers apart, and it maps to `update.RecoverBoot`. P12 must implement `--boot` (register row 68).
- Decision: uninstall plans over the union of the manifest and the core's owned paths (C9), so a manifest damaged or never written by an interrupted install still removes the units, binary and directories; and it keeps every user with `--keep-data`, because the kept controller files belong to `darkpanel`.
- `install.sh`: under dash a failed redirection of `exec 3<file` ends the shell with a raw error that no `||` catches (dash 0.5.12 on this host, 2026-10-20), so `read_token_file` tests the path before opening it; `stat -L /dev/fd/3` reads the opened file, so a file swapped in after the test is never read. A shell killed by a signal skips its `EXIT` trap, so the `INT` and `TERM` traps exit, which runs it (`timeout -s INT` on the script and `TestInstallCleansUpOnInterrupt`, 2026-10-20). No test can interleave a swap between the test and the open; the TOCTOU half of row 28 rests on reading through one descriptor.
- Against the template before Task 7, the new tests fail where each fix lands (dry run 2026-10-20): the missing and directory token files (old message), the empty token file (installed), no terminal (raw `cannot create /dev/tty` error), both temporary-directory cases (raw `mktemp` error) and the interrupt (exit by signal, temporary directory left). The legacy `Ed` and patched key id rows pass against it too: those checks existed untested, and each row's signature still verifies cryptographically, so only the checked field can refuse it.
- `TestInstallCleansUpOnInterrupt` sets its server hook through an `atomic.Pointer`: a plain field set after the server started was a data race under `-race` (1 of 30 runs, 2026-10-20), because the race detector cannot see the ordering install.sh's request provides.
- Task 5's three tests fail before the migration: `TestOtherKindsIgnored` (`Files holds 3 path locks, want none`), and `TestOwnershipRecordsEveryKind` and `TestOwnershipKindsMigrationKeepsEntries` (`CHECK constraint failed: kind IN ('file', 'block')`), 2026-10-20.
- `TestUninstallKeepDataThenReinstall` stores a value sealed with the agent key in the kept agent.db's `core_meta`, and after `--keep-data` and a reinstall reads it back and unseals it with the kept key, the check spec section 7 asks for.
- The install and uninstall waits use real time (`time.After`), paced by `PollInterval`, `HealthTimeout` and `StopTimeout`, which the tests set to milliseconds (`fakes_test.go` `config`, `uninstallConfig`), so no test waits a second.

## Task index

1. Install layout and core owned paths
2. Agent API handler
3. Unit templates
4. Agent API client
5. Ownership kinds migration
6. Agent socket rules
7. install.sh hardening
8. System install
9. Uninstall

---

### Task 1: Install layout and core owned paths

**Files:**
- Create: `internal/install/layout.go`
- Test: `internal/install/layout_test.go`

**Interfaces:**
- Consumes: `paths.Paths` and `paths.Production`, `core.OwnedPath` and its kinds, `update.ControllerUnit`, `update.AgentUnit`, `update.RollbackUnit` (Assumptions).
- Produces: C1. Top-level names added: `Role`, `RoleController`, `RoleAgent`, `ParseRole`, `ServiceUser`, `RecoverUnit`, `DropInDir`, `DropInName`, `AgentLockName`, `Layout`, `Production`, `Rooted`, `Layout.UnitPath`, `Layout.DropInPath`, `OwnedPaths`, `CoreOwnedPaths`; tests: `TestParseRole`, `TestProductionLayout`, `TestRootedLayout`, `TestOwnedPaths`. The file carries the package comment of `internal/install`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/install/layout_test.go`:

```go
package install

import (
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

func TestParseRole(t *testing.T) {
	for _, s := range []string{"controller", "agent"} {
		if r, err := ParseRole(s); err != nil || string(r) != s {
			t.Errorf("ParseRole(%q) = %q, %v", s, r, err)
		}
	}
	for _, s := range []string{"", "Agent", "both"} {
		if _, err := ParseRole(s); err == nil {
			t.Errorf("ParseRole(%q) succeeded", s)
		}
	}
}

func TestProductionLayout(t *testing.T) {
	l := Production()
	if l.Paths != paths.Production() {
		t.Errorf("Paths = %+v, want paths.Production()", l.Paths)
	}
	if l.DataDir != "/var/lib/darkpanel" || l.ConfigDir != "/etc/darkpanel" || l.UnitDir != "/etc/systemd/system" {
		t.Errorf("layout = %+v", l)
	}
	if got := l.DropInPath(); got != "/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf" {
		t.Errorf("DropInPath = %s", got)
	}
	// Every data path lies under DataDir and every key or credential under
	// ConfigDir, which uninstall --keep-data relies on.
	for _, d := range []string{l.Paths.ControllerDir, l.Paths.AgentDir, l.Paths.UpdateDir} {
		if !strings.HasPrefix(d, l.DataDir+"/") {
			t.Errorf("%s is not under %s", d, l.DataDir)
		}
	}
	for _, d := range []string{l.Paths.ControllerKey, l.Paths.AgentKey, l.Paths.AgentTLSDir} {
		if !strings.HasPrefix(d, l.ConfigDir+"/") {
			t.Errorf("%s is not under %s", d, l.ConfigDir)
		}
	}
}

func TestRootedLayout(t *testing.T) {
	root := t.TempDir()
	got, prod := Rooted(root), Production()
	pairs := [][2]string{
		{got.Paths.ControllerDir, prod.Paths.ControllerDir}, {got.Paths.AgentDir, prod.Paths.AgentDir},
		{got.Paths.ControllerKey, prod.Paths.ControllerKey}, {got.Paths.AgentKey, prod.Paths.AgentKey},
		{got.Paths.ControllerSocket, prod.Paths.ControllerSocket}, {got.Paths.AgentSocket, prod.Paths.AgentSocket},
		{got.Paths.AgentTLSDir, prod.Paths.AgentTLSDir}, {got.Paths.UpdateDir, prod.Paths.UpdateDir},
		{got.Paths.BinaryDir, prod.Paths.BinaryDir}, {got.Paths.BinaryLink, prod.Paths.BinaryLink},
		{got.DataDir, prod.DataDir}, {got.ConfigDir, prod.ConfigDir}, {got.UnitDir, prod.UnitDir},
	}
	for _, p := range pairs {
		if p[0] != filepath.Join(root, p[1]) {
			t.Errorf("rooted %s, want %s under %s", p[0], p[1], root)
		}
	}
}

func TestOwnedPaths(t *testing.T) {
	l := Production()
	agent := OwnedPaths(l, RoleAgent)
	controller := OwnedPaths(l, RoleController)
	if !reflect.DeepEqual(controller[:len(agent)], agent) {
		t.Fatal("the controller role's paths do not start with the agent role's")
	}
	want := map[string]string{
		"/etc/systemd/system/darkpanel-agent.service":                      core.OwnedUnit,
		"/etc/systemd/system/darkpanel-rollback.service":                   core.OwnedUnit,
		"/etc/systemd/system/darkpanel-update-recover.service":             core.OwnedUnit,
		"/usr/local/lib/darkpanel":                                         core.OwnedDir,
		"/usr/local/bin/darkpanel":                                         core.OwnedFile,
		"/var/lib/darkpanel":                                               core.OwnedDir,
		"/etc/darkpanel":                                                   core.OwnedDir,
		"/var/lib/darkpanel/agent":                                         core.OwnedDir,
		"/var/lib/darkpanel/update":                                        core.OwnedDir,
		"/etc/darkpanel/agent":                                             core.OwnedDir,
		"/etc/darkpanel/agent.key":                                         core.OwnedFile,
		"/etc/systemd/system/darkpanel-controller.service":                 core.OwnedUnit,
		"/etc/systemd/system/darkpanel-controller.service.d":               core.OwnedDir,
		"/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf": core.OwnedUnit,
		"/var/lib/darkpanel/controller":                                    core.OwnedDir,
		"/etc/darkpanel/controller.key":                                    core.OwnedFile,
		"darkpanel":                                                        core.OwnedUser,
	}
	if len(controller) != len(want) {
		t.Errorf("controller role owns %d paths, want %d", len(controller), len(want))
	}
	for _, op := range controller {
		if want[op.Path] != op.Kind {
			t.Errorf("%s has kind %q, want %q", op.Path, op.Kind, want[op.Path])
		}
		if len(op.BlockIDs) != 0 {
			t.Errorf("%s declares blocks", op.Path)
		}
	}
	for _, op := range agent {
		if op.Kind == core.OwnedUser || strings.Contains(op.Path, "controller") {
			t.Errorf("the agent role owns %s", op.Path)
		}
	}
	if !reflect.DeepEqual(CoreOwnedPaths(), controller) {
		t.Error("CoreOwnedPaths differs from the production controller role's paths")
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/1 && TMPDIR=/root/.cache/plan3-tmp/1 GOTOOLCHAIN=local go test -count=1 ./internal/install/`
Expected: FAIL, the build stops with `undefined: ParseRole` (and the other names of `layout.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/install/layout.go`:

```go
// Package install is darkpanel system install and darkpanel uninstall (spec
// section 7): the directories, users, keys, units and ownership manifest of
// one host, created idempotently and removed in a fixed order.
package install

import (
	"fmt"
	"path/filepath"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// Role is what a host runs: the controller role runs the controller and an
// agent, the agent role an agent only.
type Role string

// The two roles of system install.
const (
	RoleController Role = "controller"
	RoleAgent      Role = "agent"
)

// ParseRole parses the value of --role.
func ParseRole(s string) (Role, error) {
	switch r := Role(s); r {
	case RoleController, RoleAgent:
		return r, nil
	}
	return "", fmt.Errorf("install: role %q: want controller or agent", s)
}

// ServiceUser is the system user the controller runs as (ADR 0005).
const ServiceUser = "darkpanel"

// RecoverUnit finishes an interrupted update at boot, before the services
// start.
const RecoverUnit = "darkpanel-update-recover.service"

// The drop-in of the controller unit that holds the agent listener flags.
const (
	DropInDir  = "darkpanel-controller.service.d"
	DropInName = "10-agent.conf"
)

// AgentLockName is the file in Paths.AgentDir that the agent daemon holds an
// exclusive flock on while it runs; uninstall takes it to prove no agent is
// left.
const AgentLockName = "agent.lock"

// Layout is every location system install and uninstall touch.
type Layout struct {
	Paths     paths.Paths
	DataDir   string // the parent of the controller, agent and update directories
	ConfigDir string // the parent of the role keys and the agent credentials
	UnitDir   string // where unit files are written
}

// Production is the layout of an installed host.
func Production() Layout {
	return Layout{
		Paths:     paths.Production(),
		DataDir:   "/var/lib/darkpanel",
		ConfigDir: "/etc/darkpanel",
		UnitDir:   "/etc/systemd/system",
	}
}

// Rooted is Production with every path moved under root, for tests.
func Rooted(root string) Layout {
	l := Production()
	for _, p := range []*string{
		&l.Paths.ControllerDir, &l.Paths.AgentDir, &l.Paths.ControllerKey, &l.Paths.AgentKey,
		&l.Paths.ControllerSocket, &l.Paths.AgentSocket, &l.Paths.AgentTLSDir, &l.Paths.UpdateDir,
		&l.Paths.BinaryDir, &l.Paths.BinaryLink, &l.DataDir, &l.ConfigDir, &l.UnitDir,
	} {
		*p = filepath.Join(root, *p)
	}
	return l
}

// UnitPath is the unit file of unit.
func (l Layout) UnitPath(unit string) string { return filepath.Join(l.UnitDir, unit) }

// DropInPath is the controller unit's agent drop-in.
func (l Layout) DropInPath() string { return filepath.Join(l.UnitDir, DropInDir, DropInName) }

// OwnedPaths is what system install creates for role, in the order uninstall
// lists it: the agent role's paths, then the controller role's additions.
func OwnedPaths(l Layout, role Role) []core.OwnedPath {
	p := l.Paths
	out := []core.OwnedPath{
		{Path: l.UnitPath(update.AgentUnit), Kind: core.OwnedUnit},
		{Path: l.UnitPath(update.RollbackUnit), Kind: core.OwnedUnit},
		{Path: l.UnitPath(RecoverUnit), Kind: core.OwnedUnit},
		{Path: p.BinaryDir, Kind: core.OwnedDir},
		{Path: p.BinaryLink, Kind: core.OwnedFile},
		{Path: l.DataDir, Kind: core.OwnedDir},
		{Path: l.ConfigDir, Kind: core.OwnedDir},
		{Path: p.AgentDir, Kind: core.OwnedDir},
		{Path: p.UpdateDir, Kind: core.OwnedDir},
		{Path: p.AgentTLSDir, Kind: core.OwnedDir},
		{Path: p.AgentKey, Kind: core.OwnedFile},
	}
	if role == RoleController {
		out = append(out,
			core.OwnedPath{Path: l.UnitPath(update.ControllerUnit), Kind: core.OwnedUnit},
			core.OwnedPath{Path: filepath.Join(l.UnitDir, DropInDir), Kind: core.OwnedDir},
			core.OwnedPath{Path: l.DropInPath(), Kind: core.OwnedUnit},
			core.OwnedPath{Path: p.ControllerDir, Kind: core.OwnedDir},
			core.OwnedPath{Path: p.ControllerKey, Kind: core.OwnedFile},
			core.OwnedPath{Path: ServiceUser, Kind: core.OwnedUser},
		)
	}
	return out
}

// CoreOwnedPaths is the core module's declaration: everything either role
// creates on a production host.
func CoreOwnedPaths() []core.OwnedPath { return OwnedPaths(Production(), RoleController) }
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/1 GOTOOLCHAIN=local
go test -count=1 -race -timeout 120s ./internal/install/
go vet ./internal/install/
golangci-lint run --allow-serial-runners ./internal/install/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/install`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/install/layout.go internal/install/layout_test.go
git commit -m "feat(install): add install layout and owned paths"
```

### Task 2: Agent API handler

**Files:**
- Create: `internal/agent/api/handler.go`
- Test: `internal/agent/api/handler_test.go`

**Interfaces:**
- Consumes: `core.AgentClient` and the `core` view types, `coretest.FakeAgent` (tests), the `wire` types, conversions, headers and `MaxBody`, `errcode` (Assumptions).
- Produces: C2 and the handler half of C10. Top-level names added: `prefix`, `problemContentType`, the twelve `op*` constants, `ControllerHello`, `Config`, `handler`, `NewHandler`, `handler.routes`, `headerPrincipal`, `handler.controllerHello`, `actionRef`, `handler.actor`, the `handler.handle*` methods, `journalQuery`, `invalidQuery`, `readJSON`, `decodeJSON`, `problem`, `handler.writeProblem`, `handler.writeJSON`, `handler.writeRaw`; tests: `testPrincipal`, `testTime`, `testChange`, `testPlan`, `discardLog`, `principalHeader`, `call`, `wantProblem`, `decode`, `TestHandlerActionRoutes`, `TestHandlerReadRoutes`, `TestHandlerRefusals`, `TestHandlerAgentErrors`. The file carries the package comment of `internal/agent/api`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/agent/api/handler_test.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

var (
	testPrincipal = core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeOperate, Source: "203.0.113.7"}
	testTime      = time.Date(2026, 10, 20, 8, 0, 0, 0, time.UTC)
	testChange    = core.ChangeView{ID: "c1", HostID: "h1", Action: "demo.set", Version: 1, Actor: "user:u1",
		Class: core.ClassCompensable, State: core.StateCompleted, CreatedAt: testTime, UpdatedAt: testTime,
		Output: json.RawMessage(`{"ok":true}`)}
	testPlan = core.PlanView{PlanID: "p1", HostID: "h1", Action: "demo.set", Version: 1, InputHash: "ab",
		Diff: []core.DiffLine{{Op: "+", Text: "x"}}, Guards: []core.Guard{{ID: "g", OK: true, Message: "fine"}}, ExpiresAt: testTime}
)

// discardLog is a logger for handlers whose log output no test reads.
func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

// principalHeader is the Darkpanel-Principal header of p.
func principalHeader(t *testing.T, p core.Principal) string {
	t.Helper()
	b, err := json.Marshal(wire.FromPrincipal(p))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// call sends one request to h; a non-nil body is sent as application/json.
func call(t *testing.T, h http.Handler, method, path string, header http.Header, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var r io.Reader
	if body != nil {
		r = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, path, r)
	for k, v := range header {
		req.Header[k] = v
	}
	if body != nil && req.Header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// wantProblem checks that rec is problem details with status and code, and
// returns it.
func wantProblem(t *testing.T, rec *httptest.ResponseRecorder, status int, code errcode.Code) problem {
	t.Helper()
	var p problem
	if rec.Code != status || rec.Header().Get("Content-Type") != problemContentType {
		t.Fatalf("answer %d %s %s, want %d problem details", rec.Code, rec.Header().Get("Content-Type"), rec.Body, status)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &p); err != nil || p.Code != code || p.Status != status {
		t.Fatalf("problem %s (%v), want code %s", rec.Body, err, code)
	}
	return p
}

func decode[T any](t *testing.T, rec *httptest.ResponseRecorder) T {
	t.Helper()
	var v T
	if rec.Code != http.StatusOK {
		t.Fatalf("answer %d %s, want 200", rec.Code, rec.Body)
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &v); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return v
}

func TestHandlerActionRoutes(t *testing.T) {
	var got []any
	fake := &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
			got = append(got, p, a, string(in))
			return json.RawMessage(`{"uptime":5}`), nil
		},
		ResourceFunc: func(_ context.Context, p core.Principal, name string) (json.RawMessage, error) {
			got = append(got, p, name)
			return nil, nil
		},
		PlanFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (core.PlanView, error) {
			got = append(got, p, a, string(in))
			return testPlan, nil
		},
		ApplyFunc: func(_ context.Context, p core.Principal, id string, o core.ApplyOptions) (core.ChangeView, error) {
			got = append(got, p, id, o)
			return testChange, nil
		},
		ConfirmFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "confirm", p, id)
			return testChange, nil
		},
		RevertFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "revert", p, id)
			return testChange, nil
		},
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	hdr := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}
	ref := core.ActionRef{Name: "hostinfo.summary", Version: 2}

	if out := call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v2/read", hdr, []byte(`{"a":1}`)); out.Body.String() != `{"uptime":5}` {
		t.Errorf("read answered %d %s", out.Code, out.Body)
	}
	if out := call(t, h, "GET", "/agent/v1/resources/hostinfo/status", hdr, nil); out.Code != 200 || out.Body.String() != "null" {
		t.Errorf("resource answered %d %s, want 200 null", out.Code, out.Body)
	}
	if v := decode[wire.PlanView](t, call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v2/plan", hdr, []byte(`{}`))); !reflect.DeepEqual(v.Core(), testPlan) {
		t.Errorf("plan = %+v", v)
	}
	apply := []byte(`{"confirmTimeoutSeconds":60,"confirmation":"yes","confirmNow":true,"later":1}`)
	if v := decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/plans/p1/apply", hdr, apply)); !reflect.DeepEqual(v.Core(), testChange) {
		t.Errorf("apply = %+v", v)
	}
	decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/changes/c1/confirm", hdr, nil))
	decode[wire.ChangeView](t, call(t, h, "POST", "/agent/v1/changes/c2/revert", hdr, nil))
	want := []any{
		testPrincipal, ref, `{"a":1}`,
		testPrincipal, "hostinfo.status",
		testPrincipal, ref, `{}`,
		testPrincipal, "p1", core.ApplyOptions{ConfirmTimeout: time.Minute, Confirmation: "yes", ConfirmNow: true},
		"confirm", testPrincipal, "c1",
		"revert", testPrincipal, "c2",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agent calls\n got %v\nwant %v", got, want)
	}
}

func TestHandlerReadRoutes(t *testing.T) {
	var got []any
	entry := core.JournalEntry{Seq: 7, At: testTime, Actor: "user:u1", Action: "demo.set", Version: 1, Phase: "applied", Result: "ok"}
	fake := &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) {
			return core.AgentHello{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "amd64", HostID: "h1"}, nil
		},
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			got = append(got, "change", id)
			return testChange, nil
		},
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			got = append(got, f)
			return nil, nil
		},
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			got = append(got, "preflight", id)
			return core.ModuleStatus{ID: id, Version: 1, Reason: "ufw is missing"}, nil
		},
		JournalFunc: func(_ context.Context, after int64, limit int) ([]core.JournalEntry, error) {
			got = append(got, after, limit)
			return []core.JournalEntry{entry}, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			got = append(got, "ack", seq)
			return nil
		},
	}
	var hellos []ControllerHello
	h := NewHandler(Config{Agent: fake, Log: discardLog(), OnHello: func(c ControllerHello) { hellos = append(hellos, c) }})
	hello := http.Header{wire.HeaderProtocol: {"1"}, wire.HeaderVersion: {"0.2.1"}, wire.HeaderControllerID: {"C1"}}

	if v := decode[wire.Hello](t, call(t, h, "GET", "/agent/v1/hello", hello, nil)); v.HostID != "h1" || v.Arch != "amd64" {
		t.Errorf("hello = %+v", v)
	}
	if want := []ControllerHello{{Protocol: 1, Version: "0.2.1", ControllerID: "C1"}}; !reflect.DeepEqual(hellos, want) {
		t.Errorf("OnHello got %+v, want %+v", hellos, want)
	}
	decode[wire.ChangeView](t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil))
	if rec := call(t, h, "GET", "/agent/v1/changes?state=pending&limit=5", nil, nil); rec.Body.String() != "[]" {
		t.Errorf("changes answered %s, want []", rec.Body)
	}
	if v := decode[wire.ModuleStatus](t, call(t, h, "GET", "/agent/v1/modules/ufw/preflight", nil, nil)); v.Reason != "ufw is missing" {
		t.Errorf("preflight = %+v", v)
	}
	if v := decode[[]wire.JournalEntry](t, call(t, h, "GET", "/agent/v1/journal?after=6&limit=50", nil, nil)); len(v) != 1 || !reflect.DeepEqual(v[0].Core(), entry) {
		t.Errorf("journal = %+v", v)
	}
	decode[[]wire.JournalEntry](t, call(t, h, "GET", "/agent/v1/journal", nil, nil))
	if rec := call(t, h, "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":7}`)); rec.Code != http.StatusNoContent {
		t.Errorf("ack answered %d %s, want 204", rec.Code, rec.Body)
	}
	want := []any{
		"change", "c1",
		core.ChangeFilter{State: "pending", Limit: 5},
		"preflight", "ufw",
		int64(6), 50,
		int64(0), 0,
		"ack", int64(7),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("agent calls\n got %v\nwant %v", got, want)
	}
}

func TestHandlerRefusals(t *testing.T) {
	fake := &coretest.FakeAgent{
		ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return json.RawMessage(`{}`), nil
		},
		HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{}, nil },
	}
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	good := principalHeader(t, testPrincipal)
	read := "/agent/v1/actions/demo.get/v1/read"
	tests := []struct {
		name, method, path string
		header             http.Header
		body               []byte
		status             int
		code               errcode.Code
	}{
		{"no principal", "POST", read, nil, []byte(`{}`), 422, errcode.InvalidInput},
		{"principal not JSON", "POST", read, http.Header{wire.HeaderPrincipal: {"user:u1"}}, []byte(`{}`), 422, errcode.InvalidInput},
		{"principal without actor", "POST", read, http.Header{wire.HeaderPrincipal: {`{"scope":"admin"}`}}, []byte(`{}`), 422, errcode.InvalidInput},
		{"bad version", "POST", "/agent/v1/actions/demo.get/1/read", http.Header{wire.HeaderPrincipal: {good}}, []byte(`{}`), 404, errcode.ActionUnknown},
		{"text body", "POST", read, http.Header{wire.HeaderPrincipal: {good}, "Content-Type": {"text/plain"}}, []byte(`{}`), 415, errcode.UnsupportedMediaType},
		{"no body", "POST", read, http.Header{wire.HeaderPrincipal: {good}, "Content-Type": {"application/json"}}, nil, 400, errcode.InvalidJSON},
		{"two values", "POST", read, http.Header{wire.HeaderPrincipal: {good}}, []byte(`{} {}`), 400, errcode.InvalidJSON},
		{"too large", "POST", read, http.Header{wire.HeaderPrincipal: {good}}, []byte(`"` + strings.Repeat("a", wire.MaxBody) + `"`), 413, errcode.TooLarge},
		{"apply body of the wrong type", "POST", "/agent/v1/plans/p1/apply", http.Header{wire.HeaderPrincipal: {good}}, []byte(`{"confirmNow":"yes"}`), 400, errcode.InvalidJSON},
		{"hello without headers", "GET", "/agent/v1/hello", nil, nil, 422, errcode.InvalidInput},
		{"hello with protocol 0", "GET", "/agent/v1/hello", http.Header{wire.HeaderProtocol: {"0"}, wire.HeaderVersion: {"1"}, wire.HeaderControllerID: {"C"}}, nil, 422, errcode.InvalidInput},
		{"unknown change filter", "GET", "/agent/v1/changes?host=h1", nil, nil, 422, errcode.InvalidInput},
		{"negative journal after", "GET", "/agent/v1/journal?after=-1", nil, nil, 422, errcode.InvalidInput},
		{"zero journal limit", "GET", "/agent/v1/journal?limit=0", nil, nil, 422, errcode.InvalidInput},
		{"unknown journal parameter", "GET", "/agent/v1/journal?before=3", nil, nil, 422, errcode.InvalidInput},
		{"negative ack", "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":-1}`), 422, errcode.InvalidInput},
		{"unknown route", "GET", "/agent/v1/nothing", nil, nil, 404, errcode.NotFound},
		{"wrong method", "POST", "/agent/v1/hello", nil, nil, 404, errcode.NotFound},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			wantProblem(t, call(t, h, tt.method, tt.path, tt.header, tt.body), tt.status, tt.code)
		})
	}
}

func TestHandlerAgentErrors(t *testing.T) {
	var fail error
	fake := &coretest.FakeAgent{
		ChangeFunc: func(context.Context, string) (core.ChangeView, error) { return core.ChangeView{}, fail },
	}
	var logged bytes.Buffer
	h := NewHandler(Config{Agent: fake, Log: slog.New(slog.NewTextHandler(&logged, nil))})

	fail = &errcode.Error{Code: errcode.InvalidInput, Detail: "bad input", Fields: []errcode.FieldError{{Path: "/name", Message: "required"}}}
	p := wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 422, errcode.InvalidInput)
	if p.Detail != "bad input" || !reflect.DeepEqual(p.Errors, []errcode.FieldError{{Path: "/name", Message: "required"}}) || p.Title != "Invalid input" {
		t.Errorf("problem = %+v", p)
	}

	fail = errcode.New(errcode.ChangeNotFound, "no change c1")
	if p := wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 404, errcode.ChangeNotFound); p.Detail != "no change c1" {
		t.Errorf("detail = %q", p.Detail)
	}

	fail = errors.New("disk on fire at /var/lib/darkpanel/agent/agent.db")
	p = wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 500, errcode.Internal)
	if p.Detail != "Internal error" || strings.Contains(p.Detail, "disk") {
		t.Errorf("an internal problem shows %q", p.Detail)
	}
	if !strings.Contains(logged.String(), "disk on fire") {
		t.Errorf("the cause was not logged: %s", logged.String())
	}

	fail = &errcode.Error{Code: "newer.code", Detail: "from the future"}
	wantProblem(t, call(t, h, "GET", "/agent/v1/changes/c1", nil, nil), 500, errcode.Internal)
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/2 && TMPDIR=/root/.cache/plan3-tmp/2 GOTOOLCHAIN=local go test -count=1 ./internal/agent/api/`
Expected: FAIL, the build stops with `undefined: NewHandler` (and the other names of `handler.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/agent/api/handler.go`:

```go
// Package api is the agent API (ADR 0012): the HTTP handler the agent serves
// on transport streams and on its socket, and the core.AgentClient that
// calls it over either.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"mime"
	"net/http"
	"slices"
	"strconv"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// prefix is the path every agent API route starts with.
const prefix = "/agent/v1/"

const problemContentType = "application/problem+json"

// The operations of the agent API, one per route.
const (
	opHello     = "hello"
	opRead      = "read"
	opResource  = "resource"
	opPlan      = "plan"
	opApply     = "apply"
	opConfirm   = "confirm"
	opRevert    = "revert"
	opChange    = "change"
	opChanges   = "changes"
	opPreflight = "preflight"
	opJournal   = "journal"
	opAck       = "ack"
)

// ControllerHello is the controller's half of a hello, sent in the headers
// of GET hello.
type ControllerHello struct {
	Protocol     int
	Version      string
	ControllerID string
}

// Config is what the handler serves.
type Config struct {
	Agent core.AgentClient // the agent runtime
	Log   *slog.Logger
	// OnHello receives the controller's half of each hello on a transport
	// stream; nil ignores it.
	OnHello func(ControllerHello)
}

// handler serves the agent API. Its three functions are what differs
// between a transport stream, whose peer is the authenticated controller,
// and the agent socket.
type handler struct {
	cfg Config
	// principal returns the caller of an operation that acts for one.
	principal func(r *http.Request) (core.Principal, error)
	// allow refuses an operation before it reaches the agent; name is the
	// action or resource name, empty for the other operations.
	allow func(ctx context.Context, op, name string) error
	// hello checks the controller's half of a hello.
	hello func(r *http.Request) error
}

// NewHandler serves the agent API on transport streams: the caller is the
// principal the controller names in the Darkpanel-Principal header.
func NewHandler(cfg Config) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = headerPrincipal
	h.allow = func(context.Context, string, string) error { return nil }
	h.hello = h.controllerHello
	return h.routes(true)
}

// routes maps every route; the journal routes only when journal is set.
func (h *handler) routes(journal bool) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+prefix+"hello", h.handleHello)
	mux.HandleFunc("POST "+prefix+"actions/{name}/{version}/read", h.handleRead)
	mux.HandleFunc("GET "+prefix+"resources/{module}/{resource}", h.handleResource)
	mux.HandleFunc("POST "+prefix+"actions/{name}/{version}/plan", h.handlePlan)
	mux.HandleFunc("POST "+prefix+"plans/{id}/apply", h.handleApply)
	mux.HandleFunc("POST "+prefix+"changes/{id}/confirm", h.handleChangeOp(opConfirm))
	mux.HandleFunc("POST "+prefix+"changes/{id}/revert", h.handleChangeOp(opRevert))
	mux.HandleFunc("GET "+prefix+"changes/{id}", h.handleChange)
	mux.HandleFunc("GET "+prefix+"changes", h.handleChanges)
	mux.HandleFunc("GET "+prefix+"modules/{id}/preflight", h.handlePreflight)
	if journal {
		mux.HandleFunc("GET "+prefix+"journal", h.handleJournal)
		mux.HandleFunc("POST "+prefix+"journal/ack", h.handleAck)
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		h.writeProblem(w, errcode.New(errcode.NotFound, fmt.Sprintf("no agent API route %s %s", r.Method, r.URL.Path)))
	})
	return mux
}

// headerPrincipal reads the principal the controller sends.
func headerPrincipal(r *http.Request) (core.Principal, error) {
	raw := r.Header.Get(wire.HeaderPrincipal)
	if raw == "" {
		return core.Principal{}, errcode.New(errcode.InvalidInput, "the "+wire.HeaderPrincipal+" header is required")
	}
	var p wire.Principal
	if err := json.Unmarshal([]byte(raw), &p); err != nil || p.Actor == "" {
		return core.Principal{}, errcode.New(errcode.InvalidInput, "the "+wire.HeaderPrincipal+" header is not a principal")
	}
	return p.Core(), nil
}

// controllerHello reads the controller's hello headers and hands them to
// OnHello.
func (h *handler) controllerHello(r *http.Request) error {
	protocol, err := strconv.Atoi(r.Header.Get(wire.HeaderProtocol))
	c := ControllerHello{Protocol: protocol, Version: r.Header.Get(wire.HeaderVersion), ControllerID: r.Header.Get(wire.HeaderControllerID)}
	if err != nil || c.Protocol < 1 || c.Version == "" || c.ControllerID == "" {
		return errcode.New(errcode.InvalidInput, fmt.Sprintf("hello needs the headers %s (a positive integer), %s and %s",
			wire.HeaderProtocol, wire.HeaderVersion, wire.HeaderControllerID))
	}
	if h.cfg.OnHello != nil {
		h.cfg.OnHello(c)
	}
	return nil
}

// actionRef reads an action route's {name} and {version}.
func actionRef(r *http.Request) (core.ActionRef, error) {
	name, version := r.PathValue("name"), r.PathValue("version")
	v, err := core.ParseVersion(version)
	if err != nil {
		return core.ActionRef{}, errcode.New(errcode.ActionUnknown, fmt.Sprintf("no action %s/%s", name, version))
	}
	return core.ActionRef{Name: name, Version: v}, nil
}

// actor runs the checks of an operation that acts for a principal: the
// principal, then allow.
func (h *handler) actor(r *http.Request, op, name string) (core.Principal, error) {
	p, err := h.principal(r)
	if err != nil {
		return core.Principal{}, err
	}
	return p, h.allow(r.Context(), op, name)
}

func (h *handler) handleHello(w http.ResponseWriter, r *http.Request) {
	if err := h.hello(r); err != nil {
		h.writeProblem(w, err)
		return
	}
	hello, err := h.cfg.Agent.Hello(r.Context())
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromHello(hello))
}

func (h *handler) handleRead(w http.ResponseWriter, r *http.Request) {
	ref, err := actionRef(r)
	var p core.Principal
	if err == nil {
		p, err = h.actor(r, opRead, ref.Name)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	var out json.RawMessage
	if err == nil {
		out, err = h.cfg.Agent.Read(r.Context(), p, ref, input)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeRaw(w, out)
}

func (h *handler) handleResource(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("module") + "." + r.PathValue("resource")
	p, err := h.actor(r, opResource, name)
	var out json.RawMessage
	if err == nil {
		out, err = h.cfg.Agent.Resource(r.Context(), p, name)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeRaw(w, out)
}

func (h *handler) handlePlan(w http.ResponseWriter, r *http.Request) {
	ref, err := actionRef(r)
	var p core.Principal
	if err == nil {
		p, err = h.actor(r, opPlan, ref.Name)
	}
	var input json.RawMessage
	if err == nil {
		input, err = readJSON(w, r)
	}
	var v core.PlanView
	if err == nil {
		v, err = h.cfg.Agent.Plan(r.Context(), p, ref, input)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromPlanView(v))
}

func (h *handler) handleApply(w http.ResponseWriter, r *http.Request) {
	p, err := h.actor(r, opApply, "")
	var opts wire.ApplyOptions
	if err == nil {
		err = decodeJSON(w, r, &opts)
	}
	var v core.ChangeView
	if err == nil {
		v, err = h.cfg.Agent.Apply(r.Context(), p, r.PathValue("id"), opts.Core())
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
}

// handleChangeOp serves confirm and revert, which take no body.
func (h *handler) handleChangeOp(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, err := h.actor(r, op, "")
		var v core.ChangeView
		if err == nil {
			if op == opConfirm {
				v, err = h.cfg.Agent.Confirm(r.Context(), p, r.PathValue("id"))
			} else {
				v, err = h.cfg.Agent.Revert(r.Context(), p, r.PathValue("id"))
			}
		}
		if err != nil {
			h.writeProblem(w, err)
			return
		}
		h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
	}
}

func (h *handler) handleChange(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opChange, "")
	var v core.ChangeView
	if err == nil {
		v, err = h.cfg.Agent.Change(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromChangeView(v))
}

func (h *handler) handleChanges(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opChanges, "")
	var f core.ChangeFilter
	if err == nil {
		f, err = wire.ParseChangeFilter(r.URL.Query())
	}
	var views []core.ChangeView
	if err == nil {
		views, err = h.cfg.Agent.Changes(r.Context(), f)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	out := make([]wire.ChangeView, len(views))
	for i, v := range views {
		out[i] = wire.FromChangeView(v)
	}
	h.writeJSON(w, http.StatusOK, out)
}

func (h *handler) handlePreflight(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opPreflight, "")
	var st core.ModuleStatus
	if err == nil {
		st, err = h.cfg.Agent.Preflight(r.Context(), r.PathValue("id"))
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	h.writeJSON(w, http.StatusOK, wire.FromModuleStatus(st))
}

// journalQuery reads after (a sequence, 0 when absent) and limit (a
// positive count, 0 when absent: the agent's default) of GET journal.
func journalQuery(r *http.Request) (after int64, limit int, err error) {
	q := r.URL.Query()
	for _, k := range slices.Sorted(maps.Keys(q)) {
		if (k != "after" && k != "limit") || len(q[k]) > 1 {
			return 0, 0, invalidQuery(k, "unknown or repeated parameter")
		}
	}
	if s := q.Get("after"); s != "" {
		if after, err = strconv.ParseInt(s, 10, 64); err != nil || after < 0 {
			return 0, 0, invalidQuery("after", "must be a sequence number, 0 or more")
		}
	}
	if s := q.Get("limit"); s != "" {
		if limit, err = strconv.Atoi(s); err != nil || limit < 1 {
			return 0, 0, invalidQuery("limit", "must be a positive integer")
		}
	}
	return after, limit, nil
}

func invalidQuery(param, msg string) error {
	return &errcode.Error{Code: errcode.InvalidInput, Detail: fmt.Sprintf("query parameter %s: %s", param, msg),
		Fields: []errcode.FieldError{{Path: "/" + param, Message: msg}}}
}

func (h *handler) handleJournal(w http.ResponseWriter, r *http.Request) {
	after, limit, err := journalQuery(r)
	if err == nil {
		err = h.allow(r.Context(), opJournal, "")
	}
	var entries []core.JournalEntry
	if err == nil {
		entries, err = h.cfg.Agent.Journal(r.Context(), after, limit)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	out := make([]wire.JournalEntry, len(entries))
	for i, e := range entries {
		out[i] = wire.FromJournalEntry(e)
	}
	h.writeJSON(w, http.StatusOK, out)
}

func (h *handler) handleAck(w http.ResponseWriter, r *http.Request) {
	err := h.allow(r.Context(), opAck, "")
	var ack wire.Ack
	if err == nil {
		err = decodeJSON(w, r, &ack)
	}
	if err == nil && ack.Seq < 0 {
		err = &errcode.Error{Code: errcode.InvalidInput, Detail: "seq must be 0 or more",
			Fields: []errcode.FieldError{{Path: "/seq", Message: "must be 0 or more"}}}
	}
	if err == nil {
		err = h.cfg.Agent.Ack(r.Context(), ack.Seq)
	}
	if err != nil {
		h.writeProblem(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readJSON reads a request body of one JSON value, sent as application/json
// and at most wire.MaxBody bytes.
func readJSON(w http.ResponseWriter, r *http.Request) (json.RawMessage, error) {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errcode.New(errcode.UnsupportedMediaType, "send the request body with Content-Type: application/json")
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, wire.MaxBody))
	var tooLarge *http.MaxBytesError
	switch {
	case errors.As(err, &tooLarge):
		return nil, errcode.New(errcode.TooLarge, fmt.Sprintf("the request body exceeds %d bytes", wire.MaxBody))
	case err != nil:
		return nil, errcode.Wrap(errcode.InvalidJSON, err, "the request body could not be read")
	case len(body) == 0:
		return nil, errcode.New(errcode.InvalidJSON, "the request body is empty")
	case !json.Valid(body):
		return nil, errcode.New(errcode.InvalidJSON, "the request body is not one JSON value")
	}
	return body, nil
}

// decodeJSON reads a body into dst. Unknown members are ignored, so a
// controller one minor newer may send members this agent does not know.
func decodeJSON(w http.ResponseWriter, r *http.Request, dst any) error {
	body, err := readJSON(w, r)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, dst); err != nil {
		return errcode.New(errcode.InvalidJSON, "the request body does not have the expected members and types")
	}
	return nil
}

// problem is an RFC 9457 problem details body, the shape the controller's
// API answers.
type problem struct {
	Type   string               `json:"type"`
	Title  string               `json:"title"`
	Status int                  `json:"status"`
	Detail string               `json:"detail"`
	Code   errcode.Code         `json:"code"`
	Errors []errcode.FieldError `json:"errors,omitempty"`
}

// writeProblem answers err as problem details. A code outside the registry
// becomes internal, and a 5xx answer carries only its title: the cause is
// logged on the agent, never sent.
func (h *handler) writeProblem(w http.ResponseWriter, err error) {
	e := errcode.From(err)
	code := e.Code
	if !errcode.Known(code) {
		code = errcode.Internal
	}
	status := code.Status()
	p := problem{Type: "about:blank", Title: code.Title(), Status: status, Detail: e.Detail, Code: code, Errors: e.Fields}
	if status >= http.StatusInternalServerError {
		p.Detail, p.Errors = code.Title(), nil
		h.cfg.Log.Error("api: request failed", "code", e.Code, "err", err)
	}
	if p.Detail == "" {
		p.Detail = code.Title()
	}
	b, merr := json.Marshal(p)
	if merr != nil {
		panic(fmt.Sprintf("api: marshal problem: %v", merr)) // strings, ints and field errors always marshal
	}
	w.Header().Set("Content-Type", problemContentType)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func (h *handler) writeJSON(w http.ResponseWriter, status int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		h.writeProblem(w, fmt.Errorf("api: marshal response: %w", err))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

// writeRaw answers an action's or resource's output as it is; no output is
// JSON null.
func (h *handler) writeRaw(w http.ResponseWriter, out json.RawMessage) {
	if len(out) == 0 {
		out = json.RawMessage("null")
	}
	if !json.Valid(out) {
		h.writeProblem(w, errors.New("api: the agent answered output that is not JSON"))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(out) //nolint:gosec // G705: checked JSON sent as application/json, never rendered as HTML
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/2 GOTOOLCHAIN=local
go test -count=1 -race -timeout 120s ./internal/agent/api/
go vet ./internal/agent/api/
golangci-lint run --allow-serial-runners ./internal/agent/api/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/agent/api`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/api/handler.go internal/agent/api/handler_test.go
git commit -m "feat(agent): add the agent API handler"
```

### Task 3: Unit templates

**Files:**
- Create: `internal/install/units.go`
- Create: `internal/install/units/darkpanel-controller.service`
- Create: `internal/install/units/darkpanel-agent.service`
- Create: `internal/install/units/darkpanel-rollback.service`
- Create: `internal/install/units/darkpanel-update-recover.service`
- Create: `internal/install/units/10-agent.conf`
- Test: `internal/install/units_test.go`

**Interfaces:**
- Consumes: C1 (`Layout`, `Role`, `RoleController`, `RecoverUnit`, `DropInName`, `Layout.UnitPath`, `Layout.DropInPath`, `Rooted`, `Production`), `Paths.Binary`, `Paths.RecoveryBinary`, the `update` unit names.
- Produces: C3. Top-level names added: `unitTemplates`, `UnitFile`, `unitData`, `agentAddress`, `CheckAgentAddress`, `Units`, `renderUnit`, `EnabledUnits`, `writeIfChanged`, `syncDir`; tests: `unitsByPath`, `wantLines`, `TestControllerUnits`, `TestControllerDropInWithoutFlags`, `TestAgentRoleUnits`, `TestRootedUnitsUseRootedPaths`, `TestCheckAgentAddress`, `TestEnabledUnits`, `TestWriteIfChanged`.

**Depends on:** Task 1

**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 2 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/install/units_test.go`:

```go
package install

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// unitsByPath renders the units of role for the production layout.
func unitsByPath(t *testing.T, role Role, listen []string, advertise string) map[string]string {
	t.Helper()
	files, err := Units(Production(), role, listen, advertise)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]string{}
	for _, f := range files {
		out[f.Path] = string(f.Content)
	}
	return out
}

func wantLines(t *testing.T, unit, text string, lines ...string) {
	t.Helper()
	have := strings.Split(text, "\n")
	for _, l := range lines {
		found := false
		for _, h := range have {
			found = found || h == l
		}
		if !found {
			t.Errorf("%s lacks the line %q:\n%s", unit, l, text)
		}
	}
	if strings.Contains(text, "{{") || strings.Contains(text, "<no value>") {
		t.Errorf("%s is not fully rendered:\n%s", unit, text)
	}
}

func TestControllerUnits(t *testing.T) {
	units := unitsByPath(t, RoleController, []string{"10.77.0.10:7443", "[fd7a::1]:7443"}, "10.77.0.10:7443")
	if len(units) != 5 {
		t.Fatalf("the controller role has %d unit files, want 5", len(units))
	}
	wantLines(t, "controller", units["/etc/systemd/system/darkpanel-controller.service"],
		"User=darkpanel", "ExecStart=/usr/local/lib/darkpanel/darkpanel serve", "RuntimeDirectory=darkpanel-controller",
		"Restart=on-failure", "RestartSec=2", "StartLimitIntervalSec=60", "StartLimitBurst=5", "TimeoutStartSec=30s",
		"TimeoutStopSec=10s", "OnFailure=darkpanel-rollback.service", "NoNewPrivileges=yes", "ProtectSystem=strict",
		"ReadWritePaths=/var/lib/darkpanel/controller", "PrivateTmp=yes", "WantedBy=multi-user.target")
	wantLines(t, "agent", units["/etc/systemd/system/darkpanel-agent.service"],
		"ExecStart=/usr/local/lib/darkpanel/darkpanel agent run", "RuntimeDirectory=darkpanel-agent", "RuntimeDirectoryMode=0700",
		"Restart=on-failure", "RestartSec=2", "StartLimitIntervalSec=60", "StartLimitBurst=5", "TimeoutStartSec=30s",
		"TimeoutStopSec=10s", "OnFailure=darkpanel-rollback.service", "WantedBy=multi-user.target")
	if strings.Contains(units["/etc/systemd/system/darkpanel-agent.service"], "User=") {
		t.Error("the agent unit names a user; the agent runs as root")
	}
	wantLines(t, "rollback", units["/etc/systemd/system/darkpanel-rollback.service"],
		"Type=oneshot", "ExecStart=/usr/local/lib/darkpanel/darkpanel.recovery update recover", "TimeoutStartSec=30s",
		"ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.recovery")
	if strings.Contains(units["/etc/systemd/system/darkpanel-rollback.service"], "[Install]") {
		t.Error("the rollback unit can be enabled; only OnFailure= may start it")
	}
	wantLines(t, "recover", units["/etc/systemd/system/darkpanel-update-recover.service"],
		"Type=oneshot", "ExecStart=/usr/local/lib/darkpanel/darkpanel.recovery update recover --boot", "TimeoutStartSec=30s",
		"ConditionPathExists=/usr/local/lib/darkpanel/darkpanel.recovery",
		"Before=darkpanel-controller.service darkpanel-agent.service", "WantedBy=multi-user.target")
	wantLines(t, "drop-in", units["/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf"],
		"[Service]", "ExecStart=",
		"ExecStart=/usr/local/lib/darkpanel/darkpanel serve --agent-listen 10.77.0.10:7443 --agent-listen [fd7a::1]:7443 --agent-advertise 10.77.0.10:7443")
}

func TestControllerDropInWithoutFlags(t *testing.T) {
	units := unitsByPath(t, RoleController, nil, "")
	wantLines(t, "drop-in", units["/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf"],
		"ExecStart=", "ExecStart=/usr/local/lib/darkpanel/darkpanel serve")
}

func TestAgentRoleUnits(t *testing.T) {
	units := unitsByPath(t, RoleAgent, nil, "")
	var got []string
	for p := range units {
		got = append(got, filepath.Base(p))
	}
	for _, name := range []string{"darkpanel-agent.service", "darkpanel-rollback.service", "darkpanel-update-recover.service"} {
		if _, ok := units["/etc/systemd/system/"+name]; !ok {
			t.Errorf("the agent role lacks %s (has %v)", name, got)
		}
	}
	if len(units) != 3 {
		t.Errorf("the agent role writes %v, want three units", got)
	}
	if _, err := Units(Production(), RoleAgent, []string{"10.0.0.1:7443"}, ""); err == nil {
		t.Error("the agent role accepted --agent-listen")
	}
	if _, err := Units(Production(), RoleAgent, nil, "10.0.0.1:7443"); err == nil {
		t.Error("the agent role accepted --agent-advertise")
	}
}

func TestRootedUnitsUseRootedPaths(t *testing.T) {
	root := t.TempDir()
	files, err := Units(Rooted(root), RoleController, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if !strings.HasPrefix(f.Path, root+"/") {
			t.Errorf("unit %s is outside %s", f.Path, root)
		}
		if strings.Contains(string(f.Content), "/usr/local/lib/darkpanel/darkpanel") && !strings.Contains(string(f.Content), root) {
			t.Errorf("%s runs the production binary under a rooted layout", f.Path)
		}
	}
}

func TestCheckAgentAddress(t *testing.T) {
	for _, ok := range []string{"10.77.0.10:7443", "[fd7a:115c::1]:7443", "controller.example.com:443", "h:1", "h:65535"} {
		if err := CheckAgentAddress(ok); err != nil {
			t.Errorf("CheckAgentAddress(%q) = %v", ok, err)
		}
	}
	for _, bad := range []string{"", "10.0.0.1", ":7443", "h:0", "h:65536", "h:port", "a b:1", "h:1;rm", "h:1\n", "$(id):1", "[::1]"} {
		if err := CheckAgentAddress(bad); err == nil {
			t.Errorf("CheckAgentAddress(%q) succeeded", bad)
		}
	}
	if _, err := Units(Production(), RoleController, []string{"h:1 --dev"}, ""); err == nil {
		t.Error("Units accepted a listen address carrying a flag")
	}
}

func TestEnabledUnits(t *testing.T) {
	if got, want := EnabledUnits(RoleController), []string{"darkpanel-controller.service", "darkpanel-agent.service", "darkpanel-update-recover.service"}; !reflect.DeepEqual(got, want) {
		t.Errorf("controller role enables %v, want %v", got, want)
	}
	if got, want := EnabledUnits(RoleAgent), []string{"darkpanel-agent.service", "darkpanel-update-recover.service"}; !reflect.DeepEqual(got, want) {
		t.Errorf("agent role enables %v, want %v", got, want)
	}
}

func TestWriteIfChanged(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "sub", "unit.service")
	for i, tt := range []struct {
		data  string
		wrote bool
	}{{"one\n", true}, {"one\n", false}, {"two\n", true}} {
		wrote, err := writeIfChanged(p, []byte(tt.data))
		if err != nil || wrote != tt.wrote {
			t.Fatalf("write %d: wrote %v, %v; want %v", i, wrote, err, tt.wrote)
		}
		if b, err := os.ReadFile(p); err != nil || string(b) != tt.data { //nolint:gosec // G304: a test's temporary path
			t.Fatalf("write %d: file holds %q, %v", i, b, err)
		}
	}
	info, err := os.Stat(p)
	if err != nil || info.Mode().Perm() != 0o644 {
		t.Errorf("mode %v, %v; want 0644", info.Mode().Perm(), err)
	}
	entries, err := os.ReadDir(filepath.Dir(p))
	if err != nil || len(entries) != 1 {
		t.Errorf("directory holds %v, %v; want only the unit, no temporary file", entries, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/3 && TMPDIR=/root/.cache/plan3-tmp/3 GOTOOLCHAIN=local go test -count=1 ./internal/install/`
Expected: FAIL, the build stops with `undefined: Units` (and the other names of `units.go`).

- [ ] **Step 3: Write the unit templates**

Create `internal/install/units/darkpanel-controller.service`:

```ini
# Written by darkpanel system install; rerun it rather than editing this file.
[Unit]
Description=Darkpanel controller
Documentation=https://github.com/Darkraise/darkpanel
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5
OnFailure=darkpanel-rollback.service

[Service]
User=darkpanel
ExecStart={{.Binary}} serve
RuntimeDirectory=darkpanel-controller
Restart=on-failure
RestartSec=2
TimeoutStartSec=30s
TimeoutStopSec=10s
NoNewPrivileges=yes
ProtectSystem=strict
ReadWritePaths={{.ControllerDir}}
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
```

Create `internal/install/units/darkpanel-agent.service`:

```ini
# Written by darkpanel system install; rerun it rather than editing this file.
[Unit]
Description=Darkpanel agent
Documentation=https://github.com/Darkraise/darkpanel
Wants=network-online.target
After=network-online.target
StartLimitIntervalSec=60
StartLimitBurst=5
OnFailure=darkpanel-rollback.service

[Service]
ExecStart={{.Binary}} agent run
RuntimeDirectory=darkpanel-agent
RuntimeDirectoryMode=0700
Restart=on-failure
RestartSec=2
TimeoutStartSec=30s
TimeoutStopSec=10s

[Install]
WantedBy=multi-user.target
```

Create `internal/install/units/darkpanel-rollback.service` (no `[Install]` section: only `OnFailure=` starts it):

```ini
# Written by darkpanel system install; rerun it rather than editing this file.
[Unit]
Description=Darkpanel update rollback after a failed service or runner
ConditionPathExists={{.Recovery}}

[Service]
Type=oneshot
ExecStart={{.Recovery}} update recover
TimeoutStartSec=30s
```

Create `internal/install/units/darkpanel-update-recover.service`:

```ini
# Written by darkpanel system install; rerun it rather than editing this file.
[Unit]
Description=Darkpanel update recovery at boot
ConditionPathExists={{.Recovery}}
Before=darkpanel-controller.service darkpanel-agent.service

[Service]
Type=oneshot
ExecStart={{.Recovery}} update recover --boot
TimeoutStartSec=30s

[Install]
WantedBy=multi-user.target
```

Create `internal/install/units/10-agent.conf` (the empty `ExecStart=` clears the unit's own before the drop-in sets it):

```ini
# Written by darkpanel system install from its --agent-listen and
# --agent-advertise flags; rerun it to change them.
[Service]
ExecStart=
ExecStart={{.Binary}} serve{{range .AgentListen}} --agent-listen {{.}}{{end}}{{if .AgentAdvertise}} --agent-advertise {{.AgentAdvertise}}{{end}}
```

- [ ] **Step 4: Write the renderer**

Create `internal/install/units.go`:

```go
package install

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"text/template"

	"github.com/darkraise/darkpanel/internal/update"
)

//go:embed units/*
var unitTemplates embed.FS

// UnitFile is one rendered unit file or drop-in.
type UnitFile struct {
	Path    string
	Content []byte
}

// unitData is what the templates in units/ read.
type unitData struct {
	Binary         string // the installed binary, which the services run
	Recovery       string // the recovery binary, which the rollback units run
	ControllerDir  string // the only directory the controller may write
	AgentListen    []string
	AgentAdvertise string
}

// agentAddress is the character set install.sh allows in the flags it
// passes on; the unit files hold the values unquoted.
var agentAddress = regexp.MustCompile(`^[][A-Za-z0-9.:-]+$`)

// CheckAgentAddress refuses a value of --agent-listen or --agent-advertise
// that is not host:port with a port from 1 to 65535.
func CheckAgentAddress(s string) error {
	host, port, err := net.SplitHostPort(s)
	n, perr := strconv.Atoi(port)
	if !agentAddress.MatchString(s) || err != nil || host == "" || perr != nil || n < 1 || n > 65535 {
		return fmt.Errorf("install: agent address %q is not host:port", s)
	}
	return nil
}

// Units renders the unit files of role: the agent, rollback and boot
// recovery units, and for the controller role also the controller unit and
// its drop-in holding the agent listener flags. Both flags are for the
// controller role only.
func Units(l Layout, role Role, agentListen []string, agentAdvertise string) ([]UnitFile, error) {
	if role != RoleController && (len(agentListen) > 0 || agentAdvertise != "") {
		return nil, errors.New("install: --agent-listen and --agent-advertise are for the controller role")
	}
	for _, a := range agentListen {
		if err := CheckAgentAddress(a); err != nil {
			return nil, err
		}
	}
	if agentAdvertise != "" {
		if err := CheckAgentAddress(agentAdvertise); err != nil {
			return nil, err
		}
	}
	data := unitData{
		Binary: l.Paths.Binary(), Recovery: l.Paths.RecoveryBinary(), ControllerDir: l.Paths.ControllerDir,
		AgentListen: agentListen, AgentAdvertise: agentAdvertise,
	}
	files := []struct{ template, path string }{
		{update.AgentUnit, l.UnitPath(update.AgentUnit)},
		{update.RollbackUnit, l.UnitPath(update.RollbackUnit)},
		{RecoverUnit, l.UnitPath(RecoverUnit)},
	}
	if role == RoleController {
		files = append(files,
			struct{ template, path string }{update.ControllerUnit, l.UnitPath(update.ControllerUnit)},
			struct{ template, path string }{DropInName, l.DropInPath()},
		)
	}
	out := make([]UnitFile, 0, len(files))
	for _, f := range files {
		content, err := renderUnit(f.template, data)
		if err != nil {
			return nil, err
		}
		out = append(out, UnitFile{Path: f.path, Content: content})
	}
	return out, nil
}

func renderUnit(name string, data unitData) ([]byte, error) {
	tmpl, err := template.ParseFS(unitTemplates, "units/"+name)
	if err != nil {
		return nil, fmt.Errorf("install: unit template %s: %w", name, err)
	}
	var b bytes.Buffer
	if err := tmpl.Execute(&b, data); err != nil {
		return nil, fmt.Errorf("install: render %s: %w", name, err)
	}
	return b.Bytes(), nil
}

// EnabledUnits are the unit files system install enables for role. The
// rollback unit has no [Install] section: only OnFailure= starts it.
func EnabledUnits(role Role) []string {
	units := []string{update.AgentUnit, RecoverUnit}
	if role == RoleController {
		units = append([]string{update.ControllerUnit}, units...)
	}
	return units
}

// writeIfChanged writes data to path with mode 0644 unless the file already
// holds exactly data, and reports whether it wrote. The new content goes to a
// temporary file in the same directory, synced and renamed over path, so a
// crash leaves the old file or the new one.
func writeIfChanged(path string, data []byte) (bool, error) {
	old, err := os.ReadFile(path) //nolint:gosec // G304: the path comes from the install layout
	switch {
	case err == nil && bytes.Equal(old, data):
		return false, nil
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return false, fmt.Errorf("install: read %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // G301: unit directories are world-readable
		return false, fmt.Errorf("install: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".tmp*")
	if err != nil {
		return false, fmt.Errorf("install: write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(0o644)
	}
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil {
		err = syncDir(dir)
	}
	if err != nil {
		return false, fmt.Errorf("install: write %s: %w", path, err)
	}
	return true, nil
}

// syncDir makes the entries of dir durable.
func syncDir(dir string) error {
	d, err := os.Open(dir) //nolint:gosec // G304: the path comes from the install layout
	if err != nil {
		return err
	}
	err = d.Sync()
	if cerr := d.Close(); err == nil {
		err = cerr
	}
	return err
}
```

- [ ] **Step 5: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/3 GOTOOLCHAIN=local
go test -count=1 -race -timeout 120s ./internal/install/
go vet ./internal/install/
golangci-lint run --allow-serial-runners ./internal/install/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/install`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/install/units.go internal/install/units internal/install/units_test.go
git commit -m "feat(install): add systemd unit templates"
```

### Task 4: Agent API client

**Files:**
- Create: `internal/agent/api/client.go`
- Test: `internal/agent/api/client_test.go`

**Interfaces:**
- Consumes: C2 (`prefix`, `problemContentType`, `problem`, `opRead`, `opPlan`, `ControllerHello`, `NewHandler`, `Config`), the handler half of C10, `transport.ControllerSession`, `transport.AgentSession`, `transport.MaxControllerStreams` (tests), `agentruntime.ProtocolVersion`.
- Produces: C4. Top-level names added: `ClientConfig`, `controllerPrincipal`, `Client`, `NewClient`, `request`, `Client.do`, `answerError`, `actionPath`, `rawOutput`, and the `core.AgentClient` methods of `Client`; tests: `tcpClient`, `TestClientRoundTrip`, `TestClientWithoutController`, `TestClientProblems`, `TestClientRefusesALargeAnswer`, `sessionPair`, `TestClientOverSession`.

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/agent/api/client_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/transport"
)

// tcpClient serves h on 127.0.0.1 and returns a client of it.
func tcpClient(t *testing.T, h http.Handler, controller *ControllerHello) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	addr := srv.Listener.Addr().String()
	return NewClient(ClientConfig{
		Dial: func(ctx context.Context) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, "tcp", addr)
		},
		Controller: controller,
	})
}

func TestClientRoundTrip(t *testing.T) {
	ctx := context.Background()
	var got []any
	hello := core.AgentHello{ProtocolVersion: 1, BinaryVersion: "0.2.0", OS: "linux", Arch: "arm64", HostID: "h1",
		JournalEpoch: "E1", Modules: []core.ModuleStatus{{ID: "ufw", Version: 1, Available: true}},
		SchemaHashes: map[string]core.SchemaHash{"core.update.apply@1": {Input: "aa", Output: "bb"}}}
	entry := core.JournalEntry{Seq: 3, At: testTime, Actor: "user:u1", Action: "demo.set", Version: 1, Phase: "applied",
		ChangeID: "c1", Input: json.RawMessage(`{"a":1}`), Result: "ok", Source: "203.0.113.7"}
	fake := &coretest.FakeAgent{
		HelloFunc: func(context.Context) (core.AgentHello, error) { return hello, nil },
		ReadFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (json.RawMessage, error) {
			got = append(got, "read", p, a, string(in))
			return json.RawMessage(`{"n":1}`), nil
		},
		ResourceFunc: func(_ context.Context, p core.Principal, name string) (json.RawMessage, error) {
			got = append(got, "resource", p, name)
			return nil, nil
		},
		PlanFunc: func(_ context.Context, p core.Principal, a core.ActionRef, in json.RawMessage) (core.PlanView, error) {
			got = append(got, "plan", p, a, string(in))
			return testPlan, nil
		},
		ApplyFunc: func(_ context.Context, p core.Principal, id string, o core.ApplyOptions) (core.ChangeView, error) {
			got = append(got, "apply", p, id, o)
			return testChange, nil
		},
		ConfirmFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "confirm", p, id)
			return testChange, nil
		},
		RevertFunc: func(_ context.Context, p core.Principal, id string) (core.ChangeView, error) {
			got = append(got, "revert", p, id)
			return testChange, nil
		},
		ChangeFunc: func(_ context.Context, id string) (core.ChangeView, error) {
			got = append(got, "change", id)
			return testChange, nil
		},
		ChangesFunc: func(_ context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
			got = append(got, "changes", f)
			return []core.ChangeView{testChange}, nil
		},
		PreflightFunc: func(_ context.Context, id string) (core.ModuleStatus, error) {
			got = append(got, "preflight", id)
			return core.ModuleStatus{ID: id, Version: 2, Available: true}, nil
		},
		JournalFunc: func(_ context.Context, after int64, limit int) ([]core.JournalEntry, error) {
			got = append(got, "journal", after, limit)
			return []core.JournalEntry{entry}, nil
		},
		AckFunc: func(_ context.Context, seq int64) error {
			got = append(got, "ack", seq)
			return nil
		},
	}
	var hellos []ControllerHello
	h := NewHandler(Config{Agent: fake, Log: discardLog(), OnHello: func(c ControllerHello) { hellos = append(hellos, c) }})
	var mu sync.Mutex
	principals := map[string]string{}
	recorded := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		principals[r.Method+" "+r.URL.Path] = r.Header.Get(wire.HeaderPrincipal)
		mu.Unlock()
		h.ServeHTTP(w, r)
	})
	c := tcpClient(t, recorded, &ControllerHello{Version: "0.2.1", ControllerID: "C1"})
	ref := core.ActionRef{Name: "hostinfo.summary", Version: 1}
	opts := core.ApplyOptions{ConfirmTimeout: 2 * time.Minute, Confirmation: "delete h1", ConfirmNow: true}
	filter := core.ChangeFilter{State: "pending", HostID: "h1", Before: "c9", Limit: 10}

	if v, err := c.Hello(ctx); err != nil || !reflect.DeepEqual(v, hello) {
		t.Errorf("Hello = %+v, %v", v, err)
	}
	if want := []ControllerHello{{Protocol: 1, Version: "0.2.1", ControllerID: "C1"}}; !reflect.DeepEqual(hellos, want) {
		t.Errorf("the agent saw the hello %+v, want %+v", hellos, want)
	}
	if out, err := c.Read(ctx, testPrincipal, ref, json.RawMessage(`{"x":true}`)); err != nil || string(out) != `{"n":1}` {
		t.Errorf("Read = %s, %v", out, err)
	}
	if out, err := c.Resource(ctx, testPrincipal, "hostinfo.status"); err != nil || out != nil {
		t.Errorf("Resource = %s, %v; want no output", out, err)
	}
	if v, err := c.Plan(ctx, testPrincipal, ref, nil); err != nil || !reflect.DeepEqual(v, testPlan) {
		t.Errorf("Plan = %+v, %v", v, err)
	}
	for name, call := range map[string]func() (core.ChangeView, error){
		"Apply":   func() (core.ChangeView, error) { return c.Apply(ctx, testPrincipal, "p/1", opts) },
		"Confirm": func() (core.ChangeView, error) { return c.Confirm(ctx, testPrincipal, "c1") },
		"Revert":  func() (core.ChangeView, error) { return c.Revert(ctx, testPrincipal, "c1") },
		"Change":  func() (core.ChangeView, error) { return c.Change(ctx, "c1") },
	} {
		if v, err := call(); err != nil || !reflect.DeepEqual(v, testChange) {
			t.Errorf("%s = %+v, %v", name, v, err)
		}
	}
	if vs, err := c.Changes(ctx, filter); err != nil || !reflect.DeepEqual(vs, []core.ChangeView{testChange}) {
		t.Errorf("Changes = %+v, %v", vs, err)
	}
	if st, err := c.Preflight(ctx, "ufw"); err != nil || st != (core.ModuleStatus{ID: "ufw", Version: 2, Available: true}) {
		t.Errorf("Preflight = %+v, %v", st, err)
	}
	if es, err := c.Journal(ctx, 2, 0); err != nil || !reflect.DeepEqual(es, []core.JournalEntry{entry}) {
		t.Errorf("Journal = %+v, %v", es, err)
	}
	if err := c.Ack(ctx, 3); err != nil {
		t.Errorf("Ack: %v", err)
	}

	// Map iteration order varies, so the change calls are checked as a set.
	want := []any{
		"read", testPrincipal, ref, `{"x":true}`,
		"resource", testPrincipal, "hostinfo.status",
		"plan", testPrincipal, ref, "null",
	}
	if !reflect.DeepEqual(got[:len(want)], want) {
		t.Errorf("first calls\n got %v\nwant %v", got[:len(want)], want)
	}
	rest := got[len(want):]
	for _, w := range [][]any{
		{"apply", testPrincipal, "p/1", opts}, {"confirm", testPrincipal, "c1"}, {"revert", testPrincipal, "c1"}, {"change", "c1"},
	} {
		found := false
		for i := 0; i+len(w) <= len(rest); i++ {
			found = found || reflect.DeepEqual(rest[i:i+len(w)], w)
		}
		if !found {
			t.Errorf("no call %v in %v", w, rest)
		}
	}
	tail := []any{"changes", filter, "preflight", "ufw", "journal", int64(2), 0, "ack", int64(3)}
	if !reflect.DeepEqual(rest[len(rest)-len(tail):], tail) {
		t.Errorf("last calls\n got %v\nwant %v", rest[len(rest)-len(tail):], tail)
	}

	// The controller sends Darkpanel-Principal on every request: the caller's
	// where the method names one, its own elsewhere.
	if len(principals) != 12 {
		t.Errorf("the agent saw %d routes, want 12: %v", len(principals), principals)
	}
	for route, raw := range principals {
		var p wire.Principal
		if err := json.Unmarshal([]byte(raw), &p); err != nil {
			t.Errorf("%s: Darkpanel-Principal %q: %v", route, raw, err)
			continue
		}
		want := controllerPrincipal
		if strings.Contains(route, "/actions/") || strings.Contains(route, "/resources/") || strings.Contains(route, "/plans/") ||
			strings.HasSuffix(route, "/confirm") || strings.HasSuffix(route, "/revert") {
			want = testPrincipal
		}
		if got := p.Core(); got != want {
			t.Errorf("%s: Darkpanel-Principal = %+v, want %+v", route, got, want)
		}
	}
}

func TestClientWithoutController(t *testing.T) {
	fake := &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }}
	// A transport handler refuses a hello without the controller's half,
	// which is how a socket client's hello would look to it.
	h := NewHandler(Config{Agent: fake, Log: discardLog()})
	var principal atomic.Value
	c := tcpClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		principal.Store(r.Header.Get(wire.HeaderPrincipal))
		h.ServeHTTP(w, r)
	}), nil)
	var e *errcode.Error
	if _, err := c.Hello(context.Background()); !errors.As(err, &e) || e.Code != errcode.InvalidInput {
		t.Errorf("Hello without a controller = %v, want request.invalid_input from the transport handler", err)
	}
	// A socket client is not the controller and sends no principal of its own.
	if got := principal.Load(); got != "" {
		t.Errorf("Hello without a controller sent Darkpanel-Principal %q", got)
	}
}

func TestClientProblems(t *testing.T) {
	ctx := context.Background()
	fields := []errcode.FieldError{{Path: "/name", Message: "required"}}
	fail := error(&errcode.Error{Code: errcode.PlanExpired, Detail: "plan p1 expired", Fields: fields})
	fake := &coretest.FakeAgent{
		ApplyFunc: func(context.Context, core.Principal, string, core.ApplyOptions) (core.ChangeView, error) {
			return core.ChangeView{}, fail
		},
	}
	c := tcpClient(t, NewHandler(Config{Agent: fake, Log: discardLog()}), nil)
	_, err := c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{})
	var e *errcode.Error
	if !errors.As(err, &e) || e.Code != errcode.PlanExpired || e.Detail != "plan p1 expired" || !reflect.DeepEqual(e.Fields, fields) {
		t.Errorf("Apply = %v, want plan.expired with its detail and fields", err)
	}
	fail = errors.New("secret cause")
	if _, err = c.Apply(ctx, testPrincipal, "p1", core.ApplyOptions{}); !errors.As(err, &e) || e.Code != errcode.Internal || strings.Contains(e.Detail, "secret") {
		t.Errorf("Apply = %v, want internal without the cause", err)
	}

	// A code this binary does not know is kept, so the controller reports
	// what a newer agent said.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/agent/v1/changes/c1" {
			w.Header().Set("Content-Type", problemContentType)
			w.WriteHeader(http.StatusConflict)
			_, _ = w.Write([]byte(`{"status":409,"code":"newer.code","detail":"later"}`))
			return
		}
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer srv.Close()
	raw := NewClient(ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "tcp", srv.Listener.Addr().String())
	}})
	if _, err := raw.Change(ctx, "c1"); !errors.As(err, &e) || e.Code != "newer.code" || e.Detail != "later" {
		t.Errorf("Change = %v, want the unknown code kept", err)
	}
	if _, err := raw.Change(ctx, "c2"); err == nil || errors.As(err, &e) || !strings.Contains(err.Error(), "502") {
		t.Errorf("Change = %v, want a plain error naming 502", err)
	}
	if _, err := raw.Resource(ctx, testPrincipal, "nodot"); !errors.As(err, &e) || e.Code != errcode.NotFound {
		t.Errorf("Resource without a module = %v, want not_found", err)
	}
}

func TestClientRefusesALargeAnswer(t *testing.T) {
	big := json.RawMessage(`"` + strings.Repeat("a", wire.MaxBody) + `"`)
	fake := &coretest.FakeAgent{
		ReadFunc: func(context.Context, core.Principal, core.ActionRef, json.RawMessage) (json.RawMessage, error) {
			return big, nil
		},
	}
	c := tcpClient(t, NewHandler(Config{Agent: fake, Log: discardLog()}), nil)
	_, err := c.Read(context.Background(), testPrincipal, core.ActionRef{Name: "demo.get", Version: 1}, json.RawMessage(`{}`))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("Read of a 4 MiB answer = %v, want it refused", err)
	}
}

// sessionPair connects a controller and an agent session over loopback TCP;
// the agent serves h on the streams the controller opens.
func sessionPair(t *testing.T, h http.Handler) *transport.Session {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	accepted := make(chan net.Conn, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			close(accepted)
			return
		}
		accepted <- c
	}()
	conn, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	agentConn, ok := <-accepted
	if !ok {
		t.Fatal("accept failed")
	}
	ctrl, err := transport.ControllerSession(conn)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := transport.AgentSession(agentConn)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(agent) }()
	t.Cleanup(func() {
		_ = ctrl.Close()
		_ = srv.Close()
	})
	return ctrl
}

func TestClientOverSession(t *testing.T) {
	fake := &coretest.FakeAgent{HelloFunc: func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }}
	ctrl := sessionPair(t, NewHandler(Config{Agent: fake, Log: discardLog()}))
	c := NewClient(ClientConfig{Dial: ctrl.Open, Controller: &ControllerHello{Version: "0.2.0", ControllerID: "C1"}})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// More requests than the session has stream slots, one after another and
	// then together: each request must give its stream back.
	for i := range 3 * transport.MaxControllerStreams {
		if _, err := c.Hello(ctx); err != nil {
			t.Fatalf("hello %d: %v", i, err)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 2*transport.MaxControllerStreams)
	for range 2 * transport.MaxControllerStreams {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Hello(ctx); err != nil {
				errs <- err
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent hello: %v", err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/4 && TMPDIR=/root/.cache/plan3-tmp/4 GOTOOLCHAIN=local go test -count=1 ./internal/agent/api/`
Expected: FAIL, the build stops with `undefined: NewClient` (and the other names of `client.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/agent/api/client.go`:

```go
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// ClientConfig is where a client sends its requests.
type ClientConfig struct {
	// Dial opens one connection to the agent API: a transport.Session's
	// Open, or a dial of the agent socket.
	Dial func(ctx context.Context) (net.Conn, error)
	// Controller is sent in the headers of GET hello; nil sends none, as a
	// client of the agent socket does. Its Protocol is ignored: the client
	// always sends this binary's protocol.
	Controller *ControllerHello
}

// controllerPrincipal is what a controller's client sends as
// Darkpanel-Principal on the routes whose method names no caller: the
// protocol sends the header on every request (ADR 0012), and the agent reads
// it only where it acts for a caller, so this principal is never recorded.
var controllerPrincipal = core.Principal{Actor: "system:controller", Scope: core.ScopeAdmin}

// Client is a core.AgentClient over the agent API.
type Client struct {
	cfg  ClientConfig
	http *http.Client
}

var _ core.AgentClient = (*Client)(nil)

// NewClient returns a client for cfg. Requests end with their context; the
// client sets no timeout of its own.
func NewClient(cfg ClientConfig) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) { return cfg.Dial(ctx) },
		// Each request opens a stream and closes it: an idle pooled stream
		// would keep one of the session's stream slots.
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 64 << 10,
	}
	return &Client{cfg: cfg, http: &http.Client{Transport: tr}}
}

// request is one call of the agent API.
type request struct {
	method, path string
	query        url.Values
	principal    *core.Principal
	body         any // nil sends none; a json.RawMessage is sent as it is
	hello        bool
}

// do sends req and decodes a 2xx answer into out (nil discards it). An
// answer in problem details becomes an *errcode.Error with the agent's code,
// detail and field errors.
func (c *Client) do(ctx context.Context, req request, out any) error {
	what := req.method + " " + req.path
	var body io.Reader
	if req.body != nil {
		b, ok := req.body.(json.RawMessage)
		if !ok {
			var err error
			if b, err = json.Marshal(req.body); err != nil {
				return fmt.Errorf("api: %s: encode the request: %w", what, err)
			}
		}
		if len(b) == 0 {
			b = json.RawMessage("null")
		}
		body = bytes.NewReader(b)
	}
	u := "http://agent" + prefix + req.path
	if len(req.query) > 0 {
		u += "?" + req.query.Encode()
	}
	hr, err := http.NewRequestWithContext(ctx, req.method, u, body)
	if err != nil {
		return fmt.Errorf("api: %s: %w", what, err)
	}
	hr.Header.Set("Accept", "application/json")
	if body != nil {
		hr.Header.Set("Content-Type", "application/json")
	}
	principal := req.principal
	if principal == nil && c.cfg.Controller != nil {
		principal = &controllerPrincipal
	}
	if principal != nil {
		b, err := json.Marshal(wire.FromPrincipal(*principal))
		if err != nil {
			return fmt.Errorf("api: %s: encode the principal: %w", what, err)
		}
		hr.Header.Set(wire.HeaderPrincipal, string(b))
	}
	if req.hello && c.cfg.Controller != nil {
		hr.Header.Set(wire.HeaderProtocol, strconv.Itoa(agentruntime.ProtocolVersion))
		hr.Header.Set(wire.HeaderVersion, c.cfg.Controller.Version)
		hr.Header.Set(wire.HeaderControllerID, c.cfg.Controller.ControllerID)
	}
	resp, err := c.http.Do(hr)
	if err != nil {
		return fmt.Errorf("api: %s: %w", what, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, wire.MaxBody+1))
	if err != nil {
		return fmt.Errorf("api: %s: read the answer: %w", what, err)
	}
	if len(data) > wire.MaxBody {
		return fmt.Errorf("api: %s: the answer exceeds %d bytes", what, wire.MaxBody)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return answerError(what, resp, data)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("api: %s: decode the answer: %w", what, err)
	}
	return nil
}

// answerError turns a non-2xx answer into an *errcode.Error when it carries
// problem details with a code, else into a plain error naming the status.
// The code is kept even when this binary does not know it.
func answerError(what string, resp *http.Response, data []byte) error {
	if mt, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type")); err == nil && mt == problemContentType {
		var p problem
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			return &errcode.Error{Code: p.Code, Detail: p.Detail, Fields: p.Errors}
		}
	}
	return fmt.Errorf("api: %s: unexpected answer %s", what, resp.Status)
}

// actionPath is the route of op on action a.
func actionPath(a core.ActionRef, op string) string {
	return "actions/" + url.PathEscape(a.Name) + "/v" + strconv.Itoa(a.Version) + "/" + op
}

// rawOutput maps JSON null to nil, as the runtime returns no output.
func rawOutput(m json.RawMessage) json.RawMessage {
	if bytes.Equal(m, []byte("null")) {
		return nil
	}
	return m
}

// Hello asks the agent about itself, sending the controller's half when the
// client has one.
func (c *Client) Hello(ctx context.Context) (core.AgentHello, error) {
	var h wire.Hello
	if err := c.do(ctx, request{method: http.MethodGet, path: "hello", hello: true}, &h); err != nil {
		return core.AgentHello{}, err
	}
	return h.Core(), nil
}

// Read runs an agent read action.
func (c *Client) Read(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
	var out json.RawMessage
	err := c.do(ctx, request{method: http.MethodPost, path: actionPath(a, opRead), principal: &p, body: input}, &out)
	if err != nil {
		return nil, err
	}
	return rawOutput(out), nil
}

// Resource reads a resource named "<module>.<resource>".
func (c *Client) Resource(ctx context.Context, p core.Principal, name string) (json.RawMessage, error) {
	module, resource, ok := strings.Cut(name, ".")
	if !ok {
		return nil, errcode.New(errcode.NotFound, "no resource "+name)
	}
	var out json.RawMessage
	path := "resources/" + url.PathEscape(module) + "/" + url.PathEscape(resource)
	if err := c.do(ctx, request{method: http.MethodGet, path: path, principal: &p}, &out); err != nil {
		return nil, err
	}
	return rawOutput(out), nil
}

// Plan plans an agent action.
func (c *Client) Plan(ctx context.Context, p core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
	var v wire.PlanView
	if err := c.do(ctx, request{method: http.MethodPost, path: actionPath(a, opPlan), principal: &p, body: input}, &v); err != nil {
		return core.PlanView{}, err
	}
	return v.Core(), nil
}

// Apply applies a plan.
func (c *Client) Apply(ctx context.Context, p core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "plans/" + url.PathEscape(planID) + "/apply", principal: &p,
		body: wire.FromApplyOptions(opts)})
}

// Confirm confirms a pending change.
func (c *Client) Confirm(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "changes/" + url.PathEscape(changeID) + "/confirm", principal: &p})
}

// Revert reverts a pending change.
func (c *Client) Revert(ctx context.Context, p core.Principal, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodPost, path: "changes/" + url.PathEscape(changeID) + "/revert", principal: &p})
}

// Change returns one change.
func (c *Client) Change(ctx context.Context, changeID string) (core.ChangeView, error) {
	return c.change(ctx, request{method: http.MethodGet, path: "changes/" + url.PathEscape(changeID)})
}

func (c *Client) change(ctx context.Context, req request) (core.ChangeView, error) {
	var v wire.ChangeView
	if err := c.do(ctx, req, &v); err != nil {
		return core.ChangeView{}, err
	}
	return v.Core(), nil
}

// Changes lists changes matching f.
func (c *Client) Changes(ctx context.Context, f core.ChangeFilter) ([]core.ChangeView, error) {
	var vs []wire.ChangeView
	if err := c.do(ctx, request{method: http.MethodGet, path: "changes", query: wire.ChangeFilterQuery(f)}, &vs); err != nil {
		return nil, err
	}
	out := make([]core.ChangeView, len(vs))
	for i, v := range vs {
		out[i] = v.Core()
	}
	return out, nil
}

// Preflight runs a module's preflight checks again.
func (c *Client) Preflight(ctx context.Context, moduleID string) (core.ModuleStatus, error) {
	var st wire.ModuleStatus
	if err := c.do(ctx, request{method: http.MethodGet, path: "modules/" + url.PathEscape(moduleID) + "/preflight"}, &st); err != nil {
		return core.ModuleStatus{}, err
	}
	return st.Core(), nil
}

// Journal returns up to limit journal entries after afterSeq; a limit of 0
// leaves the count to the agent.
func (c *Client) Journal(ctx context.Context, afterSeq int64, limit int) ([]core.JournalEntry, error) {
	q := url.Values{"after": {strconv.FormatInt(afterSeq, 10)}}
	if limit > 0 {
		q.Set("limit", strconv.Itoa(limit))
	}
	var es []wire.JournalEntry
	if err := c.do(ctx, request{method: http.MethodGet, path: "journal", query: q}, &es); err != nil {
		return nil, err
	}
	out := make([]core.JournalEntry, len(es))
	for i, e := range es {
		out[i] = e.Core()
	}
	return out, nil
}

// Ack tells the agent the controller stored the journal up to seq.
func (c *Client) Ack(ctx context.Context, seq int64) error {
	return c.do(ctx, request{method: http.MethodPost, path: "journal/ack", body: wire.Ack{Seq: seq}}, nil)
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/4 GOTOOLCHAIN=local
go test -count=1 -race -timeout 120s ./internal/agent/api/
go vet ./internal/agent/api/
golangci-lint run --allow-serial-runners ./internal/agent/api/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/agent/api`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/api/client.go internal/agent/api/client_test.go
git commit -m "feat(agent): add the agent API client"
```

### Task 5: Ownership kinds migration

**Files:**
- Create: `internal/core/store/migrations/agent/0005_ownership_kinds.sql`
- Modify: `internal/platform/files.go:38-40` (the `Files` doc comment), `internal/platform/files.go:56-58` (the declaration loop of `NewFiles`)
- Test: `internal/platform/files_test.go`, `internal/platform/ownership_test.go`

**Interfaces:**
- Consumes: `store.Open`, `store.CoreMigrations`, `store.Migrate`, `store.MigrationSet`, `store.MigrateOptions` (tests), `platform.NewOwnership`, the unexported `kindFile` and `kindBlock` of `internal/platform/files.go:23-24`, and the existing `fakeOwnership`, `filesNow`, `dirNames` and `openAgentDB` test helpers of `internal/platform`.
- Produces: C5. Top-level names added: tests `TestOtherKindsIgnored`, `TestOwnershipRecordsEveryKind`, `TestOwnershipKindsMigrationKeepsEntries`.

**Depends on:** none

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing tests**

Run `mkdir -p /root/.cache/plan3-tmp/5`, then save this patch as `/root/.cache/plan3-tmp/5/tests.patch` and apply it from the repository root with `git apply /root/.cache/plan3-tmp/5/tests.patch`. It adds `TestOtherKindsIgnored` to `files_test.go`, and to `ownership_test.go` the new invalid-entry rows, `TestOwnershipRecordsEveryKind` and `TestOwnershipKindsMigrationKeepsEntries`, which also records a `dir` entry after the migration; the old row `Kind: "dir"` becomes `Kind: "socket"` because `dir` is now valid.

```diff
diff --git a/internal/platform/files_test.go b/internal/platform/files_test.go
index ccc2230..a6e3e3d 100644
--- a/internal/platform/files_test.go
+++ b/internal/platform/files_test.go
@@ -180,6 +180,44 @@ func TestUndeclaredWritesRefused(t *testing.T) {
 	}
 }
 
+// Directories, users and units are uninstall's to remove: Files neither
+// writes nor removes them, and holds no lock for them.
+func TestOtherKindsIgnored(t *testing.T) {
+	dir := t.TempDir()
+	own := &fakeOwnership{}
+	data, unit := filepath.Join(dir, "data"), filepath.Join(dir, "demo.service")
+	f := NewFiles(FilesConfig{
+		Owned: map[string][]core.OwnedPath{"demo": {
+			{Path: data, Kind: core.OwnedDir},
+			{Path: "demo-user", Kind: core.OwnedUser},
+			{Path: unit, Kind: core.OwnedUnit},
+		}},
+		Ownership: own,
+		Clock:     coretest.NewFakeClock(filesNow),
+		TestMode:  true,
+	})
+	ctx := context.Background()
+	attempts := map[string]error{
+		"write a directory": f.WriteFile(ctx, "demo", data, []byte("x"), 0o644),
+		"write a unit":      f.WriteFile(ctx, "demo", unit, []byte("x"), 0o644),
+		"remove a unit":     f.Remove(ctx, "demo", unit),
+	}
+	for name, err := range attempts {
+		if err == nil {
+			t.Errorf("%s: succeeded, want a refusal", name)
+		}
+	}
+	if len(f.locks) != 0 {
+		t.Errorf("Files holds %d path locks, want none", len(f.locks))
+	}
+	if names := dirNames(t, dir); len(names) != 0 {
+		t.Errorf("directory holds %v, want nothing written", names)
+	}
+	if got, _ := own.List(ctx); len(got) != 0 {
+		t.Errorf("ownership = %+v, want nothing recorded", got)
+	}
+}
+
 func TestSymlinkedParentRefused(t *testing.T) {
 	dir := t.TempDir()
 	realDir := filepath.Join(dir, "real")
diff --git a/internal/platform/ownership_test.go b/internal/platform/ownership_test.go
index 99d1375..3100f03 100644
--- a/internal/platform/ownership_test.go
+++ b/internal/platform/ownership_test.go
@@ -2,9 +2,12 @@ package platform
 
 import (
 	"context"
+	"io/fs"
 	"path/filepath"
 	"reflect"
+	"strings"
 	"testing"
+	"testing/fstest"
 	"time"
 
 	"github.com/darkraise/darkpanel/internal/core"
@@ -80,8 +83,11 @@ func TestOwnershipRefusesInvalidEntries(t *testing.T) {
 	o := NewOwnership(openAgentDB(t))
 	ctx := context.Background()
 	bad := []core.OwnedEntry{
-		{Path: "/etc/a", Module: "demo", Kind: "dir"},
+		{Path: "/etc/a", Module: "demo", Kind: "socket"},
 		{Path: "/etc/a", Module: "demo", Kind: "file", BlockID: "x"},
+		{Path: "/etc/a", Module: "demo", Kind: "dir", BlockID: "x"},
+		{Path: "demo", Module: "demo", Kind: "user", BlockID: "x"},
+		{Path: "/etc/a.service", Module: "demo", Kind: "unit", BlockID: "x"},
 		{Path: "/etc/a", Module: "demo", Kind: "block"},
 	}
 	for _, e := range bad {
@@ -90,3 +96,95 @@ func TestOwnershipRefusesInvalidEntries(t *testing.T) {
 		}
 	}
 }
+
+func TestOwnershipRecordsEveryKind(t *testing.T) {
+	o := NewOwnership(openAgentDB(t))
+	ctx := context.Background()
+	at := time.Date(2026, 10, 20, 1, 2, 3, 0, time.UTC)
+	want := []core.OwnedEntry{
+		{Path: "/etc/systemd/system/darkpanel-agent.service", Module: "core", Kind: core.OwnedUnit, CreatedAt: at},
+		{Path: "/var/lib/darkpanel", Module: "core", Kind: core.OwnedDir, CreatedAt: at},
+		{Path: "darkpanel", Module: "core", Kind: core.OwnedUser, CreatedAt: at},
+	}
+	for _, e := range want {
+		if err := o.Record(ctx, e); err != nil {
+			t.Fatalf("Record(%+v): %v", e, err)
+		}
+	}
+	got, err := o.List(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !reflect.DeepEqual(got, want) {
+		t.Errorf("List = %+v, want %+v", got, want)
+	}
+}
+
+// TestOwnershipKindsMigrationKeepsEntries applies the agent migrations before
+// the ownership kinds, records entries, and applies the rest.
+func TestOwnershipKindsMigrationKeepsEntries(t *testing.T) {
+	ctx := context.Background()
+	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "agent.db"))
+	if err != nil {
+		t.Fatal(err)
+	}
+	t.Cleanup(func() { _ = db.Close() })
+	full, err := store.CoreMigrations(store.SideAgent)
+	if err != nil {
+		t.Fatal(err)
+	}
+	early := fstest.MapFS{}
+	err = fs.WalkDir(full.FS, ".", func(p string, d fs.DirEntry, err error) error {
+		if err != nil || d.IsDir() || p >= "0005" {
+			return err
+		}
+		b, err := fs.ReadFile(full.FS, p)
+		early[p] = &fstest.MapFile{Data: b}
+		return err
+	})
+	if err != nil {
+		t.Fatal(err)
+	}
+	migrate := func(set store.MigrationSet) {
+		t.Helper()
+		opts := store.MigrateOptions{
+			Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(t.TempDir(), "backups"), Keep: 5, Now: time.Now(),
+		}
+		if _, err := store.Migrate(ctx, db, opts, set, nil); err != nil {
+			t.Fatal(err)
+		}
+	}
+	migrate(store.MigrationSet{Module: full.Module, FS: early})
+	o := NewOwnership(db)
+	at := time.Date(2026, 10, 17, 1, 2, 3, 0, time.UTC)
+	before := []core.OwnedEntry{
+		{Path: "/etc/a.conf", Module: "demo", Kind: core.OwnedFile, CreatedAt: at},
+		{Path: "/etc/hosts", Module: "demo", Kind: core.OwnedBlock, BlockID: "names", CreatedAt: at},
+	}
+	for _, e := range before {
+		if err := o.Record(ctx, e); err != nil {
+			t.Fatal(err)
+		}
+	}
+	if err := o.Record(ctx, core.OwnedEntry{Path: "/var/lib/x", Module: "demo", Kind: core.OwnedDir, CreatedAt: at}); err == nil {
+		t.Fatal("the old table accepted kind dir; the test is not running before the migration")
+	}
+	migrate(full)
+	got, err := o.List(ctx)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if !reflect.DeepEqual(got, before) {
+		t.Errorf("after the migration List = %+v, want %+v", got, before)
+	}
+	var sql string
+	if err := db.Read().QueryRowContext(ctx, `SELECT sql FROM sqlite_master WHERE name = 'core_owned_files'`).Scan(&sql); err != nil {
+		t.Fatal(err)
+	}
+	if !strings.Contains(sql, "STRICT") || strings.Contains(sql, "core_owned_files_new") {
+		t.Errorf("the rebuilt table is %s", sql)
+	}
+	if err := o.Record(ctx, core.OwnedEntry{Path: "/var/lib/x", Module: "demo", Kind: core.OwnedDir, CreatedAt: at}); err != nil {
+		t.Errorf("after the migration Record(dir): %v", err)
+	}
+}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `TMPDIR=/root/.cache/plan3-tmp/5 GOTOOLCHAIN=local go test -count=1 ./internal/platform/`
Expected: FAIL with `TestOtherKindsIgnored` (`Files holds 3 path locks, want none`), `TestOwnershipRecordsEveryKind` and `TestOwnershipKindsMigrationKeepsEntries` (both `CHECK constraint failed: kind IN ('file', 'block')`, the latter after its entries survive the other migrations).

- [ ] **Step 3: Write the migration**

Create `internal/core/store/migrations/agent/0005_ownership_kinds.sql`:

```sql
-- Uninstall also removes the directories, users and units Darkpanel created
-- (spec section 7), so the manifest gains those kinds. SQLite cannot change a
-- CHECK constraint in place, so the table is rebuilt. Only a marked block has
-- a block id.

CREATE TABLE core_owned_files_new (
    path       TEXT NOT NULL,
    module     TEXT NOT NULL,
    kind       TEXT NOT NULL CHECK (kind IN ('file', 'block', 'dir', 'user', 'unit')),
    block_id   TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    PRIMARY KEY (path, module, block_id),
    CHECK ((kind = 'block') = (block_id <> ''))
) STRICT;

INSERT INTO core_owned_files_new (path, module, kind, block_id, created_at)
    SELECT path, module, kind, block_id, created_at FROM core_owned_files;

DROP TABLE core_owned_files;

ALTER TABLE core_owned_files_new RENAME TO core_owned_files;
```

- [ ] **Step 4: Make Files ignore the new kinds**

Save this patch as `/root/.cache/plan3-tmp/5/files.patch` and apply it with `git apply /root/.cache/plan3-tmp/5/files.patch`:

```diff
diff --git a/internal/platform/files.go b/internal/platform/files.go
index e27b0f1..63e2736 100644
--- a/internal/platform/files.go
+++ b/internal/platform/files.go
@@ -36,8 +36,10 @@ type FilesConfig struct {
 }
 
 // Files is the core.FileWriter of the agent: it writes only the paths and
-// marked blocks a module declares, never through a symlink (ADR 0009). Calls
-// on one path are serialised, so the agent must build exactly one Files.
+// marked blocks a module declares, never through a symlink (ADR 0009).
+// Declarations of the other kinds (directories, users, units) are uninstall's
+// to remove and are ignored here. Calls on one path are serialised, so the
+// agent must build exactly one Files.
 type Files struct {
 	cfg       FilesConfig
 	parentUID int
@@ -55,6 +57,9 @@ func NewFiles(cfg FilesConfig) *Files {
 	locks := map[string]*sync.Mutex{}
 	for _, paths := range cfg.Owned {
 		for _, op := range paths {
+			if op.Kind != kindFile && op.Kind != kindBlock {
+				continue
+			}
 			if locks[op.Path] == nil {
 				locks[op.Path] = &sync.Mutex{}
 			}
```

- [ ] **Step 5: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/5 GOTOOLCHAIN=local
go test -count=1 -race -timeout 300s ./internal/platform/ ./internal/core/store/... ./internal/agent/...
go vet ./internal/platform/
golangci-lint run --allow-serial-runners ./internal/platform/...
go mod tidy -diff
```
Expected: every package `ok`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.` The store and agent packages run every agent migration, so they prove the rebuild applies on a fresh agent.db.

- [ ] **Step 6: Commit**

```bash
git add internal/core/store/migrations/agent/0005_ownership_kinds.sql internal/platform/files.go internal/platform/files_test.go internal/platform/ownership_test.go
git commit -m "feat(platform): widen owned file kinds"
```

### Task 6: Agent socket rules

**Files:**
- Create: `internal/agent/api/socket.go`
- Test: `internal/agent/api/socket_test.go`

**Interfaces:**
- Consumes: C2 (`handler`, `handler.routes`, `Config`, `opPlan`, `opApply`, `opConfirm`, `opRevert`, `opRead`, `opResource`), the handler half of C10, `core.ActorUnenrolled`, `core.ScopeAdmin`, `core.ModuleIDCore`, `errcode.AgentNotEnrolled`, `errcode.ModuleUnavailable`.
- Produces: C6. Top-level names added: `SocketSource`, `SocketConfig`, `NewSocketHandler`, `socketPrincipal`, `handler.socketAllow`, `handler.preflight`; tests: `socketFake`, `newSocketFake`, `socketHandler`, `TestSocketPrincipalIsLocalRoot`, `TestSocketBeforeEnrollment`, `TestSocketPreflight`, `TestSocketServesNoJournal`.

**Depends on:** Task 2

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/agent/api/socket_test.go`:

```go
package api

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/wire"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// socketFake is an agent whose module "ufw" fails its preflight and whose
// module "hostinfo" passes; it records the principal of every call.
type socketFake struct {
	coretest.FakeAgent
	principals []core.Principal
	preflights []string
}

func newSocketFake() *socketFake {
	f := &socketFake{}
	record := func(p core.Principal) { f.principals = append(f.principals, p) }
	f.HelloFunc = func(context.Context) (core.AgentHello, error) { return core.AgentHello{HostID: "h1"}, nil }
	f.ReadFunc = func(_ context.Context, p core.Principal, _ core.ActionRef, _ json.RawMessage) (json.RawMessage, error) {
		record(p)
		return json.RawMessage(`{}`), nil
	}
	f.ResourceFunc = func(_ context.Context, p core.Principal, _ string) (json.RawMessage, error) {
		record(p)
		return json.RawMessage(`{}`), nil
	}
	f.PlanFunc = func(_ context.Context, p core.Principal, _ core.ActionRef, _ json.RawMessage) (core.PlanView, error) {
		record(p)
		return testPlan, nil
	}
	f.ApplyFunc = func(_ context.Context, p core.Principal, _ string, _ core.ApplyOptions) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.ConfirmFunc = func(_ context.Context, p core.Principal, _ string) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.RevertFunc = func(_ context.Context, p core.Principal, _ string) (core.ChangeView, error) {
		record(p)
		return testChange, nil
	}
	f.ChangeFunc = func(context.Context, string) (core.ChangeView, error) { return testChange, nil }
	f.ChangesFunc = func(context.Context, core.ChangeFilter) ([]core.ChangeView, error) { return nil, nil }
	f.PreflightFunc = func(_ context.Context, id string) (core.ModuleStatus, error) {
		f.preflights = append(f.preflights, id)
		if id == "ufw" {
			return core.ModuleStatus{ID: id, Version: 1, Reason: "ufw is not installed"}, nil
		}
		return core.ModuleStatus{ID: id, Version: 1, Available: true}, nil
	}
	return f
}

// socketHandler serves fake on the socket; enrolled switches enrollment.
func socketHandler(fake *socketFake, enrolled *atomic.Bool) http.Handler {
	return NewSocketHandler(Config{Agent: fake, Log: discardLog()}, SocketConfig{
		Identity: func() (string, bool) { return "h1", enrolled.Load() },
	})
}

func TestSocketPrincipalIsLocalRoot(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	h := socketHandler(fake, &enrolled)
	// A peer of the socket cannot claim to be anyone else.
	spoof := http.Header{wire.HeaderPrincipal: {principalHeader(t, testPrincipal)}}

	if rec := call(t, h, "POST", "/agent/v1/actions/hostinfo.summary/v1/read", spoof, []byte(`{}`)); rec.Code != 200 {
		t.Fatalf("read answered %d %s", rec.Code, rec.Body)
	}
	enrolled.Store(true)
	for _, path := range []string{"/agent/v1/actions/hostinfo.summary/v1/read", "/agent/v1/actions/hostinfo.summary/v1/plan"} {
		if rec := call(t, h, "POST", path, spoof, []byte(`{}`)); rec.Code != 200 {
			t.Fatalf("%s answered %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, path := range []string{"/agent/v1/plans/p1/apply", "/agent/v1/changes/c1/confirm", "/agent/v1/changes/c1/revert"} {
		var body []byte
		if path == "/agent/v1/plans/p1/apply" {
			body = []byte(`{}`)
		}
		if rec := call(t, h, "POST", path, spoof, body); rec.Code != 200 {
			t.Fatalf("%s answered %d %s", path, rec.Code, rec.Body)
		}
	}
	unenrolled := core.Principal{Actor: core.ActorUnenrolled, Scope: core.ScopeAdmin, Source: SocketSource}
	root := core.Principal{Actor: "local:root@h1", Scope: core.ScopeAdmin, Source: SocketSource}
	want := []core.Principal{unenrolled, root, root, root, root, root}
	if !reflect.DeepEqual(fake.principals, want) {
		t.Errorf("principals\n got %+v\nwant %+v", fake.principals, want)
	}
}

func TestSocketBeforeEnrollment(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	h := socketHandler(fake, &enrolled)
	for _, path := range []string{"/agent/v1/hello", "/agent/v1/changes/c1", "/agent/v1/changes", "/agent/v1/modules/hostinfo/preflight", "/agent/v1/resources/hostinfo/status"} {
		if rec := call(t, h, "GET", path, nil, nil); rec.Code != 200 {
			t.Errorf("GET %s before enrollment answered %d %s", path, rec.Code, rec.Body)
		}
	}
	for _, tt := range []struct {
		path string
		body []byte
	}{
		{"/agent/v1/actions/hostinfo.summary/v1/plan", []byte(`{}`)},
		{"/agent/v1/plans/p1/apply", []byte(`{}`)},
		{"/agent/v1/changes/c1/confirm", nil},
		{"/agent/v1/changes/c1/revert", nil},
	} {
		wantProblem(t, call(t, h, "POST", tt.path, nil, tt.body), 409, errcode.AgentNotEnrolled)
	}
	if len(fake.principals) != 1 {
		t.Errorf("the agent acted %d times before enrollment, want once (the resource read)", len(fake.principals))
	}
}

func TestSocketPreflight(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	enrolled.Store(true)
	h := socketHandler(fake, &enrolled)
	wantProblem(t, call(t, h, "POST", "/agent/v1/actions/ufw.status/v1/read", nil, []byte(`{}`)), 409, errcode.ModuleUnavailable)
	p := wantProblem(t, call(t, h, "POST", "/agent/v1/actions/ufw.allow/v1/plan", nil, []byte(`{}`)), 409, errcode.ModuleUnavailable)
	if p.Detail != "module ufw is unavailable: ufw is not installed" {
		t.Errorf("detail = %q", p.Detail)
	}
	wantProblem(t, call(t, h, "GET", "/agent/v1/resources/ufw/rules", nil, nil), 409, errcode.ModuleUnavailable)
	// The core's actions, such as an update, need no module.
	if rec := call(t, h, "POST", "/agent/v1/actions/core.update.status/v1/read", nil, []byte(`{}`)); rec.Code != 200 {
		t.Errorf("core read answered %d %s", rec.Code, rec.Body)
	}
	// Applying a plan is not checked again: its plan was.
	if rec := call(t, h, "POST", "/agent/v1/plans/p1/apply", nil, []byte(`{}`)); rec.Code != 200 {
		t.Errorf("apply answered %d %s", rec.Code, rec.Body)
	}
	if want := []string{"ufw", "ufw", "ufw"}; !reflect.DeepEqual(fake.preflights, want) {
		t.Errorf("preflights %v, want %v", fake.preflights, want)
	}
}

func TestSocketServesNoJournal(t *testing.T) {
	fake := newSocketFake()
	var enrolled atomic.Bool
	enrolled.Store(true)
	h := socketHandler(fake, &enrolled)
	wantProblem(t, call(t, h, "GET", "/agent/v1/journal", nil, nil), 404, errcode.NotFound)
	wantProblem(t, call(t, h, "POST", "/agent/v1/journal/ack", nil, []byte(`{"seq":1}`)), 404, errcode.NotFound)
	// Hello needs no controller headers on the socket.
	if rec := call(t, h, "GET", "/agent/v1/hello", nil, nil); rec.Code != 200 {
		t.Errorf("hello answered %d %s", rec.Code, rec.Body)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/6 && TMPDIR=/root/.cache/plan3-tmp/6 GOTOOLCHAIN=local go test -count=1 ./internal/agent/api/`
Expected: FAIL, the build stops with `undefined: NewSocketHandler` (and the other names of `socket.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/agent/api/socket.go`:

```go
package api

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// SocketSource is the Principal.Source of every agent socket request.
const SocketSource = "socket"

// SocketConfig is what the agent socket's rules need.
type SocketConfig struct {
	// Identity reports this host's id and whether the agent holds
	// credentials; it is asked on every request, so an enrollment while the
	// socket serves takes effect at once.
	Identity func() (hostID string, enrolled bool)
}

// NewSocketHandler serves the agent API on the agent socket, whose peer the
// socket listener has already checked is root. The caller is always the
// local root user, whatever header the peer sends; before enrollment only
// hello and reads are served. Enablement is the controller's rule and is not
// applied, but a module whose preflight checks fail is refused. The journal
// routes are absent: the controller alone collects and acknowledges the
// journal.
func NewSocketHandler(cfg Config, sock SocketConfig) http.Handler {
	h := &handler{cfg: cfg}
	h.principal = func(*http.Request) (core.Principal, error) { return socketPrincipal(sock.Identity()), nil }
	h.allow = func(ctx context.Context, op, name string) error { return h.socketAllow(ctx, sock, op, name) }
	h.hello = func(*http.Request) error { return nil }
	return h.routes(false)
}

// socketPrincipal is the local root user, named after the host once the
// agent is enrolled.
func socketPrincipal(hostID string, enrolled bool) core.Principal {
	actor := core.ActorUnenrolled
	if enrolled {
		actor = "local:root@" + hostID
	}
	return core.Principal{Actor: actor, Scope: core.ScopeAdmin, Source: SocketSource}
}

func (h *handler) socketAllow(ctx context.Context, sock SocketConfig, op, name string) error {
	switch op {
	case opPlan, opApply, opConfirm, opRevert:
		if _, enrolled := sock.Identity(); !enrolled {
			return errcode.New(errcode.AgentNotEnrolled,
				"this agent is not enrolled; until it enrolls, its socket serves only hello and reads")
		}
	}
	switch op {
	case opRead, opResource, opPlan:
		return h.preflight(ctx, name)
	}
	return nil
}

// preflight runs the checks of the module name belongs to, which the
// controller's dispatcher would otherwise have enforced. The core has no
// preflight checks.
func (h *handler) preflight(ctx context.Context, name string) error {
	module, _, _ := strings.Cut(name, ".")
	if module == core.ModuleIDCore {
		return nil
	}
	st, err := h.cfg.Agent.Preflight(ctx, module)
	if err != nil {
		return err
	}
	if !st.Available {
		return errcode.New(errcode.ModuleUnavailable, fmt.Sprintf("module %s is unavailable: %s", module, st.Reason))
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/6 GOTOOLCHAIN=local
go test -count=1 -race -timeout 120s ./internal/agent/api/
go vet ./internal/agent/api/
golangci-lint run --allow-serial-runners ./internal/agent/api/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/agent/api`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/agent/api/socket.go internal/agent/api/socket_test.go
git commit -m "feat(agent): add agent socket rules"
```

### Task 7: install.sh hardening

**Files:**
- Modify: `install.sh.in:96` (`verify_release`'s `mktemp`), `install.sh.in:160-190` (`check_token_file`, which becomes `read_token_file`, and `read_token`), `install.sh.in:235-236` (the token file check in `main`), `install.sh.in:252` (the temporary directory and its traps), `install.sh.in:283-284` (the agent role's token)
- Create: `install.sh` (generated, mode 0755)
- Test: `scripts/gen-install-sh/main_test.go`

**Interfaces:**
- Consumes: `Generate`, `keyWord` and the existing test helpers of `scripts/gen-install-sh` (`readTemplate`, `signer`, `newSigner`, `script`, `needTools`, `verify`, `tarGz`, `readFile`, `writeFile`, `appendFile`, `rename`), `trustedkeys.Keys`, `trustedkeys.BaseURL`, `trustedkeys.TestBuild`, `release/sign.sh`, `make install-sh`.
- Produces: C7. Top-level names added (tests): `signer.sigPath`, `patchSignature`, `TestVerifyReleaseWithoutATemporaryDirectory`, `fixture`, `fixtureVersion`, `fixtureStub`, `newFixture`, `fixture.command`, `fixture.run`, `fixture.bin`, `fixture.tokenFile`, `TestInstallControllerPassesListenArgs`, `TestInstallRefusesAChecksumMismatch`, `TestInstallRefusesABadTokenFile`, `TestInstallWithoutATerminal`, `TestInstallWithoutATemporaryDirectory`, `TestInstallCleansUpOnInterrupt`, `TestCommittedScriptIsCurrent`, `TestSignedReleaseVerifies`; `TestInstallAgentEndToEnd` is rewritten over `fixture` and `TestVerifyRelease` gains two rows.

**Items:** 28, 37

**Depends on:** none

**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing tests**

Run `mkdir -p /root/.cache/plan3-tmp/7`, then save this patch as `/root/.cache/plan3-tmp/7/tests.patch` and apply it from the repository root with `git apply /root/.cache/plan3-tmp/7/tests.patch`. It adds the legacy `Ed` and patched key id rows to `TestVerifyRelease`, moves the end-to-end setup into `fixture`, and adds the controller-role, checksum, token file, terminal, temporary directory, interrupt, committed-script and `sign.sh` round-trip tests. The wrong-owner token row runs only as root, because only root can give a file to another user.

```diff
diff --git a/scripts/gen-install-sh/main_test.go b/scripts/gen-install-sh/main_test.go
index b05fbc8..e2f709f 100644
--- a/scripts/gen-install-sh/main_test.go
+++ b/scripts/gen-install-sh/main_test.go
@@ -6,10 +6,13 @@ import (
 	"compress/gzip"
 	"crypto/rand"
 	"crypto/sha256"
+	"encoding/base64"
+	"encoding/binary"
 	"encoding/hex"
 	"errors"
 	"fmt"
 	"io"
+	"io/fs"
 	"net/http"
 	"net/http/httptest"
 	"os"
@@ -20,7 +23,9 @@ import (
 	"slices"
 	"strings"
 	"sync/atomic"
+	"syscall"
 	"testing"
+	"time"
 
 	"aead.dev/minisign"
 
@@ -63,7 +68,26 @@ func (s signer) sign(t *testing.T, dir, comment string) {
 		t.Fatal(err)
 	}
 	sig := r.SignWithComments(s.priv, comment, "signature from darkpanel test key")
-	writeFile(t, filepath.Join(dir, "checksums.txt."+s.key.ID+".minisig"), string(sig))
+	writeFile(t, s.sigPath(dir), string(sig))
+}
+
+func (s signer) sigPath(dir string) string {
+	return filepath.Join(dir, "checksums.txt."+s.key.ID+".minisig")
+}
+
+// patchSignature rewrites the decoded signature line of the signature file p.
+// The bytes after the algorithm and key id still verify, so a refusal proves
+// the script checks the patched field itself.
+func patchSignature(t *testing.T, p string, patch func(sig []byte)) {
+	t.Helper()
+	lines := strings.Split(readFile(t, p), "\n")
+	sig, err := base64.StdEncoding.DecodeString(lines[1])
+	if err != nil {
+		t.Fatal(err)
+	}
+	patch(sig)
+	lines[1] = base64.StdEncoding.EncodeToString(sig)
+	writeFile(t, p, strings.Join(lines, "\n"))
 }
 
 // script writes install.sh generated with keys and returns its path.
@@ -285,11 +309,21 @@ func TestVerifyRelease(t *testing.T) {
 		}, oneKey, false},
 		{"bad global signature", func(t *testing.T, dir string, a, _ signer) {
 			a.sign(t, dir, comment)
-			p := filepath.Join(dir, "checksums.txt."+a.key.ID+".minisig")
+			p := a.sigPath(dir)
 			lines := strings.Split(readFile(t, p), "\n")
 			lines[3] = strings.Repeat("A", 86) + "=="
 			writeFile(t, p, strings.Join(lines, "\n"))
 		}, oneKey, false},
+		{"legacy Ed signature", func(t *testing.T, dir string, a, _ signer) {
+			a.sign(t, dir, comment)
+			patchSignature(t, a.sigPath(dir), func(sig []byte) { sig[1] = 'd' })
+		}, oneKey, false},
+		{"signature key id patched", func(t *testing.T, dir string, a, _ signer) {
+			a.sign(t, dir, comment)
+			patchSignature(t, a.sigPath(dir), func(sig []byte) {
+				binary.LittleEndian.PutUint64(sig[2:10], ^binary.LittleEndian.Uint64(sig[2:10]))
+			})
+		}, oneKey, false},
 		{"no signature files", func(*testing.T, string, signer, signer) {}, oneKey, false},
 		{"second of two keys signed", func(t *testing.T, dir string, _, b signer) { b.sign(t, dir, comment) }, twoKeys, true},
 		{"both keys signed", func(t *testing.T, dir string, a, b signer) {
@@ -311,13 +345,51 @@ func TestVerifyRelease(t *testing.T) {
 	}
 }
 
+func TestVerifyReleaseWithoutATemporaryDirectory(t *testing.T) {
+	needTools(t, "sh", "openssl")
+	s := newSigner(t)
+	dir := t.TempDir()
+	writeFile(t, filepath.Join(dir, "checksums.txt"), "abc  darkpanel_1.4.0_linux_amd64.tar.gz\n")
+	s.sign(t, dir, "version=1.4.0 channel=stable purpose=checksums")
+	cmd := exec.Command("sh", "-c", `. "$1"; verify_release "$2" "$3"`, "sh", script(t, []trustedkeys.Key{s.key}, "https://example.com"), dir, "1.4.0") //nolint:gosec // G204: a fixed command over test files
+	cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_SH_LIB=1", "TMPDIR="+filepath.Join(t.TempDir(), "missing"))
+	out, err := cmd.CombinedOutput()
+	if err == nil || !strings.Contains(string(out), "cannot create a temporary directory") {
+		t.Errorf("err %v, output %q; want the temporary directory refusal", err, out)
+	}
+}
+
 func oneKey(a, _ signer) []trustedkeys.Key  { return []trustedkeys.Key{a.key} }
 func twoKeys(a, b signer) []trustedkeys.Key { return []trustedkeys.Key{a.key, b.key} }
 func noKeys(signer, signer) []trustedkeys.Key {
 	return nil
 }
 
-func TestInstallAgentEndToEnd(t *testing.T) {
+// fixture is a signed release of version fixtureVersion served over HTTP,
+// an install root holding a supported os-release, and a stub darkpanel that
+// logs its arguments and standard input under logBase.
+type fixture struct {
+	script  string
+	root    string
+	release string
+	tarball string
+	logBase string
+	srv     *httptest.Server
+	// tarballRequests counts requests for any tarball.
+	tarballRequests atomic.Int32
+	// onTarball, when set, runs before a tarball is served. It is set while
+	// the server runs, and the race detector cannot see the ordering that
+	// install.sh's request gives it.
+	onTarball atomic.Pointer[func(*http.Request)]
+}
+
+const (
+	fixtureVersion = "1.10.0"
+	fixtureStub    = "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$DARKPANEL_STUB_LOG.args\"\ncat >\"$DARKPANEL_STUB_LOG.stdin\"\n"
+)
+
+func newFixture(t *testing.T) *fixture {
+	t.Helper()
 	needTools(t, "sh", "openssl", "base64", "od", "sha256sum", "tar", "stat")
 	if _, err := exec.LookPath("curl"); err != nil {
 		needTools(t, "wget")
@@ -325,18 +397,19 @@ func TestInstallAgentEndToEnd(t *testing.T) {
 	if runtime.GOARCH != "amd64" && runtime.GOARCH != "arm64" {
 		t.Skipf("install.sh supports amd64 and arm64, not %s", runtime.GOARCH)
 	}
-	const version = "1.10.0"
+	f := &fixture{
+		release: t.TempDir(),
+		root:    t.TempDir(),
+		tarball: "darkpanel_" + fixtureVersion + "_linux_" + runtime.GOARCH + ".tar.gz",
+	}
 	s := newSigner(t)
-	stub := "#!/bin/sh\nprintf '%s\\n' \"$@\" >\"$DARKPANEL_STUB_LOG.args\"\ncat >\"$DARKPANEL_STUB_LOG.stdin\"\n"
-	tarball := "darkpanel_" + version + "_linux_" + runtime.GOARCH + ".tar.gz"
-	release := t.TempDir()
-	writeFile(t, filepath.Join(release, tarball), string(tarGz(t, map[string]string{"darkpanel": stub, "LICENSE": "license\n", "README.md": "readme\n"})))
-	sum := sha256.Sum256([]byte(readFile(t, filepath.Join(release, tarball))))
+	writeFile(t, filepath.Join(f.release, f.tarball), string(tarGz(t, map[string]string{"darkpanel": fixtureStub, "LICENSE": "license\n", "README.md": "readme\n"})))
+	sum := sha256.Sum256([]byte(readFile(t, filepath.Join(f.release, f.tarball))))
 	// The other architecture's tarball and the SBOM are listed but never fetched.
-	writeFile(t, filepath.Join(release, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+tarball+"\n"+
-		strings.Repeat("0", 64)+"  darkpanel_"+version+"_linux_other.tar.gz\n"+
-		strings.Repeat("1", 64)+"  darkpanel_"+version+".cdx.json\n")
-	s.sign(t, release, "version="+version+" channel=stable purpose=checksums")
+	writeFile(t, filepath.Join(f.release, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+f.tarball+"\n"+
+		strings.Repeat("0", 64)+"  darkpanel_"+fixtureVersion+"_linux_other.tar.gz\n"+
+		strings.Repeat("1", 64)+"  darkpanel_"+fixtureVersion+".cdx.json\n")
+	s.sign(t, f.release, "version="+fixtureVersion+" channel=stable purpose=checksums")
 	index := `{"formatVersion": 1, "releases": [
   {"version": "1.11.0-beta.1", "channel": "beta"},
   {"version": "1.2.0", "channel": "stable"},
@@ -344,81 +417,279 @@ func TestInstallAgentEndToEnd(t *testing.T) {
 ]}`
 	mux := http.NewServeMux()
 	mux.HandleFunc("GET /index/releases.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(index)) })
-	mux.Handle("GET /v"+version+"/", http.StripPrefix("/v"+version+"/", http.FileServer(http.Dir(release))))
-	var tarballRequests atomic.Int32
-	counted := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
+	mux.Handle("GET /v"+fixtureVersion+"/", http.StripPrefix("/v"+fixtureVersion+"/", http.FileServer(http.Dir(f.release))))
+	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
 		if strings.HasSuffix(r.URL.Path, ".tar.gz") {
-			tarballRequests.Add(1)
+			f.tarballRequests.Add(1)
+			if on := f.onTarball.Load(); on != nil {
+				(*on)(r)
+			}
 		}
 		mux.ServeHTTP(w, r)
-	})
-	srv := httptest.NewServer(counted)
-	defer srv.Close()
-
-	root := t.TempDir()
-	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
-	tokenFile := filepath.Join(root, "token")
-	writeFile(t, tokenFile, "dpe1.example-token\n")
-	logBase := filepath.Join(root, "stub")
-	run := func(args ...string) ([]byte, error) {
-		cmd := exec.Command("sh", append([]string{script(t, []trustedkeys.Key{s.key}, srv.URL)}, args...)...) //nolint:gosec // G204: the generated test script
-		cmd.Env = append(os.Environ(), "DARKPANEL_INSTALL_ROOT="+root, "DARKPANEL_STUB_LOG="+logBase)
-		return cmd.CombinedOutput()
-	}
-	if out, err := run("--role", "agent", "--token-file", tokenFile, "--base-url", srv.URL); err != nil {
+	}))
+	t.Cleanup(f.srv.Close)
+	f.script = script(t, []trustedkeys.Key{s.key}, f.srv.URL)
+	writeFile(t, filepath.Join(f.root, "etc", "os-release"), "ID=debian\nVERSION_ID=\"12\"\nPRETTY_NAME=\"Debian GNU/Linux 12 (bookworm)\"\n")
+	f.logBase = filepath.Join(f.root, "stub")
+	return f
+}
+
+// command is install.sh with args against the fixture; env adds variables.
+func (f *fixture) command(env []string, args ...string) *exec.Cmd {
+	cmd := exec.Command("sh", append([]string{f.script}, args...)...) //nolint:gosec // G204: the generated test script
+	cmd.Env = append(append(os.Environ(), "DARKPANEL_INSTALL_ROOT="+f.root, "DARKPANEL_STUB_LOG="+f.logBase), env...)
+	return cmd
+}
+
+func (f *fixture) run(args ...string) ([]byte, error) {
+	return f.command(nil, args...).CombinedOutput()
+}
+
+func (f *fixture) bin() string {
+	return filepath.Join(f.root, "usr", "local", "lib", "darkpanel", "darkpanel")
+}
+
+// tokenFile writes content to a token file of mode 0600 and returns its path.
+func (f *fixture) tokenFile(t *testing.T, content string) string {
+	t.Helper()
+	p := filepath.Join(t.TempDir(), "token")
+	writeFile(t, p, content)
+	return p
+}
+
+func TestInstallAgentEndToEnd(t *testing.T) {
+	f := newFixture(t)
+	tokenFile := f.tokenFile(t, "dpe1.example-token\n")
+	if out, err := f.run("--role", "agent", "--token-file", tokenFile, "--base-url", f.srv.URL); err != nil {
 		t.Fatalf("install.sh: %v\n%s", err, out)
 	}
-	bin := filepath.Join(root, "usr", "local", "lib", "darkpanel", "darkpanel")
-	if got := readFile(t, bin); got != stub {
+	if got := readFile(t, f.bin()); got != fixtureStub {
 		t.Errorf("installed binary = %q, want the stub", got)
 	}
-	if info, err := os.Stat(bin); err != nil || info.Mode().Perm() != 0o755 {
+	if info, err := os.Stat(f.bin()); err != nil || info.Mode().Perm() != 0o755 {
 		t.Errorf("installed binary mode = %v, %v; want 0755", info, err)
 	}
-	if target, err := os.Readlink(filepath.Join(root, "usr", "local", "bin", "darkpanel")); err != nil || target != bin {
-		t.Errorf("link = %q, %v; want %s", target, err, bin)
+	if target, err := os.Readlink(filepath.Join(f.root, "usr", "local", "bin", "darkpanel")); err != nil || target != f.bin() {
+		t.Errorf("link = %q, %v; want %s", target, err, f.bin())
 	}
-	if got := readFile(t, logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
+	if got := readFile(t, f.logBase+".args"); got != "system\ninstall\n--role\nagent\n" {
 		t.Errorf("darkpanel ran with %q, want system install --role agent", got)
 	}
-	if got := readFile(t, logBase+".stdin"); got != "dpe1.example-token\n" {
+	if got := readFile(t, f.logBase+".stdin"); got != "dpe1.example-token\n" {
 		t.Errorf("darkpanel read %q on standard input, want the token", got)
 	}
 
 	// A version with a second line is refused before any tarball is fetched.
-	fetched := tarballRequests.Load()
-	if out, err := run("--role", "controller", "--version", "1.0.0\nx", "--base-url", srv.URL); err == nil || !strings.Contains(string(out), "is not a semantic version") {
+	fetched := f.tarballRequests.Load()
+	if out, err := f.run("--role", "controller", "--version", "1.0.0\nx", "--base-url", f.srv.URL); err == nil || !strings.Contains(string(out), "is not a semantic version") {
 		t.Errorf("a multi-line version: err %v, output %s", err, out)
 	}
-	if got := tarballRequests.Load(); got != fetched {
+	if got := f.tarballRequests.Load(); got != fetched {
 		t.Errorf("a multi-line version fetched %d tarballs, want none", got-fetched)
 	}
 
 	// With saved credentials and no token file, darkpanel gets an empty
 	// standard input and the operator is told how to recover a revoked agent.
-	writeFile(t, filepath.Join(root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
-	out, err := run("--role", "agent")
+	writeFile(t, filepath.Join(f.root, "etc", "darkpanel", "agent", "current", "agent.crt"), "cert\n")
+	out, err := f.run("--role", "agent")
 	if err != nil {
 		t.Fatalf("install.sh with saved credentials: %v\n%s", err, out)
 	}
 	if !strings.Contains(string(out), "rerun install.sh with --token-file") {
 		t.Errorf("output %q does not say how to recover a revoked agent", out)
 	}
-	if got := readFile(t, logBase+".stdin"); got != "" {
+	if got := readFile(t, f.logBase+".stdin"); got != "" {
 		t.Errorf("darkpanel read %q on standard input, want nothing", got)
 	}
 
-	if err := os.Chmod(tokenFile, 0o644); err != nil { //nolint:gosec // G302: the test needs a token file others can read
+	writeFile(t, filepath.Join(f.root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"20.04\"\nPRETTY_NAME=\"Ubuntu 20.04.6 LTS\"\n")
+	if out, err := f.run("--role", "controller"); err == nil || !strings.Contains(string(out), "this host is Ubuntu 20.04.6 LTS") {
+		t.Errorf("an unsupported OS: err %v, output %s", err, out)
+	}
+}
+
+func TestInstallControllerPassesListenArgs(t *testing.T) {
+	f := newFixture(t)
+	if out, err := f.run("--role", "controller", "--agent-listen", "0.0.0.0:7443", "--agent-listen", "[::1]:7443", "--agent-advertise", "panel.example:7443"); err != nil {
+		t.Fatalf("install.sh: %v\n%s", err, out)
+	}
+	want := "system\ninstall\n--role\ncontroller\n--agent-listen\n0.0.0.0:7443\n--agent-listen\n[::1]:7443\n--agent-advertise\npanel.example:7443\n"
+	if got := readFile(t, f.logBase+".args"); got != want {
+		t.Errorf("darkpanel ran with %q, want %q", got, want)
+	}
+	if got := readFile(t, f.logBase+".stdin"); got != "" {
+		t.Errorf("darkpanel read %q on standard input, want nothing", got)
+	}
+}
+
+func TestInstallRefusesAChecksumMismatch(t *testing.T) {
+	f := newFixture(t)
+	writeFile(t, filepath.Join(f.release, f.tarball), string(tarGz(t, map[string]string{"darkpanel": "#!/bin/sh\necho evil\n"})))
+	out, err := f.run("--role", "controller")
+	if err == nil || !strings.Contains(string(out), f.tarball+" does not match checksums.txt") {
+		t.Errorf("a tampered tarball: err %v, output %s", err, out)
+	}
+	if _, err := os.Stat(f.bin()); !errors.Is(err, fs.ErrNotExist) {
+		t.Errorf("a tampered tarball was installed: stat %v", err)
+	}
+}
+
+func TestInstallRefusesABadTokenFile(t *testing.T) {
+	f := newFixture(t)
+	missing := filepath.Join(t.TempDir(), "missing")
+	worldReadable := f.tokenFile(t, "dpe1.x\n")
+	if err := os.Chmod(worldReadable, 0o644); err != nil { //nolint:gosec // G302: the test needs a token file others can read
 		t.Fatal(err)
 	}
-	// A bad token file is refused before anything is downloaded.
-	srv.Close()
-	if out, err := run("--role", "agent", "--token-file", tokenFile); err == nil || !strings.Contains(string(out), "mode 0600 or 0400, not 0644") {
-		t.Errorf("a world-readable token file: err %v, output %s", err, out)
+	tests := []struct {
+		name, path, want string
+	}{
+		{"missing", missing, "token file " + missing + " does not exist or cannot be read"},
+		{"a directory", t.TempDir(), "does not exist or cannot be read"},
+		{"empty", f.tokenFile(t, ""), "is empty"},
+		{"world readable", worldReadable, "mode 0600 or 0400, not 0644"},
+	}
+	if os.Geteuid() == 0 {
+		otherOwner := f.tokenFile(t, "dpe1.x\n")
+		if err := os.Chown(otherOwner, 65534, 65534); err != nil {
+			t.Fatal(err)
+		}
+		tests = append(tests, struct{ name, path, want string }{"owned by another user", otherOwner, "must be owned by uid 0, the caller, not uid 65534"})
 	}
-	writeFile(t, filepath.Join(root, "etc", "os-release"), "ID=ubuntu\nVERSION_ID=\"20.04\"\nPRETTY_NAME=\"Ubuntu 20.04.6 LTS\"\n")
-	if out, err := run("--role", "controller"); err == nil || !strings.Contains(string(out), "this host is Ubuntu 20.04.6 LTS") {
-		t.Errorf("an unsupported OS: err %v, output %s", err, out)
+	for _, tt := range tests {
+		t.Run(tt.name, func(t *testing.T) {
+			out, err := f.run("--role", "agent", "--token-file", tt.path)
+			if err == nil || !strings.Contains(string(out), tt.want) {
+				t.Errorf("err %v, output %q; want a refusal containing %q", err, out, tt.want)
+			}
+		})
+	}
+	// Every refusal comes before anything is downloaded.
+	if got := f.tarballRequests.Load(); got != 0 {
+		t.Errorf("bad token files fetched %d tarballs, want none", got)
+	}
+}
+
+func TestInstallWithoutATerminal(t *testing.T) {
+	f := newFixture(t)
+	cmd := f.command(nil, "--role", "agent")
+	// A new session has no controlling terminal, whatever runs the test.
+	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
+	out, err := cmd.CombinedOutput()
+	if err == nil || !strings.Contains(string(out), "no terminal to read the enrollment token from; pass --token-file") {
+		t.Errorf("err %v, output %q; want the no-terminal refusal", err, out)
+	}
+	if strings.Contains(string(out), "/dev/tty") {
+		t.Errorf("output %q holds a raw shell error", out)
+	}
+}
+
+func TestInstallWithoutATemporaryDirectory(t *testing.T) {
+	f := newFixture(t)
+	out, err := f.command([]string{"TMPDIR=" + filepath.Join(t.TempDir(), "missing")}, "--role", "controller").CombinedOutput()
+	if err == nil || !strings.Contains(string(out), "cannot create a temporary directory") {
+		t.Errorf("err %v, output %q; want the temporary directory refusal", err, out)
+	}
+}
+
+func TestInstallCleansUpOnInterrupt(t *testing.T) {
+	f := newFixture(t)
+	tmp := t.TempDir()
+	requested := make(chan struct{})
+	hold := func(r *http.Request) {
+		close(requested)
+		select {
+		case <-r.Context().Done():
+		case <-time.After(30 * time.Second):
+		}
+	}
+	f.onTarball.Store(&hold)
+	cmd := f.command([]string{"TMPDIR=" + tmp}, "--role", "controller")
+	// The interrupt goes to the whole group, as a terminal's Ctrl-C does.
+	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
+	if err := cmd.Start(); err != nil {
+		t.Fatal(err)
+	}
+	select {
+	case <-requested:
+	case <-time.After(30 * time.Second):
+		_ = cmd.Process.Kill()
+		t.Fatal("install.sh never requested the tarball")
+	}
+	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGINT); err != nil {
+		t.Fatal(err)
+	}
+	err := cmd.Wait()
+	var exit *exec.ExitError
+	if !errors.As(err, &exit) || exit.ExitCode() != 130 {
+		t.Errorf("install.sh after SIGINT: %v, want exit status 130", err)
+	}
+	entries, err := os.ReadDir(tmp)
+	if err != nil {
+		t.Fatal(err)
+	}
+	if len(entries) != 0 {
+		t.Errorf("install.sh left %v in its temporary directory", entries)
+	}
+}
+
+func TestCommittedScriptIsCurrent(t *testing.T) {
+	if trustedkeys.TestBuild() {
+		t.Skip("the committed install.sh embeds the production keys")
+	}
+	want, err := Generate(readTemplate(t), trustedkeys.Keys(), trustedkeys.BaseURL())
+	if err != nil {
+		t.Fatal(err)
+	}
+	const committed = "../../install.sh"
+	if got := readFile(t, committed); got != string(want) {
+		t.Error("install.sh differs from install.sh.in rendered with the trusted keys; run make install-sh")
+	}
+	if info, err := os.Stat(committed); err != nil || info.Mode().Perm() != 0o755 {
+		t.Errorf("install.sh mode = %v, %v; want 0755", info, err)
+	}
+}
+
+// TestSignedReleaseVerifies signs with release/sign.sh, the real minisign
+// command and the harness key, so the release workflow and install.sh are
+// proven to agree on the signature format rather than each against its own
+// fixture.
+func TestSignedReleaseVerifies(t *testing.T) {
+	needTools(t, "bash", "go", "sh", "openssl", "base64", "od")
+	work := t.TempDir()
+	minisignBin := filepath.Join(work, "minisign")
+	if out, err := exec.Command("go", "build", "-o", minisignBin, "aead.dev/minisign/cmd/minisign").CombinedOutput(); err != nil { //nolint:gosec // G204: a fixed build of a required module
+		t.Fatalf("build minisign: %v\n%s", err, out)
+	}
+	keyText := readFile(t, "../../internal/update/trustedkeys/testdata/harness.key")
+	var priv minisign.PrivateKey
+	if err := priv.UnmarshalText([]byte(keyText)); err != nil {
+		t.Fatalf("harness.key: %v", err)
+	}
+	pub, ok := priv.Public().(minisign.PublicKey)
+	if !ok {
+		t.Fatalf("public key of type %T", priv.Public())
+	}
+	key := trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}
+	const version = "1.4.0"
+	rel := filepath.Join(work, "rel")
+	writeFile(t, filepath.Join(rel, "checksums.txt"), "abc  darkpanel_"+version+"_linux_amd64.tar.gz\n")
+	info := filepath.Join(work, "version.json")
+	writeFile(t, info, `{"version":"`+version+`","testBuild":false,"trustedKeys":["`+key.ID+`"]}`+"\n")
+
+	cmd := exec.Command("bash", "../../release/sign.sh", version, "stable", rel, info) //nolint:gosec // G204: the repository's signing script over test files
+	for _, kv := range os.Environ() {
+		if !strings.HasPrefix(kv, "MINISIGN") {
+			cmd.Env = append(cmd.Env, kv)
+		}
+	}
+	// The harness key is not encrypted, so minisign reads no password.
+	cmd.Env = append(cmd.Env, "MINISIGN="+minisignBin, "MINISIGN_KEY_1="+keyText)
+	if out, err := cmd.CombinedOutput(); err != nil {
+		t.Fatalf("sign.sh: %v\n%s", err, out)
+	}
+	if !verify(t, script(t, []trustedkeys.Key{key}, "https://example.com"), rel, version) {
+		t.Error("verify_release refused a release signed by sign.sh")
 	}
 }
 
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `TMPDIR=/root/.cache/plan3-tmp/7 GOTOOLCHAIN=local go test -count=1 ./scripts/gen-install-sh/`
Expected: FAIL with `TestVerifyReleaseWithoutATemporaryDirectory` and `TestInstallWithoutATemporaryDirectory` (a raw `mktemp: failed to create directory` error), `TestInstallRefusesABadTokenFile` (`missing` and `a directory` say only `does not exist`, `empty` installs, a tarball is fetched), `TestInstallWithoutATerminal` (`cannot create /dev/tty`), `TestInstallCleansUpOnInterrupt` (`signal: interrupt`, a `tmp.*` directory left) and `TestCommittedScriptIsCurrent` (`open ../../install.sh: no such file or directory`). Every other test passes, the two new `TestVerifyRelease` rows included.

- [ ] **Step 3: Harden the template**

Save this patch as `/root/.cache/plan3-tmp/7/template.patch` and apply it with `git apply /root/.cache/plan3-tmp/7/template.patch`:

```diff
diff --git a/install.sh.in b/install.sh.in
index 867c183..895d3e7 100644
--- a/install.sh.in
+++ b/install.sh.in
@@ -93,7 +93,7 @@ hex() {
 verify_release() {
 	vdir=$1
 	vversion=$2
-	vtmp=$(mktemp -d)
+	vtmp=$(mktemp -d) || die "cannot create a temporary directory"
 	verified=1
 	openssl dgst -blake2b512 -binary "$vdir/checksums.txt" >"$vtmp/digest"
 	for key in $KEYS; do
@@ -157,11 +157,19 @@ install_binary() {
 	ln -sfn "$bindir/darkpanel" "$ROOT/usr/local/bin/darkpanel"
 }
 
-# check_token_file refuses a token file other users could read or replace.
-check_token_file() {
-	[ -f "$1" ] || die "token file $1 does not exist"
-	owner=$(stat -c %u "$1")
-	mode=$(stat -c %a "$1")
+# read_token_file reads the enrollment token from $1, refusing a file other
+# users could read or replace. The owner and mode checks and the read go
+# through one open descriptor, so a file swapped in after the checks is never
+# read. A failed redirection of exec ends the shell with a raw error that no
+# || catches, so the path is tested before it is opened.
+read_token_file() {
+	if ! { [ -f "$1" ] && [ -r "$1" ]; }; then
+		die "token file $1 does not exist or cannot be read"
+	fi
+	exec 3<"$1"
+	[ -f /dev/fd/3 ] || die "token file $1 is not a regular file"
+	owner=$(stat -L -c %u /dev/fd/3)
+	mode=$(stat -L -c %a /dev/fd/3)
 	if [ -z "$ROOT" ]; then
 		[ "$owner" = 0 ] || die "token file $1 must be owned by root (uid 0), not uid $owner"
 	else
@@ -172,19 +180,26 @@ check_token_file() {
 	600 | 400) ;;
 	*) die "token file $1 must have mode 0600 or 0400, not 0$mode" ;;
 	esac
+	token=$(cat <&3)
+	exec 3<&-
+	[ -n "$token" ] || die "token file $1 is empty"
 }
 
 # read_token prompts for the enrollment token on the terminal without echo.
+# /dev/tty exists without a controlling terminal too, so only opening it
+# tells whether there is one.
 read_token() {
-	if ! { [ -r /dev/tty ] && [ -w /dev/tty ]; }; then
+	if ! (: </dev/tty) 2>/dev/null; then
 		die "no terminal to read the enrollment token from; pass --token-file"
 	fi
 	printf 'Enrollment token: ' >/dev/tty
 	stty -echo </dev/tty
-	trap 'stty echo </dev/tty' INT TERM
+	trap 'stty echo </dev/tty; exit 130' INT
+	trap 'stty echo </dev/tty; exit 143' TERM
 	IFS= read -r token </dev/tty || token=
 	stty echo </dev/tty
-	trap - INT TERM
+	trap 'exit 130' INT
+	trap 'exit 143' TERM
 	printf '\n' >/dev/tty
 	[ -n "$token" ] || die "no enrollment token entered"
 }
@@ -232,8 +247,9 @@ main() {
 	*) usage ;;
 	esac
 	[ -n "$ROOT" ] || [ "$(id -u)" = 0 ] || die "run as root: sudo sh install.sh ..."
-	# Checked before anything is downloaded, so a bad token file costs nothing.
-	[ -z "$token_file" ] || check_token_file "$token_file"
+	# Read before anything is downloaded, so a bad token file costs nothing.
+	token=''
+	[ -z "$token_file" ] || read_token_file "$token_file"
 	[ -n "$KEYS" ] || die "this install.sh embeds no trusted release keys and cannot verify a release"
 	check_os
 	arch=$(detect_arch)
@@ -249,8 +265,12 @@ main() {
 	*) die "--base-url must start with https:// or http://" ;;
 	esac
 
-	tmp=$(mktemp -d)
+	tmp=$(mktemp -d) || die "cannot create a temporary directory"
+	# A shell killed by a signal skips the EXIT trap; exiting from the
+	# signal's own trap runs it.
 	trap 'rm -rf "$tmp"' EXIT
+	trap 'exit 130' INT
+	trap 'exit 143' TERM
 	if [ -z "$version" ]; then
 		fetch "$base/index/releases.json" "$tmp/releases.json" || die "cannot download $base/index/releases.json"
 		version=$(newest_stable "$tmp/releases.json")
@@ -280,8 +300,8 @@ main() {
 		# shellcheck disable=SC2086 # listen_args holds checked words
 		"$bin" system install --role controller $listen_args </dev/null
 		set +f
-	elif [ -n "$token_file" ]; then
-		"$bin" system install --role agent <"$token_file"
+	elif [ -n "$token" ]; then
+		printf '%s\n' "$token" | "$bin" system install --role agent
 	elif [ -e "$ROOT/etc/darkpanel/agent/current" ]; then
 		echo "Saved agent credentials found; installing without an enrollment token."
 		echo "If this agent was revoked, rerun install.sh with --token-file."
```

- [ ] **Step 4: Generate install.sh**

Run: `TMPDIR=/root/.cache/plan3-tmp/7 GOTOOLCHAIN=local make install-sh && ls -l install.sh && grep -n "^KEYS=" install.sh`
Expected: `install.sh` with mode `-rwxr-xr-x` and the line `25:KEYS=''` (no production key exists yet).

- [ ] **Step 5: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/7 GOTOOLCHAIN=local
go test -count=1 -race -timeout 300s ./scripts/gen-install-sh/
go vet ./scripts/gen-install-sh/
golangci-lint run --allow-serial-runners ./scripts/gen-install-sh/...
shellcheck -s sh install.sh
make lint-sh
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/scripts/gen-install-sh`; vet, shellcheck, `make lint-sh` and `go mod tidy -diff` print nothing beyond make's command echo; golangci-lint prints `0 issues.` On a host without `shellcheck`, `dash`, `openssl` or `curl`/`wget` the matching tests skip; this host has them all.

- [ ] **Step 6: Commit**

```bash
git add install.sh.in install.sh scripts/gen-install-sh/main_test.go
git commit -m "fix(install-sh): harden token reading and cleanup"
```

Check `git ls-files -s install.sh` prints mode `100755`.

### Task 8: System install

**Files:**
- Create: `internal/install/install.go`
- Test: `internal/install/fakes_test.go`, `internal/install/install_test.go`

**Interfaces:**
- Consumes: C1, C3 (`Units`, `EnabledUnits`, `writeIfChanged`, `CheckAgentAddress`), C5 (the manifest accepts `dir`, `user` and `unit`), `secret.LoadOrCreateKey`, `store.Open`, `store.CoreMigrations`, `store.Migrate`, `platform.NewOwnership`, `platform.Systemd`, `core.Runner`, `errcode.EnrollTokenInvalid`, `agentruntime.BackupsKept`, `coretest.NewFakeClock` (tests).
- Produces: C8 and the install half of C10. Top-level names added: `HealthTimeout`, `pollInterval`, `Controller`, `CheckResult`, `CheckAccepted`, `CheckRefused`, `CheckUnreachable`, `Agent`, `Users`, `SystemUsers`, `Config`, `Config.check`, `installer`, `Install`, `dirSpec`, and the `installer` methods `dirSpecs`, `directories`, `chown`, `ensureUser`, `keys`, `database`, `units`, `restart`, `startController`, `enroll`, `startAgent`; tests: the C10 names of `fakes_test.go` and `install_test.go`, and `TestInstallController`, `TestInstallRerunChangesNothing`, `TestInstallRerunAfterEachCheckpoint`, `TestInstallAgentEnrollment`, `TestInstallControllerReenrollsRefusedCredentials`, `TestInstallWaitsForTheController`, `TestInstallResetsAFailedUnit`, `TestInstallRefusals`.

**Depends on:** Task 1, Task 3, Task 5

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the test fakes**

Create `internal/install/fakes_test.go`. `failOn` takes a method name, or `"<Method> <unit>"` to fail one unit only; `fakeSystemd.stuck` and `failOn` serve Task 9's uninstall tests as well:

```go
package install

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/platform"
)

// fakeSystemd is a platform.Systemd that records every call except State as
// "<Method> <units...>" and keeps unit states. A unit it was never told
// about is loaded and inactive; one in missing is not-found.
type fakeSystemd struct {
	mu      sync.Mutex
	calls   []string
	states  map[string]platform.UnitState
	missing map[string]bool
	stuck   map[string]bool  // units that stay active when stopped
	fail    map[string]error // by method name, or by "<Method> <unit>" for one unit
}

var _ platform.Systemd = (*fakeSystemd)(nil)

func newFakeSystemd() *fakeSystemd {
	return &fakeSystemd{states: map[string]platform.UnitState{}, missing: map[string]bool{}, stuck: map[string]bool{},
		fail: map[string]error{}}
}

func (f *fakeSystemd) record(method string, units []string, active string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(method+" "+strings.Join(units, " ")))
	if err := f.fail[method]; err != nil {
		return err
	}
	for _, u := range units {
		if err := f.fail[method+" "+u]; err != nil {
			return err
		}
	}
	if active != "" {
		for _, u := range units {
			if active != "inactive" || !f.stuck[u] {
				f.states[u] = platform.UnitState{LoadState: "loaded", ActiveState: active}
			}
		}
	}
	return nil
}

func (f *fakeSystemd) setState(unit, active string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.states[unit] = platform.UnitState{LoadState: "loaded", ActiveState: active}
}

func (f *fakeSystemd) failOn(method string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err == nil {
		delete(f.fail, method)
		return
	}
	f.fail[method] = err
}

func (f *fakeSystemd) recorded() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.calls)
}

func (f *fakeSystemd) StartTransientService(_ context.Context, name string, _ platform.TransientService) error {
	return f.record("StartTransientService", []string{name}, "active")
}

func (f *fakeSystemd) StartTransientTimer(_ context.Context, name string, _ platform.TransientTimer) error {
	return f.record("StartTransientTimer", []string{name}, "active")
}

func (f *fakeSystemd) Start(_ context.Context, unit string) error {
	return f.record("Start", []string{unit}, "active")
}

func (f *fakeSystemd) Stop(_ context.Context, unit string) error {
	return f.record("Stop", []string{unit}, "inactive")
}

func (f *fakeSystemd) Restart(_ context.Context, unit string) error {
	return f.record("Restart", []string{unit}, "active")
}

func (f *fakeSystemd) Kill(_ context.Context, unit string, _ syscall.Signal) error {
	return f.record("Kill", []string{unit}, "")
}

func (f *fakeSystemd) ResetFailed(_ context.Context, unit string) error {
	return f.record("ResetFailed", []string{unit}, "inactive")
}

func (f *fakeSystemd) Enable(_ context.Context, unitFiles ...string) error {
	return f.record("Enable", unitFiles, "")
}

func (f *fakeSystemd) Disable(_ context.Context, unitFiles ...string) error {
	return f.record("Disable", unitFiles, "")
}

func (f *fakeSystemd) Reload(context.Context) error { return f.record("Reload", nil, "") }

func (f *fakeSystemd) State(_ context.Context, unit string) (platform.UnitState, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.fail["State"]; err != nil {
		return platform.UnitState{}, err
	}
	if f.missing[unit] {
		return platform.UnitState{LoadState: "not-found", ActiveState: "inactive", SubState: "dead"}, nil
	}
	if st, ok := f.states[unit]; ok {
		return st, nil
	}
	return platform.UnitState{LoadState: "loaded", ActiveState: "inactive", SubState: "dead"}, nil
}

// fakeUsers is a user database in memory.
type fakeUsers struct {
	mu    sync.Mutex
	users map[string][2]int
}

func (u *fakeUsers) Lookup(name string) (int, int, bool, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	ids, ok := u.users[name]
	return ids[0], ids[1], ok, nil
}

// fakeRunner records every command and runs useradd and userdel against
// users. exit makes every command exit with that status.
type fakeRunner struct {
	mu    sync.Mutex
	users *fakeUsers
	cmds  []string
	exit  int
}

func (r *fakeRunner) Run(_ context.Context, cmd core.Cmd) (core.CmdResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.cmds = append(r.cmds, strings.Join(append([]string{cmd.Path}, cmd.Args...), " "))
	if r.exit != 0 {
		return core.CmdResult{ExitCode: r.exit, Stderr: []byte("refused\n")}, nil
	}
	name := cmd.Args[len(cmd.Args)-1]
	r.users.mu.Lock()
	defer r.users.mu.Unlock()
	switch cmd.Path {
	case "/usr/sbin/useradd":
		r.users.users[name] = [2]int{990, 990}
	case "/usr/sbin/userdel":
		delete(r.users.users, name)
	}
	return core.CmdResult{}, nil
}

func (r *fakeRunner) commands() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.cmds)
}

// fakeController is the local controller: healthy from its unhealthy-th
// call to Healthy on, and issuing token on EnrollLocal.
type fakeController struct {
	mu        sync.Mutex
	unhealthy int
	health    int
	enrolls   int
}

func (c *fakeController) Healthy(context.Context) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.health++
	if c.health <= c.unhealthy {
		return os.ErrDeadlineExceeded
	}
	return nil
}

func (c *fakeController) EnrollLocal(context.Context) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.enrolls++
	return "dpe1.local", nil
}

// fakeAgent answers Check with check and, on Enroll, saves a credential
// generation the way the agent does: gen-1 and the current symlink.
type fakeAgent struct {
	mu        sync.Mutex
	layout    Layout
	check     CheckResult
	enrollErr error
	tokens    []string
}

func (a *fakeAgent) Check(context.Context) (CheckResult, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.check, nil
}

func (a *fakeAgent) Enroll(_ context.Context, token string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.tokens = append(a.tokens, token)
	if a.enrollErr != nil {
		return a.enrollErr
	}
	dir := a.layout.Paths.AgentTLSDir
	if err := os.MkdirAll(filepath.Join(dir, "gen-1"), 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, "gen-1", "agent.crt"), []byte("cert\n"), 0o600); err != nil {
		return err
	}
	_ = os.Remove(a.layout.Paths.AgentTLSCurrent())
	return os.Symlink("gen-1", a.layout.Paths.AgentTLSCurrent())
}

// chowns records every Chown call; the tests run without root.
type chowns struct {
	mu     sync.Mutex
	owners map[string][2]int
}

func (c *chowns) chown(path string, uid, gid int) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.owners[path] = [2]int{uid, gid}
	return nil
}

func (c *chowns) of(path string) ([2]int, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	ids, ok := c.owners[path]
	return ids, ok
}

// testHost is one fake host with every dependency of Install and
// PlanUninstall.
type testHost struct {
	layout     Layout
	systemd    *fakeSystemd
	users      *fakeUsers
	runner     *fakeRunner
	controller *fakeController
	agent      *fakeAgent
	chowns     *chowns
	clock      *coretest.FakeClock
	out        *bytes.Buffer
}

var installTime = time.Date(2026, 10, 20, 9, 0, 0, 0, time.UTC)

func newTestHost(t *testing.T) *testHost {
	t.Helper()
	l := Rooted(t.TempDir())
	users := &fakeUsers{users: map[string][2]int{}}
	return &testHost{
		layout:     l,
		systemd:    newFakeSystemd(),
		users:      users,
		runner:     &fakeRunner{users: users},
		controller: &fakeController{},
		agent:      &fakeAgent{layout: l, check: CheckAccepted},
		chowns:     &chowns{owners: map[string][2]int{}},
		clock:      coretest.NewFakeClock(installTime),
		out:        &bytes.Buffer{},
	}
}

// config is an install of role on h.
func (h *testHost) config(role Role) Config {
	return Config{
		Layout: h.layout, Role: role, BinaryVersion: "0.2.0", Systemd: h.systemd, Exec: h.runner, Users: h.users,
		Controller: h.controller, Agent: h.agent, Clock: h.clock, Out: h.out, Chown: h.chowns.chown,
		PollInterval: time.Millisecond, HealthTimeout: time.Second,
	}
}
```

- [ ] **Step 2: Write the failing test**

Create `internal/install/install_test.go`:

```go
package install

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// manifest lists agent.db's ownership manifest as "kind path" lines, with
// " (module <id>)" after an entry of a module other than the core.
func manifest(t *testing.T, l Layout) []string {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, l.Paths.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	entries, err := platform.NewOwnership(db).List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		line := e.Kind + " " + e.Path
		if e.Module != core.ModuleIDCore {
			line += " (module " + e.Module + ")"
		}
		out = append(out, line)
	}
	return out
}

// wantManifest is OwnedPaths(l, role) as manifest lists it.
func wantManifest(l Layout, role Role) []string {
	var out []string
	for _, op := range OwnedPaths(l, role) {
		out = append(out, op.Kind+" "+op.Path)
	}
	slices.SortFunc(out, func(a, b string) int {
		return strings.Compare(a[strings.IndexByte(a, ' ')+1:], b[strings.IndexByte(b, ' ')+1:])
	})
	return out
}

func wantMode(t *testing.T, path string, mode fs.FileMode) {
	t.Helper()
	st, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != mode {
		t.Errorf("%s has mode %o, want %o", path, st.Mode().Perm(), mode)
	}
}

func readBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path) //nolint:gosec // G304: a test's temporary path
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestInstallController(t *testing.T) {
	h := newTestHost(t)
	cfg := h.config(RoleController)
	cfg.AgentListen = []string{"10.77.0.10:7443"}
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatalf("Install: %v\n%s", err, h.out)
	}
	l, p := h.layout, h.layout.Paths
	for _, d := range []struct {
		path string
		mode fs.FileMode
	}{
		{l.DataDir, 0o755}, {l.ConfigDir, 0o755}, {p.AgentDir, 0o700}, {p.UpdateDir, 0o700},
		{p.AgentTLSDir, 0o700}, {p.BinaryDir, 0o755}, {p.ControllerDir, 0o700},
	} {
		wantMode(t, d.path, d.mode)
	}
	for _, path := range []string{l.DataDir, l.ConfigDir, p.AgentDir, p.UpdateDir, p.AgentTLSDir, p.BinaryDir, p.AgentKey} {
		if ids, ok := h.chowns.of(path); !ok || ids != [2]int{0, 0} {
			t.Errorf("%s owned by %v (recorded %v), want root", path, ids, ok)
		}
	}
	for _, path := range []string{p.ControllerDir, p.ControllerKey} {
		if ids, _ := h.chowns.of(path); ids != [2]int{990, 990} {
			t.Errorf("%s owned by %v, want the darkpanel user", path, ids)
		}
	}
	wantMode(t, p.AgentKey, 0o600)
	wantMode(t, p.ControllerKey, 0o600)
	if got, want := h.runner.commands(), []string{"/usr/sbin/useradd --system --no-create-home --shell /usr/sbin/nologin darkpanel"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
	if got, want := manifest(t, l), wantManifest(l, RoleController); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest\n got %v\nwant %v", got, want)
	}
	files, err := Units(l, RoleController, cfg.AgentListen, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range files {
		if got := readBytes(t, f.Path); string(got) != string(f.Content) {
			t.Errorf("%s holds %q", f.Path, got)
		}
	}
	want := []string{
		"Reload", "Enable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service",
		"Restart darkpanel-controller.service", "Restart darkpanel-agent.service",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
	if h.controller.enrolls != 1 || !reflect.DeepEqual(h.agent.tokens, []string{"dpe1.local"}) {
		t.Errorf("enroll-local ran %d times, agent enrolled with %v; want once with dpe1.local", h.controller.enrolls, h.agent.tokens)
	}
	if !strings.Contains(h.out.String(), "sudo darkpanel user create --username admin") {
		t.Errorf("output does not say how to create the first admin:\n%s", h.out)
	}
}

func TestInstallRerunChangesNothing(t *testing.T) {
	for _, role := range []Role{RoleController, RoleAgent} {
		t.Run(string(role), func(t *testing.T) {
			h := newTestHost(t)
			cfg := h.config(role)
			if role == RoleAgent {
				cfg.Token = "dpe1.agent"
			}
			ctx := context.Background()
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("Install: %v", err)
			}
			key := readBytes(t, h.layout.Paths.AgentKey)
			before := manifest(t, h.layout)
			h.out.Reset()
			cfg.Token = ""
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("second Install: %v\n%s", err, h.out)
			}
			if string(readBytes(t, h.layout.Paths.AgentKey)) != string(key) {
				t.Error("the second install replaced the agent key")
			}
			if after := manifest(t, h.layout); !reflect.DeepEqual(after, before) {
				t.Errorf("manifest changed\nbefore %v\n after %v", before, after)
			}
			if len(h.agent.tokens) != 1 {
				t.Errorf("the agent enrolled with %v, want once", h.agent.tokens)
			}
			for _, line := range []string{"keys: 0 created", "units: 0 written", "enrollment: the saved credentials are valid"} {
				if !strings.Contains(h.out.String(), line) {
					t.Errorf("second run output lacks %q:\n%s", line, h.out)
				}
			}
			if role == RoleController && len(h.runner.commands()) != 1 {
				t.Errorf("useradd ran %d times, want once", len(h.runner.commands()))
			}
		})
	}
}

// TestInstallRerunAfterEachCheckpoint fails Install at each step, reruns it
// without the failure, and expects the state of an install that never
// failed.
func TestInstallRerunAfterEachCheckpoint(t *testing.T) {
	boom := errors.New("boom")
	const agentRestart = "Restart darkpanel-agent.service"
	tests := []struct {
		name   string
		step   string // the step the injected failure stops
		inject func(h *testHost)
		fix    func(h *testHost)
	}{
		{"useradd fails", "directories", func(h *testHost) { h.runner.exit = 1 }, func(h *testHost) { h.runner.exit = 0 }},
		{"chown fails", "directories", nil, nil},
		{"reload fails", "units", func(h *testHost) { h.systemd.failOn("Reload", boom) }, func(h *testHost) { h.systemd.failOn("Reload", nil) }},
		{"controller start fails", "controller", func(h *testHost) { h.systemd.failOn("Restart", boom) }, func(h *testHost) { h.systemd.failOn("Restart", nil) }},
		{"controller never healthy", "controller", func(h *testHost) { h.controller.unhealthy = 1 << 30 }, func(h *testHost) { h.controller.unhealthy = 0 }},
		{"enrollment fails", "enrollment", func(h *testHost) { h.agent.enrollErr = boom }, func(h *testHost) { h.agent.enrollErr = nil }},
		{"agent start fails", "agent", func(h *testHost) { h.systemd.failOn(agentRestart, boom) }, func(h *testHost) { h.systemd.failOn(agentRestart, nil) }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost(t)
			cfg := h.config(RoleController)
			cfg.HealthTimeout = 20 * time.Millisecond
			ctx := context.Background()
			brk, fix := tt.inject, tt.fix
			if brk == nil {
				calls := 0
				cfg.Chown = func(path string, uid, gid int) error {
					if calls++; calls == 4 {
						return boom
					}
					return h.chowns.chown(path, uid, gid)
				}
				brk, fix = func(*testHost) {}, func(*testHost) {}
			}
			brk(h)
			err := Install(ctx, cfg)
			if err == nil {
				t.Fatal("Install succeeded with a failure injected")
			}
			if !strings.HasPrefix(err.Error(), "install: "+tt.step+": ") {
				t.Errorf("Install = %v, want a failure of the %s step", err, tt.step)
			}
			fix(h)
			cfg.Chown = h.chowns.chown
			if err := Install(ctx, cfg); err != nil {
				t.Fatalf("rerun: %v\n%s", err, h.out)
			}
			if got, want := manifest(t, h.layout), wantManifest(h.layout, RoleController); !reflect.DeepEqual(got, want) {
				t.Errorf("manifest\n got %v\nwant %v", got, want)
			}
			if _, err := os.Lstat(h.layout.Paths.AgentTLSCurrent()); err != nil {
				t.Errorf("no credentials after the rerun: %v", err)
			}
			if ids, _ := h.chowns.of(h.layout.Paths.ControllerKey); ids != [2]int{990, 990} {
				t.Errorf("controller key owned by %v", ids)
			}
			last := h.systemd.recorded()
			if last[len(last)-1] != "Restart darkpanel-agent.service" {
				t.Errorf("the rerun ended with %q, want the agent started", last[len(last)-1])
			}
		})
	}
}

func TestInstallAgentEnrollment(t *testing.T) {
	tokenInvalid := errcode.New(errcode.EnrollTokenInvalid, "invalid enrollment token")
	tests := []struct {
		name      string
		saved     bool
		check     CheckResult
		token     string
		enrollErr error
		wantErr   string
		wantOut   string
		tokens    []string
	}{
		{name: "fresh with a token", token: "dpe1.t", wantOut: "enrollment: enrolled", tokens: []string{"dpe1.t"}},
		{name: "fresh without a token", wantErr: "no saved credentials and no enrollment token"},
		{name: "valid credentials", saved: true, check: CheckAccepted, wantOut: "enrollment: the saved credentials are valid"},
		{name: "valid credentials and a token", saved: true, check: CheckAccepted, token: "dpe1.t", wantOut: "the enrollment token was not used"},
		{name: "refused without a token", saved: true, check: CheckRefused, wantErr: "rerun install.sh with --token-file"},
		{name: "refused with a token", saved: true, check: CheckRefused, token: "dpe1.t", wantOut: "enrollment: enrolled", tokens: []string{"dpe1.t"}},
		{name: "controller unreachable", saved: true, check: CheckUnreachable, token: "dpe1.t", wantOut: "the saved credentials are kept"},
		{name: "token refused", token: "dpe1.t", enrollErr: tokenInvalid, wantErr: "revoke the host and create a new token", tokens: []string{"dpe1.t"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newTestHost(t)
			if tt.saved {
				if err := h.agent.Enroll(context.Background(), "earlier"); err != nil {
					t.Fatal(err)
				}
				h.agent.tokens = nil
			}
			h.agent.check, h.agent.enrollErr = tt.check, tt.enrollErr
			cfg := h.config(RoleAgent)
			cfg.Token = tt.token
			err := Install(context.Background(), cfg)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("Install = %v, want an error containing %q", err, tt.wantErr)
				}
			} else if err != nil {
				t.Fatalf("Install: %v", err)
			}
			if tt.wantOut != "" && !strings.Contains(h.out.String(), tt.wantOut) {
				t.Errorf("output lacks %q:\n%s", tt.wantOut, h.out)
			}
			if !reflect.DeepEqual(h.agent.tokens, tt.tokens) {
				t.Errorf("enrolled with %v, want %v", h.agent.tokens, tt.tokens)
			}
			if h.controller.enrolls != 0 || len(h.runner.commands()) != 0 {
				t.Error("the agent role used the controller or created a user")
			}
			if _, err := os.Lstat(h.layout.Paths.ControllerDir); !errors.Is(err, fs.ErrNotExist) {
				t.Error("the agent role created the controller directory")
			}
		})
	}
}

func TestInstallControllerReenrollsRefusedCredentials(t *testing.T) {
	for _, check := range []CheckResult{CheckRefused, CheckUnreachable} {
		h := newTestHost(t)
		if err := h.agent.Enroll(context.Background(), "earlier"); err != nil {
			t.Fatal(err)
		}
		h.agent.tokens, h.agent.check = nil, check
		if err := Install(context.Background(), h.config(RoleController)); err != nil {
			t.Fatal(err)
		}
		if h.controller.enrolls != 1 || !reflect.DeepEqual(h.agent.tokens, []string{"dpe1.local"}) {
			t.Errorf("check %d: enroll-local ran %d times, tokens %v", check, h.controller.enrolls, h.agent.tokens)
		}
	}
}

func TestInstallWaitsForTheController(t *testing.T) {
	h := newTestHost(t)
	h.controller.unhealthy = 3
	if err := Install(context.Background(), h.config(RoleController)); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if h.controller.health != 4 {
		t.Errorf("Healthy asked %d times, want 4", h.controller.health)
	}

	h = newTestHost(t)
	h.controller.unhealthy = 1 << 30
	cfg := h.config(RoleController)
	cfg.HealthTimeout = 30 * time.Millisecond
	err := Install(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "did not answer /healthz") {
		t.Fatalf("Install = %v, want the health wait to fail", err)
	}
	if len(h.agent.tokens) != 0 {
		t.Error("the agent enrolled although the controller never answered")
	}
}

func TestInstallResetsAFailedUnit(t *testing.T) {
	h := newTestHost(t)
	h.systemd.setState("darkpanel-agent.service", "failed")
	cfg := h.config(RoleAgent)
	cfg.Token = "dpe1.t"
	if err := Install(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}
	got := h.systemd.recorded()
	if want := []string{"ResetFailed darkpanel-agent.service", "Restart darkpanel-agent.service"}; !reflect.DeepEqual(got[len(got)-2:], want) {
		t.Errorf("systemd calls end %v, want %v", got[len(got)-2:], want)
	}
}

func TestInstallRefusals(t *testing.T) {
	h := newTestHost(t)
	tests := map[string]func(c *Config){
		"unknown role":             func(c *Config) { c.Role = "both" },
		"token for the controller": func(c *Config) { c.Token = "dpe1.t" },
		"listen for an agent":      func(c *Config) { c.Role = RoleAgent; c.AgentListen = []string{"10.0.0.1:7443"} },
		"bad listen address":       func(c *Config) { c.AgentListen = []string{"10.0.0.1"} },
		"bad advertise address":    func(c *Config) { c.AgentAdvertise = "h:1 --dev" },
		"no controller":            func(c *Config) { c.Controller = nil },
		"no systemd":               func(c *Config) { c.Systemd = nil },
	}
	for name, change := range tests {
		cfg := h.config(RoleController)
		change(&cfg)
		if err := Install(context.Background(), cfg); err == nil {
			t.Errorf("%s: Install succeeded", name)
		}
	}
	if _, err := os.Lstat(h.layout.DataDir); !errors.Is(err, fs.ErrNotExist) {
		t.Error("a refused install created directories")
	}

	// A directory that is a symlink is refused rather than followed.
	if err := os.MkdirAll(filepath.Dir(h.layout.Paths.AgentDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), h.layout.Paths.AgentDir); err != nil {
		t.Fatal(err)
	}
	if err := Install(context.Background(), h.config(RoleController)); err == nil || !strings.Contains(err.Error(), "is not a directory") {
		t.Errorf("Install over a symlinked directory = %v", err)
	}
}
```

- [ ] **Step 3: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/8 && TMPDIR=/root/.cache/plan3-tmp/8 GOTOOLCHAIN=local go test -count=1 ./internal/install/`
Expected: FAIL, the build stops with `undefined: Config` (and the other names of `install.go`).

- [ ] **Step 4: Write the implementation**

Create `internal/install/install.go`:

```go
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update"
)

// The waits of system install.
const (
	// HealthTimeout bounds the wait for a started controller to answer
	// /healthz on its socket.
	HealthTimeout = 60 * time.Second
	// pollInterval is how often a wait asks again.
	pollInterval = time.Second
)

// Controller is the local controller, reached over its socket; the
// controller role needs it.
type Controller interface {
	// Healthy fails unless GET /healthz on the controller socket answers
	// status ok.
	Healthy(ctx context.Context) error
	// EnrollLocal runs core.host.enroll-local and returns its token.
	EnrollLocal(ctx context.Context) (string, error)
}

// CheckResult is the outcome of one handshake with the saved agent
// credentials, the exit status of darkpanel agent check.
type CheckResult int

// The outcomes of Agent.Check.
const (
	CheckAccepted    CheckResult = iota // the controller accepted the credentials
	CheckRefused                        // the controller refused them
	CheckUnreachable                    // no handshake completed
)

// Agent is this host's agent credentials.
type Agent interface {
	// Check performs one mTLS handshake with the saved credentials.
	Check(ctx context.Context) (CheckResult, error)
	// Enroll consumes token and writes a new credential generation.
	Enroll(ctx context.Context, token string) error
}

// Users looks up system users.
type Users interface {
	// Lookup returns name's uid and gid; found is false when there is no
	// such user.
	Lookup(name string) (uid, gid int, found bool, err error)
}

// SystemUsers reads the system's user database.
type SystemUsers struct{}

// Lookup implements Users with os/user.
func (SystemUsers) Lookup(name string) (int, int, bool, error) {
	u, err := user.Lookup(name)
	var unknown user.UnknownUserError
	if errors.As(err, &unknown) {
		return 0, 0, false, nil
	}
	if err != nil {
		return 0, 0, false, fmt.Errorf("install: look up user %s: %w", name, err)
	}
	uid, err := strconv.Atoi(u.Uid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("install: user %s has uid %q", name, u.Uid)
	}
	gid, err := strconv.Atoi(u.Gid)
	if err != nil {
		return 0, 0, false, fmt.Errorf("install: user %s has gid %q", name, u.Gid)
	}
	return uid, gid, true, nil
}

// Config is what system install needs.
type Config struct {
	Layout         Layout
	Role           Role
	AgentListen    []string // controller role: the --agent-listen values
	AgentAdvertise string   // controller role: the --agent-advertise value
	// Token is the agent role's enrollment token from standard input,
	// trimmed; empty when none was given.
	Token string
	// Modules are the compiled-in modules, whose agent migrations agent.db
	// gets as the agent would apply them.
	Modules       []core.Manifest
	BinaryVersion string
	Systemd       platform.Systemd
	Exec          core.Runner
	Users         Users
	Controller    Controller // controller role only
	Agent         Agent
	Clock         core.Clock
	Out           io.Writer
	// Chown gives path to uid and gid; nil means os.Chown.
	Chown func(path string, uid, gid int) error
	// PollInterval and HealthTimeout pace the wait for the controller; zero
	// means 1 s and HealthTimeout.
	PollInterval  time.Duration
	HealthTimeout time.Duration
}

func (c Config) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"Systemd", c.Systemd != nil}, {"Exec", c.Exec != nil}, {"Users", c.Users != nil}, {"Agent", c.Agent != nil},
		{"Clock", c.Clock != nil}, {"Out", c.Out != nil}, {"BinaryVersion", c.BinaryVersion != ""},
		{"Controller", c.Role != RoleController || c.Controller != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("install: Config.%s is required", r.name)
		}
	}
	if _, err := ParseRole(string(c.Role)); err != nil {
		return err
	}
	if c.Role == RoleController && c.Token != "" {
		return errors.New("install: an enrollment token is only for the agent role")
	}
	// Units checks the flags too; checking them here refuses them before
	// anything on the host changes.
	if c.Role != RoleController && (len(c.AgentListen) > 0 || c.AgentAdvertise != "") {
		return errors.New("install: --agent-listen and --agent-advertise are for the controller role")
	}
	for _, a := range c.AgentListen {
		if err := CheckAgentAddress(a); err != nil {
			return err
		}
	}
	if c.AgentAdvertise != "" {
		return CheckAgentAddress(c.AgentAdvertise)
	}
	return nil
}

// installer is one run of system install.
type installer struct {
	cfg      Config
	uid, gid int // the service user's, controller role only
}

// Install installs role on this host. Every step works out from the host
// what is already done, so rerunning Install after any failure, or after a
// completed install, finishes or repairs it and changes nothing else.
func Install(ctx context.Context, cfg Config) error {
	if err := cfg.check(); err != nil {
		return err
	}
	if cfg.Chown == nil {
		cfg.Chown = os.Chown
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = pollInterval
	}
	if cfg.HealthTimeout == 0 {
		cfg.HealthTimeout = HealthTimeout
	}
	in := &installer{cfg: cfg}
	steps := []struct {
		name string
		run  func(context.Context) (string, error)
	}{
		{"directories", in.directories},
		{"keys", in.keys},
		{"database", in.database},
		{"units", in.units},
		{"controller", in.startController},
		{"enrollment", in.enroll},
		{"agent", in.startAgent},
	}
	for _, s := range steps {
		line, err := s.run(ctx)
		if err != nil {
			return fmt.Errorf("install: %s: %w", s.name, err)
		}
		if line != "" {
			_, _ = fmt.Fprintf(cfg.Out, "%s: %s\n", s.name, line)
		}
	}
	if cfg.Role == RoleController {
		_, _ = fmt.Fprintln(cfg.Out, "Create the first admin over the controller socket: sudo darkpanel user create --username admin")
	}
	return nil
}

// dirSpec is one directory system install creates.
type dirSpec struct {
	path    string
	mode    fs.FileMode
	service bool // owned by the service user instead of root
}

func (in *installer) dirSpecs() []dirSpec {
	l, p := in.cfg.Layout, in.cfg.Layout.Paths
	specs := []dirSpec{
		{path: l.DataDir, mode: 0o755}, {path: l.ConfigDir, mode: 0o755}, {path: p.AgentDir, mode: 0o700},
		{path: p.UpdateDir, mode: 0o700}, {path: p.AgentTLSDir, mode: 0o700}, {path: p.BinaryDir, mode: 0o755},
	}
	if in.cfg.Role == RoleController {
		specs = append(specs, dirSpec{path: p.ControllerDir, mode: 0o700, service: true})
	}
	return specs
}

// directories creates the service user for the controller role, then every
// directory with its owner and mode; an existing directory gets them too.
func (in *installer) directories(ctx context.Context) (string, error) {
	if in.cfg.Role == RoleController {
		if err := in.ensureUser(ctx); err != nil {
			return "", err
		}
	}
	for _, d := range in.dirSpecs() {
		if err := os.MkdirAll(d.path, d.mode); err != nil {
			return "", err
		}
		st, err := os.Lstat(d.path)
		if err != nil {
			return "", err
		}
		if !st.IsDir() {
			return "", fmt.Errorf("%s is not a directory", d.path)
		}
		// MkdirAll applies the umask and leaves an existing directory's mode
		// alone.
		if err := os.Chmod(d.path, d.mode); err != nil {
			return "", err
		}
		if err := in.chown(d.path, d.service); err != nil {
			return "", err
		}
	}
	if err := os.MkdirAll(filepath.Dir(in.cfg.Layout.Paths.BinaryLink), 0o755); err != nil { //nolint:gosec // G301: a directory on PATH
		return "", err
	}
	return "ready", nil
}

func (in *installer) chown(path string, service bool) error {
	uid, gid := 0, 0
	if service {
		uid, gid = in.uid, in.gid
	}
	if err := in.cfg.Chown(path, uid, gid); err != nil {
		return fmt.Errorf("chown %s: %w", path, err)
	}
	return nil
}

// ensureUser creates the service user unless it exists, and remembers its
// ids.
func (in *installer) ensureUser(ctx context.Context) error {
	uid, gid, found, err := in.cfg.Users.Lookup(ServiceUser)
	if err != nil {
		return err
	}
	if !found {
		res, err := in.cfg.Exec.Run(ctx, core.Cmd{
			Path: "/usr/sbin/useradd",
			Args: []string{"--system", "--no-create-home", "--shell", "/usr/sbin/nologin", ServiceUser},
		})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("useradd %s exited %d: %s", ServiceUser, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
		if uid, gid, found, err = in.cfg.Users.Lookup(ServiceUser); err != nil {
			return err
		}
		if !found {
			return fmt.Errorf("useradd succeeded but there is no user %s", ServiceUser)
		}
	}
	in.uid, in.gid = uid, gid
	return nil
}

// keys creates each role key that is absent. An existing key is never
// replaced, because the database it seals is readable only with it; it only
// gets its owner again.
func (in *installer) keys(context.Context) (string, error) {
	p := in.cfg.Layout.Paths
	created := 0
	keys := []struct {
		path    string
		service bool
	}{{p.AgentKey, false}}
	if in.cfg.Role == RoleController {
		keys = append(keys, struct {
			path    string
			service bool
		}{p.ControllerKey, true})
	}
	for _, k := range keys {
		st, err := os.Lstat(k.path)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			if _, err := secret.LoadOrCreateKey(k.path); err != nil {
				return "", err
			}
			created++
		case err != nil:
			return "", err
		case !st.Mode().IsRegular():
			return "", fmt.Errorf("%s is not a regular file", k.path)
		}
		if err := in.chown(k.path, k.service); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("%d created, %d kept", created, len(keys)-created), nil
}

// database migrates agent.db as the agent would and records every path this
// role creates in the ownership manifest. Recording an entry again changes
// nothing.
func (in *installer) database(ctx context.Context) (string, error) {
	p := in.cfg.Layout.Paths
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		return "", err
	}
	defer func() { _ = db.Close() }()
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		return "", err
	}
	var sets []store.MigrationSet
	for _, m := range in.cfg.Modules {
		if fsys := m.Migrations[core.SideAgent]; fsys != nil && m.ID != core.ModuleIDCore {
			sets = append(sets, store.MigrationSet{Module: m.ID, FS: fsys})
		}
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: in.cfg.BinaryVersion, BackupRoot: p.AgentBackups(),
		Keep: agentruntime.BackupsKept, Now: in.cfg.Clock.Now(),
	}, coreSet, sets)
	if err != nil {
		return "", err
	}
	for _, f := range res.Failed {
		_, _ = fmt.Fprintf(in.cfg.Out, "warning: the agent migrations of module %s failed; the agent reports it unavailable\n", f.Module)
	}
	own := platform.NewOwnership(db)
	now := in.cfg.Clock.Now().UTC()
	owned := OwnedPaths(in.cfg.Layout, in.cfg.Role)
	for _, op := range owned {
		e := core.OwnedEntry{Path: op.Path, Module: core.ModuleIDCore, Kind: op.Kind, CreatedAt: now}
		if err := own.Record(ctx, e); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("agent.db migrated, %d owned paths recorded", len(owned)), nil
}

// units writes the unit files that differ, reloads systemd and enables the
// role's units.
func (in *installer) units(ctx context.Context) (string, error) {
	files, err := Units(in.cfg.Layout, in.cfg.Role, in.cfg.AgentListen, in.cfg.AgentAdvertise)
	if err != nil {
		return "", err
	}
	written := 0
	for _, f := range files {
		wrote, err := writeIfChanged(f.Path, f.Content)
		if err != nil {
			return "", err
		}
		if wrote {
			written++
		}
	}
	if err := in.cfg.Systemd.Reload(ctx); err != nil {
		return "", err
	}
	if err := in.cfg.Systemd.Enable(ctx, EnabledUnits(in.cfg.Role)...); err != nil {
		return "", err
	}
	return fmt.Sprintf("%d written, %d unchanged, enabled %s", written, len(files)-written,
		strings.Join(EnabledUnits(in.cfg.Role), ", ")), nil
}

// restart starts unit with the installed binary, clearing a failed state
// first: a unit that hit its start limit refuses a plain start.
func (in *installer) restart(ctx context.Context, unit string) error {
	st, err := in.cfg.Systemd.State(ctx, unit)
	if err != nil {
		return err
	}
	if st.ActiveState == "failed" {
		if err := in.cfg.Systemd.ResetFailed(ctx, unit); err != nil {
			return err
		}
	}
	return in.cfg.Systemd.Restart(ctx, unit)
}

// startController starts the controller and waits until it answers on its
// socket. The agent role has no controller.
func (in *installer) startController(ctx context.Context) (string, error) {
	if in.cfg.Role != RoleController {
		return "", nil
	}
	if err := in.restart(ctx, update.ControllerUnit); err != nil {
		return "", err
	}
	wait, cancel := context.WithTimeout(ctx, in.cfg.HealthTimeout)
	defer cancel()
	for {
		err := in.cfg.Controller.Healthy(wait)
		if err == nil {
			return "started and healthy", nil
		}
		select {
		case <-wait.Done():
			return "", fmt.Errorf("the controller did not answer /healthz on its socket within %s (see journalctl -u %s): %w",
				in.cfg.HealthTimeout, update.ControllerUnit, err)
		case <-time.After(in.cfg.PollInterval):
		}
	}
}

// enroll keeps saved credentials the controller accepts, and otherwise
// enrolls: the controller role with a token it issues to itself, the agent
// role with the token from standard input.
func (in *installer) enroll(ctx context.Context) (string, error) {
	_, err := os.Lstat(in.cfg.Layout.Paths.AgentTLSCurrent())
	saved := err == nil
	if saved {
		res, err := in.cfg.Agent.Check(ctx)
		if err != nil {
			return "", err
		}
		switch {
		case res == CheckAccepted && in.cfg.Token != "":
			return "the saved credentials are valid; the enrollment token was not used", nil
		case res == CheckAccepted:
			return "the saved credentials are valid", nil
		case res == CheckUnreachable && in.cfg.Role == RoleAgent:
			return "warning: the controller is unreachable; the saved credentials are kept", nil
		}
	}
	token := in.cfg.Token
	switch {
	case in.cfg.Role == RoleController:
		if token, err = in.cfg.Controller.EnrollLocal(ctx); err != nil {
			return "", err
		}
	case token == "" && saved:
		return "", errors.New("the controller refused this agent's saved credentials; create a new token for this host and rerun install.sh with --token-file")
	case token == "":
		return "", errors.New("this agent has no saved credentials and no enrollment token was given; rerun install.sh with --token-file, or on a terminal to be prompted")
	}
	if err := in.cfg.Agent.Enroll(ctx, token); err != nil {
		var e *errcode.Error
		if errors.As(err, &e) && e.Code == errcode.EnrollTokenInvalid {
			return "", fmt.Errorf("the controller refused the enrollment token: it is expired or mistyped, or this host's token was used but no credentials were saved; revoke the host and create a new token: %w", err)
		}
		return "", err
	}
	return "enrolled", nil
}

// startAgent starts the agent with the installed binary.
func (in *installer) startAgent(ctx context.Context) (string, error) {
	if err := in.restart(ctx, update.AgentUnit); err != nil {
		return "", err
	}
	return "started " + update.AgentUnit, nil
}
```

- [ ] **Step 5: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/8 GOTOOLCHAIN=local
go test -count=1 -race -timeout 300s ./internal/install/
go vet ./internal/install/
golangci-lint run --allow-serial-runners ./internal/install/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/install`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 6: Commit**

```bash
git add internal/install/install.go internal/install/fakes_test.go internal/install/install_test.go
git commit -m "feat(install): add system install"
```

### Task 9: Uninstall

**Files:**
- Create: `internal/install/uninstall.go`
- Test: `internal/install/uninstall_test.go`

**Interfaces:**
- Consumes: C1 (`OwnedPaths`, `AgentLockName`, `RecoverUnit`, `Rooted`), C5, C8 (`Users`, `pollInterval`, `Install`, `Config`), the install half of C10, `core.Uninstaller`, `platform.NewFiles`, `platform.OwnedPaths`, `platform.NewOwnership`, `secret.LoadOrCreateKey`, `secret.NewSealer`, `unix.Open`, `unix.Flock`.
- Produces: C9. Top-level names added: `StopTimeout`, `UninstallConfig`, `UninstallConfig.check`, `Uninstall`, `uninstallStep`, `PlanUninstall`, `within`, `describe`, `removeTree`, `clearExcept`, and the `Uninstall` methods `load`, `owned`, `kept`, `selectEntries`, `plan`, `Describe`, `Run`, `Close`, `closeDB`, `forget`, `stopServices`, `waitStopped`, `takeAgentLock`, `runModules`, `removeBlocks`, `removeFiles`, `removeUnits`, `removeBinary`, `removeData`; tests: `demoModule`, `installHost`, `testHost.uninstallConfig`, `exists`, `runUninstall`, `TestUninstallRemovesEverything`, `TestUninstallAgentRole`, `TestUninstallKeepDataThenReinstall`, `TestUninstallRefusesARunningAgent`, `TestUninstallStopsAtAFailedStepAndResumes`, `TestUninstallWaitsForServicesToStop`, `TestUninstallWithoutAgentDatabase`, `TestDescribeUninstall`.

**Depends on:** Task 1, Task 3, Task 5, Task 8

**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/install/uninstall_test.go`:

```go
package install

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
)

// demoModule owns a file, a block of a shared file, a directory, a unit and
// a user, and has an uninstall step that records what it saw.
type demoModule struct {
	l     Layout
	calls []string
}

func (m *demoModule) paths() (conf, hosts, data, unit string) {
	return filepath.Join(m.l.ConfigDir, "..", "demo.conf"), filepath.Join(m.l.ConfigDir, "..", "hosts"),
		filepath.Join(m.l.DataDir, "..", "demo-data"), m.l.UnitPath("demo.service")
}

func (m *demoModule) Manifest() core.Manifest {
	conf, hosts, data, unit := m.paths()
	return core.Manifest{ID: "demo", Version: 1, OwnedPaths: []core.OwnedPath{
		{Path: filepath.Clean(conf), Kind: core.OwnedFile},
		{Path: filepath.Clean(hosts), Kind: core.OwnedBlock, BlockIDs: []string{"names"}},
		{Path: filepath.Clean(data), Kind: core.OwnedDir},
		{Path: unit, Kind: core.OwnedUnit},
		{Path: "demo", Kind: core.OwnedUser},
	}}
}

func (m *demoModule) Uninstall(_ context.Context, env core.Env, keepData bool) error {
	_, hosts, _, _ := m.paths()
	b, err := os.ReadFile(filepath.Clean(hosts))
	if err != nil {
		return err
	}
	m.calls = append(m.calls, "uninstall keepData="+map[bool]string{true: "true", false: "false"}[keepData]+
		" block present="+map[bool]string{true: "yes", false: "no"}[strings.Contains(string(b), "demo-name")]+
		" env="+map[bool]string{true: "complete", false: "incomplete"}[env.DB != nil && env.Files != nil && env.Sealer != nil])
	return nil
}

var _ core.Uninstaller = (*demoModule)(nil)

// installHost installs role on a fresh test host, puts the binary and its
// link where install.sh would, and lets the demo module write its paths.
func installHost(t *testing.T, role Role) (*testHost, *demoModule) {
	t.Helper()
	h := newTestHost(t)
	ctx := context.Background()
	cfg := h.config(role)
	if role == RoleAgent {
		cfg.Token = "dpe1.t"
	}
	if err := Install(ctx, cfg); err != nil {
		t.Fatalf("Install: %v", err)
	}
	p := h.layout.Paths
	if err := os.WriteFile(p.Binary(), []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p.PrevBinary(), []byte("previous"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(p.Binary(), p.BinaryLink); err != nil {
		t.Fatal(err)
	}
	m := &demoModule{l: h.layout}
	conf, hosts, data, unit := m.paths()
	if err := os.WriteFile(filepath.Clean(hosts), []byte("127.0.0.1 localhost\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	own := platform.NewOwnership(db)
	files := platform.NewFiles(platform.FilesConfig{
		Owned: platform.OwnedPaths([]core.Manifest{m.Manifest()}), Ownership: own, Clock: h.clock, TestMode: true,
	})
	if err := files.WriteFile(ctx, "demo", filepath.Clean(conf), []byte("x=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := files.ReplaceBlock(ctx, "demo", filepath.Clean(hosts), "names", []byte("10.0.0.1 demo-name\n")); err != nil {
		t.Fatal(err)
	}
	for _, e := range []core.OwnedEntry{
		{Path: filepath.Clean(data), Module: "demo", Kind: core.OwnedDir, CreatedAt: installTime},
		{Path: unit, Module: "demo", Kind: core.OwnedUnit, CreatedAt: installTime},
		{Path: "demo", Module: "demo", Kind: core.OwnedUser, CreatedAt: installTime},
	} {
		if err := own.Record(ctx, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.MkdirAll(filepath.Join(filepath.Clean(data), "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(unit, []byte("[Service]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	h.users.users["demo"] = [2]int{991, 991}
	h.systemd.calls = nil
	h.runner.cmds = nil
	return h, m
}

func (h *testHost) uninstallConfig(m *demoModule, keepData bool) UninstallConfig {
	return UninstallConfig{
		Layout: h.layout, KeepData: keepData, Modules: []core.Module{m}, Systemd: h.systemd, Exec: h.runner, Users: h.users,
		Clock: h.clock, Log: slog.New(slog.NewTextHandler(io.Discard, nil)), FilesTestMode: true,
		PollInterval: time.Millisecond, StopTimeout: time.Second,
	}
}

func exists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Lstat(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

func runUninstall(t *testing.T, cfg UninstallConfig) (string, error) {
	t.Helper()
	u, err := PlanUninstall(context.Background(), cfg)
	if err != nil {
		t.Fatalf("PlanUninstall: %v", err)
	}
	var out bytes.Buffer
	err = u.Run(context.Background(), &out)
	return out.String(), err
}

func TestUninstallRemovesEverything(t *testing.T) {
	h, m := installHost(t, RoleController)
	out, err := runUninstall(t, h.uninstallConfig(m, false))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	l, p := h.layout, h.layout.Paths
	conf, hosts, data, unit := m.paths()
	for _, path := range []string{
		l.DataDir, l.ConfigDir, p.BinaryDir, p.BinaryLink, filepath.Clean(conf), filepath.Clean(data), unit,
		filepath.Join(l.UnitDir, DropInDir), l.UnitPath("darkpanel-controller.service"), l.UnitPath("darkpanel-agent.service"),
		l.UnitPath("darkpanel-rollback.service"), l.UnitPath(RecoverUnit),
	} {
		if exists(t, path) {
			t.Errorf("%s is left", path)
		}
	}
	if got := string(readBytes(t, filepath.Clean(hosts))); got != "127.0.0.1 localhost\n" {
		t.Errorf("hosts holds %q, want only the line Darkpanel did not write", got)
	}
	if want := []string{"uninstall keepData=false block present=yes env=complete"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v", m.calls, want)
	}
	if got, want := h.runner.commands(), []string{"/usr/sbin/userdel darkpanel", "/usr/sbin/userdel demo"}; !reflect.DeepEqual(got, want) {
		t.Errorf("commands %v, want %v", got, want)
	}
	want := []string{
		"Stop darkpanel-controller.service", "Stop darkpanel-agent.service",
		"Disable darkpanel-controller.service darkpanel-agent.service darkpanel-update-recover.service", "Reload",
	}
	if got := h.systemd.recorded(); !reflect.DeepEqual(got, want) {
		t.Errorf("systemd calls\n got %v\nwant %v", got, want)
	}
	for _, step := range []string{"services:", "agent lock:", "modules:", "marked blocks:", "files:", "units:", "binary:", "data:"} {
		if !strings.Contains(out, "\n"+step) && !strings.HasPrefix(out, step) {
			t.Errorf("output lacks the step %s:\n%s", step, out)
		}
	}
}

func TestUninstallAgentRole(t *testing.T) {
	h, m := installHost(t, RoleAgent)
	h.systemd.missing["darkpanel-controller.service"] = true
	if out, err := runUninstall(t, h.uninstallConfig(m, false)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if exists(t, h.layout.DataDir) || exists(t, h.layout.Paths.BinaryDir) || exists(t, h.layout.UnitPath("darkpanel-agent.service")) {
		t.Error("the agent role's paths are left")
	}
	if got := h.systemd.recorded(); got[0] != "Stop darkpanel-agent.service" {
		t.Errorf("first call %q, want the agent stopped; the controller unit does not exist", got[0])
	}
	if got := h.runner.commands(); !reflect.DeepEqual(got, []string{"/usr/sbin/userdel demo"}) {
		t.Errorf("commands %v: the agent role has no darkpanel user to remove", got)
	}
}

func TestUninstallKeepDataThenReinstall(t *testing.T) {
	h, m := installHost(t, RoleController)
	p := h.layout.Paths
	key, err := secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		t.Fatal(err)
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := sealer.Seal("test", []byte("kept secret"))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO core_meta (key, value) VALUES ('test_sealed', ?)`, hex.EncodeToString(sealed))
		return err
	})
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatal(err)
	}
	controllerKey := readBytes(t, p.ControllerKey)

	if out, err := runUninstall(t, h.uninstallConfig(m, true)); err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	for _, path := range []string{p.AgentDB(), p.AgentKey, p.ControllerKey, p.ControllerDir, p.UpdateDir, p.AgentTLSCurrent()} {
		if !exists(t, path) {
			t.Errorf("--keep-data removed %s", path)
		}
	}
	for _, path := range []string{p.BinaryDir, p.BinaryLink, h.layout.UnitPath("darkpanel-agent.service")} {
		if exists(t, path) {
			t.Errorf("--keep-data kept %s", path)
		}
	}
	if len(h.runner.commands()) != 0 {
		t.Errorf("--keep-data ran %v; every user is kept", h.runner.commands())
	}
	if want := []string{"uninstall keepData=true block present=yes env=complete"}; !reflect.DeepEqual(m.calls, want) {
		t.Errorf("module steps %v, want %v", m.calls, want)
	}

	if err := Install(ctx, h.config(RoleController)); err != nil {
		t.Fatalf("reinstall: %v", err)
	}
	if !bytes.Equal(readBytes(t, p.ControllerKey), controllerKey) {
		t.Error("the reinstall replaced the controller key")
	}
	key, err = secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		t.Fatal(err)
	}
	if sealer, err = secret.NewSealer(key); err != nil {
		t.Fatal(err)
	}
	// The kept key opens what the kept database holds.
	if db, err = store.Open(ctx, p.AgentDB()); err != nil {
		t.Fatal(err)
	}
	var stored string
	err = db.Read().QueryRowContext(ctx, `SELECT value FROM core_meta WHERE key = 'test_sealed'`).Scan(&stored)
	if cerr := db.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		t.Fatalf("the kept agent.db lost its row: %v", err)
	}
	if sealed, err = hex.DecodeString(stored); err != nil {
		t.Fatal(err)
	}
	if plain, err := sealer.Open("test", sealed); err != nil || string(plain) != "kept secret" {
		t.Errorf("the kept agent key opens %q, %v", plain, err)
	}
	// --keep-data keeps every user, so the module's user stays recorded.
	want := append(wantManifest(h.layout, RoleController), "user demo (module demo)")
	if got := manifest(t, h.layout); !reflect.DeepEqual(got, want) {
		t.Errorf("manifest after the reinstall\n got %v\nwant %v", got, want)
	}
	if len(h.agent.tokens) != 1 {
		t.Errorf("the reinstall enrolled again (%v); the kept credentials were valid", h.agent.tokens)
	}
}

func TestUninstallRefusesARunningAgent(t *testing.T) {
	h, m := installHost(t, RoleController)
	lock := filepath.Join(h.layout.Paths.AgentDir, AgentLockName)
	fd, err := unix.Open(lock, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = unix.Close(fd) }()
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	_, err = runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "an agent still runs") ||
		!strings.Contains(err.Error(), "not run: modules, marked blocks, files, units, binary, data") {
		t.Fatalf("Run = %v, want a refusal naming the steps not run", err)
	}
	if !exists(t, h.layout.Paths.Binary()) || len(m.calls) != 0 {
		t.Error("a refused uninstall removed something")
	}
}

func TestUninstallStopsAtAFailedStepAndResumes(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.failOn("Reload", errors.New("bus gone"))
	_, err := runUninstall(t, h.uninstallConfig(m, false))
	if err == nil || !strings.Contains(err.Error(), "units: bus gone; not run: binary, data") {
		t.Fatalf("Run = %v", err)
	}
	if !exists(t, h.layout.Paths.Binary()) || !exists(t, h.layout.Paths.AgentDB()) {
		t.Fatal("the steps after the failure ran")
	}
	h.systemd.failOn("Reload", nil)
	if out, err := runUninstall(t, h.uninstallConfig(m, false)); err != nil {
		t.Fatalf("second Run: %v\n%s", err, out)
	}
	if exists(t, h.layout.DataDir) || exists(t, h.layout.Paths.BinaryDir) {
		t.Error("the second run left data or the binary")
	}
}

func TestUninstallWaitsForServicesToStop(t *testing.T) {
	h, m := installHost(t, RoleController)
	h.systemd.stuck["darkpanel-agent.service"] = true
	cfg := h.uninstallConfig(m, false)
	cfg.StopTimeout = 20 * time.Millisecond
	_, err := runUninstall(t, cfg)
	if err == nil || !strings.Contains(err.Error(), "darkpanel-agent.service is still active") {
		t.Fatalf("Run = %v, want the stop wait to fail", err)
	}
	if !exists(t, h.layout.Paths.AgentDB()) {
		t.Error("the run went on after a service did not stop")
	}
}

func TestUninstallWithoutAgentDatabase(t *testing.T) {
	h := newTestHost(t)
	l := h.layout
	unit := l.UnitPath("darkpanel-agent.service")
	for _, path := range []string{l.Paths.Binary(), unit} {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runUninstall(t, h.uninstallConfig(&demoModule{l: l}, false))
	if err != nil {
		t.Fatalf("Run: %v\n%s", err, out)
	}
	if exists(t, l.Paths.BinaryDir) || exists(t, unit) {
		t.Error("the binary or the unit is left")
	}
	if !strings.Contains(out, "modules: no agent database") {
		t.Errorf("output %s does not say the module steps were skipped", out)
	}
}

func TestDescribeUninstall(t *testing.T) {
	h, m := installHost(t, RoleController)
	u, err := PlanUninstall(context.Background(), h.uninstallConfig(m, true))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = u.Close() }()
	var out bytes.Buffer
	u.Describe(&out)
	conf, hosts, _, _ := m.paths()
	for _, want := range []string{
		"1. services", "8. data", "keep " + h.layout.DataDir, "file " + filepath.Clean(conf) + ", of module demo",
		"block " + filepath.Clean(hosts) + " (block names), of module demo", "unit " + h.layout.DropInPath() + ", of module core",
	} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("plan lacks %q:\n%s", want, out.String())
		}
	}
	if strings.Contains(out.String(), "file "+h.layout.Paths.AgentKey) {
		t.Errorf("the --keep-data plan removes the agent key:\n%s", out.String())
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/plan3-tmp/9 && TMPDIR=/root/.cache/plan3-tmp/9 GOTOOLCHAIN=local go test -count=1 ./internal/install/`
Expected: FAIL, the build stops with `undefined: UninstallConfig` (and the other names of `uninstall.go`).

- [ ] **Step 3: Write the implementation**

Create `internal/install/uninstall.go`:

```go
package install

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/update"
)

// StopTimeout bounds the wait for a stopped service to become inactive; the
// units stop within their TimeoutStopSec of 10 s.
const StopTimeout = 30 * time.Second

// UninstallConfig is what darkpanel uninstall needs.
type UninstallConfig struct {
	Layout Layout
	// KeepData keeps the data and configuration directories, with the
	// databases, backups, update records, role keys and agent credentials,
	// and every user, so that a later install finds them.
	KeepData bool
	// Modules are the compiled-in modules; each that implements
	// core.Uninstaller runs its step.
	Modules []core.Module
	Systemd platform.Systemd
	Exec    core.Runner
	Users   Users
	Clock   core.Clock
	Log     *slog.Logger
	// FilesTestMode is platform.FilesConfig.TestMode: false in production.
	FilesTestMode bool
	// PollInterval and StopTimeout pace the wait for stopped services; zero
	// means 1 s and StopTimeout.
	PollInterval time.Duration
	StopTimeout  time.Duration
}

func (c UninstallConfig) check() error {
	required := []struct {
		name string
		set  bool
	}{
		{"Systemd", c.Systemd != nil}, {"Exec", c.Exec != nil}, {"Users", c.Users != nil},
		{"Clock", c.Clock != nil}, {"Log", c.Log != nil},
	}
	for _, r := range required {
		if !r.set {
			return fmt.Errorf("install: UninstallConfig.%s is required", r.name)
		}
	}
	return nil
}

// Uninstall is a planned uninstall: the ownership manifest, read into memory
// before anything is removed, and the steps that remove it.
type Uninstall struct {
	cfg     UninstallConfig
	db      *store.DB      // nil when agent.db does not exist
	own     core.Ownership // nil without db
	files   *platform.Files
	sealer  core.Sealer
	entries []core.OwnedEntry
	steps   []uninstallStep
	lockFD  int // the agent lock, -1 until taken
}

type uninstallStep struct {
	name string
	plan []string // what the step does, for Describe
	run  func(ctx context.Context) (string, error)
}

// PlanUninstall reads the ownership manifest of agent.db, adds the core's
// paths in case the manifest lacks them, and plans the steps. Nothing on the
// host changes until Run. Call Run or Close.
func PlanUninstall(ctx context.Context, cfg UninstallConfig) (*Uninstall, error) {
	if err := cfg.check(); err != nil {
		return nil, err
	}
	if cfg.PollInterval == 0 {
		cfg.PollInterval = pollInterval
	}
	if cfg.StopTimeout == 0 {
		cfg.StopTimeout = StopTimeout
	}
	u := &Uninstall{cfg: cfg, lockFD: -1}
	if err := u.load(ctx); err != nil {
		_ = u.Close()
		return nil, err
	}
	u.steps = u.plan()
	return u, nil
}

// load opens agent.db and the agent key when they exist and collects the
// entries to remove.
func (u *Uninstall) load(ctx context.Context) error {
	p := u.cfg.Layout.Paths
	if _, err := os.Lstat(p.AgentDB()); err == nil {
		if u.db, err = store.Open(ctx, p.AgentDB()); err != nil {
			return err
		}
		u.own = platform.NewOwnership(u.db)
		if u.entries, err = u.own.List(ctx); err != nil {
			return err
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("install: %w", err)
	}
	for _, op := range OwnedPaths(u.cfg.Layout, RoleController) {
		e := core.OwnedEntry{Path: op.Path, Module: core.ModuleIDCore, Kind: op.Kind}
		if !slices.ContainsFunc(u.entries, func(x core.OwnedEntry) bool { return x.Path == e.Path && x.Module == e.Module && x.BlockID == "" }) {
			u.entries = append(u.entries, e)
		}
	}
	if _, err := os.Lstat(p.AgentKey); err == nil {
		key, err := secret.LoadOrCreateKey(p.AgentKey)
		if err != nil {
			return err
		}
		s, err := secret.NewSealer(key)
		if err != nil {
			return err
		}
		u.sealer = s
	}
	if u.db != nil {
		u.files = platform.NewFiles(platform.FilesConfig{
			Owned: u.owned(), Ownership: u.own, Clock: u.cfg.Clock, TestMode: u.cfg.FilesTestMode,
		})
	}
	return nil
}

// owned is every module's declaration plus every file and block of the
// manifest, so an entry whose module no longer declares it is still removed.
func (u *Uninstall) owned() map[string][]core.OwnedPath {
	var mans []core.Manifest
	for _, m := range u.cfg.Modules {
		mans = append(mans, m.Manifest())
	}
	owned := platform.OwnedPaths(mans)
	for _, e := range u.entries {
		ops := owned[e.Module]
		i := slices.IndexFunc(ops, func(op core.OwnedPath) bool { return op.Path == e.Path && op.Kind == e.Kind })
		switch {
		case e.Kind != core.OwnedFile && e.Kind != core.OwnedBlock:
		case i < 0:
			op := core.OwnedPath{Path: e.Path, Kind: e.Kind}
			if e.BlockID != "" {
				op.BlockIDs = []string{e.BlockID}
			}
			owned[e.Module] = append(ops, op)
		case e.BlockID != "" && !slices.Contains(ops[i].BlockIDs, e.BlockID):
			ops[i].BlockIDs = append(slices.Clone(ops[i].BlockIDs), e.BlockID)
		}
	}
	return owned
}

// within reports whether path is dir or lies under it.
func within(path, dir string) bool { return path == dir || strings.HasPrefix(path, dir+"/") }

// kept reports whether --keep-data keeps e.
func (u *Uninstall) kept(e core.OwnedEntry) bool {
	l := u.cfg.Layout
	return u.cfg.KeepData && e.Module == core.ModuleIDCore && (within(e.Path, l.DataDir) || within(e.Path, l.ConfigDir))
}

// selectEntries returns the entries for which keep is true.
func (u *Uninstall) selectEntries(keep func(e core.OwnedEntry) bool) []core.OwnedEntry {
	var out []core.OwnedEntry
	for _, e := range u.entries {
		if keep(e) {
			out = append(out, e)
		}
	}
	return out
}

func describe(entries []core.OwnedEntry) []string {
	var out []string
	for _, e := range entries {
		line := e.Kind + " " + e.Path
		if e.BlockID != "" {
			line += " (block " + e.BlockID + ")"
		}
		out = append(out, line+", of module "+e.Module)
	}
	return out
}

func (u *Uninstall) plan() []uninstallStep {
	l := u.cfg.Layout
	isCore := func(e core.OwnedEntry) bool { return e.Module == core.ModuleIDCore }
	blocks := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedBlock })
	files := u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Kind == core.OwnedFile && (!isCore(e) || e.Path != l.Paths.BinaryLink) && !u.kept(e)
	})
	dirs := u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Kind == core.OwnedDir && !isCore(e) && !within(e.Path, l.UnitDir)
	})
	units := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedUnit })
	unitDirs := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedDir && within(e.Path, l.UnitDir) })
	users := u.selectEntries(func(e core.OwnedEntry) bool { return e.Kind == core.OwnedUser })
	services := []string{update.ControllerUnit, update.AgentUnit, RecoverUnit}

	steps := []uninstallStep{
		{name: "services", plan: []string{"stop and disable " + strings.Join(services, ", ")},
			run: func(ctx context.Context) (string, error) { return u.stopServices(ctx, services) }},
		{name: "agent lock", plan: []string{"make sure no agent runs (" + filepath.Join(l.Paths.AgentDir, AgentLockName) + ")"},
			run: u.takeAgentLock},
		{name: "modules", plan: []string{"run each module's uninstall step"}, run: u.runModules},
		{name: "marked blocks", plan: describe(blocks), run: func(ctx context.Context) (string, error) { return u.removeBlocks(ctx, blocks) }},
		{name: "files", plan: describe(append(slices.Clone(files), dirs...)),
			run: func(ctx context.Context) (string, error) { return u.removeFiles(ctx, files, dirs) }},
		{name: "units", plan: describe(append(slices.Clone(units), unitDirs...)),
			run: func(ctx context.Context) (string, error) { return u.removeUnits(ctx, units, unitDirs) }},
		{name: "binary", plan: []string{"remove " + l.Paths.BinaryDir + " and " + l.Paths.BinaryLink}, run: u.removeBinary},
	}
	if u.cfg.KeepData {
		steps = append(steps, uninstallStep{name: "data",
			plan: []string{"keep " + l.DataDir + ", " + l.ConfigDir + " and every user"},
			run: func(context.Context) (string, error) {
				return "kept " + l.DataDir + ", " + l.ConfigDir + " and every user", u.closeDB()
			}})
	} else {
		steps = append(steps, uninstallStep{name: "data",
			plan: append([]string{"remove " + l.ConfigDir + " and " + l.DataDir + ", agent.db last"}, describe(users)...),
			run:  func(ctx context.Context) (string, error) { return u.removeData(ctx, users) }})
	}
	return steps
}

// Describe writes the plan, one line per step and one per entry it removes.
func (u *Uninstall) Describe(w io.Writer) {
	_, _ = fmt.Fprintln(w, "darkpanel uninstall will, in this order:")
	for i, s := range u.steps {
		_, _ = fmt.Fprintf(w, "%d. %s\n", i+1, s.name)
		for _, line := range s.plan {
			_, _ = fmt.Fprintf(w, "     %s\n", line)
		}
	}
}

// Run runs the steps in order, printing one line per finished step. A failed
// step stops the run, and the error names the steps that did not run;
// running a new plan later picks up where this one stopped. Run closes the
// plan.
func (u *Uninstall) Run(ctx context.Context, w io.Writer) error {
	defer func() { _ = u.Close() }()
	for i, s := range u.steps {
		line, err := s.run(ctx)
		if err != nil {
			var rest []string
			for _, r := range u.steps[i+1:] {
				rest = append(rest, r.name)
			}
			if len(rest) == 0 {
				return fmt.Errorf("uninstall: %s: %w", s.name, err)
			}
			return fmt.Errorf("uninstall: %s: %w; not run: %s", s.name, err, strings.Join(rest, ", "))
		}
		_, _ = fmt.Fprintf(w, "%s: %s\n", s.name, line)
	}
	return nil
}

// Close releases agent.db and the agent lock; it is safe to call twice.
func (u *Uninstall) Close() error {
	err := u.closeDB()
	if u.lockFD >= 0 {
		if cerr := unix.Close(u.lockFD); err == nil {
			err = cerr
		}
		u.lockFD = -1
	}
	return err
}

func (u *Uninstall) closeDB() error {
	if u.db == nil {
		return nil
	}
	err := u.db.Close()
	u.db, u.own = nil, nil
	return err
}

// forget drops e from the manifest, which --keep-data keeps.
func (u *Uninstall) forget(ctx context.Context, e core.OwnedEntry) error {
	if u.own == nil {
		return nil
	}
	return u.own.Forget(ctx, e.Module, e.Path, e.BlockID)
}

// stopServices stops the services that exist, waits until they are down,
// and disables them with the boot recovery unit.
func (u *Uninstall) stopServices(ctx context.Context, units []string) (string, error) {
	var present []string
	for _, unit := range units {
		st, err := u.cfg.Systemd.State(ctx, unit)
		if err != nil {
			return "", err
		}
		if st.LoadState == "not-found" {
			continue
		}
		present = append(present, unit)
		if unit == RecoverUnit {
			continue
		}
		if err := u.cfg.Systemd.Stop(ctx, unit); err != nil {
			return "", err
		}
		if err := u.waitStopped(ctx, unit); err != nil {
			return "", err
		}
	}
	if len(present) == 0 {
		return "no Darkpanel service is installed", nil
	}
	if err := u.cfg.Systemd.Disable(ctx, present...); err != nil {
		return "", err
	}
	return "stopped and disabled " + strings.Join(present, ", "), nil
}

func (u *Uninstall) waitStopped(ctx context.Context, unit string) error {
	wait, cancel := context.WithTimeout(ctx, u.cfg.StopTimeout)
	defer cancel()
	for {
		st, err := u.cfg.Systemd.State(wait, unit)
		if err != nil {
			return err
		}
		if st.ActiveState == "inactive" || st.ActiveState == "failed" {
			return nil
		}
		select {
		case <-wait.Done():
			return fmt.Errorf("%s is still %s after %s", unit, st.ActiveState, u.cfg.StopTimeout)
		case <-time.After(u.cfg.PollInterval):
		}
	}
}

// takeAgentLock holds the lock a running agent holds, so nothing below
// races an agent that was started by hand.
func (u *Uninstall) takeAgentLock(context.Context) (string, error) {
	dir := u.cfg.Layout.Paths.AgentDir
	if _, err := os.Lstat(dir); errors.Is(err, fs.ErrNotExist) {
		return "no agent directory", nil
	}
	path := filepath.Join(dir, AgentLockName)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return "", fmt.Errorf("open %s: %w", path, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = unix.Close(fd)
		if errors.Is(err, unix.EWOULDBLOCK) {
			return "", fmt.Errorf("an agent still runs and holds %s; stop it first", path)
		}
		return "", fmt.Errorf("lock %s: %w", path, err)
	}
	u.lockFD = fd
	return "no agent runs", nil
}

// runModules runs each module's uninstall step before its blocks and files
// go, so a module can still read them.
func (u *Uninstall) runModules(ctx context.Context) (string, error) {
	if u.db == nil {
		return "no agent database; no module installed anything", nil
	}
	env := core.Env{DB: u.db, Exec: u.cfg.Exec, Files: u.files, Sealer: u.sealer, Clock: u.cfg.Clock, Log: u.cfg.Log}
	var ran []string
	for _, m := range u.cfg.Modules {
		un, ok := m.(core.Uninstaller)
		if !ok {
			continue
		}
		id := m.Manifest().ID
		if err := un.Uninstall(ctx, env, u.cfg.KeepData); err != nil {
			return "", fmt.Errorf("module %s: %w", id, err)
		}
		ran = append(ran, id)
	}
	if len(ran) == 0 {
		return "no module has an uninstall step", nil
	}
	return "ran the uninstall steps of " + strings.Join(ran, ", "), nil
}

func (u *Uninstall) removeBlocks(ctx context.Context, blocks []core.OwnedEntry) (string, error) {
	for _, e := range blocks {
		if err := u.files.RemoveBlock(ctx, e.Module, e.Path, e.BlockID); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("removed %d marked blocks", len(blocks)), nil
}

func (u *Uninstall) removeFiles(ctx context.Context, files, dirs []core.OwnedEntry) (string, error) {
	for _, e := range files {
		var err error
		if u.files != nil {
			err = u.files.Remove(ctx, e.Module, e.Path)
		} else {
			err = removeTree(e.Path)
		}
		if err != nil {
			return "", err
		}
	}
	for _, e := range dirs {
		if err := removeTree(e.Path); err != nil {
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	return fmt.Sprintf("removed %d files and %d directories", len(files), len(dirs)), nil
}

// removeUnits removes the unit files, then their drop-in directories when
// nothing else is left in them, and reloads systemd.
func (u *Uninstall) removeUnits(ctx context.Context, units, dirs []core.OwnedEntry) (string, error) {
	for _, e := range units {
		if err := os.Remove(e.Path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	var left []string
	for _, e := range dirs {
		err := os.Remove(e.Path)
		switch {
		case errors.Is(err, syscall.ENOTEMPTY) || errors.Is(err, syscall.EEXIST):
			left = append(left, e.Path)
		case err != nil && !errors.Is(err, fs.ErrNotExist):
			return "", err
		}
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	if err := u.cfg.Systemd.Reload(ctx); err != nil {
		return "", err
	}
	line := fmt.Sprintf("removed %d unit files and reloaded systemd", len(units))
	if len(left) > 0 {
		line += "; kept " + strings.Join(left, ", ") + ", which holds files Darkpanel did not write"
	}
	return line, nil
}

func (u *Uninstall) removeBinary(ctx context.Context) (string, error) {
	p := u.cfg.Layout.Paths
	for _, path := range []string{p.BinaryLink, p.BinaryDir} {
		if err := removeTree(path); err != nil {
			return "", err
		}
	}
	for _, e := range u.selectEntries(func(e core.OwnedEntry) bool {
		return e.Module == core.ModuleIDCore && (e.Path == p.BinaryLink || e.Path == p.BinaryDir)
	}) {
		if err := u.forget(ctx, e); err != nil {
			return "", err
		}
	}
	return "removed " + p.BinaryDir + " and " + p.BinaryLink, nil
}

// removeData removes the configuration directory, the users and the data
// directory. agent.db, which holds the manifest, goes last, so a run that
// stops early can be planned again.
func (u *Uninstall) removeData(ctx context.Context, users []core.OwnedEntry) (string, error) {
	l, p := u.cfg.Layout, u.cfg.Layout.Paths
	if err := removeTree(l.ConfigDir); err != nil {
		return "", err
	}
	var removed []string
	for _, e := range users {
		_, _, found, err := u.cfg.Users.Lookup(e.Path)
		if err != nil {
			return "", err
		}
		if !found {
			continue
		}
		res, err := u.cfg.Exec.Run(ctx, core.Cmd{Path: "/usr/sbin/userdel", Args: []string{e.Path}})
		if err != nil {
			return "", err
		}
		if res.ExitCode != 0 {
			return "", fmt.Errorf("userdel %s exited %d: %s", e.Path, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
		removed = append(removed, e.Path)
	}
	keep := map[string]bool{p.AgentDir: true, filepath.Join(p.AgentDir, AgentLockName): true}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		keep[p.AgentDB()+suffix] = true
	}
	if err := clearExcept(l.DataDir, keep); err != nil {
		return "", err
	}
	if err := u.Close(); err != nil {
		return "", err
	}
	if err := removeTree(l.DataDir); err != nil {
		return "", err
	}
	line := "removed " + l.ConfigDir + " and " + l.DataDir
	if len(removed) > 0 {
		line += " and the users " + strings.Join(removed, ", ")
	}
	return line, nil
}

// clearExcept removes everything under dir except the paths in keep, going
// into a kept directory to clear the rest of it.
func clearExcept(dir string, keep map[string]bool) error {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		path := filepath.Join(dir, e.Name())
		switch {
		case keep[path] && e.IsDir():
			if err := clearExcept(path, keep); err != nil {
				return err
			}
		case keep[path]:
		default:
			if err := removeTree(path); err != nil {
				return err
			}
		}
	}
	return nil
}

// removeTree removes path and everything under it; a symlink is removed
// itself, never followed, and a missing path is not an error.
func removeTree(path string) error {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return fmt.Errorf("refusing to remove %q", path)
	}
	st, err := os.Lstat(path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return nil
	case err != nil:
		return err
	case st.IsDir():
		return os.RemoveAll(path)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}
```

- [ ] **Step 4: Run the tests and the checks**

Run:
```bash
export TMPDIR=/root/.cache/plan3-tmp/9 GOTOOLCHAIN=local
go test -count=1 -race -timeout 300s ./internal/install/
go vet ./internal/install/
golangci-lint run --allow-serial-runners ./internal/install/...
go mod tidy -diff
```
Expected: `ok  	github.com/darkraise/darkpanel/internal/install`; vet and `go mod tidy -diff` print nothing; golangci-lint prints `0 issues.`

- [ ] **Step 5: Commit**

```bash
git add internal/install/uninstall.go internal/install/uninstall_test.go
git commit -m "feat(install): add uninstall"
```

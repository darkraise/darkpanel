# M2 Assembly and Harness Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dr-superpowers:subagent-driven-development for `subagent`, dr-superpowers:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build M2 plan 6, parts P12 (assembly) and P13 (harness): `darkpanel serve` as the controller alone with the agent listener, session registry, rollout driver and scheduler; `darkpanel agent`, `system install`, `uninstall`, `selftest` and the `update` commands; the CLI's `--local` agent-socket path; and the M2 harness scenarios on three VMs per OS. It also closes the open review rows the earlier M2 plans handed to the assembly.

**Architecture:** Tasks 1-9 settle what the assembly consumes: the core update and rollout actions in the manifest, the lock-key rule, the `unavailable` code, serialised credential saves, the configurable health status, `--local`, the release-workflow hardening and the harness's private link and signed test releases. Tasks 10-12 rewire `internal/controller/server` so `serve` runs the controller alone (agent listener, session registry, rollout driver, scheduler) and prove it end to end over the real agent transport; Tasks 13-18 add the `cmd/darkpanel` commands and the build-tagged harness variants. Tasks 19-22 provision the three-VM topology and run the M2 scenarios in `harness.yml`; Tasks 23-24 update the user docs, the spec and ADR 0012.

**Tech Stack:** Go 1.27 (`GOTOOLCHAIN=local`), `github.com/spf13/cobra`, `net/http`, `crypto/tls` (TLS 1.3 only) through `internal/transport`, `modernc.org/sqlite` through `internal/core/store`, `golang.org/x/sys/unix` (flock), `github.com/godbus/dbus/v5` through `internal/platform`, `aead.dev/minisign` v0.3.0, `golang.org/x/crypto/ssh` (harness), QEMU/KVM and cloud-init (harness, GitHub Actions only), GNU make, POSIX shell, git-cliff 2.14.2, GitHub Actions.

**Spec:** docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves; each task's **Implementer:** line names the subagent that implements it

**Plan review:** 2026-10-20 — dr-superpowers:judge-opus — executability 17 / coherence 17 / coverage 17 / assumptions 13 (round 1)

**Parallelism:** waves — 1-3 | 4 | 5-6 | 7 | 8-9 | 10 | 11 | 12-13 | 14 | 15 | 16 | 17 | 18 | 19 | 20 | 21 | 22-24

**Integration check:** `mkdir -p /root/.cache/plan6-tmp/integration && export GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/integration && timeout 600 go build ./... && timeout 1500 go test -count=1 ./... && timeout 900 go test -tags harness -race -count=1 ./test/harness/... && timeout 600 go vet ./... && timeout 600 go vet -tags harness ./test/... && timeout 900 golangci-lint run --allow-serial-runners && go mod tidy -diff`

**Worktree setup:** none

## Global Constraints

- Build only with the toolchain `go.mod` pins: every `go` command runs with `GOTOOLCHAIN=local` (ADR 0001).
- `/tmp` on this host is a small tmpfs that parallel Go builds fill: every `go` command, `golangci-lint` and `make` run with `TMPDIR=/root/.cache/plan6-tmp/<task number>` (create it with `mkdir -p`, empty it when the task ends), also where a step's command omits it.
- `/nonexistent` exists on this host. A test that needs a missing path uses `filepath.Join(t.TempDir(), "missing")`.
- Dependencies: no task edits `go.mod` or `go.sum`; every module the plan imports is already required. After every task `GOTOOLCHAIN=local go mod tidy -diff` prints nothing.
- Files: a task touches only the files its **Files:** block names.
- JSON fields are camelCase. New tables are `STRICT` with fixed-width RFC 3339 UTC text times (layout `2006-01-02T15:04:05.000000000Z`).
- Comments follow the repository style: full sentences that explain why, never what. New comments name no plan, task, register row or review.
- Error strings start with the package prefix (`server:`, `cli:`, `daemon:`, `fleet:`, `registry:`, `release:`, `vm:`; `cmd/darkpanel` errors need none because `cli.PrintError` prefixes `darkpanel:`); a typed error built with `errcode.New` or `errcode.Wrap` carries its code instead.
- Security defaults are never configurable: TLS 1.3 only on the agent transport, no system roots, Ed25519 agent keys only; secrets are never flags or arguments (ADR 0006): tokens come from a file, a hidden prompt or standard input.
- Unit tests touch no real systemd, D-Bus, `/etc`, `/var/lib`, `/run`, `/usr/local` or a network beyond `127.0.0.1`: systemd is a fake implementing `platform.Systemd`, listeners use `127.0.0.1:0` (the agent listener's fixed `127.0.0.1:7443` endpoint is rebound to port 0 through `server.Options.AgentBind`), every path comes from `paths.Under(t.TempDir())`, `install.Rooted(t.TempDir())` or `t.TempDir()`, Unix sockets live under `os.MkdirTemp("", "dp")` (socket paths are limited to 108 bytes), and time comes from `coretest.NewFakeClock` wherever a duration above one second is waited on. Tasks of one wave therefore share no port, path or global state.
- Tests run as root on this host (`id -u` = 0). A test that depends on running as root or not checks `os.Geteuid()` and skips the other branch with a reason.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task of the plan declares in that package; each task's **Interfaces:** block lists the names it adds.
- Lint: `golangci-lint run --allow-serial-runners` and `GOTOOLCHAIN=local go vet ./...` stay clean; harness code is also vetted with `go vet -tags harness ./test/...` and linted with `golangci-lint run --allow-serial-runners --build-tags harness ./test/...`.
- The waves merge into the plan's work branch, `worktree-m2-assembly-and-harness` (the name dr-superpowers:using-git-worktrees gives it, as `worktree-m2-services` was for the previous plan); each task of a wave runs in its own worktree from the wave's base on that branch.
- Gate 7 (Harness green, `docs/superpowers/gates.md`, which Task 22 rewrites) runs once, after the last wave merges and before the final whole-branch review, through dr-superpowers:running-gates. It needs the owner's go-ahead to push: never push `main` for it. After the go-ahead: `git push -u origin worktree-m2-assembly-and-harness`, then `timeout 60 gh workflow run harness.yml --ref worktree-m2-assembly-and-harness`, then `timeout 9000 gh run watch "$(timeout 60 gh run list --workflow harness.yml --branch worktree-m2-assembly-and-harness --limit 1 --json databaseId --jq '.[0].databaseId')" --exit-status`. Green is the gate's command printing `success`: both vm jobs (Ubuntu 22.04 and Debian 13) passed TestM1 and every TestM2 step. A red run stops execution: download the logs (`gh run download <id> --name` for the job's log artifact), debug it with dr-superpowers:systematic-debugging, fix it in the task that owns the failing code, and run the gate again. After the green run, replace the budget inferences in gate 7's Why line with the measured vm-job and TestM2 durations of both jobs, in one `docs(gates): record the measured harness budget` commit on the work branch.
- The harness scenarios (Tasks 20-22) need KVM, which this host lacks: locally the harness code of Tasks 19-22 is only compiled (`go vet -tags harness ./test/...`) and its unit tests run (`go test -tags harness -race -count=1 ./test/harness/...`); the scenarios themselves run only in `.github/workflows/harness.yml`, as gate 7 (see the next constraint).
- Commits use `<type>(<scope>): <subject>`, subject at most 50 characters, imperative, no period, and carry no attribution lines.

## Contracts

(Earlier M2 plans' contracts are cited as F-C<n> foundation, S-C<n> security libraries, A-C<n> agent API and install, P8-C<n>..P11-C<n> services.)

**C1. Core manifest (Task 1).** `internal/registry`: `func CoreActions(u *update.Updater, controllerVersion string) []core.ActionDef` returns `append(update.Actions(u), rollout.Actions(rollout.ActionsConfig{Version: controllerVersion})...)`. `Build()` builds the core module with `coreactions.Deps{Extra: CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}`. `BuildWith` is unchanged. The snapshots `schemas/core.update.{apply,rollback,status,rollout,rollout-cancel,rollouts}/v1.json` are generated with `make schemas` and committed. `internal/deps/deps.go` is deleted. The server's core module (`internal/controller/server/services.go`) gets the same `Extra` (with `buildinfo.Version`) and `OwnedPaths` in this task, because `TestServerRegistryMatchesBuild` compares it with `Build()`'s.

**C2. Revertible agent actions take a lock (Task 2).** `core.NewRegistry` refuses an agent-side revertible action whose `LockKeys` is empty: `action <ref>: a revertible agent action declares at least one lock key, so an update's reservation holds it off`.

**C3. The unavailable code (Task 3).** `errcode.Unavailable Code = "unavailable"`, HTTP 503, title `Unavailable`. The fleet renewal endpoint answers it after `Server.Close` (was `host.offline`). `coreactions.ProtocolVersion` and its test are removed; `coreactions.Supported` compares with `agentruntime.ProtocolVersion`.

**C4. Serialised credential saves (Task 4).** `daemon.SaveGeneration(dir string, c *Credentials, expect int) (int, error)`: it holds an exclusive `flock` on `dir/save.lock` (mode 0600) from reading `current` to the fsync of `dir`; `expect` is `daemon.AnyCurrent` (-1) for an enrollment, or the generation that must still be current (the renewal's session generation), else it writes nothing and fails with `daemon.ErrSuperseded`. `Enroll` passes `AnyCurrent`; the renewal passes its session's generation and treats `ErrSuperseded` as "the session ends, the daemon picks up the newer generation".

**C5. Health status (Task 5).** `api.Config.HealthStatus string`: the `status` that `GET /healthz` reports, in both the bare and the detailed answer, always with HTTP 200; empty means `ok`. `server.Options.HealthStatus` passes it through (Task 10).

**C6. --local (Task 6).** `cli.Options.AgentSocket string`: the agent socket `--local` dials; empty means `paths.Production().AgentSocket`. The root command has a persistent `--local` flag. A generated agent-side action or resource command run with `--local` sends its read, plan and apply, or resource read, to the agent socket through `api.NewClient(api.ClientConfig{Dial: <unix dial of AgentSocket>})` instead of the controller API, and refuses `--host` and `--url` (`--host does not apply with --local: the agent socket serves this host only`, the same for `--url`). It dials the socket once before reading or planning, so a fallback never runs after a plan was shown; errors coded by the agent print and exit as on the API path; each socket call times out after 90 s; `local` is a reserved flag name no input field may take. Every other command run with `--local` fails with a usage error: `--local reaches only this host's agent; <command path> is not an agent-side command`. When the socket does not exist or refuses the connection and the action has an entry in `Options.LocalFallbacks`, the command prints `darkpanel: the agent socket <path> does not answer; running <action> in this process` to standard error, asks `Apply? [y/N]` for a mutating action unless `--yes` (a non-terminal needs `--yes`; a read asks nothing), runs the fallback with the collected input and shows its output as a read does. The same task adds `func cli.ExitWith(code int, err error) error`: `cli.ExitCode` returns `code` for it (also when wrapped), and `cli.PrintError` prints `err` (nothing when `err` is nil); `darkpanel agent check` exits with its `install.CheckResult` through it.

**C7. Release hardening (Task 7).** `releasetool check-build <version> <key id>[,<key id>] < version.json` now takes the release version first and refuses a `version.json` whose `version` differs from it, whose `testBuild` is missing or true, or whose `trustedKeys` differ from the signing key ids (16 upper-case hex digits each); `release/sign.sh` is its only caller. Task 13's `version --json` fields (C13) are what it reads. `releasetool latest <version>` is new and used only by `release.yml`.

**C8. VM private link (Task 8).** `vm.Config` gains `PrivMAC string`, `PrivAddrs []string` and `PrivMcastPort int`; a non-zero `PrivMcastPort` adds a second NIC on multicast group `vm.McastGroup:<PrivMcastPort>`, named `dppriv` in the guest (beside `dpmgmt` and `dpinet`), with `PrivAddrs`. `func (v *VM) DialContext(ctx context.Context, network, addr string) (net.Conn, error)` dials `addr` from inside the guest through the SSH connection (direct-tcpip).

**C9. Harness releases (Task 9).** Package `release` at `test/harness/release` (build tag `harness`): `type Version struct{ Version, Channel, Binary string }` (`Binary` is a linux/amd64 `darkpanel` built for `Version`); `func LoadKey(path string) (minisign.PrivateKey, error)` reads the unencrypted harness key `internal/update/trustedkeys/testdata/harness.key`; `func Publish(dir string, key minisign.PrivateKey, versions []Version) error` writes `dir/index/releases.json` (`{"formatVersion":1,"releases":[{"version","channel"}...]}`) and, per version, `dir/v<version>/darkpanel_<version>_linux_amd64.tar.gz` (entries `darkpanel`, `LICENSE`, `README.md` at the archive root, as `release/package.sh` writes them), `checksums.txt` and `checksums.txt.<key id>.minisig` signed in hashed mode with trusted comment `version=<version> channel=<channel> purpose=checksums`.

**C10. Controller-only serve (Task 10).** `server.Options` gains `EmbeddedAgent bool` (the M1 in-process agent over `dispatch.StaticClients`; only `--dev` may set it), `AgentListen []netip.AddrPort` (from `listen.ParseAgentListen`), `AgentAdvertise string`, `AgentBind listen.BindFunc` (nil means `listen.Bind`) and `HealthStatus string`. Without `EmbeddedAgent`, `Start` opens no agent.db and no agent key, creates only the controller's directories (never `AgentDir`, which would then belong to the unprivileged controller user) and takes the single-instance lock only in the socket directory and `ControllerDir`; the agent authority, `AgentAdvertise` and `Version` are wired in both modes, and only the client source differs; it wires `fleet.LoadOrCreateAuthority`, `fleet.NewRegistry`, `fleet.NewServer` and `listen.StartAgent` as P8's "What P12 wires" says, the registry as the `dispatch.ClientSource` and as `coreactions.Deps.Sessions`, the authority as `Deps.Authority`, `Deps.AgentAdvertise`, `Deps.Version = buildinfo.Version` (`Deps.Extra` and `Deps.OwnedPaths` are already set by Task 1), and `dispatch.Config.Version = buildinfo.Version`. `func (s *Server) AgentListeners() []listen.Bound` returns the agent listener's bound sockets (none with `EmbeddedAgent`). Shutdown stops the agent listener, then the fleet server, then the session registry, before the dispatcher's dependencies and the database close.

**C11. Rollouts in serve (Task 11).** `server.Options.ReleaseIndex rollout.ReleaseSource`; nil means `update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, clock)`. `Start` creates `rollout.NewDriver` and `rollout.NewScheduler` (Clients: the session registry, or the static clients with `EmbeddedAgent`) after the dispatcher and starts both; Shutdown stops the scheduler, then the driver, before the dispatcher's dependencies close. The update actions registered without an updater (`update.Actions(nil)`, as in the controller process and the embedded agent) now answer `errcode.Unavailable` with `the update actions run only in darkpanel agent run`, so the driver fails such a host at once instead of retrying it; Task 12's end-to-end test asserts that 503.

**C12. Fleet end-to-end test (Task 12).** No cross-task names. `make verify-build` also runs `TestFleetEndToEnd` (its `-run` pattern is `^(TestEndToEnd|TestFleetEndToEnd)$`).

**C13. Version and selftest (Task 13).** `darkpanel version --json` adds `"testBuild"` (`trustedkeys.TestBuild()`) and `"trustedKeys"` (the `ID` of every `trustedkeys.Keys()` entry, in order; `[]` when none). `darkpanel selftest` builds `registry.Build()`, loads `store.CoreMigrations` for both sides, reads one file from `schemas.FS`, and requires at least one trusted key; it prints `selftest: ok` and exits 0, else exits 1 naming the failed check.

**C14. serve flags (Task 14).** `serve --agent-listen <ip:port>` (repeatable, `listen.ParseAgentListen`), `--agent-advertise <host:port>` (`install.CheckAgentAddress`), `--embedded-agent` (refused without `--dev`: `--embedded-agent runs the M1 in-process agent and needs --dev`). Production `serve` (no `--dev`) refuses euid 0: `serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev to use per-user development paths`. `--agent-listen` with `--embedded-agent` is refused too (`--agent-listen does not apply with --embedded-agent, which opens no agent listener`); `--agent-advertise` stays allowed with it, because C10 wires `AgentAdvertise` in both modes. All these refusals exit 2.

**C15. agent commands (Task 15).** `darkpanel agent run [--dev]`, `agent enroll [--token-file <path>] [--dev]`, `agent check [--dev]` (exit status `int(install.CheckResult)`). Production `agent run` and `agent enroll` refuse a non-zero euid: `agent <cmd> runs as root in production mode; run it with sudo, or add --dev`. Shared helpers in `cmd/darkpanel/agent.go`: `func openAgent(ctx context.Context, p paths.Paths, log *slog.Logger) (*agentHost, error)` with `type agentHost struct{ DB *store.DB; Sealer *secret.Sealer; Updater *update.Updater; Registry *core.Registry; Close func() error }` — the agent.db, the agent key's sealer, the updater over the D-Bus systemd provider (`platform.NewSystemd`) and the registry built with `registry.CoreActions(updater, "")`; Tasks 16 and 17 reuse it. The updater's probes: `Controller: update.ControllerProbe(p.ControllerSocket)` when the controller unit file exists (`install.Production().UnitPath(update.ControllerUnit)` in production, never under `--dev`), and `Agent:` the `Hello` of `api.NewClient` over `p.AgentSocket`; `Index: update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{})`; `HTTP` left nil. `NewerAgents` is set on the controller's host only (Task 16).

**C16. update commands (Task 16).** A hand-written `update` group adopts `core.update.apply`, `core.update.rollback`, `core.update.status`, `core.update.rollout`, `core.update.rollout-cancel` and `core.update.rollouts` (`cli.Options.Adopt`) and adds `update run <tx>` (`Updater.Run`) and `update recover [--boot | --deadline <tx>]` (`update.RecoverBoot`, `RecoverDeadline` with `tx`, else `RecoverFailure`); both open agent.db without taking the agent lock. The adopted `update rollback` gains `--direct` and `--transaction <tx>`: with `--transaction` it runs `Updater.RunRollback(tx)`; without, `Updater.DirectRollback(force)` and prints the transaction (JSON with `--json`). `cli.Options.LocalFallbacks["core.update.rollback"]` fails with `update.nothing_to_roll_back` when `Paths.PrevBinary()` is missing, else runs `<PrevBinary> update rollback --direct --json [--force]` (never from a process already under `--direct`) and returns its standard output as the output. On the controller's host (`p.ControllerDB()` exists) `update.Config.NewerAgents` reads `core_hosts.agent_version` of enrolled hosts from controller.db.

**C17. system install and uninstall (Task 17).** `darkpanel system install --role controller|agent [--agent-listen <addr>]... [--agent-advertise <host:port>]` (root): for the agent role the token is standard input, trimmed, when standard input is not a terminal (at most 4096 bytes), else empty; the controller role never reads standard input, because it enrolls its own agent through `core.host.enroll-local`. `install.Config.Controller` is a client of the controller socket (`Healthy`: `GET /healthz` answers status `ok`; `EnrollLocal`: plan `core.host.enroll-local/v1` and apply it with the plan's confirmation phrase, returning `output.token`). `install.Config.Agent` asks the agent socket's `hello` first and answers `CheckAccepted` without a handshake when `controllerConnected` is true, else runs `daemon.Check` (register row 88: a check beside a connected daemon would replace its session). `darkpanel uninstall [--keep-data] [--yes]` (root): `install.PlanUninstall`, `Describe` to standard output, `Proceed? [y/N]` unless `--yes`, then `Run`.

**C18. Harness builds (Task 18).** Build tag `harnessbroken`: serve passes `HealthStatus: "degraded"` and agent run sets `daemon.Config.NeverConnected` (new field: Hello never reports a live controller session). `harnesscrash`: `serve` and `agent run` exit 1 at once with `harnesscrash: exiting at start`; `selftest` still passes. `harnessmigration`: `internal/core/store` embeds one more core migration per side, numbered 9000 (`9000_harness_marker.sql`), creating `harness_marker (note TEXT NOT NULL) STRICT` and inserting one row; an older binary ignores it (recorded migrations above its highest embedded one are ignored, `internal/core/store/migrate.go:84`).

**C19. Harness builds and topology (Tasks 19-22).** Task 19: `make harness-builds` writes `bin/harness/darkpanel-<version>` (linux/amd64, `CGO_ENABLED=0`, `-trimpath`, no UI, tag `harnesskeys` plus the variant's tag) for `0.2.0` (good, N), `0.3.0` (good), `0.3.1` (`harnessbroken`), `0.3.2` (`harnesscrash`), `0.3.3` (good), `0.3.4` (`harnessmigration,harnessbroken`), and `bin/harness/install.sh` (the generator with `-tags harnesskeys`); the Makefile's `LDFLAGS` becomes `$(call ldflags,$(VERSION))` with an unchanged expansion; every target VM gets `sqlite3` and the UFW rule `ufw allow in on dppriv to any port 7443 proto tcp`; `harness.yml`'s build job uploads `bin/harness` as the artifact `darkpanel-harness`, and its vm job downloads it, with `timeout-minutes: 120` and go test's `-timeout 100m`. Task 20 defines the harness test flag `-m2 <dir>`, which points at that directory (the workflow passes `-m2 "$GITHUB_WORKSPACE/bin/harness"` and uploads `harness/**/*.log`, because TestM2's VMs log under `<workdir>/m2`); adds `func (v *VM) RunInput(ctx context.Context, cmd string, stdin io.Reader) (string, error)` to package `vm`; splits `bootPair` in `m1_test.go` into `harnessImages` and `bootVMs` (TestM1 unchanged, deadline 40 minutes); and adds `TestM2` (deadline `m2Deadline` = 55 minutes), which runs named steps from one step list: Task 20 `install`, `self-management`, `offline`, `revoke`; Task 21 `update-good`, `health-failure`, `crash-loop`, `reboot-in-window`, `runner-frozen`, `migration-rollback`; Task 22 `break-glass`, `uninstall`. Task 21's `requireSettled` is used by Task 22. Task 22 rewrites gate 7 in `docs/superpowers/gates.md`.

## Decisions

- Row 23: the registry refuses keyless revertible agent actions (C2), which makes `lifecycle.ReserveAll`'s documented rule ("an action that must not run during an update has to declare a key") a checked one. Keyless compensable and irreversible actions, the update actions among them, are unaffected; they cannot stay unconfirmed across an update's restart.
- Row 88: `system install` does not run a handshake beside a connected daemon (C17); `darkpanel agent check` by hand still does, and says so in its help.
- Row 89: one `flock` per credential directory serialises the writers; the per-writer link name the row suggests is not needed under the lock. A renewal superseded by an enrollment leaves an unrevoked certificate whose key was never written to disk; it expires within a year and needs no revocation.
- Row 90: deferred to M7 hardening; one active rollout fleet-wide and `force` cancel for a host that never returns are spec section 8's design.
- harnessmigration lives in `internal/core/store` (the store owns its embedded migrations), not in `cmd/darkpanel` as the spec's table says.
- Production `serve` refuses root (C14): run as root it would create root-owned controller files that the `darkpanel` unit could not open.
- `--local`'s fallback for the irreversible `core.update.rollback` needs only `--yes` (C6): without the agent there is no plan, so there is no confirmation phrase to type.
- A development agent (`agent run --dev`) never connects to D-Bus: its updater gets a systemd provider whose every call fails with `agent run --dev does not manage systemd units` (C15).
- `selftest` requires at least one trusted key (C13). Default builds trust none until the owner adds the release key (register row 35), so they fail it; the updater runs `selftest` only on a staged release binary, and every release binary passes `releasetool check-build`, which refuses an empty key list.
- Gate 7's Applies also names `install.sh.in`, `internal/install/` and `internal/update/` (Task 22), because the M2 scenarios install and update real hosts with that code.

## Assumptions (evidence)

### How the code was verified

- Every task's code was implemented, tested and committed on dry-run branches before this plan was written, one commit per task, and each task section reproduces its commit byte for byte: a replay script applied each section to its commit's parent and compared every file the commit changes (2026-10-20, all 24 sections; Task 1's six snapshots are `make schemas` output, as its step says). The plan file itself was then applied task by task in plan order to a worktree of `main` (`486cd3f`), running `make schemas` in Task 1, and the result equals the dry run's final tree for all 107 files the dry run changed (2026-10-20). The integration check passed on that tree: see the last bullet of this section.
- Each dry-run commit's Step 2 failure line was captured before its implementation, and its Step 4 commands passed after it, with the commands as written (`GOTOOLCHAIN=local`, a per-task `TMPDIR`), as root, on 2026-10-20. Each task's evidence also includes mutations of its implementation (removed checks, swapped conditions), each caught by the task's own tests and then restored.
- Wave order differs from the dry run's commit order in two places. Task 6 ran on Tasks 1-5 and 7-9 but here runs in wave 3, before Tasks 7-9; Task 13 ran on that same base but here runs after Tasks 10-12. This is inference: neither task shares a file or a Go package with the tasks it moved past (their **Files:** blocks), and neither consumes their names. The plan-order replay above confirms that every block still applies.
- The header's **Integration check** passed on the plan-order tree, 2026-10-20: `go build ./...`, `go test -count=1 ./...` (exit 0, no failing package), `go test -tags harness -race -count=1 ./test/harness/...` (three packages ok), both `go vet` runs, `golangci-lint` (`0 issues.`) and an empty `go mod tidy -diff`.
- Codex is off for the plan review (`codex-gate`: plugin-not-enabled), so dr-superpowers:judge-opus reviews the plan.

### Scoring

- The **Files** axis counts decisions, as the earlier M2 plans did (`docs/superpowers/plans/2026-10-20-m2-services.md`, every task `files 1`): a unit with its tests, and the call sites that only wire it in, are one shape; Task 18's build-tagged variant pairs are one repeated shape. Task 7 is the one task spanning several unrelated artifacts (a Go tool, a shell script, the workflow, the Makefile, the README), so it scores files 2 and coupling 1. Under that reading no task reaches `files + spec + coupling >= 4`, so none was split. The largest tasks by file count are 6 (10 files), 18 (18 files) and 20 (7 files); their code is complete and replay-checked, which is why spec scores 0 everywhere.
- Risk 3 marks the tasks touching concurrency, security or data loss: 4 (flock), 7 (release signing), 10 (process privilege split and shutdown order), 15 (the root agent daemon), 16 (binary rollback), 17 (install and uninstall), 18 (the store's migration loading). Each runs alone in its wave.

### Facts the tasks rely on

- Task 1: `TestServerRegistryMatchesBuild` (`internal/controller/server/services_test.go:212`) compares the server's core registry with `registry.Build()`'s, so Task 1 also edits `services.go`, and Task 10 keeps those fields. `go list -deps` of `internal/install`, `internal/update`, `internal/controller/rollout` and `internal/controller/coreactions` names no `internal/registry`, so the new imports make no cycle (2026-10-20). Every module `internal/deps/deps.go` held has a real importer, so deleting it leaves `go mod tidy -diff` empty (2026-10-20).
- Task 2: `rg 'DefineRevertible|ClassRevertible'` found exactly two keyless revertible agent actions, both test fixtures (`internal/core/registry_test.go`, `internal/cli/fixture_test.go`), and no production module declares a revertible action (2026-10-20).
- Task 3: the agent's renewal treats any error as a retry after `RenewRetry` (`internal/agent/daemon/renew.go:80`), and no code in `internal/agent`, `internal/transport`, `internal/cli` or `cmd` matches `host.offline` or 503, so changing the renewal's answer to `unavailable` breaks no client (2026-10-20). `coreactions` already depends on `internal/agent/runtime` (`go list -deps`).
- Task 4: the lock follows the repository's flock style (`internal/controller/server/server.go:245-262`, `internal/agent/daemon/daemon.go:160-176`). `save.lock` is ignored by `listGenerations` and `LoadCredentials`, and uninstall removes `ConfigDir`, which holds `AgentTLSDir`, as one tree (`internal/install/uninstall.go:530-532`). A renewal superseded by an enrollment leaves a certificate whose key was never written; its row expires within `CertValidity`, 365 days (`internal/controller/fleet/ca.go:33`).
- Task 5: `update.ControllerProbe` requires HTTP 200 and then reads `status` (`internal/update/health.go:36-62`), so a `degraded` answer must keep status 200 (register row 44).
- Task 6: the agent socket's handler takes the principal from the peer identity alone (`internal/agent/api/socket.go:33`); cobra runs only the nearest persistent pre-run hook unless `EnableTraverseRunHooks` is set (cobra v1.10.2 `command.go:974-985`); `paths.Production().AgentSocket` is `/run/darkpanel-agent/agent.sock` (`internal/paths/paths.go:73`).
- Task 7: git-cliff 2.14.2 with `--tag-pattern` lists every commit since the last stable tag for a stable release (scratch repository with two betas, 2026-10-20). `gh release create --latest=false` explicitly does not mark a release latest (cli.github.com/manual/gh_release_create, fetched 2026-10-20). GitHub's immutable releases forbid replacing a published asset, which `releases.json` on the rolling `index` release needs (docs.github.com "Immutable releases", fetched 2026-10-20). The repository is public, so the verify job's `git fetch` needs no persisted credentials (api.github.com, 2026-10-20). `actionlint` v1.7.12 reported nothing on `release.yml` (2026-10-20).
- Task 8: `golang.org/x/crypto` v0.58.0 `ssh.Client.DialContext` (`ssh/tcpip.go:380`) returns `ctx.Err()` when the context ends first and closes a late connection.
- Task 9: `aead.dev/minisign` v0.3.0 `PrivateKey.UnmarshalText` (`private.go:132`) accepts the unencrypted harness key, which `PrivateKeyFromFile` refuses; `(*Reader).SignWithComments` (`minisign.go:116`) signs in hashed mode, the only mode `internal/update/download.go:296` accepts. A published release verified both in `update.Download` (the unit test) and in the harness `install.sh`'s `verify_release` (scratch run, 2026-10-20).
- Task 10: `paths.MkdirAll` also creates `AgentDir` (`internal/paths/paths.go:132-143`), so controller-only serve creates only its own directories; `fleet.NewRegistry` starts no goroutine before `Register` (`internal/controller/fleet/registry.go:70-95`); the audit ingester skips an offline host (`internal/controller/audit/ingest.go:177-180`).
- Task 11: the rollout driver retries only an uncoded error, `host.offline` or `lock.held` (`internal/controller/rollout/driver.go:207-215`) and fails the host on any other coded error (`driver.go:323-330`), so `unavailable` from the nil-updater actions ends an embedded-mode rollout instead of retrying it forever.
- Task 12: the fleet end-to-end test passed under `-race -count=3` in about 1.2 s per run (2026-10-20); dropping `Deps.Sessions`, `dispatch.Config.Version` or `coreactions.Deps.Version` each fails it (register rows 3 and 4).
- Task 13: `releasetool check-build <version> <key id>[,<key id>] < version.json` reads `version`, `testBuild` and `trustedKeys` (`release/releasetool/main.go:25`, `:85-92`, `:122-126`); a `harnesskeys` build's `version --json` is refused as a test build, and the same output with `testBuild` false is accepted (2026-10-20).
- Task 14: the controller unit runs `serve` as `User=darkpanel` and its drop-in passes `--agent-listen` and `--agent-advertise` (`internal/install/units/darkpanel-controller.service`, `10-agent.conf`).
- Task 15: a built `agent run --dev` served its socket unenrolled and exited 0 on SIGTERM; `agent check --dev` then exited 1 (smoke run, 2026-10-20).
- Task 16: when root opens a database owned by another user, modernc.org/sqlite v1.60.1 gives the `-wal` and `-shm` files it creates the database's owner (scratch test as root, 2026-10-20; `TestNewerAgentsKeepsOwner` keeps the check).
- Task 18: an older binary ignores recorded migrations above its highest embedded one (`internal/core/store/migrate.go:84`, `TestMigrateIgnoresNewerRecordedMigrations` at `migrate_test.go:310`). The 0.3.4 variant answered `/healthz` `degraded` and wrote `harness_marker` at migration 9000, and 0.3.0 then started on the same state and answered `ok` (2026-10-20).
- Task 19: ufw 0.36.2 checks only an interface name's syntax (`common.py` `set_interface`, lines 295-341), so the `dppriv` rule is harmless on TestM1's single-NIC target. `make harness-builds` built the six binaries and `install.sh`; each binary reported its version, `testBuild: true` and the harness key, and passed `selftest`; `install.sh` in library mode verified two of the published releases served over HTTP (2026-10-20).
- Task 20: the harness API client's login, plan, apply (with the confirmation phrase), read and resource calls, and the two-step first-admin flow, were run against a development controller (2026-10-20); the routes match `internal/controller/api/routes.go` and `actions.go`.
- Task 21: `sqlite3` 3.46.1 read `core_locks` and `harness_marker` from databases the 0.3.4 build created (2026-10-20). STRICT tables need SQLite 3.37; Ubuntu 22.04 ships 3.37.2 (package version, not run).
- Task 22: on this host, with no agent socket, `hostinfo summary get --local --json` exits 1 naming the socket, `update rollback --local --yes --json` prints the fallback line and then `update.nothing_to_roll_back`, and without `--yes` and a terminal it exits 3 (2026-10-20).

### Unverified until gate 7 runs the scenarios

No KVM is available here, so the following claims are inference until gate 7 runs TestM2 in `.github/workflows/harness.yml`. The verifying step is gate 7's run, after the last wave, as the Global Constraints spell out (the push waits for the owner's go-ahead); each bullet names the task whose scenario checks the claim. Decision on the borderline assumptions score (13 in the round-1 review): these claims cannot be verified on this host, so the plan routes all of them through that one gated run instead of a per-task step, and execution stops on a red run.

- unverified — Task 20 verifies it: three VMs boot together with 2 GiB each on a GitHub runner; QEMU's socket `mcast=` netdev keeps the private link apart from the internet link by UDP port; the guest sshd allows TCP forwarding (OpenSSH's default) for `DialContext`; the whole run fits the budget (vm job 120 minutes, go test 100, TestM1 40, TestM2 55).
- unverified — Task 20 verifies it: `/usr/bin/python3 -m http.server` runs on the scanner under `systemd-run` and the targets reach `http://198.18.0.20:8000` through UFW; install.sh's OpenSSL checks pass with OpenSSL 3.0 (Ubuntu 22.04, Debian 12); `system install --role controller --agent-listen 10.77.0.10:7443` binds that address; both roles enroll and report 0.2.0; the controller runs as `darkpanel` and the agent as root.
- unverified — Task 20 verifies it: `hostinfo.summary` of both hosts over mTLS; no darkpanel TCP listener on the agent and none beyond loopback and `10.77.0.10:7443` on the controller; the scans find only 22/tcp; a stopped agent is offline within 60 s; a revoked agent stays offline and re-enrolls within three minutes.
- unverified — Task 20 verifies it: from Task 10 on, TestM1's `checkServe` (`test/harness/m1_test.go:239`, `serve --dev` as root) starts a controller-only process that also opens the agent listener on `127.0.0.1:7443`; being loopback-only, it should not change TestM1's listener and scan checks, which Task 20 leaves unchanged.
- unverified — Task 21 verifies it: the rollout reads the index from the scanner and updates both hosts to 0.3.0 within two minutes each, and the 250 ms poll catches the agent at 0.2.0 beside the controller at 0.3.0 (a window of a few seconds, the run's riskiest timing).
- unverified — Task 21 verifies it: 0.3.1 rolls back for `degraded`; 0.3.2's crash loop is rolled back by `darkpanel-rollback.service`; a reboot right after 0.3.3's swap lands inside the health window; a runner frozen after the swap is rolled back by the deadline timer; 0.3.4 leaves a backup and `harness_marker` rows that 0.3.0 runs on (agent.db holds the marker only if the 0.3.4 agent migrated before the rollback stopped it, likely but not certain); `sqlite3` as root leaves no root-owned `-wal` or `-shm` file.
- unverified — Task 22 verifies it: `--local` reads answer with the controller stopped; with the agent stopped too, `update rollback --local --yes --json` re-executes `darkpanel.prev` and returns the agent host to 0.2.0; `uninstall --yes` leaves nothing the manifest lists, and `uninstall --keep-data --yes` followed by a reinstall of 0.2.0 keeps the admin and both hosts.

### Found next to this work, not fixed

- `internal/controller/fleet/problem.go:42-45` logs every 5xx answer at error level, so the expected 503 `unavailable` during a graceful shutdown logs an error.
- `internal/cli/fixture_test.go:126` says `testRegistry` is built as `registry.Build` builds it; after Task 1 `Build` adds `Extra` and `OwnedPaths`, which `testRegistry` does not.
- `internal/registry/registry_test.go` reads `mods[0].ID` in a failure message, so an empty module list panics there (M1 platform-core register row 44).
- Re-enrolling an agent as a different host leaves the old host enrolled with its last serial unrevoked; the operator has to revoke it.
- `harness.yml`'s build job runs the harness unit tests without `-race`.

## Task index

1. Update and rollout actions in the core manifest
2. Revertible agent actions declare a lock key
3. The unavailable error code
4. Serialised credential saves
5. Health status in /healthz
6. --local through the agent socket
7. Release workflow hardening
8. Harness VM private link and port forwarding
9. Harness release builder
10. Controller-only serve
11. Rollouts in serve
12. End to end over the agent transport
13. Version fields and selftest
14. serve flags
15. agent commands
16. update commands
17. system install and uninstall
18. Harness builds
19. Harness provisioning, builds and workflow
20. M2 harness topology and fleet scenarios
21. Update scenarios
22. Break-glass and uninstall scenarios
23. User documentation
24. Spec and ADR amendments

---

### Task 1: Update and rollout actions in the core manifest

**Files:**
- Modify: `internal/registry/registry.go`
- Modify: `internal/controller/server/services.go`
- Delete: `internal/deps/deps.go`
- Create: `schemas/core.update.apply/v1.json`, `schemas/core.update.rollback/v1.json`, `schemas/core.update.status/v1.json`, `schemas/core.update.rollout/v1.json`, `schemas/core.update.rollout-cancel/v1.json`, `schemas/core.update.rollouts/v1.json` (generated by `make schemas`)
- Test: `internal/registry/registry_test.go`
- Test: `internal/controller/coreactions/registry_test.go`
- Test: `internal/modules/modules_test.go`

**Interfaces:**
- Consumes: P11-C7 (`update.Actions(u *update.Updater) []core.ActionDef`, `rollout.Actions(rollout.ActionsConfig{Version}) []core.ActionDef`), `install.CoreOwnedPaths() []core.OwnedPath`, `coreactions.Deps.Extra` and `coreactions.Deps.OwnedPaths`.
- Produces: C1 `registry.CoreActions(u *update.Updater, controllerVersion string) []core.ActionDef`; `registry.Build()` now holds the six update and rollout actions and the core owned paths; the server's own registry (`internal/controller/server/services.go`) passes `Extra: registry.CoreActions(nil, buildinfo.Version)` and `OwnedPaths: install.CoreOwnedPaths()`, so Task 10 finds them already wired. Test-only names in package `registry`: `coreActionNames`, `TestCoreActions`. In package `coreactions_test`: field `agent` of `declaration`.

Neither `internal/update`, `internal/controller/rollout` nor `internal/install` imports `internal/registry`, so the new imports make no cycle. The generated CLI commands `darkpanel update apply|rollback|status|rollout|rollout-cancel|rollouts` appear in `registry.Build()`'s tree with no further change: `core.ReservedNames` refuses only module ids, and the CLI reserves `update` only once the assembly passes a hand-written `update` group in `cli.Options.Commands` (Task 16 adopts them then).

**Items:** 86
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Replace `internal/registry/registry_test.go` with this complete content:

```go
package registry

import (
	"context"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// coreActionNames are the update and rollout actions every process's core
// manifest holds, in registration order.
var coreActionNames = []string{
	"core.update.apply", "core.update.rollback", "core.update.status",
	"core.update.rollout", "core.update.rollout-cancel", "core.update.rollouts",
}

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
		t.Fatalf("modules = %d, first %q; want core first, then modules.All()", len(mods), mods[0].ID)
	}
	var names []string
	for _, a := range mods[0].Actions {
		if slices.Contains(coreActionNames, a.Spec.Name) {
			names = append(names, a.Spec.Name)
		}
	}
	if !slices.Equal(names, coreActionNames) {
		t.Errorf("core update and rollout actions = %q, want %q", names, coreActionNames)
	}
	if !reflect.DeepEqual(mods[0].OwnedPaths, install.CoreOwnedPaths()) {
		t.Errorf("core owned paths = %v, want install.CoreOwnedPaths()", mods[0].OwnedPaths)
	}
}

func TestCoreActions(t *testing.T) {
	var names []string
	for _, a := range CoreActions(nil, "") {
		names = append(names, a.Spec.Name)
	}
	if !slices.Equal(names, coreActionNames) {
		t.Errorf("CoreActions = %q, want %q", names, coreActionNames)
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

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 600 go test -count=1 -run 'TestBuild$|TestCoreActions' ./internal/registry/`
Expected: FAIL with `internal/registry/registry_test.go:61:20: undefined: CoreActions`

- [ ] **Step 3: Write the implementation**

Replace `internal/registry/registry.go` with this complete content:

```go
// Package registry builds the one module registry every component uses: the
// core module first, then modules.All() (spec section 2).
package registry

import (
	"fmt"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/update"
)

// CoreActions returns the update and rollout actions of the core manifest,
// for coreactions.Deps.Extra. Every process registers both sets so that the
// agent and the controller hold the same manifest and schema hashes: the
// agent passes its updater and runs only the update actions, the controller
// passes nil and its version and runs only the rollout actions.
func CoreActions(u *update.Updater, controllerVersion string) []core.ActionDef {
	return append(update.Actions(u), rollout.Actions(rollout.ActionsConfig{Version: controllerVersion})...)
}

// Build returns the registry with a core module built from zero Deps apart
// from the core actions and owned paths, for callers that use only the
// registry's shape: the CLI, the schema generator and tests. Its core
// handlers must not be called.
func Build() (*core.Registry, error) {
	return BuildWith(coreactions.Module(coreactions.Deps{Extra: CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths()}))
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

The server builds its own registry and a test (`TestServerRegistryMatchesBuild`) requires it to match `registry.Build()`, so the server passes the same core actions and owned paths now.

In `internal/controller/server/services.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
```

In `internal/controller/server/services.go`, replace:

```go
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
	}))
```

with:

```go
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
	}))
```

`TestCoreActionDeclarations` lists every core action and required them all to be controller-side; the three update actions are agent-side.

In `internal/controller/coreactions/registry_test.go`, replace:

```go
	compensatedBy string
}
```

with:

```go
	compensatedBy string
	agent         bool
}
```

In `internal/controller/coreactions/registry_test.go`, replace:

```go
	"core.user.delete":         {class: core.ClassIrreversible, breakGlass: true},
}
```

with:

```go
	"core.user.delete":         {class: core.ClassIrreversible, breakGlass: true},

	"core.update.apply":          {class: core.ClassCompensable, compensatedBy: "core.update.rollback", agent: true},
	"core.update.rollback":       {class: core.ClassIrreversible, agent: true},
	"core.update.status":         {class: core.ClassRead, agent: true},
	"core.update.rollout":        {class: core.ClassCompensable, compensatedBy: "core.update.rollout-cancel"},
	"core.update.rollout-cancel": {class: core.ClassIrreversible},
	"core.update.rollouts":       {class: core.ClassRead},
}
```

In `internal/controller/coreactions/registry_test.go`, replace:

```go
		got := declaration{class: s.Class, breakGlass: s.BreakGlass, lockKeys: s.LockKeys, compensatedBy: s.CompensatedBy}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", a.Ref(), got, want)
		}
		if s.Version != 1 || s.Side != core.SideController || s.Summary == "" {
			t.Errorf("%s: version %d, side %s, summary %q; want version 1, controller side, a summary", a.Ref(), s.Version, s.Side, s.Summary)
		}
```

with:

```go
		got := declaration{class: s.Class, breakGlass: s.BreakGlass, lockKeys: s.LockKeys, compensatedBy: s.CompensatedBy,
			agent: s.Side == core.SideAgent}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("%s: %+v, want %+v", a.Ref(), got, want)
		}
		if s.Version != 1 || (s.Side != core.SideController && s.Side != core.SideAgent) || s.Summary == "" {
			t.Errorf("%s: version %d, side %s, summary %q; want version 1, a known side, a summary", a.Ref(), s.Version, s.Side, s.Summary)
		}
```

`TestServerRegistryHasTheSameShape` builds the core module as the server does, so it passes the same `Extra` and `OwnedPaths`.

In `internal/modules/modules_test.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/controller/auth"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
```

In `internal/modules/modules_test.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
```

In `internal/modules/modules_test.go`, replace:

```go
		Auth: new(auth.Service), Audit: &coretest.MemAudit{}, Modules: modules.All(),
	}))
```

with:

```go
		Auth: new(auth.Service), Audit: &coretest.MemAudit{}, Modules: modules.All(),
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
	}))
```

Delete the build-tagged file that kept modules in `go.mod` before they had importers; every module it names now has one (`aead.dev/minisign` in `internal/update/download.go`, `github.com/godbus/dbus/v5` in `internal/platform/systemd.go`, `github.com/hashicorp/yamux` in `internal/transport/session.go`):

```bash
git rm internal/deps/deps.go
```

Generate the snapshots of the six new actions:

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 600 make schemas`
Expected: `git status --short schemas/` lists exactly six new directories, whose files have these SHA-256 sums (`sha256sum schemas/core.update.*/v1.json`):

```
08cbaf33cacc0516f65ed3a3af22950cb17a6c0e53a558490e640cbc1bf2a5b2  schemas/core.update.apply/v1.json
8f1f1850a1e09256a95b1262910cabefa0ff19ec4f32e371ac1042e8f93385e4  schemas/core.update.rollback/v1.json
98c4db2ef881d8b6e8f921258e18eaa891b50907b20f91965d857de2793be6c7  schemas/core.update.rollout-cancel/v1.json
71c3c0a8a098201833ed051300a35defeb29acbf4112586961b164a375eb9939  schemas/core.update.rollout/v1.json
21a5c30b718a92503a516637b018c9d7ead8978c00bd498748b6a43a283edfb6  schemas/core.update.rollouts/v1.json
1728f99a5b137336adf3c4cd7070ba52930c52c1a5b67c7972aae555f5d230ce  schemas/core.update.status/v1.json
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 900 go test -race -count=1 ./internal/registry/ ./internal/controller/coreactions/ ./internal/modules/ ./internal/controller/server/ ./internal/cli/ ./schemas/`
Expected: PASS (every package `ok`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/1 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/registry/registry.go internal/registry/registry_test.go internal/controller/server/services.go internal/controller/coreactions/registry_test.go internal/modules/modules_test.go schemas/core.update.apply schemas/core.update.rollback schemas/core.update.status schemas/core.update.rollout schemas/core.update.rollout-cancel schemas/core.update.rollouts
git commit -m "feat(registry): register the core update actions"
```

---

### Task 2: Revertible agent actions declare a lock key

**Files:**
- Modify: `internal/core/registry.go`
- Modify: `internal/core/lifecycle/reserve.go`
- Test: `internal/core/registry_test.go`
- Test: `internal/cli/fixture_test.go`

**Interfaces:**
- Consumes: none.
- Produces: C2: `core.NewRegistry` refuses an agent-side revertible action with no lock keys (`action <ref>: a revertible agent action declares at least one lock key, so an update's reservation holds it off`). The test helper `revertible(name, side)` in package `core` (`internal/core/registry_test.go`) now declares the lock key `name`; no new top-level identifier.

Every other revertible action in the repository's tests already declares a lock key (`internal/controller/api/server_test.go`, `internal/controller/dispatch/fixture_test.go`, `internal/agent/runtime/fixture_test.go`, `internal/core/lifecycle/fixture_test.go`, `internal/core/schema/module_test.go`); no production module declares a revertible action. Only the two fixtures below break.

**Items:** 23
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

In `internal/core/registry_test.go`, replace:

```go
	return DefineRevertible[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: side}, echo{})
```

with:

```go
	return DefineRevertible[echoIn, echoOut](ActionSpec{Name: name, Version: 1, Side: side, LockKeys: []string{name}}, echo{})
```

In `internal/core/registry_test.go`, replace:

```go
	noVerify.Handler.Verify = nil
```

with:

```go
	noVerify.Handler.Verify = nil
	keyless := revertible("demo.rule.set", SideAgent)
	keyless.Spec.LockKeys = nil
```

In `internal/core/registry_test.go`, replace:

```go
		{"revertible on controller", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{revertible("demo.rule.set", SideController)}}}, "revertible actions are agent-side"},
```

with:

```go
		{"revertible on controller", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{revertible("demo.rule.set", SideController)}}}, "revertible actions are agent-side"},
		{"revertible without a lock key", []Module{testModule{ID: "demo", Version: 1, Actions: []ActionDef{keyless}}}, "a revertible agent action declares at least one lock key"},
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 600 go test -count=1 -run 'TestNewRegistryRules' ./internal/core/`
Expected: FAIL with `registry_test.go:160: NewRegistry error = <nil>, want one containing "a revertible agent action declares at least one lock key"`

- [ ] **Step 3: Write the implementation**

In `internal/core/registry.go`, replace:

```go
		return fmt.Errorf("action %s: revertible actions are agent-side; controller-side actions have no revert timer", ref)
```

with:

```go
		return fmt.Errorf("action %s: revertible actions are agent-side; controller-side actions have no revert timer", ref)
	case s.Class == ClassRevertible && s.Side == SideAgent && len(s.LockKeys) == 0:
		return fmt.Errorf("action %s: a revertible agent action declares at least one lock key, so an update's reservation holds it off", ref)
```

In `internal/core/lifecycle/reserve.go`, replace:

```go
// and is not stopped, so an action that must not run during an update has to
// declare a key.
```

with:

```go
// and is not stopped, so an action that must not run during an update has to
// declare a key; core.NewRegistry enforces that for revertible agent actions,
// which could otherwise stay unconfirmed across an update's restart.
```

The CLI's demo module declares a keyless revertible action, so its registry would now fail to build (`change_test.go:18: action demo.rule.set/v1: a revertible agent action declares at least one lock key, ...`):

In `internal/cli/fixture_test.go`, replace:

```go
	add.CompensatedBy = "demo.item.remove"
```

with:

```go
	add.CompensatedBy = "demo.item.remove"
	rule := agent("demo.rule.set", 1, core.ClassRevertible)
	rule.LockKeys = []string{"demo.rule"}
```

In `internal/cli/fixture_test.go`, replace:

```go
			core.DefineRevertible(agent("demo.rule.set", 1, core.ClassRevertible), fixtureHandler[demoRuleInput, demoRule]{}),
```

with:

```go
			core.DefineRevertible(rule, fixtureHandler[demoRuleInput, demoRule]{}),
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 900 go test -race -count=1 ./internal/core/... ./internal/cli/`
Expected: PASS (every package `ok`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 1200 go test -count=1 ./...`
Expected: PASS (every package `ok`; this proves no other registry in the repository declares a keyless revertible agent action)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/2 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/core/registry.go internal/core/registry_test.go internal/core/lifecycle/reserve.go internal/cli/fixture_test.go
git commit -m "feat(core): refuse keyless revertible agent actions"
```

---

### Task 3: The unavailable error code

**Files:**
- Modify: `internal/core/errcode/errcode.go`
- Modify: `internal/controller/fleet/server.go`
- Modify: `internal/controller/coreactions/window.go`
- Test: `internal/core/errcode/errcode_test.go`
- Test: `internal/controller/fleet/server_test.go`
- Test: `internal/controller/coreactions/window_test.go`

**Interfaces:**
- Consumes: `agentruntime.ProtocolVersion` (`internal/agent/runtime/runtime.go`).
- Produces: C3 `errcode.Unavailable Code = "unavailable"` (HTTP 503, title `Unavailable`); the fleet server's answer to a request after `Server.Close` carries it; `coreactions.ProtocolVersion` and `TestProtocolVersionMatchesTheAgent` are removed. No new test identifiers.

`coreactions` already depends on the agent runtime through `internal/controller/fleet`, so importing it directly makes no cycle. No client matches the old `host.offline` answer: the agent daemon retries every failed renewal after `RenewRetry` (`internal/agent/daemon/renew.go`). Nothing else mirrors the code list in code (the web UI, the OpenAPI generator and the CLI do not enumerate codes).

**Items:** 91
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

In `internal/core/errcode/errcode_test.go`, replace:

```go
		{Internal, 500},
```

with:

```go
		{Internal, 500},
		{Unavailable, 503},
```

In `internal/core/errcode/errcode_test.go`, replace:

```go
	for code, want := range map[Code]string{
		HostOffline:              "host.offline",
```

with:

```go
	for code, want := range map[Code]string{
		Unavailable:              "unavailable",
		HostOffline:              "host.offline",
```

In `internal/controller/fleet/server_test.go`, replace:

```go
	wantProblemBody(t, rec.Code, rec.Body.Bytes(), http.StatusServiceUnavailable, errcode.HostOffline, errcode.HostOffline.Title())
```

with:

```go
	wantProblemBody(t, rec.Code, rec.Body.Bytes(), http.StatusServiceUnavailable, errcode.Unavailable, "Unavailable")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 600 go test -count=1 -run 'TestStatus|TestFleetCodeValues' ./internal/core/errcode/`
Expected: FAIL with `internal/core/errcode/errcode_test.go:37:4: undefined: Unavailable`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 600 go test -count=1 -run 'TestServerCloseWaitsForHandlers' ./internal/controller/fleet/`
Expected: FAIL with `internal/controller/fleet/server_test.go:413:88: undefined: errcode.Unavailable`

- [ ] **Step 3: Write the implementation**

In `internal/core/errcode/errcode.go`, replace:

```go
	Internal               Code = "internal"
)
```

with:

```go
	Internal               Code = "internal"
	Unavailable            Code = "unavailable"
)
```

In `internal/core/errcode/errcode.go`, replace:

```go
	Internal:               {http.StatusInternalServerError, "Internal error"},
```

with:

```go
	Internal:               {http.StatusInternalServerError, "Internal error"},
	Unavailable:            {http.StatusServiceUnavailable, "Unavailable"},
```

In `internal/controller/fleet/server.go`, replace:

```go
// cannot race Close's Wait. A request after Close is answered 503, because
// an empty answer would read as 200 and the agent could not tell it from a
// broken one. host.offline is the only code that answers 503.
```

with:

```go
// cannot race Close's Wait. A request after Close is answered 503
// unavailable, because an empty answer would read as 200 and the agent could
// not tell it from a broken one.
```

In `internal/controller/fleet/server.go`, replace:

```go
			writeProblem(w, s.cfg.Log, errcode.New(errcode.HostOffline, "the controller is shutting down"))
```

with:

```go
			writeProblem(w, s.cfg.Log, errcode.New(errcode.Unavailable, "the controller is shutting down"))
```

In `internal/controller/coreactions/window.go`, replace:

```go
import "github.com/darkraise/darkpanel/internal/update"

// ProtocolVersion is the agent protocol this controller speaks (ADR 0003).
const ProtocolVersion = 1
```

with:

```go
import (
	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/update"
)
```

In `internal/controller/coreactions/window.go`, replace:

```go
	if protocol != ProtocolVersion {
```

with:

```go
	if protocol != agentruntime.ProtocolVersion {
```

In `internal/controller/coreactions/window_test.go`, replace:

```go
import (
	"testing"

	agentruntime "github.com/darkraise/darkpanel/internal/agent/runtime"
)
```

with:

```go
import "testing"
```

In `internal/controller/coreactions/window_test.go`, delete these lines, from the empty line after `TestSupported`'s closing brace to the end of the file (the file then ends with that brace and a newline):

```go

// The agent reports the runtime's protocol and coreactions may not import the runtime outside tests, so this test keeps the two equal.
func TestProtocolVersionMatchesTheAgent(t *testing.T) {
	if ProtocolVersion != agentruntime.ProtocolVersion {
		t.Errorf("ProtocolVersion = %d, the agent runtime reports %d", ProtocolVersion, agentruntime.ProtocolVersion)
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 900 go test -race -count=1 ./internal/core/errcode/ ./internal/controller/fleet/ ./internal/controller/coreactions/ ./internal/controller/dispatch/`
Expected: PASS (every package `ok`)

Run: `grep -rn "coreactions.ProtocolVersion" --include=*.go .`
Expected: no output

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/3 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/core/errcode/errcode.go internal/core/errcode/errcode_test.go internal/controller/fleet/server.go internal/controller/fleet/server_test.go internal/controller/coreactions/window.go internal/controller/coreactions/window_test.go
git commit -m "feat(errcode): add the unavailable code"
```

---

### Task 4: Serialised credential saves

**Files:**
- Modify: `internal/agent/daemon/creds.go`
- Modify: `internal/agent/daemon/renew.go`
- Modify: `internal/agent/daemon/enroll.go`
- Test: `internal/agent/daemon/creds_test.go`
- Test: `internal/agent/daemon/daemon_test.go`
- Test: `internal/agent/daemon/check_test.go`

**Interfaces:**
- Consumes: P10-C3 `daemon.SaveGeneration`, `LoadCredentials`, `PruneGenerations`, `Enroll`; P10-C9 test helpers `newTestAuthority`, `(*testAuthority).credentials`, `sameCredentials`, `generations` (this task changes `generations` to skip `save.lock` too).
- Produces: C4 `func SaveGeneration(dir string, c *Credentials, expect int) (int, error)`, `const AnyCurrent = -1`, `var ErrSuperseded`; unexported `saveLock`, `currentGeneration`, `lockSave` in `internal/agent/daemon/creds.go`; tests `TestSaveGenerationExpect`, `TestSaveGenerationConcurrentWriters` in `internal/agent/daemon/creds_test.go`. The unexported `errSuperseded` in `renew.go` is removed.

**Items:** 89
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 3 = 5

- [ ] **Step 1: Write the failing test**

Replace the whole of `internal/agent/daemon/creds_test.go` with:

```go
package daemon

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/transport"
)

// testAuthority is a stand-in agent root for tests that need certificates
// but no controller.
type testAuthority struct {
	cert *x509.Certificate
	key  ed25519.PrivateKey
}

func newTestAuthority(t *testing.T) *testAuthority {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test agent root"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(24 * time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, pub, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &testAuthority{cert: cert, key: key}
}

// credentials returns hostID's credentials with a fresh key, as an
// enrollment or a renewal produces them.
func (a *testAuthority) credentials(t *testing.T, hostID string) *Credentials {
	t.Helper()
	return a.issue(t, transport.HostURI(hostID), x509.ExtKeyUsageClientAuth)
}

// issue returns credentials whose certificate names uri and has usage eku.
func (a *testAuthority) issue(t *testing.T, uri string, eku x509.ExtKeyUsage) *Credentials {
	t.Helper()
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		t.Fatal(err)
	}
	u, err := url.Parse(uri)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(12 * time.Hour),
		ExtKeyUsage: []x509.ExtKeyUsage{eku}, URIs: []*url.URL{u},
		KeyUsage: x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, a.cert, pub, a.key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	return &Credentials{
		Key: key, Cert: cert, CA: a.cert,
		Controller: Endpoint{Address: "127.0.0.1:7443", SAN: transport.ControllerURI("C1")},
	}
}

// sameCredentials reports whether got holds want's key and certificate.
func sameCredentials(got, want *Credentials) bool {
	return got != nil && got.Key.Equal(want.Key) && got.Cert.Equal(want.Cert) && got.CA.Equal(want.CA) &&
		got.Controller == want.Controller
}

// generations lists the entries of dir other than current and save.lock.
func generations(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		if e.Name() != currentLink && e.Name() != saveLock {
			out = append(out, e.Name())
		}
	}
	return out
}

func TestCredentialsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "agent")
	if _, err := LoadCredentials(dir); !errors.Is(err, ErrNotEnrolled) {
		t.Fatalf("LoadCredentials of an empty directory = %v, want ErrNotEnrolled", err)
	}
	ca := newTestAuthority(t)
	first := ca.credentials(t, "H1")
	n, err := SaveGeneration(dir, first, AnyCurrent)
	if err != nil || n != 1 {
		t.Fatalf("SaveGeneration = %d, %v; want generation 1", n, err)
	}
	got, err := LoadCredentials(dir)
	if err != nil || !sameCredentials(got, first) || got.Generation != 1 || got.HostID() != "H1" {
		t.Fatalf("LoadCredentials = %+v, %v", got, err)
	}
	if c := got.TLSCertificate(); c.Leaf != got.Cert || len(c.Certificate) != 1 {
		t.Errorf("TLSCertificate = %+v", c)
	}
	for path, mode := range map[string]os.FileMode{
		dir: 0o700, filepath.Join(dir, "gen-1"): 0o700, filepath.Join(dir, "gen-1", keyFile): 0o600,
		filepath.Join(dir, "gen-1", certFile): 0o600, filepath.Join(dir, "gen-1", caFile): 0o600,
		filepath.Join(dir, "gen-1", controllerFile): 0o600, filepath.Join(dir, saveLock): 0o600,
	} {
		if st, err := os.Stat(path); err != nil || st.Mode().Perm() != mode {
			t.Errorf("%s: %v, %v; want mode %v", path, st.Mode(), err, mode)
		}
	}
	second := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, second, AnyCurrent); err != nil || n != 2 {
		t.Fatalf("second SaveGeneration = %d, %v", n, err)
	}
	if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, second) || got.Generation != 2 {
		t.Fatalf("LoadCredentials after the second generation = %+v, %v", got, err)
	}
	// The previous generation stays until a handshake with the new one.
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-1", "gen-2"}) {
		t.Errorf("generations = %v, want both", gens)
	}
	// An enrollment beside the running daemon may already have written a
	// newer generation; a handshake with generation 2 must not delete it.
	third := ca.credentials(t, "H1")
	if n, err := SaveGeneration(dir, third, AnyCurrent); err != nil || n != 3 {
		t.Fatalf("third SaveGeneration = %d, %v", n, err)
	}
	if err := PruneGenerations(dir, 2); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-2", "gen-3"}) {
		t.Errorf("generations after pruning = %v, want gen-2 and gen-3", gens)
	}
	if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, third) {
		t.Errorf("LoadCredentials after pruning = %+v, %v; want the third generation", got, err)
	}
	if err := PruneGenerations(dir, 3); err != nil {
		t.Fatal(err)
	}
	if gens := generations(t, dir); !slices.Equal(gens, []string{"gen-3"}) {
		t.Errorf("generations after the second pruning = %v, want gen-3 alone", gens)
	}
}

func TestSaveGenerationRefusesBadCredentials(t *testing.T) {
	ca := newTestAuthority(t)
	other := newTestAuthority(t)
	cases := map[string]func(c *Credentials){
		"another key":       func(c *Credentials) { c.Key = ca.credentials(t, "H1").Key },
		"another authority": func(c *Credentials) { c.CA = other.cert },
		"no address":        func(c *Credentials) { c.Controller.Address = "" },
		"address port":      func(c *Credentials) { c.Controller.Address = "controller" },
		"controller SAN":    func(c *Credentials) { c.Controller.SAN = "darkpanel://host/C1" },
		"server usage": func(c *Credentials) {
			*c = *ca.issue(t, transport.HostURI("H1"), x509.ExtKeyUsageServerAuth)
		},
		"no host id": func(c *Credentials) {
			*c = *ca.issue(t, transport.ControllerURI("H1"), x509.ExtKeyUsageClientAuth)
		},
	}
	for name, spoil := range cases {
		dir := t.TempDir()
		c := ca.credentials(t, "H1")
		spoil(c)
		if _, err := SaveGeneration(dir, c, AnyCurrent); err == nil {
			t.Errorf("%s: SaveGeneration accepted it", name)
		}
		if _, err := LoadCredentials(dir); !errors.Is(err, ErrNotEnrolled) {
			t.Errorf("%s: a refused generation became current: %v", name, err)
		}
	}
}

func TestLoadCredentialsRefusesAForeignCurrent(t *testing.T) {
	dir := t.TempDir()
	ca := newTestAuthority(t)
	if _, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent); err != nil {
		t.Fatal(err)
	}
	for _, target := range []string{"../gen-1", "/etc", "gen-x"} {
		if err := os.Remove(filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadCredentials(dir); err == nil || errors.Is(err, ErrNotEnrolled) {
			t.Errorf("current -> %s: LoadCredentials = %v, want a refusal", target, err)
		}
	}
}

// Spec Verification "credentials": an enrollment or a renewal interrupted
// at any step before, during or after the switch of current leaves a
// complete old or a complete new generation, and a restarted agent can
// write the next one.
func TestSaveGenerationInterrupted(t *testing.T) {
	ca := newTestAuthority(t)
	crash := errors.New("power cut")
	switched := slices.Index(generationSteps, stepRename) + 1
	for _, renewal := range []bool{false, true} {
		for i, step := range generationSteps {
			dir := filepath.Join(t.TempDir(), "agent")
			var old *Credentials
			if renewal {
				old = ca.credentials(t, "H1")
				if _, err := SaveGeneration(dir, old, AnyCurrent); err != nil {
					t.Fatal(err)
				}
			}
			next := ca.credentials(t, "H1")
			_, err := saveGeneration(dir, next, AnyCurrent, func(s string) error {
				if s == step {
					return crash
				}
				return nil
			})
			if !errors.Is(err, crash) {
				t.Fatalf("renewal %v, step %s: saveGeneration = %v, want the crash", renewal, step, err)
			}
			// The restarted agent reads one complete generation.
			got, err := LoadCredentials(dir)
			switch {
			case i >= switched:
				if err != nil || !sameCredentials(got, next) {
					t.Errorf("renewal %v, crash at %s: loaded %+v, %v; want the new generation", renewal, step, got, err)
				}
			case renewal:
				if err != nil || !sameCredentials(got, old) {
					t.Errorf("renewal %v, crash at %s: loaded %+v, %v; want the old generation", renewal, step, got, err)
				}
			default:
				if !errors.Is(err, ErrNotEnrolled) {
					t.Errorf("enrollment, crash at %s: LoadCredentials = %v, want ErrNotEnrolled", step, err)
				}
			}
			// It enrolls or renews again, and the first handshake with the
			// result prunes everything else.
			again := ca.credentials(t, "H1")
			n, err := SaveGeneration(dir, again, AnyCurrent)
			if err != nil {
				t.Fatalf("renewal %v, crash at %s: SaveGeneration after the restart: %v", renewal, step, err)
			}
			if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, again) || got.Generation != n {
				t.Fatalf("renewal %v, crash at %s: loaded %+v, %v after the restart", renewal, step, got, err)
			}
			if err := PruneGenerations(dir, n); err != nil {
				t.Fatal(err)
			}
			if gens := generations(t, dir); !slices.Equal(gens, []string{generationName(n)}) {
				t.Errorf("renewal %v, crash at %s: left %v after pruning", renewal, step, gens)
			}
		}
	}
}

// A renewal saves only while current is still its session's generation; an
// enrollment saves whatever is current. A refused save writes nothing.
func TestSaveGenerationExpect(t *testing.T) {
	ca := newTestAuthority(t)
	cases := []struct {
		name    string
		current int // the generation current names; 0 removes current
		expect  int
		want    int // the saved generation; 0 wants ErrSuperseded
	}{
		{"the expected generation", 2, 2, 3},
		{"a newer generation", 2, 1, 0},
		{"an older generation", 1, 2, 0},
		{"no current", 0, 1, 0},
		{"any current", 1, AnyCurrent, 3},
		{"any current without current", 0, AnyCurrent, 3},
	}
	for _, tc := range cases {
		dir := filepath.Join(t.TempDir(), "agent")
		for range 2 {
			if _, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.Remove(filepath.Join(dir, currentLink)); err != nil {
			t.Fatal(err)
		}
		if tc.current != 0 {
			if err := os.Symlink(generationName(tc.current), filepath.Join(dir, currentLink)); err != nil {
				t.Fatal(err)
			}
		}
		state := func() string {
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			target, _ := os.Readlink(filepath.Join(dir, currentLink))
			out := "current -> " + target
			for _, e := range entries {
				out += " " + e.Name()
			}
			return out
		}
		before := state()
		next := ca.credentials(t, "H1")
		n, err := SaveGeneration(dir, next, tc.expect)
		if tc.want == 0 {
			if !errors.Is(err, ErrSuperseded) || n != 0 || next.Generation != 0 {
				t.Errorf("%s: SaveGeneration = %d, %v, generation %d; want ErrSuperseded", tc.name, n, err, next.Generation)
			}
			if after := state(); after != before {
				t.Errorf("%s: a superseded save changed %s to %s", tc.name, before, after)
			}
			continue
		}
		if err != nil || n != tc.want {
			t.Errorf("%s: SaveGeneration = %d, %v; want generation %d", tc.name, n, err, tc.want)
			continue
		}
		if got, err := LoadCredentials(dir); err != nil || !sameCredentials(got, next) || got.Generation != tc.want {
			t.Errorf("%s: LoadCredentials = %+v, %v; want the saved generation", tc.name, got, err)
		}
	}
}

// darkpanel agent enroll and the daemon's renewal save beside each other. Whatever the interleaving, current names a complete
// generation that its writer reported, and a writer that finished after
// another succeeded only if it expected any current.
func TestSaveGenerationConcurrentWriters(t *testing.T) {
	ca := newTestAuthority(t)
	for _, tc := range []struct {
		name    string
		renewal [2]bool // whether each writer expects the generation current at the start
	}{
		{"a renewal and an enrollment", [2]bool{true, false}},
		{"two renewals", [2]bool{true, true}},
		{"two enrollments", [2]bool{false, false}},
	} {
		dir := filepath.Join(t.TempDir(), "agent")
		start, err := SaveGeneration(dir, ca.credentials(t, "H1"), AnyCurrent)
		if err != nil {
			t.Fatal(err)
		}
		for round := range 20 {
			var creds [2]*Credentials
			var gens [2]int
			var errs [2]error
			var wg sync.WaitGroup
			ready := make(chan struct{})
			for i := range 2 {
				creds[i] = ca.credentials(t, "H1")
				expect := AnyCurrent
				if tc.renewal[i] {
					expect = start
				}
				wg.Go(func() {
					<-ready
					gens[i], errs[i] = SaveGeneration(dir, creds[i], expect)
				})
			}
			close(ready)
			wg.Wait()
			winner, won := 0, false
			for i := range 2 {
				switch {
				case errs[i] == nil && (!won || gens[i] > gens[winner]):
					winner, won = i, true
				case errs[i] == nil:
				case !tc.renewal[i] || !errors.Is(errs[i], ErrSuperseded):
					t.Fatalf("%s, round %d: writer %d: %v", tc.name, round, i, errs[i])
				}
			}
			if !won {
				t.Fatalf("%s, round %d: both writers were superseded", tc.name, round)
			}
			if loser := 1 - winner; errs[loser] == nil && tc.renewal[winner] {
				t.Fatalf("%s, round %d: renewal %d of generation %d saved after generation %d",
					tc.name, round, gens[winner], start, gens[loser])
			}
			got, err := LoadCredentials(dir)
			if err != nil || !sameCredentials(got, creds[winner]) || got.Generation != gens[winner] {
				t.Fatalf("%s, round %d: current is %+v, %v; want the winner's generation %d", tc.name, round, got, err, gens[winner])
			}
			start = gens[winner]
			if err := PruneGenerations(dir, start); err != nil {
				t.Fatal(err)
			}
		}
	}
}
```

In `internal/agent/daemon/daemon_test.go`, replace:

```go
	next := &Credentials{Key: key, Cert: cert, CA: old.CA, Controller: old.Controller}
	if _, err := SaveGeneration(dir, next); err != nil {
```

with:

```go
	next := &Credentials{Key: key, Cert: cert, CA: old.CA, Controller: old.Controller}
	if _, err := SaveGeneration(dir, next, old.Generation); err != nil {
```

In `internal/agent/daemon/check_test.go`, replace:

```go
	c.Controller = e
	if _, err := SaveGeneration(dir, c); err != nil {
```

with:

```go
	c.Controller = e
	if _, err := SaveGeneration(dir, c, AnyCurrent); err != nil {
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/plan6-tmp/4 && GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 600 go test -count=1 -run 'TestSaveGeneration|TestCredentialsRoundTrip' ./internal/agent/daemon/`
Expected: FAIL with `internal/agent/daemon/check_test.go:68:38: undefined: AnyCurrent`

- [ ] **Step 3: Write the implementation**

In `internal/agent/daemon/creds.go`, replace:

```go
	"strings"

	"github.com/darkraise/darkpanel/internal/transport"
)
```

with:

```go
	"strings"

	"golang.org/x/sys/unix"

	"github.com/darkraise/darkpanel/internal/transport"
)
```

In `internal/agent/daemon/creds.go`, replace:

```go
	currentTemp    = "current.tmp"
)

// ErrNotEnrolled is LoadCredentials' answer when AgentTLSDir has no current
// generation.
var ErrNotEnrolled = errors.New("daemon: this agent is not enrolled")
```

with:

```go
	currentTemp    = "current.tmp"
	saveLock       = "save.lock"
)

// ErrNotEnrolled is LoadCredentials' answer when AgentTLSDir has no current
// generation.
var ErrNotEnrolled = errors.New("daemon: this agent is not enrolled")

// AnyCurrent is SaveGeneration's expect for an enrollment, which replaces
// whatever generation is current.
const AnyCurrent = -1

// ErrSuperseded is SaveGeneration's answer when current is not the
// generation the caller expects: darkpanel agent enroll saved a newer one
// beside the daemon, or the credentials were removed.
var ErrSuperseded = errors.New("daemon: a newer credential generation is current")
```

In `internal/agent/daemon/creds.go`, replace:

```go
func LoadCredentials(dir string) (*Credentials, error) {
	target, err := os.Readlink(filepath.Join(dir, currentLink))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, ErrNotEnrolled
	}
	if err != nil {
		return nil, fmt.Errorf("daemon: read the current credentials: %w", err)
	}
	m := generationPattern.FindStringSubmatch(target)
	if m == nil {
		return nil, fmt.Errorf("daemon: %s points to %q, not to a generation", filepath.Join(dir, currentLink), target)
	}
	n, _ := strconv.Atoi(m[1])
	gen := filepath.Join(dir, target)
```

with:

```go
func LoadCredentials(dir string) (*Credentials, error) {
	n, err := currentGeneration(dir)
	if err != nil {
		return nil, err
	}
	gen := filepath.Join(dir, generationName(n))
```

In `internal/agent/daemon/creds.go`, replace:

```go
func parseKey(data []byte) (ed25519.PrivateKey, error) {
```

with:

```go
// currentGeneration is the number of the generation current names, or
// ErrNotEnrolled without current.
func currentGeneration(dir string) (int, error) {
	target, err := os.Readlink(filepath.Join(dir, currentLink))
	if errors.Is(err, fs.ErrNotExist) {
		return 0, ErrNotEnrolled
	}
	if err != nil {
		return 0, fmt.Errorf("daemon: read the current credentials: %w", err)
	}
	m := generationPattern.FindStringSubmatch(target)
	if m == nil {
		return 0, fmt.Errorf("daemon: %s points to %q, not to a generation", filepath.Join(dir, currentLink), target)
	}
	n, _ := strconv.Atoi(m[1])
	return n, nil
}

func parseKey(data []byte) (ed25519.PrivateKey, error) {
```

In `internal/agent/daemon/creds.go`, replace:

```go
// greater than every generation in dir, complete or not.
func SaveGeneration(dir string, c *Credentials) (int, error) {
	return saveGeneration(dir, c, func(string) error { return nil })
}

func saveGeneration(dir string, c *Credentials, before func(step string) error) (int, error) {
```

with:

```go
// greater than every generation in dir, complete or not.
//
// darkpanel agent enroll saves beside the running daemon's renewal, so every
// writer holds an exclusive flock on dir/save.lock from reading current to
// the fsync of dir; under it one current.tmp serves every writer. expect is
// AnyCurrent for an enrollment, or the generation that must still be
// current, the renewal's session generation: when current names another
// generation or none, SaveGeneration writes nothing and answers
// ErrSuperseded.
func SaveGeneration(dir string, c *Credentials, expect int) (int, error) {
	return saveGeneration(dir, c, expect, func(string) error { return nil })
}

func saveGeneration(dir string, c *Credentials, expect int, before func(step string) error) (int, error) {
```

In `internal/agent/daemon/creds.go`, replace:

```go
		return 0, fmt.Errorf("daemon: create %s: %w", dir, err)
	}
	gens, err := listGenerations(dir)
```

with:

```go
		return 0, fmt.Errorf("daemon: create %s: %w", dir, err)
	}
	unlock, err := lockSave(dir)
	if err != nil {
		return 0, err
	}
	defer unlock()
	if expect != AnyCurrent {
		n, err := currentGeneration(dir)
		if errors.Is(err, ErrNotEnrolled) || err == nil && n != expect {
			return 0, ErrSuperseded
		}
		if err != nil {
			return 0, err
		}
	}
	gens, err := listGenerations(dir)
```

In `internal/agent/daemon/creds.go`, replace:

```go
// PruneGenerations deletes every generation of dir numbered below keep. The
```

with:

```go
// lockSave waits for the exclusive lock on dir/save.lock and returns its
// release. A writer holds it for one save, a few fsyncs, so it blocks rather
// than failing.
func lockSave(dir string) (func(), error) {
	path := filepath.Join(dir, saveLock)
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o600)
	if err != nil {
		return nil, fmt.Errorf("daemon: open %s: %w", path, err)
	}
	if err := unix.Flock(fd, unix.LOCK_EX); err != nil {
		_ = unix.Close(fd)
		return nil, fmt.Errorf("daemon: lock %s: %w", path, err)
	}
	return func() { _ = unix.Close(fd) }, nil
}

// PruneGenerations deletes every generation of dir numbered below keep. The
```

In `internal/agent/daemon/renew.go`, replace:

```go
// errSuperseded is renew's answer when a newer generation became current
// during the session, written by darkpanel agent enroll beside the daemon.
var errSuperseded = errors.New("daemon: a newer credential generation is current")

// renewAt is when a certificate is renewed: after two thirds of its life
```

with:

```go
// renewAt is when a certificate is renewed: after two thirds of its life
```

In `internal/agent/daemon/renew.go`, replace:

```go
		if errors.Is(err, errSuperseded) {
```

with:

```go
		if errors.Is(err, ErrSuperseded) {
```

In `internal/agent/daemon/renew.go`, replace:

```go
// same host and chain to the same root, and saves it as a new generation.
```

with:

```go
// same host and chain to the same root, and saves it as a new generation
// while c's generation is still current, else it answers ErrSuperseded.
```

In `internal/agent/daemon/renew.go`, replace:

```go
	next := &Credentials{Key: key, Cert: cert, CA: c.CA, Controller: c.Controller}
	cur, err := LoadCredentials(dir)
	if err != nil {
		return nil, err
	}
	if cur.Generation != c.Generation {
		return nil, errSuperseded
	}
	if _, err := SaveGeneration(dir, next); err != nil {
```

with:

```go
	next := &Credentials{Key: key, Cert: cert, CA: c.CA, Controller: c.Controller}
	if _, err := SaveGeneration(dir, next, c.Generation); err != nil {
```

In `internal/agent/daemon/enroll.go`, replace:

```go
	if _, err := SaveGeneration(dir, c); err != nil {
		return nil, err
	}
	return c, nil
```

with:

```go
	if _, err := SaveGeneration(dir, c, AnyCurrent); err != nil {
		return nil, err
	}
	return c, nil
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 600 go test -race -count=1 ./internal/agent/daemon/`
Expected: PASS (`ok  github.com/darkraise/darkpanel/internal/agent/daemon`; `TestSaveGenerationConcurrentWriters` takes a few seconds under `-race`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/4 timeout 300 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/agent/daemon/creds.go internal/agent/daemon/renew.go internal/agent/daemon/enroll.go internal/agent/daemon/creds_test.go internal/agent/daemon/daemon_test.go internal/agent/daemon/check_test.go
git commit -m "fix(daemon): serialise credential saves"
```

---

### Task 5: Health status in /healthz

**Files:**
- Modify: `internal/controller/api/server.go`
- Modify: `internal/controller/api/meta.go`
- Test: `internal/controller/api/meta_test.go`

**Interfaces:**
- Consumes: none.
- Produces: C5 `api.Config.HealthStatus string` (empty means `ok`), reported by the bare `/healthz` answer, the detailed answer (authenticated network callers) and the socket's `/healthz`, always with HTTP 200. New names in package `api`: method `(*Server).health() string`, test `TestHealthzStatus`. `internal/controller/server` is not touched (Task 10 passes `server.Options.HealthStatus`).

`update.ControllerProbe` (`internal/update/health.go`) reads `GET /healthz` over the controller socket, requires HTTP 200 and decodes `status` and `version`; the socket row of the test below is that answer, so a degraded controller now reaches the probe's "status other than ok fails at once" rule instead of waiting out the deadline.

**Items:** 44
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

In `internal/controller/api/meta_test.go`, replace:

```go
func TestOpenAPIRoute(t *testing.T) {
```

with:

```go
// An unhealthy controller still answers 200, because the update's health
// probe reads any other status as "not yet" and would wait out its deadline
// instead of failing at once.
func TestHealthzStatus(t *testing.T) {
	h := newHarness(t)
	readToken := h.token(core.ScopeRead)
	for _, tc := range []struct{ config, want string }{
		{"", "ok"},
		{"degraded", "degraded"},
	} {
		h.srv.cfg.HealthStatus = tc.config
		detail := fmt.Sprintf(`{"status":%q,"version":%q,"commit":%q,"schemaVersion":3}`, tc.want, buildinfo.Version, buildinfo.Commit)
		for _, answer := range []struct {
			name string
			rec  *httptest.ResponseRecorder
			body string
		}{
			{"bare", h.serve(request("GET", "/healthz", "")), fmt.Sprintf(`{"status":%q}`, tc.want)},
			{"detail", h.serve(withToken(request("GET", "/healthz", ""), readToken)), detail},
			{"socket", h.socketRequest(request("GET", "/healthz", ""), 0), detail},
		} {
			if answer.rec.Code != http.StatusOK || answer.rec.Body.String() != answer.body {
				t.Errorf("HealthStatus %q, %s answer: %d %s, want 200 %s", tc.config, answer.name, answer.rec.Code, answer.rec.Body, answer.body)
			}
		}
	}
}

func TestOpenAPIRoute(t *testing.T) {
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 600 go test -count=1 -run 'TestHealthzStatus' ./internal/controller/api/`
Expected: FAIL with `internal/controller/api/meta_test.go:70:13: h.srv.cfg.HealthStatus undefined (type Config has no field or method HealthStatus)`

- [ ] **Step 3: Write the implementation**

In `internal/controller/api/server.go`, replace:

```go
	// UI is the web UI served at /; nil serves none.
	UI fs.FS
```

with:

```go
	// UI is the web UI served at /; nil serves none.
	UI fs.FS
	// HealthStatus is the status GET /healthz reports, always with HTTP 200;
	// empty means "ok".
	HealthStatus string
```

In `internal/controller/api/meta.go`, replace:

```go
		writeJSON(w, s.log, http.StatusOK, healthStatus{Status: "ok"})
```

with:

```go
		writeJSON(w, s.log, http.StatusOK, healthStatus{Status: s.health()})
```

In `internal/controller/api/meta.go`, replace:

```go
	writeJSON(w, s.log, http.StatusOK, healthDetail{Status: "ok", Version: buildinfo.Version, Commit: buildinfo.Commit, SchemaVersion: n})
}
```

with:

```go
	writeJSON(w, s.log, http.StatusOK, healthDetail{Status: s.health(), Version: buildinfo.Version, Commit: buildinfo.Commit, SchemaVersion: n})
}

// health is the status /healthz reports.
func (s *Server) health() string {
	if s.cfg.HealthStatus == "" {
		return "ok"
	}
	return s.cfg.HealthStatus
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 900 go test -race -count=1 ./internal/controller/api/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/5 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/controller/api/server.go internal/controller/api/meta.go internal/controller/api/meta_test.go
git commit -m "feat(api): report a configured status in /healthz"
```

---

### Task 6: --local through the agent socket

**Files:**
- Create: `internal/cli/local.go`
- Modify: `internal/cli/root.go`
- Modify: `internal/cli/read.go`
- Modify: `internal/cli/mutate.go`
- Modify: `internal/cli/enroll.go`
- Modify: `internal/cli/errors.go`
- Modify: `internal/cli/input.go`
- Test: `internal/cli/local_test.go` (create)
- Test: `internal/cli/root_test.go`
- Test: `internal/cli/errors_test.go`

**Interfaces:**
- Consumes: A-C `api.NewClient`, `api.ClientConfig{Dial}`, `(*api.Client).Read`, `.Plan`, `.Apply`, `.Resource` (`internal/agent/api/client.go`); in tests `api.NewSocketHandler`, `api.Config`, `api.SocketConfig` (`internal/agent/api/socket.go`) and `socket.Listen`, `socket.Config` (`internal/agent/socket/socket.go`); `paths.Production().AgentSocket`; `coretest.FakeAgent`; F-C11 `cli.Options.LocalFallbacks`, `cli.LocalFallback`; `errcode.UpdateNothingToRollBack`.
- Produces: C6 `cli.Options.AgentSocket string` (empty means `paths.Production().AgentSocket`), the root's persistent `--local` flag, `func cli.ExitWith(code int, err error) error`. A `LocalFallback` receives the input the command collected (the same JSON the socket's plan or read would get) and its output is shown as a read's. New package-level names in `internal/cli`: const `annotationLocal`; types `socketError`, `exitError`, `actionCalls`, `changeErrorWire`; funcs `guardLocal`, `refuseLocal`, `useLocal`, `localCalls`, `localError`, `optional`, `nullOutput`, `apiCalls`; methods `(*app).dialAgent`, `withAgent`, `runFallback`, `localRead`, `localResource`, `localMutating`, `applyAndShow`, `askApply`; `changeWire` gains `Actor`, `CreatedAt`, `UpdatedAt` and its `Error` becomes `*changeErrorWire`; `(*app).planAndApply` now takes `(cmd *cobra.Command, calls actionCalls, input any, agreed bool)`. Test names: type `agentCalls`, funcs `socketDir`, `agentSocket`, `staleSocket`, `localTestCommands`, tests `TestLocalReadAndResource`, `TestLocalMutatingPrintsLikeTheAPI`, `TestLocalConfirmations`, `TestLocalRefusals`, `TestLocalLeavesHelpAndCompletion`, `TestLocalFallback`; `cliEnv` gains the fields `agentSocket` and `fallbacks`.

Only generated agent-side commands carry the `annotationLocal` annotation; every other command run with `--local` is refused by the root's persistent pre-run hook, which `guardLocal` also puts in front of any hand-written command's own persistent hook (cobra runs only the nearest one). A hand-written command that later replaces the `RunE` of a generated agent-side command (the `update rollback` of the update group) keeps the annotation and must hand a `--local` run to the generated `RunE` it replaced. Before reading or planning, `--local` dials the socket once: a missing path or a refused connection runs the action's fallback when it has one; every other dial error, and a missing socket without a fallback, fails with `the agent socket <path> does not answer: <cause>`. The socket's plan and change are converted to the CLI's `planWire` and `changeWire`, so the existing printers and `--json` print exactly what the API path prints; `TestLocalMutatingPrintsLikeTheAPI` runs the same command both ways and compares standard output and standard error byte for byte.

**Items:** 7, 10, 18
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/cli/local_test.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/socket"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// agentCalls records what the fake agent or a fallback was asked; the
// socket serves each request on its own goroutine.
type agentCalls struct {
	mu  sync.Mutex
	got []string
}

func (c *agentCalls) add(format string, args ...any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.got = append(c.got, fmt.Sprintf(format, args...))
}

func (c *agentCalls) list() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.got)
}

// socketDir is a directory short enough for Unix socket paths.
func socketDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

// agentSocket serves the agent socket's handler over agent, as agent run
// serves it, for the enrolled host h1, and returns the socket's path.
func agentSocket(t *testing.T, agent *coretest.FakeAgent) string {
	t.Helper()
	if agent.PreflightFunc == nil {
		agent.PreflightFunc = func(_ context.Context, id string) (core.ModuleStatus, error) {
			return core.ModuleStatus{ID: id, Version: 1, Available: true}, nil
		}
	}
	path := filepath.Join(socketDir(t), "agent.sock")
	h := api.NewSocketHandler(api.Config{Agent: agent, Log: slog.New(slog.DiscardHandler)},
		api.SocketConfig{Identity: func() (string, bool) { return "h1", true }})
	srv, err := socket.Listen(socket.Config{Path: path, Handler: h, Allow: func(uint32) bool { return true }})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve() }()
	t.Cleanup(func() {
		_ = srv.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
	})
	return path
}

// staleSocket is a socket file in dir that nothing listens on, as a
// crashed agent leaves it: a dial is refused.
func staleSocket(t *testing.T, dir string) string {
	t.Helper()
	path := filepath.Join(dir, "stale.sock")
	ln, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		t.Fatal(err)
	}
	ln.SetUnlinkOnClose(false)
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLocalReadAndResource(t *testing.T) {
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		ReadFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (json.RawMessage, error) {
			rec.add("read %s/v%d %s", a.Name, a.Version, input)
			return json.RawMessage(`{"id":"i1","name":"first","tags":["a","b"]}`), nil
		},
		ResourceFunc: func(_ context.Context, _ core.Principal, name string) (json.RawMessage, error) {
			rec.add("resource %s", name)
			return json.RawMessage(`{"name":"web","port":443}`), nil
		},
	}
	cli := cliEnv{agentSocket: agentSocket(t, agent)}
	out, errOut, code := cli.run(t, "demo", "item", "get", "--id", "i1", "--local")
	if code != 0 || out != "id: i1\nname: first\ntags: a, b\n" {
		t.Errorf("read: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "item", "get", "--id", "i1", "--json", "--local")
	if code != 0 || out != `{"id":"i1","name":"first","tags":["a","b"]}`+"\n" {
		t.Errorf("read --json: exit %d, out %q, err %q", code, out, errOut)
	}
	out, errOut, code = cli.run(t, "demo", "rule", "get", "--local")
	if code != 0 || out != "name: web\nport: 443\n" {
		t.Errorf("resource: exit %d, out %q, err %q", code, out, errOut)
	}
	want := []string{`read demo.item.get/v2 {"id":"i1"}`, `read demo.item.get/v2 {"id":"i1"}`, "resource demo.rule"}
	if got := rec.list(); !slices.Equal(got, want) {
		t.Errorf("the agent saw %q, want %q", got, want)
	}
}

// The socket's plan and change are converted to the API's Plan and Change
// shapes, so a command prints the same with --local as through the
// controller API, with and without --json.
func TestLocalMutatingPrintsLikeTheAPI(t *testing.T) {
	plan := exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/plan",
		body:  `{"input":{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}}`,
		reply: planJSON("p1", "demo.item.add", "h1", "", `[{"id":"name-free","ok":true,"message":"the name is free"}]`)}
	apply := exchange{method: "POST", path: "/api/v1/hosts/h1/actions/demo.item.add/v1/apply", body: `{"planId":"p1"}`,
		reply: changeJSON("c1", "demo.item.add", "compensable", "completed",
			`"revertDeadline":null,"output":{"id":"i9","token":"tok-secret"},"error":null,"compensatedBy":"demo.item.remove"`)}
	base := newFakeAPI(t, testToken, hostList("h1:web"), plan, apply, hostList("h1:web"), plan, apply)
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		PlanFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
			if !jsonEqual(input, []byte(`{"name":"n1","apiKey":"key-1","passphrase":"pass-1"}`)) {
				t.Errorf("plan input %s", input)
			}
			rec.add("plan %s/v%d", a.Name, a.Version)
			return core.PlanView{PlanID: "p1", HostID: "h1", Action: "demo.item.add", Version: 1, InputHash: "ih",
				Diff:      []core.DiffLine{{Op: "+", Text: "add demo.item.add"}, {Op: " ", Text: "keep"}},
				Guards:    []core.Guard{{ID: "name-free", OK: true, Message: "the name is free"}},
				ExpiresAt: time.Date(2026, 10, 18, 12, 5, 0, 0, time.UTC)}, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			rec.add("apply %s %+v", planID, opts)
			return core.ChangeView{ID: "c1", HostID: "h1", Action: "demo.item.add", Actor: "token:t1", CompensatedBy: "demo.item.remove",
				Version: 1, Class: core.ClassCompensable, State: core.StateCompleted,
				CreatedAt: time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC), UpdatedAt: time.Date(2026, 10, 18, 12, 0, 1, 0, time.UTC),
				Output: json.RawMessage(`{"id":"i9","token":"tok-secret"}`)}, nil
		},
	}
	sock := agentSocket(t, agent)
	for _, extra := range [][]string{nil, {"--json"}} {
		args := slices.Concat([]string{"demo", "item", "add", "--name", "n1", "--yes"}, extra)
		apiOut, apiErr, apiCode := cliEnv{env: tokenEnv, stdin: "key-1\npass-1\n"}.run(t, slices.Concat(args, []string{"--url", base})...)
		out, errOut, code := cliEnv{stdin: "key-1\npass-1\n", agentSocket: sock}.run(t, slices.Concat(args, []string{"--local"})...)
		if apiCode != 0 || code != 0 || out != apiOut || errOut != apiErr {
			t.Errorf("%q: --local exit %d, out %q, err %q\nAPI exit %d, out %q, err %q", extra, code, out, errOut, apiCode, apiOut, apiErr)
		}
	}
	want := []string{"plan demo.item.add/v1", "apply p1 {ConfirmTimeout:0s Confirmation: ConfirmNow:false}"}
	if got := rec.list(); !slices.Equal(got, slices.Concat(want, want)) {
		t.Errorf("the agent saw %q", got)
	}
}

func TestLocalConfirmations(t *testing.T) {
	limits := filepath.Join(t.TempDir(), "limits.json")
	if err := os.WriteFile(limits, []byte(`{"limits":{"max":10}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := &agentCalls{}
	agent := &coretest.FakeAgent{
		PlanFunc: func(_ context.Context, _ core.Principal, a core.ActionRef, input json.RawMessage) (core.PlanView, error) {
			var in struct{ ID string }
			_ = json.Unmarshal(input, &in)
			// demo.item.remove is irreversible: its plan names the item id
			// as the confirmation phrase.
			return core.PlanView{PlanID: "p-" + in.ID, HostID: "h1", Action: a.Name, Version: a.Version, Confirmation: in.ID}, nil
		},
		ApplyFunc: func(_ context.Context, _ core.Principal, planID string, opts core.ApplyOptions) (core.ChangeView, error) {
			rec.add("apply %s %q %t", planID, opts.Confirmation, opts.ConfirmNow)
			if planID == "p-busy" {
				return core.ChangeView{}, errcode.New(errcode.PlanGuardFailed, "guard free failed: the item is in use")
			}
			return core.ChangeView{ID: "c1", HostID: "h1", Action: "demo", Version: 1, State: core.StateCompleted}, nil
		},
	}
	cli := cliEnv{agentSocket: agentSocket(t, agent)}
	tests := []struct {
		name    string
		args    []string
		code    int
		errPart string
		calls   []string
	}{
		{name: "irreversible without its phrase", args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			code: ExitRefused, errPart: "pass --confirmation i1"},
		{name: "irreversible with its phrase", args: []string{"demo", "item", "remove", "--id", "i1", "--yes", "--confirmation", "i1"},
			calls: []string{`apply p-i1 "i1" false`}},
		{name: "a refusal keeps its code", args: []string{"demo", "item", "remove", "--id", "busy", "--yes", "--confirmation", "busy"},
			code: ExitRefused, errPart: "darkpanel: guard free failed: the item is in use (plan.guard_failed)\n",
			calls: []string{`apply p-busy "busy" false`}},
		{name: "revertible confirmed at once",
			args:  []string{"demo", "rule", "set", "--name", "web", "--port", "443", "--input", limits, "--yes", "--confirm"},
			calls: []string{`apply p- "" true`}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := len(rec.list())
			out, errOut, code := cli.run(t, append(tt.args, "--local")...)
			if code != tt.code || !strings.Contains(errOut, tt.errPart) {
				t.Errorf("exit %d, out %q, err %q; want exit %d and an error containing %q", code, out, errOut, tt.code, tt.errPart)
			}
			if got := rec.list()[before:]; !slices.Equal(got, tt.calls) {
				t.Errorf("applies %q, want %q", got, tt.calls)
			}
		})
	}
}

// localTestCommands are hand-written commands as the assembly passes them;
// own has a persistent pre-run hook of its own, which cobra runs instead of
// the root's.
func localTestCommands(hookRan *bool) []*cobra.Command {
	version := &cobra.Command{Use: "version", RunE: func(*cobra.Command, []string) error { return nil }}
	own := &cobra.Command{Use: "own", PersistentPreRunE: func(*cobra.Command, []string) error {
		*hookRan = true
		return nil
	}}
	own.AddCommand(&cobra.Command{Use: "sub", RunE: func(*cobra.Command, []string) error { return nil }})
	return []*cobra.Command{version, own}
}

func TestLocalRefusals(t *testing.T) {
	missing := filepath.Join(socketDir(t), "missing")
	notAgent := func(path string) string {
		return "darkpanel: --local reaches only this host's agent; darkpanel " + path + " is not an agent-side command\n"
	}
	withHost := "darkpanel: --host does not apply with --local: the agent socket serves this host only\n"
	withURL := "darkpanel: --url does not apply with --local: the agent socket serves this host only\n"
	tests := []struct {
		args []string
		want string
	}{
		{[]string{"demo", "item", "get", "--id", "i1", "--host", "h1"}, withHost},
		{[]string{"demo", "rule", "get", "--host", "web"}, withHost},
		{[]string{"demo", "item", "get", "--id", "i1", "--url", "http://127.0.0.1:1"}, withURL},
		{[]string{"demo", "rule", "get", "--url", "http://127.0.0.1:1"}, withURL},
		{[]string{"host", "list"}, notAgent("host list")},
		{[]string{"token", "create", "--name", "ci", "--scope", "read", "--yes"}, notAgent("token create")},
		{[]string{"change", "list"}, notAgent("change list")},
		{[]string{"controller", "web-ca"}, notAgent("controller web-ca")},
		{[]string{"user", "create", "--username", "ann"}, notAgent("user create")},
		{[]string{"demo"}, notAgent("demo")},
		{[]string{"version"}, notAgent("version")},
		{[]string{"own", "sub"}, notAgent("own sub")},
	}
	for _, tt := range tests {
		hookRan := false
		cli := cliEnv{agentSocket: missing, commands: localTestCommands(&hookRan)}
		out, errOut, code := cli.run(t, append(tt.args, "--local")...)
		if code != ExitUsage || errOut != tt.want || out != "" || hookRan {
			t.Errorf("darkpanel %q --local: exit %d, out %q, err %q, own hook ran %t; want exit 2 and %q",
				tt.args, code, out, errOut, hookRan, tt.want)
		}
	}
	hookRan := false
	if _, errOut, code := (cliEnv{commands: localTestCommands(&hookRan)}).run(t, "own", "sub"); code != 0 || !hookRan {
		t.Errorf("own sub without --local: exit %d, err %q, hook ran %t", code, errOut, hookRan)
	}
}

func TestLocalLeavesHelpAndCompletion(t *testing.T) {
	for _, args := range [][]string{{"help"}, {"help", "demo", "item", "get"}, {"demo", "item", "get", "--help"}, {"completion", "bash"}} {
		out, errOut, code := cliEnv{}.run(t, args...)
		if code != 0 || out == "" || errOut != "" {
			t.Errorf("darkpanel %q: exit %d, out %d bytes, err %q", args, code, len(out), errOut)
		}
	}
	out, _, _ := cliEnv{}.run(t, "help", "demo", "item", "get")
	if !strings.Contains(out, "--local") {
		t.Errorf("demo item get's help does not show --local:\n%s", out)
	}
}

func TestLocalFallback(t *testing.T) {
	dir := socketDir(t)
	missing := filepath.Join(dir, "missing")
	stale := staleSocket(t, dir)
	tooLong := filepath.Join(dir, strings.Repeat("s", 120))
	notice := func(path, action string) string {
		return "darkpanel: the agent socket " + path + " does not answer; running " + action + " in this process\n"
	}
	tests := []struct {
		name     string
		sock     string
		terminal bool
		stdin    string
		args     []string
		code     int
		out      string
		errOut   string // a prefix of standard error
		calls    []string
	}{
		{name: "missing socket", sock: missing, args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			out: "removed: i1\n", errOut: notice(missing, "demo.item.remove"), calls: []string{`remove {"id":"i1"}`}},
		{name: "refused connection, --json", sock: stale, args: []string{"demo", "item", "remove", "--id", "i1", "--yes", "--json"},
			out: `{"removed":"i1"}` + "\n", errOut: notice(stale, "demo.item.remove"), calls: []string{`remove {"id":"i1"}`}},
		{name: "no --yes and no terminal", sock: missing, args: []string{"demo", "item", "remove", "--id", "i1"}, code: ExitRefused,
			errOut: notice(missing, "demo.item.remove") + "darkpanel: standard input is not a terminal; pass --yes to apply\n"},
		{name: "agreed on the terminal", sock: missing, terminal: true, stdin: "y\n", args: []string{"demo", "item", "remove", "--id", "i1"},
			out: "removed: i1\n", errOut: notice(missing, "demo.item.remove") + "Apply? [y/N] ", calls: []string{`remove {"id":"i1"}`}},
		{name: "declined on the terminal", sock: missing, terminal: true, stdin: "n\n", args: []string{"demo", "item", "remove", "--id", "i1"},
			code: ExitRefused, errOut: notice(missing, "demo.item.remove") + "Apply? [y/N] darkpanel: not applied\n"},
		{name: "a read asks nothing", sock: missing, args: []string{"demo", "item", "get", "--id", "i1", "--json"},
			out: `{"id":"i1"}` + "\n", errOut: notice(missing, "demo.item.get"), calls: []string{`get {"id":"i1"}`}},
		{name: "the fallback's error", sock: missing, args: []string{"demo", "item", "remove", "--id", "gone", "--yes"}, code: ExitError,
			errOut: notice(missing, "demo.item.remove") + "darkpanel: there is no previous binary (update.nothing_to_roll_back)\n",
			calls:  []string{`remove {"id":"gone"}`}},
		{name: "no fallback", sock: missing, args: []string{"demo", "item", "list"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + missing + " does not answer: dial unix " + missing + ": connect: no such file or directory\n"},
		{name: "a resource has no fallback", sock: missing, args: []string{"demo", "status", "get"}, code: ExitError,
			errOut: "darkpanel: the agent socket " + missing + " does not answer: "},
		{name: "another dial error runs no fallback", sock: tooLong, args: []string{"demo", "item", "remove", "--id", "i1", "--yes"},
			code: ExitError, errOut: "darkpanel: the agent socket " + tooLong + " does not answer: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := &agentCalls{}
			fallbacks := map[string]LocalFallback{
				"demo.item.remove": func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
					rec.add("remove %s", input)
					var in struct{ ID string }
					if err := json.Unmarshal(input, &in); err != nil {
						return nil, err
					}
					if in.ID == "gone" {
						return nil, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary")
					}
					return json.Marshal(map[string]string{"removed": in.ID})
				},
				"demo.item.get": func(_ context.Context, input json.RawMessage) (json.RawMessage, error) {
					rec.add("get %s", input)
					return json.RawMessage(`{"id":"i1"}`), nil
				},
			}
			cli := cliEnv{agentSocket: tt.sock, fallbacks: fallbacks, terminal: tt.terminal, stdin: tt.stdin}
			out, errOut, code := cli.run(t, append(tt.args, "--local")...)
			if code != tt.code || out != tt.out || !strings.HasPrefix(errOut, tt.errOut) {
				t.Errorf("exit %d, out %q, err %q; want exit %d, out %q, err starting %q", code, out, errOut, tt.code, tt.out, tt.errOut)
			}
			if got := rec.list(); !slices.Equal(got, tt.calls) {
				t.Errorf("fallback calls %q, want %q", got, tt.calls)
			}
		})
	}
}
```

In `internal/cli/root_test.go`, replace:

```go
	secrets  []string
	commands []*cobra.Command
}

```

with:

```go
	secrets  []string
	commands []*cobra.Command
	// agentSocket is the socket --local dials; fallbacks are its
	// LocalFallbacks.
	agentSocket string
	fallbacks   map[string]LocalFallback
}

```

In `internal/cli/root_test.go`, replace:

```go
	var out, errb bytes.Buffer
	root, err := NewRoot(Options{
		Registry: reg, Commands: c.commands,
		Stdin: strings.NewReader(c.stdin), Stdout: &out, Stderr: &errb,
		Getenv: envOf(c.env), UserHomeDir: homeAt(t.TempDir()),
```

with:

```go
	var out, errb bytes.Buffer
	root, err := NewRoot(Options{
		Registry: reg, Commands: c.commands, AgentSocket: c.agentSocket, LocalFallbacks: c.fallbacks,
		Stdin: strings.NewReader(c.stdin), Stdout: &out, Stderr: &errb,
		Getenv: envOf(c.env), UserHomeDir: homeAt(t.TempDir()),
```

In `internal/cli/errors_test.go`, replace:

```go
		{"refusal inside a run", ranError(refusedf("a guard failed")), ExitRefused},
		{"unmarked error from cobra", errors.New(`unknown flag: --bogus`), ExitUsage},
	}
	for _, c := range cases {
```

with:

```go
		{"refusal inside a run", ranError(refusedf("a guard failed")), ExitRefused},
		{"unmarked error from cobra", errors.New(`unknown flag: --bogus`), ExitUsage},
		{"ExitWith", ExitWith(ExitUsage, errors.New("not enrolled")), ExitUsage},
		{"ExitWith inside a run", ranError(fmt.Errorf("check: %w", ExitWith(ExitRefused, nil))), ExitRefused},
		{"ExitWith over a usage error", ExitWith(ExitError, usagef("bad flag")), ExitError},
	}
	for _, c := range cases {
```

In `internal/cli/errors_test.go`, replace:

```go
		t.Errorf("nil error printed %q", b.String())
	}
}

```

with:

```go
		t.Errorf("nil error printed %q", b.String())
	}
	PrintError(&b, ranError(ExitWith(ExitUsage, errors.New("the agent is not enrolled"))))
	if want := "darkpanel: the agent is not enrolled\n"; b.String() != want {
		t.Errorf("ExitWith(2, err) printed as %q, want %q", b.String(), want)
	}
	b.Reset()
	PrintError(&b, ranError(ExitWith(ExitError, nil)))
	if b.Len() != 0 {
		t.Errorf("ExitWith(1, nil) printed %q", b.String())
	}
}

```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/6 timeout 600 go test -count=1 -run 'TestLocal|TestExitCode|TestPrintError' ./internal/cli/`
Expected: FAIL with `internal/cli/errors_test.go:34:16: undefined: ExitWith`

- [ ] **Step 3: Write the implementation**

Create `internal/cli/local.go`:

```go
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// annotationLocal marks the generated agent-side commands, the only ones
// --local serves.
const annotationLocal = "darkpanel.local"

// guardLocal makes every command but the generated agent-side ones refuse
// --local. The check runs as root's persistent pre-run hook; cobra runs
// only the nearest such hook, so a hand-written command's own hook gets
// the check put before it.
func guardLocal(root *cobra.Command) {
	root.PersistentPreRunE = func(cmd *cobra.Command, _ []string) error { return refuseLocal(cmd) }
	var walk func(c *cobra.Command)
	walk = func(c *cobra.Command) {
		for _, sub := range c.Commands() {
			if hook, plain := sub.PersistentPreRunE, sub.PersistentPreRun; hook != nil || plain != nil {
				sub.PersistentPreRun = nil
				sub.PersistentPreRunE = func(cmd *cobra.Command, args []string) error {
					if err := refuseLocal(cmd); err != nil {
						return err
					}
					if hook != nil {
						return hook(cmd, args)
					}
					plain(cmd, args)
					return nil
				}
			}
			walk(sub)
		}
	}
	walk(root)
}

func refuseLocal(cmd *cobra.Command) error {
	if local, _ := cmd.Flags().GetBool("local"); !local || cmd.Annotations[annotationLocal] != "" {
		return nil
	}
	return usagef("--local reaches only this host's agent; %s is not an agent-side command", cmd.CommandPath())
}

// useLocal reports whether an agent-side command runs with --local, which
// leaves no host and no API to choose.
func useLocal(cmd *cobra.Command) (bool, error) {
	local, _ := cmd.Flags().GetBool("local")
	if !local {
		return false, nil
	}
	for _, flag := range []string{"host", "url"} {
		if cmd.Flags().Changed(flag) {
			return false, usagef("--%s does not apply with --local: the agent socket serves this host only", flag)
		}
	}
	return true, nil
}

// socketError is a failed dial of the agent socket.
type socketError struct {
	path string
	err  error
}

func (e *socketError) Error() string {
	return fmt.Sprintf("the agent socket %s does not answer: %v", e.path, e.err)
}

func (e *socketError) Unwrap() error { return e.err }

// absent reports whether no agent serves the socket: the path is missing,
// or nothing listens on it.
func (e *socketError) absent() bool {
	return errors.Is(e.err, syscall.ENOENT) || errors.Is(e.err, syscall.ECONNREFUSED)
}

func (a *app) dialAgent(ctx context.Context) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "unix", a.opts.AgentSocket)
	if err != nil {
		return nil, &socketError{path: a.opts.AgentSocket, err: err}
	}
	return c, nil
}

// withAgent runs run with a client of the agent socket. A dial first finds
// a socket no agent serves before anything is read or planned; then def's
// local fallback, if it has one, runs instead. def is nil for a resource,
// which has no fallback.
func (a *app) withAgent(cmd *cobra.Command, def *core.ActionDef, input json.RawMessage, secrets []string,
	run func(*api.Client) error) error {
	conn, err := a.dialAgent(cmd.Context())
	if err != nil {
		var down *socketError
		if errors.As(err, &down) && down.absent() && def != nil {
			if fallback, ok := a.opts.LocalFallbacks[def.Spec.Name]; ok {
				return a.runFallback(cmd, *def, fallback, input, secrets)
			}
		}
		return err
	}
	if err := conn.Close(); err != nil {
		return err
	}
	return run(api.NewClient(api.ClientConfig{Dial: a.dialAgent}))
}

// runFallback runs def in this process, after the same Apply? [y/N]
// question as the mutating flow; a read asks nothing.
func (a *app) runFallback(cmd *cobra.Command, def core.ActionDef, fallback LocalFallback, input json.RawMessage, secrets []string) error {
	_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "darkpanel: the agent socket %s does not answer; running %s in this process\n",
		sanitize(a.opts.AgentSocket), def.Spec.Name)
	if def.Spec.Class != core.ClassRead {
		if err := a.askApply(cmd, false); err != nil {
			return err
		}
	}
	out, err := fallback(cmd.Context(), input)
	if err != nil {
		return localError(err)
	}
	return a.show(cmd, nullOutput(out), secrets)
}

// The agent socket's caller is always the local root user, whatever
// principal the client sends, so the local calls send the zero principal.

func (a *app) localRead(cmd *cobra.Command, def core.ActionDef, input json.RawMessage, secrets []string) error {
	return a.withAgent(cmd, &def, input, secrets, func(c *api.Client) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
		defer cancel()
		out, err := c.Read(ctx, core.Principal{}, core.ActionRef{Name: def.Spec.Name, Version: def.Spec.Version}, input)
		if err != nil {
			return localError(err)
		}
		return a.show(cmd, nullOutput(out), secrets)
	})
}

func (a *app) localResource(cmd *cobra.Command, name string, secrets []string) error {
	return a.withAgent(cmd, nil, nil, secrets, func(c *api.Client) error {
		ctx, cancel := context.WithTimeout(cmd.Context(), requestTimeout)
		defer cancel()
		out, err := c.Resource(ctx, core.Principal{}, name)
		if err != nil {
			return localError(err)
		}
		return a.show(cmd, nullOutput(out), secrets)
	})
}

func (a *app) localMutating(cmd *cobra.Command, def core.ActionDef, input json.RawMessage, secrets []string) error {
	return a.withAgent(cmd, &def, input, secrets, func(c *api.Client) error {
		return a.applyAndShow(cmd, localCalls(c, def), input, secrets)
	})
}

// localCalls are def's plan and apply on the agent socket. They convert the
// agent's plan and change to the API's shapes, so the plan, the change and
// --json print as they do through the controller API.
func localCalls(c *api.Client, def core.ActionDef) actionCalls {
	ref := core.ActionRef{Name: def.Spec.Name, Version: def.Spec.Version}
	return actionCalls{
		plan: func(ctx context.Context, input any) (planWire, error) {
			raw, err := json.Marshal(input)
			if err != nil {
				return planWire{}, fmt.Errorf("encode the input: %w", err)
			}
			ctx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()
			v, err := c.Plan(ctx, core.Principal{}, ref, raw)
			if err != nil {
				return planWire{}, localError(err)
			}
			return planWire{PlanID: v.PlanID, HostID: optional(v.HostID), Action: v.Action, Version: v.Version,
				Diff: v.Diff, Guards: v.Guards, Confirmation: optional(v.Confirmation)}, nil
		},
		apply: func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error) {
			ctx, cancel := context.WithTimeout(ctx, requestTimeout)
			defer cancel()
			v, err := c.Apply(ctx, core.Principal{}, planID, core.ApplyOptions{Confirmation: confirmation, ConfirmNow: confirmNow})
			if err != nil {
				return changeWire{}, nil, localError(err)
			}
			ch := changeWire{ID: v.ID, HostID: optional(v.HostID), Action: v.Action, Version: v.Version, Class: v.Class,
				State: v.State, Actor: v.Actor, CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt, RevertDeadline: v.RevertDeadline,
				Output: nullOutput(v.Output), CompensatedBy: optional(v.CompensatedBy)}
			if v.Error != nil {
				ch.Error = &changeErrorWire{Code: v.Error.Code, Message: v.Error.Message}
			}
			raw, err := json.Marshal(ch)
			if err != nil {
				return changeWire{}, nil, fmt.Errorf("encode the change: %w", err)
			}
			return ch, raw, nil
		},
	}
}

// localError makes a coded error of the agent socket or of a fallback read
// as the API's refusal would, so its code chooses the exit code.
func localError(err error) error {
	var coded *errcode.Error
	if !errors.As(err, &coded) {
		return err
	}
	detail := coded.Detail
	if coded.Err != nil {
		detail += ": " + coded.Err.Error()
	}
	return &problemError{Status: coded.Code.Status(), Code: coded.Code, Detail: detail, Fields: coded.Fields}
}

func optional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// nullOutput is the output the API shows for none: JSON null.
func nullOutput(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage("null")
	}
	return raw
}
```

In `internal/cli/root.go`, replace:

```go
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/core"
)

```

with:

```go
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
)

```

In `internal/cli/root.go`, replace:

```go
	Adopt map[string][]string
	// LocalFallbacks run an agent-side action in-process when --local finds
	// no agent socket, keyed by action name. NewRoot checks the keys, but
	// nothing runs a fallback yet: the --local flag that does is not wired.
	LocalFallbacks map[string]LocalFallback
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	Getenv         func(string) string
	UserHomeDir    func() (string, error)
	// IsTerminal reports whether standard input is an interactive terminal.
	IsTerminal func() bool
```

with:

```go
	Adopt map[string][]string
	// LocalFallbacks run an agent-side action in-process when --local finds
	// no agent socket, keyed by action name. NewRoot checks the keys.
	LocalFallbacks map[string]LocalFallback
	// AgentSocket is the agent socket --local dials; empty means the
	// production path.
	AgentSocket string
	Stdin       io.Reader
	Stdout      io.Writer
	Stderr      io.Writer
	Getenv      func(string) string
	UserHomeDir func() (string, error)
	// IsTerminal reports whether standard input is an interactive terminal.
	IsTerminal func() bool
```

In `internal/cli/root.go`, replace:

```go
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	reserved := slices.Clone(handWritten)
	for _, c := range opts.Commands {
```

with:

```go
	root.SetOut(opts.Stdout)
	root.SetErr(opts.Stderr)
	root.PersistentFlags().Bool("local", false, "send an agent-side command to this host's agent socket instead of the controller API")
	reserved := slices.Clone(handWritten)
	for _, c := range opts.Commands {
```

In `internal/cli/root.go`, replace:

```go
	a.wrapEnrollment(root)
	markRuns(root)
	return root, nil
}
```

with:

```go
	a.wrapEnrollment(root)
	markRuns(root)
	guardLocal(root)
	return root, nil
}
```

In `internal/cli/root.go`, replace:

```go
	if o.UserHomeDir == nil {
		o.UserHomeDir = os.UserHomeDir
	}
	stdin, isFile := o.Stdin.(*os.File)
```

with:

```go
	if o.UserHomeDir == nil {
		o.UserHomeDir = os.UserHomeDir
	}
	if o.AgentSocket == "" {
		o.AgentSocket = paths.Production().AgentSocket
	}
	stdin, isFile := o.Stdin.(*os.File)
```

In `internal/cli/root.go`, replace:

```go
		Annotations: map[string]string{AnnotationAction: e.Name},
	}
	addInputFlags(cmd, fields)
	addConnFlags(cmd, def.Spec.BreakGlass, def.Spec.Side == core.SideAgent)
```

with:

```go
		Annotations: map[string]string{AnnotationAction: e.Name},
	}
	if def.Spec.Side == core.SideAgent {
		cmd.Annotations[annotationLocal] = "true"
	}
	addInputFlags(cmd, fields)
	addConnFlags(cmd, def.Spec.BreakGlass, def.Spec.Side == core.SideAgent)
```

In `internal/cli/read.go`, replace:

```go
}

// runRead runs a read action and shows its output.
func (a *app) runRead(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	c, err := a.connect(cmd)
```

with:

```go
}

// runRead runs a read action and shows its output; with --local the agent
// socket runs it.
func (a *app) runRead(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	local, err := useLocal(cmd)
	if err != nil {
		return err
	}
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	if local {
		return a.localRead(cmd, def, input, secrets)
	}
	c, err := a.connect(cmd)
```

In `internal/cli/read.go`, replace:

```go
		Short:       "Read the " + res.Name + " resource of a host",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationResource: e.Name},
	}
	addConnFlags(cmd, false, true)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		c, err := a.connect(cmd)
		if err != nil {
```

with:

```go
		Short:       "Read the " + res.Name + " resource of a host",
		Args:        cobra.NoArgs,
		Annotations: map[string]string{AnnotationResource: e.Name, annotationLocal: "true"},
	}
	addConnFlags(cmd, false, true)
	cmd.Flags().Bool("json", false, "print the raw JSON output")
	cmd.RunE = func(cmd *cobra.Command, _ []string) error {
		local, err := useLocal(cmd)
		if err != nil {
			return err
		}
		if local {
			return a.localResource(cmd, res.Name, secrets)
		}
		c, err := a.connect(cmd)
		if err != nil {
```

Replace the whole of `internal/cli/mutate.go` with:

```go
package cli

import (
	"context"
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

// changeWire is the API's Change. A change from the agent socket is
// converted to it, so --json prints the same object either way.
type changeWire struct {
	ID             string           `json:"id"`
	HostID         *string          `json:"hostId"`
	Action         string           `json:"action"`
	Version        int              `json:"version"`
	Class          core.Class       `json:"class"`
	State          string           `json:"state"`
	Actor          string           `json:"actor"`
	CreatedAt      time.Time        `json:"createdAt"`
	UpdatedAt      time.Time        `json:"updatedAt"`
	RevertDeadline *time.Time       `json:"revertDeadline"`
	Output         json.RawMessage  `json:"output"`
	Error          *changeErrorWire `json:"error"`
	CompensatedBy  *string          `json:"compensatedBy"`
}

type changeErrorWire struct {
	Code    string `json:"code"`
	Message string `json:"message"`
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

// runMutating runs the mutating flow of one action (spec section 15), on
// the agent socket with --local.
func (a *app) runMutating(cmd *cobra.Command, def core.ActionDef, fields []inputField, secrets []string) error {
	local, err := useLocal(cmd)
	if err != nil {
		return err
	}
	input, err := collectInput(cmd, fields, a.term)
	if err != nil {
		return err
	}
	if local {
		return a.localMutating(cmd, def, input, secrets)
	}
	c, err := a.connect(cmd)
	if err != nil {
		return err
	}
	host, err := hostFor(cmd, c, def.Spec.Side)
	if err != nil {
		return err
	}
	return a.applyAndShow(cmd, apiCalls(c, def, host), input, secrets)
}

// applyAndShow runs the mutating flow over calls and shows the change: with
// --json its raw JSON on standard output, else its state and output for
// people.
func (a *app) applyAndShow(cmd *cobra.Command, calls actionCalls, input any, secrets []string) error {
	ch, raw, err := a.planAndApply(cmd, calls, input, false)
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

// actionCalls are the plan and apply of one action, on the controller API
// or on the agent socket. apply returns the change and its JSON.
type actionCalls struct {
	plan  func(ctx context.Context, input any) (planWire, error)
	apply func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error)
}

// apiCalls are def's plan and apply on the API c reaches, under host for an
// agent-side action.
func apiCalls(c *client, def core.ActionDef, host string) actionCalls {
	return actionCalls{
		plan: func(ctx context.Context, input any) (planWire, error) {
			var plan planWire
			err := c.do(ctx, http.MethodPost, actionPath(def, host, "plan"), map[string]any{"input": input}, &plan)
			return plan, err
		},
		apply: func(ctx context.Context, planID, confirmation string, confirmNow bool) (changeWire, json.RawMessage, error) {
			body := map[string]any{"planId": planID}
			if confirmation != "" {
				body["confirmation"] = confirmation
			}
			if confirmNow {
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
		},
	}
}

// planAndApply plans with input, shows the plan, asks, and applies. A
// failing guard, a declined question or a wrong phrase refuses before
// anything is applied. The plan goes to standard output, or to standard
// error with --json. agreed skips the Apply? [y/N] question, as --yes
// does, for a step of a flow the operator already agreed to; it never
// skips the confirmation phrase.
func (a *app) planAndApply(cmd *cobra.Command, calls actionCalls, input any, agreed bool) (changeWire, json.RawMessage, error) {
	ctx := cmd.Context()
	info := cmd.OutOrStdout()
	if asJSON, _ := cmd.Flags().GetBool("json"); asJSON {
		info = cmd.ErrOrStderr()
	}
	plan, err := calls.plan(ctx, input)
	if err != nil {
		return changeWire{}, nil, err
	}
	printPlan(info, plan)
	for _, g := range plan.Guards {
		if !g.OK {
			return changeWire{}, nil, refusedf("guard %s failed: %s; nothing was applied", g.ID, g.Message)
		}
	}
	if err := a.askApply(cmd, agreed); err != nil {
		return changeWire{}, nil, err
	}
	var phrase string
	if plan.Confirmation != nil && *plan.Confirmation != "" {
		if phrase, err = a.confirmation(cmd, *plan.Confirmation); err != nil {
			return changeWire{}, nil, err
		}
	}
	now, _ := cmd.Flags().GetBool("confirm")
	return calls.apply(ctx, plan.PlanID, phrase, now)
}

// askApply asks Apply? [y/N] unless --yes or agreed; without a terminal it
// refuses, because only --yes can answer.
func (a *app) askApply(cmd *cobra.Command, agreed bool) error {
	if yes, _ := cmd.Flags().GetBool("yes"); yes || agreed {
		return nil
	}
	if !a.term.isTerm {
		return refusedf("standard input is not a terminal; pass --yes to apply")
	}
	answer, err := a.term.line("Apply? [y/N] ")
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if ans := strings.ToLower(strings.TrimSpace(answer)); ans != "y" && ans != "yes" {
		return refusedf("not applied")
	}
	return nil
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
	if _, err := fmt.Fprintf(w, "Change %s: %s\n", sanitize(ch.ID), sanitize(ch.State)); err != nil {
		return err
	}
	if ch.State == core.StatePending && ch.RevertDeadline != nil {
		if _, err := fmt.Fprintf(w, "Confirm it before %s with: darkpanel change confirm %s\nUnless confirmed by then, it is reverted.\n",
			ch.RevertDeadline.UTC().Format(time.RFC3339), sanitize(ch.ID)); err != nil {
			return err
		}
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

In `internal/cli/enroll.go`, replace:

```go
	}
	out := cmd.OutOrStdout()
	ch, _, err := a.planAndApply(cmd, c, def, "", input, false)
	if err != nil {
		return err
```

with:

```go
	}
	out := cmd.OutOrStdout()
	ch, _, err := a.planAndApply(cmd, apiCalls(c, def, ""), input, false)
	if err != nil {
		return err
```

In `internal/cli/enroll.go`, replace:

```go
		return err
	}
	act, _, err := a.planAndApply(cmd, c, activate, "", map[string]any{"username": username, "code": code}, true)
	if err != nil {
		return fmt.Errorf("%w; %s", err, retry)
```

with:

```go
		return err
	}
	act, _, err := a.planAndApply(cmd, apiCalls(c, activate, ""), map[string]any{"username": username, "code": code}, true)
	if err != nil {
		return fmt.Errorf("%w; %s", err, retry)
```

In `internal/cli/errors.go`, replace:

```go
}

// ExitCode is the process exit code for err, the error of a command tree
// that NewRoot built: 0 for nil, 2 for a usage error (including every error
// cobra raises while parsing the command line), 3 for a refused change or an
// API refusal with plan.guard_failed, 4 when the API refused with an auth.*
// code, and 1 for any other error.
func ExitCode(err error) int {
	var (
		usage   *usageError
		refused *refusedError
```

with:

```go
}

// exitError is an error with the exit code a hand-written command chose.
type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string {
	if e.err == nil {
		return ""
	}
	return e.err.Error()
}

func (e *exitError) Unwrap() error { return e.err }

// ExitWith returns an error that makes ExitCode answer code, also when it is
// wrapped; PrintError prints err, and nothing when err is nil, for a command
// whose exit status is its answer.
func ExitWith(code int, err error) error {
	return &exitError{code: code, err: err}
}

// ExitCode is the process exit code for err, the error of a command tree
// that NewRoot built: 0 for nil, the code of an ExitWith error, 2 for a
// usage error (including every error cobra raises while parsing the command
// line), 3 for a refused change or an API refusal with plan.guard_failed, 4
// when the API refused with an auth.* code, and 1 for any other error.
func ExitCode(err error) int {
	var (
		exit    *exitError
		usage   *usageError
		refused *refusedError
```

In `internal/cli/errors.go`, replace:

```go
	case err == nil:
		return ExitOK
	case errors.As(err, &usage):
		return ExitUsage
```

with:

```go
	case err == nil:
		return ExitOK
	case errors.As(err, &exit):
		return exit.code
	case errors.As(err, &usage):
		return ExitUsage
```

In `internal/cli/errors.go`, replace:

```go
}

// PrintError writes err as "darkpanel: <message>", followed by one indented
// line per field error of an API refusal, all sanitized. A nil err prints
// nothing.
func PrintError(w io.Writer, err error) {
	if err == nil {
		return
	}
```

with:

```go
}

// PrintError writes err as "darkpanel: <message>", followed by one indented
// line per field error of an API refusal, all sanitized. A nil err, or an
// ExitWith error without one, prints nothing.
func PrintError(w io.Writer, err error) {
	var exit *exitError
	if err == nil || errors.As(err, &exit) && exit.err == nil {
		return
	}
```

In `internal/cli/input.go`, replace:

```go
// reservedFlags are the flags the CLI defines itself; no input field may
// take one (generation fails instead).
var reservedFlags = []string{"url", "socket", "dev", "help", "json", "host", "input", "yes", "confirmation", "confirm"}

// terminal reads the operator's answers: from a terminal with prompts, or
```

with:

```go
// reservedFlags are the flags the CLI defines itself; no input field may
// take one (generation fails instead).
var reservedFlags = []string{"url", "socket", "dev", "help", "json", "host", "local", "input", "yes", "confirmation", "confirm"}

// terminal reads the operator's answers: from a terminal with prompts, or
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/6 timeout 600 go test -race -count=1 ./internal/cli/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/6 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/6 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/6 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/cli/local.go internal/cli/root.go internal/cli/read.go internal/cli/mutate.go internal/cli/enroll.go internal/cli/errors.go internal/cli/input.go internal/cli/local_test.go internal/cli/root_test.go internal/cli/errors_test.go
git commit -m "feat(cli): run agent commands on the agent socket"
```

---

### Task 7: Release workflow hardening

**Files:**
- Modify: `.github/workflows/release.yml`
- Modify: `Makefile`
- Modify: `release/README.md`
- Modify: `release/releasetool/main.go`
- Modify: `release/releasetool/versions.go`
- Modify: `release/sign.sh`
- Test: `release/releasetool/releasetool_test.go`
- Test: `release/sign.test.sh`
- Test: `scripts/gen-install-sh/main_test.go`

**Interfaces:**
- Consumes: the M2 release workflow plan's `release/releasetool` commands, `release/sign.sh` and `release.yml` jobs as they are on `main`; no name from an earlier task of this plan.
- Produces: C7. The `releasetool check-build` command now takes the release version first: `releasetool check-build <version> <key id>[,<key id>] < version.json` (its only caller is `release/sign.sh`). New command `releasetool latest <version> < release-list.json` prints `true` or `false`. New identifiers in `release/releasetool` (package `main`): `func readList(r io.Reader) ([]ghRelease, error)`, `func isLatest(list []ghRelease, v version) bool`, field `buildInfo.Version`, test `TestRunLatest`; `checkBuild` becomes `checkBuild(versionJSON []byte, version string, signing []string) error`. No other task touches these packages.

The eight hardening items and where each lands:

1. Stable release notes cover every commit since the previous stable release: the build job passes `--tag-pattern '^v[0-9]+\.[0-9]+\.[0-9]+$'` to `git-cliff --current` for a stable release, so earlier betas are not release boundaries; beta notes keep `cliff.toml`'s pattern and start at the previous tag.
2. `sign.sh` runs `sha256sum --check --strict --quiet checksums.txt` in the release directory before any key signs.
3. Every `actions/checkout` in `release.yml` sets `persist-credentials: false`. Nothing later in those jobs pushes; the verify job's `git fetch` of the tag reads the public repository anonymously.
4. `publish` asks `releasetool latest` and passes `--latest=true|false` to `gh release create`: only a stable version that no published stable release outranks becomes GitHub's Latest.
5. `VERSION` uses `git describe --tags --match 'v*'`, so the rolling `index` tag never names a dev build.
6. `release-prep` checks `command -v $(GIT_CLIFF)` before `snapshots.sh copy` freezes the snapshots.
7. `release/README.md` says to leave GitHub's immutable releases off, because `publish` clobbers `releases.json` on the published `index` release.
8. `sign.sh` passes the version it signs to `check-build`, which refuses a `version.json` whose `version` differs.

**Items:** 11, 36
**Depends on:** none
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 2 - spec 0 - coupling 1 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

In `release/releasetool/releasetool_test.go`, replace:

```go
	const a, b = "A000000000000000", "B000000000000000"
	tests := []struct {
		name string
		info string
		keys []string
		ok   bool
	}{
		{"one key", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a}, true},
		{"rotation in any order", `{"testBuild":false,"trustedKeys":["B000000000000000","A000000000000000"]}`, []string{a, b}, true},
		{"test build", `{"testBuild":true,"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"no testBuild field", `{"trustedKeys":["A000000000000000"]}`, []string{a}, false},
		{"binary trusts an extra key", `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`, []string{a}, false},
		{"signing key not trusted", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, []string{a, b}, false},
		{"no signing keys", `{"testBuild":false,"trustedKeys":[]}`, nil, false},
		{"not JSON", `darkpanel dev`, []string{a}, false},
	}
	for _, tt := range tests {
		if err := checkBuild([]byte(tt.info), tt.keys); (err == nil) != tt.ok {
			t.Errorf("%s: checkBuild = %v, want ok %v", tt.name, err, tt.ok)
		}
```

with:

```go
	const a, b = "A000000000000000", "B000000000000000"
	tests := []struct {
		name    string
		info    string
		version string
		keys    []string
		ok      bool
	}{
		{"one key", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, true},
		{"rotation in any order", `{"version":"1.2.3","testBuild":false,"trustedKeys":["B000000000000000","A000000000000000"]}`, "1.2.3", []string{a, b}, true},
		{"test build", `{"version":"1.2.3","testBuild":true,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"no testBuild field", `{"version":"1.2.3","trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary of another version", `{"version":"1.2.4","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary of the tag name", `{"version":"v1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"no version field", `{"testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a}, false},
		{"binary trusts an extra key", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`, "1.2.3", []string{a}, false},
		{"signing key not trusted", `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000"]}`, "1.2.3", []string{a, b}, false},
		{"no signing keys", `{"version":"1.2.3","testBuild":false,"trustedKeys":[]}`, "1.2.3", nil, false},
		{"not JSON", `darkpanel dev`, "1.2.3", []string{a}, false},
	}
	for _, tt := range tests {
		if err := checkBuild([]byte(tt.info), tt.version, tt.keys); (err == nil) != tt.ok {
			t.Errorf("%s: checkBuild = %v, want ok %v", tt.name, err, tt.ok)
		}
```

In `release/releasetool/releasetool_test.go`, replace:

```go

func TestRunCheckBuild(t *testing.T) {
	info := `{"testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`
	if err := run([]string{"check-build", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err != nil {
		t.Errorf("check-build of both keys = %v, want nil", err)
	}
	if err := run([]string{"check-build", ""}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of no keys = nil, want an error")
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"check-tag"}, {"index", "extra"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
```

with:

```go

func TestRunCheckBuild(t *testing.T) {
	info := `{"version":"1.2.3","testBuild":false,"trustedKeys":["A000000000000000","B000000000000000"]}`
	if err := run([]string{"check-build", "1.2.3", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err != nil {
		t.Errorf("check-build of both keys = %v, want nil", err)
	}
	if err := run([]string{"check-build", "1.2.3", ""}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of no keys = nil, want an error")
	}
	if err := run([]string{"check-build", "1.2.4", "A000000000000000,B000000000000000"}, strings.NewReader(info), &bytes.Buffer{}); err == nil {
		t.Error("check-build of another version = nil, want an error")
	}
}

func TestRunLatest(t *testing.T) {
	// gh release list order: the rolling index release, a draft of a newer
	// line, and betas of the next minor version.
	list := `[
  {"tagName": "index", "isDraft": false},
  {"tagName": "v1.5.0", "isDraft": true},
  {"tagName": "v1.4.0-beta.1", "isDraft": false},
  {"tagName": "v1.3.1", "isDraft": false},
  {"tagName": "v1.3.0", "isDraft": false},
  {"tagName": "v1.2.4", "isDraft": false}
]`
	tests := []struct {
		version string
		want    string
	}{
		{"1.3.2", "true\n"},
		{"1.4.0", "true\n"},
		{"2.0.0", "true\n"},
		{"1.2.5", "false\n"},
		{"1.3.1", "false\n"},
		{"1.4.0-beta.2", "false\n"},
		{"1.6.0-beta.1", "false\n"},
	}
	for _, tt := range tests {
		var out bytes.Buffer
		if err := run([]string{"latest", tt.version}, strings.NewReader(list), &out); err != nil || out.String() != tt.want {
			t.Errorf("latest %s = %q, %v; want %q", tt.version, out.String(), err, tt.want)
		}
	}

	var out bytes.Buffer
	if err := run([]string{"latest", "0.1.0"}, strings.NewReader("[]"), &out); err != nil || out.String() != "true\n" {
		t.Errorf("latest of the first release = %q, %v; want %q", out.String(), err, "true\n")
	}
	if err := run([]string{"latest", "v1.3.2"}, strings.NewReader(list), &bytes.Buffer{}); err == nil {
		t.Error("latest of a tag name = nil, want an error")
	}
	if err := run([]string{"latest", "1.3.2"}, strings.NewReader("not json"), &bytes.Buffer{}); err == nil {
		t.Error("latest of invalid JSON = nil, want an error")
	}
}

func TestRunUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"publish"}, {"check-tag"}, {"index", "extra"}, {"check-build", "A000000000000000"}, {"latest"}} {
		if err := run(args, strings.NewReader(""), &bytes.Buffer{}); err == nil || !strings.HasPrefix(err.Error(), "usage:") {
			t.Errorf("run(%q) = %v, want the usage error", args, err)
```

Replace the whole of `release/sign.test.sh` with:

```bash
#!/usr/bin/env bash
# Tests for release/sign.sh against a stub minisign. Run: bash release/sign.test.sh
set -euo pipefail

script="$(cd "$(dirname "$0")" && pwd)/sign.sh"
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
pass=0
fail=0

# expect NAME STATUS COMMAND...: runs COMMAND and checks its exit status.
expect() {
  local name=$1 want=$2 got=0
  shift 2
  "$@" >"$work/out" 2>&1 || got=$?
  if [ "$got" -eq "$want" ]; then
    pass=$((pass + 1))
  else
    echo "FAIL $name: exit $got, want $want" >&2
    cat "$work/out" >&2
    fail=$((fail + 1))
  fi
}

# The stub writes a hashed-mode signature file whose key id is the KEYID=
# line of the key file, stored little-endian as minisign does, and logs the
# signed file, the trusted comment and the password line it read. A key file
# with a FAIL line makes it fail.
cat >"$work/minisign" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
while [ "$#" -gt 0 ]; do
  case $1 in
  -s) key=$2; shift 2 ;;
  -m) msg=$2; shift 2 ;;
  -t) comment=$2; shift 2 ;;
  -x) out=$2; shift 2 ;;
  *) shift ;;
  esac
done
IFS= read -r password
if grep -qx FAIL "$key"; then
  echo "stub: refusing key" >&2
  exit 1
fi
id=$(sed -n 's/^KEYID=//p' "$key")
le=$(printf '%s' "$id" | sed 's/../&\n/g' | tac | tr -d '\n' | sed 's/../\\x&/g')
{
  echo "untrusted comment: stub signature"
  { printf 'ED'; printf '%b' "$le"; head -c 64 /dev/zero; } | base64 -w0
  echo
  echo "trusted comment: $comment"
  head -c 64 /dev/zero | base64 -w0
  echo
} >"$out"
echo "$msg|$comment|$password" >>"$STUB_LOG"
STUB
chmod +x "$work/minisign"

export MINISIGN="$work/minisign" STUB_LOG="$work/log"
unset MINISIGN_KEY_1 MINISIGN_PASSWORD_1 MINISIGN_KEY_2 MINISIGN_PASSWORD_2
key1="KEYID=0123456789ABCDEF"
key2="KEYID=00000000000000FF"

# trusts VERSION ID...: writes a version.json for a production build of
# VERSION trusting ID...
trusts() {
  local version=$1 ids
  shift
  ids=$(printf '"%s",' "$@")
  printf '{"version":"%s","testBuild":false,"trustedKeys":[%s]}\n' "$version" "${ids%,}" >"$work/version.json"
}

# fresh: a release directory holding one tarball and its checksums.txt, and
# no log.
fresh() {
  rm -rf "$work/rel" "$work/log"
  mkdir -p "$work/rel"
  printf 'amd64 tarball' >"$work/rel/darkpanel_1.2.3_linux_amd64.tar.gz"
  (cd "$work/rel" && sha256sum darkpanel_1.2.3_linux_amd64.tar.gz >checksums.txt)
}

sigs() { (cd "$work/rel" && find . -name '*.minisig' | LC_ALL=C sort | tr '\n' ' '); }

fresh
trusts 1.2.3 0123456789ABCDEF
expect one_key 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect one_key_file 0 test "$(sigs)" = "./checksums.txt.0123456789ABCDEF.minisig "
expect one_key_log 0 test "$(cat "$work/log" 2>/dev/null)" = "$work/rel/checksums.txt|version=1.2.3 channel=stable purpose=checksums|pw1"

fresh
trusts 1.3.0-beta.1 0123456789ABCDEF 00000000000000FF
expect two_keys 0 env MINISIGN_KEY_1="$key1" MINISIGN_PASSWORD_1=pw1 MINISIGN_KEY_2="$key2" MINISIGN_PASSWORD_2=pw2 \
  bash "$script" 1.3.0-beta.1 beta "$work/rel" "$work/version.json"
expect two_keys_files 0 test "$(sigs)" = "./checksums.txt.00000000000000FF.minisig ./checksums.txt.0123456789ABCDEF.minisig "
expect two_keys_log 0 grep -qx ".*|version=1.3.0-beta.1 channel=beta purpose=checksums|pw2" "$work/log"

fresh
trusts 1.2.3 0123456789ABCDEF
expect no_key 1 bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect no_key_no_files 0 test -z "$(sigs)"

fresh
trusts 1.2.3 0123456789ABCDEF
expect trust_mismatch 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect trust_mismatch_no_files 0 test -z "$(sigs)"

fresh
trusts 1.2.3 0123456789ABCDEF 00000000000000FF
expect second_key_fails 1 env MINISIGN_KEY_1="$key1" MINISIGN_KEY_2="$key2"$'\nFAIL' bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect second_key_fails_no_files 0 test -z "$(sigs)"

fresh
printf '{"version":"1.2.3","testBuild":true,"trustedKeys":["0123456789ABCDEF"]}\n' >"$work/version.json"
expect test_build 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect test_build_no_files 0 test -z "$(sigs)"

fresh
trusts 1.2.4 0123456789ABCDEF
expect other_version 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect other_version_no_files 0 test -z "$(sigs)"

fresh
trusts 1.2.3 0123456789ABCDEF
printf 'changed after packaging' >"$work/rel/darkpanel_1.2.3_linux_amd64.tar.gz"
expect checksum_mismatch 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect checksum_mismatch_no_files 0 test -z "$(sigs)"
expect checksum_mismatch_not_signed 0 test ! -e "$work/log"

fresh
trusts 1.2.3 0123456789ABCDEF
rm "$work/rel/darkpanel_1.2.3_linux_amd64.tar.gz"
expect checksum_file_missing 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 stable "$work/rel" "$work/version.json"
expect checksum_file_missing_not_signed 0 test ! -e "$work/log"

fresh
trusts 1.2.3 0123456789ABCDEF
expect usage 2 bash "$script" 1.2.3 stable "$work/rel"
expect bad_version 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2 stable "$work/rel" "$work/version.json"
expect bad_channel 1 env MINISIGN_KEY_1="$key1" bash "$script" 1.2.3 nightly "$work/rel" "$work/version.json"
expect bad_args_no_files 0 test -z "$(sigs)"

echo "sign tests: $pass passed, $fail failed"
[ "$fail" -eq 0 ]
```

`sign.sh` now checks the files that `checksums.txt` lists, so the end-to-end signing test in `scripts/gen-install-sh` needs a real file behind its checksum line (`crypto/sha256` and `encoding/hex` are already imported there).

In `scripts/gen-install-sh/main_test.go`, replace:

```go
	const version = "1.4.0"
	rel := filepath.Join(work, "rel")
	writeFile(t, filepath.Join(rel, "checksums.txt"), "abc  darkpanel_"+version+"_linux_amd64.tar.gz\n")
	info := filepath.Join(work, "version.json")
	writeFile(t, info, `{"version":"`+version+`","testBuild":false,"trustedKeys":["`+key.ID+`"]}`+"\n")
```

with:

```go
	const version = "1.4.0"
	rel := filepath.Join(work, "rel")
	// sign.sh checks every file checksums.txt lists before it signs.
	tarball := "darkpanel_" + version + "_linux_amd64.tar.gz"
	writeFile(t, filepath.Join(rel, tarball), "tarball\n")
	sum := sha256.Sum256([]byte("tarball\n"))
	writeFile(t, filepath.Join(rel, "checksums.txt"), hex.EncodeToString(sum[:])+"  "+tarball+"\n")
	info := filepath.Join(work, "version.json")
	writeFile(t, info, `{"version":"`+version+`","testBuild":false,"trustedKeys":["`+key.ID+`"]}`+"\n")
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 go test -count=1 -run 'TestCheckBuild|TestRunCheckBuild|TestRunLatest|TestRunUsage' ./release/releasetool/`
Expected: FAIL with `release/releasetool/releasetool_test.go:223:53: too many arguments in call to checkBuild`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 300 bash release/sign.test.sh`
Expected: FAIL with these lines (each followed by the script's output), then `sign tests: 18 passed, 7 failed`:
```
FAIL other_version: exit 0, want 1
FAIL other_version_no_files: exit 1, want 0
FAIL checksum_mismatch: exit 0, want 1
FAIL checksum_mismatch_no_files: exit 1, want 0
FAIL checksum_mismatch_not_signed: exit 1, want 0
FAIL checksum_file_missing: exit 0, want 1
FAIL checksum_file_missing_not_signed: exit 1, want 0
```

Run (the missing git-cliff freezes the snapshots today; the last command removes the directory it leaves, which must not be committed):
```bash
mkdir -p /root/.cache/plan6-tmp/7
GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 300 make release-prep VERSION=0.0.1 GIT_CLIFF=/root/.cache/plan6-tmp/7/missing-git-cliff
test ! -e schemas/released/v0.0.1 && echo clean || echo "snapshots frozen"
rm -rf schemas/released/v0.0.1
```
Expected: make fails with `bash: line 1: /root/.cache/plan6-tmp/7/missing-git-cliff: No such file or directory` and `Error 127`, then `snapshots frozen`.

Run (the rolling `index` tag, newer than every release tag, names the dev version today):
```bash
mk="$PWD/Makefile" c=/root/.cache/plan6-tmp/7/describe && rm -rf "$c" && git clone -q . "$c" && \
  git -C "$c" -c user.name=t -c user.email=t@t tag -a v0.0.0-test.1 -m t HEAD~2 && \
  git -C "$c" -c user.name=t -c user.email=t@t tag -a index -m index HEAD && \
  GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 make --no-print-directory -n -C "$c" -f "$mk" release-check; rm -rf "$c"
```
Expected: `go run ./release/releasetool check-version index`

- [ ] **Step 3: Write the implementation**

In `release/releasetool/main.go`, replace:

```go
// Command releasetool holds the release workflow's logic that can be tested
// locally: tag and version checks, the releases.json index, minisign key ids,
// and the check that a release binary is not a test build and trusts exactly
// the keys that sign it.
package main

```

with:

```go
// Command releasetool holds the release workflow's logic that can be tested
// locally: tag and version checks, the releases.json index, whether a release
// becomes GitHub's Latest, minisign key ids, and the check that a release
// binary is the version it is released as, is not a test build and trusts
// exactly the keys that sign it.
package main

```

In `release/releasetool/main.go`, replace:

```go
       releasetool check-version <version>
       releasetool index < release-list.json > releases.json
       releasetool keyid <file.minisig>
       releasetool check-build <key id>[,<key id>] < version.json`

func main() {
```

with:

```go
       releasetool check-version <version>
       releasetool index < release-list.json > releases.json
       releasetool latest <version> < release-list.json
       releasetool keyid <file.minisig>
       releasetool check-build <version> <key id>[,<key id>] < version.json`

func main() {
```

In `release/releasetool/main.go`, replace:

```go
		return err
	case cmd == "index" && len(rest) == 0:
		var list []ghRelease
		if err := json.NewDecoder(stdin).Decode(&list); err != nil {
			return fmt.Errorf("release list: %w", err)
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(buildIndex(list))
	case cmd == "keyid" && len(rest) == 1:
		sig, err := os.ReadFile(rest[0]) //nolint:gosec // G304: the signature file named on the command line
```

with:

```go
		return err
	case cmd == "index" && len(rest) == 0:
		list, err := readList(stdin)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(buildIndex(list))
	case cmd == "latest" && len(rest) == 1:
		v, err := parseVersion(rest[0])
		if err != nil {
			return err
		}
		list, err := readList(stdin)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(stdout, isLatest(list, v))
		return err
	case cmd == "keyid" && len(rest) == 1:
		sig, err := os.ReadFile(rest[0]) //nolint:gosec // G304: the signature file named on the command line
```

In `release/releasetool/main.go`, replace:

```go
		_, err = fmt.Fprintln(stdout, id)
		return err
	case cmd == "check-build" && len(rest) == 1:
		info, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		keys := strings.FieldsFunc(rest[0], func(r rune) bool { return r == ',' })
		return checkBuild(info, keys)
	}
	return errors.New(usage)
}

```

with:

```go
		_, err = fmt.Fprintln(stdout, id)
		return err
	case cmd == "check-build" && len(rest) == 2:
		info, err := io.ReadAll(stdin)
		if err != nil {
			return err
		}
		keys := strings.FieldsFunc(rest[1], func(r rune) bool { return r == ',' })
		return checkBuild(info, rest[0], keys)
	}
	return errors.New(usage)
}

func readList(r io.Reader) ([]ghRelease, error) {
	var list []ghRelease
	if err := json.NewDecoder(r).Decode(&list); err != nil {
		return nil, fmt.Errorf("release list: %w", err)
	}
	return list, nil
}

```

In `release/releasetool/main.go`, replace:

```go
// buildInfo is the part of `darkpanel version --json` the release checks.
type buildInfo struct {
	TestBuild   *bool    `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

// checkBuild refuses a binary built for tests, and one whose trusted keys
// differ from the keys signing the release: hosts running it could not
// verify the next release, or would trust a key the release does not use.
func checkBuild(versionJSON []byte, signing []string) error {
	var b buildInfo
	if err := json.Unmarshal(versionJSON, &b); err != nil {
```

with:

```go
// buildInfo is the part of `darkpanel version --json` the release checks.
type buildInfo struct {
	Version     string   `json:"version"`
	TestBuild   *bool    `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

// checkBuild refuses a binary built for tests, one built as another version
// than the release it ships in, and one whose trusted keys differ from the
// keys signing the release: hosts running it could not verify the next
// release, or would trust a key the release does not use. A binary of the
// wrong version would be installed as the signed version, and the updater's
// stepping and support checks would rely on a version it does not report.
func checkBuild(versionJSON []byte, version string, signing []string) error {
	var b buildInfo
	if err := json.Unmarshal(versionJSON, &b); err != nil {
```

In `release/releasetool/main.go`, replace:

```go
	if *b.TestBuild {
		return errors.New("the release binary is a test build")
	}
	if len(signing) == 0 {
```

with:

```go
	if *b.TestBuild {
		return errors.New("the release binary is a test build")
	}
	if b.Version != version {
		return fmt.Errorf("the release binary reports version %q, but the release is %q", b.Version, version)
	}
	if len(signing) == 0 {
```

In `release/releasetool/versions.go`, replace:

```go
	return idx
}
```

with:

```go
	return idx
}

// isLatest reports whether the release of v becomes the repository's Latest
// release on GitHub: a stable version that no published stable release
// outranks. GitHub marks each new publication Latest unless told otherwise,
// so a patch on an older line would otherwise displace the newest line.
func isLatest(list []ghRelease, v version) bool {
	if v.channel() != "stable" {
		return false
	}
	for _, r := range list {
		if r.IsDraft {
			continue
		}
		p, err := parseTag(r.TagName)
		if err != nil || p.channel() != "stable" {
			continue
		}
		if compare(p, v) >= 0 {
			return false
		}
	}
	return true
}
```

In `release/sign.sh`, replace:

```bash
#!/usr/bin/env bash
# Signs checksums.txt with every minisign key in the environment, then
# refuses the release unless the release binary is not a test build and
# trusts exactly those keys (spec section 9, ADR 0007). The sign job of
# release.yml runs it in the owner-approved release environment.
```

with:

```bash
#!/usr/bin/env bash
# Checks every file checksums.txt lists against it, signs checksums.txt with
# every minisign key in the environment, then refuses the release unless the
# release binary reports the version being signed, is not a test build and
# trusts exactly those keys (spec section 9, ADR 0007). The sign job of
# release.yml runs it in the owner-approved release environment.
```

In `release/sign.sh`, replace:

```bash
esac

comment="version=$version channel=$channel purpose=checksums"
# Signatures stay in $work until every key has signed and the build check
```

with:

```bash
esac

# A signature vouches for every file checksums.txt names, so no key signs
# until the files in the release directory match it.
if ! (cd "$dir" && sha256sum --check --strict --quiet checksums.txt) >&2; then
  echo "sign: the files in $dir do not match checksums.txt" >&2
  exit 1
fi

comment="version=$version channel=$channel purpose=checksums"
# Signatures stay in $work until every key has signed and the build check
```

In `release/sign.sh`, replace:

```bash
  exit 1
fi
"$work/releasetool" check-build "$(IFS=,; echo "${ids[*]}")" <"$info"
mv "$work"/sigs/*.minisig "$dir/"
echo "sign: checksums.txt signed by ${ids[*]}"
```

with:

```bash
  exit 1
fi
"$work/releasetool" check-build "$version" "$(IFS=,; echo "${ids[*]}")" <"$info"
mv "$work"/sigs/*.minisig "$dir/"
echo "sign: checksums.txt signed by ${ids[*]}"
```

In `Makefile`, replace:

```makefile

# Outside a git checkout (a source tarball) git fails and both fall back.
VERSION ?= $(or $(shell git describe --tags --always --dirty 2>/dev/null),dev)
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
ARCHES := amd64 arm64
```

with:

```makefile

# Outside a git checkout (a source tarball) git fails and both fall back.
# Only release tags name a version; the rolling index release's tag does not.
VERSION ?= $(or $(shell git describe --tags --match 'v*' --always --dirty 2>/dev/null),dev)
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
ARCHES := amd64 arm64
```

In `Makefile`, replace:

```makefile
# make release-prep VERSION=x.y.z freezes the schema snapshots and rewrites
# CHANGELOG.md; the owner commits both, then pushes a signed tag vx.y.z.
release-prep: release-check
	bash release/snapshots.sh copy v$(VERSION)
	$(GIT_CLIFF) --tag v$(VERSION) -o CHANGELOG.md
```

with:

```makefile
# make release-prep VERSION=x.y.z freezes the schema snapshots and rewrites
# CHANGELOG.md; the owner commits both, then pushes a signed tag vx.y.z.
# Released snapshots are immutable, so git-cliff is checked before they are frozen.
release-prep: release-check
	@command -v $(GIT_CLIFF) >/dev/null || { echo "release-prep: $(GIT_CLIFF) not found; install git-cliff 2.14.2 or set GIT_CLIFF" >&2; exit 1; }
	bash release/snapshots.sh copy v$(VERSION)
	$(GIT_CLIFF) --tag v$(VERSION) -o CHANGELOG.md
```

In `.github/workflows/release.yml`, replace:

```yaml
        with:
          fetch-depth: 0
      # No Go cache in release jobs: a tag run can restore a cache that
      # main-branch CI saved, and Go trusts cached modules and builds unchecked.
```

with:

```yaml
        with:
          fetch-depth: 0
          persist-credentials: false
      # No Go cache in release jobs: a tag run can restore a cache that
      # main-branch CI saved, and Go trusts cached modules and builds unchecked.
```

In `.github/workflows/release.yml`, replace:

```yaml
    env:
      VERSION: ${{ needs.verify.outputs.version }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
```

with:

```yaml
    env:
      VERSION: ${{ needs.verify.outputs.version }}
      CHANNEL: ${{ needs.verify.outputs.channel }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          fetch-depth: 0
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
```

In `.github/workflows/release.yml`, replace:

```yaml
          git diff --exit-code -- install.sh
          cp install.sh bin/release/
      - run: git-cliff --current --strip all -o bin/release-notes.md
      - uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
```

with:

```yaml
          git diff --exit-code -- install.sh
          cp install.sh bin/release/
      - name: Write the release notes
        run: |
          # Stable notes cover every commit since the previous stable release,
          # its betas' commits included; beta notes start at the previous tag.
          stable_only=()
          if [ "$CHANNEL" = stable ]; then stable_only=(--tag-pattern '^v[0-9]+\.[0-9]+\.[0-9]+$'); fi
          git-cliff --current --strip all "${stable_only[@]}" -o bin/release-notes.md
      - uses: actions/attest-build-provenance@4d101475d8b20a2381f78447822ac1eab6504dd8 # v4.2.2
        with:
```

In `.github/workflows/release.yml`, replace:

```yaml
      CHANNEL: ${{ needs.verify.outputs.channel }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
```

with:

```yaml
      CHANNEL: ${{ needs.verify.outputs.channel }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
```

In `.github/workflows/release.yml`, replace:

```yaml
      GH_REPO: ${{ github.repository }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
```

with:

```yaml
      GH_REPO: ${{ github.repository }}
    steps:
      - uses: actions/checkout@3d3c42e5aac5ba805825da76410c181273ba90b1 # v7.0.1
        with:
          persist-credentials: false
      - uses: actions/setup-go@b7ad1dad31e06c5925ef5d2fc7ad053ef454303e # v7.0.0
        with:
          go-version-file: go.mod
```

In `.github/workflows/release.yml`, replace:

```yaml
            prerelease=()
            if [ "$CHANNEL" = beta ]; then prerelease=(--prerelease); fi
            gh release create "$GITHUB_REF_NAME" dist/release/* --verify-tag \
              --title "Darkpanel $VERSION" --notes-file dist/release-notes.md "${prerelease[@]}"
          fi
      - name: Regenerate releases.json
```

with:

```yaml
            prerelease=()
            if [ "$CHANNEL" = beta ]; then prerelease=(--prerelease); fi
            # GitHub marks every new release Latest unless told otherwise; a
            # patch on an older line must not displace the newest stable one.
            gh release list --exclude-drafts --limit 1000 --json tagName,isDraft >"$RUNNER_TEMP/published.json"
            latest=$(go run ./release/releasetool latest "$VERSION" <"$RUNNER_TEMP/published.json")
            gh release create "$GITHUB_REF_NAME" dist/release/* --verify-tag \
              --title "Darkpanel $VERSION" --notes-file dist/release-notes.md "${prerelease[@]}" --latest="$latest"
          fi
      - name: Regenerate releases.json
```

In `release/README.md`, replace:

```markdown
  `main`, and that `schemas/released/<tag>/` equals the current schemas.
- `build` runs `make release` (tarballs, SBOM, `checksums.txt`), checks the
  committed `install.sh` is current, writes the release notes with git-cliff,
  and attests every file.
- `sign` (environment `release`) verifies the attestations against this
  repository and the tag's commit, checks the amd64 binary is not a test build
  and trusts exactly the signing keys, and signs `checksums.txt` with each key
  as `checksums.txt.<key id>.minisig`.
- `publish` creates the GitHub Release (a prerelease for a beta version) and
  regenerates `releases.json` on the rolling `index` release.

```

with:

```markdown
  `main`, and that `schemas/released/<tag>/` equals the current schemas.
- `build` runs `make release` (tarballs, SBOM, `checksums.txt`), checks the
  committed `install.sh` is current, writes the release notes with git-cliff
  (a stable release's notes cover every commit since the previous stable
  release, a beta's since the previous tag), and attests every file.
- `sign` (environment `release`) verifies the attestations against this
  repository and the tag's commit, checks every file against `checksums.txt`,
  checks the amd64 binary reports the tag's version, is not a test build and
  trusts exactly the signing keys, and signs `checksums.txt` with each key as
  `checksums.txt.<key id>.minisig`.
- `publish` creates the GitHub Release (a prerelease for a beta version; not
  marked Latest when a higher stable release is already published) and
  regenerates `releases.json` on the rolling `index` release.

```

In `release/README.md`, replace:

```markdown
delete that draft by hand before the re-run.

## Rotating the signing key

```

with:

```markdown
delete that draft by hand before the re-run.

Leave GitHub's immutable releases setting off for this repository: every
`publish` replaces `releases.json` on the published `index` release with
`gh release upload --clobber`, and an immutable release's files can be
neither replaced nor deleted.

## Rotating the signing key

```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 go test -race -count=1 ./release/releasetool/ ./scripts/gen-install-sh/`
Expected: PASS (`ok` for both packages)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 make test-release`
Expected: `snapshots tests: 13 passed, 0 failed`, `package tests: 14 passed, 0 failed`, `sign tests: 25 passed, 0 failed`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 120 make lint-sh`
Expected: only the echoed `shellcheck release/*.sh install.sh`, exit 0.

Run:
```bash
GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 300 make release-prep VERSION=0.0.1 GIT_CLIFF=/root/.cache/plan6-tmp/7/missing-git-cliff
test ! -e schemas/released/v0.0.1 && echo clean || echo "snapshots frozen"
```
Expected: `release-prep: /root/.cache/plan6-tmp/7/missing-git-cliff not found; install git-cliff 2.14.2 or set GIT_CLIFF`, `make: *** [Makefile:107: release-prep] Error 1`, then `clean`.

Run: the clone command of Step 2 again.
Expected: `go run ./release/releasetool check-version v0.0.0-test.1-2-g<short hash of HEAD>`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12 .github/workflows/release.yml .github/workflows/ci.yml`
Expected: no output, exit 0.

Run: `grep -E '^\s*-? *uses: ' .github/workflows/release.yml | grep -vE '@[0-9a-f]{40} # v[0-9]'`
Expected: no output (every action stays pinned by commit SHA).

Run: `grep -c 'persist-credentials: false' .github/workflows/release.yml`
Expected: `4` (one per `actions/checkout`).

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 go build ./...`
Expected: no output.

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 600 go vet ./...`
Expected: no output.

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/7 go mod tidy -diff`
Expected: no output.

- [ ] **Step 5: Commit**

```bash
git add .github/workflows/release.yml Makefile release/README.md release/releasetool/main.go release/releasetool/releasetool_test.go release/releasetool/versions.go release/sign.sh release/sign.test.sh scripts/gen-install-sh/main_test.go
git commit -m "fix(release): harden the release workflow"
```

---

### Task 8: Harness VM private link and port forwarding

**Files:**
- Modify: `test/harness/vm/seed.go`
- Modify: `test/harness/vm/vm.go`
- Test: `test/harness/vm/seed_test.go`
- Test: `test/harness/vm/vm_test.go`

**Interfaces:**
- Consumes: the existing `test/harness/vm` package: `Config`, `McastGroup`, `(*VM).sshClient`, `(*VM).dropClient`, and the test fixtures `testServer`, `sshTestServer`, `vmOf`, `wantKeys`, `first`.
- Produces: C8: `vm.Config.PrivMAC string`, `vm.Config.PrivAddrs []string`, `vm.Config.PrivMcastPort int`; `func (v *VM) DialContext(ctx context.Context, network, addr string) (net.Conn, error)`; the guest interface name `dppriv`. New identifiers in package `vm`: const `privIface`; test fixture field `sshTestServer.forwards chan string` and method `func (s *sshTestServer) forward(nc ssh.NewChannel)` (the fixture now forwards direct-tcpip channels to their target on 127.0.0.1); tests `TestBootRefusesOneLinkTwice`, `TestDialContextReachesAGuestListener`, `TestDialContextReconnectsAfterATimedOutDial`.

**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

In `test/harness/vm/seed_test.go`, replace:

```go
	cfg := Config{
		Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
		InetAddrs: []string{"198.18.0.10/15", "2001:db8:d0::10/64"},
		Provision: "#!/bin/bash\necho '<ok>' && true\n", Key: key,
	}
	s, err := seedFiles(cfg, hostKey)
```

with:

```go
	cfg := Config{
		Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
		InetAddrs: []string{"198.18.0.10/15", "2001:db8:d0::10/64"},
		PrivMAC:   "52:54:00:d0:02:10", PrivAddrs: []string{"10.77.0.10/24"}, PrivMcastPort: 23457,
		Provision: "#!/bin/bash\necho '<ok>' && true\n", Key: key,
	}
	s, err := seedFiles(cfg, hostKey)
```

In `test/harness/vm/seed_test.go`, replace:

```go
	if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
		t.Fatal(err)
	}
	mgmt, inet := network.Ethernets["dpmgmt"], network.Ethernets["dpinet"]
	if network.Version != 2 || len(network.Ethernets) != 2 ||
		mgmt.Match.MAC != cfg.MgmtMAC || mgmt.SetName != "dpmgmt" || !mgmt.DHCP4 || len(mgmt.Addresses) != 0 ||
		inet.Match.MAC != cfg.InetMAC || inet.SetName != "dpinet" || inet.DHCP4 || inet.DHCP6 || inet.AcceptRA ||
		!slices.Equal(inet.Addresses, cfg.InetAddrs) {
		t.Errorf("network-config = %s", s.NetworkConfig)
	}
}

// TestSeedFilesKeyNames decodes the seed into generic values, so a wrong
// JSON tag on the seed types, which TestSeedFiles decodes back unchanged,
// shows as a key cloud-init or netplan does not read.
func TestSeedFilesKeyNames(t *testing.T) {
	key, err := NewKey()
	if err != nil {
```

with:

```go
	if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
		t.Fatal(err)
	}
	mgmt, inet, priv := network.Ethernets["dpmgmt"], network.Ethernets["dpinet"], network.Ethernets["dppriv"]
	if network.Version != 2 || len(network.Ethernets) != 3 ||
		mgmt.Match.MAC != cfg.MgmtMAC || mgmt.SetName != "dpmgmt" || !mgmt.DHCP4 || len(mgmt.Addresses) != 0 ||
		inet.Match.MAC != cfg.InetMAC || inet.SetName != "dpinet" || inet.DHCP4 || inet.DHCP6 || inet.AcceptRA ||
		!slices.Equal(inet.Addresses, cfg.InetAddrs) ||
		priv.Match.MAC != cfg.PrivMAC || priv.SetName != "dppriv" || priv.DHCP4 || priv.DHCP6 || priv.AcceptRA ||
		!slices.Equal(priv.Addresses, cfg.PrivAddrs) {
		t.Errorf("network-config = %s", s.NetworkConfig)
	}
}

// TestSeedFilesKeyNames decodes the seed into generic values, so a wrong
// JSON tag on the seed types, which TestSeedFiles decodes back unchanged,
// shows as a key cloud-init or netplan does not read. Without a private link
// port the network config has no dppriv.
func TestSeedFilesKeyNames(t *testing.T) {
	key, err := NewKey()
	if err != nil {
```

In `test/harness/vm/seed_test.go`, replace:

```go
	if err != nil {
		t.Fatal(err)
	}
	cfg := Config{
		Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
		InetAddrs: []string{"198.18.0.10/15"}, Provision: "#!/bin/bash\n", Key: key,
	}
	s, err := seedFiles(cfg, hostKey)
	if err != nil {
		t.Fatal(err)
	}
	var user, network any
	if err := json.Unmarshal(bytes.TrimPrefix(s.UserData, []byte("#cloud-config\n")), &user); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
		t.Fatal(err)
	}
	u := wantKeys(t, "user-data", user, "hostname", "users", "ssh_pwauth", "ssh_deletekeys", "ssh_keys", "write_files", "runcmd")
	wantKeys(t, "users[0]", first(t, "users", u["users"]), "name", "sudo", "shell", "lock_passwd", "ssh_authorized_keys")
	wantKeys(t, "ssh_keys", u["ssh_keys"], "ed25519_private", "ed25519_public")
	wantKeys(t, "write_files[0]", first(t, "write_files", u["write_files"]), "path", "permissions", "content")
	n := wantKeys(t, "network-config", network, "version", "ethernets")
	eth := wantKeys(t, "ethernets", n["ethernets"], "dpmgmt", "dpinet")
	mgmt := wantKeys(t, "dpmgmt", eth["dpmgmt"], "match", "set-name", "dhcp4", "dhcp6", "accept-ra")
	inet := wantKeys(t, "dpinet", eth["dpinet"], "match", "set-name", "dhcp4", "dhcp6", "accept-ra", "addresses")
	wantKeys(t, "dpmgmt match", mgmt["match"], "macaddress")
	wantKeys(t, "dpinet match", inet["match"], "macaddress")
}

// wantKeys checks that v is a JSON object with exactly keys and returns it.
```

with:

```go
	if err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name   string
		priv   bool
		static []string // the NICs with static addresses, besides dpmgmt
	}{
		{"internet only", false, []string{"dpinet"}},
		{"private link", true, []string{"dpinet", "dppriv"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Config{
				Name: "target", MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
				InetAddrs: []string{"198.18.0.10/15"}, Provision: "#!/bin/bash\n", Key: key,
			}
			if c.priv {
				cfg.PrivMAC, cfg.PrivAddrs, cfg.PrivMcastPort = "52:54:00:d0:02:10", []string{"10.77.0.10/24"}, 23457
			}
			s, err := seedFiles(cfg, hostKey)
			if err != nil {
				t.Fatal(err)
			}
			var user, network any
			if err := json.Unmarshal(bytes.TrimPrefix(s.UserData, []byte("#cloud-config\n")), &user); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(s.NetworkConfig, &network); err != nil {
				t.Fatal(err)
			}
			u := wantKeys(t, "user-data", user, "hostname", "users", "ssh_pwauth", "ssh_deletekeys", "ssh_keys", "write_files", "runcmd")
			wantKeys(t, "users[0]", first(t, "users", u["users"]), "name", "sudo", "shell", "lock_passwd", "ssh_authorized_keys")
			wantKeys(t, "ssh_keys", u["ssh_keys"], "ed25519_private", "ed25519_public")
			wantKeys(t, "write_files[0]", first(t, "write_files", u["write_files"]), "path", "permissions", "content")
			n := wantKeys(t, "network-config", network, "version", "ethernets")
			eth := wantKeys(t, "ethernets", n["ethernets"], append([]string{"dpmgmt"}, c.static...)...)
			mgmt := wantKeys(t, "dpmgmt", eth["dpmgmt"], "match", "set-name", "dhcp4", "dhcp6", "accept-ra")
			wantKeys(t, "dpmgmt match", mgmt["match"], "macaddress")
			for _, name := range c.static {
				nic := wantKeys(t, name, eth[name], "match", "set-name", "dhcp4", "dhcp6", "accept-ra", "addresses")
				wantKeys(t, name+" match", nic["match"], "macaddress")
			}
		})
	}
}

// wantKeys checks that v is a JSON object with exactly keys and returns it.
```

In `test/harness/vm/vm_test.go`, replace:

```go

import (
	"context"
	"fmt"
	"net"
	"os/exec"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
```

with:

```go

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
```

In `test/harness/vm/vm_test.go`, replace:

```go
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:20",
			},
		},
	}
	for _, c := range cases {
		if got := qemuArgs(c.cfg, c.overlay, c.seed, c.port); !slices.Equal(got, c.want) {
```

with:

```go
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:20",
			},
		},
		{
			Config{Name: "controller", Dir: "/run/h", MemoryMiB: 2048, CPUs: 2,
				MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", McastPort: 23456,
				PrivMAC: "52:54:00:d0:02:10", PrivMcastPort: 23457},
			"/run/h/controller.qcow2", "/run/h/controller-seed.img", 40022,
			[]string{
				"-name", "controller", "-machine", "q35,accel=kvm", "-cpu", "host", "-smp", "2", "-m", "2048",
				"-display", "none", "-monitor", "none", "-serial", "file:/run/h/controller-console.log",
				"-drive", "file=/run/h/controller.qcow2,if=virtio,format=qcow2",
				"-drive", "file=/run/h/controller-seed.img,if=virtio,format=raw,readonly=on",
				"-netdev", "user,id=mgmt,hostfwd=tcp:127.0.0.1:40022-:22",
				"-device", "virtio-net-pci,netdev=mgmt,mac=52:54:00:d0:01:10",
				"-netdev", "socket,id=inet,mcast=230.0.0.1:23456",
				"-device", "virtio-net-pci,netdev=inet,mac=52:54:00:d0:00:10",
				"-netdev", "socket,id=priv,mcast=230.0.0.1:23457",
				"-device", "virtio-net-pci,netdev=priv,mac=52:54:00:d0:02:10",
			},
		},
	}
	for _, c := range cases {
		if got := qemuArgs(c.cfg, c.overlay, c.seed, c.port); !slices.Equal(got, c.want) {
```

In `test/harness/vm/vm_test.go`, replace:

```go
	}
}

// TestConnectGivesUpOnASilentPeer: a peer that accepts TCP and never speaks
// SSH must not hold connect past its context.
func TestConnectGivesUpOnASilentPeer(t *testing.T) {
```

with:

```go
	}
}

// TestBootRefusesOneLinkTwice: a private link on the internet link's
// multicast port would join both NICs to one link, so Boot refuses it before
// it creates any file.
func TestBootRefusesOneLinkTwice(t *testing.T) {
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(t.TempDir(), "run")
	cfg := Config{
		Name: "controller", Dir: dir, Image: "missing.qcow2", MemoryMiB: 2048, CPUs: 2,
		MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", McastPort: 23456,
		PrivMAC: "52:54:00:d0:02:10", PrivMcastPort: 23456, Key: key,
	}
	_, err = Boot(context.Background(), cfg)
	if err == nil || !strings.Contains(err.Error(), "multicast port 23456") {
		t.Errorf("Boot with one multicast port for both links: %v, want a refusal naming port 23456", err)
	}
	if _, serr := os.Stat(dir); !errors.Is(serr, fs.ErrNotExist) {
		t.Errorf("Boot created %s before refusing the config (%v)", dir, serr)
	}
}

// TestConnectGivesUpOnASilentPeer: a peer that accepts TCP and never speaks
// SSH must not hold connect past its context.
func TestConnectGivesUpOnASilentPeer(t *testing.T) {
```

In `test/harness/vm/vm_test.go`, replace:

```go

// sshTestServer is an SSH server on 127.0.0.1 with no client authentication.
type sshTestServer struct {
	addr    string
	hostKey ssh.PublicKey
	opens   chan int32 // the connection number of each session request, as it arrives
}

// testServer starts an sshTestServer. reply gives the output of each exec
// request, with n counting connections from 1; ok false leaves the request
// unanswered, so the command hangs. Unless acceptFirst, the first connection
// never answers a session request, as a stuck connection does.
func testServer(t *testing.T, acceptFirst bool, reply func(n int32, cmd string) (out string, ok bool)) *sshTestServer {
	t.Helper()
	hostKey, err := NewKey()
```

with:

```go

// sshTestServer is an SSH server on 127.0.0.1 with no client authentication.
type sshTestServer struct {
	addr     string
	hostKey  ssh.PublicKey
	opens    chan int32  // the connection number of each session request, as it arrives
	forwards chan string // the target of each direct-tcpip channel it forwards
}

// testServer starts an sshTestServer. reply gives the output of each exec
// request, with n counting connections from 1; ok false leaves the request
// unanswered, so the command hangs. Unless acceptFirst, the first connection
// never answers a session or direct-tcpip request, as a stuck connection
// does. A direct-tcpip channel is forwarded to its target on this host.
func testServer(t *testing.T, acceptFirst bool, reply func(n int32, cmd string) (out string, ok bool)) *sshTestServer {
	t.Helper()
	hostKey, err := NewKey()
```

In `test/harness/vm/vm_test.go`, replace:

```go
	if err != nil {
		t.Fatal(err)
	}
	s := &sshTestServer{addr: ln.Addr().String(), hostKey: hostKey.PublicKey(), opens: make(chan int32, 16)}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
```

with:

```go
	if err != nil {
		t.Fatal(err)
	}
	s := &sshTestServer{
		addr: ln.Addr().String(), hostKey: hostKey.PublicKey(),
		opens: make(chan int32, 16), forwards: make(chan string, 16),
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
```

In `test/harness/vm/vm_test.go`, replace:

```go
					if n == 1 && !acceptFirst {
						continue // neither accepted nor rejected: NewSession waits
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
```

with:

```go
					if n == 1 && !acceptFirst {
						continue // neither accepted nor rejected: NewSession waits
					}
					if nc.ChannelType() == "direct-tcpip" {
						go s.forward(nc)
						continue
					}
					ch, creqs, err := nc.Accept()
					if err != nil {
						continue
```

In `test/harness/vm/vm_test.go`, replace:

```go
	return s
}

// vmOf returns a VM that dials s, connected.
func vmOf(ctx context.Context, t *testing.T, s *sshTestServer, name string) *VM {
	t.Helper()
```

with:

```go
	return s
}

// forward connects a direct-tcpip channel to its target (RFC 4254 section
// 7.2), or rejects it when the target refuses.
func (s *sshTestServer) forward(nc ssh.NewChannel) {
	var req struct {
		Host       string
		Port       uint32
		OriginHost string
		OriginPort uint32
	}
	if err := ssh.Unmarshal(nc.ExtraData(), &req); err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	target := net.JoinHostPort(req.Host, strconv.FormatUint(uint64(req.Port), 10))
	select {
	case s.forwards <- target:
	default:
	}
	tc, err := net.DialTimeout("tcp", target, 5*time.Second)
	if err != nil {
		_ = nc.Reject(ssh.ConnectionFailed, err.Error())
		return
	}
	ch, reqs, err := nc.Accept()
	if err != nil {
		_ = tc.Close()
		return
	}
	go ssh.DiscardRequests(reqs)
	go func() {
		_, _ = io.Copy(ch, tc)
		_ = ch.CloseWrite()
	}()
	_, _ = io.Copy(tc, ch)
	_ = tc.Close()
	_ = ch.Close()
}

// vmOf returns a VM that dials s, connected.
func vmOf(ctx context.Context, t *testing.T, s *sshTestServer, name string) *VM {
	t.Helper()
```

In `test/harness/vm/vm_test.go`, replace:

```go
	}
}

// TestTimedOutRunKeepsAReplacement: a Run that times out on a stuck
// connection must close that connection, not one a concurrent reconnect put
// in its place.
```

with:

```go
	}
}

// TestDialContextReachesAGuestListener: the harness's HTTP client reaches a
// listener on the guest's loopback interface through the SSH connection, and
// a target that refuses fails the dial without dropping the connection.
func TestDialContextReachesAGuestListener(t *testing.T) {
	guest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(guest.Close)
	s := testServer(t, true, func(int32, string) (string, bool) { return "", true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "controller")
	transport := &http.Transport{DialContext: v.DialContext}
	t.Cleanup(transport.CloseIdleConnections)
	client := &http.Client{Transport: transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, guest.URL+"/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if err != nil || resp.StatusCode != http.StatusOK || string(body) != "ok" {
		t.Errorf("GET /healthz through the VM = %s %q, %v", resp.Status, body, err)
	}
	select {
	case got := <-s.forwards:
		if want := guest.Listener.Addr().String(); got != want {
			t.Errorf("the SSH server forwarded to %s, want %s", got, want)
		}
	default:
		t.Error("the request did not go through a direct-tcpip channel")
	}

	closed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	refused := closed.Addr().String()
	_ = closed.Close()
	v.mu.Lock()
	before := v.client
	v.mu.Unlock()
	if c, err := v.DialContext(ctx, "tcp", refused); err == nil {
		_ = c.Close()
		t.Errorf("DialContext to the closed %s = nil, want the refusal", refused)
	}
	v.mu.Lock()
	kept := v.client == before
	v.mu.Unlock()
	if !kept {
		t.Error("a refused dial dropped a working connection")
	}
}

// TestDialContextReconnectsAfterATimedOutDial: a dial whose context ends on a
// stuck connection drops it, as a stuck Run does, and the next dial uses a
// new connection.
func TestDialContextReconnectsAfterATimedOutDial(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			_, _ = c.Write([]byte("hello\n"))
			_ = c.Close()
		}
	}()
	s := testServer(t, false, func(int32, string) (string, bool) { return "", true })
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	v := vmOf(ctx, t, s, "flaky")
	short, cancelShort := context.WithTimeout(ctx, 200*time.Millisecond)
	c, err := v.DialContext(short, "tcp", ln.Addr().String())
	cancelShort()
	if err == nil {
		_ = c.Close()
		t.Fatal("DialContext on a stuck connection = nil, want the context error")
	}
	c, err = v.DialContext(ctx, "tcp", ln.Addr().String())
	if err != nil {
		t.Fatalf("DialContext after a timed-out dial: %v; want a new connection", err)
	}
	defer func() { _ = c.Close() }()
	if b, err := io.ReadAll(c); err != nil || string(b) != "hello\n" {
		t.Errorf("read through the new connection = %q, %v", b, err)
	}
}

// TestTimedOutRunKeepsAReplacement: a Run that times out on a stuck
// connection must close that connection, not one a concurrent reconnect put
// in its place.
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/plan6-tmp/8 && GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go test -tags harness -count=1 -run 'TestQemuArgs|TestSeedFiles|TestBootRefusesOneLinkTwice|TestDialContext' ./test/harness/vm/`
Expected: FAIL with `test/harness/vm/seed_test.go:30:3: unknown field PrivMAC in struct literal of type Config` (build failed)

- [ ] **Step 3: Write the implementation**

In `test/harness/vm/seed.go`, replace:

```go
const (
	mgmtIface = "dpmgmt"
	inetIface = "dpinet"
)

// Config describes one VM of a run.
```

with:

```go
const (
	mgmtIface = "dpmgmt"
	inetIface = "dpinet"
	privIface = "dppriv"
)

// Config describes one VM of a run.
```

In `test/harness/vm/seed.go`, replace:

```go
	// McastPort is the UDP port of the run's multicast link, the same for
	// every VM of the run.
	McastPort int
	// Provision is a bash script cloud-init runs once as root; Boot fails
	// unless it exits 0.
	Provision string
```

with:

```go
	// McastPort is the UDP port of the run's multicast link, the same for
	// every VM of the run.
	McastPort int
	// PrivMcastPort, when not zero, adds a private NIC with PrivMAC on a
	// second multicast link of McastGroup, which stands in for the tailnet;
	// the guest names it dppriv and gives it PrivAddrs, in CIDR form.
	PrivMAC       string
	PrivAddrs     []string
	PrivMcastPort int
	// Provision is a bash script cloud-init runs once as root; Boot fails
	// unless it exits 0.
	Provision string
```

In `test/harness/vm/seed.go`, replace:

```go
	if err != nil {
		return seed{}, err
	}
	network, err := marshal(netConfig{Version: 2, Ethernets: map[string]netEthernet{
		mgmtIface: {Match: netMatch{MAC: cfg.MgmtMAC}, SetName: mgmtIface, DHCP4: true},
		inetIface: {Match: netMatch{MAC: cfg.InetMAC}, SetName: inetIface, Addresses: cfg.InetAddrs},
	}})
	if err != nil {
		return seed{}, err
	}
```

with:

```go
	if err != nil {
		return seed{}, err
	}
	ethernets := map[string]netEthernet{
		mgmtIface: {Match: netMatch{MAC: cfg.MgmtMAC}, SetName: mgmtIface, DHCP4: true},
		inetIface: {Match: netMatch{MAC: cfg.InetMAC}, SetName: inetIface, Addresses: cfg.InetAddrs},
	}
	if cfg.PrivMcastPort != 0 {
		ethernets[privIface] = netEthernet{Match: netMatch{MAC: cfg.PrivMAC}, SetName: privIface, Addresses: cfg.PrivAddrs}
	}
	network, err := marshal(netConfig{Version: 2, Ethernets: ethernets})
	if err != nil {
		return seed{}, err
	}
```

In `test/harness/vm/vm.go`, replace:

```go
// On an error the VM is stopped, and the error quotes the end of its console
// log, which stays in cfg.Dir.
func Boot(ctx context.Context, cfg Config) (*VM, error) {
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
```

with:

```go
// On an error the VM is stopped, and the error quotes the end of its console
// log, which stays in cfg.Dir.
func Boot(ctx context.Context, cfg Config) (*VM, error) {
	if cfg.PrivMcastPort != 0 && cfg.PrivMcastPort == cfg.McastPort {
		return nil, fmt.Errorf("vm %s: the private link has the internet link's multicast port %d; two links on one group would be one link", cfg.Name, cfg.McastPort)
	}
	if err := os.MkdirAll(cfg.Dir, 0o700); err != nil {
		return nil, fmt.Errorf("vm %s: %w", cfg.Name, err)
	}
```

In `test/harness/vm/vm.go`, replace:

```go

// qemuArgs are the QEMU arguments of cfg: KVM, the overlay and the seed as
// virtio disks, the serial console to a file, a user-mode management NIC
// forwarding sshPort on 127.0.0.1 to the VM's port 22, and the "internet"
// NIC on the run's multicast link, which needs no bridge, tap device or root.
func qemuArgs(cfg Config, overlay, seed string, sshPort int) []string {
	return []string{
		"-name", cfg.Name,
		"-machine", "q35,accel=kvm",
		"-cpu", "host",
```

with:

```go

// qemuArgs are the QEMU arguments of cfg: KVM, the overlay and the seed as
// virtio disks, the serial console to a file, a user-mode management NIC
// forwarding sshPort on 127.0.0.1 to the VM's port 22, the "internet" NIC on
// the run's multicast link, which needs no bridge, tap device or root, and
// the private NIC on a second multicast link when cfg has one.
func qemuArgs(cfg Config, overlay, seed string, sshPort int) []string {
	args := []string{
		"-name", cfg.Name,
		"-machine", "q35,accel=kvm",
		"-cpu", "host",
```

In `test/harness/vm/vm.go`, replace:

```go
		"-netdev", fmt.Sprintf("socket,id=inet,mcast=%s:%d", McastGroup, cfg.McastPort),
		"-device", "virtio-net-pci,netdev=inet,mac=" + cfg.InetMAC,
	}
}

func consolePath(cfg Config) string { return filepath.Join(cfg.Dir, cfg.Name+"-console.log") }
```

with:

```go
		"-netdev", fmt.Sprintf("socket,id=inet,mcast=%s:%d", McastGroup, cfg.McastPort),
		"-device", "virtio-net-pci,netdev=inet,mac=" + cfg.InetMAC,
	}
	if cfg.PrivMcastPort != 0 {
		args = append(args,
			"-netdev", fmt.Sprintf("socket,id=priv,mcast=%s:%d", McastGroup, cfg.PrivMcastPort),
			"-device", "virtio-net-pci,netdev=priv,mac="+cfg.PrivMAC,
		)
	}
	return args
}

func consolePath(cfg Config) string { return filepath.Join(cfg.Dir, cfg.Name+"-console.log") }
```

In `test/harness/vm/vm.go`, replace:

```go
	return v.client, nil
}

// CopyFile copies the local file src to dst on the VM as root, with mode.
func (v *VM) CopyFile(ctx context.Context, src, dst string, mode fs.FileMode) error {
	f, err := os.Open(src) //nolint:gosec // G304: a file the test names
```

with:

```go
	return v.client, nil
}

// DialContext connects to addr as the VM sees it, through a direct-tcpip
// channel of the SSH connection, so a client on this host reaches listeners
// bound to the guest's loopback interface, such as the controller's
// 127.0.0.1:8080. A dial whose context ends before the channel opens drops
// the connection, as a timed-out Run does, and the next call dials a new one;
// a target that refuses leaves it open.
func (v *VM) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	c, err := v.sshClient(ctx)
	if err != nil {
		return nil, err
	}
	conn, err := c.DialContext(ctx, network, addr)
	if err != nil {
		if ctx.Err() != nil {
			v.dropClient(c)
		}
		return nil, fmt.Errorf("vm %s: dial %s %s: %w", v.cfg.Name, network, addr, err)
	}
	return conn, nil
}

// CopyFile copies the local file src to dst on the VM as root, with mode.
func (v *VM) CopyFile(ctx context.Context, src, dst string, mode fs.FileMode) error {
	f, err := os.Open(src) //nolint:gosec // G304: a file the test names
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go test -tags harness -race -count=1 -run 'TestQemuArgs|TestSeedFiles|TestBootRefusesOneLinkTwice|TestDialContext' ./test/harness/vm/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness` and `test/harness/vm`; the VM tests skip without `-os`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 600 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/8 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add test/harness/vm/seed.go test/harness/vm/seed_test.go test/harness/vm/vm.go test/harness/vm/vm_test.go
git commit -m "feat(harness): add VM private link and guest dialing"
```

---

### Task 9: Harness release builder

**Files:**
- Create: `test/harness/release/release.go`
- Test: `test/harness/release/release_test.go`

**Interfaces:**
- Consumes: existing names only: `update.Index`, `update.Release`, `update.ParseVersion`, `update.TarballName`, `update.Download`, `update.NewIndexSource`, `trustedkeys.Key`, `core.SystemClock`, `errcode.From`, `errcode.UpdateSignatureInvalid`; `aead.dev/minisign` v0.3.0 (`PrivateKey.UnmarshalText`, `NewReader`, `(*Reader).SignWithComments`); the committed key `internal/update/trustedkeys/testdata/harness.key` and the repository's `LICENSE` and `README.md`.
- Produces: C9: package `release` at `test/harness/release` (build tag `harness`): `type Version struct{ Version, Channel, Binary string }`, `func LoadKey(path string) (minisign.PrivateKey, error)`, `func Publish(dir string, key minisign.PrivateKey, versions []Version) error`. `Publish` reads `LICENSE` and `README.md` from the nearest directory at or above the working directory that holds `go.mod`, so it works from any package directory of the repository. Unexported names in the package: `publishVersion`, `sign`, `file`, `tarGz`, `writeFile`, `repoRoot`; test names: `harnessKey`, `harnessKeyID`, `trusted`, `TestLoadKey`, `TestPublishVerifies`, `TestPublishTarball`.

**Items:** 24
**Depends on:** none
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

- [ ] **Step 1: Write the failing test**

Create `test/harness/release/release_test.go`:

```go
//go:build harness

package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// harnessKey is the committed harness key, relative to this package's
// directory, the working directory of its tests.
const harnessKey = "../../../internal/update/trustedkeys/testdata/harness.key"

// harnessKeyID is the ID the harnesskeys build trusts the harness key under.
const harnessKeyID = "BCB10AB3E9AF694E"

// trusted returns the public half of key as the harnesskeys build lists it.
func trusted(t *testing.T, key minisign.PrivateKey) trustedkeys.Key {
	t.Helper()
	pub, ok := key.Public().(minisign.PublicKey)
	if !ok {
		t.Fatalf("public key of type %T", key.Public())
	}
	return trustedkeys.Key{ID: fmt.Sprintf("%016X", pub.ID()), PublicKey: pub.String()}
}

func TestLoadKey(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := trusted(t, key).ID; got != harnessKeyID {
		t.Errorf("harness key ID = %s, want %s", got, harnessKeyID)
	}
	if _, err := LoadKey(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Error("LoadKey of a missing file = nil, want an error")
	}
}

// TestPublishVerifies serves what Publish writes and fetches it with the
// updater's own index source and download, so the layout, the tarball, the
// checksums and the signatures are checked by the verifiers that read them in
// production.
func TestPublishVerifies(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	bins := t.TempDir()
	versions := []Version{
		{Version: "0.2.0", Channel: "stable", Binary: filepath.Join(bins, "darkpanel-0.2.0")},
		{Version: "0.3.0", Channel: "beta", Binary: filepath.Join(bins, "darkpanel-0.3.0")},
	}
	for _, v := range versions {
		if err := os.WriteFile(v.Binary, []byte("#!/bin/sh\necho darkpanel "+v.Version+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	dir := t.TempDir()
	if err := Publish(dir, key, versions); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(http.FileServer(http.Dir(dir)))
	t.Cleanup(srv.Close)
	client := srv.Client()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	keys := []trustedkeys.Key{trusted(t, key)}

	releases, err := update.NewIndexSource(srv.URL, client, core.SystemClock{}).Releases(ctx)
	want := []update.Release{{Version: "0.2.0", Channel: "stable"}, {Version: "0.3.0", Channel: "beta"}}
	if err != nil || !slices.Equal(releases, want) {
		t.Errorf("index releases = %v, %v; want %v", releases, err, want)
	}

	cases := []struct {
		rel     update.Release
		binary  string // the binary the download must extract; empty wants the refusal
		comment string
	}{
		{update.Release{Version: "0.2.0", Channel: "stable"}, versions[0].Binary, "stable release"},
		{update.Release{Version: "0.3.0", Channel: "beta"}, versions[1].Binary, "beta release"},
		{update.Release{Version: "0.2.0", Channel: "beta"}, "", "a stable signature does not pass for beta"},
	}
	for _, c := range cases {
		got, err := update.Download(ctx, client, srv.URL, keys, c.rel, "amd64", t.TempDir())
		if c.binary == "" {
			if ce := errcode.From(err); ce.Code != errcode.UpdateSignatureInvalid {
				t.Errorf("%s: Download = %v, want %s", c.comment, err, errcode.UpdateSignatureInvalid)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Download: %v", c.comment, err)
			continue
		}
		gotBytes, err := os.ReadFile(got) //nolint:gosec // G304: the path Download returned
		if err != nil {
			t.Fatal(err)
		}
		wantBytes, err := os.ReadFile(c.binary)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(gotBytes, wantBytes) {
			t.Errorf("%s: extracted %q, want %q", c.comment, gotBytes, wantBytes)
		}
	}

	checksums := filepath.Join(dir, "v0.2.0", "checksums.txt")
	text, err := os.ReadFile(checksums) //nolint:gosec // G304: a file Publish wrote
	if err != nil {
		t.Fatal(err)
	}
	tampered := append(bytes.Clone(text), []byte("0000000000000000000000000000000000000000000000000000000000000000  extra\n")...)
	if err := os.WriteFile(checksums, tampered, 0o600); err != nil { //nolint:gosec // G703: a file Publish wrote in a test directory
		t.Fatal(err)
	}
	_, err = update.Download(ctx, client, srv.URL, keys, update.Release{Version: "0.2.0", Channel: "stable"}, "amd64", t.TempDir())
	if ce := errcode.From(err); ce.Code != errcode.UpdateSignatureInvalid {
		t.Errorf("Download of a tampered checksums.txt = %v, want %s", err, errcode.UpdateSignatureInvalid)
	}
}

// TestPublishTarball checks the archive against what release/package.sh
// writes: darkpanel, LICENSE and README.md at the root, owned by 0:0, with
// the repository's LICENSE and README.md.
func TestPublishTarball(t *testing.T) {
	key, err := LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(t.TempDir(), "darkpanel")
	if err := os.WriteFile(bin, []byte("binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := Publish(dir, key, []Version{{Version: "0.2.0", Channel: "stable", Binary: bin}}); err != nil {
		t.Fatal(err)
	}
	f, err := os.Open(filepath.Join(dir, "v0.2.0", "darkpanel_0.2.0_linux_amd64.tar.gz")) //nolint:gosec // G304: a file Publish wrote
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		name     string
		mode     int64
		uid, gid int
		body     string
	}
	var got []entry
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		if hdr.Typeflag != tar.TypeReg {
			t.Errorf("%s has type %c, want a regular file", hdr.Name, hdr.Typeflag)
		}
		got = append(got, entry{hdr.Name, hdr.Mode, hdr.Uid, hdr.Gid, string(body)})
	}
	license, err := os.ReadFile("../../../LICENSE")
	if err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile("../../../README.md")
	if err != nil {
		t.Fatal(err)
	}
	want := []entry{
		{"darkpanel", 0o755, 0, 0, "binary"},
		{"LICENSE", 0o644, 0, 0, string(license)},
		{"README.md", 0o644, 0, 0, string(readme)},
	}
	if !slices.Equal(got, want) {
		t.Errorf("tarball entries differ from package.sh's:\n got %+v\nwant %+v", got, want)
	}
	sigs, err := filepath.Glob(filepath.Join(dir, "v0.2.0", "*.minisig"))
	if err != nil || len(sigs) != 1 || filepath.Base(sigs[0]) != "checksums.txt."+harnessKeyID+".minisig" {
		t.Errorf("signature files = %v, %v; want only checksums.txt.%s.minisig", sigs, err, harnessKeyID)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `mkdir -p /root/.cache/plan6-tmp/9 && GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go test -tags harness -count=1 -run 'TestLoadKey|TestPublish' ./test/harness/release/`
Expected: FAIL with `test/harness/release/release_test.go:47:14: undefined: LoadKey` (build failed)

- [ ] **Step 3: Write the implementation**

Create `test/harness/release/release.go`:

```go
//go:build harness

// Package release publishes signed test releases for the harness in the
// production layout (spec section 9): <dir>/index/releases.json and, per
// version, <dir>/v<version>/ with the tarball, checksums.txt and its
// signature. The scanner VM serves the directory as the release base URL of
// the harnesskeys build and of install.sh --base-url.
package release

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"aead.dev/minisign"

	"github.com/darkraise/darkpanel/internal/update"
)

// Version is one release to publish.
type Version struct {
	Version string // a semantic version, such as 0.3.1
	Channel string // stable or beta
	Binary  string // a linux/amd64 darkpanel built for Version
}

// LoadKey reads the unencrypted minisign secret key at path, such as the
// committed harness key internal/update/trustedkeys/testdata/harness.key.
// PrivateKeyFromFile only decrypts, so it refuses an unencrypted key;
// UnmarshalText reads one and, unlike the C minisign tool, does not check the
// key's checksum, which the harness key leaves all zero.
func LoadKey(path string) (minisign.PrivateKey, error) {
	text, err := os.ReadFile(path) //nolint:gosec // G304: the key file the harness names
	if err != nil {
		return minisign.PrivateKey{}, fmt.Errorf("release: %w", err)
	}
	var key minisign.PrivateKey
	if err := key.UnmarshalText(text); err != nil {
		return minisign.PrivateKey{}, fmt.Errorf("release: %s: %w", path, err)
	}
	return key, nil
}

// Publish writes versions under dir, signed with key, and an index listing
// them in order. LICENSE and README.md come from the repository root, the
// nearest directory above the working directory that holds go.mod, so the
// harness tests and this package's tests, which run in different package
// directories, package the same files.
func Publish(dir string, key minisign.PrivateKey, versions []Version) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	license, err := os.ReadFile(filepath.Join(root, "LICENSE")) //nolint:gosec // G304: a repository file
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md")) //nolint:gosec // G304: a repository file
	if err != nil {
		return fmt.Errorf("release: %w", err)
	}
	index := update.Index{FormatVersion: 1, Releases: []update.Release{}}
	seen := map[string]bool{}
	for _, v := range versions {
		if _, err := update.ParseVersion(v.Version); err != nil {
			return fmt.Errorf("release: %w", err)
		}
		if v.Channel != "stable" && v.Channel != "beta" {
			return fmt.Errorf("release: %s: channel %q is not stable or beta", v.Version, v.Channel)
		}
		if seen[v.Version] {
			return fmt.Errorf("release: version %s is listed twice", v.Version)
		}
		seen[v.Version] = true
		if err := publishVersion(dir, key, v, license, readme); err != nil {
			return err
		}
		index.Releases = append(index.Releases, update.Release{Version: v.Version, Channel: v.Channel})
	}
	data, err := json.Marshal(index)
	if err != nil {
		return fmt.Errorf("release: index: %w", err)
	}
	return writeFile(filepath.Join(dir, "index", "releases.json"), append(data, '\n'))
}

// publishVersion writes v<version>/ as release/package.sh and release/sign.sh
// write it: the tarball, checksums.txt in sha256sum's text form, and one
// hashed-mode signature with the trusted comment the verifiers require.
func publishVersion(dir string, key minisign.PrivateKey, v Version, license, readme []byte) error {
	binary, err := os.ReadFile(v.Binary)
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	tarball, err := tarGz([]file{
		{"darkpanel", 0o755, binary},
		{"LICENSE", 0o644, license},
		{"README.md", 0o644, readme},
	})
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	vdir := filepath.Join(dir, "v"+v.Version)
	name := update.TarballName(v.Version, "amd64")
	if err := writeFile(filepath.Join(vdir, name), tarball); err != nil {
		return err
	}
	checksums := fmt.Appendf(nil, "%x  %s\n", sha256.Sum256(tarball), name)
	if err := writeFile(filepath.Join(vdir, "checksums.txt"), checksums); err != nil {
		return err
	}
	sig, err := sign(key, checksums, fmt.Sprintf("version=%s channel=%s purpose=checksums", v.Version, v.Channel))
	if err != nil {
		return fmt.Errorf("release: %s: %w", v.Version, err)
	}
	return writeFile(filepath.Join(vdir, "checksums.txt."+fmt.Sprintf("%016X", key.ID())+".minisig"), sig)
}

// sign signs message in minisign's hashed mode, the only mode the updater
// and install.sh accept. The package-level minisign.SignWithComments signs in
// the legacy mode, so the message goes through a Reader, which signs its
// BLAKE2b-512 digest.
func sign(key minisign.PrivateKey, message []byte, trustedComment string) ([]byte, error) {
	r := minisign.NewReader(bytes.NewReader(message))
	if _, err := io.Copy(io.Discard, r); err != nil {
		return nil, err
	}
	return r.SignWithComments(key, trustedComment, "signature from minisign secret key"), nil
}

type file struct {
	name string
	mode int64
	data []byte
}

// tarGz builds the archive as package.sh does: entries in the given order,
// owner 0:0 by number, fixed modes and mtime, and gzip without a name or
// timestamp.
func tarGz(files []file) ([]byte, error) {
	var buf bytes.Buffer
	gz, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(gz)
	for _, f := range files {
		hdr := &tar.Header{
			Typeflag: tar.TypeReg, Name: f.name, Mode: f.mode, Size: int64(len(f.data)),
			ModTime: time.Unix(0, 0), Format: tar.FormatGNU,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.data); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	if err := gz.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// writeFile writes data to path, creating its directory. Release files are
// public, and the web server that serves them may run as any user, so they
// are world-readable.
func writeFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { //nolint:gosec // G301: published release files
		return fmt.Errorf("release: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil { //nolint:gosec // G306: published release files
		return fmt.Errorf("release: %w", err)
	}
	return nil
}

// repoRoot returns the nearest directory at or above the working directory
// that holds go.mod.
func repoRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("release: %w", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("release: %w", err)
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("release: no go.mod at or above the working directory; run from inside the repository")
		}
		dir = parent
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go test -tags harness -race -count=1 -run 'TestLoadKey|TestPublish' ./test/harness/release/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness`, `test/harness/release` and `test/harness/vm`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 600 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/9 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add test/harness/release/release.go test/harness/release/release_test.go
git commit -m "feat(harness): publish signed test releases"
```

---

### Task 10: Controller-only serve

**Files:**
- Modify: `internal/controller/server/server.go`
- Modify: `internal/controller/server/services.go`
- Modify: `internal/controller/server/listeners.go`
- Test: `internal/controller/server/server_test.go`
- Test: `internal/controller/server/services_test.go`
- Test: `internal/controller/server/e2e_test.go`

**Interfaces:**
- Consumes: C1 (`registry.CoreActions(nil, buildinfo.Version)` and `install.CoreOwnedPaths()`, already passed in `services.go` by Task 1; keep them, do not add them again); C5 (`api.Config.HealthStatus`); P8-C1 to C4 (`fleet.LoadOrCreateAuthority`, `(*fleet.Authority).CheckPeer`, `(*fleet.Authority).ControllerID`, `fleet.NewRegistry`, `fleet.RegistryConfig`, `(*fleet.Registry).Close`, `fleet.NewServer`, `fleet.ServerConfig`, `fleet.NewEnrollLimiter`, `(*fleet.Server).Serve`, `(*fleet.Server).Close`); `listen.StartAgent`, `listen.AgentConfig`, `(*listen.AgentService).Bound`, `(*listen.AgentService).Shutdown`, `listen.Bound`, `listen.BindFunc`; `coreactions.Deps` fields `Authority`, `AgentAdvertise`, `Sessions`, `Version`; `dispatch.Config.Version`, `dispatch.ClientSource`, `dispatch.NewStaticClients`.
- Produces: C10. `server.Options` fields `EmbeddedAgent bool`, `AgentListen []netip.AddrPort`, `AgentAdvertise string`, `AgentBind listen.BindFunc`, `HealthStatus string`; `func (s *Server) AgentListeners() []listen.Bound`. Unexported, package `server`: fields `authority`, `sessions`, `agentListen` of `services`; methods `startEmbeddedAgent`, `startAgentListener`. Test names, package `server`: `noTailscale`, `embeddedOptions`, `TestControllerOnly`, `TestEnrollmentTokensCarryTheAdvertisedAddress`, `TestHealthStatusPassesThrough`, `TestHostListAppliesTheSupportWindow`. `testOptions` now starts the controller alone (it sets `AgentBind: localBind` and `Interfaces: noTailscale`); tests that need the local agent use `embeddedOptions`.

Decision: the agent authority is loaded and passed as `Deps.Authority` in both modes, and `Deps.AgentAdvertise` and `Deps.Version` too, so the host actions behave the same with and without the embedded agent and a development database keeps one controller id when it switches modes. Only the client source differs: the session registry (also `Deps.Sessions`) without `EmbeddedAgent`, `dispatch.StaticClients` over the in-process runtime with it. Controller-only `Start` creates only `ControllerDir`, the controller key's directory and the socket's directory: `paths.MkdirAll` would also create `AgentDir`, owned by the controller's user. The shutdown steps are registered as: locks, controller database, (agent database, agent runtime with `EmbeddedAgent`), controller engine, purges, audit ingester, `agent sessions`, `agent connections`, `agent listener`, then the listeners and the socket; `Shutdown` runs them in reverse. `TestRecoverRunsAtStart` and `TestPurgesRunAtStartAndEveryTenMinutes` keep `testOptions`: `core.module.disable` plans on the controller without the agent.

**Items:** 1, 3, 4, 86
**Depends on:** Task 1, Task 5
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

- [ ] **Step 1: Write the failing test**

In `internal/controller/server/server_test.go`, replace:

```go

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
```

with:

```go

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
```

In `internal/controller/server/server_test.go`, replace:

```go

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)
```

with:

```go

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
)
```

In `internal/controller/server/server_test.go`, replace:

```go
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

// testOptions are options over temporary paths, with the socket in a short
// directory of its own (Unix socket paths are limited to 108 bytes).
func testOptions(t *testing.T) Options {
	t.Helper()
	run, err := os.MkdirTemp("", "dp")
```

with:

```go
	return lc.Listen(ctx, "tcp4", "127.0.0.1:0")
}

// noTailscale reports no Tailscale interface, so the agent listener binds
// only its loopback endpoint and the extra ones.
func noTailscale(string) ([]netip.Addr, bool, error) { return nil, false, nil }

// testOptions are options over temporary paths, with the socket in a short
// directory of its own (Unix socket paths are limited to 108 bytes). They
// start the controller alone, as production does.
func testOptions(t *testing.T) Options {
	t.Helper()
	run, err := os.MkdirTemp("", "dp")
```

In `internal/controller/server/server_test.go`, replace:

```go
		Clock:      coretest.NewFakeClock(t0),
		Hostname:   func() (string, error) { return testHostname, nil },
		Bind:       localBind,
		AuthParams: testParams,
	}
}

// start starts a server and shuts it down when the test ends.
func start(t *testing.T, opts Options) *Server {
	t.Helper()
```

with:

```go
		Clock:      coretest.NewFakeClock(t0),
		Hostname:   func() (string, error) { return testHostname, nil },
		Bind:       localBind,
		AgentBind:  localBind,
		Interfaces: noTailscale,
		AuthParams: testParams,
	}
}

// embeddedOptions are testOptions with the in-process agent of
// --embedded-agent, for the tests that need the local host's agent.
func embeddedOptions(t *testing.T) Options {
	t.Helper()
	opts := testOptions(t)
	opts.EmbeddedAgent = true
	return opts
}

// start starts a server and shuts it down when the test ends.
func start(t *testing.T, opts Options) *Server {
	t.Helper()
```

In `internal/controller/server/server_test.go`, replace:

```go
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
```

with:

```go
}

func TestStartPreparesStorage(t *testing.T) {
	opts := embeddedOptions(t)
	p := opts.Paths
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	id := s.LocalHostID()
	if got := s.AgentListeners(); len(got) != 0 {
		t.Errorf("the embedded agent's serve binds the agent listener %v, want none", got)
	}
	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
```

In `internal/controller/server/server_test.go`, replace:

```go
		t.Errorf("service log lacks the controller backup naming a directory under %s:\n%s", opts.Paths.ControllerBackups(), out)
	}
}
```

with:

```go
		t.Errorf("service log lacks the controller backup naming a directory under %s:\n%s", opts.Paths.ControllerBackups(), out)
	}
}

// TestControllerOnly is serve without the embedded agent: no agent state on
// disk, the agent listener bound and logged, every host offline until its
// agent connects, and nothing serving agents after Shutdown.
func TestControllerOnly(t *testing.T) {
	opts := testOptions(t)
	extra := netip.MustParseAddrPort("192.0.2.1:7444")
	opts.AgentListen = []netip.AddrPort{extra}
	var logs lockedBuffer
	opts.Log = slog.New(slog.NewTextHandler(&logs, nil))
	p := opts.Paths
	s, err := Start(context.Background(), opts)
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	for _, path := range []string{p.AgentDir, p.AgentKey, p.AgentDB()} {
		if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s: %v, want no agent state from a controller-only serve", path, err)
		}
	}
	for _, dir := range []string{filepath.Dir(p.ControllerSocket), p.ControllerDir} {
		wantMode(t, filepath.Join(dir, LockName), 0o600)
	}

	bound := s.AgentListeners()
	var endpoints []netip.AddrPort
	for _, b := range bound {
		endpoints = append(endpoints, b.Endpoint.Addr)
		if !b.Endpoint.TLS || !strings.HasPrefix(b.Addr.String(), "127.0.0.1:") {
			t.Errorf("agent listener %+v, want TLS bound by AgentBind on 127.0.0.1", b)
		}
		if !strings.Contains(logs.String(), b.Addr.String()) {
			t.Errorf("the service log does not name the agent listener's address %s:\n%s", b.Addr, logs.String())
		}
	}
	if want := []netip.AddrPort{netip.MustParseAddrPort("127.0.0.1:7443"), extra}; !slices.Equal(endpoints, want) {
		t.Errorf("agent listener endpoints %v, want %v", endpoints, want)
	}

	for _, name := range []string{"core.update.apply", "core.update.rollout"} {
		if _, ok := s.Registry().Action(core.ActionRef{Name: name, Version: 1}); !ok {
			t.Errorf("the core module has no %s", name)
		}
	}
	id := s.LocalHostID()
	if _, err := s.dispatcher.Resource(context.Background(), root(id), id, "hostinfo.summary"); errcode.From(err).Code != errcode.HostOffline {
		t.Errorf("hostinfo.summary of the local host without its agent: %v, want host.offline", err)
	}

	if err := s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	for _, b := range bound {
		var d net.Dialer
		if c, err := d.DialContext(context.Background(), "tcp", b.Addr.String()); err == nil {
			_ = c.Close()
			t.Errorf("the agent listener %s still accepts connections after Shutdown", b.Addr)
		}
	}
	if got := s.AgentListeners(); len(got) != 0 {
		t.Errorf("agent listeners after Shutdown: %v, want none", got)
	}
}

// TestEnrollmentTokensCarryTheAdvertisedAddress shows the agent authority
// and --agent-advertise wired into the host actions in both modes: a host
// created without a controller address gets a token for the advertised one.
func TestEnrollmentTokensCarryTheAdvertisedAddress(t *testing.T) {
	for name, opts := range map[string]Options{"controller only": testOptions(t), "embedded agent": embeddedOptions(t)} {
		t.Run(name, func(t *testing.T) {
			opts.AgentAdvertise = "panel.example:7443"
			s := start(t, opts)
			v, err := s.dispatcher.Plan(context.Background(), root(s.LocalHostID()), "",
				core.ActionRef{Name: "core.host.create", Version: 1}, json.RawMessage(`{"name":"web1"}`))
			if err != nil {
				t.Fatalf("plan core.host.create: %v", err)
			}
			if !slices.ContainsFunc(v.Diff, func(l core.DiffLine) bool { return strings.Contains(l.Text, "token for panel.example:7443") }) {
				t.Errorf("plan diff %+v, want a token for the advertised address", v.Diff)
			}
		})
	}
}

// TestHealthStatusPassesThrough shows Options.HealthStatus reaching
// /healthz.
func TestHealthStatusPassesThrough(t *testing.T) {
	opts := testOptions(t)
	opts.HealthStatus = "degraded"
	s := start(t, opts)
	if code, body := get(t, http.DefaultClient, baseURL(t, s)+"/healthz", ""); code != 200 || strings.TrimSpace(body) != `{"status":"degraded"}` {
		t.Errorf("GET /healthz = %d %s, want 200 with status degraded", code, body)
	}
}
```

In `internal/controller/server/services_test.go`, replace:

```go
// from DefaultEnabled by the agent's Hello, and a resource and a core action
// read through the dispatcher with the server's own registry.
func TestStartSeedsAndServesModules(t *testing.T) {
	s := start(t, testOptions(t))
	ctx := context.Background()
	id := s.LocalHostID()
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE host_id = ? AND module_id = 'hostinfo' AND enabled = 1 AND available = 1`, id); n != 1 {
```

with:

```go
// from DefaultEnabled by the agent's Hello, and a resource and a core action
// read through the dispatcher with the server's own registry.
func TestStartSeedsAndServesModules(t *testing.T) {
	s := start(t, embeddedOptions(t))
	ctx := context.Background()
	id := s.LocalHostID()
	if n := count(t, s, `SELECT count(*) FROM core_module_state WHERE host_id = ? AND module_id = 'hostinfo' AND enabled = 1 AND available = 1`, id); n != 1 {
```

In `internal/controller/server/services_test.go`, replace:

```go
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

// TestServerRegistryMatchesBuild compares the registry the server assembled
```

with:

```go
	}
}

// TestShutdownStopsTheTimers shows that after Shutdown no purge, ingestion,
// revert or listener timer of the server is left armed, with and without
// the embedded agent.
func TestShutdownStopsTheTimers(t *testing.T) {
	for name, opts := range map[string]Options{"controller only": testOptions(t), "embedded agent": embeddedOptions(t)} {
		t.Run(name, func(t *testing.T) {
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
		})
	}
}

// TestHostListAppliesTheSupportWindow shows the controller's version wired
// into the host actions: the embedded agent of the same development build is
// supported, and an agent reporting a release this build cannot compare
// with is not.
func TestHostListAppliesTheSupportWindow(t *testing.T) {
	s := start(t, embeddedOptions(t))
	supported := func() bool {
		t.Helper()
		out, err := s.dispatcher.Read(context.Background(), root(s.LocalHostID()), "", core.ActionRef{Name: "core.host.list", Version: 1}, json.RawMessage(`{}`))
		if err != nil {
			t.Fatalf("core.host.list: %v", err)
		}
		var list struct {
			Hosts []struct {
				Supported bool `json:"supported"`
			} `json:"hosts"`
		}
		if err := json.Unmarshal(out, &list); err != nil || len(list.Hosts) != 1 {
			t.Fatalf("core.host.list = %s (%v), want one host", out, err)
		}
		return list.Hosts[0].Supported
	}
	if !supported() {
		t.Error("the embedded agent of the same build is outside the support window")
	}
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(),
		`UPDATE core_hosts SET agent_version = '0.1.0' WHERE id = ?`, s.LocalHostID()); err != nil {
		t.Fatal(err)
	}
	if supported() {
		t.Error("an agent at 0.1.0 is inside a dev controller's support window")
	}
}

// TestServerRegistryMatchesBuild compares the registry the server assembled
```

In `internal/controller/server/e2e_test.go`, replace:

```go
// TestEndToEnd is the end-to-end test of spec section "Verification" over
// one assembled server.
func TestEndToEnd(t *testing.T) {
	opts := testOptions(t)
	opts.Hostname = func() (string, error) { return "e2e-host", nil }
	opts.Listen = append(opts.Listen, listen.Spec{Kind: listen.KindHTTPS, Addr: netip.MustParseAddrPort("127.0.0.1:0")})
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
```

with:

```go
// TestEndToEnd is the end-to-end test of spec section "Verification" over
// one assembled server.
func TestEndToEnd(t *testing.T) {
	opts := embeddedOptions(t)
	opts.Hostname = func() (string, error) { return "e2e-host", nil }
	opts.Listen = append(opts.Listen, listen.Spec{Kind: listen.KindHTTPS, Addr: netip.MustParseAddrPort("127.0.0.1:0")})
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 timeout 600 go test -count=1 ./internal/controller/server/`
Expected: FAIL with `internal/controller/server/server_test.go:73:3: unknown field AgentBind in struct literal of type Options`

- [ ] **Step 3: Write the implementation**

In `internal/controller/server/server.go`, replace:

```go
// Package server assembles `darkpanel serve` (spec section 1, part P14): the
// controller and, in M1, the agent half in one process. Start opens the
// stores and starts the services and listeners in the order the spec sets;
// Shutdown stops them in reverse.
package server
```

with:

```go
// Package server assembles `darkpanel serve` (spec section 1): the controller
// with its agent listener, or, with the development-only embedded agent, the
// controller and the M1 in-process agent in one process. Start opens the
// stores and starts the services and listeners in the order the spec sets;
// Shutdown stops them in reverse.
package server
```

In `internal/controller/server/server.go`, replace:

```go
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"sync"
```

with:

```go
	"fmt"
	"log/slog"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"sync"
```

In `internal/controller/server/server.go`, replace:

```go
)

// LockName is the single-instance lock file, taken in the controller
// socket's directory and in both data directories, so two serves that share
// either the socket or a database refuse each other.
const LockName = "controller.lock"

// BackupsKept is how many migration backups the controller database keeps.
```

with:

```go
)

// LockName is the single-instance lock file, taken in the controller
// socket's directory and in the controller's data directory, and in the
// agent's with the embedded agent, so two serves that share either the
// socket or a database refuse each other.
const LockName = "controller.lock"

// BackupsKept is how many migration backups the controller database keeps.
```

In `internal/controller/server/server.go`, replace:

```go
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Options configure Start. Paths is required. Production passes
// paths.Resolve(dev), the parsed --listen values and nil or zero for every
// other field; tests override the rest.
type Options struct {
	Paths paths.Paths
	// Listen are the listeners to serve, from listen.ParseSpecs; nil means
```

with:

```go
const timeFormat = "2006-01-02T15:04:05.000000000Z"

// Options configure Start. Paths is required. Production passes
// paths.Resolve(dev), the parsed --listen and --agent-listen values, the
// --agent-advertise address and nil or zero for every other field; tests
// override the rest.
type Options struct {
	Paths paths.Paths
	// Listen are the listeners to serve, from listen.ParseSpecs; nil means
```

In `internal/controller/server/server.go`, replace:

```go
	ListenSocket func(path string) (net.Listener, error)
	// AuthParams is zero for auth.DefaultParams.
	AuthParams auth.Params
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
```

with:

```go
	ListenSocket func(path string) (net.Listener, error)
	// AuthParams is zero for auth.DefaultParams.
	AuthParams auth.Params
	// EmbeddedAgent runs the M1 in-process agent over agent.db and the agent
	// key instead of the agent listener; only --dev may set it.
	EmbeddedAgent bool
	// AgentListen are the agent listener's extra binds, from
	// listen.ParseAgentListen.
	AgentListen []netip.AddrPort
	// AgentAdvertise is the address an enrollment token carries when the
	// request names none; empty means there is none.
	AgentAdvertise string
	// AgentBind is nil for listen.Bind.
	AgentBind listen.BindFunc
	// HealthStatus is the status /healthz reports; empty means ok.
	HealthStatus string
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
```

In `internal/controller/server/server.go`, replace:

```go
	close func(ctx context.Context) error
}

// Server is one running controller with its local agent.
type Server struct {
	opts     Options
	clock    core.Clock
```

with:

```go
	close func(ctx context.Context) error
}

// Server is one running controller, with its agent listener or its embedded
// agent.
type Server struct {
	opts     Options
	clock    core.Clock
```

In `internal/controller/server/server.go`, replace:

```go
}

// openStorage creates the directories, checks the socket's directory, loads
// or creates both keys, takes the single-instance locks, opens both
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
```

with:

```go
}

// openStorage creates the directories, checks the socket's directory, loads
// or creates the keys, takes the single-instance locks, opens the databases,
// migrates the controller database and records the local host. Only the
// embedded agent opens agent.db and the agent key: the controller alone
// leaves the agent's files to the agent, which runs as another user.
func (s *Server) openStorage(ctx context.Context) error {
	p := s.opts.Paths
	runDir := filepath.Dir(p.ControllerSocket)
	dirs := []string{runDir, p.ControllerDir}
	if s.opts.EmbeddedAgent {
		dirs = append(dirs, p.AgentDir)
		if err := p.MkdirAll(); err != nil {
			return err
		}
	} else {
		for _, d := range []string{p.ControllerDir, filepath.Dir(p.ControllerKey), runDir} {
			if err := os.MkdirAll(d, 0o700); err != nil {
				return fmt.Errorf("server: create %s: %w", d, err)
			}
		}
	}
	if err := checkPrivateDir(runDir); err != nil {
		return err
	}
```

In `internal/controller/server/server.go`, replace:

```go
	if s.controllerSealer, err = loadSealer(p.ControllerKey); err != nil {
		return err
	}
	if s.agentSealer, err = loadSealer(p.AgentKey); err != nil {
		return err
	}
	locked := map[string]bool{}
	for _, dir := range []string{runDir, p.ControllerDir, p.AgentDir} {
		// flock refuses a second lock on one file even within a process.
		if dir = filepath.Clean(dir); locked[dir] {
			continue
```

with:

```go
	if s.controllerSealer, err = loadSealer(p.ControllerKey); err != nil {
		return err
	}
	if s.opts.EmbeddedAgent {
		if s.agentSealer, err = loadSealer(p.AgentKey); err != nil {
			return err
		}
	}
	locked := map[string]bool{}
	for _, dir := range dirs {
		// flock refuses a second lock on one file even within a process.
		if dir = filepath.Clean(dir); locked[dir] {
			continue
```

In `internal/controller/server/server.go`, replace:

```go
	if s.controllerDB, err = s.openDB(ctx, "controller database", p.ControllerDB()); err != nil {
		return err
	}
	if s.agentDB, err = s.openDB(ctx, "agent database", p.AgentDB()); err != nil {
		return err
	}
	if err := s.migrateController(ctx); err != nil {
		return err
```

with:

```go
	if s.controllerDB, err = s.openDB(ctx, "controller database", p.ControllerDB()); err != nil {
		return err
	}
	if s.opts.EmbeddedAgent {
		if s.agentDB, err = s.openDB(ctx, "agent database", p.AgentDB()); err != nil {
			return err
		}
	}
	if err := s.migrateController(ctx); err != nil {
		return err
```

In `internal/controller/server/services.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
```

In `internal/controller/server/services.go`, replace:

```go
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
```

with:

```go
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// services are the controller's services, with the embedded agent's runtime
// or the agent listener's.
type services struct {
	auditLog    *audit.Store
	authService *auth.Service
	authority   *fleet.Authority
	registry    *core.Registry
	runtime     *runtime.Runtime // with EmbeddedAgent only
	sessions    *fleet.Registry  // without EmbeddedAgent only
	agentListen *listen.AgentService
	hosts       *dispatch.Hosts
	engine      *lifecycle.Engine
	ingester    *audit.Ingester
```

In `internal/controller/server/services.go`, replace:

```go
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
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
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
	clients := dispatch.NewStaticClients()
	clients.Add(s.localHostID, rt)
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{
		DB: s.controllerDB, Registry: reg, Clients: clients, Clock: s.clock, Log: s.log,
	})
	if err := s.hosts.Hello(ctx, s.localHostID); err != nil {
		return fmt.Errorf("server: hello from the local agent: %w", err)
	}

	eng, err := lifecycle.New(lifecycle.Config{
```

with:

```go
func (s *Server) Registry() *core.Registry { return s.registry }

// startServices builds the services over the migrated stores, in the order
// that the controller's foreign keys and recovery need: the embedded agent
// says Hello (seeding the local host's module rows from DefaultEnabled)
// before the controller engine recovers, and nothing is served until both
// have. Without the embedded agent every host is offline until its agent
// connects to the agent listener, which starts once the dispatcher exists.
//
// The agent authority is loaded in both modes, so the host actions work
// alike and a development database keeps one controller id across modes.
func (s *Server) startServices(ctx context.Context) error {
	s.auditLog = audit.New(s.controllerDB, s.clock)
	s.authService = auth.New(s.controllerDB, s.controllerSealer, s.clock, s.auditLog, auth.Options{Params: s.opts.AuthParams})
	var err error
	if s.authority, err = fleet.LoadOrCreateAuthority(ctx, s.controllerDB, s.controllerSealer, s.clock); err != nil {
		return err
	}
	deps := coreactions.Deps{
		Auth: s.authService, Audit: s.auditLog, Modules: modules.All(),
		Extra: registry.CoreActions(nil, buildinfo.Version), OwnedPaths: install.CoreOwnedPaths(),
		Authority: s.authority, AgentAdvertise: s.opts.AgentAdvertise, Version: buildinfo.Version,
	}
	if !s.opts.EmbeddedAgent {
		s.sessions, err = fleet.NewRegistry(fleet.RegistryConfig{
			DB: s.controllerDB, Clock: s.clock, CheckPeer: s.authority.CheckPeer,
			Version: buildinfo.Version, ControllerID: s.authority.ControllerID(), Log: s.log,
		})
		if err != nil {
			return fmt.Errorf("server: %w", err)
		}
		// Assigned only when set: a nil *fleet.Registry in the interface
		// would not read as "no sessions".
		deps.Sessions = s.sessions
	}
	reg, err := registry.BuildWith(coreactions.Module(deps))
	if err != nil {
		return fmt.Errorf("server: registry: %w", err)
	}
	s.registry = reg

	var clients dispatch.ClientSource
	if s.opts.EmbeddedAgent {
		if clients, err = s.startEmbeddedAgent(ctx); err != nil {
			return err
		}
	} else {
		clients = s.sessions
	}
	s.hosts = dispatch.NewHosts(dispatch.HostsConfig{
		DB: s.controllerDB, Registry: reg, Clients: clients, Clock: s.clock, Log: s.log,
	})
	if s.opts.EmbeddedAgent {
		if err := s.hosts.Hello(ctx, s.localHostID); err != nil {
			return fmt.Errorf("server: hello from the local agent: %w", err)
		}
	}

	eng, err := lifecycle.New(lifecycle.Config{
```

In `internal/controller/server/services.go`, replace:

```go

	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	return nil
}
```

with:

```go

	s.dispatcher, err = dispatch.New(dispatch.Config{
		Registry: reg, Engine: eng, Hosts: s.hosts, DB: s.controllerDB, Clock: s.clock, Log: s.log, Ingest: s.ingester,
		Version: buildinfo.Version,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	if !s.opts.EmbeddedAgent {
		if err := s.startAgentListener(ctx); err != nil {
			return err
		}
	}
	return nil
}

// startEmbeddedAgent opens the M1 in-process agent runtime over agent.db and
// returns the client source that holds it as the local host's agent.
func (s *Server) startEmbeddedAgent(ctx context.Context) (dispatch.ClientSource, error) {
	rt, err := runtime.Open(ctx, runtime.Config{
		DB: s.agentDB, Sealer: s.agentSealer, Clock: s.clock, Log: s.log, Registry: s.registry,
		HostID: s.localHostID, BinaryVersion: buildinfo.Version, BackupDir: s.opts.Paths.AgentBackups(),
		Exec: platform.NewRunner(platform.ProductionAllowlist()),
	})
	if err != nil {
		return nil, err
	}
	s.runtime = rt
	s.onShutdown("agent runtime", func(context.Context) error { rt.Close(); return nil })

	// Every core_hosts row needs a client here, or Changes and ingestion
	// fail on it; the local host is the only one the embedded agent serves.
	clients := dispatch.NewStaticClients()
	clients.Add(s.localHostID, rt)
	return clients, nil
}

// startAgentListener binds the agent listener and serves its connections
// with the fleet server, whose agent sessions join the session registry.
// Shutdown runs these steps in reverse: the listener stops accepting, the
// fleet server closes the connections, and the registry ends the sessions,
// all before the dispatcher's services stop.
func (s *Server) startAgentListener(ctx context.Context) error {
	s.onShutdown("agent sessions", func(context.Context) error { s.sessions.Close(); return nil })
	srv, err := fleet.NewServer(fleet.ServerConfig{
		Authority: s.authority, Registry: s.sessions, Limiter: fleet.NewEnrollLimiter(s.clock), Log: s.log,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	s.onShutdown("agent connections", func(context.Context) error { srv.Close(); return nil })
	svc, err := listen.StartAgent(ctx, listen.AgentConfig{
		Extra: s.opts.AgentListen, DB: s.controllerDB, Clock: s.clock, Serve: srv.Serve, Log: s.log,
		Interfaces: s.opts.Interfaces, Bind: s.opts.AgentBind,
	})
	if err != nil {
		return err
	}
	s.agentListen = svc
	s.onShutdown("agent listener", svc.Shutdown)
	addrs := make([]string, 0, len(svc.Bound()))
	for _, b := range svc.Bound() {
		addrs = append(addrs, b.Addr.String())
	}
	s.log.Info("server: serving agents", "addrs", addrs)
	return nil
}
```

In `internal/controller/server/listeners.go`, replace:

```go
// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// startListeners loads or creates the web authority, builds the API, starts
// the network listeners with it and serves the controller socket.
func (s *Server) startListeners(ctx context.Context) error {
```

with:

```go
// Listeners are the network listeners currently bound.
func (s *Server) Listeners() []listen.Bound { return s.listenService.State().Listeners }

// AgentListeners are the agent listener's bound sockets: none with the
// embedded agent, and none after Shutdown.
func (s *Server) AgentListeners() []listen.Bound {
	if s.agentListen == nil {
		return nil
	}
	return s.agentListen.Bound()
}

// startListeners loads or creates the web authority, builds the API, starts
// the network listeners with it and serves the controller socket.
func (s *Server) startListeners(ctx context.Context) error {
```

In `internal/controller/server/listeners.go`, replace:

```go
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log,
	})
	if err != nil {
		return err
```

with:

```go
		SchemaVersion: func(ctx context.Context) (int, error) {
			return store.SchemaVersion(ctx, s.controllerDB, store.SideController)
		},
		HostID: s.localHostID, Dev: s.opts.Dev, UI: api.EmbeddedUI(), Log: s.log, HealthStatus: s.opts.HealthStatus,
	})
	if err != nil {
		return err
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 timeout 600 go test -race -count=1 ./internal/controller/server/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 timeout 600 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/10 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/services.go internal/controller/server/listeners.go internal/controller/server/server_test.go internal/controller/server/services_test.go internal/controller/server/e2e_test.go
git commit -m "feat(server): run serve as the controller alone"
```

---

### Task 11: Rollouts in serve

**Files:**
- Modify: `internal/controller/server/server.go`
- Modify: `internal/controller/server/services.go`
- Modify: `internal/update/actions.go`
- Create: `internal/controller/server/rollouts_test.go`
- Test: `internal/controller/server/rollouts_test.go`
- Test: `internal/update/actions_test.go`
- Test: `internal/controller/rollout/driver_test.go`

**Interfaces:**
- Consumes: C10 (Task 10: `embeddedOptions`, `testOptions`, `start`, `count`, `t0`, the `clients` variable and the `startAgentListener` step in `startServices`); P11-C7 (`rollout.NewDriver`, `rollout.DriverConfig`, `(*rollout.Driver).Start`, `(*rollout.Driver).Stop`, `rollout.NewScheduler`, `rollout.SchedulerConfig`, `(*rollout.Scheduler).Start`, `(*rollout.Scheduler).Stop`, `rollout.ReleaseSource`, `rollout.Create`, `rollout.CreateParams`, `rollout.PollInterval`, `rollout.ScheduleInterval`, `rollout.ActionRollout`); `update.NewIndexSource`, `update.Release`, `trustedkeys.BaseURL`; `core.ActorUpdateSchedule`; C3 (`errcode.Unavailable`); in package `update`'s tests, `wantCode` and `actionDef`; in package `rollout`'s tests, the table of `TestDriverRefusalsFailTheHost`.
- Produces: C11. `server.Options.ReleaseIndex rollout.ReleaseSource`. Unexported, package `server`: const `indexTimeout`, method `startRollouts`. The update action handlers built by `update.Actions(nil)` now fail with `errcode.Unavailable` (`the update actions run only in darkpanel agent run`) instead of an uncoded error. Test names, package `server`: `fakeIndex`, `exec`, `TestRolloutDriverRuns`, `TestEmbeddedRolloutFails`, `TestUpdateSchedulerClaimsAWindow`; a new row `a plan where no updater runs` of `TestDriverRefusalsFailTheHost`.

The driver and the scheduler start at the end of `startServices`, after the dispatcher, the recovery and the agent listener, so `Shutdown` stops the scheduler, then the driver, before the agent listener, the session registry and the dispatcher's services. `TestShutdownStopsTheTimers` (Task 10, both modes) already fails if either `Stop` is not registered, because each re-arms a fake-clock timer. The driver test runs without the embedded agent: the driver takes the first host (`waiting` to `updating`) and then waits without a plan because the host is offline, which is observable within one `PollInterval`. The scheduler test runs with the embedded agent, so the local host is online; it sets the host `enrolled` with `agent_version` `0.1.0` (the embedded agent reports `dev`, which the scheduler skips) and a window at the fake clock's start.

The embedded agent's process has no updater. Its update actions used to fail with an uncoded error, which reaches the driver through `dispatch.StaticClients` without a code, and the driver's `notYet` (`internal/controller/rollout/driver.go`) treats an uncoded error as a transport failure: such a rollout stayed `running` with its host `updating` forever and blocked every other rollout. The handlers now answer `unavailable`. The driver already fails the host on every coded error other than `host.offline` and `lock.held`, so `driver.go` does not change; the new row of `TestDriverRefusalsFailTheHost` passes before and after and guards that rule. `TestEmbeddedRolloutFails` shows the result: a rollout to the embedded local host ends `failed` with its host's error code `unavailable` within one `PollInterval`. Over the agent transport the same answer arrives as an RFC 9457 `unavailable` problem.

**Items:** 14, 15, 86
**Depends on:** Task 3, Task 10
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/rollouts_test.go` with this complete content:

```go
package server

import (
	"context"
	"database/sql"
	"testing"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeIndex is a release index that lists its releases.
type fakeIndex []update.Release

func (f fakeIndex) Releases(context.Context) ([]update.Release, error) { return f, nil }

// exec runs a statement on the server's controller database.
func exec(t *testing.T, s *Server, query string, args ...any) {
	t.Helper()
	if _, err := s.controllerDB.Writer().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
}

// TestRolloutDriverRuns shows the driver started with the server: within one
// PollInterval it takes the first host of a new rollout, and with the host
// offline it waits without planning, the rollout still running.
func TestRolloutDriverRuns(t *testing.T) {
	opts := testOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	var r core.Rollout
	if err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: clock.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create the rollout: %v", err)
	}

	clock.Advance(rollout.PollInterval)
	var rolloutState, hostState string
	var planID sql.NullString
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT r.state, h.state, h.plan_id FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		 WHERE r.id = ? AND h.host_id = ?`, r.ID, id).Scan(&rolloutState, &hostState, &planID); err != nil {
		t.Fatal(err)
	}
	if rolloutState != core.RolloutRunning || hostState != core.RolloutHostUpdating || planID.Valid {
		t.Errorf("after one PollInterval the rollout is %s with its host %s and plan %v; want running, updating and no plan while the host is offline",
			rolloutState, hostState, planID)
	}
}

// TestEmbeddedRolloutFails shows that a rollout to the embedded agent, whose
// process runs no updater, fails at once instead of waiting for an update
// that can never start.
func TestEmbeddedRolloutFails(t *testing.T) {
	opts := embeddedOptions(t)
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	ctx := context.Background()
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled' WHERE id = ?`, id)
	var r core.Rollout
	if err := s.controllerDB.Write(ctx, func(tx *sql.Tx) error {
		var err error
		r, err = rollout.Create(ctx, tx, rollout.CreateParams{
			Input:             core.RolloutInput{Hosts: []string{id}, Version: "0.2.0", Channel: core.ChannelStable},
			ControllerVersion: buildinfo.Version, Now: clock.Now(),
		})
		return err
	}); err != nil {
		t.Fatalf("create the rollout: %v", err)
	}

	clock.Advance(rollout.PollInterval)
	var rolloutState, hostState string
	var code sql.NullString
	if err := s.controllerDB.Read().QueryRowContext(ctx,
		`SELECT r.state, h.state, h.error_code FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		 WHERE r.id = ? AND h.host_id = ?`, r.ID, id).Scan(&rolloutState, &hostState, &code); err != nil {
		t.Fatal(err)
	}
	if rolloutState != core.RolloutFailed || hostState != core.RolloutHostFailed || code.String != string(errcode.Unavailable) {
		t.Errorf("after one PollInterval the rollout is %s with its host %s (%v); want failed, failed with unavailable",
			rolloutState, hostState, code)
	}
}

// TestUpdateSchedulerClaimsAWindow shows the scheduler started with the
// server over its release index and the embedded agent's clients: an
// online local host inside its update window, behind the newest release of
// its channel, gets a one-host rollout written by system:update-schedule.
func TestUpdateSchedulerClaimsAWindow(t *testing.T) {
	opts := embeddedOptions(t)
	opts.ReleaseIndex = fakeIndex{{Version: "0.2.0", Channel: core.ChannelStable}}
	clock := opts.Clock.(*coretest.FakeClock)
	s := start(t, opts)
	id := s.LocalHostID()
	exec(t, s, `UPDATE core_hosts SET state = 'enrolled', agent_version = '0.1.0' WHERE id = ?`, id)
	exec(t, s, `INSERT INTO core_host_update (host_id, channel, days, time_utc) VALUES (?, 'stable', 'mon,tue,wed,thu,fri,sat,sun', ?)`,
		id, t0.Format("15:04"))

	clock.Advance(rollout.ScheduleInterval)
	if n := count(t, s, `SELECT count(*) FROM core_rollouts r JOIN core_rollout_hosts h ON h.rollout_id = r.id
		WHERE r.version = '0.2.0' AND h.host_id = ?`, id); n != 1 {
		t.Errorf("rollouts of the local host to 0.2.0 after one ScheduleInterval: %d, want 1", n)
	}
	if n := count(t, s, `SELECT count(*) FROM core_audit WHERE actor = ? AND action = ?`,
		core.ActorUpdateSchedule, rollout.ActionRollout); n != 1 {
		t.Errorf("audit entries of the scheduled rollout: %d, want 1", n)
	}
}
```

In `internal/update/actions_test.go`, replace:

```go
	ctx := context.Background()
	apply := actionDef(t, defs, ActionApply)
	_, err = apply.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantInternal(t, err)
	_, err = apply.Handler.Apply(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantInternal(t, err)
	err = apply.Handler.Verify(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`),
		json.RawMessage(`{}`))
	wantInternal(t, err)
	rollback := actionDef(t, defs, ActionRollback)
	_, err = rollback.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"force":false}`))
	wantInternal(t, err)
	_, err = actionDef(t, defs, ActionStatus).Handler.Read(ctx, core.Env{}, json.RawMessage(`{}`))
	wantInternal(t, err)
}

func TestApplyAction(t *testing.T) {
```

with:

```go
	ctx := context.Background()
	apply := actionDef(t, defs, ActionApply)
	_, err = apply.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantCode(t, err, errcode.Unavailable)
	_, err = apply.Handler.Apply(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`))
	wantCode(t, err, errcode.Unavailable)
	err = apply.Handler.Verify(ctx, core.Env{}, json.RawMessage(`{"version":"1.3.0","channel":"stable","force":false}`),
		json.RawMessage(`{}`))
	wantCode(t, err, errcode.Unavailable)
	rollback := actionDef(t, defs, ActionRollback)
	_, err = rollback.Handler.Plan(ctx, core.Env{}, json.RawMessage(`{"force":false}`))
	wantCode(t, err, errcode.Unavailable)
	_, err = actionDef(t, defs, ActionStatus).Handler.Read(ctx, core.Env{}, json.RawMessage(`{}`))
	wantCode(t, err, errcode.Unavailable)
}

func TestApplyAction(t *testing.T) {
```

In `internal/controller/rollout/driver_test.go`, replace:

```go
		{"a refused plan", errcode.New(errcode.UpdateNotFound, "no release 1.3.0 in channel stable"), nil, errcode.UpdateNotFound},
		{"a plan answered internal", agentInternal, nil, errcode.Internal},
		{"an apply answered internal", nil, agentInternal, errcode.Internal},
		{"an uncoded plan error", errors.New("transport: connection reset"), nil, ""},
		{"an uncoded apply error", nil, errors.New("transport: connection reset"), ""},
	}
```

with:

```go
		{"a refused plan", errcode.New(errcode.UpdateNotFound, "no release 1.3.0 in channel stable"), nil, errcode.UpdateNotFound},
		{"a plan answered internal", agentInternal, nil, errcode.Internal},
		{"an apply answered internal", nil, agentInternal, errcode.Internal},
		{"a plan where no updater runs", errcode.New(errcode.Unavailable, "the update actions run only in darkpanel agent run"), nil, errcode.Unavailable},
		{"an uncoded plan error", errors.New("transport: connection reset"), nil, ""},
		{"an uncoded apply error", nil, errors.New("transport: connection reset"), ""},
	}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 timeout 600 go test -count=1 -run 'TestRolloutDriverRuns|TestUpdateSchedulerClaimsAWindow|TestEmbeddedRolloutFails' ./internal/controller/server/`
Expected: FAIL with `internal/controller/server/rollouts_test.go:107:7: opts.ReleaseIndex undefined (type Options has no field or method ReleaseIndex)`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 timeout 600 go test -count=1 -run TestActionsRegisterWithoutAnUpdater ./internal/update/`
Expected: FAIL with `actions_test.go:113: error = update: this process has no updater; update actions run on the agent, want code unavailable`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 timeout 600 go test -count=1 -run TestDriverRefusalsFailTheHost ./internal/controller/rollout/`
Expected: PASS (the driver already fails a host on a coded refusal; the row guards it)

- [ ] **Step 3: Write the implementation**

In `internal/controller/server/server.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
```

In `internal/controller/server/server.go`, replace:

```go
	AgentBind listen.BindFunc
	// HealthStatus is the status /healthz reports; empty means ok.
	HealthStatus string
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
```

with:

```go
	AgentBind listen.BindFunc
	// HealthStatus is the status /healthz reports; empty means ok.
	HealthStatus string
	// ReleaseIndex is the release index the update scheduler reads; nil
	// means the published index under trustedkeys.BaseURL().
	ReleaseIndex rollout.ReleaseSource
}

// closer is one thing Shutdown stops, in reverse order of acquisition.
```

In `internal/controller/server/services.go`, replace:

```go
	"context"
	"errors"
	"fmt"
	"sync"
	"time"
```

with:

```go
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"
```

In `internal/controller/server/services.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/controller/dispatch"
	"github.com/darkraise/darkpanel/internal/controller/fleet"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/lifecycle"
	"github.com/darkraise/darkpanel/internal/core/schema"
```

In `internal/controller/server/services.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
)

// PurgeInterval is how often the controller purges expired plans, sessions
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// services are the controller's services, with the embedded agent's runtime
// or the agent listener's.
type services struct {
```

with:

```go
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// PurgeInterval is how often the controller purges expired plans, sessions
// and tokens (spec section 20). The agent runtime runs its own purge.
const PurgeInterval = 10 * time.Minute

// indexTimeout bounds one read of the release index. The index is one small
// file, so its client may bound the whole request.
const indexTimeout = 30 * time.Second

// services are the controller's services, with the embedded agent's runtime
// or the agent listener's.
type services struct {
```

In `internal/controller/server/services.go`, replace:

```go
			return err
		}
	}
	return nil
}
```

with:

```go
			return err
		}
	}
	return s.startRollouts(clients)
}

// startRollouts starts the rollout driver over the dispatcher and the update
// scheduler over clients, which tell it the online hosts. Shutdown stops the
// scheduler, then the driver, before the agent sessions and the dispatcher's
// services; each waits for a running pass.
func (s *Server) startRollouts(clients dispatch.ClientSource) error {
	index := s.opts.ReleaseIndex
	if index == nil {
		index = update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: indexTimeout}, s.clock)
	}
	driver, err := rollout.NewDriver(rollout.DriverConfig{DB: s.controllerDB, Dispatcher: s.dispatcher, Clock: s.clock, Log: s.log})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	scheduler, err := rollout.NewScheduler(rollout.SchedulerConfig{
		DB: s.controllerDB, Clients: clients, Index: index, Audit: s.auditLog, Clock: s.clock, Log: s.log,
		Version: buildinfo.Version,
	})
	if err != nil {
		return fmt.Errorf("server: %w", err)
	}
	driver.Start()
	s.onShutdown("rollout driver", func(context.Context) error { driver.Stop(); return nil })
	scheduler.Start()
	s.onShutdown("update scheduler", func(context.Context) error { scheduler.Stop(); return nil })
	return nil
}
```

In `internal/update/actions.go`, replace:

```go
// Actions returns core.update.apply/v1, core.update.rollback/v1 and
// core.update.status/v1 over u, for coreactions.Deps.Extra. Every process
// registers the one core manifest, but only the agent runs updates: the
// controller process passes nil, and every handler then fails with an
// internal error.
func Actions(u *Updater) []core.ActionDef {
	a := updateActions{u: u}
	return []core.ActionDef{
```

with:

```go
// Actions returns core.update.apply/v1, core.update.rollback/v1 and
// core.update.status/v1 over u, for coreactions.Deps.Extra. Every process
// registers the one core manifest, but only the agent runs updates: the
// controller process and the embedded agent pass nil, and every handler then
// fails with unavailable, which the rollout driver treats as a failed host
// rather than one to wait for.
func Actions(u *Updater) []core.ActionDef {
	a := updateActions{u: u}
	return []core.ActionDef{
```

In `internal/update/actions.go`, replace:

```go

func (a updateActions) updater() (*Updater, error) {
	if a.u == nil {
		return nil, errors.New("update: this process has no updater; update actions run on the agent")
	}
	return a.u, nil
}
```

with:

```go

func (a updateActions) updater() (*Updater, error) {
	if a.u == nil {
		return nil, errcode.New(errcode.Unavailable, "the update actions run only in darkpanel agent run")
	}
	return a.u, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 timeout 600 go test -race -count=1 ./internal/controller/server/ ./internal/update/ ./internal/controller/rollout/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 timeout 600 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/11 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/server.go internal/controller/server/services.go internal/controller/server/rollouts_test.go internal/update/actions.go internal/update/actions_test.go internal/controller/rollout/driver_test.go
git commit -m "feat(server): drive rollouts and scheduled updates"
```

---

### Task 12: End to end over the agent transport

**Files:**
- Create: `internal/controller/server/fleet_e2e_test.go`
- Modify: `Makefile`
- Test: `internal/controller/server/fleet_e2e_test.go`

**Interfaces:**
- Consumes: C10 (`Server.AgentListeners`, `testOptions` with `AgentBind: localBind`), C11 (the driver and scheduler run, unused here; `update.Actions(nil)` answers `errcode.Unavailable`, which the old agent's `core.update.status` returns); from `e2e_test.go`: `e2e` (fields `t`, `s`, `clock`, `url`, `home`, `session`, `csrf`), `credentials`, `change`, `adminPassword`, `secretLine`, `(*e2e).do`, `(*e2e).admin`, `(*e2e).want`, `(*e2e).wantProblem`, `(*e2e).cli`, `reply.problemCode`; from `server_test.go`: `start`, `lockedBuffer`; from `listeners_test.go`: `baseURL`; from `server.go`: `loadSealer`; P10 (`daemon.Enroll`, `daemon.Run`, `daemon.Config`, `daemon.ErrRefused`, `daemon.EnrollTimeout`, `transport.BackoffMax`); C1 (`registry.CoreActions`).
- Produces: test names only, package `server`: `eventually`, `noRecovery`, `fleetAgent` (`newFleetAgent`, `(*fleetAgent).enroll`, `run`, `hello`, `skipWaits`), `(*e2e).loginAdmin`, `(*e2e).applyPlanned`, `(*e2e).host`, `TestFleetEndToEnd`.

No production code changes. `make verify-build` runs this test beside `TestEndToEnd`. The controller's fake clock starts at the real time, because the agent and the controller verify certificates against the real clock; the authority backdates what it issues by an hour, which covers the `auth.TOTPPeriod` the login advances. Each agent has its own fake clock, which `skipWaits` advances by `transport.BackoffMax` between polls so the dial loop's backoff and `CredentialPoll` never sleep; the heartbeat runs on the real clock and is not affected. The second agent reports version `0.1.0`, which a `dev` controller cannot compare with, so it is outside the support window: this shows `dispatch.Config.Version` and `coreactions.Deps.Version` wired, which no other test of the package can (the embedded agent and the first daemon report the controller's own version).

**Items:** 1, 2, 86
**Depends on:** Task 1, Task 10, Task 11
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

- [ ] **Step 1: Write the failing test**

Create `internal/controller/server/fleet_e2e_test.go` with this complete content:

```go
package server

import (
	"context"
	"encoding/json"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	agentapi "github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// eventually polls cond every 50 ms for at most 30 s, running tick between
// polls when it is set, and fails the test when cond never holds.
func eventually(t *testing.T, what string, tick func(), cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s: not within 30 s", what)
		}
		if tick != nil {
			tick()
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// noRecovery is the daemon's update recovery when no update ever ran.
type noRecovery struct{}

func (noRecovery) Recover(context.Context, update.RecoverMode, string) error { return nil }

// fleetAgent is a remote host's agent daemon, run in this process as
// darkpanel agent run builds it, over its own layout, agent.db and clock,
// reporting version as its binary's.
type fleetAgent struct {
	t       *testing.T
	version string
	paths   paths.Paths
	db      *store.DB
	clock   *coretest.FakeClock
	logs    *lockedBuffer
	stop    func()
}

func newFleetAgent(t *testing.T, version string) *fleetAgent {
	t.Helper()
	p := paths.Under(t.TempDir())
	sockDir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sockDir) })
	p.AgentSocket = filepath.Join(sockDir, "agent.sock")
	if err := os.MkdirAll(p.AgentDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(context.Background(), p.AgentDB())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return &fleetAgent{t: t, version: version, paths: p, db: db, clock: coretest.NewFakeClock(time.Now().UTC()), logs: &lockedBuffer{}}
}

// enroll consumes token into the agent's credential directory.
func (a *fleetAgent) enroll(token string) {
	a.t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), daemon.EnrollTimeout)
	defer cancel()
	if _, err := daemon.Enroll(ctx, a.paths.AgentTLSDir, token); err != nil {
		a.t.Fatalf("enroll: %v", err)
	}
}

// run starts the daemon until a.stop or the end of the test.
func (a *fleetAgent) run() {
	a.t.Helper()
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Extra: registry.CoreActions(nil, ""), OwnedPaths: install.CoreOwnedPaths(),
	}))
	if err != nil {
		a.t.Fatal(err)
	}
	sealer, err := loadSealer(a.paths.AgentKey)
	if err != nil {
		a.t.Fatal(err)
	}
	uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
	cfg := daemon.Config{
		Paths: a.paths,
		Runtime: runtime.Config{
			DB: a.db, Sealer: sealer, Clock: a.clock, Log: slog.New(slog.NewTextHandler(a.logs, nil)),
			Registry: reg, BinaryVersion: a.version, BackupDir: a.paths.AgentBackups(),
			Exec: platform.NewRunner(platform.ProductionAllowlist()),
		},
		Updater:   noRecovery{},
		AllowPeer: func(peer uint32) bool { return peer == uid },
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- daemon.Run(ctx, cfg) }()
	var once sync.Once
	a.stop = func() {
		once.Do(func() {
			cancel()
			if err := <-done; err != nil {
				a.t.Errorf("daemon.Run: %v", err)
			}
		})
	}
	a.t.Cleanup(a.stop)
}

// hello asks the agent socket for Hello.
func (a *fleetAgent) hello() (core.AgentHello, error) {
	c := agentapi.NewClient(agentapi.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", a.paths.AgentSocket)
	}})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Hello(ctx)
}

// skipWaits advances the agent's clock past its longest redial backoff and
// credential poll, so its dial loop never sleeps for long.
func (a *fleetAgent) skipWaits() { a.clock.Advance(transport.BackoffMax) }

// loginAdmin creates the first admin through the controller socket with the
// CLI, as TestEndToEnd does, and logs it in over HTTP.
func (e *e2e) loginAdmin() {
	e.t.Helper()
	sock := e.s.SocketPath()
	out, errOut, code := e.cli(nil, adminPassword+"\n", "user", "create", "--username", "admin", "--socket", sock, "--yes")
	m := secretLine.FindStringSubmatch(out)
	if code != cli.ExitUsage || m == nil {
		e.t.Fatalf("user create: exit %d\n%s\n%s", code, out, errOut)
	}
	totp, err := auth.TOTPCode(m[1], e.clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	if out, errOut, code := e.cli(nil, totp+"\n", "user", "activate", "--username", "admin", "--socket", sock, "--yes"); code != cli.ExitOK {
		e.t.Fatalf("user activate: exit %d\n%s\n%s", code, out, errOut)
	}
	e.clock.Advance(auth.TOTPPeriod)
	totp, _ = auth.TOTPCode(m[1], e.clock.Now())
	r := e.do("POST", "/api/v1/auth/login", map[string]string{"username": "admin", "password": adminPassword, "code": totp}, credentials{})
	var login struct {
		CSRFToken string `json:"csrfToken"`
	}
	e.want(r, 200, &login)
	for _, c := range (&http.Response{Header: r.header}).Cookies() {
		if c.Name == api.SessionCookiePlain {
			e.session = c.Value
		}
	}
	e.csrf = login.CSRFToken
}

// applyPlanned plans and applies a controller-side action with the admin
// session, answering the plan's confirmation phrase when it has one.
func (e *e2e) applyPlanned(name string, input any) change {
	e.t.Helper()
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/plan", map[string]any{"input": input}, e.admin()), 200, &plan)
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		body["confirmation"] = *plan.Confirmation
	}
	var ch change
	e.want(e.do("POST", "/api/v1/actions/"+name+"/v1/apply", body, e.admin()), 200, &ch)
	if ch.State != "completed" {
		e.t.Fatalf("%s: change %s is %s", name, ch.ID, ch.State)
	}
	return ch
}

// host reads one host of core.host.list over the network API.
func (e *e2e) host(id string) core.Host {
	e.t.Helper()
	var list struct {
		Output core.HostList `json:"output"`
	}
	e.want(e.do("POST", "/api/v1/actions/core.host.list/v1/read", map[string]any{"input": map[string]any{}}, e.admin()), 200, &list)
	for _, h := range list.Output.Hosts {
		if h.ID == id {
			return h
		}
	}
	e.t.Fatalf("core.host.list has no host %s", id)
	return core.Host{}
}

// TestFleetEndToEnd drives a controller-only server and real agent daemons
// over the agent listener's mutual TLS: enrollment, the host list, a
// resource through the agent API, the support window, offline answers,
// reconnection, revocation, re-enrollment and the agent socket without a
// controller.
func TestFleetEndToEnd(t *testing.T) {
	began := time.Now()
	opts := testOptions(t)
	// Certificates are checked against the real time, so the controller's
	// clock starts there; the authority backdates what it issues by an hour,
	// which covers the TOTP step the login advances.
	opts.Clock = coretest.NewFakeClock(time.Now().UTC().Truncate(time.Second))
	e := &e2e{t: t, clock: opts.Clock.(*coretest.FakeClock), home: t.TempDir()}
	e.s = start(t, opts)
	e.url = baseURL(t, e.s)
	e.loginAdmin()

	agentAddr := ""
	for _, b := range e.s.AgentListeners() {
		if b.Endpoint.Addr == netip.MustParseAddrPort("127.0.0.1:7443") {
			agentAddr = b.Addr.String()
		}
	}
	if agentAddr == "" {
		t.Fatalf("no loopback agent listener in %v", e.s.AgentListeners())
	}
	createHost := func(name string) core.EnrollmentGrant {
		t.Helper()
		var grant core.EnrollmentGrant
		created := e.applyPlanned("core.host.create", map[string]any{"name": name, "controllerAddress": agentAddr})
		if err := json.Unmarshal(created.Output, &grant); err != nil || grant.Token == "" {
			t.Fatalf("core.host.create output %s (%v), want a token", created.Output, err)
		}
		return grant
	}
	grant := createHost("agent1")
	hostID := grant.Host.ID
	online := func() bool { return e.host(hostID).Online }
	offline := func() bool { return !e.host(hostID).Online }

	// Enrollment and the first session (R22, R23, R27).
	agent := newFleetAgent(t, buildinfo.Version)
	agent.enroll(grant.Token)
	agent.run()
	eventually(t, "the enrolled agent online", agent.skipWaits, online)
	h := e.host(hostID)
	if h.State != core.HostEnrolled || h.AgentVersion == nil || *h.AgentVersion != buildinfo.Version ||
		h.ProtocolVersion == nil || *h.ProtocolVersion != runtime.ProtocolVersion || h.OS == nil || *h.OS == "" || h.Arch == nil || *h.Arch == "" ||
		!h.Supported {
		t.Errorf("host after its agent connected: %+v, want enrolled and supported with its versions and platform", h)
	}

	// A resource through the controller, mTLS and the agent API (R22, R26).
	var summary struct {
		Hostname         string `json:"hostname"`
		DarkpanelVersion string `json:"darkpanelVersion"`
	}
	e.want(e.do("GET", "/api/v1/hosts/"+hostID+"/resources/hostinfo/summary", nil, e.admin()), 200, &summary)
	if summary.Hostname == "" || summary.DarkpanelVersion != buildinfo.Version {
		t.Errorf("hostinfo.summary over the agent transport = %+v", summary)
	}

	// An agent this controller cannot compare its version with is outside
	// the support window: it is listed unsupported and gets only the update
	// actions (R24, R25).
	oldGrant := createHost("old1")
	oldID := oldGrant.Host.ID
	old := newFleetAgent(t, "0.1.0")
	old.enroll(oldGrant.Token)
	old.run()
	eventually(t, "the old agent online", old.skipWaits, func() bool { return e.host(oldID).Online })
	if h := e.host(oldID); h.Supported {
		t.Errorf("host of agent 0.1.0 under a %s controller: %+v, want unsupported", buildinfo.Version, h)
	}
	e.wantProblem(e.do("GET", "/api/v1/hosts/"+oldID+"/resources/hostinfo/summary", nil, e.admin()), 409, errcode.HostVersionUnsupported)
	// The test's agents run no updater, so the agent itself answers
	// unavailable: the call passed the support window.
	r := e.do("POST", "/api/v1/hosts/"+oldID+"/actions/core.update.status/v1/read", map[string]any{"input": map[string]any{}}, e.admin())
	e.wantProblem(r, 503, errcode.Unavailable)
	old.stop()

	// A stopped agent is offline, and its actions fail at once (R27).
	agent.stop()
	eventually(t, "the stopped agent offline", nil, offline)
	asked := time.Now()
	r = e.do("POST", "/api/v1/hosts/"+hostID+"/actions/core.update.status/v1/read", map[string]any{"input": map[string]any{}}, e.admin())
	if took := time.Since(asked); took > 5*time.Second {
		t.Errorf("an action on the offline host took %v", took)
	}
	e.wantProblem(r, 503, errcode.HostOffline)

	// A restarted agent reconnects.
	agent.run()
	eventually(t, "the restarted agent online", agent.skipWaits, online)

	// Revocation closes the session and the controller refuses the redial
	// (R23).
	e.applyPlanned("core.host.revoke", map[string]any{"hostId": hostID})
	eventually(t, "the revoked agent offline", nil, offline)
	eventually(t, "the revoked agent's redial refused", agent.skipWaits, func() bool {
		return strings.Contains(agent.logs.String(), daemon.ErrRefused.Error())
	})
	if h := e.host(hostID); h.State != core.HostRevoked || h.Online {
		t.Errorf("host after its redial was refused: %+v, want revoked and offline", h)
	}

	// A new token re-enrolls the host; the running daemon takes up the new
	// credentials (R23).
	reissued := e.applyPlanned("core.host.token", map[string]any{"hostId": hostID, "controllerAddress": agentAddr})
	if err := json.Unmarshal(reissued.Output, &grant); err != nil || grant.Token == "" {
		t.Fatalf("core.host.token output %s (%v), want a token", reissued.Output, err)
	}
	agent.enroll(grant.Token)
	eventually(t, "the re-enrolled agent online", agent.skipWaits, online)
	if h := e.host(hostID); h.State != core.HostEnrolled {
		t.Errorf("host after re-enrollment: %+v, want enrolled", h)
	}

	// The agent keeps serving its socket while the controller is down (R28).
	if err := e.s.Shutdown(context.Background()); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
	eventually(t, "the agent socket reporting no controller", nil, func() bool {
		hello, err := agent.hello()
		return err == nil && hello.ControllerConnected != nil && !*hello.ControllerConnected
	})
	t.Logf("the fleet end-to-end test took %v", time.Since(began))
}
```

In `Makefile`, replace:

```makefile
	file bin/darkpanel-linux-arm64 | grep -F 'ELF 64-bit LSB executable, ARM aarch64' | grep -qF 'statically linked'
	for arch in $(ARCHES); do grep -qaF '$(UI_MARKER)' bin/darkpanel-linux-$$arch; done
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 go test -count=1 -run '^TestEndToEnd$$' ./internal/controller/server/
	bin/darkpanel-linux-$$(go env GOARCH) version --json | grep -qF '"commit":"$(COMMIT)"'

lint-go:
```

with:

```makefile
	file bin/darkpanel-linux-arm64 | grep -F 'ELF 64-bit LSB executable, ARM aarch64' | grep -qF 'statically linked'
	for arch in $(ARCHES); do grep -qaF '$(UI_MARKER)' bin/darkpanel-linux-$$arch; done
	DARKPANEL_REQUIRE_UI=1 go test -count=1 ./web/
	DARKPANEL_REQUIRE_UI=1 go test -count=1 -run '^(TestEndToEnd|TestFleetEndToEnd)$$' ./internal/controller/server/
	bin/darkpanel-linux-$$(go env GOARCH) version --json | grep -qF '"commit":"$(COMMIT)"'

lint-go:
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 timeout 600 go test -count=1 -v -run '^TestFleetEndToEnd$' ./internal/controller/server/`
Expected: PASS, with the line `the fleet end-to-end test took` about 1.2 s. This task adds no production code: Tasks 10 and 11 already wired what the test drives, so it passes at once. A failure is a wiring bug of Task 10 or 11: report it with the failing line; do not change the test to pass.

- [ ] **Step 3: Write the implementation**

None: the test is the task. The `Makefile` block above is the whole remaining change.

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 timeout 600 go test -race -count=1 ./internal/controller/server/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 timeout 600 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/12 go mod tidy -diff`
Expected: no output, exit 0

Run: `make -n verify-build | grep -F TestFleetEndToEnd`
Expected: `DARKPANEL_REQUIRE_UI=1 go test -count=1 -run '^(TestEndToEnd|TestFleetEndToEnd)$' ./internal/controller/server/`

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/fleet_e2e_test.go Makefile
git commit -m "test(server): add the fleet end-to-end test"
```

---

### Task 13: Version fields and selftest

**Files:**
- Create: `cmd/darkpanel/selftest.go`
- Modify: `cmd/darkpanel/main.go`
- Test: `cmd/darkpanel/version_test.go`
- Test: `cmd/darkpanel/selftest_test.go`

**Interfaces:**
- Consumes: `trustedkeys.Keys()`, `trustedkeys.TestBuild()` (`internal/update/trustedkeys`); `registry.Build()`; `store.CoreMigrations`, `store.SideController`, `store.SideAgent`; `schemas.FS`. The release workflow's `releasetool check-build <version> <key id>[,<key id>] < version.json` (Task 7) reads the `version`, `testBuild` and `trustedKeys` fields this task adds.
- Produces: C13. `darkpanel version --json` gains `"testBuild"` and `"trustedKeys"` (the `ID` of every trusted key, in order; `[]` when none). `darkpanel selftest` prints `selftest: ok` and exits 0, else exits 1 with `darkpanel: selftest failed the <check> check: <reason>`; the checks are named `registry`, `migrations`, `schemas` and `trusted keys`. New names in package `main` (`cmd/darkpanel`): variable `trustedKeys` (a seam, `= trustedkeys.Keys`), fields `versionInfo.TestBuild` and `versionInfo.TrustedKeys`, type `selfTestCheck`, variable `selfTestChecks`, functions `newSelftestCmd`, `checkCoreMigrations`, `checkSchemaSnapshot`, `checkTrustedKeys`; test helper `withTrustedKeys(t *testing.T, keys []trustedkeys.Key)`; tests `TestVersionTrustedKeys`, `TestSelftest`.

`selftest` requires a trusted key, and a default build (no `harnesskeys` tag) trusts none until the owner adds the production key, so in such a build `darkpanel selftest` fails with the `trusted keys` check. That is intended: the updater runs `<new binary> selftest` on the staged binary before it swaps, and a binary that trusts no key could never verify, and so never install, another update. Every release binary trusts the keys that sign it (`releasetool check-build` refuses an empty or different list), and a running binary with no key cannot verify a release, so it never stages one; the check therefore rejects only a new binary that would strand the host. The tests set the trusted keys through the `trustedKeys` seam, so they pass in every build.

**Items:** 34
**Depends on:** Task 7
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

In `cmd/darkpanel/version_test.go`, replace:

```go
	"runtime"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
)
```

with:

```go
	"runtime"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)
```

In `cmd/darkpanel/version_test.go`, replace:

```go
	if len(got) != len(want)+1 {
		t.Errorf("version --json has keys %v, want exactly version, commit, go, os, arch, ui", got)
	}
}
```

with:

```go
	if got["testBuild"] != trustedkeys.TestBuild() {
		t.Errorf("version --json \"testBuild\" = %v, want %v", got["testBuild"], trustedkeys.TestBuild())
	}
	if _, ok := got["trustedKeys"].([]any); !ok {
		t.Errorf("version --json \"trustedKeys\" = %v, want a list", got["trustedKeys"])
	}
	if len(got) != len(want)+3 {
		t.Errorf("version --json has keys %v, want exactly version, commit, go, os, arch, ui, testBuild, trustedKeys", got)
	}
}

// withTrustedKeys makes the build trust keys for one test.
func withTrustedKeys(t *testing.T, keys []trustedkeys.Key) {
	t.Helper()
	old := trustedKeys
	t.Cleanup(func() { trustedKeys = old })
	trustedKeys = func() []trustedkeys.Key { return keys }
}

func TestVersionTrustedKeys(t *testing.T) {
	tests := []struct {
		name string
		keys []trustedkeys.Key
		want string
	}{
		{"none", nil, `"trustedKeys":[]`},
		{"in order", []trustedkeys.Key{{ID: "00000000000000B2"}, {ID: "00000000000000A1"}}, `"trustedKeys":["00000000000000B2","00000000000000A1"]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedKeys(t, tt.keys)
			if got := runRoot(t, "version", "--json"); !strings.Contains(got, tt.want) {
				t.Errorf("version --json = %s, want it to contain %s", got, tt.want)
			}
		})
	}
}
```

Create `cmd/darkpanel/selftest_test.go`:

```go
package main

import (
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestSelftest(t *testing.T) {
	harnessKey := trustedkeys.Key{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"}
	tests := []struct {
		name string
		keys []trustedkeys.Key
		code int
		out  string
	}{
		{"passes", []trustedkeys.Key{harnessKey}, cli.ExitOK, "selftest: ok\n"},
		{"no trusted key", nil, cli.ExitError, "darkpanel: selftest failed the trusted keys check: this build trusts no release signing key, so it could never verify an update\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			withTrustedKeys(t, tt.keys)
			out, code := runArgs("selftest")
			if code != tt.code || out != tt.out {
				t.Errorf("darkpanel selftest: exit %d, output %q; want exit %d and %q", code, out, tt.code, tt.out)
			}
		})
	}
	if out, code := runArgs("selftest", "extra"); code != cli.ExitUsage || !strings.Contains(out, `unknown command "extra"`) {
		t.Errorf("darkpanel selftest extra: exit %d, output %q; want a usage error", code, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 600 go test -count=1 -run 'TestVersion|TestSelftest' ./cmd/darkpanel/`
Expected: FAIL with `cmd/darkpanel/version_test.go:68:9: undefined: trustedKeys`

- [ ] **Step 3: Write the implementation**

In `cmd/darkpanel/main.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/web"
)
```

with:

```go
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
	"github.com/darkraise/darkpanel/web"
)
```

In `cmd/darkpanel/main.go`, replace:

```go
// geteuid is the effective uid serve checks; tests replace it.
var geteuid = os.Geteuid
```

with:

```go
// geteuid is the effective uid serve checks; tests replace it.
var geteuid = os.Geteuid

// trustedKeys lists the release keys version and selftest report; tests
// replace it.
var trustedKeys = trustedkeys.Keys
```

In `cmd/darkpanel/main.go`, replace:

```go
	UI      bool   `json:"ui"`
}

func currentVersion() versionInfo {
	return versionInfo{
```

with:

```go
	UI      bool   `json:"ui"`
	// TestBuild and TrustedKeys are what the release workflow checks before
	// it publishes a binary.
	TestBuild   bool     `json:"testBuild"`
	TrustedKeys []string `json:"trustedKeys"`
}

func currentVersion() versionInfo {
	ids := []string{}
	for _, k := range trustedKeys() {
		ids = append(ids, k.ID)
	}
	return versionInfo{
```

In `cmd/darkpanel/main.go`, replace:

```go
		UI: uiEmbedded(web.Dist),
	}
}
```

with:

```go
		UI:          uiEmbedded(web.Dist),
		TestBuild:   trustedkeys.TestBuild(),
		TrustedKeys: ids,
	}
}
```

In `cmd/darkpanel/main.go`, replace:

```go
// registry.Build() plus the hand-written serve and version.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd()}
```

with:

```go
// registry.Build() plus the hand-written serve, version and selftest.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd()}
```

Create `cmd/darkpanel/selftest.go`:

```go
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/schemas"
)

// selfTestCheck is one check of selftest, named in its failure.
type selfTestCheck struct {
	name string
	run  func() error
}

// selfTestChecks are the checks the updater relies on before it switches a
// host to a new binary: they need no database, socket or network, so they
// pass on any host the binary was built for.
var selfTestChecks = []selfTestCheck{
	{"registry", func() error {
		_, err := registry.Build()
		return err
	}},
	{"migrations", checkCoreMigrations},
	{"schemas", checkSchemaSnapshot},
	{"trusted keys", checkTrustedKeys},
}

func newSelftestCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "selftest",
		Short: "Check that this binary can run; the updater runs it on a new binary before switching to it",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			for _, c := range selfTestChecks {
				if err := c.run(); err != nil {
					return fmt.Errorf("selftest failed the %s check: %w", c.name, err)
				}
			}
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "selftest: ok")
			return err
		},
	}
}

func checkCoreMigrations() error {
	for _, side := range []string{store.SideController, store.SideAgent} {
		set, err := store.CoreMigrations(side)
		if err != nil {
			return err
		}
		files, err := fs.Glob(set.FS, "*.sql")
		if err != nil {
			return err
		}
		if len(files) == 0 {
			return fmt.Errorf("no %s core migrations are embedded", side)
		}
	}
	return nil
}

func checkSchemaSnapshot() error {
	files, err := fs.Glob(schemas.FS, "*/v*.json")
	if err != nil {
		return err
	}
	if len(files) == 0 {
		return errors.New("no schema snapshots are embedded")
	}
	data, err := fs.ReadFile(schemas.FS, files[0])
	if err != nil {
		return err
	}
	if !json.Valid(data) {
		return fmt.Errorf("schema snapshot %s is not valid JSON", files[0])
	}
	return nil
}

// checkTrustedKeys refuses a binary that trusts no release key: once the
// updater switched a host to it, the host could never verify, and so never
// install, another update.
func checkTrustedKeys() error {
	if len(trustedKeys()) == 0 {
		return errors.New("this build trusts no release signing key, so it could never verify an update")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 600 go test -race -count=1 ./cmd/darkpanel/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

Check that the release workflow's build check reads the new fields. Run:

```bash
export GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/13
B=$TMPDIR/bin; mkdir -p $B
timeout 600 go build -o $B/rt ./release/releasetool
timeout 600 go build -tags harnesskeys -ldflags "-X github.com/darkraise/darkpanel/internal/buildinfo.Version=0.2.0" -o $B/dp ./cmd/darkpanel
$B/dp selftest
$B/dp version --json | $B/rt check-build 0.2.0 BCB10AB3E9AF694E
$B/dp version --json | sed 's/"testBuild":true/"testBuild":false/' | $B/rt check-build 0.2.0 BCB10AB3E9AF694E && echo accepted
rm -rf $B
```

Expected: `selftest: ok`, then `releasetool: the release binary is a test build` (exit 1: the only refusal), then `accepted`.

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/main.go cmd/darkpanel/version_test.go cmd/darkpanel/selftest.go cmd/darkpanel/selftest_test.go
git commit -m "feat(cmd): add selftest and release key fields"
```

---

### Task 14: serve flags

**Files:**
- Modify: `cmd/darkpanel/main.go`
- Test: `cmd/darkpanel/serve_test.go`

**Interfaces:**
- Consumes: C10 `server.Options.EmbeddedAgent`, `AgentListen`, `AgentAdvertise` (Task 10); `listen.ParseAgentListen`; `install.CheckAgentAddress`.
- Produces: C14. `darkpanel serve` gains `--agent-listen <ip:port>` (repeatable), `--agent-advertise <host:port>` and `--embedded-agent`; its `Short` is `Run the controller`. Refusals, all exit 2 from `PreRunE`: a bad `--agent-listen` (the `listen:` error), a bad `--agent-advertise` (the `install:` error), `--embedded-agent` without `--dev` (`--embedded-agent runs the M1 in-process agent and needs --dev`), `--embedded-agent` with `--agent-listen` (`--agent-listen does not apply with --embedded-agent, which opens no agent listener`), and production `serve` as euid 0 (`serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev to use per-user development paths`). No new top-level names.

The production refusal is inverted from M1: the controller unit runs `serve` as `User=darkpanel` (`internal/install/units/darkpanel-controller.service`), and the unit's drop-in `10-agent.conf` passes `--agent-listen` and `--agent-advertise`; `TestServeProductionOptions` runs exactly those arguments. `TestExitCodes` now fakes euid 0, so plain `serve` is the root refusal and only `serve --dev` reaches `serveRun`.

**Depends on:** Task 10
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

- [ ] **Step 1: Write the failing test**

In `cmd/darkpanel/serve_test.go`, replace:

```go
	"bytes"
	"context"
	"errors"
	"path/filepath"
	"slices"
	"strings"
```

with:

```go
	"bytes"
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"slices"
	"strings"
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
// TestExitCodes is register row 30: usage errors exit 2, failed runs 1.
func TestExitCodes(t *testing.T) {
	served := fakeServe(t, 1000, errors.New("the controller database is locked"))
	tests := []struct {
		args []string
		code int
```

with:

```go
// TestExitCodes is register row 30: usage errors exit 2, failed runs 1.
func TestExitCodes(t *testing.T) {
	served := fakeServe(t, 0, errors.New("the controller database is locked"))
	tests := []struct {
		args []string
		code int
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
		{[]string{"serve", "--nope"}, cli.ExitUsage, "unknown flag: --nope"},
		{[]string{"serve", "--dev", "--listen", "http://192.0.2.1:80"}, cli.ExitUsage, "plain HTTP is allowed only on a loopback address"},
		{[]string{"serve", "--dev", "--listen", "ftp://127.0.0.1:21"}, cli.ExitUsage, "is not loopback, tailscale"},
		{[]string{"serve"}, cli.ExitUsage, "serve runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"serve", "--dev"}, cli.ExitError, "darkpanel: the controller database is locked"},
		{[]string{"token", "list", "--help"}, cli.ExitOK, "List"},
		{[]string{"hostinfo", "summary", "get", "--help"}, cli.ExitOK, "--host"},
```

with:

```go
		{[]string{"serve", "--nope"}, cli.ExitUsage, "unknown flag: --nope"},
		{[]string{"serve", "--dev", "--listen", "http://192.0.2.1:80"}, cli.ExitUsage, "plain HTTP is allowed only on a loopback address"},
		{[]string{"serve", "--dev", "--listen", "ftp://127.0.0.1:21"}, cli.ExitUsage, "is not loopback, tailscale"},
		{[]string{"serve"}, cli.ExitUsage, "serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev"},
		{[]string{"serve", "--embedded-agent"}, cli.ExitUsage, "--embedded-agent runs the M1 in-process agent and needs --dev"},
		{[]string{"serve", "--dev", "--agent-listen", "0.0.0.0:7443"}, cli.ExitUsage, "the address must be a specific address of this host"},
		{[]string{"serve", "--dev", "--agent-listen", "panel.example:7443"}, cli.ExitUsage, `--agent-listen "panel.example:7443" is not <ip>:<port>`},
		{[]string{"serve", "--dev", "--agent-advertise", "panel.example"}, cli.ExitUsage, `agent address "panel.example" is not host:port`},
		{[]string{"serve", "--dev", "--embedded-agent", "--agent-listen", "192.0.2.7:7443"}, cli.ExitUsage, "--agent-listen does not apply with --embedded-agent, which opens no agent listener"},
		{[]string{"serve", "--dev"}, cli.ExitError, "darkpanel: the controller database is locked"},
		{[]string{"token", "list", "--help"}, cli.ExitOK, "List"},
		{[]string{"hostinfo", "summary", "get", "--help"}, cli.ExitOK, "--host"},
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
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
```

with:

```go
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	served := fakeServe(t, 0, nil)
	if out, code := runArgs("serve", "--dev", "--listen", "loopback", "--listen", "https://127.0.0.1:9443"); code != cli.ExitOK {
		t.Fatalf("serve --dev: exit %d: %s", code, out)
	}
	if out, code := runArgs("serve", "--dev", "--embedded-agent"); code != cli.ExitOK {
		t.Fatalf("serve --dev --embedded-agent: exit %d: %s", code, out)
	}
	if len(*served) != 2 {
		t.Fatalf("serve ran %d times, want twice", len(*served))
	}
	opts := (*served)[0]
	var listens []string
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
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

with:

```go
		listens = append(listens, s.String())
	}
	if opts.Paths != paths.Under(filepath.Join(state, "darkpanel-dev")) || !opts.Dev || opts.Log == nil ||
		!slices.Equal(listens, []string{"loopback", "https://127.0.0.1:9443"}) || opts.EmbeddedAgent {
		t.Errorf("serve --dev passed %+v, want the dev paths, Dev, a logger, both listen values and no embedded agent", opts)
	}
	if embedded := (*served)[1]; !embedded.EmbeddedAgent || !embedded.Dev {
		t.Errorf("serve --dev --embedded-agent passed %+v, want EmbeddedAgent and Dev", embedded)
	}
}

// TestServeProductionOptions runs serve with the arguments of the
// controller unit's agent listener drop-in, as the darkpanel user.
func TestServeProductionOptions(t *testing.T) {
	served := fakeServe(t, 998, nil)
	args := []string{"serve", "--agent-listen", "192.0.2.7:7443", "--agent-listen", "[2001:db8::7]:7443",
		"--agent-listen", "192.0.2.7:7443", "--agent-advertise", "panel.example:7443"}
	if out, code := runArgs(args...); code != cli.ExitOK {
		t.Fatalf("serve as the darkpanel user: exit %d: %s", code, out)
	}
	opts := (*served)[0]
	var listens []string
	for _, s := range opts.Listen {
		listens = append(listens, s.String())
	}
	wantAgent := []netip.AddrPort{netip.MustParseAddrPort("192.0.2.7:7443"), netip.MustParseAddrPort("[2001:db8::7]:7443")}
	if opts.Paths != paths.Production() || opts.Dev || !slices.Equal(listens, []string{"loopback", "tailscale"}) ||
		!slices.Equal(opts.AgentListen, wantAgent) || opts.AgentAdvertise != "panel.example:7443" ||
		opts.EmbeddedAgent || opts.HealthStatus != "" {
		t.Errorf("serve passed %+v, want the production paths, the default listeners, agent listen %v and the advertised address", opts, wantAgent)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 600 go test -count=1 -run 'TestExitCodes|TestServe' ./cmd/darkpanel/`
Expected: FAIL with `serve_test.go:69: darkpanel serve: exit 1, output "darkpanel: the controller database is locked\n"; want exit 2 and "serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev"` (and `unknown flag: --embedded-agent` / `unknown flag: --agent-listen` rows)

- [ ] **Step 3: Write the implementation**

In `cmd/darkpanel/main.go`, replace:

```go
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"runtime"
```

with:

```go
	"io"
	"io/fs"
	"log/slog"
	"net/netip"
	"os"
	"os/signal"
	"runtime"
```

In `cmd/darkpanel/main.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
```

with:

```go
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/controller/server"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
```

In `cmd/darkpanel/main.go`, replace:

```go
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
```

with:

```go
func newServeCmd() *cobra.Command {
	var (
		dev            bool
		embeddedAgent  bool
		listenFlags    []string
		agentFlags     []string
		agentAdvertise string
		specs          []listen.Spec
		agentListen    []netip.AddrPort
	)
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the controller",
		Args:  cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
```

In `cmd/darkpanel/main.go`, replace:

```go
			if specs, err = listen.ParseSpecs(listenFlags); err != nil {
				return err
			}
			if !dev && geteuid() != 0 {
				return errors.New("serve runs as root in production mode; run it with sudo, or add --dev to use per-user development paths")
			}
			return nil
		},
```

with:

```go
			if specs, err = listen.ParseSpecs(listenFlags); err != nil {
				return err
			}
			if agentListen, err = listen.ParseAgentListen(agentFlags); err != nil {
				return err
			}
			if agentAdvertise != "" {
				if err := install.CheckAgentAddress(agentAdvertise); err != nil {
					return err
				}
			}
			if embeddedAgent && !dev {
				return errors.New("--embedded-agent runs the M1 in-process agent and needs --dev")
			}
			if embeddedAgent && len(agentListen) > 0 {
				return errors.New("--agent-listen does not apply with --embedded-agent, which opens no agent listener")
			}
			// Run as root, serve would create controller files that the
			// darkpanel user's unit could not open afterwards.
			if !dev && geteuid() == 0 {
				return errors.New("serve runs as the darkpanel user in production; start it with systemctl start darkpanel-controller, or add --dev to use per-user development paths")
			}
			return nil
		},
```

In `cmd/darkpanel/main.go`, replace:

```go
			// interrupt end a shutdown that hangs.
			context.AfterFunc(ctx, stop)
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return serveRun(ctx, server.Options{Paths: p, Listen: specs, Dev: dev, Log: log})
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listenFlags, "listen", nil,
		"listener to open instead of the defaults (repeatable): loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>")
	return cmd
}
```

with:

```go
			// interrupt end a shutdown that hangs.
			context.AfterFunc(ctx, stop)
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			return serveRun(ctx, server.Options{
				Paths: p, Listen: specs, Dev: dev, Log: log,
				EmbeddedAgent: embeddedAgent, AgentListen: agentListen, AgentAdvertise: agentAdvertise,
			})
		},
	}
	cmd.Flags().BoolVar(&dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
	cmd.Flags().StringArrayVar(&listenFlags, "listen", nil,
		"listener to open instead of the defaults (repeatable): loopback, tailscale, https://<ip>:<port> or http://<loopback ip>:<port>")
	cmd.Flags().StringArrayVar(&agentFlags, "agent-listen", nil,
		"extra address of this host for the agent listener, <ip>:<port> (repeatable); 127.0.0.1:7443 and the Tailscale addresses are always bound")
	cmd.Flags().StringVar(&agentAdvertise, "agent-advertise", "",
		"<host>:<port> that enrollment tokens tell agents to connect to when the request names none")
	cmd.Flags().BoolVar(&embeddedAgent, "embedded-agent", false,
		"run the M1 in-process agent over this user's agent database instead of the agent listener (needs --dev)")
	return cmd
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 600 go test -race -count=1 ./cmd/darkpanel/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/14 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/main.go cmd/darkpanel/serve_test.go
git commit -m "feat(cmd): add the agent listener flags to serve"
```

---

### Task 15: agent commands

**Files:**
- Create: `cmd/darkpanel/agent.go`
- Modify: `cmd/darkpanel/main.go`
- Test: `cmd/darkpanel/agent_test.go`

**Interfaces:**
- Consumes: `daemon.Run`, `daemon.Config` (`Paths`, `Runtime`, `Updater`, `AllowPeer`), `daemon.Enroll`, `daemon.Check`, `daemon.ReadTokenFile`, `daemon.Credentials.HostID`; `runtime.Config`; `update.New`, `update.Config`, `update.Probes`, `update.ControllerProbe`, `update.NewIndexSource`, `update.ControllerUnit`, `update.ActionStatus`; `platform.NewSystemd`, `platform.Systemd`, `platform.NewRunner`, `platform.ProductionAllowlist`; `install.Production`, `install.Rooted`, `install.Layout.UnitPath`, `install.CoreOwnedPaths`, `install.CheckResult`; `registry.CoreActions`, `registry.BuildWith`, `coreactions.Module`, `coreactions.Deps`; `secret.LoadOrCreateKey`, `secret.NewSealer`; `store.Open`; agent `api.NewClient`; C6 `cli.ExitWith`; C13 `trustedKeys` (Task 13); `geteuid`, `runArgs` (existing).
- Produces: C15. `darkpanel agent run [--dev]`, `agent enroll [--token-file <path>] [--dev]`, `agent check [--dev]`. New names in package `main`, for Tasks 16 and 17 to reuse: seams `daemonRun`, `daemonEnroll`, `daemonCheck`, `newSystemd` (`func(ctx context.Context) (systemdConn, error)`), `newUpdater` (`= update.New`), `resolvePaths` (`= paths.Resolve`), `installLayout` (`= install.Production`), `stdinTerminal` (`func(io.Reader) bool`), `readHidden` (`func(io.Reader) (string, error)`); type `systemdConn` (`platform.Systemd` plus `Close() error`); variable `errDevSystemd` (`agent run --dev does not manage systemd units`) and type `devSystemd` (every `platform.Systemd` method fails with it; `Close` returns nil); constant `maxStdinToken`; type `agentHost` (fields `DB`, `Sealer`, `Updater`, `Registry`, `Close`); functions `openAgent(ctx context.Context, p paths.Paths, log *slog.Logger) (*agentHost, error)`, `newAgentCmd`, `runGroup(cmd *cobra.Command, args []string) error` (the RunE of a hand-written group: exit 2 for an unknown subcommand), `requireRoot(name string, dev *bool)`, `addDevFlag`, `newAgentRunCmd`, `newAgentEnrollCmd`, `readToken`, `newAgentCheckCmd`, `checkWords`. Test helpers: type `fakeSystemd` (embeds `platform.Systemd`, counts `Close` in `closed`), type `agentSeams` (its `systemdCalls` counts calls of the `newSystemd` seam), `fakeAgent(t *testing.T, euid int) *agentSeams`, `runStdin(stdin io.Reader, args ...string) (stdout, stderr string, code int)`; tests `TestAgentRefusals`, `TestAgentRunDev`, `TestAgentRunProduction`, `TestAgentEnrollToken`, `TestAgentCheckExit`.

**Items:** 18
**Depends on:** Task 1, Task 6, Task 13
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Notes for the implementer:
- `openAgent` takes no agent lock (only `daemon.Run` does), so `update run` and `update recover` (Task 16) can open agent.db beside the running daemon. It creates the agent's missing directories (`AgentDir`, the key's directory, and `Paths.MkdirAgent`'s credential, update and socket directories) with mode 0700, so `agent run --dev` works on an empty layout; on an installed host they exist and `system install` sets their modes.
- A development agent (any `p` other than the installed layout's paths) never connects to D-Bus: its updater gets `devSystemd`, whose every method fails with `errDevSystemd`, so its update actions fail cleanly and its start recovery only logs. Only the installed layout calls `newSystemd` (`platform.NewSystemd`).
- The controller probe is set only when `p` is the installed layout's paths and that layout's controller unit file exists; `installLayout` and `resolvePaths` let the test use `install.Rooted(t.TempDir())` as the installed layout, so no test touches `/etc`, `/var/lib` or `/run`.
- Under `--dev`, `AllowPeer` admits root and the invoking uid; production passes nil (root alone).
- `agent enroll` promises no pickup time: a daemon with a live session takes the new credentials only after that session ends.
- The `agent` group has a `RunE` (`runGroup`) because cobra reports an unknown subcommand by itself only below the root command.

- [ ] **Step 1: Write the failing test**

Create `cmd/darkpanel/agent_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/transport"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeSystemd is a systemd provider that fails the test when the code under
// test manages a unit; it only counts Close.
type fakeSystemd struct {
	platform.Systemd
	closed int
}

func (f *fakeSystemd) Close() error {
	f.closed++
	return nil
}

// agentSeams replaces the agent commands' seams for one test: geteuid
// answers euid, the production paths are a rooted layout under a temporary
// directory, and the daemon calls are recorded instead of run.
type agentSeams struct {
	layout  install.Layout
	dev     paths.Paths
	systemd *fakeSystemd
	// systemdCalls counts the connections to the systemd provider.
	systemdCalls int
	runs         []daemon.Config
	updates      []update.Config
	// statusErr is core.update.status run on the registry the daemon got.
	statusErr error
	enrolls   []string // "<dir> <token>"
	checkRes  install.CheckResult
	checkErr  error
}

func fakeAgent(t *testing.T, euid int) *agentSeams {
	t.Helper()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv("XDG_RUNTIME_DIR", "")
	s := &agentSeams{
		layout:  install.Rooted(t.TempDir()),
		dev:     paths.Under(filepath.Join(state, "darkpanel-dev")),
		systemd: &fakeSystemd{},
	}
	oldEUID, oldRun, oldEnroll, oldCheck := geteuid, daemonRun, daemonEnroll, daemonCheck
	oldSystemd, oldUpdater, oldResolve, oldLayout := newSystemd, newUpdater, resolvePaths, installLayout
	t.Cleanup(func() {
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, resolvePaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
	geteuid = func() int { return euid }
	installLayout = func() install.Layout { return s.layout }
	resolvePaths = func(dev bool) (paths.Paths, error) {
		if dev {
			return paths.Resolve(true)
		}
		return s.layout.Paths, nil
	}
	newSystemd = func(context.Context) (systemdConn, error) {
		s.systemdCalls++
		return s.systemd, nil
	}
	newUpdater = func(cfg update.Config) (*update.Updater, error) {
		s.updates = append(s.updates, cfg)
		return update.New(cfg)
	}
	daemonRun = func(ctx context.Context, cfg daemon.Config) error {
		s.runs = append(s.runs, cfg)
		def, ok := cfg.Runtime.Registry.Action(core.ActionRef{Name: update.ActionStatus, Version: 1})
		if !ok {
			t.Errorf("the agent registry has no %s", update.ActionStatus)
			return nil
		}
		_, s.statusErr = def.Handler.Read(ctx, core.Env{}, json.RawMessage(`{}`))
		return nil
	}
	daemonEnroll = func(_ context.Context, dir, token string) (*daemon.Credentials, error) {
		s.enrolls = append(s.enrolls, dir+" "+token)
		uri, err := url.Parse(transport.HostURI("01HZY5S4ZK8V3J1M2N3P4Q5R6S"))
		if err != nil {
			t.Fatal(err)
		}
		return &daemon.Credentials{Cert: &x509.Certificate{URIs: []*url.URL{uri}}}, nil
	}
	daemonCheck = func(context.Context, string) (install.CheckResult, error) { return s.checkRes, s.checkErr }
	return s
}

func runStdin(stdin io.Reader, args ...string) (stdout, stderr string, code int) {
	var out, errOut bytes.Buffer
	code = run(args, cli.Options{Stdin: stdin, Stdout: &out, Stderr: &errOut})
	return out.String(), errOut.String(), code
}

func TestAgentRefusals(t *testing.T) {
	s := fakeAgent(t, 1000)
	tests := []struct {
		args []string
		code int
		out  string
	}{
		{[]string{"agent", "run"}, cli.ExitUsage, "agent run runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"agent", "enroll"}, cli.ExitUsage, "agent enroll runs as root in production mode; run it with sudo, or add --dev"},
		{[]string{"agent", "run", "extra"}, cli.ExitUsage, `unknown command "extra"`},
		{[]string{"agent", "nope"}, cli.ExitUsage, `unknown command "nope"`},
	}
	for _, tt := range tests {
		out, code := runArgs(tt.args...)
		if code != tt.code || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit %d and %q", strings.Join(tt.args, " "), code, out, tt.code, tt.out)
		}
	}
	if len(s.runs) != 0 || len(s.enrolls) != 0 {
		t.Errorf("refused commands ran the daemon %d times and enrolled %d times", len(s.runs), len(s.enrolls))
	}
}

func TestAgentRunDev(t *testing.T) {
	s := fakeAgent(t, 1000)
	if out, code := runArgs("agent", "run", "--dev"); code != cli.ExitOK {
		t.Fatalf("agent run --dev: exit %d: %s", code, out)
	}
	if len(s.runs) != 1 || len(s.updates) != 1 {
		t.Fatalf("agent run --dev ran the daemon %d times over %d updaters, want once", len(s.runs), len(s.updates))
	}
	cfg, upd := s.runs[0], s.updates[0]
	if cfg.Paths != s.dev || cfg.Runtime.DB == nil || cfg.Runtime.Sealer == nil || cfg.Runtime.Registry == nil ||
		cfg.Runtime.BackupDir != s.dev.AgentBackups() || cfg.Runtime.Exec == nil || cfg.Updater == nil {
		t.Errorf("agent run --dev passed %+v, want the dev paths and an opened runtime", cfg)
	}
	if cfg.AllowPeer == nil || !cfg.AllowPeer(uint32(os.Getuid())) || !cfg.AllowPeer(0) || cfg.AllowPeer(uint32(os.Getuid())+4242) { //nolint:gosec // G115: a uid fits in 32 bits
		t.Error("agent run --dev must admit root and the invoking user on the agent socket, and no one else")
	}
	if s.statusErr != nil {
		t.Errorf("core.update.status on the agent registry: %v, want it to run on the agent's updater", s.statusErr)
	}
	if upd.Paths != s.dev || upd.HTTP != nil || upd.Index == nil ||
		upd.Probes.Agent == nil || upd.Probes.Controller != nil || upd.NewerAgents != nil {
		t.Errorf("the dev updater config is %+v, want the dev paths, no HTTP client, an index, only the agent probe", upd)
	}
	// A development agent never reaches the host's systemd: its updater's
	// provider refuses every unit operation.
	if s.systemdCalls != 0 {
		t.Errorf("agent run --dev connected to systemd %d times, want never", s.systemdCalls)
	}
	ctx := context.Background()
	if err := upd.Systemd.Restart(ctx, update.AgentUnit); !errors.Is(err, errDevSystemd) {
		t.Errorf("the dev updater's Restart = %v, want %v", err, errDevSystemd)
	}
	if _, err := upd.Systemd.State(ctx, update.ControllerUnit); !errors.Is(err, errDevSystemd) {
		t.Errorf("the dev updater's State = %v, want %v", err, errDevSystemd)
	}
	for _, d := range []string{s.dev.AgentDir, s.dev.AgentTLSDir, s.dev.UpdateDir, filepath.Dir(s.dev.AgentSocket)} {
		if st, err := os.Stat(d); err != nil || !st.IsDir() || st.Mode().Perm() != 0o700 {
			t.Errorf("agent run --dev left %s as %v, %v; want a directory with mode 0700", d, st, err)
		}
	}
	if s.systemd.closed != 0 {
		t.Errorf("the systemd seam closed %d times, want never", s.systemd.closed)
	}
}

func TestAgentRunProduction(t *testing.T) {
	for _, withController := range []bool{false, true} {
		s := fakeAgent(t, 0)
		if withController {
			unit := s.layout.UnitPath(update.ControllerUnit)
			if err := os.MkdirAll(filepath.Dir(unit), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(unit, []byte("[Unit]\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		if out, code := runArgs("agent", "run"); code != cli.ExitOK {
			t.Fatalf("agent run as root: exit %d: %s", code, out)
		}
		cfg, upd := s.runs[0], s.updates[0]
		if s.systemdCalls != 1 || upd.Systemd != s.systemd || s.systemd.closed != 1 {
			t.Errorf("agent run connected to systemd %d times and closed it %d times, want the systemd seam once", s.systemdCalls, s.systemd.closed)
		}
		if cfg.Paths != s.layout.Paths || cfg.AllowPeer != nil {
			t.Errorf("agent run passed paths %+v and AllowPeer set %v; want the installed paths and root alone", cfg.Paths, cfg.AllowPeer != nil)
		}
		if got := upd.Probes.Controller != nil; got != withController {
			t.Errorf("controller unit installed %v: the updater probes the controller %v", withController, got)
		}
	}
}

func TestAgentEnrollToken(t *testing.T) {
	dir := t.TempDir()
	writeToken := func(name string, mode os.FileMode) string {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("  file-token\n"), mode); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, mode); err != nil {
			t.Fatal(err)
		}
		return path
	}
	private, shared := writeToken("private", 0o600), writeToken("shared", 0o644)
	tests := []struct {
		name     string
		args     []string
		stdin    string
		terminal bool
		code     int
		token    string // "" when the command must not enroll
		stderr   string
	}{
		{"token file", []string{"--token-file", private}, "", false, cli.ExitOK, "file-token", ""},
		{"token file readable by others", []string{"--token-file", shared}, "", false, cli.ExitError, "", "has mode 0644; want 0600 or stricter"},
		{"standard input", nil, "\n  piped-token \n", false, cli.ExitOK, "piped-token", ""},
		{"empty standard input", nil, " \n", false, cli.ExitError, "", "no enrollment token on standard input"},
		{"standard input too large", nil, strings.Repeat("x", maxStdinToken+1), false, cli.ExitError, "", "larger than an enrollment token"},
		{"terminal prompt", nil, "", true, cli.ExitOK, "typed-token", "Enrollment token: \n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 1000)
			oldTerm, oldRead := stdinTerminal, readHidden
			t.Cleanup(func() { stdinTerminal, readHidden = oldTerm, oldRead })
			stdinTerminal = func(io.Reader) bool { return tt.terminal }
			readHidden = func(io.Reader) (string, error) { return " typed-token ", nil }
			args := append([]string{"agent", "enroll", "--dev"}, tt.args...)
			stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), args...)
			if code != tt.code || !strings.Contains(stderr, tt.stderr) {
				t.Fatalf("exit %d, stderr %q; want exit %d and %q", code, stderr, tt.code, tt.stderr)
			}
			if tt.token == "" {
				if len(s.enrolls) != 0 {
					t.Errorf("enrolled %v, want no enrollment", s.enrolls)
				}
				return
			}
			if want := []string{s.dev.AgentTLSDir + " " + tt.token}; len(s.enrolls) != 1 || s.enrolls[0] != want[0] {
				t.Errorf("enrolled %q, want %q", s.enrolls, want)
			}
			want := "enrolled as host 01HZY5S4ZK8V3J1M2N3P4Q5R6S\n" +
				"A running agent uses the new credentials from its next connection attempt; it needs no restart.\n"
			if stdout != want {
				t.Errorf("stdout %q, want %q", stdout, want)
			}
		})
	}
}

func TestAgentCheckExit(t *testing.T) {
	tests := []struct {
		res    install.CheckResult
		err    error
		code   int
		stdout string
		stderr string
	}{
		{install.CheckAccepted, nil, 0, "accepted: the controller accepted this agent's credentials\n", ""},
		{install.CheckRefused, errors.New("daemon: the controller refused this agent's credentials"), 1,
			"refused: the controller refused this agent's credentials, or there are none; enroll again with a new token\n",
			"darkpanel: daemon: the controller refused this agent's credentials\n"},
		{install.CheckUnreachable, errors.New("dial tcp 192.0.2.1:7443: i/o timeout"), 2,
			"unreachable: no handshake with the controller completed\n", "darkpanel: dial tcp 192.0.2.1:7443: i/o timeout\n"},
	}
	for _, tt := range tests {
		s := fakeAgent(t, 1000)
		s.checkRes, s.checkErr = tt.res, tt.err
		stdout, stderr, code := runStdin(strings.NewReader(""), "agent", "check", "--dev")
		if code != tt.code || stdout != tt.stdout || stderr != tt.stderr {
			t.Errorf("check result %d: exit %d, stdout %q, stderr %q; want exit %d, %q and %q",
				tt.res, code, stdout, stderr, tt.code, tt.stdout, tt.stderr)
		}
	}
	out, code := runArgs("agent", "check", "--help")
	if code != cli.ExitOK || !strings.Contains(out, "replaces a running agent's session for a moment") {
		t.Errorf("agent check --help: exit %d, output %q; want the warning about the running agent's session", code, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 600 go test -count=1 -run 'TestAgent' ./cmd/darkpanel/`
Expected: FAIL with `cmd/darkpanel/agent_test.go:66:51: undefined: daemonRun`

- [ ] **Step 3: Write the implementation**

Create `cmd/darkpanel/agent.go`:

```go
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/agent/daemon"
	"github.com/darkraise/darkpanel/internal/agent/runtime"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/secret"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/platform"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

// The agent commands' seams; tests replace them.
var (
	daemonRun    = daemon.Run
	daemonEnroll = daemon.Enroll
	daemonCheck  = daemon.Check
	newSystemd   = func(ctx context.Context) (systemdConn, error) { return platform.NewSystemd(ctx) }
	newUpdater   = update.New
	resolvePaths = paths.Resolve
	// installLayout is the installed layout, whose controller unit file
	// tells an agent that the controller runs on its host.
	installLayout = install.Production
	// stdinTerminal reports whether r is an interactive terminal.
	stdinTerminal = func(r io.Reader) bool {
		f, ok := r.(*os.File)
		return ok && term.IsTerminal(int(f.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
	}
	// readHidden reads one line from the terminal r without echo.
	readHidden = func(r io.Reader) (string, error) {
		f, ok := r.(*os.File)
		if !ok {
			return "", errors.New("standard input is not a terminal")
		}
		b, err := term.ReadPassword(int(f.Fd())) //nolint:gosec // G115: a file descriptor fits in an int
		return string(b), err
	}
)

// systemdConn is the systemd provider and its connection's Close.
type systemdConn interface {
	platform.Systemd
	Close() error
}

// errDevSystemd is every unit operation of a development agent's updater.
var errDevSystemd = errors.New("agent run --dev does not manage systemd units")

// devSystemd is the systemd provider of a development agent: the host's units
// belong to the installed services, so a development agent's update actions
// fail and its start recovery only logs.
type devSystemd struct{}

func (devSystemd) StartTransientService(context.Context, string, platform.TransientService) error {
	return errDevSystemd
}

func (devSystemd) StartTransientTimer(context.Context, string, platform.TransientTimer) error {
	return errDevSystemd
}

func (devSystemd) Start(context.Context, string) error                { return errDevSystemd }
func (devSystemd) Stop(context.Context, string) error                 { return errDevSystemd }
func (devSystemd) Restart(context.Context, string) error              { return errDevSystemd }
func (devSystemd) Kill(context.Context, string, syscall.Signal) error { return errDevSystemd }
func (devSystemd) ResetFailed(context.Context, string) error          { return errDevSystemd }
func (devSystemd) Enable(context.Context, ...string) error            { return errDevSystemd }
func (devSystemd) Disable(context.Context, ...string) error           { return errDevSystemd }
func (devSystemd) Reload(context.Context) error                       { return errDevSystemd }
func (devSystemd) Close() error                                       { return nil }
func (devSystemd) State(context.Context, string) (platform.UnitState, error) {
	return platform.UnitState{}, errDevSystemd
}

// maxStdinToken is more than any token, as daemon.ReadTokenFile allows.
const maxStdinToken = 4096

// agentHost is what every agent-side command opens: the agent database,
// the agent key's sealer, the updater and the registry whose update actions
// run on that updater.
type agentHost struct {
	DB       *store.DB
	Sealer   *secret.Sealer
	Updater  *update.Updater
	Registry *core.Registry
	Close    func() error
}

// openAgent opens the agent's state at p without the agent lock, which only
// darkpanel agent run takes: the update runner and recovery open agent.db
// beside the running daemon. It creates the agent's missing directories, as
// system install does, so a development layout works from an empty state.
func openAgent(ctx context.Context, p paths.Paths, log *slog.Logger) (*agentHost, error) {
	for _, d := range []string{p.AgentDir, filepath.Dir(p.AgentKey)} {
		if err := os.MkdirAll(d, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", d, err)
		}
	}
	if err := p.MkdirAgent(); err != nil {
		return nil, err
	}
	key, err := secret.LoadOrCreateKey(p.AgentKey)
	if err != nil {
		return nil, err
	}
	sealer, err := secret.NewSealer(key)
	if err != nil {
		return nil, err
	}
	db, err := store.Open(ctx, p.AgentDB())
	if err != nil {
		return nil, err
	}
	// Only the installed layout's agent manages the host's units.
	l := installLayout()
	installed := p == l.Paths
	var sd systemdConn = devSystemd{}
	if installed {
		if sd, err = newSystemd(ctx); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	closeAll := func() error { return errors.Join(sd.Close(), db.Close()) }
	agentSocket := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", p.AgentSocket)
	}})
	probes := update.Probes{Agent: agentSocket.Hello}
	// Only the installed layout has a controller unit; a development agent
	// never waits for a controller after an update.
	if installed {
		if _, err := os.Stat(l.UnitPath(update.ControllerUnit)); err == nil {
			probes.Controller = update.ControllerProbe(p.ControllerSocket)
		}
	}
	upd, err := newUpdater(update.Config{
		Paths: p, DB: db, Systemd: sd, Clock: core.SystemClock{}, Log: log,
		Version: buildinfo.Version, Arch: goruntime.GOARCH,
		BaseURL: trustedkeys.BaseURL(), Keys: trustedKeys(),
		Index:  update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{}),
		Probes: probes,
	})
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	reg, err := registry.BuildWith(coreactions.Module(coreactions.Deps{
		Extra: registry.CoreActions(upd, ""), OwnedPaths: install.CoreOwnedPaths(),
	}))
	if err != nil {
		_ = closeAll()
		return nil, err
	}
	return &agentHost{DB: db, Sealer: sealer, Updater: upd, Registry: reg, Close: closeAll}, nil
}

func newAgentCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run, enroll and check this host's agent",
		RunE:  runGroup,
	}
	cmd.AddCommand(newAgentRunCmd(), newAgentEnrollCmd(), newAgentCheckCmd())
	return cmd
}

// runGroup is the RunE of a hand-written command group: cobra checks for an
// unknown subcommand only below the root, so a group refuses one itself and
// exits 2 as the generated groups do.
func runGroup(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return cli.ExitWith(cli.ExitUsage, fmt.Errorf("unknown command %q for %q", args[0], cmd.CommandPath()))
	}
	return cmd.Help()
}

// requireRoot is the PreRunE of the agent commands that change the agent's
// root-owned state; cli exits 2 for it, like a flag error.
func requireRoot(name string, dev *bool) func(*cobra.Command, []string) error {
	return func(*cobra.Command, []string) error {
		if !*dev && geteuid() != 0 {
			return fmt.Errorf("agent %s runs as root in production mode; run it with sudo, or add --dev", name)
		}
		return nil
	}
}

func addDevFlag(cmd *cobra.Command, dev *bool) {
	cmd.Flags().BoolVar(dev, "dev", false, "use per-user development paths under $XDG_STATE_HOME/darkpanel-dev")
}

func newAgentRunCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:     "run",
		Short:   "Run the agent: serve the agent socket and keep a session to the controller",
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("run", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return err
			}
			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()
			// Releasing the signals once shutdown starts lets a second
			// interrupt end a shutdown that hangs.
			context.AfterFunc(ctx, stop)
			log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
			host, err := openAgent(ctx, p, log)
			if err != nil {
				return err
			}
			cfg := daemon.Config{
				Paths: p,
				Runtime: runtime.Config{
					DB: host.DB, Sealer: host.Sealer, Clock: core.SystemClock{}, Log: log, Registry: host.Registry,
					BinaryVersion: buildinfo.Version, BackupDir: p.AgentBackups(),
					Exec: platform.NewRunner(platform.ProductionAllowlist()),
				},
				Updater: host.Updater,
			}
			if dev {
				// The development agent runs as the invoking user, whose own
				// CLI must reach the socket.
				uid := uint32(os.Getuid()) //nolint:gosec // G115: a uid fits in 32 bits
				cfg.AllowPeer = func(peer uint32) bool { return peer == 0 || peer == uid }
			}
			return errors.Join(daemonRun(ctx, cfg), host.Close())
		},
	}
	addDevFlag(cmd, &dev)
	return cmd
}

func newAgentEnrollCmd() *cobra.Command {
	var (
		dev       bool
		tokenFile string
	)
	cmd := &cobra.Command{
		Use:   "enroll",
		Short: "Enroll this host's agent with an enrollment token",
		Long: "Enroll this host's agent with an enrollment token from --token-file, a hidden prompt on the " +
			"terminal, or standard input. A running agent uses the new credentials from its next connection " +
			"attempt; it needs no restart.",
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("enroll", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return err
			}
			token, err := readToken(cmd, tokenFile, dev)
			if err != nil {
				return err
			}
			creds, err := daemonEnroll(cmd.Context(), p.AgentTLSDir, token)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintf(cmd.OutOrStdout(), "enrolled as host %s\n"+
				"A running agent uses the new credentials from its next connection attempt; it needs no restart.\n",
				creds.HostID())
			return err
		},
	}
	addDevFlag(cmd, &dev)
	cmd.Flags().StringVar(&tokenFile, "token-file", "", "read the token from this file, owned by root with mode 0600 or stricter")
	return cmd
}

// readToken reads the enrollment token from tokenFile, else from a hidden
// prompt when standard input is a terminal, else from standard input.
func readToken(cmd *cobra.Command, tokenFile string, dev bool) (string, error) {
	if tokenFile != "" {
		uid := 0
		if dev {
			uid = os.Getuid()
		}
		return daemon.ReadTokenFile(tokenFile, uid)
	}
	in := cmd.InOrStdin()
	if stdinTerminal(in) {
		_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Enrollment token: ")
		token, err := readHidden(in)
		_, _ = fmt.Fprintln(cmd.ErrOrStderr())
		if err != nil {
			return "", err
		}
		if token = strings.TrimSpace(token); token == "" {
			return "", errors.New("no enrollment token was entered")
		}
		return token, nil
	}
	data, err := io.ReadAll(io.LimitReader(in, maxStdinToken+1))
	if err != nil {
		return "", fmt.Errorf("read the token from standard input: %w", err)
	}
	token := strings.TrimSpace(string(data))
	switch {
	case len(data) > maxStdinToken:
		return "", errors.New("standard input is larger than an enrollment token")
	case token == "":
		return "", errors.New("no enrollment token on standard input; pass --token-file or run on a terminal")
	}
	return token, nil
}

func newAgentCheckCmd() *cobra.Command {
	var dev bool
	cmd := &cobra.Command{
		Use:   "check",
		Short: "Check that the controller accepts this agent's credentials",
		Long: "Check that the controller accepts this agent's credentials with one handshake. The controller " +
			"treats the check as this host's session, so it replaces a running agent's session for a moment " +
			"and the agent reconnects.\n\n" +
			"Exit status: 0 accepted, 1 refused (enroll again with a new token), 2 unreachable.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return err
			}
			res, err := daemonCheck(cmd.Context(), p.AgentTLSDir)
			if _, werr := fmt.Fprintln(cmd.OutOrStdout(), checkWords(res)); werr != nil && err == nil {
				err = werr
			}
			return cli.ExitWith(int(res), err)
		},
	}
	addDevFlag(cmd, &dev)
	return cmd
}

func checkWords(res install.CheckResult) string {
	switch res {
	case install.CheckAccepted:
		return "accepted: the controller accepted this agent's credentials"
	case install.CheckRefused:
		return "refused: the controller refused this agent's credentials, or there are none; enroll again with a new token"
	default:
		return "unreachable: no handshake with the controller completed"
	}
}
```

In `cmd/darkpanel/main.go`, replace:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version and selftest.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd()}
	return cli.NewRoot(opts)
}
```

with:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest and agent.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd()}
	return cli.NewRoot(opts)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 600 go test -race -count=1 ./cmd/darkpanel/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/15 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/agent.go cmd/darkpanel/agent_test.go cmd/darkpanel/main.go
git commit -m "feat(cmd): add the agent run, enroll and check commands"
```

---

### Task 16: update commands

**Files:**
- Create: `cmd/darkpanel/update.go`
- Modify: `cmd/darkpanel/main.go`
- Modify: `cmd/darkpanel/agent.go`
- Test: `cmd/darkpanel/update_test.go`

**Interfaces:**
- Consumes: C15 `openAgent`, `agentHost`, `resolvePaths`, `runGroup`, `fakeAgent`, `agentSeams.updates`, `agentSeams.layout` (Task 15); C6 `cli.Options.Adopt`, `cli.Options.LocalFallbacks`, `cli.LocalFallback`, `cli.Options.AgentSocket`, `cli.FindCommand`, `cli.ExitWith` (Task 6); C1 the six adopted actions (`update.ActionApply`, `update.ActionRollback`, `update.ActionStatus`, `rollout.ActionRollout`, `rollout.ActionRolloutCancel`, `rollout.ActionRollouts`); `update.Updater.Run`, `Recover`, `RunRollback`, `DirectRollback`; `update.RecoverBoot`, `RecoverFailure`, `RecoverDeadline`; `update.ParseVersion`, `update.Version.Compare`; `update.Config.NewerAgents`; `errcode.UpdateNothingToRollBack`; `store.Open`, `store.Migrate`, `store.CoreMigrations`; `install.Rooted`; `geteuid`, `runArgs`, `runRoot` (existing).
- Produces: C16. `darkpanel update run <tx>`, `update recover [--boot | --deadline <tx>]`, the adopted `update rollback` with `--direct` and `--transaction <tx>`, the `core.update.rollback` local fallback, and `update.Config.NewerAgents` on the controller's host. `cmd/darkpanel` leaves `cli.Options.AgentSocket` empty, so `--local` dials the production agent socket; `--local` has no development mode. New names in package `main`: variables `updateActions`, `openUpdater`; type `hostUpdater`; functions `withUpdater`, `rootRefusal`, `newUpdateCmd`, `newUpdateRunCmd`, `newUpdateRecoverCmd`, `addDirectRollback(root *cobra.Command) error`, `directRollback`, `rollbackFallback(stderr io.Writer) cli.LocalFallback`, `newerAgents(controllerDB string) func(ctx context.Context, version string) ([]string, error)`. Test helpers: type `fakeUpdater`, `fakeUpdate(t *testing.T, euid int) (*fakeUpdater, install.Layout)`, `writePrevBinary(t *testing.T, p paths.Paths)`, `migratedControllerDB(t *testing.T, path string, versions map[string]string)`; tests `TestUpdateCommands`, `TestUpdateRollbackFallback`, `TestUpdateHelp`, `TestNewerAgents`, `TestNewerAgentsKeepsOwner`, `TestOpenAgentNewerAgents`.

**Items:** 43, 68
**Depends on:** Task 1, Task 6, Task 15
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Notes for the implementer:
- The argv the units and runners use: `darkpanel update run <tx>` (`internal/update/updater.go:328`, `runner.go:322`), `darkpanel.recovery update recover --deadline <tx>` (`runner.go:215`), `darkpanel update rollback --direct --transaction <tx>` (`rollback.go:279`), `update recover` (`darkpanel-rollback.service`) and `update recover --boot` (`darkpanel-update-recover.service`). They all act on the installed binary, so none has `--dev`, and all refuse a non-root euid with exit 2.
- `update run` and `update recover` open the updater through `openAgent`, which takes no agent lock.
- `addDirectRollback` runs after `cli.NewRoot`, and only replaces the `RunE` of the generated command (its annotations and flags stay). Every run without `--direct`, including `--local`, goes to the generated `RunE`, so the agent socket and the local fallback still serve it. The `RunE` it installs is outside the cli's own marking of run errors, so its errors carry their exit status through `cli.ExitWith` (usage errors 2, failures 1).
- The local fallback re-executes `PrevBinary()` with `update rollback --direct --json [--force]`, and a `--direct` run never runs the fallback, so the re-execution cannot loop. The child's standard error passes through. Its standard output is the fallback's output.
- `NewerAgents` opens controller.db only for the query. Run as root, SQLite gives the `-wal` and `-shm` files it creates the database file's owner; `TestNewerAgentsKeepsOwner` checks that on a database owned by uid 65534 (it skips when not root).

- [ ] **Step 1: Write the failing test**

Create `cmd/darkpanel/update_test.go`:

```go
package main

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// fakeUpdater records the update commands' calls.
type fakeUpdater struct {
	calls []string
	paths []paths.Paths
}

func (f *fakeUpdater) Run(_ context.Context, tx string) error {
	f.calls = append(f.calls, "run "+tx)
	return nil
}

func (f *fakeUpdater) Recover(_ context.Context, mode update.RecoverMode, tx string) error {
	f.calls = append(f.calls, fmt.Sprintf("recover %d %q", mode, tx))
	return nil
}

func (f *fakeUpdater) RunRollback(_ context.Context, tx string) error {
	f.calls = append(f.calls, "run-rollback "+tx)
	return nil
}

func (f *fakeUpdater) DirectRollback(_ context.Context, force bool) (core.UpdateTransaction, error) {
	f.calls = append(f.calls, fmt.Sprintf("direct-rollback force=%v", force))
	return core.UpdateTransaction{ID: "01HZY6TX", Kind: core.UpdateKindRollback, FromVersion: "0.3.0", ToVersion: "0.2.0", State: core.UpdateRolledBack}, nil
}

// fakeUpdate replaces the update commands' seams for one test: geteuid
// answers euid and the installed layout is a rooted one.
func fakeUpdate(t *testing.T, euid int) (*fakeUpdater, install.Layout) {
	t.Helper()
	f := &fakeUpdater{}
	layout := install.Rooted(t.TempDir())
	oldEUID, oldResolve, oldOpen := geteuid, resolvePaths, openUpdater
	t.Cleanup(func() { geteuid, resolvePaths, openUpdater = oldEUID, oldResolve, oldOpen })
	geteuid = func() int { return euid }
	resolvePaths = func(dev bool) (paths.Paths, error) {
		if dev {
			t.Error("an update command resolved the development paths")
		}
		return layout.Paths, nil
	}
	openUpdater = func(_ context.Context, p paths.Paths, _ *slog.Logger) (hostUpdater, func() error, error) {
		f.paths = append(f.paths, p)
		return f, func() error { return nil }, nil
	}
	return f, layout
}

func TestUpdateCommands(t *testing.T) {
	tests := []struct {
		args  []string
		euid  int
		code  int
		calls []string
		out   string
	}{
		{[]string{"update", "run", "01HZY6TX"}, 0, cli.ExitOK, []string{"run 01HZY6TX"}, ""},
		{[]string{"update", "run"}, 0, cli.ExitUsage, nil, "accepts 1 arg(s), received 0"},
		{[]string{"update", "run", "01HZY6TX"}, 1000, cli.ExitUsage, nil, "update run acts on the installed binary and runs as root; run it with sudo"},
		{[]string{"update", "recover"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverFailure, "")}, ""},
		{[]string{"update", "recover", "--boot"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverBoot, "")}, ""},
		{[]string{"update", "recover", "--deadline", "01HZY6TX"}, 0, cli.ExitOK, []string{fmt.Sprintf("recover %d %q", update.RecoverDeadline, "01HZY6TX")}, ""},
		{[]string{"update", "recover", "--boot", "--deadline", "01HZY6TX"}, 0, cli.ExitUsage, nil, "--boot and --deadline exclude each other"},
		{[]string{"update", "recover"}, 1000, cli.ExitUsage, nil, "update recover acts on the installed binary and runs as root"},
		{[]string{"update", "rollback", "--direct"}, 0, cli.ExitOK, []string{"direct-rollback force=false"}, "rollback 01HZY6TX from 0.3.0 to 0.2.0: rolled-back\n"},
		{[]string{"update", "rollback", "--direct", "--force", "--json"}, 0, cli.ExitOK, []string{"direct-rollback force=true"}, `"state":"rolled-back"`},
		{[]string{"update", "rollback", "--direct", "--transaction", "01HZY6RB"}, 0, cli.ExitOK, []string{"run-rollback 01HZY6RB"}, ""},
		{[]string{"update", "rollback", "--transaction", "01HZY6RB"}, 0, cli.ExitUsage, nil, "--transaction needs --direct"},
		{[]string{"update", "rollback", "--direct", "--host", "web1"}, 0, cli.ExitUsage, nil, "--host does not apply with --direct"},
		{[]string{"update", "rollback", "--direct"}, 1000, cli.ExitUsage, nil, "update rollback acts on the installed binary and runs as root"},
		{[]string{"update", "nope"}, 0, cli.ExitUsage, nil, `unknown command "nope" for "darkpanel update"`},
	}
	for _, tt := range tests {
		t.Run(strings.Join(tt.args[1:], " "), func(t *testing.T) {
			f, layout := fakeUpdate(t, tt.euid)
			// A previous binary is installed, so --direct must not hand the
			// rollback to it.
			writePrevBinary(t, layout.Paths)
			out, code := runArgs(tt.args...)
			if code != tt.code || !strings.Contains(out, tt.out) || !slices.Equal(f.calls, tt.calls) {
				t.Errorf("exit %d, output %q, calls %q; want exit %d, %q and calls %q", code, out, f.calls, tt.code, tt.out, tt.calls)
			}
			for _, p := range f.paths {
				if p != layout.Paths {
					t.Errorf("opened the updater on %+v, want the installed paths", p)
				}
			}
			if _, err := os.Stat(layout.Paths.PrevBinary() + ".args"); err == nil {
				t.Error("the previous binary ran")
			}
		})
	}
}

// writePrevBinary installs a previous binary that records its arguments
// beside itself and prints a rollback transaction.
func writePrevBinary(t *testing.T, p paths.Paths) {
	t.Helper()
	if err := os.MkdirAll(p.BinaryDir, 0o700); err != nil {
		t.Fatal(err)
	}
	script := `#!/bin/sh
printf '%s\n' "$*" > "$0.args"
echo 'darkpanel.prev: rolling back' >&2
printf '{"id":"01HZY6RB","operationId":"01HZY6RB","parentId":null,"kind":"rollback","fromVersion":"0.3.0","toVersion":"0.2.0","targetVersion":"0.2.0","state":"rolled-back","error":null,"startedAt":"2026-10-20T00:00:00Z","updatedAt":"2026-10-20T00:00:01Z"}\n'
`
	if err := os.WriteFile(p.PrevBinary(), []byte(script), 0o700); err != nil { //nolint:gosec // G306: the fake binary must be executable
		t.Fatal(err)
	}
}

func TestUpdateRollbackFallback(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "agent.sock")
	tests := []struct {
		name    string
		prev    bool
		args    []string
		code    int
		argv    string // the previous binary's arguments; "" when it must not run
		stdout  string
		stderr  string
		wantErr string
	}{
		{"previous binary", true, []string{"--yes", "--json"}, cli.ExitOK, "update rollback --direct --json\n", `"state":"rolled-back"`, "darkpanel.prev: rolling back\n", ""},
		{"previous binary with force", true, []string{"--yes", "--force", "--json"}, cli.ExitOK, "update rollback --direct --json --force\n", `"toVersion":"0.2.0"`, "", ""},
		{"no previous binary", false, []string{"--yes"}, cli.ExitError, "", "", "", "there is no previous binary to roll back to (update.nothing_to_roll_back)"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, layout := fakeUpdate(t, 0)
			if tt.prev {
				writePrevBinary(t, layout.Paths)
			}
			var stdout, stderr bytes.Buffer
			args := append([]string{"update", "rollback", "--local"}, tt.args...)
			code := run(args, cli.Options{AgentSocket: missing, Stdin: strings.NewReader(""), Stdout: &stdout, Stderr: &stderr})
			if code != tt.code || !strings.Contains(stdout.String(), tt.stdout) ||
				!strings.Contains(stderr.String(), tt.stderr) || !strings.Contains(stderr.String(), tt.wantErr) {
				t.Fatalf("exit %d, stdout %q, stderr %q; want exit %d, %q and %q %q",
					code, stdout.String(), stderr.String(), tt.code, tt.stdout, tt.stderr, tt.wantErr)
			}
			if !strings.Contains(stderr.String(), "running core.update.rollback in this process") {
				t.Errorf("stderr %q does not say the rollback runs in this process", stderr.String())
			}
			argv, err := os.ReadFile(layout.Paths.PrevBinary() + ".args")
			if got := string(argv); (err == nil) != (tt.argv != "") || got != tt.argv {
				t.Errorf("the previous binary ran with %q (%v), want %q", got, err, tt.argv)
			}
		})
	}
}

func TestUpdateHelp(t *testing.T) {
	out := runRoot(t, "update", "--help")
	for _, sub := range []string{"run", "recover", "apply", "rollback", "status", "rollout", "rollout-cancel", "rollouts"} {
		if !strings.Contains(out, "\n  "+sub+" ") {
			t.Errorf("darkpanel update --help does not list %s:\n%s", sub, out)
		}
	}
	out = runRoot(t, "update", "rollback", "--help")
	for _, flag := range []string{"--direct", "--transaction string", "--force", "--local"} {
		if !strings.Contains(out, flag) {
			t.Errorf("darkpanel update rollback --help does not list %s", flag)
		}
	}
}

// migratedControllerDB returns a migrated controller.db at path holding one
// host per entry of versions, keyed by name: "<state> <agent version>",
// with the version "-" for none.
func migratedControllerDB(t *testing.T, path string, versions map[string]string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	set, err := store.CoreMigrations(store.SideController)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "0.3.0", BackupRoot: filepath.Join(filepath.Dir(path), "backups"),
		Keep: 1, Now: time.Now(),
	}, set, nil); err != nil {
		t.Fatal(err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error {
		i := 0
		for name, v := range versions {
			state, agent, _ := strings.Cut(v, " ")
			var version any = agent
			if agent == "-" {
				version = nil
			}
			i++
			if _, err := tx.Exec(`INSERT INTO core_hosts (id, name, created_at, state, agent_version) VALUES (?, ?, ?, ?, ?)`,
				fmt.Sprintf("01HZY7HOST%016d", i), name, "2026-10-20T00:00:00.000000000Z", state, version); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestNewerAgents(t *testing.T) {
	path := filepath.Join(t.TempDir(), "controller.db")
	migratedControllerDB(t, path, map[string]string{
		"older": "enrolled 0.2.9", "equal": "enrolled 0.3.0", "newer-b": "enrolled 0.3.1", "newer-a": "enrolled 0.4.0-rc.1",
		"pending-newer": "pending 0.9.0", "revoked-newer": "revoked 0.9.0", "unparsed": "enrolled nonsense", "never-seen": "enrolled -",
	})
	got, err := newerAgents(path)(context.Background(), "0.3.0")
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"newer-a", "newer-b"}; !slices.Equal(got, want) {
		t.Errorf("newerAgents(0.3.0) = %q, want %q", got, want)
	}
	if _, err := newerAgents(path)(context.Background(), "not a version"); err == nil {
		t.Error("newerAgents accepted a version that does not parse")
	}
}

// TestNewerAgentsKeepsOwner shows that reading controller.db as root leaves
// every file of it with the controller user's ownership.
func TestNewerAgentsKeepsOwner(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("needs root to own the database as another user")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "controller.db")
	migratedControllerDB(t, path, map[string]string{"web1": "enrolled 0.3.1"})
	for _, f := range []string{path + "-wal", path + "-shm"} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			t.Fatal(err)
		}
	}
	const nobody = 65534
	if err := os.Chown(path, nobody, nobody); err != nil {
		t.Fatal(err)
	}
	if got, err := newerAgents(path)(context.Background(), "0.3.0"); err != nil || !slices.Equal(got, []string{"web1"}) {
		t.Fatalf("newerAgents = %q, %v; want web1", got, err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if !strings.HasPrefix(e.Name(), "controller.db") {
			continue
		}
		st, err := os.Stat(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if uid := st.Sys().(*syscall.Stat_t).Uid; uid != nobody {
			t.Errorf("%s is owned by uid %d after a read as root, want %d", e.Name(), uid, nobody)
		}
	}
}

func TestOpenAgentNewerAgents(t *testing.T) {
	s := fakeAgent(t, 0)
	if err := os.MkdirAll(s.layout.Paths.ControllerDir, 0o700); err != nil {
		t.Fatal(err)
	}
	for _, withController := range []bool{false, true} {
		if withController {
			migratedControllerDB(t, s.layout.Paths.ControllerDB(), map[string]string{"web1": "enrolled 0.3.1"})
		}
		host, err := openAgent(context.Background(), s.layout.Paths, slog.New(slog.DiscardHandler))
		if err != nil {
			t.Fatal(err)
		}
		if err := host.Close(); err != nil {
			t.Fatal(err)
		}
		cfg := s.updates[len(s.updates)-1]
		if got := cfg.NewerAgents != nil; got != withController {
			t.Errorf("controller.db present %v: NewerAgents set %v", withController, got)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 600 go test -count=1 -run 'TestUpdate|TestNewerAgents|TestOpenAgent' ./cmd/darkpanel/`
Expected: FAIL with `cmd/darkpanel/update_test.go:57:57: undefined: openUpdater`

- [ ] **Step 3: Write the implementation**

Create `cmd/darkpanel/update.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"slices"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/controller/rollout"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

// updateActions are the generated commands the update group adopts: the
// cli refuses a reserved group that leaves one of them out.
var updateActions = []string{
	update.ActionApply, update.ActionRollback, update.ActionStatus,
	rollout.ActionRollout, rollout.ActionRolloutCancel, rollout.ActionRollouts,
}

// hostUpdater is what the update commands call on the agent's updater.
type hostUpdater interface {
	Run(ctx context.Context, tx string) error
	Recover(ctx context.Context, mode update.RecoverMode, tx string) error
	RunRollback(ctx context.Context, tx string) error
	DirectRollback(ctx context.Context, force bool) (core.UpdateTransaction, error)
}

// openUpdater opens the installed agent's updater and returns it with its
// close; tests replace it.
var openUpdater = func(ctx context.Context, p paths.Paths, log *slog.Logger) (hostUpdater, func() error, error) {
	host, err := openAgent(ctx, p, log)
	if err != nil {
		return nil, nil, err
	}
	return host.Updater, host.Close, nil
}

// withUpdater runs fn on the installed agent's updater. These commands act
// on the installed binary, so they have no --dev.
func withUpdater(cmd *cobra.Command, fn func(context.Context, hostUpdater) error) error {
	p, err := resolvePaths(false)
	if err != nil {
		return err
	}
	log := slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil))
	u, closeFn, err := openUpdater(cmd.Context(), p, log)
	if err != nil {
		return err
	}
	return errors.Join(fn(cmd.Context(), u), closeFn())
}

// rootRefusal is the refusal of an update command run without root; cli
// exits 2 for it, like a flag error.
func rootRefusal(cmd *cobra.Command) error {
	if geteuid() == 0 {
		return nil
	}
	return fmt.Errorf("%s acts on the installed binary and runs as root; run it with sudo",
		strings.TrimPrefix(cmd.CommandPath(), "darkpanel "))
}

func newUpdateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "update",
		Short: "Update, roll back and recover this host's binary, and run rollouts",
		RunE:  runGroup,
	}
	cmd.AddCommand(newUpdateRunCmd(), newUpdateRecoverCmd())
	return cmd
}

func newUpdateRunCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "run <transaction>",
		Short:   "Run one update transaction; the update's transient unit runs this",
		Args:    cobra.ExactArgs(1),
		PreRunE: func(cmd *cobra.Command, _ []string) error { return rootRefusal(cmd) },
		RunE: func(cmd *cobra.Command, args []string) error {
			return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error { return u.Run(ctx, args[0]) })
		},
	}
}

func newUpdateRecoverCmd() *cobra.Command {
	var (
		boot     bool
		deadline string
	)
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Bring unfinished updates to a consistent state; the recovery units and the deadline timer run this",
		Args:  cobra.NoArgs,
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if boot && deadline != "" {
				return errors.New("--boot and --deadline exclude each other")
			}
			return rootRefusal(cmd)
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			mode := update.RecoverFailure
			switch {
			case boot:
				mode = update.RecoverBoot
			case deadline != "":
				mode = update.RecoverDeadline
			}
			return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error { return u.Recover(ctx, mode, deadline) })
		},
	}
	cmd.Flags().BoolVar(&boot, "boot", false, "recover at boot, before the services start: roll back without restarting anything")
	cmd.Flags().StringVar(&deadline, "deadline", "", "roll back this swapped transaction whose health deadline passed")
	return cmd
}

// addDirectRollback gives the generated update rollback command --direct and
// --transaction. Without --direct the generated flow runs unchanged, also
// with --local, where the agent socket and the local fallback serve it.
func addDirectRollback(root *cobra.Command) error {
	cmd, ok := cli.FindCommand(root, update.ActionRollback)
	if !ok {
		return fmt.Errorf("no command for %s", update.ActionRollback)
	}
	var (
		direct bool
		tx     string
	)
	cmd.Flags().BoolVar(&direct, "direct", false,
		"roll back in this process, without the agent; the break-glass path when no agent answers (root)")
	cmd.Flags().StringVar(&tx, "transaction", "", "with --direct, run this rollback transaction; the rollback's transient unit passes it")
	generated := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !direct {
			if tx != "" {
				return cli.ExitWith(cli.ExitUsage, errors.New("--transaction needs --direct"))
			}
			return generated(cmd, args)
		}
		for _, name := range []string{"host", "url"} {
			if cmd.Flags().Changed(name) {
				return cli.ExitWith(cli.ExitUsage, fmt.Errorf("--%s does not apply with --direct, which rolls back this host", name))
			}
		}
		if err := rootRefusal(cmd); err != nil {
			return cli.ExitWith(cli.ExitUsage, err)
		}
		// The replaced RunE is outside the cli's own marking, so a failure
		// carries its exit status itself.
		if err := directRollback(cmd, tx); err != nil {
			return cli.ExitWith(cli.ExitError, err)
		}
		return nil
	}
	return nil
}

func directRollback(cmd *cobra.Command, tx string) error {
	force, err := cmd.Flags().GetBool("force")
	if err != nil {
		return err
	}
	asJSON, err := cmd.Flags().GetBool("json")
	if err != nil {
		return err
	}
	return withUpdater(cmd, func(ctx context.Context, u hostUpdater) error {
		if tx != "" {
			return u.RunRollback(ctx, tx)
		}
		tr, err := u.DirectRollback(ctx, force)
		if err != nil {
			return err
		}
		if asJSON {
			return json.NewEncoder(cmd.OutOrStdout()).Encode(tr)
		}
		_, err = fmt.Fprintf(cmd.OutOrStdout(), "rollback %s from %s to %s: %s\n", tr.ID, tr.FromVersion, tr.ToVersion, tr.State)
		return err
	})
}

// rollbackFallback is core.update.rollback when --local finds no agent
// socket: the previous binary rolls back in its own process, because the
// rollback replaces the binary this process runs. That process runs with
// --direct, which never starts another, so the re-execution cannot loop.
func rollbackFallback(stderr io.Writer) cli.LocalFallback {
	return func(ctx context.Context, input json.RawMessage) (json.RawMessage, error) {
		var in core.UpdateRollbackInput
		if err := json.Unmarshal(input, &in); err != nil {
			return nil, err
		}
		p, err := resolvePaths(false)
		if err != nil {
			return nil, err
		}
		prev := p.PrevBinary()
		switch _, err := os.Stat(prev); {
		case errors.Is(err, fs.ErrNotExist):
			return nil, errcode.New(errcode.UpdateNothingToRollBack, "there is no previous binary to roll back to")
		case err != nil:
			return nil, err
		}
		args := []string{"update", "rollback", "--direct", "--json"}
		if in.Force {
			args = append(args, "--force")
		}
		var out bytes.Buffer
		run := exec.CommandContext(ctx, prev, args...) //nolint:gosec // G204: the installed previous binary, never a path from a request
		run.Stdout, run.Stderr = &out, stderr
		if err := run.Run(); err != nil {
			return nil, fmt.Errorf("%s %s: %w", prev, strings.Join(args, " "), err)
		}
		if !json.Valid(out.Bytes()) {
			return nil, fmt.Errorf("%s %s printed no JSON transaction", prev, strings.Join(args, " "))
		}
		return json.RawMessage(bytes.TrimSpace(out.Bytes())), nil
	}
}

// newerAgents is update.Config.NewerAgents on the controller's host: the
// names of the enrolled hosts whose agent reports a version newer than
// version, read from controller.db. It only reads; run as root, SQLite gives
// any -wal or -shm file it creates the database file's owner, so the
// controller can still open them.
func newerAgents(controllerDB string) func(ctx context.Context, version string) ([]string, error) {
	return func(ctx context.Context, version string) ([]string, error) {
		v, err := update.ParseVersion(version)
		if err != nil {
			return nil, err
		}
		db, err := store.Open(ctx, controllerDB)
		if err != nil {
			return nil, err
		}
		defer func() { _ = db.Close() }()
		rows, err := db.Read().QueryContext(ctx,
			`SELECT name, agent_version FROM core_hosts WHERE state = 'enrolled' AND agent_version IS NOT NULL`)
		if err != nil {
			return nil, err
		}
		defer func() { _ = rows.Close() }()
		var newer []string
		for rows.Next() {
			var name, agent string
			if err := rows.Scan(&name, &agent); err != nil {
				return nil, err
			}
			// A version that does not parse is not known to be newer.
			if av, err := update.ParseVersion(agent); err == nil && av.Compare(v) > 0 {
				newer = append(newer, name)
			}
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
		slices.Sort(newer)
		return newer, nil
	}
}
```

In `cmd/darkpanel/main.go`, replace:

```go
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
	"github.com/darkraise/darkpanel/web"
)
```

with:

```go
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/registry"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
	"github.com/darkraise/darkpanel/web"
)
```

In `cmd/darkpanel/main.go`, replace:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest and agent.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd()}
	return cli.NewRoot(opts)
}

func newVersionCmd() *cobra.Command {
```

with:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest, agent and
// update, the last adopting the generated update and rollout commands.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd(), newUpdateCmd()}
	opts.Adopt = map[string][]string{"update": updateActions}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}
	opts.LocalFallbacks = map[string]cli.LocalFallback{update.ActionRollback: rollbackFallback(stderr)}
	root, err := cli.NewRoot(opts)
	if err != nil {
		return nil, err
	}
	if err := addDirectRollback(root); err != nil {
		return nil, err
	}
	return root, nil
}

func newVersionCmd() *cobra.Command {
```

In `cmd/darkpanel/agent.go`, replace:

```go
			probes.Controller = update.ControllerProbe(p.ControllerSocket)
		}
	}
	upd, err := newUpdater(update.Config{
		Paths: p, DB: db, Systemd: sd, Clock: core.SystemClock{}, Log: log,
		Version: buildinfo.Version, Arch: goruntime.GOARCH,
		BaseURL: trustedkeys.BaseURL(), Keys: trustedKeys(),
		Index:  update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{}),
		Probes: probes,
	})
	if err != nil {
		_ = closeAll()
		return nil, err
```

with:

```go
			probes.Controller = update.ControllerProbe(p.ControllerSocket)
		}
	}
	cfg := update.Config{
		Paths: p, DB: db, Systemd: sd, Clock: core.SystemClock{}, Log: log,
		Version: buildinfo.Version, Arch: goruntime.GOARCH,
		BaseURL: trustedkeys.BaseURL(), Keys: trustedKeys(),
		Index:  update.NewIndexSource(trustedkeys.BaseURL(), &http.Client{Timeout: 30 * time.Second}, core.SystemClock{}),
		Probes: probes,
	}
	// A rollback on the controller's host must not leave enrolled agents
	// newer than the controller it returns to.
	if _, err := os.Stat(p.ControllerDB()); err == nil {
		cfg.NewerAgents = newerAgents(p.ControllerDB())
	}
	upd, err := newUpdater(cfg)
	if err != nil {
		_ = closeAll()
		return nil, err
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 600 go test -race -count=1 ./cmd/darkpanel/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/16 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/update.go cmd/darkpanel/update_test.go cmd/darkpanel/main.go cmd/darkpanel/agent.go
git commit -m "feat(cmd): add the update run, recover and direct rollback"
```

---

### Task 17: system install and uninstall

**Files:**
- Create: `cmd/darkpanel/system.go`
- Create: `cmd/darkpanel/uninstall.go`
- Modify: `cmd/darkpanel/main.go`
- Test: `cmd/darkpanel/system_test.go`
- Test: `cmd/darkpanel/uninstall_test.go`

**Interfaces:**
- Consumes: C15 `newSystemd`, `systemdConn`, `installLayout`, `stdinTerminal`, `maxStdinToken`, `daemonCheck`, `daemonEnroll`, `runGroup`, `fakeAgent`, `agentSeams` (fields `layout`, `systemd`, `enrolls`, `checkRes`, `checkErr`), `fakeSystemd.closed`, `runStdin` (Task 15); C6 `cli.ExitWith`, `cli.ExitRefused`; `install.Install`, `install.Config`, `install.Controller`, `install.Agent`, `install.CheckResult`, `install.ParseRole`, `install.RoleController`, `install.RoleAgent`, `install.CheckAgentAddress`, `install.SystemUsers`, `install.PlanUninstall`, `install.UninstallConfig`, `(*install.Uninstall).Describe`, `Run`, `Close`; `modules.All`; agent `api.NewClient`, `api.NewSocketHandler`, `api.Config`, `api.SocketConfig`; `platform.NewRunner`, `platform.ProductionAllowlist`; `geteuid`, `runArgs` (existing).
- Produces: C17. `darkpanel system install --role controller|agent [--agent-listen <addr>]... [--agent-advertise <host:port>]` and `darkpanel uninstall [--keep-data] [--yes]`. New names in package `main`: seams `runInstall` (`= install.Install`) and `planUninstall` (`func(ctx context.Context, cfg install.UninstallConfig) (plannedUninstall, error)`); constants `socketRequestTimeout`, `agentHelloTimeout`; types `socketController`, `installAgent` (fields `socket`, `tlsDir`), `plannedUninstall`; functions `newSystemCmd`, `newSystemInstallCmd`, `manifests`, `newSocketController(path string) socketController`, `newUninstallCmd`, `confirmUninstall`. Test helpers: `fakeInstall(t *testing.T, euid int, terminal bool) (*[]install.Config, *agentSeams)`, `serveUnix(t *testing.T, h http.Handler) string`, type `helloAgent`, type `fakePlan`; tests `TestSystemInstallRefusals`, `TestSystemInstallConfig`, `TestSocketController`, `TestInstallAgentCheck`, `TestUninstall`.

**Items:** 9, 19, 68, 88
**Depends on:** Task 6, Task 15
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Notes for the implementer:
- The token is read from standard input only for the agent role and only when standard input is not a terminal (install.sh pipes it); the controller role enrolls through `core.host.enroll-local` and reads nothing.
- `socketController` copies the dial pattern of `internal/cli`'s unexported `newSocketClient`. The routes are the controller socket's own (`internal/controller/api/socket.go`): `GET /healthz`, and `POST /api/v1/actions/core.host.enroll-local/v1/plan` with `{"input":{}}`, then `.../apply` with `{"planId","confirmation"}`. The apply answers a change whose `output.token` is the token.
- `installAgent.Check` asks the agent socket's `hello` first (within `agentHelloTimeout`). It answers `CheckAccepted` without a handshake only when `controllerConnected` is true; otherwise (no socket, false, null) it runs `daemon.Check`. `TestInstallAgentCheck` serves a real agent socket handler (`api.NewSocketHandler`) whose agent answers only `Hello`.
- `uninstall` prints the plan's `Describe`, then asks `Proceed? [y/N] ` on standard error unless `--yes`. Without a terminal and without `--yes`, and on a declined prompt, it exits 3 (`cli.ExitRefused`) and calls the plan's `Close` instead of `Run`.

- [ ] **Step 1: Write the failing test**

Create `cmd/darkpanel/system_test.go`:

```go
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// fakeInstall replaces the installer and the systemd provider for one test
// and records the Config system install passed.
func fakeInstall(t *testing.T, euid int, terminal bool) (*[]install.Config, *agentSeams) {
	t.Helper()
	s := fakeAgent(t, euid)
	var got []install.Config
	oldInstall, oldTerm := runInstall, stdinTerminal
	t.Cleanup(func() { runInstall, stdinTerminal = oldInstall, oldTerm })
	runInstall = func(_ context.Context, cfg install.Config) error {
		got = append(got, cfg)
		return nil
	}
	stdinTerminal = func(io.Reader) bool { return terminal }
	return &got, s
}

func TestSystemInstallRefusals(t *testing.T) {
	tests := []struct {
		args []string
		euid int
		out  string
	}{
		{[]string{"system", "install", "--role", "controller"}, 1000, "system install runs as root; run it with sudo"},
		{[]string{"system", "install"}, 0, `install: role "": want controller or agent`},
		{[]string{"system", "install", "--role", "web"}, 0, `install: role "web": want controller or agent`},
		{[]string{"system", "install", "--role", "agent", "--agent-listen", "192.0.2.7:7443"}, 0, "--agent-listen and --agent-advertise are for the controller role"},
		{[]string{"system", "install", "--role", "agent", "--agent-advertise", "panel.example:7443"}, 0, "--agent-listen and --agent-advertise are for the controller role"},
		{[]string{"system", "install", "--role", "controller", "--agent-listen", "192.0.2.7"}, 0, `agent address "192.0.2.7" is not host:port`},
		{[]string{"system", "install", "--role", "controller", "--agent-advertise", "panel.example:0"}, 0, `agent address "panel.example:0" is not host:port`},
		{[]string{"system", "nope"}, 0, `unknown command "nope" for "darkpanel system"`},
	}
	for _, tt := range tests {
		got, _ := fakeInstall(t, tt.euid, false)
		out, code := runArgs(tt.args...)
		if code != cli.ExitUsage || !strings.Contains(out, tt.out) {
			t.Errorf("darkpanel %s: exit %d, output %q; want exit 2 and %q", strings.Join(tt.args, " "), code, out, tt.out)
		}
		if len(*got) != 0 {
			t.Errorf("darkpanel %s ran the installer", strings.Join(tt.args, " "))
		}
	}
}

func TestSystemInstallConfig(t *testing.T) {
	tests := []struct {
		name       string
		args       []string
		stdin      string
		terminal   bool
		role       install.Role
		token      string
		controller bool
	}{
		{"controller", []string{"--role", "controller", "--agent-listen", "192.0.2.7:7443", "--agent-advertise", "panel.example:7443"},
			"ignored\n", false, install.RoleController, "", true},
		{"agent with a piped token", []string{"--role", "agent"}, "  piped-token \n", false, install.RoleAgent, "piped-token", false},
		{"agent on a terminal", []string{"--role", "agent"}, "typed\n", true, install.RoleAgent, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, s := fakeInstall(t, 0, tt.terminal)
			args := append([]string{"system", "install"}, tt.args...)
			if stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), args...); code != cli.ExitOK {
				t.Fatalf("exit %d: %s%s", code, stdout, stderr)
			}
			if len(*got) != 1 {
				t.Fatalf("the installer ran %d times, want once", len(*got))
			}
			cfg := (*got)[0]
			if cfg.Layout != s.layout || cfg.Role != tt.role || cfg.Token != tt.token || cfg.BinaryVersion != "dev" ||
				cfg.Systemd != s.systemd || cfg.Exec == nil || cfg.Users != (install.SystemUsers{}) || cfg.Clock == nil || cfg.Out == nil {
				t.Errorf("Config = %+v, want the installed layout, role %s, token %q and the production providers", cfg, tt.role, tt.token)
			}
			if got := cfg.Controller != nil; got != tt.controller {
				t.Errorf("Controller set %v, want %v", got, tt.controller)
			}
			if a, ok := cfg.Agent.(installAgent); !ok || a.socket != s.layout.Paths.AgentSocket || a.tlsDir != s.layout.Paths.AgentTLSDir {
				t.Errorf("Agent = %#v, want the installed agent socket and credentials", cfg.Agent)
			}
			var wantModules []string
			for _, m := range modules.All() {
				wantModules = append(wantModules, m.Manifest().ID)
			}
			var gotModules []string
			for _, m := range cfg.Modules {
				gotModules = append(gotModules, m.ID)
			}
			if !slices.Equal(gotModules, wantModules) {
				t.Errorf("Modules = %v, want %v", gotModules, wantModules)
			}
			if tt.role == install.RoleController && (!slices.Equal(cfg.AgentListen, []string{"192.0.2.7:7443"}) || cfg.AgentAdvertise != "panel.example:7443") {
				t.Errorf("agent listener flags = %v %q", cfg.AgentListen, cfg.AgentAdvertise)
			}
			if s.systemd.closed != 1 {
				t.Errorf("systemd closed %d times, want once", s.systemd.closed)
			}
		})
	}
}

// serveUnix serves h on a Unix socket until the test ends and returns its
// path.
func serveUnix(t *testing.T, h http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "dp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "s.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h} //nolint:gosec // G112: a test server on a private socket
	go func() { _ = srv.Serve(l) }()
	t.Cleanup(func() { _ = srv.Close() })
	return path
}

func TestSocketController(t *testing.T) {
	var applied []string
	mux := http.NewServeMux()
	health := "ok"
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"status":"`+health+`","version":"dev"}`)
	})
	mux.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/plan", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Input json.RawMessage `json:"input"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil || string(body.Input) != "{}" {
			t.Errorf("plan body input %s (%v), want {}", body.Input, err)
		}
		_, _ = io.WriteString(w, `{"planId":"01HZY8PLAN","confirmation":"enroll-local web1"}`)
	})
	mux.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/apply", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PlanID       string `json:"planId"`
			Confirmation string `json:"confirmation"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		applied = append(applied, body.PlanID+" "+body.Confirmation)
		_, _ = io.WriteString(w, `{"id":"01HZY8CHG","state":"applied","output":{"token":"dpe1.token"},"error":null}`)
	})
	c := newSocketController(serveUnix(t, mux))
	ctx := context.Background()
	if err := c.Healthy(ctx); err != nil {
		t.Errorf("Healthy with status ok: %v", err)
	}
	health = "degraded"
	if err := c.Healthy(ctx); err == nil || !strings.Contains(err.Error(), `status "degraded"`) {
		t.Errorf("Healthy with status degraded: %v, want an error naming the status", err)
	}
	token, err := c.EnrollLocal(ctx)
	if err != nil || token != "dpe1.token" {
		t.Errorf("EnrollLocal = %q, %v; want the apply's output token", token, err)
	}
	if want := []string{"01HZY8PLAN enroll-local web1"}; !slices.Equal(applied, want) {
		t.Errorf("applied %q, want the plan id with the plan's confirmation phrase %q", applied, want)
	}

	refusing := http.NewServeMux()
	refusing.HandleFunc("POST /api/v1/actions/core.host.enroll-local/v1/plan", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = io.WriteString(w, `{"title":"Forbidden","status":403,"detail":"the controller socket accepts only root","code":"auth.forbidden"}`)
	})
	if _, err := newSocketController(serveUnix(t, refusing)).EnrollLocal(ctx); err == nil ||
		!strings.Contains(err.Error(), "the controller socket accepts only root (auth.forbidden)") {
		t.Errorf("EnrollLocal against a refusal: %v, want the problem's detail and code", err)
	}
	missing := newSocketController(filepath.Join(t.TempDir(), "missing.sock"))
	if err := missing.Healthy(ctx); err == nil {
		t.Error("Healthy without a controller socket succeeded")
	}
}

// helloAgent answers only Hello, as an agent whose controller session is
// connected reports.
type helloAgent struct {
	core.AgentClient
	connected *bool
}

func (h helloAgent) Hello(context.Context) (core.AgentHello, error) {
	return core.AgentHello{HostID: "01HZY5S4ZK8V3J1M2N3P4Q5R6S", ControllerConnected: h.connected}, nil
}

func TestInstallAgentCheck(t *testing.T) {
	yes, no := true, false
	tests := []struct {
		name      string
		socket    bool
		connected *bool
		checks    int
	}{
		{"connected", true, &yes, 0},
		{"not connected", true, &no, 1},
		{"not enrolled", true, nil, 1},
		{"no agent socket", false, nil, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, 0)
			s.checkRes, s.checkErr = install.CheckUnreachable, errors.New("dial: no route")
			checks := 0
			daemonCheck = func(_ context.Context, dir string) (install.CheckResult, error) {
				checks++
				if dir != "/tls" {
					t.Errorf("checked %s, want /tls", dir)
				}
				return s.checkRes, s.checkErr
			}
			socket := filepath.Join(t.TempDir(), "missing.sock")
			if tt.socket {
				socket = serveUnix(t, api.NewSocketHandler(api.Config{Agent: helloAgent{connected: tt.connected}},
					api.SocketConfig{Identity: func() (string, bool) { return "01HZY5S4ZK8V3J1M2N3P4Q5R6S", tt.connected != nil }}))
			}
			res, err := installAgent{socket: socket, tlsDir: "/tls"}.Check(context.Background())
			if checks != tt.checks {
				t.Errorf("ran %d handshakes, want %d", checks, tt.checks)
			}
			wantRes, wantErr := install.CheckAccepted, error(nil)
			if tt.checks > 0 {
				wantRes, wantErr = s.checkRes, s.checkErr
			}
			if res != wantRes || !errors.Is(err, wantErr) {
				t.Errorf("Check = %d, %v; want %d, %v", res, err, wantRes, wantErr)
			}
		})
	}
	s := fakeAgent(t, 0)
	if err := (installAgent{tlsDir: "/tls"}).Enroll(context.Background(), "dpe1.token"); err != nil {
		t.Fatal(err)
	}
	if want := []string{"/tls dpe1.token"}; !slices.Equal(s.enrolls, want) {
		t.Errorf("enrolled %q, want %q", s.enrolls, want)
	}
}
```

Create `cmd/darkpanel/uninstall_test.go`:

```go
package main

import (
	"context"
	"fmt"
	"io"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
)

// fakePlan is a planned uninstall that records what was done with it.
type fakePlan struct {
	calls []string
}

func (f *fakePlan) Describe(w io.Writer) {
	f.calls = append(f.calls, "describe")
	_, _ = fmt.Fprintln(w, "will remove the units")
}

func (f *fakePlan) Run(_ context.Context, w io.Writer) error {
	f.calls = append(f.calls, "run")
	_, _ = fmt.Fprintln(w, "removed the units")
	return nil
}

func (f *fakePlan) Close() error {
	f.calls = append(f.calls, "close")
	return nil
}

func TestUninstall(t *testing.T) {
	tests := []struct {
		name     string
		args     []string
		euid     int
		terminal bool
		stdin    string
		code     int
		calls    []string
		stdout   string
		stderr   string
	}{
		{"yes", []string{"--yes"}, 0, false, "", cli.ExitOK, []string{"describe", "run"}, "will remove the units\nremoved the units\n", ""},
		{"keep data", []string{"--yes", "--keep-data"}, 0, false, "", cli.ExitOK, []string{"describe", "run"}, "", ""},
		{"no terminal without yes", nil, 0, false, "y\n", cli.ExitRefused, []string{"describe", "close"}, "will remove the units\n",
			"darkpanel: uninstall needs --yes when standard input is not a terminal\n"},
		{"confirmed on the terminal", nil, 0, true, "y\n", cli.ExitOK, []string{"describe", "run"}, "", "Proceed? [y/N] "},
		{"declined on the terminal", nil, 0, true, "\n", cli.ExitRefused, []string{"describe", "close"}, "", "Proceed? [y/N] darkpanel: uninstall declined\n"},
		{"not root", []string{"--yes"}, 1000, false, "", cli.ExitUsage, nil, "", "darkpanel: uninstall runs as root; run it with sudo\n"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := fakeAgent(t, tt.euid)
			plan := &fakePlan{}
			var cfgs []install.UninstallConfig
			oldPlan, oldTerm := planUninstall, stdinTerminal
			t.Cleanup(func() { planUninstall, stdinTerminal = oldPlan, oldTerm })
			planUninstall = func(_ context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
				cfgs = append(cfgs, cfg)
				return plan, nil
			}
			stdinTerminal = func(io.Reader) bool { return tt.terminal }
			stdout, stderr, code := runStdin(strings.NewReader(tt.stdin), append([]string{"uninstall"}, tt.args...)...)
			if code != tt.code || !slices.Equal(plan.calls, tt.calls) || !strings.Contains(stdout, tt.stdout) || !strings.HasSuffix(stderr, tt.stderr) {
				t.Errorf("exit %d, calls %q, stdout %q, stderr %q; want exit %d, calls %q, %q and %q",
					code, plan.calls, stdout, stderr, tt.code, tt.calls, tt.stdout, tt.stderr)
			}
			if tt.calls == nil {
				return
			}
			cfg := cfgs[0]
			if cfg.Layout != s.layout || cfg.KeepData != slices.Contains(tt.args, "--keep-data") || len(cfg.Modules) != len(modules.All()) ||
				cfg.Systemd != s.systemd || cfg.Exec == nil || cfg.Users != (install.SystemUsers{}) || cfg.Clock == nil || cfg.Log == nil || cfg.FilesTestMode {
				t.Errorf("UninstallConfig = %+v, want the installed layout, the modules and the production providers", cfg)
			}
			if s.systemd.closed != 1 {
				t.Errorf("systemd closed %d times, want once", s.systemd.closed)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 600 go test -count=1 -run 'TestSystem|TestSocketController|TestInstallAgent|TestUninstall' ./cmd/darkpanel/`
Expected: FAIL with `cmd/darkpanel/system_test.go:29:25: undefined: runInstall`

- [ ] **Step 3: Write the implementation**

Create `cmd/darkpanel/system.go`:

```go
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/agent/api"
	"github.com/darkraise/darkpanel/internal/buildinfo"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
)

// runInstall is system install's installer; tests replace it.
var runInstall = install.Install

// socketRequestTimeout bounds one request on the controller socket, as the
// CLI's own socket client does.
const socketRequestTimeout = 90 * time.Second

// agentHelloTimeout bounds the hello that tells whether the agent already
// holds a live controller session.
const agentHelloTimeout = 10 * time.Second

func newSystemCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "system",
		Short: "Install this host's system services",
		RunE:  runGroup,
	}
	cmd.AddCommand(newSystemInstallCmd())
	return cmd
}

func newSystemInstallCmd() *cobra.Command {
	var (
		roleFlag       string
		role           install.Role
		agentListen    []string
		agentAdvertise string
	)
	cmd := &cobra.Command{
		Use:   "install",
		Short: "Install or repair the controller or agent role on this host",
		Long: "Install or repair the controller or agent role on this host: directories, users, keys, units, " +
			"enrollment and the started services. Rerun it to finish or repair an install. For the agent role " +
			"the enrollment token is read from standard input when it is not a terminal; install.sh pipes it.",
		Args: cobra.NoArgs,
		// cli marks only RunE errors as run errors, so these refusals of the
		// command line exit 2 like cobra's own flag errors.
		PreRunE: func(*cobra.Command, []string) error {
			var err error
			if role, err = install.ParseRole(roleFlag); err != nil {
				return err
			}
			if role != install.RoleController && (len(agentListen) > 0 || agentAdvertise != "") {
				return errors.New("--agent-listen and --agent-advertise are for the controller role")
			}
			addrs := slices.Clone(agentListen)
			if agentAdvertise != "" {
				addrs = append(addrs, agentAdvertise)
			}
			for _, a := range addrs {
				if err := install.CheckAgentAddress(a); err != nil {
					return err
				}
			}
			if geteuid() != 0 {
				return errors.New("system install runs as root; run it with sudo")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			var token string
			if in := cmd.InOrStdin(); role == install.RoleAgent && !stdinTerminal(in) {
				data, err := io.ReadAll(io.LimitReader(in, maxStdinToken+1))
				if err != nil {
					return fmt.Errorf("read the token from standard input: %w", err)
				}
				if len(data) > maxStdinToken {
					return errors.New("standard input is larger than an enrollment token")
				}
				token = strings.TrimSpace(string(data))
			}
			sd, err := newSystemd(cmd.Context())
			if err != nil {
				return err
			}
			l := installLayout()
			cfg := install.Config{
				Layout: l, Role: role, AgentListen: agentListen, AgentAdvertise: agentAdvertise, Token: token,
				Modules: manifests(modules.All()), BinaryVersion: buildinfo.Version,
				Systemd: sd, Exec: platform.NewRunner(platform.ProductionAllowlist()), Users: install.SystemUsers{},
				Agent: installAgent{socket: l.Paths.AgentSocket, tlsDir: l.Paths.AgentTLSDir},
				Clock: core.SystemClock{}, Out: cmd.OutOrStdout(),
			}
			if role == install.RoleController {
				cfg.Controller = newSocketController(l.Paths.ControllerSocket)
			}
			return errors.Join(runInstall(cmd.Context(), cfg), sd.Close())
		},
	}
	cmd.Flags().StringVar(&roleFlag, "role", "", "controller or agent")
	cmd.Flags().StringArrayVar(&agentListen, "agent-listen", nil,
		"controller role: extra address of this host for the agent listener, <ip>:<port> (repeatable)")
	cmd.Flags().StringVar(&agentAdvertise, "agent-advertise", "",
		"controller role: <host>:<port> that enrollment tokens tell agents to connect to")
	return cmd
}

func manifests(mods []core.Module) []core.Manifest {
	out := make([]core.Manifest, 0, len(mods))
	for _, m := range mods {
		out = append(out, m.Manifest())
	}
	return out
}

// socketController is install.Controller over the controller socket, whose
// peer credentials are the authentication.
type socketController struct {
	client *http.Client
}

func newSocketController(path string) socketController {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		c, err := d.DialContext(ctx, "unix", path)
		if err != nil {
			return nil, fmt.Errorf("controller socket %s: %w", path, err)
		}
		return c, nil
	}}
	return socketController{client: &http.Client{Transport: tr, Timeout: socketRequestTimeout}}
}

func (c socketController) Healthy(ctx context.Context) error {
	var health struct {
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/healthz", nil, &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return fmt.Errorf("the controller reports status %q", health.Status)
	}
	return nil
}

// EnrollLocal plans core.host.enroll-local and applies it with the plan's
// confirmation phrase: the action is irreversible, and the installer stands
// for the operator who would type it.
func (c socketController) EnrollLocal(ctx context.Context) (string, error) {
	const route = "/api/v1/actions/core.host.enroll-local/v1/"
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	if err := c.do(ctx, http.MethodPost, route+"plan", map[string]any{"input": map[string]any{}}, &plan); err != nil {
		return "", err
	}
	apply := struct {
		PlanID       string `json:"planId"`
		Confirmation string `json:"confirmation"`
	}{PlanID: plan.PlanID}
	if plan.Confirmation != nil {
		apply.Confirmation = *plan.Confirmation
	}
	var change struct {
		State  string `json:"state"`
		Output struct {
			Token string `json:"token"`
		} `json:"output"`
		Error *struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := c.do(ctx, http.MethodPost, route+"apply", apply, &change); err != nil {
		return "", err
	}
	switch {
	case change.Error != nil:
		return "", fmt.Errorf("core.host.enroll-local %s: %s (%s)", change.State, change.Error.Message, change.Error.Code)
	case change.Output.Token == "":
		return "", fmt.Errorf("core.host.enroll-local %s without a token", change.State)
	}
	return change.Output.Token, nil
}

// do sends body as JSON (nil sends none) and decodes a 2xx answer into out;
// any other answer fails with the problem's detail and code.
func (c socketController) do(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://controller"+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	if resp.StatusCode/100 != 2 {
		var p struct {
			Detail string `json:"detail"`
			Code   string `json:"code"`
		}
		if json.Unmarshal(data, &p) == nil && p.Detail != "" {
			return fmt.Errorf("%s %s: %s (%s)", method, path, p.Detail, p.Code)
		}
		return fmt.Errorf("%s %s: %s", method, path, resp.Status)
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	return nil
}

// installAgent is install.Agent over this host's agent. Its Check asks the
// running agent first: a handshake beside a live session would replace that
// session, taking the host offline for a moment on every installer rerun.
type installAgent struct {
	socket string
	tlsDir string
}

func (a installAgent) Check(ctx context.Context) (install.CheckResult, error) {
	helloCtx, cancel := context.WithTimeout(ctx, agentHelloTimeout)
	defer cancel()
	client := api.NewClient(api.ClientConfig{Dial: func(ctx context.Context) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", a.socket)
	}})
	if h, err := client.Hello(helloCtx); err == nil && h.ControllerConnected != nil && *h.ControllerConnected {
		return install.CheckAccepted, nil
	}
	return daemonCheck(ctx, a.tlsDir)
}

func (a installAgent) Enroll(ctx context.Context, token string) error {
	_, err := daemonEnroll(ctx, a.tlsDir, token)
	return err
}
```

Create `cmd/darkpanel/uninstall.go`:

```go
package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/spf13/cobra"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/modules"
	"github.com/darkraise/darkpanel/internal/platform"
)

// plannedUninstall is what uninstall does with install.PlanUninstall's plan.
type plannedUninstall interface {
	Describe(w io.Writer)
	Run(ctx context.Context, w io.Writer) error
	Close() error
}

// planUninstall plans the uninstall; tests replace it.
var planUninstall = func(ctx context.Context, cfg install.UninstallConfig) (plannedUninstall, error) {
	u, err := install.PlanUninstall(ctx, cfg)
	if err != nil {
		return nil, err
	}
	return u, nil
}

func newUninstallCmd() *cobra.Command {
	var keepData, yes bool
	cmd := &cobra.Command{
		Use:   "uninstall",
		Short: "Remove Darkpanel from this host: services, units, users, binary and, unless --keep-data, its data",
		Args:  cobra.NoArgs,
		PreRunE: func(*cobra.Command, []string) error {
			if geteuid() != 0 {
				return errors.New("uninstall runs as root; run it with sudo")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			sd, err := newSystemd(cmd.Context())
			if err != nil {
				return err
			}
			plan, err := planUninstall(cmd.Context(), install.UninstallConfig{
				Layout: installLayout(), KeepData: keepData, Modules: modules.All(),
				Systemd: sd, Exec: platform.NewRunner(platform.ProductionAllowlist()), Users: install.SystemUsers{},
				Clock: core.SystemClock{}, Log: slog.New(slog.NewTextHandler(cmd.ErrOrStderr(), nil)),
			})
			if err != nil {
				return errors.Join(err, sd.Close())
			}
			plan.Describe(cmd.OutOrStdout())
			if err := confirmUninstall(cmd, yes); err != nil {
				return errors.Join(err, plan.Close(), sd.Close())
			}
			return errors.Join(plan.Run(cmd.Context(), cmd.OutOrStdout()), sd.Close())
		},
	}
	cmd.Flags().BoolVar(&keepData, "keep-data", false,
		"keep the databases, backups, update records, keys, agent credentials and users, so a later install finds them")
	cmd.Flags().BoolVar(&yes, "yes", false, "uninstall without asking Proceed? [y/N]")
	return cmd
}

// confirmUninstall asks Proceed? [y/N] on the terminal unless yes; without a
// terminal only --yes proceeds. A refusal exits 3, as a declined prompt does.
func confirmUninstall(cmd *cobra.Command, yes bool) error {
	if yes {
		return nil
	}
	in := cmd.InOrStdin()
	if !stdinTerminal(in) {
		return cli.ExitWith(cli.ExitRefused, errors.New("uninstall needs --yes when standard input is not a terminal"))
	}
	_, _ = fmt.Fprint(cmd.ErrOrStderr(), "Proceed? [y/N] ")
	answer, err := bufio.NewReader(in).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
		return cli.ExitWith(cli.ExitRefused, errors.New("uninstall declined"))
	}
	return nil
}
```

In `cmd/darkpanel/main.go`, replace:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest, agent and
// update, the last adopting the generated update and rollout commands.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd(), newUpdateCmd()}
	opts.Adopt = map[string][]string{"update": updateActions}
	stderr := opts.Stderr
	if stderr == nil {
```

with:

```go
}

// newRoot returns the command tree: the commands generated from
// registry.Build() plus the hand-written serve, version, selftest, agent,
// update, system and uninstall; update adopts the generated update and
// rollout commands.
func newRoot(opts cli.Options) (*cobra.Command, error) {
	reg, err := registry.Build()
	if err != nil {
		return nil, err
	}
	opts.Registry = reg
	opts.Commands = []*cobra.Command{
		newServeCmd(), newVersionCmd(), newSelftestCmd(), newAgentCmd(), newUpdateCmd(), newSystemCmd(), newUninstallCmd(),
	}
	opts.Adopt = map[string][]string{"update": updateActions}
	stderr := opts.Stderr
	if stderr == nil {
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 600 go test -race -count=1 ./cmd/darkpanel/`
Expected: PASS

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 600 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/17 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/system.go cmd/darkpanel/system_test.go cmd/darkpanel/uninstall.go cmd/darkpanel/uninstall_test.go cmd/darkpanel/main.go
git commit -m "feat(cmd): add system install and uninstall"
```

---

### Task 18: Harness builds

**Files:**
- Create: `cmd/darkpanel/harness_broken.go`
- Create: `cmd/darkpanel/harness_crash.go`
- Create: `internal/core/store/migrations_default.go`
- Create: `internal/core/store/migrations_harness.go`
- Create: `internal/core/store/harnessmigrations/agent/9000_harness_marker.sql`
- Create: `internal/core/store/harnessmigrations/controller/9000_harness_marker.sql`
- Modify: `cmd/darkpanel/main.go`
- Modify: `cmd/darkpanel/agent.go`
- Modify: `internal/agent/daemon/daemon.go`
- Modify: `internal/core/store/migrations.go`
- Test: `cmd/darkpanel/harness_broken_test.go`
- Test: `cmd/darkpanel/harness_crash_test.go`
- Test: `cmd/darkpanel/serve_test.go`
- Test: `cmd/darkpanel/agent_test.go`
- Test: `cmd/darkpanel/signal_test.go`
- Test: `internal/agent/daemon/daemon_test.go`
- Test: `internal/core/store/migrations_harness_test.go`
- Test: `internal/core/store/migrations_default_test.go`

**Interfaces:**
- Consumes: C5/C10 `server.Options.HealthStatus` (Task 10); C13 `selftest`, `withTrustedKeys` (Task 13); C14 serve (Task 14); C15 `newAgentRunCmd`, `fakeAgent`, `agentSeams` (fields `runs`, `updates`, `dev`) (Task 15); `fakeServe`, `runArgs` (existing); daemon test helpers `newTestController`, `newAgentHost`, `(*agentHost).config`, `(*agentHost).start`, `(*agentHost).hello`, `sessionClient`, `connected`, `waitFor` (existing, package `daemon`); store test helpers `openTemp`, `migrateOpts`, `sqlFS`, `metaSQL` (existing, package `store`).
- Produces: C18. Build tags `harnessbroken` (serve passes `HealthStatus: "degraded"`, agent run sets `daemon.Config.NeverConnected`), `harnesscrash` (`serve` and `agent run` return `harnesscrash: exiting at start` before anything opens, exit 1; `selftest` passes), `harnessmigration` (one more core migration per side, `9000_harness_marker.sql`, creating `harness_marker (note TEXT NOT NULL) STRICT` with one row). New exported field `daemon.Config.NeverConnected bool`. New names in package `main`: variables `healthStatus`, `neverConnected`, `crashAtStart`, `errCrashAtStart`; test helper `withoutHarness(t *testing.T)` (in `serve_test.go`; `fakeServe` and `fakeAgent` call it, so the default tests check the production behaviour in every build); tests `TestHarnessBroken` (tag `harnessbroken`), `TestHarnessCrash` (tag `harnesscrash`). New names in package `daemon`: test `TestRunNeverConnected`. New names in package `store`: function `withHarnessMigrations(side string, core fs.FS) (fs.FS, error)` (one per build), variable `harnessFS` and type `unionFS` (tag `harnessmigration` only), tests `TestHarnessMigration`, `TestHarnessMigrationRefusesAShadowedName` (tag `harnessmigration`), `TestNoHarnessMigration` (default build).

**Items:** 13, 44
**Depends on:** Task 5, Task 10, Task 13, Task 14, Task 15
**Implementer:** dr-superpowers:impl-opus-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 3 = 6

Notes for the implementer:
- `CoreMigrations` passes its set through `withHarnessMigrations`. Without the `harnessmigration` tag that function returns the set unchanged, so the default build embeds exactly the files it did before (`TestNoHarnessMigration` checks that no 9000 file is embedded). With the tag it returns a union of the core directory and `harnessmigrations/<side>/`, read through `ReadDir`, and refuses a name present in both.
- An older binary ignores the recorded 9000 migration: `Migrate` skips recorded migrations numbered above its highest embedded one (`internal/core/store/migrate.go:84`), which the default-build test `TestMigrateIgnoresNewerRecordedMigrations` (`internal/core/store/migrate_test.go:310`) already covers.
- `NeverConnected` only changes what Hello reports (false while enrolled, never true); the session itself is unchanged.
- The tagged `cmd/darkpanel` tests run the whole package under the tag. `fakeServe`, `fakeAgent` and the hung-serve child in `signal_test.go` clear the harness variables, so every default test passes under every tag, and the tagged tests set them back to the build's values.

- [ ] **Step 1: Write the failing test**

In `internal/agent/daemon/daemon_test.go`, replace:

```go
	}
}

func TestRunTakesUpAnEnrollment(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
```

with:

```go
	}
}

// TestRunNeverConnected: with NeverConnected an enrolled agent with a live
// session reports no controller session, on its socket and to the
// controller alike.
func TestRunNeverConnected(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
	h := newAgentHost(t)
	if err := (Agent{TLSDir: h.layout.Paths.AgentTLSDir}).Enroll(context.Background(), ctrl.token("H1")); err != nil {
		t.Fatal(err)
	}
	cfg := h.config()
	cfg.NeverConnected = true
	h.start(cfg)
	waitFor(t, "a session", func() bool { return len(ctrl.agentSessions()) == 1 })
	hello, err := sessionClient(ctrl, ctrl.agentSessions()[0]).Hello(context.Background())
	if err != nil || hello.HostID != "H1" || connected(hello) != "false" {
		t.Errorf("Hello over the session = %+v, %v; want host H1 reporting false", hello, err)
	}
	if hello := h.hello(); hello.HostID != "H1" || connected(hello) != "false" {
		t.Errorf("Hello over the socket = %+v; want host H1 reporting false", hello)
	}
}

func TestRunTakesUpAnEnrollment(t *testing.T) {
	ctrl := newTestController(t, time.Now().UTC().Truncate(time.Second))
	ctrl.addHost("H1", "pending")
```

Create `internal/core/store/migrations_harness_test.go`:

```go
//go:build harnessmigration

package store

import (
	"context"
	"io/fs"
	"testing"
	"time"
)

func TestHarnessMigration(t *testing.T) {
	for _, side := range []string{SideController, SideAgent} {
		t.Run(side, func(t *testing.T) {
			set, err := CoreMigrations(side)
			if err != nil {
				t.Fatal(err)
			}
			files, err := fs.Glob(set.FS, "*.sql")
			if err != nil || len(files) < 2 || files[len(files)-1] != "9000_harness_marker.sql" {
				t.Fatalf("core migrations of %s = %v, %v; want the real ones, then 9000_harness_marker.sql", side, files, err)
			}
			db := openTemp(t)
			opts := migrateOpts(t, "v1", time.Now())
			opts.Side = side
			if _, err := Migrate(context.Background(), db, opts, set, nil); err != nil {
				t.Fatal(err)
			}
			var note string
			if err := db.Read().QueryRow("SELECT note FROM harness_marker").Scan(&note); err != nil || note != "written by the harnessmigration build" {
				t.Errorf("harness_marker = %q, %v", note, err)
			}
			if v, err := SchemaVersion(context.Background(), db, side); err != nil || v != 9000 {
				t.Errorf("SchemaVersion = %d, %v; want 9000", v, err)
			}
		})
	}
}

func TestHarnessMigrationRefusesAShadowedName(t *testing.T) {
	core := sqlFS(map[string]string{"0001_core.sql": metaSQL})
	if _, err := fs.ReadDir(unionFS{core, core}, "."); err == nil {
		t.Error("the union read a migration that is in both sets")
	}
}
```

Create `internal/core/store/migrations_default_test.go`:

```go
//go:build !harnessmigration

package store

import (
	"io/fs"
	"strings"
	"testing"
)

// TestNoHarnessMigration: only the harnessmigration build embeds a 9000
// migration; an older binary ignores it once recorded
// (TestMigrateIgnoresNewerRecordedMigrations).
func TestNoHarnessMigration(t *testing.T) {
	for _, side := range []string{SideController, SideAgent} {
		set, err := CoreMigrations(side)
		if err != nil {
			t.Fatal(err)
		}
		files, err := fs.Glob(set.FS, "*.sql")
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if strings.HasPrefix(f, "9000_") {
				t.Errorf("the default build embeds %s/%s", side, f)
			}
		}
	}
}
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
	var got []server.Options
	oldRun, oldEUID := serveRun, geteuid
	t.Cleanup(func() { serveRun, geteuid = oldRun, oldEUID })
	serveRun = func(ctx context.Context, opts server.Options) error {
		if ctx.Err() != nil {
			t.Error("serve passed a context that has already ended")
```

with:

```go
	var got []server.Options
	oldRun, oldEUID := serveRun, geteuid
	t.Cleanup(func() { serveRun, geteuid = oldRun, oldEUID })
	withoutHarness(t)
	serveRun = func(ctx context.Context, opts server.Options) error {
		if ctx.Err() != nil {
			t.Error("serve passed a context that has already ended")
```

In `cmd/darkpanel/serve_test.go`, replace:

```go
	return &got
}

func runArgs(args ...string) (string, int) {
	var out bytes.Buffer
	code := run(args, cli.Options{Stdout: &out, Stderr: &out})
```

with:

```go
	return &got
}

// withoutHarness clears a harness build's settings for one test, so the
// tests check the production behaviour in every build.
func withoutHarness(t *testing.T) {
	t.Helper()
	oldStatus, oldNever, oldCrash := healthStatus, neverConnected, crashAtStart
	t.Cleanup(func() { healthStatus, neverConnected, crashAtStart = oldStatus, oldNever, oldCrash })
	healthStatus, neverConnected, crashAtStart = "", false, false
}

func runArgs(args ...string) (string, int) {
	var out bytes.Buffer
	code := run(args, cli.Options{Stdout: &out, Stderr: &out})
```

In `cmd/darkpanel/agent_test.go`, replace:

```go
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, resolvePaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
	geteuid = func() int { return euid }
	installLayout = func() install.Layout { return s.layout }
	resolvePaths = func(dev bool) (paths.Paths, error) {
```

with:

```go
		geteuid, daemonRun, daemonEnroll, daemonCheck = oldEUID, oldRun, oldEnroll, oldCheck
		newSystemd, newUpdater, resolvePaths, installLayout = oldSystemd, oldUpdater, oldResolve, oldLayout
	})
	withoutHarness(t)
	geteuid = func() int { return euid }
	installLayout = func() install.Layout { return s.layout }
	resolvePaths = func(dev bool) (paths.Paths, error) {
```

In `cmd/darkpanel/agent_test.go`, replace:

```go
		cfg.Runtime.BackupDir != s.dev.AgentBackups() || cfg.Runtime.Exec == nil || cfg.Updater == nil {
		t.Errorf("agent run --dev passed %+v, want the dev paths and an opened runtime", cfg)
	}
	if cfg.AllowPeer == nil || !cfg.AllowPeer(uint32(os.Getuid())) || !cfg.AllowPeer(0) || cfg.AllowPeer(uint32(os.Getuid())+4242) { //nolint:gosec // G115: a uid fits in 32 bits
		t.Error("agent run --dev must admit root and the invoking user on the agent socket, and no one else")
	}
```

with:

```go
		cfg.Runtime.BackupDir != s.dev.AgentBackups() || cfg.Runtime.Exec == nil || cfg.Updater == nil {
		t.Errorf("agent run --dev passed %+v, want the dev paths and an opened runtime", cfg)
	}
	if cfg.NeverConnected {
		t.Error("agent run set NeverConnected outside a harnessbroken build")
	}
	if cfg.AllowPeer == nil || !cfg.AllowPeer(uint32(os.Getuid())) || !cfg.AllowPeer(0) || cfg.AllowPeer(uint32(os.Getuid())+4242) { //nolint:gosec // G115: a uid fits in 32 bits
		t.Error("agent run --dev must admit root and the invoking user on the agent socket, and no one else")
	}
```

In `cmd/darkpanel/signal_test.go`, replace:

```go
		t.Skip("runs only as the child of TestSecondInterruptKillsServe")
	}
	geteuid = func() int { return 1000 }
	serveRun = func(ctx context.Context, _ server.Options) error {
		fmt.Println("ready")
		<-ctx.Done()
```

with:

```go
		t.Skip("runs only as the child of TestSecondInterruptKillsServe")
	}
	geteuid = func() int { return 1000 }
	crashAtStart = false
	serveRun = func(ctx context.Context, _ server.Options) error {
		fmt.Println("ready")
		<-ctx.Done()
```

Create `cmd/darkpanel/harness_broken_test.go`:

```go
//go:build harnessbroken

package main

import (
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
)

func TestHarnessBroken(t *testing.T) {
	status, never := healthStatus, neverConnected
	if status != "degraded" || !never {
		t.Fatalf("a harnessbroken build has health status %q and NeverConnected %v, want degraded and true", status, never)
	}
	served := fakeServe(t, 0, nil)
	s := fakeAgent(t, 0)
	healthStatus, neverConnected = status, never
	if out, code := runArgs("serve", "--dev"); code != cli.ExitOK || len(*served) != 1 || (*served)[0].HealthStatus != "degraded" {
		t.Errorf("serve --dev: exit %d, %s; want it to pass HealthStatus degraded", code, out)
	}
	if out, code := runArgs("agent", "run", "--dev"); code != cli.ExitOK || len(s.runs) != 1 || !s.runs[0].NeverConnected {
		t.Errorf("agent run --dev: exit %d, %s; want it to pass NeverConnected", code, out)
	}
}
```

Create `cmd/darkpanel/harness_crash_test.go`:

```go
//go:build harnesscrash

package main

import (
	"os"
	"testing"

	"github.com/darkraise/darkpanel/internal/cli"
	"github.com/darkraise/darkpanel/internal/update/trustedkeys"
)

func TestHarnessCrash(t *testing.T) {
	if !crashAtStart {
		t.Fatal("a harnesscrash build does not crash at start")
	}
	served := fakeServe(t, 0, nil)
	s := fakeAgent(t, 0)
	crashAtStart = true
	for _, args := range [][]string{{"serve", "--dev"}, {"agent", "run", "--dev"}} {
		if out, code := runArgs(args...); code != cli.ExitError || out != "darkpanel: harnesscrash: exiting at start\n" {
			t.Errorf("darkpanel %v: exit %d, output %q; want exit 1 and the harnesscrash error", args, code, out)
		}
	}
	if len(*served) != 0 || len(s.runs) != 0 || len(s.updates) != 0 {
		t.Errorf("the crashing build served %d times, ran %d agents and opened %d updaters", len(*served), len(s.runs), len(s.updates))
	}
	if _, err := os.Stat(s.dev.AgentDir); !os.IsNotExist(err) {
		t.Errorf("agent run created %s before crashing: %v", s.dev.AgentDir, err)
	}
	withTrustedKeys(t, []trustedkeys.Key{{ID: "BCB10AB3E9AF694E", PublicKey: "RWROaa/pswqxvLcQvIucxc5teWzMb2E2rinAmlfu9fYXug3dfuI81MvD"}})
	if out, code := runArgs("selftest"); code != cli.ExitOK || out != "selftest: ok\n" {
		t.Errorf("selftest: exit %d, output %q; want it to pass in a harnesscrash build", code, out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go test -count=1 -run TestRunNeverConnected ./internal/agent/daemon/`
Expected: FAIL with `internal/agent/daemon/daemon_test.go:419:6: cfg.NeverConnected undefined (type Config has no field or method NeverConnected)`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go test -count=1 -tags harnessmigration -run TestHarnessMigration ./internal/core/store/`
Expected: FAIL with `internal/core/store/migrations_harness_test.go:42:26: undefined: unionFS`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go test -count=1 ./cmd/darkpanel/`
Expected: FAIL with `cmd/darkpanel/agent_test.go:153:9: cfg.NeverConnected undefined (type daemon.Config has no field or method NeverConnected)`

(`TestNoHarnessMigration` passes already: it guards the default build.)

- [ ] **Step 3: Write the implementation**

In `internal/agent/daemon/daemon.go`, replace:

```go
	// Heartbeat paces the agent's pings of the controller; the zero value is
	// transport's defaults.
	Heartbeat transport.HeartbeatConfig
}

func (c Config) check() error {
```

with:

```go
	// Heartbeat paces the agent's pings of the controller; the zero value is
	// transport's defaults.
	Heartbeat transport.HeartbeatConfig
	// NeverConnected makes Hello report no live controller session while
	// enrolled, whatever the session's state, for a build that must fail an
	// update's health check; false in production.
	NeverConnected bool
}

func (c Config) check() error {
```

In `internal/agent/daemon/daemon.go`, replace:

```go
		if !id.enrolled {
			return nil
		}
		v := d.connected.Load()
		return &v
	}
	rt, err := runtime.Open(ctx, rc)
```

with:

```go
		if !id.enrolled {
			return nil
		}
		v := d.connected.Load() && !d.cfg.NeverConnected
		return &v
	}
	rt, err := runtime.Open(ctx, rc)
```

In `internal/core/store/migrations.go`, replace:

```go
	if err != nil {
		return MigrationSet{}, fmt.Errorf("store: core migrations: %w", err)
	}
	return MigrationSet{Module: "core", FS: sub}, nil
}
```

with:

```go
	if err != nil {
		return MigrationSet{}, fmt.Errorf("store: core migrations: %w", err)
	}
	set, err := withHarnessMigrations(side, sub)
	if err != nil {
		return MigrationSet{}, err
	}
	return MigrationSet{Module: "core", FS: set}, nil
}
```

Create `internal/core/store/migrations_default.go`:

```go
//go:build !harnessmigration

package store

import "io/fs"

// withHarnessMigrations returns the core migrations of side unchanged; only
// the harnessmigration build adds to them.
func withHarnessMigrations(_ string, core fs.FS) (fs.FS, error) { return core, nil }
```

Create `internal/core/store/migrations_harness.go`:

```go
//go:build harnessmigration

package store

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"slices"
	"strings"
)

// harnessFS holds one extra core migration per side, numbered 9000 so that it
// sorts after every real one and an older binary ignores it.
//
//go:embed harnessmigrations/*/*.sql
var harnessFS embed.FS

// withHarnessMigrations returns the core migrations of side joined with the
// harness migration of side.
func withHarnessMigrations(side string, core fs.FS) (fs.FS, error) {
	extra, err := fs.Sub(harnessFS, "harnessmigrations/"+side)
	if err != nil {
		return nil, fmt.Errorf("store: harness migrations: %w", err)
	}
	return unionFS{core, extra}, nil
}

// unionFS is two flat directories read as one; a name in both is refused,
// so no harness file can shadow a real migration.
type unionFS [2]fs.FS

func (u unionFS) Open(name string) (fs.File, error) {
	// A directory handle would list one side only, so the directory is read
	// through ReadDir alone.
	if name == "." {
		return nil, &fs.PathError{Op: "open", Path: name, Err: errors.New("use ReadDir")}
	}
	f, err := u[0].Open(name)
	if errors.Is(err, fs.ErrNotExist) {
		return u[1].Open(name)
	}
	return f, err
}

func (u unionFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != "." {
		return nil, &fs.PathError{Op: "readdir", Path: name, Err: fs.ErrNotExist}
	}
	var all []fs.DirEntry
	for _, f := range u {
		entries, err := fs.ReadDir(f, ".")
		if err != nil {
			return nil, err
		}
		all = append(all, entries...)
	}
	slices.SortFunc(all, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	for i := 1; i < len(all); i++ {
		if all[i].Name() == all[i-1].Name() {
			return nil, fmt.Errorf("store: migration %s is in both the core and the harness migrations", all[i].Name())
		}
	}
	return all, nil
}
```

Create `internal/core/store/harnessmigrations/agent/9000_harness_marker.sql`:

```sql
-- Applied only by a harnessmigration build: the harness reads this row to see
-- that a newer schema was applied, then that an older binary runs on it.
CREATE TABLE harness_marker (note TEXT NOT NULL) STRICT;
INSERT INTO harness_marker (note) VALUES ('written by the harnessmigration build');
```

Create `internal/core/store/harnessmigrations/controller/9000_harness_marker.sql`:

```sql
-- Applied only by a harnessmigration build: the harness reads this row to see
-- that a newer schema was applied, then that an older binary runs on it.
CREATE TABLE harness_marker (note TEXT NOT NULL) STRICT;
INSERT INTO harness_marker (note) VALUES ('written by the harnessmigration build');
```

In `cmd/darkpanel/main.go`, replace:

```go
// replace it.
var trustedKeys = trustedkeys.Keys

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
```

with:

```go
// replace it.
var trustedKeys = trustedkeys.Keys

// The harness's faulty builds set these in init (harness_broken.go,
// harness_crash.go); they are zero in every other build.
var (
	healthStatus   string // serve's /healthz status; empty is ok
	neverConnected bool   // agent run's Hello never reports a live session
	crashAtStart   bool   // serve and agent run exit at once
)

// errCrashAtStart is how a harnesscrash build's services exit.
var errCrashAtStart = errors.New("harnesscrash: exiting at start")

type versionInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
```

In `cmd/darkpanel/main.go`, replace:

```go
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := paths.Resolve(dev)
			if err != nil {
				return err
```

with:

```go
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			if crashAtStart {
				return errCrashAtStart
			}
			p, err := paths.Resolve(dev)
			if err != nil {
				return err
```

In `cmd/darkpanel/main.go`, replace:

```go
			return serveRun(ctx, server.Options{
				Paths: p, Listen: specs, Dev: dev, Log: log,
				EmbeddedAgent: embeddedAgent, AgentListen: agentListen, AgentAdvertise: agentAdvertise,
			})
		},
	}
```

with:

```go
			return serveRun(ctx, server.Options{
				Paths: p, Listen: specs, Dev: dev, Log: log,
				EmbeddedAgent: embeddedAgent, AgentListen: agentListen, AgentAdvertise: agentAdvertise,
				HealthStatus: healthStatus,
			})
		},
	}
```

In `cmd/darkpanel/agent.go`, replace:

```go
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("run", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := resolvePaths(dev)
			if err != nil {
				return err
```

with:

```go
		Args:    cobra.NoArgs,
		PreRunE: requireRoot("run", &dev),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if crashAtStart {
				return errCrashAtStart
			}
			p, err := resolvePaths(dev)
			if err != nil {
				return err
```

In `cmd/darkpanel/agent.go`, replace:

```go
					BinaryVersion: buildinfo.Version, BackupDir: p.AgentBackups(),
					Exec: platform.NewRunner(platform.ProductionAllowlist()),
				},
				Updater: host.Updater,
			}
			if dev {
				// The development agent runs as the invoking user, whose own
```

with:

```go
					BinaryVersion: buildinfo.Version, BackupDir: p.AgentBackups(),
					Exec: platform.NewRunner(platform.ProductionAllowlist()),
				},
				Updater:        host.Updater,
				NeverConnected: neverConnected,
			}
			if dev {
				// The development agent runs as the invoking user, whose own
```

Create `cmd/darkpanel/harness_broken.go`:

```go
//go:build harnessbroken

package main

// A harnessbroken build passes selftest but reports a degraded controller and
// an agent that never reaches it, so an update to it fails its health check
// and rolls back.
func init() {
	healthStatus = "degraded"
	neverConnected = true
}
```

Create `cmd/darkpanel/harness_crash.go`:

```go
//go:build harnesscrash

package main

// A harnesscrash build passes selftest but its services exit at once, so
// systemd's restarts exhaust the start limit and OnFailure= rolls it back.
func init() { crashAtStart = true }
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 900 go test -race -count=1 ./cmd/darkpanel/ ./internal/core/store/ ./internal/agent/daemon/`
Expected: PASS

Run each: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 900 go test -race -count=1 -tags <tags> ./cmd/darkpanel/ ./internal/core/store/` for `<tags>` = `harnessbroken`, `harnesscrash`, `harnessmigration`, `harnesskeys,harnessbroken`, `harnesskeys,harnesscrash`, `harnesskeys,harnessmigration,harnessbroken`
Expected: PASS for every one

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go vet ./...` and, for each of `harnessbroken`, `harnesscrash`, `harnessmigration`, `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 600 go vet -tags <tag> ./cmd/darkpanel/ ./internal/core/store/`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 900 golangci-lint run --allow-serial-runners ./...` and, for each of `harnessbroken`, `harnesscrash`, `harnessmigration`, `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 900 golangci-lint run --allow-serial-runners --build-tags <tag> ./cmd/darkpanel/ ./internal/core/store/`
Expected: `0 issues.` for every one

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18 timeout 120 go mod tidy -diff`
Expected: no output, exit 0

Build every variant the harness needs and check it:

```bash
export GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/18
B=$TMPDIR/hb; mkdir -p $B
build() { timeout 600 go build -trimpath -tags "$2" -ldflags "-X github.com/darkraise/darkpanel/internal/buildinfo.Version=$1" -o $B/darkpanel-$1 ./cmd/darkpanel; }
build 0.3.0 harnesskeys
build 0.3.1 harnesskeys,harnessbroken
build 0.3.2 harnesskeys,harnesscrash
build 0.3.4 harnesskeys,harnessmigration,harnessbroken
for v in 0.3.0 0.3.1 0.3.2 0.3.4; do $B/darkpanel-$v version --json; $B/darkpanel-$v selftest; done
S=$(mktemp -d $TMPDIR/st.XXXX)
XDG_STATE_HOME=$S XDG_RUNTIME_DIR= $B/darkpanel-0.3.2 serve --dev; echo "exit $?"
rm -rf $S $B
```

Expected: each `version --json` reports its version with `"testBuild":true,"trustedKeys":["BCB10AB3E9AF694E"]`; each `selftest` prints `selftest: ok`; the 0.3.2 serve prints `darkpanel: harnesscrash: exiting at start` and `exit 1`.

- [ ] **Step 5: Commit**

```bash
git add cmd/darkpanel/main.go cmd/darkpanel/agent.go cmd/darkpanel/serve_test.go cmd/darkpanel/agent_test.go cmd/darkpanel/signal_test.go cmd/darkpanel/harness_broken.go cmd/darkpanel/harness_crash.go cmd/darkpanel/harness_broken_test.go cmd/darkpanel/harness_crash_test.go internal/agent/daemon/daemon.go internal/agent/daemon/daemon_test.go internal/core/store/migrations.go internal/core/store/migrations_default.go internal/core/store/migrations_harness.go internal/core/store/migrations_default_test.go internal/core/store/migrations_harness_test.go internal/core/store/harnessmigrations/agent/9000_harness_marker.sql internal/core/store/harnessmigrations/controller/9000_harness_marker.sql
git commit -m "feat(cmd): add the harness build variants"
```

---

### Task 19: Harness provisioning, builds and workflow

**Files:**
- Modify: `test/harness/provision.go`
- Modify: `Makefile`
- Modify: `.github/workflows/harness.yml`
- Test: `test/harness/provision_test.go`

**Interfaces:**
- Consumes: `listen.AgentPort` (`internal/controller/listen`, 7443); C18 (build tags `harnessbroken`, `harnesscrash`, `harnessmigration`; `harnesskeys` from the earlier plans, which trusts only `internal/update/trustedkeys/testdata/harness.key` and embeds the base URL `http://198.18.0.20:8000`); `scripts/gen-install-sh` (`-out`); the existing `TargetScript`, `ScannerScript`, `targetTemplate`, `scriptHeader`, `fetchFunc` of package `harness`.
- Produces: C19's `make harness-builds`: `bin/harness/darkpanel-<version>` for `0.2.0` (`harnesskeys`), `0.3.0` (`harnesskeys`), `0.3.1` (`harnesskeys,harnessbroken`), `0.3.2` (`harnesskeys,harnesscrash`), `0.3.3` (`harnesskeys`), `0.3.4` (`harnesskeys,harnessmigration,harnessbroken`), each linux/amd64, `CGO_ENABLED=0`, `-trimpath`, stamped with its own version, without the UI; and `bin/harness/install.sh`. Makefile names: the function `ldflags` (one argument, the version; `LDFLAGS` is now `$(call ldflags,$(VERSION))`), `HARNESS_DIR`, `HARNESS_BUILDS`, the phony target `harness-builds`. Package `harness`: variable `agentRule`. Every target VM now gets `sqlite3` (apt, unpinned like curl and ufw) and the UFW rule `ufw allow in on dppriv to any port 7443 proto tcp` before UFW is enabled. The workflow's build job runs `make harness-builds` and uploads `bin/harness` as the artifact `darkpanel-harness`; the vm job downloads it into `bin/harness`, its `timeout-minutes` is 120 and go test's `-timeout` 100m. Task 20 adds the `-m2` argument, together with the flag it names.

**Depends on:** Task 18
**Implementer:** dr-superpowers:impl-sonnet-high
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 2 = 5

Notes for the implementer:
- One provisioning script serves TestM1's target and both M2 targets. ufw validates only an interface name's syntax (ufw 0.36.2 `common.py` `set_interface`), so on TestM1's target, which has no `dppriv` NIC, the rule is accepted and matches nothing; TestM1's checks (`ufw status` shows 22/tcp allowed, the scan finds only 22/tcp) are unaffected.
- The scanner needs nothing more to serve the releases: `python3` is on every cloud image, because cloud-init is a Python 3 program. Task 20 starts `/usr/bin/python3 -m http.server` there.
- `make harness-builds` needs neither pnpm nor the web build: no scenario uses the UI, and `install.sh` and the updater check only signatures and versions.

- [ ] **Step 1: Write the failing test**

In `test/harness/provision_test.go`, replace:

```go
		if strings.Contains(s, c.other) {
			t.Errorf("%s script names another OS's packages (%s):\n%s", c.os.ID, c.other, s)
		}
		allow, enable := strings.Index(s, "ufw allow 22/tcp"), strings.Index(s, "ufw --force enable")
		if allow < 0 || enable < allow || strings.Index(s, "systemctl enable --now ufw docker tailscaled") < enable {
			t.Errorf("%s script must allow 22/tcp, enable UFW, then start ufw, Docker and tailscaled under systemd:\n%s", c.os.ID, s)
		}
		bashSyntax(t, s)
	}
	scanner := ScannerScript()
```

with:

```go
		if strings.Contains(s, c.other) {
			t.Errorf("%s script names another OS's packages (%s):\n%s", c.os.ID, c.other, s)
		}
		if !strings.Contains(s, "apt_get install --no-install-recommends ca-certificates curl sqlite3 ufw\n") {
			t.Errorf("%s script does not install sqlite3 beside curl and ufw:\n%s", c.os.ID, s)
		}
		allow, enable := strings.Index(s, "ufw allow 22/tcp"), strings.Index(s, "ufw --force enable")
		if allow < 0 || enable < allow || strings.Index(s, "systemctl enable --now ufw docker tailscaled") < enable {
			t.Errorf("%s script must allow 22/tcp, enable UFW, then start ufw, Docker and tailscaled under systemd:\n%s", c.os.ID, s)
		}
		if agent := strings.Index(s, "ufw allow in on dppriv to any port 7443 proto tcp\n"); agent < allow || agent > enable {
			t.Errorf("%s script must allow 7443/tcp in on dppriv only, before UFW is enabled:\n%s", c.os.ID, s)
		}
		bashSyntax(t, s)
	}
	scanner := ScannerScript()
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 timeout 600 go test -tags harness -count=1 -run TestTargetScript ./test/harness/`
Expected: FAIL with `provision_test.go:54: ubuntu-22.04 script does not install sqlite3 beside curl and ufw:` and `provision_test.go:61: ubuntu-22.04 script must allow 7443/tcp in on dppriv only, before UFW is enabled:` (and the same two lines for debian-13)

- [ ] **Step 3: Write the implementation**

In `test/harness/provision.go`, replace:

```go
package harness

import (
	"strings"
	"text/template"
)

// scriptHeader starts every provisioning script. The lock timeout waits out
```

with:

```go
package harness

import (
	"strconv"
	"strings"
	"text/template"

	"github.com/darkraise/darkpanel/internal/controller/listen"
)

// scriptHeader starts every provisioning script. The lock timeout waits out
```

In `test/harness/provision.go`, replace:

```go
}
`

var targetTemplate = template.Must(template.New("target").Parse(scriptHeader + fetchFunc + `apt_get update
apt_get install --no-install-recommends ca-certificates curl ufw
pkgdir=/var/cache/harness-packages
mkdir -p "$pkgdir"
{{range .Packages}}fetch '{{.Name}}' '{{.Version}}' '{{.URL}}' '{{.SHA256}}'
{{end}}apt_get install --no-install-recommends{{range .Packages}} "$pkgdir/{{.Name}}.deb"{{end}}
ufw allow 22/tcp
ufw --force enable
systemctl enable --now ufw docker tailscaled
`))

// TargetScript provisions the target VM of o: UFW enabled with only 22/tcp
// allowed and ufw.service started (Debian's package enables the unit on
// install without starting it, so it would stay inactive until a reboot),
// and Docker Engine and tailscaled from o's pinned .deb files. tailscaled
// runs but is not logged in until M5.
func TargetScript(o OS) string {
	var b strings.Builder
	if err := targetTemplate.Execute(&b, o); err != nil {
```

with:

```go
}
`

// agentRule lets agents reach the controller's agent listener on the
// private link that stands in for the tailnet, and nowhere else. dppriv is
// the name the vm package gives the private NIC; ufw checks only the
// interface name's syntax, so on a target without that NIC the rule matches
// nothing.
var agentRule = "ufw allow in on dppriv to any port " + strconv.Itoa(listen.AgentPort) + " proto tcp\n"

var targetTemplate = template.Must(template.New("target").Parse(scriptHeader + fetchFunc + `apt_get update
apt_get install --no-install-recommends ca-certificates curl sqlite3 ufw
pkgdir=/var/cache/harness-packages
mkdir -p "$pkgdir"
{{range .Packages}}fetch '{{.Name}}' '{{.Version}}' '{{.URL}}' '{{.SHA256}}'
{{end}}apt_get install --no-install-recommends{{range .Packages}} "$pkgdir/{{.Name}}.deb"{{end}}
ufw allow 22/tcp
` + agentRule + `ufw --force enable
systemctl enable --now ufw docker tailscaled
`))

// TargetScript provisions the target VM of o: UFW enabled with 22/tcp
// allowed, and the agent port only on the private link, and ufw.service
// started (Debian's package enables the unit on install without starting it,
// so it would stay inactive until a reboot), sqlite3 for reading the
// databases, and Docker Engine and tailscaled from o's pinned .deb files.
// tailscaled runs but is not logged in until M5.
func TargetScript(o OS) string {
	var b strings.Builder
	if err := targetTemplate.Execute(&b, o); err != nil {
```

In `Makefile`, replace:

```makefile
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
ARCHES := amd64 arm64
BUILDINFO := github.com/darkraise/darkpanel/internal/buildinfo
LDFLAGS := -s -w -X $(BUILDINFO).Version=$(VERSION) -X $(BUILDINFO).Commit=$(COMMIT)
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
RELEASE_DIR := bin/release
CYCLONEDX ?= cyclonedx-gomod
```

with:

```makefile
COMMIT ?= $(or $(shell git rev-parse --short HEAD 2>/dev/null),unknown)
ARCHES := amd64 arm64
BUILDINFO := github.com/darkraise/darkpanel/internal/buildinfo
# ldflags takes the version, so harness-builds can stamp each build its own.
ldflags = -s -w -X $(BUILDINFO).Version=$(1) -X $(BUILDINFO).Commit=$(COMMIT)
LDFLAGS := $(call ldflags,$(VERSION))
GOVULNCHECK := golang.org/x/vuln/cmd/govulncheck@v1.9.0
RELEASE_DIR := bin/release
CYCLONEDX ?= cyclonedx-gomod
```

In `Makefile`, replace:

```makefile
# install-sh embeds the production trusted keys; KEYS=harness embeds the
# harness test key and base URL instead.
KEYS ?= production
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>
```

with:

```makefile
# install-sh embeds the production trusted keys; KEYS=harness embeds the
# harness test key and base URL instead.
KEYS ?= production
# The harness's signed test releases (spec section 13), as version:tags.
# Every one trusts only the harness key; the scenarios use no UI, so none
# embeds it.
HARNESS_DIR := bin/harness
HARNESS_BUILDS := 0.2.0:harnesskeys 0.3.0:harnesskeys 0.3.1:harnesskeys,harnessbroken \
	0.3.2:harnesskeys,harnesscrash 0.3.3:harnesskeys 0.3.4:harnesskeys,harnessmigration,harnessbroken
# A string from web/index.html; finding it in a binary proves the UI is embedded.
UI_MARKER := <title>Darkpanel</title>
```

In `Makefile`, replace:

```makefile
export GOTOOLCHAIN := local

# release is phony on purpose: the release/ directory would otherwise satisfy it.
.PHONY: web-deps web build verify-build lint-go lint-web lint-sh lint test-go test-web test-adr test-release test schemas vuln check clean release-check release release-prep install-sh

web-deps:
	pnpm -C web install --frozen-lockfile
```

with:

```makefile
export GOTOOLCHAIN := local

# release is phony on purpose: the release/ directory would otherwise satisfy it.
.PHONY: web-deps web build verify-build lint-go lint-web lint-sh lint test-go test-web test-adr test-release test schemas vuln check clean release-check release release-prep install-sh harness-builds

web-deps:
	pnpm -C web install --frozen-lockfile
```

In `Makefile`, replace:

```makefile

install-sh:
	go run $(if $(filter harness,$(KEYS)),-tags harnesskeys,) ./scripts/gen-install-sh -out install.sh
```

with:

```makefile

install-sh:
	go run $(if $(filter harness,$(KEYS)),-tags harnesskeys,) ./scripts/gen-install-sh -out install.sh

# harness-builds writes bin/harness/darkpanel-<version> for every harness
# release and the install.sh that trusts the harness key; the harness test's
# -m2 flag names the directory.
harness-builds:
	mkdir -p $(HARNESS_DIR)
	for build in $(HARNESS_BUILDS); do \
		version=$${build%%:*}; \
		CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -tags "$${build#*:}" \
			-ldflags "$(call ldflags,$$version)" -o $(HARNESS_DIR)/darkpanel-$$version ./cmd/darkpanel; \
	done
	go run -tags harnesskeys ./scripts/gen-install-sh -out $(HARNESS_DIR)/install.sh
```

In `.github/workflows/harness.yml`, replace:

```yaml
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml
      - run: make build verify-build
      - name: Harness unit tests
        run: go test -tags harness ./test/harness/...
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
```

with:

```yaml
          cache: pnpm
          cache-dependency-path: web/pnpm-lock.yaml
      - run: make build verify-build
      - run: make harness-builds
      - name: Harness unit tests
        run: go test -tags harness ./test/harness/...
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
```

In `.github/workflows/harness.yml`, replace:

```yaml
          path: bin/darkpanel-linux-amd64
          retention-days: 3
          if-no-files-found: error

  vm:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 60
    strategy:
      fail-fast: false
      matrix:
```

with:

```yaml
          path: bin/darkpanel-linux-amd64
          retention-days: 3
          if-no-files-found: error
      - uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: darkpanel-harness
          path: bin/harness
          retention-days: 3
          if-no-files-found: error

  vm:
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 120
    strategy:
      fail-fast: false
      matrix:
```

In `.github/workflows/harness.yml`, replace:

```yaml
        with:
          name: darkpanel-linux-amd64
          path: bin
      - uses: actions/cache@55cc8345863c7cc4c66a329aec7e433d2d1c52a9 # v6.1.0
        with:
          path: ~/.cache/darkpanel-harness
```

with:

```yaml
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
```

In `.github/workflows/harness.yml`, replace:

```yaml
      - run: go vet -tags harness ./test/...
      - name: Run the harness
        run: >-
          go test -tags harness -timeout 45m -v ./test/harness/
          -args -os ${{ matrix.os }}
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -commit "$GITHUB_SHA"
```

with:

```yaml
      - run: go vet -tags harness ./test/...
      - name: Run the harness
        run: >-
          go test -tags harness -timeout 100m -v ./test/harness/
          -args -os ${{ matrix.os }}
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -commit "$GITHUB_SHA"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness`, `test/harness/release` and `test/harness/vm`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 go mod tidy -diff`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/19 timeout 900 make harness-builds && for v in 0.2.0 0.3.0 0.3.1 0.3.2 0.3.3 0.3.4; do bin/harness/darkpanel-$v version --json; bin/harness/darkpanel-$v selftest; done && grep -n "^DEFAULT_BASE_URL=" bin/harness/install.sh`
Expected: six `version --json` lines, each with `"version":"<v>"`, `"ui":false`, `"testBuild":true` and `"trustedKeys":["BCB10AB3E9AF694E"]`, each followed by `selftest: ok`; then `26:DEFAULT_BASE_URL='http://198.18.0.20:8000'`

Run: `make -n build | grep -o 'ldflags "[^"]*"'`
Expected: `ldflags "-s -w -X github.com/darkraise/darkpanel/internal/buildinfo.Version=<git describe> -X github.com/darkraise/darkpanel/internal/buildinfo.Commit=<short commit>"`, as before the change

- [ ] **Step 5: Commit**

```bash
git add test/harness/provision.go test/harness/provision_test.go Makefile .github/workflows/harness.yml
git commit -m "feat(harness): add M2 provisioning and builds"
```

---

### Task 20: M2 harness topology and fleet scenarios

**Files:**
- Create: `test/harness/api.go`
- Create: `test/harness/m2_test.go`
- Modify: `test/harness/m1_test.go`
- Modify: `test/harness/vm/vm.go`
- Modify: `.github/workflows/harness.yml`
- Test: `test/harness/api_test.go` (create)
- Test: `test/harness/vm/vm_test.go`

**Interfaces:**
- Consumes: C8 (`vm.Config.PrivMAC`, `PrivAddrs`, `PrivMcastPort`, `(*vm.VM).DialContext`); C9 (`release.Version`, `release.LoadKey`, `release.Publish`); C19 and Task 19 (`bin/harness/darkpanel-<version>`, `bin/harness/install.sh`, `sqlite3` and the 7443/tcp rule on every target); C15/C17 (`darkpanel agent enroll --token-file`, `install.sh --role controller --agent-listen`, `--role agent --token-file`); from package `harness`: `osFlag`, `workdirFlag`, `cacheFlag`, `targetIPv4`, `targetIPv6`, `scannerIPv4`, `scannerIPv6`, `checkScan`, `darkpanelListeners`, `eventually`, `pollInterval`, `TargetScript`, `ScannerScript`, `LoadPins`, `PinsFile`, `OS`; from package `vm`: `runWith`, `connect`, `closeClient`, `NewKey`, `McastGroup`; `auth.TOTPCode`, `auth.TOTPPeriod`; `api.CSRFHeader`; `listen.AgentPort`; `install.ServiceUser`; `update.ControllerUnit`, `update.AgentUnit`; `paths.Production`; `errcode.HostOffline`; `core.Host`, `core.HostList`, `core.HostCreateInput`, `core.HostTokenInput`, `core.HostIDInput`, `core.EnrollmentGrant`, `core.UpdateStatusInput`, `core.ErrorDetail`, `core.ParseVersion`, `core.StateCompleted`, `core.StateFailed`, `core.HostEnrolled`, `core.HostPending`, `core.HostRevoked`.
- Produces: `func (v *VM) RunInput(ctx context.Context, cmd string, stdin io.Reader) (string, error)` (package `vm`) and its test `TestRunInputSendsStandardInput`. Package `harness`, file `api.go`: `type API`, `func NewAPI(base string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *API`, `type APIError struct{ Status int; Code, Detail string }`, `type Change`, methods `Login`, `Apply`, `Read`, `Resource`, `Hosts`, `do`; functions `findHost`, `localHost`, `actionPath`, `resourcePath`; constants `apiTimeout`, `maxProblemBody`; tests `TestActionPath`, `fakeAPI`, `TestAPI` (`api_test.go`). File `m1_test.go`: `harnessImages`, `bootVMs`, extracted from `bootPair`, which keeps its behaviour. File `m2_test.go`: the flag `m2Flag` (`-m2`); constants `agentIPv4`, `agentIPv6`, `controllerPriv`, `agentPriv`, `versionN`, `versionGood`, `versionBroken`, `versionCrash`, `versionReboot`, `versionMigration`, `releaseChannel`, `harnessKey`, `releaseURL`, `releaseDir`, `releaseUnit`, `installScript`, `enrollFile`, `adminName`, `apiURL`, `m2Deadline`; variables `m2Versions`, `agentAddress`; `type fleet` (fields `os`, `controller`, `agent`, `scanner`, `api`, `localID`, `agentID`, `password`, `totpSecret`, `lastCode`); `TestM2` with the steps `install` (gate), `self-management` (gate), `offline`, `revoke` (gate); functions `bootFleet`, `serveReleases`, `copyTree`, `scenarioInstall`, `installRole`, `createAdmin`, `login`, `nextCode`, `nextStep`, `totpSecret`, `writeToken`, `removeToken`, `scenarioSelfManagement`, `scenarioOffline`, `scenarioRevoke`, `vmNow`, `waitHosts`, `hostsAt`, `requireBinaryVersion`; tests `TestTOTPSecret`, `TestNextStep`, `TestHostsAt`. The workflow passes `-m2 "$GITHUB_WORKSPACE/bin/harness"` and uploads `harness/**/*.log`, because TestM2's VMs log under `<workdir>/m2`.

**Items:** 1, 2, 5, 6, 8, 19
**Depends on:** Task 8, Task 9, Task 15, Task 17, Task 19
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 1 = 4

Notes for the implementer:
- TestM1 still runs first: go test runs top-level tests one after another unless they call `t.Parallel`, and each test's cleanups (`bootVMs` registers `Shutdown`) stop its VMs before the next test starts. TestM2 skips unless both `-os` and `-m2` are given.
- Topology: controller `198.18.0.10`/`2001:db8:d0::10` and `10.77.0.10/24`, agent `198.18.0.11`/`2001:db8:d0::11` and `10.77.0.11/24`, scanner `198.18.0.20`/`2001:db8:d0::20`; the private link is a second multicast port (the internet link's plus one). Every VM gets 2 GiB.
- The first admin: without a terminal, `darkpanel user create` reads the password and then the activation code from standard input, but the code depends on the secret it prints in between. So `createAdmin` feeds the password alone; the CLI prints `Secret: <base32>`, then stops with `no code given; activate the user with: darkpanel user activate --username admin` (exit 2), and `user activate --username admin --yes` gets the code (`code` is a secret field, read from standard input). The controller refuses a TOTP step it has accepted before, so `nextCode` waits for the next 30 s step before every later code (at most 30 s).
- The API client logs in at `POST /api/v1/auth/login` and sends `X-CSRF-Token` on every POST; plans and applies go to `/api/v1/actions/<name>/v<n>/{plan,apply,read}` (controller side) or `/api/v1/hosts/<id>/actions/...` (agent side), resources to `/api/v1/hosts/<id>/resources/<module>/<resource>`. `Apply` passes the confirmation phrase the plan names, so irreversible actions (`core.host.revoke`, `core.host.token`) apply as an operator typing the phrase would. Sessions live in controller.db, so the session survives the controller's restarts.

- [ ] **Step 1: Write the failing test**

Create `test/harness/api_test.go` with this complete content:

```go
//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
)

func TestActionPath(t *testing.T) {
	cases := []struct {
		host, action, op, want string
	}{
		{"", "core.host.create/v1", "plan", "/api/v1/actions/core.host.create/v1/plan"},
		{"", "core.host.list/v1", "read", "/api/v1/actions/core.host.list/v1/read"},
		{"01HZY", "core.update.status/v2", "read", "/api/v1/hosts/01HZY/actions/core.update.status/v2/read"},
		{"a b", "core.update.apply/v1", "apply", "/api/v1/hosts/a%20b/actions/core.update.apply/v1/apply"},
	}
	for _, c := range cases {
		got, err := actionPath(c.host, c.action, c.op)
		if err != nil || got != c.want {
			t.Errorf("actionPath(%q, %q, %q) = %q, %v; want %q", c.host, c.action, c.op, got, err, c.want)
		}
	}
	for _, bad := range []string{"core.host.create", "core.host.create/", "/v1", "core.host.create/1"} {
		if got, err := actionPath("", bad, "plan"); err == nil {
			t.Errorf("actionPath of %q = %q, want an error", bad, got)
		}
	}
	if got, err := resourcePath("01HZY", "hostinfo.summary"); err != nil || got != "/api/v1/hosts/01HZY/resources/hostinfo/summary" {
		t.Errorf("resourcePath = %q, %v", got, err)
	}
	if got, err := resourcePath("01HZY", "summary"); err == nil {
		t.Errorf("resourcePath of a name without a module = %q, want an error", got)
	}
}

// fakeAPI is a controller API with one user: it checks the session cookie
// and, on every POST but the login, the CSRF token.
func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	const session, csrf = "s3ss10n", "csrf-t0ken"
	type planBody struct {
		Input json.RawMessage `json:"input"`
	}
	type applyBody struct {
		PlanID       string `json:"planId"`
		Confirmation string `json:"confirmation"`
	}
	problem := func(w http.ResponseWriter, status int, code, detail string) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]any{"type": "about:blank", "title": "x", "status": status, "detail": detail, "code": code})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/v1/auth/login", func(w http.ResponseWriter, r *http.Request) {
		var in struct{ Username, Password, Code string }
		if err := json.NewDecoder(r.Body).Decode(&in); err != nil || in.Username != "admin" || in.Password != "pw" || in.Code != "123456" {
			problem(w, http.StatusUnauthorized, "auth.invalid_credentials", "invalid username, password or code")
			return
		}
		//nolint:gosec // G124: a cookie of plain HTTP cannot be Secure, as the controller's loopback one cannot
		http.SetCookie(w, &http.Cookie{Name: "darkpanel_session", Value: session, Path: "/", HttpOnly: true, SameSite: http.SameSiteStrictMode})
		_ = json.NewEncoder(w).Encode(map[string]any{"csrfToken": csrf, "user": map[string]any{"username": "admin"}})
	})
	authed := func(h http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			if c, err := r.Cookie("darkpanel_session"); err != nil || c.Value != session {
				problem(w, http.StatusUnauthorized, "auth.unauthenticated", "no session")
				return
			}
			if r.Method == http.MethodPost && r.Header.Get("X-CSRF-Token") != csrf {
				problem(w, http.StatusForbidden, "auth.csrf", "missing or invalid X-CSRF-Token header")
				return
			}
			h(w, r)
		}
	}
	mux.HandleFunc("POST /api/v1/actions/core.host.revoke/v1/plan", authed(func(w http.ResponseWriter, r *http.Request) {
		var in planBody
		_ = json.NewDecoder(r.Body).Decode(&in)
		if string(in.Input) != `{"hostId":"h1"}` {
			problem(w, http.StatusBadRequest, "request.invalid_input", "input "+string(in.Input))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"planId": "p1", "confirmation": "agent"})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.revoke/v1/apply", authed(func(w http.ResponseWriter, r *http.Request) {
		var in applyBody
		_ = json.NewDecoder(r.Body).Decode(&in)
		if in.PlanID != "p1" || in.Confirmation != "agent" {
			problem(w, http.StatusBadRequest, "plan.confirmation_mismatch", "plan "+in.PlanID+" confirmed with "+in.Confirmation)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c1", "state": "completed", "output": map[string]any{"id": "h1", "state": "revoked"}})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.create/v1/plan", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"planId": "p2", "confirmation": nil})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.create/v1/apply", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "c2", "state": "failed", "output": nil,
			"error": map[string]any{"code": "host.name_taken", "message": "a host named agent exists"}})
	}))
	mux.HandleFunc("POST /api/v1/actions/core.host.list/v1/read", authed(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"hosts": []map[string]any{
			{"id": "h0", "name": "controller", "isLocal": true, "state": "enrolled", "online": true, "agentVersion": "0.2.0"},
			{"id": "h1", "name": "agent", "isLocal": false, "state": "enrolled", "online": false, "agentVersion": nil},
		}}})
	}))
	mux.HandleFunc("POST /api/v1/hosts/h1/actions/core.update.status/v1/read", authed(func(w http.ResponseWriter, _ *http.Request) {
		problem(w, http.StatusServiceUnavailable, "host.offline", "host h1 is offline")
	}))
	mux.HandleFunc("GET /api/v1/hosts/h0/resources/hostinfo/summary", authed(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"hostname":"controller"}`)
	}))
	mux.HandleFunc("GET /api/v1/hosts/h2/resources/hostinfo/summary", authed(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>bad gateway</html>")
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestAPI(t *testing.T) {
	srv := fakeAPI(t)
	var d net.Dialer
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	a := NewAPI(srv.URL, d.DialContext)

	var apiErr *APIError
	if _, err := a.Hosts(ctx); !errors.As(err, &apiErr) || apiErr.Status != http.StatusUnauthorized || apiErr.Code != "auth.unauthenticated" {
		t.Errorf("Hosts before the login = %v, want the API's auth.unauthenticated", err)
	}
	if err := a.Login(ctx, "admin", "wrong", "123456"); !errors.As(err, &apiErr) || apiErr.Code != "auth.invalid_credentials" {
		t.Errorf("Login with a wrong password = %v, want auth.invalid_credentials", err)
	}
	if err := a.Login(ctx, "admin", "pw", "123456"); err != nil {
		t.Fatalf("Login: %v", err)
	}

	hosts, err := a.Hosts(ctx)
	if err != nil || len(hosts) != 2 || !hosts[0].IsLocal || hosts[0].AgentVersion == nil || *hosts[0].AgentVersion != "0.2.0" ||
		hosts[1].Name != "agent" || hosts[1].Online || hosts[1].AgentVersion != nil {
		t.Errorf("Hosts = %+v, %v", hosts, err)
	}
	if h, ok := findHost(hosts, "h1"); !ok || h.Name != "agent" {
		t.Errorf("findHost(h1) = %+v, %v", h, ok)
	}
	if _, ok := findHost(hosts, "h9"); ok {
		t.Error("findHost found a host that is not listed")
	}
	if h, ok := localHost(hosts); !ok || h.ID != "h0" {
		t.Errorf("localHost = %+v, %v; want h0", h, ok)
	}

	var out core.Host
	ch, err := a.Apply(ctx, "", "core.host.revoke/v1", core.HostIDInput{HostID: "h1"}, &out)
	if err != nil || ch.ID != "c1" || out.State != "revoked" {
		t.Errorf("Apply of an irreversible action = %+v, %+v, %v; want the change with the plan's confirmation applied", ch, out, err)
	}
	if ch, err := a.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent"}, nil); err == nil ||
		!strings.Contains(err.Error(), "host.name_taken") || ch.State != core.StateFailed {
		t.Errorf("Apply of a change that failed = %+v, %v; want an error naming its code", ch, err)
	}

	err = a.Read(ctx, "h1", "core.update.status/v1", core.UpdateStatusInput{}, nil)
	if !errors.As(err, &apiErr) || apiErr.Status != http.StatusServiceUnavailable || apiErr.Code != "host.offline" || apiErr.Detail != "host h1 is offline" {
		t.Errorf("Read on an offline host = %v, want host.offline with its detail", err)
	}

	var sum struct {
		Hostname string `json:"hostname"`
	}
	if err := a.Resource(ctx, "h0", "hostinfo.summary", &sum); err != nil || sum.Hostname != "controller" {
		t.Errorf("Resource = %+v, %v", sum, err)
	}
	if err := a.Resource(ctx, "h2", "hostinfo.summary", &sum); !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadGateway ||
		apiErr.Code != "" || !strings.Contains(apiErr.Detail, "bad gateway") {
		t.Errorf("Resource answered without problem details = %v, want an APIError quoting the body", err)
	}
}
```

In `test/harness/vm/vm_test.go`, replace:

```go
		t.Errorf("commands run = %q, want %q", cmds, want)
	}
}
```

with:

```go
		t.Errorf("commands run = %q, want %q", cmds, want)
	}
}

// TestRunInputSendsStandardInput: RunInput feeds the command's standard input
// and ends it, as a CLI reading a password from a pipe needs.
func TestRunInputSendsStandardInput(t *testing.T) {
	hostKey, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	srv := &ssh.ServerConfig{NoClientAuth: true}
	srv.AddHostKey(hostKey)
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		t.Cleanup(func() { _ = c.Close() })
		_, chans, reqs, err := ssh.NewServerConn(c, srv)
		if err != nil {
			return
		}
		go ssh.DiscardRequests(reqs)
		for nc := range chans {
			ch, creqs, err := nc.Accept()
			if err != nil {
				continue
			}
			go func() {
				for r := range creqs {
					var req struct{ Command string }
					if r.Type != "exec" || ssh.Unmarshal(r.Payload, &req) != nil {
						_ = r.Reply(false, nil)
						continue
					}
					_ = r.Reply(true, nil)
					in, _ := io.ReadAll(ch) // until the client ends standard input
					_, _ = fmt.Fprintf(ch, "%s read %q", req.Command, in)
					_, _ = ch.SendRequest("exit-status", false, ssh.Marshal(struct{ Status uint32 }{0}))
					_ = ch.Close()
				}
			}()
		}
	}()
	key, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	v := &VM{cfg: Config{Name: "stdin", Key: key}, sshAddr: ln.Addr().String(), hostKey: hostKey.PublicKey(), exited: make(chan struct{})}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.connect(ctx); err != nil {
		t.Fatal(err)
	}
	defer v.closeClient()
	out, err := v.RunInput(ctx, "cat", strings.NewReader("secret\n"))
	if want := `cat read "secret\n"`; err != nil || out != want {
		t.Errorf("RunInput = %q, %v; want %q", out, err, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 600 go test -tags harness -count=1 -run 'TestAPI|TestActionPath' ./test/harness/`
Expected: FAIL with `test/harness/api_test.go:30:15: undefined: actionPath`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 600 go test -tags harness -count=1 -run TestRunInputSendsStandardInput ./test/harness/vm/`
Expected: FAIL with `test/harness/vm/vm_test.go:868:16: v.RunInput undefined (type *VM has no field or method RunInput)`

- [ ] **Step 3: Write the implementation**

In `test/harness/vm/vm.go`, replace:

```go
	return v.runWith(ctx, cmd, nil)
}

func (v *VM) runWith(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	c, err := v.sshClient(ctx)
	if err != nil {
```

with:

```go
	return v.runWith(ctx, cmd, nil)
}

// RunInput is Run with stdin as the command's standard input, which ends
// when stdin does. A secret goes this way rather than into cmd, which errors
// quote.
func (v *VM) RunInput(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	return v.runWith(ctx, cmd, stdin)
}

func (v *VM) runWith(ctx context.Context, cmd string, stdin io.Reader) (string, error) {
	c, err := v.sshClient(ctx)
	if err != nil {
```

Create `test/harness/api.go` with this complete content:

```go
//go:build harness

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/api"
	"github.com/darkraise/darkpanel/internal/core"
)

// apiTimeout bounds one API request; every call of the scenarios also has
// its own context.
const apiTimeout = 90 * time.Second

// API is a client of the controller's HTTP API logged in as one user: it
// keeps the session cookie and sends the session's CSRF token with every
// POST, as the web UI does.
type API struct {
	base string
	http *http.Client
	csrf string
}

// NewAPI returns a client of the API at base, such as http://127.0.0.1:8080,
// whose connections dial opens: vm.VM.DialContext reaches the controller's
// loopback listener inside its VM. Each request opens its own connection,
// so a connection the controller's restart closed is never reused.
func NewAPI(base string, dial func(ctx context.Context, network, addr string) (net.Conn, error)) *API {
	jar, err := cookiejar.New(nil)
	if err != nil {
		panic(err) // cookiejar.New fails only on options, and it gets none
	}
	tr := &http.Transport{DialContext: dial, DisableKeepAlives: true}
	return &API{base: strings.TrimSuffix(base, "/"), http: &http.Client{Transport: tr, Jar: jar, Timeout: apiTimeout}}
}

// APIError is an answer outside 2xx. Code and Detail come from its problem
// details; an answer without them leaves Code empty and quotes the body in
// Detail.
type APIError struct {
	Status int
	Code   string
	Detail string
}

func (e *APIError) Error() string {
	if e.Code == "" {
		return fmt.Sprintf("harness: the API answered %d: %s", e.Status, e.Detail)
	}
	return fmt.Sprintf("harness: the API answered %d %s: %s", e.Status, e.Code, e.Detail)
}

// Change is the part of a change the scenarios read.
type Change struct {
	ID     string            `json:"id"`
	State  string            `json:"state"`
	Output json.RawMessage   `json:"output"`
	Error  *core.ErrorDetail `json:"error"`
}

// Login opens a session with a password and a TOTP code.
func (a *API) Login(ctx context.Context, username, password, code string) error {
	var s struct {
		CSRFToken *string `json:"csrfToken"`
	}
	body := map[string]string{"username": username, "password": password, "code": code}
	if err := a.do(ctx, http.MethodPost, "/api/v1/auth/login", body, &s); err != nil {
		return err
	}
	if s.CSRFToken == nil || *s.CSRFToken == "" {
		return errors.New("harness: the login answered no CSRF token")
	}
	a.csrf = *s.CSRFToken
	return nil
}

// Apply plans action, written name/v<n>, with input on host, or on the
// controller when host is empty; applies the plan with the confirmation
// phrase it names, as an operator typing it would; and decodes the change's
// output into out unless out is nil. A change that did not complete is an
// error naming its code.
func (a *API) Apply(ctx context.Context, host, action string, input, out any) (Change, error) {
	planPath, err := actionPath(host, action, "plan")
	if err != nil {
		return Change{}, err
	}
	applyPath, _ := actionPath(host, action, "apply")
	var plan struct {
		PlanID       string  `json:"planId"`
		Confirmation *string `json:"confirmation"`
	}
	if err := a.do(ctx, http.MethodPost, planPath, map[string]any{"input": input}, &plan); err != nil {
		return Change{}, fmt.Errorf("plan %s: %w", action, err)
	}
	body := map[string]any{"planId": plan.PlanID}
	if plan.Confirmation != nil {
		body["confirmation"] = *plan.Confirmation
	}
	var ch Change
	if err := a.do(ctx, http.MethodPost, applyPath, body, &ch); err != nil {
		return Change{}, fmt.Errorf("apply %s: %w", action, err)
	}
	if ch.State != core.StateCompleted {
		if ch.Error != nil {
			return ch, fmt.Errorf("harness: change %s of %s is %s: %s: %s", ch.ID, action, ch.State, ch.Error.Code, ch.Error.Message)
		}
		return ch, fmt.Errorf("harness: change %s of %s is %s, want %s", ch.ID, action, ch.State, core.StateCompleted)
	}
	if out != nil {
		if err := json.Unmarshal(ch.Output, out); err != nil {
			return ch, fmt.Errorf("harness: output of %s: %w", action, err)
		}
	}
	return ch, nil
}

// Read runs the read action, written name/v<n>, with input on host, or on
// the controller when host is empty, and decodes its output into out unless
// out is nil.
func (a *API) Read(ctx context.Context, host, action string, input, out any) error {
	path, err := actionPath(host, action, "read")
	if err != nil {
		return err
	}
	var r struct {
		Output json.RawMessage `json:"output"`
	}
	if err := a.do(ctx, http.MethodPost, path, map[string]any{"input": input}, &r); err != nil {
		return fmt.Errorf("read %s: %w", action, err)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(r.Output, out); err != nil {
		return fmt.Errorf("harness: output of %s: %w", action, err)
	}
	return nil
}

// Resource reads the resource name, written module.resource, of host.
func (a *API) Resource(ctx context.Context, host, name string, out any) error {
	path, err := resourcePath(host, name)
	if err != nil {
		return err
	}
	if err := a.do(ctx, http.MethodGet, path, nil, out); err != nil {
		return fmt.Errorf("resource %s of %s: %w", name, host, err)
	}
	return nil
}

// Hosts returns the host list, the local host first.
func (a *API) Hosts(ctx context.Context) ([]core.Host, error) {
	var l core.HostList
	if err := a.Read(ctx, "", "core.host.list/v1", struct{}{}, &l); err != nil {
		return nil, err
	}
	return l.Hosts, nil
}

// findHost returns the host with id.
func findHost(hosts []core.Host, id string) (core.Host, bool) {
	for _, h := range hosts {
		if h.ID == id {
			return h, true
		}
	}
	return core.Host{}, false
}

// localHost returns the host that runs the controller.
func localHost(hosts []core.Host) (core.Host, bool) {
	for _, h := range hosts {
		if h.IsLocal {
			return h, true
		}
	}
	return core.Host{}, false
}

// actionPath is the route of op (plan, apply or read) of action, written
// name/v<n>: under /api/v1/hosts/<host> for an agent-side action, without a
// host for a controller-side one.
func actionPath(host, action, op string) (string, error) {
	name, version, ok := strings.Cut(action, "/")
	if !ok || name == "" {
		return "", fmt.Errorf("harness: action %q: want name/v<n>, such as core.host.list/v1", action)
	}
	if _, err := core.ParseVersion(version); err != nil {
		return "", fmt.Errorf("harness: action %q: %w", action, err)
	}
	path := "/api/v1/actions/" + name + "/" + version + "/" + op
	if host != "" {
		path = "/api/v1/hosts/" + url.PathEscape(host) + path[len("/api/v1"):]
	}
	return path, nil
}

// resourcePath is the route of the resource name, written module.resource,
// of host.
func resourcePath(host, name string) (string, error) {
	module, resource, ok := strings.Cut(name, ".")
	if !ok || module == "" || resource == "" {
		return "", fmt.Errorf("harness: resource %q: want module.resource, such as hostinfo.summary", name)
	}
	return "/api/v1/hosts/" + url.PathEscape(host) + "/resources/" + module + "/" + resource, nil
}

// maxProblemBody bounds how much of an answer without problem details an
// APIError quotes.
const maxProblemBody = 512

// do sends one request with body as JSON, unless body is nil, and decodes a
// 2xx answer into out, unless out is nil.
func (a *API) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("harness: %s %s: %w", method, path, err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, a.base+path, rd)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if method == http.MethodPost && a.csrf != "" {
		req.Header.Set(api.CSRFHeader, a.csrf)
	}
	resp, err := a.http.Do(req)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		e := &APIError{Status: resp.StatusCode}
		var p struct {
			Code   string `json:"code"`
			Detail string `json:"detail"`
		}
		if json.Unmarshal(data, &p) == nil && p.Code != "" {
			e.Code, e.Detail = p.Code, p.Detail
		} else {
			e.Detail = string(data[:min(len(data), maxProblemBody)])
		}
		return e
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("harness: %s %s: %w", method, path, err)
	}
	return nil
}
```

In `test/harness/m1_test.go`, replace:

```go
	// healthz asks the loopback listener inside the VM; --max-time ends a
	// request that connects and then hangs.
	healthz = "curl -fsS --max-time 10 http://127.0.0.1:8080/healthz"
	// testDeadline stays under the workflow's 45-minute go test timeout, so
	// the cleanups that stop the VMs still run.
	testDeadline = 40 * time.Minute
)
```

with:

```go
	// healthz asks the loopback listener inside the VM; --max-time ends a
	// request that connects and then hangs.
	healthz = "curl -fsS --max-time 10 http://127.0.0.1:8080/healthz"
	// testDeadline and TestM2's m2Deadline together stay under the
	// workflow's 100-minute go test timeout, so the cleanups that stop the
	// VMs still run.
	testDeadline = 40 * time.Minute
)
```

In `test/harness/m1_test.go`, replace:

```go

// bootPair boots the target of -os and the scanner on one multicast link.
func bootPair(ctx context.Context, t *testing.T) pair {
	t.Helper()
	pins, err := LoadPins(PinsFile)
	if err != nil {
```

with:

```go

// bootPair boots the target of -os and the scanner on one multicast link.
func bootPair(ctx context.Context, t *testing.T) pair {
	t.Helper()
	o, targetImg, scannerImg := harnessImages(ctx, t)
	dir := *workdirFlag
	if dir == "" {
		dir = t.TempDir()
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	// Each matrix job has a runner of its own; the random port keeps two runs
	// on one machine off each other's link.
	port := 20000 + rand.IntN(20000) //nolint:gosec // G404: a link port, not a secret
	t.Logf("%s: run directory %s, multicast link %s:%d", o.ID, dir, vm.McastGroup, port)
	vms := bootVMs(ctx, t, dir, []vm.Config{
		{
			Name: "target", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
			InetAddrs: []string{targetIPv4 + "/15", targetIPv6 + "/64"},
			McastPort: port, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "scanner", Dir: dir, Image: scannerImg, MemoryMiB: 1024, CPUs: 1,
			MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20",
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: port, Provision: ScannerScript(), Key: key,
		},
	})
	return pair{os: o, target: vms[0], scanner: vms[1]}
}

// harnessImages returns the OS of -os with its verified target image, and
// the scanner's verified image.
func harnessImages(ctx context.Context, t *testing.T) (OS, string, string) {
	t.Helper()
	pins, err := LoadPins(PinsFile)
	if err != nil {
```

In `test/harness/m1_test.go`, replace:

```go
		}
		cache = filepath.Join(d, "darkpanel-harness")
	}
	dir := *workdirFlag
	if dir == "" {
		dir = t.TempDir()
	}
	targetImg, err := vm.Fetch(ctx, o.Image.URL, o.Image.SHA256, cache)
	if err != nil {
		t.Fatal(err)
```

with:

```go
		}
		cache = filepath.Join(d, "darkpanel-harness")
	}
	targetImg, err := vm.Fetch(ctx, o.Image.URL, o.Image.SHA256, cache)
	if err != nil {
		t.Fatal(err)
```

In `test/harness/m1_test.go`, replace:

```go
	if err != nil {
		t.Fatal(err)
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	// Each matrix job has a runner of its own; the random port keeps two runs
	// on one machine off each other's link.
	port := 20000 + rand.IntN(20000) //nolint:gosec // G404: a link port, not a secret
	t.Logf("%s: run directory %s, multicast link %s:%d", o.ID, dir, vm.McastGroup, port)
	cfgs := []vm.Config{
		{
			Name: "target", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10",
			InetAddrs: []string{targetIPv4 + "/15", targetIPv6 + "/64"},
			McastPort: port, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "scanner", Dir: dir, Image: scannerImg, MemoryMiB: 1024, CPUs: 1,
			MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20",
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: port, Provision: ScannerScript(), Key: key,
		},
	}
	vms := make([]*vm.VM, len(cfgs))
	errs := make([]error, len(cfgs))
	var wg sync.WaitGroup
```

with:

```go
	if err != nil {
		t.Fatal(err)
	}
	return o, targetImg, scannerImg
}

// bootVMs boots cfgs in parallel and stops each VM when the test ends. Any
// failed boot fails the test, naming dir, where the logs stay.
func bootVMs(ctx context.Context, t *testing.T, dir string, cfgs []vm.Config) []*vm.VM {
	t.Helper()
	vms := make([]*vm.VM, len(cfgs))
	errs := make([]error, len(cfgs))
	var wg sync.WaitGroup
```

In `test/harness/m1_test.go`, replace:

```go
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("boot: %v\nlogs in %s", err, dir)
	}
	return pair{os: o, target: vms[0], scanner: vms[1]}
}

func checkOSRelease(ctx context.Context, t *testing.T, p pair) {
```

with:

```go
	if err := errors.Join(errs...); err != nil {
		t.Fatalf("boot: %v\nlogs in %s", err, dir)
	}
	return vms
}

func checkOSRelease(ctx context.Context, t *testing.T, p pair) {
```

Create `test/harness/m2_test.go` with this complete content:

```go
//go:build harness

package harness

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	mrand "math/rand/v2"
	"net"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/controller/auth"
	"github.com/darkraise/darkpanel/internal/controller/listen"
	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/release"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

var m2Flag = flag.String("m2", "", "directory of make harness-builds (darkpanel-<version> and install.sh); empty skips TestM2")

// The M2 fleet (spec section 13): the controller has M1's target addresses,
// the agent the next ones, and both have a private link that stands in for
// the tailnet.
const (
	agentIPv4      = "198.18.0.11"
	agentIPv6      = "2001:db8:d0::11"
	controllerPriv = "10.77.0.10"
	agentPriv      = "10.77.0.11"
)

// The harness releases: N, then one minor up a good build, a build
// that fails its health check, one that crashes at start, a good build for
// the reboot and the frozen runner, and one that also adds a migration.
const (
	versionN         = "0.2.0"
	versionGood      = "0.3.0"
	versionBroken    = "0.3.1"
	versionCrash     = "0.3.2"
	versionReboot    = "0.3.3"
	versionMigration = "0.3.4"
	releaseChannel   = "stable"
)

var m2Versions = []string{versionN, versionGood, versionBroken, versionCrash, versionReboot, versionMigration}

const (
	// harnessKey is the committed key the harnesskeys builds trust.
	harnessKey = "../../internal/update/trustedkeys/testdata/harness.key"
	// releaseURL is the release base URL a harnesskeys build embeds: the
	// scanner, which serves releaseDir with releaseUnit.
	releaseURL  = "http://" + scannerIPv4 + ":8000"
	releaseDir  = "/srv/harness-releases"
	releaseUnit = "harness-releases"
	// installScript is where the targets keep the harness's install.sh,
	// outside every path uninstall removes.
	installScript = "/var/tmp/install.sh"
	// enrollFile holds an enrollment token on the agent while it is used.
	enrollFile = "/root/enroll-token"
	adminName  = "admin"
	// apiURL is the controller's loopback listener, which the harness reaches
	// through the controller VM's SSH connection.
	apiURL = "http://127.0.0.1:8080"
	// m2Deadline and TestM1's testDeadline together stay under the workflow's
	// 100-minute go test timeout, so the cleanups that stop the VMs still
	// run.
	m2Deadline = 55 * time.Minute
)

// agentAddress is where agents dial the controller: its private-link address.
var agentAddress = net.JoinHostPort(controllerPriv, strconv.Itoa(listen.AgentPort))

// fleet is one OS's M2 run: the VMs, the API session, and the hosts and
// admin the scenarios create.
type fleet struct {
	os                         OS
	controller, agent, scanner *vm.VM
	api                        *API
	localID, agentID           string
	password, totpSecret       string
	// lastCode is when the newest TOTP code was used. The controller never
	// accepts a step twice, so the next code must come from a later step.
	lastCode time.Time
}

func TestM2(t *testing.T) {
	if *osFlag == "" || *m2Flag == "" {
		t.Skip("no -os or -m2 given: the VM tests run in .github/workflows/harness.yml")
	}
	ctx, cancel := context.WithTimeout(context.Background(), m2Deadline)
	defer cancel()
	f := bootFleet(ctx, t)
	serveReleases(ctx, t, f)
	steps := []struct {
		name string
		gate bool // later steps need this one
		run  func(t *testing.T)
	}{
		{"install", true, func(t *testing.T) { scenarioInstall(ctx, t, f) }},
		{"self-management", true, func(t *testing.T) { scenarioSelfManagement(ctx, t, f) }},
		{"offline", false, func(t *testing.T) { scenarioOffline(ctx, t, f) }},
		{"revoke", true, func(t *testing.T) { scenarioRevoke(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
			return
		}
	}
}

// bootFleet boots the controller, the agent and the scanner of -os. All
// three share the internet link; the controller and the agent also share
// the private link.
func bootFleet(ctx context.Context, t *testing.T) *fleet {
	t.Helper()
	o, targetImg, scannerImg := harnessImages(ctx, t)
	dir := t.TempDir()
	if *workdirFlag != "" {
		// TestM1's VMs used the run directory first; a directory of its own
		// keeps their logs.
		dir = filepath.Join(*workdirFlag, "m2")
	}
	key, err := vm.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	port := 20000 + mrand.IntN(20000) //nolint:gosec // G404: a link port, not a secret
	privPort := port + 1
	t.Logf("%s: run directory %s, internet link %s:%d, private link %s:%d", o.ID, dir, vm.McastGroup, port, vm.McastGroup, privPort)
	vms := bootVMs(ctx, t, dir, []vm.Config{
		{
			Name: "controller", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:10", InetMAC: "52:54:00:d0:00:10", PrivMAC: "52:54:00:d0:02:10",
			InetAddrs: []string{targetIPv4 + "/15", targetIPv6 + "/64"}, PrivAddrs: []string{controllerPriv + "/24"},
			McastPort: port, PrivMcastPort: privPort, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "agent", Dir: dir, Image: targetImg, MemoryMiB: 2048, CPUs: 2,
			MgmtMAC: "52:54:00:d0:01:11", InetMAC: "52:54:00:d0:00:11", PrivMAC: "52:54:00:d0:02:11",
			InetAddrs: []string{agentIPv4 + "/15", agentIPv6 + "/64"}, PrivAddrs: []string{agentPriv + "/24"},
			McastPort: port, PrivMcastPort: privPort, Provision: TargetScript(o), Key: key,
		},
		{
			Name: "scanner", Dir: dir, Image: scannerImg, MemoryMiB: 2048, CPUs: 1,
			MgmtMAC: "52:54:00:d0:01:20", InetMAC: "52:54:00:d0:00:20",
			InetAddrs: []string{scannerIPv4 + "/15", scannerIPv6 + "/64"},
			McastPort: port, Provision: ScannerScript(), Key: key,
		},
	})
	return &fleet{os: o, controller: vms[0], agent: vms[1], scanner: vms[2]}
}

// serveReleases signs the builds of -m2 as the harness releases, copies them
// to the scanner and serves them there as the release base URL.
func serveReleases(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	key, err := release.LoadKey(harnessKey)
	if err != nil {
		t.Fatal(err)
	}
	versions := make([]release.Version, 0, len(m2Versions))
	for _, v := range m2Versions {
		versions = append(versions, release.Version{Version: v, Channel: releaseChannel, Binary: filepath.Join(*m2Flag, "darkpanel-"+v)})
	}
	dir := t.TempDir()
	if err := release.Publish(dir, key, versions); err != nil {
		t.Fatal(err)
	}
	if err := copyTree(ctx, f.scanner, dir, releaseDir); err != nil {
		t.Fatal(err)
	}
	cmd := fmt.Sprintf("sudo systemd-run --unit=%s /usr/bin/python3 -m http.server 8000 --bind %s --directory %s",
		releaseUnit, scannerIPv4, releaseDir)
	if _, err := f.scanner.Run(ctx, cmd); err != nil {
		t.Fatal(err)
	}
	err = eventually(ctx, time.Minute, func(ctx context.Context) error {
		_, err := f.controller.Run(ctx, "curl -fsS --max-time 10 -o /dev/null "+releaseURL+"/index/releases.json")
		return err
	})
	if err != nil {
		journal, _ := f.scanner.Run(ctx, "sudo journalctl -u "+releaseUnit+" --no-pager -n 30")
		t.Fatalf("the release server does not answer: %v\n%s journal:\n%s", err, releaseUnit, journal)
	}
}

// copyTree copies the directory src to dst on v as root, files 0644.
func copyTree(ctx context.Context, v *vm.VM, src, dst string) error {
	return filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, p)
		if err != nil {
			return err
		}
		target := path.Join(dst, filepath.ToSlash(rel))
		if d.IsDir() {
			_, err := v.Run(ctx, "sudo mkdir -p "+target)
			return err
		}
		return v.CopyFile(ctx, p, target, 0o644)
	})
}

// scenarioInstall is scenario 1: install.sh installs N as the controller and
// as an agent enrolled with a token the API created, and both report N. The
// controller runs as the darkpanel user, its agent as root.
func scenarioInstall(ctx context.Context, t *testing.T, f *fleet) {
	for _, v := range []*vm.VM{f.controller, f.agent} {
		if err := v.CopyFile(ctx, filepath.Join(*m2Flag, "install.sh"), installScript, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	installRole(ctx, t, f.controller, versionN, "--role controller --agent-listen "+agentAddress)
	for unit, user := range map[string]string{update.ControllerUnit: install.ServiceUser, update.AgentUnit: "root"} {
		out, err := f.controller.Run(ctx, `stat -c %U "/proc/$(systemctl show -p MainPID --value `+unit+`)"`)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(out); got != user {
			t.Errorf("%s runs as %q, want %q", unit, got, user)
		}
	}
	createAdmin(ctx, t, f)
	login(ctx, t, f)
	hosts := waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		h, ok := localHost(hosts)
		if !ok {
			return errors.New("no local host listed")
		}
		return hostsAt(versionN, h.ID)(hosts)
	})
	local, _ := localHost(hosts)
	f.localID = local.ID

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.create/v1", core.HostCreateInput{Name: "agent", ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	f.agentID = grant.Host.ID
	writeToken(ctx, t, f.agent, grant.Token)
	installRole(ctx, t, f.agent, versionN, "--role agent --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), versionN)
	}
}

// installRole runs install.sh for version with args, quoting the services'
// journals when it fails.
func installRole(ctx context.Context, t *testing.T, v *vm.VM, version, args string) {
	t.Helper()
	cmd := "sudo sh " + installScript + " --version " + version + " --base-url " + releaseURL + " " + args
	if _, err := v.Run(ctx, cmd); err != nil {
		journal, _ := v.Run(ctx, "sudo journalctl -u "+update.ControllerUnit+" -u "+update.AgentUnit+" --no-pager -n 80")
		t.Fatalf("%v\njournal:\n%s", err, journal)
	}
}

// createAdmin creates the first admin over the controller socket, as the
// README's first-admin section does, and activates it. Without a terminal
// the CLI reads the password, and then the code, from standard input; the
// code depends on the secret it prints, so the first run gets the password
// alone, prints the secret and stops at the missing code, naming user
// activate, which the second run is.
func createAdmin(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	f.password = rand.Text()
	out, err := f.controller.RunInput(ctx, "sudo darkpanel user create --username "+adminName+" --yes", strings.NewReader(f.password+"\n"))
	if err == nil || !strings.Contains(err.Error(), "no code given") {
		t.Fatalf("user create = %v, want it to stop at the missing code\n%s", err, out)
	}
	if f.totpSecret, err = totpSecret(out); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	code := nextCode(ctx, t, f)
	if out, err := f.controller.RunInput(ctx, "sudo darkpanel user activate --username "+adminName+" --yes", strings.NewReader(code+"\n")); err != nil {
		t.Fatalf("user activate: %v\n%s", err, out)
	}
}

// login opens a new API session as the admin.
func login(ctx context.Context, t *testing.T, f *fleet) {
	t.Helper()
	f.api = NewAPI(apiURL, f.controller.DialContext)
	if err := f.api.Login(ctx, adminName, f.password, nextCode(ctx, t, f)); err != nil {
		t.Fatal(err)
	}
}

// nextCode waits for a TOTP step after the last code used and returns that
// step's code.
func nextCode(ctx context.Context, t *testing.T, f *fleet) string {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	case <-time.After(time.Until(nextStep(f.lastCode))):
	}
	f.lastCode = time.Now()
	code, err := auth.TOTPCode(f.totpSecret, f.lastCode)
	if err != nil {
		t.Fatal(err)
	}
	return code
}

// nextStep is the start of the TOTP step after the one at t.
func nextStep(t time.Time) time.Time {
	period := int64(auth.TOTPPeriod / time.Second)
	return time.Unix((t.Unix()/period+1)*period, 0)
}

// totpSecret returns the secret user create prints on its line "Secret: ".
func totpSecret(out string) (string, error) {
	for line := range strings.SplitSeq(out, "\n") {
		if s, ok := strings.CutPrefix(strings.TrimSpace(line), "Secret: "); ok && s != "" {
			return s, nil
		}
	}
	return "", errors.New("harness: user create printed no Secret: line")
}

// writeToken writes an enrollment token to enrollFile on v, owned by root
// with mode 0600, as agent enroll and install.sh require. It goes through
// standard input, so no command line or error quotes it.
func writeToken(ctx context.Context, t *testing.T, v *vm.VM, token string) {
	t.Helper()
	if _, err := v.RunInput(ctx, "sudo sh -c 'umask 077 && cat > "+enrollFile+"'", strings.NewReader(token)); err != nil {
		t.Fatal(err)
	}
}

func removeToken(ctx context.Context, t *testing.T, v *vm.VM) {
	t.Helper()
	if _, err := v.Run(ctx, "sudo rm -f "+enrollFile); err != nil {
		t.Fatal(err)
	}
}

// scenarioSelfManagement is scenario 2: hostinfo.summary of the controller's
// own host and of the agent host through the API, both over mTLS; the host
// list shows both online with versions. Only the controller listens beyond
// loopback, on its private address alone, and the scans find only 22/tcp on
// both hosts.
func scenarioSelfManagement(ctx context.Context, t *testing.T, f *fleet) {
	hosts := waitHosts(ctx, t, f, time.Minute, hostsAt(versionN, f.localID, f.agentID))
	for _, h := range hosts {
		if h.LastSeenAt == nil {
			t.Errorf("host %s shows no last-seen time", h.Name)
		}
	}
	for id, name := range map[string]string{f.localID: "controller", f.agentID: "agent"} {
		var sum struct {
			Hostname string `json:"hostname"`
		}
		if err := f.api.Resource(ctx, id, "hostinfo.summary", &sum); err != nil {
			t.Error(err)
		} else if sum.Hostname != name {
			t.Errorf("hostinfo.summary of host %s names %q, want %q", id, sum.Hostname, name)
		}
	}
	for _, c := range []struct {
		v    *vm.VM
		want []string // darkpanel's listeners beyond loopback
	}{{f.controller, []string{agentAddress}}, {f.agent, nil}} {
		out, err := c.v.Run(ctx, "sudo ss -Htlnp")
		if err != nil {
			t.Fatal(err)
		}
		if _, public := darkpanelListeners(out); !slices.Equal(public, c.want) {
			t.Errorf("darkpanel listens beyond loopback on %v, want %v\n%s", public, c.want, out)
		}
	}
	for _, s := range []struct{ name, family, addr string }{
		{"scan-controller-ipv4", "", targetIPv4}, {"scan-controller-ipv6", "-6", targetIPv6},
		{"scan-agent-ipv4", "", agentIPv4}, {"scan-agent-ipv6", "-6", agentIPv6},
	} {
		t.Run(s.name, func(t *testing.T) { checkScan(ctx, t, f.scanner, s.family, s.addr) })
	}
}

// scenarioOffline is scenario 3: with its service stopped the agent host is
// offline within 60 s and an action on it fails at once with host.offline;
// started again, it returns.
func scenarioOffline(ctx context.Context, t *testing.T, f *fleet) {
	if _, err := f.agent.Run(ctx, "sudo systemctl stop "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, time.Minute, func(hosts []core.Host) error {
		if h, ok := findHost(hosts, f.agentID); !ok || h.Online {
			return fmt.Errorf("host %s is listed %v, online %v", f.agentID, ok, h.Online)
		}
		return nil
	})
	start := time.Now()
	err := f.api.Read(ctx, f.agentID, "core.update.status/v1", core.UpdateStatusInput{}, nil)
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != string(errcode.HostOffline) {
		t.Errorf("an action on the offline host = %v, want %s", err, errcode.HostOffline)
	}
	if elapsed := time.Since(start); elapsed > 10*time.Second {
		t.Errorf("the action on the offline host took %v; it must fail at once", elapsed)
	}
	if _, err := f.agent.Run(ctx, "sudo systemctl start "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.agentID))
}

// scenarioRevoke is scenario 4: core.host.revoke closes the agent's live
// session and its redials are refused, so it stays offline while its
// journal shows failed sessions; core.host.token and agent enroll re-enroll
// it.
func scenarioRevoke(ctx context.Context, t *testing.T, f *fleet) {
	waitHosts(ctx, t, f, 2*time.Minute, hostsAt(versionN, f.agentID))
	since := vmNow(ctx, t, f.agent)
	var h core.Host
	if _, err := f.api.Apply(ctx, "", "core.host.revoke/v1", core.HostIDInput{HostID: f.agentID}, &h); err != nil {
		t.Fatal(err)
	}
	if h.State != core.HostRevoked {
		t.Errorf("core.host.revoke answered state %q, want %q", h.State, core.HostRevoked)
	}
	waitHosts(ctx, t, f, 30*time.Second, func(hosts []core.Host) error {
		if h, ok := findHost(hosts, f.agentID); !ok || h.Online || h.State != core.HostRevoked {
			return fmt.Errorf("host %s: %+v", f.agentID, h)
		}
		return nil
	})
	// The first failed session is the one revoke closed; every later one is a
	// refused redial.
	err := eventually(ctx, 2*time.Minute, func(ctx context.Context) error {
		out, err := f.agent.Run(ctx, fmt.Sprintf("sudo journalctl -u %s --since @%d --no-pager -o cat", update.AgentUnit, since))
		if err != nil {
			return err
		}
		if n := strings.Count(out, "daemon: controller session"); n < 2 {
			return fmt.Errorf("%d failed sessions in the agent's journal since the revoke, want 2 or more:\n%s", n, out)
		}
		return nil
	})
	if err != nil {
		t.Error(err)
	}
	hosts, err := f.api.Hosts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if h, _ := findHost(hosts, f.agentID); h.Online {
		t.Errorf("the revoked host %s came back online", f.agentID)
	}

	var grant core.EnrollmentGrant
	if _, err := f.api.Apply(ctx, "", "core.host.token/v1", core.HostTokenInput{HostID: f.agentID, ControllerAddress: agentAddress}, &grant); err != nil {
		t.Fatal(err)
	}
	writeToken(ctx, t, f.agent, grant.Token)
	out, err := f.agent.Run(ctx, "sudo darkpanel agent enroll --token-file "+enrollFile)
	removeToken(ctx, t, f.agent)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "enrolled as host "+f.agentID) {
		t.Errorf("agent enroll printed %q, want it enrolled as host %s", out, f.agentID)
	}
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(versionN, f.agentID))
}

// vmNow is v's clock in Unix seconds, for journalctl --since.
func vmNow(ctx context.Context, t *testing.T, v *vm.VM) int64 {
	t.Helper()
	out, err := v.Run(ctx, "date +%s")
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.ParseInt(strings.TrimSpace(out), 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// waitHosts polls the host list until check accepts it, for up to timeout,
// and returns the list it accepted. An API error, such as that of a
// controller restarting, counts as not yet.
func waitHosts(ctx context.Context, t *testing.T, f *fleet, timeout time.Duration, check func([]core.Host) error) []core.Host {
	t.Helper()
	var hosts []core.Host
	err := eventually(ctx, timeout, func(ctx context.Context) error {
		var err error
		if hosts, err = f.api.Hosts(ctx); err != nil {
			return err
		}
		return check(hosts)
	})
	if err != nil {
		t.Fatalf("host list: %v", err)
	}
	return hosts
}

// hostsAt accepts a host list in which every host of ids is enrolled,
// online, and runs version.
func hostsAt(version string, ids ...string) func([]core.Host) error {
	return func(hosts []core.Host) error {
		for _, id := range ids {
			h, ok := findHost(hosts, id)
			switch {
			case !ok:
				return fmt.Errorf("host %s is not listed", id)
			case h.State != core.HostEnrolled || !h.Online:
				return fmt.Errorf("host %s (%s) is %s and online %v, want enrolled and online", h.Name, id, h.State, h.Online)
			case h.AgentVersion == nil || *h.AgentVersion != version:
				got := "no version"
				if h.AgentVersion != nil {
					got = *h.AgentVersion
				}
				return fmt.Errorf("host %s (%s) runs %s, want %s", h.Name, id, got, version)
			}
		}
		return nil
	}
}

// requireBinaryVersion checks the version the binary at path reports.
func requireBinaryVersion(ctx context.Context, t *testing.T, v *vm.VM, path, version string) {
	t.Helper()
	out, err := v.Run(ctx, path+" version --json")
	if err != nil {
		t.Fatal(err)
	}
	var ver struct {
		Version string `json:"version"`
	}
	if err := json.Unmarshal([]byte(out), &ver); err != nil {
		t.Fatalf("%s version --json: %v\n%s", path, err, out)
	}
	if ver.Version != version {
		t.Errorf("%s reports version %q, want %q", path, ver.Version, version)
	}
}

func TestTOTPSecret(t *testing.T) {
	out := "Change 01HZY: completed\nScan this QR code with an authenticator app, or enter the secret by hand:\n" +
		"█▀▀▀▀▀█\nURI: otpauth://totp/Darkpanel:admin?secret=JBSWY3DPEHPK3PXP&issuer=Darkpanel\n" +
		"Secret: JBSWY3DPEHPK3PXP\nThe secret will not be shown again.\n"
	if got, err := totpSecret(out); err != nil || got != "JBSWY3DPEHPK3PXP" {
		t.Errorf("totpSecret = %q, %v", got, err)
	}
	for _, bad := range []string{"", "Change 01HZY: completed\n", "Secret: \n"} {
		if got, err := totpSecret(bad); err == nil {
			t.Errorf("totpSecret(%q) = %q, want an error", bad, got)
		}
	}
}

func TestNextStep(t *testing.T) {
	cases := []struct{ at, want int64 }{{0, 30}, {29, 30}, {30, 60}, {1_700_000_015, 1_700_000_040}}
	for _, c := range cases {
		if got := nextStep(time.Unix(c.at, 500)); !got.Equal(time.Unix(c.want, 0)) {
			t.Errorf("nextStep(%d) = %d, want %d", c.at, got.Unix(), c.want)
		}
	}
	// A code of the next step differs from the last one's, which the
	// controller would refuse as a replay.
	at := time.Unix(1_700_000_015, 0)
	last, _ := auth.TOTPCode("JBSWY3DPEHPK3PXP", at)
	next, _ := auth.TOTPCode("JBSWY3DPEHPK3PXP", nextStep(at))
	if last == next {
		t.Errorf("the codes of two steps are both %s", last)
	}
}

func TestHostsAt(t *testing.T) {
	v := func(s string) *string { return &s }
	hosts := []core.Host{
		{ID: "h0", Name: "controller", IsLocal: true, State: core.HostEnrolled, Online: true, AgentVersion: v("0.2.0")},
		{ID: "h1", Name: "agent", State: core.HostEnrolled, Online: true, AgentVersion: v("0.3.0")},
		{ID: "h2", Name: "old", State: core.HostRevoked, Online: false, AgentVersion: v("0.2.0")},
		{ID: "h3", Name: "new", State: core.HostPending},
		{ID: "h5", Name: "stopped", State: core.HostEnrolled, Online: false, AgentVersion: v("0.2.0")},
	}
	cases := []struct {
		version string
		ids     []string
		want    string // "" accepts
	}{
		{"0.2.0", []string{"h0"}, ""},
		{"0.3.0", []string{"h1"}, ""},
		{"0.2.0", []string{"h0", "h1"}, "host agent (h1) runs 0.3.0, want 0.2.0"},
		{"0.2.0", []string{"h2"}, "host old (h2) is revoked and online false, want enrolled and online"},
		{"0.2.0", []string{"h3"}, "host new (h3) is pending and online false, want enrolled and online"},
		{"0.2.0", []string{"h5"}, "host stopped (h5) is enrolled and online false, want enrolled and online"},
		{"0.2.0", []string{"h9"}, "host h9 is not listed"},
	}
	for _, c := range cases {
		err := hostsAt(c.version, c.ids...)(hosts)
		if got := fmt.Sprint(err); (c.want == "" && err != nil) || (c.want != "" && got != c.want) {
			t.Errorf("hostsAt(%s, %v) = %v, want %q", c.version, c.ids, err, c.want)
		}
	}
	h := core.Host{ID: "h4", Name: "fresh", State: core.HostEnrolled, Online: true}
	if err := hostsAt("0.2.0", "h4")([]core.Host{h}); fmt.Sprint(err) != "host fresh (h4) runs no version, want 0.2.0" {
		t.Errorf("hostsAt of a host without a version = %v", err)
	}
}
```

In `.github/workflows/harness.yml`, replace:

```yaml
          go test -tags harness -timeout 100m -v ./test/harness/
          -args -os ${{ matrix.os }}
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness"
          -cache "$HOME/.cache/darkpanel-harness"
```

with:

```yaml
          go test -tags harness -timeout 100m -v ./test/harness/
          -args -os ${{ matrix.os }}
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness"
          -cache "$HOME/.cache/darkpanel-harness"
```

In `.github/workflows/harness.yml`, replace:

```yaml
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-${{ matrix.os }}
          path: ${{ runner.temp }}/harness/*.log
          retention-days: 7
          if-no-files-found: ignore
```

with:

```yaml
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-${{ matrix.os }}
          path: ${{ runner.temp }}/harness/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness`, `test/harness/release` and `test/harness/vm`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 go mod tidy -diff`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/20 timeout 300 go test -tags harness -count=1 -run '^TestM[12]$' -v ./test/harness/ -args -m2 bin/harness`
Expected: `--- SKIP: TestM1` (`no -os given`) and `--- SKIP: TestM2` (`no -os or -m2 given`), then `ok`

- [ ] **Step 5: Commit**

```bash
git add test/harness/api.go test/harness/api_test.go test/harness/m1_test.go test/harness/m2_test.go test/harness/vm/vm.go test/harness/vm/vm_test.go .github/workflows/harness.yml
git commit -m "test(harness): add the M2 fleet and its scenarios"
```

---

### Task 21: Update scenarios

**Files:**
- Create: `test/harness/m2_update_test.go`
- Modify: `test/harness/m2_test.go`
- Test: `test/harness/m2_update_test.go`

**Interfaces:**
- Consumes: Task 20 (`fleet`, `versionN`, `versionGood`, `versionBroken`, `versionCrash`, `versionReboot`, `versionMigration`, `releaseChannel`, `waitHosts`, `hostsAt`, `requireBinaryVersion`, `(*API).Apply`, `(*API).Read`, `(*API).Resource`, `(*API).Hosts`, `(*vm.VM).RunInput`, the step list of `TestM2`); from M1: `eventually`, `pollInterval`, `unitState`; `update.Record` (fields `Deadline`, `UpdatedAt`), `update.WriteRecord`, `update.RunnerUnit`, `update.HealthDeadline`, `update.ControllerUnit`, `update.AgentUnit`; `paths.Production` (`AgentDB`, `ControllerDB`, `ControllerBackups`, `UpdateDir`, `UpdateRecord`, `Binary`, `FailedBinary`), `paths.Under`; `core.Rollout`, `core.RolloutHost`, `core.RolloutList`, `core.RolloutInput`, `core.RolloutsInput`, `core.UpdateStatus`, `core.UpdateStatusInput`, `core.UpdateTransaction`, the `core.Rollout*` and `core.Update*` state constants, `core.UpdateKindUpdate`.
- Produces: package `harness`, file `m2_update_test.go`: constants `updateBudget`, `rolloutTimeout`; functions `scenarioUpdateGood`, `oneMinorBehind`, `scenarioHealthFailure`, `scenarioCrashLoop`, `scenarioRebootInWindow`, `scenarioRunnerFrozen`, `scenarioMigrationRollback`, `failedUpdate`, `requireRolledBackFor`, `startRollout`, `followRollout`, `readRollout`, `rolloutHost`, `activeRollout`, `requireSettled`, `requireFleetVersion`, `lastRecord`, `newestTransaction`, `recordTook`, `requireNoLocks`, `sqliteCount`, `listDir`, `newEntries`, `watchSwap`, `swapWatch`; tests `TestSwapWatch`, `TestRolloutHelpers`. TestM2 gains the steps `update-good` (gate), `health-failure`, `crash-loop`, `reboot-in-window`, `runner-frozen`, `migration-rollback`.

**Items:** 12, 13, 15
**Depends on:** Task 20
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 2 - risk 0 = 3

Notes for the implementer:
- Every rollout lists the controller's host first, then the agent host. A rollout that fails at the controller's host stops there, so in scenarios 6 to 10 the agent host stays on the good build, `skipped`, with no new update record.
- Each failure scenario starts with `requireSettled` (no active rollout, both hosts online at the good build, up to three minutes), so one failed scenario does not start the next on a fleet still updating.
- Timing uses the hosts' own clocks: a record's `updatedAt` minus its `startedAt`, or, for the frozen runner, the record file's `deadline` minus `update.HealthDeadline` (the swap). The rolled-back record's error message names the path that rolled it back: `with status "degraded"` (the runner's health check, scenarios 6 and 10), `a service or the runner failed before the update was committed` (OnFailure=, scenario 7), `the services did not report healthy before the deadline` (the deadline timer, scenario 9); scenario 8 only logs it.
- `swapWatch` runs on the VM as root (`sudo sh -s`, the script on standard input). Records are written with `json.MarshalIndent`, so the state reads `"state": "swapped"`. The runner records `swapped` before it renames the new binary into place, so the watch also waits for the binary's SHA-256 to equal the record's `newSha256`: the reboot (scenario 8) and the freeze (scenario 9) then fall inside the health window, and `darkpanel.failed` is the new build.
- `core_locks` is read from `/var/lib/darkpanel/agent/agent.db` with `sudo sqlite3`; the rollback releases the reservation just after recording its state, so `requireNoLocks` retries for 30 s.

- [ ] **Step 1: Write the failing test**

Create `test/harness/m2_update_test.go` with the unit tests of its helpers:

```go
//go:build harness

package harness

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
)

func TestSwapWatch(t *testing.T) {
	root := t.TempDir()
	p := paths.Under(root)
	if err := os.MkdirAll(p.UpdateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "darkpanel")
	if err := os.WriteFile(binary, []byte("the new binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("the new binary"))
	record := func(id, state, newSHA string) {
		t.Helper()
		r := update.Record{ID: id, OperationID: id, Kind: core.UpdateKindUpdate, FromVersion: "0.3.0", ToVersion: "0.3.3",
			TargetVersion: "0.3.3", State: state, NewSHA256: newSHA}
		if err := update.WriteRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(then string) (string, error) {
		script := swapWatch(p.UpdateDir, binary, then, time.Second)
		out, err := exec.Command("sh", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		return string(out), err
	}

	record("01OLDCOMMITTED", core.UpdateCommitted, hex.EncodeToString(sum[:]))
	if out, err := watch(""); err == nil {
		t.Errorf("swapWatch over committed records only = %q, want a timeout", out)
	}
	record("01SWAPPEDOTHER", core.UpdateSwapped, strings.Repeat("0", 64))
	if out, err := watch(""); err == nil || !strings.Contains(out, "no update record reached swapped with its new binary in place") {
		t.Errorf("swapWatch while the binary is not yet the new one = %q, %v; want a timeout", out, err)
	}
	record("01SWAPPEDOTHER", core.UpdateRolledBack, strings.Repeat("0", 64))
	record("01SWAPPEDNEW", core.UpdateSwapped, hex.EncodeToString(sum[:]))
	thenFile := filepath.Join(root, "then")
	out, err := watch(`echo "frozen $tx" > ` + thenFile)
	if err != nil || out != "01SWAPPEDNEW\n" {
		t.Errorf("swapWatch = %q, %v; want the swapped transaction", out, err)
	}
	if got, err := os.ReadFile(thenFile); err != nil || string(got) != "frozen 01SWAPPEDNEW\n" { //nolint:gosec // G304: the test's own file
		t.Errorf("then ran with %q, %v; want $tx set to the swapped transaction", got, err)
	}
	if script := swapWatch(p.UpdateDir, binary, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"), time.Minute); !strings.Contains(script,
		"systemctl kill --signal=STOP darkpanel-update-$tx.service") {
		t.Errorf("swapWatch does not run its command with the runner unit of $tx:\n%s", script)
	}
}

func TestRolloutHelpers(t *testing.T) {
	ro := core.Rollout{ID: "r1", State: core.RolloutDraining, Hosts: []core.RolloutHost{
		{HostID: "h0", Position: 0, State: core.RolloutHostFailed},
		{HostID: "h1", Position: 1, State: core.RolloutHostSkipped},
	}}
	if h, ok := rolloutHost(ro, "h1"); !ok || h.State != core.RolloutHostSkipped {
		t.Errorf("rolloutHost(h1) = %+v, %v", h, ok)
	}
	if _, ok := rolloutHost(ro, "h9"); ok {
		t.Error("rolloutHost found a host the rollout does not list")
	}
	for state, want := range map[string]bool{
		core.RolloutRunning: true, core.RolloutDraining: true,
		core.RolloutSucceeded: false, core.RolloutFailed: false, core.RolloutCancelled: false,
	} {
		if got := activeRollout(state); got != want {
			t.Errorf("activeRollout(%s) = %v, want %v", state, got, want)
		}
	}
	before := []string{"0.2.0-0.3.0-20261020T120000Z", "none-0.2.0-20261020T110000Z"}
	after := []string{"0.2.0-0.3.0-20261020T120000Z", "0.3.0-0.3.4-20261020T130000Z", "0.3.0-0.3.1-20261020T125000Z"}
	if got := newEntries(before, after, "0.3.0-0.3.4-"); !slices.Equal(got, []string{"0.3.0-0.3.4-20261020T130000Z"}) {
		t.Errorf("newEntries = %v", got)
	}
	if got := newEntries(after, after, "0.3.0-0.3.4-"); len(got) != 0 {
		t.Errorf("newEntries of an unchanged directory = %v", got)
	}
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rec := core.UpdateTransaction{StartedAt: start, UpdatedAt: start.Add(95 * time.Second)}
	if got := recordTook(rec); got != 95*time.Second {
		t.Errorf("recordTook = %v, want 1m35s", got)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 timeout 600 go test -tags harness -count=1 -run 'TestSwapWatch|TestRolloutHelpers' ./test/harness/`
Expected: FAIL with `test/harness/m2_update_test.go:41:13: undefined: swapWatch`

- [ ] **Step 3: Write the implementation**

Replace the complete content of `test/harness/m2_update_test.go` with:

```go
//go:build harness

package harness

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// updateBudget is S5's bound: a host's update commits, or rolls back, within
// two minutes.
const updateBudget = 2 * time.Minute

// rolloutTimeout bounds one rollout of both hosts: each update's budget, the
// driver's 5 s polls and the controller's own restart.
const rolloutTimeout = 8 * time.Minute

// scenarioUpdateGood is scenario 5: a rollout to the good build updates the
// controller's host, then the agent host, each within two minutes, and
// hostinfo.summary on the agent host works while it is one minor behind
// (S5). No reservation is left.
func scenarioUpdateGood(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, versionN)
	ro := startRollout(ctx, t, f, versionGood)
	var behindErr error
	behind := false
	// The agent host is one minor behind only from the controller host's
	// commit to the agent's restart, a few seconds, so the rollout is
	// polled faster than pollInterval.
	ro = followRollout(ctx, t, f, ro.ID, 250*time.Millisecond, func(cur core.Rollout) {
		ctl, _ := rolloutHost(cur, f.localID)
		ag, _ := rolloutHost(cur, f.agentID)
		if !behind && ctl.State == core.RolloutHostCommitted && ag.State != core.RolloutHostCommitted {
			if behindErr = oneMinorBehind(ctx, f, versionN, versionGood); behindErr == nil {
				behind = true
			}
		}
	})
	if ro.State != core.RolloutSucceeded {
		t.Fatalf("rollout %s to %s is %s (%v), want %s", ro.ID, versionGood, ro.State, ro.Error, core.RolloutSucceeded)
	}
	if !behind {
		t.Errorf("hostinfo.summary never answered on the agent host while it ran %s and the controller %s; last error: %v", versionN, versionGood, behindErr)
	}
	for _, id := range []string{f.localID, f.agentID} {
		h, _ := rolloutHost(ro, id)
		if h.State != core.RolloutHostCommitted || h.OperationID == nil {
			t.Errorf("rollout host %s is %s, want %s with an operation", id, h.State, core.RolloutHostCommitted)
			continue
		}
		rec := lastRecord(ctx, t, f, id, *h.OperationID)
		if rec.State != core.UpdateCommitted || rec.ToVersion != versionGood {
			t.Errorf("host %s's update ended %s at %s, want %s at %s", id, rec.State, rec.ToVersion, core.UpdateCommitted, versionGood)
		}
		if took := recordTook(rec); took > updateBudget {
			t.Errorf("host %s's update took %v, over %v", id, took, updateBudget)
		}
	}
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireNoLocks(ctx, t, v)
	}
	requireFleetVersion(ctx, t, f, versionGood)
}

// oneMinorBehind reads hostinfo.summary of the agent host, provided the host
// list shows it online at agentVersion and the controller's host at
// controllerVersion.
func oneMinorBehind(ctx context.Context, f *fleet, agentVersion, controllerVersion string) error {
	hosts, err := f.api.Hosts(ctx)
	if err != nil {
		return err
	}
	if err := hostsAt(controllerVersion, f.localID)(hosts); err != nil {
		return err
	}
	if err := hostsAt(agentVersion, f.agentID)(hosts); err != nil {
		return err
	}
	var sum struct {
		Hostname string `json:"hostname"`
	}
	return f.api.Resource(ctx, f.agentID, "hostinfo.summary", &sum)
}

// scenarioHealthFailure is scenario 6: the controller of the broken build
// reports degraded, so its host rolls back at once, within two minutes.
func scenarioHealthFailure(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionBroken, nil)
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
}

// scenarioCrashLoop is scenario 7: the services of the crashing build exhaust
// their start limit, and OnFailure= runs the rollback before the runner's
// deadline.
func scenarioCrashLoop(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionCrash, nil)
	requireRolledBackFor(t, rec, "a service or the runner failed before the update was committed")
	if took := recordTook(rec); took > updateBudget {
		t.Errorf("the rollback took %v, over %v", took, updateBudget)
	}
}

// scenarioRebootInWindow is scenario 8: the controller's host reboots inside
// the health window, after the swap, and the record ends rolled-back.
func scenarioRebootInWindow(ctx context.Context, t *testing.T, f *fleet) {
	rec := failedUpdate(ctx, t, f, versionReboot, func() {
		tx := watchSwap(ctx, t, f.controller, "")
		t.Logf("rebooting the controller's host in the health window of %s", tx)
		if err := f.controller.Reboot(ctx); err != nil {
			t.Fatal(err)
		}
	})
	if rec.Error != nil {
		t.Logf("rolled back for %s: %s", rec.Error.Code, rec.Error.Message)
	}
	for _, unit := range []string{update.ControllerUnit, update.AgentUnit} {
		if state := unitState(ctx, f.controller, unit); state != "active" {
			t.Errorf("%s is %q after the reboot", unit, state)
		}
	}
}

// scenarioRunnerFrozen is scenario 9: the runner is frozen right after the
// swap, so only the deadline timer can act, and it rolls back within two
// minutes of the swap.
func scenarioRunnerFrozen(ctx context.Context, t *testing.T, f *fleet) {
	var tx string
	rec := failedUpdate(ctx, t, f, versionReboot, func() {
		tx = watchSwap(ctx, t, f.controller, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"))
		t.Logf("froze the runner of %s", tx)
		// The deadline rollback kills the runner only when the runner holds
		// the update lock; one frozen outside it would stay stopped.
		t.Cleanup(func() {
			cctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			_, _ = f.controller.Run(cctx, "sudo systemctl kill --signal=KILL "+update.RunnerUnit(tx)+" || true")
		})
	})
	requireRolledBackFor(t, rec, "the services did not report healthy before the deadline")
	out, err := f.controller.Run(ctx, "sudo cat "+paths.Production().UpdateRecord(tx))
	if err != nil {
		t.Fatal(err)
	}
	var r update.Record
	if err := json.Unmarshal([]byte(out), &r); err != nil || r.Deadline == nil {
		t.Fatalf("record %s: %v, deadline %v\n%s", tx, err, r.Deadline, out)
	}
	if took := r.UpdatedAt.Sub(r.Deadline.Add(-update.HealthDeadline)); took > updateBudget {
		t.Errorf("the rollback ended %v after the swap, over %v", took, updateBudget)
	}
}

// scenarioMigrationRollback is scenario 10: the migrating build backs the
// controller database up, migrates both databases and fails its health
// check; the good build then runs on the migrated databases, which keep the
// rows the migration wrote.
func scenarioMigrationRollback(ctx context.Context, t *testing.T, f *fleet) {
	backups := paths.Production().ControllerBackups()
	before := listDir(ctx, t, f.controller, backups)
	rec := failedUpdate(ctx, t, f, versionMigration, nil)
	requireRolledBackFor(t, rec, `with status "degraded"`)
	if got := newEntries(before, listDir(ctx, t, f.controller, backups), versionGood+"-"+versionMigration+"-"); len(got) == 0 {
		t.Errorf("%s gained no backup of the update from %s to %s", backups, versionGood, versionMigration)
	}
	for _, db := range []string{paths.Production().ControllerDB(), paths.Production().AgentDB()} {
		if n, err := sqliteCount(ctx, f.controller, db, "SELECT count(*) FROM harness_marker"); err != nil || n < 1 {
			t.Errorf("%s holds %d harness_marker rows (%v), want the migration's row", db, n, err)
		}
	}
}

// failedUpdate rolls version out, runs during (unless nil) once the rollout
// exists, and checks what every failed update leaves: the rollout failed at
// the controller's host, whose operation rolled back with the new binary
// kept as darkpanel.failed and no reservation left; the agent host was
// skipped and ran no update; both hosts run the good build. It returns the
// record that rolled back.
func failedUpdate(ctx context.Context, t *testing.T, f *fleet, version string, during func()) core.UpdateTransaction {
	t.Helper()
	requireSettled(ctx, t, f, versionGood)
	agentBefore := newestTransaction(ctx, t, f, f.agentID)
	ro := startRollout(ctx, t, f, version)
	if during != nil {
		during()
	}
	ro = followRollout(ctx, t, f, ro.ID, pollInterval, nil)
	if ro.State != core.RolloutFailed {
		t.Errorf("rollout %s to %s is %s, want %s", ro.ID, version, ro.State, core.RolloutFailed)
	}
	ctl, _ := rolloutHost(ro, f.localID)
	if ctl.State != core.RolloutHostFailed || ctl.OperationID == nil {
		t.Fatalf("the controller's host is %s in rollout %s, want %s with an operation", ctl.State, ro.ID, core.RolloutHostFailed)
	}
	if ag, _ := rolloutHost(ro, f.agentID); ag.State != core.RolloutHostSkipped {
		t.Errorf("the agent host is %s in rollout %s, want %s", ag.State, ro.ID, core.RolloutHostSkipped)
	}
	rec := lastRecord(ctx, t, f, f.localID, *ctl.OperationID)
	if rec.State != core.UpdateRolledBack {
		t.Errorf("the controller host's update to %s ended %s, want %s", version, rec.State, core.UpdateRolledBack)
	}
	requireBinaryVersion(ctx, t, f.controller, paths.Production().FailedBinary(), version)
	requireNoLocks(ctx, t, f.controller)
	if after := newestTransaction(ctx, t, f, f.agentID); after != agentBefore {
		t.Errorf("the agent host ran update %s; the rollout must stop at the first failure", after)
	}
	requireFleetVersion(ctx, t, f, versionGood)
	return rec
}

// requireRolledBackFor checks that rec rolled back with an error message
// containing reason, which names the path that rolled it back.
func requireRolledBackFor(t *testing.T, rec core.UpdateTransaction, reason string) {
	t.Helper()
	if rec.Error == nil || !strings.Contains(rec.Error.Message, reason) {
		t.Errorf("update %s rolled back for %+v, want a message containing %q", rec.ID, rec.Error, reason)
	}
}

// startRollout rolls version out to both hosts, the controller's first.
func startRollout(ctx context.Context, t *testing.T, f *fleet, version string) core.Rollout {
	t.Helper()
	var ro core.Rollout
	in := core.RolloutInput{Hosts: []string{f.localID, f.agentID}, Version: version, Channel: releaseChannel}
	if _, err := f.api.Apply(ctx, "", "core.update.rollout/v1", in, &ro); err != nil {
		t.Fatal(err)
	}
	return ro
}

// followRollout polls rollout id every interval until it is no longer
// active, calling observe (unless nil) with each state read, and returns
// the last one. API errors, such as those of the controller restarting
// during its own update, count as not yet.
func followRollout(ctx context.Context, t *testing.T, f *fleet, id string, interval time.Duration, observe func(core.Rollout)) core.Rollout {
	t.Helper()
	ctx, cancel := context.WithTimeout(ctx, rolloutTimeout)
	defer cancel()
	var last core.Rollout
	var lastErr error
	for {
		ro, err := readRollout(ctx, f, id)
		if err == nil {
			last = ro
			if observe != nil {
				observe(ro)
			}
			if !activeRollout(ro.State) {
				return ro
			}
		} else {
			lastErr = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("rollout %s is still %s after %v: %v; last error: %v", id, last.State, rolloutTimeout, ctx.Err(), lastErr)
		case <-time.After(interval):
		}
	}
}

// readRollout returns rollout id from the newest rollouts.
func readRollout(ctx context.Context, f *fleet, id string) (core.Rollout, error) {
	var l core.RolloutList
	if err := f.api.Read(ctx, "", "core.update.rollouts/v1", core.RolloutsInput{Limit: 5}, &l); err != nil {
		return core.Rollout{}, err
	}
	for _, ro := range l.Rollouts {
		if ro.ID == id {
			return ro, nil
		}
	}
	return core.Rollout{}, fmt.Errorf("rollout %s is not among the newest five", id)
}

// rolloutHost returns host id's row of ro.
func rolloutHost(ro core.Rollout, id string) (core.RolloutHost, bool) {
	for _, h := range ro.Hosts {
		if h.HostID == id {
			return h, true
		}
	}
	return core.RolloutHost{}, false
}

// activeRollout reports whether a rollout in state can still update a host.
func activeRollout(state string) bool {
	return state == core.RolloutRunning || state == core.RolloutDraining
}

// requireSettled waits until no rollout is active and both hosts run version
// online. Every update scenario starts from it, so a scenario that failed
// does not leave the next one starting on a fleet that is still updating.
func requireSettled(ctx context.Context, t *testing.T, f *fleet, version string) {
	t.Helper()
	err := eventually(ctx, 3*time.Minute, func(ctx context.Context) error {
		var l core.RolloutList
		if err := f.api.Read(ctx, "", "core.update.rollouts/v1", core.RolloutsInput{Limit: 5}, &l); err != nil {
			return err
		}
		for _, ro := range l.Rollouts {
			if activeRollout(ro.State) {
				return fmt.Errorf("rollout %s to %s is %s", ro.ID, ro.Version, ro.State)
			}
		}
		hosts, err := f.api.Hosts(ctx)
		if err != nil {
			return err
		}
		return hostsAt(version, f.localID, f.agentID)(hosts)
	})
	if err != nil {
		t.Fatalf("the fleet did not settle on %s: %v", version, err)
	}
}

// requireFleetVersion waits until the host list shows both hosts online at
// version, and checks that both installed binaries report it.
func requireFleetVersion(ctx context.Context, t *testing.T, f *fleet, version string) {
	t.Helper()
	waitHosts(ctx, t, f, 3*time.Minute, hostsAt(version, f.localID, f.agentID))
	for _, v := range []*vm.VM{f.controller, f.agent} {
		requireBinaryVersion(ctx, t, v, paths.Production().Binary(), version)
	}
}

// lastRecord returns the newest record of operation op on host. The agent
// may still be restarting after a rollback, so API errors are retried.
func lastRecord(ctx context.Context, t *testing.T, f *fleet, host, op string) core.UpdateTransaction {
	t.Helper()
	var st core.UpdateStatus
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		if err := f.api.Read(ctx, host, "core.update.status/v1", core.UpdateStatusInput{OperationID: op}, &st); err != nil {
			return err
		}
		if len(st.Transactions) == 0 {
			return fmt.Errorf("host %s has no record of operation %s", host, op)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return st.Transactions[0]
}

// newestTransaction returns the id of host's newest update record, or "".
func newestTransaction(ctx context.Context, t *testing.T, f *fleet, host string) string {
	t.Helper()
	var st core.UpdateStatus
	err := eventually(ctx, time.Minute, func(ctx context.Context) error {
		return f.api.Read(ctx, host, "core.update.status/v1", core.UpdateStatusInput{}, &st)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Transactions) == 0 {
		return ""
	}
	return st.Transactions[0].ID
}

// recordTook is how long rec's step ran, by the host's own clock.
func recordTook(rec core.UpdateTransaction) time.Duration { return rec.UpdatedAt.Sub(rec.StartedAt) }

// requireNoLocks waits until v's agent database holds no lock: a terminal
// update releases its reservation just after it records its state.
func requireNoLocks(ctx context.Context, t *testing.T, v *vm.VM) {
	t.Helper()
	err := eventually(ctx, 30*time.Second, func(ctx context.Context) error {
		n, err := sqliteCount(ctx, v, paths.Production().AgentDB(), "SELECT count(*) FROM core_locks")
		if err == nil && n != 0 {
			err = fmt.Errorf("core_locks holds %d rows", n)
		}
		return err
	})
	if err != nil {
		t.Error(err)
	}
}

// sqliteCount runs a count query, free of single quotes, on db on v as root.
func sqliteCount(ctx context.Context, v *vm.VM, db, query string) (int, error) {
	out, err := v.Run(ctx, "sudo sqlite3 "+db+" '"+query+"'")
	if err != nil {
		return 0, err
	}
	return strconv.Atoi(strings.TrimSpace(out))
}

// listDir lists dir on v as root; a missing dir lists nothing.
func listDir(ctx context.Context, t *testing.T, v *vm.VM, dir string) []string {
	t.Helper()
	out, err := v.Run(ctx, "sudo sh -c 'ls -1 "+dir+" 2>/dev/null || true'")
	if err != nil {
		t.Fatal(err)
	}
	return strings.Fields(out)
}

// newEntries returns the names of after, starting with prefix, that before
// lacks.
func newEntries(before, after []string, prefix string) []string {
	var out []string
	for _, name := range after {
		if strings.HasPrefix(name, prefix) && !slices.Contains(before, name) {
			out = append(out, name)
		}
	}
	return out
}

// watchSwap waits on v for the update in its health window and runs then
// there, at once, with $tx set; it returns the transaction id.
func watchSwap(ctx context.Context, t *testing.T, v *vm.VM, then string) string {
	t.Helper()
	p := paths.Production()
	out, err := v.RunInput(ctx, "sudo sh -s", strings.NewReader(swapWatch(p.UpdateDir, p.Binary(), then, 3*time.Minute)))
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(out)
}

// swapWatch is a shell script, run as root, that waits up to timeout for an
// update record in dir to reach swapped and for binary to be that record's
// new binary, then runs then with $tx set to the record's transaction id and
// prints the id. The runner records swapped before it renames the new
// binary into place, so waiting for the hash too puts then inside the health
// window.
func swapWatch(dir, binary, then string, timeout time.Duration) string {
	return fmt.Sprintf(`set -eu
end=$(( $(date +%%s) + %d ))
while [ "$(date +%%s)" -le "$end" ]; do
	for f in %s/*.json; do
		[ -f "$f" ] || continue
		grep -q '"state": "swapped"' "$f" || continue
		want=$(sed -n 's/.*"newSha256": "\([0-9a-f]*\)".*/\1/p' "$f")
		[ -n "$want" ] && [ "$(sha256sum %s | cut -d ' ' -f 1)" = "$want" ] || continue
		tx=$(basename "$f" .json)
		%s
		echo "$tx"
		exit 0
	done
	sleep 0.1
done
echo "no update record reached swapped with its new binary in place" >&2
exit 1
`, int(timeout/time.Second), dir, binary, then)
}

func TestSwapWatch(t *testing.T) {
	root := t.TempDir()
	p := paths.Under(root)
	if err := os.MkdirAll(p.UpdateDir, 0o700); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(root, "darkpanel")
	if err := os.WriteFile(binary, []byte("the new binary"), 0o600); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("the new binary"))
	record := func(id, state, newSHA string) {
		t.Helper()
		r := update.Record{ID: id, OperationID: id, Kind: core.UpdateKindUpdate, FromVersion: "0.3.0", ToVersion: "0.3.3",
			TargetVersion: "0.3.3", State: state, NewSHA256: newSHA}
		if err := update.WriteRecord(p, r); err != nil {
			t.Fatal(err)
		}
	}
	watch := func(then string) (string, error) {
		script := swapWatch(p.UpdateDir, binary, then, time.Second)
		out, err := exec.Command("sh", "-c", script).CombinedOutput() //nolint:gosec // G204: the test's own script
		return string(out), err
	}

	record("01OLDCOMMITTED", core.UpdateCommitted, hex.EncodeToString(sum[:]))
	if out, err := watch(""); err == nil {
		t.Errorf("swapWatch over committed records only = %q, want a timeout", out)
	}
	record("01SWAPPEDOTHER", core.UpdateSwapped, strings.Repeat("0", 64))
	if out, err := watch(""); err == nil || !strings.Contains(out, "no update record reached swapped with its new binary in place") {
		t.Errorf("swapWatch while the binary is not yet the new one = %q, %v; want a timeout", out, err)
	}
	record("01SWAPPEDOTHER", core.UpdateRolledBack, strings.Repeat("0", 64))
	record("01SWAPPEDNEW", core.UpdateSwapped, hex.EncodeToString(sum[:]))
	thenFile := filepath.Join(root, "then")
	out, err := watch(`echo "frozen $tx" > ` + thenFile)
	if err != nil || out != "01SWAPPEDNEW\n" {
		t.Errorf("swapWatch = %q, %v; want the swapped transaction", out, err)
	}
	if got, err := os.ReadFile(thenFile); err != nil || string(got) != "frozen 01SWAPPEDNEW\n" { //nolint:gosec // G304: the test's own file
		t.Errorf("then ran with %q, %v; want $tx set to the swapped transaction", got, err)
	}
	if script := swapWatch(p.UpdateDir, binary, "systemctl kill --signal=STOP "+update.RunnerUnit("$tx"), time.Minute); !strings.Contains(script,
		"systemctl kill --signal=STOP darkpanel-update-$tx.service") {
		t.Errorf("swapWatch does not run its command with the runner unit of $tx:\n%s", script)
	}
}

func TestRolloutHelpers(t *testing.T) {
	ro := core.Rollout{ID: "r1", State: core.RolloutDraining, Hosts: []core.RolloutHost{
		{HostID: "h0", Position: 0, State: core.RolloutHostFailed},
		{HostID: "h1", Position: 1, State: core.RolloutHostSkipped},
	}}
	if h, ok := rolloutHost(ro, "h1"); !ok || h.State != core.RolloutHostSkipped {
		t.Errorf("rolloutHost(h1) = %+v, %v", h, ok)
	}
	if _, ok := rolloutHost(ro, "h9"); ok {
		t.Error("rolloutHost found a host the rollout does not list")
	}
	for state, want := range map[string]bool{
		core.RolloutRunning: true, core.RolloutDraining: true,
		core.RolloutSucceeded: false, core.RolloutFailed: false, core.RolloutCancelled: false,
	} {
		if got := activeRollout(state); got != want {
			t.Errorf("activeRollout(%s) = %v, want %v", state, got, want)
		}
	}
	before := []string{"0.2.0-0.3.0-20261020T120000Z", "none-0.2.0-20261020T110000Z"}
	after := []string{"0.2.0-0.3.0-20261020T120000Z", "0.3.0-0.3.4-20261020T130000Z", "0.3.0-0.3.1-20261020T125000Z"}
	if got := newEntries(before, after, "0.3.0-0.3.4-"); !slices.Equal(got, []string{"0.3.0-0.3.4-20261020T130000Z"}) {
		t.Errorf("newEntries = %v", got)
	}
	if got := newEntries(after, after, "0.3.0-0.3.4-"); len(got) != 0 {
		t.Errorf("newEntries of an unchanged directory = %v", got)
	}
	start := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	rec := core.UpdateTransaction{StartedAt: start, UpdatedAt: start.Add(95 * time.Second)}
	if got := recordTook(rec); got != 95*time.Second {
		t.Errorf("recordTook = %v, want 1m35s", got)
	}
}
```

In `test/harness/m2_test.go`, replace:

```go
		{"self-management", true, func(t *testing.T) { scenarioSelfManagement(ctx, t, f) }},
		{"offline", false, func(t *testing.T) { scenarioOffline(ctx, t, f) }},
		{"revoke", true, func(t *testing.T) { scenarioRevoke(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
```

with:

```go
		{"self-management", true, func(t *testing.T) { scenarioSelfManagement(ctx, t, f) }},
		{"offline", false, func(t *testing.T) { scenarioOffline(ctx, t, f) }},
		{"revoke", true, func(t *testing.T) { scenarioRevoke(ctx, t, f) }},
		{"update-good", true, func(t *testing.T) { scenarioUpdateGood(ctx, t, f) }},
		{"health-failure", false, func(t *testing.T) { scenarioHealthFailure(ctx, t, f) }},
		{"crash-loop", false, func(t *testing.T) { scenarioCrashLoop(ctx, t, f) }},
		{"reboot-in-window", false, func(t *testing.T) { scenarioRebootInWindow(ctx, t, f) }},
		{"runner-frozen", false, func(t *testing.T) { scenarioRunnerFrozen(ctx, t, f) }},
		{"migration-rollback", false, func(t *testing.T) { scenarioMigrationRollback(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness`, `test/harness/release` and `test/harness/vm`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/21 go mod tidy -diff`
Expected: no output, exit 0

- [ ] **Step 5: Commit**

```bash
git add test/harness/m2_update_test.go test/harness/m2_test.go
git commit -m "test(harness): add the M2 update scenarios"
```

---

### Task 22: Break-glass and uninstall scenarios

**Files:**
- Create: `test/harness/m2_breakglass_test.go`
- Modify: `test/harness/m2_test.go`
- Modify: `docs/superpowers/gates.md`
- Test: `test/harness/m2_breakglass_test.go`

**Interfaces:**
- Consumes: Task 20 (`fleet`, `versionN`, `versionGood`, `agentAddress`, `installRole`, `login`, `waitHosts`, `hostsAt`, `localHost`, `findHost`, `requireBinaryVersion`, `(*vm.VM).RunInput`, the step list of `TestM2`); Task 21 (`requireSettled`); C6/C16 (`darkpanel hostinfo summary get --local --json`; `darkpanel update rollback --local --yes --json`, whose fallback prints `darkpanel: the agent socket <path> does not answer; running core.update.rollback in this process` to standard error and re-executes `darkpanel.prev update rollback --direct --json`); C17 (`darkpanel uninstall [--keep-data] --yes`); `install.Production().UnitPath`, `install.RecoverUnit`, `install.ServiceUser`; `update.ControllerUnit`, `update.AgentUnit`, `update.RollbackUnit`; `paths.Production` (`AgentSocket`, `AgentDB`, `ControllerDB`, `ControllerKey`, `AgentKey`, `AgentTLSCurrent`, `Binary`); `core.UpdateTransaction`, `core.UpdateKindRollback`, `core.UpdateRolledBack`.
- Produces: package `harness`, file `m2_breakglass_test.go`: functions `scenarioBreakGlass`, `scenarioUninstall`, `requireUnitsGone`, `readManifest`, `parseManifest`, `remainingScript`; `type ownedEntry struct{ Kind, Module, Block, Path string }`; tests `TestParseManifest`, `TestRemainingScript`. TestM2 gains the steps `break-glass` and `uninstall`. Gate 7 in `docs/superpowers/gates.md` names the M2 scenarios, applies also to `install.sh.in`, `internal/install/` and `internal/update/`, and records the budget (job 120 minutes, go test 100 minutes) as inferences to measure on the first run.

**Items:** 7, 9, 10, 18
**Depends on:** Task 6, Task 16, Task 17, Task 20, Task 21
**Implementer:** dr-superpowers:impl-sonnet-medium
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 1 = 3

Notes for the implementer:
- The `--local` fallback of the irreversible `core.update.rollback` needs `--yes` and no confirmation phrase: without the agent there is no plan to name one (C6). Without a terminal and without `--yes` it exits 3 with `standard input is not a terminal; pass --yes to apply`.
- The agent host can roll back from the good build to N because its newest operation is scenario 5's committed update (scenarios 6 to 10 never reach it) and its `darkpanel.prev` is the N binary that update replaced. The rollback restarts the agent itself.
- The manifest (`core_owned_files` in agent.db) is read before `uninstall`, which deletes agent.db last. `remainingScript` reports a `user` row whose user exists, a `block` row whose file still holds `BEGIN DARKPANEL <module>/<block>` (the marker `platform.Files` writes), and any other row whose path exists, a dangling link included.
- After `uninstall --keep-data` on the controller, the reinstall of N opens the kept databases; a fresh login (the kept admin and TOTP secret, sealed by the kept controller key) and the host list (the same local host id, the agent host still listed) show it.

- [ ] **Step 1: Write the failing test**

Create `test/harness/m2_breakglass_test.go` with the unit tests of its helpers:

```go
//go:build harness

package harness

import (
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestParseManifest(t *testing.T) {
	out := "unit|core||/etc/systemd/system/darkpanel-agent.service\nblock|firewall|rules|/etc/ufw/before.rules\nuser|core||darkpanel\n"
	got, err := parseManifest(out)
	want := []ownedEntry{
		{Kind: "unit", Module: "core", Path: "/etc/systemd/system/darkpanel-agent.service"},
		{Kind: "block", Module: "firewall", Block: "rules", Path: "/etc/ufw/before.rules"},
		{Kind: "user", Module: "core", Path: "darkpanel"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("parseManifest = %+v, %v; want %+v", got, err, want)
	}
	for _, bad := range []string{"unit|core|/etc/x\n", "dir|core||\n", "|core||/etc/x\n"} {
		if got, err := parseManifest(bad); err == nil {
			t.Errorf("parseManifest(%q) = %+v, want an error", bad, got)
		}
	}
}

func TestRemainingScript(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kept := write("kept.conf", "x\n")
	blocks := write("hosts", "127.0.0.1 localhost\n# BEGIN DARKPANEL demo/names\n10.0.0.2 b\n# END DARKPANEL demo/names\n")
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	entries := []ownedEntry{
		{Kind: "file", Module: "core", Path: kept},
		{Kind: "file", Module: "core", Path: filepath.Join(dir, "gone.conf")},
		{Kind: "file", Module: "core", Path: link},
		{Kind: "dir", Module: "core", Path: dir},
		{Kind: "unit", Module: "core", Path: filepath.Join(dir, "gone.service")},
		{Kind: "block", Module: "demo", Block: "names", Path: blocks},
		{Kind: "block", Module: "demo", Block: "other", Path: blocks},
		{Kind: "block", Module: "demo", Block: "names", Path: filepath.Join(dir, "gone-hosts")},
		{Kind: "user", Module: "core", Path: "root"},
		{Kind: "user", Module: "core", Path: "dp-no-such-user"},
	}
	out, err := exec.Command("sh", "-c", remainingScript(entries)).CombinedOutput() //nolint:gosec // G204: the test's own script
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{"file " + kept, "file " + link, "dir " + dir, "block demo/names in " + blocks, "user root"}
	if got := strings.Split(strings.TrimSpace(string(out)), "\n"); !slices.Equal(got, want) {
		t.Errorf("remaining = %q, want %q", got, want)
	}
	if out, err := exec.Command("sh", "-c", remainingScript(nil)).CombinedOutput(); err != nil || len(out) != 0 { //nolint:gosec // G204: the test's own script
		t.Errorf("remainingScript of no entries printed %q, %v; want nothing", out, err)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 timeout 600 go test -tags harness -count=1 -run 'TestParseManifest|TestRemainingScript' ./test/harness/`
Expected: FAIL with `test/harness/m2_breakglass_test.go:16:14: undefined: parseManifest`

- [ ] **Step 3: Write the implementation**

Replace the complete content of `test/harness/m2_breakglass_test.go` with:

```go
//go:build harness

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/install"
	"github.com/darkraise/darkpanel/internal/paths"
	"github.com/darkraise/darkpanel/internal/update"
	"github.com/darkraise/darkpanel/test/harness/vm"
)

// scenarioBreakGlass is scenario 11: with the controller stopped the agent's
// local CLI still reads through the agent socket, and with the agent
// stopped too, update rollback --local runs the rollback in-process and
// returns the agent host from the good build to N.
func scenarioBreakGlass(ctx context.Context, t *testing.T, f *fleet) {
	requireSettled(ctx, t, f, versionGood)
	if _, err := f.controller.Run(ctx, "sudo systemctl stop "+update.ControllerUnit); err != nil {
		t.Fatal(err)
	}
	out, err := f.agent.Run(ctx, "sudo darkpanel hostinfo summary get --local --json")
	if err != nil {
		t.Fatal(err)
	}
	var sum struct {
		Hostname string `json:"hostname"`
	}
	if err := json.Unmarshal([]byte(out), &sum); err != nil || sum.Hostname != "agent" {
		t.Errorf("hostinfo summary get --local = %+v, %v; want the agent's summary\n%s", sum, err, out)
	}

	if _, err := f.agent.Run(ctx, "sudo systemctl stop "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	// The irreversible rollback asks for no confirmation phrase here: without
	// an agent there is no plan to name one, so --yes is all it takes.
	const notice = "/var/tmp/rollback-notice"
	out, err = f.agent.Run(ctx, "sudo darkpanel update rollback --local --yes --json 2>"+notice)
	stderr, _ := f.agent.Run(ctx, "cat "+notice)
	if err != nil {
		t.Fatalf("%v\n%s", err, stderr)
	}
	want := "darkpanel: the agent socket " + paths.Production().AgentSocket + " does not answer; running core.update.rollback in this process"
	if !strings.Contains(stderr, want) {
		t.Errorf("update rollback --local printed %q to standard error, want %q", stderr, want)
	}
	var tr core.UpdateTransaction
	if err := json.Unmarshal([]byte(out), &tr); err != nil {
		t.Fatalf("update rollback --local --json: %v\n%s", err, out)
	}
	if tr.Kind != core.UpdateKindRollback || tr.FromVersion != versionGood || tr.ToVersion != versionN || tr.State != core.UpdateRolledBack {
		t.Errorf("the rollback is %+v; want a %s from %s to %s, %s", tr, core.UpdateKindRollback, versionGood, versionN, core.UpdateRolledBack)
	}
	requireBinaryVersion(ctx, t, f.agent, paths.Production().Binary(), versionN)

	// The rollback restarts the agent itself; starting both units again is a
	// no-op for a running one.
	if _, err := f.agent.Run(ctx, "sudo systemctl start "+update.AgentUnit); err != nil {
		t.Fatal(err)
	}
	if _, err := f.controller.Run(ctx, "sudo systemctl start "+update.ControllerUnit); err != nil {
		t.Fatal(err)
	}
	waitHosts(ctx, t, f, 3*time.Minute, func(hosts []core.Host) error {
		if err := hostsAt(versionGood, f.localID)(hosts); err != nil {
			return err
		}
		return hostsAt(versionN, f.agentID)(hosts)
	})
}

// scenarioUninstall is scenario 12: uninstall leaves nothing of the agent's
// ownership manifest and none of its units; on the controller, uninstall
// --keep-data and a reinstall of N find the kept databases, keys and users.
func scenarioUninstall(ctx context.Context, t *testing.T, f *fleet) {
	// uninstall deletes agent.db, the manifest with it, last.
	entries := readManifest(ctx, t, f.agent)
	agentUnit := install.Production().UnitPath(update.AgentUnit)
	if !slices.ContainsFunc(entries, func(e ownedEntry) bool { return e.Path == agentUnit }) {
		t.Fatalf("the agent's ownership manifest does not list %s: %+v", agentUnit, entries)
	}
	if out, err := f.agent.Run(ctx, "sudo darkpanel uninstall --yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	out, err := f.agent.RunInput(ctx, "sudo sh -s", strings.NewReader(remainingScript(entries)))
	if err != nil {
		t.Fatal(err)
	}
	if left := strings.TrimSpace(out); left != "" {
		t.Errorf("uninstall left what the manifest lists:\n%s", left)
	}
	requireUnitsGone(ctx, t, f.agent, update.AgentUnit, update.RollbackUnit, install.RecoverUnit)

	if out, err := f.controller.Run(ctx, "sudo darkpanel uninstall --keep-data --yes"); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	requireUnitsGone(ctx, t, f.controller, update.ControllerUnit, update.AgentUnit, update.RollbackUnit, install.RecoverUnit)
	p := paths.Production()
	for _, kept := range []string{p.ControllerDB(), p.AgentDB(), p.ControllerKey, p.AgentKey, p.AgentTLSCurrent()} {
		if _, err := f.controller.Run(ctx, "sudo test -e "+kept); err != nil {
			t.Errorf("uninstall --keep-data removed %s", kept)
		}
	}
	if _, err := f.controller.Run(ctx, "getent passwd "+install.ServiceUser); err != nil {
		t.Errorf("uninstall --keep-data removed the user %s: %v", install.ServiceUser, err)
	}
	installRole(ctx, t, f.controller, versionN, "--role controller --agent-listen "+agentAddress)
	// A new session needs the kept admin, its TOTP secret and the kept
	// controller key, which seals them.
	login(ctx, t, f)
	waitHosts(ctx, t, f, 2*time.Minute, func(hosts []core.Host) error {
		if h, ok := localHost(hosts); !ok || h.ID != f.localID {
			return fmt.Errorf("the local host is %+v, want the kept host %s", h, f.localID)
		}
		if _, ok := findHost(hosts, f.agentID); !ok {
			return fmt.Errorf("the kept agent host %s is not listed", f.agentID)
		}
		return hostsAt(versionN, f.localID)(hosts)
	})
}

// requireUnitsGone checks that systemd on v no longer knows units.
func requireUnitsGone(ctx context.Context, t *testing.T, v *vm.VM, units ...string) {
	t.Helper()
	for _, u := range units {
		out, err := v.Run(ctx, "systemctl show -p LoadState --value "+u)
		if err != nil {
			t.Fatal(err)
		}
		if state := strings.TrimSpace(out); state != "not-found" {
			t.Errorf("%s is %s after uninstall, want not-found", u, state)
		}
	}
}

// ownedEntry is one row of the ownership manifest (core_owned_files).
type ownedEntry struct {
	Kind, Module, Block, Path string
}

// readManifest reads v's ownership manifest from its agent database.
func readManifest(ctx context.Context, t *testing.T, v *vm.VM) []ownedEntry {
	t.Helper()
	out, err := v.Run(ctx, "sudo sqlite3 -separator '|' "+paths.Production().AgentDB()+
		` "SELECT kind, module, block_id, path FROM core_owned_files ORDER BY path, module, block_id"`)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := parseManifest(out)
	if err != nil {
		t.Fatal(err)
	}
	return entries
}

// parseManifest reads sqlite3's kind|module|block_id|path lines.
func parseManifest(out string) ([]ownedEntry, error) {
	var entries []ownedEntry
	for line := range strings.SplitSeq(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 4)
		if len(f) != 4 || f[0] == "" || f[3] == "" {
			return nil, fmt.Errorf("harness: manifest line %q: want kind|module|block_id|path", line)
		}
		entries = append(entries, ownedEntry{Kind: f[0], Module: f[1], Block: f[2], Path: f[3]})
	}
	return entries, nil
}

// remainingScript is a shell script, run as root, that prints one line for
// each manifest entry still present: a user that exists, a marked block
// still in its file, or any other path that exists, a dangling link
// included.
func remainingScript(entries []ownedEntry) string {
	var b strings.Builder
	b.WriteString(`while IFS='|' read -r kind module block path; do
	case $kind in
	user) getent passwd "$path" >/dev/null && echo "user $path" ;;
	block) grep -qsF "BEGIN DARKPANEL $module/$block" "$path" && echo "block $module/$block in $path" ;;
	*) { [ -e "$path" ] || [ -L "$path" ]; } && echo "$kind $path" ;;
	esac
done <<'MANIFEST'
`)
	for _, e := range entries {
		b.WriteString(e.Kind + "|" + e.Module + "|" + e.Block + "|" + e.Path + "\n")
	}
	b.WriteString("MANIFEST\ntrue\n")
	return b.String()
}

func TestParseManifest(t *testing.T) {
	out := "unit|core||/etc/systemd/system/darkpanel-agent.service\nblock|firewall|rules|/etc/ufw/before.rules\nuser|core||darkpanel\n"
	got, err := parseManifest(out)
	want := []ownedEntry{
		{Kind: "unit", Module: "core", Path: "/etc/systemd/system/darkpanel-agent.service"},
		{Kind: "block", Module: "firewall", Block: "rules", Path: "/etc/ufw/before.rules"},
		{Kind: "user", Module: "core", Path: "darkpanel"},
	}
	if err != nil || !slices.Equal(got, want) {
		t.Errorf("parseManifest = %+v, %v; want %+v", got, err, want)
	}
	for _, bad := range []string{"unit|core|/etc/x\n", "dir|core||\n", "|core||/etc/x\n"} {
		if got, err := parseManifest(bad); err == nil {
			t.Errorf("parseManifest(%q) = %+v, want an error", bad, got)
		}
	}
}

func TestRemainingScript(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		t.Helper()
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	kept := write("kept.conf", "x\n")
	blocks := write("hosts", "127.0.0.1 localhost\n# BEGIN DARKPANEL demo/names\n10.0.0.2 b\n# END DARKPANEL demo/names\n")
	link := filepath.Join(dir, "dangling")
	if err := os.Symlink(filepath.Join(dir, "missing"), link); err != nil {
		t.Fatal(err)
	}
	entries := []ownedEntry{
		{Kind: "file", Module: "core", Path: kept},
		{Kind: "file", Module: "core", Path: filepath.Join(dir, "gone.conf")},
		{Kind: "file", Module: "core", Path: link},
		{Kind: "dir", Module: "core", Path: dir},
		{Kind: "unit", Module: "core", Path: filepath.Join(dir, "gone.service")},
		{Kind: "block", Module: "demo", Block: "names", Path: blocks},
		{Kind: "block", Module: "demo", Block: "other", Path: blocks},
		{Kind: "block", Module: "demo", Block: "names", Path: filepath.Join(dir, "gone-hosts")},
		{Kind: "user", Module: "core", Path: "root"},
		{Kind: "user", Module: "core", Path: "dp-no-such-user"},
	}
	out, err := exec.Command("sh", "-c", remainingScript(entries)).CombinedOutput() //nolint:gosec // G204: the test's own script
	if err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	want := []string{"file " + kept, "file " + link, "dir " + dir, "block demo/names in " + blocks, "user root"}
	if got := strings.Split(strings.TrimSpace(string(out)), "\n"); !slices.Equal(got, want) {
		t.Errorf("remaining = %q, want %q", got, want)
	}
	if out, err := exec.Command("sh", "-c", remainingScript(nil)).CombinedOutput(); err != nil || len(out) != 0 { //nolint:gosec // G204: the test's own script
		t.Errorf("remainingScript of no entries printed %q, %v; want nothing", out, err)
	}
}
```

In `test/harness/m2_test.go`, replace:

```go
		{"reboot-in-window", false, func(t *testing.T) { scenarioRebootInWindow(ctx, t, f) }},
		{"runner-frozen", false, func(t *testing.T) { scenarioRunnerFrozen(ctx, t, f) }},
		{"migration-rollback", false, func(t *testing.T) { scenarioMigrationRollback(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
```

with:

```go
		{"reboot-in-window", false, func(t *testing.T) { scenarioRebootInWindow(ctx, t, f) }},
		{"runner-frozen", false, func(t *testing.T) { scenarioRunnerFrozen(ctx, t, f) }},
		{"migration-rollback", false, func(t *testing.T) { scenarioMigrationRollback(ctx, t, f) }},
		{"break-glass", false, func(t *testing.T) { scenarioBreakGlass(ctx, t, f) }},
		{"uninstall", false, func(t *testing.T) { scenarioUninstall(ctx, t, f) }},
	}
	for _, s := range steps {
		if !t.Run(s.name, s.run) && s.gate {
```

In `docs/superpowers/gates.md`, replace:

```markdown
Command: `timeout 60 gh run list --workflow harness.yml --branch "$(git branch --show-current)" --commit "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion'`
Green: `success`
Evidence: output
Applies: on `main` once it is pushed to origin, and on any branch whose diff against `main` touches `test/harness/` or `.github/workflows/harness.yml`, once that branch is pushed and `gh workflow run harness.yml --ref "$(git branch --show-current)"` has run to completion (`gh run watch`); the query finds `workflow_dispatch` runs as well as push runs
Why: only real VMs show that UFW, Docker and tailscaled behave on all four operating systems and that a full scan from outside finds only 22/tcp (R21, ADR 0008); the run on `main` is M1's harness evidence
```

with:

```markdown
Command: `timeout 60 gh run list --workflow harness.yml --branch "$(git branch --show-current)" --commit "$(git rev-parse HEAD)" --json conclusion --jq '.[0].conclusion'`
Green: `success`
Evidence: output
Applies: on `main` once it is pushed to origin, and on any branch whose diff against `main` touches `test/harness/`, `.github/workflows/harness.yml`, `install.sh.in`, `internal/install/` or `internal/update/`, once that branch is pushed and `gh workflow run harness.yml --ref "$(git branch --show-current)"` has run to completion (`gh run watch`); the query finds `workflow_dispatch` runs as well as push runs
Why: only real VMs show that UFW, Docker and tailscaled behave on all four operating systems and that a full scan from outside finds only 22/tcp (R21, ADR 0008), and, in TestM2's twelve scenarios on three VMs per OS (M2 spec section 13), that install.sh installs a controller and an enrolled agent, that the agent survives going offline and being revoked, that rollouts update, roll back on a failed health check, a crash loop, a reboot in the health window and a frozen runner, and keep migrated databases working, and that break-glass and uninstall work; the run on `main` is M1's and M2's harness evidence. Budget: the vm job's `timeout-minutes` is 120 and go test's `-timeout` 100m (TestM1's deadline 40 minutes, TestM2's 55); both are inferences, to be measured on the first M2 run and recorded here
```

- [ ] **Step 4: Run test to verify it passes**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 timeout 600 go test -tags harness -race -count=1 ./test/harness/...`
Expected: PASS (`ok` for `test/harness`, `test/harness/release` and `test/harness/vm`)

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 go build ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 go vet ./...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 go vet -tags harness ./test/...`
Expected: no output, exit 0

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 timeout 900 golangci-lint run --allow-serial-runners ./...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 timeout 900 golangci-lint run --allow-serial-runners --build-tags harness ./test/...`
Expected: `0 issues.`

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/22 go mod tidy -diff`
Expected: no output, exit 0

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
Expected: `check-adrs tests: 21 passed, 0 failed` and no `check-adrs:` error line

- [ ] **Step 5: Commit**

```bash
git add test/harness/m2_breakglass_test.go test/harness/m2_test.go docs/superpowers/gates.md
git commit -m "test(harness): add break-glass and uninstall"
```

---

### Task 23: User documentation

**Files:**
- Modify: `README.md`
- Modify: `CONTRIBUTING.md`

**Interfaces:**
- Consumes: the commands of C13-C17 and `--local` (C6), the serve flags (C14), `make harness-builds` (C19), as built by Tasks 6 and 13-19.
- Produces: nothing other tasks use.

Documentation only: there is no failing test. Step 2 is the check that every command the text names exists with the flags it shows; Step 4 is the ADR checker, which also reads the repository's Markdown links.

**Depends on:** Task 6, Task 13, Task 14, Task 15, Task 16, Task 17, Task 19
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Write the documentation**

Replace the whole of `README.md` with:

````markdown
# Darkpanel

Darkpanel is an MIT-licensed, single-binary VPS and self-hosted fleet manager,
with a CLI and a web UI, for owners of unmanaged Linux servers. It starts with
a Docker-aware UFW firewall, Docker, Tailscale and DERP, reverse-proxy exposure
and safe self-update.

## Status

Pre-release, in milestone M2 (Fleet and lifecycle). `darkpanel serve` runs the
controller: the REST API with TOTP login, the web UI, the audit log and the
agent listener. `darkpanel agent run` runs the agent on every host, the
controller's own included, over mutual TLS. `install.sh` and
`darkpanel system install` install either role, and hosts update themselves
from signed releases with automatic rollback. No release has been published
yet; do not use it on a server you care about.

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
from. The commands below call it `darkpanel`; copy the binary for your
machine's architecture onto your `PATH` under that name:

```sh
sudo install -m 0755 bin/darkpanel-linux-amd64 /usr/local/bin/darkpanel
```

## Install

`install.sh` installs a release on Ubuntu 22.04 or 24.04 or Debian 12 or 13. It
verifies the release's minisign signature with OpenSSL against the keys it
embeds, checks the tarball's checksum, installs the binary under
`/usr/local/lib/darkpanel/` with `/usr/local/bin/darkpanel` pointing at it, and
runs `darkpanel system install` for the role. On the controller's host:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh -o install.sh
sudo sh install.sh --role controller --agent-advertise <address agents reach>:7443
```

`system install` creates the `darkpanel` user and the directories, keys and
systemd units, starts `darkpanel-controller.service`, enrolls the host's own
agent and starts `darkpanel-agent.service`. Rerunning it finishes or repairs an
install. The controller runs as `darkpanel`, the agent as root. The agent
listener is always bound on `127.0.0.1:7443` and on port 7443 of each
Tailscale address; `--agent-listen <ip>:<port>` adds a bind, and
`--agent-advertise` is the address enrollment tokens carry when the request
names none. Both are written to
`/etc/systemd/system/darkpanel-controller.service.d/10-agent.conf`.

The controller keeps its database under `/var/lib/darkpanel/controller/`, the
agent under `/var/lib/darkpanel/agent/`, the keys and the agent's credentials
under `/etc/darkpanel/`, and serves HTTP on `127.0.0.1:8080` and `[::1]:8080`
and HTTPS on port 8443 of each Tailscale address. `--listen` in a drop-in
replaces those web listeners and may be repeated: `loopback`, `tailscale`,
`https://<ip>:<port>` or `http://<loopback ip>:<port>`. Reach the loopback
listener from another machine through SSH port forwarding:
`ssh -L 8080:127.0.0.1:8080 <host>`.

### Add a host

Create the host on the web UI's Hosts page ("Add host"), or with
`darkpanel host create --name <name>`. The answer shows a one-hour, single-use
enrollment token once. On the new host:

```sh
curl -fsSL https://github.com/Darkraise/darkpanel/releases/latest/download/install.sh -o install.sh
sudo sh install.sh --role agent
```

It asks for the token on the terminal (or reads it from `--token-file`, a
root-owned file with mode 0600). The agent connects out to the controller; it
opens no listening port. `darkpanel host revoke` closes a host's session and
refuses its certificate; `darkpanel host token` issues a revoked host a new
token, and `sudo darkpanel agent enroll` on the host takes it.

### Updates

A host updates to a signed release with `darkpanel update rollout --hosts <id>
--version <version> --channel stable`, one host at a time, the controller's own host first,
stopping at the first failure; `darkpanel host update-policy` sets a host's
channel (`stable` or `beta`) and an optional weekly update window. Each update
takes a database backup before migrating, keeps the previous binary as
`darkpanel.prev`, and rolls back by itself when the new version is not healthy
within 80 seconds. [docs/versioning.md](docs/versioning.md) explains the
version and compatibility rules.

### Break-glass

On a host whose controller cannot be reached, `sudo darkpanel --local
<command>` sends an agent-side command to the local agent's socket instead,
for example `sudo darkpanel hostinfo summary get --local`. With the agent
stopped too, `sudo darkpanel update rollback --local --yes` puts the previous
binary back.

### Uninstall

`sudo darkpanel uninstall` stops and removes the services, units, binary,
users, data and keys listed in the host's ownership manifest, after printing
the plan. `--keep-data` keeps the databases, backups, keys and credentials, so
a later install finds them.

### The first admin

There is no default account and no setup page. Create the first admin on the
controller's host itself, through the controller socket:

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
in `DARKPANEL_TOKEN` or as `"token"` in `cli.json`. A token in `cli.json` is
sent only to the `"url"` in the same file (or to `http://127.0.0.1:8080` when
the file names none), and the CLI refuses to send any token over plain `http`
except to a loopback address:

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

In `CONTRIBUTING.md`, replace:

```markdown
| `make lint` | `lint-go` and `lint-web` |
```

with:

```markdown
| `make lint-sh` | shellcheck of the release scripts and `install.sh` |
| `make lint` | `lint-go`, `lint-web` and `lint-sh` |
```

In `CONTRIBUTING.md`, replace:

```markdown
| `make test` | `test-go`, `test-web` and `test-adr` |
```

with:

```markdown
| `make test-release` | the release scripts' tests |
| `make test` | `test-go`, `test-web`, `test-adr` and `test-release` |
```

In `CONTRIBUTING.md`, replace:

```markdown
| `make check` | `lint`, `test`, `build`, `verify-build` and `vuln` |
| `make clean` | removes `bin/` and the built UI |
```

with:

```markdown
| `make check` | `lint`, `test`, `build`, `verify-build` and `vuln` |
| `make install-sh` | regenerates `install.sh` from `install.sh.in` with the trusted release keys |
| `make release VERSION=x.y.z` | the release tarballs, SBOM and `checksums.txt` in `bin/release/` |
| `make release-prep VERSION=x.y.z` | freezes the schema snapshots and rewrites `CHANGELOG.md` before a release tag |
| `make harness-builds` | the harness's test releases and `install.sh` with the harness key, in `bin/harness/` |
| `make clean` | removes `bin/` and the built UI |
```

In `CONTRIBUTING.md`, replace:

```markdown
and the controller socket in `$XDG_RUNTIME_DIR/darkpanel-dev/`. In `--dev` the
socket also accepts your own uid, so the break-glass commands need no `sudo`.
```

with:

```markdown
and the sockets in `$XDG_RUNTIME_DIR/darkpanel-dev/`. In `--dev` the sockets
also accept your own uid, so the break-glass commands need no `sudo`.
```

In `CONTRIBUTING.md`, replace:

```markdown
In another terminal, create the first admin through the socket and print the
web root:
```

with:

````markdown
`serve` runs the controller alone; its own host stays offline until an agent
connects. Add `--embedded-agent` to run the agent half in the same process
instead, as M1 did. To run a real agent beside it, enroll it with a token for
the local host and start it, each in another terminal:

```sh
go run ./cmd/darkpanel host enroll-local --dev
go run ./cmd/darkpanel agent enroll --dev
go run ./cmd/darkpanel agent run --dev
```

`agent enroll` asks for the token the first command printed. A development
agent never touches systemd, so its update actions fail.

Create the first admin through the socket and print the web root:
````


- [ ] **Step 2: Check the commands the text names**

Run: `GOTOOLCHAIN=local TMPDIR=/root/.cache/plan6-tmp/23 go build -o /root/.cache/plan6-tmp/23/dp ./cmd/darkpanel && D=/root/.cache/plan6-tmp/23/dp && for c in "system install" "host create" "host revoke" "host token" "host update-policy" "update rollout" "update rollback" "uninstall" "hostinfo summary get" "host enroll-local" "agent enroll" "agent run"; do $D $c --help >/dev/null || echo "missing: $c"; done; $D update rollout --help | grep -q -- --channel && $D update rollback --help | grep -q -- --yes && echo ok`
Expected: `ok` and no `missing:` line

- [ ] **Step 3: Run the documentation checks**

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
Expected: `check-adrs tests: 21 passed, 0 failed` and no `check-adrs:` error line

- [ ] **Step 4: Commit**

```bash
git add README.md CONTRIBUTING.md
git commit -m "docs: describe install, fleet and break-glass use"
```

---

### Task 24: Spec and ADR amendments

**Files:**
- Modify: `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`
- Modify: `docs/adr/0012-m2-contract-and-layout-additions.md`
- Modify: `docs/superpowers/notes/m2-services-amendments.md`

**Interfaces:**
- Consumes: the decisions of this plan's header and the behaviour Tasks 1-18 built (C2, C3, C4, C6, C10, C14, C15, C17, C18).
- Produces: nothing other tasks use.

The spec and ADR 0012 (still Proposed, so it may be edited) are corrected to what the M2 plans built: the actor `system:controller` (register row 69), the plan-5 decisions (row 87), the `unavailable` code and the plan-5 text debt (row 91), the rollout-cancel wording (row 90), the lock-key rule (row 23), the agent check rule (row 88) and the save lock (row 89). Plan 5's file is complete and is not rewritten; its amendments note records the corrections.

**Items:** 69, 87, 91
**Depends on:** Task 2, Task 3, Task 4, Task 6, Task 10, Task 14, Task 15, Task 17, Task 18
**Implementer:** dr-superpowers:impl-sonnet-low
**Evaluation:** files 1 - spec 0 - coupling 1 - risk 0 = 2

- [ ] **Step 1: Amend the documents**

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  `darkpanel` (ADR 0005) and no longer opens `agent.db` or the agent key.
```

with:

```markdown
  `darkpanel` (ADR 0005), refuses to run as root without `--dev` (it would
  create root-owned files the unit could not open), and no longer opens
  `agent.db` or the agent key or creates the agent's directories.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  valid only with `--dev`, so development and the existing end-to-end tests
  keep working. `darkpanel agent run --dev` runs the real daemon against the
  development layout. Production units never pass either flag.
```

with:

```markdown
  valid only with `--dev` and refused with `--agent-listen` (it opens no agent
  listener), so development and the existing end-to-end tests keep working.
  `darkpanel agent run --dev` runs the real daemon against the development
  layout, with an updater that never manages systemd units, so its update
  actions fail. Production units never pass either flag.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
    because the controller's own agent dials it.
```

with:

```markdown
    because the controller's own agent dials it, and it refuses an
    unspecified address.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  point leaves a complete old or a complete new generation.
```

with:

```markdown
  point leaves a complete old or a complete new generation. Writers of a
  generation (`agent enroll` and the daemon's renewal) hold an exclusive
  `flock` on `AgentTLSDir/save.lock` from reading `current` to the final
  fsync; a renewal whose session generation is no longer current writes
  nothing.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
| `core.host.token/v1` | irreversible | network | `hostId`, `controllerAddress` (optional) | `EnrollmentGrant` |
| `core.host.enroll-local/v1` | irreversible | controller socket (break-glass) | none | `EnrollmentGrant` for the local host, address `127.0.0.1:7443` |
```

with:

```markdown
| `core.host.token/v1` | irreversible, typed confirmation (the host name) | network | `hostId`, `controllerAddress` (optional) | `EnrollmentGrant` |
| `core.host.enroll-local/v1` | irreversible, typed confirmation (the host name) | controller socket (break-glass) | none | `EnrollmentGrant` for the local host, address `127.0.0.1:7443` |
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  `core.host.token` is refused for an enrolled, unrevoked host and replaces
  any earlier token of that host. `core.host.enroll-local` is allowed in every
```

with:

```markdown
  Host names are unique. `core.host.token` is refused for the local host and
  for an enrolled, unrevoked host, and replaces any earlier token of that
  host. `core.host.enroll-local` is allowed in every
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  interface by the same rule and watcher as the web listener (ADR 0006,
  `internal/controller/listen`), plus every `--agent-listen` address. It never
  binds all interfaces by default.
```

with:

```markdown
  interface by the same rule as the web listener and a watcher of its own
  (ADR 0006, `internal/controller/listen`), whatever the web `--listen` says,
  plus every `--agent-listen` address. It never binds all interfaces.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  - an unknown hash: the action version is unavailable on that host with the
    reason `agent schema unknown`.
```

with:

```markdown
  - an unknown or missing hash: the action version answers
    `module.unavailable` on that host with the reason `agent schema unknown`.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  `journal_epoch`, created with agent.db and replaced only when agent.db or
  its journal table is recreated; re-enrollment keeps it, so unacknowledged
```

with:

```markdown
  `journal_epoch`, created by the runtime when it opens agent.db and finds
  none (a migration that rebuilds `core_journal` would delete such a row, so
  no agent migration creates it), and replaced only when agent.db or its
  journal table is recreated; re-enrollment keeps it, so unacknowledged
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  runs when `--local` finds no agent socket (section 8).
```

with:

```markdown
  runs when `--local` finds no agent socket (section 8). `--local` dials the
  socket once before reading or planning; only a missing socket or a refused
  connection runs the fallback, which asks `Apply? [y/N]` for a mutating
  action unless `--yes` and needs no confirmation phrase, because without the
  agent there is no plan. `--host` and `--url` are refused with `--local`.
  The controller sends the actor `system:controller` as `Darkpanel-Principal`
  on its own calls (hello, change reads, preflight, the journal).
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
     flow with that token, which also repairs a revoked or lost local agent.
     Agent role: on 1, the token from standard input is required; on 2 with
```

with:

```markdown
     flow with that token, which also repairs a revoked or lost local agent.
     When the agent socket's `hello` reports `controllerConnected: true`, the
     credentials count as accepted and no handshake runs: a check is a
     session, and it would replace the running daemon's for a moment.
     Agent role: on 1, the token from standard input is required; on 2 with
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
    update recover`, `TimeoutStartSec=30s`,
```

with:

```markdown
    update recover` (the boot unit adds `--boot`), `TimeoutStartSec=30s`,
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  it. Handlers reach the function through `Env.DB`; the runner and `--direct`
```

with:

```markdown
  it. The registry refuses an agent-side revertible action that declares no
  lock key, so no revertible change can start, and stay unconfirmed, while
  an update holds `*`; a keyless compensable or irreversible change may. Handlers reach the function through `Env.DB`; the runner and `--direct`
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  `running` rollout to `draining` (or straight to `cancelled` when no host is
  `updating`), serialised with dispatch on the same database writer; the
```

with:

```markdown
  `running` rollout to `draining` (or straight to `cancelled` when no host was
  sent its update), serialised with dispatch on the same database writer; the
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  never comes back. A controller restart during its own host's update resumes
```

with:

```markdown
  never comes back. `core_rollouts` also stores `outcome` (the end state of a
  draining rollout), `change_id`, `error_code` and `error_message`, and
  `core_rollout_hosts` stores `apply_sent_at` (from then on the host may be
  updating) and `followed_at` (the start of the ten-minute limit). The update
  actions of a process without an updater, the controller's, answer
  `unavailable`, which fails the host. A controller restart during its own host's update resumes
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
`rollout.in_progress`.
```

with:

```markdown
`rollout.in_progress`, `unavailable` (503: a service that is shutting down or
that this process does not run).
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
`local:root@unenrolled`. New reserved top-level CLI name: `system` (`agent`,
```

with:

```markdown
`local:root@unenrolled`, `system:controller` (the controller's own agent API
calls). New reserved top-level CLI name: `system` (`agent`,
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  `core_rollout_hosts`); agent `0005_ownership_kinds.sql` (P4),
  `0006_journal_epoch.sql` (P10).
```

with:

```markdown
  `core_rollout_hosts`); agent `0005_ownership_kinds.sql` (P4); the agent's
  journal epoch is created by the runtime (section 5), not a migration.
```

In `docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md`, replace:

```markdown
  the work branch and dispatching `harness.yml`.

```

with:

```markdown
  the work branch and dispatching `harness.yml`.
- Ownership as built: P9 committed the `schemas/core.host.*` snapshots and
  the host line of `internal/cli/names_test.go` (the snapshot test fails as
  soon as an action changes without its snapshot); P12 also changed
  `internal/core/registry.go` (the lock-key rule), `internal/core/errcode/`
  (`unavailable`), `internal/agent/daemon/` (the save lock, the harnessbroken
  hook), `internal/update/actions.go` (`unavailable` without an updater) and
  `internal/controller/fleet/server.go`; the `harnessmigration` files live in
  `internal/core/store/`, which owns the embedded migrations.

```

In `docs/adr/0012-m2-contract-and-layout-additions.md`, replace:

```markdown
  keys, so it covers only changes whose action declares at least one; the
  registry does not require one, and a change with no lock keys starts while
  `*` is held. An action that must not run during an update declares a key.
```

with:

```markdown
  keys, so it covers only changes whose action declares at least one. The
  registry refuses an agent-side revertible action without a lock key, so no
  revertible change can start, and stay unconfirmed, while `*` is held; a
  keyless compensable or irreversible change may. Any other action that must
  not run during an update declares a key.
```

In `docs/adr/0012-m2-contract-and-layout-additions.md`, replace:

```markdown
  leaves a binary that can finish the rollback. The rollback and boot
  recovery units run it.
```

with:

```markdown
  leaves a binary that can finish the rollback. The rollback unit runs
  `update recover`, which restarts the services after a rollback; the boot
  recovery unit runs `update recover --boot`, ordered before the services,
  which restarts nothing.
```

In `docs/adr/0012-m2-contract-and-layout-additions.md`, replace:

```markdown
  `system:update-schedule` (scheduled updates) and `local:root@unenrolled`
  (the agent socket before enrollment) join the shared contracts' actors.
```

with:

```markdown
  `system:update-schedule` (scheduled updates), `local:root@unenrolled`
  (the agent socket before enrollment) and `system:controller` (the
  controller's own agent API calls: hello, change reads, preflight and the
  journal) join the shared contracts' actors.
```

In `docs/superpowers/notes/m2-services-amendments.md`, replace:

`````markdown
- `darkpanel agent enroll [--token-file <path>]`: the token from `daemon.ReadTokenFile(path, 0)` (`os.Getuid()` under `--dev`), else the hidden prompt or standard input (P12's), then `daemon.Enroll(ctx, p.AgentTLSDir, token)`. No unit restart is needed: a running daemon without a live session takes the new generation up on its next attempt (within `CredentialPoll` while unenrolled, after its backoff wait otherwise); a daemon with a live session takes it up only after that session ends. Revoke and `core.host.enroll-local` end the session, and `core.host.token` is refused for an enrolled host, so P12 must not promise or test a pickup bound for a daemon with a live session.
````
`````

with:

`````markdown
- `darkpanel agent enroll [--token-file <path>]`: the token from `daemon.ReadTokenFile(path, 0)` (`os.Getuid()` under `--dev`), else the hidden prompt or standard input (P12's), then `daemon.Enroll(ctx, p.AgentTLSDir, token)`. No unit restart is needed: a running daemon without a live session takes the new generation up on its next attempt (within `CredentialPoll` while unenrolled, after its backoff wait otherwise); a daemon with a live session takes it up only after that session ends. Revoke and `core.host.enroll-local` end the session, and `core.host.token` is refused for an enrolled host, so P12 must not promise or test a pickup bound for a daemon with a live session.
````

## After completion

Recorded with M2 plan 6, which closed register row 91; the plan file itself is
not rewritten.

- P11-C2, P11-C4, P11-C5 and the decisions about handed-off hosts describe the
  first rule: a host counts as handed its update once its plan id is stored.
  The merged code uses `apply_sent_at`, written just before the first apply: a
  host with a plan and no `apply_sent_at` was never sent its update, so a
  cancel without `force` ends the rollout at once and a draining rollout
  skips that host. The unenrolled check still fails a revoked or deleted host
  whatever its marker says (register row 90, deferred).
- P9-C1 gave two reasons for `coreactions.ProtocolVersion` beside the agent's
  `runtime.ProtocolVersion`; neither held once `coreactions` imported the agent
  runtime through `fleet`. Plan 6 removed the constant and
  `TestProtocolVersionMatchesTheAgent`.
- A renewal arriving after `fleet.Server.Close` answered `503 host.offline`;
  plan 6 added the code `unavailable` and answers that instead.
`````


- [ ] **Step 2: Run the ADR checks**

Run: `bash scripts/check-adrs.test.sh && bash scripts/check-adrs.sh --complete`
Expected: `check-adrs tests: 21 passed, 0 failed` and no `check-adrs:` error line

- [ ] **Step 3: Commit**

```bash
git add docs/superpowers/specs/2026-10-19-m2-fleet-lifecycle-design.md docs/adr/0012-m2-contract-and-layout-additions.md docs/superpowers/notes/m2-services-amendments.md
git commit -m "docs(spec): record the assembly amendments"
```

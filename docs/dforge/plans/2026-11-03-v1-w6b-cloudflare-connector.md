# v1 wave 6b: Cloudflare connector and tunnel exposure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 6b of the v1 completion spec: the `cloudflare` module's agent side (the `cloudflared` connector container run from a pinned digest with its token in a read-only file, `connector.stop`, `connector.list`, the log-secret source, and the `network.attach` and `network.detach` attachment claims), the controller composites `cloudflare.connector.deploy`, `cloudflare.expose.container` and `cloudflare.expose.remove`, the `/cloudflare/connectors` web page and the container's Expose via tunnel action, the M6 Cloudflare harness test with its workflow job, and the wave 3 review residuals of the Cloudflare hostname guard and the docker module (register rows 295 and 296).

**Architecture:** The connector half follows the npm module's agent-side shape: a separate dependency struct `agentDeps` on the existing `cf` value (tests replace it with an in-memory Docker Engine), agent tables in a new agent migration, one constructor per action returning a `core.ActionDef`, and one registration task that wires every constructor into `Manifest()` and commits the schema snapshots. Agent actions reach Docker only through `internal/platform/dockerapi` and the host file system only through `Env.Files.WriteManaged`/`RemoveManaged`; they journal every Engine object and file as the npm module does. The controller composites decide their steps from their input and the controller's `cloudflare_tunnels` and `cloudflare_hostnames` rows alone; the token travels from the `Internal` read step `cloudflare.tunnel.token` into `cloudflare.connector.run` through a sealed step reference.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`) (standard library, `internal/platform/dockerapi`, `internal/controller/cloud`), SQLite through `internal/core/store`, React 19 + TypeScript + TanStack Query + darkraise-ui (Vitest), the KVM harness in `test/harness` (GitHub Actions `harness.yml`).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1-2 | 3 | 4 | 5 | 6 | 7 | 8-10 | 11 | 12-13 | 14 | 15

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w6b-tmp/integ && export TMPDIR=/root/.cache/w6b-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w6b-tmp/wave && export TMPDIR=/root/.cache/w6b-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/modules/... ./internal/core/... ./schemas/... && timeout 300 go vet -tags harness ./test/... && timeout 1200 make lint-web test-web`

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 7.2 (the connector), the expose half of 7.3, the cloudflare rows of section 5 that 3c did not build (`connector.run`, `connector.stop`, `network.attach`, `network.detach`, `connector.deploy`, `expose.container`, `expose.remove`), the supported-target and attachment-claim rules of section 6 as 7.3 applies them, the cloudflare error codes of section 11, the M6 Verification row's tunnel items (real cloudflared reading its token file; inspect and logs never show the token; a default-bridge-only target refused in preview), and the register rows the tasks' **Items:** lines name. Out of scope: everything of waves 7, 8, 9 and 10 (backup, panel exposure and its fixed-address attach caller, docs, runbook) and the live demonstration (register row 55). Spec 5 lets the wave that builds a module add read actions: `cloudflare.connector.list` is such an addition.
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No test or command of this plan talks to `/var/run/docker.sock`, pulls or runs a container, runs `ufw`, `iptables`, `ip6tables`, `systemctl` or `docker`, writes under `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel`, or starts, stops or reloads any systemd unit. Every Docker effect in a unit test goes through the module's in-memory Engine (Contracts "Agent test host"); `productionAgentDeps` points the Docker client at a nonexistent socket when `testing.Testing()` is true, as `internal/modules/npm/deps.go` `productionNM` does. Real cloudflared runs only on harness VMs (Task 15). Only Task 1 reaches the network (public cloudflared sources and the Docker Hub registry API, read with `curl` into `$TMPDIR`, never through the Docker daemon); provider calls in unit tests go to the `internal/modules/cloudflare/fake` server on `127.0.0.1`.
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w6b-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. The root file system was 91 % full on 2026-11-03: before a task's first build, when `df -h /` shows 90 % or more, trim Go build cache entries older than a few hours (`go clean -cache` is acceptable). Web commands use `pnpm -C web exec vitest run <files>` for single files (`pnpm -C web vitest run` does not work here).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`; only the standard library and modules already required are used, and after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing. No Docker SDK and no Cloudflare SDK.
- After every task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. A task that edits `web/` also runs `pnpm -C web lint`, `pnpm -C web typecheck` and `pnpm -C web test`. Only Task 14 runs `make schemas` and commits snapshots: until Task 14, the new actions are not in `Manifest()`, so `modules.TestSnapshotsCurrent` stays green.
- Contract-check gate: after wave 1-2 merges, the controller reads Task 1's report `docs/dforge/notes/v1-w6b-cloudflare-spike.md`. Any entry under its `## Divergences` heading other than `none` stops execution until the integrator amends Contracts and every task that states the old value (a header amendment), before wave 3 starts. Tasks after wave 1-2 implement their text as written.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN. Quote the RED and GREEN output in the report, not a paraphrase. A task that skips the full-suite line above says so and why in its report.
- Frozen files (spec 1.12, and the earlier waves' modules this plan does not own), which no task edits: `internal/core/**`, `internal/platform/**`, `internal/modules/modules.go`, `internal/cli/**`, `internal/controller/**`, `cmd/darkpanel/**`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/src/app.tsx`, `web/src/shell.tsx`, `test/harness/pins.go`, `test/harness/images.json`, `Makefile`, `internal/modules/firewall/**`, `internal/modules/tailscale/**`, `internal/modules/tailnet/**`, `internal/modules/derp/**`, `internal/modules/npm/**`, `internal/modules/panel/**`; `internal/modules/docker/**` except Task 5's files; `internal/modules/cloudflare/**` and `web/src/modules/cloudflare/**` except the files a task's **Files:** block names; `test/harness/**` except the files Tasks 5 and 15 name. A task that finds a missing code, allowlist rule or extension point in a frozen file stops and reports it; it does not edit the file. Every error code this plan uses exists (Assumptions).
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; TCP only on `127.0.0.1` port 0 (`httptest` servers, the Cloudflare fake); `coretest.NewFakeClock` wherever a duration above one second is waited on; no fixed path outside a temporary directory; no global mutable state shared between tests of different tasks (the docker log-secret registry is written only by `init` functions and read-only in tests, which inject their own source). Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from Contracts "Test prefixes", except the shared helpers of Contracts "Agent test host", which only Task 3 declares.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean. Error strings start with the package name (`cloudflare:`, `docker:`, `harness:`). Refusals are `*errcode.Error` with client-safe details using only codes already in `internal/core/errcode/errcode.go` (`cloudflare.reserved`, `cloudflare.unsupported_target`, `cloudflare.connector_missing`, `cloud.conflict`, `cloud.not_connected`, `docker.engine_unavailable`, `docker.in_use`, `docker.owned`, `plan.state_changed`, `request.invalid_input`, `not_found`); a refusal that committed nothing is wrapped with `core.Definite`. No detail, diff line, `StateDoc`, effect, log line, audit entry, error or composite output carries a tunnel token; the token appears only in the sealed `token_ct` column, the 0400 token file, the sealed plan input of `cloudflare.connector.run` and the sealed step output of the `token` step.
- Lock keys: every agent mutation locks `cloudflare`; the reads take none; the composites take `composite:cloudflare` (the engine's coordination lock); their children take their own locks.
- Scopes: `cloudflare.connector.list` is `read`; agent mutations `operate`; every composite `admin` (the engine's default for controller-side actions).
- Values copied from the spec, used exactly: module id `cloudflare`; lock key `cloudflare`; container `darkpanel-cloudflared-<tunnel id>`; label `darkpanel.module=cloudflare`; restart policy `unless-stopped`; command `tunnel --no-autoupdate run --token-file /run/darkpanel/token`; token directory `/var/lib/darkpanel/agent/cloudflare-tokens` (kind `managed-dir`, root-owned 0700, flat names), file `<tunnel id>.token`, owner UID and GID 65532, mode 0400, bind-mounted read-only at `/run/darkpanel/token`; the token is never in the container's environment or command line; the image pinned by digest in `internal/modules/cloudflare/image.go` (`cloudflare/cloudflared` 2025.4.0 or later); the unsupported-target reason `connect the container to a user-defined network`; the expose service `http://<container>:<port>`; `cloudflare.expose.container` `unwind: reverse` with steps `network` then `hostname`; `cloudflare.expose.remove` `unwind: forward` with steps `hostname` then `network`; `cloudflare.connector.deploy` `unwind: reverse` with steps `tunnel` (only when no tunnel is named), `token`, `run`.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Tasks 5 and 15 touch `test/harness/` and Task 15 `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag, and no task runs `release.yml`.

## Contracts

- **Image** (Task 1, `internal/modules/cloudflare/image.go`): `const Image = "cloudflare/cloudflared:<version>@sha256:<64 hex>"`, the digest of the version's multi-arch index (amd64 and arm64 both present). `image_test.go` (prefix `TestIm`) checks the form: repository `cloudflare/cloudflared`, a version tag of at least `2025.4.0`, and a `@sha256:` digest of 64 lower-case hex digits. Constants beside it, also from Task 1: `const imageUID = 65532` (the image's user, the token file's owner and group).
- **Spike note** (Task 1): `docs/dforge/notes/v1-w6b-cloudflare-spike.md` with the headings `## Image`, `## Token file`, `## Token format`, `## Log lines`, `## Unauthorized token` and `## Divergences`. `## Token format` gives the token's form as cloudflared decodes it (the expected form: standard base64 of the JSON object `{"a":<account tag>,"t":<tunnel UUID>,"s":<base64 secret>}`) and a Go expression a test can use to build one. `## Log lines` gives an exact substring cloudflared logs after it has read and decoded the token file (for example one naming the tunnel id) and the substring it logs when the file is unreadable. `## Unauthorized token` says whether the container keeps running, restarts or exits when the edge refuses a well-formed but unknown token. Task 15 reads these values from the note.
- **Wire types** (Task 3, `internal/modules/cloudflare/connector_wire.go`; JSON names exact):

  ```go
  // ConnectorRunInput is the input of cloudflare.connector.run.
  type ConnectorRunInput struct {
      Tunnel string `json:"tunnel"`              // the tunnel id: a lower-case UUID
      Token  string `json:"token" secret:"true"` // the tunnel token from cloudflare.tunnel.token
  }
  // Connector is one connector on a host.
  type Connector struct {
      Tunnel      string `json:"tunnel"`
      Container   string `json:"container"`   // darkpanel-cloudflared-<tunnel>
      ContainerID string `json:"containerId"`
      State       string `json:"state"`       // the Engine's container state; "missing" when the container is gone
  }
  // ConnectorStopInput is the input of cloudflare.connector.stop.
  type ConnectorStopInput struct{ Tunnel string `json:"tunnel"` }
  // ConnectorStopped is the output of cloudflare.connector.stop.
  type ConnectorStopped struct {
      Tunnel  string `json:"tunnel"`
      Removed bool   `json:"removed"` // false when nothing was left to remove
  }
  // ConnectorListInput is the input of cloudflare.connector.list.
  type ConnectorListInput struct{}
  // ConnectorList is the output of cloudflare.connector.list, ordered by tunnel.
  type ConnectorList struct{ Connectors []Connector `json:"connectors"` }
  // NetworkAttachInput is the input of cloudflare.network.attach.
  type NetworkAttachInput struct {
      Tunnel   string     `json:"tunnel"`
      Network  string     `json:"network"`
      Claimant string     `json:"claimant"`
      Target   string     `json:"target,omitempty"`   // a container that must be on Network
      Address  netip.Addr `json:"address,omitzero"`   // a fixed address for the connector on Network
  }
  // NetworkAttachment is the output of cloudflare.network.attach and cloudflare.network.detach.
  type NetworkAttachment struct {
      Tunnel      string `json:"tunnel"`
      Network     string `json:"network"`
      Claimant    string `json:"claimant"`
      Preexisting bool   `json:"preexisting"`
      Attached    bool   `json:"attached"` // whether the connector is on Network after the change
  }
  // NetworkDetachInput is the input of cloudflare.network.detach; Network "" releases every claim of Claimant on the tunnel's connector.
  type NetworkDetachInput struct {
      Tunnel   string `json:"tunnel"`
      Network  string `json:"network"`
      Claimant string `json:"claimant"`
  }
  // ConnectorDeployInput is the input of cloudflare.connector.deploy; Tunnel "" creates a tunnel.
  type ConnectorDeployInput struct {
      HostID string `json:"hostId"`
      Tunnel string `json:"tunnel,omitempty"` // id or name of a tunnel Darkpanel manages
  }
  // ConnectorDeployOutput is the output of cloudflare.connector.deploy.
  type ConnectorDeployOutput struct {
      Tunnel    string    `json:"tunnel"`  // the tunnel id
      Created   bool      `json:"created"` // whether the composite created the tunnel
      Connector Connector `json:"connector"`
  }
  // ExposeInput is the input of cloudflare.expose.container.
  type ExposeInput struct {
      HostID    string `json:"hostId"`
      Tunnel    string `json:"tunnel"`    // id or name
      Container string `json:"container"`
      Port      int    `json:"port" jsonschema:"minimum=1,maximum=65535"`
      Network   string `json:"network"`   // a user-defined bridge network the container is on
      Hostname  string `json:"hostname"`
  }
  // ExposeOutput is the output of cloudflare.expose.container.
  type ExposeOutput struct {
      Hostname string `json:"hostname"`
      Service  string `json:"service"`
      Network  string `json:"network"`
  }
  // ExposeRemoveInput is the input of cloudflare.expose.remove.
  type ExposeRemoveInput struct {
      HostID   string `json:"hostId"`
      Hostname string `json:"hostname"`
  }
  // ExposeRemoveOutput is the output of cloudflare.expose.remove.
  type ExposeRemoveOutput struct{ Hostname string `json:"hostname"` }
  ```

  `connector_wire_test.go` (prefix `TestCw`) checks the JSON names and that `ConnectorRunInput.Token` carries `secret:"true"`.
- **Agent tables** (Task 3, `internal/modules/cloudflare/migrations/agent/0001_cloudflare.sql`):

  ```sql
  CREATE TABLE cloudflare_connectors (
      tunnel_id    TEXT PRIMARY KEY,
      container_id TEXT NOT NULL,
      token_ct     BLOB NOT NULL,
      change_id    TEXT NOT NULL,
      created_at   TEXT NOT NULL
  ) STRICT;
  CREATE TABLE cloudflare_attachments (
      tunnel_id   TEXT NOT NULL,
      network     TEXT NOT NULL,
      claimant    TEXT NOT NULL,
      change_id   TEXT NOT NULL,
      preexisting INTEGER NOT NULL CHECK (preexisting IN (0, 1)),
      PRIMARY KEY (tunnel_id, network, claimant)
  ) STRICT;
  ```

  The token is sealed with associated data `"cloudflare_connectors.token_ct.<tunnel id>"` (constant `connectorSealAD(tunnel string) string`). `store.go`'s embed directive becomes `//go:embed migrations/controller/*.sql migrations/agent/*.sql`, and `agentMigrations() fs.FS` (in `connector_store.go`) returns `migrations/agent`. Times use the existing `storeFormatTime`/`storeParseTime`.
- **Agent store** (Task 3, `connector_store.go`): `type connectorRow struct { TunnelID, ContainerID, ChangeID string; TokenCT []byte; CreatedAt time.Time }`; `type claimRow struct { TunnelID, Network, Claimant, ChangeID string; Preexisting bool }`; `loadConnector(ctx, q queryer, tunnelID string) (connectorRow, bool, error)`; `listConnectors(ctx, q queryer) ([]connectorRow, error)` ordered by tunnel id; `connectorByContainer(ctx, q queryer, containerID string) (connectorRow, bool, error)`; `insertConnector(ctx, tx *sql.Tx, r connectorRow) error`; `deleteConnector(ctx, tx *sql.Tx, tunnelID string) error`; `connectorClaims(ctx, q queryer, tunnelID, network string) ([]claimRow, error)` (network "" for every network of the tunnel, ordered by network then claimant); `insertClaim(ctx, tx *sql.Tx, c claimRow) error`; `deleteClaim(ctx, tx *sql.Tx, tunnelID, network, claimant string) error`.
- **Names** (Task 3, `connector_deps.go`): `const tokenDir = "/var/lib/darkpanel/agent/cloudflare-tokens"`; `const tokenMount = "/run/darkpanel/token"`; `func connectorName(tunnel string) string` returns `"darkpanel-cloudflared-" + tunnel`; `func tokenFileName(tunnel string) string` returns `tunnel + ".token"`; `func tunnelIDProblem(id string) string` is `""` for a lower-case UUID (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`) and a message otherwise; `func claimantProblem(c string) string` accepts 1 to 300 bytes without white space or control characters; `var networkNamePattern = regexp.MustCompile("^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$")`. The manifest's `OwnedPaths` gains `{Path: tokenDir, Kind: core.OwnedManagedDir, Pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\\.token$"}` and `Migrations[core.SideAgent] = agentMigrations()` (Task 3 edits `cloudflare.go` for both; `actions()` is untouched until Task 14).
- **Agent dependencies** (Task 3): `client.go`'s `cf` becomes `struct{ d deps; a agentDeps }` and `productionCF()` sets `a: productionAgentDeps()`. In `connector_deps.go`: `type agentDeps struct { docker connectorDocker }`; `type connectorDocker interface` with exactly `InspectContainer`, `ListContainers`, `CreateContainer`, `StartContainer`, `StopContainer`, `RemoveContainer`, `InspectNetwork`, `ConnectNetwork`, `DisconnectNetwork`, `InspectImage` and `PullImage(ctx, ref) (pullStream, error)`, with the `dockerapi.Client` signatures (see `internal/modules/npm/deps.go` `dockerAPI`); `type pullStream interface { Next() (dockerapi.PullMessage, error); Close() error }`; `productionAgentDeps()` uses `dockerapi.DefaultSocket`, or `/nonexistent/darkpanel/docker.sock` when `testing.Testing()`; `engineCoded(err error) error` maps an unreachable Engine to `docker.engine_unavailable`, as npm's does. Task 6 adds the field `readFile func(name string) ([]byte, error)` to `agentDeps` (`os.ReadFile` in `productionAgentDeps`), because `Env.Files` has no read method and the token file's effect is observed by reading it; Task 6's tests wire the field themselves.
- **Agent test host** (Task 3, `connector_testhost_test.go`; only Task 3 declares these): `type agHost struct` with an agent database (core and cloudflare agent migrations), `*coretest.FakeClock`, an in-memory effects journal, an in-memory `FileWriter` that records `WriteManaged`/`RemoveManaged` calls with data, mode, uid and gid (`agFiles`, with `file(dir, name string) (agFile, bool)`), and an in-memory Engine `agDocker` implementing `connectorDocker` (containers with labels, image, `Cmd`, `Env`, mounts, restart policy, state and network endpoints; networks with driver; images; `Fail(op string, err error)` to inject one failure; `calls() []string`); `newAGHost(t *testing.T) *agHost`; `(h *agHost) cf() cf` (a `cf` whose `a.docker` is `h.docker`); `(h *agHost) env(changeID string) core.Env` (operator principal, `Files: h.files`, `Sealer: coretest.Sealer{}`, `Effects` only when changeID is set); `agContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON`, `agOnNetwork(network, ip string)`, `agNetwork(name, driver string) dockerapi.Network`; `agToken(tunnel string) string` builds a well-formed token per the spike note's `## Token format`. In detail:
  - `agHost` has the fields `docker *agDocker` and `files *agFiles`, which tests read directly.
  - `agDocker` seeding, each storing a deep copy; containers and networks are then found by id or by name by every `connectorDocker` method: `(d *agDocker) putContainer(c dockerapi.ContainerJSON)` keyed by `c.ID`, replacing a container with the same id; `(d *agDocker) putNetwork(n dockerapi.Network)` keyed by `n.ID`; `(d *agDocker) putImage(ref string)` makes `InspectImage(ref)` succeed.
  - `agDocker` behaviour (as `thDocker` in `internal/modules/npm/testhost_test.go`): `calls()` returns the `connectorDocker` method names (for example `RemoveContainer`) in call order, a failed call included; `Fail(op, err)` queues err as the answer of the next call of method op, which then changes nothing. `InspectContainer`, `InspectNetwork` and `InspectImage` of an absent object return a `*dockerapi.APIError` with StatusCode 404 (`dockerapi.IsNotFound` is true). `PullImage(ref)` records the image and returns a stream whose `Next` ends in `io.EOF`. `CreateContainer` returns a 404 `*dockerapi.APIError` when `cfg.Image` is not a known image and a 409 one when the name is taken; the stored container keeps the request's `Config` and `HostConfig` unchanged and starts with `State.Status == "created"` and `Running == false`. `StartContainer` sets `State.Running = true` and `State.Status = "running"`; `StopContainer` sets `Running = false` and `Status = "exited"`; `RemoveContainer` deletes the container. `ConnectNetwork(network, container, ep)` stores the endpoint under `NetworkSettings.Networks[<network name>]` with `NetworkID` set and `IPAMConfig` as given, and answers a 403 `*dockerapi.APIError` when the container is already on it; `DisconnectNetwork` removes it and answers a 403 one when the container is not on it.
  - Builders: `agOnNetwork(network, ip string)` returns `func(*dockerapi.ContainerJSON)` (the option type `agContainer` takes) and adds an endpoint under `NetworkSettings.Networks[network]`; `agNetwork(name, driver string)` returns a `dockerapi.Network` with a unique `ID`, the `Name` and `Driver`, and no containers.
  - `agHost.env(changeID)` gives `Env.DB` (writable and readable), `Env.Sealer`, `Env.Clock` and, when changeID is set, `Env.Effects`, whose `List` and `Secret` the tests read.
- **Agent effects** (Task 6 creates `internal/modules/cloudflare/connector_effects.go`, mirroring `internal/modules/npm/effects.go`; Tasks 8, 12 and 13 consume it): nouns `nounTokenFile = "cloudflare.tokenfile"`, `nounContainer = "cloudflare.container"`, `nounConnector = "cloudflare.connector"` (Task 8 adds `nounClaim` and `nounEndpoint` in its own file); `const engineCommitWait = 2 * time.Minute`; `func resourceOf(noun, identity string) string` (`noun + ":" + identity`); `func identityOf(resource string) string`; `func connectorEffect(kind core.EffectKind, resource string, before json.RawMessage, wait time.Duration) core.Effect` (not Local; a Create gets the null before-state and, when its identity starts with `?`, the recovery identity `{Kind: "name", Value: <identity without ?>}`); `func refused(err error) bool` (a 4xx `*dockerapi.APIError`); `func (c cf) journal(ctx context.Context, env core.Env, e core.Effect, observe func(context.Context, core.Env, core.Effect) (core.Observation, error), do func(ctx context.Context) (resource string, err error)) error` (npm's `run`: `Intend`, the call, `Skipped` plus a Definite error on `refused` or a Definite error, the effect left intended on any other failure, otherwise `Made` with the observed state of the resource `do` names, or of `e.Resource` when it names none; a resource still starting with `?` is an error); `func nullState() core.StateDoc`; `func named(state json.RawMessage) core.Observation`. A local effect (the token file, a row, a claim) is built by the caller from `connectorEffect` with `Local` and `InverseLocal` set and `After` filled.
- **Agent actions** (names exact; `Side: core.SideAgent`, `LockKeys: []string{"cloudflare"}` on the mutations):
  - `cloudflare.connector.run` v1 (Task 6, `connector_run.go`): `ClassCompensable`, an `Undoer` (its undo removes the container, the token file and the row), `LongRunning`, `ApplyTimeout` 10 minutes; `DefineMutating[ConnectorRunInput, Connector]`. Plan refuses (Definite `request.invalid_input`) a bad tunnel id or an empty token, and when a `cloudflare_connectors` row or a container named `connectorName(tunnel)` already exists. Apply pulls `Image` when `InspectImage` reports it absent, writes the token file with `Env.Files.WriteManaged(ctx, "cloudflare", tokenDir, tokenFileName(t), token, 0o400, imageUID, imageUID)`, inserts the row with the sealed token, creates and starts the container (label, restart policy, command, read-only bind mount `<tokenDir>/<file>:/run/darkpanel/token`, no environment beyond the image's), and journals the file, the row and the container. Verify checks the container's image, label, command, mount and a state of `running` or `restarting`. Shared helpers Task 12 consumes: `removeConnector(ctx context.Context, env core.Env, c cf, tunnel string) (bool, error)` (container, file, row; absent parts are not errors).
  - `cloudflare.connector.stop` v1 (Task 12, `connector_stop.go`): `ClassCompensable`, `CompensatedBy: "cloudflare.connector.run"`, `Converges: true`; `DefineMutating[ConnectorStopInput, ConnectorStopped]`; refuses (Definite `docker.in_use`, detail naming the claimants) while `cloudflare_attachments` holds a claim for the tunnel; otherwise removes through `removeConnector`. Run again on an absent connector, it plans an empty diff.
  - `cloudflare.connector.list` v1 (Task 7, `connector_list.go`): `ClassRead`, `DefineRead[ConnectorListInput, ConnectorList]`: every row with its container's state (`missing` on a not-found inspect).
  - Log-secret source (Task 7, `connector_logsecrets.go`): an `init` calls `docker.RegisterLogSecrets(ID, connectorLogSecrets)`; `connectorLogSecrets(ctx, env, containerID)` returns the opened token of the row whose `container_id` equals containerID, and no secret (nil, nil) for any other container.
  - `cloudflare.network.attach` v1 (Task 8, `connector_network.go`): `ClassCompensable` with an `Undoer` that drops only the claim this change made and disconnects only when no claim is left and the attachment was not pre-existing; `DefineMutating[NetworkAttachInput, NetworkAttachment]`. Plan refuses: no connector row or container for the tunnel → Definite `cloudflare.connector_missing`; a network that is not a user-defined bridge, or a `Target` not attached to `Network` or attached only to `bridge`, `host`, `none` or a non-bridge driver → Definite `cloudflare.unsupported_target` with the reason `connect the container to a user-defined network`; a bad name or claimant → Definite `request.invalid_input`. Apply connects (with `Address` as the endpoint's IPv4 or IPv6 address when set) only when the connector is not already on the network, recording `preexisting: true` when it was there before the first claim, and inserts the claim. Shared names Task 13 consumes, all in `connector_network.go`: constants `nounClaim = "cloudflare.claim"` and `nounEndpoint = "cloudflare.endpoint"`; effect resources `cloudflare.claim:<tunnel>|<network>|<claimant>` (creation form `cloudflare.claim:?<tunnel>|<network>|<claimant>`) and `cloudflare.endpoint:<tunnel>|<network>` (creation form with `?`), the container always being `connectorName(<tunnel>)`; `type claimDoc struct` with JSON members `tunnel`, `network`, `claimant`, `preexisting`, `changeId` in that order, rendered by `claimDocument(c claimRow) (json.RawMessage, error)`; and `type networkClaims struct{ c cf }`, which implements `Observe` and `Inverse` for all four effect kinds the attach and detach journal: claim create (undo drops the claim when its change id is the effect's), claim delete (undo restores it from `Before`), endpoint modify without `InverseRecovery` (undo disconnects, refused while a claim of another change is left) and endpoint modify with `InverseRecovery` set (undo reconnects with a dynamic address and returns the endpoint resource). Task 13's detach handler embeds `networkClaims`.
  - `cloudflare.network.detach` v1 (Task 13, `connector_network_detach.go`): `ClassCompensable` with an `Undoer` (restores the claims and attachment it removed), `Converges: true`; `DefineMutating[NetworkDetachInput, NetworkAttachment]`; removes the claimant's claims (on `Network`, or on every network when it is `""`) and disconnects the connector from a network only when no claim is left there and the attachment was not pre-existing. An absent claim or connector is converged, not an error.
- **Controller composites** (`Side` controller, class composite, engine-held `composite:cloudflare`):
  - `cloudflare.connector.deploy` v1 (Task 9, `connector_deploy.go`), `UnwindReverse`, `DefineComposite[ConnectorDeployInput, ConnectorDeployOutput]`. `Steps`: with `Tunnel` empty — `tunnel` (`cloudflare.tunnel.create/v1`, input `{}`), `token` (`cloudflare.tunnel.token/v1`, input `{"tunnel":"${steps.tunnel.output.id}"}`), `run` (host `HostID`, `cloudflare.connector.run/v1`, input `{"tunnel":"${steps.tunnel.output.id}","token":"${steps.token.output.token}"}`); with `Tunnel` named — `loadTunnel` resolves it to its id (Definite `not_found` when unknown) and the steps are `token` (`{"tunnel":"<id>"}`) and `run` (`{"tunnel":"<id>","token":"${steps.token.output.token}"}`). Each step follows the one before it (`After`). `Output` takes `Connector` from the `run` output and the tunnel id from the `tunnel` output or the resolved id.
  - `cloudflare.expose.container` v1 (Task 10, `expose.go`), `UnwindReverse`, `DefineComposite[ExposeInput, ExposeOutput]`. `Steps` validates `Container` and `Network` against `networkNamePattern`, `Port` 1-65535 and `Hostname` non-empty (Definite `request.invalid_input`), resolves `Tunnel` through `loadTunnel` (Definite `not_found`), then: `network` (host `HostID`, `cloudflare.network.attach/v1`, input `{"tunnel":"<id>","network":"${input.network}","claimant":"expose:${input.hostname}","target":"${input.container}"}`), `hostname` (controller, `cloudflare.hostname.add/v1`, input `{"tunnel":"<id>","hostname":"${input.hostname}","service":"http://<container>:<port>"}` with the validated container name and port written literally, `After: network`). `Output` gives the hostname, the service and the network.
  - `cloudflare.expose.remove` v1 (Task 10, `expose.go`), `UnwindForward`, `DefineComposite[ExposeRemoveInput, ExposeRemoveOutput]`. `Steps` reads the `cloudflare_hostnames` row through `loadHostname` (Definite `not_found` when absent) for its tunnel id, then: `hostname` (controller, `cloudflare.hostname.remove/v1`, input `{"hostname":"${input.hostname}"}`), `network` (host `HostID`, `cloudflare.network.detach/v1`, input `{"tunnel":"<tunnel id>","network":"","claimant":"expose:${input.hostname}"}`, `After: hostname`).
  - The expose claimant is `"expose:" + <hostname>` (constant `exposeClaimantPrefix = "expose:"` in `expose.go`).
- **Registration** (Task 14): `cloudflare.go` `actions()` appends, in this order, `c.connectorRun()`, `c.connectorStop()`, `c.connectorList()`, `c.networkAttach()`, `c.networkDetach()`, `c.connectorDeploy()`, `c.exposeContainer()`, `c.exposeRemove()` (each a method `func (c cf) <name>() core.ActionDef` defined by the task named above); `register_test.go` gains a row per action; `make schemas` regenerates the snapshots under `schemas/`.
- **Web** (wire JSON as above; React files under `web/src/modules/cloudflare/`):
  - Task 2 creates `connector-types.ts` exporting `interface Connector { tunnel: string; container: string; containerId: string; state: string }`, `interface ConnectorList { connectors: Connector[] }`, `const CLOUDFLARE_ACTION_VERSION = 1` if `types.ts` does not already export one (reuse it when it does), `connectorListKey(hostId: string)` (a TanStack query key), and `function tunnelLabel(id: string, tunnels: { id: string; name: string }[]): string` (the tunnel's name, else its id). It creates `connectors-page.tsx` exporting `ConnectorsPage`, and adds the `/cloudflare/connectors` route and a "Connectors" link (between Tunnels and Hostnames) to `global.tsx`.
  - Task 11 replaces the body of `container-actions.tsx` `ExposeAction` (the registered slot `cloudflare.expose`, `web/src/modules/registry.ts`) and creates `expose-dialog.tsx` exporting `ExposeTunnelForm`; it consumes Task 2's `connector-types.ts`.
- **Harness** (Task 15): `test/harness/m6_cloudflare_test.go` declares `var m6cFlag = flag.Bool("m6c", false, "run TestM6Cloudflare; needs -os and -m2, and is a go test run of its own")` and `func TestM6Cloudflare(t *testing.T)`, every other identifier prefixed `m6c`; `.github/workflows/harness.yml` gains job `vm-m6c` modelled on `vm-m6n` (`go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/ -args -os ${{ matrix.os }} -m6c ...`, logs artifact `logs-m6c-${{ matrix.os }}`); `test/harness/m6cw_workflow_unit_test.go` (prefix `TestM6cwUnit`) checks that job as `m6nw_workflow_unit_test.go` checks `vm-m6n`.
- **Test prefixes** (top-level test functions `Test<Prefix>…`, helpers `<prefix>…` in lower case): Task 1 `Im`/`im`; Task 3 `Cw`/`cw`, `Cs`/`cs` for the store, `Ag`/`ag` for the test host and deps; Task 4 `Rs`/`rs`; Task 5 keeps each touched test file's existing prefix and adds `Rd`/`rd` where it needs a new one; Task 6 `Cr`/`cr`; Task 7 `Cl`/`cl`; Task 8 `Na`/`na`; Task 9 `Cd`/`cd`; Task 10 `Ex`/`ex`; Task 12 `Cp`/`cp`; Task 13 `Nd`/`nd`; Task 14 extends `TestReg`; Task 15 `M6c`/`m6c` and `M6cw`. Web tests name their `describe` blocks after their component.

## Assumptions (evidence)

- The `cloudflare` module is controller-only today: `internal/modules/cloudflare/cloudflare.go` declares eleven controller actions and only `Migrations[core.SideController]` (read 2026-11-03); `cloudflare.tunnel.token` already exists as an `Internal` read returning `core.Revealed[TunnelToken]` (`tunnel_read.go:76-107`), and `cloudflare.hostname.add`/`remove` exist with the reserved-service checks and `CompensatedBy` (`hostname_add.go:135`, `hostname_remove.go:26`). `internal/cli/cloudflare.go` returns no command and every action gets a generated CLI command, so no CLI file changes. `web/src/modules/registry.ts:40-50` already registers the container action `cloudflare.expose` rendering `container-actions.tsx` `ExposeAction`, which returns `null`.
- A composite's `Internal` read step is dispatched through `ReadInternal` (`internal/controller/dispatch/children.go:32-39`), and its output is sealed in the step row (`internal/core/lifecycle/composite_run.go:606-629`); a later step reads it through `${steps.<id>.output.<path>}`, which may sit inside a longer string (`internal/modules/npm/composite_deploy_expose.go:214`, `internal/modules/tailnet/join.go:70`). The preview dry-runs only steps without an output reference (spec 1.3), so `expose.container`'s `network` step is previewed and its unsupported-target refusal shows in the preview.
- `Env.Files.WriteManaged` writes a flat file in a declared managed directory with a given mode, UID and GID (`internal/core/contract.go:682`, `internal/platform/files_managed.go:22-46`), so the 65532-owned 0400 token file needs no new platform code.
- The error codes `cloudflare.reserved`, `cloudflare.unsupported_target` and `cloudflare.connector_missing` exist (`internal/core/errcode/errcode.go:111-113,223-225`), as do `docker.in_use`, `docker.owned`, `docker.engine_unavailable`, `plan.state_changed`, `request.invalid_input`, `not_found`, `cloud.conflict` and `cloud.not_connected`.
- `docker.RegisterLogSecrets` exists and is called from `init` (`internal/modules/docker/logsecrets.go:17-24`, `internal/modules/npm/reveal.go:13`); the cloudflare half is register row 324.
- `CompensatedBy` names an operator-invoked compensating action and needs no handler (spec 1.2; `internal/core/contract.go:221`), so `connector.stop` declares `CompensatedBy: cloudflare.connector.run` without building its input.
- Ruling: the harness cannot point the controller at a fake Cloudflare API: the provider base URL is a constant (`internal/modules/cloudflare/client.go:33`), harness binaries take build tags only from the frozen `Makefile` (`HARNESS_BUILDS`, lines 21-26), and the harness lab drives the real controller. The Verification row's "connector status from the fake API" is therefore proven by module tests on `internal/modules/cloudflare/fake` (`tunnel_read_test.go`, which 3c built), and `TestM6Cloudflare` covers the host half: it applies `cloudflare.connector.run` directly on the agent through the controller API with a well-formed token built per the spike note, then checks the container, the file, inspect, logs, `connector.list`, the attach and detach claims (including the default-bridge refusal) and the undo. `connector.deploy` and the expose composites, which need the Cloudflare API, are covered by unit tests; their live run is register row 55.
- Ruling: `cloudflare.connector.run` refuses an existing connector rather than replacing it, so its undo never removes a connector another change made; replacing a connector is `connector.stop` then `connector.run`.
- Ruling: `cloudflare.connector.stop` refuses while the tunnel's connector holds attachment claims, because stopping it would silently break the exposures those claims serve; the operator removes the exposures first (spec 6's claims rule, applied to the connector's own removal).
- Ruling: `cloudflare.expose.container` takes `Network` in its input (the web fills it with the container's first user-defined bridge network in name order, as the npm Expose action does), because a composite's `Steps` reads only its input and the controller database (`internal/core/contract.go:303-306`); the agent-side attach plan enforces the supported-target rule.
- Ruling for register row 296's lookup case (Task 4): `cloudflare.hostname.add`'s reserved check treats a not-found lookup as "not reserved" (so a container name, which the controller cannot resolve, still passes, which `expose.container` needs) and refuses with `cloudflare.reserved` on any other lookup error; register row 295's ruling (hostname.remove refuses only IP-literal and legacy-numeric forms in the panel range, with no DNS lookup) is quoted in the row.
- The cloudflared image's digest, its user (UID 65532 per the spec's Versions table), the `--token-file` flag in the pinned version, the token's format, the log lines that show the file was read, and the container's behaviour on an unknown token: unverified — Task 1 verifies them.
- The harness VMs run Docker 29.9.1 on all four OSes (`test/harness/images.json`, `pins.go:26`) and pull images from Docker Hub, as `TestM6NPM` does (`m6_npm_test.go` `prepare`); `vm-m6n` is the job pattern (`.github/workflows/harness.yml:427-490`).
- No task carries an **Executor:** line: the owner answered the executor-lane offer (agy) with "Claude only" on 2026-11-03, so plan-lint's lane-eligible warnings are expected.
- Scoring follows the programme's convention (plans w1-w6a): the files axis counts kinds of file (Go source, Go test, SQL, TypeScript, workflow YAML, generated schema, Markdown note), not instances.

## Task index

1. Spike: cloudflared image pin and token-file contract
2. web: connectors page
3. cloudflare: agent connector seam, wire types, tables and test host
4. cloudflare: hostname guard residuals
5. docker and harness: wave 3 residuals
6. cloudflare: connector.run
7. cloudflare: connector.list and the log-secret source
8. cloudflare: network.attach claims
9. cloudflare: connector.deploy composite
10. cloudflare: expose.container and expose.remove composites
11. web: Expose via tunnel container action
12. cloudflare: connector.stop
13. cloudflare: network.detach
14. cloudflare: register the actions and schemas
15. harness: TestM6Cloudflare and the vm-m6c job

---

### Task 1: Spike: cloudflared image pin and token-file contract

**Files:**
- Create: `internal/modules/cloudflare/image.go`
- Create: `internal/modules/cloudflare/image_test.go`
- Create: `docs/dforge/notes/v1-w6b-cloudflare-spike.md`

**Interfaces:**
- Consumes: nothing from an earlier task. Existing: the `cloudflare` package (`package cloudflare`).
- Produces: Contracts "Image" (`const Image`, `const imageUID = 65532` in `image.go`, and `TestImPinned`); the spike note `docs/dforge/notes/v1-w6b-cloudflare-spike.md` with Contracts "Spike note" headings, whose `## Divergences` heading the contract-check gate reads and whose `## Token format`, `## Log lines` and `## Unauthorized token` sections Task 15 reads (Task 3's `agToken` is built from `## Token format`).

**Items:** 34

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

This task reaches the network (it is the only task of the plan that does): the public cloudflared sources on `raw.githubusercontent.com` and `api.github.com`, the Docker Hub tag list and the Docker registry API, all read with `curl` into `$TMPDIR`. It never talks to the Docker daemon, never pulls or runs an image, and never connects to `/var/run/docker.sock`. It edits no file other than the three above; in particular it does not edit the spec, and a statement the sources refute goes under `## Divergences` for the integrator to amend.

- [ ] **Step 1: Write the failing test**

Create `internal/modules/cloudflare/image_test.go`:

```go
package cloudflare

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

var imPinnedForm = regexp.MustCompile(`^cloudflare/cloudflared:([0-9]{4})\.([0-9]{1,2})\.([0-9]+)@sha256:([0-9a-f]{64})$`)

// TestImPinned: the image the connector runs is a release of 2025.4.0 or later, pinned by the digest of its multi-arch
// index and never by a moving tag; the user it runs as is 65532.
func TestImPinned(t *testing.T) {
	m := imPinnedForm.FindStringSubmatch(Image)
	if m == nil {
		t.Fatalf("Image = %q, want cloudflare/cloudflared:<year>.<month>.<patch>@sha256:<64 hex digits>", Image)
	}
	if strings.Trim(m[4], "0") == "" {
		t.Fatalf("Image = %q, want the digest of the release's index, not a placeholder of zeros", Image)
	}
	var got [3]int
	for i := range got {
		n, err := strconv.Atoi(m[i+1])
		if err != nil {
			t.Fatal(err)
		}
		got[i] = n
	}
	for i, floor := range [3]int{2025, 4, 0} {
		if got[i] > floor {
			break
		}
		if got[i] < floor {
			t.Fatalf("Image = %q, want version 2025.4.0 or later", Image)
		}
	}
	if imageUID != 65532 {
		t.Errorf("imageUID = %d, want 65532", imageUID)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w6b-tmp/1 && export TMPDIR=/root/.cache/w6b-tmp/1 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestImPinned' ./internal/modules/cloudflare/
```

Expected: FAIL to build, `undefined: Image` and `undefined: imageUID`. Quote this output in the report.

- [ ] **Step 3: Run the spike and record it**

Work in `S=$TMPDIR/cloudflared-spike` (`mkdir -p "$S"`); every fetched file lands there and is data, not code: read it with `rg`/`sed -n`, and run any Python that reads it as `python3 -I -c ...`, never from inside `$S`. Use `curl -fsS` with `timeout 60` and record each URL's HTTP status (`-w '%{http_code}'`) for the note's `## Sources` list, as `docs/dforge/notes/v1-w6a-npm-spike.md` lists its sources. A file that answers 404 is looked up again in the tag's tree (`https://api.github.com/repos/cloudflare/cloudflared/git/trees/<tag>?recursive=1`) and the moved path is listed. Raw files come from `https://raw.githubusercontent.com/cloudflare/cloudflared/<tag>/<path>`; a cloudflared tag is the bare version (`2025.4.0`), and the first thing to confirm is that this holds for the chosen release.

1. **Choose the release and read its digest.**
   1. List the tags: `https://hub.docker.com/v2/repositories/cloudflare/cloudflared/tags?page_size=100&ordering=last_updated`. Only tags of the form `<year>.<month>.<patch>` qualify (not `latest`, a date-stamped build or an architecture-suffixed tag), and the version must be 2025.4.0 or later (the first release with `tunnel run --token-file`, per the spec's Versions table). Cross-check the list against `https://api.github.com/repos/cloudflare/cloudflared/releases?per_page=30`.
   2. Start at the newest qualifying tag. It is the pin when every check in items 2 to 6 below holds for it; otherwise move to the next older qualifying tag and record, in `## Image`, each newer tag skipped and the first item that failed for it. Pin a tag for which an item fails, and list the failure under `## Divergences`, only when no qualifying tag passes (then pin the newest one and list every failure).
   3. Read the digest of the tag's index from the registry, not from the tag list alone: get a pull token from `https://auth.docker.io/token?service=registry.docker.io&scope=repository:cloudflare/cloudflared:pull` (`python3 -I -c` reading the `token` field from stdin), then `curl -fsS -D "$S/index.headers" -o "$S/index.json" -H "Authorization: Bearer $TOKEN" -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' https://registry-1.docker.io/v2/cloudflare/cloudflared/manifests/<version>`. The digest is the `docker-content-digest` response header. Require the response `content-type` to be an index (`application/vnd.oci.image.index.v1+json` or `application/vnd.docker.distribution.manifest.list.v2+json`) and `index.json` to list both `linux/amd64` and `linux/arm64` (an attestation manifest with platform `unknown/unknown` does not count as a platform); require the digest to equal the tag's `digest` field in the Docker Hub tag list.
   4. Fetch the `linux/amd64` manifest of the index and its config blob (the blob answers through a redirect: `curl -fsSL`, and send the `Authorization` header only to the registry host). From the config record `config.User`, `config.Entrypoint`, `config.Cmd` and `config.Env`.
2. **Image (spec 7.2, Versions table).** Confirm and record, each with the file and the line numbers at the tag: (a) the user the image runs as is UID 65532 (`config.User` and the base image's `/etc/passwd` entry for a name such as `nonroot`; the Dockerfile's `USER` line and its base image at the tag); (b) the entrypoint is `cloudflared`, possibly with flags, so that the container command `tunnel --no-autoupdate run --token-file /run/darkpanel/token` is appended to it, and record the exact `Entrypoint` and `Cmd` arrays; (c) the image has no shell or other process the module depends on. A user other than 65532, or an entrypoint that is not `cloudflared`, is a divergence.
3. **Token file (spec 7.2).** Fetch `cmd/cloudflared/tunnel/cmd.go`, `cmd/cloudflared/tunnel/subcommands.go`, `cmd/cloudflared/tunnel/configuration.go`, `cmd/cloudflared/main.go`, `cmd/cloudflared/updater/update.go`, `cmd/cloudflared/cliutil/*.go` and any file a finding points to. Confirm and record: (a) `tunnel run` has a `--token-file` flag in the pinned tag (the flag name, its environment variable, and which command defines it); (b) the code that reads the file: whether it trims white space (a trailing newline), and what it does with an empty file; (c) `--token-file` together with the other forms of giving a token or a tunnel (`--token`, a positional tunnel name, `TUNNEL_TOKEN`): which one wins, so that the module's command line, which gives only `--token-file`, is unambiguous; (d) `--no-autoupdate` is accepted where the command line puts it (`tunnel --no-autoupdate run ...`), i.e. it is a flag of the `tunnel` command or of the root command that `tunnel` inherits; (e) a file owned by UID 65532 with mode 0400, bind-mounted read-only, is readable by the process (the code opens it read-only and does not write beside it or change its mode).
4. **Token format (`## Token format`).** Find the decoder (`ParseToken` or the function the pinned tag uses): confirm that the token is standard base64 (`base64.StdEncoding`, padded) of a JSON object whose members are `a` (the account tag), `t` (the tunnel UUID) and `s` (the tunnel secret, itself base64 in JSON because the Go field is a byte slice), and whether `e` (an endpoint) is optional; record the Go struct and its JSON tags, what the decoder rejects (an empty `t`, a `t` that is not a UUID, an empty `s`) and the exact message of each rejection. Write into the note a Go expression that builds a well-formed token for a tunnel id, for example `base64.StdEncoding.EncodeToString([]byte(fmt.Sprintf(`{"a":%q,"t":%q,"s":%q}`, account, tunnel, base64.StdEncoding.EncodeToString(secret))))`, and check it against the decoder. Task 3's `agToken` builds a token as that expression does; a form that differs (raw or URL-safe base64, other member names) is a divergence.
5. **Log lines (`## Log lines`).** From the source, record (a) an exact substring cloudflared logs after it has read and decoded the token file and before it contacts the edge (for example the line that names the tunnel id, such as `Starting tunnel tunnelID=<uuid>`; give the file, the line and the format string, and say how the tunnel id appears), and (b) the exact substring it logs when the token file is unreadable or the token does not decode (the message and the file and line that produce it). State each as the text a test can search a container's log for. Nothing may be inferred from running the image: the harness (Task 15) checks these against a real container, and anything the source does not settle goes under `## Unverified`.
6. **Unauthorized token (`## Unauthorized token`).** From the supervisor and connection code, record what cloudflared does when the edge refuses a well-formed token for a tunnel it does not know (the registration error, whether it retries with backoff, whether it exits, and with which exit code). Give the result as a line of its own at the start of the section, `Behaviour: running`, `Behaviour: restarting` (the process exits non-zero and the restart policy brings it back) or `Behaviour: exits` (the process exits zero or the policy would not restart it), or `Behaviour: unsettled` when the source does not decide it; then the evidence. `unsettled` is not a divergence: Task 15 observes the real behaviour.
7. **Write `internal/modules/cloudflare/image.go`.** `package cloudflare`; one exported `const Image` with a doc comment saying it is the cloudflared image the connector runs, pinned by the digest of its multi-arch index, whose value is `cloudflare/cloudflared:<version>@sha256:<64 hex digits>` for the release and digest of item 1, with no other text; and `const imageUID = 65532` with a doc comment saying it is the user and group the image runs as, and so the owner and group of the token file. The file declares nothing else.
8. **Write `docs/dforge/notes/v1-w6b-cloudflare-spike.md`.** Start with the title `# v1 wave 6b Cloudflare spike`, then `Checked: <today, YYYY-MM-DD>` on its own line, then these headings in this order: `## Sources`, `## Image` (a line `Pinned: <the exact Image value>`, the release date from the tag list, the platforms of the index, the config values of item 1.4 and the findings of item 2, the skipped newer tags), `## Token file`, `## Token format`, `## Log lines`, `## Unauthorized token`, `## Divergences`. `## Divergences` holds the single line `none` when no check differs from a value in the plan's Global Constraints "Values copied from the spec", the plan's Contracts or spec 7.2, and otherwise one bullet per difference: the claim as the plan or spec states it, what the pinned tag does (file and line), and the tasks that state the old value (Tasks 3, 6 and 15 state the command, the token form, the mount and the user). Everything the sources did not settle goes under a `## Unverified` heading placed before `## Divergences` (omit it when empty); an unsettled item is not a divergence.
9. Do not edit any other file.

- [ ] **Step 4: Run the tests and the note checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/1 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestImPinned' -v ./internal/modules/cloudflare/
# the note carries the lines the gate and Task 15 read, in the right form
rg -n '^Checked: 20[0-9]{2}-[0-9]{2}-[0-9]{2}$' docs/dforge/notes/v1-w6b-cloudflare-spike.md
rg -n '^## (Sources|Image|Token file|Token format|Log lines|Unauthorized token|Divergences)$' docs/dforge/notes/v1-w6b-cloudflare-spike.md
rg -n '^Behaviour: (running|restarting|exits|unsettled)$' docs/dforge/notes/v1-w6b-cloudflare-spike.md
sed -n '/^## Divergences$/,$p' docs/dforge/notes/v1-w6b-cloudflare-spike.md
# the note and the code pin the same image
img=$(rg -o 'cloudflare/cloudflared:[^"]+' internal/modules/cloudflare/image.go); rg -nF -- "Pinned: $img" docs/dforge/notes/v1-w6b-cloudflare-spike.md
# the digest is the registry's current answer for the tag
ver=${img#*:}; ver=${ver%%@*}; dig=${img#*@}
TOKEN=$(timeout 30 curl -fsS 'https://auth.docker.io/token?service=registry.docker.io&scope=repository:cloudflare/cloudflared:pull' | python3 -I -c 'import json,sys;print(json.load(sys.stdin)["token"])')
timeout 30 curl -fsSI -H "Authorization: Bearer $TOKEN" -H 'Accept: application/vnd.oci.image.index.v1+json, application/vnd.docker.distribution.manifest.list.v2+json' "https://registry-1.docker.io/v2/cloudflare/cloudflared/manifests/$ver" | rg -i "docker-content-digest: $dig"
git status --short
```

Expected: `ok`; every `rg` prints its line; the `Divergences` section prints `none` or the bullets; the `docker-content-digest` line matches; `git status --short` lists only the three files of this task.

- [ ] **Step 5: Run the task gate and commit**

```bash
export TMPDIR=/root/.cache/w6b-tmp/1 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 300 go test -race -count=1 ./internal/modules/cloudflare/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf "$TMPDIR"; go clean -testcache
git add internal/modules/cloudflare/image.go internal/modules/cloudflare/image_test.go
git commit -m "feat(cloudflare): pin the cloudflared image by digest"
git add docs/dforge/notes/v1-w6b-cloudflare-spike.md
git commit -m "docs(notes): add the v1-w6b cloudflare spike"
```

Record the real output of each command in the report, and copy the `## Divergences` section and the `Behaviour:` line into the report's last lines so the controller can run the contract-check gate from it.

### Task 2: web: connectors page

**Files:**
- Create: `web/src/modules/cloudflare/connector-types.ts`
- Create: `web/src/modules/cloudflare/connectors-page.tsx`
- Test: `web/src/modules/cloudflare/connectors-page.test.tsx`
- Modify: `web/src/modules/cloudflare/global.tsx` (the route and the link)
- Test: `web/src/modules/cloudflare/global.test.tsx`
- Modify: `web/src/modules/cloudflare/cloudflare-change.tsx` (an optional `hostId` prop)
- Modify: `web/src/modules/cloudflare/fixtures.ts` (`cfConnector`)

**Interfaces:**
- Consumes: nothing from an earlier task. Existing: `CloudflareChange` and `CLOUDFLARE_ACTION_VERSION`, `cloudflareKey` and `TunnelView` (`types.ts`); `cfTunnel` (`fixtures.ts`); `listHosts`, `HOSTS_QUERY_KEY`, `readAction` and `errorMessage` (`@/api/client`); `ChangeDialog` and its `target` of `{ name, version, hostId?, module? }`, which checks the module's availability on the host when both are set (`@/components/change-dialog`); `renderWithProviders`, `stubFetch` (`@/test/utils`).
- Produces: Contracts "Web": `connector-types.ts` exporting `Connector`, `ConnectorList`, `connectorListKey(hostId)` and `tunnelLabel(id, tunnels)` (`CLOUDFLARE_ACTION_VERSION` is reused from `types.ts`, which already exports it), `ConnectorsPage` at `/cloudflare/connectors`, and the "Connectors" link between Tunnels and Hostnames. Also `cfConnector` in `fixtures.ts` and the `hostId` prop of `CloudflareChange`, which Task 11 may use. The page calls the wire actions `cloudflare.connector.list` (agent, read), `cloudflare.connector.stop` (agent) and `cloudflare.connector.deploy` (controller composite) of Contracts "Agent actions" and "Controller composites"; they are built by later tasks, and the page is tested against a stubbed API.

**Items:** 34

**Depends on:** none

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 0 = 5

The page is the host half of the connector: for one host that runs the Cloudflare module it lists the connectors (tunnel, container, state) with a Stop action, and offers a form that deploys a connector there for a new or an existing tunnel. Rulings: a tunnel that already has a connector on the chosen host is not offered in the form (`connector.run` refuses a second one); the tunnel's name is shown where the tunnel list knows the id, and the id where it does not (a tunnel list that cannot be read must not hide the connectors); the stop dialog is an agent action, so `CloudflareChange` gains an optional `hostId`, which sets the dialog's `target` to `{ name, version, hostId, module: "cloudflare" }`; deploy is a controller composite, so it carries `hostId` in its input and none in the target.

- [ ] **Step 1: Write the failing tests**

Add this fixture at the end of `web/src/modules/cloudflare/fixtures.ts`, import `Connector` from `./connector-types` at its top (a type import, written before the existing import) and extend the file's leading comment with "and the running connector of that tunnel":

```ts
export function cfConnector(over: Partial<Connector> = {}): Connector {
  return {
    tunnel: "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f",
    container: "darkpanel-cloudflared-9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f",
    containerId: "c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00c0ffee00",
    state: "running",
    ...over,
  }
}
```

Create `web/src/modules/cloudflare/connectors-page.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Change, Host, Plan } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ConnectorsPage } from "./connectors-page"
import { cfConnector, cfTunnel } from "./fixtures"

const HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const LIST_SUFFIX = "cloudflare.connector.list/v1/read"
const cnList = (host: string) => `POST /api/v1/hosts/${host}/actions/${LIST_SUFFIX}`
const cnAvailability = (host: string) => `GET /api/v1/hosts/${host}/modules/cloudflare/availability`

function cnHost(id: string, name: string, enabled = true): Host {
  return {
    id,
    name,
    isLocal: false,
    online: true,
    modules: [{ id: "cloudflare", version: 1, enabled, available: true, reason: null, checkedAt: null }],
    ingestion: null,
  }
}

function cnTunnels() {
  return [cfTunnel({ id: "t1", name: "darkpanel-c1" }), cfTunnel({ id: "t2", name: "darkpanel-c2", status: "inactive", connections: [] })]
}

function cnPlan(action: string, hostId: string | null): Plan {
  return {
    planId: "p1", hostId, action, version: 1, inputHash: "h", diff: [{ op: "~", text: "connector" }], guards: [],
    expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
  }
}

const cnChange: Change = {
  id: "c9", hostId: "h1", action: "cloudflare.connector.stop", version: 1, class: "compensable", state: "completed", actor: "user:u1",
  createdAt: "2026-11-03T12:00:00Z", updatedAt: "2026-11-03T12:00:00Z", revertDeadline: null, output: { tunnel: "t1", removed: true }, error: null,
  compensatedBy: "cloudflare.connector.run", parentChangeId: null, owner: null, repairedBy: null, repairOutcome: null, override: null,
}

function cnRender(hosts: Host[], routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [HOSTS]: { body: { output: { hosts } } },
    [TUNNELS]: { body: { output: { tunnels: cnTunnels() } } },
    [cnList("h1")]: { body: { output: { connectors: [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })] } } },
    [cnAvailability("h1")]: { body: { module: "cloudflare", available: true, reason: null, actions: [] } },
    ...routes,
  })
  const result = renderWithProviders(<ConnectorsPage />)
  return { calls, ...result }
}

function cnListCalls(calls: FetchCall[]): FetchCall[] {
  return calls.filter((c) => c.path.endsWith(LIST_SUFFIX))
}

function cnPlanCall(calls: FetchCall[], path: string): FetchCall | undefined {
  return calls.find((c) => c.method === "POST" && c.path === path)
}

async function cnTable() {
  return screen.findByRole("table", { name: "Connectors" })
}

describe("ConnectorsPage", () => {
  test("lists the connectors of the only host with the tunnel's name, the container and the state", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: {
        body: {
          output: {
            connectors: [
              cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1", state: "running" }),
              cfConnector({ tunnel: "t-gone", container: "darkpanel-cloudflared-t-gone", state: "missing" }),
            ],
          },
        },
      },
    })
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell").slice(0, 3).map((c) => c.textContent))).toEqual([
      ["darkpanel-c1", "darkpanel-cloudflared-t1", "running"],
      ["t-gone", "darkpanel-cloudflared-t-gone", "missing"],
    ])
    expect(screen.getByText("Connectors on web-1")).toBeTruthy()
    expect(screen.queryByRole("radiogroup", { name: "Host" })).toBeNull()
    const read = cnListCalls(calls)
    expect(read.map((c) => [c.path, c.body])).toEqual([["/api/v1/hosts/h1/actions/cloudflare.connector.list/v1/read", { input: {} }]])
  })

  test("offers only the hosts that run the module and reads none until one is chosen", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1"), cnHost("h2", "web-2"), cnHost("h3", "db-1", false)], {
      [cnList("h2")]: { body: { output: { connectors: [cfConnector({ tunnel: "t2", container: "darkpanel-cloudflared-t2" })] } } },
    })
    const group = await screen.findByRole("radiogroup", { name: "Host" })
    expect(within(group).getAllByRole("radio")).toHaveLength(2)
    expect(within(group).getByRole("radio", { name: "web-1" })).toBeTruthy()
    expect(within(group).getByRole("radio", { name: "web-2" })).toBeTruthy()
    expect(within(group).queryByRole("radio", { name: "db-1" })).toBeNull()
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
    expect(cnListCalls(calls)).toHaveLength(0)
    await userEvent.setup().click(within(group).getByRole("radio", { name: "web-2" }))
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["darkpanel-c2"])
    expect(cnListCalls(calls).map((c) => c.path)).toEqual(["/api/v1/hosts/h2/actions/cloudflare.connector.list/v1/read"])
  })

  test("without a host that runs the module the page says so", async () => {
    cnRender([cnHost("h3", "db-1", false)])
    expect((await screen.findByText("No host runs the Cloudflare module")).textContent).toBe("No host runs the Cloudflare module")
    expect(screen.queryByRole("button", { name: "Deploy connector" })).toBeNull()
  })

  test("a host without connectors says so and still offers to deploy one", async () => {
    cnRender([cnHost("h1", "web-1")], { [cnList("h1")]: { body: { output: { connectors: [] } } } })
    expect((await screen.findByText("No connectors on this host")).textContent).toBe("No connectors on this host")
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
    expect(screen.getByRole("button", { name: "Deploy connector" })).toBeTruthy()
  })

  test("a failed read of the connectors shows the reason", async () => {
    cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: {
        status: 503,
        body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the Docker Engine is not reachable", code: "docker.engine_unavailable" },
      },
    })
    expect((await screen.findByRole("alert")).textContent).toBe("the Docker Engine is not reachable")
    expect(screen.queryByRole("table", { name: "Connectors" })).toBeNull()
  })

  test("stopping a connector plans the stop on its host by tunnel id and refreshes the list when the dialog closes", async () => {
    let connectors = [cfConnector({ tunnel: "t1", container: "darkpanel-cloudflared-t1" })]
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      [cnList("h1")]: () => ({ body: { output: { connectors } } }),
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/plan": { body: cnPlan("cloudflare.connector.stop", "h1") },
      "POST /api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/apply": () => {
        connectors = []
        return { body: cnChange }
      },
    })
    const user = userEvent.setup()
    await cnTable()
    await user.click(screen.getByRole("button", { name: "Stop the connector of darkpanel-c1" }))
    const dialog = await screen.findByRole("dialog", { name: "Stop the connector of darkpanel-c1" })
    expect(within(dialog).getByText("cloudflare.connector.stop/v1 on host h1")).toBeTruthy()
    await within(dialog).findByLabelText("Planned changes")
    expect(cnPlanCall(calls, "/api/v1/hosts/h1/actions/cloudflare.connector.stop/v1/plan")?.body).toEqual({ input: { tunnel: "t1" } })
    expect(calls.some((c) => c.path === "/api/v1/hosts/h1/modules/cloudflare/availability")).toBe(true)
    const apply = within(dialog).getByRole("button", { name: "Apply" })
    await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
    await user.click(apply)
    await within(dialog).findByText("completed")
    // The dialog's corner button has the same name; the footer's is the last.
    await user.click(within(dialog).getAllByRole("button", { name: "Close" }).slice(-1)[0] as HTMLElement)
    expect((await screen.findByText("No connectors on this host")).textContent).toBe("No connectors on this host")
    expect(cnListCalls(calls)).toHaveLength(2)
  })

  test("deploying plans the composite on the controller with the host alone, which has it create a tunnel", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      "POST /api/v1/actions/cloudflare.connector.deploy/v1/plan": { body: cnPlan("cloudflare.connector.deploy", null) },
    })
    const user = userEvent.setup()
    await cnTable()
    expect(screen.getByRole("radio", { name: "Create a new tunnel" }).getAttribute("aria-checked")).toBe("true")
    await user.click(screen.getByRole("button", { name: "Deploy connector" }))
    const dialog = await screen.findByRole("dialog", { name: "Deploy a connector on web-1" })
    await within(dialog).findByLabelText("Planned changes")
    expect(within(dialog).getByText("cloudflare.connector.deploy/v1 on the controller")).toBeTruthy()
    expect(cnPlanCall(calls, "/api/v1/actions/cloudflare.connector.deploy/v1/plan")?.body).toEqual({ input: { hostId: "h1" } })
  })

  test("deploying for an existing tunnel names it, and a tunnel that already has a connector on the host is not offered", async () => {
    const { calls } = cnRender([cnHost("h1", "web-1")], {
      "POST /api/v1/actions/cloudflare.connector.deploy/v1/plan": { body: cnPlan("cloudflare.connector.deploy", null) },
    })
    const user = userEvent.setup()
    await cnTable()
    const form = screen.getByRole("form", { name: "Deploy a connector" })
    expect(within(form).getAllByRole("radio")).toHaveLength(2)
    expect(within(form).queryByRole("radio", { name: "darkpanel-c1" })).toBeNull()
    await user.click(within(form).getByRole("radio", { name: "darkpanel-c2" }))
    await user.click(within(form).getByRole("button", { name: "Deploy connector" }))
    const dialog = await screen.findByRole("dialog", { name: "Deploy a connector on web-1" })
    await within(dialog).findByLabelText("Planned changes")
    expect(cnPlanCall(calls, "/api/v1/actions/cloudflare.connector.deploy/v1/plan")?.body).toEqual({ input: { hostId: "h1", tunnel: "t2" } })
  })

  test("a tunnel list that cannot be read shows the reason, still lists the connectors by tunnel id and offers a new tunnel only", async () => {
    cnRender([cnHost("h1", "web-1")], {
      [TUNNELS]: {
        status: 409,
        body: { type: "about:blank", title: "Not connected", status: 409, detail: "no Cloudflare account is connected", code: "cloud.not_connected" },
      },
    })
    const rows = within(await cnTable()).getAllByRole("row").slice(1)
    expect(rows.map((r) => within(r).getAllByRole("cell")[0]?.textContent)).toEqual(["t1"])
    expect((await screen.findByRole("alert")).textContent).toBe("no Cloudflare account is connected")
    const form = screen.getByRole("form", { name: "Deploy a connector" })
    expect(within(form).getAllByRole("radio")).toHaveLength(1)
    expect(within(form).getByRole("radio", { name: "Create a new tunnel" })).toBeTruthy()
  })

  test("has no accessibility violations", async () => {
    const { container } = cnRender([cnHost("h1", "web-1"), cnHost("h2", "web-2")])
    await userEvent.setup().click(await screen.findByRole("radio", { name: "web-1" }))
    await cnTable()
    expect(await axe(container)).toHaveNoViolations()
  })
})
```

Replace `web/src/modules/cloudflare/global.test.tsx` with this (it mocks the new page and checks the route and the link; the changes are the mock, the two table rows, the four-link test and the parameterised link test):

```tsx
import { screen } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { Route, Routes } from "react-router"
import { expect, test, vi } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import CloudflareGlobal from "./global"

// The pages of other files are replaced by a marker each; this file tests the routes and the links.
vi.mock("./account-panel", () => ({ AccountPanel: () => <p>account panel</p> }))
vi.mock("./tunnels-page", () => ({ TunnelsPage: () => <p>tunnels page</p> }))
vi.mock("./connectors-page", () => ({ ConnectorsPage: () => <p>connectors page</p> }))
vi.mock("./hostnames-page", () => ({ HostnamesPage: () => <p>hostnames page</p> }))

function glRender(path: string) {
  stubFetch({})
  return renderWithProviders(
    <Routes>
      <Route path="/cloudflare/*" element={<CloudflareGlobal />} />
    </Routes>,
    { path },
  )
}

test.each([
  ["/cloudflare", "account panel"],
  ["/cloudflare/tunnels", "tunnels page"],
  ["/cloudflare/connectors", "connectors page"],
  ["/cloudflare/hostnames", "hostnames page"],
])("%s shows the %s", async (path, marker) => {
  glRender(path)
  expect((await screen.findByText(marker)).textContent).toBe(marker)
})

test("an unknown address shows the not-found state", async () => {
  glRender("/cloudflare/nowhere")
  expect((await screen.findByText("Page not found")).textContent).toBe("Page not found")
})

test("links the four pages and marks the open one", async () => {
  glRender("/cloudflare/tunnels")
  const nav = await screen.findByRole("navigation", { name: "Cloudflare pages" })
  const links = Array.from(nav.querySelectorAll("a")).map((a) => [a.textContent, a.getAttribute("href"), a.getAttribute("aria-current")])
  expect(links).toEqual([
    ["Account", "/cloudflare", null],
    ["Tunnels", "/cloudflare/tunnels", "page"],
    ["Connectors", "/cloudflare/connectors", null],
    ["Hostnames", "/cloudflare/hostnames", null],
  ])
})

test.each([
  ["Connectors", "connectors page", "/cloudflare/connectors"],
  ["Hostnames", "hostnames page", "/cloudflare/hostnames"],
])("following the %s link opens that page", async (name, marker, location) => {
  glRender("/cloudflare")
  const user = userEvent.setup()
  await user.click(await screen.findByRole("link", { name }))
  expect((await screen.findByText(marker)).textContent).toBe(marker)
  expect(screen.getByTestId("location").textContent).toBe(location)
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/2 && export TMPDIR=/root/.cache/w6b-tmp/2 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/cloudflare/connectors-page.test.tsx src/modules/cloudflare/global.test.tsx
```

Expected: `connectors-page.test.tsx` fails to load (`Failed to resolve import "./connectors-page"`); in `global.test.tsx` the tests `/cloudflare/connectors shows the connectors page`, `links the four pages and marks the open one` and `following the Connectors link opens that page` fail. Quote the output in the report.

- [ ] **Step 3: Implement**

1. `connector-types.ts`: a type-only import of `TunnelView` from `./types`. Export `interface Connector { tunnel: string; container: string; containerId: string; state: string }` (the wire type of Contracts "Wire types", with a comment that `state` is the Engine's container state and `"missing"` when the container is gone) and `interface ConnectorList { connectors: Connector[] }`. Export `connectorListKey(hostId: string)` returning the tuple `["cloudflare", "connectors", hostId] as const`, with a comment that it sits under `["cloudflare"]`, the prefix `CloudflareChange` invalidates when a dialog closes. Export `tunnelLabel(id: string, tunnels: Pick<TunnelView, "id" | "name">[]): string` returning the name of the tunnel with that id, else the id.
2. `cloudflare-change.tsx`: add the constant `CLOUDFLARE_MODULE = "cloudflare"`, the optional prop `hostId?: string` (commented: set for an agent action, the host it runs on, whose module availability the dialog checks) and build the dialog's `target` as `{ name: action, version: CLOUDFLARE_ACTION_VERSION }` plus `hostId` and `module: CLOUDFLARE_MODULE` only when `hostId` is set. Update the component's doc comment to say it plans and applies one Cloudflare action on the controller or on the host `hostId` names. Nothing else changes: existing callers pass no `hostId`.
3. `global.tsx`: import `ConnectorsPage`, add `{ to: "/cloudflare/connectors", label: "Connectors", end: false }` to `LINKS` between Tunnels and Hostnames, and `<Route path="connectors" element={<ConnectorsPage />} />` between the `tunnels` and `hostnames` routes.
4. `connectors-page.tsx` exports `ConnectorsPage`, built from darkraise-ui components as `tunnels-page.tsx` and `hostnames-page.tsx` are:
   - Hosts: `useQuery` with `HOSTS_QUERY_KEY` and `listHosts`. While pending, a `Spinner` labelled "Loading the hosts"; on error a destructive `Alert` with `role="alert"` and `errorMessage(error)`. A host is eligible when its `modules` list has `cloudflare` with `enabled` true. With no eligible host, an `EmptyState` titled "No host runs the Cloudflare module" (description: enable the module on a host to run a connector there). With one eligible host it is the host; with several, a `Card` titled "Host" holds a `RadioGroup` labelled "Host" with one `RadioGroupItem` and `Label` (the host's name) per host, and no host is selected, so nothing is read, until one is chosen.
   - Tunnels: one `useQuery` of `cloudflare.tunnel.list` through `readAction("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {})` under `cloudflareKey("tunnels")`, the same query `tunnels-page.tsx` makes, so the cache is shared. Its failure shows a destructive alert with the reason in the deploy card and never blocks the connectors.
   - For the chosen host (a child component keyed by the host id, so the query and the form state restart per host): `useQuery` with `connectorListKey(host.id)` and `readAction<ConnectorList>("cloudflare.connector.list", CLOUDFLARE_ACTION_VERSION, {}, host.id)`. A `Card` titled `Connectors on <host name>` shows a `Spinner` labelled "Loading the connectors" while pending, a destructive alert with the reason on error, an `EmptyState` titled "No connectors on this host" for an empty list, and otherwise a `Table` with `aria-label="Connectors"` and the columns Tunnel (`tunnelLabel`), Container (monospace), State (a `Badge`, `default` variant for `running`, `secondary` otherwise) and an Actions column with a visually hidden header. Each row's button "Stop" has `aria-label` `Stop the connector of <tunnel label>` and opens the change dialog for `cloudflare.connector.stop` with the title `Stop the connector of <tunnel label>`, the input `{ tunnel: <tunnel id> }` and `hostId` set to the host.
   - Deploy: a second `Card` titled "Deploy a connector" holds a `form` with `aria-label="Deploy a connector"`: a `fieldset` with the legend "Tunnel" and a `RadioGroup` labelled "Tunnel" whose first item "Create a new tunnel" (value `new`) is selected by default, then one item per known tunnel that has no connector on this host, labelled with the tunnel's name (monospace) and valued by its id; a chosen tunnel that stops being offered falls back to the new-tunnel choice. Submitting (a button "Deploy connector") opens the change dialog for `cloudflare.connector.deploy` with the title `Deploy a connector on <host name>` and the input `{ hostId: <host id> }` plus `tunnel: <id>` only for an existing tunnel, with no `hostId` prop (controller action). While the tunnel list is pending the form is not shown (a labelled `Spinner`).
   - One `CloudflareChange` is rendered while a change is pending and closes by clearing it; its invalidation of `["cloudflare"]` refreshes the connector list after a stop.
   - Every control has an accessible name; the page passes `axe`.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/2 GOTOOLCHAIN=local
timeout 300 pnpm -C web exec vitest run src/modules/cloudflare/
timeout 600 pnpm -C web lint
timeout 600 pnpm -C web typecheck
timeout 900 pnpm -C web test
rm -rf /root/.cache/w6b-tmp/2
```

Expected: every test file passes, `lint` and `typecheck` print no error, and `pnpm -C web test` ends with every file passed. The Go checks of Global Constraints are unaffected by this task; `timeout 600 go build ./... && timeout 300 go vet ./...` may be skipped, and the report says so.

- [ ] **Step 5: Commit**

```bash
git add web/src/modules/cloudflare/connector-types.ts web/src/modules/cloudflare/cloudflare-change.tsx web/src/modules/cloudflare/fixtures.ts
git add web/src/modules/cloudflare/connectors-page.tsx web/src/modules/cloudflare/connectors-page.test.tsx
git add web/src/modules/cloudflare/global.tsx web/src/modules/cloudflare/global.test.tsx
git commit -m "feat(web): add the Cloudflare connectors page"
```

### Task 3: cloudflare: agent connector seam, wire types, tables and test host

**Files:**
- Create: `internal/modules/cloudflare/connector_wire.go`
- Test: `internal/modules/cloudflare/connector_wire_test.go`
- Create: `internal/modules/cloudflare/connector_store.go`
- Test: `internal/modules/cloudflare/connector_store_test.go`
- Create: `internal/modules/cloudflare/connector_deps.go`
- Test: `internal/modules/cloudflare/connector_deps_test.go`
- Test: `internal/modules/cloudflare/connector_testhost_test.go` (the agent test host; only this task declares its identifiers)
- Create: `internal/modules/cloudflare/migrations/agent/0001_cloudflare.sql`
- Modify: `internal/modules/cloudflare/store.go` (the embed directive)
- Modify: `internal/modules/cloudflare/client.go` (`cf` and `productionCF`)
- Modify: `internal/modules/cloudflare/cloudflare.go` (`Manifest`)
- Test: `internal/modules/cloudflare/register_test.go` (`TestRegManifest`)

**Interfaces:**
- Consumes: nothing from an earlier task. Existing: `queryer`, `storeFormatTime`, `storeParseTime` (`store.go`); `cfJournal` (`host_test.go`, the in-memory effects journal); `controllerMigrations`, `Module`, `module` and `ID`; `dockerapi` (`ContainerJSON`, `ContainerCreate`, `Network`, `EndpointSettings`, `ImageInspect`, `PullMessage`, `APIError`, `IsNotFound`, `ErrUnavailable`, `DefaultSocket`, `New`); `core.FileWriter`, `core.OwnedPath`; `coretest.FakeClock`, `coretest.Sealer`; `errcode.DockerEngineUnavailable`. It does not use `Image` or `imageUID` (Task 1).
- Produces: Contracts "Wire types", "Agent tables", "Agent store", "Names", "Agent dependencies" and "Agent test host", exactly as written there. Later tasks build on them: `agentDeps.docker`, `connectorDocker`, `pullStream`, `engineCoded`, `ownedPaths`, `connectorName`, `tokenFileName`, `tunnelIDProblem`, `claimantProblem`, `networkNamePattern`, `tokenDir`, `tokenMount`, the store functions, and the test host (`agHost`, `newAGHost`, `agDocker`, `agFiles`, `agContainer`, `agOnNetwork`, `agNetwork`, `agToken`). Task 6 adds `readFile` to `agentDeps` and creates `connector_effects.go`; this task creates neither.

**Items:** 34, 35

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

This task adds the agent half's foundations and registers nothing: the new actions stay out of `Manifest().Actions` until Task 14, so `modules.TestSnapshotsCurrent` stays green. The manifest does gain the token directory as an owned path and the agent migrations. Rulings: the test host's Engine fake answers like Docker for the calls the connector uses (404 for an absent object, 409 for a taken name, 403 for a duplicate or missing endpoint), keeps every create request's `Config` and `HostConfig` as sent, and reports a `bind` or `Mounts` entry as a `MountPoint`; the file fake accepts only the module's own token directory and `<uuid>.token` names, as the platform's `WriteManaged` does, and records every call; production wiring points the Docker client at `/nonexistent/darkpanel/docker.sock` when `testing.Testing()` is true, so no test binary that links the module can reach the host's daemon.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/cloudflare/connector_wire_test.go`:

```go
package cloudflare

import (
	"encoding/json"
	"net/netip"
	"reflect"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/schema"
)

func TestCwWireShapesKeepTheirJSONNames(t *testing.T) {
	const tunnel = "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f"
	conn := Connector{Tunnel: tunnel, Container: "darkpanel-cloudflared-" + tunnel, ContainerID: "c0ffee", State: "running"}
	const connJSON = `{"tunnel":"` + tunnel + `","container":"darkpanel-cloudflared-` + tunnel + `","containerId":"c0ffee","state":"running"}`
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"run input", ConnectorRunInput{Tunnel: tunnel, Token: "tok"}, `{"tunnel":"` + tunnel + `","token":"tok"}`},
		{"connector", conn, connJSON},
		{"stop input", ConnectorStopInput{Tunnel: tunnel}, `{"tunnel":"` + tunnel + `"}`},
		{"stopped", ConnectorStopped{Tunnel: tunnel, Removed: true}, `{"tunnel":"` + tunnel + `","removed":true}`},
		{"stopped with nothing left", ConnectorStopped{Tunnel: tunnel}, `{"tunnel":"` + tunnel + `","removed":false}`},
		{"list input", ConnectorListInput{}, `{}`},
		{"empty list", ConnectorList{Connectors: []Connector{}}, `{"connectors":[]}`},
		{"list", ConnectorList{Connectors: []Connector{conn}}, `{"connectors":[` + connJSON + `]}`},
		{"attach input", NetworkAttachInput{Tunnel: tunnel, Network: "web", Claimant: "expose:app.example.com"},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"expose:app.example.com"}`},
		{"attach input with a target and an address",
			NetworkAttachInput{Tunnel: tunnel, Network: "web", Claimant: "panel", Target: "app", Address: netip.MustParseAddr("172.31.255.2")},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"panel","target":"app","address":"172.31.255.2"}`},
		{"attachment", NetworkAttachment{Tunnel: tunnel, Network: "web", Claimant: "c", Preexisting: true, Attached: true},
			`{"tunnel":"` + tunnel + `","network":"web","claimant":"c","preexisting":true,"attached":true}`},
		{"detach input", NetworkDetachInput{Tunnel: tunnel, Claimant: "c"}, `{"tunnel":"` + tunnel + `","network":"","claimant":"c"}`},
		{"deploy input without a tunnel", ConnectorDeployInput{HostID: "h1"}, `{"hostId":"h1"}`},
		{"deploy input with a tunnel", ConnectorDeployInput{HostID: "h1", Tunnel: "darkpanel-c1"}, `{"hostId":"h1","tunnel":"darkpanel-c1"}`},
		{"deploy output", ConnectorDeployOutput{Tunnel: tunnel, Created: true, Connector: conn},
			`{"tunnel":"` + tunnel + `","created":true,"connector":` + connJSON + `}`},
		{"expose input", ExposeInput{HostID: "h1", Tunnel: "darkpanel-c1", Container: "app", Port: 8080, Network: "web", Hostname: "app.example.com"},
			`{"hostId":"h1","tunnel":"darkpanel-c1","container":"app","port":8080,"network":"web","hostname":"app.example.com"}`},
		{"expose output", ExposeOutput{Hostname: "app.example.com", Service: "http://app:8080", Network: "web"},
			`{"hostname":"app.example.com","service":"http://app:8080","network":"web"}`},
		{"expose remove input", ExposeRemoveInput{HostID: "h1", Hostname: "app.example.com"}, `{"hostId":"h1","hostname":"app.example.com"}`},
		{"expose remove output", ExposeRemoveOutput{Hostname: "app.example.com"}, `{"hostname":"app.example.com"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tc.want {
				t.Errorf("JSON %s, want %s", got, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal([]byte(tc.want), back.Interface()); err != nil {
				t.Fatal(err)
			}
			again, err := json.Marshal(back.Elem().Interface())
			if err != nil || string(again) != tc.want {
				t.Errorf("round trip %s (%v), want %s", again, err, tc.want)
			}
		})
	}
}

// cwProperty returns the schema keywords of one property of a type's schema, which is a reference to the one
// definition it holds.
func cwProperty(t *testing.T, doc json.RawMessage, property string) map[string]any {
	t.Helper()
	var s struct {
		Ref  string `json:"$ref"`
		Defs map[string]struct {
			Properties map[string]map[string]any `json:"properties"`
		} `json:"$defs"`
	}
	if err := json.Unmarshal(doc, &s); err != nil {
		t.Fatal(err)
	}
	for _, def := range s.Defs {
		if p, ok := def.Properties[property]; ok {
			return p
		}
	}
	t.Fatalf("the schema has no property %q: %s", property, doc)
	return nil
}

// TestCwTokenIsASecretAndPortIsBounded: the token a connector run takes is marked secret in the struct tag and in the
// schema, so the audit log, the snapshots and the CLI never show it, and the port of an exposure is bounded in its
// schema.
func TestCwTokenIsASecretAndPortIsBounded(t *testing.T) {
	sf, ok := reflect.TypeFor[ConnectorRunInput]().FieldByName("Token")
	if !ok || sf.Tag.Get("secret") != "true" {
		t.Errorf("ConnectorRunInput.Token tag %q, want secret:\"true\"", sf.Tag)
	}
	if sf, ok := reflect.TypeFor[ConnectorRunInput]().FieldByName("Tunnel"); !ok || sf.Tag.Get("secret") != "" {
		t.Errorf("ConnectorRunInput.Tunnel tag %q, want no secret tag", sf.Tag)
	}
	in, err := schema.Input(reflect.TypeFor[ConnectorRunInput]())
	if err != nil {
		t.Fatal(err)
	}
	if cwProperty(t, in, "token")["x-secret"] != true || cwProperty(t, in, "tunnel")["x-secret"] != nil {
		t.Errorf("connector run input schema: want token alone marked secret: %s", in)
	}
	expose, err := schema.Input(reflect.TypeFor[ExposeInput]())
	if err != nil {
		t.Fatal(err)
	}
	port := cwProperty(t, expose, "port")
	if port["minimum"] != float64(1) || port["maximum"] != float64(65535) {
		t.Errorf("expose input port schema %v, want minimum 1 and maximum 65535", port)
	}
}
```

Create `internal/modules/cloudflare/connector_store_test.go`:

```go
package cloudflare

import (
	"context"
	"database/sql"
	"io/fs"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	csTunnelA = "0a1b2c3d-0000-4000-8000-00000000000a"
	csTunnelB = "0a1b2c3d-0000-4000-8000-00000000000b"
)

// csWrite runs fn in a write transaction of the host's agent database and fails the test on its error.
func csWrite(t *testing.T, db *store.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

func csConnector(tunnel, container string, at time.Time) connectorRow {
	return connectorRow{TunnelID: tunnel, ContainerID: container, TokenCT: []byte("sealed-" + tunnel), ChangeID: "01JCHANGE" + tunnel[len(tunnel)-1:], CreatedAt: at}
}

// TestCsConnectors: a connector row keeps every column, including its time to the nanosecond, is found by tunnel and
// by container, is listed in tunnel order, refuses a second row for a tunnel and is deleted by itself.
func TestCsConnectors(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	db := h.db.Read()
	if rows, err := listConnectors(ctx, db); err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("listConnectors on an empty table = %#v, %v; want an empty, non-nil slice", rows, err)
	}
	if _, ok, err := loadConnector(ctx, db, csTunnelA); err != nil || ok {
		t.Fatalf("loadConnector on an empty table = %t, %v; want not found", ok, err)
	}
	at := time.Date(2026, 11, 3, 12, 0, 0, 123456789, time.UTC)
	b, a := csConnector(csTunnelB, "cid-b", at.Add(time.Minute)), csConnector(csTunnelA, "cid-a", at)
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		if err := insertConnector(ctx, tx, b); err != nil {
			return err
		}
		return insertConnector(ctx, tx, a)
	})
	got, ok, err := loadConnector(ctx, db, csTunnelA)
	if err != nil || !ok || !reflect.DeepEqual(got, a) {
		t.Fatalf("loadConnector = %+v, %t, %v; want %+v", got, ok, err, a)
	}
	got, ok, err = connectorByContainer(ctx, db, "cid-b")
	if err != nil || !ok || !reflect.DeepEqual(got, b) {
		t.Fatalf("connectorByContainer = %+v, %t, %v; want %+v", got, ok, err, b)
	}
	if _, ok, err := connectorByContainer(ctx, db, "cid-none"); err != nil || ok {
		t.Errorf("connectorByContainer of an unknown container = %t, %v; want not found", ok, err)
	}
	all, err := listConnectors(ctx, db)
	if err != nil || !reflect.DeepEqual(all, []connectorRow{a, b}) {
		t.Errorf("listConnectors = %+v, %v; want %+v then %+v", all, err, a, b)
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error { return insertConnector(ctx, tx, csConnector(csTunnelA, "cid-other", at)) })
	if err == nil || !strings.Contains(err.Error(), csTunnelA) {
		t.Errorf("a second connector for one tunnel: error %v, want one naming the tunnel", err)
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error { return deleteConnector(ctx, tx, csTunnelA) })
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error { return deleteConnector(ctx, tx, csTunnelA) })
	if _, ok, _ := loadConnector(ctx, db, csTunnelA); ok {
		t.Error("the connector remains after its delete")
	}
	if _, ok, _ := loadConnector(ctx, db, csTunnelB); !ok {
		t.Error("deleting one connector removed another")
	}
}

// TestCsClaims: claims are listed for one network or for all of a tunnel's, ordered by network and claimant, keep
// their preexisting flag, never mix tunnels, refuse a duplicate and are deleted one at a time.
func TestCsClaims(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	db := h.db.Read()
	claims := []claimRow{
		{TunnelID: csTunnelA, Network: "web", Claimant: "expose:b.example.com", ChangeID: "c2", Preexisting: false},
		{TunnelID: csTunnelA, Network: "api", Claimant: "expose:z.example.com", ChangeID: "c3", Preexisting: true},
		{TunnelID: csTunnelA, Network: "web", Claimant: "expose:a.example.com", ChangeID: "c1", Preexisting: false},
		{TunnelID: csTunnelB, Network: "web", Claimant: "expose:other.example.com", ChangeID: "c4", Preexisting: false},
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		for _, c := range claims {
			if err := insertClaim(ctx, tx, c); err != nil {
				return err
			}
		}
		return nil
	})
	all, err := connectorClaims(ctx, db, csTunnelA, "")
	want := []claimRow{claims[1], claims[2], claims[0]}
	if err != nil || !reflect.DeepEqual(all, want) {
		t.Fatalf("connectorClaims of every network = %+v, %v; want %+v", all, err, want)
	}
	web, err := connectorClaims(ctx, db, csTunnelA, "web")
	if err != nil || !reflect.DeepEqual(web, []claimRow{claims[2], claims[0]}) {
		t.Errorf("connectorClaims on web = %+v, %v; want the two web claims in claimant order", web, err)
	}
	none, err := connectorClaims(ctx, db, "0a1b2c3d-0000-4000-8000-0000000000ff", "")
	if err != nil || none == nil || len(none) != 0 {
		t.Errorf("connectorClaims of a tunnel without claims = %#v, %v; want an empty, non-nil slice", none, err)
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error { return insertClaim(ctx, tx, claims[0]) })
	if err == nil || !strings.Contains(err.Error(), claims[0].Claimant) {
		t.Errorf("a duplicate claim: error %v, want one naming the claimant", err)
	}
	csWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		if err := deleteClaim(ctx, tx, csTunnelA, "web", "expose:a.example.com"); err != nil {
			return err
		}
		return deleteClaim(ctx, tx, csTunnelA, "web", "expose:nobody.example.com")
	})
	left, _ := connectorClaims(ctx, db, csTunnelA, "")
	if !reflect.DeepEqual(left, []claimRow{claims[1], claims[0]}) {
		t.Errorf("claims after the delete = %+v, want the two others of the tunnel", left)
	}
	if other, _ := connectorClaims(ctx, db, csTunnelB, ""); len(other) != 1 {
		t.Errorf("the other tunnel's claims = %+v, want its one claim", other)
	}
}

// TestCsMigrationsEnforceTheSchema: the agent migration creates both tables as STRICT tables whose preexisting column
// accepts 0 and 1 only, and the module's two migration sets are separate.
func TestCsMigrationsEnforceTheSchema(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_attachments (tunnel_id, network, claimant, change_id, preexisting) VALUES ('t', 'n', 'c', 'x', 2)`)
		return err
	})
	if err == nil {
		t.Error("a preexisting value of 2 was accepted")
	}
	err = h.db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO cloudflare_connectors (tunnel_id, container_id, token_ct, change_id, created_at) VALUES ('t', 'c', 'text, not a blob', 'x', 'y')`)
		return err
	})
	if err == nil {
		t.Error("a text value was accepted in the token_ct blob column of a STRICT table")
	}
	agent, err := fs.ReadFile(agentMigrations(), "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(agent), "cloudflare_connectors") || strings.Contains(string(agent), "cloudflare_hostnames") {
		t.Errorf("agent 0001_cloudflare.sql = %q, %v; want the migration that creates the connector tables only", agent, err)
	}
	ctl, err := fs.ReadFile(controllerMigrations(), "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(ctl), "cloudflare_hostnames") || strings.Contains(string(ctl), "cloudflare_connectors") {
		t.Errorf("controller 0001_cloudflare.sql = %q, %v; want the controller tables only", ctl, err)
	}
}

// TestCsTokenSealIsBoundToTheTunnel: the token is sealed with the associated data of its tunnel, so a ciphertext does
// not open under another tunnel's.
func TestCsTokenSealIsBoundToTheTunnel(t *testing.T) {
	if got := connectorSealAD(csTunnelA); got != "cloudflare_connectors.token_ct."+csTunnelA {
		t.Fatalf("connectorSealAD = %q", got)
	}
	sealer := coretest.Sealer{}
	ct, err := sealer.Seal(connectorSealAD(csTunnelA), []byte(agToken(csTunnelA)))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := sealer.Open(connectorSealAD(csTunnelA), ct); err != nil || string(pt) != agToken(csTunnelA) {
		t.Errorf("open under its own tunnel = %q, %v", pt, err)
	}
	if _, err := sealer.Open(connectorSealAD(csTunnelB), ct); err == nil {
		t.Error("a token sealed for one tunnel opened under another tunnel's associated data")
	}
}
```

Create `internal/modules/cloudflare/connector_deps_test.go`:

```go
package cloudflare

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const agTunnel = "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6f"

func TestAgNamesAndConstants(t *testing.T) {
	if got := []string{tokenDir, tokenMount}; !slices.Equal(got, []string{"/var/lib/darkpanel/agent/cloudflare-tokens", "/run/darkpanel/token"}) {
		t.Errorf("tokenDir and tokenMount %q, want the spec's paths", got)
	}
	if got := connectorName(agTunnel); got != "darkpanel-cloudflared-"+agTunnel {
		t.Errorf("connectorName = %q", got)
	}
	if got := tokenFileName(agTunnel); got != agTunnel+".token" {
		t.Errorf("tokenFileName = %q", got)
	}
}

func TestAgTunnelIDProblem(t *testing.T) {
	tests := []struct {
		name, id string
		bad      bool
	}{
		{"a lower-case uuid", agTunnel, false},
		{"all zeros", "00000000-0000-0000-0000-000000000000", false},
		{"upper case", strings.ToUpper(agTunnel), true},
		{"no dashes", strings.ReplaceAll(agTunnel, "-", ""), true},
		{"empty", "", true},
		{"a name", "darkpanel-c1", true},
		{"a trailing newline", agTunnel + "\n", true},
		{"a path", "../" + agTunnel, true},
		{"too long", agTunnel + "0", true},
		{"a non-hex digit", "9f1c2e3a-4b5d-4c6e-8f70-1a2b3c4d5e6g", true},
	}
	for _, tc := range tests {
		if got := tunnelIDProblem(tc.id); (got != "") != tc.bad {
			t.Errorf("%s: tunnelIDProblem(%q) = %q, want a problem: %t", tc.name, tc.id, got, tc.bad)
		}
	}
}

func TestAgClaimantProblem(t *testing.T) {
	tests := []struct {
		name, claimant string
		bad            bool
	}{
		{"an expose claimant", "expose:app.example.com", false},
		{"one byte", "x", false},
		{"the longest", strings.Repeat("a", 300), false},
		{"one byte too long", strings.Repeat("a", 301), true},
		{"empty", "", true},
		{"a space", "expose: app", true},
		{"a tab", "expose:\tapp", true},
		{"a newline", "expose:app\n", true},
		{"a control character", "expose:\x00app", true},
	}
	for _, tc := range tests {
		if got := claimantProblem(tc.claimant); (got != "") != tc.bad {
			t.Errorf("%s: claimantProblem = %q, want a problem: %t", tc.name, got, tc.bad)
		}
	}
}

func TestAgNetworkNamePattern(t *testing.T) {
	for name, ok := range map[string]bool{
		"web": true, "my_net-1.x": true, "A": true, strings.Repeat("a", 128): true,
		"": false, "-web": false, ".web": false, "web net": false, "web|x": false, "a/b": false, strings.Repeat("a", 129): false,
	} {
		if got := networkNamePattern.MatchString(name); got != ok {
			t.Errorf("networkNamePattern.MatchString(%q) = %t, want %t", name, got, ok)
		}
	}
}

// TestAgManifestDeclaresTheTokenDirectoryAndTheAgentTables: the token directory is a managed directory whose names are
// "<tunnel id>.token", the agent migrations are the module's, and the registry accepts the manifest.
func TestAgManifestDeclaresTheTokenDirectoryAndTheAgentTables(t *testing.T) {
	m := Module().Manifest()
	if len(m.OwnedPaths) != 1 {
		t.Fatalf("owned paths %+v, want the token directory only", m.OwnedPaths)
	}
	op := m.OwnedPaths[0]
	if op.Path != tokenDir || op.Kind != core.OwnedManagedDir {
		t.Errorf("owned path %+v, want %s as a managed-dir", op, tokenDir)
	}
	for name, ok := range map[string]bool{
		agTunnel + ".token": true, strings.ToUpper(agTunnel) + ".token": false, agTunnel: false, agTunnel + ".tokens": false,
		"x.token": false, "../" + agTunnel + ".token": false, agTunnel + ".token\n": false,
	} {
		if got := agManagedName.MatchString(name); got != ok {
			t.Errorf("name pattern matches %q: %t, want %t", name, got, ok)
		}
	}
	if m.Migrations[core.SideAgent] == nil || m.Migrations[core.SideController] == nil || len(m.Migrations) != 2 {
		t.Errorf("migrations %v, want the controller's and the agent's", m.Migrations)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}

// TestAgProductionDepsReachNothing: in a test binary the production wiring dials a socket that never exists, never the
// host's Docker daemon.
func TestAgProductionDepsReachNothing(t *testing.T) {
	m, ok := Module().(module)
	if !ok || m.c.a.docker == nil {
		t.Fatalf("Module() = %#v, want a module over the production agent wiring", Module())
	}
	if productionCF().a.docker == nil {
		t.Fatal("productionCF has no agent Engine")
	}
	d := productionAgentDeps().docker
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := d.InspectContainer(ctx, "darkpanel-cloudflared-x"); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("InspectContainer error %v, want the Engine unavailable", err)
	}
	s, err := d.PullImage(ctx, "cloudflare/cloudflared:2025.4.0")
	if err == nil || s != nil {
		t.Errorf("PullImage = %v, %v; want a nil stream and an error", s, err)
	}
}

func TestAgEngineCoded(t *testing.T) {
	down := errors.Join(errors.New("dial unix"), dockerapi.ErrUnavailable)
	coded := engineCoded(down)
	if errcode.From(coded).Code != errcode.DockerEngineUnavailable || !errors.Is(coded, dockerapi.ErrUnavailable) {
		t.Errorf("engineCoded(unreachable) = %v, want docker.engine_unavailable wrapping the cause", coded)
	}
	other := &dockerapi.APIError{StatusCode: 409, Message: "conflict"}
	if got := engineCoded(other); !errors.Is(got, other) || errors.Is(got, dockerapi.ErrUnavailable) || errcode.From(got).Code == errcode.DockerEngineUnavailable {
		t.Errorf("engineCoded(other) = %v, want it unchanged", got)
	}
	if engineCoded(nil) != nil {
		t.Error("engineCoded(nil) is not nil")
	}
}
```

Create `internal/modules/cloudflare/connector_testhost_test.go`:

```go
package cloudflare

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

var (
	_ connectorDocker = (*agDocker)(nil)
	_ connectorDocker = engineClient{}
	_ core.FileWriter = (*agFiles)(nil)
)

// agStart is the time the agent test host's clock starts at.
var agStart = time.Date(2026, 11, 3, 12, 0, 0, 0, time.UTC)

// agOperator is the principal every agent test host call runs as.
var agOperator = core.Principal{Actor: "user:u1", Scope: core.ScopeOperate}

// agManagedName is the file-name pattern the module declares for its token directory.
var agManagedName = regexp.MustCompile(ownedPaths()[0].Pattern)

// agID returns a 64-digit hex id derived from seed.
func agID(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return hex.EncodeToString(sum[:])
}

func agAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

// agContainer builds a stopped container named name; the options change it.
func agContainer(name string, opts ...func(*dockerapi.ContainerJSON)) dockerapi.ContainerJSON {
	c := dockerapi.ContainerJSON{
		ID:      agID("container:" + name),
		Name:    "/" + name,
		Created: "2026-10-01T00:00:00Z",
		Image:   "sha256:" + agID("image:"+name),
		State:   dockerapi.ContainerState{Status: "exited", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  dockerapi.ContainerConfig{Image: name + ":latest"},
	}
	for _, o := range opts {
		o(&c)
	}
	return c
}

// agRunning marks the container running.
func agRunning(c *dockerapi.ContainerJSON) {
	c.State.Status = "running"
	c.State.Running = true
	c.State.Pid = 1
	c.State.StartedAt = "2026-10-01T00:00:01Z"
}

// agLabelled sets the container's labels.
func agLabelled(m map[string]string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.Config.Labels = maps.Clone(m) }
}

// agOnNetwork attaches the container to the network name with the IPv4 address ip; the endpoint is keyed by the
// network's name.
func agOnNetwork(network, ip string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) {
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		netID := agID("network:" + network)
		c.NetworkSettings.Networks[network] = &dockerapi.EndpointSettings{
			NetworkID:  netID,
			EndpointID: agID("endpoint:" + c.ID + "@" + netID),
			IPAddress:  netip.MustParseAddr(ip),
		}
	}
}

// agNetwork builds a network named name that uses the driver.
func agNetwork(name, driver string) dockerapi.Network {
	return dockerapi.Network{ID: agID("network:" + name), Name: name, Created: "2026-10-01T00:00:00Z", Scope: "local", Driver: driver}
}

// agToken builds a well-formed tunnel token for the tunnel: the standard base64 of the JSON object
// {"a":<account tag>,"t":<tunnel id>,"s":<base64 secret>}.
func agToken(tunnel string) string {
	secret := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	doc := fmt.Sprintf(`{"a":%q,"t":%q,"s":%q}`, "0123456789abcdef0123456789abcdef", tunnel, secret)
	return base64.StdEncoding.EncodeToString([]byte(doc))
}

func agCloneEndpoint(e *dockerapi.EndpointSettings) *dockerapi.EndpointSettings {
	if e == nil {
		return nil
	}
	c := *e
	c.Aliases = slices.Clone(e.Aliases)
	c.DNSNames = slices.Clone(e.DNSNames)
	if e.IPAMConfig != nil {
		v := *e.IPAMConfig
		c.IPAMConfig = &v
	}
	return &c
}

// agCloneContainer copies c so that no map, slice or endpoint pointer is shared with it.
func agCloneContainer(c dockerapi.ContainerJSON) dockerapi.ContainerJSON {
	c.Config.Env = slices.Clone(c.Config.Env)
	c.Config.Cmd = slices.Clone(c.Config.Cmd)
	c.Config.Entrypoint = slices.Clone(c.Config.Entrypoint)
	c.Config.Labels = maps.Clone(c.Config.Labels)
	c.HostConfig.Binds = slices.Clone(c.HostConfig.Binds)
	c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
	if c.NetworkSettings.Networks != nil {
		nets := make(map[string]*dockerapi.EndpointSettings, len(c.NetworkSettings.Networks))
		for k, e := range c.NetworkSettings.Networks {
			nets[k] = agCloneEndpoint(e)
		}
		c.NetworkSettings.Networks = nets
	}
	c.Mounts = slices.Clone(c.Mounts)
	return c
}

func agCloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	n.Containers = maps.Clone(n.Containers)
	return n
}

// agMatch reports whether an object with these labels and name passes the Engine filters the fake supports: label
// (every value must match), name (substring of any value) and status (any value).
func agMatch(f dockerapi.Filters, labels map[string]string, name, status string) bool {
	for _, want := range f["label"] {
		k, v, hasValue := strings.Cut(want, "=")
		got, ok := labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	if names := f["name"]; len(names) > 0 && !slices.ContainsFunc(names, func(s string) bool { return strings.Contains(name, s) }) {
		return false
	}
	if statuses := f["status"]; len(statuses) > 0 && !slices.Contains(statuses, status) {
		return false
	}
	return true
}

// agPull is the progress stream of a fake pull.
type agPull struct {
	msgs []dockerapi.PullMessage
	pos  int
}

func (p *agPull) Next() (dockerapi.PullMessage, error) {
	if p.pos >= len(p.msgs) {
		return dockerapi.PullMessage{}, io.EOF
	}
	m := p.msgs[p.pos]
	p.pos++
	return m, m.Err()
}

func (p *agPull) Close() error { return nil }

// agDocker is an in-memory Docker Engine holding the three predefined networks (bridge, host and none) and no
// container or image.
type agDocker struct {
	mu         sync.Mutex
	clock      core.Clock
	containers map[string]dockerapi.ContainerJSON // by id
	networks   map[string]dockerapi.Network       // by id
	images     map[string]bool
	faults     map[string][]error
	called     []string
	created    int
}

func newAGDocker(clock core.Clock) *agDocker {
	d := &agDocker{
		clock:      clock,
		containers: map[string]dockerapi.ContainerJSON{},
		networks:   map[string]dockerapi.Network{},
		images:     map[string]bool{},
		faults:     map[string][]error{},
	}
	for name, driver := range map[string]string{"bridge": "bridge", "host": "host", "none": "null"} {
		n := agNetwork(name, driver)
		d.networks[n.ID] = n
	}
	return d
}

// Fail queues err as the answer of the next call of the method op; the call changes nothing.
func (d *agDocker) Fail(op string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults[op] = append(d.faults[op], err)
}

// calls returns the names of the methods called, in order; a failed call is recorded too.
func (d *agDocker) calls() []string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return slices.Clone(d.called)
}

// enter records a call and returns the failure queued for it; the caller holds d.mu.
func (d *agDocker) enter(op string) error {
	d.called = append(d.called, op)
	if q := d.faults[op]; len(q) > 0 {
		d.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func (d *agDocker) now() string { return d.clock.Now().UTC().Format(time.RFC3339Nano) }

func (d *agDocker) putContainer(c dockerapi.ContainerJSON) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.containers[c.ID] = agCloneContainer(c)
}

func (d *agDocker) putNetwork(n dockerapi.Network) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.networks[n.ID] = agCloneNetwork(n)
}

func (d *agDocker) putImage(ref string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.images[ref] = true
}

// hasImage reports whether the Engine holds the image.
func (d *agDocker) hasImage(ref string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.images[ref]
}

// container returns the container with the id or name.
func (d *agDocker) container(idOrName string) (dockerapi.ContainerJSON, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, false
	}
	return agCloneContainer(d.containers[id]), true
}

// network returns the network with the id or name.
func (d *agDocker) network(idOrName string) (dockerapi.Network, bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, false
	}
	return agCloneNetwork(d.networks[id]), true
}

func (d *agDocker) containerID(idOrName string) (string, bool) {
	if _, ok := d.containers[idOrName]; ok {
		return idOrName, true
	}
	for id, c := range d.containers {
		if strings.TrimPrefix(c.Name, "/") == strings.TrimPrefix(idOrName, "/") {
			return id, true
		}
	}
	return "", false
}

func (d *agDocker) networkID(idOrName string) (string, bool) {
	if _, ok := d.networks[idOrName]; ok {
		return idOrName, true
	}
	for id, n := range d.networks {
		if n.Name == idOrName {
			return id, true
		}
	}
	return "", false
}

func (d *agDocker) noContainer(id string) error {
	return agAPIError(http.StatusNotFound, "No such container: %s", id)
}

func (d *agDocker) noNetwork(id string) error {
	return agAPIError(http.StatusNotFound, "network %s not found", id)
}

func (d *agDocker) ListContainers(_ context.Context, o dockerapi.ListContainersOptions) ([]dockerapi.ContainerSummary, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListContainers"); err != nil {
		return nil, err
	}
	var out []dockerapi.ContainerSummary
	for _, c := range d.containers {
		name := strings.TrimPrefix(c.Name, "/")
		if (!o.All && !c.State.Running) || !agMatch(o.Filters, c.Config.Labels, name, c.State.Status) {
			continue
		}
		cc := agCloneContainer(c)
		out = append(out, dockerapi.ContainerSummary{
			ID: c.ID, Names: []string{c.Name}, Image: c.Config.Image, ImageID: c.Image, State: c.State.Status, Status: c.State.Status,
			Labels: cc.Config.Labels, NetworkSettings: &dockerapi.SummaryNetworkSettings{Networks: cc.NetworkSettings.Networks}, Mounts: cc.Mounts,
		})
	}
	slices.SortFunc(out, func(a, b dockerapi.ContainerSummary) int { return strings.Compare(a.Names[0], b.Names[0]) })
	return out, nil
}

func (d *agDocker) InspectContainer(_ context.Context, idOrName string) (dockerapi.ContainerJSON, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	id, ok := d.containerID(idOrName)
	if !ok {
		return dockerapi.ContainerJSON{}, d.noContainer(idOrName)
	}
	return agCloneContainer(d.containers[id]), nil
}

// CreateContainer creates a stopped container. Without an endpoint in the request it joins the network the
// HostConfig names, or the default bridge network when it names none. A mount or a "src:dst[:ro]" bind is reported
// as a MountPoint.
func (d *agDocker) CreateContainer(_ context.Context, cfg dockerapi.ContainerCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateContainer"); err != nil {
		return "", err
	}
	if cfg.Name != "" {
		if _, taken := d.containerID(cfg.Name); taken {
			return "", agAPIError(http.StatusConflict, "Conflict. The container name %q is already in use", "/"+cfg.Name)
		}
	}
	if !d.images[cfg.Image] {
		return "", agAPIError(http.StatusNotFound, "No such image: %s", cfg.Image)
	}
	d.created++
	name := cfg.Name
	if name == "" {
		name = fmt.Sprintf("ag-container-%d", d.created)
	}
	c := dockerapi.ContainerJSON{
		ID:      agID(fmt.Sprintf("container:%s#%d", name, d.created)),
		Name:    "/" + name,
		Created: d.now(),
		Image:   "sha256:" + agID("image:"+cfg.Image),
		State:   dockerapi.ContainerState{Status: "created", StartedAt: "0001-01-01T00:00:00Z", FinishedAt: "0001-01-01T00:00:00Z"},
		Config:  cfg.ContainerConfig,
	}
	if cfg.HostConfig != nil {
		c.HostConfig = *cfg.HostConfig
	}
	attach := map[string]*dockerapi.EndpointSettings{}
	if cfg.NetworkingConfig != nil {
		maps.Copy(attach, cfg.NetworkingConfig.EndpointsConfig)
	}
	if len(attach) == 0 {
		mode := c.HostConfig.NetworkMode
		if mode == "" || mode == "default" {
			mode = "bridge"
		}
		attach[mode] = nil
	}
	for netName, ep := range attach {
		id, ok := d.networkID(netName)
		if !ok {
			return "", d.noNetwork(netName)
		}
		n := d.networks[id]
		e := agCloneEndpoint(ep)
		if e == nil {
			e = &dockerapi.EndpointSettings{}
		}
		e.NetworkID, e.EndpointID = n.ID, agID("endpoint:"+c.ID+"@"+n.ID)
		if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
			e.IPAddress = e.IPAMConfig.IPv4Address
		}
		if c.NetworkSettings.Networks == nil {
			c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
		}
		c.NetworkSettings.Networks[n.Name] = e
	}
	for _, m := range c.HostConfig.Mounts {
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: m.Type, Source: m.Source, Destination: m.Target, RW: !m.ReadOnly})
	}
	for _, b := range c.HostConfig.Binds {
		parts := strings.Split(b, ":")
		if len(parts) < 2 {
			return "", agAPIError(http.StatusBadRequest, "invalid bind %q", b)
		}
		c.Mounts = append(c.Mounts, dockerapi.MountPoint{Type: "bind", Source: parts[0], Destination: parts[1], RW: !slices.Contains(parts[2:], "ro")})
	}
	d.containers[c.ID] = agCloneContainer(c)
	return c.ID, nil
}

func (d *agDocker) StartContainer(_ context.Context, id string) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("StartContainer"); err != nil {
		return false, err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return false, d.noContainer(id)
	}
	c := d.containers[cid]
	if c.State.Running {
		return false, nil
	}
	c.State.Status, c.State.Running, c.State.Pid, c.State.StartedAt = "running", true, 1, d.now()
	d.containers[cid] = c
	return true, nil
}

func (d *agDocker) StopContainer(_ context.Context, id string, _ time.Duration) (bool, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("StopContainer"); err != nil {
		return false, err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return false, d.noContainer(id)
	}
	c := d.containers[cid]
	if !c.State.Running {
		return false, nil
	}
	c.State.Status, c.State.Running, c.State.Pid, c.State.FinishedAt = "exited", false, 0, d.now()
	d.containers[cid] = c
	return true, nil
}

func (d *agDocker) RemoveContainer(_ context.Context, id string, o dockerapi.RemoveContainerOptions) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveContainer"); err != nil {
		return err
	}
	cid, ok := d.containerID(id)
	if !ok {
		return d.noContainer(id)
	}
	if d.containers[cid].State.Running && !o.Force {
		return agAPIError(http.StatusConflict, "cannot remove container %q: container is running: stop the container before removing or force remove", id)
	}
	delete(d.containers, cid)
	return nil
}

// attached returns the endpoints of containers on network n, keyed by container id.
func (d *agDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			out[c.ID] = dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
		}
	}
	return out
}

func (d *agDocker) InspectNetwork(_ context.Context, idOrName string) (dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, d.noNetwork(idOrName)
	}
	n := agCloneNetwork(d.networks[id])
	n.Containers = d.attached(n)
	return n, nil
}

func (d *agDocker) ConnectNetwork(_ context.Context, network, container string, ep *dockerapi.EndpointSettings) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ConnectNetwork"); err != nil {
		return err
	}
	nid, ok := d.networkID(network)
	if !ok {
		return d.noNetwork(network)
	}
	cid, ok := d.containerID(container)
	if !ok {
		return d.noContainer(container)
	}
	n, c := d.networks[nid], d.containers[cid]
	if c.NetworkSettings.Networks[n.Name] != nil {
		return agAPIError(http.StatusForbidden, "endpoint with name %s already exists in network %s", strings.TrimPrefix(c.Name, "/"), n.Name)
	}
	e := agCloneEndpoint(ep)
	if e == nil {
		e = &dockerapi.EndpointSettings{}
	}
	e.NetworkID, e.EndpointID = n.ID, agID("endpoint:"+c.ID+"@"+n.ID)
	if e.IPAMConfig != nil && e.IPAMConfig.IPv4Address.IsValid() {
		e.IPAddress = e.IPAMConfig.IPv4Address
	}
	if e.IPAMConfig != nil && e.IPAMConfig.IPv6Address.IsValid() {
		e.GlobalIPv6Address = e.IPAMConfig.IPv6Address
	}
	if c.NetworkSettings.Networks == nil {
		c.NetworkSettings.Networks = map[string]*dockerapi.EndpointSettings{}
	}
	c.NetworkSettings.Networks[n.Name] = e
	d.containers[cid] = c
	return nil
}

func (d *agDocker) DisconnectNetwork(_ context.Context, network, container string, _ bool) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("DisconnectNetwork"); err != nil {
		return err
	}
	nid, ok := d.networkID(network)
	if !ok {
		return d.noNetwork(network)
	}
	cid, ok := d.containerID(container)
	if !ok {
		return d.noContainer(container)
	}
	n, c := d.networks[nid], d.containers[cid]
	if c.NetworkSettings.Networks[n.Name] == nil {
		return agAPIError(http.StatusForbidden, "container %s is not connected to network %s", container, n.Name)
	}
	delete(c.NetworkSettings.Networks, n.Name)
	d.containers[cid] = c
	return nil
}

func (d *agDocker) InspectImage(_ context.Context, ref string) (dockerapi.ImageInspect, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectImage"); err != nil {
		return dockerapi.ImageInspect{}, err
	}
	if !d.images[ref] {
		return dockerapi.ImageInspect{}, agAPIError(http.StatusNotFound, "No such image: %s", ref)
	}
	img := dockerapi.ImageInspect{ID: "sha256:" + agID("image:"+ref), Created: d.now(), Architecture: "amd64", Os: "linux", Config: &dockerapi.ContainerConfig{}}
	if strings.Contains(ref, "@") {
		img.RepoDigests = []string{ref}
	} else {
		img.RepoTags = []string{ref}
	}
	return img, nil
}

func (d *agDocker) PullImage(_ context.Context, ref string) (pullStream, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("PullImage"); err != nil {
		return nil, err
	}
	d.images[ref] = true
	return &agPull{msgs: []dockerapi.PullMessage{{Status: "Pulling from " + ref}, {Status: "Status: Downloaded newer image for " + ref}}}, nil
}

// agFile is a file the module wrote through WriteManaged.
type agFile struct {
	Data     []byte
	Mode     fs.FileMode
	UID, GID int
}

// agFiles is an in-memory core.FileWriter that accepts only the module's own token directory and file names, as the
// platform's would, and records every call. The module uses no other method.
type agFiles struct {
	mu     sync.Mutex
	files  map[string]agFile
	called []string
	faults map[string][]error
}

func newAGFiles() *agFiles { return &agFiles{files: map[string]agFile{}, faults: map[string][]error{}} }

// Fail queues err as the answer of the next call of the method op, WriteManaged or RemoveManaged; the call changes
// nothing.
func (f *agFiles) Fail(op string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.faults[op] = append(f.faults[op], err)
}

// file returns the file stored at dir/name.
func (f *agFiles) file(dir, name string) (agFile, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	file, ok := f.files[dir+"/"+name]
	file.Data = bytes.Clone(file.Data)
	return file, ok
}

// calls returns the calls made, in order, as "WriteManaged <dir>/<name>" and "RemoveManaged <dir>/<name>".
func (f *agFiles) calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.called)
}

// enter records a call, checks the arguments as the platform does and returns the failure queued for it; the caller
// holds f.mu.
func (f *agFiles) enter(op, module, dir, name string) error {
	f.called = append(f.called, op+" "+dir+"/"+name)
	switch {
	case module != ID:
		return fmt.Errorf("fake: module %q is not %q", module, ID)
	case dir != tokenDir:
		return fmt.Errorf("fake: %s is not an owned managed directory", dir)
	case name == "." || name == ".." || filepath.Base(name) != name || !agManagedName.MatchString(name):
		return fmt.Errorf("fake: %q does not match the owned name pattern", name)
	}
	if q := f.faults[op]; len(q) > 0 {
		f.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func (f *agFiles) WriteManaged(_ context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("WriteManaged", module, dir, name); err != nil {
		return err
	}
	f.files[dir+"/"+name] = agFile{Data: bytes.Clone(data), Mode: mode, UID: uid, GID: gid}
	return nil
}

// RemoveManaged removes the file; an absent one is not an error, as on the platform.
func (f *agFiles) RemoveManaged(_ context.Context, module, dir, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.enter("RemoveManaged", module, dir, name); err != nil {
		return err
	}
	delete(f.files, dir+"/"+name)
	return nil
}

var errAGUnused = errors.New("fake: not used by the cloudflare module")

func (f *agFiles) WriteFile(context.Context, string, string, []byte, fs.FileMode) error {
	return errAGUnused
}
func (f *agFiles) ReplaceBlock(context.Context, string, string, string, []byte) error {
	return errAGUnused
}
func (f *agFiles) RemoveBlock(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) Remove(context.Context, string, string) error              { return errAGUnused }
func (f *agFiles) Restore(context.Context, string, core.RestoreEntry) error  { return errAGUnused }
func (f *agFiles) ReadSysctl(context.Context, string, string) (string, error) {
	return "", errAGUnused
}
func (f *agFiles) WriteSysctl(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) Rename(context.Context, string, string, string) error      { return errAGUnused }
func (f *agFiles) EnsureServiceDir(context.Context, string, string) error    { return errAGUnused }
func (f *agFiles) RenameIn(context.Context, string, string, string, string) error {
	return errAGUnused
}
func (f *agFiles) RemoveIn(context.Context, string, string, string) error { return errAGUnused }
func (f *agFiles) RemoveServiceDir(context.Context, string, string) error { return errAGUnused }
func (f *agFiles) Owns(context.Context, string, string) (bool, error)     { return false, errAGUnused }

// agHost is the fake host an agent-side Cloudflare test runs on: an agent database with the core and the module's
// migrations, a manual clock, an effects journal, a file writer and an in-memory Docker Engine.
type agHost struct {
	t       *testing.T
	db      *store.DB
	clock   *coretest.FakeClock
	journal *cfJournal
	files   *agFiles
	docker  *agDocker
}

// newAGHost returns a host whose Engine holds only the predefined networks, whose token directory is empty and whose
// clock reads agStart.
func newAGHost(t *testing.T) *agHost {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	clock := coretest.NewFakeClock(agStart)
	db, err := store.Open(ctx, filepath.Join(dir, "agent.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	coreSet, err := store.CoreMigrations(store.SideAgent)
	if err != nil {
		t.Fatal(err)
	}
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideAgent, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: agStart,
	}, coreSet, []store.MigrationSet{{Module: ID, FS: agentMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("cloudflare agent migrations failed: %v", res.Failed[0].Err)
	}
	return &agHost{t: t, db: db, clock: clock, journal: &cfJournal{}, files: newAGFiles(), docker: newAGDocker(clock)}
}

// cf returns a cf whose agent dependencies are the host's Engine.
func (h *agHost) cf() cf { return cf{a: agentDeps{docker: h.docker}} }

// env returns the agent-side environment of a call of the change; an empty changeID is a plan or a read, which has no
// effects journal.
func (h *agHost) env(changeID string) core.Env {
	env := core.Env{
		HostID:    "h1",
		ChangeID:  changeID,
		Principal: agOperator,
		DB:        h.db,
		Files:     h.files,
		Sealer:    coretest.Sealer{},
		Clock:     h.clock,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	if changeID != "" {
		h.journal.changeID = changeID
		env.Effects = h.journal
	}
	return env
}

// TestAgHostEnvAndJournal: env carries the host's database, files, sealer and clock, gives the journal only to a
// change, and the journal records the effects of the change.
func TestAgHostEnvAndJournal(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	if env := h.env(""); env.Effects != nil || env.DB != h.db || env.Files != h.files || env.Sealer == nil || env.Clock == nil || env.ChangeID != "" ||
		env.Principal.Scope != core.ScopeOperate {
		t.Errorf("env(\"\") = %+v", env)
	}
	env := h.env("c1")
	if env.Effects == nil || env.ChangeID != "c1" {
		t.Fatalf("env(c1) = %+v", env)
	}
	seq, err := env.Effects.Intend(ctx, core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.connector:t"}, nil)
	if err != nil || seq != 1 {
		t.Fatalf("Intend = %d, %v", seq, err)
	}
	if err := env.Effects.Made(ctx, seq, "cloudflare.connector:t", core.StateDoc{State: []byte(`{}`)}); err != nil {
		t.Fatal(err)
	}
	if got := h.journal.effects(); len(got) != 1 || got[0].State != core.EffectMade || got[0].ChangeID != "c1" {
		t.Errorf("effects = %+v", got)
	}
	if cf := h.cf(); cf.a.docker != connectorDocker(h.docker) {
		t.Error("cf().a.docker is not the host's Engine")
	}
	if got := agToken(agTunnel); got == "" {
		t.Error("agToken is empty")
	}
}

// TestAgTokenIsTheDocumentedToken: agToken is the standard base64 of {"a":<account tag>,"t":<tunnel id>,"s":<base64
// secret>}.
func TestAgTokenIsTheDocumentedToken(t *testing.T) {
	raw, err := base64.StdEncoding.DecodeString(agToken(agTunnel))
	if err != nil {
		t.Fatal(err)
	}
	var doc struct{ A, T, S string }
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	secret, err := base64.StdEncoding.DecodeString(doc.S)
	if doc.T != agTunnel || len(doc.A) != 32 || err != nil || len(secret) != 32 {
		t.Errorf("token document %+v (secret %d bytes, %v), want the tunnel, a 32-character account tag and a 32-byte secret", doc, len(secret), err)
	}
	if agToken("00000000-0000-0000-0000-000000000000") == agToken(agTunnel) {
		t.Error("two tunnels share a token")
	}
}

// TestAgFilesRecordWritesAndRefuseForeignPaths: a write is stored with its mode and owner and read back as a copy,
// a removal of an absent file succeeds, and another module, directory or name is refused as the platform would.
func TestAgFilesRecordWritesAndRefuseForeignPaths(t *testing.T) {
	ctx := context.Background()
	f := newAGFiles()
	name := tokenFileName(agTunnel)
	data := []byte("secret")
	if err := f.WriteManaged(ctx, ID, tokenDir, name, data, 0o400, 65532, 65532); err != nil {
		t.Fatal(err)
	}
	data[0] = 'X'
	got, ok := f.file(tokenDir, name)
	if !ok || string(got.Data) != "secret" || got.Mode != 0o400 || got.UID != 65532 || got.GID != 65532 {
		t.Fatalf("file = %+v, %t; want the bytes written, mode 0400 and owner 65532:65532", got, ok)
	}
	got.Data[0] = 'Y'
	if again, _ := f.file(tokenDir, name); string(again.Data) != "secret" {
		t.Error("file returned the stored bytes, not a copy")
	}
	for _, c := range []struct{ module, dir, name string }{
		{"npm", tokenDir, name}, {ID, "/var/lib/darkpanel/agent/npm-backups", name}, {ID, tokenDir, "x.token"}, {ID, tokenDir, "../" + name},
	} {
		if err := f.WriteManaged(ctx, c.module, c.dir, c.name, nil, 0o400, 0, 0); err == nil {
			t.Errorf("WriteManaged(%q, %q, %q) was accepted", c.module, c.dir, c.name)
		}
		if err := f.RemoveManaged(ctx, c.module, c.dir, c.name); err == nil {
			t.Errorf("RemoveManaged(%q, %q, %q) was accepted", c.module, c.dir, c.name)
		}
	}
	if err := f.WriteFile(ctx, ID, "/etc/x", nil, 0o600); err == nil {
		t.Error("WriteFile was accepted")
	}
	f.Fail("RemoveManaged", errors.New("disk gone"))
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err == nil || err.Error() != "disk gone" {
		t.Errorf("RemoveManaged with a queued failure = %v", err)
	}
	if _, ok := f.file(tokenDir, name); !ok {
		t.Error("a failed removal removed the file")
	}
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err != nil {
		t.Fatal(err)
	}
	if err := f.RemoveManaged(ctx, ID, tokenDir, name); err != nil {
		t.Errorf("removing an absent file: %v, want nil", err)
	}
	if _, ok := f.file(tokenDir, name); ok {
		t.Error("the file remains after its removal")
	}
	want := "WriteManaged " + tokenDir + "/" + name
	if calls := f.calls(); calls[0] != want || calls[len(calls)-1] != "RemoveManaged "+tokenDir+"/"+name {
		t.Errorf("calls = %v", calls)
	}
}

// TestAgDockerCreatesInspectsAndConnects: the Engine fake keeps what a create asked for, starts a container on the
// default bridge network, lists by label, connects and disconnects as Docker does and fails a call on request.
func TestAgDockerCreatesInspectsAndConnects(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	d := h.docker
	const image = "cloudflare/cloudflared:test@sha256:" + "a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1a1"
	if _, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{Name: "c1", ContainerConfig: dockerapi.ContainerConfig{Image: image}}); !dockerapi.IsNotFound(err) {
		t.Fatalf("create from an absent image: %v, want not found", err)
	}
	if _, err := d.InspectImage(ctx, image); !dockerapi.IsNotFound(err) {
		t.Fatalf("InspectImage of an absent image: %v, want not found", err)
	}
	if d.hasImage(image) {
		t.Fatal("the Engine holds an image before any pull")
	}
	stream, err := d.PullImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := stream.Next(); err != nil {
			break
		}
	}
	_ = stream.Close()
	if _, err := d.InspectImage(ctx, image); err != nil || !d.hasImage(image) {
		t.Fatalf("InspectImage after the pull: %v", err)
	}
	id, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{
		Name: "darkpanel-cloudflared-t",
		ContainerConfig: dockerapi.ContainerConfig{
			Image: image, Cmd: []string{"tunnel", "run"}, Labels: map[string]string{core.LabelModule: ID},
		},
		HostConfig: &dockerapi.HostConfig{
			RestartPolicy: dockerapi.RestartPolicy{Name: "unless-stopped"},
			Mounts:        []dockerapi.Mount{{Type: "bind", Source: "/host/file", Target: "/run/darkpanel/token", ReadOnly: true}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := d.CreateContainer(ctx, dockerapi.ContainerCreate{Name: "darkpanel-cloudflared-t", ContainerConfig: dockerapi.ContainerConfig{Image: image}}); err == nil {
		t.Error("a second container with the same name was created")
	}
	c, err := d.InspectContainer(ctx, "darkpanel-cloudflared-t")
	if err != nil {
		t.Fatal(err)
	}
	if c.ID != id || c.State.Status != "created" || c.Config.Image != image || c.HostConfig.RestartPolicy.Name != "unless-stopped" ||
		!slices.Equal(c.Config.Cmd, []string{"tunnel", "run"}) || len(c.Mounts) != 1 ||
		c.Mounts[0] != (dockerapi.MountPoint{Type: "bind", Source: "/host/file", Destination: "/run/darkpanel/token", RW: false}) {
		t.Errorf("created container %+v", c)
	}
	if c.NetworkSettings.Networks["bridge"] == nil || len(c.NetworkSettings.Networks) != 1 {
		t.Errorf("networks %v, want the default bridge only", c.NetworkSettings.Networks)
	}
	if changed, err := d.StartContainer(ctx, id); err != nil || !changed {
		t.Fatalf("StartContainer = %t, %v", changed, err)
	}
	if changed, err := d.StartContainer(ctx, id); err != nil || changed {
		t.Errorf("a second StartContainer = %t, %v; want unchanged", changed, err)
	}
	list, err := d.ListContainers(ctx, dockerapi.ListContainersOptions{Filters: dockerapi.Filters{"label": {core.LabelModule + "=" + ID}}})
	if err != nil || len(list) != 1 || list[0].ID != id || list[0].State != "running" {
		t.Errorf("ListContainers by label = %+v, %v", list, err)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err == nil {
		t.Error("a running container was removed without force")
	}

	d.putNetwork(agNetwork("web", "bridge"))
	d.putImage("app:latest")
	d.putContainer(agContainer("app", agRunning, agLabelled(map[string]string{"k": "v"}), agOnNetwork("web", "172.20.0.5")))
	app, ok := d.container("app")
	if !ok || !app.State.Running || app.Config.Labels["k"] != "v" || app.NetworkSettings.Networks["web"].IPAddress != netip.MustParseAddr("172.20.0.5") {
		t.Fatalf("seeded container %+v, %t", app, ok)
	}
	ep := &dockerapi.EndpointSettings{IPAMConfig: &dockerapi.EndpointIPAMConfig{IPv4Address: netip.MustParseAddr("172.20.0.9")}}
	if err := d.ConnectNetwork(ctx, "web", id, ep); err != nil {
		t.Fatal(err)
	}
	if err := d.ConnectNetwork(ctx, "web", id, nil); err == nil {
		t.Error("a container was connected to a network twice")
	}
	c, _ = d.container(id)
	if got := c.NetworkSettings.Networks["web"]; got == nil || got.IPAddress != netip.MustParseAddr("172.20.0.9") {
		t.Errorf("web endpoint %+v, want the fixed address", got)
	}
	n, err := d.InspectNetwork(ctx, "web")
	if err != nil || len(n.Containers) != 2 || n.Containers[id].Name != "darkpanel-cloudflared-t" || n.Containers[app.ID].Name != "app" {
		t.Errorf("InspectNetwork web = %+v, %v; want the connector and app attached", n.Containers, err)
	}
	if err := d.DisconnectNetwork(ctx, "web", id, false); err != nil {
		t.Fatal(err)
	}
	if err := d.DisconnectNetwork(ctx, "web", id, false); err == nil {
		t.Error("a container was disconnected from a network it is not on")
	}
	if _, err := d.InspectNetwork(ctx, "nowhere"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectNetwork of an unknown network: %v, want not found", err)
	}
	for name, driver := range map[string]string{"bridge": "bridge", "host": "host", "none": "null"} {
		if n, ok := d.network(name); !ok || n.Driver != driver {
			t.Errorf("predefined network %s = %+v, %t; want driver %s", name, n, ok, driver)
		}
	}

	before := len(d.calls())
	d.Fail("StopContainer", errors.New("daemon busy"))
	if _, err := d.StopContainer(ctx, id, time.Second); err == nil || err.Error() != "daemon busy" {
		t.Errorf("StopContainer with a queued failure = %v", err)
	}
	if c, _ := d.container(id); !c.State.Running {
		t.Error("a failed stop stopped the container")
	}
	if changed, err := d.StopContainer(ctx, id, time.Second); err != nil || !changed {
		t.Errorf("StopContainer = %t, %v", changed, err)
	}
	if err := d.RemoveContainer(ctx, id, dockerapi.RemoveContainerOptions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := d.InspectContainer(ctx, id); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectContainer after the removal: %v, want not found", err)
	}
	if got := d.calls()[before:]; !slices.Equal(got, []string{"StopContainer", "StopContainer", "RemoveContainer", "InspectContainer"}) {
		t.Errorf("calls since the failure = %v", got)
	}
}
```

In `internal/modules/cloudflare/register_test.go` replace `TestRegManifest` and its doc comment with:

```go
// TestRegManifest: the module is enabled by default, owns its controller and
// agent tables through its migrations, owns the token directory of the
// connectors, declares no resource or preflight check, and the registry
// accepts the whole manifest.
func TestRegManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest id %q version %d default-enabled %t, want %q 1 true", m.ID, m.Version, m.DefaultEnabled, ID)
	}
	if len(m.Resources) != 0 || len(m.Preflight) != 0 {
		t.Errorf("manifest declares %d resources and %d preflight checks, want none", len(m.Resources), len(m.Preflight))
	}
	if len(m.OwnedPaths) != 1 || m.OwnedPaths[0].Path != tokenDir || m.OwnedPaths[0].Kind != core.OwnedManagedDir ||
		m.OwnedPaths[0].Pattern != `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\.token$` {
		t.Errorf("owned paths %+v, want the token directory as a managed directory of <uuid>.token files", m.OwnedPaths)
	}
	mig := m.Migrations[core.SideController]
	if len(m.Migrations) != 2 || mig == nil || m.Migrations[core.SideAgent] == nil {
		t.Fatalf("migrations %v, want the controller's and the agent's", m.Migrations)
	}
	if b, err := fs.ReadFile(mig, "0001_cloudflare.sql"); err != nil || !strings.Contains(string(b), "cloudflare_hostnames") {
		t.Errorf("0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_hostnames", b, err)
	}
	if b, err := fs.ReadFile(m.Migrations[core.SideAgent], "0001_cloudflare.sql"); err != nil || !strings.Contains(string(b), "cloudflare_connectors") {
		t.Errorf("agent 0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_connectors", b, err)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/3 && export TMPDIR=/root/.cache/w6b-tmp/3 GOTOOLCHAIN=local
timeout 300 go vet ./internal/modules/cloudflare/
timeout 300 go test -count=1 -run 'TestCw|TestCs|TestAg|TestRegManifest' ./internal/modules/cloudflare/
```

Expected: both commands fail to build; the errors name `connectorRow`, `connectorDocker`, `engineClient`, `pullStream`, `ownedPaths`, `tokenDir`, `Connector` and the unknown `cf` field `a`, among others. Quote the output in the report.

- [ ] **Step 3: Implement**

1. `migrations/agent/0001_cloudflare.sql`, exactly the tables of Contracts "Agent tables", with the comments:

```sql
-- The Cloudflare module's agent tables (spec 7.2, 7.3), read and written only
-- through internal/modules/cloudflare. Times are fixed-width RFC 3339 UTC
-- text, so they compare as text.

-- One row per cloudflared connector on this host, by the tunnel's id. The
-- tunnel token is sealed with associated data
-- "cloudflare_connectors.token_ct.<tunnel id>".
CREATE TABLE cloudflare_connectors (
    tunnel_id    TEXT PRIMARY KEY,
    container_id TEXT NOT NULL,
    token_ct     BLOB NOT NULL,
    change_id    TEXT NOT NULL,
    created_at   TEXT NOT NULL
) STRICT;

-- Who needs a tunnel's connector attached to a network.
CREATE TABLE cloudflare_attachments (
    tunnel_id   TEXT NOT NULL,
    network     TEXT NOT NULL,
    claimant    TEXT NOT NULL,
    change_id   TEXT NOT NULL,
    preexisting INTEGER NOT NULL CHECK (preexisting IN (0, 1)),
    PRIMARY KEY (tunnel_id, network, claimant)
) STRICT;
```

2. `store.go`: the embed directive becomes `//go:embed migrations/controller/*.sql migrations/agent/*.sql`, so `storeMigrationsFS` holds both directories; `controllerMigrations` is unchanged.
3. `connector_wire.go`: the wire types of Contracts "Wire types", every one with its doc comment and the JSON tags as written there (imports `net/netip`; `Address` is `netip.Addr` with `omitzero`; `ConnectorRunInput.Token` carries `secret:"true"`; `ExposeInput.Port` carries `jsonschema:"minimum=1,maximum=65535"`). No behaviour.
4. `connector_store.go`: per Contracts "Agent store".
   - `agentMigrations() fs.FS` is `fs.Sub(storeMigrationsFS, "migrations/agent")`; the error branch panics, with a comment that `fs.Sub` fails only for an invalid path and this one is a constant (as `controllerMigrations` does).
   - `connectorSealAD(tunnel string) string` returns `"cloudflare_connectors.token_ct." + tunnel`.
   - `connectorRow` and `claimRow` are the structs of the Contract; `Preexisting` is stored as the integer 0 or 1 (the driver converts the boolean), and `CreatedAt` is stored with `storeFormatTime` and read back with `storeParseTime`.
   - `loadConnector` and `connectorByContainer` select the five columns `tunnel_id, container_id, token_ct, change_id, created_at` (one shared `connectorColumns` constant and one scan helper) for the tunnel or the container; `sql.ErrNoRows` is `(zero, false, nil)`, any other error is wrapped as `cloudflare: read the connector of tunnel <id>: %w` (or `of container`).
   - `listConnectors` returns every row ordered by `tunnel_id` and an empty, non-nil slice for an empty table; `rows.Err` and `rows.Close` are handled.
   - `insertConnector` inserts the five columns and `deleteConnector` deletes the row of the tunnel and only that row, an absent row not being an error; both take the `*sql.Tx` of a write and wrap their errors with the `cloudflare:` prefix and the tunnel id. A second insert for a tunnel fails (the primary key).
   - `connectorClaims` returns the claims of the tunnel ordered by network then claimant, restricted to one network when `network` is not `""`, and an empty non-nil slice when there are none; `insertClaim` and `deleteClaim` as for the connector (a claimant holding a claim on a network cannot insert a second; deleting an absent claim is not an error).
5. `connector_deps.go`: per Contracts "Names" and "Agent dependencies".
   - Constants `tokenDir = "/var/lib/darkpanel/agent/cloudflare-tokens"` and `tokenMount = "/run/darkpanel/token"`, each with a trailing `//nolint:gosec // G101: a directory path, not a credential` (and `a mount path, not a credential`) because gosec reads the word "token" in a constant name as a credential. A third constant `absentSocket = "/nonexistent/darkpanel/docker.sock"` with a comment that test binaries that link the module open a runtime that may call the Engine and must never reach the host's daemon, so a path that never exists makes every call fail as a missing socket.
   - `tunnelIDPattern` (`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`) and `networkNamePattern` (`^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$`), package variables.
   - `connectorName(tunnel)` is `"darkpanel-cloudflared-" + tunnel`; `tokenFileName(tunnel)` is `tunnel + ".token"`.
   - `tunnelIDProblem(id)` returns `""` for a match of `tunnelIDPattern` and `"tunnel must be a lower-case UUID"` otherwise. `claimantProblem(c)` returns `"claimant must be 1 to 300 bytes"` for an empty or longer-than-300-byte string and `"claimant must not contain whitespace or control characters"` when any rune is white space (`unicode.IsSpace`) or a control character (`unicode.IsControl`), else `""`.
   - `ownedPaths() []core.OwnedPath` returns the one entry `{Path: tokenDir, Kind: core.OwnedManagedDir, Pattern: "^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\\.token$"}`.
   - `type agentDeps struct { docker connectorDocker }`; `type connectorDocker interface` with exactly the eleven methods `InspectContainer`, `ListContainers`, `CreateContainer`, `StartContainer`, `StopContainer`, `RemoveContainer`, `InspectNetwork`, `ConnectNetwork`, `DisconnectNetwork`, `InspectImage` and `PullImage`, each with the `dockerapi.Client` signature (read it in `internal/platform/dockerapi`, and `dockerAPI` in `internal/modules/npm/deps.go`), except `PullImage(ctx context.Context, ref string) (pullStream, error)`; `type pullStream interface { Next() (dockerapi.PullMessage, error); Close() error }`.
   - `type engineClient struct{ *dockerapi.Client }` adapts the client: its `PullImage` returns `(nil, err)` as a nil interface on failure (never the client's nil pointer inside the interface) and the stream otherwise.
   - `productionAgentDeps() agentDeps` wires `engineClient{dockerapi.New(socket)}`, `socket` being `dockerapi.DefaultSocket` or `absentSocket` when `testing.Testing()` is true.
   - `engineCoded(err error) error` wraps an error that matches `dockerapi.ErrUnavailable` (`errors.Is`) with `errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")` and returns every other error, `nil` included, unchanged.
6. `client.go`: `type cf struct { d deps; a agentDeps }` (one field per line, gofmt) and `productionCF()` also sets `a: productionAgentDeps()`.
7. `cloudflare.go`: `Manifest()` gains `OwnedPaths: ownedPaths()` and `Migrations` with both `core.SideController: controllerMigrations()` and `core.SideAgent: agentMigrations()`; update its doc comment to say the module declares the token directory it owns on the host and the agent tables of its connectors. `actions()` is untouched.
8. The Task 3 doc comments name no plan, task or register row.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/3 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCw|TestCs|TestAg|TestRegManifest' -v ./internal/modules/cloudflare/
timeout 600 go test -count=1 ./internal/modules/... ./internal/core/... ./schemas/...
```

Expected: every test `PASS`, every package `ok`. Then the task's verification, each with its real output recorded:

```bash
export TMPDIR=/root/.cache/w6b-tmp/3 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/3 && go clean -testcache
```

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/migrations/agent/0001_cloudflare.sql internal/modules/cloudflare/store.go
git add internal/modules/cloudflare/connector_wire.go internal/modules/cloudflare/connector_wire_test.go
git add internal/modules/cloudflare/connector_store.go internal/modules/cloudflare/connector_store_test.go
git add internal/modules/cloudflare/connector_deps.go internal/modules/cloudflare/connector_deps_test.go internal/modules/cloudflare/connector_testhost_test.go
git add internal/modules/cloudflare/client.go internal/modules/cloudflare/cloudflare.go internal/modules/cloudflare/register_test.go
git commit -m "feat(cloudflare): add the agent connector seam"
```

### Task 4: cloudflare: hostname guard residuals

**Files:**
- Modify: `internal/modules/cloudflare/client.go` (`list`)
- Test: `internal/modules/cloudflare/client_test.go`
- Modify: `internal/modules/cloudflare/hostname.go` (`reserved` and two helpers)
- Modify: `internal/modules/cloudflare/hostname_add.go` (`inspect`)
- Test: `internal/modules/cloudflare/hostname_add_test.go`
- Modify: `internal/modules/cloudflare/hostname_remove.go` (`inspectRow`)
- Test: `internal/modules/cloudflare/hostname_remove_test.go`

**Interfaces:**
- Consumes: nothing from an earlier task. Existing: `list`, `api.call`, `answerError`, `apRaw`, `apWantCode`, `cfLookup`, `cfInPanel` (`client_test.go`); `reserved`, `legacyIPv4`, `panelNetwork`, `lookupTimeout`, `exposureParent` (`hostname.go`, `hostname_add.go`); `newHAFixture`, `haEngine`, `haInputWith`, `haFixture.plan`, `haFixture.writes` (`hostname_add_test.go`); `hrNew`, `hrReserved`, `hrIn` (`hostname_remove_test.go`).
- Produces: nothing a later task uses. Private to the package: `maxListPages`, `serviceHost`, `reservedLiteral`, and `cf.reserved(ctx, service) (bool, error)` (its signature changes; its only callers are `hostname_add.go` and its tests).

**Items:** 295, 296

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

This task discharges register row 295 and the two cloudflare cases of register row 296 (the page cap of `list()` and the lookup errors of the add guard); Task 5 handles the other three cases of row 296. Rulings:

- Row 295: `cloudflare.hostname.remove` refuses a stored service only when its host is an address of the panel network spelled as an IP literal or in a legacy numeric form (`172.31.255.1`, `2887778049`, `0xac1fff01`, `[::ffff:172.31.255.1]`), with no DNS lookup, so a hostname whose name later resolves into the panel network can be removed. The add-side guard keeps its lookup. `panel.exposure.remove` still removes any such row.
- Row 296, lookup errors: on add, a lookup the resolver answers as not-found (`*net.DNSError` with `IsNotFound`, found with `errors.As`) means "not reserved", so a container name the controller cannot resolve still passes, which `cloudflare.expose.container` needs; any other lookup error (a timeout, a server failure, a cancelled context) refuses the add with `cloudflare.reserved`, Definite, because only a not-found answer is evidence that the name does not reach the panel. A host that is an address literal never reaches the resolver, and a call under `panel.exposure.add` skips the guard as before.
- Row 296, page cap: `list` reads at most `maxListPages` = 200 pages (20000 items at the largest page size, far beyond the few thousand DNS records spec 7.1 expects) and then fails as `cloud.unavailable`; the cap applies whether the answers give no total or a total that never ends.

- [ ] **Step 1: Write the failing tests**

In `internal/modules/cloudflare/client_test.go` add `"strconv"` to the imports and insert this before `TestAPTunnelDecodesTheProvidersFields`:

```go
// rsZonesPages makes GET /zones answer page after page: pageSize gives how many
// zones the page holds and info the answer's result_info (nil for none).
func rsZonesPages(srv *cloudfake.Server, pageSize func(page int) int, info func(page int) any) {
	srv.Handle(http.MethodGet, "/zones", func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		zones := make([]any, pageSize(page))
		for i := range zones {
			zones[i] = map[string]string{"id": fmt.Sprintf("z%d-%d", page, i), "name": "example.com"}
		}
		body := map[string]any{"success": true, "errors": []any{}, "messages": []any{}, "result": zones}
		if info != nil {
			body["result_info"] = info(page)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(body)
	})
}

// TestRsListsStopAtThePageCap: a listing that does not end within maxListPages
// pages fails as cloud.unavailable after exactly that many requests, whether
// the answers give no total or a total of a million pages.
func TestRsListsStopAtThePageCap(t *testing.T) {
	full := func(int) int { return 50 }
	for name, info := range map[string]func(page int) any{
		"no result_info": nil,
		"no total":       func(page int) any { return map[string]any{"page": page, "per_page": 50} },
		"a million pages": func(page int) any {
			return map[string]any{"page": page, "per_page": 50, "total_pages": 1000000, "total_count": 50000000}
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, a := apRaw(t)
			rsZonesPages(srv, full, info)
			zones, err := a.Zones(context.Background())
			_ = apWantCode(t, err, errcode.CloudUnavailable)
			if zones != nil {
				t.Errorf("%d zones returned with the error", len(zones))
			}
			if n := len(srv.Requests()); n != maxListPages {
				t.Errorf("%d requests, want %d", n, maxListPages)
			}
		})
	}
}

// TestRsListsReadUpToThePageCap: a listing that ends on page maxListPages is
// read whole, by a short last page or by its stated total.
func TestRsListsReadUpToThePageCap(t *testing.T) {
	for name, tc := range map[string]struct {
		size func(page int) int
		info func(page int) any
		want int
	}{
		"a short last page": {
			size: func(page int) int {
				if page == maxListPages {
					return 1
				}
				return 50
			},
			want: (maxListPages-1)*50 + 1,
		},
		"the stated total": {
			size: func(int) int { return 50 },
			info: func(page int) any {
				return map[string]any{"page": page, "per_page": 50, "total_pages": maxListPages, "total_count": maxListPages * 50}
			},
			want: maxListPages * 50,
		},
	} {
		t.Run(name, func(t *testing.T) {
			srv, a := apRaw(t)
			rsZonesPages(srv, tc.size, tc.info)
			zones, err := a.Zones(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if len(zones) != tc.want || len(srv.Requests()) != maxListPages {
				t.Errorf("%d zones in %d requests, want %d in %d", len(zones), len(srv.Requests()), tc.want, maxListPages)
			}
		})
	}
}
```

In `internal/modules/cloudflare/hostname_add_test.go` add `"errors"`, `"net"` and `"net/netip"` to the imports. In `TestHAReserved` replace

```go
		if got := c.reserved(context.Background(), service); got != want {
			t.Errorf("reserved(%q) = %t, want %t", service, got, want)
		}
```

with

```go
		if got, err := c.reserved(context.Background(), service); err != nil || got != want {
			t.Errorf("reserved(%q) = %t, %v; want %t, nil", service, got, err, want)
		}
```

and insert this before `TestHACheckDrift`:

```go
// rsLookupFailure is a name lookup that fails with err, and counts its calls.
type rsLookupFailure struct {
	err   error
	calls int
}

func (l *rsLookupFailure) lookup(context.Context, string) ([]netip.Addr, error) {
	l.calls++
	return nil, l.err
}

// TestRsReservedLookupErrors: only a not-found answer from the resolver lets a
// name pass the guard; a timeout, a server failure or any other error is
// returned, and an address literal never reaches the resolver.
func TestRsReservedLookupErrors(t *testing.T) {
	notFound := &net.DNSError{Err: "no such host", Name: "web.app.test", IsNotFound: true}
	for name, tc := range map[string]struct {
		err     error
		service string
		want    bool // the answer when the lookup error is nil
		failed  bool
		lookups int
	}{
		"not found":                {err: notFound, service: "http://web.app.test:80", lookups: 1},
		"not found, wrapped":       {err: fmt.Errorf("resolve: %w", notFound), service: "http://web.app.test:80", lookups: 1},
		"timeout":                  {err: &net.DNSError{Err: "i/o timeout", Name: "web.app.test", IsTimeout: true}, service: "http://web.app.test:80", failed: true, lookups: 1},
		"server failure":           {err: &net.DNSError{Err: "server misbehaving", Name: "web.app.test", IsTemporary: true}, service: "http://web.app.test:80", failed: true, lookups: 1},
		"deadline":                 {err: context.DeadlineExceeded, service: "http://web.app.test:80", failed: true, lookups: 1},
		"another error":            {err: errors.New("resolver gone"), service: "http://web.app.test:80", failed: true, lookups: 1},
		"a panel address literal":  {err: errors.New("resolver gone"), service: "http://172.31.255.4:80", want: true},
		"an other address literal": {err: errors.New("resolver gone"), service: "http://10.1.2.3:80"},
		"a legacy numeric literal": {err: errors.New("resolver gone"), service: "http://0xac1fff01:80", want: true},
		"no host":                  {err: errors.New("resolver gone"), service: "http_status:404"},
	} {
		t.Run(name, func(t *testing.T) {
			l := &rsLookupFailure{err: tc.err}
			c := cf{d: deps{lookup: l.lookup}}
			got, err := c.reserved(context.Background(), tc.service)
			if (err != nil) != tc.failed {
				t.Fatalf("reserved(%q) error = %v, want an error: %t", tc.service, err, tc.failed)
			}
			if tc.failed && !errors.Is(err, tc.err) {
				t.Errorf("reserved(%q) error = %v, want the lookup's error", tc.service, err)
			}
			if got != tc.want {
				t.Errorf("reserved(%q) = %t, want %t", tc.service, got, tc.want)
			}
			if l.calls != tc.lookups {
				t.Errorf("%d lookups, want %d", l.calls, tc.lookups)
			}
		})
	}
}

// TestRsReservedAnswersWithAnAddress: a name that resolves to an address
// outside the panel network is not reserved, and one that resolves to the panel
// network among other addresses is.
func TestRsReservedAnswersWithAnAddress(t *testing.T) {
	for name, tc := range map[string]struct {
		addrs []netip.Addr
		want  bool
	}{
		"outside":        {addrs: []netip.Addr{netip.MustParseAddr("203.0.113.9")}},
		"no addresses":   {},
		"among others":   {addrs: []netip.Addr{netip.MustParseAddr("203.0.113.9"), netip.MustParseAddr("172.31.255.8")}, want: true},
		"mapped address": {addrs: []netip.Addr{netip.MustParseAddr("::ffff:172.31.255.8")}, want: true},
	} {
		t.Run(name, func(t *testing.T) {
			c := cf{d: deps{lookup: func(context.Context, string) ([]netip.Addr, error) { return tc.addrs, nil }}}
			if got, err := c.reserved(context.Background(), "http://web.app.test:80"); err != nil || got != tc.want {
				t.Errorf("reserved = %t, %v; want %t, nil", got, err, tc.want)
			}
		})
	}
}

// TestRsAddPlanWhenTheServiceHostCannotBeResolved: a plan whose service names a
// host the resolver fails on, other than by not finding it, is refused with
// cloudflare.reserved and sends no write; a host the resolver does not find
// passes, as a container name does, and so does any host under the panel
// exposure.
func TestRsAddPlanWhenTheServiceHostCannotBeResolved(t *testing.T) {
	const service = "http://web.app.test:8080"
	run := func(t *testing.T, err error, call *core.CallContext) (*haFixture, *rsLookupFailure, error) {
		t.Helper()
		f := newHAFixture(t)
		l := &rsLookupFailure{err: err}
		f.c.d.lookup = l.lookup
		f.eng.Close()
		f.eng = haEngine(t, f.h, f.c)
		t.Cleanup(f.eng.Close)
		_, planErr := f.plan(haInputWith(haTunnelName, "app.example.com", service), call)
		return f, l, planErr
	}
	t.Run("a timeout is refused", func(t *testing.T) {
		f, l, err := run(t, &net.DNSError{Err: "i/o timeout", Name: "web.app.test", IsTimeout: true}, nil)
		if got := errcode.From(err); got.Code != errcode.CloudflareReserved || !strings.Contains(got.Detail, "web.app.test") {
			t.Fatalf("plan error = %v, want %s naming the host", err, errcode.CloudflareReserved)
		}
		if l.calls != 1 || len(f.writes()) != 0 {
			t.Errorf("%d lookups and writes %v, want one lookup and no write", l.calls, f.writes())
		}
	})
	t.Run("not found passes", func(t *testing.T) {
		_, l, err := run(t, &net.DNSError{Err: "no such host", Name: "web.app.test", IsNotFound: true}, nil)
		if err != nil || l.calls != 1 {
			t.Errorf("plan error = %v after %d lookups, want it accepted after one", err, l.calls)
		}
	})
	t.Run("the panel exposure does not look up", func(t *testing.T) {
		call := &core.CallContext{ParentChangeID: "01HPARENT", ParentAction: "panel.exposure.add", StepID: "hostname"}
		_, l, err := run(t, errors.New("resolver gone"), call)
		if err != nil || l.calls != 0 {
			t.Errorf("plan error = %v after %d lookups, want it accepted without a lookup", err, l.calls)
		}
	})
}
```

In `internal/modules/cloudflare/hostname_remove_test.go` add `"net/netip"` to the imports and replace `TestHRReserved` and its doc comment (everything between the comment `// TestHRReserved:` and `func hrReserved`) with:

```go
// TestHRReserved: a hostname whose service is an address of the panel
// network, in canonical or legacy numeric form, is removed only as a step of
// panel.exposure.remove.
func TestHRReserved(t *testing.T) {
	for _, service := range []string{"http://172.31.255.1:8080", "http://2887778049:8080"} {
		t.Run(service, func(t *testing.T) { hrReserved(t, service) })
	}
}

// TestRsRemoveNeverResolvesTheServiceName: a name that now resolves into the
// panel network does not stop its hostname from being removed, and the removal
// does not look the name up.
func TestRsRemoveNeverResolvesTheServiceName(t *testing.T) {
	ctx := context.Background()
	w := hrNew(t, func(o *hrOpts) { o.service = "http://gw" + cfInPanel + ":9443" })
	lookups := 0
	w.c.d.lookup = func(ctx context.Context, host string) ([]netip.Addr, error) {
		lookups++
		return cfLookup(ctx, host)
	}
	w.def = w.c.hostnameRemove()
	if _, err := w.def.Handler.Plan(ctx, w.h.env(""), hrIn); err != nil {
		t.Fatalf("plan: %v", err)
	}
	if _, err := w.def.Handler.Apply(ctx, w.h.env("01J00000000000000000000002"), hrIn); err != nil {
		t.Fatalf("apply: %v", err)
	}
	if _, ok := w.row(t); ok {
		t.Error("the hostname row remains")
	}
	if lookups != 0 {
		t.Errorf("%d name lookups, want none", lookups)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/4 && export TMPDIR=/root/.cache/w6b-tmp/4 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestRs|TestHAReserved|TestHRReserved|TestAPLists' ./internal/modules/cloudflare/
```

Expected: FAIL to build, `undefined: maxListPages` and `assignment mismatch: 2 variables but c.reserved returns 1 value`. Quote this output in the report.

- [ ] **Step 3: Implement**

1. `client.go`: add `const maxListPages = 200` above `list`, with a doc comment: it is how many pages `list` reads before giving up, 20000 items at the largest page size, far beyond the largest listing spec 7.1 expects, so reaching it means the provider ignores the page number or claims an endless listing. In `list` the loop becomes `for page := 1; page <= maxListPages; page++` with its body unchanged (both the stated-total and the short-page exits stay), and after the loop `return answerError(http.MethodGet, nil, fmt.Sprintf("cloudflare listed more than %d pages of %s", maxListPages, path))`, which is `cloud.unavailable`. Add the cap to `list`'s doc comment.
2. `hostname.go`: replace `reserved` with three functions.
   - `serviceHost(service string) (host string, addr netip.Addr, literal bool)`: parses the URL; for a parse error or an empty `Host` it returns `""`; otherwise the host without one trailing dot, and, when that host parses with `netip.ParseAddr` (the address unmapped from an IPv4-in-IPv6 form) or with `legacyIPv4`, that address and `literal` true.
   - `reservedLiteral(service string) bool`: true when `serviceHost` reports a literal inside `panelNetwork`; it resolves no name.
   - `(c cf) reserved(ctx, service) (bool, error)`: `serviceHost`; a literal answers `panelNetwork.Contains(addr), nil` without a lookup; an empty host answers `false, nil`; otherwise it looks the host up with `c.lookup` under `lookupTimeout`: an error that is a `*net.DNSError` with `IsNotFound` (`errors.As`, so a wrapped one counts) answers `false, nil`, any other error is returned with `false`, and otherwise the answer is whether any returned address (unmapped) is inside `panelNetwork`. Rewrite the doc comments to say this (the not-found answer is the only evidence that a name does not reach the panel; the check stays best effort because cloudflared resolves names on its own host). `lookup`, `legacyIPv4` and the other helpers stay as they are; the `errors` import already exists.
3. `hostname_add.go`: in `inspect` the guard becomes: when `env.Call.ParentAction != exposureParent`, call `h.c.reserved(ctx, in.Service)`; on an error return `core.Definite(errcode.Wrap(errcode.CloudflareReserved, err, ...))` with the detail `cannot tell whether service <service> routes into the panel network <panelNetwork>: its host could not be resolved` (the resolver's error stays in the chain and out of the detail); when reserved, keep the existing `core.Definite(errcode.New(errcode.CloudflareReserved, ...))` refusal and its detail.
4. `hostname_remove.go`: in `inspectRow` the condition becomes `env.Call.ParentAction != exposureRemoveParent && reservedLiteral(row.Service)`; the refusal and its detail stay.
5. Nothing else changes: `panel.exposure.add` and `panel.exposure.remove` callers are in frozen modules and keep their parents.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/4 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestRs|TestHAReserved|TestHRReserved|TestAPLists|TestHA|TestHR' ./internal/modules/cloudflare/
```

Expected: `ok`. Then, as a check of the tests and not part of the change, make `reserved` return `false, nil` for every lookup error, run `timeout 300 go test -count=1 -run 'TestRs' ./internal/modules/cloudflare/` and see `TestRsReservedLookupErrors` (timeout, server failure, deadline, another error) and `TestRsAddPlanWhenTheServiceHostCannotBeResolved` fail; restore it and run again green. Then the task's verification, each with its real output recorded:

```bash
export TMPDIR=/root/.cache/w6b-tmp/4 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/4 && go clean -testcache
```

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/client.go internal/modules/cloudflare/client_test.go
git commit -m "fix(cloudflare): cap the pages a list reads"
git add internal/modules/cloudflare/hostname.go internal/modules/cloudflare/hostname_add.go internal/modules/cloudflare/hostname_add_test.go
git add internal/modules/cloudflare/hostname_remove.go internal/modules/cloudflare/hostname_remove_test.go
git commit -m "fix(cloudflare): resolve service names on add only"
```

### Task 5: docker and harness: wave 3 residuals

**Files:**
- Modify: `internal/modules/docker/compose_check.go` (`checkPlugins` and its doc comment)
- Test: `internal/modules/docker/compose_check_test.go`
- Modify: `internal/modules/docker/dstate/object.go` (`networkDoc`, `volumeDoc`, `NetworkCreated`, `VolumeCreated`)
- Test: `internal/modules/docker/dstate/dstate_test.go`
- Modify: `test/harness/m4_live_test.go` (`m4lWorkflow`)
- Test: `test/harness/m4_live_unit_test.go`

**Interfaces:**
- Consumes: nothing from another task. Existing names: `shadowPluginDirs`, `composeSystemPath`, `composeUnavailable` (compose_check.go); `ccPrepare`, `ccProject`, `ccConfig`, `ccRefused`, `ccSimple`, `ccDirMode`, `dkFile` (the docker package's test helpers); `m3dxExpoJob`, `m3dxExpoSteps` (harness).
- Produces: nothing a later task uses. `m4lStepBefore(job, step, marker string) bool` is private to the harness package.

**Items:** 296

**Depends on:** none

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 3 = 6
**Lenses:** security

This task discharges three of the five cases of register row 296 (the shadow-plugin directory ownership, the raw label values in the network and volume effect documents, and the missing unit test of the build-step order check); Task 4 handles the other two. Rulings:

- The shadow-plugin check refuses a system plugin directory that exists but is not root-owned or is writable by its group or others, exactly as `composeSystemPath` judges `pluginDir`. A directory that does not exist is accepted: it holds nothing the Docker CLI could run. The ancestors of the three directories stay unchecked, as the ancestors of `pluginDir` do.
- A network or volume effect document records each label value as `sha256:<64 hex digits>` of the value, keeping the keys. The documents are only compared (never read back to recreate an object), and the digest keeps a changed value a changed document while a secret placed in a label never reaches the journal.
- The `at < 0` branch of the build-step order check cannot be reached through `m4lWorkflow` (the step text always comes from the job it is searched in), so it is made testable by moving the comparison into `m4lStepBefore`.

- [ ] **Step 1: Write the failing tests**

Append to `internal/modules/docker/compose_check_test.go`:

```go
func TestCCPrepareChecksTheShadowPluginDirectories(t *testing.T) {
	cases := []struct {
		name   string
		dir    dkFile
		detail string // "" when the directory is accepted
	}{
		{"root-owned and closed", dkFile{Mode: ccDirMode}, ""},
		{"owned by another user", dkFile{Mode: ccDirMode, UID: 1000}, "owned by uid 1000"},
		{"writable by its group", dkFile{Mode: fs.ModeDir | 0o775}, "writable by its group or others"},
		{"writable by others", dkFile{Mode: fs.ModeDir | 0o757}, "writable by its group or others"},
		{"a symlink", dkFile{Mode: ccDirMode, Symlink: true}, "is a symlink"},
		{"not a directory", dkFile{Mode: 0o755}, "is not a directory"},
	}
	for _, dir := range shadowPluginDirs {
		for _, tc := range cases {
			t.Run(dir+" "+tc.name, func(t *testing.T) {
				h := newDKHost(t)
				p := ccProject(t, h, "shop")
				ccConfig(h, p, nil, ccSimple)
				h.files[dir] = tc.dir
				_, err := ccPrepare(h, "shop", nil)
				if tc.detail == "" {
					if err != nil {
						t.Fatalf("a %s directory was refused: %v", tc.name, err)
					}
					return
				}
				ccRefused(t, err, errcode.DockerComposeUnavailable, dir, tc.detail)
				if len(h.cmds) != 0 {
					t.Errorf("ran %v after refusing %s", h.cmds, dir)
				}
			})
		}
	}
}
```

In `internal/modules/docker/dstate/dstate_test.go` add the imports `crypto/sha256`, `encoding/hex`, `fmt` and `strings`, then add this helper and test after `TestDsVolumeCreated`:

```go
// dsDigest is how a label value appears in a state document.
func dsDigest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(sum[:])
}

func TestDsLabelValuesAreDigestedNotRecorded(t *testing.T) {
	const secret = "s3cr3t-value-in-a-label"
	network := func(value string) *dockerapi.Network {
		return &dockerapi.Network{ID: "n1", Created: "2026-10-23T09:15:00Z", Labels: map[string]string{"token": value}}
	}
	volume := func(value string) *dockerapi.Volume {
		return &dockerapi.Volume{Name: "data", Labels: map[string]string{"token": value}}
	}
	render := map[string]func(value string) (json.RawMessage, error){
		"network": func(value string) (json.RawMessage, error) { return NetworkCreated(network(value)) },
		"volume":  func(value string) (json.RawMessage, error) { return VolumeCreated(volume(value), true) },
	}
	for name, f := range render {
		t.Run(name, func(t *testing.T) {
			raw, err := f(secret)
			text := dsText(t, raw, err)
			if strings.Contains(text, secret) {
				t.Errorf("the document %s holds the raw label value", text)
			}
			var doc struct {
				Labels map[string]string `json:"labels"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			if len(doc.Labels) != 1 || doc.Labels["token"] != dsDigest(secret) {
				t.Errorf("labels %v, want the key token with the digest %s", doc.Labels, dsDigest(secret))
			}
			same, _ := f(secret)
			other, _ := f("another-value")
			if string(same) != text {
				t.Errorf("one value rendered as %s and as %s", text, same)
			}
			if string(other) == text {
				t.Errorf("two label values rendered as the same document %s", text)
			}
		})
	}
}
```

In the same file, replace the two `want` literals that hold raw label values so they use the digest:

- in `TestDsNetworkCreated`, the line `want := `{"id":"n1","created":"2026-10-23T09:15:00Z","labels":{"a":"1","b":"2"},"unused":false}`` becomes ``want := fmt.Sprintf(`{"id":"n1","created":"2026-10-23T09:15:00Z","labels":{"a":%q,"b":%q},"unused":false}`, dsDigest("1"), dsDigest("2"))``;
- in `TestDsVolumeCreated`, the line `want := `{"name":"data","labels":{"x":"y"},"unused":true}`` becomes ``want := fmt.Sprintf(`{"name":"data","labels":{"x":%q},"unused":true}`, dsDigest("y"))``.

Append to `test/harness/m4_live_unit_test.go`:

```go
func TestM4lStepBefore(t *testing.T) {
	const job = "    steps:\n      - name: Build\n        run: make\n      - uses: actions/upload-artifact@x\n        with:\n          path: bin/harness\n"
	const build = "      - name: Build\n        run: make\n"
	const upload = "      - uses: actions/upload-artifact@x\n        with:\n          path: bin/harness\n"
	for _, tc := range []struct {
		name, step, marker string
		want               bool
	}{
		{"the step comes before the marker", build, "path: bin/harness", true},
		{"the step comes after the marker", upload, "- name: Build", false},
		{"the step text is not in the job", "      - name: Other\n        run: make\n", "path: bin/harness", false},
		{"the marker is not in the job", build, "path: bin/other", false},
	} {
		if got := m4lStepBefore(job, tc.step, tc.marker); got != tc.want {
			t.Errorf("%s: m4lStepBefore = %t, want %t", tc.name, got, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/5 && export TMPDIR=/root/.cache/w6b-tmp/5 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCCPrepareChecksTheShadowPluginDirectories' ./internal/modules/docker/
timeout 300 go test -count=1 -run 'TestDs' ./internal/modules/docker/dstate/
timeout 300 go test -tags harness -count=1 -run 'TestM4lStepBefore' ./test/harness/
```

Expected: the first run fails on the `owned by another user`, `writable by its group`, `writable by others`, `a symlink` and `not a directory` rows ("no error, want docker.compose_unavailable"); the second fails because the documents hold the raw label values; the third fails to build with `undefined: m4lStepBefore`.

- [ ] **Step 3: Implement**

1. `checkPlugins` in `compose_check.go`: keep the first check of each `shadowPluginDirs` entry (a `<dir>/docker-compose` that exists is refused, a lstat error other than not-exist is wrapped). After it, lstat `dir` itself: a not-exist error continues with the next directory, any other error returns `docker: check <dir>: <err>`, and otherwise the result of `k.composeSystemPath(dir, true, "")` is returned when it is not nil. The remaining checks of `pluginDir` stay as they are. Rewrite the doc comment so it says that every system plugin directory must be root-owned and closed to group and others, besides holding no `docker-compose`.
2. `dstate/object.go`: add an unexported `labelDigests(labels map[string]string) map[string]string` that returns nil for an empty map and otherwise a new map with the same keys whose values are `"sha256:"` plus the lower-case hex SHA-256 of the value. `NetworkCreated` and `VolumeCreated` put `labelDigests(...)` in the `Labels` field. Update the doc comments of both functions and of the two document types to say that label values are recorded as digests. Nothing else in the documents changes (the `omitempty` tags keep an empty map out of the JSON).
3. `m4_live_test.go`: add `m4lStepBefore(job, step, marker string) bool` with a doc comment: it reports whether the text of step, without its trailing newlines, occurs in job at or before the first occurrence of marker, and is false when either text is absent. In `m4lWorkflow` replace the `upload`/`at` computation and its condition by `if !m4lStepBefore(build, step, "path: bin/harness")`, keeping the same error text.

- [ ] **Step 4: Run the tests to verify they pass, then check that the new harness test guards the branch**

```bash
export TMPDIR=/root/.cache/w6b-tmp/5 GOTOOLCHAIN=local
timeout 300 go test -count=1 ./internal/modules/docker/... 
timeout 300 go vet -tags harness ./test/...
timeout 300 go test -tags harness -count=1 -run 'Unit|TestM4l' ./test/harness/...
```

Expected: every package `ok`, vet silent. Then, as a check of the test and not part of the change, delete the condition `at >= 0 &&` from `m4lStepBefore`, run `timeout 300 go test -tags harness -count=1 -run 'TestM4lStepBefore' ./test/harness/` and see `the step text is not in the job` fail; restore the line and run it again green.

Then the task's verification, each with its real output recorded:

```bash
export TMPDIR=/root/.cache/w6b-tmp/5 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/docker/... 
timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/5 && go clean -testcache
```

- [ ] **Step 5: Commit**

```bash
git add internal/modules/docker/compose_check.go internal/modules/docker/compose_check_test.go
git commit -m "fix(docker): check shadow plugin directory owners"
git add internal/modules/docker/dstate/object.go internal/modules/docker/dstate/dstate_test.go
git commit -m "fix(docker): digest label values in effect docs"
git add test/harness/m4_live_test.go test/harness/m4_live_unit_test.go
git commit -m "test(harness): cover the build step order check"
```

### Task 6: cloudflare: connector.run

**Files:**
- Create: `internal/modules/cloudflare/connector_run.go` (the action, its handler's Plan, Apply and Verify, the container configuration, `removeConnector`)
- Create: `internal/modules/cloudflare/connector_effects.go` (effect journaling, Observe and Inverse)
- Modify: `internal/modules/cloudflare/connector_deps.go` (`agentDeps` gains `readFile`; `productionAgentDeps` sets it)
- Test: `internal/modules/cloudflare/connector_run_test.go`

**Interfaces:**
- Consumes (Contracts): `Image`, `imageUID` (Task 1); `ConnectorRunInput`, `Connector` (Task 3 wire types); `connectorRow`, `loadConnector`, `insertConnector`, `deleteConnector`, `connectorSealAD`, the `cloudflare_connectors` table; `tokenDir`, `tokenMount`, `connectorName`, `tokenFileName`, `tunnelIDProblem`, `agentDeps`, `connectorDocker`, `pullStream`, `engineCoded`, `productionAgentDeps`; the `cf` value with its `a agentDeps` field; the Agent test host (`newAGHost`, `h.cf()`, `h.env`, `h.docker` with `putContainer`, `putImage`, `Fail`, `calls`, `agContainer`, `agToken`); `ID`; `docker/dstate` (`ContainerRun`, `ContainerRunning`, `Phase`).
- Produces (Contracts "Agent actions"): `func (c cf) connectorRun() core.ActionDef` for action `cloudflare.connector.run` v1 and `func removeConnector(ctx context.Context, env core.Env, c cf, tunnel string) (bool, error)`. Also: the field `readFile func(name string) ([]byte, error)` of `agentDeps`, and the effect resources `cloudflare.tokenfile:<tunnel>`, `cloudflare.container:<container id>`, `cloudflare.connector:<tunnel>` (creation forms with a `?` before the identity).
- Every top-level identifier these two files declare starts with `connector`, `eff` or is `removeConnector`, so none collides with the identifiers of Tasks 7 and 8.

**Items:** 34

**Depends on:** Task 1, Task 3

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8
**Lenses:** security

`cloudflare.connector.run` deploys the `cloudflared` connector for one tunnel on this host. Decisions the tests pin:

- Plan refuses with Definite `request.invalid_input`: a tunnel id that is not a lower-case UUID (field `/tunnel`), an empty token (field `/token`), a `cloudflare_connectors` row for the tunnel, and a container named `connectorName(tunnel)` (Contracts). An unreachable Engine is `docker.engine_unavailable` (not Definite).
- Apply order is: pull `Image` when `InspectImage` reports it absent; write the token file; create the container; insert the row (it holds the new container's id); start the container. The row exists before the container runs, so the log-secret source of Task 7 can always find the token of a running connector.
- Four effects, in this order: (1) Create `cloudflare.tokenfile:?<tunnel>` (local, inverse local, recovery name `<tunnel>`), (2) Create `cloudflare.container:?<container name>` (commit wait 2 minutes, recovery name `<container name>`), (3) Create `cloudflare.connector:?<tunnel>` (the row; local, inverse local, recovery name `<tunnel>`), (4) Modify `cloudflare.container:<id>` (the start; commit wait 2 minutes, before-state a stopped container, as `npm.instance.deploy` journals its start). Every Create has the null state as its before-state; creations are named by their final resource when made.
- The state documents never hold the token: the token file is `{"name":"<tunnel>.token","size":<bytes>}` (read through `agentDeps.readFile` from `tokenDir/<name>`; absent file is the null state), the row is `{"tunnel":…,"containerId":…,"changeId":…}`, and containers use `dstate.ContainerRun` (the restorable form for a creation or an effect being undone, the recorded form otherwise, as `internal/modules/npm/effects.go` `phaseOf`). The local effects carry `After` equal to the document they will have once made.
- The container: name `connectorName(tunnel)`, image `Image`, label `core.LabelModule: ID`, command `tunnel --no-autoupdate run --token-file /run/darkpanel/token`, restart policy `unless-stopped`, one `HostConfig.Mounts` entry `{Type: "bind", Source: tokenDir+"/"+tokenFileName(tunnel), Target: tokenMount, ReadOnly: true}`, no `Env`, no `Entrypoint`, no `Binds`, default network.
- Undo (`Inverse`): a started container is stopped (30 seconds grace), a created container is removed with `Force`, the row is deleted only when its `change_id` equals the effect's (otherwise a Definite `request.invalid_input` refusal that keeps it), the token file is removed with `Env.Files.RemoveManaged`. An object already gone is undone. A 4xx answer of the Engine is a Definite refusal; an unreachable Engine is `docker.engine_unavailable`.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/cloudflare/connector_run_test.go`:

```go
package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const crTunnel = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"

// crWrite is one WriteManaged call.
type crWrite struct {
	Dir, Name string
	Data      []byte
	Mode      fs.FileMode
	UID, GID  int
}

// crFiles wraps the host's FileWriter: it records the managed-file calls and serves the files written to readFile.
type crFiles struct {
	core.FileWriter
	mu        sync.Mutex
	files     map[string][]byte
	writes    []crWrite
	removes   []string
	failWrite error
}

func (f *crFiles) WriteManaged(ctx context.Context, module, dir, name string, data []byte, mode fs.FileMode, uid, gid int) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.failWrite != nil {
		return f.failWrite
	}
	if err := f.FileWriter.WriteManaged(ctx, module, dir, name, data, mode, uid, gid); err != nil {
		return err
	}
	f.files[dir+"/"+name] = bytes.Clone(data)
	f.writes = append(f.writes, crWrite{Dir: dir, Name: name, Data: bytes.Clone(data), Mode: mode, UID: uid, GID: gid})
	return nil
}

func (f *crFiles) RemoveManaged(ctx context.Context, module, dir, name string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.FileWriter.RemoveManaged(ctx, module, dir, name); err != nil {
		return err
	}
	delete(f.files, dir+"/"+name)
	f.removes = append(f.removes, dir+"/"+name)
	return nil
}

func (f *crFiles) read(name string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	data, ok := f.files[name]
	if !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return bytes.Clone(data), nil
}

// crHost is the agent test host with the recorder of the managed files in place of its FileWriter and in
// agentDeps.readFile.
type crHost struct {
	*agHost
	cfg cf
	rec *crFiles
}

func newCR(t *testing.T) *crHost {
	t.Helper()
	h := newAGHost(t)
	rec := &crFiles{FileWriter: h.env("").Files, files: map[string][]byte{}}
	cfg := h.cf()
	cfg.a.readFile = rec.read
	return &crHost{agHost: h, cfg: cfg, rec: rec}
}

func (h *crHost) env(changeID string) core.Env {
	env := h.agHost.env(changeID)
	env.Files = h.rec
	return env
}

func crInput(tunnel, token string) json.RawMessage {
	raw, err := json.Marshal(ConnectorRunInput{Tunnel: tunnel, Token: token})
	if err != nil {
		panic(err)
	}
	return raw
}

func crApply(t *testing.T, h *crHost, changeID, tunnel string) (Connector, error) {
	t.Helper()
	raw, err := h.cfg.connectorRun().Handler.Apply(context.Background(), h.env(changeID), crInput(tunnel, agToken(tunnel)))
	if err != nil {
		return Connector{}, err
	}
	var out Connector
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

var crMutations = []string{"PullImage", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "ConnectNetwork", "DisconnectNetwork"}

// crMutated are the calls that changed the Engine, in order.
func crMutated(h *crHost) []string {
	var got []string
	for _, c := range h.docker.calls() {
		if slices.Contains(crMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// crRefused checks err is a Definite refusal with the code and returns it.
func crRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

func crSeedRow(t *testing.T, h *crHost, tunnel, containerID string) {
	t.Helper()
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(tunnel), []byte(agToken(tunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: tunnel, ContainerID: containerID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

func crHasRow(t *testing.T, h *crHost, tunnel string) bool {
	t.Helper()
	_, ok, err := loadConnector(context.Background(), h.env("").DB.Read(), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func TestCrSpec(t *testing.T) {
	def := newCR(t).cfg.connectorRun()
	s := def.Spec
	if s.Name != "cloudflare.connector.run" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || !s.LongRunning || s.ApplyTimeout != 10*time.Minute || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			h.Plan != nil, h.Apply != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[ConnectorRunInput]() || def.OutputType != reflect.TypeFor[Connector]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
}

func TestCrProductionDepsCanReadFiles(t *testing.T) {
	if productionAgentDeps().readFile == nil {
		t.Error("productionAgentDeps has no readFile")
	}
}

func TestCrPlanShowsTheRun(t *testing.T) {
	ctx := context.Background()
	h := newCR(t)
	token := agToken(crTunnel)
	plan := func(tunnel string) core.PlanOutcome {
		out, err := h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(tunnel, token))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	out := plan(crTunnel)
	want := []core.DiffLine{
		{Op: "+", Text: fmt.Sprintf("token file %s/%s.token, mode 0400, owned by uid and gid %d", tokenDir, crTunnel, imageUID)},
		{Op: "+", Text: "container darkpanel-cloudflared-" + crTunnel + " from " + Image},
		{Op: "+", Text: "restart policy unless-stopped; the token file is mounted read-only at /run/darkpanel/token"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	for _, l := range out.Diff {
		if strings.Contains(l.Text, token) {
			t.Errorf("the diff line %q carries the token", l.Text)
		}
	}
	other := plan("1b2c3d4e-5f60-4b7c-8d9e-0a1b2c3d4e5f")
	if len(out.StateHash) != 64 || out.StateHash != plan(crTunnel).StateHash || out.StateHash == other.StateHash {
		t.Errorf("state hashes %q and %q: want a stable hash that differs per tunnel", out.StateHash, other.StateHash)
	}
	if got := crMutated(h); len(got) != 0 {
		t.Errorf("the plan changed the Engine: %v", got)
	}
	if len(h.rec.writes) != 0 {
		t.Errorf("the plan wrote files: %+v", h.rec.writes)
	}
}

func TestCrRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		tunnel string
		token  string
		seed   func(t *testing.T, h *crHost)
		path   string // the field the refusal names, "" for none
		detail string // a substring of the refusal's detail
	}{
		{name: "an empty tunnel", tunnel: "", token: "tok", path: "/tunnel"},
		{name: "a name that is no UUID", tunnel: "my-tunnel", token: "tok", path: "/tunnel"},
		{name: "an upper-case UUID", tunnel: strings.ToUpper(crTunnel), token: "tok", path: "/tunnel"},
		{name: "an empty token", tunnel: crTunnel, token: "", path: "/token"},
		{name: "a connector row", tunnel: crTunnel, token: "tok", detail: crTunnel,
			seed: func(t *testing.T, h *crHost) { crSeedRow(t, h, crTunnel, "existing-container") }},
		{name: "a container of the name", tunnel: crTunnel, token: "tok", detail: "darkpanel-cloudflared-" + crTunnel,
			seed: func(_ *testing.T, h *crHost) { h.docker.putContainer(agContainer(connectorName(crTunnel))) }},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newCR(t)
				if tc.seed != nil {
					tc.seed(t, h)
				}
				var err error
				if stage == "plan" {
					_, err = h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(tc.tunnel, tc.token))
				} else {
					_, err = h.cfg.connectorRun().Handler.Apply(ctx, h.env("c1"), crInput(tc.tunnel, tc.token))
				}
				coded := crRefused(t, err, errcode.InvalidInput)
				if tc.path != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tc.path }) {
					t.Errorf("fields %+v, want one at %s", coded.Fields, tc.path)
				}
				if !strings.Contains(coded.Detail, tc.detail) {
					t.Errorf("detail %q, want it to contain %q", coded.Detail, tc.detail)
				}
				if got := crMutated(h); len(got) != 0 || len(h.rec.writes) != 0 {
					t.Errorf("the refusal changed the host: calls %v, files %+v", got, h.rec.writes)
				}
			})
		}
	}
	t.Run("the Engine down", func(t *testing.T) {
		h := newCR(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cfg.connectorRun().Handler.Plan(ctx, h.env(""), crInput(crTunnel, "tok"))
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
}

func TestCrApply(t *testing.T) {
	ctx := context.Background()
	h := newCR(t)
	token := agToken(crTunnel)
	out, err := crApply(t, h, "c1", crTunnel)
	if err != nil {
		t.Fatal(err)
	}
	c, err := h.docker.InspectContainer(ctx, connectorName(crTunnel))
	if err != nil {
		t.Fatal(err)
	}
	if out.Tunnel != crTunnel || out.Container != connectorName(crTunnel) || out.ContainerID != c.ID || out.State != "running" {
		t.Errorf("output %+v", out)
	}

	t.Run("the container", func(t *testing.T) {
		wantCmd := []string{"tunnel", "--no-autoupdate", "run", "--token-file", "/run/darkpanel/token"}
		wantMounts := []dockerapi.Mount{{Type: "bind", Source: tokenDir + "/" + crTunnel + ".token", Target: "/run/darkpanel/token", ReadOnly: true}}
		if !c.State.Running || c.Config.Image != Image || c.Config.Labels[core.LabelModule] != "cloudflare" ||
			!slices.Equal(c.Config.Cmd, wantCmd) || len(c.Config.Entrypoint) != 0 ||
			c.HostConfig.RestartPolicy.Name != "unless-stopped" || !slices.Equal(c.HostConfig.Mounts, wantMounts) || len(c.HostConfig.Binds) != 0 {
			t.Errorf("container %+v host %+v", c.Config, c.HostConfig)
		}
		if strings.Contains(strings.Join(c.Config.Env, "\n")+strings.Join(c.Config.Cmd, "\n"), token) {
			t.Error("the token is in the container's environment or command line")
		}
		if got, want := crMutated(h), []string{"PullImage", "CreateContainer", "StartContainer"}; !slices.Equal(got, want) {
			t.Errorf("Engine calls %v, want %v", got, want)
		}
	})

	t.Run("the token file", func(t *testing.T) {
		if len(h.rec.writes) != 1 {
			t.Fatalf("%d files written, want 1", len(h.rec.writes))
		}
		w := h.rec.writes[0]
		if w.Dir != tokenDir || w.Name != tokenFileName(crTunnel) || string(w.Data) != token || w.Mode != 0o400 || w.UID != imageUID || w.GID != imageUID {
			t.Errorf("write %+v, want the token in %s/%s with mode 0400 and owner %d:%d", w, tokenDir, tokenFileName(crTunnel), imageUID, imageUID)
		}
	})

	t.Run("the row", func(t *testing.T) {
		env := h.env("")
		row, ok, err := loadConnector(ctx, env.DB.Read(), crTunnel)
		if err != nil || !ok {
			t.Fatalf("row %+v, %v, %v", row, ok, err)
		}
		if row.ContainerID != c.ID || row.ChangeID != "c1" || !row.CreatedAt.Equal(env.Clock.Now()) {
			t.Errorf("row %+v", row)
		}
		if bytes.Contains(row.TokenCT, []byte(token)) {
			t.Error("the stored token is not sealed")
		}
		opened, err := env.Sealer.Open(connectorSealAD(crTunnel), row.TokenCT)
		if err != nil || string(opened) != token {
			t.Errorf("the sealed token opens to %q, %v", opened, err)
		}
	})

	t.Run("the journal", func(t *testing.T) {
		effs, err := h.env("c1").Effects.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		type want struct {
			kind     core.EffectKind
			resource string
			recovery string
			local    bool
			wait     time.Duration
		}
		wants := []want{
			{core.EffectCreate, "cloudflare.tokenfile:" + crTunnel, crTunnel, true, 0},
			{core.EffectCreate, "cloudflare.container:" + c.ID, connectorName(crTunnel), false, 2 * time.Minute},
			{core.EffectCreate, "cloudflare.connector:" + crTunnel, crTunnel, true, 0},
			{core.EffectModify, "cloudflare.container:" + c.ID, "", false, 2 * time.Minute},
		}
		if len(effs) != len(wants) {
			t.Fatalf("%d effects, want %d: %+v", len(effs), len(wants), effs)
		}
		for i, w := range wants {
			e := effs[i]
			if e.Kind != w.kind || e.Resource != w.resource || e.State != core.EffectMade || e.Local != w.local || e.InverseLocal != w.local ||
				e.CommitWait != w.wait || e.ChangeID != "c1" || e.Seq != i+1 || e.Post == nil {
				t.Errorf("effect %d = %+v, want a made %s of %s (local %t, commit wait %s)", i+1, e, w.kind, w.resource, w.local, w.wait)
			}
			switch {
			case w.kind == core.EffectCreate:
				if e.Recovery == nil || e.Recovery.Kind != "name" || e.Recovery.Value != w.recovery || string(e.Before.State) != "null" {
					t.Errorf("effect %d recovery %+v before %s, want name %s and a null before", i+1, e.Recovery, e.Before.State, w.recovery)
				}
			case e.Recovery != nil:
				t.Errorf("effect %d declares a recovery identity", i+1)
			}
		}
		for _, i := range []int{0, 2} {
			if effs[i].After == nil || string(effs[i].After.State) != string(effs[i].Post.State) {
				t.Errorf("local effect %d: after %v, post %s, want them equal", i+1, effs[i].After, effs[i].Post.State)
			}
		}
		var file struct {
			Name string `json:"name"`
			Size int    `json:"size"`
		}
		if err := json.Unmarshal(effs[0].Post.State, &file); err != nil || file.Name != tokenFileName(crTunnel) || file.Size != len(token) {
			t.Errorf("token file state %s (%v), want its name and %d bytes", effs[0].Post.State, err, len(token))
		}
		var before, post map[string]any
		_ = json.Unmarshal(effs[3].Before.State, &before)
		_ = json.Unmarshal(effs[3].Post.State, &post)
		if before["id"] != c.ID || before["running"] != false || post["running"] != true || post["startedAt"] == nil {
			t.Errorf("the start's before %v and post %v, want the container stopped and then running", before, post)
		}
		if _, ok := before["startedAt"]; ok {
			t.Errorf("the start's before %v holds a timestamp no undo can restore", before)
		}
		raw, _ := json.Marshal(effs)
		if bytes.Contains(raw, []byte(token)) {
			t.Error("an effect holds the token")
		}
		for seq := 1; seq <= len(effs); seq++ {
			if s, _ := h.env("c1").Effects.Secret(ctx, seq); len(s) != 0 {
				t.Errorf("effect %d carries a secret", seq)
			}
		}
	})
}

func TestCrApplyPullsOnlyAMissingImage(t *testing.T) {
	t.Run("absent", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		if !slices.Contains(h.docker.calls(), "PullImage") {
			t.Errorf("calls %v: the image was not pulled", h.docker.calls())
		}
		if _, err := h.docker.InspectImage(context.Background(), Image); err != nil {
			t.Errorf("the image is not on the host: %v", err)
		}
	})
	t.Run("present", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		if slices.Contains(h.docker.calls(), "PullImage") {
			t.Errorf("calls %v: a present image was pulled", h.docker.calls())
		}
	})
}

func TestCrApplyFailures(t *testing.T) {
	ctx := context.Background()
	effects := func(t *testing.T, h *crHost) []core.Effect {
		t.Helper()
		effs, err := h.env("c1").Effects.List(ctx)
		if err != nil {
			t.Fatal(err)
		}
		return effs
	}
	t.Run("a failed pull changes nothing", func(t *testing.T) {
		h := newCR(t)
		h.docker.Fail("PullImage", errors.New("boom"))
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		if len(h.rec.writes) != 0 || len(effects(t, h)) != 0 {
			t.Errorf("files %+v, effects %+v: want none", h.rec.writes, effects(t, h))
		}
	})
	t.Run("a refused create is Definite and keeps the file effect", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("CreateContainer", &dockerapi.APIError{StatusCode: http.StatusConflict, Message: "name in use"})
		_, err := crApply(t, h, "c1", crTunnel)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		effs := effects(t, h)
		if len(effs) != 2 || effs[0].State != core.EffectMade || effs[1].State != core.EffectSkipped {
			t.Errorf("effects %+v, want the token file made and the container skipped", effs)
		}
		if crHasRow(t, h, crTunnel) {
			t.Error("a row was inserted for a container that was never created")
		}
	})
	t.Run("an unknown create failure stays intended", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.docker.Fail("CreateContainer", errors.New("timeout"))
		_, err := crApply(t, h, "c1", crTunnel)
		effs := effects(t, h)
		if err == nil || core.IsDefinite(err) || len(effs) != 2 || effs[1].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and an intended container effect", err, effs)
		}
	})
	t.Run("a failed file write creates nothing", func(t *testing.T) {
		h := newCR(t)
		h.docker.putImage(Image)
		h.rec.failWrite = errors.New("disk full")
		_, err := crApply(t, h, "c1", crTunnel)
		effs := effects(t, h)
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and one intended file effect", err, effs)
		}
		if slices.Contains(h.docker.calls(), "CreateContainer") {
			t.Error("a container was created without its token file")
		}
	})
}

func TestCrVerify(t *testing.T) {
	ctx := context.Background()
	token := agToken(crTunnel)
	edit := func(change func(c *dockerapi.ContainerJSON)) func(t *testing.T, h *crHost, id string) {
		return func(t *testing.T, h *crHost, id string) {
			t.Helper()
			c, err := h.docker.InspectContainer(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			change(&c)
			h.docker.putContainer(c)
		}
	}
	for _, tc := range []struct {
		name   string
		change func(t *testing.T, h *crHost, id string)
		fails  bool
	}{
		{"as deployed", func(*testing.T, *crHost, string) {}, false},
		{"a restarting container", edit(func(c *dockerapi.ContainerJSON) {
			c.State.Status, c.State.Running, c.State.Restarting = "restarting", false, true
		}), false},
		{"an exited container", edit(func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "exited", false }), true},
		{"another image", edit(func(c *dockerapi.ContainerJSON) { c.Config.Image = "cloudflare/cloudflared:latest" }), true},
		{"no module label", edit(func(c *dockerapi.ContainerJSON) { c.Config.Labels = map[string]string{} }), true},
		{"another command", edit(func(c *dockerapi.ContainerJSON) { c.Config.Cmd = []string{"tunnel", "run", "--token-file", "/other"} }), true},
		{"the token in the environment", edit(func(c *dockerapi.ContainerJSON) { c.Config.Env = append(c.Config.Env, "TUNNEL_TOKEN="+token) }), true},
		{"the token in the command", edit(func(c *dockerapi.ContainerJSON) { c.Config.Cmd = append(slices.Clone(c.Config.Cmd), token) }), true},
		{"a writable mount", edit(func(c *dockerapi.ContainerJSON) {
			c.HostConfig.Mounts = slices.Clone(c.HostConfig.Mounts)
			c.HostConfig.Mounts[0].ReadOnly = false
		}), true},
		{"no mount", edit(func(c *dockerapi.ContainerJSON) { c.HostConfig.Mounts = nil }), true},
		{"no restart policy", edit(func(c *dockerapi.ContainerJSON) { c.HostConfig.RestartPolicy.Name = "no" }), true},
		{"no row", func(t *testing.T, h *crHost, _ string) {
			t.Helper()
			env := h.env("")
			if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, crTunnel) }); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"a row of another container", func(t *testing.T, h *crHost, _ string) {
			t.Helper()
			env := h.env("")
			err := env.DB.Write(ctx, func(tx *sql.Tx) error {
				if err := deleteConnector(ctx, tx, crTunnel); err != nil {
					return err
				}
				return insertConnector(ctx, tx, connectorRow{TunnelID: crTunnel, ContainerID: "another", TokenCT: []byte("x"), ChangeID: "c1", CreatedAt: env.Clock.Now()})
			})
			if err != nil {
				t.Fatal(err)
			}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newCR(t)
			out, err := crApply(t, h, "c1", crTunnel)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(t, h, out.ContainerID)
			rawOut, _ := json.Marshal(out)
			err = h.cfg.connectorRun().Handler.Verify(ctx, h.env("c1"), crInput(crTunnel, token), rawOut)
			switch {
			case tc.fails && err == nil:
				t.Error("Verify passed")
			case !tc.fails && err != nil:
				t.Errorf("Verify: %v", err)
			}
			if err != nil && strings.Contains(err.Error(), token) {
				t.Error("the error carries the token")
			}
		})
	}
	t.Run("the container is gone", func(t *testing.T) {
		h := newCR(t)
		out, err := crApply(t, h, "c1", crTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if err := h.docker.RemoveContainer(ctx, out.ContainerID, dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		rawOut, _ := json.Marshal(out)
		if err := h.cfg.connectorRun().Handler.Verify(ctx, h.env("c1"), crInput(crTunnel, token), rawOut); err == nil {
			t.Error("Verify passed")
		}
	})
}

// crUndo undoes the made effects of the change as the engine does: in reverse order, each checked against its
// journaled post-state before and against its undo target after.
func crUndo(t *testing.T, h *crHost, changeID string) {
	t.Helper()
	ctx := context.Background()
	def := h.cfg.connectorRun()
	env := h.env(changeID)
	effs, err := env.Effects.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.ChangeID != changeID || e.State != core.EffectMade {
			continue
		}
		obs, err := def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe effect %d (%s): %v", e.Seq, e.Resource, err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("effect %d (%s): observed %s before its undo, journaled %s", e.Seq, e.Resource, obs.Current.State, e.Post.State)
		}
		e.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
			t.Fatalf("inverse of effect %d (%s): %v", e.Seq, e.Resource, err)
		}
		obs, err = def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe effect %d (%s) after its undo: %v", e.Seq, e.Resource, err)
		}
		want := "null"
		if e.Kind != core.EffectCreate {
			want = string(e.Before.State)
		}
		if string(obs.Current.State) != want {
			t.Errorf("effect %d (%s): observed %s after its undo, want %s", e.Seq, e.Resource, obs.Current.State, want)
		}
	}
}

func TestCrUndo(t *testing.T) {
	ctx := context.Background()
	t.Run("everything the change made", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		crUndo(t, h, "c1")
		if _, err := h.docker.InspectContainer(ctx, connectorName(crTunnel)); !dockerapi.IsNotFound(err) {
			t.Errorf("the container is still there: %v", err)
		}
		if _, err := h.rec.read(tokenDir + "/" + tokenFileName(crTunnel)); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("the token file is still there: %v", err)
		}
		if crHasRow(t, h, crTunnel) {
			t.Error("the row is still there")
		}
		if !slices.Contains(h.docker.calls(), "StopContainer") {
			t.Errorf("calls %v: the started container was not stopped", h.docker.calls())
		}
	})
	t.Run("an undo run again", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		crUndo(t, h, "c1")
		env := h.env("c1")
		effs, _ := env.Effects.List(ctx)
		for i := len(effs) - 1; i >= 0; i-- {
			e := effs[i]
			e.State = core.EffectUndoing
			if _, err := h.cfg.connectorRun().Handler.Inverse(ctx, env, e); err != nil {
				t.Errorf("inverse of effect %d again: %v", e.Seq, err)
			}
		}
	})
	t.Run("a row of another change is kept", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		env := h.env("c1")
		effs, _ := env.Effects.List(ctx)
		e := effs[2]
		e.ChangeID, e.State = "c2", core.EffectUndoing
		_, err := h.cfg.connectorRun().Handler.Inverse(ctx, env, e)
		crRefused(t, err, errcode.InvalidInput)
		if !crHasRow(t, h, crTunnel) {
			t.Error("the row of another change was deleted")
		}
	})
}

func TestCrObserveCreations(t *testing.T) {
	ctx := context.Background()
	creations := []core.Effect{
		{Kind: core.EffectCreate, Resource: "cloudflare.tokenfile:?" + crTunnel},
		{Kind: core.EffectCreate, Resource: "cloudflare.container:?" + connectorName(crTunnel)},
		{Kind: core.EffectCreate, Resource: "cloudflare.connector:?" + crTunnel},
	}
	h := newCR(t)
	def := h.cfg.connectorRun()
	for _, e := range creations {
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
			t.Errorf("%s before the apply: matches %d state %s (%v), want none and null", e.Resource, obs.Matches, obs.Current.State, err)
		}
	}
	if _, err := crApply(t, h, "c1", crTunnel); err != nil {
		t.Fatal(err)
	}
	for _, e := range creations {
		obs, err := def.Handler.Observe(ctx, h.env("c1"), e)
		want := strings.Replace(e.Resource, "?", "", 1)
		if strings.HasPrefix(e.Resource, "cloudflare.container:") {
			c, _ := h.docker.InspectContainer(ctx, connectorName(crTunnel))
			want = "cloudflare.container:" + c.ID
		}
		if err != nil || obs.Matches != 1 || obs.Resource != want || !obs.Exclusive {
			t.Errorf("%s after the apply: matches %d resource %q exclusive %t (%v), want one match of %s", e.Resource, obs.Matches, obs.Resource, obs.Exclusive, err, want)
		}
	}
}

func TestCrRemoveConnector(t *testing.T) {
	ctx := context.Background()
	t.Run("everything", func(t *testing.T) {
		h := newCR(t)
		if _, err := crApply(t, h, "c1", crTunnel); err != nil {
			t.Fatal(err)
		}
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || !removed {
			t.Fatalf("removed %t, %v", removed, err)
		}
		if _, err := h.docker.InspectContainer(ctx, connectorName(crTunnel)); !dockerapi.IsNotFound(err) {
			t.Errorf("the container is still there: %v", err)
		}
		if !slices.Contains(h.rec.removes, tokenDir+"/"+tokenFileName(crTunnel)) || crHasRow(t, h, crTunnel) {
			t.Errorf("removed files %v, row present %t", h.rec.removes, crHasRow(t, h, crTunnel))
		}
		again, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || again {
			t.Errorf("removing again: %t, %v; want false and no error", again, err)
		}
	})
	t.Run("nothing to remove", func(t *testing.T) {
		h := newCR(t)
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || removed {
			t.Errorf("removed %t, %v; want false and no error", removed, err)
		}
	})
	t.Run("only the row", func(t *testing.T) {
		h := newCR(t)
		crSeedRow(t, h, crTunnel, "gone")
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if err != nil || !removed || crHasRow(t, h, crTunnel) {
			t.Errorf("removed %t, %v, row present %t", removed, err, crHasRow(t, h, crTunnel))
		}
	})
	t.Run("only the container", func(t *testing.T) {
		h := newCR(t)
		h.docker.putContainer(agContainer(connectorName(crTunnel)))
		removed, err := removeConnector(ctx, h.env("c2"), h.cfg, crTunnel)
		if _, ierr := h.docker.InspectContainer(ctx, connectorName(crTunnel)); err != nil || !removed || !dockerapi.IsNotFound(ierr) {
			t.Errorf("removed %t, %v, inspect %v", removed, err, ierr)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/6 && export TMPDIR=/root/.cache/w6b-tmp/6 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCr' ./internal/modules/cloudflare/
```

Expected: a build failure naming the first undefined identifier, `cfg.a.readFile undefined (type agentDeps has no field or method readFile)` or `h.cfg.connectorRun undefined`. Quote it in the report.

- [ ] **Step 3: Implement**

1. `connector_deps.go`: add `readFile func(name string) ([]byte, error)` to `agentDeps` with the comment `os.ReadFile`, and set `readFile: os.ReadFile` in `productionAgentDeps`. Nothing else in the file changes.
2. `connector_run.go`:
   - `func (c cf) connectorRun() core.ActionDef` returns `core.DefineMutating[ConnectorRunInput, Connector]` with `Name: ID + ".connector.run"`, a one-sentence `Summary`, `Version: 1`, `Class: core.ClassCompensable`, `Side: core.SideAgent`, `LockKeys: []string{"cloudflare"}`, `LongRunning: true`, `ApplyTimeout: 10 * time.Minute`, and a `connectorRunHandler{c}`.
   - `connectorCreate(tunnel string) dockerapi.ContainerCreate` builds the container exactly as the decisions above list it.
   - A precheck shared by Plan and Apply: it collects every bad field into one Definite `request.invalid_input` refusal with `Fields` (`/tunnel` from `tunnelIDProblem`, `/token` for an empty token; the detail is the messages joined by `; `), then refuses an existing row (`a connector for tunnel <id> is already deployed on this host`) and an existing container (`a Docker container named <name> already exists`), each Definite `request.invalid_input`. Engine and database failures are returned wrapped, an unreachable Engine through `engineCoded`, and are not Definite. The token appears in no message.
   - Plan returns the three diff lines of `TestCrPlanShowsTheRun` verbatim and `StateHash` = hex SHA-256 of `Image + "\n" + tunnel`; it changes nothing and pulls nothing.
   - Apply runs the precheck, pulls `Image` when `InspectImage` reports not-found (a failed pull or a stream message with an error is a Definite refusal wrapped with `engineCoded`; a present image is left alone), then the four journaled steps in the order given above through one journaling helper (below), and returns `Connector{Tunnel, Container: connectorName(tunnel), ContainerID: <id>, State: <Status of a last InspectContainer>}`. The file is written with `Env.Files.WriteManaged(ctx, ID, tokenDir, tokenFileName(tunnel), []byte(token), 0o400, imageUID, imageUID)`; the row is inserted in one `Env.DB.Write` with `insertConnector`, the token sealed by `Env.Sealer.Seal(connectorSealAD(tunnel), token)`, `ChangeID: env.ChangeID` and `CreatedAt: env.Clock.Now()`.
   - Verify (the output's tunnel is the input's): it fails, with a message that never contains the token, unless the row exists and holds the container's id, the container exists, its image is `Image`, its label `core.LabelModule` is `ID`, its `Cmd` is the five arguments, its restart policy is `unless-stopped`, its `HostConfig.Mounts` hold the one read-only bind mount, its `Status` is `running` or `restarting`, and neither its `Env` nor its `Cmd` contains the token.
   - `removeConnector(ctx, env, c, tunnel)` removes the container named `connectorName(tunnel)` with `Force` (a not-found answer counts as absent), then the token file with `env.Files.RemoveManaged`, then the row with `deleteConnector` in one `Env.DB.Write`; it returns true when the container or the row existed, and wraps Engine errors with `engineCoded`.
3. `connector_effects.go`, mirroring `internal/modules/npm/effects.go` and `instance_deploy.go` for the cloudflare package:
   - Every name of Contracts "Agent effects", exactly as stated there: the nouns `nounTokenFile`, `nounContainer`, `nounConnector`, `engineCommitWait`, `resourceOf`, `identityOf`, `connectorEffect` (forces the null before-state and the `name` recovery identity on a Create whose identity starts with `?`), `refused`, `nullState`, `named`, and the journaling helper `func (c cf) journal(...)` (npm's `run`: `Intend`, the call, a 4xx Engine answer or a Definite error from the call marks the effect `Skipped` and returns a Definite error, any other failure leaves it intended, otherwise `Made` with the state observed for the resource the call names; a resource still starting with `?` is an error). Tasks 8, 12 and 13 call these names, so they are not renamed.
   - `Observe` (the handler's `core.Observer`) handles each noun: the token file as `{"name","size"}` from `readFile(tokenDir + "/" + name)` (not-exist is null); the row as its three-field document from `loadConnector` (absent is null); containers through `InspectContainer` and `dstate.ContainerRun` (not-found is null). For a creation identity (`?`) it reports `Matches` 0 with the null state when the object is absent and `Matches` 1 with `Resource` set to the made resource, `Exclusive` true, when present; for a made resource it reports `Matches` 1.
   - `Inverse` handles the cases in the decisions above and returns an error for any other resource; Engine errors map as the decisions say (a 4xx answer Definite, an unreachable Engine through `engineCoded`).
4. Do not register the action in `Manifest()` (Task 14 does).

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/6 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCr' ./internal/modules/cloudflare/
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/6 && go clean -testcache
```

Expected: every test passes, `0 issues.`, no output from `go vet` and `go mod tidy -diff`; `modules.TestSnapshotsCurrent` stays green because the action is not registered. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/connector_run.go internal/modules/cloudflare/connector_effects.go internal/modules/cloudflare/connector_deps.go internal/modules/cloudflare/connector_run_test.go
git commit -m "feat(cloudflare): add connector.run"
```

### Task 7: cloudflare: connector.list and the log-secret source

**Files:**
- Create: `internal/modules/cloudflare/connector_list.go`
- Create: `internal/modules/cloudflare/connector_logsecrets.go`
- Test: `internal/modules/cloudflare/connector_list_test.go`
- Test: `internal/modules/cloudflare/connector_logsecrets_test.go`

**Interfaces:**
- Consumes (Contracts): `ConnectorListInput`, `ConnectorList`, `Connector` (Task 3 wire types); `listConnectors`, `connectorByContainer`, `insertConnector`, `deleteConnector`, `connectorRow`, `connectorSealAD`, `connectorName`; `cf` with `a agentDeps`; `engineCoded`; the Agent test host (`newAGHost`, `h.cf()`, `h.env`, `h.docker` with `putContainer`, `Fail`, `calls`, `agContainer`, `agToken`); `docker.RegisterLogSecrets` and `docker/logfilter` as `internal/modules/npm/reveal.go` and `reveal_test.go` use them.
- Produces (Contracts "Agent actions"): `func (c cf) connectorList() core.ActionDef` for `cloudflare.connector.list` v1, and `func connectorLogSecrets(ctx context.Context, env core.Env, containerID string) ([][]byte, error)` registered by an `init` as the `cloudflare` log-secret source.

**Items:** 34, 324

**Depends on:** Task 3

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7
**Lenses:** security

Decisions the tests pin:

- `cloudflare.connector.list` is a read action on the agent (`core.DefineRead`, no lock key, the default read scope). It answers one `Connector` per `cloudflare_connectors` row, ordered by tunnel id: `Container` is `connectorName(tunnel)`, `ContainerID` the row's id, and `State` the `State.Status` of `InspectContainer(row.ContainerID)`, or `missing` when that answers not-found. A container with the connector's name but another id is not the row's container, and a container without a row is not listed. With no rows the answer is `{"connectors":[]}`, not `null`. An unreachable Engine is `docker.engine_unavailable`, and any other inspect failure is returned as an error: neither reads as `missing`. The read makes no Engine call but `InspectContainer` and never shows a token.
- The log-secret source answers the opened token of the row whose `container_id` is the container asked about, and `nil, nil` for any other container. A token that cannot be opened is an error without the token in its text, so the docker module refuses the log read instead of serving it unmasked. The docker module already filters by the `darkpanel.module` label, so the source reads only the agent database. Task 6 inserts the row before it starts the container, so every running connector has its token registered.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/cloudflare/connector_list_test.go`:

```go
package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	clTunnelA = "0a000000-0000-4000-8000-00000000000a"
	clTunnelB = "0b000000-0000-4000-8000-00000000000b"
	clTunnelC = "0c000000-0000-4000-8000-00000000000c"
)

// clSeed records a connector row for tunnel with its token sealed as connector.run seals it.
func clSeed(t *testing.T, h *agHost, tunnel, containerID string) {
	t.Helper()
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(tunnel), []byte(agToken(tunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: tunnel, ContainerID: containerID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// clState sets the container's state as the Engine reports it.
func clState(status string) func(*dockerapi.ContainerJSON) {
	return func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = status, status == "running" }
}

func clRead(t *testing.T, h *agHost) (json.RawMessage, error) {
	t.Helper()
	return h.cf().connectorList().Handler.Read(context.Background(), h.env(""), json.RawMessage(`{}`))
}

func TestClSpec(t *testing.T) {
	def := newAGHost(t).cf().connectorList()
	s := def.Spec
	if s.Name != "cloudflare.connector.list" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent ||
		len(s.LockKeys) != 0 || s.Summary == "" || def.Handler.Read == nil {
		t.Errorf("spec %+v", s)
	}
	if core.RequiredScope(s) != core.ScopeRead {
		t.Errorf("scope %s, want read", core.RequiredScope(s))
	}
}

func TestClListReportsEveryRowWithItsContainerState(t *testing.T) {
	h := newAGHost(t)
	a := agContainer(connectorName(clTunnelA), clState("running"))
	b := agContainer(connectorName(clTunnelB), clState("exited"))
	stranger := agContainer("darkpanel-cloudflared-0d000000-0000-4000-8000-00000000000d", clState("running"))
	// A container with connector C's name that is not the container of C's row.
	impostor := agContainer(connectorName(clTunnelC), clState("running"))
	for _, c := range []dockerapi.ContainerJSON{a, b, stranger, impostor} {
		h.docker.putContainer(c)
	}
	clSeed(t, h, clTunnelB, b.ID)
	clSeed(t, h, clTunnelC, "the-container-that-was-removed")
	clSeed(t, h, clTunnelA, a.ID)

	raw, err := clRead(t, h)
	if err != nil {
		t.Fatal(err)
	}
	var got ConnectorList
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []Connector{
		{Tunnel: clTunnelA, Container: connectorName(clTunnelA), ContainerID: a.ID, State: "running"},
		{Tunnel: clTunnelB, Container: connectorName(clTunnelB), ContainerID: b.ID, State: "exited"},
		{Tunnel: clTunnelC, Container: connectorName(clTunnelC), ContainerID: "the-container-that-was-removed", State: "missing"},
	}
	if !slices.Equal(got.Connectors, want) {
		t.Errorf("connectors %+v, want %+v", got.Connectors, want)
	}
	for _, c := range h.docker.calls() {
		if c != "InspectContainer" {
			t.Errorf("the read called %s, want only InspectContainer", c)
		}
	}
	for _, tunnel := range []string{clTunnelA, clTunnelB, clTunnelC} {
		if strings.Contains(string(raw), agToken(tunnel)) {
			t.Errorf("the answer carries the token of %s", tunnel)
		}
	}
}

func TestClListWithoutRowsIsAnEmptyList(t *testing.T) {
	raw, err := clRead(t, newAGHost(t))
	if err != nil {
		t.Fatal(err)
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(raw, &members); err != nil || string(members["connectors"]) != "[]" {
		t.Errorf("answer %s (%v), want connectors to be an empty list", raw, err)
	}
}

func TestClListFailsWhenTheEngineCannotAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		code errcode.Code // "" for an error that need carry no code
	}{
		{"the Engine is down", dockerapi.ErrUnavailable, errcode.DockerEngineUnavailable},
		{"the Engine fails", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newAGHost(t)
			c := agContainer(connectorName(clTunnelA), clState("running"))
			h.docker.putContainer(c)
			clSeed(t, h, clTunnelA, c.ID)
			h.docker.Fail("InspectContainer", tc.err)
			raw, err := clRead(t, h)
			if err == nil || len(raw) != 0 {
				t.Fatalf("answer %s, error %v; want an error and no answer", raw, err)
			}
			if tc.code != "" && errcode.From(err).Code != tc.code {
				t.Errorf("error %v, want code %s", err, tc.code)
			}
		})
	}
}
```

Create `internal/modules/cloudflare/connector_logsecrets_test.go`:

```go
package cloudflare

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/modules/docker"
	"github.com/darkraise/darkpanel/internal/modules/docker/logfilter"
)

func TestClLogSecretsAreTheStoredToken(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	clSeed(t, h, clTunnelB, "container-b")

	for tunnel, id := range map[string]string{clTunnelA: "container-a", clTunnelB: "container-b"} {
		token := agToken(tunnel)
		secrets, err := connectorLogSecrets(ctx, h.env(""), id)
		if err != nil || len(secrets) != 1 || string(secrets[0]) != token {
			t.Fatalf("secrets for %s = %q, %v; want exactly the token of tunnel %s", id, secrets, err, tunnel)
		}
		f := logfilter.New(secrets)
		out := string(f.Push("stdout", []byte("registered tunnel connection with "+token+" ok\n"))) + string(f.Flush("stdout"))
		if strings.Contains(out, token) || !strings.Contains(out, logfilter.Mask) {
			t.Errorf("filtered line %q, want the token replaced by %q", out, logfilter.Mask)
		}
	}
}

func TestClLogSecretsOfAnotherContainerAreNone(t *testing.T) {
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	for _, id := range []string{"container-of-another-module", "", "container-"} {
		secrets, err := connectorLogSecrets(context.Background(), h.env(""), id)
		if secrets != nil || err != nil {
			t.Errorf("secrets for %q = %q, %v; want none and no error", id, secrets, err)
		}
	}
	empty, err := connectorLogSecrets(context.Background(), newAGHost(t).env(""), "container-a")
	if empty != nil || err != nil {
		t.Errorf("secrets before any connector exists = %q, %v; want none and no error", empty, err)
	}
}

// TestClLogSecretsFailClosed: a stored token that cannot be opened is an error, which makes docker refuse the log
// read rather than show the connector's logs unmasked.
func TestClLogSecretsFailClosed(t *testing.T) {
	ctx := context.Background()
	h := newAGHost(t)
	clSeed(t, h, clTunnelA, "container-a")
	env := h.env("")
	err := env.DB.Write(ctx, func(tx *sql.Tx) error {
		if err := deleteConnector(ctx, tx, clTunnelA); err != nil {
			return err
		}
		return insertConnector(ctx, tx, connectorRow{TunnelID: clTunnelA, ContainerID: "container-a", TokenCT: []byte("not a sealed value"),
			ChangeID: "c0", CreatedAt: time.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	secrets, err := connectorLogSecrets(ctx, env, "container-a")
	if err == nil || secrets != nil {
		t.Fatalf("secrets = %q, %v; want an error and no secret", secrets, err)
	}
	if strings.Contains(err.Error(), agToken(clTunnelA)) {
		t.Error("the error carries the token")
	}
}

// TestClLogSecretsAreRegistered: the package registered its source with the docker module when it was imported, so
// a second registration for the module id panics.
func TestClLogSecretsAreRegistered(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("registering a second source for cloudflare did not panic, so the package registered none")
		}
	}()
	docker.RegisterLogSecrets(ID, func(context.Context, core.Env, string) ([][]byte, error) { return nil, nil })
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/7 && export TMPDIR=/root/.cache/w6b-tmp/7 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCl' ./internal/modules/cloudflare/
```

Expected: a build failure naming `connectorList` (`h.cf().connectorList undefined`), then `connectorLogSecrets`. Quote it in the report.

- [ ] **Step 3: Implement**

1. `connector_list.go`: `func (c cf) connectorList() core.ActionDef` returns `core.DefineRead[ConnectorListInput, ConnectorList]` with `Name: ID + ".connector.list"`, a one-sentence `Summary`, `Version: 1`, `Side: core.SideAgent` and a reader type holding the `cf`, modelled on `internal/modules/npm/instance_status.go`. `Read` loads `listConnectors(ctx, env.DB.Read())` and builds a non-nil `[]Connector` in that order. For each row it calls `InspectContainer(ctx, row.ContainerID)`: `dockerapi.IsNotFound` gives `State: "missing"`, success gives `State: c.State.Status`, any other error returns `cloudflare: inspect container <name>: <err wrapped by engineCoded>`.
2. `connector_logsecrets.go`: an `init` calls `docker.RegisterLogSecrets(ID, connectorLogSecrets)`; `connectorLogSecrets` loads `connectorByContainer(ctx, env.DB.Read(), containerID)`, returns `nil, nil` when there is no row, opens `row.TokenCT` with `env.Sealer.Open(connectorSealAD(row.TunnelID), …)` and returns it as the one secret. An open failure returns `cloudflare: open the token of tunnel <id>: <err>` with no secret. Give the function a doc comment that says why it is fail-closed, as npm's `revealLogSecrets` does.
3. Do not register the read action in `Manifest()` (Task 14 does). Importing `internal/modules/docker` adds no import cycle (the docker module imports nothing of cloudflare).

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/7 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestCl' ./internal/modules/cloudflare/
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/7 && go clean -testcache
```

Expected: all pass, `0 issues.`, no output from vet and tidy. `go test ./...` also runs the docker and npm packages, whose own `init` registrations are unaffected. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/connector_list.go internal/modules/cloudflare/connector_list_test.go
git commit -m "feat(cloudflare): add connector.list"
git add internal/modules/cloudflare/connector_logsecrets.go internal/modules/cloudflare/connector_logsecrets_test.go
git commit -m "feat(cloudflare): mask the tunnel token in logs"
```

### Task 8: cloudflare: network.attach claims

**Files:**
- Create: `internal/modules/cloudflare/connector_network.go`
- Test: `internal/modules/cloudflare/connector_network_test.go`

**Interfaces:**
- Consumes (Contracts): `NetworkAttachInput`, `NetworkAttachment` (Task 3 wire types); `claimRow`, `connectorRow`, `loadConnector`, `connectorClaims`, `insertClaim`, `deleteClaim`, `insertConnector`, `deleteConnector`, `connectorSealAD`; `connectorName`, `tunnelIDProblem`, `claimantProblem`, `networkNamePattern`, `engineCoded`; `cf` with `a agentDeps`; the Agent test host (`newAGHost`, `h.cf()`, `h.env`, `h.docker` with `putContainer`, `putNetwork`, `Fail`, `calls`, `agContainer`, `agOnNetwork`, `agNetwork`, `agToken`); `docker/dstate` (`Endpoint`, `Phase`); `ID`.
- Produces (Contracts "Agent actions"): `func (c cf) networkAttach() core.ActionDef` for `cloudflare.network.attach` v1; `claimDocument(c claimRow) (json.RawMessage, error)`; the handler type `networkClaims` (struct with field `c cf`) with `Observe` and `Inverse`, which Task 13's detach handler embeds; the constants `nounClaim = "cloudflare.claim"` and `nounEndpoint = "cloudflare.endpoint"`, the type `claimDoc`, and the effect resources `cloudflare.claim:<tunnel>|<network>|<claimant>` and `cloudflare.endpoint:<tunnel>|<network>` (creation forms with a `?` before the identity).
- Every other top-level identifier this file declares starts with `attach` or `claim` or is `networkAttachHandler`/`networkAttachCheck`, so none collides with Task 6's (`connector…`, `eff…`) or Task 13's.

**Items:** 35

**Depends on:** Task 3, Task 6

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

`cloudflare.network.attach` joins the tunnel's connector container to a user-defined bridge network, so the connector can reach a container there by name, and records the claimant that needs it. It mirrors `internal/modules/npm/network_claims.go` (read it first), with the tunnel in every identity. Decisions the tests pin:

- Validation, a Definite `request.invalid_input` naming every bad field: `/tunnel` (`tunnelIDProblem`), `/network` (`networkNamePattern`), `/claimant` (`claimantProblem`), `/target` when set (`networkNamePattern`), in the detail joined by `; `. Then, in this order, each a Definite refusal: no `cloudflare_connectors` row for the tunnel, or no container named `connectorName(tunnel)` → `cloudflare.connector_missing`; the network does not exist → `not_found`; the supported-target rule → `cloudflare.unsupported_target` with the detail exactly `connect the container to a user-defined network`; the target does not exist → `not_found`; the connector is already on the network with another fixed address than the input's → `request.invalid_input` at `/address`.
- The supported-target rule: the network must be a user-defined bridge (its `Driver` is `bridge` and its name is none of `bridge`, `host`, `none`), and a named `Target` must be attached to it (`NetworkSettings.Networks[<network name>]` is not nil); the rule is judged on the network alone when there is no target. A target attached only to other networks (the default bridge included) is refused.
- Plan lists what Apply will do: when the connector is not on the network, `+ container <name> joins network <network>` and, with an address, `+ ipv4 <addr>` or `+ ipv6 <addr>`; when the claimant has no claim yet, `+ claim of <claimant> on network <network>` and, when the connector was already on the network and no claim exists, ` ` `<name> is already on network <network>; detaching leaves it there` (operation space). The state hash is the hex SHA-256 of the JSON of the network id, the connector's endpoint id (empty when not attached), the target's id (empty without a target), the tunnel and the network's claims (claimant, change id, preexisting, ordered by claimant). `Guards` is the empty slice.
- Apply decides again with the same check, then: when the connector is not on the network, connects it (`ConnectNetwork(network id, container id, endpoint)`; with an address the endpoint carries `IPAMConfig` with `IPv4Address` for an IPv4 address and `IPv6Address` otherwise) as a Modify effect of `cloudflare.endpoint:<tunnel>|<network>` with the observed null state as before-state and a commit wait of 2 minutes; when the claimant has no claim on the network, inserts it as a Create effect of `cloudflare.claim:?<tunnel>|<network>|<claimant>` (local, inverse local, recovery name `<tunnel>|<network>|<claimant>`, `After` = `claimDocument(row)`, null before-state). The claim's `Preexisting` is true when the connector was already on the network and no claim existed, the first claim's value when claims exist, and the claimant's own claim's value when it has one. The output is `NetworkAttachment{Tunnel, Network, Claimant, Preexisting, Attached: true}`. A claimant that already holds the claim, with the connector on the network, makes no effect and no Engine call.
- The claim document: `{"tunnel":…,"network":…,"claimant":…,"preexisting":…,"changeId":…}` in that order.
- Journaling goes through Task 6's `c.journal`, `connectorEffect`, `resourceOf`, `nullState` and `named` (Contracts "Agent effects"): a 4xx Engine answer or a Definite error skips the effect and is returned Definite; any other failure leaves it intended.
- `networkClaims` implements the undo of every effect the attach and Task 13's detach journal: a claim Create is undone by deleting the claim only when its `change_id` is the effect's (absent is done; another change's claim is a Definite `request.invalid_input` refusal that keeps it); a claim Delete is undone by inserting the claim from the effect's `Before` document (the identical claim already there is done, a different claim is a Definite refusal, a document that does not describe the claim is a Definite refusal); an endpoint Modify without `InverseRecovery` (an attach's connect) is undone by `DisconnectNetwork`, refused (Definite `request.invalid_input`) while a claim of another change is left on the network, and an absent endpoint or container is done; an endpoint Modify with `InverseRecovery` set (a detach's disconnect) is undone by `ConnectNetwork(network, container name, nil)`, returning `InverseResult{Resource: <endpoint resource>}`. A 4xx Engine answer is a Definite refusal, an unreachable Engine is `docker.engine_unavailable`.
- `Observe` reads an endpoint from the Engine through `dstate.Endpoint(container name, network, endpoint, phase)` (null when the container or endpoint is absent; the recorded form unless the effect is a Create or is being or was undone, which use the restorable form) and a claim from `connectorClaims` (a row's `claimDocument`, or null); for a creation identity it reports `Matches` 0 or 1 and, when 1, the made `Resource`.
- `Verify` fails unless the network exists, the connector is on it and the claimant holds the claim.

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/cloudflare/connector_network_test.go`:

```go
package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	naTunnel   = "0a1b2c3d-4e5f-4a6b-8c7d-9e0f1a2b3c4d"
	naClaimant = "expose:app.example.com"
	naReason   = "connect the container to a user-defined network"
)

// newNA is an agent host with a running connector for naTunnel on the default bridge only, the networks web, other,
// bridge (the default one) and lan (a macvlan), and the container app on web.
func newNA(t *testing.T) *agHost {
	t.Helper()
	h := newAGHost(t)
	for _, n := range []dockerapi.Network{agNetwork("web", "bridge"), agNetwork("other", "bridge"), agNetwork("bridge", "bridge"), agNetwork("lan", "macvlan")} {
		h.docker.putNetwork(n)
	}
	conn := agContainer(connectorName(naTunnel), func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "running", true })
	h.docker.putContainer(conn)
	h.docker.putContainer(agContainer("app", agOnNetwork("web", "172.20.0.5")))
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(naTunnel), []byte(agToken(naTunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: naTunnel, ContainerID: conn.ID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// naOnNetwork puts the connector container on the network with a fixed address.
func naOnNetwork(t *testing.T, h *agHost, network, ip string) {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
	if err != nil {
		t.Fatal(err)
	}
	agOnNetwork(network, ip)(&c)
	h.docker.putContainer(c)
}

func naInput(mutate ...func(*NetworkAttachInput)) json.RawMessage {
	in := NetworkAttachInput{Tunnel: naTunnel, Network: "web", Claimant: naClaimant, Target: "app"}
	for _, m := range mutate {
		if m != nil {
			m(&in)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return raw
}

func naApply(t *testing.T, h *agHost, changeID string, mutate ...func(*NetworkAttachInput)) (NetworkAttachment, error) {
	t.Helper()
	raw, err := h.cf().networkAttach().Handler.Apply(context.Background(), h.env(changeID), naInput(mutate...))
	if err != nil {
		return NetworkAttachment{}, err
	}
	var out NetworkAttachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

func naClaimant2(in *NetworkAttachInput) { in.Claimant = "expose:b.example.com" }

func naClaims(t *testing.T, h *agHost) []claimRow {
	t.Helper()
	rows, err := connectorClaims(context.Background(), h.env("").DB.Read(), naTunnel, "")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func naOnWeb(t *testing.T, h *agHost) bool {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
	if err != nil {
		t.Fatal(err)
	}
	return c.NetworkSettings.Networks["web"] != nil
}

var naMutations = []string{"ConnectNetwork", "DisconnectNetwork", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "PullImage"}

func naMutated(h *agHost) []string {
	var got []string
	for _, c := range h.docker.calls() {
		if slices.Contains(naMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// naEffects are the journaled effects of one change.
func naEffects(t *testing.T, h *agHost, changeID string) []core.Effect {
	t.Helper()
	all, err := h.env(changeID).Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var effs []core.Effect
	for _, e := range all {
		if e.ChangeID == changeID {
			effs = append(effs, e)
		}
	}
	return effs
}

func naRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

// naUndo undoes the made effects of the change as the engine does: in reverse order, each checked against its
// journaled post-state before and against its undo target after.
func naUndo(t *testing.T, h *agHost, changeID string) {
	t.Helper()
	ctx := context.Background()
	def := h.cf().networkAttach()
	env := h.env(changeID)
	effs := naEffects(t, h, changeID)
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.State != core.EffectMade {
			continue
		}
		obs, err := def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe %s: %v", e.Resource, err)
		}
		if string(obs.Current.State) != string(e.Post.State) {
			t.Errorf("%s: observed %s before its undo, journaled %s", e.Resource, obs.Current.State, e.Post.State)
		}
		e.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, e); err != nil {
			t.Fatalf("inverse of %s: %v", e.Resource, err)
		}
		obs, err = def.Handler.Observe(ctx, env, e)
		if err != nil {
			t.Fatalf("observe %s after its undo: %v", e.Resource, err)
		}
		want := "null"
		if e.Kind != core.EffectCreate {
			want = string(e.Before.State)
		}
		if string(obs.Current.State) != want {
			t.Errorf("%s: observed %s after its undo, want %s", e.Resource, obs.Current.State, want)
		}
	}
}

func TestNaSpec(t *testing.T) {
	def := newNA(t).cf().networkAttach()
	s := def.Spec
	if s.Name != "cloudflare.network.attach" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	h := def.Handler
	if h.Plan == nil || h.Apply == nil || h.Verify == nil || h.Observe == nil || h.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			h.Plan != nil, h.Apply != nil, h.Verify != nil, h.Observe != nil, h.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[NetworkAttachInput]() || def.OutputType != reflect.TypeFor[NetworkAttachment]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
}

func TestNaClaimDocument(t *testing.T) {
	doc, err := claimDocument(claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: true})
	want := `{"tunnel":"` + naTunnel + `","network":"web","claimant":"` + naClaimant + `","preexisting":true,"changeId":"c1"}`
	if err != nil || string(doc) != want {
		t.Errorf("claimDocument = %s, %v; want %s", doc, err, want)
	}
}

func TestNaRefuses(t *testing.T) {
	ctx := context.Background()
	removeConnectorRow := func(t *testing.T, h *agHost) {
		env := h.env("")
		if err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, naTunnel) }); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name   string
		mutate func(*NetworkAttachInput)
		seed   func(t *testing.T, h *agHost)
		code   errcode.Code
		fields []string // the paths of the field errors, sorted
		detail string   // the exact detail, "" for any
	}{
		{name: "a bad tunnel id", mutate: func(in *NetworkAttachInput) { in.Tunnel = "x" }, code: errcode.InvalidInput, fields: []string{"/tunnel"}},
		{name: "a bad network name", mutate: func(in *NetworkAttachInput) { in.Network = "bad name" }, code: errcode.InvalidInput, fields: []string{"/network"}},
		{name: "a bad claimant", mutate: func(in *NetworkAttachInput) { in.Claimant = "a b" }, code: errcode.InvalidInput, fields: []string{"/claimant"}},
		{name: "a bad target name", mutate: func(in *NetworkAttachInput) { in.Target = "bad name" }, code: errcode.InvalidInput, fields: []string{"/target"}},
		{name: "every bad field", mutate: func(in *NetworkAttachInput) {
			in.Tunnel, in.Network, in.Claimant, in.Target = "x", "bad name", "a b", "bad name"
		},
			code: errcode.InvalidInput, fields: []string{"/claimant", "/network", "/target", "/tunnel"}},
		{name: "no connector row", seed: removeConnectorRow, code: errcode.CloudflareConnectorMissing},
		{name: "no connector container", code: errcode.CloudflareConnectorMissing,
			seed: func(t *testing.T, h *agHost) {
				if err := h.docker.RemoveContainer(ctx, connectorName(naTunnel), dockerapi.RemoveContainerOptions{Force: true}); err != nil {
					t.Fatal(err)
				}
			}},
		{name: "an unknown network", mutate: func(in *NetworkAttachInput) { in.Network = "nosuch" }, code: errcode.NotFound},
		{name: "the default bridge", mutate: func(in *NetworkAttachInput) { in.Network, in.Target = "bridge", "legacy" }, code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("legacy", agOnNetwork("bridge", "172.17.0.2")))
			}},
		{name: "a macvlan network without a target", mutate: func(in *NetworkAttachInput) { in.Network, in.Target = "lan", "" }, code: errcode.CloudflareUnsupportedTarget, detail: naReason},
		{name: "an unknown target", mutate: func(in *NetworkAttachInput) { in.Target = "ghost" }, code: errcode.NotFound},
		{name: "a target on another network only", code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			mutate: func(in *NetworkAttachInput) { in.Target = "elsewhere" },
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("elsewhere", agOnNetwork("other", "172.21.0.3")))
			}},
		{name: "a target on the default bridge only", code: errcode.CloudflareUnsupportedTarget, detail: naReason,
			mutate: func(in *NetworkAttachInput) { in.Target = "legacy" },
			seed: func(_ *testing.T, h *agHost) {
				h.docker.putContainer(agContainer("legacy", agOnNetwork("bridge", "172.17.0.2")))
			}},
		{name: "another fixed address", code: errcode.InvalidInput, fields: []string{"/address"},
			mutate: func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("172.20.0.10") },
			seed:   func(t *testing.T, h *agHost) { naOnNetwork(t, h, "web", "172.20.0.9") }},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newNA(t)
				if tc.seed != nil {
					tc.seed(t, h)
				}
				def := h.cf().networkAttach()
				var err error
				if stage == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), naInput(tc.mutate))
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), naInput(tc.mutate))
				}
				coded := naRefused(t, err, tc.code)
				var paths []string
				for _, f := range coded.Fields {
					paths = append(paths, f.Path)
				}
				slices.Sort(paths)
				if !slices.Equal(paths, tc.fields) {
					t.Errorf("field paths %v, want %v", paths, tc.fields)
				}
				if tc.detail != "" && coded.Detail != tc.detail {
					t.Errorf("detail %q, want %q", coded.Detail, tc.detail)
				}
				if got := naMutated(h); len(got) != 0 || len(naClaims(t, h)) != 0 {
					t.Errorf("the refusal changed the host: calls %v, claims %+v", got, naClaims(t, h))
				}
			})
		}
	}
	t.Run("the Engine down", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput())
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
}

func TestNaPlanShowsTheJoinAndTheClaim(t *testing.T) {
	ctx := context.Background()
	plan := func(h *agHost, mutate ...func(*NetworkAttachInput)) core.PlanOutcome {
		out, err := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput(mutate...))
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	join := core.DiffLine{Op: "+", Text: "container " + connectorName(naTunnel) + " joins network web"}
	claim := core.DiffLine{Op: "+", Text: "claim of " + naClaimant + " on network web"}

	h := newNA(t)
	out := plan(h)
	if want := []core.DiffLine{join, claim}; !slices.Equal(out.Diff, want) {
		t.Errorf("diff %+v, want %+v", out.Diff, want)
	}
	if len(out.StateHash) != 64 || out.StateHash != plan(h).StateHash || out.Guards == nil {
		t.Errorf("hash %q (stable %t), guards %v: want a 64-digit stable hash and an empty guard list", out.StateHash, out.StateHash == plan(h).StateHash, out.Guards)
	}
	if got := plan(h, func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("172.20.0.10") }).Diff; !slices.Equal(got, []core.DiffLine{join, {Op: "+", Text: "ipv4 172.20.0.10"}, claim}) {
		t.Errorf("diff with an IPv4 address %+v", got)
	}
	if got := plan(h, func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr("fd00::10") }).Diff; !slices.Equal(got, []core.DiffLine{join, {Op: "+", Text: "ipv6 fd00::10"}, claim}) {
		t.Errorf("diff with an IPv6 address %+v", got)
	}
	if got := naMutated(h); len(got) != 0 {
		t.Errorf("the plan changed the Engine: %v", got)
	}

	t.Run("the connector is already on the network", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		note := core.DiffLine{Op: " ", Text: connectorName(naTunnel) + " is already on network web; detaching leaves it there"}
		if got, want := plan(h).Diff, []core.DiffLine{claim, note}; !slices.Equal(got, want) {
			t.Errorf("diff %+v, want %+v", got, want)
		}
	})
	t.Run("a changed network changes the hash", func(t *testing.T) {
		h := newNA(t)
		before := plan(h).StateHash
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if after := plan(h, naClaimant2).StateHash; after == before {
			t.Error("the hash did not change after a claim and a join")
		}
	})
	t.Run("a claimant that already holds the claim", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if got := plan(h).Diff; len(got) != 0 {
			t.Errorf("diff %+v, want none", got)
		}
	})
}

func TestNaApplyJoinsAndClaims(t *testing.T) {
	ctx := context.Background()
	h := newNA(t)
	out, err := naApply(t, h, "c1")
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: naTunnel, Network: "web", Claimant: naClaimant, Preexisting: false, Attached: true}); out != want {
		t.Errorf("output %+v, want %+v", out, want)
	}
	if !naOnWeb(t, h) {
		t.Error("the connector did not join web")
	}
	wantClaim := claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: false}
	if got := naClaims(t, h); len(got) != 1 || got[0] != wantClaim {
		t.Errorf("claims %+v, want %+v", got, wantClaim)
	}
	if got := naMutated(h); !slices.Equal(got, []string{"ConnectNetwork"}) {
		t.Errorf("Engine calls %v, want one ConnectNetwork", got)
	}

	effs := naEffects(t, h, "c1")
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2: %+v", len(effs), effs)
	}
	join, claim := effs[0], effs[1]
	if join.Kind != core.EffectModify || join.Resource != "cloudflare.endpoint:"+naTunnel+"|web" || join.State != core.EffectMade || join.Local ||
		join.CommitWait != 2*time.Minute || string(join.Before.State) != "null" || join.Recovery != nil || join.InverseRecovery != nil || join.Post == nil {
		t.Errorf("join effect %+v", join)
	}
	id := naTunnel + "|web|" + naClaimant
	if claim.Kind != core.EffectCreate || claim.Resource != "cloudflare.claim:"+id || claim.State != core.EffectMade || !claim.Local || !claim.InverseLocal ||
		claim.Recovery == nil || claim.Recovery.Kind != "name" || claim.Recovery.Value != id || string(claim.Before.State) != "null" ||
		claim.After == nil || claim.Post == nil || string(claim.After.State) != string(claim.Post.State) {
		t.Errorf("claim effect %+v", claim)
	}
	if want, _ := claimDocument(wantClaim); string(claim.Post.State) != string(want) {
		t.Errorf("claim post-state %s, want %s", claim.Post.State, want)
	}

	if err := h.cf().networkAttach().Handler.Verify(ctx, h.env("c1"), naInput(), mustJSON(t, out)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestNaApplyWithAFixedAddress(t *testing.T) {
	for _, tc := range []struct {
		addr string
		got  func(*dockerapi.EndpointIPAMConfig) netip.Addr
	}{
		{"172.20.0.10", func(c *dockerapi.EndpointIPAMConfig) netip.Addr { return c.IPv4Address }},
		{"fd00::10", func(c *dockerapi.EndpointIPAMConfig) netip.Addr { return c.IPv6Address }},
	} {
		t.Run(tc.addr, func(t *testing.T) {
			h := newNA(t)
			want := netip.MustParseAddr(tc.addr)
			if _, err := naApply(t, h, "c1", func(in *NetworkAttachInput) { in.Address = want }); err != nil {
				t.Fatal(err)
			}
			c, err := h.docker.InspectContainer(context.Background(), connectorName(naTunnel))
			if err != nil {
				t.Fatal(err)
			}
			ep := c.NetworkSettings.Networks["web"]
			if ep == nil || ep.IPAMConfig == nil || tc.got(ep.IPAMConfig) != want {
				t.Errorf("endpoint %+v, want the fixed address %s", ep, want)
			}
		})
	}
}

func TestNaApplyClaimsThatShareANetwork(t *testing.T) {
	t.Run("the connector was on the network before", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		out, err := naApply(t, h, "c1")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want preexisting and attached", out)
		}
		if got := naMutated(h); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := naClaims(t, h); len(got) != 1 || !got[0].Preexisting {
			t.Errorf("claims %+v, want one preexisting claim", got)
		}
		if effs := naEffects(t, h, "c1"); len(effs) != 1 || effs[0].Resource != "cloudflare.claim:"+naTunnel+"|web|"+naClaimant {
			t.Errorf("effects %+v, want the claim only", effs)
		}
	})
	for _, preexisting := range []bool{false, true} {
		t.Run("a second claimant inherits the mark", func(t *testing.T) {
			h := newNA(t)
			if preexisting {
				naOnNetwork(t, h, "web", "172.20.0.9")
			}
			if _, err := naApply(t, h, "c1"); err != nil {
				t.Fatal(err)
			}
			out, err := naApply(t, h, "c2", naClaimant2)
			if err != nil {
				t.Fatal(err)
			}
			if out.Preexisting != preexisting {
				t.Errorf("preexisting %t, want %t", out.Preexisting, preexisting)
			}
			if got := naClaims(t, h); len(got) != 2 || got[0].Preexisting != preexisting || got[1].Preexisting != preexisting {
				t.Errorf("claims %+v", got)
			}
			if effs := naEffects(t, h, "c2"); len(effs) != 1 {
				t.Errorf("second change journaled %d effects, want only its claim", len(effs))
			}
		})
	}
	t.Run("the same claimant again", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		calls := len(h.docker.calls())
		out, err := naApply(t, h, "c2")
		if err != nil || !out.Attached {
			t.Fatalf("output %+v, %v", out, err)
		}
		if got := naClaims(t, h); len(got) != 1 || got[0].ChangeID != "c1" {
			t.Errorf("claims %+v, want the first change's claim only", got)
		}
		if effs := naEffects(t, h, "c2"); len(effs) != 0 {
			t.Errorf("effects %+v, want none", effs)
		}
		for _, c := range h.docker.calls()[calls:] {
			if slices.Contains(naMutations, c) {
				t.Errorf("the repeated attach called %s", c)
			}
		}
	})
}

func TestNaApplyFailures(t *testing.T) {
	t.Run("a refused connect is Definite and leaves no claim", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("ConnectNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "no"})
		_, err := naApply(t, h, "c1")
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a Definite refusal", err)
		}
		effs := naEffects(t, h, "c1")
		if len(effs) != 1 || effs[0].State != core.EffectSkipped || len(naClaims(t, h)) != 0 {
			t.Errorf("effects %+v, claims %+v; want one skipped effect and no claim", effs, naClaims(t, h))
		}
	})
	t.Run("an unknown connect failure stays intended", func(t *testing.T) {
		h := newNA(t)
		h.docker.Fail("ConnectNetwork", errors.New("timeout"))
		_, err := naApply(t, h, "c1")
		effs := naEffects(t, h, "c1")
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite and one intended effect", err, effs)
		}
	})
}

func TestNaVerify(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		damage func(t *testing.T, h *agHost)
		fails  bool
	}{
		{"as attached", func(*testing.T, *agHost) {}, false},
		{"the connector left the network", func(t *testing.T, h *agHost) {
			if err := h.docker.DisconnectNetwork(ctx, "web", connectorName(naTunnel), false); err != nil {
				t.Fatal(err)
			}
		}, true},
		{"the claim is gone", func(t *testing.T, h *agHost) {
			env := h.env("")
			err := env.DB.Write(ctx, func(tx *sql.Tx) error { return deleteClaim(ctx, tx, naTunnel, "web", naClaimant) })
			if err != nil {
				t.Fatal(err)
			}
		}, true},
		{"the network is gone", func(t *testing.T, h *agHost) {
			h.docker.Fail("InspectNetwork", &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"})
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNA(t)
			out, err := naApply(t, h, "c1")
			if err != nil {
				t.Fatal(err)
			}
			tc.damage(t, h)
			err = h.cf().networkAttach().Handler.Verify(ctx, h.env("c1"), naInput(), mustJSON(t, out))
			if tc.fails != (err != nil) {
				t.Errorf("Verify error %v, want failure %t", err, tc.fails)
			}
		})
	}
}

func TestNaUndo(t *testing.T) {
	ctx := context.Background()
	t.Run("a join and a claim", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		if naOnWeb(t, h) || len(naClaims(t, h)) != 0 {
			t.Errorf("after the undo the connector is on web: %t, claims %+v", naOnWeb(t, h), naClaims(t, h))
		}
	})
	t.Run("a claim on a network the connector was already on", func(t *testing.T) {
		h := newNA(t)
		naOnNetwork(t, h, "web", "172.20.0.9")
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		if !naOnWeb(t, h) || len(naClaims(t, h)) != 0 {
			t.Errorf("after the undo the connector is on web: %t, claims %+v; want it kept on web with no claim", naOnWeb(t, h), naClaims(t, h))
		}
	})
	t.Run("a claim of another change keeps the connector on the network", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		if _, err := naApply(t, h, "c2", naClaimant2); err != nil {
			t.Fatal(err)
		}
		def := h.cf().networkAttach()
		env := h.env("c1")
		effs := naEffects(t, h, "c1")
		claim, join := effs[1], effs[0]
		claim.State = core.EffectUndoing
		if _, err := def.Handler.Inverse(ctx, env, claim); err != nil {
			t.Fatalf("inverse of the claim: %v", err)
		}
		join.State = core.EffectUndoing
		_, err := def.Handler.Inverse(ctx, env, join)
		naRefused(t, err, errcode.InvalidInput)
		if !naOnWeb(t, h) {
			t.Error("the connector left a network another change still claims")
		}
		if got := naClaims(t, h); len(got) != 1 || got[0].ChangeID != "c2" {
			t.Errorf("claims %+v, want only the second change's", got)
		}
	})
	t.Run("a claim of another change is not dropped", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		claim := naEffects(t, h, "c1")[1]
		claim.ChangeID, claim.State = "c9", core.EffectUndoing
		_, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c1"), claim)
		naRefused(t, err, errcode.InvalidInput)
		if len(naClaims(t, h)) != 1 {
			t.Error("the claim of another change was dropped")
		}
	})
	t.Run("an undo run again", func(t *testing.T) {
		h := newNA(t)
		if _, err := naApply(t, h, "c1"); err != nil {
			t.Fatal(err)
		}
		naUndo(t, h, "c1")
		effs := naEffects(t, h, "c1")
		for i := len(effs) - 1; i >= 0; i-- {
			e := effs[i]
			e.State = core.EffectUndoing
			if _, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c1"), e); err != nil {
				t.Errorf("inverse of %s again: %v", e.Resource, err)
			}
		}
	})
}

// TestNaInverseOfWhatADetachJournals: the detach action of a later task embeds networkClaims, so its undo of a
// removed claim and of a disconnect is pinned here with effects built the way that action journals them.
func TestNaInverseOfWhatADetachJournals(t *testing.T) {
	ctx := context.Background()
	claimID := naTunnel + "|web|" + naClaimant
	row := claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1", Preexisting: true}

	t.Run("a removed claim comes back", func(t *testing.T) {
		h := newNA(t)
		doc, err := claimDocument(row)
		if err != nil {
			t.Fatal(err)
		}
		e := core.Effect{ChangeID: "c9", Kind: core.EffectDelete, Resource: "cloudflare.claim:" + claimID, State: core.EffectUndoing, Before: core.StateDoc{State: doc}}
		def := h.cf().networkAttach()
		if _, err := def.Handler.Inverse(ctx, h.env("c9"), e); err != nil {
			t.Fatal(err)
		}
		if got := naClaims(t, h); len(got) != 1 || got[0] != row {
			t.Errorf("claims %+v, want %+v", got, row)
		}
		if _, err := def.Handler.Inverse(ctx, h.env("c9"), e); err != nil {
			t.Errorf("restoring the same claim again: %v", err)
		}
		other := row
		other.ChangeID = "c2"
		otherDoc, _ := claimDocument(other)
		e.Before = core.StateDoc{State: otherDoc}
		_, err = def.Handler.Inverse(ctx, h.env("c9"), e)
		naRefused(t, err, errcode.InvalidInput)
		e.Before = core.StateDoc{State: json.RawMessage(`{"tunnel":"x"}`)}
		_, err = def.Handler.Inverse(ctx, h.env("c9"), e)
		naRefused(t, err, errcode.InvalidInput)
	})

	t.Run("a left network is joined again", func(t *testing.T) {
		h := newNA(t)
		e := core.Effect{ChangeID: "c9", Kind: core.EffectModify, Resource: "cloudflare.endpoint:" + naTunnel + "|web", State: core.EffectUndoing,
			InverseRecovery: &core.RecoveryID{Kind: "name", Value: connectorName(naTunnel) + "@web"}}
		res, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), e)
		if err != nil {
			t.Fatal(err)
		}
		if res.Resource != e.Resource || !naOnWeb(t, h) {
			t.Errorf("result %+v, on web %t; want the endpoint resource and a joined network", res, naOnWeb(t, h))
		}
		_, err = h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), e)
		if err == nil || !core.IsDefinite(err) {
			t.Errorf("joining a network the connector is on: %v, want a Definite refusal", err)
		}
	})

	t.Run("an effect of another module is refused", func(t *testing.T) {
		h := newNA(t)
		_, err := h.cf().networkAttach().Handler.Inverse(ctx, h.env("c9"), core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.volume:x"})
		if err == nil {
			t.Error("an unknown resource was undone")
		}
	})
}

func TestNaObserveCreations(t *testing.T) {
	ctx := context.Background()
	h := newNA(t)
	def := h.cf().networkAttach()
	id := naTunnel + "|web|" + naClaimant
	claim := core.Effect{Kind: core.EffectCreate, Resource: "cloudflare.claim:?" + id}
	obs, err := def.Handler.Observe(ctx, h.env("c1"), claim)
	if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
		t.Errorf("before the attach: matches %d state %s (%v), want none and null", obs.Matches, obs.Current.State, err)
	}
	if _, err := naApply(t, h, "c1"); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, h.env("c1"), claim)
	want, _ := claimDocument(claimRow{TunnelID: naTunnel, Network: "web", Claimant: naClaimant, ChangeID: "c1"})
	if err != nil || obs.Matches != 1 || obs.Resource != "cloudflare.claim:"+id || string(obs.Current.State) != string(want) || !strings.Contains(string(obs.Current.State), `"changeId":"c1"`) {
		t.Errorf("after the attach: matches %d resource %q state %s (%v)", obs.Matches, obs.Resource, obs.Current.State, err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/8 && export TMPDIR=/root/.cache/w6b-tmp/8 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestNa' ./internal/modules/cloudflare/
```

Expected: a build failure naming `h.cf().networkAttach undefined` (and then `claimDocument`). Quote it in the report.

- [ ] **Step 3: Implement**

Create `connector_network.go` with the behaviour of the decisions above, structured as `internal/modules/npm/network_claims.go` is (read it and its `claims`/`addClaim`/`dropClaim` store helpers; the cloudflare store has `connectorClaims`, `insertClaim`, `deleteClaim` with the tunnel in the key):

1. `func (c cf) networkAttach() core.ActionDef` returns `core.DefineMutating[NetworkAttachInput, NetworkAttachment]` with `Name: ID + ".network.attach"`, a one-sentence `Summary`, `Version: 1`, `Class: core.ClassCompensable`, `Side: core.SideAgent`, `LockKeys: []string{"cloudflare"}` and a `networkAttachHandler` that embeds `networkClaims{c}`.
2. A check function, used by Plan and Apply, that makes every decision and changes nothing, every failure Definite except a database or Engine read failure (those are returned wrapped, an unreachable Engine through `engineCoded`, and are Definite only as npm's `networkEngine` makes them, since the Engine changed nothing). It returns the network, the connector container, the connector's endpoint on the network (`NetworkSettings.Networks[network.Name]`, nil when absent), the target's id, the network's claims, and whether to connect, whether to add a claim and the claim's `Preexisting`.
3. The supported-target rule as a small function of the network and the optional target container, returning the `cloudflare.unsupported_target` error with the detail `connect the container to a user-defined network`.
4. Plan, Apply and Verify as the decisions describe. Apply journals through a helper modelled on npm's `run` (intend, call, skip on a refusal, otherwise made with the observed state), using the effect shapes in the decisions; the endpoint effect's `Before` is the state `Observe` reports before the connect.
5. `type claimDoc` and `claimDocument` with the JSON of the decisions; `networkClaims` with `Observe` and `Inverse`; the identity helpers that split `<tunnel>|<network>|<claimant>` (at most three parts, so a claimant may hold `|`) and `<tunnel>|<network>`.
6. Do not register the action in `Manifest()` (Task 14 does). No message, diff line or effect carries a token; this action never reads one.

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/8 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestNa' ./internal/modules/cloudflare/
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/8 && go clean -testcache
```

Expected: all pass, `0 issues.`, no output from vet and tidy. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/connector_network.go internal/modules/cloudflare/connector_network_test.go
git commit -m "feat(cloudflare): add network.attach"
```

### Task 9: cloudflare: connector.deploy composite

**Files:**
- Create: `internal/modules/cloudflare/connector_deploy.go`
- Test: `internal/modules/cloudflare/connector_deploy_test.go`

**Interfaces:**
- Consumes: Contracts "Wire types" (`ConnectorDeployInput`, `ConnectorDeployOutput`, `ConnectorRunInput`, `Connector`; Task 3) and "Controller composites" (`cloudflare.connector.deploy`). From the module as it stands: `TunnelCreateInput`, `TunnelInput`, `TunnelRef` and `TunnelToken` (`wire.go`); `loadTunnel` (`store.go`); `cf.tunnelCreate` (`tunnel_lifecycle.go`) and `cf.tunnelToken` (`tunnel_read.go`), which the test validates the step inputs against; the controller test host `newCFHost`/`cfHost` (`host_test.go`) with `cfWrite` and `cfT0` (`store_test.go`). The pattern is `exposeContainerComposer` in `internal/modules/npm/composite_deploy_expose.go` and `nodeJoinComposer` in `internal/modules/tailnet/join.go` (a step that reaches a secret only through a reference to an earlier step's output).
- Produces: `func (c cf) connectorDeploy() core.ActionDef` (Contracts "Registration"; Task 14 appends it to `actions()`). The task also declares `connectorDeployComposer` and `deployStepInput`, and no other top-level identifier.

**Items:** 34

**Depends on:** Task 3

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/modules/cloudflare/connector_deploy_test.go`. The tests read the composite the way the engine does, through `ActionDef.Handler.Steps` and `Handler.Output` with the host's controller database, and resolve each step template with a small resolver that follows the engine's reference rules (a string that is exactly one reference takes the referenced value's JSON type; a reference inside a longer string is replaced by the value's text; a substituted value is never read as a template again).

```go
package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

const (
	cdHost       = "h7"
	cdTunnelID   = "5c1d2f3a-0b4e-4c6d-8e7f-9a0b1c2d3e4f"
	cdTunnelName = "darkpanel-01JD0000000000000000000001"
	cdSecret     = "cd-token-secret-value"
)

// cdSeedTunnel stores the managed tunnel a named deployment resolves.
func cdSeedTunnel(t *testing.T, h *cfHost) {
	t.Helper()
	cfWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		return insertTunnel(ctx, tx, tunnelRow{ID: cdTunnelID, Name: cdTunnelName, ChangeID: "01JD0000000000000000000001", CreatedAt: cfT0})
	})
}

// cdJSON is v as the JSON a step output or an input holds.
func cdJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// cdSteps asks the composite for its steps the way the engine does, through the erased handler, with the host's
// controller database.
func cdSteps(t *testing.T, h *cfHost, in ConnectorDeployInput) ([]core.Step, error) {
	t.Helper()
	return cf{}.connectorDeploy().Handler.Steps(context.Background(), h.env(""), cdJSON(t, in))
}

// cdGeneric is v as decoded JSON.
func cdGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(cdJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var cdRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// cdResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a substituted value is never read as a
// template again. A reference to a member that does not exist fails the test, as it fails the plan.
func cdResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": cdGeneric(t, v)}
	}
	root := map[string]any{"input": cdGeneric(t, in), "steps": steps}
	lookup := func(ref string) any {
		var cur any = root
		for _, p := range strings.Split(ref, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
			if cur, ok = m[p]; !ok {
				t.Errorf("reference ${%s}: there is no member %q", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := cdRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return cdRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	var tv any
	if err := json.Unmarshal(tmpl, &tv); err != nil {
		t.Fatalf("template %s: %v", tmpl, err)
	}
	return cdJSON(t, walk(tv))
}

// cdStrict decodes raw into a T and refuses a member T does not have.
func cdStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// cdWantStep fails the test unless s is the step id on host running action after the steps after.
func cdWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func cdIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// cdRefs counts the step references in the template of s.
func cdRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

// cdRefused fails the test unless err is a Definite refusal with the code.
func cdRefused(t *testing.T, err error, code errcode.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want a refusal with code %s", code)
	}
	if got := errcode.From(err).Code; got != code {
		t.Errorf("error %v has code %s, want %s", err, got, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
}

// TestCdDeclaration: the deployment is a controller-side composite that takes the module's coordination lock and
// unwinds in reverse.
func TestCdDeclaration(t *testing.T) {
	def := cf{}.connectorDeploy()
	s := def.Spec
	if s.Name != "cloudflare.connector.deploy" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite ||
		s.Side != core.SideController || s.Unwind != core.UnwindReverse || !slices.Equal(s.LockKeys, []string{"composite:cloudflare"}) {
		t.Errorf("spec %+v, want version 1, a summary, a controller-side composite, unwind reverse, lock composite:cloudflare", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestCdStepsCreateTunnel: without a named tunnel the deployment creates one, reads its token and runs the
// connector on the host. The token reaches the run only as a reference to the token step's output.
func TestCdStepsCreateTunnel(t *testing.T) {
	h := newCFHost(t)
	in := ConnectorDeployInput{HostID: cdHost}
	steps, err := cdSteps(t, h, in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"tunnel", "token", "run"}; !slices.Equal(cdIDs(steps), want) {
		t.Fatalf("steps %v, want %v", cdIDs(steps), want)
	}
	cdWantStep(t, steps[0], "tunnel", "", "cloudflare.tunnel.create/v1", nil)
	cdWantStep(t, steps[1], "token", "", "cloudflare.tunnel.token/v1", []string{"tunnel"})
	cdWantStep(t, steps[2], "run", cdHost, "cloudflare.connector.run/v1", []string{"token"})

	outputs := map[string]any{"tunnel": TunnelRef{ID: cdTunnelID, Name: cdTunnelName}, "token": TunnelToken{Token: cdSecret}}
	create := cdResolve(t, steps[0].Input, in, outputs)
	cdStrict[TunnelCreateInput](t, create)
	if err := schema.NewValidator().ValidateInput(cf{}.tunnelCreate(), create); err != nil {
		t.Errorf("tunnel input %s is invalid for cloudflare.tunnel.create: %v", create, err)
	}
	token := cdResolve(t, steps[1].Input, in, outputs)
	if got := cdStrict[TunnelInput](t, token); got != (TunnelInput{Tunnel: cdTunnelID}) {
		t.Errorf("token input %+v, want the created tunnel's id", got)
	}
	if err := schema.NewValidator().ValidateInput(cf{}.tunnelToken(), token); err != nil {
		t.Errorf("token input %s is invalid for cloudflare.tunnel.token: %v", token, err)
	}
	if got := cdStrict[ConnectorRunInput](t, cdResolve(t, steps[2].Input, in, outputs)); got != (ConnectorRunInput{Tunnel: cdTunnelID, Token: cdSecret}) {
		t.Errorf("run input %+v, want the created tunnel's id and the token the token step revealed", got)
	}
	if got := cdStrict[ConnectorRunInput](t, steps[2].Input); got.Tunnel != "${steps.tunnel.output.id}" || got.Token != "${steps.token.output.token}" {
		t.Errorf("run template %+v, want references to the tunnel's id and the token step's token", got)
	}
	if cdRefs(steps[0]) != 0 || cdRefs(steps[1]) != 1 || cdRefs(steps[2]) != 2 {
		t.Errorf("step references: tunnel %d, token %d, run %d; want 0, 1 and 2", cdRefs(steps[0]), cdRefs(steps[1]), cdRefs(steps[2]))
	}
}

// TestCdStepsNamedTunnel: a named tunnel, by id or by name, is resolved to its id when the steps are made, so the
// deployment has no tunnel step and every template holds the id, never the typed name.
func TestCdStepsNamedTunnel(t *testing.T) {
	for _, named := range []string{cdTunnelID, cdTunnelName} {
		t.Run(named, func(t *testing.T) {
			h := newCFHost(t)
			cdSeedTunnel(t, h)
			in := ConnectorDeployInput{HostID: cdHost, Tunnel: named}
			steps, err := cdSteps(t, h, in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"token", "run"}; !slices.Equal(cdIDs(steps), want) {
				t.Fatalf("steps %v, want %v", cdIDs(steps), want)
			}
			cdWantStep(t, steps[0], "token", "", "cloudflare.tunnel.token/v1", nil)
			cdWantStep(t, steps[1], "run", cdHost, "cloudflare.connector.run/v1", []string{"token"})
			outputs := map[string]any{"token": TunnelToken{Token: cdSecret}}
			token := cdResolve(t, steps[0].Input, in, outputs)
			if got := cdStrict[TunnelInput](t, token); got != (TunnelInput{Tunnel: cdTunnelID}) {
				t.Errorf("token input %+v, want the tunnel's id", got)
			}
			if err := schema.NewValidator().ValidateInput(cf{}.tunnelToken(), token); err != nil {
				t.Errorf("token input %s is invalid for cloudflare.tunnel.token: %v", token, err)
			}
			if got := cdStrict[ConnectorRunInput](t, cdResolve(t, steps[1].Input, in, outputs)); got != (ConnectorRunInput{Tunnel: cdTunnelID, Token: cdSecret}) {
				t.Errorf("run input %+v, want the tunnel's id and the revealed token", got)
			}
			if got := cdStrict[ConnectorRunInput](t, steps[1].Input); got.Tunnel != cdTunnelID || got.Token != "${steps.token.output.token}" {
				t.Errorf("run template %+v, want the literal id and a reference to the token", got)
			}
			if cdRefs(steps[0]) != 0 || cdRefs(steps[1]) != 1 {
				t.Errorf("step references: token %d, run %d; want 0 and 1", cdRefs(steps[0]), cdRefs(steps[1]))
			}
			if strings.Contains(string(steps[0].Input)+string(steps[1].Input), cdTunnelName) {
				t.Errorf("the templates %s and %s hold the tunnel's name, want its id", steps[0].Input, steps[1].Input)
			}
		})
	}
}

// TestCdStepsUnknownTunnel: a tunnel Darkpanel does not manage is refused before any step runs, and what the
// operator typed is not read as a reference.
func TestCdStepsUnknownTunnel(t *testing.T) {
	h := newCFHost(t)
	cdSeedTunnel(t, h)
	for _, typed := range []string{"missing", "${steps.token.output.token}", "5c1d2f3a-0b4e-4c6d-8e7f-000000000000"} {
		_, err := cdSteps(t, h, ConnectorDeployInput{HostID: cdHost, Tunnel: typed})
		cdRefused(t, err, errcode.NotFound)
	}
	_, err := cdSteps(t, newCFHost(t), ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID})
	cdRefused(t, err, errcode.NotFound)
}

func TestCdOutput(t *testing.T) {
	conn := Connector{Tunnel: cdTunnelID, Container: "darkpanel-cloudflared-" + cdTunnelID, ContainerID: "c0ffee", State: "running"}
	tests := []struct {
		name  string
		in    ConnectorDeployInput
		steps map[string]json.RawMessage
		want  ConnectorDeployOutput
	}{
		{
			"created", ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": cdJSON(t, TunnelRef{ID: cdTunnelID, Name: cdTunnelName}), "run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Created: true, Connector: conn},
		},
		{
			"named by id", ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{"run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Connector: conn},
		},
		{
			"named by name", ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelName},
			map[string]json.RawMessage{"run": cdJSON(t, conn)},
			ConnectorDeployOutput{Tunnel: cdTunnelID, Connector: conn},
		},
	}
	def := cf{}.connectorDeploy()
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			cdSeedTunnel(t, h)
			out, err := def.Handler.Output(context.Background(), h.env(""), cdJSON(t, tc.in), tc.steps)
			if err != nil {
				t.Fatal(err)
			}
			if got := cdStrict[ConnectorDeployOutput](t, out); got != tc.want {
				t.Errorf("output %+v, want %+v", got, tc.want)
			}
			if bytes.Contains(out, []byte("token")) {
				t.Errorf("output %s mentions a token", out)
			}
		})
	}

	failures := []struct {
		name  string
		seed  bool
		in    ConnectorDeployInput
		steps map[string]json.RawMessage
		want  string
	}{
		{"created without the run step", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": cdJSON(t, TunnelRef{ID: cdTunnelID})}, "run"},
		{"created without the tunnel step", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"run": cdJSON(t, conn)}, "tunnel"},
		{"created with a tunnel step that holds no id", true, ConnectorDeployInput{HostID: cdHost},
			map[string]json.RawMessage{"tunnel": json.RawMessage(`{}`), "run": cdJSON(t, conn)}, "tunnel"},
		{"named without the run step", true, ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{}, "run"},
		{"named tunnel no longer stored", false, ConnectorDeployInput{HostID: cdHost, Tunnel: cdTunnelID},
			map[string]json.RawMessage{"run": cdJSON(t, conn)}, cdTunnelID},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			if tc.seed {
				cdSeedTunnel(t, h)
			}
			_, err := def.Handler.Output(context.Background(), h.env(""), cdJSON(t, tc.in), tc.steps)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %v, want one naming %q", err, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w6b-tmp/9 && export TMPDIR=/root/.cache/w6b-tmp/9 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestCd' ./internal/modules/cloudflare/
```

Expected: FAIL to build, with `cf{}.connectorDeploy undefined (type cf has no field or method connectorDeploy)`. Quote that output in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/cloudflare/connector_deploy.go`.

1. `func (c cf) connectorDeploy() core.ActionDef` returns `core.DefineComposite[ConnectorDeployInput, ConnectorDeployOutput]` with `core.ActionSpec{Name: ID + ".connector.deploy", Version: 1, Unwind: core.UnwindReverse, Summary: <one sentence: run a tunnel's connector on a host, creating the tunnel first when none is named>}` and the handler `connectorDeployComposer{}`. Set no `Side`, `Class` or `LockKeys`: `DefineComposite` makes the action a controller-side composite under `composite:cloudflare`, which `TestCdDeclaration` checks. Follow the layout of `exposeContainer` in the npm file named above, doc comments included.
2. `deployStepInput(members map[string]any) (json.RawMessage, error)` marshals a map into a step template and wraps a marshal error as `cloudflare: %w`.
3. `connectorDeployComposer.Steps(ctx, env, in)` builds the steps of Contracts "Controller composites", `cloudflare.connector.deploy`, exactly:
   - `in.Tunnel == ""`: step `tunnel` (no host, `cloudflare.tunnel.create`, version 1, input `{}`), step `token` (no host, `cloudflare.tunnel.token`, version 1, input `{"tunnel":"${steps.tunnel.output.id}"}`, after `tunnel`), step `run` (host `in.HostID`, `cloudflare.connector.run`, version 1, input `{"tunnel":"${steps.tunnel.output.id}","token":"${steps.token.output.token}"}`, after `token`).
   - `in.Tunnel != ""`: resolve it with `loadTunnel(ctx, env.DB.Read(), in.Tunnel)`. A read error is returned unchanged. A tunnel that is not found is `core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))`, the text `cloudflare.tunnel.token` uses. Otherwise the steps are `token` (no host, input `{"tunnel":"<row.ID>"}`, no `After`) and `run` (host `in.HostID`, input `{"tunnel":"<row.ID>","token":"${steps.token.output.token}"}`, after `token`), with the row's id written literally.
   - The token reaches `run` only as the reference `${steps.token.output.token}`: the engine refuses a literal in the secret field and stores a template in clear. The text the operator typed for the tunnel never enters a template, because the id comes from the row. gosec flags the reference string as G101: annotate the line `//nolint:gosec // G101: a reference to the token step's output, not a credential`.
4. `connectorDeployComposer.Output(ctx, env, in, steps)` decodes the `run` step's output as a `Connector`; a missing `run` output or one that does not decode is an error that names the `run` step (`cloudflare: the deployment has no output of its run step`). With `in.Tunnel == ""` it sets `Created` to true and takes the tunnel id from the `tunnel` step's output (a `TunnelRef`); a missing step or an output without an id is an error that names the `tunnel` step. With a named tunnel it sets `Created` to false and re-resolves `in.Tunnel` with `loadTunnel`; a tunnel that is no longer stored is an error that names the typed tunnel. The `token` step's output is never in `steps` (a read step) and never in the output: `ConnectorDeployOutput` has no token member.
5. Do not edit `cloudflare.go`; Task 14 registers the action.

- [ ] **Step 4: Run the test to verify it passes**

```bash
export TMPDIR=/root/.cache/w6b-tmp/9 GOTOOLCHAIN=local
timeout 600 go test -count=1 -v -run 'TestCd' ./internal/modules/cloudflare/
```

Expected: PASS for `TestCdDeclaration`, `TestCdStepsCreateTunnel`, `TestCdStepsNamedTunnel`, `TestCdStepsUnknownTunnel` and `TestCdOutput`.

- [ ] **Step 5: Verify the task and commit**

```bash
mkdir -p /root/.cache/w6b-tmp/9 && export TMPDIR=/root/.cache/w6b-tmp/9 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/cloudflare/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing, every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output.

```bash
rm -rf /root/.cache/w6b-tmp/9 && GOTOOLCHAIN=local go clean -testcache
```

```bash
git add internal/modules/cloudflare/connector_deploy.go internal/modules/cloudflare/connector_deploy_test.go
git commit -m "feat(cloudflare): add connector.deploy composite"
```

### Task 10: cloudflare: expose.container and expose.remove composites

**Files:**
- Create: `internal/modules/cloudflare/expose.go`
- Test: `internal/modules/cloudflare/expose_test.go`

**Interfaces:**
- Consumes: Contracts "Wire types" (`ExposeInput`, `ExposeOutput`, `ExposeRemoveInput`, `ExposeRemoveOutput`, `NetworkAttachInput`, `NetworkAttachment`, `NetworkDetachInput`; Task 3), "Names" (`networkNamePattern`, Task 3) and "Controller composites" (`cloudflare.expose.container`, `cloudflare.expose.remove`, the claimant). From the module as it stands: `loadTunnel` and `loadHostname` (`store.go`), `hostnameInvalid(path, detail)` (`hostname_add.go`, the Definite `request.invalid_input` refusal with one field), `HostnameAddInput`, `HostnameInput` and `HostnameView` (`wire.go`), `cf.hostnameAdd` and `cf.hostnameRemove`, which the test validates the step inputs against; `newCFHost`/`cfHost` (`host_test.go`) with `cfWrite`, `cfT0` and `cfHostname` (`store_test.go`). The pattern is `exposeContainerComposer` and `exposeRemoveComposer` in `internal/modules/npm/composite_deploy_expose.go`.
- Produces: `func (c cf) exposeContainer() core.ActionDef` and `func (c cf) exposeRemove() core.ActionDef` (Contracts "Registration"; Task 14 appends them to `actions()`), and `const exposeClaimantPrefix = "expose:"` (Contracts "Controller composites"). The task also declares `exposeContainerComposer`, `exposeRemoveComposer`, `exposeStepInput` and `exposeStepOutput`, and no other top-level identifier.

**Items:** 35

**Depends on:** Task 3

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/modules/cloudflare/expose_test.go`. As in Task 9, the tests call `Handler.Steps` and `Handler.Output` with the host's controller database and resolve the templates with a resolver that follows the engine's reference rules. The step inputs are decoded strictly into the child's wire type, and the two inputs of actions that exist today are also validated against the real action.

```go
package cloudflare

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

const (
	exHost       = "h7"
	exTunnelID   = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
	exTunnelName = "darkpanel-01JD0000000000000000000002"
)

// exSeed stores the managed tunnel and an active hostname row for each name.
func exSeed(t *testing.T, h *cfHost, hostnames ...string) {
	t.Helper()
	cfWrite(t, h.db, func(ctx context.Context, tx *sql.Tx) error {
		err := insertTunnel(ctx, tx, tunnelRow{ID: exTunnelID, Name: exTunnelName, ChangeID: "01JD0000000000000000000002", CreatedAt: cfT0})
		if err != nil {
			return err
		}
		for _, name := range hostnames {
			if err := insertHostname(ctx, tx, cfHostname(name, exTunnelID, "active")); err != nil {
				return err
			}
		}
		return nil
	})
}

// exBase is a valid exposure input; a test changes the member it examines.
func exBase() ExposeInput {
	return ExposeInput{HostID: exHost, Tunnel: exTunnelID, Container: "web", Port: 8080, Network: "appnet", Hostname: "app.example.com"}
}

// exJSON is v as the JSON a step output or an input holds.
func exJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// exSteps asks def for its steps the way the engine does, through the erased handler, with the host's controller
// database.
func exSteps(t *testing.T, h *cfHost, def core.ActionDef, in any) ([]core.Step, error) {
	t.Helper()
	return def.Handler.Steps(context.Background(), h.env(""), exJSON(t, in))
}

// exGeneric is v as decoded JSON.
func exGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(exJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var exRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// exResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text, and a substituted value is never read as a
// template again. A reference to a member that does not exist fails the test, as it fails the plan.
func exResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": exGeneric(t, v)}
	}
	root := map[string]any{"input": exGeneric(t, in), "steps": steps}
	lookup := func(ref string) any {
		var cur any = root
		for _, p := range strings.Split(ref, ".") {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
			if cur, ok = m[p]; !ok {
				t.Errorf("reference ${%s}: there is no member %q", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := exRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return exRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	var tv any
	if err := json.Unmarshal(tmpl, &tv); err != nil {
		t.Fatalf("template %s: %v", tmpl, err)
	}
	return exJSON(t, walk(tv))
}

// exStrict decodes raw into a T and refuses a member T does not have.
func exStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// exWantStep fails the test unless s is the step id on host running action after the steps after.
func exWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func exIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// exRefs counts the step references in the template of s.
func exRefs(s core.Step) int { return strings.Count(string(s.Input), "${steps.") }

// exRefused fails the test unless err is a Definite refusal with the code, and answers it.
func exRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	if err == nil {
		t.Fatalf("no error, want a refusal with code %s", code)
	}
	coded := errcode.From(err)
	if coded.Code != code {
		t.Errorf("error %v has code %s, want %s", err, coded.Code, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// TestExDeclarations: both are controller-side composites that take the module's coordination lock; the exposure
// unwinds in reverse, the removal forward.
func TestExDeclarations(t *testing.T) {
	tests := []struct {
		name   string
		def    core.ActionDef
		unwind core.Unwind
	}{
		{"cloudflare.expose.container", cf{}.exposeContainer(), core.UnwindReverse},
		{"cloudflare.expose.remove", cf{}.exposeRemove(), core.UnwindForward},
	}
	for _, tc := range tests {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
			s.Unwind != tc.unwind || !slices.Equal(s.LockKeys, []string{"composite:cloudflare"}) {
			t.Errorf("%s: spec %+v, want version 1, a summary, a controller-side composite, unwind %v, lock composite:cloudflare", tc.name, s, tc.unwind)
		}
		if tc.def.Handler.Steps == nil || tc.def.Handler.Output == nil {
			t.Errorf("%s: the handler lacks Steps or Output", tc.name)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
	if got := exposeClaimantPrefix; got != "expose:" {
		t.Errorf("claimant prefix %q, want expose:", got)
	}
}

// TestExExposeContainerSteps: the connector is attached to the target's network first, under the claim of the
// hostname, and the hostname is added after it with the service http://<container>:<port>. The tunnel is named by
// its id in both steps, whether the input gave its id or its name.
func TestExExposeContainerSteps(t *testing.T) {
	tests := []struct {
		name        string
		in          ExposeInput
		wantService string
	}{
		{"tunnel by id", exBase(), "http://web:8080"},
		{"tunnel by name", ExposeInput{HostID: exHost, Tunnel: exTunnelName, Container: "api.v2_x", Port: 3000, Network: "backend", Hostname: "api.example.com"}, "http://api.v2_x:3000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			exSeed(t, h)
			steps, err := exSteps(t, h, cf{}.exposeContainer(), tc.in)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"network", "hostname"}; !slices.Equal(exIDs(steps), want) {
				t.Fatalf("steps %v, want %v", exIDs(steps), want)
			}
			exWantStep(t, steps[0], "network", exHost, "cloudflare.network.attach/v1", nil)
			exWantStep(t, steps[1], "hostname", "", "cloudflare.hostname.add/v1", []string{"network"})

			attach := exStrict[NetworkAttachInput](t, exResolve(t, steps[0].Input, tc.in, nil))
			wantAttach := NetworkAttachInput{Tunnel: exTunnelID, Network: tc.in.Network, Claimant: "expose:" + tc.in.Hostname, Target: tc.in.Container}
			if attach != wantAttach {
				t.Errorf("attach input %+v, want %+v", attach, wantAttach)
			}
			add := exResolve(t, steps[1].Input, tc.in, nil)
			wantAdd := HostnameAddInput{Tunnel: exTunnelID, Hostname: tc.in.Hostname, Service: tc.wantService}
			if got := exStrict[HostnameAddInput](t, add); got != wantAdd {
				t.Errorf("hostname input %+v, want %+v", got, wantAdd)
			}
			if err := schema.NewValidator().ValidateInput(cf{}.hostnameAdd(), add); err != nil {
				t.Errorf("hostname input %s is invalid for cloudflare.hostname.add: %v", add, err)
			}
			if exRefs(steps[0]) != 0 || exRefs(steps[1]) != 0 {
				t.Errorf("the templates %s and %s hold step references, want references to the input only", steps[0].Input, steps[1].Input)
			}
			if strings.Contains(string(steps[0].Input)+string(steps[1].Input), exTunnelName) {
				t.Errorf("the templates hold the tunnel's name, want its id")
			}
		})
	}
}

// TestExExposeContainerKeepsTypedTextOutOfTheTemplate: the engine reads every "${" of a template as a reference, so
// the hostname the operator typed reaches the steps only through references to the input, and the container name,
// which is written into the service literally, cannot hold one.
func TestExExposeContainerKeepsTypedTextOutOfTheTemplate(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h)
	in := exBase()
	in.Hostname = "${steps.network.output.network}.example.com"
	steps, err := exSteps(t, h, cf{}.exposeContainer(), in)
	if err != nil {
		t.Fatal(err)
	}
	outputs := map[string]any{"network": NetworkAttachment{Network: "other"}}
	if got := exStrict[NetworkAttachInput](t, exResolve(t, steps[0].Input, in, outputs)).Claimant; got != "expose:"+in.Hostname {
		t.Errorf("claimant %q, want the typed hostname unchanged", got)
	}
	if got := exStrict[HostnameAddInput](t, exResolve(t, steps[1].Input, in, outputs)).Hostname; got != in.Hostname {
		t.Errorf("hostname %q, want the typed hostname unchanged", got)
	}

	in = exBase()
	in.Container = "web${input.port}"
	_, err = exSteps(t, h, cf{}.exposeContainer(), in)
	if coded := exRefused(t, err, errcode.InvalidInput); len(coded.Fields) == 0 || coded.Fields[0].Path != "/container" {
		t.Errorf("fields %+v, want a refusal of /container", coded.Fields)
	}
}

// TestExExposeContainerRefusals: an input the steps cannot be made from is refused before any step runs, naming
// the member, and a tunnel Darkpanel does not manage is not found.
func TestExExposeContainerRefusals(t *testing.T) {
	with := func(change func(in *ExposeInput)) ExposeInput {
		in := exBase()
		change(&in)
		return in
	}
	tests := []struct {
		name string
		in   ExposeInput
		code errcode.Code
		path string
	}{
		{"empty container", with(func(in *ExposeInput) { in.Container = "" }), errcode.InvalidInput, "/container"},
		{"container with a space", with(func(in *ExposeInput) { in.Container = "web app" }), errcode.InvalidInput, "/container"},
		{"container starting with a hyphen", with(func(in *ExposeInput) { in.Container = "-web" }), errcode.InvalidInput, "/container"},
		{"container with a slash", with(func(in *ExposeInput) { in.Container = "a/b" }), errcode.InvalidInput, "/container"},
		{"container too long", with(func(in *ExposeInput) { in.Container = strings.Repeat("a", 129) }), errcode.InvalidInput, "/container"},
		{"empty network", with(func(in *ExposeInput) { in.Network = "" }), errcode.InvalidInput, "/network"},
		{"network with a separator", with(func(in *ExposeInput) { in.Network = "app|net" }), errcode.InvalidInput, "/network"},
		{"port 0", with(func(in *ExposeInput) { in.Port = 0 }), errcode.InvalidInput, "/port"},
		{"negative port", with(func(in *ExposeInput) { in.Port = -1 }), errcode.InvalidInput, "/port"},
		{"port 65536", with(func(in *ExposeInput) { in.Port = 65536 }), errcode.InvalidInput, "/port"},
		{"empty hostname", with(func(in *ExposeInput) { in.Hostname = "" }), errcode.InvalidInput, "/hostname"},
		{"unknown tunnel", with(func(in *ExposeInput) { in.Tunnel = "missing" }), errcode.NotFound, ""},
		{"no tunnel", with(func(in *ExposeInput) { in.Tunnel = "" }), errcode.NotFound, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := newCFHost(t)
			exSeed(t, h)
			_, err := exSteps(t, h, cf{}.exposeContainer(), tc.in)
			coded := exRefused(t, err, tc.code)
			if tc.path != "" && (len(coded.Fields) == 0 || coded.Fields[0].Path != tc.path) {
				t.Errorf("fields %+v, want a refusal of %s", coded.Fields, tc.path)
			}
		})
	}
	// The boundary values are accepted.
	for _, port := range []int{1, 65535} {
		h := newCFHost(t)
		exSeed(t, h)
		if _, err := exSteps(t, h, cf{}.exposeContainer(), with(func(in *ExposeInput) { in.Port = port })); err != nil {
			t.Errorf("port %d: %v", port, err)
		}
	}
}

func TestExExposeContainerOutput(t *testing.T) {
	def := cf{}.exposeContainer()
	in := exJSON(t, exBase())
	steps := map[string]json.RawMessage{
		"network":  exJSON(t, NetworkAttachment{Tunnel: exTunnelID, Network: "appnet", Claimant: "expose:app.example.com", Attached: true}),
		"hostname": exJSON(t, HostnameView{Hostname: "app.example.com", Tunnel: exTunnelID, Service: "http://web:8080", Phase: "active", Health: "ok"}),
	}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	want := ExposeOutput{Hostname: "app.example.com", Service: "http://web:8080", Network: "appnet"}
	if got := exStrict[ExposeOutput](t, out); got != want {
		t.Errorf("output %+v, want %+v", got, want)
	}
	for _, missing := range []string{"network", "hostname"} {
		partial := map[string]json.RawMessage{}
		for k, v := range steps {
			if k != missing {
				partial[k] = v
			}
		}
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}

// TestExExposeRemoveSteps: the hostname goes first, then every claim of the exposure on the tunnel's connector,
// whatever network it was made on. The tunnel is the one the hostname row names.
func TestExExposeRemoveSteps(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h, "app.example.com")
	in := ExposeRemoveInput{HostID: exHost, Hostname: "app.example.com"}
	steps, err := exSteps(t, h, cf{}.exposeRemove(), in)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"hostname", "network"}; !slices.Equal(exIDs(steps), want) {
		t.Fatalf("steps %v, want %v", exIDs(steps), want)
	}
	exWantStep(t, steps[0], "hostname", "", "cloudflare.hostname.remove/v1", nil)
	exWantStep(t, steps[1], "network", exHost, "cloudflare.network.detach/v1", []string{"hostname"})

	remove := exResolve(t, steps[0].Input, in, nil)
	if got := exStrict[HostnameInput](t, remove); got != (HostnameInput{Hostname: "app.example.com"}) {
		t.Errorf("remove input %+v, want the hostname", got)
	}
	if err := schema.NewValidator().ValidateInput(cf{}.hostnameRemove(), remove); err != nil {
		t.Errorf("remove input %s is invalid for cloudflare.hostname.remove: %v", remove, err)
	}
	detach := exStrict[NetworkDetachInput](t, exResolve(t, steps[1].Input, in, nil))
	if want := (NetworkDetachInput{Tunnel: exTunnelID, Network: "", Claimant: "expose:app.example.com"}); detach != want {
		t.Errorf("detach input %+v, want %+v: every claim of the exposure on the hostname's tunnel", detach, want)
	}
	if exRefs(steps[0]) != 0 || exRefs(steps[1]) != 0 {
		t.Errorf("the templates %s and %s hold step references, want references to the input only", steps[0].Input, steps[1].Input)
	}
}

// TestExExposeRemoveUnknownHostname: a hostname Darkpanel does not manage is refused before any step runs.
func TestExExposeRemoveUnknownHostname(t *testing.T) {
	h := newCFHost(t)
	exSeed(t, h, "app.example.com")
	for _, typed := range []string{"other.example.com", "", "${steps.hostname.output.hostname}"} {
		_, err := exSteps(t, h, cf{}.exposeRemove(), ExposeRemoveInput{HostID: exHost, Hostname: typed})
		_ = exRefused(t, err, errcode.NotFound)
	}
}

func TestExExposeRemoveOutput(t *testing.T) {
	def := cf{}.exposeRemove()
	in := exJSON(t, ExposeRemoveInput{HostID: exHost, Hostname: "app.example.com"})
	steps := map[string]json.RawMessage{
		"hostname": exJSON(t, HostnameView{Hostname: "app.example.com", Phase: "removed"}),
		"network":  exJSON(t, NetworkAttachment{Tunnel: exTunnelID, Claimant: "expose:app.example.com"}),
	}
	out, err := def.Handler.Output(context.Background(), core.Env{}, in, steps)
	if err != nil {
		t.Fatal(err)
	}
	if got := exStrict[ExposeRemoveOutput](t, out); got != (ExposeRemoveOutput{Hostname: "app.example.com"}) {
		t.Errorf("output %+v, want the hostname", got)
	}
	for _, missing := range []string{"hostname", "network"} {
		partial := map[string]json.RawMessage{}
		for k, v := range steps {
			if k != missing {
				partial[k] = v
			}
		}
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w6b-tmp/10 && export TMPDIR=/root/.cache/w6b-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestEx' ./internal/modules/cloudflare/
```

Expected: FAIL to build, with `cf{}.exposeContainer undefined (type cf has no field or method exposeContainer)` (and `exposeRemove`, `exposeClaimantPrefix`). Quote that output in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/cloudflare/expose.go`.

1. `const exposeClaimantPrefix = "expose:"`. A claim's claimant is this prefix followed by the hostname.
2. `func (c cf) exposeContainer() core.ActionDef`: `core.DefineComposite[ExposeInput, ExposeOutput]` with `Name: ID + ".expose.container"`, `Version: 1`, `Unwind: core.UnwindReverse`, a one-sentence `Summary` and the handler `exposeContainerComposer{}`. `func (c cf) exposeRemove() core.ActionDef`: `core.DefineComposite[ExposeRemoveInput, ExposeRemoveOutput]` with `Name: ID + ".expose.remove"`, `Version: 1`, `Unwind: core.UnwindForward`, a `Summary` and `exposeRemoveComposer{}`. Set no `Side`, `Class` or `LockKeys` (`composite:cloudflare` comes from `DefineComposite`).
3. `exposeStepInput(members map[string]any) (json.RawMessage, error)` marshals a map into a step template and wraps a marshal error as `cloudflare: %w`. `exposeStepOutput(steps map[string]json.RawMessage, id string, into any) error` decodes the output of step `id`; a step with no output, or an output that does not decode, is an error that names the step.
4. `exposeContainerComposer.Steps(ctx, env, in)` checks, in this order, and refuses each failure with `hostnameInvalid(<path>, <detail>)` (a Definite `request.invalid_input` that names the member): `Container` against `networkNamePattern` (`/container`), `Network` against `networkNamePattern` (`/network`), `Port` from 1 to 65535 (`/port`), `Hostname` not empty (`/hostname`). The pattern keeps `$`, white space and `|` out of both names, so the container name can be written into the service literally without making a reference. Then it resolves `in.Tunnel` with `loadTunnel(ctx, env.DB.Read(), in.Tunnel)`: a read error is returned unchanged, and a tunnel that is not found, an empty `Tunnel` included, is `core.Definite(errcode.New(errcode.NotFound, "no managed tunnel "+in.Tunnel))`. The steps are those of Contracts "Controller composites", `cloudflare.expose.container`, with the resolved row's id written literally:
   - `network`: host `in.HostID`, `cloudflare.network.attach`, version 1, no `After`, input members `tunnel` (the id), `network` (`${input.network}`), `claimant` (`exposeClaimantPrefix + "${input.hostname}"`), `target` (`${input.container}`).
   - `hostname`: no host, `cloudflare.hostname.add`, version 1, `After: ["network"]`, input members `tunnel` (the id), `hostname` (`${input.hostname}`), `service` (`"http://" + in.Container + ":" + strconv.Itoa(in.Port)`, written literally).
   - Every operator-typed value that can hold `${` (the hostname) reaches a template only as a reference to the input; the engine reads every `${` of a template as a reference.
5. `exposeContainerComposer.Output` decodes the `network` output as a `NetworkAttachment` and the `hostname` output as a `HostnameView` through `exposeStepOutput` (the `network` step first), and answers `ExposeOutput{Hostname: <hostname output>.Hostname, Service: <hostname output>.Service, Network: <network output>.Network}`.
6. `exposeRemoveComposer.Steps(ctx, env, in)` reads the row with `loadHostname(ctx, env.DB.Read(), in.Hostname)`: a read error is returned unchanged and an absent row is `core.Definite(errcode.New(errcode.NotFound, "no managed hostname "+in.Hostname))`. The steps are those of Contracts "Controller composites", `cloudflare.expose.remove`:
   - `hostname`: no host, `cloudflare.hostname.remove`, version 1, no `After`, input `{"hostname":"${input.hostname}"}`.
   - `network`: host `in.HostID`, `cloudflare.network.detach`, version 1, `After: ["hostname"]`, input members `tunnel` (the row's `TunnelID`, literally), `network` (`""`), `claimant` (`exposeClaimantPrefix + "${input.hostname}"`).
7. `exposeRemoveComposer.Output` answers `ExposeRemoveOutput{Hostname: in.Hostname}` once both the `hostname` and the `network` outputs are present; a missing one is an error that names the step.
8. Do not edit `cloudflare.go`; Task 14 registers the actions.

- [ ] **Step 4: Run the test to verify it passes**

```bash
export TMPDIR=/root/.cache/w6b-tmp/10 GOTOOLCHAIN=local
timeout 600 go test -count=1 -v -run 'TestEx' ./internal/modules/cloudflare/
```

Expected: PASS for `TestExDeclarations`, `TestExExposeContainerSteps`, `TestExExposeContainerKeepsTypedTextOutOfTheTemplate`, `TestExExposeContainerRefusals`, `TestExExposeContainerOutput`, `TestExExposeRemoveSteps`, `TestExExposeRemoveUnknownHostname` and `TestExExposeRemoveOutput`.

- [ ] **Step 5: Verify the task and commit**

```bash
mkdir -p /root/.cache/w6b-tmp/10 && export TMPDIR=/root/.cache/w6b-tmp/10 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/cloudflare/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing, every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output.

```bash
rm -rf /root/.cache/w6b-tmp/10 && GOTOOLCHAIN=local go clean -testcache
```

```bash
git add internal/modules/cloudflare/expose.go internal/modules/cloudflare/expose_test.go
git commit -m "feat(cloudflare): add expose composites"
```

### Task 11: web: Expose via tunnel container action

**Files:**
- Modify: `web/src/modules/cloudflare/container-actions.tsx` (today the whole file is `export function ExposeAction() { return null }`)
- Create: `web/src/modules/cloudflare/expose-dialog.tsx`
- Test: `web/src/modules/cloudflare/expose-dialog.test.tsx`
- Test: `web/src/modules/cloudflare/container-actions.test.tsx`

**Interfaces:**
- Consumes: Contracts "Web": Task 2's `web/src/modules/cloudflare/connector-types.ts` (`Connector`, `ConnectorList`, `connectorListKey(hostId)`, `tunnelLabel(id, tunnels)`), and the wire JSON of `cloudflare.connector.list` (read on the host) and `cloudflare.expose.container` (`ExposeInput`). From the code as it stands: `CLOUDFLARE_ACTION_VERSION`, `cloudflareKey` and `TunnelView` in `web/src/modules/cloudflare/types.ts`; `CloudflareChange` in `web/src/modules/cloudflare/cloudflare-change.tsx` (plans and applies one controller action in a dialog and, however it closes, calls `onClose` and refreshes the `["cloudflare"]` queries); `ContainerRef` in `web/src/modules/types.ts`; `ContainerDetail`, `DockerNetwork`, `DOCKER_ACTION_VERSION`, `DOCKER_MODULE` and `dockerKey` in `web/src/modules/docker/types.ts`; the fixtures `dkDetail` and `dkNetwork` in `web/src/modules/docker/fixtures.ts` and `cfTunnel` in `web/src/modules/cloudflare/fixtures.ts`. The pattern is `web/src/modules/npm/container-actions.tsx` and its test. `web/src/modules/registry.ts` already registers `ExposeAction` in the slot `cloudflare.expose`; it does not change, and neither does `CloudflareChange`.
- Produces: `ExposeAction({ container, onDone }: { container: ContainerRef; onDone: () => void })`, the registered slot's component, and, in `web/src/modules/cloudflare/expose-dialog.tsx`, `ExposeTunnelForm` with its exported props interface `ExposeTunnelFormProps { container: ContainerRef; tunnels: { id: string; name: string }[]; endpoints: string[]; networks: DockerNetwork[]; ports: number[]; onDone: () => void }`. `tunnels` are the tunnels offered, `endpoints` the names of the networks the container is attached to, `networks` the host's networks and `ports` the container's private ports.

**Items:** 35

**Depends on:** Task 2

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 1 = 5

- [ ] **Step 1: Write the failing tests**

Create `web/src/modules/cloudflare/expose-dialog.test.tsx`. It renders `ExposeTunnelForm` with props and stubs only the plan request of `cloudflare.expose.container`.

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test, vi } from "vitest"
import { axe } from "vitest-axe"
import { dkNetwork } from "@/modules/docker/fixtures"
import type { DockerNetwork } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeTunnelForm } from "./expose-dialog"

const ED_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"
const ED_UNSUPPORTED =
  "This container is not on a user-defined bridge network, so the Cloudflare connector cannot reach it by name: connect the container to a user-defined network."

const edContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }
const edTunnels = [
  { id: "t1", name: "darkpanel-a" },
  { id: "t2", name: "darkpanel-b" },
]

function edPlan(call: FetchCall) {
  return {
    body: {
      planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h",
      diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
    },
  }
}

function edNetworks(): DockerNetwork[] {
  return [
    dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }),
    dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" }),
    dkNetwork({ id: "id-alpha", name: "alpha", driver: "bridge" }),
    dkNetwork({ id: "id-mesh", name: "mesh", driver: "overlay" }),
    dkNetwork({ id: "id-host", name: "host", driver: "host" }),
  ]
}

interface EdOptions {
  container?: ContainerRef
  endpoints?: string[]
  routes?: Record<string, StubHandler>
  onDone?: () => void
}

function edRender(options: EdOptions = {}) {
  const calls = stubFetch({ [ED_PLAN]: edPlan, ...options.routes })
  renderWithProviders(
    <ExposeTunnelForm
      container={options.container ?? edContainer}
      tunnels={edTunnels}
      endpoints={options.endpoints ?? ["bridge", "backend", "alpha", "mesh"]}
      networks={edNetworks()}
      ports={[9000, 80]}
      onDone={options.onDone ?? (() => {})}
    />,
  )
  return calls
}

function edForm(): HTMLElement {
  return screen.getByRole("form", { name: "Expose container via tunnel" })
}

function edPlanBodies(calls: FetchCall[]): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith("/plan")).map((c) => c.body)
}

describe("ExposeTunnelForm", () => {
  test("offers the tunnels and only the user-defined bridge networks, selects the first of each and defaults to the lowest port", async () => {
    const user = userEvent.setup()
    const calls = edRender()
    const form = edForm()
    const tunnels = within(form).getByRole("radiogroup", { name: "Tunnel" })
    expect(within(tunnels).getAllByRole("radio")).toHaveLength(2)
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-a", checked: true })).toBeTruthy()
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-b", checked: false })).toBeTruthy()
    const networks = within(form).getByRole("radiogroup", { name: "Network" })
    expect(within(networks).getAllByRole("radio")).toHaveLength(2)
    expect(within(networks).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
    expect(within(networks).getByRole("radio", { name: "backend", checked: false })).toBeTruthy()
    for (const hidden of ["bridge", "mesh", "host"]) expect(within(networks).queryByRole("radio", { name: hidden })).toBeNull()
    expect((within(form).getByLabelText("Port") as HTMLInputElement).value).toBe("80")
    const plan = within(form).getByRole("button", { name: "Plan" })
    expect(plan.hasAttribute("disabled")).toBe(true)
    await user.type(within(form).getByLabelText("Hostname"), " App.Example.com ")
    expect(plan.hasAttribute("disabled")).toBe(false)
    await user.click(plan)
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(calls.find((c) => c.path.endsWith("/plan"))?.path).toBe("/api/v1/actions/cloudflare.expose.container/v1/plan")
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "alpha", hostname: "app.example.com" } },
    ])
  })

  test("another tunnel, network and port reach the input", async () => {
    const user = userEvent.setup()
    const calls = edRender()
    const form = edForm()
    await user.click(within(form).getByRole("radio", { name: "darkpanel-b" }))
    await user.click(within(form).getByRole("radio", { name: "backend" }))
    const port = within(form).getByLabelText("Port")
    await user.clear(port)
    await user.type(port, "9000")
    await user.type(within(form).getByLabelText("Hostname"), "api.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    await screen.findByRole("dialog", { name: "Expose web at api.example.com" })
    await waitFor(() => expect(edPlanBodies(calls)).toHaveLength(1))
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t2", container: "web", port: 9000, network: "backend", hostname: "api.example.com" } },
    ])
  })

  test("the container name is sent without Docker's leading slash", async () => {
    const user = userEvent.setup()
    const calls = edRender({ container: { ...edContainer, name: "/web" } })
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((edPlanBodies(calls)[0] as { input: { container: string } }).input.container).toBe("web")
  })

  test.each([
    ["Port", "0"],
    ["Port", "65536"],
    ["Port", ""],
    ["Port", "http"],
    ["Hostname", ""],
  ])("%s %j keeps Plan off", async (label, typed) => {
    edRender()
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(false)
    const field = within(form).getByLabelText(label)
    await user.clear(field)
    if (typed !== "") await user.type(field, typed)
    expect(within(form).getByRole("button", { name: "Plan" }).hasAttribute("disabled")).toBe(true)
  })

  test("a container with no user-defined network is warned about and the controller's refusal in the preview is shown", async () => {
    const calls = edRender({
      endpoints: ["mesh", "bridge"],
      routes: {
        [ED_PLAN]: {
          status: 422,
          body: {
            type: "about:blank", title: "Unsupported tunnel target", status: 422,
            detail: "web is not on a user-defined bridge network: connect the container to a user-defined network", code: "cloudflare.unsupported_target",
          },
        },
      },
    })
    const user = userEvent.setup()
    const form = edForm()
    expect(within(form).getByText(ED_UNSUPPORTED)).toBeTruthy()
    expect(within(form).queryByRole("radiogroup", { name: "Network" })).toBeNull()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((await within(dialog).findByRole("alert")).textContent).toBe(
      "web is not on a user-defined bridge network: connect the container to a user-defined network",
    )
    expect(edPlanBodies(calls)).toEqual([
      { input: { hostId: "h1", tunnel: "t1", container: "web", port: 80, network: "bridge", hostname: "app.example.com" } },
    ])
  })

  test("the controller's refusal of a tunnel without a connector on the host is shown", async () => {
    edRender({
      routes: {
        [ED_PLAN]: {
          status: 409,
          body: {
            type: "about:blank", title: "No connector for the tunnel on this host", status: 409,
            detail: "tunnel t1 has no connector on this host", code: "cloudflare.connector_missing",
          },
        },
      },
    })
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    expect((await within(dialog).findByRole("alert")).textContent).toBe("tunnel t1 has no connector on this host")
  })

  test("closing the change dialog calls onDone", async () => {
    const onDone = vi.fn()
    edRender({ onDone })
    const user = userEvent.setup()
    const form = edForm()
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(onDone).not.toHaveBeenCalled()
    await user.keyboard("{Escape}")
    await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull())
    expect(onDone).toHaveBeenCalledTimes(1)
  })

  test("the form has no accessibility violations", async () => {
    edRender()
    expect(await axe(edForm())).toHaveNoViolations()
  })
})
```

Create `web/src/modules/cloudflare/container-actions.test.tsx`. It renders `ExposeAction` and stubs the four reads it makes (the tunnel list, the host's connector list, the container's inspect and the host's networks) and the plan request.

```tsx
import { screen, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { describe, expect, test } from "vitest"
import { dkDetail, dkNetwork } from "@/modules/docker/fixtures"
import type { ContainerDetail } from "@/modules/docker/types"
import type { ContainerRef } from "@/modules/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { ExposeAction } from "./container-actions"
import type { Connector } from "./connector-types"
import { cfTunnel } from "./fixtures"

const CT_HOST = "/api/v1/hosts/h1"
const CT_TUNNELS = "POST /api/v1/actions/cloudflare.tunnel.list/v1/read"
const CT_CONNECTORS = `POST ${CT_HOST}/actions/cloudflare.connector.list/v1/read`
const CT_INSPECT = `POST ${CT_HOST}/actions/docker.container.inspect/v1/read`
const CT_NETWORKS = `GET ${CT_HOST}/resources/docker/networks`
const CT_PLAN = "POST /api/v1/actions/cloudflare.expose.container/v1/plan"

const ctContainer: ContainerRef = { hostId: "h1", containerId: "c1", name: "web", ports: [] }

function ctTunnels() {
  return [
    cfTunnel({ id: "t1", name: "darkpanel-a" }),
    cfTunnel({ id: "t2", name: "darkpanel-b" }),
    cfTunnel({ id: "t3", name: "darkpanel-c" }),
  ]
}

function ctConnector(tunnel: string, state: string): Connector {
  return { tunnel, container: "darkpanel-cloudflared-" + tunnel, containerId: "id-" + tunnel, state }
}

function ctDetail(): ContainerDetail {
  return dkDetail({
    id: "c1",
    ports: [{ privatePort: 9000, proto: "tcp" }, { privatePort: 80, proto: "tcp" }],
    networks: ["bridge", "backend", "alpha"].map((network) => ({ network, networkId: "id-" + network, container: "web" })),
  })
}

function ctRender(routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [CT_TUNNELS]: { body: { output: { tunnels: ctTunnels() } } },
    [CT_CONNECTORS]: { body: { output: { connectors: [ctConnector("t2", "running"), ctConnector("t3", "missing")] } } },
    [CT_INSPECT]: { body: { output: ctDetail() } },
    [CT_NETWORKS]: {
      body: {
        networks: [
          dkNetwork({ id: "id-bridge", name: "bridge", driver: "bridge" }),
          dkNetwork({ id: "id-backend", name: "backend", driver: "bridge" }),
          dkNetwork({ id: "id-alpha", name: "alpha", driver: "bridge" }),
        ],
      },
    },
    [CT_PLAN]: (call: FetchCall) => ({
      body: {
        planId: "p1", hostId: null, action: "cloudflare.expose.container", version: 1, inputHash: "h",
        diff: [{ op: "~", text: String(call.path) }], guards: [], expiresAt: "2026-11-03T13:00:00Z", confirmation: null,
      },
    }),
    ...routes,
  })
  const result = renderWithProviders(<ExposeAction container={ctContainer} onDone={() => {}} />)
  return { calls, ...result }
}

function ctProblem(status: number, detail: string, code: string) {
  return { status, body: { type: "about:blank", title: "Problem", status, detail, code } }
}

describe("ExposeAction", () => {
  test("offers only the tunnels whose connector runs on this host and plans the exposure with the chosen one", async () => {
    const { calls } = ctRender()
    const user = userEvent.setup()
    const form = await screen.findByRole("form", { name: "Expose container via tunnel" })
    const tunnels = within(form).getByRole("radiogroup", { name: "Tunnel" })
    expect(within(tunnels).getAllByRole("radio")).toHaveLength(1)
    expect(within(tunnels).getByRole("radio", { name: "darkpanel-b", checked: true })).toBeTruthy()
    expect(within(form).getByRole("radio", { name: "alpha", checked: true })).toBeTruthy()
    expect((within(form).getByLabelText("Port") as HTMLInputElement).value).toBe("80")
    await user.type(within(form).getByLabelText("Hostname"), "app.example.com")
    await user.click(within(form).getByRole("button", { name: "Plan" }))
    const dialog = await screen.findByRole("dialog", { name: "Expose web at app.example.com" })
    await within(dialog).findByLabelText("Planned changes")
    expect(calls.filter((c) => c.path.endsWith("/plan")).map((c) => c.body)).toEqual([
      { input: { hostId: "h1", tunnel: "t2", container: "web", port: 80, network: "alpha", hostname: "app.example.com" } },
    ])
    expect(calls.some((c) => c.method === "POST" && c.path === `${CT_HOST}/actions/cloudflare.connector.list/v1/read`)).toBe(true)
  })

  test("says so when no tunnel exists and points to the tunnels page", async () => {
    ctRender({ [CT_TUNNELS]: { body: { output: { tunnels: [] } } } })
    expect((await screen.findByText("No Cloudflare tunnel exists yet. Create a tunnel first.")).textContent).toBe(
      "No Cloudflare tunnel exists yet. Create a tunnel first.",
    )
    expect(screen.getByRole("link", { name: "Open the tunnels page" }).getAttribute("href")).toBe("/cloudflare/tunnels")
    expect(screen.queryByRole("form")).toBeNull()
  })

  test.each([
    ["the host runs no connector", []],
    ["the only connector's container is missing", [ctConnector("t1", "missing")]],
    ["the host runs the connector of a tunnel that is not listed", [ctConnector("t9", "running")]],
  ])("says so when %s and points to the connectors page", async (_name, connectors) => {
    ctRender({ [CT_CONNECTORS]: { body: { output: { connectors } } } })
    expect((await screen.findByText("No Cloudflare connector runs on this host. Deploy one first.")).textContent).toBe(
      "No Cloudflare connector runs on this host. Deploy one first.",
    )
    expect(screen.getByRole("link", { name: "Open the connectors page" }).getAttribute("href")).toBe("/cloudflare/connectors")
    expect(screen.queryByRole("form")).toBeNull()
  })

  test.each([
    ["the tunnels", CT_TUNNELS, "the Cloudflare account is not connected", "cloud.not_connected"],
    ["the connectors", CT_CONNECTORS, "the Cloudflare module is unavailable", "module.unavailable"],
    ["the container", CT_INSPECT, "the Docker daemon is not reachable", "docker.engine_unavailable"],
  ])("shows the reason when %s cannot be read", async (_name, route, detail, code) => {
    ctRender({ [route]: ctProblem(503, detail, code) })
    expect((await screen.findByRole("alert")).textContent).toBe(detail)
    expect(screen.queryByRole("form")).toBeNull()
  })
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/11 && export TMPDIR=/root/.cache/w6b-tmp/11 GOTOOLCHAIN=local
timeout 600 pnpm -C web exec vitest run src/modules/cloudflare/expose-dialog.test.tsx src/modules/cloudflare/container-actions.test.tsx
```

Expected: `expose-dialog.test.tsx` FAILS with `Failed to resolve import "./expose-dialog"`; `container-actions.test.tsx` FAILS in every test, because `ExposeAction` renders nothing (`Unable to find role="form"` or `Unable to find an element with the text`). Quote that output in the report.

- [ ] **Step 3: Implement**

1. Create `web/src/modules/cloudflare/expose-dialog.tsx` exporting `ExposeTunnelForm` and `ExposeTunnelFormProps`. It renders a `<form aria-label="Expose container via tunnel">` followed by `CloudflareChange` for `action="cloudflare.expose.container"`, laid out like `ExposeForm` in the npm file. Use `useId` for the element ids, the darkraise-ui `Alert`, `Button`, `Input`, `Label` and `RadioGroup` components the npm and hostnames pages use, and no import from `web/src/modules/npm`: keep a local port parser (a whole number from 1 to 65535 spelled in digits, else null) and a local network filter.
   - Container name: `container.name` without a leading `/`.
   - Tunnel: a `RadioGroup` with `aria-label="Tunnel"`, one radio per entry of `tunnels` in the given order, labelled with `tunnelLabel(t.id, tunnels)`. The selected tunnel is the picked one when it is still in `tunnels`, else the first.
   - Network: the supported networks are the entries of `endpoints` whose network in `networks` has driver `bridge` and a name other than `bridge`, sorted by name. With at least one, a `RadioGroup` with `aria-label="Network"` lists them and the picked one, else the first, is selected. With none, show a warning `Alert` (`variant="warning"`, `role="status"`) with exactly this text and no network radio group: `This container is not on a user-defined bridge network, so the Cloudflare connector cannot reach it by name: connect the container to a user-defined network.` The plan still goes to the controller, so its own refusal shows in the preview; the network sent is then the first of `[...endpoints].sort()`, or `""` when there is none.
   - Port: a text field labelled `Port`, initially the lowest of `ports` (empty when there is none), `aria-invalid` when the text is not empty and not a valid port. Hostname: a text field labelled `Hostname`; the value used is the text trimmed and lower-cased.
   - A submit button named `Plan`, disabled until a tunnel is selected, the port is valid and the hostname is not empty. Submitting sets the pending change to the title `Expose <name> at <hostname>` and the input `{ hostId: container.hostId, tunnel: <selected tunnel id>, container: <name>, port, network, hostname }`, and nothing else. `CloudflareChange` receives the title and the input (null while nothing is pending), and its `onClose` clears the pending change and then calls `onDone`.
2. Replace the content of `web/src/modules/cloudflare/container-actions.tsx` with `ExposeAction`. It runs four TanStack queries, all with `useQuery`:
   - the tunnels: key `cloudflareKey("tunnels")`, `readAction<{ tunnels: TunnelView[] }>("cloudflare.tunnel.list", CLOUDFLARE_ACTION_VERSION, {})`;
   - the host's connectors: key `connectorListKey(hostId)`, `readAction<ConnectorList>("cloudflare.connector.list", CLOUDFLARE_ACTION_VERSION, {}, hostId)`;
   - the container: key `["resource", hostId, "docker.container", containerId]`, `readAction<ContainerDetail>("docker.container.inspect", DOCKER_ACTION_VERSION, { container: containerId }, hostId)`;
   - the host's networks: key `dockerKey(hostId, "networks")`, `readResource<{ networks: DockerNetwork[] }>(hostId, DOCKER_MODULE, "networks")`.
   While any is pending, render `<Spinner aria-label="Loading the tunnels and the container's networks" />`. When any failed, render a destructive `Alert` (`role="alert"`) holding `errorMessage` of the first error in the order tunnels, connectors, container, networks. When there is no tunnel, render the paragraph `No Cloudflare tunnel exists yet. Create a tunnel first.` and, as a separate `Link` from `react-router`, `Open the tunnels page` to `/cloudflare/tunnels`. A tunnel is offered when a connector of the host's list names it and that connector's `state` is not `missing` (the attach would otherwise be refused with `cloudflare.connector_missing`); when no tunnel is offered, render the paragraph `No Cloudflare connector runs on this host. Deploy one first.` and a `Link` `Open the connectors page` to `/cloudflare/connectors`. Otherwise render `ExposeTunnelForm` with `tunnels` the offered tunnels as `{ id, name }` in the list's order, `endpoints` the container's `networks[].network`, `networks` the host's networks, `ports` the container's `ports[].privatePort` and the `container` and `onDone` it was given.
3. Keep the files to the repository's style: no comments except for a non-obvious why, no `any`, and `pnpm -C web lint` clean with `--max-warnings 0`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
export TMPDIR=/root/.cache/w6b-tmp/11 GOTOOLCHAIN=local
timeout 600 pnpm -C web exec vitest run src/modules/cloudflare/expose-dialog.test.tsx src/modules/cloudflare/container-actions.test.tsx
```

Expected: both files pass (20 tests).

- [ ] **Step 5: Verify the task and commit**

```bash
export TMPDIR=/root/.cache/w6b-tmp/11 GOTOOLCHAIN=local
timeout 600 pnpm -C web lint
timeout 600 pnpm -C web typecheck
timeout 900 pnpm -C web test
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: lint and typecheck print no problem, every web test file passes, `go vet` prints nothing, every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output. The Go packages have no change in this task (`web/embed_test.go` still runs).

```bash
rm -rf /root/.cache/w6b-tmp/11 && GOTOOLCHAIN=local go clean -testcache
```

```bash
git add web/src/modules/cloudflare/container-actions.tsx web/src/modules/cloudflare/expose-dialog.tsx web/src/modules/cloudflare/expose-dialog.test.tsx web/src/modules/cloudflare/container-actions.test.tsx
git commit -m "feat(web): expose a container via a tunnel"
```

### Task 12: cloudflare: connector.stop

**Files:**
- Create: `internal/modules/cloudflare/connector_stop.go`
- Test: `internal/modules/cloudflare/connector_stop_test.go`

**Interfaces:**
- Consumes: Contracts "Wire types" (`ConnectorStopInput`, `ConnectorStopped`, `ConnectorRunInput`, `Connector`), "Names" (`tokenDir`, `connectorName`, `tokenFileName`, `tunnelIDProblem`), "Agent store" (`loadConnector`, `connectorClaims`, `insertClaim`, `deleteConnector`, `claimRow`), "Agent dependencies" (`cf.a.docker`, `engineCoded`), "Agent test host" (`newAGHost`, `agHost.cf`, `agHost.env`, `agHost.docker` with `calls` and `Fail`, `agHost.files` with `file`, `agToken`; Task 3) and "Agent actions": `cf.connectorRun` and `removeConnector(ctx, env, c, tunnel) (bool, error)` (Task 6). The pattern is `instanceRemoveHandler` in `internal/modules/npm/instance_remove.go` and its test (a removal that refuses while another object uses what it removes) and the claim structure of `internal/modules/npm/network_claims.go`.
- Produces: `func (c cf) connectorStop() core.ActionDef` (Contracts "Registration"; Task 14 appends it to `actions()`). The task also declares `connectorStopHandler` and the types and helpers it needs, with the prefix `connectorStop`, and no other top-level identifier.

**Items:** 34

**Depends on:** Task 6

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing test**

Create `internal/modules/cloudflare/connector_stop_test.go`. The tests start connectors by applying `cloudflare.connector.run` on the test host (Contracts: it pulls the pinned image when the Engine lacks it, and writes the token file, the row and the container); if Task 3's in-memory Engine needs the image registered first, register it inside `cpDeploy` with Task 3's own helper and change nothing else. They hold claims by inserting rows with `insertClaim`, drop a container from the Engine with the `RemoveContainer` method of `h.cf().a.docker`, and read the host with `loadConnector`, `InspectContainer` and the in-memory file store. The tests do not look at the effects journal: whatever `removeConnector` journals is Task 6's contract.

```go
package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	cpTunnel = "5c1d2f3a-0b4e-4c6d-8e7f-9a0b1c2d3e4f"
	cpOther  = "7a8b9c0d-1e2f-4a3b-8c4d-5e6f7a8b9c0d"
)

// cpModule registers the actions it holds, so the registry's own checks run on them.
type cpModule struct{ defs []core.ActionDef }

func (m cpModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

func cpJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// cpDeploy runs cloudflare.connector.run for tunnel on the host and answers the connector it started.
func cpDeploy(t *testing.T, h *agHost, tunnel string) Connector {
	t.Helper()
	in := cpJSON(t, ConnectorRunInput{Tunnel: tunnel, Token: agToken(tunnel)})
	raw, err := h.cf().connectorRun().Handler.Apply(context.Background(), h.env("run-"+tunnel[:8]), in)
	if err != nil {
		t.Fatalf("connector.run for %s: %v", tunnel, err)
	}
	var c Connector
	if err := json.Unmarshal(raw, &c); err != nil || c.ContainerID == "" {
		t.Fatalf("connector.run output %s (%v), want a connector with a container id", raw, err)
	}
	return c
}

// cpClaim records an attachment claim on the connector of tunnel.
func cpClaim(t *testing.T, h *agHost, tunnel, network, claimant string) {
	t.Helper()
	ctx := context.Background()
	err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error {
		return insertClaim(ctx, tx, claimRow{TunnelID: tunnel, Network: network, Claimant: claimant, ChangeID: "c0"})
	})
	if err != nil {
		t.Fatal(err)
	}
}

// cpDropContainer removes the connector's container from the Engine and leaves its record and file.
func cpDropContainer(t *testing.T, h *agHost, c Connector) {
	t.Helper()
	err := h.cf().a.docker.RemoveContainer(context.Background(), c.ContainerID, dockerapi.RemoveContainerOptions{Force: true})
	if err != nil {
		t.Fatal(err)
	}
}

func cpRowStored(t *testing.T, h *agHost, tunnel string) bool {
	t.Helper()
	_, ok, err := loadConnector(context.Background(), h.env("").DB.Read(), tunnel)
	if err != nil {
		t.Fatal(err)
	}
	return ok
}

func cpEngineHas(t *testing.T, h *agHost, tunnel string) bool {
	t.Helper()
	_, err := h.cf().a.docker.InspectContainer(context.Background(), connectorName(tunnel))
	switch {
	case err == nil:
		return true
	case dockerapi.IsNotFound(err):
		return false
	}
	t.Fatal(err)
	return false
}

func cpFileStored(h *agHost, tunnel string) bool {
	_, ok := h.files.file(tokenDir, tokenFileName(tunnel))
	return ok
}

func cpPlan(t *testing.T, h *agHost, tunnel string) (core.PlanOutcome, error) {
	t.Helper()
	return h.cf().connectorStop().Handler.Plan(context.Background(), h.env(""), cpJSON(t, ConnectorStopInput{Tunnel: tunnel}))
}

func cpApply(t *testing.T, h *agHost, change, tunnel string) (ConnectorStopped, error) {
	t.Helper()
	raw, err := h.cf().connectorStop().Handler.Apply(context.Background(), h.env(change), cpJSON(t, ConnectorStopInput{Tunnel: tunnel}))
	if err != nil {
		return ConnectorStopped{}, err
	}
	var out ConnectorStopped
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

// cpRefused fails the test unless err is a Definite refusal with the code, and answers it.
func cpRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want code %s", err, code)
	}
	if !core.IsDefinite(err) {
		t.Errorf("error %v is not marked definite", err)
	}
	return coded
}

// cpDiff is the plan of stopping the running connector c of tunnel.
func cpDiff(c Connector, tunnel string) []core.DiffLine {
	return []core.DiffLine{
		{Op: "-", Text: "container " + connectorName(tunnel) + " (" + c.ContainerID[:min(len(c.ContainerID), 12)] + ")"},
		{Op: "-", Text: "token file " + tokenDir + "/" + tokenFileName(tunnel)},
		{Op: "-", Text: "the stored connector record of tunnel " + tunnel},
	}
}

// TestCpDefinition: the stop is a compensable agent action under the module's lock, compensated by the run it
// removes, with no undo of its own.
func TestCpDefinition(t *testing.T) {
	h := newAGHost(t)
	def := h.cf().connectorStop()
	s := def.Spec
	if s.Name != "cloudflare.connector.stop" || s.Version != 1 || s.Summary == "" || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		s.CompensatedBy != "cloudflare.connector.run" || !s.Converges || !slices.Equal(s.LockKeys, []string{"cloudflare"}) {
		t.Errorf("spec %+v, want version 1, a summary, a compensable agent action compensated by cloudflare.connector.run, converging, lock cloudflare", s)
	}
	if def.Handler.Inverse != nil {
		t.Error("the stop has an inverse, want CompensatedBy as its one compensation")
	}
	if _, err := core.NewRegistry(cpModule{[]core.ActionDef{h.cf().connectorRun(), def}}); err != nil {
		t.Errorf("the registry refuses the action: %v", err)
	}
	v := schema.NewValidator()
	for input, field := range map[string]string{`{"tunnel":"` + cpTunnel + `"}`: "", `{"tunnel":"` + cpTunnel + `","y":1}`: "/y"} {
		err := v.ValidateInput(def, json.RawMessage(input))
		var coded *errcode.Error
		switch {
		case field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", input, err)
		case field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })):
			t.Errorf("input %s: %v, want a refusal naming %s", input, err, field)
		}
	}
}

// TestCpPlan: the plan lists what the stop removes, changes nothing, and is empty once nothing is left. A claim on
// another tunnel's connector does not hold this one.
func TestCpPlan(t *testing.T) {
	accepted := []struct {
		name  string
		setup func(t *testing.T, h *agHost, c Connector)
		diff  func(c Connector) []core.DiffLine
	}{
		{"a running connector", nil, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel) }},
		{"the container is gone from the Engine", cpDropContainer, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel)[1:] }},
		{"another tunnel's connector holds a claim", func(t *testing.T, h *agHost, _ Connector) {
			cpDeploy(t, h, cpOther)
			cpClaim(t, h, cpOther, "appnet", "expose:other.example.com")
		}, func(c Connector) []core.DiffLine { return cpDiff(c, cpTunnel) }},
	}
	for _, tt := range accepted {
		t.Run(tt.name, func(t *testing.T) {
			h := newAGHost(t)
			c := cpDeploy(t, h, cpTunnel)
			if tt.setup != nil {
				tt.setup(t, h, c)
			}
			before := len(h.docker.calls())
			out, err := cpPlan(t, h, cpTunnel)
			if err != nil {
				t.Fatal(err)
			}
			if want := tt.diff(c); !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if out.StateHash == "" {
				t.Error("empty state hash")
			}
			for _, line := range out.Diff {
				if strings.Contains(line.Text, agToken(cpTunnel)) {
					t.Errorf("diff line %q shows the token", line.Text)
				}
			}
			for _, call := range []string{"RemoveContainer", "StopContainer"} {
				if slices.Contains(h.docker.calls()[before:], call) {
					t.Errorf("a plan called %s", call)
				}
			}
		})
	}

	t.Run("nothing to stop", func(t *testing.T) {
		h := newAGHost(t)
		out, err := cpPlan(t, h, cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if len(out.Diff) != 0 || out.StateHash == "" {
			t.Errorf("diff %v and hash %q, want an empty diff and a hash", out.Diff, out.StateHash)
		}
	})

	refusals := []struct {
		name   string
		tunnel string
		setup  func(t *testing.T, h *agHost, c Connector)
		code   errcode.Code
		path   string
		has    []string
	}{
		{"a name instead of a tunnel id", "web", nil, errcode.InvalidInput, "/tunnel", nil},
		{"no tunnel id", "", nil, errcode.InvalidInput, "/tunnel", nil},
		{"a tunnel id in upper case", strings.ToUpper(cpTunnel), nil, errcode.InvalidInput, "/tunnel", nil},
		{"the connector holds claims", cpTunnel, func(t *testing.T, h *agHost, _ Connector) {
			cpClaim(t, h, cpTunnel, "appnet", "expose:app.example.com")
			cpClaim(t, h, cpTunnel, "backend", "expose:api.example.com")
		}, errcode.DockerInUse, "", []string{"expose:app.example.com", "appnet", "expose:api.example.com", "backend"}},
		{"the Engine is unavailable", cpTunnel, func(_ *testing.T, h *agHost, _ Connector) {
			h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		}, errcode.DockerEngineUnavailable, "", nil},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			h := newAGHost(t)
			c := cpDeploy(t, h, cpTunnel)
			if tt.setup != nil {
				tt.setup(t, h, c)
			}
			_, err := cpPlan(t, h, tt.tunnel)
			coded := cpRefused(t, err, tt.code)
			if tt.path != "" && !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tt.path }) {
				t.Errorf("fields %+v, want a refusal of %s", coded.Fields, tt.path)
			}
			for _, want := range tt.has {
				if !strings.Contains(coded.Detail, want) {
					t.Errorf("detail %q does not hold %q", coded.Detail, want)
				}
			}
			if slices.Contains(h.docker.calls(), "RemoveContainer") {
				t.Error("a refused plan removed the container")
			}
		})
	}
}

// TestCpPlanStateHash: the hash is stable while the connector is and changes when its container disappears or the
// connector is gone, so a plan made before the change is refused at apply.
func TestCpPlanStateHash(t *testing.T) {
	h := newAGHost(t)
	c := cpDeploy(t, h, cpTunnel)
	hash := func(h *agHost) string {
		t.Helper()
		out, err := cpPlan(t, h, cpTunnel)
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := hash(h)
	if again := hash(h); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	if empty := hash(newAGHost(t)); empty == first {
		t.Error("the hash of a host without the connector equals the hash of one with it")
	}
	cpDropContainer(t, h, c)
	if dropped := hash(h); dropped == first {
		t.Error("the hash did not change when the container disappeared")
	}
}

func TestCpApply(t *testing.T) {
	t.Run("removes the container, the file and the record of that tunnel only", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		cpDeploy(t, h, cpOther)
		if !cpEngineHas(t, h, cpTunnel) || !cpFileStored(h, cpTunnel) || !cpRowStored(t, h, cpTunnel) {
			t.Fatal("the connector run left no container, file or record to remove")
		}
		out, err := cpApply(t, h, "stop1", cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if out != (ConnectorStopped{Tunnel: cpTunnel, Removed: true}) {
			t.Errorf("output %+v, want tunnel %s removed", out, cpTunnel)
		}
		if cpEngineHas(t, h, cpTunnel) || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Error("the container, the file or the record of the stopped tunnel is still there")
		}
		if !cpEngineHas(t, h, cpOther) || !cpFileStored(h, cpOther) || !cpRowStored(t, h, cpOther) {
			t.Error("the stop removed part of another tunnel's connector")
		}
		plan, err := cpPlan(t, h, cpTunnel)
		if err != nil || len(plan.Diff) != 0 {
			t.Errorf("plan after the stop: diff %v, error %v, want an empty diff", plan.Diff, err)
		}
	})

	t.Run("a repeated stop converges", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		if _, err := cpApply(t, h, "stop1", cpTunnel); err != nil {
			t.Fatal(err)
		}
		out, err := cpApply(t, h, "stop2", cpTunnel)
		if err != nil {
			t.Fatalf("the second stop: %v", err)
		}
		if out != (ConnectorStopped{Tunnel: cpTunnel, Removed: false}) {
			t.Errorf("output %+v, want nothing removed", out)
		}
	})

	t.Run("finishes a stop whose container is already gone", func(t *testing.T) {
		h := newAGHost(t)
		c := cpDeploy(t, h, cpTunnel)
		cpDropContainer(t, h, c)
		out, err := cpApply(t, h, "stop1", cpTunnel)
		if err != nil {
			t.Fatal(err)
		}
		if !out.Removed || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Errorf("output %+v, file stored %v, record stored %v, want the file and the record removed", out, cpFileStored(h, cpTunnel), cpRowStored(t, h, cpTunnel))
		}
	})

	t.Run("refused again at apply while a claim is held", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		cpClaim(t, h, cpTunnel, "appnet", "expose:app.example.com")
		_, err := cpApply(t, h, "stop1", cpTunnel)
		coded := cpRefused(t, err, errcode.DockerInUse)
		if !strings.Contains(coded.Detail, "expose:app.example.com") {
			t.Errorf("detail %q does not name the claimant", coded.Detail)
		}
		if slices.Contains(h.docker.calls(), "RemoveContainer") || !cpEngineHas(t, h, cpTunnel) || !cpFileStored(h, cpTunnel) || !cpRowStored(t, h, cpTunnel) {
			t.Error("a refused apply removed part of the connector")
		}
	})

	t.Run("an Engine failure keeps the record and a repeat finishes", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		h.docker.Fail("RemoveContainer", &dockerapi.APIError{StatusCode: 500, Message: "boom"})
		if _, err := cpApply(t, h, "stop1", cpTunnel); err == nil {
			t.Fatal("the stop succeeded although the Engine failed")
		}
		if !cpRowStored(t, h, cpTunnel) {
			t.Error("the record is gone although the container was not removed")
		}
		out, err := cpApply(t, h, "stop2", cpTunnel)
		if err != nil || !out.Removed {
			t.Fatalf("the repeated stop: %+v, %v", out, err)
		}
		if cpEngineHas(t, h, cpTunnel) || cpFileStored(h, cpTunnel) || cpRowStored(t, h, cpTunnel) {
			t.Error("the repeated stop left the container, the file or the record")
		}
	})
}

// TestCpVerify: the stop passes only when neither the container nor the record is left.
func TestCpVerify(t *testing.T) {
	ctx := context.Background()
	verify := func(t *testing.T, h *agHost) error {
		t.Helper()
		in := cpJSON(t, ConnectorStopInput{Tunnel: cpTunnel})
		return h.cf().connectorStop().Handler.Verify(ctx, h.env("stop1"), in, cpJSON(t, ConnectorStopped{Tunnel: cpTunnel, Removed: true}))
	}

	t.Run("a stopped connector", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		if verify(t, h) == nil {
			t.Error("verify passed while the connector runs")
		}
		if _, err := cpApply(t, h, "stop1", cpTunnel); err != nil {
			t.Fatal(err)
		}
		if err := verify(t, h); err != nil {
			t.Errorf("verify after the stop: %v", err)
		}
	})

	t.Run("the record is left", func(t *testing.T) {
		h := newAGHost(t)
		c := cpDeploy(t, h, cpTunnel)
		cpDropContainer(t, h, c)
		if err := verify(t, h); err == nil || !strings.Contains(err.Error(), "connector record") {
			t.Errorf("verify: %v, want an error naming the connector record", err)
		}
	})

	t.Run("the container is left", func(t *testing.T) {
		h := newAGHost(t)
		cpDeploy(t, h, cpTunnel)
		err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error { return deleteConnector(ctx, tx, cpTunnel) })
		if err != nil {
			t.Fatal(err)
		}
		if err := verify(t, h); err == nil || !strings.Contains(err.Error(), "container "+connectorName(cpTunnel)) {
			t.Errorf("verify: %v, want an error naming the container", err)
		}
	})
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w6b-tmp/12 && export TMPDIR=/root/.cache/w6b-tmp/12 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestCp' ./internal/modules/cloudflare/
```

Expected: FAIL to build, with `h.cf().connectorStop undefined (type cf has no field or method connectorStop)`. Quote that output in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/cloudflare/connector_stop.go`.

1. `func (c cf) connectorStop() core.ActionDef` returns `core.DefineMutating[ConnectorStopInput, ConnectorStopped]` with `core.ActionSpec{Name: ID + ".connector.stop", Version: 1, Class: core.ClassCompensable, Side: core.SideAgent, LockKeys: []string{"cloudflare"}, CompensatedBy: ID + ".connector.run", Converges: true, Summary: <one sentence>}` and the handler `connectorStopHandler{c: c}`. The handler has `Plan`, `Apply` and `Verify` only: no `Inverse` (the compensation is `CompensatedBy`; `TestCpDefinition` registers it with `cloudflare.connector.run` and the registry accepts exactly one compensation) and no `Recover`.
2. A shared read, `inspect(ctx, env, tunnel)`, answers what the stop finds and refuses what it must not remove. It only reads, so every failure is `core.Definite`. In this order:
   - `tunnelIDProblem(tunnel) != ""`: `request.invalid_input` with the problem as detail and the field `/tunnel`.
   - `connectorClaims(ctx, env.DB.Read(), tunnel, "")` returns at least one claim: `errcode.New(errcode.DockerInUse, ...)` whose detail names the connector and every claim as `<claimant> on <network>`, joined with `, ` in the order read, and says the exposures are removed first (the tests look for each claimant and each network; the token never appears).
   - the connector row with `loadConnector(ctx, env.DB.Read(), tunnel)`, and the container with `InspectContainer(ctx, connectorName(tunnel))` on `c.a.docker`: a not-found container (`dockerapi.IsNotFound`) is absent; any other failure is `fmt.Errorf("cloudflare: inspect container %s: %w", name, engineCoded(err))`, so an unreachable Engine is `docker.engine_unavailable`.
3. `Plan` returns the diff of what is left to remove, every line with `Op: "-"`, in this order and with exactly these texts (`short` is the first 12 characters of the container id, or all of it when shorter): `container <connectorName(tunnel)> (<short>)` when the container exists; `token file <tokenDir>/<tokenFileName(tunnel)>` and `the stored connector record of tunnel <tunnel>` when the row exists. With neither, the diff is empty (a stop of an absent connector). `StateHash` is the hex SHA-256 of the newline-joined row presence, the row's container id, the found container id and whether it runs, so a plan made before the container disappears or is recreated is refused at apply. `Guards` is an empty, non-nil slice and there is no `Confirmation`: the compensation is `cloudflare.connector.run`.
4. `Apply` runs `inspect` again (its refusals are Definite and nothing was removed), then calls `removeConnector(ctx, env, c, tunnel)` exactly once with the Env it was given and returns `ConnectorStopped{Tunnel: tunnel, Removed: <its bool>}`. An error from `removeConnector` is returned unchanged, not marked Definite: part of the removal may have happened, and the record stays until the container is gone so a repeated stop finishes (the test `an Engine failure keeps the record and a repeat finishes`). `Apply` journals nothing itself and does not call the Engine directly: `removeConnector` owns the removal of the container, the file and the row.
5. `Verify` reads the same two facts and fails with `cloudflare: the stop left <list>` when either remains, the list joined with `, ` from `container <connectorName(tunnel)>` and `the connector record`. An Engine failure other than not-found is returned wrapped with `engineCoded`, not Definite. It ignores the claims.
6. Do not edit `cloudflare.go` or `connector_run.go`; Task 14 registers the action.

- [ ] **Step 4: Run the test to verify it passes**

```bash
export TMPDIR=/root/.cache/w6b-tmp/12 GOTOOLCHAIN=local
timeout 600 go test -count=1 -v -run 'TestCp' ./internal/modules/cloudflare/
```

Expected: PASS for `TestCpDefinition`, `TestCpPlan`, `TestCpPlanStateHash`, `TestCpApply` and `TestCpVerify`.

- [ ] **Step 5: Verify the task and commit**

```bash
mkdir -p /root/.cache/w6b-tmp/12 && export TMPDIR=/root/.cache/w6b-tmp/12 GOTOOLCHAIN=local
timeout 600 go build ./... && timeout 300 go vet ./...
timeout 1500 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/cloudflare/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
```

Expected: `go vet` prints nothing, every `go test` line is `ok` or `?`, `golangci-lint` prints `0 issues.`, `go mod tidy -diff` prints nothing. Record the real output.

```bash
rm -rf /root/.cache/w6b-tmp/12 && GOTOOLCHAIN=local go clean -testcache
```

```bash
git add internal/modules/cloudflare/connector_stop.go internal/modules/cloudflare/connector_stop_test.go
git commit -m "feat(cloudflare): add connector.stop"
```

### Task 13: cloudflare: network.detach

**Files:**
- Create: `internal/modules/cloudflare/connector_network_detach.go`
- Test: `internal/modules/cloudflare/connector_network_detach_test.go`

**Interfaces:**
- Consumes (Contracts): `NetworkDetachInput`, `NetworkAttachment`, `NetworkAttachInput` (Task 3 wire types); `claimRow`, `connectorRow`, and the store helpers `connectorClaims`, `deleteClaim`, `insertClaim`, `insertConnector`, with `connectorSealAD`; `connectorName`, `tunnelIDProblem`, `claimantProblem`, `networkNamePattern`, `engineCoded`; `cf` with `a agentDeps` and its `a.docker` methods `InspectContainer`, `InspectNetwork`, `DisconnectNetwork`; Task 6's effects (`connectorEffect`, `resourceOf`, `nullState`, `engineCommitWait`, `c.journal`); Task 8's `networkAttach()` (used by the tests to seed claims), `networkClaims` (with `Observe` and `Inverse` for the claim Create, claim Delete, endpoint Modify and endpoint Modify with `InverseRecovery` effects), `claimDocument`, `nounClaim`, `nounEndpoint`; the Agent test host (`newAGHost`, `h.cf()`, `h.env`, `h.docker` with `putContainer`, `putNetwork`, `Fail`, `calls`, `agContainer`, `agOnNetwork`, `agNetwork`, `agToken`, `agHost`); `ID`.
- Produces (Contracts "Agent actions"): `func (c cf) networkDetach() core.ActionDef` for `cloudflare.network.detach` v1, which Task 14 registers; the handler type `networkDetachHandler` (embeds `networkClaims`).
- Every top-level identifier this file declares starts with `detach` or is `networkDetachHandler`, so none collides with Task 8's (`attach…`, `claim…`, `networkAttach…`, `networkClaims`) or Task 12's. Test identifiers start with `Nd`/`nd`.

**Items:** 35

**Depends on:** Task 8

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

`cloudflare.network.detach` releases the claims of one claimant on a tunnel's connector, and takes the connector off a network only when no other claim needs it and the connector was not on that network before the first claim. It is the pair of Task 8's `cloudflare.network.attach` and mirrors the detach half of `internal/modules/npm/network_claims.go` (read `networkDetachHandler`'s `check`, `Plan`, `Apply` and `Verify` first), with the tunnel in every identity and key. Its undo is not written here: `networkClaims.Observe` and `networkClaims.Inverse` of Task 8 undo every effect this action journals, and the handler gets them by embedding `networkClaims`. Decisions the tests pin:

- Input: `NetworkDetachInput{Tunnel, Network, Claimant}`; `Network` `""` means every network on which the claimant holds a claim for the tunnel.
- Validation, a Definite `request.invalid_input` naming every bad field, in the detail joined by `; `: `/tunnel` (`tunnelIDProblem`), `/network` when not `""` (`networkNamePattern`), `/claimant` (`claimantProblem`). Validation runs before any database or Engine read.
- The claims removed are the rows of `connectorClaims(ctx, db, tunnel, in.Network)` whose claimant is `in.Claimant`, in network-name order. A claim of another tunnel is never listed, changed or removed. No claim to remove means a converged action: the plan has an empty diff, Apply journals no effect and makes no Engine call (not even a read), and the output is `NetworkAttachment{Tunnel, Network: in.Network, Claimant: in.Claimant}`.
- With at least one claim, the check inspects the connector container `connectorName(tunnel)` once (a 404 means no connector: no network is left) and, per claim, the claim's network by name (a 404 means the network is gone: nothing to leave) and the other claimants of that network (from `connectorClaims(ctx, db, tunnel, network)`, ordered). The connector's endpoint is `NetworkSettings.Networks[claim.Network]` of the container, nil when the container or network is absent. A step disconnects when there is no other claimant, the claim is not `Preexisting` and the endpoint exists. A failed read is returned Definite wrapped as `cloudflare: inspect container <name>: %w` / `cloudflare: inspect network <name>: %w` with the cause passed through `engineCoded`, so an unreachable Engine is `docker.engine_unavailable`.
- Plan diff, per claim in order: `- claim of <claimant> on network <network>`; then, in this priority, `- container <connector name> leaves network <network>` when it disconnects, else (operation space) `<connector name> stays on network <network>: claimed by <other claimants joined by ", ">` when others remain, else `<connector name> stays on network <network>: it was there before` when the claim is `Preexisting`. The state hash is the hex SHA-256 of the JSON array, one element per claim in order, of `{networkId (empty when the network is gone), endpointId (the connector's endpoint id, empty when not attached), tunnel, claims (the network's claims as claimant, changeId, preexisting, ordered by claimant)}`; with no claim it is the hash of `[]`. `Guards` is the empty slice.
- Apply decides again with the same check, then handles the claims in network order, each network's disconnect before its claim removal, so a failed disconnect keeps the claim and a repeat finishes the work.
  - A disconnect is a Modify effect of `cloudflare.endpoint:<tunnel>|<network>` (`connectorEffect` with the state `networkClaims.Observe` reports before the call as the before-state, a commit wait of `engineCommitWait`, `InverseRecovery` `{Kind: "name", Value: <connector name>@<network>}`), journaled with `c.journal` and `networkClaims.Observe` as its observer; the call is `DisconnectNetwork(ctx, <network id>, <container id>, false)`, an error wrapped as `cloudflare: disconnect <name> from network <network>: %w` through `engineCoded`.
  - A claim removal is a Delete effect of `cloudflare.claim:<tunnel>|<network>|<claimant>` with `claimDocument(claim)` as the before-state, `After` the null state, `Local` and `InverseLocal` true, journaled with `networkClaims.Observe` as its observer; the call deletes the row with `deleteClaim` in an `env.DB.Write`.
  - A 4xx Engine answer or a Definite error skips the effect and is returned Definite; any other failure leaves it intended (the behaviour of `c.journal`).
- The output is `NetworkAttachment{Tunnel, Network: in.Network, Claimant: in.Claimant, Preexisting, Attached}`: `Preexisting` is true when any removed claim was `Preexisting`; `Attached` is true when for any removed claim the connector was on the network and did not leave it (so it is false when nothing was removed, which is a deliberate reading of the wire comment for a converged call and for `Network` `""`).
- Verify fails unless the claimant holds no claim for the tunnel on `Network`, or on any network when `Network` is `""`; the message names the first network left.
- `Spec`: `Name: ID + ".network.detach"`, a one-sentence `Summary`, `Version: 1`, `Class: core.ClassCompensable`, `Side: core.SideAgent`, `LockKeys: []string{"cloudflare"}`, `Converges: true`. The action is not added to `Manifest()` (Task 14 does it).

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/cloudflare/connector_network_detach_test.go`:

```go
package cloudflare

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"reflect"
	"slices"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const (
	ndTunnel      = "5e6f7a8b-9c0d-4e1f-8a2b-3c4d5e6f7a8b"
	ndOtherTunnel = "6f7a8b9c-0d1e-4f2a-9b3c-4d5e6f7a8b9c"
	ndClaimant    = "expose:app.example.com"
	ndClaimant2   = "expose:b.example.com"
)

// ndModule registers the actions, so the registry's own checks run on them.
type ndModule struct{ defs []core.ActionDef }

func (m ndModule) Manifest() core.Manifest {
	return core.Manifest{ID: ID, Version: 1, Actions: m.defs}
}

// newND is an agent host with a running connector for ndTunnel that is on no user-defined network, and the
// user-defined bridge networks web and web2.
func newND(t *testing.T) *agHost {
	t.Helper()
	h := newAGHost(t)
	h.docker.putNetwork(agNetwork("web", "bridge"))
	h.docker.putNetwork(agNetwork("web2", "bridge"))
	conn := agContainer(connectorName(ndTunnel), func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "running", true })
	h.docker.putContainer(conn)
	ctx := context.Background()
	env := h.env("")
	sealed, err := env.Sealer.Seal(connectorSealAD(ndTunnel), []byte(agToken(ndTunnel)))
	if err != nil {
		t.Fatal(err)
	}
	err = env.DB.Write(ctx, func(tx *sql.Tx) error {
		return insertConnector(ctx, tx, connectorRow{TunnelID: ndTunnel, ContainerID: conn.ID, TokenCT: sealed, ChangeID: "c0", CreatedAt: env.Clock.Now()})
	})
	if err != nil {
		t.Fatal(err)
	}
	return h
}

// ndJoin puts the connector on the network before any claim exists.
func ndJoin(t *testing.T, h *agHost, network, ip string) {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(ndTunnel))
	if err != nil {
		t.Fatal(err)
	}
	agOnNetwork(network, ip)(&c)
	h.docker.putContainer(c)
}

func ndJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// ndAttach applies cloudflare.network.attach for the claimant, with no target, as change.
func ndAttach(t *testing.T, h *agHost, change, network, claimant string) {
	t.Helper()
	raw := ndJSON(t, NetworkAttachInput{Tunnel: ndTunnel, Network: network, Claimant: claimant})
	if _, err := h.cf().networkAttach().Handler.Apply(context.Background(), h.env(change), raw); err != nil {
		t.Fatalf("attach %s on %s: %v", claimant, network, err)
	}
}

// ndAddClaim inserts a claim row without touching the Engine.
func ndAddClaim(t *testing.T, h *agHost, c claimRow) {
	t.Helper()
	ctx := context.Background()
	if err := h.env("").DB.Write(ctx, func(tx *sql.Tx) error { return insertClaim(ctx, tx, c) }); err != nil {
		t.Fatal(err)
	}
}

func ndClaims(t *testing.T, h *agHost, tunnel string) []claimRow {
	t.Helper()
	rows, err := connectorClaims(context.Background(), h.env("").DB.Read(), tunnel, "")
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func ndInput(mutate ...func(*NetworkDetachInput)) json.RawMessage {
	in := NetworkDetachInput{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}
	for _, m := range mutate {
		if m != nil {
			m(&in)
		}
	}
	raw, err := json.Marshal(in)
	if err != nil {
		panic(err)
	}
	return raw
}

func ndAllNetworks(in *NetworkDetachInput) { in.Network = "" }

func ndApply(t *testing.T, h *agHost, change string, mutate ...func(*NetworkDetachInput)) (NetworkAttachment, error) {
	t.Helper()
	raw, err := h.cf().networkDetach().Handler.Apply(context.Background(), h.env(change), ndInput(mutate...))
	if err != nil {
		return NetworkAttachment{}, err
	}
	var out NetworkAttachment
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return out, nil
}

func ndOn(t *testing.T, h *agHost, network string) bool {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(ndTunnel))
	if err != nil {
		t.Fatal(err)
	}
	return c.NetworkSettings.Networks[network] != nil
}

var ndMutations = []string{"ConnectNetwork", "DisconnectNetwork", "CreateContainer", "StartContainer", "StopContainer", "RemoveContainer", "PullImage"}

// ndMutatedAfter lists the Engine calls that change something, made after the first mark calls.
func ndMutatedAfter(h *agHost, mark int) []string {
	var got []string
	for _, c := range h.docker.calls()[mark:] {
		if slices.Contains(ndMutations, c) {
			got = append(got, c)
		}
	}
	return got
}

// ndEffects are the journaled effects of one change, in journal order.
func ndEffects(t *testing.T, h *agHost, change string) []core.Effect {
	t.Helper()
	all, err := h.env(change).Effects.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var effs []core.Effect
	for _, e := range all {
		if e.ChangeID == change {
			effs = append(effs, e)
		}
	}
	return effs
}

func ndRefused(t *testing.T, err error, code errcode.Code) *errcode.Error {
	t.Helper()
	var coded *errcode.Error
	if err == nil || !core.IsDefinite(err) || !errors.As(err, &coded) || coded.Code != code {
		t.Fatalf("error %v, want a Definite %s", err, code)
	}
	return coded
}

// ndObserved is the state the detach handler observes for the effect now.
func ndObserved(t *testing.T, h *agHost, change string, e core.Effect) string {
	t.Helper()
	obs, err := h.cf().networkDetach().Handler.Observe(context.Background(), h.env(change), e)
	if err != nil {
		t.Fatalf("observe %s: %v", e.Resource, err)
	}
	return string(obs.Current.State)
}

// ndUndo undoes the made effects of the change in reverse order, as the engine does, and returns each result by
// resource.
func ndUndo(t *testing.T, h *agHost, change string) map[string]core.InverseResult {
	t.Helper()
	def := h.cf().networkDetach()
	env := h.env(change)
	effs := ndEffects(t, h, change)
	results := map[string]core.InverseResult{}
	for i := len(effs) - 1; i >= 0; i-- {
		e := effs[i]
		if e.State != core.EffectMade {
			continue
		}
		e.State = core.EffectUndoing
		res, err := def.Handler.Inverse(context.Background(), env, e)
		if err != nil {
			t.Fatalf("inverse of %s: %v", e.Resource, err)
		}
		results[e.Resource] = res
	}
	return results
}

func TestNdSpec(t *testing.T) {
	h := newND(t)
	def := h.cf().networkDetach()
	s := def.Spec
	if s.Name != "cloudflare.network.detach" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"cloudflare"}) || !s.Converges || s.Summary == "" {
		t.Errorf("spec %+v", s)
	}
	handler := def.Handler
	if handler.Plan == nil || handler.Apply == nil || handler.Verify == nil || handler.Observe == nil || handler.Inverse == nil {
		t.Errorf("handler lacks a stage: plan %t apply %t verify %t observe %t inverse %t",
			handler.Plan != nil, handler.Apply != nil, handler.Verify != nil, handler.Observe != nil, handler.Inverse != nil)
	}
	if def.InputType != reflect.TypeFor[NetworkDetachInput]() || def.OutputType != reflect.TypeFor[NetworkAttachment]() {
		t.Errorf("types %v and %v", def.InputType, def.OutputType)
	}
	if _, err := core.NewRegistry(ndModule{defs: []core.ActionDef{h.cf().networkAttach(), def}}); err != nil {
		t.Errorf("the registry refuses the actions: %v", err)
	}
	v := schema.NewValidator()
	for _, tc := range []struct{ input, field string }{
		{`{"tunnel":"` + ndTunnel + `","network":"","claimant":"c"}`, ""},
		{`{"tunnel":"` + ndTunnel + `","network":"web","claimant":"c","x":1}`, "/x"},
	} {
		err := v.ValidateInput(def, json.RawMessage(tc.input))
		var coded *errcode.Error
		switch {
		case tc.field == "" && err != nil:
			t.Errorf("input %s: %v, want it accepted", tc.input, err)
		case tc.field != "" && (!errors.As(err, &coded) || !slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == tc.field })):
			t.Errorf("input %s: %v, want a refusal naming %s", tc.input, err, tc.field)
		}
	}
}

func TestNdRefuses(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		mutate func(*NetworkDetachInput)
		fields []string // the paths of the field errors, sorted
	}{
		{"a bad tunnel id", func(in *NetworkDetachInput) { in.Tunnel = "x" }, []string{"/tunnel"}},
		{"a bad network name", func(in *NetworkDetachInput) { in.Network = "bad name" }, []string{"/network"}},
		{"an empty claimant", func(in *NetworkDetachInput) { in.Claimant = "" }, []string{"/claimant"}},
		{"a claimant with white space", func(in *NetworkDetachInput) { in.Claimant = "a b" }, []string{"/claimant"}},
		{"every bad field", func(in *NetworkDetachInput) { in.Tunnel, in.Network, in.Claimant = "x", "bad name", "a b" },
			[]string{"/claimant", "/network", "/tunnel"}},
	} {
		for _, stage := range []string{"plan", "apply"} {
			t.Run(tc.name+" "+stage, func(t *testing.T) {
				h := newND(t)
				ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
				def := h.cf().networkDetach()
				var err error
				if stage == "plan" {
					_, err = def.Handler.Plan(ctx, h.env(""), ndInput(tc.mutate))
				} else {
					_, err = def.Handler.Apply(ctx, h.env("c1"), ndInput(tc.mutate))
				}
				coded := ndRefused(t, err, errcode.InvalidInput)
				var paths []string
				for _, f := range coded.Fields {
					paths = append(paths, f.Path)
				}
				slices.Sort(paths)
				if !slices.Equal(paths, tc.fields) {
					t.Errorf("field paths %v, want %v", paths, tc.fields)
				}
				if got := h.docker.calls(); len(got) != 0 {
					t.Errorf("a refused input reached the Engine: %v", got)
				}
				if got := ndClaims(t, h, ndTunnel); len(got) != 1 {
					t.Errorf("claims %+v, want the claim untouched", got)
				}
			})
		}
	}
}

func TestNdPlan(t *testing.T) {
	ctx := context.Background()
	name := connectorName(ndTunnel)
	del := func(claimant, network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "claim of " + claimant + " on network " + network}
	}
	leave := func(network string) core.DiffLine {
		return core.DiffLine{Op: "-", Text: "container " + name + " leaves network " + network}
	}
	for _, tc := range []struct {
		name   string
		seed   func(t *testing.T, h *agHost)
		mutate func(*NetworkDetachInput)
		diff   []core.DiffLine
	}{
		{name: "the last claim takes the connector off the network",
			seed: func(t *testing.T, h *agHost) { ndAttach(t, h, "c1", "web", ndClaimant) },
			diff: []core.DiffLine{del(ndClaimant, "web"), leave("web")}},
		{name: "another claim keeps the connector there",
			seed: func(t *testing.T, h *agHost) {
				ndAttach(t, h, "c1", "web", ndClaimant)
				ndAttach(t, h, "c2", "web", ndClaimant2)
			},
			diff: []core.DiffLine{del(ndClaimant, "web"), {Op: " ", Text: name + " stays on network web: claimed by " + ndClaimant2}}},
		{name: "an attachment that was there before",
			seed: func(t *testing.T, h *agHost) {
				ndJoin(t, h, "web", "172.20.0.9")
				ndAttach(t, h, "c1", "web", ndClaimant)
			},
			diff: []core.DiffLine{del(ndClaimant, "web"), {Op: " ", Text: name + " stays on network web: it was there before"}}},
		{name: "every network of the claimant",
			seed: func(t *testing.T, h *agHost) {
				ndAttach(t, h, "c1", "web", ndClaimant)
				ndAttach(t, h, "c2", "web2", ndClaimant)
			},
			mutate: ndAllNetworks,
			diff:   []core.DiffLine{del(ndClaimant, "web"), leave("web"), del(ndClaimant, "web2"), leave("web2")}},
		{name: "no claim of the claimant",
			seed: func(t *testing.T, h *agHost) { ndAttach(t, h, "c1", "web", ndClaimant2) }},
		{name: "no claim on any network", mutate: ndAllNetworks},
		{name: "a claim of another tunnel",
			seed: func(t *testing.T, h *agHost) {
				ndAddClaim(t, h, claimRow{TunnelID: ndOtherTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
			}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newND(t)
			if tc.seed != nil {
				tc.seed(t, h)
			}
			mark := len(h.docker.calls())
			out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput(tc.mutate))
			if err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(out.Diff, tc.diff) {
				t.Errorf("diff %+v, want %+v", out.Diff, tc.diff)
			}
			if got := ndMutatedAfter(h, mark); len(got) != 0 {
				t.Errorf("the plan changed the Engine: %v", got)
			}
		})
	}

	t.Run("the state hash follows the claims", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		plan := func() core.PlanOutcome {
			out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
			if err != nil {
				t.Fatal(err)
			}
			return out
		}
		first := plan()
		if len(first.StateHash) != 64 || first.StateHash != plan().StateHash || first.Guards == nil || len(first.Guards) != 0 {
			t.Errorf("hash %q (stable %t), guards %v: want a 64-digit stable hash and an empty guard list", first.StateHash, first.StateHash == plan().StateHash, first.Guards)
		}
		ndAttach(t, h, "c2", "web", ndClaimant2)
		if plan().StateHash == first.StateHash {
			t.Error("the hash did not change when another claimant joined the network")
		}
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
	})
	t.Run("no claim needs no Engine", func(t *testing.T) {
		h := newND(t)
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		out, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err != nil || len(out.Diff) != 0 || len(h.docker.calls()) != 0 {
			t.Errorf("diff %+v, error %v, Engine calls %v; want an empty diff and no call", out.Diff, err, h.docker.calls())
		}
	})
}

func TestNdApplyLeavesTheNetworkAndReleasesTheClaim(t *testing.T) {
	ctx := context.Background()
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	mark := len(h.docker.calls())
	out, err := ndApply(t, h, "c2")
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}); out != want {
		t.Errorf("output %+v, want %+v", out, want)
	}
	if ndOn(t, h, "web") {
		t.Error("the connector is still on web")
	}
	if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
		t.Errorf("claims %+v, want none", got)
	}
	if got := ndMutatedAfter(h, mark); !slices.Equal(got, []string{"DisconnectNetwork"}) {
		t.Errorf("Engine calls %v, want one DisconnectNetwork", got)
	}

	effs := ndEffects(t, h, "c2")
	if len(effs) != 2 {
		t.Fatalf("%d effects, want 2: %+v", len(effs), effs)
	}
	ep, claim := effs[0], effs[1]
	if ep.Kind != core.EffectModify || ep.Resource != "cloudflare.endpoint:"+ndTunnel+"|web" || ep.State != core.EffectMade || ep.Local ||
		ep.CommitWait != engineCommitWait || ep.Recovery != nil || ep.Post == nil || string(ep.Post.State) != "null" ||
		string(ep.Before.State) == "null" || ep.InverseRecovery == nil || ep.InverseRecovery.Kind != "name" ||
		ep.InverseRecovery.Value != connectorName(ndTunnel)+"@web" {
		t.Errorf("endpoint effect %+v, want a made non-local modify with the inverse recovery %s@web", ep, connectorName(ndTunnel))
	}
	wantBefore, err := claimDocument(claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c1"})
	if err != nil {
		t.Fatal(err)
	}
	if claim.Kind != core.EffectDelete || claim.Resource != "cloudflare.claim:"+ndTunnel+"|web|"+ndClaimant || claim.State != core.EffectMade ||
		!claim.Local || !claim.InverseLocal || string(claim.Before.State) != string(wantBefore) || claim.After == nil ||
		string(claim.After.State) != "null" || claim.Post == nil || string(claim.Post.State) != "null" {
		t.Errorf("claim effect %+v, want a made local delete of the claim with a null post-state", claim)
	}
	if err := h.cf().networkDetach().Handler.Verify(ctx, h.env("c2"), ndInput(), ndJSON(t, out)); err != nil {
		t.Errorf("Verify: %v", err)
	}
}

func TestNdApplyKeepsWhatIsStillNeeded(t *testing.T) {
	t.Run("another claim keeps the connector there", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		ndAttach(t, h, "c2", "web", ndClaimant2)
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c3")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Attached || out.Preexisting {
			t.Errorf("output %+v, want the connector still attached", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want the connector left on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 1 || got[0].Claimant != ndClaimant2 {
			t.Errorf("claims %+v, want only %s", got, ndClaimant2)
		}
		if effs := ndEffects(t, h, "c3"); len(effs) != 1 || effs[0].Kind != core.EffectDelete {
			t.Errorf("effects %+v, want only the claim deletion", effs)
		}
	})
	t.Run("an attachment that was there before stays", func(t *testing.T) {
		h := newND(t)
		ndJoin(t, h, "web", "172.20.0.9")
		ndAttach(t, h, "c1", "web", ndClaimant)
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if !out.Preexisting || !out.Attached {
			t.Errorf("output %+v, want a preexisting attachment left in place", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want the connector left on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})
	t.Run("a claim of another tunnel is not touched", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		other := claimRow{TunnelID: ndOtherTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0", Preexisting: true}
		ndAddClaim(t, h, other)
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if ndOn(t, h, "web") || out.Attached {
			t.Errorf("the other tunnel's claim kept this connector on web: output %+v", out)
		}
		if got := ndClaims(t, h, ndOtherTunnel); len(got) != 1 || got[0] != other {
			t.Errorf("the other tunnel's claims %+v, want %+v", got, other)
		}
	})
}

func TestNdApplyEveryNetworkOfTheClaimant(t *testing.T) {
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	ndAttach(t, h, "c2", "web", ndClaimant2)
	ndAttach(t, h, "c3", "web2", ndClaimant)
	out, err := ndApply(t, h, "c4", ndAllNetworks)
	if err != nil {
		t.Fatal(err)
	}
	if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "", Claimant: ndClaimant, Attached: true}); out != want {
		t.Errorf("output %+v, want %+v (still on web, which the other claimant needs)", out, want)
	}
	if !ndOn(t, h, "web") || ndOn(t, h, "web2") {
		t.Errorf("on web %t, on web2 %t; want web kept and web2 left", ndOn(t, h, "web"), ndOn(t, h, "web2"))
	}
	if got := ndClaims(t, h, ndTunnel); len(got) != 1 || got[0].Claimant != ndClaimant2 || got[0].Network != "web" {
		t.Errorf("claims %+v, want only %s on web", got, ndClaimant2)
	}
	var got []string
	for _, e := range ndEffects(t, h, "c4") {
		got = append(got, e.Resource)
	}
	want := []string{
		"cloudflare.claim:" + ndTunnel + "|web|" + ndClaimant,
		"cloudflare.endpoint:" + ndTunnel + "|web2",
		"cloudflare.claim:" + ndTunnel + "|web2|" + ndClaimant,
	}
	if !slices.Equal(got, want) {
		t.Errorf("effects %v, want %v (each network's disconnect before its claim)", got, want)
	}
}

func TestNdApplyConverges(t *testing.T) {
	ctx := context.Background()
	t.Run("a second detach changes nothing", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c3")
		if err != nil {
			t.Fatal(err)
		}
		if want := (NetworkAttachment{Tunnel: ndTunnel, Network: "web", Claimant: ndClaimant}); out != want {
			t.Errorf("output %+v, want %+v", out, want)
		}
		if got := h.docker.calls()[mark:]; len(got) != 0 {
			t.Errorf("a converged detach called the Engine: %v", got)
		}
		if effs := ndEffects(t, h, "c3"); len(effs) != 0 {
			t.Errorf("effects %+v, want none", effs)
		}
		plan, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
		if err != nil || len(plan.Diff) != 0 {
			t.Errorf("plan after the detach: diff %+v, error %v; want an empty diff", plan.Diff, err)
		}
	})
	t.Run("the connector container is gone", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		if err := h.docker.RemoveContainer(ctx, connectorName(ndTunnel), dockerapi.RemoveContainerOptions{Force: true}); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		out, err := ndApply(t, h, "c2")
		if err != nil {
			t.Fatal(err)
		}
		if out.Attached {
			t.Errorf("output %+v, want not attached", out)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
		if effs := ndEffects(t, h, "c2"); len(effs) != 1 || effs[0].Kind != core.EffectDelete {
			t.Errorf("effects %+v, want only the claim deletion", effs)
		}
	})
	t.Run("the claimed network is gone", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "gone", Claimant: ndClaimant, ChangeID: "c0"})
		mark := len(h.docker.calls())
		if _, err := ndApply(t, h, "c2", func(in *NetworkDetachInput) { in.Network = "gone" }); err != nil {
			t.Fatal(err)
		}
		if got := ndMutatedAfter(h, mark); len(got) != 0 {
			t.Errorf("Engine calls %v, want none", got)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 0 {
			t.Errorf("claims %+v, want none", got)
		}
	})
}

func TestNdApplyFailures(t *testing.T) {
	t.Run("a refused disconnect is Definite, keeps the claim and a repeat finishes", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		h.docker.Fail("DisconnectNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "no"})
		_, err := ndApply(t, h, "c2")
		if err == nil || !core.IsDefinite(err) {
			t.Fatalf("error %v, want a Definite refusal", err)
		}
		effs := ndEffects(t, h, "c2")
		if len(effs) != 1 || effs[0].State != core.EffectSkipped || len(ndClaims(t, h, ndTunnel)) != 1 || !ndOn(t, h, "web") {
			t.Fatalf("effects %+v, claims %+v; want one skipped effect, the claim kept and the connector on web", effs, ndClaims(t, h, ndTunnel))
		}
		if _, err := ndApply(t, h, "c3"); err != nil {
			t.Fatal(err)
		}
		if ndOn(t, h, "web") || len(ndClaims(t, h, ndTunnel)) != 0 {
			t.Errorf("after the repeat: on web %t, claims %+v; want it off web with no claim", ndOn(t, h, "web"), ndClaims(t, h, ndTunnel))
		}
	})
	t.Run("an unknown disconnect failure stays intended", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		h.docker.Fail("DisconnectNetwork", errors.New("timeout"))
		_, err := ndApply(t, h, "c2")
		effs := ndEffects(t, h, "c2")
		if err == nil || core.IsDefinite(err) || len(effs) != 1 || effs[0].State != core.EffectIntended || len(ndClaims(t, h, ndTunnel)) != 1 {
			t.Errorf("error %v, effects %+v; want a failure that is not Definite, one intended effect and the claim kept", err, effs)
		}
	})
	t.Run("the Engine down", func(t *testing.T) {
		h := newND(t)
		ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
		h.docker.Fail("InspectContainer", dockerapi.ErrUnavailable)
		_, err := ndApply(t, h, "c2")
		if err == nil || errcode.From(err).Code != errcode.DockerEngineUnavailable {
			t.Errorf("error %v, want docker.engine_unavailable", err)
		}
		if got := ndClaims(t, h, ndTunnel); len(got) != 1 || len(ndEffects(t, h, "c2")) != 0 {
			t.Errorf("claims %+v, effects %+v; want the claim kept and nothing journaled", got, ndEffects(t, h, "c2"))
		}
	})
}

func TestNdVerify(t *testing.T) {
	ctx := context.Background()
	h := newND(t)
	ndAttach(t, h, "c1", "web", ndClaimant)
	ndAttach(t, h, "c1b", "web2", ndClaimant)
	verify := func(mutate ...func(*NetworkDetachInput)) error {
		return h.cf().networkDetach().Handler.Verify(ctx, h.env("c2"), ndInput(mutate...), ndJSON(t, NetworkAttachment{}))
	}
	if verify() == nil || verify(ndAllNetworks) == nil {
		t.Error("Verify passed before the detach")
	}
	if _, err := ndApply(t, h, "c2"); err != nil {
		t.Fatal(err)
	}
	if err := verify(); err != nil {
		t.Errorf("Verify of web after its detach: %v", err)
	}
	if verify(ndAllNetworks) == nil {
		t.Error("Verify of every network passed while the claim on web2 is left")
	}
	ndAttach(t, h, "c4", "web", ndClaimant2)
	if err := verify(); err != nil {
		t.Errorf("Verify failed on a claim of another claimant: %v", err)
	}
	if _, err := ndApply(t, h, "c5", ndAllNetworks); err != nil {
		t.Fatal(err)
	}
	if err := verify(ndAllNetworks); err != nil {
		t.Errorf("Verify of every network after its detach: %v", err)
	}
}

func TestNdUndo(t *testing.T) {
	t.Run("a disconnect and a claim come back", func(t *testing.T) {
		h := newND(t)
		ndAttach(t, h, "c1", "web", ndClaimant)
		original := ndClaims(t, h, ndTunnel)
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		effs := ndEffects(t, h, "c2")
		for _, e := range effs {
			if got := ndObserved(t, h, "c2", e); got != string(e.Post.State) {
				t.Errorf("%s: observed %s before the undo, journaled %s", e.Resource, got, e.Post.State)
			}
		}
		mark := len(h.docker.calls())
		results := ndUndo(t, h, "c2")
		endpoint := "cloudflare.endpoint:" + ndTunnel + "|web"
		if got := results[endpoint]; got.Resource != endpoint {
			t.Errorf("inverse of the endpoint names %q, want %s", got.Resource, endpoint)
		}
		if got := ndMutatedAfter(h, mark); !slices.Equal(got, []string{"ConnectNetwork"}) || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want one ConnectNetwork and the connector back on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); !slices.Equal(got, original) {
			t.Errorf("claims %+v, want the original %+v back", got, original)
		}
		for _, e := range effs {
			if e.Kind != core.EffectDelete {
				continue
			}
			e.State = core.EffectUndone
			if got := ndObserved(t, h, "c2", e); got != string(e.Before.State) {
				t.Errorf("%s: observed %s after the undo, want %s", e.Resource, got, e.Before.State)
			}
		}
	})
	t.Run("a preexisting claim comes back without an Engine call", func(t *testing.T) {
		h := newND(t)
		ndJoin(t, h, "web", "172.20.0.9")
		ndAttach(t, h, "c1", "web", ndClaimant)
		original := ndClaims(t, h, ndTunnel)
		if len(original) != 1 || !original[0].Preexisting {
			t.Fatalf("claims %+v, want one preexisting claim", original)
		}
		if _, err := ndApply(t, h, "c2"); err != nil {
			t.Fatal(err)
		}
		mark := len(h.docker.calls())
		ndUndo(t, h, "c2")
		if got := ndMutatedAfter(h, mark); len(got) != 0 || !ndOn(t, h, "web") {
			t.Errorf("Engine calls %v, on web %t; want none and the connector still on web", got, ndOn(t, h, "web"))
		}
		if got := ndClaims(t, h, ndTunnel); !slices.Equal(got, original) {
			t.Errorf("claims %+v, want the original %+v back", got, original)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w6b-tmp/13 && export TMPDIR=/root/.cache/w6b-tmp/13 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestNd' ./internal/modules/cloudflare/
```

Expected: a build failure naming `h.cf().networkDetach undefined`. Quote it in the report.

- [ ] **Step 3: Implement**

Create `connector_network_detach.go` with the behaviour of the decisions above, structured as the detach half of `internal/modules/npm/network_claims.go` is (read it first; Task 8's `connector_network.go` shows the cloudflare names for the same helpers):

1. `func (c cf) networkDetach() core.ActionDef` returns `core.DefineMutating[NetworkDetachInput, NetworkAttachment]` with the `Spec` of the decisions and `networkDetachHandler{networkClaims{c: c}}` as its handler. `type networkDetachHandler struct{ networkClaims }` declares `Plan`, `Apply` and `Verify` only: `Observe` and `Inverse` are promoted from `networkClaims`, so the action is an `Undoer` and Task 8's `connector_network.go` is not edited.
2. A `detachCheck` method used by Plan and Apply, which makes every decision and changes nothing. It validates first (the field errors of the decisions, as a Definite `request.invalid_input` `*errcode.Error` with `Detail` the messages joined by `"; "` and `Fields` the field errors), then selects the claims, then reads the Engine only when a claim was found. It returns the connector container's id (empty when absent) and one step per claim: the claim, the other claimants, the network id (empty when the network is gone), whether the connector is attached, whether to disconnect, and the data of the hash. A database read failure is returned Definite; an Engine read failure as the decisions say.
3. Plan, Apply and Verify as the decisions describe. The hash is `sha256` over `json.Marshal` of the slice of per-claim documents, a non-nil empty slice when no claim was found, hex encoded.
4. Apply journals through `c.journal` with the effect shapes of the decisions. For the disconnect's before-state call `h.Observe(ctx, env, core.Effect{Kind: core.EffectModify, Resource: <endpoint resource>, State: core.EffectIntended})` before the call and use its `Current`. Wrap errors with the `cloudflare:` prefix. No message, diff line or effect carries a token; this action never reads one.
5. Do not edit `connector_network.go`, `cloudflare.go` or any other file, and do not register the action in `Manifest()` (Task 14 does).

- [ ] **Step 4: Run the tests to verify they pass, then the task's checks**

```bash
export TMPDIR=/root/.cache/w6b-tmp/13 GOTOOLCHAIN=local
timeout 300 go test -count=1 -run 'TestNd' ./internal/modules/cloudflare/
gofmt -l internal/modules/cloudflare/
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./...
timeout 600 go test -race -count=1 ./internal/modules/cloudflare/...
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
rm -rf /root/.cache/w6b-tmp/13 && go clean -testcache
```

Expected: all pass, `gofmt -l` prints nothing, `0 issues.`, no output from vet and tidy. Record the real output.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/connector_network_detach.go internal/modules/cloudflare/connector_network_detach_test.go
git commit -m "feat(cloudflare): add network.detach"
```

### Task 14: cloudflare: register the actions and schemas

**Files:**
- Modify: `internal/modules/cloudflare/cloudflare.go` (`actions()`, and the doc comments of `Manifest` and `actions`)
- Test: `internal/modules/cloudflare/register_test.go`
- Create (generated by `make schemas`, committed): `schemas/cloudflare.connector.run/v1.json`, `schemas/cloudflare.connector.stop/v1.json`, `schemas/cloudflare.connector.list/v1.json`, `schemas/cloudflare.network.attach/v1.json`, `schemas/cloudflare.network.detach/v1.json`, `schemas/cloudflare.connector.deploy/v1.json`, `schemas/cloudflare.expose.container/v1.json`, `schemas/cloudflare.expose.remove/v1.json`
- Read only: `internal/modules/modules.go` (it already lists `cloudflare.Module()`), `internal/modules/modules_test.go` (`TestSnapshotsCurrent`, `TestReleasedSnapshotsCompatible`, `TestServerRegistryHasTheSameShape`), `Makefile` (target `schemas` runs `go run ./internal/core/schema/schemagen -out schemas`), `internal/modules/npm/register_test.go` (the pattern this task follows)

**Interfaces:**
- Consumes (Contracts "Registration" and "Agent actions"): the eight constructors `c.connectorRun()` (Task 6), `c.connectorStop()` (Task 12), `c.connectorList()` (Task 7), `c.networkAttach()` (Task 8), `c.networkDetach()` (Task 13), `c.connectorDeploy()` (Task 9), `c.exposeContainer()` and `c.exposeRemove()` (Task 10); the wire types of Contracts "Wire types" (Task 3); the `tokenDir` constant and `agentMigrations()` (Task 3); the existing test helpers `newCFHost` (`host_test.go`), and the store functions `insertTunnel` and `insertHostname`.
- Produces: `Manifest().Actions` holds 19 actions, the eleven of wave 3c followed by the eight of this plan in the Contracts order; eight committed snapshots under `schemas/`. Task 15's harness calls these actions by name through the controller API.

**Items:** 34, 35

**Depends on:** Task 6, Task 7, Task 8, Task 9, Task 10, Task 12, Task 13

**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

This task is the plan's one registration point: until it runs, the eight actions are not in `Manifest()`, so `modules.TestSnapshotsCurrent` stayed green. It also holds the only test that sees the agent actions and the composites registered together, which is where a composite step that names a wrong action or a wrong input member shows.

- [ ] **Step 1: Extend the registration tests in `internal/modules/cloudflare/register_test.go`**

Make these seven edits. They all fail on the current tree: the first four because the eight actions are not declared (`cloudflare.connector.run is not declared`), the fifth because the manifest has no agent migrations and no owned path, the last two for the same missing declarations.

1. Replace the import block with:

```go
import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"maps"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/ext"
	"github.com/darkraise/darkpanel/internal/core/schema"
)
```

2. Delete the variable `regOtherWave` and its comment, and replace `regDefs` with:

```go
// regDefs returns the manifest's actions by name.
func regDefs(t *testing.T) map[string]core.ActionDef {
	t.Helper()
	defs := map[string]core.ActionDef{}
	for _, a := range Module().Manifest().Actions {
		if _, dup := defs[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		defs[a.Spec.Name] = a
	}
	return defs
}
```

3. Replace `TestRegActions` with:

```go
// TestRegActions: the manifest declares exactly the nineteen actions of the
// account, tunnel, hostname, connector and exposure surface at version 1, each
// on its side with the class, lock key, deadline, unwind policy and lifecycle
// handlers of the table.
func TestRegActions(t *testing.T) {
	tests := []struct {
		name          string
		class         core.Class
		agent         bool
		compensatedBy string
		converges     bool
		internal      bool
		undo, recover bool
		apply         time.Duration // the ApplyTimeout of a long-running action; 0 for none
		unwind        core.Unwind
	}{
		{name: "cloudflare.account.connect", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.account.disconnect", class: core.ClassIrreversible},
		{name: "cloudflare.account.get", class: core.ClassRead},
		{name: "cloudflare.tunnel.create", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.tunnel.delete", class: core.ClassIrreversible},
		{name: "cloudflare.tunnel.list", class: core.ClassRead},
		{name: "cloudflare.tunnel.token", class: core.ClassRead, internal: true},
		{name: "cloudflare.tunnel.adopt", class: core.ClassCompensable, undo: true},
		{name: "cloudflare.hostname.add", class: core.ClassCompensable, undo: true, recover: true},
		{name: "cloudflare.hostname.remove", class: core.ClassCompensable, compensatedBy: "cloudflare.hostname.add", converges: true, recover: true},
		{name: "cloudflare.hostname.list", class: core.ClassRead},
		{name: "cloudflare.connector.run", class: core.ClassCompensable, agent: true, undo: true, apply: 10 * time.Minute},
		{name: "cloudflare.connector.stop", class: core.ClassCompensable, agent: true, compensatedBy: "cloudflare.connector.run", converges: true},
		{name: "cloudflare.connector.list", class: core.ClassRead, agent: true},
		{name: "cloudflare.network.attach", class: core.ClassCompensable, agent: true, undo: true},
		{name: "cloudflare.network.detach", class: core.ClassCompensable, agent: true, undo: true, converges: true},
		{name: "cloudflare.connector.deploy", class: core.ClassComposite, unwind: core.UnwindReverse},
		{name: "cloudflare.expose.container", class: core.ClassComposite, unwind: core.UnwindReverse},
		{name: "cloudflare.expose.remove", class: core.ClassComposite, unwind: core.UnwindForward},
	}
	defs := regDefs(t)
	var want []string
	for _, tt := range tests {
		want = append(want, tt.name)
		def, ok := defs[tt.name]
		if !ok {
			t.Errorf("%s is not declared", tt.name)
			continue
		}
		s, h := def.Spec, def.Handler
		side := core.SideController
		if tt.agent {
			side = core.SideAgent
		}
		if s.Version != 1 || s.Side != side || s.Class != tt.class || s.Unwind != tt.unwind {
			t.Errorf("%s: version %d side %s class %s unwind %q, want 1 %s %s %q", tt.name, s.Version, s.Side, s.Class, s.Unwind, side, tt.class, tt.unwind)
		}
		wantLocks := []string{"cloudflare"}
		switch tt.class {
		case core.ClassRead:
			wantLocks = nil
		case core.ClassComposite:
			wantLocks = []string{"composite:cloudflare"}
		}
		if !slices.Equal(s.LockKeys, wantLocks) {
			t.Errorf("%s: lock keys %v, want %v", tt.name, s.LockKeys, wantLocks)
		}
		if s.LongRunning != (tt.apply > 0) || s.ApplyTimeout != tt.apply {
			t.Errorf("%s: long-running %t apply timeout %s, want %t %s", tt.name, s.LongRunning, s.ApplyTimeout, tt.apply > 0, tt.apply)
		}
		if s.CompensatedBy != tt.compensatedBy || s.Converges != tt.converges || s.Internal != tt.internal {
			t.Errorf("%s: compensatedBy %q converges %t internal %t, want %q %t %t",
				tt.name, s.CompensatedBy, s.Converges, s.Internal, tt.compensatedBy, tt.converges, tt.internal)
		}
		if (h.Inverse != nil) != tt.undo || (h.Recover != nil) != tt.recover {
			t.Errorf("%s: undo handler %t recovery handler %t, want %t %t", tt.name, h.Inverse != nil, h.Recover != nil, tt.undo, tt.recover)
		}
		// account.disconnect journals no effect, and an Observer is required only of an action that journals; a
		// composite is run by the engine and has none.
		journals := tt.class != core.ClassComposite && tt.name != "cloudflare.account.disconnect"
		if tt.class != core.ClassRead && (h.Observe != nil) != journals {
			t.Errorf("%s: Observe handler %t, want %t", tt.name, h.Observe != nil, journals)
		}
		if tt.class == core.ClassComposite && (h.Steps == nil || h.Output == nil || h.Plan != nil || h.Apply != nil) {
			t.Errorf("%s: a composite has Steps and Output handlers and no Plan or Apply", tt.name)
		}
	}
	slices.Sort(want)
	if got := slices.Sorted(maps.Keys(defs)); !slices.Equal(got, want) {
		t.Errorf("declared actions %v, want %v", got, want)
	}
}

// regScopes are the scopes of the connector surface: the read needs read, the
// agent mutations operate and every composite admin.
var regScopes = map[string]core.Scope{
	"cloudflare.connector.run":       core.ScopeOperate,
	"cloudflare.connector.stop":      core.ScopeOperate,
	"cloudflare.connector.list":      core.ScopeRead,
	"cloudflare.network.attach":      core.ScopeOperate,
	"cloudflare.network.detach":      core.ScopeOperate,
	"cloudflare.connector.deploy":    core.ScopeAdmin,
	"cloudflare.expose.container":    core.ScopeAdmin,
	"cloudflare.expose.remove":       core.ScopeAdmin,
}

// TestRegScopes: no connector action is registered with a scope other than the
// one its class and side give it.
func TestRegScopes(t *testing.T) {
	defs := regDefs(t)
	for _, name := range slices.Sorted(maps.Keys(regScopes)) {
		def, ok := defs[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		if got := core.RequiredScope(def.Spec); got != regScopes[name] {
			t.Errorf("%s needs scope %s, want %s", name, got, regScopes[name])
		}
	}
}
```

4. In `TestRegInputs`, append these rows to the `tests` table, after the `cloudflare.hostname.list` rows (they use the constant `regTunnelID`, declared in edit 7):

```go
		{"cloudflare.connector.run", `{"tunnel":"` + regTunnelID + `","token":"x"}`, ""},
		{"cloudflare.connector.run", `{"tunnel":"` + regTunnelID + `"}`, "/token"},
		{"cloudflare.connector.run", `{"token":"x"}`, "/tunnel"},
		{"cloudflare.connector.stop", `{"tunnel":"` + regTunnelID + `"}`, ""},
		{"cloudflare.connector.stop", `{}`, "/tunnel"},
		{"cloudflare.connector.list", `{}`, ""},
		{"cloudflare.connector.list", `{"x":1}`, "/x"},
		{"cloudflare.network.attach", `{"tunnel":"t","network":"appnet","claimant":"expose:app.example.com","target":"web","address":"10.9.0.2"}`, ""},
		{"cloudflare.network.attach", `{"tunnel":"t","network":"appnet"}`, "/claimant"},
		{"cloudflare.network.attach", `{"tunnel":"t","network":1,"claimant":"c"}`, "/network"},
		{"cloudflare.network.detach", `{"tunnel":"t","network":"","claimant":"c"}`, ""},
		{"cloudflare.network.detach", `{"tunnel":"t","claimant":"c"}`, "/network"},
		{"cloudflare.connector.deploy", `{"hostId":"h1"}`, ""},
		{"cloudflare.connector.deploy", `{"hostId":"h1","tunnel":"edge"}`, ""},
		{"cloudflare.connector.deploy", `{}`, "/hostId"},
		{"cloudflare.connector.deploy", `{"hostId":"h1","tunnel":1}`, "/tunnel"},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":8080,"network":"appnet","hostname":"app.example.com"}`, ""},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":"8080","network":"appnet","hostname":"app.example.com"}`, "/port"},
		{"cloudflare.expose.container", `{"hostId":"h1","tunnel":"edge","container":"web","port":0,"network":"appnet","hostname":"app.example.com"}`, "/port"},
		{"cloudflare.expose.container", `{"tunnel":"edge","container":"web","port":8080,"network":"appnet","hostname":"app.example.com"}`, "/hostId"},
		{"cloudflare.expose.remove", `{"hostId":"h1","hostname":"app.example.com"}`, ""},
		{"cloudflare.expose.remove", `{"hostId":"h1"}`, "/hostname"},
```

5. Replace `TestRegSecretFields` with the two tests below. The first keeps the old assertions and adds the connector token; the second says that nothing else of the connector surface is secret.

```go
// TestRegSecretFields: the API token a connect takes, the tunnel token a read
// returns and the tunnel token a connector run takes are marked as secrets in
// their schemas, so the snapshots, the audit log and the CLI never show them.
func TestRegSecretFields(t *testing.T) {
	defs := regDefs(t)
	connect, ok := defs["cloudflare.account.connect"]
	if !ok {
		t.Fatal("cloudflare.account.connect is not declared")
	}
	token, ok := defs["cloudflare.tunnel.token"]
	if !ok {
		t.Fatal("cloudflare.tunnel.token is not declared")
	}
	run, ok := defs["cloudflare.connector.run"]
	if !ok {
		t.Fatal("cloudflare.connector.run is not declared")
	}
	in, err := schema.Input(connect.InputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, in, "apiToken") || regSecretProperty(t, in, "accountId") {
		t.Errorf("cloudflare.account.connect input: want apiToken alone marked secret: %s", in)
	}
	out, err := schema.Output(token.OutputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, out, "token") {
		t.Errorf("cloudflare.tunnel.token output: want token marked secret: %s", out)
	}
	in, err = schema.Input(run.InputType)
	if err != nil {
		t.Fatal(err)
	}
	if !regSecretProperty(t, in, "token") || regSecretProperty(t, in, "tunnel") {
		t.Errorf("cloudflare.connector.run input: want token alone marked secret: %s", in)
	}
}

// TestRegNoOtherConnectorSecrets: of the connector surface only the input of
// connector.run holds a secret; no output and no composite input does.
func TestRegNoOtherConnectorSecrets(t *testing.T) {
	defs := regDefs(t)
	for _, name := range slices.Sorted(maps.Keys(regScopes)) {
		def, ok := defs[name]
		if !ok {
			t.Errorf("%s is not declared", name)
			continue
		}
		in, err := schema.Input(def.InputType)
		if err != nil {
			t.Errorf("%s input: %v", name, err)
		}
		out, err := schema.Output(def.OutputType)
		if err != nil {
			t.Errorf("%s output: %v", name, err)
		}
		if secret := bytes.Contains(in, []byte("x-secret")); secret != (name == "cloudflare.connector.run") {
			t.Errorf("%s: its input schema marks a field secret: %t", name, secret)
		}
		if bytes.Contains(out, []byte("x-secret")) {
			t.Errorf("%s: its output schema marks a field secret", name)
		}
	}
}
```

6. Replace `TestRegManifest` with the test below. It does not depend on what an earlier task left in the old body: it states the whole final manifest.

```go
// TestRegManifest: the module is enabled by default, owns its controller
// tables and its agent tables through its migrations, owns the one managed
// directory of the tunnel token files, declares no resource, and the registry
// accepts the whole manifest.
func TestRegManifest(t *testing.T) {
	m := Module().Manifest()
	if m.ID != ID || m.Version != 1 || !m.DefaultEnabled {
		t.Errorf("manifest id %q version %d default-enabled %t, want %q 1 true", m.ID, m.Version, m.DefaultEnabled, ID)
	}
	if len(m.Resources) != 0 || len(m.Preflight) != 0 {
		t.Errorf("manifest declares %d resources and %d preflight checks, want none", len(m.Resources), len(m.Preflight))
	}
	if len(m.OwnedPaths) != 1 {
		t.Fatalf("manifest owns %d paths %+v, want the one managed directory of the token files", len(m.OwnedPaths), m.OwnedPaths)
	}
	owned := m.OwnedPaths[0]
	if owned.Path != "/var/lib/darkpanel/agent/cloudflare-tokens" || owned.Kind != core.OwnedManagedDir {
		t.Errorf("owned path %s of kind %s, want /var/lib/darkpanel/agent/cloudflare-tokens of kind %s", owned.Path, owned.Kind, core.OwnedManagedDir)
	}
	pattern, err := regexp.Compile(owned.Pattern)
	if err != nil {
		t.Fatalf("the owned path's file-name pattern %q: %v", owned.Pattern, err)
	}
	for name, accepted := range map[string]bool{
		regTunnelID + ".token":            true,
		strings.ToUpper(regTunnelID) + ".token": false,
		regTunnelID + ".token.bak":        false,
		"../" + regTunnelID + ".token":    false,
		"notes.txt":                       false,
	} {
		if pattern.MatchString(name) != accepted {
			t.Errorf("file name %q: pattern match %t, want %t", name, !accepted, accepted)
		}
	}
	if len(m.Migrations) != 2 || m.Migrations[core.SideController] == nil || m.Migrations[core.SideAgent] == nil {
		t.Fatalf("migrations %v, want the controller's and the agent's", m.Migrations)
	}
	if b, err := fs.ReadFile(m.Migrations[core.SideController], "0001_cloudflare.sql"); err != nil || !strings.Contains(string(b), "cloudflare_hostnames") {
		t.Errorf("controller 0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_hostnames", b, err)
	}
	b, err := fs.ReadFile(m.Migrations[core.SideAgent], "0001_cloudflare.sql")
	if err != nil || !strings.Contains(string(b), "cloudflare_connectors") || !strings.Contains(string(b), "cloudflare_attachments") {
		t.Errorf("agent 0001_cloudflare.sql = %q, %v, want the migration that creates cloudflare_connectors and cloudflare_attachments", b, err)
	}
	if _, err := core.NewRegistry(Module()); err != nil {
		t.Errorf("core.NewRegistry: %v", err)
	}
}
```

7. Append these to the end of the file: the types test, the helpers and the composite steps test.

```go
// TestRegConnectorTypes: each connector action is registered with its own
// input and output type, and its schema snapshot can be built.
func TestRegConnectorTypes(t *testing.T) {
	defs := regDefs(t)
	for _, tt := range []struct {
		name    string
		in, out reflect.Type
	}{
		{"cloudflare.connector.run", reflect.TypeFor[ConnectorRunInput](), reflect.TypeFor[Connector]()},
		{"cloudflare.connector.stop", reflect.TypeFor[ConnectorStopInput](), reflect.TypeFor[ConnectorStopped]()},
		{"cloudflare.connector.list", reflect.TypeFor[ConnectorListInput](), reflect.TypeFor[ConnectorList]()},
		{"cloudflare.network.attach", reflect.TypeFor[NetworkAttachInput](), reflect.TypeFor[NetworkAttachment]()},
		{"cloudflare.network.detach", reflect.TypeFor[NetworkDetachInput](), reflect.TypeFor[NetworkAttachment]()},
		{"cloudflare.connector.deploy", reflect.TypeFor[ConnectorDeployInput](), reflect.TypeFor[ConnectorDeployOutput]()},
		{"cloudflare.expose.container", reflect.TypeFor[ExposeInput](), reflect.TypeFor[ExposeOutput]()},
		{"cloudflare.expose.remove", reflect.TypeFor[ExposeRemoveInput](), reflect.TypeFor[ExposeRemoveOutput]()},
	} {
		def, ok := defs[tt.name]
		if !ok {
			t.Errorf("%s is not declared", tt.name)
			continue
		}
		if def.InputType != tt.in || def.OutputType != tt.out {
			t.Errorf("%s: input %v output %v, want %v %v", tt.name, def.InputType, def.OutputType, tt.in, tt.out)
		}
		if _, err := schema.ActionSnapshot(def); err != nil {
			t.Errorf("%s: %v", def.Ref(), err)
		}
	}
}

// regTunnelID is the id of the tunnel row the composite tests seed.
const regTunnelID = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"

var regRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// regGeneric decodes the JSON form of v into maps, slices and scalars.
func regGeneric(t *testing.T, v any) any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var out any
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

// regResolve resolves the references of a step template against the
// composite's input and the outputs of the steps it follows, as the engine
// does: a string that is exactly one reference becomes the referenced value
// with its JSON type, a reference inside a longer string is replaced by the
// value's text, and a substituted value is never read as a template again. A
// reference to a member that does not exist fails the test, as it fails the plan.
func regResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	input := regGeneric(t, in)
	steps := map[string]any{}
	for id, o := range outputs {
		steps[id] = map[string]any{"output": o}
	}
	lookup := func(ref string) any {
		parts := strings.Split(ref, ".")
		var cur any
		switch parts[0] {
		case "input":
			cur = input
		case "steps":
			cur = steps
		default:
			t.Errorf("reference ${%s} is neither an input nor a step reference", ref)
			return nil
		}
		for _, p := range parts[1:] {
			m, ok := cur.(map[string]any)
			if !ok {
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
			next, ok := m[p]
			if !ok {
				t.Errorf("reference ${%s}: there is no member %q", ref, p)
				return nil
			}
			cur = next
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := regRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return regRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
		case map[string]any:
			for k, e := range x {
				x[k] = walk(e)
			}
		case []any:
			for i, e := range x {
				x[i] = walk(e)
			}
		}
		return v
	}
	out, err := json.Marshal(walk(regGeneric(t, tmpl)))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// regSample is the output of def's output type with every member at its zero
// value and the members of set replaced. A member of set that the type does
// not have fails the test, which is how a reference to a missing output
// member shows.
func regSample(t *testing.T, def core.ActionDef, set map[string]any) map[string]any {
	t.Helper()
	m, ok := regGeneric(t, reflect.New(def.OutputType).Elem().Interface()).(map[string]any)
	if !ok {
		t.Fatalf("the output of %s is not a JSON object", def.Spec.Name)
	}
	for k, v := range set {
		if _, has := m[k]; !has {
			t.Errorf("the output of %s has no member %q", def.Spec.Name, k)
		}
		m[k] = v
	}
	return m
}

// TestRegCompositeSteps: every step a connector or exposure composite returns
// names a registered action, and obeys the rules the engine checks at plan
// time: a controller action takes no host and an agent action the composite's
// host, no step is irreversible or a composite, every compensable step of a
// reverse composite has an undo handler, every step of a forward composite
// that is not a read converges, each step follows only steps before it and
// references only steps it follows, and each step's input, once its
// references are resolved, is the one the Contracts give and is valid for its
// action's schema.
func TestRegCompositeSteps(t *testing.T) {
	ctx := context.Background()
	h := newCFHost(t)
	now := h.clock.Now()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		if err := insertTunnel(ctx, tx, tunnelRow{ID: regTunnelID, Name: "edge", ChangeID: "c0", CreatedAt: now}); err != nil {
			return err
		}
		return insertHostname(ctx, tx, hostnameRow{Hostname: "app.example.com", TunnelID: regTunnelID, Service: "http://web:8080",
			ZoneID: "z1", RecordID: "r1", Phase: "active", ChangeID: "c0", UpdatedAt: now})
	})
	if err != nil {
		t.Fatal(err)
	}
	reg, err := core.NewRegistry(Module())
	if err != nil {
		t.Fatal(err)
	}
	validator := schema.NewValidator()
	samples := map[string]map[string]any{
		"cloudflare.tunnel.create": {"id": regTunnelID},
		"cloudflare.tunnel.token":  {"token": "sample-token"},
	}
	id := regTunnelID
	tests := []struct {
		name, action string
		input        any
		forward      bool
		steps        []string
		inputs       map[string]string // the resolved input of each step
	}{
		{"deploy creating a tunnel", "cloudflare.connector.deploy", ConnectorDeployInput{HostID: "h1"}, false,
			[]string{"tunnel", "token", "run"},
			map[string]string{"tunnel": `{}`, "token": `{"tunnel":"` + id + `"}`, "run": `{"tunnel":"` + id + `","token":"sample-token"}`}},
		{"deploy on a named tunnel", "cloudflare.connector.deploy", ConnectorDeployInput{HostID: "h1", Tunnel: "edge"}, false,
			[]string{"token", "run"},
			map[string]string{"token": `{"tunnel":"` + id + `"}`, "run": `{"tunnel":"` + id + `","token":"sample-token"}`}},
		{"expose a container", "cloudflare.expose.container",
			ExposeInput{HostID: "h1", Tunnel: "edge", Container: "web", Port: 8080, Network: "appnet", Hostname: "app.example.com"}, false,
			[]string{"network", "hostname"},
			map[string]string{
				"network":  `{"tunnel":"` + id + `","network":"appnet","claimant":"expose:app.example.com","target":"web"}`,
				"hostname": `{"tunnel":"` + id + `","hostname":"app.example.com","service":"http://web:8080"}`,
			}},
		{"remove an exposure", "cloudflare.expose.remove", ExposeRemoveInput{HostID: "h1", Hostname: "app.example.com"}, true,
			[]string{"hostname", "network"},
			map[string]string{
				"hostname": `{"hostname":"app.example.com"}`,
				"network":  `{"tunnel":"` + id + `","network":"","claimant":"expose:app.example.com"}`,
			}},
	}
	for _, c := range tests {
		t.Run(c.name, func(t *testing.T) {
			def, ok := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			if !ok {
				t.Fatalf("%s is not registered", c.action)
			}
			if (def.Spec.Unwind == core.UnwindForward) != c.forward {
				t.Errorf("%s unwinds %q, want forward %t", c.action, def.Spec.Unwind, c.forward)
			}
			raw, err := json.Marshal(c.input)
			if err != nil {
				t.Fatal(err)
			}
			steps, err := def.Handler.Steps(ctx, h.env(""), raw)
			if err != nil {
				t.Fatal(err)
			}
			ids := map[string]int{}
			outputs := map[string]any{}
			var got []string
			for i, s := range steps {
				got = append(got, s.ID)
				if _, dup := ids[s.ID]; dup {
					t.Errorf("step id %s appears twice", s.ID)
				}
				ids[s.ID] = i
				child, ok := reg.Action(s.Action)
				if !ok {
					t.Errorf("step %s: %s is not registered", s.ID, s.Action)
					continue
				}
				outputs[s.ID] = regSample(t, child, samples[s.Action.Name])
				if child.Spec.Side == core.SideController && s.Host != "" || child.Spec.Side == core.SideAgent && s.Host != "h1" {
					t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, child.Spec.Side, s.Host)
				}
				switch child.Spec.Class {
				case core.ClassIrreversible, core.ClassComposite:
					t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, child.Spec.Class)
				case core.ClassCompensable:
					if !c.forward && child.Handler.Inverse == nil {
						t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
					}
				}
				if c.forward && child.Spec.Class != core.ClassRead && !child.Spec.Converges {
					t.Errorf("step %s: %s does not converge, which a forward composite needs of every step that is not a read", s.ID, s.Action)
				}
				for _, a := range s.After {
					if before, ok := ids[a]; !ok || before >= i {
						t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
					}
				}
				if i > 0 && len(s.After) == 0 {
					t.Errorf("step %s follows no step", s.ID)
				}
				// Only the outputs of followed steps are offered, so a reference to any other step fails in regResolve.
				followed := map[string]any{}
				for _, a := range s.After {
					if o, ok := outputs[a]; ok {
						followed[a] = o
					}
				}
				input := regResolve(t, s.Input, c.input, followed)
				if strings.Contains(string(input), "${") {
					t.Errorf("step %s: input %s holds a reference left unresolved", s.ID, s.Input)
					continue
				}
				if want, ok := c.inputs[s.ID]; !ok {
					t.Errorf("step %s has no expected input in the test table", s.ID)
				} else if !reflect.DeepEqual(regGeneric(t, input), regGeneric(t, json.RawMessage(want))) {
					t.Errorf("step %s: resolved input %s, want %s", s.ID, input, want)
				}
				if err := validator.ValidateInput(child, input); err != nil {
					t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
				}
			}
			if !slices.Equal(got, c.steps) {
				t.Errorf("steps %v, want %v", got, c.steps)
			}
		})
	}
}
```

Run `gofmt -w internal/modules/cloudflare/register_test.go` after the edits; the alignment of the `regScopes` map and of the owned-path name map above is gofmt's to settle.

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w6b-tmp/14 && export TMPDIR=/root/.cache/w6b-tmp/14 GOTOOLCHAIN=local && timeout 600 go test -count=1 -run 'TestReg' ./internal/modules/cloudflare/`
Expected: FAIL. `TestRegActions` reports `cloudflare.connector.run is not declared` and the same for the other seven; `TestRegScopes`, `TestRegConnectorTypes` and `TestRegNoOtherConnectorSecrets` report the same; `TestRegSecretFields` stops with `cloudflare.connector.run is not declared`; `TestRegInputs` reports the eight names not declared; `TestRegCompositeSteps` stops with `cloudflare.connector.deploy is not registered`. `TestRegManifest` may already pass, because Task 3 added the owned path and the agent migrations; it is in this step so that the manifest is pinned whole. Quote the first lines of the output in the report.

- [ ] **Step 3: Implement**

1. In `internal/modules/cloudflare/cloudflare.go`, `func (c cf) actions() []core.ActionDef`: after `c.hostnameList()` append, one per line and in exactly this order, `c.connectorRun()`, `c.connectorStop()`, `c.connectorList()`, `c.networkAttach()`, `c.networkDetach()`, `c.connectorDeploy()`, `c.exposeContainer()`, `c.exposeRemove()`. Change nothing else in the list.
2. Rewrite the doc comments of `Manifest` and `actions` so that they state what the manifest now holds, in full sentences and without naming a plan, task or register row: the controller-side account, tunnel and hostname actions, the agent-side connector actions (run, stop, list) and attachment claims (attach, detach), the controller composites that deploy a connector and expose a container through a tunnel, the controller and agent migrations, and the managed directory of the tunnel token files. Do not change `Manifest`'s fields: Task 3 already added `OwnedPaths` and `Migrations[core.SideAgent]`.
3. Run the schema generator with the Makefile's own target, which writes one `schemas/<action name>/v1.json` per registered action and removes nothing: `timeout 600 make schemas`. Then run `git status --short schemas`: it must list exactly the eight new directories of the **Files** block as untracked and no modified file. A modified snapshot of an older action means an earlier task changed a wire type of a released action; stop and report it instead of committing it.
4. Open `schemas/cloudflare.connector.run/v1.json` and check that `token` carries `"x-secret": true` in the input and that the output (`Connector`) has none.

Nothing in this task changes `internal/modules/modules.go`, the CLI or the web: every registered action already gets its generated CLI command, and the module is already in `modules.All()`.

- [ ] **Step 4: Run the tests to verify they pass**

Run:
```bash
export TMPDIR=/root/.cache/w6b-tmp/14 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestReg' ./internal/modules/cloudflare/
timeout 600 go test -count=1 -run 'TestSnapshotsCurrent|TestReleasedSnapshotsCompatible|TestServerRegistryHasTheSameShape' ./internal/modules/
rg -l 'x-secret' schemas | rg cloudflare
```
Expected: `ok` for both packages; the `rg` lists exactly `schemas/cloudflare.account.connect/v1.json`, `schemas/cloudflare.connector.run/v1.json` and `schemas/cloudflare.tunnel.token/v1.json`.

Before step 3.3 the second command is expected to fail with `schemas/cloudflare.connector.run/v1.json is missing; run make schemas` (and seven more): that is the red state of the generated files, and the report quotes it.

Then the task's closing checks, each with its real output recorded: `timeout 600 go build ./...`; `timeout 300 go vet ./...` (no output); `timeout 1500 go test -count=1 ./...` (every line `ok` or `?`; if a test outside `internal/modules/cloudflare` fails because it lists the registered actions, report it, do not edit a frozen file); `timeout 900 go test -race -count=1 ./internal/modules/cloudflare/`; `timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `timeout 300 go mod tidy -diff` (no output). Remove `/root/.cache/w6b-tmp/14` and run `go clean -testcache` when done.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/cloudflare.go internal/modules/cloudflare/register_test.go schemas/cloudflare.connector.run schemas/cloudflare.connector.stop schemas/cloudflare.connector.list schemas/cloudflare.network.attach schemas/cloudflare.network.detach schemas/cloudflare.connector.deploy schemas/cloudflare.expose.container schemas/cloudflare.expose.remove
git commit -m "feat(cloudflare): register connector actions"
```

### Task 15: harness: TestM6Cloudflare and the vm-m6c job

**Files:**
- Create: `test/harness/m6_cloudflare_test.go` (build tag `harness`; the unit tests first, then the helpers, then the scenario, in the order `m6_npm_test.go` uses)
- Create: `test/harness/m6cw_workflow_unit_test.go` (build tag `harness`)
- Modify: `.github/workflows/harness.yml` (append the job `vm-m6c` after `vm-m6n`, at the end of the file)
- Read only: `test/harness/m6_npm_test.go` (structure: `m6nRun`, the step table, `m6nRunCmd`, `m6nPull`, `m6nRefused`, `m6nNetworkMembers`, `m6nDiagnostic`), `test/harness/m6nw_workflow_unit_test.go` (the workflow check this task copies), `test/harness/m5_derp_test.go` (`m5dLab`), `test/harness/m4_helpers_test.go` (`m4ApplyAsync`, `m4Plan`, `m4Undo`, `m4Effects`), `test/harness/m3_docker_helpers_test.go` (`m3dDocker`, `m3dWaitRunning`, `m3dxShell`, `m3dxImage`), `test/harness/m3_firewall_helpers_test.go` (`m3Run`), `test/harness/api.go` (`API.Read`, `API.Apply`), `docs/dforge/notes/v1-w6b-cloudflare-spike.md` (Task 1)

**Interfaces:**
- Consumes: Contracts "Image" (`cloudflare.Image`) and "Spike note" (the sections `## Token format`, `## Log lines` and `## Unauthorized token`; the values below are named constants the implementer fills from them); Contracts "Wire types" (`cloudflare.ConnectorRunInput`, `Connector`, `ConnectorStopInput`, `ConnectorStopped`, `ConnectorListInput`, `ConnectorList`, `NetworkAttachInput`, `NetworkDetachInput`, `NetworkAttachment`); Contracts "Names" for the values the cloudflare package keeps unexported (the token directory, the token mount, the image user: the harness declares its own constants and a unit test pins them); the registered actions of Task 14 by name; the existing helpers named under **Files**.
- Produces (Contracts "Harness"): `var m6cFlag = flag.Bool("m6c", false, "run TestM6Cloudflare; needs -os and -m2, and is a go test run of its own")`, `func TestM6Cloudflare(t *testing.T)`, every other identifier prefixed `m6c`; the workflow job `vm-m6c`; `test/harness/m6cw_workflow_unit_test.go` with tests prefixed `TestM6cwUnit`.

**Items:** 34, 35

**Depends on:** Task 1, Task 14

**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 1 = 5

`TestM6Cloudflare` covers the host half of M6's Cloudflare items (the plan's Assumptions ruling): the harness lab drives the real controller, whose Cloudflare base URL is a constant, so nothing here reaches the Cloudflare API. The scenario applies `cloudflare.connector.run` directly on the agent through the controller API with a well-formed token that the edge does not know, and checks what the host shows: the container, the token file, that the real pinned cloudflared reads the file, that nothing shows the token, `connector.list`, the attachment claims (including the default-bridge refusal in a preview), `connector.stop` and the undo. `connector.deploy` and the expose composites are covered by unit tests (Tasks 9 and 10); their live run is register row 55, out of scope. Never run the scenario on this host: without `-m6c` it skips, and with it it needs KVM and the harness VMs, which only `.github/workflows/harness.yml` provides.

- [ ] **Step 1: Write the failing tests**

Create `test/harness/m6cw_workflow_unit_test.go` with this content. It copies `m6nwWorkflow` for the new job and adds the check that the three other M5 and M6 jobs do not hold the new test.

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
	m6cwRunStep = "Run the M6 Cloudflare harness"
	m6cwJob     = "vm-m6c"
	// m6cwStepTimeout is the -timeout in minutes of the TestM6Cloudflare step
	// and m6cwJobLimit the job's timeout-minutes.
	m6cwStepTimeout = 40
	m6cwJobLimit    = 60
)

// m6cwWorkflow checks the text of harness.yml for TestM6Cloudflare: the
// vm-m6c job has the vm jobs' setup, a timeout-minutes of m6cwJobLimit, and a
// step that runs the test with the lab flags, a work directory of its own, a
// -timeout of m6cwStepTimeout minutes and its failure logs; and none of the
// vm-m5, vm-m5d and vm-m6n jobs holds the test.
func m6cwWorkflow(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM6Cloudflare", "harness-m6c", "-m6c"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m6cwJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m6cwJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m6cwJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m6cwJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m6cwJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m6cwJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m6cwJob, have, m6cwJobLimit)
	}

	run, ok := m4lStep(job, m6cwRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m6cwJob, m6cwRunStep)
	}
	for _, want := range []string{
		"-run '^TestM6Cloudflare$'", " -m6c", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m6c"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m6cwRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m6cwRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m6cwStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m6cwRunStep, m[1], m6cwStepTimeout)
	}
	if time.Duration(n)*time.Minute < m6cDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m6cwRunStep, m[1], m6cDeadline)
	}
	for _, want := range []string{"name: logs-m6c-${{ matrix.os }}", "harness-m6c/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m6cwJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m6cwWorkflowDoc has the parts of harness.yml that m6cwWorkflow reads.
const m6cwWorkflowDoc = `jobs:
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
    runs-on: ubuntu-24.04
    timeout-minutes: 60
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
      - name: Run the M6 Cloudflare harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/
          -args -os ${{ matrix.os }} -m6c
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6c"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m6c-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6c/**/*.log
`

func TestM6cwUnitWorkflow(t *testing.T) {
	if err := m6cwWorkflow(m6cwWorkflowDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m6n job", "  vm-m6n:\n", "  vm-other:\n", "no vm-m6n job"},
		{"no vm-m5d job", "  vm-m5d:\n", "  vm-other:\n", "no vm-m5d job"},
		{"limit under 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 50", "timeout-minutes 50, want 60"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 60"},
		{"no needs", "  vm-m6c:\n    needs: build\n", "  vm-m6c:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no Cloudflare step", "Run the M6 Cloudflare harness", "Run M6 Cloudflare", `no step named "Run the M6 Cloudflare harness"`},
		{"wrong test", "'^TestM6Cloudflare$'", "'^TestM6Other$'", "TestM6Cloudflare"},
		{"no flag", " -m6c\n", "\n", "-m6c"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m6c\"\n", "", "-workdir"},
		{"workdir of vm-m6n", "-workdir \"$RUNNER_TEMP/harness-m6c\"", "-workdir \"$RUNNER_TEMP/harness-m6n\"", "harness-m6c"},
		{"short timeout", "-timeout 40m -v -run '^TestM6Cloudflare$'", "-timeout 30m -v -run '^TestM6Cloudflare$'", "-timeout 30m"},
		{"long timeout", "-timeout 40m -v -run '^TestM6Cloudflare$'", "-timeout 50m -v -run '^TestM6Cloudflare$'", "-timeout 50m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m6c/**/*.log\n", "", "harness-m6c/**/*.log"},
		{"artifact name of vm-m6n", "name: logs-m6c-${{ matrix.os }}", "name: logs-m6n-${{ matrix.os }}", "logs-m6c-"},
		{"test added to vm-m6n", "-run '^TestM6NPM$'", "-run '^TestM6NPM$|^TestM6Cloudflare$'", "vm-m6n job holds TestM6Cloudflare"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m6c", "vm-m5d job holds -m6c"},
	} {
		if n := strings.Count(m6cwWorkflowDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m6cwWorkflowDoc, tc.from, tc.to, 1)
		if err := m6cwWorkflow(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM6cwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM6Cloudflare runs.
func TestM6cwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m6cwWorkflow(string(b)); err != nil {
		t.Error(err)
	}
}
```

Create `test/harness/m6_cloudflare_test.go` starting with the unit tests below (the build tag, the package clause and this import block; Step 3 adds the imports the helpers and the scenario need):

```go
//go:build harness

package harness

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
)

// TestM6cUnitSpikeValues: the constants taken from the spike note are filled,
// the line that shows the token file was read cannot be mistaken for the line
// that shows it was not, and the states allowed after an unknown token are
// states of a container that docker keeps alive.
func TestM6cUnitSpikeValues(t *testing.T) {
	read := m6cLogRead(m6cTunnel)
	if read == "" || m6cLogUnreadable == "" {
		t.Fatal("harness: m6cLogReadTemplate or m6cLogUnreadable is empty; fill them from the spike note's ## Log lines")
	}
	if strings.Contains(read, "{tunnel}") {
		t.Errorf("harness: the read line %q keeps its placeholder", read)
	}
	if strings.Contains(m6cLogUnreadable, read) || strings.Contains(read, m6cLogUnreadable) {
		t.Errorf("harness: the read line %q and the unreadable line %q contain one another", read, m6cLogUnreadable)
	}
	if len(m6cRefusalStates) == 0 {
		t.Fatal("harness: m6cRefusalStates is empty; fill it from the spike note's ## Unauthorized token")
	}
	for _, s := range m6cRefusalStates {
		if !slices.Contains(m6cLiveStates, s) {
			t.Errorf("harness: refusal state %q is not one of %v", s, m6cLiveStates)
		}
	}
}

func TestM6cUnitExpand(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"Tunnel ID: {tunnel}", "Tunnel ID: abc"},
		{"{tunnel} and {tunnel}", "abc and abc"},
		{"no placeholder", "no placeholder"},
	} {
		if got := m6cExpand(tc.in, "abc"); got != tc.want {
			t.Errorf("m6cExpand(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestM6cUnitConstants pins the values the spec names and the cloudflare
// package keeps to itself.
func TestM6cUnitConstants(t *testing.T) {
	if m6cTokenDir != "/var/lib/darkpanel/agent/cloudflare-tokens" || m6cTokenMount != "/run/darkpanel/token" || m6cImageUID != 65532 {
		t.Errorf("token directory %q, mount %q, uid %d, want the values of spec 7.2", m6cTokenDir, m6cTokenMount, m6cImageUID)
	}
	if m6cConnector != "darkpanel-cloudflared-"+m6cTunnel {
		t.Errorf("connector name %q, want darkpanel-cloudflared-<tunnel>", m6cConnector)
	}
	want := []string{"tunnel", "--no-autoupdate", "run", "--token-file", "/run/darkpanel/token"}
	if got := m6cArgs(m6cTokenMount); !slices.Equal(got, want) {
		t.Errorf("m6cArgs = %q, want %q", got, want)
	}
}

func TestM6cUnitToken(t *testing.T) {
	tok := m6cToken(m6cTunnel)
	raw, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		t.Fatalf("harness: the token is not standard base64: %v", err)
	}
	var parts map[string]string
	if err := json.Unmarshal(raw, &parts); err != nil {
		t.Fatalf("harness: the token does not decode to a JSON object: %v", err)
	}
	if parts["a"] != m6cAccountTag || parts["t"] != m6cTunnel || parts["s"] != m6cSecret || len(parts) != 3 {
		t.Errorf("the token has %d members with a=%q t=%q and the secret matching: %t, want the account tag, the tunnel and the secret in a, t and s",
			len(parts), parts["a"], parts["t"], parts["s"] == m6cSecret)
	}
	if secret, err := base64.StdEncoding.DecodeString(m6cSecret); err != nil || len(secret) != 32 {
		t.Errorf("the secret decodes to %d bytes (error %v), want 32", len(secret), err)
	}
	if m6cToken("another-tunnel") == tok {
		t.Error("the token does not depend on the tunnel")
	}
}

func TestM6cUnitLeaks(t *testing.T) {
	tok := m6cToken(m6cTunnel)
	decoded, err := base64.StdEncoding.DecodeString(tok)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, text string
		want       []string
	}{
		{"clean", "INF Starting tunnel tunnelID=" + m6cTunnel, nil},
		{"the token", "cloudflared --token " + tok, []string{"token"}},
		{"the secret alone", "secret=" + m6cSecret, []string{"secret"}},
		{"the decoded token", string(decoded), []string{"decoded token", "secret"}},
	} {
		if got := m6cLeaks(tc.text); !slices.Equal(got, tc.want) {
			t.Errorf("%s: m6cLeaks = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// m6cUnitInspect is what docker inspect prints for a connector that is as it
// should be.
func m6cUnitInspect(t *testing.T) string {
	t.Helper()
	doc := []map[string]any{{
		"Name": "/" + m6cConnector,
		"Config": map[string]any{
			"Image":  cloudflare.Image,
			"Cmd":    m6cArgs(m6cTokenMount),
			"Env":    []string{"PATH=/usr/local/sbin:/usr/local/bin", "TUNNEL_ORIGIN_CERT=/etc/cloudflared/cert.pem"},
			"Labels": map[string]string{"darkpanel.module": "cloudflare"},
		},
		"HostConfig": map[string]any{"RestartPolicy": map[string]any{"Name": "unless-stopped"}},
		"Mounts": []map[string]any{{
			"Type": "bind", "Source": m6cTokenDir + "/" + m6cTunnel + ".token", "Destination": m6cTokenMount, "Mode": "ro", "RW": false,
		}},
		"State": map[string]any{"Status": "running"},
	}}
	b, err := json.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestM6cUnitInspectCheck(t *testing.T) {
	good := m6cUnitInspect(t)
	if err := m6cInspectCheck([]byte(good), m6cLiveStates); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	restarting := strings.Replace(good, `"Status":"running"`, `"Status":"restarting"`, 1)
	if err := m6cInspectCheck([]byte(restarting), m6cLiveStates); err != nil {
		t.Errorf("a restarting connector: %v, want it accepted", err)
	}
	if err := m6cInspectCheck([]byte(restarting), []string{"running"}); err == nil {
		t.Error("a restarting connector was accepted where only running is allowed")
	}
	tok := m6cToken(m6cTunnel)
	for _, tc := range []struct{ name, from, to, want string }{
		{"wrong name", `"/` + m6cConnector + `"`, `"/other"`, "named"},
		{"wrong image", cloudflare.Image, "cloudflare/cloudflared:latest", "image"},
		{"wrong label", `"darkpanel.module":"cloudflare"`, `"darkpanel.module":"npm"`, "label"},
		{"restart policy", `"unless-stopped"`, `"always"`, "restart policy"},
		{"token on the command line", `"--token-file","` + m6cTokenMount + `"`, `"--token","` + tok + `"`, "command"},
		{"token in the environment", "/etc/cloudflared/cert.pem", tok, "environment holds"},
		{"token variable", "TUNNEL_ORIGIN_CERT=/etc/cloudflared/cert.pem", "TUNNEL_TOKEN=x", "environment sets TUNNEL_TOKEN"},
		{"second mount", `"Mounts":[`, `"Mounts":[{"Type":"volume","Source":"v","Destination":"/x","RW":true},`, "mounts"},
		{"wrong source", "cloudflare-tokens/" + m6cTunnel, "cloudflare-tokens/other", "mount is"},
		{"writable mount", `"RW":false`, `"RW":true`, "writable"},
		{"exited", `"Status":"running"`, `"Status":"exited"`, "want one of"},
	} {
		if n := strings.Count(good, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		err := m6cInspectCheck([]byte(strings.Replace(good, tc.from, tc.to, 1)), m6cLiveStates)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
	for _, tc := range []struct{ name, in, want string }{
		{"no container", `[]`, "want 1"},
		{"two containers", `[{},{}]`, "want 1"},
		{"not JSON", `{`, "docker inspect"},
	} {
		if err := m6cInspectCheck([]byte(tc.in), m6cLiveStates); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}

func TestM6cUnitParseStat(t *testing.T) {
	for _, tc := range []struct {
		out  string
		want m6cStat
		bad  bool
	}{
		{out: "65532 65532 400\n", want: m6cStat{UID: 65532, GID: 65532, Mode: "400"}},
		{out: "0 0 700", want: m6cStat{UID: 0, GID: 0, Mode: "700"}},
		{out: "", bad: true},
		{out: "65532 65532", bad: true},
		{out: "a b c", bad: true},
		{out: "1 2 3 4", bad: true},
	} {
		got, err := m6cParseStat(tc.out)
		switch {
		case tc.bad && err == nil:
			t.Errorf("m6cParseStat(%q) was accepted", tc.out)
		case !tc.bad && (err != nil || got != tc.want):
			t.Errorf("m6cParseStat(%q) = %+v, %v, want %+v", tc.out, got, err, tc.want)
		}
	}
}

func TestM6cUnitSealed(t *testing.T) {
	plain := hex.EncodeToString([]byte(m6cToken(m6cTunnel)))
	for _, tc := range []struct{ name, out, want string }{
		{"sealed", "A1B2C3D4E5F60718\n", ""},
		{"empty", "\n", "no sealed token"},
		{"not hexadecimal", "sealed!", "not hexadecimal"},
		{"plain, lower case", plain + "\n", "in the clear"},
		{"plain, upper case", strings.ToUpper(plain), "in the clear"},
		{"plain, inside", "AA" + strings.ToUpper(plain) + "BB", "in the clear"},
	} {
		err := m6cSealedOK(tc.out, m6cToken(m6cTunnel))
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: %v, want an error containing %q", tc.name, err, tc.want)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w6b-tmp/15 && export TMPDIR=/root/.cache/w6b-tmp/15 GOTOOLCHAIN=local && timeout 300 go vet -tags harness ./test/harness/ ; timeout 300 go test -tags harness -count=1 -run 'M6cUnit|M6cwUnit' ./test/harness/`
Expected: FAIL to build, naming undefined identifiers: `undefined: m6cDeadline` (from `m6cw_workflow_unit_test.go`) and `undefined: m6cTunnel`, `undefined: m6cLogRead`, `undefined: m6cToken`, `undefined: m6cInspectCheck` (from `m6_cloudflare_test.go`). Quote the first lines.

- [ ] **Step 3: Implement**

Do the three parts in this order, and read `docs/dforge/notes/v1-w6b-cloudflare-spike.md` before part 1.

**Part 1: `m6_cloudflare_test.go`, helpers and constants (below the unit tests).** Extend the import block with what the code uses (expected, besides the imports the unit tests already have: `bytes`, `context`, `flag`, `fmt`, `time`, `errcode`, `paths` and `vm`; `gofmt` and `go vet` name any that is missing or unused). Declare, with a doc comment on each:

| Identifier | Kind | Value or rule |
|---|---|---|
| `m6cFlag` | `var` | `flag.Bool("m6c", false, "run TestM6Cloudflare; needs -os and -m2, and is a go test run of its own")` |
| `m6cDeadline` | `const` | `30 * time.Minute`; the workflow step's `-timeout` is 40 minutes, this plus 10 |
| `m6cTunnel` | `const` | `"0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"`, a lower-case UUID the edge does not know |
| `m6cAccountTag` | `const` | `"9d4e6a2b1c8f4e3a9b7d5c3e1f2a4b6c"` |
| `m6cSecret` | `var` | the standard base64 of 32 bytes of `0x6d` (`bytes.Repeat([]byte{0x6d}, 32)`) |
| `m6cTokenDir` | `const` | `"/var/lib/darkpanel/agent/cloudflare-tokens"` |
| `m6cTokenMount` | `const` | `"/run/darkpanel/token"` |
| `m6cAbsentToken` | `const` | `"/run/darkpanel/absent"`, a path nothing is mounted at |
| `m6cImageUID` | `const` | `65532` |
| `m6cConnector` | `const` | `"darkpanel-cloudflared-" + m6cTunnel` |
| `m6cAppName`, `m6cBridgeName`, `m6cControlName`, `m6cNet` | `const` | `"m6c-app"`, `"m6c-bridge"`, `"m6c-control"`, `"m6c-net"` |
| `m6cClaimantA`, `m6cClaimantB`, `m6cClaimantC` | `const` | `"expose:m6c-a.example.test"`, `"expose:m6c-b.example.test"`, `"expose:m6c-c.example.test"` |
| `m6cLiveStates` | `var []string` | `{"running", "restarting"}`: the states connector.run's own verification accepts |

The three constants the spike note fixes, named exactly so; each is filled from the note and nowhere else:

| Identifier | Rule |
|---|---|
| `m6cLogReadTemplate` (string const) | the exact substring `## Log lines` gives for what cloudflared logs after it has read and decoded the token file; where the substring holds the tunnel id, write `{tunnel}` in its place |
| `m6cLogUnreadable` (string const) | the exact substring `## Log lines` gives for what cloudflared logs when the token file cannot be read |
| `m6cRefusalStates` (`var []string`) | from `## Unauthorized token`: `{"running"}` when the container keeps running after the edge refuses the token; `{"running", "restarting"}` when it restarts or exits (the restart policy relaunches it) |

`m6cToken` follows the Go expression in `## Token format` of the note; the expected form, which Contracts fixes, is the standard base64 of the JSON object `{"a":<m6cAccountTag>,"t":<tunnel>,"s":<m6cSecret>}` with the members in that order. Do not copy any value of the note into a comment or a log message other than these constants.

Functions, each with the exact signature shown, all pure (no VM, no network) except where stated:

- `m6cToken(tunnel string) string`: the token above for `tunnel`.
- `m6cExpand(template, tunnel string) string`: replaces every `{tunnel}` in `template` by `tunnel`.
- `m6cLogRead(tunnel string) string`: `m6cExpand(m6cLogReadTemplate, tunnel)`.
- `m6cArgs(tokenFile string) []string`: `tunnel`, `--no-autoupdate`, `run`, `--token-file`, `tokenFile`, in that order (the command spec 7.2 names), a fresh slice each call.
- `m6cLeaks(text string) []string`: which secrets `text` shows, in the fixed order `"token"` (it holds `m6cToken(m6cTunnel)`), `"decoded token"` (it holds the JSON the token decodes to), `"secret"` (it holds `m6cSecret`); nil when none. It returns labels, never values.
- `type m6cStat struct { UID, GID int; Mode string }` and `m6cParseStat(out string) (m6cStat, error)`: parses the output of `stat -c '%u %g %a'`, exactly three space-separated fields, the first two integers.
- `m6cSealedOK(hexOut, token string) error`: `hexOut` is what `SELECT hex(token_ct)` printed. After trimming white space it must be non-empty (error text `harness: no sealed token`), valid hexadecimal (`harness: the sealed token is not hexadecimal`) and must not contain the hexadecimal form of `token`, compared case-insensitively (`harness: the stored token is in the clear`).
- `m6cInspectCheck(inspect []byte, allowed []string) error`: decodes the JSON array `docker inspect` prints and checks the one container against the connector that `connector.run` must create for `m6cTunnel`. Exactly these checks, in this order, each error starting `harness:` and containing the words the unit test matches: the array holds one container (`docker inspect printed %d containers, want 1`; undecodable JSON: `docker inspect: %w`); its `Name` is `/` + `m6cConnector` (`the container is named %q, want %q`); `Config.Image` equals `cloudflare.Image` (`the container runs image %q, want %q`); label `darkpanel.module` is `cloudflare` (`the container label darkpanel.module is %q, want cloudflare`); `HostConfig.RestartPolicy.Name` is `unless-stopped` (`the container's restart policy is %q, want unless-stopped`); `Config.Cmd` equals `m6cArgs(m6cTokenMount)` (`the container's command differs from %q`, with the expected command, followed by `, and it holds the %s` and the labels of `m6cLeaks` of the joined command when that is not empty; the actual command is never printed); no `Config.Env` entry is named `TUNNEL_TOKEN` or starts with `TUNNEL_TOKEN` (`the container's environment sets TUNNEL_TOKEN`) and `m6cLeaks` of the joined entries is empty (`the container's environment holds the %s`); exactly one entry in `Mounts` (`the container has %d mounts, want the one token mount`); that mount has `Type` `bind`, `Source` `m6cTokenDir + "/" + m6cTunnel + ".token"` and `Destination` `m6cTokenMount` (`the container's mount is %s %s:%s, want a bind of %s at %s`); and `RW` false (`the token mount is writable`); `State.Status` is in `allowed` (`the container is %s, want one of %v`). No error prints an environment value or the actual command.

Functions that use a VM:

- `m6cStaysUp(ctx context.Context, t *testing.T, v *vm.VM, name string, allowed []string)`: five times, two seconds apart (wait with `select` on `ctx.Done()` and `time.After`), reads `sudo docker inspect -f '{{.State.Status}}' <name>` and calls `t.Errorf` when the state is not in `allowed`.
- `m6cDiagnostics() []m6nDiagnostic`: what a failed step logs from the agent: `sudo docker ps -a` with the same table format as `m6nDiagnostics`, `sudo docker network ls`, `sudo ls -la ` + `m6cTokenDir` (a listing, never a `cat`), and `sudo docker logs --tail 40 <name> 2>&1` for `m6cConnector`, `m6cControlName` and `m6cAppName`. No diagnostic runs `docker inspect` or reads the token file.
- `(r *m6cRun) logDiagnostics(t *testing.T)`: as `m6nRun.logDiagnostics` with the timeout constant `m6cDiagnosticTimeout` (15 seconds), using `m5dCapDiagnostic`.

**Part 2: the scenario.** `type m6cRun struct { ctx context.Context; f *fleet; token string; changeID string; containerID string }`. `TestM6Cloudflare` has the shape of `TestM6NPM`: it skips unless `*osFlag`, `*m2Flag` and `*m6cFlag` are all set, derives `ctx` with the timeout `m6cDeadline`, builds `r := &m6cRun{ctx: ctx, f: m5dLab(ctx, t), token: m6cToken(m6cTunnel)}`, runs the steps below as subtests in order, logs the diagnostics when a subtest fails, and stops at a failed gate. Its doc comment says what it proves and that it never reaches the Cloudflare API. The steps, `{name, gate}` then what each does (all agent calls use `r.f.agent`, `r.f.api` and `r.f.agentID`; a failure message never contains the token, the secret or the contents of the token file):

1. `prepare` (gate): `m6nPull` of `cloudflare.Image` and of the pinned `pebble-challtestsrv` image (`m3dxImage`), the target container's image, on the agent.
2. `control` (not gate): a negative control for the log constants. Remove `m6cControlName` if present, run the connector image with `m6nRunCmd(m6nRunSpec{Name: m6cControlName, Image: cloudflare.Image, Args: m6cArgs(m6cAbsentToken)})`, register a cleanup that removes it, and wait up to two minutes (`eventually`) for `sudo docker logs` of it to contain `m6cLogUnreadable`; then require that those logs do not contain `m6cLogRead(m6cTunnel)`.
3. `run` (gate): `m4ApplyAsync(r.ctx, t, r.f.api, r.f.agentID, "cloudflare.connector.run", cloudflare.ConnectorRunInput{Tunnel: m6cTunnel, Token: r.token})`. Decode the change output as `cloudflare.Connector` and require `Tunnel` = `m6cTunnel`, `Container` = `m6cConnector`, a 64-character lower-case hexadecimal `ContainerID` and a `State` in `m6cLiveStates`; keep the id and the change id in `r`. Require `m6cLeaks` of the raw output to be empty, and the same of the JSON of `m4Effects` for the change, which must list at least one effect.
4. `host-state` (not gate): `m6cInspectCheck` of `sudo docker inspect <m6cConnector>` with `m6cLiveStates`; the token file `m6cTokenDir/<m6cTunnel>.token` has `m6cParseStat` of `sudo stat -c '%u %g %a'` equal to `{m6cImageUID, m6cImageUID, "400"}`, its content with white space trimmed equals the token, the directory has owner 0, group 0 and mode `700`, and `sudo ls -A <dir>` lists only that file; the sealed row: `sudo sqlite3 -cmd '.timeout 10000' ` + `paths.Production().AgentDB()` + a double-quoted `SELECT hex(token_ct) FROM cloudflare_connectors WHERE tunnel_id = '<m6cTunnel>' AND container_id = '<r.containerID>'`, quoted as `m6_npm_test.go` quotes its `UPDATE`, whose output passes `m6cSealedOK`.
5. `reads-token` (not gate): `eventually` (three minutes) the output of `sudo docker logs <m6cConnector> 2>&1` contains `m6cLogRead(m6cTunnel)` and does not contain `m6cLogUnreadable`; then `m6cStaysUp` with `m6cRefusalStates`.
6. `no-leak` (not gate): collect `sudo docker inspect <m6cConnector>`, `sudo docker logs <m6cConnector> 2>&1`, `sudo ps -eo args`, the raw JSON of `r.f.api.Read` of `docker.container.inspect/v1` and of `docker.container.logs/v1` (inputs `map[string]any{"container": m6cConnector}` and `map[string]any{"container": m6cConnector, "tail": 500}`, decoded into `json.RawMessage`); for each, `t.Errorf` with the source's name and the labels of `m6cLeaks` when it is not empty.
7. `list` (not gate): `r.f.api.Read` of `cloudflare.connector.list/v1` with `cloudflare.ConnectorListInput{}` into `cloudflare.ConnectorList`; exactly one connector with the tunnel, container name and `r.containerID`, and a state in `m6cLiveStates`.
8. `rerun-refused` (not gate): `m4Plan` of `cloudflare.connector.run` on the agent with the same input is refused: `m6nRefused(t, "a second connector.run for the tunnel", err, errcode.InvalidInput, "")`.
9. `unsupported-target` (not gate): run `m6cBridgeName` from the target image on the default bridge (`m6nRunCmd`, no network, the arguments `-dnsserver '' -http01 '' -https01 '' -tlsalpn01 ''` that `m6nRun.expose` passes that image), `m3dWaitRunning`, then `m4Plan` of `cloudflare.network.attach` with `cloudflare.NetworkAttachInput{Tunnel: m6cTunnel, Network: "bridge", Claimant: m6cClaimantA, Target: m6cBridgeName}` is refused: `m6nRefused(..., errcode.CloudflareUnsupportedTarget, m6nUnsupportedReason)`.
10. `claims` (gate): create `m6cNet`, run `m6cAppName` on it from the target image, `m3dWaitRunning`. Apply `cloudflare.network.attach` for `m6cClaimantA` and then `m6cClaimantB` (network `m6cNet`, target `m6cAppName`), each through `m4ApplyAsync`; after the first, `m6nNetworkMembers` of `m6cNet` holds `m6cConnector`, and the output decoded as `cloudflare.NetworkAttachment` has `Attached` true and `Preexisting` false. Apply `cloudflare.network.detach` for A: the connector is still a member. `m4Plan` of `cloudflare.connector.stop` is refused: `m6nRefused(..., errcode.DockerInUse, m6cClaimantB)`. Apply `cloudflare.network.detach` for B with `Network: ""`: the output has `Attached` false and the connector has left `m6cNet`. `m4Plan` of the same detach again returns no error and an empty diff (the claim is gone, so it converges).
11. `attach-undo` (not gate): apply `cloudflare.network.attach` for `m6cClaimantC`; the connector is a member; `m4Undo` of that change returns nil; the connector is no longer a member; `m4Plan` of `cloudflare.connector.stop` returns no error, which shows the undo dropped the claim.
12. `stop` (gate): apply `cloudflare.connector.stop` (`cloudflare.ConnectorStopInput{Tunnel: m6cTunnel}`); the output (`cloudflare.ConnectorStopped`) has `Removed` true; `sudo docker ps -a --format '{{.Names}}'` no longer lists `m6cConnector`; `sudo ls -A <m6cTokenDir>` prints nothing; `connector.list` is empty; `m4Plan` of stop again returns no error and an empty diff.
13. `run-undo` (not gate): apply `cloudflare.connector.run` again with the same input; `m3dWaitRunning` is not used (the state may be `restarting`), so check with `docker inspect` that the container exists; `m4Undo` of that change returns nil; then the container is gone, `<m6cTokenDir>` is empty and `connector.list` is empty.

**Part 3: the workflow job.** Append this job to `.github/workflows/harness.yml` after `vm-m6n`, with a blank line before it. Its action pins are those of `vm-m6n`, copied unchanged.

```yaml
  vm-m6c:
    # The Cloudflare connector test's job, beside vm-m6n. The limit is the
    # step's -timeout plus 20 minutes of setup. TestM6Cloudflare does not read
    # -derper, so the step leaves it out.
    needs: build
    runs-on: ubuntu-24.04
    timeout-minutes: 60
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
      # TestM6Cloudflare boots a lab of its own with one agent, applies
      # cloudflare.connector.run with a well-formed tunnel token and has the
      # cloudflared image pinned in internal/modules/cloudflare/image.go read
      # its token file. It never reaches the Cloudflare API. Its own deadline
      # is 30 minutes, and the extra 10 leave its VM cleanups time to finish.
      - name: Run the M6 Cloudflare harness
        run: >-
          go test -tags harness -timeout 40m -v -run '^TestM6Cloudflare$' ./test/harness/
          -args -os ${{ matrix.os }} -m6c
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6c"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-m6c-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6c/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

Before saving, confirm that `git diff .github/workflows/harness.yml` shows only the appended job, and that the copied `uses:` pins are character for character those of `vm-m6n`.

- [ ] **Step 4: Run the tests to verify they pass**

Run:
```bash
export TMPDIR=/root/.cache/w6b-tmp/15 GOTOOLCHAIN=local
gofmt -l test/harness
timeout 300 go vet -tags harness ./test/...
timeout 300 go test -tags harness -count=1 -run 'Unit' ./test/harness/...
timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/...
```
Expected: `gofmt -l` prints nothing; `go vet` prints nothing; the test run prints `ok` (the `M6cUnit*` and `M6cwUnit*` tests pass with the other harness unit tests, and `TestM6Cloudflare` itself is not run: it skips without `-m6c`); `golangci-lint` prints `0 issues.`

Then the task's closing checks, each with its real output recorded: `timeout 600 go build ./...`; `timeout 300 go vet ./...` (no output); `timeout 1500 go test -count=1 ./...` (every line `ok` or `?`); `timeout 300 go mod tidy -diff` (no output). `go test -race` has no package of this task to run on: the harness package has the build tag, and the race run on it is the `-tags harness` unit run above with `-race` added: `timeout 600 go test -tags harness -race -count=1 -run 'Unit' ./test/harness/...`. Remove `/root/.cache/w6b-tmp/15` and run `go clean -testcache` when done.

The live run of the scenario is not part of this task's verification: it happens when the plan's final gate dispatches `harness.yml` on the branch (gate 7, under the owner's standing authorization), and the report of that run belongs to the plan's gates, not to this task.

- [ ] **Step 5: Commit**

```bash
git add test/harness/m6_cloudflare_test.go test/harness/m6cw_workflow_unit_test.go .github/workflows/harness.yml
git commit -m "test(harness): add TestM6Cloudflare and vm-m6c"
```

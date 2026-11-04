# v1 wave 8: panel exposure Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: the skill the **Execution:** line names — dforge:subagent-driven-development for `subagent`, dforge:executing-plans for `inline`. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build wave 8 of the v1 completion spec: the `panel` module, which exposes the Darkpanel UI on the controller's host through Nginx Proxy Manager or a Cloudflare tunnel (`panel.exposure.add`) and removes it again, also through the exposure itself (`panel.exposure.remove`), with the composite-runner retirement of the removed path, the every-page banner, restore marking of exposures and the harness test `TestM6Panel` (register rows 36, 91, 328, 335, 381 and 391).

**Architecture:** One integrator task gives the composite runner exposure retirement (`core.Step.Retires`, stored per step, turned into `Env.Call.Retired` for the later steps once the retiring step has committed) and hides composite step events of hidden composites. The `panel` module then adds agent actions over the Docker Engine API (the `darkpanel-panel` network on a free /29 and the connector check), controller actions over `core.Listeners` and its own `panel_exposures` table (listener open and revoke, exposure status), and the two composites that chain them with the existing NPM, Cloudflare and firewall children. NPM and Cloudflare get the residual fixes this path needs, backup marking learns exposures, the web gets a panel page and a banner, and a harness job exposes the panel through a real NPM end to end.

**Tech Stack:** Go 1.27 (`go.mod`: `go 1.27`, toolchain `go1.27.3`), SQLite through `internal/core/store` (`modernc.org/sqlite v1.60.1`), the Docker Engine API client `internal/platform/dockerapi`, React 19 + TypeScript with Vitest and vitest-axe in `web/`, the KVM harness in `test/harness` (GitHub Actions `harness.yml`).

**Spec:** docs/dforge/specs/2026-10-21-v1-completion-design.md

**Execution:** subagent — `claude --model sonnet --effort high` — the plan runs parallel waves, and each task's **Implementer:** line chooses its implementer

**Parallelism:** waves — 1 | 2-4 | 5 | 6 | 7 | 8 | 9-10 | 11 | 12-13

**Task check:** none — full suite every 3 tasks

**Integration check:** `mkdir -p /root/.cache/w8-tmp/integ && export TMPDIR=/root/.cache/w8-tmp/integ GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff && timeout 1200 make lint-web test-web`

**CI:** `.github/workflows/ci.yml .github/workflows/harness.yml .github/workflows/release.yml .github/workflows/runner-probe.yml` — platforms: ubuntu-24.04, self-hosted — local: `make test-adr`; `make lint-sh test-release`; `make lint-go test-go vuln`; `make lint-web test-web`; `make build verify-build` — uncovered: none

**Worktree setup:** `timeout 600 make web-deps`

**Wave check:** `mkdir -p /root/.cache/w8-tmp/wave && export TMPDIR=/root/.cache/w8-tmp/wave GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 900 go test -count=1 ./internal/modules/panel/... ./internal/modules/npm/... ./internal/modules/cloudflare/... ./internal/core/... ./internal/controller/... ./schemas/... && timeout 300 go vet -tags harness ./test/...`

**Plan review:** 2026-11-04 — dforge:judge-opus — executability 16 / coherence 15 / coverage 18 / assumptions 14 (round 1)

## Global Constraints

- Scope is spec revision 8 as amended on `main`: section 8 (R72), the parts of 1.3, 1.5 and 1.10 that section 8 relies on, the wave 8 row of Parallel decomposition, the M6 harness evidence for the panel, the plan rulings W8-R1 to W8-R16 under Assumptions (Task 11 copies them into the spec), and the register rows the tasks' **Items:** lines name. Out of scope: the live demonstration with owner credentials (register row 55, owner checklist), fuzz targets (wave 9a), accessibility audit beyond each web test's axe check (wave 9b), user documentation (wave 9c), the runbook (wave 10).
- Host safety (absolute). This host is live: it runs an installed `darkpanel-agent`, `darkpanel-controller` and a Docker daemon. No unit test or command of this plan talks to the Docker daemon, D-Bus or systemd, runs `systemctl`, `docker`, `ufw`, `iptables` or `nft`, binds an address other than `127.0.0.1` port 0 or a Unix socket under `t.TempDir()`, or reads or writes `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel*`. Docker access in tests goes through the in-memory fake of Contracts "Panel agent dependencies"; runtime listeners in tests go through the fake of Contracts "Panel controller fixture". Real Docker, NPM, listeners and firewall changes run only on harness VMs (Task 12).
- Every `go`, `golangci-lint`, `make` and `pnpm` command runs with `GOTOOLCHAIN=local`, an explicit `timeout`, and `TMPDIR=/root/.cache/w8-tmp/<task number>` (`mkdir -p` it first; remove it and run `go clean -testcache` when the task ends), also where a step's command omits one of them: `/tmp` is an 8 GiB tmpfs and Unix socket paths over 108 bytes fail. When `df -h /` shows 90 % or more before a task's first build, trim the Go build cache (`go clean -cache` is acceptable).
- Dependencies: no task changes `go.mod`, `go.sum`, `web/package.json` or `web/pnpm-lock.yaml`; after every task `GOTOOLCHAIN=local timeout 300 go mod tidy -diff` prints nothing.
- After every Go task all of these pass, with their real output recorded: `go build ./...`; `go vet ./...` (no output); `go test -count=1 ./...` (every line `ok` or `?`); `go test -race -count=1` on the task's packages; `golangci-lint run --allow-serial-runners ./...` (`0 issues.`); `go mod tidy -diff` (no output). A task that edits `test/harness/` also runs `go vet -tags harness ./test/...`, `golangci-lint run --allow-serial-runners --build-tags harness ./test/...` and `go test -tags harness -count=1 -run 'Unit' ./test/harness/...`. A task that edits `web/` runs `make lint-web test-web`. Only Task 11 runs `make schemas` and commits snapshots.
- TDD: write the test, run it and see the expected RED (usually a build failure naming the first undefined identifier), implement, run GREEN. Quote the RED and GREEN output in the report verbatim, not a paraphrase. A task that skips the full-suite line above says so and why in its report.
- Frozen files (spec 1.12), which no task edits except where a task's **Files:** block names the file: `internal/core/**`, `internal/platform/**`, `internal/controller/**`, `internal/cli/**`, `internal/install/**`, `internal/transport/**`, `cmd/darkpanel/**`, `go.mod`, `go.sum`, `web/src/app.tsx`, `web/src/modules/registry.ts`, `web/src/modules/types.ts`, `web/package.json`, `test/harness/pins.go`, `schemas/**` except Task 11's regenerated snapshots, `Makefile`, `.github/workflows/**` except `harness.yml` in Task 12. Tasks 1 and 13 are Integrator tasks: the plan rulings W8-R1 (Task 1, `internal/core/**`) and W8-R12 (Task 13, `internal/controller/server/**`) authorise exactly the edits their steps describe. Task 8 edits `internal/controller/backup/**`, which wave 7 owned and a later wave may edit (Parallel decomposition). A task that finds a missing code, field or extension point in a frozen file it does not name stops and reports it; it does not edit the file.
- A task edits only the files of its own **Files:** block. Each task of a wave runs in its own worktree from the wave's base (`main` plus every merged earlier wave), so a package-wide command sees only merged work plus the task's own files. "The base" in a task's text means the work its **Depends on** line names.
- Test isolation: `t.TempDir()` for files and databases; no TCP listener except `httptest` on `127.0.0.1` port 0; `coretest.NewFakeClock` wherever a duration above one second is waited on; no package-level mutable state written by tests (fakes are injected through the dependency structs of Contracts, never a replaced package variable). Tasks of one wave therefore share no port, path or global state.
- Tasks that share a Go package declare no top-level identifier, test helpers included, that another task declares. Every new test identifier carries its task's prefix from Contracts "Test prefixes"; only Task 4 declares the shared fixtures of Contracts "Panel agent dependencies" and "Panel controller fixture". An unexported top-level non-test identifier a task adds in `internal/modules/panel` that neither Contracts nor the task's own text names starts with its task's word: Task 4 `store` or `deps`, Task 6 `network` or `connector`, Task 7 `listener` or `status`, Task 9 `add`, Task 10 `remove`, Task 11 `reg`.
- Code style: match the surrounding code; a doc comment on every exported identifier; comments only for a non-obvious why, in full sentences, never naming a plan, task, register row or review; `//nolint` names the rule and a reason; gofmt clean; eslint `--max-warnings 0` and `tsc --noEmit` clean in `web/`. Error strings start with the package name (`panel:`, `npm:`, `cloudflare:`, `lifecycle:`, `server:`, `backup:`, `harness:`). Refusals are `*errcode.Error` using only codes already in `internal/core/errcode/errcode.go`: `panel.totp_incomplete`, `panel.already_exposed`, `panel.wrong_host`, `panel.not_exposed`, `npm.not_public`, `cloudflare.connector_missing`, `docker.owned`, `docker.engine_unavailable`, `request.invalid_input`, `not_found`; every other failure is a plain wrapped error.
- Secrets: no log line, error, audit row, problem detail, effect document or test failure message carries a session token, a TOTP secret, a tunnel token or a certificate private key.
- Values copied from the spec, used exactly: network name `darkpanel-panel`, label `darkpanel.module=panel` (`core.LabelModule`), bridge option `com.docker.network.bridge.name` = `dp-panel`, panel range `172.31.255.0/24` searched for a free /29 from the top down, gateway the /29's first host address, proxy address its second, `EnableIPv6` false, exactly one IPAM config; default listener port 9443; client headers `X-Darkpanel-Client` for `via: npm` and `Cf-Connecting-IP` for `via: tunnel` (`core.ClientHeaderNPM`, `core.ClientHeaderTunnel`); connector container `darkpanel-cloudflared-<tunnel>`; NPM proxy host forced SSL, HSTS, forward scheme `https`; tunnel service `https://<gateway>:<port>` with `noTLSVerify` true; the firewall rule `allow in on dp-panel from <proxy> to <gateway> port <port> proto tcp`, IPv4; the banner text "Panel exposed via <npm|tunnel> at <hostname>"; every `panel.*` action `MinScope: admin`.
- Commits use `<type>(<scope>): <subject>`, type one of feat|fix|docs|style|refactor|test|chore|perf, subject at most 50 characters, imperative, no trailing period, no attribution of any kind. English only.
- The plan ends with the gates in `docs/dforge/gates.md`. Task 12 touches `test/harness/` and `.github/workflows/harness.yml`, so gate 7 (harness) applies to the branch once it is pushed, as well as on `main`; dispatch `harness.yml` on the branch per the owner's standing authorization. No task creates or pushes a tag, and no task runs `release.yml`.

## Contracts

- **Test prefixes.** Task 1 `Rt` (`internal/core/lifecycle`, `internal/core/store`, `internal/core`); Task 13 `Sv` (`internal/controller/server`); Task 2 `Rr` (`internal/modules/npm`); Task 3 `Al` (`internal/modules/cloudflare`); Task 4 `Fd`; Task 6 `Nw`; Task 7 `Ls`; Task 9 `Ea`; Task 10 `Er`; Task 11 `Rg` (all `internal/modules/panel`); Task 8 `Px` (`internal/controller/backup`); Task 12 `M6p` (`test/harness`: `TestM6Panel`, unit tests `TestM6pUnit*`, workflow tests `TestM6pwUnit*`). Web test files are named after their component.

- **Step retirement** (Task 1, `internal/core/contract.go`):

  ```go
  type Step struct {
      ID      string          `json:"id"`
      Host    string          `json:"host"`
      Action  ActionRef       `json:"action"`
      Input   json.RawMessage `json:"input"`
      After   []string        `json:"after,omitempty"`
      Retires string          `json:"retires,omitempty"` // exposure id this step retires once it has committed (1.5); forward composites only
  }
  ```

  Migration `internal/core/store/migrations/controller/0014_composite_retires.sql` adds `core_composite_steps.retires TEXT NOT NULL DEFAULT ''`. The registry-time step check (`internal/core/lifecycle/composite.go`) refuses, as a module bug, a non-empty `Retires` on a step of a composite whose `Unwind` is not `forward` or on a read step. When the runner plans or applies step `s` it sets `CallContext.Retired` to the non-empty `retires` values, in ordinal order and without duplicates, of every step of the same composite with a lower ordinal whose state is `applied` or `confirmed` (`core.StepApplied`, `core.StepConfirmed`); the composite dry run at plan time passes no `Retired`.

- **Step event visibility** (Task 13, `internal/controller/server/services.go`): the engine's `Publish` drops a `composite.step` event whose composite change is not network visible (`api.NetworkVisible` over the composite's `ChangeView`); every other event is published unchanged.

- **Panel names** (Task 4, `internal/modules/panel/wire.go`):

  ```go
  const (
      ID              = "panel"                // existing
      NetworkName     = "darkpanel-panel"
      BridgeName      = "dp-panel"
      BridgeOption    = "com.docker.network.bridge.name"
      DefaultPort     = 9443
      Claimant        = "panel"                // the npm and cloudflare attachment claimant (W8-R6)
      ConnectorPrefix = "darkpanel-cloudflared-"
      ViaNPM          = "npm"
      ViaTunnel       = "tunnel"
      StateOpen        = "open"                // panel_exposures.state
      StateRevoked     = "revoked"
      StateNeedsRepair = "needs_repair"
      ActionAdd        = "panel.exposure.add"
      ActionRemove     = "panel.exposure.remove"
      ActionStatus     = "panel.exposure.status"
      ActionNetCreate  = "panel.network.create"
      ActionNetRemove  = "panel.network.remove"
      ActionConnCheck  = "panel.connector.check"
      ActionOpen       = "panel.listener.open"
      ActionRevoke     = "panel.listener.revoke"
  )
  var PanelRange = netip.MustParsePrefix("172.31.255.0/24")
  ```

- **Wire types** (Task 4, `internal/modules/panel/wire.go`; JSON names as shown; schema tags follow the npm and cloudflare wire files):

  ```go
  type ExposeInput struct {
      Via      string `json:"via"`              // npm | tunnel
      Hostname string `json:"hostname"`         // lowercase DNS name, at most 253 characters
      HostID   string `json:"hostId"`           // must be the controller's host (panel.wrong_host)
      Tunnel   string `json:"tunnel,omitempty"` // required for via tunnel, refused for via npm; lowercase UUID
      Port     int    `json:"port,omitempty"`   // 0 means DefaultPort; else 1024-65535 (W8-R4)
  }
  type ExposeOutput struct {
      ExposureID string       `json:"exposureId"`
      Via        string       `json:"via"`
      Hostname   string       `json:"hostname"`
      HostID     string       `json:"hostId"`
      Tunnel     string       `json:"tunnel,omitempty"`
      Subnet     netip.Prefix `json:"subnet"`
      Gateway    netip.Addr   `json:"gateway"`
      Proxy      netip.Addr   `json:"proxy"`
      Port       int          `json:"port"`
  }
  type RemoveInput struct{}  // at most one live exposure exists
  type RemoveOutput struct {
      ExposureID string `json:"exposureId"`
      Hostname   string `json:"hostname"`
  }
  type NetworkCreateInput struct{}
  type NetworkView struct {
      Network string       `json:"network"` // NetworkName
      ID      string       `json:"id"`      // the Engine's network id
      Subnet  netip.Prefix `json:"subnet"`
      Gateway netip.Addr   `json:"gateway"`
      Proxy   netip.Addr   `json:"proxy"`
  }
  type NetworkRemoveInput struct{}
  type NetworkRemoveOutput struct {
      Network string `json:"network"`
      Removed bool   `json:"removed"` // false when it was already absent
  }
  type ConnectorCheckInput struct {
      Tunnel string `json:"tunnel"`
  }
  type ConnectorCheckOutput struct {
      Container string `json:"container"` // ConnectorPrefix + tunnel
      State     string `json:"state"`     // the Engine's container state, "running" on success
  }
  type ListenerOpenInput struct {
      Via      string       `json:"via"`
      Hostname string       `json:"hostname"`
      HostID   string       `json:"hostId"`
      Tunnel   string       `json:"tunnel,omitempty"`
      Subnet   netip.Prefix `json:"subnet"`
      Gateway  netip.Addr   `json:"gateway"`
      Proxy    netip.Addr   `json:"proxy"`
      Port     int          `json:"port"`
  }
  type ListenerOpenOutput struct {
      ExposureID string         `json:"exposureId"`
      ListenerID string         `json:"listenerId"`
      RuleID     string         `json:"ruleId"`
      Address    netip.AddrPort `json:"address"`
  }
  type ListenerRevokeInput struct{}
  type ListenerRevokeOutput struct {
      ExposureID string `json:"exposureId"`
      ListenerID string `json:"listenerId"`
      RuleID     string `json:"ruleId"`
      Revoked    bool   `json:"revoked"` // false when the listener was already revoked or closed
  }
  type StatusInput struct{}
  type StatusOutput struct {
      Exposed  bool          `json:"exposed"`
      Exposure *ExposureView `json:"exposure,omitempty"` // the live exposure, nil when none
  }
  type ExposureView struct {
      ID         string       `json:"id"`
      Via        string       `json:"via"`
      Hostname   string       `json:"hostname"`
      HostID     string       `json:"hostId"`
      Tunnel     string       `json:"tunnel,omitempty"`
      Subnet     netip.Prefix `json:"subnet"`
      Gateway    netip.Addr   `json:"gateway"`
      Proxy      netip.Addr   `json:"proxy"`
      Port       int          `json:"port"`
      State      string       `json:"state"` // open | revoked | needs_repair
      ListenerID string       `json:"listenerId"`
      CreatedAt  time.Time    `json:"createdAt"`
  }
  ```

  `func ValidateExpose(in ExposeInput) error` (Task 4) checks the field rules in the comments above and answers `request.invalid_input` naming the field's JSON pointer.

- **Exposure store** (Task 4, `internal/modules/panel/migrations/controller/0001_panel.sql`, `internal/modules/panel/store.go`):

  ```sql
  CREATE TABLE panel_exposures (
      id                TEXT PRIMARY KEY,        -- the panel.exposure.add change id (W8-R2)
      via               TEXT NOT NULL CHECK (via IN ('npm', 'tunnel')),
      hostname          TEXT NOT NULL,
      host_id           TEXT NOT NULL,
      tunnel            TEXT NOT NULL DEFAULT '',
      subnet            TEXT NOT NULL,
      gateway           TEXT NOT NULL,
      proxy             TEXT NOT NULL,
      port              INTEGER NOT NULL,
      listener_id       TEXT NOT NULL,
      rule_id           TEXT NOT NULL,
      state             TEXT NOT NULL CHECK (state IN ('open', 'revoked', 'needs_repair')),
      removal_change_id TEXT NOT NULL DEFAULT '',
      created_at        TEXT NOT NULL,
      updated_at        TEXT NOT NULL
  );
  ```

  An exposure is **gone** when `state = 'revoked' AND removal_change_id <> '' AND EXISTS (SELECT 1 FROM core_changes c WHERE c.id = removal_change_id AND c.state = 'completed')`; every other row is **live**. At most one live row exists (W8-R3).

  ```go
  // GoneSQL is the predicate above over a row aliased e, for queries in this module and in internal/controller/backup.
  const GoneSQL = `e.state = 'revoked' AND e.removal_change_id <> '' AND EXISTS (SELECT 1 FROM core_changes c WHERE c.id = e.removal_change_id AND c.state = 'completed')`
  type exposureRow struct {
      ID, Via, Hostname, HostID, Tunnel string
      Subnet                            netip.Prefix
      Gateway, Proxy                    netip.Addr
      Port                              int
      ListenerID, RuleID, State         string
      RemovalChangeID                   string
      CreatedAt, UpdatedAt              time.Time
  }
  func liveExposure(ctx context.Context, q storeQueryer) (exposureRow, bool, error) // the live row, false when none
  func insertExposure(ctx context.Context, tx *sql.Tx, r exposureRow) error
  func setExposureState(ctx context.Context, tx *sql.Tx, id, state, removalChangeID string, at time.Time) error // removalChangeID "" leaves the column unchanged
  func deleteExposure(ctx context.Context, tx *sql.Tx, id string) error
  func (r exposureRow) view() ExposureView
  func controllerMigrations() fs.FS
  // RuleID is the firewall rule id of exposure id: "0" + the first 25 Crockford base-32 characters of
  // sha256("darkpanel/panel/" + id), the form of derp/server.RuleID.
  func RuleID(exposureID string) string
  ```

  `storeQueryer` is the package's unexported read interface (`QueryRowContext`, `QueryContext`), satisfied by `*sql.DB` from `db.Read()` and by `*sql.Tx`, as in `cloudflare/store.go`. The table is `STRICT`, as every module table is.

- **Panel agent dependencies** (Task 4, `internal/modules/panel/deps.go`; fake in `internal/modules/panel/fixture_test.go`):

  ```go
  // panelDocker is the Engine API surface the agent actions use; *dockerapi.Client implements it.
  type panelDocker interface {
      ListNetworks(ctx context.Context, f dockerapi.Filters) ([]dockerapi.Network, error)
      InspectNetwork(ctx context.Context, id string) (dockerapi.Network, error)
      CreateNetwork(ctx context.Context, req dockerapi.NetworkCreate) (string, error)
      RemoveNetwork(ctx context.Context, id string) error
      InspectContainer(ctx context.Context, id string) (dockerapi.ContainerJSON, error)
  }
  type agentDeps struct {
      docker func() (panelDocker, error) // a client over the Engine socket; tests return the fake
  }
  func productionAgent() agentDeps
  ```

  Task 4 copies the exact method signatures from `internal/platform/dockerapi` (the names above are the method names; the parameter and result types are the client's own), and the socket path and error wrapping from `npm/deps.go` `productionNM`. The fake (`fdDocker`, `func fdNewDocker() *fdDocker`) keeps networks and containers in maps, records every `NetworkCreate` request in `Created []dockerapi.NetworkCreate` and every removal in `Removed []string`, answers Engine-style not-found errors the way `dockerapi` reports them (Task 4 reads `dockerapi`'s not-found helper and uses it), and has `AddNetwork(dockerapi.Network)` and `AddContainer(dockerapi.ContainerJSON)` helpers. A helper that no test of its own task uses carries `//nolint:unused // shared helper for this package's tests` until a later task uses it.

- **Panel controller fixture** (Task 4, `internal/modules/panel/fixture_test.go`):

  ```go
  // fdDB opens a controller database in t.TempDir() migrated with store.CoreMigrations(store.SideController)
  // and controllerMigrations(), closed at cleanup.
  func fdDB(t *testing.T) *store.DB
  // fdListeners is an in-memory core.Listeners: Open records the spec under ids "l1", "l2", ..., Revoke moves
  // open to revoked (and succeeds on revoked or closed), Close moves revoked to closed, Get answers the row;
  // FailOpen, when set, is returned by Open.
  type fdListeners struct{ Rows map[string]core.ListenerInfo; FailOpen error; next int }
  func fdNewListeners() *fdListeners
  // fdEnv is a controller-side env with DB, Listeners, a fake clock at fdNow and Call set to call.
  func fdEnv(t *testing.T, db *store.DB, l core.Listeners, call core.CallContext) core.Env
  func fdInsertUser(t *testing.T, db *store.DB, name, state string)        // core_users row, role admin
  func fdInsertHost(t *testing.T, db *store.DB, id string, local bool)     // core_hosts row, state enrolled
  func fdInsertChange(t *testing.T, db *store.DB, id, action, state string) // core_plans and core_changes rows
  func fdInsertExposure(t *testing.T, db *store.DB, r exposureRow)
  var fdNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
  ```

- **Action constructors** (Tasks 6, 7, 9, 10; listed in the manifest by Task 11, `internal/modules/panel/actions.go`):

  | Constructor | File (task) | Action | Side, class, flags |
  |---|---|---|---|
  | `networkCreateAction(d agentDeps) core.ActionDef` | `network.go` (6) | `panel.network.create/v1` | agent, compensable with `Inverse` (removes the network it created while no container is attached), lock key `panel`, `MinScope: admin` |
  | `networkRemoveAction(d agentDeps) core.ActionDef` | `network_remove.go` (6) | `panel.network.remove/v1` | agent, compensable, `Converges` (absent counts as removed), `CompensatedBy: ActionNetCreate` (the registry resolves it by name, without a version), lock key `panel`, `MinScope: admin` |
  | `connectorCheckAction(d agentDeps) core.ActionDef` | `connector_check.go` (6) | `panel.connector.check/v1` | agent, internal read (`DefineInternalRead`), `MinScope: admin` |
  | `listenerOpenAction() core.ActionDef` | `listener.go` (7) | `panel.listener.open/v1` | controller, compensable with `Inverse` (revoke, close, delete the row), lock key `panel`, `MinScope: admin` |
  | `listenerRevokeAction() core.ActionDef` | `listener_revoke.go` (7) | `panel.listener.revoke/v1` | controller, compensable, `ForwardOnly`, `Converges`, `BreakGlass`, lock key `panel`, `MinScope: admin` |
  | `statusAction() core.ActionDef` | `status.go` (7) | `panel.exposure.status/v1` | controller read, `MinScope: admin` |
  | `exposureAddAction() core.ActionDef` | `expose_add.go` (9) | `panel.exposure.add/v1` | composite (`DefineComposite`), `Unwind: reverse`, `MinScope: admin` |
  | `exposureRemoveAction() core.ActionDef` | `expose_remove.go` (10) | `panel.exposure.remove/v1` | composite, `Unwind: forward`, `MinScope: admin` |

  Each task's tests call the constructor and drive its handler the way the npm and cloudflare tests drive theirs (plan, then apply, through the module test helpers those packages use); no task edits `panel.go` or `actions.go` except Task 11.

- **Listener open and revoke** (Task 7): `panel.listener.open` refuses `request.invalid_input` unless `env.Call.ParentAction == ActionAdd` and `env.Call.ParentChangeID != ""`; it refuses `panel.already_exposed` when a live exposure exists (checked at plan and again inside the apply's write transaction). Apply calls `env.Listeners.Open(core.ListenerSpec{Addr: <gateway>:<port>, AllowedPeer: <proxy>, ClientHeader: X-Darkpanel-Client for npm or Cf-Connecting-IP for tunnel, PublicHost: <hostname>, ChangeID: env.Call.ParentChangeID})`, then inserts the `panel_exposures` row with `id = env.Call.ParentChangeID`, `rule_id = RuleID(id)`, `state = open`. `panel.listener.revoke` takes the live exposure (none: `panel.not_exposed`), calls `env.Listeners.Revoke` (a revoked or closed listener counts as done), then `env.Listeners.Close`, and sets the row to `revoked` with `removal_change_id = env.Call.ParentChangeID` when that is set; it plans an empty diff when the row is already `revoked` and the listener is not open, and its output always carries the exposure's id, listener id and rule id.

- **Composite steps** (Tasks 9 and 10). `panel.exposure.add`, after the refusals of W8-R10, returns these steps, in this order (`H` is `in.HostID`; `P` the port; `[npm]` and `[tunnel]` steps exist only for that `via`):

  | Step id | Host | Action | Input |
  |---|---|---|---|
  | `public` [npm] | — | `npm.public.check/v1` | `{"hostId": H}` |
  | `connector` [tunnel] | H | `panel.connector.check/v1` | `{"tunnel": T}` |
  | `network` | H | `panel.network.create/v1` | `{}` |
  | `attach` [npm] | H | `npm.network.attach/v1` | `{"network": "darkpanel-panel", "claimant": "panel", "address": "${steps.network.output.proxy}"}` |
  | `attach` [tunnel] | H | `cloudflare.network.attach/v1` | `{"tunnel": T, "network": "darkpanel-panel", "claimant": "panel", "address": "${steps.network.output.proxy}"}` |
  | `listen` | — | `panel.listener.open/v1` | `{"via", "hostname", "hostId", "tunnel" from input, "subnet": "${steps.network.output.subnet}", "gateway": "${steps.network.output.gateway}", "proxy": "${steps.network.output.proxy}", "port": P}` |
  | `cert` [npm] | H | `npm.certificate.request/v1` | `{"domains": [hostname]}` |
  | `proxy` [npm] | H | `npm.proxyhost.add/v1` | `{"domains": [hostname], "forwardScheme": "https", "forwardHost": "${steps.network.output.gateway}", "forwardPort": P, "certificateId": "${steps.cert.output.id}", "sslForced": true, "hsts": true}` |
  | `proxy` [tunnel] | — | `cloudflare.hostname.add/v1` | `{"tunnel": T, "hostname": hostname, "service": "https://${steps.network.output.gateway}:<P>", "noTLSVerify": true}` |
  | `ports` | H | `firewall.rules.apply/v1` | `{"label": "panel exposure <hostname>", "add": [{"id": "${steps.listen.output.ruleId}", "action": "allow", "direction": "in", "proto": "tcp", "from": "${steps.network.output.proxy}", "to": "${steps.network.output.gateway}", "toPort": "<P>", "inIface": "dp-panel", "family": "v4"}]}` |

  `panel.exposure.remove`, after `panel.not_exposed` (no live exposure), reads the live exposure `E` and returns:

  | Step id | Host | Action | Input | Retires |
  |---|---|---|---|---|
  | `revoke` | — | `panel.listener.revoke/v1` | `{}` | `E.ID` |
  | `proxy` [npm] | `E.HostID` | `npm.proxyhost.remove/v1` | `{"domain": E.Hostname}` | |
  | `proxy` [tunnel] | — | `cloudflare.hostname.remove/v1` | `{"hostname": E.Hostname}` | |
  | `attach` [npm] | `E.HostID` | `npm.network.detach/v1` | `{"network": "darkpanel-panel", "claimant": "panel"}` | |
  | `attach` [tunnel] | `E.HostID` | `cloudflare.network.detach/v1` | `{"tunnel": E.Tunnel, "network": "darkpanel-panel", "claimant": "panel"}` | |
  | `network` | `E.HostID` | `panel.network.remove/v1` | `{}` | |
  | `ports` | `E.HostID` | `firewall.rules.apply/v1` | `{"label": "panel exposure <hostname>", "delete": ["${steps.revoke.output.ruleId}"]}` | |

  Both `ports` labels are `"panel exposure " + hostname` cut to the firewall's 64-character label limit. `After` follows the table order for every step. Tasks 9 and 10 copy the exact input field names from the npm, cloudflare and firewall wire types, and correct this table only by a NEEDS_CONTEXT report, never silently. The `ports` steps reference an earlier step's output, so the composite dry run lists them as planned later (W8-R7).

## Assumptions (evidence)

All checked on 2026-11-04 at `227de56` unless stated.

- The composite runner never sets `Env.Call.Retired`: `internal/core/lifecycle/composite_run.go:509` (`stepCall`) and `composite.go:114` set only the parent and step; `core.Step` (`internal/core/contract.go:293`) has no retirement field; `core_composite_steps` (`internal/core/store/migrations/controller/0009_lifecycle_core.sql:80`) has no column for it. The firewall guard already skips a retired `Via` (`internal/modules/firewall/guard/flows.go:233-241`) and the engine already stores and forwards `Retired` (`lifecycle/engine.go:379`, `:473`; `internal/agent/wire/wire.go:70-88`).
- The composite dry run plans every non-read step without step references at composite plan time and lists a step with references as "planned after step …" (`internal/core/lifecycle/composite.go:95-115`); templates may embed references inside strings (`internal/core/lifecycle/refs.go:17-20`).
- A request through a runtime listener gets `ProxyPath.ExposureID = ListenerSpec.ChangeID` (`internal/controller/api/runtime.go:30`); `Revoke` commits `revoked` and closes the listening socket (`internal/controller/listen/runtime.go:157`, `:350-357`), so the guard's controller-listener flow on `dp-panel` (`flows.go:327-382`, proxy = gateway + 1) disappears once the revoke step has applied.
- Break-glass actions are refused only by the API layer (`internal/controller/api/actions.go:206`, `socket.go:244`), not by the composite runner; the generated command of a break-glass action talks to the controller socket with `--socket`/`--dev` (`internal/cli/root.go:240`, `:245-256`) and refuses `--local` (`internal/cli/local.go:58`).
- NPM already serves the panel: `npm.network.attach` takes a fixed IPv4 `address` (`internal/modules/npm/wire.go:57`, `network_claims.go:215`, `:360-366`); `npm.proxyhost.add` writes the `location /` with `X-Darkpanel-Client $realip_remote_addr` and, under parent `panel.exposure.add`, `proxy_read_timeout 3600s` (`npm/proxy/location.go:15-29`, `proxyhost_add.go:156-169`) and lets that parent's upstream sit in the panel network (`npm/proxy/upstream.go:38-59`); `npm.proxyhost.remove` refuses a panel-reserved host unless the parent is `panel.exposure.remove` (`proxyhost_edit.go:20-23`, `:157-169`); `npm.public.check` answers `npm.not_public` (`controller_checks.go:71-110`).
- nginx does not verify an upstream TLS certificate unless `proxy_ssl_verify on` is set (https://nginx.org/en/docs/http/ngx_http_proxy_module.html#proxy_ssl_verify), and no NPM code sets it (no `proxy_ssl_verify` in `internal/modules/npm`), so the internal hop's verification is off. That NPM's proxy include sends `X-Forwarded-Proto $scheme`, which the runtime listener requires (`internal/controller/api/runtime.go:25-60`): unverified — Task 12 verifies it.
- Cloudflare: `cloudflare.network.attach` takes a fixed `address` (`internal/modules/cloudflare/connector_network.go:318-324`) but checks neither the module label (`:212`) nor an Engine-assigned address (`:250-255`) — register row 381; `cloudflare.hostname.add` takes `noTLSVerify` and skips the panel-range check under parent `panel.exposure.add` (`cloudflare/wire.go:89`, `hostname_add.go:22`, `:136-145`); `hostname.remove` admits a panel-reserved hostname under parent `panel.exposure.remove` (`hostname_remove.go:17`, `:75-78`); the connector container is `darkpanel-cloudflared-<tunnel>` (`connector_deps.go:37`).
- `firewall.rules.apply` takes `{guard, overrides, label, add []model.Rule, delete []string, manual}` and is revertible with `Converges` (`internal/modules/firewall/actions_rules.go:20-26`, `:118`; `handler.go:59-64`); `model.Rule` has `id, action, direction, proto, from, toPort, to, inIface, family` (`model/rule.go:120-136`); a deterministic rule id precedent is `derp/server.RuleID` (`internal/modules/derp/server/server.go:237-240`).
- `dockerapi.NetworkCreate` carries `Name, Driver, Internal, Attachable, EnableIPv6 *bool, IPAM *IPAM, Options, Labels` (`internal/platform/dockerapi/networks.go:55-64`); `npm/deps.go:61` and `:373-410` (`reservedVia`) read the panel network's first IPv4 IPAM config only — register row 328, discharged by creating exactly one IPv4 config with IPv6 off (W8-R9).
- `npm_reserved` releases in `accesslist.go:457` and `certificate.go:432` call `dropReservedExposure` (`npm/store.go:150-155`), which deletes every owner's rows — register row 335; the owner-scoped form is `proxyHostEditRelease` (`proxyhost_edit.go:538-543`).
- No module settings store exists (no settings table in `internal/core/store/migrations`; the firewall keeps its own, `internal/modules/firewall/docker_store.go:31`), hence W8-R4.
- Users: `core_users.state` is `pending` or `active` (`internal/core/store/migrations/controller/0003_identity.sql:11`) and every user is an admin (`internal/controller/auth/service.go:23-24`); the controller's host is the `core_hosts` row with `is_local = 1` (`0001_core.sql:12`, `:16`).
- `backup.Mark` closes open runtime listeners and nothing else of the panel (`internal/controller/backup/mark.go:29`, `:70-74`); `VerifyInstalled` does not look at listeners (`validate.go:221`) — register row 391.
- `composite.step` events reach the bus through `lifecycle.Config.Publish = s.events.Publish` (`internal/controller/server/services.go:192-200`), with no visibility check (register row 91); `api.NetworkVisible` is the rule (`internal/controller/api/actions.go:400-407`).
- Web: the panel module entry exists with a `routes` stub (`web/src/modules/registry.ts:52`, `web/src/modules/panel/routes.tsx`); the every-page banner precedent is `web/src/modules/npm/banner.tsx`, rendered by `web/src/shell.tsx:10`, `:100`; controller composites are applied through `ChangeDialog` (`web/src/components/change-dialog.tsx:377`) as `NpmControllerChange` does (`web/src/modules/npm/npm-change.tsx`).
- Harness: the lab helper `m5dLab` boots a controller, an agent and a scanner (`test/harness/m5_derp_test.go:72`), `localHost` finds the controller's host (`test/harness/api.go:183`), the NPM-with-Pebble setup is `harnessSettings` in `m6_npm_test.go:594-642`, `core.audit.list` returns `Source` as `"<client> via <proxy>"` for proxied requests (`internal/controller/coreactions/audit.go:50`, `internal/controller/api/server.go:208-216`), and `vm-m6c` is the job pattern (`.github/workflows/harness.yml:494-559`, checked by `test/harness/m6cw_workflow_unit_test.go`). That the controller VM runs Docker and its local agent can deploy NPM: unverified — Task 12 verifies it first.

- `TestFdRuleID`'s two pinned values (`0T6TGP8XA19D3CXP6GZDFX42FX`, `0JBCM1G2AKVMZ62MEWM3JG206E`) are `RuleID` of `01JC…0001` and `01JC…0002` by the `derp/server.RuleID` algorithm with the prefix `darkpanel/panel/`, recomputed independently on 2026-11-04 with Python's `hashlib.sha256` and `base64.b32encode` translated to the Crockford alphabet `0123456789ABCDEFGHJKMNPQRSTVWXYZ`.
- That `firewall.rules.apply` plans cleanly on the lab controller's own host (UFW enabled by the harness provisioning, Docker integration off) and that the local agent there keeps its database at the production agent path, which the NPM harness settings step writes: unverified — Task 12 verifies both in its first step.
- No task carries an **Executor:** line: the agy executor offer timed out with no answer on 2026-11-04.

**Plan rulings** (Task 11 copies them into spec section 8):

- **W8-R1 retirement.** `core.Step.Retires` names the exposure a step retires; the runner gives later steps `Retired` only once that step is `applied` or `confirmed`, from the stored `core_composite_steps.retires`; only forward composites may set it. Nothing ties the value to the listener the step revoked: composites are first-party module code, and `panel.exposure.remove` is the only one that sets it, on the step that revokes that exposure's listener.
- **W8-R2 exposure id.** The exposure id is the `panel.exposure.add` change id: `panel.listener.open` sets `ListenerSpec.ChangeID` and the row id to `env.Call.ParentChangeID`, so the runtime handler's `Via.ExposureID` and the removal's `Retires` agree.
- **W8-R3 exposure rows.** `panel_exposures` keeps one row per exposure; `open` → `revoked` (revoke) and `open`/`revoked` → `needs_repair` (restore marking, `needs_repair` → `revoked` by revoke); the row is deleted only by `panel.listener.open`'s undo; an exposure is gone once revoked by a removal that completed.
- **W8-R4 port.** The spec's setting `exposurePort` is the optional `port` input of `panel.exposure.add` (default 9443), recorded on the exposure; no settings store is added.
- **W8-R5 pre-checks.** `via: npm` starts with the read step `npm.public.check`; `via: tunnel` starts with the agent internal read `panel.connector.check`, which refuses `cloudflare.connector_missing` unless `darkpanel-cloudflared-<tunnel>` exists, carries `darkpanel.module=cloudflare` and is running.
- **W8-R6 claimant.** Both attachments use claimant `panel`.
- **W8-R7 rule id and ordering.** The firewall rule id is `RuleID(exposure id)`; the add's `ports` step takes it from `listen`'s output and the removal's from `revoke`'s, so the removal's firewall step is planned only after the listener is revoked and closed.
- **W8-R8 break-glass command.** The break-glass path is the generated `sudo darkpanel panel listener revoke`, which talks to the controller socket; the spec's `--local` wording is replaced.
- **W8-R9 network.** `panel.network.create` picks the highest /29 in `172.31.255.0/24` that overlaps no IPv4 subnet of any Docker network on the host, creates `darkpanel-panel` with `EnableIPv6` false and exactly that one IPAM config, and refuses `panel.already_exposed` when a network named `darkpanel-panel` exists; `panel.network.remove` refuses `docker.owned` for a `darkpanel-panel` network without `darkpanel.module=panel`.
- **W8-R10 add refusals.** In `Steps`, in this order: input validation; `panel.wrong_host` unless `hostId` is the `is_local` host; `panel.totp_incomplete` while any `core_users` row is `pending`; `panel.already_exposed` while a live exposure exists.
- **W8-R11 removal refusal.** `panel.not_exposed` when no live exposure exists; a `needs_repair` exposure is removable.
- **W8-R12 step events.** A `composite.step` event of a composite the network listeners hide is not published (register row 91).
- **W8-R13 banner.** `web/src/modules/panel/banner.tsx` renders above every page from `panel.exposure.status`, linking to the controller host's panel page, which offers removal.
- **W8-R14 attachments.** `cloudflare.network.attach` and `.detach` refuse `docker.owned` for a connector container without `darkpanel.module=cloudflare`, and attach refuses `request.invalid_input` when the connector is already on the network at an address other than the requested one, whether fixed or Engine-assigned (register row 381).
- **W8-R15 reservation release.** NPM's access-list and certificate releases delete only the releasing owner's `npm_reserved` rows (register row 335).
- **W8-R16 restore.** `backup.Mark` sets every live exposure to `needs_repair`; `backup.VerifyInstalled` fails when a live exposure is in another state (register row 391).

## Task index

1. core: composite step retirement
2. npm: owner-scoped reservation release
3. cloudflare: connector label check and fixed-address attach
4. panel: wire types, exposure store, agent deps and fixtures
5. web: panel exposure page and every-page banner
6. panel: network create and remove, connector check
7. panel: listener open and revoke, exposure status
8. backup: exposures in restore marking and verification
9. panel: exposure.add composite
10. panel: exposure.remove composite
11. panel: register the actions, schemas and spec rulings
12. harness: TestM6Panel and the vm-m6p job
13. server: hide step events of hidden composites

---

### Task 1: core: composite step retirement

**Files:**
- Modify: `internal/core/contract.go` (`Step`, near the line `type Step struct`)
- Create: `internal/core/store/migrations/controller/0014_composite_retires.sql`
- Modify: `internal/core/lifecycle/composite.go` (`compositeSteps`)
- Modify: `internal/core/lifecycle/composite_run.go` (`stepRow`, `loadComposite`, `startStep`, the intent insert of `core_composite_steps`)
- Test: `internal/core/lifecycle/composite_retire_test.go` (new), `internal/core/lifecycle/composite_fixture_test.go` (one helper gains a parameter), `internal/core/store/migrations_test.go`
- Read only: `internal/core/store/migrations/controller/0009_lifecycle_core.sql` and `0013_restore_quarantine.sql` (naming precedent), `internal/core/lifecycle/composite_run.go` `stepCall`, `internal/core/lifecycle/engine.go` (`Plan` and `storedCall` already carry `Retired`)

**Interfaces:**
- Consumes: nothing from another task. The engine already stores and forwards `core.CallContext.Retired` (`engine.go` `Plan`, `storedCall`).
- Produces: Contracts "Step retirement" (`core.Step.Retires`, migration `0014_composite_retires.sql`, the registry-time refusal, the runner's `Env.Call.Retired`). Task 10 sets `Retires` on the `revoke` step of `panel.exposure.remove`.

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 3 = 8

This is the integrator task: Global Constraints allow exactly these edits in `internal/core/**`.

- [ ] **Step 1: Write the failing tests**

1. Create `internal/core/lifecycle/composite_retire_test.go`. It registers a one-action module `rtm` whose handler records the `CallContext` it sees, and drives the existing `cfx.bad.drop` (forward) and `cfx.bad.run` (reverse) composites of the composite fixture, whose steps a test sets.

```go
package lifecycle

import (
	"context"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

var refRtPut = core.ActionRef{Name: "rtm.mark.put", Version: 1} // forward only, converges, records the call context it sees

// rtCall is one Plan or Apply of rtm.mark.put: the item it named and the Retired list of its call context. Run is set
// when a composite's runner made the call, not the composite's own dry run.
type rtCall struct {
	Phase, Name string
	Run         bool
	Retired     []string
}

type rtHost struct {
	mu    sync.Mutex
	calls []rtCall
}

func (h *rtHost) record(phase, name string, call core.CallContext) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.calls = append(h.calls, rtCall{Phase: phase, Name: name, Run: call.ParentChangeID != "", Retired: slices.Clone(call.Retired)})
}

// of returns the calls of a phase, by item name; dry selects the composite's own dry runs instead of the runner's.
func (h *rtHost) of(phase string, dry bool) map[string][]rtCall {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := map[string][]rtCall{}
	for _, c := range h.calls {
		if c.Phase == phase && c.Run == !dry {
			out[c.Name] = append(out[c.Name], c)
		}
	}
	return out
}

type rtIn struct {
	Name string `json:"name"`
}

type rtOut struct {
	Name string `json:"name"`
}

type rtPut struct{ h *rtHost }

func (x rtPut) Plan(_ context.Context, env core.Env, in rtIn) (core.PlanOutcome, error) {
	x.h.record("plan", in.Name, env.Call)
	return core.PlanOutcome{Diff: []core.DiffLine{{Op: "+", Text: in.Name}}, StateHash: "rt"}, nil
}

func (x rtPut) Apply(_ context.Context, env core.Env, in rtIn) (rtOut, error) {
	x.h.record("apply", in.Name, env.Call)
	return rtOut{Name: in.Name}, nil
}

func (rtPut) Verify(context.Context, core.Env, rtIn, rtOut) error { return nil }

type rtModule struct{ h *rtHost }

func (m rtModule) Manifest() core.Manifest {
	return core.Manifest{ID: "rtm", Version: 1, Actions: []core.ActionDef{
		core.DefineMutating(core.ActionSpec{
			Name: refRtPut.Name, Version: 1, Class: core.ClassCompensable, Side: core.SideAgent,
			LockKeys: []string{"rtm"}, ForwardOnly: true, Converges: true,
		}, rtPut(m)),
	}}
}

// rtStep is a step of rtm.mark.put on testHost that retires exposure.
func rtStep(id, retires string, after ...string) core.Step {
	s := cstStep(id, refRtPut, `{"name":"`+id+`"}`, after...)
	s.Retires = retires
	return s
}

func TestRtRunnerGivesLaterStepsTheRetiredExposures(t *testing.T) {
	h := &rtHost{}
	f := newCfxFixture(t, rtModule{h})
	f.bad.set(
		rtStep("a", "exp1"),
		rtStep("b", "", "a"),
		rtStep("c", "exp2", "b"),
		rtStep("d", "exp1", "c"),
		rtStep("e", "", "d"),
	)
	pl, err := f.planC(refCfxDrop, `{"name":"x"}`)
	if err != nil {
		t.Fatal(err)
	}
	dry := h.of("plan", true)
	if len(dry) != 5 {
		t.Fatalf("the composite's dry run planned %d steps, want 5", len(dry))
	}
	for name, calls := range dry {
		for _, c := range calls {
			if len(c.Retired) != 0 {
				t.Errorf("the dry run of step %s had Retired %v, want none", name, c.Retired)
			}
		}
	}

	v, err := f.ctrl.Apply(context.Background(), cfxRoot, pl.PlanID, core.ApplyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if done := f.await(v.ID, core.StateCompleted, core.StateNeedsRepair, core.StateFailed, core.StateReverted); done.State != core.StateCompleted {
		t.Fatalf("composite = %s (%+v), want completed", done.State, done.Error)
	}
	want := map[string][]string{
		"a": nil,
		"b": {"exp1"},
		"c": {"exp1"},
		"d": {"exp1", "exp2"},
		"e": {"exp1", "exp2"},
	}
	for _, phase := range []string{"plan", "apply"} {
		got := h.of(phase, false)
		for name, retired := range want {
			if len(got[name]) == 0 {
				t.Errorf("%s of step %s never ran", phase, name)
			}
			for _, c := range got[name] {
				if !slices.Equal(c.Retired, retired) {
					t.Errorf("%s of step %s had Retired %v, want %v", phase, name, c.Retired, retired)
				}
			}
		}
	}
	steps := f.stepsOf(v.ID)
	for id, retires := range map[string]string{"a": "exp1", "b": "", "c": "exp2", "d": "exp1", "e": ""} {
		if steps[id].Retires != retires {
			t.Errorf("stored retires of step %s = %q, want %q", id, steps[id].Retires, retires)
		}
	}
}

func TestRtStepRetired(t *testing.T) {
	row := func(id string, ordinal int, state, retires string) stepRow {
		return stepRow{ID: id, Ordinal: ordinal, State: state, Retires: retires}
	}
	steps := []stepRow{
		row("a", 0, core.StepConfirmed, "exp1"),
		row("b", 1, core.StepApplied, "exp2"),
		row("c", 2, core.StepApplying, "exp3"),
		row("d", 3, core.StepPlanned, "exp4"),
		row("e", 4, core.StepApplied, "exp1"),
		row("f", 5, core.StepUndone, "exp5"),
		row("g", 6, core.StepRead, ""),
		row("h", 7, core.StepPending, ""),
	}
	for _, tc := range []struct {
		name string
		step int
		want []string
	}{
		{"the first step has nothing retired before it", 0, nil},
		{"a confirmed step counts", 1, []string{"exp1"}},
		{"an applying step does not count yet", 3, []string{"exp1", "exp2"}},
		{"a planned step does not count", 4, []string{"exp1", "exp2"}},
		{"an exposure retired twice is listed once, in ordinal order", 5, []string{"exp1", "exp2"}},
		{"an undone step does not count", 7, []string{"exp1", "exp2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := stepRetired(steps, steps[tc.step]); !slices.Equal(got, tc.want) {
				t.Errorf("stepRetired = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRtRegistryRefusesAMisplacedRetires(t *testing.T) {
	retiring := func(s core.Step) core.Step {
		s.Retires = "exp1"
		return s
	}
	for _, tc := range []struct {
		name  string
		ref   core.ActionRef
		steps []core.Step
		want  string
	}{
		{"a reverse composite", refCfxBad,
			[]core.Step{retiring(cstStep("a", refCstAdd, `{"name":"x","value":"1"}`))},
			"step a: retires is allowed only on a step of a forward composite"},
		{"a read step", refCfxDrop,
			[]core.Step{cstStep("a", refCstDel, `{"name":"x"}`), retiring(cstStep("r", refCstGet, `{"name":"x"}`, "a"))},
			"step r: retires is not allowed on a read step"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newCfxFixture(t)
			f.bad.set(tc.steps...)
			_, err := f.planC(tc.ref, `{"name":"x"}`)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("Plan error = %v, want one containing %q", err, tc.want)
			}
		})
	}
}
```

2. In `internal/core/lifecycle/composite_fixture_test.go` let the fixture take extra modules. Replace the signature line `func newCfxFixture(t *testing.T) *cfxFixture {` with `func newCfxFixture(t *testing.T, extra ...core.Module) *cfxFixture {`, and in its body replace `reg, err := core.NewRegistry(cstModule{host}, cfxModule{bad})` with `reg, err := core.NewRegistry(append([]core.Module{cstModule{host}, cfxModule{bad}}, extra...)...)`. Every existing caller passes only `t` and is unchanged.

3. In `internal/core/store/migrations_test.go` add `"fmt"` to the import block (after `"encoding/json"`) and append:

```go
func TestRtCompositeRetiresMigration(t *testing.T) {
	ctx := context.Background()
	db := openTemp(t)
	opts := migrateOpts(t, "w8", time.Now())
	opts.Side = SideController
	if _, err := Migrate(ctx, db, opts, coreSetUpTo(t, SideController, 13), nil); err != nil {
		t.Fatal(err)
	}
	lifecycleRows(t, db)
	const at = "2026-11-04T00:00:00.000000000Z"
	const step = `INSERT INTO core_composite_steps (change_id, step_id, ordinal, host_id, action, input_template, state, updated_at%s)
		VALUES ('C1', ?, ?, 'H1', 'fix.item.add/v1', '{}', 'applied', ?%s)`
	if _, err := db.Writer().ExecContext(ctx, fmt.Sprintf(step, "", ""), "old", 0, at); err != nil {
		t.Fatal(err)
	}

	set, err := CoreMigrations(SideController)
	if err != nil {
		t.Fatal(err)
	}
	res, err := Migrate(ctx, db, migrateOpts(t, "w8a", time.Now()), set, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Applied) == 0 || res.Applied[0].Number != 14 || res.Applied[0].Name != "composite_retires" {
		t.Fatalf("applied %+v, want 0014_composite_retires first", res.Applied)
	}

	retires := func(id string) string {
		t.Helper()
		var got string
		if err := db.Read().QueryRowContext(ctx, `SELECT retires FROM core_composite_steps WHERE step_id = ?`, id).Scan(&got); err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := retires("old"); got != "" {
		t.Errorf("a step recorded before the migration retires %q, want the empty default", got)
	}
	if _, err := db.Writer().ExecContext(ctx, fmt.Sprintf(step, ", retires", ", ?"), "new", 1, at, "exp1"); err != nil {
		t.Fatal(err)
	}
	if got := retires("new"); got != "exp1" {
		t.Errorf("a step recorded with retires exp1 reads back %q", got)
	}
	if _, err := db.Writer().ExecContext(ctx, `UPDATE core_composite_steps SET retires = NULL WHERE step_id = 'new'`); err == nil {
		t.Error("a NULL retires was accepted")
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/1 && export TMPDIR=/root/.cache/w8-tmp/1 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestRt' ./internal/core/lifecycle/ ./internal/core/store/
```

Expected: `./internal/core/lifecycle/` fails to build with `unknown field Retires in struct literal of type core.Step` or `s.Retires undefined (type core.Step has no field or method Retires)`; `./internal/core/store/` fails `TestRtCompositeRetiresMigration` with `applied [], want 0014_composite_retires first`. Quote these lines in the report.

- [ ] **Step 3: Implement**

1. `internal/core/contract.go`, `type Step struct`: add the field exactly as Contracts "Step retirement" gives it, `Retires string` with tag `json:"retires,omitempty"` and the comment `// exposure id this step retires once it has committed (1.5); forward composites only`, after `After`. Run `gofmt -w` on the file; it re-aligns the other fields.
2. Create `internal/core/store/migrations/controller/0014_composite_retires.sql` with a two-line comment saying the column holds the exposure id a forward composite's step retires once committed (empty for every other step), then this statement, which is the whole migration:

```sql
ALTER TABLE core_composite_steps ADD COLUMN retires TEXT NOT NULL DEFAULT '';
```

3. `internal/core/lifecycle/composite.go`, `compositeSteps`: directly after the `switch` that checks a step's host against the action's side, and before the `switch cdef.Spec.Class`, refuse a non-empty `st.Retires` as a module bug, using the function's `bug` helper, in this order: when `def.Spec.Unwind` is not `core.UnwindForward`, `bug("step %s: retires is allowed only on a step of a forward composite", st.ID)`; when `cdef.Spec.Class` is `core.ClassRead`, `bug("step %s: retires is not allowed on a read step", st.ID)`. The text is pinned by the test.
4. `internal/core/lifecycle/composite_run.go`:
   - Add `Retires string` to `stepRow` (after `After`). In `loadComposite` add `retires` to the end of the step `SELECT` list and scan it into `s.Retires`.
   - In the composite intent transaction (the `INSERT INTO core_composite_steps` loop), add the `retires` column and write `s.Step.Retires`.
   - Add `func stepRetired(steps []stepRow, s stepRow) []string` with a doc comment: it returns the non-empty `Retires` values, in ordinal order and without duplicates, of every step of `steps` whose `Ordinal` is lower than `s.Ordinal` and whose `State` is `core.StepApplied` or `core.StepConfirmed`; nil when there is none. Any other state (applying, planned, undone, released, read, pending) does not count: the exposure is retired only once the retiring step has committed.
   - In `startStep`, directly after `call := e.stepCall(c, s)`, set `call.Retired = stepRetired(steps, s)`. The plan request and the apply options both use this `call`, so a step is planned and applied with the same list. Leave `stepCall` and its other callers (undo and release in `composite_unwind.go` and `composite_ops.go`) unchanged: only forward composites retire, and they never undo. Do not touch `compositePlan` in `composite.go`: the composite's dry run passes no `Retired`.
- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/1 && export TMPDIR=/root/.cache/w8-tmp/1 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestRt' ./internal/core/lifecycle/ ./internal/core/store/
```

Expected: every package prints `ok`. Then the whole task gate of Global Constraints, with its real output recorded:

```bash
mkdir -p /root/.cache/w8-tmp/1 && export TMPDIR=/root/.cache/w8-tmp/1 GOTOOLCHAIN=local
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/core/lifecycle/ ./internal/core/store/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
go clean -testcache && rm -rf /root/.cache/w8-tmp/1
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/core/contract.go internal/core/store/migrations/controller/0014_composite_retires.sql internal/core/lifecycle/composite.go internal/core/lifecycle/composite_run.go internal/core/lifecycle/composite_retire_test.go internal/core/lifecycle/composite_fixture_test.go internal/core/store/migrations_test.go
git commit -m "feat(lifecycle): retire exposures per composite step"
```

### Task 2: npm: owner-scoped reservation release

**Files:**
- Modify: `internal/modules/npm/accesslist.go` (`accessListRemoveHandler.Apply`)
- Modify: `internal/modules/npm/certificate.go` (`certificateRemoveHandler.Apply`)
- Modify: `internal/modules/npm/store.go` (delete `dropReservedExposure`)
- Test: `internal/modules/npm/accesslist_test.go`, `internal/modules/npm/certificate_test.go`, `internal/modules/npm/store_test.go`
- Read only: `internal/modules/npm/proxyhost_edit.go` (`proxyHostEditRelease`, the owner-scoped release both handlers will use)

**Interfaces:**
- Consumes: nothing from another task.
- Produces: nothing a task names. After this task, releasing an access list or a certificate under its owner's removal deletes only that owner's `npm_reserved` rows under the exposure (W8-R15), so `panel.exposure.remove` (Task 10) cannot release the rows of `npm.admin.expose` under the same hostname.

**Items:** 335

**Depends on:** none
**Implementer:** dforge:impl-sonnet-medium
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 1 = 4

- [ ] **Step 1: Write the failing tests**

1. Append to `internal/modules/npm/accesslist_test.go` (the file already imports `context`, `database/sql` and `npmapi`):

```go
// rrReserve reserves an NPM object under the exposure hostname the reservation fixtures share.
func rrReserve(t *testing.T, h *thHost, kind string, id int, owner string) {
	t.Helper()
	ctx := context.Background()
	err := h.db.Write(ctx, func(tx *sql.Tx) error {
		return addReserved(ctx, tx, reservedRow{Kind: kind, ObjectID: id, Owner: owner, Exposure: "npm.example.com", ChangeID: "c0"})
	})
	if err != nil {
		t.Fatalf("reserve %s %d: %v", kind, id, err)
	}
}

// rrWantReserved checks which of the objects are still reserved, and by whom; an owner "" means released.
func rrWantReserved(t *testing.T, h *thHost, want []reservedRow) {
	t.Helper()
	for _, w := range want {
		got, held, err := reservedOf(context.Background(), h.db.Read(), w.Kind, w.ObjectID)
		if err != nil {
			t.Fatalf("reservedOf(%s, %d): %v", w.Kind, w.ObjectID, err)
		}
		if w.Owner == "" && held {
			t.Errorf("%s %d is still reserved by %s, want it released", w.Kind, w.ObjectID, got.Owner)
		}
		if w.Owner != "" && (!held || got.Owner != w.Owner) {
			t.Errorf("%s %d: reserved = %v by %q, want it kept for %s", w.Kind, w.ObjectID, held, got.Owner, w.Owner)
		}
	}
}

// TestRrAccessListRemoveReleasesOnlyItsOwnersReservations: removing a reserved list as its owner's removal releases the
// owner's records under the exposure and keeps another owner's records under the same hostname.
func TestRrAccessListRemoveReleasesOnlyItsOwnersReservations(t *testing.T) {
	ctx := context.Background()
	h, n := alSetup(t)
	list := h.npm.SeedAccessList(npmapi.AccessList{Name: "admin-only", Clients: []npmapi.AccessClient{{Address: "203.0.113.7", Directive: "allow"}}})
	alReserve(t, h, list.ID, "npm.admin.expose")
	rrReserve(t, h, "proxy_host", 41, "npm.admin.expose")
	rrReserve(t, h, "proxy_host", 42, "panel.exposure.add")
	rrReserve(t, h, "certificate", 43, "panel.exposure.add")
	h.addChange("c3", "npm.accesslist.remove")
	if _, err := (accessListRemoveHandler{n: n}).Apply(ctx, h.child("c3", "npm.admin.unexpose"), AccessListRemoveInput{ID: list.ID}); err != nil {
		t.Fatalf("Apply as the owner's removal: %v", err)
	}
	rrWantReserved(t, h, []reservedRow{
		{Kind: "access_list", ObjectID: list.ID},
		{Kind: "proxy_host", ObjectID: 41},
		{Kind: "proxy_host", ObjectID: 42, Owner: "panel.exposure.add"},
		{Kind: "certificate", ObjectID: 43, Owner: "panel.exposure.add"},
	})
}
```

2. Append to `internal/modules/npm/certificate_test.go`:

```go
// TestRrCertificateRemoveReleasesOnlyItsOwnersReservations: removing a reserved certificate as its owner's removal
// releases the owner's records under the exposure and keeps another owner's records under the same hostname.
func TestRrCertificateRemoveReleasesOnlyItsOwnersReservations(t *testing.T) {
	ctx := context.Background()
	h, n, _ := ceSetup(t)
	cert := h.npm.SeedCertificate(npmapi.Certificate{NiceName: "panel.example.com", Provider: "letsencrypt", DomainNames: []string{"panel.example.com"}})
	ceReserve(t, h, cert.ID, "panel.exposure.add")
	rrReserve(t, h, "proxy_host", 41, "panel.exposure.add")
	rrReserve(t, h, "proxy_host", 42, "npm.admin.expose")
	rrReserve(t, h, "access_list", 43, "npm.admin.expose")
	h.addChange("c3", "npm.certificate.remove")
	if _, err := (certificateRemoveHandler{n: n}).Apply(ctx, h.child("c3", "panel.exposure.remove"), CertificateRemoveInput{ID: cert.ID}); err != nil {
		t.Fatalf("Apply as the owner's removal: %v", err)
	}
	rrWantReserved(t, h, []reservedRow{
		{Kind: "certificate", ObjectID: cert.ID},
		{Kind: "proxy_host", ObjectID: 41},
		{Kind: "proxy_host", ObjectID: 42, Owner: "npm.admin.expose"},
		{Kind: "access_list", ObjectID: 43, Owner: "npm.admin.expose"},
	})
}
```

3. In `internal/modules/npm/store_test.go`, `TestSdReservedRows`, the function under test is about to be deleted, so move its two calls to the owner-scoped release the handlers will use. Replace `return dropReservedExposure(ctx, tx, "npm.example.org")` with `return proxyHostEditRelease(ctx, tx, "npm.example.org", "npm.admin.expose")` and `return dropReservedExposure(ctx, tx, "nobody.example.org")` with `return proxyHostEditRelease(ctx, tx, "nobody.example.org", "npm.admin.expose")`. The test's expectations stay as they are: both `npm.admin.expose` rows of `npm.example.org` are released, the `panel.exposure.add` row of `panel.example.org` stays, and releasing an exposure with no rows is not an error.

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/2 && export TMPDIR=/root/.cache/w8-tmp/2 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestRr|TestSdReservedRows' ./internal/modules/npm/
```

Expected: `TestRrAccessListRemoveReleasesOnlyItsOwnersReservations` fails with `proxy_host 42: reserved = false by "", want it kept for panel.exposure.add` (and the same for certificate 43), and `TestRrCertificateRemoveReleasesOnlyItsOwnersReservations` fails with `proxy_host 42: reserved = false by "", want it kept for npm.admin.expose`; `TestSdReservedRows` passes. Quote the lines in the report.

- [ ] **Step 3: Implement**

1. `accessListRemoveHandler.Apply` in `accesslist.go` and `certificateRemoveHandler.Apply` in `certificate.go`: in the `if reserved {` block replace the call `dropReservedExposure(ctx, tx, row.Exposure)` with `proxyHostEditRelease(ctx, tx, row.Exposure, row.Owner)`, and return its error as it is (it already starts with `npm:`; drop the extra `fmt.Errorf("npm: %w", err)` wrap). Keep `view.Reserved = row.Owner`. Update both doc comments from "releases every object of its exposure" to say it releases the objects its owner reserved under the exposure. Remove any import that becomes unused.
2. `store.go`: delete `dropReservedExposure`; nothing else calls it after step 1 and the `store_test.go` edit.
3. Do not change `proxyHostEditRelease`.

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/2 && export TMPDIR=/root/.cache/w8-tmp/2 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestRr|TestSdReservedRows|TestAl|TestCe' ./internal/modules/npm/
```

Expected: `ok`. Then the task gate of Global Constraints:

```bash
mkdir -p /root/.cache/w8-tmp/2 && export TMPDIR=/root/.cache/w8-tmp/2 GOTOOLCHAIN=local
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/npm/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
go clean -testcache && rm -rf /root/.cache/w8-tmp/2
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/npm/accesslist.go internal/modules/npm/certificate.go internal/modules/npm/store.go internal/modules/npm/accesslist_test.go internal/modules/npm/certificate_test.go internal/modules/npm/store_test.go
git commit -m "fix(npm): release only the owner's reservations"
```

### Task 3: cloudflare: connector label check and fixed-address attach

**Files:**
- Modify: `internal/modules/cloudflare/connector_network.go` (`networkAttachHandler.check`, `attachFixedOf`)
- Modify: `internal/modules/cloudflare/connector_network_detach.go` (`networkDetachHandler.detachCheck`)
- Modify: `internal/modules/cloudflare/connector_run.go` (`foreignConnector` message)
- Test: `internal/modules/cloudflare/connector_network_test.go`, `internal/modules/cloudflare/connector_network_detach_test.go`
- Read only: `internal/modules/cloudflare/connector_testhost_test.go` (`agRunning`, `agLabelled`, `agOnNetwork`, the fake Engine's `ConnectNetwork`, which records a fixed address as the endpoint's `IPAddress` or `GlobalIPv6Address`)

**Interfaces:**
- Consumes: nothing from another task.
- Produces: the behaviour of W8-R14 that `panel.exposure.add` and `.remove` (Tasks 9 and 10) rely on: `cloudflare.network.attach` and `cloudflare.network.detach` refuse `docker.owned` for a connector container without the label `darkpanel.module=cloudflare`, and attach refuses `request.invalid_input` (field `/address`) when the connector is already on the network at an address other than the requested one, whether the address was fixed or assigned by the Engine.

**Items:** 381

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

- [ ] **Step 1: Write the failing tests**

1. In `connector_network_test.go`, `newNA`: the connector fixture must carry the label the module gives its containers, or every existing attach test would meet the new refusal. Replace the line
   `conn := agContainer(connectorName(naTunnel), func(c *dockerapi.ContainerJSON) { c.State.Status, c.State.Running = "running", true })`
   with
   `conn := agContainer(connectorName(naTunnel), agRunning, agLabelled(map[string]string{core.LabelModule: ID}))`.
2. In `connector_network_detach_test.go`, `newND`: make the same replacement for `connectorName(ndTunnel)`.
3. In `connector_network_test.go`, `TestNaAddressOfADynamicEndpoint`: only the matching address is still accepted, the others are refused by the new test below. Replace its doc comment with `// TestNaAddressOfADynamicEndpoint: a connector on the network at the address the Engine assigned is claimed with no Engine call when the input asks for that same address, and the plan shows no address.` and the loop header `for _, addr := range []string{"172.20.0.9", "172.20.0.10", "fd00::10"} {` with `for _, addr := range []string{"172.20.0.9"} {`.
4. Append to `connector_network_test.go` (it already imports `strings`, `netip`, `core`, `errcode` and `dockerapi`):

```go
// alRelabel gives the container of the tunnel's connector the labels.
func alRelabel(t *testing.T, h *agHost, tunnel string, labels map[string]string) {
	t.Helper()
	c, err := h.docker.InspectContainer(context.Background(), connectorName(tunnel))
	if err != nil {
		t.Fatal(err)
	}
	c.Config.Labels = labels
	h.docker.putContainer(c)
}

// alForeign are the connector containers the module did not label, and what their refusal says.
var alForeign = []struct {
	name   string
	labels map[string]string
	detail string
}{
	{"an unlabelled container", nil, "is not labelled darkpanel.module=cloudflare"},
	{"a container of another module", map[string]string{core.LabelModule: "npm"}, "belongs to module npm"},
}

// TestAlAttachRefusesAForeignConnector: a container of the connector's name that does not carry the label
// darkpanel.module=cloudflare is refused with docker.owned at plan and at apply, and nothing is connected or claimed.
func TestAlAttachRefusesAForeignConnector(t *testing.T) {
	ctx := context.Background()
	for _, foreign := range alForeign {
		t.Run(foreign.name, func(t *testing.T) {
			h := newNA(t)
			alRelabel(t, h, naTunnel, foreign.labels)
			mark := len(h.docker.calls())
			_, err := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput())
			coded := naRefused(t, err, errcode.DockerOwned)
			if !strings.Contains(coded.Detail, foreign.detail) || !strings.Contains(coded.Detail, connectorName(naTunnel)) {
				t.Errorf("plan detail %q, want the container's name and %q", coded.Detail, foreign.detail)
			}
			_, err = h.cf().networkAttach().Handler.Apply(ctx, h.env("c1"), naInput())
			_ = naRefused(t, err, errcode.DockerOwned)
			if got := naMutated(h, mark); len(got) != 0 || len(naClaims(t, h)) != 0 || naOnWeb(t, h) {
				t.Errorf("the refusal changed the host: calls %v, claims %+v, on web %t", got, naClaims(t, h), naOnWeb(t, h))
			}
		})
	}
}

// TestAlAttachAddressOfAnExistingEndpoint: a connector already on the network at addresses the Engine assigned is
// refused any other address with invalid_input at /address, whichever family, and keeps its endpoint and claims; it is
// claimed with no Engine call when the input asks for one of its addresses.
func TestAlAttachAddressOfAnExistingEndpoint(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name   string
		v6     string // the IPv6 address the Engine assigned, "" for none
		ask    string
		refuse bool
	}{
		{"another IPv4 address", "", "172.20.0.10", true},
		{"an IPv6 address when only IPv4 is assigned", "", "fd00::10", true},
		{"another IPv6 address", "fd00::9", "fd00::10", true},
		{"the assigned IPv6 address", "fd00::9", "fd00::9", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newNA(t)
			c, err := h.docker.InspectContainer(ctx, connectorName(naTunnel))
			if err != nil {
				t.Fatal(err)
			}
			agOnNetwork("web", "172.20.0.9")(&c)
			if tc.v6 != "" {
				c.NetworkSettings.Networks["web"].GlobalIPv6Address = netip.MustParseAddr(tc.v6)
			}
			h.docker.putContainer(c)
			ask := func(in *NetworkAttachInput) { in.Address = netip.MustParseAddr(tc.ask) }
			mark := len(h.docker.calls())
			_, planErr := h.cf().networkAttach().Handler.Plan(ctx, h.env(""), naInput(ask))
			_, applyErr := naApply(t, h, "c1", ask)
			if !tc.refuse {
				if planErr != nil || applyErr != nil {
					t.Fatalf("plan error %v, apply error %v; want both to accept", planErr, applyErr)
				}
				if got := naMutated(h, mark); len(got) != 0 || len(naClaims(t, h)) != 1 {
					t.Errorf("calls %v, claims %+v; want no Engine call and one claim", got, naClaims(t, h))
				}
				return
			}
			for stage, err := range map[string]error{"plan": planErr, "apply": applyErr} {
				coded := naRefused(t, err, errcode.InvalidInput)
				if len(coded.Fields) != 1 || coded.Fields[0].Path != "/address" {
					t.Errorf("%s fields %+v, want one at /address", stage, coded.Fields)
				}
			}
			if got := naMutated(h, mark); len(got) != 0 || len(naClaims(t, h)) != 0 {
				t.Errorf("the refusal changed the host: calls %v, claims %+v", got, naClaims(t, h))
			}
		})
	}
}
```

5. Append to `connector_network_detach_test.go` (it already imports `strings`):

```go
// TestAlDetachRefusesAForeignConnector: a container of the connector's name that does not carry the label
// darkpanel.module=cloudflare is refused with docker.owned at plan and at apply, and the claim and the endpoint stay.
func TestAlDetachRefusesAForeignConnector(t *testing.T) {
	ctx := context.Background()
	for _, foreign := range alForeign {
		t.Run(foreign.name, func(t *testing.T) {
			h := newND(t)
			ndJoin(t, h, "web", "172.20.0.5")
			ndAddClaim(t, h, claimRow{TunnelID: ndTunnel, Network: "web", Claimant: ndClaimant, ChangeID: "c0"})
			alRelabel(t, h, ndTunnel, foreign.labels)
			mark := len(h.docker.calls())
			_, err := h.cf().networkDetach().Handler.Plan(ctx, h.env(""), ndInput())
			coded := ndRefused(t, err, errcode.DockerOwned)
			if !strings.Contains(coded.Detail, foreign.detail) || !strings.Contains(coded.Detail, connectorName(ndTunnel)) {
				t.Errorf("plan detail %q, want the container's name and %q", coded.Detail, foreign.detail)
			}
			_, err = h.cf().networkDetach().Handler.Apply(ctx, h.env("c1"), ndInput())
			_ = ndRefused(t, err, errcode.DockerOwned)
			if got := ndMutatedAfter(h, mark); len(got) != 0 || len(ndClaims(t, h, ndTunnel)) != 1 || !ndOn(t, h, "web") {
				t.Errorf("the refusal changed the host: calls %v, claims %+v, on web %t", got, ndClaims(t, h, ndTunnel), ndOn(t, h, "web"))
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/3 && export TMPDIR=/root/.cache/w8-tmp/3 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestAl|TestNa|TestNd' ./internal/modules/cloudflare/
```

Expected: `TestAlAttachRefusesAForeignConnector` and `TestAlDetachRefusesAForeignConnector` fail with `error <nil>, want a Definite docker.owned` for both foreign cases, and three subtests of `TestAlAttachAddressOfAnExistingEndpoint` (`another_IPv4_address`, `an_IPv6_address_when_only_IPv4_is_assigned`, `another_IPv6_address`) fail with `error <nil>, want a Definite request.invalid_input`; every `TestNa` and `TestNd` test passes. Quote the lines in the report.

- [ ] **Step 3: Implement**

1. `connector_run.go`, `foreignConnector`: attach and detach now use it, so both messages end with `; Darkpanel does not manage it` instead of `; Darkpanel does not remove it`, and its doc comment says the module did not create the container, so Darkpanel does not change or remove it. The prefixes `is not labelled darkpanel.module=cloudflare` and `belongs to module <owner>` and the container name stay: `TestCpForeignContainer` and `TestCrUndoLeavesAForeignContainer` pin them.
2. `connector_network.go`, `networkAttachHandler.check`: right after the successful `InspectContainer` of the connector (and before the network is inspected), return `foreignConnector(c, name)` when it is non-nil; it is already a Definite `docker.owned`.
3. `connector_network.go`: replace `attachFixedOf` (its only caller is `check`) with `attachAddrsOf(ep *dockerapi.EndpointSettings) []netip.Addr`, which returns every valid address of the endpoint without duplicates: `IPAddress`, `GlobalIPv6Address`, and the `IPAMConfig` fixed `IPv4Address` and `IPv6Address` when `IPAMConfig` is set. In `check`, when `in.Address.IsValid()` and the connector is already on the network, refuse when that list is not empty and does not contain `in.Address`: the same Definite `attachInvalid` with one field error at `/address`, message `<container> is already on network <network> at <addresses>, not <requested>`. An endpoint that reports no address at all is not judged. The refusal applies at plan and at apply, which both call `check`, and no Engine call or claim is made.
4. `connector_network_detach.go`, `networkDetachHandler.detachCheck`: in the `default:` branch of the switch on `InspectContainer`, return `foreignConnector(c, name)` when it is non-nil before using the container. The early return for a claimant with no claims stays as it is, so a detach with nothing to release still converges.
5. Do not change the undo handlers (`attachUndoConnect`, `attachUndoDisconnect`) or the plan state hash.

- [ ] **Step 4: Run the tests to verify they pass**

Run:

```bash
mkdir -p /root/.cache/w8-tmp/3 && export TMPDIR=/root/.cache/w8-tmp/3 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestAl|TestNa|TestNd|TestCp|TestCr' ./internal/modules/cloudflare/
```

Expected: `ok`. Then the task gate of Global Constraints:

```bash
mkdir -p /root/.cache/w8-tmp/3 && export TMPDIR=/root/.cache/w8-tmp/3 GOTOOLCHAIN=local
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/modules/cloudflare/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
go clean -testcache && rm -rf /root/.cache/w8-tmp/3
```

Expected: no build or vet output, every test line `ok` or `?`, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/cloudflare/connector_network.go internal/modules/cloudflare/connector_network_detach.go internal/modules/cloudflare/connector_run.go internal/modules/cloudflare/connector_network_test.go internal/modules/cloudflare/connector_network_detach_test.go
git commit -m "fix(cloudflare): check connector label and address"
```

### Task 4: panel: wire types, exposure store, agent deps and fixtures

**Files:**
- Create: `internal/modules/panel/wire.go`
- Create: `internal/modules/panel/store.go`
- Create: `internal/modules/panel/migrations/controller/0001_panel.sql`
- Create: `internal/modules/panel/deps.go`
- Test: `internal/modules/panel/fixture_test.go` (create; the shared fixtures and the tests that pin them)
- Test: `internal/modules/panel/wire_test.go`, `internal/modules/panel/store_test.go`, `internal/modules/panel/deps_test.go` (create)
- Do not edit `internal/modules/panel/panel.go`, `internal/modules/panel/panel_test.go` or any `actions.go`: `TestManifestIsASkeleton` keeps passing, and Task 11 owns the manifest.

**Interfaces:**
- Consumes: the module id `ID` (`panel.go`), `core.Env`, `core.Listeners`, `core.ListenerSpec`, `core.ListenerInfo` (`internal/core`), `store.DB`, `store.CoreMigrations`, `store.Migrate` (`internal/core/store`), `errcode.InvalidInput`, `errcode.Error`, `errcode.FieldError` (`internal/core/errcode`), the Engine client and its not-found helper `dockerapi.IsNotFound` (`internal/platform/dockerapi`). Patterns: `internal/modules/cloudflare/store.go` (the embedded controller migrations, `cfDB` in `store_test.go`), `internal/modules/npm/deps.go` (`productionNM`, `absentSocket`), `internal/modules/derp/server/server.go` (`RuleID`).
- Produces (all in Contracts): "Panel names" and "Wire types" (`wire.go`), "Exposure store" (`store.go`, `migrations/controller/0001_panel.sql`), "Panel agent dependencies" (`deps.go`, the fake `fdDocker` in `fixture_test.go`) and "Panel controller fixture" (`fixture_test.go`).
- Produces beyond Contracts, declared by this task and named in Task 6's text: `(*fdDocker).Fail(op string, err error)` queues an error as the answer of the next call of the Engine method `op` (that call changes nothing); `fdDocker` fields `Created []dockerapi.NetworkCreate` and `Removed []string` (the names of the networks removed).
- One point that completes Contracts: `ValidateExpose` returns a plain `*errcode.Error`, not marked `core.Definite`.

**Items:** 36

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 1 - coupling 2 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

Create the four test files below exactly. `fixture_test.go` holds the fixtures of Contracts "Panel agent dependencies" and "Panel controller fixture" and three tests that pin the fixtures themselves (`TestFdDockerFake`, `TestFdListenersFake`, `TestFdControllerFixtures`), because every later task's tests rest on them. Every helper is used by a test of this task, so none carries a `//nolint:unused`.

`internal/modules/panel/fixture_test.go`:

```go
package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/netip"
	"path/filepath"
	"reflect"
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
	_ panelDocker    = (*fdDocker)(nil)
	_ panelDocker    = (*dockerapi.Client)(nil)
	_ core.Listeners = (*fdListeners)(nil)
)

// fdNow is the time the controller fixture's clock reads.
var fdNow = time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)

// fdStamp is a time column as the module's migrations store it.
func fdStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// fdAPIError is the Engine's answer with the status code.
func fdAPIError(code int, format string, args ...any) error {
	return &dockerapi.APIError{StatusCode: code, Message: fmt.Sprintf(format, args...)}
}

// fdDocker is an in-memory Docker Engine for the agent actions. Created holds every NetworkCreate request, Removed the
// name of every network a RemoveNetwork call removed.
type fdDocker struct {
	mu         sync.Mutex
	networks   map[string]dockerapi.Network       // by id
	containers map[string]dockerapi.ContainerJSON // by id
	faults     map[string][]error
	next       int

	Created []dockerapi.NetworkCreate
	Removed []string
}

func fdNewDocker() *fdDocker {
	return &fdDocker{
		networks:   map[string]dockerapi.Network{},
		containers: map[string]dockerapi.ContainerJSON{},
		faults:     map[string][]error{},
	}
}

// AddNetwork stores a network; an empty ID becomes "fdnet-<name>".
func (d *fdDocker) AddNetwork(n dockerapi.Network) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if n.ID == "" {
		n.ID = "fdnet-" + n.Name
	}
	d.networks[n.ID] = fdCloneNetwork(n)
}

// AddContainer stores a container; an empty ID becomes "fdctr-<name>".
func (d *fdDocker) AddContainer(c dockerapi.ContainerJSON) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if c.ID == "" {
		c.ID = "fdctr-" + strings.TrimPrefix(c.Name, "/")
	}
	d.containers[c.ID] = c
}

// Fail queues err as the answer of the next call of the method op; that call changes nothing.
func (d *fdDocker) Fail(op string, err error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.faults[op] = append(d.faults[op], err)
}

func (d *fdDocker) enter(op string) error {
	if q := d.faults[op]; len(q) > 0 {
		d.faults[op] = q[1:]
		return q[0]
	}
	return nil
}

func fdCloneNetwork(n dockerapi.Network) dockerapi.Network {
	n.IPAM.Config = slices.Clone(n.IPAM.Config)
	n.Options = maps.Clone(n.Options)
	n.Labels = maps.Clone(n.Labels)
	n.Containers = nil
	return n
}

func (d *fdDocker) networkID(idOrName string) (string, bool) {
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

// attached returns the endpoints of the containers on the network, keyed by container id.
func (d *fdDocker) attached(n dockerapi.Network) map[string]dockerapi.NetworkContainer {
	out := map[string]dockerapi.NetworkContainer{}
	for _, c := range d.containers {
		if ep := c.NetworkSettings.Networks[n.Name]; ep != nil {
			out[c.ID] = dockerapi.NetworkContainer{Name: strings.TrimPrefix(c.Name, "/"), EndpointID: ep.EndpointID}
		}
	}
	return out
}

// fdMatch applies the label and name filters of the Engine API: every label must match, the name is a substring.
func fdMatch(f dockerapi.Filters, n dockerapi.Network) bool {
	for _, want := range f["label"] {
		k, v, hasValue := strings.Cut(want, "=")
		got, ok := n.Labels[k]
		if !ok || (hasValue && got != v) {
			return false
		}
	}
	names := f["name"]
	return len(names) == 0 || slices.ContainsFunc(names, func(s string) bool { return strings.Contains(n.Name, s) })
}

func (d *fdDocker) ListNetworks(_ context.Context, f dockerapi.Filters) ([]dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("ListNetworks"); err != nil {
		return nil, err
	}
	var out []dockerapi.Network
	for _, n := range d.networks {
		if fdMatch(f, n) {
			out = append(out, fdCloneNetwork(n))
		}
	}
	slices.SortFunc(out, func(a, b dockerapi.Network) int { return strings.Compare(a.Name, b.Name) })
	return out, nil
}

func (d *fdDocker) InspectNetwork(_ context.Context, idOrName string) (dockerapi.Network, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectNetwork"); err != nil {
		return dockerapi.Network{}, err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return dockerapi.Network{}, fdAPIError(http.StatusNotFound, "network %s not found", idOrName)
	}
	n := fdCloneNetwork(d.networks[id])
	n.Containers = d.attached(n)
	return n, nil
}

// CreateNetwork records the request and stores the network it describes. Like the Engine it gives a subnet without a
// gateway the subnet's first host address.
func (d *fdDocker) CreateNetwork(_ context.Context, req dockerapi.NetworkCreate) (string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("CreateNetwork"); err != nil {
		return "", err
	}
	if _, taken := d.networkID(req.Name); taken {
		return "", fdAPIError(http.StatusConflict, "network with name %s already exists", req.Name)
	}
	rec := req
	rec.Options, rec.Labels = maps.Clone(req.Options), maps.Clone(req.Labels)
	if req.EnableIPv6 != nil {
		v := *req.EnableIPv6
		rec.EnableIPv6 = &v
	}
	if req.IPAM != nil {
		ipam := *req.IPAM
		ipam.Config = slices.Clone(req.IPAM.Config)
		rec.IPAM = &ipam
	}
	d.Created = append(d.Created, rec)
	d.next++
	n := dockerapi.Network{
		ID: fmt.Sprintf("fdnet%02d", d.next), Name: req.Name, Created: "2026-11-04T12:00:00Z", Scope: "local", Driver: req.Driver,
		Internal: req.Internal, Attachable: req.Attachable, Options: maps.Clone(req.Options), Labels: maps.Clone(req.Labels),
	}
	if n.Driver == "" {
		n.Driver = "bridge"
	}
	if req.EnableIPv6 != nil {
		n.EnableIPv6 = *req.EnableIPv6
	}
	if req.IPAM != nil {
		n.IPAM = dockerapi.IPAM{Driver: req.IPAM.Driver, Config: slices.Clone(req.IPAM.Config)}
		for i, c := range n.IPAM.Config {
			if c.Subnet.IsValid() && !c.Gateway.IsValid() {
				n.IPAM.Config[i].Gateway = c.Subnet.Masked().Addr().Next()
			}
		}
	}
	d.networks[n.ID] = n
	return n.ID, nil
}

func (d *fdDocker) RemoveNetwork(_ context.Context, idOrName string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("RemoveNetwork"); err != nil {
		return err
	}
	id, ok := d.networkID(idOrName)
	if !ok {
		return fdAPIError(http.StatusNotFound, "network %s not found", idOrName)
	}
	if len(d.attached(d.networks[id])) > 0 {
		return fdAPIError(http.StatusForbidden, "error while removing network: network %s has active endpoints", idOrName)
	}
	d.Removed = append(d.Removed, d.networks[id].Name)
	delete(d.networks, id)
	return nil
}

func (d *fdDocker) InspectContainer(_ context.Context, idOrName string) (dockerapi.ContainerJSON, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if err := d.enter("InspectContainer"); err != nil {
		return dockerapi.ContainerJSON{}, err
	}
	if c, ok := d.containers[idOrName]; ok {
		return c, nil
	}
	for _, c := range d.containers {
		if strings.TrimPrefix(c.Name, "/") == idOrName {
			return c, nil
		}
	}
	return dockerapi.ContainerJSON{}, fdAPIError(http.StatusNotFound, "No such container: %s", idOrName)
}

// fdDB opens a controller database in t.TempDir() migrated with store.CoreMigrations(store.SideController) and
// controllerMigrations(), closed at cleanup.
func fdDB(t *testing.T) *store.DB {
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
	res, err := store.Migrate(ctx, db, store.MigrateOptions{
		Side: store.SideController, BinaryVersion: "test", BackupRoot: filepath.Join(dir, "backups"), Keep: 5, Now: fdNow,
	}, set, []store.MigrationSet{{Module: ID, FS: controllerMigrations()}})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Failed) > 0 {
		t.Fatalf("panel migrations failed: %v", res.Failed[0].Err)
	}
	return db
}

// fdListeners is an in-memory core.Listeners: Open records the spec under ids "l1", "l2", ..., Revoke moves open to
// revoked (and succeeds on revoked or closed), Close moves revoked to closed (and succeeds on closed; an open listener is
// refused), Get answers the row; FailOpen, when set, is returned by Open.
type fdListeners struct {
	Rows     map[string]core.ListenerInfo
	FailOpen error
	next     int
}

func fdNewListeners() *fdListeners { return &fdListeners{Rows: map[string]core.ListenerInfo{}} }

func (l *fdListeners) Open(_ context.Context, spec core.ListenerSpec) (string, error) {
	if l.FailOpen != nil {
		return "", l.FailOpen
	}
	l.next++
	id := fmt.Sprintf("l%d", l.next)
	l.Rows[id] = core.ListenerInfo{ID: id, Spec: spec, State: core.ListenerOpen}
	return id, nil
}

func (l *fdListeners) Revoke(_ context.Context, id string) error {
	row, ok := l.Rows[id]
	if !ok {
		return fmt.Errorf("fake: no listener %s", id)
	}
	if row.State == core.ListenerOpen {
		row.State = core.ListenerRevoked
		l.Rows[id] = row
	}
	return nil
}

func (l *fdListeners) Close(_ context.Context, id string) error {
	row, ok := l.Rows[id]
	if !ok {
		return fmt.Errorf("fake: no listener %s", id)
	}
	if row.State == core.ListenerOpen {
		return fmt.Errorf("fake: listener %s is open; revoke it first", id)
	}
	row.State = core.ListenerClosed
	l.Rows[id] = row
	return nil
}

func (l *fdListeners) Get(_ context.Context, id string) (core.ListenerInfo, error) {
	row, ok := l.Rows[id]
	if !ok {
		return core.ListenerInfo{}, fmt.Errorf("fake: no listener %s", id)
	}
	return row, nil
}

// fdEnv is a controller-side env with DB, Listeners, a fake clock at fdNow and Call set to call.
func fdEnv(t *testing.T, db *store.DB, l core.Listeners, call core.CallContext) core.Env {
	t.Helper()
	return core.Env{
		Principal: core.Principal{Actor: "user:u1", UserID: "u1", Scope: core.ScopeAdmin},
		Call:      call,
		DB:        db,
		Sealer:    coretest.Sealer{},
		Clock:     coretest.NewFakeClock(fdNow),
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
		Listeners: l,
	}
}

func fdExec(t *testing.T, db *store.DB, query string, args ...any) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, query, args...)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

// fdInsertUser adds a core_users row of role admin in the state (pending or active).
func fdInsertUser(t *testing.T, db *store.DB, name, state string) {
	t.Helper()
	fdExec(t, db, `INSERT INTO core_users (id, username, role, state, password_hash, totp_secret_ct, created_at, updated_at)
		VALUES (?, ?, 'admin', ?, 'x', x'00', ?, ?)`, "u-"+name, name, state, fdStamp(fdNow), fdStamp(fdNow))
}

// fdInsertHost adds a core_hosts row in state enrolled; local marks the controller's own host.
func fdInsertHost(t *testing.T, db *store.DB, id string, local bool) {
	t.Helper()
	fdExec(t, db, `INSERT INTO core_hosts (id, name, is_local, created_at, state) VALUES (?, ?, ?, ?, 'enrolled')`,
		id, id, local, fdStamp(fdNow))
}

// fdInsertChange adds the core_plans and core_changes rows of a controller-side change of action in state.
func fdInsertChange(t *testing.T, db *store.DB, id, action, state string) {
	t.Helper()
	at := fdStamp(fdNow)
	fdExec(t, db, `INSERT INTO core_plans (id, host_id, action, version, actor, input_hash, state_hash, diff_json, guards_json,
		confirmation, created_at, expires_at) VALUES (?, '', ?, 1, 'user:u1', '', '', '[]', '[]', '', ?, ?)`, "p-"+id, action, at, at)
	fdExec(t, db, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state, input_json,
		created_at, updated_at) VALUES (?, ?, '', ?, 1, 'compensable', '', 'user:u1', ?, '{}', ?, ?)`, id, "p-"+id, action, state, at, at)
}

// fdInsertExposure stores the exposure row.
func fdInsertExposure(t *testing.T, db *store.DB, r exposureRow) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return insertExposure(ctx, tx, r) }); err != nil {
		t.Fatal(err)
	}
}

// TestFdDockerFake: the fake Engine answers as the Engine does, because the agent actions' tests rely on it: name and
// label filters, a 409 for a taken name, the first host address as the gateway, attached containers in the inspect
// answer, a 403 for a removal with active endpoints, Engine-style not-found answers and queued faults.
func TestFdDockerFake(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	ipam := &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{Subnet: netip.MustParsePrefix("172.31.255.248/29")}}}
	v6 := false
	id, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: NetworkName, IPAM: ipam, EnableIPv6: &v6, Labels: map[string]string{core.LabelModule: ID}})
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Created) != 1 || d.Created[0].Name != NetworkName || d.Created[0].IPAM == nil || *d.Created[0].EnableIPv6 {
		t.Errorf("Created = %+v", d.Created)
	}
	if _, err := d.CreateNetwork(ctx, dockerapi.NetworkCreate{Name: NetworkName}); !dockerapi.IsConflict(err) {
		t.Errorf("creating a taken name: %v, want a 409", err)
	}
	d.AddNetwork(dockerapi.Network{Name: "other", Labels: map[string]string{"app": "x"}})
	byLabel, err := d.ListNetworks(ctx, dockerapi.Filters{"label": {core.LabelModule + "=" + ID}})
	if err != nil || len(byLabel) != 1 || byLabel[0].Name != NetworkName {
		t.Errorf("ListNetworks by label = %v, %v", byLabel, err)
	}
	byName, err := d.ListNetworks(ctx, dockerapi.Filters{"name": {"oth"}})
	if err != nil || len(byName) != 1 || byName[0].Name != "other" {
		t.Errorf("ListNetworks by name = %v, %v", byName, err)
	}
	got, err := d.InspectNetwork(ctx, NetworkName)
	if err != nil || got.ID != id || got.IPAM.Config[0].Gateway != netip.MustParseAddr("172.31.255.249") || got.EnableIPv6 {
		t.Errorf("InspectNetwork = %+v, %v", got, err)
	}
	d.AddContainer(dockerapi.ContainerJSON{Name: "/web", NetworkSettings: dockerapi.NetworkSettings{
		Networks: map[string]*dockerapi.EndpointSettings{NetworkName: {NetworkID: id, EndpointID: "e1"}}}})
	if got, _ = d.InspectNetwork(ctx, id); len(got.Containers) != 1 {
		t.Errorf("InspectNetwork lists %d containers, want 1", len(got.Containers))
	}
	if c, err := d.InspectContainer(ctx, "web"); err != nil || c.ID != "fdctr-web" {
		t.Errorf("InspectContainer(web) = %+v, %v", c, err)
	}
	var refusal *dockerapi.APIError
	if err := d.RemoveNetwork(ctx, id); !errors.As(err, &refusal) || refusal.StatusCode != http.StatusForbidden {
		t.Errorf("removing a network with an endpoint = %v, want a 403", err)
	}
	if len(d.Removed) != 0 {
		t.Errorf("Removed = %v after a refused removal", d.Removed)
	}
	delete(d.containers, "fdctr-web")
	if err := d.RemoveNetwork(ctx, id); err != nil || len(d.Removed) != 1 || d.Removed[0] != NetworkName {
		t.Errorf("RemoveNetwork = %v, Removed = %v", err, d.Removed)
	}
	if _, err := d.InspectNetwork(ctx, NetworkName); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectNetwork of a removed network = %v, want not found", err)
	}
	if err := d.RemoveNetwork(ctx, NetworkName); !dockerapi.IsNotFound(err) {
		t.Errorf("RemoveNetwork of a removed network = %v, want not found", err)
	}
	if _, err := d.InspectContainer(ctx, "ghost"); !dockerapi.IsNotFound(err) {
		t.Errorf("InspectContainer of a missing container = %v, want not found", err)
	}
	d.Fail("InspectContainer", dockerapi.ErrUnavailable)
	if _, err := d.InspectContainer(ctx, "ghost"); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("queued fault: %v", err)
	}
	if _, err := d.InspectContainer(ctx, "ghost"); !dockerapi.IsNotFound(err) {
		t.Errorf("the fault was not consumed: %v", err)
	}
}

// TestFdListenersFake: the fake runtime opens listeners as l1, l2, revokes and closes them in order, treats a repeated
// revoke or close as done, refuses to close an open listener and returns FailOpen.
func TestFdListenersFake(t *testing.T) {
	ctx := context.Background()
	l := fdNewListeners()
	spec := core.ListenerSpec{Addr: netip.MustParseAddrPort("172.31.255.249:9443"), AllowedPeer: netip.MustParseAddr("172.31.255.250"),
		ClientHeader: core.ClientHeaderNPM, PublicHost: "panel.example.org", ChangeID: "c1"}
	a, err := l.Open(ctx, spec)
	if err != nil || a != "l1" {
		t.Fatalf("Open = %q, %v", a, err)
	}
	if b, _ := l.Open(ctx, spec); b != "l2" {
		t.Errorf("second Open = %q, want l2", b)
	}
	if info, err := l.Get(ctx, "l1"); err != nil || info.State != core.ListenerOpen || info.Spec != spec {
		t.Errorf("Get(l1) = %+v, %v", info, err)
	}
	if err := l.Close(ctx, "l1"); err == nil {
		t.Error("closing an open listener succeeded")
	}
	for range 2 {
		if err := l.Revoke(ctx, "l1"); err != nil {
			t.Fatal(err)
		}
	}
	if info, _ := l.Get(ctx, "l1"); info.State != core.ListenerRevoked {
		t.Errorf("state %s after revoke, want revoked", info.State)
	}
	for range 2 {
		if err := l.Close(ctx, "l1"); err != nil {
			t.Fatal(err)
		}
	}
	if err := l.Revoke(ctx, "l1"); err != nil {
		t.Errorf("revoking a closed listener: %v", err)
	}
	if info, _ := l.Get(ctx, "l1"); info.State != core.ListenerClosed {
		t.Errorf("state %s after close and revoke, want closed", info.State)
	}
	if _, err := l.Get(ctx, "nope"); err == nil {
		t.Error("Get of an unknown listener succeeded")
	}
	l.FailOpen = errors.New("bind failed")
	if _, err := l.Open(ctx, spec); err == nil || err.Error() != "bind failed" {
		t.Errorf("Open with FailOpen = %v", err)
	}
}

// TestFdControllerFixtures: the database carries the module's table, fdEnv reaches it with the listeners and the call,
// and the insert helpers write rows the core tables accept, one local host at most.
func TestFdControllerFixtures(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	l := fdNewListeners()
	call := core.CallContext{ParentChangeID: "c1", ParentAction: ActionAdd}
	env := fdEnv(t, db, l, call)
	if env.DB != db || env.Listeners != l || !reflect.DeepEqual(env.Call, call) || !env.Clock.Now().Equal(fdNow) || env.Principal.Scope != core.ScopeAdmin || env.HostID != "" {
		t.Errorf("fdEnv = %+v", env)
	}
	fdInsertUser(t, db, "alice", "active")
	fdInsertUser(t, db, "bob", "pending")
	fdInsertHost(t, db, "h1", true)
	fdInsertHost(t, db, "h2", false)
	fdInsertChange(t, db, "c1", ActionAdd, "completed")
	fdInsertExposure(t, db, fdRow("c1"))
	count := func(query string) (n int) {
		t.Helper()
		if err := db.Read().QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for query, want := range map[string]int{
		`SELECT count(*) FROM core_users WHERE role = 'admin'`:                                       2,
		`SELECT count(*) FROM core_users WHERE state = 'pending'`:                                    1,
		`SELECT count(*) FROM core_hosts WHERE is_local = 1 AND state = 'enrolled'`:                  1,
		`SELECT count(*) FROM core_hosts WHERE is_local = 0 AND state = 'enrolled'`:                  1,
		`SELECT count(*) FROM core_changes WHERE id = 'c1' AND state = 'completed' AND host_id = ''`: 1,
		`SELECT count(*) FROM panel_exposures WHERE id = 'c1'`:                                       1,
	} {
		if got := count(query); got != want {
			t.Errorf("%s = %d, want %d", query, got, want)
		}
	}
}
```

`internal/modules/panel/wire_test.go`:

```go
package panel

import (
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
)

// TestFdNames: the constants and the range are the values the spec fixes.
func TestFdNames(t *testing.T) {
	strs := map[string][2]string{
		"NetworkName":      {NetworkName, "darkpanel-panel"},
		"BridgeName":       {BridgeName, "dp-panel"},
		"BridgeOption":     {BridgeOption, "com.docker.network.bridge.name"},
		"Claimant":         {Claimant, "panel"},
		"ConnectorPrefix":  {ConnectorPrefix, "darkpanel-cloudflared-"},
		"ViaNPM":           {ViaNPM, "npm"},
		"ViaTunnel":        {ViaTunnel, "tunnel"},
		"StateOpen":        {StateOpen, "open"},
		"StateRevoked":     {StateRevoked, "revoked"},
		"StateNeedsRepair": {StateNeedsRepair, "needs_repair"},
		"ActionAdd":        {ActionAdd, "panel.exposure.add"},
		"ActionRemove":     {ActionRemove, "panel.exposure.remove"},
		"ActionStatus":     {ActionStatus, "panel.exposure.status"},
		"ActionNetCreate":  {ActionNetCreate, "panel.network.create"},
		"ActionNetRemove":  {ActionNetRemove, "panel.network.remove"},
		"ActionConnCheck":  {ActionConnCheck, "panel.connector.check"},
		"ActionOpen":       {ActionOpen, "panel.listener.open"},
		"ActionRevoke":     {ActionRevoke, "panel.listener.revoke"},
	}
	for name, v := range strs {
		if v[0] != v[1] {
			t.Errorf("%s = %q, want %q", name, v[0], v[1])
		}
	}
	if DefaultPort != 9443 {
		t.Errorf("DefaultPort = %d, want 9443", DefaultPort)
	}
	if PanelRange != netip.MustParsePrefix("172.31.255.0/24") {
		t.Errorf("PanelRange = %s, want 172.31.255.0/24", PanelRange)
	}
}

// TestFdWireJSON: each wire type has the JSON names the contract fixes, optional fields drop out when empty, and the
// document survives a round trip.
func TestFdWireJSON(t *testing.T) {
	created := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	view := ExposureView{
		ID: "c1", Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"), Port: 9443,
		State: StateOpen, ListenerID: "l1", CreatedAt: created,
	}
	const viewJSON = `{"id":"c1","via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29",` +
		`"gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443,"state":"open","listenerId":"l1","createdAt":"2026-11-04T12:00:00Z"}`
	tests := []struct {
		name string
		v    any
		want string
	}{
		{"expose input", ExposeInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1"},
			`{"via":"npm","hostname":"panel.example.org","hostId":"h1"}`},
		{"expose input in full", ExposeInput{Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: "0123abcd-0123-4abc-8def-0123456789ab", Port: 9500},
			`{"via":"tunnel","hostname":"panel.example.org","hostId":"h1","tunnel":"0123abcd-0123-4abc-8def-0123456789ab","port":9500}`},
		{"expose output", ExposeOutput{ExposureID: "c1", Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1",
			Subnet: view.Subnet, Gateway: view.Gateway, Proxy: view.Proxy, Port: 9443},
			`{"exposureId":"c1","via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29",` +
				`"gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443}`},
		{"remove input", RemoveInput{}, `{}`},
		{"remove output", RemoveOutput{ExposureID: "c1", Hostname: "panel.example.org"}, `{"exposureId":"c1","hostname":"panel.example.org"}`},
		{"network create input", NetworkCreateInput{}, `{}`},
		{"network view", NetworkView{Network: NetworkName, ID: "n1", Subnet: view.Subnet, Gateway: view.Gateway, Proxy: view.Proxy},
			`{"network":"darkpanel-panel","id":"n1","subnet":"172.31.255.248/29","gateway":"172.31.255.249","proxy":"172.31.255.250"}`},
		{"network remove input", NetworkRemoveInput{}, `{}`},
		{"network remove output", NetworkRemoveOutput{Network: NetworkName, Removed: true}, `{"network":"darkpanel-panel","removed":true}`},
		{"connector check input", ConnectorCheckInput{Tunnel: "t1"}, `{"tunnel":"t1"}`},
		{"connector check output", ConnectorCheckOutput{Container: "darkpanel-cloudflared-t1", State: "running"},
			`{"container":"darkpanel-cloudflared-t1","state":"running"}`},
		{"listener open input", ListenerOpenInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: view.Subnet,
			Gateway: view.Gateway, Proxy: view.Proxy, Port: 9443},
			`{"via":"npm","hostname":"panel.example.org","hostId":"h1","subnet":"172.31.255.248/29","gateway":"172.31.255.249","proxy":"172.31.255.250","port":9443}`},
		{"listener open output", ListenerOpenOutput{ExposureID: "c1", ListenerID: "l1", RuleID: "R1", Address: netip.MustParseAddrPort("172.31.255.249:9443")},
			`{"exposureId":"c1","listenerId":"l1","ruleId":"R1","address":"172.31.255.249:9443"}`},
		{"listener revoke input", ListenerRevokeInput{}, `{}`},
		{"listener revoke output", ListenerRevokeOutput{ExposureID: "c1", ListenerID: "l1", RuleID: "R1", Revoked: true},
			`{"exposureId":"c1","listenerId":"l1","ruleId":"R1","revoked":true}`},
		{"status input", StatusInput{}, `{}`},
		{"status output without an exposure", StatusOutput{}, `{"exposed":false}`},
		{"status output with an exposure", StatusOutput{Exposed: true, Exposure: &view}, `{"exposed":true,"exposure":` + viewJSON + `}`},
		{"exposure view of a tunnel", ExposureView{ID: "c2", Via: ViaTunnel, Tunnel: "t1", State: StateNeedsRepair},
			`{"id":"c2","via":"tunnel","hostname":"","hostId":"","tunnel":"t1","subnet":"","gateway":"","proxy":"","port":0,` +
				`"state":"needs_repair","listenerId":"","createdAt":"0001-01-01T00:00:00Z"}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b, err := json.Marshal(tc.v)
			if err != nil {
				t.Fatal(err)
			}
			if string(b) != tc.want {
				t.Errorf("got  %s\nwant %s", b, tc.want)
			}
			back := reflect.New(reflect.TypeOf(tc.v))
			if err := json.Unmarshal(b, back.Interface()); err != nil {
				t.Fatal(err)
			}
			if again, _ := json.Marshal(back.Elem().Interface()); string(again) != tc.want {
				t.Errorf("round trip changed the document: %s", again)
			}
		})
	}
}

// TestFdWireSchemas: every input and output type reflects into a schema, and the schema of the exposure input lists the
// two routes.
func TestFdWireSchemas(t *testing.T) {
	inputs := []any{ExposeInput{}, RemoveInput{}, NetworkCreateInput{}, NetworkRemoveInput{}, ConnectorCheckInput{}, ListenerOpenInput{},
		ListenerRevokeInput{}, StatusInput{}}
	for _, v := range inputs {
		if _, err := schema.Input(reflect.TypeOf(v)); err != nil {
			t.Errorf("input %T: %v", v, err)
		}
	}
	outputs := []any{ExposeOutput{}, RemoveOutput{}, NetworkView{}, NetworkRemoveOutput{}, ConnectorCheckOutput{}, ListenerOpenOutput{},
		ListenerRevokeOutput{}, StatusOutput{}}
	for _, v := range outputs {
		if _, err := schema.Output(reflect.TypeOf(v)); err != nil {
			t.Errorf("output %T: %v", v, err)
		}
	}
	b, err := schema.Input(reflect.TypeFor[ExposeInput]())
	if err != nil || !strings.Contains(string(b), `"enum":["npm","tunnel"]`) {
		t.Errorf("ExposeInput schema %s (error %v), want the via enum npm, tunnel", b, err)
	}
	b, err = schema.Output(reflect.TypeFor[StatusOutput]())
	if err != nil || !strings.Contains(string(b), `"null"`) {
		t.Errorf("StatusOutput schema %s (error %v), want exposure nullable", b, err)
	}
}

// TestFdValidateExpose: ValidateExpose accepts the inputs the rules allow and refuses every other with
// request.invalid_input naming the JSON pointer of each bad field.
func TestFdValidateExpose(t *testing.T) {
	const tunnel = "0123abcd-0123-4abc-8def-0123456789ab"
	npm := ExposeInput{Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1"}
	tun := ExposeInput{Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: tunnel}
	long := strings.Repeat("a", 63) + "." + strings.Repeat("b", 63) + "." + strings.Repeat("c", 63) + "." + strings.Repeat("d", 61) + ".org"
	tests := []struct {
		name   string
		in     ExposeInput
		mod    func(in *ExposeInput)
		fields []string // the JSON pointers refused, sorted; nil when the input is valid
	}{
		{name: "npm", in: npm},
		{name: "tunnel", in: tun},
		{name: "port 1024", in: npm, mod: func(in *ExposeInput) { in.Port = 1024 }},
		{name: "port 65535", in: npm, mod: func(in *ExposeInput) { in.Port = 65535 }},
		{name: "253 characters", in: npm, mod: func(in *ExposeInput) { in.Hostname = long[:253] }},
		{name: "unknown via", in: npm, mod: func(in *ExposeInput) { in.Via = "ssh" }, fields: []string{"/via"}},
		{name: "empty via", in: npm, mod: func(in *ExposeInput) { in.Via = "" }, fields: []string{"/via"}},
		{name: "empty hostname", in: npm, mod: func(in *ExposeInput) { in.Hostname = "" }, fields: []string{"/hostname"}},
		{name: "upper-case hostname", in: npm, mod: func(in *ExposeInput) { in.Hostname = "Panel.example.org" }, fields: []string{"/hostname"}},
		{name: "one label", in: npm, mod: func(in *ExposeInput) { in.Hostname = "panel" }, fields: []string{"/hostname"}},
		{name: "wildcard", in: npm, mod: func(in *ExposeInput) { in.Hostname = "*.example.org" }, fields: []string{"/hostname"}},
		{name: "trailing dot", in: npm, mod: func(in *ExposeInput) { in.Hostname = "panel.example.org." }, fields: []string{"/hostname"}},
		{name: "leading hyphen", in: npm, mod: func(in *ExposeInput) { in.Hostname = "-panel.example.org" }, fields: []string{"/hostname"}},
		{name: "underscore", in: npm, mod: func(in *ExposeInput) { in.Hostname = "pa_nel.example.org" }, fields: []string{"/hostname"}},
		{name: "IP address", in: npm, mod: func(in *ExposeInput) { in.Hostname = "192.168.1.5" }, fields: []string{"/hostname"}},
		{name: "254 characters", in: npm, mod: func(in *ExposeInput) { in.Hostname = long[:253] + "a" }, fields: []string{"/hostname"}},
		{name: "empty host", in: npm, mod: func(in *ExposeInput) { in.HostID = "" }, fields: []string{"/hostId"}},
		{name: "tunnel for npm", in: npm, mod: func(in *ExposeInput) { in.Tunnel = tunnel }, fields: []string{"/tunnel"}},
		{name: "tunnel missing", in: tun, mod: func(in *ExposeInput) { in.Tunnel = "" }, fields: []string{"/tunnel"}},
		{name: "tunnel not a UUID", in: tun, mod: func(in *ExposeInput) { in.Tunnel = "my-tunnel" }, fields: []string{"/tunnel"}},
		{name: "upper-case tunnel", in: tun, mod: func(in *ExposeInput) { in.Tunnel = strings.ToUpper(tunnel) }, fields: []string{"/tunnel"}},
		{name: "port 1023", in: npm, mod: func(in *ExposeInput) { in.Port = 1023 }, fields: []string{"/port"}},
		{name: "port 65536", in: npm, mod: func(in *ExposeInput) { in.Port = 65536 }, fields: []string{"/port"}},
		{name: "negative port", in: npm, mod: func(in *ExposeInput) { in.Port = -1 }, fields: []string{"/port"}},
		{name: "several fields", in: tun, mod: func(in *ExposeInput) { in.Hostname, in.Port, in.Tunnel = "x", 80, "" },
			fields: []string{"/hostname", "/port", "/tunnel"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			in := tt.in
			if tt.mod != nil {
				tt.mod(&in)
			}
			err := ValidateExpose(in)
			if tt.fields == nil {
				if err != nil {
					t.Fatalf("ValidateExpose(%+v) = %v, want nil", in, err)
				}
				return
			}
			var coded *errcode.Error
			if !errors.As(err, &coded) || coded.Code != errcode.InvalidInput {
				t.Fatalf("ValidateExpose(%+v) = %v, want request.invalid_input", in, err)
			}
			var got []string
			for _, f := range coded.Fields {
				if f.Message == "" {
					t.Errorf("field %s has no message", f.Path)
				}
				got = append(got, f.Path)
			}
			slices.Sort(got)
			if !slices.Equal(got, tt.fields) {
				t.Errorf("fields %v, want %v", got, tt.fields)
			}
		})
	}
}
```

`internal/modules/panel/store_test.go`:

```go
package panel

import (
	"context"
	"database/sql"
	"net/netip"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// fdRow is a live npm exposure whose id is id.
func fdRow(id string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: "panel.example.org", HostID: "h1", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"), Port: DefaultPort,
		ListenerID: "l1", RuleID: RuleID(id), State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

func fdWrite(t *testing.T, db *store.DB, fn func(ctx context.Context, tx *sql.Tx) error) {
	t.Helper()
	ctx := context.Background()
	if err := db.Write(ctx, func(tx *sql.Tx) error { return fn(ctx, tx) }); err != nil {
		t.Fatal(err)
	}
}

// TestFdMigrationConstraints: the migration makes a STRICT table that refuses a second row of an id, a route that is
// neither npm nor tunnel and a state outside open, revoked and needs_repair.
func TestFdMigrationConstraints(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	var strict int
	if err := db.Read().QueryRowContext(ctx, `SELECT strict FROM pragma_table_list('panel_exposures')`).Scan(&strict); err != nil || strict != 1 {
		t.Errorf("panel_exposures strict = %d, err %v; want a STRICT table", strict, err)
	}
	const insert = `INSERT INTO panel_exposures (id, via, hostname, host_id, subnet, gateway, proxy, port, listener_id, rule_id, state, created_at, updated_at)
		VALUES (?, ?, 'h', 'h1', 's', 'g', 'p', 9443, 'l', 'r', ?, 't', 't')`
	cases := map[string][]any{
		"same id":       {"c1", "npm", "open"},
		"unknown via":   {"c2", "ssh", "open"},
		"unknown state": {"c3", "npm", "closed"},
	}
	for name, args := range cases {
		err := db.Write(ctx, func(tx *sql.Tx) error { _, err := tx.ExecContext(ctx, insert, args...); return err })
		if err == nil {
			t.Errorf("%s: the insert succeeded", name)
		}
	}
}

// TestFdExposureRoundTrip: a row is stored and read back whole, the times in UTC, and view() shows it.
func TestFdExposureRoundTrip(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	if _, ok, err := liveExposure(ctx, db.Read()); err != nil || ok {
		t.Fatalf("empty table: ok %v, err %v", ok, err)
	}
	in := fdRow("c1")
	in.Via, in.Tunnel = ViaTunnel, "0123abcd-0123-4abc-8def-0123456789ab"
	in.CreatedAt = fdNow.In(time.FixedZone("x", 7*3600))
	in.UpdatedAt = fdNow.Add(time.Minute)
	fdInsertExposure(t, db, in)
	got, ok, err := liveExposure(ctx, db.Read())
	if err != nil || !ok {
		t.Fatalf("liveExposure: ok %v, err %v", ok, err)
	}
	if !got.CreatedAt.Equal(fdNow) || !got.UpdatedAt.Equal(fdNow.Add(time.Minute)) {
		t.Errorf("times %v and %v, want %v and %v", got.CreatedAt, got.UpdatedAt, fdNow, fdNow.Add(time.Minute))
	}
	got.CreatedAt, got.UpdatedAt = in.CreatedAt, in.UpdatedAt
	if got != in {
		t.Errorf("liveExposure = %+v, want %+v", got, in)
	}
	v := got.view()
	want := ExposureView{ID: "c1", Via: ViaTunnel, Hostname: "panel.example.org", HostID: "h1", Tunnel: in.Tunnel, Subnet: in.Subnet,
		Gateway: in.Gateway, Proxy: in.Proxy, Port: DefaultPort, State: StateOpen, ListenerID: "l1", CreatedAt: in.CreatedAt}
	if v != want {
		t.Errorf("view() = %+v, want %+v", v, want)
	}
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		err := insertExposure(ctx, tx, fdRow("c1"))
		if err == nil || !strings.HasPrefix(err.Error(), "panel:") {
			t.Errorf("a second insert of c1 = %v, want a panel: error", err)
		}
		return nil
	})
}

// TestFdLiveAndGone: an exposure is gone only when it is revoked and its removal change completed; every other row is
// live, and liveExposure and GoneSQL agree.
func TestFdLiveAndGone(t *testing.T) {
	tests := []struct {
		name      string
		state     string
		removal   string // removal_change_id
		removalAs string // the state of the change removal names; "" inserts no change
		gone      bool
	}{
		{name: "open", state: StateOpen},
		{name: "needs repair", state: StateNeedsRepair},
		{name: "revoked without a removal", state: StateRevoked},
		{name: "revoked by a missing change", state: StateRevoked, removal: "r1"},
		{name: "revoked by a change still applying", state: StateRevoked, removal: "r1", removalAs: "applying"},
		{name: "revoked by a change that needs repair", state: StateRevoked, removal: "r1", removalAs: "needs_repair"},
		{name: "revoked by a failed change", state: StateRevoked, removal: "r1", removalAs: "failed"},
		{name: "revoked by a completed change", state: StateRevoked, removal: "r1", removalAs: "completed", gone: true},
		{name: "open with a completed removal change recorded", state: StateOpen, removal: "r1", removalAs: "completed"},
		{name: "needs repair with a completed removal change recorded", state: StateNeedsRepair, removal: "r1", removalAs: "completed"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			db := fdDB(t)
			if tt.removalAs != "" {
				fdInsertChange(t, db, tt.removal, ActionRemove, tt.removalAs)
			}
			r := fdRow("c1")
			r.State, r.RemovalChangeID = tt.state, tt.removal
			fdInsertExposure(t, db, r)
			got, ok, err := liveExposure(ctx, db.Read())
			if err != nil {
				t.Fatal(err)
			}
			if ok == tt.gone || (ok && got.ID != "c1") {
				t.Errorf("liveExposure = %q, %v; gone = %v", got.ID, ok, tt.gone)
			}
			var ids []string
			rows, err := db.Read().QueryContext(ctx, "SELECT e.id FROM panel_exposures e WHERE "+GoneSQL)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = rows.Close() }()
			for rows.Next() {
				var id string
				if err := rows.Scan(&id); err != nil {
					t.Fatal(err)
				}
				ids = append(ids, id)
			}
			if (len(ids) == 1) != tt.gone || len(ids) > 1 {
				t.Errorf("GoneSQL matched %v, gone = %v", ids, tt.gone)
			}
		})
	}
}

// TestFdLiveExposureSkipsGoneRows: after a removal completed and a new exposure was made, liveExposure answers the new
// one.
func TestFdLiveExposureSkipsGoneRows(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertChange(t, db, "r1", ActionRemove, "completed")
	old := fdRow("c1")
	old.State, old.RemovalChangeID = StateRevoked, "r1"
	fdInsertExposure(t, db, old)
	fresh := fdRow("c2")
	fresh.CreatedAt = fdNow.Add(time.Hour)
	fdInsertExposure(t, db, fresh)
	got, ok, err := liveExposure(ctx, db.Read())
	if err != nil || !ok || got.ID != "c2" {
		t.Fatalf("liveExposure = %q, %v, %v; want c2", got.ID, ok, err)
	}
}

// TestFdSetExposureState: the state and the update time change; a removal change id is recorded only when given; a
// missing exposure is an error.
func TestFdSetExposureState(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	later := fdNow.Add(time.Minute)
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateNeedsRepair, "", later)
	})
	got, _, err := liveExposure(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateNeedsRepair || got.RemovalChangeID != "" || !got.UpdatedAt.Equal(later) || !got.CreatedAt.Equal(fdNow) {
		t.Errorf("after needs_repair: %+v", got)
	}
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateRevoked, "r1", later.Add(time.Minute))
	})
	fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error {
		return setExposureState(ctx, tx, "c1", StateRevoked, "", later.Add(2*time.Minute))
	})
	got, _, err = liveExposure(ctx, db.Read())
	if err != nil {
		t.Fatal(err)
	}
	if got.State != StateRevoked || got.RemovalChangeID != "r1" || !got.UpdatedAt.Equal(later.Add(2*time.Minute)) {
		t.Errorf("after revoked: %+v, want removal change r1 kept", got)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error { return setExposureState(ctx, tx, "nope", StateRevoked, "", later) })
	if err == nil || !strings.Contains(err.Error(), "panel:") {
		t.Errorf("setExposureState of a missing id = %v, want a panel: error", err)
	}
	err = db.Write(ctx, func(tx *sql.Tx) error { return setExposureState(ctx, tx, "c1", "closed", "", later) })
	if err == nil {
		t.Error("setExposureState to a state outside the table's list succeeded")
	}
}

// TestFdDeleteExposure: the row goes, and deleting it again is not an error.
func TestFdDeleteExposure(t *testing.T) {
	ctx := context.Background()
	db := fdDB(t)
	fdInsertExposure(t, db, fdRow("c1"))
	for range 2 {
		fdWrite(t, db, func(ctx context.Context, tx *sql.Tx) error { return deleteExposure(ctx, tx, "c1") })
	}
	if _, ok, err := liveExposure(ctx, db.Read()); err != nil || ok {
		t.Errorf("after delete: ok %v, err %v", ok, err)
	}
}

// TestFdRuleID: the rule id is a 26-character firewall id, the same for one exposure id and different for another, and
// two ids hash to the values below (SHA-256 of "darkpanel/panel/" + id, Crockford base-32, first 25 characters).
func TestFdRuleID(t *testing.T) {
	if got, want := RuleID("01JC0000000000000000000001"), "0T6TGP8XA19D3CXP6GZDFX42FX"; got != want {
		t.Errorf("RuleID(...001) = %s, want %s", got, want)
	}
	if got, want := RuleID("01JC0000000000000000000002"), "0JBCM1G2AKVMZ62MEWM3JG206E"; got != want {
		t.Errorf("RuleID(...002) = %s, want %s", got, want)
	}
	shape := regexp.MustCompile(`^0[0-9A-HJKMNP-TV-Z]{25}$`)
	for _, id := range []string{"", "c1", "01JC0000000000000000000003"} {
		if got := RuleID(id); !shape.MatchString(got) || got != RuleID(id) {
			t.Errorf("RuleID(%q) = %q, want a stable 0 + 25 Crockford characters", id, got)
		}
	}
	if RuleID("c1") == RuleID("c2") {
		t.Error("two exposure ids share a rule id")
	}
}
```

`internal/modules/panel/deps_test.go`:

```go
package panel

import (
	"context"
	"errors"
	"testing"

	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

// TestFdProductionAgentDialsNoDaemon: in a test binary the production dependencies dial a socket that never exists, so
// a test that reaches them cannot touch the host's Docker daemon.
func TestFdProductionAgentDialsNoDaemon(t *testing.T) {
	docker, err := productionAgent().docker()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := docker.InspectNetwork(context.Background(), NetworkName); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("InspectNetwork error = %v, want dockerapi.ErrUnavailable", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w8-tmp/4 && export TMPDIR=/root/.cache/w8-tmp/4 GOTOOLCHAIN=local
timeout 600 go test -count=1 ./internal/modules/panel/
```

Expected: `FAIL ... [build failed]` with `undefined:` errors for the identifiers this task adds (`NetworkName`, `exposureRow`, `controllerMigrations`, `panelDocker`, ...). Quote the output verbatim in the report.

- [ ] **Step 3: Implement**

1. `migrations/controller/0001_panel.sql` is the table of Contracts "Exposure store" with `STRICT` appended and a header comment in the style of `cloudflare/migrations/controller/0001_cloudflare.sql` (times are fixed-width RFC 3339 UTC text; the table is read and written only through `internal/modules/panel`). The statement is:

```sql
CREATE TABLE panel_exposures (
    id                TEXT PRIMARY KEY,
    via               TEXT NOT NULL CHECK (via IN ('npm', 'tunnel')),
    hostname          TEXT NOT NULL,
    host_id           TEXT NOT NULL,
    tunnel            TEXT NOT NULL DEFAULT '',
    subnet            TEXT NOT NULL,
    gateway           TEXT NOT NULL,
    proxy             TEXT NOT NULL,
    port              INTEGER NOT NULL,
    listener_id       TEXT NOT NULL,
    rule_id           TEXT NOT NULL,
    state             TEXT NOT NULL CHECK (state IN ('open', 'revoked', 'needs_repair')),
    removal_change_id TEXT NOT NULL DEFAULT '',
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
) STRICT;
```

2. `wire.go` (package `panel`). Declare, each with a doc comment (a group comment for the constant block), exactly the constants and `PanelRange` of Contracts "Panel names" except `ID`, which `panel.go` already declares and which must not be declared again, and every wire type of Contracts "Wire types" with the field names, types and JSON names shown there. Schema tags beyond the JSON tags, and no others: `jsonschema:"enum=npm,enum=tunnel"` on `Via` of `ExposeInput` and of `ListenerOpenInput`; `jsonschema:"maxLength=253"` on `ExposeInput.Hostname`; `jsonschema:"minimum=0,maximum=65535"` on `ExposeInput.Port`; `jsonschema:"nullable"` on `StatusOutput.Exposure`; `jsonschema:"enum=open,enum=revoked,enum=needs_repair"` on `ExposureView.State`. `ValidateExpose(in ExposeInput) error` checks, and records one `errcode.FieldError{Path, Message}` per bad field, in the order `/via`, `/hostname`, `/hostId`, `/tunnel`, `/port`:
   - `/via` is `npm` or `tunnel`.
   - `/hostname` equals its own lower-case form, has 1 to 253 bytes, splits on `.` into at least two labels, every label matching `^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`, and its last label starts with a letter. These are the rules `npm.proxyhost.add` applies to a domain (without its wildcard form), so every child of the composite accepts what this accepts. An IP address, a trailing dot, an underscore and a wildcard are therefore refused.
   - `/hostId` is not empty. Whether it is the controller's host is the composite's check (`panel.wrong_host`), not this function's.
   - `/tunnel` matches `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` for `via` `tunnel`, and is empty for `via` `npm`.
   - `/port` is 0 or from 1024 to 65535.
   The error is `&errcode.Error{Code: errcode.InvalidInput, Detail: <the messages joined by "; ">, Fields: <the fields>}`, and the function returns nil when no field is bad. The helpers it uses are unexported and named `validateHostname` (returns the problem text, `""` when none), `validateLabel` and `validateTunnel` (the two compiled patterns).

3. `store.go`. Embed `migrations/controller/*.sql` and return it from `controllerMigrations() fs.FS` as `cloudflare/store.go` does. Declare `storeTimeFormat = "2006-01-02T15:04:05.000000000Z"` (every time column is written in UTC in this form and read back with it), the `storeQueryer` interface, `GoneSQL` exactly as in Contracts, `exposureRow` exactly as in Contracts, and the functions of Contracts "Exposure store". Behaviour:
   - `liveExposure` selects the columns of the table `FROM panel_exposures e WHERE NOT (` + `GoneSQL` + `) ORDER BY created_at, id LIMIT 1` and returns `(row, true, nil)`, or `(exposureRow{}, false, nil)` when no row is live. Subnet, gateway, proxy and both times are parsed back into `netip.Prefix`, `netip.Addr` and `time.Time`; a value that does not parse is an error.
   - `insertExposure` inserts every column (`Subnet`, `Gateway` and `Proxy` as their `String()` forms, the times in UTC). A second insert of an id, a `via` outside `npm` and `tunnel`, and a state outside the three states fail through the table's constraints.
   - `setExposureState(ctx, tx, id, state, removalChangeID, at)` sets `state` and `updated_at`, and sets `removal_change_id` only when the argument is not empty. A row that does not exist is an error (`panel: exposure <id> does not exist`).
   - `deleteExposure` deletes the row of the id; there being none is not an error, because the undo of the listener open may run twice.
   - `view()` copies the fields of the row into an `ExposureView` (`CreatedAt` from `CreatedAt`; the removal change id, rule id and update time are not shown).
   - `RuleID(exposureID)` is `"0"` + the first 25 characters of the Crockford base-32 encoding (alphabet `0123456789ABCDEFGHJKMNPQRSTVWXYZ`, no padding) of `sha256("darkpanel/panel/" + exposureID)`, built as `derp/server.RuleID` is built.
   Every error is wrapped with a `panel:` prefix, and no function logs.

4. `deps.go`. Declare `panelDocker` with the five methods and the parameter and result types of Contracts "Panel agent dependencies" (the parameter and result types are the ones `*dockerapi.Client` has; the fixture asserts the client implements it), `agentDeps{docker func() (panelDocker, error)}` and `productionAgent() agentDeps`. `productionAgent` builds one `dockerapi.New(socket)` client and returns it from `docker`, with `socket` = `dockerapi.DefaultSocket`, replaced by the constant `depsAbsentSocket = "/nonexistent/darkpanel/docker.sock"` when `testing.Testing()` is true, so a test binary that reaches the production dependencies can never reach the host's Docker daemon (the precedent is `npm/deps.go`).

5. Every exported identifier has a doc comment; comments elsewhere only for a non-obvious why; no `//nolint` is needed.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
timeout 600 go test -count=1 ./internal/modules/panel/
timeout 600 go test -race -count=1 ./internal/modules/panel/
```

Expected: `ok` for both, with `TestManifestIsASkeleton` still among the passing tests. Quote the output verbatim.

- [ ] **Step 5: Check and commit**

```bash
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w8-tmp/4 && go clean -testcache
git add internal/modules/panel
git commit -m "feat(panel): add exposure wire types and store"
```

Expected: `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, and `golangci-lint` prints `0 issues.`.

### Task 5: web: panel exposure page and every-page banner

**Files:**
- Create: `web/src/modules/panel/types.ts`
- Create: `web/src/modules/panel/status.ts`
- Create: `web/src/modules/panel/fixtures.ts`
- Create: `web/src/modules/panel/banner.tsx`
- Create: `web/src/modules/panel/panel-change.tsx`
- Create: `web/src/modules/panel/panel-page.tsx`
- Modify: `web/src/modules/panel/routes.tsx` (the stub that renders `null`)
- Modify: `web/src/shell.tsx` (one import and one element, next to `AdminExposureBanner`)
- Test: `web/src/modules/panel/types.test.ts`, `banner.test.tsx`, `panel-change.test.tsx`, `panel-page.test.tsx`, `routes.test.tsx` (create)
- Test: `web/src/shell.test.tsx` (modify: one import and one test)
- `web/src/modules/registry.ts` already lists the module (`{ id: "panel", ..., routes: lazy(() => import("./panel/routes")) }`) and stays unedited.

**Interfaces:**
- Consumes (Contracts "Wire types" and "Panel names", as JSON): the action names `panel.exposure.add`, `panel.exposure.remove` and `panel.exposure.status` at version 1; the `status` read's output `{"exposed": bool, "exposure": ExposureView | absent}` with `ExposureView` `{id, via, hostname, hostId, tunnel?, subnet, gateway, proxy, port, state, listenerId, createdAt}`, `via` one of `npm` and `tunnel`, `state` one of `open`, `revoked` and `needs_repair`; the `add` input `{via, hostname, hostId, tunnel?, port?}` and the `remove` input `{}`. From the web: `readAction`, `listHosts`, `HOSTS_QUERY_KEY`, `errorMessage`, `ApiError` (`@/api/client`), `ChangeDialog` (`@/components/change-dialog`), `ModuleRouteProps` (`@/modules/types`), the test helpers `renderWithProviders` and `stubFetch` (`@/test/utils`), and `NpmControllerChange` in `web/src/modules/npm/npm-change.tsx` as the pattern.
- Produces: the module's host page at `/hosts/:hostId/panel` and the banner `PanelExposureBanner` that `Shell` renders above every page (plan ruling W8-R13). Nothing a Go task consumes.

**Items:** 36

**Depends on:** none
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

Create the files below exactly. The component tests stub `fetch` (`stubFetch`); none reaches a network. `fixtures.ts` is shared by the tests and by `shell.test.tsx`.

`web/src/modules/panel/fixtures.ts`:

```ts
import type { ExposureView, StatusOutput } from "./types"

// paExposure is a live exposure of the panel through NPM; the overrides change it.
export function paExposure(over: Partial<ExposureView> = {}): ExposureView {
  return {
    id: "01JC0000000000000000000001",
    via: "npm",
    hostname: "panel.example.org",
    hostId: "h1",
    subnet: "172.31.255.248/29",
    gateway: "172.31.255.249",
    proxy: "172.31.255.250",
    port: 9443,
    state: "open",
    listenerId: "l1",
    createdAt: "2026-11-04T12:00:00Z",
    ...over,
  }
}

// paStatus is the answer of panel.exposure.status for the exposure, or for none.
export function paStatus(exposure?: ExposureView): StatusOutput {
  return exposure === undefined ? { exposed: false } : { exposed: true, exposure }
}
```

`web/src/modules/panel/types.test.ts`:

```ts
import { expect, test } from "vitest"
import { paExposure } from "./fixtures"
import { isStatusOutput } from "./types"

test("accepts the answer for no exposure and for a live one", () => {
  expect(isStatusOutput({ exposed: false })).toBe(true)
  expect(isStatusOutput({ exposed: false, exposure: null })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure() })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure({ via: "tunnel", tunnel: "0123abcd-0123-4abc-8def-0123456789ab", state: "needs_repair" }) })).toBe(true)
  expect(isStatusOutput({ exposed: true, exposure: paExposure({ state: "revoked" }) })).toBe(true)
})

test.each([
  ["not an object", "exposed"],
  ["null", null],
  ["an array", []],
  ["exposed missing", {}],
  ["exposed not a boolean", { exposed: "yes" }],
  ["exposed without an exposure", { exposed: true }],
  ["exposed with a null exposure", { exposed: true, exposure: null }],
  ["an exposure while not exposed", { exposed: false, exposure: paExposure() }],
  ["an unknown route", { exposed: true, exposure: { ...paExposure(), via: "ssh" } }],
  ["an unknown state", { exposed: true, exposure: { ...paExposure(), state: "closed" } }],
  ["a port that is text", { exposed: true, exposure: { ...paExposure(), port: "9443" } }],
  ["a missing hostname", { exposed: true, exposure: { ...paExposure(), hostname: undefined } }],
  ["a tunnel that is not text", { exposed: true, exposure: { ...paExposure(), tunnel: 5 } }],
])("rejects %s", (_, value) => {
  expect(isStatusOutput(value)).toBe(false)
})
```

`web/src/modules/panel/banner.test.tsx`:

```tsx
import { screen, waitFor } from "@testing-library/react"
import type { QueryClient } from "@tanstack/react-query"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import { renderWithProviders, stubFetch, type StubHandler } from "@/test/utils"
import { PanelExposureBanner } from "./banner"
import { paExposure, paStatus } from "./fixtures"
import { panelStatusKey } from "./types"

const PB_READ = "POST /api/v1/actions/panel.exposure.status/v1/read"
const PB_TEXT = "Panel exposed via npm at panel.example.org"

function pbRender(response: StubHandler) {
  const calls = stubFetch({ [PB_READ]: response })
  const result = renderWithProviders(<PanelExposureBanner />)
  return { calls, ...result }
}

async function pbSettled(client: QueryClient) {
  await waitFor(() => expect(["success", "error"]).toContain(client.getQueryState(panelStatusKey)?.status))
}

test("says how the panel is exposed and links to the controller host's panel page to remove it", async () => {
  const { calls } = pbRender({ body: { output: paStatus(paExposure({ hostId: "host/1" })) } })
  expect(await screen.findByText(PB_TEXT)).toBeTruthy()
  expect(screen.getByRole("link", { name: "Remove the panel exposure" }).getAttribute("href")).toBe("/hosts/host%2F1/panel")
  expect(calls.filter((c) => c.path === "/api/v1/actions/panel.exposure.status/v1/read").map((c) => c.body)).toEqual([{ input: {} }])
})

test("names the tunnel route", async () => {
  pbRender({ body: { output: paStatus(paExposure({ via: "tunnel", tunnel: "0123abcd-0123-4abc-8def-0123456789ab" })) } })
  expect(await screen.findByText("Panel exposed via tunnel at panel.example.org")).toBeTruthy()
})

test.each([
  ["revoked", "The listener is revoked, but the removal has not finished."],
  ["needs_repair", "The exposure needs repair. Remove it again to finish."],
] as const)("an exposure in state %s says what is left to do", async (state, note) => {
  pbRender({ body: { output: paStatus(paExposure({ state })) } })
  expect(await screen.findByText(PB_TEXT)).toBeTruthy()
  expect(screen.getByText(note)).toBeTruthy()
})

test("an open exposure carries no note", async () => {
  pbRender({ body: { output: paStatus(paExposure()) } })
  await screen.findByText(PB_TEXT)
  expect(screen.queryByText(/removal has not finished|needs repair/)).toBeNull()
})

test("renders nothing while the panel is not exposed", async () => {
  const { client } = pbRender({ body: { output: paStatus() } })
  await pbSettled(client)
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
  expect(screen.queryByRole("link")).toBeNull()
})

test("renders nothing when the status read fails", async () => {
  const { client } = pbRender({ status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } })
  await pbSettled(client)
  expect(client.getQueryState(panelStatusKey)?.status).toBe("error")
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
  expect(screen.queryByRole("alert")).toBeNull()
})

test("renders nothing when the controller answers with something that is not a status", async () => {
  const { client } = pbRender({ body: { output: { exposed: true } } })
  await pbSettled(client)
  expect(client.getQueryState(panelStatusKey)?.status).toBe("error")
  expect(screen.queryByText(/Panel exposed/)).toBeNull()
})

test("the banner has no accessibility violations", async () => {
  const { container } = pbRender({ body: { output: paStatus(paExposure({ state: "needs_repair" })) } })
  await screen.findByText(PB_TEXT)
  expect(await axe(container)).toHaveNoViolations()
})
```

`web/src/modules/panel/panel-change.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test, vi } from "vitest"
import type { Plan } from "@/api/types"
import { renderWithProviders, stubFetch } from "@/test/utils"
import { PanelChange } from "./panel-change"
import { panelStatusKey } from "./types"

const PC_INPUT = { via: "npm", hostname: "panel.example.org", hostId: "h1" }
const PC_DONE = { id: "c1", hostId: null, state: "applying", class: "composite", error: null, output: null, revertDeadline: null }

function pcPlan(action: string): Plan {
  return {
    planId: "p1", hostId: null, action, version: 1, inputHash: "h",
    diff: [{ op: "+", text: `${action} panel.example.org` }], guards: [], expiresAt: "2026-11-04T13:00:00Z", confirmation: null,
  }
}

function pcRender(input: Record<string, unknown> | null, action = "panel.exposure.add") {
  const calls = stubFetch({
    [`POST /api/v1/actions/${action}/v1/plan`]: { body: pcPlan(action) },
    [`POST /api/v1/actions/${action}/v1/apply`]: { body: PC_DONE },
  })
  const onClose = vi.fn()
  const result = renderWithProviders(<PanelChange hostId="h1" action={action} title="Expose the panel" input={input} onClose={onClose} />)
  return { calls, onClose, ...result }
}

test("stays closed and asks nothing while the input is null", () => {
  const { calls } = pcRender(null)
  expect(screen.queryByRole("dialog")).toBeNull()
  expect(calls).toEqual([])
})

test("plans the composite on the controller at version 1, with no host and no availability check", async () => {
  const { calls } = pcRender(PC_INPUT)
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  expect(within(dialog).getByText("panel.exposure.add/v1 on the controller")).toBeTruthy()
  expect((await within(dialog).findByLabelText("Planned changes")).textContent).toBe("+ panel.exposure.add panel.example.org")
  expect(calls.map((c) => [c.method, c.path, c.body])).toEqual([["POST", "/api/v1/actions/panel.exposure.add/v1/plan", { input: PC_INPUT }]])
})

test("applies the plan it showed", async () => {
  const { calls } = pcRender({}, "panel.exposure.remove")
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  const apply = await within(dialog).findByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await userEvent.setup().click(apply)
  await waitFor(() => expect(calls.some((c) => c.path.endsWith("/apply"))).toBe(true))
  expect(calls.find((c) => c.path.endsWith("/apply"))?.path).toBe("/api/v1/actions/panel.exposure.remove/v1/apply")
  expect(calls.find((c) => c.path.endsWith("/apply"))?.body).toEqual({ planId: "p1" })
})

test("closing reports the close and refetches the panel reads and that host's resources only", async () => {
  const { client, onClose } = pcRender(PC_INPUT)
  client.setQueryData(panelStatusKey, {})
  client.setQueryData(["resource", "h1", "npm.instance.status"], {})
  client.setQueryData(["resource", "h2", "npm.instance.status"], {})
  client.setQueryData(["npm", "instances"], {})
  client.setQueryData(["tailnet", "policy"], {})
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel" })
  await within(dialog).findByLabelText("Planned changes")
  await userEvent.setup().keyboard("{Escape}")
  await waitFor(() => expect(onClose).toHaveBeenCalledTimes(1))
  expect(client.getQueryState(panelStatusKey)?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h1", "npm.instance.status"])?.isInvalidated).toBe(true)
  expect(client.getQueryState(["resource", "h2", "npm.instance.status"])?.isInvalidated).toBe(false)
  expect(client.getQueryState(["npm", "instances"])?.isInvalidated).toBe(false)
  expect(client.getQueryState(["tailnet", "policy"])?.isInvalidated).toBe(false)
})
```

`web/src/modules/panel/panel-page.test.tsx`:

```tsx
import { screen, waitFor, within } from "@testing-library/react"
import userEvent from "@testing-library/user-event"
import { expect, test } from "vitest"
import { axe } from "vitest-axe"
import type { Host } from "@/api/types"
import { renderWithProviders, stubFetch, type FetchCall, type StubHandler } from "@/test/utils"
import { paExposure, paStatus } from "./fixtures"
import { PanelPage } from "./panel-page"
import type { StatusOutput } from "./types"

const PP_STATUS = "POST /api/v1/actions/panel.exposure.status/v1/read"
const PP_HOSTS = "POST /api/v1/actions/core.host.list/v1/read"
const PP_ADD_PLAN = "POST /api/v1/actions/panel.exposure.add/v1/plan"
const PP_REMOVE_PLAN = "POST /api/v1/actions/panel.exposure.remove/v1/plan"
const PP_REMOVE_APPLY = "POST /api/v1/actions/panel.exposure.remove/v1/apply"
const PP_TUNNEL = "0123abcd-0123-4abc-8def-0123456789ab"

function ppHost(id: string, isLocal: boolean): Host {
  return { id, name: id === "h1" ? "controller" : "web-2", isLocal, online: true, modules: [], ingestion: null }
}

function ppPlan(call: FetchCall) {
  const parts = call.path.split("/")
  return {
    body: {
      planId: "p1", hostId: null, action: parts[parts.length - 3] ?? "", version: 1, inputHash: "h",
      diff: [{ op: "+", text: "change" }], guards: [], expiresAt: "2026-11-04T13:00:00Z", confirmation: null,
    },
  }
}

function ppRender(hostId: string, status: StatusOutput, routes: Record<string, StubHandler> = {}) {
  const calls = stubFetch({
    [PP_STATUS]: { body: { output: status } },
    [PP_HOSTS]: { body: { output: { hosts: [ppHost("h1", true), ppHost("h2", false)] } } },
    [PP_ADD_PLAN]: ppPlan,
    [PP_REMOVE_PLAN]: ppPlan,
    [PP_REMOVE_APPLY]: { body: { id: "c1", hostId: null, state: "applying", class: "composite", error: null, output: null, revertDeadline: null } },
    ...routes,
  })
  const result = renderWithProviders(<PanelPage hostId={hostId} />)
  return { calls, ...result }
}

function ppPlanBodies(calls: FetchCall[], suffix: string): unknown[] {
  return calls.filter((c) => c.method === "POST" && c.path.endsWith(suffix)).map((c) => c.body)
}

function ppReads(calls: FetchCall[]): number {
  return calls.filter((c) => c.path === "/api/v1/actions/panel.exposure.status/v1/read").length
}

async function ppForm() {
  return await screen.findByRole("form", { name: "Expose the panel" })
}

test("the controller's host with no exposure offers the form, and the route defaults to NPM", async () => {
  ppRender("h1", paStatus())
  const form = await ppForm()
  expect(within(form).getByRole("radio", { name: "Nginx Proxy Manager", checked: true })).toBeTruthy()
  expect(within(form).getByRole("radio", { name: "Cloudflare tunnel", checked: false })).toBeTruthy()
  expect(within(form).getByLabelText("Public hostname")).toBeTruthy()
  expect(within(form).queryByLabelText("Tunnel id")).toBeNull()
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
})

test("an NPM exposure plans panel.exposure.add with the trimmed lower-case hostname and the host, and nothing else", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "  Panel.Example.org ")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  const dialog = await screen.findByRole("dialog", { name: "Expose the panel at panel.example.org" })
  expect(await within(dialog).findByLabelText("Planned changes")).toBeTruthy()
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toEqual([{ input: { via: "npm", hostname: "panel.example.org", hostId: "h1" } }])
})

test("a tunnel exposure asks for the tunnel and sends it with the port when one is typed", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.click(within(form).getByRole("radio", { name: "Cloudflare tunnel" }))
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
  await user.type(within(form).getByLabelText("Tunnel id"), PP_TUNNEL.toUpperCase())
  await user.type(within(form).getByLabelText("Port (optional)"), "9500")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog", { name: "Expose the panel at panel.example.org" })
  await waitFor(() => expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toHaveLength(1))
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")[0]).toEqual({
    input: { via: "tunnel", hostname: "panel.example.org", hostId: "h1", tunnel: PP_TUNNEL, port: 9500 },
  })
})

test("switching back to NPM drops the tunnel from the request", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.click(within(form).getByRole("radio", { name: "Cloudflare tunnel" }))
  await user.type(within(form).getByLabelText("Tunnel id"), PP_TUNNEL)
  await user.click(within(form).getByRole("radio", { name: "Nginx Proxy Manager" }))
  expect(within(form).queryByLabelText("Tunnel id")).toBeNull()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.click(within(form).getByRole("button", { name: "Review" }))
  await screen.findByRole("dialog")
  await waitFor(() => expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")).toHaveLength(1))
  expect(ppPlanBodies(calls, "/panel.exposure.add/v1/plan")[0]).toEqual({ input: { via: "npm", hostname: "panel.example.org", hostId: "h1" } })
})

test.each(["80", "1023", "65536", "99999", "9443x", "-1", "1e3"])("the port %s is refused before anything is planned", async (port) => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.type(within(form).getByLabelText("Port (optional)"), port)
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(true)
  expect(within(form).getByLabelText("Port (optional)").getAttribute("aria-invalid")).toBe("true")
  expect(within(form).getByText("Enter a whole number from 1024 to 65535.")).toBeTruthy()
  expect(ppPlanBodies(calls, "/plan")).toEqual([])
})

test.each(["1024", "65535"])("the port %s is accepted", async (port) => {
  const user = userEvent.setup()
  ppRender("h1", paStatus())
  const form = await ppForm()
  await user.type(within(form).getByLabelText("Public hostname"), "panel.example.org")
  await user.type(within(form).getByLabelText("Port (optional)"), port)
  expect(within(form).getByRole("button", { name: "Review" }).hasAttribute("disabled")).toBe(false)
})

test("a host that is not the controller's gets no form and no plan", async () => {
  const { calls } = ppRender("h2", paStatus())
  expect(await screen.findByText("The panel can be exposed only from the controller's own host. Open the panel page of that host.")).toBeTruthy()
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
  expect(ppPlanBodies(calls, "/plan")).toEqual([])
})

test("an exposure shows its route, hostname, network and state in place of the form", async () => {
  ppRender("h1", paStatus(paExposure()))
  const list = await screen.findByLabelText("Panel exposure")
  const value = (label: string) => within(list).getByText(label).nextElementSibling?.textContent ?? null
  expect(value("Route")).toBe("Nginx Proxy Manager")
  expect(value("Hostname")).toBe("panel.example.org")
  expect(value("Network")).toBe("172.31.255.248/29")
  expect(value("Gateway")).toBe("172.31.255.249")
  expect(value("Proxy")).toBe("172.31.255.250")
  expect(value("Port")).toBe("9443")
  expect(value("State")).toBe("open")
  expect(within(list).queryByText("Tunnel")).toBeNull()
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
})

test("a tunnel exposure shows its tunnel, and an exposure that needs repair can still be removed", async () => {
  ppRender("h1", paStatus(paExposure({ via: "tunnel", tunnel: PP_TUNNEL, state: "needs_repair" })))
  const list = await screen.findByLabelText("Panel exposure")
  expect(within(list).getByText("Cloudflare tunnel")).toBeTruthy()
  expect(within(list).getByText("Tunnel").nextElementSibling?.textContent).toBe(PP_TUNNEL)
  expect(within(list).getByText("State").nextElementSibling?.textContent).toBe("needs_repair")
  expect(screen.getByRole("button", { name: "Remove exposure" }).hasAttribute("disabled")).toBe(false)
})

test("an exposure is removed from any host's page, planning panel.exposure.remove with an empty input on the controller", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h2", paStatus(paExposure()))
  await user.click(await screen.findByRole("button", { name: "Remove exposure" }))
  const dialog = await screen.findByRole("dialog", { name: "Remove the panel exposure at panel.example.org" })
  expect(within(dialog).getByText("panel.exposure.remove/v1 on the controller")).toBeTruthy()
  expect(await within(dialog).findByLabelText("Planned changes")).toBeTruthy()
  expect(ppPlanBodies(calls, "/panel.exposure.remove/v1/plan")).toEqual([{ input: {} }])
  const apply = within(dialog).getByRole("button", { name: "Apply" })
  await waitFor(() => expect(apply.hasAttribute("disabled")).toBe(false))
  await user.click(apply)
  await waitFor(() => expect(calls.some((c) => c.path === "/api/v1/actions/panel.exposure.remove/v1/apply")).toBe(true))
  expect(calls.find((c) => c.path === "/api/v1/actions/panel.exposure.remove/v1/apply")?.body).toEqual({ planId: "p1" })
})

test("closing the removal dialog reads the exposure again", async () => {
  const user = userEvent.setup()
  const { calls } = ppRender("h1", paStatus(paExposure()))
  await user.click(await screen.findByRole("button", { name: "Remove exposure" }))
  const dialog = await screen.findByRole("dialog")
  await within(dialog).findByLabelText("Planned changes")
  expect(ppReads(calls)).toBe(1)
  await user.keyboard("{Escape}")
  await waitFor(() => expect(ppReads(calls)).toBe(2))
})

test("a failed status read is said and offers neither the form nor the removal", async () => {
  ppRender("h1", paStatus(), {
    [PP_STATUS]: { status: 503, body: { type: "about:blank", title: "Unavailable", status: 503, detail: "the database is busy", code: "unavailable" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("the database is busy")
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
  expect(screen.queryByRole("button", { name: "Remove exposure" })).toBeNull()
})

test("an answer that is not a status is said", async () => {
  ppRender("h1", paStatus(), { [PP_STATUS]: { body: { output: { exposed: true } } } })
  expect((await screen.findByRole("alert")).textContent).toBe("The controller answered with an unexpected panel exposure status.")
})

test("a failed host list is said when the form would need it", async () => {
  ppRender("h1", paStatus(), {
    [PP_HOSTS]: { status: 500, body: { type: "about:blank", title: "Internal", status: 500, detail: "Internal error", code: "internal" } },
  })
  expect((await screen.findByRole("alert")).textContent).toBe("Internal error")
  expect(screen.queryByRole("form", { name: "Expose the panel" })).toBeNull()
})

test("the form and the exposure have no accessibility violations", async () => {
  const form = ppRender("h1", paStatus())
  await ppForm()
  expect(await axe(form.container)).toHaveNoViolations()
  form.unmount()
  const exposed = ppRender("h1", paStatus(paExposure()))
  await screen.findByRole("button", { name: "Remove exposure" })
  expect(await axe(exposed.container)).toHaveNoViolations()
})
```

`web/src/modules/panel/routes.test.tsx`:

```tsx
import { screen } from "@testing-library/react"
import { Route, Routes } from "react-router"
import { expect, test } from "vitest"
import { renderWithProviders, stubFetch } from "@/test/utils"
import PanelRoutes from "./routes"

function prRender(path: string) {
  const calls = stubFetch({
    "POST /api/v1/actions/panel.exposure.status/v1/read": { body: { output: { exposed: false } } },
    "POST /api/v1/actions/core.host.list/v1/read": {
      body: { output: { hosts: [{ id: "h1", name: "controller", isLocal: true, online: true, modules: [], ingestion: null }] } },
    },
  })
  renderWithProviders(
    <Routes>
      <Route path="hosts/:hostId/panel/*" element={<PanelRoutes hostId="h1" />} />
    </Routes>,
    { path },
  )
  return calls
}

test("the index route is the panel page of the host", async () => {
  prRender("/hosts/h1/panel")
  expect(await screen.findByRole("form", { name: "Expose the panel" })).toBeTruthy()
})

test("any other path says the module has no such page and reads nothing", () => {
  const calls = prRender("/hosts/h1/panel/nowhere")
  expect(screen.getByText("Page not found")).toBeTruthy()
  expect(screen.getByText("The panel module has no page at this address.")).toBeTruthy()
  expect(calls).toEqual([])
})
```

In `web/src/shell.test.tsx` add the import `import { paExposure, paStatus } from "@/modules/panel/fixtures"` below the existing `npInstance` import, and append this test at the end of the file:

```tsx
test("every page shows the panel-exposure banner above its content, linking to the controller host's panel page", async () => {
  renderShell({ body: bound }, "/audit", { status: 204 }, [], undefined, {
    "POST /api/v1/actions/panel.exposure.status/v1/read": { body: { output: paStatus(paExposure({ hostId: "h1" })) } },
  })
  const banner = await screen.findByText("Panel exposed via npm at panel.example.org")
  const content = await screen.findByText("Audit content")
  expect(banner.compareDocumentPosition(content) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  expect(screen.getByRole("link", { name: "Remove the panel exposure" }).getAttribute("href")).toBe("/hosts/h1/panel")
})
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w8-tmp/5 && export TMPDIR=/root/.cache/w8-tmp/5 GOTOOLCHAIN=local
timeout 600 make web-deps
timeout 600 pnpm -C web exec vitest run src/modules/panel src/shell.test.tsx
```

Expected: the five new test files fail to load (`Failed to resolve import "./banner"`, `"./panel-page"`, `"./panel-change"`, `"./types"`) and `shell.test.tsx` fails (`Failed to resolve import "@/modules/panel/fixtures"` until `fixtures.ts` exists, then the banner test fails with the text not found). Quote the output verbatim.

- [ ] **Step 3: Implement**

1. `types.ts`: `PANEL_MODULE = "panel"`, `PANEL_ACTION_VERSION = 1`, `panelStatusKey = [PANEL_MODULE, "exposure"]` (the one query key the banner and the page share), the types `ExposureVia` (`"npm" | "tunnel"`), `ExposureState` (`"open" | "revoked" | "needs_repair"`), `ExposureView` and `StatusOutput` (`exposure` optional and nullable), and `isStatusOutput(v: unknown): v is StatusOutput` in the style of `isInstanceStatus` in `npm/types.ts`: a record whose `exposed` is a boolean; when `exposed` is true, `exposure` must be a valid `ExposureView` (every field of the type present with its type, `tunnel` optional); when it is false, `exposure` must be absent or null.
2. `status.ts`: `readExposureStatus(): Promise<StatusOutput>` reads `panel.exposure.status` at version 1 on the controller with input `{}` through `readAction`, rejects with `Error("The controller answered with an unexpected panel exposure status.")` when `isStatusOutput` refuses the body, and `messageOf(error: unknown): string` returns `errorMessage(error)` for an `ApiError`, the message of any other `Error` and `String(error)` otherwise (the same helper `npm/status.ts` has).
3. `banner.tsx`: `PanelExposureBanner()` reads the status with `useQuery` under `panelStatusKey`, `queryFn: readExposureStatus`, `staleTime: 60_000`. It renders nothing while the read is pending or failed, when `exposed` is false, or when the exposure is missing. Otherwise it renders a `variant="warning"` `Alert` with `role="status"` and `className="mb-4"` holding an `AlertTitle` "Panel exposed", an `AlertDescription` with exactly `Panel exposed via <via> at <hostname>` (no trailing period), for state `revoked` a second `AlertDescription` "The listener is revoked, but the removal has not finished.", for state `needs_repair` a second `AlertDescription` "The exposure needs repair. Remove it again to finish." (none for `open`), and a `Link` with the text "Remove the panel exposure" to `/hosts/<encodeURIComponent(exposure.hostId)>/panel`, styled like the link in `npm/banner.tsx`.
4. `panel-change.tsx`: `PanelChange({ hostId, action, title, input, onClose })` wraps `ChangeDialog` like `NpmControllerChange`: `open` is `input !== null`; the target is `{ name: action, version: PANEL_ACTION_VERSION }` with no `hostId` and no `module`, so the dialog plans on the controller and makes no availability request; when the dialog closes (`onOpenChange(false)`) it calls `onClose()` and invalidates the queries under `[PANEL_MODULE]` and under `["resource", hostId]` and nothing else.
5. `panel-page.tsx`: `PanelPage({ hostId })` reads the status (`panelStatusKey`, `readExposureStatus`, no `staleTime`) and the host list (`HOSTS_QUERY_KEY`, `listHosts`), holds the pending change (`null` or `{action, title, input}`), always renders a `PanelChange` for it (`hostId` of the page, `onClose` clears the pending change), and shows exactly one of these bodies, in this order:
   - the status is pending: `Spinner` labelled "Loading the panel exposure";
   - the status failed: a destructive `Alert` (`role="alert"`) with `messageOf(error)`;
   - the status says the panel is exposed: a `<dl aria-label="Panel exposure">` with the rows Route ("Nginx Proxy Manager" for `npm`, "Cloudflare tunnel" for `tunnel`), Hostname, Tunnel (only for a tunnel exposure), Network (the subnet), Gateway, Proxy, Port and State (a `Badge`, `default` variant for `open`, `destructive` for the other states), each label a `<dt>` followed by its value in the next `<dd>`; and a destructive button "Remove exposure" that sets the pending change to action `panel.exposure.remove`, title `Remove the panel exposure at <hostname>`, input `{}`. The button is enabled in every state, because a `needs_repair` exposure is removable (W8-R11), and it is shown on every host's page, because the removal does not depend on the host;
   - the status says the panel is not exposed and the host list is pending: `Spinner` labelled "Loading the host";
   - ... and the host list failed: a destructive `Alert` with `messageOf(error)`;
   - ... and the host of the page has `isLocal` true: the form;
   - ... otherwise: the paragraph "The panel can be exposed only from the controller's own host. Open the panel page of that host." and no form.
   The form is `<form aria-label="Expose the panel">` with: a `RadioGroup` "Route" of two items "Nginx Proxy Manager" (`npm`, the default) and "Cloudflare tunnel" (`tunnel`); an `Input` labelled "Public hostname"; for the tunnel route only an `Input` labelled "Tunnel id"; an `Input` labelled "Port (optional)" with `inputMode="numeric"`, `aria-invalid` when its text is not empty and is not a whole number from 1024 to 65535, and a help text under it that reads "The port the controller serves the exposure on. Leave empty for 9443." and, while invalid, "Enter a whole number from 1024 to 65535." in the destructive colour; and a submit button "Review", disabled until the hostname is not blank, the tunnel route has a non-blank tunnel and the port is valid. Submitting sets the pending change to action `panel.exposure.add`, title `Expose the panel at <hostname>` and input `{ via, hostname, hostId, tunnel?, port? }` where `hostname` is trimmed and lower-cased, `hostId` is the page's host, `tunnel` is present only for the tunnel route (trimmed, lower-cased) and `port` only when the field is not empty (as a number). Use `useId` for the element ids, as `npm/instance-panel.tsx` does.
6. `routes.tsx`: the default export `PanelRoutes({ hostId }: ModuleRouteProps)` renders `Routes` with an index route to `<PanelPage key={hostId} hostId={hostId} />` and a `*` route to `EmptyState` with title "Page not found" and description "The panel module has no page at this address.", as `npm/routes.tsx` does.
7. `shell.tsx`: import `PanelExposureBanner` from `@/modules/panel/banner` and render `<PanelExposureBanner />` on the line after `<AdminExposureBanner />`, inside the same layout, so both banners sit above `<Outlet />`.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
timeout 600 pnpm -C web exec vitest run src/modules/panel src/shell.test.tsx
```

Expected: every test file passes. Quote the output verbatim.

- [ ] **Step 5: Check and commit**

```bash
timeout 1200 make lint-web test-web
rm -rf /root/.cache/w8-tmp/5
git add web/src/modules/panel web/src/shell.tsx web/src/shell.test.tsx
git commit -m "feat(web): add the panel exposure page and banner"
```

Expected: `eslint --max-warnings 0` and `tsc --noEmit` print nothing and the whole web suite passes. This task edits no Go file, so the Go-only checks of Global Constraints do not apply; say so in the report.

### Task 6: panel: network create and remove, connector check

**Files:**
- Create: `internal/modules/panel/network.go` (`panel.network.create`, and the helpers all three actions share)
- Create: `internal/modules/panel/network_remove.go` (`panel.network.remove`)
- Create: `internal/modules/panel/connector_check.go` (`panel.connector.check`)
- Test: `internal/modules/panel/network_test.go`, `internal/modules/panel/network_remove_test.go`, `internal/modules/panel/connector_check_test.go` (create)
- Do not edit `panel.go` or `actions.go`: Task 11 lists the constructors in the manifest.

**Interfaces:**
- Consumes (Task 4, Contracts "Panel names", "Wire types", "Panel agent dependencies"): `NetworkName`, `BridgeName`, `BridgeOption`, `ConnectorPrefix`, `PanelRange`, `ActionNetCreate`, `ActionNetRemove`, `ActionConnCheck`, `ID`, the wire types `NetworkCreateInput`, `NetworkView`, `NetworkRemoveInput`, `NetworkRemoveOutput`, `ConnectorCheckInput`, `ConnectorCheckOutput`, `agentDeps`, `panelDocker`; in the tests the fake `fdDocker` (`fdNewDocker`, `AddNetwork`, `AddContainer`, `Fail`, `Created`, `Removed`; `Fail` and the two fields are declared by Task 4 beyond Contracts) and `fdNow`. From the repository: `core.DefineMutating`, `core.DefineInternalRead`, `core.Effect`, `core.EffectJournal`, `core.Observation`, `core.Definite`, `errcode` (`internal/core`), `dstate.NetworkCreated` (`internal/modules/docker/dstate`), `dockerapi` and `dockerapi.IsNotFound`, `dockerapi.ErrUnavailable`. Patterns: `npm/network_claims.go`, `npm/effects.go` (`run`, `observeNetwork`), `npm/instance_deploy.go` (`createEngineObjects`, `Inverse`), `npm/controller_checks.go` (`DefineInternalRead`).
- Produces (Contracts "Action constructors"): `networkCreateAction(d agentDeps) core.ActionDef`, `networkRemoveAction(d agentDeps) core.ActionDef`, `connectorCheckAction(d agentDeps) core.ActionDef`. Task 11 lists them in the manifest. Unexported identifiers of this task start with `network` or `connector`.

**Items:** 328, 36

**Depends on:** Task 4
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing tests**

Create the three test files exactly. They drive each action's handler (`def.Handler.Plan`, `Apply`, `Verify`, `Observe`, `Inverse`, `Reveal`) through the fake Engine of Task 4 and an in-memory effect journal declared here (`nwJournal`), the way the npm tests drive theirs; none reaches Docker.

`internal/modules/panel/network_test.go`:

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/netip"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/coretest"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const nwChange = "01JC0000000000000000000009"

// nwJournal is an in-memory core.EffectJournal. onIntend, when set, runs as each effect is recorded.
type nwJournal struct {
	list     []core.Effect
	onIntend func()
}

func (j *nwJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	if j.onIntend != nil {
		j.onIntend()
	}
	e.Seq, e.State, e.ChangeID = len(j.list)+1, core.EffectIntended, nwChange
	j.list = append(j.list, e)
	return e.Seq, nil
}

func (j *nwJournal) intended(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *nwJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *nwJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.intended(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *nwJournal) List(context.Context) ([]core.Effect, error) { return slices.Clone(j.list), nil }

func (j *nwJournal) Secret(context.Context, int) ([]byte, error) {
	return nil, errors.New("fake: no secret")
}

var _ core.EffectJournal = (*nwJournal)(nil)

// nwEnv is the agent-side environment of a call; the journal is set for a change and nil for a plan or a read.
func nwEnv(j *nwJournal) core.Env {
	env := core.Env{
		HostID:    "h1",
		Principal: core.Principal{Actor: "user:u1", Scope: core.ScopeAdmin},
		Clock:     coretest.NewFakeClock(fdNow),
	}
	if j != nil {
		env.ChangeID, env.Effects = nwChange, j
	}
	return env
}

// nwDeps points the agent actions at the fake Engine.
func nwDeps(d *fdDocker) agentDeps {
	return agentDeps{docker: func() (panelDocker, error) { return d, nil }}
}

func nwJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// nwNet is a bridge network with one IPv4 or IPv6 subnet per argument.
func nwNet(name string, labels map[string]string, subnets ...string) dockerapi.Network {
	n := dockerapi.Network{Name: name, Created: "2026-10-01T00:00:00Z", Scope: "local", Driver: "bridge", Labels: labels}
	for _, s := range subnets {
		n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix(s)})
	}
	return n
}

// nwPanelNet is the darkpanel-panel network as panel.network.create makes it.
func nwPanelNet(subnet string) dockerapi.Network {
	p := netip.MustParsePrefix(subnet)
	n := nwNet(NetworkName, map[string]string{core.LabelModule: ID})
	n.IPAM.Config = []dockerapi.IPAMConfig{{Subnet: p, Gateway: p.Addr().Next()}}
	n.Options = map[string]string{BridgeOption: BridgeName}
	return n
}

// nwAttach puts a container on the network.
func nwAttach(d *fdDocker, network, container string) {
	d.AddContainer(dockerapi.ContainerJSON{Name: "/" + container, NetworkSettings: dockerapi.NetworkSettings{
		Networks: map[string]*dockerapi.EndpointSettings{network: {EndpointID: "e-" + container}}}})
}

// nwRefusal asserts err is a definite refusal with the code and returns it.
func nwRefusal(t *testing.T, err error, code errcode.Code) *errcode.Error {
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

// nwStates returns the states of the journaled effects, in order.
func nwStates(j *nwJournal) []core.EffectState {
	var out []core.EffectState
	for _, e := range j.list {
		out = append(out, e.State)
	}
	return out
}

func nwApplyCreate(t *testing.T, d *fdDocker, j *nwJournal) (NetworkView, error) {
	t.Helper()
	raw, err := networkCreateAction(nwDeps(d)).Handler.Apply(context.Background(), nwEnv(j), nwJSON(t, NetworkCreateInput{}))
	if err != nil {
		return NetworkView{}, err
	}
	var out NetworkView
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestNwCreatePlanPicksSubnet: the plan takes the highest /29 of 172.31.255.0/24 that overlaps no IPv4 subnet of any
// network, gives the gateway the first host address and the proxy the second, and refuses when none is free.
func TestNwCreatePlanPicksSubnet(t *testing.T) {
	// takeFrom8 occupies every /29 above the lowest two: .8 up to .255.
	takeFrom8 := func(d *fdDocker) {
		for i, s := range []string{"172.31.255.128/25", "172.31.255.64/26", "172.31.255.32/27", "172.31.255.16/28", "172.31.255.8/29"} {
			d.AddNetwork(nwNet(fmt.Sprintf("taken%d", i), nil, s))
		}
	}
	tests := []struct {
		name   string
		setup  func(d *fdDocker)
		subnet string // "" when none is free
	}{
		{name: "empty host", subnet: "172.31.255.248/29"},
		{name: "top block taken", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.248/29")) }, subnet: "172.31.255.240/29"},
		{name: "a /28 over the two top blocks", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.240/28")) }, subnet: "172.31.255.232/29"},
		{name: "a /25 over the upper half", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.128/25")) }, subnet: "172.31.255.120/29"},
		{name: "a /30 inside the top block", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.31.255.252/30")) }, subnet: "172.31.255.240/29"},
		{name: "networks outside the range", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet("a", nil, "172.31.254.0/24", "10.0.0.0/8"))
		}, subnet: "172.31.255.248/29"},
		{name: "an IPv6 subnet", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "fd00::/64")) }, subnet: "172.31.255.248/29"},
		{name: "a network without a subnet", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil)) }, subnet: "172.31.255.248/29"},
		{name: "a second subnet of one network", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet("a", nil, "10.1.0.0/16", "172.31.255.248/29"))
		}, subnet: "172.31.255.240/29"},
		{name: "one network covering the range", setup: func(d *fdDocker) { d.AddNetwork(nwNet("a", nil, "172.16.0.0/12")) }},
		{name: "only the lowest block left", setup: takeFrom8, subnet: "172.31.255.0/29"},
		{name: "every block taken", setup: func(d *fdDocker) {
			takeFrom8(d)
			d.AddNetwork(nwNet("last", nil, "172.31.255.0/29"))
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			if tt.setup != nil {
				tt.setup(d)
			}
			out, err := networkCreateAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
			if tt.subnet == "" {
				var coded *errcode.Error
				if err == nil || !strings.Contains(err.Error(), "no free /29") || errors.As(err, &coded) || !core.IsDefinite(err) {
					t.Fatalf("error %v, want a definite plain error naming that no /29 is free", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			p := netip.MustParsePrefix(tt.subnet)
			want := []core.DiffLine{
				{Op: "+", Text: "network darkpanel-panel " + tt.subnet},
				{Op: "+", Text: "gateway " + p.Addr().Next().String() + ", proxy " + p.Addr().Next().Next().String()},
			}
			if !slices.Equal(out.Diff, want) {
				t.Errorf("diff %v, want %v", out.Diff, want)
			}
			if len(d.Created) != 0 {
				t.Error("a plan created a network")
			}
		})
	}
}

// TestNwCreatePlanDiffOnEmptyHost pins the whole diff of the first case as literal text.
func TestNwCreatePlanDiffOnEmptyHost(t *testing.T) {
	out, err := networkCreateAction(nwDeps(fdNewDocker())).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
	if err != nil {
		t.Fatal(err)
	}
	want := []core.DiffLine{
		{Op: "+", Text: "network darkpanel-panel 172.31.255.248/29"},
		{Op: "+", Text: "gateway 172.31.255.249, proxy 172.31.255.250"},
	}
	if !slices.Equal(out.Diff, want) {
		t.Errorf("diff %v, want %v", out.Diff, want)
	}
}

// TestNwCreatePlanStateHash: the hash is stable, and it changes when another network moves the chosen subnet, which
// makes apply refuse a plan whose subnet was taken since.
func TestNwCreatePlanStateHash(t *testing.T) {
	d := fdNewDocker()
	def := networkCreateAction(nwDeps(d))
	plan := func() string {
		t.Helper()
		out, err := def.Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
		if err != nil || out.StateHash == "" {
			t.Fatalf("plan: hash %q, error %v", out.StateHash, err)
		}
		return out.StateHash
	}
	first := plan()
	if again := plan(); again != first {
		t.Errorf("a second plan hashes %s, want %s", again, first)
	}
	d.AddNetwork(nwNet("elsewhere", nil, "10.9.0.0/24"))
	if unrelated := plan(); unrelated != first {
		t.Error("the hash changed when a network outside the range appeared")
	}
	d.AddNetwork(nwNet("a", nil, "172.31.255.248/29"))
	if moved := plan(); moved == first {
		t.Error("the hash did not change when the chosen subnet was taken")
	}
}

// TestNwCreatePlanRefusals: an existing darkpanel-panel network, whoever made it, an unreachable Engine and a client that
// cannot be built are refused before anything is created.
func TestNwCreatePlanRefusals(t *testing.T) {
	tests := []struct {
		name  string
		setup func(d *fdDocker)
		code  errcode.Code // "" for a plain definite error
	}{
		{name: "network exists", setup: func(d *fdDocker) { d.AddNetwork(nwPanelNet("172.31.255.248/29")) }, code: errcode.PanelAlreadyExposed},
		{name: "network of that name without the label", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24"))
		}, code: errcode.PanelAlreadyExposed},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("ListNetworks", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			_, err := networkCreateAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
			_ = nwRefusal(t, err, tt.code)
			if len(d.Created) != 0 {
				t.Error("a refused plan created a network")
			}
		})
	}
	t.Run("client cannot be built", func(t *testing.T) {
		deps := agentDeps{docker: func() (panelDocker, error) { return nil, errors.New("no socket") }}
		_, err := networkCreateAction(deps).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkCreateInput{}))
		if err == nil || !core.IsDefinite(err) || !strings.HasPrefix(err.Error(), "panel:") {
			t.Errorf("error %v, want a definite panel: error", err)
		}
	})
}

// TestNwCreateApply: apply journals the creation before the call, makes the network the spec describes (the label, the
// bridge name, IPv6 off, exactly one address configuration with the gateway), records it made with its state, and its
// output and verify agree with the Engine.
func TestNwCreateApply(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	j := &nwJournal{}
	createdAtIntend := -1
	j.onIntend = func() { createdAtIntend = len(d.Created) }
	out, err := nwApplyCreate(t, d, j)
	if err != nil {
		t.Fatal(err)
	}
	if createdAtIntend != 0 {
		t.Errorf("%d networks existed when the effect was journaled, want 0", createdAtIntend)
	}
	ipv6 := false
	want := dockerapi.NetworkCreate{
		Name: "darkpanel-panel", Driver: "bridge", EnableIPv6: &ipv6,
		IPAM: &dockerapi.IPAM{Config: []dockerapi.IPAMConfig{{
			Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		}}},
		Options: map[string]string{"com.docker.network.bridge.name": "dp-panel"},
		Labels:  map[string]string{"darkpanel.module": "panel"},
	}
	if !reflect.DeepEqual(d.Created, []dockerapi.NetworkCreate{want}) {
		t.Errorf("created %+v, want %+v", d.Created, want)
	}
	wantOut := NetworkView{
		Network: "darkpanel-panel", ID: "fdnet01", Subnet: netip.MustParsePrefix("172.31.255.248/29"),
		Gateway: netip.MustParseAddr("172.31.255.249"), Proxy: netip.MustParseAddr("172.31.255.250"),
	}
	if out != wantOut {
		t.Errorf("output %+v, want %+v", out, wantOut)
	}
	if len(j.list) != 1 {
		t.Fatalf("%d effects journaled, want 1", len(j.list))
	}
	e := j.list[0]
	n, err := d.InspectNetwork(ctx, "fdnet01")
	if err != nil {
		t.Fatal(err)
	}
	post, err := dstate.NetworkCreated(&n)
	if err != nil {
		t.Fatal(err)
	}
	if e.State != core.EffectMade || e.Kind != core.EffectCreate || e.Resource != "panel.network:fdnet01" ||
		!reflect.DeepEqual(e.Recovery, &core.RecoveryID{Kind: "name", Value: "darkpanel-panel"}) || string(e.Before.State) != "null" ||
		e.CommitWait != 2*time.Minute || e.Local || e.Post == nil || !bytes.Equal(e.Post.State, post) {
		t.Errorf("effect %+v, want a made creation of panel.network:fdnet01 recovered by name, committing within 2 minutes, with post %s", e, post)
	}
	if err := networkCreateAction(nwDeps(d)).Handler.Verify(ctx, nwEnv(j), nwJSON(t, NetworkCreateInput{}), nwJSON(t, out)); err != nil {
		t.Errorf("verify after apply: %v", err)
	}
}

// TestNwCreateApplyFailures: a refusal of the Engine is definite and skips the effect, any other failure leaves it
// intended for the engine to settle, and a network that appeared since the plan is refused without an effect.
func TestNwCreateApplyFailures(t *testing.T) {
	tests := []struct {
		name     string
		setup    func(d *fdDocker)
		definite bool
		code     errcode.Code
		states   []core.EffectState
	}{
		{name: "pool overlap refused", setup: func(d *fdDocker) {
			d.Fail("CreateNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "Pool overlaps with other one on this address space"})
		}, definite: true, states: []core.EffectState{core.EffectSkipped}},
		{name: "server error", setup: func(d *fdDocker) {
			d.Fail("CreateNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		}, states: []core.EffectState{core.EffectIntended}},
		{name: "engine lost during the call", setup: func(d *fdDocker) { d.Fail("CreateNetwork", dockerapi.ErrUnavailable) },
			states: []core.EffectState{core.EffectIntended}},
		{name: "network appeared since the plan", setup: func(d *fdDocker) { d.AddNetwork(nwPanelNet("172.31.255.248/29")) },
			definite: true, code: errcode.PanelAlreadyExposed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			j := &nwJournal{}
			_, err := nwApplyCreate(t, d, j)
			if err == nil {
				t.Fatal("apply succeeded")
			}
			if core.IsDefinite(err) != tt.definite {
				t.Errorf("definite = %v, want %v (%v)", core.IsDefinite(err), tt.definite, err)
			}
			if tt.code != "" {
				_ = nwRefusal(t, err, tt.code)
			}
			if got := nwStates(j); !slices.Equal(got, tt.states) {
				t.Errorf("effect states %v, want %v", got, tt.states)
			}
		})
	}
}

// TestNwCreateVerify: verify reads the network from the Engine alone and accepts only the network as it was created.
func TestNwCreateVerify(t *testing.T) {
	good := nwPanelNet("172.31.255.248/29")
	tests := []struct {
		name string
		net  *dockerapi.Network
		edit func(n *dockerapi.Network)
		has  string // "" when verify passes
	}{
		{name: "as created", net: &good},
		{name: "missing", has: "does not exist"},
		{name: "no label", net: &good, edit: func(n *dockerapi.Network) { n.Labels = nil }, has: core.LabelModule},
		{name: "label of another module", net: &good, edit: func(n *dockerapi.Network) { n.Labels = map[string]string{core.LabelModule: "npm"} }, has: core.LabelModule},
		{name: "another bridge", net: &good, edit: func(n *dockerapi.Network) { n.Options = nil }, has: "dp-panel"},
		{name: "IPv6 on", net: &good, edit: func(n *dockerapi.Network) { n.EnableIPv6 = true }, has: "IPv6"},
		{name: "two configurations", net: &good, edit: func(n *dockerapi.Network) {
			n.IPAM.Config = append(n.IPAM.Config, dockerapi.IPAMConfig{Subnet: netip.MustParsePrefix("fd00::/64")})
		}, has: "2 address configurations"},
		{name: "no configuration", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config = nil }, has: "0 address configurations"},
		{name: "a /24", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config[0].Subnet = netip.MustParsePrefix("172.31.255.0/24") }, has: "/29"},
		{name: "outside the range", net: &good, edit: func(n *dockerapi.Network) { n.IPAM.Config[0].Subnet = netip.MustParsePrefix("10.9.0.0/29") }, has: "/29"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			if tt.net != nil {
				n := *tt.net
				n.IPAM.Config = slices.Clone(n.IPAM.Config)
				if tt.edit != nil {
					tt.edit(&n)
				}
				d.AddNetwork(n)
			}
			err := networkCreateAction(nwDeps(d)).Handler.Verify(context.Background(), nwEnv(&nwJournal{}),
				nwJSON(t, NetworkCreateInput{}), nwJSON(t, NetworkView{}))
			if tt.has == "" {
				if err != nil {
					t.Fatalf("verify: %v", err)
				}
				return
			}
			if err == nil || !strings.HasPrefix(err.Error(), "panel:") || !strings.Contains(err.Error(), tt.has) {
				t.Errorf("verify error %v, want a panel: error holding %q", err, tt.has)
			}
		})
	}
}

// TestNwCreateObserve: a creation is found by the exact name, a made network by its id; the state is the network's
// created document, and a missing network is the null state.
func TestNwCreateObserve(t *testing.T) {
	ctx := context.Background()
	creation := core.Effect{Kind: core.EffectCreate, Resource: "panel.network:?darkpanel-panel"}
	made := core.Effect{Kind: core.EffectCreate, Resource: "panel.network:n1", State: core.EffectMade}
	doc := func(d *fdDocker, id string) json.RawMessage {
		t.Helper()
		n, err := d.InspectNetwork(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := dstate.NetworkCreated(&n)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	observe := func(d *fdDocker, e core.Effect) (core.Observation, error) {
		return networkCreateAction(nwDeps(d)).Handler.Observe(ctx, nwEnv(nil), e)
	}
	t.Run("creation not made", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwNet("darkpanel-panel-old", nil, "10.1.0.0/24"))
		d.AddNetwork(nwNet("xdarkpanel-panel", nil, "10.2.0.0/24"))
		obs, err := observe(d, creation)
		if err != nil || obs.Matches != 0 || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, %v; want no match and the null state", obs, err)
		}
	})
	t.Run("creation made", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		obs, err := observe(d, creation)
		if err != nil || obs.Matches != 1 || obs.Resource != "panel.network:n1" || !obs.Exclusive || !bytes.Equal(obs.Current.State, doc(d, "n1")) {
			t.Errorf("observation %+v, %v; want panel.network:n1 with the network's document", obs, err)
		}
	})
	t.Run("made network by id", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		obs, err := observe(d, made)
		if err != nil || obs.Matches != 1 || !obs.Exclusive || !bytes.Equal(obs.Current.State, doc(d, "n1")) || !strings.Contains(string(obs.Current.State), `"unused":true`) {
			t.Errorf("observation %+v, %v", obs, err)
		}
		nwAttach(d, NetworkName, "web")
		obs, err = observe(d, made)
		if err != nil || !strings.Contains(string(obs.Current.State), `"unused":false`) {
			t.Errorf("observation with a container attached %s, %v; want unused false", obs.Current.State, err)
		}
	})
	t.Run("made network gone", func(t *testing.T) {
		obs, err := observe(fdNewDocker(), made)
		if err != nil || string(obs.Current.State) != "null" {
			t.Errorf("observation %+v, %v; want the null state", obs, err)
		}
	})
	t.Run("foreign resources", func(t *testing.T) {
		for _, r := range []string{"panel.network:", "panel.network:?", "npm.network:n1", "n1"} {
			if _, err := observe(fdNewDocker(), core.Effect{Kind: core.EffectCreate, Resource: r}); err == nil {
				t.Errorf("Observe(%q) succeeded", r)
			}
		}
	})
	t.Run("engine unavailable", func(t *testing.T) {
		d := fdNewDocker()
		d.Fail("ListNetworks", dockerapi.ErrUnavailable)
		if _, err := observe(d, creation); !errors.Is(err, dockerapi.ErrUnavailable) {
			t.Errorf("error %v, want the Engine's", err)
		}
	})
}

// TestNwCreateInverse: the undo removes the network the creation made while no container is attached, an absent network
// is undone, and a network in use or an Engine refusal is a definite failure that removed nothing.
func TestNwCreateInverse(t *testing.T) {
	ctx := context.Background()
	setup := func(t *testing.T) (*fdDocker, core.ActionDef, core.Effect) {
		t.Helper()
		d := fdNewDocker()
		j := &nwJournal{}
		if _, err := nwApplyCreate(t, d, j); err != nil {
			t.Fatal(err)
		}
		return d, networkCreateAction(nwDeps(d)), j.list[0]
	}
	t.Run("removes and converges", func(t *testing.T) {
		d, def, e := setup(t)
		for i := range 2 {
			if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err != nil {
				t.Fatalf("inverse %d: %v", i+1, err)
			}
		}
		if !slices.Equal(d.Removed, []string{"darkpanel-panel"}) {
			t.Errorf("removed %v, want the network once", d.Removed)
		}
	})
	t.Run("container attached", func(t *testing.T) {
		d, def, e := setup(t)
		nwAttach(d, NetworkName, "web")
		_, err := def.Handler.Inverse(ctx, nwEnv(nil), e)
		if err == nil || !core.IsDefinite(err) || !strings.Contains(err.Error(), "attached") {
			t.Errorf("error %v, want a definite refusal naming the attached container", err)
		}
		if len(d.Removed) != 0 {
			t.Errorf("removed %v with a container attached", d.Removed)
		}
	})
	t.Run("refused by the Engine", func(t *testing.T) {
		d, def, e := setup(t)
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusForbidden, Message: "in use"})
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil || !core.IsDefinite(err) {
			t.Errorf("error %v, want a definite failure", err)
		}
	})
	t.Run("failed by the Engine", func(t *testing.T) {
		d, def, e := setup(t)
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil || core.IsDefinite(err) {
			t.Errorf("error %v, want a failure that is not definite", err)
		}
	})
	t.Run("creation resource", func(t *testing.T) {
		_, def, e := setup(t)
		e.Resource = "panel.network:?darkpanel-panel"
		if _, err := def.Handler.Inverse(ctx, nwEnv(nil), e); err == nil {
			t.Error("inverse of an unmade creation succeeded")
		}
	})
}

// TestNwCreateDeclaration: the action is a compensable, undoable agent action under the panel lock for admins, its
// schemas reflect, and the registry accepts it.
func TestNwCreateDeclaration(t *testing.T) {
	def := networkCreateAction(nwDeps(fdNewDocker()))
	s := def.Spec
	if s.Name != "panel.network.create" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"panel"}) || s.MinScope != core.ScopeAdmin || s.CompensatedBy != "" || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse == nil || def.Handler.Observe == nil {
		t.Error("the action has no undo or observer")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("registry: %v", err)
	}
}

// nwModule registers actions, so the registry's own checks run on them.
type nwModule struct{ defs []core.ActionDef }

func (m nwModule) Manifest() core.Manifest { return core.Manifest{ID: ID, Version: 1, Actions: m.defs} }
```

`internal/modules/panel/network_remove_test.go`:

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/docker/dstate"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

func nwApplyRemove(t *testing.T, d *fdDocker, j *nwJournal) (NetworkRemoveOutput, error) {
	t.Helper()
	raw, err := networkRemoveAction(nwDeps(d)).Handler.Apply(context.Background(), nwEnv(j), nwJSON(t, NetworkRemoveInput{}))
	if err != nil {
		return NetworkRemoveOutput{}, err
	}
	var out NetworkRemoveOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out, nil
}

// TestNwRemovePlan: the plan removes the panel network, plans an empty diff when it is already gone, and refuses a
// network of that name that the panel module did not make.
func TestNwRemovePlan(t *testing.T) {
	plan := func(d *fdDocker) (core.PlanOutcome, error) {
		return networkRemoveAction(nwDeps(d)).Handler.Plan(context.Background(), nwEnv(nil), nwJSON(t, NetworkRemoveInput{}))
	}
	t.Run("present", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		out, err := plan(d)
		if err != nil {
			t.Fatal(err)
		}
		if want := []core.DiffLine{{Op: "-", Text: "network darkpanel-panel"}}; !slices.Equal(out.Diff, want) || out.StateHash == "" {
			t.Errorf("diff %v and hash %q, want %v and a hash", out.Diff, out.StateHash, want)
		}
		if len(d.Removed) != 0 {
			t.Error("a plan removed a network")
		}
	})
	t.Run("absent", func(t *testing.T) {
		out, err := plan(fdNewDocker())
		if err != nil || len(out.Diff) != 0 || out.StateHash == "" {
			t.Errorf("diff %v, hash %q, error %v; want an empty diff and a hash", out.Diff, out.StateHash, err)
		}
	})
	t.Run("state hash", func(t *testing.T) {
		d := fdNewDocker()
		absent, _ := plan(d)
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		present, _ := plan(d)
		again, _ := plan(d)
		if present.StateHash != again.StateHash || present.StateHash == absent.StateHash {
			t.Errorf("hashes absent %s, present %s, again %s; want present stable and different from absent", absent.StateHash, present.StateHash, again.StateHash)
		}
		nwAttach(d, NetworkName, "web")
		if used, _ := plan(d); used.StateHash == present.StateHash {
			t.Error("the hash did not change when a container joined the network")
		}
	})
	refusals := []struct {
		name  string
		setup func(d *fdDocker)
		code  errcode.Code
	}{
		{name: "no label", setup: func(d *fdDocker) { d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24")) }, code: errcode.DockerOwned},
		{name: "label of another module", setup: func(d *fdDocker) {
			d.AddNetwork(nwNet(NetworkName, map[string]string{core.LabelModule: "npm"}, "10.9.0.0/24"))
		}, code: errcode.DockerOwned},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("InspectNetwork", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			tt.setup(d)
			_, err := plan(d)
			_ = nwRefusal(t, err, tt.code)
		})
	}
}

// TestNwRemoveApply: apply journals the deletion with the network's state before the call, removes the network and
// records it made; a network already gone is removed without an effect; apply converges when run again.
func TestNwRemoveApply(t *testing.T) {
	ctx := context.Background()
	t.Run("present", func(t *testing.T) {
		d := fdNewDocker()
		n := nwPanelNet("172.31.255.248/29")
		n.ID = "n1"
		d.AddNetwork(n)
		before, err := d.InspectNetwork(ctx, "n1")
		if err != nil {
			t.Fatal(err)
		}
		beforeDoc, err := dstate.NetworkCreated(&before)
		if err != nil {
			t.Fatal(err)
		}
		j := &nwJournal{}
		removedAtIntend := -1
		j.onIntend = func() { removedAtIntend = len(d.Removed) }
		out, err := nwApplyRemove(t, d, j)
		if err != nil {
			t.Fatal(err)
		}
		if out != (NetworkRemoveOutput{Network: "darkpanel-panel", Removed: true}) || !slices.Equal(d.Removed, []string{"darkpanel-panel"}) || removedAtIntend != 0 {
			t.Errorf("output %+v, removed %v, removed at intend %d", out, d.Removed, removedAtIntend)
		}
		if len(j.list) != 1 {
			t.Fatalf("%d effects journaled, want 1", len(j.list))
		}
		e := j.list[0]
		if e.State != core.EffectMade || e.Kind != core.EffectDelete || e.Resource != "panel.network:n1" || e.Recovery != nil ||
			!bytes.Equal(e.Before.State, beforeDoc) || e.CommitWait != 2*time.Minute || e.Local || e.Post == nil || string(e.Post.State) != "null" {
			t.Errorf("effect %+v, want a made deletion of panel.network:n1 from %s to null", e, beforeDoc)
		}
		if err := networkRemoveAction(nwDeps(d)).Handler.Verify(ctx, nwEnv(j), nwJSON(t, NetworkRemoveInput{}), nwJSON(t, out)); err != nil {
			t.Errorf("verify after apply: %v", err)
		}
		again, err := nwApplyRemove(t, d, &nwJournal{})
		if err != nil || again != (NetworkRemoveOutput{Network: "darkpanel-panel"}) || len(d.Removed) != 1 {
			t.Errorf("second apply: %+v, %v, removed %v; want removed false and no second removal", again, err, d.Removed)
		}
	})
	t.Run("absent", func(t *testing.T) {
		d := fdNewDocker()
		j := &nwJournal{}
		out, err := nwApplyRemove(t, d, j)
		if err != nil || out != (NetworkRemoveOutput{Network: "darkpanel-panel"}) || len(j.list) != 0 || len(d.Removed) != 0 {
			t.Errorf("output %+v, %v, effects %d, removed %v; want removed false and nothing journaled", out, err, len(j.list), d.Removed)
		}
	})
	t.Run("gone during the call", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusNotFound, Message: "no such network"})
		j := &nwJournal{}
		out, err := nwApplyRemove(t, d, j)
		if err != nil || !out.Removed || !slices.Equal(nwStates(j), []core.EffectState{core.EffectMade}) {
			t.Errorf("output %+v, %v, effects %v; want removed and the effect made", out, err, nwStates(j))
		}
	})
	t.Run("container attached", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		nwAttach(d, NetworkName, "web")
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		if err == nil || !core.IsDefinite(err) || !slices.Equal(nwStates(j), []core.EffectState{core.EffectSkipped}) {
			t.Errorf("error %v, effects %v; want a definite refusal and a skipped effect", err, nwStates(j))
		}
		if _, err := d.InspectNetwork(ctx, NetworkName); err != nil {
			t.Errorf("the network is gone: %v", err)
		}
	})
	t.Run("failed by the Engine", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwPanelNet("172.31.255.248/29"))
		d.Fail("RemoveNetwork", &dockerapi.APIError{StatusCode: http.StatusInternalServerError, Message: "boom"})
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		if err == nil || core.IsDefinite(err) || !slices.Equal(nwStates(j), []core.EffectState{core.EffectIntended}) {
			t.Errorf("error %v, effects %v; want a failure that is not definite and an intended effect", err, nwStates(j))
		}
	})
	t.Run("not the panel's network", func(t *testing.T) {
		d := fdNewDocker()
		d.AddNetwork(nwNet(NetworkName, nil, "10.9.0.0/24"))
		j := &nwJournal{}
		_, err := nwApplyRemove(t, d, j)
		_ = nwRefusal(t, err, errcode.DockerOwned)
		if len(j.list) != 0 || len(d.Removed) != 0 {
			t.Errorf("effects %d, removed %v for a network the module does not own", len(j.list), d.Removed)
		}
	})
}

// TestNwRemoveVerify: verify passes only when the network is gone.
func TestNwRemoveVerify(t *testing.T) {
	verify := func(d *fdDocker) error {
		return networkRemoveAction(nwDeps(d)).Handler.Verify(context.Background(), nwEnv(&nwJournal{}),
			nwJSON(t, NetworkRemoveInput{}), nwJSON(t, NetworkRemoveOutput{}))
	}
	d := fdNewDocker()
	if err := verify(d); err != nil {
		t.Errorf("verify with the network gone: %v", err)
	}
	d.AddNetwork(nwPanelNet("172.31.255.248/29"))
	if err := verify(d); err == nil || !strings.HasPrefix(err.Error(), "panel:") {
		t.Errorf("verify with the network present = %v, want a panel: error", err)
	}
	d.Fail("InspectNetwork", dockerapi.ErrUnavailable)
	if err := verify(d); !errors.Is(err, dockerapi.ErrUnavailable) {
		t.Errorf("verify with the Engine down = %v, want the Engine's error", err)
	}
}

// TestNwRemoveObserve: the observer reads a deleted network's resource by id, as null once the network is gone.
func TestNwRemoveObserve(t *testing.T) {
	ctx := context.Background()
	d := fdNewDocker()
	n := nwPanelNet("172.31.255.248/29")
	n.ID = "n1"
	d.AddNetwork(n)
	e := core.Effect{Kind: core.EffectDelete, Resource: "panel.network:n1", State: core.EffectIntended}
	def := networkRemoveAction(nwDeps(d))
	obs, err := def.Handler.Observe(ctx, nwEnv(nil), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) == "null" || !strings.Contains(string(obs.Current.State), `"id":"n1"`) {
		t.Errorf("observation before removal %+v, %v", obs, err)
	}
	if err := d.RemoveNetwork(ctx, "n1"); err != nil {
		t.Fatal(err)
	}
	obs, err = def.Handler.Observe(ctx, nwEnv(nil), e)
	if err != nil || obs.Matches != 1 || string(obs.Current.State) != "null" {
		t.Errorf("observation after removal %+v, %v; want the null state", obs, err)
	}
}

// TestNwRemoveDeclaration: the action is a compensable agent action compensated by panel.network.create, converging,
// without an undo of its own, and the registry resolves its compensating action only when both are registered.
func TestNwRemoveDeclaration(t *testing.T) {
	d := nwDeps(fdNewDocker())
	def := networkRemoveAction(d)
	s := def.Spec
	if s.Name != "panel.network.remove" || s.Version != 1 || s.Class != core.ClassCompensable || s.Side != core.SideAgent ||
		!slices.Equal(s.LockKeys, []string{"panel"}) || s.MinScope != core.ScopeAdmin || !s.Converges || s.CompensatedBy != "panel.network.create" || s.ForwardOnly {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Inverse != nil || def.Handler.Observe == nil {
		t.Error("the action has an undo of its own or no observer")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{networkCreateAction(d), def}}); err != nil {
		t.Errorf("registry with both actions: %v", err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err == nil || !strings.Contains(err.Error(), "panel.network.create") {
		t.Errorf("registry with the removal alone = %v, want its compensating action reported missing", err)
	}
}
```

`internal/modules/panel/connector_check_test.go`:

```go
package panel

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/platform/dockerapi"
)

const nwTunnel = "0123abcd-0123-4abc-8def-0123456789ab"

// nwConnector is the connector container of nwTunnel, running and labelled darkpanel.module=cloudflare.
func nwConnector() dockerapi.ContainerJSON {
	return dockerapi.ContainerJSON{
		ID: "ctr1", Name: "/darkpanel-cloudflared-" + nwTunnel,
		State:  dockerapi.ContainerState{Status: "running", Running: true},
		Config: dockerapi.ContainerConfig{Labels: map[string]string{core.LabelModule: "cloudflare"}},
	}
}

// TestNwConnectorCheck: the check passes for a running connector of the tunnel that the cloudflare module made, and
// refuses every other case with cloudflare.connector_missing, a bad tunnel with request.invalid_input and an unreachable
// Engine with docker.engine_unavailable.
func TestNwConnectorCheck(t *testing.T) {
	tests := []struct {
		name   string
		mod    func(c *dockerapi.ContainerJSON)
		setup  func(d *fdDocker)
		tunnel string       // "" means nwTunnel
		empty  bool         // the input names no tunnel at all
		absent bool         // the container does not exist
		code   errcode.Code // "" when the check passes
		has    string
		field  string
	}{
		{name: "running"},
		{name: "container missing", mod: nil, setup: func(d *fdDocker) {
			d.Fail("InspectContainer", &dockerapi.APIError{StatusCode: 404, Message: "No such container"})
		},
			code: errcode.CloudflareConnectorMissing, has: "does not exist"},
		{name: "another tunnel's connector", tunnel: "11111111-1111-4111-8111-111111111111", code: errcode.CloudflareConnectorMissing, has: "does not exist"},
		{name: "no label", mod: func(c *dockerapi.ContainerJSON) { c.Config.Labels = nil }, code: errcode.CloudflareConnectorMissing, has: "darkpanel.module=cloudflare"},
		{name: "label of another module", mod: func(c *dockerapi.ContainerJSON) { c.Config.Labels = map[string]string{core.LabelModule: "npm"} },
			code: errcode.CloudflareConnectorMissing, has: "darkpanel.module=cloudflare"},
		{name: "stopped", mod: func(c *dockerapi.ContainerJSON) { c.State = dockerapi.ContainerState{Status: "exited"} },
			code: errcode.CloudflareConnectorMissing, has: "exited"},
		{name: "restarting", mod: func(c *dockerapi.ContainerJSON) { c.State = dockerapi.ContainerState{Status: "restarting"} },
			code: errcode.CloudflareConnectorMissing, has: "restarting"},
		{name: "tunnel not a UUID", tunnel: "my-tunnel", code: errcode.InvalidInput, field: "/tunnel"},
		{name: "empty tunnel", empty: true, code: errcode.InvalidInput, field: "/tunnel"},
		{name: "engine unavailable", setup: func(d *fdDocker) { d.Fail("InspectContainer", dockerapi.ErrUnavailable) }, code: errcode.DockerEngineUnavailable},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := fdNewDocker()
			c := nwConnector()
			if tt.mod != nil {
				tt.mod(&c)
			}
			if !tt.absent {
				d.AddContainer(c)
			}
			if tt.setup != nil {
				tt.setup(d)
			}
			tunnel := nwTunnel
			switch {
			case tt.empty:
				tunnel = ""
			case tt.tunnel != "":
				tunnel = tt.tunnel
			}
			def := connectorCheckAction(nwDeps(d))
			identity, raw, err := def.Handler.Reveal(context.Background(), nwEnv(nil), nwJSON(t, ConnectorCheckInput{Tunnel: tunnel}))
			if tt.code == "" {
				var out ConnectorCheckOutput
				if err != nil || json.Unmarshal(raw, &out) != nil || out != (ConnectorCheckOutput{Container: "darkpanel-cloudflared-" + nwTunnel, State: "running"}) {
					t.Fatalf("output %s, error %v; want the running connector", raw, err)
				}
				if !strings.Contains(identity, nwTunnel) {
					t.Errorf("identity %q does not name the tunnel", identity)
				}
				return
			}
			coded := nwRefusal(t, err, tt.code)
			if tt.has != "" && !strings.Contains(coded.Detail, tt.has) {
				t.Errorf("detail %q does not hold %q", coded.Detail, tt.has)
			}
			if tt.field != "" && (len(coded.Fields) != 1 || coded.Fields[0].Path != tt.field) {
				t.Errorf("fields %v, want one at %s", coded.Fields, tt.field)
			}
		})
	}
}

// TestNwConnectorCheckDeclaration: the check is an internal read on the agent for admins, without locks, and its
// schemas reflect.
func TestNwConnectorCheckDeclaration(t *testing.T) {
	def := connectorCheckAction(nwDeps(fdNewDocker()))
	s := def.Spec
	if s.Name != "panel.connector.check" || s.Version != 1 || s.Class != core.ClassRead || s.Side != core.SideAgent || !s.Internal ||
		s.MinScope != core.ScopeAdmin || len(s.LockKeys) != 0 {
		t.Errorf("spec %+v", s)
	}
	if def.Handler.Reveal == nil || def.Handler.Read != nil {
		t.Error("the check is not an internal read")
	}
	if _, err := schema.Input(def.InputType); err != nil {
		t.Error(err)
	}
	if _, err := schema.Output(def.OutputType); err != nil {
		t.Error(err)
	}
	if _, err := core.NewRegistry(nwModule{defs: []core.ActionDef{def}}); err != nil {
		t.Errorf("registry: %v", err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

```bash
mkdir -p /root/.cache/w8-tmp/6 && export TMPDIR=/root/.cache/w8-tmp/6 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestNw' ./internal/modules/panel/
```

Expected: `FAIL ... [build failed]` with `undefined: networkCreateAction` (and `networkRemoveAction`, `connectorCheckAction`). Quote the output verbatim.

- [ ] **Step 3: Implement**

Common rules. The lock key is `"panel"` (an unexported constant of this task), every action has `MinScope: core.ScopeAdmin`, and a docker client comes from `d.docker()` on every call (never kept); when that fails, the error is a `core.Definite` `panel: connect to the Docker Engine: %w`. A failed Engine read at plan time is `core.Definite(fmt.Errorf("panel: <what>: %w", err))`, where an error that `errors.Is` `dockerapi.ErrUnavailable` is first wrapped as `errcode.Wrap(errcode.DockerEngineUnavailable, err, "the Docker Engine is not reachable")` (one shared helper), so the code reaches the caller. An Engine answer is a **refusal** when it is a `*dockerapi.APIError` with a status from 400 to 499 (one shared predicate); a refusal proves the call committed nothing. The effect resource of an Engine network is `panel.network:<network id>`, and a creation's is `panel.network:?darkpanel-panel`. The effects' `CommitWait` is 2 minutes and none is `Local`. Hashes are the hex SHA-256 of a JSON document (one shared helper).

1. `networkCreateAction` is `core.DefineMutating[NetworkCreateInput, NetworkView]` with `Name: ActionNetCreate`, `Version: 1`, `Class: core.ClassCompensable`, `Side: core.SideAgent`, the lock key, `MinScope: admin` and a one-sentence `Summary`; its handler `networkCreateHandler` has `Plan`, `Apply`, `Verify`, `Observe` and `Inverse`, so `DefineMutating` wires the observer and the undo. The registry then needs no `CompensatedBy` or `ForwardOnly`.
   - **Choice** (shared by `Plan` and `Apply`): `ListNetworks(ctx, nil)`. A network named `darkpanel-panel`, whatever its labels, is refused with a `core.Definite` `errcode.New(errcode.PanelAlreadyExposed, ...)`. Otherwise the subnet is the first of `172.31.255.248/29`, `.240/29`, ..., `.0/29` (descending from the top of `PanelRange`) that overlaps (`netip.Prefix.Overlaps`) no IPv4 `IPAMConfig.Subnet` of any listed network; an IPv6 or invalid subnet and a network without address configuration contribute none. When none is free the refusal is a `core.Definite` plain error whose text begins `panel: no free /29 is left in 172.31.255.0/24` (not an `*errcode.Error`, because no registered code fits). The gateway is the subnet's first host address, the proxy address the second.
   - **Plan**: the diff is exactly two lines, `+ network darkpanel-panel <subnet>` and `+ gateway <gateway>, proxy <proxy>`; `Guards` is the empty slice; `StateHash` is the hash of the document `{"subnet": "<subnet>"}`, so it changes when another network moves the choice and only then (the engine plans again at apply and refuses an apply whose hash changed). A plan creates nothing.
   - **Apply**: repeat the choice. Journal the creation *before* the call: `core.Effect{Kind: core.EffectCreate, Resource: "panel.network:?darkpanel-panel", Recovery: &core.RecoveryID{Kind: "name", Value: "darkpanel-panel"}, CommitWait: 2 * time.Minute, Before: StateDoc{State: null}}` through `env.Effects.Intend`. Then `CreateNetwork` with exactly this request (the tests compare it whole): `Name` `darkpanel-panel`, `Driver` `"bridge"`, `EnableIPv6` a pointer to `false`, `IPAM` with exactly one `IPAMConfig{Subnet, Gateway}`, `Options` `{BridgeOption: BridgeName}`, `Labels` `{core.LabelModule: ID}`, and no other field (this is register row 328: exactly one IPv4 configuration, IPv6 off, which `npm`'s reserved-upstream read relies on). A refusal skips the effect (`Skipped`) and returns a `core.Definite` error; any other failure, an unreachable Engine included, leaves the effect intended and returns the wrapped error, not Definite, for the engine to settle through `Observe`. On success `InspectNetwork(id)`, render it with `dstate.NetworkCreated`, record `Made(seq, "panel.network:"+id, StateDoc{State: doc})` and return the `NetworkView` of the inspected network: `Network` its name, `ID`, `Subnet` the masked subnet of its first IPv4 configuration, `Gateway` that configuration's gateway (the first host address when it reports none), `Proxy` the gateway's next address.
   - **Verify** reads the Engine only and ignores its `out` (startup recovery passes the zero value). `InspectNetwork("darkpanel-panel")`: not found is the error `panel: network darkpanel-panel does not exist`. Otherwise it collects problems and returns `panel: network darkpanel-panel is not as created: <problems joined by "; ">` when there are any: `it lacks the label darkpanel.module=panel` (label absent or another value), `its bridge is not dp-panel` (option `BridgeOption` absent or another value), `IPv6 is on`, `it has <n> address configurations, not 1` and, for a single configuration, `its subnet is not a /29 of 172.31.255.0/24`.
   - **Observe** reads one effect's resource. A resource that is not `panel.network:<identity>` with a non-empty identity is an error. For a made network (identity not starting with `?`) `InspectNetwork(identity)` is the state `dstate.NetworkCreated` renders, or the state `null` when the network is gone, with `Matches: 1, Exclusive: true`. For a creation (`?<name>`) `ListNetworks` filtered by the name, keeping only networks whose name equals it exactly (the Engine's filter matches substrings), each rendered the same way: none is `Matches 0` and the null state; one is `Matches 1`, `Exclusive`, `Resource: "panel.network:<id>"` and that state; several are `Matches n` with every `Candidate` and the null state. This function is shared with `panel.network.remove`.
   - **Inverse** undoes a made creation: a resource that is a creation (`?`), or an effect that is not a creation, is an error. `InspectNetwork(id)`: not found is undone; a network with any attached container (`Containers` non-empty) is a `core.Definite` error naming the attached containers (`panel: network <name> still has <n> attached container(s)`) and removes nothing; otherwise `RemoveNetwork(id)` (by id, never by name): success or not found is undone, a refusal is `core.Definite`, any other failure is returned wrapped and not Definite.
2. `networkRemoveAction` is `core.DefineMutating[NetworkRemoveInput, NetworkRemoveOutput]` with `Name: ActionNetRemove`, `Class: compensable`, `Side: agent`, the lock key, `Converges: true`, `CompensatedBy: ActionNetCreate`, `MinScope: admin`; its handler has `Plan`, `Apply`, `Verify` and `Observe` (the shared function) and no `Inverse`.
   - **Find** (shared by `Plan` and `Apply`): `InspectNetwork("darkpanel-panel")`. Not found is "absent". A network whose labels lack `darkpanel.module=panel` (no label, or another module's) is refused with a `core.Definite` `errcode.New(errcode.DockerOwned, ...)` saying it is not labelled and Darkpanel will not remove it (W8-R9); the check runs again in `Apply`.
   - **Plan**: absent: an empty diff; present: the single line `- network darkpanel-panel`. `Guards` is the empty slice. `StateHash` is the hash of `{"id": "<network id>", "containers": [<sorted names of the attached containers>]}` (`{"id": "", "containers": []}` when absent), so it differs between absent and present and changes when a container joins.
   - **Apply**: absent: return `NetworkRemoveOutput{Network: "darkpanel-panel"}` (`Removed` false) and journal nothing. Present: journal `core.Effect{Kind: core.EffectDelete, Resource: "panel.network:<id>", CommitWait: 2 * time.Minute, Before: StateDoc{State: dstate.NetworkCreated of the inspected network}}` before the call, then `RemoveNetwork(id)`. Success, and not found (the network went away in between), record `Made(seq, resource, StateDoc{State: null})` and return `Removed: true`. A refusal (an attached container makes the Engine answer 403) skips the effect and returns a `core.Definite` error; any other failure leaves the effect intended and returns the wrapped error, not Definite.
   - **Verify**: `InspectNetwork` not found is success; a present network is the error `panel: network darkpanel-panel still exists`; any other failure is returned wrapped.
3. `connectorCheckAction` is `core.DefineInternalRead[ConnectorCheckInput, ConnectorCheckOutput]` with `Name: ActionConnCheck`, `Version: 1`, `Side: core.SideAgent`, `MinScope: admin` (the definition sets the read class and the internal flag), no lock key. The read, in this order: a `tunnel` that does not match the lower-case UUID pattern `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` (a pattern this task declares itself) is a `core.Definite` `*errcode.Error` `request.invalid_input` with one field `/tunnel`; then `InspectContainer(ConnectorPrefix + tunnel)`: not found, a container whose `Config.Labels["darkpanel.module"]` is not `cloudflare`, and a container that is not `State.Running` are each a `core.Definite` `errcode.New(errcode.CloudflareConnectorMissing, "the connector <name> <why>")` with `<why>` `does not exist on this host`, `is not labelled darkpanel.module=cloudflare` and `is not running (state <status>)` (W8-R5); any other Engine failure is the shared Definite engine error. The result is `core.Revealed{Identity: "panel connector check for tunnel <tunnel>", Value: ConnectorCheckOutput{Container: <name>, State: <the Engine's State.Status>}}`. It changes nothing.
4. Every error string starts with `panel:`; no log line is written; comments only for a non-obvious why, never naming a plan, task or register row.

- [ ] **Step 4: Run the tests to verify they pass**

```bash
timeout 600 go test -count=1 -run 'TestNw' ./internal/modules/panel/
timeout 600 go test -count=1 ./internal/modules/panel/
timeout 600 go test -race -count=1 ./internal/modules/panel/
```

Expected: `ok` for all three. Quote the output verbatim.

- [ ] **Step 5: Check and commit**

```bash
timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff
rm -rf /root/.cache/w8-tmp/6 && go clean -testcache
git add internal/modules/panel
git commit -m "feat(panel): add network and connector actions"
```

Expected: `go vet` and `go mod tidy -diff` print nothing, every `go test` line is `ok` or `?`, and `golangci-lint` prints `0 issues.`.

### Task 7: panel: listener open and revoke, exposure status

**Files:**
- Create: `internal/modules/panel/listener.go` (`panel.listener.open`)
- Create: `internal/modules/panel/listener_revoke.go` (`panel.listener.revoke`)
- Create: `internal/modules/panel/status.go` (`panel.exposure.status`)
- Test: `internal/modules/panel/listener_test.go`, `internal/modules/panel/listener_revoke_test.go`, `internal/modules/panel/status_test.go`

**Interfaces:**
- Consumes (Contracts): "Panel names" (`ID`, `ActionAdd`, `ActionRemove`, `ActionOpen`, `ActionRevoke`, `ActionStatus`, `ViaNPM`, `ViaTunnel`, `StateOpen`, `StateRevoked`, `StateNeedsRepair`, `DefaultPort`, `PanelRange`); "Wire types" (`ExposeInput`, `ValidateExpose`, `ListenerOpenInput`, `ListenerOpenOutput`, `ListenerRevokeInput`, `ListenerRevokeOutput`, `StatusInput`, `StatusOutput`, `ExposureView`); "Exposure store" (`exposureRow`, `liveExposure`, `insertExposure`, `setExposureState`, `deleteExposure`, `view`, `RuleID`, `GoneSQL`); "Panel controller fixture" (`fdDB`, `fdNewListeners`, `fdListeners`, `fdEnv`, `fdInsertHost`, `fdInsertChange`, `fdInsertExposure`, `fdNow`); "Listener open and revoke" for the behaviour; "Action constructors" for the three constructor signatures.
- Produces (Contracts "Action constructors"): `listenerOpenAction() core.ActionDef`, `listenerRevokeAction() core.ActionDef`, `statusAction() core.ActionDef`. Task 11 lists them in the manifest, Task 9 and Task 10 name the two listener actions as child steps, and the web page and banner (Task 5) show the status. The unexported names this task adds all start with `listener` or `status`.

**Items:** 36

**Depends on:** Task 4
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 3 = 7

- [ ] **Step 1: Write the failing tests**

The tests drive the handlers directly through the erased handler (`def.Handler.Plan`, `Apply`, `Verify`, `Observe`, `Inverse`, `Read`), as the cloudflare tests do, over the Task 4 fixtures. `lsJournal` is the in-memory effect journal the apply writes to; the revoke and status tests give their env no journal, so a handler that journals there fails with a nil dereference. Create the three files with exactly this content.

`internal/modules/panel/listener_test.go`:

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
)

const (
	lsAdd    = "01JADD00000000000000000001"
	lsRemove = "01JREMOVE00000000000000001"
	lsOther  = "01JOTHER0000000000000000001"
	lsChild  = "01JCHILD000000000000000001"
	lsHost   = "h1"
	lsName   = "panel.example.com"
	lsTunnel = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
)

var (
	lsSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	lsGateway = netip.MustParseAddr("172.31.255.249")
	lsProxy   = netip.MustParseAddr("172.31.255.250")
	lsAddCall = core.CallContext{ParentChangeID: lsAdd, ParentAction: ActionAdd, StepID: "listen"}
	lsRmCall  = core.CallContext{ParentChangeID: lsRemove, ParentAction: ActionRemove, StepID: "revoke"}
)

// lsModule serves the three actions under the panel module id, so these tests do not depend on the module's manifest.
type lsModule struct{}

func (lsModule) Manifest() core.Manifest {
	return core.Manifest{
		ID: ID, Version: 1,
		Actions: []core.ActionDef{listenerOpenAction(), listenerRevokeAction(), statusAction()},
	}
}

// lsJournal is an in-memory core.EffectJournal.
type lsJournal struct {
	changeID string
	list     []core.Effect
}

func (j *lsJournal) Intend(_ context.Context, e core.Effect, _ []byte) (int, error) {
	e.Seq, e.State, e.ChangeID = len(j.list)+1, core.EffectIntended, j.changeID
	j.list = append(j.list, e)
	return e.Seq, nil
}

func (j *lsJournal) find(seq int) (*core.Effect, error) {
	if seq < 1 || seq > len(j.list) || j.list[seq-1].State != core.EffectIntended {
		return nil, fmt.Errorf("fake: effect %d is not intended", seq)
	}
	return &j.list[seq-1], nil
}

func (j *lsJournal) Made(_ context.Context, seq int, resource string, post core.StateDoc) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State, e.Resource, e.Post = core.EffectMade, resource, &post
	return nil
}

func (j *lsJournal) Skipped(_ context.Context, seq int) error {
	e, err := j.find(seq)
	if err != nil {
		return err
	}
	e.State = core.EffectSkipped
	return nil
}

func (j *lsJournal) List(context.Context) ([]core.Effect, error) {
	return append([]core.Effect(nil), j.list...), nil
}

func (j *lsJournal) Secret(context.Context, int) ([]byte, error) {
	return nil, errors.New("fake: no secret")
}

var _ core.EffectJournal = (*lsJournal)(nil)

// lsRacy opens a listener as fdListeners does and then runs onOpen, which plays another change that exposes the
// panel while this one is still opening its listener.
type lsRacy struct {
	*fdListeners
	onOpen func()
}

func (r lsRacy) Open(ctx context.Context, spec core.ListenerSpec) (string, error) {
	id, err := r.fdListeners.Open(ctx, spec)
	r.onOpen()
	return id, err
}

// lsRig is a controller database with the controller's host, the in-memory listeners and one effect journal.
type lsRig struct {
	t  *testing.T
	db *store.DB
	l  *fdListeners
	j  *lsJournal
}

func lsNewRig(t *testing.T) *lsRig {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, lsHost, true)
	return &lsRig{t: t, db: db, l: fdNewListeners(), j: &lsJournal{}}
}

// env is the controller env of a call; a change id also gives it the effect journal.
func (r *lsRig) env(call core.CallContext, changeID string) core.Env {
	r.t.Helper()
	env := fdEnv(r.t, r.db, r.l, call)
	if changeID != "" {
		r.j.changeID = changeID
		env.ChangeID, env.Effects = changeID, r.j
	}
	return env
}

// live is the live exposure row.
func (r *lsRig) live() (exposureRow, bool) {
	r.t.Helper()
	row, ok, err := liveExposure(context.Background(), r.db.Read())
	if err != nil {
		r.t.Fatal(err)
	}
	return row, ok
}

// expose stores the exposure lsAdd in rowState with removal as its removal change, behind a listener that the
// fake holds in listenerState (open, revoked or closed), and returns the row.
func (r *lsRig) expose(rowState, removal, listenerState string) exposureRow {
	r.t.Helper()
	ctx := context.Background()
	id, err := r.l.Open(ctx, lsSpec(ViaNPM))
	if err != nil {
		r.t.Fatal(err)
	}
	if listenerState != core.ListenerOpen {
		if err := r.l.Revoke(ctx, id); err != nil {
			r.t.Fatal(err)
		}
	}
	if listenerState == core.ListenerClosed {
		if err := r.l.Close(ctx, id); err != nil {
			r.t.Fatal(err)
		}
	}
	row := lsRow(lsAdd, rowState, removal)
	row.ListenerID = id
	fdInsertExposure(r.t, r.db, row)
	return row
}

// lsRow is an exposure through NPM with the given id, state and removal change.
func lsRow(id, state, removal string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: state, RemovalChangeID: removal,
		CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

// lsSpec is the runtime listener the open action asks for.
func lsSpec(via string) core.ListenerSpec {
	header := core.ClientHeaderNPM
	if via == ViaTunnel {
		header = core.ClientHeaderTunnel
	}
	return core.ListenerSpec{
		Addr: netip.AddrPortFrom(lsGateway, DefaultPort), AllowedPeer: lsProxy, ClientHeader: header, PublicHost: lsName, ChangeID: lsAdd,
	}
}

func lsOpenInput(via string) ListenerOpenInput {
	in := ListenerOpenInput{
		Via: via, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy, Port: DefaultPort,
	}
	if via == ViaTunnel {
		in.Tunnel = lsTunnel
	}
	return in
}

func lsJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// lsDecode decodes raw into a T and refuses a member T does not have.
func lsDecode[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("output %s: %v", raw, err)
	}
	return v
}

func lsDiff(diff []core.DiffLine) string {
	var b strings.Builder
	for _, l := range diff {
		b.WriteString(l.Op + " " + l.Text + "\n")
	}
	return b.String()
}

// lsWantCode fails the test unless err carries code.
func lsWantCode(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	if err == nil || errcode.From(err).Code != code {
		t.Errorf("%s: error %v, want code %s", what, err, code)
	}
}

// lsWantRefusal fails the test unless err carries code and is marked as a refusal that committed nothing.
func lsWantRefusal(t *testing.T, what string, err error, code errcode.Code) {
	t.Helper()
	lsWantCode(t, what, err, code)
	if err != nil && !core.IsDefinite(err) {
		t.Errorf("%s: error %v is not marked definite", what, err)
	}
}

// TestLsDeclarations: the three actions are controller-side; the open action is compensable with an undo, the
// revoke action is compensable, forward-only, converging and break-glass without an undo, the status action is a
// read. All three need the admin scope, and the registry accepts the set.
func TestLsDeclarations(t *testing.T) {
	open, revoke, status := listenerOpenAction(), listenerRevokeAction(), statusAction()
	for _, tc := range []struct {
		def   core.ActionDef
		name  string
		class core.Class
	}{
		{open, ActionOpen, core.ClassCompensable},
		{revoke, ActionRevoke, core.ClassCompensable},
		{status, ActionStatus, core.ClassRead},
	} {
		s := tc.def.Spec
		if s.Name != tc.name || s.Version != 1 || s.Summary == "" || s.Class != tc.class || s.Side != core.SideController || s.MinScope != core.ScopeAdmin {
			t.Errorf("%s: spec %+v, want version 1, a summary, class %s, controller side, admin scope", tc.name, s, tc.class)
		}
		if _, err := schema.ActionSnapshot(tc.def); err != nil {
			t.Errorf("%s: schema: %v", tc.name, err)
		}
	}
	if !slices.Equal(open.Spec.LockKeys, []string{"panel"}) || open.Spec.ForwardOnly || open.Spec.BreakGlass || open.Handler.Inverse == nil || open.Handler.Observe == nil {
		t.Errorf("open: spec %+v, want lock key panel, an undo and an observer, not forward-only and not break-glass", open.Spec)
	}
	if !slices.Equal(revoke.Spec.LockKeys, []string{"panel"}) || !revoke.Spec.ForwardOnly || !revoke.Spec.Converges || !revoke.Spec.BreakGlass || revoke.Handler.Inverse != nil {
		t.Errorf("revoke: spec %+v, want lock key panel, forward-only, converging and break-glass, with no undo", revoke.Spec)
	}
	if len(status.Spec.LockKeys) != 0 || status.Handler.Read == nil {
		t.Errorf("status: spec %+v, want a read handler and no lock", status.Spec)
	}
	if _, err := core.NewRegistry(lsModule{}); err != nil {
		t.Errorf("registry: %v", err)
	}
	for via, name := range map[string]string{ViaNPM: "npm", ViaTunnel: "tunnel"} {
		if err := schema.NewValidator().ValidateInput(open, lsJSON(t, lsOpenInput(via))); err != nil {
			t.Errorf("a valid input via %s is refused by the open schema: %v", name, err)
		}
	}
}

// TestLsOpenRefusals: the open action runs only as a step of panel.exposure.add, checks its input and refuses while
// an exposure is live, at plan and at apply, before it touches a listener or the journal.
func TestLsOpenRefusals(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	badAddr := func(subnet, gateway, proxy string) func(in *ListenerOpenInput) {
		return func(in *ListenerOpenInput) {
			in.Subnet, in.Gateway, in.Proxy = netip.MustParsePrefix(subnet), netip.MustParseAddr(gateway), netip.MustParseAddr(proxy)
		}
	}
	tests := []struct {
		name string
		call core.CallContext
		edit func(in *ListenerOpenInput)
		live bool
		want errcode.Code
	}{
		{"no parent", core.CallContext{}, nil, false, errcode.InvalidInput},
		{"the removal as parent", core.CallContext{ParentChangeID: lsAdd, ParentAction: ActionRemove, StepID: "listen"}, nil, false, errcode.InvalidInput},
		{"a parent without a change id", core.CallContext{ParentAction: ActionAdd, StepID: "listen"}, nil, false, errcode.InvalidInput},
		{"an unknown via", lsAddCall, func(in *ListenerOpenInput) { in.Via = "carrier" }, false, errcode.InvalidInput},
		{"a tunnel for npm", lsAddCall, func(in *ListenerOpenInput) { in.Tunnel = lsTunnel }, false, errcode.InvalidInput},
		{"a hostname in capitals", lsAddCall, func(in *ListenerOpenInput) { in.Hostname = "Panel.Example.com" }, false, errcode.InvalidInput},
		{"no port", lsAddCall, func(in *ListenerOpenInput) { in.Port = 0 }, false, errcode.InvalidInput},
		{"a port below 1024", lsAddCall, func(in *ListenerOpenInput) { in.Port = 80 }, false, errcode.InvalidInput},
		{"a subnet that is not a /29", lsAddCall, badAddr("172.31.255.240/28", "172.31.255.241", "172.31.255.242"), false, errcode.InvalidInput},
		{"a subnet outside the panel range", lsAddCall, badAddr("172.31.254.248/29", "172.31.254.249", "172.31.254.250"), false, errcode.InvalidInput},
		{"a subnet that is not masked", lsAddCall, badAddr("172.31.255.249/29", "172.31.255.250", "172.31.255.251"), false, errcode.InvalidInput},
		{"a gateway that is not the first host", lsAddCall, badAddr("172.31.255.248/29", "172.31.255.250", "172.31.255.251"), false, errcode.InvalidInput},
		{"a proxy that is not the second host", lsAddCall, badAddr("172.31.255.248/29", "172.31.255.249", "172.31.255.251"), false, errcode.InvalidInput},
		{"a live exposure", lsAddCall, nil, true, errcode.PanelAlreadyExposed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			if tc.live {
				fdInsertExposure(t, r.db, lsRow(lsOther, StateOpen, ""))
			}
			in := lsOpenInput(ViaNPM)
			if tc.edit != nil {
				tc.edit(&in)
			}
			raw := lsJSON(t, in)
			_, err := def.Handler.Plan(ctx, r.env(tc.call, ""), raw)
			lsWantRefusal(t, "plan", err, tc.want)
			_, err = def.Handler.Apply(ctx, r.env(tc.call, lsChild), raw)
			lsWantRefusal(t, "apply", err, tc.want)
			if len(r.l.Rows) != 0 || len(r.j.list) != 0 {
				t.Errorf("the refusals opened %d listeners and journaled %d effects, want none", len(r.l.Rows), len(r.j.list))
			}
			if row, ok := r.live(); ok != tc.live || (ok && row.ID != lsOther) {
				t.Errorf("live exposure %+v (found %t), want only the one stored before: %t", row, ok, tc.live)
			}
		})
	}
}

// TestLsOpenPlan: the plan shows the listener and the exposure it records, writes nothing, and is not blocked by an
// exposure that is gone.
func TestLsOpenPlan(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	r := lsNewRig(t)
	fdInsertChange(t, r.db, lsRemove, ActionRemove, "completed")
	fdInsertExposure(t, r.db, lsRow(lsOther, StateRevoked, lsRemove))
	plan, err := def.Handler.Plan(ctx, r.env(lsAddCall, ""), lsJSON(t, lsOpenInput(ViaNPM)))
	if err != nil {
		t.Fatalf("plan beside a gone exposure: %v", err)
	}
	diff := lsDiff(plan.Diff)
	for _, want := range []string{lsName, "172.31.255.249:9443", "172.31.255.250", ViaNPM} {
		if !strings.Contains(diff, want) {
			t.Errorf("diff %q does not name %q", diff, want)
		}
	}
	if plan.StateHash == "" {
		t.Error("the plan has no state hash")
	}
	if len(r.l.Rows) != 0 {
		t.Errorf("the plan opened %d listeners", len(r.l.Rows))
	}
}

// TestLsOpenApply: the apply opens the listener with the trusted peer and the client header of the way in, records
// the exposure under the parent's change id with its rule id, journals one made creation, and verifies; it stops
// verifying once the listener is no longer open.
func TestLsOpenApply(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	for _, tc := range []struct{ name, via string }{{"npm", ViaNPM}, {"tunnel", ViaTunnel}} {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			in := lsOpenInput(tc.via)
			raw := lsJSON(t, in)
			env := r.env(lsAddCall, lsChild)
			rawOut, err := def.Handler.Apply(ctx, env, raw)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			wantOut := ListenerOpenOutput{
				ExposureID: lsAdd, ListenerID: "l1", RuleID: RuleID(lsAdd), Address: netip.AddrPortFrom(lsGateway, DefaultPort),
			}
			if got := lsDecode[ListenerOpenOutput](t, rawOut); got != wantOut {
				t.Errorf("output %+v, want %+v", got, wantOut)
			}
			if len(r.l.Rows) != 1 || r.l.Rows["l1"].Spec != lsSpec(tc.via) || r.l.Rows["l1"].State != core.ListenerOpen {
				t.Errorf("listeners %+v, want l1 open with %+v", r.l.Rows, lsSpec(tc.via))
			}
			row, ok := r.live()
			if !ok {
				t.Fatal("no exposure is recorded")
			}
			created, updated := row.CreatedAt, row.UpdatedAt
			row.CreatedAt, row.UpdatedAt = fdNow, fdNow
			want := lsRow(lsAdd, StateOpen, "")
			want.Via, want.Tunnel = tc.via, in.Tunnel
			if row != want {
				t.Errorf("exposure %+v, want %+v", row, want)
			}
			if !created.Equal(fdNow) || !updated.Equal(fdNow) {
				t.Errorf("exposure times %v and %v, want the clock's %v", created, updated, fdNow)
			}
			if len(r.j.list) != 1 {
				t.Fatalf("%d effects journaled, want 1", len(r.j.list))
			}
			eff := r.j.list[0]
			if eff.Kind != core.EffectCreate || eff.State != core.EffectMade || eff.Resource != "panel.exposure:"+lsAdd || !eff.Local || !eff.InverseLocal || eff.Post == nil {
				t.Errorf("effect %+v, want a made local creation of panel.exposure:%s with a post state", eff, lsAdd)
			}
			if err := def.Handler.Verify(ctx, env, raw, rawOut); err != nil {
				t.Errorf("verify: %v", err)
			}
			if err := r.l.Revoke(ctx, "l1"); err != nil {
				t.Fatal(err)
			}
			if err := def.Handler.Verify(ctx, env, raw, rawOut); err == nil {
				t.Error("verify accepted an exposure whose listener is revoked")
			}
		})
	}
}

// TestLsOpenFailures: a listener that cannot open leaves no exposure and a skipped effect; an exposure that
// appears while the listener opens wins, and the listener this apply opened is revoked and closed.
func TestLsOpenFailures(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	raw := lsJSON(t, lsOpenInput(ViaNPM))

	t.Run("the listener cannot open", func(t *testing.T) {
		r := lsNewRig(t)
		failure := errors.New("bind failed")
		r.l.FailOpen = failure
		if _, err := def.Handler.Apply(ctx, r.env(lsAddCall, lsChild), raw); !errors.Is(err, failure) {
			t.Fatalf("apply error %v, want the listener's error", err)
		}
		if _, ok := r.live(); ok {
			t.Error("an exposure is recorded")
		}
		if len(r.j.list) != 1 || r.j.list[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped effect", r.j.list)
		}
	})

	t.Run("another exposure appears while the listener opens", func(t *testing.T) {
		r := lsNewRig(t)
		env := r.env(lsAddCall, lsChild)
		other := lsRow(lsOther, StateOpen, "")
		other.ListenerID = "lx"
		env.Listeners = lsRacy{fdListeners: r.l, onOpen: func() { fdInsertExposure(t, r.db, other) }}
		_, err := def.Handler.Apply(ctx, env, raw)
		lsWantCode(t, "apply", err, errcode.PanelAlreadyExposed)
		if row, ok := r.live(); !ok || row.ID != lsOther || row.ListenerID != "lx" {
			t.Errorf("live exposure %+v (found %t), want the other one unchanged", row, ok)
		}
		if got := r.l.Rows["l1"].State; got != core.ListenerClosed {
			t.Errorf("the listener this apply opened is %q, want closed", got)
		}
		if len(r.j.list) != 1 || r.j.list[0].State != core.EffectSkipped {
			t.Errorf("effects %+v, want one skipped effect", r.j.list)
		}
	})
}

// TestLsOpenObserveAndInverse: the observer finds the exposure by the id in the resource, as a made object or as a
// creation not yet named, and answers the null state for any other id; the inverse revokes and closes the listener
// and deletes the row, touches no other exposure, and may run again.
func TestLsOpenObserveAndInverse(t *testing.T) {
	ctx := context.Background()
	def := listenerOpenAction()
	r := lsNewRig(t)
	env := r.env(lsAddCall, lsChild)
	if _, err := def.Handler.Apply(ctx, env, lsJSON(t, lsOpenInput(ViaNPM))); err != nil {
		t.Fatal(err)
	}
	eff := r.j.list[0]

	obs, err := def.Handler.Observe(ctx, env, eff)
	if err != nil {
		t.Fatal(err)
	}
	if obs.Matches != 1 || !obs.Exclusive || obs.Resource != "panel.exposure:"+lsAdd || !bytes.Equal(obs.Current.State, eff.Post.State) {
		t.Errorf("observation %+v, want one exclusive match of panel.exposure:%s in the journaled post state %s", obs, lsAdd, eff.Post.State)
	}
	creation := eff
	creation.Resource = "panel.exposure:?" + lsAdd
	if obs, err := def.Handler.Observe(ctx, env, creation); err != nil || obs.Matches != 1 || obs.Resource != "panel.exposure:"+lsAdd {
		t.Errorf("observation of the creation %+v, %v; want one match naming panel.exposure:%s", obs, err, lsAdd)
	}
	other := eff
	other.Resource = "panel.exposure:" + lsOther
	if obs, err := def.Handler.Observe(ctx, env, other); err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation of another exposure %+v, %v; want the null state", obs, err)
	}
	foreign := eff
	foreign.Resource = "npm.proxyhost:1"
	if _, err := def.Handler.Observe(ctx, env, foreign); err == nil {
		t.Error("observe accepted a resource that is not an exposure")
	}

	keep := lsNewRig(t)
	keep.expose(StateOpen, "", core.ListenerOpen)
	foreignEffect := core.Effect{Kind: core.EffectCreate, Resource: "panel.exposure:" + lsOther}
	if _, err := def.Handler.Inverse(ctx, keep.env(lsAddCall, lsChild), foreignEffect); err != nil {
		t.Fatalf("inverse of an exposure that is not there: %v", err)
	}
	if row, ok := keep.live(); !ok || row.ID != lsAdd || row.State != StateOpen || keep.l.Rows[row.ListenerID].State != core.ListenerOpen {
		t.Errorf("the inverse of another exposure changed %+v (found %t) or its listener", row, ok)
	}

	for i := range 2 {
		if _, err := def.Handler.Inverse(ctx, env, eff); err != nil {
			t.Fatalf("inverse, run %d: %v", i+1, err)
		}
	}
	if got := r.l.Rows["l1"].State; got != core.ListenerClosed {
		t.Errorf("listener l1 is %q after the inverse, want closed", got)
	}
	if row, ok := r.live(); ok {
		t.Errorf("exposure %+v remains after the inverse", row)
	}
	if obs, err := def.Handler.Observe(ctx, env, eff); err != nil || string(obs.Current.State) != "null" {
		t.Errorf("observation after the inverse %+v, %v; want the null state", obs, err)
	}
}
```

`internal/modules/panel/listener_revoke_test.go`:

```go
package panel

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
)

// lsEmptyInput is the input of the revoke and status actions.
var lsEmptyInput = json.RawMessage(`{}`)

// TestLsRevokePlan: a removal that finds no exposure is refused; an exposure that is not yet revoked, or whose
// listener is still open, shows the change; a revoked exposure behind a listener that is no longer open plans
// nothing, which is how a removal runs after the break-glass revoke.
func TestLsRevokePlan(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()

	none := lsNewRig(t)
	_, err := def.Handler.Plan(ctx, none.env(core.CallContext{}, ""), lsEmptyInput)
	lsWantRefusal(t, "plan without an exposure", err, errcode.PanelNotExposed)

	tests := []struct {
		name     string
		row      string
		listener string
		empty    bool
	}{
		{"an open exposure behind an open listener", StateOpen, core.ListenerOpen, false},
		{"a revoked exposure behind a listener that is still open", StateRevoked, core.ListenerOpen, false},
		{"a revoked exposure behind a revoked listener", StateRevoked, core.ListenerRevoked, true},
		{"a revoked exposure behind a closed listener", StateRevoked, core.ListenerClosed, true},
		{"an exposure in needs_repair behind a closed listener", StateNeedsRepair, core.ListenerClosed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			row := r.expose(tc.row, "", tc.listener)
			plan, err := def.Handler.Plan(ctx, r.env(core.CallContext{}, ""), lsEmptyInput)
			if err != nil {
				t.Fatalf("plan: %v", err)
			}
			if tc.empty != (len(plan.Diff) == 0) {
				t.Errorf("diff %q, want empty: %t", lsDiff(plan.Diff), tc.empty)
			}
			if plan.StateHash == "" {
				t.Error("the plan has no state hash")
			}
			if got := r.l.Rows[row.ListenerID].State; got != tc.listener {
				t.Errorf("the plan changed the listener to %q", got)
			}
		})
	}
}

// TestLsRevokeApply: the apply revokes and closes the listener, then marks the exposure revoked with the removal
// that called it, when there is one. It is the same apply for the break-glass command (no parent) and for the
// removal, runs again to the same end, and answers whether this run revoked an open listener.
func TestLsRevokeApply(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()
	tests := []struct {
		name        string
		row         string
		removal     string // the removal change the stored row names
		listener    string
		call        core.CallContext
		wantRevoked bool
		wantRemoval string
	}{
		{"the removal", StateOpen, "", core.ListenerOpen, lsRmCall, true, lsRemove},
		{"the break-glass command", StateOpen, "", core.ListenerOpen, core.CallContext{}, true, ""},
		{"the break-glass command again", StateRevoked, "", core.ListenerClosed, core.CallContext{}, false, ""},
		{"the removal after the break-glass command", StateRevoked, "", core.ListenerClosed, lsRmCall, false, lsRemove},
		{"a removal that replaces an earlier one", StateRevoked, lsOther, core.ListenerClosed, lsRmCall, false, lsRemove},
		{"the break-glass command after a removal", StateRevoked, lsOther, core.ListenerClosed, core.CallContext{}, false, lsOther},
		{"an exposure that needs repair", StateNeedsRepair, "", core.ListenerClosed, lsRmCall, false, lsRemove},
		{"a revoke that was cut off before the close", StateOpen, "", core.ListenerRevoked, lsRmCall, false, lsRemove},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			row := r.expose(tc.row, tc.removal, tc.listener)
			env := r.env(tc.call, "")
			raw, err := def.Handler.Apply(ctx, env, lsEmptyInput)
			if err != nil {
				t.Fatalf("apply: %v", err)
			}
			want := ListenerRevokeOutput{ExposureID: lsAdd, ListenerID: row.ListenerID, RuleID: RuleID(lsAdd), Revoked: tc.wantRevoked}
			if got := lsDecode[ListenerRevokeOutput](t, raw); got != want {
				t.Errorf("output %+v, want %+v", got, want)
			}
			if got := r.l.Rows[row.ListenerID].State; got != core.ListenerClosed {
				t.Errorf("listener %s is %q, want closed", row.ListenerID, got)
			}
			live, ok := r.live()
			if !ok || live.State != StateRevoked || live.RemovalChangeID != tc.wantRemoval || !live.UpdatedAt.Equal(fdNow) {
				t.Errorf("exposure %+v (found %t), want revoked with removal %q", live, ok, tc.wantRemoval)
			}
			if err := def.Handler.Verify(ctx, env, lsEmptyInput, raw); err != nil {
				t.Errorf("verify: %v", err)
			}
		})
	}

	none := lsNewRig(t)
	_, err := def.Handler.Apply(ctx, none.env(lsRmCall, ""), lsEmptyInput)
	lsWantRefusal(t, "apply without an exposure", err, errcode.PanelNotExposed)
}

// TestLsRevokeVerify: the verification wants the exposure revoked and its listener no longer open.
func TestLsRevokeVerify(t *testing.T) {
	ctx := context.Background()
	def := listenerRevokeAction()
	tests := []struct {
		name     string
		row      string
		listener string
		ok       bool
	}{
		{"revoked and closed", StateRevoked, core.ListenerClosed, true},
		{"revoked behind an open listener", StateRevoked, core.ListenerOpen, false},
		{"still open behind a closed listener", StateOpen, core.ListenerClosed, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			r.expose(tc.row, "", tc.listener)
			err := def.Handler.Verify(ctx, r.env(core.CallContext{}, ""), lsEmptyInput, lsEmptyInput)
			if (err == nil) != tc.ok {
				t.Errorf("verify error %v, want success: %t", err, tc.ok)
			}
		})
	}
	none := lsNewRig(t)
	if err := def.Handler.Verify(ctx, none.env(core.CallContext{}, ""), lsEmptyInput, lsEmptyInput); err == nil {
		t.Error("verify accepted a controller with no exposure")
	}
}
```

`internal/modules/panel/status_test.go`:

```go
package panel

import (
	"context"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
)

// TestLsStatus: the status answers the live exposure with its state, and nothing once no exposure is live, which
// includes an exposure revoked by a removal that completed.
func TestLsStatus(t *testing.T) {
	ctx := context.Background()
	def := statusAction()
	read := func(t *testing.T, r *lsRig) (StatusOutput, string) {
		t.Helper()
		raw, err := def.Handler.Read(ctx, r.env(core.CallContext{}, ""), lsEmptyInput)
		if err != nil {
			t.Fatalf("read: %v", err)
		}
		return lsDecode[StatusOutput](t, raw), string(raw)
	}

	t.Run("no exposure", func(t *testing.T) {
		got, raw := read(t, lsNewRig(t))
		if got.Exposed || got.Exposure != nil || raw != `{"exposed":false}` {
			t.Errorf("status %s, want {\"exposed\":false}", raw)
		}
	})

	t.Run("a gone exposure", func(t *testing.T) {
		r := lsNewRig(t)
		fdInsertChange(t, r.db, lsRemove, ActionRemove, "completed")
		fdInsertExposure(t, r.db, lsRow(lsAdd, StateRevoked, lsRemove))
		if got, raw := read(t, r); got.Exposed || got.Exposure != nil {
			t.Errorf("status %s, want no exposure", raw)
		}
	})

	tests := []struct {
		name     string
		row      string
		removal  string
		change   string // the state of the removal change, "" for none
		listener string
	}{
		{"an open exposure", StateOpen, "", "", core.ListenerOpen},
		{"a revoked exposure with no removal", StateRevoked, "", "", core.ListenerClosed},
		{"a revoked exposure whose removal failed", StateRevoked, lsRemove, "failed", core.ListenerClosed},
		{"an exposure that needs repair", StateNeedsRepair, "", "", core.ListenerClosed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := lsNewRig(t)
			if tc.change != "" {
				fdInsertChange(t, r.db, lsRemove, ActionRemove, tc.change)
			}
			row := r.expose(tc.row, tc.removal, tc.listener)
			got, raw := read(t, r)
			if !got.Exposed || got.Exposure == nil {
				t.Fatalf("status %s, want the live exposure", raw)
			}
			created := got.Exposure.CreatedAt
			got.Exposure.CreatedAt = fdNow
			want := ExposureView{
				ID: lsAdd, Via: ViaNPM, Hostname: lsName, HostID: lsHost, Subnet: lsSubnet, Gateway: lsGateway, Proxy: lsProxy,
				Port: DefaultPort, State: tc.row, ListenerID: row.ListenerID, CreatedAt: fdNow,
			}
			if *got.Exposure != want || !created.Equal(fdNow) {
				t.Errorf("exposure %+v created %v, want %+v created %v", *got.Exposure, created, want, fdNow)
			}
		})
	}

	t.Run("an exposure through a tunnel", func(t *testing.T) {
		r := lsNewRig(t)
		row := lsRow(lsAdd, StateOpen, "")
		row.Via, row.Tunnel, row.Port = ViaTunnel, lsTunnel, 9444
		fdInsertExposure(t, r.db, row)
		got, raw := read(t, r)
		if !got.Exposed || got.Exposure == nil || got.Exposure.Via != ViaTunnel || got.Exposure.Tunnel != lsTunnel || got.Exposure.Port != 9444 {
			t.Errorf("status %s, want the tunnel exposure on port 9444", raw)
		}
	})
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w8-tmp/7 && TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestLs' ./internal/modules/panel/`
Expected: FAIL to build, naming `undefined: listenerOpenAction`, `undefined: listenerRevokeAction` and `undefined: statusAction`. Quote the output verbatim.

- [ ] **Step 3: Implement**

Follow the surrounding code (`internal/modules/cloudflare/account_connect.go` is the model for a controller-side compensable action that writes a table of its own and journals one local creation). A doc comment on every exported identifier, comments elsewhere only for a non-obvious why. Plain errors start with `panel:`. Refusals are `*errcode.Error` marked with `core.Definite` when nothing committed. A conversion of a validated port to `uint16` that gosec reports as G115 carries `//nolint:gosec // G115: the port was validated to 1024-65535`.

1. `status.go`: `statusAction()` is `core.DefineRead[StatusInput, StatusOutput]` with `ActionSpec{Name: ActionStatus, Version: 1, Side: core.SideController, MinScope: core.ScopeAdmin, Summary: "Show whether the panel is exposed and how."}` and a handler type `statusHandler` whose `Read` calls `liveExposure(ctx, env.DB.Read())`. No live exposure answers the zero `StatusOutput` (`{"exposed":false}`, no `exposure` member). A live exposure answers `Exposed: true` and `Exposure` pointing at `row.view()`. A revoked exposure whose removal has not completed, and one in `needs_repair`, are live and are shown with their state.

2. `listener.go`: `listenerOpenAction()` is `core.DefineMutating[ListenerOpenInput, ListenerOpenOutput]` with `ActionSpec{Name: ActionOpen, Version: 1, Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"panel"}, MinScope: core.ScopeAdmin, Summary: "Open the runtime listener that serves the exposed panel."}`. The handler type `listenerOpenHandler` implements `Plan`, `Apply`, `Verify`, `Observe` and `Inverse`; `core.DefineMutating` finds the last two and registers them as the undo handler. Declare `const listenerNoun = "panel.exposure"`; the journaled resource of an exposure is `panel.exposure:<exposure id>`, and `panel.exposure:?<exposure id>` before the creation is named.
   - `listenerCheck(env core.Env, in ListenerOpenInput) error`, shared by `Plan` and `Apply`, refuses with `core.Definite(...)` in this order: (a) `request.invalid_input` unless `env.Call.ParentAction == ActionAdd` and `env.Call.ParentChangeID != ""`; (b) whatever `ValidateExpose(ExposeInput{Via, Hostname, HostID, Tunnel, Port})` answers; (c) `request.invalid_input` at `/port` when `in.Port == 0` (the composite always passes the effective port); (d) at `/subnet` unless `in.Subnet` is valid, has 29 bits, equals `in.Subnet.Masked()` and its address lies in `PanelRange`; (e) at `/gateway` unless `in.Gateway == in.Subnet.Addr().Next()`; (f) at `/proxy` unless `in.Proxy == in.Gateway.Next()`. A small helper `listenerBad(path, message string) error` builds the field errors (an `errcode.Error` with code `errcode.InvalidInput`, a detail saying the listener input is invalid and one `errcode.FieldError{Path: path, Message: message}`).
   - `Plan`: `listenerCheck`; then `liveExposure(ctx, env.DB.Read())`, and a live exposure of any state is `core.Definite(errcode.New(errcode.PanelAlreadyExposed, detail))` with a detail that names its hostname. The outcome has two diff lines, `+ runtime listener <gateway>:<port> for <hostname> via <via>` and `+ allowed peer <proxy>`, and a non-empty `StateHash` (the hex SHA-256 of the text `no live exposure`).
   - `Apply`, in this order: `listenerCheck`; the live-exposure refusal again, before anything is journaled or opened; `id := env.Call.ParentChangeID`, `now := env.Clock.Now().UTC()`; the `exposureRow` to store (`State: StateOpen`, `RuleID: RuleID(id)`, `CreatedAt` and `UpdatedAt` both `now`, `ListenerID` set after the open); the state document of the exposure, the JSON of one unexported struct `listenerDoc` with the members `id`, `via`, `hostname`, `hostId`, `tunnel`, `subnet`, `gateway`, `proxy`, `port` (never the listener id or the row state, which change later), built by one function `listenerState(row exposureRow)` that `Observe` also uses so that both produce identical bytes; `env.Effects.Intend` of `core.Effect{Kind: core.EffectCreate, Resource: "panel.exposure:?"+id, Recovery: &core.RecoveryID{Kind: "name", Value: id}, Local: true, InverseLocal: true, Before: {State: null}, After: &state}` (the same shape `accountConnectHandler.Apply` journals); `env.Listeners.Open` with a `core.ListenerSpec` whose `Addr` is `netip.AddrPortFrom(in.Gateway, uint16(in.Port))`, `AllowedPeer` is `in.Proxy`, `ClientHeader` is `core.ClientHeaderNPM` for `via: npm` and `core.ClientHeaderTunnel` for `via: tunnel`, `PublicHost` is `in.Hostname` and `ChangeID` is `id`. When `Open` fails, call `env.Effects.Skipped` and return the `Open` error joined with any journal error (`errors.Is` must still find the `Open` error). Otherwise write the row in one `env.DB.Write` transaction that first calls `liveExposure(ctx, tx)` again and returns `errcode.New(errcode.PanelAlreadyExposed, detail)` when a live exposure has appeared meanwhile, and then `insertExposure`. When that transaction fails, revoke and close the listener this apply opened (`context.WithoutCancel(ctx)` for those two calls), call `Skipped`, and return the transaction's error joined with any cleanup error. On success call `env.Effects.Made(ctx, seq, "panel.exposure:"+id, state)` and return `ListenerOpenOutput{ExposureID: id, ListenerID: <the opened id>, RuleID: RuleID(id), Address: netip.AddrPortFrom(in.Gateway, uint16(in.Port))}`.
   - `Verify` (it reads `env.Call.ParentChangeID`, never the output, because recovery passes a zero output): the live exposure has `ID == env.Call.ParentChangeID` and `State == StateOpen`, and `env.Listeners.Get(ctx, row.ListenerID)` answers a listener in state `core.ListenerOpen`; otherwise a plain `panel:` error.
   - `Observe`: parse the exposure id from `e.Resource` (prefix `panel.exposure:`, an optional `?` after it, a non-empty id; any other resource is a plain error). The live exposure with that id answers `core.Observation{Current: listenerState(row), Resource: "panel.exposure:"+id, Matches: 1, Exclusive: true}`. No live exposure, or one with another id, answers the null state, `core.StateDoc{State: json.RawMessage("null")}`, `Exclusive: true`, `Matches: 0`.
   - `Inverse`: parse the id the same way. When the live exposure has that id: `env.Listeners.Revoke` then `env.Listeners.Close` on its `ListenerID` (a revoked or closed listener is fine), then `deleteExposure` in an `env.DB.Write`. When no live exposure has that id it changes nothing and succeeds (the undo may run again). It never touches another exposure.

3. `listener_revoke.go`: `listenerRevokeAction()` is `core.DefineMutating[ListenerRevokeInput, ListenerRevokeOutput]` with `ActionSpec{Name: ActionRevoke, Version: 1, Class: core.ClassCompensable, Side: core.SideController, LockKeys: []string{"panel"}, ForwardOnly: true, Converges: true, BreakGlass: true, MinScope: core.ScopeAdmin, Summary: "Revoke the exposed panel's runtime listener, then close it."}`. The handler type `listenerRevokeHandler` implements `Plan`, `Apply` and `Verify` only: no `Observe` and no `Inverse` (the registry allows exactly one of undo handler, `CompensatedBy` and `ForwardOnly`), and it journals no effect (`env.Effects` is nil in its tests); a run that is cut off is repeated, which `Converges` allows. A helper `listenerLive(ctx, env) (row exposureRow, open bool, err error)` returns the live exposure and whether `env.Listeners.Get(ctx, row.ListenerID)` reports it open; no live exposure is `core.Definite(errcode.New(errcode.PanelNotExposed, detail))`.
   - `Plan`: the refusal above; then an **empty** `Diff` (nil) when `row.State == StateRevoked` and the listener is not open, else lines that show what changes (`- runtime listener <id>: open` and `+ runtime listener <id>: revoked, then closed` when it is open; `- exposure <hostname>: <state>` and `+ exposure <hostname>: revoked` when the row is not yet revoked). `StateHash` is the hex SHA-256 of `<exposure id>|<row state>|<listener id>`.
   - `Apply`: `listenerLive`; `env.Listeners.Revoke` then `env.Listeners.Close` on the listener, always, so that a listener revoked but not closed by an interrupted run is closed now; then one `env.DB.Write` that calls `setExposureState(ctx, tx, row.ID, StateRevoked, env.Call.ParentChangeID, env.Clock.Now())` (an empty `ParentChangeID`, the break-glass command, leaves `removal_change_id` as it is; a removal records itself even when the row was already revoked). The output is `ListenerRevokeOutput{ExposureID, ListenerID, RuleID: row.RuleID, Revoked: open}` where `open` is whether the listener was open when this run began, so a repeated run answers `Revoked: false` and still carries the ids.
   - `Verify`: `listenerLive` succeeds, the row is `StateRevoked` and the listener is not open; otherwise a plain `panel:` error. It ignores the output.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestLs' -v ./internal/modules/panel/`
Expected: PASS for every `TestLs` test; quote the output verbatim.

Then the checks every Go task ends with, each with its real output recorded:
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 600 go build ./...`
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 300 go vet ./...` (no output)
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 900 go test -race -count=1 ./internal/modules/panel/`
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 1500 go test -count=1 ./...` (every line `ok` or `?`)
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`)
- `TMPDIR=/root/.cache/w8-tmp/7 GOTOOLCHAIN=local timeout 300 go mod tidy -diff` (no output)

Finish with `rm -rf /root/.cache/w8-tmp/7 && GOTOOLCHAIN=local go clean -testcache`. The tests open no listener socket, run no Docker, systemd or firewall command and read nothing under `/var/lib/darkpanel`, `/etc/darkpanel` or `/run/darkpanel*`: listeners are the in-memory `fdListeners`, the database is in `t.TempDir()`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/panel/listener.go internal/modules/panel/listener_revoke.go internal/modules/panel/status.go internal/modules/panel/listener_test.go internal/modules/panel/listener_revoke_test.go internal/modules/panel/status_test.go
git commit -m "feat(panel): add listener open, revoke and status"
```

### Task 8: backup: exposures in restore marking and verification

**Files:**
- Modify: `internal/controller/backup/mark.go` (`Mark`)
- Modify: `internal/controller/backup/validate.go` (`VerifyInstalled`)
- Test: `internal/controller/backup/exposure_test.go`

**Interfaces:**
- Consumes (Contracts): "Panel names" (`StateOpen`, `StateRevoked`, `StateNeedsRepair`); "Exposure store" (the `panel_exposures` columns and the exported `GoneSQL`, a predicate over a row aliased `e`; W8-R3 and W8-R16 for the meaning of live and gone). The test fixtures `fxDB`, `fxInsertChange`, `fxChange`, `fxNow`, `cbTimeLayout` (`fixture_test.go`), `mkExec`, `mkDump`, `mkMark`, `mkStamp` (`mark_test.go`) and `vfInstalled`, `vfExec`, `vfTx` (`validate_test.go`) already exist in this package.
- Produces: no new name. `Mark` and `VerifyInstalled` keep their signatures; `backup` imports `internal/modules/panel` (it already imports `internal/modules`, which imports `panel`, so no import cycle arises: `panel` imports nothing from `internal/controller/backup`).
- No exported accessor in package `panel` is needed and none is requested: at this task's base the panel manifest lists no `controllerMigrations`, so the test helper `pxMigrate` reads the SQL files of `internal/modules/panel/migrations/controller` from the source tree (`os.DirFS("../../modules/panel/migrations/controller")`, relative to the package directory the test runs in) and applies them when `panel_exposures` is absent. Once Task 11 lists the migrations, `fxDB` creates the table itself, `pxMigrate` finds it and does nothing, and the tests stay valid unchanged.

**Items:** 391

**Depends on:** Task 4
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 1 - risk 2 = 6

- [ ] **Step 1: Write the failing tests**

Create `internal/controller/backup/exposure_test.go` with exactly this content. Each `Mark` case builds its own database (one live exposure at most, plus gone ones, as W8-R3 allows). The `VerifyInstalled` cases use `vfInstalled`'s hook, which runs after `Mark` on the open database, to store the exposures marking would have changed.

```go
package backup

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core/store"
)

// pxMigrations is the panel module's controller migration directory, read from the source tree because the module's
// manifest may not list it yet.
const pxMigrations = "../../modules/panel/migrations/controller"

// pxExposure is one panel_exposures row; Change, when set, is the state of the removal change the row names.
type pxExposure struct{ ID, State, Removal, Change string }

// pxMigrate creates panel_exposures from the panel module's migration files unless the database has it already.
func pxMigrate(t *testing.T, db *store.DB) {
	t.Helper()
	ctx := context.Background()
	var n int
	err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'panel_exposures'`).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	if n > 0 {
		return
	}
	dir := os.DirFS(pxMigrations)
	files, err := fs.Glob(dir, "*.sql")
	if err != nil || len(files) == 0 {
		t.Fatalf("panel migrations in %s: %v, %v", pxMigrations, files, err)
	}
	slices.Sort(files)
	for _, name := range files {
		script, err := fs.ReadFile(dir, name)
		if err != nil {
			t.Fatal(err)
		}
		err = db.Write(ctx, func(tx *sql.Tx) error {
			_, err := tx.ExecContext(ctx, string(script))
			return err
		})
		if err != nil {
			t.Fatalf("apply %s: %v", name, err)
		}
	}
}

// pxInsert stores the exposures, with the removal changes they name, in a database that has panel_exposures.
func pxInsert(t *testing.T, db *store.DB, rows ...pxExposure) {
	t.Helper()
	now := fxNow.Format(cbTimeLayout)
	for _, r := range rows {
		if r.Change != "" {
			fxInsertChange(t, db, fxChange{ID: r.Removal, HostID: "host-a", Action: "panel.exposure.remove", Class: "composite", State: r.Change})
		}
		mkExec(t, db, `INSERT INTO panel_exposures (id, via, hostname, host_id, tunnel, subnet, gateway, proxy, port, listener_id,
			rule_id, state, removal_change_id, created_at, updated_at)
			VALUES (?, 'npm', 'panel.example.com', 'host-a', '', '172.31.255.248/29', '172.31.255.249', '172.31.255.250', 9443, 'l1',
			'0RULE', ?, ?, ?, ?)`, r.ID, r.State, r.Removal, now, now)
	}
}

// pxRows maps each exposure id to its state and updated_at.
func pxRows(t *testing.T, db *store.DB) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, row := range mkDump(t, db, `SELECT id, state, updated_at FROM panel_exposures ORDER BY id`) {
		id, rest, _ := strings.Cut(row, "|")
		out[id] = rest
	}
	return out
}

// TestPxMarkSetsEveryLiveExposureToNeedsRepair: an exposure that is open, revoked without a completed removal or
// already marked becomes (or stays) needs_repair; one revoked by a removal that completed is gone and is not touched.
func TestPxMarkSetsEveryLiveExposureToNeedsRepair(t *testing.T) {
	fixture := fxNow.Format(cbTimeLayout)
	tests := []struct {
		name string
		rows []pxExposure
		want map[string]string // id -> "<state>|<updated_at>"
	}{
		{"an open exposure", []pxExposure{{ID: "e1", State: "open"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure with no removal", []pxExposure{{ID: "e1", State: "revoked"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal is unfinished", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "needs_repair"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal failed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "failed"}},
			map[string]string{"e1": "needs_repair|" + mkStamp}},
		{"a revoked exposure whose removal completed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}},
			map[string]string{"e1": "revoked|" + fixture}},
		{"an exposure already marked", []pxExposure{{ID: "e1", State: "needs_repair"}},
			map[string]string{"e1": "needs_repair|" + fixture}},
		{"a gone exposure beside an open one", []pxExposure{
			{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}, {ID: "e2", State: "open"}},
			map[string]string{"e1": "revoked|" + fixture, "e2": "needs_repair|" + mkStamp}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := pxDB(t, "")
			pxInsert(t, db, tc.rows...)
			mkMark(t, db)
			got := pxRows(t, db)
			for id, want := range tc.want {
				if got[id] != want {
					t.Errorf("exposure %s after Mark: %q, want %q", id, got[id], want)
				}
			}
		})
	}
}

// TestPxMarkWithoutThePanelTable: a controller database that has no panel_exposures table is marked as before.
func TestPxMarkWithoutThePanelTable(t *testing.T) {
	db := fxDB(t, "")
	mkExec(t, db, `DROP TABLE IF EXISTS panel_exposures`)
	fxInsertChange(t, db, fxChange{ID: "c1", HostID: "host-a", Action: "fixture.act", State: "applying", Owner: "engine"})
	mkMark(t, db)
	if got := mkDump(t, db, `SELECT state FROM core_changes WHERE id = 'c1'`); !slices.Equal(got, []string{"quarantined"}) {
		t.Errorf("change c1 is %v after Mark, want quarantined", got)
	}
}

// pxDB opens a controller database that has panel_exposures.
func pxDB(t *testing.T, dir string) *store.DB {
	t.Helper()
	db := fxDB(t, dir)
	pxMigrate(t, db)
	return db
}

// pxInstalled builds the installed database of vfInstalled with the exposures stored after Mark, as a restore
// leaves them when marking missed one.
func pxInstalled(t *testing.T, rows ...pxExposure) string {
	t.Helper()
	return vfInstalled(t, vfTx, func(t *testing.T, db *store.DB) {
		pxMigrate(t, db)
		pxInsert(t, db, rows...)
	})
}

// TestPxVerifyInstalledAcceptsMarkedExposures: exposures in needs_repair and exposures that are gone pass.
func TestPxVerifyInstalledAcceptsMarkedExposures(t *testing.T) {
	tests := []struct {
		name string
		rows []pxExposure
	}{
		{"no exposure", nil},
		{"an exposure in needs_repair", []pxExposure{{ID: "e1", State: "needs_repair"}}},
		{"a gone exposure", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}}},
		{"a gone exposure beside one in needs_repair", []pxExposure{
			{ID: "e1", State: "revoked", Removal: "rm1", Change: "completed"}, {ID: "e2", State: "needs_repair"}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := pxInstalled(t, tc.rows...)
			if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
				t.Errorf("VerifyInstalled: %v", err)
			}
		})
	}

	t.Run("a database without the panel table", func(t *testing.T) {
		path := vfInstalled(t, vfTx, func(t *testing.T, db *store.DB) { vfExec(t, db, `DROP TABLE IF EXISTS panel_exposures`) })
		if err := VerifyInstalled(context.Background(), path, vfTx); err != nil {
			t.Errorf("VerifyInstalled: %v", err)
		}
	})
}

// TestPxVerifyInstalledRefusesAnExposureInAnotherState: a live exposure that is not in needs_repair means marking
// did not reach it, so the installed database is not this restore's quarantine, and the error names the exposure.
func TestPxVerifyInstalledRefusesAnExposureInAnotherState(t *testing.T) {
	tests := []struct {
		name string
		rows []pxExposure
		id   string
		want string // the state the error names
	}{
		{"an open exposure", []pxExposure{{ID: "e1", State: "open"}}, "e1", "open"},
		{"a revoked exposure with no removal", []pxExposure{{ID: "e1", State: "revoked"}}, "e1", "revoked"},
		{"a revoked exposure whose removal failed", []pxExposure{{ID: "e1", State: "revoked", Removal: "rm1", Change: "failed"}}, "e1", "revoked"},
		{"an open exposure beside one in needs_repair", []pxExposure{{ID: "e1", State: "needs_repair"}, {ID: "e2", State: "open"}}, "e2", "open"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			path := pxInstalled(t, tc.rows...)
			err := VerifyInstalled(context.Background(), path, vfTx)
			if !errors.Is(err, ErrNotQuarantined) {
				t.Fatalf("VerifyInstalled = %v, want ErrNotQuarantined", err)
			}
			if !strings.Contains(err.Error(), tc.id) || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q does not name exposure %s in state %s", err, tc.id, tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w8-tmp/8 && TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestPx' ./internal/controller/backup/`
Expected: FAIL with assertions, not a build error: `exposure e1 after Mark: "open|2026-11-04T12:00:00.000000000Z", want "needs_repair|2026-11-04T15:30:15.123456789Z"` (and the same for the revoked cases) and `VerifyInstalled = <nil>, want ErrNotQuarantined`. `TestPxMarkWithoutThePanelTable` and the accepting `VerifyInstalled` cases already pass; they guard the behaviour that must not change. Quote the output verbatim.

- [ ] **Step 3: Implement**

1. `mark.go`, `Mark`: import `github.com/darkraise/darkpanel/internal/modules/panel`. In the one transaction, after the `core_listeners` update and before the `core_locks` delete, look the table up the way the `core_task_runs` lookup does (`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'panel_exposures'`) and only when it exists run
   `UPDATE panel_exposures AS e SET state = ?, updated_at = ? WHERE e.state IN (?, ?) AND NOT (` + `panel.GoneSQL` + `)`
   with the bound arguments `panel.StateNeedsRepair`, `at` (the same `markStampLayout` string the other updates write), `panel.StateOpen`, `panel.StateRevoked`. A row already in `needs_repair` and a gone row keep their `updated_at`. The query concatenates the constant `GoneSQL` and carries `//nolint:gosec // G202: GoneSQL is a constant`, as the existing placeholder lists do. Errors read `backup: mark panel_exposures: %w` and `backup: mark look up panel_exposures: %w`. The table check stays because a database that predates the panel migration has no table; update the doc comment of `Mark` to say that it also sets every live exposure to `needs_repair`.

2. `validate.go`, `VerifyInstalled`: import the same package. After the `core_locks` check, look the table up the same way (read-only connection, `QueryRowContext` on `db`) and return `nil` when it is absent. Otherwise run `SELECT e.id, e.state FROM panel_exposures AS e WHERE e.state <> ? AND NOT (` + `panel.GoneSQL` + `) ORDER BY e.id LIMIT 1` with `panel.StateNeedsRepair` bound (same `//nolint:gosec` reason). A row found is `fmt.Errorf("%w: exposure %s is %s, not %s", ErrNotQuarantined, id, state, panel.StateNeedsRepair)`; any other error is `backup: installed database: read panel_exposures: %w`, and a failed table lookup `backup: installed database: look up panel_exposures: %w`. Reuse the `id` and `state` variables the function already declares. Extend the doc comment of `VerifyInstalled` with "and every live panel exposure is in state needs_repair". The function must still open the database read-only and write nothing.

3. Change nothing else in the package; the existing `Mk` and `Vf` tests pass unchanged.

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestPx' -v ./internal/controller/backup/`
Expected: PASS; quote the output verbatim.

Then the checks every Go task ends with, each with its real output recorded:
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 600 go build ./...`
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 300 go vet ./...` (no output)
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 900 go test -count=1 ./internal/controller/backup/` (the whole package takes about four minutes because of the key derivation in its other tests; it ends `ok`)
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 1800 go test -race -count=1 -run 'TestPx|TestMk|TestVf' ./internal/controller/backup/` (the families that exercise `Mark` and `VerifyInstalled`; the rest of the package is untouched and its race run would take far longer than the task's time budget, which the report says)
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 1500 go test -count=1 ./...` (every line `ok` or `?`)
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`)
- `TMPDIR=/root/.cache/w8-tmp/8 GOTOOLCHAIN=local timeout 300 go mod tidy -diff` (no output)

Finish with `rm -rf /root/.cache/w8-tmp/8 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/backup/mark.go internal/controller/backup/validate.go internal/controller/backup/exposure_test.go
git commit -m "feat(backup): mark and verify panel exposures"
```

### Task 9: panel: exposure.add composite

**Files:**
- Create: `internal/modules/panel/expose_add.go` (`panel.exposure.add`)
- Test: `internal/modules/panel/expose_add_test.go`

**Interfaces:**
- Consumes (Contracts): "Panel names" (`ActionAdd`, `ActionRemove`, `ActionNetCreate`, `ActionConnCheck`, `ActionOpen`, `NetworkName`, `BridgeName`, `Claimant`, `DefaultPort`, `ViaNPM`, `ViaTunnel`, the states); "Wire types" (`ExposeInput`, `ExposeOutput`, `NetworkView`, `NetworkCreateInput`, `ConnectorCheckInput`, `ListenerOpenInput`, `ListenerOpenOutput`, `ValidateExpose`); "Exposure store" (`liveExposure`, `exposureRow`, `RuleID`); "Panel controller fixture" (`fdDB`, `fdNewListeners`, `fdEnv`, `fdInsertHost`, `fdInsertUser`, `fdInsertChange`, `fdInsertExposure`, `fdNow`); "Composite steps" (the add table) and W8-R5, W8-R6, W8-R7, W8-R10; "Action constructors" for `exposureAddAction`. The input and output types of the children it names come from the modules that own them: `npm` (`PublicInput`, `AttachInput`, `CertificateInput`, `CertificateView`, `ProxyHostInput`), `cloudflare` (`NetworkAttachInput`, `HostnameAddInput`) and `firewall` (`RulesApplyInput`, with `model.Rule`). The tests import those packages; the implementation does not, it names the child actions as strings in unexported constants whose names start with `add`.
- Produces (Contracts "Action constructors"): `exposureAddAction() core.ActionDef`, listed in the manifest by Task 11.

**Items:** 36

**Depends on:** Task 4
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing tests**

Create `internal/modules/panel/expose_add_test.go` with exactly this content. It asks the composite for its steps the way the engine does (through `def.Handler.Steps` with a controller env over the fixture database), resolves each step's template the way the engine does against the composite input and the outputs of earlier steps, decodes the result strictly into the wire type of the child action (an unknown or misspelled member fails), validates it against the real input schema of every child that lives in the `npm`, `cloudflare` or `firewall` module, and applies the registry-time rules of the composite engine that a unit test can reach (`eaCheckGraph`). The tests only read those modules' manifests: they call no handler and reach no Docker daemon.

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/firewall/model"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	eaHost   = "h1"
	eaName   = "panel.example.com"
	eaTunnel = "8e6f7a8b-9c0d-4e1f-a2b3-c4d5e6f70819"
	eaChange = "01JEXPOSURE0000000000000A"
)

var (
	eaSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	eaGateway = netip.MustParseAddr("172.31.255.249")
	eaProxy   = netip.MustParseAddr("172.31.255.250")
)

// eaEnv is a controller env over a database that holds the controller's host h1, another host h2 and an
// administrator who has enrolled TOTP, so that no refusal applies until a test adds its cause.
func eaEnv(t *testing.T) (core.Env, *store.DB) {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, eaHost, true)
	fdInsertHost(t, db, "h2", false)
	fdInsertUser(t, db, "admin", "active")
	return fdEnv(t, db, fdNewListeners(), core.CallContext{}), db
}

func eaNPMInput(port int) ExposeInput {
	return ExposeInput{Via: ViaNPM, Hostname: eaName, HostID: eaHost, Port: port}
}

func eaTunnelInput(port int) ExposeInput {
	return ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Port: port}
}

func eaJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// eaSteps asks the composite for its steps the way the engine does, through the erased handler.
func eaSteps(t *testing.T, env core.Env, in ExposeInput) ([]core.Step, error) {
	t.Helper()
	return exposureAddAction().Handler.Steps(context.Background(), env, eaJSON(t, in))
}

// eaOK returns the steps of in, which must be accepted.
func eaOK(t *testing.T, in ExposeInput) []core.Step {
	t.Helper()
	env, _ := eaEnv(t)
	steps, err := eaSteps(t, env, in)
	if err != nil {
		t.Fatalf("steps: %v", err)
	}
	return steps
}

// eaGeneric is v as decoded JSON.
func eaGeneric(t *testing.T, v any) any {
	t.Helper()
	var out any
	if err := json.Unmarshal(eaJSON(t, v), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

var eaRef = regexp.MustCompile(`\$\{([^}]*)\}`)

// eaResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text. A reference to a member that does not exist
// fails the test, as it fails the plan.
func eaResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": eaGeneric(t, v)}
	}
	root := map[string]any{"input": eaGeneric(t, in), "steps": steps}
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
			if m := eaRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return eaRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
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
	out, err := json.Marshal(walk(tv))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// eaStrict decodes raw into a T and refuses a member T does not have.
func eaStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// eaOutputs are the outputs the earlier steps of an exposure on port answer.
func eaOutputs(port uint16) map[string]any {
	return map[string]any{
		"network": NetworkView{Network: NetworkName, ID: "n1", Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy},
		"listen": ListenerOpenOutput{
			ExposureID: eaChange, ListenerID: "l1", RuleID: RuleID(eaChange), Address: netip.AddrPortFrom(eaGateway, port),
		},
		"cert": npm.CertificateView{ID: 42},
	}
}

// eaDef returns the declaration of action name of the npm, cloudflare or firewall module.
func eaDef(t *testing.T, name string) (core.ActionDef, bool) {
	t.Helper()
	for _, m := range []core.Module{npm.Module(), cloudflare.Module(), firewall.Module()} {
		for _, a := range m.Manifest().Actions {
			if a.Spec.Name == name && a.Spec.Version == 1 {
				return a, true
			}
		}
	}
	return core.ActionDef{}, false
}

// eaWantStep fails the test unless s is the step id on host running action after the steps after.
func eaWantStep(t *testing.T, s core.Step, id, host, action string, after []string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) {
		t.Errorf("step %+v, want %s: %s on host %q after %v", s, id, action, host, after)
	}
}

func eaIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

var eaStepRef = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.`)

// eaCheckGraph applies the registry-time rules of the composite engine that a test can reach: unique ids, steps that
// follow earlier steps only, references only to steps the referencing step follows, host exactly for agent actions,
// a revertible step only last, and an undo handler for every compensable step of the module's neighbours. The
// resolved input of a step of another module must be valid for that action.
func eaCheckGraph(t *testing.T, steps []core.Step, in ExposeInput, port uint16) {
	t.Helper()
	pos := map[string]int{}
	follows := map[string]map[string]bool{}
	for i, s := range steps {
		if _, dup := pos[s.ID]; dup {
			t.Errorf("step id %s is used twice", s.ID)
		}
		set := map[string]bool{}
		for _, a := range s.After {
			if _, ok := pos[a]; !ok {
				t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
			}
			set[a] = true
			for f := range follows[a] {
				set[f] = true
			}
		}
		pos[s.ID], follows[s.ID] = i, set
		for _, m := range eaStepRef.FindAllStringSubmatch(string(s.Input), -1) {
			if !set[m[1]] {
				t.Errorf("step %s references step %s, which it does not follow", s.ID, m[1])
			}
		}
		def, ok := eaDef(t, s.Action.Name)
		if !ok {
			continue
		}
		if (def.Spec.Side == core.SideController) != (s.Host == "") {
			t.Errorf("step %s: %s is a %s-side action and the step's host is %q", s.ID, s.Action, def.Spec.Side, s.Host)
		}
		if def.Spec.Class == core.ClassRevertible && i != len(steps)-1 {
			t.Errorf("step %s: %s is revertible and must be the last step", s.ID, s.Action)
		}
		if def.Spec.Class == core.ClassCompensable && def.Handler.Inverse == nil {
			t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
		}
		resolved := eaResolve(t, s.Input, in, eaOutputs(port))
		if err := schema.NewValidator().ValidateInput(def, resolved); err != nil {
			t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, resolved, s.Action, err)
		}
	}
}

// eaPorts checks the final firewall step: one allow rule from the proxy to the gateway on the bridge, with the
// rule id the listener step answers, labelled with the hostname.
func eaPorts(t *testing.T, s core.Step, in ExposeInput, port uint16, after string) {
	t.Helper()
	eaWantStep(t, s, "ports", eaHost, "firewall.rules.apply/v1", []string{after})
	got := eaStrict[firewall.RulesApplyInput](t, eaResolve(t, s.Input, in, eaOutputs(port)))
	want := []model.Rule{{
		ID: RuleID(eaChange), Action: model.ActionAllow, Direction: model.DirIn, Proto: model.ProtoTCP,
		From: eaProxy.String(), To: eaGateway.String(), ToPort: strconv.Itoa(int(port)), InIface: BridgeName, Family: model.FamilyV4,
	}}
	if got.Label != "panel exposure "+eaName || !reflect.DeepEqual(got.Add, want) || len(got.Delete) != 0 || got.Guard != nil || len(got.Overrides) != 0 {
		t.Errorf("ports input %+v, want label %q and the rules %+v", got, "panel exposure "+eaName, want)
	}
	if !strings.Contains(string(s.Input), "${steps.listen.output.ruleId}") {
		t.Errorf("ports template %s does not take the rule id from the listen step", s.Input)
	}
}

// TestEaDeclaration: a controller-side composite that takes the module's coordination lock, unwinds in reverse and
// needs the admin scope, over an input the schema accepts.
func TestEaDeclaration(t *testing.T) {
	def := exposureAddAction()
	s := def.Spec
	if s.Name != ActionAdd || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
		s.Unwind != core.UnwindReverse || s.MinScope != core.ScopeAdmin || !slices.Equal(s.LockKeys, []string{"composite:panel"}) {
		t.Errorf("spec %+v, want version 1, a summary, a controller-side composite, unwind reverse, admin scope, lock composite:panel", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
	for _, in := range []ExposeInput{eaNPMInput(0), eaTunnelInput(0), eaNPMInput(9444)} {
		if err := schema.NewValidator().ValidateInput(def, eaJSON(t, in)); err != nil {
			t.Errorf("a valid input %+v is refused by the schema: %v", in, err)
		}
	}
}

// TestEaRefusals: Steps refuses in the order input, host, TOTP, existing exposure; each row also holds the causes
// that come after its own, to pin the order. An exposure that is gone refuses nothing.
func TestEaRefusals(t *testing.T) {
	pending := func(t *testing.T, db *store.DB) { fdInsertUser(t, db, "newcomer", "pending") }
	exposure := func(state, removal, change string) func(t *testing.T, db *store.DB) {
		return func(t *testing.T, db *store.DB) {
			if change != "" {
				fdInsertChange(t, db, removal, ActionRemove, change)
			}
			fdInsertExposure(t, db, eaExposure("01JLIVE000000000000000001", state, removal))
		}
	}
	with := func(fs ...func(t *testing.T, db *store.DB)) func(t *testing.T, db *store.DB) {
		return func(t *testing.T, db *store.DB) {
			for _, f := range fs {
				f(t, db)
			}
		}
	}
	edit := func(f func(in *ExposeInput)) ExposeInput {
		in := eaNPMInput(0)
		f(&in)
		return in
	}
	tests := []struct {
		name  string
		setup func(t *testing.T, db *store.DB)
		in    ExposeInput
		code  errcode.Code
		path  string // the field a request.invalid_input names
	}{
		{"an unknown via", with(pending, exposure(StateOpen, "", "")), edit(func(in *ExposeInput) { in.Via, in.HostID = "carrier", "h2" }), errcode.InvalidInput, "/via"},
		{"no hostname", nil, edit(func(in *ExposeInput) { in.Hostname = "" }), errcode.InvalidInput, "/hostname"},
		{"a hostname in capitals", nil, edit(func(in *ExposeInput) { in.Hostname = "Panel.Example.com" }), errcode.InvalidInput, "/hostname"},
		{"a hostname that is a reference", nil, edit(func(in *ExposeInput) { in.Hostname = "${steps.network.output.proxy}.example.com" }), errcode.InvalidInput, "/hostname"},
		{"a tunnel for npm", nil, edit(func(in *ExposeInput) { in.Tunnel = eaTunnel }), errcode.InvalidInput, "/tunnel"},
		{"a tunnel exposure without a tunnel", nil, ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost}, errcode.InvalidInput, "/tunnel"},
		{"a tunnel that is not a UUID", nil, ExposeInput{Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: "my-tunnel"}, errcode.InvalidInput, "/tunnel"},
		{"a port below 1024", nil, edit(func(in *ExposeInput) { in.Port = 80 }), errcode.InvalidInput, "/port"},
		{"a port above 65535", nil, edit(func(in *ExposeInput) { in.Port = 65536 }), errcode.InvalidInput, "/port"},
		{"another host", with(pending, exposure(StateOpen, "", "")), edit(func(in *ExposeInput) { in.HostID = "h2" }), errcode.PanelWrongHost, ""},
		{"an unknown host", nil, edit(func(in *ExposeInput) { in.HostID = "h9" }), errcode.PanelWrongHost, ""},
		{"a user still to enrol TOTP", pending, eaNPMInput(0), errcode.PanelTOTPIncomplete, ""},
		{"a user still to enrol TOTP and an exposure", with(pending, exposure(StateOpen, "", "")), eaNPMInput(0), errcode.PanelTOTPIncomplete, ""},
		{"an open exposure", exposure(StateOpen, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"a revoked exposure with no removal", exposure(StateRevoked, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"a revoked exposure whose removal failed", exposure(StateRevoked, "01JREMOVE00000000000000001", "failed"), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
		{"an exposure that needs repair", exposure(StateNeedsRepair, "", ""), eaNPMInput(0), errcode.PanelAlreadyExposed, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			env, db := eaEnv(t)
			if tc.setup != nil {
				tc.setup(t, db)
			}
			steps, err := eaSteps(t, env, tc.in)
			e := errcode.From(err)
			if err == nil || e.Code != tc.code {
				t.Fatalf("steps %v, error %v; want code %s", eaIDs(steps), err, tc.code)
			}
			if tc.path != "" && !slices.ContainsFunc(e.Fields, func(f errcode.FieldError) bool { return f.Path == tc.path }) {
				t.Errorf("fields %+v, want one at %s", e.Fields, tc.path)
			}
		})
	}

	t.Run("a gone exposure", func(t *testing.T) {
		env, db := eaEnv(t)
		fdInsertChange(t, db, "01JREMOVE00000000000000001", ActionRemove, "completed")
		fdInsertExposure(t, db, eaExposure("01JGONE0000000000000000001", StateRevoked, "01JREMOVE00000000000000001"))
		if _, err := eaSteps(t, env, eaNPMInput(0)); err != nil {
			t.Errorf("steps beside a gone exposure: %v", err)
		}
	})
}

// eaExposure is an exposure through NPM with the given id, state and removal change.
func eaExposure(id, state, removal string) exposureRow {
	return exposureRow{
		ID: id, Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: state, RemovalChangeID: removal,
		CreatedAt: fdNow, UpdatedAt: fdNow,
	}
}

// TestEaNPMSteps: the public instance is checked first, the bridge network is created and the proxy attached at its
// second address, the listener opens, the certificate and the proxy host follow, and the firewall rule is last.
func TestEaNPMSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		port int
		want uint16
	}{{"the default port", 0, DefaultPort}, {"an explicit port", 9444, 9444}} {
		t.Run(tc.name, func(t *testing.T) {
			in := eaNPMInput(tc.port)
			steps := eaOK(t, in)
			if want := []string{"public", "network", "attach", "listen", "cert", "proxy", "ports"}; !slices.Equal(eaIDs(steps), want) {
				t.Fatalf("steps %v, want %v", eaIDs(steps), want)
			}
			out := eaOutputs(tc.want)
			resolve := func(i int) json.RawMessage { return eaResolve(t, steps[i].Input, in, out) }

			eaWantStep(t, steps[0], "public", "", "npm.public.check/v1", nil)
			if got := eaStrict[npm.PublicInput](t, resolve(0)); got != (npm.PublicInput{HostID: eaHost}) {
				t.Errorf("public input %+v, want host %s", got, eaHost)
			}
			eaWantStep(t, steps[1], "network", eaHost, "panel.network.create/v1", []string{"public"})
			if got := eaStrict[NetworkCreateInput](t, resolve(1)); got != (NetworkCreateInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			eaWantStep(t, steps[2], "attach", eaHost, "npm.network.attach/v1", []string{"network"})
			wantAttach := npm.AttachInput{Network: NetworkName, Claimant: Claimant, Address: eaProxy}
			if got := eaStrict[npm.AttachInput](t, resolve(2)); got != wantAttach {
				t.Errorf("attach input %+v, want %+v", got, wantAttach)
			}
			eaWantStep(t, steps[3], "listen", "", "panel.listener.open/v1", []string{"attach"})
			wantListen := ListenerOpenInput{
				Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: int(tc.want),
			}
			if got := eaStrict[ListenerOpenInput](t, resolve(3)); got != wantListen {
				t.Errorf("listen input %+v, want %+v", got, wantListen)
			}
			eaWantStep(t, steps[4], "cert", eaHost, "npm.certificate.request/v1", []string{"listen"})
			if got := eaStrict[npm.CertificateInput](t, resolve(4)); !reflect.DeepEqual(got, npm.CertificateInput{Domains: []string{eaName}}) {
				t.Errorf("certificate input %+v, want the hostname", got)
			}
			eaWantStep(t, steps[5], "proxy", eaHost, "npm.proxyhost.add/v1", []string{"cert"})
			wantProxy := npm.ProxyHostInput{
				Domains: []string{eaName}, ForwardScheme: "https", ForwardHost: eaGateway.String(), ForwardPort: int(tc.want),
				CertificateID: 42, SSLForced: true, HSTS: true,
			}
			if got := eaStrict[npm.ProxyHostInput](t, resolve(5)); !reflect.DeepEqual(got, wantProxy) {
				t.Errorf("proxy input %+v, want %+v", got, wantProxy)
			}
			eaPorts(t, steps[6], in, tc.want, "proxy")
			eaCheckGraph(t, steps, in, tc.want)
		})
	}
}

// TestEaTunnelSteps: the connector is checked first, the proxy is the tunnel's connector, and the tunnel hostname
// routes to the gateway over HTTPS without verifying the internal certificate.
func TestEaTunnelSteps(t *testing.T) {
	for _, tc := range []struct {
		name string
		port int
		want uint16
	}{{"the default port", 0, DefaultPort}, {"an explicit port", 9444, 9444}} {
		t.Run(tc.name, func(t *testing.T) {
			in := eaTunnelInput(tc.port)
			steps := eaOK(t, in)
			if want := []string{"connector", "network", "attach", "listen", "proxy", "ports"}; !slices.Equal(eaIDs(steps), want) {
				t.Fatalf("steps %v, want %v", eaIDs(steps), want)
			}
			out := eaOutputs(tc.want)
			resolve := func(i int) json.RawMessage { return eaResolve(t, steps[i].Input, in, out) }

			eaWantStep(t, steps[0], "connector", eaHost, "panel.connector.check/v1", nil)
			if got := eaStrict[ConnectorCheckInput](t, resolve(0)); got != (ConnectorCheckInput{Tunnel: eaTunnel}) {
				t.Errorf("connector input %+v, want tunnel %s", got, eaTunnel)
			}
			eaWantStep(t, steps[1], "network", eaHost, "panel.network.create/v1", []string{"connector"})
			if got := eaStrict[NetworkCreateInput](t, resolve(1)); got != (NetworkCreateInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			eaWantStep(t, steps[2], "attach", eaHost, "cloudflare.network.attach/v1", []string{"network"})
			wantAttach := cloudflare.NetworkAttachInput{Tunnel: eaTunnel, Network: NetworkName, Claimant: Claimant, Address: eaProxy}
			if got := eaStrict[cloudflare.NetworkAttachInput](t, resolve(2)); got != wantAttach {
				t.Errorf("attach input %+v, want %+v", got, wantAttach)
			}
			eaWantStep(t, steps[3], "listen", "", "panel.listener.open/v1", []string{"attach"})
			wantListen := ListenerOpenInput{
				Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: int(tc.want),
			}
			if got := eaStrict[ListenerOpenInput](t, resolve(3)); got != wantListen {
				t.Errorf("listen input %+v, want %+v", got, wantListen)
			}
			eaWantStep(t, steps[4], "proxy", "", "cloudflare.hostname.add/v1", []string{"listen"})
			wantHostname := cloudflare.HostnameAddInput{
				Tunnel: eaTunnel, Hostname: eaName, Service: "https://" + eaGateway.String() + ":" + strconv.Itoa(int(tc.want)), NoTLSVerify: true,
			}
			if got := eaStrict[cloudflare.HostnameAddInput](t, resolve(4)); got != wantHostname {
				t.Errorf("hostname input %+v, want %+v", got, wantHostname)
			}
			eaPorts(t, steps[5], in, tc.want, "proxy")
			eaCheckGraph(t, steps, in, tc.want)
		})
	}
}

// TestEaLabelFitsTheFirewall: the firewall takes a label of at most 64 characters, and a hostname may be longer, so
// the label is cut and the step stays valid.
func TestEaLabelFitsTheFirewall(t *testing.T) {
	long := strings.Join([]string{strings.Repeat("a", 60), strings.Repeat("b", 60), strings.Repeat("c", 60), "example", "com"}, ".")
	for _, via := range []string{ViaNPM, ViaTunnel} {
		t.Run(via, func(t *testing.T) {
			in := eaNPMInput(0)
			if via == ViaTunnel {
				in = eaTunnelInput(0)
			}
			in.Hostname = long
			steps := eaOK(t, in)
			ports := steps[len(steps)-1]
			resolved := eaResolve(t, ports.Input, in, eaOutputs(DefaultPort))
			label := eaStrict[firewall.RulesApplyInput](t, resolved).Label
			if len(label) > 64 || !strings.HasPrefix(label, "panel exposure ") {
				t.Errorf("label %q (%d characters), want at most 64 characters and the prefix %q", label, len(label), "panel exposure ")
			}
			def, ok := eaDef(t, "firewall.rules.apply")
			if !ok {
				t.Fatal("firewall.rules.apply is not declared")
			}
			if err := schema.NewValidator().ValidateInput(def, resolved); err != nil {
				t.Errorf("ports input is invalid for firewall.rules.apply: %v", err)
			}
		})
	}
}

// TestEaOutput: the output is built from the network and listen steps, with the port the listener binds.
func TestEaOutput(t *testing.T) {
	def := exposureAddAction()
	steps := func(port uint16) map[string]json.RawMessage {
		out := map[string]json.RawMessage{}
		for id, v := range eaOutputs(port) {
			out[id] = eaJSON(t, v)
		}
		out["ports"] = json.RawMessage(`{}`)
		return out
	}
	tests := []struct {
		name string
		in   ExposeInput
		port uint16
		want ExposeOutput
	}{
		{"npm on the default port", eaNPMInput(0), DefaultPort, ExposeOutput{
			ExposureID: eaChange, Via: ViaNPM, Hostname: eaName, HostID: eaHost, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: DefaultPort,
		}},
		{"a tunnel on another port", eaTunnelInput(9444), 9444, ExposeOutput{
			ExposureID: eaChange, Via: ViaTunnel, Hostname: eaName, HostID: eaHost, Tunnel: eaTunnel, Subnet: eaSubnet, Gateway: eaGateway, Proxy: eaProxy, Port: 9444,
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := def.Handler.Output(context.Background(), core.Env{}, eaJSON(t, tc.in), steps(tc.port))
			if err != nil {
				t.Fatal(err)
			}
			if got := eaStrict[ExposeOutput](t, out); got != tc.want {
				t.Errorf("output %+v, want %+v", got, tc.want)
			}
		})
	}
	in := eaJSON(t, eaNPMInput(0))
	for _, missing := range []string{"network", "listen"} {
		partial := steps(DefaultPort)
		delete(partial, missing)
		if _, err := def.Handler.Output(context.Background(), core.Env{}, in, partial); err == nil || !strings.Contains(err.Error(), missing) {
			t.Errorf("output without the %s step: %v, want an error naming it", missing, err)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w8-tmp/9 && TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestEa' ./internal/modules/panel/`
Expected: FAIL to build with `undefined: exposureAddAction`. Quote the output verbatim.

- [ ] **Step 3: Implement**

`exposureAddAction()` is `core.DefineComposite[ExposeInput, ExposeOutput]` with `ActionSpec{Name: ActionAdd, Version: 1, Unwind: core.UnwindReverse, MinScope: core.ScopeAdmin, Summary: "Expose the panel on the controller's host through Nginx Proxy Manager or a Cloudflare tunnel."}` (`DefineComposite` sets the class, the controller side and the lock key `composite:panel`). The composer type is `addComposer`. Follow `internal/modules/npm/composite_deploy_expose.go`: the step templates are built from `map[string]any` and marshalled, and the engine reads every `${` in a template as a reference.

1. `Steps(ctx, env, in)` refuses, in this order and with nothing else read first (W8-R10): (a) `ValidateExpose(in)`, returned as it is (`request.invalid_input` naming the field); (b) `panel.wrong_host` (`errcode.PanelWrongHost`) unless `in.HostID` is the id of the `core_hosts` row with `is_local = 1` (`SELECT id FROM core_hosts WHERE is_local = 1` through `env.DB.Read()`; no such row, or another id, refuses); (c) `panel.totp_incomplete` (`errcode.PanelTOTPIncomplete`) when `SELECT count(*) FROM core_users WHERE state = 'pending'` is above zero; (d) `panel.already_exposed` (`errcode.PanelAlreadyExposed`) when `liveExposure(ctx, env.DB.Read())` finds a row (a gone exposure is not live and refuses nothing).

2. The effective port is `in.Port`, or `DefaultPort` when it is 0. Every step's `Action.Version` is 1. Build the steps of the Contracts "Composite steps" add table exactly, in its order, for the input's `via`; every step except the first has `After` equal to the one-element list holding the id of the step before it (so the first step has no `After`, and every reference a template makes points at a step it follows). The inputs, by JSON member name taken from the wire types the tests decode into:
   - `public` (npm): host `""`, `npm.public.check`, `{"hostId": "${input.hostId}"}`.
   - `connector` (tunnel): host `in.HostID`, `ActionConnCheck`, `{"tunnel": "${input.tunnel}"}`.
   - `network`: host `in.HostID`, `ActionNetCreate`, `{}`.
   - `attach`: host `in.HostID`; for npm `npm.network.attach` with `{"network": NetworkName, "claimant": Claimant, "address": "${steps.network.output.proxy}"}`; for a tunnel `cloudflare.network.attach` with the same members plus `"tunnel": "${input.tunnel}"`.
   - `listen`: host `""`, `ActionOpen`, `{"via": in.Via, "hostname": "${input.hostname}", "hostId": "${input.hostId}", "subnet": "${steps.network.output.subnet}", "gateway": "${steps.network.output.gateway}", "proxy": "${steps.network.output.proxy}", "port": <port>}`, plus `"tunnel": "${input.tunnel}"` for a tunnel (npm omits the member, because `${input.tunnel}` does not exist in an npm input and an unresolved reference fails the plan).
   - `cert` (npm): host `in.HostID`, `npm.certificate.request`, `{"domains": ["${input.hostname}"]}`.
   - `proxy` (npm): host `in.HostID`, `npm.proxyhost.add`, `{"domains": ["${input.hostname}"], "forwardScheme": "https", "forwardHost": "${steps.network.output.gateway}", "forwardPort": <port>, "certificateId": "${steps.cert.output.id}", "sslForced": true, "hsts": true}`.
   - `proxy` (tunnel): host `""`, `cloudflare.hostname.add`, `{"tunnel": "${input.tunnel}", "hostname": "${input.hostname}", "service": "https://${steps.network.output.gateway}:<port>", "noTLSVerify": true}` (the port is part of the string, written as decimal digits).
   - `ports`: host `in.HostID`, `firewall.rules.apply`, `{"label": <label>, "add": [{"id": "${steps.listen.output.ruleId}", "action": "allow", "direction": "in", "proto": "tcp", "from": "${steps.network.output.proxy}", "to": "${steps.network.output.gateway}", "toPort": "<port>", "inIface": BridgeName, "family": "v4"}]}`, with `toPort` the port as a decimal string and no `delete`, `guard`, `overrides` or `manual` member.
   - `<label>` is `"panel exposure " + in.Hostname` cut to its first 64 characters, built by a small function `addLabel(hostname string) string`. The cut is this task's one refinement of the Contracts table: the firewall's label pattern `^[A-Za-z0-9 :._/-]{0,64}$` would otherwise refuse the step, after every earlier step had applied, for a hostname longer than 49 characters, which `ValidateExpose` allows up to 253. The hostname is written into the label literally, which is safe only because `ValidateExpose` has already refused any hostname that is not a lowercase DNS name (no `$`, no `{`), so keep the validation first.
   - The operator-typed values (`hostname`, `hostId`, `tunnel`) reach every other template only as references to the input, as the npm composites do.

3. `Output(ctx, env, in, steps)` decodes the outputs of the `network` step (`NetworkView`) and the `listen` step (`ListenerOpenOutput`) and returns `ExposeOutput{ExposureID: <listen>.ExposureID, Via: in.Via, Hostname: in.Hostname, HostID: in.HostID, Tunnel: in.Tunnel, Subnet, Gateway, Proxy from the network output, Port: int(<listen>.Address.Port())}`. A missing output is an error that names the step (`panel: the exposure has no output of its <id> step`); an output that does not decode is a `panel:` error naming the step too. It does not read `env`.

4. Declare nothing else the contracts do not name except identifiers that start with `add`. Do not edit `panel.go` or `actions.go` (Task 11 lists the action).

- [ ] **Step 4: Run the tests to verify they pass**

Run: `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 600 go test -count=1 -run 'TestEa' -v ./internal/modules/panel/`
Expected: PASS for every `TestEa` test; quote the output verbatim.

Then the checks every Go task ends with, each with its real output recorded:
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 600 go build ./...`
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 300 go vet ./...` (no output)
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 900 go test -race -count=1 ./internal/modules/panel/`
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 1500 go test -count=1 ./...` (every line `ok` or `?`)
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 900 golangci-lint run --allow-serial-runners ./...` (`0 issues.`)
- `TMPDIR=/root/.cache/w8-tmp/9 GOTOOLCHAIN=local timeout 300 go mod tidy -diff` (no output)

Finish with `rm -rf /root/.cache/w8-tmp/9 && GOTOOLCHAIN=local go clean -testcache`.

- [ ] **Step 5: Commit**

```bash
git add internal/modules/panel/expose_add.go internal/modules/panel/expose_add_test.go
git commit -m "feat(panel): add the exposure.add composite"
```

### Task 10: panel: exposure.remove composite

**Files:**
- Create: `internal/modules/panel/expose_remove.go`
- Test: `internal/modules/panel/expose_remove_test.go`
- Read: `internal/modules/npm/composite_deploy_expose.go` and `composite_deploy_expose_test.go` (the composite pattern and how its steps are tested through the erased handler), `internal/modules/cloudflare/expose.go` (`exposeRemoveComposer.Steps` reads the controller database through `env.DB.Read()` and refuses with `core.Definite`), `internal/modules/npm/wire.go` (`ProxyHostRemoveInput`, `DetachInput`), `internal/modules/cloudflare/wire.go` (`HostnameInput`), `internal/modules/cloudflare/connector_wire.go` (`NetworkDetachInput`), `internal/modules/firewall/actions_rules.go` (`RulesApplyInput`, and the 64-character limit on a rule comment)

**Interfaces:**
- Consumes: Contracts "Step retirement" (`core.Step.Retires`, Task 1); "Panel names" (`ActionRemove`, `ActionRevoke`, `ActionNetRemove`, `NetworkName`, `Claimant`, `ViaNPM`, `ViaTunnel`, `StateOpen`, `StateRevoked`, `StateNeedsRepair`, `DefaultPort`); "Wire types" (`RemoveInput`, `RemoveOutput`, `ListenerRevokeInput`, `ListenerRevokeOutput`, `NetworkRemoveInput`); "Exposure store" (`exposureRow`, `liveExposure`, `RuleID`); "Panel controller fixture" (`fdDB`, `fdEnv`, `fdNewListeners`, `fdInsertHost`, `fdInsertChange`, `fdInsertExposure`, `fdNow`); "Composite steps" (the table of `panel.exposure.remove`). The wire types of the child actions come from their modules: `npm.ProxyHostRemoveInput`, `npm.DetachInput`, `cloudflare.HostnameInput`, `cloudflare.NetworkDetachInput`, `firewall.RulesApplyInput`. The panel actions Tasks 6 and 7 build are named by string in the steps and are not called here; this task's tests do not register them (Task 11 does).
- Produces: `exposureRemoveAction() core.ActionDef` (Contracts "Action constructors"); unexported `removeComposer`, `removeInput` and `removeLabel`. Task 11 lists the constructor in the manifest.

**Items:** 36

**Depends on:** Task 1, Task 4
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 1 - spec 2 - coupling 2 - risk 2 = 7

- [ ] **Step 1: Write the failing test**

Create `internal/modules/panel/expose_remove_test.go` with exactly this content. `erDB` seeds the controller's host and the given exposure rows; `erSteps` asks the action for its steps the way the engine does, through `Handler.Steps`; `erPorts` resolves the one reference of the firewall step by plain text replacement, because a resolver for the engine's whole template language belongs to Task 11's registry test.

```go
package panel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/core/store"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	erExposureID = "01JD0000000000000000000010"
	erHostID     = "h1"
	erHostname   = "panel.example.com"
	erTunnel     = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"
	erRemovalID  = "01JD0000000000000000000011"
)

// erRow is the live exposure of the controller's host over via.
func erRow(via string) exposureRow {
	r := exposureRow{
		ID: erExposureID, Via: via, Hostname: erHostname, HostID: erHostID,
		Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		Proxy: netip.MustParseAddr("172.31.255.250"), Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(erExposureID),
		State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
	if via == ViaTunnel {
		r.Tunnel = erTunnel
	}
	return r
}

// erDB is a controller database holding the local host and rows.
func erDB(t *testing.T, rows ...exposureRow) *store.DB {
	t.Helper()
	db := fdDB(t)
	fdInsertHost(t, db, erHostID, true)
	for _, r := range rows {
		fdInsertExposure(t, db, r)
	}
	return db
}

// erSteps asks the removal for its steps the way the engine does, through the erased handler.
func erSteps(t *testing.T, db *store.DB) ([]core.Step, error) {
	t.Helper()
	raw, err := json.Marshal(RemoveInput{})
	if err != nil {
		t.Fatal(err)
	}
	return exposureRemoveAction().Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
}

// erStrict decodes raw into a T and refuses a member T does not have.
func erStrict[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("input %s: %v", raw, err)
	}
	return v
}

// erWant fails the test unless s is the step id on host running action after the steps after, retiring retires.
func erWant(t *testing.T, s core.Step, id, host, action string, after []string, retires string) {
	t.Helper()
	if s.ID != id || s.Host != host || s.Action.String() != action || !slices.Equal(s.After, after) || s.Retires != retires {
		t.Errorf("step %+v, want %s: %s on host %q after %v retiring %q", s, id, action, host, after, retires)
	}
}

func erIDs(steps []core.Step) []string {
	var ids []string
	for _, s := range steps {
		ids = append(ids, s.ID)
	}
	return ids
}

// erPorts decodes the input of the firewall step once its one reference, to the revoke step's rule id, is resolved.
func erPorts(t *testing.T, s core.Step, ruleID string) firewall.RulesApplyInput {
	t.Helper()
	const ref = "${steps.revoke.output.ruleId}"
	if strings.Count(string(s.Input), "${") != 1 || !strings.Contains(string(s.Input), ref) {
		t.Fatalf("ports input %s, want exactly one reference, to %s", s.Input, ref)
	}
	return erStrict[firewall.RulesApplyInput](t, json.RawMessage(strings.ReplaceAll(string(s.Input), ref, ruleID)))
}

func erCode(err error) errcode.Code {
	var e *errcode.Error
	if errors.As(err, &e) {
		return e.Code
	}
	return ""
}

// TestErDeclaration: the removal is a controller-side forward composite under the panel's composite lock, for admins.
func TestErDeclaration(t *testing.T) {
	def := exposureRemoveAction()
	s := def.Spec
	if s.Name != ActionRemove || s.Version != 1 || s.Summary == "" || s.Class != core.ClassComposite || s.Side != core.SideController ||
		s.Unwind != core.UnwindForward || !slices.Equal(s.LockKeys, []string{"composite:panel"}) ||
		s.MinScope != core.ScopeAdmin || core.RequiredScope(s) != core.ScopeAdmin {
		t.Errorf("spec %+v, want panel.exposure.remove v1 with a summary, a controller-side forward composite, lock composite:panel, scope admin", s)
	}
	if def.Handler.Steps == nil || def.Handler.Output == nil {
		t.Error("the handler lacks Steps or Output")
	}
	if def.InputType != reflect.TypeFor[RemoveInput]() || def.OutputType != reflect.TypeFor[RemoveOutput]() {
		t.Errorf("types %v and %v, want RemoveInput and RemoveOutput", def.InputType, def.OutputType)
	}
	if _, err := schema.ActionSnapshot(def); err != nil {
		t.Errorf("schema: %v", err)
	}
}

// TestErSteps: the listener is revoked first and retires the exposure; the proxy host or hostname, the attachment, the
// network and the firewall rule follow in order, each on the exposure's host, with the inputs the other modules take.
func TestErSteps(t *testing.T) {
	for _, via := range []string{ViaNPM, ViaTunnel} {
		t.Run(via, func(t *testing.T) {
			steps, err := erSteps(t, erDB(t, erRow(via)))
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"revoke", "proxy", "attach", "network", "ports"}; !slices.Equal(erIDs(steps), want) {
				t.Fatalf("steps %v, want %v", erIDs(steps), want)
			}
			erWant(t, steps[0], "revoke", "", "panel.listener.revoke/v1", nil, erExposureID)
			if got := erStrict[ListenerRevokeInput](t, steps[0].Input); got != (ListenerRevokeInput{}) {
				t.Errorf("revoke input %+v, want none", got)
			}
			if via == ViaNPM {
				erWant(t, steps[1], "proxy", erHostID, "npm.proxyhost.remove/v1", []string{"revoke"}, "")
				if got := erStrict[npm.ProxyHostRemoveInput](t, steps[1].Input); got != (npm.ProxyHostRemoveInput{Domain: erHostname}) {
					t.Errorf("proxy input %+v, want the hostname as the domain and no id", got)
				}
				erWant(t, steps[2], "attach", erHostID, "npm.network.detach/v1", []string{"proxy"}, "")
				if got := erStrict[npm.DetachInput](t, steps[2].Input); got != (npm.DetachInput{Network: NetworkName, Claimant: Claimant}) {
					t.Errorf("attach input %+v, want the panel network and the panel claimant", got)
				}
			} else {
				erWant(t, steps[1], "proxy", "", "cloudflare.hostname.remove/v1", []string{"revoke"}, "")
				if got := erStrict[cloudflare.HostnameInput](t, steps[1].Input); got != (cloudflare.HostnameInput{Hostname: erHostname}) {
					t.Errorf("proxy input %+v, want the hostname", got)
				}
				erWant(t, steps[2], "attach", erHostID, "cloudflare.network.detach/v1", []string{"proxy"}, "")
				want := cloudflare.NetworkDetachInput{Tunnel: erTunnel, Network: NetworkName, Claimant: Claimant}
				if got := erStrict[cloudflare.NetworkDetachInput](t, steps[2].Input); got != want {
					t.Errorf("attach input %+v, want %+v", got, want)
				}
			}
			erWant(t, steps[3], "network", erHostID, "panel.network.remove/v1", []string{"attach"}, "")
			if got := erStrict[NetworkRemoveInput](t, steps[3].Input); got != (NetworkRemoveInput{}) {
				t.Errorf("network input %+v, want none", got)
			}
			erWant(t, steps[4], "ports", erHostID, "firewall.rules.apply/v1", []string{"network"}, "")
			want := firewall.RulesApplyInput{Label: "panel exposure " + erHostname, Delete: []string{RuleID(erExposureID)}}
			if got := erPorts(t, steps[4], RuleID(erExposureID)); !reflect.DeepEqual(got, want) {
				t.Errorf("ports input %+v, want %+v", got, want)
			}
			for _, s := range steps[:4] {
				if strings.Contains(string(s.Input), "${") {
					t.Errorf("step %s input %s holds a reference, want only the ports step to", s.ID, s.Input)
				}
			}
		})
	}
}

// TestErRemovableStates: an exposure whose listener the break-glass command revoked, one that a restore marked
// needs_repair and one whose removal has not completed are still live, so each plans the same removal.
func TestErRemovableStates(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(r *exposureRow)
		removal string // the state of the removal change, "" for none
	}{
		{"open", func(*exposureRow) {}, ""},
		{"revoked by the break-glass command", func(r *exposureRow) { r.State = StateRevoked }, ""},
		{"needs repair", func(r *exposureRow) { r.State = StateNeedsRepair }, ""},
		{"revoked by a removal that needs repair", func(r *exposureRow) { r.State = StateRevoked; r.RemovalChangeID = erRemovalID }, core.StateNeedsRepair},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := erRow(ViaNPM)
			tc.mutate(&row)
			db := erDB(t)
			if tc.removal != "" {
				fdInsertChange(t, db, erRemovalID, ActionRemove, tc.removal)
			}
			fdInsertExposure(t, db, row)
			steps, err := erSteps(t, db)
			if err != nil {
				t.Fatal(err)
			}
			if want := []string{"revoke", "proxy", "attach", "network", "ports"}; !slices.Equal(erIDs(steps), want) || steps[0].Retires != erExposureID {
				t.Errorf("steps %v retiring %q, want %v retiring %s", erIDs(steps), steps[0].Retires, want, erExposureID)
			}
		})
	}
}

// TestErRefusals: with no live exposure the removal is refused with panel.not_exposed before any step is planned, and
// an exposure that a completed removal revoked is gone.
func TestErRefusals(t *testing.T) {
	gone := erRow(ViaNPM)
	gone.State, gone.RemovalChangeID = StateRevoked, erRemovalID
	tests := []struct {
		name string
		seed func(t *testing.T) *store.DB
	}{
		{"no exposure", func(t *testing.T) *store.DB { return erDB(t) }},
		{"a removed exposure", func(t *testing.T) *store.DB {
			db := erDB(t)
			fdInsertChange(t, db, erRemovalID, ActionRemove, core.StateCompleted)
			fdInsertExposure(t, db, gone)
			return db
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := erSteps(t, tc.seed(t))
			if erCode(err) != errcode.PanelNotExposed || !core.IsDefinite(err) || len(steps) != 0 {
				t.Errorf("steps %+v, error %v, want a definite %s refusal", steps, err, errcode.PanelNotExposed)
			}
		})
	}
}

// TestErPlansTheLiveExposure: a removed exposure of the past does not hide or replace the live one.
func TestErPlansTheLiveExposure(t *testing.T) {
	old := erRow(ViaNPM)
	old.ID, old.Hostname, old.State, old.RemovalChangeID = "01JD0000000000000000000001", "old.example.com", StateRevoked, erRemovalID
	db := erDB(t)
	fdInsertChange(t, db, erRemovalID, ActionRemove, core.StateCompleted)
	fdInsertExposure(t, db, old)
	fdInsertExposure(t, db, erRow(ViaTunnel))
	steps, err := erSteps(t, db)
	if err != nil {
		t.Fatal(err)
	}
	if steps[0].Retires != erExposureID || steps[1].Action.Name != "cloudflare.hostname.remove" {
		t.Errorf("steps retire %q and remove through %s, want the live tunnel exposure %s", steps[0].Retires, steps[1].Action.Name, erExposureID)
	}
	if got := erStrict[cloudflare.HostnameInput](t, steps[1].Input); got.Hostname != erHostname {
		t.Errorf("proxy input %+v, want the live exposure's hostname %s", got, erHostname)
	}
}

// TestErLabelFitsTheFirewall: the firewall step's label is the rule comment, which the firewall limits to 64
// characters, so the label is cut to 64 characters, as the add composite cuts it, and the removal is still planned.
func TestErLabelFitsTheFirewall(t *testing.T) {
	comment := regexp.MustCompile(`^[A-Za-z0-9 :._/-]{0,64}$`)
	tests := []struct {
		name     string
		hostname string
		want     string
	}{
		{"short", erHostname, "panel exposure " + erHostname},
		{"the longest that fits", strings.Repeat("a", 37) + ".example.com", "panel exposure " + strings.Repeat("a", 37) + ".example.com"},
		{"one more", strings.Repeat("a", 38) + ".example.com", ("panel exposure " + strings.Repeat("a", 38) + ".example.com")[:64]},
		{"long", strings.Repeat("a", 60) + ".example.com", ("panel exposure " + strings.Repeat("a", 60))[:64]},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row := erRow(ViaNPM)
			row.Hostname = tc.hostname
			steps, err := erSteps(t, erDB(t, row))
			if err != nil {
				t.Fatal(err)
			}
			got := erPorts(t, steps[4], RuleID(erExposureID)).Label
			if got != tc.want || !comment.MatchString(got) {
				t.Errorf("label %q, want %q, which the firewall accepts as a comment", got, tc.want)
			}
		})
	}
}

// TestErOutput: the output names the exposure the revoke step revoked and its hostname, which stays stored once the
// exposure is gone.
func TestErOutput(t *testing.T) {
	def := exposureRemoveAction()
	in, err := json.Marshal(RemoveInput{})
	if err != nil {
		t.Fatal(err)
	}
	revoke := func(id string) json.RawMessage {
		raw, err := json.Marshal(ListenerRevokeOutput{ExposureID: id, ListenerID: "l1", RuleID: RuleID(erExposureID), Revoked: true})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	gone := erRow(ViaNPM)
	gone.State, gone.RemovalChangeID = StateRevoked, erRemovalID
	tests := []struct {
		name    string
		removal string // the state of the removal change, "" for none
		row     exposureRow
		steps   map[string]json.RawMessage
		want    *RemoveOutput // nil: an error naming wantErr
		wantErr string
	}{
		{"an open exposure", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke(erExposureID)},
			&RemoveOutput{ExposureID: erExposureID, Hostname: erHostname}, ""},
		{"a removed exposure", core.StateCompleted, gone, map[string]json.RawMessage{"revoke": revoke(erExposureID)},
			&RemoveOutput{ExposureID: erExposureID, Hostname: erHostname}, ""},
		{"no revoke step", "", erRow(ViaNPM), map[string]json.RawMessage{}, nil, "revoke"},
		{"a revoke step that names no exposure", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke("")}, nil, "exposure"},
		{"an exposure that is not stored", "", erRow(ViaNPM), map[string]json.RawMessage{"revoke": revoke("01JD0000000000000000000099")}, nil, "01JD0000000000000000000099"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			db := erDB(t)
			if tc.removal != "" {
				fdInsertChange(t, db, erRemovalID, ActionRemove, tc.removal)
			}
			fdInsertExposure(t, db, tc.row)
			out, err := def.Handler.Output(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), in, tc.steps)
			if tc.want == nil {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("output %s, error %v, want an error naming %q", out, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got := erStrict[RemoveOutput](t, out); got != *tc.want {
				t.Errorf("output %+v, want %+v", got, *tc.want)
			}
		})
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run: `mkdir -p /root/.cache/w8-tmp/10 && export TMPDIR=/root/.cache/w8-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestEr' ./internal/modules/panel/`
Expected: FAIL to build with `undefined: exposureRemoveAction`. Quote the output in the report.

- [ ] **Step 3: Implement**

Create `internal/modules/panel/expose_remove.go`.

1. `exposureRemoveAction() core.ActionDef` returns `core.DefineComposite[RemoveInput, RemoveOutput]` over `removeComposer{}` with the spec `Name: ActionRemove`, `Version: 1`, `Unwind: core.UnwindForward`, `MinScope: core.ScopeAdmin` and a one-sentence `Summary` (revoke the listener, then remove the proxy host or hostname, the attachment, the network and the firewall rule). `DefineComposite` itself sets the class, the controller side and the lock key `composite:panel`. Give the function and the type a doc comment.
2. `removeComposer.Steps(ctx, env, _ RemoveInput)` reads the live exposure with `liveExposure(ctx, env.DB.Read())`. A read error is returned wrapped with `panel:`. With no live row it returns `core.Definite(errcode.New(errcode.PanelNotExposed, detail))` with a one-sentence detail and no steps (plan ruling W8-R11); a row in any state of `open`, `revoked` or `needs_repair` that is not gone is removable, and `liveExposure` already applies the "gone" predicate of Contracts "Exposure store".
3. It returns the five steps of Contracts "Composite steps" for `panel.exposure.remove`, with `E` the stored row: `revoke` (controller, `panel.listener.revoke`, input `{}`, `Retires: E.ID`); `proxy` (for `ViaNPM` on host `E.HostID`, `npm.proxyhost.remove`, input `{"domain": E.Hostname}`; for `ViaTunnel` a controller step, `cloudflare.hostname.remove`, input `{"hostname": E.Hostname}`); `attach` (host `E.HostID`; for `ViaNPM` `npm.network.detach` with `{"network": NetworkName, "claimant": Claimant}`; for `ViaTunnel` `cloudflare.network.detach` with `{"tunnel": E.Tunnel, "network": NetworkName, "claimant": Claimant}`); `network` (host `E.HostID`, `panel.network.remove`, input `{}`); `ports` (host `E.HostID`, `firewall.rules.apply`, input `{"label": removeLabel(E.Hostname), "delete": ["${steps.revoke.output.ruleId}"]}`). Every action is version 1. Each step's `After` is the id of the step before it, in table order. Only `revoke` sets `Retires`.
4. Build each input by marshalling a map through one helper `removeInput` (an error is wrapped with `panel:`), so the members carry the JSON names of the wire types the test decodes. The engine reads every `${` of a template as a reference; the only one here is the firewall step's, and the stored hostname and tunnel cannot hold `${` because the add validated them as a DNS name and a UUID.
5. `removeLabel(hostname string) string` returns `"panel exposure " + hostname` cut to its first 64 characters, the same rule as Task 9's `addLabel` (Contracts "Composite steps"). The firewall validates the label of a `rules.apply` input as a rule comment (`^[A-Za-z0-9 :._/-]{0,64}$`, also when the input only deletes), so a longer label would make the final step of the removal fail after the listener was already revoked. The cut follows Contracts "Composite steps".
6. `removeComposer.Output(ctx, env, _ RemoveInput, steps)` reads `steps["revoke"]` as a `ListenerRevokeOutput` (a missing step, undecodable JSON or an empty `ExposureID` is an error naming the revoke step or the exposure), then reads `SELECT hostname FROM panel_exposures WHERE id = ?` through `env.DB.Read()` for that id (no row is an error naming the id; it must not use `liveExposure`, because the exposure is gone once the removal has completed) and returns `RemoveOutput{ExposureID, Hostname}`.
7. Edit no other file. The step actions are looked up by name by the engine; this task registers nothing.

- [ ] **Step 4: Run the test to verify it passes**

Run: `mkdir -p /root/.cache/w8-tmp/10 && export TMPDIR=/root/.cache/w8-tmp/10 GOTOOLCHAIN=local && timeout 300 go test -count=1 -run 'TestEr' -v ./internal/modules/panel/`
Expected: PASS for `TestErDeclaration`, `TestErSteps` (npm, tunnel), `TestErRemovableStates`, `TestErRefusals`, `TestErPlansTheLiveExposure`, `TestErLabelFitsTheFirewall` and `TestErOutput`.

- [ ] **Step 5: Full checks and commit**

Run, each with its real output recorded: `mkdir -p /root/.cache/w8-tmp/10 && export TMPDIR=/root/.cache/w8-tmp/10 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/modules/panel/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Then `rm -rf /root/.cache/w8-tmp/10 && go clean -testcache`.

```bash
git add internal/modules/panel/expose_remove.go internal/modules/panel/expose_remove_test.go
git commit -m "feat(panel): add the exposure.remove composite"
```

### Task 11: panel: register the actions, schemas and spec rulings

**Files:**
- Create: `internal/modules/panel/actions.go`
- Modify: `internal/modules/panel/panel.go` (the `module` type, `Module` and `Manifest`)
- Modify (test): `internal/modules/panel/panel_test.go` (replaced as a whole: `TestManifestIsASkeleton` is deleted), `internal/modules/modules_test.go` (one test appended), `internal/modules/registry_test.go` (one test appended)
- Create (generated by `make schemas`): `schemas/panel.network.create/v1.json`, `schemas/panel.network.remove/v1.json`, `schemas/panel.connector.check/v1.json`, `schemas/panel.listener.open/v1.json`, `schemas/panel.listener.revoke/v1.json`, `schemas/panel.exposure.status/v1.json`, `schemas/panel.exposure.add/v1.json`, `schemas/panel.exposure.remove/v1.json`
- Modify: `docs/dforge/specs/2026-10-21-v1-completion-design.md` (section 8: the `--local` phrase of the break-glass sentence, and one new bullet; no other line)
- Read: `internal/modules/npm/npm.go`, `internal/modules/npm/actions.go` and `internal/modules/npm/register_test.go` (the manifest and the registration test this task follows, including `TestRegSnapshotsAreCommitted`), `internal/modules/modules.go` (already lists `panel.Module()`, unchanged), `internal/modules/modules_test.go`, `internal/modules/registry_test.go`, `internal/cli/root.go` (`addConnFlags` gives a break-glass command `--socket` and `--dev` and no `--url`) and `internal/cli/local.go` (`refuseLocal`), the wave 7 rulings bullet of spec section 10

**Interfaces:**
- Consumes: Contracts "Action constructors" (all eight: `networkCreateAction(d agentDeps)`, `networkRemoveAction(d agentDeps)`, `connectorCheckAction(d agentDeps)`, `listenerOpenAction()`, `listenerRevokeAction()`, `statusAction()`, `exposureAddAction()`, `exposureRemoveAction()`); "Panel agent dependencies" (`agentDeps`, `productionAgent`); "Exposure store" (`controllerMigrations`, `exposureRow`, `RuleID`); "Panel controller fixture" (`fdDB`, `fdEnv`, `fdNewListeners`, `fdInsertHost`, `fdInsertUser`, `fdInsertExposure`, `fdNow`); "Wire types", "Panel names". The npm, cloudflare and firewall modules, whose actions the composites call, are used through their exported `Module()`.
- Produces: the panel manifest with eight actions and the controller migrations (what Tasks 5, 8 and 12 and the CLI rely on); `regActions(d agentDeps) []core.ActionDef`; the eight committed snapshots `schemas/panel.*/v1.json`; the "Wave 8 rulings" bullet of spec section 8.

**Items:** 36

**Depends on:** Task 4, Task 6, Task 7, Task 9, Task 10
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 1 = 6

- [ ] **Step 1: Write the failing tests**

Three files change. Replace `internal/modules/panel/panel_test.go` with exactly this content (it checks the manifest against a table of the eight actions, every input against its schema, the committed snapshots, and the steps of both composites against the real registry: sides, hosts, classes, undo handlers, convergence, references, retirement, and each resolved input against the child action's schema):

```go
package panel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/core"
	"github.com/darkraise/darkpanel/internal/core/errcode"
	"github.com/darkraise/darkpanel/internal/core/schema"
	"github.com/darkraise/darkpanel/internal/modules/cloudflare"
	"github.com/darkraise/darkpanel/internal/modules/firewall"
	"github.com/darkraise/darkpanel/internal/modules/npm"
)

const (
	rgHostID   = "h1"
	rgHostname = "panel.example.com"
	rgTunnel   = "0b6c1f8e-3d52-4a7b-9c14-5e8d2a1f7b30"
)

// rgAction is one row of the table of the actions spec section 8 and the plan rulings give the module.
type rgAction struct {
	name          string
	class         core.Class
	side          core.Side
	locks         []string
	in, out       reflect.Type
	undoer        bool // has an Inverse (and so an Observe) handler
	internal      bool
	converges     bool
	forwardOnly   bool
	breakGlass    bool
	compensatedBy string
	unwind        core.Unwind
}

func rgType[T any]() reflect.Type { return reflect.TypeFor[T]() }

var rgActions = []rgAction{
	{name: ActionNetCreate, class: core.ClassCompensable, side: core.SideAgent, locks: []string{"panel"}, undoer: true, in: rgType[NetworkCreateInput](), out: rgType[NetworkView]()},
	{name: ActionNetRemove, class: core.ClassCompensable, side: core.SideAgent, locks: []string{"panel"}, converges: true, compensatedBy: ActionNetCreate, in: rgType[NetworkRemoveInput](), out: rgType[NetworkRemoveOutput]()},
	{name: ActionConnCheck, class: core.ClassRead, side: core.SideAgent, internal: true, in: rgType[ConnectorCheckInput](), out: rgType[ConnectorCheckOutput]()},
	{name: ActionOpen, class: core.ClassCompensable, side: core.SideController, locks: []string{"panel"}, undoer: true, in: rgType[ListenerOpenInput](), out: rgType[ListenerOpenOutput]()},
	{name: ActionRevoke, class: core.ClassCompensable, side: core.SideController, locks: []string{"panel"}, converges: true, forwardOnly: true, breakGlass: true, in: rgType[ListenerRevokeInput](), out: rgType[ListenerRevokeOutput]()},
	{name: ActionStatus, class: core.ClassRead, side: core.SideController, in: rgType[StatusInput](), out: rgType[StatusOutput]()},
	{name: ActionAdd, class: core.ClassComposite, side: core.SideController, locks: []string{"composite:panel"}, unwind: core.UnwindReverse, in: rgType[ExposeInput](), out: rgType[ExposeOutput]()},
	{name: ActionRemove, class: core.ClassComposite, side: core.SideController, locks: []string{"composite:panel"}, unwind: core.UnwindForward, in: rgType[RemoveInput](), out: rgType[RemoveOutput]()},
}

func rgDefs(t *testing.T, m core.Module) map[string]core.ActionDef {
	t.Helper()
	defs := map[string]core.ActionDef{}
	for _, a := range m.Manifest().Actions {
		if _, dup := defs[a.Spec.Name]; dup {
			t.Errorf("%s is declared twice", a.Spec.Name)
		}
		defs[a.Spec.Name] = a
	}
	return defs
}

// TestRgManifestDeclaresTheActions: the module declares the eight actions of the table, all version 1 and for
// admins, each on its side under the panel lock (the composites under composite:panel, the reads under none), with the
// class, handlers, flags and unwind policy of the table.
func TestRgManifestDeclaresTheActions(t *testing.T) {
	m := Module()
	man := m.Manifest()
	if man.ID != ID || man.Version != 1 || !man.DefaultEnabled {
		t.Errorf("manifest %q v%d default-enabled %v, want panel v1 enabled by default", man.ID, man.Version, man.DefaultEnabled)
	}
	got := rgDefs(t, m)
	for _, want := range rgActions {
		a, ok := got[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		delete(got, want.name)
		s := a.Spec
		if s.Version != 1 || s.Summary == "" || s.Class != want.class || s.Side != want.side || !slices.Equal(s.LockKeys, want.locks) ||
			s.MinScope != core.ScopeAdmin || core.RequiredScope(s) != core.ScopeAdmin || s.Internal != want.internal ||
			s.Converges != want.converges || s.ForwardOnly != want.forwardOnly || s.BreakGlass != want.breakGlass ||
			s.CompensatedBy != want.compensatedBy || s.Unwind != want.unwind {
			t.Errorf("%s: spec %+v; want class %s on the %s, locks %v, scope admin, internal %v, converges %v, forward-only %v, break-glass %v, compensated by %q, unwind %q",
				want.name, s, want.class, want.side, want.locks, want.internal, want.converges, want.forwardOnly, want.breakGlass, want.compensatedBy, want.unwind)
		}
		h := a.Handler
		if (h.Inverse != nil) != want.undoer || (want.undoer && h.Observe == nil) {
			t.Errorf("%s: undo handler %v, observer %v; want undo %v", want.name, h.Inverse != nil, h.Observe != nil, want.undoer)
		}
		var served bool
		switch {
		case want.internal:
			served = h.Reveal != nil && h.Read == nil
		case want.class == core.ClassRead:
			served = h.Read != nil && h.Reveal == nil
		case want.class == core.ClassComposite:
			served = h.Steps != nil && h.Output != nil && h.Plan == nil && h.Apply == nil
		default:
			served = h.Plan != nil && h.Verify != nil && h.Apply != nil
		}
		if !served {
			t.Errorf("%s: handlers do not match its class %s (internal %v)", want.name, want.class, want.internal)
		}
		if a.InputType != want.in || a.OutputType != want.out {
			t.Errorf("%s: types %v and %v, want %v and %v", want.name, a.InputType, a.OutputType, want.in, want.out)
		}
		if _, err := schema.ActionSnapshot(a); err != nil {
			t.Errorf("%s: %v", a.Ref(), err)
		}
	}
	for name := range got {
		t.Errorf("%s is declared but is not in the action table", name)
	}
}

// TestRgManifestDeclaresTheRest: no resource, preflight check, external unit or owned path, and the controller's
// migrations only.
func TestRgManifestDeclaresTheRest(t *testing.T) {
	man := Module().Manifest()
	if len(man.Resources) != 0 || len(man.Preflight) != 0 || len(man.ExternalUnits) != 0 || len(man.OwnedPaths) != 0 {
		t.Errorf("manifest declares %d resources, %d preflight checks, %d external units and %d owned paths, want none",
			len(man.Resources), len(man.Preflight), len(man.ExternalUnits), len(man.OwnedPaths))
	}
	migrations, ok := man.Migrations[core.SideController]
	if !ok || len(man.Migrations) != 1 {
		t.Fatalf("migrations %v, want the controller side only", man.Migrations)
	}
	found := false
	err := fs.WalkDir(migrations, ".", func(p string, d fs.DirEntry, err error) error {
		found = found || err == nil && !d.IsDir() && filepath.Base(p) == "0001_panel.sql"
		return err
	})
	if err != nil || !found {
		t.Errorf("controller migrations hold no 0001_panel.sql (walk error %v)", err)
	}
}

var (
	rgSubnet  = netip.MustParsePrefix("172.31.255.248/29")
	rgGateway = netip.MustParseAddr("172.31.255.249")
	rgProxy   = netip.MustParseAddr("172.31.255.250")
)

// rgSamples are valid inputs of the actions, written with the types' own JSON names.
var rgSamples = map[string]any{
	ActionNetCreate: NetworkCreateInput{},
	ActionNetRemove: NetworkRemoveInput{},
	ActionConnCheck: ConnectorCheckInput{Tunnel: rgTunnel},
	ActionOpen: ListenerOpenInput{Via: ViaNPM, Hostname: rgHostname, HostID: rgHostID, Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy,
		Port: DefaultPort},
	ActionRevoke: ListenerRevokeInput{},
	ActionStatus: StatusInput{},
	ActionAdd:    ExposeInput{Via: ViaTunnel, Hostname: rgHostname, HostID: rgHostID, Tunnel: rgTunnel, Port: 9444},
	ActionRemove: RemoveInput{},
}

func rgMembers(t *testing.T, v any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// rgRefusal requires err to be request.invalid_input naming field.
func rgRefusal(err error, field string) bool {
	var coded *errcode.Error
	return errors.As(err, &coded) && coded.Code == errcode.InvalidInput &&
		slices.ContainsFunc(coded.Fields, func(f errcode.FieldError) bool { return f.Path == field })
}

// TestRgInputsAcceptTheirSampleAndRefuseUnknownMembers: each action is registered with its own input type, so its
// schema accepts the input it takes and refuses a member it does not have.
func TestRgInputsAcceptTheirSampleAndRefuseUnknownMembers(t *testing.T) {
	defs := rgDefs(t, Module())
	v := schema.NewValidator()
	for _, want := range rgActions {
		def, ok := defs[want.name]
		if !ok {
			t.Errorf("%s is not declared", want.name)
			continue
		}
		members := rgMembers(t, rgSamples[want.name])
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); err != nil {
			t.Errorf("%s %s: %v, want it accepted", want.name, raw, err)
		}
		members["x"] = 1
		raw, _ = json.Marshal(members)
		if err := v.ValidateInput(def, raw); !rgRefusal(err, "/x") {
			t.Errorf("%s %s: %v, want request.invalid_input naming /x", want.name, raw, err)
		}
	}
}

// TestRgInputsRefuseWhatTheWireTypesRequire: the members the wire types make required or typed.
func TestRgInputsRefuseWhatTheWireTypesRequire(t *testing.T) {
	defs := rgDefs(t, Module())
	v := schema.NewValidator()
	for _, tc := range []struct {
		name, action, field string
		mutate              func(map[string]any)
	}{
		{"expose without the route", ActionAdd, "/via", func(m map[string]any) { delete(m, "via") }},
		{"expose without the hostname", ActionAdd, "/hostname", func(m map[string]any) { delete(m, "hostname") }},
		{"expose without the host", ActionAdd, "/hostId", func(m map[string]any) { delete(m, "hostId") }},
		{"expose with a text port", ActionAdd, "/port", func(m map[string]any) { m["port"] = "9443" }},
		{"listener open without the proxy address", ActionOpen, "/proxy", func(m map[string]any) { delete(m, "proxy") }},
		{"connector check without the tunnel", ActionConnCheck, "/tunnel", func(m map[string]any) { delete(m, "tunnel") }},
	} {
		def, ok := defs[tc.action]
		if !ok {
			t.Errorf("%s: %s is not declared", tc.name, tc.action)
			continue
		}
		members := rgMembers(t, rgSamples[tc.action])
		tc.mutate(members)
		raw, _ := json.Marshal(members)
		if err := v.ValidateInput(def, raw); !rgRefusal(err, tc.field) {
			t.Errorf("%s: %s %s: %v, want request.invalid_input naming %s", tc.name, tc.action, raw, err, tc.field)
		}
	}
}

// TestRgNoSecretIsDeclared: the module handles no secret, so no input and no output schema marks a field secret.
func TestRgNoSecretIsDeclared(t *testing.T) {
	for _, def := range Module().Manifest().Actions {
		in, err := schema.Input(def.InputType)
		if err != nil {
			t.Errorf("%s input: %v", def.Ref(), err)
		}
		out, err := schema.Output(def.OutputType)
		if err != nil {
			t.Errorf("%s output: %v", def.Ref(), err)
		}
		if strings.Contains(string(in), "x-secret") || strings.Contains(string(out), "x-secret") {
			t.Errorf("%s: a schema marks a field secret", def.Ref())
		}
	}
}

// TestRgSnapshotsAreCommitted: each action has its committed snapshot, and no panel snapshot exists for anything else
// (modules.TestSnapshotsCurrent checks that each file is current).
func TestRgSnapshotsAreCommitted(t *testing.T) {
	dir := filepath.Join("..", "..", "..", "schemas")
	want := map[string]bool{}
	for _, a := range rgActions {
		want[a.name] = true
		if _, err := os.Stat(filepath.Join(dir, a.name, "v1.json")); err != nil {
			t.Errorf("schemas/%s/v1.json: %v; run make schemas", a.name, err)
		}
	}
	dirs, err := filepath.Glob(filepath.Join(dir, "panel.*"))
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if name := filepath.Base(d); !want[name] {
			t.Errorf("schemas/%s belongs to no panel action; run make schemas", name)
		}
	}
}

// rgRegistry is the registry of the panel module with the modules its composites call.
func rgRegistry(t *testing.T) *core.Registry {
	t.Helper()
	reg, err := core.NewRegistry(Module(), npm.Module(), cloudflare.Module(), firewall.Module())
	if err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestRgRegistryAcceptsTheModule(t *testing.T) {
	reg := rgRegistry(t)
	for _, a := range rgActions {
		if _, ok := reg.Action(core.ActionRef{Name: a.name, Version: 1}); !ok {
			t.Errorf("the registry has no %s", a.name)
		}
	}
}

var (
	rgRef     = regexp.MustCompile(`\$\{([^}]*)\}`)
	rgStepRef = regexp.MustCompile(`\$\{steps\.([a-z][a-z0-9]*)\.output`)
)

func rgGeneric(t *testing.T, v any) any {
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

// rgResolve resolves the references of a step template against the composite's input in and the outputs of earlier
// steps, as the engine does: a string that is exactly one reference becomes the referenced value with its JSON type,
// a reference inside a longer string is replaced by the value's text. A reference to a member that does not exist
// fails the test, as it fails the plan.
func rgResolve(t *testing.T, tmpl json.RawMessage, in any, outputs map[string]any) json.RawMessage {
	t.Helper()
	input := rgGeneric(t, in)
	steps := map[string]any{}
	for id, v := range outputs {
		steps[id] = map[string]any{"output": rgGeneric(t, v)}
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
			switch c := cur.(type) {
			case map[string]any:
				next, ok := c[p]
				if !ok {
					t.Errorf("reference ${%s}: there is no member %q", ref, p)
					return nil
				}
				cur = next
			case []any:
				i, err := strconv.Atoi(p)
				if err != nil || i < 0 || i >= len(c) {
					t.Errorf("reference ${%s}: %q is not an index of the array", ref, p)
					return nil
				}
				cur = c[i]
			default:
				t.Errorf("reference ${%s}: %q is looked up in a value that has no members", ref, p)
				return nil
			}
		}
		return cur
	}
	var walk func(v any) any
	walk = func(v any) any {
		switch x := v.(type) {
		case string:
			if m := rgRef.FindStringSubmatch(x); m != nil && m[0] == x {
				return lookup(m[1])
			}
			return rgRef.ReplaceAllStringFunc(x, func(s string) string { return fmt.Sprint(lookup(s[2 : len(s)-1])) })
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
	out, err := json.Marshal(walk(tv))
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// rgOutputs gives each step action whose output a composite's steps reference a sample output of its output type.
var rgOutputs = map[string]any{
	ActionNetCreate:           NetworkView{Network: NetworkName, ID: "n1", Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy},
	"npm.certificate.request": npm.CertificateView{ID: 3, Domains: []string{rgHostname}},
	ActionOpen:                ListenerOpenOutput{ExposureID: "c1", ListenerID: "l1", RuleID: RuleID("c1"), Address: netip.AddrPortFrom(rgGateway, DefaultPort)},
	ActionRevoke:              ListenerRevokeOutput{ExposureID: "c1", ListenerID: "l1", RuleID: RuleID("c1"), Revoked: true},
}

// rgComposites are the two composites with the step ids each returns; the removal reads row, the live exposure.
var rgComposites = []struct {
	name, action string
	input        any
	forward      bool
	steps        []string
	row          *exposureRow
	ruleRef      string // the reference the ports step holds to the rule id
}{
	{"expose through NPM", ActionAdd, ExposeInput{Via: ViaNPM, Hostname: rgHostname, HostID: rgHostID}, false,
		[]string{"public", "network", "attach", "listen", "cert", "proxy", "ports"}, nil, "${steps.listen.output.ruleId}"},
	{"expose through a tunnel", ActionAdd, ExposeInput{Via: ViaTunnel, Hostname: rgHostname, HostID: rgHostID, Tunnel: rgTunnel}, false,
		[]string{"connector", "network", "attach", "listen", "proxy", "ports"}, nil, "${steps.listen.output.ruleId}"},
	{"remove an NPM exposure", ActionRemove, RemoveInput{}, true,
		[]string{"revoke", "proxy", "attach", "network", "ports"}, rgRow(ViaNPM), "${steps.revoke.output.ruleId}"},
	{"remove a tunnel exposure", ActionRemove, RemoveInput{}, true,
		[]string{"revoke", "proxy", "attach", "network", "ports"}, rgRow(ViaTunnel), "${steps.revoke.output.ruleId}"},
}

// rgRow is the live exposure of the controller's host over via.
func rgRow(via string) *exposureRow {
	id := "01JD0000000000000000000010"
	r := &exposureRow{
		ID: id, Via: via, Hostname: rgHostname, HostID: rgHostID, Subnet: rgSubnet, Gateway: rgGateway, Proxy: rgProxy,
		Port: DefaultPort, ListenerID: "l1", RuleID: RuleID(id), State: StateOpen, CreatedAt: fdNow, UpdatedAt: fdNow,
	}
	if via == ViaTunnel {
		r.Tunnel = rgTunnel
	}
	return r
}

// TestRgStepsMatchTheRegistry: every step a composite returns names a registered action and obeys the rules the engine
// checks at plan time: a controller action takes no host and an agent action the controller's host, no step is
// irreversible or a composite, at most one step is revertible and it is last, every compensable step of a reverse
// composite has an undo handler, every step of a forward composite that is not a read converges, each step follows
// only steps before it and references only steps it follows, only a non-read step of a forward composite retires an
// exposure, and each step's input is valid for its action's schema once its references are resolved (rgResolve)
// against the composite's input and sample outputs of the steps it follows.
func TestRgStepsMatchTheRegistry(t *testing.T) {
	validator := schema.NewValidator()
	reg := rgRegistry(t)
	for _, c := range rgComposites {
		t.Run(c.name, func(t *testing.T) {
			def, ok := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			if !ok {
				t.Fatalf("%s is not registered", c.action)
			}
			if (def.Spec.Unwind == core.UnwindForward) != c.forward {
				t.Errorf("%s unwinds %q, want forward %v", c.action, def.Spec.Unwind, c.forward)
			}
			db := fdDB(t)
			fdInsertHost(t, db, rgHostID, true)
			fdInsertUser(t, db, "ann", "active")
			if c.row != nil {
				fdInsertExposure(t, db, *c.row)
			}
			raw, _ := json.Marshal(c.input)
			steps, err := def.Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
			if err != nil {
				t.Fatal(err)
			}
			index := map[string]int{}
			follows := map[string]map[string]bool{}
			outputs := map[string]any{}
			revertible := -1
			var got []string
			for i, s := range steps {
				got = append(got, s.ID)
				if _, dup := index[s.ID]; dup {
					t.Errorf("step id %s appears twice", s.ID)
				}
				index[s.ID] = i
				follows[s.ID] = map[string]bool{}
				for _, a := range s.After {
					if before, ok := index[a]; !ok || before >= i {
						t.Errorf("step %s follows %s, which is not an earlier step", s.ID, a)
					}
					follows[s.ID][a] = true
					for f := range follows[a] {
						follows[s.ID][f] = true
					}
				}
				if o, ok := rgOutputs[s.Action.Name]; ok {
					outputs[s.ID] = o
				}
				child, ok := reg.Action(s.Action)
				if !ok {
					t.Errorf("step %s: %s is not registered", s.ID, s.Action)
					continue
				}
				if child.Spec.Side == core.SideController && s.Host != "" || child.Spec.Side == core.SideAgent && s.Host != rgHostID {
					t.Errorf("step %s: %s is %s-side but the step names host %q", s.ID, s.Action, child.Spec.Side, s.Host)
				}
				switch child.Spec.Class {
				case core.ClassIrreversible, core.ClassComposite:
					t.Errorf("step %s: %s is %s, which a step never is", s.ID, s.Action, child.Spec.Class)
				case core.ClassRevertible:
					if revertible >= 0 {
						t.Errorf("step %s: a second revertible step", s.ID)
					}
					revertible = i
				case core.ClassCompensable:
					if !c.forward && child.Handler.Inverse == nil {
						t.Errorf("step %s: %s has no undo handler, which a reverse composite needs", s.ID, s.Action)
					}
				}
				if c.forward && child.Spec.Class != core.ClassRead && !child.Spec.Converges {
					t.Errorf("step %s: %s does not converge, which a forward composite needs of every step that is not a read", s.ID, s.Action)
				}
				if s.Retires != "" && (!c.forward || child.Spec.Class == core.ClassRead) {
					t.Errorf("step %s retires %q, which only a non-read step of a forward composite may", s.ID, s.Retires)
				}
				for _, m := range rgStepRef.FindAllStringSubmatch(string(s.Input), -1) {
					if !follows[s.ID][m[1]] {
						t.Errorf("step %s references step %s but does not follow it", s.ID, m[1])
					}
				}
				followed := map[string]any{}
				for id := range follows[s.ID] {
					if o, ok := outputs[id]; ok {
						followed[id] = o
					}
				}
				input := rgResolve(t, s.Input, c.input, followed)
				if strings.Contains(string(input), "${") {
					t.Errorf("step %s: input %s holds a reference left unresolved", s.ID, s.Input)
					continue
				}
				if err := validator.ValidateInput(child, input); err != nil {
					t.Errorf("step %s: input %s is invalid for %s: %v", s.ID, input, s.Action, err)
				}
			}
			if revertible >= 0 && revertible != len(steps)-1 {
				t.Errorf("the revertible step %s is not last", steps[revertible].ID)
			}
			if !slices.Equal(got, c.steps) {
				t.Errorf("steps %v, want %v", got, c.steps)
			}
		})
	}
}

// TestRgPortsStepWaitsForTheRuleID: the firewall step of each composite is the last, names the rule by the output of
// the step that opens or revokes the listener, and so is planned only after that step has applied.
func TestRgPortsStepWaitsForTheRuleID(t *testing.T) {
	reg := rgRegistry(t)
	for _, c := range rgComposites {
		t.Run(c.name, func(t *testing.T) {
			def, _ := reg.Action(core.ActionRef{Name: c.action, Version: 1})
			db := fdDB(t)
			fdInsertHost(t, db, rgHostID, true)
			if c.row != nil {
				fdInsertExposure(t, db, *c.row)
			}
			raw, _ := json.Marshal(c.input)
			steps, err := def.Handler.Steps(context.Background(), fdEnv(t, db, fdNewListeners(), core.CallContext{}), raw)
			if err != nil {
				t.Fatal(err)
			}
			if len(steps) == 0 {
				t.Fatal("the composite returned no steps")
			}
			last := steps[len(steps)-1]
			if last.ID != "ports" || last.Action.Name != "firewall.rules.apply" || !strings.Contains(string(last.Input), c.ruleRef) {
				t.Errorf("last step %s (%s) holds %s, want the ports step with %s", last.ID, last.Action, last.Input, c.ruleRef)
			}
		})
	}
}
```

Append this test to the end of `internal/modules/registry_test.go` (its imports already hold `bytes`, `strings`, `cli` and `cobra`):

```go
// TestPanelCLICommands is the parity test for the panel: every panel action but the internal connector check has its
// generated command, and listener revoke, the break-glass action, takes the controller socket's flags in place of
// --url and --host and refuses --local. The agent-side network commands take --host and the others --url.
func TestPanelCLICommands(t *testing.T) {
	var stdout, stderr bytes.Buffer
	root, err := cli.NewRoot(cli.Options{
		Registry: built(t), Stdout: &stdout, Stderr: &stderr,
		Commands: []*cobra.Command{{Use: "serve"}, {Use: "version"}},
	})
	if err != nil {
		t.Fatalf("cli.NewRoot: %v", err)
	}
	tests := []struct {
		action   string
		path     string
		socket   bool // the break-glass flags --socket and --dev instead of --url
		hostFlag bool // an agent-side command
	}{
		{"panel.exposure.add", "darkpanel panel exposure add", false, false},
		{"panel.exposure.remove", "darkpanel panel exposure remove", false, false},
		{"panel.exposure.status", "darkpanel panel exposure status", false, false},
		{"panel.listener.open", "darkpanel panel listener open", false, false},
		{"panel.listener.revoke", "darkpanel panel listener revoke", true, false},
		{"panel.network.create", "darkpanel panel network create", false, true},
		{"panel.network.remove", "darkpanel panel network remove", false, true},
	}
	for _, tc := range tests {
		cmd, ok := cli.FindCommand(root, tc.action)
		if !ok {
			t.Errorf("action %s has no generated command %s", tc.action, tc.path)
			continue
		}
		if cmd.CommandPath() != tc.path {
			t.Errorf("action %s: command %q, want %q", tc.action, cmd.CommandPath(), tc.path)
		}
		flags := cmd.Flags()
		hasSocket := flags.Lookup("socket") != nil && flags.Lookup("dev") != nil
		if hasSocket != tc.socket || (flags.Lookup("url") != nil) == tc.socket || (flags.Lookup("host") != nil) != tc.hostFlag {
			t.Errorf("%s: socket flags %v, url flag %v, host flag %v; want socket flags %v and host flag %v",
				tc.path, hasSocket, flags.Lookup("url") != nil, flags.Lookup("host") != nil, tc.socket, tc.hostFlag)
		}
	}
	if _, ok := cli.FindCommand(root, "panel.connector.check"); ok {
		t.Error("the internal action panel.connector.check has a generated command")
	}
	root.SetArgs([]string{"panel", "listener", "revoke", "--local"})
	if err := root.Execute(); err == nil || !strings.Contains(err.Error(), "--local reaches only this host's agent") {
		t.Errorf("darkpanel panel listener revoke --local: %v, want it refused because the action is not agent-side", err)
	}
}
```

Append this test to the end of `internal/modules/modules_test.go` (its imports already hold `core`):

```go
// TestPanelActionsAreRegistered shows that the registry registry.Build() returns holds the eight panel actions, each
// at version 1.
func TestPanelActionsAreRegistered(t *testing.T) {
	reg := built(t)
	for _, name := range []string{
		"panel.network.create", "panel.network.remove", "panel.connector.check", "panel.listener.open",
		"panel.listener.revoke", "panel.exposure.status", "panel.exposure.add", "panel.exposure.remove",
	} {
		if _, ok := reg.Action(core.ActionRef{Name: name, Version: 1}); !ok {
			t.Errorf("the registry has no %s", name)
		}
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w8-tmp/11 && export TMPDIR=/root/.cache/w8-tmp/11 GOTOOLCHAIN=local && timeout 600 go test -count=1 ./internal/modules/panel/ ./internal/modules/ 2>&1 | head -60`
Expected: FAIL. `TestRgManifestDeclaresTheActions` reports `panel.network.create is not declared` for every action; `TestRgManifestDeclaresTheRest` reports `migrations map[], want the controller side only`; `TestRgSnapshotsAreCommitted` reports `schemas/panel.network.create/v1.json: ... no such file or directory`; `TestPanelActionsAreRegistered` reports `the registry has no panel.network.create`; `TestPanelCLICommands` reports `action panel.exposure.add has no generated command` and `unknown command "panel"`. Quote the output in the report. The tests compile, because every constructor and wire type already exists.

- [ ] **Step 3: Register the actions**

1. Create `internal/modules/panel/actions.go` with `regActions(d agentDeps) []core.ActionDef` (a doc comment on it): it returns, in this order, `networkCreateAction(d)`, `networkRemoveAction(d)`, `connectorCheckAction(d)`, `listenerOpenAction()`, `listenerRevokeAction()`, `statusAction()`, `exposureAddAction()` and `exposureRemoveAction()`. Nothing else.
2. In `internal/modules/panel/panel.go` change `module` to `type module struct{ d agentDeps }`, `Module()` to return `module{d: productionAgent()}` (no Docker access happens before an agent action runs), and `Manifest()` to return `core.Manifest{ID: ID, Version: 1, DefaultEnabled: true, Actions: regActions(m.d), Migrations: map[core.Side]fs.FS{core.SideController: controllerMigrations()}}`. Update the `Manifest` doc comment: the manifest declares the eight actions and the controller migrations, and no resource or check. The package doc comment stays.
3. If `cli.NewRoot` refuses an input type of a panel action, or `registry.Build` refuses the module, report BLOCKED with the exact message and do not edit `internal/cli` or `internal/core`: both are frozen.

- [ ] **Step 4: Generate the snapshots**

Run: `mkdir -p /root/.cache/w8-tmp/11 && export TMPDIR=/root/.cache/w8-tmp/11 GOTOOLCHAIN=local && timeout 300 make schemas`
Then: `git status --short --untracked-files=all schemas`
Expected: exactly eight lines, `?? schemas/panel.<action>/v1.json` for the eight actions of Files, and no modified file. If any other file is listed, stop and report it with its `git diff`: an earlier task changed a published type, which wave 8 does not do. Commit the eight snapshots with this task.

- [ ] **Step 5: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w8-tmp/11 && export TMPDIR=/root/.cache/w8-tmp/11 GOTOOLCHAIN=local && timeout 600 go test -count=1 -v -run 'TestRg|TestPanel|TestSnapshotsCurrent|TestEveryActionHasACLICommand|TestRegistrationRules|TestAllListsTheModuleSkeletons' ./internal/modules/panel/ ./internal/modules/`
Expected: PASS for all of them, including `TestRgStepsMatchTheRegistry` (four subtests) and `TestPanelCLICommands`.

- [ ] **Step 6: Copy the plan rulings into the spec**

Two edits to `docs/dforge/specs/2026-10-21-v1-completion-design.md`, both in section 8, with the Edit tool; touch no other line.

1. The break-glass sentence of the Remove bullet. Replace the two lines

```text
  the removal to complete. Break-glass: `sudo darkpanel panel listener
  revoke --local` over the controller socket runs `panel.listener.revoke`
```

with

```text
  the removal to complete. Break-glass: `sudo darkpanel panel listener
  revoke` over the controller socket runs `panel.listener.revoke`
```

2. Insert the following bullet directly after the line ``  `panel.not_exposed` refuses a removal when no exposure exists.`` (the last line of section 8, followed by an empty line and `### 9. Hardening`), with an empty line between that line and the new bullet's first line, with the text exactly as given:

```markdown
- **Wave 8 rulings.** Wave 8 (plan `v1-w8-panel-exposure`) settles these
  gaps and underdetermined points of the text above:
  - **W8-R1 retirement.** `core.Step.Retires` names the exposure a step
    retires. The runner stores it per step
    (`core_composite_steps.retires`) and gives later steps `Retired` only
    once that step is `applied` or `confirmed`; only forward composites may
    set it.
  - **W8-R2 exposure id.** The exposure id is the `panel.exposure.add`
    change id: `panel.listener.open` sets `ListenerSpec.ChangeID` and the
    row id to `env.Call.ParentChangeID`, so the runtime handler's
    `Via.ExposureID` and the removal's `Retires` agree.
  - **W8-R3 exposure rows.** `panel_exposures` keeps one row per exposure.
    `open` becomes `revoked` (revoke); `open` and `revoked` become
    `needs_repair` (restore marking), and revoke makes `needs_repair`
    `revoked` again. The row is deleted only by the undo of
    `panel.listener.open`. An exposure is gone once a removal that revoked
    it has completed.
  - **W8-R4 port.** The setting `exposurePort` is the optional `port` input
    of `panel.exposure.add` (default 9443), recorded on the exposure; no
    settings store is added.
  - **W8-R5 pre-checks.** `via: npm` starts with the read step
    `npm.public.check`; `via: tunnel` starts with the agent internal read
    `panel.connector.check`, which refuses `cloudflare.connector_missing`
    unless `darkpanel-cloudflared-<tunnel>` exists, carries
    `darkpanel.module=cloudflare` and is running.
  - **W8-R6 claimant.** Both attachments use the claimant `panel`.
  - **W8-R7 rule id and ordering.** The firewall rule id is
    `RuleID(exposure id)`; the add's `ports` step takes it from `listen`'s
    output and the removal's from `revoke`'s, so the removal's firewall step
    is planned only after the listener is revoked and closed.
  - **W8-R8 break-glass command.** The break-glass path is the generated
    `sudo darkpanel panel listener revoke`, which talks to the controller
    socket; the wording above that named a `--local` form is replaced.
  - **W8-R9 network.** `panel.network.create` picks the highest /29 in
    `172.31.255.0/24` that overlaps no IPv4 subnet of any Docker network on
    the host, creates `darkpanel-panel` with `EnableIPv6` false and exactly
    that one IPAM config, and refuses `panel.already_exposed` when a network
    named `darkpanel-panel` exists. `panel.network.remove` refuses
    `docker.owned` for a `darkpanel-panel` network without
    `darkpanel.module=panel`.
  - **W8-R10 add refusals.** In `Steps`, in this order: input validation;
    `panel.wrong_host` unless `hostId` is the `is_local` host;
    `panel.totp_incomplete` while any `core_users` row is `pending`;
    `panel.already_exposed` while a live exposure exists.
  - **W8-R11 removal refusal.** `panel.not_exposed` when no live exposure
    exists; a `needs_repair` exposure is removable.
  - **W8-R12 step events.** A `composite.step` event of a composite the
    network listeners hide is not published.
  - **W8-R13 banner.** `web/src/modules/panel/banner.tsx` renders above
    every page from `panel.exposure.status`, linking to the controller
    host's panel page, which offers removal.
  - **W8-R14 attachments.** `cloudflare.network.attach` and `.detach`
    refuse `docker.owned` for a connector container without
    `darkpanel.module=cloudflare`, and attach refuses
    `request.invalid_input` when the connector is already on the network at
    an address other than the requested one, whether fixed or
    Engine-assigned.
  - **W8-R15 reservation release.** NPM's access-list and certificate
    releases delete only the releasing owner's `npm_reserved` rows.
  - **W8-R16 restore.** `backup.Mark` sets every live exposure to
    `needs_repair`; `backup.VerifyInstalled` fails when a live exposure is
    in another state.
```

Verify, with these commands from the repository root (`SPEC=docs/dforge/specs/2026-10-21-v1-completion-design.md`):

- `rg -c '^  - \*\*W8-R[0-9]+ ' $SPEC` prints `16`.
- `rg -n 'Wave 8 rulings' $SPEC` prints one line, inside section 8 (a line number between the `### 8.` and `### 9.` headings, which `rg -n '^### (8|9)\. ' $SPEC` gives).
- `rg -n 'revoke --local' $SPEC` prints nothing (exit status 1).
- `git diff -U0 -- $SPEC | rg '^-[^-]'` prints only the one replaced line (the line that held `revoke --local`), and `git diff --stat` lists only this file.

- [ ] **Step 7: Full checks and commits**

Run, each with its real output recorded: `mkdir -p /root/.cache/w8-tmp/11 && export TMPDIR=/root/.cache/w8-tmp/11 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 1500 go test -count=1 ./... && timeout 600 go test -race -count=1 ./internal/modules/panel/ ./internal/modules/ && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 300 go mod tidy -diff`
Then `rm -rf /root/.cache/w8-tmp/11 && go clean -testcache`.

Two commits, one concern each:

```bash
git add internal/modules/panel/actions.go internal/modules/panel/panel.go internal/modules/panel/panel_test.go internal/modules/modules_test.go internal/modules/registry_test.go schemas/panel.network.create schemas/panel.network.remove schemas/panel.connector.check schemas/panel.listener.open schemas/panel.listener.revoke schemas/panel.exposure.status schemas/panel.exposure.add schemas/panel.exposure.remove
git commit -m "feat(panel): register the panel actions"
git add docs/dforge/specs/2026-10-21-v1-completion-design.md
git commit -m "docs(spec): add the wave 8 rulings to section 8"
```

### Task 12: harness: TestM6Panel and the vm-m6p job

**Files:**
- Create: `test/harness/m6_panel_test.go` (flag, deadline, helpers and `TestM6Panel`)
- Test: `test/harness/m6_panel_unit_test.go`, `test/harness/m6pw_workflow_unit_test.go`
- Modify: `.github/workflows/harness.yml` (a new job `vm-m6p` between `vm-m6c` and `vm-m7r`; this task is the one that may edit this frozen file)
- Read: `test/harness/m6_npm_test.go` (the `m6nRun` steps `prepare`, `deploy`, `harnessSettings`, `listProxyHosts`, and `m6nRunCmd`, `m6nContainerIP`, `m6nPull`, `m6nHasDomain`, `m6nDiagnostic`), `test/harness/m6_cloudflare_test.go` (the flag, deadline and step-loop layout), `test/harness/m6cw_workflow_unit_test.go` (the workflow test this task follows; its helpers `m3dxExpoJob`, `m4lStep`, `m4lSetup` and `m4lJobMax` are reused), `test/harness/m5_derp_test.go` (`m5dLab`), `test/harness/m4_helpers_test.go` (`m4Plan`, `m4ApplyAsync`, `m4Settle`), `test/harness/api.go` (`API`, `NewAPI`, `localHost`), `test/harness/m1_test.go` (`targetIPv4`, `scannerIPv4`, `eventually`), `test/harness/m2_test.go` (`fleet`, `agentIPv4`, `controllerPriv`, `agentPriv`, `adminName`, `nextCode`, `login`), `internal/controller/coreactions/audit.go` (`AuditListInput`, `AuditList`, `AuditEntry`, `AuditInput`), `internal/controller/api/server.go` (`clientAddr`: a proxied request is recorded as `<client> via <proxy>`), `internal/controller/api/runtime.go` (the runtime listener requires exactly one `X-Forwarded-Proto: https` and reads only the one client header), `.github/workflows/harness.yml` (the `vm-m6c` job, the layout of the new job)

**Interfaces:**
- Consumes: Contracts "Wire types" (`panel.ExposeInput`, `ExposeOutput`, `StatusInput`, `StatusOutput`, `ExposureView`, `RemoveInput`, `RemoveOutput`), "Panel names" (`panel.ID`, `ViaNPM`, `NetworkName`, `BridgeName`, `BridgeOption`, `DefaultPort`, `PanelRange`, `StateOpen`); the actions Task 11 registers (`panel.exposure.add/v1`, `panel.exposure.status/v1`, `panel.exposure.remove/v1`); Task 2 and Task 3 (the NPM and Cloudflare residual fixes the exposure path needs, exercised here only through NPM); `coreactions.AuditList`, `AuditListInput`, `AuditEntry`, `AuditInput`, `MaxAuditLimit`; the harness helpers named under Files.
- Produces: `TestM6Panel` and the job `vm-m6p` that runs it (what the plan's gate 7 dispatches); `m6pFlag`, `m6pDeadline`; the pure helpers `m6pSpoofHeaders`, `m6pForgedAddrs`, `m6pCheckAudit`, `m6pParseReply`, `m6pCheckRefusal`, `m6pCheckPlain`, `m6pDirectRefused`, `m6pCheckExposed`, `m6pCheckNetwork`, `m6pCheckStatus`, `m6pForwardedProtoLine`, whose signatures are fixed by the unit tests below.

**Items:** 36

**Depends on:** Task 2, Task 3, Task 11
**Implementer:** dforge:impl-opus-high
**Evaluation:** files 2 - spec 2 - coupling 1 - risk 2 = 7

- [ ] **Step 1: Write the failing tests**

Create `test/harness/m6_panel_unit_test.go` with exactly this content. Every helper it names is a pure function of strings, JSON and wire values, so these tests run without a VM.

```go
//go:build harness

package harness

import (
	"net/http"
	"net/netip"
	"strings"
	"testing"

	"github.com/darkraise/darkpanel/internal/controller/coreactions"
	"github.com/darkraise/darkpanel/internal/modules/panel"
)

// m6puExposed is the output of a panel.exposure.add through NPM that took the highest free /29.
func m6puExposed() panel.ExposeOutput {
	return panel.ExposeOutput{
		ExposureID: "01JD0000000000000000000010", Via: panel.ViaNPM, Hostname: "panel.example.test", HostID: "h1",
		Subnet: netip.MustParsePrefix("172.31.255.248/29"), Gateway: netip.MustParseAddr("172.31.255.249"),
		Proxy: netip.MustParseAddr("172.31.255.250"), Port: panel.DefaultPort,
	}
}

func m6puInput() panel.ExposeInput {
	return panel.ExposeInput{Via: panel.ViaNPM, Hostname: "panel.example.test", HostID: "h1"}
}

func TestM6pUnitSpoofHeaders(t *testing.T) {
	h := m6pSpoofHeaders()
	for _, name := range []string{"X-Forwarded-For", "X-Real-Ip", "X-Darkpanel-Client", "X-Forwarded-Proto"} {
		if len(h.Values(name)) != 1 {
			t.Errorf("header %s has values %q, want one", name, h.Values(name))
		}
	}
	if len(h) != 4 {
		t.Errorf("%d headers, want the four the proxy path must not trust", len(h))
	}
	if got := h.Get("X-Forwarded-Proto"); got != "http" {
		t.Errorf("X-Forwarded-Proto %q, want http, the value a plain-HTTP client claims", got)
	}
	all := strings.Join([]string{h.Get("X-Forwarded-For"), h.Get("X-Real-Ip"), h.Get("X-Darkpanel-Client")}, " ")
	for _, addr := range m6pForgedAddrs {
		if !strings.Contains(all, addr) {
			t.Errorf("forged address %s is in no header", addr)
		}
		if _, err := netip.ParseAddr(addr); err != nil {
			t.Errorf("forged address %q: %v", addr, err)
		}
	}
}

// m6puLogin is the audit row of a login attempt of user from source; "" records none.
func m6puLogin(user, source string) coreactions.AuditEntry {
	e := coreactions.AuditEntry{Action: "core.auth.login", Phase: "login", Input: coreactions.AuditInput(`{"username":"` + user + `"}`)}
	if source != "" {
		e.Source = &source
	}
	return e
}

func TestM6pUnitCheckAudit(t *testing.T) {
	const proxy = "172.31.255.250"
	good := []coreactions.AuditEntry{
		m6puLogin("m6p-public", "198.18.0.20 via "+proxy),
		m6puLogin("m6p-private", "10.77.0.11 via "+proxy),
		m6puLogin("admin", "127.0.0.1"),
		m6puLogin("admin", "198.18.0.20 via "+proxy),
		{Action: "core.audit.list", Phase: "apply", Input: coreactions.AuditInput(`{"username":"m6p-plain"}`)},
	}
	want := map[string]string{"m6p-public": "198.18.0.20 via " + proxy, "m6p-private": "10.77.0.11 via " + proxy, "admin": "198.18.0.20 via " + proxy}
	tests := []struct {
		name    string
		entries []coreactions.AuditEntry
		want    map[string]string
		absent  []string
		err     string // "" means accepted
	}{
		{"every source recorded as itself", good, want, []string{"m6p-plain"}, ""},
		{"a second row of another source is fine", good, map[string]string{"admin": "127.0.0.1"}, nil, ""},
		{"no row of a source", good[:1], map[string]string{"m6p-private": "10.77.0.11 via " + proxy}, nil, "no login of m6p-private"},
		{"a source recorded as another", append([]coreactions.AuditEntry{m6puLogin("m6p-private", "198.18.0.20 via "+proxy)}, good[:1]...),
			map[string]string{"m6p-private": "10.77.0.11 via " + proxy}, nil, "m6p-private"},
		{"a row without a source", []coreactions.AuditEntry{m6puLogin("m6p-public", "")}, map[string]string{"m6p-public": "198.18.0.20 via " + proxy}, nil, "m6p-public"},
		{"the forged client header recorded", []coreactions.AuditEntry{m6puLogin("m6p-public", m6pForgedClient+" via "+proxy)},
			map[string]string{"m6p-public": m6pForgedClient + " via " + proxy}, nil, "forged address " + m6pForgedClient},
		{"the forged forwarded-for recorded", []coreactions.AuditEntry{m6puLogin("m6p-public", "203.0.113.99")}, nil, nil, "forged address 203.0.113.99"},
		{"a plain-HTTP login that reached the panel", append([]coreactions.AuditEntry{m6puLogin("m6p-plain", "198.18.0.20 via "+proxy)}, good...),
			want, []string{"m6p-plain"}, "m6p-plain"},
	}
	for _, tc := range tests {
		err := m6pCheckAudit(tc.entries, tc.want, tc.absent)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitParseReply(t *testing.T) {
	tests := []struct {
		name, out string
		want      m6pReply
		err       bool
	}{
		{"a status", `{"status":401,"body":"{\"code\":\"auth.invalid_credentials\"}"}` + "\n", m6pReply{Status: 401, Body: `{"code":"auth.invalid_credentials"}`}, false},
		{"an error", `{"error":"socket hang up"}`, m6pReply{Error: "socket hang up"}, false},
		{"noise before the line", "Unable to find image\n\n" + `{"status":200,"body":""}` + "\n", m6pReply{Status: 200}, false},
		{"no output", "", m6pReply{}, true},
		{"not JSON", "node: command not found\n", m6pReply{}, true},
		{"neither status nor error", `{"body":"x"}`, m6pReply{}, true},
	}
	for _, tc := range tests {
		got, err := m6pParseReply(tc.out)
		if (err != nil) != tc.err || got != tc.want {
			t.Errorf("%s: m6pParseReply = %+v, %v, want %+v (error %v)", tc.name, got, err, tc.want, tc.err)
		}
	}
}

func TestM6pUnitCheckRefusal(t *testing.T) {
	tests := []struct {
		name   string
		status int
		body   string
		err    bool
	}{
		{"the panel's refusal", 401, `{"type":"about:blank","status":401,"code":"auth.invalid_credentials"}`, false},
		{"NPM's bad gateway", 502, `<html>502 Bad Gateway</html>`, true},
		{"the panel's rate limit", 429, `{"code":"auth.rate_limited"}`, true},
		{"a 401 that is not the panel's", 401, `Unauthorized`, true},
		{"a login that succeeded", 200, `{"csrfToken":"x"}`, true},
		{"the refusal of a forged protocol", 403, `{"code":"forbidden"}`, true},
	}
	for _, tc := range tests {
		if err := m6pCheckRefusal(tc.status, []byte(tc.body)); (err != nil) != tc.err {
			t.Errorf("%s: m6pCheckRefusal = %v, want error %v", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitCheckPlain(t *testing.T) {
	const host = "panel.example.test"
	tests := []struct {
		name     string
		status   int
		location string
		err      bool
	}{
		{"forced SSL", http.StatusMovedPermanently, "https://panel.example.test/api/v1/auth/login", false},
		{"a temporary redirect", http.StatusTemporaryRedirect, "https://panel.example.test/", false},
		{"served", http.StatusOK, "", true},
		{"redirected to plain HTTP", http.StatusMovedPermanently, "http://panel.example.test/", true},
		{"redirected to another host", http.StatusMovedPermanently, "https://other.example.test/", true},
		{"not found", http.StatusNotFound, "", true},
		{"a redirect without a location", http.StatusMovedPermanently, "", true},
	}
	for _, tc := range tests {
		if err := m6pCheckPlain(tc.status, tc.location, host); (err != nil) != tc.err {
			t.Errorf("%s: m6pCheckPlain = %v, want error %v", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitDirectRefused(t *testing.T) {
	for out, wantErr := range map[string]bool{"000": false, "000\n": false, "200": true, "401": true, "": true} {
		if err := m6pDirectRefused(out); (err != nil) != wantErr {
			t.Errorf("m6pDirectRefused(%q) = %v, want error %v", out, err, wantErr)
		}
	}
}

func TestM6pUnitCheckExposed(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*panel.ExposeOutput, *panel.ExposeInput)
		id     string
		err    string // "" means accepted
	}{
		{"the highest free /29", func(*panel.ExposeOutput, *panel.ExposeInput) {}, "", ""},
		{"a port that was asked for", func(o *panel.ExposeOutput, in *panel.ExposeInput) { in.Port, o.Port = 9555, 9555 }, "", ""},
		{"an exposure id that is not the change", func(*panel.ExposeOutput, *panel.ExposeInput) {}, "01JD0000000000000000000099", "exposure id"},
		{"another hostname", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Hostname = "other.example.test" }, "", "does not echo"},
		{"the default port ignored", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Port = 8080 }, "", "does not echo"},
		{"a /28", func(o *panel.ExposeOutput, _ *panel.ExposeInput) {
			o.Subnet = netip.MustParsePrefix("172.31.255.240/28")
		}, "", "a /29"},
		{"a /29 outside the panel range", func(o *panel.ExposeOutput, _ *panel.ExposeInput) {
			o.Subnet = netip.MustParsePrefix("172.30.255.248/29")
			o.Gateway, o.Proxy = netip.MustParseAddr("172.30.255.249"), netip.MustParseAddr("172.30.255.250")
		}, "", "inside 172.31.255.0/24"},
		{"a gateway that is the network address", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Gateway = netip.MustParseAddr("172.31.255.248") }, "", "first and second"},
		{"a proxy that is the third address", func(o *panel.ExposeOutput, _ *panel.ExposeInput) { o.Proxy = netip.MustParseAddr("172.31.255.251") }, "", "first and second"},
	}
	for _, tc := range tests {
		out, in := m6puExposed(), m6puInput()
		tc.mutate(&out, &in)
		id := tc.id
		if id == "" {
			id = out.ExposureID
		}
		err := m6pCheckExposed(out, id, in)
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

// m6puInspect is docker network inspect of the panel network with NPM at the proxy address.
const m6puInspect = `[{"Name":"darkpanel-panel","EnableIPv6":false,
 "Labels":{"darkpanel.module":"panel"},"Options":{"com.docker.network.bridge.name":"dp-panel"},
 "IPAM":{"Driver":"default","Config":[{"Subnet":"172.31.255.248/29","Gateway":"172.31.255.249"}]},
 "Containers":{"abc":{"Name":"darkpanel-npm","IPv4Address":"172.31.255.250/29","IPv6Address":""}}}]`

func TestM6pUnitCheckNetwork(t *testing.T) {
	replace := func(from, to string) func(string) string {
		return func(doc string) string {
			if n := strings.Count(doc, from); n != 1 {
				t.Fatalf("the fixture holds %q %d times, want once", from, n)
			}
			return strings.Replace(doc, from, to, 1)
		}
	}
	tests := []struct {
		name string
		edit func(string) string
		err  string // "" means accepted
	}{
		{"the network as add creates it", func(doc string) string { return doc }, ""},
		{"another name", replace(`"Name":"darkpanel-panel"`, `"Name":"other"`), "network"},
		{"no module label", replace(`"darkpanel.module":"panel"`, `"darkpanel.module":"npm"`), "labels"},
		{"another bridge name", replace(`"dp-panel"`, `"br-1234"`), "options"},
		{"IPv6 on", replace(`"EnableIPv6":false`, `"EnableIPv6":true`), "IPv6"},
		{"a second IPAM config", replace(`"Gateway":"172.31.255.249"}]`, `"Gateway":"172.31.255.249"},{"Subnet":"fd00::/64"}]`), "IPAM"},
		{"another subnet", replace(`"Subnet":"172.31.255.248/29"`, `"Subnet":"172.31.255.240/29"`), "IPAM"},
		{"NPM at another address", replace(`"IPv4Address":"172.31.255.250/29"`, `"IPv4Address":"172.31.255.251/29"`), "darkpanel-npm is at"},
		{"NPM not attached", replace(`"Name":"darkpanel-npm"`, `"Name":"other"`), "not on the panel network"},
		{"not JSON", func(string) string { return "Error: No such network" }, "docker network inspect"},
	}
	for _, tc := range tests {
		err := m6pCheckNetwork([]byte(tc.edit(m6puInspect)), m6puExposed())
		switch {
		case tc.err == "" && err != nil:
			t.Errorf("%s: %v, want it accepted", tc.name, err)
		case tc.err != "" && (err == nil || !strings.Contains(err.Error(), tc.err)):
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.err)
		}
	}
}

func TestM6pUnitCheckStatus(t *testing.T) {
	view := func(mutate func(*panel.ExposureView)) panel.StatusOutput {
		o := m6puExposed()
		v := panel.ExposureView{ID: o.ExposureID, Via: o.Via, Hostname: o.Hostname, HostID: o.HostID, Subnet: o.Subnet, Gateway: o.Gateway,
			Proxy: o.Proxy, Port: o.Port, State: panel.StateOpen, ListenerID: "l1"}
		mutate(&v)
		return panel.StatusOutput{Exposed: true, Exposure: &v}
	}
	tests := []struct {
		name   string
		status panel.StatusOutput
		banner string
		err    bool
	}{
		{"the exposure the add made", view(func(*panel.ExposureView) {}), "Panel exposed via npm at panel.example.test", false},
		{"not exposed", panel.StatusOutput{}, "", true},
		{"exposed without a view", panel.StatusOutput{Exposed: true}, "", true},
		{"another exposure", view(func(v *panel.ExposureView) { v.ID = "01JD0000000000000000000099" }), "", true},
		{"another proxy address", view(func(v *panel.ExposureView) { v.Proxy = netip.MustParseAddr("172.31.255.251") }), "", true},
		{"a revoked listener", view(func(v *panel.ExposureView) { v.State = panel.StateRevoked }), "", true},
		{"no listener id", view(func(v *panel.ExposureView) { v.ListenerID = "" }), "", true},
	}
	for _, tc := range tests {
		banner, err := m6pCheckStatus(tc.status, m6puExposed())
		if (err != nil) != tc.err || banner != tc.banner {
			t.Errorf("%s: m6pCheckStatus = %q, %v, want %q (error %v)", tc.name, banner, err, tc.banner, tc.err)
		}
	}
}

func TestM6pUnitForwardedProtoLine(t *testing.T) {
	tests := []struct {
		name, out string
		err       bool
	}{
		{"NPM's include", "add_header X-Served-By $host;\nproxy_set_header Host $host;\nproxy_set_header X-Forwarded-Proto  $scheme;\nproxy_set_header X-Forwarded-For $remote_addr;\n", false},
		{"indented in a location", "  location / {\n    proxy_set_header X-Forwarded-Proto $scheme;\n  }\n", false},
		{"commented out", "# proxy_set_header X-Forwarded-Proto $scheme;\n", true},
		{"the client's value passed on", "proxy_set_header X-Forwarded-Proto $http_x_forwarded_proto;\n", true},
		{"a fixed value", "proxy_set_header X-Forwarded-Proto https;\n", true},
		{"only another header", "proxy_set_header X-Forwarded-For $remote_addr;\n", true},
		{"no output", "", true},
	}
	for _, tc := range tests {
		if err := m6pForwardedProtoLine(tc.out); (err != nil) != tc.err {
			t.Errorf("%s: m6pForwardedProtoLine = %v, want error %v", tc.name, err, tc.err)
		}
	}
}
```

Create `test/harness/m6pw_workflow_unit_test.go` with exactly this content. It follows `m6cw_workflow_unit_test.go`: one function checks the text of `harness.yml`, a table of single edits to a fixture shows that each rule can fail, and the second test runs the check on the committed file. The names carry `m6pYml` because gosec reads `pw` in an identifier as a password.

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
	m6pYmlRunStep = "Run the M6 panel harness"
	m6pYmlJob     = "vm-m6p"
	// m6pYmlStepTimeout is the -timeout in minutes of the TestM6Panel step and
	// m6pYmlJobLimit the job's timeout-minutes.
	m6pYmlStepTimeout = 50
	m6pYmlJobLimit    = 70
)

// m6pYmlCheck checks the text of harness.yml for TestM6Panel: the vm-m6p job
// has the vm jobs' setup, a timeout-minutes of m6pYmlJobLimit, and a step that
// runs the test with the lab flags, a work directory of its own, a -timeout of
// m6pYmlStepTimeout minutes and its failure logs; and none of the vm-m5, vm-m5d,
// vm-m6n, vm-m6c and vm-m7r jobs holds the test.
func m6pYmlCheck(yml string) error {
	for _, name := range []string{"vm-m5", "vm-m5d", "vm-m6n", "vm-m6c", "vm-m7r"} {
		other, ok := m3dxExpoJob(yml, name)
		if !ok {
			return fmt.Errorf("harness: the workflow has no %s job", name)
		}
		for _, text := range []string{"TestM6Panel", "harness-m6p", "-m6p"} {
			if strings.Contains(other, text) {
				return fmt.Errorf("harness: the %s job holds %s, which belongs to the %s job", name, text, m6pYmlJob)
			}
		}
	}

	job, ok := m3dxExpoJob(yml, m6pYmlJob)
	if !ok {
		return fmt.Errorf("harness: the workflow has no %s job", m6pYmlJob)
	}
	musts := []string{"needs: build", "runs-on: ubuntu-24.04", "fail-fast: false", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]"}
	for _, want := range append(musts, m4lSetup...) {
		if !strings.Contains(job, want) {
			return fmt.Errorf("harness: the %s job lacks %s", m6pYmlJob, want)
		}
	}
	limit := m3dxExpoLimitRE.FindStringSubmatch(job)
	if limit == nil {
		return fmt.Errorf("harness: the %s job has no timeout-minutes", m6pYmlJob)
	}
	if have, _ := strconv.Atoi(limit[1]); have != m6pYmlJobLimit || have > m4lJobMax {
		return fmt.Errorf("harness: the %s job has timeout-minutes %d, want %d", m6pYmlJob, have, m6pYmlJobLimit)
	}

	run, ok := m4lStep(job, m6pYmlRunStep)
	if !ok {
		return fmt.Errorf("harness: the %s job has no step named %q", m6pYmlJob, m6pYmlRunStep)
	}
	for _, want := range []string{
		"-run '^TestM6Panel$'", " -m6p", "-os ${{ matrix.os }}",
		`-bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"`,
		`-m2 "$GITHUB_WORKSPACE/bin/harness"`,
		`-commit "$GITHUB_SHA"`,
		`-workdir "$RUNNER_TEMP/harness-m6p"`,
		`-cache "$HOME/.cache/darkpanel-harness"`,
	} {
		if !strings.Contains(run, want) {
			return fmt.Errorf("harness: the step %q lacks %s", m6pYmlRunStep, want)
		}
	}
	m := m3dxExpoTimeoutRE.FindStringSubmatch(run)
	if m == nil {
		return fmt.Errorf("harness: the step %q has no -timeout <n>m", m6pYmlRunStep)
	}
	n, _ := strconv.Atoi(m[1])
	if n != m6pYmlStepTimeout {
		return fmt.Errorf("harness: the step %q has -timeout %sm, want %dm", m6pYmlRunStep, m[1], m6pYmlStepTimeout)
	}
	if time.Duration(n)*time.Minute < m6pDeadline+10*time.Minute {
		return fmt.Errorf("harness: the step %q has -timeout %sm, under the scenario deadline %s plus 10 minutes", m6pYmlRunStep, m[1], m6pDeadline)
	}
	for _, want := range []string{"name: logs-m6p-${{ matrix.os }}", "harness-m6p/**/*.log"} {
		if !strings.Contains(job, want) {
			return errors.New("harness: the " + m6pYmlJob + " job's failure upload lacks " + want)
		}
	}
	return nil
}

// m6pYmlDoc has the parts of harness.yml that m6pYmlCheck reads.
const m6pYmlDoc = `jobs:
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
  vm-m6p:
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
      - name: Run the M6 panel harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6Panel$' ./test/harness/
          -args -os ${{ matrix.os }} -m6p
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6p"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@x
        with:
          name: logs-m6p-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6p/**/*.log
  vm-m7r:
    needs: build
    timeout-minutes: 70
    steps:
      - name: Run the M7 restore harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM7Restore$' ./test/harness/
          -args -os ${{ matrix.os }} -m7r
`

func TestM6pwUnitWorkflow(t *testing.T) {
	if err := m6pYmlCheck(m6pYmlDoc); err != nil {
		t.Fatalf("the fixture: %v", err)
	}
	for _, tc := range []struct{ name, from, to, want string }{
		{"no vm-m6p job", "  vm-m6p:\n", "  vm-other:\n", "no vm-m6p job"},
		{"no vm-m6c job", "  vm-m6c:\n", "  vm-other:\n", "no vm-m6c job"},
		{"no vm-m7r job", "  vm-m7r:\n", "  vm-other:\n", "no vm-m7r job"},
		{"limit under 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 60", "timeout-minutes 60, want 70"},
		{"limit past the 120 minutes", "runs-on: ubuntu-24.04\n    timeout-minutes: 70", "runs-on: ubuntu-24.04\n    timeout-minutes: 130", "timeout-minutes 130, want 70"},
		{"no needs", "  vm-m6p:\n    needs: build\n", "  vm-m6p:\n    needs: other\n", "needs: build"},
		{"os matrix without debian-13", "os: [ubuntu-22.04, ubuntu-24.04, debian-12, debian-13]", "os: [ubuntu-22.04, ubuntu-24.04, debian-12]", "debian-13"},
		{"no KVM", "test -e /dev/kvm", "test -e /dev/none", "/dev/kvm"},
		{"no panel step", "Run the M6 panel harness", "Run M6 panel", `no step named "Run the M6 panel harness"`},
		{"wrong test", "'^TestM6Panel$'", "'^TestM6Other$'", "TestM6Panel"},
		{"no flag", " -m6p\n", "\n", "-m6p"},
		{"no fleet builds", "          -m2 \"$GITHUB_WORKSPACE/bin/harness\"\n", "", "-m2"},
		{"no commit", "          -commit \"$GITHUB_SHA\"\n", "", "-commit"},
		{"no image cache", "          -cache \"$HOME/.cache/darkpanel-harness\"\n", "", "-cache"},
		{"no workdir", "          -workdir \"$RUNNER_TEMP/harness-m6p\"\n", "", "-workdir"},
		{"workdir of vm-m6n", "-workdir \"$RUNNER_TEMP/harness-m6p\"", "-workdir \"$RUNNER_TEMP/harness-m6n\"", "harness-m6p"},
		{"short timeout", "-timeout 50m -v -run '^TestM6Panel$'", "-timeout 40m -v -run '^TestM6Panel$'", "-timeout 40m"},
		{"long timeout", "-timeout 50m -v -run '^TestM6Panel$'", "-timeout 60m -v -run '^TestM6Panel$'", "-timeout 60m"},
		{"no failure logs", "          path: ${{ runner.temp }}/harness-m6p/**/*.log\n", "", "harness-m6p/**/*.log"},
		{"artifact name of vm-m6c", "name: logs-m6p-${{ matrix.os }}", "name: logs-m6c-${{ matrix.os }}", "logs-m6p-"},
		{"test added to vm-m6c", "-run '^TestM6Cloudflare$'", "-run '^TestM6Cloudflare$|^TestM6Panel$'", "vm-m6c job holds TestM6Panel"},
		{"test added to vm-m6n", "-run '^TestM6NPM$'", "-run '^TestM6NPM$|^TestM6Panel$'", "vm-m6n job holds TestM6Panel"},
		{"flag added to vm-m7r", "-args -os ${{ matrix.os }} -m7r", "-args -os ${{ matrix.os }} -m7r -m6p", "vm-m7r job holds -m6p"},
		{"flag added to vm-m5d", "-args -os ${{ matrix.os }} -m5d", "-args -os ${{ matrix.os }} -m5d -m6p", "vm-m5d job holds -m6p"},
	} {
		if n := strings.Count(m6pYmlDoc, tc.from); n != 1 {
			t.Fatalf("%s: the fixture holds %q %d times, want once", tc.name, tc.from, n)
		}
		doc := strings.Replace(m6pYmlDoc, tc.from, tc.to, 1)
		if err := m6pYmlCheck(doc); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: %v, want an error naming %q", tc.name, err, tc.want)
		}
	}
}

// TestM6pwUnitWorkflowFile checks the committed workflow, which is the only
// place TestM6Panel runs.
func TestM6pwUnitWorkflowFile(t *testing.T) {
	b, err := os.ReadFile("../../.github/workflows/harness.yml")
	if err != nil {
		t.Fatal(err)
	}
	if err := m6pYmlCheck(string(b)); err != nil {
		t.Error(err)
	}
}
```

- [ ] **Step 2: Run the tests to verify they fail**

Run: `mkdir -p /root/.cache/w8-tmp/12 && export TMPDIR=/root/.cache/w8-tmp/12 GOTOOLCHAIN=local && timeout 600 go vet -tags harness ./test/harness/`
Expected: FAIL to build with `undefined: m6pSpoofHeaders` (the first identifier the unit tests use), also `undefined: m6pDeadline`. Quote the output in the report.

- [ ] **Step 3: Implement**

All new Go code is in `test/harness/m6_panel_test.go` (`//go:build harness`, package `harness`); every top-level identifier starts with `m6p`.

**A. Flag, deadline and constants.** `m6pFlag = flag.Bool("m6p", false, "run TestM6Panel; needs -os and -m2, and is a go test run of its own")`. `const m6pDeadline = 40 * time.Minute`, with the comment that the workflow step's `-timeout` must be at least this plus 10 minutes. Constants: `m6pHost = "panel.example.test"` (the public hostname; Pebble's DNS answers every name with NPM's address), `m6pClientName = "m6p-client"`, the login names `m6pUserPublic = "m6p-public"`, `m6pUserPrivate = "m6p-private"`, `m6pUserAttached = "m6p-attached"`, `m6pUserPlain = "m6p-plain"`, `m6pUserAfter = "m6p-after"`, and a wrong password and a wrong TOTP code that every refused login sends (`m6pWrongPassword = "wrong-password-0000"` and `m6pWrongCode = "000000"`; the password constant carries `//nolint:gosec // G101: a wrong password the test sends on purpose`). The forged values: `m6pForgedFor = "203.0.113.99, 198.51.100.7"`, `m6pForgedReal = "203.0.113.98"`, `m6pForgedClient = "203.0.113.97"` and `m6pForgedAddrs = []string{"203.0.113.99", "198.51.100.7", m6pForgedReal, m6pForgedClient}`.

**B. The pure helpers, pinned by the unit tests.** Error texts start with `harness:`. The words a test looks for in an error are given in quotes.

1. `m6pSpoofHeaders() http.Header`: the four headers `X-Forwarded-For` (`m6pForgedFor`), `X-Real-Ip` (`m6pForgedReal`), `X-Darkpanel-Client` (`m6pForgedClient`) and `X-Forwarded-Proto` (`http`), one value each, under their canonical names.
2. `m6pCheckAudit(entries []coreactions.AuditEntry, want map[string]string, absent []string) error`. It looks only at entries whose `Action` is `core.auth.login`, reads the user from the entry's recorded input (`{"username": ...}`; an undecodable input is an error), and takes a nil `Source` as "". It fails, first, on any entry whose source contains one of `m6pForgedAddrs`, naming "forged address <address>"; then, for each user of `want` in name order, when the user has no entry ("no login of <user>") or none of the user's sources equals the wanted one (the error names the user, the sources seen and the wanted source; other rows of the user, such as a loopback login, are fine); then, for each user of `absent`, when the user has any entry (the error names the user).
3. `m6pReply` is `struct{ Status int; Body string; Error string }` with the JSON names `status`, `body`, `error`. `m6pParseReply(out string) (m6pReply, error)` decodes the last line of the trimmed output; invalid JSON, or a reply with neither a non-zero status nor an error, is an error.
4. `m6pCheckRefusal(status int, body []byte) error` accepts exactly the panel's own refusal of a login: status 401 and a JSON body whose `code` is `auth.invalid_credentials`. Anything else, including NPM's 502 and the rate limit, is an error quoting the status and the start of the body.
5. `m6pCheckPlain(status int, location, host string) error` accepts a 301, 302, 307 or 308 whose `Location` parses to scheme `https` and the host `host`; otherwise an error quoting the status and the location.
6. `m6pDirectRefused(code string) error` accepts a curl `%{http_code}` of `000` (surrounding white space ignored) and refuses every other value.
7. `m6pCheckExposed(out panel.ExposeOutput, changeID string, in panel.ExposeInput) error`: the exposure id equals `changeID` ("exposure id"); via, hostname, host id, tunnel and port echo the input, the port being `panel.DefaultPort` when `in.Port` is 0 ("does not echo"); the subnet is valid, a /29, equal to its masked form and inside `panel.PanelRange` ("a /29" and "inside 172.31.255.0/24"); the gateway is the subnet address plus one and the proxy plus two ("first and second").
8. `m6pCheckNetwork(inspect []byte, out panel.ExposeOutput) error` decodes the output of `docker network inspect` (an array of exactly one network; invalid JSON is "docker network inspect: ..." and another count is "docker network inspect gave N networks") and requires: the name `panel.NetworkName` ("network"); the label `darkpanel.module` equal to `panel.ID` ("labels"); the option `panel.BridgeOption` equal to `panel.BridgeName` ("options"); `EnableIPv6` false ("IPv6"); exactly one IPAM config whose subnet and gateway are `out.Subnet` and `out.Gateway` ("IPAM"); and a container named `darkpanel-npm` (`m6nNPMName`) whose `IPv4Address`, a prefix, has the address `out.Proxy` ("darkpanel-npm is at" for another address, "not on the panel network" for none).
9. `m6pCheckStatus(st panel.StatusOutput, out panel.ExposeOutput) (string, error)`: `st.Exposed` is true with a non-nil exposure whose id, via, hostname, host id, tunnel, subnet, gateway, proxy and port equal those of `out`, whose state is `panel.StateOpen` and whose listener id is not empty; it returns the banner text `Panel exposed via <via> at <hostname>` (spec section 8) and a nil error, else "" and an error.
10. `m6pForwardedProtoLine(out string) error` accepts output holding at least one line that is exactly (leading and trailing white space ignored) `proxy_set_header X-Forwarded-Proto $scheme;` with any run of white space between the words, and refuses a commented line, a value other than `$scheme` and no line at all; the error quotes the `X-Forwarded-Proto` lines it did see.

**C. VM helpers (not unit tested; they are what the scenario is built from).**

1. `m6pController(f *fleet) *fleet` returns a copy of `*f` whose `agent` is `f.controller` and whose `agentID` is `f.localID`. The existing `m6nRun` steps then act on the controller's own host, where the panel must be exposed: a `m6nRun{ctx, f: m6pController(f)}` is `r.n` below. Only the original `f` calls `nextCode`, so the TOTP step bookkeeping (`lastCode`) stays in one place; `api` is the same pointer in both.
2. `m6pAnswer` is `struct{ Status int; Body []byte; Location string }`. `m6pRequest(ctx, from *vm.VM, scheme, addr, host string, roots *x509.CertPool, method, path string, body []byte, header http.Header) (m6pAnswer, error)` sends one request to `scheme://host<path>` over a connection that `from.DialContext(ctx, "tcp", addr)` opens (so the TCP peer NPM sees is `from`), with `DisableKeepAlives`, a 15-second timeout, no redirect followed (`CheckRedirect` returns `http.ErrUseLastResponse`), the body limited to 64 KiB, and for `https` the TLS configuration `RootCAs: roots` and `ServerName: host`, so the chain NPM serves is verified against Pebble's root. `header` is added over the request's own; a `Host` is the URL's.
3. `m6pLoginBody(user string) []byte` is the JSON `{"username", "password", "code"}` with the wrong password and code of A. `m6pExposureAPI(from *vm.VM, addr, host string, roots *x509.CertPool) *API` returns `&API{base: "https://" + host, http: &http.Client{Transport: ..., Jar: <a new cookiejar>, Timeout: apiTimeout}}`, the transport dialing as in `m6pRequest`; `NewAPI` cannot be used because it builds its own transport.
4. `m6pApplyThrough(ctx, t, via, settle *API, action string, input any) Change` plans the version 1 action with `m4Plan` and applies it with `via.do` exactly as `m4ApplyAsync` does (the confirmation phrase of the plan, if any, goes in the apply body), then settles the change with `m4Settle` through `settle`, and fails the test unless the change completed, quoting its error code and message. `via` and `settle` differ because the exposure `via` uses is gone when the removal ends.
5. `m6pClientScript` is a JavaScript constant run as `docker exec m6p-client node -e <script> <address> <port> <host> <user> <headers JSON>` in a container of the NPM image (the image has `node`; `process.argv.slice(1)` holds the five arguments). It sends one HTTPS POST to `/api/v1/auth/login` at `<address>:<port>` with SNI and `Host` `<host>`, certificate verification off, a 10-second timeout, the JSON body of `m6pLoginBody` for `<user>`, `Content-Type: application/json` and the headers of the JSON argument, and prints one line, `{"status": <code>, "body": "<text>"}`, or `{"error": "<code or message>"}` on any error or timeout. `m6pExec(ctx, t, v *vm.VM, address, port, host, user string) m6pReply` runs it with `m3Run` over `m3dxShell`-quoted arguments, passing the JSON of `m6pSpoofHeaders()`, and parses the output with `m6pParseReply`.
6. `m6pDiagnostics() []m6nDiagnostic` lists what a failed step logs from the controller's VM: `sudo docker ps -a` as a table of names, images and status, `sudo docker network ls`, `sudo docker network inspect darkpanel-panel`, `sudo ss -H -ltn`, `sudo ufw status verbose`, NPM's own answer (`curl -sS --max-time 10 http://127.0.0.1:81/api/`), the last 40 lines of the logs of `darkpanel-npm`, `m6n-pebble`, `m6n-challtestsrv` and `m6p-client`, and `sudo journalctl -u <update.ControllerUnit> -u <update.AgentUnit> --no-pager -n 80`.

**D. The scenario.** `TestM6Panel` skips unless `-os`, `-m2` and `-m6p` are given (`t.Skip("no -os, -m2 or -m6p given: the VM tests run in .github/workflows/harness.yml")`); it runs under `context.WithTimeout(context.Background(), m6pDeadline)`, builds `f := m5dLab(ctx, t)`, and `r := &m6pRun{ctx: ctx, f: f, n: &m6nRun{ctx: ctx, f: m6pController(f)}}`. `m6pRun` also holds `out panel.ExposeOutput`, `change string`, `x *API` (the session through the exposure), `roots *x509.CertPool` and `clientIP string`. It runs these steps in a loop like `TestM6NPM`'s (a failed step logs the diagnostics of C.6 with `m5dCapDiagnostic`; a failed gate step ends the test); the three step methods of `r.n` are reused unchanged:

| Step | Gate | What it does |
|---|---|---|
| `verify` | yes | See D.1. |
| `prepare` | yes | `r.n.prepare`: Pebble's configuration and CA, and the three images, on the controller's host. |
| `deploy` | yes | `r.n.deploy`: `npm.instance.deploy-public` on the local host. |
| `harness-settings` | yes | `r.n.harnessSettings`: NPM recreated with Pebble's directory, Pebble and its DNS server on NPM's network. Afterwards `r.roots` is a pool holding `r.n.root` (fail if the PEM does not parse). |
| `public` | yes | See D.2. |
| `expose` | yes | See D.3. |
| `login` | yes | See D.4. |
| `banner` | no | See D.5. |
| `spoofed-clients` | no | See D.6. |
| `plain-http` | no | See D.7. |
| `audit` | no | See D.8. |
| `remove-through-the-exposure` | yes | See D.9. |
| `after-removal` | no | See D.10. |

1. **verify** states and checks the five things the plan could not prove from the code. (a) The controller VM runs Docker: `sudo docker version --format '{{.Server.Version}}'` on `f.controller` prints a version. (b) Its local agent can deploy NPM: `f.api.Hosts` lists a local host whose id is `f.localID`, and `m4Plan(ctx, f.api, "", "npm.instance.deploy-public", npm.DeployPublicInput{HostID: f.localID, AcmeEmail: m6nAcmeEmail})` returns no error (the plan is never applied and expires). (c) NPM's templates pass the scheme on: `m6nPull` of `npm.Image` on the controller, then `sudo docker run --rm --entrypoint grep <npm.Image> -rhs X-Forwarded-Proto /app /etc/nginx` through `f.controller.Run` (an error from the command, including grep's exit status 1 for no match, is not fatal by itself: the output is what is checked), whose output `m6pForwardedProtoLine` must accept; log the matched lines with `t.Logf`. (d) The firewall can be changed on the controller's own host: `m4Plan(ctx, f.api, f.localID, "firewall.rules.apply", firewall.RulesApplyInput{Label: "m6p probe", Add: []model.Rule{{ID: "0M6PPR0BE00000000000000000", Action: "allow", Direction: "in", Proto: "tcp", From: "192.0.2.1", To: "any", ToPort: "9", Family: "v4"}}})` returns no error and a plan whose every guard is OK (the plan is never applied and expires). (e) The local agent keeps its database at the production path: `sudo test -f <paths.Production().AgentDB()>` on `f.controller` succeeds. The runtime listener refuses a request unless `X-Forwarded-Proto` arrives exactly once as `https`, so a template that passed the client's value on would turn the forged header of D.6 into a panel that serves plain HTTP; the spoofed requests of D.6 then also prove it end to end.
2. **public** is `m6nRun.public` for the controller's host: for ports 80 and 443 the scanner opens (and closes) a TCP connection to `net.JoinHostPort(targetIPv4, port)` within a minute (`eventually`), then `m4ApplyAsync(ctx, t, f.api, f.localID, "npm.instance.confirm-public", npm.ConfirmPublicInput{})`, then `npm.instance.status` of the local host reports `Public == "confirmed"`. (`m6nRun.public` itself dials `agentIPv4` and cannot be reused.)
3. **expose**: `in := panel.ExposeInput{Via: panel.ViaNPM, Hostname: m6pHost, HostID: f.localID}`; `ch := m4ApplyAsync(ctx, t, f.api, "", "panel.exposure.add", in)`; decode `ch.Output` into `r.out`; `m6pCheckExposed(r.out, ch.ID, in)`; keep `r.change = ch.ID`. Then check the host: `docker network inspect darkpanel-panel` on the controller (`m3dDocker`) passes `m6pCheckNetwork(…, r.out)`; within 30 seconds `sudo ss -H -ltn` lists a local address equal to `net.JoinHostPort(r.out.Gateway.String(), "9443")`; `sudo ufw status` contains `dp-panel` (the firewall rule; the failure message quotes the status); and `r.n.listProxyHosts(t)` holds a proxy host for `m6pHost` (`m6nHasDomain`).
4. **login**: `r.x = m6pExposureAPI(f.scanner, net.JoinHostPort(targetIPv4, "443"), m6pHost, r.roots)`. Within 3 minutes (`eventually`) a `m6pRequest` of `GET /healthz` through `f.scanner` with `https` answers 200, which shows NPM serves the certificate Pebble issued and reaches the panel. Then `r.x.Login(ctx, adminName, f.password, nextCode(ctx, t, f))` logs the admin in over the exposure; the TOTP check is the panel's, as everywhere.
5. **banner**: the banner's data is `panel.exposure.status`. Read it through the exposure, `r.x.Read(ctx, "", "panel.exposure.status/v1", struct{}{}, &st)`, and over the loopback API `f.api`; `m6pCheckStatus(st, r.out)` accepts both and returns `Panel exposed via npm at panel.example.test` (compared with that literal).
6. **spoofed-clients** sends one refused login with the forged headers of `m6pSpoofHeaders()` from each of three sources and requires `m6pCheckRefusal` of each answer, which shows the request reached the panel and was handled as an ordinary login. Public source: `m6pRequest` from `f.scanner` to `targetIPv4:443`, `POST /api/v1/auth/login`, `m6pLoginBody(m6pUserPublic)`, the spoof headers plus `Content-Type: application/json`. Private source: the same from `f.agent` to `net.JoinHostPort(controllerPriv, "443")` for `m6pUserPrivate`. Attached container: start `m6pClientName` on `f.controller` with `m3Run(m6nRunCmd(m6nRunSpec{Name: m6pClientName, Image: npm.Image, Network: panel.NetworkName, Entrypoint: "sleep", Args: []string{"3600"}}))`, `m3dWaitRunning`, set `r.clientIP = m6nContainerIP(ctx, t, f.controller, m6pClientName, panel.NetworkName)`, and `m6pExec` to `r.out.Proxy` port `443` for `m6pUserAttached`. Then the two direct attempts, which a peer other than the proxy must not get through: `sudo curl -sk -o /dev/null -w '%{http_code}' --max-time 10 -H 'X-Forwarded-Proto: https' -H 'X-Darkpanel-Client: 203.0.113.97' https://<gateway>:9443/healthz || true` run on `f.controller` must satisfy `m6pDirectRefused`; `m6pExec` from the container to `<gateway>` port `9443` must come back with a non-empty `Error` and status 0.
7. **plain-http**: through `f.scanner` to `targetIPv4:80` with scheme `http`, `GET /` and `POST /api/v1/auth/login` with `m6pLoginBody(m6pUserPlain)` both answer a redirect that satisfies `m6pCheckPlain(answer.Status, answer.Location, m6pHost)`: NPM forces SSL and the panel never sees a plain-HTTP request.
8. **audit**: read `core.audit.list/v1` through `f.api` with `coreactions.AuditListInput{Action: "core.auth.login", Limit: coreactions.MaxAuditLimit}` into a `coreactions.AuditList`; `m6pCheckAudit(list.Entries, want, []string{m6pUserPlain})` with `want` mapping `m6pUserPublic` and `adminName` to `scannerIPv4 + " via " + proxy`, `m6pUserPrivate` to `agentPriv + " via " + proxy` and `m6pUserAttached` to `r.clientIP + " via " + proxy`, `proxy` being `r.out.Proxy.String()`. The audit log thus records the true client address and the proxy's, and none of the forged addresses; the admin's loopback login rows are other rows of the same user and do not matter.
9. **remove-through-the-exposure**: `ch := m6pApplyThrough(ctx, t, r.x, f.api, "panel.exposure.remove", panel.RemoveInput{})`: the removal is planned and applied over the exposure (the guard context carries it as the request's path) and settled over the loopback API, because the exposure is gone when it ends. It must complete: its final firewall step is accepted only because the composite retired the exposure after `revoke`. Decode `ch.Output` into a `panel.RemoveOutput` equal to `{ExposureID: r.out.ExposureID, Hostname: m6pHost}`.
10. **after-removal** (each failure reported, the step not fatal): `panel.exposure.status` through `f.api` reports `Exposed` false; `docker network inspect darkpanel-panel` on the controller fails; `sudo ss -H -ltn` no longer lists the gateway's port 9443; `sudo ufw status` no longer contains `dp-panel`; `r.n.listProxyHosts(t)` holds no proxy host for `m6pHost`; and within a minute (`eventually`) a refused-login request for `m6pUserAfter` through the scanner no longer satisfies `m6pCheckRefusal` (it errors or NPM answers something else), which shows the panel is unreachable through the proxy.

**E. The job.** In `.github/workflows/harness.yml` insert this job directly above the line `  vm-m7r:` (so it follows `vm-m6c`), with one empty line after it, with the text exactly as given. It is `vm-m6c`'s job with the test, the flags, the work directory and the artifact name of this test and a limit of 70 minutes (the step's 50 plus 20 of setup).

```yaml
  vm-m6p:
    # The panel exposure test's job, beside vm-m6c. The limit is the step's
    # -timeout plus 20 minutes of setup. TestM6Panel boots a controller, an
    # agent and a scanner, runs NPM with Pebble on the controller's host, and
    # does not read -derper.
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
      # TestM6Panel exposes the panel through a real NPM, whose certificate
      # comes from Pebble, on the controller's own host: it sends forged client
      # headers from a public source, a private source and a container on the
      # panel network, reads the audit log, checks plain HTTP and the banner's
      # status read, and removes the exposure through the exposure itself. Its
      # own deadline is 40 minutes, and the extra 10 leave its VM cleanups time
      # to finish.
      - name: Run the M6 panel harness
        run: >-
          go test -tags harness -timeout 50m -v -run '^TestM6Panel$' ./test/harness/
          -args -os ${{ matrix.os }} -m6p
          -bin "$GITHUB_WORKSPACE/bin/darkpanel-linux-amd64"
          -m2 "$GITHUB_WORKSPACE/bin/harness"
          -commit "$GITHUB_SHA"
          -workdir "$RUNNER_TEMP/harness-m6p"
          -cache "$HOME/.cache/darkpanel-harness"
      - if: failure()
        uses: actions/upload-artifact@cf430e030ddbb5b0abf93d22962f4752f3646cd9 # v7.0.2
        with:
          name: logs-m6p-${{ matrix.os }}
          path: ${{ runner.temp }}/harness-m6p/**/*.log
          retention-days: 7
          if-no-files-found: ignore
```

- [ ] **Step 4: Run the tests to verify they pass**

Run: `mkdir -p /root/.cache/w8-tmp/12 && export TMPDIR=/root/.cache/w8-tmp/12 GOTOOLCHAIN=local && timeout 600 go vet -tags harness ./test/... && timeout 600 go test -tags harness -count=1 -run 'Unit' -v ./test/harness/ 2>&1 | rg -- '--- (PASS|FAIL): TestM6p'`
Expected: `PASS` for `TestM6pUnitSpoofHeaders`, `TestM6pUnitCheckAudit`, `TestM6pUnitParseReply`, `TestM6pUnitCheckRefusal`, `TestM6pUnitCheckPlain`, `TestM6pUnitDirectRefused`, `TestM6pUnitCheckExposed`, `TestM6pUnitCheckNetwork`, `TestM6pUnitCheckStatus`, `TestM6pUnitForwardedProtoLine`, `TestM6pwUnitWorkflow` and `TestM6pwUnitWorkflowFile`, and the other workflow unit tests (`TestM6cwUnitWorkflowFile` among them) still pass with the new job in the file. Also run `python3 -I -c "import yaml; d = yaml.safe_load(open('.github/workflows/harness.yml')); print(list(d['jobs']))"`: the list shows `vm-m6c`, `vm-m6p`, `vm-m7r` in that order.

`TestM6Panel` itself runs only in the `vm-m6p` job; locally it skips. State that in the report, and that its first step (D.1) is what proves the unverified assumptions of the plan's Assumptions list (Docker on the controller VM, the local agent deploying NPM and its database path, `firewall.rules.apply` on the controller's host, and `X-Forwarded-Proto`); nothing of it ran on a VM in this task.

- [ ] **Step 5: Full checks and commit**

Run, each with its real output recorded: `mkdir -p /root/.cache/w8-tmp/12 && export TMPDIR=/root/.cache/w8-tmp/12 GOTOOLCHAIN=local && timeout 600 go build ./... && timeout 300 go vet ./... && timeout 300 go vet -tags harness ./test/... && timeout 1500 go test -count=1 ./... && timeout 600 go test -tags harness -count=1 -run 'Unit' ./test/harness/... && timeout 900 golangci-lint run --allow-serial-runners ./... && timeout 600 golangci-lint run --allow-serial-runners --build-tags harness ./test/... && timeout 300 go mod tidy -diff`
Then `rm -rf /root/.cache/w8-tmp/12 && go clean -testcache`.

```bash
git add test/harness/m6_panel_test.go test/harness/m6_panel_unit_test.go test/harness/m6pw_workflow_unit_test.go
git commit -m "test(harness): add TestM6Panel"
git add .github/workflows/harness.yml
git commit -m "chore(ci): add the vm-m6p harness job"
```

### Task 13: server: hide step events of hidden composites

**Files:**
- Modify: `internal/controller/server/services.go` (`engineConfig`; one new method)
- Test: `internal/controller/server/services_test.go`
- Read only: `internal/controller/api/actions.go` (`NetworkVisible`), `internal/controller/events/bus.go`, `internal/core/lifecycle` (`EventCompositeStep`, `StepEvent`)

**Interfaces:**
- Consumes: nothing from another task; `api.NetworkVisible(reg, view)` already exists.
- Produces: Contracts "Step event visibility" (the engine's `Publish` drops the `composite.step` events of a change the network listeners hide).

**Items:** 91

**Depends on:** none
**Implementer:** dforge:impl-sonnet-high
**Evaluation:** files 1 - spec 2 - coupling 0 - risk 2 = 5

This is an integrator task: Global Constraints allow exactly these edits in `internal/controller/server/**` (W8-R12).

- [ ] **Step 1: Write the failing test**

In `internal/controller/server/services_test.go` append the following, and in `TestServerEventWiring` add the line `svStepChange(t, s, "C1", "core.module.disable")` immediately before the line `step := lifecycle.StepEvent{ChangeID: "C1", ...}`: the wiring test publishes a step event for change `C1`, which must now exist as a visible change, or the new filter drops the event.

```go
// svStepChange stores a controller change of action, the row a composite's step events name. Its times are old, so
// the change watcher never publishes it.
func svStepChange(t *testing.T, s *Server, id, action string) {
	t.Helper()
	const old = "2020-01-01T00:00:00.000000000Z"
	exec(t, s, `INSERT INTO core_changes (id, plan_id, host_id, action, version, class, compensated_by, actor, state,
		input_json, created_at, updated_at)
		VALUES (?, ?, '', ?, 1, 'compensable', '', 'local:root@h', 'completed', '{}', ?, ?)`,
		id, planDisable(t, s).PlanID, action, old, old)
}

// TestSvStepEventsHideBreakGlassComposites: the engine's Publish drops the composite.step events of a change the
// network listeners hide, and of a change it cannot read, and passes every other event on unchanged.
func TestSvStepEventsHideBreakGlassComposites(t *testing.T) {
	s := start(t, embeddedOptions(t))
	ch, _ := s.events.Subscribe(t.Context())
	const hidden, shown, missing = "01SVHIDDEN0000000000000000", "01SVSHOWN00000000000000000", "01SVMISSING000000000000000"
	svStepChange(t, s, hidden, "core.user.reset-password")
	svStepChange(t, s, shown, "core.module.disable")

	publish := s.engineConfig(s.registry).Publish
	for _, id := range []string{missing, hidden, shown} {
		publish(lifecycle.EventCompositeStep, lifecycle.StepEvent{ChangeID: id, StepID: "s1", State: core.StepApplied})
	}
	publish(events.TypeHost, events.HostEvent{HostID: "h1", Online: true})

	pubWant(t, ch, events.TypeCompositeStep, lifecycle.StepEvent{ChangeID: shown, StepID: "s1", State: core.StepApplied})
	pubWant(t, ch, events.TypeHost, events.HostEvent{HostID: "h1", Online: true})
}
```

- [ ] **Step 2: Run the test to verify it fails**

```bash
mkdir -p /root/.cache/w8-tmp/13 && export TMPDIR=/root/.cache/w8-tmp/13 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestSvStep|TestServerEventWiring' ./internal/controller/server/
```

Expected: `TestSvStepEventsHideBreakGlassComposites` fails with `composite.step event = {"changeId":"01SVMISSING000000000000000",...}, want {"changeId":"01SVSHOWN00000000000000000",...}`. Quote the line in the report.

- [ ] **Step 3: Implement**

In `internal/controller/server/services.go`:
   - Add the method `func (s *Server) publishEngineEvent(eventType string, data any)`. When `eventType` is `lifecycle.EventCompositeStep` and `data` is a `lifecycle.StepEvent`, read the composite's change with `s.engine.Change(context.Background(), step.ChangeID)` and drop the event (return) when that fails or when `s.networkVisible(view)` is false; log the read failure at `Warn` as `server: composite step event dropped; its change is unreadable` with the change id and the error (a hidden change is dropped silently). Every other event, and a composite step event of a visible change, goes to `s.events.Publish(eventType, data)` unchanged.
   - `engineConfig` is called before `s.engine` is assigned, so the method must read `s.engine` when an event arrives; never capture it in `engineConfig`.
   - In `engineConfig` set `Publish: s.publishEngineEvent` and update its doc comment (it now filters).

- [ ] **Step 4: Run the tests to verify they pass**

```bash
mkdir -p /root/.cache/w8-tmp/13 && export TMPDIR=/root/.cache/w8-tmp/13 GOTOOLCHAIN=local
timeout 600 go test -count=1 -run 'TestSv|TestServerEventWiring|TestServerEventsHideBreakGlass' ./internal/controller/server/
timeout 300 go build ./... && timeout 300 go vet ./...
timeout 900 go test -count=1 ./...
timeout 900 go test -race -count=1 ./internal/controller/server/
timeout 900 golangci-lint run --allow-serial-runners ./...
timeout 300 go mod tidy -diff
go clean -testcache && rm -rf /root/.cache/w8-tmp/13
```

Expected: every package prints `ok` or `?`, no build or vet output, `0 issues.`, and nothing from `go mod tidy -diff`.

- [ ] **Step 5: Commit**

```bash
git add internal/controller/server/services.go internal/controller/server/services_test.go
git commit -m "fix(server): hide step events of hidden composites"
```

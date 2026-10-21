# v1 completion: firewall, Docker, Tailscale, exposure, hardening and release

**Date:** 2026-10-21
**Status:** design approved by the owner section by section on 2026-10-21;
written spec awaiting the owner's review
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R37–R81; M2 register rows 11, 16 and 35
(docs/superpowers/registers/2026-10-19-m2-fleet-lifecycle-core.md); the
controller backup and restore deferred by ADR 0005 to M8
**Execution strategy:** parallel — rolling wave plans. Wave 0 (core
extensions) fixes every shared file, interface, dependency, error code and
module skeleton; safety-critical waves (0, 1, 2, 5, 7) run alone; waves 3, 4
and 6 run their parts in parallel worktrees. Each wave's plan is written when
the previous wave has merged (owner standing preference: favour parallel to
reach v1 sooner).

## Goal

Build everything v1 still lacks, up to a v1.0.0-rc.1 that is ready to tag with
every gate green, and collect every step only the owner can take into one
checklist (section 12). M9 post-v1 candidates are out of scope.

## Owner decisions (2026-10-21)

- One spec covers every open v1 item. Plans roll: one plan per wave, written
  after the previous wave merges, executed and merged into `main` locally
  without stopping except at owner-only steps.
- Integrations with outside services (Tailscale's cloud API, Cloudflare,
  Let's Encrypt through Nginx Proxy Manager, DERP reachability) are built and
  tested against in-process fake servers. Live demonstrations run once, as a
  gated step before v1.0, when the owner provides credentials.
- DERP runs as a signed derper artifact: the release workflow builds upstream
  `tailscale.com/cmd/derper` at a pinned version, signs it with the release
  minisign key, and the agent installs it as a systemd service.
- Approach: core extensions first and alone, then the firewall's safe apply
  alone, then modules in parallel waves by dependency; hardening and release
  last.
- Cross-module calls, agent background tasks, streams, hand-written API
  clients and trusted proxies as in section 1.
- During this programme Claude may push wave branches and `main` to origin and
  dispatch `harness.yml`; tags and releases need the owner.

## Versions

| Component | Version | Note |
|---|---|---|
| Docker Engine API | 1.44 minimum (Docker 25) | `docker` preflight refuses older |
| Docker firewall backend field | `/info` `FirewallBackend`, API 1.50+ | absent on older daemons, which only have the iptables backend |
| UFW | as shipped by each supported OS (0.36.1 on Ubuntu 22.04 through 0.36.2 on Debian 13) | rule comments need 0.35+ |
| derper | the `tailscale.com` version pinned in `release/derper.version` | bumped by a commit; each Darkpanel release ships one |
| Nginx Proxy Manager | `jc21/nginx-proxy-manager` pinned by digest in `internal/modules/npm/image.go` | |
| cloudflared | `cloudflare/cloudflared` pinned by digest in `internal/modules/cloudflare/image.go` | |

Pinned digests and the derper version are confirmed and recorded by the first
task of the wave that introduces them.

## Design

### 1. Core extensions (wave 0)

#### 1.1 Cross-module calls

A controller-side action may run other modules' actions as **child changes**.
`core.Env` gains `Children ChildRunner`, set only for controller-side actions:

```go
type ChildRunner interface {
    // Plan plans ref on hostID ("" for a controller-side child) and returns
    // the child's plan; the parent stores child plan ids in its own plan.
    Plan(ctx context.Context, hostID string, ref ActionRef, input any) (ChildPlan, error)
    // Apply applies a planned child; revertible children stay unconfirmed.
    Apply(ctx context.Context, child ChildPlan) (json.RawMessage, error)
}
```

- **Plan.** The parent's `Plan` plans every child. The parent's diff is the
  concatenation of the children's diffs under one heading per child; the
  parent's guards are the union of the children's guards. A refused child
  guard refuses the parent plan. Child plans expire with the parent's.
- **Apply.** The parent's `Apply` applies children in plan order. A child that
  is revertible is left unconfirmed; after the parent's own `Verify` passes,
  the core confirms every revertible child. When a child fails, the core
  reverts the revertible children already applied and runs the compensating
  action of each compensable child already applied, in reverse order, then
  fails the parent with the first error. A composite may not plan an
  irreversible child: `Children.Plan` refuses one with `core.child_failed`.
- **Records.** Each child is an ordinary change with `parent_change_id` set
  (new nullable column in both `core_changes` tables, additive migration). The
  audit log shows the parent and its children. Locks, snapshots and journal
  stay with the owning module.
- **Class.** A composite action is `compensable`, compensated by its declared
  inverse (for example `npm.expose.remove`).

#### 1.2 Agent background tasks

Optional module interface on the agent side:

```go
type BackgroundTask struct {
    ID    string        // "firewall.reconcile"; actor system:<ID>
    Every time.Duration // 0 means event-driven only
    Run   func(ctx context.Context, env Env, wake <-chan struct{}) error
}
type Background interface{ BackgroundTasks() []BackgroundTask }
```

The agent runtime starts tasks after startup recovery, restarts a task that
returns an error with capped backoff (1 s to 5 min), and stops them before
shutdown. A task mutates the host only through the module's own action
handlers with lock keys taken, as actor `system:<ID>`, and its changes are
applied and confirmed in one step (they restore state the owner already
confirmed). Tasks never run while an update reservation holds their lock keys.

#### 1.3 Streams

- Agent endpoint `GET /agent/v1/streams/{module}/{name}?<params>` over a yamux
  stream the controller opens; chunked JSON lines; closed by either side. It is
  additive to protocol version 1 (frozen parts are untouched) and advertised in
  `hello` as `streams: [...]` with a schema hash per stream.
- A module declares streams in its manifest:
  `StreamDef{Name, ParamsType, EventType, Open}`.
- Controller relay: `GET /api/v1/hosts/{host}/streams/{module}/{name}` as
  server-sent events, behind the same authentication and scope (`read`)
  middleware; at most 8 open streams per session; idle timeout 10 minutes.
- CLI: `darkpanel <module> <stream> --follow` prints events as lines.
- Streams in v1: `docker.logs`, `docker.stats`, `docker.pull` (keyed by change
  id).

#### 1.4 Guard context

Lockout guards need to know which connections must survive. The dispatcher
passes `Env.Guard GuardContext` to agent plans:

```go
type GuardContext struct {
    ClientAddr      netip.Addr       // the browser or CLI's address at the controller, if any
    PanelListeners  []netip.AddrPort // the controller's web listeners, when this host runs the controller
    AgentListeners  []netip.AddrPort // the controller's agent listeners, when this host runs the controller
    AgentPeers      []netip.Addr     // source addresses of connected agents, when this host runs the controller
    ControllerAddr  netip.AddrPort   // the address this agent dials
}
```

The agent fills `ControllerAddr` itself; the controller fills the rest. Local
break-glass plans have only `ControllerAddr`. The context is stored with the
plan so apply re-checks the same facts.

#### 1.5 Host clients (`internal/platform`)

- **Exec allowlist** grows to: `/usr/sbin/ufw`, `/usr/sbin/iptables`,
  `/usr/sbin/ip6tables`, `/usr/sbin/iptables-save`, `/usr/sbin/ip6tables-save`,
  `/usr/bin/apt-get`, `/usr/bin/docker` (Compose plugin only, output never
  parsed), `/usr/bin/systemctl`, `/usr/bin/tar`. Values reaching them are
  constrained by schema patterns and separated with `--` where supported.
- **Docker Engine client** (`internal/platform/dockerapi`): HTTP over
  `/var/run/docker.sock`, API version negotiated at 1.44+, typed calls for
  the endpoints sections 3 and 6 use, streaming for logs, stats, pull and
  events. Hand-written; no Docker SDK.
- **Tailscale local client** (`internal/platform/tslocal`): HTTP over
  `/var/run/tailscale/tailscaled.sock` for status, prefs (get and edit),
  login with an auth key, and logout. Hand-written; no Tailscale dependency.
- **Socket table** (`internal/platform/netstat`): listening TCP and UDP sockets
  from `/proc/net/{tcp,tcp6,udp,udp6}` joined to owning processes through
  `/proc/<pid>/fd`, and established sockets for the SSH-session guard.
- **Restorable files.** A new owned-path kind `restore` (ADR 0014): a system
  file the module may overwrite only with bytes previously snapshotted from the
  same path, and which uninstall never removes. The firewall module declares
  UFW's state files this way (section 2.2).

#### 1.6 Cloud plumbing (controller)

- `internal/controller/cloud`: one HTTP client with 15 s timeouts, retry with
  backoff on 429 and 5xx (at most 3 attempts, honouring `Retry-After`), and
  errors mapped to the codes in section 11.
- **Accounts.** A module stores account credentials in its own table with the
  secret column sealed (ADR 0005, associated data
  `<table>.secret.<row id>`). Credentials are write-only through the API.
- **Fakes.** `internal/controller/cloud/fakes` provides in-process servers for
  the Tailscale API, Cloudflare API and Nginx Proxy Manager API with recorded
  request and response shapes, used by every unit test. Each fake records the
  requests it served so tests assert on them.

#### 1.7 Trusted proxies and runtime listeners

- Controller setting `trustedProxies` (list of addresses). `X-Forwarded-For`
  and `X-Forwarded-Proto` are honoured only on a connection from one of them;
  the audit `source` and the login rate limiter use the forwarded address then.
- The controller can open and close **runtime listeners** recorded in its
  database (`core_listeners`: id, address, purpose, created by change), in
  addition to the command-line ones, without a restart. Panel exposure
  (section 7.3) is the only user in v1. Runtime listeners are HTTPS only and
  accept connections only from their recorded allowed peer.

#### 1.8 Module skeletons and shared files

Wave 0 creates every new module package with an empty manifest and registers
it: `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`,
`cloudflare`, `panel`. It adds their lines to `internal/modules/modules.go`
and `web/src/modules/registry.ts`, every error code of section 11, every Go
and pnpm dependency, and the allowlist. Every module is default-enabled; preflight decides
whether it is available on a host (R9). Later waves never edit these files;
a wave that finds a missing code or dependency stops and the integrator adds
it on `main` before the wave continues.

#### 1.9 ADR 0014

ADR 0014 records 1.1–1.7, the `restore` owned-path kind, DERP as a signed
derper artifact under systemd, and `cloudflared` as a container (closing the
two deferred decisions in `docs/adr/README.md`).

### 2. Firewall (R37–R49)

Module `firewall`; agent side; lock key `firewall`; default enabled; preflight:
`/usr/sbin/ufw` present and `ufw` version ≥ 0.35.

#### 2.1 Rule model and UFW management (R37, R38)

- **Reading.** Status, default policies and logging come from
  `ufw status verbose`; rules come from `ufw status numbered` joined with
  `/etc/ufw/user.rules` and `user6.rules` (the `### tuple ###` lines carry the
  exact rule including comments). The parsers are fuzzed (R74).
- **Model.** `Rule{ID, Number, Action (allow|deny|limit|reject), Direction
  (in|out|route), Proto, FromAddr, FromPort, ToAddr, ToPort, InIface,
  OutIface, Family (v4|v6|both), Comment, Origin (darkpanel|manual)}`.
- **Tag.** Darkpanel rules carry the comment `darkpanel:<ulid>` optionally
  followed by ` <label>`. Rules without the tag are `manual`; deleting one
  requires `manual: true` in the input and is shown as such in the diff.
- **Writing** only through `ufw` with argument vectors.

Resources: `firewall.status` (enabled, defaults, logging, rules).
Actions (all `revertible`, agent, lock `firewall`):

| Action | Input |
|---|---|
| `firewall.enable/v1`, `firewall.disable/v1` | none |
| `firewall.defaults.set/v1` | incoming, outgoing, routed: allow\|deny\|reject |
| `firewall.logging.set/v1` | off\|low\|medium\|high\|full |
| `firewall.rule.add/v1` | the rule model's writable fields, optional `position` |
| `firewall.rule.delete/v1` | `id`, or `number` plus `manual: true` |

#### 2.2 Safe apply (R39–R42)

- **Preview (R39).** Plan reads the current model, applies the requested
  operation to a copy of the model in Go, and renders the diff of the two rule
  lists, defaults and logging. The state hash covers the parsed model.
  Verify re-reads UFW after apply and compares it with the predicted model;
  any difference fails verify, which reverts (ADR 0004).
- **Lockout guard (R40).** A pure function
  `Evaluate(model, flows) []Guard` over a Go model of UFW's evaluation for new
  connections: loopback accepted, then user rules in order (first match;
  `limit` counts as allow for one new connection), then the default policy.
  `ufw` disabled means everything is allowed. The flows checked:
  1. each established TCP connection to a local port owned by `sshd`: a new
     connection from its remote address to that port;
  2. when `PanelListeners` is set: from `ClientAddr` to each panel listener;
  3. when `AgentListeners` is set: from each `AgentPeers` address to each agent
     listener;
  4. outbound from this host to `ControllerAddr` (outgoing policy and `out`
     rules).
  A blocked flow fails the plan with code `firewall.lockout_refused` naming the
  flow. Input field `overrideLockout: true` turns the refusal into a passing
  guard with the warning kept in the diff and the audit entry.
- **Auto-revert (R41).** Every firewall action is revertible with the agent's
  revert timer. Setting `revertTimeoutSeconds` (default 60, 30–600) is the
  module's per-host setting. The UI shows a countdown with Confirm and Revert;
  the CLI's `--confirm` waits for a keypress or `--yes`.
- **Snapshots (R42).** The snapshot is a tar of `/etc/ufw/user.rules`,
  `user6.rules`, `before.rules`, `before6.rules`, `after.rules`,
  `after6.rules`, `/etc/ufw/ufw.conf` and `/etc/default/ufw`, each declared as a
  `restore` owned path. Revert writes them back with the file provider and runs
  `ufw reload` (or `ufw disable` when the snapshot was disabled). Snapshots are
  kept in the agent database: the last 50, plus any referenced by an
  unconfirmed change.
  - Resource `firewall.snapshots` lists them (id, time, change, actor).
  - `firewall.snapshot.compare/v1` (class `read`) diffs two snapshots, or one
    against the current state.
  - `firewall.snapshot.restore/v1` (revertible) restores one; it is
    break-glass, so `sudo darkpanel firewall snapshot restore --local` works
    without the controller. `darkpanel firewall rollback --local` restores the
    snapshot taken before the newest change and confirms it in the same
    command.

#### 2.3 Docker integration (R43–R47)

- **Detection (R43).** Resource `firewall.docker`: Docker firewall backend
  from `/info` (`iptables` when the field is absent), iptables variant from
  `iptables --version` (`nf_tables` or `legacy`), UFW's own variant (the same
  binary), block state. Supported: backend `iptables` and Docker's variant
  equal to UFW's. Otherwise `supported: false` with the reason (for example
  "Docker uses its nftables firewall backend; not supported in v1").
- **Block (R44).** `firewall.docker.enable/v1` (revertible) writes a marked
  block `docker` into `/etc/ufw/after.rules` and `after6.rules` and reloads UFW.
  The block, written from the documented behaviour of Docker's `DOCKER-USER`
  chain and UFW's chains (no code or text from GPL ufw-docker):
  1. `DOCKER-USER` jumps to `ufw-user-forward` first, so route rules decide;
  2. established and related traffic returns;
  3. traffic from the configured private ranges returns (setting
     `privateRanges`, default the RFC 1918 ranges, 100.64.0.0/10,
     fd00::/8 and fd7a:115c:a1e0::/48);
  4. new inbound TCP and UDP to each Docker network subnet (read from the
     Engine API when the block is written) jumps to a logging deny chain;
  5. everything else returns.
  Container-to-container and container-outbound traffic never match step 4
  because it matches only packets entering from a non-Docker interface
  (R46). `firewall.docker.disable/v1` removes the block. A subnet added later
  triggers the reconcile task (below).
- **Container rules (R45).** `firewall.container.allow/v1` and
  `firewall.container.deny/v1` (revertible) take `{container (name or
  compose project/service), port, proto, from (optional CIDR)}`. Desired state
  lives in `firewall_container_rules`; each row is realised as a UFW route rule
  `allow|deny proto <p> from <from> to <container ip> port <port>` with the
  comment `darkpanel:<row id> container:<name>`, one per container address
  family.
- **Background task `firewall.reconcile`.** Wakes on Docker events (container
  start, network connect and disconnect, network create) and every 5 minutes.
  It recomputes route rules from `firewall_container_rules` and current
  container addresses, and rewrites the block when the subnet set changed,
  through the same handlers (actor `system:firewall.reconcile`).
- **Health (R47).** The same task checks that the block is present in both
  files and loaded (`iptables-save -t filter` shows the `DOCKER-USER` jump).
  A missing or unloaded block sets `firewall.docker.health` to `broken` with
  the reason; the UI offers `firewall.docker.repair/v1` (revertible), which
  rewrites and reloads.

#### 2.4 Exposure view (R48, R49)

Resource `firewall.exposure`: one row per listening socket and per Docker
published port, per family: `{proto, address, port, process or container,
internet, tailnet, localhost, decidedBy}`. Each verdict is `open`, `closed` or
`filtered`, with `decidedBy` naming the UFW rule number and tag, the default
policy, the Docker block step, or "bound to loopback". The computation reuses
the guard's evaluation model plus the Docker block and Docker's own published
port binding rules (a port published on 127.0.0.1 is localhost-only). The
tailnet verdict evaluates traffic arriving on `tailscale0` from 100.64.0.0/10.

#### 2.5 Uninstall (R30)

The firewall `Uninstaller` takes `keepData` plus a module option
`firewallRules: keep|revert`. `revert` restores the snapshot taken before the
first Darkpanel firewall change on the host; `keep` deletes nothing but
Darkpanel's tags stay. Either way the `docker` marked block is removed and UFW
reloaded.

#### 2.6 Web pages

`/hosts/:id/firewall`: status and defaults, rules table (origin badge, add and
delete dialogs with the plan diff and guard results), snapshots (list,
compare, restore), Docker integration (detection, enable, health, repair) and
exposure table with filters. Every apply shows the revert countdown.

### 3. Docker management (R50–R55)

Module `docker`; agent side; lock key `docker`; default enabled; preflight:
socket present and API ≥ 1.44.

| Action | Class |
|---|---|
| `docker.container.start/v1`, `.stop/v1` | compensable, each other's inverse |
| `docker.container.restart/v1` | compensable, by `start` |
| `docker.container.remove/v1` | irreversible (typed name) |
| `docker.container.inspect/v1`, `docker.container.logs/v1` (last N lines) | read |
| `docker.image.pull/v1` | compensable, by `docker.image.remove` |
| `docker.image.remove/v1`, `docker.image.prune/v1` | irreversible |
| `docker.network.create/v1`, `docker.volume.create/v1` | compensable, by their remove |
| `docker.network.remove/v1`, `docker.volume.remove/v1` | irreversible |
| `docker.network.connect/v1`, `.disconnect/v1` | compensable, each other's inverse |
| `docker.compose.up/v1`, `.down/v1` | compensable, each other's inverse |
| `docker.compose.pull/v1`, `.restart/v1` | compensable, by `docker.compose.up` |

- Resources: `docker.engine`, `docker.containers` (with stats summary),
  `docker.images`, `docker.networks`, `docker.volumes`,
  `docker.compose.projects`.
- Streams: `docker.logs` (follow, since, tail), `docker.stats`, `docker.pull`.
- **Prune (R51).** The plan lists every image to be removed and the bytes
  reclaimed; apply removes exactly that list (re-checked against the state
  hash), not "whatever is dangling now".
- **In use (R52).** Removing a network or volume used by any container fails
  the plan with `docker.in_use` naming the containers.
- **Compose (R53).** Projects come from the labels
  `com.docker.compose.project`, `.project.working_dir` and
  `.project.config_files`. Actions run
  `/usr/bin/docker compose --project-directory <dir> -f <file>... -p <name>
  <verb>` with fixed flags (`up -d`, `down`, `pull`, `restart`); success is
  the exit code, and the result is read back from the Engine API. A project
  whose files are gone is listed `unavailable` with the reason.
- **Exposure in container views (R55).** The container page shows the
  `firewall.exposure` rows of its published ports and Allow and Deny buttons
  that plan `firewall.container.allow/v1` and `deny/v1` on the same host
  directly, with the firewall's diff, guards and revert countdown. Without an
  available firewall module or a supported Docker integration the buttons are
  disabled with the reason.
- Web pages: `/hosts/:id/docker` with containers, images, networks, volumes and
  Compose tabs; a container page with logs (follow), stats, inspect and
  exposure.

### 4. Tailscale (R56–R64)

#### 4.1 Node (R56, R57) — module `tailscale`, agent side, lock key `tailscale`

- Resource `tailscale.status`: backend state, self (addresses, DNS name,
  tags), peers (online, addresses, connection: `direct <endpoint>`, `derp
  <region>` or `peer-relay`), preferred DERP region and per-region latency
  (from the local API's net check report).
- `tailscale.prefs.set/v1` (revertible): `wantRunning`, `advertiseRoutes`,
  `exitNodeID`, `advertiseExitNode`, `acceptRoutes`, `shieldsUp`, `runSSH`.
  Snapshot: the current prefs; revert edits them back. Guard (same idea as
  2.2): when the controller address or a panel listener is a Tailscale address,
  a change that sets `wantRunning: false`, sets `shieldsUp: true` on the
  controller host, or drops a route the controller link uses fails with
  `tailscale.link_refused` unless `overrideLockout: true`.
- `tailscale.node.login/v1` (compensable, by `tailscale.node.logout/v1`):
  `authKey` (secret field), `hostname`, `advertiseTags`.
- `tailscale.install/v1` (compensable, by `tailscale.uninstall/v1`): writes
  `/usr/share/keyrings/tailscale-archive-keyring.gpg` (fingerprint pinned in
  code, refused on mismatch) and `/etc/apt/sources.list.d/tailscale.list` as
  owned files, runs `apt-get update` limited to that source and
  `apt-get install -y tailscale`. `tailscale.update/v1` (compensable, by
  `tailscale.install/v1`) upgrades the package.
- Preflight for everything but install: the local API socket answers.
- Web: `/hosts/:id/tailscale` with status, peers, settings form and install or
  update.

#### 4.2 Tailnet (R58–R60) — module `tailnet`, controller side, lock key `tailnet`

- `tailnet.connect/v1` (compensable, by `tailnet.disconnect/v1`): tailnet name,
  OAuth client id and secret (sealed). Connect fetches a token and checks the
  scopes it needs (`devices:core`, `devices:routes`, `auth_keys`,
  `policy_file`), failing with `cloud.permission_missing` naming the missing
  ones. One tailnet per controller in v1.
- Read actions: `tailnet.device.list/v1`, `tailnet.policy.get/v1`.
- Compensable: `tailnet.device.authorize/v1`;
  `tailnet.device.routes.set/v1` (approve routes and exit node; the plan
  stores the previous set and the compensation is the same action with it);
  `tailnet.authkey.create/v1` (compensated by `tailnet.authkey.delete/v1`; the
  output shows the key once).
- Irreversible (typed confirmation): `tailnet.device.expire/v1`,
  `tailnet.device.delete/v1`.
- **Policy (R59).** `tailnet.policy.set/v1` (compensable, by
  `tailnet.policy.restore/v1`): input is the full HuJSON text. Plan fetches the
  current policy as HuJSON with its ETag, validates the new text with
  Tailscale's validate endpoint, and renders a line diff. Apply posts with
  `If-Match` on the stored ETag; a mismatch fails with
  `tailnet.policy_conflict`. The snapshot is the previous HuJSON text, so
  comments survive both ways.
- **Join (R60).** `tailnet.node.join/v1` (composite, compensable by
  `tailnet.node.leave/v1`): creates a one-use, pre-authorized auth key and runs
  child `tailscale.node.login/v1` on the host with it; the key never leaves
  the plan record unsealed. The host page offers "Join tailnet" right after
  enrollment.
- Web: `/tailnet` with devices, approvals, auth keys and a policy editor with
  validate and diff.
- Harness tests needing a real tailnet use the test tailnet of ADR 0008 and
  run in the live step (section 12).

#### 4.3 DERP (R61–R64) — module `derp`, controller side with an agent half

- **Artifact.** The release workflow builds `tailscale.com/cmd/derper` at
  `release/derper.version` for amd64 and arm64 (`CGO_ENABLED=0`), names them
  `derper-<version>-linux-<arch>`, lists them in `checksums.txt` and signs
  with the release key. The harness builds them with test keys.
- **Agent half.** `derp.install/v1` (compensable, by `derp.uninstall/v1`):
  downloads (or receives from the controller) the artifact, verifies it with
  the updater's verification code, installs `/usr/local/lib/darkpanel/derper`,
  writes `/etc/systemd/system/darkpanel-derper.service` and
  `/var/lib/darkpanel/derp/` as owned paths, and starts the unit.
  `derp.binary.update/v1` (compensable, by itself with the previous version)
  swaps the binary keeping `derper.prev`, restarts, health-checks
  (`/derp/probe` and STUN) and restores the previous binary on failure.
- **Certificates (R61).** `certMode: letsencrypt` with `hostname`, or
  `certMode: ip` with the host's public address. The first DERP task confirms
  derper's flags for both at the pinned version and records them here.
- **Client verification (R62).** derper runs with `--verify-clients`, and
  fail-open disabled, against the host's own tailscaled; preflight requires
  the host to be joined to the tailnet. Turning verification off needs input
  `verifyClients: false` and shows a warning.
- **Deploy (composite).** `derp.server.deploy/v1` (compensable, by
  `derp.server.remove/v1`): children `derp.install/v1`,
  `firewall.rule.add/v1` ×3 (80/tcp, 443/tcp, 3478/udp, labelled
  `derp`), and optionally `derp.region.set/v1`.
- **Region (R63).** `derp.region.set/v1` and `derp.region.remove/v1`
  (composite, compensable by each other): edit the policy's `derpMap` through
  child `tailnet.policy.set/v1` with region id 900–999 (chosen by the user,
  refused if taken), region code and name, the node entry, and option
  `omitDefaultRegions`.
- **Probe (R64).** `derp.probe/v1` (read): TLS `/derp/probe` and a STUN
  binding request from the controller and from chosen agents; the nodes using
  the region come from each agent's `tailscale.status`.
- Controller table `derp_servers` (host, hostname or IP, region id, version,
  cert mode). Web: `/derp`.

### 5. Exposure: Nginx Proxy Manager (R65–R68) — module `npm`

- **Deploy (R65).** `npm.deploy/v1` (agent, compensable by `npm.remove/v1`,
  lock `npm`): creates network `darkpanel-npm`, volumes `darkpanel-npm-data`
  and `darkpanel-npm-letsencrypt`, and container `darkpanel-npm` from the
  pinned image with 80 and 443 published on all addresses and 81 published on
  127.0.0.1 and the host's Tailscale address only (loopback only when the
  host has no Tailscale address). Admin email and a 32-byte
  random password are generated, sealed in `npm_instances`, and set at first
  start (the wave's first task confirms the pinned version's mechanism; if it
  has none, deploy logs in with the image's documented default and changes it
  before the port is published beyond loopback). The composite
  `npm.deploy.host/v1` adds firewall children for 80 and 443.
- **Management (R66).** Agent actions call the local API on 127.0.0.1:81 with
  the stored credentials: `npm.proxyhost.add/v1`, `.update/v1`, `.delete/v1`,
  `npm.accesslist.add/v1`, `.delete/v1`, `npm.certificate.request/v1`,
  `.delete/v1` (compensable pairs; certificate delete irreversible). Read
  actions list each.
- **Expose container (R67).** `npm.expose.container/v1` (composite,
  compensable by `npm.expose.remove/v1`): children
  `docker.network.connect/v1` (NPM to the container's network),
  `npm.certificate.request/v1`, `npm.proxyhost.add/v1` (forward to
  `<container name>:<port>`), `firewall.rule.add/v1` for 80 and 443 when not
  already open.
- **Upgrade (R68).** `npm.upgrade/v1` (agent, compensable by itself with the
  previous digest): stop the container, tar both volumes to
  `/var/lib/darkpanel/agent/backups/npm/<time>/`, recreate on the new digest,
  wait up to 120 s for the API, and on failure restore the volumes and the
  previous container. Keeps the last three backups.
- Web: `/hosts/:id/npm`, plus an "Expose" button on container pages.

### 6. Exposure: Cloudflare Tunnel (R69–R71) — module `cloudflare`

- **Account (R69).** `cloudflare.account.connect/v1` (controller, compensable
  by `.disconnect/v1`): account id and API token (sealed). Connect verifies the
  token and checks `Cloudflare Tunnel: Edit` on the account and `DNS: Edit` on
  at least one zone, listing the zones it can edit.
- **Tunnels (R70).** `cloudflare.tunnel.create/v1` (remotely managed,
  compensable by `.delete/v1`), `cloudflare.tunnel.list/v1` (read, with
  connector status). `cloudflare.connector.deploy/v1` (composite): child agent
  action `cloudflare.cloudflared.run/v1` (compensable by `.stop/v1`), which
  creates container `darkpanel-cloudflared-<tunnel>` from the pinned image with
  `tunnel --no-autoupdate run` and the token in `TUNNEL_TOKEN`, restart policy
  `unless-stopped`; the token is sealed in the agent database and passed only
  through the Engine API.
- **Hostnames (R71).** `cloudflare.hostname.add/v1` and `.remove/v1`
  (controller, compensable by each other): update the tunnel's ingress
  configuration and create or delete the proxied CNAME to
  `<tunnel id>.cfargotunnel.com` together; a failure of the second step undoes
  the first. `cloudflare.expose.container/v1` (composite): children
  `docker.network.connect/v1` (cloudflared to the container's network) and
  `cloudflare.hostname.add/v1` with service `http://<container>:<port>`. No
  inbound port opens.
- Controller tables `cloudflare_accounts`, `cloudflare_tunnels`. Web:
  `/cloudflare`, plus "Expose via tunnel" on container pages.

### 7. Exposure: the panel itself (R72) — module `panel`

- `panel.expose/v1` (controller composite, compensable by
  `panel.unexpose/v1`), input `via: npm|tunnel`, host must be the controller's
  host, plus the NPM domain or the tunnel hostname.
- **Refusals.** Fails with `panel.totp_incomplete` while any admin is pending
  TOTP, and with `panel.already_exposed` when an exposure exists.
- **Steps.** Open a runtime listener (1.7) on the gateway address of network
  `darkpanel-panel` (created as a child), HTTPS from the web root, allowed peer
  the proxy container's address on that network, and add that address to
  `trustedProxies`; connect the proxy container to `darkpanel-panel`; create
  the NPM proxy host (upstream `https`, certificate verification off for the
  internal hop) or the tunnel hostname (`originRequest.noTLSVerify`).
- **Guardrails.** Login over the exposed path requires TOTP as everywhere;
  every page shows a banner "Panel exposed via <npm|tunnel> at <host>" with a
  link to unexpose; the audit log marks requests that arrived through the
  proxy.
- **Revert.** `panel.unexpose/v1` removes the proxy host or hostname, the
  network connection, the trusted proxy and the listener.

### 8. Hardening (R73–R79)

- **Threat model (R73).** `docs/security/threat-model.md`: assets, actors,
  trust boundaries (browser–controller, controller–agent, agent–host,
  controller–cloud APIs, proxies), and per area (authentication, agent
  protocol, command execution, secret handling, firewall, cloud credentials,
  exposure) the threats, mitigations and residual risk.
- **Security review (R73).** Run as the owner's two-reviewer panel
  (`dr-superpowers:judge-fable` and Codex `gpt-6-astra`, one shared brief).
  Findings go into a register `docs/superpowers/registers/<date>-v1-security-review.md`;
  every critical and high finding is fixed and re-reviewed; medium and low are
  fixed or deferred by the owner.
- **Fuzzing (R74).** Go native fuzz tests for: `ufw status verbose` and
  `numbered`, `user.rules` tuples, `iptables-save`, `iptables --version`,
  `/proc/net/*`, os-release, cpuinfo, meminfo, loadavg, uptime, marked
  blocks, HuJSON handling. Seed corpora from the four OS images.
  `.github/workflows/fuzz.yml` runs each target for 30 s on every push and for
  10 minutes nightly.
- **Upgrade matrix (R75).** Harness test `TestUpgradeMatrix`: for every `v*`
  tag (or, before the first tag, the commit that closed M2, built as
  `v0.2.0`), install it with test keys, upgrade to the build under test,
  check health and data; ship a deliberately broken build and check rollback
  within 2 minutes; run an agent built at the previous minor against the new
  controller. All four OS.
- **Footprint (R76).** Harness test `TestFootprint`: agent idle RSS after
  10 minutes ≤ 40 MB; controller RSS ≤ 150 MB with 10 enrolled agents (the
  extra agents run as additional agent processes with their own data
  directories and identities on the harness VMs, using a test-only
  `--state-dir` flag compiled only into harness builds). A miss is a bug.
- **Accessibility (R77).** `vitest-axe` checks and keyboard-only tests for
  login, host list, a firewall change with confirm and revert, container
  actions and expose container. Contrast is checked on the darkraise-ui
  tokens in both themes.
- **Documentation (R78, R79).** `docs/user/` (install, first admin, every
  module, lockout recovery, uninstall), `docs/dev/module-guide.md`, and
  `docs/api/` generated from `/api/v1/openapi.json` by `make api-docs`, with a
  CI check that the committed copy is current.

### 9. Release (R80, R81)

- **Controller backup and restore.** `darkpanel controller backup --out
  <file>` over the controller socket: SQLite online backup of
  `controller.db` plus `controller.key`, in a tar encrypted with
  XChaCha20-Poly1305 under a key derived from a passphrase (argon2id, t=3,
  m=64 MiB, p=4, 16-byte salt in the header). `darkpanel controller restore
  --in <file>` runs with the controller stopped, on a host after
  `system install --role controller`, refuses a newer schema than the binary,
  and keeps the replaced files as `.pre-restore`.
- **Runbook (R81).** `docs/runbook.md`: lockout (break-glass firewall
  rollback, Tailscale prefs revert), failed update (rollback, recover), lost
  controller (restore from backup, re-point agents by re-enrollment).
- **Release candidate.** `CHANGELOG.md` and `docs/versioning.md` updated;
  `make release-check` green; the commit ready to tag `v1.0.0-rc.1`. Tagging
  is the owner's (section 12).

### 10. Web UI shell

The sidebar groups host pages under each host (Overview, Firewall, Docker,
Tailscale, Proxy) through the module registry, and adds global pages Tailnet,
DERP and Cloudflare. A shared `ChangeDialog` component renders plan diffs,
guard results, the override checkbox for refused guards, typed confirmation
for irreversible actions, and the revert countdown; wave 0 builds it so
module pages only consume it.

### 11. Error codes and actors

New codes (wave 0 adds all): `firewall.lockout_refused`,
`firewall.unsupported_integration`, `firewall.prediction_mismatch`,
`docker.engine_unavailable`, `docker.in_use`, `docker.compose_unavailable`,
`tailscale.unavailable`, `tailscale.link_refused`,
`tailnet.policy_conflict`, `tailnet.policy_invalid`, `cloud.auth_failed`,
`cloud.permission_missing`, `cloud.rate_limited`, `cloud.unavailable`,
`npm.unhealthy`, `derp.verify_failed`, `panel.totp_incomplete`,
`panel.already_exposed`, `core.child_failed`, `core.stream_limit`.
New actors: `system:firewall.reconcile`.

### 12. Owner checklist (the end of this programme)

1. M2 row 35: minisign keys and secrets in the release environment, the
   environment limited to `v*` tags, a `v*` tag ruleset, the SSH tag-signing
   key in `release/allowed_signers`, the public key in
   `internal/update/trustedkeys`, `make install-sh` committed.
2. Credentials for the live step: a test tailnet OAuth client (also the CI
   secrets of ADR 0008), a Cloudflare API token with a test zone, a domain
   and a public host for Let's Encrypt and DERP. Claude then runs the live
   demonstrations: tailnet management and join, a DERP region carrying traffic
   between two nodes, a container exposed through NPM with a real certificate
   and through a tunnel, the panel exposed and reverted.
3. Tag `v1.0.0-rc.1` (closes M2 rows 11 and 16 once the release run is green).
4. Run the release candidate on the production VPS plus one agent for 14 days
   with no manual repair (S7).
5. Tag `v1.0.0`; milestones close through `dr-superpowers:closing-a-milestone`.

## Contracts

- Every new action follows ADR 0002's compatibility rules and gets a schema
  snapshot under `schemas/`.
- Agent protocol: streams are additive; `hello` gains `streams`; frozen parts
  are unchanged.
- Database: new module tables are prefixed with the module id; core gains
  `parent_change_id` on changes and `core_listeners`; all migrations are
  expand-only.
- Files on disk: every new file, directory, unit and block is in its module's
  owned paths; UFW state files are `restore` paths.

## Parallel decomposition

| Wave | Part | Owns | Consumes | Runs |
|---|---|---|---|---|
| 0 | Core extensions | `internal/core/**`, `internal/platform/**`, `internal/agent/**`, `internal/controller/**` (except module packages), `internal/transport/**`, `internal/modules/modules.go`, `web/src/modules/registry.ts`, `web/src/components/change-dialog*`, `web/src/shell.tsx`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `docs/adr/0014-*`, `docs/adr/README.md`, module skeleton packages | — | alone |
| 1 | Firewall UFW and safe apply | `internal/modules/firewall/**` (except `docker*.go`, `exposure*.go`), `web/src/modules/firewall/**` (except docker and exposure pages), `test/harness/m3_firewall_test.go` | wave 0 | alone |
| 2 | Firewall Docker integration and exposure | `internal/modules/firewall/docker*.go`, `exposure*.go`, their web pages, `test/harness/m3_docker_test.go` | waves 0, 1 | alone |
| 3a | Docker module | `internal/modules/docker/**`, `web/src/modules/docker/**`, `test/harness/m4_test.go` | 0, 2 | parallel |
| 3b | Tailscale node | `internal/modules/tailscale/**`, `web/src/modules/tailscale/**`, `test/harness/m5_node_test.go` | 0 | parallel |
| 3c | Tailnet | `internal/modules/tailnet/**`, `web/src/modules/tailnet/**` | 0 | parallel |
| 3d | Cloudflare | `internal/modules/cloudflare/**`, `web/src/modules/cloudflare/**`, `test/harness/m6_cloudflare_test.go` | 0 | parallel |
| 4a | DERP | `internal/modules/derp/**`, `web/src/modules/derp/**`, `release/derper.version`, `.github/workflows/release.yml`, `test/harness/m5_derp_test.go` | 0, 1, 3b, 3c | parallel |
| 4b | Nginx Proxy Manager | `internal/modules/npm/**`, `web/src/modules/npm/**`, `test/harness/m6_npm_test.go` | 0, 1, 3a | parallel |
| 4c | Backup and restore | `internal/cli/controller_backup*.go`, `internal/controller/backup/**` | 0 | parallel |
| 5 | Panel exposure | `internal/modules/panel/**`, `web/src/modules/panel/**` | 0, 4b, 3d | alone |
| 6a | Fuzz | `*_fuzz_test.go` files, `testdata/fuzz/**`, `.github/workflows/fuzz.yml` | 0–5 | parallel |
| 6b | Accessibility | `web/src/**/*.a11y.test.tsx`, `web/src/test/a11y.ts` | 0–5 | parallel |
| 6c | Docs and API reference | `docs/user/**`, `docs/dev/**`, `docs/api/**`, `Makefile` target `api-docs`, `.github/workflows/ci.yml` docs check | 0–5 | parallel |
| 6d | Upgrade matrix and footprint | `test/harness/upgrade_matrix_test.go`, `test/harness/footprint_test.go`, `.github/workflows/harness.yml` | 0–5 | parallel |
| 7 | Security review, fixes, runbook, RC readiness | `docs/security/**`, `docs/runbook.md`, `CHANGELOG.md`, `docs/versioning.md`; fixes anywhere | 0–6 | alone |

- "Owns" is within a wave; a later wave may edit an earlier wave's files
  (wave 2 adds actions to the firewall manifest that wave 1 wrote).
- Shared files (module list, web registry, allowlist, error codes,
  dependencies, lockfiles) belong to wave 0 only. Before wave 6, a change to
  `harness.yml` (a job split or a CI secret) belongs to the wave's alone part
  or, in a parallel wave, to the integrator on `main`. `Makefile` and `ci.yml`
  belong to 6c within wave 6; `harness.yml` to 6d; `release.yml` to 4a.
- Schema snapshots and the OpenAPI document are regenerated by the integrator
  after each wave merges.
- Test isolation: unit tests use `t.TempDir()`, in-process fakes on random
  ports or temporary Unix sockets and no global state; harness tests run in
  separate files with their own VMs per OS job, and a job whose total passes
  its 120-minute limit is split per milestone.

## Plans

One plan per wave, named `docs/superpowers/plans/<date>-v1-w<n>-<topic>.md`,
written after the previous wave merges. Every plan ends with the gates in
`docs/superpowers/gates.md`, including the harness gate when it touches the
harness, a module's host behaviour or the paths gate 7 lists.

## Verification

| Exit criterion | Evidence |
|---|---|
| M3: S1–S3 pass on all four OS | harness `TestM3*`: lockout refusals for each flow, revert on disconnect and on reboot, exposure equals the external scan (IPv4 and IPv6), published ports unreachable unless a route rule allows them |
| M4: every Docker requirement on a managed agent, including allow and deny from a container view | harness `TestM4` on the enrolled agent VM; web tests of the container exposure buttons |
| M5: node and tailnet management; a Darkpanel DERP region carries traffic between two nodes | unit tests against fakes; harness node tests; the live step for tailnet and DERP |
| M6: container exposed through NPM and through a tunnel; panel exposed and reverted | harness with real NPM and cloudflared containers against fakes; the live step for real certificates, tunnels and DNS |
| M7: no open critical or high findings; matrix, footprint, accessibility, docs | security register; `TestUpgradeMatrix`, `TestFootprint`; a11y tests; docs CI check |
| M8: v1.0 tagged and signed, production plus one agent, runbook | owner checklist items 1, 3–5; `docs/runbook.md` |

## Rejected alternatives

- One plan for all of v1: about 300 tasks written against code that does not
  exist yet; rejected by the owner for rolling wave plans.
- Milestone-by-milestone order: simpler tracking, but the Docker module would
  wait for the whole firewall.
- User-flow slices first: repeated rework of shared modules and merge
  collisions in parallel waves.
- Running a change against a scratch copy of UFW's files for the preview: UFW
  has no supported alternate root; the preview is computed from the model and
  verify compares UFW's result with the prediction.
- Writing UFW's `user.rules` directly: UFW owns that file; Darkpanel writes
  through `ufw` and only restores snapshotted bytes.
- Vendor SDKs (Docker, Tailscale): large dependency trees against the S6
  footprint; the hand-written clients cover only the endpoints used.
- derper built into Darkpanel or built on the host: see Owner decisions.

## Out of scope

M9 candidates; Docker's nftables backend (detected, reported unsupported);
Headscale; more than one tailnet or Cloudflare account per controller.

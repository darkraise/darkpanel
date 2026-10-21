# v1 completion: firewall, Docker, Tailscale, exposure, hardening and release

**Date:** 2026-10-21
**Status:** design approved by the owner section by section on 2026-10-21;
revision 2 after the first Fable and Codex gpt-6-astra panel round (67
findings, all accepted; see the review log). The written spec awaits the
second panel round and the owner's review.
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirements:** R37–R81; M2 register rows 11, 16 and 35
(docs/superpowers/registers/2026-10-19-m2-fleet-lifecycle-core.md); the
controller backup and restore deferred by ADR 0005 to M8; the M5 web
certificate work deferred by ADR 0010
**Execution strategy:** parallel — rolling wave plans. Wave 0 (core
extensions) fixes every shared file, interface, dependency, error code,
extension point and module skeleton. Safety-critical waves run alone (0, 1,
2, 4, 6, 7, 9); waves 3, 5 and 8 run their parts in parallel worktrees. Each
wave's plan is written when the previous wave has merged (owner standing
preference: favour parallel to reach v1 sooner).

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
  tested against in-process fake servers and local stand-ins. Live
  demonstrations run once, as a gated step before v1.0, when the owner
  provides credentials.
- DERP runs as a signed derper artifact built by the release workflow from
  upstream `tailscale.com/cmd/derper` at a pinned version and installed by the
  agent as a systemd service.
- Approach: core extensions first and alone, then the firewall alone, then
  modules in waves by dependency, with connectivity, data-loss and exposure
  work alone; hardening and release last.
- During this programme Claude may push wave branches and `main` to origin and
  dispatch `harness.yml`; tags and releases need the owner.
- After round 1 the owner chose: revise every finding, then a second panel
  round before the wave 0 plan.

## Versions

| Component | Version | Note |
|---|---|---|
| Docker Engine API | 1.44 minimum (Docker 25) for the `docker` module | preflight refuses older |
| Docker for the firewall integration | Docker 28.0 or later | earlier releases let hosts on the same L2 reach localhost-published ports; the integration reports older daemons unsupported |
| Docker `FirewallBackend` | `/info` field, API 1.49 (Docker 28.1) or later, object `{Driver, Info}`; `Driver` is `iptables`, `nftables`, `iptables+firewalld` or `nftables+firewalld`; `Info` is not stable and is not read | absent field (Docker 28.0, or a client below 1.49): the daemon uses its iptables backend |
| UFW | as shipped by each supported OS (0.36.1 on Ubuntu 22.04 to 0.36.2 on Debian 13) | rule comments need 0.35 |
| Compose plugin | the `docker-compose-plugin` package from Docker's repository, at `/usr/libexec/docker/cli-plugins/docker-compose` | harness pins its version |
| derper | `release/derper.version` (a `tailscale.com` module version, 1.102 or later) | Tailscale publishes no binaries or images; `-verify-clients` needs tailscaled from the same version on the DERP host (4.3) |
| Nginx Proxy Manager | `jc21/nginx-proxy-manager` 2.13 or later, pinned by digest in `internal/modules/npm/image.go` | 2.13 removed the default admin; `LE_SERVER` (2.12) points certbot at another ACME directory |
| cloudflared | `cloudflare/cloudflared` 2025.4.0 or later, pinned by digest in `internal/modules/cloudflare/image.go` | `tunnel run --token-file` (7.2) |
| Tailscale | tailscaled 1.82 or later on managed nodes | LocalAPI endpoints in 1.8 |
| Pebble | `ghcr.io/letsencrypt/pebble`, pinned in `test/harness/pins.go` | ACME stand-in for harness certificate tests |

## Design

### 1. Core extensions (wave 0)

Wave 0 changes the shared core. ADR 0014 records every decision in this
section; where it refines ADR 0002, 0003, 0004 or 0009 it says so.

#### 1.1 Action naming

Action names stay `<module>.<noun>.<verb>` and resources `<module>.<noun>`
(registry.go). Every name in this spec follows that grammar; nouns and verbs
use `-` for multi-word segments (`tailnet.device.set-routes`). `BreakGlass`
stays controller-side only; an agent action is reachable over the agent socket
by `--local` without any flag, as today.

#### 1.2 Undo handlers

ADR 0004's `compensable` class keeps its meaning (no timer; undone by a
compensating step) but the compensating step may be either a registered
action (`CompensatedBy`) or an `Undo` handler on the action itself:

```go
// Undo returns the host to a usable state after this change. It receives the
// change's stored output, so it acts only on what the change created or
// changed, and it must succeed when run twice.
Undo func(ctx context.Context, env Env, in, out json.RawMessage) error
```

- Exactly one of `CompensatedBy` and `Undo` is set on a compensable action;
  the registry enforces it.
- An `Undo` handler, and a `CompensatedBy` target used as compensation, never
  removes a resource the change did not create; the change's output records
  what it created (`created: [...]`). A compensating action is never of class
  `irreversible`; the registry refuses one.
- Each action's spec states what its undo restores and what it cannot (for
  example "a pulled image is removed only if it was absent before the pull").
- The UI and CLI offer "Undo" on an interrupted or failed compensable change,
  as ADR 0004 already offers the compensating action.

#### 1.3 Composite operations

A composite is a controller-side action whose apply runs **steps**: child
changes of other modules' actions on one or more hosts. It replaces the
"plan every child up front" design of revision 1.

```go
type Step struct {
    ID     string                 // unique within the composite
    Host   string                 // "" for a controller-side step
    Action ActionRef
    Input  func(prev StepOutputs) (any, error) // may read earlier steps' outputs
}
type Composite interface {
    Steps(ctx context.Context, env Env, in json.RawMessage) ([]Step, error)
    Preview(ctx context.Context, env Env, in json.RawMessage) (PlanOutcome, error)
}
```

- **Rules checked at plan time.** Every step is planned just in time, after
  the previous step has finished, so a compensable step (which releases its
  locks when it completes, ADR 0004) never meets a stale state hash or a held
  lock from an earlier step. A revertible step holds its locks until it is
  confirmed, so a composite has at most one revertible step per (host, lock
  key), and a module offers a batch action for that (`firewall.rules.apply`
  adds several rules in one change). No step may be `irreversible`.
  Revertible steps come after every compensable step on the same host.
- **Plan.** The composite's plan stores its input, its step list with the
  steps' static inputs, and a preview: `Preview` plans every step whose input
  depends on no earlier output (a dry run, no mutation) and describes the
  others from their declared input template (for example "auth key: created
  in step `key`, tags tag:server"). Guards of pre-planned steps are the
  composite's guards; a refused guard refuses the composite.
- **Durable step log.** Controller table `core_composite_steps` (composite
  change id, step id, ordinal, host, action ref, resolved input with secret
  fields sealed, child plan id, child change id, state, output, error). Before
  a step's child is planned and before it is applied, its row is written and
  fsynced. States: `pending → planned → applying → applied → (confirmed)`,
  and on unwinding `undoing → undone | undo_failed`.
- **Apply.** Steps run in order. Each step is planned just in time with its
  resolved input (fresh state hash, fresh guards against live facts); a
  refused guard stops the composite and unwinds. Secrets produced by a step
  (an auth key, a tunnel token) are sealed in the step row and passed to the
  next step's input on the controller; they never appear in the preview,
  audit or API.
- **Confirmation (R41).** A composite containing a revertible step is itself
  confirm-or-revert to the user: after the last step's verify passes, the
  composite is `awaiting_confirmation` with the deadline of its revertible
  steps. Revertible steps are applied last (rule above), each with the
  composite's confirm timeout, so their agent-side timers start after every
  slow compensable step has finished. Confirming the composite confirms each
  revertible step; if any revertible step reverts (deadline, explicit revert,
  agent restart), the controller unwinds the compensable steps.
- **Unwind.** In reverse order: revertible steps are reverted (or left to
  their own timer if the agent is unreachable), compensable steps are undone
  through their undo or compensating action with the step's stored output.
  An undo failure leaves the step `undo_failed` and the composite
  `needs_repair`; `darkpanel change repair <id>` and a UI button retry the
  failed undo steps. Nothing is retried silently.
- **Recovery.** On controller start, before serving: a composite in `applying`
  queries the child change of its current step from the agent by plan id
  (idempotent apply returns the stored result, ADR 0004) to resolve an
  uncertain outcome, then unwinds; a composite in `awaiting_confirmation`
  past its deadline unwinds; one within its deadline keeps waiting. This is
  an explicit refinement of ADR 0004 for composites only: the controller
  unwinds automatically because every undo acts only on what the composite
  created. A single compensable action keeps ADR 0004's behaviour
  (interrupted, operator decides).
- **Records.** Each child is an ordinary change with `parent_change_id` set
  (nullable column, additive migration on both sides). Locks, snapshots and
  journal stay with the owning module.
- **Tests.** Fault injection at every state transition: controller killed
  before and after each step's apply, agent unreachable during unwind, undo
  failure, deadline passing while the controller is down.

#### 1.4 Agent background tasks

Optional agent-side module interface:

```go
type BackgroundTask struct {
    ID    string        // "firewall.reconcile"; audit actor system:<ID>
    Every time.Duration // 0: event-driven only
    Run   func(ctx context.Context, env Env, wake <-chan struct{}) error
}
type Background interface{ BackgroundTasks() []BackgroundTask }
```

- The runtime starts a module's tasks after startup recovery, only while the
  module is enabled and its preflight passes; disabling the module or a
  failing preflight cancels them. A returned error restarts the task with
  backoff from 1 s to 5 min.
- A task mutates only by planning and applying one of its module's actions
  through the lifecycle engine, as actor `system:<ID>`, with the action's
  locks, snapshot and journal. Such a change is confirmed in the same step
  (it restores state the owner already confirmed) and is marked `system` so
  rollbacks can skip it (2.6).
- A task skips its run, without error, while any of its lock keys is held by
  an unconfirmed change or an update reservation, and runs again on the next
  wake.
- Event subscriptions (Docker events) reconnect with the same backoff, and a
  reconnect triggers a full run.

#### 1.5 Guard context

Lockout guards (2.3, 4.1) need the paths that must survive a change.

- **Wire form.** Every action that declares guards has an input field
  `guard` of type `GuardContext` in its schema. The dispatcher fills it and
  overwrites any client value; the API rejects a client-supplied `guard`. As
  an ordinary schema field it is covered by ADR 0002's rule that the
  controller never sends a field the agent's schema lacks, so an agent
  without guard support cannot receive a guarded action.
- **Content.**

  ```go
  type GuardContext struct {
      Client        *Peer        // the browser or CLI as the controller saw it
      Via           *ProxyPath   // set when the request came through an exposure (8)
      PanelListen   []AddrPort   // the controller's web and runtime listeners, on the controller's host
      AgentListen   []AddrPort   // the controller's agent listeners, on the controller's host
      AgentPeers    []Addr       // live agents' source addresses, on the controller's host
  }
  type Peer struct{ Addr Addr; Port uint16 }
  type ProxyPath struct{ Kind string /* npm|tunnel */; ProxyAddr Addr; ListenerAddr AddrPort; Public []AddrPort }
  ```

  The agent adds its own live facts at plan and again at apply: its session's
  local and remote address to the controller, established SSH sessions, and
  interface addresses. A local break-glass plan has only the agent's own
  facts.
- **Apply re-check.** Apply re-evaluates the guards with the stored context
  plus freshly read live facts and refuses if any flow that passed at plan now
  fails, or a new protected flow (a new SSH session, a new agent) fails.

#### 1.6 Streams

- **Discovery.** A new agent endpoint `GET /agent/v1/streams` lists the
  agent's streams and their schema hashes. `hello` is not changed (it is
  frozen for protocol 1). The controller calls it after `hello`; a 404 means
  an agent without streams.
- **Transport.** `GET /agent/v1/streams/{module}/{name}?<params>` on a yamux
  stream the controller opens: chunked JSON lines, at most 64 KiB per event,
  closed by either side. The controller reserves 8 of the 32 streams per
  agent session (`MaxControllerStreams`) for actions, confirmations and
  heartbeats; streams may use at most 16 of the rest.
- **Relay.** `GET /api/v1/hosts/{host}/streams/{module}/{name}` as
  server-sent events, scope `read`. Stream responses use their own deadlines,
  not the 60 s write timeout: a write deadline renewed per event (30 s), an
  idle timeout of 10 minutes, a hard limit of 2 hours. Limits: 4 open streams
  per principal, 16 per host; a full limit answers `core.stream_limit`.
  Buffering is bounded at 256 events per stream; a slow reader is
  disconnected with a final `overflow` event. Streams close when the session
  or token behind them expires or is revoked (checked every 30 s).
- **CLI.** `darkpanel <module> <name> --follow` reads the SSE endpoint with
  its own streaming client, not the bounded JSON client.
- Streams in v1: `docker.logs`, `docker.stats`, `docker.pull`.

#### 1.7 Per-action availability

Module preflight (ADR 0002) stays module-wide. An action may also declare
`Available func(ctx, env) error`; dispatch refuses an action whose check
fails with `module.unavailable` and the reason, and the UI greys it out.
`tailscale.package.install` uses this to stay available on a host without
tailscaled while the module's other actions are not (4.1).

#### 1.8 Host clients (`internal/platform`)

- **Exec allowlist** grows to `/usr/sbin/ufw`, `/usr/sbin/iptables-save`,
  `/usr/sbin/ip6tables-save`, `/usr/sbin/iptables-legacy-save`,
  `/usr/sbin/iptables-nft-save`, `/usr/sbin/ip6tables-legacy-save`,
  `/usr/sbin/ip6tables-nft-save`, `/usr/sbin/iptables` (only `--version`),
  `/usr/bin/apt-get`, `/usr/bin/docker` (only `compose`), `/usr/bin/systemctl`.
  The runner gets a per-call environment allowlist: `apt-get` may add
  `DEBIAN_FRONTEND=noninteractive`, `docker` may add `HOME=/root`; nothing
  else is inherited. `ufw` has no `--` separator, so every value passed to it
  is constrained by schema patterns alone (2.2 lists them).
- **Docker Engine client** (`internal/platform/dockerapi`): HTTP over
  `/var/run/docker.sock`, negotiates the API version (1.44 or later), typed
  calls for the endpoints sections 2–7 use, streaming for logs, stats, pull
  and events. Hand-written; no Docker SDK.
- **Tailscale local client** (`internal/platform/tslocal`): HTTP over
  `/var/run/tailscale/tailscaled.sock`: `GET /localapi/v0/status`,
  `GET` and `PATCH /localapi/v0/prefs` (body `ipn.MaskedPrefs`: the prefs
  plus a `<Field>Set` boolean per field), `POST /localapi/v0/start` with
  `ipn.Options{AuthKey, UpdatePrefs}` for login, `POST /localapi/v0/logout`,
  `GET /localapi/v0/derpmap`, and the certificate pair endpoint. The wire
  types are copied as plain Go structs with only the fields used, and a
  contract test runs them against the tailscaled in the harness pins. There
  is no netcheck endpoint (the CLI computes it in its own process), so DERP
  latency is measured by the agent (4.1).
- **Socket table** (`internal/platform/netstat`): listening and established
  TCP and UDP sockets from `/proc/net/{tcp,tcp6,udp,udp6}` joined to owning
  processes through `/proc/<pid>/fd`, and interface addresses through
  netlink.
- **Netfilter reader** (`internal/platform/netfilter`): runs the `*-save`
  tools for the filter table, parses the output into chains and rules, and
  evaluates a packet tuple against them (2.3). Fuzzed (R74).
- **Restore writes** (2.6): `FileWriter.Restore(path string, data []byte,
  proof []byte)`. The firewall snapshot store issues `proof` as an HMAC over
  (path, data) with a key from `Sealer.Derive("restore")`; the file provider
  verifies it and writes only to paths declared with the owned-path kind
  `restore`. Uninstall never removes a `restore` path.
- **Darkpanel-owned Docker objects.** Containers, networks and volumes that a
  module creates carry the label `darkpanel.module=<id>`. They are that
  module's resources (ADR 0002), driven through `dockerapi` by the module's
  own actions; generic `docker.*` mutating actions refuse them with
  `docker.owned` naming the owning module. ADR 0014 records this as the rule
  for module-owned containers.

#### 1.9 Cloud plumbing (controller)

- `internal/controller/cloud`: one HTTP client with 15 s timeouts. Retries
  (at most 3, backoff, honouring `Retry-After`) apply to `GET`, `PUT`,
  `DELETE` and to `POST` endpoints the provider documents as idempotent or
  that take an idempotency key; any other `POST` that fails with a 5xx or a
  transport error is an **uncertain outcome**: the client looks the object up
  by a client-chosen name or tag it set on the request (for example a tunnel
  name `darkpanel-<change id>`) before reporting success or failure, and never
  re-sends blindly.
- **Accounts.** Each module stores credentials in its own table with the
  secret column sealed (ADR 0005, associated data `<table>.secret.<row id>`).
  Credentials are write-only through the API.
- **Fakes.** `internal/controller/cloud/cloudfake` provides the shared test
  server scaffolding (recorded request log, fault injection: 429, 5xx, lost
  response after commit). Each module's fake server lives in its own package
  (`internal/modules/<id>/fake`) and is built by the wave that builds the
  module, after its spike task has fixed the real API shapes.

#### 1.10 Trusted proxies and runtime listeners

- **Proxy trust** is per listener: a runtime listener (below) names the one
  proxy address it trusts and the header it reads. For Nginx Proxy Manager the
  client address is the **last** entry of `X-Forwarded-For` (NPM appends its
  peer to the incoming header, so earlier entries are client-supplied); for a
  tunnel it is `Cf-Connecting-IP`, which Cloudflare sets. Malformed values
  refuse the request. Requests on other listeners never read forwarded
  headers. The audit entry keeps both the transport peer and the forwarded
  address.
- **Public HTTPS.** A runtime listener refuses a request whose
  `X-Forwarded-Proto` is not `https`; the NPM proxy host is created with
  forced SSL and HSTS, and tunnels are HTTPS at the edge.
- **Runtime listeners.** The controller opens and closes listeners recorded
  in `core_listeners` (id, address, port, allowed peer address, trusted
  header, created by change, state) without a restart. They are HTTPS only,
  with the web certificate authority, and accept TCP connections only from
  the allowed peer. They share the existing listener code's TLS configuration
  rules; the constraint in `docs/superpowers/distilled/constraints.md`
  applies, so wave 0 adds a session-resumption test with shared ticket keys if
  runtime listeners share or reuse a `tls.Config`.
- Requests through a runtime listener get `X-Accel-Buffering: no` on SSE
  responses, and the CSRF origin check accepts the exposure's public host
  name (8).

#### 1.11 Uninstall options

`darkpanel uninstall` gains `--module-option <module>.<key>=<value>`
(repeatable), passed to the module's `Uninstaller` as a map; the plan lists
each module's options and their defaults. The firewall uses
`firewall.rules=keep|remove|baseline` (2.7).

#### 1.12 Module skeletons, extension points and shared files

Wave 0 creates every new module package with an empty manifest and registers
it: `firewall`, `docker`, `tailscale`, `tailnet`, `derp`, `npm`,
`cloudflare`, `panel`. Every module is default-enabled; preflight decides
whether it is available on a host (R9). Wave 0 also adds:

- every error code of section 11 and every dependency of section 11;
- `web/src/modules/registry.ts` entries that import each module's
  `web/src/modules/<id>/index.ts` (a stub until its wave), and a typed
  **container action slot** that module entries may fill (NPM's "Expose",
  Cloudflare's "Expose via tunnel", firewall's "Allow" and "Deny"), so the
  Docker container page renders other modules' buttons without importing
  them;
- `web/src/components/change-dialog.tsx`: plan diff, guard results, a
  per-guard override control for guards marked `overridable`, typed
  confirmation for irreversible actions, the revert countdown, and composite
  step progress;
- the sidebar grouping in `web/src/shell.tsx` (host pages under each host,
  global pages Tailnet, DERP, Cloudflare);
- harness helpers: per-test VM names, work directories and network ranges
  derived from the test name, a VM concurrency limit, and the Compose plugin
  and Pebble in `test/harness/pins.go` and `provision.go`.

Later waves never edit these files. A wave that finds a missing code,
dependency or extension point stops, and the integrator adds it on `main`
before the wave continues.

#### 1.13 CLI confirmation and overrides

The existing contract stays: `--yes` answers the "Apply?" question only;
`--confirm` confirms a revertible change as soon as verify passes; without it
the CLI prints the change id and deadline and `darkpanel change confirm <id>`
confirms. Guards marked `overridable` (only the lockout guards of 2.3 and
4.1) are overridden with `--override <guard id>` (repeatable), which re-plans
with the override recorded in the input; every other guard is a hard
refusal.

### 2. Firewall (R37–R49)

Module `firewall`; agent side; lock key `firewall`; preflight:
`/usr/sbin/ufw` present, UFW 0.35 or later, the `*-save` tools present.

#### 2.1 Configured state and loaded state

- **Configured state** comes from files, whether UFW is active or not:
  `ENABLED` and `LOGLEVEL` from `/etc/ufw/ufw.conf`; `IPV6` and the
  `DEFAULT_{INPUT,OUTPUT,FORWARD}_POLICY` values from `/etc/default/ufw`;
  rules from the `### tuple ###` lines of `/etc/ufw/user.rules` and
  `user6.rules`, which carry every field including the comment; and the
  framework files `before*.rules` and `after*.rules`.
- **Loaded state** comes from `iptables-save` and `ip6tables-save` (filter
  table) of the variant UFW uses, parsed by the netfilter reader.
- `ufw status` output is not parsed for state.
- Every parser is fuzzed (R74).

#### 2.2 Rule model and management (R37, R38)

- **Model.** `Rule{ID, Action (allow|deny|limit|reject), Direction
  (in|out|route), Proto (tcp|udp|any), From (CIDR|any), FromPort, To
  (CIDR|any), ToPort (port|range|list), InIface, OutIface, Family
  (v4|v6|both), Comment, Origin (darkpanel|manual)}`.
- **Tag.** Darkpanel rules carry the comment `darkpanel:<ulid>` optionally
  followed by ` <label>`. Rules without the tag are `manual`; changing or
  deleting one requires `manual: true` in the input and is shown as such in
  the diff, otherwise the plan fails with `firewall.manual_required`.
- **Schema patterns** (the only protection, since `ufw` has no `--`): ports
  `^[0-9]{1,5}(:[0-9]{1,5})?$` within 1–65535; CIDRs parsed by `netip` and
  re-rendered; interface names `^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,14}$`; comments
  `^[A-Za-z0-9 :._/-]{0,64}$` after the tag; protocols from the enum.
- **Writing** only through `ufw` with argument vectors.

Resources: `firewall.status` (configured and loaded state, rules with origin,
`active` and `inSync` flags). Actions (all `revertible`, agent, lock
`firewall`, guards overridable):

| Action | Input |
|---|---|
| `firewall.state.enable`, `firewall.state.disable` | none |
| `firewall.defaults.set` | incoming, outgoing, routed: allow\|deny\|reject |
| `firewall.logging.set` | off\|low\|medium\|high\|full |
| `firewall.rules.apply` | a list of operations: `add` (rule fields, optional `position`) and `delete` (`id`, or the rule's tuple plus `manual: true`) |
| `firewall.rule.add`, `firewall.rule.delete` | one operation; thin forms of `rules.apply` for the CLI |

#### 2.3 Lockout guard and evaluation (R40, S1)

- **Prediction.** The plan computes the configured state after the change and
  renders the chains UFW will load from it: the `ufw-user-*` chains from the
  rule tuples using UFW's documented translation, plus the framework files as
  they are, plus UFW's fixed skeleton (`ufw-before-*`, `ufw-user-*`,
  `ufw-after-*`, logging and policy chains) for an active UFW. Verify reads
  the loaded state after apply and compares the UFW-owned chains with the
  prediction; any difference fails verify with
  `firewall.prediction_mismatch`, which reverts. The wave 1 spike records the
  translation and skeleton on all four OS images as golden files, and the
  harness re-checks them.
- **Evaluation.** The netfilter evaluator walks the predicted `INPUT`,
  `OUTPUT` and `FORWARD` chains (including every non-UFW chain found in the
  loaded state, such as tailscaled's and Docker's, kept in their loaded
  positions) for a packet tuple `(family, in-interface, out-interface, source,
  destination, protocol, destination port, conntrack state NEW)`. It supports
  the matches UFW, Docker and tailscaled generate (`-i`, `-o`, `-s`, `-d`,
  `-p`, `--dport`, `--dports`, `-m conntrack --ctstate`, `-m addrtype`,
  `-m comment`, jumps, `RETURN`, `ACCEPT`, `DROP`, `REJECT`, `LOG`
  continuing). `limit` and `recent` matches, and any match it does not
  support, make the verdict `unknown`. tailscaled inserts `ts-input` and
  `ts-forward` at position 1 of `INPUT` and `FORWARD`, and `ts-input` accepts
  everything arriving on `tailscale0`, so UFW rules never govern tailnet
  traffic; the evaluator reproduces that from the loaded state and the
  exposure view's `tailnet` verdicts say so. With `IPV6=no` there are no UFW IPv6
  chains, and IPv6 flows are evaluated against whatever ip6tables holds.
- **Protected flows.**
  1. Each established TCP connection whose local socket is owned by `sshd`:
     a new connection from its remote address to its local address and port,
     arriving on the interface that holds the local address.
  2. When the controller runs on this host and the request came directly:
     from `Client.Addr` to each `PanelListen` address, arriving on the
     interface that holds that address.
  3. When the request came through an exposure: from `Via.ProxyAddr` to
     `Via.ListenerAddr` (in on the panel network's bridge), and either from
     the internet (a documentation address) to each `Via.Public` port through
     `FORWARD` to the proxy container (NPM), or outbound from this host to
     TCP 443 and 7844 on the internet (tunnel).
  4. When the controller runs on this host: from each `AgentPeers` address to
     each `AgentListen` address.
  5. Outbound from this host to the agent's controller address and port, on
     the interface the session currently uses.

  Tailscale SSH sessions are not netfilter flows (tailscaled handles them) and
  are covered by the Tailscale guard (4.1).
- **Verdict.** A flow that evaluates to drop, reject or `unknown` fails the
  guard `firewall.lockout` naming the flow. The guard is `overridable`; an
  override is recorded in the plan, the audit entry and the diff.

#### 2.4 Auto-revert (R41)

Every firewall action is revertible with the agent's revert timer. Setting
`revertTimeoutSeconds` (default 60, range 30–600) is the module's per-host
setting. The UI shows a countdown with Confirm and Revert; the CLI behaves as
in 1.13.

#### 2.5 Logical snapshots (R42)

- **Content.** A snapshot holds the firewall's whole logical state: a tar of
  UFW's state files (`user.rules`, `user6.rules`, `before.rules`,
  `before6.rules`, `after.rules`, `after6.rules`, `ufw.conf`,
  `/etc/default/ufw`, with mode, owner and presence), the rows of
  `firewall_container_rules`, the module's settings, and the Docker
  integration flag. The tar's members are validated on restore (exactly the
  declared paths, regular files only, size limit 4 MiB each).
- **Store.** `firewall_snapshots` (id, created, change id, actor, `system`
  flag, `pinned` flag, tar, logical state JSON, restore proofs). The engine's
  per-change snapshot is a reference to this row.
- **Restore** is crash-safe: one database transaction records the restore
  intent and writes the logical rows; then each file is written with
  `FileWriter.Restore` (temporary file and rename per file); then `ufw
  reload` (or `ufw disable` when the snapshot was inactive); then the intent
  is cleared. Startup recovery finishes an intent it finds by restoring the
  same snapshot again (idempotent).
- **Retention.** The last 50 unpinned snapshots, plus any referenced by an
  unconfirmed change, plus pinned ones. The first mutation on a host pins a
  `baseline` snapshot taken before it, which is never pruned.
- **Actions.** Resource `firewall.snapshots`; `firewall.snapshot.compare`
  (read) diffs two snapshots or one against now; `firewall.snapshot.restore`
  (revertible) restores one, and its diff lists every manual rule it would add
  or remove, which needs `manual: true`.
- **Break-glass.** `sudo darkpanel firewall snapshot restore --local <id>` and
  `sudo darkpanel firewall rollback --local [--change <id>]`, which restores
  the snapshot taken before the newest non-system change (or the named one)
  and confirms in the same command. These CLI commands live in
  `internal/cli/firewall*.go`.
- **Reconcile** (2.8) reads only committed state and does not run while the
  `firewall` lock is held, so it never re-applies an allow that a pending or
  reverted change removed.

#### 2.6 Docker integration (R43–R47)

- **Detection (R43).** Resource `firewall.docker`: the daemon's version and
  `FirewallBackend.Driver` (`iptables` when the field is absent); which
  netfilter variant holds Docker's chains (Docker runs the `iptables` on its
  own `PATH`, so the driver alone does not say), found by running both the
  `-legacy-save` and `-nft-save` tools and looking for `DOCKER-USER` and
  `DOCKER-FORWARD`; which variant holds UFW's chains; whether Docker's
  localhost-port protection is loaded in the `raw` table (it is off when the
  daemon runs with `DOCKER_INSECURE_NO_IPTABLES_RAW=1`); and the block state.
  Supported only when Docker is 28.0 or later, its driver is exactly
  `iptables`, its chains are in the same variant as UFW's, and its `raw`
  protection is loaded. In `nftables` mode Docker creates no `DOCKER-USER`
  chain, so a block could do nothing. Inconclusive detection is
  unsupported. Otherwise the resource says why (for example "Docker uses its
  nftables firewall backend; not supported in v1").
- **Block (R44).** `firewall.docker.enable` (revertible) writes marked block
  `docker` into `/etc/ufw/after.rules` and `after6.rules` and reloads UFW.
  The block is a self-contained filter section, written from Docker's and
  UFW's documented behaviour (no code or text from GPL ufw-docker). IPv4:

  ```
  *filter
  :DOCKER-USER - [0:0]
  :darkpanel-docker-deny - [0:0]
  -A DOCKER-USER -m conntrack --ctstate RELATED,ESTABLISHED -j RETURN
  -A DOCKER-USER -s 10.0.0.0/8 -j RETURN
  -A DOCKER-USER -s 172.16.0.0/12 -j RETURN
  -A DOCKER-USER -s 192.168.0.0/16 -j RETURN
  -A DOCKER-USER -s 100.64.0.0/10 -j RETURN
  -A DOCKER-USER -j ufw-user-forward
  -A DOCKER-USER -p tcp -m conntrack --ctstate NEW -d 10.0.0.0/8 -j darkpanel-docker-deny
  -A DOCKER-USER -p tcp -m conntrack --ctstate NEW -d 172.16.0.0/12 -j darkpanel-docker-deny
  -A DOCKER-USER -p tcp -m conntrack --ctstate NEW -d 192.168.0.0/16 -j darkpanel-docker-deny
  -A DOCKER-USER -p udp -m conntrack --ctstate NEW -d 10.0.0.0/8 -j darkpanel-docker-deny
  -A DOCKER-USER -p udp -m conntrack --ctstate NEW -d 172.16.0.0/12 -j darkpanel-docker-deny
  -A DOCKER-USER -p udp -m conntrack --ctstate NEW -d 192.168.0.0/16 -j darkpanel-docker-deny
  -A DOCKER-USER -j RETURN
  -A darkpanel-docker-deny -m limit --limit 3/min -j LOG --log-prefix "[DARKPANEL DOCKER DENY] "
  -A darkpanel-docker-deny -j DROP
  COMMIT
  ```

  IPv6 uses sources `fd00::/8` and `fd7a:115c:a1e0::/48` and destination
  `fd00::/8`. The private source ranges are the setting `privateRanges`
  (default as above; a value that excludes a Docker network's subnet is
  refused). Denying by private **destination** ranges protects every Docker
  network in those ranges from the moment it exists, so no protection waits
  for reconcile; a Docker network outside them is listed by `firewall.docker`
  as unprotected and makes the integration's health `degraded`.
- **Why internal traffic keeps working (R46).** Established traffic and
  traffic from the private source ranges (other containers, the LAN, the
  tailnet) return before any route rule or deny is reached; container
  outbound traffic leaves from a private source. Route rules therefore govern
  only traffic from outside the private ranges, which is what R45 asks.
- **Reload behaviour.** UFW loads every rules file with `iptables-restore
  -n`, and `iptables-restore` flushes a user-defined chain that the input
  declares even with `-n`, so the `:DOCKER-USER` and `:darkpanel-docker-deny`
  declarations empty both chains before the block's rules are added: rules
  never accumulate across reloads. The harness checks this on all four images,
  and the health check treats duplicates as broken.
- **`IPV6=no`.** UFW then loads no IPv6 chains and never reads
  `after6.rules`; when ip6tables exists it restores a plain IPv6 table with
  `DROP` policies and only `lo` accepted, without `-n`, which also removes
  Docker's IPv6 rules. The integration therefore reports a host with
  `IPV6=no` and any IPv6-enabled Docker network as `degraded` with that
  reason, and the exposure view evaluates IPv6 from the loaded table as it
  is.
- **Container rules (R45).** `firewall.container.allow` and
  `firewall.container.deny` (revertible) take `{selector, publishedPort,
  proto, from (CIDR, default any)}` where `selector` is a container name or a
  Compose `project/service`. The published port is translated to the
  container port through the container's port bindings. Desired state lives
  in `firewall_container_rules`. Each row is realised as one UFW route rule
  per container address (every replica, every network, each family):
  `allow` rules are appended, `deny` rules (an explicit deny for a source
  range within a broader allow) are inserted before every Darkpanel route
  allow, so a deny always wins. Removing an allowance is `firewall.container.deny`
  without `from`, which deletes the row and its rules. The preview shows the
  resulting route rules and the effective verdict per source.
- **Address changes.** Docker emits a container's `die` before its address
  can be given to another container, and the reconcile task consumes events
  in order, removing a container's rules on `die` and `destroy` before it
  handles any later event, and adding rules on `start`. A stale allow can
  only matter if a different container gets the same address and publishes
  the same container port, and only while the agent is not consuming events
  (agent stopped or restarting); on start the agent runs a full reconcile
  before it serves requests. That residual window is recorded in the threat
  model, and the harness recreates containers under continuous scanning,
  including across an agent restart, and expects no reachable port that no
  rule allows.
- **Health (R47).** The reconcile task checks, for both families, that the
  block is present in the file, that the loaded `DOCKER-USER` chain equals the
  block's rules exactly once and in order, and that `FORWARD` jumps to
  `DOCKER-USER` before Docker's own accept rules. Anything else sets
  `firewall.docker` health to `broken` with the reason; the UI offers
  `firewall.docker.repair` (revertible), which rewrites and reloads.
  `firewall.docker.disable` removes the block and reloads, and verify checks
  that `darkpanel-docker-deny` is gone from the loaded state.

#### 2.7 Exposure view (R48, R49)

- **Rows.** Resource `firewall.exposure`: one row per (protocol, address,
  port, family), merging a listening socket with the Docker published port
  it belongs to (a `docker-proxy` socket is shown as its container's port).
- **Verdicts** per source class: `internet` (evaluated from a documentation
  address outside every private range, arriving on the default-route
  interface), `tailnet` (from 100.64.0.0/10 and fd7a:115c:a1e0::/48 on
  `tailscale0`), `localhost`. Each verdict is `open`, `closed`, `filtered`,
  `partial` (open for some sources only, with the rule that decides) or
  `unknown` (the evaluator met an unsupported match); `decidedBy` names the
  rule (UFW tuple and tag, a framework file line, a Docker or tailscaled
  chain, the block step, or "bound to loopback").
- **Published ports** follow Docker's path: `DNAT` in `nat PREROUTING`, then
  `FORWARD` through `DOCKER-USER` (the block, then route rules) and Docker's
  chains; with the integration off they are `open` to the internet whatever
  `INPUT` says, decided by Docker's chain.
- **Harness check (R49).** The scanner probes every row's address and port
  from the internet network for both families; TCP must match exactly;
  for UDP an `open` verdict must not be scanned `closed`, and a `closed` or
  `filtered` verdict must not get a UDP response. `partial` rows are probed
  from both an allowed and a denied scanner address.

#### 2.8 Background task `firewall.reconcile`

Wakes on Docker events (container start, die, destroy, network connect and
disconnect, network create) and every 5 minutes. It recomputes route rules
from `firewall_container_rules` and current container addresses, and checks
health (2.6). Changes go through `firewall.rules.apply` as actor
`system:firewall.reconcile` (1.4).

#### 2.9 Uninstall (R30)

The firewall `Uninstaller` takes `firewall.rules`:

- `remove` (default): delete every Darkpanel-tagged rule; manual rules stay;
- `keep`: leave every rule; tags stay;
- `baseline`: restore the pinned baseline snapshot, after printing which
  manual rules it would add or remove.

In every case the `docker` block is removed and UFW reloaded.

#### 2.10 Web pages

`/hosts/:id/firewall`: configured and loaded state (with an "out of sync"
banner), rules table with origin badges, add and delete through
`ChangeDialog`, snapshots (list, compare, restore), Docker integration
(detection, enable, health, repair) and the exposure table with filters. The
container action slot gets "Allow" and "Deny".

### 3. Docker management (R50–R55)

Module `docker`; agent side; lock key `docker`; preflight: socket present,
API 1.44 or later.

| Action | Class | Undo or compensation |
|---|---|---|
| `docker.container.start`, `docker.container.stop` | compensable | each other |
| `docker.container.restart` | compensable | undo: start the container if it is stopped; a restart itself is not reversed |
| `docker.container.remove` | irreversible (typed name) | — |
| `docker.container.inspect`, `docker.container.logs` | read | — |
| `docker.image.pull` | compensable | undo: remove the image only if the plan recorded the tag absent; otherwise nothing (a re-pointed tag is not restored) |
| `docker.image.remove`, `docker.image.prune` | irreversible | — |
| `docker.network.create`, `docker.volume.create` | compensable | undo: remove the created object if unused |
| `docker.network.remove`, `docker.volume.remove` | irreversible | — |
| `docker.network.connect`, `docker.network.disconnect` | compensable | each other |
| `docker.compose.up` | compensable | undo: `down` limited to the containers this change created (recorded in output) |
| `docker.compose.down` | irreversible (typed project name) | — (removes containers and their writable layers) |
| `docker.compose.pull`, `docker.compose.restart` | compensable | undo: `up` the project; a pull or restart itself is not reversed |

- Resources: `docker.engine`, `docker.containers` (with a stats summary),
  `docker.images`, `docker.networks`, `docker.volumes`, `docker.projects`.
- Streams: `docker.logs` (follow, since, tail), `docker.stats`, `docker.pull`
  (keyed by change id).
- **Secrets in inspect.** `docker.container.inspect` returns environment
  variable names with values replaced by `[redacted]` for every container;
  `docker.container.reveal-env` (read, admin scope, audited) returns values,
  and refuses Darkpanel-owned containers. Logs of Darkpanel-owned containers
  are filtered for the secrets their module registered (6, 7.2).
- **Prune (R51).** The plan lists every image to be removed and the bytes
  reclaimed; apply removes exactly that list (re-checked against the state
  hash).
- **In use (R52).** Removing a network or volume used by any container fails
  the plan with `docker.in_use` naming the containers.
- **Ownership.** Mutating actions on objects labelled `darkpanel.module`
  refuse with `docker.owned` (1.8).
- **Compose (R53).**
  - **Trust boundary.** Compose files are operator-authored and trusted like
    any root-run configuration; Darkpanel never writes them. Running them is
    an admin-scope action, and the plan says so ("runs the project's Compose
    file as root").
  - **Discovery.** Projects come from container labels
    (`com.docker.compose.project`, `.project.working_dir`,
    `.project.config_files`, `.project.environment_file`) and are recorded in
    `docker_projects` (name, directory, files, env files, profiles seen,
    config hash) when first seen, so a project stays listed after `down`.
  - **Validation.** The directory and every file must be absolute, exist,
    and be owned by root or the directory's owner, with no symlink in the
    path (the file provider's rules); otherwise `docker.compose_unavailable`
    with the reason. A changed config hash since the last run is shown in the
    plan.
  - **Running.** `/usr/bin/docker compose --project-directory <dir> -p <name>
    -f <file>... [--env-file <f>]... <verb>` with fixed verb flags (`up -d`,
    `down`, `pull`, `restart`), environment `HOME=/root` only, plugin path
    checked to be root-owned and not writable by others. Success is the exit
    code; the result is read back from the Engine API.
- **Exposure in container views (R55).** The container page shows the
  `firewall.exposure` rows of its published ports and the container action
  slot's buttons; firewall's Allow and Deny plan
  `firewall.container.allow` and `deny` directly on the same host, with the
  firewall's diff, guards and revert countdown. Without an available firewall
  module or a supported integration they are disabled with the reason.
- **Web.** `/hosts/:id/docker` with containers, images, networks, volumes and
  projects tabs; a container page with logs (follow), stats, inspect and
  exposure.

### 4. Tailscale

#### 4.1 Node (R56, R57) — module `tailscale`, agent side, lock key `tailscale`

- **Status (R56).** Resource `tailscale.status`: backend state, self
  (addresses, DNS name, tags), peers (online, addresses, connection `direct
  <endpoint>`, `derp <region>` or `peer-relay`), preferred DERP region.
  Resource `tailscale.latency` measures DERP latency itself, since the local
  API has no netcheck endpoint: STUN binding requests from the agent to each
  region's STUN endpoints in the DERP map (from the local client), three
  probes per region, median. A small hand-written STUN client; no Tailscale
  library.
- **Preferences (R57).** `tailscale.prefs.set` (revertible): `wantRunning`,
  `advertiseRoutes`, `exitNode`, `advertiseExitNode`, `acceptRoutes`,
  `shieldsUp`, `runSSH`. It patches only the fields given (masked prefs); the
  snapshot holds the previous values of exactly those fields, and revert
  patches them back.
- **Guard `tailscale.link` (overridable).** The agent classifies each
  protected flow of 2.3 (and each live Tailscale SSH session, read from the
  local client's status) by whether it depends on Tailscale: it arrives on or
  leaves through `tailscale0`, or its route (`netlink` route lookup) uses a
  subnet route or exit node through `tailscale0`, or it is a Tailscale SSH
  session. A change fails the guard when it would break a dependent flow:
  `wantRunning: false`; `shieldsUp: true` while an inbound flow arrives over
  Tailscale or a Tailscale SSH session is live (Tailscale SSH is handled by
  tailscaled's own network stack, before netfilter, and shields-up blocks
  it); `runSSH: false` while a Tailscale SSH session is live;
  `acceptRoutes: false` or a different `exitNode` while a flow's route uses
  them.
- **Login and logout.** `tailscale.node.login` (compensable, undo: log out
  only if the node was logged out before): `authKey` (secret), `hostname`,
  `advertiseTags`. `tailscale.node.logout` is irreversible (typed hostname)
  and carries the same guard.
- **Install and update.** `tailscale.package.install` (compensable; input
  optional `version` and `hold`; undo: remove the package if this change
  installed it, or reinstall the previous version if this change changed it
  and the repository still has it, otherwise the step needs repair) and
  `tailscale.package.update` (irreversible, typed hostname: a package
  upgrade cannot be reliably undone); install is available without
  tailscaled (1.7).
  - Files: `/usr/share/keyrings/tailscale-archive-keyring.gpg` (fingerprint
    pinned in code) and `/etc/apt/sources.list.d/tailscale.list`. If either
    exists and is not byte-identical to what Darkpanel would write, install
    refuses with `tailscale.repo_conflict`; if identical, it is used but not
    recorded as owned, so uninstall leaves it.
  - `apt-get` runs with `-o DPkg::Lock::Timeout=120`, `update` limited to
    that source list, then `install -y tailscale`.
  - The repository line uses the OS codename from `/etc/os-release` and the
    `stable` track; unsupported OS or architecture refuses.
- **Module preflight** checks only the OS; `tailscale.status` and every action
  except install report `tailscale.unavailable` through per-action
  availability when the socket does not answer.
- **Web certificate (ADR 0010).** When the controller's host is on a tailnet,
  the agent's `tailscale.cert.get` (read, controller-only caller) returns the
  `tailscale cert` pair for the node's MagicDNS name from the local client;
  the controller uses it for its Tailscale listeners and adds the MagicDNS
  name to the web root certificate's names, renewing at 2/3 of the
  certificate's life and on address changes. Without it the existing web
  root path is unchanged.
- **Web.** `/hosts/:id/tailscale` with status, peers, latency, preferences and
  install or update.

#### 4.2 Tailnet (R58–R60) — module `tailnet`, controller side, lock key `tailnet`

- **Connect (R58).** `tailnet.account.connect` (compensable, by
  `tailnet.account.disconnect`, which is irreversible: it deletes the sealed
  credentials): tailnet name,
  OAuth client id and secret (sealed), and the tags the client may assign
  (`tagsAllowed`). Connect obtains a token and probes each capability with a
  harmless read; the scopes needed are recorded in 4.2a. One tailnet per
  controller in v1.
- **4.2a Scopes.** `devices:core`, `devices:routes`, `auth_keys`,
  `policy_file`, and `devices:posture_attributes`, which `policy_file`
  requires (it also requires `devices:core:read`, covered by
  `devices:core`). A client holding `auth_keys` or `devices:core` must have
  tags, and keys it creates must carry those tags or tags they own through
  `tagOwners`. Tokens come from `POST /api/v2/oauth/token` and last one hour;
  the client caches one and renews it at 50 minutes.
- **Devices.** Read `tailnet.device.list`, `tailnet.policy.get`.
  Compensable: `tailnet.device.authorize` (undo: none needed beyond
  re-deauthorization, which the API offers; the spike settles it, else the
  action is irreversible); `tailnet.device.set-routes` (approve routes and
  exit node; undo: set the previous list stored in the output);
  `tailnet.authkey.create` (undo: delete the created key; the key is shown
  once). Irreversible (typed): `tailnet.device.expire`,
  `tailnet.device.delete`.
- **Auth keys** require `tags`, each within `tagsAllowed`.
- **Policy (R59).** `tailnet.policy.set` (compensable): input is the full
  HuJSON text. Plan fetches `GET /api/v2/tailnet/{t}/acl` with `Accept:
  application/hujson` and its `ETag`, validates the new text with `POST
  .../acl/validate` (an empty 200 body is valid; otherwise `{message, data}`
  becomes `tailnet.policy_invalid`), and renders a line diff. Apply posts with
  `If-Match` on that ETag; a 412 fails with `tailnet.policy_conflict`. The output stores the before-text and the
  after-ETag. Undo posts the before-text with `If-Match` on the after-ETag;
  if the policy changed since, undo refuses with `tailnet.policy_conflict`
  and the change needs repair, so later edits are never erased.
- **Structural edits.** Other modules edit the policy through
  `tailnet.policy.patch` (compensable, same ETag rules): a list of JSON Patch
  operations applied with `github.com/tailscale/hujson`'s patch support, so
  comments and unrelated content are preserved.
- **Join (R60).** `tailnet.node.join` (composite): steps `install`
  (`tailscale.package.install`, skipped when present), `key`
  (`tailnet.authkey.create`, one use, pre-authorized, tags from input), and
  `login` (`tailscale.node.login` with the key from `key`). In the same step
  as installation: the Hosts page's "Add host" dialog and `darkpanel host
  create --tailnet-join --tags <t>` record a join request; when the new agent
  completes enrollment, the controller starts `tailnet.node.join` for it as
  actor `system:enroll-join`, and the host page shows its progress. A failure
  is shown on the host with a retry.
- **Web.** `/tailnet` with devices, approvals, auth keys and a policy editor
  with validate and diff.

#### 4.3 DERP (R61–R64) — module `derp`

- **Artifact.** The release workflow builds `tailscale.com/cmd/derper` at
  `release/derper.version` for amd64 and arm64 (`CGO_ENABLED=0`), names them
  `derper-<derper version>-linux-<arch>`, and lists them in a `derper.json`
  manifest (`{darkpanelVersion, derperVersion, artifacts: {arch: sha256}}`)
  that is itself listed in the release's signed `checksums.txt`. The agent
  takes the derper artifact only from the release its own Darkpanel version
  belongs to, verified through that release's signature and manifest, so the
  derper version is fixed by the Darkpanel release. The harness builds them
  with test keys and caches the build keyed by `release/derper.version`.
- **Agent half.** `derp.server.install` (compensable, undo: uninstall what it
  installed; lock `derp`): installs `/usr/local/lib/darkpanel/derper`,
  `/etc/systemd/system/darkpanel-derper.service` and `/var/lib/darkpanel/derp/`
  as owned paths, and starts the unit. `derp.binary.update` (compensable, lock
  `derp`): a durable swap like the Darkpanel updater's (ADR 0007): the new
  binary is staged and verified, the old one kept as `derper.prev` (owned),
  the phase recorded before each step; agent startup recovery finishes or
  rolls back an interrupted swap; a failed health check (TLS `/derp/probe`,
  STUN, and a relayed round trip between two agents when the controller asks
  for it) restores `derper.prev`.
- **Certificates (R61).** Two modes:
  - `letsencrypt`: `-certmode=letsencrypt -hostname=<name> -certdir=/var/lib/darkpanel/derp/certs`,
    with the hostname's DNS pointing at the host; derper renews itself; port
    80 serves the ACME challenge.
  - `ip`: `-certmode=manual -hostname=<public IP> -certdir=...`; derper
    creates a self-signed certificate for the address on first start, and
    clients pin it: the DERP map node gets `HostName: "<ip>"` and `CertName:
    "sha256-raw:<hex SHA-256 of the certificate DER>"`, which
    `derp.server.install` reads back and stores in `derp_servers`. The
    certificate does not expire in a way clients check (they pin it);
    replacing it means a new pin through `derp.region.set`.
  Both run with `-a :443 -http-port 80 -stun -stun-port 3478`.
- **Client verification (R62).** `-verify-clients` checks every client's node
  key against the host's own tailscaled and fails closed when tailscaled does
  not answer; no fail-open flag applies to it (`-verify-client-url-fail-open`
  only concerns URL verification, which Darkpanel does not use). Upstream
  requires derper and tailscaled from the same version. The DERP composites
  therefore include a step `tailscale.package.install` with `version:
  <derper version>` and `hold: true` (the tailscale module owns the package;
  `apt-mark hold` keeps it), `tailscale.package.update` refuses a held
  package, and `derp.server.upgrade` (composite: package step, then
  `derp.binary.update`) moves both together. Preflight: the host is on
  the tailnet. Turning verification off needs input `verifyClients: false`
  and shows a warning.
- **Ports.** 80/tcp, 443/tcp and 3478/udp; preflight refuses a host where any
  is already bound (for example by Nginx Proxy Manager).
- **Deploy.** `derp.server.deploy` (composite): steps `tailscaled`
  (`tailscale.package.install` at the derper version, held), `install`,
  `region` (optional `derp.region.set`, controller), `ports` (one
  `firewall.rules.apply` adding the three rules, labelled `derp`, last as the
  revertible step).
- **Region (R63).** `derp.region.set` and `derp.region.remove` (compensable,
  each other's compensation, controller): edit the policy's `derpMap` through
  `tailnet.policy.patch` with region id 900–999 (refused if taken:
  `derp.region_taken`), region code and name, the node entry (including what
  the `ip` mode needs, read from `derp_servers`), and `omitDefaultRegions`
  only when the input sets it; removing a region does not touch
  `omitDefaultRegions` unless asked.
- **Probe (R64).** `derp.server.probe` (read): TLS `/derp/probe` and STUN
  from the controller and chosen agents, and a relayed round trip between two
  chosen agents forced through the region; the nodes using the region come
  from each agent's `tailscale.status`.
- Controller table `derp_servers` (host, mode, hostname or address, region
  id, derper version, certificate pin when needed). Web: `/derp`.

### 5. Action catalogue

Classes: R revertible, C compensable (with its compensation), I irreversible
(typed confirmation), Rd read, X composite (compensable through its steps'
unwind). Side: A agent, C controller. Names omit the module prefix.

| Module | Action | Class | Side | Locks |
|---|---|---|---|---|
| firewall | `state.enable`, `state.disable`, `defaults.set`, `logging.set`, `rules.apply`, `rule.add`, `rule.delete`, `snapshot.restore`, `docker.enable`, `docker.disable`, `docker.repair`, `container.allow`, `container.deny` | R | A | `firewall` |
| firewall | `snapshot.compare` | Rd | A | — |
| docker | the table in 3; `container.reveal-env` Rd | — | A | `docker` |
| tailscale | `prefs.set` | R | A | `tailscale` |
| tailscale | `node.login` (undo: log out if it was logged out), `package.install` (undo: remove if this change installed it) | C | A | `tailscale` |
| tailscale | `node.logout`, `package.update` | I | A | `tailscale` |
| tailscale | `cert.get` | Rd | A | — |
| tailnet | `account.connect` (by `account.disconnect`) | C | C | `tailnet` |
| tailnet | `account.disconnect`, `device.expire`, `device.delete` | I | C | `tailnet` |
| tailnet | `device.authorize` (undo: de-authorize), `device.set-routes` (undo: previous list), `authkey.create` (undo: delete the key), `policy.set`, `policy.patch` (undo: ETag-conditional restore) | C | C | `tailnet` |
| tailnet | `device.list`, `policy.get` | Rd | C | — |
| tailnet | `node.join` | X | C | the steps' |
| derp | `server.install` (undo: uninstall what it installed), `server.uninstall` (by `server.install`), `binary.update` (undo: restore `derper.prev`) | C | A | `derp` |
| derp | `region.set`, `region.remove` (each other) | C | C | `tailnet` |
| derp | `server.probe` | Rd | C | — |
| derp | `server.deploy` (tailscaled, install, region, ports), `server.upgrade` (tailscaled, binary), `server.remove` (region remove, port rules delete, uninstall) | X | C | the steps' |
| npm | `instance.deploy` (undo: remove what it created), `instance.upgrade` (undo: durable rollback), `network.attach`, `network.detach` (each other), `proxy-host.add`, `proxy-host.update`, `proxy-host.remove`, `access-list.add`, `access-list.remove` (inverse, or undo bound to the id), `certificate.request` (undo: delete the created certificate) | C | A | `npm` |
| npm | `instance.remove` (deletes volumes), `certificate.remove` | I | A | `npm` |
| npm | `instance.deploy-public` (deploy, ports), `expose.container` (network, cert, proxy), `expose.remove` (proxy remove, network detach) | X | C | the steps' |
| cloudflare | `account.connect` (by `account.disconnect`), `tunnel.create` (undo: delete the created tunnel), `hostname.add`, `hostname.remove` (each other) | C | C | `cloudflare`, `cloudflare:<tunnel>` |
| cloudflare | `account.disconnect`, `tunnel.delete` | I | C | `cloudflare` |
| cloudflare | `tunnel.list` | Rd | C | — |
| cloudflare | `connector.run`, `connector.stop` (each other), `network.attach`, `network.detach` (each other) | C | A | `cloudflare` |
| cloudflare | `connector.deploy` (tunnel, token, run), `expose.container` (network, hostname), `expose.remove` (hostname remove, network detach) | X | C | the steps' |
| panel | `network.create` (undo: remove), `network.remove` (by create) | C | A | `panel` |
| panel | `listener.open`, `listener.close` (each other) | C | C | `panel` |
| panel | `exposure.add` (steps in 8) | X | C | the steps' |
| panel | `exposure.remove` (local revocation first, 8; `BreakGlass`) | C, by `exposure.add` with the stored input | C | `panel` |

The wave that builds a module may add read actions and resources; any other
change to this table is a spec change.

### 6. Exposure: Nginx Proxy Manager (R65–R68) — module `npm`

- **Bootstrap.** Since 2.13 NPM has no default admin; until the first user
  exists it is in setup mode (`GET /api` reports `setup: true`) and `POST
  /api/users` creates an admin without authentication. The image's
  `INITIAL_ADMIN_*` variables are not used, because NPM logs that password in
  plain text. Deploy starts the container with 81 published on 127.0.0.1
  only and creates the admin through the setup API from the agent before the
  step completes; the admin email is `admin@darkpanel.invalid` and the
  password 32 random bytes, sealed in `npm_instances`. A deploy that finds
  `setup: false` on a container it just created fails. The password is never
  in the container's environment, the agent's logs or the log stream (logs
  of `darkpanel-npm` are filtered for it). Tokens come from `POST
  /api/tokens` with `{identity, secret}`.
- **Deploy (R65).** `npm.instance.deploy` (agent, compensable, undo: remove
  what it created; lock `npm`): network `darkpanel-npm`, volumes
  `darkpanel-npm-data` and `darkpanel-npm-letsencrypt`, container
  `darkpanel-npm` from the pinned digest, all labelled `darkpanel.module=npm`,
  with 80 and 443 published on all addresses and 81 on 127.0.0.1 only.
  Preflight refuses a host where 80 or 443 is already bound or a DERP server
  is installed. The controller and the agent reach the API on 127.0.0.1:81.
- **Opening 80 and 443.** NPM's ports are Docker-published, so with the
  integration enabled they are opened with `firewall.container.allow` for
  `darkpanel-npm`; without the integration they are already reachable and the
  plan says so. The composite `npm.instance.deploy-public` has steps
  `deploy` and `ports` (revertible, last); the user confirms it after
  checking the ports from outside.
- **Management (R66).** Agent actions call the local API with the stored
  credentials: `npm.proxy-host.add`, `.update`, `.remove`,
  `npm.access-list.add`, `.remove` (compensable, each with its inverse or an
  undo bound to the created id), `npm.certificate.request` (compensable,
  undo: delete the certificate it created; HTTP-01 through NPM's
  `provider: letsencrypt`, the ACME account being the admin's email),
  `npm.certificate.remove`
  (irreversible). Read actions list each.
- **Expose container (R67).** `npm.expose.container` (composite) requires a
  public instance (`npm.instance.deploy-public` confirmed, so 80 and 443 are
  already open and HTTP-01 can reach NPM; otherwise `npm.not_public`). Steps:
  `network` (`npm.network.attach`, attaching the owned `darkpanel-npm` to the
  target's network; `docker.network.connect` refuses owned containers),
  `cert` (`npm.certificate.request`), `proxy` (`npm.proxy-host.add`
  forwarding to `<container>:<port>` with forced SSL and HSTS). It has no
  revertible step.
- **Upgrade (R68).** `npm.instance.upgrade` (agent, compensable, lock `npm`)
  is a durable transaction in `npm_upgrades` (phase per step, fsynced):
  record the current container configuration and digest; stop the container;
  archive both volumes to `/var/lib/darkpanel/agent/backups/npm/<time>/` with
  a checksum, verified after writing; recreate on the new digest; wait up to
  120 s for the API. On failure, or on agent startup with an unfinished
  transaction, restore the volumes from the verified archive and recreate the
  previous container from the recorded configuration. The last three
  archives are kept.
- **Web.** `/hosts/:id/npm`, and "Expose" in the container action slot.

### 7. Exposure: Cloudflare Tunnel (R69–R71) — module `cloudflare`

#### 7.1 Account and tunnels (R69, R70)

- **Account.** `cloudflare.account.connect` (controller, compensable by
  `cloudflare.account.disconnect`): account id and API token (sealed). Token
  verification shows only that the token is valid, so connect probes
  capabilities with harmless reads (list tunnels on the account, list zones)
  and maps a 403 to `cloud.permission_missing` naming the permission. Zones
  the token can edit DNS in are discovered by listing zones and are confirmed
  on first use. The required permissions are "Cloudflare Tunnel Write" on the
  account (shown as Cloudflare Tunnel Edit in the dashboard) and "DNS Write"
  on the zones used.
- **Tunnels.** `cloudflare.tunnel.create` (controller, compensable, undo:
  delete the created tunnel; name `darkpanel-<change id>` so an uncertain
  create is resolved by lookup, 1.9) creates a remotely managed tunnel with
  `POST /accounts/{a}/cfd_tunnel` and `config_src: cloudflare`; its token is
  read from `GET .../cfd_tunnel/{id}/token`. Connector status comes from
  `GET .../cfd_tunnel/{id}` (`status`) and `.../connections`.
  `cloudflare.tunnel.delete` is irreversible (typed name) and refuses while
  hostnames or connectors exist. `cloudflare.tunnel.list` (read) shows
  connector status.
- **Lock keys.** `cloudflare:<tunnel id>` for configuration changes, so
  hostname changes on one tunnel are serialised.

#### 7.2 Connector (R70)

- `cloudflare.connector.run` (agent, compensable, undo: stop and remove what
  it created; lock `cloudflare`): container `darkpanel-cloudflared-<tunnel>`
  from the pinned digest, labelled `darkpanel.module=cloudflare`, restart
  policy `unless-stopped`, running `tunnel --no-autoupdate run` with the token
  read from a file with `--token-file` (cloudflared 2025.4.0 or later): the
  token is sealed in the agent database and written to a root-only file
  `/var/lib/darkpanel/agent/cloudflare/<tunnel>/token` (mode 0400, owned
  path) that is bind-mounted read-only; the token is never in the
  container's environment or command line.
- `cloudflare.connector.deploy` (composite): steps `tunnel` (optional
  `cloudflare.tunnel.create`), `token` (fetch the token, sealed in the step
  row), `run`.

#### 7.3 Public hostnames (R71)

- `cloudflare.hostname.add` and `cloudflare.hostname.remove` (controller,
  compensable, each other's compensation, lock `cloudflare:<tunnel id>`):
  durable two-step operations in `cloudflare_hostnames` (hostname, tunnel,
  service, DNS record id, phase). Add reads the tunnel configuration, inserts
  the rule before the catch-all (a catch-all `http_status:404` is required
  last, and added if absent), and writes it with `PUT
  .../configurations`, which replaces the whole configuration and has no
  version check: the read-modify-write is serialised by the lock, and the
  configuration is re-read just before writing and compared with the one the
  plan read, refusing with `cloud.conflict` if someone else changed it. It
  then creates the proxied CNAME to `<tunnel id>.cfargotunnel.com`
  and records its id. Remove deletes only the recorded record id and the
  rule it added. Controller startup recovery finishes or undoes an
  operation found mid-phase. Unrelated ingress rules are never changed.
- `cloudflare.expose.container` (composite): steps `network`
  (`cloudflare.network.attach`, attaching the owned cloudflared container to
  the target's network) and `hostname` (service `http://<container>:<port>`).
  No inbound port opens.
- Controller tables `cloudflare_accounts`, `cloudflare_tunnels`,
  `cloudflare_hostnames`. Web: `/cloudflare`, and "Expose via tunnel" in the
  container action slot.

### 8. Exposure: the panel itself (R72) — module `panel`

- **Expose.** `panel.exposure.add` (controller composite), input
  `via: npm|tunnel`, the public hostname, and the controller's host (the
  only host allowed: `panel.wrong_host`). Refused with
  `panel.totp_incomplete` while any admin is pending TOTP and with
  `panel.already_exposed` when an exposure exists.
- **Steps.**
  1. `network`: network `darkpanel-panel` (labelled `darkpanel.module=panel`)
     with a free /29 chosen at plan time from 172.31.255.0/24 downwards,
     gateway `.1`, proxy address `.2`, created by `panel.network.create`.
  2. `attach`: attach the proxy container (NPM or cloudflared) to it with the
     static address `.2` (`npm.network.attach` or
     `cloudflare.network.attach` with a fixed address).
  3. `listen`: open the runtime listener on `<gateway>:9443` (setting
     `exposurePort`, default 9443), allowed peer `.2`, trusted header per 1.10.
  4. `proxy`: the NPM proxy host (upstream `https://<gateway>:9443`, forced
     SSL, HSTS, certificate verification of the internal hop off) or the
     tunnel hostname (service `https://<gateway>:9443`, `noTLSVerify`).
  5. `ports`: `firewall.rules.apply` adding `allow in on <bridge> from
     <proxy> to <gateway> port 9443 proto tcp`, revertible, last.
- **Guardrails.** Login over the exposure requires TOTP as everywhere; every
  page shows a banner "Panel exposed via <npm|tunnel> at <host>" with a link
  to remove it; the audit log marks proxied requests and keeps both
  addresses (1.10).
- **Remove.** `panel.exposure.remove` revokes locally first, durably: it
  closes the runtime listener and marks the exposure `revoked` in one
  transaction, so the panel is unreachable through the proxy even if the
  remote cleanup fails; then it removes the proxy host or hostname, the
  network attachment, the network and the firewall rule, retrying the remote
  steps and leaving the exposure `needs_repair` if they keep failing. A
  remove request that arrived through the exposure gets its response before
  the listener closes. Break-glass: `sudo darkpanel panel exposure remove
  --local` over the controller socket (a controller-side `BreakGlass`
  action) performs the local revocation without the web UI.

### 9. Hardening (R73–R79)

- **Threat model (R73).** `docs/security/threat-model.md`: assets, actors,
  trust boundaries (browser–controller, proxy–controller,
  controller–agent, agent–host, controller–cloud APIs) and, per area
  (authentication, agent protocol, command execution, Compose execution,
  secret handling, firewall, cloud credentials, exposure), threats,
  mitigations and residual risk.
- **Security review (R73).** The owner's two-reviewer panel
  (`dr-superpowers:judge-fable` and Codex `gpt-6-astra`, one shared brief).
  Findings go into `docs/superpowers/registers/<date>-v1-security-review.md`;
  every critical and high finding is fixed and re-reviewed; medium and low
  are fixed or deferred by the owner.
- **Fuzzing (R74).** Wave 8 starts by writing `docs/security/parsers.md`, an
  inventory of every parser of host-tool output, config files and untrusted
  input in the repository (found by searching for parse functions and
  readers), each mapped to its fuzz target. Known members: UFW tuple lines,
  `ufw.conf`, `/etc/default/ufw`, `*-save` output, `iptables --version`,
  `/proc/net/*`, os-release, cpuinfo, meminfo, loadavg, uptime, marked
  blocks, HuJSON patching, the nmap parser in `test/harness/nmap.go`, the
  release manifest and checksum parsers, the backup archive header.
  `.github/workflows/fuzz.yml` runs each target for 30 s on every push and 10
  minutes nightly; a test fails when a parser in the inventory has no target.
- **Upgrade matrix (R75, S5).**
  - Supported upgrade graph: within a major version, from any earlier
    release; agents one minor behind the controller. The updater's refusal of
    cross-major updates stays. Pre-1.0 builds (0.x) are not upgradable to
    1.0; the release notes say so, and the owner's production host is
    reinstalled from 1.0.0-rc.1 (section 12).
  - Harness `TestUpgradeMatrix`, with test keys: a baseline built from the
    commit the wave starts from, labelled `1.0.0`, and the build under test
    labelled `1.1.0`, exercise the upgrade path and an agent one minor behind
    on all four OS; a deliberately broken `1.1.1` checks rollback within 2
    minutes. Once real `v1.*` tags exist, every one of them is added as a
    source. Pre-release ordering (`-rc.N`) is already handled by the updater
    and gets a unit test.
- **Footprint (R76, S6).** Harness `TestFootprint`.
  - Agent: the real agent on an enrolled VM, idle with every module enabled
    and background tasks running; RSS sampled every 10 s for 10 minutes after
    5 minutes of warm-up; the maximum must be ≤ 40 MB.
  - Controller: 10 enrolled agents. The nine extra agents are harness-only
    processes built with a `harness` build tag that adds `--state-dir`, which
    moves the database, keys, TLS credentials, sockets, lock file and update
    directory under one directory; each runs with only `hostinfo` enabled and
    no background tasks, as its own enrolled host. The controller's RSS is
    sampled the same way while the UI's host list is refreshed every 15 s;
    the maximum must be ≤ 150 MB.
- **Accessibility (R77).** `docs/accessibility.md` lists the core flows
  (login with TOTP, first-admin recovery instructions page, host list, a
  firewall change with confirm and revert, container actions, expose a
  container) against WCAG 2.1 AA criteria, each marked automated or manual.
  Automated: `vitest-axe` on every page of those flows, keyboard-only tests
  (focus order, focus visible, dialogs trap and return focus). Manual, in a
  browser through Playwright with screenshots kept as evidence: contrast in
  both themes, 200% zoom and 320 px reflow, screen-reader labels on the
  change dialog.
- **Documentation (R78, R79).** `docs/user/` (install, first admin, every
  module, lockout recovery, uninstall), `docs/dev/module-guide.md`, and
  `docs/api/` generated from `/api/v1/openapi.json` by `make api-docs`, with
  a CI check that the committed copy is current.

### 10. Release (R80, R81)

- **Backup format.** `darkpanel controller backup --out <file>` (over the
  controller socket): a versioned file — header `{magic "DPBK", version 1,
  argon2id t=3 m=64 MiB p=4, 16-byte salt, 24-byte nonce prefix}`
  authenticated as associated data — followed by the STREAM construction of
  XChaCha20-Poly1305 over 64 KiB chunks (chunk counter and a final flag in the
  nonce), so memory stays bounded whatever the database size. The plaintext
  is a tar of an SQLite online backup of `controller.db` and of
  `controller.key`. The passphrase is read from the terminal twice, or from
  `--passphrase-file` (root-owned, 0600); at most one backup runs at a time.
- **Restore.** `darkpanel controller restore --in <file>`, root, with both
  services stopped (refused otherwise), on a host after
  `system install --role controller`:
  1. decrypt and authenticate the whole file into a staging directory
     `/var/lib/darkpanel/controller/restore-<time>/` (mode 0700); refuse on
     any authentication failure or truncation;
  2. validate: tar members are exactly the two files; the database passes
     `PRAGMA integrity_check`, its schema is not newer than the binary's, and
     the key opens a sealed canary value in it;
  3. write a restore journal, move the current database (with `-wal` and
     `-shm`) and key to `.pre-restore`, move the staged pair into place with
     correct owner and mode, then clear the journal;
  4. on start after a crash, a journal found by `restore` or by the
     controller is completed or rolled back from `.pre-restore`.
  Tests cover wrong passphrase, truncation, tampering, a large database
  within the memory bound, and a crash at each step; the harness restores a
  controller backup onto a fresh VM and re-enrolls an agent.
- **Runbook (R81).** `docs/runbook.md`: lockout (firewall break-glass,
  Tailscale prefs revert, panel exposure removal), failed update (rollback,
  recover), lost controller (restore from backup, re-enroll agents).
- **Release candidate.** `CHANGELOG.md` and `docs/versioning.md` updated
  (0.x → 1.0 is a reinstall); `make release-check` green; the commit ready to
  tag `v1.0.0-rc.1`. Tagging is the owner's (section 12).

### 11. Error codes, actors and dependencies

- **Error codes** (wave 0 adds all): `firewall.lockout_refused`,
  `firewall.manual_required`, `firewall.unsupported_integration`,
  `firewall.prediction_mismatch`, `docker.engine_unavailable`,
  `docker.in_use`, `docker.owned`, `docker.compose_unavailable`,
  `tailscale.unavailable`, `tailscale.link_refused`,
  `tailscale.repo_conflict`, `tailnet.policy_conflict`,
  `tailnet.policy_invalid`, `cloud.not_connected`, `cloud.auth_failed`,
  `cloud.permission_missing`, `cloud.rate_limited`, `cloud.unavailable`,
  `cloud.uncertain_outcome`, `npm.unhealthy`, `npm.not_public`, `derp.region_taken`,
  `derp.verify_failed`, `panel.totp_incomplete`, `panel.already_exposed`,
  `panel.wrong_host`, `cloud.conflict`, `core.child_failed`, `core.needs_repair`,
  `core.stream_limit`, `core.restore_mismatch`, `module.unavailable` (if not
  present).
- **Actors:** `system:firewall.reconcile`, `system:enroll-join`.
- **Dependencies** (wave 0 adds all): `github.com/tailscale/hujson`;
  `golang.org/x/crypto` (argon2, chacha20poly1305, already present or added);
  `vitest-axe`; no Docker or Tailscale SDK.

### 12. Owner checklist (the end of this programme)

1. M2 row 35: minisign keys and secrets in the release environment, the
   environment limited to `v*` tags, a `v*` tag ruleset, the SSH tag-signing
   key in `release/allowed_signers`, the public key in
   `internal/update/trustedkeys`, `make install-sh` committed.
2. Credentials for the live step: a test tailnet OAuth client with the scopes
   of 4.2a and allowed tags (also the CI secrets of ADR 0008), a Cloudflare
   API token with a test zone, and a domain plus a public host for Let's
   Encrypt and DERP. Claude then runs the live demonstrations: tailnet
   management and join, a DERP region carrying traffic between two nodes, a
   container exposed through NPM with a real certificate and through a tunnel,
   the panel exposed and removed.
3. Tag `v1.0.0-rc.1` (closes M2 rows 11 and 16 once the release run is
   green).
4. Reinstall the production VPS from `v1.0.0-rc.1` (0.x does not upgrade to
   1.0), add one agent, and run them for 14 days with no manual repair (S7).
5. Tag `v1.0.0`; milestones close through `dr-superpowers:closing-a-milestone`.

## Contracts

- Every new action follows ADR 0002's compatibility rules and gets a schema
  snapshot under `schemas/`; the `guard` field is part of each guarded
  action's schema.
- Agent protocol: `hello` and the frozen surfaces are unchanged; streams are
  a new endpoint discovered by `GET /agent/v1/streams`.
- Database: new module tables are prefixed with the module id; core gains
  `parent_change_id` on changes, `core_composite_steps` and `core_listeners`;
  all migrations are expand-only.
- Files on disk: every new file, directory, unit and block is in its module's
  owned paths; UFW state files are `restore` paths.
- ADR 0014 records: 1.1–1.13, the composite refinement of ADR 0004, the
  `restore` path kind, module-owned Docker objects, DERP as a signed artifact
  under systemd and `cloudflared` as a container (closing the deferred
  decisions in `docs/adr/README.md`), and the new packages
  (`platform/dockerapi`, `tslocal`, `netstat`, `netfilter`,
  `controller/cloud`, `controller/backup`).

## Parallel decomposition

"Owns" is within a wave; a later wave may edit an earlier wave's files.

| Wave | Part | Owns | Consumes | Runs |
|---|---|---|---|---|
| 0 | Core extensions | `internal/core/**`, `internal/platform/**`, `internal/agent/**`, `internal/controller/**` except module code, `internal/transport/**`, `internal/cli/**` except module command files, `internal/install/**`, `internal/modules/modules.go`, module skeleton packages, `web/src/modules/registry.ts`, `web/src/modules/*/index.ts` stubs, `web/src/components/change-dialog*`, `web/src/shell.tsx`, `web/src/api/**`, `go.mod`, `go.sum`, `web/package.json`, `web/pnpm-lock.yaml`, `test/harness/pins.go`, `provision.go`, `images.json` and the harness helpers, `docs/adr/0014-*`, `docs/adr/README.md` | — | alone |
| 1 | Firewall: model, UFW, guard, safe apply, snapshots, break-glass | `internal/modules/firewall/**` except `docker*.go`, `exposure*.go`, `reconcile*.go`; `internal/cli/firewall*.go`; `web/src/modules/firewall/**` except docker and exposure pages; `test/harness/m3_firewall_test.go` | 0 | alone |
| 2 | Firewall: Docker integration, reconcile, exposure view | `internal/modules/firewall/docker*.go`, `exposure*.go`, `reconcile*.go`, their web pages, `test/harness/m3_docker_test.go` | 0, 1 | alone |
| 3a | Docker module | `internal/modules/docker/**`, `web/src/modules/docker/**`, `test/harness/m4_test.go` | 0, 2 | parallel |
| 3b | Tailnet (spike: scopes, policy API) | `internal/modules/tailnet/**`, `web/src/modules/tailnet/**` | 0 | parallel |
| 3c | Cloudflare account, tunnels, hostnames (spike: permissions, configuration API) | `internal/modules/cloudflare/**` except `connector*.go`, `expose*.go`; `web/src/modules/cloudflare/**` | 0 | parallel |
| 4 | Tailscale node, guard, package, web certificate (spike: local API) | `internal/modules/tailscale/**`, `web/src/modules/tailscale/**`, `internal/controller/webcert/tailscale*.go`, `test/harness/m5_node_test.go` | 0, 1, 3b | alone |
| 5a | DERP (spike: derper flags and certificate modes) | `internal/modules/derp/**`, `web/src/modules/derp/**`, `release/derper.version`, `.github/workflows/release.yml`, `test/harness/m5_derp_test.go` | 0, 1, 3b, 4 | parallel |
| 5b | Nginx Proxy Manager (spike: image and bootstrap) | `internal/modules/npm/**`, `web/src/modules/npm/**`, `test/harness/m6_npm_test.go` | 0, 2, 3a | parallel |
| 5c | Cloudflare connector and expose container (spike: token file) | `internal/modules/cloudflare/connector*.go`, `expose*.go`, `test/harness/m6_cloudflare_test.go` | 0, 3a, 3c | parallel |
| 6 | Controller backup and restore | `internal/controller/backup/**`, `internal/cli/controller_backup*.go`, `test/harness/restore_test.go` | 0 | alone |
| 7 | Panel exposure | `internal/modules/panel/**`, `web/src/modules/panel/**`, `test/harness/m6_panel_test.go` | 0, 2, 5b, 5c | alone |
| 8a | Fuzz inventory and targets | `docs/security/parsers.md`, `*_fuzz_test.go`, `testdata/fuzz/**`, `.github/workflows/fuzz.yml` | 0–7 | parallel |
| 8b | Accessibility | `web/src/**` (fixes and `.a11y.test.tsx`), `docs/accessibility.md` | 0–7 | parallel |
| 8c | Docs and API reference | `docs/user/**`, `docs/dev/**`, `docs/api/**`, `Makefile`, `.github/workflows/ci.yml` | 0–7 | parallel |
| 8d | Upgrade matrix and footprint | `test/harness/upgrade_matrix_test.go`, `footprint_test.go`, the `harness` build-tag files in `internal/agent/`, `.github/workflows/harness.yml` | 0–7 | parallel |
| 9 | Security review, fixes, runbook, RC readiness | `docs/security/threat-model.md`, `docs/runbook.md`, `CHANGELOG.md`, `docs/versioning.md`; fixes anywhere | 0–8 | alone |

- Shared files (module list, web registry, allowlist, error codes,
  dependencies, lockfiles, harness pins and helpers) belong to wave 0. Before
  wave 8, a change to `harness.yml` (a job split or a CI secret) belongs to
  the wave's alone part or, in a parallel wave, to the integrator on `main`.
- Schema snapshots and the OpenAPI document are regenerated by the integrator
  after each wave merges.
- Every part with a spike runs it as its first task; a spike result that
  changes this spec is applied to the spec on `main` before the part's plan
  continues, and one that breaks a requirement goes to the owner.
- Test isolation: unit tests use `t.TempDir()`, in-process fakes on random
  ports or temporary Unix sockets and no global state; harness tests derive
  VM names, work directories and network ranges from the test name (wave 0
  helper) and respect the VM concurrency limit; a job whose total passes its
  120-minute limit is split per milestone.

## Plans

One plan per wave, named `docs/superpowers/plans/<date>-v1-w<n>-<topic>.md`,
written after the previous wave merges. Every plan ends with the gates in
`docs/superpowers/gates.md`, including the harness gate when it touches the
harness, a module's host behaviour or the paths gate 7 lists.

## Verification

| Exit criterion | Evidence |
|---|---|
| M3: S1–S3 on all four OS | harness `TestM3*`: refusal for each protected flow (SSH, direct panel, agent link, outbound controller link), revert on client disconnect and on reboot, prediction mismatch reverts, exposure equals the scan by 2.7's rule for IPv4 and IPv6, published ports unreachable unless a route rule allows them, new Docker networks protected at creation, container recreation under continuous scanning |
| M4: every Docker requirement on a managed agent, including allow and deny from a container view | harness `TestM4` on the enrolled agent VM with the Compose plugin; web tests of the container action slot |
| M5: node and tailnet management; a Darkpanel DERP region carries traffic between two nodes | unit tests against fakes; harness node tests (install, prefs revert, guard); the live step for tailnet, join and DERP |
| M6: container exposed through NPM and through a tunnel; panel exposed and reverted | harness: real NPM issuing a certificate from Pebble (NPM's `LE_SERVER` pointed at Pebble's directory and `REQUESTS_CA_BUNDLE` at Pebble's root, set only in harness deployments), real cloudflared started with its token file (it cannot reach a fake edge, so the test checks the container, the token file and that the token is absent from inspect; connector status comes from the fake API), panel exposure through NPM end to end; the live step for real certificates, tunnels and DNS |
| M7: no open critical or high findings; matrix, footprint, accessibility, docs | security register; `TestUpgradeMatrix`, `TestFootprint`; `docs/accessibility.md` with automated and manual evidence; docs CI check |
| M8: v1.0 tagged and signed, production plus one agent, runbook | owner checklist items 1, 3–5; `docs/runbook.md`; harness restore test |

## Rejected alternatives

- One plan for all of v1: about 300 tasks written against code that does not
  exist yet; rejected by the owner for rolling wave plans.
- Milestone-by-milestone order; user-flow slices first (owner chose core
  first with parallel waves).
- Planning every composite child up front (revision 1): siblings on one lock
  fail, and later children's inputs do not exist yet.
- A model of UFW's user rules alone for the guard and exposure view
  (revision 1): ignores the framework files and other programs' chains.
- Denying only the Docker subnets known when the block is written (revision
  1): new networks would be exposed until reconcile ran.
- Running a change against a scratch copy of UFW's files: UFW has no
  supported alternate root.
- Writing UFW's `user.rules` directly: UFW owns that file; Darkpanel writes
  through `ufw` and only restores snapshotted bytes.
- Vendor SDKs (Docker, Tailscale): large dependency trees against the S6
  footprint.
- derper built into Darkpanel or built on the host: see Owner decisions.
- Secrets in container environment variables: readable through inspect.
- Upgrading 0.x hosts to 1.0: the updater refuses cross-major updates by
  design; the production host is reinstalled.

## Out of scope

M9 candidates; Docker's nftables backend (detected, reported unsupported);
Docker before 28.0 for the firewall integration; Headscale; more than one
tailnet or Cloudflare account per controller.

## Review log

- **Round 1 (2026-10-21)**, Fable and Codex gpt-6-astra, one shared brief:
  96 findings, 67 after deduplication (7 critical, 41 high, 13 medium, 6
  low), all accepted. Revision 2 changed: action names to the registry
  grammar (1.1); undo handlers (1.2); composites as durable step logs with
  just-in-time child plans, one step per host and lock, revertible steps
  last, user confirmation and automatic unwind (1.3); background tasks
  through the lifecycle (1.4); guard context as a schema field with live
  re-checks (1.5); streams through a new endpoint with their own limits
  (1.6); per-action availability (1.7); restore proofs, owned Docker
  objects, exec environment (1.8); uncertain cloud outcomes (1.9); per
  listener proxy trust and public HTTPS (1.10); uninstall options (1.11);
  extension points (1.12); CLI semantics kept (1.13); the firewall's
  configured and loaded state, predicted chains and a netfilter evaluator
  with real protected paths (2.1–2.3); logical snapshots with a pinned
  baseline (2.5); Docker 28 minimum, variant detection, exact block text,
  destination-range denies, route-rule semantics, reconcile ordering and
  health (2.6); exposure verdict semantics (2.7); uninstall choices (2.9);
  Docker undo meanings, inspect redaction, Compose trust and persistence (3);
  Tailscale guard, masked prefs, latency, repository conflicts, web
  certificate (4.1); tags, ETag-safe undo, HuJSON patches, same-step join
  (4.2); DERP manifest, durable binary swap, spike before plan (4.3); NPM
  bootstrap, route-rule ports, durable upgrade (6); Cloudflare probes,
  durable hostnames, token file (7); panel exposure path, local-first removal
  (8); fuzz inventory, upgrade graph, footprint method, accessibility matrix
  (9); backup format and staged restore (10); waves reshuffled so risky parts
  run alone and no part consumes a sibling (Parallel decomposition).

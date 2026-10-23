# ADR 0014: v1 core extensions

**Requirements:** R5, R10, R11, R12, R54, R58, R61, R69, R70, R72

## Status

Proposed

## Context

The v1 completion design (`docs/dforge/specs/2026-10-21-v1-completion-design.md`)
builds the firewall, Docker, Tailscale, DERP, Nginx Proxy Manager, Cloudflare
Tunnel and panel-exposure modules on one set of core extensions, built first
as wave 0 so that the module waves change no shared file. Several extensions
refine ADR 0004 (composites, undo, repair, recovery handlers), ADR 0009
(full-vector exec rules, new owned-path kinds, the Compose exception) and
ADR 0008 (no test tailnet in CI), and the design closes two of the index's
deferred decisions: how DERP servers run and how `cloudflared` runs.
Accepted ADRs are not edited, so the additions are recorded here. The
design's section numbers are cited as "spec 1.n".

## Decision

- **Action names (spec 1.1).** Names stay `<module>.<noun>.<verb>`; only the
  verb may contain `-`. `ActionRef` marshals as text in its
  `<name>/v<version>` form.
- **Effects journal and undo (spec 1.2).** Every agent- and controller-side
  change records its effects in `core_effects` (`Env.Effects`). An effect not
  proven settled is `unresolved`, its change goes to `needs_repair` and keeps
  its locks, and only the operator resolves it (`accept` or `reconcile`,
  after an `Observe` preview). Calls that hand work to another process
  (Docker Engine, the Tailscale local API, systemd jobs, every cloud
  provider) are not `Local` and wait `CommitWait` (cloud calls 10 minutes,
  local daemons 2 minutes) before resolution. Every compensable action
  declares exactly one of an undo handler (`Undoer`), a compensating action
  or `ForwardOnly`. Undo is allowed only while no later change took any of
  the change's lock keys (the lock epochs) and never over an unresolved
  effect.
- **Composites (spec 1.3), refining ADR 0004.** A composite is a
  controller-side action of class `composite` whose apply runs steps: child
  changes of other modules' actions, with `unwind: reverse | forward`.
  Composites and long-running actions apply asynchronously (apply answers
  `202` with the change `applying`); the composite runner follows each child
  through the agent's read-only plan lookup and is recovered at startup.
- **Repair and release (spec 1.3).** A change in `needs_repair` is repaired
  under the repaired change's own locks by a repair change of depth 1 (a
  repair is never repaired), or given up by an audited release (`admin`,
  with a reason, journaled as phase `release`), which abandons its
  non-terminal effects.
- **Module recovery handlers (spec 1.3b).** An action may declare a module
  `Recoverer` that the engine runs, owned `module`, when apply or verify
  fails without an unresolved effect, and that repair runs again. This is the
  one exception to ADR 0004's rule that compensable and irreversible actions
  are never retried on their own. Verify stays read-only.
- **Task units (spec 1.3c).** Host sequences that can restart or disconnect
  the agent (Tailscale and DERP package and binary work) run in transient
  units `darkpanel-task-<change id>-<op>-<attempt>.service` running
  `darkpanel agent task`, fenced by `core_task_runs` and the uninstall
  barrier (`core_meta` key `uninstalling`, `core.uninstalling`).
- **Background tasks and enablement (spec 1.4).** Modules declare background
  tasks that run on the agent only while the module is enabled and its
  preflight passes; the controller pushes enablement to the agent
  (`PUT /agent/v1/modules/{id}/enabled`), and a task's actor is
  `system:<task id>`.
- **Controller-generated call context (spec 1.5).** Why a change runs
  (`Env.Call`: parent composite, step, retired exposures, the guard
  topology) comes from the controller, never from input; the API and the
  agent socket refuse a supplied `guard` or `call`. Guards declared
  `Overridable` are overridden only by listing their ids in `overrides`
  (`--override`); every other guard is a hard refusal. A request through a
  runtime listener carries its proxy path in `GuardContext.Via`.
- **Streams and events (spec 1.6).** Modules declare streams (`StreamDef`);
  the controller relays them and its own event bus as server-sent events with
  epoch-qualified ids and fixed limits.
- **Availability, scope and internal actions (spec 1.7).** `ActionSpec`
  gains `Available`, `MinScope` (raise only) and `Internal` (dispatched only
  by the controller).
- **Host clients, owned paths and exec rules (spec 1.8), refining ADR 0009.**
  The runner's allowlist is a list of `ExecRule`s that check the whole
  argument vector and fix the environment (`ProductionRules()`); nothing
  else runs. Owned paths gain the kinds `restore`, `managed-dir`, `sysctl`
  and `service-dir`, and `unit` becomes writable. Docker objects a module
  creates carry the label `darkpanel.module=<id>`, and mutations of objects
  owned by another module are refused (`docker.owned`). New host clients
  live in `internal/platform/dockerapi`, `tslocal`, `netstat` and
  `netfilter`; Docker and Tailscale are reached through their APIs, with no
  SDK.
- **Compose exceptions (spec 3), to ADR 0009 and R54.** `docker compose` is
  an allowlisted binary that reads operator-authored files and starts
  containers through the daemon; it runs only with fixed verb flags, a fixed
  environment that disables Compose's Git and OCI remote loaders, and
  `--no-build`. Validation reads `docker compose config --format json`: the
  one place Darkpanel parses `docker` CLI output, Compose's own JSON
  document, parsed strictly and fuzzed.
- **Cloud plumbing (spec 1.9).** `internal/controller/cloud` is the one
  provider HTTP client: 15 s per attempt, at most 3 retries for `GET` and
  `DELETE` only, honouring `Retry-After`, never re-sending a write. A write
  that fails with a 5xx or a transport error is `cloud.uncertain_outcome`:
  its effect becomes `unresolved` and the operator resolves it after
  `CommitBy`. Every 4xx answer to a write is definite. Account credentials
  live in each module's own table, sealed with associated data
  `<table>.<column>.<id>`, write-only through the API. Tests use
  `internal/controller/cloud/cloudfake` and per-module fakes; integrations
  with real providers run only in the owner's live step.
- **Runtime listeners and trusted proxies (spec 1.10).** The controller opens
  and closes HTTPS listeners (served only through `ServeTLS`) recorded in `core_listeners` without a restart
  (`core.Listeners`, on `Env` for controller-side actions). Each accepts TCP
  connections only from its allowed peer, reads exactly one client header
  that a proxy Darkpanel configured sets (`X-Darkpanel-Client` for Nginx
  Proxy Manager, `Cf-Connecting-IP` for a tunnel; the public host is stored
  lowercased without one trailing dot), never `X-Forwarded-For`
  or `X-Real-IP`, refuses a forwarded protocol other than `https`, refuses
  an unsafe request whose `Origin` is not the exposure's public host, and
  records both addresses as the audit source `"<client> via <proxy>"`. Revocation is
  durable and monotonic: `revoked` is committed before the listener stops,
  and a revoked listener is never reopened. Requests on every other listener
  never read forwarded headers.
- **Secret delivery (spec 1.14).** A reveal action (`ActionSpec.Reveal`,
  class `read`, scope `admin`) returns one stored secret once in the
  response body, audited before it returns, never over `--local`.
- **Extension package and frozen call sites (spec 1.12).** `internal/core/ext`
  (importing only `internal/core`, `internal/core/store` and the standard
  library) defines `Route`, `Service`, `Deps`, `Dispatcher`, `Publisher` and
  the optional `Controller` interface a module implements. At startup the
  controller assembly calls `Controller(deps)` on every module and
  `backup.Contribute(deps)`, mounts each route on the web surface or the
  controller socket, and starts each service after composite recovery with
  restart backoff from 1 s to 5 min and actor `system:<service name>`. A
  route path must lie under `/api/v1/` and may not end in `/`, be a
  catch-all wildcard or be `{$}`; a clashing pattern, or a duplicate route
  in the combined OpenAPI document, fails startup. Stream routes
  authenticate passively only when the network mux routes the request to a
  stream pattern. The service runner logs a warning at shutdown when a
  service's `Run` returned an error other than cancellation or panicked
  (a panic by its type only) and asks the quarantine gate again before every
  restart. The other call sites are the web
  certificate sources (`internal/controller/listen/sources.go`), the CLI's
  module command map (`internal/cli/modules.go`), path selection
  (`cmd/darkpanel/paths.go`) and the web registry's extension points
  (`web/src/modules/types.ts`). After wave 0 these files, the core contract,
  the lifecycle engine, the migrations, `go.mod` and the web dependency
  manifest are frozen: a later wave that needs a change there stops, and the
  change is made on `main` first.
- **Restore quarantine gate (spec 1.12, 10).** The `core_meta` key
  `restore_quarantine` in `controller.db` suspends every automatic mutation
  source (composite recovery, the child watcher, controller module recovery
  handlers, the update scheduler and rollout driver, controller services)
  and refuses every action apply, undo, repair, release and resolution with
  `core.quarantined`. The flag is read through `quarantine.Active`; a read
  error suspends the automatic sources and refuses the action with the read's
  own error, like quarantine. The agent engine is not gated. Restore
  sets the flag; only `quarantine release` clears it.
- **DERP as a signed artifact under systemd (spec 4.3).** The release
  workflow builds `derper` from upstream `tailscale.com/cmd/derper` at the
  version in `release/derper.version` and signs it with the release; the
  agent installs it as `/usr/local/lib/darkpanel/derper` running as system
  user `darkpanel-derp` in the unit `darkpanel-derper.service`. This closes
  the index's deferred decision on how DERP servers run.
- **`cloudflared` as a container (spec 7.2).** The connector runs as the
  owned container `darkpanel-cloudflared-<tunnel>` from a pinned image digest,
  labelled `darkpanel.module=cloudflare`, reading its token from a file in a
  managed directory. This closes the index's deferred decision on how
  `cloudflared` runs.
- **Test tailnet, diverging from ADR 0008.** ADR 0008 planned a test tailnet
  in CI from M5. Tests that need a tailnet run in the owner's live step
  instead; the harness runs the Tailscale node tests that need none.
- **New packages.** `internal/core/ext`, `internal/platform/dockerapi`,
  `tslocal`, `netstat`, `netfilter`, `internal/controller/cloud`,
  `internal/controller/events`, `internal/controller/backup` and
  `internal/controller/quarantine`, and one package per module under
  `internal/modules/<id>`: `firewall`, `docker`, `tailscale`, `tailnet`,
  `derp`, `npm`, `cloudflare`, `panel`.

## Alternatives considered

- A package-level registry that modules fill from `init`: rejected. The
  extension package has no state; the assembly asks each compiled-in module,
  so the import graph `server → modules → ext` has no cycle and tests build
  their own module lists.
- Retrying uncertain cloud writes automatically: rejected. Providers give no
  guarantee that a timed-out request did not commit, so a resend can create
  a second object; the operator resolves against what the provider shows.
- Trusting `X-Forwarded-For` from any proxy: rejected. Only a header that a
  proxy Darkpanel configured overwrites can carry the client address, and
  only on a listener that accepts that proxy alone.
- DERP as a container: rejected. Client verification needs `derper` beside
  the host's `tailscaled`, and a systemd unit under its own user keeps the
  binary signed with the release.
- `cloudflared` as a systemd service: rejected. The pinned image needs no
  package repository and runs unprivileged as UID 65532.
- A test tailnet in CI: rejected for v1. It needs owner credentials in CI;
  the live step covers the same flows once.

## Consequences

- Modules add actions, controller routes, services, CLI commands and web
  pages only through the extension points; the frozen files change only on
  `main`, before the wave that needs the change continues.
- Every cloud write can leave a change in `needs_repair` that only the
  operator resolves, and the UI and CLI offer the preview and resolution.
- Panel exposure depends on the runtime-listener contract; any proxy other
  than the ones Darkpanel configures cannot carry a trusted client address.
- A DERP host's `tailscaled` is held at the derper version, so a Tailscale
  security release needs a Darkpanel release that bumps
  `release/derper.version`.
- Restore leaves the controller in quarantine until the operator resolves
  or keeps every record and releases it.

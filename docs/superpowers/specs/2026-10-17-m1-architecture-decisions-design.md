# M1 epic: architecture decision records

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17; revised after the Fable and Codex review (docs/superpowers/findings/2026-10-17-m1-adr-spec-review.md)
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirement:** R1
**Execution strategy:** parallel — the ADRs are separate files; shared contracts are fixed first in wave 0 and reconciled in wave 2 (owner ruling 2026-10-17: favour parallel to reach v1 sooner).

## Goal

Record the decisions every later epic builds on, as numbered architecture
decision records (ADRs) in `docs/adr/`, before any implementation. Each ADR has
the sections Status, Context, Decision, Alternatives considered, Consequences,
and a Requirements line naming the R-numbers it serves. ADRs are written with
status `Proposed`, may be corrected while the epic is open, and become
`Accepted` when the epic closes; after that, an ADR changes only through a new
ADR that supersedes it.

## Deliverables

| File | Decision |
|---|---|
| `docs/adr/README.md` | Index, ADR format, lifecycle, shared contracts, traceability |
| `docs/adr/0001-stack-and-layout.md` | Stack, layout, process model, privilege |
| `docs/adr/0002-module-contract.md` | Module contract, action classes, compatibility |
| `docs/adr/0003-agent-protocol.md` | Controller–agent protocol, identity, enrollment, revocation |
| `docs/adr/0004-change-lifecycle.md` | Change lifecycle, locking, recovery |
| `docs/adr/0005-storage-and-secrets.md` | Storage, migrations, secrets, keys, backup |
| `docs/adr/0006-api-and-cli.md` | API, listeners, authentication boundaries, CLI, local access |
| `docs/adr/0007-release-and-update.md` | Build provenance, signing, keys, update and rollback |
| `docs/adr/0008-test-harness.md` | Integration test harness |
| `docs/adr/0009-host-execution-safety.md` | Command execution and file ownership |

## Shared contracts (fixed in `docs/adr/README.md` in wave 0)

Every ADR uses these; no ADR redefines them.

- **Names.** Action names are `<module>.<noun>.<verb>` in lower case
  (`firewall.rule.add`); versions are positive integers written `v1`. Host ids
  and change ids are ULIDs. JSON fields are camelCase.
- **Schemas.** JSON Schema draft 2020-12, generated from Go types with
  `github.com/invopop/jsonschema`. A `secret:"true"` struct tag marks fields
  that are encrypted at rest, write-only through the API, and redacted from
  logs, audit, snapshots, diffs, events and errors.
- **Action classes.** `read`; `revertible` (snapshot plus confirm-or-revert
  timer); `compensable` (can be undone by a defined compensating action, no
  timer); `irreversible` (cannot be undone, for example pruning volumes;
  requires typed confirmation). Every mutating action declares one class.
- **Errors.** RFC 9457 problem details with a stable `code` from a registry in
  `internal/core/errcode`.
- **Actors.** Audit actors are `user:<id>`, `token:<id>`, `agent:<host id>`,
  `local:root@<host id>` and `system:<job>`.
- **Version skew.** The controller is never older than its agents. It supports
  agents of the same and previous minor version, updates first, and drives
  agent updates up to its own version.

## Decisions

### 0001 Stack, layout and process model

- Go 1.27 with `go 1.27` and `toolchain go1.27.2` in `go.mod`; CI sets
  `GOTOOLCHAIN=local` on the exact version. CGO disabled; `-trimpath`; one
  binary for `linux/amd64` and `linux/arm64`.
- HTTP with the standard library router; CLI with `github.com/spf13/cobra`.
- Web UI: Vite, React 19, darkraise-ui 6.x, TanStack Query, built to `web/dist`
  and embedded with `go:embed`; pnpm.
- Process model: two systemd services from one binary. `darkpanel-controller`
  runs as an unprivileged `darkpanel` user (it never touches the host
  directly). `darkpanel-agent` runs as root on every managed host, including
  the controller's host, where it connects over loopback like any other agent.
  In M1, before the transport exists, the controller calls the agent handler
  in-process.
- Layout:

```
cmd/darkpanel/            entry point, role dispatch (serve, agent, CLI, update)
internal/core/            registry, actions, lifecycle, audit, errcode, schema checks
internal/controller/      HTTP API, auth, database, fleet, scheduler, CA
internal/agent/           agent runtime, local socket, journal, revert timers
internal/transport/       TLS, enrollment, yamux
internal/platform/        providers: exec, files, systemd, OS detection (ADR 0009)
internal/update/          updater, transaction, recovery
internal/modules/<id>/    manifest, controller part, agent part, migrations
schemas/<action>/v<n>.json committed action schema snapshots
web/src/modules/<id>/     module UI pages
docs/adr/                 decision records
```

### 0002 Module contract and compatibility

- A module implements one Go interface returning a manifest: id, version,
  actions, resources, settings schema, preflight checks, owned paths and
  marked blocks (ADR 0009), uninstall step, and migrations per role
  (controller, agent). It may supply a controller part, an agent part, or both;
  the UI part is a route bundle registered in `web/src/modules/registry.ts`.
- Modules are compiled in and listed once in `internal/modules`; no runtime
  plugins in v1.
- An action declares name, version, typed input and output, class (shared
  contracts), where it runs (controller or agent), whether it is break-glass,
  and the lock keys it takes (ADR 0004).
- A module never changes another module's resources directly; it calls the
  owning module's actions (for example DERP opens ports through
  `firewall.rule.add`), so locking, snapshots and audit stay with the owner.
- Enablement and preflight results are stored per host in
  `core_module_state`. A disabled module, or one whose migration or preflight
  fails, is marked unavailable with the reason; the rest of the system starts.
- Compatibility rules, enforced by a test against the schema snapshots of every
  released tag (snapshots are immutable once released):
  - Allowed in a published version: new optional input fields, new output
    fields, new output enum values (clients must accept unknown values).
  - Forbidden: removing or renaming a field, changing a type, a default or a
    constraint, adding a required input field, removing an enum value, or
    changing an action's meaning or class.
  - A breaking change is a new version; the old version stays until the next
    major release.
- Agents advertise, per action version, the hash of the schema they implement.
  The controller refuses to send an input field the agent's schema does not
  contain, so an older agent can never silently ignore a newer safety field.

### 0003 Controller–agent protocol, identity and enrollment

- **Identity.** The controller runs an internal Ed25519 certificate authority
  (10-year lifetime, key custody in ADR 0005). Controller certificates carry
  `serverAuth` and the URI SAN `darkpanel://controller/<controller id>`; agent
  certificates carry only `clientAuth` and `darkpanel://host/<host id>`. Agents
  verify the chain and require the controller SAN they pinned at enrollment;
  the controller requires a host SAN, a known host and an unrevoked serial.
- **Transport.** The agent dials the controller's agent listener (default port
  7443, bound to the Tailscale interface by default; any other bind is
  explicit). TLS 1.3, ALPN `darkpanel-agent/1` requires a client certificate;
  ALPN `darkpanel-enroll/1` accepts none and serves only enrollment. The
  connection carries a yamux session with stream limits and deadlines; the
  controller sends HTTP/1.1 requests to the agent handler over streams it
  opens, and the agent pushes events over streams it opens.
- **Agent endpoints.** `GET /agent/v1/hello`; `POST /agent/v1/actions/{name}/{version}/{plan|apply|confirm|revert}`;
  `GET /agent/v1/resources/{module}/{resource}`; `POST /agent/v1/journal/ack`.
- **Hello** is exchanged both ways: protocol version, binary version, OS, and
  for the agent its modules, action versions and schema hashes. A protocol or
  version outside the supported window is shown on the host (R27) and only
  updates are dispatched.
- **Liveness.** Heartbeat every 15 s; a host is offline after 45 s without one,
  and actions to it fail immediately.
- **Enrollment.** The admin creates a host record; the controller issues a
  single-use token bound to that host id: 128-bit random, stored hashed,
  valid one hour, compared in constant time, consumed atomically, rate
  limited. The token carries the authority fingerprint and controller SAN. The
  agent verifies the controller against them before sending the token, then
  sends a certificate signing request; the controller ignores any identity in
  the request and issues a certificate for the bound host id.
- **Certificates and revocation.** Agent certificates last one year and are
  renewed after two thirds of their life; renewal revokes the previous serial.
  Revocation is by serial, checked at every handshake, and closes the host's
  live session. An agent with an expired certificate re-enrolls with a new
  token.
- **Journal delivery.** The agent journal is forwarded in sequence order; the
  controller acknowledges by sequence and deduplicates; acknowledged entries
  are pruned, unacknowledged ones are kept up to a size cap.

### 0004 Change lifecycle, locking and recovery

- **Plan** records host, action and version, principal, canonical input hash,
  guard results (for example the lockout check), a diff, and a hash of the
  state the action's lock keys cover. It expires after 10 minutes. Apply takes
  the plan id only, never new input; the caller is re-authorized on apply and
  confirm.
- **Locks** are taken by key (for example `firewall`), declared per action, held
  from apply until the change is confirmed, reverted or completed.
- **Apply ordering**, for revertible actions:
  1. Re-check guards and the state hash; refuse on mismatch.
  2. Persist snapshot, intent and revert deadline to the agent database
     (fsync) before touching the host.
  3. Mutate; run verify. If verify fails, revert immediately.
  4. Record the result against the plan id. A repeated apply with the same
     plan id returns the stored result instead of acting twice.
- **Confirm and revert** are arbitrated in one transaction; whichever commits
  first wins. Deadlines use the monotonic clock while running and wall-clock
  deadlines on restart.
- **Startup recovery.** On start the agent first processes incomplete changes:
  an intent without a result is reverted from its snapshot; an overdue
  unconfirmed change is reverted; then it accepts requests.
- **Compensable and irreversible actions** record intent and result but arm no
  timer; compensable actions name their compensating action.
- **Controller-side actions** (cloud APIs such as the tailnet policy) follow the
  same plan, apply and journal steps in the controller database, with
  snapshots where the remote API allows, and no revert timer.
- Snapshot and journal records carry a format version; new binaries read all
  formats written by the previous minor release.

### 0005 Storage, migrations, secrets and keys

- SQLite through `modernc.org/sqlite` (exact versions pinned, including
  `modernc.org/libc`), write-ahead-log mode, pragmas set in the connection
  string, one writer connection: `/var/lib/darkpanel/controller.db` (owned by
  `darkpanel`) and `/var/lib/darkpanel/agent.db` (root).
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations: embedded SQL per module and role, numbered, forward-only, each in
  a transaction, recorded in `core_migrations` with a checksum. A changed
  checksum or failed core migration stops startup; a failed module migration
  marks only that module unavailable (ADR 0002).
- **Expand, then contract.** A release's migrations must leave the schema usable
  by the previous minor release's binary. Destructive changes (drop, rename)
  ship only in a later release, after nothing reads the old shape. A binary
  rollback therefore runs the old binary on the new schema without restoring
  data.
- Before migrating, the database is copied with `VACUUM INTO` to
  `/var/lib/darkpanel/backups/<update id>/` (mode 0600, last five kept), for
  disaster recovery only. The agent's journal and revert records are never
  restored from a backup.
- **Secrets.** XChaCha20-Poly1305 with a random 24-byte nonce and associated
  data `<table>.<column>.<row id>`; ciphertexts carry a key id so rotation can
  be added. Each role has its own key, created atomically at install
  (`/etc/darkpanel/controller.key` owned by `darkpanel`,
  `/etc/darkpanel/agent.key` owned by root, both 0600). Agents hold only the
  secrets their own modules use (for example Nginx Proxy Manager credentials
  on its host).
- **Certificate authority key** is stored encrypted in `controller.db`.
  `darkpanel controller backup` exports the database and the controller key
  into one encrypted archive; `restore` is the lost-controller path in the
  runbook (R81).

### 0006 API, listeners, authentication and CLI

- **Listeners.** Web and API: 127.0.0.1 and the Tailscale interface by default,
  HTTPS with a `tailscale cert` certificate when the tailnet has HTTPS enabled,
  otherwise a certificate from the internal authority; plain HTTP only on
  127.0.0.1. Agent listener: ADR 0003, no web routes. Local sockets: below.
- **Authentication boundary.** One middleware wraps every route on every
  listener and denies unless the route is in an explicit public list (login,
  health). It applies to every HTTP method. Sessions use Secure, HttpOnly,
  SameSite=Strict cookies with CSRF tokens; API tokens are scoped
  (read, operate, admin) and expire. Behind Nginx Proxy Manager or a tunnel,
  forwarded headers are trusted only from configured proxy addresses.
- **REST** under `/api/v1`: hosts; resources; actions at
  `/api/v1/hosts/{host}/actions/{name}/{version}/plan` and `.../apply`,
  controller-side actions at `/api/v1/actions/{name}/{version}/plan` and
  `.../apply`; `/api/v1/changes/{id}/confirm` and `/revert`; audit; auth.
  (`{version}` is a whole path segment such as `v1`, which the standard router
  requires.)
- Live updates over server-sent events at `/api/v1/events`; OpenAPI 3.1 at
  `/api/v1/openapi.json`, generated from the registry; `/api/v1` is additive
  only (ADR 0002).
- **CLI**, generated from the registry: `darkpanel <module> <noun> <verb>`.
  Scalar inputs are flags; nested inputs come from `--input <file|->` as JSON;
  secret fields are prompted or read from stdin, never accepted as flags. Read
  actions print tables or `--json`. Mutating actions show the plan, then ask;
  `--yes` answers that question only. A revertible change is confirmed only by
  `--confirm` or by `darkpanel change confirm <id>`. Top-level names `serve`,
  `agent`, `update`, `user`, `controller`, `change`, `uninstall`, `version`
  are reserved.
- **Local access.** The agent serves its handler on `/run/darkpanel/agent.sock`
  (root, 0600) for every agent action, checked with `SO_PEERCRED` uid 0,
  with actor `local:root@<host id>`; entries are journaled and reach the audit
  log when the controller is reachable (R28). The controller serves
  `/run/darkpanel/controller.sock` (0600, `darkpanel`, root allowed via
  `SO_PEERCRED`) for controller break-glass commands such as
  `darkpanel user reset-totp`. `darkpanel update rollback` needs neither
  service (ADR 0007).

### 0007 Release, signing, update and rollback

- **Build provenance.** Releases build from a clean checkout of a signed tag
  with pinned toolchain and dependencies, `-trimpath`, and third-party GitHub
  Actions pinned by commit SHA. The build job publishes GitHub artifact
  attestations.
- **Signing.** A separate job in the protected environment `release` (owner
  approval required) holds the minisign key, verifies the attestations against
  the tag's commit, and signs `checksums.txt`. The signature's trusted comment
  carries version, channel and the checksum file's purpose, so a replayed older
  file is detected. Verification uses `aead.dev/minisign`, pinned.
- **Keys.** Binaries embed a trusted key list. Rotation: releases are signed by
  both the old and the new key for at least two minor releases; a host that
  skips past the window updates through the last dual-signed release first.
  Emergency revocation of a key is a release signed by the remaining key.
- **Update transaction.** Run by the currently installed (old) binary as a
  transient systemd unit, independent of the services:
  1. Refuse while any revertible change is unconfirmed on the host; take all
     lock keys for the duration.
  2. Download, verify signature, trusted comment (refuse downgrades unless
     forced) and checksum; run the new binary's self-test.
  3. Write a transaction record, keep the current binary as `darkpanel.prev`,
     swap atomically, restart the services.
  4. Health-check within the limit (S5: two minutes); on failure, swap back and
     restart.
- The service units set `OnFailure=darkpanel-rollback.service`, which runs
  `darkpanel.prev update rollback`, so a new binary that cannot start is rolled
  back even after a reboot.
- On the controller's host the controller updates first, then its agent;
  fleet rollouts go one host at a time and stop at the first failure, with
  progress stored so an interrupted rollout resumes.
- Releases are published on GitHub Releases of `github.com/Darkraise/darkpanel`.
  The install script embeds the trusted keys and verifies before installing;
  its exact method is decided in the M2 install epic.

### 0008 Integration test harness

- QEMU/KVM VMs on GitHub-hosted Linux runners from pinned, checksummed
  official cloud images (Ubuntu 22.04 and 24.04, Debian 12 and 13) with
  cloud-init, one operating system per matrix job. A first step checks for
  `/dev/kvm` and fails loudly if it is missing; GitHub documents nested
  virtualization as not officially supported, so this is a known risk with
  self-hosted KVM runners as the fallback.
- A scanner VM sits on an "internet" network addressed outside private ranges
  (198.18.0.0/15 and an IPv6 documentation prefix), so ufw-docker private-range
  exemptions cannot mask exposure; IPv4 and IPv6 are both tested.
- Fault injection: kill the agent mid-apply, reboot during an unconfirmed
  change, and ship a deliberately broken binary to the updater.
- Tailscale tests use a dedicated test tailnet with ephemeral auth keys stored
  as CI secrets (needed from M5).
- arm64 is covered by builds and unit tests in v1; arm64 VM tests are Unknown
  until GitHub arm64 runners are confirmed to expose KVM.
- Local runs need a KVM-capable machine; the development host is an LXC
  container that could get one if Proxmox passes `/dev/kvm` through.

### 0009 Host execution safety

- **Commands.** The exec provider runs only binaries from an allowlist of
  absolute paths, with a fixed minimal environment, a timeout from the caller's
  context, and argument vectors only; values that could be read as options are
  separated with `--` where the tool supports it. Input schemas constrain
  values with patterns (ports, names, CIDRs, comments) before they reach a
  provider.
- **Files.** The file provider writes only paths and marked blocks declared in
  the module manifest; it refuses symlinks (`O_NOFOLLOW`), writes atomically via
  a temporary file and rename, and records ownership in the ownership manifest
  that `darkpanel uninstall` reverses (R30).
- Prefer APIs over tools: Docker Engine API, Tailscale local API, systemd over
  D-Bus.

## Product spec amendments

This design changes two approved requirements; both edits are made with this
spec:

- **R10** now reads: mutating actions follow one change lifecycle; revertible
  actions store a snapshot sufficient to roll them back; compensable and
  irreversible actions are declared as such and irreversible ones require
  typed confirmation.
- **R31** now names the controller's local socket for controller break-glass
  commands, and `update rollback` as needing neither service.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| Foundation | 0 | `docs/adr/README.md`: index with final numbers and titles, ADR template, lifecycle, the shared contracts above | this spec |
| ADR 0001–0009 | 1 | one ADR file each | README |
| Reconciliation | 2 | edits to any ADR for consistency; traceability table in README | all ADRs |

All nine wave-1 parts may run together. They reference each other only by the
numbers, titles and shared contracts fixed in wave 0. The reconciliation part
reads every ADR, removes contradictions, checks each decision against this
spec, and adds a table mapping R1, R5–R12, R15–R36 to ADR sections. There are
no tests, ports or runtime state to isolate.

## Verification

- Each ADR exists with the required sections and matches its decision above.
- The README lists all nine ADRs, the shared contracts and the traceability
  table, and every decision named in R1 maps to an ADR.
- Wave 2 reports no remaining contradiction between ADRs.

## Rejected alternatives

- Reverse HTTP/2 over the dialed connection instead of yamux: it would drop a
  dependency, but HTTP/2 lets only the client open streams, so agent-pushed
  events would need a long-poll; yamux is maintained (commits through
  2026-10) and already approved.
- 90-day agent certificates: hosts offline longer than 30 days would lock
  themselves out; one-year certificates with serial revocation are simpler.
- Restoring the database on update rollback: it loses pending reverts and new
  enrollments; expand-then-contract migrations make it unnecessary.

## Out of scope

Implementing any decision, and decisions owned by later epics: how DERP and
`cloudflared` run (M5, M6) and the install script's verification method (M2).

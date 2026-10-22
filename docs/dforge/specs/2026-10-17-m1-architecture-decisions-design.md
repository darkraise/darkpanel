# M1 epic: architecture decision records

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17; revised after the Fable and Codex review (docs/dforge/findings/2026-10-17-m1-adr-spec-review.md); revised again after the owner accepted the Fable review of the ADRs on 2026-10-17 (docs/dforge/findings/2026-10-17-m1-adr-review.md)
**Product spec:** docs/dforge/specs/2026-10-17-darkpanel-design.md
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
  that are encrypted at rest wherever they are stored, including inside plan
  records and snapshots, are write-only through the API, and are redacted from
  logs, audit, diffs, events and errors.
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
  in-process, and in development mode both roles use the invoking user's
  paths; the separate users and directories (ADR 0005) apply from the M2
  install.
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
schemas/released/<tag>/   frozen snapshots per release (ADR 0002)
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
  contracts), where it runs (controller or agent), whether it is break-glass
  (a controller-side action that the controller's local socket also serves,
  ADR 0006), and the lock keys it takes (ADR 0004).
- A module never changes another module's resources directly; it calls the
  owning module's actions (for example DERP opens ports through
  `firewall.rule.add`), so locking, snapshots and audit stay with the owner.
- Enablement and preflight results are stored per host in
  `core_module_state`. A disabled module, or one whose migration or preflight
  fails, is marked unavailable with the reason; the rest of the system starts.
- Compatibility rules, enforced by a test against the schema snapshots of every
  released tag, which the release job copies to `schemas/released/<tag>/`
  (immutable once released):
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
  The controller certificate lasts one year and the controller renews it from
  its own authority after two thirds of its life; agents pin the SAN, not the
  certificate, so a renewal needs no change on the agents.
- **Transport.** The agent dials the controller's agent listener (default port
  7443). The listener binds 127.0.0.1 and the Tailscale interface by default,
  with the same Tailscale bind rule as the web listener (ADR 0006); any other
  bind address is explicit configuration, and it never binds all interfaces by
  default. The agent on the controller's host dials 127.0.0.1. TLS 1.3, ALPN
  `darkpanel-agent/1` requires a client certificate; ALPN `darkpanel-enroll/1`
  accepts none and serves only enrollment. The connection carries a yamux
  session with stream limits and deadlines; the controller sends HTTP/1.1
  requests to the agent handler over streams it opens, and the agent pushes
  events over streams it opens.
- **Agent endpoints.** `GET /agent/v1/hello`; `POST
  /agent/v1/actions/{name}/{version}/{plan|apply|confirm|revert}`; `GET
  /agent/v1/resources/{module}/{resource}`; `POST /agent/v1/journal/ack`.
- **Hello** is exchanged both ways: protocol version, binary version, OS, and
  for the agent its modules, action versions and schema hashes. A protocol or
  version outside the supported window is shown on the host (R27) and only
  updates are dispatched. `GET /agent/v1/hello`, the actions `core.update.apply`
  and `core.update.rollback` (ADR 0007) and the yamux framing are frozen for
  protocol version 1; changing any of them makes a new protocol version, and a
  controller keeps supporting the previous protocol version for update dispatch
  only.
- **Liveness.** Heartbeat every 15 s; a host is offline after 45 s without one,
  and actions to it fail immediately.
- **Enrollment.** The admin creates a host record; the controller issues a
  single-use token bound to that host id: 128-bit random, stored hashed,
  valid one hour, compared in constant time, consumed atomically, rate
  limited. The token carries the controller's address and port, the authority
  fingerprint and the controller SAN, so the operator copies one value. The
  agent verifies the controller against them before sending the token, then
  sends a certificate signing request; the controller ignores any identity in
  the request and issues a certificate for the bound host id.
- **Certificates and revocation.** Agent certificates last one year. After two
  thirds of their life the agent requests renewal over its existing session;
  the controller issues a new certificate and keeps both serials valid. The
  agent stores the new key and certificate atomically (temporary file, fsync,
  rename) before using them. The controller revokes the previous serial when
  the agent first completes a handshake with the new certificate, or when the
  previous certificate expires, whichever comes first. Revocation is by serial,
  checked at every handshake, and closes the host's live session. An agent
  whose certificate has expired re-enrolls with a new token.
- **Journal delivery.** The agent journal is forwarded in sequence order; the
  controller acknowledges by sequence and deduplicates; acknowledged entries
  are pruned, unacknowledged ones are kept up to a size cap.

### 0004 Change lifecycle, locking and recovery

- **Plan** records host, action and version, principal, the canonical input
  (secret fields encrypted at rest, see the shared contracts) and its hash,
  guard results (for example the lockout check), a diff, and a hash of the
  state the action's lock keys cover. It is stored in the database of the side
  that executes it (agent or controller) and expires after 10 minutes. Apply
  takes the plan id only and executes the stored input; the hash is what the
  API and audit expose, so a client can check the plan matches what it
  submitted. The controller re-authorizes the caller on apply and confirm; the
  agent trusts the controller and the local root actor.
- **Locks** are taken by key (for example `firewall`), declared per action, held
  from apply until the change is confirmed, reverted or completed. They are
  rows in the executing side's database, keyed by lock key and owned by a
  change id or an update transaction id (ADR 0007), so they survive a restart.
- **Apply ordering**, for revertible actions:
  1. Re-check guards and the state hash; refuse on mismatch.
  2. Persist snapshot, intent and revert deadline to the agent database
     (fsync) before touching the host.
  3. Mutate; run verify. If verify fails, revert immediately.
  4. Record the result against the plan id. A repeated apply with the same
     plan id returns the stored result instead of acting twice.
- **Apply ordering**, for compensable and irreversible actions: persist intent
  (fsync); mutate; run verify; persist the result.
- **Confirm and revert** are arbitrated in one transaction; whichever commits
  first wins. Deadlines use the monotonic clock while running. After a restart
  every unconfirmed revertible change is reverted whatever its deadline, so
  recovery never depends on the wall clock of a freshly booted host.
- **Startup recovery.** On start the agent first processes incomplete changes,
  then it accepts requests:
  - A revertible intent without a result, and every unconfirmed revertible
    change, is reverted from its snapshot.
  - A compensable or irreversible intent without a result is marked
    `interrupted` with the action's verify output and journaled; the agent never
    retries or compensates on its own. For a compensable action the UI and CLI
    offer the named compensating action. A repeated apply with an interrupted
    plan id returns the interrupted result.
  - Locks whose owner has a result are released.
- **Compensable and irreversible actions** arm no timer; compensable actions
  name their compensating action.
- **Controller-side actions** (cloud APIs such as the tailnet policy) follow the
  same plan, apply and journal steps in the controller database, with
  snapshots where the remote API allows, and no revert timer.
- Plan, snapshot, journal and update transaction records carry a format
  version and change only additively within a major version; every binary of a
  major version reads every format written in that major, so an older binary
  that takes over in a rollback reads what a newer one wrote.

### 0005 Storage, migrations, secrets and keys

- SQLite through `modernc.org/sqlite` (exact versions pinned, including
  `modernc.org/libc`), write-ahead-log mode, pragmas set in the connection
  string, one writer connection. Each role has its own directory, because the
  write-ahead log creates files next to the database:
  `/var/lib/darkpanel/controller/controller.db` (directory and file owned by
  `darkpanel`) and `/var/lib/darkpanel/agent/agent.db` (root).
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations: embedded SQL per module and role, numbered, forward-only, each in
  a transaction, recorded in `core_migrations` with a checksum. Each role runs
  its migrations when its service starts, before serving. A binary ignores
  `core_migrations` rows numbered above its own highest migration and verifies
  checksums only for the migrations it embeds. A changed checksum or failed
  core migration stops startup, which the update health check catches
  (ADR 0007); a failed module migration marks only that module unavailable
  (ADR 0002).
- **Expand, then contract.** A release's migrations must leave the schema usable
  by the previous minor release's binary. Destructive changes (drop, rename)
  ship only in a later release, after nothing reads the old shape. New columns
  are nullable or have defaults, so an older binary's inserts still succeed. A
  binary rollback therefore runs the old binary on the new schema without
  restoring data; updates step one minor release at a time (ADR 0007), so the
  old binary is never more than one minor behind the schema.
- Before migrating, each role copies its database with `VACUUM INTO` into its
  own directory, `backups/<from>-<to>-<timestamp>/` (mode 0600, last five
  kept), for disaster recovery only. No backup is restored automatically, and
  the agent's journal and revert records are never restored from one.
- **Secrets.** XChaCha20-Poly1305 with a random 24-byte nonce and associated
  data `<table>.<column>.<row id>`, where the row id is the table's declared
  primary key (a ULID), never the implicit SQLite rowid, which `VACUUM` may
  renumber; ciphertexts carry a key id so rotation can be added. Secret fields
  inside plan records and snapshots are encrypted the same way (ADR 0004). Each
  role has its own key, created atomically at install
  (`/etc/darkpanel/controller.key` owned by `darkpanel`,
  `/etc/darkpanel/agent.key` owned by root, both 0600). Agents hold only the
  secrets their own modules use (for example Nginx Proxy Manager credentials on
  its host).
- **Certificate authority key** is stored encrypted in `controller.db`.
  `darkpanel controller backup` exports the database and the controller key
  into one archive encrypted with a key derived from an operator passphrase
  with argon2id; `restore` is the lost-controller path in the runbook (R81).

### 0006 API, listeners, authentication and CLI

- **Listeners.** Web and API: 127.0.0.1 and the Tailscale interface by default.
  A Tailscale bind uses `IP_FREEBIND`/`IPV6_FREEBIND`, so the listener exists
  before the address does; the service watches for address changes and
  rebinds, and with no Tailscale address configured the listener is loopback
  only and the UI says so. HTTPS with a `tailscale cert` certificate when the
  tailnet has HTTPS enabled, obtained by the local agent (root) and handed to
  the controller over the agent protocol; otherwise a certificate from the
  internal authority carrying the host's DNS name and Tailscale addresses as
  SANs, separate from the agent-listener certificate. Plain HTTP only on
  127.0.0.1. Agent listener: ADR 0003, no web routes. Local sockets: below.
- **Authentication boundary.** One middleware wraps every route on every
  listener and denies unless the route is in an explicit public list (login, and
  `/healthz`, which returns status only; its version and schema version require
  a session or token). It applies to every HTTP method. Sessions use Secure,
  HttpOnly, SameSite=Strict cookies with CSRF tokens; API tokens are scoped
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
- **Local access.** Each service gets its own runtime directory through systemd
  `RuntimeDirectory=`. The agent serves its handler on
  `/run/darkpanel-agent/agent.sock` (root, 0600) for every agent action, checked
  with `SO_PEERCRED` uid 0, with actor `local:root@<host id>`; entries are
  journaled and reach the audit log when the controller is reachable (R28). The
  controller serves `/run/darkpanel-controller/controller.sock` (0600,
  `darkpanel`, root allowed via `SO_PEERCRED`) for its version, schema version
  and health (used by the update health check, ADR 0007) and for the break-glass
  actions (ADR 0002): `darkpanel user create`, which creates the first admin
  (R15), and `darkpanel user reset-totp`. `darkpanel update rollback` needs
  neither service; run by root it re-executes `darkpanel.prev` (ADR 0007).

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
- **Update action.** An update is the agent action `core.update.apply` (class
  `compensable`, compensating action `core.update.rollback`), reachable over the
  agent protocol and over the agent's local socket. The agent starts the
  transaction as a root transient systemd unit through systemd's D-Bus
  `StartTransientUnit`, running the currently installed binary as `darkpanel
  update run <transaction id>`, independent of the services. The controller
  never swaps binaries; on its own host it updates through its local agent
  (R26).
- **Transaction record.** `/var/lib/darkpanel/update/<transaction id>.json`,
  written by the old binary with fsync at each state change: `staged`,
  `swapped` (with a deadline), `committed`, `rolled-back`. It follows the
  record format rule in ADR 0004.
- **Update transaction:**
  1. Refuse while any revertible change is unconfirmed on the host; take every
     lock key for the duration (locks survive the restart, ADR 0004).
  2. Download, verify signature, trusted comment and checksum; run the new
     binary's self-test. Downgrades are refused unless forced; a forced
     downgrade goes back at most one minor release.
  3. Record `swapped`, keep the current binary as `darkpanel.prev`, swap
     atomically, restart `darkpanel-controller` if present, then
     `darkpanel-agent`. Each service runs its migrations as it starts
     (ADR 0005).
  4. Health check within two minutes (S5): the controller reports the new
     version and schema version on its local socket; the agent reports the new
     version on its local socket and, when enrolled, a connected controller
     session. On the controller's host both must pass. On a pass, record
     `committed` and release the locks; on a failure or at the deadline, roll
     back.
- **Rollback** of a `swapped` transaction runs at most once: it swaps
  `darkpanel.prev` back, keeps the failed binary as `darkpanel.failed`,
  records `rolled-back` and restarts the services. For a `committed` or
  `rolled-back` record the automatic path does nothing. Both service units set
  `Restart=on-failure` with a `StartLimitBurst` that is exhausted well inside
  the two-minute window, and `OnFailure=darkpanel-rollback.service`. That unit,
  and `darkpanel-update-recover.service` (ordered before both services and run
  on every boot), execute `darkpanel.prev update rollback --pending`, so a
  reboot during the window always ends in a rollback and the operator re-runs
  the update.
- **Manual rollback.** `darkpanel update rollback`, typed by root, re-executes
  `darkpanel.prev` when it exists, so the binary that wrote the record reads
  it. After a commit it goes back one step, with the same refusal as step 1.
  On the controller's host it is refused while any enrolled agent reports a
  newer version, unless forced; a forced rollback marks those hosts outside the
  supported window until they are downgraded.
- **Stepping.** An update never crosses more than one minor release. From 1.2
  to 1.5 the agent runs three transactions (the latest patch of 1.3, then 1.4,
  then 1.5), each committed before the next; a failure leaves the host on the
  last committed minor. This keeps every step inside the schema window
  (ADR 0005) and the key-rotation window above.
- **Order.** On the controller's host one swap serves both roles: "the
  controller first" is the restart and health-check order, and if either role
  fails its check both roll back together. A fleet rollout is the controller
  issuing `core.update.apply` to one host at a time, its own host first; it
  stops at the first failure and stores progress so an interrupted rollout
  resumes.
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
  change, reboot during an update's health-check window, and ship a
  deliberately broken binary to the updater.
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
  the module manifest. It opens the parent directory with
  `openat2(RESOLVE_NO_SYMLINKS)`, which fails if any path component is a
  symlink, and requires that directory to be owned by root and not writable by
  others. It then creates a temporary file and renames it over the target
  relative to that directory descriptor, preserving the target's mode and
  owner. A file with duplicated, nested or unterminated `BEGIN/END DARKPANEL`
  markers is refused, never repaired by guessing. Ownership is recorded in the
  ownership manifest that `darkpanel uninstall` reverses (R30). Declared paths
  are fixed, so this defends against a symlink planted by an unprivileged
  process, not against root. `openat2` needs Linux 5.6; every supported host
  ships 5.15 or later.
- Prefer APIs over tools: Docker Engine API, Tailscale local API, systemd over
  D-Bus.

## Product spec amendments

This design changes five approved requirements; every edit is made with this
spec:

- **R10** now reads: mutating actions follow one change lifecycle; revertible
  actions store a snapshot sufficient to roll them back; compensable and
  irreversible actions are declared as such and irreversible ones require
  typed confirmation.
- **R31** now names the controller's local socket for controller break-glass
  commands, and `update rollback` as needing neither service.
- **R12** now reads: each role's key file is readable only by the service that
  uses it (root for the agent, `darkpanel` for the controller); ADR 0005
  records why a root-only key is unbuildable with an unprivileged controller.
- **R13** now says `/healthz` returns status to anyone and its version and
  schema version only to an authenticated caller.
- **R34** now reads: the backup taken before migrating is for disaster recovery;
  expand-then-contract migrations let the previous binary run on the migrated
  schema, so a rollback restores nothing. The Database row of the
  compatibility table says the same.

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
`cloudflared` run (M5, M6), the install script's verification method (M2), and
building `darkpanel controller backup` and `restore` (M8 Production rollout and
runbook).

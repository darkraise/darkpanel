# M1 epic: architecture decision records

**Date:** 2026-10-17
**Status:** design approved by the owner 2026-10-17
**Product spec:** docs/superpowers/specs/2026-10-17-darkpanel-design.md
**Requirement:** R1
**Execution strategy:** parallel — the eight ADRs are independent files that share nothing but the index, which a foundation part writes first (owner ruling 2026-10-17: favour parallel to reach v1 sooner).

## Goal

Record the decisions every later epic builds on, as numbered architecture
decision records (ADRs) in `docs/adr/`, before any implementation. Each ADR has
the sections Status, Context, Decision, Alternatives considered, Consequences.
An ADR is changed only by a new ADR that supersedes it.

## Deliverables

| File | Decision |
|---|---|
| `docs/adr/README.md` | Index, ADR format, supersession rule |
| `docs/adr/0001-stack-and-layout.md` | Stack and repository layout |
| `docs/adr/0002-module-contract.md` | Module contract and action versioning |
| `docs/adr/0003-agent-protocol.md` | Controller–agent protocol and enrollment |
| `docs/adr/0004-change-lifecycle.md` | Change lifecycle |
| `docs/adr/0005-storage-and-secrets.md` | Storage, migrations and secrets |
| `docs/adr/0006-api-and-cli.md` | API and CLI surface |
| `docs/adr/0007-release-signing.md` | Release signing and update verification |
| `docs/adr/0008-test-harness.md` | Integration test harness |

## Decisions

### 0001 Stack and layout

- Go 1.27 (pinned with the `toolchain` directive), CGO disabled, one binary
  `darkpanel` for `linux/amd64` and `linux/arm64`.
- HTTP with the standard library router; CLI with `github.com/spf13/cobra`.
- Web UI: Vite, React 19, darkraise-ui 6.x, TanStack Query, built to
  `web/dist` and embedded with `go:embed`. pnpm for the web workspace.
- Layout:

```
cmd/darkpanel/            entry point, role dispatch (serve, agent, CLI)
internal/core/            registry, actions, change lifecycle, audit, schema checks
internal/controller/      HTTP API, auth, database, fleet, scheduler
internal/agent/           agent runtime, local socket, journal, revert timers
internal/transport/       TLS, certificate authority, enrollment, yamux
internal/platform/        providers: exec (argv only), marked-block files, systemd, OS detection
internal/modules/<id>/    manifest, controller part, agent part, migrations
schemas/<action>/v<n>.json committed action schema snapshots
web/src/modules/<id>/     module UI pages
docs/adr/                 decision records
```

### 0002 Module contract and action versioning

- A module implements one Go interface returning a manifest: id, version,
  actions, resources, settings schema, preflight checks, embedded migrations.
  It may supply a controller part, an agent part, or both; the UI part is a
  route bundle registered in `web/src/modules/registry.ts`.
- Modules are compiled in and registered in one list in `internal/modules`;
  there is no runtime plugin loading in v1.
- An action has a name (`firewall.rule.add`), an integer version, typed Go input
  and output structs, and flags: read or mutate; runs on controller or agent;
  revertible (confirm-or-revert); break-glass (available on the local socket).
- Handlers are registered with generics so the input and output types are
  checked at compile time.
- JSON Schemas are generated from the Go types and committed under `schemas/`.
  A test regenerates them and fails when a published version changes in a
  non-additive way: a field removed or renamed, a type changed, an enum value
  removed, or a new required input field. A breaking change is a new version;
  the old version stays registered until the next major release.

### 0003 Controller–agent protocol and enrollment

- The agent dials the controller (default port 7443) with TLS 1.3 and a client
  certificate. The controller runs an internal Ed25519 certificate authority.
  Both sides verify the full chain to that authority; the controller also checks
  that the host id in the certificate's URI SAN exists and is not revoked.
- After the TLS handshake the connection carries a yamux session. The
  controller opens streams and sends HTTP/1.1 requests to the agent's
  `http.Handler`; the agent opens streams to push events to the controller.
- Agent endpoints: `GET /agent/v1/hello` (agent version, protocol version, OS,
  modules and their action versions), `POST /agent/v1/actions/{name}/v{n}/{plan|apply|confirm|revert}`,
  `GET /agent/v1/resources/{module}/{resource}`.
- The same handler is served on `/run/darkpanel/agent.sock` (root only, mode
  0600) for the local CLI and break-glass actions.
- Enrollment: the controller issues a single-use token valid for one hour that
  embeds the authority's fingerprint. The agent generates its own key, pins the
  fingerprint, and submits a certificate signing request with the token.
  Certificates last 90 days and are renewed automatically after two thirds of
  their life.
- Compatibility: unknown JSON fields are ignored; the controller dispatches
  only action versions listed in `hello`; it supports agents of the same and
  the previous minor version. Reconnects use exponential backoff with jitter.
- On the controller's host the agent is a separate systemd service connecting
  over loopback mTLS like any other host. In M1, before the transport exists,
  the controller calls the same handler in-process.

### 0004 Change lifecycle

- `plan` returns a change id, a human-readable and structured diff, guard
  results (for example the lockout check) and a hash of the observed host
  state. A plan expires after 10 minutes.
- `apply` refuses when the current state hash differs from the plan's. It writes
  a module-defined snapshot to `/var/lib/darkpanel/snapshots/<module>/<change>`
  before changing anything, then applies and runs the module's verify step.
- Revertible changes arm a revert deadline stored in the agent database. The
  agent reverts automatically if `confirm` does not arrive in time, including
  after an agent restart, and the timer runs on the agent so a lost client or
  controller cannot prevent it.
- One change at a time per module per host, enforced by the agent.
- Every plan, apply, confirm and revert is recorded in the agent's journal and
  forwarded to the controller's audit log.

### 0005 Storage, migrations and secrets

- SQLite through `modernc.org/sqlite`, write-ahead-log mode, one writer:
  `/var/lib/darkpanel/controller.db` and `/var/lib/darkpanel/agent.db`.
- Core tables use the prefix `core_`; each module uses `<module id>_`.
- Migrations are embedded SQL files per module, numbered, forward-only, recorded
  in `core_migrations` with a checksum. A changed checksum stops startup.
- Before any migration the database is copied with `VACUUM INTO` to
  `/var/lib/darkpanel/backups/`, keeping the last five. Rolling back an update
  restores that copy; there are no down migrations.
- Secrets are encrypted with XChaCha20-Poly1305 under a 32-byte key in
  `/etc/darkpanel/secret.key` (root, mode 0600), and are write-only through the
  API.

### 0006 API and CLI surface

- REST under `/api/v1`: hosts; module resources; actions at
  `/api/v1/hosts/{host}/actions/{name}/v{n}:plan` and `:apply`, and
  controller-side actions at `/api/v1/actions/{name}/v{n}:plan` and `:apply`;
  changes at `/api/v1/changes/{id}:confirm` and `:revert`; audit; auth.
- Live updates over server-sent events at `/api/v1/events`.
- An OpenAPI 3.1 document is generated from the registry at
  `/api/v1/openapi.json`.
- Errors use RFC 9457 problem details with a stable `code` field.
- `/api/v1` follows the additive-only rule of ADR 0002; `/api/v2` appears only
  in a major release.
- The CLI is generated from the registry: `darkpanel <module> <action>` with
  flags from the input schema, plus `--host`, `--plan`, `--yes` and `--json`.
  It talks to the controller with an API token, or to the local agent socket
  with `--local`.

### 0007 Release signing and update verification

- GitHub Actions builds release artifacts and `checksums.txt` (SHA-256). A
  separate job runs in the protected GitHub environment `release`, which needs
  the owner's approval, and signs `checksums.txt` with minisign
  (`aead.dev/minisign` for verification).
- The binary embeds a list of trusted public keys. A new key is introduced by a
  release signed with a key already trusted; a key is retired the same way.
- The updater verifies the signature, then the artifact's checksum, and refuses
  downgrades unless forced.
- Releases are published on GitHub Releases of `github.com/Darkraise/darkpanel`.
- The install script must verify the signature rather than trust TLS alone; the
  method is decided in the M2 install epic.

### 0008 Integration test harness

- Integration tests run in QEMU/KVM virtual machines on GitHub Actions Linux
  runners, booted from official cloud images (Ubuntu 22.04 and 24.04, Debian 12
  and 13) with cloud-init, one operating system per matrix job.
- A separate scanner VM on the same virtual network probes the system under
  test from outside, so exposure results are checked against real
  reachability.
- The development host cannot run these VMs (it is an LXC container without
  `/dev/kvm`); local runs need a KVM-capable machine. The harness design is the
  integration test harness epic's job.

## Parallel decomposition

| Part | Wave | Owns | Consumes |
|---|---|---|---|
| Foundation | 0 | `docs/adr/README.md` (index of all eight ADRs with final numbers and titles, ADR template, supersession rule) | this spec |
| ADR 0001 | 1 | `docs/adr/0001-stack-and-layout.md` | README template |
| ADR 0002 | 1 | `docs/adr/0002-module-contract.md` | README template |
| ADR 0003 | 1 | `docs/adr/0003-agent-protocol.md` | README template |
| ADR 0004 | 1 | `docs/adr/0004-change-lifecycle.md` | README template |
| ADR 0005 | 1 | `docs/adr/0005-storage-and-secrets.md` | README template |
| ADR 0006 | 1 | `docs/adr/0006-api-and-cli.md` | README template |
| ADR 0007 | 1 | `docs/adr/0007-release-signing.md` | README template |
| ADR 0008 | 1 | `docs/adr/0008-test-harness.md` | README template |

All wave-1 parts may run together. ADRs refer to each other only by the
number and title fixed in the README, never by content written in the same
wave. No part edits another part's file; the index is not touched in wave 1.
There are no tests, ports or shared runtime state.

## Verification

- Each ADR file exists with the five sections and matches the decision above.
- `docs/adr/README.md` lists all eight.
- Every decision named in R1 (module contract, controller–agent protocol,
  change lifecycle, storage and migrations, API versioning, release signing)
  maps to an ADR.

## Out of scope

Implementation of any decision; those belong to the M1 and M2 epics that
consume these ADRs.

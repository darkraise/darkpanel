# Architecture decision records

Decisions every Darkpanel epic builds on. Source design:
`docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md`.
Check the files with `bash scripts/check-adrs.sh --complete`.

## Index

- [ADR 0001: Stack, layout and process model](0001-stack-and-layout.md)
- [ADR 0002: Module contract and compatibility](0002-module-contract.md)
- [ADR 0003: Controller–agent protocol, identity and
  enrollment](0003-agent-protocol.md)
- [ADR 0004: Change lifecycle, locking and recovery](0004-change-lifecycle.md)
- [ADR 0005: Storage, migrations, secrets and keys](0005-storage-and-secrets.md)
- [ADR 0006: API, listeners, authentication and CLI](0006-api-and-cli.md)
- [ADR 0007: Release, signing, update and rollback](0007-release-and-update.md)
- [ADR 0008: Integration test harness](0008-test-harness.md)
- [ADR 0009: Host execution safety](0009-host-execution-safety.md)

## Format

Every ADR starts with `# ADR NNNN: <title>`, then a `**Requirements:**` line
naming the product-spec requirements it serves, then the sections Status,
Context, Decision, Alternatives considered and Consequences, in that order.

## Lifecycle

- `Proposed`: written, may still be corrected while its epic is open.
- `Accepted`: set when the epic that wrote it closes. From then on the ADR is
  not edited; a change needs a new ADR.
- `Superseded by ADR NNNN`: the newer ADR replaces it; the old file stays.

## Shared contracts

Every ADR uses these definitions and does not redefine them.

- **Names.** Action names are `<module>.<noun>.<verb>` in lower case, for
  example `firewall.rule.add`. Versions are positive integers written `v1`.
  Host ids and change ids are ULIDs. JSON fields are camelCase.
- **Schemas.** JSON Schema draft 2020-12, generated from Go types with
  `github.com/invopop/jsonschema`. The struct tag `secret:"true"` marks a field
  that is encrypted at rest wherever it is stored, including inside plan
  records and snapshots, is write-only through the API, and is redacted from
  logs, audit, diffs, events and errors.
- **Action classes.** Every action is exactly one of:
  - `read`: changes nothing.
  - `revertible`: stores a snapshot and arms a confirm-or-revert timer.
  - `compensable`: undone by a named compensating action; no timer.
  - `irreversible`: cannot be undone (for example pruning volumes); requires
    typed confirmation.
- **Errors.** RFC 9457 problem details with a stable `code` field, from the
  registry in `internal/core/errcode`.
- **Actors.** Audit actors are `user:<id>`, `token:<id>`, `agent:<host id>`,
  `local:root@<host id>` and `system:<job>`.
- **Version skew.** The controller is never older than its agents. It supports
  agents of the same and the previous minor version, is updated first, and
  drives agent updates up to its own version.

## Deferred decisions

- How DERP servers run (container or systemd service): M5 DERP epic.
- How `cloudflared` runs: M6 Cloudflare Tunnel epic.
- How the install script verifies a download (it must not trust TLS alone):
  M2 install epic.
- `darkpanel controller backup` and `restore` (ADR 0005) are built by the M8
  Production rollout and runbook epic.

## Traceability

Product-spec requirements that depend on these decisions, with the part of
each ADR's Decision section that serves them. `none` means the requirement is
decided by its own epic, not by an ADR.

| Requirement | ADRs (Decision part) |
|---|---|
| R1 | 0002 (module contract), 0003 (protocol), 0004 (change lifecycle), 0005 (storage and migrations), 0006 (API versioning), 0007 (release signing) |
| R5 | 0002 (manifest interface, compiled-in modules) |
| R6 | 0002 (compatibility rules) |
| R7 | 0002 (action declaration), 0006 (REST, CLI) |
| R8 | 0005 (migrations) |
| R9 | 0002 (enablement and preflight) |
| R10 | 0004 (plan, apply ordering, compensable and irreversible actions) |
| R11 | 0009 (commands, files) |
| R12 | 0005 (secrets), 0006 (authentication boundary) |
| R15, R16, R17 | 0006 (authentication boundary) |
| R18 | 0003 (journal delivery), 0004 (apply ordering, compensable and irreversible actions, controller-side actions) |
| R19 | none: a stored role field, decided by the identity epic |
| R20 | 0006 (listeners) |
| R21 | 0008 (harness) |
| R22 | 0003 (identity, transport) |
| R23 | 0003 (enrollment, certificates and revocation) |
| R24 | 0002 (schema hashes), 0003 (hello) |
| R25 | 0003 (hello) |
| R26 | 0001 (process model), 0003 (transport), 0007 (update action) |
| R27 | 0003 (hello, liveness) |
| R28 | 0003 (journal delivery), 0004 (startup recovery), 0006 (local access) |
| R29 | 0007 (release hosting; method deferred to the M2 install epic) |
| R30 | 0009 (files) |
| R31 | 0006 (local access), 0007 (rollback) |
| R32 | 0007 (signing, keys) |
| R33 | 0007 (update action, update transaction, rollback) |
| R34 | 0005 (migrations, expand then contract, backups), 0007 (update transaction, stepping) |
| R35 | 0007 (trusted comment carries the channel; scheduling is decided by the self-update epic) |
| R36 | 0007 (order and rollouts) |

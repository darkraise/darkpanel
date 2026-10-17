# Architecture decision records

Decisions every Darkpanel epic builds on. Source design:
`docs/superpowers/specs/2026-10-17-m1-architecture-decisions-design.md`.
Check the files with `bash scripts/check-adrs.sh`.

## Index

- [ADR 0001: Stack, layout and process model](0001-stack-and-layout.md)
- [ADR 0002: Module contract and compatibility](0002-module-contract.md)
- [ADR 0003: Controller–agent protocol, identity and enrollment](0003-agent-protocol.md)
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
  that is encrypted at rest, write-only through the API, and redacted from
  logs, audit, snapshots, diffs, events and errors.
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
